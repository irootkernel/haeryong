"""Exercise the real Make aggregate with harmless substituted leaf commands."""
import os
from pathlib import Path
import subprocess
import sys

import pytest

pytestmark = pytest.mark.unit
STAGES = ["prepare", "unit", "integration", "persistence", "e2e"]


@pytest.mark.parametrize("failure", [None, *STAGES])
@pytest.mark.parametrize("keep_default", [None, "0"])
def test_parallel_make_is_serial_once_and_stops_at_the_first_failure(tmp_path, failure, keep_default):
    root = Path(__file__).resolve().parents[2]
    (tmp_path / "Makefile").write_bytes((root / "Makefile").read_bytes())
    leaf = tmp_path / "leaf"
    leaf.write_text(f"#!{sys.executable}\n" + '''import os, pathlib, sys, time
stage = sys.argv[-1]
with pathlib.Path(os.environ["ORDER"]).open("a") as stream:
    stream.write("start " + stage + "\\n")
    stream.flush()
    time.sleep(0.01)
    stream.write("end " + stage + "\\n")
sys.exit(7 if stage == os.environ.get("FAIL_STAGE") else 0)
''')
    leaf.chmod(0o700)
    order = tmp_path / "order"
    env = os.environ | {"ORDER": str(order), "FAIL_STAGE": failure or ""}
    for name in ("MAKEFLAGS", "MFLAGS", "MAKEOVERRIDES", "KEEP_GOING"):env.pop(name, None)
    arguments=["make", "-j8", "test", f"PYTHON={leaf}"]
    if keep_default is not None:arguments.append("KEEP_GOING="+keep_default)
    result = subprocess.run(arguments, cwd=tmp_path,
                            env=env, capture_output=True, timeout=10)
    expected = STAGES if failure is None else STAGES[:STAGES.index(failure)+1]
    assert order.read_text().splitlines() == [f"{event} {stage}" for stage in expected for event in ("start", "end")]
    assert (result.returncode == 0) is (failure is None)


def test_test_run_root_rejects_unverified_shared_and_symlink_directories(tmp_path, monkeypatch):
    from tests import run_checks

    root = tmp_path / "shared"
    root.mkdir(mode=0o700)
    monkeypatch.setenv("HAERYONG_TEST_RUN_ROOT", str(root))
    with pytest.raises(RuntimeError, match="unverified"):
        run_checks.run_root()
    alias = tmp_path / "alias"
    alias.symlink_to(root, target_is_directory=True)
    monkeypatch.setenv("HAERYONG_TEST_RUN_ROOT", str(alias))
    with pytest.raises(RuntimeError, match="non-symlink"):
        run_checks.run_root()


def test_ambient_collection_only_options_cannot_replace_a_required_gate(monkeypatch):
    from tests import run_checks

    monkeypatch.setenv("PYTEST_ADDOPTS", "--collect-only")
    monkeypatch.setattr(sys, "argv", ["run_checks.py", "unit"])
    monkeypatch.setattr(run_checks, "toolchain", lambda: pytest.fail("must refuse before starting the stage"))
    with pytest.raises(RuntimeError, match="PYTEST_ADDOPTS must be unset"):
        run_checks.main()


@pytest.mark.parametrize("override", [False, True])
def test_make_selects_local_test_python_and_preserves_explicit_override(tmp_path, override):
    root = Path(__file__).resolve().parents[2]
    (tmp_path / "Makefile").write_bytes((root / "Makefile").read_bytes())
    selected = tmp_path / ".venv" / "bin" / "python"
    selected.parent.mkdir(parents=True)
    explicit = tmp_path / "explicit-python"
    for path in (selected, explicit):
        path.write_text(f"#!{sys.executable}\n" + "import pathlib, sys\npathlib.Path('selected').write_text(sys.argv[0])\n")
        path.chmod(0o700)
    env = dict(os.environ)
    for name in ("PYTHON", "MAKEFLAGS", "MFLAGS", "MAKEOVERRIDES", "KEEP_GOING"):env.pop(name, None)
    args = ["make", "test-unit"]
    if override:
        args.append(f"PYTHON={explicit}")
    result = subprocess.run(args, cwd=tmp_path, env=env, capture_output=True, timeout=10)
    assert result.returncode == 0
    assert (tmp_path / (tmp_path / "selected").read_text()).resolve() == (explicit if override else selected)


@pytest.mark.parametrize('target,stages', [('test', STAGES), ('test-e2e', STAGES[-2:])])
@pytest.mark.parametrize('mode', ['pass', 'ordinary', 'raw-code', 'fatal', 'cancel'])
def test_keep_going_serial_once_and_leaf_control_results(tmp_path, target, stages, mode):
    root = Path(__file__).resolve().parents[2]
    (tmp_path / 'Makefile').write_bytes((root / 'Makefile').read_bytes())
    leaf = tmp_path / 'leaf'
    leaf.write_text(f'#!{sys.executable}\n' + '''import os,pathlib,sys
stage=sys.argv[3]
assert sys.argv[4:]==['--keep-going']
with pathlib.Path('order').open('a') as f:f.write(stage+'\\n')
mode=os.environ['MODE']
# Raw child codes belong in leaf artifacts. The Make loop sees only leaf control.
sys.exit((2 if mode=='fatal' else 130 if mode=='cancel' else 1) if mode!='pass' and stage==os.environ['FIRST'] else 0)
''')
    leaf.chmod(0o700)
    result = subprocess.run(['make','-j8',target,'KEEP_GOING=1',f'PYTHON={leaf}'], cwd=tmp_path,
                            env=os.environ | {'MODE':mode,'FIRST':stages[0]}, capture_output=True, timeout=10)
    assert (tmp_path/'order').read_text().splitlines() == (stages[:1] if mode in ('fatal','cancel') else stages)
    assert (result.returncode == 0) == (mode == 'pass')


@pytest.mark.parametrize('keep', [False, True])
def test_leaf_go_failure_does_not_hide_python_in_keep_going(tmp_path, monkeypatch, keep):
    from tests import run_checks as r
    monkeypatch.setattr(sys,'argv',['run_checks.py','unit']+(['--keep-going'] if keep else []))
    monkeypatch.delenv('PYTEST_ADDOPTS',raising=False)
    monkeypatch.setattr(r,'toolchain',lambda:None)
    monkeypatch.setattr(r.platform,'platform',lambda:'test-only-host')
    monkeypatch.setattr(r,'run_root',lambda:tmp_path)
    monkeypatch.setattr(r.subprocess,'check_output',lambda *a,**k:'darwin\narm64\n')
    calls=[]
    def go(*args):
        calls.append('go')
        raise r.CheckFailure('compile failed (raw child 2)')
    monkeypatch.setattr(r,'go_suite',go)
    monkeypatch.setattr(r,'pytest_suite',lambda *a:calls.append('python'))
    if keep:
        assert r.main()==1
        assert calls==['go','python']
    else:
        with pytest.raises(r.CheckFailure):r.main()
        assert calls==['go']


def test_prepare_keeps_all_independent_syntax_digests_and_builds_without_rewrite(tmp_path,monkeypatch):
    from tests import run_checks as r
    import json
    monkeypatch.setattr(r,'ROOT',tmp_path)
    for folder in ['cmd','internal','tests/contracts','docs/testing']:(tmp_path/folder).mkdir(parents=True,exist_ok=True)
    source=tmp_path/'cmd/main.go';source.write_bytes(b'package main;func main(){}\n')
    (tmp_path/'conftest.py').write_text('def (')
    (tmp_path/'tests/bad.py').write_text('if :')
    (tmp_path/'tests/contracts/manifest.json').write_text(json.dumps({'files':[{'path':p,'bytes':0,'sha256':'wrong'} for p in ['cmd/main.go','conftest.py']]}))
    calls=[]
    def command(args,log,env,**kwargs):
        calls.append(args)
        log.write_text('cmd/main.go\n' if args[0]=='gofmt' else '')
        if args[:2]==['go','build'] and env['GOOS']=='darwin' and env['GOARCH']=='arm64':raise r.CheckFailure('first cross-build failed')
    monkeypatch.setattr(r,'command',command)
    monkeypatch.setattr(r,'validate_layers',lambda:None)
    work=tmp_path/'work';work.mkdir()
    before=source.read_bytes()
    with pytest.raises(r.CheckFailure):r.prepare(work,{},True)
    failures=json.loads((work/'failures.json').read_text())
    names={f['check'] for f in failures}
    assert {'format','syntax:conftest.py','syntax:tests/bad.py','digest:cmd/main.go','digest:conftest.py','haeryong-darwin-arm64'}<=names
    assert [a[3] for a in calls if a[:2]==['go','build']]==[str(work/('haeryong-'+os+'-'+arch)) for os in ['darwin','linux'] for arch in ['arm64','amd64']]
    assert calls[0][1]=='-l' and source.read_bytes()==before


@pytest.mark.parametrize('code',[1,2,4,7])
def test_command_preserves_raw_child_exit_separate_from_control(tmp_path,code):
    from tests import run_checks as r
    import json
    log=tmp_path/'child.log'
    with pytest.raises(r.CheckFailure):r.command([sys.executable,'-B','-c',f'raise SystemExit({code})'],log,dict(os.environ))
    result=json.loads(log.with_suffix('.status.json').read_text())
    assert result['exit_code']==code and result['signal'] is None and not result['timed_out']


def test_timeout_stops_owned_descendant_and_keeps_failure(tmp_path):
    from tests import run_checks as r
    import json,signal,time
    childfile=tmp_path/'child'
    code="import subprocess,sys,time; p=subprocess.Popen([sys.executable,'-c','import time; time.sleep(60)']); open(sys.argv[1],'w').write(str(p.pid)); time.sleep(60)"
    log=tmp_path/'timeout.log'
    with pytest.raises(r.CheckFailure):r.command([sys.executable,'-B','-c',code,str(childfile)],log,dict(os.environ),timeout=1)
    result=json.loads(log.with_suffix('.status.json').read_text())
    assert result['timed_out'] and result['signal'] in (signal.SIGTERM,signal.SIGKILL)
    descendant=int(childfile.read_text())
    # ps distinguishes an OS-owned reaping zombie from a surviving runnable child.
    state=subprocess.run(['ps','-o','stat=','-p',str(descendant)],capture_output=True,text=True,timeout=5).stdout.strip()
    assert not state or state.startswith('Z')
    later=tmp_path/'later.log';r.command([sys.executable,'-B','-c','pass'],later,dict(os.environ))


@pytest.mark.parametrize('keep',[False,True])
def test_native_argv_preserves_defaults_and_opt_in(tmp_path,monkeypatch,keep):
    from tests import run_checks as r
    import json
    calls=[]
    def command(args,log,env,**kwargs):
        calls.append(args)
        (tmp_path/'go-unit.json').write_text(json.dumps([{'SpecReports':[{'LeafNodeLabels':['unit'],'State':'passed'}]}]))
    monkeypatch.setattr(r,'command',command);monkeypatch.setattr(r,'go_packages',lambda _:['./fixture'])
    r.go_suite('unit',tmp_path,{},keep);r.pytest_suite('unit',['tests/unit'],tmp_path,{},keep)
    assert ('--keep-going' in calls[0])==keep
    assert ('-x' in calls[1])==(not keep)


@pytest.mark.parametrize('sig',[15,2])
def test_cancellation_cleans_child_group_and_never_starts_later_check(tmp_path,sig):
    import signal,time,json
    root=Path(__file__).resolve().parents[2]
    ready=tmp_path/'ready';later=tmp_path/'later'
    script='''import sys,signal
from pathlib import Path
from tests import run_checks as r
signal.signal(signal.SIGTERM,r.interrupt);signal.signal(signal.SIGINT,r.interrupt)
try:
 r.command([sys.executable,'-B','-c',"import pathlib,sys,time; pathlib.Path(sys.argv[1]).write_text('ready'); time.sleep(60)",sys.argv[1]],Path(sys.argv[3]),dict(__import__('os').environ))
 Path(sys.argv[2]).write_text('incorrect continuation')
except r.RunInterrupted as e:sys.exit(128+e.signum)
'''
    proc=subprocess.Popen([sys.executable,'-B','-c',script,str(ready),str(later),str(tmp_path/'child.log')],cwd=root,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    try:
        deadline=time.monotonic()+10
        while not ready.exists() and time.monotonic()<deadline:
            assert proc.poll() is None
            time.sleep(.01)
        assert ready.exists()
        proc.send_signal(sig)
        assert proc.wait(timeout=10)==128+sig
        assert not later.exists()
        status=json.loads((tmp_path/'child.status.json').read_text())
        assert status['signal'] in (signal.SIGTERM,signal.SIGKILL)
        assert status['timed_out'] is False and status['exit_code'] < 0
    finally:
        if proc.poll() is None:proc.kill();proc.wait(timeout=5)


def test_leaf_global_setup_failure_is_fatal_control(tmp_path):
    root=Path(__file__).resolve().parents[2]
    result=subprocess.run([sys.executable,'-B','tests/run_checks.py','unit','--keep-going'],cwd=root,
                          env=os.environ|{'PYTEST_ADDOPTS':'--collect-only'},capture_output=True,timeout=10)
    assert result.returncode==2 and b'PYTEST_ADDOPTS must be unset' in result.stderr


@pytest.mark.parametrize('report',[None,'{','{}','[{}]','[{"SpecReports":{}}]','[{"SpecReports":[null]}]','[{"SpecReports":[{}]}]','[{"SpecReports":[{"State":"passed","LeafNodeLabels":7}]}]'])
def test_malformed_go_reports_are_check_failures_and_python_still_runs(tmp_path,monkeypatch,report):
    from tests import run_checks as r
    monkeypatch.setattr(r,'go_packages',lambda _:['./fixture'])
    def command(*args,**kwargs):
        if report is not None:(tmp_path/'go-unit.json').write_text(report)
    monkeypatch.setattr(r,'command',command)
    checks=r.Checks(tmp_path,True);calls=[]
    checks.run('go',lambda:r.go_suite('unit',tmp_path,{},True))
    checks.run('python',lambda:calls.append('ran'))
    assert calls==['ran'] and len(checks.failures)==1


@pytest.mark.parametrize('manifest',['{','{}','{"files":{}}','{"files":[{},null]}'])
def test_malformed_manifest_retains_independent_prepare_remainder(tmp_path,monkeypatch,manifest):
    from tests import run_checks as r
    import json
    monkeypatch.setattr(r,'ROOT',tmp_path)
    (tmp_path/'tests/contracts').mkdir(parents=True);(tmp_path/'conftest.py').write_text('pass')
    (tmp_path/'tests/contracts/manifest.json').write_text(manifest)
    calls=[]
    def command(args,log,env,**kwargs):calls.append(args);log.write_text('')
    monkeypatch.setattr(r,'command',command);monkeypatch.setattr(r,'validate_layers',lambda:None)
    work=tmp_path/'work';work.mkdir()
    with pytest.raises(r.CheckFailure):r.prepare(work,{},True)
    assert len([args for args in calls if args[:2]==['go','build']])==4
    assert any(args[:2]==['go','mod'] for args in calls)
    assert json.loads((work/'failures.json').read_text())


@pytest.mark.parametrize('stop',['cancel','cleanup-failure'])
@pytest.mark.parametrize('status_fails',[False,True])
def test_stop_control_survives_evidence_failure_and_never_continues(tmp_path,monkeypatch,stop,status_fails):
    from tests import run_checks as r
    import signal
    class Child:
        pid=123
        returncode=-15
        def wait(self,**kwargs):
            if stop=='cancel':raise r.RunInterrupted(signal.SIGTERM)
            raise subprocess.TimeoutExpired('fixture',1)
    monkeypatch.setattr(r.subprocess,'Popen',lambda *a,**k:Child())
    def cleanup(_):
        if stop=='cleanup-failure':raise r.FatalSetup('cleanup uncertain')
    monkeypatch.setattr(r,'stop_children',cleanup)
    original=Path.write_text
    def write(path,*args,**kwargs):
        if status_fails and path.name.endswith('.status.json'):raise OSError('status unavailable')
        return original(path,*args,**kwargs)
    monkeypatch.setattr(Path,'write_text',write)
    checks=r.Checks(tmp_path,True);later=[]
    with pytest.raises(r.RunInterrupted if stop=='cancel' else r.FatalSetup):
        checks.run('child',lambda:r.command(['fixture'],tmp_path/'child.log',{},timeout=1))
        checks.run('later',lambda:later.append(True))
    assert not later


@pytest.mark.parametrize("group_state", ["absent", "zombies", "unrelated-opaque-state"])
def test_sigkill_eperm_uses_only_current_reaped_owned_group_evidence(tmp_path, monkeypatch, group_state):
    from tests import run_checks as r
    import errno, json, signal

    group = max(os.getpid(), os.getpgrp()) + 100000  # synthetic; killpg is fault-injected below
    calls, snapshots = [], []
    class Child:
        pid = group
        returncode = None
        def wait(self, *, timeout):
            calls.append(("wait", timeout))
            if timeout == 17:
                raise subprocess.TimeoutExpired("owned-fixture", timeout)
            self.returncode = -signal.SIGTERM
            return self.returncode
    child = Child()
    monkeypatch.setattr(r.subprocess, "Popen", lambda *a, **k: child)
    def killpg(pgid, sig):
        assert pgid == group
        calls.append(("signal", sig))
        if sig == signal.SIGKILL:
            raise PermissionError(errno.EPERM, "injected owned group EPERM")
    monkeypatch.setattr(r.os, "killpg", killpg)
    def snapshot(argv, **kwargs):
        snapshots.append(argv)
        assert argv == ["ps", "-A", "-o", "pid=,pgid=,uid=,stat="]
        assert kwargs["timeout"] == 5 and kwargs["capture_output"] and kwargs["encoding"] == "ascii"
        assert kwargs["env"]["LC_ALL"] == "C"
        assert child.returncode == -signal.SIGTERM and ("wait", 0) in calls
        # An unrelated runnable process with another UID must not be selected.
        text = f"{os.getpid()} {os.getpgrp()} {os.geteuid()} S\n{group+9} {group+9} {os.geteuid()+1} R\n{group+10} {group+10} -2 SS\n"
        if group_state == "unrelated-opaque-state":
            text += f"{group+11} {group+11} -2 synthetic-unknown-state\n"
        if group_state == "zombies":
            text += f"{group+1} {group} {os.geteuid()} Z\n{group+2} {group} {os.geteuid()} Z+\n"
        return subprocess.CompletedProcess(argv, 0, text, "")
    monkeypatch.setattr(r.subprocess, "run", snapshot)
    handlers = {sig: signal.getsignal(sig) for sig in (signal.SIGINT, signal.SIGTERM)}
    checks = r.Checks(tmp_path, True); later = []
    checks.run("timeout", lambda: r.command(["owned-fixture"], tmp_path / "child.log", {}, timeout=17))
    checks.run("later", lambda: later.append("ran"))
    assert later == ["ran"] and len(checks.failures) == 1
    status = json.loads((tmp_path / "child.status.json").read_text())
    assert status == {"argv": ["owned-fixture"], "exit_code": -signal.SIGTERM, "signal": signal.SIGTERM, "timed_out": True}
    assert snapshots == [["ps", "-A", "-o", "pid=,pgid=,uid=,stat="]]
    assert [item for item in calls if item[0] == "signal"] == [("signal", signal.SIGTERM), ("signal", signal.SIGKILL)]
    assert ("wait", 5) in calls and {sig: signal.getsignal(sig) for sig in handlers} == handlers


@pytest.mark.parametrize("fault", [
    "runnable", "stopped", "foreign-zombie", "foreign-signed-uid", "mixed", "unknown-state", "bad-pid", "bad-group", "bad-uid",
    "missing-field", "extra-field", "duplicate-pid", "blank-line", "empty", "truncated", "missing-caller",
    "wrong-caller-uid", "wrong-caller-group", "unknown-caller-state", "unrelated-bad-pid", "unrelated-extra-field", "stderr", "failed", "timeout", "unavailable", "undecodable",
])
def test_sigkill_eperm_unknown_or_live_group_is_fatal_and_stops_later_checks(tmp_path, monkeypatch, fault):
    from tests import run_checks as r
    import errno, json, signal

    group = max(os.getpid(), os.getpgrp()) + 100000  # no real group is signalled
    class Child:
        pid = group
        returncode = None
        def wait(self, *, timeout):
            if timeout == 17:
                raise subprocess.TimeoutExpired("owned-fixture", timeout)
            self.returncode = -signal.SIGTERM
            return self.returncode
    monkeypatch.setattr(r.subprocess, "Popen", lambda *a, **k: Child())
    signals = []
    def killpg(pgid, sig):
        assert pgid == group; signals.append(sig)
        if sig == signal.SIGKILL:
            raise PermissionError(errno.EPERM, "injected EPERM")
    monkeypatch.setattr(r.os, "killpg", killpg)
    def snapshot(argv, **kwargs):
        if fault == "timeout":raise subprocess.TimeoutExpired(argv, 5)
        if fault == "unavailable":raise FileNotFoundError("injected missing ps")
        if fault == "undecodable":raise UnicodeDecodeError("ascii", b"\xff", 0, 1, "injected invalid metadata")
        caller = f"{os.getpid()} {os.getpgrp()} {os.geteuid()} S\n"
        row = f"{group+1} {group} {os.geteuid()} Z\n"
        if fault == "runnable":row = row.replace(" Z", " R")
        if fault == "stopped":row = row.replace(" Z", " T")
        if fault == "foreign-zombie":row = f"{group+1} {group} {os.geteuid()+1} Z\n"
        if fault == "foreign-signed-uid":row = f"{group+1} {group} -2 Z\n"
        if fault == "mixed":row += f"{group+2} {group} {os.geteuid()} S\n"
        if fault == "unknown-state":row = row.replace(" Z", " ?")
        if fault == "bad-pid":row = f"invalid {group} {os.geteuid()} Z\n"
        if fault == "bad-group":row = f"{group+1} -1 {os.geteuid()} Z\n"
        if fault == "bad-uid":row = f"{group+1} {group} owner Z\n"
        if fault == "missing-field":row = f"{group+1} {group} {os.geteuid()}\n"
        if fault == "extra-field":row = row.rstrip() + " argv-not-requested\n"
        if fault == "duplicate-pid":row += row
        if fault == "blank-line":row += "\n"
        if fault == "unknown-caller-state":caller = caller.replace(" S", " synthetic-unknown-state")
        if fault == "unrelated-bad-pid":row += f"invalid {group+9} -2 synthetic-unknown-state\n"
        if fault == "unrelated-extra-field":row += f"{group+9} {group+9} -2 synthetic-unknown-state extra\n"
        if fault == "wrong-caller-uid":caller = f"{os.getpid()} {os.getpgrp()} {os.geteuid()+1} S\n"
        if fault == "wrong-caller-group":caller = f"{os.getpid()} {group+9} {os.geteuid()} S\n"
        text = ("" if fault == "missing-caller" else caller) + row
        if fault == "empty":text = ""
        if fault == "truncated":text = text.rstrip("\n")
        return subprocess.CompletedProcess(argv, 1 if fault == "failed" else 0, text, "injected error" if fault == "stderr" else "")
    monkeypatch.setattr(r.subprocess, "run", snapshot)
    handlers = {sig: signal.getsignal(sig) for sig in (signal.SIGINT, signal.SIGTERM)}
    checks = r.Checks(tmp_path, True); later = []
    with pytest.raises(r.FatalSetup):
        checks.run("timeout", lambda: r.command(["owned-fixture"], tmp_path / "child.log", {}, timeout=17))
        checks.run("later", lambda: later.append(True))
    assert not later and not checks.failures
    assert signals == [signal.SIGTERM, signal.SIGKILL]
    status = json.loads((tmp_path / "child.status.json").read_text())
    assert status["timed_out"] is True  # no fabricated successful command
    assert status["exit_code"] == -signal.SIGTERM and status["signal"] == signal.SIGTERM, "known leader exit survives fatal descendant cleanup"
    assert {sig: signal.getsignal(sig) for sig in handlers} == handlers


@pytest.mark.parametrize("fault", ["term-eperm", "kill-eacces", "leader-not-reaped", "leader-unknown"])
def test_cleanup_does_not_generalize_permission_exception_or_leader_termination(monkeypatch, fault):
    from tests import run_checks as r
    import errno, signal
    group = max(os.getpid(), os.getpgrp()) + 100000
    class Child:
        pid = group
        returncode = None
        def wait(self, *, timeout):
            if fault == "leader-not-reaped":raise subprocess.TimeoutExpired("owned-fixture", timeout)
            if fault != "leader-unknown":self.returncode = -signal.SIGTERM
            return self.returncode
    def killpg(pgid, sig):
        assert pgid == group
        if sig == signal.SIGTERM and fault == "term-eperm":raise PermissionError(errno.EPERM, "injected TERM EPERM")
        if sig == signal.SIGKILL:raise PermissionError(errno.EACCES if fault == "kill-eacces" else errno.EPERM, "injected kill denial")
    monkeypatch.setattr(r.os, "killpg", killpg)
    monkeypatch.setattr(r.subprocess, "run", lambda *a, **k: pytest.fail("unqualified permission errors must not inspect metadata"))
    handlers = {sig: signal.getsignal(sig) for sig in (signal.SIGINT, signal.SIGTERM)}
    with pytest.raises(r.FatalSetup):r.stop_children(Child())
    assert {sig: signal.getsignal(sig) for sig in handlers} == handlers


@pytest.mark.parametrize("ignore_term", [False, True])
def test_actual_ready_descendant_term_and_kill_cleanup_retains_timeout_and_continues(tmp_path, monkeypatch, ignore_term):
    from tests import run_checks as r
    import json, signal, time
    ready = tmp_path / "descendant-ready"
    # The descendant publishes readiness only AFTER its selected TERM behavior
    # is installed. The real command timeout starts after that barrier.
    descendant_code = "import os,pathlib,signal,sys,time; signal.signal(signal.SIGTERM,signal.SIG_IGN if sys.argv[2]=='ignore' else signal.SIG_DFL); pathlib.Path(sys.argv[1]).write_text(str(os.getpid())); time.sleep(60)"
    parent_code = "import subprocess,sys,time; subprocess.Popen([sys.executable,'-B','-c',sys.argv[1],sys.argv[2],sys.argv[3]]); time.sleep(60)"
    argv = [sys.executable, "-B", "-c", parent_code, descendant_code, str(ready), "ignore" if ignore_term else "normal"]
    original_popen, original_killpg = subprocess.Popen, os.killpg
    owned, sent = [], []
    def prepared(args, **kwargs):
        process = original_popen(args, **kwargs)
        if args == argv:
            owned.append(process)
            deadline = time.monotonic() + 15
            while not ready.exists() and time.monotonic() < deadline:
                assert process.poll() is None, "owned leader exited before descendant readiness"
                time.sleep(.01)
            if not ready.exists():
                original_killpg(process.pid, signal.SIGKILL);process.wait(timeout=5)
                pytest.fail("owned descendant did not reach its TERM readiness barrier")
        return process
    def killpg(pgid, sig):
        assert owned and pgid == owned[0].pid, "signals must target only this newly owned group"
        sent.append(sig);return original_killpg(pgid, sig)
    monkeypatch.setattr(r.subprocess, "Popen", prepared)
    monkeypatch.setattr(r.os, "killpg", killpg)
    try:
        checks = r.Checks(tmp_path, True)
        checks.run("actual-timeout", lambda: r.command(argv, tmp_path / "actual.log", dict(os.environ), timeout=1))
        assert len(checks.failures) == 1 and owned[0].returncode == -signal.SIGTERM
        status = json.loads((tmp_path / "actual.status.json").read_text())
        assert status["timed_out"] and status["exit_code"] == -signal.SIGTERM and status["signal"] == signal.SIGTERM
        assert signal.SIGTERM in sent and (not ignore_term or signal.SIGKILL in sent)
        pid = int(ready.read_text()); deadline = time.monotonic() + 5
        while True:
            observation = subprocess.run(["ps", "-p", str(pid), "-o", "pid=,pgid=,uid=,stat="], capture_output=True, text=True, timeout=5)
            fields = observation.stdout.split()
            if not fields:assert observation.returncode == 1;break
            assert observation.returncode == 0 and len(fields) == 4
            assert [int(value) for value in fields[:3]] == [pid, owned[0].pid, os.geteuid()]
            if fields[3].startswith("Z"):break
            assert time.monotonic() < deadline, "newly owned descendant remained runnable after cleanup"
            time.sleep(.01)
        assert r.owned_group_is_drained(owned[0].pid), "actual native ps grammar must establish this fresh group's drained state"
        # Restore only instrumentation before a fresh independent actual command;
        # production cleanup/timeout semantics stay in force throughout.
        monkeypatch.setattr(r.subprocess, "Popen", original_popen)
        monkeypatch.setattr(r.os, "killpg", original_killpg)
        checks.run("later", lambda: r.command([sys.executable, "-B", "-c", "pass"], tmp_path / "later.log", dict(os.environ)))
        assert (tmp_path / "later.status.json").is_file() and len(checks.failures) == 1
    finally:
        # Every failure path still owns only this newly launched fixture. The
        # direct Popen handle, or the ready descendant's exact current PID/group/
        # UID, must establish ownership before emergency cleanup signals it.
        if owned:
            leader = owned[0]
            active_leader = leader.poll() is None
            active_descendant = False
            if ready.exists():
                descendant = int(ready.read_text())
                current = subprocess.run(["ps", "-p", str(descendant), "-o", "pid=,pgid=,uid=,stat="], capture_output=True, text=True, timeout=5)
                fields = current.stdout.split()
                assert not current.stderr, "cleanup metadata errors are not absence"
                if fields:
                    assert current.returncode == 0 and len(fields) == 4
                    assert [int(value) for value in fields[:3]] == [descendant, leader.pid, os.geteuid()]
                    active_descendant = not fields[3].startswith("Z")
                else:
                    assert current.returncode == 1
            if active_leader or active_descendant:
                try:
                    original_killpg(leader.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
            leader.wait(timeout=5)


def test_unreaped_fatal_cleanup_keeps_unknown_raw_child_status(tmp_path, monkeypatch):
    from tests import run_checks as r
    import errno, json, signal
    group = max(os.getpid(), os.getpgrp()) + 100000
    class Child:
        pid = group
        returncode = None
        def wait(self, *, timeout):
            raise subprocess.TimeoutExpired("owned-fixture", timeout)
    monkeypatch.setattr(r.subprocess, "Popen", lambda *a, **k: Child())
    def killpg(pgid, sig):
        assert pgid == group
        if sig == signal.SIGKILL:
            raise PermissionError(errno.EPERM, "injected inability to establish terminal leader")
    monkeypatch.setattr(r.os, "killpg", killpg)
    monkeypatch.setattr(r.subprocess, "run", lambda *a, **k: pytest.fail("an unreaped leader cannot use metadata fallback"))
    checks = r.Checks(tmp_path, True); later = []
    with pytest.raises(r.FatalSetup):
        checks.run("owned", lambda: r.command(["owned-fixture"], tmp_path / "unknown.log", {}, timeout=17))
        checks.run("later", lambda: later.append(True))
    status = json.loads((tmp_path / "unknown.status.json").read_text())
    assert status["timed_out"] is True and status["exit_code"] is None and status["signal"] is None
    assert not later and not checks.failures
