"""Opt-in Task01 browser proof. Live mode consumes Coordinator-owned private routes;
this driver never changes Tailscale, a production profile, or provider state.
"""
import argparse
import hashlib
import http.client
import http.server
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import signal
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--browser-cli', type=Path, required=True)
parser.add_argument('--scratch-root', type=Path, required=True)
parser.add_argument('--receipt', type=Path, required=True)
parser.add_argument('--screenshot', type=Path)
parser.add_argument('--management-port', type=int)
parser.add_argument('--renderer-pool-base', type=int)
parser.add_argument('--external-base-url')
parser.add_argument('--renderer-origin-1')
parser.add_argument('--renderer-origin-2')
parser.add_argument('--ready-file', type=Path)
parser.add_argument('--serve-ready-file', type=Path)
parser.add_argument('--web-complete-file', type=Path)
parser.add_argument('--serve-removed-file', type=Path)
parser.add_argument('--keep-work', action='store_true')
args = parser.parse_args()
live = bool(args.external_base_url)
if live:
    assert all((args.renderer_origin_1, args.renderer_origin_2, args.ready_file,
                args.serve_ready_file, args.web_complete_file, args.serve_removed_file))
    assert all(urllib.parse.urlsplit(x).scheme == 'https' for x in
               (args.external_base_url, args.renderer_origin_1, args.renderer_origin_2))
else:
    assert not any((args.renderer_origin_1, args.renderer_origin_2, args.ready_file,
                    args.serve_ready_file, args.web_complete_file, args.serve_removed_file))
REPO = Path(__file__).resolve().parents[5]
receipt = args.receipt.resolve()
assert receipt.parent.is_dir() and not receipt.exists()
os.umask(0o077)
work = Path(tempfile.mkdtemp(prefix='h3p7-', dir=args.scratch_root.resolve(strict=True))).resolve()
env = os.environ | {'TMPDIR': str(work), 'HAERYONG_TEST_STATE_DIR': str(work / 'app-state'),
                    'PLAYWRIGHT_DAEMON_SOCKETS_DIR': str(work / 'sockets'),
                    'PWTEST_SOCKETS_DIR': str(work / 'sockets'),
                    'PLAYWRIGHT_DAEMON_SESSION_DIR': str(work / 'registry'),
                    'PYTHONDONTWRITEBYTECODE': '1', 'GOTOOLCHAIN': 'local',
                    'GOPROXY': 'off', 'GOSUMDB': 'off'}
os.environ['HAERYONG_TEST_STATE_DIR'] = env['HAERYONG_TEST_STATE_DIR']
(work / 'sockets').mkdir(mode=0o700)
(work / 'output/playwright').mkdir(parents=True)
(work / 'playwright-cli.json').write_text(json.dumps({'browser': {
    'launchOptions': {'headless': True},
    'contextOptions': {'ignoreHTTPSErrors': False, 'viewport': {'width': 1280, 'height': 900}}}}))
spec = importlib.util.spec_from_file_location('h', REPO / 'tests/e2e/harness.py')
h = importlib.util.module_from_spec(spec)
spec.loader.exec_module(h)
cli = str(args.browser_cli.resolve(strict=True))
session = work.name
product = None
browser_attempted = False
secrets = set()
proof = {'scope': __doc__, 'mode': 'live-tailscale' if live else 'local-deterministic',
         'runtime': str(work), 'complete': False, 'cases': [], 'cleanup': {},
         'physical_second_device': 'not_run', 'physical_mobile': 'not_run',
         'native_Wails_UI': 'not_run', 'provider_calls': 0}


def redact(value):
    for secret in secrets:
        value = value.replace(secret, '[redacted]')
    return value


def fingerprints():
    paths = [p for folder in ('cmd', 'internal', 'skills') for p in (REPO / folder).rglob('*.go')
             if not p.name.endswith('_test.go')]
    paths += [p for p in (REPO / 'internal/webassets/dist').rglob('*') if p.is_file()]
    paths += [p for p in (REPO / 'frontend/src').rglob('*') if p.is_file() and not p.name.endswith('.test.ts')]
    paths += [REPO / 'frontend/check_build.py',
              REPO / 'skills/use-haeryong/SKILL.md',
              REPO / 'internal/application/configuration/schema.json',
              REPO / 'internal/application/import/metadata-schema.json',
              REPO / 'internal/application/storage/defaults.json',
              REPO / 'go.mod', REPO / 'go.sum', Path(__file__), Path(__file__).with_suffix('.js')]
    return {str(p.relative_to(REPO)): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(paths)}


def publish(path, value):
    assert path.parent.is_dir() and not path.exists()
    temporary = path.with_name(path.name + '.' + session + '.tmp')
    h.write_json(temporary, value)
    temporary.replace(path)


def wait_signal(path):
    deadline = time.monotonic() + 300
    while not path.exists():
        assert time.monotonic() < deadline, 'Coordinator route handshake did not complete'
        time.sleep(.1)
    assert json.loads(path.read_text())['session'] == session


def call(arguments, result=False, timeout=90):
    run = subprocess.run([cli, '--session=' + session, *arguments], cwd=work, env=env,
                         capture_output=True, text=True, timeout=timeout)
    if run.returncode or '### Error' in run.stdout:
        detail = run.stdout.split('### Error', 1)[-1].split('### Ran', 1)[0]
        proof['browser_error'] = redact(detail or run.stderr)[-2400:]
        raise AssertionError('Browser operation failed; see private receipt')
    if result:
        match = re.search(r'### Result\s*\n', run.stdout)
        assert match, 'Browser result missing'
        return json.JSONDecoder().raw_decode(run.stdout[match.end():].lstrip())[0]


def js(code, timeout=90):
    return call(['run-code', code], True, timeout=timeout)


def request_url(url, method='GET', headers=None, body=None):
    fields = dict(headers or {})
    data = None
    if body is not None:
        data = json.dumps(body).encode()
        fields.setdefault('Content-Type', 'application/json')
    request = urllib.request.Request(url, data=data, headers=fields, method=method)
    try:
        with urllib.request.urlopen(request, timeout=15) as response:
            return response.status, dict(response.headers), response.read()
    except urllib.error.HTTPError as error:
        return error.code, dict(error.headers), error.read()


def local(method, path, *, expected=(200,), **kwargs):
    status, value, headers = product.request(method, path, **kwargs)
    assert status in expected, 'Unexpected local HTTP status ' + str(status) + ' for ' + path.split('?')[0]
    return value.get('data') if isinstance(value, dict) else value


def identity():
    return product.cli(['service', 'status'])['data']


def wait_restart(previous):
    deadline = time.monotonic() + 90
    while True:
        try:
            current = identity()
            if current['instance_id'] != previous['instance_id']:
                assert current['library_id'] == previous['library_id']
                assert current['profile_id'] == previous['profile_id']
                assert current['endpoint'] == product.endpoint
                return current
        except (AssertionError, OSError, ValueError):
            pass
        assert product.process.poll() is None, 'Owned daemon exited during restart'
        assert time.monotonic() < deadline, 'Controlled restart did not become ready'
        time.sleep(.1)


def configure(changes, label, *, exposure=None, confirm_refusal=False):
    current = local('GET', '/api/v1/configuration', client=owner)
    path = work / (label + '.json')
    h.write_json(path, changes)
    plan = product.cli(['config', 'plan', str(path), '--expected-config-revision',
                        str(current['config_revision'])], client=owner)['data']['result']
    impact = plan['impact']['exposure']
    assert impact['proxy_configuration_changed_by_commit'] is False
    if exposure is not None:
        assert impact['changed'] is exposure
    command = ['config', 'apply', plan['plan_id'], '--expected-config-revision',
               str(plan['base_config_revision'])]
    if confirm_refusal:
        product.cli(command, client=owner, code=2)
        assert local('GET', '/api/v1/configuration', client=owner)['config_revision'] == current['config_revision']
    previous = identity()
    product.cli(command + ['--confirmed'], client=owner)
    if plan['impact']['controlled_restart_required']:
        current_identity = wait_restart(previous)
    else:
        current_identity = identity()
    updated = local('GET', '/api/v1/configuration', client=owner)
    assert updated['config_revision'] == current['config_revision'] + 1
    proof['cases'].append({'kind': label, 'actual': {'impact': plan['impact'],
                           'config_revision': updated['config_revision'],
                           'instance_changed': current_identity['instance_id'] != previous['instance_id'],
                           'confirmation_refusal_checked': confirm_refusal}})
    return plan


def open_render(version):
    key = h.operation()
    value = local('POST', '/api/v1/versions/' + version + '/render-sessions', client=owner,
                  body={}, headers={'Idempotency-Key': key}, expected=(201,))
    secrets.add(value['handoff']['secret'])
    return key, value


def close_render(version, opened):
    local('POST', '/api/v1/versions/' + version + '/render-sessions/' + opened['session_id'] + '/close',
          client=owner, body={}, expected=(204,))


def free_pool():
    if args.renderer_pool_base:
        return args.renderer_pool_base
    while True:
        base = h.port()
        if base < 10000 or base > 65000:
            continue
        held = []
        try:
            for port in range(base, base + 10):
                listener = socket.socket()
                listener.bind(('127.0.0.1', port))
                held.append(listener)
            return base
        except OSError:
            pass
        finally:
            for listener in held:
                listener.close()


def form_check(bind_port, opened):
    secret = opened['handoff']['secret']
    target = opened['handoff']['url'] + '?page=first.html'
    document = ('<!doctype html><meta charset=utf-8><form method=POST action="' + target + '">'
                '<input type=hidden name=secret value="' + secret + '"></form>'
                '<script>document.forms[0].submit()</script>').encode()

    class Form(http.server.BaseHTTPRequestHandler):
        def log_message(self, *unused):
            pass

        def do_GET(self):
            self.send_response(200)
            self.send_header('Content-Type', 'text/html; charset=utf-8')
            self.send_header('Content-Length', str(len(document)))
            self.end_headers()
            self.wfile.write(document)

    server = http.server.ThreadingHTTPServer(('127.0.0.1', bind_port), Form)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    origin = 'http://127.0.0.1:' + str(server.server_port)
    try:
        result = js('''async page => {
            const child = await page.context().newPage();
            try {
                const completed = child.waitForResponse(r => r.request().method() === 'POST' && r.url().startsWith(TARGET));
                await child.goto(FORM);
                const response = await completed;
                return {status: response.status(), origin: response.request().headers().origin,
                        url: response.url()};
            } finally { await child.close(); }
        }'''.replace('TARGET', json.dumps(opened['handoff']['url'])).replace('FORM', json.dumps(origin + '/fixture')))
        assert result['origin'] == origin
        return result
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


def occupied_port():
    product.stop()

    class Occupied(http.server.BaseHTTPRequestHandler):
        def log_message(self, *unused):
            pass

        def do_GET(self):
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b'owned-conflict-sentinel')

    server = http.server.ThreadingHTTPServer(('127.0.0.1', product.address), Occupied)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        result = subprocess.run([product.binary, 'daemon', 'run', *product.local_flags],
                                env=env, capture_output=True, text=True, timeout=30)
        assert result.returncode != 0, 'Occupied configured port unexpectedly started'
        diagnostic = result.stdout + result.stderr
        assert 'address already in use' in diagnostic
        assert 'explicit unused --port' in diagnostic and 'same --profile/--data-dir' in diagnostic
        assert 'persisted network.listener' in diagnostic
        assert request_url(product.endpoint)[2] == b'owned-conflict-sentinel'
        proof['cases'].append({'kind': 'occupied configured listener refuses startup without takeover/fallback',
                               'actual': {'exit': result.returncode, 'sentinel_preserved': True, 'explicit_recovery_guidance': True}})
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
    product.start(configured=True)


def stop_signal(signum, frame):
    raise KeyboardInterrupt('owned driver interrupted')


signal.signal(signal.SIGTERM, stop_signal)
before = fingerprints()
try:
    subprocess.run(['go', 'build', '-tags', 'haeryong_test_state', '-o', str(work / 'haeryong'), './cmd/haeryong'],
                   cwd=REPO, env=env, capture_output=True, check=True, timeout=180)
    assert fingerprints() == before
    proof['binary_sha256'] = hashlib.sha256((work / 'haeryong').read_bytes()).hexdigest()
    product = h.Product(work / 'haeryong', work)
    if args.management_port:
        product.address = args.management_port
        product.endpoint = 'http://127.0.0.1:' + str(product.address)
    product.initialize()
    product.start()
    owner = product.owner('Private ingress local owner')
    secrets.add(owner['token'])
    pool = free_pool()
    for port in range(pool, pool + 10):
        assert port not in (9272, 9273, product.address)
        with socket.socket() as probe:
            probe.bind(('127.0.0.1', port))
    configure({'network': {'listener': {'port': product.address}, 'allowed_origins': [product.endpoint],
                          'renderer_pool': {'base_port': pool, 'count': 2}}}, 'persist-listener-and-initial-renderer-pool')
    product.stop()
    product.start(configured=True)
    assert product.cli(['doctor'], client=owner)['data']['endpoint_checks']['ephemeral_override'] is False
    occupied_port()
    fixture = []
    for index in range(1, 4):
        source = work / ('source-' + str(index))
        (source / 'nested').mkdir(parents=True)
        (source / 'first.html').write_text('''<!doctype html><meta charset=utf-8><title>Private Reader</title>
<link rel=stylesheet href=style.css><h1 id=heading>VERSION first</h1><button id=increment onclick="this.textContent=String(Number(this.textContent)+1)">0</button>
<a id=next href="nested/second.html?scenario=2#part">next</a>
<a id=newtab target=_blank href="nested/second.html?popup=1#part">new tab</a>
<script type=module>import {value} from './module.js'; window.moduleValue=value;
new Worker('./worker.js').onmessage=e=>window.workerValue=e.data;
fetch('/root-asset.txt').then(r=>r.text()).then(t=>window.rootAsset=t);</script>'''.replace('VERSION', 'Version ' + str(index)))
        (source / 'nested/second.html').write_text('<h1 id=second>Second page</h1><h2 id=part>Exact fragment</h2><a href="../first.html">first</a>')
        (source / 'style.css').write_text('#heading{color:rgb(1,2,3)}')
        (source / 'module.js').write_text('export const value="module-running";')
        (source / 'worker.js').write_text('postMessage("worker-running")')
        (source / 'root-asset.txt').write_text('asset-' + str(index))
        _, _, published = product.import_tree(owner, source, extra=('--new-document',))
        fixture.append({'version': published['version_id'], 'document': published['document_id'], 'label': 'Version ' + str(index)})
    _, discovered = open_render(fixture[0]['version'])
    upstream1 = discovered['render_origin']
    assert urllib.parse.urlsplit(upstream1).port == pool
    close_render(fixture[0]['version'], discovered)
    upstream2 = 'http://127.0.0.1:' + str(pool + 1)
    browser_attempted = True
    call(['open', product.endpoint + '/health/live', '--browser', 'chrome', '--profile', str(work / 'browser')])
    proof['browser_version'] = js('async page => page.context().browser().version()')
    _, native_offer = open_render(fixture[0]['version'])
    foreign = form_check(0, native_offer)
    assert foreign['status'] == 403
    companion = form_check(9271, native_offer)
    assert companion['status'] == 200
    close_render(fixture[0]['version'], native_offer)
    proof['cases'].append({'kind': 'actual browser companion-origin handoff boundary',
                           'actual': {'foreign': foreign, 'companion': companion}})
    inputs = {'base': product.endpoint, 'fixture': fixture, 'token': owner['token'], 'phase': 'local',
              'expectedOrigins': [upstream1], 'screenshot': None}
    script = Path(__file__).with_suffix('.js').read_text()
    proof['cases'].append({'kind': 'actual local Reader before private configuration',
                           'actual': js(script.replace('INPUT', json.dumps(inputs)))})
    task02_source = work / 'task02-source-root'
    task02_source.mkdir(exist_ok=True)
    (task02_source / 'note.md').write_text('# Task02 source scope proof\n')

    def run_task02_cohort(target_base, target_renderer1, mode_label):
        origin_denied = request_url(target_base + '/api/v1/session', headers={'Origin': 'https://foreign.invalid'})[0]
        forged_denied = request_url(target_base + '/api/v1/session', headers={'Origin': target_base, 'Tailscale-User-Login': 'owner@example.com', 'X-Forwarded-User': 'admin'})[0]
        assert origin_denied == 403 and forged_denied == 401
        cohort_inputs = inputs | {
            'base': target_base,
            'expectedOrigins': [target_renderer1],
            'phase': 'task02-cohort',
            'disposableSourceRoot': str(task02_source),
            'modeLabel': mode_label,
        }
        # task02-cohort spans multiple 60s CreatePairing rate-limit windows plus a
        # 61.5s session-age wait (~240-260s minimum); scope a 420s timeout to this phase only.
        cohort_result = js(script.replace('INPUT', json.dumps(cohort_inputs)), timeout=420)
        cohort_result['http_foreign_origin_status'] = origin_denied
        cohort_result['http_forged_identity_status'] = forged_denied
        proof['cases'].append({'kind': 'TASK-007-02 pairing, sessions, policy recovery, memory-only and passive-idle expiry (' + mode_label + ')',
                               'actual': cohort_result})
        assert local('GET', '/api/v1/session', client=owner)['session_id'] == owner['session']['session_id']

    if not live:
        run_task02_cohort(product.endpoint, upstream1, 'local-deterministic')
    external = args.external_base_url or 'https://node.tailnet.ts.net:9271'
    renderer1 = args.renderer_origin_1 or 'https://node.tailnet.ts.net:9291'
    renderer2 = args.renderer_origin_2 or 'https://node.tailnet.ts.net:9292'
    shifted = pool + 2 if not live else 9281
    mappings = [{'version_id': fixture[0]['version'], 'origin': renderer1, 'upstream': upstream1, 'reassignable': False},
                {'version_id': fixture[1]['version'], 'origin': renderer2, 'upstream': upstream2, 'reassignable': False}]
    configure({'network': {'remote_enabled': True, 'external_base_url': external,
                          'allowed_origins': [product.endpoint, external],
                          'renderer_pool': {'base_port': shifted, 'count': 8}, 'renderer_mappings': mappings}},
              'private-exposure-preview-confirm-and-restart', exposure=True, confirm_refusal=True)
    doctor = product.cli(['doctor'], client=owner)['data']['endpoint_checks']
    assert doctor['remote_enabled'] and all(m['supported'] for m in doctor['renderer_mappings'])
    assert doctor['runtime_local_endpoint'] == product.endpoint
    proof['cases'].append({'kind': 'authenticated endpoint doctor configured intent only', 'actual': doctor})
    if live:
        publish(args.ready_file, {'session': session, 'pid': product.process.pid,
                                 'management': product.endpoint, 'external': external,
                                 'renderers': [{'origin': renderer1, 'upstream': upstream1}, {'origin': renderer2, 'upstream': upstream2}],
                                 'fixture': fixture, 'runtime': str(work)})
        wait_signal(args.serve_ready_file)
        denied = []
        for fields in ({'Origin': external}, {'Origin': external, 'Tailscale-User-Login': 'owner@example.com'},
                       {'Origin': 'https://foreign.invalid'}):
            denied.append(request_url(external + '/api/v1/session', headers=fields)[0])
        assert denied == [401, 401, 403]
        proof['cases'].append({'kind': 'private reachability and forged network identity do not authenticate', 'actual': denied})
        # Real trusted login UI; the separate local owner approves its exact remote-origin request.
        call(['goto', external + '/documents/' + fixture[0]['document'] + '/versions/' + fixture[0]['version'] + '?page=first.html'])
        call(['snapshot'])
        pairing = js('''async page => {
            await page.getByLabel('장치 이름',{exact:true}).fill('Private browser owner');
            if (!await page.getByLabel('이 장치 기억',{exact:true}).isChecked()) throw Error('remember default absent');
            const ready=page.waitForResponse(r=>r.url().endsWith('/api/v1/auth/pairings')&&r.request().method()==='POST');
            await page.getByRole('button',{name:'연결 코드 요청',exact:true}).click();
            const value=(await (await ready).json()).data;
            return {pairing_id:value.pairing_id, code:value.code, origin:value.origin, etag:value.etag};
        }''')
        local('POST', '/api/v1/auth/pairings/' + pairing['pairing_id'] + '/approve', client=owner,
              body={'code': pairing['code'], 'origin': pairing['origin']},
              headers={'If-Match': pairing['etag'], 'Idempotency-Key': h.operation()}, expected=(200,))
        browser_session = js('''async page => {
            await page.waitForFunction(()=>localStorage.getItem('haeryong.owner-session.v1'));
            const current=await page.evaluate(async()=>{
                const r=await fetch('/api/v1/session',{headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1')}});
                return (await r.json()).data;
            });
            return {session_id:current.session_id, origin:new URL(page.url()).origin};
        }''')
        assert browser_session['origin'] == external
        live_inputs = inputs | {'base': external, 'token': None, 'phase': 'live',
                                'expectedOrigins': [renderer1, renderer2],
                                'screenshot': str(args.screenshot.resolve()) if args.screenshot else None}
        observed = js(script.replace('INPUT', json.dumps(live_inputs)))
        proof['cases'].append({'kind': 'actual private dual Reader, scripts, isolation, links and renew', 'actual': observed})
        boundary = js(script.replace('INPUT', json.dumps(live_inputs | {'phase': 'browser-boundaries'})))
        proof['cases'].append({'kind': 'actual private unconfigured CSP and raw top-level return', 'actual': boundary})
        proof['optional_private_companion_origin_browser'] = 'Unverified: attempted additional owned-form acquisition failed; deterministic Go remote Origin refusal coverage remains separate. No failed cohort is passed.'
        # The original completed Open is replayed, never revived, after an ordinary controlled restart.
        configure({'network': {'renderer_pool': {'base_port': shifted, 'count': 9}}}, 'private-renderer-restart', exposure=False)
        proof['cases'].append({'kind': 'private restart and remembered Reader recovery',
                               'actual': js(script.replace('INPUT', json.dumps(live_inputs | {'phase': 'restart'})))})
        local('DELETE', '/api/v1/sessions/' + browser_session['session_id'], client=owner, expected=(204,))
        revoked = js(script.replace('INPUT', json.dumps(live_inputs | {'phase': 'revoked'})))
        proof['cases'].append({'kind': 'private bearer and narrow delivery lose current authority after local-owner revoke', 'actual': revoked})
        assert local('GET', '/api/v1/session', client=owner)['session_id'] == owner['session']['session_id']
        run_task02_cohort(external, renderer1, 'private-https')
        publish(args.web_complete_file, {'session': session})
        wait_signal(args.serve_removed_file)
        try:
            unreachable = request_url(external + '/health/live')[0] != 200
        except (OSError, urllib.error.URLError):
            unreachable = True
        assert unreachable, 'Removed private proxy still reaches the application'
    recovery = inputs | {'phase': 'local-recovery', 'fixture': [fixture[2]], 'expectedOrigins': None}
    proof['cases'].append({'kind': 'same verified local owner and Reader remain usable without private proxy',
                           'actual': js(script.replace('INPUT', json.dumps(recovery)))})
    assert product.cli(['doctor'], client=owner)['data']['endpoint_checks']['connected_endpoint'] == product.endpoint
    assert fingerprints() == before, 'Verification inputs changed during browser run'
    proof['inputs'] = before
    proof['complete'] = True
except BaseException as error:
    proof['error'] = redact(type(error).__name__ + ': ' + str(error))[:2000]
    raise
finally:
    if browser_attempted:
        try:
            result = subprocess.run([cli, '--session=' + session, 'close'], cwd=work, env=env,
                                    capture_output=True, text=True, timeout=45)
            proof['cleanup']['browser_closed'] = result.returncode == 0
        except Exception as error:
            proof['cleanup']['browser_error'] = type(error).__name__
    if product and product.process:
        try:
            product.stop()
            proof['cleanup']['daemon_exit'] = 0
        except Exception as error:
            proof['cleanup']['daemon_error'] = type(error).__name__
            proof['complete'] = False
            product.close()
    proof['cleanup']['daemon_closed'] = product is None or product.process is None
    if browser_attempted and not proof['cleanup'].get('browser_closed'):
        proof['complete'] = False
    receipt.write_text(json.dumps(proof, ensure_ascii=False, indent=2) + '\n')
    if proof['complete'] and not args.keep_work:
        shutil.rmtree(work)
assert proof['complete'], 'Owned verification cleanup failed; see private receipt'
print(json.dumps({'complete': proof['complete'], 'cases': len(proof['cases']), 'receipt': str(receipt)}))
