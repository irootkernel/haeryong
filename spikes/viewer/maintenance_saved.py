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
import uuid


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



def task05_json(raw):
    def closed(pairs):
        result = {}
        for key, value in pairs:
            assert key not in result, 'duplicate action JSON key'
            result[key] = value
        return result
    return json.loads(raw, object_pairs_hook=closed)


def task05_equal(left, right):
    return json.dumps(left, sort_keys=True, separators=(',', ':')) == json.dumps(right, sort_keys=True, separators=(',', ':'))


def task05_pointer(value, pointer):
    assert pointer == '' or pointer.startswith('/')
    for part in pointer.split('/')[1:]:
        part = part.replace('~1', '/').replace('~0', '~')
        value = value[int(part)] if isinstance(value, list) else value[part]
    return value


def task05_owner_approve(endpoint, token, request, operation_id):
    """One real, nonredirecting owner request; no retry or proxy recursion."""
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):
            return None
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    call = urllib.request.Request(endpoint + request['path'],
        data=json.dumps(request['body'], separators=(',', ':')).encode(), method='POST',
        headers={'Authorization': 'Bearer ' + token, 'Origin': endpoint, 'Content-Type': 'application/json',
                 'If-Match': request['if_match'], 'Idempotency-Key': operation_id})
    try:
        response = opener.open(call, timeout=30)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        return response.status, response.read()


class Task05ActionFence:
    """Finite opt-in request fences; never a replacement backend or UI oracle."""

    def __init__(self, spec, grant):
        assert spec['schema'] == 'haeryong.task05-finite-actions/v1'
        assert spec['preparation_only'] is False and spec['controlled_fixture'] is True
        encoded = json.dumps(spec, sort_keys=True, separators=(',', ':')).encode()
        assert hashlib.sha256(encoded).hexdigest() == grant['task05_actions_sha256']
        self.cases = json.loads(encoded)['cases']
        assert self.cases and [c['id'] for c in self.cases] == grant['task05_action_case_ids']
        assert len({c['id'] for c in self.cases}) == len(self.cases)
        self.index, self.current, self.operations, self.records = 0, None, set(), []
        self.side_lock, self.side_records = threading.Lock(), []
        interleaved = [c for c in self.cases if 'interleave' in c]
        if interleaved:
            opt = grant.get('task05_owner_interleave')
            assert isinstance(opt, dict) and set(opt) == {'schema', 'case_specs', 'selected_case_ids',
                'max_deliveries_per_case', 'max_deliveries_this_driver', 'max_deliveries_selected_scope'}
            assert opt['schema'] == 'haeryong.task05-owner-interleave-release/v1'
            assert opt['max_deliveries_per_case'] == opt['max_deliveries_this_driver'] == 1
            assert opt['max_deliveries_selected_scope'] == 2
            selected = opt['selected_case_ids']
            assert 1 <= len(selected) <= 2 and len(selected) == len(set(selected))
            assert len(interleaved) == 1 and set(opt['case_specs']) == {c['id'] for c in interleaved}
            for case in interleaved:
                assert case['id'] in selected
                encoded_side = json.dumps(case['interleave'], sort_keys=True, separators=(',', ':')).encode()
                assert hashlib.sha256(encoded_side).hexdigest() == opt['case_specs'][case['id']]
        else:
            assert 'task05_owner_interleave' not in grant, 'unused side-owner authority'
        for case in self.cases:
            request = case['request']
            operation = request['operation']
            target = request['target']
            assert isinstance(target, str) and target and '/' not in target and '%' not in target
            from urllib.parse import quote
            encoded_target = quote(target, safe='')
            routes = {
                'findingDecision': ('POST', '/api/v1/findings/' + encoded_target + '/decisions'),
                'topicAssignmentSet': ('PUT', '/api/v1/topic-assignments/' + encoded_target),
                'overlayUndo': ('POST', '/api/v1/overlays/' + encoded_target + '/undo'),
                'scanStart': ('POST', '/api/v1/scans'),
            }
            assert operation in routes and (request['method'], request['path']) == routes[operation]
            body = request['body']
            if operation == 'findingDecision':
                assert set(body) == {'decision'} and body['decision'] in ('approve', 'reject')
            elif operation == 'topicAssignmentSet':
                assert set(body) == {'pinned', 'decision'} and type(body['pinned']) is bool
                assert body['decision'] in ('accepted', 'rejected') and not (body['pinned'] and body['decision'] == 'rejected')
            elif operation == 'overlayUndo':
                assert body == {}
            else:
                assert body['finding_id'] == target and type(body['expected_finding_revision']) is int and body['expected_finding_revision'] > 0
                assert body['scope'] == 'selected_projects' and body['document_id'] and body['version_id']
                assert body['depth'] and body['source_mode'] and body['project_ids']
                assert ('region_id' not in body or not body['region_id'] or body.get('page_path'))
            if operation == 'scanStart':
                assert request['if_match'] is None
            else:
                prefix = 'finding-' if operation == 'findingDecision' else 'overlay-'
                assert re.fullmatch(re.escape('"' + prefix + target + '-r') + r'[1-9][0-9]*"', request['if_match'])
            assert case['before'] and case['after'] and case['response_fields'] is not None
            assert type(case['max_deliveries']) is int and case['max_deliveries'] in (1, 2)
            assert type(case['required_deliveries']) is int and 1 <= case['required_deliveries'] <= case['max_deliveries']
            assert type(case['drop_ack_once']) is bool
            assert case['expected_status'] in (200, 202, 409, 412, 428, 503)
            assert 'UNBOUND' not in json.dumps(case), 'actual fixture/hooks must be bound before release'
            if 'interleave' in case:
                side = case['interleave']
                assert set(side) == {'kind', 'request', 'candidate_job_id', 'effect_id', 'linked_findings',
                                     'before', 'after', 'domain_reads'}
                assert side['kind'] == 'one-actual-owner-approve-before-forward'
                assert operation in ('overlayUndo', 'topicAssignmentSet') and target == side['effect_id']
                assert case['expected_status'] == 412 and case['max_deliveries'] == case['required_deliveries'] == 1
                assert not case['drop_ack_once'] and not case.get('require_receipt_get')
                child = side['request']; assert set(child) == {'operation', 'target', 'method', 'path', 'body', 'if_match'}
                assert isinstance(child['target'], str) and child['target'] and not any(c in child['target'] for c in '/%')
                assert child['operation'] == 'findingDecision' and child['method'] == 'POST'
                assert child['path'] == '/api/v1/findings/' + quote(child['target'], safe='') + '/decisions'
                assert child['body'] == {'decision': 'approve'}
                assert re.fullmatch(re.escape('"finding-' + child['target'] + '-r') + r'[1-9][0-9]*"', child['if_match'])
                links = side['linked_findings']
                assert len(links) >= 2 and all(set(x) == {'job_id', 'finding_id'} for x in links)
                assert len({x['finding_id'] for x in links}) == len(links) and child['target'] not in {x['finding_id'] for x in links}
                assert side['candidate_job_id'] and all(x['job_id'] and x['finding_id'] for x in links)
                assert side['before'] and side['after'] and side['domain_reads']
                job_paths = {'/api/v1/jobs/' + quote(job, safe='') + '/findings?limit=30'
                    for job in {side['candidate_job_id'], *(x['job_id'] for x in links)}}
                assert job_paths <= {read['path'] for read in side['domain_reads']}
                assert all(read['json_pointers'] == [''] for read in side['domain_reads']), 'complete controlled domain projection required'
                for read in side['before'] + side['after'] + side['domain_reads']:
                    assert set(read) == ({'path', 'envelope', 'json_pointers'} if read in side['domain_reads'] else {'path', 'envelope', 'json_pointers', 'expected'})
                    u = urllib.parse.urlsplit(read['path'])
                    assert not u.scheme and not u.netloc and not u.fragment
                    assert re.fullmatch(r'/api/v1/jobs/[^/%]+/findings', u.path) or u.path == '/api/v1/topic-assignments'
                    assert read['envelope'] is True and read['json_pointers']

    def arm(self, case_id, snapshot):
        assert self.current is None and self.index < len(self.cases)
        case = self.cases[self.index]
        assert case['id'] == case_id
        for spec in case['before']:
            assert task05_equal(snapshot(spec), spec['expected']), 'Task05 before oracle mismatch'
        self.current = {'case': case, 'operation_id': None, 'job_id': None, 'deliveries': 0, 'responses': [], 'verified': False,
                        'side': None, 'primary_forwarded': False, 'receipt_gate': None}
        if case['id'] == 'B1-all-linked-undo-lostack':
            assert case['drop_ack_once'] and case.get('require_receipt_get')
            assert case['required_deliveries'] == case['max_deliveries'] == 1
            self.current['receipt_gate'] = {'blocked': 0, 'released': False, 'forwarded': 0}

    def permit(self, method, path, headers, raw):
        current = self.current
        assert current is not None
        case, request = current['case'], current['case']['request']
        assert method == request['method'] and path == request['path']
        assert task05_equal(task05_json(raw), request['body'])
        keys = headers.get_all('Idempotency-Key', [])
        tags = headers.get_all('If-Match', [])
        assert len(keys) == 1 and re.fullmatch(r'[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}', keys[0])
        assert tags == ([] if request['if_match'] is None else [request['if_match']])
        assert current['deliveries'] < case['max_deliveries']
        if current['operation_id'] is None:
            with self.side_lock:
                assert keys[0] not in self.operations
                current['operation_id'] = keys[0]
                self.operations.add(keys[0])
        assert keys[0] == current['operation_id'], 'operation key rebound'
        current['deliveries'] += 1
        if 'interleave' in case:
            current['original'] = (method, path, bytes(raw), tuple(tags), tuple(keys))
        return current

    def before_forward(self, current, snapshot, findings, approve, operation, active):
        if 'interleave' not in current['case']:
            return
        side = current['case']['interleave']
        with self.side_lock:
            assert current is self.current and current['deliveries'] == 1 and current['side'] is None
            assert not self.side_records, 'one side approval maximum for this driver'
            proof = {'case_id': current['case']['id'], 'phase': 'running', 'deliveries': 0, 'verified': False}
            current['side'] = proof; self.side_records.append(proof)
        try:
            assert active(), 'abandoned primary request'
            for spec in side['before']:
                assert task05_equal(snapshot(spec), spec['expected']), 'side before oracle mismatch'
            def selected(job, identity):
                values = [f for f in findings(job) if f['finding_id'] == identity]
                assert len(values) == 1
                return values[0]
            candidate = selected(side['candidate_job_id'], side['request']['target'])
            assert candidate['status'] == 'proposed' and candidate['etag'] == side['request']['if_match']
            assert candidate['applied_ref'] is None
            old_tag = current['case']['request']['if_match']
            for link in side['linked_findings']:
                old = selected(link['job_id'], link['finding_id'])
                assert old['status'] == 'applied' and old['applied_ref']['overlay_id'] == side['effect_id']
                effect = old['effect']
                assert effect['availability'] == 'available' and effect['overlay_id'] == side['effect_id']
                assert effect['etag'] == old_tag and effect['decision'] == 'accepted' and effect['pinned'] is False
            child_id = str(uuid.uuid4())
            with self.side_lock:
                assert child_id not in self.operations
                self.operations.add(child_id); proof['operation_id'] = child_id
            assert active(), 'abandoned before side delivery'
            proof['deliveries'] = 1
            status, raw = approve(side['request'], child_id)
            proof['upstream'] = {'status': status, 'body_sha256': hashlib.sha256(raw).hexdigest()}
            assert status == 200, 'actual side approval not200'
            value = task05_json(raw)['data']
            assert value['operation_id'] == child_id and value['state'] == 'completed'
            assert value['finding']['finding_id'] == side['request']['target'] and value['finding']['status'] == 'applied'
            receipt = operation(child_id)
            assert receipt['operation_id'] == child_id and receipt['state'] == 'completed' and receipt['original_status'] == 200
            assert receipt['result_metadata'] == value, 'side receipt is not exact original result'
            proof['receipt_get'] = {'operation_id': child_id, 'original_status': receipt['original_status'],
                'data_sha256': hashlib.sha256(json.dumps(receipt, sort_keys=True, separators=(',', ':')).encode()).hexdigest()}
            new_tag = value['effect']['etag']
            assert value['effect']['overlay_id'] == side['effect_id'] and new_tag != old_tag
            for job, identity in [(x['job_id'], x['finding_id']) for x in side['linked_findings']] + [(side['candidate_job_id'], side['request']['target'])]:
                row = selected(job, identity); effect = row['effect']
                assert row['status'] == 'applied' and row['applied_ref']['overlay_id'] == side['effect_id']
                assert effect['overlay_id'] == side['effect_id'] and effect['availability'] == 'available'
                assert effect['etag'] == new_tag and effect['decision'] == 'accepted' and effect['pinned'] is False
            for spec in side['after']:
                assert task05_equal(snapshot(spec), spec['expected']), 'side after oracle mismatch'
            proof['post_side_projection'] = [snapshot(spec) for spec in side['domain_reads']]
            for spec, projected in zip(side['domain_reads'], proof['post_side_projection']):
                if '/findings?' in spec['path']:
                    assert projected['fields']['']['value']['next_cursor'] is None, 'controlled domain needs complete bound page'
            proof['old_effect_etag'], proof['current_effect_etag'] = old_tag, new_tag
            assert active(), 'abandoned after side commit; never forward late primary'
            proof['phase'] = 'ready'
        except BaseException as error:
            proof['phase'], proof['failure_type'] = 'failed', type(error).__name__
            proof['side_outcome_unknown'] = proof['deliveries'] == 1 and 'upstream' not in proof
            raise

    def mark_forwarded(self, current, method, path, headers, raw, active):
        if 'interleave' not in current['case']:
            return
        with self.side_lock:
            assert active() and current is self.current and not current['primary_forwarded']
            assert current['side']['phase'] == 'ready'
            assert (method, path, bytes(raw), tuple(headers.get_all('If-Match', [])), tuple(headers.get_all('Idempotency-Key', []))) == current['original']
            current['side']['primary_request'] = {'method': method, 'path': path, 'operation_id': current['operation_id'],
                'if_match': headers.get_all('If-Match'), 'raw_body_sha256': hashlib.sha256(raw).hexdigest()}
            current['primary_forwarded'] = True

    def observe(self, current, status, raw):
        assert current is self.current
        case = current['case']
        if 'interleave' in case:
            assert current['primary_forwarded'] and current['side']['phase'] == 'ready'
        assert status == case['expected_status'], 'actual upstream Task05 status mismatch'
        value = task05_json(raw)
        if status in (200, 202):
            result = value['data']
            assert result['operation_id'] == current['operation_id']
            if case['request']['operation'] == 'scanStart':
                assert isinstance(result['job_id'], str) and re.fullmatch(r'[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}', result['job_id'])
                current['job_id'] = result['job_id']
            else:
                assert result['state'] in ('completed', 'repair_pending')
                assert (status == 202) == (result['state'] == 'repair_pending')
        for pointer, expected in case['response_fields'].items():
            assert task05_equal(task05_pointer(value, pointer), self.expand(expected)), 'actual Task05 response projection mismatch'
        current['responses'].append({'status': status, 'body_sha256': hashlib.sha256(raw).hexdigest()})
        return case['drop_ack_once'] and len(current['responses']) == 1

    def expand(self, value):
        if isinstance(value, dict):
            return {key: self.expand(item) for key, item in value.items()}
        if isinstance(value, list):
            return [self.expand(item) for item in value]
        if isinstance(value, str):
            for name in ('operation_id', 'job_id'):
                marker = '$' + name
                if marker in value:
                    assert self.current[name] is not None
                    value = value.replace(marker, self.current[name])
        return value

    def state(self):
        if self.current is None:
            return {'active': False}
        current = self.current
        return {'active': True, 'case_id': current['case']['id'], 'responses': len(current['responses']),
                'deliveries': current['deliveries'], 'operation_id': current['operation_id'], 'job_id': current['job_id'],
                'receipt_gate': current['receipt_gate']}

    def verify(self, case_id, snapshot):
        current = self.current
        assert current and current['case']['id'] == case_id and current['responses']
        case = current['case']
        assert current['deliveries'] == case['required_deliveries'] <= case['max_deliveries']
        if 'interleave' in case:
            side = current['side']
            assert side['phase'] == 'ready' and current['primary_forwarded'] and len(current['responses']) == 1
            assert task05_equal([snapshot(spec) for spec in case['interleave']['domain_reads']], side['post_side_projection']), 'stale412 changed post-side domain state'
        for spec in case['after']:
            assert task05_equal(snapshot(self.expand(spec)), self.expand(spec['expected'])), 'Task05 after oracle mismatch'
        if 'interleave' in case:
            side['verified'], side['phase'] = True, 'verified'
        self.records.append({'case_id': case_id, 'operation_id': current['operation_id'], 'job_id': current['job_id'],
                             'deliveries': current['deliveries'], 'upstream': current['responses'], 'verified': True})
        self.index += 1
        self.current = None

    def finish(self):
        assert self.current is None and self.index == len(self.cases), 'finite Task05 cases incomplete'
        assert len(self.side_records) == sum('interleave' in case for case in self.cases)
        assert all(row['verified'] and row['deliveries'] == 1 for row in self.side_records), 'side owner proof incomplete'


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
             'events': [], 'faults': [], 'proxy_errors': [], 'credential_cleanup': [], 'render_admissions': []}
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
        def action_snapshot(spec):
            # Selected public-read projection only; never canonical mutation input.
            path = spec['path']
            assert path.startswith('/api/v1/') and not urllib.parse.urlsplit(path).netloc
            try:
                value = get(path, spec['envelope'])
            except urllib.error.HTTPError as error:
                assert error.code == 404
                return {'status': 404}
            fields = {}
            for pointer in spec['json_pointers']:
                try:
                    fields[pointer] = {'present': True, 'value': task05_pointer(value, pointer)}
                except (KeyError, IndexError):
                    fields[pointer] = {'present': False}
            return {'status': 200, 'fields': fields}

        action_fence = None
        if cfg.get('task05_actions') is not None:
            assert cfg['settings_mutations'] is False and cfg['controlled_v2'] is True
            action_fence = Task05ActionFence(cfg['task05_actions'], grant)
            proof['task05_actions'] = action_fence.records
            proof['task05_receipt_reads'] = []
            proof['task05_side_owner'] = action_fence.side_records

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
                        elif action == 'task05-arm':
                            assert action_fence is not None
                            action_fence.arm(value['case_id'], action_snapshot)
                        elif action == 'task05-state':
                            assert action_fence is not None
                            self.send(200, json.dumps(action_fence.state()).encode(), [('Content-Type', 'application/json')])
                            return
                        elif action == 'task05-receipt-recover':
                            assert action_fence is not None
                            current = action_fence.current
                            assert current is not None and current['case']['id'] == value['case_id'] == 'B1-all-linked-undo-lostack'
                            assert current['operation_id'] == value['operation_id']
                            gate = current['receipt_gate']
                            assert gate == {'blocked': 1, 'released': False, 'forwarded': 0}
                            assert current['deliveries'] == len(current['responses']) == 1
                            gate['released'] = True
                            proof['faults'].append({'kind': 'task05-B1-explicit-receipt-recovery-enabled',
                                'case_id': current['case']['id'], 'operation_id': current['operation_id']})
                        elif action == 'task05-verify':
                            assert action_fence is not None
                            current = action_fence.current
                            assert current is not None
                            if current['case'].get('require_receipt_get'):
                                assert current['case']['required_deliveries'] == current['case']['max_deliveries'] == 1
                                assert any(e['case_id'] == value['case_id'] and e['operation_id'] == current['operation_id']
                                           and e['method'] == 'GET' and e['status'] == 200 for e in proof['task05_receipt_reads'])
                            if current['receipt_gate'] is not None:
                                assert current['receipt_gate'] == {'blocked': 1, 'released': True, 'forwarded': 1}
                            action_fence.verify(value['case_id'], action_snapshot)
                        elif action == 'task05-render-closed':
                            assert action_fence is not None
                            opens = {(e['version_id'], e['session_id']) for e in proof['events'] if e['operation'] == 'open'}
                            closes = {(e['version_id'], e['session_id']) for e in proof['events'] if e['operation'] == 'close' and e['status'] == 204}
                            assert opens and opens == closes
                            assert all(e['status'] == 201 for e in proof['events'] if e['operation'] == 'open')
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
                action_request = action_fence is not None and self.command in ('POST', 'PUT') and bool(re.fullmatch(
                    r'/api/v1/(?:findings/[^/]+/decisions|topic-assignments/[^/]+|overlays/[^/]+/undo|scans)', u.path))
                allowed = self.command in ('GET', 'HEAD') or self.command == 'POST' and render is not None or self.command == 'PUT' and position is not None or policy or action_request
                if not allowed:
                    proof['proxy_errors'].append('forbidden-mutation:' + u.path)
                    self.send(403, b'')
                    return
                match = re.fullmatch(r'/api/v1/jobs/([^/]+)/findings', u.path)
                selected = match is not None and match[1] in jobs
                with lock:
                    deny, disconnected = control['denied'], control['disconnected']
                protected_action = action_fence is not None and (action_request or u.path.startswith((
                    '/api/v1/findings/', '/api/v1/operations/', '/api/v1/related-explanations', '/api/v1/topic-assignments')))
                if (selected or protected_action or u.path in ('/api/v1/session', '/api/v1/capabilities')) and (deny or disconnected):
                    self.send(401 if deny else 503, b'{"error":{"code":"CONTROLLED_UI_TRANSPORT_FAULT","retryable":false}}', [('Content-Type', 'application/json')])
                    return
                # Only B1's bound operation GET is faulted; never hold an old
                # successful response across remount or fabricate a receipt.
                if action_fence is not None and self.command == 'GET':
                    with lock:
                        current = action_fence.current
                        if current and current['receipt_gate'] is not None and self.path == '/api/v1/operations/' + str(current['operation_id']):
                            gate = current['receipt_gate']
                            if not gate['released']:
                                assert current['operation_id'] is not None and current['deliveries'] == len(current['responses']) == 1
                                assert gate['blocked'] == 0 and gate['forwarded'] == 0, 'unexpected extra automatic B1 receipt read'
                                gate['blocked'] = 1
                                proof['faults'].append({'kind': 'task05-B1-automatic-receipt-503-before-upstream',
                                    'case_id': current['case']['id'], 'operation_id': current['operation_id'],
                                    'method': 'GET', 'status': 503, 'upstream_forwarded': False})
                                self.send(503, b'{"error":{"code":"CONTROLLED_UI_TRANSPORT_FAULT","retryable":false}}', [('Content-Type', 'application/json')])
                                return
                            assert gate['blocked'] == 1 and gate['forwarded'] == 0, 'one explicit B1 receipt GET maximum'
                            gate['forwarded'] += 1
                body = self.rfile.read(int(self.headers.get('Content-Length') or 0)) or None
                with lock:
                    action_case = action_fence.permit(self.command, self.path, self.headers, body) if action_request else None
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
                if action_case is not None:
                    live = lambda: not stopping.is_set()
                    action_fence.before_forward(action_case, action_snapshot,
                        lambda job: rows('/api/v1/jobs/' + urllib.parse.quote(job, safe='') + '/findings?limit=30'),
                        lambda spec, key: task05_owner_approve(endpoint, token, spec, key),
                        lambda key: get('/api/v1/operations/' + key), live)
                    action_fence.mark_forwarded(action_case, self.command, self.path, self.headers, body, live)
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
                if action_fence is not None and self.command == 'GET':
                    with lock:
                        current = action_fence.current
                        if current and current['operation_id'] and self.path == '/api/v1/operations/' + current['operation_id']:
                            proof['task05_receipt_reads'].append({'case_id': current['case']['id'], 'method': 'GET',
                                'operation_id': current['operation_id'], 'status': status, 'body_sha256': hashlib.sha256(data).hexdigest()})
                if action_case is not None:
                    with lock:
                        drop_ack = action_fence.observe(action_case, status, data)
                    if drop_ack:
                        proof['faults'].append({'kind': 'task05-lost-ack-after-real-upstream', 'case_id': action_case['case']['id'], 'upstream_status': status})
                        self.connection.shutdown(socket.SHUT_RDWR)
                        self.connection.close()
                        return
                if action_fence is not None and render and self.command == 'POST' and render[3] is None:
                    # Preserve real upstream facts BEFORE the strict success gate.
                    # Never retain bearer/raw headers, handoff secrets or raw bodies.
                    admission = {'path': u.path, 'method': self.command, 'requested_version_id': render[1],
                        'request_body_sha256': hashlib.sha256(body or b'').hexdigest(),
                        'upstream_status': status, 'upstream_body_sha256': hashlib.sha256(data).hexdigest(),
                        'declared_readers': [{'document_id': row['document_id'], 'version_id': row['version_id'],
                            'page_paths': urllib.parse.parse_qs(urllib.parse.urlsplit(row['route']).query).get('page', [])}
                            for row in cfg['probe']['task05_readers'] if row['version_id'] == render[1]]}
                    try:
                        wire = json.loads(data)
                    except (ValueError, UnicodeDecodeError):
                        wire = {}
                    if isinstance(wire, dict):
                        error = wire.get('error')
                        if isinstance(error, dict) and isinstance(error.get('code'), str):
                            admission['public_error_code'] = error['code']
                        result = wire.get('data')
                        if isinstance(result, dict):
                            admission['response_version_id'] = result.get('version_id')
                            admission['response_session_id'] = result.get('session_id')
                    with lock:
                        proof['render_admissions'].append(admission)
                    assert status == 201, 'all Task05 actual render opens require 201'
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
                        frame = traceback.extract_tb(error.__traceback__)[-1]
                        proof['proxy_errors'].append({'type': type(error).__name__, 'path': urllib.parse.urlsplit(self.path).path,
                            'site': {'file': Path(frame.filename).name, 'line': frame.lineno, 'function': frame.name}})
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
        if action_fence is not None:
            config['task05_actions'] = cfg['task05_actions']
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
            proof['native_close_facts'] = {'opened': sorted(opens), 'closed': sorted(closed), 'windowClose': window,
                'windowClose_event_observed': any(row['event'] == 'windowClose' for row in observed)}
            assert opens and isinstance(window, list) and all(row.get('status') == 204 for row in window) and opens == closed
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
        if action_fence is not None:
            action_fence.finish()
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
