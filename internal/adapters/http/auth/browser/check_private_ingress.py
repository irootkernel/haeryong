"""Opt-in private browser proof; --task03-only selects the responsive workflow.
Live mode consumes Coordinator-owned private routes;
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
import sqlite3
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile

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
parser.add_argument('--task03-only', action='store_true', help='Run only TASK-007-03 workflows, excluding the historical Task01/02 cohorts')
parser.add_argument('--task03-stages', help='Comma-separated affected Task03 stages; omitted runs all')
parser.add_argument('--task04-only', action='store_true', help='Run only TASK-007-04 multi-device reconciliation, conflict UI, durable job, resync and passive-idle workflows')
parser.add_argument('--task04-stages', help='Comma-separated affected Task04 stages; omitted runs all')
args = parser.parse_args()
assert not (args.task03_only and args.task04_only), 'Select at most one task-scoped mode'
task03_stages = args.task03_stages.split(',') if args.task03_stages else None
if task03_stages:
    assert args.task03_only and set(task03_stages) <= {'responsive', 'library-search', 'reader-note-review-links', 'outline', 'controlled-ask', 'provider-maintenance', 'transfer', 'faults', 'transfer-faults', 'import-faults', 'download-faults', 'captures', 'library-cursor'}
task04_stages = args.task04_stages.split(',') if args.task04_stages else None
if task04_stages:
    assert args.task04_only and set(task04_stages) <= {'personal-and-conflicts', 'receipt-and-lost-ack', 'durable-job-browser-close', 'resync-and-passive-idle', 'reader-page-recovery', 'reader-pending-resync'}
task03_or_04 = args.task03_only or args.task04_only
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
    if task03_or_04:
        drain_fixture_indexes()
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


def drain_fixture_indexes():
    # Import completion and asynchronous derived-index completion are separate.
    # Do not interrupt the latter with fixture configuration restarts.
    jobs = local('GET', '/api/v1/jobs?limit=100', client=owner)
    assert not jobs.get('next_cursor'), 'Unexpected fixture job page; no partial readiness claim'
    for job in jobs['items']:
        if job['kind'] == 'derived_index':
            product.job(owner, job['job_id'], timeout=60)


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
    product.start(configured=True, provider_binary=nonexistent_provider if task03_or_04 else None)
    if task03_or_04:
        assert not nonexistent_provider.exists()


def stop_signal(signum, frame):
    raise KeyboardInterrupt('owned driver interrupted')


signal.signal(signal.SIGTERM, stop_signal)
nonexistent_provider = work / 'nonexistent-task03-provider-binary'
assert not nonexistent_provider.exists()
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
    product.start(provider_binary=nonexistent_provider if args.task03_only else None)
    if args.task03_only:
        assert not nonexistent_provider.exists()
    owner = product.owner('Private ingress local owner')
    owner_created_at = time.monotonic()
    secrets.add(owner['token'])
    pool = free_pool()
    for port in range(pool, pool + 10):
        assert port not in (9272, 9273, product.address)
        with socket.socket() as probe:
            # Match Go's ordinary reuse after an owned listener/connection ended.
            # An active listener still refuses this bind (no SO_REUSEPORT).
            probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            probe.bind(('127.0.0.1', port))
    configure({'network': {'listener': {'port': product.address}, 'allowed_origins': [product.endpoint],
                          'renderer_pool': {'base_port': pool, 'count': 2}}}, 'persist-listener-and-initial-renderer-pool')
    product.stop()
    product.start(configured=True, provider_binary=nonexistent_provider if task03_or_04 else None)
    if task03_or_04:
        assert not nonexistent_provider.exists()
    assert product.cli(['doctor'], client=owner)['data']['endpoint_checks']['ephemeral_override'] is False
    if not task03_or_04:
        occupied_port()
    task02_source = work / 'task02-source-root'
    task02_source.mkdir(exist_ok=True)
    note_bytes = (b'# Task02 source scope proof\nRetry and overview source verification line.\n'
                  if task03_or_04 else b'# Task02 source scope proof\n')
    (task02_source / 'note.md').write_bytes(note_bytes)
    note_sha256 = hashlib.sha256(note_bytes).hexdigest()
    fixture = []
    for index in range(1, 4):
        source = work / ('source-' + str(index))
        (source / 'nested').mkdir(parents=True)
        if task03_or_04:
            (source / 'empty-dir').mkdir()
            (source / 'assets').mkdir()
        (source / 'first.html').write_text('''<!doctype html><meta charset=utf-8><title>Private Reader</title>
<link rel=stylesheet href=style.css><h1 id=heading>VERSION first</h1><button id=increment onclick="this.textContent=String(Number(this.textContent)+1)">0</button>
<a id=next href="nested/second.html?scenario=2#part">next</a>
<a id=newtab target=_blank href="nested/second.html?popup=1#part">new tab</a>
<script type=module>import {value} from './module.js'; window.moduleValue=value;
new Worker('./worker.js').onmessage=e=>window.workerValue=e.data;
fetch('/root-asset.txt').then(r=>r.text()).then(t=>window.rootAsset=t);</script>'''.replace('VERSION', 'Version ' + str(index)))
        (source / 'nested/second.html').write_text('<h1 id=second>Second page</h1><h2 id=part>Exact fragment</h2><a href="../first.html">first</a>')
        if task03_or_04:
            (source / 'first.html').write_text((source / 'first.html').read_text() +
                '<section id=retry data-haeryong-region=retry>Retry region keyword alpha-search-token</section>' +
                '<section id=overview data-haeryong-region=overview>Overview region summary</section>')
            if args.task04_only:
                (source / 'first.html').write_text((source / 'first.html').read_text() +
                    '<div style="height:5000px">Task04 ordinary scrolling fixture</div>')
            (source / 'unrequested-sentinel.html').write_text('<!doctype html><title>Unrequested</title><p>unrequested page</p>')
            (source / 'assets/unrequested-sentinel.bin').write_bytes(b'unrequested-binary-asset')
            (source / 'note.md').write_bytes(note_bytes)
        (source / 'style.css').write_text('#heading{color:rgb(1,2,3)}')
        (source / 'module.js').write_text('export const value="module-running";')
        (source / 'worker.js').write_text('postMessage("worker-running")')
        (source / 'root-asset.txt').write_text('asset-' + str(index))
        _, _, published = product.import_tree(owner, source, extra=('--new-document',))
        fixture.append({'version': published['version_id'], 'document': published['document_id'],
                        'project': published.get('project_id'), 'label': 'Version ' + str(index)})
    if task03_or_04:
        v0_doc = local('GET', '/api/v1/documents/' + fixture[0]['document'], client=owner)
        v0_project = v0_doc['project_id']
        fixture[0]['project'] = v0_project
        source1 = work / 'source-1'
        meta = {
            'schema_version': '1.0',
            'content': {'default_page': 'first.html'},
            'pages': [
                {'page_path': 'first.html', 'label': 'First page'},
                {'page_path': 'nested/second.html', 'label': 'Second page'},
            ],
            'regions': [
                {'id': 'retry', 'page_path': 'first.html', 'label': 'Retry Section', 'evidence_ids': ['ev-1']},
                {'id': 'overview', 'page_path': 'first.html', 'label': 'Overview Section', 'evidence_ids': []},
            ],
            'sources': [
                {
                    'id': '00000000-0000-4000-8000-000000000731',
                    'kind': 'snapshot',
                    'retention_mode': 'selected_snapshot',
                    'release_label': 'v1.0-task03',
                    'snapshot_sha256': note_sha256,
                    'asset_path': 'note.md',
                    'bytes_sha256': note_sha256,
                },
            ],
            'evidence': [
                {
                    'id': 'ev-1',
                    'source_id': '00000000-0000-4000-8000-000000000731',
                    'capture_status': 'verified',
                    'path': 'note.md',
                    'line_start': 1,
                    'line_end': 2,
                    'bytes_sha256': note_sha256,
                },
            ],
            'references': [
                {
                    'id': 'ref-to-v2',
                    'document_id': fixture[1]['document'],
                    'version_id': fixture[1]['version'],
                    'page_path': 'nested/second.html',
                    'region_id': None,
                    'page_query': 'scenario=2',
                    'page_fragment': 'part',
                    'label': 'Version 2 Reference',
                    'from': {'page_path': 'first.html', 'region_id': 'retry'},
                    'relation': 'see_also',
                },
            ],
        }
        meta_file = work / 'task03-metadata.json'
        meta_file.write_text(json.dumps(meta, indent=2))
        (source1 / 'first.html').write_text((source1 / 'first.html').read_text().replace('Version 1 first', 'Version 1 v2 first'))
        _, _, pub_v0_v2 = product.import_tree(
            owner, source1,
            extra=('--document', fixture[0]['document'], '--base-version', fixture[0]['version'], '--metadata', str(meta_file)),
        )
        fixture[0]['prior_version'] = fixture[0]['version']
        fixture[0]['version'] = pub_v0_v2['version_id']
        fixture[0]['label'] = 'Version 1 v2'
        fixture[0]['assetIndex'] = 1
        drain_fixture_indexes()
        product.cli(['sources', 'approve', '--project', v0_project, '--source-id', '00000000-0000-4000-8000-000000000731',
                     '--kind', 'files', '--root-path', str(task02_source), '--confirmed'], client=owner)
        configure({'projects': {v0_project: {'provider_id': 'codex', 'source_mode_default': 'pinned'}}},
                  'task03-project-provider-and-source-default')
        assert not nonexistent_provider.exists()
        task03_zip = work / 'task03-archive.zip'
        with zipfile.ZipFile(task03_zip, 'w') as zf:
            zf.writestr(zipfile.ZipInfo('empty-dir/'), b'')
            zf.writestr('first.html', '<!doctype html><meta charset=utf-8><title>ZIP Import</title><h1 id=heading>ZIP imported</h1>')
            zf.writestr('style.css', 'body{margin:0}')
        task03_single = work / 'task03-single.html'
        task03_single.write_text('<!doctype html><meta charset=utf-8><title>Single HTML Import</title><h1 id=heading>Single HTML imported</h1>')
        task04_durable_zip = work / 'task04-durable.zip'
        with zipfile.ZipFile(task04_durable_zip, 'w') as zf:
            zf.writestr('first.html', '<!doctype html><meta charset=utf-8><title>Durable Job</title><h1 id=heading>Durable import completed</h1>')
            for idx in range(1, 250):
                zf.writestr(f'pages/p{idx:03d}.html', f'<!doctype html><title>Page {idx}</title><p id="r{idx}">Durable content {idx} ' + ('word ' * 80) + '</p>')
    _, discovered = open_render(fixture[0]['version'])
    upstream1 = discovered['render_origin']
    assert urllib.parse.urlsplit(upstream1).port == pool
    close_render(fixture[0]['version'], discovered)
    upstream2 = 'http://127.0.0.1:' + str(pool + 1)
    browser_attempted = True
    if task03_or_04:
        preferences = work / 'browser/Default/Preferences'
        preferences.parent.mkdir(parents=True, exist_ok=True)
        origin = args.external_base_url or product.endpoint
        preferences.write_text(json.dumps({'profile': {'content_settings': {'exceptions': {
            'file_system_write_guard': {origin.rstrip('/') + ',*': {'setting': 1}}
        }}}}))
    call(['open', product.endpoint + '/health/live', '--browser', 'chrome', '--profile', str(work / 'browser')])
    proof['browser_version'] = js('async page => page.context().browser().version()')
    if not task03_or_04:
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
    if not task03_or_04:
        proof['cases'].append({'kind': 'actual local Reader before private configuration',
                               'actual': js(script.replace('INPUT', json.dumps(inputs)))})

    def run_task03_cli_fallback(target_base, version_id):
        # Bootstrap and the browser have used the ordinary two-per-minute
        # creation allowance for this proxy client. Respect the live policy.
        pairing_wait = max(0, 61 - (time.monotonic() - owner_created_at))
        if pairing_wait:
            print('WAIT ordinary pairing creation window: %.1f seconds' % pairing_wait, flush=True)
            time.sleep(pairing_wait)
        cli_caller = work / ('task03-cli-caller-' + str(len(proof['cases'])))
        cli_caller.mkdir(mode=0o700, exist_ok=True)
        cli_cred = cli_caller / 'credential.json'
        cli_client = {'flags': ['--endpoint', target_base, '--profile', product.profile,
                                '--client-dir', str(cli_caller), '--credential', str(cli_cred), '--json']}
        begun = product.cli(['auth', 'connect', 'new'], client=cli_client)['data']
        local('POST', '/api/v1/auth/pairings/' + begun['pairing_id'] + '/approve', client=owner,
              body={'code': begun['code'], 'origin': begun['origin']},
              headers={'If-Match': begun['etag'], 'Idempotency-Key': h.operation()}, expected=(200,))
        connected = product.cli(['auth', 'connect'], client=cli_client)['data']
        assert connected['connected'] is True and connected['session_id'] and connected['endpoint'] == target_base and cli_cred.is_file()
        out_dir = work / ('task03-cli-download-' + str(len(proof['cases'])))
        product.cli(['files', 'download', '--version', version_id, '--output', str(out_dir)], client=cli_client)
        single_out = work / ('task03-cli-single-' + str(len(proof['cases'])) + '.html')
        product.cli(['files', 'get', '--version', version_id, '--path', 'first.html',
                     '--output', str(single_out)], client=cli_client)
        inv = product.files(owner, version_id)
        for item in inv:
            target_path = out_dir / item['path']
            if item['kind'] == 'directory':
                assert target_path.is_dir(), 'missing downloaded directory ' + item['path']
            elif item['kind'] == 'file':
                assert target_path.is_file(), 'missing downloaded file ' + item['path']
                assert hashlib.sha256(target_path.read_bytes()).hexdigest() == item['bytes_sha256']
        assert (out_dir / 'empty-dir').is_dir(), 'empty-dir not preserved by CLI files download'
        assert hashlib.sha256(single_out.read_bytes()).hexdigest() == hashlib.sha256((work / 'source-1/first.html').read_bytes()).hexdigest()
        return {
            'pairedOrigin': begun['origin'],
            'sessionId': connected['session_id'],
            'downloadedEntries': len(inv),
            'emptyDirPreserved': True,
            'singleFileGetVerified': single_out.is_file() and single_out.stat().st_size > 0,
            'ordinaryPairingWindowWaitSeconds': pairing_wait,
        }

    def run_task03_cohort(target_base, target_renderer1, target_renderer2, mode_label):
        assert not nonexistent_provider.exists()
        cohort_inputs = inputs | {
            'base': target_base,
            'expectedOrigins': [target_renderer1, target_renderer2],
            'phase': 'task03-cohort',
            'modeLabel': mode_label,
            'archivePath': str(task03_zip),
            'singleHtmlPath': str(task03_single),
            'sourceDirPath': str(work / 'source-2'),
            'noteSha256': note_sha256,
            'noteText': note_bytes.decode(),
            'pageSha256': hashlib.sha256((work / 'source-1/first.html').read_bytes()).hexdigest(),
            'destination': str(work / 'task03-browser-destination'),
            'screenshot': str(args.screenshot.resolve()) if args.screenshot else None,
            'selectedStages': task03_stages,
        }
        Path(cohort_inputs['destination']).mkdir()
        empty_directory = work / 'task03-empty-picker-directory'
        empty_directory.mkdir()
        cohort_inputs['emptyDirectoryPath'] = str(empty_directory)
        if live:
            call(['goto', target_base + '/connect'])
            pairing = js('''async page => {
                await page.getByLabel('장치 이름',{exact:true}).fill('Task03 private browser');
                const response=page.waitForResponse(r=>r.url().endsWith('/api/v1/auth/pairings')&&r.request().method()==='POST');
                await page.getByRole('button',{name:'연결 코드 요청',exact:true}).click();
                const p=(await (await response).json()).data;
                return {pairing_id:p.pairing_id,code:p.code,origin:p.origin,etag:p.etag};
            }''')
            local('POST', '/api/v1/auth/pairings/' + pairing['pairing_id'] + '/approve', client=owner,
                  body={'code': pairing['code'], 'origin': pairing['origin']},
                  headers={'If-Match': pairing['etag'], 'Idempotency-Key': h.operation()}, expected=(200,))
            js('async page => {await page.waitForFunction(()=>localStorage.getItem("haeryong.owner-session.v1"));return true;}')
        else:
            js('async page => {await page.goto(' + json.dumps(target_base) + ');await page.evaluate(t=>localStorage.setItem("haeryong.owner-session.v1",t),' + json.dumps(owner['token']) + ');return true;}')
        cohort_result = js(script.replace('INPUT', json.dumps(cohort_inputs)), timeout=420)
        if not cohort_result.get('complete'):
            proof['cases'].append({'kind': 'Task03 partial controlled browser execution; not a passed cohort', 'actual': cohort_result})
            raise AssertionError('Task03 browser stage failed: ' + str(cohort_result.get('error')))
        proof['cases'].append({'kind': 'TASK-007-03 controlled browser workflow (' + mode_label + ')',
                               'actual': cohort_result})
        if 'browserArchive' in cohort_result:
            verify_task03_archive(Path(cohort_inputs['destination']) / cohort_result['browserArchive'])
            cohort_result['browserArchiveInventoryVerified'] = True
            cohort_result['cliFallbackDownload'] = run_task03_cli_fallback(target_base, fixture[0]['version'])
        if 'browserFile' in cohort_result:
            downloaded = Path(cohort_inputs['destination']) / cohort_result['browserFile']
            assert downloaded.read_bytes() == (work / 'source-1/note.md').read_bytes()
            cohort_result['browserFileBytesVerified'] = True
        if 'resumeImportedVersion' in cohort_result:
            entries = product.files(owner, cohort_result['resumeImportedVersion'])
            actual = {i['path']: i['bytes_sha256'] for i in entries if i['kind'] == 'file'}
            expected = {str(p.relative_to(work / 'source-2')): hashlib.sha256(p.read_bytes()).hexdigest()
                        for p in (work / 'source-2').rglob('*') if p.is_file()}
            assert actual == expected
            assert not any(i['path'].rstrip('/') == 'empty-dir' for i in entries)
            cohort_result['resumeRepresentedFilesVerified'] = True
        assert not nonexistent_provider.exists()

    def verify_task03_archive(archive):
        with zipfile.ZipFile(archive) as zf:
            inventory = product.files(owner, fixture[0]['version'])
            actual = {i.filename.rstrip('/'): i for i in zf.infolist()}
            assert set(actual) == {i['path'].rstrip('/') for i in inventory}
            for entry in inventory:
                item = actual[entry['path'].rstrip('/')]
                if entry['kind'] == 'directory':
                    assert item.is_dir()
                else:
                    assert hashlib.sha256(zf.read(item)).hexdigest() == entry['bytes_sha256']

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

    def run_task04_cohort(target_base, target_renderer1, target_renderer2, mode_label):
        assert not nonexistent_provider.exists()
        if live:
            call(['goto', target_base + '/connect'])
            pairing = js('''async page => {
                await page.getByLabel('장치 이름',{exact:true}).fill('Task04 private browser');
                const response=page.waitForResponse(r=>r.url().endsWith('/api/v1/auth/pairings')&&r.request().method()==='POST');
                await page.getByRole('button',{name:'연결 코드 요청',exact:true}).click();
                const p=(await (await response).json()).data;
                return {pairing_id:p.pairing_id,code:p.code,origin:p.origin,etag:p.etag};
            }''')
            local('POST', '/api/v1/auth/pairings/' + pairing['pairing_id'] + '/approve', client=owner,
                  body={'code': pairing['code'], 'origin': pairing['origin']},
                  headers={'If-Match': pairing['etag'], 'Idempotency-Key': h.operation()}, expected=(200,))
            js('async page => {await page.waitForFunction(()=>localStorage.getItem("haeryong.owner-session.v1"));return true;}')
            peer_token = owner['token']
        else:
            peer_pair = local('POST', '/api/v1/auth/pairings', body={'device_label': 'Task04 peer context (' + mode_label + ')'}, expected=(201,))
            local('POST', '/api/v1/auth/pairings/' + peer_pair['pairing_id'] + '/approve', client=owner,
                  body={'code': peer_pair['code'], 'origin': peer_pair['origin']},
                  headers={'If-Match': peer_pair['etag'], 'Idempotency-Key': h.operation()}, expected=(200,))
            peer_polled = local('POST', '/api/v1/auth/pairings/' + peer_pair['pairing_id'] + '/poll',
                                body={'polling_secret': peer_pair['polling_secret']}, expected=(200,))
            peer_token = peer_polled['session_token']
            secrets.add(peer_token)
            js('async page => {await page.goto(' + json.dumps(target_base) + ');await page.evaluate(t=>localStorage.setItem("haeryong.owner-session.v1",t),' + json.dumps(owner['token']) + ');return true;}')

        cohort_inputs = inputs | {
            'base': target_base,
            'expectedOrigins': [target_renderer1, target_renderer2],
            'phase': 'task04-cohort',
            'modeLabel': mode_label,
            'peerToken': peer_token,
            'archivePath': str(task03_zip),
            'durableArchivePath': str(task04_durable_zip),
            'singleHtmlPath': str(task03_single),
            'sourceDirPath': str(work / 'source-2'),
            'noteSha256': note_sha256,
            'noteText': note_bytes.decode(),
            'pageSha256': hashlib.sha256((work / 'source-1/first.html').read_bytes()).hexdigest(),
            'screenshot': str(args.screenshot.resolve()) if args.screenshot else None,
            'selectedStages': task04_stages,
        }
        # No retention engine is claimed: a single owned fixture history row is
        # trimmed after a real peer mutation. The browser must consume the real
        # same-epoch trimmed_cursor control on its own resumed subscription.
        trim_stop = threading.Event()
        trim_result = {}
        trim_thread = None
        if not task04_stages or 'resync-and-passive-idle' in task04_stages:
            head = local('GET', '/api/v1/events?transport=poll', client=owner,
                         headers={'Haeryong-Passive-Read': '1'})
            trim_epoch, trim_sequence = head['stream_epoch'], int(head['head'])
            assert trim_sequence > 0
            database = (product.data_dir / 'storage/library.sqlite').resolve(strict=True)
            assert database.is_relative_to(work) and not database.is_symlink()
            library = owner['session']['library_id']
            project_name = 'Task04 resync ' + session
            cohort_inputs.update(resumeCursor=f'{trim_epoch}:{trim_sequence}',
                                 resyncProjectName=project_name)

            def trim_owned_history():
                try:
                    while not trim_stop.is_set():
                        projects = local('GET', '/api/v1/projects', client=owner,
                                         headers={'Haeryong-Passive-Read': '1'})
                        if any(p['name'] == project_name for p in projects['items']):
                            with sqlite3.connect(database.as_uri() + '?mode=rw', uri=True, timeout=5) as db:
                                db.execute('BEGIN IMMEDIATE')
                                actual = db.execute('SELECT library_id,stream_epoch FROM libraries WHERE singleton=1').fetchone()
                                assert actual == (library, trim_epoch), 'owned history identity changed'
                                later = db.execute('SELECT count(*) FROM events WHERE stream_epoch=? AND sequence>?',
                                                   (trim_epoch, trim_sequence)).fetchone()[0]
                                assert later > 0, 'newer committed head required before trimming old cursor'
                                deleted = db.execute('DELETE FROM events WHERE stream_epoch=? AND sequence=?',
                                                     (trim_epoch, trim_sequence)).rowcount
                                assert deleted == 1, 'exact retained cursor row required'
                            trim_result.update(complete=True, rows_trimmed=deleted, same_epoch=True,
                                               scope='one test-owned history row; controlled fault, not automatic retention')
                            return
                        trim_stop.wait(0.1)
                except Exception as error:
                    trim_result.update(complete=False, error=type(error).__name__ + ': ' + str(error))

            trim_thread = threading.Thread(target=trim_owned_history, name='owned-task04-history-fault')
            trim_thread.start()
        try:
            cohort_result = js(script.replace('INPUT', json.dumps(cohort_inputs)), timeout=420)
        finally:
            trim_stop.set()
            if trim_thread:
                trim_thread.join(timeout=30)
                assert not trim_thread.is_alive(), 'owned history observer did not settle'
        if trim_thread:
            cohort_result['controlledHistoryFault'] = trim_result
            if not trim_result.get('complete'):
                cohort_result['complete'] = False
                cohort_result['error'] = str(cohort_result.get('error', '')) + '; owned history fault: ' + str(trim_result)
        if not cohort_result.get('complete'):
            proof['cases'].append({'kind': 'Task04 partial browser execution; not a passed cohort', 'actual': cohort_result})
            raise AssertionError('Task04 browser stage failed: ' + str(cohort_result.get('error')))
        proof['cases'].append({'kind': 'TASK-007-04 multi-device reconciliation, conflict UI, durable job, resync and passive-idle (' + mode_label + ')',
                               'actual': cohort_result})
        assert not nonexistent_provider.exists()

    if not live:
        if args.task03_only:
            run_task03_cohort(product.endpoint, upstream1, upstream2, 'local-deterministic')
        elif args.task04_only:
            run_task04_cohort(product.endpoint, upstream1, upstream2, 'local-deterministic')
        else:
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
    if task03_or_04:
        assert not nonexistent_provider.exists()
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
        if args.task03_only:
            run_task03_cohort(external, renderer1, renderer2, 'private-https')
        elif args.task04_only:
            run_task04_cohort(external, renderer1, renderer2, 'private-https')
        else:
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
    if not task03_or_04:
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
