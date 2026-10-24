"""BL actual Chrome Links tab: keyboard, exact navigation/return and narrow touch.
Browser plugin not available; use the assigned installed Playwright CLI.
Projection-owned error/tombstone states are explicitly separate from product rows.
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
parser.add_argument('--versions',action='store_true',help='T5 deep links and actual S05 comparison/import')
args=parser.parse_args()
REPO=Path(__file__).resolve().parents[5]
receipt=args.receipt.resolve();assert receipt.parent.is_dir()
os.umask(0o077)
work=Path(tempfile.mkdtemp(prefix='haeryong-links-',dir='/tmp')).resolve()
sockets=Path(tempfile.mkdtemp(prefix='h4r-',dir='/tmp')).resolve()
env=os.environ|{'TMPDIR':str(work),'HAERYONG_TEST_STATE_DIR':str(work/'app-state'),'PYTHONDONTWRITEBYTECODE':'1','PLAYWRIGHT_DAEMON_SOCKETS_DIR':str(sockets),'PLAYWRIGHT_DAEMON_SESSION_DIR':str(work/'registry'),'GOTOOLCHAIN':'local','GOPROXY':'off','GOSUMDB':'off'}
os.environ['HAERYONG_TEST_STATE_DIR']=env['HAERYONG_TEST_STATE_DIR']
def load(name,path):
    spec=importlib.util.spec_from_file_location(name,path);module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module
h=load('h',REPO/'tests/e2e/harness.py')
support=load('reference_support',REPO/'tests/manual/reference_support.py')
proof={'scope':__doc__,'complete':False,'runtime':str(work),'cases':[],'command':['python3','-B',str(Path(__file__).relative_to(REPO)),'--browser-cli',str(args.browser_cli),'--receipt','<private>']+(['--corpus'] if args.corpus else [])+(['--versions'] if args.versions else [])}
product=None;opened=False;competing_server=None
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
    fixture=support.prepare(product,owner,args.corpus)
    if args.versions:
        fixture['versions']=support.prepare_versions(product,owner)
        (work/'third.html').write_text('<h1>UI imported third version</h1>')
        # A nonce-scoped, loopback test hook runs one REAL competing CLI
        # publication while the UI's first POST is held. No product API added.
        import threading,uuid
        from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
        hook='/compete-'+uuid.uuid4().hex
        class CompetingWriter(BaseHTTPRequestHandler):
            def log_message(self,*args):pass
            def do_GET(self):
                if self.path!=hook:
                    self.send_response(404);self.end_headers();return
                if 'competing_import' not in proof:
                    identity,_=support.publish(product,owner,'versions-competing-writer',{'winner.html':'<h1>Concurrent published version</h1>'},extra=['--document',fixture['versions']['old']['document_id']])
                    proof['competing_import']=identity
                body=json.dumps(proof['competing_import']).encode()
                self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
        competing_server=ThreadingHTTPServer(('127.0.0.1',0),CompetingWriter)
        threading.Thread(target=competing_server.serve_forever,daemon=True).start()
        fixture['competing_url']=f'http://127.0.0.1:{competing_server.server_port}'+hook
    proof['fixture']=fixture
    capabilities=product.data('GET','/api/v1/capabilities',client=owner)
    assert capabilities['features']['backlinks'] is True,'full-delivery flag is not enabled'
    proof['production_capability']=True
    call(['open',product.endpoint+'/health/live','--browser','chrome','--profile',str(work/'profile')]);opened=True
    constants=fixture|{'base':product.endpoint,'token':owner['token'],'work':str(work),'capabilities':capabilities}
    script=Path(__file__).with_suffix('.js').read_text().replace('LINKS_CONSTANTS',json.dumps(constants,ensure_ascii=False))
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
    if competing_server:competing_server.shutdown();competing_server.server_close()
    if product:product.close()
    proof['daemon_closed']=product is None or product.process is None
    receipt.write_text(json.dumps(proof,ensure_ascii=False,indent=2)+'\n');receipt.chmod(0o600)
    shutil.rmtree(sockets,ignore_errors=True)
print(json.dumps({'complete':proof['complete'],'cases':len(proof['cases']),'receipt':str(receipt)}))
