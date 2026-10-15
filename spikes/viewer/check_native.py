"""Verify actual pinned Wails dispatch, close, crash and restart observations."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time
import urllib.request

p=argparse.ArgumentParser()
p.add_argument('--binary',type=Path,required=True)
p.add_argument('--state',type=Path,required=True)
p.add_argument('--output',type=Path,required=True)
p.add_argument('--endpoint',default='http://127.0.0.1:9270')
a=p.parse_args();a.output.mkdir(mode=0o700,parents=True,exist_ok=True)
assert a.output.stat().st_mode&0o777==0o700
def health():
    with urllib.request.urlopen(a.endpoint+'/health',timeout=5) as response:return json.load(response)
baseline=health();outcomes=[]
def reports():return set(a.state.glob('report-*.json'))
def wait_report(process,before,timeout=95):
    deadline=time.monotonic()+timeout
    while time.monotonic()<deadline:
        assert process.poll() is None,'native exited before its report'
        for file in reports()-before:
            report=json.loads(file.read_text())
            if 'wails.io' not in report.get('user_agent','') or report.get('kind'):continue
            failures=[r for r in report['results'] if r['status']!='pass']
            assert not failures, json.dumps(failures)
            assert report['instance']==baseline['instance'],'native attached to another daemon'
            native=next(r['value']for r in report['results']if r['name']=='native-known-binding')
            assert native['before']['binding_calls']==1 and native['after']['binding_calls']==2
            assert native['after']['native_guard']['denied_messages']>=12
            assert native['after']['native_guard']['plain_popups']>=1
            streaming=next(r['value']for r in report['results']if r['name']=='native-streamed-independent-progressing-download')
            downloaded=Path(streaming['path'])
            with downloaded.open('rb') as stream:actual=hashlib.file_digest(stream,'sha256').hexdigest()
            assert downloaded.stat().st_size==36*1024*1024 and actual==streaming['sha256']
            assert streaming['elapsed_seconds']>=30
            return file,report
        time.sleep(.2)
    raise AssertionError('native did not deliver complete actual report before deadline')

for name,crash in [('normal-close',False),('forced-crash',True),('restart',False)]:
    before=reports();log=a.output/(name+'.log')
    with log.open('w')as output:
        process=subprocess.Popen([str(a.binary.resolve()),'--base',a.endpoint,'--state',str(a.state.resolve()),'--close-after','100s'],stdout=output,stderr=output)
        started=time.monotonic()
        try:
            if crash:
                deadline=time.monotonic()+15
                while time.monotonic()<deadline:
                    assert process.poll() is None
                    receipt=a.state/'native-guard.json'
                    if receipt.exists():
                        proof=json.loads(receipt.read_text())
                        if proof.get('pid')==process.pid and proof.get('installed'):
                            observed=a.state/f'native-observed-{process.pid}.json'
                            if observed.exists():
                                current=json.loads(observed.read_text())
                                if current.get('renderer_ids'):break
                    time.sleep(.1)
                else:raise AssertionError('fresh native guard installation not observed')
                process.kill();code=process.wait(timeout=5);assert code==-9
                assert not (a.state/f'native-close-{process.pid}.json').exists(),'forced kill fabricated a close receipt'
                outcomes.append({'case':name,'exit':code,'fresh_native_guard':proof,'observed_renderers_before_kill':current,'explicit_close_receipt':False,'daemon_same_instance':health()==baseline})
            else:
                file,report=wait_report(process,before)
                code=process.wait(timeout=45);assert code==0
                assert time.monotonic()-started>=95,'native closed before configured real close interval'
                assert health()==baseline,'native lifecycle changed daemon state'
                receipt_path=a.state/f'native-close-{process.pid}.json';receipt=json.loads(receipt_path.read_text());assert receipt['rows'] and all(row['status']==204 for row in receipt['rows']),'normal native close did not explicitly close observed renderers'
                outcomes.append({'case':name,'exit':code,'report':str(file),'original_report':report,'normal_close_receipt':receipt,'daemon_same_instance':True})
        finally:
            if process.poll() is None:process.kill();process.wait(timeout=5)
credential=(a.state/'native-session').read_text().strip()
request=urllib.request.Request(a.endpoint+'/viewer/observations',headers={'Authorization':'Bearer '+credential})
with urllib.request.urlopen(request,timeout=5)as response:observations=json.load(response)
forced=next(r for r in outcomes if r['case']=='forced-crash')
ids=forced['observed_renderers_before_kill']['renderer_ids'];leases=[r for r in observations['delivery_leases']if r['id']in ids]
assert len(leases)==len(ids)and all(r['remaining_ms']==0 and not r['closed']for r in leases),'lost connection was not separately represented by lease expiry'
forced['later_lease_observation']=leases
assert all(r['daemon_same_instance']for r in outcomes)
result={'status':'pass','fixture_only':True,'binary_sha256':hashlib.sha256(a.binary.read_bytes()).hexdigest(),'daemon':baseline,'outcomes':outcomes,'close_mechanism':'actual Wails window.Close; not human window UI'}
(a.output/'native-result.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({'status':'pass','native_cases':len(outcomes),'output':str(a.output/'native-result.json')}))
