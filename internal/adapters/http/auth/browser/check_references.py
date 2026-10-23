"""RF09–RF12 actual Chrome Reader; F03/F13 seeds and explicit unchanged M11 corpus.

Explicit --corpus selects the unchanged Gaori G12 → Mulgae M11 payloads.
Browser plugin not available; the assigned installed Playwright CLI is used.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--browser-cli',type=Path,required=True)
parser.add_argument('--receipt',type=Path,required=True)
parser.add_argument('--corpus',action='store_true',help='M11 opt-in real-sample reference click/selection/focus/return')
args=parser.parse_args()
REPO=Path(__file__).resolve().parents[5]
receipt=args.receipt.resolve();assert receipt.parent.is_dir()
os.umask(0o077)
work=Path(tempfile.mkdtemp(prefix='h4r-',dir=receipt.parent)).resolve()
sockets=Path(tempfile.mkdtemp(prefix='h4r-',dir='/tmp')).resolve()
env=os.environ|{'TMPDIR':str(work),'HAERYONG_TEST_STATE_DIR':str(work/'app-state'),'PYTHONDONTWRITEBYTECODE':'1','PLAYWRIGHT_DAEMON_SOCKETS_DIR':str(sockets),'PLAYWRIGHT_DAEMON_SESSION_DIR':str(work/'registry'),'GOTOOLCHAIN':'local','GOPROXY':'off','GOSUMDB':'off'}
os.environ['HAERYONG_TEST_STATE_DIR']=env['HAERYONG_TEST_STATE_DIR']
def load(name,path):
    spec=importlib.util.spec_from_file_location(name,path);module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module
h=load('h',REPO/'tests/e2e/harness.py')
support=load('reference_support',REPO/'tests/manual/reference_support.py')
proof={'scope':__doc__,'complete':False,'runtime':str(work),'cases':[],'command':['python3','-B',str(Path(__file__).relative_to(REPO)),'--browser-cli',str(args.browser_cli),'--receipt','<private>']+(['--corpus'] if args.corpus else [])}
product=None;opened=False
def fingerprints():
    paths=[p for folder in ['cmd','internal'] for p in (REPO/folder).rglob('*.go') if not p.name.endswith('_test.go')]
    paths += [p for p in (REPO/'frontend/src').rglob('*') if p.is_file()]
    paths += [p for p in (REPO/'internal/webassets/dist').rglob('*') if p.is_file()]
    paths += [Path(__file__),Path(__file__).with_suffix('.js'),REPO/'tests/manual/reference_support.py',REPO/'go.mod',REPO/'go.sum']
    return {str(p.relative_to(REPO)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(paths)}
def call(arguments,result=False):
    run=subprocess.run([str(args.browser_cli.resolve(strict=True)),'--session='+work.name,*arguments],cwd=work,env=env,capture_output=True,text=True,timeout=600)
    if run.returncode or '### Error' in run.stdout:
        diagnostic=run.stdout.split('### Error',1)[-1] if '### Error' in run.stdout else run.stderr
        proof['browser_error']=diagnostic.replace(owner['token'],'[redacted]')[:4000]
        raise AssertionError('browser operation failed; see private receipt')
    if result:
        match=re.search(r'### Result\s*\n',run.stdout);assert match
        return json.JSONDecoder().raw_decode(run.stdout[match.end():].lstrip())[0]
before=fingerprints()
try:
    subprocess.run(['go','build','-tags','haeryong_test_state','-o',str(work/'haeryong'),'./cmd/haeryong'],cwd=REPO,env=env,capture_output=True,check=True,timeout=180)
    assert fingerprints()==before,'inputs changed during build'
    product=h.Product(work/'haeryong',work);product.initialize();product.start();owner=product.owner('Reference browser proof')
    fixture=support.prepare(product,owner,args.corpus);proof['fixture']=fixture
    capabilities=product.data('GET','/api/v1/capabilities',client=owner)
    assert capabilities['features']['references'] is True,'full-delivery flag is not enabled'
    proof['production_capability']=True
    call(['open',product.endpoint+'/health/live','--browser','chrome','--profile',str(work/'profile')]);opened=True
    constants=fixture|{'base':product.endpoint,'token':owner['token'],'work':str(work),'capabilities':capabilities}
    script=Path(__file__).with_suffix('.js').read_text().replace('REFERENCE_CONSTANTS',json.dumps(constants,ensure_ascii=False))
    observed=call(['run-code',script],True);proof.update(observed)
    assert fingerprints()==before,'inputs changed during observations'
    proof['inputs']=before
    proof['input_sha256']=hashlib.sha256(json.dumps(before,sort_keys=True).encode()).hexdigest()
    proof['binary_sha256']=hashlib.sha256((work/'haeryong').read_bytes()).hexdigest()
    proof['complete']=bool(proof['cases']) and all(c['ok'] for c in proof['cases'])
    assert proof['complete'],'RF row failed; every attempted row retained in receipt'
finally:
    if opened:
        try:call(['close'])
        except Exception:pass
    if product:product.close()
    proof['daemon_closed']=product is None or product.process is None
    receipt.write_text(json.dumps(proof,ensure_ascii=False,indent=2)+'\n');receipt.chmod(0o600)
    shutil.rmtree(sockets,ignore_errors=True)
print(json.dumps({'complete':proof['complete'],'cases':len(proof['cases']),'receipt':str(receipt)}))
