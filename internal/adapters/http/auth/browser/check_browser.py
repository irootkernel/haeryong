"""Actual browser proof of the React trusted shell on the production
HTTP/auth/SQLite boundary (TASK-003-01 update of the retained TASK-002-05
harness).

Regression coverage preserved from the previous harness: pairing through the
trusted UI, comparison-code disclosure without polling-secret leaks, owner
approval of a peer origin, remembered/memory-only sessions, configured
remember-default injection, browser/daemon restart, single-use issuance not
repeated on response loss, origin isolation, sandboxed-frame management
boundary and console health. New TASK-003-01 scenarios: capabilities
api-version mismatch and runtime-generation change through a loopback fault
proxy, disconnected write gating with the credential retained, registry deep
links and validated return routes, plus keyboard and touch walkthroughs of the
changed Connect/navigation flows.

The harness queries the shell by role/accessible name and the [data-state]
service indicator; it does not depend on element IDs or DOM shape. The
loopback fault proxy models the documented private-proxy forwarding path and
is not private ingress. Physical devices, Wails and complete renderer proof
remain unclaimed. Never print secrets.
"""
import argparse
import http.client
import http.server
import json
import os
from pathlib import Path
import re
import signal
import socket
import subprocess
import sys
import shutil
import tempfile
import threading
import urllib.parse
import uuid

parser = argparse.ArgumentParser()
parser.add_argument('--fixture-binary', type=Path, required=True)
parser.add_argument('--browser-cli', type=Path, required=True)
parser.add_argument('--receipt', type=Path)
args = parser.parse_args()
volume = Path('/Volumes/RootKernel')
base = volume / 'tmp' if os.path.ismount(volume) and os.access(volume, os.W_OK) else Path(tempfile.gettempdir())
runtime = Path(tempfile.mkdtemp(prefix='haeryong-task0101-browser-', dir=args.receipt.resolve().parent if args.receipt else base))
socket_dir = Path(tempfile.mkdtemp(prefix='hbs-', dir=base))
profile = runtime / 'browser-profile'
session = 'hauth' + uuid.uuid4().hex[:10]
env = os.environ | {'TMPDIR': str(runtime), 'PLAYWRIGHT_DAEMON_SESSION_DIR': str(runtime / 'registry'), 'PLAYWRIGHT_DAEMON_SOCKETS_DIR': str(socket_dir)}
browser_cli = args.browser_cli.resolve(strict=True)
browser = (['bash'] if browser_cli.suffix == '.sh' else []) + [str(browser_cli), '--session=' + session]
binary = str(args.fixture_binary.resolve(strict=True))
server = None
proxy = None
opened = False
clock_offset_seconds = 0

STATUS_VERIFIED_REMEMBERED = '기억된 연결로 접속했습니다.'
WAITING = '승인을 기다리는 중'
ISSUANCE_LOST = '연결이 확인되지 않았습니다'
STOPPED = '대기를 중단했습니다'
INCOMPATIBLE = '지원하지 않는 서비스 계약입니다'
DISCONNECTED = '서비스에 연결할 수 없습니다'

VERIFIED_WAIT = "document.querySelector('[data-state]')&&document.querySelector('[data-state]').getAttribute('data-state')==='verified'"
UNAUTHENTICATED_WAIT = "document.querySelector('[data-state]')===null&&document.querySelector('input[aria-label=\"장치 이름\"]')!==null"


def cli(arguments, result=False):
    value = subprocess.run(browser + arguments, cwd=runtime, env=env, capture_output=True, text=True, timeout=90)
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


def quoted(text):
    return json.dumps(text).strip('"')


class FaultProxy(http.server.ThreadingHTTPServer):
    """Loopback alias proxy: forwards with the alias Host and can rewrite the
    capabilities envelope (api_version or runtime_generation) for mismatch
    scenarios, or truncate one successful approval body. Modes: off | api2 |
    generation | truncate-approve (self-resetting)."""

    daemon_threads = True
    mode = 'off'
    target = None

    def handle_error(self, request, client_address):
        # Restarting the fixture or clients going away resets relay sockets;
        # that is an expected connection-loss outcome, not a harness bug.
        error = sys.exc_info()[1]
        if isinstance(error, (ConnectionResetError, ConnectionAbortedError, BrokenPipeError, TimeoutError)):
            return
        super().handle_error(request, client_address)


def make_proxy():
    listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    listener.bind(('127.0.0.1', 0))
    listener.listen(8)
    address = listener.getsockname()

    class RelayHandler(http.server.BaseHTTPRequestHandler):
        protocol_version = 'HTTP/1.1'

        def log_message(self, *items):
            return

        def relay(self):
            if FaultProxy.target is None:
                self.send_error(503)
                return
            try:
                self._relay_body()
            except (http.client.HTTPException, ConnectionError, BrokenPipeError, TimeoutError, OSError):
                # Expected connection loss while the fixture restarts or a
                # client goes away; unrelated bugs still propagate.
                self.close_connection = True

        def _relay_body(self):
            host, port = FaultProxy.target
            length = int(self.headers.get('Content-Length') or 0)
            body = self.rfile.read(length) if length else None
            forward = http.client.HTTPConnection(host, port, timeout=30)
            headers = {name: value for name, value in self.headers.items() if name.lower() not in {'host', 'accept-encoding', 'connection', 'content-length'}}
            headers['Host'] = f'{address[0]}:{address[1]}'
            headers['Accept-Encoding'] = 'identity'
            forward.request(self.command, self.path, body=body, headers=headers)
            response = forward.getresponse()
            streaming = 'text/event-stream' in (response.getheader('Content-Type') or '')
            if streaming:
                # Relay the event stream incrementally so liveness keeps working.
                self.send_response(response.status, response.reason)
                for name in ('Content-Type', 'Cache-Control', 'X-Accel-Buffering'):
                    value = response.getheader(name)
                    if value:
                        self.send_header(name, value)
                self.send_header('Connection', 'close')
                self.end_headers()
                while True:
                    chunk = response.read(1024)
                    if not chunk:
                        break
                    self.wfile.write(chunk)
                    self.wfile.flush()
                forward.close()
                return
            payload = response.read()
            status, reason, heads = response.status, response.reason, response.getheaders()
            forward.close()
            truncate = (
                FaultProxy.mode == 'truncate-approve'
                and self.command == 'POST'
                and urllib.parse.urlparse(self.path).path.endswith('/approve')
                and status == 200
            )
            if truncate:
                # The server committed this approval; deliver its success
                # status and original headers (including Content-Length), then
                # only a body prefix before closing: the browser's body read
                # fails after headers arrived. One-shot; later approvals pass.
                FaultProxy.mode = 'off'
                self.send_response(status, reason)
                for name, value in heads:
                    if name.lower() in {'content-length', 'transfer-encoding', 'connection', 'content-encoding'}:
                        continue
                    self.send_header(name, value)
                self.send_header('Content-Length', str(len(payload)))
                self.end_headers()
                self.wfile.write(payload[: max(1, len(payload) // 2)])
                self.close_connection = True
                return
            if (FaultProxy.mode in {'api2', 'generation'} and self.command == 'GET'
                    and urllib.parse.urlparse(self.path).path == '/api/v1/capabilities' and status == 200):
                envelope = json.loads(payload)
                if FaultProxy.mode == 'api2':
                    envelope['data']['api_version'] = '2.0'
                else:
                    envelope['data']['runtime_generation'] = 'proxy-generation-2'
                payload = json.dumps(envelope).encode()
            self.send_response(status, reason)
            for name, value in heads:
                if name.lower() in {'content-length', 'transfer-encoding', 'connection', 'content-encoding'}:
                    continue
                self.send_header(name, value)
            self.send_header('Content-Length', str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        do_GET = do_POST = do_DELETE = do_PUT = do_PATCH = do_HEAD = do_OPTIONS = relay

    httpd = FaultProxy(address, RelayHandler, bind_and_activate=False)
    httpd.socket = listener
    return httpd, f'http://{address[0]}:{address[1]}'


def start_proxy():
    global proxy
    httpd, alias = make_proxy()
    proxy = httpd
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    return alias


def stop_proxy():
    global proxy
    if proxy is not None:
        proxy.shutdown()
        proxy.server_close()
        proxy = None


def fixture_port(endpoint):
    return urllib.parse.urlparse(endpoint).port


def start(port=0, alias=None):
    global server
    command = [binary, '--directory', str(runtime), '--port', str(port), '--clock-offset-seconds', str(clock_offset_seconds)]
    if alias:
        command += ['--alias', alias]
    server = subprocess.Popen(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    value = json.loads(server.stdout.readline())
    assert value['fixture'] == 'task00301-production-auth-shell' and value['pid'] == server.pid
    return value


def control(value):
    global server, clock_offset_seconds
    server.stdin.write(json.dumps(value) + '\n')
    server.stdin.flush()
    verdict = json.loads(server.stdout.readline())
    if verdict != {'ok': True}:
        # Typed, redacted fixture diagnostics; no codes/IDs/origins are echoed.
        raise RuntimeError('Private fixture control failed (reason=' + str(verdict.get('reason', 'unknown')) + ')')
    if value.get('action') == 'advance':
        # Keep the synthetic clock monotonic across fixture restarts. Durable
        # pairing-rate timestamps must not become future rows after restart.
        clock_offset_seconds += value['seconds']


def stop():
    global server
    if server and server.poll() is None:
        server.send_signal(signal.SIGTERM)
        assert server.wait(timeout=10) == 0, 'Owned production-boundary fixture did not stop cleanly'
    server = None


def pair(label, page='page', remember=None):
    control({'action': 'advance', 'seconds': 61})
    value = script('''async (page) => {
      const target=PAGE;
      await target.getByLabel('장치 이름',{exact:true}).fill(LABEL);
      const remember=REMEMBER;
      if(remember!==null)await target.getByLabel('이 장치 기억',{exact:true}).setChecked(remember);
      const created=target.waitForResponse(response=>response.url().endsWith('/api/v1/auth/pairings')&&response.request().method()==='POST');
      await target.getByRole('button',{name:'연결 코드 요청',exact:true}).click();
      const envelope=await(await created).json().catch(error=>{throw new Error('pairing POST failed: '+String(error))});
      if(!envelope.data)throw new Error('pairing POST rejected: '+JSON.stringify(envelope.error||{}));
      const value=envelope.data;
      await target.waitForFunction(()=>document.querySelector('[aria-label="비교 코드"]')!==null,null,{timeout:9000}).catch(async()=>{throw new Error('comparison code absent: '+await target.evaluate(()=>document.body.textContent.slice(0,200)))});
      await target.waitForFunction(()=>{const node=document.querySelector('[aria-label="페어링 진행"]');return node!==null&&node.textContent.includes('WAITING')},null,{timeout:9000}).catch(async()=>{throw new Error('waiting copy absent: '+await target.evaluate(()=>document.body.textContent.slice(0,200)))});
      if(!await target.evaluate(secret=>!document.documentElement.outerHTML.includes(secret)&&!location.href.includes(secret),value.polling_secret))throw new Error('polling secret leaked');
      return {pairing_id:value.pairing_id,code:value.code};
    }'''.replace('PAGE', page).replace('LABEL', json.dumps(label)).replace('REMEMBER', json.dumps(remember)).replace('WAITING', quoted(WAITING)))
    control({'action': 'approve', **value})
    del value
    return script('''async (page) => { const target=PAGE; await target.waitForFunction(()=>VERIFIED,null,{timeout:12000}); return true; }'''.replace('PAGE', page).replace('VERIFIED', VERIFIED_WAIT))


def attach_console_watch(target_expression):
    return script('''async (page) => {
      const target=EXPRESSION;
      target.qaErrors=[];
      target.on('pageerror',error=>target.qaErrors.push(error.message));
      target.on('console',message=>{if(['error','warning'].includes(message.type())&&!/Failed to load resource|CORS policy|Access to fetch|net::|ERR_/.test(message.text()))target.qaErrors.push(message.text())});
      return true;
    }'''.replace('EXPRESSION', target_expression))


proof = {'browser_plugin': 'absent', 'fallback': 'Browser plugin not available; existing approved Playwright CLI workflow',
         'private_ingress': 'not_run', 'physical_device': 'not_run', 'wails': 'not_run'}
try:
    alias_origin = start_proxy()
    ready = start(alias=alias_origin)
    FaultProxy.target = ('127.0.0.1', fixture_port(ready['endpoint']))
    local, peer = ready['endpoint'], ready['peer_endpoint']
    cli(['open', local, '--profile', str(profile)])
    opened = True
    proof.update(script('''async (page) => {
      await page.setViewportSize({width:1280,height:800});
      await page.evaluate(()=>sessionStorage.setItem('haeryong.qa.errors','[]'));
      const record=message=>page.evaluate(message=>{const errors=JSON.parse(sessionStorage.getItem('haeryong.qa.errors')||'[]');errors.push(message);sessionStorage.setItem('haeryong.qa.errors',JSON.stringify(errors))},message).catch(()=>{});
      page.on('pageerror',error=>void record(error.message));
      page.on('console',message=>{if(['error','warning'].includes(message.type())&&!/Failed to load resource|CORS policy|Access to fetch|net::|ERR_/.test(message.text()))void record(message.text())});
      if(await page.title()!=='Haeryong')throw new Error('wrong page identity');
      if(await page.getByRole('heading',{name:'Haeryong 연결',exact:true}).isVisible()!==true)throw new Error('connect heading missing');
      if((await page.locator('main').innerText()).length<30)throw new Error('blank trusted shell');
      if(!await page.locator('meta[name="haeryong-session-defaults"]').getAttribute('content').then(value=>value&&value.includes('remember-device=true')))throw new Error('server-injected remember default missing');
      return {page_identity:true,nonblank:true,remember_meta_injection:true,
              no_framework_overlay:!await page.locator('body').innerText().then(text=>/Webpack|Vite Error|Unhandled Runtime Error/.test(text)),
              engine:page.context().browser().version()};
    }'''))
    proof.update(script('''async(page)=>{if(!await page.getByLabel('이 장치 기억',{exact:true}).isChecked())throw new Error('configured true default was not checked before interaction');return {remember_default_true_before_user_action:true}}'''))
    cli(['screenshot', '--filename', str(runtime / 'default-true.png')])
    proof['default_true_screenshot'] = str(runtime / 'default-true.png')
    pair('Remembered owner', remember=None)
    source_root = runtime / 'source-root'
    source_root.mkdir()
    source_alias = runtime / 'source-alias'
    source_alias.symlink_to(source_root, target_is_directory=True)
    proof.update(script('''async(page)=>{
      const base=BASE, root=SOURCE_ROOT, alias=SOURCE_ALIAS;
      const api=async(method,path,body)=>page.evaluate(async({method,path,body})=>{
        const response=await fetch('/api/v1'+path,{method,headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1'),'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()},body:body===undefined?undefined:JSON.stringify(body)});
        const result=await response.json();if(!response.ok)throw Error('S09 fixture request '+response.status);return result.data;
      },{method,path,body});
      await page.goto(base+'/projects');
      await page.getByRole('heading',{name:'프로젝트',exact:true}).waitFor();
      // D2: real App/ShellController/HTTP freshness guard and generation-keyed
      // screen remount. Applied POST acknowledgements wait until AFTER remount.
      let generation=null, remounts=0;
      await page.route('**/api/v1/capabilities',async route=>{
        const response=await route.fetch(),body=await response.json();
        if(generation!==null)body.data.runtime_generation=generation;
        await route.fulfill({response,json:body});
      });
      const acrossGeneration=async(path,dispatch,retry,complete)=>{
        let applied,release,delivered,key='',posts=0;
        const appliedPromise=new Promise(resolve=>applied=resolve);
        const held=new Promise(resolve=>release=resolve);
        const deliveredPromise=new Promise(resolve=>delivered=resolve);
        const bounded=(promise,stage)=>Promise.race([promise,page.waitForTimeout(15000).then(()=>{throw Error('D2 '+path+' stalled at '+stage)})]);
        const reads=[];
        const onRequest=request=>{if(request.url().includes('/operations/'))reads.push(request.url().split('/').pop());};
        page.on('request',onRequest);
        const pattern='**/api/v1'+path;
        await page.route(pattern,async route=>{
          if(route.request().method()!=='POST'){await route.continue();return;}
          posts++;key=route.request().headers()['idempotency-key'];
          const response=await route.fetch();if(!response.ok())throw Error('remount POST was not applied');
          applied();await held;await route.fulfill({response});delivered();
        });
        const oldScreen=await page.locator('main.shell-content').elementHandle();
        await bounded(dispatch(),'dispatch');await bounded(appliedPromise,'applied');
        generation='remount-'+(remounts+1);
        await page.evaluate(()=>document.dispatchEvent(new Event('visibilitychange')));
        await page.waitForFunction(el=>!el.isConnected,oldScreen);
        await retry.waitFor();remounts++;
        const responseFinished=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().endsWith('/api/v1'+path));
        release();await bounded(responseFinished,'response');await bounded(deliveredPromise,'delivered');
        // The stale guard discards the ack at headers without reading its body.
        // A CDP round-trip lets the retired flow classify that discarded ack.
        await page.evaluate(()=>new Promise(resolve=>setTimeout(resolve,0)));
        await retry.click();
        await complete.waitFor();
        if(posts!==1||reads.length<1||reads.some(id=>id!==key))throw Error('runtime remount lost original write identity');
        await page.unroute(pattern);page.off('request',onRequest);
      };

      await page.getByLabel('프로젝트 이름',{exact:true}).fill('S09 proof');
      await acrossGeneration('/projects',()=>page.getByLabel('프로젝트 이름',{exact:true}).press('Enter'),page.getByRole('button',{name:'등록 결과 다시 확인',exact:true}),page.getByRole('button',{name:'프로젝트 등록',exact:true}));
      await page.getByRole('button',{name:'S09 proof',exact:true}).click();
      await page.getByRole('heading',{name:'원본',exact:true}).waitFor();
      await page.getByText('등록된 출처 없음',{exact:true}).waitFor();
      const project=(await page.url()).split('/projects/')[1].split('/')[0];
      const initial=await api('GET','/configuration');
      await page.getByRole('button',{name:'출처 설정 편집',exact:true}).click();
      await page.getByRole('button',{name:'출처 추가',exact:true}).click();
      await page.getByLabel(/^종류/).selectOption('files');
      await page.getByLabel('승인할 원본 루트',{exact:true}).fill(alias);
      await page.getByRole('button',{name:'변경 미리보기',exact:true}).focus();
      await acrossGeneration('/configuration/plans',()=>page.keyboard.press('Enter'),page.getByRole('button',{name:'원래 설정 작업 결과 다시 확인',exact:true}),page.getByRole('heading',{name:'설정 계획 미리보기',exact:true}));
      await page.getByRole('heading',{name:'설정 계획 미리보기',exact:true}).waitFor();
      const preview=await page.getByLabel('설정 계획 미리보기',{exact:true}).innerText();
      if(!preview.includes(root)||preview.includes(alias))throw Error('preview did not show canonical server root');
      const commit=page.getByRole('button',{name:'확인 후 적용',exact:true});
      if(!await commit.isDisabled())throw Error('expanded scope skipped confirmation');
      await page.getByRole('button',{name:'취소',exact:true}).click();
      if((await api('GET','/configuration')).config_revision!==initial.config_revision)throw Error('cancel committed a configuration');
      const [commitPlanResponse]=await Promise.all([page.waitForResponse(r=>r.request().method()==='POST'&&r.url().endsWith('/configuration/plans')),page.getByRole('button',{name:'변경 미리보기',exact:true}).click()]);
      const commitPlan=(await commitPlanResponse.json()).data;
      await page.getByLabel('출처/공개 범위 확대 영향을 확인했습니다',{exact:true}).check();
      await acrossGeneration('/configuration/plans/'+commitPlan.plan_id+'/commit',()=>commit.click(),page.getByRole('button',{name:'원래 설정 작업 결과 다시 확인',exact:true}),page.getByText('출처 설정 적용됨',{exact:true}));
      if(remounts!==3)throw Error('runtime remount coverage incomplete');
      await page.getByRole('button',{name:'원본 목록으로',exact:true}).click();
      await page.getByText('관측 기록 없음',{exact:true}).waitFor();
      if((await page.locator('main').innerText()).includes(root))throw Error('S09 exposed a host root');
      await page.screenshot({path:SOURCE_SHOT,fullPage:true});
      await page.getByRole('button',{name:'출처 설정 편집',exact:true}).click();
      const providers=page.getByLabel('공개할 Provider ID(쉼표 구분)',{exact:true});
      await providers.fill('');await providers.pressSequentially('prov-a, prov-b');
      const [planResponse]=await Promise.all([page.waitForResponse(r=>r.request().method()==='POST'&&r.url().endsWith('/configuration/plans')),page.getByRole('button',{name:'변경 미리보기',exact:true}).click()]);
      const normalized=(await planResponse.json()).data.normalized_changes.projects[project].sources[0];
      if(JSON.stringify(normalized.provider_ids)!==JSON.stringify(['prov-a','prov-b']))throw Error('ordinary provider typing lost comma');
      if(normalized.root_path!==root)throw Error('server did not canonicalize source root');
      await page.getByRole('heading',{name:'설정 계획 미리보기',exact:true}).waitFor();
      // Advance the real configuration between this preview and its commit.
      const current=await api('GET','/configuration');
      const other=await api('POST','/configuration/plans',{scope:{kind:'project',project_id:project},expected_config_revision:current.config_revision,changes:{sources:current.configuration.projects[project].sources}});
      await api('POST','/configuration/plans/'+other.plan_id+'/commit',{expected_config_revision:other.base_config_revision,confirmed:true});
      await page.getByLabel('출처/공개 범위 확대 영향을 확인했습니다',{exact:true}).check();await commit.click();
      await page.getByText('계획이 오래되었습니다.',{exact:false}).waitFor();
      await page.getByRole('button',{name:'현재 설정 다시 불러오기',exact:true}).click();
      await page.getByRole('button',{name:'변경 미리보기',exact:true}).waitFor();
      // E3(b): leave/re-enter the real settings screen in the SAME generation,
      // then deliver the original preview/commit ack normally to the retired flow.
      const normalReentry=async(path,dispatch,complete)=>{
        let applied,release,delivered,key='',posts=0,result;
        const appliedPromise=new Promise(resolve=>applied=resolve);
        const held=new Promise(resolve=>release=resolve);
        const deliveredPromise=new Promise(resolve=>delivered=resolve);
        const bounded=(promise,stage)=>Promise.race([promise,page.waitForTimeout(15000).then(()=>{throw Error('E3 '+path+' stalled at '+stage)})]);
        const reads=[];
        const onRequest=request=>{if(request.url().includes('/operations/'))reads.push(request.url().split('/').pop());};
        page.on('request',onRequest);
        const pattern='**/api/v1'+path;
        await page.route(pattern,async route=>{
          if(route.request().method()!=='POST'){await route.continue();return;}
          posts++;key=route.request().headers()['idempotency-key'];
          const response=await route.fetch();if(!response.ok())throw Error('normal settings write was not applied');
          result=(await response.json()).data;
          applied();await held;await route.fulfill({response});delivered();
        });
        const oldScreen=await page.getByLabel('출처 설정',{exact:true}).elementHandle();
        await bounded(dispatch(),'dispatch');await bounded(appliedPromise,'applied');
        await page.getByRole('button',{name:'원본 목록으로',exact:true}).click();
        await page.getByRole('heading',{name:'원본',exact:true}).waitFor();
        await page.getByRole('button',{name:'출처 설정 편집',exact:true}).click();
        await page.waitForFunction(el=>!el.isConnected,oldScreen);
        const retry=page.getByRole('button',{name:'원래 설정 작업 결과 다시 확인',exact:true});
        await retry.waitFor();
        const responseFinished=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().endsWith('/api/v1'+path));
        release();const response=await bounded(responseFinished,'response');await bounded(response.finished(),'body');await bounded(deliveredPromise,'delivered');
        await page.evaluate(()=>new Promise(resolve=>setTimeout(resolve,0)));
        await retry.click();await complete.waitFor();
        if(posts!==1||reads.some(id=>id!==key))throw Error('normal settings completion lost its retained intent');
        await page.unroute(pattern);page.off('request',onRequest);
        return result;
      };
      const normalPlan=await normalReentry('/configuration/plans',()=>page.getByRole('button',{name:'변경 미리보기',exact:true}).click(),page.getByRole('heading',{name:'설정 계획 미리보기',exact:true}));
      await normalReentry('/configuration/plans/'+normalPlan.plan_id+'/commit',()=>page.getByRole('button',{name:'확인 후 적용',exact:true}).click(),page.getByText('출처 설정 적용됨',{exact:true}));

      // Touch exercises the same settings flow in a genuine touch context.
      const context=await page.context().browser().newContext({hasTouch:true,viewport:{width:820,height:1180}});
      const token=await page.evaluate(()=>localStorage.getItem('haeryong.owner-session.v1'));
      await context.addInitScript(([token,origin])=>{if(window===top&&location.origin===origin)localStorage.setItem('haeryong.owner-session.v1',token)},[token,base]);
      const touch=await context.newPage();await touch.goto(base+'/projects/'+project+'/sources');
      await touch.getByRole('button',{name:'출처 설정 편집',exact:true}).tap();
      await touch.getByRole('button',{name:'변경 미리보기',exact:true}).tap();
      await touch.getByRole('heading',{name:'설정 계획 미리보기',exact:true}).waitFor();
      await touch.screenshot({path:TOUCH_SHOT,fullPage:true});
      await touch.getByRole('button',{name:'취소',exact:true}).tap();
      await touch.getByRole('button',{name:'변경 미리보기',exact:true}).waitFor();
      await context.close();await page.unroute('**/api/v1/capabilities');await page.goto(base);
      return {s09_normal_preview_commit_completion_after_reentry:true,s09_runtime_remount_registration_plan_commit:true,s09_canonical_plan_preview:true,s09_provider_typing:true,s09_registration:true,s09_preview_confirm_commit:true,s09_cancel_no_commit:true,s09_stale_plan:true,s09_scope_impact_confirmation:true,s09_logical_identity_no_host_path:true,s09_keyboard:true,s09_touch:true};
    }'''.replace('BASE',json.dumps(local)).replace('SOURCE_ROOT',json.dumps(str(source_root))).replace('SOURCE_ALIAS',json.dumps(str(source_alias))).replace('SOURCE_SHOT',json.dumps(str(runtime/'sources.png'))).replace('TOUCH_SHOT',json.dumps(str(runtime/'sources-touch.png')))))
    proof.update(script('''async (page) => {
      const key='haeryong.owner-session.v1';
      if(!await page.evaluate(key=>Boolean(localStorage.getItem(key)),key))throw new Error('default remembered token absent');
      if(!await page.evaluate(async()=>{const token=localStorage.getItem('haeryong.owner-session.v1');for(const path of ['/api/v1/auth/pairings','/api/v1/sessions','/api/v1/jobs']){const response=await fetch(path,{headers:{Authorization:'Bearer '+token}});const value=await response.json();if(!response.ok||!Array.isArray(value.data?.items)||value.data.next_cursor!==null)return false;}return true;}))throw new Error('common list response envelope missing');
      await page.goto((await page.evaluate(()=>location.origin))+'/connect');
      await page.getByRole('button',{name:'비공개 상태 확인',exact:true}).click();
      await page.waitForFunction(()=>{const node=document.querySelector('[aria-label="비공개 상태"]');return node!==null&&/library/i.test(node.textContent)});
      const capabilities=await page.evaluate(async()=>{const token=localStorage.getItem('haeryong.owner-session.v1');const response=await fetch('/api/v1/capabilities',{headers:{Authorization:'Bearer '+token}});return (await response.json()).data;});
      if(capabilities.api_version!=='1.0')throw new Error('unexpected api_version');
      await page.reload();
      await page.waitForFunction(()=>(()=>{const node=document.querySelector('[aria-label="연결 상태"]');return node!==null&&node.textContent==='REMEMBERED'})());
      return {remember_default:true,capabilities_read:true,private_status_authorized_identity:true,common_list_envelopes:true,remembered_notice_on_connect:true};
    }'''.replace('REMEMBERED', quoted(STATUS_VERIFIED_REMEMBERED))))
    proof.update(script('''async (page) => {
      const base=BASE;
      await page.goto(base+'/connect?return='+encodeURIComponent('https://unrelated.example/'));
      await page.waitForFunction(()=>VERIFIED);
      if(await page.evaluate(()=>location.pathname)!=='/')throw new Error('external return accepted');
      const route='/documents/11111111-1111-4111-8111-111111111111/versions/22222222-2222-4222-8222-222222222222?page=nested%2Fguide.html&page_query=q%3D1&page_fragment=part#region=example';
      await page.goto(base+'/connect?return='+encodeURIComponent(route));
      await page.waitForFunction(()=>VERIFIED);
      if(await page.evaluate(()=>location.pathname+location.search)!==route.split('#')[0])throw new Error('logical return was lost');
      const reader=route.split('?')[0];
      for(const encodedPage of ['guide%5Cnotes.html','line%0Anotes.html','tab%09notes.html','%ED%95%9C%EA%B8%80%20notes.html','literal%3Fq%23h.html','%252e%252e%2Fnotes.html']){
        const target=reader+'?page='+encodedPage+'&page_query=q%3D1&page_fragment=part#region=example';
        await page.goto(base+'/connect?return='+encodeURIComponent(target));
        await page.waitForFunction(()=>VERIFIED);
        if(await page.evaluate(()=>location.pathname+location.search+location.hash)!==target)throw new Error('encoded filename auth-return context was lost');
        await page.reload();
        await page.waitForFunction(()=>VERIFIED);
        if(await page.evaluate(()=>location.pathname+location.search+location.hash)!==target)throw new Error('encoded filename reload context was lost');
      }
      for(const encodedPage of ['..%2Fescape.html','nested%2F.%2Fnotes.html','nested%2F%2Fnotes.html','%2Foutside.html','C%3Anotes.html','nul%00.html']){
        await page.goto(base+'/connect?return='+encodeURIComponent(reader+'?page='+encodedPage));
        await page.waitForFunction(()=>VERIFIED);
        if(await page.evaluate(()=>location.pathname+location.search+location.hash)!=='/')throw new Error('invalid page auth-return was accepted');
      }
      await page.goto(base+route);
      await page.waitForFunction(()=>VERIFIED&&document.body.textContent.includes('문서 정보를 불러오지 못했습니다'));
      if(await page.evaluate(()=>location.pathname+location.search+location.hash)!==route)throw new Error('deep link context was rewritten');
      await page.reload();
      await page.waitForFunction(()=>VERIFIED&&document.body.textContent.includes('문서 정보를 불러오지 못했습니다'));
      const second=await page.context().newPage();await second.goto(base);
      await second.waitForFunction(()=>VERIFIED);await second.close();
      const denied=await page.evaluate(async()=>{const paths=['/api/v1/session','/api/v1/private/assets','/api/v1/operations/unknown','/api/v1/jobs','/api/v1/events'];return Promise.all(paths.map(async path=>(await fetch(path,{credentials:'omit'})).status));});
      if(!denied.every(status=>status===401))throw new Error('unpaired protected endpoint was readable');
      if(!await page.evaluate(key=>{const token=localStorage.getItem(key);return !document.documentElement.outerHTML.includes(token)&&!location.href.includes(token)},'haeryong.owner-session.v1'))throw new Error('bearer leaked into DOM or URL');
      return {safe_return_external_rejected:true,logical_return_and_reload:true,deep_link_context_preserved:true,new_tab:true,
              encoded_filename_auth_return_preserved:true,encoded_filename_reload_preserved:true,invalid_page_auth_return_rejected:true,
              reader_unknown_version_honest:true,unpaired_assets_receipts_jobs_events_denied:true,no_dom_url_bearer:true};
    }'''.replace('BASE', json.dumps(local)).replace('REMEMBERED', quoted(STATUS_VERIFIED_REMEMBERED)).replace('VERIFIED', VERIFIED_WAIT)))
    cli(['close']); opened = False
    cli(['open', local, '--profile', str(profile)]); opened = True
    script('''async(page)=>{
      const record=message=>page.evaluate(message=>{const errors=JSON.parse(sessionStorage.getItem('haeryong.qa.errors')||'[]');errors.push(message);sessionStorage.setItem('haeryong.qa.errors',JSON.stringify(errors))},message).catch(()=>{});
      page.on('pageerror',error=>void record(error.message));page.on('console',message=>{if(['error','warning'].includes(message.type())&&!/Failed to load resource|CORS policy|Access to fetch|net::|ERR_/.test(message.text()))void record(message.text())});
      await page.waitForFunction(()=>VERIFIED);return true;
    }'''.replace('VERIFIED', VERIFIED_WAIT))
    proof['browser_restart'] = True
    stop(); ready = start(fixture_port(local), alias=alias_origin)
    assert ready['endpoint'] == local
    FaultProxy.target = ('127.0.0.1', fixture_port(ready['endpoint']))
    script('''async(page)=>{await page.reload();await page.waitForFunction(()=>VERIFIED);return true}'''.replace('VERIFIED', VERIFIED_WAIT))
    proof['daemon_restart'] = True
    control({'action': 'remember_default', 'remember': False})
    stop(); ready = start(fixture_port(local), alias=alias_origin)
    assert ready['endpoint'] == local
    FaultProxy.target = ('127.0.0.1', fixture_port(ready['endpoint']))
    proof.update(script('''async(page)=>{
      const context=await page.context().browser().newContext(),target=await context.newPage();
      target.qaErrors=[];target.on('pageerror',error=>target.qaErrors.push(error.message));
      await target.setViewportSize({width:1280,height:800});await target.goto(BASE);
      if(!await target.locator('meta[name="haeryong-session-defaults"]').getAttribute('content').then(value=>value&&value.includes('remember-device=false')))throw new Error('configured false default not injected');
      if(await target.getByLabel('이 장치 기억',{exact:true}).isChecked())throw new Error('configured false default was checked before interaction');
      if(/Webpack|Vite Error|Unhandled Runtime Error/.test(await target.locator('body').innerText()))throw new Error('false-default shell overlay');
      await target.screenshot({path:DESKTOP});await target.setViewportSize({width:390,height:844});await target.screenshot({path:MOBILE});
      return {remember_default_false_injected:true,remember_default_false_before_user_action:true,default_false_nonblank:true,default_false_no_overlay:true};
    }'''.replace('BASE', json.dumps(local)).replace('DESKTOP', json.dumps(str(runtime / 'default-false-desktop.png'))).replace('MOBILE', json.dumps(str(runtime / 'default-false-mobile.png')))))
    proof.update({'default_false_desktop_screenshot': str(runtime / 'default-false-desktop.png'), 'default_false_mobile_screenshot': str(runtime / 'default-false-mobile.png')})
    other_page = 'page.context().browser().contexts().find(context=>context!==page.context()).pages()[0]'
    pair('False default explicit remember', other_page, True)
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(value=>value!==page.context()),target=context.pages()[0];
      if(!await target.evaluate(()=>Boolean(localStorage.getItem('haeryong.owner-session.v1'))))throw new Error('explicit true override did not persist');
      await target.reload();
      await target.waitForFunction(()=>VERIFIED);
      if(target.qaErrors.length)throw new Error('false-default override page error');await context.close();
      return {false_default_explicit_remember_override:true,false_default_remembered_reload:true};
    }'''.replace('VERIFIED', VERIFIED_WAIT)))
    script('''async(page)=>{const context=await page.context().browser().newContext(),target=await context.newPage();await target.goto(BASE);if(await target.getByLabel('이 장치 기억',{exact:true}).isChecked())throw new Error('fresh false default changed');return true}'''.replace('BASE', json.dumps(local)))
    pair('False default memory only', other_page, None)
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(value=>value!==page.context()),target=context.pages()[0];
      if(!await target.evaluate(()=>localStorage.getItem('haeryong.owner-session.v1')===null))throw new Error('untouched false default persisted a bearer');
      await target.reload();await target.waitForFunction(()=>UNAUTHENTICATED);
      await context.close();
      return {false_default_without_override_memory_only:true,false_default_memory_only_reload_requires_pairing:true};
    }'''.replace('UNAUTHENTICATED', UNAUTHENTICATED_WAIT)))
    control({'action': 'remember_default', 'remember': True})
    control({'action': 'advance', 'seconds': 61})
    request = script('''async(page)=>{
      const peer=await page.context().newPage();await peer.goto(PEER);
      if(await peer.evaluate(()=>document.querySelector('[data-state]')!==null))throw new Error('origin-local credential crossed origins');
      await peer.getByLabel('장치 이름',{exact:true}).fill('Peer origin');
      const wait=peer.waitForResponse(r=>r.url().endsWith('/api/v1/auth/pairings')&&r.request().method()==='POST');
      await peer.getByRole('button',{name:'연결 코드 요청',exact:true}).click();const p=(await(await wait).json()).data;
      return {pairing_id:p.pairing_id,code:p.code};
    }'''.replace('PEER', json.dumps(peer)))
    script('''async(page)=>{const peer=page.context().pages().find(tab=>tab.url().startsWith(PEER));
      await page.goto((await page.evaluate(()=>location.origin))+'/connect');
      await page.waitForFunction(()=>VERIFIED);
      await page.getByLabel('요청 ID',{exact:true}).fill(PAIRING_ID);await page.getByLabel('비교 코드',{exact:true}).fill(COMPARISON);
      await page.getByRole('button',{name:'장치 승인',exact:true}).click();
      await peer.waitForFunction(()=>VERIFIED,null,{timeout:12000});return true}'''.replace('PEER', json.dumps(peer)).replace('PAIRING_ID', json.dumps(request['pairing_id'])).replace('COMPARISON', json.dumps(request['code'])).replace('VERIFIED', VERIFIED_WAIT))
    del request
    proof.update(script('''async(page)=>{
      const peer=page.context().pages().find(tab=>tab.url().startsWith(PEER));
      await peer.reload();await peer.waitForFunction(()=>VERIFIED);
      await page.getByRole('button',{name:'장치 목록 새로고침',exact:true}).click();
      await page.getByRole('button',{name:'해제: Peer origin',exact:true}).click();
      await peer.waitForFunction(()=>document.querySelector('[data-state]')===null,null,{timeout:6000});
      if(!await peer.evaluate(()=>localStorage.getItem('haeryong.owner-session.v1')===null))throw new Error('revoked client retained token');
      await peer.close();return {origin_isolation:true,owner_approves_peer_exact_request:true,peer_remembered_reload:true,peer_revoked_client_cleared:true};
    }'''.replace('PEER', json.dumps(peer)).replace('VERIFIED', VERIFIED_WAIT)))
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
    proof.update(script('''async(page)=>{await page.getByLabel('요청 ID',{exact:true}).fill('');await page.getByLabel('비교 코드',{exact:true}).fill('');
      if(await page.getByLabel('비교 코드',{exact:true}).inputValue()!==''||await page.getByLabel('요청 ID',{exact:true}).inputValue()!=='')throw new Error('comparison control was not cleared');return {comparison_controls_cleared_before_capture:true}}'''))
    cli(['screenshot', '--filename', str(runtime / 'desktop.png')])
    script('''async(page)=>{await page.setViewportSize({width:390,height:844});return true}''')
    cli(['screenshot', '--filename', str(runtime / 'mobile.png')])
    proof.update({'desktop_screenshot': str(runtime / 'desktop.png'), 'mobile_screenshot': str(runtime / 'mobile.png'), 'screenshot_evidence': True})
    # Memory-only and controlled storage-write failure are real browser contexts.
    for name, remember, storage_failure in [('memory-only', False, False), ('storage-failure', True, True)]:
        script('''async(page)=>{const modeContext=await page.context().browser().newContext();const modePage=await modeContext.newPage();await modePage.goto(BASE);if(FAIL)await modePage.evaluate(()=>{Storage.prototype.setItem=function(){throw new DOMException('controlled write failure','QuotaExceededError')}});return true}'''.replace('BASE', json.dumps(local)).replace('FAIL', str(storage_failure).lower()))
        pair(name, 'page.context().browser().contexts().find(context=>context!==page.context()).pages()[0]', remember)
        script('''async(page)=>{const modeContext=page.context().browser().contexts().find(context=>context!==page.context());const modePage=modeContext.pages()[0];if(!await modePage.evaluate(()=>localStorage.getItem('haeryong.owner-session.v1')===null))throw new Error('memory-only token persisted');await modePage.reload();await modePage.waitForFunction(()=>UNAUTHENTICATED);await modeContext.close();return true}'''.replace('UNAUTHENTICATED', UNAUTHENTICATED_WAIT))
        proof[name.replace('-', '_')] = True
    control({'action': 'advance', 'seconds': 61})
    lost = script('''async(page)=>{
      const context=await page.context().browser().newContext(), target=await context.newPage();await target.goto(BASE);
      await target.evaluate(()=>{window.pollCalls=0;window.tokenPolls=0;window.pollCallsAtToken=0});
      await target.route('**/auth/pairings/*/poll',async route=>{const response=await route.fetch();const value=await response.json();
        if(value.data?.token){await target.evaluate(()=>{window.pollCalls++;window.tokenPolls++;window.pollCallsAtToken=window.pollCalls});await route.abort('failed');}
        else{await target.evaluate(()=>{window.pollCalls++;});await route.fulfill({response});}});
      await target.getByLabel('장치 이름',{exact:true}).fill('Lost issuance');const wait=target.waitForResponse(r=>r.url().endsWith('/api/v1/auth/pairings')&&r.request().method()==='POST');
      await target.getByRole('button',{name:'연결 코드 요청',exact:true}).click();const p=(await(await wait).json()).data;return {pairing_id:p.pairing_id,code:p.code};
    }'''.replace('BASE', json.dumps(local)))
    control({'action': 'approve', **lost})
    del lost
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      await target.waitForFunction(()=>{const node=document.querySelector('[aria-label="페어링 진행"]');return node!==null&&node.textContent.includes('ISSUANCE')},null,{timeout:12000});
      const observe=()=>target.evaluate(()=>({pollCalls:window.pollCalls||0,tokenPolls:window.tokenPolls||0,pollCallsAtToken:window.pollCallsAtToken||0,stored:localStorage.getItem('haeryong.owner-session.v1')!==null,phase:document.querySelector('[data-state]')!==null?document.querySelector('[data-state]').getAttribute('data-state'):'none'}));
      const atLoss=await observe();
      if(atLoss.tokenPolls!==1)throw new Error('expected exactly one token-bearing issuance, observed '+JSON.stringify(atLoss));
      await target.waitForTimeout(4500);
      const after=await observe();
      if(after.pollCalls!==atLoss.pollCalls)throw new Error('client polled again after the uncertain issuance: '+JSON.stringify({atLoss,after}));
      if(after.tokenPolls!==1||after.stored||after.phase!=='none')throw new Error('uncertain issuance was consumed or retried: '+JSON.stringify(after));
      await context.close();return {lost_single_use_issuance_not_retried:true,issuance_observed:after};
    }'''.replace('ISSUANCE', quoted(ISSUANCE_LOST))))
    script('''async(page)=>{await page.goto((await page.evaluate(()=>location.origin))+'/connect');await page.evaluate(()=>localStorage.removeItem('haeryong.owner-session.v1'));await page.reload();await page.waitForFunction(()=>UNAUTHENTICATED);return true}'''.replace('UNAUTHENTICATED', UNAUTHENTICATED_WAIT))
    proof['storage_loss_connect'] = True
    pair('Expiry owner')
    control({'action': 'expiry', 'seconds': 1})
    script('''async(page)=>{await page.waitForFunction(()=>document.querySelector('[data-state]')===null,null,{timeout:9000});
      if(!await page.evaluate(()=>localStorage.getItem('haeryong.owner-session.v1')===null))throw new Error('expired stored token retained');return true}''')
    proof['optional_expiry'] = True

    # TASK-003-01: capabilities mismatch / generation change through the alias proxy.
    control({'action': 'expiry', 'seconds': 0})
    stop(); ready = start(fixture_port(local), alias=alias_origin)
    FaultProxy.target = ('127.0.0.1', fixture_port(ready['endpoint']))
    script('''async(page)=>{const context=await page.context().browser().newContext(),target=await context.newPage();await target.goto(ALIAS);return true}'''.replace('ALIAS', json.dumps(alias_origin)))
    attach_console_watch("page.context().browser().contexts().find(context=>context!==page.context()).pages()[0]")
    pair('Compatibility owner', 'page.context().browser().contexts().find(context=>context!==page.context()).pages()[0]', True)
    FaultProxy.mode = 'api2'
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      await target.goto(ALIAS+'/connect');
      await target.waitForFunction(()=>document.querySelector('[data-state]')&&document.querySelector('[data-state]').getAttribute('data-state')==='incompatible',null,{timeout:12000});
      if(!await target.evaluate(()=>document.body.textContent.includes('INCOMPATIBLE')))throw new Error('incompatibility copy missing');
      for(const name of ['비공개 상태 확인','로그아웃','장치 목록 새로고침']){const button=target.getByRole('button',{name,exact:true});if(await button.count()>0&&!await button.first().isDisabled())throw new Error('write affordance enabled under incompatible contract: '+name)}
      if(!await target.evaluate(()=>Boolean(localStorage.getItem('haeryong.owner-session.v1'))))throw new Error('credential dropped on compatibility mismatch');
      await target.screenshot({path:SHOT});
      return {api_version_mismatch_visible:true,incompatible_writes_disabled:true,incompatible_credential_retained:true};
    }'''.replace('ALIAS', json.dumps(alias_origin)).replace('INCOMPATIBLE', quoted(INCOMPATIBLE)).replace('SHOT', json.dumps(str(runtime / 'incompatible.png')))))
    FaultProxy.mode = 'off'
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      await target.waitForFunction(()=>VERIFIED,null,{timeout:12000});
      return {mismatch_recovery_after_proxy_cleared:true};
    }'''.replace('VERIFIED', VERIFIED_WAIT)))
    FaultProxy.mode = 'generation'
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      await target.waitForTimeout(6500);
      if(await target.locator('[data-state]').getAttribute('data-state')!=='verified')throw new Error('generation change lost the verified state');
      if(target.qaErrors.length)throw new Error('generation-change scenario observed application errors');
      return {runtime_generation_change_reverified:true};
    }'''))
    FaultProxy.mode = 'off'

    # TASK-003-01: disconnected state disables writes and preserves the credential.
    script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      await target.goto(ALIAS+'/connect');
      await target.waitForFunction(()=>VERIFIED,null,{timeout:12000});
      return true;
    }'''.replace('ALIAS', json.dumps(alias_origin)).replace('VERIFIED', VERIFIED_WAIT))
    stop()
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      await target.waitForFunction(()=>document.body.textContent.includes('DISCONNECTED'),null,{timeout:15000});
      if(await target.locator('[data-state]').getAttribute('data-state')!=='degraded')throw new Error('degraded state not signalled');
      for(const name of ['비공개 상태 확인','로그아웃','장치 목록 새로고침']){const button=target.getByRole('button',{name,exact:true});if(await button.count()>0&&!await button.first().isDisabled())throw new Error('write affordance enabled while disconnected: '+name)}
      if(!await target.evaluate(()=>Boolean(localStorage.getItem('haeryong.owner-session.v1'))))throw new Error('credential dropped while disconnected');
      await target.screenshot({path:SHOT});
      return {disconnected_state_visible:true,disconnected_writes_disabled:true,disconnected_credential_retained:true};
    }'''.replace('DISCONNECTED', quoted(DISCONNECTED)).replace('SHOT', json.dumps(str(runtime / 'degraded.png')))))
    ready = start(fixture_port(local), alias=alias_origin)
    FaultProxy.target = ('127.0.0.1', fixture_port(ready['endpoint']))
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      await target.locator('.banner').getByRole('button',{name:'다시 연결',exact:true}).click();
      await target.waitForFunction(()=>VERIFIED,null,{timeout:15000});
      if(await target.getByRole('button',{name:'비공개 상태 확인',exact:true}).isDisabled())throw new Error('writes stayed disabled after recovery');
      return {reconnect_retry_recovers_verified:true};
    }'''.replace('VERIFIED', VERIFIED_WAIT)))

    # TASK-003-01 (R35): a lost approve acknowledgement keeps its K operation
    # identity through disconnect/reconnect and reconciles via the receipt.
    control({'action': 'advance', 'seconds': 61})
    pending_request = script('''async(page)=>{
      const peerContext=await page.context().browser().newContext(),peer=await peerContext.newPage();await peer.goto(PEER);
      await peer.getByLabel('장치 이름',{exact:true}).fill('Lost ack');
      const wait=peer.waitForResponse(r=>r.url().endsWith('/api/v1/auth/pairings')&&r.request().method()==='POST');
      await peer.getByRole('button',{name:'연결 코드 요청',exact:true}).click();const p=(await(await wait).json()).data;
      await peerContext.close();
      return {pairing_id:p.pairing_id,code:p.code};
    }'''.replace('PEER', json.dumps(peer)))
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      const request=REQUEST;
      await target.route('**/auth/pairings/*/approve',async route=>{await route.fetch();await route.abort('failed');});
      await target.getByLabel('요청 ID',{exact:true}).fill(request.pairing_id);
      await target.getByLabel('비교 코드',{exact:true}).fill(request.code);
      await target.getByRole('button',{name:'장치 승인',exact:true}).click();
      await target.waitForFunction(()=>{const node=document.querySelector('[aria-label="소유자 도구 결과"]');return node!==null&&node.textContent.includes('결과가 확인되지 않았습니다')},null,{timeout:12000});
      await target.waitForFunction(()=>document.body.textContent.includes('같은 요청으로 재시도'));
      return {lost_ack_marked_unknown:true};
    }'''.replace('REQUEST', json.dumps(pending_request))))
    stop()
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      await target.waitForFunction(()=>document.body.textContent.includes('DISCONNECTED'),null,{timeout:15000});
      if(!await target.evaluate(()=>document.body.textContent.includes('결과 미확정 요청이 유지되고 있습니다')))throw new Error('pending intent lost across disconnect');
      return {lost_ack_intent_preserved_while_degraded:true};
    }'''.replace('DISCONNECTED', quoted(DISCONNECTED))))
    ready = start(fixture_port(local), alias=alias_origin)
    FaultProxy.target = ('127.0.0.1', fixture_port(ready['endpoint']))
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      await target.waitForFunction(()=>VERIFIED,null,{timeout:15000});
      await target.unroute('**/auth/pairings/*/approve');
      if(!await target.evaluate(()=>document.body.textContent.includes('같은 요청으로 재시도')))throw new Error('pending intent lost after reconnect');
      await target.getByRole('button',{name:'결과 확인',exact:true}).click();
      await target.waitForFunction(()=>{const node=document.querySelector('[aria-label="소유자 도구 결과"]');return node!==null&&node.textContent.includes('completed')},null,{timeout:12000});
      await target.waitForFunction(()=>!document.body.textContent.includes('같은 요청으로 재시도'),null,{timeout:12000});
      return {lost_ack_reconciled_same_operation:true,intent_cleared_after_receipt:true};
    }'''.replace('VERIFIED', VERIFIED_WAIT)))
    del pending_request

    # TASK-003-01 (C2): a server-committed approval whose delivered 200 body is
    # malformed JSON stays uncertain and reconciles via the same receipt.
    control({'action': 'advance', 'seconds': 61})
    malformed_request = script('''async(page)=>{
      const peerContext=await page.context().browser().newContext(),peer=await peerContext.newPage();await peer.goto(PEER);
      await peer.getByLabel('장치 이름',{exact:true}).fill('Malformed body');
      const wait=peer.waitForResponse(r=>r.url().endsWith('/api/v1/auth/pairings')&&r.request().method()==='POST');
      await peer.getByRole('button',{name:'연결 코드 요청',exact:true}).click();const p=(await(await wait).json()).data;
      await peerContext.close();
      return {pairing_id:p.pairing_id,code:p.code};
    }'''.replace('PEER', json.dumps(peer)))
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      const request=REQUEST;
      await target.goto(ALIAS+'/connect');
      await target.waitForFunction(()=>VERIFIED,null,{timeout:12000});
      await target.route('**/auth/pairings/*/approve',async route=>{await route.fetch();await route.fulfill({status:200,headers:{'content-type':'application/json'},body:'{"data":'});});
      await target.getByLabel('요청 ID',{exact:true}).fill(request.pairing_id);
      await target.getByLabel('비교 코드',{exact:true}).fill(request.code);
      await target.getByRole('button',{name:'장치 승인',exact:true}).click();
      await target.waitForFunction(()=>{const node=document.querySelector('[aria-label="소유자 도구 결과"]');return node!==null&&node.textContent.includes('결과가 확인되지 않았습니다')},null,{timeout:12000});
      await target.waitForFunction(()=>document.body.textContent.includes('같은 요청으로 재시도'));
      await target.unroute('**/auth/pairings/*/approve');
      await target.getByRole('button',{name:'결과 확인',exact:true}).click();
      await target.waitForFunction(()=>{const node=document.querySelector('[aria-label="소유자 도구 결과"]');return node!==null&&node.textContent.includes('completed')},null,{timeout:12000});
      await target.waitForFunction(()=>!document.body.textContent.includes('같은 요청으로 재시도'),null,{timeout:12000});
      return {malformed_success_body_marked_unknown:true,malformed_body_reconciled_same_receipt:true};
    }'''.replace('REQUEST', json.dumps(malformed_request)).replace('ALIAS', json.dumps(alias_origin)).replace('VERIFIED', VERIFIED_WAIT)))
    del malformed_request

    # TASK-003-01 (C2): a server-committed approval whose success headers arrive
    # but whose body read fails (original Content-Length, prefix only, closed).
    control({'action': 'advance', 'seconds': 61})
    truncated_request = script('''async(page)=>{
      const peerContext=await page.context().browser().newContext(),peer=await peerContext.newPage();await peer.goto(PEER);
      await peer.getByLabel('장치 이름',{exact:true}).fill('Truncated body');
      const wait=peer.waitForResponse(r=>r.url().endsWith('/api/v1/auth/pairings')&&r.request().method()==='POST');
      await peer.getByRole('button',{name:'연결 코드 요청',exact:true}).click();const p=(await(await wait).json()).data;
      await peerContext.close();
      return {pairing_id:p.pairing_id,code:p.code};
    }'''.replace('PEER', json.dumps(peer)))
    FaultProxy.mode = 'truncate-approve'
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      const request=REQUEST;
      await target.goto(ALIAS+'/connect');
      await target.waitForFunction(()=>VERIFIED,null,{timeout:12000});
      await target.getByLabel('요청 ID',{exact:true}).fill(request.pairing_id);
      await target.getByLabel('비교 코드',{exact:true}).fill(request.code);
      // Register the exact approval response before clicking: the browser must
      // observe the successful status and headers, then fail reading the body.
      const headersArrived=target.waitForResponse(r=>r.url().endsWith('/approve')&&r.request().method()==='POST');
      await target.getByRole('button',{name:'장치 승인',exact:true}).click();
      const response=await headersArrived;
      if(response.status()!==200)throw new Error('expected successful status, observed '+response.status());
      const length=Number(response.headers()['content-length']||0);
      if(!(length>0))throw new Error('success Content-Length header did not reach the browser');
      let bodyReadFailed=false;
      try{await response.body();}catch{bodyReadFailed=true;}
      if(!bodyReadFailed)throw new Error('browser body read did not fail on the truncated body');
      await target.waitForFunction(()=>{const node=document.querySelector('[aria-label="소유자 도구 결과"]');return node!==null&&node.textContent.includes('결과가 확인되지 않았습니다')},null,{timeout:12000});
      await target.waitForFunction(()=>document.body.textContent.includes('같은 요청으로 재시도'));
      await target.getByRole('button',{name:'결과 확인',exact:true}).click();
      await target.waitForFunction(()=>{const node=document.querySelector('[aria-label="소유자 도구 결과"]');return node!==null&&node.textContent.includes('completed')},null,{timeout:12000});
      await target.waitForFunction(()=>!document.body.textContent.includes('같은 요청으로 재시도'),null,{timeout:12000});
      return {browser_observed_success_status_200:true,browser_observed_content_length:true,browser_body_read_failed:true,truncated_body_marked_unknown:true,truncated_body_reconciled_same_receipt:true};
    }'''.replace('REQUEST', json.dumps(truncated_request)).replace('ALIAS', json.dumps(alias_origin)).replace('VERIFIED', VERIFIED_WAIT)))
    del truncated_request

    # TASK-003-01 (C3): an approval whose pending-list lookup is delayed while
    # the connection becomes unverified is never dispatched: zero approval
    # POSTs while incompatible and no pending intent; after reverification the
    # SAME pairing is approved by an explicit attempt and the retained peer's
    # UI poll consumes the approved issuance.
    control({'action': 'advance', 'seconds': 61})
    delayed_request = script('''async(page)=>{
      const peerContext=await page.context().browser().newContext(),peer=await peerContext.newPage();await peer.goto(PEER);
      await peer.getByLabel('장치 이름',{exact:true}).fill('Delayed lookup');
      const wait=peer.waitForResponse(r=>r.url().endsWith('/api/v1/auth/pairings')&&r.request().method()==='POST');
      await peer.getByRole('button',{name:'연결 코드 요청',exact:true}).click();const p=(await(await wait).json()).data;
      return {pairing_id:p.pairing_id,code:p.code};
    }'''.replace('PEER', json.dumps(peer)))
    script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      const request=REQUEST;
      await target.goto(ALIAS+'/connect');
      await target.waitForFunction(()=>VERIFIED,null,{timeout:12000});
      target.approvePosts=0;
      target.on('request',r=>{if(r.url().endsWith('/approve')&&r.method()==='POST')target.approvePosts++});
      const gate=new Promise(resolve=>{target.releaseList=resolve;});
      await target.route('**/api/v1/auth/pairings',async route=>{
        if(route.request().method()==='GET'){await gate;}
        await route.continue();
      });
      await target.getByLabel('요청 ID',{exact:true}).fill(request.pairing_id);
      await target.getByLabel('비교 코드',{exact:true}).fill(request.code);
      await target.getByRole('button',{name:'장치 승인',exact:true}).click();
      return true;
    }'''.replace('REQUEST', json.dumps(delayed_request)).replace('ALIAS', json.dumps(alias_origin)).replace('VERIFIED', VERIFIED_WAIT))
    FaultProxy.mode = 'api2'
    script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      await target.waitForFunction(()=>document.querySelector('[data-state]')&&document.querySelector('[data-state]').getAttribute('data-state')==='incompatible',null,{timeout:20000});
      return true;
    }''')
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      // Release the delayed list; the surviving click callback resumes while
      // incompatible. OwnerTools is unmounted in this phase, so observe the
      // network and pending-intent state directly.
      const listed=target.waitForResponse(r=>r.url().endsWith('/api/v1/auth/pairings')&&r.request().method()==='GET');
      target.releaseList();
      await(await listed).finished();
      await target.waitForTimeout(250);
      const posts=target.approvePosts;
      if(posts!==0)throw new Error('approval POST dispatched while incompatible: '+posts);
      return {delayed_lookup_blocked_locally:true,zero_approval_posts_while_incompatible:true};
    }'''))
    FaultProxy.mode = 'off'
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      const request=REQUEST;
      // No retry control exists while incompatible; the periodic verify
      // recovers on its own once the proxy is cleared.
      await target.waitForFunction(()=>VERIFIED,null,{timeout:20000});
      if(await target.evaluate(()=>document.body.textContent.includes('같은 요청으로 재시도')))throw new Error('blocked dispatch created a pending intent');
      await target.getByLabel('요청 ID',{exact:true}).fill(request.pairing_id);
      await target.getByLabel('비교 코드',{exact:true}).fill(request.code);
      await target.getByRole('button',{name:'장치 승인',exact:true}).click();
      await target.waitForFunction(()=>{const node=document.querySelector('[aria-label="소유자 도구 결과"]');return node!==null&&node.textContent.includes('요청이 처리되었습니다')},null,{timeout:12000});
      const posts=target.approvePosts;
      if(posts!==1)throw new Error('expected exactly one approval POST after reverification, observed '+posts);
      // The retained peer's real UI poll consumes the approved issuance.
      const peerContext=page.context().browser().contexts().filter(item=>item!==page.context()).pop();
      const peer=peerContext.pages()[0];
      await peer.waitForFunction(()=>VERIFIED,null,{timeout:20000});
      await peerContext.close();
      return {no_pending_from_blocked_dispatch:true,explicit_valid_attempt_same_pairing_succeeds:true,retained_peer_poll_consumed_issuance:true};
    }'''.replace('REQUEST', json.dumps(delayed_request)).replace('VERIFIED', VERIFIED_WAIT)))
    del delayed_request

    # TASK-003-01 (C2): a lost revoke acknowledgement resolves through the
    # natural session_id when the target row itself reports state revoked,
    # without a duplicate or new-target revoke.
    control({'action': 'advance', 'seconds': 61})
    script('''async(page)=>{const peerContext=await page.context().browser().newContext(),target=await peerContext.newPage();await target.goto(PEER);return true}'''.replace('PEER', json.dumps(peer)))
    pair('Revoke lost peer', 'page.context().browser().contexts().filter(context=>context!==page.context()).pop().pages()[0]', True)
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      await target.goto(ALIAS+'/connect');
      await target.waitForFunction(()=>VERIFIED,null,{timeout:12000});
      await target.evaluate(()=>{window.revokeCalls=0});
      await target.route('**/api/v1/sessions/*',async route=>{if(route.request().method()==='DELETE'){await target.evaluate(()=>window.revokeCalls++);const response=await route.fetch();await route.abort('failed');}else{await route.continue();}});
      await target.getByRole('button',{name:'장치 목록 새로고침',exact:true}).click();
      await target.getByRole('button',{name:'해제: Revoke lost peer',exact:true}).click();
      await target.waitForFunction(()=>document.body.textContent.includes('해지 결과가 확인되지 않았습니다'),null,{timeout:12000});
      await target.waitForFunction(()=>document.body.textContent.includes('다시 확인'),null,{timeout:12000});
      await target.unroute('**/api/v1/sessions/*');
      await target.getByRole('button',{name:'다시 확인',exact:true}).click();
      await target.waitForFunction(()=>{const node=document.querySelector('[aria-label="소유자 도구 결과"]');return node!==null&&node.textContent.includes('해지가 확인되었습니다')},null,{timeout:12000});
      const revokeCalls=await target.evaluate(()=>window.revokeCalls);
      if(revokeCalls!==1)throw new Error('expected exactly one server-committed revoke, observed '+revokeCalls);
      const listed=await target.evaluate(async()=>{const token=localStorage.getItem('haeryong.owner-session.v1');const response=await fetch('/api/v1/sessions',{headers:{Authorization:'Bearer '+token}});const value=await response.json();return value.data.items.filter(item=>item.device_label==='Revoke lost peer').map(item=>item.state);});
      if(listed.length!==1||listed[0]!=='revoked')throw new Error('target row state '+JSON.stringify(listed)+' is not revoked');
      return {lost_revoke_ack_unknown:true,revoked_row_confirms_natural_target:true,single_revoke_no_duplicate:true};
    }'''.replace('ALIAS', json.dumps(alias_origin)).replace('VERIFIED', VERIFIED_WAIT)))
    script('''async(page)=>{const context=page.context().browser().contexts().filter(context=>context!==page.context()).pop();await context.close();return true}''')

    # TASK-003-01: routine same-authority rechecks keep the verified screen and
    # partially entered decision inputs intact across intervals and visibility
    # changes; degraded gating still only lifts on a successful recheck.
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      await target.goto(ALIAS+'/connect');
      await target.waitForFunction(()=>VERIFIED,null,{timeout:12000});
      const id=target.getByLabel('요청 ID',{exact:true}),code=target.getByLabel('비교 코드',{exact:true});
      await id.fill('partial-request');
      await code.fill('12');
      await target.waitForTimeout(9500);
      if(await target.locator('[data-state]').getAttribute('data-state')!=='verified')throw new Error('routine recheck disturbed the verified phase');
      if(await id.inputValue()!=='partial-request'||await code.inputValue()!=='12')throw new Error('routine recheck lost partially entered decision inputs');
      if(await id.isDisabled())throw new Error('decision inputs disabled without a pending intent');
      // Real visibility transition: another page takes the foreground, then the
      // target returns; the browser fires the actual visibilitychange path.
      await id.blur();
      const other=await target.context().newPage();
      await other.bringToFront();
      let visibilityKind='synthetic-event-handler';
      // rAF-based polling stops on hidden pages; observe with timed polling.
      const becameHidden=await target.waitForFunction(()=>document.hidden===true,null,{timeout:5000,polling:100}).then(()=>true,()=>false);
      if(becameHidden){
        await target.bringToFront();
        await target.waitForFunction(()=>document.hidden===false,null,{timeout:5000});
        visibilityKind='browser-tab-focus';
      }else{
        // This context kept the page visible; exercise the real
        // visibilitychange handler without claiming an actual tab refocus.
        await target.evaluate(()=>document.dispatchEvent(new Event('visibilitychange')));
      }
      await other.close();
      await id.focus();
      await target.waitForTimeout(1200);
      if(await target.locator('[data-state]').getAttribute('data-state')!=='verified')throw new Error('visibility recheck disturbed the verified phase');
      if(await id.inputValue()!=='partial-request'||await code.inputValue()!=='12')throw new Error('visibility recheck lost partially entered inputs');
      if(target.qaErrors.length)throw new Error('recheck stability scenario observed application errors');
      await id.fill('');await code.fill('');
      const kind=visibilityKind;
      await context.close();
      return {recheck_keeps_verified_screen:true,recheck_preserves_partial_inputs:true,visibility_kind:kind};
    }'''.replace('ALIAS', json.dumps(alias_origin)).replace('VERIFIED', VERIFIED_WAIT)))

    # TASK-003-01: keyboard walkthrough of the changed Connect flow.
    control({'action': 'advance', 'seconds': 61})
    proof.update(script('''async(page)=>{
      const context=await page.context().browser().newContext({viewport:{width:1440,height:900}}),target=await context.newPage();
      target.qaErrors=[];target.on('pageerror',error=>target.qaErrors.push(error.message));
      await target.goto(BASE);
      const label=target.getByLabel('장치 이름',{exact:true});
      await label.focus();
      if(await target.evaluate(()=>document.activeElement!==document.querySelector('input[aria-label="장치 이름"]')))throw new Error('keyboard focus did not reach the device label');
      await target.keyboard.type('Keyboard owner');
      await target.keyboard.press('Tab');
      if(await target.evaluate(()=>document.activeElement!==document.querySelector('input[aria-label="이 장치 기억"]')))throw new Error('tab order skipped the remember control');
      await target.keyboard.press(' ');
      await target.keyboard.press('Tab');
      if(!await target.evaluate(()=>{const node=document.activeElement;return node&&node.tagName==='BUTTON'&&node.textContent==='연결 코드 요청'}))throw new Error('tab order skipped the request button');
      const created=target.waitForResponse(r=>r.url().endsWith('/api/v1/auth/pairings')&&r.request().method()==='POST');
      await target.keyboard.press('Enter');
      await created;
      await target.waitForFunction(()=>document.querySelector('[aria-label="비교 코드"]')!==null,null,{timeout:9000});
      await target.keyboard.press('Escape');
      await target.waitForFunction(()=>{const node=document.querySelector('[aria-label="페어링 진행"]');return node!==null&&node.textContent.includes('STOPPED')},null,{timeout:9000});
      if(!await target.evaluate(()=>{const node=document.activeElement;return node&&node.tagName==='BUTTON'&&node.textContent==='연결 코드 요청'}))throw new Error('focus did not return to the request button after Escape');
      if(target.qaErrors.length)throw new Error('keyboard flow observed application errors');
      await target.screenshot({path:SHOT});
      await context.close();
      return {keyboard_pairing_tab_space_enter:true,keyboard_escape_stops_waiting:true,keyboard_focus_targets:true};
    }'''.replace('BASE', json.dumps(local)).replace('STOPPED', quoted(STOPPED)).replace('SHOT', json.dumps(str(runtime / 'keyboard.png')))))

    # TASK-003-01: touch walkthrough (emulated) of navigation with 44px targets.
    script('''async(page)=>{const context=await page.context().browser().newContext({hasTouch:true,viewport:{width:820,height:1180}}),target=await context.newPage();await target.goto(BASE);return true}'''.replace('BASE', json.dumps(local)))
    attach_console_watch("page.context().browser().contexts().find(context=>context!==page.context()).pages()[0]")
    pair('Touch owner', 'page.context().browser().contexts().find(context=>context!==page.context()).pages()[0]', True)
    proof.update(script('''async(page)=>{
      const context=page.context().browser().contexts().find(context=>context!==page.context()),target=context.pages()[0];
      await target.waitForFunction(()=>VERIFIED,null,{timeout:12000});
      const link=target.getByRole('link',{name:'서재',exact:true});
      const box=await link.boundingBox();
      if(!box||box.height<43.5)throw new Error('touch target below 44px: '+JSON.stringify(box));
      const contextInfo=await context.browser().contexts().indexOf(context)>=0?{touch:await target.evaluate(()=>navigator.maxTouchPoints>0),viewport:target.viewportSize()}:null;
      if(!contextInfo||!contextInfo.touch)throw new Error('touch context does not report touch capability');
      if(!contextInfo.viewport||contextInfo.viewport.width!==820||contextInfo.viewport.height!==1180)throw new Error('touch viewport mismatch: '+JSON.stringify(contextInfo.viewport));
      await target.touchscreen.tap(box.x+box.width/2,box.y+box.height/2);
      await target.getByRole('heading',{name:'서재',exact:true}).waitFor();
      await target.getByRole('button',{name:'검색',exact:true}).waitFor();
      await target.screenshot({path:SHOT});
      if(target.qaErrors.length)throw new Error('touch flow observed application errors');
      await context.close();
      return {touch_navigation_tap:true,touch_targets_44px:true};
    }'''.replace('VERIFIED', VERIFIED_WAIT).replace('SHOT', json.dumps(str(runtime / 'touch.png')))))

    proof.update(script('''async(page)=>{if((await page.evaluate(()=>JSON.parse(sessionStorage.getItem('haeryong.qa.errors')||'[]'))).length)throw new Error('Relevant application console/page errors observed');return {console_health:true}}'''))
    proof.update({'interaction': True, 'runtime': str(runtime), 'fixture_classification': 'actual production HTTP/service/SQLite; synthetic policy/child fixtures and loopback fault proxy; real desktop Chrome'})
    (runtime / 'browser-result.json').write_text(json.dumps(proof, indent=2) + '\n')
    if args.receipt:
        args.receipt.write_text(json.dumps(proof, indent=2) + '\n')
        args.receipt.chmod(0o600)
    print(json.dumps(proof))
finally:
    if opened:
        cli(['close'])
    stop_proxy()
    stop()
    shutil.rmtree(socket_dir)
