"""Observe owned Wails UI/storage and active-daemon restart on the current loader."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import time
from check_support import health, restart_daemon

p=argparse.ArgumentParser()
p.add_argument('--binary',type=Path,required=True)
p.add_argument('--daemon-binary',type=Path,required=True)
p.add_argument('--state',type=Path,required=True)
p.add_argument('--output',type=Path,required=True)
a=p.parse_args();a.output.mkdir(mode=0o700,parents=True,exist_ok=True)
assert a.output.stat().st_mode&0o777==0o700
outcomes=[]
for mode in ['write','read']:
    before=set(a.state.glob('report-*.json'))
    with(a.output/(mode+'-native-original.log')).open('w')as log:
        process=subprocess.Popen([str(a.binary.resolve()),'--state',str(a.state.resolve()),'--lifecycle',mode,'--close-after','35s'],stdout=log,stderr=subprocess.STDOUT)
        try:
            deadline=time.monotonic()+32;restarted=False;report=None
            while time.monotonic()<deadline:
                assert process.poll()is None,'native exited before lifecycle observation'
                for file in set(a.state.glob('report-*.json'))-before:
                    row=json.loads(file.read_text())
                    if 'wails.io'not in row.get('user_agent',''):continue
                    if mode=='write'and row.get('kind')=='active-daemon-restart-ready'and not restarted:
                        restart_daemon(a.daemon_binary,a.state,a.output);restarted=True
                    if row.get('kind')==('active-daemon-restart'if mode=='write'else'ui-restart-storage'):
                        report={'pid':process.pid,'path':str(file),'original':row};break
                if report:break
                time.sleep(.2)
            assert report,'native lifecycle report missing'
            assert report['original']['status']=='pass',json.dumps(report['original'])
            assert process.wait(timeout=45)==0,'normal actual Wails close failed'
            report['normal_close_receipt']=json.loads((a.state/f'native-close-{process.pid}.json').read_text())
            outcomes.append(report)
        finally:
            if process.poll()is None:process.kill();process.wait(timeout=5)
result={'fixture_only':True,'status':'pass','binary_sha256':hashlib.sha256(a.binary.read_bytes()).hexdigest(),'daemon':health(),'outcomes':outcomes,'actual_ui_process_exit_and_relaunch':True}
(a.output/'native-lifecycle-result.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({'status':'pass','output':str(a.output/'native-lifecycle-result.json')}))
