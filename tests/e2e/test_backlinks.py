"""BL public HTTP/CLI counts, honest validation and same-revision owner recovery."""
import json
import time
import urllib.parse
import uuid
import pytest
from .test_references import reference_ready

pytestmark = pytest.mark.e2e


def wait_created_jobs(product, owner, ids):
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        rows = [product.data('GET', '/api/v1/jobs/' + identity, client=owner) for identity in ids]
        assert not any(row['state'] in ('failed', 'cancelled') for row in rows)
        if all(row['state'] == 'completed' for row in rows):
            return rows
        time.sleep(.05)
    pytest.fail('created index jobs did not complete')


def test_backlinks_BL_public_counts_HEAD_CLI_and_owner_rebuild(product, owner, tmp_path):
    target = tmp_path / 'target'; target.mkdir()
    (target / 'target.html').write_text("<p id='retry'>Immutable target</p>")
    _, _, t = product.import_tree(owner, target)
    reference_ready(product, owner, t['version_id'])
    source = tmp_path / 'source'; source.mkdir()
    original = b"<p id='origin'>Original source</p>"
    (source / 'source.html').write_bytes(original)
    metadata = tmp_path / 'references.json'
    metadata.write_text(json.dumps({'references': [dict(id=identity, document_id=t['document_id'],
        version_id=t['version_id'], page_path='target.html', region_id='retry',
        **{'from': dict(page_path='source.html', region_id='origin')}, label='Authored pin') for identity in ['a', 'b']]}))
    _, _, s = product.import_tree(owner, source, extra=('--metadata', str(metadata), '--new-document'))
    reference_ready(product, owner, s['version_id'])
    route = '/api/v1/versions/' + t['version_id'] + '/backlinks'
    params = urllib.parse.urlencode(dict(page_path='target.html', region_id='retry', limit='1'))
    first = product.data('GET', route + '?' + params, client=owner)
    assert first['counts'] == dict(edge_count='2', referring_document_count='1')
    assert len(first['items']) == 1 and first['next_cursor']
    second = product.data('GET', route + '?' + params + '&cursor=' + urllib.parse.quote(first['next_cursor'], safe=''), client=owner)
    assert second['next_cursor'] is None
    assert first['items'][0]['edge_id'] != second['items'][0]['edge_id']
    outgoing = product.data('GET', '/api/v1/versions/' + s['version_id'] + '/references', client=owner)
    assert outgoing['counts'] == dict(edge_count='2', referenced_document_count='1')
    assert {r['edge_id'] for r in outgoing['items']} == {first['items'][0]['edge_id'], second['items'][0]['edge_id']}
    for suffix, expected in [('?' + params, 200), ('?region_id=retry', 400), ('?unknown=canary', 400)]:
        status, body, _ = product.request('HEAD', route + suffix, client=owner, raw=True)
        assert status == expected and body == b''
    cli = product.cli(['backlinks', 'list', '--version', t['version_id'], '--page', 'target.html', '--region', 'retry', '--limit', '1'], client=owner)['data']
    assert cli == first
    valid_route = '/api/v1/versions/' + t['version_id'] + '/indexes/validate?kind=references'
    assert product.data('GET', valid_route, client=owner)['state'] == 'consistent'
    assert product.data('GET', valid_route.replace('kind=references', 'kind=search'), client=owner)['state'] == 'consistent'
    assert product.cli(['indexes', 'validate', '--version', t['version_id'], '--kind', 'references'], client=owner)['data']['state'] == 'consistent'
    key = str(uuid.uuid4())
    created = product.cli(['indexes', 'rebuild', '--version', t['version_id'], '--kind', 'all', '--operation-id', key], client=owner)['data']['result']
    assert len(created['job_ids']) == 2 and created['operation_id'] == key
    wait_created_jobs(product, owner, created['job_ids'])
    again = product.data('POST', '/api/v1/versions/' + t['version_id'] + '/indexes/rebuild', client=owner,
                         body={'kind': 'all'}, headers={'Idempotency-Key': key})
    assert again == created
    assert product.data('GET', valid_route, client=owner)['state'] == 'consistent'
    product.stop(); product.start()
    assert product.data('GET', route, client=owner)['counts'] == first['counts']
    status, data, _ = product.request('GET', '/api/v1/versions/' + s['version_id'] + '/file?path=source.html', client=owner, raw=True)
    assert status == 200 and data == original
