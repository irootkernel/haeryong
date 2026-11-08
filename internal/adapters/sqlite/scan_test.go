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
	appauth "haeryong.local/haeryong/internal/application/auth"
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
})

func mustUnfiledProject(s *Store) string {
	GinkgoHelper()
	h, err := s.Health(ctx)
	Expect(err).NotTo(HaveOccurred())
	return h.Library.UnfiledProjectID
}
