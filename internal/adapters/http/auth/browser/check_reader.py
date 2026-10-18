"""Actual current shared Reader in Chrome with a real isolated test daemon and authenticated owner; native proof remains separate."""
import importlib.util,json,os,pathlib,re,subprocess,tempfile,urllib.parse,hashlib
import argparse,shutil
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--browser-cli',type=pathlib.Path,required=True)
parser.add_argument('--receipt',type=pathlib.Path,required=True)
parser.add_argument('--screenshot',type=pathlib.Path,help='Optional private rendered Reader screenshot')
args=parser.parse_args()
REPO=pathlib.Path(__file__).resolve().parents[5]
RECEIPT=args.receipt.resolve()
assert RECEIPT.parent.is_dir(), 'receipt parent must exist'
volume=pathlib.Path('/Volumes/RootKernel')
base=volume/'tmp' if os.path.ismount(volume) and os.access(volume,os.W_OK) else pathlib.Path(tempfile.gettempdir())
os.umask(0o077);work=pathlib.Path(tempfile.mkdtemp(prefix='h3r-',dir=base))
env=os.environ|{'TMPDIR':str(work),'HAERYONG_TEST_STATE_DIR':str(work/'app-state'),'PYTHONDONTWRITEBYTECODE':'1'}
os.environ['HAERYONG_TEST_STATE_DIR']=env['HAERYONG_TEST_STATE_DIR']
spec=importlib.util.spec_from_file_location('h',REPO/'tests/e2e/harness.py');h=importlib.util.module_from_spec(spec);spec.loader.exec_module(h)
cli=str(args.browser_cli.resolve(strict=True))
session=work.name;product=None;opened=False
proof={'scope':__doc__,'runtime':str(work),'complete':False,'cases':[]}
def fingerprints():
 paths=sorted([p for folder in ['cmd','internal'] for p in (REPO/folder).rglob('*.go') if not p.name.endswith('_test.go')]+[REPO/'go.mod',REPO/'go.sum']+[p for p in (REPO/'internal/webassets/dist').rglob('*') if p.is_file()])
 return {str(p.relative_to(REPO)):hashlib.sha256(p.read_bytes()).hexdigest() for p in paths}
def call(args,result=False):
 p=subprocess.run([cli,'--session='+session,*args],cwd=work,env=env,capture_output=True,text=True,timeout=90)
 if p.returncode!=0 or '### Error' in p.stdout:
  proof['browser_error'] = p.stdout.split('### Error',1)[-1].split('###',1)[0][:1800]
  raise AssertionError('Browser operation failed; see bounded private fixture receipt')
 if result:
  m=re.search(r'### Result\s*\n',p.stdout);assert m
  return json.JSONDecoder().raw_decode(p.stdout[m.end():].lstrip())[0]
def js(code):return call(['run-code',code],True)
before=fingerprints()
try:
 subprocess.run(['go','build','-tags','haeryong_test_state','-o',str(work/'haeryong'),'./cmd/haeryong'],cwd=REPO,env=env,capture_output=True,check=True,timeout=90)
 assert fingerprints()==before,'Go/embed inputs changed during isolated build; no stable-candidate proof'
 product=h.Product(work/'haeryong',work);product.initialize();product.start();owner=product.owner('Reader browser regression')
 source=work/'source';(source/'nested').mkdir(parents=True)
 first="""<!doctype html><meta charset=utf-8><title>Reader first</title><h1 id=heading>첫 페이지</h1><div id=framework></div><button id=increment onclick="this.textContent=String(Number(this.textContent)+1)">0</button><a id=next href="nested/둘째.html?scenario=2#part">next</a><a id=popup target=_blank href="nested/둘째.html?popup=1#part">popup</a><div style="height:2600px"></div><h2 id=part>끝</h2><script type=module>import {value} from './module.js';window.moduleValue=value;import('https://esm.sh/preact@10.27.2').then(({h,render})=>{render(h('strong',{},'External Preact rendered'),document.querySelector('#framework'));window.cdnFramework='pass';}).catch(()=>window.cdnFramework='unavailable');new Worker('./worker.js').onmessage=e=>window.workerValue=e.data;</script>"""
 second="""<!doctype html><meta charset=utf-8><title>Reader second</title><h1>둘째 페이지</h1><a href='../first.html'>first</a><div style='height:2600px'></div><h2 id=part>둘째 끝</h2>"""
 (source/'first.html').write_text(first);(source/'nested/둘째.html').write_text(second)
 (source/'module.js').write_text('export const value="module-running";');(source/'worker.js').write_text('postMessage("worker-running")');(source/'attachment.bin').write_bytes(bytes(range(256))*100)
 _,_,published=product.import_tree(owner,source);version=published['version_id'];document=published['document_id']
 call(['open',product.endpoint+'/health/live','--browser','chrome','--profile',str(work/'profile')]);opened=True
 js("async page=>{await page.evaluate(token=>localStorage.setItem('haeryong.owner-session.v1',token),"+json.dumps(owner['token'])+");return true;}")
 route=product.endpoint+f'/documents/{document}/versions/{version}?'+urllib.parse.urlencode({'page':'first.html','page_query':'scenario=1','page_fragment':''})
 smoke=js("""async page=>{
   const opens=[];
   page.on('request',r=>{if(r.method()==='POST'&&new URL(r.url()).pathname.endsWith('/render-sessions'))opens.push(r.url());});
   await page.goto(ROUTE);
   const frame=page.frameLocator('iframe[title="결과 뷰어"]');
   await frame.locator('#heading').waitFor({timeout:20000});
   await frame.locator('#increment').click();
   if(await frame.locator('#increment').textContent()!=='1')throw Error('author interaction failed');
   const f=page.frames().find(f=>f!==page.mainFrame()&&f.url().includes('first.html'));
   await f.waitForFunction(()=>window.moduleValue==='module-running'&&window.workerValue==='worker-running');
   await page.waitForFunction(()=>!document.body.textContent.includes('페이지를 여는 중…'));
   await f.evaluate(async()=>{await window.__haeryong.state({selection:'real Reader',text:'한글🌊'.repeat(1_800_000)});});
   await page.getByText('페이지 context를 확인했습니다.',{exact:true}).waitFor();
   await f.waitForFunction(()=>window.cdnFramework==='pass'||window.cdnFramework==='unavailable');
   const cdn=await f.evaluate(()=>window.cdnFramework);
   if(cdn!=='pass')throw Error('external Preact dependency unavailable; no CDN qualification');
   if(SCREENSHOT)await page.screenshot({path:SCREENSHOT,fullPage:true});
   const boundary=await f.evaluate(()=>{
     let dom=false,storage=false;try{parent.document.body;dom=true;}catch{}try{parent.localStorage;storage=true;}catch{}
     parent.postMessage({type:'management.delete',version_id:'forged',payload:{delete:true}},'*');
     return {hostDOMDenied:!dom,hostStorageDenied:!storage,nativeAbsent:!(window.wails||window.runtime||window.fixtureNativeCall||window.haeryongNative)};
   });
   if(!boundary.hostDOMDenied||!boundary.hostStorageDenied||!boundary.nativeAbsent)throw Error('Reader host/native boundary failed');
   const integrity=await page.evaluate(async()=>{
     const json=JSON.stringify({selection:'real Reader',text:'한글🌊'.repeat(1_800_000)}),bytes=new TextEncoder().encode(json);
     const digest=Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes)),v=>v.toString(16).padStart(2,'0')).join('');
     const records=Object.keys(localStorage).filter(k=>k.startsWith('haeryong.context-transfer.v1:')).map(k=>JSON.parse(localStorage.getItem(k)));
     const record=records.find(r=>r.digest===digest);
     if(!record||record.offset!==bytes.length||record.receipt.bytes_sha256!==digest)throw Error('large context exact byte/digest mismatch');
     return {bytes:bytes.length,digest,canonicalPage:record.binding.page_path};
   });
   const personal=async(kind,pagePath)=>page.evaluate(async({kind,pagePath})=>{
     const u=new URL(location.href),parts=u.pathname.split('/'),v=parts[4];
     const response=await fetch('/api/v1/versions/'+v+'/'+kind+(pagePath?'?page='+encodeURIComponent(pagePath):''),{headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1')},credentials:'omit',redirect:'error'});
     if(!response.ok)throw Error('personal read '+response.status);return (await response.json()).data;
   },{kind,pagePath});
   const waitPersonal=async(kind,pagePath,test)=>{for(let i=0;i<80;i++){const value=await personal(kind,pagePath);if(test(value))return value;await page.waitForTimeout(100);}throw Error('personal state did not settle: '+kind);};
   const initialOpens=opens.length;
   const initialHistory=await page.evaluate(()=>history.length);
   await frame.locator('#next').click();
   await page.frameLocator('iframe').getByRole('heading',{name:'둘째 페이지',exact:true}).waitFor();
   await page.waitForFunction(()=>new URL(location.href).searchParams.get('page')==='nested/둘째.html');
   const secondURL=page.url();
   const secondHistory=await page.evaluate(()=>history.length);
   if(opens.length!==initialOpens)throw Error('child link reopened render delivery');
   const secondParams=new URL(secondURL).searchParams;
   if(secondParams.get('page_query')!=='scenario=2'||secondParams.get('page_fragment')!=='part')throw Error('child exact query/fragment lost');
   await page.goBack();
   await page.frameLocator('iframe').locator('#heading').waitFor();
   await page.waitForFunction(()=>new URL(location.href).searchParams.get('page')==='first.html');
   if(opens.length!==initialOpens)throw Error('child Back reopened render delivery');
   await page.goForward();
   await page.frameLocator('iframe').getByRole('heading',{name:'둘째 페이지',exact:true}).waitFor();
   await page.waitForFunction(()=>new URL(location.href).searchParams.get('page')==='nested/둘째.html');
   if(opens.length!==initialOpens)throw Error('child Forward reopened render delivery');
   const pointerBeforeDefault=await waitPersonal('reading-position',null,v=>v.value.page_path==='nested/둘째.html');
   const secondFrame=page.frames().find(f=>f!==page.mainFrame());
   await secondFrame.evaluate(()=>scrollTo(0,700));
   await waitPersonal('page-position','nested/둘째.html',v=>v.exists&&Math.abs(v.value.position.scroll_y-700)<5);
   await page.getByRole('button',{name:'기본 페이지로 지정',exact:true}).click();
   await page.getByText('기본 페이지를 저장했습니다.',{exact:true}).waitFor();
   const pointerAfterDefault=await personal('reading-position');
   if(pointerAfterDefault.etag!==pointerBeforeDefault.etag)throw Error('default changed last-opened pointer');
   await page.locator('aside[aria-label="페이지 및 파일 목록"]').getByRole('link',{name:'first.html',exact:true}).click();
   await page.frameLocator('iframe').locator('#heading').waitFor();
   if(opens.length!==initialOpens+1)throw Error('host selection should open exactly one delivery');
   await waitPersonal('reading-position',null,v=>v.value.page_path==='first.html');
   const firstFrame=page.frames().find(f=>f!==page.mainFrame());
   await firstFrame.evaluate(()=>scrollTo(0,500));
   await waitPersonal('page-position','first.html',v=>v.exists&&Math.abs(v.value.position.scroll_y-500)<5);
   await page.getByRole('button',{name:'이어서 읽기',exact:true}).click();
   await page.waitForFunction(()=>!document.body.textContent.includes('페이지를 여는 중…'));
   await page.waitForTimeout(1000);
   const resumedScroll=await page.frames().find(f=>f!==page.mainFrame()).evaluate(()=>scrollY);
   if(Math.abs(resumedScroll-500)>5)throw Error('Continue did not restore saved page position: '+resumedScroll);
   const popupPromise=page.waitForEvent('popup');
   await page.frameLocator('iframe').locator('#popup').click();
   const popup=await popupPromise;
   await popup.waitForURL(u=>u.pathname.startsWith('/documents/'));
   if(popup.url().startsWith(new URL(f.url()).origin+'/'))throw Error('popup stayed on raw renderer');
   await popup.frameLocator('iframe').getByRole('heading',{name:'둘째 페이지',exact:true}).waitFor();
   const popupURL=popup.url(); await popup.close();
   await page.getByRole('button',{name:'새 버전 가져오기',exact:true}).click();
   const control=page.getByTestId('import-control'); await control.waitFor();
   await page.getByTestId('base-preview').filter({hasText:new URL(page.url()).pathname.split('/')[4]}).waitFor();
   const preselected=await control.evaluate(el=>({preselected:el.dataset.preselected,values:Array.from(el.querySelectorAll('select')).map(s=>({value:s.value,disabled:s.disabled}))}));
   if(preselected.preselected!=='true'||!preselected.values.some(s=>s.value===new URL(page.url()).pathname.split('/')[2]&&s.disabled))throw Error('Reader Import exact document not preselected');
   await page.getByRole('button',{name:'가져오기 패널 닫기',exact:true}).click();
   const baseRoute=page.url().split('?')[0];
   await page.goto(baseRoute);
   await page.frameLocator('iframe').getByRole('heading',{name:'둘째 페이지',exact:true}).waitFor();
   await page.getByRole('button',{name:'기본 페이지 설정 해제',exact:true}).click();
   await page.getByText('기본 페이지 설정을 해제했습니다.',{exact:true}).waitFor();
   await page.goto(baseRoute);
   await page.getByText('페이지를 선택하거나 원본 파일을 내려받으세요.',{exact:true}).waitFor();
   if(await page.locator('iframe').count()!==0)throw Error('multiple pages without default should show Files/Pages');
   return {url:page.url(),frame:null,cdn,boundary,integrity,resumedScroll,preselected,defaultPrecedence:true,clearDefaultFilesPages:true,interactive:true,module:true,worker:true,context:true,largeContext:true,childHistoryDelta:secondHistory-initialHistory,childNavigationNoNewDelivery:true,hostNewDelivery:true,defaultSaved:true,popupURL,iframeCount:await page.locator('iframe').count(),hostNative:await page.evaluate(()=>!!(window.wails||window.haeryongNative))};
 }""".replace('ROUTE',json.dumps(route)).replace('SCREENSHOT',json.dumps(str(args.screenshot.resolve()) if args.screenshot else None)))
 proof['cases'].append({'kind':'actual Reader first page / ordinary JS / module / worker / context', 'actual':smoke})
 assert smoke['childHistoryDelta']==1 and smoke['largeContext'] and smoke['interactive'] and smoke['context'] and smoke['iframeCount']==0 and not smoke['hostNative']
 proof['browser']=js('async page=>await page.evaluate(()=>navigator.userAgent)')
 assert fingerprints()==before,'Go/embed inputs changed during observations; do not attribute to stable candidate'
 proof['input_sha256']=hashlib.sha256(json.dumps(before,sort_keys=True).encode()).hexdigest()
 proof['complete']=True
finally:
 if opened:call(['close']);proof['browser_closed']=True
 if product is not None:
  if product.process is not None:product.stop()
  proof['daemon_closed']=product.process is None
 RECEIPT.write_text(json.dumps(proof,ensure_ascii=False,indent=2)+'\n')
assert proof['complete'] and proof['browser_closed'] and proof['daemon_closed']
shutil.rmtree(work)
print(json.dumps({'complete':True,'cases':len(proof['cases']),'scope':proof['scope'],'browser_closed':True,'daemon_closed':True}))
