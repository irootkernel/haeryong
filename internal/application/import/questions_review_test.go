package importer_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/questions"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("T5QA17 normal metadata-free import accepts exact runtime regions on separate pages and retains full context through ongoing authority", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	s, _, input := askFixture(f)
	target, _ := json.Marshal(map[string]any{"project_id": f.project, "document_id": nil})
	r, e := f.core.Create(ctx, f.token, key(GinkgoT()), importer.CreateRequest{Purpose: "result", InputMode: "directory", SelectionName: "runtime pages", Target: target})
	Expect(e).NotTo(HaveOccurred())
	var created struct {
		ID string `json:"upload_id"`
	}
	Expect(json.Unmarshal(r.Body, &created)).To(Succeed())
	upload := created.ID
	var cp domain.Checkpoint
	for _, page := range []string{"a.html", "b.html"} {
		content := []byte("<html>" + page + `<script>window.__haeryong?.select("runtime-choice",{selected_text:"full"})</script></html>`)
		hash := sha256.Sum256(content)
		size := int64(len(content))
		ack, e := f.core.Put(ctx, f.token, upload, importer.Chunk{Path: page, Kind: "file", Hash: hex.EncodeToString(hash[:]), Length: &size, Final: true}, bytes.NewReader(content))
		Expect(e).NotTo(HaveOccurred())
		cp = ack.Checkpoint
	}
	cp.Generation++
	cp.Complete = true
	r, e = f.core.Complete(ctx, f.token, upload, key(GinkgoT()), cp)
	Expect(e).NotTo(HaveOccurred())
	Expect(f.core.Process(ctx, decoded(GinkgoT(), r)["job_id"])).To(Succeed())
	_, version := f.imported(GinkgoT(), upload, key(GinkgoT()))
	region := "runtime-choice"
	input.RegionID = &region
	input.PageQuery = "mode=exact"
	input.PageFragment = "runtime-choice"
	input.DisplayState = json.RawMessage(`{"selected":"` + strings.Repeat("full α ", 4096) + `"}`)
	hashes := map[string]string{}
	for _, page := range []string{"a.html", "b.html"} {
		input.PagePath = &page
		qid, jid := askStart(s, f, version, input)
		q, e := s.Question(ctx, f.token, qid)
		Expect(e).NotTo(HaveOccurred())
		Expect(q.RegionID).To(Equal(&region))
		Expect(q.PagePath).To(Equal(&page))
		Expect(q.PageQuery).To(Equal(input.PageQuery))
		Expect(q.PageFragment).To(Equal(input.PageFragment))
		hashes[page] = q.Explanation.Hash
		var body bytes.Buffer
		Expect(s.ReadContext(ctx, f.token, qid, func(_ domain.QuestionContext, _ domain.Object, r io.Reader) error {
			_, e := io.Copy(&body, r)
			return e
		})).To(Succeed())
		Expect(body.String()).To(Equal(string(input.DisplayState)))
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			j, e := tx.Job(jid)
			if e != nil {
				return e
			}
			var scope domain.ProviderScope
			Expect(json.Unmarshal(j.Scope, &scope)).To(Succeed())
			Expect(app.AuthorizeQuestion(tx, scope)).To(Succeed())
			revoked := scope
			revoked.AuthEpoch = "revoked-test-epoch"
			Expect(errors.Is(app.AuthorizeQuestion(tx, revoked), domain.ErrDenied)).To(BeTrue())
			_, e = tx.VersionRegion(version, page, region)
			Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue())
			wrong := q.Explanation
			other := "missing.html"
			wrong.PagePath = &other
			_, e = app.ResolveQuestionReference(tx, scope.CredentialScope, wrong)
			Expect(e).To(HaveOccurred())
			wrong = q.Explanation
			wrong.Hash = strings.Repeat("f", 64)
			_, e = app.ResolveQuestionReference(tx, scope.CredentialScope, wrong)
			Expect(errors.Is(e, domain.ErrContentIntegrity)).To(BeTrue())
			return nil
		})).To(Succeed())
		d := &generationDriver{data: answerBytes("final", "Runtime context is display data, not source proof.")}
		p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: d, Prepare: s.Prepare, Apply: s.Apply}
		Expect(p.Process(ctx, jid)).To(Succeed())
		Expect(p.StopProviders()).To(Succeed())
		Expect(d.calls.Load()).To(Equal(int32(1)))
	}
	Expect(hashes["a.html"]).NotTo(Equal(hashes["b.html"]))
	input.PagePath = nil
	_, e = s.Start(ctx, f.token, key(GinkgoT()), version, input)
	Expect(e).To(HaveOccurred())
	missing := "missing.html"
	input.PagePath = &missing
	_, e = s.Start(ctx, f.token, key(GinkgoT()), version, input)
	Expect(e).To(HaveOccurred())
})

var _ = It("T5QA18 source-policy refusal commits a terminal failed receipt without a Question or job and preserves accepted keys", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	s, v, input := askFixture(f)
	id, root := questionSource(f, s, &input)
	questionMapping(f, id, root, false)
	k := key(GinkgoT())
	refused, e := s.Start(ctx, f.token, k, v, input)
	var refusal *questions.AdmissionRefusal
	Expect(errors.As(e, &refusal)).To(BeTrue())
	Expect(refused.Status).To(Equal(403))
	Expect(refusal.OperationID).To(Equal(k))
	Expect(refusal.PrincipalID).NotTo(BeEmpty())
	Expect(refusal.RuntimeGeneration).NotTo(BeEmpty())
	receipt, e := f.auth.Operation(ctx, f.token, k)
	Expect(e).NotTo(HaveOccurred())
	Expect(receipt.State).To(Equal("failed"))
	Expect(receipt.Response).NotTo(BeNil())
	Expect(receipt.Response.Status).To(Equal(403))
	Expect(receipt.Response.Body).To(Equal(refused.Body))
	var storedFailure struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	Expect(json.Unmarshal(receipt.Response.Body, &storedFailure)).To(Succeed())
	Expect(storedFailure.Error.Message).To(Equal("Forbidden"))

	page, e := s.Questions(ctx, f.token, v, "")
	Expect(e).NotTo(HaveOccurred())
	Expect(page.Items).To(BeEmpty())

	questionMapping(f, id, root, true)
	replayed, e := s.Start(ctx, f.token, k, v, input)
	refusal = nil
	Expect(errors.As(e, &refusal)).To(BeTrue())
	Expect(replayed.Status).To(Equal(403))
	Expect(replayed.Body).To(Equal(refused.Body))
	// A fresh explicit intent alone may use the restored source authority.
	newKey := key(GinkgoT())
	accepted, e := s.Start(ctx, f.token, newKey, v, input)
	Expect(e).NotTo(HaveOccurred())
	Expect(accepted.Status).To(Equal(202))
	questionMapping(f, id, root, false)
	replay, e := s.Start(ctx, f.token, newKey, v, input)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay.Body).To(Equal(accepted.Body))
	Expect(replay.Status).To(Equal(202))
	input.Question = "different under original terminal key"
	_, e = s.Start(ctx, f.token, k, v, input)
	refusal = nil
	Expect(errors.As(e, &refusal)).To(BeFalse())
	Expect(errors.Is(e, domain.ErrConflict)).To(BeTrue())
})
