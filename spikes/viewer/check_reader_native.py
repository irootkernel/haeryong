"""Actual native ReaderMode regression: reused pinned beta28 harness, typed ReaderService, fixed static origin, real tagged production daemon/auth/import/storage."""
import argparse,importlib.util,json,os,pathlib,shutil,subprocess,tempfile,time,urllib.parse,urllib.error,urllib.request,hashlib,signal,sys
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--receipt',type=pathlib.Path,required=True)
parser.add_argument('--stages',default='selfcheck,app,seed,restart,crash,evidence')
args=parser.parse_args()
STAGES=set(args.stages.split(','))
ALL_STAGES={'selfcheck','app','seed','restart','crash','evidence'}
assert sys.platform=='darwin','this check requires the existing native macOS Wails harness'
assert STAGES and STAGES<=ALL_STAGES,STAGES
assert not ('restart' in STAGES and 'seed' not in STAGES),'restart requires the seed stage'
assert not (STAGES&{'seed','restart','crash','evidence'} and 'app' not in STAGES),'these stages require app setup'
REPO=pathlib.Path(__file__).resolve().parents[2]
RECEIPT=args.receipt.resolve()
assert RECEIPT.parent.is_dir(),'receipt parent must exist'
volume=pathlib.Path('/Volumes/RootKernel')
scratch=volume/'tmp' if os.path.ismount(volume) and os.access(volume,os.W_OK) else pathlib.Path(tempfile.gettempdir())
os.umask(0o077)
work=pathlib.Path(tempfile.mkdtemp(prefix='h3n-',dir=scratch))
print(json.dumps({'runtime':str(work)}),flush=True)
env=os.environ|{'TMPDIR':str(work),'HAERYONG_TEST_STATE_DIR':str(work/'app-state'),'PYTHONDONTWRITEBYTECODE':'1'}
os.environ['HAERYONG_TEST_STATE_DIR']=env['HAERYONG_TEST_STATE_DIR']
spec=importlib.util.spec_from_file_location('h',REPO/'tests/e2e/harness.py');h=importlib.util.module_from_spec(spec);spec.loader.exec_module(h)
proof={'scope':__doc__,'runtime':str(work),'complete':False,'selected_stages':sorted(STAGES),'full_qualification':STAGES==ALL_STAGES,'driver_sha256':hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),'cases':[]}
state=work/'state';state.mkdir(mode=0o700)
def fingerprints():
    paths=sorted([p for folder in ['cmd','internal'] for p in (REPO/folder).rglob('*.go') if not p.name.endswith('_test.go')]+[REPO/'go.mod',REPO/'go.sum']+[p for p in (REPO/'internal/webassets/dist').rglob('*') if p.is_file()]
                 +[REPO/'spikes/viewer/desktop/main.go',REPO/'spikes/viewer/desktop/reader.go',REPO/'spikes/viewer/desktop/guard_darwin.go',REPO/'spikes/viewer/desktop/guard_darwin.m',REPO/'spikes/viewer/desktop/go.mod',REPO/'spikes/viewer/desktop/go.sum']
                 +[REPO/'frontend/native.html',REPO/'frontend/vite.native.config.ts',REPO/'frontend/src/native-main.tsx',REPO/'frontend/src/client/native-bridge.ts',REPO/'frontend/src/client/native-wire.ts',REPO/'frontend/src/client/native.ts'])
    return {str(p.relative_to(REPO)):hashlib.sha256(p.read_bytes()).hexdigest() for p in paths}
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
    process=subprocess.Popen([str(work/'native'),*args,'--base',base or product.endpoint,'--state',str(state),'--reader-dist',str(work/'dist-native'),'--close-after',close_after],stdout=handle,stderr=handle,env=env)
    runs.append((process,handle))
    return process
before=fingerprints()
try:
    subprocess.run(['go','build','-tags','haeryong_test_state','-o',str(work/'haeryong'),'./cmd/haeryong'],cwd=REPO,env=env,capture_output=True,check=True,timeout=180)
    subprocess.run(['go','-C','spikes/viewer/desktop','build','-o',str(work/'native'),'.'],cwd=REPO,env=env,capture_output=True,check=True,timeout=240)
    subprocess.run([str(REPO/'frontend/node_modules/.bin/vite'),'build','--config','vite.native.config.ts','--outDir',str(work/'dist-native'),'--emptyOutDir'],cwd=REPO/'frontend',env=env,capture_output=True,check=True,timeout=240)
    assert fingerprints()==before,'inputs changed during isolated build; no stable-candidate proof'
    product=h.Product(work/'haeryong',work);product.initialize();product.start();owner=product.owner('native reader mode')
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
          const caps=await call("capabilities");const session=await call("currentSession");
          if(!caps.ok||!session.ok)throw Error("bridge metadata unavailable");
          const owner=JSON.stringify([caps.value.library_id,session.value.principal_id,session.value.session_id]);
          const version=location.pathname.split("/versions/")[1]||"";
          // Wait for the verified shell first, then read the REAL current
          // base; both the wire precondition and the journal's INTERNAL
          // camelCase conditions are constructed from that same captured
          // base — nothing invented.
          const ready=await new Promise((resolve)=>{const deadline=Date.now()+30000;const check=()=>{const el=document.querySelector('.service-state[data-state="verified"]');if(el)resolve(true);else if(Date.now()>deadline)resolve(false);else setTimeout(check,250);};check();});
          log("shell.verified-before-seed",ready,{});
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
    # daemon, the honest 503 future capabilities through the closed facade,
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
          const separated=await wait(()=>{const d=detailText();return d.includes('작성자 선언(불변): reference_only(작성자 선언)')&&d.includes('사유 source_read_not_implemented')?true:null;},15000);
          log("evidence.authored-service-separation",!!separated,{detail:detailText().slice(0,400),pageError:window.__pageError});
          const vhist=()=>{const b=[...document.querySelectorAll('button')].find(b=>b.textContent==='검증 이력 보기/숨기기');if(!b)return null;b.click();return true;};
          await wait(vhist,15000);
          const v503=await wait(()=>[...document.querySelectorAll('.evidence-detail p')].some(p=>p.textContent==='서비스가 아직 이 기능을 제공하지 않습니다(503 FEATURE_NOT_READY).'),15000);
          log("verifications.honest-503",!!v503,{});
          const a503=await wait(()=>[...document.querySelectorAll('.assessment-block > p')].some(p=>p.textContent==='서비스가 아직 이 기능을 제공하지 않습니다(503 FEATURE_NOT_READY).'),15000);
          log("assessments.honest-503",!!a503,{});
          const check=[...document.querySelectorAll('button')].find(b=>b.textContent==='출처 근거 확인(제공되지 않음)');
          log("source-check.disabled",!!check&&check.disabled,{});
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
            proof['cases'].append({'kind':'TASK-003-04 real-daemon native App: supplied regions/evidence through the closed facade, authored/service separation, honest 503 verifications+assessments, disabled source-check','probe_rows':[r['name'] for r in rows]})

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
            applicable={'source_intent':'historical','target_set_id':'t1','captured_at':'2026-10-19T00:00:00Z','scope':{'kind':'page','page_path':'first.html','region_id':None},'status':'aligned_within_scope','coverage':{'coverage_set_id':'c1','unit_kind':'region','eligible':4,'assessed':4,'complete':True,'units_url':f'/api/v1/versions/{ev_version}/assessments/coverage?coverage_set_id=c1'},'assessed_at':'2026-10-19T01:00:00Z','reassessment_required':False}
            seeded_assessments={'items':[{'assessment_id':'asm-1','version_id':ev_version,'scope':{'kind':'page','page_path':'first.html','region_id':None},'target_set_id':'t1','state':'published'}],'next_cursor':None,'applicable':applicable}
            seeded_verifications={'items':[{'verification_id':'ver-1','outcome':'matched','checked_at':'2026-10-19T02:00:00Z','verified_at':'2026-10-19T02:00:00Z','reason':None},{'verification_id':'ver-2','outcome':'captured','checked_at':'2026-10-19T03:00:00Z','verified_at':'2026-10-19T03:00:00Z','reason':None}],'next_cursor':None}
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
                        cursor=urllib.parse.parse_qs(urllib.parse.urlsplit(path).query).get('cursor',[''])[0]
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
                proof['cases'].append({'kind':'TASK-003-04 labelled seeded fixture backend (real App+closed facade, presentation only; transparent for all non-seeded methods with real render opens/204 closes): overlay badge vs unit-derived conflict, 2/4 fixed distinct-region denominator with discrepancy/blocked/partial reasons, posthoc/captured labels, authored capture_status never upgraded','probe_rows':[r['name'] for r in seeded_rows],'seeded_backend':f'http://127.0.0.1:{seeded_port_number}','render_close_rows':seeded_close_rows})
            finally:
                seeded_server.shutdown();seeded_server.server_close()

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
    assert fingerprints()==before,'inputs changed during observations'
    proof['daemon_alive_after_runs']=True
    proof['input_sha256']=hashlib.sha256(json.dumps(before,sort_keys=True).encode()).hexdigest()
    proof['binary_sha256']=hashlib.sha256((work/'native').read_bytes()).hexdigest()
    proof['complete']=True
finally:
    for process,handle in runs:
        if process.poll() is None:process.kill();process.wait(timeout=5)
        handle.close()
    if product is not None and product.process is not None:product.stop()
    proof['daemon_closed']=product is None or product.process is None
    RECEIPT.write_text(json.dumps(proof,ensure_ascii=False,indent=2)+'\n')
    if os.environ.get('H3N_RETAIN') is None:
        shutil.rmtree(work,ignore_errors=True)
assert proof['complete'] and proof['daemon_closed']
print(json.dumps({'complete':True,'cases':len(proof['cases']),'selfcheck_rows':len(proof.get('selfcheck_rows',[])),'scope':proof['scope']}))
