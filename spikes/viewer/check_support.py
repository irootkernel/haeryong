"""Owned fixture lifecycle support; never manages an unrelated daemon/profile."""
import hashlib
import json
import os
from pathlib import Path
import shlex
import signal
import subprocess
import time
import urllib.request


def health(endpoint='http://127.0.0.1:9270'):
    with urllib.request.urlopen(endpoint+'/health',timeout=3) as response:
        return json.load(response)


def restart_daemon(binary, state, output, expected_binary=None):
    binary,state,output=Path(binary).resolve(),Path(state).resolve(),Path(output).resolve()
    pid=int(subprocess.check_output(['lsof','-nP','-iTCP:9270','-sTCP:LISTEN','-t'],text=True).strip())
    argv=shlex.split(subprocess.check_output(['ps','-p',str(pid),'-o','command='],text=True).strip())
    assert Path(argv[0]).resolve()==Path(expected_binary or binary).resolve(),'not the explicitly owned fixture executable'
    assert '--state' in argv and Path(argv[argv.index('--state')+1]).resolve()==state,'not the explicitly owned state'
    before=health();started=time.time_ns()
    os.kill(pid,signal.SIGTERM)
    deadline=time.monotonic()+10
    while subprocess.run(['kill','-0',str(pid)],capture_output=True).returncode==0:
        if time.monotonic()>deadline:raise RuntimeError('Owned daemon did not exit')
        time.sleep(.1)
    log=(output/'daemon-original.log').open('ab')
    process=subprocess.Popen([str(binary),*argv[1:]],stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
    log.close()
    deadline=time.monotonic()+20
    while True:
        if process.poll() is not None:raise RuntimeError('Owned replacement daemon exited')
        try:
            after=health()
            if after['instance']!=before['instance']:break
        except (OSError,ValueError):pass
        if time.monotonic()>deadline:raise RuntimeError('Owned replacement daemon did not become ready')
        time.sleep(.1)
    row={'before':before,'after':after,'old_pid':pid,'new_pid':process.pid,'argv':[str(binary),*argv[1:]],'same_state':str(state),'binary_sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'started_unix_ns':started,'ready_unix_ns':time.time_ns(),'actual_sigterm_and_process_relaunch':True}
    (output/'daemon-restart.json').write_text(json.dumps(row,indent=2)+'\n')
    return row
