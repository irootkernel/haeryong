"""Task6 production composition. API-created records are supporting evidence,
not substitutes for each actual browser/native campaign's host actions.
"""
from concurrent.futures import ThreadPoolExecutor
import http.client
import json
import threading
import time
from urllib.parse import urlsplit

import pytest

from .test_annotations import create, content
from .test_lifecycle import transition, commit
from .test_purge import purge_plan
from .harness import Proxy
from tests.manual import owner_records_support as support

pytestmark = pytest.mark.e2e


def populated(product, owner):
    fixture = support.prepare(product, owner)
    boundary = fixture['boundary']['version_id']
    anchors = [(boundary, 'a.html', 'retry'), (boundary, 'b.html', 'retry'),
               (boundary, 'no-region.html', None), (boundary, None, None)]
    anchors += [(v['version_id'], page, region) for v, page, region in [
        (fixture['originals']['mulgae'], 'recovery/compose.html', 'M11'),
        (fixture['originals']['gaori'], 'gaori/lifecycle.html', 'G12')]]
    for index, (version, page, region) in enumerate(anchors):
        create(product, owner, version, page, region, text='Task6 exact original note ' + str(index), context='Task6 retained context ' + str(index))
    for sequence,version in enumerate(fixture['expected_trees'],1):
        product.cli(['review', 'set', '--version', version, '--action', 'complete', '--if-none-match', '*'], client=owner)
        fixture['expected_review_sequences'][version]=sequence
    return fixture


def test_owner_records_two_projects_successors_restart_recovery_and_portable_metadata(product, owner, request):
    product.diagnostic_stderr = (product.root / 'owned-daemon.stderr').open('w')
    request.addfinalizer(product.diagnostic_stderr.close)
    fixture = populated(product, owner)
    expected = support.records(product, owner, support.recovery_versions(fixture))
    support.successors(product, owner, fixture)
    expected = support.records(product, owner, support.recovery_versions(fixture), known=expected)
    product.stop(); product.start()
    assert support.records(product, owner, support.recovery_versions(fixture)) == expected
    path, _ = support.backup(product, owner, product.root / 'owner-backups')
    fresh, authority = support.recover(product, owner, path)
    assert authority['before']['library_id'] == authority['after']['library_id']
    assert support.records(product, fresh, support.recovery_versions(fixture), known=expected) == expected
    support.assert_originals(product, fresh, fixture)
    support.assert_recovery_payloads(product, fresh, fixture)
    observations = support.export_roundtrip(product, fresh, fixture)
    assert len(observations) == 6
    for row in fixture['successors'].values():
        assert product.data('GET', '/api/v1/versions/' + row['version_id'] + '/review', client=fresh)['value']['state'] == 'unreviewed'


@pytest.mark.parametrize('loss', ['before', 'after'])
def test_owner_records_note_loss_replay_and_stale_conflict(product, owner, tmp_path, loss):
    root = tmp_path / 'note'; root.mkdir(); (root / 'a.html').write_text('<p id="retry">Exact note</p>')
    _, _, identity = product.import_tree(owner, root)
    note = create(product, owner, identity['version_id'], 'a.html', text='original')
    path = '/api/v1/annotations/' + note['annotation_id']
    body = dict(action='edit', **content('committed exact edit'))
    headers = {'Idempotency-Key': support.key(), 'If-Match': note['etag']}
    proxy_seen = []
    def before(method, route, fields, raw):
        if method == 'PATCH' and route == path:
            proxy_seen.append((fields['Idempotency-Key'], fields['If-Match'], raw))
    with Proxy(product, drop=lambda m, p, s: loss == 'after' and m == 'PATCH' and p == path and s == 200, before=before) as proxy:
        def request():
            connection = http.client.HTTPConnection(urlsplit(proxy.endpoint).netloc, timeout=30)
            try:
                connection.request('PATCH', path, json.dumps(body), headers | {'Origin': product.endpoint, 'Authorization': 'Bearer ' + owner['token'], 'Content-Type': 'application/json'})
                response = connection.getresponse()
                raw = response.read()
                return response.status, json.loads(raw)['data'] if raw else None
            finally:
                connection.close()
        if loss == 'before':
            proxy.blocked_path = ('PATCH', path)
            assert request()[0] == 503
            assert len(support.all_items(product, owner, path + '/history')) == 1
            proxy.release()
        else:
            with pytest.raises((OSError, http.client.HTTPException)):
                request()
            assert proxy.dropped
        status, saved = request()
        assert status == 200
        assert saved['body'] == body['body']
        assert request() == (200, saved)
    assert len(set(proxy_seen)) == 1
    assert len(support.all_items(product, owner, path + '/history')) == 2
    stale, error, _ = product.request('PATCH', path, client=owner, headers=headers | {'Idempotency-Key': support.key()}, body=dict(action='edit', **content('must not overwrite')))
    assert stale == 412 and error['error']['details']['etag'] == saved['etag']
    assert product.data('GET', path, client=owner)['body'] == body['body']


@pytest.mark.parametrize('winner', ['restore', 'purge', 'competing'])
def test_owner_records_lifecycle_winner_data_and_immutable_survivor(product, owner, tmp_path, winner):
    root = tmp_path / 'lifecycle'; root.mkdir()
    (root / 'a.html').write_bytes(b'<p>Task6 unique purge canary</p>')
    (root / 'shared.bin').write_bytes(b'unchanged shared bytes')
    _, _, target = product.import_tree(owner, root, extra=('--new-document',))
    source = tmp_path / 'survivor'; source.mkdir(); (source / 'a.html').write_bytes(b'<p>Surviving immutable HTML</p>'); (source / 'shared.bin').write_bytes(b'unchanged shared bytes')
    _, _, survivor = product.import_tree(owner, source, extra=('--new-document',))
    assert survivor['document_id'] != target['document_id']
    expected_source, expected_other = support.tree(root), support.tree(source)
    transition(product, owner, target['document_id'], 'trash')
    purge = purge_plan(product, owner, target['document_id'])
    restore = product.data('POST', '/api/v1/documents/' + target['document_id'] + '/lifecycle-plans', client=owner, body=dict(operation='restore', expected_revision=purge['expected_revision']))
    def send(plan):
        return product.request('POST', '/api/v1/lifecycle-plans/' + plan['plan_id'] + '/commit', client=owner, body=dict(plan_id=plan['plan_id'], expected_revision=plan['expected_revision'], confirmed=True))
    if winner == 'competing':
        barrier = threading.Barrier(2)
        def competing(plan):
            barrier.wait(timeout=30)
            return send(plan)
        with ThreadPoolExecutor(max_workers=2) as pool:
            futures = [pool.submit(competing, plan) for plan in [restore, purge]]
            try:
                results = [future.result() for future in futures]
            finally:
                barrier.abort()
    else:
        first, second = (restore, purge) if winner == 'restore' else (purge, restore)
        results = [send(first), send(second)]
    statuses = sorted(row[0] for row in results)
    assert statuses in ([200, 412], [202, 412])
    if 200 in statuses:
        assert product.data('GET', '/api/v1/documents/' + target['document_id'], client=owner)['lifecycle'] == 'active'
        assert support.published_tree(product, owner, target['version_id']) == expected_source
    else:
        assert product.request('GET', '/api/v1/versions/' + target['version_id'], client=owner)[0] == 404
        accepted = next(wire['data'] for status, wire, _ in results if status == 202)
        assert send(purge)[1]['data'] == accepted
    assert support.published_tree(product, owner, survivor['version_id']) == expected_other
    assert support.tree(root) == expected_source and support.tree(source) == expected_other


def test_owner_records_actual_interrupted_backup_retains_same_acceptance_and_records(product, owner, canonical_f12):
    fixture = populated(product, owner)
    expected = support.records(product, owner, support.recovery_versions(fixture))
    imported = product.cli(['import', canonical_f12['archive']], client=owner, timeout=1800)['data']
    product.job(owner, imported['result']['job_id'], timeout=1800)
    destination = product.root / 'interrupted-backup'
    args = ['backup', 'create', '--destination', str(destination), '--operation-id', support.key()]
    accepted = support.copy_cli(product, owner, args)['result']
    incomplete = destination / ('.' + accepted['set_id'] + '.incomplete')
    deadline = time.monotonic() + 60
    while True:
        observed = product.data('GET', '/api/v1/jobs/' + accepted['job_id'], client=owner)
        assert observed['state'] != 'completed', 'completed before interruption; no interruption proof'
        if observed['state'] == 'running' and incomplete.is_dir():
            break
        assert time.monotonic() < deadline
        time.sleep(.01)
    process = product.process
    product.stop(kill=True)
    assert process.returncode != 0
    assert incomplete.is_dir() and not (destination / accepted['set_id']).exists(), 'copy completed before kill; no interruption proof'
    product.cli(['backup', 'verify', '--input', str(incomplete)], code=2)
    product.start()
    assert support.copy_cli(product, owner, args)['result'] == accepted, 'create replay must retain original job'
    current=product.data('GET','/api/v1/jobs/'+accepted['job_id'],client=owner)
    deadline=time.monotonic()+60
    while current['state']=='interrupted':
        assert time.monotonic()<deadline
        time.sleep(.1);current=product.data('GET','/api/v1/jobs/'+accepted['job_id'],client=owner)
    execution=accepted
    if current['state'] in ('failed','cancelled'):
        execution=support.copy_cli(product,owner,['backup','resume',accepted['set_id']])['result']
        assert execution['set_id']==accepted['set_id'] and execution['job_id']!=accepted['job_id']
        assert product.data('GET','/api/v1/jobs/'+accepted['job_id'],client=owner)['state']==current['state']
    product.job(owner, execution['job_id'], timeout=1800)
    complete = destination / accepted['set_id']
    assert product.cli(['backup', 'verify', '--input', str(complete)], timeout=1800)['data']['valid']
    fresh, _ = support.recover(product, owner, complete)
    assert support.records(product, fresh, fixture['expected_trees']) == expected
