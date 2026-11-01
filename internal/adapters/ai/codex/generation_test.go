package codex

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"haeryong.local/haeryong/skills"
)

func directoryFixture(mode string) (*Driver, domain.ProviderAttempt, []domain.ProviderInput, *testStore) {
	root := GinkgoT().TempDir()
	binary := filepath.Join(root, "controlled-codex")
	script := `#!/usr/bin/env python3
import sys,json,os,time,signal,shutil
mode=MODE
if '--version' in sys.argv:print('codex-cli 0.164.0');sys.exit(0)
cwd=None
def send(value):sys.stdout.write(json.dumps(value)+'\n');sys.stdout.flush()
def full():return {'id':'generation-turn','status':'completed','itemsView':'full','items':[{'type':'reasoning','summary':['PRIVATE_REASONING']},{'type':'agentMessage','phase':'final_answer','text':'First final'},{'type':'agentMessage','phase':'commentary','text':'COMMENTARY'},{'type':'agentMessage','phase':'final_answer','text':'Second final'}]}
def late(signum,frame):
 if cwd:
  with open(os.path.join(cwd,'result','drained.txt'),'w') as f:
   f.write('late owned writer drained');f.flush();os.fsync(f.fileno())
  with open(sys.argv[0]+'.late-written','w') as f:f.write('late writer persisted')
 sys.exit(0)
if mode=='late_writer':signal.signal(signal.SIGTERM,late)
for line in sys.stdin:
 r=json.loads(line);method=r['method'];p=r.get('params',{});result={}
 with open(sys.argv[0]+'.trace','a') as f:f.write(json.dumps({'method':method,'view':p.get('itemsView')})+'\n')
 if method=='initialized':continue
 if method=='initialize':result={'userAgent':'controlled'}
 if method=='account/read':result={'requiresOpenaiAuth':True,'account':{'type':'chatgpt'}}
 if method=='config/read':result={'config':{}}
 if method=='account/rateLimits/read':result={'ordinaryUsageAllowed':None}
 if method=='model/list':result={'data':[{'model':'controlled-model'}],'nextCursor':None}
 if method=='thread/start':
  cwd=p['cwd'];assert p['sandbox']=='workspace-write' and p['approvalPolicy']=='never'
  result={'approvalPolicy':'never','cwd':cwd,'model':'controlled-model','modelProvider':'openai','sandbox':{'type':'readOnly' if mode=='wrong_mode' else 'workspaceWrite','networkAccess':False},'thread':{'id':'generation-thread'}}
 if method=='skills/list':
  selected=p['cwds'][0];skill=os.path.join(selected,'.agents','skills','use-haeryong','SKILL.md')
  assert os.path.isfile(skill)
  result={'data':[{'cwd':selected,'skills':[] if mode=='skill_missing' else [{'name':'use-haeryong','path':skill,'enabled':mode!='skill_disabled'}],'errors':[]}]}
 if method=='turn/start':
  policy=p['sandboxPolicy'];assert policy=={'type':'workspaceWrite','writableRoots':[cwd],'networkAccess':False,'excludeTmpdirEnvVar':True,'excludeSlashTmp':True}
  skill=[x for x in p['input'] if x['type']=='skill'];assert len(skill)==1 and skill[0]['path']==os.path.join(cwd,'.agents','skills','use-haeryong','SKILL.md')
  assert p['input'][-1]['text']=='Exact source: π\n'
  if mode=='prompt_ref':
   import hashlib
   assert p['input'][0]['text']==('large exact request π\n'*4096)
   contexts=[(i,json.loads(x['text'])) for i,x in enumerate(p['input']) if x['type']=='text' and x['text'].startswith('{"authority":')]
   assert len(contexts)==1
   i,context=contexts[0]
   assert context['basis']=='explanation_context' and context['reference']['version_id']=='11111111-1111-4111-8111-111111111111'
   assert hashlib.sha256(p['input'][i+1]['text'].encode()).hexdigest()==context['reference']['bytes_sha256']==context['supplied_object_sha256']

  out=os.path.join(cwd,'result');os.makedirs(os.path.join(out,'pages'));os.makedirs(os.path.join(out,'empty'))
  with open(os.path.join(out,'read.html'),'w') as f:f.write('<a href="pages/next.html">next</a><script src="app.js"></script>')
  with open(os.path.join(out,'pages','next.html'),'w') as f:f.write('<html>complete next</html>')
  with open(os.path.join(out,'app.js'),'w') as f:f.write('globalThis.complete=true')
  with open(os.path.join(out,'attachment.任意'),'wb') as f:f.write(bytes([0,255,1,2]))
  os.symlink('pages/next.html',os.path.join(out,'alias'))
  if mode=='bad_skill':
   with open(skill[0]['path'],'a') as f:f.write('\nmodified guidance')
  if mode=='symlink_root':
   shutil.rmtree(out);outside=os.path.join(os.path.dirname(sys.argv[0]),'outside');os.mkdir(outside)
   with open(os.path.join(outside,'private.txt'),'w') as f:f.write('must not capture')
   os.symlink(outside,out)
  send({'id':r['id'],'result':{'turn':{'id':'generation-turn','status':'inProgress','items':[]}}})
  if mode=='unknown':break
  terminal=full()
  if mode=='summary':terminal['itemsView']='summary';terminal['items']=terminal['items'][-1:]
  send({'method':'turn/completed','params':{'threadId':'generation-thread','turn':terminal}})
  continue
 if method=='thread/turns/list':
  complete=mode!='unknown' or os.path.exists(sys.argv[0]+'.complete')
  terminal=full() if complete else {'id':'generation-turn','status':'inProgress','items':[]}
  terminal['itemsView']=p['itemsView']
  if p['itemsView']=='notLoaded':terminal.pop('items',None)
  result={'data':[terminal],'nextCursor':None}
 send({'id':r['id'],'result':result})
if mode=='late_writer':
 while True:time.sleep(1)
`
	encoded, _ := json.Marshal(mode)
	script = strings.Replace(script, "MODE", string(encoded), 1)
	Expect(os.WriteFile(binary, []byte(script), 0700)).To(Succeed())
	tx := &testTx{}
	store := &testStore{tx: tx}
	path, owned, e := skills.Materialize(root)
	Expect(e).NotTo(HaveOccurred())
	inv := domain.ProviderInvocation{Prompt: "Generate the complete selected tree", Generation: &domain.GenerationInvocation{SkillPath: path, SkillSHA: owned.SHA256, SkillVersion: owned.Version, Intent: "draft"}}
	read, e := tx.CapturedRead("read")
	Expect(e).NotTo(HaveOccurred())
	target := json.RawMessage(`{"project_id":"project","document_id":null,"intent":"draft"}`)
	sha, e := SemanticRequestSHA("owner", target, inv, []domain.CapturedRead{read})
	Expect(e).NotTo(HaveOccurred())
	raw, _ := json.Marshal(domain.ProviderScope{ProviderID: "codex", PrincipalID: "owner", Target: target, Invocation: &inv, ReadIDs: []string{"read"}})
	tx.job = domain.Job{ID: "job", Kind: "generation", Scope: raw}
	attempt, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	a := domain.ProviderAttempt{ID: attempt, JobID: "job", RequestSHA: sha, TargetSetID: "target"}
	d := New(store, testBytes{}, root)
	d.Binary = binary
	d.BinarySHA = fmt.Sprintf("%x", sha256.Sum256([]byte(script)))
	return d, a, []domain.ProviderInput{{AttemptID: a.ID, ReadID: "read", Hash: read.BytesSHA}}, store
}

// Install the existing test-only signal seam after Execute registers its owned
// transport. Other Store methods and all production cleanup logic are unchanged.
type directoryCloseBarrierStore struct {
	*testStore
	install func()
}

func (s *directoryCloseBarrierStore) Job(ctx context.Context, id string) (domain.Job, error) {
	s.install()
	return s.testStore.Job(ctx, id)
}

func waitDirectoryWriteAcknowledgment(path string) error {
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		body, e := os.ReadFile(path)
		if e == nil && string(body) == "late writer persisted" {
			return nil
		}
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		select {
		case <-deadline.C:
			return fmt.Errorf("controlled late-writer acknowledgment not observed")
		case <-tick.C:
		}
	}
}

func directoryEntries(out work.ProviderOutcome) map[string][]byte {
	data, e := io.ReadAll(out.Body)
	Expect(e).NotTo(HaveOccurred())
	Expect(out.Body.Close()).To(Succeed())
	zip, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	Expect(e).NotTo(HaveOccurred())
	entries := map[string][]byte{}
	for _, file := range zip.File {
		r, e := file.Open()
		Expect(e).NotTo(HaveOccurred())
		body, e := io.ReadAll(r)
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Close()).To(Succeed())
		entries[file.Name] = body
	}
	return entries
}

var _ = It("C04DIR1 controlled native generation discovers one owned skill and captures full ordinary directory only after owned process drain", Label("integration"), func() {
	for _, mode := range []string{"complete", "summary", "late_writer"} {
		d, a, inputs, store := directoryFixture(mode)
		var closing *transport
		var acknowledgmentErr error
		fileAbsentBeforeTERM, readerDoneBeforeKILL, killForwarded := false, false, false
		if mode == "late_writer" {
			cwd, e := d.generationCWD(a)
			Expect(e).NotTo(HaveOccurred())
			d.Store = &directoryCloseBarrierStore{testStore: store, install: func() {
				d.mu.Lock()
				defer d.mu.Unlock()
				active, ok := d.active[a.ID]
				if !ok || closing != nil {
					return
				}
				closing = active.transport
				ownedSignal := closing.signal
				closing.signal = func(group int, sig syscall.Signal) error {
					if sig == syscall.SIGTERM {
						_, e := os.Stat(filepath.Join(cwd, "result", "drained.txt"))
						fileAbsentBeforeTERM = os.IsNotExist(e)
					}
					if sig == syscall.SIGKILL {
						select {
						case <-closing.done:
							readerDoneBeforeKILL = true
						default:
						}
						// The fixture must acknowledge its own completed late write.
						// Even a failed ACK never skips the actual bounded KILL/drain.
						acknowledgmentErr = waitDirectoryWriteAcknowledgment(d.Binary + ".late-written")
						killForwarded = true
					}
					return ownedSignal(group, sig)
				}
			}}
		}
		acked := false
		out, e := d.Execute(context.Background(), a, inputs, func() error { return nil }, func(p work.ProviderProgress) error { acked = true; Expect(p.Observed).To(HaveLen(1)); return nil })
		if mode == "late_writer" {
			AddReportEntry("late writer shutdown barrier", map[string]any{
				"file_absent_before_actual_TERM":     fileAbsentBeforeTERM,
				"reader_done_before_controlled_KILL": readerDoneBeforeKILL,
				"write_acknowledged":                 acknowledgmentErr == nil && killForwarded,
				"actual_KILL_forwarded":              killForwarded,
			})
			Expect(closing).NotTo(BeNil(), "install the existing signal seam before close")
			Expect(fileAbsentBeforeTERM).To(BeTrue(), "late output must not exist before actual TERM")
			Expect(acknowledgmentErr).NotTo(HaveOccurred(), "actual TERM handler flush/fsync must precede controlled KILL")
			Expect(killForwarded).To(BeTrue())
			Eventually(closing.waitDone, time.Second).Should(BeClosed())
			Expect(closing.done).To(BeClosed())
		}
		Expect(e).NotTo(HaveOccurred(), mode)
		Expect(acked).To(BeTrue())
		Expect(out.State).To(Equal("result_saved"))
		Expect(out.CompletionObserved).To(BeTrue())
		Expect(out.Transport.BackendState).To(Equal("completed"))
		Expect(out.Transport.CleanupState).To(Equal("drained"))
		Expect(out.LocalClosed).To(BeTrue())
		g := out.Transport.Generation
		Expect(g).NotTo(BeNil())
		Expect(g.SkillDiscovered).To(BeTrue())
		Expect(g.SkillInputAcknowledged).To(BeTrue())
		Expect(g.Format).To(Equal("directory_zip"))
		Expect(g.CapturedAt).NotTo(BeEmpty())
		Expect(g.PublicFinalSHA).To(Equal(fmt.Sprintf("%x", sha256.Sum256([]byte("First final\nSecond final")))))
		entries := directoryEntries(out)
		Expect(entries).To(HaveKey("read.html"))
		Expect(entries).To(HaveKey("pages/next.html"))
		Expect(entries).To(HaveKey("empty/"))
		Expect(entries["attachment.任意"]).To(Equal([]byte{0, 255, 1, 2}))
		Expect(string(entries["alias"])).To(Equal("pages/next.html"))
		Expect(entries).NotTo(HaveKey("index.html"))
		Expect(entries).NotTo(HaveKey(".agents/skills/use-haeryong/SKILL.md"))
		if mode == "late_writer" {
			Expect(entries).To(HaveKey("drained.txt"))
			Expect(string(entries["drained.txt"])).To(Equal("late owned writer drained"))
		}
	}
})
var _ = It("C04DIR2 missing disabled altered guidance and unsupported setup refuse dispatch while corrupt completed output never becomes a saved directory", Label("integration"), func() {
	for _, mode := range []string{"skill_missing", "skill_disabled", "wrong_mode", "bad_skill", "symlink_root"} {
		d, a, inputs, _ := directoryFixture(mode)
		sent := false
		out, _ := d.Execute(context.Background(), a, inputs, func() error { sent = true; return nil }, func(work.ProviderProgress) error { return nil })
		Expect(out.Body).To(BeNil(), mode)
		if mode == "bad_skill" || mode == "symlink_root" {
			Expect(sent).To(BeTrue())
			Expect(out.CompletionObserved).To(BeTrue())
			Expect(out.State).To(Equal("failed"))
			Expect(out.Reason).To(Equal("result_unavailable"))
			Expect(out.Transport.BackendState).To(Equal("completed"))
		} else {
			Expect(sent).To(BeFalse())
			Expect(out.RequestID).To(BeEmpty())
		}
		Expect(out.LocalClosed).To(BeTrue(), mode)
	}
})
var _ = It("C04DIR3 exact history reconciliation captures retained owned directory without a second turn and status-only lookup never retrieves it", Label("integration"), func() {
	d, a, inputs, _ := directoryFixture("unknown")
	out, e := d.Execute(context.Background(), a, inputs, func() error { return nil }, func(work.ProviderProgress) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	Expect(out.State).To(Equal("outcome_unknown"))
	Expect(out.Body).To(BeNil())
	Expect(out.Transport.LocalClosed).To(BeTrue())
	a.RequestID, a.SessionID, a.Transport = out.RequestID, out.SessionID, out.Transport
	Expect(os.WriteFile(d.Binary+".complete", []byte("completed"), 0600)).To(Succeed())
	status, e := d.Reconcile(context.Background(), a, false)
	Expect(e).NotTo(HaveOccurred())
	Expect(status.Body).To(BeNil())
	Expect(status.CompletionObserved).To(BeTrue())
	Expect(status.State).To(Equal("failed"))
	Expect(status.Lookup.BackendState).To(Equal("completed"))
	recovered, e := d.Reconcile(context.Background(), a, true)
	Expect(e).NotTo(HaveOccurred())
	Expect(recovered.State).To(Equal("result_saved"))
	Expect(directoryEntries(recovered)).To(HaveKey("pages/next.html"))
	Expect(recovered.Transport.Generation.CapturedAt).NotTo(BeEmpty())
	trace, e := os.ReadFile(d.Binary + ".trace")
	Expect(e).NotTo(HaveOccurred())
	turns, full := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(trace)), "\n") {
		var row struct {
			Method string
			View   string
		}
		Expect(json.Unmarshal([]byte(line), &row)).To(Succeed())
		if row.Method == "turn/start" {
			turns++
		}
		if row.Method == "thread/turns/list" && row.View == "full" {
			full++
		}
	}
	Expect(turns).To(Equal(1))
	Expect(full).To(Equal(1))
	old := *a.Transport
	old.LocalClosed = false
	old.CleanupState = "unknown"
	a.Transport = &old
	uncertain, e := d.Reconcile(context.Background(), a, true)
	Expect(e).To(HaveOccurred())
	Expect(uncertain.CompletionObserved).To(BeTrue())
	Expect(uncertain.State).To(Equal("outcome_unknown"))
	Expect(uncertain.Reason).To(Equal("result_retrieval_pending"))
	Expect(uncertain.Body).To(BeNil())
	Expect(uncertain.CleanupError).To(HaveOccurred())
})
var _ = It("C04DIR4 explicit new attempt never mixes old partial tree and produced archive byte corruption fails before consumption", Label("integration"), func() {
	d, a, inputs, _ := directoryFixture("complete")
	oldCWD, e := d.generationCWD(a)
	Expect(e).NotTo(HaveOccurred())
	Expect(os.MkdirAll(filepath.Join(oldCWD, "result"), 0700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(oldCWD, "result", "old-only.bin"), []byte("partial old output"), 0600)).To(Succeed())
	a.ID, e = service.NewID()
	Expect(e).NotTo(HaveOccurred())
	inputs[0].AttemptID = a.ID
	out, e := d.Execute(context.Background(), a, inputs, func() error { return nil }, func(work.ProviderProgress) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	reader, ok := out.Body.(*directoryResultReader)
	Expect(ok).To(BeTrue())
	info, e := reader.file.Stat()
	Expect(e).NotTo(HaveOccurred())
	produced, e := zip.NewReader(reader.file, info.Size())
	Expect(e).NotTo(HaveOccurred())
	names := []string{}
	for _, entry := range produced.File {
		names = append(names, entry.Name)
	}
	Expect(names).To(ContainElements("read.html", "pages/next.html", "attachment.任意"))
	Expect(names).NotTo(ContainElement("old-only.bin"), "the previous partial tree is not part of the fresh attempt")
	file, e := os.OpenFile(reader.file.Name(), os.O_WRONLY, 0600)
	Expect(e).NotTo(HaveOccurred())
	_, e = file.WriteAt([]byte{255}, 0)
	Expect(e).NotTo(HaveOccurred())
	Expect(file.Close()).To(Succeed())
	_, e = io.Copy(io.Discard, out.Body)
	Expect(e).To(MatchError(domain.ErrContentIntegrity))
	Expect(out.Body.Close()).To(Succeed())
	remaining, e := os.ReadFile(filepath.Join(oldCWD, "result", "old-only.bin"))
	Expect(e).NotTo(HaveOccurred())
	Expect(string(remaining)).To(Equal("partial old output"))
})

type generationPromptBytes map[string][]byte

func (b generationPromptBytes) Read(ctx context.Context, o domain.Object, p domain.Pin, consume func(io.ReadSeeker) error) error {
	if data, ok := b[o.Hash]; ok {
		if int64(len(data)) != o.Size || fmt.Sprintf("%x", sha256.Sum256(data)) != o.Hash {
			return domain.ErrContentIntegrity
		}
		return consume(bytes.NewReader(data))
	}
	return (testBytes{}).Read(ctx, o, p, consume)
}

var _ = It("C04DIR5 controlled actual native frame receives the complete retained large prompt and separately pinned explanation context before matching ACK", Label("integration"), func() {
	d, a, inputs, store := directoryFixture("prompt_ref")
	prompt := []byte(strings.Repeat("large exact request π\n", 4096))
	contextBytes := []byte("<html>frozen prior explanation π</html>")
	hash := func(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
	var scope domain.ProviderScope
	Expect(json.Unmarshal(store.tx.job.Scope, &scope)).To(Succeed())
	scope.Invocation.Prompt = ""
	scope.Invocation.PromptRef = &domain.ProviderText{Hash: hash(prompt), Size: int64(len(prompt))}
	page := "read.html"
	reference := domain.GenerationReference{ExplanationReference: domain.ExplanationReference{VersionID: "11111111-1111-4111-8111-111111111111", PagePath: &page, Hash: hash(contextBytes)}, ProjectID: "prior-project", DocumentID: "prior-document", ObjectHash: hash(contextBytes), Size: int64(len(contextBytes))}
	scope.Invocation.Generation.References = []domain.GenerationReference{reference}
	read, e := store.tx.CapturedRead("read")
	Expect(e).NotTo(HaveOccurred())
	a.RequestSHA, e = SemanticRequestSHA(scope.PrincipalID, scope.Target, *scope.Invocation, []domain.CapturedRead{read})
	Expect(e).NotTo(HaveOccurred())
	store.tx.job.Scope, _ = json.Marshal(scope)
	store.tx.objects = map[string]domain.Object{hash(prompt): {Hash: hash(prompt), Size: int64(len(prompt)), State: "ready"}, hash(contextBytes): {Hash: hash(contextBytes), Size: int64(len(contextBytes)), State: "ready"}}
	d.Bytes = generationPromptBytes{hash(prompt): prompt, hash(contextBytes): contextBytes}
	acked := false
	out, e := d.Execute(context.Background(), a, inputs, func() error { return nil }, func(p work.ProviderProgress) error {
		acked = true
		Expect(p.Observed).To(HaveLen(1), "only the original actual project source belongs to source input observations")
		return nil
	})
	Expect(e).NotTo(HaveOccurred())
	Expect(acked).To(BeTrue())
	Expect(out.State).To(Equal("result_saved"))
	Expect(out.Transport.Generation.PromptInputAcknowledged).To(BeTrue())
	Expect(out.Transport.Generation.ReferencesAcknowledged).To(BeTrue())
	Expect(out.Transport.Generation.References).To(Equal([]domain.GenerationReference{reference}))
	Expect(directoryEntries(out)).To(HaveKey("read.html"))
})
