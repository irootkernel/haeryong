package importer_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	httpimports "haeryong.local/haeryong/internal/adapters/http/imports"
	"haeryong.local/haeryong/internal/application/questions"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("T5QA07 real authenticated HTTP exposes captured question scope and saved answer streams without dispatch on reopen", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	s, v, input := askFixture(f)
	handler, e := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{"127.0.0.1:9270"}, Origins: []string{"http://127.0.0.1:9270"}}, httpimports.NewQuestions(f.core, nil, s))
	Expect(e).NotTo(HaveOccurred())
	input.Question = strings.Repeat("complete HTTP question ", 2048)
	input.DisplayState = json.RawMessage(`{"selected":"exact owner-only saved context"}`)
	body, e := json.Marshal(input)
	Expect(e).NotTo(HaveOccurred())
	k := key(GinkgoT())
	accepted := generationHTTPRequest(handler, f, "POST", "/api/v1/versions/"+v+"/questions", k, "", body)
	Expect(accepted.Code).To(Equal(202), accepted.Body.String())
	var created struct {
		Data map[string]string `json:"data"`
	}
	Expect(json.Unmarshal(accepted.Body.Bytes(), &created)).To(Succeed())
	qid, jid := created.Data["question_id"], created.Data["job_id"]
	d := &generationDriver{data: answerBytes("partial", "An explicitly partial HTTP answer.")}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: d, Prepare: s.Prepare, Apply: s.Apply}
	Expect(p.Process(ctx, jid)).To(Succeed())
	Expect(p.StopProviders()).To(Succeed())
	read := generationHTTPRequest(handler, f, "GET", "/api/v1/questions/"+qid, "", "", nil)
	Expect(read.Code).To(Equal(200), read.Body.String())
	var saved struct {
		Data questions.QuestionView `json:"data"`
	}
	Expect(json.Unmarshal(read.Body.Bytes(), &saved)).To(Succeed())
	Expect(saved.Data.TargetSetID).To(BeNil())
	Expect(string(saved.Data.Targets)).To(Equal("[]"))
	Expect(saved.Data.RequestedSelection).NotTo(BeEmpty())
	Expect(saved.Data.BodyRef.Size).To(Equal(strconv.Itoa(len(input.Question))))
	Expect(saved.Data.Answers).NotTo(BeNil())
	Expect(saved.Data.Answers.Items).To(HaveLen(1))
	Expect(saved.Data.Answers.Items[0].State).To(Equal("partial"))
	text := generationHTTPRequest(handler, f, "GET", saved.Data.TextURL, "", "", nil)
	Expect(text.Code).To(Equal(200))
	Expect(text.Body.String()).To(Equal(input.Question))
	a := saved.Data.Answers.Items[0]
	answer := generationHTTPRequest(handler, f, "GET", a.BodyURL, "", "", nil)
	Expect(answer.Code).To(Equal(200))
	Expect(answer.Body.String()).To(Equal("An explicitly partial HTTP answer."))
	Expect(answer.Header().Get("ETag")).To(Equal(`"sha256-` + a.BodyRef.Hash + `"`))
	head := generationHTTPRequest(handler, f, "HEAD", a.BodyURL, "", "", nil)
	Expect(head.Code).To(Equal(200))
	Expect(head.Body.Len()).To(BeZero())
	unauthorized := httptest.NewRequest("GET", "http://127.0.0.1:9270"+a.BodyURL, nil)
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, unauthorized)
	Expect(denied.Code).To(Equal(401))
	// A normal authenticated workflow can read the version, but not owner Q&A.
	// Known and missing IDs must refuse identically before record lookup.
	scoped := f.seedScoped(GinkgoT(), domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read", "project.read"}, ProjectIDs: []string{f.project}, VersionIDs: []string{v}})
	_, e = f.auth.Authenticate(ctx, scoped)
	Expect(e).NotTo(HaveOccurred())
	missingQuestion, missingAnswer := key(GinkgoT()), key(GinkgoT())
	for _, ids := range [][2]string{{qid, a.ID}, {missingQuestion, missingAnswer}} {
		paths := []string{"/versions/" + v + "/questions", "/questions/" + ids[0], "/questions/" + ids[0] + "/answers", "/questions/" + ids[0] + "/text", "/questions/" + ids[0] + "/context", "/answers/" + ids[1], "/answers/" + ids[1] + "/body"}
		for _, path := range paths {
			for _, method := range []string{"GET", "HEAD"} {
				request := httptest.NewRequest(method, "http://127.0.0.1:9270/api/v1"+path, nil)
				request.Header.Set("Authorization", "Bearer "+scoped)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				Expect(response.Code).To(Equal(403), method+" "+path)
				Expect(response.Body.String()).NotTo(ContainSubstring(input.Question))
				noAuth := httptest.NewRequest(method, "http://127.0.0.1:9270/api/v1"+path, nil)
				response = httptest.NewRecorder()
				handler.ServeHTTP(response, noAuth)
				Expect(response.Code).To(Equal(401))
			}
		}
		_, e = s.Question(ctx, scoped, ids[0])
		Expect(errors.Is(e, domain.ErrDenied)).To(BeTrue())
		_, e = s.Answers(ctx, scoped, ids[0], "")
		Expect(errors.Is(e, domain.ErrDenied)).To(BeTrue())
		_, e = s.Answer(ctx, scoped, ids[1])
		Expect(errors.Is(e, domain.ErrDenied)).To(BeTrue())
		consumed := false
		for _, text := range []struct {
			id     string
			answer bool
		}{{ids[0], false}, {ids[1], true}} {
			e = s.ReadText(ctx, scoped, text.id, text.answer, func(domain.Object, io.Reader) error { consumed = true; return nil })
			Expect(errors.Is(e, domain.ErrDenied)).To(BeTrue())
		}
		e = s.ReadContext(ctx, scoped, ids[0], func(domain.QuestionContext, domain.Object, io.Reader) error { consumed = true; return nil })
		Expect(errors.Is(e, domain.ErrDenied)).To(BeTrue())
		Expect(consumed).To(BeFalse())
	}
	for _, path := range []string{"/questions/" + missingQuestion, "/answers/" + missingAnswer, "/questions/" + missingQuestion + "/text", "/questions/" + missingQuestion + "/context", "/answers/" + missingAnswer + "/body"} {
		missing := generationHTTPRequest(handler, f, "GET", "/api/v1"+path, "", "", nil)
		Expect(missing.Code).To(Equal(404), path)
	}
	context := generationHTTPRequest(handler, f, "GET", "/api/v1/questions/"+qid+"/context", "", "", nil)
	Expect(context.Code).To(Equal(200))
	Expect(context.Body.String()).To(Equal(string(input.DisplayState)))
	Expect(d.calls.Load()).To(Equal(int32(1)))
})

var _ = It("T5QA19 authenticated citation streams preserve exact retained bytes and current source denial without changing observation basis", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	s, v, input := askFixture(f)
	sourceID, root := questionSource(f, s, &input)
	handler, e := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{"127.0.0.1:9270"}, Origins: []string{"http://127.0.0.1:9270"}}, httpimports.NewQuestions(f.core, nil, s))
	Expect(e).NotTo(HaveOccurred())
	qid, jid := askStart(s, f, v, input)
	driver := &questionCitationDriver{f: f, observe: true, body: "No compulsory source quote is present in this valid answer."}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: s.Prepare, Apply: s.Apply}
	Expect(p.Process(ctx, jid)).To(Succeed())
	Expect(p.StopProviders()).To(Succeed())
	answers, e := s.Answers(ctx, f.token, qid, "")
	Expect(e).NotTo(HaveOccurred())
	answer := answers.Items[0]
	endpoint := "/api/v1/answers/" + answer.ID + "/citations/0/source"
	response := generationHTTPRequest(handler, f, "GET", endpoint, "", "", nil)
	Expect(response.Code).To(Equal(200), response.Body.String())
	Expect(response.Body.String()).To(Equal("exact selected project source\nsecond line\n"))
	Expect(response.Header().Get("ETag")).To(Equal(`"sha256-` + answer.Citations[0].Hash + `"`))
	Expect(response.Header().Get("Content-Length")).To(Equal(strconv.Itoa(response.Body.Len())))
	Expect(response.Header().Get("Content-Type")).To(Equal("application/octet-stream"))
	Expect(response.Header().Get("X-Content-Type-Options")).To(Equal("nosniff"))
	head := generationHTTPRequest(handler, f, "HEAD", endpoint, "", "", nil)
	Expect(head.Code).To(Equal(200))
	Expect(head.Body.Len()).To(BeZero())
	for _, index := range []string{"-1", "01", "1", "not-an-index"} {
		r := generationHTTPRequest(handler, f, "GET", "/api/v1/answers/"+answer.ID+"/citations/"+index+"/source", "", "", nil)
		Expect(r.Code).NotTo(Equal(200))
	}
	// Withdrawing only AI disclosure does not withdraw the owner's retained read.
	questionMapping(f, sourceID, root, false)
	response = generationHTTPRequest(handler, f, "GET", endpoint, "", "", nil)
	Expect(response.Code).To(Equal(200))
	// Actual source authority loss applies to every chunk, even after open.
	Expect(s.ReadCitation(ctx, f.token, answer.ID, 0, func(_ domain.SourceCaptureRead, r io.Reader) error {
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			cfg, e := tx.Configuration()
			if e != nil {
				return e
			}
			var value map[string]any
			if e = json.Unmarshal(cfg.Value, &value); e != nil {
				return e
			}
			value["projects"] = map[string]any{}
			raw, e := json.Marshal(value)
			if e != nil {
				return e
			}
			return tx.SetConfiguration(domain.Configuration{Revision: cfg.Revision, Value: raw, Actor: "controlled source withdrawal"}, cfg.Revision)
		})).To(Succeed())
		got, e := io.ReadAll(r)
		Expect(got).To(BeEmpty())
		Expect(e).To(HaveOccurred())
		return nil
	})).To(Succeed())
	for _, method := range []string{"GET", "HEAD"} {
		r := generationHTTPRequest(handler, f, method, endpoint, "", "", nil)
		Expect(r.Code).To(Equal(403))
		Expect(r.Body.String()).NotTo(ContainSubstring("exact selected project source"))
	}
	current, e := s.Answer(ctx, f.token, answer.ID)
	Expect(e).NotTo(HaveOccurred())
	Expect(current.Citations[0].Basis).To(Equal("observed_input"))
	Expect(current.Citations[0].ReadID).To(Equal(answer.Citations[0].ReadID))
	Expect(current.Citations[0].Availability).To(Equal("denied"))
	Expect(driver.calls.Load()).To(Equal(int32(1)))
})

var _ = It("T5QA20 HTTP admission refusal carries exact original key runtime and owner only for new source-policy normalization", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	s, v, input := askFixture(f)
	sourceID, root := questionSource(f, s, &input)
	questionMapping(f, sourceID, root, false)
	handler, e := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{"127.0.0.1:9270"}, Origins: []string{"http://127.0.0.1:9270"}}, httpimports.NewQuestions(f.core, nil, s))
	Expect(e).NotTo(HaveOccurred())
	body, e := json.Marshal(input)
	Expect(e).NotTo(HaveOccurred())
	k := key(GinkgoT())
	response := generationHTTPRequest(handler, f, "POST", "/api/v1/versions/"+v+"/questions", k, "", body)
	Expect(response.Code).To(Equal(403))
	var refused struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	Expect(json.Unmarshal(response.Body.Bytes(), &refused)).To(Succeed())
	Expect(refused.Error.Details["question_admission"]).To(Equal("refused_before_admission"))
	Expect(refused.Error.Details["operation_id"]).To(Equal(k))
	actor, e := f.auth.Authenticate(ctx, f.token)
	Expect(e).NotTo(HaveOccurred())
	Expect(refused.Error.Details["principal_id"]).To(Equal(actor.PrincipalID))
	Expect(refused.Error.Details["runtime_generation"]).NotTo(BeEmpty())
	operation := generationHTTPRequest(handler, f, "GET", "/api/v1/operations/"+k, "", "", nil)
	Expect(operation.Code).To(Equal(200), operation.Body.String())
	var terminal struct {
		Data struct {
			State    string          `json:"state"`
			Status   int             `json:"original_status"`
			Metadata json.RawMessage `json:"result_metadata"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(operation.Body.Bytes(), &terminal)).To(Succeed())
	Expect(terminal.Data.State).To(Equal("failed"))
	Expect(terminal.Data.Status).To(Equal(403))
	Expect(string(terminal.Data.Metadata)).To(ContainSubstring(`"question_admission":"refused_before_admission"`))
	questionMapping(f, sourceID, root, true)
	replay := generationHTTPRequest(handler, f, "POST", "/api/v1/versions/"+v+"/questions", k, "", body)
	Expect(replay.Code).To(Equal(403))
	var envelope map[string]json.RawMessage
	Expect(json.Unmarshal(replay.Body.Bytes(), &envelope)).To(Succeed())
	Expect(envelope).To(HaveKey("error"))
	Expect(envelope).NotTo(HaveKey("data"))

	input.RegionID = new(string)
	body, e = json.Marshal(input)
	Expect(e).NotTo(HaveOccurred())
	invalid := generationHTTPRequest(handler, f, "POST", "/api/v1/versions/"+v+"/questions", key(GinkgoT()), "", body)
	Expect(invalid.Code).To(Equal(400))
	Expect(invalid.Body.String()).NotTo(ContainSubstring("refused_before_admission"))
})

type countingQuestionFiles struct {
	source.FileReader
	opens int
}

func (f *countingQuestionFiles) Open(ctx context.Context, root, path string) (io.ReadCloser, error) {
	f.opens++
	return f.FileReader.Open(ctx, root, path)
}

var _ = It("T5QA21 reference-only citation remains honestly not retained and source inspection refuses without re-collection", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	s, v, input := askFixture(f)
	sourceID, root := questionSource(f, s, &input)
	questionRetentionMapping(f, sourceID, root, true, "reference_only")
	counted := &countingQuestionFiles{FileReader: s.Sources.Files}
	s.Sources.Files = counted
	qid, jid := askStart(s, f, v, input)
	Expect(counted.opens).To(Equal(1))
	driver := &questionCitationDriver{f: f, observe: true}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: s.Prepare, Apply: s.Apply}
	Expect(p.Process(ctx, jid)).To(Succeed())
	Expect(p.StopProviders()).To(Succeed())
	answers, e := s.Answers(ctx, f.token, qid, "")
	Expect(e).NotTo(HaveOccurred())
	Expect(answers.Items).To(HaveLen(1))
	answer := answers.Items[0]
	Expect(answer.State).To(Equal("final"))
	Expect(answer.Citations).To(HaveLen(1))
	citation := answer.Citations[0]
	Expect(citation.Basis).To(Equal("observed_input"))
	Expect(citation.Availability).To(Equal("unavailable"))
	Expect(citation.Reason).NotTo(BeNil())
	Expect(*citation.Reason).To(Equal("not_retained"))
	Expect(citation.BytesOrigin).To(BeNil())
	before := counted.opens
	called := false
	e = s.ReadCitation(ctx, f.token, answer.ID, 0, func(domain.SourceCaptureRead, io.Reader) error { called = true; return nil })
	Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue())
	Expect(called).To(BeFalse())
	Expect(counted.opens).To(Equal(before))
	handler, e := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{"127.0.0.1:9270"}, Origins: []string{"http://127.0.0.1:9270"}}, httpimports.NewQuestions(f.core, nil, s))
	Expect(e).NotTo(HaveOccurred())
	for _, method := range []string{"GET", "HEAD"} {
		r := generationHTTPRequest(handler, f, method, "/api/v1/answers/"+answer.ID+"/citations/0/source", "", "", nil)
		Expect(r.Code).To(Equal(404))
		Expect(counted.opens).To(Equal(before))
	}
	Expect(driver.calls.Load()).To(Equal(int32(1)))
})
