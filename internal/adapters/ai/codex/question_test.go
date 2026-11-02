package codex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

type questionFrameTx struct {
	*testTx
	entries []domain.Entry
	version domain.Version
	region  domain.Region
}

func (t *questionFrameTx) PublishedVersion(id string) (domain.Version, error) {
	if id != t.version.ID {
		return domain.Version{}, domain.ErrNotFound
	}
	return t.version, nil
}
func (t *questionFrameTx) Entries(set, after string, limit int) ([]domain.Entry, error) {
	if set != t.version.ContentSetID {
		return nil, domain.ErrNotFound
	}
	rows := []domain.Entry{}
	for _, e := range t.entries {
		if e.Path > after {
			rows = append(rows, e)
			if len(rows) == limit {
				break
			}
		}
	}
	return rows, nil
}
func (t *questionFrameTx) VersionRegion(version, page, region string) (domain.Region, error) {
	if version != t.region.VersionID || page != t.region.PagePath || region != t.region.ID {
		return domain.Region{}, domain.ErrNotFound
	}
	return t.region, nil
}

type questionFrameStore struct {
	*testStore
	question *questionFrameTx
}

func (s *questionFrameStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(s.question)
}

type questionFrameBytes map[string][]byte

func (b questionFrameBytes) Read(ctx context.Context, o domain.Object, _ domain.Pin, consume func(io.ReadSeeker) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	raw, ok := b[o.Hash]
	if !ok || int64(len(raw)) != o.Size {
		return domain.ErrContentIntegrity
	}
	return consume(bytes.NewReader(raw))
}

func questionFrames() (*Driver, domain.ProviderAttempt, domain.ProviderInvocation, *questionFrameStore, questionFrameBytes) {
	base := &testTx{objects: map[string]domain.Object{}}
	store := &questionFrameStore{testStore: &testStore{tx: base}}
	store.question = &questionFrameTx{testTx: base, version: domain.Version{ID: "11111111-1111-4111-8111-111111111111", ContentSetID: "set"}}
	data := questionFrameBytes{}
	put := func(text []byte) domain.ProviderText {
		h := sha256.Sum256(text)
		hash := hex.EncodeToString(h[:])
		data[hash] = bytes.Clone(text)
		base.objects[hash] = domain.Object{Hash: hash, Size: int64(len(text)), State: "ready"}
		return domain.ProviderText{Hash: hash, Size: int64(len(text))}
	}
	prompt := put([]byte("Whole saved version question π"))
	manifest := put([]byte(`{"inventory":"immutable whole directory"}`))
	for i := 0; i < 66; i++ {
		raw := []byte(fmt.Sprintf("<html>complete page %d π</html>", i))
		o := put(raw)
		store.question.entries = append(store.question.entries, domain.Entry{Path: fmt.Sprintf("pages/%02d.html", i), Kind: "file", Hash: o.Hash, Size: o.Size})
	}
	for path, raw := range map[string][]byte{"behavior.js": []byte("globalThis.savedQuestion=true"), "attachment.bin": {0, 255, 1, 2}} {
		o := put(raw)
		store.question.entries = append(store.question.entries, domain.Entry{Path: path, Kind: "file", Hash: o.Hash, Size: o.Size})
	}
	store.question.entries = append(store.question.entries, domain.Entry{Path: "empty", Kind: "directory"}, domain.Entry{Path: "alias.html", Kind: "symlink", LinkText: "pages/00.html"})
	sort.Slice(store.question.entries, func(i, j int) bool { return store.question.entries[i].Path < store.question.entries[j].Path })
	qid, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	aid, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	q := &domain.QuestionInvocation{QuestionID: qid, VersionID: store.question.version.ID, SourceMode: "pinned", Explanation: domain.GenerationReference{ExplanationReference: domain.ExplanationReference{VersionID: store.question.version.ID, Hash: strings.Repeat("a", 64)}, ObjectHash: manifest.Hash, Size: manifest.Size}, Limitations: []domain.QuestionLimitation{{Code: "unable_to_verify", Message: "No project source selected."}}}
	inv := domain.ProviderInvocation{PromptRef: &prompt, Question: q}
	scope := domain.ProviderScope{ProviderID: "codex", PrincipalID: "owner", Invocation: &inv, Target: json.RawMessage(`{"operation_id":"11111111-1111-4111-8111-111111111113","action":"question","version_id":"11111111-1111-4111-8111-111111111111","page_path":null}`)}
	sha, e := SemanticRequestSHA(scope.PrincipalID, scope.Target, inv, nil)
	Expect(e).NotTo(HaveOccurred())
	raw, e := json.Marshal(scope)
	Expect(e).NotTo(HaveOccurred())
	base.job = domain.Job{ID: "job", Kind: "question", Scope: raw}
	d := New(store, data, GinkgoT().TempDir())
	return d, domain.ProviderAttempt{ID: aid, JobID: "job", RequestSHA: sha}, inv, store, data
}

func questionFrameParts(d *Driver, a domain.ProviderAttempt, inv domain.ProviderInvocation, inputs ...domain.ProviderInput) []map[string]any {
	f, _, e := d.frame(context.Background(), a, inputs, inv, "question-thread")
	Expect(e).NotTo(HaveOccurred())
	defer os.Remove(f.Name())
	defer f.Close()
	var body struct {
		Input []map[string]any `json:"input"`
	}
	Expect(json.NewDecoder(f).Decode(&body)).To(Succeed())
	return body.Input
}

func questionOutputShape(parts []map[string]any) domain.AnswerEnvelope {
	var shapes []domain.AnswerEnvelope
	for _, part := range parts {
		raw := []byte(part["text"].(string))
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields["state"] == nil || fields["body"] == nil {
			continue
		}
		Expect(fields).To(HaveLen(5))
		var shape domain.AnswerEnvelope
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		Expect(decoder.Decode(&shape)).To(Succeed())
		Expect(shape.Citations).NotTo(BeNil())
		Expect(shape.ExplanationReferences).NotTo(BeNil())
		Expect(shape.Limitations).NotTo(BeNil())
		shapes = append(shapes, shape)
	}
	Expect(shapes).To(HaveLen(1), "one complete typed output example in the actual frame")
	return shapes[0]
}

var _ = It("T5QFRAME01 whole-version question supplies all directory files beyond a cursor page and preserves exact binary encoding", Label("unit"), func() {
	d, a, inv, store, data := questionFrames()
	parts := questionFrameParts(d, a, inv)
	shape := questionOutputShape(parts)
	Expect(shape.ExplanationReferences).To(Equal([]domain.ExplanationReference{inv.Question.Explanation.ExplanationReference}))
	Expect(shape.ExplanationReferences[0].PagePath).To(BeNil())
	Expect(shape.ExplanationReferences[0].RegionID).To(BeNil())
	files := map[string][]byte{}
	kinds := map[string]string{}
	for i, part := range parts {
		if part["type"] != "text" {
			continue
		}
		var metadata map[string]any
		if json.Unmarshal([]byte(part["text"].(string)), &metadata) != nil {
			continue
		}
		if metadata["basis"] == "explanation_entry" {
			kinds[metadata["path"].(string)] = metadata["kind"].(string)
		}
		if metadata["basis"] != "explanation_file" {
			continue
		}
		raw := []byte(parts[i+1]["text"].(string))
		if metadata["encoding"] == "base64" {
			var e error
			raw, e = base64.StdEncoding.DecodeString(string(raw))
			Expect(e).NotTo(HaveOccurred())
		}
		files[metadata["path"].(string)] = raw
	}
	Expect(files).To(HaveLen(68))
	for _, entry := range store.question.entries {
		if entry.Kind == "file" {
			Expect(files[entry.Path]).To(Equal(data[entry.Hash]), entry.Path)
		}
	}
	Expect(kinds).To(HaveKeyWithValue("empty", "directory"))
	Expect(kinds).To(HaveKeyWithValue("alias.html", "symlink"))
})

var _ = It("T5QFRAME02 page-region question supplies its exact page and region metadata without unrelated directory files", Label("unit"), func() {
	d, a, inv, store, data := questionFrames()
	page, region := "pages/02.html", "logical-region"
	var selected domain.Entry
	for _, entry := range store.question.entries {
		if entry.Path == page {
			selected = entry
		}
	}
	inv.Question.PagePath = &page
	inv.Question.RegionID = &region
	inv.Question.Explanation.ExplanationReference.PagePath = &page
	inv.Question.Explanation.ExplanationReference.RegionID = &region
	inv.Question.Explanation.Hash = selected.Hash
	inv.Question.Explanation.ObjectHash = selected.Hash
	inv.Question.Explanation.Size = selected.Size
	store.question.region = domain.Region{VersionID: inv.Question.VersionID, PagePath: page, ID: region, Metadata: []byte(`{"id":"logical-region","label":"Selected region"}`)}
	sourceBytes := []byte("Exact authorized source body; no runtime observation is asserted.\n")
	sum := sha256.Sum256(sourceBytes)
	hash := hex.EncodeToString(sum[:])
	data[hash] = sourceBytes
	store.tx.objects[hash] = domain.Object{Hash: hash, Size: int64(len(sourceBytes)), State: "ready"}
	a.TargetSetID = "11111111-1111-4111-8111-111111111118"
	read := domain.CapturedRead{ID: "11111111-1111-4111-8111-111111111119", TargetSetID: a.TargetSetID, SourceJSON: []byte(`{"kind":"git","repo_id":"11111111-1111-4111-8111-111111111120","object_format":"sha1","exact_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), LocationJSON: []byte(`{"path":"source.go"}`), BytesSHA: hash}
	store.tx.reads = map[string]domain.CapturedRead{read.ID: read}
	parts := questionFrameParts(d, a, inv, domain.ProviderInput{AttemptID: a.ID, ReadID: read.ID, Hash: hash})
	shape := questionOutputShape(parts)
	Expect(shape.ExplanationReferences).To(Equal([]domain.ExplanationReference{inv.Question.Explanation.ExplanationReference}))
	Expect(shape.Limitations).To(HaveLen(1))
	Expect(shape.Limitations[0].Code).NotTo(BeEmpty())
	Expect(shape.Limitations[0].Message).NotTo(BeEmpty())
	files := []string{}
	regions := 0
	candidates := 0
	for i, part := range parts {
		var m map[string]any
		if json.Unmarshal([]byte(part["text"].(string)), &m) != nil {
			continue
		}
		if m["basis"] == "explanation_file" {
			files = append(files, m["path"].(string))
			Expect([]byte(parts[i+1]["text"].(string))).To(Equal(data[selected.Hash]))
		}
		if m["basis"] == "explanation_region" {
			regions++
			Expect(m["region_id"]).To(Equal(region))
		}
		if candidate, ok := m["citation_candidate"].(map[string]any); ok {
			candidates++
			Expect(candidate).To(HaveLen(8))
			Expect(candidate).To(HaveKeyWithValue("verification_id", BeNil()))
			raw, e := json.Marshal(candidate)
			Expect(e).NotTo(HaveOccurred())
			var citation domain.QuestionCitation
			Expect(json.Unmarshal(raw, &citation)).To(Succeed())
			Expect(citation.Basis).To(Equal("observed_input"))
			Expect(citation.ReadID).NotTo(BeNil())
			Expect(*citation.ReadID).To(Equal(read.ID))
			Expect(citation.InputOrigin).NotTo(BeNil())
			Expect(*citation.InputOrigin).To(Equal("daemon_supplied"))
			Expect(citation.TargetSetID).To(Equal(a.TargetSetID))
			Expect(citation.Source).To(MatchJSON(read.SourceJSON))
			Expect(citation.Location).To(MatchJSON(read.LocationJSON))
			Expect(citation.Hash).To(Equal(hash))
			Expect([]byte(parts[i+1]["text"].(string))).To(Equal(sourceBytes))
			shape.Citations = append(shape.Citations, citation)
		}
	}
	Expect(files).To(Equal([]string{page}))
	Expect(regions).To(Equal(1))
	Expect(candidates).To(Equal(1))
	encoded, e := json.Marshal(shape)
	Expect(e).NotTo(HaveOccurred())
	var fields map[string]json.RawMessage
	Expect(json.Unmarshal(encoded, &fields)).To(Succeed())
	for name, count := range map[string]int{"citations": 8, "explanation_references": 4, "limitations": 2} {
		var rows []map[string]json.RawMessage
		Expect(json.Unmarshal(fields[name], &rows)).To(Succeed())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0]).To(HaveLen(count))
	}
})

var _ = It("T5QFRAME03 question frame stops before dispatch when current authority is revoked during complete context delivery", Label("unit"), func() {
	d, a, inv, store, _ := questionFrames()
	store.question.failGuardAt = 12
	f, _, e := d.frame(context.Background(), a, nil, inv, "thread")
	Expect(e).To(MatchError(domain.ErrDenied))
	Expect(f).To(BeNil())
	entries, e := os.ReadDir(d.Scratch)
	Expect(e).NotTo(HaveOccurred())
	for _, entry := range entries {
		Expect(entry.Name()).NotTo(HavePrefix("provider-input-"))
	}
})

var _ = It("T5QFRAME04 question semantic identity ignores staging IDs but preserves exact anchor context and payload without mutating invocation", Label("unit"), func() {
	_, _, inv, _, _ := questionFrames()
	originalRender := "11111111-1111-4111-8111-111111111114"
	inv.Question.Context = &domain.QuestionContext{ID: "original-context", Hash: strings.Repeat("b", 64), Size: 12, MediaType: "application/json", RenderInstanceID: &originalRender}
	target := json.RawMessage(`{"operation_id":"old","version_id":"v"}`)
	first, e := SemanticRequestSHA("owner", target, inv, nil)
	Expect(e).NotTo(HaveOccurred())
	before, _ := json.Marshal(inv)
	other := inv
	managed := *inv.Question
	other.Question = &managed
	context := *inv.Question.Context
	other.Question.Context = &context
	other.Question.Context.ID = "different-context"
	newRender := "11111111-1111-4111-8111-111111111115"
	other.Question.Context.RenderInstanceID = &newRender
	other.Question.QuestionID = "different-question"
	other.CWD = "different-physical-cwd"
	second, e := SemanticRequestSHA("owner", json.RawMessage(`{"operation_id":"new","version_id":"v"}`), other, nil)
	Expect(e).NotTo(HaveOccurred())
	Expect(second).To(Equal(first))
	after, _ := json.Marshal(inv)
	Expect(after).To(Equal(before))
	other.Question.Context.Hash = strings.Repeat("c", 64)
	changedContext, e := SemanticRequestSHA("owner", target, other, nil)
	Expect(e).NotTo(HaveOccurred())
	Expect(changedContext).NotTo(Equal(first))
	other.Question.Context.Hash = inv.Question.Context.Hash
	other.Question.PageQuery = "different-selection"
	third, e := SemanticRequestSHA("owner", target, other, nil)
	Expect(e).NotTo(HaveOccurred())
	Expect(third).NotTo(Equal(first))
})

var _ = It("T5QDRIVER01 typed question uses actual read-only native permission and complete-frame ACK without generation output or a skill invocation", Label("integration"), func() {
	d, a, inv, store, data := questionFrames()
	controlled, _, _, _ := fixture("complete")
	script, e := os.ReadFile(controlled.Binary)
	Expect(e).NotTo(HaveOccurred())
	text := strings.Replace(string(script), "assert r['params']['cwd']==os.path.dirname(sys.argv[0])", "assert os.path.basename(r['params']['cwd']).startswith('question-')", 1)
	text = strings.Replace(text, "elif method=='thread/start':result=", "elif method=='thread/start':\n  assert r['params']['sandbox']=='read-only' and r['params']['approvalPolicy']=='never'\n  result=", 1)
	text = strings.Replace(text, "assert r['params']['input'][2]['text']=='Exact source: π\\n'", `assert all(x['type']=='text' for x in r['params']['input'])
  assert 'sandboxPolicy' not in r['params']
  assert r['params']['input'][0]['text']=='Whole saved version question π'
  metadata=[json.loads(x['text']) for x in r['params']['input'] if x['text'].startswith('{')]
  assert len([m for m in metadata if m.get('basis')=='explanation_file'])==68`, 1)
	Expect(os.WriteFile(controlled.Binary, []byte(text), 0700)).To(Succeed())
	sum := sha256.Sum256([]byte(text))
	d.Binary = controlled.Binary
	d.BinarySHA = hex.EncodeToString(sum[:])
	d.Bytes = data
	acked := false
	out, e := d.Execute(context.Background(), a, nil, func() error { return nil }, func(p work.ProviderProgress) error { acked = true; Expect(p.Observed).To(BeEmpty()); return nil })
	Expect(e).NotTo(HaveOccurred())
	Expect(out.State).To(Equal("result_saved"))
	Expect(acked).To(BeTrue())
	Expect(out.Body).NotTo(BeNil())
	defer out.Body.Close()
	body, e := io.ReadAll(out.Body)
	Expect(e).NotTo(HaveOccurred())
	Expect(string(body)).To(Equal("Grounded final answer"))
	Expect(out.Transport.Generation).To(BeNil())
	Expect(out.Transport.Question).NotTo(BeNil())
	Expect(out.Transport.Question.PromptInputAcknowledged).To(BeTrue())
	Expect(out.Transport.Question.ExplanationInputAcknowledged).To(BeTrue())
	Expect(out.Transport.Sandbox).To(Equal("read-only"))
	Expect(store.tx.metadata.Question.QuestionID).To(Equal(inv.Question.QuestionID))
	Expect(out.LocalClosed).To(BeTrue())
})

var _ = It("T5QFRAME05 runtime region without declared metadata supplies exact page and complete context with honest optional enrichment", Label("unit"), func() {
	for _, page := range []string{"pages/02.html", "pages/03.html"} {
		d, a, inv, store, data := questionFrames()
		region := "same-runtime-region"
		var selected domain.Entry
		for _, entry := range store.question.entries {
			if entry.Path == page {
				selected = entry
			}
		}
		inv.Question.PagePath = &page
		inv.Question.RegionID = &region
		inv.Question.PageQuery = "captured=1"
		inv.Question.PageFragment = "same-runtime-region"
		inv.Question.Explanation.ExplanationReference.PagePath = &page
		inv.Question.Explanation.ExplanationReference.RegionID = &region
		inv.Question.Explanation.Hash = selected.Hash
		inv.Question.Explanation.ObjectHash = selected.Hash
		inv.Question.Explanation.Size = selected.Size
		complete := []byte(strings.Repeat("complete selected context α ", 4096))
		sum := sha256.Sum256(complete)
		hash := hex.EncodeToString(sum[:])
		data[hash] = complete
		store.tx.objects[hash] = domain.Object{Hash: hash, Size: int64(len(complete)), State: "ready"}
		inv.Question.Context = &domain.QuestionContext{Hash: hash, Size: int64(len(complete)), MediaType: "application/json"}
		parts := questionFrameParts(d, a, inv)
		regions, files, contexts := 0, 0, 0
		for i, part := range parts {
			var m map[string]any
			if json.Unmarshal([]byte(part["text"].(string)), &m) != nil {
				continue
			}
			switch m["basis"] {
			case "explanation_region":
				regions++
				Expect(m["page_path"]).To(Equal(page))
				Expect(m["region_id"]).To(Equal(region))
				Expect(m["metadata_status"]).To(Equal("not_declared"))
				Expect(m["metadata"]).To(BeNil())
			case "explanation_file":
				files++
				Expect(m["path"]).To(Equal(page))
				Expect(parts[i+1]["text"]).To(Equal(string(data[selected.Hash])))
			case "captured_display_context":
				contexts++
				Expect(parts[i+1]["text"]).To(Equal(string(complete)))
			}
		}
		Expect(regions).To(Equal(1))
		Expect(files).To(Equal(1))
		Expect(contexts).To(Equal(1))
	}
})
