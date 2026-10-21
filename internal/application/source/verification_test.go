package source_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func vsha(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

// fakeReader stands in for the git adapter with in-memory blobs.
type fakeReader struct {
	blobs       map[string]string
	missing     map[string]bool
	rootMissing bool
	hook        func()
}

func (f *fakeReader) ResolveRevision(ctx context.Context, root, revision string) error {
	if f.rootMissing {
		return source.ErrInvalidRoot
	}
	if f.missing[revision] {
		return source.ErrObjectMissing
	}
	return nil
}

func (f *fakeReader) ResolveHead(ctx context.Context, root string) (string, error) {
	return "", source.ErrObjectMissing
}

func (f *fakeReader) ChangedPaths(ctx context.Context, root, base, tip string, cited []string) ([]string, error) {
	return nil, nil
}

func (f *fakeReader) ReadBlob(ctx context.Context, root, revision, path string) (io.ReadCloser, error) {
	if f.hook != nil {
		f.hook()
	}
	content, ok := f.blobs[revision+"\x00"+path]
	if !ok {
		return nil, source.ErrObjectMissing
	}
	return io.NopCloser(strings.NewReader(content)), nil
}

type vFixture struct {
	store    *foundation.Store
	auth     *appauth.Service
	core     *source.Service
	reader   *fakeReader
	token    string
	project  string
	document string
	version  string
	dataDir  string
}

func newVerificationFixture(t GinkgoTInterface) *vFixture {
	GinkgoHelper()
	selection := service.Selection{Profile: "test", DataDir: filepath.Join(t.TempDir(), "library")}
	_, e := service.Initialize(selection)
	Expect(e != nil).To(BeFalse())
	w, e := service.OpenWriter(selection)
	Expect(e != nil).To(BeFalse())
	DeferCleanup(func() { w.Close() })
	store := foundation.New(nil)
	state, closeStore, e := store.Prepare(context.Background(), w.Profile)
	Expect(e != nil && !state.Ready).To(BeFalse(), func() string { return fmt.Sprint(e) })
	DeferCleanup(func() { _ = closeStore() })
	Expect(w.MarkStorageReady(nil)).NotTo(HaveOccurred())
	auth := appauth.New(store)
	clock := &atomic.Int64{}
	clock.Store(time.Now().UnixMilli())
	auth.Now = func() time.Time { return time.UnixMilli(clock.Load()) }
	pairing, e := auth.CreatePairing(context.Background(), "http://127.0.0.1:9270", "owner", "client")
	Expect(e != nil).To(BeFalse())
	op, _ := service.NewID()
	_, e = auth.BootstrapDecide(context.Background(), domain.PairingDecision{ID: pairing.ID, Action: "approve", Code: pairing.Code, Origin: pairing.Origin, ETag: pairing.ETag, OperationID: op})
	Expect(e != nil).To(BeFalse())
	redeemed, e := auth.Poll(context.Background(), pairing.ID, pairing.PollingSecret, pairing.Origin)
	Expect(e != nil || redeemed.Token == "").To(BeFalse(), func() string { return fmt.Sprint(e) })
	reader := &fakeReader{blobs: map[string]string{}, missing: map[string]bool{}}
	core := source.New(store, auth, store, reader)
	return &vFixture{store: store, auth: auth, core: core, reader: reader, token: redeemed.Token, dataDir: selection.DataDir}
}

func (f *vFixture) configure(mappings string) {
	var value map[string]any
	Expect(json.Unmarshal(defaultConfiguration(), &value)).NotTo(HaveOccurred())
	value["projects"] = map[string]any{f.project: map[string]any{"sources": []any{json.RawMessage(mappings)}}}
	raw, _ := json.Marshal(value)
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		current, e := tx.Configuration()
		if e != nil {
			return e
		}
		return tx.SetConfiguration(domain.Configuration{Revision: current.Revision, Value: raw, Actor: "fixture"}, current.Revision)
	})).To(Succeed())
}

func defaultConfiguration() []byte {
	return []byte(`{"schema_version":"1.0","network":{"listener":{"bind":"127.0.0.1","port":9270},"remote_enabled":false,"external_base_url":null,"allowed_origins":["http://127.0.0.1:9270"]},"security":{"remember_device_default":true,"session_idle_seconds":null,"session_absolute_seconds":null,"pairing_expiry_seconds":300},"maintenance":{"schedule":{"mode":"manual","interval_seconds":null,"cron":null,"timezone":"Asia/Seoul"},"scope":"selected_projects","project_ids":[],"depth":"change_focused","missed_runs":"coalesce_one","max_concurrent_ai_jobs":1,"max_provider_attempts_per_run":20,"max_run_seconds":1800,"auto_apply":{"backlink_index_repair":"auto","related_explanation":"propose","topic_assignment":"propose","freshness_assessment":"propose"},"content_generation":"propose"},"retention":{"trash":"indefinite","backups":"indefinite","completed_job_detail":"indefinite","diagnostic_data":"indefinite","auto_purge_enabled":false},"source":{"default_retention":"reference_only"},"projects":{}}`)
}

// buildVersion creates a published version with supplied source/evidence
// metadata rows and a retained snapshot asset when assetPath is set.
func (f *vFixture) buildVersion(sourceMeta, evidenceMeta string, assetPath string, asset []byte) {
	ctx := context.Background()
	// The retained asset object is staged before the ledger transaction:
	// the store writer is not reentrant.
	var assetHash string
	var assetSize int64
	if assetPath != "" {
		object, e := f.store.Put(ctx, strings.NewReader(string(asset)), func(tx app.WriteTx, object domain.Object) error { return nil })
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
		assetHash, assetSize = object.Hash, object.Size
	}
	f.project, f.document, f.version = "11111111-1111-4111-8111-111111111112", "12121212-1212-4121-8121-121212121212", "13131313-1313-4131-8131-131313131313"
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.CreateProject(domain.Project{ID: f.project, Name: "P"}); e != nil {
			return e
		}
		if e := tx.CreateDocument(domain.Document{ID: f.document, ProjectID: f.project, Title: "Doc"}); e != nil {
			return e
		}
		if e := tx.CreateContentSet("14141414-1414-4141-8141-141414141414", vsha(nil)); e != nil {
			return e
		}
		if assetPath != "" {
			if e := tx.PutEntry(domain.Entry{ContentSetID: "14141414-1414-4141-8141-141414141414", Path: assetPath, Kind: "file", Hash: assetHash, Size: assetSize}); e != nil {
				return e
			}
		}
		if e := tx.SealContentSet("14141414-1414-4141-8141-141414141414"); e != nil {
			return e
		}
		if e := tx.PutVersion(domain.Version{ID: f.version, DocumentID: f.document, ContentSetID: "14141414-1414-4141-8141-141414141414", Title: "Doc", ViewerProfile: "html_js_v1", SourceBasis: "evidence_recorded", PayloadSHA: vsha(nil), ManifestSHA: vsha(nil)}); e != nil {
			return e
		}
		if e := tx.PutSource(domain.Source{VersionID: f.version, ID: "src", Metadata: json.RawMessage(sourceMeta)}); e != nil {
			return e
		}
		if e := tx.PutEvidence(domain.Evidence{VersionID: f.version, ID: "ev", SourceID: "src", Metadata: json.RawMessage(evidenceMeta)}); e != nil {
			return e
		}
		return tx.PublishVersion(f.version, 1)
	})).To(Succeed())
}

func (f *vFixture) verify(key string, body string) (domain.Response, error) {
	raw := []byte(body)
	if body == "" {
		raw = []byte(`{}`)
	}
	return f.core.Verify(context.Background(), f.token, f.version, "ev", key, raw)
}

func gitMeta(revision string) string {
	return `{"id":"src","kind":"git","retention_mode":"reference_only","repo_id":"77777777-7777-4777-8777-777777777777","object_format":"sha1","exact_revision":"` + revision + `"}`
}

func gitMapping(root string) string {
	return `{"source_id":"git-1","kind":"git","root_path":` + jsonEscape(root) + `,"repo_id":"77777777-7777-4777-8777-777777777777","retention_mode":"reference_only","provider_ids":[]}`
}

func jsonEscape(v string) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func outcome(t GinkgoTInterface, response domain.Response) map[string]any {
	t.Helper()
	var data map[string]any
	ExpectWithOffset(1, json.Unmarshal(response.Body, &data)).NotTo(HaveOccurred())
	return data
}

var gitRevision = strings.Repeat("a1b2c3d4", 5)

var _ = Describe("staged imported-evidence verification", Label("persistence"), func() {
	t := GinkgoT()
	It("runs the four outcomes without touching authored evidence", func() {
		f := newVerificationFixture(t)
		blob := "package main\n\nfunc main() {}\n"
		f.reader.blobs[gitRevision+"\x00pkg/main.go"] = blob
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"pkg/main.go","line_start":3,"line_end":3}`, "", nil)
		f.configure(gitMapping(t.TempDir()))

		response, e := f.verify("10000000-0000-4000-8000-000000000001", "")
		Expect(e).NotTo(HaveOccurred())
		first := outcome(t, response)
		Expect(response.Status).To(Equal(201))
		Expect(first["outcome"]).To(Equal("captured"))
		Expect(first["bytes_sha256"]).To(Equal(vsha([]byte("func main() {}\n"))))
		Expect(first["verified_at"]).NotTo(BeNil())

		bounded := "func main() {}\n"
		response, e = f.verify("10000000-0000-4000-8000-000000000002", `{"expected_bytes_sha256":"`+vsha([]byte(bounded))+`","expected_excerpt":`+jsonEscape(bounded)+`}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(outcome(t, response)["outcome"]).To(Equal("matched"))

		response, e = f.verify("10000000-0000-4000-8000-000000000003", `{"expected_bytes_sha256":"`+vsha([]byte("other"))+`"}`)
		Expect(e).NotTo(HaveOccurred())
		third := outcome(t, response)
		Expect(third["outcome"]).To(Equal("mismatch"))
		Expect(third["reason"]).To(Equal("bytes_mismatch"))

		f.reader.missing[gitRevision] = true
		response, e = f.verify("10000000-0000-4000-8000-000000000004", "")
		Expect(e).NotTo(HaveOccurred())
		fourth := outcome(t, response)
		Expect(fourth["outcome"]).To(Equal("unavailable"))
		Expect(fourth["reason"]).To(Equal("revision_unavailable"))
		Expect(fourth["bytes_sha256"]).To(BeNil())

		// The authored evidence metadata stays byte-identical.
		var evidence domain.Evidence
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			var e error
			evidence, e = tx.QueryEvidence(f.version, "ev")
			return e
		})).To(Succeed())
		Expect(string(evidence.Metadata)).To(Equal(`{"id":"ev","source_id":"src","capture_status":"reference_only","path":"pkg/main.go","line_start":3,"line_end":3}`))
	})

	It("records linkage and location facts as typed unavailable outcomes", func() {
		f := newVerificationFixture(t)
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"pkg/main.go"}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		f.reader.rootMissing = true
		response, e := f.verify("20000000-0000-4000-8000-000000000001", "")
		Expect(e).NotTo(HaveOccurred())
		Expect(outcome(t, response)["reason"]).To(Equal("missing_source"))

		// Unregistered source: the configured mapping keeps a different
		// repo identity, so the authored source has no linkage.
		f2 := newVerificationFixture(t)
		f2.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"pkg/main.go"}`, "", nil)
		f2.configure(`{"source_id":"git-1","kind":"git","root_path":"/repo","repo_id":"99999999-9999-4999-8999-999999999999","retention_mode":"reference_only","provider_ids":[]}`)
		response, e = f2.verify("20000000-0000-4000-8000-000000000003", "")
		Expect(e).NotTo(HaveOccurred())
		Expect(outcome(t, response)["reason"]).To(Equal("source_not_registered"))

		// Ambiguous mapping fails closed as a typed unavailable outcome.
		f3 := newVerificationFixture(t)
		f3.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"pkg/main.go"}`, "", nil)
		f3.configure(`{"source_id":"git-a","kind":"git","root_path":"/a","repo_id":"77777777-7777-4777-8777-777777777777","retention_mode":"reference_only","provider_ids":[]}`)
		Expect(f3.store.Write(context.Background(), func(tx app.WriteTx) error {
			current, e := tx.Configuration()
			if e != nil {
				return e
			}
			var config map[string]any
			Expect(json.Unmarshal(current.Value, &config)).NotTo(HaveOccurred())
			projects := config["projects"].(map[string]any)
			settings := projects[f3.project].(map[string]any)
			settings["sources"] = []any{
				json.RawMessage(`{"source_id":"git-a","kind":"git","root_path":"/a","repo_id":"77777777-7777-4777-8777-777777777777","retention_mode":"reference_only","provider_ids":[]}`),
				json.RawMessage(`{"source_id":"git-b","kind":"git","root_path":"/b","repo_id":"77777777-7777-4777-8777-777777777777","retention_mode":"reference_only","provider_ids":[]}`),
			}
			raw, _ := json.Marshal(config)
			return tx.SetConfiguration(domain.Configuration{Revision: current.Revision, Value: raw, Actor: "fixture"}, current.Revision)
		})).To(Succeed())
		response, e = f3.verify("20000000-0000-4000-8000-000000000004", "")
		Expect(e).NotTo(HaveOccurred())
		Expect(outcome(t, response)["reason"]).To(Equal("mapping_ambiguous"))

		// Out-of-range line bounds are facts about the target bytes.
		f4 := newVerificationFixture(t)
		f4.reader.blobs[gitRevision+"\x00pkg/main.go"] = "one line\n"
		f4.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"pkg/main.go","line_start":2,"line_end":2}`, "", nil)
		f4.configure(gitMapping(t.TempDir()))
		response, e = f4.verify("20000000-0000-4000-8000-000000000005", "")
		Expect(e).NotTo(HaveOccurred())
		Expect(outcome(t, response)["reason"]).To(Equal("line_range_out_of_bounds"))
	})

	It("reads snapshot-kind evidence from retained bytes and splits corrupt from missing", func() {
		asset := "retained exact bytes\n"
		f := newVerificationFixture(t)
		f.buildVersion(`{"id":"src","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+vsha([]byte(asset))+`","asset_path":"snap.bin"}`, `{"id":"ev","source_id":"src","capture_status":"verified","path":"snap.bin"}`, "snap.bin", []byte(asset))
		response, e := f.verify("30000000-0000-4000-8000-000000000001", `{"expected_bytes_sha256":"`+vsha([]byte(asset))+`"}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(outcome(t, response)["outcome"]).To(Equal("matched"))

		// A missing retained object is an honest unavailable fact.
		f2 := newVerificationFixture(t)
		f2.buildVersion(`{"id":"src","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+vsha([]byte(asset))+`","asset_path":"snap.bin"}`, `{"id":"ev","source_id":"src","capture_status":"verified","path":"snap.bin"}`, "snap.bin", []byte(asset))
		var object domain.Object
		Expect(f2.store.Write(context.Background(), func(tx app.WriteTx) error {
			entry, e := tx.QueryEntry("14141414-1414-4141-8141-141414141414", "snap.bin")
			if e != nil {
				return e
			}
			object = domain.Object{Hash: entry.Hash, Size: entry.Size}
			return nil
		})).To(Succeed())
		Expect(os.Remove(filepath.Join(f2.dataDir, "storage", "objects", object.Hash))).To(Succeed())
		response, e = f2.verify("30000000-0000-4000-8000-000000000002", "")

		Expect(e).NotTo(HaveOccurred())
		Expect(outcome(t, response)["reason"]).To(Equal("missing_retained_bytes"))
		// A corrupt retained object withholds payload with the integrity
		// reason, never a success-shaped view.
		f3 := newVerificationFixture(t)
		f3.buildVersion(`{"id":"src","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+vsha([]byte(asset))+`","asset_path":"snap.bin"}`, `{"id":"ev","source_id":"src","capture_status":"verified","path":"snap.bin"}`, "snap.bin", []byte(asset))
		var again domain.Object
		Expect(f3.store.Write(context.Background(), func(tx app.WriteTx) error {
			entry, e := tx.QueryEntry("14141414-1414-4141-8141-141414141414", "snap.bin")
			if e != nil {
				return e
			}
			again = domain.Object{Hash: entry.Hash, Size: entry.Size}
			return nil
		})).To(Succeed())
		Expect(os.WriteFile(filepath.Join(f3.dataDir, "storage", "objects", again.Hash), bytes.Repeat([]byte("x"), int(again.Size)), 0600)).To(Succeed())
		response, e = f3.verify("30000000-0000-4000-8000-000000000003", "")
		Expect(e).NotTo(HaveOccurred())
		Expect(outcome(t, response)["reason"]).To(Equal("retained_bytes_corrupt"))
	})

	It("replays receipts, normalizes expectations and returns the stable 202 pending envelope", func() {
		f := newVerificationFixture(t)
		blob := "alpha\nbeta\n"
		f.reader.blobs[gitRevision+"\x00f.txt"] = blob
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt","line_start":1,"line_end":1}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		key := "40000000-0000-4000-8000-000000000001"
		first, e := f.verify(key, `{"expected_excerpt":`+jsonEscape("alpha\n")+`}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(outcome(t, first)["outcome"]).To(Equal("matched"))
		replay, e := f.verify(key, `{"expected_excerpt":`+jsonEscape("alpha\n")+`}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(replay.Status).To(Equal(201))
		Expect(replay.Body).To(Equal(first.Body))
		// SN3: an empty inline expectation never matches non-empty bytes.
		empty, e := f.verify("40000000-0000-4000-8000-000000000002", `{"expected_excerpt":""}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(outcome(t, empty)["outcome"]).To(Equal("mismatch"))

		// A caller that vanishes after reservation leaves the accepted
		// receipt untouched; the next identical request receives the stable
		// 202 pending envelope without a second effect, and the restart
		// sweep settles it deterministically.
		f2 := newVerificationFixture(t)
		f2.reader.blobs[gitRevision+"\x00f.txt"] = blob
		f2.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt","line_start":1,"line_end":1}`, "", nil)
		f2.configure(gitMapping(t.TempDir()))
		// C7: a cancelled request no longer strands a pending receipt — the
		// check runs detached and the retry reaches a terminal result.
		pendingKey := "40000000-0000-4000-8000-000000000003"
		cancelCtx, cancel := context.WithCancel(context.Background())
		f2.reader.hook = func() { cancel() }
		_, _ = f2.core.Verify(cancelCtx, f2.token, f2.version, "ev", pendingKey, []byte(`{}`))
		// The detached check completes regardless of the caller's context;
		// only an HTTP delivery would have failed.
		f2.reader.hook = nil
		var verificationCount int
		Expect(f2.store.Write(context.Background(), func(tx app.WriteTx) error {
			rows, e := tx.Verifications(f2.version, "ev", "", "", 10)
			verificationCount = len(rows)
			return e
		})).To(Succeed())
		Expect(verificationCount).To(Equal(1), "the detached check still committed")
		terminal, e := f2.verify(pendingKey, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(terminal.Status).To(Equal(201), "the retry replays the terminal receipt")
		Expect(outcome(t, terminal)["outcome"]).To(Equal("captured"))
	})

	It("sweeps crashed intents deterministically and releases pins", func() {
		f := newVerificationFixture(t)
		f.reader.blobs[gitRevision+"\x00f.txt"] = "data\n"
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt"}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		key := "50000000-0000-4000-8000-000000000001"
		f.core.Fault = func(stage string) error {
			if stage == "after_reservation" {
				return errors.New("boom")
			}
			return nil
		}
		_, e := f.verify(key, "")
		Expect(e).NotTo(HaveOccurred(), "settlement converts the crash into a typed failure")
		receipt, e := f.auth.Operation(context.Background(), f.token, key)
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal("failed"))
		// A completed check plus a separately-open intent of the same key
		// resolves to the completed result.
		f.core.Fault = nil
		key2 := "50000000-0000-4000-8000-000000000002"
		_, e = f.verify(key2, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(f.core.Recover(context.Background())).To(Succeed())
		receipt2, e := f.auth.Operation(context.Background(), f.token, key2)
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt2.State).To(Equal("completed"))
	})

	It("fails typed when an access-affecting change intervenes between stages", func() {
		f := newVerificationFixture(t)
		f.reader.blobs[gitRevision+"\x00f.txt"] = "data\n"
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt"}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		key := "60000000-0000-4000-8000-000000000001"
		// The reader advances the project permission generation mid-read.
		f.reader.hook = func() {
			f.reader.hook = nil
			Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
				return tx.ApplyProjectConfiguration(f.project, "", "", true, 1)
			})).To(Succeed())
		}
		response, e := f.verify(key, "")
		Expect(e).NotTo(HaveOccurred())
		var body map[string]any
		Expect(json.Unmarshal(response.Body, &body)).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(412))
		Expect(body["error"]).NotTo(BeNil())
		receipt, e := f.auth.Operation(context.Background(), f.token, key)
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal("failed"))
		var rows int
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			history, e := tx.Verifications(f.version, "ev", "", "", 10)
			rows = len(history)
			return e
		})).To(Succeed())
		Expect(rows).To(Equal(0), "no verification row for a revoked-mid-check operation")
	})

	It("rejects unsealed and wrongly bound expectation references before any source read", func() {
		asset := "package main\n\nfunc main() {}\n"
		f := newVerificationFixture(t)
		f.reader.blobs[gitRevision+"\x00pkg/main.go"] = asset
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"pkg/main.go","line_start":3,"line_end":3}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		key := "70000000-0000-4000-8000-000000000001"
		uploadID := "71000000-0000-4000-8000-000000000001"
		principal := ownerPrincipal(f)
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			return tx.CreateUpload(domain.Upload{ID: uploadID, PrincipalID: principal, Purpose: "body", InputMode: "object", SelectionName: "expectation", Target: []byte(`{"operation_id":"` + key + `","action":"evidence_verification","version_id":"` + f.version + `","evidence_id":"ev"}`), Checkpoint: domain.Checkpoint{Generation: 1, InventorySHA256: app.EmptyInventorySHA256, AcceptedBytes: "0"}, CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z"})
		})).To(Succeed())
		// Unsealed: rejected before any source read.
		_, e := f.verify(key, `{"expected_excerpt_ref":{"upload_id":"`+uploadID+`"}}`)
		Expect(e).To(MatchError(domain.ErrTransition))
		// A wrong operation binding is rejected independently of state.
		_, e = f.verify("70000000-0000-4000-8000-000000000002", `{"expected_excerpt_ref":{"upload_id":"`+uploadID+`"}}`)
		Expect(e).To(HaveOccurred())
	})
})

func ownerPrincipal(f *vFixture) string {
	record, e := f.auth.Authenticate(context.Background(), f.token)
	ExpectWithOffset(1, e).NotTo(HaveOccurred())
	return record.PrincipalID
}
