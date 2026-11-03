#!/usr/bin/env python3
"""Leaf handlers for the root Make authority. No aggregate or dependency installation."""
import argparse
import ast
from contextlib import contextmanager
import errno
import hashlib
import importlib.metadata
import json
import os
from pathlib import Path
import platform
import re
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
import threading
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
IDENTITY = ".haeryong-test-run.json"
PROGRESS_INTERVAL = 30


def scratch_parent():
    volume = Path("/Volumes/RootKernel")
    if os.path.ismount(volume) and os.access(volume, os.W_OK):
        parent = volume / "tmp"
        parent.mkdir(mode=0o700, exist_ok=True)
        return parent
    return Path(tempfile.gettempdir())


def new_run():
    root = Path(tempfile.mkdtemp(prefix="haeryong-tests-", dir=scratch_parent()))
    value = {"kind": "haeryong-test-run/v1", "repository": str(ROOT), "uid": os.getuid(), "run_id": uuid.uuid4().hex}
    with (root / IDENTITY).open("x") as stream:
        os.chmod(root / IDENTITY, 0o600)
        json.dump(value, stream)
    return root


def run_root():
    selected = os.environ.get("HAERYONG_TEST_RUN_ROOT")
    if not selected:
        return new_run()
    root = Path(selected)
    if not root.is_absolute() or any(path.is_symlink() for path in [root, *root.parents]):
        raise RuntimeError("Test run root must be an absolute non-symlink path")
    info = root.stat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise RuntimeError("Test run root must be a private directory owned by this user")
    marker = root / IDENTITY
    if not marker.is_file() or marker.is_symlink():
        raise RuntimeError("Refusing unverified test run root")
    value = json.loads(marker.read_text())
    if value.get("kind") != "haeryong-test-run/v1" or value.get("repository") != str(ROOT) or value.get("uid") != os.getuid():
        raise RuntimeError("Test run identity does not match this repository/user")
    return root


def toolchain():
    if sys.version_info < (3, 11):
        raise RuntimeError("Python 3.11 or newer is required")
    required = re.search(r"^go (\S+)$", (ROOT / "go.mod").read_text(), re.M)[1]
    actual = subprocess.check_output(["go", "env", "GOVERSION"], text=True).strip()
    if actual != "go" + required:
        raise RuntimeError(f"Required Go toolchain: {required}")
    for package, minimum in [("pytest", (9, 1, 1)), ("PyYAML", (6, 0, 3))]:
        try:
            installed = importlib.metadata.version(package)
        except importlib.metadata.PackageNotFoundError:
            raise RuntimeError(f"{package} is missing from {sys.executable}. Run 'make test-deps', then 'make test', or select a prepared PYTHON.") from None
        match = re.match(r"^(\d+)\.(\d+)(?:\.(\d+))?", installed)
        if not match or tuple(int(n or 0) for n in match.groups()) < minimum:
            raise RuntimeError(f"Required {package}>={'.'.join(map(str, minimum))}")


class CheckFailure(RuntimeError):
    """A completed check failed; independent checks may still run."""


class FatalSetup(RuntimeError):
    """Safe execution cannot be established or owned cleanup is uncertain."""


class RunInterrupted(BaseException):
    def __init__(self, signum):
        self.signum = signum


def interrupt(signum, _frame):
    raise RunInterrupted(signum)


def owned_group_is_drained(pgid):
    # Only the current metadata snapshot is inspected: no argv, historical PID
    # signals or conclusion from the direct leader alone. Any unknown snapshot
    # is fatal, so keep-going cannot schedule more work after uncertain cleanup.
    if type(pgid) is not int or pgid <= 0 or pgid == os.getpgrp():
        raise FatalSetup("Invalid owned process group for cleanup observation")
    try:
        observed = subprocess.run(["ps", "-A", "-o", "pid=,pgid=,uid=,stat="],
                                  capture_output=True, encoding="ascii", timeout=5,
                                  env=os.environ | {"LC_ALL": "C"})
    except (OSError, subprocess.SubprocessError, UnicodeError) as error:
        raise FatalSetup("Cannot observe current owned process group") from error
    if observed.returncode != 0 or observed.stderr or not observed.stdout or not observed.stdout.endswith("\n"):
        raise FatalSetup("Incomplete current process metadata for cleanup")
    members, seen, caller = [], set(), False
    for line in observed.stdout.splitlines():
        fields = line.split()
        if len(fields) != 4:
            raise FatalSetup("Malformed current process metadata for cleanup (field count)")
        if (any(not re.fullmatch(r"[0-9]+", field) for field in fields[:2])
                or not re.fullmatch(r"-?[0-9]+", fields[2])):
            raise FatalSetup("Malformed current process metadata for cleanup (numeric identity)")
        try:
            pid, group, uid = map(int, fields[:3])
        except ValueError as error:
            raise FatalSetup("Invalid numeric process metadata for cleanup") from error
        # Native ps can render an unrelated effective UID as signed decimal.
        # It is never the current owner unless it equals the actual effective UID.
        state = fields[3]
        # State is relevant only to the owned group and this caller witness.
        # Unrelated states are opaque; all rows still require structural and
        # numeric integrity, and globally unique process identities.
        if (group == pgid or pid == os.getpid()) and not re.fullmatch(r"[RSDTtIWUXZ][<ANLSsl+EVXW>]*", state):
            raise FatalSetup("Malformed current process metadata for cleanup (relevant state)")
        if pid in seen:
            raise FatalSetup("Duplicate current process metadata for cleanup")
        seen.add(pid)
        if pid == os.getpid():
            caller = group == os.getpgrp() and uid == os.geteuid() and not state.startswith("Z")
        if group == pgid:
            members.append((uid, state))
    # ps -A must include this still-running caller. A successful exit alone
    # cannot turn empty, partial or unparseable output into proof of absence.
    if not caller:
        raise FatalSetup("Current process metadata lacks the cleanup caller")
    return all(uid == os.geteuid() and state.startswith("Z") for uid, state in members)


def stop_children(process):
    # Each command owns a new process group, including descendants whose direct
    # parent already exited. Repeated cancellation must not interrupt draining.
    handlers = {sig: signal.signal(sig, signal.SIG_IGN) for sig in (signal.SIGINT, signal.SIGTERM)}
    try:
        for sig in (signal.SIGTERM, signal.SIGKILL):
            try:
                os.killpg(process.pid, sig)
            except ProcessLookupError:
                break
            except PermissionError as error:
                if sig != signal.SIGKILL or error.errno != errno.EPERM:
                    raise
                # The error itself proves no postcondition: kernels may deny
                # signals to zombie-only groups, or deny genuinely live work.
                # A successful zero-wait reaps/establishes the owned direct
                # leader's terminal state before any metadata-only fallback.
                process.wait(timeout=0)
                if process.returncode is None or not owned_group_is_drained(process.pid):
                    raise
                break
            if sig == signal.SIGTERM:
                try:
                    process.wait(timeout=1)
                except subprocess.TimeoutExpired:
                    pass
        process.wait(timeout=5)
    except (OSError, subprocess.SubprocessError) as error:
        raise FatalSetup("Cannot establish owned child cleanup") from error
    finally:
        for sig, handler in handlers.items():
            signal.signal(sig, handler)
    # Orphan zombies await OS reaping; SIGKILL prevents runnable survivors.


@contextmanager
def command_progress(output, timeout, progress):
    """Show observation only; the original child wait owns the deadline/result."""
    started = time.monotonic()
    stopped = threading.Event()
    offset, pending = 0, b""

    def milestones():
        nonlocal offset, pending
        if progress is None:
            return
        try:
            with progress.open("rb") as stream:
                stream.seek(offset)
                data = stream.read()
        except FileNotFoundError:
            return  # Pytest creates this metadata stream during configuration.
        offset += len(data)
        lines = (pending + data).split(b"\n")
        pending = lines.pop()
        for line in lines:
            print(line.decode("utf-8", errors="replace"), flush=True)

    def observe():
        next_report = started + PROGRESS_INTERVAL
        while True:
            finished = stopped.wait(min(1, PROGRESS_INTERVAL))
            try:
                milestones()
                now = time.monotonic()
                if not finished and now >= next_report:
                    info = output.stat()
                    quiet = max(0, time.time() - info.st_mtime)
                    print(f"WAIT {output.stem}: elapsed={now-started:.0f}s; "
                          f"timeout={timeout}s; log unchanged={quiet:.0f}s", flush=True)
                    next_report = now + PROGRESS_INTERVAL
            except OSError as error:
                # Progress observation must not replace the actual child result.
                print(f"Progress unavailable for {output.stem}: {type(error).__name__}", flush=True)
            if finished:
                return

    observer = threading.Thread(target=observe, name="test-progress", daemon=True)
    try:
        observer.start()
    except RuntimeError:
        # An unavailable observer must not leave a launched child unwaited.
        yield
        return
    try:
        yield
    finally:
        stopped.set()
        observer.join()


def command(args, output, env, *, timeout=120, progress=None):
    result = {"argv": args, "exit_code": None, "signal": None, "timed_out": False}
    child = None
    try:
        with output.open("xb") as stream:
            print(f"START {output.stem}: timeout={timeout}s; private log={output}", flush=True)
            try:
                child = subprocess.Popen(args, cwd=ROOT, env=env, stdout=stream,
                                         stderr=subprocess.STDOUT, start_new_session=True)
            except OSError as error:
                raise FatalSetup(f"Cannot start required tool: {args[0]}") from error
            try:
                with command_progress(output, timeout, progress):
                    result["exit_code"] = child.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                result["timed_out"] = True
                stop_children(child)
                result["exit_code"] = child.returncode
            except (RunInterrupted, KeyboardInterrupt):
                stop_children(child)
                result["exit_code"] = child.returncode
                raise
            if child.returncode is not None and child.returncode < 0:
                result["signal"] = -child.returncode
            if result["signal"] in (signal.SIGINT, signal.SIGTERM) and not result["timed_out"]:
                stop_children(child)
                raise RunInterrupted(result["signal"])
    finally:
        # Cleanup control may fail after the direct child has already reaped.
        # Preserve that known raw outcome independently; unknown stays unknown.
        if result["exit_code"] is None and child is not None and child.returncode is not None:
            result["exit_code"] = child.returncode
        if result["exit_code"] is not None and result["exit_code"] < 0:
            result["signal"] = -result["exit_code"]
        pending = sys.exc_info()[1]
        try:
            output.with_suffix(".status.json").write_text(json.dumps(result, indent=2)+"\n")
        except OSError as error:
            if isinstance(pending, (RunInterrupted, KeyboardInterrupt, FatalSetup)):
                print("Child status could not be recorded during stop", file=sys.stderr)
            else:
                raise FatalSetup("Cannot retain required child evidence") from error
    print(f"{' '.join(args[:4])}: exit={result['exit_code']}; timeout={result['timed_out']}; private log={output}", flush=True)
    if result["exit_code"] or result["timed_out"]:
        raise CheckFailure(f"Required command failed: {args[0]} (exit {result['exit_code']}, timeout {result['timed_out']})")


class Checks:
    def __init__(self, work, keep_going):
        self.work, self.keep_going, self.failures = work, keep_going, []

    def run(self, name, check):
        try:
            check()
        except FatalSetup:
            raise
        except (RuntimeError, OSError, ValueError, SyntaxError, subprocess.SubprocessError) as error:
            failure = {"check": name, "error": str(error)}
            self.failures.append(failure)
            path = self.work / "failures.json"
            try:
                recorded = json.loads(path.read_text()) if path.exists() else []
                recorded.append(failure)
                path.write_text(json.dumps(recorded, indent=2)+"\n")
            except (OSError, ValueError) as error:
                raise FatalSetup("Cannot retain required failure evidence") from error
            print(f"Required check failed: {name}: {error}", file=sys.stderr, flush=True)
            if not self.keep_going:
                raise CheckFailure(str(error)) from error



def go_packages(layer):
    packages = set()
    for base in ("cmd", "internal", "tests/integration"):
        for path in (ROOT / base).rglob("*_test.go"):
            text = path.read_text()
            if f'Label("{layer}")' in text:
                packages.add("./" + str(path.parent.relative_to(ROOT)))
    if not packages:
        raise RuntimeError(f"No meaningful Go suite selected for {layer}")
    return sorted(packages)


def go_suite(layer, work, env, keep_going=False):
    report = work / f"go-{layer}.json"
    # Ginkgo applies one deadline to compilation and all selected package suites.
    # The persistence bound is about 3x the slowest retained complete PASS interval.
    budget = 8400 if layer == "persistence" else 1000
    args = ["go", "tool", "ginkgo", "--race", "--fail-on-empty", "--fail-on-pending", "--no-color",
            f"--timeout={budget}s", f"--label-filter={layer}", f"--json-report={report}", *go_packages(layer)]
    if keep_going:
        args.insert(3, "--keep-going")
    failure = None
    try:
        command(args, work / f"go-{layer}.log", env | {"HAERYONG_TEST_LAYER": layer}, timeout=budget + 500)
    except CheckFailure as error:
        failure = error
    try:
        reports = json.loads(report.read_text())
    except (OSError, ValueError) as error:
        raise CheckFailure(f"Required {layer} report unavailable or malformed; child failure={failure}") from error
    if not isinstance(reports, list) or not reports:
        raise CheckFailure("Required Go report must contain suites")
    suite_failures = []
    for suite in reports:
        if not isinstance(suite, dict):
            raise CheckFailure("Required Go suite report is malformed")
        reasons = suite.get("SpecialSuiteFailureReasons") or []
        if not isinstance(reasons, list) or any(not isinstance(reason, str) for reason in reasons):
            raise CheckFailure("Required Go suite failure reasons are malformed")
        if reasons:
            suite_failures.append(f"{suite.get('SuitePath', '<unknown>')}: {'; '.join(reasons)}")
        specs = suite.get("SpecReports")
        if specs is None and suite.get("SuiteSucceeded") is False and reasons:
            # Ginkgo emits null specs for a suite it could not start. It still fails.
            continue
        if not isinstance(specs, list):
            raise CheckFailure("Required Go suite report is malformed")
        for spec in specs:
            if not isinstance(spec, dict) or not isinstance(spec.get("State"), str):
                raise CheckFailure("Required Go spec report is malformed")
            labels, containers = spec.get("LeafNodeLabels") or [], spec.get("ContainerHierarchyLabels") or []
            if not isinstance(labels, list) or any(not isinstance(label, str) for label in labels):
                raise CheckFailure("Required Go spec labels are malformed")
            if not isinstance(containers, list) or any(not isinstance(group, list) or any(not isinstance(label, str) for label in group) for group in containers):
                raise CheckFailure("Required Go container labels are malformed")
    if suite_failures:
        raise CheckFailure(f"Required {layer} suites failed or did not run: "
                           + " | ".join(suite_failures) + f"; child failure={failure}")
    selected = [spec for suite in reports for spec in suite["SpecReports"]
                if layer in (spec.get("LeafNodeLabels") or []) or any(layer in labels for labels in (spec.get("ContainerHierarchyLabels") or []))]
    if failure or not selected or any(spec["State"] != "passed" for spec in selected):
        raise CheckFailure(f"Required {layer} specs were absent, failed, pending or skipped")
    print(f"{layer}: {len(selected)} selected specs passed; filtered other layers are not required skips", flush=True)


def prepare(work, env, keep_going=False):
    checks = Checks(work, keep_going)
    sources = sorted(str(path.relative_to(ROOT)) for base in ("cmd", "internal", "tests")
                     for path in (ROOT / base).rglob("*.go"))
    def formatting():
        log = work / "format.log"
        command(["gofmt", "-l" if keep_going else "-w", *sources], log, env)
        if keep_going and log.stat().st_size:
            raise CheckFailure("Go sources need formatting (no source was rewritten)")
    checks.run("format", formatting)
    python = [ROOT / "conftest.py", *sorted((ROOT / "tests").rglob("*.py")),
              *sorted((ROOT / "docs/testing").rglob("*.py"))]
    for path in python:
        checks.run("syntax:"+str(path.relative_to(ROOT)), lambda path=path: ast.parse(path.read_text(), filename=str(path)))
    checks.run("layers", validate_layers)
    checks.run("docs", lambda: command([sys.executable, "-B", "docs/testing/validate_docs.py"], work / "docs.log", env))
    manifest = []
    def read_manifest():
        value = json.loads((ROOT / "tests/contracts/manifest.json").read_text())
        if not isinstance(value, dict) or not isinstance(value.get("files"), list):
            raise CheckFailure("Selected contract manifest files must be an array")
        manifest.extend(value["files"])
    checks.run("contract-manifest", read_manifest)
    for index, row in enumerate(manifest):
        def digest(row=row):
            if (not isinstance(row, dict) or not isinstance(row.get("path"), str)
                    or type(row.get("bytes")) is not int or not isinstance(row.get("sha256"), str)):
                raise CheckFailure("Selected contract manifest row is malformed")
            data = (ROOT / row["path"]).read_bytes()
            if hashlib.sha256(data).hexdigest() != row["sha256"] or len(data) != row["bytes"]:
                raise CheckFailure("Selected contract digest differs: " + row["path"])
        name = row.get("path", str(index)) if isinstance(row, dict) else str(index)
        checks.run("digest:"+str(name), digest)
    checks.run("modules", lambda: command(["go", "mod", "verify"], work / "modules.log", env))
    checks.run("vet", lambda: command(["go", "vet", "./..."], work / "vet.log", env))
    def dependencies():
        log = work / "dependencies.log"
        command(["go", "list", "-deps", "./internal/domain", "./internal/application/..."], log, env)
        for name in log.read_text().splitlines():
            if name in {"database/sql", "net/http", "modernc.org/sqlite"} or name.startswith(("haeryong.local/haeryong/internal/adapters/", "github.com/wailsapp/")):
                raise CheckFailure("Domain/application dependency boundary violated: " + name)
    checks.run("dependencies", dependencies)
    for target_os in ("darwin", "linux"):
        for target_arch in ("arm64", "amd64"):
            name = f"haeryong-{target_os}-{target_arch}"
            checks.run(name, lambda name=name, target_os=target_os, target_arch=target_arch:
                       command(["go", "build", "-o", str(work / name), "./cmd/haeryong"], work / f"{name}.log",
                               env | {"CGO_ENABLED": "0", "GOOS": target_os, "GOARCH": target_arch}))
    if checks.failures:
        raise CheckFailure("Required prepare checks failed")


def validate_layers():
    allowed = {"unit", "integration", "persistence"}
    for base in ("cmd", "internal", "tests/integration"):
        for path in (ROOT / base).rglob("*_test.go"):
            text = path.read_text()
            registrations = re.findall(r'var _ = It\("([^"]+)", Label\("([^"]+)"\)', text)
            if text.count("var _ = It(") != len(registrations) or any(layer not in allowed for _, layer in registrations):
                raise RuntimeError("Every Go scenario needs one explicit layer: " + str(path.relative_to(ROOT)))
            if path.name != "suite_test.go" and "func Test" in text:
                raise RuntimeError("Legacy Go test entrypoint remains: " + str(path.relative_to(ROOT)))
    # Boundary checks are structural guards, not evidence of semantic coverage.
    for layer in ("unit", "integration"):
        for package in go_packages(layer):
            for path in (ROOT / package[2:]).glob("*_test.go"):
                text = path.read_text()
                if f'Label("{layer}")' in text and any(value in text for value in ('"modernc.org/sqlite"', 'foundation.New(', 'sqlite.New(')):
                    raise RuntimeError("Real database dependency in isolated layer: " + str(path.relative_to(ROOT)))


def pytest_suite(layer, paths, work, env, keep_going=False):
    progress = work / f"pytest-{layer}.progress.log"
    args = [sys.executable, "-B", "-m", "pytest", "--strict-config", "--strict-markers", "-W", "error",
            "--haeryong-progress", str(progress),
            "--basetemp", str(work / "pytest-tmp"), f"--junitxml={work / ('pytest-'+layer+'.xml')}", *paths]
    if not keep_going:
        args.insert(4, "-x")
    command(args, work / f"pytest-{layer}.log", env, timeout=5400 if layer == "e2e" else 1800,
            progress=progress)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("stage", choices=["new-run", "prepare", "unit", "integration", "persistence", "e2e"])
    parser.add_argument("--keep-going", action="store_true", help="continue independent checks; retain every failure")
    args = parser.parse_args()
    if args.stage == "new-run":
        print(new_run())
        return 0
    if os.environ.get("PYTEST_ADDOPTS"):
        raise RuntimeError("PYTEST_ADDOPTS must be unset for the canonical required stages")
    os.umask(0o077)
    env = os.environ | {"GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GOFLAGS": "-mod=readonly",
                        "PYTEST_DISABLE_PLUGIN_AUTOLOAD": "1", "PYTHONDONTWRITEBYTECODE": "1"}
    os.environ.update({key: env[key] for key in ("GOTOOLCHAIN", "GOPROXY", "GOSUMDB", "GOFLAGS")})
    toolchain()
    host_os, host_arch = subprocess.check_output(["go", "env", "GOHOSTOS", "GOHOSTARCH"], env=env, text=True).splitlines()
    env.update({"GOOS": host_os, "GOARCH": host_arch, "CGO_ENABLED": "1"})
    root = run_root()
    work = root / args.stage
    work.mkdir(mode=0o700)
    (work / "tmp").mkdir(mode=0o700)
    env["TMPDIR"] = str(work / "tmp")
    env["HAERYONG_DOCS_TEST_TMP_ROOT"] = str(work / "tmp")
    env["HAERYONG_REQUIRED_TEST_STAGE"] = args.stage
    env["HAERYONG_CONTRACT_REPORT"] = str(work / "contracts.json")
    env["HAERYONG_TASK006_CRASH_REPORT"] = str(work / "crash-boundaries.jsonl")
    env["HAERYONG_TASK006_REAL_INPUT_REPORT"] = str(work / "real-inputs.json")
    env["HAERYONG_TASK004_CROSSVOLUME_REPORT"] = str(work / "cross-volume.json")
    env["HAERYONG_TASK005_WORKLOAD_REPORT"] = str(work / "f12.json")
    env["HAERYONG_TASK004_WORKLOAD_REPORT"] = str(work / "f12-http.json")
    (work / "environment.json").write_text(json.dumps({"platform": platform.platform(), "python": platform.python_version(), "stage": args.stage,
                                                      "scope": "Headless local tests only; no browser, provider, remote CI or AMD64 runtime claim"}, indent=2)+"\n")
    print(f"Stage {args.stage}; private artifacts={work}", flush=True)
    checks = Checks(work, args.keep_going)
    if args.stage == "prepare":
        checks.run("prepare", lambda: prepare(work, env, args.keep_going))
    elif args.stage in {"unit", "persistence"}:
        checks.run("go-"+args.stage, lambda: go_suite(args.stage, work, env, args.keep_going))
        if args.stage == "unit":
            checks.run("pytest-unit", lambda: pytest_suite("unit", ["tests/unit"], work, env, args.keep_going))
    elif args.stage == "integration":
        checks.run("go-integration", lambda: go_suite("integration", work, env, args.keep_going))
        checks.run("pytest-integration", lambda: pytest_suite("integration", ["tests/contracts", "docs/testing/test_validate_docs.py"], work, env, args.keep_going))
    else:
        host = (platform.system(), platform.machine())
        if host not in {("Darwin", "arm64"), ("Linux", "aarch64"), ("Linux", "arm64")}:
            raise RuntimeError("Required E2E native runtime: macOS or Linux ARM64")
        compiler = subprocess.check_output(["go", "env", "CC"], text=True).strip()
        if not shutil.which(compiler):
            raise RuntimeError("Native C compiler is required for race instrumentation")
        checks.run("pytest-e2e", lambda: pytest_suite("e2e", ["tests/e2e"], work, env, args.keep_going))
    if checks.failures:
        return 1
    print(f"Stage {args.stage} passed", flush=True)
    return 0


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, interrupt)
    signal.signal(signal.SIGINT, interrupt)
    try:
        raise SystemExit(main())
    except RunInterrupted as error:
        raise SystemExit(128 + error.signum)
    except CheckFailure as error:
        print(f"Required check failed: {error}", file=sys.stderr)
        raise SystemExit(1)
    except (RuntimeError, OSError, subprocess.SubprocessError, importlib.metadata.PackageNotFoundError) as error:
        print(f"Required test prerequisite/check failed: {error}", file=sys.stderr)
        raise SystemExit(2 if "--keep-going" in sys.argv else 1)
