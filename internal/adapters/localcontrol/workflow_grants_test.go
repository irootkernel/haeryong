package localcontrol

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/ai/codex"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	sourceadapter "haeryong.local/haeryong/internal/adapters/source"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/generation"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"haeryong.local/haeryong/skills"
)

func workflowTestID() string { id, e := service.NewID(); Expect(e).NotTo(HaveOccurred()); return id }
func workflowFixture() (*foundation.Store, *HTTP, service.Profile, service.Identity, string, string) {
	ctx := context.Background()
	selection := service.Selection{Profile: "workflow", DataDir: filepath.Join(GinkgoT().TempDir(), "library")}
	_, e := service.Initialize(selection)
	Expect(e).NotTo(HaveOccurred())
	writer, e := service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(writer.Close)
	store := foundation.New(nil)
	_, closeStore, e := store.Prepare(ctx, writer.Profile)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(closeStore)
	core := appauth.New(store)
	pair, e := core.CreatePairing(ctx, "http://127.0.0.1:9270", "owner workflow fixture", "fixture")
	Expect(e).NotTo(HaveOccurred())
	_, e = core.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: workflowTestID(), Action: "approve"})
	Expect(e).NotTo(HaveOccurred())
	redeemed, e := core.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	Expect(e).NotTo(HaveOccurred())
	health, e := store.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	endpoint := &HTTP{Auth: core}
	id := service.Identity{InstanceID: workflowTestID(), LibraryID: writer.Profile.LibraryID, ProfileID: writer.Profile.ProfileID, API: service.APIVersion, Storage: service.StorageState{Ready: true}}
	id, e = endpoint.Start(ctx, writer.Profile, id, 0, func() {})
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(endpoint.Close(ctx)).To(Succeed()); Expect(<-endpoint.Done()).To(Succeed()) })
	return store, endpoint, writer.Profile, id, redeemed.Token, health.Library.UnfiledProjectID
}
func workflowRequest(project string) (appauth.WorkflowGrantRequest, string) {
	secret, e := sec.Secret()
	Expect(e).NotTo(HaveOccurred())
	return appauth.WorkflowGrantRequest{BindingID: workflowTestID(), CredentialID: workflowTestID(), TokenVerifier: sec.Verifier("workflow", secret), Workflow: "generation", ProjectID: project, ProviderID: "codex", SourceSelection: []byte(`{}`)}, secret
}

func workflowSealed(core *importer.Service, token, project string) string {
	GinkgoHelper()
	ctx := context.Background()
	target, _ := json.Marshal(map[string]any{"project_id": project, "document_id": nil})
	r, e := core.Create(ctx, token, workflowTestID(), importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: target})
	Expect(e).NotTo(HaveOccurred())
	var created struct {
		ID string `json:"upload_id"`
	}
	Expect(json.Unmarshal(r.Body, &created)).To(Succeed())
	body := []byte("<html>immutable prior explanation</html>")
	hash := sha256.Sum256(body)
	length := int64(len(body))
	ack, e := core.Put(ctx, token, created.ID, importer.Chunk{Path: "prior.html", Kind: "file", Hash: hex.EncodeToString(hash[:]), Length: &length, Final: true}, bytes.NewReader(body))
	Expect(e).NotTo(HaveOccurred())
	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	r, e = core.Complete(ctx, token, created.ID, workflowTestID(), cp)
	Expect(e).NotTo(HaveOccurred())
	var queued struct {
		Job string `json:"job_id"`
	}
	Expect(json.Unmarshal(r.Body, &queued)).To(Succeed())
	Expect(core.Process(ctx, queued.Job)).To(Succeed())
	return created.ID
}

func workflowPrior(store *foundation.Store, core *importer.Service, token, project string) string {
	GinkgoHelper()
	ctx := context.Background()
	upload := workflowSealed(core, token, project)
	r, e := core.Import(ctx, token, workflowTestID(), importer.ImportRequest{UploadID: upload, ProjectID: project, DocumentPresent: true})
	Expect(e).NotTo(HaveOccurred())
	var queued struct {
		Job string `json:"job_id"`
	}
	Expect(json.Unmarshal(r.Body, &queued)).To(Succeed())
	Expect(core.Process(ctx, queued.Job)).To(Succeed())
	j, e := store.Job(ctx, queued.Job)
	Expect(e).NotTo(HaveOccurred())
	var result struct {
		Version string `json:"version_id"`
	}
	Expect(json.Unmarshal(j.Result, &result)).To(Succeed())
	Expect(result.Version).NotTo(BeEmpty())
	return result.Version
}

var _ = It("GGR05 protected issuer keeps prior project readable but refuses fresh generation body source capture and import effects there", Label("persistence"), func() {
	ctx := context.Background()
	store, endpoint, profile, id, owner, a := workflowFixture()
	b := workflowTestID()
	Expect(store.Write(ctx, func(tx app.WriteTx) error { return tx.CreateProject(domain.Project{ID: b, Name: "prior project"}) })).To(Succeed())
	imports := importer.New(store, endpoint.Auth, store, staging.New())
	prior := workflowPrior(store, imports, owner, b)
	root := GinkgoT().TempDir()
	Expect(os.WriteFile(filepath.Join(root, "source.txt"), []byte("prior project source"), 0600)).To(Succeed())
	Expect(store.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var value map[string]any
		if e = json.Unmarshal(cfg.Value, &value); e != nil {
			return e
		}
		projects, _ := value["projects"].(map[string]any)
		if projects == nil {
			projects = map[string]any{}
			value["projects"] = projects
		}
		projects[b] = map[string]any{"sources": []domain.ManagedSourceMapping{{SourceID: "prior-source", Kind: "files", RootPath: root, RetentionMode: "selected_snapshot", ProviderIDs: []string{"codex"}}}}
		cfg.Value, e = json.Marshal(value)
		if e != nil {
			return e
		}
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
	input, token := workflowRequest(a)
	input.VersionIDs = []string{prior}
	_, e := IssueWorkflowGrant(ctx, profile, id, owner, workflowTestID(), input)
	Expect(e).NotTo(HaveOccurred())
	actor, e := endpoint.Auth.Authenticate(ctx, token)
	Expect(e).NotTo(HaveOccurred())
	Expect(endpoint.Auth.Authority(token).Authorize(ctx, domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID}, Method: "GET", Target: "/api/v1/versions/" + prior})).To(Succeed())
	var priorBytes []byte
	Expect(query.New(store, endpoint.Auth, store).ReadFile(ctx, token, prior, "prior.html", false, func(_ query.ResolvedFile, r io.ReadSeeker) error {
		var e error
		priorBytes, e = io.ReadAll(r)
		return e
	})).To(Succeed())
	Expect(string(priorBytes)).To(Equal("<html>immutable prior explanation</html>"))
	path, skill, e := skills.Materialize(GinkgoT().TempDir())
	Expect(e).NotTo(HaveOccurred())
	sources := source.New(store, endpoint.Auth, store, nil)
	sources.Files = sourceadapter.Files{ExcludedPaths: []string{profile.DataDir}}
	gen := generation.New(store, endpoint.Auth, sources, domain.GenerationInvocation{SkillPath: path, SkillSHA: skill.SHA256, SkillVersion: skill.Version}, codex.SemanticRequestSHA)
	request := generation.Request{ProjectID: a, DocumentPresent: true, Intent: "draft", Prompt: "Explain this project", ProviderID: "codex"}
	_, e = gen.Start(ctx, token, workflowTestID(), request)
	Expect(e).NotTo(HaveOccurred(), "selected project remains usable")
	request.ProjectID = b
	_, generationErr := gen.Start(ctx, token, workflowTestID(), request)
	bodyTarget, _ := json.Marshal(map[string]any{"operation_id": workflowTestID(), "action": "generation", "project_id": b, "document_id": nil})
	_, bodyErr := imports.Create(ctx, token, workflowTestID(), importer.CreateRequest{Purpose: "body", InputMode: "object", Target: bodyTarget})
	_, captureErr := sources.Capture(ctx, token, workflowTestID(), b, source.CaptureRequest{VersionID: prior, SourceMode: "current", ProviderID: "codex", Selections: []source.CaptureSelection{{SourceID: "prior-source", Paths: []source.CapturePath{{Path: "source.txt"}}}}})
	input, importToken := workflowRequest(a)
	input.Workflow = "import"
	input.VersionIDs = []string{prior}
	_, e = IssueWorkflowGrant(ctx, profile, id, owner, workflowTestID(), input)
	Expect(e).NotTo(HaveOccurred())
	resultTarget, _ := json.Marshal(map[string]any{"project_id": b, "document_id": nil})
	_, importStageErr := imports.Create(ctx, importToken, workflowTestID(), importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: resultTarget})
	metadataTarget, _ := json.Marshal(map[string]any{"operation_id": workflowTestID(), "action": "import", "project_id": b, "document_id": nil})
	_, metadataErr := imports.Create(ctx, importToken, workflowTestID(), importer.CreateRequest{Purpose: "metadata", InputMode: "object", Target: metadataTarget})
	AddReportEntry("prior-read versus output authority", map[string]bool{"generation_denied": errors.Is(generationErr, domain.ErrDenied), "body_denied": errors.Is(bodyErr, domain.ErrDenied), "source_capture_denied": errors.Is(captureErr, domain.ErrDenied), "import_staging_denied": errors.Is(importStageErr, domain.ErrDenied)})
	Expect(generationErr).To(MatchError(domain.ErrDenied))
	Expect(bodyErr).To(MatchError(domain.ErrDenied))
	Expect(captureErr).To(MatchError(domain.ErrDenied))
	Expect(importStageErr).To(MatchError(domain.ErrDenied))
	Expect(metadataErr).To(MatchError(domain.ErrDenied))
})

var _ = It("GGR06 protected selection change does not expand an older import credential or invalidate its accepted import and receipt", Label("persistence"), func() {
	ctx := context.Background()
	store, endpoint, profile, id, owner, a := workflowFixture()
	b := workflowTestID()
	Expect(store.Write(ctx, func(tx app.WriteTx) error { return tx.CreateProject(domain.Project{ID: b, Name: "second project"}) })).To(Succeed())
	imports := importer.New(store, endpoint.Auth, store, staging.New())
	va, vb := workflowPrior(store, imports, owner, a), workflowPrior(store, imports, owner, b)
	input, oldToken := workflowRequest(b)
	input.Workflow, input.VersionIDs = "import", []string{va, vb}
	grant, e := IssueWorkflowGrant(ctx, profile, id, owner, workflowTestID(), input)
	Expect(e).NotTo(HaveOccurred())
	u1, u2 := workflowSealed(imports, oldToken, b), workflowSealed(imports, oldToken, b)
	k := workflowTestID()
	request := importer.ImportRequest{UploadID: u1, DocumentPresent: true}
	accepted, e := imports.Import(ctx, oldToken, k, request)
	Expect(e).NotTo(HaveOccurred())
	var queued struct {
		Job string `json:"job_id"`
	}
	Expect(json.Unmarshal(accepted.Body, &queued)).To(Succeed())
	rotated, newToken := workflowRequest(a)
	rotated.Workflow, rotated.VersionIDs, rotated.BindingID = "import", []string{va, vb}, input.BindingID
	rotated.ExpectedSelectionRevision = grant.Selection.Revision
	_, e = IssueWorkflowGrant(ctx, profile, id, owner, workflowTestID(), rotated)
	Expect(e).NotTo(HaveOccurred())
	_, e = endpoint.Auth.Authenticate(ctx, oldToken)
	Expect(e).NotTo(HaveOccurred(), "selection change need not revoke the older credential")
	_, e = imports.Import(ctx, oldToken, workflowTestID(), importer.ImportRequest{UploadID: u2, DocumentPresent: true})
	Expect(e).To(MatchError(domain.ErrDenied), "sealed upload and omitted project cannot bypass fresh admission")
	_, e = imports.Validate(ctx, oldToken, workflowTestID(), importer.ImportRequest{UploadID: u2, DocumentPresent: true})
	Expect(e).To(MatchError(domain.ErrDenied), "a fresh persisted validation uses its resolved upload target")
	target, _ := json.Marshal(map[string]any{"project_id": a, "document_id": nil})
	_, e = imports.Create(ctx, oldToken, workflowTestID(), importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: target})
	Expect(e).To(MatchError(domain.ErrDenied), "current selection alone cannot expand captured B to A")
	replayed, e := imports.Import(ctx, oldToken, k, request)
	Expect(e).NotTo(HaveOccurred())
	Expect(replayed).To(Equal(accepted))
	Expect(imports.Process(ctx, queued.Job)).To(Succeed(), "already accepted import keeps its captured authority")
	j, e := store.Job(ctx, queued.Job)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
	_, e = imports.Create(ctx, newToken, workflowTestID(), importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: target})
	Expect(e).NotTo(HaveOccurred(), "new credential can use its selected A")
})

var _ = It("GGR07 protected re-selection preserves original generation replay and accepted scope but fresh output requires both captured and current project", Label("persistence"), func() {
	ctx := context.Background()
	store, endpoint, profile, id, owner, a := workflowFixture()
	b := workflowTestID()
	Expect(store.Write(ctx, func(tx app.WriteTx) error { return tx.CreateProject(domain.Project{ID: b, Name: "second project"}) })).To(Succeed())
	imports := importer.New(store, endpoint.Auth, store, staging.New())
	va, vb := workflowPrior(store, imports, owner, a), workflowPrior(store, imports, owner, b)
	input, oldToken := workflowRequest(a)
	input.VersionIDs = []string{va, vb}
	grant, e := IssueWorkflowGrant(ctx, profile, id, owner, workflowTestID(), input)
	Expect(e).NotTo(HaveOccurred())
	path, skill, e := skills.Materialize(GinkgoT().TempDir())
	Expect(e).NotTo(HaveOccurred())
	gen := generation.New(store, endpoint.Auth, nil, domain.GenerationInvocation{SkillPath: path, SkillSHA: skill.SHA256, SkillVersion: skill.Version}, codex.SemanticRequestSHA)
	request := generation.Request{ProjectID: a, DocumentPresent: true, Intent: "draft", Prompt: "Explain project A", ProviderID: "codex"}
	k := workflowTestID()
	accepted, e := gen.Start(ctx, oldToken, k, request)
	Expect(e).NotTo(HaveOccurred())
	rotated, newToken := workflowRequest(b)
	rotated.BindingID, rotated.VersionIDs, rotated.ExpectedSelectionRevision = input.BindingID, []string{va, vb}, grant.Selection.Revision
	_, e = IssueWorkflowGrant(ctx, profile, id, owner, workflowTestID(), rotated)
	Expect(e).NotTo(HaveOccurred())
	actor, e := endpoint.Auth.Authenticate(ctx, oldToken)
	Expect(e).NotTo(HaveOccurred())
	serialized, e := json.Marshal(actor)
	Expect(e).NotTo(HaveOccurred())
	Expect(string(serialized)).NotTo(ContainSubstring("mutation_project"))
	replayed, e := gen.Start(ctx, oldToken, k, request)
	Expect(e).NotTo(HaveOccurred())
	Expect(replayed).To(Equal(accepted))
	_, e = gen.Start(ctx, oldToken, workflowTestID(), request)
	Expect(e).To(MatchError(domain.ErrDenied), "captured A is now restricted by current B")
	request.ProjectID = b
	_, e = gen.Start(ctx, oldToken, workflowTestID(), request)
	Expect(e).To(MatchError(domain.ErrDenied), "current B cannot expand old captured A")
	_, e = gen.Start(ctx, newToken, workflowTestID(), request)
	Expect(e).NotTo(HaveOccurred())
	legacyToken, e := sec.Secret()
	Expect(e).NotTo(HaveOccurred())
	Expect(store.Write(ctx, func(tx app.WriteTx) error {
		c, e := tx.ScopedCredential(input.CredentialID)
		if e != nil {
			return e
		}
		Expect(c.MutationProjectID).To(Equal(a))
		c.MutationProjectID = b
		Expect(tx.PutScopedCredential(c)).To(MatchError(domain.ErrConflict))
		var response struct {
			Job string `json:"job_id"`
		}
		Expect(json.Unmarshal(accepted.Body, &response)).To(Succeed())
		j, e := tx.Job(response.Job)
		if e != nil {
			return e
		}
		var scope domain.ProviderScope
		Expect(json.Unmarshal(j.Scope, &scope)).To(Succeed())
		Expect(appauth.AuthorizeAccepted(tx, scope.PrincipalID, scope.AuthEpoch, scope.Purpose, scope.Target, scope.CredentialScope)).To(Succeed())
		// An existing credential without the new issuer fact still authenticates
		// for reads, but the current selection is not a guessed captured grant.
		legacy := c
		legacy.ID = workflowTestID()
		legacy.TokenHash = sec.Verifier("workflow", legacyToken)
		legacy.MutationProjectID = ""
		return tx.PutScopedCredential(legacy)
	})).To(Succeed())
	legacy, e := endpoint.Auth.Authenticate(ctx, legacyToken)
	Expect(e).NotTo(HaveOccurred())
	Expect(endpoint.Auth.Authority(legacyToken).Authorize(ctx, domain.Request{Key: domain.OperationKey{PrincipalID: legacy.PrincipalID}, Method: "GET", Target: "/api/v1/versions/" + vb})).To(Succeed())
	_, e = gen.Start(ctx, legacyToken, workflowTestID(), request)
	Expect(e).To(MatchError(domain.ErrDenied))
})

var _ = It("GGR01 real protected local owner grant replay rotation expiry and revocation preserve stable binding without retaining bearer in receipts", Label("persistence"), func() {
	ctx := context.Background()
	store, endpoint, profile, id, owner, project := workflowFixture()
	input, secret := workflowRequest(project)
	key := workflowTestID()
	grant, e := IssueWorkflowGrant(ctx, profile, id, owner, key, input)
	Expect(e).NotTo(HaveOccurred())
	Expect(grant.Selection.ProjectID).To(Equal(project))
	Expect(grant.LibraryID).To(Equal(profile.LibraryID))
	Expect(grant.Active).To(BeTrue())
	replay, e := IssueWorkflowGrant(ctx, profile, id, owner, key, input)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(grant))
	actor, e := endpoint.Auth.Authenticate(ctx, secret)
	Expect(e).NotTo(HaveOccurred())
	Expect(actor.Scope.Kind).To(Equal("workflow"))
	Expect(actor.PrincipalID).To(Equal(grant.PrincipalID))
	ownerSession, e := endpoint.Auth.Authenticate(ctx, owner)
	Expect(e).NotTo(HaveOccurred())
	receipt, e := store.Receipt(ctx, domain.OperationKey{PrincipalID: ownerSession.PrincipalID, OperationID: key})
	Expect(e).NotTo(HaveOccurred())
	encoded, _ := json.Marshal(receipt)
	Expect(bytes.Contains(encoded, []byte(secret))).To(BeFalse(), "only the verifier belongs in a server receipt")
	// A workflow bearer and an OS-local proof are not the current owner.
	other, _ := workflowRequest(project)
	_, e = IssueWorkflowGrant(ctx, profile, id, secret, workflowTestID(), other)
	var failure *BootstrapError
	Expect(errors.As(e, &failure)).To(BeTrue())
	Expect(failure.Status).To(Equal(403))
	_, e = IssueWorkflowGrant(ctx, profile, id, "", workflowTestID(), other)
	Expect(e).To(MatchError(sec.ErrRequired))
	now := time.Now().UTC().Truncate(time.Millisecond)
	endpoint.Auth.Now = func() time.Time { return now }
	rotated, newSecret := workflowRequest(project)
	rotated.BindingID = input.BindingID
	rotated.ExpectedSelectionRevision = grant.Selection.Revision
	rotated.PreviousCredentialID = &input.CredentialID
	expires := now.Add(time.Second)
	rotated.ExpiresAt = &expires
	next, e := IssueWorkflowGrant(ctx, profile, id, owner, workflowTestID(), rotated)
	Expect(e).NotTo(HaveOccurred())
	Expect(next.PrincipalID).To(Equal(grant.PrincipalID))
	Expect(next.BindingID).To(Equal(grant.BindingID))
	_, e = endpoint.Auth.Authenticate(ctx, secret)
	Expect(e).To(MatchError(sec.ErrRevoked))
	_, e = endpoint.Auth.Authenticate(ctx, newSecret)
	Expect(e).NotTo(HaveOccurred())
	endpoint.Auth.Now = func() time.Time { return expires.Add(time.Second) }
	_, e = endpoint.Auth.Authenticate(ctx, newSecret)
	Expect(e).To(MatchError(sec.ErrExpired))
	final, lastSecret := workflowRequest(project)
	final.BindingID = grant.BindingID
	final.ExpectedSelectionRevision = next.Selection.Revision
	final.PreviousCredentialID = &rotated.CredentialID
	last, e := IssueWorkflowGrant(ctx, profile, id, owner, workflowTestID(), final)
	Expect(e).NotTo(HaveOccurred())
	Expect(last.PrincipalID).To(Equal(grant.PrincipalID))
	Expect(RevokeWorkflowGrant(ctx, profile, id, owner, workflowTestID(), grant.BindingID)).To(Succeed())
	_, e = endpoint.Auth.Authenticate(ctx, lastSecret)
	Expect(e).To(MatchError(sec.ErrRevoked))
	saved, e := WorkflowGrant(ctx, profile, id, owner, grant.BindingID)
	Expect(e).NotTo(HaveOccurred())
	Expect(saved.Active).To(BeFalse())
	Expect(saved.PrincipalID).To(Equal(grant.PrincipalID))
	Expect(store.Write(ctx, func(tx app.WriteTx) error { jobs, e := tx.Jobs(); Expect(jobs).To(BeEmpty()); return e })).To(Succeed())
})
var _ = It("GGR02 grant proof binds current owner body instance profile library and operation and refuses substitution before mutation", Label("persistence"), func() {
	ctx := context.Background()
	store, endpoint, p, id, owner, project := workflowFixture()
	input, _ := workflowRequest(project)
	body, _ := json.Marshal(input)
	key := workflowTestID()
	nonce := strings.Repeat("ab", 32)
	for _, mode := range []string{"body", "bearer", "instance", "profile", "library", "operation", "peer", "forwarded", "duplicate", "unknown"} {
		raw := append([]byte{}, body...)
		actualOwner := owner
		actualKey := key
		proofProfile, proofIdentity := p, id
		if mode == "instance" {
			proofIdentity.InstanceID = "old"
		}
		if mode == "profile" {
			proofProfile.ProfileID = "other"
		}
		if mode == "library" {
			proofProfile.LibraryID = "other"
		}
		if mode == "duplicate" {
			raw = append(raw[:len(raw)-1], []byte(`,"workflow":"generation"}`)...)
		}
		if mode == "unknown" {
			raw = append(raw[:len(raw)-1], []byte(`,"owner":true}`)...)
		}
		proof := workflowRequestProof(proofProfile, proofIdentity, nonce, "POST", workflowGrantsPath, key, owner, raw)
		if mode == "body" {
			raw = append(raw, ' ')
		}
		if mode == "bearer" {
			actualOwner = "different"
		}
		if mode == "operation" {
			actualKey = workflowTestID()
		}
		req := httptest.NewRequest("POST", workflowGrantsPath, bytes.NewReader(raw))
		req.RemoteAddr = "127.0.0.1:4000"
		req.Header.Set("Authorization", "Bearer "+actualOwner)
		req.Header.Set("Idempotency-Key", actualKey)
		req.Header.Set("X-Haeryong-Nonce", nonce)
		req.Header.Set("X-Haeryong-Proof", proof)
		if mode == "peer" {
			req.RemoteAddr = "192.0.2.1:1"
		}
		if mode == "forwarded" {
			req.Header.Set("Forwarded", "for=127.0.0.1")
		}
		response := httptest.NewRecorder()
		endpoint.workflowGrant(response, req, p, id)
		Expect(response.Code).To(BeNumerically(">=", 400), mode)
	}
	Expect(store.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.WorkflowPrincipalByBinding(input.BindingID)
		Expect(e).To(MatchError(domain.ErrNotFound))
		return nil
	})).To(Succeed())
})

var _ = It("GGR08 protected workflow inventory above16KiB roundtrips exactly with signed replay and body tamper refusal", Label("persistence"), func() {
	ctx := context.Background()
	_, endpoint, profile, id, owner, project := workflowFixture()
	input, _ := workflowRequest(project)
	paths := []map[string]string{}
	for i := 0; i < 700; i++ {
		paths = append(paths, map[string]string{"path": strings.Repeat("selected/", 8) + strconv.Itoa(i) + ".go"})
	}
	input.SourceSelection, _ = json.Marshal(map[string]any{"source_mode": "current", "selections": []any{map[string]any{"source_id": workflowTestID(), "paths": paths}}})
	body, e := json.Marshal(input)
	Expect(e).NotTo(HaveOccurred())
	Expect(len(body)).To(BeNumerically(">", 16384))
	key := workflowTestID()
	grant, e := IssueWorkflowGrant(ctx, profile, id, owner, key, input)
	Expect(e).NotTo(HaveOccurred())
	Expect(string(grant.Selection.SourceSelection)).To(MatchJSON(string(input.SourceSelection)))
	encoded, e := json.Marshal(grant)
	Expect(e).NotTo(HaveOccurred())
	Expect(len(encoded)).To(BeNumerically(">", 16384))
	replay, e := IssueWorkflowGrant(ctx, profile, id, owner, key, input)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(grant))
	current, e := WorkflowGrant(ctx, profile, id, owner, input.BindingID)
	Expect(e).NotTo(HaveOccurred())
	Expect(current.Selection).To(Equal(grant.Selection))
	// A correctly shaped, owner-authenticated request cannot alter signed bytes.
	nonce := strings.Repeat("a", 64)
	tampered := append(append([]byte{}, body...), byte(' '))
	req := httptest.NewRequest("POST", workflowGrantsPath, bytes.NewReader(tampered))
	req.RemoteAddr = "127.0.0.1:40000"
	req.Header.Set("Authorization", "Bearer "+owner)
	req.Header.Set("Idempotency-Key", workflowTestID())
	req.Header.Set("X-Haeryong-Nonce", nonce)
	req.Header.Set("X-Haeryong-Proof", workflowRequestProof(profile, id, nonce, "POST", workflowGrantsPath, req.Header.Get("Idempotency-Key"), owner, body))
	recorder := httptest.NewRecorder()
	endpoint.workflowGrant(recorder, req, profile, id)
	Expect(recorder.Code).To(Equal(401))
	changed := input
	changed.SourceSelection = json.RawMessage(`{}`)
	_, e = IssueWorkflowGrant(ctx, profile, id, owner, key, changed)
	Expect(e).To(HaveOccurred(), "same-key changed signed logical body must not replay")
})

type workflowAuthFailureStore struct {
	app.Store
	cause error
}

func (s workflowAuthFailureStore) Write(context.Context, func(app.WriteTx) error) error {
	return s.cause
}

type workflowUnreadBody struct{ reads int }

func (b *workflowUnreadBody) Read([]byte) (int, error) { b.reads++; return 0, io.EOF }

var _ = It("GGR09 workflow preauthentication preserves transient store503 versus session401 and nonowner403 without consuming content", Label("persistence"), func() {
	store, endpoint, profile, id, owner, project := workflowFixture()
	input, workflowToken := workflowRequest(project)
	_, e := IssueWorkflowGrant(context.Background(), profile, id, owner, workflowTestID(), input)
	Expect(e).NotTo(HaveOccurred())
	check := func(h *HTTP, token string, status int, code string) {
		body := &workflowUnreadBody{}
		nonce := strings.Repeat("b", 64)
		key := workflowTestID()
		req := httptest.NewRequest("POST", workflowGrantsPath, body)
		req.RemoteAddr = "127.0.0.1:40000"
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Haeryong-Nonce", nonce)
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("X-Haeryong-Proof", workflowRequestProof(profile, id, nonce, "POST", workflowGrantsPath, key, token, nil))
		response := httptest.NewRecorder()
		h.workflowGrant(response, req, profile, id)
		Expect(response.Code).To(Equal(status))
		Expect(body.reads).To(BeZero())
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		Expect(json.Unmarshal(response.Body.Bytes(), &failure)).To(Succeed())
		Expect(failure.Error.Code).To(Equal(code))
		Expect(response.Header().Get("X-Haeryong-Proof")).To(Equal(workflowResponseProof(profile, nonce, status, response.Body.Bytes())))
	}
	for _, test := range []struct {
		cause  error
		status int
		code   string
	}{
		{errors.New("controlled auth storage unavailable"), 503, "SERVICE_RECOVERING"}, {context.DeadlineExceeded, 503, "SERVICE_RECOVERING"},
		{sec.ErrRequired, 401, "AUTH_REQUIRED"}, {sec.ErrExpired, 401, "AUTH_REQUIRED"}, {sec.ErrRevoked, 401, "AUTH_REQUIRED"}, {domain.ErrDenied, 403, "FORBIDDEN"},
	} {
		h := &HTTP{Auth: appauth.New(workflowAuthFailureStore{Store: store, cause: test.cause})}
		check(h, owner, test.status, test.code)
	}
	// This is an actual issued workflow credential, not an owner inference.
	check(endpoint, workflowToken, 403, "FORBIDDEN")
})
