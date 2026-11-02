package importer_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/ai/codex"
	"haeryong.local/haeryong/internal/adapters/gitreader"
	"haeryong.local/haeryong/internal/application/questions"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

func askFixture(f *fixture) (*questions.Service, string, questions.Request) {
	_, version := f.imported(GinkgoT(), f.sealed(GinkgoT(), nil, "<html><body>exact explanation</body></html>"), key(GinkgoT()))
	sources := source.New(f.store, f.auth, f.store, gitreader.New())
	s := questions.New(f.store, f.auth, sources, codex.SemanticRequestSHA)
	page := "hello.html"
	return s, version, questions.Request{Question: "Explain this captured page without inventing source verification.", PagePath: &page, ProviderID: "codex", SourcesPresent: true, Sources: source.CaptureRequest{Selections: []source.CaptureSelection{}}}
}
func askStart(s *questions.Service, f *fixture, version string, input questions.Request) (string, string) {
	r, e := s.Start(ctx, f.token, key(GinkgoT()), version, input)
	Expect(e).NotTo(HaveOccurred())
	Expect(r.Status).To(Equal(202))
	var accepted map[string]string
	Expect(json.Unmarshal(r.Body, &accepted)).To(Succeed())
	return accepted["question_id"], accepted["job_id"]
}
func answerBytes(state, body string) []byte {
	b, e := json.Marshal(domain.AnswerEnvelope{State: state, Body: body, Citations: []domain.QuestionCitation{}, ExplanationReferences: []domain.ExplanationReference{}, Limitations: []domain.QuestionLimitation{}})
	Expect(e).NotTo(HaveOccurred())
	return b
}

var _ = It("T5QA01 accepted question freezes complete text and anchor while same-key replay never creates another job", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	s, v, input := askFixture(f)
	input.Question = strings.Repeat("complete UTF-8 question 한글 ", 4096)
	input.PageQuery = "mode=selected"
	input.PageFragment = "captured-anchor"
	input.DisplayState = json.RawMessage(`{"visible":"complete original display","scroll":42}`)
	k := key(GinkgoT())
	first, e := s.Start(ctx, f.token, k, v, input)
	Expect(e).NotTo(HaveOccurred())
	second, e := s.Start(ctx, f.token, k, v, input)
	Expect(e).NotTo(HaveOccurred())
	Expect(second.Status).To(Equal(first.Status))
	Expect(second.Body).To(Equal(first.Body))
	var accepted map[string]string
	Expect(json.Unmarshal(first.Body, &accepted)).To(Succeed())
	q, e := s.Question(ctx, f.token, accepted["question_id"])
	Expect(e).NotTo(HaveOccurred())
	Expect(q.PageQuery).To(Equal(input.PageQuery))
	Expect(q.PageFragment).To(Equal(input.PageFragment))
	Expect(q.Context).NotTo(BeNil())
	var text strings.Builder
	Expect(s.ReadText(ctx, f.token, q.ID, false, func(_ domain.Object, r io.Reader) error { _, e := io.Copy(&text, r); return e })).To(Succeed())
	Expect(text.String()).To(Equal(input.Question))
	input.Question = "changed under original K"
	_, e = s.Start(ctx, f.token, k, v, input)
	Expect(errors.Is(e, domain.ErrConflict)).To(BeTrue())
	page, e := s.Questions(ctx, f.token, v, "")
	Expect(e).NotTo(HaveOccurred())
	Expect(page.Items).To(HaveLen(1))
})

var _ = It("T5QA02 complete and partial answers retain producing attempt and saved body while read and restart execute no provider", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	s, v, input := askFixture(f)
	for _, state := range []string{"final", "partial"} {
		qid, jid := askStart(s, f, v, input)
		body := strings.Repeat(state+" explanation-only body ", 2048)
		d := &generationDriver{data: answerBytes(state, body)}
		p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: d, Prepare: s.Prepare, Apply: s.Apply}
		Expect(p.Process(ctx, jid)).To(Succeed())
		Expect(p.StopProviders()).To(Succeed())
		Expect(d.calls.Load()).To(Equal(int32(1)))
		j, e := f.store.Job(ctx, jid)
		Expect(e).NotTo(HaveOccurred())
		Expect(j.State).To(Equal("completed"))
		answers, e := s.Answers(ctx, f.token, qid, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(answers.Items).To(HaveLen(1))
		a := answers.Items[0]
		Expect(a.State).To(Equal(state))
		Expect(a.Citations).To(BeEmpty())
		Expect(a.Limitations).To(ContainElement(domain.QuestionLimitation{Code: "unable_to_verify", Message: "No project source bytes were selected. Explanation context does not establish project-source verification."}))
		Expect(f.closeStore()).To(Succeed())
		_, closeStore, e := f.store.Prepare(ctx, f.profile)
		Expect(e).NotTo(HaveOccurred())
		f.closeStore = closeStore
		var saved strings.Builder
		Expect(s.ReadText(ctx, f.token, a.ID, true, func(_ domain.Object, r io.Reader) error { _, e := io.Copy(&saved, r); return e })).To(Succeed())
		Expect(saved.String()).To(Equal(body))
		Expect(d.calls.Load()).To(Equal(int32(1)))
	}
})

var _ = It("T5QA03 prepared question refuses retry without mutation and saved continue applies the same answer with Execute zero", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	s, v, input := askFixture(f)
	qid, jid := askStart(s, f, v, input)
	d := &generationDriver{data: answerBytes("final", "A complete retained answer.")}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: d, Prepare: s.Prepare, Apply: func(app.WriteTx, domain.ProviderAttempt) error { return domain.ErrDenied }}
	Expect(p.Process(ctx, jid)).To(Succeed())
	Expect(p.StopProviders()).To(Succeed())
	j, e := f.store.Job(ctx, jid)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	var beforeQ domain.Question
	var beforeAttempts []domain.ProviderAttempt
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		var e error
		beforeQ, e = tx.Question(qid)
		if e != nil {
			return e
		}
		beforeAttempts, e = tx.ProviderAttempts(jid)
		return e
	})).To(Succeed())
	Expect(beforeQ.PreparedAnswer).NotTo(BeNil())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { _, e := tx.ResumeProviderJob(jid, j.AttemptGeneration, "retry"); return e })).To(MatchError(domain.ErrTransition))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		q, e := tx.Question(qid)
		if e != nil {
			return e
		}
		a, e := tx.ProviderAttempts(jid)
		if e != nil {
			return e
		}
		current, e := tx.Job(jid)
		if e != nil {
			return e
		}
		Expect(reflect.DeepEqual(q, beforeQ)).To(BeTrue())
		Expect(a).To(Equal(beforeAttempts))
		Expect(current).To(Equal(j))
		_, e = tx.ResumeProviderJob(jid, j.AttemptGeneration, "continue")
		return e
	})).To(Succeed())
	p = &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Prepare: s.Prepare, Apply: s.Apply}
	Expect(p.Process(ctx, jid)).To(Succeed())
	Expect(p.StopProviders()).To(Succeed())
	Expect(d.calls.Load()).To(Equal(int32(1)))
	answers, e := s.Answers(ctx, f.token, qid, "")
	Expect(e).NotTo(HaveOccurred())
	Expect(answers.Items).To(HaveLen(1))
	Expect(answers.Items[0].ID).To(Equal(beforeQ.PreparedAnswer.ID))
	Expect(answers.Items[0].AttemptID).To(Equal(beforeAttempts[0].ID))
})

var _ = It("T5QA04 malformed final or fabricated observed citation saves error history without completing the question", Label("persistence"), func() {
	citation := func(output map[string]any) map[string]any { return output["citations"].([]any)[0].(map[string]any) }
	cases := []struct {
		name   string
		change func(map[string]any)
	}{
		{name: "complete typed control"},
		{name: "duplicate root state"},
		{name: "fabricated observed read", change: func(output map[string]any) { citation(output)["read_id"] = "invented" }},
		{name: "extra citation label", change: func(output map[string]any) { citation(output)["id"] = "S1" }},
		{name: "extra citation locators", change: func(output map[string]any) { citation(output)["locators"] = []string{"selected.txt:1"} }},
		{name: "missing citation basis", change: func(output map[string]any) { delete(citation(output), "basis") }},
		{name: "missing explicit verification null", change: func(output map[string]any) { delete(citation(output), "verification_id") }},
		{name: "missing input origin", change: func(output map[string]any) { delete(citation(output), "input_origin") }},
		{name: "outer explanation metadata", change: func(output map[string]any) {
			output["explanation_references"].([]any)[0].(map[string]any)["basis"] = "explanation_context"
		}},
		{name: "string limitation", change: func(output map[string]any) { output["limitations"] = []string{"Runtime evidence unavailable."} }},
	}
	for index, test := range cases {
		// Every malformed output starts from a valid envelope with a real
		// producing-attempt observation, so rejection is not masked by a
		// missing source read. Each fixture has its own unresolved fence.
		f := newFixture(GinkgoT())
		s, v, input := askFixture(f)
		questionSource(f, s, &input)
		input.Question = "Independent output contract control " + strconv.Itoa(index)
		qid, jid := askStart(s, f, v, input)
		q, e := s.Question(ctx, f.token, qid)
		Expect(e).NotTo(HaveOccurred())
		limit := domain.QuestionLimitation{Code: "unable_to_verify", Message: "Current runtime behavior is not established by this selected source."}
		body := "This controlled answer cites the exact supplied project file and qualifies missing runtime evidence."
		d := &questionCitationDriver{f: f, observe: true, body: body, alter: func(raw []byte) []byte {
			var output map[string]any
			Expect(json.Unmarshal(raw, &output)).To(Succeed())
			refs, marshalError := json.Marshal([]domain.ExplanationReference{q.Explanation})
			Expect(marshalError).NotTo(HaveOccurred())
			var references []any
			Expect(json.Unmarshal(refs, &references)).To(Succeed())
			output["explanation_references"] = references
			output["limitations"] = []domain.QuestionLimitation{limit}
			if test.change != nil {
				test.change(output)
			}
			changed, marshalError := json.Marshal(output)
			Expect(marshalError).NotTo(HaveOccurred())
			if test.name == "duplicate root state" {
				changed = []byte(strings.Replace(string(changed), `"state":"final"`, `"state":"final","state":"partial"`, 1))
			}
			return changed
		}}
		p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: d, Prepare: s.Prepare, Apply: s.Apply}
		processError := p.Process(ctx, jid)
		if index == 0 {
			Expect(processError).NotTo(HaveOccurred())
			Expect(p.StopProviders()).To(Succeed())
		} else {
			Expect(errors.Is(processError, questions.ErrAnswerInvalid)).To(BeTrue(), "%s actual validation diagnostic: %v", test.name, processError)
			Expect(errors.Is(p.StopProviders(), questions.ErrAnswerInvalid)).To(BeTrue())
		}
		j, e := f.store.Job(ctx, jid)
		Expect(e).NotTo(HaveOccurred())
		Expect(d.calls.Load()).To(Equal(int32(1)))
		answers, e := s.Answers(ctx, f.token, qid, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(answers.Items).To(HaveLen(1))
		if index == 0 {
			Expect(j.State).To(Equal("completed"))
			answer := answers.Items[0]
			Expect(answer.State).To(Equal("final"))
			Expect(answer.Citations).To(HaveLen(1))
			Expect(answer.Citations[0].ObservedAt).NotTo(BeNil())
			Expect(answer.ExplanationReferences).To(Equal([]domain.ExplanationReference{q.Explanation}))
			Expect(answer.Limitations).To(ContainElement(limit))
			var saved strings.Builder
			Expect(s.ReadText(ctx, f.token, answer.ID, true, func(_ domain.Object, r io.Reader) error { _, e := io.Copy(&saved, r); return e })).To(Succeed())
			Expect(saved.String()).To(Equal(body))
		} else {
			Expect(j.State).To(Equal("paused"))
			Expect(answers.Items[0].State).To(Equal("error"))
			Expect(answers.Items[0].Citations).To(BeEmpty())
			Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
				attempts, e := tx.ProviderAttempts(jid)
				Expect(attempts).To(HaveLen(1))
				Expect(attempts[0].State).To(Equal("result_saved"))
				Expect(attempts[0].ResultHash).NotTo(BeEmpty())
				return e
			})).To(Succeed())
			Expect(f.store.Write(ctx, func(tx app.WriteTx) error { q, e := tx.Question(qid); Expect(q.PreparedAnswer).To(BeNil()); return e })).To(Succeed())
		}
		Expect(d.calls.Load()).To(Equal(int32(1)))
	}
})

var _ = It("T5QA05 unprepared question retry remains explicit and creates a distinct producing attempt", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	s, v, input := askFixture(f)
	qid, jid := askStart(s, f, v, input)
	d := &generationDriver{data: answerBytes("final", "Valid after explicit retry.")}
	failure := errors.New("controlled preparation interruption")
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: d, Prepare: func(context.Context, domain.ProviderAttempt) error { return failure }, Apply: s.Apply}
	Expect(errors.Is(p.Process(ctx, jid), failure)).To(BeTrue())
	Expect(errors.Is(p.StopProviders(), failure)).To(BeTrue())
	j, e := f.store.Job(ctx, jid)
	Expect(e).NotTo(HaveOccurred())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		q, e := tx.Question(qid)
		if e != nil {
			return e
		}
		Expect(q.PreparedAnswer).To(BeNil())
		_, e = tx.ResumeProviderJob(jid, j.AttemptGeneration, "retry")
		return e
	})).To(Succeed())
	p = &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: d, Prepare: s.Prepare, Apply: s.Apply}
	Expect(p.Process(ctx, jid)).To(Succeed())
	Expect(p.StopProviders()).To(Succeed())
	Expect(d.calls.Load()).To(Equal(int32(2)))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		attempts, e := tx.ProviderAttempts(jid)
		Expect(attempts).To(HaveLen(2))
		if len(attempts) == 2 {
			Expect(attempts[0].ID).NotTo(Equal(attempts[1].ID))
		}
		return e
	})).To(Succeed())
})

var _ = It("T5QA06 request decoder rejects nested duplicate fields and retains complete uncapped question and display state", Label("unit"), func() {
	text := strings.Repeat("large context ", 4096)
	raw, e := json.Marshal(map[string]any{"question": text, "page_path": nil, "region_id": nil, "display_state": map[string]any{"content": text}})
	Expect(e).NotTo(HaveOccurred())
	input, e := questions.DecodeRequest(strings.NewReader(string(raw)))
	Expect(e).NotTo(HaveOccurred())
	Expect(input.Question).To(Equal(text))
	Expect(string(input.DisplayState)).To(ContainSubstring(text))
	for _, raw := range []string{`{"question":"x","page_path":null,"region_id":null,"display_state":{"x":1,"x":2}}`, `{"question":"x","page_path":null,"region_id":null,"body_ref":{}}`, `{"question":"x","page_path":null,"region_id":null,"unknown":true}`} {
		_, e := questions.DecodeRequest(strings.NewReader(raw))
		Expect(e).To(HaveOccurred())
	}
})
