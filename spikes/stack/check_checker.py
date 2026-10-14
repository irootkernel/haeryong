"""Exercise the real checker control flow with synthetic browser/transport faults."""

import ast
import argparse
import json
import os
from pathlib import Path
import shutil
import shlex
import subprocess
import sys
import tempfile
import uuid
from types import SimpleNamespace

root = Path(__file__).resolve().parent
parser = argparse.ArgumentParser()
parser.add_argument("--browser-cli", help="Also verify isolation with two owned real browser sessions")
options = parser.parse_args()
temporary_root = Path(tempfile.gettempdir())
if os.path.ismount("/Volumes/RootKernel") and os.access("/Volumes/RootKernel", os.W_OK):
    temporary_root = Path("/Volumes/RootKernel/tmp")
    temporary_root.mkdir(parents=True, exist_ok=True)

source = ast.parse((root / "check_lifecycle.py").read_text())
writer = next(node for node in source.body if isinstance(node, ast.FunctionDef) and node.name == "write_result")
branch = next(node for node in source.body if isinstance(node, ast.If)
              and isinstance(node.test, ast.Attribute) and node.test.attr == "browser_cli"
              and any(isinstance(child, ast.Try) for child in node.body))
operation = next(node for node in branch.body if isinstance(node, ast.Try))
code = compile(ast.fix_missing_locations(ast.Module(body=[writer, operation], type_ignores=[])),
               "checker-browser-control-flow", "exec")

with tempfile.TemporaryDirectory(prefix="hy-check-", dir=temporary_root) as owned:
    directory = Path(owned)
    cases = [(True, False, False), (False, True, False), (False, False, True), (False, False, False)]
    for number, (fail_close, fail_browser, fail_preflight) in enumerate(cases):
        output = directory / str(number)
        output.mkdir(mode=0o700)
        browser_directory = output / "browser"
        browser_directory.mkdir(mode=0o700)
        calls = []

        def cli(argv):
            calls.append(argv[0])
            if argv[0] == "close" and fail_close:
                raise RuntimeError("controlled close failure")
            if argv[0] == "run-code" and fail_browser:
                raise RuntimeError("controlled browser failure")
            return '{"passed":true}'

        def preflight(*unused_args, **unused_kwargs):
            if fail_preflight:
                raise RuntimeError("controlled preflight failure")

        outcomes = [{"client_exit": 0}, {"client_exit": -9}]
        scope = {"json": json, "shutil": shutil, "args": SimpleNamespace(output=output, browser_cli="synthetic"),
                 "subprocess": SimpleNamespace(run=preflight), "sys": sys, "Path": Path,
                 "__file__": str(root / "check_lifecycle.py"),
                 "identity": {"fixture": "synthetic-control-flow-only"}, "outcomes": outcomes,
                 "browser": "incomplete", "browser_cleanup": "not_run",
                 "browser_directory": browser_directory, "session": "synthetic",
                 "cli": cli, "script": ""}
        try:
            exec(code, scope)
            assert not fail_close and not fail_browser and not fail_preflight, "failure was suppressed"
        except RuntimeError as error:
            expected = "controlled close failure" if fail_close else (
                "controlled browser failure" if fail_browser else "controlled preflight failure")
            assert str(error) == expected
        result = json.loads((output / "lifecycle-result.json").read_text())
        assert result["outcomes"] == outcomes and calls[-1] == "close"
        assert result["browser_directory"] == str(browser_directory)
        assert result["browser_session"] == "synthetic"
        assert result["status"] == ("failed" if fail_close else "incomplete" if fail_browser or fail_preflight else "pass")
        assert result["browser_cleanup"] == ("failed" if fail_close else "pass")
        assert browser_directory.exists() == fail_close, "failed cleanup must retain the owned recovery directory"

    source = ast.parse((root / "check.py").read_text())
    container = next(node.value for node in source.body if isinstance(node, ast.Assign)
                     and any(isinstance(target, ast.Name) and target.id == "container" for target in node.targets))
    script = container.elts[-1].value
    start = script.index("health=;")
    end = script.index("wget -qO-", script.index("printf '%s", start))
    probe = script[start:end]
    runtime = directory / "runtime"
    runtime.mkdir(mode=0o700)
    (runtime / "endpoint").write_text("http://127.0.0.1:1")
    binaries = directory / "bin"
    binaries.mkdir(mode=0o700)
    wget = binaries / "wget"
    wget.write_text('#!/bin/sh\nprintf x >> "$READINESS_CALLS"\nprintf "controlled transport failure\\n" >&2\nexit 1\n')
    wget.chmod(0o700)
    sleep = binaries / "sleep"
    sleep.write_text("#!/bin/sh\nexit 0\n")
    sleep.chmod(0o700)
    calls = directory / "readiness-calls"
    checked = subprocess.run(["sh", "-ec", probe.replace("/tmp/run", shlex.quote(str(runtime)))],
                             capture_output=True, text=True, timeout=5,
                             env=os.environ | {"PATH": str(binaries) + os.pathsep + os.environ["PATH"],
                                               "READINESS_CALLS": str(calls)})
    assert checked.returncode != 0 and calls.read_text() == "xxxxx"
    assert "after 5 attempts" in checked.stderr and "http://127.0.0.1:1" in checked.stderr
    assert "controlled transport failure" in checked.stderr

    # The endpoint appears after the first read; moving the read outside the loop must fail.
    (runtime / "endpoint").unlink()
    late_calls = directory / "late-readiness-calls"
    wget.write_text('#!/bin/sh\nprintf "%s\\n" "$2" >> "$READINESS_CALLS"\n'
                    'test "$2" = "http://127.0.0.1:2/health" || exit 1\n'
                    'printf \'{"fixture":"stack-spike"}\\n\'\n')
    sleep.write_text('#!/bin/sh\nprintf "%s" "http://127.0.0.1:2" > "$READINESS_ENDPOINT"\n')
    checked = subprocess.run(["sh", "-ec", probe.replace("/tmp/run", shlex.quote(str(runtime)))],
                             capture_output=True, text=True, timeout=5,
                             env=os.environ | {"PATH": str(binaries) + os.pathsep + os.environ["PATH"],
                                               "READINESS_CALLS": str(late_calls),
                                               "READINESS_ENDPOINT": str(runtime / "endpoint")})
    assert checked.returncode == 0, checked.stderr
    assert json.loads(checked.stdout) == {"fixture": "stack-spike"}
    assert late_calls.read_text().splitlines() == ["http://127.0.0.1:2/health"]

    if options.browser_cli:
        # Execute the actual controller's namespace setup, then deliberately reuse a name.
        setup = []
        for node in branch.body:
            if isinstance(node, ast.Assign) and any(isinstance(target, ast.Name) and target.id in
                    {"temporary_root", "browser_directory", "session", "prefix", "cli_env"}
                    for target in node.targets):
                setup.append(node)
            elif isinstance(node, ast.If) and any(isinstance(child, ast.Assign) and any(
                    isinstance(target, ast.Name) and target.id == "temporary_root" for target in child.targets)
                    for child in ast.walk(node)):
                setup.append(node)
        initialize = compile(ast.fix_missing_locations(ast.Module(body=setup, type_ignores=[])),
                             "checker-browser-namespace", "exec")
        sessions = []
        closed = set()

        def browser_cli(number, argv):
            session = sessions[number]
            result = subprocess.run(session["prefix"] + argv, cwd=directory, env=session["cli_env"],
                                    capture_output=True, text=True, timeout=60)
            assert result.returncode == 0 and "### Error" not in result.stdout, result.stdout + result.stderr
            return result.stdout

        try:
            for number in range(2):
                scope = {"os": os, "Path": Path, "tempfile": tempfile, "uuid": uuid,
                         "args": SimpleNamespace(browser_cli=options.browser_cli)}
                exec(initialize, scope)
                sessions.append(scope)
                if number:
                    scope["prefix"][1] = sessions[0]["prefix"][1]
                browser_cli(number, ["open", "about:blank"])
                browser_cli(number, ["run-code", f'async (page) => await page.evaluate(() => window.name = "namespace-{number}")'])
            assert '"namespace-0"' in browser_cli(0, ["run-code", "async (page) => await page.evaluate(() => window.name)"])
            assert '"namespace-1"' in browser_cli(1, ["run-code", "async (page) => await page.evaluate(() => window.name)"])
            browser_cli(0, ["close"])
            closed.add(0)
            shutil.rmtree(sessions[0]["browser_directory"])
            assert '"namespace-1"' in browser_cli(1, ["run-code", "async (page) => await page.evaluate(() => window.name)"])
        finally:
            cleanup_errors = []
            for number, session in enumerate(sessions):
                if number not in closed:
                    try:
                        browser_cli(number, ["close"])
                        shutil.rmtree(session["browser_directory"])
                    except Exception as error:
                        print(f'Owned browser recovery directory: {session["browser_directory"]}')
                        cleanup_errors.append(error)
            if cleanup_errors:
                raise cleanup_errors[0]
        print("PASS: two actual CLI sessions with the same name stay separate; closing one preserves the other")

print("PASS: actual checker browser/cleanup and readiness failure paths; synthetic faults only")
