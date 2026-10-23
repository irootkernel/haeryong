"""RF09–RF11 native stage support; explicit local capability/fault projections.

The normal enabled path forwards the real Reader/facade/resolver. Only absent,
false, 403/410 and delayed-loss responses are test-owned seeds. No storage edits.
"""
import ctypes
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
from pathlib import Path
import threading
import time
import urllib.error
import urllib.parse
import urllib.request


def run(repo, work, state, product, owner, launch, proof):
    spec=importlib.util.spec_from_file_location('reference_support',repo/'tests/manual/reference_support.py')
    support=importlib.util.module_from_spec(spec);spec.loader.exec_module(support)
    fixture=support.prepare(product,owner)
    proof['reference_fixture']=fixture
    script_path=Path(__file__).with_name('reference_probe.js')
    source_hash=hashlib.sha256(script_path.read_bytes()).hexdigest()
    proof['reference_probe_sha256']=source_hash
    # Input goes only to the PID of this owned native test application.
    # CGEventPostToPid avoids sending keystrokes to the user's foreground app.
    quartz=ctypes.CDLL('/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices')
    quartz.CGEventCreateKeyboardEvent.argtypes=[ctypes.c_void_p,ctypes.c_ushort,ctypes.c_bool]
    quartz.CGEventCreateKeyboardEvent.restype=ctypes.c_void_p
    quartz.CGEventPostToPid.argtypes=[ctypes.c_int,ctypes.c_void_p]
    quartz.CFRelease.argtypes=[ctypes.c_void_p]
    def key(pid,code):
        for down in (True,False):
            event=quartz.CGEventCreateKeyboardEvent(None,code,down)
            assert event,'native keyboard event allocation failed'
            quartz.CGEventPostToPid(pid,event);quartz.CFRelease(event)
    upstream=product.endpoint
    for mode in ('absent','false','true','loss'):
        control={'mode':mode,'calls':[],'capabilities':[],'release':threading.Event(),'started':threading.Event(),'peer_disconnects':0}
        class Projection(BaseHTTPRequestHandler):
            def log_message(self,*args):pass
            def forward(self):
                size=int(self.headers.get('Content-Length') or 0)
                data=self.rfile.read(size) if size else None
                request=urllib.request.Request(upstream+self.path,data=data,method=self.command,headers={k:v for k,v in self.headers.items() if k.lower() not in ('host','content-length')})
                try:response=urllib.request.urlopen(request,timeout=60)
                except urllib.error.HTTPError as error:response=error
                with response:
                    status=response.status
                    body=response.read() if '/api/v1/events' not in self.path else None
                    if self.path=='/api/v1/capabilities' and status==200:
                        value=json.loads(body);features=value['data']['features']
                        if control['mode']=='absent':features.pop('references',None)
                        elif control['mode']=='false':features['references']=False
                        else:assert features['references'] is True
                        control['capabilities'].append(features.get('references','absent'))
                        body=json.dumps(value).encode()
                    try:
                        self.send_response(status)
                        for k,v in response.getheaders():
                            if k.lower() not in ('transfer-encoding','content-length','connection'):self.send_header(k,v)
                        if body is not None:self.send_header('Content-Length',str(len(body)))
                        self.end_headers()
                        if body is not None:self.wfile.write(body)
                        else:
                            while chunk:=response.read1(65536):self.wfile.write(chunk);self.wfile.flush()
                    except (BrokenPipeError,ConnectionResetError):control['peer_disconnects']+=1
            def do_GET(self):
                url=urllib.parse.urlsplit(self.path)
                if url.path.endswith('/references/resolve'):
                    identity=urllib.parse.parse_qs(url.query).get('reference_id',[''])[0]
                    control['calls'].append({'reference':identity,'path':self.path,'mode':control['mode']})
                    if mode=='loss':
                        control['started'].set();assert control['release'].wait(45),'native delayed resolver not released'
                    if identity in ('gone','denied'):
                        body=json.dumps({'error':{'code':'TARGET_GONE' if identity=='gone' else 'FORBIDDEN','message':'labelled native fixture','retryable':False,'details':{}}}).encode()
                        self.send_response(410 if identity=='gone' else 403);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body);return
                if url.path=='/api/v1/status':
                    if mode=='loss' and control['started'].is_set():
                        # The probe's second status read follows its observed
                        # disabled channel. Only then release the real reply.
                        if False in control['capabilities']:control['release'].set()
                        control['mode']='false'
                    body=json.dumps({'data':{'resolver_started':control['started'].is_set(),'reference_calls':len(control['calls'])}}).encode()
                    self.send_response(200);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body);return
                self.forward()
            def do_POST(self):self.forward()
            def do_PUT(self):self.forward()
            def do_DELETE(self):self.forward()
            def do_HEAD(self):self.forward()
        server=ThreadingHTTPServer(('127.0.0.1',0),Projection)
        server.daemon_threads=True
        threading.Thread(target=server.serve_forever,daemon=True).start()
        try:
            constants=fixture|{'mode':mode}
            probe=work/('probe-references-'+mode+'.js')
            probe.write_text(script_path.read_text().replace('REFERENCE_CONSTANTS',json.dumps(constants,ensure_ascii=False)))
            process=launch(['--reader','--reader-port','9271','--reader-path',fixture['source_route'],'--reader-probe',str(probe)],'155s' if mode=='true' else '65s',base=f'http://127.0.0.1:{server.server_port}')
            deadline=time.monotonic()+(180 if mode=='true' else 90)
            sent=set();rows=None
            while time.monotonic()<deadline:
                path=state/f'native-reader-selfcheck-{process.pid}.json'
                if path.exists():
                    try:observed=json.loads(path.read_text())['rows']
                    except (ValueError,KeyError):observed=[]
                    for r in observed:
                        name=r['name']
                        if name.startswith('input.request.') and name not in sent:
                            key(process.pid,36 if name.endswith('enter') else 125);sent.add(name)
                    if any(r['name']=='page.done' and r['status']=='pass' for r in observed):rows=observed;break
                if process.poll() is not None:break
                time.sleep(.2)
            if rows is None:
                proof['cases'].append({'kind':'RF09–RF11 native '+mode,'ok':False,'window_or_probe_unavailable':True,'note':'No final actual-window report; Supervisor must run this stage in a window-capable session.'})
                raise AssertionError('native reference window/probe unavailable; no passing native proof claimed')
            assert process.wait(timeout=190)==0
            case={'kind':'RF09–RF11 native '+mode,'ok':all(r['status']=='pass' for r in rows),'rows':rows,'resolver_calls':control['calls'],'capability_projections':control['capabilities'],'native_inputs':sorted(sent),'seeded':mode!='true','fault_ids_seeded':['gone','denied'] if mode=='true' else [],'retired_request_peer_disconnects':control['peer_disconnects']}
            if mode=='true':
                guard=json.loads((state/'native-guard.json').read_text())
                popups=guard.get('popup_urls') or []
                expected='/documents/'+fixture['source']['document_id']+'/versions/'+fixture['source']['version_id']
                fallback=[p for p in popups if urllib.parse.urlsplit(p).path==expected and urllib.parse.parse_qs(urllib.parse.urlsplit(p).query).get('page')==['fallback.html']]
                case['native_popup_guard']={'destinations':popups,'fallback_count':len(fallback)}
                case['ok']=case['ok'] and len(fallback)>=2
            proof['cases'].append(case)
            count_ok=(not control['calls']) if mode in ('absent','false') else (len(control['calls'])==1 and control['release'].is_set()) if mode=='loss' else any(r['reference']=='exact' for r in control['calls'])
            case['call_count_ok']=bool(count_ok);case['ok']=case['ok'] and bool(count_ok)
        finally:
            control['release'].set();server.shutdown();server.server_close()
    assert hashlib.sha256(script_path.read_bytes()).hexdigest()==source_hash,'reference probe changed during observations'
    assert all(c.get('ok',True) for c in proof['cases']),'native RF row failed; all capability modes retained before stopping'
