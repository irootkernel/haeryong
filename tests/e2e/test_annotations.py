"""T-005-02 exact anchors/CAS, X19/C07 and B-1 through public production boundaries."""
import hashlib
import uuid
from urllib.parse import urlencode
import pytest

pytestmark = pytest.mark.e2e

def key():
    return str(uuid.uuid4())

def content(text='note', context=None):
    return dict(body=text, body_ref=None, selected_text='selected fallback', context=context, context_ref=None)

def create(product, owner, version, page, region=None, **kwargs):
    return product.data('POST', f'/api/v1/versions/{version}/annotations', client=owner,
                        headers={'Idempotency-Key': key()}, body=dict(page_path=page, region_id=region,
                        page_query='scenario=+%2F' if page else '', page_fragment='part' if page else '', **content(**kwargs)))

def setup_notes(product, owner, tmp_path):
    source = tmp_path / 'notes-input'
    source.mkdir()
    for page in ['a.html', 'b.html']:
        (source / page).write_text('<h1 id="retry">same page-local region</h1>')
    (source / 'no-regions.html').write_text('<h1>Plain page</h1>')
    _, _, published = product.import_tree(owner, source, extra=('--new-document',))
    return source, published

def context_bytes(product, client, note, revision, expected):
    status, raw, headers = product.request('GET', f"/api/v1/annotations/{note}/context?revision={revision}", client=client, raw=True)
    assert status == 200 and raw == expected
    headers = {k.lower(): v for k, v in headers.items()}
    assert headers['etag'] == '"sha256-' + hashlib.sha256(raw).hexdigest() + '"'
    assert headers['content-length'] == str(len(raw))
    assert headers['content-type'] in {'text/plain', 'application/json'}

def test_exact_notes_restart_new_versions_explicit_reattach_history_and_context_recovery(product, owner, tmp_path):
    source, old = setup_notes(product, owner, tmp_path)
    a = create(product, owner, old['version_id'], 'a.html', 'retry', context='old context 🌊')
    b = create(product, owner, old['version_id'], 'b.html', 'retry')
    plain = create(product, owner, old['version_id'], 'no-regions.html')
    whole = create(product, owner, old['version_id'], None)
    assert a['anchor']['page_path'] == 'a.html' and b['anchor']['page_path'] == 'b.html'
    assert a['resolution']['status'] == 'unresolved'  # best-effort DOM ID is not authored region metadata
    assert plain['anchor']['region_id'] is None and whole['anchor']['page_path'] is None
    (source / 'a.html').write_text('<h1>Changed ambiguous region structure</h1>')
    _, _, new = product.import_tree(owner, source, extra=('--document', old['document_id'], '--base-version', old['version_id']))
    assert product.data('GET', f"/api/v1/versions/{new['version_id']}/annotations", client=owner)['items'] == []
    destination = dict(version_id=new['version_id'], page_path='a.html', region_id='retry', page_query='', page_fragment='')
    route = '/api/v1/annotations/' + a['annotation_id']
    reattach = dict(action='reattach', anchor=destination, selected_text=None, context='new context', context_ref=None)
    status, _, _ = product.request('PATCH', route, client=owner, headers={'Idempotency-Key': key(), 'If-Match': a['etag']}, body=reattach)
    assert status == 400
    destination['region_id'] = None  # explicit owner page-level fallback
    moved = product.data('PATCH', route, client=owner, headers={'Idempotency-Key': key(), 'If-Match': a['etag']}, body=reattach)
    assert moved['original_anchor'] == a['anchor'] and moved['anchor'] == destination and moved['body'] == a['body']
    assert moved['context_ref'] != a['context_ref']
    second = product.owner('context recovery owner')
    context_bytes(product, second, a['annotation_id'], 1, 'old context 🌊'.encode())
    context_bytes(product, second, a['annotation_id'], 2, b'new context')
    product.stop(); product.start()
    product.data('DELETE', '/api/v1/sessions/' + owner['session']['session_id'], client=second)
    context_bytes(product, second, a['annotation_id'], 1, 'old context 🌊'.encode())
    context_bytes(product, second, a['annotation_id'], 2, b'new context')
    status, _, _ = product.request('GET', route + '/context?revision=1', client=owner, raw=True)
    assert status == 401
    history = product.data('GET', route + '/history', client=second)['items']
    assert [row['action'] for row in history] == ['create', 'reattach']
    assert history[0]['anchor'] == a['anchor'] and history[0]['body'] == 'note'
    product.data('DELETE', route, client=second, headers={'Idempotency-Key': key(), 'If-Match': moved['etag']})
    tombstone = product.data('GET', route, client=second)
    assert tombstone['deleted_at'] and tombstone['body'] is None and tombstone['context_ref'] is None
    context_bytes(product, second, a['annotation_id'], 1, 'old context 🌊'.encode())
    assert product.request('GET', route + '/context?revision=3', client=second)[0] == 404
    assert product.request('GET', route + '/context?revision=999', client=second)[0] == 404
    assert product.data('GET', f"/api/v1/versions/{old['version_id']}/review", client=second)['exists'] is False

def test_note_concurrency_receipt_replay_preconditions_and_revisit_CLI(product, owner, tmp_path):
    _, published = setup_notes(product, owner, tmp_path)
    note = create(product, owner, published['version_id'], 'a.html')
    route = '/api/v1/annotations/' + note['annotation_id']
    second = product.owner('concurrent note editor')
    patch = dict(action='edit', **content('new server body'))
    operation = key()
    saved = product.data('PATCH', route, client=second, headers={'Idempotency-Key': operation, 'If-Match': note['etag']}, body=patch)
    status, error, _ = product.request('PATCH', route, client=owner, headers={'Idempotency-Key': key(), 'If-Match': note['etag']}, body=dict(action='edit', **content('local unsaved draft')))
    assert status == 412 and error['error']['details'] == dict(revision=saved['revision'], etag=saved['etag'])
    assert product.data('PATCH', route, client=second, headers={'Idempotency-Key': operation, 'If-Match': note['etag']}, body=patch) == saved
    assert product.request('PATCH', route, client=owner, headers={'Idempotency-Key': operation, 'If-Match': saved['etag']}, body=patch)[0] == 409
    for control, expected in [({}, 428), ({'If-Match':'*'},400), ({'If-Match':'"a", "b"'},400), ({'If-None-Match':'*'},400), ({'If-Match':'"stale"'},412)]:
        assert product.request('PATCH', route, client=owner, headers={'Idempotency-Key': key(), **control}, body=patch)[0] == expected
    assert len(product.data('GET', route + '/history', client=owner)['items']) == 2
    document = published['document_id']
    absent = product.cli(['revisit','show','--document',document], client=owner)['data']
    assert absent['exists'] is False and absent['value'] == dict(flag=False) and absent['revision'] is None
    flag = product.cli(['revisit','set','--document',document,'--flag','true','--if-none-match','*'], client=owner)['data']['result']
    assert flag['exists'] and flag['value']['flag'] is True
    listed = product.data('GET','/api/v1/documents?revisit=true&limit=1',client=owner)
    assert listed['items'][0]['document_id'] == document and listed['items'][0]['revisit'] is True
    product.cli(['revisit','set','--document',document,'--flag','false','--if-match',flag['etag']], client=owner)
    assert product.data('GET','/api/v1/documents?revisit=true',client=owner)['items'] == []
    assert product.data('GET', f"/api/v1/versions/{published['version_id']}/review", client=owner)['exists'] is False

def test_annotation_body_upload_exact_target_stream_and_prior_revision(product, owner, tmp_path):
    _, published = setup_notes(product, owner, tmp_path)
    final = key()
    text = ('**Immutable Markdown** 🌊\n' * 4000).encode()
    upload = product.data('POST','/api/v1/uploads',client=owner,headers={'Idempotency-Key':key()},body=dict(purpose='body',input_mode='object',target=dict(operation_id=final,action='annotation_create',version_id=published['version_id'],page_path='a.html')))['upload_id']
    ack = product.data('PUT',f'/api/v1/uploads/{upload}/entries?path=_input&kind=file',client=owner,body=text,headers={'Content-Type':'application/octet-stream','Haeryong-Upload-Offset':'0','Haeryong-Upload-Length':str(len(text)),'Haeryong-Upload-Chunk-SHA256':hashlib.sha256(text).hexdigest(),'Haeryong-Upload-Final':'true'})
    cp = ack['checkpoint']; cp = cp | dict(generation=cp['generation']+1,complete_input=True)
    sealed = product.data('POST',f'/api/v1/uploads/{upload}/complete',client=owner,headers={'Idempotency-Key':key()},body=dict(checkpoint=cp))
    product.job(owner,sealed['job_id'])
    body = dict(page_path='a.html',region_id=None,page_query='',page_fragment='',**content())
    body.update(body=None,body_ref=dict(upload_id=upload))
    route = f"/api/v1/versions/{published['version_id']}/annotations"
    assert product.request('POST',route,client=owner,headers={'Idempotency-Key':key()},body=body)[0] == 409
    note = product.data('POST',route,client=owner,headers={'Idempotency-Key':final},body=body)
    assert note['body'] is None and note['body_ref']['bytes_sha256'] == hashlib.sha256(text).hexdigest()
    status, raw, _ = product.request('GET',note['body_ref']['content_url'],client=owner,raw=True)
    assert status == 200 and raw == text


def sealed_note_body(product, owner, target):
    text = b'identical note object'
    upload = product.data('POST', '/api/v1/uploads', client=owner, headers={'Idempotency-Key': key()}, body=dict(purpose='body', input_mode='object', target=target))['upload_id']
    ack = product.data('PUT', f'/api/v1/uploads/{upload}/entries?path=_input&kind=file', client=owner, body=text, headers={'Content-Type': 'application/octet-stream', 'Haeryong-Upload-Offset': '0', 'Haeryong-Upload-Length': str(len(text)), 'Haeryong-Upload-Chunk-SHA256': hashlib.sha256(text).hexdigest(), 'Haeryong-Upload-Final': 'true'})
    checkpoint = ack['checkpoint'] | dict(generation=ack['checkpoint']['generation'] + 1, complete_input=True)
    sealed = product.data('POST', f'/api/v1/uploads/{upload}/complete', client=owner, headers={'Idempotency-Key': key()}, body=dict(checkpoint=checkpoint))
    product.job(owner, sealed['job_id'])
    return upload


def bound_note_context(product, owner, version, page, render=None, payload=b'identical context bytes'):
    fields = dict(version_id=version, page_path=page)
    if render is not None:
        fields['render_instance_id'] = render
    query = urlencode(fields)
    return product.data('POST', '/api/v1/contexts?' + query, client=owner, headers={'Idempotency-Key': key(), 'Content-Type': 'text/plain'}, body=payload)['context_ref']


@pytest.mark.parametrize('action', ['create', 'edit'])
def test_note_receipt_binding_equivalent_handles_replay_and_changed_binding_conflicts(product, owner, tmp_path, action):
    _, published = setup_notes(product, owner, tmp_path)
    version, operation = published['version_id'], key()
    body = dict(page_path='a.html', region_id=None, page_query='', page_fragment='', **content())
    method, route, controls = 'POST', f'/api/v1/versions/{version}/annotations', {}
    binding = dict(operation_id=operation, action='annotation_create', version_id=version, page_path='a.html')
    if action == 'edit':
        base = create(product, owner, version, 'a.html')
        other = create(product, owner, version, 'b.html')
        method, route, controls = 'PATCH', '/api/v1/annotations/' + base['annotation_id'], {'If-Match': base['etag']}
        body = dict(action='edit', **content())
        binding = dict(operation_id=operation, action='annotation_edit', annotation_id=base['annotation_id'])
    first, alternate = sealed_note_body(product, owner, binding), sealed_note_body(product, owner, binding)
    changed_target = binding | (dict(page_path='b.html') if action == 'create' else dict(annotation_id=other['annotation_id']))
    wrong_uploads = [sealed_note_body(product, owner, binding | dict(operation_id=key())), sealed_note_body(product, owner, changed_target)]
    render = key()
    context = bound_note_context(product, owner, version, 'a.html', render)
    same_context = bound_note_context(product, owner, version, 'a.html', render)
    another = product.owner('same owner, another context binding session')
    wrong_contexts = [bound_note_context(product, owner, version, 'b.html', render), bound_note_context(product, owner, version, 'a.html', key()), bound_note_context(product, another, version, 'a.html', render)]
    body.update(body=None, body_ref=dict(upload_id=first), context_ref=context)
    headers = {'Idempotency-Key': operation, **controls}
    saved = product.data(method, route, client=owner, headers=headers, body=body)
    history_route = '/api/v1/annotations/' + saved['annotation_id'] + '/history'
    history = product.data('GET', history_route, client=owner)
    equivalent = body | dict(body_ref=dict(upload_id=alternate), context_ref=same_context)
    assert product.data(method, route, client=owner, headers=headers, body=equivalent) == saved
    assert product.data(method, route, client=owner, headers=headers, body=equivalent | dict(body='identical note object', body_ref=None)) == saved
    for changed in [equivalent | dict(body_ref=dict(upload_id=value)) for value in wrong_uploads] + [equivalent | dict(context_ref=value) for value in wrong_contexts]:
        status, failure, _ = product.request(method, route, client=owner, headers=headers, body=changed)
        assert status == 409 and failure['error']['code'] == 'OPERATION_CONFLICT'
        assert product.data('GET', history_route, client=owner) == history
        current = product.data('GET', '/api/v1/annotations/' + saved['annotation_id'], client=owner)
        assert current['revision'] == saved['revision'] and current['etag'] == saved['etag']
    assert len(product.data('GET', f'/api/v1/versions/{version}/annotations', client=owner)['items']) == (1 if action == 'create' else 2)


@pytest.mark.parametrize('action', ['create', 'edit'])
def test_inline_saved_context_equivalence_captured_binding_and_changed_intent(product, owner, tmp_path, action):
    _, published = setup_notes(product, owner, tmp_path)
    version, operation = published['version_id'], key()
    inline = 'inline context 🌊'
    method, route, controls = 'POST', f'/api/v1/versions/{version}/annotations', {}
    body = dict(page_path='a.html', region_id=None, page_query='', page_fragment='', **content('local note', inline))
    if action == 'edit':
        base = create(product, owner, version, 'a.html')
        method, route, controls = 'PATCH', '/api/v1/annotations/' + base['annotation_id'], {'If-Match': base['etag']}
        body = dict(action='edit', **content('local note', inline))
    headers = {'Idempotency-Key': operation, **controls}
    saved = product.data(method, route, client=owner, headers=headers, body=body)
    referenced = body | dict(context=None, context_ref=saved['context_ref'])
    alternate = bound_note_context(product, owner, version, 'a.html', payload=inline.encode())
    assert product.data(method, route, client=owner, headers=headers, body=referenced) == saved
    assert product.data(method, route, client=owner, headers=headers, body=referenced | dict(context_ref=alternate)) == saved
    destination = dict(version_id=version, page_path='b.html', region_id=None, page_query='', page_fragment='')
    current_route = '/api/v1/annotations/' + saved['annotation_id']
    moved = product.data('PATCH', current_route, client=owner, headers={'Idempotency-Key': key(), 'If-Match': saved['etag']}, body=dict(action='reattach', anchor=destination, selected_text=None, context=None, context_ref=None))
    another = product.owner('replay implicit binding from another owner session')
    wrong_refs = [bound_note_context(product, owner, version, 'b.html', payload=inline.encode()), bound_note_context(product, owner, version, 'a.html', render=key(), payload=inline.encode()), bound_note_context(product, another, version, 'a.html', payload=inline.encode()), bound_note_context(product, owner, version, 'a.html', payload=b'changed bytes')]
    product.stop(); product.start()
    history = product.data('GET', current_route + '/history', client=owner)
    assert product.data(method, route, client=another, headers=headers, body=body) == saved
    assert product.data(method, route, client=another, headers=headers, body=referenced) == saved
    for changed in [referenced | dict(context_ref=value) for value in wrong_refs] + [body | dict(context='changed inline bytes')]:
        status, failure, _ = product.request(method, route, client=owner, headers=headers, body=changed)
        assert status == 409 and failure['error']['code'] == 'OPERATION_CONFLICT'
        assert product.data('GET', current_route + '/history', client=owner) == history
        assert product.data('GET', current_route, client=owner)['etag'] == moved['etag']


# Seed only lifecycle/credential fixtures, as existing durability/scope tests do.
# This is not a lifecycle API or TASK-005-03 workflow implementation.
def annotation_sql_snapshot(path):
    import sqlite3
    from contextlib import closing
    tables = {'annotations': 'annotation_id', 'annotation_history': 'annotation_id,revision', 'revisit_flags': 'owner_id,document_id', 'contexts': 'context_id', 'content_objects': 'content_hash', 'object_pins': 'pin_id', 'object_references': 'owner_kind,owner_id,content_hash', 'events': 'sequence', 'operation_receipts': 'principal_id,operation_id'}
    with closing(sqlite3.connect('file:' + str(path) + '?mode=ro', uri=True)) as conn:
        return {table: conn.execute('SELECT * FROM ' + table + ' ORDER BY ' + order).fetchall() for table, order in tables.items()}


@pytest.mark.parametrize('lifecycle', ['active', 'archived', 'trash', 'purged'])
def test_personal_write_lifecycle_contract_and_nondisclosure(product, owner, tmp_path, lifecycle):
    import json
    import secrets
    import sqlite3
    import time
    from contextlib import closing
    from .harness import PrivateClient
    from .test_search import ready
    from .test_references import reference_ready
    _, published = setup_notes(product, owner, tmp_path)
    version, document = published['version_id'], published['document_id']
    create_route = f'/api/v1/versions/{version}/annotations'
    original_body = dict(page_path='a.html', region_id=None, page_query='', page_fragment='', **content('edit base', 'saved context'))
    original_key = key()
    edit = product.data('POST', create_route, client=owner, headers={'Idempotency-Key': original_key}, body=original_body)
    move, remove = create(product, owner, version, 'a.html'), create(product, owner, version, 'a.html')
    revisit_route = f'/api/v1/documents/{document}/revisit'
    revisit_key = key()
    revisit = product.data('PUT', revisit_route, client=owner, headers={'Idempotency-Key': revisit_key, 'If-None-Match': '*'}, body=dict(flag=False))
    ready(product, owner, version); reference_ready(product, owner, version)
    # Stop the real daemon before fixture seeding; restart reuses its normal
    # auth, handlers and durable storage, with no production fault/lifecycle API.
    product.stop()
    path = product.data_dir / 'storage' / 'library.sqlite'
    token, principal = secrets.token_hex(32), key()
    with closing(sqlite3.connect(path)) as conn, conn:
        project = conn.execute('SELECT project_id FROM documents WHERE document_id=?', (document,)).fetchone()[0]
        permissions = conn.execute('SELECT permissions_revision FROM projects WHERE project_id=?', (project,)).fetchone()[0]
        epoch = conn.execute('SELECT auth_epoch FROM libraries WHERE singleton=1').fetchone()[0]
        revision = conn.execute('SELECT max(config_revision) FROM config_revisions').fetchone()[0]
        scope = json.dumps(dict(kind='workflow', actions=['version.read', 'project.read'], project_ids=[project], version_ids=[version]))
        conn.execute('INSERT INTO workflow_principals VALUES(?,?,?,?,?)', (principal, key(), 1, scope, 1))
        verifier = hashlib.sha256(('workflow\x00' + token).encode()).hexdigest()
        conn.execute('INSERT INTO scoped_credentials VALUES(?,?,?,?,?,?,?,?,?,?,?)', (key(), principal, verifier, epoch, scope, json.dumps({project: permissions}), revision, time.time_ns() // 1000000, None, 0, 1))
        conn.execute('UPDATE documents SET lifecycle=? WHERE document_id=?', (lifecycle, document))
    product.start()
    reader = PrivateClient(token=token)
    assert product.request('GET', '/api/v1/capabilities', client=reader)[0] == 200
    before = annotation_sql_snapshot(path)
    cases = [
        ('POST', create_route, {}, dict(page_path='a.html', region_id=None, page_query='', page_fragment='', **content('new note', 'new inline context'))),
        ('PATCH', '/api/v1/annotations/' + edit['annotation_id'], {'If-Match': edit['etag']}, dict(action='edit', **content('edited', 'new edit context'))),
        ('PATCH', '/api/v1/annotations/' + move['annotation_id'], {'If-Match': move['etag']}, dict(action='reattach', anchor=dict(version_id=version, page_path='b.html', region_id=None, page_query='', page_fragment=''), selected_text=None, context='new move context', context_ref=None)),
        ('DELETE', '/api/v1/annotations/' + remove['annotation_id'], {'If-Match': remove['etag']}, None),
        ('PUT', revisit_route, {'If-Match': revisit['etag']}, dict(flag=True)),
    ]
    blocked = lifecycle in {'trash', 'purged'}
    for method, route, controls, body in cases:
        status, payload, _ = product.request(method, route, client=owner, headers={'Idempotency-Key': key(), **controls}, body=body)
        if blocked:
            assert status == 409 and payload['error']['code'] == 'INVALID_TRANSITION'
            assert payload['error']['details'] == dict(lifecycle=lifecycle)
            assert annotation_sql_snapshot(path) == before
        else:
            assert status == (201 if method == 'POST' else 200)
            assert payload['data']['revision'] == (1 if method == 'POST' else 2)
    before = annotation_sql_snapshot(path)
    for method, route, controls, body in cases:
        unknown = route.replace(version if method == 'POST' else document if method == 'PUT' else route.rsplit('/', 1)[-1], key())
        for target in (route, unknown):
            status, failure, _ = product.request(method, target, client=reader, headers={'Idempotency-Key': key(), **controls}, body=body)
            assert status == 404 and failure['error']['code'] == 'NOT_FOUND' and failure['error']['details'] == {}
            assert annotation_sql_snapshot(path) == before
        status, failure, _ = product.request(method, unknown, client=owner, headers={'Idempotency-Key': key(), **controls}, body=body)
        assert status == 404 and failure['error']['code'] == 'NOT_FOUND' and failure['error']['details'] == {}
        assert annotation_sql_snapshot(path) == before
    if blocked:
        for route in [create_route, '/api/v1/annotations/' + edit['annotation_id'], revisit_route, '/api/v1/operations/' + original_key, '/api/v1/operations/' + revisit_key]:
            assert product.request('GET', route, client=owner)[0] == 404
        assert product.request('POST', create_route, client=owner, headers={'Idempotency-Key': original_key}, body=original_body)[0] == 404
        assert product.request('PUT', revisit_route, client=owner, headers={'Idempotency-Key': revisit_key, 'If-None-Match': '*'}, body=dict(flag=False))[0] == 404
        assert annotation_sql_snapshot(path) == before
