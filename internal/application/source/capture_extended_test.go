package source_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	httpsource "haeryong.local/haeryong/internal/adapters/http/source"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/gitreader"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

func fixtureGit(root string, args ...string) string {
	GinkgoHelper()
	cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = []string{}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(name, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CEILING_DIRECTORIES="+filepath.Dir(root))
	out, e := cmd.CombinedOutput()
	Expect(e).NotTo(HaveOccurred(), "fixture git command failed")
	return strings.TrimSpace(string(out))
}

var _ = It("SCAP05 actual Git current resolves once to full immutable revision while pinned and metadata diff remain exact after branch motion/offline", Label("persistence"), func() {
	f, root, id := managedFixture("selected_snapshot")
	fixtureGit(root, "init", "--initial-branch=selected")
	Expect(os.WriteFile(filepath.Join(root, "file"), []byte("old\x00bytes\r\n"), 0600)).To(Succeed())
	fixtureGit(root, "add", "file")
	fixtureGit(root, "commit", "-m", "fixture old")
	old := fixtureGit(root, "rev-parse", "HEAD")
	mapping, _ := json.Marshal(domain.ManagedSourceMapping{SourceID: id, Kind: "git", RootPath: root, RepoID: "fixture", RetentionMode: "selected_snapshot", ProviderIDs: []string{"codex"}})
	f.configure(string(mapping))
	f.core.Reader = gitreader.New()
	request := managedRequest(id, "file")
	request.Selections[0].Revision = "selected"
	first, e := f.core.Capture(context.Background(), f.token, sid(), f.project, request)
	Expect(e).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(root, "file"), []byte("new bytes\n"), 0600)).To(Succeed())
	fixtureGit(root, "add", "file")
	fixtureGit(root, "commit", "-m", "fixture next")
	latest := fixtureGit(root, "rev-parse", "HEAD")
	Expect(latest).NotTo(Equal(old))
	second, e := f.core.Capture(context.Background(), f.token, sid(), f.project, request)
	Expect(e).NotTo(HaveOccurred())
	request.SourceMode = "pinned"
	request.Selections[0].Revision = old
	third, e := f.core.Capture(context.Background(), f.token, sid(), f.project, request)
	Expect(e).NotTo(HaveOccurred())
	view, e := f.core.CaptureView(context.Background(), f.token, captureID(first), "")
	Expect(e).NotTo(HaveOccurred())
	row := view["items"].([]domain.SourceCaptureRead)[0]
	Expect(string(row.SourceJSON)).To(ContainSubstring(old))
	Expect(row.Hash).To(Equal(vsha([]byte("old\x00bytes\r\n"))))
	pinned, e := f.core.CaptureView(context.Background(), f.token, captureID(third), "")
	Expect(e).NotTo(HaveOccurred())
	Expect(pinned["items"].([]domain.SourceCaptureRead)[0].Hash).To(Equal(row.Hash))
	Expect(os.RemoveAll(root)).To(Succeed())
	// Every subsequent operation has only persisted metadata/retained bytes.
	diff, e := f.core.CompareCaptures(context.Background(), f.token, captureID(first), captureID(second), "")
	Expect(e).NotTo(HaveOccurred())
	Expect(diff["changes"]).To(HaveLen(1))
	Expect(diff["semantic_verdict"]).To(BeNil())
	Expect(diff["next_cursor"]).To(BeNil())
	same, e := f.core.CompareCaptures(context.Background(), f.token, captureID(first), captureID(third), "")
	Expect(e).NotTo(HaveOccurred())
	Expect(same["changes"]).To(BeEmpty())
	Expect(f.core.ReadCaptured(context.Background(), f.token, row.ID, func(_ domain.SourceCaptureRead, r io.ReadSeeker) error {
		body, e := io.ReadAll(r)
		Expect(body).To(Equal([]byte("old\x00bytes\r\n")))
		return e
	})).To(Succeed())
	unavailable, e := f.core.Capture(context.Background(), f.token, sid(), f.project, managedRequest(id, "file"))
	Expect(e).NotTo(HaveOccurred())
	v, e := f.core.CaptureView(context.Background(), f.token, captureID(unavailable), "")
	Expect(e).NotTo(HaveOccurred())
	Expect(v["complete"]).To(BeFalse())
	Expect(v["target_set_id"]).To(BeNil())
})

var _ = It("SCAP06 reference-only rereads exact approved bytes without incidental retention and revocation fences the actual consumer", Label("persistence"), func() {
	f, root, id := managedFixture("reference_only")
	body := []byte("arbitrary file bytes\x00\xff")
	Expect(os.WriteFile(filepath.Join(root, "file"), body, 0600)).To(Succeed())
	r, e := f.core.Capture(context.Background(), f.token, sid(), f.project, managedRequest(id, "file"))
	Expect(e).NotTo(HaveOccurred())
	view, e := f.core.CaptureView(context.Background(), f.token, captureID(r), "")
	Expect(e).NotTo(HaveOccurred())
	row := view["items"].([]domain.SourceCaptureRead)[0]
	Expect(row.Availability).To(Equal("unavailable"))
	Expect(row.Reason).To(Equal("not_retained"))
	var object domain.Object
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error { var e error; object, e = tx.Object(row.Hash); return e })).To(Succeed())
	Expect(f.store.Collect(context.Background(), object)).To(Succeed())
	Expect(f.core.ReadCaptured(context.Background(), f.token, row.ID, func(r domain.SourceCaptureRead, stream io.ReadSeeker) error {
		Expect(r.BytesOrigin).To(Equal("approved_source"))
		actual, e := io.ReadAll(stream)
		Expect(actual).To(Equal(body))
		return e
	})).To(Succeed())
	Expect(os.WriteFile(filepath.Join(root, "file"), []byte("different"), 0600)).To(Succeed())
	called := false
	e = f.core.ReadCaptured(context.Background(), f.token, row.ID, func(domain.SourceCaptureRead, io.ReadSeeker) error { called = true; return nil })
	Expect(e).To(MatchError(source.ErrSourceChanged))
	Expect(called).To(BeFalse())
	Expect(os.WriteFile(filepath.Join(root, "file"), body, 0600)).To(Succeed())
	e = f.core.ReadCaptured(context.Background(), f.token, row.ID, func(_ domain.SourceCaptureRead, stream io.ReadSeeker) error {
		f.configure(`{"source_id":"replacement","kind":"files","root_path":"` + root + `","retention_mode":"reference_only","provider_ids":[]}`)
		_, e := io.ReadAll(stream)
		return e
	})
	Expect(e).NotTo(Succeed())
})

var _ = It("SCAP08 scoped capture views/reads/diffs and saved receipts hide foreign project/version and revoked source existence", Label("persistence"), func() {
	f, root, id := managedFixture("selected_snapshot")
	f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt"}`, "", nil)
	mapping, _ := json.Marshal(domain.ManagedSourceMapping{SourceID: id, Kind: "files", RootPath: root, RetentionMode: "selected_snapshot", ProviderIDs: []string{}})
	f.configure(string(mapping))
	Expect(os.WriteFile(filepath.Join(root, "secret"), []byte("private snippet"), 0600)).To(Succeed())
	request := managedRequest(id, "secret")
	request.VersionID = f.version
	scoped := seedScopedSourceReader(GinkgoT(), f)
	key := sid()
	r, e := f.core.Capture(context.Background(), scoped, key, f.project, request)
	Expect(e).NotTo(HaveOccurred())
	view, e := f.core.CaptureView(context.Background(), scoped, captureID(r), "")
	Expect(e).NotTo(HaveOccurred())
	read := view["items"].([]domain.SourceCaptureRead)[0]
	receipt, e := f.auth.Operation(context.Background(), scoped, key)
	Expect(e).NotTo(HaveOccurred())
	Expect(receipt.Response.Status).To(Equal(201))
	foreign := request
	foreign.VersionID = sid()
	_, e = f.core.Capture(context.Background(), scoped, sid(), f.project, foreign)
	Expect(e).To(MatchError(domain.ErrNotFound))
	f.configure(`{"source_id":"other","kind":"files","root_path":"` + root + `","retention_mode":"selected_snapshot","provider_ids":[]}`)
	hidden, e := f.core.CaptureView(context.Background(), scoped, captureID(r), "")
	Expect(e).To(MatchError(domain.ErrNotFound))
	Expect(hidden).To(BeNil())
	called := false
	e = f.core.ReadCaptured(context.Background(), scoped, read.ID, func(domain.SourceCaptureRead, io.ReadSeeker) error { called = true; return nil })
	Expect(e).To(MatchError(domain.ErrNotFound))
	Expect(called).To(BeFalse())
	_, e = f.core.CompareCaptures(context.Background(), scoped, captureID(r), captureID(r), "")
	Expect(e).To(MatchError(domain.ErrNotFound))
	_, e = f.auth.Operation(context.Background(), scoped, key)
	Expect(e).To(MatchError(domain.ErrNotFound))
})

var _ = It("SCAP09 coherent source-specific backup omits only reference-only temporary bytes while independent snapshot ownership and exact history survive", Label("persistence"), func() {
	for _, independent := range []bool{false, true} {
		f, root, id := managedFixture("reference_only")
		body := []byte("selected temporary input")
		Expect(os.WriteFile(filepath.Join(root, "file"), body, 0600)).To(Succeed())
		job := sid()
		var reads []domain.SourceCaptureRead
		_, e := f.core.CaptureAndAdmit(context.Background(), f.token, sid(), f.project, managedRequest(id, "file"), capturedJobAdmission(f, job, &reads))
		Expect(e).NotTo(HaveOccurred())
		read := reads[0]
		if independent {
			Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
				return tx.Adopt(domain.Reference{Kind: "selected_snapshot", ID: "independent", Hash: read.Hash}, "")
			})).To(Succeed())
		}
		snapshot := filepath.Join(GinkgoT().TempDir(), "copy.sqlite")
		Expect(f.store.SnapshotDatabase(context.Background(), snapshot)).To(Succeed())
		Expect(sqlite.SanitizeCopyDatabase(context.Background(), snapshot)).To(Succeed())
		Expect(sqlite.CheckCopyDatabase(context.Background(), snapshot)).To(Succeed())
		page, e := sqlite.CopyObjectPage(context.Background(), snapshot, "")
		Expect(e).NotTo(HaveOccurred())
		if independent {
			Expect(page).To(ContainElement(HaveField("Hash", read.Hash)))
		} else {
			Expect(page).NotTo(ContainElement(HaveField("Hash", read.Hash)))
		}
		// The live job's ownership was not edited by projecting the private copy.
		Expect(f.store.Collect(context.Background(), domain.Object{Hash: read.Hash, Size: read.Size})).To(MatchError(domain.ErrPinned))
	}
})

// The wrapper observes a real Git read boundary; it never substitutes bytes.
type movingCaptureGit struct {
	*gitreader.Reader
	afterFirst func()
	reads      int
}

func (r *movingCaptureGit) ReadBlob(ctx context.Context, root, revision, path string) (io.ReadCloser, error) {
	stream, e := r.Reader.ReadBlob(ctx, root, revision, path)
	if e != nil {
		return nil, e
	}
	r.reads++
	if r.reads == 1 && r.afterFirst != nil {
		r.afterFirst()
	}
	return stream, nil
}

var _ = It("SCAP14 branch movement between actual selected Git reads cannot retarget one capture and historical intent never forces current", Label("persistence"), func() {
	f, root, id := managedFixture("selected_snapshot")
	fixtureGit(root, "init", "--initial-branch=selected")
	for _, name := range []string{"one", "two"} {
		Expect(os.WriteFile(filepath.Join(root, name), []byte("old "+name), 0600)).To(Succeed())
	}
	fixtureGit(root, "add", ".")
	fixtureGit(root, "commit", "-m", "old")
	old := fixtureGit(root, "rev-parse", "HEAD")
	for _, name := range []string{"one", "two"} {
		Expect(os.WriteFile(filepath.Join(root, name), []byte("new "+name), 0600)).To(Succeed())
	}
	fixtureGit(root, "add", ".")
	fixtureGit(root, "commit", "-m", "new")
	newRevision := fixtureGit(root, "rev-parse", "HEAD")
	fixtureGit(root, "update-ref", "refs/heads/selected", old)
	mapping, _ := json.Marshal(domain.ManagedSourceMapping{SourceID: id, Kind: "git", RootPath: root, RepoID: "fixture", RetentionMode: "selected_snapshot", ProviderIDs: []string{}})
	f.configure(string(mapping))
	reader := &movingCaptureGit{Reader: gitreader.New(), afterFirst: func() { fixtureGit(root, "update-ref", "refs/heads/selected", newRevision) }}
	f.core.Reader = reader
	request := managedRequest(id, "one", "two")
	request.Selections[0].Revision = "selected"
	r, e := f.core.Capture(context.Background(), f.token, sid(), f.project, request)
	Expect(e).NotTo(HaveOccurred())
	view, e := f.core.CaptureView(context.Background(), f.token, captureID(r), "")
	Expect(e).NotTo(HaveOccurred())
	Expect(view["complete"]).To(BeTrue())
	Expect(reader.reads).To(Equal(2))
	Expect(fixtureGit(root, "rev-parse", "selected")).To(Equal(newRevision))
	for _, read := range view["items"].([]domain.SourceCaptureRead) {
		Expect(string(read.SourceJSON)).To(ContainSubstring(old))
		var location struct {
			Path string `json:"path"`
		}
		Expect(json.Unmarshal(read.LocationJSON, &location)).To(Succeed())
		Expect(read.Hash).To(Equal(vsha([]byte("old " + location.Path))))
	}
	// With a remembered current default, implicit historical still demands an
	// exact pinned identity. Explicit current remains the higher-priority choice.
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var v map[string]any
		if e = json.Unmarshal(cfg.Value, &v); e != nil {
			return e
		}
		v["projects"].(map[string]any)[f.project].(map[string]any)["source_mode_default"] = "current"
		cfg.Value, _ = json.Marshal(v)
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
	request.SourceMode = ""
	request.SourceIntent = "historical"
	r, e = f.core.Capture(context.Background(), f.token, sid(), f.project, request)
	Expect(e).NotTo(HaveOccurred())
	view, e = f.core.CaptureView(context.Background(), f.token, captureID(r), "")
	Expect(e).NotTo(HaveOccurred())
	Expect(view["complete"]).To(BeFalse())
	request.SourceMode = "current"
	r, e = f.core.Capture(context.Background(), f.token, sid(), f.project, request)
	Expect(e).NotTo(HaveOccurred())
	view, e = f.core.CaptureView(context.Background(), f.token, captureID(r), "")
	Expect(e).NotTo(HaveOccurred())
	Expect(view["complete"]).To(BeTrue())
	for _, read := range view["items"].([]domain.SourceCaptureRead) {
		Expect(string(read.SourceJSON)).To(ContainSubstring(newRevision))
	}
})

type policyCaptureFiles struct {
	inner interface {
		Open(context.Context, string, string) (io.ReadCloser, error)
	}
	opens     int
	afterOpen func()
}

func (f *policyCaptureFiles) Open(ctx context.Context, root, path string) (io.ReadCloser, error) {
	r, e := f.inner.Open(ctx, root, path)
	if e == nil {
		f.opens++
		if f.afterOpen != nil {
			f.afterOpen()
		}
	}
	return r, e
}

var _ = It("SCAP17 policy revocation during detached first read prevents later managed reads and preserves the original unconsumed denominator", Label("persistence"), func() {
	f, root, id := managedFixture("reference_only")
	for _, path := range []string{"one", "two"} {
		Expect(os.WriteFile(filepath.Join(root, path), []byte(path), 0600)).To(Succeed())
	}
	files := &policyCaptureFiles{inner: f.core.Files}
	files.afterOpen = func() {
		files.afterOpen = nil
		f.configure(`{"source_id":"replacement","kind":"files","root_path":"` + root + `","retention_mode":"reference_only","provider_ids":[]}`)
	}
	f.core.Files = files
	key := sid()
	response, e := f.core.Capture(context.Background(), f.token, key, f.project, managedRequest(id, "one", "two"))
	Expect(e).NotTo(HaveOccurred())
	Expect(response.Status).To(Equal(412))
	Expect(files.opens).To(Equal(1), "revocation must prevent the next actual source open")
	owner, e := f.auth.Authenticate(context.Background(), f.token)
	Expect(e).NotTo(HaveOccurred())
	receipt, e := f.store.Receipt(context.Background(), domain.OperationKey{PrincipalID: owner.PrincipalID, OperationID: key})
	Expect(e).NotTo(HaveOccurred())
	var ids map[string]string
	Expect(json.Unmarshal(receipt.Normalization, &ids)).To(Succeed())
	var rows []domain.SourceCaptureRead
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		var e error
		rows, e = tx.SourceCaptureReads(ids["capture_id"], "", 64)
		return e
	})).To(Succeed())
	Expect(rows).To(HaveLen(2))
	Expect(rows).To(ContainElement(And(HaveField("Availability", "unavailable"), HaveField("Reason", "not_read"))))
	for _, r := range rows {
		Expect(r.Hash).To(BeEmpty())
	}
})

var _ = It("SCAP18 a later matched posthoc check never reconstructs unobserved attempt inputs or authorizes legacy hash-only managed submission", Label("persistence"), func() {
	f := newVerificationFixture(GinkgoT())
	body := "later exact target bytes\n"
	f.reader.blobs[gitRevision+"\x00f.txt"] = body
	f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt"}`, "", nil)
	mapping := gitMapping(GinkgoT().TempDir())
	mapping = strings.Replace(mapping, `"provider_ids":[]`, `"provider_ids":["codex"]`, 1)
	f.configure(mapping)
	job := sid()
	var reads []domain.SourceCaptureRead
	request := source.CaptureRequest{VersionID: f.version, ProviderID: "codex", SourceMode: "pinned", Selections: []source.CaptureSelection{{SourceID: "git-1", Revision: gitRevision, Paths: []source.CapturePath{{Path: "f.txt"}}}}}
	_, e := f.core.CaptureAndAdmit(context.Background(), f.token, sid(), f.project, request, capturedJobAdmission(f, job, &reads))
	Expect(e).NotTo(HaveOccurred())
	driver := &suppliedCaptureDriver{store: f.store, observed: false}
	p := work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver}
	Expect(p.Process(context.Background(), job)).To(Succeed())
	Expect(driver.attemptIDs).To(HaveLen(1))
	attempt := driver.attemptIDs[0]
	posthocKey := sid()
	response, e := f.verify(posthocKey, `{"expected_bytes_sha256":"`+vsha([]byte(body))+`"}`)
	Expect(e).NotTo(HaveOccurred())
	result := outcome(GinkgoT(), response)
	Expect(result["outcome"]).To(Equal("matched"))
	Expect(result["verified_at"]).NotTo(BeNil())
	actor, e := f.auth.Authenticate(context.Background(), f.token)
	Expect(e).NotTo(HaveOccurred())
	var posthocRead string
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		v, found, e := tx.VerificationByOperation(actor.PrincipalID, posthocKey)
		if e == nil {
			Expect(found).To(BeTrue())
			posthocRead = v.ReadID
		}
		return e
	})).To(Succeed())
	Expect(posthocRead).NotTo(BeEmpty())
	Expect(posthocRead).NotTo(Equal(reads[0].ID))
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		if e := source.ValidateObservedRead(tx, attempt, reads[0].ID); !errors.Is(e, domain.ErrReconciliation) {
			return fmt.Errorf("later check promoted original unobserved input: %v", e)
		}
		if e := source.ValidateObservedRead(tx, attempt, posthocRead); e == nil {
			return errors.New("posthoc read fabricated producing input")
		}
		inputs, e := tx.ProviderInputs(attempt)
		if e == nil {
			Expect(inputs[0].ObservedOrigin).To(BeEmpty())
			Expect(inputs[0].ObservedAt).To(BeNil())
		}
		return e
	})).To(Succeed())
	// Even independently retained identical bytes and a valid posthoc read cannot
	// enter the new managed path without its required source capture binding.
	posthocJob := sid()
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		read, e := tx.CapturedRead(posthocRead)
		if e != nil {
			return e
		}
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		project, e := tx.Scope(f.project)
		if e != nil {
			return e
		}
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		target, _ := json.Marshal(map[string]string{"project_id": f.project})
		scope, _ := json.Marshal(domain.ProviderScope{ProviderID: "codex", PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, TargetSetID: read.TargetSetID, ReadIDs: []string{posthocRead}, RequestSHA: vsha([]byte("explicit distinct legacy-hash request"))})
		return tx.CreateJob(domain.Job{ID: posthocJob, Kind: "question", Scope: scope, ConfigRevision: cfg.Revision, Permissions: map[string]int64{f.project: project.Project.PermissionsRevision}})
	})).To(Succeed())
	Expect(p.Process(context.Background(), posthocJob)).To(MatchError(domain.ErrDenied))
	Expect(driver.calls).To(Equal(1))
	j, e := f.store.Job(context.Background(), posthocJob)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(j.Error).To(Equal("authority_reconciliation_required"))
})

var _ = It("SCAP22 captured read and exact-byte diff inventories paginate without rereading offline sources or losing selected units", Label("persistence"), func() {
	f, root, id := managedFixture("selected_snapshot")
	paths := []string{}
	for n := 0; n < 67; n++ {
		path := fmt.Sprintf("file-%03d", n)
		paths = append(paths, path)
		Expect(os.WriteFile(filepath.Join(root, path), []byte("old "+path), 0600)).To(Succeed())
	}
	first, e := f.core.Capture(context.Background(), f.token, sid(), f.project, managedRequest(id, paths...))
	Expect(e).NotTo(HaveOccurred())
	for _, path := range paths {
		Expect(os.WriteFile(filepath.Join(root, path), []byte("new "+path), 0600)).To(Succeed())
	}
	second, e := f.core.Capture(context.Background(), f.token, sid(), f.project, managedRequest(id, paths...))
	Expect(e).NotTo(HaveOccurred())
	Expect(os.RemoveAll(root)).To(Succeed())
	page, e := f.core.CaptureView(context.Background(), f.token, captureID(first), "")
	Expect(e).NotTo(HaveOccurred())
	Expect(page["items"]).To(HaveLen(64))
	cursor := page["next_cursor"].(string)
	last, e := f.core.CaptureView(context.Background(), f.token, captureID(first), cursor)
	Expect(e).NotTo(HaveOccurred())
	Expect(last["items"]).To(HaveLen(3))
	Expect(last["next_cursor"]).To(BeNil())
	diff, e := f.core.CompareCaptures(context.Background(), f.token, captureID(first), captureID(second), "")
	Expect(e).NotTo(HaveOccurred())
	Expect(diff["changes"]).To(HaveLen(64))
	next := diff["next_cursor"].(string)
	tail, e := f.core.CompareCaptures(context.Background(), f.token, captureID(first), captureID(second), next)
	Expect(e).NotTo(HaveOccurred())
	Expect(tail["changes"]).To(HaveLen(3))
	Expect(tail["next_cursor"]).To(BeNil())
	seen := map[string]bool{}
	for _, p := range []map[string]any{diff, tail} {
		for _, row := range p["changes"].([]domain.SourceCaptureDelta) {
			key := string(row.Location)
			Expect(seen[key]).To(BeFalse())
			seen[key] = true
			Expect(row.Before.Hash).NotTo(Equal(row.After.Hash))
		}
	}
	Expect(seen).To(HaveLen(67))
	_, e = f.core.CompareCaptures(context.Background(), f.token, captureID(second), captureID(first), next)
	Expect(e).To(MatchError(domain.ErrInvalidTarget))
})

type countedCaptureBytes struct {
	source.CaptureBytes
	reads int
}

func (b *countedCaptureBytes) Read(ctx context.Context, o domain.Object, p domain.Pin, consume func(io.ReadSeeker) error) error {
	b.reads++
	return b.CaptureBytes.Read(ctx, o, p, consume)
}

var _ = It("SCAP23 a pinned retained read cannot cross projects under a reused source ID/path and a broader provider disclosure choice", Label("persistence"), func() {
	f, root, id := managedFixture("selected_snapshot")
	Expect(os.WriteFile(filepath.Join(root, "file"), []byte("project A source; never disclosed to codex"), 0600)).To(Succeed())
	mapping := domain.ManagedSourceMapping{SourceID: id, Kind: "files", RootPath: root, RetentionMode: "selected_snapshot", ProviderIDs: []string{}}
	raw, _ := json.Marshal(mapping)
	f.configure(string(raw))
	first, e := f.core.Capture(context.Background(), f.token, sid(), f.project, managedRequest(id, "file"))
	Expect(e).NotTo(HaveOccurred())
	view, e := f.core.CaptureView(context.Background(), f.token, captureID(first), "")
	Expect(e).NotTo(HaveOccurred())
	read := view["items"].([]domain.SourceCaptureRead)[0]
	other := sid()
	otherRoot := GinkgoT().TempDir()
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		if e := tx.CreateProject(domain.Project{ID: other, Name: "Other scope"}); e != nil {
			return e
		}
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var value map[string]any
		if e = json.Unmarshal(cfg.Value, &value); e != nil {
			return e
		}
		value["projects"].(map[string]any)[other] = map[string]any{"sources": []domain.ManagedSourceMapping{{SourceID: id, Kind: "files", RootPath: otherRoot, RetentionMode: "selected_snapshot", ProviderIDs: []string{"codex"}}}}
		cfg.Value, _ = json.Marshal(value)
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
	counter := &countedCaptureBytes{CaptureBytes: f.store}
	f.core.Bytes = counter
	request := managedRequest(id, "file")
	request.SourceMode = "pinned"
	request.ProviderID = "codex"
	request.Selections[0].Paths[0].ReadID = read.ID
	response, e := f.core.Capture(context.Background(), f.token, sid(), other, request)
	Expect(e).NotTo(HaveOccurred())
	Expect(response.Status).To(Equal(403), "A retained read cannot inherit another project's disclosure authority")
	Expect(counter.reads).To(BeZero(), "project mismatch is refused before actual byte verification/consumption")
	version, document, set := sid(), sid(), sid()
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		if e := tx.CreateDocument(domain.Document{ID: document, ProjectID: other, Title: "Scoped version"}); e != nil {
			return e
		}
		if e := tx.CreateContentSet(set, vsha(nil)); e != nil {
			return e
		}
		if e := tx.SealContentSet(set); e != nil {
			return e
		}
		if e := tx.PutVersion(domain.Version{ID: version, DocumentID: document, ContentSetID: set, Title: "Scoped version", ViewerProfile: "html_js_v1", SourceBasis: "unverified", PayloadSHA: vsha(nil), ManifestSHA: vsha(nil)}); e != nil {
			return e
		}
		return tx.PublishVersion(version, 1)
	})).To(Succeed())
	otherFixture := *f
	otherFixture.project = other
	otherFixture.version = version
	scoped := seedScopedSourceReader(GinkgoT(), &otherFixture)
	request.VersionID = version
	handler := httpsource.New(f.core)
	var failures []map[string]any
	var requestIDs []string
	for _, readID := range []string{read.ID, sid()} {
		request.Selections[0].Paths[0].ReadID = readID
		raw, _ := json.Marshal(request)
		key := sid()
		req := httptest.NewRequest("POST", "/api/v1/projects/"+other+"/source-captures", strings.NewReader(string(raw)))
		req.Header.Set("Authorization", "Bearer "+scoped)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(404))
		var body map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		requestID, ok := body["request_id"].(string)
		Expect(ok).To(BeTrue())
		Expect(requestID).NotTo(BeEmpty())
		requestIDs = append(requestIDs, requestID)
		delete(body, "request_id") // Each distinct operation has its own correlation ID.
		failures = append(failures, body)
		Expect(rec.Body.String()).NotTo(ContainSubstring(read.Hash))
		Expect(rec.Body.String()).NotTo(ContainSubstring("snapshot_sha256"))
		actor, e := f.auth.Authenticate(context.Background(), scoped)
		Expect(e).NotTo(HaveOccurred())
		receipt, e := f.store.Receipt(context.Background(), domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key})
		Expect(e).NotTo(HaveOccurred())
		var ids map[string]string
		Expect(json.Unmarshal(receipt.Normalization, &ids)).To(Succeed())
		view, e := f.core.CaptureView(context.Background(), scoped, ids["capture_id"], "")
		Expect(e).NotTo(HaveOccurred())
		Expect(view["complete"]).To(BeFalse())
		Expect(view["target_set_id"]).To(BeNil())
		rows := view["items"].([]domain.SourceCaptureRead)
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Hash).To(BeEmpty())
		Expect(string(rows[0].SourceJSON)).To(Equal("{}"))
	}
	Expect(failures[0]).To(Equal(failures[1]), "foreign and genuinely missing read IDs expose the same retained nondisclosing error")
	Expect(requestIDs[0]).NotTo(Equal(requestIDs[1]))
	Expect(counter.reads).To(BeZero())
})
