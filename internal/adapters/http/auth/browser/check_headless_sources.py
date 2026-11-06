"""Standalone opt-in headless-source and private-web boundary driver (TASK-007-05).

Executes against a normal untagged ./cmd/haeryong build from a non-repository
working directory with explicit --profile/--data-dir/--client-dir/--credential
flags, without setting HAERYONG_TEST_STATE_DIR, HOME, or CODEX_HOME, and with
--provider-binary pointing to a nonexistent path under the owned scratch root.
"""
import argparse
import hashlib
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
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument(
    '--browser-cli',
    type=Path,
    required=True,
    help='Path to playwright_cli.sh wrapper',
)
parser.add_argument(
    '--scratch-root',
    type=Path,
    required=True,
    help='Writable scratch parent directory outside the repository',
)
parser.add_argument(
    '--receipt',
    type=Path,
    required=True,
    help='Destination path for the structured JSON execution receipt',
)
parser.add_argument(
    '--binary',
    type=Path,
    help='Optional prebuilt normal untagged haeryong binary; built from ./cmd/haeryong when omitted',
)
parser.add_argument('--screenshot', type=Path, help='Optional PNG screenshot path')
parser.add_argument('--management-port', type=int, help='Optional loopback management port')
parser.add_argument('--renderer-pool-base', type=int, help='Optional loopback renderer pool base port')
parser.add_argument('--external-base-url', help='Canonical https://<node>[:port] URL for private Tailscale Serve mode')
parser.add_argument('--renderer-origin-1', help='Canonical https://<node>:<port1> URL for isolated renderer 1')
parser.add_argument('--renderer-origin-2', help='Canonical https://<node>:<port2> URL for isolated renderer 2')
parser.add_argument('--renderer-upstream-1', help='Optional http://127.0.0.1:<port1> upstream for renderer 1')
parser.add_argument('--renderer-upstream-2', help='Optional http://127.0.0.1:<port2> upstream for renderer 2')
parser.add_argument('--ready-file', type=Path, help='Coordinator handshake output written before Serve setup')
parser.add_argument('--serve-ready-file', type=Path, help='Coordinator handshake input awaited before remote browser open')
parser.add_argument('--web-complete-file', type=Path, help='Coordinator handshake output written after remote browser checks')
parser.add_argument('--serve-removed-file', type=Path, help='Coordinator handshake input awaited after Serve removal')
parser.add_argument('--keep-work', action='store_true', help='Retain the owned scratch directory after completion')
args = parser.parse_args()

live = bool(args.external_base_url)
if live:
    assert all((
        args.renderer_origin_1,
        args.renderer_origin_2,
        args.ready_file,
        args.serve_ready_file,
        args.web_complete_file,
        args.serve_removed_file,
    )), 'Live private-HTTPS mode requires --renderer-origin-1/2 and all four handshake files'
    assert all(
        urllib.parse.urlsplit(url).scheme == 'https'
        for url in (args.external_base_url, args.renderer_origin_1, args.renderer_origin_2)
    ), 'Live private-HTTPS origins must use https://'
else:
    assert not any((
        args.renderer_origin_1,
        args.renderer_origin_2,
        args.ready_file,
        args.serve_ready_file,
        args.web_complete_file,
        args.serve_removed_file,
    )), 'Local-deterministic mode must not pass live private-HTTPS flags'

REPO = Path(__file__).resolve().parents[5]
SCRIPT_PATH = Path(__file__).resolve()
JS_PATH = SCRIPT_PATH.with_suffix('.js').resolve(strict=True)
receipt = args.receipt.resolve()
assert receipt.parent.is_dir() and not receipt.exists(), 'Receipt parent must exist and receipt must not pre-exist'
screenshot_path = args.screenshot.resolve() if args.screenshot else None
ready_file = args.ready_file.resolve() if args.ready_file else None
serve_ready_file = args.serve_ready_file.resolve() if args.serve_ready_file else None
web_complete_file = args.web_complete_file.resolve() if args.web_complete_file else None
serve_removed_file = args.serve_removed_file.resolve() if args.serve_removed_file else None
prebuilt_binary = args.binary.resolve(strict=True) if args.binary else None
cli = str(args.browser_cli.resolve(strict=True))

assert 'HAERYONG_TEST_STATE_DIR' not in os.environ, 'Normal untagged proof must not set HAERYONG_TEST_STATE_DIR'

os.umask(0o077)
scratch_root = args.scratch_root.resolve(strict=True)
work = Path(tempfile.mkdtemp(prefix='h5s-', dir=str(scratch_root))).resolve()
assert not work.is_relative_to(REPO), 'Scratch work directory must live outside the repository'

run_cwd = work / 'nonrepo-cwd'
run_cwd.mkdir(mode=0o700)
assert not run_cwd.is_relative_to(REPO), 'Execution CWD must live outside the repository'

# Never mutate HOME, CODEX_HOME, or HAERYONG_TEST_STATE_DIR.
env = dict(os.environ)
env.update({
    'TMPDIR': str(work),
    'PLAYWRIGHT_DAEMON_SOCKETS_DIR': str(work / 'sockets'),
    'PWTEST_SOCKETS_DIR': str(work / 'sockets'),
    'PLAYWRIGHT_DAEMON_SESSION_DIR': str(work / 'registry'),
    'PYTHONDONTWRITEBYTECODE': '1',
    'GOTOOLCHAIN': 'local',
    'GOPROXY': 'off',
    'GOSUMDB': 'off',
})
assert 'HAERYONG_TEST_STATE_DIR' not in env

(work / 'sockets').mkdir(mode=0o700)
(work / 'output/playwright').mkdir(parents=True)
(run_cwd / 'output/playwright').mkdir(parents=True)
pw_config = json.dumps({
    'browser': {
        'launchOptions': {'headless': True},
        'contextOptions': {'ignoreHTTPSErrors': False, 'viewport': {'width': 1440, 'height': 900}},
    }
})
(work / 'playwright-cli.json').write_text(pw_config)
(run_cwd / 'playwright-cli.json').write_text(pw_config)

spec = importlib.util.spec_from_file_location('h', REPO / 'tests/e2e/harness.py')
h = importlib.util.module_from_spec(spec)
spec.loader.exec_module(h)

os.chdir(run_cwd)
assert Path.cwd() == run_cwd and not Path.cwd().is_relative_to(REPO)

session = work.name
product = None
browser_attempted = False
secrets = set()
proof = {
    'scope': 'TASK-007-05 headless source-absence and private-web boundary proof',
    'mode': 'live-tailscale' if live else 'local-deterministic',
    'runtime': str(work),
    'nonrepo_cwd': str(run_cwd),
    'test_state_env_absent': 'HAERYONG_TEST_STATE_DIR' not in os.environ and 'HAERYONG_TEST_STATE_DIR' not in env,
    'complete': False,
    'cases': [],
    'cleanup': {},
    'physical_second_device': 'not_run',
    'physical_mobile': 'not_run',
    'native_Wails_UI': 'not_run',
    'provider_calls': 0,
}


def redact(value):
    text = str(value)
    for secret in secrets:
        if secret:
            text = text.replace(secret, '[redacted]')
    return text


def fingerprints():
    paths = [
        p for folder in ('cmd', 'internal', 'skills')
        for p in (REPO / folder).rglob('*.go')
        if not p.name.endswith('_test.go')
    ]
    paths += [p for p in (REPO / 'internal/webassets/dist').rglob('*') if p.is_file()]
    paths += [p for p in (REPO / 'frontend/src').rglob('*') if p.is_file() and not p.name.endswith('.test.ts')]
    paths += [
        REPO / 'frontend/check_build.py',
        REPO / 'skills/use-haeryong/SKILL.md',
        REPO / 'internal/application/configuration/schema.json',
        REPO / 'internal/application/import/metadata-schema.json',
        REPO / 'internal/application/storage/defaults.json',
        REPO / 'go.mod',
        REPO / 'go.sum',
        SCRIPT_PATH,
        JS_PATH,
    ]
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
        time.sleep(0.1)
    payload = json.loads(path.read_text())
    assert payload['session'] == session
    return payload


def call(arguments, result=False, timeout=90):
    run = subprocess.run(
        [cli, '--session=' + session, *arguments],
        cwd=run_cwd,
        env=env,
        capture_output=True,
        text=True,
        timeout=timeout,
    )
    if run.returncode or '### Error' in run.stdout:
        detail = run.stdout.split('### Error', 1)[-1].split('### Ran', 1)[0]
        proof['browser_error'] = redact(detail or run.stderr)[-2400:]
        raise AssertionError('Browser operation failed; see private receipt')
    if result:
        match = re.search(r'### Result\s*\n', run.stdout)
        assert match, 'Browser result missing'
        return json.JSONDecoder().raw_decode(run.stdout[match.end():].lstrip())[0]


def js(code, timeout=120):
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
    status, value, _ = product.request(method, path, **kwargs)
    assert status in expected, f'Unexpected local HTTP status {status} for {path.split("?")[0]}: {value}'
    return value.get('data') if isinstance(value, dict) and 'data' in value else value


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
        time.sleep(0.1)


def drain_fixture_indexes():
    jobs = local('GET', '/api/v1/jobs?limit=100', client=owner)
    assert not jobs.get('next_cursor'), 'Unexpected fixture job page; no partial readiness claim'
    for job in jobs['items']:
        if job['kind'] == 'derived_index':
            product.job(owner, job['job_id'], timeout=60)


def configure(changes, label, *, exposure=None):
    drain_fixture_indexes()
    current = local('GET', '/api/v1/configuration', client=owner)
    path = work / f'{label}.json'
    h.write_json(path, changes)
    plan = product.cli(
        ['config', 'plan', str(path), '--expected-config-revision', str(current['config_revision'])],
        client=owner,
    )['data']['result']
    impact = plan['impact']['exposure']
    assert impact['proxy_configuration_changed_by_commit'] is False
    if exposure is not None:
        assert impact['changed'] is exposure
    command = ['config', 'apply', plan['plan_id'], '--expected-config-revision', str(plan['base_config_revision']), '--confirmed']
    previous = identity()
    product.cli(command, client=owner)
    if plan['impact']['controlled_restart_required']:
        current_identity = wait_restart(previous)
    else:
        current_identity = identity()
    updated = local('GET', '/api/v1/configuration', client=owner)
    assert updated['config_revision'] == current['config_revision'] + 1
    proof['cases'].append({
        'kind': label,
        'actual': {
            'impact': plan['impact'],
            'config_revision': updated['config_revision'],
            'instance_changed': current_identity['instance_id'] != previous['instance_id'],
        },
    })
    return plan


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


def write_multipage_fixture(target_dir: Path, label: str, run_token: str, *, include_regions: bool):
    (target_dir / 'nested').mkdir(parents=True, exist_ok=True)
    regions_html = ''
    if include_regions:
        regions_html = (
            '<section id="overview" data-haeryong-region="overview" tabindex="-1">'
            '<h2>Overview Region</h2>'
            '<p id="overview-copy">Anchored explanation region with unretained Git and retained snapshot citations.</p>'
            '</section>'
        )
    (target_dir / 'first.html').write_text(
        f'''<!doctype html><meta charset="utf-8"><title>{label} Reader</title>
<link rel="stylesheet" href="style.css">
<h1 id="heading">{label} first</h1>
<button id="increment" onclick="this.textContent=String(Number(this.textContent)+1)">0</button>
<a id="next" href="nested/second.html?scenario=2#part">next</a>
<a id="newtab" target="_blank" href="nested/second.html?popup=1#part">new tab</a>
{regions_html}
<script type="module">
import {{value}} from './module.js';
window.moduleValue = value;
new Worker('./worker.js').onmessage = e => {{ window.workerValue = e.data; }};
fetch('/root-asset.txt').then(r => r.text()).then(t => {{ window.rootAsset = t.trim(); }});
</script>
'''
    )
    (target_dir / 'nested/second.html').write_text(
        f'''<!doctype html><meta charset="utf-8"><title>{label} Second</title>
<h1 id="second">{label} second</h1>
<h2 id="part">Exact fragment</h2>
<a id="back-first" href="../first.html">first</a>
'''
    )
    (target_dir / 'style.css').write_text('#heading{color:rgb(1,2,3)}')
    (target_dir / 'module.js').write_text('export const value="module-running";')
    (target_dir / 'worker.js').write_text('postMessage("worker-running");')
    (target_dir / 'root-asset.txt').write_text(f'asset-{label}-{run_token}\n')


def stop_signal(signum, frame):
    raise KeyboardInterrupt('owned driver interrupted')


signal.signal(signal.SIGTERM, stop_signal)
nonexistent_provider = work / 'nonexistent-task05-codex-binary'
assert not nonexistent_provider.exists()

before = fingerprints()
try:
    binary_target = work / 'haeryong'
    if prebuilt_binary is not None:
        shutil.copy2(prebuilt_binary, binary_target)
        binary_target.chmod(0o755)
        assert hashlib.sha256(binary_target.read_bytes()).hexdigest() == hashlib.sha256(prebuilt_binary.read_bytes()).hexdigest()
    else:
        subprocess.run(
            ['go', 'build', '-o', str(binary_target), './cmd/haeryong'],
            cwd=REPO,
            env=env,
            capture_output=True,
            check=True,
            timeout=180,
        )
    assert fingerprints() == before
    # Inspect the actual copied build rather than infer its mode from symbols.
    build_info = subprocess.run(
        ['go', 'version', '-m', str(binary_target)], cwd=run_cwd, env=env,
        capture_output=True, text=True, check=True,
    ).stdout
    assert '\tpath\thaeryong.local/haeryong/cmd/haeryong' in build_info
    assert '\tbuild\t-tags=' not in build_info, 'Expected an untagged normal build'
    proof['build_info'] = build_info
    proof['binary_sha256'] = hashlib.sha256(binary_target.read_bytes()).hexdigest()
    proof['normal_untagged_binary'] = True

    product = h.Product(binary_target, work)
    if args.management_port:
        product.address = args.management_port
        product.endpoint = 'http://127.0.0.1:' + str(product.address)
    product.initialize()
    product.start(provider_binary=nonexistent_provider)
    assert not nonexistent_provider.exists()

    owner = product.owner('Task05 local owner')
    secrets.add(owner['token'])

    pool = free_pool()
    for port in range(pool, pool + 10):
        assert port not in (9272, 9273, product.address)
        with socket.socket() as probe:
            probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            probe.bind(('127.0.0.1', port))

    upstream1 = args.renderer_upstream_1 or f'http://127.0.0.1:{pool}'
    upstream2 = args.renderer_upstream_2 or f'http://127.0.0.1:{pool + 1}'
    upstream1_port = urllib.parse.urlsplit(upstream1).port
    upstream2_port = urllib.parse.urlsplit(upstream2).port
    assert upstream1_port == pool and upstream2_port == pool + 1

    configure(
        {
            'network': {
                'listener': {'port': product.address},
                'allowed_origins': [product.endpoint],
                'renderer_pool': {'base_port': pool, 'count': 4},
            }
        },
        'persist-listener-and-initial-renderer-pool',
    )
    product.stop()
    product.start(configured=True, provider_binary=nonexistent_provider)
    assert not nonexistent_provider.exists()
    assert product.cli(['doctor'], client=owner)['data']['endpoint_checks']['ephemeral_override'] is False

    # Step 1: Register an owned project and two owned source roots under work.
    proj_reg = product.cli(['projects', 'register', '--name', 'Task05 Headless Boundary Project'], client=owner)['data']['result']
    project_id = proj_reg['project_id']

    git_root = work / 'owned-git-source'
    (git_root / 'notes').mkdir(parents=True)
    git_only_bytes = b'Git-only unretained line 1: host checkout required\nGit-only unretained line 2: reference_only evidence\n'
    git_only_sha256 = hashlib.sha256(git_only_bytes).hexdigest()
    (git_root / 'notes/git-only.md').write_bytes(git_only_bytes)

    ask_missing_bytes = b'Current-mode ask target line 1\nCurrent-mode ask target line 2\n'
    ask_missing_sha256 = hashlib.sha256(ask_missing_bytes).hexdigest()
    (git_root / 'notes/ask-missing.md').write_bytes(ask_missing_bytes)

    for cmd in (
        ['git', '-c', 'core.hooksPath=/dev/null', 'init', '--quiet', '--object-format=sha1'],
        ['git', 'config', 'user.email', 'task05@example.invalid'],
        ['git', 'config', 'user.name', 'Task05 Fixture'],
        ['git', 'add', '.'],
        ['git', '-c', 'core.hooksPath=/dev/null', '-c', 'commit.gpgsign=false', 'commit', '--quiet', '-m', 'Task05 owned git source'],
    ):
        subprocess.run(cmd, cwd=git_root, env=env, check=True, capture_output=True, text=True)
    exact_revision = subprocess.run(
        ['git', 'rev-parse', 'HEAD'], cwd=git_root, env=env, check=True, capture_output=True, text=True
    ).stdout.strip()
    assert len(exact_revision) == 40

    git_source_id = str(uuid.uuid4())
    git_repo_id = str(uuid.uuid4())

    snap_root = work / 'owned-snapshot-source'
    (snap_root / 'snap').mkdir(parents=True)
    retained_bytes = (
        b'Retained snapshot line 1: pinned headless proof\n'
        b'Retained snapshot line 2: readable after host checkout removal\n'
    )
    retained_sha256 = hashlib.sha256(retained_bytes).hexdigest()
    (snap_root / 'snap/retained.md').write_bytes(retained_bytes)
    snap_source_id = str(uuid.uuid4())

    product.cli(
        [
            'sources', 'approve',
            '--project', project_id,
            '--source-id', git_source_id,
            '--kind', 'git',
            '--root-path', str(git_root),
            '--repo-id', git_repo_id,
            '--retention-mode', 'reference_only',
            '--provider-ids', 'codex',
            '--confirmed',
        ],
        client=owner,
    )
    product.cli(
        [
            'sources', 'approve',
            '--project', project_id,
            '--source-id', snap_source_id,
            '--kind', 'files',
            '--root-path', str(snap_root),
            '--retention-mode', 'selected_snapshot',
            '--provider-ids', 'codex',
            '--confirmed',
        ],
        client=owner,
    )
    configure(
        {'projects': {project_id: {'provider_id': 'codex', 'source_mode_default': 'pinned'}}},
        'task05-project-provider-and-source-default',
    )

    # Step 2: Capture retained snapshot bytes before removing owned checkouts.
    capture_req_path = work / 'capture-request.json'
    h.write_json(
        capture_req_path,
        {
            'source_mode': 'current',
            'selections': [
                {
                    'source_id': snap_source_id,
                    'paths': [{'path': 'snap/retained.md'}],
                }
            ],
        },
    )
    capture_res = product.cli(
        ['sources', 'capture', '--project', project_id, '--input', str(capture_req_path)],
        client=owner,
    )['data']['result']
    capture_id = capture_res['capture_id']
    capture_target_set_id = capture_res['target_set_id']
    assert capture_id and capture_target_set_id

    captured_view = product.cli(['sources', 'captured', '--capture', capture_id], client=owner)['data']
    assert len(captured_view['items']) == 1
    retained_item = captured_view['items'][0]
    retained_read_id = retained_item['read_id']
    assert retained_item['source_id'] == snap_source_id
    assert retained_item['bytes_sha256'] == retained_sha256
    assert retained_item['availability'] == 'available'
    assert retained_item['retention_mode'] == 'selected_snapshot'
    assert retained_item['read_state'] == 'not_read_by_view'

    # Step 3: Import two multi-page versions (v_sourced with Git + snapshot evidence, v_plain without metadata).
    run_token = uuid.uuid4().hex[:12]
    sourced_dir = work / 'import-sourced'
    write_multipage_fixture(sourced_dir, 'Sourced', run_token, include_regions=True)
    (sourced_dir / 'snap').mkdir(parents=True, exist_ok=True)
    (sourced_dir / 'snap/retained.md').write_bytes(retained_bytes)

    sourced_meta_path = work / 'sourced-metadata.json'
    h.write_json(
        sourced_meta_path,
        {
            'schema_version': '1.0',
            'version': {'source_intent': 'moving_target'},
            'content': {'default_page': 'first.html'},
            'pages': [
                {'page_path': 'first.html', 'label': 'First page'},
                {'page_path': 'nested/second.html', 'label': 'Second page'},
            ],
            'sources': [
                {
                    'id': git_source_id,
                    'kind': 'git',
                    'retention_mode': 'reference_only',
                    'repo_id': git_repo_id,
                    'object_format': 'sha1',
                    'exact_revision': exact_revision,
                    'release_label': 'git-ref-v1',
                },
                {
                    'id': snap_source_id,
                    'kind': 'snapshot',
                    'retention_mode': 'selected_snapshot',
                    'snapshot_sha256': retained_sha256,
                    'asset_path': 'snap/retained.md',
                    'bytes_sha256': retained_sha256,
                    'release_label': 'snap-retained-v1',
                },
            ],
            'evidence': [
                {
                    'id': 'ev-git-unretained',
                    'source_id': git_source_id,
                    'capture_status': 'reference_only',
                    'path': 'notes/git-only.md',
                    'line_start': 1,
                    'line_end': 2,
                    'bytes_sha256': git_only_sha256,
                },
                {
                    'id': 'ev-snap-retained',
                    'source_id': snap_source_id,
                    'capture_status': 'verified',
                    'path': 'snap/retained.md',
                    'line_start': 1,
                    'line_end': 2,
                    'bytes_sha256': retained_sha256,
                },
            ],
            'regions': [
                {
                    'id': 'overview',
                    'page_path': 'first.html',
                    'label': 'Overview Region',
                    'evidence_ids': ['ev-git-unretained', 'ev-snap-retained'],
                }
            ],
        },
    )
    _, _, pub_sourced = product.import_tree(
        owner,
        sourced_dir,
        extra=('--project', project_id, '--new-document', '--metadata', str(sourced_meta_path)),
    )
    doc_sourced_id = pub_sourced['document_id']
    v_sourced_id = pub_sourced['version_id']

    plain_dir = work / 'import-plain'
    write_multipage_fixture(plain_dir, 'Plain', run_token, include_regions=False)
    _, _, pub_plain = product.import_tree(
        owner,
        plain_dir,
        extra=('--project', project_id, '--new-document'),
    )
    doc_plain_id = pub_plain['document_id']
    v_plain_id = pub_plain['version_id']
    drain_fixture_indexes()

    # Step 4: Record one pre-removal Git observation so historical observation vs live filesystem absence is explicit.
    obs_before = product.cli(['sources', 'observe', '--version', v_sourced_id], client=owner)['data']['result']
    assert obs_before['target_set_id'] is not None
    assert obs_before['sources'] == [
        {
            'source_id': git_source_id,
            'observation_id': obs_before['sources'][0]['observation_id'],
            'availability': 'available',
            'classification': 'unchanged',
            'reassessment_required': False,
            'reason': None,
            'changed_paths': [],
            'dependency_scope': [],
        }
    ]
    sources_before_delete = product.cli(['sources', 'status', '--project', project_id], client=owner)['data']['items']
    by_id_before = {row['source_id']: row for row in sources_before_delete}
    assert by_id_before[git_source_id]['last_availability'] == 'available'
    assert by_id_before[git_source_id]['last_observed_revision'] == exact_revision

    # Step 5: Remove both owned source checkouts and both import directories before any Reader open or absence test.
    for path_to_remove in (git_root, snap_root, sourced_dir, plain_dir):
        shutil.rmtree(path_to_remove)
        assert not path_to_remove.exists(), f'Owned path still exists after removal: {path_to_remove}'

    # Configure remote renderer mappings (without opening versions locally first) and restart daemon.
    external = args.external_base_url or 'https://node.tailnet.ts.net:9271'
    renderer1 = args.renderer_origin_1 or 'https://node.tailnet.ts.net:9291'
    renderer2 = args.renderer_origin_2 or 'https://node.tailnet.ts.net:9292'
    shifted = pool + 2 if not live else 9281
    if live:
        mappings = [
            {'version_id': v_sourced_id, 'origin': renderer1, 'upstream': upstream1, 'reassignable': False},
            {'version_id': v_plain_id, 'origin': renderer2, 'upstream': upstream2, 'reassignable': False},
        ]
        configure(
            {
                'network': {
                    'remote_enabled': True,
                    'external_base_url': external,
                    'allowed_origins': [product.endpoint, external],
                    'renderer_pool': {'base_port': shifted, 'count': 8},
                    'renderer_mappings': mappings,
                }
            },
            'private-exposure-preview-confirm-and-restart',
            exposure=True,
        )

    # Full process stop + start after checkout/import removal to prove durability across restart.
    product.stop()
    product.start(configured=True, provider_binary=nonexistent_provider)
    assert not nonexistent_provider.exists()

    # Step 6: Verify recorded-only source status before triggering post-removal observation.
    sources_recorded_only = product.cli(['sources', 'status', '--project', project_id], client=owner)['data']['items']
    by_id_recorded = {row['source_id']: row for row in sources_recorded_only}
    assert by_id_recorded[git_source_id]['last_availability'] == 'available'
    assert by_id_recorded[git_source_id]['last_observed_revision'] == exact_revision

    # Trigger mandatory post-removal owner source observation: resolves missing_source without fabricating a target set.
    obs_after = product.cli(['sources', 'observe', '--version', v_sourced_id], client=owner)['data']['result']
    assert obs_after['target_set_id'] is None
    assert obs_after['captured_at'] is None
    assert obs_after['sources'] == [
        {
            'source_id': git_source_id,
            'observation_id': obs_after['sources'][0]['observation_id'],
            'availability': 'unavailable',
            'classification': 'unavailable',
            'reassessment_required': False,
            'reason': 'missing_source',
            'changed_paths': [],
            'dependency_scope': [],
        }
    ]
    sources_after_observe = product.cli(['sources', 'status', '--project', project_id], client=owner)['data']['items']
    by_id_after = {row['source_id']: row for row in sources_after_observe}
    assert by_id_after[git_source_id]['last_availability'] == 'unavailable'
    assert by_id_after[git_source_id]['last_observation_reason'] == 'missing_source'
    # Last captured identity remains historical when a later observation fails.
    assert by_id_after[git_source_id]['last_observed_revision'] == exact_revision
    proof['cases'].append({
        'kind': 'post-removal source observation records missing_source without target_set fabrication',
        'actual': {
            'recorded_before_new_observe': by_id_recorded[git_source_id],
            'observation_after_removal': obs_after,
            'status_after_new_observe': by_id_after[git_source_id],
        },
    })

    # Step 7: Explicit retained pinned source read after checkout deletion.
    retained_out_file = work / 'retained-read-output.md'
    read_cli = product.cli(
        ['sources', 'read', '--read', retained_read_id, '--output', str(retained_out_file)],
        client=owner,
    )['data']
    assert retained_out_file.read_bytes() == retained_bytes
    assert read_cli['read_id'] == retained_read_id
    assert read_cli['bytes_sha256'] == retained_sha256
    assert read_cli['input_observation'] is False
    retained_list = local('GET', f'/api/v1/projects/{project_id}/source-reads?source_id={snap_source_id}', client=owner)
    assert len(retained_list['items']) == 1
    assert retained_list['items'][0]['read_id'] == retained_read_id
    assert retained_list['items'][0]['availability'] == 'available'
    assert retained_list['items'][0]['retention_mode'] == 'selected_snapshot'
    assert retained_list['items'][0]['bytes_sha256'] == retained_sha256
    assert retained_list['items'][0]['read_state'] == 'not_read_by_view'
    proof['cases'].append({
        'kind': 'explicit retained selected_snapshot read succeeds after checkout deletion without claiming provider input',
        'actual': {
            'cli_read': read_cli,
            'retained_row': retained_list['items'][0],
        },
    })

    # Step 8: CLI posthoc verification contrast (unretained Git vs retained snapshot).
    ev_git_before = product.cli(
        ['evidence', 'show', '--version', v_sourced_id, '--evidence', 'ev-git-unretained'],
        client=owner,
    )['data']
    assert ev_git_before['authored']['capture_status'] == 'reference_only'
    assert ev_git_before['availability'] == 'unavailable'
    assert ev_git_before['reason'] == 'missing_source'
    assert ev_git_before['bytes_origin'] is None

    ev_snap_before = product.cli(
        ['evidence', 'show', '--version', v_sourced_id, '--evidence', 'ev-snap-retained'],
        client=owner,
    )['data']
    assert ev_snap_before['authored']['capture_status'] == 'verified'
    assert ev_snap_before['availability'] == 'available'
    assert ev_snap_before['bytes_origin'] == 'retained_snapshot'
    assert ev_snap_before['excerpt']['bytes_sha256'] == retained_sha256

    verify_git_cli = product.cli(
        [
            'evidence', 'verify',
            '--version', v_sourced_id,
            '--evidence', 'ev-git-unretained',
            '--expected-sha256', git_only_sha256,
        ],
        client=owner,
    )['data']['result']
    assert verify_git_cli['outcome'] == 'unavailable'
    assert verify_git_cli['reason'] == 'missing_source'
    assert verify_git_cli['bytes_origin'] is None
    assert verify_git_cli['verified_at'] is None

    verify_snap_cli = product.cli(
        [
            'evidence', 'verify',
            '--version', v_sourced_id,
            '--evidence', 'ev-snap-retained',
            '--expected-sha256', retained_sha256,
        ],
        client=owner,
    )['data']['result']
    assert verify_snap_cli['outcome'] == 'matched'
    assert verify_snap_cli['reason'] is None
    assert verify_snap_cli['bytes_origin'] == 'retained_snapshot'
    assert verify_snap_cli['bytes_sha256'] == retained_sha256
    assert verify_snap_cli['verified_at'] is not None

    ev_git_after = product.cli(
        ['evidence', 'show', '--version', v_sourced_id, '--evidence', 'ev-git-unretained'],
        client=owner,
    )['data']
    ev_snap_after = product.cli(
        ['evidence', 'show', '--version', v_sourced_id, '--evidence', 'ev-snap-retained'],
        client=owner,
    )['data']
    assert ev_git_after['authored']['capture_status'] == 'reference_only'
    assert ev_snap_after['authored']['capture_status'] == 'verified'
    proof['cases'].append({
        'kind': 'posthoc verification contrast between unretained git missing_source and retained selected_snapshot',
        'actual': {
            'git_unretained_verification': verify_git_cli,
            'snapshot_retained_verification': verify_snap_cli,
            'authored_capture_status_preserved': {
                'ev-git-unretained': ev_git_after['authored']['capture_status'],
                'ev-snap-retained': ev_snap_after['authored']['capture_status'],
            },
        },
    })

    # Step 9: Current-source missing evidence Question (explicit source_selection on deleted Git checkout).
    ask_req_path = work / 'ask-missing-source.json'
    h.write_json(
        ask_req_path,
        {
            'question': 'Can current source mode read notes/ask-missing.md after the host checkout was removed?',
            'provider_id': 'codex',
            'source_mode': 'current',
            'page_path': 'first.html',
            'region_id': 'overview',
            'source_selection': {
                'source_mode': 'current',
                'selections': [
                    {
                        'source_id': git_source_id,
                        'paths': [{'path': 'notes/ask-missing.md'}],
                    }
                ],
            },
        },
    )
    ask_rows = product.cli(
        ['ask', '--version', v_sourced_id, '--request-file', str(ask_req_path)],
        client=owner, code=5, ndjson=True,
    )
    # The CLI emits the complete saved outcome, then its separate nonzero error
    # envelope. Preserve both native records rather than inventing error details.
    saved_outcomes = [row['data'] for row in ask_rows
                      if isinstance(row.get('data'), dict) and 'job_outcome' in row['data']]
    assert len(saved_outcomes) == 1
    assert ask_rows[-1]['error']['code'] == 'UNAVAILABLE'
    assert 'question paused: source_capture_unavailable' in ask_rows[-1]['error']['message']
    ask_job = saved_outcomes[0]['job_outcome']
    ask_question = saved_outcomes[0]['saved']['question']
    assert saved_outcomes[0]['saved']['answers'] == []
    question_id = ask_question['question_id']
    question_job_id = ask_job['job_id']
    assert ask_job['state'] == 'paused'
    assert ask_job['error'] == 'source_capture_unavailable'
    if ask_job.get('provider_attempts') is not None:
        assert ask_job['provider_attempts']['count'] == 0
    assert ask_question['source_mode'] == 'current'
    assert ask_question['target_set_id'] is None
    assert ask_question['targets'] == []
    expected_selection = {
        'source_intent': 'moving_target', 'version_id': v_sourced_id,
        'source_mode': 'current', 'provider_id': 'codex',
        'selections': json.loads(ask_req_path.read_text())['source_selection']['selections'],
    }
    saved_question_view = local('GET', f'/api/v1/questions/{question_id}', client=owner)
    assert saved_question_view['requested_selection'] == expected_selection
    assert ask_question['job_state'] == 'paused'
    assert ask_question['job_error'] == 'source_capture_unavailable'
    answers = local('GET', f'/api/v1/questions/{question_id}/answers', client=owner)
    assert answers['items'] == [] and answers['next_cursor'] is None
    assert any(
        lim.get('code') == 'unable_to_verify'
        and 'no partial source request was dispatched' in lim.get('message', '')
        for lim in ask_question.get('limitations', [])
    )
    job_view = local('GET', f'/api/v1/jobs/{question_job_id}', client=owner)
    job_attempts = job_view['provider_attempts']
    assert job_attempts['count'] == 0
    assert job_attempts['unknown'] == 0 and job_attempts['reconciliation_pending'] == 0
    proof['cases'].append({
        'kind': 'current-mode question on missing checkout settles paused source_capture_unavailable with zero attempts',
        'actual': {
            'question_id': question_id,
            'job_id': question_job_id,
            'job_state': ask_job['state'],
            'job_error': ask_job['error'],
            'attempt_count': job_attempts['count'],
            'target_set_id': ask_question['target_set_id'],
            'targets': ask_question['targets'],
            'requested_selection': saved_question_view['requested_selection'],
            'limitations': ask_question['limitations'],
            'answers_count': len(answers['items']),
            'provider_attempts': job_attempts,
        },
    })

    # Step 10: Provider status check with nonexistent --provider-binary.
    prov_status = product.cli(['providers', 'status'], client=owner)['data']
    assert prov_status['provider_id'] == 'codex'
    assert prov_status['state'] == 'provider_error'
    assert prov_status['reason'] == 'transport_unavailable'
    assert prov_status['authentication_mode'] == 'unknown'
    assert not nonexistent_provider.exists()
    proof['cases'].append({
        'kind': 'nonexistent provider executable reports provider_error transport_unavailable with zero native starts',
        'actual': prov_status,
    })

    fixture = [
        {
            'version': v_sourced_id,
            'document': doc_sourced_id,
            'project': project_id,
            'label': 'Sourced',
            'runToken': run_token,
            'hasMetadata': True,
        },
        {
            'version': v_plain_id,
            'document': doc_plain_id,
            'project': project_id,
            'label': 'Plain',
            'runToken': run_token,
            'hasMetadata': False,
        },
    ]

    # Step 11: Open browser and run local-deterministic or Coordinator-supervised live private-HTTPS verification.
    browser_attempted = True
    call(['open', product.endpoint + '/health/live', '--browser', 'chrome', '--profile', str(work / 'browser')])
    proof['browser_version'] = js('async page => page.context().browser().version()')
    script = JS_PATH.read_text()

    def run_browser_suite(target_base, target_renderer1, target_renderer2, mode_label):
        assert not nonexistent_provider.exists()
        if live:
            call(['goto', target_base + '/connect'])
            pairing = js(
                '''async page => {
                await page.getByLabel('장치 이름', {exact: true}).fill('Task05 private web browser');
                if (!await page.getByLabel('이 장치 기억', {exact: true}).isChecked()) {
                    throw Error('remember default absent');
                }
                const response = page.waitForResponse(
                    r => r.url().endsWith('/api/v1/auth/pairings') && r.request().method() === 'POST'
                );
                await page.getByRole('button', {name: '연결 코드 요청', exact: true}).click();
                const p = (await (await response).json()).data;
                return {pairing_id: p.pairing_id, code: p.code, origin: p.origin, etag: p.etag};
            }'''
            )
            local(
                'POST',
                '/api/v1/auth/pairings/' + pairing['pairing_id'] + '/approve',
                client=owner,
                body={'code': pairing['code'], 'origin': pairing['origin']},
                headers={'If-Match': pairing['etag'], 'Idempotency-Key': h.operation()},
                expected=(200,),
            )
            js('async page => { await page.waitForFunction(() => localStorage.getItem("haeryong.owner-session.v1")); return true; }')
        else:
            js(
                'async page => { await page.goto('
                + json.dumps(target_base)
                + '); await page.evaluate(t => localStorage.setItem("haeryong.owner-session.v1", t), '
                + json.dumps(owner['token'])
                + '); return true; }'
            )

        suite_inputs = {
            'base': target_base,
            'expectedOrigins': [target_renderer1, target_renderer2],
            'modeLabel': mode_label,
            'fixture': fixture,
            'projectId': project_id,
            'gitSourceId': git_source_id,
            'approvedRoots': {git_source_id: str(git_root), snap_source_id: str(snap_root)},
            'gitRepoId': git_repo_id,
            'exactRevision': exact_revision,
            'gitOnlySha256': git_only_sha256,
            'snapSourceId': snap_source_id,
            'retainedReadId': retained_read_id,
            'retainedSha256': retained_sha256,
            'retainedText': retained_bytes.decode('utf-8'),
            'questionId': question_id,
            'questionJobId': question_job_id,
            'screenshot': str(screenshot_path) if screenshot_path else None,
        }
        result = js(script.replace('INPUT', json.dumps(suite_inputs)), timeout=240)
        proof['browser_suite'] = result
        assert result.get('complete') is True, f'Browser suite did not complete cleanly: {result}'
        assert not nonexistent_provider.exists()
        proof['cases'].append({
            'kind': f'TASK-007-05 browser verification ({mode_label})',
            'actual': result,
        })
        return result

    if live:
        doctor = product.cli(['doctor'], client=owner)['data']['endpoint_checks']
        assert doctor['remote_enabled'] and all(m['supported'] for m in doctor['renderer_mappings'])
        assert doctor['runtime_local_endpoint'] == product.endpoint
        proof['cases'].append({'kind': 'authenticated endpoint doctor configured intent only', 'actual': doctor})

        publish(
            ready_file,
            {
                'session': session,
                'pid': product.process.pid,
                'management': product.endpoint,
                'external': external,
                'renderers': [
                    {'origin': renderer1, 'upstream': upstream1},
                    {'origin': renderer2, 'upstream': upstream2},
                ],
                'fixture': fixture,
                'runtime': str(work),
            },
        )
        wait_signal(serve_ready_file)
        denied = []
        for fields in (
            {'Origin': external},
            {'Origin': external, 'Tailscale-User-Login': 'owner@example.com'},
            {'Origin': 'https://foreign.invalid'},
        ):
            denied.append(request_url(external + '/api/v1/session', headers=fields)[0])
        assert denied == [401, 401, 403]
        proof['cases'].append({
            'kind': 'private reachability and forged network identity do not authenticate',
            'actual': denied,
        })
        run_browser_suite(external, renderer1, renderer2, 'private-https')
        publish(web_complete_file, {'session': session})
        wait_signal(serve_removed_file)
        try:
            unreachable = request_url(external + '/health/live')[0] != 200
        except (OSError, urllib.error.URLError):
            unreachable = True
        assert unreachable, 'Removed private proxy still reaches the application'
        proof['cases'].append({
            'kind': 'removed private proxy route unreachable while local loopback endpoint stays healthy',
            'actual': {
                'remote_unreachable_after_serve_off': unreachable,
                'local_health_status': request_url(product.endpoint + '/health/live')[0],
            },
        })
    else:
        run_browser_suite(product.endpoint, upstream1, upstream2, 'local-deterministic')

    assert product.cli(['doctor'], client=owner)['data']['endpoint_checks']['connected_endpoint'] == product.endpoint
    assert not nonexistent_provider.exists()
    assert fingerprints() == before, 'Verification inputs changed during browser run'
    proof['inputs'] = before
    proof['complete'] = True
except BaseException as error:
    proof['error'] = redact(type(error).__name__ + ': ' + str(error))[:2000]
    raise
finally:
    if browser_attempted:
        try:
            result = subprocess.run(
                [cli, '--session=' + session, 'close'],
                cwd=run_cwd,
                env=env,
                capture_output=True,
                text=True,
                timeout=45,
            )
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
        os.chdir(scratch_root)
        shutil.rmtree(work)
assert proof['complete'], 'Owned verification cleanup failed; see private receipt'
print(json.dumps({'complete': proof['complete'], 'cases': len(proof['cases']), 'receipt': str(receipt)}))
