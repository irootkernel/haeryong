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
