"""Actual Reader failure/recovery regression against a private tagged daemon.

No native qualification. Lease metadata shortening, one renewal 503 and held
HTTP replies are explicit test controls; authority, commits, handoff, cookies,
Reader code and stored operation receipts remain real. Never print credentials.
"""
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
import urllib.parse

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--browser-cli', type=Path, required=True)
parser.add_argument('--receipt', type=Path, required=True)
args = parser.parse_args()
REPO = Path(__file__).resolve().parents[5]
CLI = args.browser_cli.resolve(strict=True)
RECEIPT = args.receipt.resolve()
assert RECEIPT.parent.is_dir(), 'receipt parent must exist'
volume = Path('/Volumes/RootKernel')
BASE = volume / 'tmp' if os.path.ismount(volume) and os.access(volume, os.W_OK) else Path(tempfile.gettempdir())
os.umask(0o077)
spec = importlib.util.spec_from_file_location('harness', REPO / 'tests/e2e/harness.py')
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)

SCENARIOS = {
    'consumed-handoff': r"""async page=>{
   const keys=[],handoffs=[];let renewals=0;
   page.on('request',r=>{
     const path=r.url().split('?')[0];
     if(r.method()==='POST'&&path.endsWith('/render-sessions'))keys.push(r.headers()['idempotency-key']);
     if(r.method()==='POST'&&path.replace(/^https?:\/\/[^/]+/,'')==='/__haeryong/handoff')handoffs.push(decodeURIComponent((String(r.postData()).split('&').find(field=>field.startsWith('secret='))||'secret=').slice(7).replace(/\+/g,' ')));
   });
   await page.route('**/api/v1/versions/*/render-sessions',async route=>{
     const response=await route.fetch(),body=await response.json();
     if(keys.length===1&&body.data)body.data.lease_expires_at=new Date(Date.now()+1500).toISOString();
     await route.fulfill({response,json:body});
   });
   await page.route('**/api/v1/versions/*/render-sessions/*/renew',async route=>{
     renewals++;
     if(renewals===2){await route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{code:'TRANSIENT_BACKEND',retryable:true}})});return;}
     await route.continue();
   });
   await page.goto(ROUTE);
   await page.frameLocator('iframe').locator('#heading').waitFor();
   await page.getByRole('button',{name:'다시 열기',exact:true}).waitFor();
   if(handoffs.length!==1)throw Error('first handoff was not actually consumed');
   await page.getByRole('button',{name:'다시 열기',exact:true}).click();
   for(let i=0;i<50&&keys.length<2;i++)await page.waitForTimeout(100);
   if(keys.length!==2)throw Error('explicit reopen did not issue a new open after consumed handoff');
   await page.frameLocator('iframe').locator('#heading').waitFor();
   await page.waitForFunction(()=>!document.body.textContent.includes('페이지를 여는 중…'));
   if(keys[0]===keys[1]||handoffs.length!==2||handoffs[0]===handoffs[1])throw Error('explicit reopen reused confirmed key/one-use handoff');
   return {reopen:true,newConfirmedKey:true,newOneUseHandoff:true,ready:true,renewFaults:1,scope:'real authenticated daemon/handoff with first lease timestamp shortened in response and exactly one injected renewal 503; no native qualification'};
 }""",
    'initial-renew-close': r"""async page=>{
   const keys=[],handoffs=[],statuses=[];let renewals=0,closeSeen=false,closeAck=false,release;
   const barrier=new Promise(resolve=>release=resolve);
   page.on('request',r=>{const path=r.url().split('?')[0];if(r.method()==='POST'&&path.endsWith('/render-sessions'))keys.push(r.headers()['idempotency-key']);if(r.method()==='POST'&&path.replace(/^https?:\/\/[^/]+/,'')==='/__haeryong/handoff')handoffs.push(true);});
   await page.route('**/api/v1/versions/*/render-sessions/*/renew',async route=>{
     renewals++;
     if(renewals===1){await route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{code:'TRANSIENT_BACKEND',retryable:true}})});return;}
     const response=await route.fetch();statuses.push(response.status());await route.fulfill({response});
   });
   await page.route('**/api/v1/versions/*/render-sessions/*/close',async route=>{
     closeSeen=true;await barrier;
     const response=await route.fetch();if(response.status()!==204)throw Error('known close not confirmed');
     closeAck=true;await route.fulfill({response});
   });
   await page.goto(ROUTE);
   await page.getByRole('button',{name:'다시 열기',exact:true}).waitFor();
   if(keys.length!==1||handoffs.length!==0)throw Error('initial renewal fault did not precede handoff');
   await page.getByRole('button',{name:'다시 열기',exact:true}).click();
   for(let i=0;i<50&&!closeSeen;i++)await page.waitForTimeout(20);
   if(!closeSeen)throw Error('known S close was not attempted');
   await page.waitForTimeout(200);
   if(keys.length!==1||closeAck)throw Error('new Open raced the unconfirmed old Close');
   release();
   await page.frameLocator('iframe').locator('#heading').waitFor();
   await page.waitForFunction(()=>!document.body.textContent.includes('페이지를 여는 중…'));
   if(keys.length!==2||keys[0]===keys[1]||handoffs.length!==1)throw Error('known-S retirement did not obtain a fresh K after Close');
   return {initialRenew503:true,heldClosePrecededNewOpen:true,knownOriginalKRetired:true,newKAfterConfirmedClose:true,oneFreshHandoff:true,ready:true};
 }""",
    'unknown-open': r"""async page=>{
   const keys=[];let handoffs=0;
   page.on('request',r=>{const path=r.url().split('?')[0];if(r.method()==='POST'&&path.endsWith('/render-sessions'))keys.push(r.headers()['idempotency-key']);if(r.method()==='POST'&&path.replace(/^https?:\/\/[^/]+/,'')==='/__haeryong/handoff')handoffs++;});
   await page.route('**/api/v1/versions/*/render-sessions',async route=>{
     const response=await route.fetch();
     if(keys.length===1){if(response.status()!==201)throw Error('first open not committed');await route.abort('failed');return;}
     await route.fulfill({response});
   });
   await page.goto(ROUTE);
   await page.getByRole('button',{name:'다시 열기',exact:true}).waitFor();
   if(handoffs!==0)throw Error('unknown open had already consumed a handoff');
   await page.getByRole('button',{name:'다시 열기',exact:true}).click();
   await page.frameLocator('iframe').locator('#heading').waitFor();
   await page.waitForFunction(()=>!document.body.textContent.includes('페이지를 여는 중…'));
   if(keys.length!==2||keys[0]!==keys[1]||handoffs!==1)throw Error('unknown Open did not recover the original key');
   return {openReplyLostAfterCommit:true,originalKRecovered:true,oneHandoff:true,ready:true};
 }""",
    'inactive-position': r"""async page=>{
   const writes=[];let firstHeld=false,release;
   const barrier=new Promise(resolve=>release=resolve);
   await page.route('**/api/v1/versions/*/page-position?*',async route=>{
     if(route.request().method()!=='PUT'){await route.continue();return;}
     const req=route.request();writes.push({url:req.url(),key:req.headers()['idempotency-key'],match:req.headers()['if-match']??null,create:req.headers()['if-none-match']??null,body:req.postData()});
     const response=await route.fetch();
     if(writes.length===1){firstHeld=true;await barrier;}
     try{await route.fulfill({response});}catch{/* The original document was deliberately reloaded before delivery. */}
   });
   await page.goto(ROUTE);
   await page.frameLocator('iframe').locator('#heading').waitFor();
   await page.waitForFunction(()=>!document.body.textContent.includes('페이지를 여는 중…'));
   await page.frames().find(f=>f!==page.mainFrame()).evaluate(()=>scrollTo(0,500));
   for(let i=0;i<80&&!firstHeld;i++)await page.waitForTimeout(100);
   if(!firstHeld)throw Error('original page position did not reach server before held reply');
   const before=await page.evaluate(()=>Object.keys(localStorage).filter(k=>k.startsWith('haeryong.reader.personal.v1:')).flatMap(k=>JSON.parse(localStorage.getItem(k)).entries).find(e=>e.target.kind==='page-position'&&e.target.page==='first.html'));
   if(!before?.pending||before.issue)throw Error('pre-reply journal state was not a passive pending record');
   await page.frameLocator('iframe').locator('#next').click();
   await page.waitForFunction(()=>new URL(location.href).searchParams.get('page')==='nested/둘째.html');
   await page.reload();release();
   await page.frameLocator('iframe').getByRole('heading',{name:'둘째 페이지',exact:true}).waitFor();
   const recovery=page.locator('.banner').filter({hasText:'first.html'});
   await recovery.getByRole('button',{name:'원래 요청으로 다시 확인',exact:true}).waitFor();
   await recovery.getByRole('button',{name:'원래 요청으로 다시 확인',exact:true}).click();
   for(let i=0;i<50&&writes.length<2;i++)await page.waitForTimeout(100);
   if(writes.length!==2||JSON.stringify(writes[0])!==JSON.stringify(writes[1]))throw Error('inactive page recovery changed original key/body/conditions/target');
   await recovery.waitFor({state:'detached'});
   return {heldReplyAfterCommit:true,reloadedOnDifferentPage:true,originalRetryOffered:true,exactOriginalKeyBodyConditionsTarget:true,ready:true};
 }""",
}

# These scenarios traverse the actual host history while the ORIGINAL real
# server response is held. Only the explicitly labelled response faults are
# synthetic; Open/Close commits, stored K, handoff and successor remain real.
for variant in ['held-open', 'held-first-renew', 'lost-open']:
    SCENARIOS['history-' + variant] = r'''async page=>{
      const variant=VARIANT, keys=[], sessions=[], closes=[];
      let committed=false,release,firstRenew=true;
      const barrier=new Promise(resolve=>release=resolve);
      page.on('request',r=>{if(r.method()==='POST'&&r.url().split('?')[0].endsWith('/render-sessions'))keys.push(r.headers()['idempotency-key']);});
      await page.route('**/api/v1/versions/*/render-sessions',async route=>{
        const response=await route.fetch(),body=await response.json();
        if(body.data)sessions.push(body.data.session_id);
        if(keys.length===1&&variant!=='held-first-renew'){
          if(response.status()!==201)throw Error('original Open did not commit');
          committed=true;await barrier;
          if(variant==='lost-open'){await route.abort('failed');return;}
        }
        await route.fulfill({response});
      });
      if(variant==='held-first-renew')await page.route('**/api/v1/versions/*/render-sessions/*/renew',async route=>{
        if(!firstRenew){await route.continue();return;}firstRenew=false;
        const response=await route.fetch();if(response.status()!==200)throw Error('first Renew was not real');
        committed=true;await barrier;
        await route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{code:'TRANSIENT_BACKEND',retryable:true}})});
      });
      await page.route('**/api/v1/versions/*/render-sessions/*/close',async route=>{
        const id=route.request().url().split('?')[0].split('/').at(-2);
        const response=await route.fetch();if(response.status()!==204)throw Error('captured Close did not acknowledge');
        closes.push(id);await route.fulfill({response});
      });
      await page.goto(ROUTE);
      for(let i=0;i<100&&!committed;i++)await page.waitForTimeout(25);
      if(!committed)throw Error('held stage did not reach actual commit');
      const originalURL=page.url(),historyLength=await page.evaluate(()=>history.length);
      await page.getByRole('link',{name:'홈',exact:true}).click();await page.goBack();
      await page.getByRole('button',{name:'화면 전체 새로고침',exact:true}).waitFor();
      await page.waitForTimeout(200);
      if(keys.length!==1)throw Error('successor dispatched before the retirement barrier');
      release();
      await page.frameLocator('iframe').locator('#heading').waitFor();
      if(keys.length!==2||page.url()!==originalURL||await page.evaluate(()=>history.length)!==historyLength+1)throw Error('successor readiness/history mismatch');
      if(variant==='lost-open'){
        if(keys[0]!==keys[1]||closes.length!==0||sessions[0]!==sessions[1])throw Error('unknown Open lost original recovery identity');
      }else if(keys[0]===keys[1]||sessions[0]===sessions[1]||JSON.stringify(closes)!==JSON.stringify([sessions[0]]))throw Error('old cleanup reached successor or reused retired identity');
      return {ready:true,variant,actualOriginalCommit:true,successorWaited:true,correctKeyRecovery:true,oldCleanupCannotReachSuccessor:true,exactURL:true,noExtraHistory:true};
    }'''.replace('VARIANT', json.dumps(variant))

SCENARIOS['lost-close-manual-retry'] = r'''async page=>{
  const keys=[],closed=[];let failRenew=true,lostAck=false;
  page.on('request',r=>{if(r.method()==='POST'&&r.url().split('?')[0].endsWith('/render-sessions'))keys.push(r.headers()['idempotency-key']);});
  await page.route('**/api/v1/versions/*/render-sessions/*/renew',async route=>{
    if(failRenew){failRenew=false;await route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{code:'TRANSIENT_BACKEND',retryable:true}})});return;}
    await route.continue();
  });
  await page.route('**/api/v1/versions/*/render-sessions/*/close',async route=>{
    closed.push(route.request().url().split('?')[0].split('/').at(-2));
    const response=await route.fetch();if(response.status()!==204)throw Error('actual Close not committed');
    if(!lostAck){lostAck=true;await route.abort('failed');return;}
    await route.fulfill({response});
  });
  await page.goto(ROUTE);
  await page.getByRole('button',{name:'다시 열기',exact:true}).click();
  await page.getByRole('alert').filter({hasText:'이전 페이지 연결의 종료를 확인하지 못했습니다'}).waitFor();
  if(keys.length!==1)throw Error('unknown Close allowed a premature Open');
  await page.getByRole('button',{name:'다시 열기',exact:true}).click();
  await page.frameLocator('iframe').locator('#heading').waitFor();
  if(closed.length!==2||closed[0]!==closed[1]||keys.length!==2||keys[0]===keys[1])throw Error('manual retry changed captured Close or reused retired K');
  return {ready:true,actualCloseCommittedBeforeLostAck:true,visibleManualRetry:true,exactCapturedCloseRetried:true,freshSuccessor:true};
}'''

SCENARIOS['independently-dead-receipt'] = r'''async page=>{
  const keys=[];let independentlyClosed=false;
  page.on('request',r=>{if(r.method()==='POST'&&r.url().split('?')[0].endsWith('/render-sessions'))keys.push(r.headers()['idempotency-key']);});
  await page.route('**/api/v1/versions/*/render-sessions',async route=>{
    const response=await route.fetch(),body=await response.json();
    if(keys.length===1){
      const token=await page.evaluate(()=>localStorage.getItem('haeryong.owner-session.v1'));
      const url=route.request().url()+'/'+body.data.session_id+'/close';
      const close=await page.request.post(url,{headers:{Authorization:'Bearer '+token,'Content-Type':'application/json'},data:{}});
      if(close.status()!==204)throw Error('independent test actor failed to close the committed S');
      independentlyClosed=true;await route.abort('failed');return;
    }
    await route.fulfill({response});
  });
  await page.goto(ROUTE);await page.getByRole('button',{name:'다시 열기',exact:true}).click();
  await page.frameLocator('iframe').locator('#heading').waitFor();
  if(!independentlyClosed||keys.length!==3||keys[0]!==keys[1]||keys[1]===keys[2])throw Error('actual dead receipt recovery did not retain K before discovering dead S');
  return {ready:true,independentActualClose:true,lostOpenReply:true,originalKRecovered:true,deadReceiptDiscovered:true,freshKAfterDeadReceipt:true};
}'''


def fingerprints():
    paths = [p for directory in ['cmd', 'internal'] for p in (REPO / directory).rglob('*.go') if not p.name.endswith('_test.go')]
    paths += [REPO / 'go.mod', REPO / 'go.sum']
    paths += [p for p in (REPO / 'internal/webassets/dist').rglob('*') if p.is_file()]
    return {str(p.relative_to(REPO)): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(paths)}


def run_case(name, script, binary, expected):
    work = Path(tempfile.mkdtemp(prefix='h3r-', dir=BASE))
    env = os.environ | {'TMPDIR': str(work), 'HAERYONG_TEST_STATE_DIR': str(work / 'app-state'), 'PYTHONDONTWRITEBYTECODE': '1'}
    os.environ['HAERYONG_TEST_STATE_DIR'] = env['HAERYONG_TEST_STATE_DIR']
    session = work.name
    product = None
    attempted = False
    result = {'name': name, 'complete': False, 'runtime': str(work)}

    def browser(arguments, read=False):
        completed = subprocess.run([str(CLI), '--session=' + session, *arguments], cwd=work, env=env, capture_output=True, text=True, timeout=90)
        if completed.returncode != 0 or '### Error' in completed.stdout:
            result['browser_error'] = completed.stdout.split('### Error', 1)[-1].split('###', 1)[0][:1800]
            raise AssertionError('Browser failure; see private bounded receipt')
        if read:
            marker = re.search(r'### Result\s*\n', completed.stdout)
            assert marker, 'browser did not return its result'
            return json.JSONDecoder().raw_decode(completed.stdout[marker.end():].lstrip())[0]

    try:
        product = harness.Product(binary, work)
        product.initialize()
        product.start()
        owner = product.owner('Reader recovery regression')
        source = work / 'source'
        (source / 'nested').mkdir(parents=True)
        (source / 'first.html').write_text('<!doctype html><meta charset=utf-8><h1 id=heading>첫 페이지</h1><a id=next href="nested/둘째.html?scenario=2#part">next</a><div style="height:2600px"></div><h2 id=part>끝</h2>')
        (source / 'nested/둘째.html').write_text('<!doctype html><meta charset=utf-8><h1>둘째 페이지</h1><div style="height:2600px"></div><h2 id=part>끝</h2>')
        _, _, published = product.import_tree(owner, source)
        route = product.endpoint + '/documents/' + published['document_id'] + '/versions/' + published['version_id'] + '?' + urllib.parse.urlencode({'page': 'first.html', 'page_query': 'scenario=1'})
        attempted = True
        browser(['open', product.endpoint + '/health/live', '--browser', 'chrome', '--profile', str(work / 'profile')])
        browser(['run-code', "async page=>{await page.evaluate(token=>localStorage.setItem('haeryong.owner-session.v1',token)," + json.dumps(owner['token']) + ");return true;}"])
        result['actual'] = browser(['run-code', script.replace('ROUTE', json.dumps(route))], read=True)
        assert result['actual']['ready'] is True
        assert fingerprints() == expected, 'Go/embed inputs changed during observations'
        result['complete'] = True
        return result
    finally:
        if attempted:
            browser(['close'])
            result['browser_closed'] = True
        if product is not None:
            if product.process is not None:
                product.stop()
            result['daemon_closed'] = product.process is None
        proof['cases'].append(result)
        RECEIPT.write_text(json.dumps(proof, ensure_ascii=False, indent=2) + '\n')
        if result['complete']:
            shutil.rmtree(work)


proof = {'complete': False, 'scope': __doc__, 'cases': [], 'driver_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
build = Path(tempfile.mkdtemp(prefix='h3b-', dir=BASE))
try:
    expected = fingerprints()
    subprocess.run(['go', 'build', '-tags', 'haeryong_test_state', '-o', str(build / 'haeryong'), './cmd/haeryong'], cwd=REPO, capture_output=True, check=True, timeout=90)
    assert fingerprints() == expected, 'Go/embed inputs changed during build'
    for name, script in SCENARIOS.items():
        run_case(name, script, build / 'haeryong', expected)
    assert len(proof['cases']) == len(SCENARIOS) and all(case['complete'] and case['browser_closed'] and case['daemon_closed'] for case in proof['cases'])
    proof['input_sha256'] = hashlib.sha256(json.dumps(expected, sort_keys=True).encode()).hexdigest()
    proof['complete'] = True
finally:
    RECEIPT.write_text(json.dumps(proof, ensure_ascii=False, indent=2) + '\n')
    if proof['complete']:
        shutil.rmtree(build)
print(json.dumps({'complete': True, 'cases': len(proof['cases']), 'native_qualification': False}))
