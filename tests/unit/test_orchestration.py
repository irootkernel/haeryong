"""Exercise the real Make aggregate with harmless substituted leaf commands."""
import os
from pathlib import Path
import subprocess
import sys

import pytest

pytestmark = pytest.mark.unit
STAGES = ["prepare", "unit", "integration", "persistence", "e2e"]


@pytest.mark.parametrize("failure", [None, *STAGES])
def test_parallel_make_is_serial_once_and_stops_at_the_first_failure(tmp_path, failure):
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
    result = subprocess.run(["make", "-j8", "test", f"PYTHON={leaf}"], cwd=tmp_path,
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
    env.pop("PYTHON", None)
    args = ["make", "test-unit"]
    if override:
        args.append(f"PYTHON={explicit}")
    result = subprocess.run(args, cwd=tmp_path, env=env, capture_output=True, timeout=10)
    assert result.returncode == 0
    assert (tmp_path / (tmp_path / "selected").read_text()).resolve() == (explicit if override else selected)
