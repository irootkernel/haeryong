#!/usr/bin/env bash
# Offline native headless gates. Requires the repository's pinned tools/cache.
set -euo pipefail
umask 077

repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)
cd -- "$repo"
command -v go >/dev/null
command -v python3 >/dev/null
python3 -c 'import yaml'
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly
required_go=$(awk '$1 == "go" { print "go" $2 }' go.mod)
if [[ $(go env GOVERSION) != "$required_go" ]]; then
  printf 'Required toolchain: %s\n' "$required_go" >&2
  exit 1
fi
export GOOS=$(go env GOHOSTOS) GOARCH=$(go env GOHOSTARCH) CGO_ENABLED=1
case "$GOOS/$GOARCH" in
  darwin/arm64|linux/arm64) ;;
  *) printf 'Required native runtime: macOS or Linux ARM64\n' >&2; exit 1 ;;
esac
command -v "$(go env CC)" >/dev/null

# Check the actual mount before creating agent/test scratch there. Otherwise
# honor the caller's normal temporary directory, including a selected volume.
scratch_base=$(python3 - <<'PY'
import os, tempfile
from pathlib import Path
volume = Path('/Volumes/RootKernel')
if os.path.ismount(volume) and os.access(volume, os.W_OK):
    base = volume / 'tmp'
    base.mkdir(mode=0o700, exist_ok=True)
else:
    base = Path(tempfile.gettempdir())
print(base.resolve())
PY
)
work=$(mktemp -d "$scratch_base/haeryong-headless.XXXXXX")
mkdir "$work/tmp" "$work/builds"
export TMPDIR="$work/tmp"
export HAERYONG_TASK005_WORKLOAD_REPORT="$work/f12.json"
export HAERYONG_TASK004_WORKLOAD_REPORT="$work/f12-http.json"
export HAERYONG_TASK004_CROSSVOLUME_REPORT="$work/cross-volume.json"
export HAERYONG_TASK006_CRASH_REPORT="$work/crash-boundaries.jsonl"
export HAERYONG_TASK006_REAL_INPUT_REPORT="$work/real-inputs.json"
printf 'Private headless artifacts: %s\n' "$work"
if [[ -n ${GITHUB_OUTPUT:-} ]]; then
  printf 'artifacts=%s\n' "$work" >> "$GITHUB_OUTPUT"
fi
trap 'code=$?; printf "Headless exit=%s; artifacts=%s\n" "$code" "$work" >&2' EXIT
python3 - "$work/environment.json" <<'PY'
import json, platform, shutil, subprocess, sys
from pathlib import Path
path = Path(sys.argv[1])
value = {'platform': platform.platform(), 'python': platform.python_version(),
         'go': subprocess.check_output(['go', 'version'], text=True).strip(),
         'free_disk_bytes': shutil.disk_usage(path.parent).free,
         'scope': 'Native headless checks; no browser, provider or remote CI proof'}
path.write_text(json.dumps(value, indent=2) + '\n')
PY

python3 -B docs/testing/validate_docs.py
python3 -B tests/contracts/run.py --report "$work/contracts.json"
python3 - "$work/contracts.json" <<'PY'
import json, sys
report = json.load(open(sys.argv[1]))
if not report['success'] or report['skipped']:
    raise SystemExit('Required contract cases failed or were skipped')
PY
go mod verify
go vet ./...
go list -deps ./internal/domain ./internal/application/... > "$work/dependencies.txt"
python3 - "$work/dependencies.txt" <<'PY'
import sys
for name in open(sys.argv[1]):
    name = name.strip()
    if (name in {'database/sql', 'net/http', 'modernc.org/sqlite'} or
            name.startswith(('haeryong.local/haeryong/internal/adapters/', 'github.com/wailsapp/'))):
        raise SystemExit('Domain/application dependency boundary violated: ' + name)
PY

# Includes the foundation package and canonical F12. Its fixture remains 600s;
# this established whole-package Go budget is a check timeout, not a content cap.
race_status=0
go test -json -race -count=1 -timeout=1000s ./... > "$work/root-tests.jsonl" || race_status=$?
python3 - "$work/root-tests.jsonl" "$race_status" <<'PY'
import json, sys
skipped, package_skips, failed, f12 = [], [], [], False
for line in open(sys.argv[1]):
    row = json.loads(line)
    if row.get('Action') == 'skip':
        if row.get('Test'):
            skipped.append(row['Test'])
        else:
            package_skips.append(row['Package'])
    if row.get('Action') == 'fail':
        failed.append(row.get('Test') or row['Package'])
    if row.get('Test') == 'TestActualCLIF12ResumeCursorRangeDownloadAndRestart' and row['Action'] == 'pass':
        f12 = True
print('Root race: exit=' + sys.argv[2] + ', skipped_tests=' + str(len(skipped)) + ', package_skips=' + str(len(package_skips)) + ', failed=' + str(len(failed)))
for name in package_skips:
    print('Package-level skip (no named test): ' + name)
if int(sys.argv[2]) or skipped or failed or not f12:
    for name in skipped + failed:
        print('Required case incomplete: ' + name, file=sys.stderr)
    raise SystemExit('Root race/F12 must pass with no required skips; private JSON retains native diagnostics')
PY
test -f "$work/f12.json"
test -f "$work/f12-http.json"
test -f "$work/cross-volume.json"
test -f "$work/crash-boundaries.jsonl"
test -f "$work/real-inputs.json"
for target_os in darwin linux; do
  for target_arch in arm64 amd64; do
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
      go build -o "$work/builds/haeryong-$target_os-$target_arch" ./cmd/haeryong
  done
done
printf 'Headless gates passed; four builds are compilation proof only.\n'
