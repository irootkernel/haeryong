"""SC19 checked-in real corpus through public production import, file, job, HTTP and CLI APIs.

Expectations are parsed from the canonical worksheet and frozen with every
source byte/path before the first search. Receipts contain private runtime IDs;
tracked records use project/page/region aliases only. No sibling repositories,
provider calls, owner-authored queries or OS cache flushing are involved.
"""
import argparse
from contextlib import contextmanager
import hashlib
import importlib.util
import json
import os
import shutil
import sys
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.parse

REPO = Path(__file__).resolve().parents[2]
WORKSHEET = REPO / 'docs/testing/search-relevance-worksheet.md'
ALIASES = {'M-I': ('mulgae', 'recovery/inventory.html'), 'M-C': ('mulgae', 'recovery/compose.html'),
           'G-O': ('gaori', 'gaori/outcomes.html'), 'G-L': ('gaori', 'gaori/lifecycle.html')}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def worksheet():
    rows = []
    canonical = WORKSHEET.read_text().split("### 9.3 Agent-predeclared real sample queries", 1)[1].split("Additional R checks:", 1)[0]
    for line in canonical.splitlines():
        if not line.startswith('| RC'):
            continue
        identity, query, expected, location = [part.strip() for part in line.split('|')[1:-1]]
        alias, regions = expected.split('/', 1)
        project, page = ALIASES[alias]
        rows.append({'id': identity, 'q': query, 'project': project, 'page': page,
                     'regions': regions.split(' or '), 'max_rank': 5, 'location': location})
    assert [r['id'] for r in rows] == [f'RC{n:02}' for n in range(1, 15)], 'canonical worksheet rows changed'
    return rows


def tree(root):
    return {str(p.relative_to(root)): {'bytes': p.stat().st_size, 'sha256': sha(p.read_bytes())}
            for p in sorted(root.rglob('*')) if p.is_file()}


def tuples(page):
    return [[h['project_id'], h['document_id'], h['version_id'], h['kind'], h['page_path'], h['region_id'], h['source_id']]
            for h in page['items']]


def fingerprints():
    paths = [p for folder in ('cmd', 'internal') for p in (REPO / folder).rglob('*.go') if not p.name.endswith('_test.go')]
    paths += [REPO / 'go.mod', REPO / 'go.sum'] + [p for p in (REPO / 'internal/webassets/dist').rglob('*') if p.is_file()]
    return {str(p.relative_to(REPO)): sha(p.read_bytes()) for p in sorted(paths)}


def run(receipt):
    receipt = receipt.resolve()
    assert receipt.parent.is_dir(), 'receipt parent must exist'
    os.umask(0o077)
    work = Path(tempfile.mkdtemp(prefix='sc19-', dir=receipt.parent)).resolve()
    env = os.environ | {'TMPDIR': str(work), 'HAERYONG_TEST_STATE_DIR': str(work / 'app-state'), 'PYTHONDONTWRITEBYTECODE': '1'}
    os.environ['HAERYONG_TEST_STATE_DIR'] = env['HAERYONG_TEST_STATE_DIR']
    spec = importlib.util.spec_from_file_location('corpus_harness', REPO / 'tests/e2e/harness.py')
    harness = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(harness)
    sources = {}
    for name in ('mulgae', 'gaori'):
        destination = work / ('checked-in-' + name)
        selected = subprocess.check_output(['git','ls-files','-z','--','spikes/knowledge-value/samples/'+name],cwd=REPO).decode().split('\0')
        original_root = REPO/'spikes/knowledge-value/samples'/name
        for path in selected:
            if not path:continue
            source=REPO/path;target=destination/source.relative_to(original_root)
            target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(source.read_bytes())
        sources[name]=destination
    frozen = {'rows': worksheet(), 'trees': {name: tree(sources[name]) for name in ('mulgae', 'gaori')},
              'aliases': ALIASES, 'additional': {'cross_project': ['RC02', 'RC08', 'RC12'], 'pages': list(ALIASES),
              'attachment': ['mulgae', 'assets/recovery.js'],
              'seeded_source': {'id': 'seeded-source', 'kind': 'git', 'repo_id': '00000000-0000-4000-8000-000000000019', 'object_format': 'sha1', 'exact_revision': 'a'*40, 'retention_mode': 'reference_only'},
              'discrimination': ['remove-M03', 'wrong-version', 'cross-page-region']}}
    frozen['sha256'] = sha(json.dumps(frozen, sort_keys=True).encode())
    proof = {'scope': __doc__, 'runtime': str(work), 'complete': False, 'frozen_before_first_search': frozen,
             'driver_sha256': sha(Path(__file__).read_bytes()), 'rows': [], 'imports': {}, 'roundtrips': {}, 'latency': [], 'misses': [], 'reversals': []}
    inputs = fingerprints()
    product = None

    def save():
        receipt.write_text(json.dumps(proof, ensure_ascii=False, indent=2) + '\n')
        receipt.chmod(0o600)

    def complete(owner, version):
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            jobs, cursor = [], None
            while True:
                page = product.data('GET', '/api/v1/jobs' + ('?' + urllib.parse.urlencode({'cursor': cursor}) if cursor else ''), client=owner)
                jobs += page['items']
                cursor = page.get('next_cursor')
                if not cursor:
                    break
            derived = [j for j in jobs if j['kind'] == 'derived_index' and j.get('result', {}).get('version_id') == version]
            if derived:
                assert len(derived) == 1
                job = product.data('GET', '/api/v1/jobs/' + derived[0]['job_id'], client=owner)
                assert job['state'] not in ('failed', 'cancelled', 'interrupted'), job['state']
                if job['state'] == 'completed':
                    assert job['result']['stage'] == 'complete' and job['result']['failed'] == '0'
                    return job
            time.sleep(.1)
        raise AssertionError('explicit public index completion deadline')

    @contextmanager
    def check(name):
        try:
            yield
        except Exception as error:
            proof['misses'].append({'id':name,'error':str(error)})
            save()

    def perform_query(owner, case, filters, expected, maximum, phase, required=True):
        started = time.monotonic()
        page = product.data('GET', '/api/v1/search?' + urllib.parse.urlencode(filters), client=owner)
        elapsed = (time.monotonic() - started) * 1000
        command = ['search', '--query', filters['q']]
        for key in ('project_id', 'lifecycle', 'versions', 'kind'):
            if key in filters:
                command += ['--project' if key == 'project_id' else '--' + key, filters[key]]
        cli = product.cli(command, client=owner)['data']
        hits = tuples(page)
        rank = next((i for i, hit in enumerate(page['items'], 1) if expected(hit)), None)
        row = {'id': case, 'phase': phase, 'filters': filters, 'ordered_tuples': hits, 'http_cli_equal': hits == tuples(cli),
               'page_equal': page == cli, 'items': page['items'], 'first_relevant_rank': rank, 'index': page['index'], 'total_matches': page['total_matches'], 'latency_ms': elapsed}
        proof['rows'].append(row)
        proof['latency'].append({'id': case, 'phase': phase, 'http_ms': elapsed})
        save()
        row['pass'] = page == cli and rank is not None and rank <= maximum and page['index']['state'] == 'complete'
        if required and not row['pass']:
            proof['misses'].append({'id': case, 'phase': phase, 'rank': rank, 'ordered_tuples': hits})
        save()
        return row

    def query(owner, case, filters, expected, maximum, phase, required=True):
        try:
            return perform_query(owner, case, filters, expected, maximum, phase, required)
        except Exception as error:
            failure={'id':case,'phase':phase,'filters':filters,'error':str(error),'first_relevant_rank':None,'items':[],'ordered_tuples':[],'pass':False}
            proof['rows'].append(failure)
            proof['misses'].append(failure)
            save()
            return failure

    try:
        # Persist the immutable expectations before importing or searching.
        save()
        subprocess.run(['go', 'build', '-tags', 'haeryong_test_state', '-o', str(work / 'haeryong'), './cmd/haeryong'], cwd=REPO, env=env, check=True, capture_output=True, timeout=180)
        product = harness.Product(work / 'haeryong', work)
        product.initialize()
        product.start()
        owner = product.owner('SC19 real sample evidence')
        for name in ('mulgae', 'gaori'):
            project = product.cli(['projects', 'register', '--name', 'SC19 ' + name], client=owner)['data']['result']['project_id']
            root = sources[name]
            _, _, published = product.import_tree(owner, root, extra=['--project', project], timeout=120)
            published['project_id'] = project
            proof['imports'][name] = published
            inventory = product.files(owner, published['version_id'])
            assert sorted(entry['path'] for entry in inventory if entry['kind'] == 'file') == sorted(frozen['trees'][name]), 'complete source paths lost'
            actual = {}
            for path, expectation in frozen['trees'][name].items():
                status, body, _ = product.request('GET', f"/api/v1/versions/{published['version_id']}/file?" + urllib.parse.urlencode({'path': path}), client=owner, raw=True)
                assert status == 200 and sha(body) == expectation['sha256'] and len(body) == expectation['bytes'], (name, path, 'byte roundtrip failed')
                actual[path] = {'sha256': sha(body), 'bytes': len(body)}
            proof['roundtrips'][name] = actual
            proof['imports'][name]['index_job'] = complete(owner, published['version_id'])
            save()
        assert frozen['trees'] == {name: tree(sources[name]) for name in ('mulgae', 'gaori')}
        # No search precedes this point; the receipt's expectations are frozen.
        for phase in ('initial', 'cold-service', 'warm-1', 'warm-2'):
            if phase == 'cold-service':
                product.stop()
                product.start()
            for row in frozen['rows']:
                target = proof['imports'][row['project']]
                filters = {'q': row['q'], 'project_id': target['project_id'], 'lifecycle': 'active', 'versions': 'latest', 'kind': 'region'}
                query(owner, row['id'], filters, lambda hit, row=row, target=target: hit['document_id'] == target['document_id'] and hit['version_id'] == target['version_id'] and hit['page_path'] == row['page'] and hit['region_id'] in row['regions'], row['max_rank'], phase)
        for identity in frozen['additional']['cross_project']:
            row = next(r for r in frozen['rows'] if r['id'] == identity)
            target = proof['imports'][row['project']]
            query(owner, identity + '-cross-project', {'q': row['q'], 'lifecycle': 'active', 'versions': 'latest'}, lambda hit, row=row, target=target: hit['document_id'] == target['document_id'] and hit['version_id'] == target['version_id'] and hit['page_path'] == row['page'] and hit['region_id'] in row['regions'], 5, 'additional')
        for alias, (name, path) in ALIASES.items():
            target = proof['imports'][name]
            query(owner, alias + '-exact-page', {'q': path, 'project_id': target['project_id'], 'lifecycle': 'active', 'versions': 'latest', 'kind': 'page'}, lambda hit, target=target, path=path: hit['document_id'] == target['document_id'] and hit['version_id'] == target['version_id'] and hit['page_path'] == path, 1, 'additional')
        target = proof['imports']['mulgae']
        query(owner, 'opaque-attachment', {'q': 'assets/recovery.js', 'project_id': target['project_id'], 'lifecycle': 'active', 'versions': 'latest', 'kind': 'attachment'}, lambda hit: hit['document_id'] == target['document_id'] and hit['version_id'] == target['version_id'] and hit['page_path'] == 'assets/recovery.js', 1, 'additional')
        with check('seeded-logical-source-setup'):
            # Optional logical source metadata is a separate labelled seeded import;
            # the two worksheet document/version trees remain untouched.
            seeded_project = product.cli(['projects','register','--name','SC19 seeded source'],client=owner)['data']['result']['project_id']
            metadata = work / 'seeded-source.json'
            source_seed = frozen['additional']['seeded_source']
            metadata.write_text(json.dumps({'schema_version':'1.0','sources':[source_seed]}, separators=(',', ':')))
            _,_,seeded = product.import_tree(owner,sources['mulgae'],extra=['--project',seeded_project,'--metadata',str(metadata)],timeout=120)
            complete(owner,seeded['version_id'])
            query(owner,'seeded-logical-source',{'q':source_seed['repo_id'],'project_id':seeded_project,'lifecycle':'active','versions':'latest','kind':'source'},lambda hit:hit['document_id']==seeded['document_id'] and hit['version_id']==seeded['version_id'] and hit['source_id']==source_seed['id'],1,'seeded-source-metadata')
            proof['seeded_source']={'publication':seeded,'label':'seeded imported logical identity; no source verification result'}

        with check('SC19-remove-M03-control'):
            # SC19 temporary-copy controls. A missing named region must fail THAT
            # copied version's target; an old original UUID is never used as an oracle
            # for a new import. Restore the entire copy path/byte map afterwards.
            copied=work/'remove-region-copy'
            shutil.copytree(sources['mulgae'],copied)
            original=tree(copied);page=copied/'recovery/inventory.html';saved=page.read_bytes()
            try:
                assert b"id='M03'" in saved
                page.write_bytes(saved.replace(b"id='M03'",b"id='removed-M03'",1))
                isolated=product.cli(['projects','register','--name','SC19 negative copies'],client=owner)['data']['result']['project_id']
                _,_,negative=product.import_tree(owner,copied,extra=['--project',isolated],timeout=120)
                complete(owner,negative['version_id'])
                row=query(owner,'SC19-remove-M03',{'q':'"accepted role"','project_id':isolated,'lifecycle':'active','versions':'latest','kind':'region'},lambda hit:hit['document_id']==negative['document_id'] and hit['version_id']==negative['version_id'] and hit['page_path']=='recovery/inventory.html' and hit['region_id'] in ['M03','M02'],5,'negative-copy',required=False)
                assert row['first_relevant_rank'] is None,'removed named region unexpectedly passed'
                proof['reversals'].append({'name':'SC19-remove-M03','expected_failure':True,'ordered_tuples':row['ordered_tuples']})
            finally:
                page.write_bytes(saved)
                assert tree(copied)==original,'temporary source copy was not restored exactly'
        with check('SC19-identity-controls'):
            _,_,old=product.import_tree(owner,copied,extra=['--project',isolated],timeout=120)
            complete(owner,old['version_id'])
            actual=query(owner,'SC19-original-identity',{'q':'failed_run_recovery','project_id':isolated,'lifecycle':'active','versions':'all','kind':'region'},lambda hit:hit['document_id']==old['document_id'] and hit['version_id']==old['version_id'] and hit['page_path']=='recovery/inventory.html' and hit['region_id']=='M01',200,'identity-control')
            hit=next(h for h in actual['items'] if h['version_id']==old['version_id'] and h['region_id']=='M01')
            _,_,newer=product.import_tree(owner,copied,extra=['--project',isolated,'--document',old['document_id']],timeout=120)
            complete(owner,newer['version_id'])
            identity=lambda candidate,version,path,region: candidate['document_id']==old['document_id'] and candidate['version_id']==version and candidate['page_path']==path and candidate['region_id']==region
            assert identity(hit,old['version_id'],'recovery/inventory.html','M01')
            assert not identity(hit,newer['version_id'],'recovery/inventory.html','M01')
            assert not identity(hit,old['version_id'],'recovery/compose.html','M01')
            proof['reversals'] += [{'name':'SC19-wrong-latest-version','expected_failure':True},{'name':'SC19-cross-page-region','expected_failure':True}]
            assert tree(copied)==original
            assert frozen['trees']=={name:tree(sources[name]) for name in ('mulgae','gaori')}

        with check('SC19-variant-runner'):
            # Explicit manual-only exhaustive evidence uses the production import
            # service/worker over these same checked-in trees. No product hook.
            variant_receipt=work/'variants.json'
            variant_command=['go','tool','ginkgo','--race','--no-color','--fail-on-empty','--fail-on-pending','--timeout=1000s','--label-filter=manual_sc19','--focus=SC19 real sample exhaustive variants','--output-dir='+str(work),'--json-report=variants.json','./internal/application/import']
            with (work/'variants.log').open('w') as log:
                variants=subprocess.run(variant_command,cwd=REPO,env=env,stdout=log,stderr=subprocess.STDOUT)
            proof['variants']={'exit':variants.returncode,'label':'manual_sc19','canonical_layers':False,'report':str(variant_receipt),'spec_sha256':sha((REPO/'internal/application/import/search_corpus_test.go').read_bytes())}
            if variants.returncode:proof['misses'].append({'id':'SC19-seeded-variants','report':str(variant_receipt)})
        for label,command in [
            ('chrome',['python3','-B','internal/adapters/http/auth/browser/check_search.py','--corpus','--browser-cli','/Users/draccoon/.nvm/versions/node/v26.7.0/bin/playwright-cli','--receipt',str(work/'chrome-corpus.json')]),
            ('native',['python3','-B','spikes/viewer/check_reader_native.py','--stages','app,corpus','--receipt',str(work/'native-corpus.json')]),
        ]:
            with (work/(label+'-corpus.log')).open('w') as log:
                result=subprocess.run(command,cwd=REPO,env=env,stdout=log,stderr=subprocess.STDOUT)
            proof.setdefault('clients',{})[label]={'exit':result.returncode,'receipt':str(work/(label+'-corpus.json'))}
            if result.returncode:proof['misses'].append({'id':label+'-corpus','exit':result.returncode})
        assert fingerprints() == inputs, 'candidate product inputs changed'


        proof['input_sha256'] = sha(json.dumps(inputs, sort_keys=True).encode())
        proof['binary_sha256'] = sha((work / 'haeryong').read_bytes())
        proof['complete'] = not proof['misses']
    except BaseException as error:
        proof['failure'] = {'type': type(error).__name__, 'detail': str(error)}
        raise
    finally:
        if product:
            product.close()
        proof['daemon_closed'] = product is None or product.process is None
        save()
    print(json.dumps({'complete': proof['complete'], 'rows': len(proof['rows']), 'misses': proof['misses'], 'receipt': str(receipt)}))
    if proof['misses']:
        raise SystemExit(1)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--receipt', type=Path, required=True)
    run(parser.parse_args().receipt)
