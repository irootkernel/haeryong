//go:build haeryong_provider_live

package source_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/ai/codex"
	"haeryong.local/haeryong/internal/adapters/contentstore"
	adapter "haeryong.local/haeryong/internal/adapters/source"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var _ = It("C03LIVE production factory and coordinator complete one exact managed-source subscription request", Label("persistence"), func() {
	evidence := os.Getenv("HAERYONG_PROVIDER_LIVE_EVIDENCE")
	binary := os.Getenv("HAERYONG_PROVIDER_LIVE_BINARY")
	Expect(filepath.IsAbs(evidence) && filepath.IsAbs(binary)).To(BeTrue(), "explicit native binary and private physical evidence directory required")
	physical, e := filepath.EvalSymlinks(evidence)
	Expect(e).NotTo(HaveOccurred())
	Expect(physical).To(Equal(evidence))
	info, e := os.Stat(evidence)
	Expect(e).NotTo(HaveOccurred())
	Expect(info.Mode().Perm() & 0077).To(BeZero())
	f := newVerificationFixture(liveFixtureT{GinkgoTInterface: GinkgoT(), root: evidence})
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	health, e := f.store.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	f.project = health.Library.UnfiledProjectID
	repo, e := filepath.Abs("../../..")
	Expect(e).NotTo(HaveOccurred())
	mapping, _ := json.Marshal(domain.ManagedSourceMapping{SourceID: "haeryong-source", Kind: "files", RootPath: repo, RetentionMode: "selected_snapshot", ProviderIDs: []string{"codex"}})
	f.configure(string(mapping))
	f.core.Files = adapter.Files{ExcludedPaths: []string{f.dataDir}}
	path := "internal/application/storage/provider.go"
	selected, e := os.ReadFile(filepath.Join(repo, path))
	Expect(e).NotTo(HaveOccurred())
	invocation := domain.ProviderInvocation{CWD: repo, Prompt: "Use only the supplied source text, without tools or other files. In the supplied Go ProviderTx interface identify the method that records actual provider input observation. Reply with exactly its method name, no punctuation or explanation."}
	job := sid()
	var reads []domain.SourceCaptureRead
	admission := codexAdmission(f, job, invocation, &reads)
	request := source.CaptureRequest{SourceMode: "current", ProviderID: "codex", Selections: []source.CaptureSelection{{SourceID: "haeryong-source", Paths: []source.CapturePath{{Path: path}}}}}
	response, e := f.core.CaptureAndAdmit(ctx, f.token, sid(), f.project, request, admission)
	Expect(e).NotTo(HaveOccurred())
	Expect(response.Status).To(Equal(201))
	Expect(reads).To(HaveLen(1))
	Expect(reads[0].Hash).To(Equal(vsha(selected)))
	driver := codex.New(f.store, f.store, evidence)
	driver.Binary = binary
	coordinator := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver}
	Expect(coordinator.Process(ctx, job)).To(Succeed())
	var attempt domain.ProviderAttempt
	var inputs []domain.ProviderInput
	var object domain.Object
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.ProviderAttempts(job)
		if e != nil {
			return e
		}
		Expect(rows).To(HaveLen(1))
		attempt = rows[0]
		inputs, e = tx.ProviderInputs(attempt.ID)
		if e != nil {
			return e
		}
		object, e = tx.Object(attempt.ResultHash)
		return e
	})).To(Succeed())
	Expect(attempt.State).To(Equal("result_saved"))
	Expect(attempt.RequestID).NotTo(BeEmpty())
	Expect(attempt.SessionID).NotTo(BeEmpty())
	Expect(attempt.Transport).NotTo(BeNil())
	Expect(attempt.Transport.AcceptedAt).NotTo(BeEmpty())
	Expect(attempt.Transport.LocalClosed).To(BeTrue())
	Expect(inputs).To(HaveLen(1))
	Expect(inputs[0].Hash).To(Equal(vsha(selected)))
	Expect(inputs[0].ObservedOrigin).To(Equal("daemon_supplied"))
	Expect(inputs[0].ObservedAt).NotTo(BeNil())
	var answer []byte
	Expect(f.store.Read(ctx, object, domain.Pin{Kind: "provider_continuation", OwnerID: attempt.ID}, func(r io.ReadSeeker) error { var e error; answer, e = io.ReadAll(r); return e })).To(Succeed())
	Expect(strings.TrimSpace(string(answer))).To(Equal("ObserveProviderInput"))
	// No source/output body, credentials, raw config, native diagnostics or
	// reasoning is retained in this report. The actual SQLite writer was used.
	proof, _ := json.MarshalIndent(map[string]any{"schema": "haeryong.production-provider-proof/v1", "factory": "internal/adapters/ai/codex.New", "coordinator": "work.ProviderCoordinator.Process", "admission": "trusted Source.CaptureAndAdmit (public Task04/05 admission not implemented)", "daemon_live_workflow": false, "attempt": attempt, "inputs": inputs, "source_path": path, "source_sha256": vsha(selected), "output_sha256": object.Hash, "expected_source_answer": true, "exactly_one_attempt": true}, "", "  ")
	Expect(os.WriteFile(filepath.Join(evidence, "production-proof.json"), append(proof, '\n'), 0600)).To(Succeed())
})

// Keep the actual attempt ledger if a native outcome is uncertain. A failed
// fixture must remain reconcilable instead of disappearing with test TempDir.
type liveFixtureT struct {
	GinkgoTInterface
	root string
}

func (t liveFixtureT) TempDir() string {
	path, e := os.MkdirTemp(t.root, "profile-")
	Expect(e).NotTo(HaveOccurred())
	return path
}

var _ = It("C03PROBE reads only redacted native subscription status without inference", Label("persistence"), func() {
	evidence := os.Getenv("HAERYONG_PROVIDER_LIVE_EVIDENCE")
	binary := os.Getenv("HAERYONG_PROVIDER_LIVE_BINARY")
	Expect(filepath.IsAbs(evidence) && filepath.IsAbs(binary)).To(BeTrue())
	repo, e := filepath.Abs("../../..")
	Expect(e).NotTo(HaveOccurred())
	d := codex.New(nil, nil, evidence)
	d.Binary = binary
	status := d.Status(context.Background(), repo)
	raw, e := json.MarshalIndent(status, "", "  ")
	Expect(e).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(evidence, "native-status.json"), append(raw, '\n'), 0600)).To(Succeed())
})

var _ = It("C03RETAIN verifies the historical real input through the canonical contentstore without inference", Label("persistence"), func() {
	evidence := os.Getenv("HAERYONG_PROVIDER_LIVE_EVIDENCE")
	Expect(filepath.IsAbs(evidence)).To(BeTrue())
	originalBytes, e := os.ReadFile(filepath.Join(evidence, "production-proof.json"))
	Expect(e).NotTo(HaveOccurred())
	var original struct {
		Attempt   domain.ProviderAttempt `json:"attempt"`
		SourceSHA string                 `json:"source_sha256"`
	}
	Expect(json.Unmarshal(originalBytes, &original)).To(Succeed())
	candidates, e := filepath.Glob(filepath.Join(evidence, "profile-*", "library", "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	var storage, readID string
	var size int64
	matches := 0
	for _, candidate := range candidates {
		db, e := sql.Open("sqlite", "file:"+candidate+"?mode=ro")
		Expect(e).NotTo(HaveOccurred())
		var found int
		Expect(db.QueryRow(`SELECT count(*) FROM provider_attempts WHERE attempt_id=?`, original.Attempt.ID).Scan(&found)).To(Succeed())
		if found == 1 {
			matches++
			storage = filepath.Dir(candidate)
			Expect(db.QueryRow(`SELECT r.read_id,r.byte_size FROM source_capture_reads r JOIN provider_inputs i ON i.read_id=r.read_id WHERE i.attempt_id=? AND i.content_hash=? AND r.content_hash=i.content_hash AND r.retention_mode='selected_snapshot' AND r.availability='available'`, original.Attempt.ID, original.SourceSHA).Scan(&readID, &size)).To(Succeed())
		}
		Expect(db.Close()).To(Succeed())
	}
	Expect(matches).To(Equal(1))
	objects, e := contentstore.New(storage)
	Expect(e).NotTo(HaveOccurred())
	defer objects.Close()
	hash := sha256.New()
	var count int64
	Expect(objects.VerifiedRead(context.Background(), contentstore.Object{Hash: original.SourceSHA, Size: size}, func(r io.ReadSeeker) error { var e error; count, e = io.Copy(hash, r); return e })).To(Succeed())
	actualSHA := fmt.Sprintf("%x", hash.Sum(nil))
	Expect(actualSHA).To(Equal(original.SourceSHA))
	Expect(count).To(Equal(size))
	current, e := os.ReadFile("../../../internal/application/storage/provider.go")
	Expect(e).NotTo(HaveOccurred())
	currentSHA := fmt.Sprintf("%x", sha256.Sum256(current))
	proof, _ := json.MarshalIndent(map[string]any{"schema": "haeryong.retained-provider-input/v1", "canonical_verifier": "contentstore.VerifiedRead", "read_only_sqlite": true, "native_inference": false, "original_proof_sha256": fmt.Sprintf("%x", sha256.Sum256(originalBytes)), "attempt_id": original.Attempt.ID, "read_id": readID, "object_path": filepath.Join(storage, "objects", original.SourceSHA), "source_sha256": actualSHA, "byte_size": count, "retention_mode": "selected_snapshot", "current_workspace_source_sha256": currentSHA, "matches_current_workspace": actualSHA == currentSHA, "original_proof_unchanged": true}, "", "  ")
	Expect(os.WriteFile(filepath.Join(evidence, "retained-input-proof.json"), append(proof, '\n'), 0600)).To(Succeed())
})
