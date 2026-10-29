package source_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/configpath"
	"haeryong.local/haeryong/internal/adapters/gitreader"
	sourcefiles "haeryong.local/haeryong/internal/adapters/source"
	config "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

func captureTestConfig(f *vFixture, change func(map[string]any)) {
	GinkgoHelper()
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		c, e := tx.Configuration()
		if e != nil {
			return e
		}
		var v map[string]any
		if e = json.Unmarshal(c.Value, &v); e != nil {
			return e
		}
		change(v)
		c.Value, _ = json.Marshal(v)
		return tx.SetConfiguration(c, c.Revision)
	})).To(Succeed())
}
func captureTestProject(v map[string]any, project string) map[string]any {
	return v["projects"].(map[string]any)[project].(map[string]any)
}
func captureTestDB(f *vFixture) *sql.DB {
	GinkgoHelper()
	db, e := sql.Open("sqlite", filepath.Join(f.dataDir, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(db.Close()).To(Succeed()) })
	return db
}

var _ = It("SCAP32 omitted modes consume remembered defaults while explicit and historical pinned preserve different exact bytes", Label("persistence"), func() {
	f, root, id := managedFixture("selected_snapshot")
	Expect(os.WriteFile(filepath.Join(root, "one"), []byte("historical A"), 0600)).To(Succeed())
	original, e := f.core.Capture(context.Background(), f.token, sid(), f.project, managedRequest(id, "one"))
	Expect(e).NotTo(HaveOccurred())
	view, e := f.core.CaptureView(context.Background(), f.token, captureID(original), "")
	Expect(e).NotTo(HaveOccurred())
	old := view["items"].([]domain.SourceCaptureRead)[0]
	Expect(os.WriteFile(filepath.Join(root, "one"), []byte("current B"), 0600)).To(Succeed())
	for _, choice := range []string{"remembered_current", "absent_default", "explicit_pinned", "historical"} {
		captureTestConfig(f, func(v map[string]any) {
			p := captureTestProject(v, f.project)
			delete(p, "source_mode_default")
			if choice != "absent_default" {
				p["source_mode_default"] = "current"
			}
		})
		request := managedRequest(id, "one")
		request.SourceMode = ""
		request.Selections[0].Paths[0].ReadID = old.ID
		expectedMode, expectedHash := "pinned", old.Hash
		if choice == "remembered_current" {
			expectedMode, expectedHash = "current", vsha([]byte("current B"))
		}
		if choice == "explicit_pinned" {
			request.SourceMode = "pinned"
		}
		if choice == "historical" {
			request.SourceIntent = "historical"
		}
		result, e := f.core.Capture(context.Background(), f.token, sid(), f.project, request)
		Expect(e).NotTo(HaveOccurred(), choice)
		current, e := f.core.CaptureView(context.Background(), f.token, captureID(result), "")
		Expect(e).NotTo(HaveOccurred())
		Expect(current["complete"]).To(BeTrue(), choice)
		selection := current["requested_selection"].(json.RawMessage)
		var resolved source.CaptureRequest
		Expect(json.Unmarshal(selection, &resolved)).To(Succeed())
		Expect(resolved.SourceMode).To(Equal(expectedMode), choice)
		Expect(current["items"].([]domain.SourceCaptureRead)[0].Hash).To(Equal(expectedHash), choice)
	}
})

var _ = It("SCAP33 explicit capture bounds settle terminally without converting a lost caller into cancellation", Label("persistence"), func() {
	f, _, id := managedFixture("reference_only")
	f.core.CheckBound = time.Second
	calls := 0
	f.core.Files = inspectedCaptureFiles{open: func(ctx context.Context, _, _ string) (io.ReadCloser, error) {
		calls++
		_, ok := ctx.Deadline()
		Expect(ok).To(BeTrue())
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	key := sid()
	result, e := f.core.Capture(context.Background(), f.token, key, f.project, managedRequest(id, "one", "two"))
	Expect(e).NotTo(HaveOccurred())
	Expect(result.Status).To(Equal(503))
	var body struct {
		Error struct {
			Code      string            `json:"code"`
			Retryable bool              `json:"retryable"`
			Details   map[string]string `json:"details"`
		} `json:"error"`
	}
	Expect(json.Unmarshal(result.Body, &body)).To(Succeed())
	Expect(body.Error.Code).To(Equal("SERVICE_RECOVERING"))
	Expect(body.Error.Retryable).To(BeFalse())
	Expect(body.Error.Details["reason"]).To(Equal("check_bound_exceeded"))
	Expect(calls).To(Equal(1))
	replay, e := f.core.Capture(context.Background(), f.token, key, f.project, managedRequest(id, "one", "two"))
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(result))
	Expect(calls).To(Equal(1))
})

var _ = It("SCAP34 actual mid-read session revocation settles401 before another selected read and records no supplied observation", Label("persistence"), func() {
	f, _, id := managedFixture("reference_only")
	actor, e := f.auth.Authenticate(context.Background(), f.token)
	Expect(e).NotTo(HaveOccurred())
	calls := 0
	f.core.Files = inspectedCaptureFiles{open: func(context.Context, string, string) (io.ReadCloser, error) {
		calls++
		_, e := f.auth.Revoke(context.Background(), f.token, actor.ID)
		Expect(e).NotTo(HaveOccurred())
		return io.NopCloser(strings.NewReader("synthetic bytes before revoked commit")), nil
	}}
	key := sid()
	response, e := f.core.Capture(context.Background(), f.token, key, f.project, managedRequest(id, "one", "two"))
	Expect(e).NotTo(HaveOccurred())
	Expect(response.Status).To(Equal(401))
	Expect(calls).To(Equal(1))
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	Expect(json.Unmarshal(response.Body, &body)).To(Succeed())
	Expect(body.Error.Code).To(Equal("SESSION_REVOKED"))
	Expect(body.Error.Retryable).To(BeFalse())
	receipt, e := f.store.Receipt(context.Background(), domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key})
	Expect(e).NotTo(HaveOccurred())
	Expect(receipt.Response).NotTo(BeNil())
	Expect(*receipt.Response).To(Equal(response))
	db := captureTestDB(f)
	var count int
	Expect(db.QueryRow(`SELECT count(*) FROM source_capture_reads`).Scan(&count)).To(Succeed())
	Expect(count).To(Equal(2))
	Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='source_capture'`).Scan(&count)).To(Succeed())
	Expect(count).To(BeZero())
	Expect(db.QueryRow(`SELECT count(*) FROM provider_inputs`).Scan(&count)).To(Succeed())
	Expect(count).To(BeZero())
})

var _ = It("SCAP35 typed selected-source failures preserve each original reason without claiming complete capture", Label("persistence"), func() {
	f, _, id := managedFixture("reference_only")
	failures := map[string]error{"invalid": source.ErrInvalidLocation, "changed": source.ErrSourceChanged, "unreadable": os.ErrPermission, "missing": os.ErrNotExist, "protected": source.ErrSourceDenied}
	f.core.Files = inspectedCaptureFiles{open: func(_ context.Context, _, path string) (io.ReadCloser, error) { return nil, failures[path] }}
	response, e := f.core.Capture(context.Background(), f.token, sid(), f.project, managedRequest(id, "invalid", "changed", "unreadable", "missing", "protected"))
	Expect(e).NotTo(HaveOccurred())
	view, e := f.core.CaptureView(context.Background(), f.token, captureID(response), "")
	Expect(e).NotTo(HaveOccurred())
	Expect(view["complete"]).To(BeFalse())
	Expect(view["target_set_id"]).To(BeNil())
	reasons := map[string]string{}
	for _, r := range view["items"].([]domain.SourceCaptureRead) {
		var loc struct {
			Path string `json:"path"`
		}
		Expect(json.Unmarshal(r.LocationJSON, &loc)).To(Succeed())
		reasons[loc.Path] = r.Reason
		Expect(r.Hash).To(BeEmpty())
		Expect(r.BytesOrigin).To(BeEmpty())
	}
	Expect(reasons).To(Equal(map[string]string{"invalid": "location_invalid", "changed": "source_changed", "unreadable": "source_unreadable", "missing": "missing_source", "protected": "source_denied"}))
	// A real resource failure is a terminal infrastructure envelope, not a source fact.
	f.core.Files = inspectedCaptureFiles{open: func(context.Context, string, string) (io.ReadCloser, error) { return nil, syscall.ENOSPC }}
	resource, e := f.core.Capture(context.Background(), f.token, sid(), f.project, managedRequest(id, "resource"))
	Expect(e).NotTo(HaveOccurred())
	Expect(resource.Status).To(Equal(503))
	Expect(string(resource.Body)).To(ContainSubstring("RESOURCE_EXHAUSTED"))
	Expect(string(resource.Body)).To(ContainSubstring(`"retryable":false`))
})

var _ = It("SCAP36 current source policy independently fences managed dispatch including normal revocation and version trash", Label("persistence"), func() {
	for _, change := range []string{"provider", "mapping", "unavailable", "normal_plan", "trashed_version"} {
		f, root, id := managedFixture("reference_only")
		if change == "trashed_version" {
			f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"one"}`, "", nil)
			mapping, _ := json.Marshal(domain.ManagedSourceMapping{SourceID: id, Kind: "files", RootPath: root, RetentionMode: "reference_only", ProviderIDs: []string{"codex"}})
			f.configure(string(mapping))
		}
		Expect(os.WriteFile(filepath.Join(root, "one"), []byte("exact queued bytes"), 0600)).To(Succeed())
		job := sid()
		var reads []domain.SourceCaptureRead
		request := managedRequest(id, "one")
		request.ProviderID = "codex"
		if change == "trashed_version" {
			request.VersionID = f.version
		}
		_, e := f.core.CaptureAndAdmit(context.Background(), f.token, sid(), f.project, request, capturedJobAdmission(f, job, &reads))
		Expect(e).NotTo(HaveOccurred(), change)
		var object domain.Object
		var beforePermission int64
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			var e error
			object, e = tx.Object(reads[0].Hash)
			if e != nil {
				return e
			}
			p, e := tx.Scope(f.project)
			beforePermission = p.Project.PermissionsRevision
			return e
		})).To(Succeed())
		switch change {
		case "provider":
			captureTestConfig(f, func(v map[string]any) {
				captureTestProject(v, f.project)["sources"].([]any)[0].(map[string]any)["provider_ids"] = []string{}
			})
		case "mapping":
			captureTestConfig(f, func(v map[string]any) { captureTestProject(v, f.project)["sources"] = []any{} })
		case "unavailable":
			db := captureTestDB(f)
			_, e = db.Exec(`UPDATE source_capture_reads SET availability='unavailable',reason='reference_only_uncopied' WHERE read_id=?`, reads[0].ID)
			Expect(e).NotTo(HaveOccurred())
		case "normal_plan":
			service := config.New(f.store, f.auth, configpath.Resolver{})
			c, e := f.store.Configuration(context.Background())
			Expect(e).NotTo(HaveOccurred())
			planned, e := service.Plan(context.Background(), f.token, sid(), config.PlanRequest{Scope: config.Scope{Kind: "project", ProjectID: f.project}, ExpectedRevision: c.Revision, Changes: map[string]any{"sources": []any{}}})
			Expect(e).NotTo(HaveOccurred())
			var plan domain.ConfigurationPlan
			Expect(json.Unmarshal(planned.Body, &plan)).To(Succeed())
			_, e = service.Commit(context.Background(), f.token, plan.ID, "", config.CommitRequest{ExpectedRevision: plan.BaseRevision, Confirmed: true})
			Expect(e).NotTo(HaveOccurred())
		case "trashed_version":
			q := query.New(f.store, f.auth, f.store)
			d, e := f.store.Document(context.Background(), f.document)
			Expect(e).NotTo(HaveOccurred())
			raw, _ := json.Marshal(map[string]any{"operation": "trash", "expected_revision": d.Revision})
			planned, e := q.LifecyclePlanCreate(context.Background(), f.token, "", f.document, raw)
			Expect(e).NotTo(HaveOccurred())
			var plan domain.LifecyclePage
			Expect(json.Unmarshal(planned.Body, &plan)).To(Succeed())
			raw, _ = json.Marshal(map[string]any{"plan_id": plan.ID, "expected_revision": plan.ExpectedRevision, "confirmed": true})
			_, e = q.LifecycleCommit(context.Background(), f.token, plan.ID, "", raw)
			Expect(e).NotTo(HaveOccurred())
		}
		if change == "provider" || change == "mapping" || change == "unavailable" {
			Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
				p, e := tx.Scope(f.project)
				Expect(p.Project.PermissionsRevision).To(Equal(beforePermission))
				return e
			})).To(Succeed())
		}
		driver := &suppliedCaptureDriver{store: f.store}
		coordinator := work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver}
		_ = coordinator.Process(context.Background(), job)
		Expect(driver.entries).To(BeZero(), change)
		Expect(driver.calls).To(BeZero(), change)
		current, e := f.store.Job(context.Background(), job)
		Expect(e).NotTo(HaveOccurred())
		Expect([]string{"paused", "cancelled"}).To(ContainElement(current.State), change)
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			attempts, e := tx.ProviderAttempts(job)
			Expect(attempts).To(BeEmpty())
			return e
		})).To(Succeed())
		if current.State == "paused" {
			Expect(f.store.Collect(context.Background(), object)).To(MatchError(domain.ErrPinned), change)
		}
	}
})

var _ = It("SCAP37 managed input admission rejects wrong bindings atomically and requires actual capture or snapshot ownership", Label("persistence"), func() {
	for _, bad := range []string{"released_reference", "target", "principal", "epoch", "project", "provider", "running", "partial_readset", "valid"} {
		retention := "selected_snapshot"
		if bad == "released_reference" {
			retention = "reference_only"
		}
		f, root, id := managedFixture(retention)
		Expect(os.WriteFile(filepath.Join(root, "one"), []byte("admission exact bytes"), 0600)).To(Succeed())
		result, e := f.core.Capture(context.Background(), f.token, sid(), f.project, managedRequest(id, "one"))
		Expect(e).NotTo(HaveOccurred())
		view, e := f.core.CaptureView(context.Background(), f.token, captureID(result), "")
		Expect(e).NotTo(HaveOccurred())
		row := view["items"].([]domain.SourceCaptureRead)[0]
		job := sid()
		e = f.store.Write(context.Background(), func(tx app.WriteTx) error {
			owner, e := tx.AuthState()
			if e != nil {
				return e
			}
			project, e := tx.Scope(f.project)
			if e != nil {
				return e
			}
			c, e := tx.SourceCapture(row.CaptureID)
			if e != nil {
				return e
			}
			target, _ := json.Marshal(map[string]string{"project_id": f.project})
			scope := domain.ProviderScope{ProviderID: "codex", PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, TargetSetID: c.TargetSetID, ReadIDs: []string{row.ID}, RequestSHA: vsha([]byte("same semantic admission fixture"))}
			switch bad {
			case "target":
				scope.TargetSetID = sid()
			case "principal":
				scope.PrincipalID = sid()
			case "epoch":
				scope.AuthEpoch = sid()
			case "project":
				other := sid()
				if e = tx.CreateProject(domain.Project{ID: other, Name: "other"}); e != nil {
					return e
				}
				scope.Target, _ = json.Marshal(map[string]string{"project_id": other})
			case "provider":
				scope.ProviderID = "not-permitted"
			case "partial_readset":
				scope.ReadIDs = append(scope.ReadIDs, sid())
			}
			body, _ := json.Marshal(scope)
			if e = tx.CreateJob(domain.Job{ID: job, Kind: "question", Scope: body, ConfigRevision: c.ConfigRevision, Permissions: map[string]int64{f.project: project.Project.PermissionsRevision}}); e != nil {
				return e
			}
			if bad == "running" {
				if _, e = tx.ClaimJob(job); e != nil {
					return e
				}
			}
			return tx.AttachSourceInputs(job, scope.ReadIDs)
		})
		if bad == "valid" {
			Expect(e).NotTo(HaveOccurred())
			var object domain.Object
			Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error { var e error; object, e = tx.Object(row.Hash); return e })).To(Succeed())
			Expect(f.store.Collect(context.Background(), object)).To(MatchError(domain.ErrPinned))
		} else {
			Expect(e).To(HaveOccurred(), bad)
			_, e = f.store.Job(context.Background(), job)
			Expect(e).To(MatchError(domain.ErrNotFound), bad)
		}
		db := captureTestDB(f)
		var inputs, pins int
		Expect(db.QueryRow(`SELECT count(*) FROM job_inputs WHERE job_id=?`, job).Scan(&inputs)).To(Succeed())
		Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='job_input' AND owner_id=?`, job).Scan(&pins)).To(Succeed())
		if bad == "valid" {
			Expect(inputs).To(Equal(1))
			Expect(pins).To(Equal(1))
		} else {
			Expect(inputs).To(BeZero(), bad)
			Expect(pins).To(BeZero(), bad)
		}
	}
})

type countedCaptureGit struct {
	source.Reader
	blobs int
}

func (r *countedCaptureGit) ReadBlob(ctx context.Context, root, revision, path string) (io.ReadCloser, error) {
	r.blobs++
	return r.Reader.ReadBlob(ctx, root, revision, path)
}
func selectedTreeDigests(root string) map[string]string {
	GinkgoHelper()
	out := map[string]string{}
	Expect(filepath.Walk(root, func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if info.IsDir() {
			if path != root && info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		relative, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, e := os.Readlink(path)
			if e != nil {
				return e
			}
			out[relative] = "symlink:" + target
			return nil
		}
		body, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		out[relative] = vsha(body)
		return nil
	})).To(Succeed())
	return out
}

var _ = It("SCAP38 real managed factory fences capture Git and working-copy stores and exact Git reference rereads never change source", Label("persistence"), func() {
	f, root, id := managedFixture("selected_snapshot")
	GinkgoT().Setenv("HOME", root)
	GinkgoT().Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	GinkgoT().Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	GinkgoT().Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	configDir, e := os.UserConfigDir()
	Expect(e).NotTo(HaveOccurred())
	profile := filepath.Join(root, "profile")
	profilesRoot, e := service.DefaultProfilesRoot()
	Expect(e).NotTo(HaveOccurred())
	profileRelative, e := filepath.Rel(root, profilesRoot)
	Expect(e).NotTo(HaveOccurred())
	Expect(filepath.IsLocal(profileRelative)).To(BeTrue(), "default profile root must remain inside this fixture before any profile write")
	secondProfile := filepath.Join(profilesRoot, "second", "profile.json")
	secondDB := filepath.Join(profilesRoot, "second", "storage", "library.sqlite")
	credential := filepath.Join(configDir, "haeryong", "clients", "fixture", "credential.json")
	for _, path := range []string{filepath.Join(profile, "profile.json"), filepath.Join(root, "codex", "auth.json"), credential, secondProfile, secondDB} {
		Expect(os.MkdirAll(filepath.Dir(path), 0700)).To(Succeed())
		Expect(os.WriteFile(path, []byte("synthetic-known-owned-secret"), 0600)).To(Succeed())
	}
	Expect(os.Link(filepath.Join(profile, "profile.json"), filepath.Join(root, "bootstrap-alias"))).To(Succeed())
	Expect(os.Link(credential, filepath.Join(root, "client-alias"))).To(Succeed())
	Expect(os.Link(filepath.Join(root, "codex", "auth.json"), filepath.Join(root, "codex-alias"))).To(Succeed())
	Expect(os.Symlink(profilesRoot, filepath.Join(root, "profiles-alias"))).To(Succeed())
	Expect(os.WriteFile(filepath.Join(root, "ordinary"), []byte("actual exact Git source"), 0600)).To(Succeed())
	fixtureGit(root, "init", "--initial-branch=selected")
	fixtureGit(root, "add", ".")
	fixtureGit(root, "commit", "-m", "synthetic isolated source fixture")
	before := selectedTreeDigests(root)
	refs := fixtureGit(root, "show-ref")
	status := fixtureGit(root, "--no-optional-locks", "status", "--porcelain")
	coreReader := &countedCaptureGit{Reader: gitreader.New()}
	f.core.Reader = coreReader
	f.core.Files = sourcefiles.ManagedFiles(profile, profilesRoot, credential)
	relative, e := filepath.Rel(root, credential)
	Expect(e).NotTo(HaveOccurred())
	relativeSecond, e := filepath.Rel(root, secondProfile)
	Expect(e).NotTo(HaveOccurred())
	relativeDB, e := filepath.Rel(root, secondDB)
	Expect(e).NotTo(HaveOccurred())
	paths := []string{"ordinary", "profile/profile.json", "codex/auth.json", relative, "bootstrap-alias", "client-alias", "codex-alias", relativeSecond, relativeDB, "profiles-alias/second/profile.json"}
	for _, mode := range []string{"files", "git_working_copy", "git_object"} {
		mapping := domain.ManagedSourceMapping{SourceID: id, Kind: "files", RootPath: root, RetentionMode: "selected_snapshot", ProviderIDs: []string{"codex"}}
		if mode != "files" {
			mapping.Kind = "git"
			mapping.RepoID = "fixture-repository"
		}
		raw, _ := json.Marshal(mapping)
		f.configure(string(raw))
		request := managedRequest(id, paths...)
		request.Selections[0].WorkingCopy = mode == "git_working_copy"
		calls := coreReader.blobs
		result, e := f.core.Capture(context.Background(), f.token, sid(), f.project, request)
		Expect(e).NotTo(HaveOccurred())
		view, e := f.core.CaptureView(context.Background(), f.token, captureID(result), "")
		Expect(e).NotTo(HaveOccurred())
		Expect(view["complete"]).To(BeFalse())
		rows := view["items"].([]domain.SourceCaptureRead)
		Expect(rows).To(HaveLen(len(paths)))
		for _, row := range rows {
			var location struct {
				Path string `json:"path"`
			}
			Expect(json.Unmarshal(row.LocationJSON, &location)).To(Succeed())
			if location.Path == "ordinary" {
				Expect(row.Hash).To(Equal(vsha([]byte("actual exact Git source"))))
			} else {
				Expect(row.Hash).To(BeEmpty())
				Expect(row.CaptureAvailability).To(Equal("denied"))
				Expect(row.Reason).To(Equal("source_denied"))
			}
		}
		if mode == "git_object" {
			Expect(coreReader.blobs-calls).To(Equal(1), "only the ordinary object is read, protected Git paths fail preflight")
		}
	}
	mapping := domain.ManagedSourceMapping{SourceID: id, Kind: "git", RootPath: root, RepoID: "fixture-repository", RetentionMode: "reference_only", ProviderIDs: []string{"codex"}}
	raw, _ := json.Marshal(mapping)
	f.configure(string(raw))
	result, e := f.core.Capture(context.Background(), f.token, sid(), f.project, managedRequest(id, "ordinary"))
	Expect(e).NotTo(HaveOccurred())
	view, e := f.core.CaptureView(context.Background(), f.token, captureID(result), "")
	Expect(e).NotTo(HaveOccurred())
	read := view["items"].([]domain.SourceCaptureRead)[0]
	var object domain.Object
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error { var e error; object, e = tx.Object(read.Hash); return e })).To(Succeed())
	Expect(f.store.Collect(context.Background(), object)).To(Succeed())
	consumed := 0
	Expect(f.core.ReadCaptured(context.Background(), f.token, read.ID, func(row domain.SourceCaptureRead, r io.ReadSeeker) error {
		consumed++
		body, e := io.ReadAll(r)
		Expect(body).To(Equal([]byte("actual exact Git source")))
		Expect(row.BytesOrigin).To(Equal("approved_source"))
		return e
	})).To(Succeed())
	calls := coreReader.blobs
	mapping.RepoID = "different-logical-repository"
	raw, _ = json.Marshal(mapping)
	f.configure(string(raw))
	e = f.core.ReadCaptured(context.Background(), f.token, read.ID, func(domain.SourceCaptureRead, io.ReadSeeker) error { consumed++; return nil })
	Expect(e).To(MatchError(domain.ErrDenied))
	Expect(consumed).To(Equal(1))
	Expect(coreReader.blobs).To(Equal(calls), "matching bytes cannot bypass a different logical repository")
	Expect(selectedTreeDigests(root)).To(Equal(before))
	Expect(fixtureGit(root, "show-ref")).To(Equal(refs))
	Expect(fixtureGit(root, "--no-optional-locks", "status", "--porcelain")).To(Equal(status))
})

var _ = It("SCAP39 actual malformed outside special and unreadable files retain typed reasons and never deliver their bytes", Label("persistence"), func() {
	f, root, id := managedFixture("reference_only")
	outside := filepath.Join(filepath.Dir(root), "outside-"+sid())
	Expect(os.WriteFile(outside, []byte("not selected"), 0600)).To(Succeed())
	DeferCleanup(func() { Expect(os.Remove(outside)).To(Succeed()) })
	Expect(os.Mkdir(filepath.Join(root, "directory"), 0700)).To(Succeed())
	Expect(syscall.Mkfifo(filepath.Join(root, "fifo"), 0600)).To(Succeed())
	Expect(os.Symlink(outside, filepath.Join(root, "escape"))).To(Succeed())
	Expect(os.WriteFile(filepath.Join(root, "unreadable"), []byte("unreadable fixture"), 0000)).To(Succeed())
	DeferCleanup(func() { Expect(os.Chmod(filepath.Join(root, "unreadable"), 0600)).To(Succeed()) })
	Expect(os.WriteFile(filepath.Join(root, "ordinary"), []byte("ordinary positive"), 0600)).To(Succeed())
	paths := []string{"../" + filepath.Base(outside), "directory", "fifo", "escape", "ordinary"}
	if os.Geteuid() != 0 {
		paths = append(paths, "unreadable")
	} else {
		AddReportEntry("permission fixture applicability", "root can read mode000; malformed/outside/special controls remain executed")
	}
	result, e := f.core.Capture(context.Background(), f.token, sid(), f.project, managedRequest(id, paths...))
	Expect(e).NotTo(HaveOccurred())
	view, e := f.core.CaptureView(context.Background(), f.token, captureID(result), "")
	Expect(e).NotTo(HaveOccurred())
	Expect(view["complete"]).To(BeFalse())
	for _, row := range view["items"].([]domain.SourceCaptureRead) {
		var loc struct {
			Path string `json:"path"`
		}
		Expect(json.Unmarshal(row.LocationJSON, &loc)).To(Succeed())
		if loc.Path == "ordinary" {
			Expect(row.Hash).To(Equal(vsha([]byte("ordinary positive"))))
			continue
		}
		Expect(row.Hash).To(BeEmpty())
		if loc.Path == "unreadable" {
			Expect(row.Reason).To(Equal("source_unreadable"))
		} else {
			Expect(row.Reason).To(Equal("location_invalid"))
		}
	}
})
