"""Exact managed capture and source-only transport through public production APIs.

No provider model turn, arbitrary filesystem API or OS confinement is asserted.
"""
import json
import shutil
import uuid

import pytest

from .harness import digest

pytestmark = pytest.mark.e2e


def test_managed_sources_SCAP19_remembered_capture_cli_exact_offline_scope_and_partial_history(product, owner, tmp_path):
    project = product.data('GET', '/api/v1/capabilities', client=owner)['unfiled_project_id']
    source_id = str(uuid.uuid4())
    root = tmp_path / 'selected'
    root.mkdir()
    body = b'\x00\xffordinary hidden source\r\n' * 70000
    (root / '.unknown').write_bytes(body)
    product.cli(['sources', 'approve', '--project', project, '--source-id', source_id, '--kind', 'files', '--root-path', str(root), '--retention-mode', 'selected_snapshot', '--confirmed'], client=owner)
    request = dict(source_mode='current', selections=[dict(source_id=source_id, paths=[dict(path='.unknown')])])
    input_file = tmp_path / 'capture.json'
    input_file.write_text(json.dumps(request))
    operation = str(uuid.uuid4())
    args = ['sources', 'capture', '--project', project, '--input', str(input_file), '--operation-id', operation]
    captured = product.cli(args, client=owner)['data']['result']
    assert captured['complete'] and captured['target_set_id']
    assert product.cli(args, client=owner)['data']['result'] == captured
    view = product.cli(['sources', 'captured', '--capture', captured['capture_id']], client=owner)['data']
    assert view['next_cursor'] is None and len(view['items']) == 1
    read = view['items'][0]
    assert read['bytes_sha256'] == digest(body)
    assert read['availability'] == 'available' and read['read_state'] == 'not_read_by_view'
    denied = dict(request, provider_id='codex')
    status, _, _ = product.request('POST', '/api/v1/projects/'+project+'/source-captures', client=owner, body=denied, headers={'Idempotency-Key': str(uuid.uuid4())})
    assert status == 403, 'empty provider disclosure must deny even the remembered default'
    product.stop(); product.start()
    shutil.rmtree(root)
    destination = tmp_path / 'delivered'
    delivered = product.cli(['sources', 'read', '--read', read['read_id'], '--output', str(destination)], client=owner)['data']
    assert destination.read_bytes() == body
    assert delivered['bytes_sha256'] == digest(body) and delivered['input_observation'] is False
    product.cli(['sources', 'read', '--read', read['read_id'], '--output', str(destination)], client=owner, code=5)
    assert destination.read_bytes() == body, 'an existing output is never overwritten'
    partial = dict(source_mode='current', selections=[dict(source_id=source_id, paths=[dict(path='.unknown'), dict(path='missing')])])
    input_file.write_text(json.dumps(partial))
    failed_capture = product.cli(['sources', 'capture', '--project', project, '--input', str(input_file)], client=owner)['data']['result']
    assert failed_capture['complete'] is False and failed_capture['target_set_id'] is None
    failed_view = product.cli(['sources', 'captured', '--capture', failed_capture['capture_id']], client=owner)['data']
    assert len(failed_view['items']) == 2
    assert {row['location']['path'] for row in failed_view['items']} == {'.unknown', 'missing'}
    assert {row['reason'] for row in failed_view['items']} == {'missing_source'}
    diff = product.cli(['sources', 'diff', '--capture', captured['capture_id'], '--other', captured['capture_id']], client=owner)['data']
    assert diff['changes'] == [] and diff['semantic_verdict'] is None
    # A normal configuration revocation fences existing retained reads too.
    current = product.data('GET', '/api/v1/configuration', client=owner)
    planned = product.data('POST', '/api/v1/configuration/plans', client=owner, body=dict(scope=dict(kind='project', project_id=project), expected_config_revision=current['config_revision'], changes=dict(sources=[])), headers={'Idempotency-Key': str(uuid.uuid4())})
    product.data('POST', '/api/v1/configuration/plans/'+planned['plan_id']+'/commit', client=owner, body=dict(expected_config_revision=planned['base_config_revision'], confirmed=True), headers={'Idempotency-Key': planned['plan_id']})
    status, _, _ = product.request('GET', '/api/v1/source-reads/'+read['read_id'], client=owner, raw=True)
    assert status == 404
