"""TASK-003-04 actual Reader evidence panel in Chrome: absent and supplied metadata, authored/service separation, real verification/history and assessment states, same-name page-local regions, protected source-check, keyboard and emulated touch."""
import importlib.util,json,os,pathlib,re,subprocess,tempfile,urllib.parse,urllib.error,urllib.request,hashlib,time
import argparse,shutil
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--browser-cli',type=pathlib.Path,required=True)
parser.add_argument('--receipt',type=pathlib.Path,required=True)
parser.add_argument('--screenshot',type=pathlib.Path,help='Optional private rendered evidence panel screenshot')
args=parser.parse_args()
REPO=pathlib.Path(__file__).resolve().parents[5]
RECEIPT=args.receipt.resolve()
assert RECEIPT.parent.is_dir(),'receipt parent must exist'
volume=pathlib.Path('/Volumes/RootKernel')
base=volume/'tmp' if os.path.ismount(volume) and os.access(volume,os.W_OK) else pathlib.Path(tempfile.gettempdir())
os.umask(0o077);work=pathlib.Path(tempfile.mkdtemp(prefix='h4e-',dir=RECEIPT.parent))
socket_dir=pathlib.Path(tempfile.mkdtemp(prefix='hes-',dir=base))
env=os.environ|{'TMPDIR':str(work),'HAERYONG_TEST_STATE_DIR':str(work/'app-state'),'PYTHONDONTWRITEBYTECODE':'1','PLAYWRIGHT_DAEMON_SOCKETS_DIR':str(socket_dir),'PLAYWRIGHT_DAEMON_SESSION_DIR':str(work/'registry')}
os.environ['HAERYONG_TEST_STATE_DIR']=env['HAERYONG_TEST_STATE_DIR']
spec=importlib.util.spec_from_file_location('h',REPO/'tests/e2e/harness.py');h=importlib.util.module_from_spec(spec);spec.loader.exec_module(h)
cli=str(args.browser_cli.resolve(strict=True))
session=work.name;product=None;opened=False
proof={'scope':__doc__,'runtime':str(work),'complete':False,'cases':[]}
def fingerprints():
 paths=sorted([p for folder in ['cmd','internal'] for p in (REPO/folder).rglob('*.go') if not p.name.endswith('_test.go')]+[REPO/'go.mod',REPO/'go.sum']+[p for p in (REPO/'internal/webassets/dist').rglob('*') if p.is_file()])
 return {str(p.relative_to(REPO)):hashlib.sha256(p.read_bytes()).hexdigest() for p in paths}
def call(args,result=False):
 p=subprocess.run([cli,'--session='+session,*args],cwd=work,env=env,capture_output=True,text=True,timeout=120)
 if p.returncode!=0 or '### Error' in p.stdout:
  diagnostic=p.stdout.split('### Error',1)[1].split('###',1)[0] if '### Error' in p.stdout else p.stderr or p.stdout
  for secret in [globals().get('owner',{}).get('token'),globals().get('token')]:
   if isinstance(secret,str) and secret:diagnostic=diagnostic.replace(secret,'[redacted fixture credential]')
  proof['browser_error']=diagnostic[:1800]
  proof['browser_exit_code']=p.returncode
  raise AssertionError('Browser operation failed; see bounded private fixture receipt')
 if result:
  m=re.search(r'### Result\s*\n',p.stdout);assert m
  return json.JSONDecoder().raw_decode(p.stdout[m.end():].lstrip())[0]
def js(code):return call(['run-code',code],True)
def require(name,condition,detail=None):
 if not condition:raise AssertionError('evidence check failed: '+name+' '+json.dumps(detail,ensure_ascii=False,default=str)[:600])
 proof['cases'].append({'name':name,'ok':bool(condition)})
before=fingerprints()
try:
 subprocess.run(['go','build','-tags','haeryong_test_state','-o',str(work/'haeryong'),'./cmd/haeryong'],cwd=REPO,env=env,capture_output=True,check=True,timeout=90)
 assert fingerprints()==before,'Go/embed inputs changed during isolated build; no stable-candidate proof'
 product=h.Product(work/'haeryong',work);product.initialize();product.start();owner=product.owner('Reader evidence regression')
 # Document A: ordinary directory without supplied metadata (T-003-04-01).
 plain=work/'plain';(plain/'nested').mkdir(parents=True)
 (plain/'first.html').write_text('<!doctype html><meta charset=utf-8><title>Plain</title><h1>평범한 문서</h1><button id="plain-button" onclick="this.textContent=String(Number(this.textContent)+1)">0</button><div style="height:1200px"></div>')
 (plain/'nested/second.html').write_text('<!doctype html><meta charset=utf-8><title>Plain second</title><h1>평범한 둘째</h1>')
 _,_,published_plain=product.import_tree(owner,plain)
 # Document B: same corpus shape plus exact supplied metadata, including a
 # valid inherited-key evidence id and the SAME region name on both pages.
 source=work/'source';(source/'nested').mkdir(parents=True)
 (source/'first.html').write_text('<!doctype html><meta charset=utf-8><title>Evidence first</title><h1>근거 첫 페이지</h1><button id="increment" onclick="this.textContent=String(Number(this.textContent)+1)">0</button><div style="height:1200px"></div>')
 (source/'nested/second.html').write_text('<!doctype html><meta charset=utf-8><title>Evidence second</title><h1>둘째 페이지</h1><button id="increment2" onclick="this.textContent=String(Number(this.textContent)+1)">0</button>')
 opaque_ids=[['.','notes/dot.txt'],['..','notes/dot-dot.txt'],['%2e','notes/percent-dot.txt'],['evidence/상세','notes/slash.txt'],['verifications','notes/keyword.txt']]
 metadata={
  'version':{'source_intent':'historical'},
  'sources':[{'id':'repo-main','kind':'git','retention_mode':'reference_only','repo_id':'11111111-2222-4333-8444-555555555555','object_format':'sha1','exact_revision':'0123456789abcdef0123456789abcdef01234567','release_label':'v3 예시'},
             {'id':'snap-src','kind':'snapshot','retention_mode':'reference_only','snapshot_sha256':'fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210'}],
  'evidence':[
   {'id':'constructor','source_id':'repo-main','capture_status':'reference_only','path':'notes/first.txt','line_start':1,'line_end':4},
   {'id':'evidence-둘째','source_id':'repo-main','capture_status':'verified','path':'notes/second.txt'},
   {'id':'evidence-free','source_id':'snap-src','capture_status':'reference_only','path':'notes/free.txt'}],
  'regions':[
   {'id':'overview','page_path':'first.html','label':'첫 페이지 개요','evidence_ids':['constructor']},
   {'id':'r2','page_path':'first.html','label':'두번째 영역','evidence_ids':[]},
   {'id':'r3','page_path':'first.html','label':'세번째 영역','evidence_ids':[]},
   {'id':'r4','page_path':'first.html','label':'네번째 영역','evidence_ids':[]},
   {'id':'overview','page_path':'nested/second.html','label':'둘째 개요','evidence_ids':['evidence-둘째']}]}
 metadata['evidence'].extend({'id':key,'source_id':'repo-main','capture_status':'reference_only','path':path} for key,path in opaque_ids)
 metadata['regions'][0]['evidence_ids'].extend(key for key,_ in opaque_ids)
 git_root=work/'approved-source';git_root.mkdir()
 def git(*args,input=None):
  return subprocess.check_output(['git','-C',str(git_root),*args],input=input,env=env|{'GIT_AUTHOR_NAME':'Fixture','GIT_AUTHOR_EMAIL':'fixture@invalid','GIT_COMMITTER_NAME':'Fixture','GIT_COMMITTER_EMAIL':'fixture@invalid'}).decode().strip()
 git('init','--quiet')
 blob=git('hash-object','-w','--stdin',input=b'first\nsecond\nthird\nfourth\n')
 notes_tree=git('mktree',input=('100644 blob '+blob+'\tfirst.txt\n').encode())
 tree=git('mktree',input=('040000 tree '+notes_tree+'\tnotes\n').encode())
 revision=git('commit-tree',tree,input=b'Synthetic source fixture\n')
 git('update-ref','HEAD',revision)
 metadata['sources'][0]['exact_revision']=revision
 metadata['evidence'][0]['bytes_sha256']=hashlib.sha256(b'first\nsecond\nthird\nfourth\n').hexdigest()
 capabilities=product.data('GET','/api/v1/capabilities',client=owner)
 product.cli(['sources','approve','--project',capabilities['unfiled_project_id'],'--source-id','22222222-2222-4333-8444-555555555555','--kind','git','--root-path',str(git_root),'--repo-id',metadata['sources'][0]['repo_id'],'--retention-mode','reference_only','--confirmed'],client=owner)
 metadata_path=work/'metadata.json';metadata_path.write_text(json.dumps(metadata,ensure_ascii=False))
 _,_,published=product.import_tree(owner,source,extra=['--metadata',str(metadata_path)])
 version=published['version_id'];document=published['document_id']
 # Version B of the SAME document (different supplied metadata bytes), used
 # by the F1 stale-detail regression in the seeded phase.
 metadata_b=dict(metadata);metadata_b['sources']=[dict(source) for source in metadata['sources']]
 metadata_b['sources'][0]['release_label']='v4 예시 B'
 metadata_b_path=work/'metadata-b.json';metadata_b_path.write_text(json.dumps(metadata_b,ensure_ascii=False))
 _,_,published_b=product.import_tree(owner,source,extra=['--metadata',str(metadata_b_path),'--document',document])
 version_b=published_b['version_id']
 version=published['version_id'];document=published['document_id']
 plain_version=published_plain['version_id'];plain_document=published_plain['document_id']
 detail=product.data('GET','/api/v1/versions/'+version,client=owner)
 detail_b=product.data('GET','/api/v1/versions/'+version_b,client=owner)
 require('supplied_import_recorded_basis',detail.get('source_basis')=='evidence_recorded',detail)
 call(['open',product.endpoint+'/health/live','--browser','chrome','--profile',str(work/'profile')]);opened=True
 js("async page=>{await page.evaluate(token=>localStorage.setItem('haeryong.owner-session.v1',token),"+json.dumps(owner['token'])+");return true;}")
 route=product.endpoint+f'/documents/{document}/versions/{version}?'+urllib.parse.urlencode({'page':'first.html'})
 plain_route=product.endpoint+f'/documents/{plain_document}/versions/{plain_version}?'+urllib.parse.urlencode({'page':'first.html'})
 smoke=js("""async page=>{  const open=async(url)=>{await page.goto(url);await page.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'근거 첫 페이지',exact:true}).waitFor({timeout:20000});};
  await open(ROUTE);
  const facts={};
  const header=page.locator('section[aria-label="버전 및 출처 정보"]');
  facts.versionHeader=await header.textContent();
  if(!facts.versionHeader.includes('버전 #'+SEQUENCE+' ')||!facts.versionHeader.includes('증거 기록됨(정확성 보증 아님)')||!facts.versionHeader.includes('v3 예시')||!facts.versionHeader.includes('repo-main'))throw Error('version header missing exact sequence/basis/provenance: '+facts.versionHeader);
  facts.versionIdPreserved=(await page.locator('.reader-header > div > p.muted-code').first().textContent()).includes(VERSION);
  // Keyboard activation of the evidence panel toggle.
  const toggle=page.getByRole('button',{name:'근거 패널',exact:true});
  await toggle.focus();await page.keyboard.press('Enter');
  const panel=page.locator('section[aria-label="근거 및 출처 패널"]');
  await panel.waitFor({timeout:10000});
  facts.keyboardOpenedPanel=true;
  await page.locator('section[aria-label="근거 및 출처 패널"]').getByText('첫 페이지 개요' ,{exact:true}).waitFor({timeout:10000});
  facts.pageLocalRegions=true;
  // Inherited-key evidence id expands on the FIRST keyboard activation.
  const row=panel.locator('.evidence-region').getByRole('button',{name:/constructor · reference_only/}).first();
  await row.focus();await page.keyboard.press('Enter');
  await page.getByText('작성자 선언(불변): reference_only(작성자 선언)',{exact:false}).first().waitFor({timeout:10000});
  facts.inheritedKeyFirstClickExpanded=await row.getAttribute('aria-expanded');
  facts.serviceSeparation=await panel.getByText('확인 전 — 이 화면은 새 소스 읽기를 시작하지 않습니다',{exact:false}).first().isVisible();
  // Verification history initially has no durable checks, awaited
  // INSIDE its own evidence-detail block.
  await panel.locator('.evidence-region .evidence-detail').getByRole('button',{name:'검증 이력 보기/숨기기',exact:true}).click();
  await page.waitForFunction(()=>[...document.querySelectorAll('section[aria-label="근거 및 출처 패널"] .evidence-region .evidence-detail p')].some(p=>p.textContent==='기록 없음.'),{timeout:10000});
  facts.verificationsEmpty=true;
  // These records come from the actual daemon, not the later seeded proxy.
  // Observe both the real HTTP response identity and its rendered authored path.
  const opaque=[];
  for(const [id,path] of OPAQUE_IDS){
   const item=panel.locator('.version-evidence .evidence-row').filter({has:page.getByRole('button',{name:id+' · reference_only(작성자 선언) · '+path,exact:true})});
   const [response]=await Promise.all([
    page.waitForResponse(r=>r.url().split('?')[0].endsWith('/api/v1/versions/'+VERSION+'/source-evidence')&&r.url().includes('evidence_id='+encodeURIComponent(id))),
    (async()=>{await item.getByRole('button',{name:id+' · reference_only(작성자 선언) · '+path,exact:true}).focus();await page.keyboard.press('Enter');})(),
   ]);
   const view=(await response.json()).data;
   if(response.status()!==200||view.evidence_id!==id||view.authored.id!==id||view.authored.path!==path)throw Error('opaque evidence identity changed: '+id);
   await item.getByText('작성자 선언(불변): reference_only(작성자 선언) · '+path,{exact:true}).waitFor({timeout:10000});
   await item.getByRole('button',{name:'검증 이력 보기/숨기기',exact:true}).click();
   await item.getByText('기록 없음.',{exact:true}).waitFor({timeout:10000});
   opaque.push(id);
  }
  facts.opaqueIds=opaque;
  // Assessment summary for the exact scope: the assessment block's OWN
  // implemented summary state, awaited independently of the order above.
  facts.assessmentsReal=await panel.getByText('신선도 요약(선택된 대상)',{exact:true}).isVisible();
  await panel.getByText('기록된 배지:',{exact:false}).waitFor({timeout:10000});
  facts.future503Count=await panel.getByText('서비스가 아직 이 기능을 제공하지 않습니다(503 FEATURE_NOT_READY).',{exact:true}).count();
  // An actual authorized source read, then the forced evidence/history refresh.
  const selected=panel.locator('.evidence-region .evidence-detail').first();
  const check=selected.getByRole('button',{name:'출처 근거 확인',exact:true});
  facts.sourceCheckEnabled=await check.isEnabled();
  await check.focus();await page.keyboard.press('Enter');
  await selected.getByText('확인 결과: 캡처됨',{exact:false}).waitFor({timeout:10000});
  await selected.locator('li').filter({hasText:'캡처됨'}).first().waitFor({timeout:10000});
  facts.realCapturedAndHistory=true;
  await selected.getByLabel('작성자 선언 다이제스트와 비교',{exact:true}).check();
  await check.click();
  await selected.getByText('확인 결과: 일치(기대 비교',{exact:false}).waitFor({timeout:10000});
  facts.authorDigestOptionMatched=true;
  // D1: the real SourcePanelSelection unmounts on a region change. The
  // daemon applies the POST, but both its ack and the first receipt read are
  // lost. Recovery in the newly keyed child must query the original receipt.
  let lostKey='', checkPosts=0, recovering=false;
  const receiptReads=[];
  const postPattern='**/api/v1/versions/'+VERSION+'/source-evidence-verifications?*';
  const receiptPattern='**/api/v1/operations/*';
  await page.route(postPattern,async route=>{
   if(route.request().method()!=='POST'){await route.continue();return;}
   checkPosts++;lostKey=route.request().headers()['idempotency-key'];
   await route.fetch();await route.abort('failed');
  });
  await page.route(receiptPattern,async route=>{
   receiptReads.push(route.request().url().split('/').pop());
   if(!recovering)await route.abort('failed');else await route.continue();
  });
  await check.click();
  await selected.getByRole('button',{name:'원래 작업 결과 다시 확인',exact:true}).waitFor();
  const oldPanel=await panel.elementHandle();
  await page.frames().find(f=>f!==page.mainFrame()).evaluate(()=>window.__haeryong.select('overview'));
  await page.waitForFunction(el=>!el.isConnected,oldPanel);
  await panel.locator('.evidence-region').getByRole('button',{name:/constructor · reference_only/}).first().click();
  const remountedDetail=panel.locator('.evidence-region .evidence-detail').first();
  await remountedDetail.getByRole('button',{name:'원래 작업 결과 다시 확인',exact:true}).waitFor();
  if(!await remountedDetail.getByLabel('작성자 선언 다이제스트와 비교',{exact:true}).isChecked())throw Error('remount lost the pending expectation display');
  recovering=true;
  await remountedDetail.getByRole('button',{name:'원래 작업 결과 다시 확인',exact:true}).click();
  await remountedDetail.getByText('확인 결과: 일치(기대 비교',{exact:false}).waitFor();
  if(checkPosts!==1||receiptReads.length<2||receiptReads.some(key=>key!==lostKey))throw Error('remounted verification identity '+JSON.stringify({posts:checkPosts,receipts:receiptReads,key:lostKey}));
  facts.remountedSourceCheckOriginalReceipt=true;
  await page.unroute(postPattern);await page.unroute(receiptPattern);

  // E3(a): normal success arrives AFTER the real keyed panel has remounted.
  // The new consumer must show the recorded result without another POST.
  let normalApplied,normalRelease,normalDelivered,normalKey='',normalPosts=0;
  const normalAppliedPromise=new Promise(resolve=>normalApplied=resolve);
  const normalHeld=new Promise(resolve=>normalRelease=resolve);
  const normalDeliveredPromise=new Promise(resolve=>normalDelivered=resolve);
  const normalReceipts=[];
  const normalRequest=request=>{if(request.url().includes('/operations/'))normalReceipts.push(request.url().split('/').pop());};
  page.on('request',normalRequest);
  await page.route(postPattern,async route=>{
   if(route.request().method()!=='POST'){await route.continue();return;}
   normalPosts++;normalKey=route.request().headers()['idempotency-key'];
   const response=await route.fetch();if(!response.ok())throw Error('normal verification was not applied');
   normalApplied();await normalHeld;await route.fulfill({response});normalDelivered();
  });
  await remountedDetail.getByRole('button',{name:'출처 근거 확인',exact:true}).click();
  await normalAppliedPromise;
  const previousPanel=await panel.elementHandle();
  await page.frames().find(f=>f!==page.mainFrame()).evaluate(()=>window.__haeryong.select('r2'));
  await page.waitForFunction(el=>!el.isConnected,previousPanel);
  await panel.locator('.evidence-region').getByRole('button',{name:/constructor · reference_only/}).first().click();
  const normalDetail=panel.locator('.evidence-region .evidence-detail').first();
  const normalRetry=normalDetail.getByRole('button',{name:'원래 작업 결과 다시 확인',exact:true});
  await normalRetry.waitFor();
  const normalResponse=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().includes('/source-evidence-verifications'));
  normalRelease();const deliveredNormalResponse=await normalResponse;await deliveredNormalResponse.finished();await normalDeliveredPromise;
  await page.evaluate(()=>new Promise(resolve=>setTimeout(resolve,0)));
  await normalRetry.click();
  await normalDetail.getByText('확인 결과: 일치(기대 비교',{exact:false}).waitFor();
  if(normalPosts!==1||normalReceipts.some(key=>key!==normalKey))throw Error('normal completion lost the original verification intent');
  facts.normalCompletionRetainedAcrossPanelRemount=true;
  await page.unroute(postPattern);page.off('request',normalRequest);

  facts.disclaimer=await page.getByText('서비스 확인은 작성자 선언을 승격하지 않으며',{exact:false}).first().isVisible();
  // A page-local region selection binds the exact page's region.
  const child=page.frames().find(f=>f!==page.mainFrame());
  await child.evaluate(()=>window.__haeryong.select('overview'));
  await page.waitForFunction(()=>document.querySelector('section[aria-label="근거 및 출처 패널"] h2')?.textContent.includes('영역 overview'));
  facts.regionSelectedHeader=true;
  facts.regionBlockSelected=await page.locator('.evidence-region[data-selected="true"]').first().getAttribute('data-selected');
  // Navigating to the second page clears the selection and shows ONLY that
  // page's own same-named region, never the first page's binding.
  await page.locator('dialog[aria-label="페이지 및 파일 목록"]').getByRole('link',{name:'nested/second.html',exact:true}).click();
  await page.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'둘째 페이지',exact:true}).waitFor({timeout:20000});
  await page.locator('section[aria-label="근거 및 출처 패널"]').getByText('둘째 개요' ,{exact:true}).waitFor({timeout:10000});
  facts.secondPageOwnRegion=true;
  facts.firstPageRegionGone=(await page.locator('section[aria-label="근거 및 출처 패널"]').getByText('첫 페이지 개요' ,{exact:true}).count())===0;
  await page.waitForFunction(()=>{const h=document.querySelector('section[aria-label="근거 및 출처 패널"] h2');return h&&!h.textContent.includes('영역 overview');});
  facts.selectionClearedOnNavigation=true;
  const second=page.frames().find(f=>f!==page.mainFrame());
  await second.evaluate(()=>window.__haeryong.select('overview'));
  await page.waitForFunction(()=>document.querySelector('section[aria-label="근거 및 출처 패널"] h2')?.textContent.includes('영역 overview'));
  facts.sameNameRegionRebound=true;
  facts.secondPageEvidence=await page.getByRole('button',{name:/evidence-둘째 · verified\\(작성자 선언\\)/}).first().isVisible();
  // Reading and ordinary interaction stay usable with the panel open.
  await page.frameLocator('iframe[title="결과 뷰어"]').locator('#increment2').click();
  facts.readingInteractive=await page.frameLocator('iframe[title="결과 뷰어"]').locator('#increment2').textContent()==='1';
  // The exact import mount keeps working alongside the evidence panel.
  await page.getByRole('button',{name:'새 버전 가져오기',exact:true}).click();
  await page.getByTestId('import-control').waitFor({timeout:10000});
  facts.importMountIntact=true;
  await page.getByRole('button',{name:'가져오기 패널 닫기',exact:true}).click();
  if(SCREENSHOT)await page.screenshot({path:SCREENSHOT,fullPage:true});
  // T-003-04-01: a directory without supplied metadata stays fully readable.
  await page.goto(PLAIN);
  await page.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'평범한 문서',exact:true}).waitFor({timeout:20000});
  await page.getByRole('button',{name:'근거 패널',exact:true}).click();
  await page.getByText('공급된 소스/근거 메타데이터 없음',{exact:false}).first().waitFor({timeout:10000});
  facts.absentMetadataHonest=true;
  // The absent-metadata document reaches its own honest no-capture summary.
  await page.waitForFunction(()=>{const text=document.querySelector('.assessment-block')?.textContent||'';return text.includes('기록된 배지:')||text.includes('선택된 실제 캡처 대상이 없습니다');},{timeout:10000});
  facts.absentAssessmentsReal=true;
  await page.frameLocator('iframe[title="결과 뷰어"]').locator('#plain-button').click();
  facts.absentReadingInteractive=await page.frameLocator('iframe[title="결과 뷰어"]').locator('#plain-button').textContent()==='1';
  return facts;
 }""".replace('ROUTE',json.dumps(route)).replace('PLAIN',json.dumps(plain_route)).replace('VERSION',json.dumps(version)).replace('SEQUENCE',json.dumps(str(detail.get('sequence')))).replace('OPAQUE_IDS',json.dumps(opaque_ids,ensure_ascii=False)).replace('SCREENSHOT',json.dumps(str(args.screenshot.resolve()) if args.screenshot else None)))
 require('normal_verification_completion_survives_panel_remount',smoke.get('normalCompletionRetainedAcrossPanelRemount'))
 require('remounted_source_check_uses_original_receipt',smoke.get('remountedSourceCheckOriginalReceipt'))
 proof['smoke']=smoke
 require('version_header_exact_identity',smoke.get('versionIdPreserved') and smoke.get('versionHeader'),smoke)
 require('keyboard_opens_panel',smoke.get('keyboardOpenedPanel'))
 require('inherited_key_first_click',smoke.get('inheritedKeyFirstClickExpanded')=='true')
 require('authored_service_separation',smoke.get('serviceSeparation'))
 require('real_empty_verifications',smoke.get('verificationsEmpty'))
 require('opaque_ids_exact_real_daemon_and_empty_history',smoke.get('opaqueIds')==[key for key,_ in opaque_ids])
 require('real_assessments',smoke.get('assessmentsReal') and smoke.get('future503Count')==0)
 require('source_check_captured_history_and_digest',smoke.get('sourceCheckEnabled') and smoke.get('realCapturedAndHistory') and smoke.get('authorDigestOptionMatched'))
 require('no_verification_claim_copy',smoke.get('disclaimer'))
 require('page_local_region_binding',smoke.get('regionSelectedHeader') and smoke.get('regionBlockSelected')=='true')
 require('same_name_region_distinct',smoke.get('secondPageOwnRegion') and smoke.get('firstPageRegionGone') and smoke.get('selectionClearedOnNavigation') and smoke.get('sameNameRegionRebound') and smoke.get('secondPageEvidence'))
 require('reading_and_import_intact',smoke.get('readingInteractive') and smoke.get('importMountIntact'))
 require('absent_metadata_still_readable',smoke.get('absentMetadataHonest') and smoke.get('absentAssessmentsReal') and smoke.get('absentReadingInteractive'))
 token=js("async page=>await page.evaluate(()=>localStorage.getItem('haeryong.owner-session.v1'))")
 touch=js(('''async (page) => {
  const context=await page.context().browser().newContext({hasTouch:true,viewport:{width:820,height:1180}});
  // Test automation must never inject the disposable bearer into generated
  // frames or foreign origins: the write runs only in the trusted TOP frame
  // of the exact selected host origin.
  await context.addInitScript(([t,o])=>{if(window===top&&location.origin===o)localStorage.setItem('haeryong.owner-session.v1',t)},TOKEN);
  const t=await context.newPage();
  await t.goto(ROUTE);
  await t.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'근거 첫 페이지',exact:true}).waitFor({timeout:20000});
  await t.getByRole('button',{name:'근거 패널',exact:true}).tap();
  await t.locator('section[aria-label="근거 및 출처 패널"]').waitFor({timeout:10000});
  const openedByTap=true;
  await t.locator('section[aria-label="근거 및 출처 패널"]').getByText('첫 페이지 개요' ,{exact:true}).waitFor({timeout:10000});
  await t.locator('.evidence-region').getByRole('button',{name:/constructor · reference_only/}).first().tap();
  const expanded=await t.locator('.evidence-region').getByRole('button',{name:/constructor · reference_only/}).first().getAttribute('aria-expanded');
  const dot=t.locator('.version-evidence .evidence-row').filter({has:t.getByRole('button',{name:'.. · reference_only(작성자 선언) · notes/dot-dot.txt',exact:true})});
  await dot.getByRole('button',{name:'.. · reference_only(작성자 선언) · notes/dot-dot.txt',exact:true}).tap();
  await dot.getByText('작성자 선언(불변): reference_only(작성자 선언) · notes/dot-dot.txt',{exact:true}).waitFor({timeout:10000});
  await t.close();
  return {openedByTap,expanded,dotIdentityByTouch:true};
 }''').replace('TOKEN',json.dumps([token,product.endpoint])).replace('ROUTE',json.dumps(route)))
 require('touch_opens_panel',touch.get('openedByTap'))
 require('touch_expands_evidence',touch.get('expanded')=='true')
 require('touch_dot_identity_real_daemon',touch.get('dotIdentityByTouch'))
 # TASK-003-04 seeded presentation phase: the same real browser against an
 # explicitly labelled local seeded fixture backend that forwards everything
 # to the real daemon and serves seeded 200 envelopes ONLY on the declared
 # future-capability GET query routes. Proves seeded overlay/history/coverage
 # presentation, later-cursor failure retaining partial rows with retry from
 # the exact failed cursor, and a delayed response after navigation never
 # relabels the new selection — presentation only, never a real source read.
 import threading,socket,http.client,select
 from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
 upstream=product.endpoint
 ev_version=version
 units_u1=[{'kind':'region','page_path':'first.html','region_id':'overview','outcome':'assessed','reason':None,'discrepancy':None,'assessment_id':'asm-1','target_set_id':'t1'},
           {'kind':'region','page_path':'first.html','region_id':'r2','outcome':'assessed','reason':None,'discrepancy':True,'assessment_id':'asm-1','target_set_id':'t1'},
           {'kind':'region','page_path':'first.html','region_id':'r3','outcome':'partial','reason':'일부만 확인','discrepancy':None,'assessment_id':'asm-1','target_set_id':'t1'}]
 units_u2=[{'kind':'region','page_path':'first.html','region_id':'r4','outcome':'blocked','reason':'읽기 거부','discrepancy':None,'assessment_id':'asm-1','target_set_id':'t1'}]
 applicable_t1={'source_intent':'historical','target_set_id':'t1','captured_at':'2026-10-19T00:00:00Z','scope':{'kind':'page','page_path':'first.html','region_id':None},'status':'aligned_within_scope','coverage':{'coverage_set_id':'c1','unit_kind':'region','eligible':4,'assessed':4,'complete':True,'units_url':f'/api/v1/versions/{ev_version}/assessments/coverage?coverage_set_id=c1&attempt_run_id=11111111-2222-4333-8444-555555555555'},'assessed_at':'2026-10-19T01:00:00Z','reassessment_required':False}
 applicable_t2={'source_intent':'historical','target_set_id':'t2','captured_at':'2026-10-19T00:00:00Z','scope':{'kind':'page','page_path':'nested/second.html','region_id':None},'status':'aligned_within_scope','coverage':{'coverage_set_id':'c2','unit_kind':'region','eligible':1,'assessed':1,'complete':True,'units_url':f'/api/v1/versions/{ev_version}/assessments/coverage?coverage_set_id=c2'},'assessed_at':'2026-10-19T01:00:00Z','reassessment_required':False}
 seeded_verifications={'items':[{'verification_id':'ver-1','outcome':'matched','checked_at':'2026-10-19T02:00:00Z','verified_at':'2026-10-19T02:00:00Z','reason':None}],'next_cursor':'vh2'}
 seeded_verifications_v2=[{'verification_id':'ver-2','outcome':'captured','checked_at':'2026-10-19T03:00:00Z','verified_at':'2026-10-19T03:00:00Z','reason':None}]
 proxy_state={'assessments_first_done':False,'p2_failed':False,'coverage_u2_seen':0}
 proxy_log=[]
 proxy_errors=[]
 # TASK-003-04 R9: private scratch harness build served by this same
 # labelled fixture server under /harness (existing check_transfer pattern).
 harness_dir=work/'harness-dist'
 subprocess.run([str(REPO/'frontend/node_modules/.bin/vite'),'build','--config','vite.harness.config.ts'],cwd=REPO/'frontend',env=env|{'HAEYONG_HARNESS_OUT_DIR':str(harness_dir)},check=True,capture_output=True,timeout=420)
 metadata_stop=threading.Event()
 metadata_done=threading.Event();metadata_done.set()
 class SeededEvidence(BaseHTTPRequestHandler):
  def log_message(self,*a): pass
  def _handle(self):
   try:
    if self.command=='GET' and self.path.split('?')[0].startswith('/harness'):
     # Private scratch harness build (R9 first-commit oracle): serve files
     # from the build root only, exactly like the check_transfer proxy.
     name=self.path.split('?')[0][len('/harness'):] or '/harness.html'
     candidate=(harness_dir/name.lstrip('/')).resolve()
     if not str(candidate).startswith(str(harness_dir.resolve())) or not candidate.is_file():
      self.send_response(404);self.send_header('Content-Length','0');self.end_headers();return
     body=candidate.read_bytes()
     mime={'.html':'text/html; charset=utf-8','.js':'application/javascript','.css':'text/css'}.get(candidate.suffix,'application/octet-stream')
     self.send_response(200);self.send_header('Content-Type',mime);self.send_header('Content-Length',str(len(body)));self.end_headers()
     if self.command!='HEAD':self.wfile.write(body)
     return
    if self.command=='GET' and self.path in ('/__t04/metadata-fail','/__t04/metadata-hold','/__t04/metadata-status'):
     if self.path.endswith('-fail'):proxy_state['metadata_mode']='fail'
     if self.path.endswith('-hold'):proxy_state['metadata_mode']='hold'
     self._envelope({key:proxy_state.get(key,False) for key in ('metadata_opened','metadata_cancelled','metadata_timed_out')});return
    if self.command=='GET' and self.path==f'/api/v1/versions/{ev_version}/metadata?kind=supplied' and proxy_state.get('metadata_mode')=='fail':
     self._envelope(None,status=500);return
    # Labelled fixture controls for the F1 stale-detail regression: the
    # driver toggles a failing document-detail lookup for one document.
    if self.command=='GET' and self.path=='/__t04/doc-detail-fail':
     proxy_state['doc_detail_fail']=True
     self.send_response(200);self.send_header('Content-Length','2');self.end_headers();self.wfile.write(b'ok');return
    if self.command=='GET' and self.path=='/__t04/doc-detail-heal':
     proxy_state['doc_detail_fail']=False
     self.send_response(200);self.send_header('Content-Length','2');self.end_headers();self.wfile.write(b'ok');return
    if self.command=='GET' and proxy_state.get('doc_detail_fail') and self.path==f'/api/v1/documents/{document}':
     proxy_log.append(('documents','fail'))
     self._envelope(None,status=500);return
    # The events stream is a long-lived SSE channel: forward it TRANSPARENTLY
    # — no buffered Content-Length — writing each upstream chunk through to
    # the client until either side closes, so the fixture never substitutes a
    # synthetic transport. A client cancel surfaces as a broken pipe and
    # simply closes this connection.
    if self.command=='GET' and self.path.split('?')[0]=='/api/v1/events':
     headers={k:v for k,v in self.headers.items() if k.lower() not in ('host','content-length','origin','connection','transfer-encoding','expect')}
     headers['Host']=urllib.parse.urlsplit(upstream).netloc
     headers['Origin']=upstream
     headers['Connection']='close'
     request=urllib.request.Request(upstream+self.path,headers=headers,method='GET')
     response=urllib.request.urlopen(request,timeout=None)
     self.send_response(response.status)
     for key,value in response.getheaders():
      if key.lower() not in ('transfer-encoding','content-length','connection'):self.send_header(key,value)
     self.send_header('Connection','close');self.end_headers()
     try:
      while True:
       chunk=response.read1(512)
       if not chunk:break
       self.wfile.write(chunk);self.wfile.flush()
     except (BrokenPipeError,ConnectionResetError):
      # A client cancel/close of the event stream is normal liveness churn.
      pass
     except http.client.IncompleteRead as incomplete:
      if incomplete.partial:
       proxy_errors.append(f'GET {self.path}: stream partial {incomplete.partial!r}')
      # A zero-byte incomplete read after the client disconnected is the
      # upstream-side face of the same normal client close.
     except Exception as error:  # noqa: BLE001 - recorded fixture diagnostic
      proxy_errors.append(f'GET {self.path}: stream {error!r}')
     finally:
      try:response.close()
      except Exception:pass
     self.close_connection=True
     return
    if self.command=='GET' and self._serve_seeded():return
    self._forward()
   except Exception as error:  # noqa: BLE001 - bounded fixture diagnostics
    proxy_errors.append(f'{self.command} {self.path}: {error!r}')
    try:
     body=json.dumps({'error':{'code':'SEEDED_PROXY_FAILURE','message':str(error),'retryable':False}}).encode()
     self.send_response(502);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
    except Exception: pass
  def do_GET(self):self._handle()
  def do_POST(self):self._handle()
  def do_PUT(self):self._handle()
  def do_DELETE(self):self._handle()
  def do_HEAD(self):self._handle()
  def _envelope(self,value,status=200):
   body=json.dumps({'data':value} if status==200 else {'error':{'code':'SEEDED_TEMPORARY','message':'labelled later-cursor fault','retryable':True}},ensure_ascii=False).encode()
   self.send_response(status);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
  def _serve_seeded(self):
   path=self.path
   query=urllib.parse.parse_qs(urllib.parse.urlsplit(path).query)
   cursor=(query.get('cursor') or [''])[0]
   if path.startswith(f'/api/v1/versions/{ev_version}/assessments/coverage'):
    if (query.get('coverage_set_id') or [''])[0]=='c2':
     self._envelope({'items':[{'kind':'region','page_path':'nested/second.html','region_id':'overview','outcome':'assessed','reason':None,'discrepancy':None,'assessment_id':'asm-2','target_set_id':'t2'}],'next_cursor':None});return True
    assert query.get('attempt_run_id')==['11111111-2222-4333-8444-555555555555'],'T5 attempt locator lost'
    proxy_log.append(('coverage_attempt',cursor,query['attempt_run_id'][0]))
    if cursor=='u2':
     proxy_state['coverage_u2_seen']+=1;proxy_log.append(('coverage','u2'))
     if not proxy_state.get('t5_coverage_failed'):
      proxy_state['t5_coverage_failed']=True;self._envelope(None,status=500);return True
     self._envelope({'items':units_u2,'next_cursor':None});return True
    proxy_log.append(('coverage','first'))
    self._envelope({'items':units_u1,'next_cursor':'u2'});return True
   if path.startswith(f'/api/v1/versions/{ev_version}/assessments'):
    page=(query.get('page_path') or [''])[0]
    if page=='first.html':
     if cursor=='p2':
      if not proxy_state['p2_failed']:
       proxy_state['p2_failed']=True;proxy_log.append(('assessments','p2-fail'))
       self._envelope(None,status=500);return True
      proxy_log.append(('assessments','p2-retry'))
      self._envelope({'items':[{'assessment_id':'asm-2','version_id':ev_version,'scope':{'kind':'page','page_path':'first.html','region_id':None},'target_set_id':'t1','state':'published'}],'next_cursor':None,'applicable':applicable_t1});return True
     if proxy_state['assessments_first_done']:
      proxy_log.append(('assessments','terminal'))
      self._envelope({'items':[{'assessment_id':'asm-1','version_id':ev_version,'scope':{'kind':'page','page_path':'first.html','region_id':None},'target_set_id':'t1','state':'published'}],'next_cursor':None,'applicable':applicable_t1});return True
     proxy_state['assessments_first_done']=True;proxy_log.append(('assessments','p1'))
     self._envelope({'items':[{'assessment_id':'asm-1','version_id':ev_version,'scope':{'kind':'page','page_path':'first.html','region_id':None},'target_set_id':'t1','state':'published'}],'next_cursor':'p2','applicable':applicable_t1});return True
    if page=='nested/second.html':
     proxy_log.append(('assessments','second-delayed'))
     time.sleep(3.0)
     try:
      self._envelope({'items':[],'next_cursor':None,'applicable':applicable_t2});return True
     except (BrokenPipeError,ConnectionResetError):
      # A client that closed during the deliberate delay is normal churn.
      return True
    self._envelope({'items':[],'next_cursor':None,'applicable':None});return True
   if urllib.parse.urlsplit(path).path==f'/api/v1/versions/{ev_version}/source-evidence-verifications' and query.get('evidence_id')==['constructor']:
    if cursor=='vh2':
     if not proxy_state.get('vh2_failed'):
      proxy_state['vh2_failed']=True;proxy_log.append(('verifications','vh2-fail'))
      self._envelope(None,status=500);return True
     proxy_log.append(('verifications','vh2-retry'))
     self._envelope({'items':seeded_verifications_v2,'next_cursor':None});return True
    self._envelope(seeded_verifications);return True
   return False
  def _forward(self):
   length=int(self.headers.get('Content-Length') or 0)
   body=self.rfile.read(length) if length else None
   # Fixture-only header handling mirroring the existing harness test proxy:
   # the daemon's origin guard accepts its exact configured origin, so the
   # forwarded request carries the UPSTREAM Host/Origin, never the browser's
   # proxy-origin values. Product origin policy is unchanged.
   headers={k:v for k,v in self.headers.items() if k.lower() not in ('host','content-length','origin','connection','transfer-encoding','expect')}
   headers['Host']=urllib.parse.urlsplit(upstream).netloc
   headers['Origin']=upstream
   headers['Content-Length']=str(len(body or b''))
   headers['Connection']='close'
   request=urllib.request.Request(upstream+self.path,data=body,headers=headers,method=self.command)
   try:
    with urllib.request.urlopen(request,timeout=60) as response:
     data=response.read();status=response.status;headers_out=[(k,v) for k,v in response.getheaders() if k.lower() not in ('transfer-encoding','content-length','connection')]
   except urllib.error.HTTPError as error:
    data=error.read();status=error.code;headers_out=[(k,v) for k,v in error.headers.items() if k.lower() not in ('transfer-encoding','content-length','connection')]
   self.send_response(status)
   for key,value in headers_out:self.send_header(key,value)
   self.send_header('Content-Length',str(len(data)));self.end_headers()
   if self.command=='GET' and self.path==f'/api/v1/versions/{ev_version}/metadata?kind=supplied' and proxy_state.get('metadata_mode')=='hold':
    # Real upstream bytes/validator, but deliberately no EOF or next chunk.
    # Observe the client's close WITHOUT sending another byte: otherwise the
    # old chunk-time stale check alone could make a broken cleanup pass.
    proxy_state['metadata_mode']='normal';metadata_done.clear()
    try:
     assert status==200 and len(data)>1
     self.wfile.write(data[:1]);self.wfile.flush();proxy_state['metadata_opened']=True
     deadline=time.monotonic()+15
     while not metadata_stop.is_set() and time.monotonic()<deadline:
      if select.select([self.connection],[],[],0.05)[0]:
       if not self.connection.recv(1,socket.MSG_PEEK):
        proxy_state['metadata_cancelled']=True;break
     else:
      if not metadata_stop.is_set():proxy_state['metadata_timed_out']=True
    except ConnectionResetError:
     proxy_state['metadata_cancelled']=True
    finally:
     self.close_connection=True;metadata_done.set()
    return
   self.wfile.write(data)
 seeded_port=socket.socket();seeded_port.bind(('127.0.0.1',0));seeded_port_number=seeded_port.getsockname()[1];seeded_port.close()
 seeded_server=ThreadingHTTPServer(('127.0.0.1',seeded_port_number),SeededEvidence)
 threading.Thread(target=seeded_server.serve_forever,daemon=True).start()
 seeded_url=f'http://127.0.0.1:{seeded_port_number}'
 seeded_route=seeded_url+f'/documents/{document}/versions/{version}?'+urllib.parse.urlencode({'page':'first.html'})
 try:
  seeded=None
  try:
   seeded=js(('''async (page) => {
   const context=await page.context().browser().newContext({hasTouch:true,viewport:{width:820,height:1180}});
   // Same trusted-top-frame/exact-origin scoping as the touch phase: the
   // disposable bearer is never injected into generated frames or foreign
   // origins by test automation itself.
   await context.addInitScript(([t,o])=>{if(window===top&&location.origin===o)localStorage.setItem('haeryong.owner-session.v1',t)},TOKEN);
   const t=await context.newPage();
   const pageErrors=[];const consoleErrors=[];
   t.on('pageerror',e=>pageErrors.push(String(e)));
   t.on('console',m=>{if(m.type()==='error')consoleErrors.push(m.text());});
   const facts={pageErrors,consoleErrors};
   try{
   await t.goto(ROUTE);
   await t.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'근거 첫 페이지',exact:true}).waitFor({timeout:20000});
   // Keyboard activation of the panel toggle in the seeded context.
   const toggle=t.getByRole('button',{name:'근거 패널',exact:true});
   await toggle.focus();await t.keyboard.press('Enter');
   const panel=t.locator('section[aria-label="근거 및 출처 패널"]');
   await panel.waitFor({timeout:10000});
   facts.keyboardOpenedPanel=true;
   // Later-cursor failure: page 1 served, page 2 fails once with the partial
   // row retained, then the retry completes from the exact failed cursor.
   await t.getByText('평가 이력 페이지를 확인하지 못했습니다',{exact:false}).first().waitFor({timeout:10000});
   facts.partialRowRetained=await t.getByText('asm-1',{exact:false}).first().isVisible();
   await t.getByRole('button',{name:'실패한 지점부터 다시 불러오기',exact:true}).tap();
   await t.getByText('asm-2',{exact:false}).first().waitFor({timeout:10000});
   facts.retryCompletedHistory=true;
   // T5 failed coverage page resumes with the same target/set/attempt locator.
   await t.getByRole('button',{name:'커버리지 다시 불러오기',exact:true}).waitFor({timeout:10000});
   await t.getByRole('button',{name:'커버리지 다시 불러오기',exact:true}).tap();
   await t.getByText('평가됨 2/4',{exact:false}).first().waitFor({timeout:10000});
   // Seeded applicable overlay vs unit-derived counts/status and the conflict.
   await t.getByText('기록된 배지',{exact:false}).first().waitFor({timeout:10000});
   const badgeText=await panel.textContent();
   facts.overlayBadgeShown=badgeText.includes('범위 내 정렬');
   facts.derivedCounts=badgeText.includes('평가됨 2/4')&&badgeText.includes('대비 구식');
   facts.conflictSurfaced=badgeText.includes('기록된 배지가 단위 파생 결과와 충돌합니다');
   // Touch activation of the units disclosure with per-unit reasons.
   await t.getByRole('button',{name:'단위/사유 펼치기/접기',exact:true}).tap();
   await t.getByText('읽기 거부',{exact:false}).first().waitFor({timeout:10000});
   facts.unitsReasonsVisible=badgeText.concat(await panel.textContent()).includes('불일치 있음');
   // Seeded verification history: the first page arrives, the second cursor
   // page fails once (partial ver-1 retained), then a KEYBOARD retry
   // dispatches the exact failed cursor and completes with ver-2.
   await t.locator('.evidence-region').getByRole('button',{name:/constructor · reference_only/}).first().tap();
   await t.locator('.evidence-region .evidence-detail').getByRole('button',{name:'검증 이력 보기/숨기기',exact:true}).click();
   await t.locator('.evidence-region .evidence-detail').getByText('일치(기대 비교 — 사후 검증)',{exact:false}).first().waitFor({timeout:10000});
   await t.locator('.evidence-region .evidence-detail').getByText('검증 이력 오류:',{exact:false}).first().waitFor({timeout:10000});
   facts.historyPartialRowRetained=(await t.locator('.evidence-region .evidence-detail').first().textContent()).includes('ver-1');
   const historyRetry=t.locator('.evidence-region .evidence-detail').getByRole('button',{name:'검증 이력 다시 불러오기',exact:true});
   await historyRetry.focus();await t.keyboard.press('Enter');
   await t.locator('.evidence-region .evidence-detail').getByText('캡처됨(기대 비교 없음',{exact:false}).first().waitFor({timeout:10000});
   const detailText=await t.locator('.evidence-region .evidence-detail').first().textContent();
   facts.seededHistoryLabels=detailText.includes('캡처됨(기대 비교 없음')&&detailText.includes('ver-1')&&detailText.includes('ver-2');
   // Keyboard close/reopen of the verification history disclosure.
   const historyToggle=t.locator('.evidence-region .evidence-detail').getByRole('button',{name:'검증 이력 보기/숨기기',exact:true});
   await historyToggle.focus();await t.keyboard.press('Enter');
   await t.waitForFunction(()=>{const b=[...document.querySelectorAll('button')].find(b=>b.textContent==='검증 이력 보기/숨기기');return b&&b.getAttribute('aria-expanded')==='false';},{timeout:10000});
   facts.historyKeyboardClosedHidden=(await t.locator('.evidence-region .evidence-detail').getByText('일치(기대 비교 — 사후 검증)',{exact:false}).count())===0;
   await t.keyboard.press('Enter');
   await t.waitForFunction(()=>{const b=[...document.querySelectorAll('button')].find(b=>b.textContent==='검증 이력 보기/숨기기');return b&&b.getAttribute('aria-expanded')==='true';},{timeout:10000});
   facts.historyKeyboardReopened=(await t.locator('.evidence-region .evidence-detail').getByText('일치(기대 비교 — 사후 검증)',{exact:false}).count())>=1;
   // Keyboard close/reopen of the coverage units disclosure.
   const unitsToggle=t.getByRole('button',{name:'단위/사유 펼치기/접기',exact:true});
   await unitsToggle.focus();await t.keyboard.press('Enter');
   await t.waitForFunction(()=>{const b=[...document.querySelectorAll('button')].find(b=>b.textContent==='단위/사유 펼치기/접기');return b&&b.getAttribute('aria-expanded')==='false';},{timeout:10000});
   facts.unitsKeyboardClosedHidden=(await t.getByText('읽기 거부',{exact:false}).count())===0;
   await t.keyboard.press('Enter');
   await t.waitForFunction(()=>{const b=[...document.querySelectorAll('button')].find(b=>b.textContent==='단위/사유 펼치기/접기');return b&&b.getAttribute('aria-expanded')==='true';},{timeout:10000});
   facts.unitsKeyboardReopened=(await t.getByText('읽기 거부',{exact:false}).count())>=1;
   facts.authoredNotUpgraded=detailText.includes('작성자 선언(불변): reference_only(작성자 선언)');
   facts.posthocDisclaimer=detailText.includes('사후 검증(posthoc)은 원본 제공 입력을 관측했다고 주장하지 않습니다');
   // F2: an unbound authored evidence record stays inspectable in the
   // explicit version context on a page whose declared regions never
   // reference it (keyboard activation).
   const freeRow=t.getByRole('button',{name:/evidence-free · reference_only/});
   await freeRow.focus();await t.keyboard.press('Enter');
   await t.locator('.version-evidence').getByText('작성자 선언(불변): reference_only(작성자 선언)',{exact:false}).first().waitFor({timeout:10000});
   facts.f2UnboundEvidenceVersionContext=(await freeRow.getAttribute('aria-expanded'))==='true';
   // Close/reopen the actual narrow-screen modal to reach the header.
   const closeEvidence=async()=>{
    const close=t.getByRole('button',{name:'문맥 닫기',exact:true});
    if(await close.isVisible())await close.tap();
   };
   const openEvidence=async()=>{
    const toggle=t.getByRole('button',{name:'근거 패널',exact:true});
    if(await toggle.getAttribute('aria-expanded')!=='true')await toggle.tap();
    await panel.waitFor({timeout:10000});
   };
   await closeEvidence();
   // F3: the snapshot source's full 64-hex identity is disclosable (touch).
   const identityToggle=t.locator('.version-sources').getByRole('button',{name:'전체 소스 신원 보기/숨기기',exact:true}).nth(1);
   await identityToggle.tap();
   await t.locator('.version-sources').getByText('snapshot_sha256 fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210',{exact:false}).waitFor({timeout:10000});
   facts.f3SnapshotFullIdentity=true;
   await identityToggle.tap();
   await identityToggle.focus();await t.keyboard.press('Enter');
   await t.locator('.version-sources').getByText('snapshot_sha256 fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210',{exact:false}).waitFor({timeout:10000});
   await t.keyboard.press('Enter');
   facts.f3SnapshotKeyboard=(await identityToggle.getAttribute('aria-expanded'))==='false';
   await openEvidence();
   if(SCREENSHOT)await t.screenshot({path:SCREENSHOT,fullPage:true});
   await closeEvidence();
   // Delayed second-page response: navigate away before it arrives and prove
   // the late reply never relabels the newly selected first page.
   await t.locator('dialog[aria-label="페이지 및 파일 목록"]').getByRole('link',{name:'nested/second.html',exact:true}).tap();
   await t.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'둘째 페이지',exact:true}).waitFor({timeout:20000});
   await t.locator('dialog[aria-label="페이지 및 파일 목록"]').getByRole('link',{name:'first.html',exact:true}).tap();
   await t.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'근거 첫 페이지',exact:true}).waitFor({timeout:20000});
   await openEvidence();
   await t.getByText('기록된 배지',{exact:false}).first().waitFor({timeout:10000});
   await t.waitForTimeout(4000);
   const finalText=await panel.textContent();
   facts.delayedNeverRelabels=finalText.includes('t1')&&!finalText.includes('t2');
   facts.noStaleErrorBanner=!finalText.includes('평가 이력 페이지를 확인하지 못했습니다');
   facts.pageErrorsAfterAll=[...pageErrors];
   facts.consoleErrorsAfterAll=[...consoleErrors];
   // F1: A→B switch inside ONE live Reader document (host-history
   // pushState + popstate, never a full reload/remount) while B's
   // document-detail lookup fails: B's exact sequence appears (or an honest
   // unknown), never A's; B's valid metadata bytes parse with no false
   // integrity error; page JS and the already captured import form survive.
   await closeEvidence();
   const versionHeader=t.locator('.version-header');
   await versionHeader.getByText('버전 #'+ASEQUENCE,{exact:false}).first().waitFor({timeout:10000});
   await t.getByRole('button',{name:'새 버전 가져오기',exact:true}).click();
   const importPanel=t.locator('[data-testid="import-control"]');
   await importPanel.getByText('업로드 전 기준 버전(base): '+BVERSION,{exact:true}).waitFor({timeout:10000});
   const importElement=await importPanel.elementHandle();
   const baseBefore=await importPanel.locator('[data-testid="base-preview"]').textContent();
   await importPanel.getByRole('textbox',{name:'문서 검색',exact:true}).fill('보존할 입력');
   const importPreserved=async()=>await importElement.evaluate(el=>el.isConnected&&document.querySelector('[data-testid="import-control"]')===el)
     &&await importPanel.getByRole('textbox',{name:'문서 검색',exact:true}).inputValue()==='보존할 입력'
     &&await importPanel.locator('[data-testid="base-preview"]').textContent()===baseBefore;
   const control=async url=>{await t.evaluate(async value=>{const response=await fetch(value);if(!response.ok)throw Error('fixture control failed');},url);};
   await control(PROXY+'/__t04/doc-detail-fail');
   await t.evaluate(url=>{history.pushState({haeryongReaderContext:crypto.randomUUID()},'',url);window.dispatchEvent(new PopStateEvent('popstate'));},BURL);
   await t.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'근거 첫 페이지',exact:true}).waitFor({timeout:20000});
   await versionHeader.getByText('버전 #'+BSEQUENCE,{exact:false}).first().waitFor({timeout:10000});
   await versionHeader.getByText('v4 예시 B',{exact:false}).first().waitFor({timeout:10000});
   await t.getByRole('button',{name:'문서 정보 다시 불러오기',exact:true}).waitFor({timeout:10000});
   const f1Text=await t.evaluate(()=>document.body.textContent);
   const f1Header=await versionHeader.textContent();
   facts.f1BSequenceNotStaleA=f1Header.includes('버전 #'+BSEQUENCE+' ')&&!f1Header.includes('버전 #'+ASEQUENCE+' ');
   facts.f1NoFalseIntegrityError=!f1Text.includes('무결성 확인 실패');
   facts.f1ImportPreservedDuringFailure=await importPreserved();
   const evidenceToggle=t.getByRole('button',{name:'근거 패널',exact:true});
   if(await evidenceToggle.getAttribute('aria-expanded')!=='true')await evidenceToggle.tap();
   await t.locator('section[aria-label="근거 및 출처 패널"]').waitFor({timeout:10000});
   await t.locator('section[aria-label="근거 및 출처 패널"]').getByText('첫 페이지 개요' ,{exact:true}).waitFor({timeout:10000});
   facts.f1MetadataStillValidUnderB=true;
   await closeEvidence();
   await t.frameLocator('iframe[title="결과 뷰어"]').locator('#increment').click();
   facts.f1PageJSWorks=(await t.frameLocator('iframe[title="결과 뷰어"]').locator('#increment').textContent())==='1';
   await control(PROXY+'/__t04/doc-detail-heal');
   const [documentHealed,projectsHealed]=await Promise.all([
    t.waitForResponse(response=>response.url().split('?')[0]===PROXY+'/api/v1/documents/'+DOCUMENTID&&response.status()===200),
    t.waitForResponse(response=>response.url().split('?')[0]===PROXY+'/api/v1/projects'&&response.status()===200),
    t.getByRole('button',{name:'문서 정보 다시 불러오기',exact:true}).click(),
   ]);
   await documentHealed.finished();await projectsHealed.finished();
   await t.getByRole('button',{name:'문서 정보 다시 불러오기',exact:true}).waitFor({state:'hidden',timeout:10000});
   facts.f1ImportPreservedAfterRetry=await importPreserved();
   // A directory with several HTML pages and no default opens its listing:
   // no active page is required to inspect its version-wide evidence.
   await t.evaluate(url=>{history.pushState({haeryongReaderContext:crypto.randomUUID()},'',url);window.dispatchEvent(new PopStateEvent('popstate'));},BURL.split('?')[0]);
   await versionHeader.getByText('페이지 없음',{exact:false}).first().waitFor({timeout:10000});
   await t.locator('iframe[title="결과 뷰어"]').waitFor({state:'hidden',timeout:10000});
   await openEvidence();
   const noPage=t.locator('.version-evidence .evidence-row').filter({has:t.getByRole('button',{name:'evidence-free · reference_only(작성자 선언) · notes/free.txt',exact:true})});
   await noPage.getByRole('button',{name:'evidence-free · reference_only(작성자 선언) · notes/free.txt',exact:true}).click();
   await noPage.getByText('작성자 선언(불변): reference_only(작성자 선언) · notes/free.txt',{exact:true}).waitFor({timeout:10000});
   facts.f2PageLessVersionEvidence=true;
   await closeEvidence();
   // C1: within the SAME live Reader/client era, open A's metadata body
   // and leave its next read parked. Switching to B must abort A before
   // any further body byte/EOF, while B's own metadata and JS still work.
   await control(PROXY+'/__t04/metadata-fail');
   await t.evaluate(url=>{history.pushState({haeryongReaderContext:crypto.randomUUID()},'',url);window.dispatchEvent(new PopStateEvent('popstate'));},ROUTE);
   await versionHeader.getByText('버전 #'+ASEQUENCE,{exact:false}).first().waitFor({timeout:10000});
   const metadataRetry=t.getByRole('button',{name:'메타데이터 다시 불러오기',exact:true});
   await metadataRetry.waitFor({timeout:10000});
   await control(PROXY+'/__t04/metadata-hold');
   await Promise.all([
    t.waitForResponse(r=>r.url().split('?')[0]===PROXY+'/api/v1/versions/'+AVERSION+'/metadata'&&r.status()===200),
    metadataRetry.click(),
   ]);
   await t.waitForFunction(async url=>(await (await fetch(url)).json()).data.metadata_opened,PROXY+'/__t04/metadata-status',{timeout:10000});
   await t.evaluate(url=>{history.pushState({haeryongReaderContext:crypto.randomUUID()},'',url);window.dispatchEvent(new PopStateEvent('popstate'));},BURL);
   await t.waitForFunction(async url=>(await (await fetch(url)).json()).data.metadata_cancelled,PROXY+'/__t04/metadata-status',{timeout:10000});
   const holdStatus=await t.evaluate(async url=>(await (await fetch(url)).json()).data,PROXY+'/__t04/metadata-status');
   facts.parkedMetadataCancelled=holdStatus.metadata_cancelled&&!holdStatus.metadata_timed_out;
   await versionHeader.getByText('v4 예시 B',{exact:false}).first().waitFor({timeout:10000});
   await t.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'근거 첫 페이지',exact:true}).waitFor({timeout:20000});
   await t.frameLocator('iframe[title="결과 뷰어"]').locator('#increment').click();
   facts.touchViewportPreserved=t.viewportSize().width===820&&t.viewportSize().height===1180;
   facts.replacementMetadataAndJSWork=(await t.frameLocator('iframe[title="결과 뷰어"]').locator('#increment').textContent())==='1'
    &&!(await t.evaluate(()=>document.body.textContent)).includes('공급 메타데이터를 확인하지 못했습니다');
   facts.pageErrorsAfterAll=[...pageErrors];
   facts.consoleErrorsAfterAll=[...consoleErrors];
   }catch(error){
    facts.error=String(error&&error.message||error);
    facts.url=t.url();
    facts.bodyText=(await t.evaluate(()=>document.body.textContent).catch(()=>'')).slice(0,400);
    facts.pageErrorsAfterAll=[...pageErrors];
    facts.consoleErrorsAfterAll=[...consoleErrors];
   }
   await t.close();
   return facts;
  }''').replace('TOKEN',json.dumps([token,seeded_url])).replace('ROUTE',json.dumps(seeded_route)).replace('PROXY',json.dumps(seeded_url)).replace('ASEQUENCE',json.dumps(detail['sequence'])).replace('BSEQUENCE',json.dumps(detail_b['sequence'])).replace('AVERSION',json.dumps(version)).replace('BVERSION',json.dumps(version_b)).replace('DOCUMENTID',json.dumps(document)).replace('BURL',json.dumps(seeded_url+f'/documents/{document}/versions/{version_b}?'+urllib.parse.urlencode({'page':'first.html'}))).replace('SCREENSHOT',json.dumps(str(args.screenshot.resolve()).replace('.png','-seeded.png') if args.screenshot else None)))
  finally:
   proof['seeded_proxy_errors']=proxy_errors
   proof['seeded_proxy_log']=proxy_log
  if seeded is None:raise AssertionError('seeded browser phase never returned facts')
  proof['seeded_smoke']=seeded
  require('seeded_keyboard_opens_panel',seeded.get('keyboardOpenedPanel'))
  require('seeded_partial_row_retained',seeded.get('partialRowRetained'))
  require('seeded_retry_completes_history',seeded.get('retryCompletedHistory'))
  require('seeded_overlay_vs_derived',seeded.get('overlayBadgeShown') and seeded.get('derivedCounts') and seeded.get('conflictSurfaced'))
  require('seeded_units_reasons_visible',seeded.get('unitsReasonsVisible'))
  require('seeded_history_partial_row_retained',seeded.get('historyPartialRowRetained'))
  require('seeded_keyboard_history_retry',seeded.get('seededHistoryLabels'))
  require('seeded_history_keyboard_close_reopen',seeded.get('historyKeyboardClosedHidden') and seeded.get('historyKeyboardReopened'))
  require('seeded_units_keyboard_close_reopen',seeded.get('unitsKeyboardClosedHidden') and seeded.get('unitsKeyboardReopened'))
  require('f2_unbound_evidence_version_context',seeded.get('f2UnboundEvidenceVersionContext'))
  require('seeded_narrow_touch_viewport_preserved',seeded.get('touchViewportPreserved'))
  require('f3_snapshot_full_identity',seeded.get('f3SnapshotFullIdentity'))
  require('f3_snapshot_keyboard_disclosure',seeded.get('f3SnapshotKeyboard'))
  require('f1_inapp_version_switch_no_stale_detail',seeded.get('f1BSequenceNotStaleA') and seeded.get('f1NoFalseIntegrityError') and seeded.get('f1MetadataStillValidUnderB') and seeded.get('f1PageJSWorks'))
  require('f1_import_state_preserved_across_failure_and_retry',seeded.get('f1ImportPreservedDuringFailure') and seeded.get('f1ImportPreservedAfterRetry'))
  require('f1_document_failure_injected',('documents','fail') in proxy_log)
  require('f2_pageless_version_evidence',seeded.get('f2PageLessVersionEvidence'))
  require('parked_metadata_body_cancelled_before_next_byte',seeded.get('parkedMetadataCancelled'))
  require('replacement_metadata_and_js_survive_old_cancel',seeded.get('replacementMetadataAndJSWork'))
  require('seeded_history_labels_truthful',seeded.get('seededHistoryLabels') and seeded.get('authoredNotUpgraded') and seeded.get('posthocDisclaimer'))
  require('seeded_delayed_never_relabels',seeded.get('delayedNeverRelabels') and seeded.get('noStaleErrorBanner'))
  require('seeded_console_health',not seeded.get('pageErrorsAfterAll'),seeded.get('pageErrorsAfterAll'))
  explained=[line for line in (seeded.get('consoleErrorsAfterAll') or []) if '503' not in line and '500' not in line]
  require('seeded_console_only_explained_faults',not explained,explained)
  # Driver-side fixture assertions: the retry reused the EXACT failed cursor
  # and every started coverage enumeration crossed its second cursor page to
  # completion (plus the one labelled T5 failed u2 attempt).
  coverage_starts=proxy_log.count(('coverage','first'))
  require('fixture_exact_failed_cursor_retry',('assessments','p2-fail') in proxy_log and ('assessments','p2-retry') in proxy_log and ('verifications','vh2-fail') in proxy_log and ('verifications','vh2-retry') in proxy_log)
  require('fixture_multicursor_coverage_complete',coverage_starts>=1 and proxy_state['coverage_u2_seen']==coverage_starts+int(proxy_state.get('t5_coverage_failed',False)),proxy_log)
  attributed=[row for row in proxy_log if row[0]=='coverage_attempt']
  require('T5_attempt_first_next_retry', bool(attributed) and all(row[2]=='11111111-2222-4333-8444-555555555555' for row in attributed) and any(attributed[i][1]==attributed[i+1][1]=='u2' for i in range(len(attributed)-1)),attributed)
  require('fixture_proxy_no_handler_failures',not proxy_errors,proxy_errors[:6])

  # TASK-003-04 R9 first-commit identity oracle: the harness page mounts the
  # ACTUAL ReaderScreen/SourcePanel with a minimally SCRIPTED client carrying
  # deterministic seeded markers — a labelled presentation fixture, never a
  # real source observation. A parent CommitProbe (public useLayoutEffect)
  # snapshots both mount areas on the FIRST commit of each new prop
  # selection, strictly before any passive reset effect could run.
  first_commit=js(('''async (page) => {
   const context=await page.context().browser().newContext({viewport:{width:1280,height:900}});
   const t=await context.newPage();
   const facts={scope:'labelled scripted client; deterministic seeded presentation markers; NOT a real source observation'};
   const errors=[];t.on('pageerror',e=>errors.push(String(e)));
   await t.goto(HARNESS);
   // Initial version A settles: metadata provenance marker, reader panel
   // open with the page-1 service marker, direct panel with the r1 marker.
   await t.getByText('MARK-META-A',{exact:false}).first().waitFor({timeout:20000});
   await t.getByRole('button',{name:'근거 패널',exact:true}).click();
   await t.waitForFunction(()=>(document.getElementById('reader-mount')||{textContent:''}).textContent.includes('tsetA1'),{timeout:20000});
   await t.waitForFunction(()=>(document.getElementById('panel-mount')||{textContent:''}).textContent.includes('tsetR1'),{timeout:20000});
   const waitForTag=async tag=>{
    await t.waitForFunction(tag=>(window.__commitLog||[]).some(e=>e.tag===tag),tag,{timeout:8000});
    return await t.evaluate(tag=>(window.__commitLog||[]).find(e=>e.tag===tag),tag);
   };
   // Same-document version A→B: FIRST commit must carry B's identity without
   // A's metadata provenance marker AND without A's service query markers.
   await t.evaluate(()=>window.__armCommit('toB'));
   await t.evaluate(()=>window.__select({versionId:'ver-B'}));
   const toB=await waitForTag('toB');
   facts.toBMetaAbsent=!toB.reader.includes('MARK-META-A');
   facts.toBQueryAbsent=!toB.reader.includes('tsetA1')&&!toB.reader.includes('AQMARKA11');
   facts.toBHeaderCurrent=toB.reader.includes('ver-B');
   facts.toBNoStaleSequence=!toB.reader.includes('버전 #1');
   await t.waitForFunction(()=>(document.getElementById('reader-mount')||{textContent:''}).textContent.includes('MARK-META-B'),{timeout:20000});
   await t.waitForFunction(()=>(document.getElementById('reader-mount')||{textContent:''}).textContent.includes('tsetB1'),{timeout:20000});
   facts.toBSettled=true;
   // Page first→second under the same version: FIRST commit must not keep
   // the first page's service query markers.
   await t.evaluate(()=>window.__armCommit('page2'));
   await t.evaluate(()=>window.__select({page:'nested/second.html'}));
   const page2=await waitForTag('page2');
   facts.page2QueryAbsent=!page2.reader.includes('tsetB1')&&!page2.reader.includes('AQMARKB1');
   await t.waitForFunction(()=>(document.getElementById('reader-mount')||{textContent:''}).textContent.includes('tsetB2'),{timeout:20000});
   facts.page2Settled=true;
   // Directly controlled SourcePanel: region switch, then same-page context
   // switch — each FIRST commit must not keep the previous service markers.
   await t.evaluate(()=>window.__armCommit('region2'));
   await t.evaluate(()=>window.__selectPanel({regionId:'r2'}));
   const region2=await waitForTag('region2');
   facts.region2QueryAbsent=!region2.panel.includes('tsetR1')&&!region2.panel.includes('AQMARKR1');
   await t.waitForFunction(()=>(document.getElementById('panel-mount')||{textContent:''}).textContent.includes('tsetR2'),{timeout:20000});
   facts.region2Settled=true;
   await t.evaluate(()=>window.__armCommit('ctx2'));
   await t.evaluate(()=>window.__selectPanel({contextKey:'ctx-2'}));
   const ctx2=await waitForTag('ctx2');
   facts.ctx2QueryAbsent=!ctx2.panel.includes('tsetR2')&&!ctx2.panel.includes('AQMARKR2');
   await t.waitForFunction(()=>(document.getElementById('panel-mount')||{textContent:''}).textContent.includes('tsetR2'),{timeout:20000});
   facts.ctx2Settled=true;
   facts.pageErrors=errors;
   await t.close();
   return facts;
  }''').replace('HARNESS',json.dumps(seeded_url+'/harness/harness.html?mode=committest')))
  proof['first_commit']=first_commit

  # TASK-003-04 R10 REAL caller-identity oracle: the ACTUAL production App
  # with a REAL HTTP client/controller against this labelled proxy + real
  # daemon, driven through the actual router/sidebar navigation. Proofs are
  # labelled REAL caller identity — never native or source verification.
  # ALL R9+R10 booleans are collected BEFORE any r9/r10 require so an old-
  # source failure in one oracle cannot prevent the other's proof.
  first_commit_real=js(('''async (page) => {
   const context=await page.context().browser().newContext({viewport:{width:1280,height:900}});
   // Trusted TOP frame + exact fixture origin only: generated frames never
   // receive the disposable bearer from test automation.
   await context.addInitScript(([t,o])=>{if(window===top&&location.origin===o)localStorage.setItem('haeryong.owner-session.v1',t)},TOKEN);
   const t=await context.newPage();
   const facts={scope:'REAL production App + REAL HTTP client through labelled proxy + real daemon; caller identity only'};
   const errors=[];t.on('pageerror',e=>errors.push(String(e)));
   const scopes=[];
   t.on('request',r=>{const u=r.url();if(u.includes('/assessments'))scopes.push(u);});
   const scopeCount=()=>scopes.length;
   await t.goto(HARNESS_REAL);
   await t.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'근거 첫 페이지',exact:true}).waitFor({timeout:30000});
   await t.getByRole('button',{name:'근거 패널',exact:true}).click();
   await t.locator('section[aria-label="근거 및 출처 패널"]').waitFor({timeout:10000});
   // Select the SAME-NAMED region 'overview' on the first page through the
   // actual authored-page channel API.
   const frame=()=>t.frames().find(f=>f!==t.mainFrame()&&f.url().includes('first.html'));
   await frame().evaluate(()=>window.__haeryong.select('overview'));
   await t.waitForFunction(()=>(document.querySelector('section[aria-label="근거 및 출처 패널"] h2')||{textContent:''}).textContent.includes('영역 overview'),{timeout:15000});
   facts.realRegionSelectedFirstPage=true;
   // Arm the parent CommitProbe, then use the REAL sidebar link: the actual
   // navigate() path that bypasses mirror() — the exact defect route.
   const beforeSidebar=scopeCount();
   await t.evaluate(()=>window.__armReal('sidebar-second'));
   await t.locator('dialog[aria-label="페이지 및 파일 목록"]').getByRole('link',{name:'nested/second.html',exact:true}).click();
   await t.waitForFunction(tag=>(window.__realCommitLog||[]).some(e=>e.tag===tag),'sidebar-second',{timeout:10000});
   const sidebar=await t.evaluate(tag=>(window.__realCommitLog||[]).find(e=>e.tag===tag),'sidebar-second');
   facts.realSidebarFirstCommitRegionCleared=!sidebar.panelH2.includes('영역 overview');
   // FIRST new assessments query scope after the click: page-only for the
   // new page — the previously selected region must not leak into it.
   for(let i=0;i<100&&scopeCount()===beforeSidebar;i++)await t.waitForTimeout(100);
   facts.realSidebarFirstQueryScopes=scopes.slice(beforeSidebar).slice(0,2);
   const firstScope=facts.realSidebarFirstQueryScopes[0]||'';
   facts.realSidebarFirstQueryPageOnly=firstScope.includes('scope_kind=page')&&firstScope.includes('page_path=nested%2Fsecond.html')&&!firstScope.includes('region_id');
   await t.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'둘째 페이지',exact:true}).waitFor({timeout:30000});
   facts.realSidebarSettled=true;
   // Page-less same-document B version through a NEW host context (actual
   // router mechanics): first commit must not fall back to the old page and
   // the first B assessment query must be version-scoped for the new version.
   const beforeB=scopeCount();
   await t.evaluate(()=>window.__armReal('pageless-B'));
   await t.evaluate(url=>{history.pushState({haeryongReaderContext:crypto.randomUUID()},'',url);window.dispatchEvent(new PopStateEvent('popstate'));},B_URL);
   await t.waitForFunction(tag=>(window.__realCommitLog||[]).some(e=>e.tag===tag),'pageless-B',{timeout:10000});
   const pageless=await t.evaluate(tag=>(window.__realCommitLog||[]).find(e=>e.tag===tag),'pageless-B');
   facts.realPagelessBNoOldPageFallback=!pageless.panelH2.includes('.html');
   facts.realPagelessBPanelPageless=pageless.panelH2.includes('페이지 없음');
   for(let i=0;i<100&&scopeCount()===beforeB;i++)await t.waitForTimeout(100);
   facts.realPagelessBFirstQueryScopes=scopes.slice(beforeB).slice(0,2);
   const bScope=facts.realPagelessBFirstQueryScopes[0]||'';
   facts.realPagelessBFirstQueryVersionScope=bScope.includes('/versions/'+B_VERSION+'/assessments')&&!bScope.includes('scope_kind')&&!bScope.includes('page_path')&&!bScope.includes('region_id');
   await t.waitForFunction(()=>!document.body.textContent.includes('페이지를 여는 중…'),{timeout:30000}).catch(()=>undefined);
   facts.realPagelessBSettled=true;
   // Drain the proxy's deliberately delayed assessment response before the
   // context closes, so no owned request is abandoned mid-flight.
   await t.waitForTimeout(3500);
   facts.pageErrors=errors;
   await t.close();
   return facts;
  }''').replace('TOKEN',json.dumps([token,seeded_url])).replace('HARNESS_REAL',json.dumps(seeded_url+f'/harness/harness.html?mode=real&doc={document}&ver={version}')).replace('B_URL',json.dumps(seeded_url+f'/documents/{document}/versions/{version_b}')).replace('B_VERSION',json.dumps(version_b)))
  proof['first_commit_real']=first_commit_real
  require('r9_tob_metadata_absent_first_commit',first_commit.get('toBMetaAbsent') and first_commit.get('toBHeaderCurrent') and first_commit.get('toBNoStaleSequence'),first_commit)
  require('r9_tob_service_query_absent_first_commit',first_commit.get('toBQueryAbsent'),first_commit)
  require('r9_tob_settles_new_selection',first_commit.get('toBSettled'),first_commit)
  require('r9_page_switch_query_absent_first_commit',first_commit.get('page2QueryAbsent') and first_commit.get('page2Settled'),first_commit)
  require('r9_region_switch_query_absent_first_commit',first_commit.get('region2QueryAbsent') and first_commit.get('region2Settled'),first_commit)
  require('r9_context_switch_query_absent_first_commit',first_commit.get('ctx2QueryAbsent') and first_commit.get('ctx2Settled'),first_commit)
  require('r9_harness_no_page_errors',not first_commit.get('pageErrors'),first_commit.get('pageErrors'))
  require('r10_real_sidebar_first_commit_region_cleared',first_commit_real.get('realRegionSelectedFirstPage') and first_commit_real.get('realSidebarFirstCommitRegionCleared'),first_commit_real)
  require('r10_real_sidebar_first_query_page_only',first_commit_real.get('realSidebarFirstQueryPageOnly'),first_commit_real)
  require('r10_real_sidebar_settles',first_commit_real.get('realSidebarSettled'),first_commit_real)
  require('r10_real_pageless_b_no_old_page_fallback',first_commit_real.get('realPagelessBPanelPageless') and first_commit_real.get('realPagelessBNoOldPageFallback'),first_commit_real)
  require('r10_real_pageless_b_first_query_version_scope',first_commit_real.get('realPagelessBFirstQueryVersionScope'),first_commit_real)
  require('r10_real_no_page_errors',not first_commit_real.get('pageErrors'),first_commit_real.get('pageErrors'))
  proof['seeded_proxy_log']=proxy_log
  proof['seeded_proxy_errors']=proxy_errors
  proof['seeded_backend']=seeded_url
 finally:
  metadata_stop.set();assert metadata_done.wait(3),'owned metadata hold did not stop'
  seeded_server.shutdown();seeded_server.server_close()
 proof['browser']=js('async page=>await page.evaluate(()=>navigator.userAgent)')
 assert fingerprints()==before,'Go/embed inputs changed during observations; do not attribute to stable candidate'
 proof['input_sha256']=hashlib.sha256(json.dumps(before,sort_keys=True).encode()).hexdigest()
 proof['complete']=True
finally:
 if opened:call(['close']);proof['browser_closed']=True
 if product is not None:
  if product.process is not None:product.stop()
  proof['daemon_closed']=product.process is None
 shutil.rmtree(socket_dir)
 RECEIPT.write_text(json.dumps(proof,ensure_ascii=False,indent=2)+'\n')
assert proof['complete'] and proof['browser_closed'] and proof['daemon_closed']
shutil.rmtree(work)
print(json.dumps({'complete':True,'cases':len(proof['cases']),'scope':proof['scope'],'browser_closed':True,'daemon_closed':True}))
