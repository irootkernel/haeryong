package codex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type testStore struct {
	app.Store
	mu sync.Mutex
	tx *testTx
}

func (s *testStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(s.tx)
}

func (s *testStore) Job(_ context.Context, id string) (domain.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tx.Job(id)
}

type testTx struct {
	app.WriteTx
	job                     domain.Job
	metadata                *domain.ProviderTransport
	deny                    bool
	lookup                  *domain.ProviderLookup
	lookups                 int
	revokeAfterLookup       bool
	guardCalls, failGuardAt int
	recordError             error
	reads                   map[string]domain.CapturedRead
	objects                 map[string]domain.Object
}

func (t *testTx) GuardProviderPayload(string, string, bool) error {
	t.guardCalls++
	if t.failGuardAt > 0 && t.guardCalls == t.failGuardAt {
		return domain.ErrDenied
	}
	if t.deny {
		return domain.ErrDenied
	}
	return nil
}
func (t *testTx) GuardProviderLookup(string, string) error {
	t.lookups++
	if t.revokeAfterLookup {
		t.deny = true
	}
	return nil
}
func (t *testTx) RecordProviderLookup(_, _ string, v domain.ProviderLookup) error {
	t.lookup = &v
	return nil
}
func (t *testTx) Job(string) (domain.Job, error) { return t.job, nil }
func (t *testTx) RecordProviderTransport(_ string, v domain.ProviderTransport) error {
	if t.recordError != nil {
		return t.recordError
	}
	copy := v
	t.metadata = &copy
	return nil
}
func (t *testTx) CapturedRead(id string) (domain.CapturedRead, error) {
	if r, ok := t.reads[id]; ok {
		return r, nil
	}
	return domain.CapturedRead{ID: id, TargetSetID: "target", SourceJSON: []byte(`{"kind":"git","repo_id":"repo","exact_revision":"revision"}`), LocationJSON: []byte(`{"path":"a.txt"}`), BytesSHA: strings.Repeat("a", 64)}, nil
}
func (t *testTx) Object(hash string) (domain.Object, error) {
	if object, ok := t.objects[hash]; ok {
		return object, nil
	}
	return domain.Object{Hash: hash, State: "ready"}, nil
}

type testBytes struct{}

func (testBytes) Read(_ context.Context, _ domain.Object, _ domain.Pin, consume func(io.ReadSeeker) error) error {
	return consume(bytes.NewReader([]byte("Exact source: π\n")))
}

func fixture(mode string) (*Driver, domain.ProviderAttempt, []domain.ProviderInput, *testStore) {
	root := GinkgoT().TempDir()
	binary := filepath.Join(root, "codex")
	script := `#!/usr/bin/env python3
import json,sys,time,os,signal
mode=MODE
if '--version' in sys.argv:
 print('codex-cli 0.164.0');sys.exit(0)
if mode=='env':
 assert all(k not in os.environ for k in ['OPENAI_API_KEY','CODEX_API_KEY','OPENAI_ADMIN_KEY','AZURE_OPENAI_API_KEY','OPENAI_BASE_URL'])
if mode=='cleanup':
 signal.signal(signal.SIGTERM,signal.SIG_IGN)
 child=os.fork()
 if child==0:
  while True:time.sleep(1)
def send(x):
 encoded=json.dumps(x)+'\n'
 # Every response is fragmented, including strings and UTF-8 boundaries.
 for start in range(0,len(encoded),7):
  sys.stdout.write(encoded[start:start+7]);sys.stdout.flush()
def terminal(status='completed'):
 return {'id':'turn','status':status,'error':{'codexErrorInfo':'unauthorized' if mode=='backend_auth' else 'usageLimitExceeded'} if status=='failed' else None,'items':[{'type':'reasoning','summary':['PRIVATE_REASONING']},{'type':'agentMessage','phase':'commentary','text':'NOT_FINAL'},{'type':'agentMessage','phase':None if mode=='unknown_phase' else 'final_answer','text':'Grounded final answer'}]}
for line in sys.stdin:
 r=json.loads(line);method=r['method'];result={}
 with open(sys.argv[0]+'.trace','a') as trace:trace.write(json.dumps({'method':method,'pid':os.getpid(),'items_view':r.get('params',{}).get('itemsView')})+'\n')
 if method=='initialize' and mode=='initialize_error':
  send({'jsonrpc':'2.0','id':r['id'],'error':{'code':-32603}});continue
 if method=='initialize':result={'userAgent':'codex','codexHome':'PRIVATE_PATH','platformFamily':'unix','platformOs':'macos'}
 elif method=='initialized':
  if mode=='ignore_pipe':
   while True:time.sleep(1)
  continue
 elif method=='account/read':
  if mode=='stalled':time.sleep(30)
  result={'account':{'type':'apiKey' if mode=='api_key' else 'chatgpt','email':'PRIVATE_EMAIL','planType':'pro'},'requiresOpenaiAuth':False if mode=='auth_not_required' else None if mode=='auth_unknown' else True}
 elif method=='config/read':
  assert r['params']['cwd']==os.path.dirname(sys.argv[0])
  result={'config':None if mode=='null_config' else {'model_provider':'openai','instructions':'PRIVATE_INSTRUCTIONS','web_search':'disabled'}}
 elif method=='account/rateLimits/read':result={'ordinaryUsageAllowed':False if mode=='quota' else None}
 elif method=='model/list':result={'data':[{'model':'gpt-5.4'}],'nextCursor':None}
 elif method=='thread/start':result={'approvalPolicy':'never','cwd':r['params']['cwd'],'model':r['params'].get('model','gpt-5.4'),'reasoningEffort':r['params'].get('config',{}).get('model_reasoning_effort','high'),'modelProvider':'openai','sandbox':{'type':'readOnly','networkAccess':False},'thread':{'id':'thread'},'instructionSources':['PRIVATE_PATH'],'disabledPluginIds':[]}
 elif method=='turn/start':
  assert r['params']['input'][2]['text']=='Exact source: π\n'
  if mode=='eof':sys.exit(0)
  result={'turn':{'id':'turn','status':'inProgress','items':[]}}
 elif method=='thread/turns/list':
  if mode=='full_rpc_error' and r['params']['itemsView']=='full':
   send({'jsonrpc':'2.0','id':r['id'],'error':{'code':-32603}});continue
  value=terminal('interrupted' if mode=='history_interrupted' else 'completed')
  if mode=='summary':value['items']=[{'type':'agentMessage','phase':'final_answer','text':'First final'},{'type':'agentMessage','phase':'final_answer','text':'Second final'}]
  if mode=='nonfull':value['itemsView']='summary'
  if mode=='full_failed' and r['params']['itemsView']=='full':value=terminal('failed')
  result={'data':[value],'nextCursor':None}
  if r['params']['itemsView']=='notLoaded':
   result['data'][0]['items']=[]
  if mode=='multipage' and not r['params'].get('cursor'):
   poison=terminal();poison['id']='foreign';poison['items'][2]['text']='FOREIGN_PAGE_POISON';result={'data':[poison],'nextCursor':'page2'}
  if mode=='missing_full' and r['params']['itemsView']=='full':
   result['data'][0]['id']='foreign';result['data'][0]['items'][2]['text']='FOREIGN_PAGE_POISON'
  if mode=='wrong_lookup':result['data'][0]['id']='foreign'
 elif method=='turn/interrupt':
  send({'jsonrpc':'2.0','id':r['id'],'result':{}})
  if mode=='interrupt_ack_only':continue
  send({'jsonrpc':'2.0','method':'turn/completed','params':{'threadId':'thread','turn':terminal('interrupted')}});continue
 if method=='config/read' and mode=='custom_provider':result['config']['model_provider']='different'
 if method=='config/read' and mode=='custom_root_route':result['config']['openai_base_url']='https://different.invalid/v1'
 if method=='config/read' and mode=='custom_chatgpt_route':result['config']['chatgpt_base_url']='https://different.invalid/backend-api/'
 if method=='config/read' and mode=='official_default':result['config']['chatgpt_base_url']='https://chatgpt.com/backend-api/'
 if method=='config/read' and mode=='official_openai':result['config']['openai_base_url']='https://api.openai.com/v1'
 if method=='mcpServerStatus/list' and mode=='stalled_mcp':time.sleep(30)
 if method=='thread/start' and mode=='stalled_thread':time.sleep(30)
 if method=='mcpServerStatus/list' and mode=='unknown_mcp':
  send({'jsonrpc':'2.0','id':r['id'],'error':{'code':-32601,'message':'unsupported'}});continue
 if method=='mcpServerStatus/list':result={'data':[{'runtimeStatus':'connected','tools':{'PRIVATE_TOOL':{'description':'PRIVATE_DESCRIPTION'}}}],'nextCursor':None}
 if method=='mcpServerStatus/list' and mode=='missing_tools':del result['data'][0]['tools']
 if method=='thread/start' and mode=='wrong_mode':result['sandbox']['networkAccess']=True
 if method=='thread/start' and mode=='wrong_cwd':result['cwd']='/unrelated'
 if method=='thread/start' and mode=='wrong_approval':result['approvalPolicy']='on-request'
 if method=='thread/start' and mode=='wrong_model':result['model']='different'
 if method=='thread/start' and mode=='wrong_effort':result['reasoningEffort']='different'
 if method=='initialize' and mode=='server_request':
  send({'jsonrpc':'2.0','id':'unsolicited','method':'permission/request','params':{}})
  denied=json.loads(sys.stdin.readline());assert denied['error']['code']==-32601
  with open(sys.argv[0]+'.denied','w') as f:f.write('denied')
 send({'jsonrpc':'2.0','id':r['id'],'result':result})
 if method=='turn/start' and mode not in ('cancel','interrupt_ack_only'):
  poison=terminal();poison['items']=[{'type':'agentMessage','phase':'final_answer','text':'WRONG_THREAD_POISON'}]
  send({'jsonrpc':'2.0','method':'turn/completed','params':{'threadId':'unrelated','turn':poison}})
  poison['id']='unrelated-turn';poison['items'][0]['text']='WRONG_TURN_POISON'
  send({'jsonrpc':'2.0','method':'turn/completed','params':{'threadId':'thread','turn':poison}})
  value=terminal('failed') if mode in ('backend_auth','backend_quota') else terminal()
  if mode=='malformed_completion':
   send({'jsonrpc':'2.0','method':'turn/completed','params':{'threadId':'','turn':value}});continue
  if mode in ('summary','nonfull','full_rpc_error'):
   value['itemsView']='summary';value['items']=[{'type':'agentMessage','phase':'final_answer','text':'Second final'}]
  send({'jsonrpc':'2.0','method':'turn/completed','params':{'threadId':'thread','turn':value}})
if mode=='cleanup':
 while True:time.sleep(1)
`
	encoded, _ := json.Marshal(mode)
	script = strings.Replace(script, "MODE", string(encoded), 1)
	Expect(os.WriteFile(binary, []byte(script), 0700)).To(Succeed())
	tx := &testTx{}
	store := &testStore{tx: tx}
	inv := domain.ProviderInvocation{Prompt: "Use the supplied source", CWD: root}
	read, _ := tx.CapturedRead("read")
	sha, e := SemanticRequestSHA("owner", []byte(`{"version_id":"version"}`), inv, []domain.CapturedRead{read})
	Expect(e).NotTo(HaveOccurred())
	scope, _ := json.Marshal(domain.ProviderScope{ProviderID: "codex", PrincipalID: "owner", Target: []byte(`{"version_id":"version"}`), Invocation: &inv, ReadIDs: []string{"read"}})
	tx.job = domain.Job{ID: "job", Scope: scope}
	d := New(store, testBytes{}, root)
	d.Binary = binary
	d.BinarySHA = fmt.Sprintf("%x", sha256.Sum256([]byte(script)))
	return d, domain.ProviderAttempt{ID: "attempt", JobID: "job", RequestSHA: sha, TargetSetID: "target"}, []domain.ProviderInput{{AttemptID: "attempt", ReadID: "read", Hash: read.BytesSHA}}, store
}

var _ = It("C03 accepts exact delivered input only after ACK and retains only explicit public final output", Label("integration"), func() {
	d, a, inputs, s := fixture("complete")
	fenced, acknowledged := false, false
	out, e := d.Execute(context.Background(), a, inputs, func() error { fenced = true; return nil }, func(p work.ProviderProgress) error {
		Expect(fenced).To(BeTrue())
		Expect(p.RequestID).To(Equal("turn"))
		Expect(p.SessionID).To(Equal("thread"))
		Expect(p.Observed).To(HaveLen(1))
		Expect(p.Observed[0].ObservedOrigin).To(Equal("daemon_supplied"))
		acknowledged = true
		return nil
	})
	Expect(e).NotTo(HaveOccurred())
	Expect(acknowledged).To(BeTrue())
	Expect(out.CompletionObserved).To(BeTrue())
	Expect(out.State).To(Equal("result_saved"))
	data, e := io.ReadAll(out.Body)
	Expect(e).NotTo(HaveOccurred())
	Expect(out.Body.Close()).To(Succeed())
	Expect(string(data)).To(Equal("Grounded final answer"))
	Expect(out.Transport.LocalClosed).To(BeTrue())
	Expect(out.Transport.BackendState).To(Equal("completed"))
	Expect(out.Transport.DriverOutcome).To(Equal("result_ready"))
	Expect(s.tx.metadata.AcceptedAt).NotTo(BeEmpty())
	Expect(s.tx.metadata.FrameSHA).To(HaveLen(64))
	Expect(s.tx.metadata.InstructionCount).To(Equal(1))
	files, e := filepath.Glob(filepath.Join(d.Scratch, "provider-*-*"))
	Expect(e).NotTo(HaveOccurred())
	Expect(files).To(BeEmpty())
})
var _ = It("C03 refuses API-key, ambiguous auth and disallowed usage before dispatch; unknown quota stays unknown", Label("integration"), func() {
	for _, mode := range []string{"api_key", "quota", "auth_not_required", "auth_unknown", "custom_root_route", "custom_chatgpt_route", "custom_provider"} {
		d, a, inputs, _ := fixture(mode)
		fenced := false
		out, e := d.Execute(context.Background(), a, inputs, func() error { fenced = true; return nil }, func(work.ProviderProgress) error { Fail("must not acknowledge"); return nil })
		Expect(e).NotTo(HaveOccurred())
		Expect(fenced).To(BeFalse())
		Expect(out.State).To(BeEmpty())
		Expect(out.Body).To(BeNil())
	}
	d, _, _, _ := fixture("complete")
	status := d.Status(context.Background(), d.Scratch)
	Expect(status.State).To(Equal("quota_unavailable"))
	Expect(status.OrdinaryUsageAllowed).To(BeNil())
	Expect(status.CatalogProvesAccess).To(BeFalse())
	raw, e := json.Marshal(status)
	Expect(e).NotTo(HaveOccurred())
	Expect(string(raw)).NotTo(ContainSubstring("PRIVATE_"))
})
var _ = It("C03 EOF after fenced send is unknown and completed unknown-phase output is unavailable", Label("integration"), func() {
	for _, mode := range []string{"eof", "unknown_phase"} {
		d, a, inputs, _ := fixture(mode)
		acknowledged := false
		out, e := d.Execute(context.Background(), a, inputs, func() error { return nil }, func(work.ProviderProgress) error { acknowledged = true; return nil })
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Body).To(BeNil())
		if mode == "eof" {
			Expect(out.State).To(Equal("outcome_unknown"))
			Expect(acknowledged).To(BeFalse())
			Expect(out.CompletionObserved).To(BeFalse())
		} else {
			Expect(out.CompletionObserved).To(BeTrue())
			Expect(out.Reason).To(Equal("result_unavailable"))
		}
	}
})
var _ = It("C03 interrupt ACK alone never confirms cessation; exact backend interrupted does", Label("integration"), func() {
	for _, mode := range []string{"cancel", "interrupt_ack_only"} {
		d, a, inputs, _ := fixture(mode)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		done := make(chan work.ProviderOutcome, 1)
		accepted := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			out, _ := d.Execute(ctx, a, inputs, func() error { return nil }, func(work.ProviderProgress) error { close(accepted); return nil })
			done <- out
		}()
		<-accepted
		// The map is installed immediately after the durable progress callback.
		Eventually(func() bool { d.mu.Lock(); defer d.mu.Unlock(); return len(d.active) == 1 }).Should(BeTrue())
		Expect(d.Cancel(ctx, a)).To(Succeed())
		if mode == "interrupt_ack_only" {
			Consistently(done, 100*time.Millisecond).ShouldNot(Receive())
			cancel()
		}
		out := <-done
		cancel()
		if mode == "cancel" {
			Expect(out.State).To(Equal("cancelled"))
			Expect(out.Reason).To(Equal("cancel_confirmed"))
		} else {
			Expect(out.State).To(Equal("outcome_unknown"))
		}
	}
})
var _ = It("C03 semantic identity ignores local read/capture IDs but changes exact bytes and prompt", Label("unit"), func() {
	inv := domain.ProviderInvocation{Prompt: "question", CWD: "/source"}
	read := domain.CapturedRead{ID: "one", SourceJSON: []byte(`{"kind":"files","source_id":"s","snapshot_id":"one"}`), LocationJSON: []byte(`{"path":"a"}`), BytesSHA: "first"}
	first, e := SemanticRequestSHA("owner", []byte(`{}`), inv, []domain.CapturedRead{read})
	Expect(e).NotTo(HaveOccurred())
	read.ID = "two"
	read.SourceJSON = []byte(`{"snapshot_id":"two","source_id":"s","kind":"files"}`)
	second, e := SemanticRequestSHA("owner", []byte(`{}`), inv, []domain.CapturedRead{read})
	Expect(e).NotTo(HaveOccurred())
	Expect(second).To(Equal(first))
	read.BytesSHA = "changed"
	third, e := SemanticRequestSHA("owner", []byte(`{}`), inv, []domain.CapturedRead{read})
	Expect(e).NotTo(HaveOccurred())
	Expect(third).NotTo(Equal(first))
	inv.Prompt = "different question"
	fourth, e := SemanticRequestSHA("owner", []byte(`{}`), inv, []domain.CapturedRead{read})
	Expect(e).NotTo(HaveOccurred())
	Expect(fourth).NotTo(Equal(third))
})

var _ = It("C03 status-only reconciliation never loads bodies; policy is rechecked before full history and consumption", Label("integration"), func() {
	for _, allowed := range []bool{false, true} {
		d, a, _, s := fixture("lookup")
		a.RequestID = "turn"
		a.SessionID = "thread"
		a.Transport = &domain.ProviderTransport{Provider: "codex", Version: Version, BinarySHA: d.BinarySHA, SetupThread: "thread", CWD: d.Scratch}
		if allowed {
			s.tx.revokeAfterLookup = true
		}
		out, e := d.Reconcile(context.Background(), a, allowed)
		Expect(e).NotTo(HaveOccurred())
		Expect(out.CompletionObserved).To(BeTrue())
		Expect(out.Body).To(BeNil())
		Expect(out.Reason).To(Equal("result_unavailable"))
		raw, e := os.ReadFile(d.Binary + ".trace")
		Expect(e).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"items_view": "notLoaded"`))
		Expect(string(raw)).NotTo(ContainSubstring(`"items_view": "full"`))
		Expect(string(raw)).NotTo(ContainSubstring(`"method": "turn/start"`))
		Expect(out.Lookup.BackendState).To(Equal("completed"))
		Expect(out.Lookup.LocalClosed).To(BeTrue())
	}
	d, a, _, s := fixture("lookup")
	a.RequestID = "turn"
	a.SessionID = "thread"
	a.Transport = &domain.ProviderTransport{Provider: "codex", Version: Version, BinarySHA: d.BinarySHA, SetupThread: "thread", CWD: d.Scratch}
	out, e := d.Reconcile(context.Background(), a, true)
	Expect(e).NotTo(HaveOccurred())
	Expect(out.Body).NotTo(BeNil())
	s.mu.Lock()
	s.tx.deny = true
	s.mu.Unlock()
	_, e = io.ReadAll(out.Body)
	Expect(e).To(MatchError(domain.ErrDenied))
	Expect(out.Body.Close()).To(Succeed())
})
var _ = It("C03 backend auth expiry and quota exhaustion retain normalized failure without fallback", Label("integration"), func() {
	for _, mode := range []string{"backend_auth", "backend_quota"} {
		d, a, inputs, _ := fixture(mode)
		out, e := d.Execute(context.Background(), a, inputs, func() error { return nil }, func(work.ProviderProgress) error { return nil })
		Expect(e).NotTo(HaveOccurred())
		Expect(out.State).To(Equal("failed"))
		Expect(out.CompletionObserved).To(BeFalse())
		Expect(out.Body).To(BeNil())
		if mode == "backend_auth" {
			Expect(out.Reason).To(Equal("auth_required"))
		} else {
			Expect(out.Reason).To(Equal("quota_exhausted"))
		}
	}
})
var _ = It("C03 real owned group ignores TERM, then KILL drains leader/descendant/readers with no false backend completion", Label("integration"), func() {
	d, a, inputs, _ := fixture("cleanup")
	out, e := d.Execute(context.Background(), a, inputs, func() error { return nil }, func(work.ProviderProgress) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	Expect(out.Transport.LocalClosed).To(BeTrue())
	Expect(out.Transport.CleanupState).To(Equal("drained"))
	Expect(out.Transport.BackendState).To(Equal("completed"))
	Expect(out.Body.Close()).To(Succeed())
})
var _ = It("C03 denied KILL and a live owned leader return bounded cleanup uncertainty", Label("integration"), func() {
	d, _, _, _ := fixture("cleanup")
	binary, _, e := d.binary(context.Background())
	Expect(e).NotTo(HaveOccurred())
	t, e := start(context.Background(), binary)
	Expect(e).NotTo(HaveOccurred())
	t.signal = func(group int, sig syscall.Signal) error {
		if sig == syscall.SIGKILL {
			return syscall.EPERM
		}
		return syscall.Kill(group, sig)
	}
	defer func() {
		Expect(syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)).To(Succeed())
		Eventually(t.waitDone, 2*time.Second).Should(BeClosed())
	}()
	began := time.Now()
	Expect(t.close()).To(MatchError(errCleanup))
	AddReportEntry("cleanup elapsed (informational)", time.Since(began))
	Expect(time.Since(began)).To(BeNumerically("<", 20*time.Second))
	Expect(t.cleanupReason).To(Equal("signal_error_cleanup_unknown"))
})

var _ = It("C03 two-input reorder preserves the identical canonical wire order and semantic request", Label("unit"), func() {
	d, a, inputs, s := fixture("complete")
	first, _ := s.tx.CapturedRead("read")
	second := first
	second.ID = "read-two"
	second.LocationJSON = []byte(`{"path":"b.txt"}`)
	s.tx.reads = map[string]domain.CapturedRead{"read": first, "read-two": second}
	var scope domain.ProviderScope
	Expect(json.Unmarshal(s.tx.job.Scope, &scope)).To(Succeed())
	scope.ReadIDs = []string{first.ID, second.ID}
	s.tx.job.Scope, _ = json.Marshal(scope)
	inv := domain.ProviderInvocation{Prompt: "same prompt", CWD: d.Scratch}
	sha1, e := SemanticRequestSHA("owner", []byte(`{}`), inv, []domain.CapturedRead{first, second})
	Expect(e).NotTo(HaveOccurred())
	sha2, e := SemanticRequestSHA("owner", []byte(`{}`), inv, []domain.CapturedRead{second, first})
	Expect(e).NotTo(HaveOccurred())
	Expect(sha1).To(Equal(sha2))
	inputs = append(inputs, domain.ProviderInput{AttemptID: a.ID, ReadID: second.ID, Hash: second.BytesSHA})
	ordered1, e := d.orderedInputs(context.Background(), a, inputs)
	Expect(e).NotTo(HaveOccurred())
	ordered2, e := d.orderedInputs(context.Background(), a, []domain.ProviderInput{inputs[1], inputs[0]})
	Expect(e).NotTo(HaveOccurred())
	Expect(ordered1).To(Equal(ordered2))
	frame1, digest1, e := d.frame(context.Background(), a, ordered1, inv, "same-thread")
	Expect(e).NotTo(HaveOccurred())
	defer os.Remove(frame1.Name())
	defer frame1.Close()
	frame2, digest2, e := d.frame(context.Background(), a, ordered2, inv, "same-thread")
	Expect(e).NotTo(HaveOccurred())
	defer os.Remove(frame2.Name())
	defer frame2.Close()
	bytes1, e := io.ReadAll(frame1)
	Expect(e).NotTo(HaveOccurred())
	bytes2, e := io.ReadAll(frame2)
	Expect(e).NotTo(HaveOccurred())
	Expect(bytes1).To(Equal(bytes2))
	Expect(digest1).To(Equal(digest2))
})

type unsupportedBytes struct{}

func (unsupportedBytes) Read(_ context.Context, _ domain.Object, _ domain.Pin, consume func(io.ReadSeeker) error) error {
	return consume(bytes.NewReader([]byte{0xff, 0x00}))
}

var _ = It("C03 unsupported text format refuses the affected attempt before dispatch without omitting input", Label("integration"), func() {
	d, a, inputs, s := fixture("complete")
	d.Bytes = unsupportedBytes{}
	fenced := false
	out, e := d.Execute(context.Background(), a, inputs, func() error { fenced = true; return nil }, func(work.ProviderProgress) error { Fail("unsupported input cannot be observed"); return nil })
	Expect(e).NotTo(HaveOccurred())
	Expect(fenced).To(BeFalse())
	Expect(out.State).To(BeEmpty())
	Expect(out.Reason).To(Equal("unsupported_input_format"))
	Expect(s.tx.metadata.AcceptedAt).To(BeEmpty())
	Expect(out.Transport.LocalClosed).To(BeTrue())
})

var _ = It("C03 keeps provider rate/session/context limits distinct from actual exhausted allowance", Label("unit"), func() {
	for native, expected := range map[string]string{"usageLimitExceeded": "quota_exhausted", "rateLimitExceeded": "provider_rate_limited", "sessionBudgetExceeded": "provider_budget_exhausted", "contextWindowExceeded": "provider_context_limit", "unauthorized": "auth_required"} {
		value := turn{}
		Expect(json.Unmarshal([]byte(`{"error":{"codexErrorInfo":"`+native+`"}}`), &value)).To(Succeed())
		Expect(errorReason(value)).To(Equal(expected))
	}
})

var _ = It("C03 cancellation racing accepted-ID registration is rechecked against the owned exact turn", Label("integration"), func() {
	d, a, inputs, s := fixture("cancel")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, e := d.Execute(ctx, a, inputs, func() error { return nil }, func(work.ProviderProgress) error {
		s.mu.Lock()
		s.tx.job.CancelRequested = true
		s.mu.Unlock()
		return nil
	})
	Expect(e).NotTo(HaveOccurred())
	Expect(out.State).To(Equal("cancelled"))
	Expect(out.Reason).To(Equal("cancel_confirmed"))
	trace, e := os.ReadFile(d.Binary + ".trace")
	Expect(e).NotTo(HaveOccurred())
	Expect(strings.Count(string(trace), `"method": "turn/interrupt"`)).To(Equal(1))
})

var _ = It("C03 corrected preflight reports precise refusals and accepts both ordinary documented URLs", Label("integration"), func() {
	for mode, want := range map[string][2]string{"api_key": {"auth_required", "unsupported_authentication"}, "quota": {"paused", "usage_not_allowed"}, "auth_not_required": {"auth_required", "unsupported_authentication"}, "auth_unknown": {"auth_required", "unsupported_authentication"}, "custom_root_route": {"paused", "unsupported_provider"}, "custom_chatgpt_route": {"paused", "unsupported_provider"}, "null_config": {"provider_error", "config_unavailable"}, "official_default": {"quota_unavailable", "quota_unknown"}, "official_openai": {"quota_unavailable", "quota_unknown"}} {
		d, a, inputs, _ := fixture(mode)
		status := d.Status(context.Background(), d.Scratch)
		Expect(status.State).To(Equal(want[0]), mode)
		Expect(status.Reason).To(Equal(want[1]), mode)
		if mode == "quota" {
			Expect(status.OrdinaryUsageAllowed).NotTo(BeNil())
			Expect(*status.OrdinaryUsageAllowed).To(BeFalse())
		} else {
			Expect(status.OrdinaryUsageAllowed).To(BeNil())
		}
		fenced := false
		out, e := d.Execute(context.Background(), a, inputs, func() error { fenced = true; return nil }, func(work.ProviderProgress) error { return nil })
		Expect(e).NotTo(HaveOccurred())
		if strings.HasPrefix(mode, "official_") {
			Expect(fenced).To(BeTrue())
			Expect(out.Body.Close()).To(Succeed())
		} else {
			Expect(fenced).To(BeFalse())
			Expect(out.State).To(BeEmpty())
			if want[0] == "auth_required" {
				Expect(out.Reason).To(Equal("auth_required"))
			} else {
				Expect(out.Reason).To(Equal(want[1]))
			}
		}
	}
})
var _ = It("C03 full exact history recovers all final messages and rejects a nonfull response even with a final", Label("integration"), func() {
	for _, mode := range []string{"summary", "nonfull"} {
		d, a, inputs, _ := fixture(mode)
		out, e := d.Execute(context.Background(), a, inputs, func() error { return nil }, func(work.ProviderProgress) error { return nil })
		Expect(e).NotTo(HaveOccurred())
		Expect(out.CompletionObserved).To(BeTrue())
		Expect(out.Transport.BackendState).To(Equal("completed"))
		if mode == "summary" {
			data, e := io.ReadAll(out.Body)
			Expect(e).NotTo(HaveOccurred())
			Expect(string(data)).To(Equal("First final\nSecond final"))
			Expect(out.Body.Close()).To(Succeed())
		} else {
			Expect(out.Body).To(BeNil())
			Expect(out.Reason).To(Equal("result_unavailable"))
		}
		trace, e := os.ReadFile(d.Binary + ".trace")
		Expect(e).NotTo(HaveOccurred())
		Expect(strings.Count(string(trace), `"method": "turn/start"`)).To(Equal(1))
		Expect(string(trace)).To(ContainSubstring(`"items_view": "full"`))
	}
	for raw, want := range map[string]string{`{"id":"t"}`: "full", `{"id":"t","itemsView":null}`: "invalid", `{"id":"t","itemsView":"unknown"}`: "unknown"} {
		var v turn
		Expect(json.Unmarshal([]byte(raw), &v)).To(Succeed())
		Expect(v.ItemsView).To(Equal(want))
	}
})
var _ = It("C03 persisted interrupted remains unresolved and never proves cessation", Label("integration"), func() {
	d, a, _, _ := fixture("history_interrupted")
	a.RequestID = "turn"
	a.SessionID = "thread"
	a.Transport = &domain.ProviderTransport{Provider: "codex", Version: Version, BinarySHA: d.BinarySHA, SetupThread: "thread", CWD: d.Scratch}
	out, e := d.Reconcile(context.Background(), a, false)
	Expect(e).NotTo(HaveOccurred())
	Expect(out.State).To(Equal("outcome_unknown"))
	Expect(out.Reason).To(Equal("transport_unknown"))
	Expect(out.Lookup.BackendState).To(Equal("interrupted"))
	Expect(out.Lookup.Source).To(Equal("history"))
	Expect(out.Body).To(BeNil())
})
var _ = It("C03 binary hash and version are refused before app-server startup", Label("integration"), func() {
	for _, mode := range []string{"hash", "version"} {
		d, a, inputs, _ := fixture("complete")
		if mode == "hash" {
			d.BinarySHA = strings.Repeat("0", 64)
		} else {
			data, e := os.ReadFile(d.Binary)
			Expect(e).NotTo(HaveOccurred())
			data = bytes.ReplaceAll(data, []byte("codex-cli 0.164.0"), []byte("codex-cli 0.165.0"))
			Expect(os.WriteFile(d.Binary, data, 0700)).To(Succeed())
			d.BinarySHA = fmt.Sprintf("%x", sha256.Sum256(data))
		}
		out, e := d.Execute(context.Background(), a, inputs, func() error { Fail("unverified binary fenced"); return nil }, func(work.ProviderProgress) error { return nil })
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Reason).To(Equal("unsupported_version"))
		_, e = os.Stat(d.Binary + ".trace")
		Expect(os.IsNotExist(e)).To(BeTrue())
	}
})
var _ = It("C03 inherited capabilities are bounded declarations and observed connected counts only", Label("integration"), func() {
	d, a, inputs, _ := fixture("complete")
	out, e := d.Execute(context.Background(), a, inputs, func() error { return nil }, func(work.ProviderProgress) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	Expect(out.Body.Close()).To(Succeed())
	c := out.Transport.Capabilities
	Expect(c.WebSearch).To(Equal("disabled"))
	Expect(c.MCPComplete).To(BeTrue())
	Expect(*c.MCPConnected).To(Equal(1))
	Expect(*c.MCPTools).To(Equal(1))
	Expect(c.AppsAvailable).To(BeNil())
	raw, e := json.Marshal(c)
	Expect(e).NotTo(HaveOccurred())
	Expect(string(raw)).NotTo(ContainSubstring("PRIVATE_"))
})
var _ = It("C03 actual unsolicited native request is denied without executing its capability", Label("integration"), func() {
	d, a, inputs, _ := fixture("server_request")
	out, e := d.Execute(context.Background(), a, inputs, func() error { return nil }, func(work.ProviderProgress) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	Expect(out.Body.Close()).To(Succeed())
	data, e := os.ReadFile(d.Binary + ".denied")
	Expect(e).NotTo(HaveOccurred())
	Expect(string(data)).To(Equal("denied"))
})
var _ = It("C03 completed spool survives caller cancellation but still obeys current payload revocation", Label("integration"), func() {
	d, a, inputs, s := fixture("complete")
	ctx, cancel := context.WithCancel(context.Background())
	out, e := d.Execute(ctx, a, inputs, func() error { return nil }, func(work.ProviderProgress) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	cancel()
	data, e := io.ReadAll(out.Body)
	Expect(e).NotTo(HaveOccurred())
	Expect(string(data)).To(Equal("Grounded final answer"))
	s.mu.Lock()
	s.tx.deny = true
	s.mu.Unlock()
	_, e = out.Body.Read(make([]byte, 1))
	Expect(e).To(MatchError(domain.ErrDenied))
	Expect(out.Body.Close()).To(Succeed())
})

var _ = It("C03 strips only child credential overrides and leaves the parent's test environment intact", Label("integration"), func() {
	keys := []string{"OPENAI_API_KEY", "CODEX_API_KEY", "OPENAI_ADMIN_KEY", "AZURE_OPENAI_API_KEY", "OPENAI_BASE_URL"}
	for _, key := range keys {
		GinkgoT().Setenv(key, "PUBLIC_TEST_OVERRIDE")
	}
	d, a, inputs, _ := fixture("env")
	out, e := d.Execute(context.Background(), a, inputs, func() error { return nil }, func(work.ProviderProgress) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	Expect(out.Body.Close()).To(Succeed())
	for _, key := range keys {
		Expect(os.Getenv(key)).To(Equal("PUBLIC_TEST_OVERRIDE"))
	}
})
var _ = It("C03 returned permission/CWD/model/effort mismatches refuse the affected request before dispatch", Label("integration"), func() {
	for _, mode := range []string{"wrong_mode", "wrong_cwd", "wrong_approval", "wrong_model", "wrong_effort"} {
		d, a, inputs, s := fixture(mode)
		var scope domain.ProviderScope
		Expect(json.Unmarshal(s.tx.job.Scope, &scope)).To(Succeed())
		scope.Invocation.Model = "selected-model"
		scope.Invocation.Effort = "medium"
		r, e := s.tx.CapturedRead("read")
		Expect(e).NotTo(HaveOccurred())
		a.RequestSHA, e = SemanticRequestSHA(scope.PrincipalID, scope.Target, *scope.Invocation, []domain.CapturedRead{r})
		Expect(e).NotTo(HaveOccurred())
		scope.RequestSHA = a.RequestSHA
		s.tx.job.Scope, _ = json.Marshal(scope)
		out, e := d.Execute(context.Background(), a, inputs, func() error { Fail("mismatched mode fenced"); return nil }, func(work.ProviderProgress) error { return nil })
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Reason).To(Equal("mode_unavailable"), mode)
		Expect(out.Body).To(BeNil())
	}
})
var _ = It("C03 stalled preflight ends on caller cancellation with no dispatch", Label("integration"), func() {
	d, a, inputs, _ := fixture("stalled")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	began := time.Now()
	out, e := d.Execute(ctx, a, inputs, func() error { Fail("stalled setup fenced"); return nil }, func(work.ProviderProgress) error { return nil })
	Expect(e).To(HaveOccurred())
	Expect(out.State).To(BeEmpty())
	Expect(time.Since(began)).To(BeNumerically("<", 3*time.Second))
})
var _ = It("C03 interrupt reservation is once-only and its deadline closes an actually stalled pipe writer", Label("integration"), func() {
	d, a, _, _ := fixture("ignore_pipe")
	binary, _, e := d.binary(context.Background())
	Expect(e).NotTo(HaveOccurred())
	t, e := start(context.Background(), binary)
	Expect(e).NotTo(HaveOccurred())
	defer t.close()
	d.mu.Lock()
	d.active[a.ID] = activeTurn{transport: t, thread: "thread", turn: "turn", interrupt: make(chan struct{})}
	d.mu.Unlock()
	writeDone := make(chan error, 1)
	go func() { writeDone <- t.send(map[string]string{"payload": strings.Repeat("x", 1024*1024)}) }()
	Eventually(func() bool {
		if t.writes.TryLock() {
			t.writes.Unlock()
			return false
		}
		return true
	}, time.Second).Should(BeTrue())
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	replies := make(chan error, 2)
	began := time.Now()
	go func() { replies <- d.Cancel(ctx, a) }()
	go func() { replies <- d.Cancel(ctx, a) }()
	Eventually(replies, 3*time.Second).Should(Receive())
	Eventually(replies, 3*time.Second).Should(Receive())
	Eventually(writeDone, 3*time.Second).Should(Receive(HaveOccurred()))
	Expect(time.Since(began)).To(BeNumerically("<", 3*time.Second))
	// Cancellation closes the pipe asynchronously; the Execute owner joins
	// that same close before claiming drain. Await it here too.
	Expect(t.close()).To(Succeed())
	Expect(t.done).To(BeClosed())
})

var _ = It("C03 stalled capability observation cannot fence, while prompt unknown telemetry remains supported", Label("integration"), func() {
	d, a, inputs, _ := fixture("stalled_mcp")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	out, e := d.Execute(ctx, a, inputs, func() error { Fail("expired capability setup fenced"); return nil }, func(work.ProviderProgress) error { return nil })
	Expect(e).To(HaveOccurred())
	Expect(out.State).To(BeEmpty())
	Expect(out.Body).To(BeNil())
	d, a, inputs, _ = fixture("unknown_mcp")
	out, e = d.Execute(context.Background(), a, inputs, func() error { return nil }, func(work.ProviderProgress) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	Expect(out.CompletionObserved).To(BeTrue())
	Expect(out.Transport.Capabilities.MCPComplete).To(BeFalse())
	Expect(out.Transport.Capabilities.MCPConnected).To(BeNil())
	Expect(out.Body.Close()).To(Succeed())
})

var _ = It("C03 cancellation before accepted-ID registration unblocks an actual stalled turn frame writer", Label("integration"), func() {
	d, _, _, _ := fixture("ignore_pipe")
	binary, _, e := d.binary(context.Background())
	Expect(e).NotTo(HaveOccurred())
	transport, e := start(context.Background(), binary)
	Expect(e).NotTo(HaveOccurred())
	defer transport.close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	began := time.Now()
	_, e = transport.submit(ctx, strings.NewReader(`{"payload":"`+strings.Repeat("x", 1024*1024)+`"}`))
	Expect(e).To(HaveOccurred())
	Expect(time.Since(began)).To(BeNumerically("<", 3*time.Second))
	Expect(transport.close()).To(Succeed())
})

var _ = It("C03 exact-ID multipage history excludes poison and missing full output preserves completed metadata", Label("integration"), func() {
	for _, mode := range []string{"multipage", "missing_full", "wrong_lookup"} {
		d, a, _, _ := fixture(mode)
		a.RequestID = "turn"
		a.SessionID = "thread"
		a.Transport = &domain.ProviderTransport{Provider: "codex", Version: Version, BinarySHA: d.BinarySHA, SetupThread: "thread", CWD: d.Scratch}
		out, e := d.Reconcile(context.Background(), a, true)
		if mode == "missing_full" {
			Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue())
		} else {
			Expect(e).NotTo(HaveOccurred())
		}
		switch mode {
		case "multipage":
			Expect(out.CompletionObserved).To(BeTrue())
			data, e := io.ReadAll(out.Body)
			Expect(e).NotTo(HaveOccurred())
			Expect(string(data)).To(Equal("Grounded final answer"))
			Expect(out.Body.Close()).To(Succeed())
		case "missing_full":
			Expect(out.Body).To(BeNil())
			Expect(out.CompletionObserved).To(BeTrue())
			Expect(out.Reason).To(Equal("result_retrieval_pending"))
			Expect(out.State).To(Equal("outcome_unknown"))
			Expect(out.Lookup.BackendState).To(Equal("completed"))
		default:
			Expect(out.Body).To(BeNil())
			Expect(out.CompletionObserved).To(BeFalse())
			Expect(out.State).To(Equal("outcome_unknown"))
			Expect(out.Lookup).To(BeNil())
		}
	}
})

var _ = It("C03 omitted connected-tool catalog remains unknown rather than zero initialized tools", Label("integration"), func() {
	d, a, inputs, _ := fixture("missing_tools")
	out, e := d.Execute(context.Background(), a, inputs, func() error { return nil }, func(work.ProviderProgress) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	Expect(out.CompletionObserved).To(BeTrue())
	Expect(out.Transport.Capabilities.MCPTools).To(BeNil())
	Expect(out.Transport.Capabilities.MCPComplete).To(BeFalse())
	Expect(out.Body.Close()).To(Succeed())
})

var _ = It("C03 malformed terminal envelope cannot complete an owned request", Label("integration"), func() {
	d, a, inputs, _ := fixture("malformed_completion")
	out, e := d.Execute(context.Background(), a, inputs, func() error { return nil }, func(work.ProviderProgress) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	Expect(out.State).To(Equal("outcome_unknown"))
	Expect(out.CompletionObserved).To(BeFalse())
	Expect(out.Body).To(BeNil())
	Expect(out.RequestID).To(Equal("turn"))
})

type failingNominationBytesSpy struct {
	reads int
}

func (s *failingNominationBytesSpy) Read(_ context.Context, _ domain.Object, _ domain.Pin, _ func(io.ReadSeeker) error) error {
	s.reads++
	return errors.New("unexpected explanation or source object read during Stage-1 semantic nomination")
}

type failingNominationStoreSpy struct {
	app.Store
	writes int
}

func (s *failingNominationStoreSpy) Write(_ context.Context, _ func(app.WriteTx) error) error {
	s.writes++
	return errors.New("unexpected store write/object lookup during Stage-1 semantic nomination")
}

var _ = It("builds Stage-1 semantic nomination frame with near-limit adaptive cards (>2KiB, <=8KiB) and 64-card batches without duplicating cards or reading explanation objects and enforces <=144KiB cap (TASK-008-02)", Label("unit"), func() {
	scratchDir := GinkgoT().TempDir()
	bytesSpy := &failingNominationBytesSpy{}
	storeSpy := &failingNominationStoreSpy{}
	d := &Driver{
		Scratch: scratchDir,
		Bytes:   bytesSpy,
		Store:   storeSpy,
	}

	cards := make([]domain.MaintenanceProjectedCard, 64)
	exps := make([]domain.MaintenanceExplanation, 64)
	for i := 0; i < 64; i++ {
		uid := fmt.Sprintf("u%02d", i+1)
		pagePath := fmt.Sprintf("page-%02d.html", i+1)
		regID := fmt.Sprintf("region-%02d", i+1)
		cards[i] = domain.MaintenanceProjectedCard{
			UnitID:          uid,
			UnitKind:        "region",
			ProjectID:       "00000000-0000-4000-8000-000000000001",
			VersionID:       "00000000-0000-4000-8000-000000000003",
			Title:           fmt.Sprintf(`Card "%s" — 한국어 제목`, uid),
			Headings:        []string{fmt.Sprintf("Heading %s", uid)},
			DeclaredTopics:  []string{"bounded-nomination"},
			SourcePaths:     []string{"internal/application/scan/nomination.go"},
			BodyExcerpt:     fmt.Sprintf("unique-card-payload-%s: %s", uid, strings.Repeat(`"quoted\segment" 한국어 `, 20)),
			ExcerptCoverage: "truncated",
			TotalBodyBytes:  2048,
		}
		exps[i] = domain.MaintenanceExplanation{
			Role:       "candidate",
			UnitID:     uid,
			UnitKind:   "region",
			ItemID:     fmt.Sprintf("item-%06d", i+1),
			ProjectID:  cards[i].ProjectID,
			DocumentID: "00000000-0000-4000-8000-000000000002",
			VersionID:  cards[i].VersionID,
			PagePath:   &pagePath,
			RegionID:   &regID,
			Title:      cards[i].Title,
			ObjectHash: strings.Repeat(fmt.Sprintf("%02x", i+1), 32),
			Size:       4096,
		}
	}
	cardsRaw, err := json.Marshal(cards)
	Expect(err).NotTo(HaveOccurred())
	cardSum := sha256.Sum256(cardsRaw)
	promptBytes, err := json.Marshal(struct {
		Consumer string                            `json:"consumer"`
		Cards    []domain.MaintenanceProjectedCard `json:"cards"`
	}{
		Consumer: "managed-maintenance-nomination",
		Cards:    cards,
	})
	Expect(err).NotTo(HaveOccurred())

	attempt := domain.ProviderAttempt{
		ID:    "00000000-0000-4000-8000-000000000099",
		JobID: "00000000-0000-4000-8000-000000000098",
	}
	inv := domain.ProviderInvocation{
		Prompt: string(promptBytes),
		Maintenance: &domain.MaintenanceInvocation{
			Mode:              domain.MaintenanceModeSemanticNomination,
			BatchID:           "nomination:p0:b0000",
			CardPayloadDigest: fmt.Sprintf("sha256:%x", cardSum[:]),
			Explanations:      exps,
		},
	}

	f, frameSHA, err := d.frame(context.Background(), attempt, nil, inv, "thread-nomination-01")
	Expect(err).NotTo(HaveOccurred())
	Expect(frameSHA).To(HaveLen(64))
	Expect(bytesSpy.reads).To(Equal(0), "Stage-1 nomination must not read backing explanation objects")
	Expect(storeSpy.writes).To(Equal(0), "Stage-1 nomination must not query store objects during framing")

	framePath := f.Name()
	rawFrame, err := io.ReadAll(f)
	Expect(err).NotTo(HaveOccurred())
	Expect(f.Close()).To(Succeed())
	Expect(os.Remove(framePath)).To(Succeed())
	Expect(len(rawFrame)).To(BeNumerically("<=", 144*1024))

	var native struct {
		ThreadID string `json:"threadId"`
		Input    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"input"`
	}
	Expect(json.Unmarshal(rawFrame, &native)).To(Succeed())
	Expect(native.ThreadID).To(Equal("thread-nomination-01"))
	Expect(native.Input).To(HaveLen(2))
	Expect(native.Input[0].Type).To(Equal("text"))
	Expect(native.Input[1].Type).To(Equal("text"))
	Expect(native.Input[0].Text).To(Equal(string(promptBytes)))

	var decodedPrompt struct {
		Consumer string                            `json:"consumer"`
		Cards    []domain.MaintenanceProjectedCard `json:"cards"`
	}
	Expect(json.Unmarshal([]byte(native.Input[0].Text), &decodedPrompt)).To(Succeed())
	Expect(decodedPrompt.Consumer).To(Equal("managed-maintenance-nomination"))
	Expect(decodedPrompt.Cards).To(Equal(cards))

	// Supplied projected card payload occurs exactly once (in Input[0].Text) and is not duplicated in Input[1].Text.
	Expect(strings.Count(string(rawFrame), "unique-card-payload-u01")).To(Equal(1))
	Expect(native.Input[1].Text).NotTo(ContainSubstring("unique-card-payload-u01"))

	instructionLines := strings.Split(native.Input[1].Text, "\n")
	Expect(len(instructionLines)).To(BeNumerically(">=", 5))
	var decodedSchema struct {
		Type                 string   `json:"type"`
		AdditionalProperties bool     `json:"additionalProperties"`
		Required             []string `json:"required"`
		Properties           struct {
			Nominations struct {
				Type     string `json:"type"`
				MinItems int    `json:"minItems"`
				MaxItems int    `json:"maxItems"`
				Items    struct {
					Type                 string   `json:"type"`
					AdditionalProperties bool     `json:"additionalProperties"`
					Required             []string `json:"required"`
					Properties           struct {
						AbstainReason struct {
							Type string `json:"type"`
						} `json:"abstain_reason"`
						Candidates struct {
							Type     string `json:"type"`
							MaxItems int    `json:"maxItems"`
						} `json:"candidates"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"nominations"`
		} `json:"properties"`
	}
	Expect(json.Unmarshal([]byte(instructionLines[3]), &decodedSchema)).To(Succeed())
	Expect(decodedSchema.Type).To(Equal("object"))
	Expect(decodedSchema.AdditionalProperties).To(BeFalse())
	Expect(decodedSchema.Required).To(Equal([]string{"nominations"}))
	Expect(decodedSchema.Properties.Nominations.Type).To(Equal("array"))
	Expect(decodedSchema.Properties.Nominations.MinItems).To(Equal(len(exps)))
	Expect(decodedSchema.Properties.Nominations.MaxItems).To(Equal(len(exps)))
	Expect(decodedSchema.Properties.Nominations.Items.AdditionalProperties).To(BeFalse())
	Expect(decodedSchema.Properties.Nominations.Items.Required).To(Equal([]string{"focal_unit_id", "abstained", "candidates"}))
	Expect(decodedSchema.Properties.Nominations.Items.Properties.AbstainReason.Type).To(Equal("string"))
	Expect(decodedSchema.Properties.Nominations.Items.Properties.Candidates.MaxItems).To(Equal(6))

	// Near-limit adaptive cards (>2048B and up to ~7.5KiB each across 18 cards) build a valid <=144KiB complete native frame without reading explanation objects.
	adaptiveCards := make([]domain.MaintenanceProjectedCard, 18)
	adaptiveExps := make([]domain.MaintenanceExplanation, 18)
	for i := 0; i < 18; i++ {
		uid := fmt.Sprintf("u%02d", i+1)
		pagePath := fmt.Sprintf("adaptive-%02d.html", i+1)
		regID := fmt.Sprintf("adaptive-reg-%02d", i+1)
		fullBody := fmt.Sprintf("adaptive-full-card-%s: %s", uid, strings.Repeat("consensus-epoch-fencing-reconciliation-barrier ", 130)) // ~6.1KiB (>2KiB, <=8KiB)
		adaptiveCards[i] = domain.MaintenanceProjectedCard{
			UnitID:          uid,
			UnitKind:        "region",
			ProjectID:       "00000000-0000-4000-8000-000000000001",
			VersionID:       "00000000-0000-4000-8000-000000000003",
			Title:           fmt.Sprintf("Adaptive Card %s", uid),
			Headings:        []string{fmt.Sprintf("Adaptive Heading %s", uid)},
			DeclaredTopics:  []string{"adaptive-nomination"},
			SourcePaths:     []string{"internal/application/scan/nomination.go"},
			BodyExcerpt:     fullBody,
			ExcerptCoverage: "full",
			TotalBodyBytes:  len(fullBody),
		}
		rawAdaptiveCard, mErr := json.Marshal(adaptiveCards[i])
		Expect(mErr).NotTo(HaveOccurred())
		Expect(len(rawAdaptiveCard)).To(BeNumerically(">", 2048))
		Expect(len(rawAdaptiveCard)).To(BeNumerically("<=", 8192))
		adaptiveExps[i] = domain.MaintenanceExplanation{
			Role:       "card",
			UnitID:     uid,
			UnitKind:   "region",
			ItemID:     fmt.Sprintf("item-%06d", i+1),
			ProjectID:  adaptiveCards[i].ProjectID,
			DocumentID: "00000000-0000-4000-8000-000000000002",
			VersionID:  adaptiveCards[i].VersionID,
			PagePath:   &pagePath,
			RegionID:   &regID,
			Title:      adaptiveCards[i].Title,
			ObjectHash: strings.Repeat(fmt.Sprintf("%02x", i+1), 32),
			Size:       8192,
		}
	}
	adaptiveCardsRaw, err := json.Marshal(adaptiveCards)
	Expect(err).NotTo(HaveOccurred())
	adaptiveSum := sha256.Sum256(adaptiveCardsRaw)
	adaptivePromptBytes, err := json.Marshal(struct {
		Consumer string                            `json:"consumer"`
		Cards    []domain.MaintenanceProjectedCard `json:"cards"`
	}{
		Consumer: "managed-maintenance-nomination",
		Cards:    adaptiveCards,
	})
	Expect(err).NotTo(HaveOccurred())
	adaptiveInv := domain.ProviderInvocation{
		Prompt: string(adaptivePromptBytes),
		Maintenance: &domain.MaintenanceInvocation{
			Mode:              domain.MaintenanceModeSemanticNomination,
			BatchID:           "nomination:p0:b0000",
			CardPayloadDigest: fmt.Sprintf("sha256:%x", adaptiveSum[:]),
			Explanations:      adaptiveExps,
		},
	}
	fAdaptive, shaAdaptive, errAdaptive := d.frame(context.Background(), attempt, nil, adaptiveInv, "thread-nomination-adaptive")
	Expect(errAdaptive).NotTo(HaveOccurred())
	Expect(shaAdaptive).To(HaveLen(64))
	Expect(bytesSpy.reads).To(Equal(0))
	Expect(storeSpy.writes).To(Equal(0))
	adaptiveFramePath := fAdaptive.Name()
	rawAdaptiveFrame, err := io.ReadAll(fAdaptive)
	Expect(err).NotTo(HaveOccurred())
	Expect(fAdaptive.Close()).To(Succeed())
	Expect(os.Remove(adaptiveFramePath)).To(Succeed())
	Expect(len(rawAdaptiveFrame)).To(BeNumerically(">", 100*1024))
	Expect(len(rawAdaptiveFrame)).To(BeNumerically("<=", 144*1024))
	Expect(strings.Count(string(rawAdaptiveFrame), "adaptive-full-card-u01")).To(Equal(1))

	// Oversized actual frame (>144 KiB after JSON quoting) is refused with ErrInvalidTarget and cleans up its temp file.
	oversizedInv := inv
	oversizedInv.Prompt = `{"consumer":"managed-maintenance-nomination","cards":"` + strings.Repeat(`\"`, 60000) + `"}`
	fOver, shaOver, errOver := d.frame(context.Background(), attempt, nil, oversizedInv, "thread-nomination-over")
	Expect(errors.Is(errOver, domain.ErrInvalidTarget)).To(BeTrue())
	Expect(fOver).To(BeNil())
	Expect(shaOver).To(BeEmpty())
	Expect(bytesSpy.reads).To(Equal(0))
	Expect(storeSpy.writes).To(Equal(0))
	entries, dirErr := os.ReadDir(scratchDir)
	Expect(dirErr).NotTo(HaveOccurred())
	Expect(entries).To(BeEmpty(), "failed oversized frame must remove its temporary provider-input file")
})
