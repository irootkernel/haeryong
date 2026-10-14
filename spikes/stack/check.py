"""Build the bounded stack spike and run SQLite on macOS and headless Linux."""

import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import uuid

parser = argparse.ArgumentParser()
parser.add_argument("--output", type=Path, required=True)
parser.add_argument("--linux-image", required=True, help="Existing Linux ARM64 image SHA-256")
args = parser.parse_args()
root = Path(__file__).resolve().parent
output = args.output.resolve()
output.mkdir(mode=0o700, parents=True, exist_ok=True)
assert output.stat().st_mode & 0o777 == 0o700, "output must be a private directory with mode 0700"


def run(argv, cwd=root, env=None, timeout=180):
    result = subprocess.run(argv, cwd=cwd, env=env, text=True, capture_output=True, timeout=timeout)
    print(result.stdout, end="")
    print(result.stderr, end="")
    result.check_returncode()
    return result.stdout


run([sys.executable, "-B", str(root / "check_checker.py")])
run(["npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund"], cwd=root / "frontend")
run(["npm", "run", "build"], cwd=root / "frontend")
env = os.environ | {"CGO_ENABLED": "0", "TMPDIR": str(output)}
host = json.loads(run(["go", "env", "-json", "GOOS", "GOARCH"], env=env))
assert host == {"GOOS": "darwin", "GOARCH": "arm64"}, "native proof requires the declared macOS ARM64 host"
run(["go", "test", "-count=1", "-v", "./..."], env=env)
run(["go", "build", "-o", str(output / "fixture"), "./cmd/fixture"], env=env)
for assets in [output / ("missing-" + uuid.uuid4().hex), root / "frontend"]:
    rejected = subprocess.run([
        str(output / "fixture"), "--assets", str(assets),
        "--endpoint", str(output / "rejected-endpoint"),
        "--database", str(output / "rejected-database"),
    ], capture_output=True, text=True, timeout=5)
    assert rejected.returncode == 1 and "compiled index.html" in rejected.stderr
    assert not (output / "rejected-endpoint").exists()
    assert not (output / "rejected-database").exists()
for target in ["endpoint", "database"]:
    private = output / ("symlink-" + uuid.uuid4().hex)
    private.mkdir(mode=0o700)
    sentinel = private / "sentinel"
    sentinel.write_text("unchanged")
    (private / target).symlink_to(sentinel)
    rejected = subprocess.run([
        str(output / "fixture"), "--assets", str(root / "frontend/dist"),
        "--endpoint", str(private / "endpoint"), "--database", str(private / "database"),
    ], capture_output=True, text=True, timeout=5)
    assert rejected.returncode == 1 and "must not be symlinks" in rejected.stderr
    assert sentinel.read_text() == "unchanged"
    assert not (private / ({"endpoint": "database", "database": "endpoint"}[target])).exists()
for target in ["endpoint", "database"]:
    private = output / ("fifo-" + uuid.uuid4().hex)
    private.mkdir(mode=0o700)
    sentinel = private / "sentinel"
    sentinel.write_text("unchanged")
    os.mkfifo(private / target, 0o600)
    rejected = subprocess.run([
        str(output / "fixture"), "--assets", str(root / "frontend/dist"),
        "--endpoint", str(private / "endpoint"), "--database", str(private / "database"),
    ], capture_output=True, text=True, timeout=5)
    assert rejected.returncode == 1 and "symlinks or special files" in rejected.stderr
    assert sentinel.read_text() == "unchanged"
    assert not (private / ({"endpoint": "database", "database": "endpoint"}[target])).exists()
parent_target = output / ("parent-target-" + uuid.uuid4().hex)
parent_target.mkdir(mode=0o700)
sentinel = parent_target / "sentinel"
sentinel.write_text("unchanged")
parent_link = output / ("parent-link-" + uuid.uuid4().hex)
parent_link.symlink_to(parent_target, target_is_directory=True)
rejected = subprocess.run([
    str(output / "fixture"), "--assets", str(root / "frontend/dist"),
    "--endpoint", str(parent_link / "endpoint"), "--database", str(parent_link / "database"),
], capture_output=True, text=True, timeout=5)
assert rejected.returncode == 1 and "private directory with mode 0700" in rejected.stderr
assert sentinel.read_text() == "unchanged"
assert not (parent_target / "endpoint").exists() and not (parent_target / "database").exists()
public = output / ("nonprivate-" + uuid.uuid4().hex)
public.mkdir(mode=0o755)
rejected = subprocess.run([
    str(output / "fixture"), "--assets", str(root / "frontend/dist"),
    "--endpoint", str(public / "endpoint"), "--database", str(public / "database"),
], capture_output=True, text=True, timeout=5)
assert rejected.returncode == 1 and "private directory with mode 0700" in rejected.stderr
assert not (public / "endpoint").exists() and not (public / "database").exists()
exclusive = output / ("exclusive-" + uuid.uuid4().hex)
exclusive.mkdir(mode=0o700)
(exclusive / "endpoint").write_text("unchanged")
rejected = subprocess.run([
    str(output / "fixture"), "--assets", str(root / "frontend/dist"),
    "--endpoint", str(exclusive / "endpoint"), "--database", str(exclusive / "database"),
], capture_output=True, text=True, timeout=5)
assert rejected.returncode == 1 and "file exists" in rejected.stderr
assert (exclusive / "endpoint").read_text() == "unchanged"
imports = run(["go", "list", "-deps", "./cmd/fixture"], env=env)
assert not any("wails" in name.lower() for name in imports.splitlines())
run(["go", "build", "-o", str(output / "native"), "."], cwd=root / "desktop")
linux = env | {"GOOS": "linux", "GOARCH": "arm64"}
run(["go", "build", "-o", str(output / "fixture-linux"), "./cmd/fixture"], env=linux)
run(["go", "test", "-c", "-o", str(output / "sqlite-linux.test"), "."], env=linux)
image = json.loads(run(["docker", "image", "inspect", args.linux_image]))[0]
assert image["Id"] == args.linux_image and image["Architecture"] == "arm64"
assert image["Os"] == "linux"
name = "haeryong-stack-" + uuid.uuid4().hex
container = [
    "docker", "run", "--rm", "--pull=never", "--name", name,
    "--label", "haeryong.stack=" + name, "--network", "none", "--read-only",
    "--tmpfs", "/tmp:rw,nosuid,nodev",
    "--mount", f"type=bind,src={output},dst=/fixture,readonly",
    "--mount", f"type=bind,src={root / 'frontend/dist'},dst=/assets,readonly",
    args.linux_image, "sh", "-ec",
    "test -z \"${DISPLAY:-}${WAYLAND_DISPLAY:-}\"; "
    "/fixture/sqlite-linux.test -test.v; "
    "mkdir -m 700 /tmp/run; "
    "/fixture/fixture-linux --assets /assets --endpoint /tmp/run/endpoint --database /tmp/run/live.db & "
    "fixture_pid=$!; trap 'kill $fixture_pid' EXIT; "
    "health=; for n in 1 2 3 4 5; do "
    "endpoint=$(cat /tmp/run/endpoint 2>/dev/null || true); "
    "case \"$endpoint\" in http://127.0.0.1:*) "
    "if health=$(wget -O- \"$endpoint/health\" 2>/tmp/run/health-error); then break; fi;; esac; sleep 1; done; "
    "if test -z \"$health\"; then "
    "printf 'fixture health unavailable after %s attempts; endpoint=%s\\n' \"$n\" \"$endpoint\" >&2; "
    "if test -f /tmp/run/health-error; then cat /tmp/run/health-error >&2; fi; exit 1; fi; "
    "printf '%s\\n' \"$health\"; "
    "wget -qO- \"$endpoint/\" | head -c 500; uname -sm",
]
try:
    linux_result = run(container, timeout=90)
    assert '"fixture":"stack-spike"' in linux_result
    health = next(json.loads(line) for line in linux_result.splitlines() if line.startswith('{"fixture":'))
    assert health["sqlite"] == "3.53.4"
    assert health["settings"] == {"journal_mode": "wal", "synchronous": 2, "foreign_keys": 1, "busy_timeout": 5000}
    assert '<div id="root">' in linux_result
finally:
    remaining = subprocess.run(["docker", "inspect", name], capture_output=True, text=True)
    if remaining.returncode == 0:
        owned = json.loads(remaining.stdout)[0]
        assert owned["Config"]["Labels"]["haeryong.stack"] == name
        subprocess.run(["docker", "rm", "-f", name], check=True, capture_output=True)
(output / "build-result.json").write_text(json.dumps({
    "headless_wails_dependency": False, "cgo_headless": False,
    "linux_image": args.linux_image, "linux_arm64_http_and_sqlite": "pass",
    "host": host, "host_sqlite": "pass", "desktop_build": "pass", "frontend_build": "pass",
    "native_lifecycle": "not_run_by_this_command", "production_release": "not_run",
}, indent=2))
