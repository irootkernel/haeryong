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
	app "haeryong.local/haeryong/internal/application/storage"
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

  if mode=='whole_ref':
   import base64,hashlib
   assert p['input'][0]['text']=='Generate using the complete prior version π'
   metadata=[]
   for i,part in enumerate(p['input']):
    if part['type']!='text':continue
    try:value=json.loads(part['text'])
    except (ValueError,TypeError):continue
    if isinstance(value,dict):metadata.append((i,value))
   contexts=[(i,m) for i,m in metadata if m.get('basis')=='explanation_context']
   assert len(contexts)==1
   i,context=contexts[0]
   reference=context['reference']
   assert reference=={'version_id':'11111111-1111-4111-8111-111111111111','page_path':None,'region_id':None,'bytes_sha256':'b'*64}
   manifest=p['input'][i+1]['text'].encode()
   assert manifest==b'{"inventory":"immutable whole directory"}'
   assert context['supplied_object_sha256']==hashlib.sha256(manifest).hexdigest()
   assert context['byte_size']==len(manifest) and context['supplied_object_sha256']!=reference['bytes_sha256']
   expected={'pages/%02d.html'%i:('<html>complete page %d π</html>'%i).encode() for i in range(66)}
   expected.update({'behavior.js':b'globalThis.savedQuestion=true','attachment.bin':bytes([0,255,1,2])})
   entries=[(i,m) for i,m in metadata if m.get('basis') in ('explanation_file','explanation_entry')]
   assert [m['path'] for _,m in entries]==sorted([*expected,'empty','alias.html']), 'complete immutable directory is required before ACK'
   files=0
   for i,entry in entries:
    path=entry['path']
    if entry['basis']=='explanation_entry':
     assert (path,entry['kind'],entry['link_target']) in [('empty','directory',''),('alias.html','symlink','pages/00.html')]
     continue
    files+=1
    raw=expected[path]
    assert entry['kind']=='file' and entry['bytes_sha256']==hashlib.sha256(raw).hexdigest() and entry['byte_size']==len(raw)
    body=p['input'][i+1]
    assert body['type']=='text'
    if path=='attachment.bin':
     assert entry['encoding']=='base64' and base64.b64decode(body['text'],validate=True)==raw
    else:
     assert entry['encoding']=='utf-8' and body['text'].encode()==raw
   assert len(entries)==70 and files==68
   source=[m for _,m in metadata if 'source' in m and 'location' in m]
   assert len(source)==1 and source[0]['bytes_sha256']==hashlib.sha256('Exact source: π\n'.encode()).hexdigest()
   with open(sys.argv[0]+'.whole-frame-checked','w') as f:json.dump({'entries':len(entries),'files':files},f)

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

// Generation uses the same immutable directory port as Question. These controls
// inject faults only after the added directory branch has enumerated a page.
type generationDirectoryTx struct {
	*questionFrameTx
	cursors           []string
	afterFirstEntries func()
}

func (t *generationDirectoryTx) Entries(set, after string, limit int) ([]domain.Entry, error) {
	entries, e := t.questionFrameTx.Entries(set, after, limit)
	t.cursors = append(t.cursors, after)
	if e == nil && after == "" && len(entries) > 0 && t.afterFirstEntries != nil {
		t.afterFirstEntries()
	}
	return entries, e
}

type generationDirectoryStore struct {
	*testStore
	directory *generationDirectoryTx
}

func (s *generationDirectoryStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(s.directory)
}

// These are requested pin descriptors at a verified byte port, not proof of
// durable SQLite pin acquisition or garbage-collection behavior.
type generationDirectoryBytes struct {
	values generationPromptBytes
	hashes []string
	pins   []domain.Pin
}

func (b *generationDirectoryBytes) Read(ctx context.Context, o domain.Object, p domain.Pin, consume func(io.ReadSeeker) error) error {
	b.hashes = append(b.hashes, o.Hash)
	b.pins = append(b.pins, p)
	return b.values.Read(ctx, o, p, consume)
}

func wholeGenerationFixture() (*Driver, domain.ProviderAttempt, []domain.ProviderInput, *generationDirectoryStore, *generationDirectoryBytes) {
	d, a, inputs, base := directoryFixture("whole_ref")
	_, _, question, frames, data := questionFrames()
	base.tx.objects = map[string]domain.Object{}
	values := generationPromptBytes{}
	for hash, raw := range data {
		values[hash] = bytes.Clone(raw)
		base.tx.objects[hash] = domain.Object{Hash: hash, Size: int64(len(raw)), State: "ready"}
	}
	put := func(raw []byte) domain.ProviderText {
		hash := fmt.Sprintf("%x", sha256.Sum256(raw))
		values[hash] = bytes.Clone(raw)
		base.tx.objects[hash] = domain.Object{Hash: hash, Size: int64(len(raw)), State: "ready"}
		return domain.ProviderText{Hash: hash, Size: int64(len(raw))}
	}
	prompt := put([]byte("Generate using the complete prior version π"))
	read, e := base.tx.CapturedRead(inputs[0].ReadID)
	Expect(e).NotTo(HaveOccurred())
	source := put([]byte("Exact source: π\n"))
	read.BytesSHA = source.Hash
	base.tx.reads = map[string]domain.CapturedRead{read.ID: read}
	inputs[0].Hash = source.Hash

	var scope domain.ProviderScope
	Expect(json.Unmarshal(base.tx.job.Scope, &scope)).To(Succeed())
	scope.Invocation.Prompt = ""
	scope.Invocation.PromptRef = &prompt
	reference := question.Question.Explanation
	reference.Hash = strings.Repeat("b", 64)
	reference.ProjectID = "prior-project"
	reference.DocumentID = "prior-document"
	scope.Invocation.Generation.References = []domain.GenerationReference{reference}
	a.RequestSHA, e = SemanticRequestSHA(scope.PrincipalID, scope.Target, *scope.Invocation, []domain.CapturedRead{read})
	Expect(e).NotTo(HaveOccurred())
	base.tx.job.Scope, e = json.Marshal(scope)
	Expect(e).NotTo(HaveOccurred())

	frames.question.testTx = base.tx
	frames.question.version.PayloadSHA = reference.Hash
	frames.question.version.ManifestSHA = reference.ObjectHash
	frames.question.version.DocumentID = reference.DocumentID
	store := &generationDirectoryStore{testStore: base, directory: &generationDirectoryTx{questionFrameTx: frames.question}}
	objects := &generationDirectoryBytes{values: values}
	d.Store, d.Bytes = store, objects
	return d, a, inputs, store, objects
}

var _ = It("C04DIR6 whole-version generation delivers every immutable directory entry before matching native ACK without source observations", Label("integration"), func() {
	d, a, inputs, store, objects := wholeGenerationFixture()
	var scope domain.ProviderScope
	Expect(json.Unmarshal(store.tx.job.Scope, &scope)).To(Succeed())
	expectedObserved := append([]domain.ProviderInput(nil), inputs...)
	expectedObserved[0].ObservedOrigin = "daemon_supplied"
	beforeSend, acknowledgements := 0, 0
	frameSHA := ""
	out, e := d.Execute(context.Background(), a, inputs, func() error {
		beforeSend++
		Expect(acknowledgements).To(BeZero())
		Expect(store.tx.metadata.Generation.ReferencesAcknowledged).To(BeFalse())
		staged, e := filepath.Glob(filepath.Join(d.Scratch, "provider-input-*"))
		Expect(e).NotTo(HaveOccurred())
		Expect(staged).To(HaveLen(1))
		raw, e := os.ReadFile(staged[0])
		Expect(e).NotTo(HaveOccurred())
		frameSHA = fmt.Sprintf("%x", sha256.Sum256(raw))
		Expect(store.tx.metadata.FrameSHA).To(Equal(frameSHA))
		var stagedFrame map[string]json.RawMessage
		Expect(json.Unmarshal(raw, &stagedFrame)).To(Succeed())
		var parts []struct{ Type, Text string }
		Expect(json.Unmarshal(stagedFrame["input"], &parts)).To(Succeed())
		files := []string{}
		for _, part := range parts {
			var metadata map[string]any
			if part.Type == "text" && json.Unmarshal([]byte(part.Text), &metadata) == nil && metadata["basis"] == "explanation_file" {
				files = append(files, metadata["path"].(string))
			}
		}
		Expect(files).To(HaveLen(68), "the actual staged generation frame must include complete directory files before dispatch")
		Expect(files).To(ContainElement("pages/65.html"), "delivery must pass the first cursor page")
		return nil
	}, func(p work.ProviderProgress) error {
		acknowledgements++
		checked, e := os.ReadFile(d.Binary + ".whole-frame-checked")
		Expect(e).NotTo(HaveOccurred(), "the native peer must inspect the complete frame before acknowledging it")
		Expect(checked).To(MatchJSON("{\"entries\":70,\"files\":68}"))
		Expect(p.Observed).To(Equal(expectedObserved), "explanation files never become source observations")
		Expect(p.RequestID).To(Equal("generation-turn"))
		return nil
	})
	Expect(e).NotTo(HaveOccurred())
	Expect(beforeSend).To(Equal(1))
	Expect(acknowledgements).To(Equal(1))
	Expect(out.State).To(Equal("result_saved"))
	Expect(out.LocalClosed).To(BeTrue())
	Expect(out.Observed).To(Equal(expectedObserved))
	Expect(out.Transport.Question).To(BeNil())
	Expect(out.Transport.FrameSHA).To(Equal(frameSHA))
	Expect(out.Transport.Generation.SkillInputAcknowledged).To(BeTrue())
	Expect(out.Transport.Generation.PromptInputAcknowledged).To(BeTrue())
	Expect(out.Transport.Generation.ReferencesAcknowledged).To(BeTrue())
	Expect(out.Transport.Generation.References).To(Equal(scope.Invocation.Generation.References))
	Expect(store.directory.cursors).To(Equal([]string{"", "pages/59.html", "pages/65.html"}))
	expectedHashes := []string{scope.Invocation.PromptRef.Hash, scope.Invocation.Generation.References[0].ObjectHash, inputs[0].Hash}
	for _, entry := range store.directory.entries {
		if entry.Kind == "file" {
			expectedHashes = append(expectedHashes, entry.Hash)
		}
	}
	Expect(objects.hashes).To(ConsistOf(expectedHashes))
	Expect(objects.pins).To(HaveLen(71))
	for _, pin := range objects.pins {
		Expect(pin.Kind).To(Equal("provider_continuation"))
		Expect(pin.OwnerID).To(Equal(a.ID))
	}
	Expect(directoryEntries(out)).To(HaveKey("read.html"))
})

var _ = It("C04FRAME1 whole-version generation refuses revoked or corrupt immutable context before dispatch and removes its incomplete frame", Label("unit"), func() {
	for _, mode := range []string{"revoked", "corrupt", "cancelled"} {
		d, a, inputs, store, objects := wholeGenerationFixture()
		var scope domain.ProviderScope
		Expect(json.Unmarshal(store.tx.job.Scope, &scope)).To(Succeed())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		expected := domain.ErrDenied
		switch mode {
		case "revoked":
			store.directory.afterFirstEntries = func() { store.tx.deny = true }
		case "cancelled":
			store.directory.afterFirstEntries = cancel
			expected = context.Canceled
		case "corrupt":
			expected = domain.ErrContentIntegrity
			for _, entry := range store.directory.entries {
				if entry.Path == "pages/65.html" {
					corrupt := bytes.Clone(objects.values[entry.Hash])
					corrupt[0] ^= 1
					objects.values[entry.Hash] = corrupt
				}
			}
		}
		frame, sha, e := d.frame(ctx, a, inputs, *scope.Invocation, "generation-thread")
		if frame != nil {
			DeferCleanup(func() { frame.Close(); os.Remove(frame.Name()) })
		}
		Expect(e).To(MatchError(expected), mode)
		Expect(frame).To(BeNil(), mode)
		Expect(sha).To(BeEmpty(), mode)
		Expect(store.directory.cursors).NotTo(BeEmpty(), "the failure must be inside directory delivery, not earlier setup")
		Expect(store.tx.metadata).To(BeNil(), "frame preparation alone cannot acknowledge source input")
		_, e = os.Stat(d.Binary + ".trace")
		Expect(os.IsNotExist(e)).To(BeTrue(), "this unit case must not start a provider process")
		staged, e := filepath.Glob(filepath.Join(d.Scratch, "provider-input-*"))
		Expect(e).NotTo(HaveOccurred())
		Expect(staged).To(BeEmpty(), mode)
	}
})
