package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/ai/codex"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/scan"
	"haeryong.local/haeryong/internal/application/search"
	"haeryong.local/haeryong/internal/application/service"
	appsource "haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

type localTestFileReader struct{}

func (localTestFileReader) Open(_ context.Context, root, rel string) (io.ReadCloser, error) {
	return os.Open(filepath.Join(root, filepath.FromSlash(rel)))
}

type spyEmptyHashReader struct {
	inner      search.ObjectReader
	emptyReads int
	readHashes []string
}

func (r *spyEmptyHashReader) Read(ctx context.Context, object domain.Object, pin domain.Pin, consume func(io.ReadSeeker) error) error {
	r.readHashes = append(r.readHashes, object.Hash)
	if object.Hash == "" {
		r.emptyReads++
		return errors.New("unexpected empty object hash read")
	}
	return r.inner.Read(ctx, object, pin, consume)
}

type testCaptureBytes struct {
	s     *Store
	bytes *searchBytes
}

func (b testCaptureBytes) Read(ctx context.Context, object domain.Object, pin domain.Pin, consume func(io.ReadSeeker) error) error {
	return b.bytes.Read(ctx, object, pin, consume)
}

func (b testCaptureBytes) Put(ctx context.Context, r io.Reader, record func(app.WriteTx, domain.Object) error) (domain.Object, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return domain.Object{}, err
	}
	sum := sha256.Sum256(raw)
	obj := domain.Object{
		Hash: hex.EncodeToString(sum[:]),
		Size: int64(len(raw)),
	}
	b.bytes.files[obj.Hash] = string(raw)
	err = b.s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.PutDurableObject(obj); e != nil {
			return e
		}
		if record != nil {
			return record(tx, obj)
		}
		return nil
	})
	return obj, err
}

type testPurgeCollector struct {
	s     *Store
	bytes *searchBytes
	errs  map[string]error
}

func (c *testPurgeCollector) Collect(ctx context.Context, obj domain.Object) error {
	var res domain.GCReservation
	err := c.s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		res, e = tx.ReserveGC(obj.Hash, id(GinkgoT()))
		return e
	})
	if c.errs != nil {
		c.errs[obj.Hash] = err
	}
	if err != nil {
		return err
	}
	if c.bytes != nil {
		delete(c.bytes.files, obj.Hash)
	}
	err = c.s.Write(ctx, func(tx app.WriteTx) error {
		return tx.FinishGC(res)
	})
	if c.errs != nil {
		c.errs[obj.Hash] = err
	}
	return err
}

func initTestGitRepo(dir string, files map[string]string) string {
	GinkgoHelper()
	Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.local",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.local",
		)
		out, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out))
		return strings.TrimSpace(string(out))
	}
	run("init")
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		Expect(os.MkdirAll(filepath.Dir(full), 0o700)).To(Succeed())
		Expect(os.WriteFile(full, []byte(content), 0o600)).To(Succeed())
	}
	run("add", ".")
	run("commit", "-m", "initial")
	return run("rev-parse", "HEAD")
}

func commitTestGitFile(dir, rel, content, msg string) string {
	GinkgoHelper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	Expect(os.MkdirAll(filepath.Dir(full), 0o700)).To(Succeed())
	Expect(os.WriteFile(full, []byte(content), 0o600)).To(Succeed())
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", msg}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.local",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.local",
		)
		out, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out))
	}
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(out))
	return strings.TrimSpace(string(out))
}

func setBacklinkRepairMode(s *Store, projectID, mode string) {
	GinkgoHelper()
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		cfg, err := tx.Configuration()
		if err != nil {
			return err
		}
		var body map[string]any
		Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
		if projectID == "" {
			maint, _ := body["maintenance"].(map[string]any)
			if maint == nil {
				maint = map[string]any{}
				body["maintenance"] = maint
			}
			autoApply, _ := maint["auto_apply"].(map[string]any)
			if autoApply == nil {
				autoApply = map[string]any{}
				maint["auto_apply"] = autoApply
			}
			autoApply["backlink_index_repair"] = mode
		} else {
			projects, _ := body["projects"].(map[string]any)
			if projects == nil {
				projects = map[string]any{}
				body["projects"] = projects
			}
			proj, _ := projects[projectID].(map[string]any)
			if proj == nil {
				proj = map[string]any{}
				projects[projectID] = proj
			}
			maint, _ := proj["maintenance"].(map[string]any)
			if maint == nil {
				maint = map[string]any{}
				proj["maintenance"] = maint
			}
			autoApply, _ := maint["auto_apply"].(map[string]any)
			if autoApply == nil {
				autoApply = map[string]any{}
				maint["auto_apply"] = autoApply
			}
			autoApply["backlink_index_repair"] = mode
		}
		raw, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		cfg.Value = raw
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
}

func configureProjectSources(s *Store, projectID string, entries []map[string]any) {
	GinkgoHelper()
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		cfg, err := tx.Configuration()
		if err != nil {
			return err
		}
		var body map[string]any
		Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
		projects, _ := body["projects"].(map[string]any)
		if projects == nil {
			projects = map[string]any{}
			body["projects"] = projects
		}
		proj, _ := projects[projectID].(map[string]any)
		if proj == nil {
			proj = map[string]any{}
			projects[projectID] = proj
		}
		proj["sources"] = entries
		raw, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		cfg.Value = raw
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
}

func startScanJob(scanSvc *scan.Service, token string, req domain.ScanRequest) string {
	GinkgoHelper()
	raw, err := json.Marshal(req)
	Expect(err).NotTo(HaveOccurred())
	res, err := scanSvc.StartScan(ctx, token, id(GinkgoT()), raw, req)
	Expect(err).NotTo(HaveOccurred())
	Expect(res.Status).To(Equal(202))
	var out struct {
		JobID string `json:"job_id"`
	}
	Expect(json.Unmarshal(res.Body, &out)).To(Succeed())
	Expect(out.JobID).NotTo(BeEmpty())
	return out.JobID
}

func runScanTurns(s *Store, bytes search.ObjectReader, jobID string, maxTurns int) domain.Job {
	GinkgoHelper()
	w := scan.NewWorker(s, bytes)
	var j domain.Job
	for turn := 0; turn < maxTurns; turn++ {
		Expect(w.Process(ctx, jobID)).To(Succeed())
		var err error
		j, err = s.Job(ctx, jobID)
		Expect(err).NotTo(HaveOccurred())
		if j.State == "completed" || j.State == "failed" || j.State == "cancelled" || j.State == "paused" {
			return j
		}
	}
	Fail("scan job did not reach terminal/settled state within maxTurns: " + j.State)
	return j
}

func runScanViaDispatcher(s *Store, bytes search.ObjectReader, jobID string, timeout time.Duration) domain.Job {
	GinkgoHelper()
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	d := &work.Dispatcher{
		Store:   s,
		Derived: work.NewMaintenance(s, bytes),
		Scans:   scan.NewWorker(s, bytes),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(runCtx)
	}()
	var finalJob domain.Job
	Eventually(func() string {
		j, err := s.Job(ctx, jobID)
		if err != nil {
			return ""
		}
		finalJob = j
		return j.State
	}, timeout, 10*time.Millisecond).Should(Or(Equal("completed"), Equal("failed"), Equal("cancelled"), Equal("paused")))
	cancel()
	Expect(<-errCh).To(Succeed())
	return finalJob
}

func publishInProjectWithSources(f *referenceWorld, projectID string, refs []any, body string, sources map[string]map[string]any, regions ...map[string]any) string {
	GinkgoHelper()
	version, document, set := f.id(), f.id(), f.id()
	if refs == nil {
		refs = []any{}
	}
	raw, _ := json.Marshal(map[string]any{"schema_version": "1.0", "viewer_profile": "html_js_v1", "references": refs})
	manifest, page := digest(raw), digest([]byte(body))
	f.bytes.files[manifest] = string(raw)
	f.bytes.files[page] = body
	if projectID == "" {
		h, err := f.s.Health(ctx)
		Expect(err).NotTo(HaveOccurred())
		projectID = h.Library.UnfiledProjectID
	}
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.CreateContentSet(set, digest(nil)); err != nil {
			return err
		}
		for _, o := range []domain.Object{{Hash: manifest, Size: int64(len(raw)), State: "ready"}, {Hash: page, Size: int64(len(body)), State: "ready"}} {
			if err := tx.PutDurableObject(o); err != nil {
				return err
			}
		}
		if err := tx.PutEntry(domain.Entry{ContentSetID: set, Path: "page.html", Kind: "file", Hash: page, Size: int64(len(body))}); err != nil {
			return err
		}
		if err := tx.SealContentSet(set); err != nil {
			return err
		}
		metadataID := ""
		if len(sources) > 0 || len(regions) > 0 {
			metadataID = f.id()
			if err := tx.CreateMetadataIndex(domain.MetadataIndex{ID: metadataID, PrincipalID: "system", Hash: manifest, ContentSetID: set}); err != nil {
				return err
			}
			for srcID, srcMeta := range sources {
				if err := tx.PutImportMetadata(metadataID, "sources", srcID, jsonBytes(srcMeta)); err != nil {
					return err
				}
			}
			for _, regMeta := range regions {
				regKey := fmt.Sprintf("%s#%s", regMeta["page_path"], regMeta["id"])
				if err := tx.PutImportMetadata(metadataID, "regions", regKey, jsonBytes(regMeta)); err != nil {
					return err
				}
			}
		}
		if err := tx.FinalizeImport(
			domain.ImportIntent{JobID: metadataID, NewDocument: true, DocumentID: document, ProjectID: projectID, DocumentRevision: 1, Title: "References"},
			domain.Version{ID: version, DocumentID: document, ContentSetID: set, ManifestSHA: manifest, PayloadSHA: digest(nil), Title: "References", ViewerProfile: "html_js_v1", SourceBasis: "unverified"},
		); err != nil {
			return err
		}
		if metadataID != "" {
			if err := tx.ReleaseMetadataPreparation(metadataID); err != nil {
				return err
			}
		}
		return nil
	})).To(Succeed())
	return version
}

func publishMultiPageInProjectWithSources(f *referenceWorld, projectID string, pages map[string]string, sources map[string]map[string]any, regions ...map[string]any) string {
	GinkgoHelper()
	version, document, set := f.id(), f.id(), f.id()
	raw, _ := json.Marshal(map[string]any{"schema_version": "1.0", "viewer_profile": "html_js_v1", "references": []any{}})
	manifest := digest(raw)
	f.bytes.files[manifest] = string(raw)
	if projectID == "" {
		h, err := f.s.Health(ctx)
		Expect(err).NotTo(HaveOccurred())
		projectID = h.Library.UnfiledProjectID
	}
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.CreateContentSet(set, digest(nil)); err != nil {
			return err
		}
		if err := tx.PutDurableObject(domain.Object{Hash: manifest, Size: int64(len(raw)), State: "ready"}); err != nil {
			return err
		}
		for pPath, body := range pages {
			pHash := digest([]byte(body))
			f.bytes.files[pHash] = body
			if err := tx.PutDurableObject(domain.Object{Hash: pHash, Size: int64(len(body)), State: "ready"}); err != nil {
				return err
			}
			if err := tx.PutEntry(domain.Entry{ContentSetID: set, Path: pPath, Kind: "file", Hash: pHash, Size: int64(len(body))}); err != nil {
				return err
			}
		}
		if err := tx.SealContentSet(set); err != nil {
			return err
		}
		metadataID := ""
		if len(sources) > 0 || len(regions) > 0 {
			metadataID = f.id()
			if err := tx.CreateMetadataIndex(domain.MetadataIndex{ID: metadataID, PrincipalID: "system", Hash: manifest, ContentSetID: set}); err != nil {
				return err
			}
			for srcID, srcMeta := range sources {
				if err := tx.PutImportMetadata(metadataID, "sources", srcID, jsonBytes(srcMeta)); err != nil {
					return err
				}
			}
			for _, regMeta := range regions {
				regKey := fmt.Sprintf("%s#%s", regMeta["page_path"], regMeta["id"])
				if err := tx.PutImportMetadata(metadataID, "regions", regKey, jsonBytes(regMeta)); err != nil {
					return err
				}
			}
		}
		if err := tx.FinalizeImport(
			domain.ImportIntent{JobID: metadataID, NewDocument: true, DocumentID: document, ProjectID: projectID, DocumentRevision: 1, Title: "MultiPage"},
			domain.Version{ID: version, DocumentID: document, ContentSetID: set, ManifestSHA: manifest, PayloadSHA: digest(nil), Title: "MultiPage", ViewerProfile: "html_js_v1", SourceBasis: "unverified"},
		); err != nil {
			return err
		}
		if metadataID != "" {
			if err := tx.ReleaseMetadataPreparation(metadataID); err != nil {
				return err
			}
		}
		return nil
	})).To(Succeed())
	return version
}

func allCapturedVersions(s *Store, jobID string) []domain.ScanCapturedVersion {
	GinkgoHelper()
	var out []domain.ScanCapturedVersion
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		after := 0
		for {
			batch, err := tx.ScanCapturedVersions(jobID, after, 64)
			if err != nil {
				return err
			}
			out = append(out, batch...)
			if len(batch) < 64 {
				return nil
			}
			after = batch[len(batch)-1].VersionOrdinal
		}
	})).To(Succeed())
	return out
}

func allJobItems(s *Store, jobID string) []domain.JobItem {
	GinkgoHelper()
	var out []domain.JobItem
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		after := 0
		for {
			batch, err := tx.JobItems(jobID, after, 64)
			if err != nil {
				return err
			}
			out = append(out, batch...)
			if len(batch) < 64 {
				return nil
			}
			after = batch[len(batch)-1].Ordinal
		}
	})).To(Succeed())
	return out
}

func applyDocumentLifecycle(s *Store, auth *appauth.Service, bytes search.ObjectReader, token, docID, action string) domain.LifecycleResult {
	GinkgoHelper()
	q := query.New(s, auth, bytes)
	d, err := s.Document(ctx, docID)
	Expect(err).NotTo(HaveOccurred())
	raw, _ := json.Marshal(map[string]any{"operation": action, "expected_revision": d.Revision})
	resp, err := q.LifecyclePlanCreate(ctx, token, "", docID, raw)
	Expect(err).NotTo(HaveOccurred())
	var plan domain.LifecyclePage
	Expect(json.Unmarshal(resp.Body, &plan)).To(Succeed())
	raw, _ = json.Marshal(map[string]any{"plan_id": plan.ID, "expected_revision": plan.ExpectedRevision, "confirmed": true})
	resp, err = q.LifecycleCommit(ctx, token, plan.ID, "", raw)
	Expect(err).NotTo(HaveOccurred())
	var result domain.LifecycleResult
	Expect(json.Unmarshal(resp.Body, &result)).To(Succeed())
	return result
}

func runPurgeTurns(s *Store, bytes *searchBytes, purgeJobID string, maxTurns int) domain.Job {
	GinkgoHelper()
	clock := time.Now()
	collector := &testPurgeCollector{s: s, bytes: bytes, errs: map[string]error{}}
	p := &work.Purger{Store: s, Bytes: collector, Now: func() time.Time { return clock }}
	var j domain.Job
	var intent domain.PurgeIntent
	var candidates []domain.Object
	for turn := 0; turn < maxTurns; turn++ {
		clock = clock.Add(61 * time.Second)
		Expect(p.Process(ctx, purgeJobID)).To(Succeed())
		var err error
		j, err = s.Job(ctx, purgeJobID)
		Expect(err).NotTo(HaveOccurred())
		if j.State == "completed" {
			return j
		}
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			var e error
			intent, e = tx.PurgeIntent(purgeJobID)
			if e != nil {
				return e
			}
			candidates, e = tx.PurgeCandidates(purgeJobID, 64)
			return e
		})).To(Succeed())
		if j.State == "paused" && intent.MetadataRemoved && intent.Reason == "protected" && len(intent.RetainedSets) == 0 && len(candidates) > 0 {
			allLegitShared := true
			for _, cand := range candidates {
				if !errors.Is(collector.errs[cand.Hash], domain.ErrPinned) {
					allLegitShared = false
					break
				}
				var refCount, pinCount int
				Expect(s.db.QueryRow(`SELECT count(*) FROM object_references WHERE content_hash=?`, cand.Hash).Scan(&refCount)).To(Succeed())
				Expect(s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE content_hash=?`, cand.Hash).Scan(&pinCount)).To(Succeed())
				if refCount+pinCount == 0 {
					allLegitShared = false
					break
				}
			}
			if allLegitShared {
				return j
			}
		}
	}
	var candDiag []string
	for _, cand := range candidates {
		var refCount, pinCount int
		_ = s.db.QueryRow(`SELECT count(*) FROM object_references WHERE content_hash=?`, cand.Hash).Scan(&refCount)
		_ = s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE content_hash=?`, cand.Hash).Scan(&pinCount)
		candDiag = append(candDiag, fmt.Sprintf("%s(refs=%d,pins=%d,err=%v)", cand.Hash, refCount, pinCount, collector.errs[cand.Hash]))
	}
	Fail(fmt.Sprintf("purge job did not settle within maxTurns: state=%s error=%s reason=%s metadata_removed=%v retained_sets=%v candidates=%v", j.State, j.Error, intent.Reason, intent.MetadataRemoved, intent.RetainedSets, candDiag))
	return j
}

var _ = It("T-008-01-01 freezes captured scan denominator across post-capture version publish, metadata edit, Git commit, permissions change, trash and purge", Label("persistence"), func() {
	s, auth, w, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "T-008-01-01 owner")
	f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}

	h, err := s.Health(ctx)
	Expect(err).NotTo(HaveOccurred())
	projectID := h.Library.UnfiledProjectID
	project2ID := f.id()
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.RegisterProject(domain.Project{ID: project2ID, Name: "Second Project"})
	})).To(Succeed())

	// Approve a local Git source on projectID and publish v1 with declared source metadata pinned to initialHead.
	gitDir := filepath.Join(GinkgoT().TempDir(), "repo")
	initialHead := initTestGitRepo(gitDir, map[string]string{
		"README.md": "# Initial\n",
		"pkg/a.go":  "package pkg\n",
	})
	configureProjectSources(s, projectID, []map[string]any{
		{
			"source_id":      "repo-main",
			"kind":           "git",
			"root_path":      gitDir,
			"repo_id":        "repo-main",
			"retention_mode": "reference_only",
			"provider_ids":   []string{},
		},
	})

	v1 := publishInProjectWithSources(f, projectID, nil, "<p id='r1'>alpha</p>", map[string]map[string]any{
		"repo-main": {"kind": "git", "repo_id": "repo-main", "exact_revision": initialHead},
	}, map[string]any{"page_path": "page.html", "id": "r1", "label": "Region 1"})
	f.run(v1, "search")
	f.run(v1, "references")

	v2 := publishInProjectWithSources(f, projectID, nil, "<p id='r2'>beta</p>", nil, map[string]any{"page_path": "page.html", "id": "r2", "label": "Region 2"})
	f.run(v2, "search")
	f.run(v2, "references")

	// Publish vPerm directly in project2ID so we can mutate project2ID permissions_revision after capture without blocking projectID.
	vPerm := publishInProjectWithSources(f, project2ID, nil, "<p id='rp'>perm doc</p>", nil, map[string]any{"page_path": "page.html", "id": "rp", "label": "Region Perm"})
	f.run(vPerm, "search")
	f.run(vPerm, "references")

	ver1, err := s.Version(ctx, v1)
	Expect(err).NotTo(HaveOccurred())
	ver2, err := s.Version(ctx, v2)
	Expect(err).NotTo(HaveOccurred())

	// Compute independent oracle for each version's expected unit count:
	// Each published version in referenceWorld has: 1 version + 1 page (page.html) + 1 region + 1 entry (page.html) + 1 source + 1 index = 6 units.
	expectedUnitsPerVersion := 6
	expectedTotalUnits := 3 * expectedUnitsPerVersion

	scanSvc := scan.New(s, auth, f.bytes, nil)
	jobID := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		ProjectIDs: []string{projectID, project2ID},
		Depth:      "full_scope",
	})

	var runBefore domain.ScanRun
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		runBefore, e = tx.ScanRun(jobID)
		return e
	})).To(Succeed())
	capturedBefore := allCapturedVersions(s, jobID)
	Expect(capturedBefore).To(HaveLen(3))
	Expect(runBefore.Denominator.TotalUnits).To(Equal(expectedTotalUnits))
	Expect(runBefore.Denominator.Documents).To(Equal(3))
	Expect(runBefore.Denominator.Versions).To(Equal(3))

	// Verify v1 captured exact Git target initialHead before post-capture Git commit.
	var v1Targets []domain.ScanSourceTarget
	for _, cv := range capturedBefore {
		if cv.VersionID == v1 {
			var env struct {
				Sources []domain.ScanSourceTarget `json:"sources"`
			}
			Expect(json.Unmarshal(cv.TargetsJSON, &env)).To(Succeed())
			v1Targets = env.Sources
		}
	}
	Expect(v1Targets).To(HaveLen(1))
	Expect(v1Targets[0].SourceID).To(Equal("repo-main"))
	Expect(v1Targets[0].ResolvedRevision).To(Equal(initialHead))
	Expect(v1Targets[0].Availability).To(Equal("available"))

	// Mutate state AFTER capture and BEFORE worker execution:
	// 1. Publish a brand new version v3.
	v3 := f.publish(nil, "<p id='r3'>gamma</p>")
	f.run(v3, "search")
	f.run(v3, "references")
	// 2. Commit a new file to the Git repository advancing HEAD.
	newHead := commitTestGitFile(gitDir, "pkg/new.go", "package pkg\n// added after capture\n", "second")
	Expect(newHead).NotTo(Equal(initialHead))
	// 3. Edit live declared source metadata on v1 to point to newHead (frozen capture on scan_captured_versions must still use initialHead).
	_, err = s.db.Exec(`UPDATE version_sources SET metadata=? WHERE version_id=? AND source_id='repo-main'`, string(jsonBytes(map[string]any{"kind": "git", "repo_id": "repo-main", "exact_revision": newHead})), v1)
	Expect(err).NotTo(HaveOccurred())
	// 4. Advance permissions_revision on project2ID (fences vPerm units to blocked without changing denominator).
	_, err = s.db.Exec(`UPDATE projects SET permissions_revision = permissions_revision + 1 WHERE project_id=?`, project2ID)
	Expect(err).NotTo(HaveOccurred())
	// 5. Trash document 2 (ver2.DocumentID) via production lifecycle API.
	trashRes := applyDocumentLifecycle(s, auth, f.bytes, owner.Token, ver2.DocumentID, "trash")
	Expect(trashRes.Lifecycle).To(Equal("trash"))

	// Drive bounded scan worker turns to completion.
	jobAfter := runScanTurns(s, f.bytes, jobID, 64)
	Expect(jobAfter.State).To(Equal("completed"))

	var runAfter domain.ScanRun
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		runAfter, e = tx.ScanRun(jobID)
		return e
	})).To(Succeed())
	itemsAfter := allJobItems(s, jobID)
	Expect(runAfter.Denominator).To(Equal(runBefore.Denominator))
	Expect(itemsAfter).To(HaveLen(expectedTotalUnits))

	var v1Completed, v2SkippedTrash, vPermBlocked int
	for _, it := range itemsAfter {
		Expect(it.VersionID).NotTo(Equal(v3), "newly published version after capture must not enter frozen scan run")
		switch it.VersionID {
		case v1:
			if it.State == "completed" {
				v1Completed++
			}
			if it.UnitKind == "source" {
				Expect(string(it.Detail)).To(ContainSubstring(initialHead))
				Expect(string(it.Detail)).NotTo(ContainSubstring(newHead))
			}
		case v2:
			if it.State == "skipped" && it.Reason == "document_trashed" {
				v2SkippedTrash++
			}
		case vPerm:
			if it.State == "blocked" && it.Reason == "permission_changed" {
				vPermBlocked++
			}
		}
	}
	Expect(v1Completed).To(Equal(expectedUnitsPerVersion))
	Expect(v2SkippedTrash).To(Equal(expectedUnitsPerVersion))
	Expect(vPermBlocked).To(Equal(expectedUnitsPerVersion))

	// 6. Now purge document 2 (ver2.DocumentID) and run Purger turns; verify privacy redaction preserves fixed denominator and item count.
	purgeRes := applyDocumentLifecycle(s, auth, f.bytes, owner.Token, ver2.DocumentID, "purge")
	Expect(purgeRes.Lifecycle).To(Equal("purged"))
	Expect(purgeRes.JobID).NotTo(BeEmpty())
	runPurgeTurns(s, f.bytes, purgeRes.JobID, 64)
	Expect(f.bytes.files).NotTo(HaveKey(digest([]byte("<p id='r2'>beta</p>"))))

	var runPostPurge domain.ScanRun
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		runPostPurge, e = tx.ScanRun(jobID)
		return e
	})).To(Succeed())
	itemsPostPurge := allJobItems(s, jobID)
	capturedPostPurge := allCapturedVersions(s, jobID)
	Expect(runPostPurge.Denominator).To(Equal(runBefore.Denominator))
	Expect(itemsPostPurge).To(HaveLen(expectedTotalUnits))

	purgedItemCount := 0
	for _, it := range itemsPostPurge {
		if it.DocumentID == ver2.DocumentID {
			purgedItemCount++
			Expect(it.PagePath).To(BeEmpty())
			Expect(it.RegionID).To(BeEmpty())
			Expect(string(it.Detail)).To(Equal(`{"purged":true}`))
			Expect(it.Reason).To(Equal("document_purged"))
		}
	}
	Expect(purgedItemCount).To(Equal(expectedUnitsPerVersion))

	var foundPurgedRoster bool
	for _, cv := range capturedPostPurge {
		if cv.DocumentID == ver2.DocumentID {
			foundPurgedRoster = true
			Expect(cv.ExclusionReason).To(Equal("document_purged"))
			Expect(string(cv.SelectionJSON)).To(Equal(`{"purged":true}`))
			Expect(string(cv.TargetsJSON)).To(Equal(`[]`))
		}
	}
	Expect(foundPurgedRoster).To(BeTrue())

	// 7. Scoped page_path / region_id scan on a version with a source asset_path, purged BOTH before materialization (stage=='inventory') and after materialization.
	vScoped := publishInProjectWithSources(f, projectID, nil, "<p id='sec-reg'>secret region</p>", map[string]map[string]any{
		"repo-main": {"kind": "git", "repo_id": "repo-main", "exact_revision": initialHead, "asset_path": "sources/secret-asset.txt"},
	}, map[string]any{"page_path": "page.html", "id": "sec-reg", "label": "Secret Region"})
	f.run(vScoped, "search")
	f.run(vScoped, "references")
	verScoped, err := s.Version(ctx, vScoped)
	Expect(err).NotTo(HaveOccurred())

	// Start one scoped scan left queued in stage=='inventory' and a second scoped scan driven to completion.
	jobScopedQueued := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		DocumentID: verScoped.DocumentID,
		PagePath:   "page.html",
		RegionID:   "sec-reg",
		Depth:      "change_focused",
	})
	jobScopedDone := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		DocumentID: verScoped.DocumentID,
		PagePath:   "page.html",
		RegionID:   "sec-reg",
		Depth:      "full_scope",
	})
	jScopedAfter := runScanTurns(s, f.bytes, jobScopedDone, 32)
	Expect(jScopedAfter.State).To(Equal("completed"))

	// Trash and purge verScoped.DocumentID while jobScopedQueued is still in stage=='inventory'.
	Expect(applyDocumentLifecycle(s, auth, f.bytes, owner.Token, verScoped.DocumentID, "trash").Lifecycle).To(Equal("trash"))
	scopedPurge := applyDocumentLifecycle(s, auth, f.bytes, owner.Token, verScoped.DocumentID, "purge")
	Expect(scopedPurge.Lifecycle).To(Equal("purged"))
	runPurgeTurns(s, f.bytes, scopedPurge.JobID, 64)

	// Document-scoped jobScopedQueued was honestly cancelled by lifecycle purge while in stage=='inventory'.
	// Stepping the worker after purge does not resurrect private data or re-read purged objects, and both
	// jobScopedQueued (cancelled) and jobScopedDone (completed) have redacted requested_scope and scrubbed target_set AssetPath.
	jQueuedAfter := runScanTurns(s, f.bytes, jobScopedQueued, 32)
	Expect(jQueuedAfter.State).To(Equal("cancelled"))
	Expect(jQueuedAfter.CancelRequested).To(BeTrue())

	for _, sJobID := range []string{jobScopedQueued, jobScopedDone} {
		var sr domain.ScanRun
		var jRow domain.Job
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			var e error
			sr, e = tx.ScanRun(sJobID)
			if e != nil {
				return e
			}
			jRow, e = tx.Job(sJobID)
			return e
		})).To(Succeed())
		Expect(sr.Denominator.Documents).To(Equal(1))
		Expect(sr.Denominator.Versions).To(Equal(1))
		Expect(sr.RequestedScope.PathPrefix).To(BeEmpty())
		Expect(sr.RequestedScope.PagePath).To(BeEmpty())
		Expect(sr.RequestedScope.RegionID).To(BeEmpty())
		if sr.Report.RequestedScope != nil {
			Expect(sr.Report.RequestedScope.PathPrefix).To(BeEmpty())
			Expect(sr.Report.RequestedScope.PagePath).To(BeEmpty())
			Expect(sr.Report.RequestedScope.RegionID).To(BeEmpty())
		}
		Expect(string(jRow.Scope)).NotTo(ContainSubstring("sec-reg"))
		Expect(string(jRow.Result)).NotTo(ContainSubstring("sec-reg"))
		Expect(string(jRow.Result)).NotTo(ContainSubstring("secret-asset.txt"))
		cvScopedRows := allCapturedVersions(s, sJobID)
		Expect(cvScopedRows).To(HaveLen(1))
		Expect(cvScopedRows[0].ExclusionReason).To(Equal("document_purged"))
		Expect(cvScopedRows[0].TargetSetID).To(BeEmpty())
		Expect(string(cvScopedRows[0].TargetsJSON)).To(Equal("[]"))
	}
	var leakedTargetSets int
	Expect(s.db.QueryRow(`SELECT count(*) FROM captured_target_sets WHERE targets_json LIKE '%secret-asset.txt%'`).Scan(&leakedTargetSets)).To(Succeed())
	Expect(leakedTargetSets).To(BeZero())
	Expect(f.bytes.files).NotTo(HaveKey(digest([]byte("<p id='r2'>beta</p>"))))
	Expect(f.bytes.files).NotTo(HaveKey(digest([]byte("<p id='sec-reg'>secret region</p>"))))
	Expect(f.bytes.files).To(HaveKey(ver1.ManifestSHA))
	Expect(f.bytes.files).To(HaveKey(digest([]byte("<p id='r1'>alpha</p>"))))
	ver1After, err := s.Version(ctx, v1)
	Expect(err).NotTo(HaveOccurred())
	Expect(ver1After.ID).To(Equal(v1))

	// R2 multi-target purge regression: purging one document in a multi-document scan with page_path predicate
	// preserves page_path for remaining healthy documents, preserves healthy SourceTargets while scrubbing purged AssetPath,
	// and immediately reconciles unmaterialized purged units into Structural.Skipped/Excluded and Semantic.Skipped.
	vMultiPurge := publishMultiPageInProjectWithSources(f, projectID, map[string]string{
		"page.html":  "<p id='mp-reg'>purge me</p>",
		"extra.html": "<p>purge extra</p>",
	}, map[string]map[string]any{
		"repo-main": {"kind": "git", "repo_id": "repo-main", "exact_revision": initialHead, "asset_path": "sources/secret-multi-purge.txt"},
	}, map[string]any{"page_path": "page.html", "id": "mp-reg", "label": "MP"})
	f.run(vMultiPurge, "search")
	f.run(vMultiPurge, "references")
	verMultiPurge, err := s.Version(ctx, vMultiPurge)
	Expect(err).NotTo(HaveOccurred())

	vMultiHealthy := publishMultiPageInProjectWithSources(f, projectID, map[string]string{
		"page.html":  "<p id='mh-reg'>keep healthy page</p>",
		"extra.html": "<p>keep extra page out of page_path scope</p>",
	}, map[string]map[string]any{
		"repo-main": {"kind": "git", "repo_id": "repo-main", "exact_revision": initialHead, "asset_path": "sources/healthy-keep.txt"},
	}, map[string]any{"page_path": "page.html", "id": "mh-reg", "label": "MH"})
	f.run(vMultiHealthy, "search")
	f.run(vMultiHealthy, "references")
	verMultiHealthy, err := s.Version(ctx, vMultiHealthy)
	Expect(err).NotTo(HaveOccurred())

	jobMultiDone := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		DocumentIDs: []string{verMultiPurge.DocumentID, verMultiHealthy.DocumentID},
		PagePath:    "page.html",
		Depth:       "full_scope",
	})
	Expect(runScanTurns(s, f.bytes, jobMultiDone, 64).State).To(Equal("completed"))
	for _, it := range allJobItems(s, jobMultiDone) {
		if it.DocumentID == verMultiHealthy.DocumentID {
			Expect(it.State).To(Equal("completed"), "pre-purge healthy baseline item %s (%s) reason=%s", it.ItemID, it.UnitKind, it.Reason)
		}
	}

	jobMultiQueued := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		DocumentIDs: []string{verMultiPurge.DocumentID, verMultiHealthy.DocumentID},
		PagePath:    "page.html",
		Depth:       "full_scope",
	})
	Expect(jobMultiQueued).NotTo(Equal(jobMultiDone))
	jMultiQueuedInit, err := s.Job(ctx, jobMultiQueued)
	Expect(err).NotTo(HaveOccurred())
	Expect(jMultiQueuedInit.State).To(Equal("queued"))

	Expect(applyDocumentLifecycle(s, auth, f.bytes, owner.Token, verMultiPurge.DocumentID, "trash").Lifecycle).To(Equal("trash"))
	multiPurgeRes := applyDocumentLifecycle(s, auth, f.bytes, owner.Token, verMultiPurge.DocumentID, "purge")
	Expect(multiPurgeRes.Lifecycle).To(Equal("purged"))
	runPurgeTurns(s, f.bytes, multiPurgeRes.JobID, 64)

	// Verify jobMultiQueued BEFORE any worker turn: unmaterialized purged units are reconciled into report counts,
	// healthy PagePath predicate is preserved, and purged AssetPath is scrubbed while healthy SourceTargets remain.
	var srMultiQueuedPre, srMultiDonePost domain.ScanRun
	var jMultiQueuedPre, jMultiDonePost domain.Job
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		srMultiQueuedPre, e = tx.ScanRun(jobMultiQueued)
		if e != nil {
			return e
		}
		jMultiQueuedPre, e = tx.Job(jobMultiQueued)
		if e != nil {
			return e
		}
		srMultiDonePost, e = tx.ScanRun(jobMultiDone)
		if e != nil {
			return e
		}
		jMultiDonePost, e = tx.Job(jobMultiDone)
		return e
	})).To(Succeed())
	Expect(srMultiQueuedPre.Denominator.Documents).To(Equal(2))
	Expect(srMultiQueuedPre.Denominator.Versions).To(Equal(2))
	Expect(srMultiQueuedPre.Denominator.TotalUnits).To(Equal(12))
	Expect(srMultiQueuedPre.Report.Structural.Skipped).To(Equal(6))
	Expect(srMultiQueuedPre.Report.Structural.Excluded).To(Equal(6))
	Expect(srMultiQueuedPre.Report.Semantic.Skipped).To(Equal(6))
	Expect(srMultiQueuedPre.RequestedScope.PagePath).To(Equal("page.html"))
	Expect(srMultiQueuedPre.RequestedScope.DocumentIDs).To(Equal([]string{verMultiHealthy.DocumentID}))
	Expect(string(jMultiQueuedPre.Result)).To(ContainSubstring("sources/healthy-keep.txt"))
	Expect(string(jMultiQueuedPre.Result)).NotTo(ContainSubstring("secret-multi-purge.txt"))
	Expect(string(jMultiDonePost.Result)).To(ContainSubstring("sources/healthy-keep.txt"))
	Expect(string(jMultiDonePost.Result)).NotTo(ContainSubstring("secret-multi-purge.txt"))
	Expect(srMultiDonePost.RequestedScope.PagePath).To(Equal("page.html"))

	// Now drive jobMultiQueued to completion and verify vMultiHealthy did NOT broaden to extra.html and matches pre-purge healthy baseline.
	jMultiQueuedDone := runScanTurns(s, f.bytes, jobMultiQueued, 64)
	Expect(jMultiQueuedDone.State).To(Equal("completed"))
	itemsMultiQueued := allJobItems(s, jobMultiQueued)
	Expect(itemsMultiQueued).To(HaveLen(12))
	for _, it := range itemsMultiQueued {
		Expect(it.PagePath).NotTo(Equal("extra.html"), "healthy document in multi-target scan must not broaden beyond preserved page_path predicate")
		if it.DocumentID == verMultiPurge.DocumentID {
			Expect(it.State).To(Equal("skipped"), "purged item %s (%s) reason=%s", it.ItemID, it.UnitKind, it.Reason)
			Expect(it.Reason).To(Equal("document_purged"))
		} else {
			Expect(it.State).To(Equal("completed"), "healthy item %s (%s) state=%s reason=%s", it.ItemID, it.UnitKind, it.State, it.Reason)
			Expect(it.Reason).To(Equal("ok"))
		}
	}
})

var _ = It("T-008-01-02 repairs or proposes deterministic reference divergence by policy with zero provider attempts", Label("persistence"), func() {
	s, auth, w, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "T-008-01-02 owner")
	f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}

	target := f.publish(nil, "<p id='retry'>target</p>")
	f.run(target, "search")
	f.run(target, "references")
	wrong := f.publish(nil, "<p id='retry'>wrong target</p>")
	f.run(wrong, "search")
	f.run(wrong, "references")
	source := f.publish([]any{f.declaration(target, "C06_FAULT")}, "<p id='retry'>source</p>")
	f.run(source, "search")
	f.run(source, "references")

	dbPath := filepath.Join(w.Profile.DataDir, "storage", "library.sqlite")
	damageReverseIndex(s, dbPath, target, wrong)
	s = New(nil)
	_, closeStore, err := s.Prepare(ctx, w.Profile)
	Expect(err).NotTo(HaveOccurred())
	defer closeStore()
	f.s = s
	auth = appauth.New(s)
	q := query.New(s, auth, f.bytes)

	// Confirm divergence is present before scan.
	bad, err := q.ValidateVersionIndexes(ctx, owner.Token, target, "references")
	Expect(err).NotTo(HaveOccurred())
	Expect(bad.State).To(Equal("inconsistent"))

	scanSvc := scan.New(s, auth, f.bytes, nil)

	// Mode 1: backlink_index_repair = "disabled" -> records anomaly on index item, no repair, no finding.
	setBacklinkRepairMode(s, "", "disabled")
	jobDisabled := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID: target,
		Depth:     "change_focused",
	})
	jDis := runScanTurns(s, f.bytes, jobDisabled, 32)
	Expect(jDis.State).To(Equal("completed"))
	findingsDisabled, err := scanSvc.ListJobFindings(ctx, owner.Token, domain.FindingFilter{JobID: jobDisabled})
	Expect(err).NotTo(HaveOccurred())
	Expect(findingsDisabled.Items).To(BeEmpty())
	stillBad, err := q.ValidateVersionIndexes(ctx, owner.Token, target, "references")
	Expect(err).NotTo(HaveOccurred())
	Expect(stillBad.State).To(Equal("inconsistent"))

	// Mode 2: backlink_index_repair = "propose" -> creates deterministic proposed finding with approval_required=true, leaves index untouched.
	setBacklinkRepairMode(s, "", "propose")
	jobPropose := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID: target,
		Depth:     "change_focused",
	})
	jProp := runScanTurns(s, f.bytes, jobPropose, 32)
	Expect(jProp.State).To(Equal("completed"))
	findingsPropose, err := scanSvc.ListJobFindings(ctx, owner.Token, domain.FindingFilter{JobID: jobPropose})
	Expect(err).NotTo(HaveOccurred())
	Expect(findingsPropose.Items).To(HaveLen(1))
	Expect(findingsPropose.Items[0].Kind).To(Equal("backlink_index_repair"))
	Expect(findingsPropose.Items[0].Origin).To(Equal("deterministic"))
	Expect(findingsPropose.Items[0].Status).To(Equal("proposed"))
	Expect(findingsPropose.Items[0].ApprovalRequired).To(BeTrue())
	Expect(findingsPropose.Items[0].Proposal).NotTo(BeEmpty())
	stillBad2, err := q.ValidateVersionIndexes(ctx, owner.Token, target, "references")
	Expect(err).NotTo(HaveOccurred())
	Expect(stillBad2.State).To(Equal("inconsistent"))

	// Mode 2b: Mid-flight policy narrowing (admitted under "auto", narrowed to "propose" before index item executes) -> proposes instead of auto-repairing.
	setBacklinkRepairMode(s, "", "auto")
	jobNarrow := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID: target,
		Depth:     "change_focused",
	})
	setBacklinkRepairMode(s, "", "propose")
	jNarrow := runScanTurns(s, f.bytes, jobNarrow, 32)
	Expect(jNarrow.State).To(Equal("completed"))
	findingsNarrow, err := scanSvc.ListJobFindings(ctx, owner.Token, domain.FindingFilter{JobID: jobNarrow})
	Expect(err).NotTo(HaveOccurred())
	Expect(findingsNarrow.Items).To(HaveLen(1))
	Expect(findingsNarrow.Items[0].Status).To(Equal("proposed"))
	Expect(findingsNarrow.Items[0].ApprovalRequired).To(BeTrue())

	// Mode 2c: Queued owned child cancellation does not spawn a second repair child and marks finding blocked with rescan_recommended=true.
	setBacklinkRepairMode(s, "", "auto")
	jobChildCancel := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID: target,
		Depth:     "change_focused",
	})
	wkr := scan.NewWorker(s, f.bytes)
	var spawnedChildID string
	for step := 0; step < 16; step++ {
		Expect(wkr.Process(ctx, jobChildCancel)).To(Succeed())
		for _, it := range allJobItems(s, jobChildCancel) {
			if it.UnitKind == "index" && it.RepairJobID != "" {
				spawnedChildID = it.RepairJobID
			}
		}
		if spawnedChildID != "" {
			break
		}
	}
	Expect(spawnedChildID).NotTo(BeEmpty())
	// Cancel the owned child job before it runs, then step parent scan to observe child terminal state.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.CancelJob(spawnedChildID)
		return e
	})).To(Succeed())
	jChildCancelDone := runScanTurns(s, f.bytes, jobChildCancel, 16)
	Expect(jChildCancelDone.State).To(Equal("completed"))
	findingsChildCancel, err := scanSvc.ListJobFindings(ctx, owner.Token, domain.FindingFilter{JobID: jobChildCancel})
	Expect(err).NotTo(HaveOccurred())
	Expect(findingsChildCancel.Items).To(HaveLen(1))
	Expect(findingsChildCancel.Items[0].Status).To(Equal("blocked"))
	var runChildCancel domain.ScanRun
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		runChildCancel, e = tx.ScanRun(jobChildCancel)
		return e
	})).To(Succeed())
	Expect(runChildCancel.Report.RescanRecommended).To(BeTrue())
	Expect(runChildCancel.Report.IncompleteReferenceRepairs).To(ContainElement(target))

	// Mode 3: backlink_index_repair = "auto" -> executes deterministic repair across actual Dispatcher turns, re-validates consistency, records applied finding, zero provider attempts.
	setBacklinkRepairMode(s, "", "auto")
	jobAuto := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID: target,
		Depth:     "change_focused",
	})
	jAuto := runScanViaDispatcher(s, f.bytes, jobAuto, 10*time.Second)
	Expect(jAuto.State).To(Equal("completed"))
	findingsAuto, err := scanSvc.ListJobFindings(ctx, owner.Token, domain.FindingFilter{JobID: jobAuto})
	Expect(err).NotTo(HaveOccurred())
	Expect(findingsAuto.Items).To(HaveLen(1))
	autoItems := allJobItems(s, jobAuto)
	var childDiag string
	for _, it := range autoItems {
		if it.UnitKind == "index" {
			var childState, childErr string
			if it.RepairJobID != "" {
				if cj, cerr := s.Job(ctx, it.RepairJobID); cerr == nil {
					childState = cj.State
					childErr = cj.Error
				}
			}
			childDiag = fmt.Sprintf("finding.status=%q finding.reason=%q item.state=%q item.reason=%q item.repair_job_id=%q child.state=%q child.error=%q job.error=%q",
				findingsAuto.Items[0].Status, findingsAuto.Items[0].Reason, it.State, it.Reason, it.RepairJobID, childState, childErr, jAuto.Error)
		}
	}
	Expect(findingsAuto.Items[0].Kind).To(Equal("backlink_index_repair"), childDiag)
	Expect(findingsAuto.Items[0].Origin).To(Equal("deterministic"), childDiag)
	Expect(findingsAuto.Items[0].Status).To(Equal("applied"), childDiag)
	Expect(findingsAuto.Items[0].ApprovalRequired).To(BeFalse(), childDiag)

	repaired, err := q.ValidateVersionIndexes(ctx, owner.Token, target, "references")
	Expect(err).NotTo(HaveOccurred())
	Expect(repaired.State).To(Equal("consistent"))

	// Mode 4: Global SQLite DDL drift (missing reverse-index declaration) fails the index item with reverse_index_definition_missing and never spawns a repair child.
	var savedEdgeIdxSQL string
	Expect(s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='index' AND name='authored_edges_target'`).Scan(&savedEdgeIdxSQL)).To(Succeed())
	_, err = s.db.Exec(`DROP INDEX authored_edges_target`)
	Expect(err).NotTo(HaveOccurred())
	jobDDL := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID: target,
		Depth:     "change_focused",
	})
	jDDL := runScanTurns(s, f.bytes, jobDDL, 16)
	Expect(jDDL.State).To(Equal("completed"))
	var sawDDLFail bool
	for _, it := range allJobItems(s, jobDDL) {
		if it.UnitKind == "index" {
			Expect(it.State).To(Equal("failed"))
			Expect(it.Reason).To(Equal("reverse_index_definition_missing"))
			Expect(it.RepairJobID).To(BeEmpty())
			sawDDLFail = true
		}
	}
	Expect(sawDDLFail).To(BeTrue())
	_, err = s.db.Exec(savedEdgeIdxSQL)
	Expect(err).NotTo(HaveOccurred())

	// Mode 2b-batch (after target repair and index recreation): Policy narrowing AFTER owned child is enqueued and BETWEEN derived-worker batches on source -> suspends/cancels child and downgrades to proposed finding without completing rebuild.
	verSource, err := s.Version(ctx, source)
	Expect(err).NotTo(HaveOccurred())
	_, err = s.db.Exec(`DELETE FROM authored_edges WHERE from_version_id=?;DELETE FROM authored_references WHERE from_version_id=?`, source, source)
	Expect(err).NotTo(HaveOccurred())
	setBacklinkRepairMode(s, "", "auto")
	jobNarrowBatch := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID: source,
		Depth:     "change_focused",
	})
	var batchChildID string
	for step := 0; step < 16 && batchChildID == ""; step++ {
		Expect(wkr.Process(ctx, jobNarrowBatch)).To(Succeed())
		for _, it := range allJobItems(s, jobNarrowBatch) {
			if it.UnitKind == "index" && it.RepairJobID != "" {
				batchChildID = it.RepairJobID
			}
		}
	}
	Expect(batchChildID).NotTo(BeEmpty())
	refWkr := work.NewMaintenance(s, f.bytes)
	// Batch 1 of derived_index worker advances stage from 'repair' to 'declarations'.
	Expect(refWkr.Process(ctx, batchChildID)).To(Succeed())
	// Narrow policy between derived_index worker batches.
	setBacklinkRepairMode(s, "", "propose")
	// Batch 2 hits ReferenceFence -> scanRepairAuthority -> ErrSearchRestricted and pauses the child.
	Expect(refWkr.Process(ctx, batchChildID)).To(MatchError(domain.ErrSearchRestricted))
	jNarrowBatchDone := runScanTurns(s, f.bytes, jobNarrowBatch, 16)
	Expect(jNarrowBatchDone.State).To(Equal("completed"))
	batchChildAfter, err := s.Job(ctx, batchChildID)
	Expect(err).NotTo(HaveOccurred())
	Expect(batchChildAfter.State).To(Equal("cancelled"))
	findingsNarrowBatch, err := scanSvc.ListJobFindings(ctx, owner.Token, domain.FindingFilter{JobID: jobNarrowBatch})
	Expect(err).NotTo(HaveOccurred())
	Expect(findingsNarrowBatch.Items).To(HaveLen(1))
	Expect(findingsNarrowBatch.Items[0].Status).To(Equal("proposed"))
	Expect(findingsNarrowBatch.Items[0].ApprovalRequired).To(BeTrue())
	sourceStillBad, err := q.ValidateVersionIndexes(ctx, owner.Token, source, "references")
	Expect(err).NotTo(HaveOccurred())
	Expect(sourceStillBad.State).To(Equal("inconsistent"))

	// Mode 2d: Actual owned child failure (mid-rebuild storage_failed) + parent restart interruption does not spawn a duplicate replacement child and records failed item + blocked finding + rescan_recommended=true.
	_, err = s.db.Exec(`DELETE FROM authored_edges WHERE from_version_id=?;DELETE FROM authored_references WHERE from_version_id=?`, source, source)
	Expect(err).NotTo(HaveOccurred())
	setBacklinkRepairMode(s, "", "auto")
	jobChildFail := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID: source,
		Depth:     "change_focused",
	})
	var failChildID string
	for step := 0; step < 16 && failChildID == ""; step++ {
		Expect(wkr.Process(ctx, jobChildFail)).To(Succeed())
		for _, it := range allJobItems(s, jobChildFail) {
			if it.UnitKind == "index" && it.RepairJobID != "" {
				failChildID = it.RepairJobID
			}
		}
	}
	Expect(failChildID).NotTo(BeEmpty())
	// Run batch 1 of child ('repair' -> 'declarations'), then fault manifest object read on batch 2 so child settles to 'failed' ('storage_failed').
	Expect(refWkr.Process(ctx, failChildID)).To(Succeed())
	savedSourceManifest := f.bytes.files[verSource.ManifestSHA]
	delete(f.bytes.files, verSource.ManifestSHA)
	_ = refWkr.Process(ctx, failChildID)
	f.bytes.files[verSource.ManifestSHA] = savedSourceManifest
	failChildJob, err := s.Job(ctx, failChildID)
	Expect(err).NotTo(HaveOccurred())
	Expect(failChildJob.State).To(Equal("failed"))
	Expect(failChildJob.Error).To(Equal("storage_failed"))
	// Simulate store restart while parent scan job is waiting on the failed child -> recoverLocalJobs marks parent 'interrupted'.
	Expect(s.Close()).To(Succeed())
	s = New(nil)
	_, closeStoreAfterFail, err := s.Prepare(ctx, w.Profile)
	Expect(err).NotTo(HaveOccurred())
	defer closeStoreAfterFail()
	f.s = s
	auth = appauth.New(s)
	q = query.New(s, auth, f.bytes)
	scanSvc = scan.New(s, auth, f.bytes, nil)
	jFailInterrupted, err := s.Job(ctx, jobChildFail)
	Expect(err).NotTo(HaveOccurred())
	Expect(jFailInterrupted.State).To(Equal("interrupted"))
	jChildFailDone := runScanTurns(s, f.bytes, jobChildFail, 16)
	Expect(jChildFailDone.State).To(Equal("completed"))
	var sawFailedIndexItem bool
	for _, it := range allJobItems(s, jobChildFail) {
		if it.UnitKind == "index" {
			Expect(it.State).To(Equal("failed"))
			Expect(it.Reason).To(Equal("storage_failed"))
			Expect(it.RepairJobID).To(Equal(failChildID), "must not spawn a duplicate replacement child after child failure and parent restart")
			sawFailedIndexItem = true
		}
	}
	Expect(sawFailedIndexItem).To(BeTrue())
	findingsChildFail, err := scanSvc.ListJobFindings(ctx, owner.Token, domain.FindingFilter{JobID: jobChildFail})
	Expect(err).NotTo(HaveOccurred())
	Expect(findingsChildFail.Items).To(HaveLen(1))
	Expect(findingsChildFail.Items[0].Status).To(Equal("blocked"))
	Expect(findingsChildFail.Items[0].ApprovalRequired).To(BeTrue())
	var runChildFail domain.ScanRun
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		runChildFail, e = tx.ScanRun(jobChildFail)
		return e
	})).To(Succeed())
	Expect(runChildFail.Report.RescanRecommended).To(BeTrue())
	Expect(runChildFail.Report.IncompleteReferenceRepairs).To(ContainElement(source))

	// R3 active repair child + lifecycle Trash report reconciliation regression:
	// when a document with an active repair child transitions to Trash mid-scan, fenceScanDocument immediately refreshes
	// scan_runs.report and jobs.result (Skipped, Excluded, Findings.Stale, IncompleteReferenceRepairs, RescanRecommended),
	// and normal dispatcher work settles both the cancelling repair child and the parent scan cleanly.
	vTrashRepair := f.publish([]any{f.declaration(target, "TRASH_REPAIR")}, "<p id='retry'>trash repair doc</p>")
	f.run(vTrashRepair, "search")
	f.run(vTrashRepair, "references")
	verTrashRepair, err := s.Version(ctx, vTrashRepair)
	Expect(err).NotTo(HaveOccurred())
	verTargetDoc, err := s.Version(ctx, target)
	Expect(err).NotTo(HaveOccurred())
	_, err = s.db.Exec(`DELETE FROM authored_edges WHERE from_version_id = ?`, vTrashRepair)
	Expect(err).NotTo(HaveOccurred())
	_, err = s.db.Exec(`DELETE FROM authored_references WHERE from_version_id = ?`, vTrashRepair)
	Expect(err).NotTo(HaveOccurred())

	jobTrashMid := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		DocumentIDs: []string{verTargetDoc.DocumentID, verTrashRepair.DocumentID},
		Depth:       "full_scope",
	})
	wkrTrashMid := scan.NewWorker(s, f.bytes)
	var trashChildID string
	for turn := 0; turn < 12; turn++ {
		Expect(wkrTrashMid.Process(ctx, jobTrashMid)).To(Succeed())
		for _, it := range allJobItems(s, jobTrashMid) {
			if it.VersionID == vTrashRepair && it.UnitKind == "index" && it.RepairJobID != "" {
				trashChildID = it.RepairJobID
			}
		}
		if trashChildID != "" {
			break
		}
	}
	Expect(trashChildID).NotTo(BeEmpty())
	// Seed a proposed finding on verTrashRepair.DocumentID for jobTrashMid so lifecycle Trash transitions it to stale.
	seedFindingID := f.id()
	unfiledProjT02 := mustUnfiledProject(s)
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.PutFinding(domain.Finding{
			ID:                     seedFindingID,
			JobID:                  jobTrashMid,
			ProjectID:              unfiledProjT02,
			DocumentID:             &verTrashRepair.DocumentID,
			VersionID:              &vTrashRepair,
			Kind:                   "freshness_assessment",
			Origin:                 "deterministic",
			Status:                 "proposed",
			Rationale:              "Proposed before Trash",
			Uncertainty:            "low",
			Evidence:               []byte(`{}`),
			EvidenceFingerprint:    "fp-seed-trash",
			Proposal:               []byte(`{}`),
			AppliedRef:             []byte(`{}`),
			ApprovalRequired:       true,
			CapturedConfigRevision: 1,
			PermissionsRevision:    1,
			Revision:               1,
		})
	})).To(Succeed())

	// Move verTrashRepair.DocumentID to Trash and immediately inspect scan_runs.report and jobs.result BEFORE stepping worker.
	Expect(applyDocumentLifecycle(s, auth, f.bytes, owner.Token, verTrashRepair.DocumentID, "trash").Lifecycle).To(Equal("trash"))
	var srTrashImmediate domain.ScanRun
	var jTrashImmediate, jChildImmediate domain.Job
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		srTrashImmediate, e = tx.ScanRun(jobTrashMid)
		if e != nil {
			return e
		}
		jTrashImmediate, e = tx.Job(jobTrashMid)
		if e != nil {
			return e
		}
		jChildImmediate, e = tx.Job(trashChildID)
		return e
	})).To(Succeed())
	Expect(jChildImmediate.CancelRequested).To(BeTrue())
	Expect(srTrashImmediate.Report.Structural.Skipped).To(BeNumerically(">=", 1))
	Expect(srTrashImmediate.Report.Structural.Excluded).To(BeNumerically(">=", 1))
	Expect(srTrashImmediate.Report.Findings.Stale).To(Equal(1))
	Expect(srTrashImmediate.Report.IncompleteReferenceRepairs).To(ContainElement(vTrashRepair))
	Expect(srTrashImmediate.Report.RescanRecommended).To(BeTrue())
	Expect(string(jTrashImmediate.Result)).To(ContainSubstring(vTrashRepair))

	// Drive dispatcher to settle both the cancelled child and the parent scan.
	jTrashMidFinal := runScanViaDispatcher(s, f.bytes, jobTrashMid, 5*time.Second)
	Expect(jTrashMidFinal.State).To(Equal("completed"))
	jChildFinal, err := s.Job(ctx, trashChildID)
	Expect(err).NotTo(HaveOccurred())
	Expect(jChildFinal.State).To(Equal("cancelled"))

	var providerAttemptsCount int
	Expect(s.db.QueryRow(`SELECT count(*) FROM provider_attempts`).Scan(&providerAttemptsCount)).To(Succeed())
	Expect(providerAttemptsCount).To(BeZero(), "deterministic scan and backlink repair must never create provider attempts")
})

var _ = It("T-008-01-03 never rediscovers or restores trashed, purged or denied items across scan, repair, restart and checkpoint resumption", Label("persistence"), func() {
	s, auth, w, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "T-008-01-03 owner")
	f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}

	// Publish vActive with a declared source whose project mapping is absent (unregistered/denied), and verify it does not block vActive structure inspection or invent findings.
	vActive := publishInProjectWithSources(f, "", nil, "<html><body><p>ordinary regionless page</p></body></html>", map[string]map[string]any{
		"unmapped-source": {"kind": "files", "snapshot_sha256": strings.Repeat("a", 64)},
	})
	f.run(vActive, "search")
	f.run(vActive, "references")

	vTrash := f.publish(nil, "<p id='t1'>trashed doc</p>")
	f.run(vTrash, "search")
	f.run(vTrash, "references")
	verTrash, err := s.Version(ctx, vTrash)
	Expect(err).NotTo(HaveOccurred())

	vPurge := f.publish(nil, "<p id='p1'>purged doc</p>")
	f.run(vPurge, "search")
	f.run(vPurge, "references")
	verPurge, err := s.Version(ctx, vPurge)
	Expect(err).NotTo(HaveOccurred())

	// Trash vTrash and trash+purge vPurge BEFORE starting an all-project scan.
	trashRes := applyDocumentLifecycle(s, auth, f.bytes, owner.Token, verTrash.DocumentID, "trash")
	Expect(trashRes.Lifecycle).To(Equal("trash"))

	trashRes2 := applyDocumentLifecycle(s, auth, f.bytes, owner.Token, verPurge.DocumentID, "trash")
	Expect(trashRes2.Lifecycle).To(Equal("trash"))
	purgeRes := applyDocumentLifecycle(s, auth, f.bytes, owner.Token, verPurge.DocumentID, "purge")
	Expect(purgeRes.Lifecycle).To(Equal("purged"))
	runPurgeTurns(s, f.bytes, purgeRes.JobID, 32)
	Expect(f.bytes.files).NotTo(HaveKey(digest([]byte("<p id='p1'>purged doc</p>"))))
	verActiveAfter, err := s.Version(ctx, vActive)
	Expect(err).NotTo(HaveOccurred())
	Expect(f.bytes.files).To(HaveKey(verActiveAfter.ManifestSHA))

	h, err := s.Health(ctx)
	Expect(err).NotTo(HaveOccurred())
	projectID := h.Library.UnfiledProjectID

	scanSvc := scan.New(s, auth, f.bytes, nil)
	jobID := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		ProjectIDs: []string{projectID},
		Depth:      "full_scope",
	})
	jDone := runScanTurns(s, f.bytes, jobID, 64)
	Expect(jDone.State).To(Equal("completed"))

	// Verify trashed and purged documents remain trash/purged and were never restored.
	docTrashAfter, err := s.Document(ctx, verTrash.DocumentID)
	Expect(err).NotTo(HaveOccurred())
	Expect(docTrashAfter.Lifecycle).To(Equal("trash"))
	docPurgeAfter, err := s.Document(ctx, verPurge.DocumentID)
	Expect(err).NotTo(HaveOccurred())
	Expect(docPurgeAfter.Lifecycle).To(Equal("purged"))

	// Verify captured roster recorded vTrash as excluded, vActive as eligible, and never rediscovered already-purged vPurge.
	captured := allCapturedVersions(s, jobID)
	var sawActive, sawTrashedExcluded, sawPurgedRediscovered bool
	for _, cv := range captured {
		if cv.VersionID == vActive && cv.ExclusionReason == "" {
			sawActive = true
		}
		if cv.DocumentID == verTrash.DocumentID && cv.ExclusionReason == "document_trashed" {
			sawTrashedExcluded = true
		}
		if cv.DocumentID == verPurge.DocumentID || cv.VersionID == vPurge {
			sawPurgedRediscovered = true
		}
	}
	for _, it := range allJobItems(s, jobID) {
		if it.DocumentID == verPurge.DocumentID || it.VersionID == vPurge {
			sawPurgedRediscovered = true
		}
	}
	Expect(sawActive).To(BeTrue())
	Expect(sawTrashedExcluded).To(BeTrue())
	Expect(sawPurgedRediscovered).To(BeFalse())

	// Verify ordinary regionless page in vActive produced 0 findings and completed cleanly.
	findingsList, err := scanSvc.ListJobFindings(ctx, owner.Token, domain.FindingFilter{JobID: jobID})
	Expect(err).NotTo(HaveOccurred())
	Expect(findingsList.Items).To(BeEmpty())

	// Verify a regionless, metadata-less version containing an ordinary page, subdirectory, unpreviewable binary file, and valid symlink completes structural inspection with zero false integrity failures, preserves exact_link_text, and never attempts to read an empty object hash.
	vSymVer, vSymDoc, vSymSet := f.id(), f.id(), f.id()
	symManifestRaw, _ := json.Marshal(map[string]any{"schema_version": "1.0", "viewer_profile": "html_js_v1", "references": []any{}})
	symPageBody := "<html><body><p>symlink package</p></body></html>"
	symRawBytes := "\x00\x01\x02\xffunpreviewable"
	symManifestHash := digest(symManifestRaw)
	symPageHash := digest([]byte(symPageBody))
	symRawHash := digest([]byte(symRawBytes))
	f.bytes.files[symManifestHash] = string(symManifestRaw)
	f.bytes.files[symPageHash] = symPageBody
	f.bytes.files[symRawHash] = symRawBytes
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.CreateContentSet(vSymSet, digest(nil)); e != nil {
			return e
		}
		for _, o := range []domain.Object{
			{Hash: symManifestHash, Size: int64(len(symManifestRaw)), State: "ready"},
			{Hash: symPageHash, Size: int64(len(symPageBody)), State: "ready"},
			{Hash: symRawHash, Size: int64(len(symRawBytes)), State: "ready"},
		} {
			if e := tx.PutDurableObject(o); e != nil {
				return e
			}
		}
		for _, ent := range []domain.Entry{
			{ContentSetID: vSymSet, Path: "assets", Kind: "directory"},
			{ContentSetID: vSymSet, Path: "assets/alias-dangling.bin", Kind: "symlink", LinkText: "../outside-dangling-target.bin"},
			{ContentSetID: vSymSet, Path: "assets/raw.bin", Kind: "file", Hash: symRawHash, Size: int64(len(symRawBytes))},
			{ContentSetID: vSymSet, Path: "index.html", Kind: "file", Hash: symPageHash, Size: int64(len(symPageBody))},
		} {
			if e := tx.PutEntry(ent); e != nil {
				return e
			}
		}
		if e := tx.SealContentSet(vSymSet); e != nil {
			return e
		}
		return tx.FinalizeImport(
			domain.ImportIntent{NewDocument: true, DocumentID: vSymDoc, ProjectID: projectID, DocumentRevision: 1, Title: "SymlinkPkg"},
			domain.Version{ID: vSymVer, DocumentID: vSymDoc, ContentSetID: vSymSet, ManifestSHA: symManifestHash, PayloadSHA: digest(nil), Title: "SymlinkPkg", ViewerProfile: "html_js_v1", SourceBasis: "unverified"},
		)
	})).To(Succeed())
	f.run(vSymVer, "search")
	f.run(vSymVer, "references")

	spyReader := &spyEmptyHashReader{inner: f.bytes}
	jobSym := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID: vSymVer,
		Depth:     "full_scope",
	})
	jSymDone := runScanTurns(s, spyReader, jobSym, 32)
	Expect(jSymDone.State).To(Equal("completed"))
	Expect(spyReader.emptyReads).To(BeZero(), "worker must never read an empty object hash for directory or symlink entries")

	var srSym domain.ScanRun
	var preservedSymEntry domain.Entry
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		srSym, e = tx.ScanRun(jobSym)
		if e != nil {
			return e
		}
		preservedSymEntry, e = tx.QueryEntry(vSymSet, "assets/alias-dangling.bin")
		return e
	})).To(Succeed())
	Expect(preservedSymEntry.Kind).To(Equal("symlink"))
	Expect(preservedSymEntry.Hash).To(BeEmpty())
	Expect(preservedSymEntry.LinkText).To(Equal("../outside-dangling-target.bin"))
	Expect(srSym.Denominator.Versions).To(Equal(1))
	Expect(srSym.Denominator.Pages).To(Equal(1))
	Expect(srSym.Denominator.Regions).To(Equal(0))
	Expect(srSym.Denominator.Entries).To(Equal(4))
	Expect(srSym.Denominator.Sources).To(Equal(0))
	Expect(srSym.Denominator.Indexes).To(Equal(1))
	Expect(srSym.Denominator.TotalUnits).To(Equal(8))
	Expect(srSym.Report.Structural.Checked).To(Equal(7))
	Expect(srSym.Report.Structural.Skipped).To(Equal(1))
	Expect(srSym.Report.Structural.Failed).To(Equal(0))
	Expect(srSym.Report.Structural.Blocked).To(Equal(0))
	Expect(srSym.Report.Structural.RegionlessVersions).To(Equal(1))
	Expect(srSym.Report.Structural.MetadataLessVersions).To(Equal(1))
	var sawSymItem, sawDirItem, sawBinItem bool
	for _, it := range allJobItems(s, jobSym) {
		if it.UnitKind == "entry" {
			Expect(it.State).To(Equal("completed"))
			Expect(it.Reason).To(Equal("ok"))
			var det map[string]any
			Expect(json.Unmarshal(it.Detail, &det)).To(Succeed())
			switch it.PagePath {
			case "assets/alias-dangling.bin":
				Expect(det["kind"]).To(Equal("symlink"))
				Expect(det["link_text"]).To(Equal("../outside-dangling-target.bin"))
				Expect(det["verified"]).To(Equal(true))
				sawSymItem = true
			case "assets":
				Expect(det["kind"]).To(Equal("directory"))
				Expect(det["verified"]).To(Equal(true))
				sawDirItem = true
			case "assets/raw.bin":
				Expect(det["kind"]).To(Equal("file"))
				Expect(det["hash"]).To(Equal(symRawHash))
				Expect(det["verified"]).To(Equal(true))
				sawBinItem = true
			}
		}
	}
	Expect(sawSymItem).To(BeTrue())
	Expect(sawDirItem).To(BeTrue())
	Expect(sawBinItem).To(BeTrue())

	// Explicit workflow grant derivation (post_work_scan_enabled: false vs true) and explicit denied source authority on a genuinely readable Git source (owner observes available, workflow credential without source.read observes denied/source_denied).
	gitDeniedDir := filepath.Join(GinkgoT().TempDir(), "repo-denied-check")
	revDeniedHead := initTestGitRepo(gitDeniedDir, map[string]string{"main.go": "package main\n"})
	configureProjectSources(s, projectID, []map[string]any{
		{
			"source_id":      "repo-denied-check",
			"kind":           "git",
			"root_path":      gitDeniedDir,
			"repo_id":        "repo-denied-check",
			"retention_mode": "reference_only",
			"provider_ids":   []string{},
		},
	})
	vReadable := publishInProjectWithSources(f, projectID, nil, "<p id='rd'>readable doc</p>", map[string]map[string]any{
		"repo-denied-check": {"kind": "git", "repo_id": "repo-denied-check", "exact_revision": revDeniedHead},
	}, map[string]any{"page_path": "page.html", "id": "rd", "label": "RD"})
	f.run(vReadable, "search")
	f.run(vReadable, "references")

	// Owner baseline scan on vReadable observes available source and completes source unit.
	jobOwnerReadable := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID: vReadable,
		Depth:     "change_focused",
	})
	jOwnerReadableDone := runScanTurns(s, f.bytes, jobOwnerReadable, 32)
	Expect(jOwnerReadableDone.State).To(Equal("completed"))
	ownerReadableVers := allCapturedVersions(s, jobOwnerReadable)
	Expect(ownerReadableVers).To(HaveLen(1))
	Expect(ownerReadableVers[0].Targets).To(HaveLen(1))
	Expect(ownerReadableVers[0].Targets[0].Availability).To(Equal("available"))

	verReadableRow, err := s.Version(ctx, vReadable)
	Expect(err).NotTo(HaveOccurred())
	wfDeniedBearer, err := sec.Secret()
	Expect(err).NotTo(HaveOccurred())
	_, err = auth.IssueWorkflowGrant(ctx, owner.Token, f.id(), appauth.WorkflowGrantRequest{
		BindingID:                 "00000000-0000-4000-8000-000000000800",
		CredentialID:              "00000000-0000-4000-8000-000000000801",
		TokenVerifier:             sec.Verifier("workflow", wfDeniedBearer),
		Workflow:                  "scan",
		ProjectID:                 projectID,
		DocumentID:                &verReadableRow.DocumentID,
		VersionIDs:                []string{vReadable},
		ProviderID:                "codex",
		ExpectedSelectionRevision: 0,
	})
	Expect(err).To(MatchError(domain.ErrDenied))

	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var body map[string]any
		Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
		projects, _ := body["projects"].(map[string]any)
		if projects == nil {
			projects = map[string]any{}
			body["projects"] = projects
		}
		proj, _ := projects[projectID].(map[string]any)
		if proj == nil {
			proj = map[string]any{}
			projects[projectID] = proj
		}
		proj["post_work_scan_enabled"] = true
		raw, _ := json.Marshal(body)
		cfg.Value = raw
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())

	wfToken, err := sec.Secret()
	Expect(err).NotTo(HaveOccurred())
	wfBindingID := "00000000-0000-4000-8000-000000000802"
	wfCredID := "00000000-0000-4000-8000-000000000803"
	draftJobID := "00000000-0000-4000-8000-000000000804"
	draftID := "00000000-0000-4000-8000-000000000805"
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.CreateJob(domain.Job{
			ID:             draftJobID,
			Kind:           "generation",
			Scope:          []byte("{}"),
			ConfigRevision: 1,
		}); e != nil {
			return e
		}
		return tx.CreateDraft(domain.Draft{
			ID:                  draftID,
			ProjectID:           projectID,
			DocumentID:          &verReadableRow.DocumentID,
			BaseVersionID:       &vReadable,
			JobID:               draftJobID,
			AllocatedDocumentID: verReadableRow.DocumentID,
			ProposedVersionID:   "00000000-0000-4000-8000-000000000806",
			State:               "preparing",
			Intent:              "draft",
		})
	})).To(Succeed())
	// A draft_write workflow grant on a project with post_work_scan_enabled: true derives scan.request + version.read + project.read without source.read.
	_, err = auth.IssueWorkflowGrant(ctx, owner.Token, f.id(), appauth.WorkflowGrantRequest{
		BindingID:                 wfBindingID,
		CredentialID:              wfCredID,
		TokenVerifier:             sec.Verifier("workflow", wfToken),
		Workflow:                  "draft_write",
		ProjectID:                 projectID,
		DocumentID:                &verReadableRow.DocumentID,
		VersionIDs:                []string{vReadable},
		DraftIDs:                  []string{draftID},
		ProviderID:                "codex",
		ExpectedSelectionRevision: 0,
	})
	Expect(err).NotTo(HaveOccurred())
	jobDenied := startScanJob(scanSvc, wfToken, domain.ScanRequest{
		VersionID: vReadable,
		Depth:     "change_focused",
	})
	jDeniedDone := runScanTurns(s, f.bytes, jobDenied, 32)
	Expect(jDeniedDone.State).To(Equal("completed"))
	deniedVers := allCapturedVersions(s, jobDenied)
	Expect(deniedVers).To(HaveLen(1))
	Expect(deniedVers[0].Targets).To(HaveLen(1))
	Expect(deniedVers[0].Targets[0].Availability).To(Equal("denied"))
	Expect(deniedVers[0].Targets[0].Reason).To(Equal("source_denied"))
	var sawDeniedSourceItem bool
	for _, it := range allJobItems(s, jobDenied) {
		if it.UnitKind == "source" {
			Expect(it.State).To(Equal("blocked"))
			Expect(it.Reason).To(Equal("source_denied"))
			sawDeniedSourceItem = true
		}
	}
	Expect(sawDeniedSourceItem).To(BeTrue())

	// Exact-version authority regression: workflow grant anchored to vReadable (A) vs unauthorized vUnauthorizedB (B) in the same project, empty read anchor, and post-capture principal broadening/narrowing.
	vUnauthorizedB := publishInProjectWithSources(f, projectID, nil, "<p id='ub'>unauthorized doc B</p>", map[string]map[string]any{
		"repo-denied-check": {"kind": "git", "repo_id": "repo-denied-check", "exact_revision": revDeniedHead},
	}, map[string]any{"page_path": "page.html", "id": "ub", "label": "UB"})
	f.run(vUnauthorizedB, "search")
	f.run(vUnauthorizedB, "references")
	verUnauthBRow, err := s.Version(ctx, vUnauthorizedB)
	Expect(err).NotTo(HaveOccurred())

	probeCounts := map[string]int{}
	scanSvc.OnSourceProbe = func(_, verID, _, _ string) {
		probeCounts[verID]++
	}
	defer func() { scanSvc.OnSourceProbe = nil }()

	wfScanAToken, err := sec.Secret()
	Expect(err).NotTo(HaveOccurred())
	wfScanABinding := "00000000-0000-4000-8000-000000000807"
	wfScanACred := "00000000-0000-4000-8000-000000000808"
	_, err = auth.IssueWorkflowGrant(ctx, owner.Token, f.id(), appauth.WorkflowGrantRequest{
		BindingID:                 wfScanABinding,
		CredentialID:              wfScanACred,
		TokenVerifier:             sec.Verifier("workflow", wfScanAToken),
		Workflow:                  "scan",
		ProjectID:                 projectID,
		DocumentID:                &verReadableRow.DocumentID,
		VersionIDs:                []string{vReadable},
		ProviderID:                "codex",
		ExpectedSelectionRevision: 0,
	})
	Expect(err).NotTo(HaveOccurred())

	// 1. Authorized version A baseline: admission probes A and worker completes A with available source.
	jobAuthA := startScanJob(scanSvc, wfScanAToken, domain.ScanRequest{
		VersionID: vReadable,
		Depth:     "change_focused",
	})
	Expect(probeCounts[vReadable]).To(Equal(1))
	Expect(probeCounts[vUnauthorizedB]).To(Equal(0))
	jAuthADone := runScanTurns(s, f.bytes, jobAuthA, 32)
	Expect(jAuthADone.State).To(Equal("completed"))
	authAVers := allCapturedVersions(s, jobAuthA)
	Expect(authAVers).To(HaveLen(1))
	Expect(authAVers[0].Targets).To(HaveLen(1))
	Expect(authAVers[0].Targets[0].Availability).To(Equal("available"))

	// 2. Explicit scan request for unauthorized version B (by version_id or document_id) is rejected at admission before any repository probe or worker item creation.
	rawUnauthVer, _ := json.Marshal(domain.ScanRequest{VersionID: vUnauthorizedB, Depth: "change_focused"})
	_, err = scanSvc.StartScan(ctx, wfScanAToken, f.id(), rawUnauthVer, domain.ScanRequest{VersionID: vUnauthorizedB, Depth: "change_focused"})
	Expect(err).To(MatchError(domain.ErrNotFound))
	Expect(probeCounts[vUnauthorizedB]).To(Equal(0))

	rawUnauthDoc, _ := json.Marshal(domain.ScanRequest{DocumentID: verUnauthBRow.DocumentID, Depth: "change_focused"})
	_, err = scanSvc.StartScan(ctx, wfScanAToken, f.id(), rawUnauthDoc, domain.ScanRequest{DocumentID: verUnauthBRow.DocumentID, Depth: "change_focused"})
	Expect(err).To(MatchError(domain.ErrNotFound))
	Expect(probeCounts[vUnauthorizedB]).To(Equal(0))

	// 3. Project-scoped scan under wfScanAToken (anchored only to A) probes/inspects A while excluding unanchored B at admission with zero repository probes, zero worker byte reads and zero repair on B.
	jobProjScopedA := startScanJob(scanSvc, wfScanAToken, domain.ScanRequest{
		ProjectID: projectID,
		Depth:     "change_focused",
	})
	Expect(probeCounts[vReadable]).To(Equal(2))
	Expect(probeCounts[vUnauthorizedB]).To(Equal(0))
	jProjScopedADone := runScanTurns(s, f.bytes, jobProjScopedA, 32)
	Expect(jProjScopedADone.State).To(Equal("completed"))
	var sawBExcluded bool
	for _, cv := range allCapturedVersions(s, jobProjScopedA) {
		if cv.DocumentID == verUnauthBRow.DocumentID {
			Expect(cv.ExclusionReason).To(Equal("authority_denied"))
			Expect(cv.VersionID).To(BeEmpty())
			Expect(cv.Targets).To(BeEmpty())
			sawBExcluded = true
		}
	}
	Expect(sawBExcluded).To(BeTrue())

	// 4. Empty read anchor (VersionIDs: []) does not silently authorize exact-version source probes or explicit version scans.
	wfEmptyAnchorToken, err := sec.Secret()
	Expect(err).NotTo(HaveOccurred())
	_, err = auth.IssueWorkflowGrant(ctx, owner.Token, f.id(), appauth.WorkflowGrantRequest{
		BindingID:                 "00000000-0000-4000-8000-000000000809",
		CredentialID:              "00000000-0000-4000-8000-000000000810",
		TokenVerifier:             sec.Verifier("workflow", wfEmptyAnchorToken),
		Workflow:                  "scan",
		ProjectID:                 projectID,
		VersionIDs:                []string{},
		ProviderID:                "codex",
		ExpectedSelectionRevision: 0,
	})
	Expect(err).NotTo(HaveOccurred())
	rawEmptyExplicit, _ := json.Marshal(domain.ScanRequest{VersionID: vReadable, Depth: "change_focused"})
	_, err = scanSvc.StartScan(ctx, wfEmptyAnchorToken, f.id(), rawEmptyExplicit, domain.ScanRequest{VersionID: vReadable, Depth: "change_focused"})
	Expect(err).To(MatchError(domain.ErrNotFound))
	Expect(probeCounts[vReadable]).To(Equal(2))
	jobEmptyProj := startScanJob(scanSvc, wfEmptyAnchorToken, domain.ScanRequest{
		ProjectID: projectID,
		Depth:     "change_focused",
	})
	Expect(probeCounts[vReadable]).To(Equal(2))
	Expect(probeCounts[vUnauthorizedB]).To(Equal(0))
	jEmptyProjDone := runScanTurns(s, f.bytes, jobEmptyProj, 32)
	Expect(jEmptyProjDone.State).To(Equal("completed"))
	for _, cv := range allCapturedVersions(s, jobEmptyProj) {
		Expect(cv.ExclusionReason).NotTo(BeEmpty())
		Expect(cv.VersionID).To(BeEmpty())
	}

	// 5. Captured scope intersection: broadening the principal after scan admission cannot expand captured authority, and narrowing the principal revokes worker/repair authority without fabricating VersionIDs.
	jobScopeFence := startScanJob(scanSvc, wfScanAToken, domain.ScanRequest{
		VersionID: vReadable,
		Depth:     "change_focused",
	})
	// 5a. Broaden principal in store to [vReadable, vUnauthorizedB]: RestrictScope(run.CapturedScope, wp.Scope) keeps intersection at [vReadable] and still rejects vUnauthorizedB.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		wp, e := tx.WorkflowPrincipalByBinding(wfScanABinding)
		if e != nil {
			return e
		}
		wp.Scope.VersionIDs = []string{vReadable, vUnauthorizedB}
		if e := tx.PutWorkflowPrincipal(wp); e != nil {
			return e
		}
		runLoaded, e := tx.ScanRun(jobScopeFence)
		if e != nil {
			return e
		}
		intersected := appauth.RestrictScope(runLoaded.CapturedScope, wp.Scope)
		Expect(intersected.VersionIDs).To(Equal([]string{vReadable}))
		actor := domain.Session{PrincipalID: runLoaded.PrincipalID, Scope: intersected}
		_, _, errA := app.CheckSourceVersion(tx, actor, vReadable)
		Expect(errA).NotTo(HaveOccurred())
		_, _, errB := app.CheckSourceVersion(tx, actor, vUnauthorizedB)
		Expect(errB).To(MatchError(domain.ErrNotFound))
		return nil
	})).To(Succeed())
	// 5b. Narrow principal to only [vUnauthorizedB] (removing vReadable so intersection with captured [vReadable] is empty []): worker revokes authority without fabricating VersionIDs.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		wp, e := tx.WorkflowPrincipalByBinding(wfScanABinding)
		if e != nil {
			return e
		}
		wp.Scope.VersionIDs = []string{vUnauthorizedB}
		return tx.PutWorkflowPrincipal(wp)
	})).To(Succeed())
	jScopeFenceDone := runScanTurns(s, f.bytes, jobScopeFence, 32)
	Expect(jScopeFenceDone.State).To(Equal("completed"))
	fenceItems := allJobItems(s, jobScopeFence)
	Expect(fenceItems).NotTo(BeEmpty())
	for _, it := range fenceItems {
		Expect(it.State).To(Equal("blocked"))
		Expect(it.Reason).To(Equal("authority_revoked"))
		Expect(it.RepairJobID).To(BeEmpty())
	}

	// Verify checkpoint resumption across actual store reopen (recoverLocalJobs) does not re-check already completed items or rediscover trashed/purged/denied content.
	jobResume := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID: vActive,
		Depth:     "change_focused",
	})
	wkr := scan.NewWorker(s, f.bytes)
	// Step 1: enumerate inventory
	Expect(wkr.Process(ctx, jobResume)).To(Succeed())
	// Step 2: inspect first unit (item-000001)
	Expect(wkr.Process(ctx, jobResume)).To(Succeed())
	var firstItemBefore domain.JobItem
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		firstItemBefore, e = tx.JobItem(jobResume, "item-000001")
		return e
	})).To(Succeed())
	Expect(firstItemBefore.State).To(Equal("completed"))

	// Close and reopen the store while jobResume is still 'running' -> recoverLocalJobs transitions it to 'interrupted'.
	Expect(s.Close()).To(Succeed())
	s = New(nil)
	_, closeReopened, err := s.Prepare(ctx, w.Profile)
	Expect(err).NotTo(HaveOccurred())
	defer closeReopened()
	f.s = s
	reopenedJob, err := s.Job(ctx, jobResume)
	Expect(err).NotTo(HaveOccurred())
	Expect(reopenedJob.State).To(Equal("interrupted"))

	jResumed := runScanTurns(s, f.bytes, jobResume, 32)
	Expect(jResumed.State).To(Equal("completed"))

	var firstItemAfter domain.JobItem
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		firstItemAfter, e = tx.JobItem(jobResume, "item-000001")
		return e
	})).To(Succeed())
	Expect(firstItemAfter.UpdatedAt).To(Equal(firstItemBefore.UpdatedAt), "completed unit before restart must not be re-executed after resume")
})

var _ = It("SCAN-GUARDS-01 enforces revision 20 scan schema declarations, early cancel materialization, child repair fencing, active-child protected restore and duration budget", Label("persistence"), func() {
	s, auth, w, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "SCAN-GUARDS-01 owner")
	f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}

	Expect(s.validateTables(ctx, SchemaRevision)).To(Succeed())
	var savedIndexSQL string
	Expect(s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='index' AND name='findings_by_job_order'`).Scan(&savedIndexSQL)).To(Succeed())
	Expect(savedIndexSQL).NotTo(BeEmpty())
	_, err := s.db.Exec(`DROP INDEX findings_by_job_order`)
	Expect(err).NotTo(HaveOccurred())
	Expect(s.validateTables(ctx, SchemaRevision)).To(MatchError("missing scan schema declaration"))
	_, err = s.db.Exec(savedIndexSQL)
	Expect(err).NotTo(HaveOccurred())
	Expect(s.validateTables(ctx, SchemaRevision)).To(Succeed())

	v2 := f.publish(nil, "<p id='retry'>two</p>")
	f.run(v2, "search")
	f.run(v2, "references")
	v1 := f.publish([]any{f.declaration(v2, "RESTORE_REF")}, "<p id='retry'>one</p>")
	f.run(v1, "search")
	f.run(v1, "references")

	scanSvc := scan.New(s, auth, f.bytes, nil)
	unfiledProj := mustUnfiledProject(s)
	// Test 1: Queued CancelJob before worker enumeration materializes opaque cancelled unit rows preserving denominator.
	jobCancel := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		ProjectIDs: []string{unfiledProj},
		Depth:      "change_focused",
	})
	var denomCancel domain.ScanRun
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		denomCancel, e = tx.ScanRun(jobCancel)
		if e != nil {
			return e
		}
		_, e = tx.CancelJob(jobCancel)
		return e
	})).To(Succeed())
	cancelledJob, err := s.Job(ctx, jobCancel)
	Expect(err).NotTo(HaveOccurred())
	Expect(cancelledJob.State).To(Equal("cancelled"))
	cancelledItems := allJobItems(s, jobCancel)
	Expect(cancelledItems).To(HaveLen(denomCancel.Denominator.TotalUnits))
	for _, it := range cancelledItems {
		Expect(it.State).To(Equal("cancelled"))
	}

	// Test 2: Child repair generation fence refuses stale item.AttemptGeneration when parent generation advances.
	ver1FenceRow, err := s.Version(ctx, v1)
	Expect(err).NotTo(HaveOccurred())
	jobFence := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID: v1,
		Depth:     "change_focused",
	})
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		claimed, e := tx.ClaimJob(jobFence)
		if e != nil {
			return e
		}
		item := domain.JobItem{
			JobID:             jobFence,
			ItemID:            "item-000001",
			Ordinal:           1,
			UnitKind:          "index",
			ProjectID:         unfiledProj,
			DocumentID:        ver1FenceRow.DocumentID,
			VersionID:         v1,
			Stage:             "repair",
			State:             "running",
			AttemptGeneration: claimed.AttemptGeneration + 1, // mismatched generation
			Detail:            []byte("{}"),
			UpdatedAt:         "2026-11-08T00:00:00Z",
		}
		return tx.PutJobItem(item)
	})).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.RebuildScanReferences(jobFence, "item-000001", v1)
		return e
	})).To(MatchError(domain.ErrStaleWorker))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		it, e := tx.JobItem(jobFence, "item-000001")
		if e != nil {
			return e
		}
		claimed, e := tx.Job(jobFence)
		if e != nil {
			return e
		}
		Expect(it.AttemptGeneration).To(Equal(claimed.AttemptGeneration + 1))
		Expect(it.RepairJobID).To(BeEmpty())
		cJob, e := tx.CancelJob(jobFence)
		if e != nil {
			return e
		}
		return tx.ConfirmStop(jobFence, cJob.AttemptGeneration)
	})).To(Succeed())

	// Test 3: Active-owned-child protected restore: acquire a genuine repair child via normal worker turns on a version with reference divergence, then verify ReviewRecovery cancels only the owned derived_index child, clears its hold, requeues the parent, excludes restore hold duration from max_run_seconds, and normal worker turns complete the parent with blocked finding (restored_repair_cancelled), rescan_recommended=true, zero leaked job pins, and untouched unrelated derived_index jobs.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var body map[string]any
		Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
		maint, _ := body["maintenance"].(map[string]any)
		if maint == nil {
			maint = map[string]any{}
			body["maintenance"] = maint
		}
		maint["max_run_seconds"] = 60
		raw, _ := json.Marshal(body)
		cfg.Value = raw
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
	setBacklinkRepairMode(s, "", "auto")
	_, err = s.db.Exec(`DELETE FROM authored_edges WHERE from_version_id=?;DELETE FROM authored_references WHERE from_version_id=?`, v1, v1)
	Expect(err).NotTo(HaveOccurred())
	jobRestore := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID: v1,
		Depth:     "full_scope",
	})
	restoreClock := time.Now().UTC()
	restoreWorker := scan.NewWorker(s, f.bytes)
	restoreWorker.Now = func() time.Time { return restoreClock }
	var ownedChildID, restoreIndexItemID string
	for turn := 0; turn < 16 && ownedChildID == ""; turn++ {
		Expect(restoreWorker.Process(ctx, jobRestore)).To(Succeed())
		for _, it := range allJobItems(s, jobRestore) {
			if it.UnitKind == "index" && it.RepairJobID != "" {
				ownedChildID = it.RepairJobID
				restoreIndexItemID = it.ItemID
				break
			}
		}
	}
	Expect(ownedChildID).NotTo(BeEmpty())
	Expect(restoreIndexItemID).NotTo(BeEmpty())

	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		// Create an unrelated derived_index job on v2 to verify it is NOT cancelled when jobRestore is reviewed.
		return tx.CreateJob(domain.Job{
			ID:    "unrelated-derived-job",
			Kind:  "derived_index",
			Scope: jsonBytes(map[string]any{"version_id": v2, "work_kind": "references"}),
		})
	})).To(Succeed())

	copiedPath := filepath.Join(GinkgoT().TempDir(), "restored.sqlite")
	Expect(s.SnapshotDatabase(ctx, copiedPath)).To(Succeed())
	_, err = ProtectRestoredDatabase(ctx, copiedPath, w.Profile, "backup-restore-test", json.RawMessage(`{}`), func(map[string]any) error { return nil })
	Expect(err).NotTo(HaveOccurred())

	// Open the restored DB in a fresh store and run ReviewRecovery to exercise active-child recovery reconciliation.
	restoredProfile := service.Profile{
		ProfileID:    w.Profile.ProfileID,
		LibraryID:    w.Profile.LibraryID,
		DataDir:      filepath.Dir(filepath.Dir(copiedPath)),
		StorageState: "ready",
	}
	Expect(os.MkdirAll(filepath.Join(restoredProfile.DataDir, "storage"), 0o700)).To(Succeed())
	restoredStorePath := filepath.Join(restoredProfile.DataDir, "storage", "library.sqlite")
	rawRestoredBytes, err := os.ReadFile(copiedPath)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(restoredStorePath, rawRestoredBytes, 0o600)).To(Succeed())

	rs := New(nil)
	_, closeRS, err := rs.Prepare(ctx, restoredProfile)
	Expect(err).NotTo(HaveOccurred())
	defer closeRS()
	var pausedRestoreRun domain.ScanRun
	Expect(rs.Write(ctx, func(tx app.WriteTx) error {
		var e error
		pausedRestoreRun, e = tx.ScanRun(jobRestore)
		if e != nil {
			return e
		}
		return tx.ReviewRecovery("backup-restore-test", []string{jobRestore})
	})).To(Succeed())
	Expect(pausedRestoreRun.StartedAt).To(BeNil(), "pauseRestoredScanBudgets must clear started_at while on restore hold")

	parentAfterReview, err := rs.Job(ctx, jobRestore)
	Expect(err).NotTo(HaveOccurred())
	Expect(parentAfterReview.State).To(Equal("queued"))
	childAfterReview, err := rs.Job(ctx, ownedChildID)
	Expect(err).NotTo(HaveOccurred())
	Expect(childAfterReview.State).To(Equal("cancelled"))
	unrelatedAfterReview, err := rs.Job(ctx, "unrelated-derived-job")
	Expect(err).NotTo(HaveOccurred())
	Expect(unrelatedAfterReview.State).To(Equal("paused"), "unrelated derived_index job must remain paused rather than cancelled by scan recovery")

	var childHoldRemaining, parentHoldRemaining bool
	Expect(rs.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM resource_revisions WHERE resource_kind='restore_job_hold' AND resource_id=?)`, ownedChildID).Scan(&childHoldRemaining)).To(Succeed())
	Expect(childHoldRemaining).To(BeFalse())
	Expect(rs.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM resource_revisions WHERE resource_kind='restore_job_hold' AND resource_id=?)`, jobRestore).Scan(&parentHoldRemaining)).To(Succeed())
	Expect(parentHoldRemaining).To(BeFalse())

	// Advance clock by 120s while jobRestore was held/queued before post-review ClaimJob (exceeding max_run_seconds=60 if hold duration were counted), then drive worker turns to completion.
	restoreClock = restoreClock.Add(120 * time.Second)
	rsWorker := scan.NewWorker(rs, f.bytes)
	rsWorker.Now = func() time.Time { return restoreClock }
	var parentTerminal domain.Job
	for step := 0; step < 32; step++ {
		Expect(rsWorker.Process(ctx, jobRestore)).To(Succeed())
		parentTerminal, err = rs.Job(ctx, jobRestore)
		Expect(err).NotTo(HaveOccurred())
		if parentTerminal.State == "completed" {
			break
		}
	}
	Expect(parentTerminal.State).To(Equal("completed"))

	var runAfterReview domain.ScanRun
	var itemAfterReview domain.JobItem
	var findingAfterReview domain.Finding
	Expect(rs.Write(ctx, func(tx app.WriteTx) error {
		var e error
		runAfterReview, e = tx.ScanRun(jobRestore)
		if e != nil {
			return e
		}
		itemAfterReview, e = tx.JobItem(jobRestore, restoreIndexItemID)
		if e != nil {
			return e
		}
		findingAfterReview, e = tx.Finding(itemAfterReview.FindingID)
		return e
	})).To(Succeed())
	Expect(runAfterReview.Report.BudgetExhausted).To(BeFalse(), "restore hold duration before post-review ClaimJob must not count against max_run_seconds")
	Expect(itemAfterReview.State).To(Equal("blocked"))
	Expect(itemAfterReview.Reason).To(Equal("restored_repair_cancelled"))
	Expect(itemAfterReview.RepairJobID).To(Equal(ownedChildID), "must not spawn a new repair child after restored_repair_cancelled")
	Expect(findingAfterReview.Status).To(Equal("blocked"))
	Expect(findingAfterReview.Reason).To(Equal("restored_repair_cancelled"))
	Expect(findingAfterReview.ApprovalRequired).To(BeTrue())
	Expect(runAfterReview.Report.RescanRecommended).To(BeTrue())
	Expect(runAfterReview.Report.IncompleteReferenceRepairs).To(ContainElement(v1))

	var leakedRestorePins int
	Expect(rs.db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='job' AND owner_id=?`, jobRestore).Scan(&leakedRestorePins)).To(Succeed())
	Expect(leakedRestorePins).To(BeZero())

	// Repair v1's reference index on s so subsequent tests in this spec see consistent references on v1.
	f.run(v1, "references")

	// Test 4: max_run_seconds duration budget starts at first ClaimJob (excluding queue wait before ClaimJob) and skips remaining units with run_budget_exhausted.
	jobBudget := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID: v1,
		Depth:     "change_focused",
	})
	currentClock := time.Now().UTC().Add(120 * time.Second) // 120s queue wait before first ClaimJob (> max_run_seconds=60)
	budgetWkr := scan.NewWorker(s, f.bytes)
	budgetWkr.Now = func() time.Time { return currentClock }
	// First turn claims job at currentClock and enumerates inventory without exhausting budget during queue wait.
	Expect(budgetWkr.Process(ctx, jobBudget)).To(Succeed())
	jAfterFirstClaim, err := s.Job(ctx, jobBudget)
	Expect(err).NotTo(HaveOccurred())
	Expect(jAfterFirstClaim.State).To(Equal("running"))
	// Advance clock by 65s of active execution past max_run_seconds (60s) -> next turn exhausts duration budget and skips remaining items.
	currentClock = currentClock.Add(65 * time.Second)
	Expect(budgetWkr.Process(ctx, jobBudget)).To(Succeed())
	jBudgetDone, err := s.Job(ctx, jobBudget)
	Expect(err).NotTo(HaveOccurred())
	Expect(jBudgetDone.State).To(Equal("completed"))
	var runBudget domain.ScanRun
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		runBudget, e = tx.ScanRun(jobBudget)
		return e
	})).To(Succeed())
	Expect(runBudget.Report.BudgetExhausted).To(BeTrue())
	Expect(runBudget.Report.Semantic.Reason).To(Equal("run_budget_exhausted"))
	for _, it := range allJobItems(s, jobBudget) {
		Expect(it.State).To(Equal("skipped"))
		Expect(it.Reason).To(Equal("run_budget_exhausted"))
	}

	// Test 5: Project-level max_run_seconds override narrows global budget at admission, and live runtime narrowing of
	// max_run_seconds enforces the stricter deadline on parent turns and ReferenceFence child batches without rewriting
	// captured run.EffectivePolicy or run.ConfigRevision (while live expansion never enlarges captured budget).
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var body map[string]any
		Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
		maint, _ := body["maintenance"].(map[string]any)
		maint["max_run_seconds"] = 120
		projectsMap := map[string]any{
			unfiledProj: map[string]any{
				"maintenance": map[string]any{
					"max_run_seconds":               45,
					"max_concurrent_ai_jobs":        1,
					"max_provider_attempts_per_run": 3,
				},
			},
		}
		body["projects"] = projectsMap
		raw, _ := json.Marshal(body)
		cfg.Value = raw
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())

	jobProjBudget := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		ProjectIDs: []string{unfiledProj},
		Depth:      "change_focused",
	})
	var runProjBudgetPre domain.ScanRun
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		runProjBudgetPre, e = tx.ScanRun(jobProjBudget)
		return e
	})).To(Succeed())
	Expect(runProjBudgetPre.EffectivePolicy.MaxRunSeconds).To(Equal(45), "project override < global must narrow captured MaxRunSeconds")
	Expect(runProjBudgetPre.EffectivePolicy.MaxConcurrentAIJobs).To(Equal(1))
	Expect(runProjBudgetPre.EffectivePolicy.MaxProviderAttemptsPerRun).To(Equal(3))
	capturedRev := runProjBudgetPre.ConfigRevision

	// Claim jobProjBudget at t0, then live-narrow project max_run_seconds from 45 -> 15 while live-expanding global to 300.
	t0 := time.Now().UTC().Add(300 * time.Second)
	projWkr := scan.NewWorker(s, f.bytes)
	projWkr.Now = func() time.Time { return t0 }
	Expect(projWkr.Process(ctx, jobProjBudget)).To(Succeed())

	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var body map[string]any
		Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
		maint, _ := body["maintenance"].(map[string]any)
		maint["max_run_seconds"] = 300
		projectsMap, _ := body["projects"].(map[string]any)
		pEntry, _ := projectsMap[unfiledProj].(map[string]any)
		pMaint, _ := pEntry["maintenance"].(map[string]any)
		pMaint["max_run_seconds"] = 15
		raw, _ := json.Marshal(body)
		cfg.Value = raw
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())

	// Advance clock by 20s (> narrowed 15s, < captured 45s) -> next turn sees live narrowed 15s and exhausts budget without rewriting captured policy.
	t0 = t0.Add(20 * time.Second)
	Expect(projWkr.Process(ctx, jobProjBudget)).To(Succeed())
	var runProjBudgetPost domain.ScanRun
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		runProjBudgetPost, e = tx.ScanRun(jobProjBudget)
		return e
	})).To(Succeed())
	Expect(runProjBudgetPost.Report.BudgetExhausted).To(BeTrue())
	Expect(runProjBudgetPost.EffectivePolicy.MaxRunSeconds).To(Equal(45), "captured policy snapshot must remain immutable")
	Expect(runProjBudgetPost.ConfigRevision).To(Equal(capturedRev), "captured config revision must remain immutable")
})

var _ = It("SCAN-GUARDS-01 resolves EE-P01 source precedence, multi-source selectors, managed files capture/mutation coalescing and >256 batch pagination", Label("persistence"), func() {
	s, auth, w, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "EE-P01 owner")
	f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}

	projectID := mustUnfiledProject(s)
	gitDirA := filepath.Join(GinkgoT().TempDir(), "repo-a")
	revA1 := initTestGitRepo(gitDirA, map[string]string{"a.go": "package a // v1\n"})
	revA2 := commitTestGitFile(gitDirA, "a.go", "package a // v2\n", "v2")

	gitDirB := filepath.Join(GinkgoT().TempDir(), "repo-b")
	revB1 := initTestGitRepo(gitDirB, map[string]string{"b.go": "package b // v1\n"})
	revB2 := commitTestGitFile(gitDirB, "b.go", "package b // v2\n", "v2")

	filesDir := filepath.Join(GinkgoT().TempDir(), "files-src")
	Expect(os.MkdirAll(filesDir, 0o700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(filesDir, "a.txt"), []byte("file-a-v1\n"), 0o600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(filesDir, "b.txt"), []byte("file-b-v1\n"), 0o600)).To(Succeed())

	configureProjectSources(s, projectID, []map[string]any{
		{
			"source_id":      "repo-a",
			"kind":           "git",
			"root_path":      gitDirA,
			"repo_id":        "repo-a",
			"retention_mode": "reference_only",
			"provider_ids":   []string{},
		},
		{
			"source_id":      "repo-b",
			"kind":           "git",
			"root_path":      gitDirB,
			"repo_id":        "repo-b",
			"retention_mode": "reference_only",
			"provider_ids":   []string{},
		},
		{
			"source_id":      "src-files",
			"kind":           "files",
			"root_path":      filesDir,
			"retention_mode": "selected_snapshot",
			"provider_ids":   []string{},
		},
	})

	// 1. Multi-source version with repo-a (pinned revA1) and repo-b (pinned revB1), while HEADs are at revA2 and revB2.
	vMulti := publishInProjectWithSources(f, projectID, nil, "<p id='m1'>multi</p>", map[string]map[string]any{
		"repo-a": {"kind": "git", "repo_id": "repo-a", "exact_revision": revA1},
		"repo-b": {"kind": "git", "repo_id": "repo-b", "exact_revision": revB1},
	}, map[string]any{"page_path": "page.html", "id": "m1", "label": "Multi"})
	f.run(vMulti, "search")
	f.run(vMulti, "references")

	scanSvc := scan.New(s, auth, f.bytes, nil)
	// Per-source selectors: repo-a pinned at revA1, repo-b current at revB2.
	jobMulti := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID: vMulti,
		Sources: []domain.ScanSourceSelector{
			{ProjectID: projectID, SourceID: "repo-a", Mode: "pinned", Revision: revA1},
			{ProjectID: projectID, SourceID: "repo-b", Mode: "current", Revision: revB2},
		},
	})
	cvMulti := allCapturedVersions(s, jobMulti)
	Expect(cvMulti).To(HaveLen(1))
	Expect(cvMulti[0].Targets).To(HaveLen(2))
	for _, t := range cvMulti[0].Targets {
		switch t.SourceID {
		case "repo-a":
			Expect(t.Mode).To(Equal("pinned"))
			Expect(t.ResolvedRevision).To(Equal(revA1))
			Expect(t.Availability).To(Equal("available"))
		case "repo-b":
			Expect(t.Mode).To(Equal("current"))
			Expect(t.ResolvedRevision).To(Equal(revB2))
			Expect(t.Availability).To(Equal("available"))
			Expect(t.ReassessmentNeeded).To(BeTrue())
		}
	}

	// Reject unknown second source_id in req.Sources.
	_, err := scanSvc.StartScan(ctx, owner.Token, f.id(), nil, domain.ScanRequest{
		VersionID: vMulti,
		Sources: []domain.ScanSourceSelector{
			{ProjectID: projectID, SourceID: "repo-a", Mode: "pinned"},
			{ProjectID: projectID, SourceID: "nonexistent-source", Mode: "pinned"},
		},
	})
	Expect(err).To(MatchError(domain.ErrInvalidTarget))

	// 2. Missing pinned revision never silently falls back to available current HEAD.
	missingRev := strings.Repeat("e", 40)
	vMissingPinned := publishInProjectWithSources(f, projectID, nil, "<p id='mp'>missing pinned</p>", map[string]map[string]any{
		"repo-a": {"kind": "git", "repo_id": "repo-a", "exact_revision": missingRev},
	}, map[string]any{"page_path": "page.html", "id": "mp", "label": "MP"})
	f.run(vMissingPinned, "search")
	f.run(vMissingPinned, "references")
	jobMissingPinned := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID:  vMissingPinned,
		SourceMode: "pinned",
	})
	cvMissing := allCapturedVersions(s, jobMissingPinned)
	Expect(cvMissing).To(HaveLen(1))
	Expect(cvMissing[0].Targets).To(HaveLen(1))
	Expect(cvMissing[0].Targets[0].Availability).To(Equal("unavailable"))
	Expect(cvMissing[0].Targets[0].Reason).To(Equal("revision_unavailable"))
	Expect(cvMissing[0].Targets[0].ResolvedRevision).NotTo(Equal(revA2))

	// 3. Files source: implicit current without capture records unavailable/source_capture_required; incompatible explicit SnapshotID is rejected; managed capture A -> scan A -> mutate second file b.txt -> managed capture B -> distinct scan B.
	vFiles := publishInProjectWithSources(f, projectID, nil, "<p id='vf'>files doc</p>", map[string]map[string]any{
		"src-files": {"kind": "files"},
	}, map[string]any{"page_path": "page.html", "id": "vf", "label": "VF"})
	f.run(vFiles, "search")
	f.run(vFiles, "references")

	// 3a. Implicit current files without capture -> unavailable / source_capture_required, no bytes_origin, no fabricated snapshot.
	jobImplicitFiles := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID:  vFiles,
		SourceMode: "current",
	})
	cvImplicit := allCapturedVersions(s, jobImplicitFiles)
	Expect(cvImplicit).To(HaveLen(1))
	Expect(cvImplicit[0].Targets).To(HaveLen(1))
	Expect(cvImplicit[0].Targets[0].Availability).To(Equal("unavailable"))
	Expect(cvImplicit[0].Targets[0].Reason).To(Equal("source_capture_required"))
	Expect(cvImplicit[0].Targets[0].BytesOrigin).To(BeEmpty())
	Expect(cvImplicit[0].Targets[0].SnapshotSHA256).To(BeEmpty())
	jImplicitDone := runScanTurns(s, f.bytes, jobImplicitFiles, 32)
	Expect(jImplicitDone.State).To(Equal("completed"))

	// 3b. Incompatible explicit SnapshotID in current mode (or Revision on files) is rejected with ErrInvalidTarget.
	_, err = scanSvc.StartScan(ctx, owner.Token, f.id(), nil, domain.ScanRequest{
		VersionID:  vFiles,
		SourceMode: "current",
		Sources: []domain.ScanSourceSelector{
			{ProjectID: projectID, SourceID: "src-files", Mode: "current", SnapshotID: strings.Repeat("9", 64)},
		},
	})
	Expect(err).To(MatchError(domain.ErrInvalidTarget))
	_, err = scanSvc.StartScan(ctx, owner.Token, f.id(), nil, domain.ScanRequest{
		VersionID:  vFiles,
		SourceMode: "current",
		Sources: []domain.ScanSourceSelector{
			{ProjectID: projectID, SourceID: "src-files", Mode: "current", Revision: revA1},
		},
	})
	Expect(err).To(MatchError(domain.ErrInvalidTarget))

	// 3c. Managed capture A over both a.txt and b.txt -> explicit scan A -> mutate b.txt -> managed capture B -> distinct scan B (no false coalescing).
	srcSvc := appsource.New(s, auth, testCaptureBytes{s: s, bytes: f.bytes}, nil)
	srcSvc.Files = localTestFileReader{}
	capRespA, err := srcSvc.Capture(ctx, owner.Token, f.id(), projectID, appsource.CaptureRequest{
		VersionID:  vFiles,
		SourceMode: "current",
		Selections: []appsource.CaptureSelection{
			{
				SourceID: "src-files",
				Paths: []appsource.CapturePath{
					{Path: "a.txt"},
					{Path: "b.txt"},
				},
			},
		},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(capRespA.Status).To(Equal(201))
	var capBodyA struct {
		TargetSetID string `json:"target_set_id"`
	}
	Expect(json.Unmarshal(capRespA.Body, &capBodyA)).To(Succeed())
	Expect(capBodyA.TargetSetID).NotTo(BeEmpty())

	jobFilesA := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID:   vFiles,
		TargetSetID: capBodyA.TargetSetID,
	})
	cvFilesA := allCapturedVersions(s, jobFilesA)
	Expect(cvFilesA).To(HaveLen(1))
	Expect(cvFilesA[0].Targets).To(HaveLen(1))
	Expect(cvFilesA[0].Targets[0].Availability).To(Equal("available"))
	Expect(cvFilesA[0].Targets[0].BytesOrigin).To(Equal("retained_snapshot"))
	snapA := cvFilesA[0].Targets[0].SnapshotSHA256
	Expect(snapA).To(HaveLen(64))

	// Mutate the SECOND file b.txt on disk while a.txt stays identical.
	Expect(os.WriteFile(filepath.Join(filesDir, "b.txt"), []byte("file-b-v2-mutated\n"), 0o600)).To(Succeed())
	capRespB, err := srcSvc.Capture(ctx, owner.Token, f.id(), projectID, appsource.CaptureRequest{
		VersionID:  vFiles,
		SourceMode: "current",
		Selections: []appsource.CaptureSelection{
			{
				SourceID: "src-files",
				Paths: []appsource.CapturePath{
					{Path: "a.txt"},
					{Path: "b.txt"},
				},
			},
		},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(capRespB.Status).To(Equal(201))
	var capBodyB struct {
		TargetSetID string `json:"target_set_id"`
	}
	Expect(json.Unmarshal(capRespB.Body, &capBodyB)).To(Succeed())
	Expect(capBodyB.TargetSetID).NotTo(Equal(capBodyA.TargetSetID))

	// Start Scan B while Scan A is still queued -> must NOT coalesce with Scan A because b.txt changed the composite snapshot identity!
	rawReqB, _ := json.Marshal(domain.ScanRequest{
		VersionID:   vFiles,
		TargetSetID: capBodyB.TargetSetID,
	})
	resB, err := scanSvc.StartScan(ctx, owner.Token, f.id(), rawReqB, domain.ScanRequest{
		VersionID:   vFiles,
		TargetSetID: capBodyB.TargetSetID,
	})
	Expect(err).NotTo(HaveOccurred())
	var outB struct {
		JobID     string `json:"job_id"`
		Coalesced bool   `json:"coalesced"`
	}
	Expect(json.Unmarshal(resB.Body, &outB)).To(Succeed())
	Expect(outB.Coalesced).To(BeFalse())
	Expect(outB.JobID).NotTo(Equal(jobFilesA))
	cvFilesB := allCapturedVersions(s, outB.JobID)
	Expect(cvFilesB).To(HaveLen(1))
	Expect(cvFilesB[0].Targets).To(HaveLen(1))
	Expect(cvFilesB[0].Targets[0].SnapshotSHA256).NotTo(Equal(snapA))

	// 4. Multi-batch (>256) pagination proof for source_capture_reads and job_items / CancelJob / refreshScanReportCounts.
	var paths260 []appsource.CapturePath
	for idx := 1; idx <= 260; idx++ {
		relName := fmt.Sprintf("extra-%04d.txt", idx)
		Expect(os.WriteFile(filepath.Join(filesDir, relName), []byte(fmt.Sprintf("extra-%04d\n", idx)), 0o600)).To(Succeed())
		paths260 = append(paths260, appsource.CapturePath{Path: relName})
	}
	capResp260, err := srcSvc.Capture(ctx, owner.Token, f.id(), projectID, appsource.CaptureRequest{
		VersionID:  vFiles,
		SourceMode: "current",
		Selections: []appsource.CaptureSelection{
			{
				SourceID: "src-files",
				Paths:    paths260,
			},
		},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(capResp260.Status).To(Equal(201))
	var capBody260 struct {
		TargetSetID string `json:"target_set_id"`
	}
	Expect(json.Unmarshal(capResp260.Body, &capBody260)).To(Succeed())
	Expect(capBody260.TargetSetID).NotTo(BeEmpty())

	// Bind capBody260.TargetSetID and verify all >256 source_capture_reads are paginated without truncation.
	job260Reads := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID:   vFiles,
		TargetSetID: capBody260.TargetSetID,
	})
	cv260 := allCapturedVersions(s, job260Reads)
	Expect(cv260).To(HaveLen(1))
	Expect(cv260[0].Targets[0].Availability).To(Equal("available"))

	// Seed >256 job_items on job260Reads (including an item at ordinal 258) and verify refreshScanReportCounts and CancelJob paginate all >256 items.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		run, e := tx.ScanRun(job260Reads)
		if e != nil {
			return e
		}
		for ord := 1; ord <= 260; ord++ {
			st := "completed"
			if ord == 258 {
				st = "pending"
			}
			if e := tx.PutJobItem(domain.JobItem{
				JobID:      job260Reads,
				ItemID:     fmt.Sprintf("item-%06d", ord),
				Ordinal:    ord,
				UnitKind:   "entry",
				ProjectID:  projectID,
				DocumentID: cv260[0].DocumentID,
				VersionID:  vFiles,
				Stage:      "inspect",
				State:      st,
				Reason:     "ok",
				Detail:     []byte("{}"),
				UpdatedAt:  "2026-11-08T00:00:00Z",
			}); e != nil {
				return e
			}
		}
		run.EnumerationComplete = true
		run.NextItemOrdinal = 261
		if e := tx.UpdateScanRun(run); e != nil {
			return e
		}
		updatedRun, e := tx.ScanRun(job260Reads)
		if e != nil {
			return e
		}
		Expect(updatedRun.Report.Structural.Checked).To(Equal(259))
		_, e = tx.CancelJob(job260Reads)
		return e
	})).To(Succeed())
	var item258 domain.JobItem
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		item258, e = tx.JobItem(job260Reads, "item-000258")
		return e
	})).To(Succeed())
	Expect(item258.State).To(Equal("cancelled"))

	// 4b. Real >batch (>64 units) incremental version inventory enumeration across multiple stepInventory turns and mid-version store reopen without duplicate units.
	var htmlBuilder strings.Builder
	var regions70 []map[string]any
	for rIdx := 1; rIdx <= 70; rIdx++ {
		regID := fmt.Sprintf("reg-%03d", rIdx)
		htmlBuilder.WriteString(fmt.Sprintf("<p id='%s'>region %03d</p>", regID, rIdx))
		regions70 = append(regions70, map[string]any{
			"page_path": "page.html",
			"id":        regID,
			"label":     fmt.Sprintf("Region %03d", rIdx),
		})
	}
	vManyUnits := publishInProjectWithSources(f, projectID, nil, htmlBuilder.String(), nil, regions70...)
	f.run(vManyUnits, "search")
	f.run(vManyUnits, "references")
	// 1 version + 1 page + 70 regions + 1 entry + 1 source + 1 index = 75 units (> 64 per-turn stepInventory budget).
	jobManyUnits := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID: vManyUnits,
		Depth:     "full_scope",
	})
	wkrMany := scan.NewWorker(s, f.bytes)
	Expect(wkrMany.Process(ctx, jobManyUnits)).To(Succeed())
	var runManyMid domain.ScanRun
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		runManyMid, e = tx.ScanRun(jobManyUnits)
		return e
	})).To(Succeed())
	Expect(runManyMid.EnumerationComplete).To(BeFalse())
	cvManyMid := allCapturedVersions(s, jobManyUnits)
	Expect(cvManyMid).To(HaveLen(1))
	Expect(cvManyMid[0].UnitCount).To(Equal(75))
	Expect(cvManyMid[0].MaterializedUnits).To(Equal(64))
	Expect(cvManyMid[0].EnumState).To(Equal("pending"))
	Expect(cvManyMid[0].EnumAfterPath).To(Equal("page.html"))
	Expect(cvManyMid[0].EnumAfterRegion).To(Equal("reg-062"))
	Expect(allJobItems(s, jobManyUnits)).To(HaveLen(64))
	// Reopen store mid-version enumeration -> jobManyUnits becomes 'interrupted' and resumes from persisted cursor without duplicating units 1..64.
	Expect(s.Close()).To(Succeed())
	s = New(nil)
	_, closeReopenMany, err := s.Prepare(ctx, w.Profile)
	Expect(err).NotTo(HaveOccurred())
	defer closeReopenMany()
	f.s = s
	auth = appauth.New(s)
	scanSvc = scan.New(s, auth, f.bytes, nil)
	srcSvc = appsource.New(s, auth, testCaptureBytes{s: s, bytes: f.bytes}, nil)
	srcSvc.Files = localTestFileReader{}
	jManyInterrupted, err := s.Job(ctx, jobManyUnits)
	Expect(err).NotTo(HaveOccurred())
	Expect(jManyInterrupted.State).To(Equal("interrupted"))
	jManyDone := runScanTurns(s, f.bytes, jobManyUnits, 128)
	Expect(jManyDone.State).To(Equal("completed"))
	itemsManyDone := allJobItems(s, jobManyUnits)
	Expect(itemsManyDone).To(HaveLen(75))
	for i, it := range itemsManyDone {
		Expect(it.Ordinal).To(Equal(i + 1))
	}

	// 4b. R1 exact page/region batch-boundary regression: when a page unit lands on the exact last slot of batch 1 (slot 64,
	// after 1 version + 1 page a.html + 61 regions on a.html = 63 units), its own regions on b.html are preserved via
	// SourceAfterIdx == 1 across store reopen and enumerated in batch 2 without skipping b.html's regions or padding empty rows.
	var boundaryRegions []map[string]any
	var bodyABuilder strings.Builder
	bodyABuilder.WriteString("<html><body>")
	for idx := 1; idx <= 61; idx++ {
		regID := fmt.Sprintf("a-reg-%03d", idx)
		bodyABuilder.WriteString(fmt.Sprintf("<section id='%s'>A %d</section>", regID, idx))
		boundaryRegions = append(boundaryRegions, map[string]any{
			"page_path": "a.html",
			"id":        regID,
			"label":     fmt.Sprintf("A %d", idx),
		})
	}
	bodyABuilder.WriteString("</body></html>")
	var bodyBBuilder strings.Builder
	bodyBBuilder.WriteString("<html><body>")
	for idx := 1; idx <= 3; idx++ {
		regID := fmt.Sprintf("b-reg-%03d", idx)
		bodyBBuilder.WriteString(fmt.Sprintf("<section id='%s'>B %d</section>", regID, idx))
		boundaryRegions = append(boundaryRegions, map[string]any{
			"page_path": "b.html",
			"id":        regID,
			"label":     fmt.Sprintf("B %d", idx),
		})
	}
	bodyBBuilder.WriteString("</body></html>")
	vBoundary := publishMultiPageInProjectWithSources(f, projectID, map[string]string{
		"a.html": bodyABuilder.String(),
		"b.html": bodyBBuilder.String(),
	}, nil, boundaryRegions...)
	f.run(vBoundary, "search")
	f.run(vBoundary, "references")

	jobBoundary := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		VersionID: vBoundary,
		Depth:     "full_scope",
	})
	wkrBoundary := scan.NewWorker(s, f.bytes)
	Expect(wkrBoundary.Process(ctx, jobBoundary)).To(Succeed())
	cvBoundaryMid := allCapturedVersions(s, jobBoundary)
	Expect(cvBoundaryMid).To(HaveLen(1))
	// 1 version + 2 pages + 64 regions + 2 entries + 1 source placeholder + 1 index = 71 units total.
	Expect(cvBoundaryMid[0].UnitCount).To(Equal(71))
	Expect(cvBoundaryMid[0].MaterializedUnits).To(Equal(64))
	Expect(cvBoundaryMid[0].EnumAfterPath).To(Equal("b.html"))
	Expect(cvBoundaryMid[0].EnumAfterRegion).To(BeEmpty())
	Expect(cvBoundaryMid[0].SourceAfterIdx).To(Equal(1))

	jBoundaryDone := runScanTurns(s, f.bytes, jobBoundary, 128)
	Expect(jBoundaryDone.State).To(Equal("completed"))
	itemsBoundary := allJobItems(s, jobBoundary)
	Expect(itemsBoundary).To(HaveLen(71))
	var bRegionIDs []string
	for _, it := range itemsBoundary {
		if it.UnitKind == "page" {
			Expect(it.PagePath).NotTo(BeEmpty())
		}
		if it.UnitKind == "region" && it.PagePath == "b.html" {
			bRegionIDs = append(bRegionIDs, it.RegionID)
		}
	}
	Expect(bRegionIDs).To(Equal([]string{"b-reg-001", "b-reg-002", "b-reg-003"}))

	// 5. Default/explicit selected_projects with empty project_ids rejects rather than widening to all projects, configured selected subset is honored, and explicit all_allowed_projects paginates >256 projects.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		for idx := 1; idx <= 260; idx++ {
			pid := fmt.Sprintf("00000000-0000-4000-8000-000000009%03d", idx)
			if e := tx.RegisterProject(domain.Project{ID: pid, Name: fmt.Sprintf("Extra Project %03d", idx)}); e != nil {
				return e
			}
		}
		return nil
	})).To(Succeed())
	lateProjectID := "00000000-0000-4000-8000-000000009260"
	vLateProj := publishInProjectWithSources(f, lateProjectID, nil, "<p id='lp'>late proj</p>", nil, map[string]any{"page_path": "page.html", "id": "lp", "label": "LP"})
	f.run(vLateProj, "search")
	f.run(vLateProj, "references")

	// 5a. Default selected_projects with empty maintenance.project_ids=[] and no request project_ids fails ErrInvalidTarget (never widens to all projects).
	rawEmptyDefault, _ := json.Marshal(domain.ScanRequest{Depth: "full_scope"})
	_, err = scanSvc.StartScan(ctx, owner.Token, f.id(), rawEmptyDefault, domain.ScanRequest{Depth: "full_scope"})
	Expect(err).To(MatchError(domain.ErrInvalidTarget))
	rawEmptyExplicitSelected, _ := json.Marshal(domain.ScanRequest{Scope: "selected_projects", Depth: "full_scope"})
	_, err = scanSvc.StartScan(ctx, owner.Token, f.id(), rawEmptyExplicitSelected, domain.ScanRequest{Scope: "selected_projects", Depth: "full_scope"})
	Expect(err).To(MatchError(domain.ErrInvalidTarget))

	// 5b. Configured maintenance.project_ids subset selects only that configured project subset when request project_ids is omitted.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var body map[string]any
		Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
		maint, _ := body["maintenance"].(map[string]any)
		if maint == nil {
			maint = map[string]any{}
			body["maintenance"] = maint
		}
		maint["scope"] = "selected_projects"
		maint["project_ids"] = []string{lateProjectID}
		raw, _ := json.Marshal(body)
		cfg.Value = raw
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
	jobConfiguredSelected := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		Depth: "full_scope",
	})
	var runConfigured domain.ScanRun
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		runConfigured, e = tx.ScanRun(jobConfiguredSelected)
		return e
	})).To(Succeed())
	Expect(runConfigured.RequestedScope.Scope).To(Equal("selected_projects"))
	Expect(runConfigured.RequestedScope.ProjectIDs).To(Equal([]string{lateProjectID}))
	Expect(runConfigured.Denominator.Projects).To(Equal(1))

	// 5c. Explicit all_allowed_projects paginates past 256 projects and includes project #260.
	jobAllProjects := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		Scope: "all_allowed_projects",
		Depth: "full_scope",
	})
	cvAll := allCapturedVersions(s, jobAllProjects)
	var sawLateProj bool
	for _, cv := range cvAll {
		if cv.ProjectID == lateProjectID && cv.VersionID == vLateProj {
			sawLateProj = true
		}
	}
	Expect(sawLateProj).To(BeTrue(), "all_allowed_projects admission must paginate past 256 projects and include project #260")

	// 6. Populated Revision 19 -> 20 SQLite migration rollback, premigration snapshot, checksum verification and schema declaration validation.
	_, migWriter := profile(GinkgoT())
	migManifest, migHash := referenceSchema8(migWriter)
	migDB := readDB(GinkgoT(), migWriter.Profile)
	for i := 8; i < 19; i++ {
		_, err = migDB.Exec(migrations[i])
		Expect(err).NotTo(HaveOccurred())
		_, err = migDB.Exec(`INSERT INTO schema_migrations VALUES(?,?,?)`, i+1, digest([]byte(migrations[i])), nowStamp())
		Expect(err).NotTo(HaveOccurred())
	}
	_, err = migDB.Exec(`UPDATE libraries SET schema_revision=19;PRAGMA user_version=19`)
	Expect(err).NotTo(HaveOccurred())
	Expect(migDB.Close()).To(Succeed())

	migFault := errors.New("controlled scan migration interruption")
	brokenMig := New(func(boundary Boundary) error {
		if boundary == BeforeMigrationCommit {
			return migFault
		}
		return nil
	})
	_, _, err = brokenMig.Prepare(ctx, migWriter.Profile)
	Expect(err).To(MatchError(migFault))
	Expect(brokenMig.Close()).To(Succeed())

	migDB = readDB(GinkgoT(), migWriter.Profile)
	var migRev, migCount int
	Expect(migDB.QueryRow(`PRAGMA user_version`).Scan(&migRev)).To(Succeed())
	Expect(migRev).To(Equal(19))
	Expect(migDB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN('scan_runs','scan_captured_versions','job_items','findings')`).Scan(&migCount)).To(Succeed())
	Expect(migCount).To(BeZero())
	for n := 1; n <= 19; n++ {
		var cs string
		Expect(migDB.QueryRow(`SELECT checksum FROM schema_migrations WHERE revision=?`, n).Scan(&cs)).To(Succeed())
		Expect(cs).To(Equal(digest([]byte(migrations[n-1]))))
	}
	Expect(migDB.Close()).To(Succeed())

	upgradedMig := New(nil)
	migState, closeUpgraded, err := upgradedMig.Prepare(ctx, migWriter.Profile)
	Expect(err).NotTo(HaveOccurred())
	defer closeUpgraded()
	Expect(migState.Ready).To(BeTrue())
	migHealthAfter, err := upgradedMig.Health(ctx)
	Expect(err).NotTo(HaveOccurred())
	Expect(migHealthAfter.Library.ID).To(Equal(migWriter.Profile.LibraryID))
	Expect(migHealthAfter.Library.SchemaRevision).To(Equal(SchemaRevision))
	Expect(upgradedMig.validateTables(ctx, SchemaRevision)).To(Succeed())
	var storedManifestHash string
	Expect(upgradedMig.db.QueryRow(`SELECT manifest_sha256 FROM document_versions WHERE version_id=?`, refSource).Scan(&storedManifestHash)).To(Succeed())
	Expect(storedManifestHash).To(Equal(migHash))
	migSnapshots, err := filepath.Glob(filepath.Join(migWriter.Profile.DataDir, "storage", "pre-migration-*.sqlite"))
	Expect(err).NotTo(HaveOccurred())
	Expect(migSnapshots).NotTo(BeEmpty())
	for _, snap := range migSnapshots {
		if _, err := os.Stat(snap + ".json"); err == nil {
			copiedObj, err := os.ReadFile(filepath.Join(snap+".objects", migHash))
			Expect(err).NotTo(HaveOccurred())
			Expect(copiedObj).To(Equal(migManifest))
		}
	}
	_, err = upgradedMig.db.Exec(`DROP INDEX findings_by_job_order;CREATE INDEX findings_by_job_order ON findings(job_id,finding_id,created_at)`)
	Expect(err).NotTo(HaveOccurred())
	Expect(upgradedMig.validateTables(ctx, SchemaRevision)).To(MatchError("changed scan schema declaration"))
	_, err = upgradedMig.db.Exec(`DROP INDEX findings_by_job_order;CREATE INDEX findings_by_job_order ON findings(job_id,created_at,finding_id)`)
	Expect(err).NotTo(HaveOccurred())
	Expect(upgradedMig.validateTables(ctx, SchemaRevision)).To(Succeed())
	_, err = upgradedMig.db.Exec(`DROP INDEX maintenance_intents_by_scan_item;CREATE INDEX maintenance_intents_by_scan_item ON maintenance_intents(scan_job_id,job_id,item_id)`)
	Expect(err).NotTo(HaveOccurred())
	Expect(upgradedMig.validateTables(ctx, SchemaRevision)).To(MatchError("changed maintenance schema declaration"))
})

type scriptedMaintenanceDriver struct {
	store        *Store
	responder    func(a domain.ProviderAttempt, inv domain.ProviderInvocation, inputs []domain.ProviderInput) (string, error)
	unknownOnJob map[string]bool
	cancelled    []string
	executeCalls int
}

func defaultNominationEnvelopeJSON(inv domain.ProviderInvocation) (string, bool) {
	if inv.Maintenance == nil || inv.Maintenance.Mode != domain.MaintenanceModeSemanticNomination {
		return "", false
	}
	var promptEnv struct {
		Consumer string                            `json:"consumer"`
		Cards    []domain.MaintenanceProjectedCard `json:"cards"`
	}
	if json.Unmarshal([]byte(inv.Prompt), &promptEnv) != nil || len(promptEnv.Cards) == 0 {
		return "", false
	}
	var noms []map[string]any
	for i, focal := range promptEnv.Cards {
		var cands []map[string]any
		// Prefer cross-version cards first, then same-version cards, up to 6.
		for pass := 0; pass < 2; pass++ {
			for j, other := range promptEnv.Cards {
				if i == j || len(cands) >= 6 {
					continue
				}
				crossVer := other.VersionID != focal.VersionID
				if (pass == 0 && !crossVer) || (pass == 1 && crossVer) {
					continue
				}
				cands = append(cands, map[string]any{
					"candidate_unit_id": other.UnitID,
					"rationale":         fmt.Sprintf("Nominated counterpart %s for focal %s across batch cards.", other.UnitID, focal.UnitID),
				})
			}
		}
		if len(cands) == 0 {
			noms = append(noms, map[string]any{
				"focal_unit_id":  focal.UnitID,
				"abstained":      true,
				"abstain_reason": "No distinct counterpart in batch.",
				"candidates":     []any{},
			})
		} else {
			noms = append(noms, map[string]any{
				"focal_unit_id": focal.UnitID,
				"abstained":     false,
				"candidates":    cands,
			})
		}
	}
	raw, _ := json.Marshal(map[string]any{"nominations": noms})
	return string(raw), true
}

func (d *scriptedMaintenanceDriver) Execute(ctx context.Context, a domain.ProviderAttempt, inputs []domain.ProviderInput, send func() error, progress func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	d.executeCalls++
	if err := send(); err != nil {
		return work.ProviderOutcome{LocalClosed: true}, err
	}
	observed := make([]domain.ProviderInput, len(inputs))
	for i, in := range inputs {
		in.ObservedOrigin = "daemon_supplied"
		observed[i] = in
	}
	reqID := "req-" + a.ID
	sessID := "sess-" + a.ID
	if progress != nil {
		if err := progress(work.ProviderProgress{RequestID: reqID, SessionID: sessID, Observed: observed}); err != nil {
			return work.ProviderOutcome{LocalClosed: true}, err
		}
	}
	if d.unknownOnJob != nil && d.unknownOnJob[a.JobID] {
		return work.ProviderOutcome{
			State:       "outcome_unknown",
			Reason:      "transport_unknown",
			LocalClosed: true,
			RequestID:   reqID,
			SessionID:   sessID,
			Observed:    observed,
		}, nil
	}
	var inv domain.ProviderInvocation
	if d.store != nil {
		if cj, err := d.store.Job(ctx, a.JobID); err == nil {
			var scope domain.ProviderScope
			if json.Unmarshal(cj.Scope, &scope) == nil && scope.Invocation != nil {
				inv = *scope.Invocation
			}
		}
	}
	bodyStr := `{"relations":[],"topics":[],"content_gaps":[],"limitations":[]}`
	if nomStr, isNom := defaultNominationEnvelopeJSON(inv); isNom {
		bodyStr = nomStr
	}
	if d.responder != nil {
		var err error
		bodyStr, err = d.responder(a, inv, observed)
		if err != nil {
			return work.ProviderOutcome{LocalClosed: true, State: "failed", Reason: "transport_error"}, err
		}
	}
	return work.ProviderOutcome{
		State:              "result_saved",
		LocalClosed:        true,
		CompletionObserved: true,
		RequestID:          reqID,
		SessionID:          sessID,
		Observed:           observed,
		Body:               io.NopCloser(strings.NewReader(bodyStr)),
	}, nil
}

func (d *scriptedMaintenanceDriver) Cancel(_ context.Context, a domain.ProviderAttempt) error {
	d.cancelled = append(d.cancelled, a.ID)
	return nil
}

func (d *scriptedMaintenanceDriver) Reconcile(_ context.Context, a domain.ProviderAttempt, _ bool) (work.ProviderOutcome, error) {
	return work.ProviderOutcome{
		LocalClosed: true,
		State:       "failed",
		Reason:      "reconciled_stopped",
	}, nil
}

func publishInProjectWithSourcesAndEvidence(f *referenceWorld, projectID string, evidence map[string]map[string]any, body string, sources map[string]map[string]any, regions ...map[string]any) string {
	GinkgoHelper()
	version, document, set := f.id(), f.id(), f.id()
	raw, _ := json.Marshal(map[string]any{"schema_version": "1.0", "viewer_profile": "html_js_v1", "references": []any{}})
	manifest, page := digest(raw), digest([]byte(body))
	f.bytes.files[manifest] = string(raw)
	f.bytes.files[page] = body
	if projectID == "" {
		h, err := f.s.Health(ctx)
		Expect(err).NotTo(HaveOccurred())
		projectID = h.Library.UnfiledProjectID
	}
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.CreateContentSet(set, digest(nil)); err != nil {
			return err
		}
		for _, o := range []domain.Object{{Hash: manifest, Size: int64(len(raw)), State: "ready"}, {Hash: page, Size: int64(len(body)), State: "ready"}} {
			if err := tx.PutDurableObject(o); err != nil {
				return err
			}
		}
		if err := tx.PutEntry(domain.Entry{ContentSetID: set, Path: "page.html", Kind: "file", Hash: page, Size: int64(len(body))}); err != nil {
			return err
		}
		if err := tx.SealContentSet(set); err != nil {
			return err
		}
		metadataID := ""
		if len(sources) > 0 || len(evidence) > 0 || len(regions) > 0 {
			metadataID = f.id()
			if err := tx.CreateMetadataIndex(domain.MetadataIndex{ID: metadataID, PrincipalID: "system", Hash: manifest, ContentSetID: set}); err != nil {
				return err
			}
			for srcID, srcMeta := range sources {
				if err := tx.PutImportMetadata(metadataID, "sources", srcID, jsonBytes(srcMeta)); err != nil {
					return err
				}
			}
			for evID, evMeta := range evidence {
				if err := tx.PutImportMetadata(metadataID, "evidence", evID, jsonBytes(evMeta)); err != nil {
					return err
				}
			}
			for _, regMeta := range regions {
				regKey := fmt.Sprintf("%s#%s", regMeta["page_path"], regMeta["id"])
				if err := tx.PutImportMetadata(metadataID, "regions", regKey, jsonBytes(regMeta)); err != nil {
					return err
				}
			}
		}
		if err := tx.FinalizeImport(
			domain.ImportIntent{JobID: metadataID, NewDocument: true, DocumentID: document, ProjectID: projectID, DocumentRevision: 1, Title: "References"},
			domain.Version{ID: version, DocumentID: document, ContentSetID: set, ManifestSHA: manifest, PayloadSHA: digest(nil), Title: "References", ViewerProfile: "html_js_v1", SourceBasis: "unverified"},
		); err != nil {
			return err
		}
		if metadataID != "" {
			if err := tx.ReleaseMetadataPreparation(metadataID); err != nil {
				return err
			}
		}
		return nil
	})).To(Succeed())
	return version
}

var _ = It("evaluates bounded relationship, topic, and content-gap candidates with four-boundary target fences, >256KiB source dependency verification, and parent/child settlement (TASK-008-02)", Label("persistence"), func() {
	s, auth, w, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "scan-task02-owner")
	f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
	projectID := mustUnfiledProject(s)
	capBytes := testCaptureBytes{s: s, bytes: f.bytes}
	srcSvc := appsource.New(s, auth, capBytes, nil)
	srcSvc.Files = localTestFileReader{}
	scanSvc := scan.New(s, auth, f.bytes, nil)

	// Create a local source directory with go.mod, internal/worker/run.go, and a >256KiB pkg/resilience/policy.go.
	srcRoot := filepath.Join(GinkgoT().TempDir(), "repo-main")
	Expect(os.MkdirAll(filepath.Join(srcRoot, "internal", "worker"), 0o755)).To(Succeed())
	Expect(os.MkdirAll(filepath.Join(srcRoot, "pkg", "resilience"), 0o755)).To(Succeed())
	goModContent := "module example.local/project\n\ngo 1.22\n"
	Expect(os.WriteFile(filepath.Join(srcRoot, "go.mod"), []byte(goModContent), 0o644)).To(Succeed())
	workerGoContent := `package worker
import (
	"example.local/project/pkg/resilience"
)
func RunBackoffCoordinator() error {
	return resilience.TailExportedBackoff()
}
`
	Expect(os.WriteFile(filepath.Join(srcRoot, "internal", "worker", "run.go"), []byte(workerGoContent), 0o644)).To(Succeed())

	// Build a >256KiB source file where the exported function appears AFTER 270KiB of comments.
	var bigPolicy strings.Builder
	bigPolicy.WriteString("package resilience\n")
	commentLine := "// padding comment line for >256KiB non-truncated source dependency verification check.\n"
	for bigPolicy.Len() < 270*1024 {
		bigPolicy.WriteString(commentLine)
	}
	bigPolicy.WriteString("\nfunc TailExportedBackoff() error {\n\treturn nil\n}\n")
	Expect(os.WriteFile(filepath.Join(srcRoot, "pkg", "resilience", "policy.go"), []byte(bigPolicy.String()), 0o644)).To(Succeed())

	srcMainID := "00000000-0000-4000-8000-000000000901"

	// Configure approved files source srcMainID and codex provider for projectID, preserving default maintenance schedule and validating against configuration.Validate.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var body map[string]any
		Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
		maint, _ := body["maintenance"].(map[string]any)
		Expect(maint).NotTo(BeNil())
		maint["scope"] = "selected_projects"
		maint["project_ids"] = []string{projectID}
		maint["depth"] = "deep_semantic"
		maint["max_concurrent_ai_jobs"] = 2
		maint["max_provider_attempts_per_run"] = 16
		maint["max_run_seconds"] = 1800
		maint["content_generation"] = "propose"
		autoApply, _ := maint["auto_apply"].(map[string]any)
		Expect(autoApply).NotTo(BeNil())
		autoApply["related_explanation"] = "propose"
		autoApply["topic_assignment"] = "propose"

		projects, _ := body["projects"].(map[string]any)
		if projects == nil {
			projects = map[string]any{}
			body["projects"] = projects
		}
		proj, _ := projects[projectID].(map[string]any)
		if proj == nil {
			proj = map[string]any{}
			projects[projectID] = proj
		}
		proj["source_mode_default"] = "current"
		proj["provider_id"] = "codex"
		proj["sources"] = []map[string]any{
			{
				"source_id":      srcMainID,
				"kind":           "files",
				"root_path":      srcRoot,
				"repo_id":        nil,
				"retention_mode": "selected_snapshot",
				"provider_ids":   []string{"codex"},
			},
		}
		raw, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		normalized, err := configuration.Decode(strings.NewReader(string(raw)), "json")
		Expect(err).NotTo(HaveOccurred())
		Expect(configuration.Validate(normalized)).To(Succeed())
		cfg.Value = raw
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())

	// Publish candidate version vCand with TWO regions on page.html ('reg-alpha' and 'reg-beta') and evidence pointing to go.mod + pkg/resilience/policy.go.
	vCand := publishInProjectWithSourcesAndEvidence(
		f,
		projectID,
		map[string]map[string]any{
			"ev-cand-mod": {
				"id":        "ev-cand-mod",
				"source_id": srcMainID,
				"kind":      "source_span",
				"locator":   map[string]any{"path": "go.mod"},
			},
			"ev-cand-policy": {
				"id":        "ev-cand-policy",
				"source_id": srcMainID,
				"kind":      "source_span",
				"locator":   map[string]any{"path": "pkg/resilience/policy.go"},
			},
		},
		"<section id='reg-alpha' data-haeryong-topic='retry-backoff'><h2>Retry Alpha Policy</h2><p>Exponential backoff resilience circuit policy for TailExportedBackoff.</p></section><section id='reg-beta' data-haeryong-topic='retry-backoff'><h2>Retry Beta Companion</h2><p>Secondary exponential backoff resilience notes for TailExportedBackoff.</p></section>",
		map[string]map[string]any{
			srcMainID: {"kind": "files"},
		},
		map[string]any{"page_path": "page.html", "id": "reg-alpha", "label": "Retry Alpha Policy", "evidence_ids": []string{"ev-cand-mod", "ev-cand-policy"}},
		map[string]any{"page_path": "page.html", "id": "reg-beta", "label": "Retry Beta Companion", "evidence_ids": []string{"ev-cand-mod", "ev-cand-policy"}},
	)
	f.run(vCand, "search")
	f.run(vCand, "references")

	// Publish trashed version vTrashed that shares the same keywords (must be excluded at shortlist and Apply boundaries: T-008-02-02).
	vTrashed := publishInProjectWithSources(
		f,
		projectID,
		nil,
		"<section id='reg-trash'><h2>Retry Backoff Trash</h2><p>Exponential backoff resilience circuit policy TailExportedBackoff in trashed doc.</p></section>",
		nil,
		map[string]any{"page_path": "page.html", "id": "reg-trash", "label": "Retry Backoff Trash"},
	)
	f.run(vTrashed, "search")
	f.run(vTrashed, "references")
	verTrashedRow, err := s.Version(ctx, vTrashed)
	Expect(err).NotTo(HaveOccurred())
	Expect(applyDocumentLifecycle(s, auth, f.bytes, owner.Token, verTrashedRow.DocumentID, "trash").Lifecycle).To(Equal("trash"))

	// Publish focal version vFocal with declared missing theme 'distributed-deadlock-detector' and evidence pointing to go.mod + internal/worker/run.go.
	vFocal := publishInProjectWithSourcesAndEvidence(
		f,
		projectID,
		map[string]map[string]any{
			"ev-focal-mod": {
				"id":        "ev-focal-mod",
				"source_id": srcMainID,
				"kind":      "source_span",
				"locator":   map[string]any{"path": "go.mod"},
			},
			"ev-focal-worker": {
				"id":        "ev-focal-worker",
				"source_id": srcMainID,
				"kind":      "source_span",
				"locator":   map[string]any{"path": "internal/worker/run.go"},
			},
		},
		"<section id='reg-focal' data-haeryong-topic='retry-backoff, distributed-deadlock-detector'><h2>Worker Backoff Coordinator</h2><p>Coordinates retry backoff execution using TailExportedBackoff resilience policy.</p></section>",
		map[string]map[string]any{
			srcMainID: {"kind": "files"},
		},
		map[string]any{"page_path": "page.html", "id": "reg-focal", "label": "Worker Backoff Coordinator", "topics": []string{"retry-backoff", "distributed-deadlock-detector"}, "evidence_ids": []string{"ev-focal-mod", "ev-focal-worker"}},
	)
	f.run(vFocal, "search")
	f.run(vFocal, "references")

	driver := &scriptedMaintenanceDriver{
		store: s,
		responder: func(a domain.ProviderAttempt, inv domain.ProviderInvocation, _ []domain.ProviderInput) (string, error) {
			if nomStr, isNom := defaultNominationEnvelopeJSON(inv); isNom {
				return nomStr, nil
			}
			out := map[string]any{
				"relations": []map[string]any{
					{
						"target_version_id": vCand,
						"target_page_path":  "page.html",
						"target_region_id":  "reg-alpha",
						"relation_type":     "source_dependency",
						"rationale":         "Worker imports pkg/resilience and calls TailExportedBackoff.",
						"uncertainty":       "none",
					},
					{
						"target_version_id": vCand,
						"target_page_path":  "page.html",
						"target_region_id":  "reg-beta",
						"relation_type":     "complementary_explanation",
						"rationale":         "Companion region explains secondary backoff details.",
						"uncertainty":       "none",
					},
					{
						"target_version_id": vFocal,
						"target_page_path":  "page.html",
						"target_region_id":  "reg-focal",
						"relation_type":     "source_dependency",
						"rationale":         "Reverse/keyword-only dependency claim from candidate to focal must downgrade to related.",
						"uncertainty":       "none",
					},
					{
						"target_version_id": vCand,
						"target_page_path":  "page.html",
						"target_region_id":  "reg-uninspected-invented",
						"relation_type":     "related",
						"rationale":         "Invented region on same version must be rejected.",
						"uncertainty":       "none",
					},
					{
						"target_version_id": vTrashed,
						"target_page_path":  "page.html",
						"target_region_id":  "reg-trash",
						"relation_type":     "related",
						"rationale":         "Trashed target must never leak via suggestions.",
						"uncertainty":       "none",
					},
				},
				"topics": []map[string]any{
					{
						"topic":       "retry-backoff",
						"rationale":   "Explicitly covered in focal heading and body.",
						"uncertainty": "none",
					},
					{
						"topic":       "retry-quantum-telepathy",
						"rationale":   "Partially overlapping multi-token hallucination must be rejected and not turned into content gap.",
						"uncertainty": "none",
					},
					{
						"topic":       "distributed-deadlock-detector",
						"rationale":   "Declared theme absent from explanation body must not become a false coverage tag.",
						"uncertainty": "none",
					},
				},
				"content_gaps": []map[string]any{
					{
						"kind":        "new_document_proposal",
						"theme":       "resilience-circuit-handbook",
						"rationale":   "Standalone handbook needed for circuit breaker operational playbooks.",
						"uncertainty": "bounded_shortlist_only",
					},
				},
				"limitations": []map[string]any{},
			}
			raw, _ := json.Marshal(out)
			return string(raw), nil
		},
	}

	var captureErrDiag []string
	wkr := scan.NewWorker(s, f.bytes)
	wkr.Sources = srcSvc
	wkr.Identity = codex.SemanticRequestSHA
	coordinator := work.ProviderCoordinator{
		Store:  s,
		Bytes:  capBytes,
		Driver: driver,
		MaintenanceSource: func(ctx context.Context, jobID string) error {
			cErr := srcSvc.CaptureMaintenanceAndAdmit(ctx, jobID, codex.SemanticRequestSHA)
			if cErr != nil {
				captureErrDiag = append(captureErrDiag, fmt.Sprintf("%s:%v", jobID, cErr))
			}
			return cErr
		},
		Prepare: wkr.PrepareMaintenance,
		Apply:   wkr.ApplyMaintenance,
	}

	// Register exact authorized current-mode captures for vCand and vFocal before scan admission (so current-mode files targets are available at admission without live fallback).
	capCandResp, err := srcSvc.Capture(ctx, owner.Token, f.id(), projectID, appsource.CaptureRequest{
		VersionID:  vCand,
		SourceMode: "current",
		Selections: []appsource.CaptureSelection{
			{
				SourceID: srcMainID,
				Paths: []appsource.CapturePath{
					{Path: "go.mod"},
					{Path: "pkg/resilience/policy.go"},
				},
			},
		},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(capCandResp.Status).To(Equal(201))

	capFocalResp, err := srcSvc.Capture(ctx, owner.Token, f.id(), projectID, appsource.CaptureRequest{
		VersionID:  vFocal,
		SourceMode: "current",
		Selections: []appsource.CaptureSelection{
			{
				SourceID: srcMainID,
				Paths: []appsource.CapturePath{
					{Path: "go.mod"},
					{Path: "internal/worker/run.go"},
				},
			},
		},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(capFocalResp.Status).To(Equal(201))

	goModSnapSHA := digest([]byte(goModContent))
	scanJobID := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		ProjectID:  projectID,
		SourceMode: "current",
		Depth:      "deep_semantic",
		Sources: []domain.ScanSourceSelector{
			{
				ProjectID:  projectID,
				SourceID:   srcMainID,
				Mode:       "current",
				SnapshotID: goModSnapSHA,
			},
		},
	})

	// Step parent scan worker and child maintenance jobs until scan completes.
	for turn := 0; turn < 96; turn++ {
		jCur, e := s.Job(ctx, scanJobID)
		Expect(e).NotTo(HaveOccurred())
		if jCur.State == "completed" || jCur.State == "failed" || jCur.State == "cancelled" || jCur.State == "paused" {
			break
		}
		Expect(wkr.Process(ctx, scanJobID)).To(Succeed())
		var childIDs []string
		rows, qErr := s.db.QueryContext(ctx, `SELECT job_id FROM maintenance_intents WHERE scan_job_id=? ORDER BY job_id`, scanJobID)
		Expect(qErr).NotTo(HaveOccurred())
		for rows.Next() {
			var cid string
			Expect(rows.Scan(&cid)).To(Succeed())
			childIDs = append(childIDs, cid)
		}
		Expect(rows.Err()).NotTo(HaveOccurred())
		Expect(rows.Close()).To(Succeed())
		for _, cid := range childIDs {
			cj, cErr := s.Job(ctx, cid)
			if cErr == nil && (cj.State == "queued" || cj.State == "running" || cj.State == "reconciling" || (cj.State == "paused" && cj.Error == "source_capture_pending")) {
				Expect(coordinator.Process(ctx, cid)).To(Succeed())
			}
		}
	}

	jFinal, err := s.Job(ctx, scanJobID)
	Expect(err).NotTo(HaveOccurred())
	var finalRun domain.ScanRun
	var itemStates []string
	var childDiag []string
	var sawMultiVersionExactBindings bool
	var sampleChildID, sampleReadID, sampleTargetSetID string
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		finalRun, e = tx.ScanRun(scanJobID)
		if e != nil {
			return e
		}
		items, e := tx.JobItems(scanJobID, 0, 100)
		if e != nil {
			return e
		}
		for _, it := range items {
			if it.State != "completed" {
				itemStates = append(itemStates, fmt.Sprintf("%s:%s:%s:%s:%s", it.ItemID, it.UnitKind, it.Stage, it.State, it.Reason))
			}
			if m, ok, mErr := tx.MaintenanceIntentByItem(scanJobID, it.ItemID); mErr == nil && ok {
				cj, _ := tx.Job(m.JobID)
				atts, _ := tx.ProviderAttempts(m.JobID)
				attState := "none"
				if len(atts) > 0 {
					attState = atts[len(atts)-1].State + ":" + atts[len(atts)-1].Reason
				}
				var capSteps []string
				for _, st := range m.Captures {
					Expect(st.ProjectID).To(Equal(projectID))
					Expect(st.VersionID).NotTo(BeEmpty())
					Expect(st.TargetSetID).NotTo(BeEmpty())
					capSteps = append(capSteps, fmt.Sprintf("%s:%s:%s:fin=%v", st.VersionID, st.State, st.Reason, st.Finished))
				}
				var bindSummary []string
				seenVerInChild := map[string]bool{}
				for _, b := range m.ReadBindings {
					Expect(b.ReadID).NotTo(BeEmpty())
					Expect(b.ProjectID).To(Equal(projectID))
					Expect(b.VersionID).To(BeElementOf(vCand, vFocal))
					Expect(b.TargetSetID).NotTo(BeEmpty())
					seenVerInChild[b.VersionID] = true
					bindSummary = append(bindSummary, fmt.Sprintf("%s->%s/%s", b.ReadID, b.VersionID, b.TargetSetID))
					if sampleReadID == "" {
						sampleChildID = m.JobID
						sampleReadID = b.ReadID
						sampleTargetSetID = b.TargetSetID
					}
				}
				if seenVerInChild[vCand] && seenVerInChild[vFocal] && len(m.Captures) >= 2 {
					sawMultiVersionExactBindings = true
				}
				childDiag = append(childDiag, fmt.Sprintf("item=%s child=%s state=%s err=%s adm=%v caps=%v binds=%v att=%s", it.ItemID, m.JobID, cj.State, cj.Error, m.AdmissionReady, capSteps, bindSummary, attState))
			}
		}
		return nil
	})).To(Succeed())
	Expect(captureErrDiag).To(BeEmpty(), "unexpected capture/admission errors: %v", captureErrDiag)
	Expect(jFinal.State).To(Equal("completed"), "parent state=%s err=%s stage=%s non-completed items=%v children=%v captureErrs=%v", jFinal.State, jFinal.Error, finalRun.Stage, itemStates, childDiag, captureErrDiag)
	Expect(sawMultiVersionExactBindings).To(BeTrue(), "expected multi-version same-project maintenance child to preserve distinct per-version capture steps and non-empty VersionID read bindings: %v", childDiag)
	Expect(sampleReadID).NotTo(BeEmpty())
	Expect(jFinal.ProviderAttempts).NotTo(BeNil())
	Expect(jFinal.ProviderAttempts.Count).To(BeNumerically(">=", 1))
	Expect(finalRun.Report.InspectedCandidateRelations).To(BeNumerically(">=", 2))

	fPage, err := scanSvc.ListJobFindings(ctx, owner.Token, domain.FindingFilter{JobID: scanJobID, Limit: 100})
	Expect(err).NotTo(HaveOccurred())

	var sawAlphaDep, sawBetaRel, sawReverseDowngrade, sawValidTopic, sawDeadlockGap, sawNewDocProposal bool
	for _, fd := range fPage.Items {
		// T-008-02-02: No finding may reference vTrashed or uninspected reg-uninspected-invented.
		Expect(string(fd.Proposal)).NotTo(ContainSubstring(vTrashed))
		Expect(string(fd.Proposal)).NotTo(ContainSubstring("reg-uninspected-invented"))
		// Property 5: Partially overlapping hallucinated topic 'retry-quantum-telepathy' is neither a topic_assignment nor a content_gap.
		Expect(string(fd.Proposal)).NotTo(ContainSubstring("retry-quantum-telepathy"))

		if fd.Kind == "new_document_proposal" {
			var prop map[string]any
			Expect(json.Unmarshal(fd.Proposal, &prop)).To(Succeed())
			originUnit, _ := prop["origin_unit"].(map[string]any)
			Expect(originUnit).NotTo(BeNil())
			Expect(originUnit["project_id"]).To(Equal(projectID))
			Expect(originUnit["document_id"]).NotTo(BeEmpty())
			Expect(originUnit["version_id"]).To(BeElementOf(vFocal, vCand))
			sawNewDocProposal = true
		}

		if fd.VersionID != nil && *fd.VersionID == vCand && fd.Kind == "related_explanation" {
			var prop map[string]any
			Expect(json.Unmarshal(fd.Proposal, &prop)).To(Succeed())
			if prop["target_version_id"] == vFocal && prop["target_region_id"] == "reg-focal" {
				sawReverseDowngrade = true
				Expect(prop["relation_type"]).To(Equal("related"))
				Expect(prop["evidence_basis"]).To(Equal("thematic_analogy"))
				Expect(prop["downgraded_from"]).To(Equal("source_dependency"))
				Expect(fd.Uncertainty).To(Equal("source_unverified"))
				Expect(fd.Rationale).To(ContainSubstring("Unverified code-dependency claim withdrawn"))
			}
		}

		if fd.VersionID != nil && *fd.VersionID == vFocal && fd.RegionID != nil && *fd.RegionID == "reg-focal" {
			switch fd.Kind {
			case "related_explanation":
				var prop map[string]any
				Expect(json.Unmarshal(fd.Proposal, &prop)).To(Succeed())
				if prop["target_version_id"] == vCand && prop["target_region_id"] == "reg-alpha" {
					sawAlphaDep = true
					Expect(prop["relation_type"]).To(Equal("depends_on"))
					Expect(prop["evidence_basis"]).To(Equal("source_fact"))
					Expect(prop["verified_dependency"]).To(Or(
						Equal("call:resilience.TailExportedBackoff"),
						Equal("import:example.local/project/pkg/resilience"),
					))
				}
				if prop["target_version_id"] == vCand && prop["target_region_id"] == "reg-beta" {
					sawBetaRel = true
					Expect(prop["relation_type"]).To(Equal("complementary_explanation"))
					Expect(prop["evidence_basis"]).To(Equal("thematic_analogy"))
				}
			case "topic_assignment":
				var prop map[string]any
				Expect(json.Unmarshal(fd.Proposal, &prop)).To(Succeed())
				Expect(prop["topic"]).NotTo(Equal("distributed-deadlock-detector"))
				if prop["topic"] == "retry-backoff" {
					sawValidTopic = true
				}
			case "new_version_proposal", "new_document_proposal":
				var prop map[string]any
				Expect(json.Unmarshal(fd.Proposal, &prop)).To(Succeed())
				if prop["theme"] == "distributed-deadlock-detector" {
					sawDeadlockGap = true
					Expect(fd.Uncertainty).To(Equal("bounded_shortlist_only"))
				}
			}
		}
	}
	Expect(sawAlphaDep).To(BeTrue(), "expected verified >256KiB source_fact dependency on (vCand, page.html, reg-alpha)")
	Expect(sawBetaRel).To(BeTrue(), "expected second candidate (vCand, page.html, reg-beta) on same version to be preserved without overwrite")
	Expect(sawReverseDowngrade).To(BeTrue(), "expected reverse/keyword-only dependency claim from vCand to vFocal to downgrade to related/thematic_analogy with source_unverified and withdrawn rationale (T-008-02-01, B12)")
	Expect(sawValidTopic).To(BeTrue(), "expected grounded topic 'retry-backoff' to be emitted")
	Expect(sawDeadlockGap).To(BeTrue(), "expected missing declared theme 'distributed-deadlock-detector' to produce content-gap proposal rather than false topic tag (T-008-02-03)")
	Expect(sawNewDocProposal).To(BeTrue(), "expected new_document_proposal with origin_unit project/document/version binding (B03)")

	// Regression: blank/wrong/duplicate read bindings are rejected by MaintenanceReadTarget and AttachSourceInputs,
	// and a failed maintenance capture/admission transitions durably out of source_capture_pending without spinning.
	_, _, _, okBlank := domain.MaintenanceReadTarget(domain.ProviderScope{
		Invocation: &domain.ProviderInvocation{
			Maintenance: &domain.MaintenanceInvocation{
				ReadBindings: []domain.MaintenanceReadBinding{
					{ReadID: sampleReadID, ProjectID: projectID, VersionID: "", TargetSetID: sampleTargetSetID},
				},
			},
		},
	}, "", projectID, sampleReadID)
	Expect(okBlank).To(BeFalse(), "blank VersionID in ReadBindings must be rejected")

	_, _, _, okDup := domain.MaintenanceReadTarget(domain.ProviderScope{
		Invocation: &domain.ProviderInvocation{
			Maintenance: &domain.MaintenanceInvocation{
				ReadBindings: []domain.MaintenanceReadBinding{
					{ReadID: sampleReadID, ProjectID: projectID, VersionID: vFocal, TargetSetID: sampleTargetSetID},
					{ReadID: sampleReadID, ProjectID: projectID, VersionID: vCand, TargetSetID: sampleTargetSetID},
				},
			},
		},
	}, "", projectID, sampleReadID)
	Expect(okDup).To(BeFalse(), "duplicate ReadID in ReadBindings must be rejected")

	badParentID := "00000000-0000-4000-8000-00000000badd"
	badItemID := "item-bad-capture"
	badChildID := "00000000-0000-4000-8000-00000000badc"
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		origChild, err := tx.Job(sampleChildID)
		if err != nil {
			return err
		}
		origIntent, err := tx.MaintenanceIntent(sampleChildID)
		if err != nil {
			return err
		}
		origRun, err := tx.ScanRun(scanJobID)
		if err != nil {
			return err
		}
		origCVs, err := tx.ScanCapturedVersions(scanJobID, 0, 256)
		if err != nil {
			return err
		}
		for i := range origCVs {
			origCVs[i].JobID = badParentID
			origCVs[i].EnumState = "complete"
		}
		if err := tx.CreateJob(domain.Job{
			ID:             badParentID,
			Kind:           "scan",
			State:          "queued",
			Scope:          []byte(`{"principal_id":"` + origRun.PrincipalID + `"}`),
			ConfigRevision: origChild.ConfigRevision,
			Permissions:    origChild.Permissions,
		}); err != nil {
			return err
		}
		badRun := origRun
		badRun.JobID = badParentID
		badRun.Stage = "inspect"
		if err := tx.CreateScanRun(badRun, origCVs); err != nil {
			return err
		}
		claimedBadParent, err := tx.ClaimJob(badParentID)
		if err != nil {
			return err
		}
		if err := tx.PutJobItem(domain.JobItem{
			JobID:             badParentID,
			ItemID:            badItemID,
			Ordinal:           1,
			UnitKind:          origIntent.UnitKind,
			ProjectID:         origIntent.ProjectID,
			DocumentID:        origIntent.DocumentID,
			VersionID:         origIntent.VersionID,
			PagePath:          origIntent.PagePath,
			RegionID:          origIntent.RegionID,
			Stage:             "inspect",
			State:             "pending",
			AttemptGeneration: claimedBadParent.AttemptGeneration,
			Detail:            []byte(`{}`),
			UpdatedAt:         nowStamp(),
		}); err != nil {
			return err
		}
		var badScope domain.ProviderScope
		if err := json.Unmarshal(origChild.Scope, &badScope); err != nil {
			return err
		}
		badScope.Invocation.Maintenance.ScanJobID = badParentID
		badScope.Invocation.Maintenance.ItemID = badItemID
		badScope.ReadIDs = []string{sampleReadID}
		// Blank VersionID in ReadBindings must be rejected by AttachSourceInputs with ErrDenied.
		badScope.Invocation.Maintenance.ReadBindings = []domain.MaintenanceReadBinding{
			{ReadID: sampleReadID, ProjectID: projectID, VersionID: "", TargetSetID: sampleTargetSetID},
		}
		badScopeBytes, _ := json.Marshal(badScope)
		badIntent := origIntent
		badIntent.JobID = badChildID
		badIntent.ScanJobID = badParentID
		badIntent.ItemID = badItemID
		badIntent.AdmissionReady = false
		badIntent.Captures = []domain.MaintenanceCaptureStep{
			{
				ProjectID:       projectID,
				OperationID:     "op-bad-blank-version",
				SourceMode:      "current",
				VersionID:       "",
				SourceSelection: []byte(`{"source_mode":"current","version_id":"","provider_id":"codex","selections":[{"source_id":"` + srcMainID + `","paths":[{"path":"go.mod"}]}]}`),
				CaptureID:       origIntent.Captures[0].CaptureID,
				TargetSetID:     sampleTargetSetID,
				ReadIDs:         []string{sampleReadID},
				State:           "completed",
				Finished:        true,
			},
		}
		if err := tx.CreateMaintenanceJob(domain.Job{
			ID:             badChildID,
			Kind:           "maintenance",
			ParentID:       badParentID,
			State:          "queued",
			Scope:          badScopeBytes,
			ConfigRevision: origChild.ConfigRevision,
			Permissions:    origChild.Permissions,
		}, badIntent); err != nil {
			return err
		}
		// Direct AttachSourceInputs on a queued job with blank binding VersionID returns ErrDenied.
		rawTx := tx.(*writeTx).tx
		_, err = rawTx.ExecContext(ctx, `UPDATE jobs SET state='queued',error='' WHERE job_id=?`, badChildID)
		if err != nil {
			return err
		}
		Expect(tx.AttachSourceInputs(badChildID, []string{sampleReadID})).To(MatchError(domain.ErrDenied))
		// Wrong TargetSetID in ReadBindings also returns ErrDenied.
		badScope.Invocation.Maintenance.ReadBindings = []domain.MaintenanceReadBinding{
			{ReadID: sampleReadID, ProjectID: projectID, VersionID: vFocal, TargetSetID: "wrong-target-set"},
		}
		badScopeBytes, _ = json.Marshal(badScope)
		_, err = rawTx.ExecContext(ctx, `UPDATE jobs SET scope=? WHERE job_id=?`, badScopeBytes, badChildID)
		if err != nil {
			return err
		}
		Expect(tx.AttachSourceInputs(badChildID, []string{sampleReadID})).To(MatchError(domain.ErrDenied))
		// Restore to paused/source_capture_pending to verify no-progress failure accounting.
		_, err = rawTx.ExecContext(ctx, `UPDATE jobs SET state='paused',error='source_capture_pending' WHERE job_id=?`, badChildID)
		return err
	})).To(Succeed())

	admitFailErr := srcSvc.CaptureMaintenanceAndAdmit(ctx, badChildID, codex.SemanticRequestSHA)
	Expect(admitFailErr).To(HaveOccurred())
	badChildAfter, err := s.Job(ctx, badChildID)
	Expect(err).NotTo(HaveOccurred())
	Expect(badChildAfter.State).To(Equal("paused"))
	Expect(badChildAfter.Error).To(Equal("source_capture_unavailable"))
	// First worker turn on the legacy Detail={} pending item records Pass-1 structural_ready=true while keeping it pending;
	// the second bounded turn enters semantic evaluation and settles the item as skipped on source_capture_unavailable without spinning.
	Expect(wkr.Process(ctx, badParentID)).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		it, err := tx.JobItem(badParentID, badItemID)
		if err != nil {
			return err
		}
		Expect(it.Stage).To(Equal("inspect"))
		Expect(it.State).To(Equal("pending"))
		Expect(string(it.Detail)).To(ContainSubstring(`"structural_ready":true`))
		return nil
	})).To(Succeed())
	Expect(wkr.Process(ctx, badParentID)).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		it, err := tx.JobItem(badParentID, badItemID)
		if err != nil {
			return err
		}
		Expect(it.State).To(Equal("completed"))
		Expect(string(it.Detail)).To(ContainSubstring(`"semantic_state":"skipped"`))
		return nil
	})).To(Succeed())

	// Populated Revision 20 -> 21 SQLite migration rollback, premigration snapshot, checksum verification, and additive empty maintenance_intents verification.
	_, mig20Writer := profile(GinkgoT())
	mig20Manifest, mig20Hash := referenceSchema8(mig20Writer)
	mig20DB := readDB(GinkgoT(), mig20Writer.Profile)
	for i := 8; i < 20; i++ {
		_, err = mig20DB.Exec(migrations[i])
		Expect(err).NotTo(HaveOccurred())
		_, err = mig20DB.Exec(`INSERT INTO schema_migrations VALUES(?,?,?)`, i+1, digest([]byte(migrations[i])), nowStamp())
		Expect(err).NotTo(HaveOccurred())
	}
	_, err = mig20DB.Exec(`UPDATE libraries SET schema_revision=20;PRAGMA user_version=20`)
	Expect(err).NotTo(HaveOccurred())
	mig20ScanJobID := "00000000-0000-4000-8000-000000002020"
	mig20FindingID := "00000000-0000-4000-8000-000000002021"
	_, err = mig20DB.Exec(`INSERT INTO jobs(job_id,kind,state,attempt_generation,cancel_requested,error,scope,config_revision,permissions_revisions,revision) VALUES(?,'scan','completed',1,0,'','{"principal_id":"owner"}',1,'{}',1)`, mig20ScanJobID)
	Expect(err).NotTo(HaveOccurred())
	_, err = mig20DB.Exec(`INSERT INTO scan_runs(job_id,principal_id,auth_kind,scope_hash,requested_scope,effective_policy,config_revision,permissions_revisions,stage,enum_version_ordinal,enumeration_complete,next_item_ordinal,denominator,report,captured_at,elapsed_ms,updated_at) VALUES(?,'owner','owner','hash-20','{"scope":"selected_projects"}','{"depth":"full_scope"}',1,'{}','complete',1,1,2,'{"projects":1}','{"stage":"complete"}','2026-11-08T00:00:00Z',10,'2026-11-08T00:00:01Z')`, mig20ScanJobID)
	Expect(err).NotTo(HaveOccurred())
	_, err = mig20DB.Exec(`INSERT INTO findings(finding_id,job_id,project_id,document_id,version_id,kind,origin,evidence_json,evidence_fingerprint,rationale,uncertainty,proposal_json,status,approval_required,reason,config_revision,permissions_revision,revision,created_at,updated_at) VALUES(?,?,(SELECT unfiled_project_id FROM libraries WHERE singleton=1),?,?,'backlink_index_repair','deterministic','[]','fp-20','populated schema 20 finding','none','{}','proposed',1,'ok',1,1,1,'2026-11-08T00:00:00Z','2026-11-08T00:00:00Z')`, mig20FindingID, mig20ScanJobID, refDocument, refSource)
	Expect(err).NotTo(HaveOccurred())
	Expect(mig20DB.Close()).To(Succeed())

	mig20Fault := errors.New("controlled revision 20 to 21 migration interruption")
	brokenMig20 := New(func(boundary Boundary) error {
		if boundary == BeforeMigrationCommit {
			return mig20Fault
		}
		return nil
	})
	_, _, err = brokenMig20.Prepare(ctx, mig20Writer.Profile)
	Expect(err).To(MatchError(mig20Fault))
	Expect(brokenMig20.Close()).To(Succeed())

	mig20DB = readDB(GinkgoT(), mig20Writer.Profile)
	var mig20Rev, mig20TableCount int
	Expect(mig20DB.QueryRow(`PRAGMA user_version`).Scan(&mig20Rev)).To(Succeed())
	Expect(mig20Rev).To(Equal(20))
	Expect(mig20DB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='maintenance_intents'`).Scan(&mig20TableCount)).To(Succeed())
	Expect(mig20TableCount).To(BeZero())
	for n := 1; n <= 20; n++ {
		var cs string
		Expect(mig20DB.QueryRow(`SELECT checksum FROM schema_migrations WHERE revision=?`, n).Scan(&cs)).To(Succeed())
		Expect(cs).To(Equal(digest([]byte(migrations[n-1]))))
	}
	Expect(mig20DB.Close()).To(Succeed())

	upgradedMig21 := New(nil)
	mig21State, closeUpgraded21, err := upgradedMig21.Prepare(ctx, mig20Writer.Profile)
	Expect(err).NotTo(HaveOccurred())
	defer closeUpgraded21()
	Expect(mig21State.Ready).To(BeTrue())
	mig21Health, err := upgradedMig21.Health(ctx)
	Expect(err).NotTo(HaveOccurred())
	Expect(mig21Health.Library.SchemaRevision).To(Equal(21))
	Expect(upgradedMig21.validateTables(ctx, SchemaRevision)).To(Succeed())
	for n := 1; n <= 21; n++ {
		var cs string
		Expect(upgradedMig21.db.QueryRow(`SELECT checksum FROM schema_migrations WHERE revision=?`, n).Scan(&cs)).To(Succeed())
		Expect(cs).To(Equal(digest([]byte(migrations[n-1]))))
	}
	var stored21Hash, stored21FindingRationale string
	Expect(upgradedMig21.db.QueryRow(`SELECT manifest_sha256 FROM document_versions WHERE version_id=?`, refSource).Scan(&stored21Hash)).To(Succeed())
	Expect(stored21Hash).To(Equal(mig20Hash))
	Expect(upgradedMig21.db.QueryRow(`SELECT rationale FROM findings WHERE finding_id=? AND job_id=?`, mig20FindingID, mig20ScanJobID).Scan(&stored21FindingRationale)).To(Succeed())
	Expect(stored21FindingRationale).To(Equal("populated schema 20 finding"))
	var maintIntentRows int
	Expect(upgradedMig21.db.QueryRow(`SELECT count(*) FROM maintenance_intents`).Scan(&maintIntentRows)).To(Succeed())
	Expect(maintIntentRows).To(BeZero())
	mig21Snapshots, err := filepath.Glob(filepath.Join(mig20Writer.Profile.DataDir, "storage", "pre-migration-*.sqlite"))
	Expect(err).NotTo(HaveOccurred())
	Expect(mig21Snapshots).NotTo(BeEmpty())
	for _, snap := range mig21Snapshots {
		if _, err := os.Stat(snap + ".json"); err == nil {
			copiedObj, err := os.ReadFile(filepath.Join(snap+".objects", mig20Hash))
			Expect(err).NotTo(HaveOccurred())
			Expect(copiedObj).To(Equal(mig20Manifest))
		}
	}
})

func mustUnfiledProject(s *Store) string {
	GinkgoHelper()
	h, err := s.Health(ctx)
	Expect(err).NotTo(HaveOccurred())
	return h.Library.UnfiledProjectID
}

type testExecGitReader struct{}

func (testExecGitReader) ResolveRevision(ctx context.Context, root, revision string) error {
	if strings.TrimSpace(revision) == "" {
		return errors.New("empty revision")
	}
	cmd := exec.CommandContext(ctx, "git", "-C", root, "cat-file", "-e", revision+"^{commit}")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("resolve revision %s: %w (%s)", revision, err, string(out))
	}
	return nil
}

func (testExecGitReader) ResolveHead(ctx context.Context, root string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (testExecGitReader) ChangedPaths(ctx context.Context, root, base, tip string, cited []string) ([]string, error) {
	return nil, nil
}

func (testExecGitReader) ReadBlob(ctx context.Context, root, revision, p string) (io.ReadCloser, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "show", revision+":"+p)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(strings.NewReader(string(out))), nil
}

var _ = It("enforces B01..B12 batch-1 invariants across git drift, region-scoped source evidence, origin_unit lifecycle/pagination, mixed-page granularity, K<=6 candidate cap, budget/retry guards, and restore ordering (TASK-008-02)", Label("persistence"), func() {
	s, auth, w, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "scan-task02-batch1-owner")
	f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
	projectID := mustUnfiledProject(s)
	capBytes := testCaptureBytes{s: s, bytes: f.bytes}
	srcSvc := appsource.New(s, auth, capBytes, testExecGitReader{})
	srcSvc.Files = localTestFileReader{}
	scanSvc := scan.New(s, auth, f.bytes, nil)

	// 1. Setup git repo at commit A: Region A's file (pkg/alpha/a.go) does NOT import pkg/gamma; Region B's file (pkg/beta/b.go) DOES import pkg/gamma.
	gitDir := filepath.Join(GinkgoT().TempDir(), "git-repo-b01")
	commitA := initTestGitRepo(gitDir, map[string]string{
		"go.mod":         "module example.local/batch1\n\ngo 1.22\n",
		"pkg/alpha/a.go": "package alpha\nfunc AlphaCoordinator() int { return 1 }\n",
		"pkg/beta/b.go":  "package beta\nimport \"example.local/batch1/pkg/gamma\"\nfunc BetaCoordinator() int { return gamma.GammaTarget() }\n",
		"pkg/gamma/c.go": "package gamma\nfunc GammaTarget() int { return 7 }\n",
	})
	srcGitID := "00000000-0000-4000-8000-000000000911"
	repoGitID := "00000000-0000-4000-8000-000000000912"
	srcUnavailFilesID := "00000000-0000-4000-8000-000000000913"

	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var body map[string]any
		Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
		maint, _ := body["maintenance"].(map[string]any)
		maint["scope"] = "selected_projects"
		maint["project_ids"] = []string{projectID}
		maint["depth"] = "deep_semantic"
		maint["max_concurrent_ai_jobs"] = 2
		maint["max_provider_attempts_per_run"] = 32
		maint["max_run_seconds"] = 1800
		maint["content_generation"] = "propose"
		autoApply, _ := maint["auto_apply"].(map[string]any)
		autoApply["related_explanation"] = "propose"
		autoApply["topic_assignment"] = "propose"

		projects, _ := body["projects"].(map[string]any)
		if projects == nil {
			projects = map[string]any{}
			body["projects"] = projects
		}
		projects[projectID] = map[string]any{
			"source_mode_default": "current",
			"provider_id":         "codex",
			"sources": []map[string]any{
				{
					"source_id":      srcGitID,
					"kind":           "git",
					"root_path":      gitDir,
					"repo_id":        repoGitID,
					"retention_mode": "selected_snapshot",
					"provider_ids":   []string{"codex"},
				},
				{
					"source_id":      srcUnavailFilesID,
					"kind":           "files",
					"root_path":      gitDir,
					"repo_id":        nil,
					"retention_mode": "selected_snapshot",
					"provider_ids":   []string{"codex"},
				},
			},
		}
		raw, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		normalized, err := configuration.Decode(strings.NewReader(string(raw)), "json")
		Expect(err).NotTo(HaveOccurred())
		Expect(configuration.Validate(normalized)).To(Succeed())
		cfg.Value = raw
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())

	// Publish target version vGamma (evidence -> go.mod + pkg/gamma/c.go on srcGitID) + 6 extra candidate versions so total candidates > 6 (B06),
	// with the first extra version declaring uncaptured srcUnavailFilesID to verify the B01 admission-unavailable negative path.
	vGamma := publishInProjectWithSourcesAndEvidence(
		f,
		projectID,
		map[string]map[string]any{
			"ev-gamma-mod": {"id": "ev-gamma-mod", "source_id": srcGitID, "kind": "source_span", "locator": map[string]any{"path": "go.mod"}},
			"ev-gamma-c":   {"id": "ev-gamma-c", "source_id": srcGitID, "kind": "source_span", "locator": map[string]any{"path": "pkg/gamma/c.go"}},
		},
		"<section id='reg-gamma' data-haeryong-topic='gamma-coordination'><h2>Gamma Coordination Target</h2><p>GammaTarget coordination service for alpha and beta callers.</p></section>",
		map[string]map[string]any{
			srcGitID: {"kind": "git", "repo_id": repoGitID},
		},
		map[string]any{"page_path": "page.html", "id": "reg-gamma", "label": "Gamma Coordination Target", "evidence_ids": []string{"ev-gamma-mod", "ev-gamma-c"}},
	)
	f.run(vGamma, "search")
	f.run(vGamma, "references")

	var vUnavailPeer string
	for i := 0; i < 6; i++ {
		regID := fmt.Sprintf("reg-extra-%d", i)
		var vExtra string
		if i == 0 {
			vExtra = publishInProjectWithSourcesAndEvidence(
				f,
				projectID,
				map[string]map[string]any{
					"ev-extra-unavail": {"id": "ev-extra-unavail", "source_id": srcUnavailFilesID, "kind": "source_span", "locator": map[string]any{"path": "go.mod"}},
				},
				fmt.Sprintf("<section id='%s' data-haeryong-topic='gamma-coordination'><h2>Extra Gamma Peer %d</h2><p>GammaTarget coordination notes peer %d.</p></section>", regID, i, i),
				map[string]map[string]any{
					srcUnavailFilesID: {"kind": "files"},
				},
				map[string]any{"page_path": "page.html", "id": regID, "label": fmt.Sprintf("Extra Gamma Peer %d", i), "evidence_ids": []string{"ev-extra-unavail"}},
			)
			vUnavailPeer = vExtra
		} else {
			vExtra = publishInProjectWithSources(
				f,
				projectID,
				nil,
				fmt.Sprintf("<section id='%s' data-haeryong-topic='gamma-coordination'><h2>Extra Gamma Peer %d</h2><p>GammaTarget coordination notes peer %d.</p></section>", regID, i, i),
				nil,
				map[string]any{"page_path": "page.html", "id": regID, "label": fmt.Sprintf("Extra Gamma Peer %d", i)},
			)
		}
		f.run(vExtra, "search")
		f.run(vExtra, "references")
	}

	// Publish focal version vMulti with TWO regions on page.html:
	// - reg-a: evidence -> go.mod + pkg/alpha/a.go (does NOT import pkg/gamma at commitA)
	// - reg-b: evidence -> go.mod + pkg/beta/b.go (DOES import pkg/gamma at commitA)
	// plus a second, regionless page ('page-regionless.html') in the same ContentSet so vMulti is a mixed version (1 page with regions, 1 page without regions) (B05).
	vMulti := func() string {
		version, document, set := f.id(), f.id(), f.id()
		rawManifest, _ := json.Marshal(map[string]any{"schema_version": "1.0", "viewer_profile": "html_js_v1", "references": []any{}})
		page1Body := "<section id='reg-a' data-haeryong-topic='gamma-coordination'><h2>Alpha Coordinator</h2><p>AlphaCoordinator shares gamma coordination terminology with GammaTarget.</p></section><section id='reg-b' data-haeryong-topic='gamma-coordination'><h2>Beta Coordinator</h2><p>BetaCoordinator imports and calls GammaTarget for gamma coordination.</p></section>"
		page2Body := "<!doctype html><html><head><title>Regionless Gamma Appendix</title></head><body><h1>Regionless Gamma Appendix</h1><p>GammaTarget coordination appendix without region tags.</p></body></html>"
		manifestHash := digest(rawManifest)
		page1Hash := digest([]byte(page1Body))
		page2Hash := digest([]byte(page2Body))
		f.bytes.files[manifestHash] = string(rawManifest)
		f.bytes.files[page1Hash] = page1Body
		f.bytes.files[page2Hash] = page2Body
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			if err := tx.CreateContentSet(set, digest(nil)); err != nil {
				return err
			}
			for _, o := range []domain.Object{
				{Hash: manifestHash, Size: int64(len(rawManifest)), State: "ready"},
				{Hash: page1Hash, Size: int64(len(page1Body)), State: "ready"},
				{Hash: page2Hash, Size: int64(len(page2Body)), State: "ready"},
			} {
				if err := tx.PutDurableObject(o); err != nil {
					return err
				}
			}
			if err := tx.PutEntry(domain.Entry{ContentSetID: set, Path: "page.html", Kind: "file", Hash: page1Hash, Size: int64(len(page1Body))}); err != nil {
				return err
			}
			if err := tx.PutEntry(domain.Entry{ContentSetID: set, Path: "page-regionless.html", Kind: "file", Hash: page2Hash, Size: int64(len(page2Body))}); err != nil {
				return err
			}
			if err := tx.SealContentSet(set); err != nil {
				return err
			}
			metadataID := f.id()
			if err := tx.CreateMetadataIndex(domain.MetadataIndex{ID: metadataID, PrincipalID: "system", Hash: manifestHash, ContentSetID: set}); err != nil {
				return err
			}
			if err := tx.PutImportMetadata(metadataID, "sources", srcGitID, jsonBytes(map[string]any{"kind": "git", "repo_id": repoGitID})); err != nil {
				return err
			}
			for evID, evMeta := range map[string]map[string]any{
				"ev-multi-mod": {"id": "ev-multi-mod", "source_id": srcGitID, "kind": "source_span", "locator": map[string]any{"path": "go.mod"}},
				"ev-multi-a":   {"id": "ev-multi-a", "source_id": srcGitID, "kind": "source_span", "locator": map[string]any{"path": "pkg/alpha/a.go"}},
				"ev-multi-b":   {"id": "ev-multi-b", "source_id": srcGitID, "kind": "source_span", "locator": map[string]any{"path": "pkg/beta/b.go"}},
			} {
				if err := tx.PutImportMetadata(metadataID, "evidence", evID, jsonBytes(evMeta)); err != nil {
					return err
				}
			}
			for _, regMeta := range []map[string]any{
				{"page_path": "page.html", "id": "reg-a", "label": "Alpha Coordinator", "evidence_ids": []string{"ev-multi-mod", "ev-multi-a"}},
				{"page_path": "page.html", "id": "reg-b", "label": "Beta Coordinator", "evidence_ids": []string{"ev-multi-mod", "ev-multi-b"}},
			} {
				regKey := fmt.Sprintf("%s#%s", regMeta["page_path"], regMeta["id"])
				if err := tx.PutImportMetadata(metadataID, "regions", regKey, jsonBytes(regMeta)); err != nil {
					return err
				}
			}
			if err := tx.FinalizeImport(
				domain.ImportIntent{JobID: metadataID, NewDocument: true, DocumentID: document, ProjectID: projectID, DocumentRevision: 1, Title: "References"},
				domain.Version{ID: version, DocumentID: document, ContentSetID: set, ManifestSHA: manifestHash, PayloadSHA: digest(nil), Title: "References", ViewerProfile: "html_js_v1", SourceBasis: "unverified"},
			); err != nil {
				return err
			}
			return tx.ReleaseMetadataPreparation(metadataID)
		})).To(Succeed())
		return version
	}()
	f.run(vMulti, "search")
	f.run(vMulti, "references")

	// Start scan in current mode while git HEAD is at commitA.
	scanJobID := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		ProjectID:  projectID,
		SourceMode: "current",
		Depth:      "deep_semantic",
	})
	// Verify scan-start resolution captured ResolvedRevision == commitA,
	// then advance git HEAD to commitB (where pkg/alpha/a.go imports pkg/gamma!) BEFORE child capture runs (B01).
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		cv, err := tx.ScanCapturedVersionByID(scanJobID, vMulti)
		if err != nil {
			return err
		}
		Expect(cv.Targets).NotTo(BeEmpty())
		Expect(cv.Targets[0].ResolvedRevision).To(Equal(commitA))
		return nil
	})).To(Succeed())

	commitB := commitTestGitFile(
		gitDir,
		"pkg/alpha/a.go",
		"package alpha\nimport \"example.local/batch1/pkg/gamma\"\nfunc AlphaCoordinator() int { return gamma.GammaTarget() }\n",
		"drift HEAD to commit B after scan start",
	)
	Expect(commitB).NotTo(Equal(commitA))

	driver := &scriptedMaintenanceDriver{
		store: s,
		responder: func(a domain.ProviderAttempt, inv domain.ProviderInvocation, _ []domain.ProviderInput) (string, error) {
			if nomStr, isNom := defaultNominationEnvelopeJSON(inv); isNom {
				return nomStr, nil
			}
			out := map[string]any{
				"relations": []map[string]any{
					{
						"target_version_id": vGamma,
						"target_page_path":  "page.html",
						"target_region_id":  "reg-gamma",
						"relation_type":     "depends_on",
						"rationale":         "Claims code dependency on GammaTarget.",
						"uncertainty":       "none",
					},
				},
				"topics": []map[string]any{},
				"content_gaps": []map[string]any{
					{
						"kind":        "new_document_proposal",
						"theme":       "gamma-operational-runbook",
						"rationale":   "Standalone runbook needed.",
						"uncertainty": "bounded_shortlist_only",
					},
				},
				"limitations": []map[string]any{},
			}
			raw, _ := json.Marshal(out)
			return string(raw), nil
		},
	}

	wkr := scan.NewWorker(s, f.bytes)
	wkr.Sources = srcSvc
	wkr.Identity = codex.SemanticRequestSHA
	coordinator := work.ProviderCoordinator{
		Store:  s,
		Bytes:  capBytes,
		Driver: driver,
		MaintenanceSource: func(ctx context.Context, jobID string) error {
			return srcSvc.CaptureMaintenanceAndAdmit(ctx, jobID, codex.SemanticRequestSHA)
		},
		Prepare: wkr.PrepareMaintenance,
		Apply:   wkr.ApplyMaintenance,
	}

	for turn := 0; turn < 128; turn++ {
		jCur, e := s.Job(ctx, scanJobID)
		Expect(e).NotTo(HaveOccurred())
		if jCur.State == "completed" || jCur.State == "failed" || jCur.State == "cancelled" || jCur.State == "paused" {
			break
		}
		Expect(wkr.Process(ctx, scanJobID)).To(Succeed())
		var childIDs []string
		rows, qErr := s.db.QueryContext(ctx, `SELECT job_id FROM maintenance_intents WHERE scan_job_id=? ORDER BY job_id`, scanJobID)
		Expect(qErr).NotTo(HaveOccurred())
		for rows.Next() {
			var cid string
			Expect(rows.Scan(&cid)).To(Succeed())
			childIDs = append(childIDs, cid)
		}
		Expect(rows.Close()).To(Succeed())
		for _, cid := range childIDs {
			cj, cErr := s.Job(ctx, cid)
			if cErr == nil && (cj.State == "queued" || cj.State == "running" || cj.State == "reconciling" || (cj.State == "paused" && cj.Error == "source_capture_pending")) {
				Expect(coordinator.Process(ctx, cid)).To(Succeed())
			}
		}
	}

	jDone, err := s.Job(ctx, scanJobID)
	Expect(err).NotTo(HaveOccurred())
	Expect(jDone.State).To(Equal("completed"))

	// Verify B05, B06 (K <= 6 cap), and B01 (captured reads use commitA, never drifted commitB, and uncaptured files source records source_unavailable without live fallback).
	var sawRegionlessPageAssessed, sawUnavailableFilesLimitation bool
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		items, err := tx.JobItems(scanJobID, 0, 100)
		if err != nil {
			return err
		}
		for _, it := range items {
			if it.UnitKind == "page" && it.VersionID == vMulti && it.PagePath == "page-regionless.html" {
				if strings.Contains(string(it.Detail), `"semantic_state":"assessed"`) {
					sawRegionlessPageAssessed = true
				}
			}
			if m, ok, mErr := tx.MaintenanceIntentByItem(scanJobID, it.ItemID); mErr == nil && ok {
				Expect(len(m.Shortlist.Candidates)).To(BeNumerically("<=", 6), "expected shortlist K <= 6 (B06)")
				for _, lim := range m.Limitations {
					if lim.Kind == "source_unavailable" && lim.Reason == "source_capture_required" && lim.TargetVersionID == vUnavailPeer && lim.Message == srcUnavailFilesID {
						sawUnavailableFilesLimitation = true
					}
				}
				for _, rid := range m.SourceReadIDs {
					cr, rErr := tx.CapturedRead(rid)
					Expect(rErr).NotTo(HaveOccurred())
					var crSrc struct {
						SourceID      string `json:"source_id"`
						ExactRevision string `json:"exact_revision"`
					}
					Expect(json.Unmarshal(cr.SourceJSON, &crSrc)).To(Succeed())
					Expect(crSrc.SourceID).NotTo(Equal(srcUnavailFilesID), "uncaptured files source must never fall back to live working-copy reads (B01)")
					Expect(crSrc.ExactRevision).To(Equal(commitA), "expected current-mode git capture to stay bound to scan-start commitA, not drifted commitB (B01)")
				}
			}
		}
		return nil
	})).To(Succeed())
	Expect(sawRegionlessPageAssessed).To(BeTrue(), "expected regionless page on mixed version vMulti to be semantically evaluated at page scope (B05)")
	Expect(sawUnavailableFilesLimitation).To(BeTrue(), "expected admission-time unavailable files target to record source_unavailable/source_capture_required without live fallback (B01)")

	// Verify B02 & B12: on vMulti, reg-a's relation to vGamma is downgraded to 'related' (because pkg/alpha/a.go at commitA does not import pkg/gamma,
	// and reg-b's pkg/beta/b.go is NOT leaked into reg-a's unit verification or evidence), whereas reg-b's relation to vGamma IS verified as 'depends_on' (source_fact)!
	fList, err := scanSvc.ListJobFindings(ctx, owner.Token, domain.FindingFilter{JobID: scanJobID, Limit: 100})
	Expect(err).NotTo(HaveOccurred())
	var sawRegADowngraded, sawRegBVerified bool
	for _, fd := range fList.Items {
		if fd.VersionID != nil && *fd.VersionID == vMulti && fd.Kind == "related_explanation" && fd.RegionID != nil {
			var prop map[string]any
			Expect(json.Unmarshal(fd.Proposal, &prop)).To(Succeed())
			if *fd.RegionID == "reg-a" && prop["target_version_id"] == vGamma {
				sawRegADowngraded = true
				Expect(prop["relation_type"]).To(Equal("related"))
				Expect(prop["evidence_basis"]).To(Equal("thematic_analogy"))
				Expect(prop["downgraded_from"]).To(Equal("depends_on"))
				Expect(fd.Rationale).To(ContainSubstring("Unverified code-dependency claim withdrawn"))
				Expect(string(fd.Evidence)).NotTo(ContainSubstring("pkg/beta/b.go"), "sibling region reg-b source file must not attach to reg-a finding (B02)")
			}
			if *fd.RegionID == "reg-b" && prop["target_version_id"] == vGamma {
				sawRegBVerified = true
				Expect(prop["relation_type"]).To(Equal("depends_on"))
				Expect(prop["evidence_basis"]).To(Equal("source_fact"))
				Expect(prop["verified_dependency"]).To(Equal("call:gamma.GammaTarget"))
			}
		}
	}
	Expect(sawRegADowngraded).To(BeTrue(), "expected reg-a dependency claim to downgrade to related without cross-region leakage or commitB drift (B01, B02, B12)")
	Expect(sawRegBVerified).To(BeTrue(), "expected reg-b dependency claim to verify as depends_on/source_fact (B02)")

	// Verify B03 & B07: Trashing vMulti's document fences its new_document_proposal (which has nil DocumentID/VersionID columns) via $.origin_unit,
	// and ListJobFindings with Limit: 1 pages past filtered rows to return the healthy vGamma finding!
	verMultiRow, err := s.Version(ctx, vMulti)
	Expect(err).NotTo(HaveOccurred())
	Expect(applyDocumentLifecycle(s, auth, f.bytes, owner.Token, verMultiRow.DocumentID, "trash").Lifecycle).To(Equal("trash"))

	pagedOne, err := scanSvc.ListJobFindings(ctx, owner.Token, domain.FindingFilter{JobID: scanJobID, Limit: 1})
	Expect(err).NotTo(HaveOccurred())
	Expect(pagedOne.Items).To(HaveLen(1), "expected ListJobFindings(Limit=1) to page past trashed vMulti findings and return 1 visible finding (B07)")
	Expect(string(pagedOne.Items[0].Proposal)).NotTo(ContainSubstring(vMulti))

	allVisibleAfterTrash, err := scanSvc.ListJobFindings(ctx, owner.Token, domain.FindingFilter{JobID: scanJobID, Limit: 100})
	Expect(err).NotTo(HaveOccurred())
	for _, fd := range allVisibleAfterTrash.Items {
		Expect(string(fd.Proposal)).NotTo(ContainSubstring(vMulti), "trashed vMulti origin_unit new_document_proposal must not be visible (B03, B07)")
	}

	// Verify B10: reviewRestoredScan succeeds when jobs table contains scan + maintenance child rows.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		wt := tx.(*writeTx)
		return wt.reviewRestoredScan(jDone)
	})).To(Succeed())

	// Verify B03 purge redaction: redactPurgedScanRecords immediately deletes vMulti's new_document_proposal finding while preserving healthy vGamma findings.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		wt := tx.(*writeTx)
		if _, err := wt.tx.ExecContext(ctx, `INSERT OR IGNORE INTO purged_versions VALUES(?,?,?)`, vMulti, verMultiRow.DocumentID, nowStamp()); err != nil {
			return err
		}
		if err := wt.redactPurgedScanRecords(verMultiRow.DocumentID); err != nil {
			return err
		}
		var remainingMultiFindings int
		if err := wt.tx.QueryRow(`SELECT count(*) FROM findings WHERE job_id=? AND json_extract(proposal_json,'$.origin_unit.version_id')=?`, scanJobID, vMulti).Scan(&remainingMultiFindings); err != nil {
			return err
		}
		Expect(remainingMultiFindings).To(Equal(0), "expected redactPurgedScanRecords to delete origin-owned new_document_proposal findings (B03)")
		var remainingGammaFindings int
		if err := wt.tx.QueryRow(`SELECT count(*) FROM findings WHERE job_id=? AND json_extract(proposal_json,'$.origin_unit.version_id')=?`, scanJobID, vGamma).Scan(&remainingGammaFindings); err != nil {
			return err
		}
		Expect(remainingGammaFindings).To(BeNumerically(">=", 1), "expected healthy sibling findings to survive purge redaction (B03)")
		return nil
	})).To(Succeed())

	// Verify B08: exhaustRunBudget and revokeRunAuthority on a scan with a completed child do NOT misclassify completed attempts as outcome_unknown.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		cnt, err := tx.ScanProviderAttemptCount(scanJobID, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(cnt).To(BeNumerically(">=", 1))
		jCur, err := tx.Job(scanJobID)
		Expect(err).NotTo(HaveOccurred())
		Expect(jCur.ProviderAttempts).NotTo(BeNil())
		Expect(jCur.ProviderAttempts.Unknown).To(Equal(0))
		return nil
	})).To(Succeed())

	// Verify B09: checkScanResumeBudget blocks decision="retry" when max_provider_attempts_per_run is narrowed to <= usedAttempts, while allowing decision="continue" and "reconcile_only".
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		cfg, err := tx.Configuration()
		if err != nil {
			return err
		}
		var body map[string]any
		Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
		maint, _ := body["maintenance"].(map[string]any)
		maint["max_provider_attempts_per_run"] = 1
		raw, _ := json.Marshal(body)
		cfg.Value = raw
		if err := tx.SetConfiguration(cfg, cfg.Revision); err != nil {
			return err
		}
		wt := tx.(*writeTx)
		Expect(wt.checkScanResumeBudget(scanJobID, "retry")).To(MatchError(domain.ErrDenied))
		Expect(wt.checkScanResumeBudget(scanJobID, "continue")).To(Succeed())
		Expect(wt.checkScanResumeBudget(scanJobID, "reconcile_only")).To(Succeed())
		return nil
	})).To(Succeed())
})

var _ = It("enforces B08 worker budget/revocation settlement after completed child vs unknown protection and B10 restored-scan ordering with zero or unknown maintenance children (TASK-008-02-B08-B10)", Label("persistence"), func() {
	s, auth, w, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "scan-task02-b08-b10-owner")
	f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
	capBytes := testCaptureBytes{s: s, bytes: f.bytes}
	srcSvc := appsource.New(s, auth, capBytes, nil)
	srcSvc.Files = localTestFileReader{}
	scanSvc := scan.New(s, auth, f.bytes, nil)

	projBudgetID := mustUnfiledProject(s)
	projRevokeID := "00000000-0000-4000-8000-000000000961"
	projUnkID := "00000000-0000-4000-8000-000000000962"
	srcBudgetID := "00000000-0000-4000-8000-000000000963"
	srcRevokeID := "00000000-0000-4000-8000-000000000964"
	srcUnkID := "00000000-0000-4000-8000-000000000965"

	srcRoot := filepath.Join(GinkgoT().TempDir(), "src-b08-b10")
	Expect(os.MkdirAll(srcRoot, 0o755)).To(Succeed())
	goModContent := "module example.local/b08b10\n\ngo 1.22\n"
	mainGoContent := "package main\nfunc B08B10Helper() {}\n"
	Expect(os.WriteFile(filepath.Join(srcRoot, "go.mod"), []byte(goModContent), 0o644)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(srcRoot, "main.go"), []byte(mainGoContent), 0o644)).To(Succeed())
	goModSHA := digest([]byte(goModContent))

	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.RegisterProject(domain.Project{ID: projRevokeID, Name: "Revoke Project"}); err != nil {
			return err
		}
		if err := tx.RegisterProject(domain.Project{ID: projUnkID, Name: "Unknown Project"}); err != nil {
			return err
		}
		cfg, err := tx.Configuration()
		if err != nil {
			return err
		}
		var body map[string]any
		Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
		maint, _ := body["maintenance"].(map[string]any)
		maint["scope"] = "selected_projects"
		maint["project_ids"] = []string{projBudgetID}
		maint["depth"] = "deep_semantic"
		maint["max_concurrent_ai_jobs"] = 2
		maint["max_provider_attempts_per_run"] = 32
		maint["max_run_seconds"] = 60
		maint["content_generation"] = "propose"
		autoApply, _ := maint["auto_apply"].(map[string]any)
		autoApply["related_explanation"] = "propose"
		autoApply["topic_assignment"] = "propose"

		projects, _ := body["projects"].(map[string]any)
		if projects == nil {
			projects = map[string]any{}
			body["projects"] = projects
		}
		mkProj := func(sid string) map[string]any {
			return map[string]any{
				"source_mode_default":    "current",
				"provider_id":            "codex",
				"post_work_scan_enabled": true,
				"sources": []map[string]any{
					{
						"source_id":      sid,
						"kind":           "files",
						"root_path":      srcRoot,
						"repo_id":        nil,
						"retention_mode": "selected_snapshot",
						"provider_ids":   []string{"codex"},
					},
				},
			}
		}
		projects[projBudgetID] = mkProj(srcBudgetID)
		projects[projRevokeID] = mkProj(srcRevokeID)
		projects[projUnkID] = mkProj(srcUnkID)
		raw, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		normalized, err := configuration.Decode(strings.NewReader(string(raw)), "json")
		Expect(err).NotTo(HaveOccurred())
		Expect(configuration.Validate(normalized)).To(Succeed())
		cfg.Value = raw
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())

	seedTwoVersionsWithCaptures := func(projID, sid, tag string) (string, string) {
		ev := map[string]map[string]any{
			"ev-mod":  {"id": "ev-mod", "source_id": sid, "kind": "source_span", "locator": map[string]any{"path": "go.mod"}},
			"ev-main": {"id": "ev-main", "source_id": sid, "kind": "source_span", "locator": map[string]any{"path": "main.go"}},
		}
		srcs := map[string]map[string]any{sid: {"kind": "files"}}
		v1 := publishInProjectWithSourcesAndEvidence(
			f,
			projID,
			ev,
			fmt.Sprintf("<section id='reg-1'><h2>%s Alpha</h2><p>First unit for %s B08B10Helper.</p></section>", tag, tag),
			srcs,
			map[string]any{"page_path": "page.html", "id": "reg-1", "label": tag + " Alpha", "evidence_ids": []string{"ev-mod", "ev-main"}},
		)
		f.run(v1, "search")
		f.run(v1, "references")

		v2 := publishInProjectWithSourcesAndEvidence(
			f,
			projID,
			ev,
			fmt.Sprintf("<section id='reg-2'><h2>%s Beta</h2><p>Second unit for %s B08B10Helper.</p></section>", tag, tag),
			srcs,
			map[string]any{"page_path": "page.html", "id": "reg-2", "label": tag + " Beta", "evidence_ids": []string{"ev-mod", "ev-main"}},
		)
		f.run(v2, "search")
		f.run(v2, "references")

		for _, vid := range []string{v1, v2} {
			resp, cErr := srcSvc.Capture(ctx, owner.Token, f.id(), projID, appsource.CaptureRequest{
				VersionID:  vid,
				SourceMode: "current",
				Selections: []appsource.CaptureSelection{
					{
						SourceID: sid,
						Paths: []appsource.CapturePath{
							{Path: "go.mod"},
							{Path: "main.go"},
						},
					},
				},
			})
			Expect(cErr).NotTo(HaveOccurred())
			Expect(resp.Status).To(Equal(201))
		}
		return v1, v2
	}

	driver := &scriptedMaintenanceDriver{
		store:        s,
		unknownOnJob: map[string]bool{},
	}
	clock := time.Now().UTC()
	wkr := scan.NewWorker(s, f.bytes)
	wkr.Sources = srcSvc
	wkr.Identity = codex.SemanticRequestSHA
	wkr.Now = func() time.Time { return clock }
	coordinator := work.ProviderCoordinator{
		Store:  s,
		Bytes:  capBytes,
		Driver: driver,
		MaintenanceSource: func(ctx context.Context, jobID string) error {
			return srcSvc.CaptureMaintenanceAndAdmit(ctx, jobID, codex.SemanticRequestSHA)
		},
		Prepare: wkr.PrepareMaintenance,
		Apply:   wkr.ApplyMaintenance,
	}

	stepUntilFirstChild := func(scanJobID string) string {
		var childID string
		for turn := 0; turn < 24 && childID == ""; turn++ {
			Expect(wkr.Process(ctx, scanJobID)).To(Succeed())
			_ = s.db.QueryRowContext(ctx, `SELECT job_id FROM maintenance_intents WHERE scan_job_id=? ORDER BY job_id LIMIT 1`, scanJobID).Scan(&childID)
		}
		Expect(childID).NotTo(BeEmpty())
		return childID
	}

	// 1. B08 exhaustRunBudget via wkr.Process after a completed child maintenance attempt settles to completed (not outcome_unknown) and releases job pins.
	_, _ = seedTwoVersionsWithCaptures(projBudgetID, srcBudgetID, "budgetcase")
	jobBudget := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		ProjectID:  projBudgetID,
		SourceMode: "current",
		Depth:      "deep_semantic",
		Sources: []domain.ScanSourceSelector{
			{ProjectID: projBudgetID, SourceID: srcBudgetID, Mode: "current", SnapshotID: goModSHA},
		},
	})
	childB1 := stepUntilFirstChild(jobBudget)
	Expect(coordinator.Process(ctx, childB1)).To(Succeed())
	jChildB1, err := s.Job(ctx, childB1)
	Expect(err).NotTo(HaveOccurred())
	Expect(jChildB1.State).To(Equal("completed"))
	// Step parent once so childB1's item settles while the second version's region remains unfinished.
	Expect(wkr.Process(ctx, jobBudget)).To(Succeed())

	jPreBudget, err := s.Job(ctx, jobBudget)
	Expect(err).NotTo(HaveOccurred())
	Expect(jPreBudget.State).To(Equal("running"))
	Expect(jPreBudget.ProviderAttempts).NotTo(BeNil())
	Expect(jPreBudget.ProviderAttempts.Count).To(Equal(1))
	Expect(jPreBudget.ProviderAttempts.Unknown).To(Equal(0))
	var budgetPinsBefore int
	Expect(s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='job' AND owner_id=?`, jobBudget).Scan(&budgetPinsBefore)).To(Succeed())
	Expect(budgetPinsBefore).To(BeNumerically(">", 0))

	// Advance clock past max_run_seconds=60s and invoke wkr.Process -> enters exhaustRunBudget.
	clock = clock.Add(75 * time.Second)
	Expect(wkr.Process(ctx, jobBudget)).To(Succeed())
	jPostBudget, err := s.Job(ctx, jobBudget)
	Expect(err).NotTo(HaveOccurred())
	Expect(jPostBudget.State).To(Equal("completed"), "completed child attempt must not cause exhaustRunBudget to pause parent as outcome_unknown (B08)")
	Expect(jPostBudget.Error).To(BeEmpty())
	var runPostBudget domain.ScanRun
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		runPostBudget, e = tx.ScanRun(jobBudget)
		return e
	})).To(Succeed())
	Expect(runPostBudget.Report.BudgetExhausted).To(BeTrue())
	var sawSkippedBudgetItem bool
	for _, it := range allJobItems(s, jobBudget) {
		if it.State == "skipped" && it.Reason == "run_budget_exhausted" {
			sawSkippedBudgetItem = true
		}
	}
	Expect(sawSkippedBudgetItem).To(BeTrue())
	var budgetPinsAfter int
	Expect(s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='job' AND owner_id=?`, jobBudget).Scan(&budgetPinsAfter)).To(Succeed())
	Expect(budgetPinsAfter).To(Equal(0), "exhaustRunBudget with zero unknown attempts must release job pins (B08)")

	// 2. B08 revokeRunAuthority via wkr.Process after a completed child maintenance attempt settles to completed (not outcome_unknown) and releases job pins.
	clock = time.Now().UTC()
	_, _ = seedTwoVersionsWithCaptures(projRevokeID, srcRevokeID, "revokecase")
	jobRevoke := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		ProjectID:  projRevokeID,
		SourceMode: "current",
		Depth:      "deep_semantic",
		Sources: []domain.ScanSourceSelector{
			{ProjectID: projRevokeID, SourceID: srcRevokeID, Mode: "current", SnapshotID: goModSHA},
		},
	})
	childR1 := stepUntilFirstChild(jobRevoke)
	Expect(coordinator.Process(ctx, childR1)).To(Succeed())
	jChildR1, err := s.Job(ctx, childR1)
	Expect(err).NotTo(HaveOccurred())
	Expect(jChildR1.State).To(Equal("completed"))
	Expect(wkr.Process(ctx, jobRevoke)).To(Succeed())

	jPreRevoke, err := s.Job(ctx, jobRevoke)
	Expect(err).NotTo(HaveOccurred())
	Expect(jPreRevoke.State).To(Equal("running"))
	Expect(jPreRevoke.ProviderAttempts).NotTo(BeNil())
	Expect(jPreRevoke.ProviderAttempts.Count).To(Equal(1))
	Expect(jPreRevoke.ProviderAttempts.Unknown).To(Equal(0))
	var revokePinsBefore int
	Expect(s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='job' AND owner_id=?`, jobRevoke).Scan(&revokePinsBefore)).To(Succeed())
	Expect(revokePinsBefore).To(BeNumerically(">", 0))

	// Rotate auth_epoch to revoke run authority and invoke wkr.Process -> enters revokeRunAuthority.
	var origEpochRevoke string
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		st, e := tx.AuthState()
		if e != nil {
			return e
		}
		origEpochRevoke = st.AuthEpoch
		_, e = tx.(*writeTx).tx.ExecContext(ctx, `UPDATE libraries SET auth_epoch='rotated-epoch-b08-revoke'`)
		return e
	})).To(Succeed())
	Expect(wkr.Process(ctx, jobRevoke)).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.(*writeTx).tx.ExecContext(ctx, `UPDATE libraries SET auth_epoch=?`, origEpochRevoke)
		return e
	})).To(Succeed())
	jPostRevoke, err := s.Job(ctx, jobRevoke)
	Expect(err).NotTo(HaveOccurred())
	Expect(jPostRevoke.State).To(Equal("completed"), "completed child attempt must not cause revokeRunAuthority to pause parent as outcome_unknown (B08)")
	Expect(jPostRevoke.Error).To(BeEmpty())
	var sawBlockedRevokeItem bool
	for _, it := range allJobItems(s, jobRevoke) {
		if it.State == "blocked" && it.Reason == "authority_revoked" {
			sawBlockedRevokeItem = true
		}
	}
	Expect(sawBlockedRevokeItem).To(BeTrue())
	var revokePinsAfter int
	Expect(s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='job' AND owner_id=?`, jobRevoke).Scan(&revokePinsAfter)).To(Succeed())
	Expect(revokePinsAfter).To(Equal(0), "revokeRunAuthority with zero unknown attempts must release job pins (B08)")

	// 3. B08 true-unknown protection: when a child maintenance attempt is genuinely outcome_unknown, both exhaustRunBudget and revokeRunAuthority pause parent as outcome_unknown and retain job pins.
	clock = time.Now().UTC()
	_, _ = seedTwoVersionsWithCaptures(projUnkID, srcUnkID, "unknowncase")
	jobUnk := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		ProjectID:  projUnkID,
		SourceMode: "current",
		Depth:      "deep_semantic",
		Sources: []domain.ScanSourceSelector{
			{ProjectID: projUnkID, SourceID: srcUnkID, Mode: "current", SnapshotID: goModSHA},
		},
	})
	childUnk := stepUntilFirstChild(jobUnk)
	driver.unknownOnJob[childUnk] = true
	_ = coordinator.Process(ctx, childUnk)
	jChildUnk, err := s.Job(ctx, childUnk)
	Expect(err).NotTo(HaveOccurred())
	Expect(jChildUnk.State).To(Equal("paused"))
	Expect(jChildUnk.Error).To(Equal("outcome_unknown"))
	jParentUnk, err := s.Job(ctx, jobUnk)
	Expect(err).NotTo(HaveOccurred())
	Expect(jParentUnk.ProviderAttempts).NotTo(BeNil())
	Expect(jParentUnk.ProviderAttempts.Unknown).To(Equal(1))

	// 3a. Trigger exhaustRunBudget on jobUnk with true unknown child attempt -> parent pauses as outcome_unknown and retains job pins.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.(*writeTx).tx.ExecContext(ctx, `UPDATE jobs SET state='running',error='' WHERE job_id=?`, jobUnk)
		return e
	})).To(Succeed())
	clock = time.Now().UTC().Add(75 * time.Second)
	Expect(wkr.Process(ctx, jobUnk)).To(Succeed())
	jUnkAfterBudget, err := s.Job(ctx, jobUnk)
	Expect(err).NotTo(HaveOccurred())
	Expect(jUnkAfterBudget.State).To(Equal("paused"))
	Expect(jUnkAfterBudget.Error).To(Equal("outcome_unknown"))
	var unkPinsAfterBudget int
	Expect(s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='job' AND owner_id=?`, jobUnk).Scan(&unkPinsAfterBudget)).To(Succeed())
	Expect(unkPinsAfterBudget).To(BeNumerically(">", 0), "exhaustRunBudget must retain job pins when an unknown provider attempt exists (B08)")

	// 3b. Trigger revokeRunAuthority on jobUnk with true unknown child attempt -> parent pauses as outcome_unknown and retains job pins.
	clock = time.Now().UTC()
	var origAuthEpoch string
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		st, e := tx.AuthState()
		if e != nil {
			return e
		}
		origAuthEpoch = st.AuthEpoch
		nowStr := clock.Format(time.RFC3339Nano)
		if _, e := tx.(*writeTx).tx.ExecContext(ctx, `UPDATE jobs SET state='running',error='' WHERE job_id=?`, jobUnk); e != nil {
			return e
		}
		if _, e := tx.(*writeTx).tx.ExecContext(ctx, `UPDATE scan_runs SET stage='inspect',started_at=?,elapsed_ms=0 WHERE job_id=?`, nowStr, jobUnk); e != nil {
			return e
		}
		_, e = tx.(*writeTx).tx.ExecContext(ctx, `UPDATE libraries SET auth_epoch='rotated-epoch-b08-unk'`)
		return e
	})).To(Succeed())
	Expect(wkr.Process(ctx, jobUnk)).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.(*writeTx).tx.ExecContext(ctx, `UPDATE libraries SET auth_epoch=?`, origAuthEpoch)
		return e
	})).To(Succeed())
	jUnkAfterRevoke, err := s.Job(ctx, jobUnk)
	Expect(err).NotTo(HaveOccurred())
	Expect(jUnkAfterRevoke.State).To(Equal("paused"))
	Expect(jUnkAfterRevoke.Error).To(Equal("outcome_unknown"))
	var unkPinsAfterRevoke int
	Expect(s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='job' AND owner_id=?`, jobUnk).Scan(&unkPinsAfterRevoke)).To(Succeed())
	Expect(unkPinsAfterRevoke).To(BeNumerically(">", 0), "revokeRunAuthority must retain job pins when an unknown provider attempt exists (B08)")

	// 4. B10 actual ProtectRestoredDatabase -> ReviewRecovery path covering both (a) a scan with zero maintenance children and (b) a scan with an unknown maintenance child hold.
	jobZeroMaint := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		ProjectID:  projBudgetID,
		SourceMode: "current",
		Depth:      "change_focused",
		Sources: []domain.ScanSourceSelector{
			{ProjectID: projBudgetID, SourceID: srcBudgetID, Mode: "current", SnapshotID: goModSHA},
		},
	})
	copiedPath := filepath.Join(GinkgoT().TempDir(), "restored-b10.sqlite")
	Expect(s.SnapshotDatabase(ctx, copiedPath)).To(Succeed())
	_, err = ProtectRestoredDatabase(ctx, copiedPath, w.Profile, "b10-restore-hold", json.RawMessage(`{}`), func(map[string]any) error { return nil })
	Expect(err).NotTo(HaveOccurred())

	restoredProfile := service.Profile{
		ProfileID:    w.Profile.ProfileID,
		LibraryID:    w.Profile.LibraryID,
		DataDir:      filepath.Dir(filepath.Dir(copiedPath)),
		StorageState: "ready",
	}
	Expect(os.MkdirAll(filepath.Join(restoredProfile.DataDir, "storage"), 0o700)).To(Succeed())
	restoredStorePath := filepath.Join(restoredProfile.DataDir, "storage", "library.sqlite")
	rawRestoredBytes, err := os.ReadFile(copiedPath)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(restoredStorePath, rawRestoredBytes, 0o600)).To(Succeed())

	rs := New(nil)
	_, closeRS, err := rs.Prepare(ctx, restoredProfile)
	Expect(err).NotTo(HaveOccurred())
	defer closeRS()

	Expect(rs.Write(ctx, func(tx app.WriteTx) error {
		return tx.ReviewRecovery("b10-restore-hold", []string{jobZeroMaint, jobUnk})
	})).To(Succeed())

	jZeroAfterReview, err := rs.Job(ctx, jobZeroMaint)
	Expect(err).NotTo(HaveOccurred())
	Expect(jZeroAfterReview.State).To(Equal("queued"), "restored scan with zero maintenance children must requeue cleanly (B10)")
	Expect(jZeroAfterReview.Error).To(BeEmpty())
	var zeroHoldRemaining bool
	Expect(rs.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM resource_revisions WHERE resource_kind='restore_job_hold' AND resource_id=?)`, jobZeroMaint).Scan(&zeroHoldRemaining)).To(Succeed())
	Expect(zeroHoldRemaining).To(BeFalse())

	jUnkParentAfterReview, err := rs.Job(ctx, jobUnk)
	Expect(err).NotTo(HaveOccurred())
	Expect(jUnkParentAfterReview.State).To(Equal("paused"), "restored scan with unknown maintenance child must remain paused as outcome_unknown (B10)")
	Expect(jUnkParentAfterReview.Error).To(Equal("outcome_unknown"))
	jUnkChildAfterReview, err := rs.Job(ctx, childUnk)
	Expect(err).NotTo(HaveOccurred())
	Expect(jUnkChildAfterReview.State).To(Equal("paused"))
	Expect(jUnkChildAfterReview.Error).To(Equal("outcome_unknown"))
	var unkChildHoldRemaining bool
	Expect(rs.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM resource_revisions WHERE resource_kind='restore_job_hold' AND resource_id=?)`, childUnk).Scan(&unkChildHoldRemaining)).To(Succeed())
	Expect(unkChildHoldRemaining).To(BeTrue(), "unknown maintenance child must retain its restore_job_hold row across ReviewRecovery (B10)")
})

var _ = It("verifies TASK-008-02 quality repair QR1..QR5: 24-region acquisition across 4 pages with source0 qualification, distinct same-pair mechanisms without overwrite, paraphrase topic grounding, focal-only gap direction, and semantic.assessed==0", Label("persistence"), func() {
	ctx := context.Background()
	s, auth, w, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "scan-task02-qr-owner")
	f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
	capBytes := testCaptureBytes{s: s, bytes: f.bytes}
	scanSvc := scan.New(s, auth, f.bytes, nil)
	projAID := mustUnfiledProject(s)
	projBID := "00000000-0000-4000-8000-000000000971"

	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.RegisterProject(domain.Project{ID: projBID, Name: "Project QR Beta"}); err != nil {
			return err
		}
		cfg, err := tx.Configuration()
		if err != nil {
			return err
		}
		var body map[string]any
		Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
		maint, _ := body["maintenance"].(map[string]any)
		Expect(maint).NotTo(BeNil())
		maint["scope"] = "selected_projects"
		maint["project_ids"] = []string{projAID, projBID}
		maint["depth"] = "deep_semantic"
		maint["max_concurrent_ai_jobs"] = 2
		maint["max_provider_attempts_per_run"] = 2
		maint["max_run_seconds"] = 60
		maint["content_generation"] = "propose"
		autoApply, _ := maint["auto_apply"].(map[string]any)
		Expect(autoApply).NotTo(BeNil())
		autoApply["related_explanation"] = "propose"
		autoApply["topic_assignment"] = "propose"

		projects, _ := body["projects"].(map[string]any)
		if projects == nil {
			projects = map[string]any{}
			body["projects"] = projects
		}
		for _, pid := range []string{projAID, projBID} {
			projects[pid] = map[string]any{
				"source_mode_default":    "pinned",
				"provider_id":            "codex",
				"post_work_scan_enabled": true,
				"sources":                []any{},
			}
		}
		raw, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		normalized, err := configuration.Decode(strings.NewReader(string(raw)), "json")
		Expect(err).NotTo(HaveOccurred())
		Expect(configuration.Validate(normalized)).To(Succeed())
		cfg.Value = raw
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())

	expectedRegionTuples := map[string]bool{}
	publishTwoPageTwelveRegionVersion := func(projectID, prefix string) string {
		var archSections, opsSections strings.Builder
		var regs []map[string]any
		for i := 1; i <= 6; i++ {
			regID := fmt.Sprintf("reg-%s-arch-%d", prefix, i)
			label := fmt.Sprintf("%s Architecture Section %d", strings.ToUpper(prefix), i)
			body := fmt.Sprintf("Section %d explains checkpoint reconciliation, coordinating bounded retry backoff, and isolating snapshot readers while mentioning lease fencing.", i)
			archSections.WriteString(fmt.Sprintf("<section id=%q><h2>%s</h2><p>%s</p></section>", regID, label, body))
			regs = append(regs, map[string]any{
				"page_path": "arch.html",
				"id":        regID,
				"label":     label,
				"topics":    []string{"distributed-deadlock-arbitration"},
			})
		}
		for i := 1; i <= 6; i++ {
			regID := fmt.Sprintf("reg-%s-ops-%d", prefix, i)
			label := fmt.Sprintf("%s Operations Section %d", strings.ToUpper(prefix), i)
			body := fmt.Sprintf("Operations section %d details runtime recovery, checkpoint reconciliation, coordinating bounded retry backoff, and isolating snapshot readers while mentioning lease fencing.", i)
			opsSections.WriteString(fmt.Sprintf("<section id=%q><h2>%s</h2><p>%s</p></section>", regID, label, body))
			regs = append(regs, map[string]any{
				"page_path": "ops.html",
				"id":        regID,
				"label":     label,
				"topics":    []string{"distributed-deadlock-arbitration"},
			})
		}
		vID := publishMultiPageInProjectWithSources(f, projectID, map[string]string{
			"arch.html": "<!doctype html><html><head><title>" + prefix + " Architecture</title></head><body>" + archSections.String() + "</body></html>",
			"ops.html":  "<!doctype html><html><head><title>" + prefix + " Operations</title></head><body>" + opsSections.String() + "</body></html>",
		}, nil, regs...)
		f.run(vID, "search")
		f.run(vID, "references")
		for _, r := range regs {
			expectedRegionTuples[vID+"\x00"+r["page_path"].(string)+"\x00"+r["id"].(string)] = true
		}
		return vID
	}

	vA := publishTwoPageTwelveRegionVersion(projAID, "alpha")
	vB := publishTwoPageTwelveRegionVersion(projBID, "beta")
	Expect(expectedRegionTuples).To(HaveLen(24))

	// QR4 & OR1: Verify 24 registered regions across 4 pages and parent-EOF searchReadUnits consistency BEFORE scan dispatch.
	var totalRegions, totalPages, totalRegionSearchUnits int
	Expect(s.db.QueryRowContext(ctx, `SELECT count(*) FROM regions WHERE version_id IN (?,?)`, vA, vB).Scan(&totalRegions)).To(Succeed())
	Expect(totalRegions).To(Equal(24))
	Expect(s.db.QueryRowContext(ctx, `SELECT count(*) FROM version_pages WHERE version_id IN (?,?)`, vA, vB).Scan(&totalPages)).To(Succeed())
	Expect(totalPages).To(Equal(4))
	Expect(s.db.QueryRowContext(ctx, `SELECT count(*) FROM search_units WHERE version_id IN (?,?) AND kind='region'`, vA, vB).Scan(&totalRegionSearchUnits)).To(Succeed())
	Expect(totalRegionSearchUnits).To(Equal(24))

	// OR1: Verify ScanSearchText obeys effective parent page EOF activation (searchReadUnits) rather than leaking unactivated staged chunks.
	var pageUnitID int64
	Expect(s.db.QueryRowContext(ctx, `SELECT unit_id FROM search_units WHERE version_id=? AND kind='page' AND page_path='arch.html'`, vA).Scan(&pageUnitID)).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		_, _, _, bodyReady, err := tx.ScanSearchText(vA, "arch.html", "reg-alpha-arch-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(bodyReady).To(ContainSubstring("checkpoint reconciliation"))
		return nil
	})).To(Succeed())
	_, execErr := s.db.ExecContext(ctx, `UPDATE search_units SET body_state='pending' WHERE unit_id=?`, pageUnitID)
	Expect(execErr).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		_, _, _, bodyBeforeEOF, sErr := tx.ScanSearchText(vA, "arch.html", "reg-alpha-arch-1")
		Expect(sErr).NotTo(HaveOccurred())
		Expect(bodyBeforeEOF).To(BeEmpty(), "expected unactivated region body before parent page EOF not to leak into ScanSearchText")
		return tx.CompleteSearchPage(pageUnitID)
	})).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		_, _, _, bodyAfterEOF, sErr := tx.ScanSearchText(vA, "arch.html", "reg-alpha-arch-1")
		Expect(sErr).NotTo(HaveOccurred())
		Expect(bodyAfterEOF).To(ContainSubstring("checkpoint reconciliation"))
		return nil
	})).To(Succeed())

	var callSeq, nomCalls int
	driver := &scriptedMaintenanceDriver{
		store: s,
		responder: func(a domain.ProviderAttempt, inv domain.ProviderInvocation, _ []domain.ProviderInput) (string, error) {
			cj, err := s.Job(ctx, a.JobID)
			Expect(err).NotTo(HaveOccurred())
			var scope domain.ProviderScope
			Expect(json.Unmarshal(cj.Scope, &scope)).To(Succeed())
			Expect(scope.Invocation).NotTo(BeNil())

			if nomStr, isNom := defaultNominationEnvelopeJSON(*scope.Invocation); isNom {
				nomCalls++
				var nomPrompt map[string]any
				Expect(json.Unmarshal([]byte(scope.Invocation.Prompt), &nomPrompt)).To(Succeed())
				Expect(nomPrompt["consumer"]).To(Equal("managed-maintenance-nomination"))
				cards, _ := nomPrompt["cards"].([]any)
				Expect(cards).To(HaveLen(24), "expected all 24 structural_ready region units in single <=64 nomination batch")
				return nomStr, nil
			}

			callSeq++
			var promptMap map[string]any
			Expect(json.Unmarshal([]byte(scope.Invocation.Prompt), &promptMap)).To(Succeed())
			Expect(promptMap["focal_excerpt"]).To(BeAssignableToTypeOf(""))
			Expect(promptMap["focal_excerpt"].(string)).To(ContainSubstring("checkpoint reconciliation"))

			cands, _ := promptMap["candidates"].([]any)
			Expect(cands).NotTo(BeEmpty())
			Expect(len(cands)).To(BeNumerically("<=", 6))
			focalVer := scope.Invocation.Maintenance.VersionID
			Expect(focalVer).To(BeElementOf(vA, vB))
			wantCrossVer := vB
			if focalVer == vB {
				wantCrossVer = vA
			}
			firstCand, _ := cands[0].(map[string]any)
			targetVer, _ := firstCand["version_id"].(string)
			targetPage, _ := firstCand["page_path"].(string)
			targetReg, _ := firstCand["region_id"].(string)
			foundCrossVer := false
			for _, rawC := range cands {
				cm, _ := rawC.(map[string]any)
				if cm["version_id"] == wantCrossVer && cm["region_id"] != nil && cm["region_id"] != "" {
					targetVer, _ = cm["version_id"].(string)
					targetPage, _ = cm["page_path"].(string)
					targetReg, _ = cm["region_id"].(string)
					foundCrossVer = true
					break
				}
			}
			Expect(foundCrossVer).To(BeTrue(), "expected content-ranked shortlist with sibling redundancy damping to surface cross-project region candidate")

			relBase := map[string]any{
				"target_version_id": targetVer,
				"target_page_path":  targetPage,
				"target_region_id":  targetReg,
				"relation_type":     "conceptual_analogy",
				"rationale":         "Both regions coordinate bounded retry backoff and checkpoint reconciliation before marking a step complete.",
				"uncertainty":       "none",
			}
			relSuperset := map[string]any{
				"target_version_id": targetVer,
				"target_page_path":  targetPage,
				"target_region_id":  targetReg,
				"relation_type":     "conceptual_analogy",
				"rationale":         "Both regions coordinate bounded retry backoff and checkpoint reconciliation before marking a step complete, and also isolate concurrent snapshot readers from writer mutations using separate transaction handles.",
				"uncertainty":       "none",
			}
			relExactDup := map[string]any{
				"target_version_id": targetVer,
				"target_page_path":  targetPage,
				"target_region_id":  targetReg,
				"relation_type":     "conceptual_analogy",
				"rationale":         "  BOTH regions   coordinate bounded retry backoff and checkpoint reconciliation\n\tbefore marking a step complete.  ",
				"uncertainty":       "none",
			}
			rels := []map[string]any{relBase, relSuperset, relExactDup}
			if callSeq == 2 {
				// Reverse the raw relation proposal order on the second scan to verify order-independent fingerprint stability.
				rels = []map[string]any{relSuperset, relBase, relExactDup}
			}

			out := map[string]any{
				"relations": rels,
				"topics": []map[string]any{
					{
						"topic":       "Reconcile Coordination Protocol",
						"rationale":   "The focal region explains checkpoint reconciliation and coordinating bounded retry backoff.",
						"uncertainty": "none",
					},
					{
						"topic":       "checkpoint-reconciliation-durability",
						"rationale":   "The focal region explains how checkpoint reconciliation and bounded retry backoff preserve state across restarts.",
						"uncertainty": "none",
					},
					{
						"topic":       "retry-quantum-telepathy",
						"rationale":   "Ungrounded hallucinated token must be rejected with an explicit per-proposal decision.",
						"uncertainty": "none",
					},
					{
						"topic":       "unbounded-retry",
						"rationale":   "The focal region explains bounded retry backoff and checkpoint reconciliation.",
						"uncertainty": "none",
					},
					{
						"topic":       "distributed-deadlock-arbitration",
						"rationale":   "Declared in region metadata scope themes, while the focal region body omits deadlock arbitration details.",
						"uncertainty": "none",
					},
				},
				"content_gaps": []map[string]any{
					{
						"kind":        "new_version_proposal",
						"theme":       "checkpoint-reconciliation",
						"rationale":   "The focal region explains checkpoint reconciliation, but shortlisted candidate neighbors omit this theme.",
						"uncertainty": "none",
					},
					{
						"kind":        "new_version_proposal",
						"theme":       "reconcile-coordination-protocol-crash-rollback",
						"rationale":   "This focal section explains the reconcile coordination protocol without explaining crash rollback recovery during partial journal commits.",
						"uncertainty": "none",
					},
					{
						"kind":        "new_version_proposal",
						"theme":       "lease-fencing-failover",
						"rationale":   "Inspected source code confirms this protocol is absent from the entire wiki. This focal section mentions lease fencing without explaining how stale epoch tokens are rejected during failover.",
						"uncertainty": "none",
					},
					{
						"kind":        "new_version_proposal",
						"theme":       "lease-fencing-unsupported-only",
						"rationale":   "Inspected source code confirms this protocol is absent from the entire wiki.",
						"uncertainty": "none",
					},
				},
				"limitations": []map[string]any{
					{
						"code":    "source_unverified",
						"reason":  "The focal section states that stale leases are recovered but leaves the internal guarantee for fencing split-brain lease epochs unspecified.",
						"message": "Without source inspection, the focal section leaves the internal guarantee for fencing split-brain lease epochs unspecified.",
					},
					{
						"code":    "source_unverified",
						"reason":  "Without repository source files, exact journal fsync ordering cannot be verified from explanation text alone.",
						"message": "Abstained from claiming source-level fsync ordering without repository source.",
					},
				},
			}
			raw, _ := json.Marshal(out)
			return string(raw), nil
		},
	}

	wkr := scan.NewWorker(s, f.bytes)
	wkr.Identity = codex.SemanticRequestSHA
	coordinator := work.ProviderCoordinator{
		Store:   s,
		Bytes:   capBytes,
		Driver:  driver,
		Prepare: wkr.PrepareMaintenance,
		Apply:   wkr.ApplyMaintenance,
	}

	stepScanJobToCompletion := func(scanJobID string) {
		for turn := 0; turn < 96; turn++ {
			jCur, e := s.Job(ctx, scanJobID)
			Expect(e).NotTo(HaveOccurred())
			if jCur.State == "completed" || jCur.State == "failed" || jCur.State == "cancelled" || jCur.State == "paused" {
				break
			}
			Expect(wkr.Process(ctx, scanJobID)).To(Succeed())
			var childIDs []string
			rows, qErr := s.db.QueryContext(ctx, `SELECT job_id FROM maintenance_intents WHERE scan_job_id=? ORDER BY job_id`, scanJobID)
			Expect(qErr).NotTo(HaveOccurred())
			for rows.Next() {
				var cid string
				Expect(rows.Scan(&cid)).To(Succeed())
				childIDs = append(childIDs, cid)
			}
			Expect(rows.Err()).NotTo(HaveOccurred())
			Expect(rows.Close()).To(Succeed())
			for _, cid := range childIDs {
				cj, cErr := s.Job(ctx, cid)
				if cErr == nil && (cj.State == "queued" || cj.State == "running" || cj.State == "reconciling") {
					Expect(coordinator.Process(ctx, cid)).To(Succeed())
				}
			}
		}
		jDone, e := s.Job(ctx, scanJobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(jDone.State).To(Equal("completed"))
	}

	multiScanJobID := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		Scope:      "selected_projects",
		ProjectIDs: []string{projAID, projBID},
		Depth:      "deep_semantic",
	})
	stepScanJobToCompletion(multiScanJobID)

	var multiRun domain.ScanRun
	var assessedItem domain.JobItem
	var regionItems []domain.JobItem
	actualRegionTuples := map[string]bool{}
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		multiRun, e = tx.ScanRun(multiScanJobID)
		if e != nil {
			return e
		}
		items, e := tx.JobItems(multiScanJobID, 0, 100)
		if e != nil {
			return e
		}
		Expect(items).NotTo(BeEmpty())
		for _, it := range items {
			if it.UnitKind == "region" {
				regionItems = append(regionItems, it)
				actualRegionTuples[it.VersionID+"\x00"+it.PagePath+"\x00"+it.RegionID] = true
			}
			if strings.Contains(string(it.Detail), `"semantic_state":"assessed"`) {
				assessedItem = it
			}
		}
		return nil
	})).To(Succeed())

	// QR4: Verify that all 24 region items across the 4 pages were enumerated with exact (version_id, page_path, region_id) tuples matching the registered fixture units.
	Expect(regionItems).To(HaveLen(24), "expected 24 UnitKind==region job items across the 2 versions / 4 pages")
	Expect(actualRegionTuples).To(HaveLen(24))
	Expect(actualRegionTuples).To(Equal(expectedRegionTuples), "expected enumerated UnitKind==region items to match the exact 24 registered (version,page,region) fixture tuples")

	// QR5: Freshness semantic.assessed stays 0 and semantic.complete stays false on Task02 scans.
	Expect(multiRun.Report.Semantic.Assessed).To(Equal(0))
	Expect(multiRun.Report.Semantic.Complete).To(BeFalse())
	Expect(assessedItem.ItemID).NotTo(BeEmpty())

	// Verify per-proposal decisions and source0 limitations in item.Detail.
	var itemDetail map[string]any
	Expect(json.Unmarshal(assessedItem.Detail, &itemDetail)).To(Succeed())
	Expect(itemDetail["filtered_relations"]).To(BeEquivalentTo(1), "expected only 1 exact-equivalent duplicate relation to be filtered while base + superset mechanisms on the same pair are both kept")
	Expect(itemDetail["filtered_topics"]).To(BeEquivalentTo(3), "expected ungrounded topic, contradicted qualifier topic (unbounded-retry against bounded body), and declared-theme-absent topic (converted to content gap) to be filtered from topic_assignment while stem and multi-token paraphrase topics are accepted")
	Expect(itemDetail["filtered_gaps"]).To(BeEquivalentTo(2), "expected neighbor-absence gap and unsupported-only gap to be rejected while finer-detail gap, explicitly-withdrawn mixed gap, and converted declared-theme gap are accepted without promoting limitations into gaps")

	decisions, ok := itemDetail["proposal_decisions"].([]any)
	Expect(ok).To(BeTrue(), "expected proposal_decisions array in item.Detail: %s", string(assessedItem.Detail))
	Expect(decisions).To(HaveLen(13))
	limsInDetail, ok := itemDetail["limitations"].([]any)
	Expect(ok).To(BeTrue(), "expected limitations array in item.Detail")
	Expect(limsInDetail).NotTo(BeEmpty())
	Expect(string(assessedItem.Detail)).To(ContainSubstring("leaves the internal guarantee for fencing split-brain lease epochs unspecified"))
	Expect(string(assessedItem.Detail)).NotTo(ContainSubstring("leases-guarantee"), "provider limitations must remain uncertainty/abstention records and never synthesize content_gap proposals")

	// Verify durable findings emitted for the assessed item.
	fPage, err := scanSvc.ListJobFindings(ctx, owner.Token, domain.FindingFilter{JobID: multiScanJobID, Limit: 100})
	Expect(err).NotTo(HaveOccurred())

	var relFindings, topicFindings, gapFindings []domain.Finding
	for _, fd := range fPage.Items {
		// QR4: With sources == 0 (source0), every emitted finding must be qualified as source_unverified (never "none").
		Expect(fd.Uncertainty).To(Equal("source_unverified"))
		switch fd.Kind {
		case "related_explanation":
			relFindings = append(relFindings, fd)
		case "topic_assignment":
			topicFindings = append(topicFindings, fd)
		case "new_version_proposal", "new_document_proposal":
			gapFindings = append(gapFindings, fd)
		}
	}
	// QR2 + EE advice: Both the base mechanism and the superset mechanism on the exact same (source, target, relation_type) pair are preserved without overwrite!
	Expect(relFindings).To(HaveLen(2))
	Expect(relFindings[0].EvidenceFingerprint).NotTo(Equal(relFindings[1].EvidenceFingerprint))
	fpByRationale := map[string]string{}
	for _, rf := range relFindings {
		var prop map[string]any
		Expect(json.Unmarshal(rf.Proposal, &prop)).To(Succeed())
		Expect(prop["mechanism_key"]).To(BeAssignableToTypeOf(""))
		Expect(prop["mechanism_key"].(string)).NotTo(BeEmpty())
		fpByRationale[rf.Rationale] = rf.EvidenceFingerprint
	}
	Expect([]string{relFindings[0].Rationale, relFindings[1].Rationale}).To(ConsistOf(
		Equal("Both regions coordinate bounded retry backoff and checkpoint reconciliation before marking a step complete."),
		ContainSubstring("isolate concurrent snapshot readers"),
	))

	// Preserve and verify the terminal-parent fence (candidates.go:3437): ApplyMaintenance on a completed parent scan is rejected with ErrStaleWorker.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		mIntent, ok, e := tx.MaintenanceIntentByItem(multiScanJobID, assessedItem.ItemID)
		Expect(e).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		atts, e := tx.ProviderAttempts(mIntent.JobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(atts).NotTo(BeEmpty())
		return wkr.ApplyMaintenance(tx, atts[len(atts)-1])
	})).To(MatchError(domain.ErrStaleWorker))

	// Run a second ordinary fake-provider scan on the unchanged versions where driver.responder (callSeq==2) returns the reversed relation proposal order [relSuperset, relBase, relExactDup].
	multiScanJobID2 := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		Scope:      "selected_projects",
		ProjectIDs: []string{projAID, projBID},
		Depth:      "deep_semantic",
	})
	stepScanJobToCompletion(multiScanJobID2)
	Expect(callSeq).To(Equal(2))

	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		mIntent2, ok, e := tx.MaintenanceIntentByItem(multiScanJobID2, assessedItem.ItemID)
		Expect(e).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		var prep2 domain.PreparedMaintenancePayload
		Expect(json.Unmarshal(mIntent2.PreparedOutputJSON, &prep2)).To(Succeed())
		Expect(prep2.Envelope.Relations).To(HaveLen(3))
		// Assert that the second raw relation order actually reversed ([relSuperset, relBase, relExactDup]).
		Expect(prep2.Envelope.Relations[0].Rationale).To(ContainSubstring("isolate concurrent snapshot readers"))
		Expect(prep2.Envelope.Relations[1].Rationale).To(Equal("Both regions coordinate bounded retry backoff and checkpoint reconciliation before marking a step complete."))
		return nil
	})).To(Succeed())

	fPageAfterReorder, err := scanSvc.ListJobFindings(ctx, owner.Token, domain.FindingFilter{JobID: multiScanJobID2, Kind: "related_explanation", Limit: 100})
	Expect(err).NotTo(HaveOccurred())
	Expect(fPageAfterReorder.Items).To(HaveLen(2))
	for _, rf := range fPageAfterReorder.Items {
		Expect(rf.EvidenceFingerprint).To(Equal(fpByRationale[rf.Rationale]), "expected EvidenceFingerprint for %q to remain identical when raw proposal array order is reversed", rf.Rationale)
	}

	// QR3 & OR4: Both normalized stem topic 'reconcile-coordination-protocol' and source-grounded paraphrase 'checkpoint-reconciliation-durability' are accepted; 'retry-quantum-telepathy' is rejected.
	Expect(topicFindings).To(HaveLen(2))
	Expect([]string{string(topicFindings[0].Proposal), string(topicFindings[1].Proposal)}).To(ConsistOf(
		ContainSubstring(`"topic":"reconcile-coordination-protocol"`),
		ContainSubstring(`"topic":"checkpoint-reconciliation-durability"`),
	))

	// QR1 & OR3: Finer missing detail ('reconcile-coordination-protocol-crash-rollback'), explicitly-withdrawn mixed gap ('lease-fencing-failover'), and declared missing scope theme ('distributed-deadlock-arbitration') are persisted, while neighbor-absence and unsupported-only gaps were rejected and provider limitations were preserved as limitations only (never promoted to gaps).
	Expect(gapFindings).To(HaveLen(3))
	var gapProposals, gapRationales []string
	for _, gf := range gapFindings {
		gapProposals = append(gapProposals, string(gf.Proposal))
		gapRationales = append(gapRationales, gf.Rationale)
	}
	Expect(gapProposals).To(ConsistOf(
		ContainSubstring(`"normalized_theme":"reconcile-coordination-protocol-crash-rollback"`),
		ContainSubstring(`"normalized_theme":"lease-fencing-failover"`),
		ContainSubstring(`"normalized_theme":"distributed-deadlock-arbitration"`),
	))
	Expect(gapRationales).To(ConsistOf(
		ContainSubstring("without explaining crash rollback recovery"),
		SatisfyAll(
			ContainSubstring("Unsupported universal-scope or uninspected-source claim withdrawn; retained only for focal scope:"),
			Not(ContainSubstring("entire wiki")),
			Not(ContainSubstring("Inspected source code")),
		),
		ContainSubstring("Declared in region metadata scope themes, while the focal region body omits deadlock arbitration details."),
	))
})

var _ = It("enforces two-stage semantic nomination prephase worker order (B), AuthorizeMaintenance strict Prompt/card guards (A/N1), PV01 malformed-JSON no-repeat hold, N4/PV02 secondary-card purge tombstone and result pin reclamation, and (C) child cancellation error propagation (TASK-008-02)", Label("persistence"), func() {
	ctx := context.Background()
	s, auth, w, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "scan-task02-nomination-owner")
	f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
	capBytes := testCaptureBytes{s: s, bytes: f.bytes}
	scanSvc := scan.New(s, auth, f.bytes, nil)
	projAID := mustUnfiledProject(s)
	projBID := "00000000-0000-4000-8000-000000000981"

	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.RegisterProject(domain.Project{ID: projBID, Name: "Project Nom Beta"}); err != nil {
			return err
		}
		cfg, err := tx.Configuration()
		if err != nil {
			return err
		}
		var body map[string]any
		Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
		maint, _ := body["maintenance"].(map[string]any)
		maint["scope"] = "selected_projects"
		maint["project_ids"] = []string{projAID, projBID}
		maint["depth"] = "deep_semantic"
		maint["max_concurrent_ai_jobs"] = 2
		maint["max_provider_attempts_per_run"] = 16
		maint["max_run_seconds"] = 120
		maint["content_generation"] = "propose"
		autoApply, _ := maint["auto_apply"].(map[string]any)
		autoApply["related_explanation"] = "propose"
		autoApply["topic_assignment"] = "propose"

		projects, _ := body["projects"].(map[string]any)
		if projects == nil {
			projects = map[string]any{}
			body["projects"] = projects
		}
		for _, pid := range []string{projAID, projBID} {
			projects[pid] = map[string]any{
				"source_mode_default":    "pinned",
				"provider_id":            "codex",
				"post_work_scan_enabled": true,
				"sources":                []any{},
			}
		}
		raw, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		normalized, err := configuration.Decode(strings.NewReader(string(raw)), "json")
		Expect(err).NotTo(HaveOccurred())
		Expect(configuration.Validate(normalized)).To(Succeed())
		cfg.Value = raw
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())

	// Publish two versions across projAID and projBID with distinct vocabulary so only Stage-1 semantic nomination pairs them.
	// vAlpha has a >2.5KiB (<=8KiB) body so its projected card exercises full-fit-first >2KiB card projection without truncation.
	vAlpha := publishInProjectWithSources(
		f,
		projAID,
		nil,
		"<section id='reg-alpha-nom'><h2>Epoch Lease Fencing</h2><p>"+
			strings.Repeat("Monotonic epoch tokens fence split-brain writers before committing journal entries. ", 32)+
			"</p></section>",
		nil,
		map[string]any{"page_path": "page.html", "id": "reg-alpha-nom", "label": "Epoch Lease Fencing"},
	)
	f.run(vAlpha, "search")
	f.run(vAlpha, "references")

	vBeta := publishInProjectWithSources(
		f,
		projBID,
		nil,
		"<section id='reg-beta-nom'><h2>Quorum Term Barrier</h2><p>Stale leader ballots are rejected by term numbers at storage commit boundaries.</p></section>",
		nil,
		map[string]any{"page_path": "page.html", "id": "reg-beta-nom", "label": "Quorum Term Barrier"},
	)
	f.run(vBeta, "search")
	f.run(vBeta, "references")

	wkr := scan.NewWorker(s, f.bytes)
	wkr.Identity = codex.SemanticRequestSHA

	// 1. (B) Prove actual worker Pass-1 order: step wkr.Process turn-by-turn until the first maintenance_intents row appears,
	// and verify that 100% of structural units are completed AND 100% of semantic leaf units have structural_ready:true (while still inspect/pending)
	// BEFORE the Stage-1 nomination child job is created.
	scanJob1 := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		Scope:      "selected_projects",
		ProjectIDs: []string{projAID, projBID},
		Depth:      "deep_semantic",
	})

	var nomChildID string
	for turn := 0; turn < 32 && nomChildID == ""; turn++ {
		Expect(wkr.Process(ctx, scanJob1)).To(Succeed())
		_ = s.db.QueryRowContext(ctx, `SELECT job_id FROM maintenance_intents WHERE scan_job_id=? ORDER BY job_id LIMIT 1`, scanJob1).Scan(&nomChildID)
	}
	Expect(nomChildID).NotTo(BeEmpty(), "expected Stage-1 nomination child job to be created after Pass-1 structural readiness completes")

	var nomIntent domain.MaintenanceIntent
	var nomJob domain.Job
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var err error
		nomIntent, err = tx.MaintenanceIntent(nomChildID)
		if err != nil {
			return err
		}
		nomJob, err = tx.Job(nomChildID)
		if err != nil {
			return err
		}
		items, err := tx.JobItems(scanJob1, 0, 100)
		if err != nil {
			return err
		}
		Expect(items).To(HaveLen(12))
		readyRegions := 0
		skippedSources := 0
		completedStructural := 0
		for _, it := range items {
			switch it.UnitKind {
			case "region":
				Expect(it.Stage).To(Equal("inspect"))
				Expect(it.State).To(Equal("pending"))
				Expect(string(it.Detail)).To(ContainSubstring(`"structural_ready":true`))
				readyRegions++
			case "source":
				Expect(it.State).To(Equal("skipped"))
				Expect(it.Reason).To(Equal("no_source_declared"))
				Expect(string(it.Detail)).To(ContainSubstring(`"status":"unverified"`))
				skippedSources++
			default:
				Expect(it.State).To(Equal("completed"), "expected structural-only unit %s (%s) to complete before nomination", it.ItemID, it.UnitKind)
				completedStructural++
			}
		}
		Expect(readyRegions).To(Equal(2), "expected both semantic leaf regions to become structural_ready before nomination child creation (B)")
		Expect(skippedSources).To(Equal(2), "expected both source0 project source units to settle as skipped/no_source_declared/unverified")
		Expect(completedStructural).To(Equal(8), "expected all 8 remaining structural units (document, version, page, index) to complete before nomination")
		return nil
	})).To(Succeed())
	Expect(nomIntent.Mode).To(Equal(domain.MaintenanceModeSemanticNomination))
	Expect(nomIntent.BatchID).To(Equal("nomination:p0:b0000"))
	Expect(nomIntent.Cards).To(HaveLen(2))
	alphaIdx, betaIdx := -1, -1
	for i, c := range nomIntent.Cards {
		switch {
		case c.ProjectID == projAID && c.VersionID == vAlpha && c.PagePath == "page.html" && c.RegionID == "reg-alpha-nom":
			alphaIdx = i
		case c.ProjectID == projBID && c.VersionID == vBeta && c.PagePath == "page.html" && c.RegionID == "reg-beta-nom":
			betaIdx = i
		}
	}
	Expect(alphaIdx).To(BeNumerically(">=", 0), "expected Alpha region card in nomIntent.Cards")
	Expect(betaIdx).To(BeNumerically(">=", 0), "expected Beta region card in nomIntent.Cards")
	Expect(alphaIdx).NotTo(Equal(betaIdx))
	Expect(nomIntent.Cards[alphaIdx].ExcerptCoverage).To(Equal("full"))
	Expect(len(nomIntent.Cards[alphaIdx].BodyExcerpt)).To(BeNumerically(">", 2048))
	Expect(nomIntent.Cards[betaIdx].ExcerptCoverage).To(Equal("full"))
	Expect(len(nomIntent.Cards[betaIdx].BodyExcerpt)).To(BeNumerically("<", 900))

	// 2. (A) & N1: Verify AuthorizeMaintenance rejects unknown Prompt root/card fields, card projection mismatches, per-card/whole-prompt raw+encoded bounds, and completed card items.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var validScope domain.ProviderScope
		Expect(json.Unmarshal(nomJob.Scope, &validScope)).To(Succeed())
		Expect(app.AuthorizeMaintenance(tx, nomJob, validScope, "", "prepared")).To(Succeed())

		// (A.1) Unknown root field in Prompt must be rejected even though Cards array is unchanged.
		badRootScope := validScope
		invRoot := *validScope.Invocation
		invRoot.Prompt = strings.TrimSuffix(validScope.Invocation.Prompt, "}") + `,"extra_root":true}`
		badRootScope.Invocation = &invRoot
		Expect(app.AuthorizeMaintenance(tx, nomJob, badRootScope, "", "prepared")).To(MatchError(domain.ErrInvalidTarget))

		// (A.2) Unknown field inside a Prompt card must be rejected even though json.Unmarshal without DisallowUnknownFields would drop it.
		badCardFieldScope := validScope
		invCardField := *validScope.Invocation
		invCardField.Prompt = strings.Replace(validScope.Invocation.Prompt, `"unit_id":"u01"`, `"unit_id":"u01","injected_card_field":"bad"`, 1)
		badCardFieldScope.Invocation = &invCardField
		Expect(app.AuthorizeMaintenance(tx, nomJob, badCardFieldScope, "", "prepared")).To(MatchError(domain.ErrInvalidTarget))

		// (A.3) Projected card field mismatch against stored m.Cards[i] (even with recomputed CardPayloadDigest) must be rejected.
		var tamperedEnv struct {
			Consumer string                            `json:"consumer"`
			Cards    []domain.MaintenanceProjectedCard `json:"cards"`
		}
		Expect(json.Unmarshal([]byte(validScope.Invocation.Prompt), &tamperedEnv)).To(Succeed())
		tamperedEnv.Cards[1].Title = "Tampered Title Not Matching Stored Card"
		tamperedPromptBytes, _ := json.Marshal(tamperedEnv)
		tamperedDigest, dErr := scan.ComputeCardPayloadDigest(tamperedEnv.Cards)
		Expect(dErr).NotTo(HaveOccurred())
		tamperedIntent := nomIntent
		tamperedIntent.CardPayloadDigest = tamperedDigest
		Expect(tx.UpdateMaintenanceIntent(tamperedIntent)).To(Succeed())
		tamperedScope := validScope
		invTampered := *validScope.Invocation
		maintTampered := *validScope.Invocation.Maintenance
		maintTampered.CardPayloadDigest = tamperedDigest
		invTampered.Prompt = string(tamperedPromptBytes)
		invTampered.Maintenance = &maintTampered
		tamperedScope.Invocation = &invTampered
		Expect(app.AuthorizeMaintenance(tx, nomJob, tamperedScope, "", "prepared")).To(MatchError(domain.ErrInvalidTarget))
		// Restore original intent digest.
		Expect(tx.UpdateMaintenanceIntent(nomIntent)).To(Succeed())

		// (A.4) Per-card raw JSON > 8192B must be rejected by AuthorizeMaintenance even when stored m.Cards and CardPayloadDigest match.
		var overRawEnv struct {
			Consumer string                            `json:"consumer"`
			Cards    []domain.MaintenanceProjectedCard `json:"cards"`
		}
		Expect(json.Unmarshal([]byte(validScope.Invocation.Prompt), &overRawEnv)).To(Succeed())
		overRawEnv.Cards[0].BodyExcerpt = strings.Repeat("a", 8200)
		overRawPromptBytes, _ := json.Marshal(overRawEnv)
		overRawDigest, dErr := scan.ComputeCardPayloadDigest(overRawEnv.Cards)
		Expect(dErr).NotTo(HaveOccurred())
		overRawIntent := nomIntent
		overRawCards := append([]domain.MaintenanceNominationCard(nil), nomIntent.Cards...)
		overRawCards[0].BodyExcerpt = overRawEnv.Cards[0].BodyExcerpt
		overRawIntent.Cards = overRawCards
		overRawIntent.CardPayloadDigest = overRawDigest
		Expect(tx.UpdateMaintenanceIntent(overRawIntent)).To(Succeed())
		overRawScope := validScope
		invOverRaw := *validScope.Invocation
		maintOverRaw := *validScope.Invocation.Maintenance
		maintOverRaw.CardPayloadDigest = overRawDigest
		invOverRaw.Prompt = string(overRawPromptBytes)
		invOverRaw.Maintenance = &maintOverRaw
		overRawScope.Invocation = &invOverRaw
		Expect(app.AuthorizeMaintenance(tx, nomJob, overRawScope, "", "prepared")).To(MatchError(domain.ErrInvalidTarget))

		// (A.5) Per-card raw JSON <= 8192B whose JSON-string re-encoded fragment exceeds 8192B (while whole prompt raw+encoded + 8KiB reserve <= 144KiB) must be rejected by AuthorizeMaintenance.
		var overEncCardEnv struct {
			Consumer string                            `json:"consumer"`
			Cards    []domain.MaintenanceProjectedCard `json:"cards"`
		}
		Expect(json.Unmarshal([]byte(validScope.Invocation.Prompt), &overEncCardEnv)).To(Succeed())
		overEncCardEnv.Cards[0].BodyExcerpt = strings.Repeat("quote\"line\n", 540)
		rawCardCheck, _ := json.Marshal(overEncCardEnv.Cards[0])
		Expect(len(rawCardCheck)).To(BeNumerically("<=", 8192))
		encCardCheck, _ := json.Marshal(string(rawCardCheck))
		Expect(len(encCardCheck) - 2).To(BeNumerically(">", 8192))
		overEncCardPromptBytes, _ := json.Marshal(overEncCardEnv)
		Expect(len(overEncCardPromptBytes) + 8192).To(BeNumerically("<=", 144*1024))
		encCardWholePrompt, _ := json.Marshal(string(overEncCardPromptBytes))
		Expect(len(encCardWholePrompt) + 8192).To(BeNumerically("<=", 144*1024))
		overEncCardDigest, dErr := scan.ComputeCardPayloadDigest(overEncCardEnv.Cards)
		Expect(dErr).NotTo(HaveOccurred())
		overEncCardIntent := nomIntent
		overEncCards := append([]domain.MaintenanceNominationCard(nil), nomIntent.Cards...)
		overEncCards[0].BodyExcerpt = overEncCardEnv.Cards[0].BodyExcerpt
		overEncCardIntent.Cards = overEncCards
		overEncCardIntent.CardPayloadDigest = overEncCardDigest
		Expect(tx.UpdateMaintenanceIntent(overEncCardIntent)).To(Succeed())
		overEncCardScope := validScope
		invOverEncCard := *validScope.Invocation
		maintOverEncCard := *validScope.Invocation.Maintenance
		maintOverEncCard.CardPayloadDigest = overEncCardDigest
		invOverEncCard.Prompt = string(overEncCardPromptBytes)
		invOverEncCard.Maintenance = &maintOverEncCard
		overEncCardScope.Invocation = &invOverEncCard
		Expect(app.AuthorizeMaintenance(tx, nomJob, overEncCardScope, "", "prepared")).To(MatchError(domain.ErrInvalidTarget))

		// (A.6) Whole-prompt raw + 8KiB reserve <= 144KiB whose JSON-string re-encoded prompt + 8KiB reserve exceeds 144KiB (with every individual card <=8192B raw+encoded and every job_item/explanation/bijection/authority condition valid) must be rejected by AuthorizeMaintenance.
		buildTwentyCardScope := func(bodyExcerpt string) (domain.MaintenanceIntent, domain.ProviderScope, []byte, [][]byte) {
			var env struct {
				Consumer string                            `json:"consumer"`
				Cards    []domain.MaintenanceProjectedCard `json:"cards"`
			}
			env.Consumer = "managed-maintenance-nomination"
			intentCopy := nomIntent
			intentCopy.Cards = nil
			intentCopy.BatchItemIDs = nil
			invCopy := *validScope.Invocation
			maintCopy := *validScope.Invocation.Maintenance
			maintCopy.BatchItemIDs = nil
			maintCopy.Explanations = nil
			var perCardRaw [][]byte
			for idx := 0; idx < 20; idx++ {
				uid := fmt.Sprintf("u%02d", idx+1)
				baseCard := nomIntent.Cards[idx%2]
				baseExp := validScope.Invocation.Maintenance.Explanations[idx%2]
				itemID := baseCard.ItemID
				regID := baseCard.RegionID
				if idx >= 2 {
					itemID = fmt.Sprintf("item-overenc-%02d", idx+1)
					regID = fmt.Sprintf("reg-overenc-%02d", idx+1)
					Expect(tx.PutJobItem(domain.JobItem{
						JobID:      scanJob1,
						ItemID:     itemID,
						Ordinal:    100 + idx,
						UnitKind:   baseCard.UnitKind,
						ProjectID:  baseCard.ProjectID,
						DocumentID: baseCard.DocumentID,
						VersionID:  baseCard.VersionID,
						PagePath:   baseCard.PagePath,
						RegionID:   regID,
						Stage:      "inspect",
						State:      "pending",
						Detail:     []byte(`{"structural_ready":true}`),
						UpdatedAt:  "2026-11-08T00:00:00Z",
					})).To(Succeed())
				}
				sc := baseCard
				sc.UnitID = uid
				sc.ItemID = itemID
				sc.RegionID = regID
				sc.BodyExcerpt = bodyExcerpt
				intentCopy.Cards = append(intentCopy.Cards, sc)
				intentCopy.BatchItemIDs = append(intentCopy.BatchItemIDs, itemID)
				maintCopy.BatchItemIDs = append(maintCopy.BatchItemIDs, itemID)

				expCopy := baseExp
				expCopy.UnitID = uid
				expCopy.ItemID = itemID
				regCopy := regID
				expCopy.RegionID = &regCopy
				maintCopy.Explanations = append(maintCopy.Explanations, expCopy)

				pCard := domain.MaintenanceProjectedCard{
					UnitID:              sc.UnitID,
					UnitKind:            sc.UnitKind,
					ProjectID:           sc.ProjectID,
					VersionID:           sc.VersionID,
					Title:               sc.Title,
					Headings:            sc.Headings,
					DeclaredTopics:      sc.DeclaredTopics,
					SourcePaths:         sc.SourcePaths,
					BodyExcerpt:         sc.BodyExcerpt,
					ExcerptCoverage:     sc.ExcerptCoverage,
					TotalBodyBytes:      sc.TotalBodyBytes,
					AuthoredRefUnitIDs:  sc.AuthoredRefUnitIDs,
					SharedSourceUnitIDs: sc.SharedSourceUnitIDs,
				}
				rawC, cErr := json.Marshal(pCard)
				Expect(cErr).NotTo(HaveOccurred())
				perCardRaw = append(perCardRaw, rawC)
				env.Cards = append(env.Cards, pCard)
			}
			wholePromptBytes, mErr := json.Marshal(env)
			Expect(mErr).NotTo(HaveOccurred())
			wholeDigest, dErr := scan.ComputeCardPayloadDigest(env.Cards)
			Expect(dErr).NotTo(HaveOccurred())
			intentCopy.CardPayloadDigest = wholeDigest
			maintCopy.CardPayloadDigest = wholeDigest
			invCopy.Prompt = string(wholePromptBytes)
			invCopy.Maintenance = &maintCopy
			scopeCopy := validScope
			scopeCopy.Invocation = &invCopy
			return intentCopy, scopeCopy, wholePromptBytes, perCardRaw
		}

		// First prove that all 20 cards pass every authority, bijection, job_item, coordinate, explanation, and object check when their size fits.
		underIntent, underScope, underWholeBytes, _ := buildTwentyCardScope("short valid body")
		Expect(len(underWholeBytes) + 8192).To(BeNumerically("<=", 144*1024))
		Expect(tx.UpdateMaintenanceIntent(underIntent)).To(Succeed())
		Expect(app.AuthorizeMaintenance(tx, nomJob, underScope, "", "prepared")).To(Succeed())

		// Now keep the exact same 20 valid cards/items/explanations and change only BodyExcerpt so each card is <=8192B raw+encoded and whole prompt raw+8KiB <= 144KiB, while JSON-string re-encoded whole prompt + 8KiB > 144KiB.
		overEncPromptIntent, overEncPromptScope, overEncWholePromptBytes, overEncPerCardRaw := buildTwentyCardScope(strings.Repeat("quote\"line\n", 480))
		for _, rawC := range overEncPerCardRaw {
			Expect(len(rawC)).To(BeNumerically("<=", 8192))
			encC, _ := json.Marshal(string(rawC))
			Expect(len(encC) - 2).To(BeNumerically("<=", 8192))
		}
		Expect(len(overEncWholePromptBytes) + 8192).To(BeNumerically("<=", 144*1024))
		encWholePrompt, _ := json.Marshal(string(overEncWholePromptBytes))
		Expect(len(encWholePrompt) + 8192).To(BeNumerically(">", 144*1024))
		Expect(tx.UpdateMaintenanceIntent(overEncPromptIntent)).To(Succeed())
		Expect(app.AuthorizeMaintenance(tx, nomJob, overEncPromptScope, "", "prepared")).To(MatchError(domain.ErrInvalidTarget))

		// Clean up temporary job_items and restore original intent before continuing.
		_, delErr := tx.(*writeTx).tx.ExecContext(ctx, `DELETE FROM job_items WHERE job_id=? AND item_id LIKE 'item-overenc-%'`, scanJob1)
		Expect(delErr).NotTo(HaveOccurred())
		Expect(tx.UpdateMaintenanceIntent(nomIntent)).To(Succeed())

		// (N1) If any covered card's job_item is already completed when attemptState != "reconciling", AuthorizeMaintenance rejects with ErrStaleWorker.
		card1Item, err := tx.JobItem(scanJob1, nomIntent.Cards[1].ItemID)
		Expect(err).NotTo(HaveOccurred())
		origState := card1Item.State
		card1Item.State = "completed"
		Expect(tx.UpdateJobItem(card1Item)).To(Succeed())
		Expect(app.AuthorizeMaintenance(tx, nomJob, validScope, "", "prepared")).To(MatchError(domain.ErrStaleWorker))
		card1Item.State = origState
		Expect(tx.UpdateJobItem(card1Item)).To(Succeed())
		return nil
	})).To(Succeed())

	// 3. Complete scanJob1 with cross-vocabulary nomination, verifying valid saved-result continuation on the adaptive >2KiB (<=8KiB) full-fit batch without mutating stored scope/cards/digest and without a second Execute, plus semantic_nomination shortlist source and N4/PV02 secondary-card purge tombstone and result pin reclamation.
	origAdaptiveScopeJSON := string(nomJob.Scope)
	origAdaptiveDigest := nomIntent.CardPayloadDigest
	origAdaptiveAlphaExcerpt := nomIntent.Cards[alphaIdx].BodyExcerpt
	var origAdaptiveScope domain.ProviderScope
	Expect(json.Unmarshal(nomJob.Scope, &origAdaptiveScope)).To(Succeed())

	var nomExecuteCalls int
	driver1 := &scriptedMaintenanceDriver{
		store: s,
		responder: func(a domain.ProviderAttempt, inv domain.ProviderInvocation, _ []domain.ProviderInput) (string, error) {
			if nomStr, isNom := defaultNominationEnvelopeJSON(inv); isNom {
				nomExecuteCalls++
				return nomStr, nil
			}
			var promptMap map[string]any
			Expect(json.Unmarshal([]byte(inv.Prompt), &promptMap)).To(Succeed())
			cands, _ := promptMap["candidates"].([]any)
			Expect(cands).To(HaveLen(1))
			cm, _ := cands[0].(map[string]any)
			rawSignals, _ := cm["signals"].([]any)
			var signalKinds []string
			var nomDetails []string
			for _, rs := range rawSignals {
				if sm, ok := rs.(map[string]any); ok {
					if k, ok := sm["kind"].(string); ok {
						signalKinds = append(signalKinds, k)
						if k == "semantic_nomination" {
							if dStr, ok := sm["detail"].(string); ok {
								nomDetails = append(nomDetails, dStr)
							}
						}
					}
				}
			}
			Expect(signalKinds).To(ContainElement("semantic_nomination"))
			Expect(nomDetails).NotTo(BeEmpty())
			for _, dStr := range nomDetails {
				Expect(dStr).To(HavePrefix("unverified_card_routing_hint: "))
			}
			return `{"relations":[],"topics":[],"content_gaps":[],"limitations":[]}`, nil
		},
	}
	failFirstNomPrepare := true
	coord1 := work.ProviderCoordinator{
		Store:  s,
		Bytes:  capBytes,
		Driver: driver1,
		Prepare: func(ctx context.Context, a domain.ProviderAttempt) error {
			if a.JobID == nomChildID && failFirstNomPrepare {
				failFirstNomPrepare = false
				return scan.ErrMaintenanceInvalid
			}
			return wkr.PrepareMaintenance(ctx, a)
		},
		Apply: wkr.ApplyMaintenance,
	}

	// First coordinator turn on nomChildID saves the valid nomination output (result_saved) and pauses on simulated first-prepare failure.
	Expect(coord1.Process(ctx, nomChildID)).To(MatchError(scan.ErrMaintenanceInvalid))
	Expect(nomExecuteCalls).To(Equal(1))
	Expect(wkr.Process(ctx, scanJob1)).To(Succeed())
	jScan1Paused, err := s.Job(ctx, scanJob1)
	Expect(err).NotTo(HaveOccurred())
	Expect(jScan1Paused.State).To(Equal("paused"))

	// Authorized owner parent continue on scanJob1 queues parent and rebinds/queues the saved adaptive >2KiB nomination result without mutating stored cards/prompt/digest/requestSHA.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		resumedParent, rErr := tx.ResumeProviderJob(scanJob1, jScan1Paused.AttemptGeneration, "continue")
		if rErr != nil {
			return rErr
		}
		Expect(resumedParent.State).To(Equal("queued"))
		resumedChild, cErr := tx.Job(nomChildID)
		if cErr != nil {
			return cErr
		}
		Expect(resumedChild.State).To(Equal("queued"))
		Expect(string(resumedChild.Scope)).To(Equal(origAdaptiveScopeJSON))
		return nil
	})).To(Succeed())

	for turn := 0; turn < 32; turn++ {
		jCur, err := s.Job(ctx, scanJob1)
		Expect(err).NotTo(HaveOccurred())
		if jCur.State == "completed" || jCur.State == "paused" || jCur.State == "failed" || jCur.State == "cancelled" {
			break
		}
		Expect(wkr.Process(ctx, scanJob1)).To(Succeed())
		var childIDs []string
		rows, qErr := s.db.QueryContext(ctx, `SELECT job_id FROM maintenance_intents WHERE scan_job_id=? ORDER BY job_id`, scanJob1)
		Expect(qErr).NotTo(HaveOccurred())
		for rows.Next() {
			var cid string
			Expect(rows.Scan(&cid)).To(Succeed())
			childIDs = append(childIDs, cid)
		}
		Expect(rows.Close()).To(Succeed())
		for _, cid := range childIDs {
			cj, cErr := s.Job(ctx, cid)
			if cErr == nil && (cj.State == "queued" || cj.State == "running" || cj.State == "reconciling") {
				Expect(coord1.Process(ctx, cid)).To(Succeed())
			}
		}
	}
	jScan1Done, err := s.Job(ctx, scanJob1)
	Expect(err).NotTo(HaveOccurred())
	Expect(jScan1Done.State).To(Equal("completed"))
	Expect(nomExecuteCalls).To(Equal(1), "expected saved-result continuation on >2KiB nomination batch to complete without a second Execute")
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		afterAdaptiveIntent, mErr := tx.MaintenanceIntent(nomChildID)
		if mErr != nil {
			return mErr
		}
		Expect(afterAdaptiveIntent.NominationState).To(Equal("completed"))
		Expect(afterAdaptiveIntent.CardPayloadDigest).To(Equal(origAdaptiveDigest))
		Expect(afterAdaptiveIntent.Cards[alphaIdx].BodyExcerpt).To(Equal(origAdaptiveAlphaExcerpt))
		Expect(afterAdaptiveIntent.Cards[alphaIdx].ExcerptCoverage).To(Equal("full"))
		Expect(afterAdaptiveIntent.Cards[betaIdx].ExcerptCoverage).To(Equal("full"))
		afterAdaptiveJob, jErr := tx.Job(nomChildID)
		if jErr != nil {
			return jErr
		}
		Expect(string(afterAdaptiveJob.Scope)).To(Equal(origAdaptiveScopeJSON))
		atts, aErr := tx.ProviderAttempts(nomChildID)
		if aErr != nil {
			return aErr
		}
		Expect(atts).To(HaveLen(1))
		Expect(atts[0].RequestSHA).To(Equal(origAdaptiveScope.RequestSHA))
		return nil
	})).To(Succeed())

	// 3b. (CM02) Separate isolated fixture verifying genuine historical <=2KiB (900B-truncated) saved-result continuation:
	// install 900B-truncated cards/canonical prompt/digest/requestSHA BEFORE first Prepare/Execute, save valid result with controlled first-prepare failure,
	// perform real owner parent continue and saved prepare/apply completion, and verify unchanged card/prompt/digest/requestSHA with executeCalls == 1.
	{
		legS, legAuth, legW, _ := authFixture(GinkgoT())
		_, legOwner := pairOwner(GinkgoT(), legAuth, "scan-task02-legacy-cont-owner")
		legF := &referenceWorld{s: legS, writer: legW, bytes: &searchBytes{files: map[string]string{}}}
		legCapBytes := testCaptureBytes{s: legS, bytes: legF.bytes}
		legScanSvc := scan.New(legS, legAuth, legF.bytes, nil)
		legProjAID := mustUnfiledProject(legS)
		legProjBID := projBID

		Expect(legS.Write(ctx, func(tx app.WriteTx) error {
			if err := tx.RegisterProject(domain.Project{ID: legProjBID, Name: "Project Nom Beta"}); err != nil {
				return err
			}
			cfg, err := tx.Configuration()
			if err != nil {
				return err
			}
			var body map[string]any
			Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
			maint, _ := body["maintenance"].(map[string]any)
			maint["scope"] = "selected_projects"
			maint["project_ids"] = []string{legProjAID, legProjBID}
			maint["depth"] = "deep_semantic"
			maint["max_concurrent_ai_jobs"] = 2
			maint["max_provider_attempts_per_run"] = 16
			maint["max_run_seconds"] = 120
			maint["content_generation"] = "propose"
			autoApply, _ := maint["auto_apply"].(map[string]any)
			autoApply["related_explanation"] = "propose"
			autoApply["topic_assignment"] = "propose"

			projects, _ := body["projects"].(map[string]any)
			if projects == nil {
				projects = map[string]any{}
				body["projects"] = projects
			}
			for _, pid := range []string{legProjAID, legProjBID} {
				projects[pid] = map[string]any{
					"source_mode_default":    "pinned",
					"provider_id":            "codex",
					"post_work_scan_enabled": true,
					"sources":                []any{},
				}
			}
			raw, err := json.Marshal(body)
			Expect(err).NotTo(HaveOccurred())
			normalized, err := configuration.Decode(strings.NewReader(string(raw)), "json")
			Expect(err).NotTo(HaveOccurred())
			Expect(configuration.Validate(normalized)).To(Succeed())
			cfg.Value = raw
			return tx.SetConfiguration(cfg, cfg.Revision)
		})).To(Succeed())

		vLegAlpha := publishInProjectWithSources(
			legF,
			legProjAID,
			nil,
			"<section id='reg-alpha-nom'><h2>Epoch Lease Fencing</h2><p>"+
				strings.Repeat("Monotonic epoch tokens fence split-brain writers before committing journal entries. ", 32)+
				"</p></section>",
			nil,
			map[string]any{"page_path": "page.html", "id": "reg-alpha-nom", "label": "Epoch Lease Fencing"},
		)
		legF.run(vLegAlpha, "search")
		legF.run(vLegAlpha, "references")

		vLegBeta := publishInProjectWithSources(
			legF,
			legProjBID,
			nil,
			"<section id='reg-beta-nom'><h2>Quorum Term Barrier</h2><p>Stale leader ballots are rejected by term numbers at storage commit boundaries.</p></section>",
			nil,
			map[string]any{"page_path": "page.html", "id": "reg-beta-nom", "label": "Quorum Term Barrier"},
		)
		legF.run(vLegBeta, "search")
		legF.run(vLegBeta, "references")

		legWkr := scan.NewWorker(legS, legF.bytes)
		legWkr.Identity = codex.SemanticRequestSHA

		legScanJob := startScanJob(legScanSvc, legOwner.Token, domain.ScanRequest{
			Scope:      "selected_projects",
			ProjectIDs: []string{legProjAID, legProjBID},
			Depth:      "deep_semantic",
		})
		var legNomChildID string
		for turn := 0; turn < 32 && legNomChildID == ""; turn++ {
			Expect(legWkr.Process(ctx, legScanJob)).To(Succeed())
			_ = legS.db.QueryRowContext(ctx, `SELECT job_id FROM maintenance_intents WHERE scan_job_id=? ORDER BY job_id LIMIT 1`, legScanJob).Scan(&legNomChildID)
		}
		Expect(legNomChildID).NotTo(BeEmpty())

		// Install historical <=2KiB / 900B-truncated cards, canonical prompt, digest, and real codex.SemanticRequestSHA BEFORE first Prepare/Execute.
		var legacyPrompt, legacyDigest, legacyRequestSHA, legacyScopeJSON string
		legAlphaIdx, legBetaIdx := -1, -1
		Expect(legS.Write(ctx, func(tx app.WriteTx) error {
			atts, aErr := tx.ProviderAttempts(legNomChildID)
			Expect(aErr).NotTo(HaveOccurred())
			Expect(atts).To(BeEmpty(), "legacy cards must be installed before first Prepare/Execute")

			legIntent, iErr := tx.MaintenanceIntent(legNomChildID)
			Expect(iErr).NotTo(HaveOccurred())
			legJob, jErr := tx.Job(legNomChildID)
			Expect(jErr).NotTo(HaveOccurred())

			legacyCards := append([]domain.MaintenanceNominationCard(nil), legIntent.Cards...)
			Expect(legacyCards).To(HaveLen(2))
			for i, c := range legacyCards {
				switch {
				case c.ProjectID == legProjAID && c.VersionID == vLegAlpha && c.PagePath == "page.html" && c.RegionID == "reg-alpha-nom":
					legAlphaIdx = i
				case c.ProjectID == legProjBID && c.VersionID == vLegBeta && c.PagePath == "page.html" && c.RegionID == "reg-beta-nom":
					legBetaIdx = i
				}
			}
			Expect(legAlphaIdx).To(BeNumerically(">=", 0), "expected Alpha region card in legacyCards")
			Expect(legBetaIdx).To(BeNumerically(">=", 0), "expected Beta region card in legacyCards")
			Expect(legAlphaIdx).NotTo(Equal(legBetaIdx))
			Expect(len(legacyCards[legAlphaIdx].BodyExcerpt)).To(BeNumerically(">", 2048))
			legacyCards[legAlphaIdx].BodyExcerpt = legacyCards[legAlphaIdx].BodyExcerpt[:900]
			legacyCards[legAlphaIdx].ExcerptCoverage = "truncated"
			legIntent.Cards = legacyCards
			legacyProjected := []domain.MaintenanceProjectedCard{
				{
					UnitID:              legacyCards[0].UnitID,
					UnitKind:            legacyCards[0].UnitKind,
					ProjectID:           legacyCards[0].ProjectID,
					VersionID:           legacyCards[0].VersionID,
					Title:               legacyCards[0].Title,
					Headings:            legacyCards[0].Headings,
					DeclaredTopics:      legacyCards[0].DeclaredTopics,
					SourcePaths:         legacyCards[0].SourcePaths,
					BodyExcerpt:         legacyCards[0].BodyExcerpt,
					ExcerptCoverage:     legacyCards[0].ExcerptCoverage,
					TotalBodyBytes:      legacyCards[0].TotalBodyBytes,
					AuthoredRefUnitIDs:  legacyCards[0].AuthoredRefUnitIDs,
					SharedSourceUnitIDs: legacyCards[0].SharedSourceUnitIDs,
				},
				{
					UnitID:              legacyCards[1].UnitID,
					UnitKind:            legacyCards[1].UnitKind,
					ProjectID:           legacyCards[1].ProjectID,
					VersionID:           legacyCards[1].VersionID,
					Title:               legacyCards[1].Title,
					Headings:            legacyCards[1].Headings,
					DeclaredTopics:      legacyCards[1].DeclaredTopics,
					SourcePaths:         legacyCards[1].SourcePaths,
					BodyExcerpt:         legacyCards[1].BodyExcerpt,
					ExcerptCoverage:     legacyCards[1].ExcerptCoverage,
					TotalBodyBytes:      legacyCards[1].TotalBodyBytes,
					AuthoredRefUnitIDs:  legacyCards[1].AuthoredRefUnitIDs,
					SharedSourceUnitIDs: legacyCards[1].SharedSourceUnitIDs,
				},
			}
			for _, lp := range legacyProjected {
				rawLP, mErr := json.Marshal(lp)
				Expect(mErr).NotTo(HaveOccurred())
				Expect(len(rawLP)).To(BeNumerically("<=", 2048))
			}
			var bErr error
			legacyPrompt, legacyDigest, bErr = scan.BuildNominationBatchPrompt(legacyProjected)
			Expect(bErr).NotTo(HaveOccurred())
			legIntent.CardPayloadDigest = legacyDigest
			Expect(tx.UpdateMaintenanceIntent(legIntent)).To(Succeed())

			var legScope domain.ProviderScope
			Expect(json.Unmarshal(legJob.Scope, &legScope)).To(Succeed())
			invLegacy := *legScope.Invocation
			maintLegacy := *legScope.Invocation.Maintenance
			maintLegacy.CardPayloadDigest = legacyDigest
			invLegacy.Prompt = legacyPrompt
			invLegacy.Maintenance = &maintLegacy
			legScope.Invocation = &invLegacy
			var shaErr error
			legacyRequestSHA, shaErr = codex.SemanticRequestSHA(legScope.PrincipalID, legScope.Target, *legScope.Invocation, nil)
			Expect(shaErr).NotTo(HaveOccurred())
			legScope.RequestSHA = legacyRequestSHA
			rawLegacyScope, mErr := json.Marshal(legScope)
			Expect(mErr).NotTo(HaveOccurred())
			legacyScopeJSON = string(rawLegacyScope)
			_, e := tx.(*writeTx).tx.ExecContext(ctx, `UPDATE jobs SET scope=? WHERE job_id=?`, rawLegacyScope, legNomChildID)
			Expect(e).NotTo(HaveOccurred())
			legJob.Scope = rawLegacyScope
			return app.AuthorizeMaintenance(tx, legJob, legScope, "", "prepared")
		})).To(Succeed())

		var legNomExecCalls int
		legDriver := &scriptedMaintenanceDriver{
			store: legS,
			responder: func(a domain.ProviderAttempt, inv domain.ProviderInvocation, _ []domain.ProviderInput) (string, error) {
				if nomStr, isNom := defaultNominationEnvelopeJSON(inv); isNom {
					legNomExecCalls++
					return nomStr, nil
				}
				return `{"relations":[],"topics":[],"content_gaps":[],"limitations":[]}`, nil
			},
		}
		failFirstLegPrepare := true
		legCoord := work.ProviderCoordinator{
			Store:  legS,
			Bytes:  legCapBytes,
			Driver: legDriver,
			Prepare: func(ctx context.Context, a domain.ProviderAttempt) error {
				if a.JobID == legNomChildID && failFirstLegPrepare {
					failFirstLegPrepare = false
					return scan.ErrMaintenanceInvalid
				}
				return legWkr.PrepareMaintenance(ctx, a)
			},
			Apply: legWkr.ApplyMaintenance,
		}

		Expect(legCoord.Process(ctx, legNomChildID)).To(MatchError(scan.ErrMaintenanceInvalid))
		Expect(legNomExecCalls).To(Equal(1))
		Expect(legWkr.Process(ctx, legScanJob)).To(Succeed())
		jLegPaused, err := legS.Job(ctx, legScanJob)
		Expect(err).NotTo(HaveOccurred())
		Expect(jLegPaused.State).To(Equal("paused"))

		Expect(legS.Write(ctx, func(tx app.WriteTx) error {
			resumedLegParent, rErr := tx.ResumeProviderJob(legScanJob, jLegPaused.AttemptGeneration, "continue")
			if rErr != nil {
				return rErr
			}
			Expect(resumedLegParent.State).To(Equal("queued"))
			resumedLegChild, cErr := tx.Job(legNomChildID)
			if cErr != nil {
				return cErr
			}
			Expect(resumedLegChild.State).To(Equal("queued"))
			Expect(string(resumedLegChild.Scope)).To(Equal(legacyScopeJSON))
			return nil
		})).To(Succeed())

		for turn := 0; turn < 32; turn++ {
			jCur, err := legS.Job(ctx, legScanJob)
			Expect(err).NotTo(HaveOccurred())
			if jCur.State == "completed" || jCur.State == "paused" || jCur.State == "failed" || jCur.State == "cancelled" {
				break
			}
			Expect(legWkr.Process(ctx, legScanJob)).To(Succeed())
			var childIDs []string
			rows, qErr := legS.db.QueryContext(ctx, `SELECT job_id FROM maintenance_intents WHERE scan_job_id=? ORDER BY job_id`, legScanJob)
			Expect(qErr).NotTo(HaveOccurred())
			for rows.Next() {
				var cid string
				Expect(rows.Scan(&cid)).To(Succeed())
				childIDs = append(childIDs, cid)
			}
			Expect(rows.Close()).To(Succeed())
			for _, cid := range childIDs {
				cj, cErr := legS.Job(ctx, cid)
				if cErr == nil && (cj.State == "queued" || cj.State == "running" || cj.State == "reconciling") {
					Expect(legCoord.Process(ctx, cid)).To(Succeed())
				}
			}
		}
		jLegDone, err := legS.Job(ctx, legScanJob)
		Expect(err).NotTo(HaveOccurred())
		Expect(jLegDone.State).To(Equal("completed"))
		Expect(legNomExecCalls).To(Equal(1), "expected historical <=2KiB 900B-truncated nomination batch to complete saved-result continuation with exactly 1 Execute")

		Expect(legS.Write(ctx, func(tx app.WriteTx) error {
			afterLegIntent, mErr := tx.MaintenanceIntent(legNomChildID)
			if mErr != nil {
				return mErr
			}
			Expect(afterLegIntent.NominationState).To(Equal("completed"))
			Expect(afterLegIntent.CardPayloadDigest).To(Equal(legacyDigest))
			Expect(afterLegIntent.Cards[legAlphaIdx].BodyExcerpt).To(HaveLen(900))
			Expect(afterLegIntent.Cards[legAlphaIdx].ExcerptCoverage).To(Equal("truncated"))
			Expect(afterLegIntent.Cards[legBetaIdx].ExcerptCoverage).To(Equal("full"))
			afterLegJob, jErr := tx.Job(legNomChildID)
			if jErr != nil {
				return jErr
			}
			Expect(string(afterLegJob.Scope)).To(Equal(legacyScopeJSON))
			var afterLegScope domain.ProviderScope
			Expect(json.Unmarshal(afterLegJob.Scope, &afterLegScope)).To(Succeed())
			Expect(afterLegScope.Invocation.Prompt).To(Equal(legacyPrompt))
			Expect(afterLegScope.RequestSHA).To(Equal(legacyRequestSHA))
			atts, aErr := tx.ProviderAttempts(legNomChildID)
			if aErr != nil {
				return aErr
			}
			Expect(atts).To(HaveLen(1))
			Expect(atts[0].RequestSHA).To(Equal(legacyRequestSHA))
			return nil
		})).To(Succeed())
	}

	// Record the saved result object hash on nomChildID before purging cards[1]'s document.
	var nomResultHash string
	var secondaryDocID string
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		m, err := tx.MaintenanceIntent(nomChildID)
		if err != nil {
			return err
		}
		Expect(m.Cards).To(HaveLen(2))
		secondaryDocID = m.Cards[1].DocumentID
		atts, err := tx.ProviderAttempts(nomChildID)
		if err != nil {
			return err
		}
		Expect(atts).To(HaveLen(1))
		nomResultHash = atts[0].ResultHash
		Expect(nomResultHash).NotTo(BeEmpty())
		return nil
	})).To(Succeed())

	var pinCountBeforePurge int
	Expect(s.db.QueryRowContext(ctx, `SELECT count(*) FROM object_pins WHERE owner_kind='provider_attempt' AND content_hash=?`, nomResultHash).Scan(&pinCountBeforePurge)).To(Succeed())
	Expect(pinCountBeforePurge).To(Equal(1))

	// Trash and purge secondaryDocID (cards[1].DocumentID) and verify N4 + Postscript (2) decoder-valid minimal tombstone and PV02 result pin reclamation.
	Expect(applyDocumentLifecycle(s, auth, f.bytes, owner.Token, secondaryDocID, "trash").Lifecycle).To(Equal("trash"))
	Expect(applyDocumentLifecycle(s, auth, f.bytes, owner.Token, secondaryDocID, "purge").Lifecycle).To(Equal("purged"))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.(*writeTx).redactPurgedScanRecords(secondaryDocID)
	})).To(Succeed())

	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		tombIntent, err := tx.MaintenanceIntent(nomChildID)
		Expect(err).NotTo(HaveOccurred(), "minimal purge tombstone must remain decodable by MaintenanceIntent (Postscript 2)")
		Expect(tombIntent.JobID).To(Equal(nomChildID))
		Expect(tombIntent.ScanJobID).To(Equal(scanJob1))
		Expect(tombIntent.ItemID).To(Equal("nomination:p0:b0000"))
		Expect(tombIntent.BatchID).To(Equal("nomination:p0:b0000"))
		Expect(tombIntent.Mode).To(Equal(domain.MaintenanceModeSemanticNomination))
		Expect(tombIntent.Purged).To(BeTrue())
		Expect(tombIntent.NominationState).To(Equal("invalidated"))
		Expect(tombIntent.NominationReason).To(Equal("document_purged"))
		Expect(tombIntent.Cards).To(BeEmpty())
		Expect(tombIntent.PreparedOutputJSON).To(BeEmpty())

		byScan, err := tx.MaintenanceIntentsByScan(scanJob1)
		Expect(err).NotTo(HaveOccurred(), "MaintenanceIntentsByScan must decode purged tombstones cleanly")
		Expect(byScan).NotTo(BeEmpty())

		scrubbedChild, err := tx.Job(nomChildID)
		Expect(err).NotTo(HaveOccurred())
		var scrubbedScope domain.ProviderScope
		Expect(json.Unmarshal(scrubbedChild.Scope, &scrubbedScope)).To(Succeed())
		Expect(scrubbedScope.Invocation.Prompt).To(BeEmpty())
		Expect(scrubbedScope.Invocation.Maintenance.Explanations).To(BeEmpty())
		return nil
	})).To(Succeed())

	var pinCountAfterPurge, candidateCountAfterPurge int
	Expect(s.db.QueryRowContext(ctx, `SELECT count(*) FROM object_pins WHERE owner_kind='provider_attempt' AND content_hash=?`, nomResultHash).Scan(&pinCountAfterPurge)).To(Succeed())
	Expect(pinCountAfterPurge).To(Equal(0), "expected PV02 purge redaction to release provider_attempt result pin on purged nomination child")
	Expect(s.db.QueryRowContext(ctx, `SELECT count(*) FROM purge_candidates WHERE content_hash=?`, nomResultHash).Scan(&candidateCountAfterPurge)).To(Succeed())
	Expect(candidateCountAfterPurge).To(Equal(1), "expected PV02 purge redaction to enqueue unpinned nomination result object in purge_candidates")

	// 4. PV01: Malformed nomination JSON pauses child and parent scan with preparation_failed and never dispatches a second Execute on retry turns.
	vGamma := publishInProjectWithSources(
		f,
		projBID,
		nil,
		"<section id='reg-gamma-nom'><h2>Quorum Replacement</h2><p>Replacement region for PV01 malformed nomination test.</p></section>",
		nil,
		map[string]any{"page_path": "page.html", "id": "reg-gamma-nom", "label": "Quorum Replacement"},
	)
	f.run(vGamma, "search")
	f.run(vGamma, "references")

	for _, pv01Case := range []struct {
		name string
		raw  string
	}{
		{
			name: "missing focal row (only 1 of 2 supplied cards)",
			raw:  `{"nominations":[{"focal_unit_id":"u01","abstained":false,"candidates":[{"candidate_unit_id":"u02","rationale":"only 1 of 2 focal cards"}]}]}`,
		},
		{
			name: "missing required abstained boolean on u02",
			raw:  `{"nominations":[{"focal_unit_id":"u01","abstained":false,"candidates":[{"candidate_unit_id":"u02","rationale":"valid u01"}]},{"focal_unit_id":"u02","candidates":[{"candidate_unit_id":"u01","rationale":"missing abstained on u02"}]}]}`,
		},
		{
			name: "abstained true with missing abstain_reason on u02",
			raw:  `{"nominations":[{"focal_unit_id":"u01","abstained":false,"candidates":[{"candidate_unit_id":"u02","rationale":"valid u01"}]},{"focal_unit_id":"u02","abstained":true,"candidates":[]}]}`,
		},
		{
			name: "abstained true with blank abstain_reason on u02",
			raw:  `{"nominations":[{"focal_unit_id":"u01","abstained":false,"candidates":[{"candidate_unit_id":"u02","rationale":"valid rationale for u01"}]},{"focal_unit_id":"u02","abstained":true,"abstain_reason":"   ","candidates":[]}]}`,
		},
	} {
		caseRaw := pv01Case.raw
		caseS, caseAuth, caseW, _ := authFixture(GinkgoT())
		_, caseOwner := pairOwner(GinkgoT(), caseAuth, "scan-task02-pv01-owner")
		caseF := &referenceWorld{s: caseS, writer: caseW, bytes: &searchBytes{files: map[string]string{}}}
		caseCapBytes := testCaptureBytes{s: caseS, bytes: caseF.bytes}
		caseScanSvc := scan.New(caseS, caseAuth, caseF.bytes, nil)
		caseProjAID := mustUnfiledProject(caseS)
		caseProjBID := projBID

		Expect(caseS.Write(ctx, func(tx app.WriteTx) error {
			if err := tx.RegisterProject(domain.Project{ID: caseProjBID, Name: "Project Nom Beta"}); err != nil {
				return err
			}
			cfg, err := tx.Configuration()
			if err != nil {
				return err
			}
			var body map[string]any
			Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
			maint, _ := body["maintenance"].(map[string]any)
			maint["scope"] = "selected_projects"
			maint["project_ids"] = []string{caseProjAID, caseProjBID}
			maint["depth"] = "deep_semantic"
			maint["max_concurrent_ai_jobs"] = 2
			maint["max_provider_attempts_per_run"] = 16
			maint["max_run_seconds"] = 120
			maint["content_generation"] = "propose"
			autoApply, _ := maint["auto_apply"].(map[string]any)
			autoApply["related_explanation"] = "propose"
			autoApply["topic_assignment"] = "propose"

			projects, _ := body["projects"].(map[string]any)
			if projects == nil {
				projects = map[string]any{}
				body["projects"] = projects
			}
			for _, pid := range []string{caseProjAID, caseProjBID} {
				projects[pid] = map[string]any{
					"source_mode_default":    "pinned",
					"provider_id":            "codex",
					"post_work_scan_enabled": true,
					"sources":                []any{},
				}
			}
			raw, err := json.Marshal(body)
			Expect(err).NotTo(HaveOccurred())
			normalized, err := configuration.Decode(strings.NewReader(string(raw)), "json")
			Expect(err).NotTo(HaveOccurred())
			Expect(configuration.Validate(normalized)).To(Succeed())
			cfg.Value = raw
			return tx.SetConfiguration(cfg, cfg.Revision)
		})).To(Succeed(), "case %s", pv01Case.name)

		vCaseAlpha := publishInProjectWithSources(
			caseF,
			caseProjAID,
			nil,
			"<section id='reg-alpha-nom'><h2>Epoch Lease Fencing</h2><p>Monotonic epoch tokens fence split-brain writers before committing journal entries.</p></section>",
			nil,
			map[string]any{"page_path": "page.html", "id": "reg-alpha-nom", "label": "Epoch Lease Fencing"},
		)
		caseF.run(vCaseAlpha, "search")
		caseF.run(vCaseAlpha, "references")

		vCaseGamma := publishInProjectWithSources(
			caseF,
			caseProjBID,
			nil,
			"<section id='reg-gamma-nom'><h2>Quorum Replacement</h2><p>Replacement region for PV01 malformed nomination test.</p></section>",
			nil,
			map[string]any{"page_path": "page.html", "id": "reg-gamma-nom", "label": "Quorum Replacement"},
		)
		caseF.run(vCaseGamma, "search")
		caseF.run(vCaseGamma, "references")

		caseWkr := scan.NewWorker(caseS, caseF.bytes)
		caseWkr.Identity = codex.SemanticRequestSHA

		badDriver := &scriptedMaintenanceDriver{
			store: caseS,
			responder: func(a domain.ProviderAttempt, inv domain.ProviderInvocation, _ []domain.ProviderInput) (string, error) {
				return caseRaw, nil
			},
		}
		badCoord := work.ProviderCoordinator{
			Store:   caseS,
			Bytes:   caseCapBytes,
			Driver:  badDriver,
			Prepare: caseWkr.PrepareMaintenance,
			Apply:   caseWkr.ApplyMaintenance,
		}

		scanJobPV01 := startScanJob(caseScanSvc, caseOwner.Token, domain.ScanRequest{
			Scope:      "selected_projects",
			ProjectIDs: []string{caseProjAID, caseProjBID},
			Depth:      "deep_semantic",
		})
		var pv01NomChild string
		for turn := 0; turn < 32 && pv01NomChild == ""; turn++ {
			Expect(caseWkr.Process(ctx, scanJobPV01)).To(Succeed())
			_ = caseS.db.QueryRowContext(ctx, `SELECT job_id FROM maintenance_intents WHERE scan_job_id=? ORDER BY job_id LIMIT 1`, scanJobPV01).Scan(&pv01NomChild)
		}
		Expect(pv01NomChild).NotTo(BeEmpty(), "case %s", pv01Case.name)

		Expect(badCoord.Process(ctx, pv01NomChild)).To(MatchError(scan.ErrMaintenanceInvalid), "case %s", pv01Case.name)
		Expect(badDriver.executeCalls).To(Equal(1), "case %s", pv01Case.name)

		jChildPV01, err := caseS.Job(ctx, pv01NomChild)
		Expect(err).NotTo(HaveOccurred())
		Expect(jChildPV01.State).To(Equal("paused"), "case %s", pv01Case.name)
		Expect(jChildPV01.Error).To(Equal("preparation_failed"), "case %s", pv01Case.name)

		// Next parent turn pauses parent scan as preparation_failed without re-dispatching or invalidating the saved-result child.
		Expect(caseWkr.Process(ctx, scanJobPV01)).To(Succeed())
		jParentPV01, err := caseS.Job(ctx, scanJobPV01)
		Expect(err).NotTo(HaveOccurred())
		Expect(jParentPV01.State).To(Equal("paused"), "case %s", pv01Case.name)
		Expect(jParentPV01.Error).To(Equal("preparation_failed"), "case %s", pv01Case.name)

		// Authorized owner parent scan continue queues parent and rebinds/queues the saved invalid child result in one transaction; subsequent coordinator/worker turns fail with ErrMaintenanceInvalid again without dispatching a second Execute.
		Expect(caseS.Write(ctx, func(tx app.WriteTx) error {
			resumedParent, rErr := tx.ResumeProviderJob(scanJobPV01, jParentPV01.AttemptGeneration, "continue")
			if rErr != nil {
				return rErr
			}
			Expect(resumedParent.State).To(Equal("queued"), "case %s", pv01Case.name)
			Expect(resumedParent.Error).To(BeEmpty(), "case %s", pv01Case.name)
			resumedChild, cErr := tx.Job(pv01NomChild)
			if cErr != nil {
				return cErr
			}
			Expect(resumedChild.State).To(Equal("queued"), "case %s", pv01Case.name)
			Expect(resumedChild.Error).To(BeEmpty(), "case %s", pv01Case.name)
			return nil
		})).To(Succeed(), "case %s", pv01Case.name)
		Expect(badCoord.Process(ctx, pv01NomChild)).To(MatchError(scan.ErrMaintenanceInvalid), "case %s", pv01Case.name)
		Expect(caseWkr.Process(ctx, scanJobPV01)).To(Succeed())
		Expect(badDriver.executeCalls).To(Equal(1), "case %s", pv01Case.name)
		jChildAfterContinue, err := caseS.Job(ctx, pv01NomChild)
		Expect(err).NotTo(HaveOccurred())
		Expect(jChildAfterContinue.State).To(Equal("paused"), "case %s", pv01Case.name)
		Expect(jChildAfterContinue.Error).To(Equal("preparation_failed"), "case %s", pv01Case.name)
		jParentAfterContinue, err := caseS.Job(ctx, scanJobPV01)
		Expect(err).NotTo(HaveOccurred())
		Expect(jParentAfterContinue.State).To(Equal("paused"), "case %s", pv01Case.name)
		Expect(jParentAfterContinue.Error).To(Equal("preparation_failed"), "case %s", pv01Case.name)

		// Blocked-child atomic rollback regression: if the child refuses resume during RebindProviderResult -> GuardProviderContinuation -> AuthorizeMaintenance, propagating the error out of Store.Write rolls back parent state/error/revision/events atomically with no new Execute or attempt.
		var card0Item domain.JobItem
		var origCard0State string
		var headSeqBefore int64
		Expect(caseS.Write(ctx, func(tx app.WriteTx) error {
			m, mErr := tx.MaintenanceIntent(pv01NomChild)
			if mErr != nil {
				return mErr
			}
			Expect(m.Cards).To(HaveLen(2), "case %s", pv01Case.name)
			var iErr error
			card0Item, iErr = tx.JobItem(scanJobPV01, m.Cards[0].ItemID)
			if iErr != nil {
				return iErr
			}
			origCard0State = card0Item.State
			card0Item.State = "completed"
			if uErr := tx.UpdateJobItem(card0Item); uErr != nil {
				return uErr
			}
			var hErr error
			_, headSeqBefore, hErr = tx.EventHead()
			return hErr
		})).To(Succeed(), "case %s", pv01Case.name)

		Expect(caseS.Write(ctx, func(tx app.WriteTx) error {
			_, rErr := tx.ResumeProviderJob(scanJobPV01, jParentAfterContinue.AttemptGeneration, "continue")
			return rErr
		})).To(MatchError(domain.ErrStaleWorker), "case %s", pv01Case.name)

		Expect(caseS.Write(ctx, func(tx app.WriteTx) error {
			pRolledBack, pErr := tx.Job(scanJobPV01)
			if pErr != nil {
				return pErr
			}
			Expect(pRolledBack.State).To(Equal("paused"), "case %s", pv01Case.name)
			Expect(pRolledBack.Error).To(Equal("preparation_failed"), "case %s", pv01Case.name)
			Expect(pRolledBack.Revision).To(Equal(jParentAfterContinue.Revision), "case %s", pv01Case.name)

			cRolledBack, cErr := tx.Job(pv01NomChild)
			if cErr != nil {
				return cErr
			}
			Expect(cRolledBack.State).To(Equal("paused"), "case %s", pv01Case.name)
			Expect(cRolledBack.Error).To(Equal("preparation_failed"), "case %s", pv01Case.name)
			Expect(cRolledBack.Revision).To(Equal(jChildAfterContinue.Revision), "case %s", pv01Case.name)

			_, headSeqAfter, hErr := tx.EventHead()
			if hErr != nil {
				return hErr
			}
			Expect(headSeqAfter).To(Equal(headSeqBefore), "case %s: rolled-back parent continue must not leak job.updated events", pv01Case.name)

			atts, aErr := tx.ProviderAttempts(pv01NomChild)
			if aErr != nil {
				return aErr
			}
			Expect(atts).To(HaveLen(1), "case %s", pv01Case.name)

			card0Item.State = origCard0State
			return tx.UpdateJobItem(card0Item)
		})).To(Succeed(), "case %s", pv01Case.name)
		Expect(badDriver.executeCalls).To(Equal(1), "case %s", pv01Case.name)
	}

	// 5. (C) Cancellation error propagation: missing child jobs and ConfirmStop failures in cancelActiveRepairChildren are propagated rather than silently ignored.
	scanJobCancelErr := startScanJob(scanSvc, owner.Token, domain.ScanRequest{
		Scope:      "selected_projects",
		ProjectIDs: []string{projAID, projBID},
		Depth:      "deep_semantic",
	})
	var cancelTestNomChild string
	for turn := 0; turn < 32 && cancelTestNomChild == ""; turn++ {
		Expect(wkr.Process(ctx, scanJobCancelErr)).To(Succeed())
		_ = s.db.QueryRowContext(ctx, `SELECT job_id FROM maintenance_intents WHERE scan_job_id=? ORDER BY job_id LIMIT 1`, scanJobCancelErr).Scan(&cancelTestNomChild)
	}
	Expect(cancelTestNomChild).NotTo(BeEmpty())

	// (C.1) MaintenanceIntentsByScan -> ConfirmStop error propagation when child is in invalid state='running' with cancel_requested=1 during exhaustRunBudget.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		wt := tx.(*writeTx)
		if _, err := wt.tx.ExecContext(ctx, `UPDATE jobs SET state='running',cancel_requested=1 WHERE job_id=?`, cancelTestNomChild); err != nil {
			return err
		}
		_, err := wt.tx.ExecContext(ctx, `UPDATE scan_runs SET elapsed_ms=999999000 WHERE job_id=?`, scanJobCancelErr)
		return err
	})).To(Succeed())
	Expect(wkr.Process(ctx, scanJobCancelErr)).To(MatchError(domain.ErrStaleWorker), "expected ConfirmStop error on maintenance_intents child in cancelActiveRepairChildren to propagate (C)")

	// (C.2) Missing child job ID in job_items.repair_job_id propagates domain.ErrNotFound rather than succeeding.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		wt := tx.(*writeTx)
		if _, err := wt.tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',cancel_requested=1 WHERE job_id=?`, cancelTestNomChild); err != nil {
			return err
		}
		_, err := wt.tx.ExecContext(ctx, `UPDATE job_items SET repair_job_id='missing-repair-child' WHERE job_id=? AND ordinal=1`, scanJobCancelErr)
		return err
	})).To(Succeed())
	Expect(wkr.Process(ctx, scanJobCancelErr)).To(MatchError(domain.ErrNotFound), "expected missing child job in cancelActiveRepairChildren to propagate ErrNotFound (C)")
})
