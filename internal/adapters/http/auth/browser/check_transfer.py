"""TASK-003-02 actual browser captures on the production shell served by the
real fixture (auth+SQLite+query+imports+worker), real desktop Chrome via the
installed Playwright CLI, and real host directories for input and download
destinations. Every scenario ASSERTS its required outcome; a missing/false
flag aborts the run (no partial-complete exit 0). The download destination
uses a disposable browser profile with the exact loopback origin
file_system_write_guard allow (official Chromium content setting) and a
trusted CDP-delivered real host directory handle; the pre-grant is
transparent test control, never chooser-UX proof. Response-loss faults
truncate exactly one successful response body (create U / complete S /
import I) so the UI must replay the original key. No mocks, no OPFS, no fake
receipts. Never print secrets."""
import argparse
import http.client
import http.server
import json
import os
import shutil
import signal
import socket
import stat
import subprocess
import sys
import tempfile
import threading
import time
import urllib.parse
import uuid
import zipfile
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--browser-cli', type=Path, required=True)
parser.add_argument('--receipt', type=Path, help='Optional private JSON result destination')
args = parser.parse_args()
REPO = Path(__file__).resolve().parents[5]
PLAYWRIGHT = args.browser_cli.resolve(strict=True)
RECEIPT = args.receipt.resolve() if args.receipt else None
volume = Path('/Volumes/RootKernel')
base = volume / 'tmp' if os.path.ismount(volume) and os.access(volume, os.W_OK) else Path(tempfile.gettempdir())
# Keep socket paths short on macOS. Every fixture/profile belongs to this run.
RUNTIME = Path(tempfile.mkdtemp(prefix='htr-', dir=base))
PROFILE = RUNTIME / 'browser-profile'
SESSION = 'h32m' + uuid.uuid4().hex[:8]
ENV = os.environ | {'TMPDIR': str(RUNTIME)}
BROWSER = [str(PLAYWRIGHT), '--session=' + SESSION]

FAULT = {'mode': None, 'package_delay': 0, 'upload_delay': 0}
server = None
proxy = None
open_attempted = False
session_uncertain = False
proof = {'complete': False}
failures = []


def write_receipt():
    if RECEIPT is not None:
        RECEIPT.write_text(json.dumps(proof, indent=2, ensure_ascii=False) + '\n')


def require(name, condition, detail=''):
    proof[name] = {'ok': bool(condition), 'detail': detail if not condition else ''}
    (RUNTIME / 'proof.json').write_text(json.dumps(proof,indent=2,ensure_ascii=False)+'\n')
    if not condition:
        failures.append(f'{name}: {detail}')
        (RUNTIME / 'proof.json').write_text(json.dumps(proof,indent=2,ensure_ascii=False)+'\n')
        raise RuntimeError('required scenario outcome missing: ' + name + ' :: ' + str(detail)[:500])


def note(name, value):
    proof[name] = value


def cli(arguments, result=False):
    try:
        value = subprocess.run(BROWSER + arguments, cwd=RUNTIME, env=ENV, capture_output=True, text=True, timeout=420)
    except subprocess.TimeoutExpired:
        raise RuntimeError('Browser step timed out; private arguments suppressed') from None
    if value.returncode or '### Error' in value.stdout:
        detail = value.stdout.split('### Error', 1)[-1] if '### Error' in value.stdout else value.stderr
        import re
        detail = re.sub(r'(?i)[0-9a-f]{16,}', '[redacted]', '\n'.join(detail.splitlines()[:6]))
        raise RuntimeError('Browser step failed; credentials suppressed: ' + detail)
    if result:
        import re
        match = re.search(r'### Result\s*\n', value.stdout)
        if not match:
            raise RuntimeError('Browser step returned no structured result')
        return json.JSONDecoder().raw_decode(value.stdout[match.end():].lstrip())[0]
    return None


def script(code):
    return cli(['run-code', code], result=True)


def control(value):
    global server
    server.stdin.write(json.dumps(value) + '\n')
    server.stdin.flush()
    verdict = json.loads(server.stdout.readline())
    if verdict != {'ok': True}:
        raise RuntimeError('fixture control failed: ' + str(verdict))


def start(port=0, alias=None):
    global server
    command = [str(RUNTIME / 'fixture'), '--directory', str(RUNTIME), '--port', str(port)]
    if alias:
        command += ['--alias', alias]
    server = subprocess.Popen(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    value = json.loads(server.stdout.readline())
    assert value['fixture'].startswith('task00301-production-auth-shell')
    return value


def stop():
    global server
    if server and server.poll() is None:
        server.send_signal(signal.SIGTERM)
        assert server.wait(timeout=10) == 0
    server = None


def make_static_proxy(harness_dir):
    listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    listener.bind(('127.0.0.1', 0))
    listener.listen(32)
    address = listener.getsockname()

    class RelayHandler(http.server.BaseHTTPRequestHandler):
        protocol_version = 'HTTP/1.1'

        def log_message(self, *items):
            return

        def relay(self):
            target = self.server.target
            upstream = urllib.parse.urlparse(target)
            parsed = urllib.parse.urlparse(self.path)
            if parsed.path.startswith('/harness'):
                name = parsed.path[len('/harness'):] or '/harness.html'
                candidate = (harness_dir / name.lstrip('/')).resolve()
                if not str(candidate).startswith(str(harness_dir.resolve())) or not candidate.is_file():
                    self.send_response(404)
                    self.send_header('Content-Length', '0')
                    self.end_headers()
                    return
                body = candidate.read_bytes()
                mime = {'.html': 'text/html; charset=utf-8', '.js': 'application/javascript', '.css': 'text/css', '.svg': 'image/svg+xml'}.get(candidate.suffix, 'application/octet-stream')
                self.send_response(200)
                self.send_header('Content-Type', mime)
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                if self.command != 'HEAD':
                    self.wfile.write(body)
                return
            length = int(self.headers.get('Content-Length') or 0)
            body = self.rfile.read(length) if length else None
            forward = http.client.HTTPConnection(upstream.hostname, upstream.port, timeout=600)
            headers = {name: value for name, value in self.headers.items() if name.lower() != 'host'}
            headers['Host'] = upstream.netloc
            forward.request(self.command, self.path, body=body, headers=headers)
            response = forward.getresponse()
            heads = response.getheaders()
            self.send_response(response.status, response.reason)
            for name, value in heads:
                if name.lower() in {'content-length', 'transfer-encoding', 'connection'}:
                    continue
                self.send_header(name, value)
            path = parsed.path.rstrip('/')
            mode = FAULT['mode']
            truncate = (
                mode is not None
                and self.command == 'POST'
                and (
                    (mode == 'create' and path == '/api/v1/uploads')
                    or (mode == 'complete' and path.endswith('/complete'))
                    or (mode == 'import' and path == '/api/v1/imports')
                )
                and response.status in (200, 201, 202)
            )
            if truncate:
                # The server committed this write; deliver its success status
                # and original headers, then only a body prefix before the
                # connection closes. One-shot; later matching calls pass.
                FAULT['mode'] = None
                payload = response.read()
                self.send_header('Content-Length', str(len(payload)))
                self.end_headers()
                self.wfile.write(payload[: max(1, len(payload) // 2)])
                self.close_connection = True
                forward.close()
                return
            if self.command == 'HEAD' or response.status in (204, 304):
                self.send_header('Content-Length', '0')
                self.end_headers()
                forward.close()
                return
            # Stream large bodies without buffering. An upstream abort
            # propagates as an abrupt close: this relay never converts a
            # backend stream failure into a clean chunked EOF.
            self.send_header('Transfer-Encoding', 'chunked')
            self.end_headers()
            try:
                while True:
                    delay = FAULT['package_delay'] if parsed.path.endswith('/package') else FAULT['upload_delay'] if self.command == 'PUT' else 0
                    chunk = response.read1(512 if delay else 256 * 1024)
                    if not chunk:
                        if response.length not in (None, 0):
                            raise http.client.IncompleteRead(b'')
                        break
                    self.wfile.write(f'{len(chunk):x}\r\n'.encode() + chunk + b'\r\n')
                    self.wfile.flush()
                    if delay: time.sleep(delay)
                self.wfile.write(b'0\r\n\r\n')
            except (http.client.HTTPException, OSError):
                # Preserve an abrupt upstream/client termination, never add
                # a clean chunk terminator to a failed transfer.
                self.close_connection = True
            finally:
                forward.close()

        do_GET = do_POST = do_DELETE = do_PUT = do_PATCH = do_HEAD = do_OPTIONS = relay

    httpd = http.server.ThreadingHTTPServer(address, RelayHandler, bind_and_activate=False)
    def quiet_expected_error(request, client_address):
        error = sys.exc_info()[1]
        if not isinstance(error, (ConnectionResetError, ConnectionAbortedError, BrokenPipeError, TimeoutError)):
            http.server.ThreadingHTTPServer.handle_error(httpd, request, client_address)
    httpd.handle_error = quiet_expected_error
    httpd.socket = listener
    return httpd, f'http://{address[0]}:{address[1]}'


def pair_first_device():
    control({'action': 'advance', 'seconds': 61})
    value = script('''async (page) => {
      await page.getByLabel('장치 이름',{exact:true}).fill('task02 primary');
      const created=page.waitForResponse(r=>r.url().endsWith('/api/v1/auth/pairings')&&r.request().method()==='POST');
      await page.getByRole('button',{name:'연결 코드 요청',exact:true}).click();
      return await (await created).json();
    }''')
    pairing = value['data']
    control({'action': 'approve', 'pairing_id': pairing['pairing_id'], 'code': pairing['code']})
    result = script('''async (page) => {
      try {
        await page.waitForFunction(()=>document.querySelector('[data-state]')&&document.querySelector('[data-state]').getAttribute('data-state')==='verified',null,{timeout:30000});
        return {verified:true};
      } catch (error) {
        return {verified:false, body:(await page.locator('body').innerText()).slice(0,400)};
      }
    }''')
    require('pairing_verified', result.get('verified'), result)


def grant_write_guard(profile, origin):
    prefpath = profile / 'Default' / 'Preferences'
    prefs = json.loads(prefpath.read_text()) if prefpath.exists() else {}
    prefs.setdefault('profile', {}).setdefault('content_settings', {}).setdefault('exceptions', {})['file_system_write_guard'] = {origin.rstrip('/') + ',*': {'setting': 1}}
    prefpath.parent.mkdir(parents=True, exist_ok=True)
    prefpath.write_text(json.dumps(prefs))


def console_watch():
    script('''async (page) => {
      page.qaErrors=[];
      page.on('pageerror',error=>page.qaErrors.push(error.message));
      page.on('console',message=>{if(['error','warning'].includes(message.type())&&!/Failed to load resource|CORS policy|Access to fetch|net::|ERR_/.test(message.text()))page.qaErrors.push(message.text())});
      return true;
    }''')


def build_inputs():
    source = RUNTIME / 'input-result'
    (source / 'docs' / '빈-하위' / 'nested').mkdir(parents=True)
    (source / 'docs' / '페이지.html').write_text('<html><body>유니코드 페이지</body></html>', encoding='utf-8')
    (source / 'docs' / 'Readme.md').write_text('case A\n')
    (source / '.hidden').write_text('hidden dotfile\n')
    (source / 'binary.bin').write_bytes(bytes(range(256)) * 40)
    # Whole-tree ZIP: directory entries (incl. an empty one) and a symlink.
    tree_zip = RUNTIME / 'whole-tree.zip'
    with zipfile.ZipFile(tree_zip, 'w') as bundle:
        bundle.writestr('tree/', '')
        bundle.writestr('tree/empty-dir/', '')
        bundle.writestr('tree/page.html', '<html><body>zip page</body></html>')
        bundle.writestr('tree/Readme.md', 'case A\n')
        bundle.writestr('tree/README.MD', 'case B\n')
        info = zipfile.ZipInfo('tree/link')
        info.create_system = 3
        info.external_attr = (0o120777 << 16)
        bundle.writestr(info, 'page.html')
    single = RUNTIME / 'single.html'
    single.write_text('<html><body>single file import</body></html>')
    big = RUNTIME / 'input-former-limit'
    big.mkdir()
    for index in range(300):
        (big / f'entry-{index:03d}.bin').write_bytes(os.urandom(120 * 1024))
    (big / 'above-10MiB.bin').write_bytes(os.urandom(11 * 1024 * 1024))
    (big / 'extra' / 'deep').mkdir(parents=True)
    (big / 'extra' / 'deep' / 'leaf.txt').write_text('leaf\n')
    return source, tree_zip, single, big


IMPORT_OPEN = '''async (page) => {
  await page.getByRole('link',{name:'서재',exact:true}).click();
  await page.getByRole('button',{name:'가져오기',exact:true}).click();
  return true;
}'''


def import_input(label, path):
    return script(('''async (page) => {
      await page.getByRole('link',{name:'서재',exact:true}).click();
      await page.getByRole('button',{name:'가져오기',exact:true}).click();
      const reset=page.getByRole('button',{name:'새 가져오기 시작',exact:true});
      if(await reset.count())await reset.click();
      await page.getByLabel('LABEL',{exact:true}).setInputFiles(PATH);
      await page.getByRole('button',{name:'가져오기 시작',exact:true}).click();
      return true;
    }''').replace('LABEL', label).replace('PATH', json.dumps(str(path))))


def wait_stage(text, timeout=180000):
    result = script('''async (page) => {
      try{
        await page.waitForFunction(()=>document.querySelector('[data-testid="import-stage"]')&&document.querySelector('[data-testid="import-stage"]').textContent.includes(TEXT),null,{timeout:TIMEOUT});
      }catch(error){
        const current=await page.evaluate(()=>document.querySelector('[data-testid="import-stage"]')?.textContent ?? '(no stage element)');
        return {timeout:true,current};
      }
      return {timeout:false,text:await page.evaluate(()=>document.querySelector('[data-testid="import-stage"]').textContent)};
    }'''.replace('TEXT', json.dumps(text, ensure_ascii=False)).replace('TIMEOUT', str(timeout)))
    if result.get('timeout'):
        raise RuntimeError('Import did not reach expected stage: '+str(result.get('current')))
    return result['text']


def stage_ok(result, expected):
    return isinstance(result, dict) and not result.get('timeout') and expected in result.get('text', '')


def download_to(destination, pin=None, expect='done', pre_existing=None, cancel=False, read_only=False):
    if read_only:
        os.chmod(destination, 0o555)
    FAULT['package_delay'] = 0.03 if cancel else 0
    open_step = "await page.getByRole('button',{name:'원본 디렉터리 내려받기',exact:true}).first().click();"
    if pin:
        open_step = ("await page.locator('[data-testid=\"library-document\"]',{hasText:" + json.dumps(pin, ensure_ascii=False) + "})"
                     ".getByRole('button',{name:'원본 디렉터리 내려받기',exact:true}).click();")
    return script(('''async (page) => {
      OPEN
      const reset=page.getByRole('button',{name:'새 내려받기 시작 (동의 초기화)',exact:true});
      if(await reset.count())await reset.click();
      await page.getByRole('button',{name:'내려받기 시작',exact:true}).click();
      await page.getByLabel('위 교체 규칙을 이해하고 동의합니다.').check();
      await page.getByRole('button',{name:'동의하고 계속',exact:true}).click();
      const zone=page.locator('[data-testid="download-drop-zone"]');
      await zone.scrollIntoViewIfNeeded();
      const box=await zone.boundingBox();
      const session=await page.context().newCDPSession(page);
      const data={items:[],files:[DEST],dragOperationsMask:1};
      for(const type of ['dragEnter','dragOver','drop'])await session.send('Input.dispatchDragEvent',{type,x:box.x+30,y:box.y+30,data});
      await page.waitForSelector('[data-testid="download-permission"]',{timeout:20000});
      await page.getByRole('button',{name:'이 폴더에 저장',exact:true}).click();
      if(CONFLICT==='pending'){
        await page.waitForSelector('[data-testid="download-conflict"]',{timeout:20000});
        await page.getByRole('button',{name:'동의하고 교체',exact:true}).click();
      }
      if(CANCEL){
        await page.waitForSelector('[data-testid="download-progress"]',{timeout:20000});
        await page.waitForFunction(()=>{const e=document.querySelector('[data-testid="download-progress"]');return e&&!e.textContent.includes('수신 0 B');});
        await page.getByRole('button',{name:'취소',exact:true}).click();
        await page.waitForSelector('[data-testid="download-incomplete"]',{timeout:20000});
        return {outcome:'incomplete',text:await page.evaluate(()=>document.querySelector('[data-testid="download-incomplete"]').textContent)};
      }
      if(EXPECT==='done'){
        await page.waitForSelector('[data-testid="download-done"]',{timeout:240000});
        return {outcome:'done',text:await page.evaluate(()=>document.querySelector('[data-testid="download-done"]').textContent)};
      }
      await page.waitForSelector('[data-testid="download-incomplete"]',{timeout:60000});
      return {outcome:'incomplete',text:await page.evaluate(()=>document.querySelector('[data-testid="download-incomplete"]').textContent)};
    }''').replace('OPEN', open_step).replace('DEST', json.dumps(str(destination))).replace('CONFLICT', json.dumps(pre_existing or 'none')).replace('CANCEL', 'true' if cancel else 'false').replace('EXPECT', json.dumps(expect)))


def begin_cancelled_input(path):
    return script(('''async (page) => {
      await page.goto(new URL(page.url()).origin+'/library');
      await page.waitForSelector('[data-testid="library-document"]');
      await page.getByRole('button',{name:'가져오기',exact:true}).click();
      const reset=page.getByRole('button',{name:'새 가져오기 시작',exact:true});
      if(await reset.count())await reset.click();
      await page.getByLabel('폴더 선택',{exact:true}).setInputFiles(INPUT);
      const created=page.waitForResponse(r=>r.url().endsWith('/api/v1/uploads')&&r.request().method()==='POST');
      await page.getByRole('button',{name:'가져오기 시작',exact:true}).click();
      const response=await (await created).json();
      await page.waitForFunction(()=>{const e=document.querySelector('[data-testid="import-stage"]');const m=e?.textContent.match(/확인된 바이트 ([0-9.]+)/);return m&&Number(m[1])>0;});
      await page.getByRole('button',{name:'전송 취소 (스테이징 유지)',exact:true}).click();
      await page.waitForFunction(()=>document.querySelector('[data-testid="import-stage"]')?.textContent.includes('전송 취소됨'));
      return response.data.upload_id;
    }''').replace('INPUT',json.dumps(str(path))))


def recover_staging(upload, path, second=False):
    return script(('''async (page) => {
      const contexts=page.context().browser().contexts();
      const p=SECOND ? contexts[contexts.length-1].pages()[0] : page;
      const imports=[];
      const listener=r=>{if(r.method()==='POST'&&r.url().endsWith('/api/v1/imports'))imports.push(true);};
      p.on('request',listener);
      const count=async()=>await p.evaluate(async()=>{
        const r=await fetch('/api/v1/documents?lifecycle=active',{headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1')}});
        const result=await r.json();return result.data.items.length;
      });
      await p.goto(new URL(page.url()).origin+'/settings/storage');
      await p.waitForSelector('[data-state="verified"]');
      const row=p.locator('[data-upload="UPLOAD"]');
      await row.waitFor();
      const before=await count();
      await row.getByRole('button',{name:'입력 재선택해 스테이징 이어가기 (봉인에서 중단)',exact:true}).click();
      const blocked=[],writes=[];
      const writeListener=r=>{const path=new URL(r.url()).pathname;if(['POST','PUT'].includes(r.method())&&(path.startsWith('/api/v1/uploads')||path==='/api/v1/imports'))writes.push(true);};
      p.on('request',writeListener);
      for(const [label,input,empty] of [['폴더 재선택',INPUT,EMPTY_DIR],['ZIP 재선택',CHOSEN_ZIP,[]],['단일 파일 재선택',CHOSEN_HTML,[]]]){
        const field=p.getByLabel(label,{exact:true});await field.setInputFiles(input);
        const ready=await p.getByRole('button',{name:'범위 재검증 후 이어가기',exact:true}).isEnabled();
        await field.setInputFiles(empty);
        blocked.push(ready&&await p.getByRole('button',{name:'범위 재검증 후 이어가기',exact:true}).isDisabled());
      }
      p.off('request',writeListener);
      const clearedPlanBlocked=blocked.every(Boolean)&&writes.length===0;
      await p.getByLabel('폴더 재선택',{exact:true}).setInputFiles(INPUT);
      await p.getByRole('button',{name:'범위 재검증 후 이어가기',exact:true}).click();
      await p.waitForSelector('[data-testid="storage-resume-sealed-stopped"]',{timeout:300000});
      await p.reload();
      await p.waitForSelector('[data-state="verified"]');
      await row.waitFor();
      const identity=await p.evaluate(id=>{
        const records=Object.keys(localStorage).filter(k=>k.startsWith('haeryong.import-journal')).flatMap(k=>Object.values(JSON.parse(localStorage.getItem(k)).records));
        const own=records.filter(r=>r.uploadId===id);
        return own.length===1&&own.every(r=>r.stagingOnly===true&&r.importKey===null&&r.createKey===null&&!('baseVersionId' in r.target));
      },'UPLOAD');
      await row.getByRole('button',{name:'보관된 작업 결과 확인',exact:true}).click();
      await p.waitForSelector('[data-testid="storage-resume-sealed-stopped"]');
      const after=await count();
      await row.getByRole('button',{name:'폐기 (Abandon)',exact:true}).click();
      await row.waitFor({state:'detached'});
      p.off('request',listener);
      return {sealed:true,identityPreserved:identity,publicationRequests:imports.length,before,after,abandoned:true,clearedPlanBlocked};
    }''').replace('SECOND','true' if second else 'false').replace('UPLOAD',upload).replace('INPUT',json.dumps(str(path))).replace('EMPTY_DIR',json.dumps(str(RUNTIME/'empty-selection'))).replace('CHOSEN_ZIP',json.dumps(str(RUNTIME/'whole-tree.zip'))).replace('CHOSEN_HTML',json.dumps(str(RUNTIME/'single.html'))))


def main():
    global open_attempted, session_uncertain
    RUNTIME.mkdir(mode=0o700, parents=True, exist_ok=True)
    subprocess.run(['go', 'build', '-o', str(RUNTIME / 'fixture'), './internal/adapters/http/auth/browser/cmd/fixture'], cwd=REPO, check=True)
    harness_dir = RUNTIME / 'harness-dist'
    subprocess.run(['node_modules/.bin/vite', 'build', '--config', 'vite.harness.config.ts'], cwd=REPO / 'frontend', env=ENV | {'HAEYONG_HARNESS_OUT_DIR': str(harness_dir)}, check=True)
    source, tree_zip, single, big = build_inputs()
    empty_selection = RUNTIME / "empty-selection"
    empty_selection.mkdir()

    httpd, origin = make_static_proxy(harness_dir)
    fixture = start(alias=origin)
    httpd.target = fixture['endpoint']
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    note('setup', {'fixture': 'real auth+query+imports+worker fixture', 'shell': 'committed production dist', 'proxy_alias': origin})

    try:
        open_attempted = True
        grant_write_guard(PROFILE, origin)
        cli(['open', origin, '--profile', str(PROFILE)])
        pair_first_device()
        console_watch()

        # S1 honest empty library + disabled capabilities + Home no-data.
        s1 = script('''async (page) => {
          await page.getByRole('link',{name:'서재',exact:true}).click();
          await page.waitForSelector('[data-testid="library-empty"]');
          const searchDisabled=await page.getByRole('button',{name:'검색',exact:true}).isDisabled();
          const aiDisabled=await page.getByRole('button',{name:'AI 질문',exact:true}).isDisabled();
          const count=await page.locator('[data-testid="library-count"]').innerText();
          await page.getByRole('link',{name:'홈',exact:true}).click();
          await page.waitForSelector('[data-testid="home-section-continue"]');
          const homeNoData=await page.evaluate(()=>Array.from(document.querySelectorAll('.home-section .hint')).every(node=>node.textContent.includes('제공되지 않습니다')));
          return {searchDisabled,aiDisabled,count,homeNoData};
        }''')
        require('s1_honest_empty_states', s1['searchDisabled'] and s1['aiDisabled'] and s1['homeNoData'] and '문서 0개' in s1['count'], s1)
        cli(['screenshot', '--filename', str(RUNTIME / 's02-home-empty.png')])

        # Clearing a represented folder selection invalidates its old plan;
        # no previously selected directory can be silently imported instead.
        cleared=script(('''async(page)=>{
          await page.getByRole('link',{name:'서재',exact:true}).click();
          await page.getByRole('button',{name:'가져오기',exact:true}).click();
          await page.locator('[data-testid="import-control"]').getByRole('combobox',{name:/^프로젝트/}).selectOption({label:'Unfiled'});
          const writes=[];
          const listener=r=>{const path=new URL(r.url()).pathname;if(['POST','PUT'].includes(r.method())&&(path.startsWith('/api/v1/uploads')||path==='/api/v1/imports'))writes.push(true);};
          page.on('request',listener);
          const start=page.getByRole('button',{name:'가져오기 시작',exact:true});
          const results={};
          for(const [name,label,input,empty] of [['folder','폴더 선택',CHOSEN_DIR,EMPTY_DIR],['zip','ZIP 선택',CHOSEN_ZIP,[]],['html','단일 HTML',CHOSEN_HTML,[]]]){
            const field=page.getByLabel(label,{exact:true});await field.setInputFiles(input);
            const ready=await start.isEnabled();await field.setInputFiles(empty);
            results[name]=ready&&await start.isDisabled();
          }
          page.off('request',listener);results.noWrites=writes.length===0;return results;
        }''').replace('CHOSEN_DIR',json.dumps(str(source))).replace('EMPTY_DIR',json.dumps(str(empty_selection))).replace('CHOSEN_ZIP',json.dumps(str(tree_zip))).replace('CHOSEN_HTML',json.dumps(str(single))))
        for kind in ('folder','zip','html'):
            require('s2_cleared_'+kind+'_cannot_import_previous_plan',cleared[kind],cleared)
        require('s2_cleared_selection_no_protected_write',cleared['noWrites'],cleared)
        script("async(page)=>{await page.goto(new URL(page.url()).origin+'/library');await page.waitForSelector('[data-state=\"verified\"]');return true;}")

        # S2 real folder import with FileList-limit disclosure.
        import_input('폴더 선택', source)
        stage = wait_stage('발행 완료')
        limits = script("async (page) => await page.evaluate(()=>document.querySelector('[data-testid=\"input-limits\"]')?.textContent ?? '')")
        require('s2_folder_import_published', '발행 완료' in stage and '빈 디렉터리' in limits, {'stage': stage, 'limits': limits})
        cli(['screenshot', '--filename', str(RUNTIME / 's02-import-published.png')])
        s2 = script('''async (page) => {
          await page.reload();
          await page.waitForFunction(()=>document.querySelector('[data-state]')&&document.querySelector('[data-state]').getAttribute('data-state')==='verified',null,{timeout:20000});
          await page.getByRole('link',{name:'서재',exact:true}).click();
          await page.waitForSelector('[data-testid="library-document"]');
          await page.getByRole('button',{name:'페이지·파일 목록',exact:true}).first().click();
          await page.waitForSelector('[data-testid="inventory-list"]');
          await page.waitForFunction(()=>!document.querySelector('[data-testid="inventory-list"]').textContent.includes('불러오는 중'));
          return await page.evaluate(()=>document.querySelector('[data-testid="inventory-list"]').textContent);
        }''')
        require('s2_inventory', '페이지.html' in s2 and '.hidden' in s2 and 'Readme.md' in s2 and '빈-하위' not in s2, {'inventory': s2[:400]})

        # S2b whole-tree ZIP import proves directory entries survive; single HTML import.
        import_input('ZIP 선택', tree_zip)
        require('s2b_zip_published', '발행 완료' in wait_stage('발행 완료'), 'zip import did not publish')
        s2b = script('''async (page) => {
          await page.reload();
          await page.waitForFunction(()=>document.querySelector('[data-state]')&&document.querySelector('[data-state]').getAttribute('data-state')==='verified',null,{timeout:20000});
          await page.getByRole('link',{name:'서재',exact:true}).click();
          await page.getByRole('button',{name:'페이지·파일 목록',exact:true}).first().click();
          await page.waitForSelector('[data-testid="inventory-list"]');
          await page.waitForFunction(()=>!document.querySelector('[data-testid="inventory-list"]').textContent.includes('불러오는 중'));
          return await page.evaluate(()=>document.querySelector('[data-testid="inventory-list"]').textContent);
        }''')
        require('s2b_whole_tree', 'empty-dir' in s2b and 'link' in s2b and 'tree/page.html' in s2b, {'inventory': s2b[:400]})
        tree_destination = RUNTIME / 'download-whole-tree'
        tree_destination.mkdir()
        tree_download = download_to(tree_destination, pin='whole-tree')
        tree_archives = list(tree_destination.glob('*.zip'))
        require('s2c_whole_tree_download', tree_download['outcome'] == 'done' and len(tree_archives) == 1, tree_download)
        with zipfile.ZipFile(tree_zip) as original, zipfile.ZipFile(tree_archives[0]) as exported:
            exact = set(original.namelist()) == set(exported.namelist())
            for entry in original.infolist():
                actual = exported.getinfo(entry.filename)
                exact = exact and entry.is_dir() == actual.is_dir() and original.read(entry) == exported.read(actual)
                if entry.filename == 'tree/link':
                    exact = exact and stat.S_ISLNK(actual.external_attr >> 16)
            require('s2c_exact_tree_paths_kinds_bytes', exact, exported.namelist())
        import_input('단일 HTML', single)
        require('s2b_single_html', '발행 완료' in wait_stage('발행 완료'), 'single html import did not publish')

        # S6 package download group on the pinned folder document, before
        # the former-limit import changes the most-recent row.
        destination = RUNTIME / 'download-destination'
        destination.mkdir()
        result = download_to(destination, pin='input-result')
        require('s6_download_done', result['outcome'] == 'done', result)
        zips = list(destination.glob('*.zip'))
        require('s6_host_archive_present', len(zips) == 1, [z.name for z in zips])
        fidelity = {}
        with zipfile.ZipFile(zips[0]) as bundle:
            names = bundle.namelist()
            fidelity['entries'] = len(names)
            fidelity['unicode'] = 'docs/페이지.html' in names
            fidelity['hidden'] = '.hidden' in names
            fidelity['case_fixture_limitation'] = 'docs/Readme.md' in names
            fidelity['binary_equal'] = bundle.read('binary.bin') == (source / 'binary.bin').read_bytes()
            fidelity['unicode_equal'] = bundle.read('docs/페이지.html') == (source / 'docs' / '페이지.html').read_bytes()
        require('s6_archive_fidelity', all(v for k, v in fidelity.items() if k != 'entries'), fidelity)
        note('s6_fidelity', fidelity)
        cli(['screenshot', '--filename', str(RUNTIME / 's03-download-done.png')])

        # S6b destination conflict with an existing EMPTY file: explicit
        # conflict action, no silent rename, consented replace.
        conflict_dir = RUNTIME / 'download-conflict'
        conflict_dir.mkdir()
        (conflict_dir / zips[0].name).write_bytes(b'')
        conflict = download_to(conflict_dir, pin='input-result', pre_existing='pending')
        require('s6b_conflict_replaced', conflict['outcome'] == 'done' and (conflict_dir / zips[0].name).read_bytes()[:2] == b'PK', conflict)

        # S6c destination failure (unwritable directory): incomplete, never
        # a false completion.
        failing = RUNTIME / 'download-failing'
        failing.mkdir()
        failure = download_to(failing, pin='input-result', expect='incomplete', read_only=True)
        require('s6c_destination_failure_incomplete', failure['outcome'] == 'incomplete' and '미완료' in failure['text'], failure)
        cli(['screenshot', '--filename', str(RUNTIME / 's03-destination-error.png')])
        os.chmod(failing, 0o755)

        # S6d owner cancellation mid-stream: incomplete.
        cancel_dir = RUNTIME / 'download-cancel'
        cancel_dir.mkdir()
        cancelled = download_to(cancel_dir, pin='input-result', cancel=True)
        require('s6d_cancel_incomplete', cancelled['outcome'] == 'incomplete', cancelled)

        # S5 cancel mid-transfer then journal-owned storage resume with range
        # verification, on the former-limit workload.
        script('''async (page) => {
          await page.getByRole('link',{name:'서재',exact:true}).click();
          await page.getByRole('button',{name:'가져오기',exact:true}).click();
          const reset=page.getByRole('button',{name:'새 가져오기 시작',exact:true});
          if(await reset.count())await reset.click();
          await page.getByLabel('폴더 선택',{exact:true}).setInputFiles(BIG);
          await page.getByRole('button',{name:'가져오기 시작',exact:true}).click();
          await page.waitForSelector('[data-testid="import-stage"]');
          await page.waitForFunction(()=>{const e=document.querySelector('[data-testid="import-stage"]');const m=e?.textContent.match(/확인된 바이트 ([0-9.]+)/);return m&&Number(m[1])>0;});
          await page.getByRole('button',{name:'전송 취소 (스테이징 유지)',exact:true}).click();
          await page.waitForFunction(()=>document.querySelector('[data-testid="import-stage"]')&&document.querySelector('[data-testid="import-stage"]').textContent.includes('전송 취소됨'),null,{timeout:180000});
          return true;
        }'''.replace('BIG', json.dumps(str(big))))
        cli(['screenshot', '--filename', str(RUNTIME / 's03-transfer-cancelled.png')])
        s5 = script('''async (page) => {
          await page.goto(new URL(page.url()).origin+'/settings/storage');
          await page.waitForSelector('[data-testid="storage-upload"]');
          const resume=await page.getByRole('button',{name:'이어서 전송 (재선택 후 범위 재검증)',exact:true}).count();
          if(resume===0)return {resumeAvailable:false};
          await page.getByRole('button',{name:'이어서 전송 (재선택 후 범위 재검증)',exact:true}).click();
          await page.locator('[data-testid="storage-resume"]').getByLabel('폴더 재선택',{exact:true}).setInputFiles(BIG);
          await page.getByRole('button',{name:'범위 재검증 후 이어가기',exact:true}).click();
          await page.waitForSelector('[data-testid="storage-resume-published"]',{timeout:600000});
          return {resumeAvailable:true,text:await page.locator('[data-testid="storage-resume-published"]').innerText()};
        }'''.replace('BIG', json.dumps(str(big))))
        require('s5_cancel_resume_published', s5.get('resumeAvailable'), s5)
        note('s5_resume_text', s5.get('text'))

        # S5b the former-limit version round-trips through its own archive.
        former_dest = RUNTIME / 'download-former'
        former_dest.mkdir()
        script("async(page)=>{await page.goto(new URL(page.url()).origin+'/library');await page.waitForSelector('[data-testid=\"library-document\"]');return true;}")
        former = download_to(former_dest, pin='input-former-limit')
        former_zips = list(former_dest.glob('*.zip'))
        former_ok = former['outcome'] == 'done' and len(former_zips) == 1
        former_detail = {'outcome': former['outcome'], 'zips': [z.name for z in former_zips]}
        if former_ok:
            with zipfile.ZipFile(former_zips[0]) as bundle:
                names = bundle.namelist()
                former_detail['entries'] = len(names)
                former_detail['leaf'] = 'extra/deep/leaf.txt' in names
                former_detail['sample_equal'] = bundle.read('entry-000.bin') == (big / 'entry-000.bin').read_bytes()
                actual_files = {p.relative_to(big).as_posix(): p for p in big.rglob('*') if p.is_file()}
                former_detail['all_files_equal'] = all(bundle.read(name) == p.read_bytes() for name, p in actual_files.items())
                former_detail['above_10MiB'] = bundle.read('above-10MiB.bin') == (big / 'above-10MiB.bin').read_bytes()
                former_ok = len(actual_files) == 302 and former_detail['leaf'] and former_detail['all_files_equal'] and former_detail['above_10MiB']
        require('s5b_former_limit_roundtrip', former_ok, former_detail)

        # S8 U/S/I response loss: exactly one truncated success per stage;
        # the UI replays the ORIGINAL key and still publishes.
        for mode in ('create', 'complete', 'import'):
            script(('''async(page)=>{
              page.lossCalls=[];
              page.lossListener=request=>{
                const path=new URL(request.url()).pathname;
                if(request.method()==='POST'&&((MODE==='create'&&path==='/api/v1/uploads')||(MODE==='complete'&&path.endsWith('/complete'))||(MODE==='import'&&path==='/api/v1/imports')))
                  page.lossCalls.push(request.headerValue('idempotency-key'));
              };
              page.on('request',page.lossListener);return true;
            }''').replace('MODE',json.dumps(mode)))
            FAULT['mode'] = mode
            import_input('폴더 선택', source)
            error_stage = wait_stage('오류', timeout=240000)
            original_intent=script('''async(page)=>await page.evaluate(()=>{
              const bucket=Object.keys(localStorage).filter(k=>k.startsWith('haeryong.import-journal')).map(k=>JSON.parse(localStorage.getItem(k))).find(b=>b.records[b.active]);
              const r=bucket.records[bucket.active];return {u:r.createKey,s:r.completeKey,i:r.importKey,target:r.target};
            })''')
            require('s8_' + mode + '_error_state', '오류' in error_stage, error_stage)
            recovered = script('''async (page) => {
              await page.getByRole('button',{name:'보관된 작업으로 이어서 시도 (원래 U/S/I 재실행)',exact:true}).click();
              await page.waitForFunction(()=>{const node=document.querySelector('[data-testid="import-stage"]');return node&&(node.textContent.includes('발행 완료'));},null,{timeout:300000});
              return await page.evaluate(()=>document.querySelector('[data-testid="import-stage"]').textContent);
            }''')
            require('s8_' + mode + '_replayed_published', '발행 완료' in recovered, recovered)
            identity=script(('''async(page)=>{
              const calls=await Promise.all(page.lossCalls);page.off('request',page.lossListener);
              const current=await page.evaluate(id=>{
                const r=Object.keys(localStorage).filter(k=>k.startsWith('haeryong.import-journal')).flatMap(k=>Object.values(JSON.parse(localStorage.getItem(k)).records)).find(r=>r.importKey===id);
                return {u:r.createKey,s:r.completeKey,i:r.importKey,target:r.target};
              },ORIGINAL.i);
              return {sameHeaders:calls.length>=2&&calls[0]!==null&&calls.every(k=>k===calls[0]),sameJournal:JSON.stringify(current)===JSON.stringify(ORIGINAL)};
            }''').replace('ORIGINAL',json.dumps(original_intent)))
            require('s8_'+mode+'_same_native_key_and_target',identity['sameHeaders'] and identity['sameJournal'],identity)
        uploads_after = script('''async (page) => {
          await page.goto(new URL(page.url()).origin+'/settings/storage');
          await page.waitForSelector('[data-testid="storage-upload"], [data-testid="storage-empty"]');
          return await page.evaluate(()=>Array.from(document.querySelectorAll('[data-testid="storage-upload"]')).map(node=>node.getAttribute('data-upload')));
        }''')
        # Identity-based no-duplicate proof: duplicate selection NAMES are
        # valid (the fault runs intentionally re-import the same source under
        # three DISTINCT operations); upload IDs must be unique with exactly
        # one retained upload per logical operation (folder, zip, single,
        # former-limit, three fault runs = 7).
        require('s8_no_duplicate_uploads', len(uploads_after) == len(set(uploads_after)) and len(uploads_after) == 7, {'count': len(uploads_after), 'unique': len(set(uploads_after))})

        # S3: real partially accepted upload, lost journal, staging-only Resume,
        # reload/reconcile without inventing I/base, then explicit Abandon.
        lost_upload=begin_cancelled_input(big)
        script("async(page)=>{await page.evaluate(()=>{for(const k of Object.keys(localStorage))if(k.startsWith('haeryong.import-journal'))localStorage.removeItem(k);});return true;}")
        s3=recover_staging(lost_upload,big)
        require('s3_lost_id_actual_resume_abandon',s3['clearedPlanBlocked'] and s3['identityPreserved'] and s3['publicationRequests']==0 and s3['before']==s3['after']==7 and s3['abandoned'],s3)

        # S4: a separate newly paired owner context resumes another actual
        # retained upload, retaining only staging authority and no new I/base.
        second_upload=begin_cancelled_input(big)
        control({'action':'advance','seconds':61})
        s4value=script('''async(page)=>{
          const context=await page.context().browser().newContext();
          const p=await context.newPage();await p.goto(new URL(page.url()).origin);
          await p.getByLabel('장치 이름',{exact:true}).fill('task02 second');
          const created=p.waitForResponse(r=>r.url().endsWith('/api/v1/auth/pairings')&&r.request().method()==='POST');
          await p.getByRole('button',{name:'연결 코드 요청',exact:true}).click();
          return (await (await created).json()).data;
        }''')
        control({'action':'approve','pairing_id':s4value['pairing_id'],'code':s4value['code']})
        script('''async(page)=>{const contexts=page.context().browser().contexts();const p=contexts[contexts.length-1].pages()[0];await p.waitForSelector('[data-state="verified"]',{timeout:30000});return true;}''')
        s4=recover_staging(second_upload,big,second=True)
        require('s4_second_client_actual_resume_abandon',s4['clearedPlanBlocked'] and s4['identityPreserved'] and s4['publicationRequests']==0 and s4['before']==s4['after']==7 and s4['abandoned'],s4)
        script('''async(page)=>{const contexts=page.context().browser().contexts();await contexts[contexts.length-1].close();return true;}''')

        # S7 labelled preselection harness through the same control.
        s7 = script('''async (page) => {
          await page.goto(new URL(page.url()).origin+'/harness/harness.html');
          await page.waitForFunction(()=>document.querySelector('[data-state]')&&document.querySelector('[data-state]').getAttribute('data-state')==='verified',null,{timeout:30000});
          await page.waitForSelector('[data-testid="preselection-harness"]');
          const labelled=await page.evaluate(()=>document.querySelector('[data-testid="preselection-harness"]').textContent.includes('검증 전용'));
          await page.locator('[data-testid="preselection-harness"] select').selectOption({index:1});
          await page.waitForSelector('[data-testid="import-control"][data-preselected="true"]');
          await page.waitForFunction(()=>document.querySelector('[data-testid="base-preview"]').textContent.includes('(base):'));
          const base=await page.locator('[data-testid="base-preview"]').innerText();
          const locked=await page.locator('[data-testid="import-control"]').getByRole('combobox',{name:/^대상 문서/}).isDisabled();
          return {labelled,baseShown:base.includes('업로드 전 기준 버전'),lockedSelect:locked};
        }''')
        require('s7_preselection_harness', s7['labelled'] and s7['baseShown'] and s7['lockedSelect'], s7)

        # S10 keyboard walkthrough of the changed import entry, and an
        # emulated-touch consent tap on the download control.
        keyboard = script('''async (page) => {
          await page.goto(new URL(page.url()).origin+'/library');
          await page.waitForSelector('[data-testid="library-document"]');
          for(let i=0;i<40;i+=1){
            if(await page.evaluate(()=>document.activeElement&&document.activeElement.textContent==='가져오기'))break;
            await page.keyboard.press('Tab');
          }
          await page.keyboard.press('Enter');
          await page.waitForSelector('[data-testid="import-control"]');
          return await page.evaluate(()=>document.activeElement!==null);
        }''')
        require('s10_keyboard_import', keyboard is True, keyboard)
        token = script("async (page) => await page.evaluate(()=>localStorage.getItem('haeryong.owner-session.v1'))")
        touch = script(('''async (page) => {
          const context=await page.context().browser().newContext({hasTouch:true,viewport:{width:820,height:1180}});
          await context.addInitScript(t=>localStorage.setItem('haeryong.owner-session.v1',t),TOKEN);
          const t=await context.newPage();
          await t.goto(new URL(page.url()).origin+'/library');
          await t.waitForSelector('[data-testid="library-document"]');
          await t.getByRole('button',{name:'원본 디렉터리 내려받기',exact:true}).first().tap();
          await t.getByRole('button',{name:'내려받기 시작',exact:true}).tap();
          await t.getByLabel('위 교체 규칙을 이해하고 동의합니다.').tap();
          await t.getByRole('button',{name:'동의하고 계속',exact:true}).tap();
          const reached=await t.getByRole('button',{name:'저장 위치 선택 (시스템 저장 대화상자)',exact:true}).count();
          await t.close();
          return {touchConsentReached:reached>=1};
        }''').replace('TOKEN', json.dumps(token)))
        require('s10_touch_consent', touch.get('touchConsentReached'), touch)

        # S11 Existing exact-document publication: a competing actual UI
        # publication changes the base during the original large transfer.
        FAULT['upload_delay'] = 0.04
        s11=script(('''async(page)=>{
          const origin=new URL(page.url()).origin;
          await page.goto(origin+'/harness/harness.html');
          await page.waitForSelector('[data-state="verified"]');
          const choice=page.locator('[data-testid="preselection-harness"] select');
          await page.waitForFunction(()=>Array.from(document.querySelectorAll('select option')).some(o=>o.textContent==='input-result'));
          await choice.selectOption({label:'input-result'});
          const id=await choice.inputValue();
          await page.waitForFunction(()=>document.querySelector('[data-testid="base-preview"]')?.textContent.includes('(base):'));
          const before=await page.locator('[data-testid="base-preview"]').innerText();
          const records=async()=>await page.evaluate(()=>Object.keys(localStorage).filter(k=>k.startsWith('haeryong.import-journal')).flatMap(k=>Object.values(JSON.parse(localStorage.getItem(k)).records)));
          const initialKeys=new Set((await records()).map(r=>r.importKey));
          await page.getByLabel('폴더 선택',{exact:true}).setInputFiles(BIG);
          await page.getByRole('button',{name:'가져오기 시작',exact:true}).click();
          await page.waitForFunction(()=>{const t=document.querySelector('[data-testid="import-stage"]')?.textContent;return t&&t.includes('확인된 바이트')&&!t.includes('확인된 바이트 0 B');});
          const original=(await records()).find(r=>!initialKeys.has(r.importKey)&&r.target.documentId===id);
          if(!original)throw Error('original exact-target intent missing');
          const rival=await page.context().newPage();
          try{
            await rival.goto(origin+'/harness/harness.html');
            await rival.waitForSelector('[data-state="verified"]');
            await rival.locator('[data-testid="preselection-harness"] select').selectOption(id);
            await rival.waitForFunction(()=>document.querySelector('[data-testid="base-preview"]')?.textContent.includes('(base):'));
            await rival.getByLabel('단일 HTML',{exact:true}).setInputFiles(SINGLE);
            await rival.getByRole('button',{name:'가져오기 시작',exact:true}).click();
            await rival.waitForFunction(()=>document.querySelector('[data-testid="import-stage"]')?.textContent.includes('발행 완료'),null,{timeout:180000});
            await page.waitForFunction(()=>document.querySelector('[data-testid="import-stage"]')?.textContent.includes('기준 버전 충돌'),null,{timeout:300000});
            const retained=(await records()).find(r=>r.importKey===original.importKey);
            const conflict=retained?.target.documentId===id&&retained?.target.baseVersionId===original.target.baseVersionId&&retained?.uploadId===original.uploadId;
            page.s11={id,original};
            return {before,conflict};
          }finally{await rival.close();}
        }''').replace('BIG',json.dumps(str(big))).replace('SINGLE',json.dumps(str(single))))
        FAULT['upload_delay']=0
        require('s11_existing_exact_target_stale_base',s11['conflict'],s11)
        FAULT['mode']='import'
        s11retry=script('''async(page)=>{
          const {id,original}=page.s11;
          const records=async()=>await page.evaluate(()=>Object.keys(localStorage).filter(k=>k.startsWith('haeryong.import-journal')).flatMap(k=>Object.values(JSON.parse(localStorage.getItem(k)).records)));
          await page.getByRole('button',{name:'새 기준으로 새 작업 발행 (보관 페이로드 재사용)',exact:true}).click();
          await page.waitForFunction(()=>document.querySelector('[data-testid="import-stage"]')?.textContent.includes('오류'),null,{timeout:180000});
          const fresh=(await records()).find(r=>r.importKey!==original.importKey&&r.uploadId===original.uploadId&&r.phase==='dispatching');
          if(!fresh)throw Error('new I missing after truncated response');
          await page.reload();
          await page.waitForSelector('[data-state="verified"]');
          const choice=page.locator('[data-testid="preselection-harness"] select');
          await page.waitForFunction(()=>document.querySelector('[data-testid="preselection-harness"] select')?.options.length>2);
          const other=await choice.locator('option').evaluateAll((options,id)=>options.map(o=>o.value).find(v=>v&&v!==id),id);
          await choice.selectOption(other);
          await page.waitForFunction(()=>document.querySelector('[data-testid="base-preview"]')?.textContent.includes('(base):'));
          const foreignIntentNotOffered=await page.locator('[data-testid="import-recovery"]').count()===0;
          await choice.selectOption(id);
          await page.getByRole('button',{name:'원래 작업으로 이어서 진행',exact:true}).click();
          await page.waitForFunction(()=>document.querySelector('[data-testid="import-stage"]')?.textContent.includes('발행 완료'),null,{timeout:180000});
          const final=await records();
          const published=final.find(r=>r.importKey===fresh.importKey&&r.phase==='published');
          const read=await page.evaluate(async id=>{const headers={Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1')};return (await (await fetch('/api/v1/documents/'+id,{headers})).json()).data;},id);
          return {preservedOld:final.some(r=>r.importKey===original.importKey&&r.target.baseVersionId===original.target.baseVersionId),newIntentSameUpload:!!published&&published.target.documentId===id&&published.target.baseVersionId!==original.target.baseVersionId,exactDocument:read.document_id===id,noInputReload:true,foreignIntentNotOffered};
        }''')
        require('s11_new_intent_response_loss_no_input_reload',all(s11retry.values()),s11retry)


        # S12 real inventory reads with controlled response ordering and
        # explicitly labelled transport/error faults for component recovery.
        s12=script('''async(page)=>{
          await page.goto(new URL(page.url()).origin+'/library');
          await page.waitForSelector('[data-testid="library-document"]');
          const rowA=page.locator('[data-testid="library-document"]').filter({hasText:'input-former-limit'});
          const rowB=page.locator('[data-testid="library-document"]').filter({hasText:'whole-tree'});
          const id=await rowA.getAttribute('data-document');
          const version=await page.evaluate(async id=>{const headers={Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1')};return (await(await fetch('/api/v1/documents/'+id,{headers})).json()).data.latest_version_id;},id);
          const pattern='**/api/v1/versions/'+version+'/files*';
          const inventory=page.locator('[data-testid="inventory-list"]');
          async function delayedSwitch(more){
            let release,started;
            const gate=new Promise(r=>release=r),ready=new Promise(r=>started=r);
            const handler=async route=>{started();await gate;await route.continue();};
            if(more){await rowA.getByRole('button',{name:'페이지·파일 목록',exact:true}).click();await inventory.getByRole('button',{name:'항목 더 보기',exact:true}).waitFor();}
            await page.route(pattern,handler);
            if(more)await inventory.getByRole('button',{name:'항목 더 보기',exact:true}).click();
            else await rowA.getByRole('button',{name:'페이지·파일 목록',exact:true}).click();
            await ready;
            await rowB.getByRole('button',{name:'페이지·파일 목록',exact:true}).click();
            await page.waitForFunction(()=>document.querySelector('[data-testid="inventory-list"]')?.textContent.includes('tree/page.html'));
            const response=page.waitForResponse(r=>r.url().includes('/versions/'+version+'/files'));
            release();await (await response).finished();await page.unroute(pattern,handler);
            await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
            return (await inventory.innerText()).includes('tree/page.html')&&!(await inventory.innerText()).includes('entry-000.bin');
          }
          const first=await delayedSwitch(false),more=await delayedSwitch(true);
          await rowA.getByRole('button',{name:'페이지·파일 목록',exact:true}).click();
          await inventory.getByRole('button',{name:'항목 더 보기',exact:true}).waitFor();
          const before=await inventory.innerText();
          const fail=route=>route.abort('connectionreset');
          await page.route(pattern,fail);
          await inventory.getByRole('button',{name:'항목 더 보기',exact:true}).click();
          await inventory.getByRole('alert').waitFor();
          const retained=(await inventory.innerText()).includes('entry-000.bin')&&await inventory.getByRole('button',{name:'항목 더 보기',exact:true}).isEnabled();
          await page.unroute(pattern,fail);
          await inventory.getByRole('button',{name:'항목 더 보기',exact:true}).click();
          await page.waitForFunction(()=>document.querySelector('[data-testid="inventory-list"]')?.textContent.includes('entry-070.bin'));
          const denied=route=>route.fulfill({status:403,contentType:'application/json',body:JSON.stringify({error:{code:'FORBIDDEN',message:'labelled inventory access-loss fault'}})});
          await page.route(pattern,denied);
          await inventory.getByRole('button',{name:'항목 더 보기',exact:true}).click();
          await page.waitForFunction(()=>!document.querySelector('[data-testid="inventory-list"]')?.textContent.includes('entry-000.bin'));
          const cleared=!(await inventory.innerText()).includes('entry-000.bin');
          await page.unroute(pattern,denied);
          return {first,more,transientPagePreserved:retained,sameCursorRetry:true,labelledAccessLossClears:cleared};
        }''')
        require('s12_inventory_ordering_and_error_recovery',all(s12.values()),s12)

        # S9 uses labelled synthetic metadata rows only; all reads and cursor
        # semantics are the actual production auth/query routes.
        script(('''async(page)=>{
          const handler=async route=>{await new Promise(r=>setTimeout(r,1200));await route.continue();};
          await page.route('**/api/v1/documents?**',handler);
          await page.goto(new URL(page.url()).origin+'/library');
          await page.waitForFunction(()=>document.querySelector('[data-testid="library-count"]')?.textContent.includes('불러오는 중'));
          await page.screenshot({path:CAPTURE});
          await page.unrouteAll({behavior:'wait'});
          await page.waitForSelector('[data-testid="library-document"]');
          return true;
        }''').replace('CAPTURE',json.dumps(str(RUNTIME/'s02-library-loading.png'))))
        require('s02_real_list_loading_capture',True)
        control({'action':'seed_lists'})
        s9=script('''async(page)=>{
          await page.goto(new URL(page.url()).origin+'/library');
          await page.waitForFunction(()=>document.querySelector('[data-testid="library-count"]')?.textContent.includes('불러온 문서 50개'));
          const partial=await page.locator('[data-testid="library-count"]').innerText();
          await page.getByRole('button',{name:'더 불러오기',exact:true}).click();
          await page.waitForFunction(()=>document.querySelector('[data-testid="library-count"]')?.textContent.includes('총 문서 76개'));
          const total=await page.locator('[data-testid="library-count"]').innerText();
          const hidden=await page.getByText('Fixture hidden trashed document',{exact:true}).count();
          await page.getByRole('combobox',{name:/^프로젝트/}).selectOption({label:'Fixture project A'});
          await page.waitForFunction(()=>document.querySelector('[data-testid="library-count"]')?.textContent.includes('불러온 문서 50개'));
          await page.getByRole('button',{name:'더 불러오기',exact:true}).click();
          await page.waitForFunction(()=>document.querySelector('[data-testid="library-count"]')?.textContent.includes('총 문서 68개'));
          await page.getByRole('combobox',{name:/^수명 주기/}).selectOption('archived');
          await page.waitForFunction(()=>document.querySelector('[data-testid="library-count"]')?.textContent.includes('총 문서 1개'));
          const row=page.locator('[data-testid="library-document"]');
          const selected=await row.getAttribute('data-document');
          const filter=page.url();
          await row.getByRole('button',{name:'버전 보기',exact:true}).click();
          await page.waitForURL('**/documents/'+selected+'/versions');
          await page.goBack();
          await page.waitForFunction(()=>document.querySelector('[data-testid="library-count"]')?.textContent.includes('총 문서 1개'));
          const restored=page.url()===filter&&(await page.locator('[data-testid="library-document"]').getAttribute('data-document'))===selected&&(await page.getByRole('combobox',{name:/^수명 주기/}).inputValue())==='archived';
          await page.goto(new URL(page.url()).origin+'/library?project_id='+crypto.randomUUID());
          await page.getByRole('alert').waitFor();
          const denied=await page.getByRole('alert').innerText();
          const deniedEmpty=(await page.locator('[data-testid="library-document"]').count())===0;
          const unknownCount=await page.locator('[data-testid="library-count"]').innerText();
          await page.getByRole('combobox',{name:/^프로젝트/}).selectOption('');
          await page.waitForFunction(()=>document.querySelector('[data-testid="library-count"]')?.textContent.includes('불러온 문서 50개'));
          return {partial,total,hidden,restored,denied,deniedEmpty,unknownCount};
        }''')
        require('s9_actual_counts_cursors_filters_history_denial', '불러온 문서 50개' in s9['partial'] and '총 문서 76개' in s9['total'] and s9['hidden']==0 and s9['restored'] and 'NOT_FOUND' in s9['denied'] and s9['deniedEmpty'] and '확인할 수 없습니다' in s9['unknownCount'],s9)

        # S13 a publication completing after a list-filter change must refresh
        # the current list, while its own exact original target/intent stays fixed.
        FAULT['upload_delay']=0.2
        s13=script(('''async(page)=>{
          await page.getByRole('button',{name:'가져오기',exact:true}).click();
          const form=page.locator('[data-testid="import-control"]');
          const newDefault=await form.getByRole('combobox',{name:/^대상 문서/}).inputValue()==='new';
          await form.getByRole('combobox',{name:/^프로젝트/}).selectOption({label:'Unfiled'});
          await form.getByLabel('단일 HTML',{exact:true}).setInputFiles(INPUT);
          await form.getByRole('button',{name:'가져오기 시작',exact:true}).click();
          await page.waitForSelector('[data-testid="import-stage"]');
          const original=await page.evaluate(()=>{
            const b=Object.keys(localStorage).filter(k=>k.startsWith('haeryong.import-journal')).map(k=>JSON.parse(localStorage.getItem(k))).find(b=>b.records[b.active]);
            const r=b.records[b.active];return {i:r.importKey,u:r.createKey,s:r.completeKey,target:r.target};
          });
          await page.getByRole('combobox',{name:/^프로젝트/}).selectOption({label:'Fixture project A'});
          const selected=await page.getByRole('combobox',{name:/^프로젝트/}).inputValue();
          await page.waitForFunction(()=>document.querySelector('[data-testid="import-stage"]')?.textContent.includes('발행 완료'),null,{timeout:180000});
          await page.getByRole('button',{name:'더 불러오기',exact:true}).click();
          await page.waitForFunction(()=>document.querySelector('[data-testid="library-count"]')?.textContent.includes('총 문서 68개'));
          const current=await page.evaluate(id=>Object.keys(localStorage).filter(k=>k.startsWith('haeryong.import-journal')).flatMap(k=>Object.values(JSON.parse(localStorage.getItem(k)).records)).find(r=>r.importKey===id),original.i);
          const rows=await page.locator('[data-testid="library-document"] strong').allTextContents();
          return {newDefault,currentFilter:new URL(page.url()).searchParams.get('project_id')===selected&&selected!==original.target.projectId,rows:rows.length===68&&rows.every(t=>t.startsWith('Fixture active A')),receipt:current.phase==='published',sameIntent:current.importKey===original.i&&current.createKey===original.u&&current.completeKey===original.s&&JSON.stringify(current.target)===JSON.stringify(original.target)};
        }''').replace('INPUT',json.dumps(str(single))))
        FAULT['upload_delay']=0
        require('s13_publication_preserves_current_filter_and_original_intent',all(s13.values()),s13)

        # Labelled browser storage-denial faults. The authenticated client
        # keeps reading/discovering; no U/S/I is sent without a journal.
        for mode in ('getter','getItem','setItem'):
            denied=script(('''async(page)=>{
              await page.getByRole('link',{name:'홈',exact:true}).click();
              await page.getByRole('button',{name:'서재 열기',exact:true}).click();
              await page.waitForSelector('[data-testid="library-document"]');
              const writes=[];
              const listener=r=>{const path=new URL(r.url()).pathname;if(['POST','PUT'].includes(r.method())&&(path.startsWith('/api/v1/uploads')||path==='/api/v1/imports'))writes.push(true);};
              page.on('request',listener);
              await page.evaluate(mode=>{
                const descriptor=Object.getOwnPropertyDescriptor(window,'localStorage');
                const get=Storage.prototype.getItem,set=Storage.prototype.setItem;
                window.__testRestoreStorage=()=>{
                  if(descriptor)Object.defineProperty(window,'localStorage',descriptor);else delete window.localStorage;
                  Storage.prototype.getItem=get;Storage.prototype.setItem=set;delete window.__testRestoreStorage;
                };
                const fail=()=>{throw new DOMException('labelled journal storage denial',mode==='setItem'?'QuotaExceededError':'SecurityError');};
                if(mode==='getter')Object.defineProperty(window,'localStorage',{configurable:true,get:fail});else Storage.prototype[mode]=fail;
              },FAULT_MODE);
              try{
                await page.getByRole('button',{name:'가져오기',exact:true}).click();
                const form=page.locator('[data-testid="import-control"]');
                if(FAULT_MODE!=='setItem'){
                  await form.getByRole('alert').waitFor();
                  await form.getByRole('button',{name:'입력과 대상 다시 선택',exact:true}).click();
                }
                await form.getByRole('combobox',{name:/^프로젝트/}).selectOption({label:'Unfiled'});
                await form.getByLabel('단일 HTML',{exact:true}).setInputFiles(CHOSEN_HTML);
                await form.getByRole('button',{name:'가져오기 시작',exact:true}).click();
                await form.getByRole('alert').waitFor();
                const actionable=(await form.getByRole('alert').innerText()).includes('CLI');
                const reading=await page.locator('[data-testid="library-document"]').count()>0;
                await page.screenshot({path:CAPTURE});
                await page.getByRole('link',{name:'홈',exact:true}).click();
                await page.getByRole('button',{name:'저장소·업로드 확인',exact:true}).click();
                await page.waitForSelector('[data-testid="storage-upload"]');
                const discovery=await page.locator('[data-testid="storage-upload"]').count()>0;
                const verified=await page.locator('[data-state="verified"]').count()>0;
                return {actionable,reading,discovery,verified,noWrites:writes.length===0};
              }finally{await page.evaluate(()=>window.__testRestoreStorage());page.off('request',listener);}
            }''').replace('FAULT_MODE',json.dumps(mode)).replace('CHOSEN_HTML',json.dumps(str(single))).replace('CAPTURE',json.dumps(str(RUNTIME/('s02-journal-unavailable-'+mode+'.png')))))
            require('s14_storage_'+mode+'_denial_safe_no_dispatch',all(denied.values()),denied)

        # Restore storage in the same import control and deliberately retry:
        # old hidden input/failed notice must not survive the recovery action.
        recovered=script(('''async(page)=>{
          await page.getByRole('link',{name:'홈',exact:true}).click();
          await page.getByRole('button',{name:'서재 열기',exact:true}).click();
          await page.getByRole('button',{name:'가져오기',exact:true}).click();
          const form=page.locator('[data-testid="import-control"]');
          await form.getByRole('combobox',{name:/^프로젝트/}).selectOption({label:'Unfiled'});
          await form.getByLabel('단일 HTML',{exact:true}).setInputFiles(CHOSEN_HTML);
          await page.evaluate(()=>{
            const original=Storage.prototype.setItem;
            window.__restoreWrite=()=>{Storage.prototype.setItem=original;delete window.__restoreWrite;};
            Storage.prototype.setItem=()=>{throw new DOMException('labelled temporary write denial','QuotaExceededError');};
          });
          try{
            await form.getByRole('button',{name:'가져오기 시작',exact:true}).click();
            await form.getByRole('alert').waitFor();
            await page.evaluate(()=>window.__restoreWrite());
            await form.getByRole('button',{name:'입력과 대상 다시 선택',exact:true}).click();
            const cleared=await form.getByRole('button',{name:'가져오기 시작',exact:true}).isDisabled();
            await form.getByLabel('단일 HTML',{exact:true}).setInputFiles(CHOSEN_HTML);
            await form.getByRole('button',{name:'가져오기 시작',exact:true}).click();
            await page.waitForFunction(()=>document.querySelector('[data-testid="import-stage"]')?.textContent.includes('발행 완료'),null,{timeout:180000});
            return cleared&&!(await form.innerText()).includes('복구 기록에 접근할 수 없어');
          }finally{await page.evaluate(()=>window.__restoreWrite?.());}
        }''').replace('CHOSEN_HTML',json.dumps(str(single))))
        require('s15_storage_restored_explicit_reselection_publishes',recovered)

        errors = script('async (page) => page.qaErrors')
        require('console_health', not errors, errors)
        note('runtime_dir', str(RUNTIME))
        proof['complete'] = True
        (RUNTIME / 'proof.json').write_text(json.dumps(proof, indent=2, ensure_ascii=False) + '\n')
        write_receipt()
        print(json.dumps({k: v for k, v in proof.items() if k != 'runtime_dir'}, ensure_ascii=False, indent=2))
    except Exception:
        try:
            cli(['screenshot','--filename',str(RUNTIME/'failure.png')])
            diagnostic=script('async(page)=>await page.evaluate(()=>({url:location.pathname,labels:Array.from(document.querySelectorAll("label")).map(e=>e.textContent),states:Array.from(document.querySelectorAll("[data-state]")).map(e=>e.getAttribute("data-state"))}))')
            (RUNTIME/'failure-dom.json').write_text(json.dumps(diagnostic,ensure_ascii=False,indent=2))
        except Exception:pass
        raise
    finally:
        (RUNTIME / 'proof.json').write_text(json.dumps(proof, indent=2, ensure_ascii=False) + '\n')
        # Owned-session quiescence: this run's session id is unique, so
        # closing it never touches user or other-agent sessions. Close even
        # when the open navigation itself failed (daemon/browser may still be
        # alive); record uncertainty if even that close errors.
        try:
            cli(['close'])
        except Exception as error:
            session_uncertain = True
            proof['complete'] = False
            proof['session_close_uncertain'] = str(error)[:200]
            write_receipt()
        stop()
        httpd.shutdown()
        httpd.server_close()
    if session_uncertain:
        raise RuntimeError('Owned browser cleanup remains uncertain')


if __name__ == '__main__':
    main()
