"""Actual native ReaderMode regression: reused pinned beta28 harness, typed ReaderService, fixed static origin, real tagged production daemon/auth/import/storage."""
import argparse,importlib.util,json,os,pathlib,re,shutil,subprocess,tempfile,time,urllib.parse,urllib.error,urllib.request,hashlib,signal,sys
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--scratch-root',type=pathlib.Path,help='Explicit existing private physical parent for this driver')
parser.add_argument('--receipt',type=pathlib.Path,required=True)
parser.add_argument('--provider-binary',type=pathlib.Path,help='Explicit existing pinned provider binary for providers-only zero-inference stage')
parser.add_argument('--stages',default='selfcheck,app,seed,restart,crash,evidence,search,references')
parser.add_argument('--existing-records',type=pathlib.Path,help='Explicit saved-record campaign manifest; requires evidence-only')
parser.add_argument('--saved-record-subset',action='store_true',help='Observe available saved cases; retain missing campaign obligations explicitly')
parser.add_argument('--saved-browser-cli',type=pathlib.Path,help=argparse.SUPPRESS)
args=parser.parse_args()
assert not args.saved_browser_cli or args.existing_records,'saved browser requires explicit manifest'
assert not args.saved_record_subset or args.existing_records,'subset requires explicit saved manifest'
assert not args.existing_records or args.stages=='evidence','saved campaign selects evidence only'
STAGES=set(args.stages.split(','))
ALL_STAGES={'selfcheck','app','seed','restart','crash','evidence','search','references'}
assert sys.platform=='darwin','this check requires the existing native macOS Wails harness'
assert STAGES and STAGES<=ALL_STAGES|{'corpus','reference-corpus','versions','owner-records','sources','providers'},STAGES
assert 'providers' not in STAGES or STAGES=={'providers'},'providers is a standalone zero-inference status campaign'
assert 'sources' not in STAGES or STAGES=={'sources'},'sources is an explicit standalone settings campaign'
assert 'owner-records' not in STAGES or STAGES=={'owner-records'},'owner-records is an explicit standalone local campaign'
assert not ('restart' in STAGES and 'seed' not in STAGES),'restart requires the seed stage'
assert not (STAGES&{'seed','restart','crash','search','corpus','reference-corpus'} and 'app' not in STAGES),'these stages require the app smoke context'
# Versions/evidence/references launch the same compiled App themselves and
# consume only the common daemon/session setup. The unrelated app smoke opens
# a CDN-dependent page; T5's no-network selection must not open that page.
assert not (STAGES&{'evidence','references'} and not STAGES&{'app','versions'} and not args.existing_records),'these stages require app or versions setup'
REQUESTED_STAGES=set(STAGES)
OFFLINE_CORPUS=os.environ.get('HAERYONG_NATIVE_CORPUS_OFFLINE')=='1'
if OFFLINE_CORPUS:
    assert STAGES in ({'app','corpus'},{'app','reference-corpus'}),'offline corpus selects exactly one local corpus with common setup'
    # T6 decision T1: the corpus launches the same compiled App itself. Exclude
    # only the unrelated CDN smoke; never count that excluded stage as passed.
    STAGES=STAGES-{'app'}
REPO=pathlib.Path(__file__).resolve().parents[2]
RECEIPT=args.receipt.resolve()
assert RECEIPT.parent.is_dir(),'receipt parent must exist'
volume=pathlib.Path('/Volumes/RootKernel')
scratch=args.scratch_root.resolve(strict=True) if args.scratch_root else pathlib.Path(tempfile.gettempdir()).resolve()
os.umask(0o077)
work=pathlib.Path(tempfile.mkdtemp(prefix='h3n-',dir=scratch)).resolve()
print(json.dumps({'runtime':str(work)}),flush=True)
env=os.environ|{'TMPDIR':str(work),'HAERYONG_TEST_STATE_DIR':str(work/'app-state'),'PYTHONDONTWRITEBYTECODE':'1'}
os.environ['HAERYONG_TEST_STATE_DIR']=env['HAERYONG_TEST_STATE_DIR']
spec=importlib.util.spec_from_file_location('h',REPO/'tests/e2e/harness.py');h=importlib.util.module_from_spec(spec);spec.loader.exec_module(h)
proof={'scope':__doc__,'runtime':str(work),'complete':False,'selected_stages':sorted(STAGES),'full_qualification':STAGES==ALL_STAGES,'driver_sha256':hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),'cases':[]}
proof.update(requested_stages=sorted(REQUESTED_STAGES),executed_stages=sorted(STAGES),excluded_stages=([{'stage':'app','status':'not_run','reason':'T6 offline corpus excludes unrelated CDN smoke'}] if OFFLINE_CORPUS else []))
state=work/'state';state.mkdir(mode=0o700)
def fingerprints():
    paths=sorted([p for folder in ['cmd','internal'] for p in (REPO/folder).rglob('*.go') if not p.name.endswith('_test.go')]+[REPO/'go.mod',REPO/'go.sum']+[p for p in (REPO/'internal/webassets/dist').rglob('*') if p.is_file()]
                 +[p for p in (REPO/'desktop').rglob('*') if p.is_file()]
                 +[REPO/'spikes/viewer/desktop/main.go',REPO/'spikes/viewer/desktop/reader_mode.go',REPO/'spikes/viewer/desktop/go.mod',REPO/'spikes/viewer/desktop/go.sum']
                 +[REPO/'frontend/native.html',REPO/'frontend/vite.native.config.ts',REPO/'frontend/src/native-main.tsx',REPO/'frontend/src/client/native-bridge.ts',REPO/'frontend/src/client/native-wire.ts',REPO/'frontend/src/client/native.ts',REPO/'spikes/viewer/check_reader_native.py',REPO/'spikes/viewer/reference_native.py',REPO/'spikes/viewer/reference_corpus_native.py',REPO/'spikes/viewer/reference_corpus_probe.js',REPO/'tests/manual/search_corpus_support.py',REPO/'spikes/viewer/reference_probe.js',REPO/'tests/manual/reference_support.py'])
    if args.existing_records:
        paths += [REPO/'internal/adapters/http/auth/browser/check_reader_evidence.py', *sorted((REPO/'frontend/src').rglob('*.ts')), *sorted((REPO/'frontend/src').rglob('*.tsx'))]
    if 'providers' in STAGES:
        paths += [REPO/'spikes/viewer/provider_probe.js',REPO/'frontend/src/app/screens/provider.tsx']
    if 'sources' in STAGES:
        paths += [REPO/name for name in ('frontend/src/app/screens/sources.tsx','frontend/src/app/screens/source-configuration.ts','frontend/src/client/types.ts')]
    result={str(p.relative_to(REPO)):hashlib.sha256(p.read_bytes()).hexdigest() for p in paths}
    # Explicit new compiler/helper/fixture closure; historical lists are not
    # automatic dependency discovery. This helper performs no product setup.
    spec=importlib.util.spec_from_file_location('owner_input_support',REPO/'tests/manual/owner_records_support.py')
    support=importlib.util.module_from_spec(spec);spec.loader.exec_module(support)
    result.update(support.fingerprints())
    return result
def receipts(pid):
    path=state/f'native-reader-receipts-{pid}.json'
    return json.loads(path.read_text()) if path.exists() else None
def final_report(pid,timeout):
    """Wait for the page's FINAL report (explicit done marker), never a
    partial interval snapshot."""
    path=state/f'native-reader-selfcheck-{pid}.json'
    deadline=time.monotonic()+timeout
    while time.monotonic()<deadline:
        if path.exists():
            try:
                rows=json.loads(path.read_text())['rows']
                done=next((r for r in rows if r['name']=='page.done'),None)
                if done and done['status']=='pass':
                    return rows
                failures=[r for r in rows if r['status']=='fail' and done]
                if done and failures:
                    return rows
            except (ValueError,KeyError):
                pass
        time.sleep(0.5)
    return None
def wait_for(predicate,timeout,step=0.2):
    deadline=time.monotonic()+timeout
    while time.monotonic()<deadline:
        value=predicate()
        if value is not None:return value
        time.sleep(step)
    return None
product=None;runs=[]
def launch(args,close_after,base=None):
    log=work/f'native-{len(runs)}.log'
    handle=log.open('w')
    settings=json.loads(globals()['args'].existing_records.read_text()) if globals()['args'].existing_records else None
    binary=pathlib.Path(settings['native_binary']).resolve(strict=True) if settings else work/'native'
    dist=pathlib.Path(settings['native_dist']).resolve(strict=True) if settings else work/'dist-native'
    process=subprocess.Popen([str(binary),*args,'--base',base or product.endpoint,'--state',str(state),'--reader-dist',str(dist),'--close-after',close_after],stdout=handle,stderr=handle,env=env)
    runs.append((process,handle))
    return process
before=fingerprints()
def assert_inputs_unchanged(stage):
    current=fingerprints()
    changes={path:{'before':before.get(path),'after':current.get(path)}
             for path in sorted(before.keys()|current.keys()) if before.get(path)!=current.get(path)}
    if changes:proof.setdefault('input_changes',{})[stage]=changes
    assert not changes,f'inputs changed during {stage}; no stable-candidate proof'

def run_saved_records():
    """Existing-profile reads only; limit=1 is a labelled transport projection."""
    import threading,socket
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
    cfg = json.loads(args.existing_records.read_text())
    semantic_details = cfg.get('semantic_details') is True
    if semantic_details:
        # Persist actual prepared bytes before semantic/preflight assertions,
        # including failures. The owning controller binds build provenance.
        proof['prepared_inputs']={path:hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()
                                  for path in cfg.get('prepared_inputs',{})}
        assert proof['prepared_inputs'] and proof['prepared_inputs']==cfg['prepared_inputs'],'prepared RG3 input binding required'
    endpoint = cfg['endpoint'].rstrip('/')
    parsed = urllib.parse.urlsplit(endpoint)
    assert parsed.scheme == 'http' and parsed.hostname == '127.0.0.1' and parsed.port and not parsed.path and not parsed.query and not parsed.fragment and parsed.username is None
    credential = pathlib.Path(cfg['owner_token_file']).resolve(strict=True)
    assert credential.stat().st_mode & 0o777 == 0o600, 'credential must be mode 0600'
    token = credential.read_text().strip()
    assert token and '\n' not in token and '\r' not in token, 'invalid credential'
    cases = cfg['cases']
    assert cases, 'saved cases required'
    proof.update(scope='Saved production-record UI reads; controlled limit=1/fault/response ordering; no producer execution',
                 full_qualification=False, manifest_sha256=hashlib.sha256(args.existing_records.read_bytes()).hexdigest(),
                 gaps=[], requests=[], pagination={}, supplied_daemon_owned=False)
    def check(condition, name):
        if not condition:proof['failure']=name
        assert condition, name
    def require(condition, name):
        if not condition and args.saved_record_subset:proof['gaps'].append(name)
        else:check(condition,name)
    canonical=lambda rows:sorted(json.dumps(r,sort_keys=True) for r in rows)
    def query(path, values):
        return path + ('?' + urllib.parse.urlencode({k:v for k,v in values.items() if v is not None}) if values else '')
    def get(path, envelope=True):
        request = urllib.request.Request(endpoint+path, headers={'Authorization':'Bearer '+token, 'Origin':endpoint})
        with urllib.request.urlopen(request, timeout=30) as response:
            value=json.load(response)
            return value['data'] if envelope else value
    def enumerate_rows(path):
        rows=[]; cursor=None; seen=set()
        while True:
            url=urllib.parse.urlsplit(path); q=dict(urllib.parse.parse_qsl(url.query));
            if '/assessments' in url.path:q['limit']='1'
            if cursor is not None:q['cursor']=cursor
            page=get(query(url.path,q)); rows.extend(page['items']); cursor=page['next_cursor']
            if cursor is None:return rows
            check(cursor not in seen,'repeated genuine cursor'); seen.add(cursor)
    def scope_query(c):
        s=c['scope']
        return {} if s['kind']=='version' else {'scope_kind':s['kind'],'page_path':s['page_path'],'region_id':s['region_id']}
    def history_path(c, target=None):
        return query('/api/v1/versions/'+c['version_id']+'/assessments',scope_query(c)|({'target_set_id':target} if target else {}))
    def snapshot(c):
        v='/api/v1/versions/'+c['version_id']
        d=get('/api/v1/documents/'+c['document_id'])
        return {'document':{k:d.get(k) for k in ('lifecycle','revision','latest_version_id','review_state','reviewed_at','updated_since_review')},
                'review':get(v+'/review'),'review_history':enumerate_rows(v+'/review/history'),'version':get(v)}
    projections=[c for c in cases if c.get('query_target_set_id') is not None]
    check(not projections or cfg.get('read_only_target_projection') is True,'explicit read-only target projection opt-in required')
    check(len({(c['version_id'],json.dumps(c['scope'],sort_keys=True)) for c in cases})==len(cases) or not projections,
          'projected selections must be unique; use separate manifests for old/new of the same scope')
    for c in projections:
        check(c['query_target_set_id']==c['expected']['applicable']['target_set_id'],'projection must select the genuine expected target')
    proof['query_projection']={'enabled':bool(projections),'scope':'read-only explicit-target public GET query projection; unchanged upstream payloads; no product target picker/default retarget',
                               'selections':[{'version_id':c['version_id'],'scope':c['scope'],'target_set_id':c['query_target_set_id']} for c in projections]}
    snapshots=[]; defaults=[]; outcomes=set(); kinds=set(); regions=[]; separated=False
    for c in cases:
        s=c['scope']; kinds.add(s['kind'])
        if s['kind']=='region':regions.append((c['version_id'],s['page_path'],s['region_id']))
        defaults.append(get(history_path(c)))
        selected_path=history_path(c,c.get('query_target_set_id'))
        first=get(selected_path); a=first['applicable']; expected=c['expected']
        check(a == expected['applicable'],'saved applicable differs from independent expected record')
        check(a is not None and a['scope']==s,'saved scope mismatch')
        separated |= a['captured_at'] is not None and a['assessed_at'] is not None and a['captured_at'] != a['assessed_at']
        history=enumerate_rows(selected_path)
        check(sorted(r['assessment_id'] for r in history)==sorted(expected['history_ids']),'saved history IDs')
        if semantic_details:
            check(canonical(history)==canonical(expected['history']),'saved complete semantic history differs from retained oracle')
            check(get('/api/v1/versions/'+c['version_id']+'/metadata?kind=supplied',False)==expected['authored_metadata'],'saved authored metadata oracle')
        check(len({x[:8] for x in expected['history_ids']})==len(expected['history_ids']),'history display prefix collision')
        units_url=a['coverage']['units_url']
        u=urllib.parse.urlsplit(units_url or '')
        if units_url is None:
            check(a['coverage']['coverage_set_id'] is None and a['coverage']['eligible']==0 and expected['units']==[],'honest no-target absence')
            units=[]
        else:
            check(not u.netloc and u.path=='/api/v1/versions/'+c['version_id']+'/assessments/coverage','fixed local units_url required')
            uq=urllib.parse.parse_qs(u.query)
            check(set(uq)<= {'coverage_set_id','attempt_run_id'} and uq.get('coverage_set_id')==[a['coverage']['coverage_set_id']],'coverage locator')
            units=enumerate_rows(units_url)
        check(canonical(units)==canonical(expected['units']),'saved fixed units differ from oracle')
        check(len({(r['kind'],r['page_path'],r['region_id']) for r in units})==len(units),'duplicate fixed tuple')
        check(a['coverage']['eligible']==len(units),'producer fixed denominator')
        outcomes.update(r['outcome'] for r in units)
        c['history_path']=history_path(c); c['selected_history_path']=selected_path; c['units_url']=units_url
        c['route']='/documents/'+c['document_id']+'/versions/'+c['version_id']
        reader_page=c.get('reader_page_path',s['page_path'])
        check('reader_page_path' not in c or s['kind']=='version','separate Reader page selects only a whole-version Evidence case')
        if reader_page is not None:c['route']+='?'+urllib.parse.urlencode({'page':reader_page})
        listed=enumerate_rows(query('/api/v1/documents',{'project_id':c['project_id'],**({'lifecycle':c['library_lifecycle']} if c.get('library_lifecycle','all')!='all' else {})}))
        document=next((d for d in listed if d['document_id']==c['document_id']),None)
        check(document is not None,'saved Library document absent')
        check(all(document.get(k)==v for k,v in c['library_expected'].items()),'independent Library expected record')
        check({'freshness','review_state','latest_version_id','lifecycle','updated_since_review'}<=set(c['library_expected']),'Library oracle fields required')
        snapshots.append(snapshot(c))
    require({'partial','blocked','not_checked'}<=outcomes,'saved partial/blocked/not_checked cases required')
    require({'version','page','region'}<=kinds and separated,'saved scopes/distinct timestamps required')
    require(any(v==v2 and p!=p2 and r==r2 for v,p,r in regions for v2,p2,r2 in regions),'same-name cross-page region cases required')
    late=cfg.get('late'); lc=cases[late['case_index']] if late else None
    require(late is not None,'same-scope old/new target records required')
    if late:
        check(lc['scope']['kind']!='region','late case must use stable page/version route')
        old=get(history_path(lc,late['older_target_set_id']))
        check(old['applicable'] == late['older_applicable'] and old['applicable'] is not None and old['applicable']['scope']==lc['scope'] and
              old['applicable']['target_set_id']==late['older_target_set_id'] and
              late['older_target_set_id']!=lc['expected']['applicable']['target_set_id'],'genuine older target required')
        if semantic_details:
            check(canonical(enumerate_rows(history_path(lc,late['older_target_set_id'])))==canonical(late['older_history']),'genuine complete older semantic history')
    lock=threading.Lock(); release=threading.Event(); delivered=threading.Event(); started=threading.Event()
    control={'arm':False,'old':None,'faults':{},'errors':[]}; streams=set(); stopping=threading.Event()
    class Proxy(BaseHTTPRequestHandler):
        def log_message(self,*unused):pass
        def send(self,status,body,headers=()):
            self.send_response(status)
            for k,v in headers:
                if k.lower() not in ('content-length','transfer-encoding','connection'):self.send_header(k,v)
            self.send_header('Content-Length',str(len(body)));self.end_headers()
            if self.command!='HEAD':self.wfile.write(body);self.wfile.flush()
        def forward(self):
            u=urllib.parse.urlsplit(self.path); q=dict(urllib.parse.parse_qsl(u.query))
            assessment=u.path.endswith('/assessments'); coverage=u.path.endswith('/assessments/coverage')
            if self.command not in ('GET','HEAD') and not (
                self.command=='POST' and '/render-sessions' in u.path or
                self.command=='PUT' and u.path.endswith(('/reading-position','/page-position'))):
                control['errors'].append('unexpected mutation route');self.send(403,b'');return
            if assessment or coverage:q['limit']='1'
            incoming_target=q.get('target_set_id')
            selected=next((c for c in cases if assessment and u.path==urllib.parse.urlsplit(c['history_path']).path and
                           (q.get('scope_kind','version'),q.get('page_path'),q.get('region_id'))==
                           (c['scope']['kind'],c['scope']['page_path'],c['scope']['region_id'])),None)
            race=lc is not None and selected is lc
            if race and incoming_target==lc['expected']['applicable']['target_set_id'] and started.is_set():
                release.set();check(delivered.wait(20),'held old response did not drain')
            with lock:
                held=race and incoming_target is None and not q.get('cursor') and control['arm']
                if held:control['arm']=False
            if held:
                started.set();check(release.wait(45),'late barrier timed out')
                try:self.send(200,control['old'],[('Content-Type','application/json')])
                finally:delivered.set()
                return
            # Keep explicit probe reads (including the old/new late barriers)
            # exact. Only the selected UI's target-absent GET is projected.
            if self.command=='GET' and selected is not None and incoming_target is None and selected.get('query_target_set_id') is not None:
                q['target_set_id']=selected['query_target_set_id']
            path=query(u.path,q)
            body=self.rfile.read(int(self.headers.get('Content-Length') or 0)) or None
            headers={k:v for k,v in self.headers.items() if k.lower() not in ('host','origin','authorization','content-length','connection','accept-encoding')}
            headers.update(Origin=endpoint,Authorization='Bearer '+token,Connection='close')
            # The existing local liveness route rejects a supplied Origin;
            # product routes still receive their exact guarded service origin.
            if u.path=='/health/live':headers.pop('Origin',None)
            request=urllib.request.Request(endpoint+path,data=body,headers=headers,method=self.command)
            try:response=urllib.request.urlopen(request,timeout=30)
            except urllib.error.HTTPError as error:response=error
            if u.path=='/api/v1/events' and response.status==200:
                with lock:streams.add(response)
                try:
                    self.send_response(200)
                    self.send_header('Content-Type','text/event-stream');self.send_header('Connection','close');self.end_headers()
                    while True:
                        try:chunk=response.read1(4096)
                        except (AttributeError,ValueError,OSError) as error:
                            if not stopping.is_set() or response.fp is not None:raise
                            proof.setdefault('closed_event_stream_reads',[]).append(type(error).__name__)
                            break
                        if not chunk:break
                        self.wfile.write(chunk);self.wfile.flush()
                finally:
                    response.close()
                    with lock:streams.discard(response)
                return
            with response:
                data=response.read();status=response.status; fields=response.getheaders()
            if semantic_details and self.command=='POST':
                render=re.fullmatch(r'/api/v1/versions/([^/]+)/render-sessions(?:/([^/]+)/(close|renew))?',u.path)
                if render:
                    event={'version_id':render[1],'session_id':render[2],'operation':render[3] or 'open','path':u.path,'status':status}
                    if event['operation']=='open' and status in (200,201):
                        opened=json.loads(data)['data'];event['session_id']=opened['session_id']
                        check(opened['version_id']==render[1],'render HTTP version identity')
                    with lock:proof.setdefault('render_http_events',[]).append(event)
            if assessment or coverage:
                with lock:
                    proof['requests'].append({'path':u.path,'query':q,'status':status})
                    kind='coverage' if coverage else 'history'
                    fault=control['faults'].get(kind)
                    selected_units=coverage and any(c['expected']['applicable']['coverage']['coverage_set_id']==q.get('coverage_set_id') for c in cases)
                    if q.get('cursor') and status==200 and fault is None and (selected is not None or selected_units):
                        control['faults'][kind]={'path':path,'attempts':1}
                        self.send(500,b'{"error":{"code":"SAVED_CURSOR_FAULT","retryable":true}}',[('Content-Type','application/json')]);return
                    if fault and fault['path']==path:fault['attempts']+=1
                    if race and incoming_target==late['older_target_set_id'] and not q.get('cursor') and status==200:
                        control['old']=data;control['arm']=True
                        proof['older_response_sha256']=hashlib.sha256(data).hexdigest()
            self.send(status,data,fields)
        def handle_request(self):
            try:self.forward()
            except (BrokenPipeError,ConnectionResetError):pass
            except Exception as error:
                # Record only the failing driver stage/class, never response
                # bodies, credentials or imported/source content.
                import traceback
                control['errors'].append({'class':type(error).__name__,'path':urllib.parse.urlsplit(self.path).path,
                                          'frames':[{'function':f.name,'line':f.lineno} for f in traceback.extract_tb(error.__traceback__)]})
                try:self.send(502,b'')
                except (BrokenPipeError,ConnectionResetError):pass
        do_GET=do_HEAD=do_POST=do_PUT=do_PATCH=do_DELETE=handle_request
    class SavedRecordServer(ThreadingHTTPServer):
        # Match the existing native reference harness's Darwin listen policy.
        request_queue_size = socket.SOMAXCONN
    server=SavedRecordServer(('127.0.0.1',0),Proxy);server.daemon_threads=True
    origin='http://127.0.0.1:'+str(server.server_port)
    thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
    probe_config={'cases':cases,'late':late,'semantic_details':semantic_details}
    script=r"""
(async()=>{
const C=CONFIG,rows=[];let last='startup',pageError=false;
const check=(ok,name)=>{last=name;if(!ok)throw Error(name);};
const log=name=>rows.push({name,status:'pass'});
window.addEventListener('error',()=>{pageError=true});window.addEventListener('unhandledrejection',()=>{pageError=true});
const wait=async f=>{const end=Date.now()+30000;while(Date.now()<end){if(await f())return;await new Promise(r=>setTimeout(r,100));}throw Error(last);};
const settle=()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));
const native=typeof window.haeryongNativeCall==='function';
const get=async(path,operation,payload)=>{
 if(native){const r=await window.haeryongNativeCall(701100,[{Operation:operation,Payload:payload}]);check(r.ok,'saved.native-read');return r.value;}
 const r=await fetch(path,{headers:{Authorization:'Bearer saved-record-probe'}});check(r.ok,'saved.http-read');return (await r.json()).data;
};
const navigate=async path=>{history.pushState({haeryongReaderContext:crypto.randomUUID()},'',path);dispatchEvent(new PopStateEvent('popstate'));await settle();};
const button=(name,root=document)=>[...root.querySelectorAll('button')].find(b=>b.textContent===name);
const block=()=>document.querySelector('.assessment-block');
const scope=s=>s.kind==='version'?'버전 전체':s.kind==='page'?'페이지 '+s.page_path:'영역 '+s.region_id+'('+s.page_path+')';
const labels={assessed:'평가됨',partial:'부분',blocked:'차단',not_checked:'미확인'};
const status={aligned_within_scope:'범위 내 정렬',outdated_against_target:'대비 구식',reassessment_needed:'재평가 필요',unable_to_verify:'확인 불가',not_assessed:'미평가'};
const stable=v=>v!==null&&typeof v==='object'?Array.isArray(v)?v.map(stable):Object.fromEntries(Object.keys(v).sort().map(k=>[k,stable(v[k])])):v;
const same=(a,b)=>JSON.stringify(stable(a))===JSON.stringify(stable(b));
function semanticField(dl,name,value,unit=false){
 const dd=dl.querySelector(':scope > div > dd[data-field="'+name+'"]');
 check(!!dd,'saved.semantic-field');
 if(unit&&value===''){
  check(dd.dataset.empty==='true'&&dd.textContent==='빈 기록(없음)'&&dd.childElementCount===0,'saved.semantic-empty');return;
 }
 const span=dd.querySelector(':scope > span.semantic-detail-value');
 const kind=value===null?'null':value===undefined?'missing':typeof value;
 check(!!span&&dd.children.length===1&&span.childElementCount===0&&span.dataset.valueKind===kind,'saved.semantic-safe-text-kind');
 if(value!==null&&typeof value==='object'){
  let parsed;try{parsed=JSON.parse(span.textContent);}catch{check(false,'saved.semantic-json');}
  check(same(parsed,value),'saved.semantic-json-fields');
 }else check(span.textContent===(value===null?'없음(null)':value===undefined?'기록 없음':String(value)),'saved.semantic-full-text');
}
async function semanticDetails(c,partial=false){
 if(!C.semantic_details)return;
 const history=block()?.querySelector('.semantic-assessment-history'),expected=c.expected.history;
 check(!!history,'saved.semantic-container');
 const nodes=[...history.querySelectorAll(':scope > details.semantic-assessment')];
 check(!partial||nodes.length>0&&nodes.length<expected.length,'saved.semantic-partial-history');
 const records=partial?expected.slice(0,nodes.length):expected;
 check(same(nodes.map(n=>n.dataset.assessmentId).sort(),records.map(r=>r.assessment_id).sort()),'saved.semantic-exact-history-set');
 const panel=block().closest('.source-panel');
 await wait(()=>panel.querySelectorAll('.version-evidence li.evidence-row').length===c.expected.authored_metadata.evidence.length);
 const authored=panel.querySelector('.version-evidence');
 const authoredRows=authored?[...authored.querySelectorAll('li.evidence-row')]:[];
 check(authoredRows.length===c.expected.authored_metadata.evidence.length&&!history.querySelector('.evidence-row')&&!authored?.querySelector('.semantic-assessment'),'saved.semantic-authored-separation');
 check(history.parentElement.textContent.includes('원본 작성 근거와 별도로 저장된 의미 평가 이력'),'saved.semantic-distinct-label');
 for(const e of c.expected.authored_metadata.evidence)check(authoredRows.some(n=>n.querySelector(':scope > button')?.textContent.startsWith(e.id+' · ')),'saved.semantic-authored-identity');
 for(const record of records){
  const node=nodes.find(n=>n.dataset.assessmentId===record.assessment_id);
  check(node.dataset.versionId===record.version_id&&node.dataset.targetSetId===(record.target_set_id??'')&&
        node.dataset.contributionCoverageSetId===(record.coverage_set_id??'')&&
        node.dataset.membershipCoverageSetId===(c.expected.applicable.coverage.coverage_set_id??''),'saved.semantic-provenance-binding');
  if(!node.open)node.querySelector(':scope > summary').click();await settle();
  check(node.open&&!node.querySelector('.semantic-details-unavailable'),'saved.semantic-disclosure');
  const provenance=node.querySelector(':scope > dl.semantic-assessment-provenance');
  check(!!provenance,'saved.semantic-provenance');
  for(const f of ['assessment_id','version_id','coverage_set_id','target_set_id','scope','state','origin','config_revision','permissions_revision','assessed_at','created_at'])semanticField(provenance,f,record[f]);
  const units=[...node.querySelectorAll(':scope > .semantic-unit-details > article.semantic-unit')];
  check(units.length===record.evidence.units.length,'saved.semantic-unit-count');
  for(const unit of record.evidence.units){
   const matches=units.filter(n=>n.dataset.kind===unit.kind&&n.dataset.pagePath===(unit.page_path??'')&&n.dataset.regionId===(unit.region_id??''));
   check(matches.length===1,'saved.semantic-exact-tuple');const n=matches[0],dl=n.querySelector(':scope > dl');
   for(const f of ['kind','page_path','region_id','outcome','discrepancy','reason','rationale'])semanticField(dl,f,unit[f],true);
   const citations=[...n.querySelectorAll(':scope > ol.semantic-citations > li.semantic-citation')];
   check(citations.length===unit.citations.length,'saved.semantic-citation-count');
   unit.citations.forEach((citation,index)=>{
    const entry=citations[index];check(entry.dataset.citationIndex===String(index),'saved.semantic-citation-order');
    for(const f of ['basis','read_id','verification_id','input_origin','target_set_id','bytes_sha256','source','location','package_evidence_ref'])semanticField(entry.querySelector(':scope > dl'),f,citation[f]);
   });
  }
 }
 log(partial?'saved.semantic-retained-before-history-retry':'saved.semantic-details.'+C.cases.indexOf(c));
}
async function selectEvidence(c){
 last='saved.evidence-ready';
 await wait(()=>{const b=button('근거 패널');if(!b)return false;if(b.getAttribute('aria-expanded')!=='true')b.click();return !!block();});
 if(c.scope.kind==='version'){
  const b=button('버전 전체 평가');check(!!b,'saved.whole-version-selection');if(b.getAttribute('aria-pressed')!=='true')b.click();await settle();
 }
 if(c.scope.kind==='region'){
  await wait(()=>[...document.querySelectorAll('.region-outline button')].some(b=>b.textContent===c.region_label));
  const b=[...document.querySelectorAll('.region-outline button')].find(b=>b.textContent===c.region_label);if(b.getAttribute('aria-current')!=='true')b.click();
 }
}
async function evidence(c){
 await selectEvidence(c);
 const e=c.expected,a=e.applicable,u=e.units,counts={assessed:0,partial:0,blocked:0,not_checked:0};
 u.forEach(r=>counts[r.outcome]++);
 if(a.coverage.units_url===null){
  await wait(()=>block()?.textContent.includes('범위 '+scope(c.scope))&&block()?.textContent.includes('커버리지 단위 집합이 없습니다'));
  check(!block().textContent.includes('단위 파생:'),'saved.no-invented-no-target-units');
  check(block().textContent.includes('대상 집합 없음')&&block().textContent.includes('캡처 없음')&&block().textContent.includes('기록된 배지: '+status[a.status]),'saved.no-target-status');
  log('saved.no-target.'+C.cases.indexOf(c));return;
 }
 last='saved.selected-scope-ready';
 await wait(async()=>{
  const b=block();if(!b)return false;
  const h=button('실패한 지점부터 다시 불러오기',b),v=button('커버리지 다시 불러오기',b);
  if(h||v){
   check(b.querySelectorAll('li').length>0,'saved.cursor-retained-rows');
   check(!b.textContent.includes('단위 파생:'),'saved.no-premature-derived-counts');
   if(C.semantic_details&&v){
    const retained=v.parentElement.previousElementSibling;
    check(retained?.tagName==='UL'&&retained.children.length>0&&retained.children.length<u.length,'saved.coverage-partial-fixed-units');
    for(const li of retained.children)check(u.some(r=>li.textContent.includes(scope(r)+' · '+labels[r.outcome])),'saved.coverage-retained-tuple');
   }
   await semanticDetails(c,!!h);
   log(h?'saved.history-failure-retains-rows':'saved.coverage-failure-retains-rows');(h||v).click();return false;
  }
  return b.textContent.includes('범위 '+scope(c.scope))&&b.textContent.includes('단위 파생:');
 });
 let b=block(),text=b.textContent;
 check(text.includes('대상 집합 '+(a.target_set_id??'없음'))&&text.includes('캡처 '+(a.captured_at??'없음'))&&text.includes('평가 시각 '+(a.assessed_at??'없음')),'saved.target-and-times');
 check(text.includes('범위 '+scope(c.scope)),'saved.exact-scope');
 check(text.includes('평가됨 '+counts.assessed+'/'+u.length)&&text.includes('부분 '+counts.partial)&&text.includes('차단 '+counts.blocked)&&text.includes('미확인 '+counts.not_checked),'saved.fixed-denominator');
 check(text.includes('기록된 배지: '+status[a.status])&&text.includes('파생 상태 '+status[e.derived_status]),'saved.status');
 for(const id of e.history_ids)check(text.includes(id.slice(0,8)+'…'),'saved.history-identity');
 const toggle=button('단위/사유 펼치기/접기',b);check(!!toggle,'saved.units-disclosure');
 if(toggle.getAttribute('aria-expanded')!=='true')toggle.click();
 await settle();const ul=toggle.nextElementSibling;
 check(ul?.tagName==='UL'&&ul.children.length===u.length,'saved.all-required-units-visible');
 for(const r of u){
  const matches=[...ul.children].filter(li=>li.textContent.includes(scope(r)+' · '+labels[r.outcome]));
  check(matches.length===1,'saved.unit-tuple');
  const t=matches[0].textContent;
  check((r.reason==null||t.includes('사유 '+r.reason))&&t.includes(r.assessment_id==null?'기여 없음':'기여 '+r.assessment_id.slice(0,8)+'…'),'saved.unit-reason-link');
  if(r.target_set_id!=null)check(t.includes('대상 '+r.target_set_id.slice(0,8)+'…'),'saved.unit-target');
  check(t.includes('불일치 있음')===(r.discrepancy===true||(typeof r.discrepancy==='string'&&r.discrepancy!=='')),'saved.unit-discrepancy');
 }
 await semanticDetails(c);
 log('saved.evidence.'+C.cases.indexOf(c));
}
try{
 await wait(()=>document.querySelector('.service-state[data-state="verified"]'));
 if(native)check(window.__haeryongNativeTransport==='HAEYONG_NATIVE_TRANSPORT_BOUND','saved.native-transport');
 for(const c of C.cases){
  await navigate(c.route);await evidence(c);
  if(c.scope.kind!=='version'||c.reader_page_path!=null){
   last='saved.actual-render-frame';
   // Form POST targets the existing about:blank iframe; its src attribute
   // does not describe the loaded child. The trusted App removes its status
   // only after the guarded viewer channel becomes ready.
   await wait(()=>{
    const viewer=document.querySelector('section.reader-content');
    return !!viewer?.querySelector('iframe.reader-frame')
      && !viewer.querySelector('[role="status"]')
      && !viewer.querySelector('[role="alert"]');
   });
   log('saved.actual-render-frame');
  }
  await navigate('/library?'+new URLSearchParams({project_id:c.project_id,lifecycle:c.library_lifecycle??'all'}));
  last='saved.library-row';
  await wait(()=>{
   const row=document.querySelector('[data-testid="library-document"][data-document="'+c.document_id+'"]');
   if(row)return true;const more=button('더 불러오기');if(more&&!more.disabled)more.click();return false;
  });
  const row=document.querySelector('[data-testid="library-document"][data-document="'+c.document_id+'"]'),e=c.library_expected,f=e.freshness;
  const fl={not_assessed:'미평가',reassessment_needed:'재평가 필요',aligned_within_scope:'확인 범위 내 일치',outdated_against_target:'선택 대상과 불일치',unable_to_verify:'확인 불가'};
  check(row.querySelector('[data-testid="library-freshness"]').textContent.trim()==='· 소스 상태: '+(f?fl[f.status]+' · '+f.coverage.assessed+'/'+f.coverage.eligible:'미평가'),'saved.library-freshness');
  check(e.review_state?row.textContent.includes('내 검토: '+{unreviewed:'미검토',reviewing:'검토 중',reviewed:'검토 완료'}[e.review_state]):!row.textContent.includes('내 검토:'),'saved.library-independent-review');
  check(row.textContent.includes('검토한 이후 업데이트됨')===Boolean(e.updated_since_review),'saved.library-review-update-state');
  check(row.querySelector('[data-testid="library-read"]').getAttribute('href')==='/documents/'+c.document_id+'/versions/'+e.latest_version_id,'saved.library-latest-version');
  log('saved.library.'+C.cases.indexOf(c));
 }
 if(C.late){
 const c=C.cases[C.late.case_index],a=c.expected.applicable;
 await navigate(c.route);await evidence(c);
 const params={version_id:c.version_id,...(c.scope.kind==='version'?{}:{scope_kind:c.scope.kind,page_path:c.scope.page_path}),target_set_id:C.late.older_target_set_id};
 const url=c.history_path+(c.history_path.includes('?')?'&':'?')+'target_set_id=';
 const old=await get(url+encodeURIComponent(params.target_set_id),'versionAssessments',params);
 check(old.applicable?.target_set_id===params.target_set_id,'saved.genuine-older-target');
 await navigate(c.route);await selectEvidence(c);last='saved.held-old-request';await wait(()=>block()?.textContent.includes('평가 이력/요약을 확인하는 중'));await settle();
 await navigate(c.route);await evidence(c);
 await get(url+encodeURIComponent(a.target_set_id),'versionAssessments',{...params,target_set_id:a.target_set_id});await settle();
 check(block().textContent.includes('대상 집합 '+a.target_set_id)&&block().textContent.includes('캡처 '+a.captured_at)&&block().textContent.includes('평가 시각 '+a.assessed_at),'saved.late-old-does-not-replace-new');
 await semanticDetails(c);
 log('saved.same-scope-late-target');
 }
 if(C.semantic_details&&!native){
  await navigate('/library?'+new URLSearchParams({project_id:C.cases[0].project_id,lifecycle:'all'}));
  last='saved.browser-reader-disposed';await wait(()=>!document.querySelector('iframe.reader-frame'));log(last);
 }
}catch(error){
 const b=block(),t=b?.textContent??'',whole=button('버전 전체 평가');
 rows.push({name:'saved.failure.'+last,status:'fail',diagnostic:{
  whole_button_present:!!whole,whole_pressed:whole?.getAttribute('aria-pressed')==='true',
  source_panel_count:document.querySelectorAll('.source-panel').length,
  renderer_frame_count:document.querySelectorAll('iframe.reader-frame').length,
  version_scope_visible:t.includes('범위 버전 전체'),page_scope_visible:t.includes('범위 페이지 '),
  loading:t.includes('평가 이력/요약을 확인하는 중'),
  foreign_applicable:t.includes('반환된 적용 가능 요약이 선택된 범위와 일치하지'),
  history_retry:!!button('실패한 지점부터 다시 불러오기',b??document),
  coverage_retry:!!button('커버리지 다시 불러오기',b??document),
  history_error:(b?.querySelector('.banner p')?.textContent??'').replace(/^평가 이력 페이지를 확인하지 못했습니다: /,'').split(' — 이미 열거된 ')[0].replace(/https?:\/\/\S+/g,'<location>').slice(0,180)
 }});
}
rows.push({name:'page.error',status:pageError?'fail':'pass'},{name:'page.done',status:'pass'});
if(native)await window.haeryongNativeCall(701101,[{Rows:rows}]);
return rows;
})()
"""
    script=script.replace('CONFIG',json.dumps(probe_config,ensure_ascii=False),1)
    browser_open=False; socket_dir=None
    try:
        if args.saved_browser_cli:
            def browser(command,result=False):
                p=subprocess.run([str(args.saved_browser_cli.resolve(strict=True)),'--session='+work.name,*command],
                                 cwd=work,env=env,capture_output=True,text=True,timeout=240)
                if command[0]=='open':
                    # Startup precedes credential injection and imported-page
                    # loading. Preserve its actual cause before classifying it.
                    for stream,value in (('stdout',p.stdout),('stderr',p.stderr)):
                        diagnostic_path=work/('browser-startup-'+stream+'.txt')
                        diagnostic_path.write_text(value);diagnostic_path.chmod(0o600)
                if p.returncode!=0 or '### Error' in p.stdout:
                    diagnostic={'stage':command[0],'exit_code':p.returncode}
                    match=re.search(r'(?:npm (?:ERR!|error) code|\bError:)\s*([A-Za-z_][A-Za-z_0-9-]{0,40})',p.stderr)
                    if match:diagnostic['error_class']=match.group(1)
                    if command[0]=='open':
                        lines=p.stdout.splitlines();start=next((i for i,line in enumerate(lines) if line.startswith('### Error')),None)
                        if start is not None and start+1<len(lines):
                            diagnostic['startup_message']=re.sub(r'https?://\S+|/(?:private|Users|Volumes|Applications)/\S+','<location>',lines[start+1])[:240]
                    proof['browser_failure']=diagnostic
                check(p.returncode==0 and '### Error' not in p.stdout,'saved browser command failed (diagnostic withheld)')
                if result:
                    marker='### Result'
                    check(marker in p.stdout,'saved browser result missing')
                    return json.JSONDecoder().raw_decode(p.stdout.split(marker,1)[1].lstrip())[0]
            socket_dir=pathlib.Path(tempfile.mkdtemp(prefix='hes-',dir='/tmp')).resolve()
            env.update(PLAYWRIGHT_DAEMON_SOCKETS_DIR=str(socket_dir),PWTEST_SOCKETS_DIR=str(socket_dir),PLAYWRIGHT_DAEMON_SESSION_DIR=str(work/'registry'))
            browser(['open',origin+'/health/live','--browser','chrome','--profile',str(work/'browser-profile')]);browser_open=True
            browser(['run-code',"async page=>{await page.evaluate(()=>localStorage.setItem('haeryong.owner-session.v1','saved-record-probe'));return true;}"],True)
            browser(['run-code','async page=>{await page.goto('+json.dumps(origin+cases[0]['route'])+');return true;}'],True)
            rows=browser(['run-code','async page=>await page.evaluate('+json.dumps(script)+')'],True)
        else:
            native=pathlib.Path(cfg['native_binary']).resolve(strict=True)
            dist=pathlib.Path(cfg['native_dist']).resolve(strict=True)
            check(dist.is_dir(),'prepared native dist missing')
            prepared={p:hashlib.sha256(p.read_bytes()).hexdigest() for p in [native,*sorted(p for p in dist.rglob('*') if p.is_file())]}
            credential_output=state/'native-session';credential_output.write_text(token+'\n');credential_output.chmod(0o600)
            probe=work/'probe-saved-evidence.js';probe.write_text(script)
            process=launch(['--reader','--reader-port','9271','--reader-path',cases[0]['route'],'--reader-probe',str(probe),'--reader-close-on-done'],'240s',base=origin)
            rows=final_report(process.pid,230);check(rows is not None,'saved native final report missing')
            proof['cases']=rows
            check(process.wait(timeout=30)==0,'saved native exit')
            observed=receipts(process.pid) or []
            closes=next((r['rows'] for r in observed if r['event']=='windowClose'),None)
            opened={(r['version_id'],r['session_id']) for r in observed if r['event']=='renderSessionOpen'}
            explicit={(r['version_id'],r['session_id']) for r in observed if r['event']=='renderSessionClose'}
            network=json.loads((state/f'native-network-observations-{process.pid}.json').read_text())
            confirmed=set()
            for version_id,session_id in opened:
                path=f'/api/v1/versions/{version_id}/render-sessions/{session_id}/close'
                http204=any(r.get('method')=='POST' and r.get('path')==path and r.get('status')==204 for r in network)
                window204=any(r.get('version_id')==version_id and r.get('session_id')==session_id and r.get('status')==204 for r in (closes or []))
                if http204 and ((version_id,session_id) in explicit or window204):
                    confirmed.add((version_id,session_id))
            proof['native_close_facts']={'window_close_rows':closes,'render_session_open_count':len(opened),
                'confirmed_closed_count':len(confirmed),'unclosed':sorted(opened-confirmed)}
            check(all(r['status']=='pass' for r in rows),'saved native probe assertion failed')
            check(bool(opened) and closes is not None and all(r.get('status')==204 for r in closes)
                  and confirmed==opened,'saved native actual render closes')
            check(set(prepared)=={native,*[p for p in dist.rglob('*') if p.is_file()]} and all(hashlib.sha256(p.read_bytes()).hexdigest()==digest for p,digest in prepared.items()),'prepared native outputs changed')
            proof['native_binary_sha256']=prepared[native]
            proof['native_dist_sha256']={str(p.relative_to(dist)):hashlib.sha256(p.read_bytes()).hexdigest() for p in dist.rglob('*') if p.is_file()}
        proof['cases']=rows
        check(all(r['status']=='pass' for r in rows),'saved UI assertion failed')
        if semantic_details and args.saved_browser_cli:
            def browser_closed():
                with lock:events=list(proof.get('render_http_events',[]))
                opened={(r['version_id'],r['session_id']) for r in events if r['operation']=='open' and r['status'] in (200,201)}
                closed={(r['version_id'],r['session_id']) for r in events if r['operation']=='close' and r['status']==204}
                return {'opened':sorted(opened),'closed':sorted(closed),'unclosed':sorted(opened-closed)} if opened and opened<=closed else None
            proof['browser_close_facts']=wait_for(browser_closed,10)
            check(proof['browser_close_facts'] is not None,'saved browser real render HTTP204 closes')
        require(started.is_set() and delivered.is_set(),'same-scope genuine late response not delivered')
        for kind in ('history','coverage'):
            fault=control['faults'].get(kind)
            if not fault or fault['attempts']<2:
                proof['gaps'].append('Required '+kind+' later-cursor failure/retry missing even with public limit=1; select saved records with at least two matching rows.')
        if not args.saved_record_subset:check(not proof['gaps'],'mandatory saved pagination proof missing')
        check(not control['errors'],'owned proxy error')
        check([snapshot(c) for c in cases]==snapshots,'Review/lifecycle/provenance changed during read campaign')
        check([get(history_path(c)) for c in cases]==defaults,'unprojected default target/history changed during campaign')
        for c in cases:
            check(get(c['selected_history_path'])['applicable']==c['expected']['applicable'],'applicable changed during campaign')
            if c['units_url'] is not None:check(canonical(enumerate_rows(c['units_url']))==canonical(c['expected']['units']),'fixed units changed during campaign')
            final_history=enumerate_rows(c['selected_history_path'])
            check(sorted(r['assessment_id'] for r in final_history)==sorted(c['expected']['history_ids']),'history changed during campaign')
            if semantic_details:
                check(canonical(final_history)==canonical(c['expected']['history']),'semantic history changed during campaign')
                check(get('/api/v1/versions/'+c['version_id']+'/metadata?kind=supplied',False)==c['expected']['authored_metadata'],'authored metadata changed during campaign')
        if semantic_details:check(all(hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()==sha for path,sha in proof['prepared_inputs'].items()),'prepared RG3 inputs changed')
        check(hashlib.sha256(args.existing_records.read_bytes()).hexdigest()==proof['manifest_sha256'],'saved manifest changed')
        assert_inputs_unchanged('saved-record observations')
        proof['pagination']=control['faults'];proof['inputs']=before;proof['selected_complete']=True;proof['complete']=not proof['gaps']
    finally:
        release.set()
        stopping.set()
        cleanup=[]
        if browser_open:
            try:browser(['close'])
            except Exception as error:cleanup.append(type(error).__name__)
        for process,handle in runs:
            try:
                if process.poll() is None:process.kill();process.wait(timeout=10)
            except Exception as error:cleanup.append(type(error).__name__)
            finally:handle.close()
        with lock:active=list(streams)
        for response in active:
            try:response.close()
            except Exception as error:cleanup.append(type(error).__name__)
        server.shutdown();server.server_close()
        if socket_dir is not None:shutil.rmtree(socket_dir)
        proof['proxy_errors']=control['errors'];proof['pagination']=control['faults'];proof['cleanup_errors']=cleanup
        proof['complete']=proof['complete'] and not cleanup and not control['errors']
        proof['selected_complete']=proof.get('selected_complete',False) and not cleanup and not control['errors']
        check(not cleanup,'owned saved-campaign cleanup failed')

if args.existing_records:
    try:
        run_saved_records()
    except BaseException as error:
        proof['failure_type']=type(error).__name__
        raise
    finally:
        proof['inputs_unchanged']=fingerprints()==before
        proof['complete']=proof['complete'] and proof['inputs_unchanged']
        RECEIPT.write_text(json.dumps(proof,ensure_ascii=False,indent=2)+'\n')
        if not os.environ.get('H3N_RETAIN'):shutil.rmtree(work)
    raise SystemExit(0 if proof['complete'] or args.saved_record_subset and proof.get('selected_complete',False) and proof['inputs_unchanged'] else 1)
try:
    subprocess.run(['go','build','-tags','haeryong_test_state','-o',str(work/'haeryong'),'./cmd/haeryong'],cwd=REPO,env=env,capture_output=True,check=True,timeout=180)
    subprocess.run(['go','-C','spikes/viewer/desktop','build','-o',str(work/'native'),'.'],cwd=REPO,env=env,capture_output=True,check=True,timeout=240)
    subprocess.run([str(REPO/'frontend/node_modules/.bin/vite'),'build','--config','vite.native.config.ts','--outDir',str(work/'dist-native'),'--emptyOutDir'],cwd=REPO/'frontend',env=env,capture_output=True,check=True,timeout=240)
    assert_inputs_unchanged('isolated build')
    product=h.Product(work/'haeryong',work);product.initialize();product.start(provider_binary=args.provider_binary if 'providers' in STAGES else None);owner=product.owner('native reader mode')
    source=work/'source';(source/'nested').mkdir(parents=True)
    first="""<!doctype html><meta charset=utf-8><title>Native first</title><h1 id=heading>첫 페이지</h1><div id=framework></div><button id=increment onclick="this.textContent=String(Number(this.textContent)+1)">0</button><div style="height:2600px"></div><h2 id=part>끝</h2>
<script>new Worker('./worker.js').onmessage=e=>{window.workerValue=e.data;window.dispatchEvent(new Event('workerready'));};</script>
<script type="module">
import {value} from './module.js';window.moduleValue=value;
await new Promise(resolve=>{if(window.workerValue)resolve();else window.addEventListener('workerready',resolve,{once:true});setTimeout(resolve,15000);});
// Non-blocking capture: the loader rejects sendContext with "Page channel is
// not ready" until host.connect arrives. Retry ONLY that specific pre-dispatch
// error within a bounded deadline (no server effect); any other error is
// reported, never swallowed. Exactly one successful large capture runs; the
// popup (which needs the logical-route template established by ready) goes
// strictly after it.
const __nativeCapture=(async()=>{
  const {h,render}=await import('https://esm.sh/preact@10.27.2');
  render(h('strong',{},'Preact mounted'),document.querySelector('#framework'));
  const framework=document.querySelector('#framework').textContent;
  const {instance}=await WebAssembly.instantiate(new Uint8Array([0,97,115,109,1,0,0,0,1,5,1,96,0,1,127,3,2,1,0,7,10,1,6,97,110,115,119,101,114,0,0,10,6,1,4,0,65,42,11]));
  const wasm=instance.exports.answer();document.querySelector('#increment').click();
  const click=document.querySelector('#increment').textContent;
  const text='module='+window.moduleValue+';worker='+window.workerValue+';framework='+framework+';wasm='+wasm+';click='+click+';'+'haeryong-native-large-context-'.repeat(32000);
  const deadline=Date.now()+30000;
  for(;;){
    try{
      return await window.__haeryong.state({selection:'native large context',text});
    }catch(error){
      if(String(error&&error.message||error)!=='Page channel is not ready'||Date.now()>deadline){
        window.__nativeCaptureError=String(error&&error.stack||error);
        throw error;
      }
      await new Promise(r=>setTimeout(r,250));
    }
  }
})();
for(let i=0;i<8;i++){try{window.webkit.messageHandlers.external.postMessage(JSON.stringify({kind:'haeyong.native',id:'child-'+i,object:0,method:0,args:{'call-id':'child-'+i,methodID:701100,args:[]}}));}catch(e){}}
for(let i=0;i<6;i++){try{await fetch('wails://denied/'+i);}catch(e){}}
(async()=>{try{await __nativeCapture;window.open('first.html?popup=native#part','_blank','noopener');}catch(e){window.__nativeCaptureError=window.__nativeCaptureError||String(e);}})();
</script>"""
    (source/'first.html').write_text(first)
    (source/'module.js').write_text('export const value="module-running";')
    (source/'worker.js').write_text('postMessage("worker-running")')
    (source/'attachment.bin').write_bytes(bytes(range(256))*513)
    _,_,published=product.import_tree(owner,source);version=published['version_id'];document=published['document_id']
    credential=state/'native-session';credential.write_text(owner['token']+'\n');os.chmod(credential,0o600)

    if 'providers' in STAGES:
        probe=work/'provider-probe.js'
        probe.write_text((REPO/'spikes/viewer/provider_probe.js').read_text())
        app=launch(['--reader','--reader-port','9271','--reader-path','/settings','--reader-probe',str(probe),'--reader-close-on-done'],'120s')
        rows=final_report(app.pid,110)
        # Preserve only bounded declared probe fields before a failing assertion.
        expected_names={'settings.general','settings.connect','settings.ai-navigation','settings.ai-connect','provider.actual-screen','provider.actual-named-status','provider.catalog-not-entitlement','provider.rendered-status','provider.no-js-owner-token','provider.no-account-secrets','provider.refresh-enabled','provider.refresh-completed','provider.future-admission-visible','page.stage','page.error','page.done'}
        allowed_names=expected_names|{'provider.probe-failed'}
        allowed_stages={'general-landing','connect-from-general','general-return','connect-from-ai','starting','transport-ready','provider-screen','named-status','provider-status-rendered','refresh-busy','refresh-final'}
        allowed_states={'ready','auth_required','quota_unavailable','quota_exhausted','unsupported_version','source_unavailable','paused','provider_error','unknown'}
        def safe_rows(observed):
            safe=[]
            for row in observed or []:
                detail=row.get('detail') or {};picked={}
                if detail.get('stage') in allowed_stages:picked['stage']=detail['stage']
                if detail.get('state') in allowed_states:picked['state']=detail['state']
                if detail.get('allowanceKind') in {'null','boolean','undefined','other'}:picked['allowanceKind']=detail['allowanceKind']
                version=detail.get('version')
                if isinstance(version,str) and len(version)<30 and (version=='unverified' or all(part.isdigit() for part in version.split('.'))):picked['version']=version
                snapshot=detail.get('snapshot')
                if isinstance(snapshot,dict):
                    picked['snapshot']={key:snapshot[key] for key in ['transport','screen','status','busy','providerError'] if isinstance(snapshot.get(key),bool)}
                    if snapshot.get('errorCategory') in {'invalid_provider_status','stale_response','other_ui_error','none'}:picked['snapshot']['errorCategory']=snapshot['errorCategory']
                safe.append({'name':row.get('name') if row.get('name') in allowed_names else 'unknown_probe_row','status':row.get('status') if row.get('status') in {'pass','fail','pending'} else 'fail','detail':picked})
            return safe
        last_rows=rows
        if last_rows is None:
            last_path=state/f'native-reader-selfcheck-{app.pid}.json'
            if last_path.exists():
                try:last_rows=json.loads(last_path.read_text()).get('rows')
                except (ValueError,KeyError):last_rows=None
        proof['provider_probe_rows']=safe_rows(last_rows)
        observation_path=state/f'native-call-observations-{app.pid}.json'
        observations=json.loads(observation_path.read_text()) if observation_path.exists() else []
        operations=[row['operation'] for row in observations if row.get('operation')=='providerStatus']
        proof['provider_operations']=operations
        proof['private_runtime_retained']=os.environ.get('H3N_RETAIN') is not None
        assert rows is not None,'provider native final report missing'
        failed=[r for r in rows if r['status']!='pass']
        assert not failed,json.dumps(safe_rows(failed),ensure_ascii=False)[:1000]
        assert len(rows)==len(expected_names) and {row['name'] for row in rows}==expected_names,'provider/settings probe membership mismatch'
        assert app.wait(timeout=130)==0,'native provider App failed to close'
        rows=safe_rows(rows)
        assert operations.count('providerStatus')>=3,'actual narrow provider status calls missing'
        guard=json.loads((state/'native-guard.json').read_text())
        assert guard.get('installed') in (1,True) and guard.get('pid')==app.pid,'native provider guard missing'
        proof['cases'].append({'kind':'TASK-006-03 actual native App provider status and refresh','input_method':'trusted-host DOM click; browser owns OS keyboard/touch evidence','probe_rows':rows,'operations':operations,'inference':False})
        proof['excluded_stages']=[{'stage':s,'status':'not_run','reason':'standalone provider status; no renderer or inference claim'} for s in sorted(ALL_STAGES)]

    # Explicit local Sources campaign: actual compiled App and existing named
    # configuration facade. No capture API, provider call or renderer proof.
    if 'sources' in STAGES:
        source_root=work/'settings-source';source_root.mkdir()
        source_alias=work/'settings-alias';source_alias.symlink_to(source_root,target_is_directory=True)
        project=product.data('GET','/api/v1/capabilities',client=owner)['unfiled_project_id']
        settings_route='/settings/projects?'+urllib.parse.urlencode({'project':project})
        settings_probe=work/'probe-source-settings.js'
        settings_probe.write_text(r"""
        const c=SOURCE_SETTINGS_CONSTANTS,rows=[];let done=false;
        let pageError=false;
        addEventListener('error',()=>{pageError=true;});
        addEventListener('unhandledrejection',()=>{pageError=true;});
        const report=()=>window.haeryongNativeCall(701101,[{Rows:[...rows,{name:'page.error',status:pageError?'fail':'pass',detail:{}},{name:'page.done',status:done?'pass':'pending',detail:{}}]}]);
        setInterval(()=>void report().catch(()=>{}),2000);
        const record=(name,ok)=>{rows.push({name,status:ok?'pass':'fail',detail:{}});if(!ok)throw Error(name);};
        const wait=async(fn,label)=>{const end=Date.now()+25000;while(Date.now()<end){if(fn())return;await new Promise(r=>setTimeout(r,100));}throw Error(label);};
        const button=name=>[...document.querySelectorAll('button')].find(b=>b.textContent===name);
        const control=name=>[...document.querySelectorAll('label')].find(l=>l.textContent.startsWith(name))?.querySelector('input,select');
        const input=(name,value)=>{const el=control(name);if(!el)throw Error('missing settings control');const proto=el.tagName==='SELECT'?HTMLSelectElement.prototype:HTMLInputElement.prototype;Object.getOwnPropertyDescriptor(proto,'value').set.call(el,value);el.dispatchEvent(new Event(el.tagName==='SELECT'?'change':'input',{bubbles:true}));};
        const settle=()=>new Promise(r=>setTimeout(r,0));
        const call=async(operation,payload={})=>{const reply=await window.haeryongNativeCall(701100,[{Operation:operation,Payload:payload}]);if(!reply.ok)throw Error('native settings operation failed: '+operation);return reply.value;};
        const read=()=>call('configurationView');
        const preview=async()=>{await settle();button('변경 미리보기').click();await wait(()=>button('확인 후 적용'),'settings preview');};
        const confirm=async()=>{const el=document.querySelector('[aria-label="설정 계획 미리보기"] input[type="checkbox"]');if(el)el.click();await settle();await wait(()=>!button('확인 후 적용').disabled,'confirmed settings commit');button('확인 후 적용').click();await wait(()=>document.body.textContent.includes('출처 설정 적용됨'),'settings committed');};
        const reopen=async()=>{button('원본 목록으로').click();await wait(()=>button('출처 설정 편집'),'source list');record('sources.logical-list-no-host-path',!document.querySelector('main').textContent.includes(c.root));button('출처 설정 편집').click();await wait(()=>control('기본 Provider ID'),'reopened settings');};
        try{
          await wait(()=>window.__haeryongNativeTransport==='HAEYONG_NATIVE_TRANSPORT_BOUND'&&document.querySelector('.service-state[data-state="verified"]'),'native verified settings');
          await wait(()=>control('기본 Provider ID'),'settings form');
          record('sources.no-js-owner-credential',!Object.keys(localStorage).some(k=>k.includes('owner-session')));
          const initial=await read();
          if(c.phase==='configure'){
            record('sources.initial-defaults',control('기본 소스 모드').value==='pinned'&&control('기본 Provider ID').value==='');
            input('기본 소스 모드','current');input('기본 Provider ID','  prov-default  ');
            button('출처 추가').click();await wait(()=>control('종류'),'new source form');
            input('종류','files');await settle();input('승인할 원본 루트',c.alias);
            input('기본 소스 모드','pinned');input('기본 Provider ID','');await preview();
            record('sources.source-only-confirmation',button('확인 후 적용').disabled&&!!control('출처/공개 범위 확대 영향을 확인했습니다')&&!control('기본 Provider 변경 영향을 확인했습니다'));
            button('취소').click();await wait(()=>control('기본 Provider ID'),'source-only preview cancelled');
            input('기본 소스 모드','current');input('기본 Provider ID','  prov-default  ');
            await preview();
            const text=document.querySelector('[aria-label="설정 계획 미리보기"]').textContent;
            record('sources.normalized-preview',text.includes(c.root)&&!text.includes(c.alias)&&text.includes('기본 소스 모드 current · 기본 Provider prov-default'));
            record('sources.expansion-confirmation-required',button('확인 후 적용').disabled&&!!control('출처/공개 범위 확대와 기본 Provider 변경 영향을 확인했습니다'));
            button('취소').click();await wait(()=>control('기본 Provider ID'),'cancelled settings');
            record('sources.cancel-no-commit',(await read()).config_revision===initial.config_revision);
            await preview();await confirm();await reopen();
            let current=await read(),saved=current.configuration.projects[c.project];
            record('sources.remembered-defaults',control('기본 소스 모드').value==='current'&&control('기본 Provider ID').value==='prov-default'&&saved.source_mode_default==='current'&&saved.provider_id==='prov-default');
            record('sources.default-provider-no-disclosure',saved.sources.length===1&&saved.sources[0].provider_ids.length===0);
            // Default provider alone is a visible owner choice, never an
            // implicit widening of the per-source disclosure inventory.
            input('기본 Provider ID','prov-next');await preview();
            const providerImpact=document.querySelector('[aria-label="기본 Provider 변경 영향"]').textContent;
            record('sources.default-provider-impact-confirmation',button('확인 후 적용').disabled&&!!control('기본 Provider 변경 영향을 확인했습니다')&&!control('출처/공개 범위 확대 영향을 확인했습니다')&&providerImpact.includes('기본 Provider: prov-default → prov-next')&&providerImpact.includes('기본 Provider 변경만으로는 출처별 공개 허용 목록이 바뀌지 않습니다.'));
            button('취소').click();await wait(()=>control('기본 Provider ID'),'default preview cancelled');
            record('sources.default-provider-cancel-no-commit',(await read()).config_revision===current.config_revision);
            input('기본 소스 모드','pinned');input('기본 Provider ID','prov-stale');await preview();
            // A distinct real owner commit makes the normal UI plan stale.
            current=await read();
            const other=await call('configurationPlan',{request:{scope:{kind:'project',project_id:c.project},expected_config_revision:current.config_revision,changes:{sources:current.configuration.projects[c.project].sources}},operation_id:crypto.randomUUID()});
            if(other.kind!=='committed')throw Error('competing plan not definitive');
            const plan=other.body;
            const switched=await call('configurationPlanCommit',{plan_id:plan.plan_id,request:{expected_config_revision:plan.base_config_revision,confirmed:true},operation_id:plan.plan_id});
            if(switched.kind!=='committed')throw Error('competing commit not definitive');
            control('기본 Provider 변경 영향을 확인했습니다').click();await settle();button('확인 후 적용').click();
            await wait(()=>button('현재 설정 다시 불러오기'),'stale settings plan');
            record('sources.stale-plan-visible',document.body.textContent.includes('계획이 오래되었습니다.'));
            button('현재 설정 다시 불러오기').click();await wait(()=>control('기본 Provider ID'),'stale settings reload');
            record('sources.stale-defaults-not-applied',control('기본 소스 모드').value==='current'&&control('기본 Provider ID').value==='prov-default');
          }else{
            // A fresh native process reloads the same actual owner settings.
            const prior=initial.configuration.projects[c.project];
            record('sources.restart-remembers-defaults',control('기본 소스 모드').value==='current'&&control('기본 Provider ID').value==='prov-default'&&prior.sources[0].provider_ids.length===0);
            input('기본 소스 모드','pinned');input('기본 Provider ID','');await preview();
            record('sources.null-clear-preview',document.querySelector('[aria-label="설정 계획 미리보기"]').textContent.includes('기본 소스 모드 pinned · 기본 Provider 없음')&&!button('확인 후 적용').disabled);
            await confirm();await reopen();
            const saved=(await read()).configuration.projects[c.project];
            record('sources.null-clear-persisted',saved.source_mode_default==='pinned'&&saved.provider_id===null&&saved.sources[0].provider_ids.length===0&&control('기본 소스 모드').value==='pinned'&&control('기본 Provider ID').value==='');
          }
        }catch(error){rows.push({name:'sources.probe-failed',status:'fail',detail:{message:String(error)}});}
        done=true;await report();
        """.replace('SOURCE_SETTINGS_CONSTANTS',json.dumps({'project':project,'root':str(source_root),'alias':str(source_alias),'phase':'configure'})))
        for phase in ('configure','clear'):
            probe=settings_probe
            if phase=='clear':
                probe=work/'probe-source-settings-clear.js'
                probe.write_text(settings_probe.read_text().replace('"phase": "configure"','"phase": "clear"'))
            app=launch(['--reader','--reader-port','9271','--reader-path',settings_route,'--reader-probe',str(probe),'--reader-close-on-done'],'150s')
            rows=final_report(app.pid,140)
            assert rows is not None,'native settings final report missing'
            failed=[r for r in rows if r['status']!='pass']
            assert not failed,json.dumps(failed,ensure_ascii=False)[:1200]
            assert app.wait(timeout=180)==0,'native settings application did not close normally'
            observations=json.loads((state/f'native-call-observations-{app.pid}.json').read_text())
            operations={row['operation'] for row in observations}
            assert {'configurationView','configurationPlan','configurationPlanCommit'}<=operations,'actual settings facade calls missing'
            guard=json.loads((state/'native-guard.json').read_text())
            assert guard.get('installed') in (1,True) and guard.get('pid')==app.pid,'native settings guard missing'
            proof['cases'].append({'kind':'actual native Sources owner configuration '+phase,'input_method':'trusted-host DOM value/events/click; no OS keyboard or touch proof','probe_rows':[r['name'] for r in rows],'operations':sorted(operations),'provider_scope':'not exercised','capture_api_scope':'not exercised'})
        confirmed=product.data('GET','/api/v1/configuration',client=owner)['configuration']['projects'][project]
        assert confirmed['source_mode_default']=='pinned' and confirmed['provider_id'] is None
        assert len(confirmed['sources'])==1 and confirmed['sources'][0]['provider_ids']==[]
        proof['excluded_stages']=[{'stage':s,'status':'not_run','reason':'standalone Sources settings campaign; renderer/provider/managed capture scope unclaimed'} for s in sorted(ALL_STAGES)]

    # 1) selfcheck through the same typed ReaderService binding and guard.
    if 'selfcheck' in STAGES:
        query='?'+urllib.parse.urlencode({'version':version,'base':product.endpoint,'file':'attachment.bin'})
        selfcheck=launch(['--reader-selfcheck','--reader-port','9271','--reader-path',query],'75s')
        rows=final_report(selfcheck.pid,90)
        assert rows is not None,'selfcheck final report never appeared'
        failed=[r for r in rows if r['status']!='pass' and r['name'] not in ('page.done',)]
        assert not failed,json.dumps(failed,ensure_ascii=False)[:1500]
        code=selfcheck.wait(timeout=120)
        assert code==0,f'selfcheck exited {code}'
        guard=json.loads((state/'native-guard.json').read_text())
        assert guard.get('installed') in (1, True),('guard not installed',guard)
        proof['cases'].append({'kind':'supplementary raw-facade selfcheck over the real daemon','rows':len(rows)})
        proof['selfcheck_rows']=[r['name'] for r in rows]

    # 2) actual compiled native App as the main frame at the fixed static
    if 'app' in STAGES:
            # origin, with an owned probe injected after guard+bootstrap.
            probe=work/'probe.js'
            probe.write_text("""
        window.__pageError=null;
        window.onerror=(m,s,l,c)=>{window.__pageError=String(m)+"@"+l+":"+c;};
        window.addEventListener('unhandledrejection',e=>{window.__pageError=String(e.reason&&(e.reason.stack||e.reason.message||e.reason));});
        window.__progress=[];window.__done=false;
        const rows=[];const log=(name,ok,detail={})=>rows.push({name,status:ok?"pass":"fail",detail});
        const wait=async(predicate,timeout)=>{const deadline=Date.now()+timeout;while(Date.now()<deadline){let value;try{value=predicate();}catch(e){window.__pageError=String(e);throw e;}if(value)return value;await new Promise(r=>setTimeout(r,250));}return null;};
        const report=async()=>{try{const r=await window.haeryongNativeCall(701101,[{Rows:[...rows,{name:"page.progress",status:"pass",detail:{steps:window.__progress}},{name:"page.error",status:window.__pageError?"fail":"pass",detail:{error:window.__pageError}},{name:"page.done",status:window.__done?"pass":"pending",detail:{}}]}]);return !!(r&&r.written===true);}catch(e){return false;}};
        setInterval(()=>{void report();},2000);
        try{
          // Native navigation completion can precede the module entrypoint.
          // Await its actual marker, never manufacture one in the probe.
          const identity=await wait(()=>window.__haeryongNativeTransport==="HAEYONG_NATIVE_TRANSPORT_BOUND",30000);
          log("transport.identity",!!identity,{value:window.__haeryongNativeTransport});
          const verified=await wait(()=>document.querySelector('.service-state[data-state=\\"verified\\"]'),30000);
          log("shell.verified",!!verified,{});
          const frame=await wait(()=>document.querySelector('iframe[title=\\"결과 뷰어\\"]'),30000);
          log("reader.frame-mounted",!!frame,{});
          const notice=await wait(()=>[...document.querySelectorAll('[role="status"]')].some(n=>n.textContent.includes("페이지 context를 확인했습니다.")),90000);
          log("reader.child-context-confirmed",!!notice,{notices:[...document.querySelectorAll('[role="status"]')].map(n=>n.textContent).slice(0,8),banners:[...document.querySelectorAll('.banner')].map(n=>n.textContent).slice(0,8)});
          const bearerKeys=Object.keys(localStorage).filter(k=>k.includes("owner-session"));
          log("shell.no-js-credential",bearerKeys.length===0,{keys:bearerKeys});
          const ready=await wait(()=>!document.body.textContent.includes("페이지를 여는 중…"),10000);
          log("reader.ready",!!ready,{});
        }catch(error){log("probe.exception",false,String(error));}
        window.__done=true;
        let reported=false;
        for(let i=0;i<3&&!reported;i++)reported=await report();
        log("report.written",reported,{});
        await report();
        """)
            app_path='/documents/{0}/versions/{1}?page=first.html'.format(document,version)
            app=launch(['--reader','--reader-port','9271','--reader-path',app_path,'--reader-probe',str(probe)],'150s')
            rows=final_report(app.pid,140)
            assert rows is not None,'probe final report never appeared for the actual app run'
            failed=[r for r in rows if r['status']!='pass' and r['name'] not in ('page.done',)]
            assert not failed,json.dumps(failed,ensure_ascii=False)[:1500]
            app_receipts=receipts(app.pid)
            events=[r['event'] for r in app_receipts]
            assert 'attachSession' in events and 'personalPut' in events,('expected attach and pointer save through the native facade',events)
            context_flow=[e for e in events if e in ('createContextUpload','putUploadEntry','completeUpload','consumeContext')]
            assert 'consumeContext' in context_flow,('context channel never completed through the typed facade',events)
            code=app.wait(timeout=180)
            assert code==0,f'app exited {code}'
            final=receipts(app.pid)
            closeRows=next((r['rows'] for r in final if r['event']=='windowClose'),None)
            assert closeRows and all(row.get('status')==204 for row in closeRows),('normal close rows must confirm explicit 204 closes',closeRows)
            guard=json.loads((state/'native-guard.json').read_text())
            aggregate=guard.get('denied_messages',0)+guard.get('scheme_denied',0)
            assert aggregate>=12,('native denial aggregate (excluding plain popups) below the original boundary',guard)
            assert guard.get('plain_popups',0)>=1,('no plain unprivileged popup was created',guard)
            popup_urls=guard.get('popup_urls') or []
            expected_popup=product.endpoint+f'/documents/{document}/versions/{version}?'+urllib.parse.urlencode({'page':'first.html','page_query':'popup=native','page_fragment':'part'})
            assert popup_urls and popup_urls[0]==expected_popup,('popup target is not the exact public browser Reader route',popup_urls,expected_popup)
            consume=next((r for r in final if r['event']=='consumeContext'),None)
            verify=next((r for r in final if r['event']=='verifyContextFresh'),None)
            assert consume and consume.get('bytes_sha256'),('context consume receipt lacks the exact server digest',consume)
            assert verify and verify.get('etag')==f"\"sha256-{consume['bytes_sha256']}\"",('fresh proof etag is not bound to the consumed exact digest',verify)
            # Known expected bytes: the imported corpus artifacts carry the
            # exact module/Worker constants, and the page embeds them plus the
            # full large-context payload construction into the capture.
            corpus_html=(source/'first.html').read_text()
            assert 'module-running' in (source/'module.js').read_text() and 'worker-running' in (source/'worker.js').read_text(),('corpus modules do not carry the known constant bytes',None)
            assert "'module='+window.moduleValue" in corpus_html and "';worker='+window.workerValue" in corpus_html,('corpus page does not embed the live module/Worker values into the captured payload',None)
            assert "haeryong-native-large-context-'.repeat(32000)" in corpus_html,('corpus does not embed the known payload construction',None)
            expected_text='module=module-running;worker=worker-running;framework=Preact mounted;wasm=42;click=1;'+'haeryong-native-large-context-'*32000
            expected_bytes=json.dumps({'selection':'native large context','text':expected_text},ensure_ascii=False,separators=(',',':')).encode('utf-8')
            expected_digest=hashlib.sha256(expected_bytes).hexdigest()
            assert consume['bytes_sha256']==expected_digest,('actual native module/worker/framework/WASM/interaction/context bytes differ',consume['bytes_sha256'],expected_digest)
            proof['native_context']={'expected_bytes':len(expected_bytes),'expected_digest':expected_digest,'actual_digest':consume['bytes_sha256'],'external_framework':'Preact@10.27.2','wasm_answer':42,'programmatic_author_click':1}
            proof['cases'].append({'kind':'actual compiled native Reader: verified shell, mounted viewer, large digest-bound context (known embedded bytes) through the typed JSON/Go path, exact popup target, >=12 denials, 204 closes','probe_rows':[r['name'] for r in rows],'events':events,'guard':{k:guard.get(k) for k in ('denied_messages','scheme_denied','plain_popups','popup_urls')},'context_digest':consume.get('bytes_sha256'),'known_embedded':{'module':'module-running','worker':'worker-running','payload_repeat':32000}})


    # SC18: actual compiled native Library, real daemon and closed search facade.
    if 'search' in STAGES:
        search_source=work/'search-source'; (search_source/'nested').mkdir(parents=True)
        search_path='nested/검토 %2F #?.html'
        # Only the selected OLD page executes this authored capture. Its real
        # heading/text flow through the isolated loader and Go context receipt;
        # a parent URL or a mounted iframe cannot satisfy the byte oracle.
        search_capture = """<script>(async()=>{
          const deadline=Date.now()+20000;
          for(;;){try{await window.__haeryong.state({selection:'native search exact old page',text:document.querySelector('h1').textContent+'|'+document.querySelector('#retry').textContent});break;}
          catch(e){if(String(e.message)!=='Page channel is not ready'||Date.now()>deadline)throw e;await new Promise(r=>setTimeout(r,100));}}
        })();</script>"""
        (search_source/search_path).write_text('<meta charset=utf-8><title>Native search</title><h1>검색원본</h1><p id="retry">readSource 검색표식</p>'+search_capture)
        (search_source/'aaa-default.html').write_text('<meta charset=utf-8><h1>N3 distinct default page</h1>')
        for n in range(54): (search_source/f'page{n:02}.html').write_text(f'<meta charset=utf-8><h1>검색표식 {n}</h1>')
        _,_,search_published=product.import_tree(owner,search_source)
        search_version=search_published['version_id'];search_document=search_published['document_id']
        (search_source/search_path).write_text('<meta charset=utf-8><h1>새 버전</h1>')
        product.import_tree(owner,search_source,extra=['--document',search_document])
        deadline=time.monotonic()+120
        while True:
            status,reply,_=product.request('GET','/api/v1/search?q=&versions=all',client=owner)
            assert status in {200,503},status
            if status==200 and reply['data']['index']['state']=='complete':break
            assert time.monotonic()<deadline,'native search fixture index did not complete'
            time.sleep(.25)
        default_bootstrap=product.data('GET',f'/api/v1/versions/{search_version}/render-bootstrap',client=owner)
        assert default_bootstrap['selected_page']!=search_path,'N3 fixture must distinguish dropped page_path from the exact hit'
        search_probe=work/'probe-search.js'
        search_probe.write_text(r"""
        window.__pageError=null;window.onerror=m=>{window.__pageError=String(m)};
        const rows=[];const log=(name,ok,detail={})=>{rows.push({name,status:ok?'pass':'fail',detail});if(!ok)throw Error(name);};
        let done=false;
        const report=async()=>{try{return await window.haeryongNativeCall(701101,[{Rows:[...rows,{name:'page.error',status:window.__pageError?'fail':'pass',detail:{}},{name:'page.done',status:done?'pass':'pending',detail:{}}]}]);}catch(e){return null;}};
        setInterval(()=>void report(),2000);
        const wait=async fn=>{const end=Date.now()+30000;while(Date.now()<end){const v=fn();if(v)return v;await new Promise(r=>setTimeout(r,100));}throw Error('native search wait timed out');};
        const input=(name,value)=>{const el=[...document.querySelectorAll('label')].find(l=>l.textContent.startsWith(name))?.querySelector('input,select');if(!el)throw Error('missing input '+name);const setter=Object.getOwnPropertyDescriptor(el.tagName==='INPUT'?HTMLInputElement.prototype:HTMLSelectElement.prototype,'value').set;setter.call(el,value);el.dispatchEvent(new Event(el.tagName==='INPUT'?'input':'change',{bubbles:true}));};
        const button=name=>[...document.querySelectorAll('button')].find(b=>b.textContent===name);
        (async()=>{try{
          await wait(()=>document.querySelector('.service-state[data-state="verified"]'));
          await wait(()=>document.querySelector('form[role="search"]'));
          input('검색어','검색표식');input('검색 버전','all');
          await new Promise(r=>setTimeout(r,50));button('검색').click();
          await wait(()=>document.querySelectorAll('[data-testid="search-hit"]').length===50);
          button('검색 결과 더 보기').click();await wait(()=>document.querySelectorAll('[data-testid="search-hit"]').length>50);log('search.more',true);
          input('수명 주기','all');await wait(()=>new URL(location.href).searchParams.get('lifecycle')==='all');
          input('검색어','readSource');await new Promise(r=>setTimeout(r,50));button('검색').click();
          const hit=await wait(()=>[...document.querySelectorAll('[data-testid="search-hit"]')].find(n=>n.textContent.includes('readSource')));
          const anchor=hit.querySelector('a');const url=new URL(anchor.href);
          log('search.exact-old-Unicode-page',url.pathname===EXACT_ROUTE&&url.searchParams.get('page')===EXACT_PAGE&&!url.hash);
          anchor.click();await wait(()=>location.pathname===EXACT_ROUTE&&document.querySelector('iframe[title="결과 뷰어"]'));
          await wait(()=>[...document.querySelectorAll('[role="status"]')].some(n=>n.textContent.includes('페이지 context를 확인했습니다.')));
          log('search.open-matching-page-content-captured',true);log('search.no-JS-credential',!Object.keys(localStorage).some(k=>k.includes('owner-session')));
        }catch(e){rows.push({name:'probe.exception',status:'fail',detail:String(e)});}
        done=true;await report();})();
        """.replace('EXACT_ROUTE',json.dumps(f'/documents/{search_document}/versions/{search_version}')).replace('EXACT_PAGE',json.dumps(search_path,ensure_ascii=False)))
        search_app=launch(['--reader','--reader-port','9271','--reader-path','/library','--reader-probe',str(search_probe)],'65s')
        search_rows=final_report(search_app.pid,100)
        assert search_rows is not None,'native search report never appeared'
        assert not [r for r in search_rows if r['status']!='pass'],json.dumps(search_rows,ensure_ascii=False)[:1500]
        assert search_app.wait(timeout=100)==0
        search_consumed=next((r for r in (receipts(search_app.pid) or []) if r['event']=='consumeContext'),None)
        expected_search_bytes=json.dumps({'selection':'native search exact old page','text':'검색원본|readSource 검색표식'},ensure_ascii=False,separators=(',',':')).encode()
        expected_search_digest=hashlib.sha256(expected_search_bytes).hexdigest()
        assert search_consumed and search_consumed.get('bytes_sha256')==expected_search_digest,('N3 selected old page content digest mismatch',search_consumed,expected_search_digest)
        proof['cases'].append({'kind':'SC18 actual native Library search/more/filter and exact old Unicode page through real facade','probe_rows':[r['name'] for r in search_rows],'selected_page_digest':expected_search_digest,'actual_digest':search_consumed['bytes_sha256']})

        # Labelled owner/scoped safe-reason presentation through the real native
        # App/facade; proxy responses are seeds, not a new scoped credential flow.
        import http.server,threading,urllib.request
        sample=product.data('GET','/api/v1/search?q=readSource&versions=all',client=owner)
        sample['index']=dict(sample['index'],state='partial',reasons=[{'code':'read_failed','version_count':'1'}])
        upstream=product.endpoint
        for scoped in (False,True):
            fixture=json.loads(json.dumps(sample))
            late_started=threading.Event();late_release=threading.Event();late_sent=threading.Event();status_checks=[]
            if not scoped:fixture['index']['reasons'].append({'code':'storage_failed','version_count':'1'})
            class SearchSeed(http.server.BaseHTTPRequestHandler):
                def log_message(self,*args):pass
                def forward(self):
                    size=int(self.headers.get('Content-Length') or 0)
                    body=self.rfile.read(size) if size else None
                    request=urllib.request.Request(upstream+self.path,data=body,headers={k:v for k,v in self.headers.items() if k.lower() not in ('host','content-length')},method=self.command)
                    try:
                        with urllib.request.urlopen(request,timeout=60) as response:data=response.read();status=response.status;headers=response.getheaders()
                    except urllib.error.HTTPError as error:data=error.read();status=error.code;headers=error.headers.items()
                    self.send_response(status)
                    for k,v in headers:
                        if k.lower() not in ('transfer-encoding','content-length','connection'):self.send_header(k,v)
                    self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data)
                def do_GET(self):
                    parsed=urllib.parse.urlsplit(self.path)
                    if parsed.path=='/api/v1/status' and scoped and late_started.is_set():
                        status_checks.append(True)
                        if len(status_checks)>1:late_release.set()
                        data=json.dumps({'data':{'late_started':True,'late_released':late_release.is_set()}}).encode();self.send_response(200);self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data);return
                    if parsed.path!='/api/v1/search':return self.forward()
                    delayed=scoped and urllib.parse.parse_qs(parsed.query).get('q')==['late-era']
                    if delayed:late_started.set();assert late_release.wait(30),'late response was never released'
                    payload=json.loads(json.dumps(fixture))
                    if delayed:
                        payload['items']=[dict(hit,title='N3 STALE PROTECTED') for hit in payload['items']]
                    data=json.dumps({'data':payload}).encode();self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data)
                    if delayed:late_sent.set()
                def do_POST(self):self.forward()
                def do_PUT(self):self.forward()
                def do_DELETE(self):
                    try:self.forward()
                    finally:
                        if urllib.parse.urlsplit(self.path).path=='/api/v1/session':late_release.set()
            server=http.server.ThreadingHTTPServer(('127.0.0.1',0),SearchSeed)
            thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
            try:
                reason_probe=work/('probe-search-reasons-'+str(scoped)+'.js')
                reason_probe.write_text(r"""
                const rows=[];let done=false;
                const report=()=>window.haeryongNativeCall(701101,[{Rows:[...rows,{name:'page.done',status:done?'pass':'pending',detail:{}}]}]).catch(()=>{});
                setInterval(()=>void report(),2000);
                (async()=>{try{
                  const end=Date.now()+30000;
                  while(!document.body.textContent.includes('콘텐츠 읽기 실패 · 1개 버전')){if(Date.now()>end)throw Error('safe reason missing');await new Promise(r=>setTimeout(r,100));}
                  const hidden=document.body.textContent.includes('색인 저장 실패 · 1개 버전');
                  rows.push({name:'search.seeded-reasons-localized',status:hidden===OWNER?'pass':'fail',detail:{scoped:!OWNER}});
                  if(!OWNER){
                    const call=(op,payload={})=>window.haeryongNativeCall(701100,[{Operation:op,Payload:payload}]);
                    const wait=async fn=>{const end=Date.now()+20000;while(Date.now()<end){if(await fn())return;await new Promise(r=>setTimeout(r,100));}throw Error('late-era wait');};
                    // Owned trusted-host probe only: obtain the actual mounted
                    // component's client from React's host fiber. No product hook,
                    // new bridge operation or replacement client is installed.
                    let screen=document.querySelector('[aria-label="검색 결과"]');
                    let fiber=screen[Object.keys(screen).find(k=>k.startsWith('__reactFiber$'))],client;
                    for(;fiber;fiber=fiber.return){const candidate=fiber.memoizedProps?.client;if(candidate?.search&&candidate?.invalidate){client=candidate;break;}}
                    if(!client)throw Error('actual live search client not found');
                    const original=client.search.bind(client);let outcome=null;
                    client.search=async request=>{try{const value=await original(request);if(request.q==='late-era')outcome='resolved';return value;}catch(error){if(request.q==='late-era')outcome=error.name;throw error;}};
                    const input=document.querySelector('form[role="search"] input');
                    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'late-era');input.dispatchEvent(new Event('input',{bubbles:true}));await new Promise(r=>setTimeout(r,50));
                    document.querySelector('form[role="search"]').dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}));
                    await wait(async()=>{const r=await call('serviceStatus');return r.ok&&r.value.late_started===true;});
                    // Query submission remounts the result owner; capture the
                    // actual pending screen after that normal route transition.
                    screen=document.querySelector('[aria-label="검색 결과"]');
                    if(!screen||screen.getAttribute('aria-busy')!=='true')throw Error('live pending screen missing');
                    // Advance only the real JS client's authority era, leaving
                    // the Go attachment, route, mounted screen and request intact.
                    // The next closed status GET releases the seeded real HTTP200.
                    client.invalidate();
                    const released=await call('serviceStatus');
                    if(!released.ok||!released.value.late_released)throw Error('late200 handshake missing');
                    await wait(()=>outcome!==null);
                    await wait(()=>screen.getAttribute('aria-busy')==='false');
                    rows.push({name:'search.native-live-screen-rejects-late200-old-era',status:outcome==='StaleResponseError'&&screen===document.querySelector('[aria-label="검색 결과"]')&&location.pathname==='/library'&&new URL(location.href).searchParams.get('q')==='late-era'&&screen.querySelectorAll('[data-testid="search-hit"]').length===0&&!!screen.querySelector('[role="alert"]')?'pass':'fail',detail:{seeded_delayed_HTTP_status:200,outcome,same_live_screen:screen===document.querySelector('[aria-label="검색 결과"]')}});
                    client.search=original;
                  }
                }catch(e){rows.push({name:'probe.exception',status:'fail',detail:String(e)});}done=true;await report();})();
                """.replace('OWNER','false' if scoped else 'true'))
                reason_app=launch(['--reader','--reader-port','9271','--reader-path','/library?q=readSource&versions=all','--reader-probe',str(reason_probe)],'40s',base=f'http://127.0.0.1:{server.server_port}')
                reason_rows=final_report(reason_app.pid,65)
                assert reason_rows is not None,'native reason report missing'
                assert not [r for r in reason_rows if r['status']!='pass'],json.dumps(reason_rows,ensure_ascii=False)
                assert reason_app.wait(timeout=65)==0
                if scoped:
                    assert late_started.is_set() and late_release.is_set() and late_sent.is_set(),'native in-flight real HTTP200 era fixture was not exercised'
                    assert credential.exists(),'live-screen era probe unexpectedly logged out'
                proof['cases'].append({'kind':'SC17/18 labelled seeded '+('scoped' if scoped else 'owner')+' safe reasons in actual native App through closed facade','probe_rows':[r['name'] for r in reason_rows]})
            finally:server.shutdown();server.server_close()

    if 'corpus' in STAGES:
        support_spec=importlib.util.spec_from_file_location('sc19_support',REPO/'tests/manual/search_corpus_support.py')
        support=importlib.util.module_from_spec(support_spec);support_spec.loader.exec_module(support)
        examples=support.prepare(product,owner)
        proof['corpus']=examples
        proof['corpus_support_sha256']=hashlib.sha256((REPO/'tests/manual/search_corpus_support.py').read_bytes()).hexdigest()
        for example in examples:
            corpus_probe=work/('probe-corpus-'+example['name']+'.js')
            corpus_probe.write_text(r"""
            const expected=EXAMPLE,rows=[];let done=false;
            const report=()=>window.haeryongNativeCall(701101,[{Rows:[...rows,{name:'page.done',status:done?'pass':'pending',detail:{}}]}]).catch(()=>{});
            setInterval(()=>void report(),1000);
            const wait=async fn=>{const deadline=Date.now()+25000;while(Date.now()<deadline){if(fn())return;await new Promise(r=>setTimeout(r,100));}throw Error('corpus native wait');};
            const actual=[];
            window.addEventListener('message',event=>{const frame=document.querySelector('iframe[title="결과 뷰어"]');if(event.source===frame?.contentWindow&&event.data?.type==='viewer.loaded')actual.push(event.data);});
            (async()=>{try{
              await wait(()=>document.querySelector('form[role="search"]'));
              const input=(label,value)=>{const el=[...document.querySelectorAll('label')].find(n=>n.textContent.startsWith(label))?.querySelector('input,select');if(!el)throw Error('missing '+label);Object.getOwnPropertyDescriptor(el.tagName==='INPUT'?HTMLInputElement.prototype:HTMLSelectElement.prototype,'value').set.call(el,value);el.dispatchEvent(new Event(el.tagName==='INPUT'?'input':'change',{bubbles:true}));};
              input('프로젝트',expected.project);input('검색 종류','region');input('검색어',expected.q);await new Promise(r=>setTimeout(r,100));
              document.querySelector('form[role="search"]').dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}));
              let hit;await wait(()=>hit=[...document.querySelectorAll('[data-testid="search-hit"]')].find(n=>n.textContent.includes('영역 '+expected.region+' ·')));
              const anchor=hit.querySelector('a'),url=new URL(anchor.href);
              if(url.pathname!==`/documents/${expected.document}/versions/${expected.version}`||url.searchParams.get('page')!==expected.page)throw Error('corpus search target mismatch');
              rows.push({name:'corpus.'+expected.name+'.search-target',status:'pass',detail:{document:expected.document,version:expected.version,page:expected.page}});
              anchor.click();
              await wait(()=>actual.some(identity=>identity.document_id===expected.document&&identity.version_id===expected.version&&identity.page_path===expected.page));
              const identity=actual.find(identity=>identity.document_id===expected.document&&identity.version_id===expected.version&&identity.page_path===expected.page);
              rows.push({name:'corpus.'+expected.name+'.actual-frame-identity',status:'pass',detail:identity});
            }catch(error){rows.push({name:'probe.exception',status:'fail',detail:String(error)});}done=true;await report();})();
            """.replace('EXAMPLE',json.dumps(example,ensure_ascii=False)))
            corpus_app=launch(['--reader','--reader-port','9271','--reader-path','/library','--reader-probe',str(corpus_probe)],'40s')
            corpus_rows=final_report(corpus_app.pid,65)
            assert corpus_rows and all(row['status']=='pass' for row in corpus_rows),corpus_rows
            assert corpus_app.wait(timeout=65)==0
            proof['cases'].append({'kind':'SC19 actual native '+example['name']+' checked-in corpus search/open','probe_rows':[r['name'] for r in corpus_rows],'expected':example})

    # 2b) same fixed-origin trusted-host WebLock + labelled SEED pending
    if 'seed' in STAGES:
            # journal record after a real server K write (persistence across a real
            # restart; the seed is setup, not native network loss).
            seed_probe=work/'probe-seed.js'
            seed_probe.write_text("""
        window.__pageError=null;window.onerror=(m)=>{window.__pageError=String(m)};
        window.__progress=[];window.__done=false;
        const rows=[];const log=(name,ok,detail={})=>rows.push({name,status:ok?"pass":"fail",detail});
        const call=(op,payload={})=>window.haeryongNativeCall(701100,[{Operation:op,Payload:payload}]);
        const report=async()=>{try{const r=await window.haeryongNativeCall(701101,[{Rows:[...rows,{name:"page.progress",status:"pass",detail:{steps:window.__progress}},{name:"page.error",status:window.__pageError?"fail":"pass",detail:{error:window.__pageError}},{name:"page.done",status:window.__done?"pass":"pending",detail:{}}]}]);return !!(r&&r.written===true);}catch(e){return false;}};
        setInterval(()=>{void report();},2000);
        (async()=>{try{
          const acquired=await window.navigator.locks.request("haeryong-native-host-probe",()=>Promise.resolve("acquired")).then(v=>v==="acquired").catch(()=>false);
          log("weblock.acquired-same-origin",acquired,{});
          // The shared shell attaches asynchronously: the verified wait must
          // precede the metadata calls or they race NATIVE_SESSION_NOT_ATTACHED.
          const ready=await new Promise((resolve)=>{const deadline=Date.now()+30000;const check=()=>{const el=document.querySelector('.service-state[data-state="verified"]');if(el)resolve(true);else if(Date.now()>deadline)resolve(false);else setTimeout(check,250);};check();});
          log("shell.verified-before-seed",ready,{});
          const caps=await call("capabilities");const session=await call("currentSession");
          if(!caps.ok||!session.ok)throw Error("bridge metadata unavailable: "+JSON.stringify({caps:caps.error||null,session:session.error||null}));
          const owner=JSON.stringify([caps.value.library_id,session.value.principal_id,session.value.session_id]);
          const version=location.pathname.split("/versions/")[1]||"";
          // Read the REAL current base; both the wire precondition and the
          // journal's INTERNAL camelCase conditions are constructed from that
          // same captured base — nothing invented.
          const get=await call("personalGet",{kind:"reading-position",version_id:version});
          if(!get.ok)throw Error("personalGet unavailable");
          const base=get.value;
          const body={page_path:"first.html",page_query:"",page_fragment:"",opened:true};
          const wire=base.exists?{if_match:base.etag}:{create:true};
          const internal=base.exists?{ifMatch:base.etag}:{create:true};
          const key=self.crypto.randomUUID();
          const put=await call("personalPut",{kind:"reading-position",version_id:version,page:null,body:body,precondition:wire,operation_id:key});
          log("seed.real-server-K-write",put.ok,put.error||{key:key,baseExists:base.exists});
          // The labelled SEED stores the EXACT dispatched request: real base,
          // real internal conditions, the immutable body and the real K.
          const target={version:version,kind:"reading-position",page:null};
          const entry={target:target,pending:{target:target,key:key,intent:body,base:base,conditions:internal,phase:"write"}};
          localStorage.setItem("haeryong.reader.personal.v1:"+owner,JSON.stringify({version:1,entries:[entry]}));
          const readBack=JSON.parse(localStorage.getItem("haeryong.reader.personal.v1:"+owner));
          log("seed.bucket-written",!!readBack&&readBack.entries.length===1&&readBack.entries[0].pending.key===key&&JSON.stringify(readBack.entries[0].pending.conditions)===JSON.stringify(internal)&&readBack.entries[0].pending.base.etag===(base.exists?base.etag:null),{});
        }catch(error){log("probe.exception",false,String(error));}
        window.__done=true;let reported=false;for(let i=0;i<3&&!reported;i++)reported=await report();log("report.written",reported,{});})();
        """)
            seed_app_path=app_path
            app2=launch(['--reader','--reader-port','9271','--reader-path',seed_app_path,'--reader-probe',str(seed_probe)],'40s')
            seed_rows=final_report(app2.pid,60)
            assert seed_rows is not None,'seed probe final report never appeared'
            seed_failed=[r for r in seed_rows if r['status']!='pass' and r['name'] not in ('page.done',)]
            assert not seed_failed,json.dumps(seed_failed,ensure_ascii=False)[:1200]
            assert app2.wait(timeout=90)==0
            seed_receipts=receipts(app2.pid)
            seeded_key=next((r['detail'].get('key') for r in seed_rows if r['name']=='seed.real-server-K-write' and r['detail'].get('key')),None)
            assert seeded_key,('seed probe never reported its dispatched K',None)
            # The seed run also contains the app's own pointer save: match the
            # receipt of the PROBE-dispatched K, not merely the first PUT.
            seed_put=next((r for r in seed_receipts if r['event']=='personalPut' and r.get('operation_prefix')==seeded_key[:8]),None)
            assert seed_put is not None,('seed K receipt not found',seeded_key,[(r.get('operation_prefix')) for r in seed_receipts if r['event']=='personalPut'])
            seeded_prefix=seed_put.get('operation_prefix')
            proof['cases'].append({'kind':'same fixed-origin WebLock acquired; labelled SEED pending record (exact internal conditions/base from the real captured base) written after a real server K write','seeded_prefix':seeded_prefix,'seed_condition':seed_put.get('condition'),'seed_body_sha256':seed_put.get('body_sha256')})


    # 3) restart: bucket read first, WebLock re-acquired, actual UI replays
    if 'restart' in STAGES:
            # the seeded original K; remembered credential at the same fixed origin.
            restart_probe=work/'probe-restart.js'
            restart_probe.write_text("""
        window.__pageError=null;window.onerror=(m)=>{window.__pageError=String(m)};
        window.__firstBucketRead=null;
        window.__progress=[];window.__done=false;
        const rows=[];const log=(name,ok,detail={})=>rows.push({name,status:ok?"pass":"fail",detail});
        const report=async()=>{try{const r=await window.haeryongNativeCall(701101,[{Rows:[...rows,{name:"page.progress",status:"pass",detail:{steps:window.__progress}},{name:"page.error",status:window.__pageError?"fail":"pass",detail:{error:window.__pageError}},{name:"page.done",status:window.__done?"pass":"pending",detail:{}}]}]);return !!(r&&r.written===true);}catch(e){return false;}};
        setInterval(()=>{void report();},2000);
        (async()=>{try{
          const keys=Object.keys(localStorage).filter(k=>k.startsWith("haeryong.reader.personal.v1:"));
          window.__firstBucketRead=keys.map(k=>[k,localStorage.getItem(k)]);
          log("bucket.first-read-recorded",Array.isArray(window.__firstBucketRead),{buckets:window.__firstBucketRead.length});
          const acquired=await window.navigator.locks.request("haeryong-native-host-restart-probe",()=>Promise.resolve("acquired")).then(v=>v==="acquired").catch(()=>false);
          log("weblock.acquired-after-restart",acquired,{});
        }catch(error){log("probe.exception",false,String(error));}
        window.__done=true;let reported=false;for(let i=0;i<3&&!reported;i++)reported=await report();log("report.written",reported,{});})();
        """)
            restart=launch(['--reader','--reader-port','9271','--reader-path',app_path,'--reader-probe',str(restart_probe)],'40s')
            restarted=final_report(restart.pid,60)
            assert restarted is not None,'restart probe final report never appeared'
            restart_failed=[r for r in restarted if r['status']!='pass' and r['name'] not in ('page.done',)]
            assert not restart_failed,json.dumps(restart_failed,ensure_ascii=False)[:1200]
            assert restart.wait(timeout=90)==0
            restart_receipts=receipts(restart.pid)
            replayed=[r for r in restart_receipts if r['event']=='personalPut' and r.get('operation_prefix')==seeded_prefix and r.get('body_sha256')==seed_put.get('body_sha256') and r.get('condition')==seed_put.get('condition')]
            assert replayed,('restart did not replay the seeded original K/body/preconditions through the actual UI',seeded_prefix,[(r.get('operation_prefix'),r.get('condition')) for r in restart_receipts if r['event']=='personalPut'])
            guard2=json.loads((state/'native-guard.json').read_text())
            assert guard2.get('installed') in (1, True) and guard2.get('pid')==restart.pid,'guard origin changed across restart'
            proof['cases'].append({'kind':'restart: bucket read first, WebLock re-acquired, actual UI replayed the seeded original K at the same fixed origin','seeded_prefix':seeded_prefix,'pid':restart.pid})


    # 3b) TASK-003-04 evidence presentation through the ACTUAL compiled App:
    # supplied metadata and authored/service separation against the real
    # daemon, the implemented evidence/history reads through the closed facade,
    # then a second run of the same real App+facade against an explicitly
    # labelled local seeded fixture backend for verification-history and
    # assessment/coverage presentation only (no real source-read claim).
    if 'evidence' in STAGES:
            meta_source=work/'evidence-source';(meta_source/'nested').mkdir(parents=True)
            (meta_source/'first.html').write_text('<!doctype html><meta charset=utf-8><title>Evidence first</title><h1>근거 첫 페이지</h1><button id="increment" onclick="this.textContent=String(Number(this.textContent)+1)">0</button><div style="height:1200px"></div>')
            (meta_source/'nested/second.html').write_text('<!doctype html><meta charset=utf-8><title>Evidence second</title><h1>둘째 페이지</h1>')
            rev='0123456789abcdef0123456789abcdef01234567'
            meta={'version':{'source_intent':'historical'},
                  'sources':[{'id':'repo-main','kind':'git','retention_mode':'reference_only','repo_id':'11111111-2222-4333-8444-555555555555','object_format':'sha1','exact_revision':rev,'release_label':'v3 예시'}],
                  'evidence':[{'id':'constructor','source_id':'repo-main','capture_status':'reference_only','path':'notes/first.txt','line_start':1,'line_end':4},
                              {'id':'evidence-둘째','source_id':'repo-main','capture_status':'verified','path':'notes/second.txt'}],
                  'regions':[{'id':'overview','page_path':'first.html','label':'첫 페이지 개요','evidence_ids':['constructor']},
                             {'id':'r2','page_path':'first.html','label':'두번째 영역','evidence_ids':[]},
                             {'id':'r3','page_path':'first.html','label':'세번째 영역','evidence_ids':[]},
                             {'id':'r4','page_path':'first.html','label':'네번째 영역','evidence_ids':[]},
                             {'id':'overview','page_path':'nested/second.html','label':'둘째 개요','evidence_ids':['evidence-둘째']}]}
            meta_path=work/'evidence-metadata.json';meta_path.write_text(json.dumps(meta,ensure_ascii=False))
            _,_,ev=product.import_tree(owner,meta_source,extra=['--metadata',str(meta_path)])
            ev_version=ev['version_id'];ev_document=ev['document_id']
            ev_route=f'/documents/{ev_document}/versions/{ev_version}?page=first.html'
            probe=work/'probe-evidence.js'
            probe.write_text("""
        window.__pageError=null;
        window.onerror=(m,s,l,c)=>{window.__pageError=String(m)+"@"+l+":"+c;};
        window.addEventListener('unhandledrejection',e=>{window.__pageError=String(e.reason&&(e.reason.stack||e.reason.message||e.reason));});
        window.__progress=[];window.__done=false;
        const rows=[];const log=(name,ok,detail={})=>rows.push({name,status:ok?"pass":"fail",detail});
        const wait=async(predicate,timeout)=>{const deadline=Date.now()+timeout;while(Date.now()<deadline){let value;try{value=predicate();}catch(e){window.__pageError=String(e);throw e;}if(value)return value;await new Promise(r=>setTimeout(r,250));}return null;};
        const text=()=>document.body.textContent||"";
        const report=async()=>{try{const r=await window.haeryongNativeCall(701101,[{Rows:[...rows,{name:"page.progress",status:"pass",detail:{steps:window.__progress}},{name:"page.error",status:window.__pageError?"fail":"pass",detail:{error:window.__pageError}},{name:"page.done",status:window.__done?"pass":"pending",detail:{}}]}]);return !!(r&&r.written===true);}catch(e){return false;}};
        setInterval(()=>{void report();},2000);
        try{
          const identity=await wait(()=>window.__haeryongNativeTransport==="HAEYONG_NATIVE_TRANSPORT_BOUND",30000);
          log("transport.identity",!!identity,{value:window.__haeryongNativeTransport});
          const verified=await wait(()=>document.querySelector('.service-state[data-state=\\"verified\\"]'),30000);
          log("shell.verified",!!verified,{});
          await wait(()=>document.querySelector('iframe[title=\\"결과 뷰어\\"]'),30000);
          await wait(()=>!document.body.textContent.includes("페이지를 여는 중…"),30000);
          const open=()=>{const b=[...document.querySelectorAll('button')].find(b=>b.textContent==='근거 패널');if(!b)return null;b.click();return true;};
          log("panel.opened-by-click",!!await wait(open,15000),{});
          const header=await wait(()=>(document.querySelector('section[aria-label=\\"버전 및 출처 정보\\"]')||{}).textContent,15000);
          log("version-header.exact-basis",!!header&&header.includes('증거 기록됨(정확성 보증 아님)')&&header.includes('repo-main')&&header.includes('v3 예시'),{header:String(header).slice(0,220)});
          const region=await wait(()=>text().includes('첫 페이지 개요'),15000);
          log("supplied.regions-present",!!region,{});
          const expandOnce=()=>{const b=[...document.querySelectorAll('button')].find(b=>/constructor · reference_only/.test(b.textContent));if(!b)return null;if(b.getAttribute('aria-expanded')!=='true')b.click();return b.getAttribute('aria-expanded');};
          const expanded=await wait(()=>{const v=expandOnce();return v==='true'?v:null;},15000);
          log("evidence.inherited-key-expands",expanded==='true',{});
          const detailText=()=>[...document.querySelectorAll('.evidence-detail')].map(e=>e.textContent).join('|');
          const separated=await wait(()=>{const d=detailText();return d.includes('작성자 선언(불변): reference_only(작성자 선언)')&&d.includes('사유 source_not_registered')?true:null;},15000);
          log("evidence.authored-service-separation",!!separated,{detail:detailText().slice(0,400),pageError:window.__pageError});
          const vhist=()=>{const b=[...document.querySelectorAll('button')].find(b=>b.textContent==='검증 이력 보기/숨기기');if(!b)return null;b.click();return true;};
          await wait(vhist,15000);
          const empty=await wait(()=>[...document.querySelectorAll('.evidence-detail p')].some(p=>p.textContent==='기록 없음.'),15000);
          log("verifications.real-empty",!!empty,{});
          const real=await wait(()=>document.querySelector('.assessment-block')?.textContent.includes('기록된 배지:'),15000);
          log("assessments.real",!!real,{});
          const check=[...document.querySelectorAll('.evidence-region .evidence-detail button')].find(b=>b.textContent==='출처 근거 확인');
          log("source-check.enabled",!!check&&!check.disabled,{});
          if(check)check.click();
          const outcome=await wait(()=>detailText().includes('확인 결과: 사용 불가')&&detailText().includes('source_not_registered'),15000);
          const history=await wait(()=>document.querySelector('.evidence-region .evidence-detail li'),15000);
          log("source-check.real-outcome-and-history",!!outcome&&!!history,{});
          log("no-claim.disclaimer",text().includes('서비스 확인은 작성자 선언을 승격하지 않으며'),{});
        }catch(error){log("probe.exception",false,String(error));}
        window.__done=true;
        let reported=false;
        for(let i=0;i<3&&!reported;i++)reported=await report();
        log("report.written",reported,{});
        await report();
        """)
            real_app=launch(['--reader','--reader-port','9271','--reader-path',ev_route,'--reader-probe',str(probe)],'150s')
            rows=final_report(real_app.pid,140)
            assert rows is not None,'evidence probe final report never appeared (real daemon run)'
            failed=[r for r in rows if r['status']!='pass' and r['name'] not in ('page.done',)]
            assert not failed,json.dumps(failed,ensure_ascii=False)[:1500]
            assert real_app.wait(timeout=180)==0
            proof['cases'].append({'kind':'TASK-003-04 real-daemon native App: supplied regions/evidence through the closed facade, authored/service separation, real verification+assessment reads, enabled source-check with durable unavailable outcome/history','probe_rows':[r['name'] for r in rows]})

            # Labelled local seeded fixture backend: forwards every request to
            # the real daemon; ONLY the three future-capability routes serve
            # seeded 200 envelopes so the actual App+facade can present
            # verification history and assessment coverage. Presentation
            # proof only — no real source read or TASK-004-01 substitution.
            import threading,socket
            from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
            upstream=product.endpoint
            units_page1=[{'kind':'region','page_path':'first.html','region_id':'overview','outcome':'assessed','reason':None,'discrepancy':None,'assessment_id':'asm-1','target_set_id':'t1'},
                         {'kind':'region','page_path':'first.html','region_id':'r2','outcome':'assessed','reason':None,'discrepancy':True,'assessment_id':'asm-1','target_set_id':'t1'},
                         {'kind':'region','page_path':'first.html','region_id':'r3','outcome':'partial','reason':'일부만 확인','discrepancy':None,'assessment_id':'asm-1','target_set_id':'t1'}]
            units_page2=[{'kind':'region','page_path':'first.html','region_id':'r4','outcome':'blocked','reason':'읽기 거부','discrepancy':None,'assessment_id':'asm-1','target_set_id':'t1'}]
            applicable={'source_intent':'historical','target_set_id':'t1','captured_at':'2026-10-19T00:00:00Z','scope':{'kind':'page','page_path':'first.html','region_id':None},'status':'aligned_within_scope','coverage':{'coverage_set_id':'c1','unit_kind':'region','eligible':4,'assessed':4,'complete':True,'units_url':f'/api/v1/versions/{ev_version}/assessments/coverage?coverage_set_id=c1&attempt_run_id=11111111-2222-4333-8444-555555555555'},'assessed_at':'2026-10-19T01:00:00Z','reassessment_required':False}
            seeded_assessments={'items':[{'assessment_id':'asm-1','version_id':ev_version,'scope':{'kind':'page','page_path':'first.html','region_id':None},'target_set_id':'t1','state':'published'}],'next_cursor':None,'applicable':applicable}
            seeded_verifications={'items':[{'verification_id':'ver-1','outcome':'matched','checked_at':'2026-10-19T02:00:00Z','verified_at':'2026-10-19T02:00:00Z','reason':None},{'verification_id':'ver-2','outcome':'captured','checked_at':'2026-10-19T03:00:00Z','verified_at':'2026-10-19T03:00:00Z','reason':None}],'next_cursor':None}
            coverage_attempts=[]
            class Seeded(BaseHTTPRequestHandler):
                def log_message(self,*a): pass
                def _serve_seeded(self):
                    """Only the declared future GET query routes serve labelled
                    seeded responses; everything else forwards untouched."""
                    path=self.path
                    if self.command!='GET':return False
                    def envelope(value):
                        body=json.dumps({'data':value},ensure_ascii=False).encode()
                        self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
                    if path.startswith(f'/api/v1/versions/{ev_version}/assessments/coverage'):
                        params=urllib.parse.parse_qs(urllib.parse.urlsplit(path).query)
                        assert params.get('attempt_run_id')==['11111111-2222-4333-8444-555555555555'],'native attempt locator lost'
                        cursor=params.get('cursor',[''])[0];coverage_attempts.append(cursor)
                        if cursor=='u2' and coverage_attempts.count('u2')==1:
                            body=json.dumps({'error':{'code':'SERVICE_RECOVERING','retryable':True,'details':{}}}).encode();self.send_response(503);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body);return True
                        envelope({'items':units_page1,'next_cursor':'u2'} if cursor!='u2' else {'items':units_page2,'next_cursor':None});return True
                    if path.startswith(f'/api/v1/versions/{ev_version}/assessments'):
                        envelope(seeded_assessments);return True
                    parsed=urllib.parse.urlsplit(path)
                    query=urllib.parse.parse_qs(parsed.query)
                    if parsed.path==f'/api/v1/versions/{ev_version}/source-evidence-verifications' and query.get('evidence_id')==['constructor']:
                        envelope(seeded_verifications);return True
                    return False
                def _forward(self):
                    length=int(self.headers.get('Content-Length') or 0)
                    body=self.rfile.read(length) if length else None
                    request=urllib.request.Request(upstream+self.path,data=body,headers={k:v for k,v in self.headers.items() if k.lower() not in ('host','content-length')},method=self.command)
                    try:
                        with urllib.request.urlopen(request,timeout=60) as response:
                            data=response.read();status=response.status;headers=[(k,v) for k,v in response.getheaders() if k.lower() not in ('transfer-encoding','content-length','connection')]
                    except urllib.error.HTTPError as error:
                        data=error.read();status=error.code;headers=[(k,v) for k,v in error.headers.items() if k.lower() not in ('transfer-encoding','content-length','connection')]
                    self.send_response(status)
                    for key,value in headers:self.send_header(key,value)
                    self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data)
                def do_GET(self):
                    if not self._serve_seeded():self._forward()
                def do_POST(self):self._forward()
                def do_PUT(self):self._forward()
                def do_DELETE(self):self._forward()
                def do_HEAD(self):self._forward()
            seeded_port=socket.socket();seeded_port.bind(('127.0.0.1',0));seeded_port_number=seeded_port.getsockname()[1];seeded_port.close()
            seeded_server=ThreadingHTTPServer(('127.0.0.1',seeded_port_number),Seeded)
            seeded_thread=threading.Thread(target=seeded_server.serve_forever,daemon=True);seeded_thread.start()
            try:
                seeded_probe=work/'probe-evidence-seeded.js'
                seeded_probe.write_text("""
        window.__pageError=null;window.onerror=(m)=>{window.__pageError=String(m)};
        window.__progress=[];window.__done=false;
        const rows=[];const log=(name,ok,detail={})=>rows.push({name,status:ok?"pass":"fail",detail});
        const wait=async(predicate,timeout)=>{const deadline=Date.now()+timeout;while(Date.now()<deadline){let value;try{value=predicate();}catch(e){window.__pageError=String(e);throw e;}if(value)return value;await new Promise(r=>setTimeout(r,250));}return null;};
        const text=()=>document.body.textContent||"";
        const report=async()=>{try{const r=await window.haeryongNativeCall(701101,[{Rows:[...rows,{name:"page.progress",status:"pass",detail:{steps:window.__progress}},{name:"page.error",status:window.__pageError?"fail":"pass",detail:{error:window.__pageError}},{name:"page.done",status:window.__done?"pass":"pending",detail:{}}]}]);return !!(r&&r.written===true);}catch(e){return false;}};
        setInterval(()=>{void report();},2000);
        try{
          await wait(()=>window.__haeryongNativeTransport==="HAEYONG_NATIVE_TRANSPORT_BOUND",30000);
          await wait(()=>document.querySelector('.service-state[data-state=\\"verified\\"]'),30000);
          const frame=await wait(()=>document.querySelector('iframe[title=\\"결과 뷰어\\"]'),30000);
          log("seeded.viewer-frame-mounted",!!frame,{});
          await wait(()=>!document.body.textContent.includes("페이지를 여는 중…"),30000);
          const failure=[...document.querySelectorAll('.banner[role="alert"]')].map(b=>b.textContent).join('|');
          log("seeded.viewer-loaded-no-failure",!failure.includes('페이지 연결을 확인하지 못했습니다'),{failure:failure.slice(0,200)});
          const open=()=>{const b=[...document.querySelectorAll('button')].find(b=>b.textContent==='근거 패널');if(!b)return null;b.click();return true;};
          await wait(open,15000);
          const badge=await wait(()=>{const block=[...document.querySelectorAll('.assessment-block')].map(e=>e.textContent).join('');return block.includes('기록된 배지')&&block.includes('범위 내 정렬')?true:null;},20000);
          log("seeded.overlay-badge-shown",!!badge,{});
          const coverageRetry=await wait(()=>[...document.querySelectorAll('button')].find(b=>b.textContent==='커버리지 다시 불러오기'),20000);
          log("T5.native-coverage-retry",!!coverageRetry,{});coverageRetry?.click();
          const derived=await wait(()=>{const block=[...document.querySelectorAll('.assessment-block')].map(e=>e.textContent).join('');return block.includes('평가됨 2/4')&&block.includes('대비 구식')?true:null;},20000);
          log("seeded.derived-counts-status",!!derived,{});
          const conflict=await wait(()=>text().includes('기록된 배지가 단위 파생 결과와 충돌합니다'),20000);
          log("seeded.overlay-conflict-surfaced",!!conflict,{});
          const expandOnce=()=>{const b=[...document.querySelectorAll('button')].find(b=>/constructor · reference_only/.test(b.textContent));if(!b)return null;if(b.getAttribute('aria-expanded')!=='true')b.click();return b.getAttribute('aria-expanded');};
          await wait(()=>{const v=expandOnce();return v==='true'?v:null;},15000);
          const detailText=()=>[...document.querySelectorAll('.evidence-detail')].map(e=>e.textContent).join('|');
          const vhist=()=>{const b=[...document.querySelectorAll('button')].find(b=>b.textContent==='검증 이력 보기/숨기기');if(!b)return null;b.click();return true;};
          await wait(vhist,15000);
          const records=await wait(()=>{const d=detailText();return d.includes('일치(기대 비교 — 사후 검증)')&&d.includes('캡처됨(기대 비교 없음')&&d.includes('ver-1')&&d.includes('ver-2')?true:null;},20000);
          log("seeded.verification-history-labels",!!records,{detail:detailText().slice(0,400),pageError:window.__pageError});
          const authored=await wait(()=>{const d=detailText();return d.includes('작성자 선언(불변): reference_only(작성자 선언)')?true:null;},20000);
          log("seeded.authored-not-upgraded",!!authored,{});
          log("seeded.posthoc-no-input-claim",text().includes('사후 검증(posthoc)은 원본 제공 입력을 관측했다고 주장하지 않습니다'),{});
          const unitsOpen=()=>{const b=[...document.querySelectorAll('button')].find(b=>b.textContent==='단위/사유 펼치기/접기');if(!b)return null;if(b.getAttribute('aria-expanded')!=='true')b.click();return b.getAttribute('aria-expanded');};
          await wait(()=>{const v=unitsOpen();return v==='true'?v:null;},15000);
          const unitRows=await wait(()=>{const block=[...document.querySelectorAll('.assessment-block')].map(e=>e.textContent).join('');return block.includes('차단')&&block.includes('읽기 거부')&&block.includes('불일치 있음')&&block.includes('부분')?true:null;},20000);
          log("seeded.unit-reasons-visible",!!unitRows,{});
        }catch(error){log("probe.exception",false,String(error));}
        window.__done=true;
        let reported=false;
        for(let i=0;i<3&&!reported;i++)reported=await report();
        log("report.written",reported,{});
        await report();
        """)
                seeded_app=launch(['--reader','--reader-port','9271','--reader-path',ev_route,'--reader-probe',str(seeded_probe)],'150s',base=f'http://127.0.0.1:{seeded_port_number}')
                seeded_rows=final_report(seeded_app.pid,140)
                assert seeded_rows is not None,'seeded evidence probe final report never appeared'
                seeded_failed=[r for r in seeded_rows if r['status']!='pass' and r['name'] not in ('page.done',)]
                assert not seeded_failed,json.dumps(seeded_failed,ensure_ascii=False)[:1500]
                assert seeded_app.wait(timeout=180)==0
                # The ordinary render/open/close POST path must have flowed
                # THROUGH the labelled fixture untouched: real opens and 204
                # closes exist for this run, proving the proxy stayed
                # transparent for non-seeded methods.
                seeded_receipts=receipts(seeded_app.pid) or []
                seeded_events=[r['event'] for r in seeded_receipts]
                assert 'renderSessionOpen' in seeded_events,('seeded run never opened a real render session through the fixture proxy',seeded_events)
                seeded_close_rows=next((r['rows'] for r in seeded_receipts if r['event']=='windowClose'),None)
                assert seeded_close_rows and all(row.get('status')==204 for row in seeded_close_rows),('seeded run normal close rows are not explicit 204 closes',seeded_close_rows)
                assert coverage_attempts[:3]==['','u2','u2'],coverage_attempts
                proof['T5_native_attempt_locator']={'first_next_retry':coverage_attempts,'attempt_run_id':'11111111-2222-4333-8444-555555555555','evidence':'labelled supplied assessment/failed coverage page'}
                proof['cases'].append({'kind':'TASK-003-04 labelled seeded fixture backend (real App+closed facade, presentation only; transparent for all non-seeded methods with real render opens/204 closes): overlay badge vs unit-derived conflict, 2/4 fixed distinct-region denominator with discrepancy/blocked/partial reasons, posthoc/captured labels, authored capture_status never upgraded','probe_rows':[r['name'] for r in seeded_rows],'seeded_backend':f'http://127.0.0.1:{seeded_port_number}','render_close_rows':seeded_close_rows})
            finally:
                seeded_server.shutdown();seeded_server.server_close()

    reference_fixture=None
    if 'owner-records' in STAGES:
        spec=importlib.util.spec_from_file_location('owner_records_native',REPO/'spikes/viewer/owner_records_native.py')
        support=importlib.util.module_from_spec(spec);spec.loader.exec_module(support)
        proof['excluded_stages']=[{'stage':s,'status':'not_run','reason':'standalone local Task6 campaign; no broader harness qualification'} for s in sorted(ALL_STAGES)]
        support.run(REPO,work,state,product,owner,launch,proof)
    if 'versions' in STAGES:
        spec=importlib.util.spec_from_file_location('reference_native',REPO/'spikes/viewer/reference_native.py')
        support=importlib.util.module_from_spec(spec);spec.loader.exec_module(support)
        reference_fixture=support.run_versions(REPO,work,state,product,owner,launch,proof)

    if 'references' in STAGES:
        ref_spec=importlib.util.spec_from_file_location('reference_native',REPO/'spikes/viewer/reference_native.py')
        ref_support=importlib.util.module_from_spec(ref_spec);ref_spec.loader.exec_module(ref_support)
        ref_support.run(REPO,work,state,product,owner,launch,proof,fixture=reference_fixture)

    if 'reference-corpus' in STAGES:
        spec=importlib.util.spec_from_file_location('reference_corpus_native',REPO/'spikes/viewer/reference_corpus_native.py')
        support=importlib.util.module_from_spec(spec);spec.loader.exec_module(support)
        support.run(REPO,work,state,product,owner,launch,proof)

    # 4) forced crash fabricates no close receipt; lease expiry stays distinct.
    if 'crash' in STAGES:
            crash=launch(['--reader','--reader-port','9271','--reader-path',app_path],'120s')
            crashed=wait_for(lambda:(lambda rows: True if rows and any(r['event']=='renderSessionOpen' for r in rows) else None)(receipts(crash.pid)),70)
            assert crashed is not None,'crash run never opened a render session'
            crash.kill();crash.wait(timeout=10)
            crash_rows=receipts(crash.pid)
            assert not any(r['event']=='windowClose' for r in crash_rows),'forced kill fabricated a close receipt'
            healthy=launch(['--reader','--reader-port','9271','--reader-path',app_path],'20s')
            assert wait_for(lambda:(lambda rows: True if rows and any(r['event']=='attachSession' for r in rows) else None)(receipts(healthy.pid)),60) is not None
            assert healthy.wait(timeout=60)==0
            proof['cases'].append({'kind':'forced crash left no fabricated close; daemon stayed healthy for the next attach','crash_pid':crash.pid})
    assert_inputs_unchanged('observations')
    proof['daemon_alive_after_runs']=True
    proof['input_sha256']=hashlib.sha256(json.dumps(before,sort_keys=True).encode()).hexdigest()
    proof['inputs']=before
    proof['binary_sha256']=hashlib.sha256((work/'native').read_bytes()).hexdigest()
    proof['complete']=True
finally:
    proof['cleanup_errors']=[]
    for process,handle in runs:
        try:
            if process.poll() is None:process.kill();process.wait(timeout=5)
        except Exception as error:proof['cleanup_errors'].append({'pid':process.pid,'error':type(error).__name__})
        finally:handle.close()
    if product is not None and product.process is not None:
        try:product.stop()
        except Exception as error:
            proof['cleanup_errors'].append({'daemon':type(error).__name__})
            try:product.close()
            except Exception as fallback:proof['cleanup_errors'].append({'daemon_fallback':type(fallback).__name__})
    proof['daemon_closed']=product is None or product.process is None
    # Preserve source binding on failed campaigns too. A stable input inventory
    # and a normal process exit cannot promote failed/missing scenario rows.
    proof['inputs']=before
    proof['inputs_unchanged']=fingerprints()==before
    proof['input_sha256']=hashlib.sha256(json.dumps(before,sort_keys=True).encode()).hexdigest()
    proof['uncaptured_inputs']=['prepared Go module/build caches and native SDK','prepared frontend/node_modules','installed Go/Python/Node/Chrome tools']
    proof['native_processes']=[{'pid':process.pid,'exit_code':process.returncode} for process,_ in runs]
    proof['build_outputs']={str(p.relative_to(work)):hashlib.sha256(p.read_bytes()).hexdigest() for p in [work/'haeryong',work/'native',*sorted((work/'dist-native').rglob('*'))] if p.is_file()}
    proof['complete']=proof['complete'] and proof['inputs_unchanged'] and not proof['cleanup_errors']
    RECEIPT.write_text(json.dumps(proof,ensure_ascii=False,indent=2)+'\n')
    if os.environ.get('H3N_RETAIN') is None:
        shutil.rmtree(work,ignore_errors=True)
assert proof['complete'] and proof['daemon_closed']
print(json.dumps({'complete':True,'cases':len(proof['cases']),'selfcheck_rows':len(proof.get('selfcheck_rows',[])),'scope':proof['scope']}))
