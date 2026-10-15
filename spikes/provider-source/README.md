# Provider/source experiment

This disposable Python harness exercises the local subscription adapter and exact-source evidence boundary for [TASK-001-05](../../docs/tasks/EPIC-001/TASK-001-05.md). It does not replace the selected Go/Wails product baseline. [The evidence record](../../docs/testing/provider-source-spike.md) distinguishes the earlier real cited turn, current startup observations and injected tests. G-AI records `pass` for the bounded complete Task05 closing list after independent static confirmation; the evidence record preserves its applicability and production limits.

## Components

- `hardened_harness.py`: immutable selected bytes, remembered exact scopes, input/turn identity, safe result projection and a one-dispatch intent ledger.
- `stdio_connector.py`: strict JSON-lines framing, partial I/O, absolute deadlines and cleanup of the owned child session.
- `native_owner.py`: explicit local owner startup; it reads the existing account/config and requests an ephemeral read-only thread. Its smoke sends zero `turn/start` calls. The default CLI prints a plan and starts no process.
- The three test modules and `run_offline.py`: 75 injected behavioral checks. No native process, provider call or network is started by these tests.

Python 3.10 or later and its standard library suffice. Run the tests from an exact disposable copy so their existing local temporary directories stay outside the repository. This command verifies the mounted scratch volume, falls back to the system temporary directory, preserves file metadata and compares bytes immediately before execution:

```sh
python3 -B - <<'PY'
import hashlib, os, shutil, subprocess, sys, tempfile
from pathlib import Path
volume = Path('/Volumes/RootKernel')
mounts = subprocess.run(['/sbin/mount'], check=True, capture_output=True, text=True).stdout
mounted = any(' on /Volumes/RootKernel (' in line for line in mounts.splitlines())
parent = volume/'tmp' if mounted and os.access(volume, os.W_OK) else Path(tempfile.gettempdir())
parent.mkdir(exist_ok=True)
scratch = Path(tempfile.mkdtemp(prefix='haeryong-provider-source-', dir=str(parent)))
code = scratch/'code'
code.mkdir()
source = Path('spikes/provider-source').resolve()
files = ('hardened_harness.py', 'stdio_connector.py', 'native_owner.py',
         'test_adoption.py', 'test_stdio.py', 'test_native_owner.py', 'run_offline.py')
for name in files:
    shutil.copy2(source/name, code/name)
    assert hashlib.sha256((source/name).read_bytes()).digest() == hashlib.sha256((code/name).read_bytes()).digest()
print('Private test evidence:', scratch, flush=True)
subprocess.run([sys.executable, '-B', str(code/'run_offline.py'),
                '--output', str(scratch/'offline-checks.json')], check=True)
PY
```

The runner prints results to stdout by default. `--output` reserves a new private file outside its code directory and never overwrites existing evidence. Optional `--original-manifest` checks a caller-selected historical hash inventory; standalone tests need no private historical files. That inventory is a preservation aid, not directory-import admission metadata. Keep scratch evidence local and remove only your own files when retention is no longer needed.

## Explicit native owner smoke

`python3 -B spikes/provider-source/native_owner.py` is execution-free. Only an authorized local owner runs `--execute-smoke`, with all of:

- `--argv-json`: the installed binary followed by exactly `app-server --listen stdio:// -c model_provider="openai"`;
- `--cwd`: a canonical existing absolute directory;
- `--deadline-monotonic` and `--cleanup-deadline-monotonic`: explicit future absolute `time.monotonic()` deadlines;
- `--receipt`: a new private file, written with mode `0600`.

Startup checks the observed CLI protocol version, actual ChatGPT Pro account, effective OpenAI provider, inherited model/effort, returned cwd/permissions and ephemeral thread identity before handing off any client. The smoke closes its owned session without disclosing sources or asking a model question. A startup failure and its cleanup status remain separate. Never turn an unresolved cleanup into success or kill unrelated processes.

Only the managed child's `OPENAI_API_KEY` and `CODEX_API_KEY` are removed. Normal parent environment, shared login, global configuration, hooks and plugins remain in use; no login/logout, proactive refresh, forced login mode, API fallback, model/effort override or blanket tool disabling is implemented. Native read-only/network-false permissions are reported as returned; they do not prove universal confinement of inherited tools.

## Evidence and uncertainty

`ScopePolicy` remembers approved project/root/revision/disclosure/provider tuples within that policy object. Reusing the scope needs no callback; expansion requires an affirmative callback for exactly the missing tuples. This thin experiment is not persistent production permission storage or a new user approval UI.

`Capture` holds verified immutable bytes. A fixture capture inventory describes selected evidence and does not require an author manifest, source metadata or index page. Missing, changed or unapproved captures block dispatch. `observed_input` requires matching accepted attempt/input evidence; a later citation check does not manufacture it. Citation matches do not establish semantic correctness or comprehension.

The intent ledger is exclusive before possible turn dispatch; it is not a power-loss-safe production recovery database. Missing acknowledgement, EOF after a possible dispatch, interrupted cancellation or uncertain transport stays uncertain, without automatic redispatch. Cancellation acknowledgement alone is not an interrupted terminal receipt. Safe projections omit credentials, account identity, hidden reasoning and raw provider errors.

Live source-grounded Q&A, imported-result integration, production recovery and the Haeryong-owned generation skill retain their canonical later owners. This experiment imposes no directory size/count/depth/type limits or compulsory author SDK.
