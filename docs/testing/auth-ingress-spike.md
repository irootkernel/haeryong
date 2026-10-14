# TASK-001-03 authentication and private-ingress experiment

**Execution date:** 2026-10-14 KST. **State:** completed disposable Task experiment.
**G-AUTH:** `not_run`; the Task experiment is complete, while production renderer and download evidence remains outstanding.
The [runnable fixture](../../spikes/auth-ingress/README.md) is disposable and
synthetic. This record does not claim a production authentication subsystem.

## Executed local slice

The inherited Go 1.27.1 and modernc.org/sqlite v1.60.1 combination runs on the
macOS ARM64 host declared in the [stack experiment](stack-spike.md). Actual
Chrome 154 uses the existing private per-run Playwright CLI environment.

| Check | Expected behavior | Observed result |
| --- | --- | --- |
| X03 / pairing concurrency | At most one redemption; one approve/reject terminal decision; pending client cannot approve itself | Go race tests pass |
| Private authorization | Invalid/revoked bearer fails data/assets/download/events; active event stream ends after revocation | Go and actual local browser checks pass |
| Remembered access | Reload, new tab, browser restart and ordinary server restart retain the same origin's device | Actual local Chrome checks pass |
| Optional modes | Memory-only reload drops the document bearer and requires new pairing; configured expiry clears private views and stored credentials | Actual local Chrome checks pass |
| Storage recovery | Controlled localStorage write failure falls back to memory; storage loss reconnects with a safe internal return | Actual local Chrome checks pass |
| Host token separation | Opaque child cannot read host DOM/storage/script state, receive credentials or perform management mutations | Served synthetic opaque-child Chrome checks pass, including actual 403 responses; selected production renderer/native contexts remain TASK-001-04 |
| Restore/receipt | Real SQLite snapshot contains old sessions; explicit invalidation rotates three epochs, denies old/pending credentials, preserves owner receipts, and new pairing survives ordinary restart | Go race tests pass; no production restore command or full content restore claim |
| Real private HTTPS | Normal certificate validation, exact remote Host/Origin, no unauthenticated private values, remembered origin-specific access and logout | Same-host Chrome and Aside Browser over the actual Tailnet HTTPS proxy pass; physical peer behavior remains unverified |

The browser checker also rejects unsafe external returns, preserves a safe
internal reader route and verifies that successful cleanup closes its own
browser/server. The trusted shell has a hash-bound script CSP and private
response cache/referrer/type policies. No credentials are put into child-frame
messages, URL query strings or generated packages. This fixture has no renderer
management proxy or credential-forwarding message bridge.

## Selected ingress and limits

The experiment selects foreground Tailscale Serve on an unused tailnet-only
HTTPS port (8451), forwarding to the same loopback application listener
`127.0.0.1:9270`. Existing Serve and Funnel routes were preserved. The new port has
no public Funnel exposure. No application request becomes authenticated merely
because it arrived through the private network. Exact local and remote Host and
Origin values are configured for the fixture; forwarded headers grant no owner
identity. Both URLs use header bearers, and their separate localStorage origins
do not share credentials.

The same-host private Chrome proof covers login, reload, new tab, browser restart
and logout. It also observes that the remote-origin pairing is absent on the
local origin. This is actual HTTPS/proxy evidence, not a second physical device.
At the owner's direction, Aside Browser directly checked the actual private
HTTPS address instead of requiring another phone pairing attempt. Aside CLI
1.26.1010.1739 controlled its local browser, whose reported engine was Chrome
153.0.0.0 on macOS. Exact bootstrap approval established the private client;
the opt-in reporter completed private reads, all five unauthenticated rejection
checks and a remembered reload. A new private tab reused the session. A separate
loopback-origin client initially had no private-origin credential, requested its
own pairing and was approved through the already trusted private client's UI.
The local client retained access after reload, revoked the private client and
the private page stopped access and cleared its private view. An unauthenticated
private read returned 401. Both test sessions were revoked and only the test's
own tabs were closed. The existing user tab was preserved.

These are two origin-separated clients on one host and a real private HTTPS
route, not proof of another physical network peer. TASK-001-03 names a second
test client and a real private route; the earlier physical-device blocking
interpretation is corrected without changing that requirement. The separate
physical Safari renderer requirement, including device-specific storage loss,
write failure and optional Home Screen observations, remains TASK-001-04 and
cannot be inferred from desktop browsers.
The portable `check_private_browser.py` additionally passed against the corrected
fixture on this actual HTTPS route. It checks a remote remembered reload,
new tab and full browser restart, an initially unpaired local origin, exact
trusted-remote approval of that client's request, local remembered reload/read,
cross-client revocation and private-view clearing. Both test sessions end revoked.
It uses the selected existing proxy and normal TLS validation and makes no
physical-peer claim. The fixture restarted on the same listener/database with
its endpoint record retained; the run did not delete that record manually.
Aside was also rerun on the corrected executable: actual private read,
remembered reload/new-tab access, an initially disconnected loopback origin,
truthful later-visit report text and logout/private-view/storage clearing passed.
Only its own tabs and test credential were closed or revoked.

The full native/renderer delivery, streaming content and origin mapping remain
TASK-001-04; the fixture's authorized JSON endpoints do not prove those surfaces.

## Task completion and remaining gate work

The selected independent reviews are complete. Logic, security, product,
documentation and testing assessed the Task's three completion criteria and
three deliverables as met; artist review covered the supplied UI source only.
Maintainability was excluded at the owner's direction. The final review left
two Low test-oracle findings: eight malformed HTTP 400 responses must exhaust
the failure budget, and a revoked stream must exclude store-outage retry bytes.
Both assertions were added and passed focused Go race checks. No Medium-or-higher
finding, pending disposition or current blocker remains.

Delegated acceptance covers the reviewed implementation, these exact locally
verified test/record corrections and the Task completion records. The provider
review predates that bounded delta; it is not represented as review of the
later bytes. Full Go race/build, actual local Chrome, keyboard and private HTTPS
checks passed on the unchanged executable source. Aside exercised that same
delivered binary. Documentation integrity passed on the final records.

G-AUTH remains `not_run`: production renderer/download delivery and the physical
Safari obligations remain TASK-001-04. No future product-gate result is inferred
from this disposable experiment. Runtime runner/reviewer records stay local.

## First physical attempt and recovery correction

The owner supplied an exact comparison request from the private browser. Local
bootstrap approved it successfully, but it remained unredeemed and expired after
five minutes. The request's poll timestamp preceded that expiry; no browser error
trace was collected, so the precise interruption cannot be attributed to Safari
backgrounding or another transport cause. This attempt did not establish
remembered physical-device access.

Inspection found that the old connect screen stopped polling permanently after
any fetch error. The corrected screen retries transient errors, respects
Retry-After and resumes on visibility return, with an actionable expired-request
state. Redemption remains single-use and is not automatically replayed after a
lost response. The five-minute request expiry is unchanged.

The new opt-in `/connect?device-check=1` mode performs private reads, unpaired
rejection and a remembered reload on the actual browser and reports nonsecret
observations through its current session. The fixture records server-derived
device identity and HTTP User-Agent in private runtime storage. Local automated
checks and the actual Aside HTTPS run exercise this reporting path. Corrected
physical-device behavior is not claimed. This reduces manual copying and result reporting without treating
instrumentation, a supplied comparison code or approval alone as a passed gate.

The expanded browser checker also holds an idle preconnection during fixture
shutdown. Its bounded shutdown allows the configured HTTP header timeout to
finish and requires exit zero. An exclusive owned-runtime lock now permits
ordinary restart with the retained endpoint file while rejecting a concurrent
server and symlinked/nonprivate records. This is disposable fixture lifecycle
behavior, not a production daemon shutdown claim.

## Reviewed recovery corrections

The first static Task review identified a restart check that manually removed
the endpoint file, duplicate-action status races, post-issuance retry problems
and conflicting physical-device/memory-only wording. Those defects were
corrected before acceptance. Pending Connect cannot replace the comparison
request. Approve/revoke ignore duplicate in-flight actions. A delivered bearer
survives a transient session failure, offers Retry connection and is not minted
again. A device-list failure leaves the connection watcher active and has its
own retry. Expired rows are labelled from the server clock and retained for
ordinary owner revocation. Storage fallback explicitly requires pairing again
after reload/close. The automatic reporter distinguishes an observed reload
from a later remembered visit.

Current Go race checks cover wrong comparison codes, another request's redemption
secret, retained approval after denial, actual HTTP poll throttling, polling-secret
hash storage, the race's exact winner, response policies, non-HTTPS remote-origin
rejection and owned record/lock handling. The full Chrome checker and portable
actual private-HTTPS checker pass on the corrected executable. Child request
assertions use the browser's actual sending-frame identity; abbreviated request
headers omitted Origin in a failed checker attempt, which was diagnosed and
corrected. This does not turn the opaque auth probe into the production renderer.
The corrected candidate is included in the completed independent review and
delegated Task acceptance described above. Runtime runner/reviewer identities
and reports stay local.

## Store-outage and cross-tab corrections

The second static review found that database errors could be reported as
authentication denial, and that runtime-lock tests duplicated helper operations
instead of executing the CLI. Those are corrected without changing the Task's
original acceptance surface. Actual closed-database Begin failures and missing
query tables now return 503 with Retry-After, distinct from 401/403 denial.
Restoring the tables leaves the same valid bearer, pending secret and approved
redemption usable. An explicit redemption 503 retains the request and re-polls
its authoritative state before another attempt; loss of an issuance response
still does not trigger replay. A stale denial for an earlier bearer cannot clear
a replacement credential.

Go race tests execute actual serve/approve child processes: a second process
cannot own the same runtime, clean shutdown releases its lock, restart rewrites
a retained stale endpoint, unsafe directory modes/symlinks fail, and wrong-code
approval preserves the real HTTP denial body. The child selector is an explicit
test flag. Invalid lifetime bounds leave approval redeemable. Barrier-controlled
concurrent requests test reserved failure-budget slots and minute rollover;
poll, decision and redemption cannot bypass the shared peer-IP budget by
changing route or source port. The window starts at the first admitted attempt,
with conservative 60-second Retry-After for eight failures and two seconds when
in-flight reservations fill capacity. These are auth-metadata controls, not
content limits.

The complete local Chrome checker passed after those changes. It verifies an
idle same-origin tab adopting the remembered device, a late 401 leaving a newer
bearer/storage intact, memory-only and failed-storage retry copy, and confirmed
revocation remaining confirmed/disabled when a subsequent list refresh fails.
It also checks the returned nonsecret approval context, post-approval guidance
and the pending page's reload warning. The portable checker passed against the
same rebuilt executable through actual private HTTPS with normal TLS validation.
Aside Browser also repeated exact controlled bootstrap, private reads, remembered
reload/new-tab access, local-origin noninheritance/401 and logout on that rebuilt
executable. Server-derived connected/reloaded observations were checked; its
test session ended revoked and only its own three tabs were closed.
All earlier local/browser failure-path assertions remain in that complete run.
The documentation navigation and deployment experiment record now point to
this executed slice. The completed review and delegated Task acceptance include
these corrections.

## Owned-runtime and diagnostic checks

The serve CLI now creates the database owner-only and rejects stable symlinks or
nonprivate records for the database and existing SQLite WAL/SHM sidecars before
SQLite opens them. Actual serve-process negative checks preserve an external
sentinel. This is an owned-runtime safeguard, not protection from a racing
same-user process. A five-second fixture request-read timeout bounds a stalled
pairing body; the real TCP check receives a bounded rejection and the process
still shuts down cleanly. These metadata-read limits do not constrain production
content transfers or event-stream response lifetimes.

Creation reservations count committed requests; store failures and the pending
cap release them. Three consecutive store failures and three full-cap retries
preserve their own explanation, and expiry permits a new create. Eight ordinary
polls, eight poll-throttle replies and eight store-unavailable replies each leave
the failure budget usable; the existing 400/403 and concurrent-slot tests still
enforce its bound. Device-proof write-only failure has the same retry hint as
other store errors. An open stream ends on a store outage with a retryable
terminal observation while its bearer remains valid; revocation still ends it.
Focused negative assertions also require eight malformed HTTP 400 responses in
one clock window to exhaust the shared failure budget, and require revoked
stream bytes to exclude retryable store-outage metadata. The outage case keeps
its positive retry-event assertion.

Decision tests compare the exact request ID, label, creation time and five-minute
expiry in both HTTP and actual CLI output. Same-label requests retain different
creation context. Quotes, newlines and terminal control bytes remain escaped in
one JSON record. The actual process tests and browser checker read their own
protected control value and require it to be absent from serve stdout/stderr,
the endpoint record and successful/denied approval output. Private GET, create
and redeem responses assert no-store and no-referrer separately from the shell.

The complete local Chrome checker passed with exact displayed timestamps for
same-label devices, a stored replacement credential restored after a watcher
401 without a false outage, retryable owner-decision copy, and approval
confirmation retained across list refresh before the peer completes redemption.
The reporter now accurately says that its observation is stored in the local
fixture. The actual private HTTPS checker also passed on the rebuilt executable.
Aside repeated the current executable's private read, remembered reload/new tab,
separate initially unpaired loopback origin and logout. Server-stored connected
and reloaded observations were checked; its test session ended revoked and only
its own tabs were closed.
Keyboard Tab/Enter activation of connect, private read and logout is checked
separately from pointer interaction. No physical touch or Safari claim is made.
Independent confirmation and delegated Task acceptance are complete, including
the two locally verified Low test-oracle corrections described above.
