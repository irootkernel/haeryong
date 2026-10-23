"""RF12 unchanged real-sample worksheet and manual_references seeded remainder.

Real imports use public production APIs; the explicitly seeded matrix uses the
existing SQLite test fixture and production query/auth/HTTP/CLI paths. Every
predeclared worksheet/matrix row is retained before a failing exit. No provider,
sibling repository or ambient Git selection is involved.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import urllib.parse

REPO=Path(__file__).resolve().parents[2]


def sha(data):return hashlib.sha256(data).hexdigest()


def load(name,path):
    spec=importlib.util.spec_from_file_location(name,path)
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module


def fingerprints():
    paths=[p for folder in ('cmd','internal') for p in (REPO/folder).rglob('*.go')]
    paths += [REPO/'go.mod',REPO/'go.sum',Path(__file__),REPO/'tests/manual/reference_support.py']
    paths += [p for p in (REPO/'internal/webassets/dist').rglob('*') if p.is_file()]
    support=load('reference_files',REPO/'tests/manual/reference_support.py')
    paths += [REPO/'spikes/knowledge-value/samples'/name/p for name,items in support.CORPUS_FILES.items() for p in items]
    return {str(p.relative_to(REPO)):sha(p.read_bytes()) for p in sorted(set(paths))}


def run(receipt):
    receipt=receipt.resolve();assert receipt.parent.is_dir();os.umask(0o077)
    work=Path(tempfile.mkdtemp(prefix='reference-corpus-',dir=receipt.parent)).resolve()
    env=os.environ|{'TMPDIR':str(work),'HAERYONG_TEST_STATE_DIR':str(work/'app-state'),'PYTHONDONTWRITEBYTECODE':'1','GOTOOLCHAIN':'local','GOPROXY':'off','GOSUMDB':'off','GOFLAGS':'-mod=readonly'}
    os.environ['HAERYONG_TEST_STATE_DIR']=env['HAERYONG_TEST_STATE_DIR']
    before=fingerprints()
    names=['RF12 list exact old pin','RF12 resolve exact page and region','RF12 HEAD list and resolver','RF12 CLI list and resolver parity','RF12 successor no retarget','RF12 imported file sets and verbatim payloads']
    proof={'scope':__doc__,'complete':False,'rows':[{'id':n,'status':'not-run'} for n in names],'misses':[],
           'driver_sha256':sha(Path(__file__).read_bytes()),'inputs':before,'input_sha256':sha(json.dumps(before,sort_keys=True).encode()),
           'commands':[['python3','-B','tests/manual/check_reference_corpus.py','--receipt','<private>']],
           'labels':{'worksheet':'real checked-in sample, original bytes','matrix':'seeded manual_references; no production lifecycle/grant workflow'}}
    product=None
    def redact(value):
        if isinstance(value,dict):return {k:redact(v) for k,v in value.items()}
        if isinstance(value,list):return [redact(v) for v in value]
        if isinstance(value,str):
            value=value.replace(str(work),'<private>').replace(str(REPO),'<repository>')
            for token in secrets:
                if token:value=value.replace(token,'[redacted]')
        return value
    secrets=[]
    def save():receipt.write_text(json.dumps(redact(proof),ensure_ascii=False,indent=2)+'\n');receipt.chmod(0o600)
    save()
    try:
        subprocess.run(['go','build','-tags','haeryong_test_state','-o',str(work/'haeryong'),'./cmd/haeryong'],cwd=REPO,env=env,capture_output=True,check=True,timeout=180)
        proof['binary_sha256']=sha((work/'haeryong').read_bytes())
        h=load('reference_corpus_harness',REPO/'tests/e2e/harness.py');support=load('reference_corpus_support',REPO/'tests/manual/reference_support.py')
        product=h.Product(work/'haeryong',work);product.initialize();product.start();owner=product.owner('RF12 real sample worksheet');secrets.append(owner['token'])
        fixture=support.prepare_corpus(product,owner);proof['mapping']=fixture
        source=fixture['source']['version_id'];base='/api/v1/versions/'+source+'/references';params=urllib.parse.urlencode({'reference_id':'prior-compose','page_path':fixture['source_page'],'region_id':'G12'})
        expected=fixture['expected_target'];source_tuple=fixture['expected_source']
        def target(row):
            actual=row['target'];assert row['resolution']=='resolved' and row['reader_url']==fixture['target_route']
            assert actual['document_id']==expected['document_id'] and actual['version_id']==expected['version_id'] and actual['page_path']==expected['page_path'] and actual['region_id']==expected['region_id']
            return actual
        for row in proof['rows']:
            row['expected_source']=source_tuple;row['expected_target']=expected
            try:
                if row['id']==names[0]:
                    page=product.data('GET',base,client=owner);assert page['materialization']['state']=='ready' and page['next_cursor'] is None and len(page['items'])==1
                    row['actual_target']=target(page['items'][0]);row['actual_source']=page['items'][0]['from']
                elif row['id']==names[1]:
                    value=product.data('GET',base+'/resolve?'+params,client=owner);row['actual_target']=target(value);row['actual_source']=value['from']
                elif row['id']==names[2]:
                    values=[]
                    for path in [base,base+'/resolve?'+params]:
                        status,body,headers=product.request('HEAD',path,client=owner,raw=True);assert status==200 and body==b'' and int(headers['Content-Length'])>0;values.append({'status':status,'body_bytes':len(body)})
                    row['actual']=values
                elif row['id']==names[3]:
                    http_list=product.data('GET',base,client=owner);http_resolve=product.data('GET',base+'/resolve?'+params,client=owner)
                    listed=product.cli(['references','list','--version',source],client=owner)['data'];resolved=product.cli(['references','resolve','--version',source,'--reference-id','prior-compose','--page',fixture['source_page'],'--region','G12'],client=owner)['data']
                    assert listed==http_list and resolved==http_resolve;row['actual_target']=target(resolved);row['actual']={'list_parity':True,'resolve_parity':True}
                elif row['id']==names[4]:
                    value=product.data('GET',base+'/resolve?'+params,client=owner);row['actual_target']=target(value)
                    assert value['target']['version_id']!=fixture['latest']['version_id'];row['actual']={'successor_label':fixture['successor_label'],'newer_target_not_selected':True}
                    frozen=json.dumps(expected,sort_keys=True);controls=[]
                    for field,reversed_value in [('version_id',fixture['latest']['version_id']),('page_path','recovery/inventory.html'),('region_id','M01')]:
                        original=expected[field]
                        try:
                            expected[field]=reversed_value
                            try:target(value)
                            except AssertionError:controls.append({'field':field,'native_assertion_failed_as_expected':True})
                            else:raise AssertionError('RF12 tuple control did not discriminate '+field)
                        finally:expected[field]=original
                        assert json.dumps(expected,sort_keys=True)==frozen
                        target(value)
                    row['oracle_controls']=controls;row['expected_tuple_restored_byte_identically']=True
                else:
                    for name,paths in support.CORPUS_FILES.items():assert sorted(fixture['tree_hashes'][name])==sorted(paths) and len(paths)==4
                    row['actual']=fixture['tree_hashes'];row['byte_identity_asserted_during_import']=True
                row['status']='pass'
            except Exception as error:row['status']='fail';row['miss']=str(error);proof['misses'].append(row['id'])
            save()
        matrix=work/'matrix.json'
        command=['go','tool','ginkgo','--race','--no-color','--fail-on-empty','--fail-on-pending','--timeout=1000s','--label-filter=manual_references','--output-dir='+str(work),'--json-report=matrix-ginkgo.json','./internal/adapters/sqlite']
        proof['commands'].append([arg.replace(str(work),'<private>') for arg in command]);save()
        with (work/'matrix.log').open('w') as log:
            result=subprocess.run(command,cwd=REPO,env=env|{'HAERYONG_REFERENCE_MATRIX_BINARY':str(work/'haeryong'),'HAERYONG_REFERENCE_MATRIX_RECEIPT':str(matrix)},stdout=log,stderr=subprocess.STDOUT)
        proof['matrix_exit']=result.returncode
        if matrix.exists():
            proof['matrix']=json.loads(matrix.read_text())
            for row in proof['matrix']['rows']:
                if row['status']!='pass':proof['misses'].append('matrix:'+row['id'])
            assert proof['matrix']['predeclared_rows']==len(proof['matrix']['rows'])
        else:proof['misses'].append('matrix:setup-or-compilation-failure')
        if result.returncode and not any(m.startswith('matrix:') for m in proof['misses']):proof['misses'].append('matrix:existing-manual-variant-or-runner-failure')
        assert fingerprints()==before,'source-tree inputs changed during evidence'
        proof['complete']=not proof['misses']
    except BaseException as error:
        proof['failure']={'type':type(error).__name__,'detail':str(error)};raise
    finally:
        if product:product.close()
        proof['daemon_closed']=product is None or product.process is None;save()
    print(json.dumps({'complete':proof['complete'],'worksheet_rows':len(proof['rows']),'matrix_rows':len(proof.get('matrix',{}).get('rows',[])),'misses':proof['misses']}))
    if not proof['complete']:raise SystemExit(1)


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--receipt',type=Path,required=True);run(parser.parse_args().receipt)
