# Decision register

**Classification:** `inherited` = Revision 4/owner direction; `proposed` = a candidate to implement and validate; `gate` = implementation evidence still required. The owner authorized documentation changes and then explicitly selected the personal-developer direction on 2026-10-10. D-08 supersedes stricter security proposals from bundle 1.1.0. D-09 establishes unrestricted directory results and multiple HTML pages, superseding fixed artifact limits and compulsory packaging in 1.2.0. Concrete runtime construction still needs evidence. The [1.5.0 follow-up reconciliation](changes/2026-10-11-opus-follow-up.md) completes the additional review corrections on top of the [1.4.0 reconciliation](changes/2026-10-11-opus-review-reconciliation.md), without changing D-01–D-09. The subsequent [1.6.0 Grok reconciliation](changes/2026-10-11-grok-reconciliation.md) records D-10, the owner's approval of additional tailnet-only HTTPS renderer ports, and the clarified import and gate contracts. See the [current directory amendment](changes/2026-10-10-directory-results-revision.md), [personal-tool amendment](changes/2026-10-10-personal-tool-revision.md) and [earlier review record](changes/2026-10-10-review-revision.md).

## Inherited baseline

| ID | Direction | Change rule |
| --- | --- | --- |
| D-01 | Go core, CLI, and independent per-user daemon; SQLite + immutable content files | Do not reopen through a local implementation preference. |
| D-02 | Wails v3 desktop and first-class private remote web access | Both required for Stage A; desktop exit cannot stop accepted jobs. |
| D-03 | Haeryong owns generation/maintenance skill and package contracts | Aquarium integrates without owning or duplicating those rules. |
| D-04 | Exact source provenance, version/page/region links, canonical backlinks | No silent latest-version retargeting or historical rewriting. |
| D-05 | Version-specific Unreviewed/Reviewing/Reviewed, notes, lifecycle controls | Haeryong interfaces do not allow AI to complete personal review or revive deleted content. |
| D-06 | Configurable schedules, per-action automation, and retention | Numeric defaults do not override invariant boundaries. |
| D-07 | Markdown specifications and a preferred 9000-series service port | Runtime explanations remain HTML result directories. |
| D-08 | Personal developer speed and normal JavaScript execution for owner-selected AI output | No mandatory declarative runtime, JavaScript API allowlist, zero-egress viewer, repeated pairing or separate administrative elevation. Keep host/native credentials and explicit personal/lifecycle actions separate from page code. This owner amendment overrides conflicting earlier security assumptions. |
| D-09 | Manage each AI result as a directory containing any number of HTML pages and supporting files, with minimal input/output friction | No Haeryong-imposed HTML/package byte, file-count or depth cap; no required author manifest, entry HTML, file allowlist or flattening. Preserve selected paths/bytes, load on demand, and report actual resource failures without silent truncation. Optional source/region metadata enriches reading but does not gate raw import. |

| D-10 | Additional tailnet-only HTTPS ports may be used to experiment with distinct renderer origins on the existing private node name | Inspect actual ports, existing Serve/Funnel routes and affected services before any setup change. Preserve one daemon/library and the separate management endpoint. No public Internet exposure, custom domain, wildcard certificate, JavaScript restriction or single-version product limit is authorized. Port selection, count and safe reuse are implementation choices to validate; this documentation update changes no network settings. |

## Proposed execution choices

| ID | Proposal | Rationale and alternative | Resolution point |
| --- | --- | --- | --- |
| P-01 | React + TypeScript + Vite; CSS variables and small semantic components | Shared browser/Wails surface; Vue/Svelte remain viable but would require contract-neutral UI plan adjustment. | Initial selection in TASK-001-01; exact versions in TASK-001-02 |
| P-02 | Go `net/http`, HTTP JSON `/api/v1`, SSE with bounded polling fallback | No independent production JavaScript service or GraphQL server; keep domain independent of transport. | TASK-001-06 |
| P-03 | One local listener `127.0.0.1:9270`; optional private HTTPS proxy to the same listener, with Tailscale Serve as candidate | Configurable port, remote off by default, exact host/origin checks and explicit bearer auth. The management listener remains singular. D-10 permits additional private HTTPS renderer ports, with their upstream mapping chosen and tested in TASK-001-04. No exclusive cookie hostname or checked/registered port claim. | TASK-001-03 and deployment |
| P-04 | Pair once; remember a per-device bearer in the trusted browser origin for local and remote use; one owner authority, scoped agent credentials | Reload, new tabs and browser restart reuse the session. Memory-only is optional; proposed idle/absolute expiry defaults are null until logout/revoke. G-AUTH proves remembered use, revocation and the private route; G-VIEW proves its renderer/download transport. Ordinary management uses normal owner confirmation. | TASK-001-03 |
| P-05 | Reference-only source retention by default, selected snapshots by explicit project policy | Portability without automatically copying private repositories; missing source stays visible. | TASK-001-05 |
| P-06 | Generated UUID-form service IDs, version-local evidence/reference IDs, exact UTF-8 page paths, page-local region IDs, streaming SHA-256 inventory and UTC timestamps | A document identifies a directory result. Each version snapshots the complete tree; page and region keys include version_id and page_path. Optional author metadata is separate from the original files. Human labels are not stable identity. | TASK-001-06 |
| P-07 | One Go headless module and separate desktop module; shared frontend workspace | Headless builds must not pull Wails runtime; a workspace coordinates development. | TASK-001-02 |
| P-08 | `database/sql` storage adapter; select and pin a SQLite driver by a platform/FTS5/backup spike | TASK-001-02 selected `modernc.org/sqlite v1.60.1` (engine 3.53.4) from the [executed stack spike](testing/stack-spike.md). Production pool setup, migrations and complete ADR acceptance remain open. | TASK-001-02 |
| P-09 | Manual scans, proposed semantic overlays, manual purge; explicit indefinite retention | Limits and schedules remain configurable; avoid surprise AI usage or deletion. | TASK-001-06 |
| P-10 | Desktop reader with a result-level Files/Pages picker, explanation canvas and context panel; responsive mobile sheets | Result navigation stays inside the chosen directory version. Layout tables are proposals, not implemented visuals. | Initial selection in TASK-001-01; delivered UI verification in TASK-003-06 |
| P-11 | macOS ARM64 desktop; macOS/Linux AMD64/ARM64 headless; Safari desktop/iOS/iPadOS and Chrome desktop/Android | Exact OS/toolchain/browser versions recorded by gates. Linux desktop/Windows packaging are later candidates, not silently required. | TASK-001-02 |
| P-12 | One `html_js_v1` profile with path-preserving multi-page rendering, streamed file access and separate host/native authority | D-08 establishes JavaScript freedom and D-09 requires directory results without artificial size/count caps. G-VIEW checks navigation, real modules/workers and browser capabilities without requiring an all-inline envelope. | TASK-001-04 and TASK-001-07 |
| P-13 | Put durable local jobs, operation receipts, outbox/revision events and object pins in EPIC-002; add provider coordination in EPIC-006 | Earlier import/lifecycle/backup work cannot depend on infrastructure introduced later. EPIC-004 includes the minimal approved source reader needed for source-change observation. | TASK-001-06 |
| P-14 | One [schema registry](contracts/schema-registry.md) for shared wire vocabulary and state/identity authority; quality evidence before contract freeze | Individual contracts retain their operation semantics. The bundle version is distinct from runtime schema versions, and a written schema is not generated or tested code. | TASK-001-06 |

## Evidence gates

### Initial execution selections: 2026-10-13

Under the approved EPIC-001 execution envelope, TASK-001-01 selects P-01 (React + TypeScript + Vite) and P-10 (shared reader layout) as the initial implementation baseline. [The design system](ui/design-system.md#initial-execution-baseline) resolves the R5/proposal differences; implementation verification remains with their named tasks. D-01–D-10 remain inherited authority, and the complete R5 design remains unapproved.

The owner selected ChatGPT subscription use, Mulgae/Haepari source snapshots at final reporting after approved validation, and confirmed iPhone/Android tailnet access. [The dated preflight](references.md#subscription-eligibility-preflight-2026-10-13) and [TASK-001-01 prerequisites](tasks/EPIC-001/TASK-001-01.md#environment-prerequisites-and-owners) distinguish those directions from unobserved account, exact-source and device proof. That TASK-001-01 checkpoint left all six gates `not_run`. [The subsequent TASK-001-02 stack experiment](testing/stack-spike.md) records G-STACK `pass` for its declared combination; the subsequent [TASK-001-03 auth experiment](testing/auth-ingress-spike.md) records G-AUTH `pass` for its bounded closing list; the completed [TASK-001-04 viewer experiment](testing/viewer-spike.md) records G-VIEW `pass`; [The completed TASK-001-05 provider/source experiment](testing/provider-source-spike.md) records bounded G-AI `pass`; [The completed TASK-001-07 experiment](testing/knowledge-value-spike.md) records bounded G-VALUE `pass`; [The completed TASK-001-06 record](testing/contract-freeze.md) adds bounded G-CONTRACT `pass`; [the prepared Epic closeout](epics/EPIC-001.md#prepared-closeout-record) records completed whole-Epic internal validation and its separate actual Master acceptance boundary.

### Owner amendment: 2026-10-15 mobile gate

**Historical selection, superseded for early G-VIEW by the subsequent amendment below.**

For the resumed EPIC-001 work, the owner explicitly selected Android Chrome as the initial physical G-VIEW environment through Tailscale/private HTTPS and approved the execution plan carrying that selection. This replaces the earlier required iOS/iPadOS Safari environment in TASK-001-04; its other required environments and all five closing assertions remain unchanged. Safari remains in P-11's later platform-support/release verification, including applicable storage and optional Home Screen checks. This amendment records a device selection, not executed physical-device evidence: TASK-001-04 remains `planned` and G-VIEW remains `not_run` until its complete checklist passes. The implementer should automate the available device workflow and result collection to minimize owner intervention without inferring unobserved results.

### Owner amendment: 2026-10-15 agent-operated private browser gate

The owner subsequently directed: “android chrome에서 검증하지 말고 그냥 네가 aside로 검증해. 지금 이를 물리적으로 검증할 수 있는 여력이 안 되네”. This supersedes the initial physical Android prerequisite above only for EPIC-001/G-VIEW. Its required constructions are now the pinned Wails build, the selected primary browser and an actual agent-operated Aside browser on the private HTTPS route. Record the actual browser engine/version and transitions; a desktop browser is not Android emulation or physical-device evidence. The original five behavioral assertions and their delivery, context and recovery obligations remain unchanged. Android Chrome and Safari remain later P-11 platform-support/release obligations, and Stage A's actual private second-device workflow remains assigned to its later Task.

At the TASK-001-04 completion checkpoint, it recorded `done` and G-VIEW `pass` for the bounded required construction after completed evidence and independent confirmation. At that point EPIC-001 remained in_progress and G-AI, G-VALUE and G-CONTRACT remained not_run. The current [TASK-001-05 record](testing/provider-source-spike.md) adds bounded G-AI `pass`; the current [TASK-001-07 experiment](testing/knowledge-value-spike.md) adds bounded G-VALUE `pass`; [The completed TASK-001-06 record](testing/contract-freeze.md) adds bounded G-CONTRACT `pass`; [the prepared Epic closeout](epics/EPIC-001.md#prepared-closeout-record) records completed internal validation and the separate actual Master acceptance boundary. No actual Android visit or result was collected. The mobile-only automatic daemon-restart helper is withdrawn; its original partial observations and failed reopen checks remain historical evidence, not a passing replacement. The prior mobile amendment records the earlier selection and status at that point.

### Required product proof

| Gate | Required evidence | Blocking effect |
| --- | --- | --- |
| G-STACK | Exact Go, Wails, frontend, SQLite and OS versions; headless build and desktop attach/exit proof | EPIC-002 or EPIC-003 must not assume an unverified release combination. |
| G-AUTH | One-time pairing, remembered reload/new-tab/restart, revocation, private HTTPS and requests without a valid bearer rejected | Fix a failed auth flow before using it for private data or remote access; no independent identity/elevation platform is required. |
| G-VIEW | The complete [TASK-001-04 closing checklist](tasks/EPIC-001/TASK-001-04.md#observable-completion-and-tests) is authoritative: pinned Wails, the selected primary browser and an agent-operated actual Aside browser through private HTTPS under the [subsequent 2026-10-15 owner amendment](#owner-amendment-2026-10-15-agent-operated-private-browser-gate); concurrent-version paths, delivery, navigation and focused host separation. D-10 authorizes the additional-private-port candidate. | Select and record the tested construction and wire/configuration before G-CONTRACT. Missing required proof blocks this gate. Production personal-state features and the remaining release matrix are later checks, not extra early closing lists. |
| G-AI | The complete [TASK-001-05 closing checklist](tasks/EPIC-001/TASK-001-05.md#observable-completion-and-tests) is authoritative: supported real subscription/authentication path, one real cited question, actual source and permission behavior, and honest adapter error/cancel/uncertainty handling. | Eligibility starts at TASK-001-01. Real generation/value belongs to TASK-001-07, and durable production provider recovery to EPIC-006. Neither substitutes for the real cited-question proof; no worker-jail platform is added. |
| G-VALUE | Two approved real projects, a small explanation/claim corpus, a Markdown/ADR comparison, useful interactive scenarios and honest source-change outcomes under [knowledge-quality evaluation](testing/knowledge-quality.md) | Complete TASK-001-07 before freezing contracts. Explainability/citation failures require correction and rerun; deterministic resolvability alone cannot pass semantic quality. |
| G-CONTRACT | Package/API/config/ID/error schemas and fixtures frozen; route/state/idempotency/authorization contradictions resolved; evidence from all preceding gates incorporated | EPIC-001 closes only when all six gates pass for the selected baseline. |

All gates begin `not_run`; the [execution protocol](execution-protocol.md#status-and-gate-semantics) owns the canonical evidence statuses `not_run`, `pass`, `fail`, and `blocked`. Recording evidence is separate from passing. A failed or blocked gate prevents EPIC-001 completion and EPIC-002 production. Preparatory bounded experiments for a later Task follow the [execution protocol](execution-protocol.md#status-and-gate-semantics); they do not make that dependent Task ready or done, or close its gate before its owning checklist passes. Executed evidence from the ready owning Task may close that Task's bounded gate. Correct the candidate inside EPIC-001, record the next bounded repair and a reconsideration trigger, and rerun affected checks before closing. Never silently replace Wails, switch billing/provider/account, remove private web scope, transfer owner/native authority to page code, or reintroduce restrictive JavaScript profiles to close a gate. Checks are scoped to D-08, D-09 and the stated product outcomes; expand them only to resolve a concrete failure.

## Choices deliberately deferred to evidence, not to the owner by default

Exact toolchain patch versions, the measured minimum OS matrix, SQLite driver, frontend accessibility primitives, and external AI adapter protocol version are implementation selections. Implementers choose and record them in the relevant ADR and compatibility evidence. Routine contract corrections, task sizing and additional evidence work do not require repeated owner approval.

## Owner decisions and when they are needed

The owner has resolved JavaScript freedom through D-08 and directory/multi-page input/output freedom through D-09. No new owner decision blocks this documentation revision. Implementers choose stream chunking, indexes, caches and measured resource handling without adding a product content ceiling. Keep existing Go/Wails, Stage A remote access, subscription-first integration, provenance and personal-review scope.

| Trigger | Decision to bring to the owner | Work that proceeds first |
| --- | --- | --- |
| G-AI cannot establish a supported subscription route for the actual host/distribution | Decide whether to change deployment, accept an explicit billing change, or pause the dependent capability through an explicit baseline change; a pause alone is not a passing gate | Check official eligibility and demonstrate supported alternatives with exact conditions. Continue independent bounded work inside the current Epic; EPIC-002 still waits for all six gates under the selected baseline. Do not silently substitute API billing |
| G-VALUE or an intermediate reader checkpoint still fails its predeclared reader outcome after the bounded repair, or the next attempt needs a product-scope change | Decide whether to continue a specific alternative, revise the affected product direction or stop that experiment | Present the actual question, expected and observed answers, comparison, failure cause and attempted repair. Routine corrections proceed first; no universal commercial advantage or fixed experiment duration is required |
| Real source or provider access is needed for an implementation spike | Select the actual project revisions and confirm the source/disclosure scope or supply an already authorized account environment | Prepare nonsensitive deterministic fixtures and the evaluation worksheet; candidate projects are not authorization to read or submit their code |
| A required renderer path cannot satisfy concurrent versions and ordinary scripts with the available host/private ingress | Choose between demonstrated deployment alternatives or an explicit product-scope revision, only if the working alternatives require it | First test the actual Wails/private-browser paths. Additional tailnet-only renderer HTTPS ports are approved by D-10 and are the first candidate to test. A custom domain, wildcard certificate, public exposure or single-version product restriction is not approved; escalate only if a demonstrated alternative needs such a change. A failed candidate is not proof that the product is impossible. |
| Deployment/distribution requires personal authority | Select the always-on host, network enrollment, signing identity and distribution/license policy | Build and validate isolated development artifacts and document the concrete deployment steps |

These are conditional environment or product decisions, not unanswered implementation details. Escalate only when the relevant evidence or authority is actually needed.

No additional owner decision blocks the [1.6.2 implementation handoff](changes/2026-10-11-grok-reconciliation.md#2026-10-12-implementation-handoff-162). Continue from the earliest non-terminal Task and follow the existing dependencies; no additional specification-only review round is required before starting. D-10 settles permission to test additional private renderer ports, not the outcome of the experiment. Keep document project membership unchanged in v1 rather than silently adding document moves. Unfiled remains a normal configurable project, not a permanent loss of source functionality. Project moves can be proposed later with their effects on historical metadata, grants and in-flight jobs. Keep coalesced position saves under the existing conflict rules rather than silently adopting cross-device last-writer-wins. Renderer mechanisms, non-content transport timings and provider-version selection are implementation work; preserve original files, ordinary JavaScript and the existing Stage A scope.

## Owner amendment 2026-11-05: Web-only EPIC-007 execution

Master directed implementation to continue through the approved EPIC-007 dossier
and selected actual web-browser verification through the current Mac's Tailscale
private HTTPS route. Physical mobile-device verification is excluded from this
run. Same-host HTTPS browsers prove the web route and their exercised workflows;
they do not establish a second physical device or actual Wails/WebKit behavior.

Complete the six members in their existing dependency order. For this run, the
Coordinator may accept each exact bounded web-only member or correction after its
due implementation, current checks and independent assessments pass, and may
create its separately authorized local commit. An excluded actual-device check
does not stop that bounded delivery or the next ordered implementation. Preserve
the original assertion text and record actual-device evidence as `not_run` or
`blocked`, with the web evidence and this exception separately identified. No
synthetic fixture, viewport emulation, compilation or browser-origin test becomes
physical-device or native UI proof. In particular, T-007-01-02 retains its actual
second-device qualification even when its separate private-web and unrelated-route
observations pass.

The original whole Stage A/device assertions remain unverified. Final overall
Epic acceptance remains with Master, and the conditional closeout commit and
dossier retirement require that actual acceptance of the presented closeout.
The Coordinator decides helper permissions within the already approved work;
routine scratch reports, scoped code changes and required checks need no repeated
owner approval. This direction grants no public/Funnel exposure, tailnet
enrollment, new domain, service installation, unscoped provider call, push or
release. Preserve the verified Ouroboros relative-link corrections.

## Owner acceptance: EPIC-007 selected-web closeout

Master accepted the complete EPIC-007 delivery under the existing web-only run
scope and the exact prepared closeout, authorizing its terminal lifecycle record,
canonical promotion, sole-consumer dossier retirement and one conditional local
closeout commit. This acceptance selects the delivered web-only scope as the completion basis
for this EPIC-007 run; excluded physical qualification does not prevent its
terminal delivery record. It does not pass the original complete Stage A gate.
This records qualified selected-web acceptance; original
physical second-device/mobile, Wails GUI, sleep/wake and complete physical Stage A
remain unverified. Original assertions and historical negative evidence are
preserved. No new provider call, public exposure, installation, push, release,
maintenance or schedule execution is authorized by this closeout.

For EPIC-008 entry, this owner-accepted web-only EPIC-007 closeout satisfies the
predecessor completion, review and applicable exit requirement. The original
physical Stage A remains unverified; EPIC-008 does not inherit an obligation to
fabricate or silently qualify that excluded evidence.
