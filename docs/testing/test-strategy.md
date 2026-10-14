# Test strategy and evidence gates

**Current status:** [TASK-001-02 stack evidence](stack-spike.md) records G-STACK `pass` for the declared macOS/Linux ARM64 combination. G-AUTH, G-VIEW, G-AI, G-VALUE and G-CONTRACT remain `not_run`; Task completion still requires independent review. Written procedures do not pass gates. Tests must run against the implementation produced by the assigned Tasks.

## Current baseline and test effort

Apply [D-08 personal-tool direction](../changes/2026-10-10-personal-tool-revision.md) and [D-09 directory-results direction](../changes/2026-10-10-directory-results-revision.md) to every gate. The preserved source-era acceptance wording does not add an enterprise security program. Use small, focused checks of useful html_js_v1 JavaScript, host DOM/token/native separation, remembered one-owner access, exact source claims, data integrity and broken-page recovery.

Normal JavaScript, modules, frameworks, WASM/workers and browser-permitted network/CDNs must work. G-VIEW uses the exact pinned Wails build, the primary browser and actual private HTTPS Safari iOS/iPadOS through TASK-001-03's route. TASK-001-04 requires concurrent immutable versions with different bytes at identical root-relative fetch/module/worker paths and the selected delivery-session/lifecycle proofs; a required unavailable device is blocked. Extend functional compatibility checks to the declared initial delivery targets before claiming support; do not require every OS/browser version combination. Existing external agents keep their normal environment. Managed adapters state actual provider permissions and remember project/provider choices. Routine local backup requires a coherent snapshot and checksums, with encryption checks only for an optional implemented export format.

## Layers

| Layer | Required coverage | Evidence limitation |
| --- | --- | --- |
| Unit | Policy evaluation, state transitions, ID validation, digest construction, eligibility, retry decisions | Does not prove persistence, browser isolation or provider behavior |
| Contract | Plain directory and optional metadata/API/config examples, page identity, error envelopes, staged context, ETags and idempotency | Does not prove a production client uses them correctly |
| SQLite/files integration | Complete directory import/export, resumable staging, cursor inventory, failure recovery, transactional edges, migrations, backup pins, shared objects, restore | Use real temporary DB/content directories, not mocked repositories alone |
| Job/fault integration | Crash boundaries, outbox/SSE replay, fenced leases, uncertain provider outcomes, policy revocation | Fake provider results must remain labelled; separately test actual adapter |
| Shared browser UI | Routes, status separation, errors, keyboard/touch controls, consent and conflict handling | Use automation for repeatable UI paths; record the actual primary native and available second-device checks separately |
| Native desktop | Pinned Wails/OS build, bridge boundary, profile matching, close/crash lifecycle | Record actual build and OS; no claim based on framework documentation alone |
| Private remote | TLS/proxy/auth, second device, revoked streams, source availability, reconnect | Run on explicitly approved ingress, not a public/no-auth substitute |
| Real provider | Supported authentication, actual source/permission behavior, citations, cancellation and limits | One supported real route is required; normal external-agent environment is allowed and fake providers are labelled |
| Release/recovery | Selected install/upgrade path, coherent local backup restore, corrupted input and page recovery | Rehearse on disposable data; optional export encryption is tested only if implemented |

## Gates and stages

EPIC-001 validates G-STACK, G-AUTH, G-VIEW, G-AI, G-VALUE and G-CONTRACT with bounded spikes before dependent product implementation. D-10 authorizes additional private HTTPS renderer ports after actual route/impact inspection. G-VIEW closes only against the full TASK-001-04 checklist on its three environments; G-AI closes against all TASK-001-05 assertions with a real cited question. Longer product contracts are not extra closing lists. Production preferred-default/Continue, Notes/Ask persistence, complete transfers and durable provider recovery stay with their owning features; remaining agreed P-11 targets remain release obligations. Official provider eligibility starts in TASK-001-01, actual scoped provider proof remains TASK-001-05, and TASK-001-07 performs thin real explanation/usefulness evaluation before TASK-001-06 freezes current contracts. A gate record contains procedure, exact environment, result, artifacts, limitations and reviewed decision. Failed gates remain blocking for their dependent proof; independent experiments follow the [execution protocol](../execution-protocol.md#status-and-gate-semantics) and cannot make dependent later Tasks ready or done. EPIC-001 and EPIC-002 production still wait for all six gates to pass under the selected baseline. Fixture-only evidence must not be promoted to a real environment pass.

Stage A ends after EPIC-007 and requires the connected reader, one validated real subscription AI integration and private remote second-device use while Wails is closed. Its integrated coverage includes C01–C09, M08, M10, M14 and M15–M20 under the current owner-amended procedures plus the knowledge-quality workflow; required current checks must pass, not merely be recorded. Stage B adds manual semantic maintenance. Stage C adds schedules and explicitly enabled retention. EPIC-010 repeats relevant acceptance, local recovery and host/session checks for the chosen setup. Host/session and data-integrity checks start at the owning feature; broad threat certification is not an added workstream.

## Contract and scenario catalogs

Run the current procedures for the 29 preserved inherited cases in [acceptance-catalog.md](acceptance-catalog.md) and the current X01–X20 supplemental cases in [contract-cases.md](contract-cases.md). Follow [traceability](traceability.md) for primary ownership; [contract cases](contract-cases.md) distinguish first feature proof from later release regression. A case is passing only when every required observable result is backed by evidence at the relevant layer. Do not convert partial semantic scan coverage into a full-library freshness claim.

## Proposed initial performance and resource checks

The following are design targets calibrated in EPIC-001 and owned by the relevant feature, then repeated for EPIC-010 benchmarking. They are not measured performance or owner-approved service-level commitments. Calibrate with a small representative owner workload first. Include the explicit X18/X20 above-former-limit regressions to prevent an old quota from returning; further scale experiments are useful when they resolve a concrete performance question. Data integrity and current host/session checks still apply.

| Workload | Proposed check |
| --- | --- |
| Library browsing | 1,000 documents / 5,000 versions; report cold/warm p50/p95 page/search latency, DB size and memory on recorded hardware |
| Search usefulness | Proposed 40-query Korean/code corpus, including 1–2-character Hangul and mixed identifiers; exact identifier matches are findable, and at least 90% of curated topical queries place a relevant allowed result in the top five |
| Search privacy | Zero denied/trash leakage across the complete fixture set; this is not a ranking tradeoff |
| Reader interaction | Record useful JavaScript/module interaction, multiple linked pages and on-demand file loading on the selected desktop and second device; check close/reload or documented app-restart recovery for a broken page |
| Directory and context scale | X18/X20 inputs above former HTML/archive/file-count/context limits succeed on recorded adequate resources. Stream/chunk/cursor/range paths preserve every selected entry and captured byte; actual resource failure is explicit and recoverable |
| Configurable work budgets | Exercise owner-configured AI job concurrency, attempt and deadline controls independently of directory input/output. A paused semantic run reports actual incomplete coverage and cannot turn its budget into an artifact-size or file-count cap |
| Idle service | Measure CPU/memory with no jobs and after repeated sessions; no continuously active model process merely to keep the UI available |
| Concurrency | Three owner-client sessions plus one configured background worker; conflict detection and short writes remain correct under deliberate races. Label simulated load; it does not replace the actual Stage A second-device proof |

TASK-001-06 records the adopted hardware/corpus/workload, latency/retrieval targets and the disposition of misses. TASK-004-02 owns retrieval checks and TASK-010-04 repeats the calibrated performance/resource checks. A proposed 300 ms p95 or 90% topical top-five target is not an unqualified release SLO: retain or revise it with the recorded evidence, and state whether a miss is a required repair or a disclosed limitation before the gate runs. Adopt performance thresholds only with actual hardware and corpus evidence. Record concrete usefulness/performance failures and tune the relevant path; no large benchmark campaign is required without an observed need.

## Environment matrix

Start with the owner's exact pinned Wails desktop/local-service host, its normal browser and actual private HTTPS Safari iOS/iPadOS for the required G-VIEW proof. The Task's runtime/device requirements govern its gate, including concurrent-version and selected delivery-session behavior. Demonstrate the headless Go service without GUI dependencies on the selected host. Record exact OS/browser/WebView/Go/Wails/SQLite/adapter versions. The initial support targets in decisions P-11 and [deployment](../operations/deployment.md) remain in scope. Verify the headless builds and browser workflows advertised for those targets before release; early spikes need not execute the entire release matrix. Additional versions/combinations outside the declared support targets are optional evidence, not a universal certification requirement.

Use the owner's available test hardware only with explicit access. This document does not assert that any device or account was contacted. An unavailable extra device outside the declared support targets stays unverified and does not block the initial baseline. A missing required G-VIEW target or observable blocks G-VIEW; a missing later required target check blocks its support claim; do not silently call that target supported or remove it from the agreed plan. A required private second-device or real provider demonstration still needs actual evidence; emulation is labelled.

## Test command ownership

Implementation should provide concise runnable checks for the behavior it delivers, reusing unit/contract/integration/UI commands and short native/second-device manual procedures where useful. Do not create a separate security platform or duplicate suites merely to match layer names. These command names and scripts must be added in the repository; this bundle does not invent currently runnable Make targets. Task evidence records the exact command actually executed, exit status and relevant output. Screenshots/network captures are generated during tests and scrubbed of private content.

## Evidence and acceptance

Use [task evidence](../templates/task-evidence.md), [Epic gate](../templates/epic-gate.md) and [release checklist](../operations/release-checklist.md). Record `pass`, `fail`, `blocked` or `not_run` individually using the canonical evidence enum in the execution protocol. Record needs-rework as a reason on `fail`, not another status. A written procedure is never a pass. Document real remaining failures and their affected scope. Current host/session, data-loss and exact-source failures cannot be relabelled passed; superseded assumptions and unselected platforms do not require a waiver. No automated or third-party result can replace explicit owner approval of meaning-changing publication or personal Reviewed status.

## Quality gates and intermediate walkthroughs

[Knowledge quality](knowledge-quality.md) is normative for G-VALUE and later generation/Q&A/maintenance acceptance, including its predeclared reader-outcome worksheet, progressive use checkpoints and bounded reconsideration rule. Record actual approved revisions, material claims, evaluator role, structural resolution, semantic support, reader answers, limitations and held-out cases. Initial sample sizes or measured averages do not excuse a material contradiction or misleading interaction.

G-VALUE is a thin real-generation/usefulness experiment on existing spike harnesses, using normal author JavaScript and a Markdown/ADR comparison without a profile competition. It does not duplicate the future production reader, personal-state service or full remote deployment. Selected fixture demonstrations contribute to later scenarios without claiming full C07/M15 completion. Reuse findings and approved examples in production reader/link/note gates and full EPIC-006/007/008/010 workflows.

## Early foundational test ownership

EPIC-002 owns operation receipts, mutable resource revisions/outbox/events, minimal durable local jobs and pin primitives. Test auth-before-replay, original precondition fingerprinting, replay-before-stale-precondition reevaluation, same-daemon local fencing and file durability there. EPIC-005 consumes them for backup, anchored export and protected restore; EPIC-006 extends with provider_attempts and external-outcome reconciliation.

TASK-003-04 presents immutable imported evidence with later service-check overlays and capability-disabled checking. TASK-004-01 first implements the approved exact-revision source reader, standalone imported-evidence verification and cheap source-change observations. Per-unit coverage and captured target timestamps remain inspectable; TASK-004-05 uses labelled supplied semantic assessments and TASK-008-03 repeats them with real results. Unchanged cited bytes or a successful structural scan cannot certify semantic alignment. EPIC-006 adds managed-AI source integration and EPIC-008 the evaluated semantic checker. A source broker is optional; normal provider tools can be integrated with recorded exact reads and appropriate native permissions.

## Evidence roles and reusable artifacts

The implementer prepares code and fixtures; an independent reviewer assesses the evidence. Record whether the reader/evaluator was the owner, another human or agents. Human preference is not inferred from agent-only runs. CI results, focused screenshots and PR evidence may be linked once from multiple task records instead of duplicated narrative reports. TASK-007-06 groups functional, host/native and session/restore evidence; a correction reruns affected or invalidated checks while retaining valid evidence for unaffected bundles.

Owner/device/account/source prerequisites have responsible actors and required-before tasks recorded during TASK-001-01. Unavailable required real prerequisites block only the dependent proof; unselected extra devices do not. No fake entitlement or silently reduced private second-device Stage A scope is allowed. No task quota or calendar estimate is a passing test criterion.
