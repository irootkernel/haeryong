# Auth and private-ingress proof

This disposable one-owner fixture belongs to TASK-001-03. It uses synthetic
private values and a separate SQLite database. It is not the production API,
reader, download transport or a passed G-AUTH gate. Its short routes exercise
mechanisms; `/api/v1` and the shared schema remain production contracts.

Use the Go 1.27.1 / modernc.org/sqlite v1.60.1 stack selected by
[TASK-001-02](../../docs/testing/stack-spike.md). Run from this directory:

```sh
go test -race -count=1 ./...
go build -o /absolute/private/scratch/auth-ingress-fixture ./cmd/fixture
python3 -B check_browser.py --fixture-binary /absolute/private/scratch/auth-ingress-fixture --browser-cli /absolute/path/to/playwright-cli
```

The browser checker requires the existing pinned CLI described in the stack
record; it installs nothing. It creates private per-run registry, socket and
profile directories. Browser restart reuses only that run's profile. Successful
cleanup removes the browser namespace; a cleanup failure keeps its recovery
identity. Runtime data and result JSON stay in private scratch, outside Git.

## Local and private HTTPS procedure

Create an exclusively owned mode-0700 temporary directory. Verify that
`/Volumes/RootKernel` is mounted and writable, then use a unique directory under
`/Volumes/RootKernel/tmp`; otherwise use the system temporary directory. Directory
existence alone does not prove a mount. Never reuse a real library or another application's path.
Choose an unused loopback port (the selected experiment uses 9270) and an unused
private HTTPS port (8451 for this experiment). Configure the exact URLs:

```sh
/absolute/private/scratch/auth-ingress-fixture serve --directory /absolute/owned/runtime --listen 127.0.0.1:9270 --remote-origin https://NODE.TAILNET.ts.net:8451
tailscale serve --https=8451 http://127.0.0.1:9270
```

Run these in separate terminals. The foreground proxy is tailnet-only. Inspect
Serve state before and after, preserve existing routes and Funnel settings, and
stop only this foreground proxy when finished. Do not use a global reset. The
fixture accepts the configured local/remote Host and Origin, not forwarded
identity headers. Behind this proxy, creation requests share the actual upstream
peer's two-committed-creates-per-minute process-local bucket; failed creates
release their reserved slot. Forwarded addresses do not invent
separate clients. Pending capacity is five and polling cadence two seconds.
Pairing JSON is bounded metadata (8 KiB), separate from artifact/content
transport. Poll/decision/redemption share a process-local bucket keyed by the
actual socket peer IP; changing its source port or forwarded headers does not
change identity. A one-minute window starts at its first admitted attempt.
Eight failed (400/403) attempts pause further attempts, with a conservative
60-second Retry-After. In-flight attempts reserve capacity; when those fill the
remaining slots, Retry-After is two seconds. Other responses release their slots.
These fixture controls do not impose result-directory or content limits.

The server holds an exclusive `serve.lock` in its owned runtime. After it stops,
the same directory and listener can restart with the retained database, control
file and endpoint record; no manual endpoint deletion is needed. Existing
records, the database and existing SQLite WAL/SHM sidecars must be regular
mode-0600 files; absent sidecars inherit the protected database's mode. A live
second server or a stable symlinked record is rejected. This owned runtime does
not defend against a racing same-user process. The fixture bounds request reads
at five seconds, including a stalled pairing body; it does not limit streaming
responses or production content transfers. This is a disposable lifecycle, not an installed
production daemon.

With the selected proxy and fixture already running, the portable checker uses
normal certificate validation and drives two origin-separated clients:

```sh
python3 -B check_private_browser.py --fixture-binary /absolute/private/scratch/auth-ingress-fixture --runtime-directory /absolute/owned/runtime --browser-cli /absolute/path/to/playwright-cli --local-origin http://127.0.0.1:9270 --remote-origin https://NODE.TAILNET.ts.net:8451
```

It approves only its own exact request through protected local bootstrap, then
checks private reload/new-tab/full browser restart, absence of the remote
credential at the local origin, trusted remote approval of a local client,
local remembered use and cross-client revocation. It logs out its local test
session and closes only its own browser. It does not configure the network,
control a physical peer or establish Safari support.

1. Open `/connect` on the first browser, keep **Remember device** selected, and
   press **Connect device**. Compare the exact request ID and comparison code.
2. Run `auth-ingress-fixture approve --directory /absolute/owned/runtime` locally
   and supply `{"ID":"displayed request ID","Code":"displayed comparison code"}`
   on standard input. The protected control credential stays in its owner-only
   file, never in arguments, browser storage, URLs or logs. The successful
   fixture-only decision returns HTTP 200 with nonsecret label/creation/expiry
   context; the CLI prints that descriptor. The device joins the list only after
   it finishes redeeming; use **Refresh devices** to see it.
3. Read the private fixture, reload, open a new tab, and close/reopen that browser
   on the same origin. Repeat through the real private HTTPS origin with a
   second test client. Changing origin does not transfer a pairing. A separate
   physical peer is optional for this auth experiment; TASK-001-04 retains its
   explicit physical Safari renderer requirement.
4. Approve the second exact request from the already paired owner's form; a
   pending client cannot approve itself. Revoke the second device from the first
   device's list. Its private view must clear and future private requests fail.
5. Separately check memory-only use, configured expiry, storage loss and an
   unavailable storage write. The automated local checker drives these cases;
   desktop results do not establish Safari's device-specific storage behavior.

For the keyboard smoke procedure, focus the device label, use Tab three times
to reach **Connect device**, and activate it with Enter. After exact bootstrap
approval, focus **Read private fixture** and activate it with Enter; the private
value appears. Activate **Log out** with Enter and require the private view and
stored credential to clear. This checks keyboard actions separately from pointer
interaction; it does not claim physical touch behavior.

Codes/polling secrets expire after five minutes. Reload/close drops the in-memory
request secret and needs a new request. Abandoned pending requests cannot be
cancelled here; when five remain unexpired, wait for one to finish or expire.
Do not share the bearer, control file or polling secret. Leaving a
server session after browser storage is removed does not restore browser access;
reconnect through a trusted owner or protected bootstrap and revoke obsolete
devices when appropriate.

## Optional automatic browser observations

Open `/connect?device-check=1` on the selected private browser and press
**Connect device**. An agent-controlled browser can be driven directly with
Aside CLI (`aside guide repl` documents its API); use a persistent REPL for
multiple actions and exact protected bootstrap approval. For an optional
separate physical client, provide both the displayed request ID and comparison
code to the local coordinator, which approves that exact ID/code through
protected bootstrap. No incoming request is approved merely because it appeared,
and expiry is not extended.

After approval, this opt-in fixture mode checks private reads and unpaired
rejection, reloads once, verifies remembered access and sends observations to
`POST /private/device-proof` with its normal bearer header. The server derives
the observation's device ID from the current session, rechecks revocation at the
write and records the HTTP User-Agent. No bearer, polling secret or control
credential enters the payload. Results remain in the private fixture database.
These are instrumented browser observations, not an automatic gate verdict or
proof of a complete browser restart; the coordinator retains their actor/source
limits. Only the `reloaded` phase reports a passed reload check. An already
remembered visit reports that this visit has not run that check and offers
**Check remembered reload**. This is fixture instrumentation, not a production API.

A transient polling timeout or transport failure retries while the page is
active and resumes after visibility returns. Retry-After is respected. An
expired/unavailable request stops with an actionable reconnect message. Store
errors return 503 with Retry-After, separately from actual 401/403 denial. A
delivered bearer survives that outage. An explicit redemption 503 retains the
request and re-polls its authoritative state before considering redemption
again; a redeemed state cannot issue another token. A lost issuance response
requires a new pairing without automatic replay. Pending secrets remain in
memory only. Retry keeps memory-only and failed-storage warnings accurate.
An already-open event stream fails closed on a store outage with a terminal
`retryable` event (`store_unavailable`, two-second retry hint); authorization
denial still ends the stream. Stream closure alone is not a revocation verdict.
Owner decision confirmation has a separate output from device-list refresh.

## Scope and results

Go race tests cover concurrent redemption and approve/reject decisions,
single-use/rejected/expired requests, pending/polling limits, hash-backed
sessions, noncredential device IDs, protected reads/assets/downloads/events,
revocation and live stream termination, owner receipt reuse and restore
invalidation followed by normal restart.

The local Chrome checker covers actual bootstrap/second-owner approval,
remembered reload/new-tab/browser/server restart, memory-only and expiry,
revocation/stream closure/private-view clearing, safe internal/external returns,
controlled storage-write failure and storage-loss reconnect. It also injects a
real five-second poll timeout and a Retry-After response, checks controlled
visibility resume and unavailable-request recovery, and drops an actual
token-redemption response to verify that issuance is not replayed. A deliberate
idle TCP preconnection checks clean shutdown before a restart with the retained
endpoint file. Duplicate Connect/approve/revoke actions, post-issuance session
failure, device-list retry and truthful automatic-report phases are checked.
The expanded checks exercise real Begin/query store failures, concurrent
failure-budget reservations and rollover, cross-route budgets, lifetime bounds,
actual serve/approve child processes, stale credential responses, an idle tab's
adoption of a remembered device and confirmed revocation with a failed list
refresh. The child-process selector is an explicit test flag, not an inherited
environment variable.
Its served synthetic opaque-child probe checks host DOM/storage/context
separation, credential absence and denied management requests; it is not the
production renderer or native bridge proof owned by TASK-001-04.

The trusted connect shell uses a hash-bound script CSP, no-store, nosniff and
no-referrer. Static shell access is public; private values still require a current
bearer. The fixture's JSON asset/download endpoints are authorization probes,
not the later streaming content implementation. Restore tests use a real SQLite
snapshot and explicit invalidation; no product restore command exists here.

See [the experiment record](../../docs/testing/auth-ingress-spike.md) for actual
local Chrome and Aside private HTTPS observations and their same-host limit.
Independent Task review and delegated Task acceptance are complete. The two
final Low test-oracle corrections passed focused local checks. Full renderer/download delivery and
physical Safari storage evidence remain TASK-001-04; this fixture does not
close those gate obligations.
