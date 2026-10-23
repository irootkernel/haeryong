#!/usr/bin/env python3
"""Leaf handlers for the root Make authority. No aggregate or dependency installation."""
import argparse
import ast
import hashlib
import importlib.metadata
import json
import os
from pathlib import Path
import platform
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import uuid

ROOT = Path(__file__).resolve().parents[1]
IDENTITY = ".haeryong-test-run.json"


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


def command(args, output, env, *, timeout=120):
    # Logs stay private; arbitrary product output is never copied into summaries.
    with output.open("xb") as stream:
        result = subprocess.run(args, cwd=ROOT, env=env, stdout=stream, stderr=subprocess.STDOUT, timeout=timeout)
    print(f"{' '.join(args[:4])}: exit={result.returncode}; private log={output}", flush=True)
    if result.returncode:
        raise RuntimeError(f"Required command failed: {args[0]} (exit {result.returncode})")


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


def go_suite(layer, work, env):
    report = work / f"go-{layer}.json"
    # Budgets bound hung runs; the serial race persistence suites vary with host load.
    budget = 3600 if layer == "persistence" else 1000
    args = ["go", "tool", "ginkgo", "--race", "--fail-on-empty", "--fail-on-pending", "--no-color",
            f"--timeout={budget}s", f"--label-filter={layer}", f"--json-report={report}", *go_packages(layer)]
    command(args, work / f"go-{layer}.log", env | {"HAERYONG_TEST_LAYER": layer}, timeout=budget + 500)
    reports = json.loads(report.read_text())
    selected = [spec for suite in reports for spec in suite["SpecReports"]
                if layer in (spec.get("LeafNodeLabels") or []) or any(layer in labels for labels in (spec.get("ContainerHierarchyLabels") or []))]
    if not selected or any(spec["State"] != "passed" for spec in selected):
        raise RuntimeError(f"Required {layer} specs were absent, failed, pending or skipped")
    print(f"{layer}: {len(selected)} selected specs passed; filtered other layers are not required skips", flush=True)


def prepare(work, env):
    sources = sorted(str(path.relative_to(ROOT)) for base in ("cmd", "internal", "tests")
                     for path in (ROOT / base).rglob("*.go"))
    command(["gofmt", "-w", *sources], work / "format.log", env)
    ast.parse((ROOT / "conftest.py").read_text(), filename="conftest.py")
    for base in (ROOT / "tests", ROOT / "docs/testing"):
        for path in base.rglob("*.py"):
            ast.parse(path.read_text(), filename=str(path))
    validate_layers()
    command([sys.executable, "-B", "docs/testing/validate_docs.py"], work / "docs.log", env)
    manifest = json.loads((ROOT / "tests/contracts/manifest.json").read_text())
    for row in manifest["files"]:
        data = (ROOT / row["path"]).read_bytes()
        if hashlib.sha256(data).hexdigest() != row["sha256"] or len(data) != row["bytes"]:
            raise RuntimeError("Selected contract digest differs: " + row["path"])
    command(["go", "mod", "verify"], work / "modules.log", env)
    command(["go", "vet", "./..."], work / "vet.log", env)
    dependencies = subprocess.check_output(["go", "list", "-deps", "./internal/domain", "./internal/application/..."], cwd=ROOT, env=env, text=True)
    for name in dependencies.splitlines():
        if name in {"database/sql", "net/http", "modernc.org/sqlite"} or name.startswith(("haeryong.local/haeryong/internal/adapters/", "github.com/wailsapp/")):
            raise RuntimeError("Domain/application dependency boundary violated: " + name)
    for target_os in ("darwin", "linux"):
        for target_arch in ("arm64", "amd64"):
            name = f"haeryong-{target_os}-{target_arch}"
            command(["go", "build", "-o", str(work / name), "./cmd/haeryong"], work / f"{name}.log",
                    env | {"CGO_ENABLED": "0", "GOOS": target_os, "GOARCH": target_arch})


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


def pytest_suite(layer, paths, work, env):
    args = [sys.executable, "-B", "-m", "pytest", "-x", "--strict-config", "--strict-markers", "-W", "error",
            "--basetemp", str(work / "pytest-tmp"), f"--junitxml={work / ('pytest-'+layer+'.xml')}", *paths]
    command(args, work / f"pytest-{layer}.log", env, timeout=5400 if layer == "e2e" else 1800)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("stage", choices=["new-run", "prepare", "unit", "integration", "persistence", "e2e"])
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
    if args.stage == "prepare":
        prepare(work, env)
    elif args.stage in {"unit", "persistence"}:
        go_suite(args.stage, work, env)
        if args.stage == "unit":
            pytest_suite("unit", ["tests/unit"], work, env)
    elif args.stage == "integration":
        go_suite("integration", work, env)
        pytest_suite("integration", ["tests/contracts", "docs/testing/test_validate_docs.py"], work, env)
    else:
        host = (platform.system(), platform.machine())
        if host not in {("Darwin", "arm64"), ("Linux", "aarch64"), ("Linux", "arm64")}:
            raise RuntimeError("Required E2E native runtime: macOS or Linux ARM64")
        compiler = subprocess.check_output(["go", "env", "CC"], text=True).strip()
        if not shutil.which(compiler):
            raise RuntimeError("Native C compiler is required for race instrumentation")
        pytest_suite("e2e", ["tests/e2e"], work, env)
    print(f"Stage {args.stage} passed", flush=True)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (RuntimeError, OSError, subprocess.SubprocessError, importlib.metadata.PackageNotFoundError) as error:
        print(f"Required test prerequisite/check failed: {error}", file=sys.stderr)
        raise SystemExit(1)
