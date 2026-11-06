"""Host-local production backup, protected restore and exact portable export.

Synthetic disposable owner libraries; no provider or source-repository mutation.
"""
import json
import os
import subprocess
import uuid
import zipfile

import pytest

from .test_annotations import create

pytestmark = pytest.mark.e2e


def copy_cli(product, owner, args, code=0):
    result = subprocess.run([product.binary, *args, '--data-dir', str(product.data_dir), *owner['flags']], text=True, capture_output=True, timeout=30)
    wire = json.loads(result.stdout) if result.stdout.strip() else {}
    assert result.returncode == code, dict(command=args[:2], expected=code, actual=result.returncode, error=wire.get('error'))
    return wire


def test_local_backup_status_replay_corruption_and_protected_restore(product, owner, tmp_path):
    tree = tmp_path / 'original'
    tree.mkdir()
    (tree / 'first.html').write_text('<h1>Original snapshot</h1>')
    (tree / 'unknown.bin').write_bytes(b'\x00\xffbackup bytes')
    (tree / 'empty').mkdir()
    (tree / 'alias').symlink_to('first.html')
    _, _, imported = product.import_tree(owner, tree)
    version = imported['version_id']
    product.cli(['review', 'set', '--version', version, '--action', 'complete', '--if-none-match', '*'], client=owner)
    note = create(product, owner, version, 'first.html', None, text='saved original note')
    path = '/api/v1/annotations/' + note['annotation_id']
    moved = product.data('PATCH', path, client=owner, headers={'Idempotency-Key':str(uuid.uuid4()),'If-Match':note['etag']}, body=dict(action='reattach',anchor=dict(version_id=version,page_path='first.html',region_id=None,page_query='original=1',page_fragment='exact'),selected_text=None,context=None,context_ref=None))
    product.data('DELETE', path, client=owner, headers={'Idempotency-Key':str(uuid.uuid4()),'If-Match':moved['etag']})
    note_history = product.data('GET', path + '/history', client=owner)
    review_history = product.data('GET', '/api/v1/versions/' + version + '/review/history', client=owner)
    product.cli(['revisit','set','--document',imported['document_id'],'--flag','true','--if-none-match','*'],client=owner)
    ext_host = 'node.private.ts.net:9443'
    ext_origin = 'https://' + ext_host
    render_origin = 'https://node.private.ts.net:9444'
    pool_base = 9281 if not (9281 <= product.address <= 9284) else 9285
    upstream_port = 9291 if product.address != 9291 else 9292
    mapping = dict(version_id=version, origin=render_origin, upstream=f'http://127.0.0.1:{upstream_port}', reassignable=False)
    doctor_mapping = dict(version_id=version, origin=render_origin, upstream=f'http://127.0.0.1:{upstream_port}')
    private_policy = tmp_path / 'private-network.json'
    private_policy.write_text(json.dumps(dict(
        network=dict(
            listener=dict(bind='127.0.0.1', port=product.address),
            remote_enabled=True,
            external_base_url=ext_origin,
            allowed_origins=[product.endpoint, ext_origin],
            renderer_pool=dict(base_port=pool_base, count=4),
            renderer_mappings=[mapping],
        ),
        retention=dict(auto_purge_enabled=True),
    )))
    cfg_plan = product.cli(['config', 'plan', str(private_policy)], client=owner)['data']['result']
    product.cli(['config', 'apply', cfg_plan['plan_id'], '--expected-config-revision', str(cfg_plan['base_config_revision']), '--confirmed'], client=owner)
    product.stop(kill=True)
    product.start(configured=True)
    pre_doctor = product.cli(['doctor'], client=owner)['data']['endpoint_checks']
    assert pre_doctor['configured_local_endpoint'] == product.endpoint
    assert pre_doctor['runtime_local_endpoint'] == product.endpoint
    assert pre_doctor['ephemeral_override'] is False
    assert pre_doctor['remote_enabled'] is True
    assert pre_doctor['external_base_url'] == ext_origin
    assert pre_doctor['renderer_mappings'] == [{**doctor_mapping, 'configured': True, 'supported': True, 'reason': None}]
    remembered = product.owner('remembered device')
    assert product.data('GET', '/api/v1/session', client=remembered)['session_id'] == remembered['session']['session_id']
    before = product.data('GET', '/api/v1/capabilities', client=owner)
    operation = str(uuid.uuid4())
    destination = tmp_path / 'backups'
    args = ['backup', 'create', '--destination', str(destination), '--operation-id', operation]
    accepted = copy_cli(product, owner, args)['data']['result']
    product.job(owner, accepted['job_id'], timeout=90)
    assert copy_cli(product, owner, args)['data']['result'] == accepted
    status = product.data('GET', '/api/v1/status', client=owner)['backup']
    assert status['mode'] == 'manual' and status['last_complete_verified']['set_id'] == accepted['set_id']
    assert status['last_trial_restore'] == dict(state='not_run', completed_at=None)
    backup = destination / accepted['set_id']
    verified = product.cli(['backup', 'verify', '--input', str(backup)])['data']
    assert verified['valid']
    one_object = next((backup / 'objects').iterdir())
    original = one_object.read_bytes()
    one_object.write_bytes(b'corrupted backup object')
    product.cli(['backup', 'verify', '--input', str(backup)], code=2)
    one_object.write_bytes(original)
    copy_cli(product, owner, ['backup', 'create', '--destination', str(tmp_path / 'ignored'), '--encryption', 'age'], code=2)
    product.cli(['auth', 'sessions', 'revoke', remembered['session']['session_id']], client=owner)
    assert product.request('GET', '/api/v1/session', client=remembered)[0] == 401
    current_policy = tmp_path / 'current-before-restore.json'
    current_policy.write_text(json.dumps(dict(security=dict(pairing_expiry_seconds=180))))
    cur_plan = product.cli(['config', 'plan', str(current_policy)], client=owner)['data']['result']
    product.cli(['config', 'apply', cur_plan['plan_id'], '--expected-config-revision', str(cur_plan['base_config_revision']), '--confirmed'], client=owner)

    product.stop()
    plan = product.cli(['restore', 'plan', '--input', str(backup)])['data']
    result = product.cli(['restore', 'apply', plan['plan_id'], '--confirm'])['data']
    assert product.cli(['restore', 'apply', plan['plan_id'], '--confirm'])['data'] == result
    assert result['library_id'] == before['library_id']
    product.start(configured=True)
    assert product.request('GET', '/api/v1/capabilities', client=owner)[0] == 401
    assert product.request('GET', '/api/v1/capabilities', client=remembered)[0] == 401
    fresh = product.owner('fresh restored owner')
    assert product.data('GET', path + '/history', client=fresh) == note_history
    assert product.data('GET', '/api/v1/versions/' + version + '/review/history', client=fresh) == review_history
    assert product.cli(['revisit','show','--document',imported['document_id']],client=fresh)['data']['value']['flag'] is True
    after = product.data('GET', '/api/v1/capabilities', client=fresh)
    assert after['runtime_generation'] != before['runtime_generation']
    assert after['stream_epoch'] != before['stream_epoch']
    assert product.data('GET', '/api/v1/versions/' + version + '/review', client=fresh)['value']['state'] == 'reviewed'
    restored = product.data('GET', '/api/v1/annotations/' + note['annotation_id'], client=fresh)
    assert restored['deleted_at'] and restored['body'] is None
    assert product.request('GET', '/api/v1/versions/' + version + '/file?path=unknown.bin', client=fresh, raw=True)[1] == b'\x00\xffbackup bytes'
    product.stop(); product.start(configured=True)
    assert product.data('GET', '/api/v1/capabilities', client=fresh)['runtime_generation'] == after['runtime_generation']
    configuration = product.data('GET', '/api/v1/configuration', client=fresh)
    net_cfg = configuration['configuration']['network']
    assert net_cfg['remote_enabled'] is False
    assert net_cfg['external_base_url'] is None
    assert net_cfg['listener'] == dict(bind='127.0.0.1', port=product.address)
    assert net_cfg['allowed_origins'] == [product.endpoint]
    assert net_cfg['renderer_mappings'] == [mapping]
    assert configuration['configuration']['retention']['auto_purge_enabled'] is False
    post_doctor = product.cli(['doctor'], client=fresh)['data']['endpoint_checks']
    assert post_doctor['configured_local_endpoint'] == product.endpoint
    assert post_doctor['runtime_local_endpoint'] == product.endpoint
    assert post_doctor['ephemeral_override'] is False
    assert post_doctor['remote_enabled'] is False
    assert post_doctor['external_base_url'] is None
    assert post_doctor['renderer_mappings'] == [{**doctor_mapping, 'configured': True, 'supported': False, 'reason': 'renderer_remote_ingress'}]
    assert product.request('GET', '/api/v1/session', client=fresh, headers={'Host': ext_host, 'Origin': ext_origin})[0] == 403
    assert product.request('POST', f'/api/v1/versions/{version}/render-sessions', client=fresh, headers={'Host': ext_host, 'Origin': ext_origin, 'Idempotency-Key': str(uuid.uuid4())}, body={})[0] == 403
    assert render_origin not in product.request('GET', '/', raw=True)[2]['Content-Security-Policy']
    choices = tmp_path / 'review.json'
    choices.write_text(json.dumps(dict(configuration=configuration['configuration'], expected_config_revision=configuration['config_revision'], resume_cleanup_job_ids=[])))
    product.stop()
    preview = copy_cli(product, fresh, ['restore', 'review'])['data']
    assert preview['state'] == 'pending_review'
    assert preview['original_configuration']['network']['remote_enabled'] is True
    assert preview['original_configuration']['network']['external_base_url'] == ext_origin
    assert preview['original_configuration']['network']['allowed_origins'] == [product.endpoint, ext_origin]
    assert preview['original_configuration']['network']['renderer_mappings'] == [mapping]
    assert preview['current_configuration']['security']['pairing_expiry_seconds'] == 180
    assert copy_cli(product, fresh, ['restore', 'review', '--choices', str(choices), '--confirm'])['data']['state'] == 'reviewed'
    product.start(configured=True)
    assert product.data('GET', '/api/v1/capabilities', client=fresh)['runtime_generation'] == after['runtime_generation']


def test_export_same_path_two_versions_directory_zip_and_plain_tree(product, owner, tmp_path):
    tree = tmp_path / 'multi'
    tree.mkdir()
    (tree / 'same.html').write_bytes(b'<h1>version one</h1>')
    (tree / 'manifest.json').write_bytes(b'ordinary author payload')
    (tree / 'empty').mkdir()
    (tree / 'alias').symlink_to('same.html')
    _, _, first = product.import_tree(owner, tree)
    create(product, owner, first['version_id'], 'same.html', None, text='first exact anchor')
    (tree / 'same.html').write_bytes(b'<h1>version two</h1>')
    _, _, second = product.import_tree(owner, tree, extra=('--document', first['document_id'], '--base-version', first['version_id']))
    for format in ('directory', 'zip'):
        destination = tmp_path / ('export-' + format)
        plan = copy_cli(product, owner, ['export', 'plan', '--document', first['document_id'], '--versions', 'all', '--records', 'notes,review,references', '--format', format, '--destination', str(destination)])['data']['result']
        accepted = copy_cli(product, owner, ['export', 'create', plan['plan_id']])['data']['result']
        product.job(owner, accepted['job_id'], timeout=90)
        assert product.cli(['export', 'verify', '--input', str(destination)])['data']['valid']
        if format == 'zip':
            with zipfile.ZipFile(destination) as archive:
                index = json.loads(archive.read('export-index.json'))
                read = archive.read
                assert read('versions/' + first['version_id'] + '/same.html') == b'<h1>version one</h1>'
                assert read('versions/' + second['version_id'] + '/same.html') == b'<h1>version two</h1>'
                assert 'versions/' + first['version_id'] + '/empty/' in archive.namelist()
                assert read('versions/' + first['version_id'] + '/alias') == b'same.html'
        else:
            index = json.loads((destination / 'export-index.json').read_text())
            assert (destination / 'versions' / first['version_id'] / 'same.html').read_bytes() == b'<h1>version one</h1>'
            assert (destination / 'versions' / second['version_id'] / 'same.html').read_bytes() == b'<h1>version two</h1>'
            assert os.readlink(destination / 'versions' / first['version_id'] / 'alias') == 'same.html'
            assert (destination / 'versions' / first['version_id'] / 'empty').is_dir()
        assert {v['version_id'] for v in index['versions']} == {first['version_id'], second['version_id']}
        assert all(v['payload_root'] == 'versions/' + v['version_id'] for v in index['versions'])

    destination = tmp_path / 'plain'
    metadata = tmp_path / 'separate-index'
    accepted = copy_cli(product, owner, ['export', 'create', '--version', first['version_id'], '--format', 'directory', '--destination', str(destination), '--metadata-destination', str(metadata)])['data']['result']
    product.job(owner, accepted['job_id'], timeout=90)
    assert (destination / 'same.html').read_bytes() == b'<h1>version one</h1>'
    assert (destination / 'manifest.json').read_bytes() == b'ordinary author payload'
    assert not (destination / 'export-index.json').exists()
    assert product.cli(['export', 'verify', '--input', str(destination), '--index', str(metadata / 'export-index.json')])['data']['valid']
    (destination / 'same.html').write_bytes(b'changed')
    product.cli(['export', 'verify', '--input', str(destination), '--index', str(metadata / 'export-index.json')], code=2)


def test_canonical_f12_export_streams_complete_inventory_without_old_limits(product, owner, canonical_f12, tmp_path):
    import hashlib
    accepted = product.cli(['import', canonical_f12['archive']], client=owner, timeout=1800)['data']
    published = product.job(owner, accepted['result']['job_id'], timeout=1800)
    import time
    backup_args = ['backup','create','--destination',str(tmp_path / 'f12-backups'),'--operation-id',str(uuid.uuid4())]
    backup_acceptance = copy_cli(product,owner,backup_args)['data']['result']
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        job = product.data('GET','/api/v1/jobs/'+backup_acceptance['job_id'],client=owner)
        if job['state'] == 'running':break
        time.sleep(0.01)
    assert job['state'] == 'running'
    product.stop(kill=True);product.start()
    assert copy_cli(product,owner,backup_args)['data']['result'] == backup_acceptance
    product.job(owner,backup_acceptance['job_id'],timeout=1800)
    complete=tmp_path / 'f12-backups' / backup_acceptance['set_id']
    assert product.cli(['backup','verify','--input',str(complete)],timeout=1800)['data']['valid']
    output = tmp_path / 'f12-export.zip'
    result = copy_cli(product, owner, ['export', 'create', '--version', published['version_id'], '--format', 'zip', '--destination', str(output)])['data']['result']
    product.job(owner, result['job_id'], timeout=1800)
    with zipfile.ZipFile(output) as archive:
        actual = {info.filename: info for info in archive.infolist() if not info.is_dir() and not (info.external_attr >> 16) & 0o170000 == 0o120000}
        assert set(actual) == set(canonical_f12['files'])
        for name, expected in canonical_f12['files'].items():
            assert actual[name].file_size == expected['byte_size']
            with archive.open(name) as stream:
                assert hashlib.file_digest(stream, 'sha256').hexdigest() == expected['sha256']
        assert archive.getinfo('represented-empty/').is_dir()



def test_local_lost_acceptance_reuses_exact_receipt_and_remote_path_is_refused(product, owner, tmp_path):
    import hashlib
    import hmac
    import socket
    profile = json.loads((product.data_dir / 'profile.json').read_text())
    identity = product.identities[-1]
    runtime = product.data('GET', '/api/v1/capabilities', client=owner)['runtime_generation']
    operation = str(uuid.uuid4())
    destination = str(tmp_path / 'lost-response-backup')
    body = json.dumps(dict(action='backup.create', runtime_generation=runtime, destination=destination), separators=(',', ':')).encode()
    nonce = os.urandom(32).hex()
    domain = ('POST\n/local/v1/copy-requests\n' + identity['instance_id'] + '\n' + profile['profile_id'] + '\n' + profile['library_id'] + '\n' + operation + '\n' + hashlib.sha256(owner['token'].encode()).hexdigest() + '\n' + hashlib.sha256(body).hexdigest()).encode()
    proof = hmac.new(bytes.fromhex(profile['control_key']), b'copy-request-v1\x00' + nonce.encode() + b'\x00' + domain, hashlib.sha256).hexdigest()
    headers = ('POST /local/v1/copy-requests HTTP/1.1\r\nHost: 127.0.0.1:' + str(product.address) + '\r\nAuthorization: Bearer ' + owner['token'] + '\r\nIdempotency-Key: ' + operation + '\r\nX-Haeryong-Nonce: ' + nonce + '\r\nX-Haeryong-Proof: ' + proof + '\r\nContent-Length: ' + str(len(body)) + '\r\nConnection: close\r\n\r\n').encode()
    with socket.create_connection(('127.0.0.1', product.address), timeout=30) as connection:
        connection.sendall(headers + body)
        # Observe only HTTP acceptance headers, deliberately lose the result body.
        with connection.makefile('rb') as stream:
            assert stream.readline().split()[1] == b'202'
            while stream.readline().strip():
                pass
    receipt = product.data('GET', '/api/v1/operations/' + operation, client=owner)
    args = ['backup', 'create', '--destination', destination, '--operation-id', operation]
    acceptance = copy_cli(product, owner, args)['data']['result']
    assert acceptance['job_id'] == receipt['result_metadata']['job_id']
    copy_cli(product, owner, ['backup', 'create', '--destination', str(tmp_path / 'changed'), '--operation-id', operation], code=4)
    jobs = product.data('GET', '/api/v1/jobs', client=owner)
    assert len([job for job in jobs['items'] if job['kind'] == 'backup']) == 1
    copy_cli(product, owner, ['backup', 'create', '--destination', str(tmp_path / 'remote'), '--endpoint', 'http://127.0.0.1:1'], code=2)
    assert not (tmp_path / 'remote').exists()


def test_export_plan_show_freezes_all_and_latest_before_later_publication(product, owner, tmp_path):
    tree = tmp_path / 'frozen'
    tree.mkdir()
    (tree / 'page.html').write_bytes(b'first')
    _, _, first = product.import_tree(owner, tree)
    (tree / 'page.html').write_bytes(b'second')
    _, _, second = product.import_tree(owner, tree, extra=('--document', first['document_id'], '--base-version', first['version_id']))
    output = tmp_path / 'all.zip'
    planned = copy_cli(product, owner, ['export','plan','--document',first['document_id'],'--versions','all','--format','zip','--destination',str(output)])['data']['result']
    shown = copy_cli(product, owner, ['export','show',planned['plan_id']])['data']
    assert {row['version_id'] for row in shown['items']} == {first['version_id'],second['version_id']}
    assert shown['next_cursor'] is None and all('payload_root' not in row for row in shown['items'])
    (tree / 'page.html').write_bytes(b'third')
    _, _, third = product.import_tree(owner, tree, extra=('--document', first['document_id'], '--base-version', second['version_id']))
    accepted = copy_cli(product, owner, ['export','create',planned['plan_id']])['data']['result']
    product.job(owner,accepted['job_id'],timeout=90)
    assert product.cli(['export','verify','--input',str(output)])['data']['versions'] == 2
    with zipfile.ZipFile(output) as archive:
        index = json.loads(archive.read('export-index.json'))
        assert {row['version_id'] for row in index['versions']} == {first['version_id'],second['version_id']}
    destination, metadata = tmp_path / 'latest', tmp_path / 'latest-index'
    plan = copy_cli(product, owner, ['export','plan','--document',first['document_id'],'--format','directory','--destination',str(destination),'--metadata-destination',str(metadata)])['data']['result']
    (tree / 'page.html').write_bytes(b'fourth')
    product.import_tree(owner, tree, extra=('--document', first['document_id'], '--base-version', third['version_id']))
    accepted = copy_cli(product, owner, ['export','create',plan['plan_id']])['data']['result']
    product.job(owner,accepted['job_id'],timeout=90)
    assert (destination / 'page.html').read_bytes() == b'third'
    assert product.cli(['export','verify','--input',str(destination),'--index',str(metadata / 'export-index.json')])['data']['versions'] == 1
