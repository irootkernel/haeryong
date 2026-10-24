"""T-005-01-01..03, X11: explicit directory Review through production CLI/HTTP."""
from concurrent.futures import ThreadPoolExecutor
import http.client
import json
import uuid
import pytest
from .harness import Proxy

pytestmark = pytest.mark.e2e


def imported(product, owner, tmp_path):
    source = tmp_path / 'directory'
    source.mkdir()
    (source / 'first.html').write_text('<h1>First</h1><a href="second.html">Second</a>')
    (source / 'second.html').write_text('<h1>Second</h1>')
    _, _, published = product.import_tree(owner, source, extra=('--new-document',))
    for _ in range(2):
        _, _, published = product.import_tree(owner, source, extra=('--document', published['document_id'], '--base-version', published['version_id']))
    assert product.data('GET', '/api/v1/versions/' + published['version_id'], client=owner)['sequence'] == 3
    return source, published


def test_review_directory_versions_history_CLI_and_stale_concurrent_state(product, owner, tmp_path):
    source, v3 = imported(product, owner, tmp_path)
    route = '/api/v1/versions/' + v3['version_id'] + '/review'
    absent = product.cli(['review', 'show', '--version', v3['version_id']], client=owner)['data']
    assert absent == dict(version_id=v3['version_id'], exists=False, value=dict(state='unreviewed', reviewed_at=None), revision=None, etag=None, page_position_url=None, required_precondition='if_none_match_star')
    for page in ['first.html', 'second.html']:
        product.data('GET', '/api/v1/versions/' + v3['version_id'] + '/render-bootstrap?page_path=' + page, client=owner)
        product.data('PUT', '/api/v1/versions/' + v3['version_id'] + '/page-position?page=' + page, client=owner, body=dict(page_query='', page_fragment='', position=dict(top=900)), headers={'Idempotency-Key': str(uuid.uuid4()), 'If-None-Match': '*'})
    assert product.data('GET', route, client=owner) == absent
    key = str(uuid.uuid4())
    saved = product.cli(['review', 'set', '--version', v3['version_id'], '--action', 'complete', '--if-none-match', '*', '--operation-id', key], client=owner)['data']
    reviewed = saved['result']
    assert reviewed['operation_id'] == key
    assert reviewed['value']['state'] == 'reviewed' and reviewed['value']['reviewed_at']
    assert product.data('PUT', route, client=owner, body=dict(action='complete', state='reviewed'), headers={'Idempotency-Key': key, 'If-None-Match': '*'}) == reviewed
    # A second device's pre-existing observation cannot erase the completion.
    second = product.owner('second Review device')
    status, error, _ = product.request('PUT', route, client=second, body=dict(action='start', state='reviewing'), headers={'Idempotency-Key': str(uuid.uuid4()), 'If-None-Match': '*'})
    assert status == 412 and error['error']['details'] == dict(revision=reviewed['revision'], etag=reviewed['etag'])
    product.cli(['review', 'set', '--version', v3['version_id'], '--action', 'clear', '--if-match', '"stale"'], client=second, code=4)
    history = product.cli(['review', 'history', '--version', v3['version_id']], client=owner)['data']
    assert len(history['items']) == 1 and history['items'][0]['operation_id'] == key
    (source / 'first.html').write_text('<h1>New version</h1>')
    _, _, v4 = product.import_tree(owner, source, extra=('--document', v3['document_id'], '--base-version', v3['version_id']))
    newroute = '/api/v1/versions/' + v4['version_id'] + '/review'
    product.stop(); product.start()
    assert product.data('GET', route, client=owner) == {k: v for k, v in reviewed.items() if k != 'operation_id'}
    assert product.data('GET', newroute, client=owner)['value'] == dict(state='unreviewed', reviewed_at=None)
    summary = product.data('GET', '/api/v1/documents/' + v3['document_id'], client=owner)
    assert summary['review_state'] == 'unreviewed' and summary['last_reviewed_version_id'] == v3['version_id'] and summary['updated_since_review'] is True
    updated = product.data('GET', '/api/v1/documents?review=updated_since_review&limit=1', client=owner)
    assert updated['items'][0]['document_id'] == v3['document_id']
    reopened = product.data('PUT', route, client=owner, body=dict(action='reopen', state='reviewing'), headers={'Idempotency-Key': str(uuid.uuid4()), 'If-Match': reviewed['etag']})
    assert reopened['value']['reviewed_at'] is None
    continuing = product.data('GET', '/api/v1/documents?review=reviewing', client=owner)
    assert continuing['items'][0]['continue_review_version_id'] == v3['version_id']
    assert continuing['items'][0]['latest_version_id'] == v4['version_id']
    latest_done = product.data('PUT', newroute, client=owner, body=dict(action='complete', state='reviewed'), headers={'Idempotency-Key': str(uuid.uuid4()), 'If-None-Match': '*'})
    old_done = product.data('PUT', route, client=owner, body=dict(action='complete', state='reviewed'), headers={'Idempotency-Key': str(uuid.uuid4()), 'If-Match': reopened['etag']})
    summary = product.data('GET', '/api/v1/documents/' + v3['document_id'], client=owner)
    assert summary['last_reviewed_version_id'] == v3['version_id'] and summary['updated_since_review'] is True
    product.data('PUT', route, client=owner, body=dict(action='clear', state='unreviewed'), headers={'Idempotency-Key': str(uuid.uuid4()), 'If-Match': old_done['etag']})
    summary = product.data('GET', '/api/v1/documents/' + v3['document_id'], client=owner)
    assert summary['last_reviewed_version_id'] == v4['version_id'] and summary['updated_since_review'] is False
    assert product.data('GET', newroute, client=owner) == {k: v for k, v in latest_done.items() if k != 'operation_id'}



def test_review_X11_controls_replay_duplicates_and_lost_ack(product, owner, tmp_path):
    _, version = imported(product, owner, tmp_path)
    route = '/api/v1/versions/' + version['version_id'] + '/review'
    body = dict(action='complete', state='reviewed')
    for headers, status, code in [({}, 428, 'PRECONDITION_REQUIRED'), ({'Idempotency-Key': 'bad', 'If-None-Match': '*'}, 400, 'INVALID_REQUEST'), ({'Idempotency-Key': str(uuid.uuid4())}, 428, 'PRECONDITION_REQUIRED'), ({'Idempotency-Key': str(uuid.uuid4()), 'If-None-Match': '*', 'If-Match': '"other"'}, 400, 'INVALID_REQUEST')]:
        actual, error, _ = product.request('PUT', route, client=owner, body=body, headers=headers)
        assert actual == status and error['error']['code'] == code
    for invalid, code in [(dict(action='scroll', state='reviewed'), 'INVALID_ENUM'), (dict(action='start', state='reviewed'), 'INVALID_REQUEST'), (dict(action='complete'), 'INVALID_REQUEST')]:
        status, error, _ = product.request('PUT', route, client=owner, body=invalid, headers={'Idempotency-Key': str(uuid.uuid4()), 'If-None-Match': '*'})
        assert status == 400 and error['error']['code'] == code
    key = str(uuid.uuid4()); controls = {'Idempotency-Key': key, 'If-None-Match': '*'}
    with ThreadPoolExecutor(max_workers=2) as pool:
        answers = list(pool.map(lambda _: product.data('PUT', route, client=owner, body=body, headers=controls), range(2)))
    assert answers[0] == answers[1]
    page_route = '/api/v1/versions/' + version['version_id'] + '/page-position?page=first.html'
    page_body = dict(page_query='', page_fragment='', position={})
    status, error, _ = product.request('PUT', page_route, client=owner, body=page_body, headers=controls)
    assert status == 409 and error['error']['code'] == 'OPERATION_CONFLICT'
    page_key = str(uuid.uuid4())
    product.data('PUT', page_route, client=owner, body=page_body, headers={'Idempotency-Key': page_key, 'If-None-Match': '*'})
    status, error, _ = product.request('PUT', route, client=owner, body=body, headers={'Idempotency-Key': page_key, 'If-Match': answers[0]['etag']})
    assert status == 409 and error['error']['code'] == 'OPERATION_CONFLICT'

    for changed, headers in [(dict(action='clear', state='unreviewed'), controls), (body, controls | {'If-None-Match': 'changed'})]:
        status, _, _ = product.request('PUT', route, client=owner, body=changed, headers=headers)
        assert status in (400, 409)
    status, error, _ = product.request('PUT', route, client=owner, body=body, headers={'Idempotency-Key': key, 'If-Match': answers[0]['etag']})
    assert status == 409 and error['error']['code'] == 'OPERATION_CONFLICT'
    before = product.data('GET', route + '/history', client=owner)
    assert len(before['items']) == 1
    no_op = product.data('PUT', route, client=owner, body=body, headers={'Idempotency-Key': str(uuid.uuid4()), 'If-Match': answers[0]['etag']})
    assert {k: v for k, v in no_op.items() if k != 'operation_id'} == {k: v for k, v in answers[0].items() if k != 'operation_id'}
    assert product.data('GET', route + '/history', client=owner) == before
    # Lost committed response is recovered with the original key AND precondition.
    with Proxy(product, drop=lambda method, path, status: method == 'PUT' and path == route) as proxy:
        caller = proxy.client(owner)
        lost = product.cli(['review', 'set', '--version', version['version_id'], '--action', 'reopen', '--if-match', answers[0]['etag']], client=caller, code=5)
        assert lost['data']['operation_id'] and lost['data']['original_status'] is None
        # Public retry reuses the pending command index; no test-side mutation of journals.
        recovered = product.cli(['review', 'set', '--version', version['version_id'], '--action', 'reopen', '--if-match', answers[0]['etag']], client=caller)['data']
        assert recovered['result']['value']['state'] == 'reviewing'
        assert recovered['operation_id'] == lost['data']['operation_id']
    assert len(product.data('GET', route + '/history', client=owner)['items']) == 2
    status, payload, _ = product.request('HEAD', route, client=owner, raw=True)
    assert status == 200 and payload == b''

    successor = product.owner('Review revocation observer')
    product.data('DELETE', '/api/v1/session', client=owner)
    status, error, _ = product.request('PUT', route, client=owner, body=body, headers=controls)
    assert status == 401 and error['error']['code'] == 'SESSION_REVOKED'
    assert len(product.data('GET', route + '/history', client=successor)['items']) == 2


def test_review_C1_individual_preconditions_and_existing_personal_PUT(product, owner, tmp_path):
    _, version = imported(product, owner, tmp_path)
    route = '/api/v1/versions/' + version['version_id'] + '/review'

    def put(path, body, controls):
        encoded = json.dumps(body).encode()
        connection = http.client.HTTPConnection('127.0.0.1', product.address, timeout=30)
        try:
            connection.putrequest('PUT', path)
            for name, value in [('Origin', product.endpoint), ('Authorization', 'Bearer ' + owner['token']),
                                ('Content-Type', 'application/json'), ('Content-Length', str(len(encoded))),
                                ('Idempotency-Key', str(uuid.uuid4())), *controls]:
                connection.putheader(name, value)
            connection.endheaders(encoded)
            response = connection.getresponse()
            return response.status, json.loads(response.read())
        finally:
            connection.close()

    body = dict(action='complete', state='reviewed')
    for controls, status, code in [
        ([('If-Match', '"a", "b"')], 400, 'INVALID_REQUEST'),
        ([('If-Match', '"a"'), ('If-Match', '"b"')], 400, 'INVALID_REQUEST'),
        ([('If-Match', 'unquoted')], 400, 'INVALID_REQUEST'),
        ([('If-Match', 'W/"weak"')], 400, 'INVALID_REQUEST'),
        ([('If-Match', '*')], 400, 'INVALID_REQUEST'),
        ([('If-Match', '""')], 400, 'INVALID_REQUEST'),
        ([], 428, 'PRECONDITION_REQUIRED'),
        ([('If-Match', '"stale"')], 412, 'PRECONDITION_FAILED'),
        # A comma inside one opaque tag is valid syntax, not a validator list.
        ([('If-Match', '"part,a"')], 412, 'PRECONDITION_FAILED'),
    ]:
        actual, error = put(route, body, controls)
        assert actual == status and error['error']['code'] == code
    personal = '/api/v1/versions/' + version['version_id'] + '/reading-position'
    personal_body = dict(page_path=None, page_query='', page_fragment='', opened=True)
    for value, status, code in [('unquoted', 400, 'INVALID_REQUEST'), ('"a", "b"', 400, 'INVALID_REQUEST'), ('"stale"', 412, 'PRECONDITION_FAILED')]:
        actual, error = put(personal, personal_body, [('If-Match', value)])
        assert actual == status and error['error']['code'] == code
    assert product.data('GET', route, client=owner)['exists'] is False
    assert product.data('GET', route + '/history', client=owner)['items'] == []
