#!/usr/bin/env bash
# Offline adapter to the canonical root contract; dependency preparation is separate.
set -euo pipefail
umask 077
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)
cd -- "$repo"
python=${PYTHON:-python3}
work=$("$python" -B tests/run_checks.py new-run)
export HAERYONG_TEST_RUN_ROOT="$work"
printf 'Private headless artifacts: %s\n' "$work"
if [[ -n ${GITHUB_OUTPUT:-} ]]; then
  printf 'artifacts=%s\n' "$work" >> "$GITHUB_OUTPUT"
fi
trap 'code=$?; printf "Headless exit=%s; artifacts=%s\n" "$code" "$work" >&2' EXIT
make test
