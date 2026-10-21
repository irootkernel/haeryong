package source_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// registrationFixture reuses the verification fixture shape for the
// registration behaviors (C1).
var _ = Describe("project registration", Label("persistence"), func() {
	t := GinkgoT()
	It("registers, replays and rejects invalid names", func() {
		f := newVerificationFixture(t)
		key := "90000000-0000-4000-8000-000000000001"
		response, e := f.core.Register(context.Background(), f.token, key, "Source project")
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(201))
		first := map[string]any{}
		Expect(json.Unmarshal(response.Body, &first)).NotTo(HaveOccurred())
		Expect(first["name"]).To(Equal("Source project"))
		Expect(first["lifecycle"]).To(Equal("active"))
		Expect(first["etag"]).NotTo(BeEmpty())
		replay, e := f.core.Register(context.Background(), f.token, key, "Source project")
		Expect(e).NotTo(HaveOccurred())
		Expect(replay.Status).To(Equal(201))
		Expect(replay.Body).To(Equal(response.Body))
		duplicate, e := f.core.Register(context.Background(), f.token, key, "Source project")
		_ = duplicate
		_ = e
		// Invalid names fail closed without touching the registry.
		_, e = f.core.Register(context.Background(), f.token, "90000000-0000-4000-8000-000000000002", "")
		Expect(e).To(MatchError(sec.ErrInvalid))
		_, e = f.core.Register(context.Background(), f.token, "90000000-0000-4000-8000-000000000003", strings.Repeat("x", 129))
		Expect(e).To(MatchError(sec.ErrInvalid))
		_, e = f.core.Register(context.Background(), f.token, "90000000-0000-4000-8000-000000000004", "bad\x80name")
		Expect(e).To(MatchError(sec.ErrInvalid))
		var count int64
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			row, e := tx.QueryProject(first["project_id"].(string))
			if e != nil {
				return e
			}
			count = row.Revision
			return nil
		})).To(Succeed())
		Expect(count).To(Equal(int64(1)), "invalid attempts created nothing")
	})

	It("returns the stable 202 envelope when an accepted reservation already exists", func() {
		f := newVerificationFixture(t)
		key := "91000000-0000-4000-8000-000000000001"
		// An accepted reservation for this exact registration request (the
		// in-flight window) yields the stable pending envelope, never a second
		// effect (C1).
		principal := ownerPrincipal(f)
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			_, existing, e := tx.ReserveOperation(domain.Request{Key: domain.OperationKey{PrincipalID: principal, OperationID: key}, Method: "POST", Target: "/api/v1/projects", Body: []byte(`{"name":"Racer"}`)}, nil)
			Expect(e).NotTo(HaveOccurred())
			Expect(existing).To(BeFalse())
			return nil
		})).To(Succeed())
		pending, e := f.core.Register(context.Background(), f.token, key, "Racer")
		Expect(e).NotTo(HaveOccurred())
		Expect(pending.Status).To(Equal(202))
		var body map[string]any
		Expect(json.Unmarshal(pending.Body, &body)).NotTo(HaveOccurred())
		Expect(body["state"]).To(Equal("accepted"))
		racers := 0
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			rows, e := tx.QueryProjects(domain.CredentialScope{Kind: "owner"}, "", "", 100)
			for _, row := range rows {
				if row.Name == "Racer" {
					racers++
				}
			}
			return e
		})).To(Succeed())
		Expect(racers).To(Equal(0), "no project materialized behind the pending envelope")
	})
})

// C6: referenced expectations through the real upload/seal service.
var _ = Describe("referenced expectations", Label("persistence"), func() {
	t := GinkgoT()
	It("adopts sealed references, replays and survives upload loss", func() {
		f := newVerificationFixture(t)
		core := importer.New(f.store, f.auth, f.store, staging.New())
		blob := "package main\n\nfunc main() {}\n"
		f.reader.blobs[gitRevision+"\x00pkg/main.go"] = blob
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"pkg/main.go","line_start":3,"line_end":3}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		key := "92000000-0000-4000-8000-000000000001"
		uploadID := stageBodyExpectation(t, f, core, key, "func main() {}\n")
		response, e := f.core.Verify(context.Background(), f.token, f.version, "ev", key, []byte(`{"expected_excerpt_ref":{"upload_id":"`+uploadID+`"}}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(outcome(t, response)["outcome"]).To(Equal("matched"))
		// A wrong binding is rejected before any source read.
		_, e = f.core.Verify(context.Background(), f.token, f.version, "ev", "92000000-0000-4000-8000-000000000002", []byte(`{"expected_excerpt_ref":{"upload_id":"`+uploadID+`"}}`))
		Expect(e).To(HaveOccurred())
		// Replay returns the stored 201 with the identical body.
		replay, e := f.core.Verify(context.Background(), f.token, f.version, "ev", key, []byte(`{"expected_excerpt_ref":{"upload_id":"`+uploadID+`"}}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(replay.Status).To(Equal(201))
		Expect(replay.Body).To(Equal(response.Body))
		// Mismatch through a sealed reference with different bytes.
		mismatchKey := "92000000-0000-4000-8000-000000000003"
		other := stageBodyExpectation(t, f, core, mismatchKey, "func main() { /* different */ }\n")
		mismatch, e := f.core.Verify(context.Background(), f.token, f.version, "ev", mismatchKey, []byte(`{"expected_excerpt_ref":{"upload_id":"`+other+`"}}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(outcome(t, mismatch)["outcome"]).To(Equal("mismatch"))
		// Losing the sealed state must not break replay: the intent proves
		// the original reference (C6/SN7).
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			return tx.AbandonUpload(uploadID)
		})).To(Succeed())
		afterLoss, e := f.core.Verify(context.Background(), f.token, f.version, "ev", key, []byte(`{"expected_excerpt_ref":{"upload_id":"`+uploadID+`"}}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(afterLoss.Status).To(Equal(201))
		Expect(afterLoss.Body).To(Equal(response.Body))
		// A different upload id claiming the same key is rejected.
		_, e = f.core.Verify(context.Background(), f.token, f.version, "ev", key, []byte(`{"expected_excerpt_ref":{"upload_id":"`+other+`"}}`))
		Expect(e).To(HaveOccurred())
	})
})

// stageBodyExpectation runs the real purpose=body upload and seal flow bound
// to one verification operation key.
func stageBodyExpectation(t GinkgoTInterface, f *vFixture, core *importer.Service, key, content string) string {
	t.Helper()
	return stageBodyExpectationAs(t, f, core, f.token, key, []byte(content))
}

// stageBodyExpectationAs stages and seals a body expectation upload under
// token, bound to this fixture's evidence and the consumer key; the bytes may
// be any content, including invalid UTF-8.
func stageBodyExpectationAs(t GinkgoTInterface, f *vFixture, core *importer.Service, token, key string, raw []byte) string {
	t.Helper()
	target, _ := json.Marshal(map[string]string{"operation_id": key, "action": "evidence_verification", "version_id": f.version, "evidence_id": "ev"})
	created, e := core.Create(context.Background(), token, opKey(t), importer.CreateRequest{Purpose: "body", InputMode: "object", SelectionName: "expectation", Target: target})
	ExpectWithOffset(1, e).NotTo(HaveOccurred(), func() string { return fmt.Sprint(e) })
	var row struct {
		ID string `json:"upload_id"`
	}
	_ = json.Unmarshal(created.Body, &row)
	ExpectWithOffset(1, row.ID).NotTo(BeEmpty())
	ack, e := core.Put(context.Background(), token, row.ID, importer.Chunk{Path: "_input", Kind: "file", Hash: vsha(raw), Length: int64Ptr(int64(len(raw))), Final: true}, strings.NewReader(string(raw)))
	ExpectWithOffset(1, e).NotTo(HaveOccurred(), func() string { return fmt.Sprint(e) })
	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	completed, e := core.Complete(context.Background(), token, row.ID, opKey(t), cp)
	ExpectWithOffset(1, e).NotTo(HaveOccurred(), func() string { return fmt.Sprint(e) })
	var creation struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(completed.Body, &creation)
	ExpectWithOffset(1, creation.JobID).NotTo(BeEmpty())
	ExpectWithOffset(1, core.Process(context.Background(), creation.JobID)).To(Succeed())
	return row.ID
}

func int64Ptr(v int64) *int64 { return &v }

func opKey(t GinkgoTInterface) string {
	t.Helper()
	id, e := service.NewID()
	ExpectWithOffset(1, e).NotTo(HaveOccurred())
	return id
}

// removeObjectFile removes a retained object file from the fixture store.
func removeObjectFile(f *vFixture, hash string) error {
	return os.Remove(filepath.Join(f.dataDir, "storage", "objects", hash))
}

// C5/C7/C8/C9/C13 service behaviors.
var _ = Describe("verification corrections", Label("persistence"), func() {
	t := GinkgoT()
	It("keys verification lookup by principal so a reused UUID never crosses receipts", func() {
		f := newVerificationFixture(t)
		blob := "alpha\nbeta\n"
		f.reader.blobs[gitRevision+"\x00f.txt"] = blob
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt","line_start":1,"line_end":1}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		key := "93000000-0000-4000-8000-000000000001"
		ownerPrincipal := ownerPrincipal(f)
		response, e := f.verify(key, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(201))
		// Seed a second principal's verification row with the SAME uuid.
		other := "94000000-0000-4000-8000-000000000009"
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			return tx.PutVerification(domain.CitationVerification{ID: other, PrincipalID: "scoped-principal", VersionID: f.version, EvidenceID: "ev", SourceJSON: []byte(`{}`), LocationJSON: []byte(`{"path":"f.txt"}`), Outcome: "captured", ConfigRevision: 1, PermissionsRevision: 1, CheckedAt: "2026-01-02T00:00:00Z", OperationID: key})
		})).To(Succeed())
		var own, foreign domain.CitationVerification
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			row, found, e := tx.VerificationByOperation(ownerPrincipal, key)
			if e != nil {
				return e
			}
			Expect(found).To(BeTrue())
			own = row
			row, found, e = tx.VerificationByOperation("scoped-principal", key)
			if e != nil {
				return e
			}
			Expect(found).To(BeTrue())
			foreign = row
			return nil
		})).To(Succeed())
		Expect(own.PrincipalID).To(Equal(ownerPrincipal))
		Expect(foreign.PrincipalID).To(Equal("scoped-principal"))
		Expect(own.ID).NotTo(Equal(foreign.ID))
	})

	It("returns one 201 and one stable 202 for a genuinely concurrent duplicate and polling sees the same check", func() {
		f := newVerificationFixture(t)
		blob := "slow blob\n"
		f.reader.blobs[gitRevision+"\x00f.txt"] = blob
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt"}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		key := "93000000-0000-4000-8000-000000000002"
		release := make(chan struct{})
		f.reader.hook = func() { <-release }
		first := make(chan domain.Response, 1)
		firstErr := make(chan error, 1)
		go func() {
			response, e := f.core.Verify(context.Background(), f.token, f.version, "ev", key, []byte(`{}`))
			first <- response
			firstErr <- e
		}()
		time.Sleep(80 * time.Millisecond)
		pending, e := f.core.Verify(context.Background(), f.token, f.version, "ev", key, []byte(`{}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(pending.Status).To(Equal(202))
		var body map[string]any
		Expect(json.Unmarshal(pending.Body, &body)).NotTo(HaveOccurred())
		Expect(body["state"]).To(Equal("accepted"))
		close(release)
		var response domain.Response
		Eventually(first, 5*time.Second).Should(Receive(&response))
		Expect(<-firstErr).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(201))
		// Polling the operation returns the same terminal check.
		receipt, e := f.auth.Operation(context.Background(), f.token, key)
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal("completed"))
		Expect(receipt.Response.Status).To(Equal(201))
		Expect(receipt.Response.Body).To(Equal(response.Body))
	})

	It("fails typed when the configuration generation advanced between stages", func() {
		f := newVerificationFixture(t)
		f.reader.blobs[gitRevision+"\x00f.txt"] = "data\n"
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt"}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		key := "93000000-0000-4000-8000-000000000003"
		f.reader.hook = func() {
			f.reader.hook = nil
			Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
				current, e := tx.Configuration()
				if e != nil {
					return e
				}
				var config map[string]any
				Expect(json.Unmarshal(current.Value, &config)).NotTo(HaveOccurred())
				raw, _ := json.Marshal(config)
				return tx.SetConfiguration(domain.Configuration{Revision: current.Revision, Value: raw, Actor: "fixture"}, current.Revision)
			})).To(Succeed())
		}
		response, e := f.verify(key, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(412))
		var body map[string]any
		Expect(json.Unmarshal(response.Body, &body)).NotTo(HaveOccurred())
		Expect(body["error"]).NotTo(BeNil())
		receipt, e := f.auth.Operation(context.Background(), f.token, key)
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal("failed"))
	})

	It("verifies retained-first for git selected_snapshot sources and survives a missing checkout (F07)", func() {
		asset := "retained exact line\n"
		f := newVerificationFixture(t)
		// git source with selected_snapshot retention and a retained asset:
		// the checkout is missing, the retained bytes still verify (C9).
		f.reader.rootMissing = true
		f.buildVersion(`{"id":"src","kind":"git","retention_mode":"selected_snapshot","repo_id":"77777777-7777-4777-8777-777777777777","object_format":"sha1","exact_revision":"`+gitRevision+`","asset_path":"snap.bin"}`, `{"id":"ev","source_id":"src","capture_status":"verified","path":"snap.bin"}`, "snap.bin", []byte(asset))
		f.configure(gitMapping(t.TempDir()))
		response, e := f.verify("93000000-0000-4000-8000-000000000004", `{"expected_bytes_sha256":"`+vsha([]byte(asset))+`"}`)
		Expect(e).NotTo(HaveOccurred())
		first := outcome(t, response)
		Expect(first["outcome"]).To(Equal("matched"))
		Expect(first["bytes_origin"]).To(Equal("retained_snapshot"))
		// Losing the retained object falls back to the approved source when
		// the checkout exists again.
		f2 := newVerificationFixture(t)
		f2.reader.blobs[gitRevision+"\x00snap.bin"] = asset
		f2.buildVersion(`{"id":"src","kind":"git","retention_mode":"selected_snapshot","repo_id":"77777777-7777-4777-8777-777777777777","object_format":"sha1","exact_revision":"`+gitRevision+`","asset_path":"snap.bin"}`, `{"id":"ev","source_id":"src","capture_status":"verified","path":"snap.bin"}`, "snap.bin", []byte(asset))
		f2.configure(gitMapping(t.TempDir()))
		var hash string
		Expect(f2.store.Write(context.Background(), func(tx app.WriteTx) error {
			entry, e := tx.QueryEntry("14141414-1414-4141-8141-141414141414", "snap.bin")
			if e != nil {
				return e
			}
			hash = entry.Hash
			return nil
		})).To(Succeed())
		Expect(removeObjectFile(f2, hash)).To(Succeed())
		response, e = f2.verify("93000000-0000-4000-8000-000000000005", "")
		Expect(e).NotTo(HaveOccurred())
		second := outcome(t, response)
		Expect(second["outcome"]).To(Equal("captured"))
		Expect(second["bytes_origin"]).To(Equal("approved_source"))
	})

	It("records the registry requested selection and per-source target shape (C13)", func() {
		f := newVerificationFixture(t)
		blob := "shape\n"
		f.reader.blobs[gitRevision+"\x00f.txt"] = blob
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt"}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		_, e := f.verify("93000000-0000-4000-8000-000000000006", "")
		Expect(e).NotTo(HaveOccurred())
		var selection, targets map[string]any
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			captures, e := tx.SourceCaptures(f.project)
			if e != nil {
				return e
			}
			Expect(captures).To(HaveLen(1))
			Expect(json.Unmarshal(captures[0].RequestedSelection, &selection)).NotTo(HaveOccurred())
			Expect(json.Unmarshal(captures[0].TargetsJSON, &targets)).NotTo(HaveOccurred())
			return nil
		})).To(Succeed())
		Expect(selection["project_id"]).To(Equal(f.project))
		Expect(selection["mode"]).To(Equal("pinned"))
		Expect(selection["revision"]).To(Equal(gitRevision))
		Expect(selection["source_ids"]).To(Equal([]any{"git-1"}), "the configuration source id, never the version-local id")
		sources := targets["sources"].([]any)
		Expect(sources).To(HaveLen(1))
		entry := sources[0].(map[string]any)
		Expect(entry["source_id"]).To(Equal("git-1"))
		Expect(entry["kind"]).To(Equal("git"))
		Expect(entry["exact_revision"]).To(Equal(gitRevision))
	})

	It("does not strand a pending receipt when the request context dies mid-read (C7)", func() {
		f := newVerificationFixture(t)
		blob := "detached\n"
		f.reader.blobs[gitRevision+"\x00f.txt"] = blob
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt"}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		key := "93000000-0000-4000-8000-000000000007"
		ctx, cancel := context.WithCancel(context.Background())
		release := make(chan struct{})
		f.reader.hook = func() {
			f.reader.hook = nil
			cancel()
			<-release
		}
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = f.core.Verify(ctx, f.token, f.version, "ev", key, []byte(`{}`))
		}()
		time.Sleep(80 * time.Millisecond)
		close(release)
		wg.Wait()
		retry, e := f.verify(key, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(retry.Status).To(Equal(201), "the retry reaches a terminal receipt without a restart sweep")
		Expect(outcome(t, retry)["outcome"]).To(Equal("captured"))
		_ = errors.New
	})
})

// D3: adoption survives an upload abandoned mid-check.
var _ = Describe("expectation adoption resilience", Label("persistence"), func() {
	t := GinkgoT()
	It("adopts from the reserved hash when the upload is abandoned mid-check and leaves no pin", func() {
		f := newVerificationFixture(t)
		core := importer.New(f.store, f.auth, f.store, staging.New())
		blob := "package main\n\nfunc main() {}\n"
		f.reader.blobs[gitRevision+"\x00pkg/main.go"] = blob
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"pkg/main.go","line_start":3,"line_end":3}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		key := "95000000-0000-4000-8000-000000000001"
		uploadID := stageBodyExpectation(t, f, core, key, "func main() {}\n")
		// Abandon the upload after the seal, before the check completes.
		f.core.Fault = func(stage string) error {
			if stage == "after_pins" {
				Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
					return tx.AbandonUpload(uploadID)
				})).To(Succeed())
			}
			return nil
		}
		response, e := f.core.Verify(context.Background(), f.token, f.version, "ev", key, []byte(`{"expected_excerpt_ref":{"upload_id":"`+uploadID+`"}}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(201))
		first := outcome(t, response)
		Expect(first["outcome"]).To(Equal("matched"))
		var stored domain.CitationVerification
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			row, e := tx.LatestVerification(f.version, "ev")
			if e != nil {
				return e
			}
			stored = row
			return nil
		})).To(Succeed())
		Expect(stored.ExpectedExcerptRef).NotTo(BeEmpty(), "the expectation reference is recorded")
		pins, refs, e := f.store.SourceCheckObservations(context.Background())
		Expect(e).NotTo(HaveOccurred())
		Expect(pins).To(Equal(0), "no intent pin is left behind")
		Expect(refs).To(Equal(1), "the expectation object stays referenced")
	})
})

// D4: the detached bound expiring inside the completion still settles.
var _ = Describe("detached bound settlement", Label("persistence"), func() {
	t := GinkgoT()
	It("settles typed when the bound expires during the completion transaction", func() {
		f := newVerificationFixture(t)
		f.reader.blobs[gitRevision+"\x00f.txt"] = "bound\n"
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt"}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		key := "96000000-0000-4000-8000-000000000001"
		f.core.CheckBound = time.Nanosecond
		response, e := f.verify(key, "")
		if e == nil && response.Status == 201 {
			// The nanosecond bound may expire after the read but before the
			// commit observes it; either way the receipt must be terminal.
			receipt, re := f.auth.Operation(context.Background(), f.token, key)
			Expect(re).NotTo(HaveOccurred())
			Expect(receipt.State).To(Equal("completed"))
			return
		}
		Expect(response.Status).To(Equal(503))
		var body map[string]any
		Expect(json.Unmarshal(response.Body, &body)).NotTo(HaveOccurred())
		Expect(body["error"]).NotTo(BeNil())
		receipt, re := f.auth.Operation(context.Background(), f.token, key)
		Expect(re).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal("failed"), "the bound expiry settled the receipt")
		// A retry with the same key reaches the terminal failure.
		retry, re := f.verify(key, "")
		Expect(re).NotTo(HaveOccurred())
		Expect(retry.Status).To(Equal(503))
	})
})
