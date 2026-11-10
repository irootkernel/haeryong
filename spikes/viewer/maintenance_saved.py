"""Opt-in actual Maintenance saved-record observer. No build/init/provider path.

Requires a separately released controller manifest, already-running owned copy,
frozen source/prepared outputs, and a task-owned credential file. This module is
not imported or executed by ordinary Reader/evidence stages.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import traceback
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import urllib.error
import urllib.parse
import urllib.request


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def write(path, value):
    Path(path).write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n')
    Path(path).chmod(0o600)


def absent(pid):
    try:
        os.kill(pid, 0)
        return False
    except ProcessLookupError:
        return True


def inventory(spec):
    paths = {Path(path) for path in spec['files']}
    for directory in spec['recursive_roots']:
        paths.update(path for path in Path(directory).rglob('*') if path.is_file())
    for directory in spec['shallow_roots']:
        paths.update(path for path in Path(directory).iterdir() if path.is_file())
    assert all(not path.is_symlink() for path in paths)
    return {str(path) for path in paths}


def wait(predicate, seconds):
    until = time.monotonic() + seconds
    while time.monotonic() < until:
        value = predicate()
        if value is not None:
            return value
        time.sleep(0.1)
    raise AssertionError('bounded observation deadline')


def run(manifest_path, receipt_path, scratch_root, browser_cli=None):
    manifest_path, receipt_path = Path(manifest_path).resolve(strict=True), Path(receipt_path).resolve()
    scratch_root = Path(scratch_root).resolve(strict=True)
    os.umask(0o077)
    work = Path(tempfile.mkdtemp(prefix='u4p-', dir=scratch_root)).resolve()
    state = work / 'state'
    state.mkdir(mode=0o700)
    proof = {'schema': 'haeryong.task04-new-panel-observation/v1', 'selected_complete': False,
             'complete': False, 'full_campaign_complete': False, 'runtime': str(work),
             'classification': 'UI contributor, not independent evaluator', 'cleanup_errors': [],
             'events': [], 'faults': [], 'proxy_errors': [], 'credential_cleanup': []}
    write(receipt_path, proof)
    process = server = native_log = None
    streams = set()
    lock, release, held, delivered, stopping = threading.Lock(), threading.Event(), threading.Event(), threading.Event(), threading.Event()
    browser_open = False
    browser_env = os.environ | {'PYTHONDONTWRITEBYTECODE': '1', 'TMPDIR': str(work),
                                'PLAYWRIGHT_DAEMON_SOCKETS_DIR': str(work / 's'),
                                'PLAYWRIGHT_DAEMON_SESSION_DIR': str(work / 'registry')}
    (work / 's').mkdir(mode=0o700)
    control = {'held_job': None, 'disconnected': False, 'denied': False, 'failed_cursor': None, 'cursor_attempts': 0}
    browser = None
    try:
        cfg = json.loads(manifest_path.read_text())
        grant_path = Path(cfg['runtime_grant']).resolve(strict=True)
        assert digest(grant_path) == cfg['runtime_grant_sha256']
        grant = json.loads(grant_path.read_text())
        assert grant['decision'] == 'released' and cfg['component'] in grant['components']
        assert grant['prepared_binding_sha256'] == cfg['prepared_binding_sha256']
        if cfg['settings_mutations']:
            assert cfg['component'] in grant['settings_restriction_components'] and cfg['controlled_v2'] is True
        assert cfg['frozen_files'] and cfg['driver_hashes']
        proof['manifest_sha256'] = digest(manifest_path)
        proof['actual_bindings_before'] = {path: digest(path) for path in cfg['frozen_files']}
        write(receipt_path, proof)
        assert proof['actual_bindings_before'] == cfg['frozen_files'], 'actual source/prepared closure mismatch'
        assert all(digest(path) == sha for path, sha in cfg['driver_hashes'].items()), 'exact adopted driver required'
        assert inventory(cfg['source_inventory']) == set(cfg['source_inventory']['expected'])
        assert inventory(cfg['prepared_inventory']) == set(cfg['prepared_inventory']['expected'])
        endpoint = cfg['endpoint'].rstrip('/')
        parsed = urllib.parse.urlsplit(endpoint)
        assert parsed.scheme == 'http' and parsed.hostname == '127.0.0.1' and parsed.port and not parsed.path and not parsed.query and not parsed.fragment and parsed.username is None
        credential = Path(cfg['owner_token_file']).resolve(strict=True)
        assert credential.is_relative_to(scratch_root) and credential.stat().st_mode & 0o777 == 0o600
        token = credential.read_text().strip()
        assert token and '\n' not in token and '\r' not in token
        jobs = {job['job_id'] for job in cfg['probe']['jobs']}
        assert jobs or cfg['probe'].get('settings_read') is True

        def get(path, envelope=True):
            request = urllib.request.Request(endpoint + path, headers={'Authorization': 'Bearer ' + token, 'Origin': endpoint})
            with urllib.request.urlopen(request, timeout=30) as response:
                value = json.load(response)
                return value['data'] if envelope else value

        def rows(path):
            result, seen, cursor = [], set(), None
            while True:
                u = urllib.parse.urlsplit(path)
                q = dict(urllib.parse.parse_qsl(u.query))
                if cursor is not None:
                    q['cursor'] = cursor
                page = get(u.path + ('?' + urllib.parse.urlencode(q) if q else ''))
                result.extend(page['items'])
                cursor = page['next_cursor']
                if cursor is None:
                    return result
                assert cursor not in seen, 'genuine cursor cycle'
                seen.add(cursor)

        # Expected records are supplied from approved retained metadata by the
        # controller. Do not silently replace an original oracle with live state.
        for job in cfg['probe']['jobs']:
            current = rows('/api/v1/jobs/' + job['job_id'] + '/findings?limit=30')
            for expected in job['findings']:
                actual = next(row for row in current if row['finding_id'] == expected['finding_id'])
                assert all(actual.get(key) == value for key, value in expected.items()), 'retained finding projection changed'
        def snapshot(spec):
            path = spec['path']
            assert path.startswith('/api/v1/') and not urllib.parse.urlsplit(path).netloc
            try:
                value = get(path, spec['envelope'])
            except urllib.error.HTTPError as error:
                assert spec['kind'] == 'authored_metadata' and error.code == 404
                return {'status': 404, 'absent': True}
            projected = {}
            for pointer in spec['json_pointers']:
                node, present = value, True
                for key in pointer.lstrip('/').split('/') if pointer else []:
                    key = key.replace('~1', '/').replace('~0', '~')
                    if not isinstance(node, dict) or key not in node:
                        present = False; break
                    node = node[key]
                projected[pointer] = {'present': present, **({'value': node} if present else {})}
            return {'status': 200, 'fields': projected}
        assert {spec['kind'] for spec in cfg['invariant_reads']} >= {'default', 'library', 'review', 'authored_metadata', 'authored_references'}
        snapshots = [snapshot(spec) for spec in cfg['invariant_reads']]
        proof['invariant_projection_sha256_before'] = hashlib.sha256(json.dumps(snapshots, sort_keys=True).encode()).hexdigest()
        configuration_before = get('/api/v1/configuration')
        allowed_plans = set()
        auto = configuration_before['configuration'].get('maintenance', {}).get('auto_apply', {})
        initial_content = configuration_before['configuration'].get('maintenance', {}).get('content_generation')
        settings_committed = False

        class Proxy(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass

            def send(self, status, body, headers=()):
                self.send_response(status)
                for key, value in headers:
                    if key.lower() not in ('content-length', 'transfer-encoding', 'connection', 'access-control-allow-origin'):
                        self.send_header(key, value)
                origin_header = self.headers.get('Origin')
                if origin_header in (cfg['native_reader_origin'], origin):
                    self.send_header('Access-Control-Allow-Origin', origin_header)
                    self.send_header('Access-Control-Allow-Headers', 'Content-Type')
                    self.send_header('Access-Control-Allow-Methods', 'POST, OPTIONS')
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                if self.command != 'HEAD':
                    self.wfile.write(body)
                    self.wfile.flush()

            def forward(self):
                nonlocal settings_committed
                u = urllib.parse.urlsplit(self.path)
                if u.path == '/__maintenance_probe':
                    if self.command == 'OPTIONS':
                        self.send(204, b'')
                        return
                    assert self.command == 'POST'
                    value = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                    action = value['action']
                    with lock:
                        if action == 'hold':
                            assert value['job_id'] in jobs
                            control['held_job'] = value['job_id']
                            release.clear(); held.clear(); delivered.clear()
                        elif action == 'release':
                            release.set()
                        elif action in ('disconnected', 'denied'):
                            control[action] = value['enabled'] is True
                        elif action == 'settings-conflict-once':
                            assert cfg['settings_mutations'] and auto.get('related_explanation') == 'auto'
                            control['settings_conflict'] = True
                        else:
                            assert action == 'state'
                    self.send(200, json.dumps({'held': held.is_set(), 'released': delivered.is_set(), 'settings_committed': settings_committed}).encode(), [('Content-Type', 'application/json')])
                    return
                render = re.fullmatch(r'/api/v1/versions/([^/]+)/render-sessions(?:/([^/]+)/(close|renew))?', u.path)
                position = re.fullmatch(r'/api/v1/versions/([^/]+)/(reading-position|page-position)', u.path)
                policy_commit = re.fullmatch(r'/api/v1/configuration/plans/([^/]+)/commit', u.path)
                policy = cfg['settings_mutations'] and self.command == 'POST' and (u.path == '/api/v1/configuration/plans' or policy_commit)
                allowed = self.command in ('GET', 'HEAD') or self.command == 'POST' and render is not None or self.command == 'PUT' and position is not None or policy
                if not allowed:
                    proof['proxy_errors'].append('forbidden-mutation:' + u.path)
                    self.send(403, b'')
                    return
                match = re.fullmatch(r'/api/v1/jobs/([^/]+)/findings', u.path)
                selected = match is not None and match[1] in jobs
                with lock:
                    deny, disconnected = control['denied'], control['disconnected']
                if (selected or u.path in ('/api/v1/session', '/api/v1/capabilities')) and (deny or disconnected):
                    self.send(401 if deny else 503, b'{"error":{"code":"CONTROLLED_UI_TRANSPORT_FAULT","retryable":false}}', [('Content-Type', 'application/json')])
                    return
                body = self.rfile.read(int(self.headers.get('Content-Length') or 0)) or None
                if policy:
                    submitted = json.loads(body)
                    assert auto.get('related_explanation') == 'auto', 'no widening or substitute starting mode'
                    if policy_commit:
                        assert policy_commit[1] in allowed_plans and submitted == {'expected_config_revision': configuration_before['config_revision'], 'confirmed': True}
                    else:
                        expected_auto = {**auto, 'related_explanation': 'propose'}
                        assert submitted == {'scope': {'kind': 'library'}, 'expected_config_revision': configuration_before['config_revision'], 'changes': {'maintenance': {'auto_apply': expected_auto, 'content_generation': initial_content}}}, 'only the exact existing related_explanation restriction is allowed'
                        if control.pop('settings_conflict', False):
                            proof['faults'].append({'kind': 'controlled-preview-409-before-upstream'})
                            self.send(409, b'{"error":{"code":"CONTROLLED_CONFIG_CONFLICT","retryable":false}}', [('Content-Type', 'application/json')])
                            return
                headers = {key: value for key, value in self.headers.items() if key.lower() not in ('host', 'origin', 'authorization', 'content-length', 'connection', 'accept-encoding')}
                headers.update(Origin=endpoint, Authorization='Bearer ' + token, Connection='close')
                if u.path == '/health/live':
                    headers.pop('Origin', None)
                request = urllib.request.Request(endpoint + self.path, data=body, headers=headers, method=self.command)
                try:
                    response = urllib.request.urlopen(request, timeout=30)
                except urllib.error.HTTPError as error:
                    response = error
                if u.path == '/api/v1/events' and response.status == 200:
                    with lock:
                        streams.add(response)
                    try:
                        self.send_response(200); self.send_header('Content-Type', 'text/event-stream'); self.end_headers()
                        while not stopping.is_set():
                            chunk = response.read(1)
                            if not chunk:
                                break
                            self.wfile.write(chunk); self.wfile.flush()
                    finally:
                        response.close()
                        with lock:
                            streams.discard(response)
                    return
                with response:
                    data, status, fields = response.read(), response.status, response.getheaders()
                if policy and status in (200, 201):
                    if policy_commit:
                        settings_committed = True
                    else:
                        allowed_plans.add(json.loads(data)['data']['plan_id'])
                if render and self.command == 'POST':
                    event = {'version_id': render[1], 'session_id': render[2], 'operation': render[3] or 'open', 'status': status, 'path': u.path}
                    if event['operation'] == 'open' and status in (200, 201):
                        actual = json.loads(data)['data']
                        assert actual['version_id'] == render[1]
                        event['session_id'] = actual['session_id']
                    with lock:
                        proof['events'].append(event)
                if selected and status == 200:
                    q = dict(urllib.parse.parse_qsl(u.query))
                    should_hold = control['held_job'] == match[1] and not q.get('cursor') and not held.is_set()
                    if should_hold:
                        held.set()
                        assert release.wait(45), 'old job release was never observed'
                        try:
                            self.send(status, data, fields)
                        finally:
                            delivered.set()
                        return
                    if cfg['probe'].get('cursor_fault') and q.get('cursor'):
                        with lock:
                            if control['failed_cursor'] is None:
                                control['failed_cursor'] = self.path
                            fault = control['failed_cursor'] == self.path
                            if fault:
                                control['cursor_attempts'] += 1
                            first = fault and control['cursor_attempts'] == 1
                        if first:
                            proof['faults'].append({'kind': 'one-real-later-cursor-500', 'path': self.path, 'upstream_body_sha256': hashlib.sha256(data).hexdigest()})
                            self.send(500, b'{"error":{"code":"CONTROLLED_UI_CURSOR_FAULT","retryable":true}}', [('Content-Type', 'application/json')])
                            return
                self.send(status, data, fields)

            def handle_request(self):
                try:
                    self.forward()
                except (BrokenPipeError, ConnectionResetError):
                    pass
                except BaseException as error:
                    if not stopping.is_set():
                        proof['proxy_errors'].append({'type': type(error).__name__, 'path': urllib.parse.urlsplit(self.path).path})
                    try:
                        self.send(502, b'')
                    except (BrokenPipeError, ConnectionResetError):
                        pass

            do_GET = do_HEAD = do_POST = do_PUT = do_PATCH = do_DELETE = do_OPTIONS = handle_request

        class Server(ThreadingHTTPServer):
            request_queue_size = socket.SOMAXCONN
            daemon_threads = True
        server = Server(('127.0.0.1', 0), Proxy)
        origin = 'http://127.0.0.1:' + str(server.server_port)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        config = {**cfg['probe'], 'control_origin': origin}
        script_path = Path(cfg['probe_source'])
        script = script_path.read_text()
        assert script.count('/*__CONFIG__*/null') == 1
        script = script.replace('/*__CONFIG__*/null', json.dumps(config, ensure_ascii=False), 1)
        injected = work / 'maintenance-probe.js'
        injected.write_text(script)
        if browser_cli:
            def browser(command, result=False):
                reply = subprocess.run([str(browser_cli), '--session=' + work.name, *command], cwd=work, env=browser_env, text=True, capture_output=True, timeout=300)
                if reply.returncode != 0 or '### Error' in reply.stdout:
                    # Retain diagnostic facts without command bodies, raw browser
                    # output, credential values, or an automatic retry.
                    combined = (reply.stdout + '\n' + reply.stderr).lower()
                    proof['browser_command_failure'] = {
                        'verb': command[0], 'returncode': reply.returncode,
                        'stdout_bytes': len(reply.stdout.encode()),
                        'stderr_bytes': len(reply.stderr.encode()),
                        'stdout_sha256': hashlib.sha256(reply.stdout.encode()).hexdigest(),
                        'stderr_sha256': hashlib.sha256(reply.stderr.encode()).hexdigest(),
                        'markers': {name: any(marker in combined for marker in markers)
                                    for name, markers in {
                                        'offline_cache_miss': ('enotcached', 'eoffline', 'cache mode is only-if-cached'),
                                        'permission_denied': ('eperm', 'eacces', 'operation not permitted', 'permission denied'),
                                        'socket_path_too_long': ('enametoolong', 'socket path too long'),
                                        'browser_executable_missing': ('executable doesn', 'browser is not installed'),
                                        'daemon_start_failed': ('daemon failed to start', 'failed to start daemon'),
                                        'CLI_error_section': ('### error',),
                                    }.items()}}
                    write(receipt_path, proof)
                assert reply.returncode == 0 and '### Error' not in reply.stdout, 'browser command failed; retain diagnostic class without body/secret print'
                if result:
                    assert '### Result' in reply.stdout
                    return json.JSONDecoder().raw_decode(reply.stdout.split('### Result', 1)[1].lstrip())[0]
            browser(['open', origin + '/health/live', '--browser', 'chrome', '--profile', str(work / 'browser-profile')])
            browser_open = True
            browser(['run-code', "async page=>{await page.evaluate(()=>localStorage.setItem('haeryong.owner-session.v1','saved-maintenance-probe'));return true;}"])
            browser(['run-code', 'async page=>{await page.setViewportSize({width:1280,height:900});await page.goto(' + json.dumps(origin + '/maintenance') + ');await page.waitForSelector(".service-state[data-state=verified]");await page.getByRole("button",{name:"목록/작업 새로고침",exact:true}).focus();await page.keyboard.press("Tab");const desktop=await page.evaluate(()=>({tag:document.activeElement.tagName,focus:document.activeElement.matches(":focus-visible")}));await page.setViewportSize({width:390,height:844});const cdp=await page.context().newCDPSession(page);await cdp.send("Emulation.setTouchEmulationEnabled",{enabled:true,maxTouchPoints:1});const b=await page.getByRole("button",{name:"목록/작업 새로고침",exact:true}).boundingBox();await cdp.send("Input.dispatchTouchEvent",{type:"touchStart",touchPoints:[{x:b.x+b.width/2,y:b.y+b.height/2}]});await cdp.send("Input.dispatchTouchEvent",{type:"touchEnd",touchPoints:[]});await page.getByRole("button",{name:"목록/작업 새로고침",exact:true}).focus();await page.keyboard.press("Tab");const compact=await page.evaluate(()=>({tag:document.activeElement.tagName,focus:document.activeElement.matches(":focus-visible")}));if(!desktop.focus||!compact.focus)throw Error("actual focus missing");return {desktop,compact};}',], True)
            proof['browser_keyboard'] = 'actual Playwright focus and Tab; native probe focus is programmatic, no physical-keyboard claim'
            proof['cases'] = browser(['run-code', 'async page=>await page.evaluate(' + json.dumps(script) + ')'], True)
        else:
            native = Path(cfg['native_binary']).resolve(strict=True)
            dist = Path(cfg['native_dist']).resolve(strict=True)
            (state / 'native-session').write_text(token + '\n'); (state / 'native-session').chmod(0o600)
            native_log = (work / 'native.log').open('wb')
            process = subprocess.Popen([str(native), '--reader', '--reader-port', str(cfg['native_reader_port']), '--base', origin,
                                        '--state', str(state), '--reader-dist', str(dist), '--reader-path', '/maintenance',
                                        '--reader-probe', str(injected), '--reader-close-on-done', '--reader-width', '390', '--reader-height', '844', '--close-after', '300s'], stdout=native_log, stderr=native_log)
            proof['native_pid'] = process.pid
            report_path = state / ('native-reader-selfcheck-' + str(process.pid) + '.json')
            def final_report():
                if report_path.exists():
                    value = json.loads(report_path.read_text())
                    if any(row['name'] == 'page.done' and row['status'] == 'pass' for row in value['rows']):
                        return value['rows']
                return None
            proof['cases'] = wait(final_report, 280)
            proof['native_exit'] = process.wait(timeout=30)
            assert proof['native_exit'] == 0
            observed = json.loads((state / ('native-reader-receipts-' + str(process.pid) + '.json')).read_text())
            window = next((row['rows'] for row in observed if row['event'] == 'windowClose'), None)
            explicit = {(row['version_id'], row['session_id']) for row in observed if row['event'] == 'renderSessionClose'}
            opens = {(row['version_id'], row['session_id']) for row in observed if row['event'] == 'renderSessionOpen'}
            http = json.loads((state / ('native-network-observations-' + str(process.pid) + '.json')).read_text())
            closed = set()
            for version, session in opens:
                close_path = '/api/v1/versions/' + version + '/render-sessions/' + session + '/close'
                network204 = any(row.get('method') == 'POST' and row.get('path') == close_path and row.get('status') == 204 for row in http)
                receipt204 = (version, session) in explicit or any(row.get('version_id') == version and row.get('session_id') == session and row.get('status') == 204 for row in (window or []))
                if network204 and receipt204:
                    closed.add((version, session))
            proof['native_close_facts'] = {'opened': sorted(opens), 'closed': sorted(closed), 'windowClose': window}
            assert opens and window is not None and window and all(row.get('status') == 204 for row in window) and opens == closed
        assert proof['cases'] and all(row['status'] == 'pass' or row['name'] == 'settings.restriction.current-not-auto' and row['status'] == 'not_run' for row in proof['cases'])
        proof['not_run'] = [row for row in proof['cases'] if row['status'] == 'not_run']
        if browser_cli:
            def closed_sessions():
                with lock:
                    events = list(proof['events'])
                opens = {(row['version_id'], row['session_id']) for row in events if row['operation'] == 'open' and row['status'] in (200, 201)}
                closes = {(row['version_id'], row['session_id']) for row in events if row['operation'] == 'close' and row['status'] == 204}
                return {'opened': sorted(opens), 'closed': sorted(closes)} if opens == closes and (opens or cfg['probe'].get('reader_required') is False) else None
            proof['browser_close_facts'] = wait(closed_sessions, 10)
        if cfg['probe'].get('cursor_fault'):
            assert control['cursor_attempts'] >= 2, 'real same-cursor explicit retry required'
        assert [snapshot(spec) for spec in cfg['invariant_reads']] == snapshots, 'default/Library/Review/metadata/reference invariant changed'
        configuration_after = get('/api/v1/configuration')
        proof['configuration_revision_before'] = configuration_before['config_revision']
        proof['configuration_revision_after'] = configuration_after['config_revision']
        proof['settings_committed'] = settings_committed
        if settings_committed:
            expected_configuration = json.loads(json.dumps(configuration_before['configuration']))
            expected_configuration['maintenance']['auto_apply']['related_explanation'] = 'propose'
            assert configuration_after['configuration'] == expected_configuration
            assert configuration_after['config_revision'] > configuration_before['config_revision']
        else:
            assert configuration_after == configuration_before
        assert all(digest(path) == sha for path, sha in cfg['frozen_files'].items()), 'source/prepared bytes changed'
        assert inventory(cfg['source_inventory']) == set(cfg['source_inventory']['expected']), 'complete source file set changed'
        assert inventory(cfg['prepared_inventory']) == set(cfg['prepared_inventory']['expected']), 'complete prepared file set changed'
        assert not proof['proxy_errors']
        proof['selected_complete'] = True
    except BaseException as error:
        proof['failure_type'] = type(error).__name__
        frames = traceback.extract_tb(error.__traceback__)
        if frames:
            frame = frames[-1]
            proof['failure_site'] = {'file': frame.filename, 'line': frame.lineno, 'function': frame.name}
    finally:
        release.set(); stopping.set()
        if browser_open:
            try:
                browser(['close'])
                proof['browser_cleanup'] = 'owned CLI close returned; browser daemon PID unknown, no absence claim'
            except BaseException as error:
                proof['cleanup_errors'].append(type(error).__name__)
        if process is not None:
            if process.poll() is None:
                proof['natural_exit_failed'] = True
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill(); process.wait(timeout=10)
                proof['selected_complete'] = False
            proof['native_pid_absent'] = absent(process.pid)
        if native_log is not None:
            native_log.close()
        with lock:
            active = list(streams)
        for stream in active:
            try:
                stream.close()
            except BaseException as error:
                proof['cleanup_errors'].append(type(error).__name__)
        if server is not None:
            try:
                server.shutdown(); server.server_close()
            except BaseException as error:
                proof['cleanup_errors'].append(type(error).__name__)
        credential_copy = state / 'native-session'
        if credential_copy.exists():
            credential_copy.unlink(); proof['credential_cleanup'].append('owned native-session removed')
        proof['cursor_retry_attempts'] = control['cursor_attempts']
        proof['complete'] = False
        proof['full_campaign_complete'] = False
        proof['selected_complete'] = proof['selected_complete'] and not proof['cleanup_errors'] and not proof['proxy_errors'] and not proof.get('natural_exit_failed', False)
        write(receipt_path, proof)
    return 0 if proof['selected_complete'] else 1
