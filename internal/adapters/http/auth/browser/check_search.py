"""SC17/18 Chrome Library search: production import/query/exact Reader open, keyboard and phone touch; explicitly seeded presentation/fault responses exercise pending/reasons/access-loss and request races."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import time
import urllib.parse

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--browser-cli', type=Path, required=True)
parser.add_argument('--receipt', type=Path, required=True)
parser.add_argument('--corpus', action='store_true', help='SC19 complete checked-in Mulgae/Gaori search-and-open examples')
args = parser.parse_args()
REPO = Path(__file__).resolve().parents[5]
receipt = args.receipt.resolve()
assert receipt.parent.is_dir()
os.umask(0o077)
work = Path(tempfile.mkdtemp(prefix='h4s-', dir=receipt.parent)).resolve()
sockets = Path(tempfile.mkdtemp(prefix='hs-', dir='/tmp')).resolve()
env = os.environ | {'TMPDIR': str(work), 'HAERYONG_TEST_STATE_DIR': str(work/'app-state'), 'PYTHONDONTWRITEBYTECODE':'1', 'PLAYWRIGHT_DAEMON_SOCKETS_DIR': str(sockets), 'PLAYWRIGHT_DAEMON_SESSION_DIR': str(work/'registry')}
os.environ['HAERYONG_TEST_STATE_DIR'] = env['HAERYONG_TEST_STATE_DIR']
spec = importlib.util.spec_from_file_location('h', REPO/'tests/e2e/harness.py')
h = importlib.util.module_from_spec(spec); spec.loader.exec_module(h)
proof = {'scope': __doc__, 'runtime': str(work), 'complete': False, 'cases': [], 'driver_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
product = None; opened = False
cli = str(args.browser_cli.resolve(strict=True)); session = work.name

def fingerprints():
    paths = [p for folder in ['cmd','internal'] for p in (REPO/folder).rglob('*.go') if not p.name.endswith('_test.go')]
    paths += [REPO/'go.mod', REPO/'go.sum'] + [p for p in (REPO/'internal/webassets/dist').rglob('*') if p.is_file()]
    return {str(p.relative_to(REPO)): hashlib.sha256(p.read_bytes()).hexdigest() for p in paths}

def call(arguments, result=False):
    run = subprocess.run([cli, '--session='+session, *arguments], cwd=work, env=env, capture_output=True, text=True, timeout=180)
    if run.returncode or '### Error' in run.stdout:
        diagnostic = run.stdout.split('### Error',1)[-1] if '### Error' in run.stdout else run.stderr
        secret = globals().get('owner',{}).get('token','')
        proof['browser_error'] = diagnostic.replace(secret,'[redacted]')[:1800] if secret else diagnostic[:1800]
        raise AssertionError('browser operation failed; see private receipt')
    if result:
        start = re.search(r'### Result\s*\n', run.stdout); assert start
        return json.JSONDecoder().raw_decode(run.stdout[start.end():].lstrip())[0]

def js(code): return call(['run-code', code], True)
def record(name, detail):
    assert detail is True or isinstance(detail,dict), name
    proof['cases'].append({'name':name,'ok':True,'detail':detail})

before = fingerprints()
try:
    subprocess.run(['go','build','-tags','haeryong_test_state','-o',str(work/'haeryong'),'./cmd/haeryong'],cwd=REPO,env=env,capture_output=True,check=True,timeout=180)
    assert before == fingerprints(), 'inputs changed during isolated build'
    product = h.Product(work/'haeryong',work); product.initialize(); product.start(); owner=product.owner('Library search regression')
    if args.corpus:
        support_spec=importlib.util.spec_from_file_location('sc19_support',REPO/'tests/manual/search_corpus_support.py')
        support=importlib.util.module_from_spec(support_spec);support_spec.loader.exec_module(support)
        examples=support.prepare(product,owner)
        proof['corpus']=examples
        proof['corpus_support_sha256']=hashlib.sha256((REPO/'tests/manual/search_corpus_support.py').read_bytes()).hexdigest()
        opened=True
        call(['open',product.endpoint+'/health/live','--browser','chrome','--profile',str(work/'profile')])
        js('async page=>{await page.evaluate(token=>localStorage.setItem("haeryong.owner-session.v1",token),'+json.dumps(owner['token'])+');return true;}')
        for example in examples:
            data=example|{'base':product.endpoint,'screenshot':str(work/('corpus-'+example['name']+'.png'))}
            record('SC19 '+example['name']+' actual Chrome exact search-and-open',js(r"""async page=>{
              const e=EXAMPLE;
              await page.goto(e.base+'/library');await page.getByRole('search').waitFor({timeout:20000});
              await page.getByLabel('프로젝트',{exact:true}).selectOption(e.project);
              await page.getByLabel('검색 종류',{exact:true}).selectOption('region');
              await page.getByLabel('검색어',{exact:true}).fill(e.q);await page.getByLabel('검색어',{exact:true}).press('Enter');
              const hit=page.getByTestId('search-hit').filter({hasText:'영역 '+e.region+' ·'}).first();await hit.waitFor({timeout:20000});
              const anchor=hit.getByRole('link',{name:'일치하는 페이지 열기',exact:true});
              const target=await page.evaluate(href=>{const u=new URL(href,location.origin);return {path:u.pathname,page:u.searchParams.get('page')};},await anchor.getAttribute('href'));
              if(target.path!==`/documents/${e.document}/versions/${e.version}`||target.page!==e.page)throw Error('wrong search identity');
              await anchor.click();const frame=page.frameLocator('iframe[title="결과 뷰어"]');
              await frame.getByRole('heading',{name:e.heading,exact:true}).waitFor({timeout:20000});
              const actual=await frame.locator('script[data-haeryong-page]').evaluate(script=>({...script.dataset}));
              if(actual.haeryongDocument!==e.document||actual.haeryongVersion!==e.version||decodeURIComponent(actual.haeryongPage)!==e.page)throw Error('actual rendered page identity mismatch');
              await page.screenshot({path:e.screenshot});return {project:e.name,heading:e.heading,exact_document_version_page:true,loader_identity:actual};
            }""".replace('EXAMPLE',json.dumps(data,ensure_ascii=False))))
    else:
        source=work/'source'; (source/'nested').mkdir(parents=True)
        exact='nested/검토 %2F #?.html'
        (source/exact).write_text('<!doctype html><meta charset=utf-8><title>정확한 검색 페이지</title><h1>선택적 재시도 검토</h1><p id="retry">readSource 검색표식</p>')
        for n in range(54): (source/f'page{n:02}.html').write_text(f'<meta charset=utf-8><h1>검색표식 {n}</h1>')
        (source/'opaque.unknown').write_bytes(b'opaque attachment')
        _,_,first=product.import_tree(owner,source)
        # Same document, changed latest, so versions=all exact old-version open is observable.
        (source/exact).write_text('<meta charset=utf-8><h1>새 최신 버전</h1>')
        _,_,latest=product.import_tree(owner,source,extra=['--document',first['document_id']])
        other=product.cli(['projects','register','--name','Search B'],client=owner)['data']['result']['project_id']
        second=work/'other'; second.mkdir(); (second/'other.html').write_text('<meta charset=utf-8><h1>다른 프로젝트 검색표식</h1>')
        _,_,other_published=product.import_tree(owner,second,extra=['--project',other])
        deadline=time.monotonic()+120
        while True:
            status, reply, _=product.request('GET','/api/v1/search?q=&versions=all',client=owner)
            assert status in {200,503}, status
            if status==200 and reply['data']['index']['state']=='complete':break
            assert time.monotonic()<deadline,'index did not complete';time.sleep(.25)
        cli_page=product.cli(['search','--query','검색표식','--versions','all','--lifecycle','all'],client=owner)['data']
        http_page=product.data('GET','/api/v1/search?'+urllib.parse.urlencode({'q':'검색표식','versions':'all','lifecycle':'all'}),client=owner)
        assert cli_page==http_page;record('production CLI/HTTP exact result and reason equality',True)
        opened=True
        call(['open',product.endpoint+'/health/live','--browser','chrome','--profile',str(work/'profile')])
        js('async page=>{await page.evaluate(token=>localStorage.setItem("haeryong.owner-session.v1",token),'+json.dumps(owner['token'])+');return true;}')
        constants=json.dumps({'base':product.endpoint,'exact':exact,'version':first['version_id'],'document':first['document_id'],'other':other,'shot':str(work/'desktop.png')},ensure_ascii=False)
        code=r'''async page=>{
          const c=CONSTANTS, facts={}; const errors=[];page.on('pageerror',e=>errors.push(String(e)));page.on('console',m=>{if(m.type()==='error'&&!m.text().includes('Failed to load resource'))errors.push(m.text());});
          await page.goto(c.base+'/library');await page.getByRole('search').waitFor({timeout:20000});
          if(!(await page.title())||!(await page.getByRole('heading',{name:'서재',exact:true}).isVisible()))throw Error('blank/wrong page');
          if(await page.locator('vite-error-overlay').count())throw Error('framework overlay');
          await page.getByLabel('검색어',{exact:true}).fill('검색표식');await page.getByLabel('검색 버전',{exact:true}).selectOption('all');await page.getByLabel('수명 주기',{exact:true}).selectOption('all');
          await page.getByLabel('검색어',{exact:true}).press('Enter');await page.getByTestId('search-hit').first().waitFor({timeout:20000});
          if((await page.evaluate(()=>new URL(location.href).searchParams.get('lifecycle')))!=='all')throw Error('Search All lost');
          await page.getByRole('button',{name:'검색 결과 더 보기',exact:true}).click();
          await page.waitForFunction(()=>document.querySelectorAll('[data-testid="search-hit"]').length>50);
          facts.more=true;
          await page.getByLabel('검색어',{exact:true}).press('Enter');await page.waitForFunction(()=>document.querySelectorAll('[data-testid="search-hit"]').length===50);facts.sameQueryResubmits=true;
          await page.getByLabel('검색어',{exact:true}).fill('readSource');await page.getByLabel('검색어',{exact:true}).press('Enter');
          await page.getByLabel('검색 종류',{exact:true}).selectOption('region');
          const hit=page.getByTestId('search-hit').filter({hasText:'readSource'}).first();await hit.waitFor({timeout:20000});
          await hit.getByText('영역 retry · 앵커 present',{exact:true}).waitFor({timeout:20000});facts.regionAnchorData=true;
          const link=hit.getByRole('link',{name:'일치하는 페이지 열기',exact:true});const href=await link.getAttribute('href');const url=await page.evaluate(href=>{const u=new URL(href,location.origin);return {pathname:u.pathname,page:u.searchParams.get('page'),hash:u.hash};},href);
          if(url.pathname!==`/documents/${c.document}/versions/${c.version}`||url.page!==c.exact||url.hash)throw Error('exact target lost '+href);
          await link.focus();await page.keyboard.press('Enter');await page.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'선택적 재시도 검토',exact:true}).waitFor({timeout:20000});facts.exactOldUnicodePage=true;
          await page.goBack();await page.getByLabel('검색어',{exact:true}).waitFor({timeout:20000});if(await page.getByLabel('검색어',{exact:true}).inputValue()!=='readSource')throw Error('Back query lost');
          await page.getByRole('button',{name:'검색 지우기',exact:true}).click();await page.getByTestId('library-count').waitFor({timeout:20000});if((await page.evaluate(()=>new URL(location.href).searchParams.has('q'))))throw Error('clear failed');
          await page.getByLabel('검색어',{exact:true}).press('Enter');await page.getByText('검색어를 입력하세요.',{exact:true}).waitFor({timeout:20000});facts.emptyClearBack=true;
          await page.getByLabel('검색 종류',{exact:true}).selectOption('attachment');await page.getByLabel('검색 버전',{exact:true}).selectOption('all');
          await page.getByLabel('검색어',{exact:true}).fill('opaque.unknown');await page.getByLabel('검색어',{exact:true}).press('Enter');
          const attachment=page.getByTestId('search-hit').first();await attachment.waitFor({timeout:20000});const fileLink=attachment.getByRole('link',{name:'파일이 포함된 버전 열기',exact:true});
          const target=await fileLink.getAttribute('href');if(target.includes('?')||!target.includes('/versions/'))throw Error('attachment retargeted');
          await fileLink.click();await page.getByRole('button',{name:'opaque.unknown',exact:true}).click();
          await page.getByRole('region',{name:'선택한 파일 내려받기',exact:true}).waitFor({timeout:20000});facts.attachmentUsesExactVersionDownload=true;
          await page.goBack();await page.getByLabel('검색 종류',{exact:true}).selectOption('');
          await page.getByLabel('검색어',{exact:true}).fill('nevermatch-unique');await page.getByLabel('검색어',{exact:true}).press('Enter');await page.getByText('색인된 텍스트에서 일치하는 결과가 없습니다.',{exact:true}).waitFor({timeout:20000});facts.completeZero=true;
          await page.getByLabel('프로젝트',{exact:true}).selectOption(c.other);await page.getByLabel('검색어',{exact:true}).fill('검색표식');await page.getByLabel('검색어',{exact:true}).press('Enter');
          await page.getByTestId('search-hit').first().waitFor({timeout:20000});if((await page.getByTestId('search-hit').count())!==1)throw Error('project filter lost');facts.twoProjects=true;
          await page.getByLabel('검색 종류',{exact:true}).selectOption('page');await page.getByTestId('search-hit').first().waitFor({timeout:20000});
          await page.getByLabel('수명 주기',{exact:true}).selectOption('archived');await page.getByText('선택한 필터에 읽을 수 있는 콘텐츠가 없습니다.',{exact:true}).waitFor({timeout:20000});
          await page.goBack();await page.getByTestId('search-hit').first().waitFor({timeout:20000});
          if(await page.getByLabel('수명 주기',{exact:true}).inputValue()!=='all'||await page.getByLabel('검색 종류',{exact:true}).inputValue()!=='page')throw Error('filter Back lost');facts.archiveKindBack=true;
          await page.screenshot({path:c.shot});if(errors.length)throw Error('console errors '+errors.join('|'));facts.console=true;return facts;
        }'''.replace('CONSTANTS',constants)
        record('production keyboard history filters more exact page and zero states',js(code))
        # No focus, refresh or navigation: a content-free publication/index hint
        # must re-read the current first page through the ordinary authenticated GET.
        (second/'other.html').write_text('<meta charset=utf-8><title>자동 갱신 페이지</title><h1>다른 프로젝트 검색표식</h1>')
        product.import_tree(owner,second,extra=['--project',other,'--document',other_published['document_id']])
        record('production event hint refreshes GET without user action',js("async page=>{await page.getByTestId('search-hit').getByText('자동 갱신 페이지',{exact:true}).waitFor({timeout:30000});return true;}"))
        # N1: labelled visibility replacement and a content-free SSE notice. The
        # live UI accumulates two pages first; no focus, navigation or manual refresh
        # may rescue its old protected rows/cursor when the notice arrives.
        record('N1 accumulated pages revalidate on library.invalidated and clear rows/cursor',js(r"""async page=>{
          const probe=await page.context().newPage(), sample=SEEDED;
          await probe.addInitScript(()=>{
            const original=window.fetch;window.__searchInvalidationStreams=[];
            window.fetch=(input,init)=>{
              if(String(input)==='/api/v1/events')return Promise.resolve(new Response(new ReadableStream({start(controller){window.__searchInvalidationStreams.push(controller);}}),{headers:{'Content-Type':'text/event-stream'}}));
              return original(input,init);
            };
          });
          let hidden=false,requests=[],release;
          await probe.route('**/api/v1/search?**',async route=>{
            const cursor=await probe.evaluate(url=>new URL(url).searchParams.get('cursor'),route.request().url());requests.push(cursor);
            const result=JSON.parse(JSON.stringify(sample));result.index.state='complete';
            if(hidden){await new Promise(resolve=>{release=resolve;});result.items=[];result.total_matches='0';result.next_cursor=null;result.index.versions_total='0';}
            else {result.items=Array.from({length:cursor?5:50},(_,n)=>({...sample.items[0],page_path:'protected-'+(cursor?50+n:n)+'.html',title:'N1 protected title',snippet:'N1 protected snippet'}));result.next_cursor=cursor?'third-page':'second-page';}
            return route.fulfill({status:200,json:{data:result}});
          });
          try{
            await probe.goto(BASE+'/library?q=seed&versions=all');await probe.waitForFunction(()=>document.querySelectorAll('[data-testid="search-hit"]').length===50);
            await probe.getByRole('button',{name:'검색 결과 더 보기',exact:true}).click();await probe.waitForFunction(()=>document.querySelectorAll('[data-testid="search-hit"]').length===55);
            hidden=true;
            await probe.evaluate(()=>{if(!window.__searchInvalidationStreams.length)throw Error('missing SSE subscription');for(const stream of window.__searchInvalidationStreams)stream.enqueue(new TextEncoder().encode('event: library.invalidated\ndata: {}\n\n'));});
            await probe.waitForFunction(()=>document.querySelector('[aria-label="검색 결과"][aria-busy="true"]'),null,{timeout:20000});
            if(await probe.getByTestId('search-hit').count()||await probe.getByRole('button',{name:'검색 결과 더 보기',exact:true}).count())throw Error('N1 protected rows or cursor retained while revalidating');
            if(requests.length!==3||requests[2]!==null||!release)throw Error('N1 invalidation did not GET first page');
            release();await probe.getByText('선택한 필터에 읽을 수 있는 콘텐츠가 없습니다.',{exact:true}).waitFor({timeout:20000});
            if(await probe.getByText('N1 protected title',{exact:true}).count()||await probe.getByText('N1 protected snippet',{exact:true}).count())throw Error('N1 hidden content retained');
            return {accumulated:55,notice:'library.invalidated',freshGET:true,clearedRowsAndCursor:true};
          }finally{if(release)release();await probe.close();}
        }""".replace('SEEDED',json.dumps(http_page,ensure_ascii=False)).replace('BASE',json.dumps(product.endpoint))))
        record('N2 authenticated login return retains accepted query whitespace',js(r"""async page=>{
          for(const whitespace of ['\t','\n','\r']){
            const login=await page.evaluate(({base,whitespace})=>{const target='/library?'+new URLSearchParams({q:'검토'+whitespace+'승인',versions:'all'});return base+'/connect?'+new URLSearchParams({return:target});},{base:BASE,whitespace});
            await page.goto(login);
            await page.waitForFunction(()=>location.pathname==='/library');
            const actual=await page.evaluate(()=>({q:new URL(location.href).searchParams.get('q'),versions:new URL(location.href).searchParams.get('versions')}));
            if(actual.q!=='검토'+whitespace+'승인'||actual.versions!=='all')throw Error('N2 login return changed accepted whitespace');
          }return {loginReturns:3};
        }""".replace('BASE',json.dumps(product.endpoint))))
        # Seeded presentation plus controlled delayed GETs; the real service and SC14
        # independently own authentication/eligibility. Never claim these injected
        # states are production failed-index or real scoped issuance evidence.
        seeded=http_page.copy();seeded['items']=http_page['items'][:1];seeded['next_cursor']='seeded-cursor';seeded['index']=dict(http_page['index'],state='partial',reasons=[{'code':'read_failed','version_count':'1'}],html_failed='2')
        code=r'''async page=>{
          const c=CONSTANTS, sample=SEEDED, facts={};let mode='owner',requests=[],release;
          await page.route('**/api/v1/search?**',async route=>{
            const req=route.request(),p=await page.evaluate(url=>Object.fromEntries(new URL(url).searchParams),req.url());requests.push({method:req.method(),q:p.q,life:p.lifecycle});
            if(p.q==='slow'){await new Promise(r=>{release=r;});}
            if(mode==='loss')return route.fulfill({status:403,json:{error:{code:'FORBIDDEN',retryable:false,details:{}}}});
            if(mode==='disconnect')return route.abort('failed');
            if(mode==='error')return route.fulfill({status:503,json:{error:{code:'SERVICE_RECOVERING',retryable:true,details:{}}}});
            if(mode==='binding'&&Boolean(p.cursor)){mode='partial';return route.fulfill({status:400,json:{error:{code:'INVALID_REQUEST',retryable:false,details:{reason:'cursor_mismatch'}}}});}
            const out=JSON.parse(JSON.stringify(sample));if(mode==='owner')out.index.reasons.push({code:'storage_failed',version_count:'1'});out.query={text:p.q,normalized:p.q||'',mode:'static'};
            out.items=out.items.map(h=>({...h,title:p.q==='slow'?'STALE-RESULT':'<script>literal result</script>'}));
            if(mode==='empty'){out.items=[];out.total_matches='0';out.index.versions_total='0';out.index.state='complete';}
            return route.fulfill({status:200,json:{data:out}});
          });
          await page.goto(c.base+'/library?q=seed&lifecycle=all&versions=all');
          await page.getByText('콘텐츠 읽기 실패 · 1개 버전',{exact:true}).waitFor({timeout:20000});
          await page.getByText('색인 저장 실패 · 1개 버전',{exact:true}).waitFor({timeout:20000});facts.ownerReasons=true;
          mode='partial';await page.getByRole('button',{name:'검색 새로고침',exact:true}).click();await page.getByText('콘텐츠 읽기 실패 · 1개 버전',{exact:true}).waitFor({timeout:20000});
          if(await page.getByText('색인 저장 실패 · 1개 버전',{exact:true}).count())throw Error('hidden reason invented');
          await page.getByText('<script>literal result</script>',{exact:true}).waitFor({timeout:20000});facts.scopedReasonsAndTextNodes=true;
          mode='binding';await page.getByRole('button',{name:'검색 결과 더 보기',exact:true}).click();await page.getByText('검색 대상이 변경되어 첫 페이지부터 다시 표시합니다.',{exact:true}).waitFor({timeout:20000});facts.bindingReset=true;
          await page.getByLabel('검색어',{exact:true}).fill('slow');await page.getByLabel('검색어',{exact:true}).press('Enter');
          await page.waitForFunction(()=>document.querySelector('[aria-label="검색 결과"][aria-busy="true"]'));
          await page.getByLabel('검색어',{exact:true}).fill('new');await page.getByLabel('검색어',{exact:true}).press('Enter');await page.getByText('<script>literal result</script>',{exact:true}).waitFor({timeout:20000});
          if(release)release();await page.waitForTimeout(200);if(await page.getByText('STALE-RESULT',{exact:true}).count())throw Error('stale response populated');facts.rapidQuery=true;
          mode='empty';await page.getByRole('button',{name:'검색 새로고침',exact:true}).click();await page.getByText('선택한 필터에 읽을 수 있는 콘텐츠가 없습니다.',{exact:true}).waitFor({timeout:20000});facts.noCorpus=true;
          mode='error';await page.getByRole('button',{name:'검색 새로고침',exact:true}).click();await page.getByRole('alert').filter({hasText:'503'}).waitFor({timeout:20000});facts.error=true;
          mode='disconnect';await page.getByRole('button',{name:'검색 새로고침',exact:true}).click();await page.getByRole('alert').filter({hasText:'서비스에 연결할 수 없습니다'}).waitFor({timeout:20000});facts.disconnect=true;
          mode='partial';await page.getByRole('button',{name:'검색 새로고침',exact:true}).click();await page.getByTestId('search-hit').first().waitFor({timeout:20000});
          mode='loss';await page.getByRole('button',{name:'검색 결과 더 보기',exact:true}).click();await page.getByRole('alert').filter({hasText:'403'}).waitFor({timeout:20000});if(await page.getByTestId('search-hit').count())throw Error('access loss retained rows');facts.accessLoss=true;
          if(requests.some(r=>r.method!=='GET'))throw Error('search mutated');facts.getOnly=true;
          await page.unroute('**/api/v1/search?**');return facts;
        }'''.replace('CONSTANTS',constants).replace('SEEDED',json.dumps(seeded,ensure_ascii=False))
        record('seeded partial scoped safe reasons binding race no corpus error and access loss',js(code))
        code=r'''async page=>{
          const c=CONSTANTS;const context=await page.context().browser().newContext({viewport:{width:390,height:844},hasTouch:true,isMobile:true});const phone=await context.newPage();
          await phone.goto(c.base+'/health/live');await phone.evaluate(token=>localStorage.setItem('haeryong.owner-session.v1',token),TOKEN);
          await phone.goto(c.base+'/library?q=readSource&versions=all&lifecycle=all');await phone.getByTestId('search-hit').first().waitFor({timeout:20000});
          const clear=phone.getByRole('button',{name:'검색 지우기',exact:true});const box=await clear.boundingBox();if(!box||box.height<44)throw Error('touch target');
          if(await phone.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('phone overflow');
          await phone.screenshot({path:SHOT});await phone.touchscreen.tap(box.x+box.width/2,box.y+box.height/2);await phone.getByTestId('library-count').waitFor({timeout:20000});await context.close();return {viewport:'390x844',touch:true,stacked:true};
        }'''.replace('CONSTANTS',constants).replace('TOKEN',json.dumps(owner['token'])).replace('SHOT',json.dumps(str(work/'phone.png')))
        record('phone stacked touch clear',js(code))
        disabled=product.data('GET','/api/v1/capabilities',client=owner)
        disabled['features']['search']=False
        record('capability false hides search and performs no search GET',js(r"""async page=>{
          let calls=0;await page.route('**/api/v1/capabilities',r=>r.fulfill({status:200,json:{data:CAPS}}));
          const listener=r=>{if(r.url().includes('/api/v1/search?'))calls++;};page.on('request',listener);
          await page.goto(BASE+'/library?q=readSource');await page.getByText('검색은 아직 제공되지 않습니다.',{exact:true}).waitFor({timeout:20000});
          if(await page.getByRole('search').count()||await page.getByTestId('search-hit').count()||calls)throw Error('feature gating bypassed');
          page.off('request',listener);await page.unroute('**/api/v1/capabilities');return true;
        }""".replace('CAPS',json.dumps(disabled)).replace('BASE',json.dumps(product.endpoint))))
    assert fingerprints()==before, 'product inputs changed during browser observations'
    assert hashlib.sha256(Path(__file__).read_bytes()).hexdigest()==proof['driver_sha256'], 'driver changed during observations'
    proof['input_sha256']=hashlib.sha256(json.dumps(before,sort_keys=True).encode()).hexdigest()
    proof['binary_sha256']=hashlib.sha256((work/'haeryong').read_bytes()).hexdigest()
    proof['complete']=True
except Exception:
    if opened:
        try:
            proof['failure_page']=js('async page=>({url:page.url(),text:(await page.locator("body").innerText()).slice(0,5000),frames:page.frames().map(f=>f.url())})')
        except Exception:pass
    raise

finally:
    if opened:
        try:call(['close'])
        except Exception:pass
    if product:product.close()
    receipt.write_text(json.dumps(proof,ensure_ascii=False,indent=2)+'\n');receipt.chmod(0o600)
    shutil.rmtree(sockets,ignore_errors=True)
print(json.dumps({'complete':proof['complete'],'cases':len(proof['cases']),'receipt':str(receipt)}))
