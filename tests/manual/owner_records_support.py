"""TASK-005-06 public setup and independent preservation oracles.

No provider, source-repository writes, direct SQL, or personal-record import.
Expected inventories come from the selected inputs before product operations.
"""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import stat
import subprocess
import time
import urllib.parse
import uuid
import zipfile

REPO = Path(__file__).resolve().parents[2]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


reference = load('owner_reference_support', REPO / 'tests/manual/reference_support.py')
FIXED_SOURCES = {'mulgae': 'cef337cf927b820316f42a41f1441fbc653203f8',
                 'gaori': '393af5999bbbb30b5d11e2a17a2325c8cf1833b7'}
QUERY, FRAGMENT = 'q=%2F+ 공백', 'part/% +'
BOUNDARY = {
    'a.html': b'<!doctype html><h1>Labelled synthetic A v1</h1><p id="retry">A retry</p>',
    'b.html': b'<!doctype html><h1>Labelled synthetic B v1</h1><p id="retry">B retry</p>',
    'no-region.html': b'<!doctype html><h1>Labelled region-free page</h1>',
    'nested/설명 %.html': '<!doctype html><p id="retry">Unicode exact page</p>'.encode(),
    '.hidden': b'preserve hidden bytes\x00', 'manifest.json': b'ordinary original payload',
    'attachment.unknown': bytes(range(256)) * 17,
}
LARGE_CONTEXT = 'Task6 native retained context 🌊' * 90000
LARGE_SELECTED_TEXT = 'Task6 exact native fallback'
LARGE_CONTEXT_BYTES = json.dumps({'selected_text': LARGE_SELECTED_TEXT, 'text': LARGE_CONTEXT}, ensure_ascii=False, separators=(',', ':')).encode()
LARGE_BODIES={'browser':('Task6 large body 🌊\n'*5000).encode(),'native':('Task6 native large body 🌊\n'*5000).encode()}
CONSENT_CASES = ['ui.notes.setup-own-save'] + ['ui.notes.safe.' + name for name in ('new','edit','reattach','delete','discard')] + ['ui.notes.invalidate.panel','ui.notes.invalidate.tab','ui.notes.invalidate.route','ui.notes.storage-refusal','ui.notes.delayed-lock-draft-change'] + ['ui.notes.confirm.' + name for name in ('new','edit','reattach','delete','discard')]
BOUNDARY['a.html'] += ('''<script>(async()=>{const text=''' + json.dumps(LARGE_CONTEXT) + ''';const end=Date.now()+30000;while(Date.now()<end){try{if(!window.__haeryong){await new Promise(r=>setTimeout(r,100));continue;}await window.__haeryong.select('retry',{selected_text:'Task6 exact native fallback',text});return;}catch(e){if(!String(e.message).includes('Page channel is not ready'))throw e;await new Promise(r=>setTimeout(r,100));}}throw Error('synthetic context channel unavailable');})();</script>''').encode()


def key():
    return str(uuid.uuid4())


def tree(root):
    """lstat traversal never follows retained links, including directory links."""
    result = {}
    def walk(base):
        for item in sorted(os.scandir(base), key=lambda item: item.name):
            path = Path(item.path)
            name = path.relative_to(root).as_posix()
            mode = path.lstat().st_mode
            if stat.S_ISLNK(mode):
                result[name] = {'kind': 'symlink', 'link_text': os.readlink(path)}
            elif stat.S_ISDIR(mode):
                result[name] = {'kind': 'directory'}
                walk(path)
            else:
                assert stat.S_ISREG(mode), 'unsupported test input kind'
                with path.open('rb') as stream:
                    digest = hashlib.file_digest(stream, 'sha256').hexdigest()
                result[name] = {'kind': 'file', 'byte_size': path.stat().st_size, 'sha256': digest}
    walk(root)
    return result


def all_items(product, owner, path, limit=1):
    items, seen, cursor = [], set(), None
    while True:
        query = {'limit': limit}
        if cursor:
            query['cursor'] = cursor
        page = product.data('GET', path + ('&' if '?' in path else '?') + urllib.parse.urlencode(query), client=owner)
        items.extend(page['items'])
        cursor = page['next_cursor']
        if not cursor:
            return items
        assert cursor not in seen, 'cursor cycle'
        seen.add(cursor)


def published_tree(product, owner, version):
    actual = {}
    for entry in product.files(owner, version, limit=2):
        name, kind = entry['path'], entry['kind']
        assert name not in actual, 'duplicate published path'
        if kind == 'file':
            status, body, _ = product.request('GET', '/api/v1/versions/' + version + '/file?' + urllib.parse.urlencode({'path': name}), client=owner, raw=True)
            assert status == 200
            actual[name] = {'kind': kind, 'byte_size': len(body), 'sha256': hashlib.sha256(body).hexdigest()}
        elif kind == 'symlink':
            actual[name] = {'kind': kind, 'link_text': entry['link_target']}
        else:
            assert kind == 'directory'
            actual[name] = {'kind': kind}
    return actual


def prepare(product, owner):
    """Import original corpus, then synthetic boundaries; NO successor yet."""
    fixture = {'projects': {}, 'originals': {}, 'roots': {}, 'expected_trees': {}, 'authored_references': {},
               'fixed_sources': FIXED_SOURCES, 'synthetic_boundaries': True}
    for name, paths in reference.CORPUS_FILES.items():
        project = product.cli(['projects', 'register', '--name', 'Task6 ' + name], client=owner)['data']['result']['project_id']
        fixture['projects'][name] = project
        root = product.root / ('owner-original-' + name)
        root.mkdir()
        for relative in paths:
            destination = root / relative
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_bytes((REPO / 'spikes/knowledge-value/samples' / name / relative).read_bytes())
        expected = tree(root)
        meta = {'regions': [{'id': 'M11', 'page_path': 'recovery/compose.html'}]}
        if name == 'gaori':
            target = fixture['originals']['mulgae']
            meta = {'regions': [{'id': 'G12', 'page_path': 'gaori/lifecycle.html'}], 'references': [{
                'id': 'prior-compose', 'document_id': target['document_id'], 'version_id': target['version_id'],
                'page_path': 'recovery/compose.html', 'region_id': 'M11',
                'from': {'page_path': 'gaori/lifecycle.html', 'region_id': 'G12'}, 'relation': 'explanatory analogy'}]}
        metadata = product.root / (name + '-owner-metadata.json')
        metadata.write_text(json.dumps(meta))
        _, _, identity = product.import_tree(owner, root, extra=('--new-document', '--project', project, '--metadata', str(metadata)), timeout=120)
        reference.ready(product, owner, identity['version_id'])
        assert published_tree(product, owner, identity['version_id']) == expected
        fixture['originals'][name] = identity
        fixture['roots'][identity['version_id']] = str(root)
        fixture['expected_trees'][identity['version_id']] = expected
        fixture['authored_references'][identity['version_id']] = meta.get('references',[])
    root = product.root / 'owner-boundary-v1'
    root.mkdir()
    for relative, body in BOUNDARY.items():
        destination = root / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_bytes(body)
    (root / 'empty').mkdir()
    (root / 'alias').symlink_to('a.html')
    (root / 'external-link').symlink_to('../not-selected')
    expected = tree(root)
    meta = product.root / 'boundary-metadata.json'
    meta.write_text(json.dumps({'regions': [{'id': 'retry', 'page_path': p} for p in ['a.html', 'b.html', 'nested/설명 %.html']]}))
    _, _, identity = product.import_tree(owner, root, extra=('--new-document', '--project', fixture['projects']['mulgae'], '--metadata', str(meta)), timeout=120)
    reference.ready(product, owner, identity['version_id'])
    fixture['boundary'] = identity
    fixture['roots'][identity['version_id']] = str(root)
    fixture['expected_trees'][identity['version_id']] = expected
    fixture['routes'] = {
        'mulgae': reference.route(fixture['originals']['mulgae'], 'recovery/compose.html', 'M11'),
        'gaori': reference.route(fixture['originals']['gaori'], 'gaori/lifecycle.html', 'G12'),
        **{p: reference.route(identity, p, 'retry' if p != 'no-region.html' else None, QUERY, FRAGMENT)
           for p in ['a.html', 'b.html', 'no-region.html']},
    }
    # Declared sequential producers: each original UI starts/completes Review.
    # These are fixture intent ordinals, never reconstructed from timestamps.
    fixture['expected_review_sequences']={fixture['originals']['mulgae']['version_id']:2,fixture['originals']['gaori']['version_id']:4,identity['version_id']:6}
    return fixture


def successors(product, owner, fixture):
    before=records(product,owner,fixture['expected_trees'],extra_ids=fixture.get('known_note_ids',()))
    result = {}
    for name, old in {**fixture['originals'], 'boundary': fixture['boundary']}.items():
        root = product.root / ('owner-synthetic-successor-' + name)
        root.mkdir()
        page = {'mulgae': 'recovery/compose.html', 'gaori': 'gaori/lifecycle.html', 'boundary': 'a.html'}[name]
        destination = root / page
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_bytes(b'<!doctype html><h1>LABELLED SYNTHETIC SUCCESSOR</h1><p id="retry">Changed meaning</p><i id="retry">Ambiguous duplicate</i>')
        expected = tree(root)
        _, _, identity = product.import_tree(owner, root, extra=('--document', old['document_id'], '--base-version', old['version_id']), timeout=120)
        reference.ready(product, owner, identity['version_id'])
        assert product.data('GET', '/api/v1/versions/' + identity['version_id'] + '/review', client=owner)['value']['state'] == 'unreviewed'
        assert all_items(product, owner, '/api/v1/versions/' + identity['version_id'] + '/annotations') == []
        result[name] = identity
        fixture.setdefault('successor_trees', {})[identity['version_id']] = expected
        fixture.setdefault('successor_roots', {})[identity['version_id']] = str(root)
    fixture['successors'] = result
    fixture['successor_routes']={name:reference.route(identity,{'mulgae':'recovery/compose.html','gaori':'gaori/lifecycle.html','boundary':'a.html'}[name],'retry') for name,identity in result.items()}
    assert records(product,owner,fixture['expected_trees'],known=before)==before, 'publication silently changed original personal records'
    return result


def recovery_versions(fixture):
    """Full old/new recovery scope, separate from original-only X18 export."""
    return {**fixture['expected_trees'], **fixture.get('successor_trees', {})}


def assert_recovery_payloads(product, owner, fixture):
    roots = {**fixture['roots'], **fixture.get('successor_roots', {})}
    for version, expected in recovery_versions(fixture).items():
        assert tree(Path(roots[version])) == expected, 'recovery input changed'
        assert published_tree(product, owner, version) == expected, 'old/new recovered payload differs'
    return {'versions': sorted(recovery_versions(fixture)), 'independent_payload_inventories': True}


def context_cases(product, owner, fixture):
    """Supporting presentation inputs, after the client's own core Save/Review."""
    require_core(fixture)
    cases = [
        {'name':'empty','pattern':'','repeat':1},
        {'name':'plain-json','pattern':'{"literal":"<script>inert</script>🌊"}','repeat':1},
        *[{'name':'cut-'+str(n),'pattern':'a','repeat':n,'suffix':'🌊e\u0301\r\n🇰🇷👩‍👩‍👧‍👦'} for n in (4095,4096,4097)],
        {'name':'oversized-cluster','prefix':'x','pattern':'\u0301','repeat':5000,'suffix':'z'},
        {'name':'crlf-tabs','pattern':'first\r\n\t second  \r','repeat':3,'suffix':'\n'},
        {'name':'trailing-newline','pattern':'line\n\n','repeat':3},
        {'name':'nonprinting','pattern':'\u200b\u2067عربي\u2069\u200b','repeat':1},
        {'name':'bidi-joining','pattern':'مرحبا بالعالم אבג (abc) 👩‍👩‍👧‍👦 ','repeat':160},
        {'name':'longest-before-end','pattern':'long','repeat':4096,'suffix':'\n🌊'},
        {'name':'large-multiline','pattern':'Task6 native retained context 🌊\n','repeat':90000},
    ]
    for case in cases:
        text=case.get('prefix','')+case['pattern']*case['repeat']+case.get('suffix','')
        case['bytes']=len(text.encode());case['sha256']=hashlib.sha256(text.encode()).hexdigest()
        saved=product.data('POST','/api/v1/versions/'+fixture['boundary']['version_id']+'/annotations',client=owner,headers={'Idempotency-Key':key()},body={'page_path':'no-region.html','region_id':None,'page_query':'','page_fragment':'','body':'Task6 supporting context '+case['name'],'body_ref':None,'selected_text':None,'context':text,'context_ref':None})
        case['annotation_id']=saved['annotation_id'];fixture.setdefault('known_note_ids',[]).append(saved['annotation_id'])
        status,body,_=product.request('GET','/api/v1/annotations/'+saved['annotation_id']+'/context?revision=1',client=owner,raw=True)
        assert status==200 and body==text.encode(), 'supporting context stream differs from declared input'
    fixture['context_cases']=cases
    return {'supporting':True,'cases':cases}


def records(product, owner, versions, known=None, extra_ids=()):
    """Freeze GET data and every retained revision stream, independently of copies."""
    result = {}
    ids=set(extra_ids)
    if known:
        ids.update(note_id for row in known.values() for note_id in row['notes'])
    current_lists={version:all_items(product, owner, '/api/v1/versions/'+version+'/annotations') for version in versions}
    ids.update(note['annotation_id'] for notes in current_lists.values() for note in notes)
    notes_by_id={note_id:product.data('GET','/api/v1/annotations/'+note_id,client=owner) for note_id in ids}
    histories={note_id:all_items(product,owner,'/api/v1/annotations/'+note_id+'/history') for note_id in ids}
    for version in versions:
        prefix = '/api/v1/versions/' + version
        notes=[note for note_id,note in notes_by_id.items() if any(version in (revision['anchor']['version_id'],revision['original_anchor']['version_id']) for revision in histories[note_id])]
        assert {note['annotation_id'] for note in current_lists[version]} == {note['annotation_id'] for note in notes if not note['deleted_at'] and note['anchor']['version_id']==version}, 'live listing omitted/added a known current identity'
        document=product.data('GET',prefix,client=owner)['document_id']
        reader={kind:product.data('GET',prefix+'/'+kind,client=owner) for kind in ('reading-position','reader-preference')}
        reader['pages']={entry['path']:product.data('GET',prefix+'/page-position?'+urllib.parse.urlencode({'page':entry['path']}),client=owner) for entry in product.files(owner,version) if entry['kind']=='file' and entry['path'].endswith('.html')}
        row = {'review': product.data('GET', prefix + '/review', client=owner),
               'review_history': all_items(product, owner, prefix + '/review/history'), 'notes': {}, 'reader':reader,
               'revisit':product.data('GET','/api/v1/documents/'+document+'/revisit',client=owner),
               'references':all_items(product,owner,prefix+'/references')}
        for note in notes:
            path = '/api/v1/annotations/' + note['annotation_id']
            history = histories[note['annotation_id']]
            streams = {}
            for revision in history:
                for kind, present in [('body', revision.get('body_ref')), ('context', revision.get('context_ref'))]:
                    if present:
                        status, body, headers = product.request('GET', path + '/' + kind + '?revision=' + str(revision['revision']), client=owner, raw=True)
                        assert status == 200
                        streams[str(revision['revision']) + ':' + kind] = {'size': len(body), 'sha256': hashlib.sha256(body).hexdigest(), 'media_type':next(value for key,value in headers.items() if key.lower()=='content-type')}
            row['notes'][note['annotation_id']] = {'current': note, 'history': history, 'streams': streams}
        result[version] = row
    return result


def validate_declared_records(product,owner,fixture,client):
    """Check original intent before freezing assigned IDs/timestamps as an oracle."""
    snapshot=records(product,owner,fixture['expected_trees'],extra_ids=fixture.get('known_note_ids',()))
    prefix='Task6 '+client+' original '
    for name in ('mulgae','gaori','a.html','b.html','no-region.html'):
        identity=fixture['originals'].get(name,fixture['boundary']);version=identity['version_id']
        candidates=[note for note in snapshot[version]['notes'].values() if note['history'][0]['body']==prefix+name]
        assert len(candidates)==1, 'missing/extra original client UI Note: '+name
        note=candidates[0];original=note['history'][0];parsed=urllib.parse.urlsplit(fixture['routes'][name]);query=urllib.parse.parse_qs(parsed.query,keep_blank_values=True)
        anchor={'version_id':version,'page_path':query['page'][0],'region_id':None if name=='no-region.html' else {'mulgae':'M11','gaori':'G12'}.get(name,'retry'),'page_query':query.get('page_query',[''])[0],'page_fragment':query.get('page_fragment',[''])[0]}
        assert original['anchor']==anchor and original['original_anchor']==anchor
        assert note['current']['original_anchor']==anchor
        assert [row['revision'] for row in note['history']]==list(range(1,note['current']['revision']+1))
        if name=='a.html':
            stream=note['streams']['1:context'];assert stream['size']==len(LARGE_CONTEXT_BYTES) and stream['sha256']==hashlib.sha256(LARGE_CONTEXT_BYTES).hexdigest()
        if name in ('mulgae','gaori','a.html'):
            review=snapshot[version];assert review['review']['value']['state']=='reviewed'
            assert [(event['action'],event['old_state'],event['new_state']) for event in review['review_history']]==[('start','unreviewed','reviewing'),('complete','reviewing','reviewed')]
    large=LARGE_BODIES[client]
    whole=[note for note in snapshot[fixture['boundary']['version_id']]['notes'].values() if note['history'][0].get('body_ref') and note['history'][0]['anchor']['page_path'] is None]
    assert len(whole)==1 and whole[0]['history'][0]['anchor']=={'version_id':fixture['boundary']['version_id'],'page_path':None,'region_id':None,'page_query':'','page_fragment':''}
    assert whole[0]['streams']['1:body']['size']==len(large) and whole[0]['streams']['1:body']['sha256']==hashlib.sha256(large).hexdigest()
    fixture['declared_records_validated']=True
    return snapshot


def require_core(fixture):
    assert fixture.get('declared_records_validated'), 'BLOCKED: required own UI Save/Review/declared tuples did not pass'


def assert_after_reader_actions(before,after):
    """Reader actions legitimately write positions; all other families stay exact.

    Restoration itself must compare the complete snapshots BEFORE these actions.
    """
    assert before.keys()==after.keys()
    for version in before:
        assert {key:value for key,value in before[version].items() if key!='reader'}=={key:value for key,value in after[version].items() if key!='reader'}


def validate_written_intents(product,owner,fixture,client):
    """Declared mutation outcomes, before lifecycle/copy can freeze them."""
    snapshot=records(product,owner,fixture['expected_trees'],extra_ids=fixture.get('known_note_ids',()))
    boundary=fixture['boundary']['version_id'];notes=list(snapshot[boundary]['notes'].values())
    prefix='Task6 '+client+' original '
    original=lambda page:next(note for note in notes if note['history'][0]['body']==prefix+page)
    moved=original('b.html');assert len(moved['history'])==3
    assert moved['current']['anchor']=={'version_id':fixture['successors']['boundary']['version_id'],'page_path':'a.html','region_id':None,'page_query':'','page_fragment':''}
    assert all(row['body']==prefix+'b.html' and row['original_anchor']==moved['history'][0]['anchor'] for row in moved['history'])
    plain=original('no-region.html')
    assert [row['body'] for row in plain['history']]==[prefix+'no-region.html','Task6 native remote edit' if client=='native' else 'Task6 remote edit','Task6 native deliberately merged' if client=='native' else 'Task6 deliberately merged','Task6 '+client+' reload peer revision']
    assert plain['current']['body']=='Task6 '+client+' reload peer revision'
    assert plain['history'][0]['actor_session']!=plain['history'][1]['actor_session']
    for loss in ('before','after'):
        found=[note for note in notes if note['current']['body']=='Task6 '+client+' lost '+loss]
        assert len(found)==1 and len(found[0]['history'])==1, 'lost response duplicated/missed the original create'
    deleted=[note for note in notes if note['history'][0]['body']=='Task6 consent saved note']
    assert len(deleted)==(2 if client=='native' else 4)
    assert all(note['current']['deleted_at'] and len(note['history'])==3 for note in deleted)
    fixture['written_intents_validated']=True
    return {'identities':sorted(snapshot[boundary]['notes']),'deleted_identities':[note['current']['annotation_id'] for note in deleted],'peer_sessions_distinct':True}


def lifecycle_observation(product,owner,fixture,document,lifecycle):
    target=fixture['originals']['mulgae'];source=fixture['originals']['gaori']
    kind='target' if document==target['document_id'] else 'referrer'
    selected=target if kind=='target' else source
    if lifecycle=='trash':
        assert product.request('GET','/api/v1/versions/'+selected['version_id'],client=owner)[0]==404
        hits=all_items(product,owner,'/api/v1/search?'+urllib.parse.urlencode({'q':'failed_run_recovery' if kind=='target' else '"accepted cancellation"','lifecycle':'all','versions':'all'}))
        assert all(hit['version_id']!=selected['version_id'] for hit in hits)
        if kind=='target':
            refs=product.data('GET','/api/v1/versions/'+source['version_id']+'/references',client=owner)
            assert refs['counts']['edge_count']=='0'
            assert refs['items'] and all(item['resolution']=='unavailable' and item['target'] is None for item in refs['items'])
        else:
            backlinks=all_items(product,owner,'/api/v1/versions/'+target['version_id']+'/backlinks')
            assert all(item['from']['version_id']!=source['version_id'] for item in backlinks)
    else:
        refs=all_items(product,owner,'/api/v1/versions/'+source['version_id']+'/references')
        assert len(refs)==1 and refs[0]['id']=='prior-compose' and refs[0]['resolution']=='resolved'
        assert refs[0]['target']['version_id']==target['version_id'] and refs[0]['target']['page_path']=='recovery/compose.html' and refs[0]['target']['region_id']=='M11'
        backlinks=all_items(product,owner,'/api/v1/versions/'+target['version_id']+'/backlinks')
        assert any(item['from']['version_id']==source['version_id'] for item in backlinks)
    return {'role':kind,'lifecycle':lifecycle,'search_reference_backlink_exclusion':lifecycle=='trash','supportingPublicReads':True}


def copy_cli(product, owner, args, code=0):
    result = subprocess.run([product.binary, *args, '--data-dir', str(product.data_dir), *owner['flags']], capture_output=True, text=True, timeout=120)
    wire = json.loads(result.stdout) if result.stdout.strip() else {}
    diagnostic = None
    if result.returncode != code and hasattr(product, 'diagnostic_stderr'):
        log = Path(product.diagnostic_stderr.name)
        diagnostic = log.read_text()[-5000:]
        for client in product.clients:
            diagnostic = diagnostic.replace(client['token'], '[redacted]')
    assert result.returncode == code, {'command': args[:2], 'exit': result.returncode, 'expected': code,
                                      'error': wire.get('error'), 'daemon_exit': product.process.poll() if product.process else None,
                                      'bounded_daemon_failure_diagnostic': diagnostic}
    return wire['data'] if code == 0 else wire


def backup(product, owner, destination):
    args = ['backup', 'create', '--destination', str(destination), '--operation-id', key()]
    accepted = copy_cli(product, owner, args)['result']
    product.job(owner, accepted['job_id'], timeout=1800)
    assert copy_cli(product, owner, args)['result'] == accepted
    path = destination / accepted['set_id']
    assert product.cli(['backup', 'verify', '--input', str(path)], timeout=1800)['data']['valid']
    return path, accepted


def copy_submission(product, owner, args):
    """Explicit local lost-response reconciliation, never a fresh-key retry.

    Source: CLI submission/replay contract and R33. Keep the failed command and
    the receipt separately. Only an actual EOF is this transport-loss case;
    all other unexpected failures remain failures.
    """
    operation = key()
    original = [*args, '--operation-id', operation]
    command = [product.binary, *original, '--data-dir', str(product.data_dir), *owner['flags']]
    result = subprocess.run(command, capture_output=True, text=True, timeout=120)
    wire = json.loads(result.stdout)
    attempts = [{'exit': result.returncode, 'operation_id': operation, 'error': wire.get('error')}]
    if result.returncode == 0:
        assert wire['data']['operation_id'] == operation
        return wire['data']['result'], attempts
    assert result.returncode == 5 and wire.get('error', {}).get('code') == 'UNAVAILABLE' and wire['error']['message'].endswith(': EOF'), {'original_arguments':original,'attempts':attempts}
    # A transport error may have data:null; the explicitly supplied UUID is
    # still the caller's exact recorded identity, not a server outcome claim.
    if wire.get('data') is not None:
        assert wire['data']['operation_id'] == operation
    status, receipt, _ = product.request('GET', '/api/v1/operations/' + operation, client=owner)
    attempts.append({'action': 'original operation lookup', 'status': status, 'receipt': receipt})
    assert status == 200 and receipt['data']['state'] == 'completed', attempts
    saved = receipt['data']['result_metadata']
    assert saved['operation_id'] == operation and saved['job_id'] and saved['set_id']
    replay = subprocess.run(command, capture_output=True, text=True, timeout=120)
    replay_wire = json.loads(replay.stdout)
    attempts.append({'action': 'explicit identical original-key replay', 'exit': replay.returncode, 'operation_id': operation})
    assert replay.returncode == 0 and replay_wire['data']['result'] == saved, attempts
    return saved, attempts


def recover(product, owner, path):
    before = product.data('GET', '/api/v1/capabilities', client=owner)
    product.stop()
    plan = product.cli(['restore', 'plan', '--input', str(path)])['data']
    result = product.cli(['restore', 'apply', plan['plan_id'], '--confirm'])['data']
    assert product.cli(['restore', 'apply', plan['plan_id'], '--confirm'])['data'] == result
    product.start()
    assert product.request('GET', '/api/v1/capabilities', client=owner)[0] == 401
    fresh = product.owner('Task6 fresh recovered setup owner')
    after = product.data('GET', '/api/v1/capabilities', client=fresh)
    assert after['library_id'] == before['library_id']
    assert after['runtime_generation'] != before['runtime_generation'] and after['stream_epoch'] != before['stream_epoch']
    product.stop()
    hold = copy_cli(product, fresh, ['restore', 'review'])
    assert hold['state'] == 'pending_review'
    product.start()
    config = product.data('GET', '/api/v1/configuration', client=fresh)
    choices = product.root / ('recovery-choices-' + key() + '.json')
    choices.write_text(json.dumps({'configuration': config['configuration'], 'expected_config_revision': config['config_revision'], 'resume_cleanup_job_ids': []}))
    product.stop()
    assert copy_cli(product, fresh, ['restore', 'review', '--choices', str(choices), '--confirm'])['state'] == 'reviewed'
    product.start()
    assert product.data('GET', '/api/v1/capabilities', client=fresh)['runtime_generation'] == after['runtime_generation']
    return fresh, {'before': before, 'after': after, 'restore': result, 'bootstrap': 'supporting public setup; not native onboarding UI'}


def assert_originals(product, owner, fixture):
    for version, expected in fixture['expected_trees'].items():
        assert tree(Path(fixture['roots'][version])) == expected, 'source input changed'
        assert published_tree(product, owner, version) == expected, 'immutable publication changed'


def interrupted_backup(product, owner, generator):
    source = product.root / 'owner-f12'
    subprocess.run([str(generator), str(source)], check=True, capture_output=True, timeout=30)
    canonical = json.loads((source / 'manifest.json').read_text())
    assert canonical['archive_bytes'] == 12_768_177 and canonical['expanded_bytes'] == 55_050_821 and len(canonical['files']) == 1027
    imported = product.cli(['import', canonical['archive'], '--new-document'], client=owner, timeout=1800)['data']
    product.job(owner, imported['result']['job_id'], timeout=1800)
    destination = product.root / 'owner-interrupted-backup'
    args = ['backup', 'create', '--destination', str(destination), '--operation-id', key()]
    acceptance = copy_cli(product, owner, args)['result']
    incomplete = destination / ('.' + acceptance['set_id'] + '.incomplete')
    deadline = time.monotonic() + 60
    while True:
        job = product.data('GET', '/api/v1/jobs/' + acceptance['job_id'], client=owner)
        assert job['state'] != 'completed', 'completed-before-kill does not prove interruption'
        if job['state'] == 'running' and incomplete.is_dir():
            break
        assert time.monotonic() < deadline, 'backup never reached observable incomplete state'
        time.sleep(.01)
    process = product.process
    product.stop(kill=True)
    assert process.returncode != 0 and incomplete.is_dir() and not (destination / acceptance['set_id']).exists(), 'completed-before-kill: interruption unproved'
    checkpoint = tree(incomplete)
    product.cli(['backup', 'verify', '--input', str(incomplete)], code=2)
    product.start()
    assert copy_cli(product, owner, args)['result'] == acceptance
    current = product.data('GET', '/api/v1/jobs/' + acceptance['job_id'], client=owner)
    deadline = time.monotonic() + 60
    while current['state'] == 'interrupted':
        assert time.monotonic() < deadline, 'original accepted job did not reconcile after restart'
        time.sleep(.1)
        current = product.data('GET', '/api/v1/jobs/' + acceptance['job_id'], client=owner)
    execution = acceptance
    if current['state'] in ('failed', 'cancelled'):
        execution = copy_cli(product, owner, ['backup', 'resume', acceptance['set_id']])['result']
        assert execution['set_id'] == acceptance['set_id'] and execution['job_id'] != acceptance['job_id']
        assert product.data('GET', '/api/v1/jobs/' + acceptance['job_id'], client=owner)['state'] == current['state']
    product.job(owner, execution['job_id'], timeout=1800)
    path = destination / acceptance['set_id']
    assert product.cli(['backup', 'verify', '--input', str(path)], timeout=1800)['data']['valid']
    return path, {'acceptance': acceptance, 'observed_job': job, 'process_exit': process.returncode, 'incomplete_inventory': checkpoint, 'recovery_job': execution, 'generator_sha256':hashlib.sha256(Path(generator).read_bytes()).hexdigest(),'canonical_manifest_sha256':hashlib.sha256((source/'manifest.json').read_bytes()).hexdigest(),'boundary': 'actual process interruption; no fsync phase inferred'}


def zip_tree(archive, prefix=''):
    actual = {}
    for entry in archive.infolist():
        if not entry.filename.startswith(prefix) or entry.filename == prefix:
            continue
        name = entry.filename[len(prefix):].rstrip('/')
        assert name not in actual, 'duplicate archive entry'
        mode = entry.external_attr >> 16
        if entry.is_dir():
            actual[name] = {'kind': 'directory'}
        elif stat.S_ISLNK(mode):
            actual[name] = {'kind': 'symlink', 'link_text': archive.read(entry).decode()}
        else:
            with archive.open(entry) as stream:
                digest = hashlib.file_digest(stream, 'sha256').hexdigest()
            actual[name] = {'kind': 'file', 'byte_size': entry.file_size, 'sha256': digest}
    return actual


def export_roundtrip(product, owner, fixture):
    expected_records = records(product, owner, fixture['expected_trees'], extra_ids=fixture.get('known_note_ids',()))
    principal=owner['session']['principal_id']
    observations = []
    original=fixture['originals']['mulgae']
    lifecycle=product.data('GET','/api/v1/documents/'+original['document_id'],client=owner)['lifecycle']
    if fixture.get('declared_records_validated'):
        assert lifecycle=='archived', 'client lifecycle must restore original archived state'
        negative_key=key()
        negative=copy_cli(product,owner,['export','create','--version',original['version_id'],'--format','directory','--destination',str(product.root/'owner-default-active-negative'),'--operation-id',negative_key],code=3)
        assert negative['error']=={'code':'DENIED','message':'NOT_FOUND'}
        fixture['default_active_negative']={'operation_id':negative_key,'wire':negative,'version_id':original['version_id'],'lifecycle':lifecycle}
    for version, expected in fixture['expected_trees'].items():
        for fmt in ('directory', 'zip'):
            destination = product.root / ('owner-export-' + version + ('.zip' if fmt == 'zip' else '-directory'))
            sidecar = product.root / ('owner-metadata-' + version + '-' + fmt)
            accepted, attempts = copy_submission(product, owner, ['export', 'create', '--version', version, '--lifecycle', 'all', '--format', fmt, '--destination', str(destination), '--metadata-destination', str(sidecar), '--records', 'notes,review,references,revisit,reader'])
            product.job(owner, accepted['job_id'], timeout=1800)
            index = sidecar / 'export-index.json'
            assert product.cli(['export', 'verify', '--input', str(destination), '--index', str(index)], timeout=1800)['data']['valid']
            if fmt == 'directory':
                assert tree(destination) == expected
            else:
                with zipfile.ZipFile(destination) as archive:
                    assert zip_tree(archive) == expected
            declaration = json.loads(index.read_text())['versions'][0]
            assert declaration['version_id']==version, 'export selected another version'
            metadata_root = sidecar / declaration['metadata_root']
            portable = [json.loads(line) for line in (metadata_root / 'records.jsonl').read_text().splitlines()]
            assert all(row['kind'] in ('notes','review','references','revisit','reader','context') for row in portable)
            assert all(row['owner_id']==principal and row['version_id']==version for row in portable if row['kind']!='context')
            note_rows = [row['value'] for row in portable if row['kind'] == 'notes']
            wanted_notes = [revision for note in expected_records[version]['notes'].values() for revision in note['history']]
            assert len(note_rows)==len(wanted_notes), 'duplicate/missing portable Note revision'
            assert {(row['annotation_id'], row['revision']) for row in note_rows} == {(row['annotation_id'], row['revision']) for row in wanted_notes}
            for wanted in wanted_notes:
                exported = next(row for row in note_rows if (row['annotation_id'], row['revision']) == (wanted['annotation_id'], wanted['revision']))
                for field in ('document_id', 'anchor', 'original_anchor', 'body', 'selected_text', 'context_ref', 'created_at', 'updated_at', 'deleted_at', 'action', 'actor_session', 'operation_id'):
                    assert exported[field] == wanted[field], 'portable Note field changed: ' + field
                if wanted.get('body_ref'):
                    assert exported['body_object']['Hash'] == wanted['body_ref']['bytes_sha256']
                    assert exported['body_object']['Size'] == int(wanted['body_ref']['byte_size'])
                    body_path=metadata_root/'objects'/wanted['body_ref']['bytes_sha256']
                    with body_path.open('rb') as stream:
                        assert hashlib.file_digest(stream,'sha256').hexdigest()==wanted['body_ref']['bytes_sha256']
                    assert body_path.stat().st_size==int(wanted['body_ref']['byte_size'])
                else:
                    assert exported['body_object'] is None
                exact={field:wanted[field] for field in ('annotation_id','document_id','anchor','original_anchor','body','selected_text','context_ref','created_at','updated_at','deleted_at','revision','action','actor_session','operation_id')}
                exact['body_object']={'GCToken':'','Hash':wanted['body_ref']['bytes_sha256'],'Size':int(wanted['body_ref']['byte_size']),'State':'ready'} if wanted.get('body_ref') else None
                assert exported==exact, 'portable Note has a missing/extra/changed field'
            review_rows = [row['value'] for row in portable if row['kind'] == 'review']
            current = expected_records[version]['review']
            states = [row for row in review_rows if 'state' in row]
            assert len(states) == (1 if current['exists'] else 0)
            if current['exists']:
                assert states[0]['state'] == current['value']['state'] and states[0]['reviewed_at'] == current['value']['reviewed_at'] and states[0]['revision'] == current['revision']
                assert states[0]['owner_id']==principal and states[0]['version_id']==version
                assert states[0]['event_sequence']==fixture['expected_review_sequences'][version]
                assert states[0]=={'owner_id':principal,'version_id':version,'state':current['value']['state'],'reviewed_at':current['value']['reviewed_at'],'revision':current['revision'],'event_sequence':fixture['expected_review_sequences'][version]}
            events = expected_records[version]['review_history']
            assert len(review_rows)==len(states)+len(events), 'unexpected portable Review record'
            assert len([row for row in review_rows if 'event_id' in row])==len(events)
            assert {row['event_id'] for row in review_rows if 'event_id' in row} == {row['event_id'] for row in events}
            for event in events:
                exported = next(row for row in review_rows if row.get('event_id') == event['event_id'])
                for field in ('version_id', 'action', 'old_state', 'new_state', 'operation_id', 'created_at'):
                    assert exported[field] == event[field]
                assert exported['owner_id']==principal
                assert exported==dict(owner_id=principal,**{field:event[field] for field in ('event_id','version_id','action','old_state','new_state','operation_id','created_at')})
            revisit=expected_records[version]['revisit']
            wanted_revisit=[{'owner_id':principal,'document_id':revisit['document_id'],'flag':revisit['value']['flag'],'revision':revisit['revision']}] if revisit['exists'] else []
            assert [row['value'] for row in portable if row['kind']=='revisit']==wanted_revisit
            reader=expected_records[version]['reader'];wanted_reader=[]
            for kind in ('reading-position','reader-preference'):
                view=reader[kind]
                if view['exists']:wanted_reader.append({'kind':kind.replace('-','_'),'resource_id':principal+'/'+version,'revision':view['revision'],'value':view['value']})
            for page,view in reader['pages'].items():
                if view['exists']:wanted_reader.append({'kind':'page_position','resource_id':principal+'/'+version+'/'+page,'revision':view['revision'],'value':view['value']})
            actual_reader=[row['value'] for row in portable if row['kind']=='reader']
            canonical=lambda row:json.dumps(row,sort_keys=True,separators=(',',':'),ensure_ascii=False)
            assert sorted(map(canonical,actual_reader))==sorted(map(canonical,wanted_reader)), 'reader family missing/extra/changed'
            wanted_references=[{'from_version_id':version,'metadata_reference_id':reference['id'],'authored':reference} for reference in fixture.get('authored_references',{}).get(version,[])]
            assert sorted(map(canonical,[row['value'] for row in portable if row['kind']=='references']))==sorted(map(canonical,wanted_references)), 'authored references changed'
            contexts = [row['value'] for row in portable if row['kind'] == 'context']
            expected_contexts = {}
            for note in expected_records[version]['notes'].values():
                for revision in note['history']:
                    if revision['context_ref']:
                        expected_contexts[revision['context_ref']] = note['streams'][str(revision['revision']) + ':context']
            assert {row['context_ref'] for row in contexts} == set(expected_contexts)
            assert len(contexts)==len(expected_contexts), 'duplicate/missing portable context'
            for descriptor in contexts:
                assert descriptor['sha256'] == expected_contexts[descriptor['context_ref']]['sha256'] and descriptor['bytes'] == expected_contexts[descriptor['context_ref']]['size']
                expected_stream=expected_contexts[descriptor['context_ref']]
                assert descriptor['media_type']==expected_stream['media_type']
                anchors=[revision['anchor'] for note in expected_records[version]['notes'].values() for revision in note['history'] if revision['context_ref']==descriptor['context_ref']]
                assert any(descriptor['version_id']==anchor['version_id'] and descriptor['page_path']==anchor['page_path'] for anchor in anchors)
                with (metadata_root / 'objects' / descriptor['sha256']).open('rb') as stream:
                    assert hashlib.file_digest(stream, 'sha256').hexdigest() == descriptor['sha256']
                assert (metadata_root/'objects'/descriptor['sha256']).stat().st_size==descriptor['bytes']
            # Reimport only original payload: personal records are NOT merged.
            _, _, imported = product.import_tree(owner, destination, extra=('--new-document',), timeout=1800)
            assert published_tree(product, owner, imported['version_id']) == expected
            assert all_items(product, owner, '/api/v1/versions/' + imported['version_id'] + '/annotations') == []
            assert product.data('GET', '/api/v1/versions/' + imported['version_id'] + '/review', client=owner)['value']['state'] == 'unreviewed'
            observations.append({'version': version, 'format': fmt, 'entries': len(expected), 'reimported': imported['version_id'], 'metadata_index': json.loads(index.read_text()), 'personal_history_compared': len(wanted_notes), 'submission_attempts': attempts})
    return observations


def fingerprints():
    paths = set()
    for folder in ('cmd', 'internal', 'desktop', 'frontend/src', 'spikes/viewer/desktop'):
        paths.update(p for p in (REPO / folder).rglob('*') if p.is_file() and 'node_modules' not in p.parts and '.git' not in p.parts)
    for relative in ('tests/e2e/harness.py', 'tests/manual/owner_records_support.py', 'tests/manual/reference_support.py',
                     'tests/manual/search_corpus_support.py', 'tests/manual/notes_confirmation_cases.js', 'tests/manual/notes_context_cases.js', 'spikes/viewer/check_reader_native.py',
                     'spikes/viewer/owner_records_native.py', 'spikes/viewer/owner_records_probe.js',
                     'internal/adapters/http/auth/browser/check_owner_records.py', 'internal/adapters/http/auth/browser/check_owner_records.js',
                     'go.mod', 'go.sum', 'frontend/package.json', 'frontend/package-lock.json', 'frontend/native.html',
                     'frontend/vite.native.config.ts', 'frontend/tsconfig.json'):
        paths.add(REPO / relative)
    for relative in ('frontend/index.html','frontend/vite.config.ts','frontend/vite.loader.config.ts','frontend/check_build.py',
                     'spikes/viewer/desktop/go.mod','spikes/viewer/desktop/go.sum','.gaori/tester.yaml',
                     'docs/testing/epic-harmony-verification-profile.md'):
        paths.add(REPO/relative)
    paths.update(path for path in (REPO/'tests/fixtures/f12').rglob('*') if path.is_file())
    for name, entries in reference.CORPUS_FILES.items():
        paths.update(REPO / 'spikes/knowledge-value/samples' / name / entry for entry in entries)
    paths.update((REPO/'tests/fixtures/f12').glob('*.go'))
    paths.add(REPO/'docs/testing/two-project-knowledge.md')
    return {str(p.relative_to(REPO)): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(paths)}
