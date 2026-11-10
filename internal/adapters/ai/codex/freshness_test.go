package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type freshnessFrameTx struct {
	*questionFrameTx
	unit domain.RequiredUnit
}

func (t *freshnessFrameTx) MaintenanceIntent(string) (domain.MaintenanceIntent, error) {
	return domain.MaintenanceIntent{}, nil
}
func (t *freshnessFrameTx) RequiredUnits(set, kind, page, region string, limit int) ([]domain.RequiredUnit, error) {
	if set != t.unit.CoverageSetID {
		return nil, domain.ErrNotFound
	}
	if kind != "" {
		return []domain.RequiredUnit{}, nil
	}
	return []domain.RequiredUnit{t.unit}, nil
}

type freshnessFrameStore struct {
	*questionFrameStore
	fresh *freshnessFrameTx
}

func (s *freshnessFrameStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(s.fresh)
}

var _ = It("T-008-03 actual frame supplies the closed source candidate and imported support files without other HTML units", Label("unit"), func() {
	d, a, _, oldStore, data := questionFrames()
	page := oldStore.question.entries[4]
	for _, entry := range oldStore.question.entries {
		if entry.Path == "pages/00.html" {
			page = entry
			break
		}
	}
	Expect(page.Path).To(Equal("pages/00.html"))
	tx := &freshnessFrameTx{questionFrameTx: oldStore.question, unit: domain.RequiredUnit{CoverageSetID: "unit-set", Kind: "page", PagePath: page.Path}}
	d.Store = &freshnessFrameStore{questionFrameStore: oldStore, fresh: tx}
	sourceText := []byte("package source\nfunc Callee() {}\nfunc Caller() { Callee() }\n")
	hash := sha256.Sum256(sourceText)
	sha := hex.EncodeToString(hash[:])
	data[sha] = sourceText
	tx.objects[sha] = domain.Object{Hash: sha, Size: int64(len(sourceText)), State: "ready"}
	a.TargetSetID = "22222222-2222-4222-8222-222222222222"
	read := domain.CapturedRead{ID: "read", TargetSetID: a.TargetSetID, BytesSHA: sha, SourceJSON: json.RawMessage(`{"kind":"git","repo_id":"repo","exact_revision":"exact"}`), LocationJSON: json.RawMessage(`{"path":"source.go"}`)}
	contextText := []byte("package source\nvar AuthorizedConfiguration = true\n")
	contextHash := sha256.Sum256(contextText)
	contextSHA := hex.EncodeToString(contextHash[:])
	data[contextSHA] = contextText
	tx.objects[contextSHA] = domain.Object{Hash: contextSHA, Size: int64(len(contextText)), State: "ready"}
	contextRead := domain.CapturedRead{ID: "explicit-context-read", TargetSetID: a.TargetSetID, BytesSHA: contextSHA, SourceJSON: read.SourceJSON, LocationJSON: json.RawMessage(`{"path":"configuration.go"}`)}
	tx.reads = map[string]domain.CapturedRead{read.ID: read, contextRead.ID: contextRead}
	inv := domain.ProviderInvocation{Prompt: `{"kind":"page","page_path":"pages/00.html","region_id":null}`, Maintenance: &domain.MaintenanceInvocation{
		Mode: domain.MaintenanceModeFreshness, CoverageSetID: tx.unit.CoverageSetID,
		ProjectID: "project", VersionID: tx.version.ID, PagePath: &page.Path, UnitKind: "page", SourceMode: "pinned",
		Explanations: []domain.MaintenanceExplanation{{VersionID: tx.version.ID, PagePath: &page.Path, ObjectHash: page.Hash, Size: page.Size}},
		ReadBindings: []domain.MaintenanceReadBinding{{ReadID: read.ID, ProjectID: "project", VersionID: tx.version.ID, TargetSetID: a.TargetSetID}, {ReadID: contextRead.ID, ProjectID: "project", VersionID: tx.version.ID, TargetSetID: a.TargetSetID}},
	}}
	scope, err := json.Marshal(domain.ProviderScope{ProviderID: "codex", Invocation: &inv, ReadIDs: []string{read.ID, contextRead.ID}})
	Expect(err).NotTo(HaveOccurred())
	tx.job.Scope = scope
	parts := questionFrameParts(d, a, inv, domain.ProviderInput{AttemptID: a.ID, ReadID: read.ID, Hash: sha}, domain.ProviderInput{AttemptID: a.ID, ReadID: contextRead.ID, Hash: contextSHA})
	var candidates []domain.QuestionCitation
	text := ""
	for _, part := range parts {
		t := part["text"].(string)
		text += t + "\n"
		var header map[string]json.RawMessage
		if json.Unmarshal([]byte(t), &header) == nil && header["citation_candidate"] != nil {
			var candidate domain.QuestionCitation
			Expect(json.Unmarshal(header["citation_candidate"], &candidate)).To(Succeed())
			var fields map[string]json.RawMessage
			Expect(json.Unmarshal(header["citation_candidate"], &fields)).To(Succeed())
			Expect(fields).To(HaveLen(8))
			Expect(string(fields["verification_id"])).To(Equal("null"))
			candidates = append(candidates, candidate)
		}
	}
	Expect(candidates).To(HaveLen(2))
	Expect(candidates[0].Basis).To(Equal("observed_input"))
	Expect(candidates[0].InputOrigin).To(HaveValue(Equal("daemon_supplied")))
	Expect(candidates[0].ReadID).To(HaveValue(Equal(read.ID)))
	Expect(candidates[0].TargetSetID).To(Equal(a.TargetSetID))
	Expect(candidates[0].Hash).To(Equal(sha))
	Expect(candidates[1].ReadID).To(HaveValue(Equal(contextRead.ID)))
	Expect(candidates[1].TargetSetID).To(Equal(a.TargetSetID))
	Expect(candidates[1].Hash).To(Equal(contextSHA))
	Expect(text).To(ContainSubstring(string(sourceText)))
	Expect(text).To(ContainSubstring(string(contextText)))
	Expect(text).To(ContainSubstring("globalThis.savedQuestion=true"))
	Expect(text).To(ContainSubstring(`"basis":"explanation_file"`))
	Expect(strings.Count(text, "<html>complete page")).To(Equal(1), "other required HTML units were not examined by this page attempt")
	Expect(text).To(ContainSubstring("complete page 0 π"))
})
