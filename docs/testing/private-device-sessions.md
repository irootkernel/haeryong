# Browser pairing and session management UX (TASK-007-02)

**Task:** [TASK-007-02](../tasks/EPIC-007/TASK-007-02.md)  
**Implementation status:** done  
**Evidence status:** pass for the composed bounded checks below. The one complete
common invocation failed its formatting check; its failure is preserved, not
relabeled as an aggregate pass.  
**Source revision:** `68bb86177935ebcf1d4bb77d17d00770b5a47451` plus the
uncommitted Task02 implementation and corrections, at schema revision 19.  
**Environment/toolchain:** macOS arm64, Go 1.27.1, Python 3.14.7, Node 26.7.0;
actual desktop Chrome 157.0.8103.38. Same-Mac Tailscale HTTPS is web evidence.  
**Implementer / independent reviewer:** AGY Argon/high author; first Task
Evaluator Claude Opus 5.5/high, two independent AGY Argon/high Operators, and
continuous Codex GPT-6.1-Sol/high Epic Evaluator. The original first results were
rework or evidence-needed. The affected TE/Operator and continuous EE confirmed
the finite product and canonical-meaning corrections; their remaining execution
gaps are settled by the actual checks below, without inventing a fresh verdict.

## Actual outcome

The shared browser/native UI adds `/settings/sessions` for current-device
identity, server-reported creation/activity/derived-expiry times, self-logout,
peer revocation and library security policy. `/connect` owns pairing requests,
comparison code, the pending and approved-but-unredeemed queue, approval/rejection
and basic device summaries. Sessions links to Connect for pairing; Connect does
not display the detailed session timestamps.

The implementation reuses the existing transports and configuration plan/commit
contract. No SQLite migration, new administrator grant or native operation was
added. These boundaries preserve the current [personal-tool](../changes/2026-10-10-personal-tool-revision.md)
and [directory-results](../changes/2026-10-10-directory-results-revision.md) applicability.

- `GET /api/v1/session`, `GET /api/v1/events`, `GET`/`HEAD /api/v1/capabilities`
  and render-session `renew` authenticate without touching `last_seen_at`.
  Revocation, epoch and configured expiry checks remain mandatory. Genuine owner
  reads and writes retain activity accounting. Task-added Sessions/Connect
  invalidation listeners no longer reload touching inventory/configuration
  routes automatically; the explicit refresh controls remain.
- Current logout uses `DELETE /api/v1/session`; peer revocation uses
  `DELETE /api/v1/sessions/{id}`. An absent pairing after conflict refresh is
  honestly ambiguous: consumed, rejected or expired. Successful decisions clear
  the comparison form. Stop waiting stops local polling only.
- Policy editing validates positive whole seconds or empty/unlimited, previews
  normalized values and estimated per-device effects, and requires explicit
  confirmation. Both initial and recovered previews require a current device
  inventory. Read failure preserves the draft and prevents a confirmable stale
  preview; a known plan can be retried without another plan POST.
- Stale configuration rereads preserve the proposed draft. A dispatched commit
  returning expiry/revocation `401`, ambiguous `503` or a network failure keeps
  its original owner/library-bound operation identity as UNKNOWN. An absent or
  unreadable receipt does not prove non-application. Authorized receipt lookup
  reconciles the original operation without another commit POST. A failed read
  after definite application is distinguished from Saved.

## Case evidence

The local evidence and earlier failures retain their tested-source qualifications.
Raw receipts, private profiles and runtime identities stay in local workflow
state; no private hostname, token or scratch dependency is required by this record.

| Case | Command/procedure and observed result | Status and limit |
| --- | --- | --- |
| Focused auth, 10 specs | Module-pinned Ginkgo `--race --label-filter=persistence` on application auth and HTTP auth/v1; approved-unredeemed rejection, decision race/readback, passive and session validators passed. | `pass` on its earlier tested source; product Go remains unchanged. |
| Focused passive/guard recovery, 5 specs | Selected passive session/events/capability/renew cases and `TestPolicyCommitDurableBeforePostCommitGuard503And401ReconcileViaOperationReceipt` passed; chained embedded-webassets race check passed. | `pass` before the later frontend corrections; not a whole common gate. |
| Corrected frontend | `python3 -B frontend/check_build.py --regenerate`, then the default verification, passed with current Node behavior tests and compiled assets. The original controlled client-boundary 503 probe also passed. | `pass`; covers fresh-inventory failure/retry, controller identity, stale draft and original-key recovery. Actual mounted Sessions reconnect passed separately below. |
| Local Chrome | `check_browser.py --fixture-binary <fixture> --browser-cli <prepared-wrapper> --receipt <receipt>` passed, including ten Task02 observations. | `pass` before G2/G3 corrections. Synthetic server time and loopback faults; no current private-route or physical-device claim. |
| First actual private cohort | The existing private-ingress driver completed twelve preceding Task01 cases, then failed with a 12-second wait in the new Task02 phase. Stopped disposable DB state and source narrowed the cause to a decision click after successful rejection had cleared its form. | `fail`. Browser closed, daemon exited zero, exactly three owned routes were removed and the entire pre-existing Serve configuration was restored. |
| Second and third private cohorts | The second failed a cookie probe made through a non-browser request context; the unchanged renderer correctly requires browser initiator metadata. The third passed the real-browser cookie probe but failed on an invalid source-mapping test payload; later test lifecycle routes also required canonical correction. | `fail`; both failures remain retained. Browsers and daemons closed, and the full Serve baseline was restored after each. |
| Final corrected private cohort | `check_private_ingress.py` with live external/renderer origins and Coordinator-owned ready/route-removal handshake; same real Go service, SQLite and strict-TLS Chrome. | `pass`, 14 completed cases with daemon exit zero and full Serve baseline restoration. Includes copied-link new tab, memory-only cookie boundary, fresh-inventory fault, idle Sessions reconnect, self-expiring policy receipt recovery, and UI peer revoke during live Reader/event delivery. Actual web engine/browser metadata is used for cookie probes; server guards were not weakened. |
| Complete common invocation | `make test KEEP_GOING=1`, including all five named layers, ran once to completion on the frozen product/driver source. All 1,373 selected Go and 252 Python cases passed. | `fail`, native exit 2: the prepare formatter listed one test file despite its own process exit zero. The aggregate failure remains retained. |
| Current canonical check composition | `gofmt` changed only whitespace in `internal/adapters/http/auth/handler_test.go`; every non-whitespace byte and all other source files stayed identical. Then `make test-prepare KEEP_GOING=1` passed with an empty formatter list, vet, pinned dependencies, documentation and four host builds. | `pass` as affected-prepare plus preserved unit/integration/persistence/E2E. No second complete invocation or fresh raw-tree aggregate pass is claimed. |
| Desktop preservation | `go test -race -count=1 ./readerfacade ./onboarding ./cmd/companion`, `go vet ./...` and disposable companion compilation from `desktop/`. | `pass` on the final application/assets inputs, unchanged by the later browser-driver, test-formatting and factual-record corrections. Log extraction was degraded separately from native exit zero. Compilation is not Wails UI proof. |

Earlier authoring failures are retained locally: frontend test type/helper errors,
the pre-fix client 503 intent-loss diagnostic, six failed local browser cohorts,
all three failed private cohorts, and the formatting-only common failure. The first local browser failure lacked an
underlying retained daemon diagnostic; the next established a socket path-length
failure. Later local failures were copy/selector, async-list and poll-window
assumptions. These failures are not relabelled as passed or as product defects.
The passive regression was authored with its fix, not executed against pre-fix
product code. The corrected private phase uses a finite scoped 420-second guard
for real pairing rate-limit windows and session age; other phases retain their
90-second wrapper. Server throttling and assertions remain intact.

### Original assertion applicability

| Original ID and meaning | Current contribution and evidence boundary |
| --- | --- |
| `T-007-02-01`: unpaired clients get no private data/assets/events and cannot approve themselves. | Focused/local proof passed; actual private unpaired/forged-identity/hostile-Origin repetition passed. |
| `T-007-02-02`: remembered access survives reload and revocation blocks delivery/mutation. | Earlier local proof passed. Current private copied exact link/new tab and UI revoke during active SSE/Reader, with protected read/mutation/file/package/open/renew/receipt/cookie denial, passed. Actual second device is excluded this run. |
| `T-007-02-03`: duplicate/expired redemption, unsafe returns, rejection and approve/reject races, truthful Stop waiting, optional expiry/memory-only. | Focused/local cases passed; actual private pairing/expiry/memory-only composition passed. Sequential private duplicate redemption does not become concurrent redemption proof; offline concurrency/expired-request tests keep their own scope. |
| `C05`: exact pinned version/page/region links, login/reload, allowed cross-project and denied-target identity. | Task02 contributes authenticated exact-link return/reopen. Earlier pinned-reference owners retain cross-project/identity coverage; this is not a new full C05 campaign. Actual private copied-link proof passed. |
| `M17`: unauthenticated/expired/revoked/unauthorized refusal; hostile Origin/CSRF, forged identity, unsafe return and generated-frame boundaries. | Current owner bearer model and explicit same-owner actions are implemented. Local/focused and completed private baseline observations retain their scopes; corrected private authorization matrix passed. No extra admin or network-identity trust is introduced. |
| `M20`: revoke during event delivery, protected restore without revived sessions/exposure, and honest disconnected/unsaved state. | Task02 owns active-event revocation and session/policy UNKNOWN recovery; actual private revoke proof passed. Protected restore and assembled remote recovery retain TASK-005-05/TASK-007-06 ownership. No complete M20 pass is asserted. |
| `X03`: single-use trusted-owner pairing, concurrent issuance, rejection/races, local Stop waiting and no extra admin grant. | Local/focused proof and the current private UI composition join with explicit concurrency/expiry qualifications above. |
| `X04`: same local/private bearer model, remembered/default/optional memory/expiry behavior, frame separation and denial after invalidation. | Local/focused proof exists; actual private cookie, expiry and UI revoke composition passed. |

### Original plan findings and corrections

These are the original finding meanings, not replacement acceptance IDs.

| Finding | Original obligation and current correction |
| --- | --- |
| `TE-P01` | Background liveness must not renew idle activity. Passive backend checks and Task-added automatic inventory refresh are corrected; actual open-Sessions reconnect/expiry proof passed. |
| `TE-P02` | Define memory-only behavior for an already-issued delivery cookie. Reload loses the client bearer but does not revoke the server session; narrow retrieval remains bound to current authority and lease until explicit close/revoke/expiry. Actual private reload/revoke proof passed; no elapsed 30-minute lease claim. |
| `TE-P03` | Same owner and explicit impact confirmation for source/destructive/exposure actions; frame refusal. Existing source/lifecycle confirmations are reused. Exposure remains host API/CLI, with no invented Connection editor. Actual private composition passed. |
| `TE-P04` | Map exact Task assertions, C05/M17/M20/X03/X04, hostile Origin/forged identity/revoked receipt and bounded M20 ownership. The table above restores that mapping; actual private and composed canonical checks are recorded above with the failed full invocation preserved. |
| `TE-P05` | Per-device immediate-expiry estimates, current-editor recovery and truthful applied/unreadable or unknown outcomes. Original-key 401/503 recovery and fresh-inventory guards have focused/Node proof; actual private proof passed. |

The first result panel added `TE-R01..06` and `EE-T02-R01..03`: private driver
and coverage, missing common/desktop evidence, idle activity caused by automatic
UI reads, stale inventory fallback, and incorrect/incomplete evidence and UI/route
claims. Those original negative judgments and priorities remain retained.
The absent-receipt exit suggestion (`TE-A02`) is optional, not a new feature gate.
The original optional `TE-N01` advice is not a new requirement.

## Contract, state and recovery effects

The shared configuration scope remains `library` with its `security` subtree,
revision-bound plan, explicit confirmation and original operation key. There is
no schema revision or new transport method. Same-owner/library and authority-era
checks prevent old responses from clearing a replacement owner's credentials or
showing old private policy data. UI inactivity never bypasses authentication.

The private idle scenario deliberately calls the existing passive render-renew
endpoint through page fetch; it does not prove the approximately 60-second
automatic timer. Its Sessions reconnect fault is browser-controlled and labelled
separately from real server event-stream expiry and revocation. Existing bounded
native/desktop evidence does not establish actual Wails UI or WebKit behavior.

## Review and handoff

The affected TE plus one non-authoring Operator and continuous EE assessed the
finite source corrections. Their original negative verdicts and priorities remain
retained. The current private checks, preserved functional common layers, affected
prepare, frontend and desktop observations settle their missing execution proof.
The formatter correction has exact non-whitespace equivalence; the final evidence
record changes only observed facts. The strict current composition is ready with
zero mandatory gaps and no new Evaluator verdict. Main accepted this exact bounded
member under the existing delegation. The separate local member commit and native
finalization use that same authority.
No Master overall Epic acceptance is supplied by a role verdict.

Reproduction uses the repository-owned
`internal/adapters/http/auth/browser/check_private_ingress.py` and its actual
`--browser-cli`, `--scratch-root`, `--receipt`, `--external-base-url`,
`--renderer-origin-1/2`, port and ready/Serve-removal handshake options. The
Coordinator establishes only the approved three private routes after fixture
readiness and removes those exact routes before signalling local recovery,
verifying full baseline equality. The driver never changes Serve itself. The
existing [private-ingress record](private-ingress.md) and
[deployment guide](../operations/deployment.md) retain setup and recovery guidance;
there is no dependency on a script outside the repository.

The next owner is TASK-007-03 after this member's actual acceptance, commit,
native finalization and owned-output settlement; TASK-007-04
([multi-device reconciliation](multi-device-reconciliation.md)) extends passive
background read isolation (`Haeryong-Passive-Read: 1`) across touching `GET`/`HEAD`
routes. Under the
[web-only owner direction](../decisions.md#owner-amendment-2026-11-05-web-only-epic-007-execution),
physical second-device/mobile, iOS/Android/Safari/Home Screen and Wails UI proof
remain excluded and unverified. The original full Stage A/device assertions are
not passed by same-host web evidence.
