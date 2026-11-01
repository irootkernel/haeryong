package source_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	generation "haeryong.local/haeryong/internal/application/generation"
	importer "haeryong.local/haeryong/internal/application/import"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func sealedGenerationBody(f *vFixture, operation string, data []byte) *generation.BodyRef {
	imports := importer.New(f.store, f.auth, f.store, staging.New())
	target, _ := json.Marshal(map[string]any{"action": "generation", "operation_id": operation, "project_id": f.project, "document_id": nil})
	r, e := imports.Create(context.Background(), f.token, sid(), importer.CreateRequest{Purpose: "body", InputMode: "object", Target: target})
	Expect(e).NotTo(HaveOccurred())
	var created struct {
		ID string `json:"upload_id"`
	}
	Expect(json.Unmarshal(r.Body, &created)).To(Succeed())
	size := int64(len(data))
	hash := vsha(data)
	ack, e := imports.Put(context.Background(), f.token, created.ID, importer.Chunk{Path: "_input", Kind: "file", Hash: hash, Length: &size, Final: true}, bytes.NewReader(data))
	Expect(e).NotTo(HaveOccurred())
	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	r, e = imports.Complete(context.Background(), f.token, created.ID, sid(), cp)
	Expect(e).NotTo(HaveOccurred())
	var response struct {
		Job string `json:"job_id"`
	}
	Expect(json.Unmarshal(r.Body, &response)).To(Succeed())
	Expect(imports.Process(context.Background(), response.Job)).To(Succeed())
	return &generation.BodyRef{ID: created.ID, Hash: hash, Size: strconv.FormatInt(size, 10), MediaType: "text/plain"}
}

var _ = It("GGEN14 large generation prompt is a retained exact object and inline/body_ref normalize to one original receipt without raw author body in control state", Label("persistence"), func() {
	ctx := context.Background()
	f, _, _ := managedFixture("selected_snapshot")
	core := generationService(f)
	prompt := strings.Repeat("complete-large-body 日本語 π\n", 40000)
	input := generation.Request{ProjectID: f.project, DocumentPresent: true, Intent: "draft", ProviderID: "codex", Prompt: prompt}
	operation := sid()
	r, e := core.Start(ctx, f.token, operation, input)
	Expect(e).NotTo(HaveOccurred())
	job, _ := generatedIDs(r)
	j, e := f.store.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(string(j.Scope)).NotTo(ContainSubstring("complete-large-body"))
	var scope domain.ProviderScope
	Expect(json.Unmarshal(j.Scope, &scope)).To(Succeed())
	Expect(scope.Invocation.Prompt).To(BeEmpty())
	Expect(scope.Invocation.PromptRef.Hash).To(Equal(vsha([]byte(prompt))))
	Expect(scope.Invocation.PromptRef.Size).To(Equal(int64(len(prompt))))
	Expect(scope.ReadIDs).To(BeEmpty())
	Expect(scope.TargetSetID).To(BeEmpty())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		receipt, e := tx.Receipt(domain.OperationKey{PrincipalID: scope.PrincipalID, OperationID: operation})
		if e != nil {
			return e
		}
		Expect(string(receipt.Original.Body)).NotTo(ContainSubstring("complete-large-body"))
		Expect(string(receipt.Normalization)).NotTo(ContainSubstring("complete-large-body"))
		return nil
	})).To(Succeed())
	input.BodyRef = sealedGenerationBody(f, operation, []byte(prompt))
	input.Prompt = ""
	replay, e := core.Start(ctx, f.token, operation, input)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(r))
	// Another sealed handle with the same exact authorized binding and bytes is
	// the same request, not another job or a retargeted capability.
	input.BodyRef = sealedGenerationBody(f, operation, []byte(prompt))
	replay, e = core.Start(ctx, f.token, operation, input)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(r))
	imports := importer.New(f.store, f.auth, f.store, staging.New())
	_, e = imports.Abandon(ctx, f.token, input.BodyRef.ID, sid())
	Expect(e).NotTo(HaveOccurred())
	replay, e = core.Start(ctx, f.token, operation, input)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(r))
	// The caller-supplied handle cannot be reused for a different operation.
	_, e = core.Start(ctx, f.token, sid(), input)
	Expect(e).To(HaveOccurred())
})
var _ = It("GGEN15 fresh body_ref requires matching target, sealed UTF-8 bytes and exact descriptor and rejects changed-body replay", Label("persistence"), func() {
	ctx := context.Background()
	f, _, _ := managedFixture("selected_snapshot")
	core := generationService(f)
	operation := sid()
	input := generation.Request{ProjectID: f.project, DocumentPresent: true, Intent: "draft", ProviderID: "codex", BodyRef: sealedGenerationBody(f, operation, []byte("large request π"))}
	ref := *input.BodyRef
	input.BodyRef.Size = "1"
	_, e := core.Start(ctx, f.token, operation, input)
	Expect(e).To(HaveOccurred())
	input.BodyRef = &ref
	r, e := core.Start(ctx, f.token, operation, input)
	Expect(e).NotTo(HaveOccurred())
	job, _ := generatedIDs(r)
	Expect(job).NotTo(BeEmpty())
	input.BodyRef = sealedGenerationBody(f, operation, []byte("different request"))
	_, e = core.Start(ctx, f.token, operation, input)
	Expect(e).To(MatchError(domain.ErrConflict))
	for _, data := range [][]byte{{0xff, 'x'}, []byte(" \n\t")} {
		key := sid()
		input.BodyRef = sealedGenerationBody(f, key, data)
		_, e = core.Start(ctx, f.token, key, input)
		Expect(e).To(HaveOccurred())
		actor, e := f.auth.Authenticate(ctx, f.token)
		Expect(e).NotTo(HaveOccurred())
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			_, e := tx.Receipt(domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key})
			Expect(e).To(MatchError(domain.ErrNotFound))
			return nil
		})).To(Succeed())
	}
	input.BodyRef = nil
	input.Prompt = "ordinary request"
	input.Sources.ProviderID = "other"
	_, e = core.Start(ctx, f.token, sid(), input)
	Expect(e).To(HaveOccurred())
	input.Sources.ProviderID = ""
	input.Sources.SourceMode = "unknown"
	_, e = core.Start(ctx, f.token, sid(), input)
	Expect(e).To(HaveOccurred())
})
