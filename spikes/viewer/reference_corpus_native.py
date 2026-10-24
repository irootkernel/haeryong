"""RF12 actual native case over unchanged complete four-file sample payloads."""
import ctypes
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import threading
import urllib.request
import urllib.error
import importlib.util
import json
import os
from pathlib import Path
import time


def run(repo,work,state,product,owner,launch,proof):
    spec=importlib.util.spec_from_file_location('reference_support',repo/'tests/manual/reference_support.py')
    support=importlib.util.module_from_spec(spec);spec.loader.exec_module(support)
    fixture=support.prepare_corpus(product,owner,knowledge=os.environ.get('HAERYONG_KNOWLEDGE_GATE')=='1');proof['reference_corpus_fixture']=fixture
    source=Path(__file__).with_name('reference_corpus_probe.js')
    probe=work/'probe-reference-corpus.js';probe.write_text(source.read_text().replace('REFERENCE_CONSTANTS',json.dumps(fixture,ensure_ascii=False)))
    quartz=ctypes.CDLL('/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices')
    quartz.CGEventCreateKeyboardEvent.argtypes=[ctypes.c_void_p,ctypes.c_ushort,ctypes.c_bool];quartz.CGEventCreateKeyboardEvent.restype=ctypes.c_void_p
    quartz.CGEventPostToPid.argtypes=[ctypes.c_int,ctypes.c_void_p];quartz.CFRelease.argtypes=[ctypes.c_void_p]
    quartz.CGEventSetFlags.argtypes=[ctypes.c_void_p,ctypes.c_uint64]
    def key(pid,code):
        for down in (True,False):
            event=quartz.CGEventCreateKeyboardEvent(None,code,down)
            if code==48:quartz.CGEventSetFlags(event,1<<19)  # Option+Tab includes authored links in WebKit.
            quartz.CGEventPostToPid(pid,event);quartz.CFRelease(event)
    sent=set()
    class Projection(BaseHTTPRequestHandler):
        def log_message(self,*args):pass
        def forward(self):
            if self.path=='/api/v1/status':
                body=json.dumps({'data':{'native_inputs':sorted(sent)}}).encode();self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body);return
            length=int(self.headers.get('Content-Length','0'));body=self.rfile.read(length) if length else None
            request=urllib.request.Request(product.endpoint+self.path,data=body,method=self.command,headers={k:v for k,v in self.headers.items() if k.lower() not in ('host','content-length')})
            try:response=urllib.request.urlopen(request,timeout=60)
            except urllib.error.HTTPError as error:response=error
            with response:
                data=response.read();self.send_response(response.status)
                for k,v in response.headers.items():
                    if k.lower() not in ('content-length','transfer-encoding','connection'):self.send_header(k,v)
                self.send_header('Content-Length',str(len(data)));self.end_headers()
                try:self.wfile.write(data)
                except (BrokenPipeError,ConnectionResetError):pass
        do_GET=do_POST=do_PUT=do_PATCH=do_DELETE=do_HEAD=forward
    server=ThreadingHTTPServer(('127.0.0.1',0),Projection);server.daemon_threads=True
    threading.Thread(target=server.serve_forever,daemon=True).start()
    process=launch(['--reader','--reader-port','9271','--reader-path',fixture['source_route'],'--reader-probe',str(probe)],'130s',base='http://127.0.0.1:'+str(server.server_port))
    rows=None;deadline=time.monotonic()+150
    while time.monotonic()<deadline:
        path=state/f'native-reader-selfcheck-{process.pid}.json'
        if path.exists():
            try:observed=json.loads(path.read_text())['rows']
            except (ValueError,KeyError):observed=[]
            for row in observed:
                name=row['name']
                if name.startswith('input.request.') and name not in sent:
                    for code in {'input.request.down':[125],'input.request.scroll':[125,125,125],
                                 'input.request.tabs-enter':[48,48,48,36]}[name]:
                        key(process.pid,code);time.sleep(.1)
                    sent.add(name)
            if any(row['name']=='page.done' and row['status']=='pass' for row in observed):rows=observed;break
        if process.poll() is not None:break
        time.sleep(.2)
    case={'kind':'RF12 unchanged real sample native reference corpus','ok':rows is not None and all(row['status']=='pass' for row in rows),'rows':rows or [],'native_inputs':sorted(sent),'probe_sha256':hashlib.sha256(source.read_bytes()).hexdigest()}
    proof['cases'].append(case)
    result=process.wait(timeout=160)
    server.shutdown();server.server_close()
    assert result==0
    assert case['ok'],'RF12 native rows failed; all evaluated rows retained'
    if fixture['knowledge_gate']:
        path='/api/v1/versions/'+fixture['target']['version_id']+'/source-evidence-verifications?evidence_id=M-S09'
        before=product.data('GET',path,client=owner)
        assert any(r['outcome']=='captured' for r in before['items'])
        product.stop();product.start()
        assert product.data('GET',path,client=owner)==before
        proof['native_knowledge_restart']={'history':before,'unchanged_after_restart':True}
