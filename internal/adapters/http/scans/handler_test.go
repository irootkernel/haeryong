package scans

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/scan"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

type fakeScanStore struct {
	app.Store
	mu          sync.Mutex
	authState   domain.AuthState
	libraryID   string
	config      domain.Configuration
	sessions    map[string]domain.Session
	sessionHash map[string]string
	credentials map[string]domain.ScopedCredential
	principals  map[string]domain.WorkflowPrincipal
	scopes      map[string]domain.ScopeState
	documents   map[string]domain.Document
	versions    map[string]domain.Version
	receipts    map[string]domain.Receipt
	jobs        map[string]domain.Job
	runs        map[string]domain.ScanRun
	rosters     map[string][]domain.ScanCapturedVersion
	targetSets  map[string]domain.TargetSet
	findings    map[string][]domain.Finding
}

func newFakeScanStore() *fakeScanStore {
	cfgRaw, _ := json.Marshal(map[string]any{
		"maintenance": map[string]any{
			"scope":                         "selected_projects",
			"project_ids":                   []string{"11111111-1111-4111-8111-111111111111"},
			"depth":                         "change_focused",
			"missed_runs":                   "coalesce_one",
			"max_concurrent_ai_jobs":        1,
			"max_provider_attempts_per_run": 8,
			"max_run_seconds":               1800,
			"auto_apply": map[string]any{
				"backlink_index_repair": "auto",
				"related_explanation":   "propose",
				"topic_assignment":      "propose",
				"freshness_assessment":  "auto",
			},
			"content_generation": "propose",
		},
		"projects": map[string]any{
			"11111111-1111-4111-8111-111111111111": map[string]any{
				"post_work_scan_enabled": true,
				"source_mode_default":    "pinned",
			},
		},
	})
	projID := "11111111-1111-4111-8111-111111111111"
	docID := "22222222-2222-4222-8222-222222222222"
	verID := "33333333-3333-4333-8333-333333333333"
	return &fakeScanStore{
		authState: domain.AuthState{
			LibraryID: "lib-test-1",
			OwnerID:   "owner-1",
			AuthEpoch: "epoch-1",
		},
		libraryID:   "lib-test-1",
		config:      domain.Configuration{Revision: 1, Value: cfgRaw},
		sessions:    map[string]domain.Session{},
		sessionHash: map[string]string{},
		credentials: map[string]domain.ScopedCredential{},
		principals:  map[string]domain.WorkflowPrincipal{},
		scopes: map[string]domain.ScopeState{
			projID: {
				Project: domain.Project{
					ID:                  projID,
					Name:                "Alpha",
					Lifecycle:           "active",
					PermissionsRevision: 1,
				},
				ConfigRevision: 1,
			},
		},
		documents: map[string]domain.Document{
			docID: {
				ID:              docID,
				ProjectID:       projID,
				Title:           "Doc One",
				Lifecycle:       "active",
				LatestVersionID: verID,
			},
		},
		versions: map[string]domain.Version{
			verID: {
				ID:           verID,
				DocumentID:   docID,
				ContentSetID: "cs-1",
				ManifestSHA:  "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			},
		},
		receipts:   map[string]domain.Receipt{},
		jobs:       map[string]domain.Job{},
		runs:       map[string]domain.ScanRun{},
		rosters:    map[string][]domain.ScanCapturedVersion{},
		targetSets: map[string]domain.TargetSet{},
		findings:   map[string][]domain.Finding{},
	}
}

func (f *fakeScanStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fn(&fakeScanTx{store: f})
}

func (f *fakeScanStore) addOwnerSession(token string) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	s := domain.Session{
		ID:          "sess-owner-1",
		PrincipalID: "owner-1",
		LibraryID:   f.libraryID,
		Scope:       domain.CredentialScope{Kind: "owner"},
		State:       "active",
		CreatedAt:   now,
		LastSeenAt:  now,
		TokenHash:   sec.Verifier("session", token),
		AuthEpoch:   f.authState.AuthEpoch,
		Revision:    1,
	}
	f.sessions[s.ID] = s
	f.sessionHash[s.TokenHash] = s.ID
}

func (f *fakeScanStore) addWorkflowCredential(token string, projID string, actions []string) {
	f.addWorkflowCredentialWithPrincipal("wp-1", "bind-1", "cred-1", token, projID, actions)
}

func (f *fakeScanStore) addWorkflowCredentialWithPrincipal(principalID, bindingID, credID, token, projID string, actions []string) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	scope := domain.CredentialScope{
		Kind:       "workflow",
		Actions:    actions,
		ProjectIDs: []string{projID},
		VersionIDs: []string{"33333333-3333-4333-8333-333333333333"},
	}
	wp := domain.WorkflowPrincipal{
		ID:        principalID,
		BindingID: bindingID,
		Active:    true,
		Scope:     scope,
		Revision:  1,
	}
	f.principals[wp.ID] = wp
	cred := domain.ScopedCredential{
		ID:                credID,
		PrincipalID:       wp.ID,
		TokenHash:         sec.Verifier("workflow", token),
		AuthEpoch:         f.authState.AuthEpoch,
		Scope:             scope,
		MutationProjectID: projID,
		Permissions:       map[string]int64{projID: 1},
		ConfigRevision:    f.config.Revision,
		CreatedAt:         now,
		Revision:          1,
	}
	f.credentials[cred.TokenHash] = cred
}

type fakeScanTx struct {
	app.WriteTx
	store *fakeScanStore
}

func (t *fakeScanTx) AuthState() (domain.AuthState, error) {
	return t.store.authState, nil
}
func (t *fakeScanTx) Library() (domain.Library, error) {
	return domain.Library{
		ID:               t.store.libraryID,
		AuthEpoch:        t.store.authState.AuthEpoch,
		UnfiledProjectID: "11111111-1111-4111-8111-111111111111",
	}, nil
}
func (t *fakeScanTx) RecoveryHold() (*domain.RecoveryHold, error) {
	return nil, nil
}
func (t *fakeScanTx) Configuration() (domain.Configuration, error) {
	return t.store.config, nil
}
func (t *fakeScanTx) SessionByHash(hash string) (domain.Session, error) {
	id, ok := t.store.sessionHash[hash]
	if !ok {
		return domain.Session{}, domain.ErrNotFound
	}
	return t.store.sessions[id], nil
}
func (t *fakeScanTx) SessionByID(id string) (domain.Session, error) {
	s, ok := t.store.sessions[id]
	if !ok {
		return domain.Session{}, domain.ErrNotFound
	}
	return s, nil
}
func (t *fakeScanTx) PutSession(s domain.Session) error {
	t.store.sessions[s.ID] = s
	return nil
}
func (t *fakeScanTx) ScopedCredentialByHash(hash string) (domain.ScopedCredential, error) {
	c, ok := t.store.credentials[hash]
	if !ok {
		return domain.ScopedCredential{}, domain.ErrNotFound
	}
	return c, nil
}
func (t *fakeScanTx) WorkflowPrincipal(id string) (domain.WorkflowPrincipal, error) {
	p, ok := t.store.principals[id]
	if !ok {
		return domain.WorkflowPrincipal{}, domain.ErrNotFound
	}
	return p, nil
}
func (t *fakeScanTx) WorkflowSelection(bindingID string) (domain.WorkflowSelection, error) {
	for _, p := range t.store.principals {
		if p.BindingID == bindingID {
			var pid string
			if len(p.Scope.ProjectIDs) > 0 {
				pid = p.Scope.ProjectIDs[0]
			}
			return domain.WorkflowSelection{
				BindingID:   bindingID,
				PrincipalID: p.ID,
				ProjectID:   pid,
				Revision:    1,
			}, nil
		}
	}
	return domain.WorkflowSelection{}, domain.ErrNotFound
}
func (t *fakeScanTx) SourceCaptures(projectID string) ([]domain.TargetSet, error) {
	return nil, nil
}
func (t *fakeScanTx) VerificationCaptures(versionID, projectID string) ([]domain.TargetSet, error) {
	return nil, nil
}
func (t *fakeScanTx) QueryEntry(contentSetID, path string) (domain.Entry, error) {
	return domain.Entry{}, domain.ErrNotFound
}
func (t *fakeScanTx) Scope(id string) (domain.ScopeState, error) {
	sv, ok := t.store.scopes[id]
	if !ok {
		return domain.ScopeState{}, domain.ErrNotFound
	}
	return sv, nil
}
func (t *fakeScanTx) QueryProjects(scope domain.CredentialScope, afterName, afterID string, limit int) ([]domain.QueryProject, error) {
	if afterID != "" {
		return nil, nil
	}
	var out []domain.QueryProject
	for _, sv := range t.store.scopes {
		out = append(out, domain.QueryProject{Project: sv.Project})
	}
	return out, nil
}
func (t *fakeScanTx) ProjectDocumentsAll(projectID string) ([]domain.Document, error) {
	var out []domain.Document
	for _, d := range t.store.documents {
		if d.ProjectID == projectID {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (t *fakeScanTx) Document(id string) (domain.Document, error) {
	d, ok := t.store.documents[id]
	if !ok {
		return domain.Document{}, domain.ErrNotFound
	}
	return d, nil
}
func (t *fakeScanTx) QueryDocument(id string) (domain.QueryDocument, error) {
	d, ok := t.store.documents[id]
	if !ok {
		return domain.QueryDocument{}, domain.ErrNotFound
	}
	return domain.QueryDocument{Document: d}, nil
}
func (t *fakeScanTx) PublishedVersion(id string) (domain.Version, error) {
	v, ok := t.store.versions[id]
	if !ok {
		return domain.Version{}, domain.ErrNotFound
	}
	return v, nil
}
func (t *fakeScanTx) CountVersionInventory(versionID, contentSetID, pathPrefix, regionID string) (int, int, int, int, error) {
	return 1, 0, 1, 0, nil
}
func (t *fakeScanTx) VersionSources(versionID string) ([]domain.Source, error) {
	return nil, nil
}
func (t *fakeScanTx) Object(hash string) (domain.Object, error) {
	return domain.Object{}, domain.ErrNotFound
}
func (t *fakeScanTx) AcquirePin(pin domain.Pin) error {
	return nil
}
func (t *fakeScanTx) Receipt(k domain.OperationKey) (domain.Receipt, error) {
	r, ok := t.store.receipts[k.PrincipalID+"|"+k.OperationID]
	if !ok {
		return domain.Receipt{}, domain.ErrNotFound
	}
	return r, nil
}
func (t *fakeScanTx) ReserveOperation(req domain.Request, normalize func() ([]byte, error)) (domain.Receipt, bool, error) {
	var norm []byte
	if normalize != nil {
		var err error
		norm, err = normalize()
		if err != nil {
			return domain.Receipt{}, false, err
		}
	}
	fp, err := app.Fingerprint(req, norm)
	if err != nil {
		return domain.Receipt{}, false, err
	}
	key := req.Key.PrincipalID + "|" + req.Key.OperationID
	if existing, ok := t.store.receipts[key]; ok {
		if existing.Fingerprint != fp {
			return domain.Receipt{}, false, domain.ErrConflict
		}
		return existing, true, nil
	}
	rec := domain.Receipt{
		Key:           req.Key,
		Fingerprint:   fp,
		Original:      req,
		Normalization: norm,
		State:         "pending",
	}
	t.store.receipts[key] = rec
	return rec, false, nil
}
func (t *fakeScanTx) CompleteOperation(k domain.OperationKey, res domain.Response) error {
	key := k.PrincipalID + "|" + k.OperationID
	rec, ok := t.store.receipts[key]
	if !ok {
		return domain.ErrNotFound
	}
	resCopy := res
	rec.State = "completed"
	rec.Response = &resCopy
	t.store.receipts[key] = rec
	return nil
}
func (t *fakeScanTx) ActiveCoalescedScan(scopeHash string) (domain.Job, bool, error) {
	for jobID, run := range t.store.runs {
		if run.ScopeHash == scopeHash {
			j := t.store.jobs[jobID]
			if j.State == "" || j.State == "queued" || j.State == "running" || j.State == "interrupted" {
				return j, true, nil
			}
		}
	}
	return domain.Job{}, false, nil
}
func (t *fakeScanTx) ScanRun(jobID string) (domain.ScanRun, error) {
	run, ok := t.store.runs[jobID]
	if !ok {
		return domain.ScanRun{}, domain.ErrNotFound
	}
	return run, nil
}
func (t *fakeScanTx) PutTargetSet(ts domain.TargetSet) error {
	t.store.targetSets[ts.ID] = ts
	return nil
}
func (t *fakeScanTx) CreateJob(j domain.Job) error {
	if j.State == "" {
		j.State = "queued"
	}
	j.Revision = 1
	t.store.jobs[j.ID] = j
	return nil
}
func (t *fakeScanTx) CreateScanRun(run domain.ScanRun, roster []domain.ScanCapturedVersion) error {
	t.store.runs[run.JobID] = run
	t.store.rosters[run.JobID] = roster
	return nil
}
func (t *fakeScanTx) ScanCapturedVersions(jobID string, afterOrdinal int, limit int) ([]domain.ScanCapturedVersion, error) {
	if afterOrdinal > 0 {
		return nil, nil
	}
	return t.store.rosters[jobID], nil
}
func (t *fakeScanTx) Version(id string) (domain.Version, error) {
	return t.PublishedVersion(id)
}
func (t *fakeScanTx) Job(id string) (domain.Job, error) {
	j, ok := t.store.jobs[id]
	if !ok {
		return domain.Job{}, domain.ErrNotFound
	}
	return j, nil
}
func (t *fakeScanTx) Jobs() ([]domain.Job, error) {
	out := make([]domain.Job, 0, len(t.store.jobs))
	for _, j := range t.store.jobs {
		out = append(out, j)
	}
	return out, nil
}
func (t *fakeScanTx) CancelJob(id string) (domain.Job, error) {
	j, ok := t.store.jobs[id]
	if !ok {
		return domain.Job{}, domain.ErrNotFound
	}
	j.State = "cancelled"
	j.Revision++
	t.store.jobs[id] = j
	return j, nil
}
func (t *fakeScanTx) EventHead() (string, int64, error) {
	return "ep-1", 1, nil
}
func (t *fakeScanTx) Events(epoch string, after int64, limit int) ([]domain.Event, error) {
	var out []domain.Event
	seq := int64(1)
	for id, j := range t.store.jobs {
		out = append(out, domain.Event{
			StreamEpoch:  "ep-1",
			Sequence:     seq,
			ResourceKind: "job",
			ResourceID:   id,
			Kind:         "job.updated",
			Revision:     j.Revision,
			Body:         json.RawMessage(`{"state":"` + j.State + `"}`),
		})
		seq++
	}
	return out, nil
}
func (t *fakeScanTx) JobFindings(filter domain.FindingFilter, afterCreatedAt, afterID string) ([]domain.Finding, error) {
	var out []domain.Finding
	for _, f := range t.store.findings[filter.JobID] {
		if filter.Kind != "" && f.Kind != filter.Kind {
			continue
		}
		if filter.Status != "" && f.Status != filter.Status {
			continue
		}
		if afterCreatedAt != "" || afterID != "" {
			if f.CreatedAt < afterCreatedAt || (f.CreatedAt == afterCreatedAt && f.ID <= afterID) {
				continue
			}
		}
		out = append(out, f)
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out, nil
}

var _ = It("handles POST /api/v1/scans receipts, coalescing, unknown-field rejection, and premature finding_id 503", Label("integration"), func() {
	store := newFakeScanStore()
	ownerToken, err := sec.Secret()
	Expect(err).NotTo(HaveOccurred())
	store.addOwnerSession(ownerToken)

	authCore := appauth.New(store)
	scanCore := scan.New(store, authCore, nil, nil)
	h := New(scanCore)

	// 1. Premature finding_id or expected_finding_revision returns 503 FEATURE_NOT_READY
	key1 := "aaaaaaaa-1111-4111-8111-111111111111"
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/scans", bytes.NewBufferString(`{"finding_id":"ffffffff-1111-4111-8111-111111111111"}`))
	req1.Header.Set("Authorization", "Bearer "+ownerToken)
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Idempotency-Key", key1)
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	Expect(rec1.Code).To(Equal(http.StatusServiceUnavailable))
	Expect(rec1.Body.String()).To(ContainSubstring("FEATURE_NOT_READY"))
	Expect(rec1.Body.String()).To(ContainSubstring("finding_reassessment_not_ready"))

	req1b := httptest.NewRequest(http.MethodPost, "/api/v1/scans", bytes.NewBufferString(`{"expected_finding_revision":1}`))
	req1b.Header.Set("Authorization", "Bearer "+ownerToken)
	req1b.Header.Set("Content-Type", "application/json")
	req1b.Header.Set("Idempotency-Key", key1)
	rec1b := httptest.NewRecorder()
	h.ServeHTTP(rec1b, req1b)
	Expect(rec1b.Code).To(Equal(http.StatusServiceUnavailable))
	Expect(rec1b.Body.String()).To(ContainSubstring("FEATURE_NOT_READY"))
	Expect(rec1b.Body.String()).To(ContainSubstring("finding_reassessment_not_ready"))

	// 2. Forbidden max_files_inspect or duplicate keys return 400 INVALID_REQUEST
	reqBad := httptest.NewRequest(http.MethodPost, "/api/v1/scans", bytes.NewBufferString(`{"max_files_inspect":10}`))
	reqBad.Header.Set("Authorization", "Bearer "+ownerToken)
	reqBad.Header.Set("Content-Type", "application/json")
	reqBad.Header.Set("Idempotency-Key", key1)
	recBad := httptest.NewRecorder()
	h.ServeHTTP(recBad, reqBad)
	Expect(recBad.Code).To(Equal(http.StatusBadRequest))

	// 3. Valid POST /api/v1/scans creates a scan job and records a 202 receipt
	key2 := "bbbbbbbb-2222-4222-8222-222222222222"
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/scans", bytes.NewBufferString(`{"project_id":"11111111-1111-4111-8111-111111111111","depth":"change_focused"}`))
	req2.Header.Set("Authorization", "Bearer "+ownerToken)
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", key2)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	Expect(rec2.Code).To(Equal(http.StatusAccepted))
	var env2 struct {
		Data struct {
			OperationID string `json:"operation_id"`
			JobID       string `json:"job_id"`
			Coalesced   bool   `json:"coalesced"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(rec2.Body.Bytes(), &env2)).To(Succeed())
	Expect(env2.Data.OperationID).To(Equal(key2))
	Expect(env2.Data.JobID).NotTo(BeEmpty())
	Expect(env2.Data.Coalesced).To(BeFalse())

	// 4. Equivalent fresh-key request coalesces onto the active scan job
	key3 := "cccccccc-3333-4333-8333-333333333333"
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/scans", bytes.NewBufferString(`{"project_ids":["11111111-1111-4111-8111-111111111111"],"depth":"change-focused"}`))
	req3.Header.Set("Authorization", "Bearer "+ownerToken)
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("Idempotency-Key", key3)
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	Expect(rec3.Code).To(Equal(http.StatusAccepted))
	var env3 struct {
		Data struct {
			OperationID string `json:"operation_id"`
			JobID       string `json:"job_id"`
			Coalesced   bool   `json:"coalesced"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(rec3.Body.Bytes(), &env3)).To(Succeed())
	Expect(env3.Data.OperationID).To(Equal(key3))
	Expect(env3.Data.JobID).To(Equal(env2.Data.JobID))
	Expect(env3.Data.Coalesced).To(BeTrue())
})

var _ = It("allows derived workflow scan.request on POST /api/v1/scans while keeping GET /api/v1/jobs/{id}/findings owner-only", Label("integration"), func() {
	store := newFakeScanStore()
	ownerToken, err := sec.Secret()
	Expect(err).NotTo(HaveOccurred())
	store.addOwnerSession(ownerToken)

	wfToken, err := sec.Secret()
	Expect(err).NotTo(HaveOccurred())
	projID := "11111111-1111-4111-8111-111111111111"
	store.addWorkflowCredential(wfToken, projID, []string{"scan.request", "job.read", "job.cancel", "version.read", "project.read"})

	authCore := appauth.New(store)
	scanCore := scan.New(store, authCore, nil, nil)
	h := New(scanCore)

	// Workflow credential can admit a scoped scan
	wfKey := "dddddddd-4444-4444-8444-444444444444"
	reqScan := httptest.NewRequest(http.MethodPost, "/api/v1/scans", bytes.NewBufferString(`{"project_id":"11111111-1111-4111-8111-111111111111"}`))
	reqScan.Header.Set("Authorization", "Bearer "+wfToken)
	reqScan.Header.Set("Content-Type", "application/json")
	reqScan.Header.Set("Idempotency-Key", wfKey)
	recScan := httptest.NewRecorder()
	h.ServeHTTP(recScan, reqScan)
	Expect(recScan.Code).To(Equal(http.StatusAccepted))
	var scanData struct {
		Data struct {
			JobID string `json:"job_id"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(recScan.Body.Bytes(), &scanData)).To(Succeed())
	jobID := scanData.Data.JobID
	Expect(jobID).NotTo(BeEmpty())

	// Seed a finding on that job
	f1 := domain.Finding{
		ID:                     "eeeeeeee-5555-4555-8555-555555555555",
		JobID:                  jobID,
		Kind:                   "backlink_index_repair",
		Origin:                 "deterministic",
		ProjectID:              projID,
		Evidence:               json.RawMessage(`{"missing":"1"}`),
		EvidenceFingerprint:    "fp-1",
		Rationale:              "Reference index diverged from authored declarations.",
		Uncertainty:            "low",
		CapturedConfigRevision: 1,
		PermissionsRevision:    1,
		Proposal:               json.RawMessage(`{"action":"reindex_references"}`),
		Status:                 "proposed",
		ApprovalRequired:       true,
		AppliedRef:             json.RawMessage(`null`),
		Revision:               1,
		ETag:                   `"finding-eeeeeeee-5555-4555-8555-555555555555-r1"`,
		CreatedAt:              "2026-11-08T00:00:00Z",
		UpdatedAt:              "2026-11-08T00:00:00Z",
	}
	f2 := f1
	f2.ID = "eeeeeeee-6666-4666-8666-666666666666"
	f2.EvidenceFingerprint = "fp-2"
	f2.CreatedAt = "2026-11-08T00:00:01Z"
	f2.UpdatedAt = "2026-11-08T00:00:01Z"
	store.findings[jobID] = []domain.Finding{f1, f2}

	// Workflow credential is rejected with 403 FORBIDDEN on GET /api/v1/jobs/{id}/findings
	reqWF := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID+"/findings", nil)
	reqWF.Header.Set("Authorization", "Bearer "+wfToken)
	recWF := httptest.NewRecorder()
	h.ServeHTTP(recWF, reqWF)
	Expect(recWF.Code).To(Equal(http.StatusForbidden))

	// Owner session can list findings with limit=1 + cursor pagination and receives a strong ETag + 304 Not Modified on If-None-Match
	reqOwner := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID+"/findings?kind=backlink_index_repair&status=proposed&limit=1", nil)
	reqOwner.Header.Set("Authorization", "Bearer "+ownerToken)
	recOwner := httptest.NewRecorder()
	h.ServeHTTP(recOwner, reqOwner)
	Expect(recOwner.Code).To(Equal(http.StatusOK))
	etag := recOwner.Header().Get("ETag")
	Expect(etag).To(HavePrefix(`"sha256-`))

	var fPage struct {
		Data struct {
			Items      []domain.Finding `json:"items"`
			NextCursor *string          `json:"next_cursor"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(recOwner.Body.Bytes(), &fPage)).To(Succeed())
	Expect(fPage.Data.Items).To(HaveLen(1))
	Expect(fPage.Data.Items[0].ID).To(Equal(f1.ID))
	Expect(fPage.Data.NextCursor).NotTo(BeNil())

	reqOwner2 := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID+"/findings?kind=backlink_index_repair&status=proposed&limit=1&cursor="+*fPage.Data.NextCursor, nil)
	reqOwner2.Header.Set("Authorization", "Bearer "+ownerToken)
	recOwner2 := httptest.NewRecorder()
	h.ServeHTTP(recOwner2, reqOwner2)
	Expect(recOwner2.Code).To(Equal(http.StatusOK))
	var fPage2 struct {
		Data struct {
			Items      []domain.Finding `json:"items"`
			NextCursor *string          `json:"next_cursor"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(recOwner2.Body.Bytes(), &fPage2)).To(Succeed())
	Expect(fPage2.Data.Items).To(HaveLen(1))
	Expect(fPage2.Data.Items[0].ID).To(Equal(f2.ID))
	Expect(fPage2.Data.NextCursor).To(BeNil())

	req304 := httptest.NewRequest(http.MethodHead, "/api/v1/jobs/"+jobID+"/findings?kind=backlink_index_repair&status=proposed&limit=1", nil)
	req304.Header.Set("Authorization", "Bearer "+ownerToken)
	req304.Header.Set("If-None-Match", etag)
	rec304 := httptest.NewRecorder()
	h.ServeHTTP(rec304, req304)
	Expect(rec304.Code).To(Equal(http.StatusNotModified))

	// Workflow baseline: can read its own scan job, list it, observe its job event, and read its operation receipt while verA is in scope
	ctx := context.Background()
	wfJob, err := authCore.Job(ctx, wfToken, jobID)
	Expect(err).NotTo(HaveOccurred())
	Expect(wfJob.ID).To(Equal(jobID))
	wfJobs, err := authCore.Jobs(ctx, wfToken)
	Expect(err).NotTo(HaveOccurred())
	Expect(wfJobs).To(HaveLen(1))
	wfEvents, err := authCore.Events(ctx, wfToken, "ep-1", 0)
	Expect(err).NotTo(HaveOccurred())
	Expect(wfEvents).To(HaveLen(1))
	Expect(wfEvents[0].ResourceKind).To(Equal("job"))
	Expect(wfEvents[0].ResourceID).To(Equal(jobID))
	wfReceipt, err := authCore.Operation(ctx, wfToken, wfKey)
	Expect(err).NotTo(HaveOccurred())
	Expect(wfReceipt.Key.OperationID).To(Equal(wfKey))

	// Narrow workflow principal scope from verA to verB while retaining projID and job.read/job.cancel:
	// old scan jobA must become 404 NotFound across Job, Jobs, Events/FilterEvent, Operation, and CancelJob
	wp := store.principals["wp-1"]
	wp.Scope.VersionIDs = []string{"99999999-9999-4999-8999-999999999999"}
	store.principals["wp-1"] = wp

	_, err = authCore.Job(ctx, wfToken, jobID)
	Expect(err).To(MatchError(domain.ErrNotFound))
	wfJobsAfter, err := authCore.Jobs(ctx, wfToken)
	Expect(err).NotTo(HaveOccurred())
	Expect(wfJobsAfter).To(BeEmpty())
	wfEventsAfter, err := authCore.Events(ctx, wfToken, "ep-1", 0)
	Expect(err).NotTo(HaveOccurred())
	Expect(wfEventsAfter).To(HaveLen(1))
	Expect(wfEventsAfter[0].ResourceKind).To(Equal("library"))
	Expect(wfEventsAfter[0].Kind).To(Equal("library.invalidated"))
	Expect(wfEventsAfter[0].ResourceID).To(BeEmpty())
	Expect(wfEventsAfter[0].Body).To(BeNil())
	filteredEv, err := authCore.FilterEvent(ctx, wfToken, domain.Event{
		StreamEpoch:  "ep-1",
		Sequence:     1,
		ResourceKind: "job",
		ResourceID:   jobID,
		Kind:         "job.updated",
		Revision:     1,
		Body:         json.RawMessage(`{"state":"queued"}`),
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(filteredEv.ResourceKind).To(Equal("library"))
	Expect(filteredEv.Kind).To(Equal("library.invalidated"))
	Expect(filteredEv.ResourceID).To(BeEmpty())
	_, err = authCore.Operation(ctx, wfToken, wfKey)
	Expect(err).To(MatchError(domain.ErrNotFound))
	_, err = authCore.CancelJob(ctx, wfToken, jobID, "dddddddd-5555-4555-8555-555555555555")
	Expect(err).To(MatchError(domain.ErrNotFound))

	// Owner single-target scan visibility: moving the targeted document to Trash hides the single-target scan job
	ownerKey := "dddddddd-6666-4666-8666-666666666666"
	reqOwnerScan := httptest.NewRequest(http.MethodPost, "/api/v1/scans", bytes.NewBufferString(`{"version_id":"33333333-3333-4333-8333-333333333333"}`))
	reqOwnerScan.Header.Set("Authorization", "Bearer "+ownerToken)
	reqOwnerScan.Header.Set("Content-Type", "application/json")
	reqOwnerScan.Header.Set("Idempotency-Key", ownerKey)
	recOwnerScan := httptest.NewRecorder()
	h.ServeHTTP(recOwnerScan, reqOwnerScan)
	Expect(recOwnerScan.Code).To(Equal(http.StatusAccepted))
	var ownerScanData struct {
		Data struct {
			JobID string `json:"job_id"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(recOwnerScan.Body.Bytes(), &ownerScanData)).To(Succeed())
	ownerJobID := ownerScanData.Data.JobID
	Expect(ownerJobID).NotTo(Equal(jobID))

	_, err = authCore.Job(ctx, ownerToken, ownerJobID)
	Expect(err).NotTo(HaveOccurred())
	_, err = authCore.Operation(ctx, ownerToken, ownerKey)
	Expect(err).NotTo(HaveOccurred())

	doc := store.documents["22222222-2222-4222-8222-222222222222"]
	doc.Lifecycle = "trash"
	store.documents[doc.ID] = doc

	_, err = authCore.Job(ctx, ownerToken, ownerJobID)
	Expect(err).To(MatchError(domain.ErrNotFound))
	_, err = authCore.Operation(ctx, ownerToken, ownerKey)
	Expect(err).To(MatchError(domain.ErrNotFound))
	_, err = authCore.CancelJob(ctx, ownerToken, ownerJobID, "dddddddd-7777-4777-8777-777777777777")
	Expect(err).To(MatchError(domain.ErrNotFound))
	reqTrashedFindings := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+ownerJobID+"/findings", nil)
	reqTrashedFindings.Header.Set("Authorization", "Bearer "+ownerToken)
	recTrashedFindings := httptest.NewRecorder()
	h.ServeHTTP(recTrashedFindings, reqTrashedFindings)
	Expect(recTrashedFindings.Code).To(Equal(http.StatusNotFound))
	ownerFilteredEv, err := authCore.FilterEvent(ctx, ownerToken, domain.Event{
		StreamEpoch:  "ep-1",
		Sequence:     2,
		ResourceKind: "job",
		ResourceID:   ownerJobID,
		Kind:         "job.updated",
		Revision:     1,
		Body:         json.RawMessage(`{"state":"queued"}`),
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(ownerFilteredEv.ResourceKind).To(Equal("library"))
	Expect(ownerFilteredEv.Kind).To(Equal("library.invalidated"))
	Expect(ownerFilteredEv.ResourceID).To(BeEmpty())

	// P-01 & P-02 regressions:
	// 1. Pre-existing trashed document in the project is excluded at admission without making the workflow project scan unreadable.
	// 2. Distinct workflow principals (wp-1 vs wp-2) and owner submitting equivalent scans never coalesce across principals; each principal reads and cancels only its own scan job.
	doc.Lifecycle = "active"
	store.documents[doc.ID] = doc
	trashedDocID := "44444444-4444-4444-8444-444444444444"
	store.documents[trashedDocID] = domain.Document{
		ID:        trashedDocID,
		ProjectID: projID,
		Title:     "Pre-existing Trashed Doc",
		Lifecycle: "trash",
	}
	wp1 := store.principals["wp-1"]
	wp1.Scope.VersionIDs = []string{"33333333-3333-4333-8333-333333333333"}
	store.principals["wp-1"] = wp1

	wf2Token, err := sec.Secret()
	Expect(err).NotTo(HaveOccurred())
	store.addWorkflowCredentialWithPrincipal("wp-2", "bind-2", "cred-2", wf2Token, projID, []string{"scan.request", "job.read", "job.cancel", "version.read", "project.read"})

	wf1ProjKey := "dddddddd-8888-4888-8888-888888888881"
	reqWF1Proj := httptest.NewRequest(http.MethodPost, "/api/v1/scans", bytes.NewBufferString(`{"project_id":"11111111-1111-4111-8111-111111111111"}`))
	reqWF1Proj.Header.Set("Authorization", "Bearer "+wfToken)
	reqWF1Proj.Header.Set("Content-Type", "application/json")
	reqWF1Proj.Header.Set("Idempotency-Key", wf1ProjKey)
	recWF1Proj := httptest.NewRecorder()
	h.ServeHTTP(recWF1Proj, reqWF1Proj)
	Expect(recWF1Proj.Code).To(Equal(http.StatusAccepted))
	var wf1ProjOut struct {
		Data struct {
			JobID     string `json:"job_id"`
			Coalesced bool   `json:"coalesced"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(recWF1Proj.Body.Bytes(), &wf1ProjOut)).To(Succeed())
	Expect(wf1ProjOut.Data.Coalesced).To(BeFalse())
	Expect(wf1ProjOut.Data.JobID).NotTo(BeEmpty())

	// wp-1 can immediately read its project scan job and operation receipt despite pre-existing trashedDocID in the project:
	wf1JobRead, err := authCore.Job(ctx, wfToken, wf1ProjOut.Data.JobID)
	Expect(err).NotTo(HaveOccurred())
	Expect(wf1JobRead.ID).To(Equal(wf1ProjOut.Data.JobID))
	wf1OpRead, err := authCore.Operation(ctx, wfToken, wf1ProjKey)
	Expect(err).NotTo(HaveOccurred())
	Expect(wf1OpRead.Key.OperationID).To(Equal(wf1ProjKey))

	// wp-2 submitting the exact same project scan receives its own distinct scan job (coalesced: false), not wp-1's job:
	wf2ProjKey := "dddddddd-8888-4888-8888-888888888882"
	reqWF2Proj := httptest.NewRequest(http.MethodPost, "/api/v1/scans", bytes.NewBufferString(`{"project_id":"11111111-1111-4111-8111-111111111111"}`))
	reqWF2Proj.Header.Set("Authorization", "Bearer "+wf2Token)
	reqWF2Proj.Header.Set("Content-Type", "application/json")
	reqWF2Proj.Header.Set("Idempotency-Key", wf2ProjKey)
	recWF2Proj := httptest.NewRecorder()
	h.ServeHTTP(recWF2Proj, reqWF2Proj)
	Expect(recWF2Proj.Code).To(Equal(http.StatusAccepted))
	var wf2ProjOut struct {
		Data struct {
			JobID     string `json:"job_id"`
			Coalesced bool   `json:"coalesced"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(recWF2Proj.Body.Bytes(), &wf2ProjOut)).To(Succeed())
	Expect(wf2ProjOut.Data.Coalesced).To(BeFalse())
	Expect(wf2ProjOut.Data.JobID).NotTo(Equal(wf1ProjOut.Data.JobID))

	// wp-2 can read its own scan job and operation receipt, and gets 404 NotFound on wp-1's scan job:
	wf2JobRead, err := authCore.Job(ctx, wf2Token, wf2ProjOut.Data.JobID)
	Expect(err).NotTo(HaveOccurred())
	Expect(wf2JobRead.ID).To(Equal(wf2ProjOut.Data.JobID))
	_, err = authCore.Job(ctx, wf2Token, wf1ProjOut.Data.JobID)
	Expect(err).To(MatchError(domain.ErrNotFound))

	// Same principal (wp-2) submitting an equivalent fresh-key request DOES coalesce onto wp-2's active job:
	wf2CoalKey := "dddddddd-8888-4888-8888-888888888883"
	reqWF2Coal := httptest.NewRequest(http.MethodPost, "/api/v1/scans", bytes.NewBufferString(`{"project_ids":["11111111-1111-4111-8111-111111111111"]}`))
	reqWF2Coal.Header.Set("Authorization", "Bearer "+wf2Token)
	reqWF2Coal.Header.Set("Content-Type", "application/json")
	reqWF2Coal.Header.Set("Idempotency-Key", wf2CoalKey)
	recWF2Coal := httptest.NewRecorder()
	h.ServeHTTP(recWF2Coal, reqWF2Coal)
	Expect(recWF2Coal.Code).To(Equal(http.StatusAccepted))
	var wf2CoalOut struct {
		Data struct {
			JobID     string `json:"job_id"`
			Coalesced bool   `json:"coalesced"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(recWF2Coal.Body.Bytes(), &wf2CoalOut)).To(Succeed())
	Expect(wf2CoalOut.Data.Coalesced).To(BeTrue())
	Expect(wf2CoalOut.Data.JobID).To(Equal(wf2ProjOut.Data.JobID))
})
