package source_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/ai/codex"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	appauth "haeryong.local/haeryong/internal/application/auth"
	generation "haeryong.local/haeryong/internal/application/generation"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

var generationAuditFailure = errors.New("controlled final generation audit failure")

type generationPipelineStore struct {
	app.Store
	actual    *foundation.Store
	failFinal bool
	snapshot  string
	take      atomic.Bool
	taken     atomic.Bool
}
type generationPipelineTx struct {
	app.WriteTx
	owner *generationPipelineStore
}

func (s *generationPipelineStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	e := s.Store.Write(ctx, func(tx app.WriteTx) error { return fn(generationPipelineTx{tx, s}) })
	if e == nil && s.take.Swap(false) {
		e = s.actual.SnapshotDatabase(ctx, s.snapshot)
	}
	return e
}
func (t generationPipelineTx) RecordProviderTransport(id string, v domain.ProviderTransport) error {
	if t.owner.failFinal && v.FinishedAt != "" {
		return generationAuditFailure
	}
	if e := t.WriteTx.RecordProviderTransport(id, v); e != nil {
		return e
	}
	if v.FrameSHA != "" && v.AcceptedAt != "" && v.FinishedAt == "" && t.owner.taken.CompareAndSwap(false, true) {
		t.owner.take.Store(true)
	}
	return nil
}
func pipelineDriver(store app.Store, bytes *foundation.Store, expected ...domain.GenerationReference) *codex.Driver {
	root := GinkgoT().TempDir()
	binary := filepath.Join(root, "controlled-generation-peer")
	script := `#!/usr/bin/env python3
import sys,json,os
if '--version' in sys.argv:print('codex-cli 0.164.0');sys.exit(0)
def send(x):sys.stdout.write(json.dumps(x)+'\n');sys.stdout.flush()
for line in sys.stdin:
 r=json.loads(line);m=r['method'];p=r.get('params',{});result={}
 with open(sys.argv[0]+'.methods','a') as f:f.write(m+'\n')
 if m=='initialized':continue
 if m=='initialize':result={'userAgent':'controlled'}
 if m=='account/read':result={'requiresOpenaiAuth':True,'account':{'type':'chatgpt'}}
 if m=='config/read':result={'config':{}}
 if m=='account/rateLimits/read':result={'ordinaryUsageAllowed':None}
 if m=='model/list':result={'data':[{'model':'controlled'}],'nextCursor':None}
 if m=='thread/start':
  cwd=p['cwd'];assert p['sandbox']=='workspace-write'
  result={'approvalPolicy':'never','cwd':cwd,'modelProvider':'openai','model':'controlled','sandbox':{'type':'workspaceWrite','networkAccess':False},'thread':{'id':'pipeline-thread'}}
 if m=='skills/list':
  path=os.path.join(p['cwds'][0],'.agents','skills','use-haeryong','SKILL.md')
  result={'data':[{'cwd':p['cwds'][0],'skills':[{'name':'use-haeryong','path':path,'enabled':True}],'errors':[]}]}
 if m=='turn/start':
  assert len([i for i in p['input'] if i['type']=='skill'])==1
  expected=json.loads(EXPECTED_CONTEXT)
  if not expected:assert p['input'][-1]['text']=='exact source delivered to controlled peer'
  if expected:
   import hashlib
   contexts=[(i,json.loads(x['text'])) for i,x in enumerate(p['input']) if x['type']=='text' and x['text'].startswith('{"authority":')]
   assert len(contexts)==len(expected)
   for ((i,context),ref) in zip(contexts,expected):
    assert context['basis']=='explanation_context'
    assert context['reference']=={k:ref[k] for k in ['version_id','page_path','region_id','bytes_sha256']}
    assert hashlib.sha256(p['input'][i+1]['text'].encode()).hexdigest()==ref['object_sha256']==context['supplied_object_sha256']

  out=os.path.join(cwd,'result');os.makedirs(os.path.join(out,'nested'));os.makedirs(os.path.join(out,'empty'))
  with open(os.path.join(out,'read.html'),'w') as f:f.write('<a href="nested/next.html">complete explanation</a><script src="app.js"></script>')
  with open(os.path.join(out,'nested','next.html'),'w') as f:f.write('<html>nested explanation</html>')
  with open(os.path.join(out,'app.js'),'w') as f:f.write('globalThis.retained=true')
  send({'id':r['id'],'result':{'turn':{'id':'pipeline-turn','status':'inProgress','items':[]}}})
  send({'method':'turn/completed','params':{'threadId':'pipeline-thread','turn':{'id':'pipeline-turn','status':'completed','itemsView':'full','items':[{'type':'agentMessage','phase':'final_answer','text':'Native directory ready'}]}}});continue
 send({'id':r['id'],'result':result})
`
	encodedContext, _ := json.Marshal(expected)
	encodedString, _ := json.Marshal(string(encodedContext))
	script = strings.Replace(script, "EXPECTED_CONTEXT", string(encodedString), 1)
	Expect(os.WriteFile(binary, []byte(script), 0700)).To(Succeed())
	d := codex.New(store, bytes, root)
	d.Binary = binary
	d.BinarySHA = fmt.Sprintf("%x", sha256.Sum256([]byte(script)))
	return d
}
func pipelineFixture(failFinal bool) (*vFixture, string, *codex.Driver, *generationPipelineStore, *work.ProviderCoordinator, error) {
	f, root, id := managedFixture("selected_snapshot")
	Expect(os.WriteFile(filepath.Join(root, "readme.txt"), []byte("exact source delivered to controlled peer"), 0600)).To(Succeed())
	generation := generationService(f)
	input := generationInput(f, id)
	input.Intent = "publish"
	response, e := generation.Start(context.Background(), f.token, sid(), input)
	Expect(e).NotTo(HaveOccurred())
	job, _ := generatedIDs(response)
	store := &generationPipelineStore{Store: f.store, actual: f.store, failFinal: failFinal, snapshot: filepath.Join(GinkgoT().TempDir(), "accepted-before-terminal.sqlite")}
	driver := pipelineDriver(store, f.store)
	imports := importer.New(store, f.auth, f.store, staging.New())
	p := &work.ProviderCoordinator{Store: store, Bytes: f.store, Driver: driver, Prepare: imports.PrepareGeneration, Apply: imports.ApplyGeneration}
	e = p.Process(context.Background(), job)
	return f, job, driver, store, p, e
}
func pipelineAttempt(f *vFixture, job string) domain.ProviderAttempt {
	var a domain.ProviderAttempt
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		rows, e := tx.ProviderAttempts(job)
		if e != nil {
			return e
		}
		Expect(rows).To(HaveLen(1))
		a = rows[0]
		return nil
	})).To(Succeed())
	return a
}

var _ = It("C04COMPOSE1 real admitted production adapter and SQLite preserve saved whole tree across selective audit failure storage reopen and no-Execute publication", Label("persistence"), func() {
	ctx := context.Background()
	f, job, driver, _, p, e := pipelineFixture(true)
	Expect(e).To(MatchError(generationAuditFailure))
	a := pipelineAttempt(f, job)
	Expect(a.State).To(Equal("result_saved"))
	Expect(a.ResultHash).NotTo(BeEmpty())
	Expect(a.Transport.Generation.Format).To(Equal("directory_zip"))
	Expect(a.Transport.Generation.SkillInputAcknowledged).To(BeTrue())
	Expect(a.Transport.Generation.PublicFinalSHA).To(BeEmpty(), "failed final audit is not silently backfilled")
	Expect(work.ProviderRestartDiagnostics(p.StopProviders())).To(BeTrue(), "the actual owned peer drained and fallback job pause committed")
	j, e := f.store.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(j.Error).To(Equal("provider_accounting_unavailable"))
	// The separately saved ZIP, not this volatile native output tree, owns reuse.
	Expect(os.RemoveAll(a.Transport.CWD)).To(Succeed())
	Expect(f.store.Objects.Close()).To(Succeed())
	Expect(f.store.Close()).To(Succeed())
	profile, e := service.Load(service.Selection{Profile: "test", DataDir: f.dataDir})
	Expect(e).NotTo(HaveOccurred())
	reopened := foundation.New(nil)
	_, closeStore, e := reopened.Prepare(ctx, profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeStore()
	f.store = reopened
	auth := appauth.New(reopened)
	imports := importer.New(reopened, auth, reopened, staging.New())
	j, e = reopened.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	_, e = auth.ResumeJob(ctx, f.token, job, sid(), appauth.JobETag(j), appauth.ResumeJobRequest{Decision: "continue", ExpectedGeneration: j.AttemptGeneration})
	Expect(e).NotTo(HaveOccurred())
	current := &work.ProviderCoordinator{Store: reopened, Bytes: reopened, Prepare: imports.PrepareGeneration, Apply: imports.ApplyGeneration}
	Expect(current.Process(ctx, job)).To(Succeed())
	j, e = reopened.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
	var result struct {
		Version  string `json:"version_id"`
		Document string `json:"document_id"`
	}
	Expect(json.Unmarshal(j.Result, &result)).To(Succeed())
	Expect(result.Version).NotTo(BeEmpty())
	q := query.New(reopened, auth, reopened)
	Expect(q.ReadFile(ctx, f.token, result.Version, "nested/next.html", false, func(_ query.ResolvedFile, r io.ReadSeeker) error {
		b, e := io.ReadAll(r)
		Expect(string(b)).To(ContainSubstring("nested explanation"))
		return e
	})).To(Succeed())
	review, e := q.ReviewGet(ctx, f.token, result.Version)
	Expect(e).NotTo(HaveOccurred())
	Expect(review.Value.State).To(Equal("unreviewed"))
	methods, e := os.ReadFile(driver.Binary + ".methods")
	Expect(e).NotTo(HaveOccurred())
	Expect(strings.Count(string(methods), "turn/start\n")).To(Equal(1))
})

func oneProviderFact(db, attempt string) json.RawMessage {
	var found json.RawMessage
	Expect(sqlite.RestoreOutstanding(context.Background(), db, func(raw json.RawMessage) error {
		var v struct {
			ID string `json:"attempt_id"`
		}
		if e := json.Unmarshal(raw, &v); e != nil {
			return e
		}
		if v.ID == attempt {
			Expect(found).To(BeNil())
			found = append(json.RawMessage{}, raw...)
		}
		return nil
	})).To(Succeed())
	Expect(found).NotTo(BeNil())
	return found
}

var _ = It("C04COMPOSE2 actual accepted and completed generation audit snapshots merge monotonically in real SQLite restore with conflicting native final facts retained", Label("persistence"), func() {
	ctx := context.Background()
	f, job, _, store, _, e := pipelineFixture(false)
	Expect(e).NotTo(HaveOccurred())
	a := pipelineAttempt(f, job)
	Expect(a.Transport.Generation.PublicFinalSHA).To(HaveLen(64))
	Expect(a.Transport.Generation.CapturedAt).NotTo(BeEmpty())
	old := oneProviderFact(store.snapshot, a.ID)
	fresh := oneProviderFact(filepath.Join(f.dataDir, "storage", "library.sqlite"), a.ID)
	var before struct {
		Transport domain.ProviderTransport `json:"transport"`
	}
	Expect(json.Unmarshal(old, &before)).To(Succeed())
	Expect(before.Transport.Generation.PublicFinalSHA).To(BeEmpty())
	Expect(before.Transport.Generation.SkillInputAcknowledged).To(BeTrue())
	Expect(before.Transport.FrameSHA).NotTo(BeEmpty())
	snapshot, e := os.ReadFile(store.snapshot)
	Expect(e).NotTo(HaveOccurred())
	available := func(object domain.Object) (bool, error) {
		var current domain.Object
		e := f.store.Write(ctx, func(tx app.WriteTx) error { var e error; current, e = tx.Object(object.Hash); return e })
		if e != nil {
			return false, e
		}
		e = f.store.Read(ctx, current, domain.Pin{Kind: "test_restore_read", OwnerID: a.ID}, func(io.ReadSeeker) error { return nil })
		return e == nil, e
	}
	for _, reverse := range []bool{false, true} {
		destination := filepath.Join(GinkgoT().TempDir(), "restore.sqlite")
		Expect(os.WriteFile(destination, snapshot, 0600)).To(Succeed())
		facts := []json.RawMessage{old, fresh}
		if reverse {
			facts = []json.RawMessage{fresh, old}
		}
		for _, fact := range facts {
			Expect(sqlite.RetainProviderOrphans(ctx, destination, bytes.NewReader(fact), available)).To(Succeed())
		}
		read := func() []byte {
			db, e := sql.Open("sqlite", "file:"+destination+"?mode=ro")
			Expect(e).NotTo(HaveOccurred())
			defer db.Close()
			var raw []byte
			Expect(db.QueryRow(`SELECT value FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&raw)).To(Succeed())
			return raw
		}
		raw := read()
		var merged struct {
			State     string                   `json:"state"`
			Transport domain.ProviderTransport `json:"transport"`
		}
		Expect(json.Unmarshal(raw, &merged)).To(Succeed())
		Expect(merged.State).To(Equal("result_saved"))
		Expect(merged.Transport.Generation.PublicFinalSHA).To(Equal(a.Transport.Generation.PublicFinalSHA))
		Expect(merged.Transport.Generation.SkillInputAcknowledged).To(BeTrue())
		Expect(merged.Transport.Generation.CapturedAt).To(Equal(a.Transport.Generation.CapturedAt))
		Expect(sqlite.RetainProviderOrphans(ctx, destination, bytes.NewReader(facts[1]), available)).To(Succeed())
		Expect(read()).To(Equal(raw))
		// Deliberately conflicting test metadata, never a claim of a second native
		// observation: both original facts must survive instead of being strengthened.
		var conflict map[string]any
		Expect(json.Unmarshal(fresh, &conflict)).To(Succeed())
		conflict["transport"].(map[string]any)["generation_output"].(map[string]any)["public_final_sha256"] = strings.Repeat("f", 64)
		bad, _ := json.Marshal(conflict)
		Expect(sqlite.RetainProviderOrphans(ctx, destination, bytes.NewReader(bad), available)).To(Succeed())
		var retained map[string]json.RawMessage
		Expect(json.Unmarshal(read(), &retained)).To(Succeed())
		Expect(retained["conflicting_prior_fact"]).NotTo(BeNil())
	}
})

var _ = It("T4R3PREP01 typed prepared generation retry refuses before assignment while exact saved continuation and a new linked request remain usable", Label("persistence"), func() {
	ctx := context.Background()
	f, _, _ := managedFixture("selected_snapshot")
	ref := publishedGenerationContext(f)
	base, e := f.store.Version(ctx, ref.VersionID)
	Expect(e).NotTo(HaveOccurred())
	core := generationService(f)
	input := generation.Request{ProjectID: f.project, DocumentPresent: true, DocumentID: &base.DocumentID, BasePresent: true, BaseVersionID: &base.ID, ProviderID: "codex", Intent: "publish", Prompt: "An explicit publication retaining its original captured base"}
	accepted, e := core.Start(ctx, f.token, sid(), input)
	Expect(e).NotTo(HaveOccurred())
	job, draftID := generatedIDs(accepted)
	// Another ordinary generation wins publication before the original result applies.
	newer, e := core.Start(ctx, f.token, sid(), input)
	Expect(e).NotTo(HaveOccurred())
	newerJob, _ := generatedIDs(newer)
	imports := importer.New(f.store, f.auth, f.store, staging.New())
	winningDriver := &admittedDirectoryDriver{f: f}
	winning := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: winningDriver, Prepare: imports.PrepareGeneration, Apply: imports.ApplyGeneration}
	Expect(winning.Process(ctx, newerJob)).To(Succeed())
	driver := &admittedDirectoryDriver{f: f}
	coordinator := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: imports.PrepareGeneration, Apply: imports.ApplyGeneration}
	Expect(coordinator.Process(ctx, job)).To(Succeed())
	before, e := f.store.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(before.State).To(Equal("paused"))
	Expect(before.Error).To(Equal("VERSION_CONFLICT"))
	var original domain.GenerationIntent
	var draft domain.Draft
	var history []domain.Draft
	var attempts []domain.ProviderAttempt
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		var err error
		original, err = tx.GenerationIntent(job)
		if err != nil {
			return err
		}
		Expect(original.PreparedVersion).NotTo(BeNil())
		Expect(original.PreparationAttemptID).NotTo(BeEmpty())
		var scope domain.ProviderScope
		Expect(json.Unmarshal(before.Scope, &scope)).To(Succeed())
		Expect(scope.Invocation).NotTo(BeNil())
		Expect(scope.Invocation.Generation).NotTo(BeNil())
		draft, err = tx.Draft(draftID)
		if err != nil {
			return err
		}
		history, err = tx.DraftHistory(draftID, 0, 64)
		if err != nil {
			return err
		}
		attempts, err = tx.ProviderAttempts(job)
		return err
	})).To(Succeed())
	db, e := sql.Open("sqlite", "file:"+filepath.Join(f.dataDir, "storage", "library.sqlite")+"?mode=ro")
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	pins := func() []string {
		rows, err := db.Query(`SELECT pin_id||':'||owner_kind||':'||owner_id||':'||content_hash FROM object_pins ORDER BY pin_id`)
		Expect(err).NotTo(HaveOccurred())
		defer rows.Close()
		var values []string
		for rows.Next() {
			var value string
			Expect(rows.Scan(&value)).To(Succeed())
			values = append(values, value)
		}
		Expect(rows.Err()).NotTo(HaveOccurred())
		return values
	}
	originalPins := pins()
	_, e = f.auth.ResumeJob(ctx, f.token, job, sid(), appauth.JobETag(before), appauth.ResumeJobRequest{ExpectedGeneration: before.AttemptGeneration, Decision: "retry"})
	Expect(e).To(MatchError(domain.ErrTransition))
	after, e := f.store.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(after).To(Equal(before))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		current, err := tx.GenerationIntent(job)
		if err != nil {
			return err
		}
		Expect(current).To(Equal(original))
		d, err := tx.Draft(draftID)
		if err != nil {
			return err
		}
		Expect(d).To(Equal(draft))
		h, err := tx.DraftHistory(draftID, 0, 64)
		if err != nil {
			return err
		}
		Expect(h).To(Equal(history))
		a, err := tx.ProviderAttempts(job)
		if err != nil {
			return err
		}
		Expect(a).To(Equal(attempts))
		return nil
	})).To(Succeed())
	Expect(pins()).To(Equal(originalPins))
	var retries int
	Expect(db.QueryRow(`SELECT count(*) FROM provider_retry_authorizations WHERE job_id=?`, job).Scan(&retries)).To(Succeed())
	Expect(retries).To(BeZero())
	Expect(coordinator.Process(ctx, job)).To(MatchError(domain.ErrTransition))
	Expect(driver.calls).To(Equal(1))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		_, err := tx.ResumeProviderJob(job, before.AttemptGeneration, "continue")
		return err
	})).To(Succeed())
	coordinator.Driver = nil
	Expect(coordinator.Process(ctx, job)).To(Succeed())
	Expect(driver.calls).To(Equal(1))
	after, e = f.store.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(after.State).To(Equal("paused"))
	Expect(after.Error).To(Equal("VERSION_CONFLICT"))
	// A new explicit request captures the current version rather than rebasing the old one.
	current, e := f.store.Document(ctx, base.DocumentID)
	Expect(e).NotTo(HaveOccurred())
	input.BaseVersionID = &current.LatestVersionID
	linked, e := core.Start(ctx, f.token, sid(), input)
	Expect(e).NotTo(HaveOccurred())
	linkedJob, _ := generatedIDs(linked)
	coordinator.Driver = driver
	Expect(coordinator.Process(ctx, linkedJob)).To(Succeed())
	Expect(driver.calls).To(Equal(2))
	final, e := f.store.Job(ctx, linkedJob)
	Expect(e).NotTo(HaveOccurred())
	Expect(final.State).To(Equal("completed"))
})

var _ = It("T4R3PREP02 a typed generation without prepared candidate can explicitly retry and apply its fresh result", Label("persistence"), func() {
	ctx := context.Background()
	f, _, _ := managedFixture("selected_snapshot")
	input := generation.Request{ProjectID: f.project, DocumentPresent: true, ProviderID: "codex", Intent: "draft", Prompt: "Retry only a candidate which has not been prepared"}
	response, e := generationService(f).Start(ctx, f.token, sid(), input)
	Expect(e).NotTo(HaveOccurred())
	job, draftID := generatedIDs(response)
	imports := importer.New(f.store, f.auth, f.store, staging.New())
	driver := &admittedDirectoryDriver{f: f}
	controlledPreparation := errors.New("controlled preparation unavailable")
	coordinator := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Apply: imports.ApplyGeneration, Prepare: func(context.Context, domain.ProviderAttempt) error {
		return controlledPreparation
	}}
	preparationError := coordinator.Process(ctx, job)
	Expect(preparationError).To(HaveOccurred())
	Expect(errors.Is(preparationError, controlledPreparation)).To(BeTrue())
	before, e := f.store.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(before.State).To(Equal("paused"))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		g, e := tx.GenerationIntent(job)
		if e != nil {
			return e
		}
		Expect(g.PreparedVersion).To(BeNil())
		d, e := tx.Draft(draftID)
		Expect(d.State).To(Equal("preparing"))
		return e
	})).To(Succeed())
	_, e = f.auth.ResumeJob(ctx, f.token, job, sid(), appauth.JobETag(before), appauth.ResumeJobRequest{ExpectedGeneration: before.AttemptGeneration, Decision: "retry"})
	Expect(e).NotTo(HaveOccurred())
	coordinator.Prepare = imports.PrepareGeneration
	Expect(coordinator.Process(ctx, job)).To(Succeed())
	Expect(driver.calls).To(Equal(2))
	after, e := f.store.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(after.State).To(Equal("completed"))
})
