"""TASK-001-03 local browser proof. Private HTTPS and physical clients require separate evidence. Never print credentials/codes."""
import json
import os
from pathlib import Path
import re
import signal
import socket
import subprocess
import tempfile
import uuid
from urllib.parse import urlparse

import argparse
import shutil
import sys

parser = argparse.ArgumentParser(description="Run the disposable local auth browser proof; no physical-device claim.")
parser.add_argument("--fixture-binary", type=Path, required=True)
parser.add_argument("--browser-cli", type=Path, required=True)
options = parser.parse_args()
root = Path("/Volumes/RootKernel")
temporary_root = root / "tmp" if os.path.ismount(root) and os.access(root, os.W_OK) else Path(tempfile.gettempdir())
runtime = Path(tempfile.mkdtemp(prefix="hy-auth-", dir=temporary_root))
binary = options.fixture_binary.resolve(strict=True)
browser_directory = runtime / "browser"
browser_directory.mkdir(mode=0o700)
session = "h" + uuid.uuid4().hex[:8]
browser = [str(options.browser_cli.resolve(strict=True)), "-s=" + session]
env = os.environ | {"TMPDIR": str(browser_directory),
                    "PLAYWRIGHT_DAEMON_SESSION_DIR": str(browser_directory / "registry"),
                    "PLAYWRIGHT_DAEMON_SOCKETS_DIR": str(browser_directory / "sockets")}
server = subprocess.Popen([str(binary), 'serve', '--directory', str(runtime)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
browser_open = False

def cli(args, result=False):
    checked = subprocess.run(browser + args, cwd=runtime, env=env, text=True, capture_output=True, timeout=45)
    if checked.returncode or '### Error' in checked.stdout:
        detail = checked.stdout.split('### Error', 1)[-1] if '### Error' in checked.stdout else checked.stderr
        detail = re.sub(r'(?i)[0-9a-f]{8,}', '[redacted]', '\n'.join(detail.strip().splitlines()[:2]))
        raise RuntimeError('Browser preparation step failed (codes suppressed): ' + detail)
    if result:
        match = re.search(r'### Result\s*\n', checked.stdout)
        if not match:
            raise RuntimeError('Browser preparation returned no result')
        return json.JSONDecoder().raw_decode(checked.stdout[match.end():].lstrip())[0]

def control_absent(data):
    control = (runtime / 'control').read_text().strip()
    assert control and control not in data, 'Owned process output or endpoint exposed control credential'

def announcement():
    line = server.stdout.readline()
    control_absent(line)
    return json.loads(line)

def stop_server():
    if server.poll() is None:
        server.send_signal(signal.SIGTERM)
    code = server.wait(timeout=10)
    control_absent(server.stdout.read())
    control_absent(server.stderr.read())
    assert code == 0, 'Owned fixture failed to shut down cleanly; output suppressed'

try:
    announced = announcement()
    assert announced['fixture'] == 'auth-ingress' and announced['pid'] == server.pid
    endpoint = announced['endpoint']
    control_absent((runtime / 'endpoint').read_text())
    assert (runtime / 'control').stat().st_mode & 0o777 == 0o600
    cli(['open', 'about:blank', '--profile', str(browser_directory / 'profile')])
    browser_open = True
    request = cli(['run-code', '''async (page) => {
      const idle=await page.context().newPage();await idle.goto(BASE);await page.bringToFront();
      await page.goto(BASE + '/reader?return=https://unrelated.example/');
      await page.locator('#label').fill('Same-label fixture');
      await page.getByRole('button', {name:'Connect device',exact:true}).click();
      await page.waitForFunction(() => document.querySelector('#state').textContent === 'Waiting for owner approval');
      await page.evaluate(()=>{
        window.fixtureInitialSecret=pending.Secret;window.fixturePendingID=pending.ID;
        if(document.documentElement.outerHTML.includes(pending.Secret)||location.href.includes(pending.Secret))throw new Error('pending secret leaked into document or URL');
        document.querySelector('#connect').requestSubmit();document.querySelector('#connect').requestSubmit();
      });
      if(!await page.evaluate(()=>pending.ID===window.fixturePendingID&&document.querySelector('#connect button').disabled))throw new Error('Connect replaced active comparison request');
      if(!await page.evaluate(async()=>!(await(await fetch('/probe-child')).text()).includes(pending.Secret)))throw new Error('served child contained polling secret');
      await page.evaluate(()=>clearTimeout(timer));
      return page.evaluate(() => ({ID:pending.ID,Code:pending.Code,CreatedText:new Date(pending.Created/1000000).toISOString()}));
    }'''.replace('BASE', json.dumps(endpoint))], result=True)
    approved = subprocess.run([str(binary),'approve','--directory',str(runtime)], input=json.dumps(request), capture_output=True, text=True, timeout=10)
    control_absent(approved.stdout);control_absent(approved.stderr)
    assert approved.returncode == 0, 'protected bootstrap approval failed'
    initial_created_text = request['CreatedText']
    del request
    proof = cli(['run-code', '''async (page) => {
      const base=BASE;
      const initialCreated=INITIAL_TIME;
      const key='haeryong.auth-ingress.v1';
      let sessionAttempt=0,deviceAttempt=0;
      let redemptions=0;
      await page.route('**/pair/redeem',async route=>{if(++redemptions===1){await route.fulfill({status:503,headers:{'Retry-After':'2'},body:'Controlled store unavailable before issuance'});return;}await route.continue();});
      await page.route('**/session',async route=>{if(++sessionAttempt===1){await route.fulfill({status:503,body:'Controlled post-issuance session failure'});return;}await route.continue();});
      await page.route('**/devices',async route=>{if(++deviceAttempt===1){await route.fulfill({status:503,body:'Controlled device-list failure'});return;}await route.continue();});
      await page.evaluate(()=>{window.fixturePollTimes=[];});
      let attempt=0;
      await page.route('**/pair/poll',async route=>{
        await page.evaluate(()=>window.fixturePollTimes.push(performance.now()));
        attempt++;
        if(attempt===1){await page.waitForTimeout(6500);try{await route.abort('timedout');}catch{}return;}
        if(attempt===2){await route.fulfill({status:429,headers:{'Retry-After':'3'},body:'Controlled polling throttle'});return;}
        await route.continue();
      });
      await page.evaluate(()=>schedulePair(pending));
      await page.waitForFunction(()=>document.querySelector('#state').textContent.includes('credential retained'),null,{timeout:20000});
      if(!await page.evaluate(key=>localStorage.getItem(key)===bearer&&bearer.length===64&&document.querySelector('#connect button').disabled,key))throw new Error('post-issuance failure lost credential or allowed duplicate pairing');
      await page.getByRole('button',{name:'Retry connection',exact:true}).click();
      await page.waitForFunction(() => document.querySelector('#state').textContent === 'Remembered connection',null,{timeout:20000});
      await page.waitForFunction(()=>document.querySelector('#result').textContent.includes('Device list unavailable'));
      if(!await page.evaluate(()=>watcher!==null&&!document.querySelector('#owner').hidden))throw new Error('device-list failure disabled connection monitoring');
      await page.getByRole('button',{name:'Refresh devices',exact:true}).click();
      await page.waitForFunction(()=>document.querySelector('#result').textContent==='Device list refreshed');
      await page.unroute('**/session');await page.unroute('**/devices');
      const idle=page.context().pages().find(tab=>tab!==page);
      await idle.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');
      if(!await idle.evaluate(()=>pending===null&&bearer.length===64))throw new Error('idle tab did not adopt remembered connection');
      if(await idle.evaluate(()=>deviceID)!==await page.evaluate(()=>deviceID))throw new Error('idle tab created another device');
      await idle.close();
      const times=await page.evaluate(()=>window.fixturePollTimes);
      if(times.length!==4||times[1]-times[0]<6900||times[2]-times[1]<2900||times[3]-times[2]<1900||redemptions!==2)throw new Error('poll timeout/throttle/store-failure recovery did not recheck before issuance');
      await page.unroute('**/pair/poll');await page.unroute('**/pair/redeem');
      if (await page.evaluate(() => location.pathname) !== '/') throw new Error('unsafe return was accepted');
      if (!await page.evaluate(key => (localStorage.getItem(key)||'').length===64,key)) throw new Error('remembered bearer missing');
      if(!await page.evaluate(()=>![bearer,window.fixtureInitialSecret].some(secret=>document.documentElement.outerHTML.includes(secret)||location.href.includes(secret))))throw new Error('credential leaked into host document or URL');
      await page.getByRole('button',{name:'Read private fixture',exact:true}).click();
      await page.waitForFunction(() => document.querySelector('#private').textContent === 'synthetic private value');
      await page.reload();
      await page.waitForFunction(() => document.querySelector('#state').textContent === 'Remembered connection');
      await page.goto(base+'/reader?return='+encodeURIComponent('/private/data'));
      await page.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');
      if(await page.evaluate(()=>location.pathname)!=='/')throw new Error('same-origin disallowed return accepted');
      const tab=await page.context().newPage();await tab.goto(base);
      await tab.waitForFunction(() => document.querySelector('#state').textContent === 'Remembered connection');await tab.close();
      const secondContext=await page.context().browser().newContext();
      try {
        const second=await secondContext.newPage();await second.goto(base+'/?device-check=1');
        await second.locator('#label').fill('Same-label fixture');
        await second.getByRole('button',{name:'Connect device',exact:true}).click();
        await second.waitForFunction(() => document.querySelector('#state').textContent === 'Waiting for owner approval');
        const pendingRequest=await second.evaluate(() => ({ID:pending.ID,Code:pending.Code,CreatedText:new Date(pending.Created/1000000).toISOString()}));
        if(pendingRequest.CreatedText===initialCreated)throw new Error('same-label requests lost distinct creation context');
        if(!await second.evaluate(()=>document.querySelector('#comparison').textContent.includes('created ')&&document.querySelector('#comparison').textContent.includes('reload or close drops this request secret')))throw new Error('pending request lacks creation context or reload warning');
        await page.locator('#request-id').fill(pendingRequest.ID);await page.locator('#code').fill((pendingRequest.Code[0]==='0'?'1':'0')+pendingRequest.Code.slice(1));
        await page.getByRole('button',{name:'Approve device',exact:true}).click();
        await page.waitForFunction(()=>document.querySelector('#decision-result').textContent.includes('HTTP 403: decision denied'));
        await page.locator('#code').fill(pendingRequest.Code);
        let approvals=0,revocations=0;
        await page.route('**/pair/decision',async route=>{approvals++;if(approvals<=2){await route.fulfill({status:approvals===1?503:429,headers:{'Retry-After':'2'},body:'Controlled retryable decision response'});return;}await page.waitForTimeout(300);await route.continue();});
        await page.getByRole('button',{name:'Approve device',exact:true}).click({clickCount:2});
        await page.waitForFunction(()=>document.querySelector('#decision-result').textContent.includes('HTTP 503')&&document.querySelector('#decision-result').textContent.includes('retrying this same exact request'));
        if(await page.locator('#decision-result').textContent().then(text=>text.includes('may already be final')))throw new Error('retryable decision described as final');
        await page.getByRole('button',{name:'Approve device',exact:true}).click({clickCount:2});
        await page.waitForFunction(()=>document.querySelector('#decision-result').textContent.includes('HTTP 429')&&document.querySelector('#decision-result').textContent.includes('wait 2 seconds'));
        let secondRedeems=0;
        await second.route('**/pair/redeem',async route=>{if(++secondRedeems===1){await route.fulfill({status:503,headers:{'Retry-After':'2'},body:'Controlled pre-issuance store failure'});return;}await route.continue();});
        await page.getByRole('button',{name:'Approve device',exact:true}).click({clickCount:2});
        await page.waitForFunction(()=>document.querySelector('#decision-result').textContent.startsWith('Exact request approved:'));
        const approvedText=await page.locator('#decision-result').textContent();
        if(!approvedText.includes('Same-label fixture (created '+pendingRequest.CreatedText+')')||!approvedText.includes('after it finishes connecting'))throw new Error('approval confirmation lost exact timestamp or next step');
        await second.waitForFunction(()=>document.querySelector('#state').textContent.includes('approved request retained'));
        await page.getByRole('button',{name:'Refresh devices',exact:true}).click();
        await page.waitForFunction(()=>document.querySelector('#result').textContent==='Device list refreshed');
        if(await page.locator('#decision-result').textContent()!==approvedText)throw new Error('refresh erased approval before peer finished redemption');
        await second.waitForFunction(() => document.querySelector('#device-check').textContent === 'Device check passed. Observation stored in the local fixture.');
        await second.waitForFunction(() => document.querySelector('#state').textContent === 'Remembered connection');
        if(approvals!==3)throw new Error('duplicate approval submitted');
        await page.waitForFunction(()=>document.querySelector('#decision-result').textContent.startsWith('Exact request approved:'));
        await page.unroute('**/pair/decision');
        await second.unroute('**/pair/redeem');
        const original=await page.evaluate(()=>bearer),replacement=await second.evaluate(()=>bearer);
        await page.evaluate(()=>clearInterval(watcher));
        await page.route('**/private/data',async route=>{
          await page.evaluate(async token=>{bearer=token;localStorage.setItem('haeryong.auth-ingress.v1',token);await connected('Remembered connection');},replacement);
          await route.fulfill({status:401,body:'Controlled late denial for previous credential'});
        });
        await page.evaluate(()=>{window.lateDenial=api('/private/data',undefined,true).catch(error=>error.stale===true);});
        if(!await page.evaluate(async token=>(await window.lateDenial)===true&&bearer===token&&localStorage.getItem('haeryong.auth-ingress.v1')===token&&!document.querySelector('#owner').hidden,replacement))throw new Error('old denial cleared replacement credential');
        await page.unroute('**/private/data');
        await page.evaluate(async token=>{bearer=token;localStorage.setItem('haeryong.auth-ingress.v1',token);await connected('Remembered connection');},original);
        await page.evaluate(()=>{window.fixtureOriginalApi=api;window.fixtureOldWatcherError=null;api=async(...args)=>{try{return await window.fixtureOriginalApi(...args);}catch(error){if(args[0]==='/session'&&error.status===401)window.fixtureOldWatcherError={stale:!!error.stale};throw error;}};});
        let storedReplacement=false;
        await page.route('**/session',async route=>{if(!storedReplacement){storedReplacement=true;await page.evaluate(token=>localStorage.setItem('haeryong.auth-ingress.v1',token),replacement);await route.fulfill({status:401,body:'Controlled denied old in-memory credential with new stored credential'});return;}await route.continue();});
        await page.waitForFunction(token=>window.fixtureOldWatcherError?.stale&&bearer===token&&document.querySelector('#state').textContent==='Remembered connection',replacement);
        if(!await page.evaluate(()=>!document.querySelector('#connection').textContent.includes('401')&&!document.querySelector('#owner').hidden))throw new Error('old watcher denial painted outage after stored replacement restored');
        await page.unroute('**/session');await page.evaluate(()=>{api=window.fixtureOriginalApi;});
        await page.evaluate(async token=>{bearer=token;localStorage.setItem('haeryong.auth-ingress.v1',token);await connected('Remembered connection');},original);
        await second.getByRole('button',{name:'Read private fixture',exact:true}).click();
        await second.waitForFunction(() => document.querySelector('#private').textContent === 'synthetic private value');
        await second.evaluate(async()=>{const response=await fetch('/private/events',{headers:{Authorization:'Bearer '+bearer}});if(response.status!==200)throw new Error('private stream denied');window.preparationReader=response.body.getReader();await window.preparationReader.read();});
        await page.getByRole('button',{name:'Refresh devices',exact:true}).click();
        let revokeRefresh=0;
        await page.route('**/devices',async route=>{if(++revokeRefresh===1){await route.fulfill({status:503,body:'Controlled list failure after confirmed revoke'});return;}await route.continue();});
        await page.route('**/devices/revoke',async route=>{revocations++;await page.waitForTimeout(300);await route.continue();});
        const secondID=await second.evaluate(()=>deviceID);
        await page.locator('#devices button[data-device-id="'+secondID+'"]').click({clickCount:2});
        await second.evaluate(async()=>{let timer;try{await Promise.race([(async()=>{while(!(await window.preparationReader.read()).done){}})(),new Promise((_,reject)=>{timer=setTimeout(()=>reject(new Error('server did not close revoked stream')),1500);})]);}finally{clearTimeout(timer);}});
        await second.waitForFunction(() => document.querySelector('#owner').hidden && document.querySelector('#private').textContent === '',null,{timeout:6000});
        if(!await second.evaluate(key=>localStorage.getItem(key)===null,key))throw new Error('revoked bearer retained in storage');
        if(revocations!==1)throw new Error('duplicate revocation submitted');
        await page.waitForFunction(()=>document.querySelector('#result').textContent.startsWith('Device revoked. Device list unavailable:'));
        if(!await page.locator('#devices button[data-device-id="'+secondID+'"]').isDisabled())throw new Error('confirmed revoked control reenabled after list failure');
        await page.getByRole('button',{name:'Refresh devices',exact:true}).click();
        await page.waitForFunction(()=>document.querySelector('#result').textContent==='Device list refreshed');
        await page.unroute('**/devices/revoke');await page.unroute('**/devices');
      } finally {await secondContext.close();}
      return {decision_retryable_copy:true,decision_refresh_preserves_confirmation:true,same_label_exact_creation_times:true,stored_replacement_avoids_false_outage:true,store_unavailable_rechecks_approved_state:true,idle_tab_adopts_remembered_device:true,stale_denial_preserves_new_credential:true,confirmed_revocation_survives_list_failure:true,decision_denial_body:true,approval_metadata_and_guidance:true,pending_reload_warning:true,pending_connect_not_replaced:true,post_issuance_retry_retains_credential:true,device_list_retry_preserves_watcher:true,duplicate_approval_guard:true,duplicate_revoke_guard:true,host_document_url_excludes_secrets:true,same_origin_disallowed_return:true,poll_timeout_recovery:true,poll_retry_after:true,automatic_device_reload_report:true,bootstrap:true,owner_second_approval:true,remembered_reload:true,new_tab:true,other_device_revocation:true,private_view_cleared:true,stream_closed_after_revocation:true,safe_return:true};
    }'''.replace('BASE',json.dumps(endpoint)).replace('INITIAL_TIME',json.dumps(initial_created_text))], result=True)
    cli(['close']);browser_open=False
    cli(['open','about:blank','--profile',str(browser_directory/'profile')]);browser_open=True
    restart = cli(['run-code','''async(page)=>{await page.goto(BASE);await page.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');return {browser_restart:true};}'''.replace('BASE',json.dumps(endpoint))],result=True)
    proof.update(restart)
    parsed = urlparse(endpoint)
    with socket.create_connection((parsed.hostname, parsed.port), timeout=2):
        stop_server()
    proof['idle_preconnect_shutdown'] = True
    server=subprocess.Popen([str(binary),'serve','--directory',str(runtime),'--listen',urlparse(endpoint).netloc],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    restarted=announcement()
    assert restarted['endpoint']==endpoint and restarted['pid']==server.pid
    further=cli(['run-code','''async(page)=>{
      const base=BASE,key='haeryong.auth-ingress.v1';
      await page.reload();await page.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');
      const context=await page.context().browser().newContext();
      try{
        const memory=await context.newPage();await memory.goto(base);
        let memorySessionAttempts=0;
        await memory.route('**/session',async route=>{if(++memorySessionAttempts===1){await route.fulfill({status:503,body:'Controlled memory-only session failure'});return;}await route.continue();});
        await memory.locator('#label').fill('Memory fixture');await memory.locator('#remember').uncheck();
        await memory.getByRole('button',{name:'Connect device',exact:true}).click();
        await memory.waitForFunction(()=>document.querySelector('#state').textContent==='Waiting for owner approval');
        const request=await memory.evaluate(()=>({ID:pending.ID,Code:pending.Code}));
        await page.locator('#request-id').fill(request.ID);await page.locator('#code').fill(request.Code);await page.getByRole('button',{name:'Approve device',exact:true}).click();
        await memory.waitForFunction(()=>document.querySelector('#state').textContent.includes('credential retained'));
        await memory.getByRole('button',{name:'Retry connection',exact:true}).click();
        await memory.waitForFunction(()=>document.querySelector('#state').textContent==='Connected in memory only. Reload or close requires pairing again.');
        await memory.unroute('**/session');
        if(!await memory.evaluate(key=>localStorage.getItem(key)===null,key))throw new Error('memory-only device persisted a bearer');
        await memory.reload();await memory.waitForFunction(()=>document.querySelector('#state').textContent==='Disconnected' && document.querySelector('#owner').hidden);
        await page.getByRole('button',{name:'Refresh devices',exact:true}).click();await page.getByRole('button',{name:/^Revoke Memory fixture — /}).click();
        const expires=await context.newPage();await expires.goto(base);
        await expires.locator('#label').fill('Expiring fixture');await expires.locator('#expiry').fill('2');
        await expires.getByRole('button',{name:'Connect device',exact:true}).click();
        await expires.waitForFunction(()=>document.querySelector('#state').textContent==='Waiting for owner approval');
        const next=await expires.evaluate(()=>({ID:pending.ID,Code:pending.Code}));
        await page.locator('#request-id').fill(next.ID);await page.locator('#code').fill(next.Code);await page.getByRole('button',{name:'Approve device',exact:true}).click();
        await expires.waitForFunction(()=>document.querySelector('#state').textContent==='Connected and remembered');
        await expires.getByRole('button',{name:'Read private fixture',exact:true}).click();
        await expires.waitForFunction(()=>document.querySelector('#private').textContent==='synthetic private value');
        await expires.waitForFunction(()=>document.querySelector('#owner').hidden && document.querySelector('#private').textContent==='',null,{timeout:6000});
        if(!await expires.evaluate(key=>localStorage.getItem(key)===null,key))throw new Error('expired device retained stored bearer');
        await page.getByRole('button',{name:'Refresh devices',exact:true}).click();
        if(!(await page.locator('#devices li').filter({hasText:'Expiring fixture'}).textContent()).includes('(expired)'))throw new Error('owner inventory still labels expired device active');
      }finally{await context.close();}
      return {ordinary_server_restart:true,memory_only_retry_truthful:true,memory_only_reload:true,optional_expiry:true,expired_device_inventory:true};
    }'''.replace('BASE',json.dumps(endpoint))],result=True)
    proof.update(further)
    # Restart the disposable process to reset only its creation-rate window;
    # the same database and owner session must survive.
    stop_server()
    server = subprocess.Popen([str(binary), 'serve', '--directory', str(runtime), '--listen', urlparse(endpoint).netloc], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    restarted = announcement()
    assert restarted['endpoint'] == endpoint and restarted['pid'] == server.pid
    recovery_request = cli(['run-code', '''async(page) => {
      const base=BASE,key='haeryong.auth-ingress.v1';
      await page.goto(base+'/reader');
      await page.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');
      if(await page.evaluate(()=>location.pathname)!=='/reader')throw new Error('internal route was lost on remembered connection');
      const deniedResponses=['/pair/decision','/devices/revoke'].map(path=>page.waitForResponse(response=>response.url()===base+path&&response.request().frame().url()===base+'/probe-child'));
      const childBody=await page.evaluate(async()=>{const text=await(await fetch('/probe-child')).text();return !text.includes(bearer);});
      if(!childBody)throw new Error('served child contained owner credential');
      await page.evaluate(()=>{const frame=document.createElement('iframe');frame.id='untrusted-fixture';frame.setAttribute('sandbox','allow-scripts');frame.src='/probe-child';document.body.append(frame);});
      const child=await page.locator('#untrusted-fixture').elementHandle();
      const frame=await child.contentFrame();
      await frame.waitForFunction(()=>Object.keys(window.fixtureBoundary||{}).length===4);
      const isolation=await frame.evaluate(()=>window.fixtureBoundary);
      if(Object.values(isolation).some(Boolean))throw new Error('opaque frame reached host credential storage/context');
      if((await Promise.all(deniedResponses)).some(response=>response.status()!==403))throw new Error('served child reached management mutation');
      await page.locator('#untrusted-fixture').evaluate(frame=>frame.remove());
      const context=await page.context().browser().newContext();
      try{
        await context.addInitScript(()=>{Storage.prototype.setItem=function(){throw new DOMException('Controlled storage write failure','QuotaExceededError');};});
        const memory=await context.newPage();await memory.goto(base);
        let failedStorageSessions=0;
        await memory.route('**/session',async route=>{if(++failedStorageSessions===1){await route.fulfill({status:503,body:'Controlled session failure after storage write failure'});return;}await route.continue();});
        await memory.locator('#label').fill('Unavailable storage fixture');
        await memory.getByRole('button',{name:'Connect device',exact:true}).click();
        await memory.waitForFunction(()=>document.querySelector('#state').textContent==='Waiting for owner approval');
        const request=await memory.evaluate(()=>({ID:pending.ID,Code:pending.Code}));
        await page.locator('#request-id').fill(request.ID);await page.locator('#code').fill(request.Code);
        await page.getByRole('button',{name:'Approve device',exact:true}).click();
        await memory.waitForFunction(()=>document.querySelector('#state').textContent.includes('credential retained'));
        await memory.getByRole('button',{name:'Retry connection',exact:true}).click();
        await memory.waitForFunction(()=>document.querySelector('#state').textContent==='Connected in memory only: storage unavailable. Reload or close requires pairing again.');
        await memory.unroute('**/session');
        await memory.getByRole('button',{name:'Read private fixture',exact:true}).click();
        await memory.waitForFunction(()=>document.querySelector('#private').textContent==='synthetic private value');
        if(!await memory.evaluate(key=>localStorage.getItem(key)===null,key))throw new Error('failed storage write persisted bearer');
        await memory.reload();await memory.waitForFunction(()=>document.querySelector('#owner').hidden && document.querySelector('#state').textContent==='Disconnected');
      }finally{await context.close();}
      await page.evaluate(key=>localStorage.removeItem(key),key);
      await page.goto(base+'/reader?return='+encodeURIComponent('/reader#reconnected'));
      await page.waitForFunction(()=>document.querySelector('#owner').hidden && document.querySelector('#state').textContent==='Disconnected');
      await page.getByRole('button',{name:'Connect device',exact:true}).click();
      await page.waitForFunction(()=>document.querySelector('#state').textContent==='Waiting for owner approval');
      return page.evaluate(()=>({ID:pending.ID,Code:pending.Code}));
    }'''.replace('BASE',json.dumps(endpoint))],result=True)
    approved = subprocess.run([str(binary),'approve','--directory',str(runtime)], input=json.dumps(recovery_request), capture_output=True, text=True, timeout=10)
    control_absent(approved.stdout);control_absent(approved.stderr)
    assert approved.returncode == 0, 'storage-loss recovery bootstrap denied'
    del recovery_request
    extra = cli(['run-code', '''async(page)=>{
      await page.waitForFunction(()=>document.querySelector('#state').textContent==='Connected and remembered');
      if(await page.evaluate(()=>location.pathname)!=='/reader' || await page.evaluate(()=>location.hash)!=='#reconnected')throw new Error('safe storage-loss return was lost');
      await page.getByRole('button',{name:'Read private fixture',exact:true}).click();
      await page.waitForFunction(()=>document.querySelector('#private').textContent==='synthetic private value');
      await page.goto(await page.evaluate(()=>location.origin+'/connect?device-check=1'));
      await page.waitForFunction(()=>document.querySelector('#device-check').textContent==='Remembered access checked. Reload check has not run on this visit.');
      await page.getByRole('button',{name:'Check remembered reload',exact:true}).click();
      await page.waitForFunction(()=>document.querySelector('#device-check').textContent==='Device check passed. Observation stored in the local fixture.');
      await page.reload();
      await page.waitForFunction(()=>document.querySelector('#device-check').textContent==='Remembered access checked. Reload check has not run on this visit.');
      return {storage_write_failure_retry_truthful:true,storage_write_failure_memory_fallback:true,storage_loss_reconnect:true,safe_internal_return:true,opaque_frame_host_storage_isolation:true,served_child_credential_absence:true,served_child_management_denied:true,device_report_phase_honesty:true,explicit_remembered_reload_check:true};
    }'''],result=True)
    proof.update(extra)
    stop_server()
    server = subprocess.Popen([str(binary), 'serve', '--directory', str(runtime), '--listen', urlparse(endpoint).netloc], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    restarted = announcement()
    assert restarted['endpoint'] == endpoint and restarted['pid'] == server.pid
    negative = cli(['run-code', '''async(page)=>{
      const base=BASE;
      await page.reload();await page.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');
      const context=await page.context().browser().newContext();
      try{
        const peer=await context.newPage();await peer.goto(base);
        await peer.evaluate(()=>{window.fixtureExpiryPolls=0;let hidden=true;Object.defineProperty(document,'hidden',{configurable:true,get:()=>hidden});window.fixtureShow=()=>{hidden=false;document.dispatchEvent(new Event('visibilitychange'));};});
        await peer.route('**/pair/poll',async route=>{await peer.evaluate(()=>window.fixtureExpiryPolls++);await route.fulfill({status:403,body:'Controlled unavailable request'});});
        await peer.getByRole('button',{name:'Connect device',exact:true}).click();
        await peer.waitForFunction(()=>document.querySelector('#state').textContent==='Waiting for owner approval');
        await peer.waitForTimeout(2250);
        if(await peer.evaluate(()=>window.fixtureExpiryPolls)!==0)throw new Error('hidden document kept polling');
        await peer.evaluate(()=>window.fixtureShow());
        await peer.waitForFunction(()=>document.querySelector('#state').textContent==='Pairing request expired or unavailable. Connect device again.',null,{timeout:6000});
        if(!await peer.evaluate(()=>pending===null&&document.querySelector('#comparison').textContent===''))throw new Error('unavailable request remained active');
        await peer.unroute('**/pair/poll');await peer.evaluate(()=>{delete document.hidden;window.fixtureRedeemCalls=0;});
        await peer.route('**/pair/redeem',async route=>{await peer.evaluate(()=>window.fixtureRedeemCalls++);await route.fetch();await route.abort('failed');});
        await peer.getByRole('button',{name:'Connect device',exact:true}).click();
        await peer.waitForFunction(()=>document.querySelector('#state').textContent==='Waiting for owner approval');
        const request=await peer.evaluate(()=>({ID:pending.ID,Code:pending.Code}));
        await page.locator('#request-id').fill(request.ID);await page.locator('#code').fill(request.Code);
        await page.getByRole('button',{name:'Approve device',exact:true}).click();
        await peer.waitForFunction(()=>document.querySelector('#state').textContent.includes('redemption is not repeated automatically'));
        await peer.waitForTimeout(4500);
        if(!await peer.evaluate(()=>window.fixtureRedeemCalls===1&&pending===null&&localStorage.getItem('haeryong.auth-ingress.v1')===null))throw new Error('lost redemption was replayed or persisted');
        return {controlled_visibility_resume:true,unavailable_request_actionable:true,lost_redemption_not_replayed:true};
      }finally{await context.close();}
    }'''.replace('BASE',json.dumps(endpoint))],result=True)
    proof.update(negative)
    proof.update({'actual_control_credential_output_absent':True,'formal_task':'TASK-001-03; local-browser slice only','private_https':'not_run','application_restore_browser':'not_run','fixture_runtime':str(runtime)})
    (runtime/'browser-result.json').write_text(json.dumps(proof,indent=2))
    print(json.dumps(proof))
finally:
    cleanup_errors = []
    if browser_open:
        try:
            cli(['close'])
        except Exception as error:
            cleanup_errors.append(error)
    try:
        stop_server()
    except (subprocess.TimeoutExpired, AssertionError) as error:
        cleanup_errors.append(error)
    if not cleanup_errors:
        shutil.rmtree(browser_directory)
    else:
        (runtime / 'cleanup-recovery.json').write_text(json.dumps({'browser_directory':str(browser_directory),'browser_session':session,'server_pid':server.pid,'cleanup':'incomplete'}))
        if sys.exc_info()[1] is None:
            raise ExceptionGroup('auth proof cleanup failed', cleanup_errors)
