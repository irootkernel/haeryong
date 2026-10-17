"""Actual browser proof of TASK-002-05 settings and production HTTP/auth/SQLite.

Two loopback-origin contexts are synthetic local/remote clients, not private
ingress, physical-device, Wails or complete renderer proof. Never print secrets.
"""
import argparse
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import tempfile
import uuid
from urllib.parse import urlparse

parser = argparse.ArgumentParser()
parser.add_argument('--fixture-binary', type=Path, required=True)
parser.add_argument('--browser-cli', type=Path, required=True)
parser.add_argument('--receipt', type=Path)
args = parser.parse_args()
volume = Path('/Volumes/RootKernel')
base = volume / 'tmp' if os.path.ismount(volume) and os.access(volume, os.W_OK) else Path(tempfile.gettempdir())
runtime = Path(tempfile.mkdtemp(prefix='haeryong-task005-browser-', dir=base))
profile = runtime / 'browser-profile'
session = 'hauth' + uuid.uuid4().hex[:10]
env = os.environ | {'TMPDIR': str(runtime), 'PLAYWRIGHT_DAEMON_SESSION_DIR': str(runtime / 'registry'), 'PLAYWRIGHT_DAEMON_SOCKETS_DIR': str(runtime / 'sockets')}
browser_cli = args.browser_cli.resolve(strict=True)
browser = (['bash'] if browser_cli.suffix == '.sh' else []) + [str(browser_cli), '--session=' + session]
binary = str(args.fixture_binary.resolve(strict=True))
server = None
opened = False

def cli(arguments, result=False):
    value = subprocess.run(browser + arguments, cwd=runtime, env=env, capture_output=True, text=True, timeout=60)
    if value.returncode or '### Error' in value.stdout:
        detail = value.stdout.split('### Error', 1)[-1] if '### Error' in value.stdout else value.stderr
        detail = re.sub(r'(?i)[0-9a-f]{8,}', '[redacted]', '\n'.join(detail.splitlines()[:3]))
        raise RuntimeError('Browser step failed; credentials suppressed: ' + detail)
    if result:
        match = re.search(r'### Result\s*\n', value.stdout)
        if not match:
            raise RuntimeError('Browser step returned no structured result')
        return json.JSONDecoder().raw_decode(value.stdout[match.end():].lstrip())[0]

def script(code):
    return cli(['run-code', code], result=True)

def start(port=0):
    global server
    server = subprocess.Popen([binary, '--directory', str(runtime), '--port', str(port)], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    value = json.loads(server.stdout.readline())
    assert value['fixture'] == 'task00205-production-auth-settings' and value['pid'] == server.pid
    return value

def control(value):
    server.stdin.write(json.dumps(value) + '\n')
    server.stdin.flush()
    assert json.loads(server.stdout.readline()) == {'ok': True}, 'Private fixture control failed; details suppressed'

def stop():
    if server and server.poll() is None:
        server.send_signal(signal.SIGTERM)
        assert server.wait(timeout=10) == 0, 'Owned production-boundary fixture did not stop cleanly'

def pair(label, page='page', remember=True):
    control({'action': 'advance', 'seconds': 61})
    value = script('''async (page) => {
      const target=PAGE;
      await target.locator('#label').fill(LABEL);
      const remember=REMEMBER;
      if(remember!==null)await target.locator('#remember').setChecked(remember);
      const created=target.waitForResponse(response=>response.url().endsWith('/api/v1/auth/pairings')&&response.request().method()==='POST');
      await target.getByRole('button',{name:'Connect device',exact:true}).click();
      const value=(await(await created).json()).data;
      await target.waitForFunction(()=>document.querySelector('#state').textContent==='Waiting for owner approval');
      if(!await target.evaluate(secret=>!document.documentElement.outerHTML.includes(secret)&&!location.href.includes(secret),value.polling_secret))throw new Error('polling secret leaked');
      return {pairing_id:value.pairing_id,code:value.code};
    }'''.replace('PAGE', page).replace('LABEL', json.dumps(label)).replace('REMEMBER', json.dumps(remember)))
    control({'action': 'approve', **value})
    del value
    return script('''async (page) => { const target=PAGE; await target.waitForFunction(()=>!document.querySelector('#owner').hidden,null,{timeout:12000}); return true; }'''.replace('PAGE', page))

proof = {'browser_plugin': 'absent', 'fallback': 'Browser plugin not available; existing approved Playwright CLI workflow', 'private_ingress': 'not_run', 'physical_device': 'not_run', 'wails': 'not_run'}
try:
    ready = start()
    local, peer = ready['endpoint'], ready['peer_endpoint']
    cli(['open', local, '--profile', str(profile)])
    opened = True
    proof.update(script('''async (page) => {
      await page.setViewportSize({width:1280,height:800});
      await page.evaluate(()=>sessionStorage.setItem('haeryong.qa.errors','[]'));
      const record=message=>page.evaluate(message=>{const errors=JSON.parse(sessionStorage.getItem('haeryong.qa.errors')||'[]');errors.push(message);sessionStorage.setItem('haeryong.qa.errors',JSON.stringify(errors))},message).catch(()=>{});
      page.on('pageerror',error=>void record(error.message));
      page.on('console',message=>{if(['error','warning'].includes(message.type())&&!/Failed to load resource|CORS policy|Access to fetch/.test(message.text()))void record(message.text());});
      if(await page.title()!=='Haeryong Connect'||await page.locator('h1').textContent()!=='Haeryong Connect')throw new Error('wrong page identity');
      if((await page.locator('main').innerText()).length<30)throw new Error('blank trusted shell');
      return {page_identity:true,nonblank:true,no_framework_overlay:!await page.locator('body').innerText().then(text=>/Webpack|Vite Error|Unhandled Runtime Error/.test(text)),engine:page.context().browser().version()};
    }'''))
    proof.update(script('''async(page)=>{if(!await page.locator('#remember').isChecked())throw new Error('configured true default was not checked before interaction');return {remember_default_true_before_user_action:true}}'''))
    cli(['screenshot', '--filename', str(runtime / 'default-true.png')])
    proof['default_true_screenshot'] = str(runtime / 'default-true.png')
    pair('Remembered owner', remember=None)
    proof.update(script('''async (page) => {
      const key='haeryong.owner-session.v1';
      if(!await page.evaluate(key=>Boolean(localStorage.getItem(key)),key))throw new Error('default remembered token absent');
      if(!await page.evaluate(async()=>{const token=localStorage.getItem('haeryong.owner-session.v1');for(const path of ['/api/v1/auth/pairings','/api/v1/sessions','/api/v1/jobs']){const response=await fetch(path,{headers:{Authorization:'Bearer '+token}});const value=await response.json();if(!response.ok||!Array.isArray(value.data?.items)||value.data.next_cursor!==null)return false;}return true;}))throw new Error('common list response envelope missing');
      await page.getByRole('button',{name:'Check private status',exact:true}).click();await page.waitForFunction(()=>document.querySelector('#private').textContent.includes('Library'));
      await page.reload();await page.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');
      await page.goto(BASE+'/connect?return='+encodeURIComponent('https://unrelated.example/'));await page.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');if(await page.evaluate(()=>location.pathname)!=='/')throw new Error('external return accepted');
      const route='/documents/11111111-1111-4111-8111-111111111111/versions/22222222-2222-4222-8222-222222222222?page=nested%2Fguide.html&page_query=q%3D1&page_fragment=part#region=example';
      await page.goto(BASE+'/connect?return='+encodeURIComponent(route));await page.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');
      if(await page.evaluate(()=>location.pathname)!==route.split('?')[0])throw new Error('logical return was lost');await page.reload();await page.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');
      const second=await page.context().newPage();await second.goto(BASE);await second.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');await second.close();
      const denied=await page.evaluate(async()=>{const paths=['/api/v1/session','/api/v1/private/assets','/api/v1/operations/unknown','/api/v1/jobs','/api/v1/events'];return Promise.all(paths.map(async path=>(await fetch(path,{credentials:'omit'})).status));});
      if(!denied.every(status=>status===401))throw new Error('unpaired protected endpoint was readable');
      if(!await page.evaluate(key=>{const token=localStorage.getItem(key);return !document.documentElement.outerHTML.includes(token)&&!location.href.includes(token)},key))throw new Error('bearer leaked into DOM or URL');
      return {remember_default:true,reload:true,new_tab:true,common_list_envelopes:true,safe_return_and_logical_reload:true,private_status:true,unpaired_assets_receipts_jobs_events_denied:true,no_dom_url_bearer:true};
    }'''.replace('BASE', json.dumps(local))))
    cli(['close']); opened = False
    cli(['open', local, '--profile', str(profile)]); opened = True
    script('''async(page)=>{
      const record=message=>page.evaluate(message=>{const errors=JSON.parse(sessionStorage.getItem('haeryong.qa.errors')||'[]');errors.push(message);sessionStorage.setItem('haeryong.qa.errors',JSON.stringify(errors))},message).catch(()=>{});
      page.on('pageerror',error=>void record(error.message));page.on('console',message=>{if(['error','warning'].includes(message.type())&&!/Failed to load resource|CORS policy|Access to fetch/.test(message.text()))void record(message.text());});
      await page.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');return true;
    }''')
    proof['browser_restart'] = True
    stop(); ready = start(urlparse(local).port)
    assert ready['endpoint'] == local
    script('''async(page)=>{await page.reload();await page.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');return true}''')
    proof['daemon_restart'] = True
    # Observe the configured false default before setChecked or any pairing
    # action. The private fixture uses the existing typed configuration writer.
    control({'action': 'remember_default', 'remember': False})
    stop(); ready = start(urlparse(local).port)
    assert ready['endpoint'] == local
    proof.update(script('''async(page)=>{
      const context=await page.context().browser().newContext(),target=await context.newPage();
      target.qaErrors=[];target.on('pageerror',error=>target.qaErrors.push(error.message));
      await target.setViewportSize({width:1280,height:800});await target.goto(BASE);
      if(await target.title()!=='Haeryong Connect'||await target.locator('h1').textContent()!=='Haeryong Connect'||(await target.locator('main').innerText()).length<30)throw new Error('false-default shell identity/blank failure');
      if(await target.locator('#remember').isChecked())throw new Error('configured false default was checked before interaction');
      if(/Webpack|Vite Error|Unhandled Runtime Error/.test(await target.locator('body').innerText()))throw new Error('false-default shell overlay');
      await target.screenshot({path:DESKTOP});await target.setViewportSize({width:390,height:844});await target.screenshot({path:MOBILE});
      return {remember_default_false_before_user_action:true,remember_default_false_survives_daemon_restart:true,default_false_page_identity:true,default_false_nonblank:true,default_false_no_overlay:true};
    }'''.replace('BASE', json.dumps(local)).replace('DESKTOP', json.dumps(str(runtime / 'default-false-desktop.png'))).replace('MOBILE', json.dumps(str(runtime / 'default-false-mobile.png')))))
    proof.update({'default_false_desktop_screenshot': str(runtime / 'default-false-desktop.png'), 'default_false_mobile_screenshot': str(runtime / 'default-false-mobile.png')})
    other_page = 'page.context().browser().contexts().find(context=>context!==page.context()).pages()[0]'
    pair('False default explicit remember', other_page, True)
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(value=>value!==page.context()),target=context.pages()[0];
      if(!await target.evaluate(()=>Boolean(localStorage.getItem('haeryong.owner-session.v1'))))throw new Error('explicit true override did not persist');
      await target.reload();await target.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');
      if(target.qaErrors.length)throw new Error('false-default override page error');await context.close();
      return {false_default_explicit_remember_override:true,false_default_remembered_reload:true};
    }'''))
    script('''async(page)=>{const context=await page.context().browser().newContext(),target=await context.newPage();await target.goto(BASE);if(await target.locator('#remember').isChecked())throw new Error('fresh false default changed');return true}'''.replace('BASE', json.dumps(local)))
    pair('False default memory only', other_page, None)
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(value=>value!==page.context()),target=context.pages()[0];
      if(!await target.evaluate(()=>localStorage.getItem('haeryong.owner-session.v1')===null))throw new Error('untouched false default persisted a bearer');
      await target.reload();await target.waitForFunction(()=>document.querySelector('#owner').hidden);await context.close();
      return {false_default_without_override_memory_only:true,false_default_memory_only_reload_requires_pairing:true};
    }'''))
    control({'action': 'remember_default', 'remember': True})
    # A second same-listener origin starts unpaired and pairs through the existing
    # trusted owner UI. No private HTTPS/network configuration is involved.
    control({'action': 'advance', 'seconds': 61})
    request = script('''async(page)=>{
      const peer=await page.context().newPage();await peer.goto(PEER);
      if(!await peer.locator('#owner').isHidden())throw new Error('origin-local credential crossed origins');
      await peer.locator('#label').fill('Peer origin');const wait=peer.waitForResponse(r=>r.url().endsWith('/api/v1/auth/pairings')&&r.request().method()==='POST');await peer.getByRole('button',{name:'Connect device',exact:true}).click();const p=(await(await wait).json()).data;return {pairing_id:p.pairing_id,code:p.code};
    }'''.replace('PEER', json.dumps(peer)))
    script('''async(page)=>{const peer=page.context().pages().find(tab=>tab.url().startsWith(PEER));await page.locator('#request-id').fill(ID);await page.locator('#code').fill(CODE);await page.getByRole('button',{name:'Approve device',exact:true}).click();await peer.waitForFunction(()=>!document.querySelector('#owner').hidden,null,{timeout:12000});return true}'''.replace('PEER',json.dumps(peer)).replace('ID', json.dumps(request['pairing_id'])).replace('CODE', json.dumps(request['code'])))
    del request
    proof.update(script('''async(page)=>{
      const peer=page.context().pages().find(tab=>tab.url().startsWith(PEER));
      await peer.reload();await peer.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');
      await page.getByRole('button',{name:'Refresh devices',exact:true}).click();await page.getByRole('button',{name:'Revoke Peer origin',exact:true}).click();
      await peer.waitForFunction(()=>document.querySelector('#owner').hidden,null,{timeout:6000});
      if(!await peer.evaluate(()=>localStorage.getItem('haeryong.owner-session.v1')===null&&document.querySelector('#private').textContent===''))throw new Error('revoked client retained private view/token');
      await peer.close();return {origin_isolation:true,owner_approves_peer:true,peer_remembered_reload:true,revoked_active_stream_and_client_cleared:true};
    }'''.replace('PEER',json.dumps(peer))))
    # Synthetic sandboxed frame uses the actual HTTP management boundary.
    proof.update(script('''async(page)=>{
      const navigated=page.waitForEvent('framenavigated',{predicate:frame=>frame.url().includes('/probe-child')});
      const denied=page.waitForResponse(response=>response.url().endsWith('/api/v1/auth/pairings/arbitrary/approve')&&response.status()===403);
      await page.evaluate(()=>{const frame=document.createElement('iframe');frame.id='probe';frame.setAttribute('sandbox','allow-scripts');frame.src='/probe-child';document.body.append(frame)});
      const probe=await navigated;await probe.waitForFunction(()=>window.boundary!==undefined);
      const boundary=await probe.evaluate(()=>window.boundary);
      await denied;
      if(Object.values(boundary).some(Boolean))throw new Error('opaque frame read trusted authority');
      await page.waitForTimeout(400);
      if(await page.evaluate(async()=>{const t=localStorage.getItem('haeryong.owner-session.v1');return(await fetch('/api/v1/session',{headers:{Authorization:'Bearer '+t}})).status})!==200)throw new Error('management postMessage changed authority');
      await page.locator('#probe').evaluate(node=>node.remove());return {frame_parent_dom_storage_script_separated:true,frame_management_http_denied:true,management_postmessage_ignored:true};
    }'''))
    proof.update(script('''async(page)=>{await page.locator('#request-id').fill('');await page.locator('#code').fill('');if(await page.locator('#code').inputValue()!==''||await page.locator('#request-id').inputValue()!=='')throw new Error('comparison control was not cleared');return {comparison_controls_cleared_before_capture:true}}'''))
    cli(['screenshot', '--filename', str(runtime / 'desktop.png')])
    script('''async(page)=>{await page.setViewportSize({width:390,height:844});return true}''')
    cli(['screenshot', '--filename', str(runtime / 'mobile.png')])
    proof.update({'desktop_screenshot': str(runtime / 'desktop.png'), 'mobile_screenshot': str(runtime / 'mobile.png'), 'screenshot_evidence': True})
    # Memory-only and controlled storage-write failure are real browser contexts.
    for name, remember, storage_failure in [('memory-only', False, False), ('storage-failure', True, True)]:
        script('''async(page)=>{const modeContext=await page.context().browser().newContext();const modePage=await modeContext.newPage();await modePage.goto(BASE);if(FAIL)await modePage.evaluate(()=>{Storage.prototype.setItem=function(){throw new DOMException('controlled write failure','QuotaExceededError')}});return true}'''.replace('BASE', json.dumps(local)).replace('FAIL', str(storage_failure).lower()))
        pair(name, 'page.context().browser().contexts().find(context=>context!==page.context()).pages()[0]', remember)
        script('''async(page)=>{const modeContext=page.context().browser().contexts().find(context=>context!==page.context());const modePage=modeContext.pages()[0];if(!await modePage.evaluate(()=>localStorage.getItem('haeryong.owner-session.v1')===null))throw new Error('memory-only token persisted');await modePage.reload();await modePage.waitForFunction(()=>document.querySelector('#owner').hidden);await modeContext.close();return true}''')
        proof[name.replace('-', '_')] = True
    control({'action': 'advance', 'seconds': 61})
    lost = script('''async(page)=>{
      const context=await page.context().browser().newContext(), target=await context.newPage();await target.goto(BASE);
      await target.evaluate(()=>window.pollCalls=0);
      await target.route('**/auth/pairings/*/poll',async route=>{await target.evaluate(()=>window.pollCalls++);const response=await route.fetch();const value=await response.json();if(value.data?.token)await route.abort('failed');else await route.fulfill({response});});
      await target.locator('#label').fill('Lost issuance');const wait=target.waitForResponse(r=>r.url().endsWith('/api/v1/auth/pairings')&&r.request().method()==='POST');await target.getByRole('button',{name:'Connect device',exact:true}).click();const p=(await(await wait).json()).data;return {pairing_id:p.pairing_id,code:p.code};
    }'''.replace('BASE',json.dumps(local)))
    control({'action':'approve', **lost})
    del lost
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      await target.waitForFunction(()=>document.querySelector('#state').textContent.includes('issuance is not repeated automatically'),null,{timeout:12000});await target.waitForTimeout(4500);
      if(!await target.evaluate(()=>window.pollCalls===1&&localStorage.getItem('haeryong.owner-session.v1')===null&&document.querySelector('#owner').hidden))throw new Error('uncertain single-use issuance was automatically repeated');
      await context.close();return {lost_single_use_issuance_not_retried:true};
    }'''))
    script('''async(page)=>{await page.evaluate(()=>localStorage.removeItem('haeryong.owner-session.v1'));await page.reload();await page.waitForFunction(()=>document.querySelector('#owner').hidden);return true}''')
    proof['storage_loss_connect'] = True
    pair('Expiry owner')
    control({'action': 'expiry', 'seconds': 1})
    script('''async(page)=>{await page.waitForFunction(()=>document.querySelector('#owner').hidden,null,{timeout:6000});if(!await page.evaluate(()=>localStorage.getItem('haeryong.owner-session.v1')===null))throw new Error('expired stored token retained');return true}''')
    proof['optional_expiry'] = True
    proof.update(script('''async(page)=>{if((await page.evaluate(()=>JSON.parse(sessionStorage.getItem('haeryong.qa.errors')||'[]'))).length)throw new Error('Relevant application console/page errors observed');return {console_health:true}}'''))
    proof.update({'interaction': True, 'runtime': str(runtime), 'fixture_classification': 'actual production HTTP/service/SQLite; synthetic policy/child fixtures; real desktop Chrome'})
    (runtime / 'browser-result.json').write_text(json.dumps(proof, indent=2) + '\n')
    if args.receipt:
        args.receipt.write_text(json.dumps(proof, indent=2) + '\n')
        args.receipt.chmod(0o600)
    print(json.dumps(proof))
finally:
    if opened:
        cli(['close'])
    stop()
