package importer_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	importer "haeryong.local/haeryong/internal/application/import"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	sourcefiles "haeryong.local/haeryong/internal/adapters/source"
	"haeryong.local/haeryong/internal/application/questions"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

func questionMapping(f *fixture, id, root string, allowed bool) {
	questionRetentionMapping(f, id, root, allowed, "selected_snapshot")
}
func questionRetentionMapping(f *fixture, id, root string, allowed bool, retention string) {
	providers := []string{}
	if allowed {
		providers = append(providers, "codex")
	}
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var value map[string]any
		if e = json.Unmarshal(cfg.Value, &value); e != nil {
			return e
		}
		value["projects"] = map[string]any{f.project: map[string]any{"sources": []any{map[string]any{"source_id": id, "kind": "files", "root_path": root, "repo_id": nil, "retention_mode": retention, "provider_ids": providers}}}}
		raw, e := json.Marshal(value)
		if e != nil {
			return e
		}
		return tx.SetConfiguration(domain.Configuration{Revision: cfg.Revision, Value: raw, Actor: "controlled owner configuration"}, cfg.Revision)
	})).To(Succeed())
}
func questionSource(f *fixture, s *questions.Service, input *questions.Request) (string, string) {
	root := GinkgoT().TempDir()
	id := key(GinkgoT())
	Expect(os.WriteFile(filepath.Join(root, "selected.txt"), []byte("exact selected project source\nsecond line\n"), 0600)).To(Succeed())
	questionMapping(f, id, root, true)
	s.Sources.Files = sourcefiles.Files{ExcludedPaths: []string{f.profile.DataDir}}
	input.Sources = source.CaptureRequest{SourceMode: "current", Selections: []source.CaptureSelection{{SourceID: id, Paths: []source.CapturePath{{Path: "selected.txt"}}}}}
	input.SourcesPresent = true
	return id, root
}

type questionCitationDriver struct {
	f       *fixture
	observe bool
	body    string
	calls   atomic.Int32
	alter   func([]byte) []byte
}

func (d *questionCitationDriver) Execute(ctx context.Context, a domain.ProviderAttempt, inputs []domain.ProviderInput, send func() error, ack func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	d.calls.Add(1)
	if e := send(); e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	citations := []domain.QuestionCitation{}
	observed := []domain.ProviderInput{}
	for _, input := range inputs {
		var read domain.CapturedRead
		var object domain.Object
		e := d.f.store.Write(ctx, func(tx app.WriteTx) error {
			var e error
			read, e = tx.CapturedRead(input.ReadID)
			if e != nil {
				return e
			}
			object, e = tx.Object(input.Hash)
			return e
		})
		if e != nil {
			return work.ProviderOutcome{LocalClosed: true}, e
		}
		if e = d.f.store.Read(ctx, object, domain.Pin{Kind: "controlled_question_delivery", OwnerID: a.ID}, func(r io.ReadSeeker) error { _, e := io.Copy(io.Discard, r); return e }); e != nil {
			return work.ProviderOutcome{LocalClosed: true}, e
		}
		origin := "daemon_supplied"
		id := input.ReadID
		citations = append(citations, domain.QuestionCitation{Basis: "observed_input", ReadID: &id, InputOrigin: &origin, TargetSetID: a.TargetSetID, Source: read.SourceJSON, Location: read.LocationJSON, Hash: input.Hash})
		if d.observe {
			observed = append(observed, domain.ProviderInput{AttemptID: a.ID, ReadID: id, ObservedOrigin: origin})
		}
	}
	if e := ack(work.ProviderProgress{RequestID: a.ID, SessionID: a.JobID, Observed: observed}); e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	answerBody := d.body
	if answerBody == "" {
		answerBody = "This controlled answer cites the exact supplied project file."
	}
	body, e := json.Marshal(domain.AnswerEnvelope{State: "final", Body: answerBody, Citations: citations, ExplanationReferences: []domain.ExplanationReference{}, Limitations: []domain.QuestionLimitation{}})
	if e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	if d.alter != nil {
		body = d.alter(body)
	}
	return work.ProviderOutcome{State: "result_saved", CompletionObserved: true, LocalClosed: true, RequestID: a.ID, SessionID: a.JobID, Body: io.NopCloser(bytes.NewReader(body))}, nil
}
func (d *questionCitationDriver) Cancel(context.Context, domain.ProviderAttempt) error {
	return domain.ErrReconciliation
}
func (d *questionCitationDriver) Reconcile(context.Context, domain.ProviderAttempt, bool) (work.ProviderOutcome, error) {
	Fail("saved question must not execute a provider lookup")
	return work.ProviderOutcome{}, domain.ErrReconciliation
}

var _ = It("T5QA08 project citation requires actual producing-attempt observation and exposes the exact captured target", Label("persistence"), func() {
	for _, observed := range []bool{false, true} {
		f := newFixture(GinkgoT())
		s, v, input := askFixture(f)
		_, rootForCitation := questionSource(f, s, &input)
		qid, jid := askStart(s, f, v, input)
		q, e := s.Question(ctx, f.token, qid)
		Expect(e).NotTo(HaveOccurred())
		Expect(q.TargetSetID).NotTo(BeNil())
		Expect(string(q.Targets)).NotTo(Equal("[]"))
		Expect(string(q.RequestedSelection)).To(ContainSubstring("selected.txt"))
		d := &questionCitationDriver{f: f, observe: observed}
		p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: d, Prepare: s.Prepare, Apply: s.Apply}
		e = p.Process(ctx, jid)
		if observed {
			Expect(e).NotTo(HaveOccurred())
			Expect(p.StopProviders()).To(Succeed())
		} else {
			Expect(errors.Is(e, questions.ErrAnswerInvalid)).To(BeTrue())
			Expect(p.StopProviders()).To(HaveOccurred())
		}
		answers, e := s.Answers(ctx, f.token, qid, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(answers.Items).To(HaveLen(1))
		a := answers.Items[0]
		if observed {
			Expect(a.State).To(Equal("final"))
			Expect(a.Citations).To(HaveLen(1))
			c := a.Citations[0]
			Expect(c.Basis).To(Equal("observed_input"))
			Expect(c.ObservedAt).NotTo(BeNil())
			Expect(c.Availability).To(Equal("available"))
			Expect(c.BytesOrigin).NotTo(BeNil())
			Expect(*c.BytesOrigin).To(Equal("retained_snapshot"))
			Expect(c.TargetSetID).To(Equal(*q.TargetSetID))
			// Inspect accepted B bytes, even after the approved workspace advances.
			Expect(os.WriteFile(filepath.Join(rootForCitation, "selected.txt"), []byte("later workspace C"), 0600)).To(Succeed())
			var full bytes.Buffer
			Expect(s.ReadCitation(ctx, f.token, a.ID, 0, func(row domain.SourceCaptureRead, reader io.Reader) error {
				Expect(row.Hash).To(Equal(c.Hash))
				_, e := io.Copy(&full, reader)
				return e
			})).To(Succeed())
			Expect(full.String()).To(Equal("exact selected project source\nsecond line\n"))
			_, e = s.Answer(ctx, f.token, a.ID)
			Expect(e).NotTo(HaveOccurred())

		} else {
			Expect(a.State).To(Equal("error"))
			Expect(a.Citations).To(BeEmpty())
		}
		Expect(d.calls.Load()).To(Equal(int32(1)))
	}
})

var _ = It("T5QA09 current source denial pauses a prepared question and explicit saved continuation after permission recovery executes zero", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	s, v, input := askFixture(f)
	id, root := questionSource(f, s, &input)
	qid, jid := askStart(s, f, v, input)
	d := &questionCitationDriver{f: f, observe: true}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: d, Prepare: func(c context.Context, a domain.ProviderAttempt) error {
		if e := s.Prepare(c, a); e != nil {
			return e
		}
		questionMapping(f, id, root, false)
		return nil
	}, Apply: s.Apply}
	Expect(p.Process(ctx, jid)).To(Succeed())
	Expect(p.StopProviders()).To(Succeed())
	j, e := f.store.Job(ctx, jid)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	var prepared domain.QuestionAnswer
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		q, e := tx.Question(qid)
		if e != nil {
			return e
		}
		Expect(q.PreparedAnswer).NotTo(BeNil())
		prepared = *q.PreparedAnswer
		_, e = tx.ResumeProviderJob(jid, j.AttemptGeneration, "retry")
		return e
	})).To(MatchError(domain.ErrTransition))
	questionMapping(f, id, root, true)
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.ResumeProviderJob(jid, j.AttemptGeneration, "continue")
		return e
	})).To(Succeed())
	p = &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Prepare: s.Prepare, Apply: s.Apply}
	Expect(p.Process(ctx, jid)).To(Succeed())
	Expect(p.StopProviders()).To(Succeed())
	Expect(d.calls.Load()).To(Equal(int32(1)))
	answers, e := s.Answers(ctx, f.token, qid, "")
	Expect(e).NotTo(HaveOccurred())
	Expect(answers.Items).To(HaveLen(1))
	Expect(answers.Items[0].ID).To(Equal(prepared.ID))
	questionMapping(f, id, root, false)
	saved, e := s.Question(ctx, f.token, qid)
	Expect(e).NotTo(HaveOccurred())
	Expect(saved.Answers.Items[0].Citations[0].Availability).To(Equal("available"), "provider disclosure does not revoke owner retained-byte reading")
	Expect(saved.Answers.Items[0].Citations[0].BytesOrigin).NotTo(BeNil())
	Expect(saved.Answers.Items[0].Citations[0].ObservedAt).To(Equal(answers.Items[0].Citations[0].ObservedAt))
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
		return tx.SetConfiguration(domain.Configuration{Revision: cfg.Revision, Value: raw, Actor: "controlled owner source removal"}, cfg.Revision)
	})).To(Succeed())
	saved, e = s.Question(ctx, f.token, qid)
	Expect(e).NotTo(HaveOccurred())
	Expect(saved.Answers.Items[0].Citations[0].Availability).To(Equal("denied"))
	Expect(saved.Answers.Items[0].Citations[0].BytesOrigin).To(BeNil())
	Expect(saved.Answers.Items[0].ID).To(Equal(prepared.ID))
	Expect(d.calls.Load()).To(Equal(int32(1)))
})

// Both identities below come from public application seams: normal import and
// Verify preserve authored source metadata, while accepted question capture
// stores the canonical managed source identity without that metadata wrapper.
var _ = It("T5QA10 normal imported Git verification corroborates only the exact accepted unobserved source identity", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	root := GinkgoT().TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "commit.gpgsign=false", "-c", "user.name=Controlled question fixture", "-c", "user.email=question@example.invalid"}, args...)...)
		b, e := cmd.CombinedOutput()
		Expect(e).NotTo(HaveOccurred(), string(b))
		return strings.TrimSpace(string(b))
	}
	git("init", "--quiet")
	sourceBytes := []byte("exact immutable project source\n")
	Expect(os.WriteFile(filepath.Join(root, "selected.txt"), sourceBytes, 0600)).To(Succeed())
	git("add", "selected.txt")
	git("commit", "--quiet", "-m", "Controlled source A")
	revision := git("rev-parse", "HEAD")
	repo, mapping := key(GinkgoT()), key(GinkgoT())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var value map[string]any
		if e = json.Unmarshal(cfg.Value, &value); e != nil {
			return e
		}
		value["projects"] = map[string]any{f.project: map[string]any{"sources": []domain.ManagedSourceMapping{{SourceID: mapping, Kind: "git", RepoID: repo, RootPath: root, RetentionMode: "selected_snapshot", ProviderIDs: []string{"codex"}}}}}
		raw, e := json.Marshal(value)
		if e != nil {
			return e
		}
		return tx.SetConfiguration(domain.Configuration{Revision: cfg.Revision, Value: raw, Actor: "controlled owner configuration"}, cfg.Revision)
	})).To(Succeed())
	identity := map[string]any{"id": "authored-source", "kind": "git", "repo_id": repo, "object_format": "sha1", "exact_revision": revision, "retention_mode": "reference_only"}
	metadata, e := json.Marshal(map[string]any{"version": map[string]any{"source_intent": "historical"}, "content": map[string]any{"default_page": "hello.html"}, "sources": []any{identity}, "evidence": []any{map[string]any{"id": "checked-source", "source_id": "authored-source", "capture_status": "reference_only", "path": "selected.txt"}}})
	Expect(e).NotTo(HaveOccurred())
	version := key(GinkgoT())
	imported, e := f.core.Import(ctx, f.token, key(GinkgoT()), importer.ImportRequest{UploadID: f.sealed(GinkgoT(), nil, "<html>exact explanation</html>"), DocumentPresent: true, VersionID: version, Metadata: bytes.NewReader(metadata)})
	Expect(e).NotTo(HaveOccurred())
	Expect(f.core.Process(ctx, decoded(GinkgoT(), imported)["job_id"])).To(Succeed())
	s, _, input := askFixture(f)
	checked, e := s.Sources.Verify(ctx, f.token, version, "checked-source", key(GinkgoT()), []byte(`{}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(checked.Status).To(Equal(201))
	var check struct {
		ID       string          `json:"verification_id"`
		Target   string          `json:"target_set_id"`
		Outcome  string          `json:"outcome"`
		Source   json.RawMessage `json:"source"`
		Location json.RawMessage `json:"location"`
		Hash     string          `json:"bytes_sha256"`
	}
	Expect(json.Unmarshal(checked.Body, &check)).To(Succeed())
	Expect(check.Outcome).To(Equal("captured"))
	hash := sha256.Sum256(sourceBytes)
	Expect(check.Hash).To(Equal(hex.EncodeToString(hash[:])))
	for _, scenario := range []string{"valid", "repo", "revision", "path", "hash", "unselected", "other-version"} {
		input.Question = "Controlled posthoc case " + scenario
		input.Sources = source.CaptureRequest{SourceMode: "pinned", Selections: []source.CaptureSelection{{SourceID: mapping, Revision: revision, Paths: []source.CapturePath{{Path: "selected.txt"}}}}}
		input.SourcesPresent = true
		selectedVersion := version
		if scenario == "other-version" {
			_, selectedVersion = f.imported(GinkgoT(), f.sealed(GinkgoT(), nil, "other explanation"), key(GinkgoT()))
		}
		if scenario == "unselected" {
			input.Sources.Selections = nil
		}
		qid, jid := askStart(s, f, selectedVersion, input)
		verification := check.ID
		citation := domain.QuestionCitation{Basis: "posthoc_verified", VerificationID: &verification, TargetSetID: check.Target, Source: append([]byte(nil), check.Source...), Location: append([]byte(nil), check.Location...), Hash: check.Hash}
		if scenario == "repo" || scenario == "revision" {
			var changed map[string]any
			Expect(json.Unmarshal(citation.Source, &changed)).To(Succeed())
			if scenario == "repo" {
				changed["repo_id"] = key(GinkgoT())
			} else {
				changed["exact_revision"] = strings.Repeat("a", 40)
			}
			citation.Source, e = json.Marshal(changed)
			Expect(e).NotTo(HaveOccurred())
		}
		if scenario == "path" {
			citation.Location = json.RawMessage(`{"path":"other.txt"}`)
		}
		if scenario == "hash" {
			citation.Hash = strings.Repeat("b", 64)
		}
		raw, e := json.Marshal(domain.AnswerEnvelope{State: "final", Body: "Controlled independently checked answer", Citations: []domain.QuestionCitation{citation}, ExplanationReferences: []domain.ExplanationReference{}, Limitations: []domain.QuestionLimitation{}})
		Expect(e).NotTo(HaveOccurred())
		driver := &generationDriver{data: raw}
		p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: s.Prepare, Apply: s.Apply}
		result := p.Process(ctx, jid)
		if scenario == "valid" {
			Expect(result).NotTo(HaveOccurred())
			Expect(p.StopProviders()).To(Succeed())
		} else {
			Expect(errors.Is(result, questions.ErrAnswerInvalid)).To(BeTrue(), scenario)
			Expect(p.StopProviders()).To(HaveOccurred())
		}
		answers, e := s.Answers(ctx, f.token, qid, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(answers.Items).To(HaveLen(1))
		answer := answers.Items[0]
		if scenario == "valid" {
			Expect(answer.State).To(Equal("final"))
			Expect(answer.Citations).To(HaveLen(1))
			c := answer.Citations[0]
			Expect(c.Basis).To(Equal("posthoc_verified"))
			Expect(c.ObservedAt).To(BeNil())
			Expect(c.InputOrigin).To(BeNil())
			Expect(c.VerifiedAt).NotTo(BeNil())
			Expect(c.Availability).To(Equal("available"))
			Expect(c.Source).To(Equal(check.Source))
			Expect(c.Location).To(Equal(check.Location))
			Expect(c.ReadID).To(BeNil())
			var full bytes.Buffer
			Expect(s.ReadCitation(ctx, f.token, answer.ID, 0, func(row domain.SourceCaptureRead, reader io.Reader) error {
				Expect(row.Hash).To(Equal(check.Hash))
				_, e := io.Copy(&full, reader)
				return e
			})).To(Succeed())
			Expect(full.Bytes()).To(Equal(sourceBytes))
			fresh, e := s.Answer(ctx, f.token, answer.ID)
			Expect(e).NotTo(HaveOccurred())
			Expect(fresh.Citations[0].ReadID).To(BeNil())
			Expect(fresh.Citations[0].ObservedAt).To(BeNil())

		} else {
			Expect(answer.State).To(Equal("error"))
			Expect(answer.Citations).To(BeEmpty())
		}
		Expect(driver.calls.Load()).To(Equal(int32(1)))
	}
	// Neither projection nor normal comparison rewrites the original record.
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		v, e := tx.Verification(check.ID)
		if e != nil {
			return e
		}
		Expect(string(v.SourceJSON)).To(Equal(string(check.Source)))
		Expect(string(v.LocationJSON)).To(Equal(string(check.Location)))
		return nil
	})).To(Succeed())
})

var _ = It("T5QA11 imported immutable historical intent overrides remembered current only when no explicit source mode was selected", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	s, _, base := askFixture(f)
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var value map[string]any
		if e = json.Unmarshal(cfg.Value, &value); e != nil {
			return e
		}
		value["projects"] = map[string]any{f.project: map[string]any{"source_mode_default": "current", "provider_id": "codex"}}
		raw, e := json.Marshal(value)
		if e != nil {
			return e
		}
		return tx.SetConfiguration(domain.Configuration{Revision: cfg.Revision, Value: raw, Actor: "controlled owner defaults"}, cfg.Revision)
	})).To(Succeed())
	for _, test := range []struct {
		intent, top, nested, expected string
		present                       bool
	}{{"historical", "", "", "pinned", false}, {"historical", "", "", "pinned", true}, {"historical", "current", "", "current", false}, {"historical", "", "current", "current", true}, {"moving_target", "", "", "current", false}, {"", "", "", "current", false}} {
		version := key(GinkgoT())
		metadata := map[string]any{"content": map[string]any{"default_page": "hello.html"}}
		if test.intent != "" {
			metadata["version"] = map[string]any{"source_intent": test.intent}
		}
		raw, e := json.Marshal(metadata)
		Expect(e).NotTo(HaveOccurred())
		r, e := f.core.Import(ctx, f.token, key(GinkgoT()), importer.ImportRequest{UploadID: f.sealed(GinkgoT(), nil, "<html>normal default question</html>"), DocumentPresent: true, VersionID: version, Metadata: bytes.NewReader(raw)})
		Expect(e).NotTo(HaveOccurred())
		Expect(f.core.Process(ctx, decoded(GinkgoT(), r)["job_id"])).To(Succeed())
		input := base
		input.SourceMode = test.top
		input.Sources = source.CaptureRequest{SourceMode: test.nested}
		input.SourcesPresent = test.present
		input.ProviderID = ""
		qid, _ := askStart(s, f, version, input)
		view, e := s.Question(ctx, f.token, qid)
		Expect(e).NotTo(HaveOccurred())
		Expect(view.SourceMode).To(Equal(test.expected))
		Expect(view.TargetSetID).To(BeNil())
		Expect(view.Limitations).To(ContainElement(HaveField("Code", "unable_to_verify")))
	}
})

var _ = It("T5QA15 producing-attempt observations cannot make a citation with an omitted required nullable union member valid", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	s, version, input := askFixture(f)
	questionSource(f, s, &input)
	qid, jid := askStart(s, f, version, input)
	driver := &questionCitationDriver{f: f, observe: true, alter: func(body []byte) []byte {
		var raw map[string]any
		Expect(json.Unmarshal(body, &raw)).To(Succeed())
		citation := raw["citations"].([]any)[0].(map[string]any)
		Expect(citation).To(HaveKey("verification_id"))
		delete(citation, "verification_id")
		changed, e := json.Marshal(raw)
		Expect(e).NotTo(HaveOccurred())
		return changed
	}}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: s.Prepare, Apply: s.Apply}
	e := p.Process(ctx, jid)
	Expect(errors.Is(e, questions.ErrAnswerInvalid)).To(BeTrue())
	Expect(p.StopProviders()).To(HaveOccurred())
	answers, e := s.Answers(ctx, f.token, qid, "")
	Expect(e).NotTo(HaveOccurred())
	Expect(answers.Items).To(HaveLen(1))
	Expect(answers.Items[0].State).To(Equal("error"))
	Expect(answers.Items[0].Citations).To(BeEmpty())
	Expect(driver.calls.Load()).To(Equal(int32(1)))
})

var _ = It("T5QA16 default question selection captures canonical imported Git evidence in pinned and current modes without explicit sources", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	root := GinkgoT().TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "commit.gpgsign=false", "-c", "user.name=Controlled question fixture", "-c", "user.email=question@example.invalid"}, args...)...)
		b, e := cmd.CombinedOutput()
		Expect(e).NotTo(HaveOccurred(), string(b))
		return strings.TrimSpace(string(b))
	}
	git("init", "--quiet")
	pinnedBytes := []byte("exact authored project source A\n")
	currentBytes := []byte("exact current project source B\n")
	Expect(os.WriteFile(filepath.Join(root, "selected.txt"), pinnedBytes, 0600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(root, "unrelated.txt"), []byte("not selected by authored evidence"), 0600)).To(Succeed())
	git("add", ".")
	git("commit", "--quiet", "-m", "Controlled authored source")
	pinnedRevision := git("rev-parse", "HEAD")
	repo, mapping := key(GinkgoT()), key(GinkgoT())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var value map[string]any
		if e = json.Unmarshal(cfg.Value, &value); e != nil {
			return e
		}
		value["projects"] = map[string]any{f.project: map[string]any{"sources": []domain.ManagedSourceMapping{{SourceID: mapping, Kind: "git", RepoID: repo, RootPath: root, RetentionMode: "selected_snapshot", ProviderIDs: []string{"codex"}}}}}
		raw, e := json.Marshal(value)
		if e != nil {
			return e
		}
		return tx.SetConfiguration(domain.Configuration{Revision: cfg.Revision, Value: raw, Actor: "controlled owner configuration"}, cfg.Revision)
	})).To(Succeed())
	metadata, e := json.Marshal(map[string]any{
		"schema_version": "1.0",
		"version":        map[string]any{"source_intent": "historical"},
		"content":        map[string]any{"default_page": "hello.html"},
		"sources":        []any{map[string]any{"id": "authored-source", "kind": "git", "repo_id": repo, "object_format": "sha1", "exact_revision": pinnedRevision, "retention_mode": "reference_only"}},
		"evidence":       []any{map[string]any{"id": "selected-source", "source_id": "authored-source", "capture_status": "reference_only", "path": "selected.txt"}},
	})
	Expect(e).NotTo(HaveOccurred())
	version := key(GinkgoT())
	imported, e := f.core.Import(ctx, f.token, key(GinkgoT()), importer.ImportRequest{UploadID: f.sealed(GinkgoT(), nil, "<html>exact explanation</html>"), DocumentPresent: true, VersionID: version, Metadata: bytes.NewReader(metadata)})
	Expect(e).NotTo(HaveOccurred())
	Expect(f.core.Process(ctx, decoded(GinkgoT(), imported)["job_id"])).To(Succeed())
	Expect(os.WriteFile(filepath.Join(root, "selected.txt"), currentBytes, 0600)).To(Succeed())
	git("add", "selected.txt")
	git("commit", "--quiet", "-m", "Controlled current source")
	currentRevision := git("rev-parse", "HEAD")
	Expect(currentRevision).NotTo(Equal(pinnedRevision))
	s, _, base := askFixture(f)
	for _, mode := range []string{"pinned", "current"} {
		input := questions.Request{Question: "Read the default " + mode + " project source.", PagePath: base.PagePath, SourceMode: mode, ProviderID: "codex"}
		qid, jid := askStart(s, f, version, input)
		q, e := s.Question(ctx, f.token, qid)
		Expect(e).NotTo(HaveOccurred())
		Expect(q.TargetSetID).NotTo(BeNil(), "default %s must capture canonical flat evidence.path", mode)
		Expect(q.Limitations).To(BeEmpty())
		var selection source.CaptureRequest
		Expect(json.Unmarshal(q.RequestedSelection, &selection)).To(Succeed())
		Expect(selection.SourceMode).To(Equal(mode))
		Expect(selection.Selections).To(HaveLen(1))
		Expect(selection.Selections[0].SourceID).To(Equal(mapping))
		Expect(selection.Selections[0].Paths).To(Equal([]source.CapturePath{{Path: "selected.txt"}}))
		expectedBytes, expectedRevision := pinnedBytes, pinnedRevision
		if mode == "current" {
			expectedBytes, expectedRevision = currentBytes, currentRevision
		}
		hash := sha256.Sum256(expectedBytes)
		driver := &questionCitationDriver{f: f, observe: true}
		p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: s.Prepare, Apply: s.Apply}
		Expect(p.Process(ctx, jid)).To(Succeed())
		Expect(p.StopProviders()).To(Succeed())
		Expect(driver.calls.Load()).To(Equal(int32(1)))
		var object domain.Object
		var captured domain.CapturedRead
		var actual domain.ProviderInput
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			attempts, e := tx.ProviderAttempts(jid)
			if e != nil {
				return e
			}
			Expect(attempts).To(HaveLen(1))
			Expect(attempts[0].TargetSetID).To(Equal(*q.TargetSetID))
			inputs, e := tx.ProviderInputs(attempts[0].ID)
			if e != nil {
				return e
			}
			Expect(inputs).To(HaveLen(1), "unrelated repository files are not default inputs")
			actual = inputs[0]
			captured, e = tx.CapturedRead(actual.ReadID)
			if e != nil {
				return e
			}
			object, e = tx.Object(actual.Hash)
			return e
		})).To(Succeed())
		Expect(actual.Hash).To(Equal(hex.EncodeToString(hash[:])))
		Expect(actual.ObservedAt).NotTo(BeNil(), "the controlled driver acknowledged the complete actual input")
		Expect(actual.ObservedOrigin).To(Equal("daemon_supplied"))
		var identity map[string]string
		Expect(json.Unmarshal(captured.SourceJSON, &identity)).To(Succeed())
		Expect(identity).To(Equal(map[string]string{"kind": "git", "repo_id": repo, "object_format": "sha1", "exact_revision": expectedRevision}))
		var location map[string]string
		Expect(json.Unmarshal(captured.LocationJSON, &location)).To(Succeed())
		Expect(location).To(Equal(map[string]string{"path": "selected.txt"}))
		Expect(captured.TargetSetID).To(Equal(*q.TargetSetID))
		var supplied []byte
		Expect(f.store.Read(ctx, object, domain.Pin{Kind: "controlled_question_delivery", OwnerID: qid}, func(r io.ReadSeeker) error { var e error; supplied, e = io.ReadAll(r); return e })).To(Succeed())
		Expect(supplied).To(Equal(expectedBytes))
		answers, e := s.Answers(ctx, f.token, qid, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(answers.Items).To(HaveLen(1))
		Expect(answers.Items[0].State).To(Equal("final"))
		Expect(answers.Items[0].Citations).To(HaveLen(1))
		citation := answers.Items[0].Citations[0]
		Expect(citation.Basis).To(Equal("observed_input"))
		Expect(citation.ObservedAt).NotTo(BeNil())
		Expect(citation.Hash).To(Equal(actual.Hash))
		Expect(citation.TargetSetID).To(Equal(*q.TargetSetID))
	}
})
