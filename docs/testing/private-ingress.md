# Controlled private browser ingress (TASK-007-01)

## Implementation boundary

The production one-owner daemon now supports configured private HTTPS browser
management plus version-bound, same-host HTTPS content origins. Configuration
preview exposes the before/after network intent; applying it does not mutate a
proxy. Endpoint doctor reports the actual selected listener, persisted intent,
runtime override and configured mapping support without probing or allocating
secondary endpoints. The private proxy forwards management to the same local
listener; content-only listeners are ordinary sticky renderer bindings.

The additive external-origin ledger retains global immutable tuple/origin
ownership, including removed mappings and tombstones. A remote-first Open
reserves its configured upstream; later local Opens reuse that sticky port.
A fresh local-first Open allocates outside all mapped upstreams from the dynamic
pool, so a later remote mapping must use that returned sticky port and adjust
the pool in the same plan. The [setup runbook](../operations/deployment.md#current-controlled-private-browser-setup-task-007-01)
owns discovery and remapping; no reservation moves. Cold unsupported
capabilities/bootstrap/Open refuse before construction, reservation or socket
allocation. Current route and owner/version guards apply before effects and
response replay. The local native facade remains local-only; fixed companion
handoff remains available locally. No SQLite schema migration, frontend asset
change, provider call or OS service installation is included.

## Selected verification

The opt-in driver is
`internal/adapters/http/auth/browser/check_private_ingress.py` with its adjacent
JavaScript. It builds an isolated test-state daemon and real stored multi-page
versions; it never modifies Serve or a production profile. Its local mode needs
`--browser-cli`, a unique physical `--scratch-root` and new `--receipt`. Its live
mode additionally requires the exact management URL, two renderer origins and
four Coordinator-owned handshake paths. The Coordinator inspects selected route
and process ownership, applies only the approved private routes, then removes
only matching owned routes and compares the full Serve configuration. Keep
receipts, failed runs and browser artifacts private until same-Task consumers
settle. They contain runtime identities and are not tracked durable evidence.

The initial browser cohort used Chrome 156.0.8078.80; the corrected cohort
below used Chrome 157.0.8103.38. Both used Tailscale 1.104.2 with certificate
validation enabled. The management HTTPS port is 9271 forwarded to loopback
9270; renderer HTTPS ports 9291/9292 forward directly to corresponding
content-only loopback ports. The existing node hostname is used; no public
Funnel, new domain/certificate, tailnet enrollment or global Serve reset occurs.
One renderer is configured with URL-equivalent uppercase hostname/leading-zero
port to exercise canonical browser origin handling. The exact numeric ports are
observations, not reservations or product capacity limits.

## Execution record

The local browser cohort passed seven checks: persisted listener/restart,
occupied listener failure with the sentinel preserved, foreign versus fixed
companion Origin handoff, ordinary multi-page Reader/CSS/module/worker/root
assets with host DOM/storage/native separation, confirmed exposure preview,
authenticated intent-only doctor and local owner/Reader recovery. Its daemon
and owned browser closed normally. The corrected actual Tailscale web cohort passed twelve checks on the same
product binary (SHA-256
`98bc0a6bfc141c807d5a6d7ba79be99bbb84f2674944186206d71f2cb4defbb2`).
The first seven repeat actual local setup/companion/preview/doctor plus private
reachability and forged-identity refusal. Two simultaneous private Reader
origins then passed ordinary pages/CSS/modules/workers/root assets and host
DOM/storage/native separation, cross-port browser CORS denial plus separate
server403, emitted pinned external copy-link text, remembered reload,
renew200, cookie-only management401 and unmapped Open503. This observes the
copy-link field, not OS clipboard delivery. Controlled restart retained exact
completed Open receipts, rejected old handoffs401 against active fresh listeners
and restored both Reader selections with remembered credentials. Local-owner
revocation produced SESSION_REVOKED401 for metadata/Open/renew/forged identity,
and actual browser narrow-resource401 on both origins; Connect UI returned and
the current credential cleared. After route removal, the private application was
unreachable and the same remembered local owner opened an unmapped third version.

All310 source/build/driver fingerprints matched at completion. The owned daemon
exited0, browser closed naturally and successful fixture runtime was removed.
The three exact owned routes were removed; full Serve JSON equaled its baseline,
including all unrelated routes and the preexisting unrelated Funnel setting.
No new public exposure was introduced. The screenshot was visually inspected
for the exact private endpoint and nested-page Reader. Full common and
independent result assessment remain due at this checkpoint.

Earlier failed executions are retained separately: the first local driver
shadowed Python's HTTP module; the first live driver referenced browser
`location` outside page evaluation; two live preflights encountered a transient
bind conflict before fixture or route creation; another driver waited for a
Chromium response event hidden by CORS failure; two restart-driver assumptions
expected403 rather than the implementation's401 for an expired handoff, with
one also probing before cold renderer construction; the next cohort similarly
expected403 for revoked delivery instead of its existing401 contract. These are failed cohorts,
not passes. Each attempted live route set was removed and full original Serve
configuration equality was observed. The corrected collection distinguishes
actual browser CORS rejection from a separate server 403 observation with the
same browser context's cookies; it reopens active listeners before testing the
old restart handoff against the existing 401 contract.

## First-result corrections and current verification

The first independent result panel required rework. The original common run
failed prepare formatting on the renderer handler, the Go unit schema-closure
check and Python embedded-definition parity; it continued with keep-going.
These failures remain historical failures. The embedded `render_bootstrap`
definition now matches the selected machine schema, and the handler is formatted.
The endpoint catalog now distinguishes supported mapped private delivery from
unsupported remote denial. The setup runbook includes exact local Open/Close,
original-key reconciliation, immutable port discovery, pool adjustment,
per-version provisioning/restart and compatible-binary forward recovery.
The occupied-listener CLI error now supplies an explicit unused-port recovery
on the same profile/data directory while preserving exit4 and the existing
listener.

Actual corrected-source race checks passed for cold unmapped controls without
constructor/files/receipts, reverse ingress and changed-delivery same-key
conflicts, a route removal or remote disable immediately after the first actual
32-KiB HTTP write, terminal non-revival, occupied-listener recovery and selected
schema parity. The complete pure contract matrix passed 58 cases. Two earlier
contract launches failed before tests because the selected system Python lacked
pytest (including an incorrectly resolved virtualenv symlink); the existing
virtualenv executable then ran the actual matrix without installation.

A replacement actual private HTTPS cohort with Chrome 157.0.8103.38 passed
thirteen checks on binary
SHA-256 `3ba87ae3dd148a57fa35a3ca9ccc79a4286988cb0c4cf0a0702d594f00a43dd8`.
It repeats the original twelve behaviors and adds authored new-tab exact
page/query/fragment context, actual unconfigured frame-src/form-action CSP
refusal and an unprivileged top-level renderer URL returning to the exact trusted
Reader context. Actual successful UI handoff POST observations recorded
`Origin: null` on both private renderer hosts. All 315 production/build/driver
fingerprints matched, including the embedded configuration/import/storage JSON
and owned skill inputs omitted by the earlier fingerprint inventory. The daemon
exited0, browser closed, runtime was removed and the full Serve baseline was
restored. Its screenshot was visually checked for the private endpoint and
nested second page; concurrent saved-position conflict notices retain their
later reconciliation owner.

Additional optional private companion-Origin browser acquisition was attempted
in five intervening failed cohorts. The owned-form path observed an unexpected
local-target401 and then timed out; canonical URL and fresh/no-store attempts
did not establish the cause, and a manual-submit diagnostic also failed local
acquisition. These are failed collection attempts, not recovered passes or a
confirmed product defect. Each owned route set and browser/daemon was cleaned
up, with full original Serve equality. The known working local form helper was
restored. The separate actual Go HTTP remote-companion Origin refusal tests
retain their bounded coverage; that particular private browser variant remains
unverified. Other newly exercised browser boundaries above passed separately.

At the correction checkpoint, the replacement common had passed prepare,
unit and integration while its remaining stages and affected assessment were
still due. Its original input snapshot preceded the final documentation and
opt-in browser-driver edits, so it is retained as that source-qualified run.
The exact-final gate must execute after these inputs are frozen; its actual
terminal evidence is required before member acceptance. The original common's final E2E uses its own
already-built immutable binaries while the replacement uses isolated output;
no running binary, E2E source, assertion, selection or timeout was changed.
Separate current browser and documentation checks preserve their own scope.
They do not relabel either earlier common as the exact-final gate.

## Final implementation gate and record boundary

The exact frozen implementation subsequently passed the complete canonical
`make test KEEP_GOING=1` in a proper detached Git worktree. Its HEAD and
unchanged index baseline matched the canonical checkout; all source bytes,
file modes and tracked binary diff matched. Every required stage and selected
case passed, including the new cold-control, guarded stream and occupied-port
regressions. The separate execution root and built artifacts are not the same
physical checkout or binary as the browser cohort.

An earlier source-only copy lacked the required Git baseline, so its prepare
documentation check failed. The Coordinator interrupted that invalid-environment
execution through its own Gaori process; its native killed130, prior failed
prepare, passed unit/integration and incomplete later stages remain retained.
Neither that interrupted run nor the earlier source-qualified common supplies
the exact-final gate result.

The implementation gate applies to the frozen product basis. The subsequent
Task lifecycle and factual completion records form a separately approved delta
with current documentation/readback verification. Their accepted composition
retains the original checks and judgments; it does not claim another complete
common execution on the later raw record tree. Delegated bounded member
acceptance follows the actual current gate and independent assessments; overall
Epic acceptance and original physical/Stage A qualification remain separate.

## Qualifications and remaining ownership

Master explicitly excluded mobile/physical-device validation for this run and
selected actual Tailscale web verification. A browser on the service host using
its real private HTTPS route proves that web route only. It does not establish
a physical second device, iOS/Android, Safari, Wails UI, Service Worker replacement,
provider qualification or complete Stage A/M15/M16/M17 passage. Those original
catalog assertions and historical results are preserved. TASK-007-02 through
TASK-007-06 retain their ordered session, refresh/recovery, download, public
scope and assembled-gate obligations. No Task acceptance, commit or overall
Epic acceptance is recorded by this implementation checkpoint.
