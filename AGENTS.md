# AGENTS.md

Haeryong's repository-local agent guidance is defined in this file.

## Core Behavior

### 1. Lead with Conclusions

- State the result or current finding first, followed by useful evidence and material limits.
- Keep updates concise; do not repeat requirements or narrate routine work.

### 2. Reuse Verified Information

- Inspect the requested code and its authorities before changing anything. Resolve discoverable facts before asking Master.
- Reuse verified facts; recheck affected information when state changes, evidence conflicts, or context is unreliable.
- State material assumptions and trade-offs. Ask when ambiguity materially changes the result; surface conflicts with repository authority or Master's goal.
- Before changing a feature, read applicable directory AGENTS.md files and the canonical documents relevant to the change. Respect the selected harness's native instruction scope and precedence.

### 3. Act on Sufficient Evidence

- Once evidence supports action, implement the smallest complete, durable fix within the authorized scope.
- Weigh correctness, performance, maintainability, and structural fit. Reuse established patterns and avoid speculative features, abstractions, compatibility layers, and impossible-state handling.
- Touch only what the outcome and verification require. Preserve unrelated work, follow local style, and remove only artifacts made obsolete by the change.
- Record only independent remaining work in the canonical deferred-feedback owner. If none exists, propose an entry and obtain approval before creating an owner. Promote epic-sized work to a TODO candidate or roadmap unit; never defer current correctness or acceptance work.
- Read directory-guide adoption coverage in the root documentation index. Maintain affected canonical documents and existing applicable guides, including guides outside that coverage. Within adopted coverage, add guides for new feature boundaries as part of the authorized work. Report uncovered areas without unrelated backfill; guide absence alone adds no workflow blocker. Finish affected documentation before candidate review.

### 4. Carry Authorization Forward

- Continue approved work without asking again. Seek distinct approval only when required or when a material scope change exceeds existing authorization.
- Keep implementation, installation, staging, commits, and publication within their respective authorization. Recheck relevant state before acting on an approved proposal.

### 5. Verify in Proportion to Risk

- Define success checks before implementation; verify affected behavior and relevant failure paths in proportion to risk.
- Run focused checks and required gates. Broaden or repeat checks only when changes, failures, or unresolved concerns justify it.
- Do not add tests for appearances or substitute prose matching for behavior verification.

### 6. Finish When Complete

- Continue until deliverables and required verification are complete or a concrete blocker prevents progress.
- Report the result, evidence, skipped checks with reasons, and remaining uncertainty; stop without opening unrelated work.

### 7. Delegate Selectively

- Delegate independent work only when its benefit outweighs coordination cost and applicable instructions permit it.
- Honor required independent reviews and delegation restrictions; keep tightly coupled work local.

## Master Preferences

- Respond in polite Korean. When directly addressing the user, use exactly Master.
- Keep repository artifacts in their established language and style; the current specification and operating guidance use English.
- Report conclusions and useful evidence without exposing private chain-of-thought.
- Do not use asynchronous follow-up question tools. Resolve optional choices from context; when an answer is required, complete independent work, ask in ordinary conversation, and wait.

## Aquarium Development Guide

- Use $aquarium:dev-setup for repository-local tooling and root operating guidance.
- Use $aquarium:dev-setup-global for supported user-global tools, paired skills, services, and global MCP configuration. Route Aquarium plugin installation or updates through the host's plugin-management flow.
- Use $aquarium:docs-setup for documentation structure and roadmap identity.
- Use $aquarium:test-setup for the common testing entrypoints and evidence-backed exceptions.
- Use `$use-mulgae` for authorized reviews and native review lifecycle operations. Route all seven enabled roles, including `artist`, to ZCode through `.mulgae/config.yaml`. Substitute a provider only with Master's explicit approval.
- Use $use-gaori for authorized check execution and evidence handling. Use $use-gaori-status for explicitly requested history or timing analysis.
- Use Podway by default for Git-backed Aquarium workflows unless Master opts out of the current workflow before its session starts. Use $use-podway for explicit Procedure v2 lifecycle operations; setup installs configuration without starting a session.
- Sorage Project setup belongs to $aquarium:dev-setup. Project registration does not authorize inbox, outbox, Handoff, or Memo operations.
- Sanho is intentionally excluded from this repository's setup.
- Tool installation alone does not enroll this repository or authorize reviews, checks, provider calls, or workflow execution.
- Keep Mulgae runtime evidence, Gaori runs, Podway runtime state, and derived Sorage files local. Do not cite runtime paths or identities as durable tracked evidence; use an approved promoted-evidence package when retention is required.

## Project Configuration

### Repository Index and Authorities

- Haeryong is a personal AI-maintained, source-grounded knowledge wiki for importing and reading complete result directories. The repository contains specifications, an R5 UI concept, executable spikes, and the initial headless profile/local-control service scaffold. Real SQLite/object storage and durable local-operation foundations are present. Owner bootstrap, remembered device sessions and the authenticated API foundation are present. TASK-002-04 adds resumable streamed upload, asynchronous sealing and atomic result-directory publication. TASK-002-05 adds authenticated query/file delivery, durable CLI orchestration and revisioned configuration authority; native reader/provider/remaining APIs retain their ordered implementing owners. TASK-002-06 adds actual process/fault/pin recovery regressions and native headless gate evidence in docs/testing/foundation-recovery.md.
- Start with docs/README.md and docs/execution-protocol.md. Follow the authority order in docs/README.md: explicit owner decisions, the preserved Revision 4 as amended by current owner directions, then implementation proposals. Evidence gates require actual execution.
- Use docs/decisions.md and docs/requirements.md for product direction, docs/architecture.md for architecture, and docs/contracts/README.md for interface navigation. docs/contracts/schema-registry.md owns shared route/state schemas.
- docs/roadmap.md, docs/epics/README.md, and docs/tasks/README.md index delivery work. Follow explicit dependencies and the execution protocol.
- Use docs/design/prototype/README.md and its HANDOFF.md for the R5 concept, reproducible inputs, task ownership, and verification limits. R5 is unapproved sample-state design; it is not a production implementation or a passed gate.
- Documentation integrity command: python3 -B docs/testing/validate_docs.py. It requires Python 3 and PyYAML, writes no project files, and does not validate application behavior.
- Prototype generation command: python3 -B docs/design/prototype/assemble-prototype.py. It rewrites prototype.html and generates haeryong-prototype.html in that directory. Change authoritative prototype inputs and regenerate together only within authorized prototype work.
- Gaori command docs-integrity wraps the documentation integrity command with parser generic, tags docs and validation, and a 120-second timeout.
- Headless build: go build ./cmd/haeryong. The root [TESTING.md](TESTING.md) owns the common Make contract: make test runs prepare, unit, integration and E2E serially; make test-e2e first includes the separately labelled internal persistence gate. Native go test alone omits the Python E2E and is not the complete gate. Focused diagnostics retain race detection and go vet ./.... The root module excludes the separately pinned spike and desktop modules. No production release entrypoint is established yet. The lifecycle commands documented in docs/testing/profile-lifecycle.md are implemented; docs/testing/storage-foundation.md records their real storage preparation and its limits. TASK-002-03 adds protected auth pairings/approve/reject and the bounded browser/session API documented in docs/testing/authenticated-sessions.md. TASK-002-04 adds the authenticated upload/import/validation HTTP subset documented in docs/testing/atomic-import.md; TASK-002-05 adds the current query, import/validate, upload recovery, file download, operation/job/event, session and configuration CLI subset documented in docs/testing/query-cli-configuration.md. TASK-002-06 adds the offline native entrypoint bash tests/integration/foundation/check-headless.sh, including current root race/F12, contract, dependency and build checks; its manual CI workflow is not remote execution evidence. TASK-003-01 adds the shared React trusted shell (frontend/) with browser HTTP and narrow named native transport adapters and Go serving of committed compiled assets from internal/webassets, documented with current limits in docs/testing/frontend-shell.md; frontend/check_build.py separates one-time npm ci --ignore-scripts preparation from validation/regeneration, and canonical gates stay Node-free. TASK-003-02 transfers/resumes complete result directories through the shared UI with real host-destination delivery, documented in docs/testing/result-transfer.md. TASK-003-03 delivers the shared multi-page Reader with isolated renderer delivery, personal reader resources and the reused native ReaderMode harness, documented in docs/testing/reader-viewer.md. TASK-003-04 adds the shared Reader's evidence presentation (SourcePanel/VersionHeader, the typed evidence query adapter and its closed browser/native/Go operations with the supplied-metadata {etag, stream} bridge), documented in docs/testing/evidence-presentation.md; TASK-004-01 adds approved exact-revision verification/history, owner source observation, committed target-bound coverage/availability and project/source configuration through all three transports, documented in docs/testing/source-observation.md. CLI adds projects register, sources approve/status/observe, evidence show/verify and assessments list (coverage cursor enumeration). The Evidence action is gated by capabilities.features.source_verification; semantic assessment production remains future work; TASK-005-01 adds explicit owner/version Review and history. TASK-004-02 adds authenticated static text/region search (GET/HEAD /api/v1/search and haeryong search) through HTTP, CLI and the shared Library/native clients behind capabilities.features.search, documented in docs/testing/text-region-search.md and docs/testing/search-relevance-worksheet.md; its drivers are internal/adapters/http/auth/browser/check_search.py (--corpus for the real-sample examples), spikes/viewer/check_reader_native.py --stages app,search and app,corpus, and tests/manual/check_search_corpus.py, which runs the real-sample worksheet and the manual_sc19 matrix outside the canonical layers. TASK-004-03 adds canonical pinned references (GET/HEAD /api/v1/versions/{id}/references and /references/resolve, haeryong references list/resolve) and exact cross-project Reader navigation behind capabilities.features.references, documented in docs/testing/pinned-references.md; its drivers are internal/adapters/http/auth/browser/check_references.py (--corpus for the real-sample Gaori G12 → Mulgae M11 case), spikes/viewer/check_reader_native.py --stages app,references and app,reference-corpus, and tests/manual/check_reference_corpus.py, which runs the real-sample worksheet and the manual_references matrix outside the canonical layers. TASK-004-04 adds canonical scoped backlinks, live outgoing counts, the Reader Links tab and owner-only deterministic index validation/rebuild at schema revision 9, documented in docs/testing/backlinks.md. Its Chrome driver is internal/adapters/http/auth/browser/check_links.py. Missing/altered reverse-index DDL remains startup-refused pending EPIC-005 verified restore. TASK-004-05 adds pinned Copy link, automatic login return and the shared Versions view with progressive file/metadata/page-region comparison, documented in docs/testing/deep-links-versions.md. It reuses the existing browser drivers and native ReaderMode stages; TASK-005-01 now adds personal Review; actual semantic production remains with its ordered owner. TASK-004-06 adds tests/integration/knowledge and opt-in real-source Evidence, saved-reader and no-AI reverse-index recovery composition to the existing corpus drivers; docs/testing/two-project-knowledge.md records the executed proof and its limits. The unchanged search worksheet/manual matrices and approved offline native corpus selection retain their bounded claims. TASK-005-01 adds schema revision 10, owner-only Review/history, review-filtered document summaries and CLI review show/history/set across HTTP/native/desktop behind features.review, documented in docs/testing/version-review.md. Reader actions remain whole-directory and explicit; Home and Versions keep exact version state separate from source freshness. TASK-005-02 adds schema revision 11, anchored Notes/history and saved context streaming through HTTP/native/desktop, independent Revisit/Home and CLI revisit show/set; docs/testing/anchored-notes.md records the executed final-candidate checks and limits. TASK-005-03 adds document Archive/Trash/Restore, revision-bound impact plans and an owner Trash list across HTTP/native/desktop and CLI at schema revision 12. docs/testing/document-lifecycle.md records the executed final-candidate checks and limits; protected restore retains its later owner. TASK-005-04 adds guarded manual purge, terminal lifecycle intent/jobs, permanent minimal version reservations and pin-safe resumable GC at schema revision 13; docs/testing/manual-purge.md records the executed final-candidate checks and limits. TASK-005-05 adds coherent host-local backup, scoped directory/ZIP export, protected offline restore and real shared backup status at schema revision 14; docs/testing/backup-recovery.md records execution and limits. Encryption/scheduling remain unavailable, and DF-02 stays deferred. Remaining specification examples are not runnable product commands.

TASK-005-06 adds the two-project owner-state composition, actual browser/native Save/Review and safe lifecycle workflows, exact retained records through restart/protected recovery, and original-directory export/reimport. docs/testing/owner-records.md owns the executed scope and limits; docs/testing/epic-005-owner-gate.md indexes the accepted assembled delivery and its qualification limits.

TASK-006-01 adds schema revision 15, durable provider
attempt/input/result accounting, acknowledged provider progress, one
dispatcher-owned waiting phase, explicit linked retry accounting, owner-only job
resume, verified queued saved-result continuation, hidden-target status-only
reconciliation and restore orphan retention. docs/testing/provider-attempts.md owns
its current proof and limits. At that checkpoint, the production provider
capability was false;
TASK-006-03/04/05 retain transport, generation and saved-Q&A ownership. Canonical
tests make no provider calls; the separate haeryong_provider_live build tag
requires explicit scoped execution and a private physical evidence directory.
The separate haeryong_provider_replay tag replays retained observations without
opening a native client.

### Commit Messages

- Use one English imperative subject with exactly one purpose header: [FEAT], [FIX], [DEV], [TEST], [DOC], [CI], [REL], or [INT].
- Release metadata subjects are exactly [REL] Release v<version>. Keep work IDs in trailers.
- End developer-authored messages with one trailer block, separated from the subject or body by a blank line. Include Aquarium-Workflow, Aquarium-Epic, and Aquarium-Task exactly once, in that order, without blank lines between trailers.
- Aquarium-Workflow identifies the original owning skill as aquarium:<skill-name>; use none for verified work outside Aquarium. Preserve a verified change-origin through later commits and resolve stale or mixed ownership before committing.
- Resolve the owning work unit and a Task's canonical parent Epic from the intended commit snapshot. Preserve native IDs; use none only for verified absence, never unresolved ownership.
- Lore context remains optional. Preserve required native provenance and personal-attribution restrictions. Tool-generated commits retain their native subjects and trailers.
- Before an authorized commit, present the complete message and scope, then verify all three trailers with git interpret-trailers --parse. Verify the actual committed message after native hooks; do not automatically amend or create a repair commit.

### Project-Specific Operating Rules

- Preserve the current owner-amended baseline: Go, Wails v3, the Haeryong-owned skill, supported real subscription AI, version-specific personal review, configurable policies, and authenticated private browser access.
- Preserve complete multi-page result directories, ordinary HTML/CSS/JavaScript behavior, remembered owner access, and focused host DOM/token/native-bridge separation. Do not add compulsory author manifests, index.html, source metadata, extension allowlists, or artificial content size/count/depth caps.
- Execute Epics and Tasks by explicit dependencies. EPIC-001 orders TASK-001-01, 02, 03, 04, 05, 07, then 06. Numeric IDs do not define execution order.
- Preserve current planning and gate states until actual authorized work establishes a transition. Specification checks, synthetic fixtures, and prototype observations do not pass product gates; follow the execution protocol's real-provider, native, private-device, and independent-review requirements.
- Preserve the Revision 4 source, historical review qualifications, and generated-file authorities. Keep secrets, private source, and hidden model reasoning out of public logs and committed evidence.
- Setup does not authorize product implementation, provider authentication or calls, network changes, service startup, staging, commits, or publication.
- For agent-owned scratch files, verify that /Volumes/RootKernel is mounted and writable before using a unique task directory under /Volumes/RootKernel/tmp. Otherwise use the system temporary directory. Pass explicit paths to temporary-file tools and clean up only this task's artifacts.

TASK-006-02 implements managed exact source capture/retained reads and metadata
comparison at schema revision 16, atomic capture-to-job input ownership,
source-specific reference-only backup projection, version-context snapshot purge
and normal shared Sources default/provider settings. docs/testing/managed-sources.md
records current checks and limits. Source capture alone never proves an attempt
received bytes. TASK-006-03/04/05 retain production transport/generation/Q&A;
Task02's bounded delivery is complete after its actual common gate, independent
final assessment and delegated member acceptance. TASK-006-03's bounded
transport delivery is now complete; TASK-006-04 is the next ordered owner after
normal member closeout. Later Tasks retain their explicit dependencies.

TASK-006-03 adds the pinned personal host-local Codex transport under
internal/adapters/ai/codex, revision 17 typed attempt audit, owner-only provider
status across HTTP/CLI/shared browser/native clients, and explicit source/output
consumer guards. docs/testing/provider-adapter.md records executed boundaries and
qualification limits. Its same-code full common and independent final assessment
passed, and the bounded native Task Goal is achieved. Final independent
assessment and delegated member acceptance establish this bounded Task03
delivery. The normal completion commit and native finalization follow under the
same authority; this record claims no commit or Master overall Epic acceptance.
Public generation/question admission, skill publication, saved Q&A and the assembled Epic gate retain
TASK-006-04/05/06 ownership.
Provider tests stay offline unless the existing live tag and an explicit scoped
authorization/private physical evidence directory select the native fixture.


TASK-006-03 correction semantics retain prepared no-dispatch attempts and exact
local reasons; complete authorized output/native terminal facts survive separate
required audit or cleanup failures. Pinned history interrupted is unresolved;
complete saved output requires exact full history when a live event is partial.
Effective config uses the selected CWD, and bounded inherited-capability audit
keeps declared/observed/unknown facts distinct without raw secrets. Poll keeps
Reader/Review available after handled accounting failure; Stop reports it.
Current offline corrections and historical proof applicability are separated in
[provider correction evidence](docs/testing/provider-corrections.md). Real
provider/status/native-client operations still require their scoped authority.

TASK-006-04 implements generation and typed drafts at schema revision 18.
One owned versioned skill is embedded from
skills/use-haeryong/SKILL.md and materialized privately for managed use. Each
attempt writes a fresh complete result directory; positive owned-process drain
precedes service-derived ZIP storage, preparation outside the writer and guarded
atomic import. Native public-final identity stays separate from ZIP identity.
Saved continuation never repeats Execute. Pinned prior explanations stay separate
from project-source reads and cannot fabricate observed_input. Remembered
owner-authenticated workflow issuance keeps credentials out of provider input.
HTTP, CLI and shared browser/native generation and draft consumers are implemented.

[The generation record](docs/testing/generation-workflow.md) owns actual evidence,
historical failures and applicability limits. Managed and standalone use shared
the same owned guidance; the four real explanations retain their bounded corpus
and Reader assessments. Three independent technical rounds have completed.
The third requested three corrections to prepared-result retry, CLI failure
acknowledgement and saved-request target presentation. Their focused checks,
Node selection and controlled browser evidence retain their tested-source
qualifications. A test-only layer-placement correction leaves product and
frontend inputs unchanged. Seven affected native observations passed with
natural owned cleanup; their controlled scope and earlier failures remain
explicit. The replacement canonical common passed on the corrected source.
The factual documentation overlay has separate affected checks. Independent
final assessment passed the bounded Task04 outcome and judged its original Goal
achieved. Earlier common successes and failures remain in the generation record.
C01's matched-request procedure is still
unverified with TASK-006-06. Saved Q&A and the assembled Epic
exit retain TASK-006-05/06 ownership. The bounded Task04 delivery is complete.
Delegated member acceptance, the normal completion commit and native
finalization follow. No Master overall Epic acceptance or physical-device proof
is claimed here.

TASK-006-05 adds typed read-only Ask, durable question/answer ownership at schema
revision 19, exact captured display/source context and current-authority saved
history across HTTP, CLI and shared browser/native clients. The
[saved-question record](docs/testing/saved-questions.md) owns implementation,
executed offline checks, bounded shared-browser/native observations and the
current real-provider evidence. The closed output-contract correction preserves
strict validation; eleven focused race specs passed. Independent semantic assessment
passed the original three real cases, including Q3's useful partial abstention,
and one separately predeclared actual source-absence answer. Complete saved reads
and zero-repeat records retain their exact scope. The pre-review candidate's full
headless common passed with all 309 mandatory members exactly once. The first
technical review identified four correction groups: optional runtime regions,
exact retained citation inspection, terminal refusal recovery and honest history
states. The corrections include a reproduced same-key admission race and its
passing durable failed-receipt regression. Affected checks and canonical frontend
verification passed. The replacement common passed 1,333 Go and 247 Python cases,
with all 316 mandatory Go tuples exactly once. Current controlled native proof
passed strict trusted Submit and full partial-answer/context/source reads.
Browser history, terminal refusal recovery, final-answer/current-retained-source
reads, authority-loss late-response clearing and invalid-output error UI have
bounded completed proof. The later acquisition passed with the expected selected
accounting/shutdown error; it is not a pristine service-exit or claim of three positive answers.
One positive UI Submit had two same-key backend 202 deliveries with no duplicate
Execute. Earlier failed cohorts and focus qualifications remain explicit.
The second technical assessment completed with four fresh roles and no unresolved
valid findings. The proper final independent assessment passed all 29 due
obligations and judged the bounded original Goal achieved; the native Goal is
recorded achieved. The bounded Task05 delivery is complete. Delegated member
acceptance, the normal completion commit and native finalization follow under
existing authority; none is recorded as performed here. At that Task05 cutoff,
Task04's whole-version Generation seam remained OPEN before Task06. Provider input
observation, later source verification and semantic support are distinct. Master
overall Epic acceptance and physical-device qualification remain future.

The later Task04-owned correction supplies every immutable file for a selected
whole-version Generation reference through the existing guarded directory helper.
The manifest identity, page-reference branch, current authority and separate
project-source observation remain intact.
Actual regression checks and the complete common gate passed. The independent
final assessment supports this bounded correction and its original Goal.
The original Task04 and Task05 acceptance, commits, review counts and evidence
qualifications remain unchanged. Exact delegated correction acceptance, the
separate correction commit and native handoff follow under existing authority; this record does not assert them as
already performed.
TASK-006-06 retains the assembled live workflow, matched C01 request, real
pinned-region question and fault/allowance proof; Master overall Epic acceptance
and physical-device qualification remain future.

TASK-006-06 adds real-DB offline AI workflow composition in
`tests/integration/ai-workflows`, labelled persistence under the common test
contract. [The AI workflow record](docs/testing/ai-workflows.md) owns the real
provider/input/cleanup qualifications, production source/Trash/restore boundary,
matched pre-call search pair, actual shared-browser Reader/saved-Q&A/page-fault
observations and the later R35 Notes correction. The corrected frontend checks,
embedded-assets race package, build and zero-model browser disconnect/reconnect
observation passed. The complete common on the corrected product passed 1,351 Go and 249 Python
cases, including all 334 required tuples once. Earlier successes and failures
keep their source qualifications. One fresh four-role Codex review and the
original-criterion independent final assessment passed the bounded Task06
delivery. Current-source applicability and affected documentation checks remain
separate from the full common. Delegated member acceptance establishes Task06
delivery; its normal commit/native finalization follows. The Epic remains in
progress pending its internal audit and Master overall acceptance. Physical/
private-device and later Epic gates retain their owners.

## EPIC-006 assembled delivery

The earlier Task and correction checkpoints above retain their historical
status and evidence scope. All six members are now complete. The
[production AI exit gate](docs/testing/epic-006-ai-gate.md) indexes the assembled delivery, accepted
shared-client correction at `68a50c4903f577cf9a97fc30c3095652c8615eb0`,
independent final assessment, passing corrected common and actual Master overall
acceptance of the exact closeout. EPIC-006 is complete; the final
closeout commit records this acceptance. The original source
review predates the verified local correction and retains its original result.

Retained real-provider, semantic, Reader/browser and native observations keep
their individual qualifications. Current common and affected documentation
checks do not establish physical private-device use or complete Stage A.
At the earlier member checkpoint EPIC-007 remained in progress. TASK-007-01 and TASK-007-02 have completed their
bounded member deliveries and verified commits/native handoffs. TASK-007-03 has completed its bounded web member; TASK-007-04 follows its
verified normal commit/native handoff. The
[2026-11-05 owner direction](docs/decisions.md#owner-amendment-2026-11-05-web-only-epic-007-execution)
selects actual Tailscale web verification for this run and excludes physical
mobile verification. Original second-device and Stage A assertions retain their
unverified scope; implementation is not blocked by the excluded device proof.
Maintenance, schedules and release retain their later Epic owners.

TASK-007-01 adds controlled same-host private HTTPS browser renderer mappings,
additive permanent external-origin ownership, confirmed exposure previews and
intent-only endpoint doctor. [Private ingress evidence](docs/testing/private-ingress.md)
owns current executed scope and failures. Local-only native renderer refusal and
fixed local companion handoff are preserved. Actual same-host Tailscale web
proof is separate from physical-device/mobile/native UI and assembled Stage A
qualification; those exclusions follow the current owner direction. The exact-current common, original independent
Task panel and scoped Epic contribution assessment support the bounded member
delivery under delegated acceptance. Its normal commit/native finalization
follow; no Master overall Epic acceptance is asserted.

TASK-007-02 adds passive-activity isolation (`AuthenticatePassive` on status,
event, capability and render-session renew routes), `/settings/sessions` and
`/connect` pairing queue/device management, `security`-scope policy plan/commit
with stale-draft preservation, and post-commit `401` `UNKNOWN` receipt
reconciliation. [Private device session evidence](docs/testing/private-device-sessions.md)
records executed local/browser checks and Coordinator-owned Tailscale web scope.

The bounded Task02 member is accepted under delegation. Its record preserves the
format-only failed full aggregate and the passing affected-prepare/current-check
composition, with actual private-web proof. TASK-007-03 follows the verified
normal completion commit and native finalization; physical Stage A stays unclaimed.

## Responsive private-web workflows (TASK-007-03)

The bounded Reader/control changes and independent private-web checks are
recorded in [responsive workflow evidence](docs/testing/mobile-reader-workflows.md). Files/Context
remain native dialogs; compact phone sticky actions, safe-area/control typography,
always-mounted Ask progress and reconnect-safe Provider presentation are current.
Actual import/download/context/Notes/Review and controlled Ask presentation retain
their stated scope. One separately approved application Ask saved a useful
partial answer with one attempt and exact retained-source reads. The evidence
record preserves input, capability and cleanup qualifications. Independent
result composition and delegated bounded member acceptance are complete; the
normal completion commit/native handoff precedes Task04. Physical mobile, Wails GUI and whole Stage A remain
unqualified under the current web-only owner direction.

## Multi-device reconciliation and conflict UI (TASK-007-04)

TASK-007-04 adds `Haeryong-Passive-Read: 1` and native `passive: true` background
read isolation across touching `GET`/`HEAD` routes, blank-line-framed SSE/native
cursor continuity (`Last-Event-ID` and native `subscribeEvents`/`eventsPoll`
cursor + no-cursor bootstrap `resync`), the single-flight `resync.required`
barrier with `readGeneration` fencing, receipt-first recovery (`GET /api/v1/operations/{id}`
followed by a fresh resource `GET` and queued-edit conflict preservation),
independent `reader-preference`/`reading-position`/`page-position` validators,
and local-vs-server conflict comparison UI across Review, Revisit, Notes
(create/edit/reattach/delete) and personal reader state.
[Multi-device reconciliation evidence](docs/testing/multi-device-reconciliation.md)
records the passing focused Go persistence, desktop `readerfacade` + `cmd/companion`,
focused Python E2E and frontend default checks, retained authoring/diagnostic
failures, the original frozen-candidate common pass, current frontend/assets/build
checks and selected actual Tailscale private HTTPS stages. The original four
independent result assessments requested rework; current corrections retain their
negative history. A subsequent affected correction restores initial Provider
bootstrap/direct-entry behavior, scopes page recovery to confirmed HTML positions,
keeps skipped mandatory personal refresh unconfirmed, and supplies passive
successors for locally retired reads. Current focused checks and selected Provider
direct-entry/Reader recovery web proof passed. The finite Reader pending-state
presentation correction also passed actual private proof. The corrected frozen
common passed 1,378 unique Go It specs plus six suite setup/cleanup entries and
253 Python tests, all 15 native commands zero. The same original roles confirmed
current bounded criteria and the strict current composition is ready. Coordinator
delegated member acceptance is complete; factual/lifecycle and AST-identical EOF
formatting have separate affected verification. Normal local commit/native
finalization and the retained-output handoff precede TASK-007-05; no Master
overall Epic acceptance is claimed.
Physical mobile, second-device and Wails GUI qualification remain excluded for
this owner-amended web run.

## Headless source and provider boundaries (TASK-007-05)

[Headless source evidence](docs/testing/headless-source-boundaries.md) records the normal
untagged embedded service from a non-repository CWD, complete sourced and
metadata-absent multi-page private-web reading after owned source/import removal
and restart, current missing-source zero-dispatch Question, independent retained
snapshot reads and honest posthoc/source/provider presentation. Production code
and schema stay unchanged. Current backend acquisition and same-fixture actual
Tailscale browser continuation compose with their original failures; no fresh
whole-driver aggregate or new real-provider call is claimed. Browser/daemon
cleanup and complete Serve restoration passed. Physical/mobile/Wails GUI and
whole Stage A retain the current owner direction; canonical Task lifecycle and
TASK-007-06 keep their ordered acceptance and assembled-gate ownership.


The original Task Evaluator confirmed the finite exact-selection/approved-root
proof and driver/record corrections. Original Task Operator and scoped Epic
contribution coverage remain applicable; strict current composition has no due
gap. Coordinator delegated bounded member acceptance is complete. Normal local
commit/native finalization and retained-output handoff precede TASK-007-06.
Original failures, unchanged-product common and historical provider proof keep
their individual scope; full physical Stage A and Master overall acceptance
remain unclaimed.

## Selected private-web integration (TASK-007-06)

The [selected gate record](docs/testing/stage-a-gate.md) records actual same-host Tailscale Reader,
source verification, saved Q&A, F12, owner state, session revoke and reconnect
observations, with physical/mobile/Wails GUI limits preserved. The integrated
run reproduced and corrected protected-restore origin normalization and large
saved Ask-context rendering. Finite C02 follow-up also corrects a confirmed
failed-seal reason hidden as cancellation; actual private error presentation and
unchanged accepted versions passed on the final normal package. The corrected extracted normal package displays
all 3,061,646 context bytes with the original digest and responsive controls;
no new provider call was used. Both owned web cohorts naturally stopped and
restored the complete original Serve configuration. The completed common passed
all Go and E2E checks but failed three documentation-fixture assertions. The
six source-pointer corrections passed the complete 64-case Python integration
selection; the failed aggregate stays failed. The original independent first
assessments and finite corrections now compose ready, with all current member
duties met and no required findings open. The Coordinator accepts this exact
bounded web member under delegation. Its normal completion commit/native handoff
and the separate assembled Epic audit follow; no whole-Epic acceptance is asserted.

## EPIC-007 assembled selected-web delivery

All six bounded members and their verified local commits/native handoffs are
complete. The [assembled gate record](docs/testing/stage-a-gate.md) retains independent
whole-Epic conclusions, the current affected-check composition and actual Master
overall acceptance of this selected-web closeout. Original physical Stage A,
second-device/mobile, Wails GUI and sleep/wake remain unverified under the owner
web-only direction. The failed common and earlier failed observations retain
their original results. Canonical Outcomes own the promoted integration and
assertion map; the sole-consumer dossier is retired by that exact acceptance.
No maintenance/schedule/release or untested-platform qualification is claimed.

## Manual scan inventory and deterministic structure checks (TASK-008-01)

TASK-008-01 implements schema revision 20 scan inventory (`scan_runs`,
`scan_captured_versions`, `job_items`, `findings` in
`internal/adapters/sqlite/scan_schema.go`), project-narrowed maintenance
policy snapshots, pre-dispatch source target sets, incremental 64-unit batch
enumeration (`document`, `version`, `page`, `region`, `entry`, `source`,
`index`), deterministic structure and reference-index inspection (including
stored `file`, `directory`, and `symlink` entries without AI), owned
`derived_index` (`references`) repair child fencing (`scanRepairAuthority`),
centralized `domain.RestrictCredentialScope` visibility/coalescing guards, and
closed coverage/finding projections (`scan_report.stage` in
`inventory`/`inspect`/`repair`/`complete`) across HTTP (`POST /api/v1/scans`,
`GET/HEAD /api/v1/jobs/{id}/findings`), CLI (`haeryong scan`,
`haeryong findings list`), and shared browser/native/desktop clients
(`scanStart`, `jobFindingsList`) behind `features.scans`.
[Scan inventory evidence](docs/testing/scan-inventory.md) records the bounded
unit, SQLite persistence, HTTP integration, public E2E (including normal
symlink/directory/binary import and 40-page/83-unit mid-scan `SIGKILL` and
restart recovery), separate populated schema 19 -> 20 migration,
frontend/desktop/contract (`59/59`), and full-root `go vet` checks together with
retained earlier failures and limits. The corrected selected common passed
1,390 Go and 256 Python cases on the frozen implementation/test candidate. Its
separately settled Coordinator bytecode guard qualification and initial failed
common remain explicit in that record. Independent Task/result and Epic-contribution
assessments, current composition and CO substantive review support delegated
bounded member acceptance. The factual/lifecycle record overlay has separate
affected documentation checks; the normal commit and native finalization follow.
TASK-008-02 through TASK-008-06 retain their ordered work and overall Epic
acceptance remains with Master.

## Bounded relationship and topic candidate discovery (TASK-008-02)

TASK-008-02 adds schema revision 21 (`maintenance_intents` and
`maintenance_intents_by_scan_item` in `internal/adapters/sqlite/scan_schema.go`),
query-local `maintenance_source_contexts` dependency projection in
`internal/adapters/sqlite/source_capture.go`, bounded
candidate retrieval (`ScanSearchText`, `ScanCandidateUnits`, shared sources, and
outgoing references capped at `maxShortlistCandidates = 6`, `B06`), per-page
region presence granularity on mixed versions (`B05`), pre-call candidate
HTML/region excerpt inspection (`inspected_candidate_relations`), child
`job.kind = "maintenance"` admission (`CaptureMaintenanceAndAdmit`) preserving
multi-step `ReadBindings` and scan-start `current`-mode Git `ResolvedRevision`
while admission-unavailable `files` targets fail closed (`B01`), multi-project
`MaintenanceReadTarget` source input guarding (`GuardSourceInput`), normalized
`SemanticRequestSHA` prompt/coordinate tie-breaking (`B04`), parent scan
pause/resume budget and restore ordering (`pauseScanRunElapsed`,
`checkScanResumeBudget` on `retry`, `curJob.ProviderAttempts.Unknown > 0`,
`ORDER BY rowid,job_id`, `B08`..`B10`), the closed Codex JSON schema
(`maintenanceFrame` / `MaintenanceOutputEnvelope`), and deterministic post-model
verification (`PrepareMaintenance` and `ApplyMaintenance` in
`internal/application/scan/candidates.go`) for `T-008-02-01` (unit-scoped source
evidence `VerifiedEvidenceByUnit` and verified module/repository directional
import required for `depends_on` / `source_fact`, with qualified rationale
withdrawal on downgraded claims, `B02`, `B12`), `T-008-02-02`
(Trash/purged/unauthorized target exclusion across all four boundaries,
`$.origin_unit` and `evidence_json` lifecycle/purge/pagination fencing, and
multi-region `(VersionID, PagePath, RegionID)` preservation without uninspected
coordinate leakage plus distinct same-pair mechanism fingerprinting
(`mechanism_key`) without overwrite, `B03`, `B07`, `QR2`), and `T-008-02-03`
(stem/paraphrase topic grounding and `kebab-case` normalization with
per-proposal `proposal_decisions`, focal-only content-gap direction without
neighbor-absence inversion, `source0` `source_unverified` qualification, and
preserved `semantic.assessed = 0` / `semantic.complete = false` separation from
`TASK-008-03` freshness, `QR1`, `QR3`..`QR5`). `TASK-008-02` remains in progress
pending the Coordinator-owned live held-out provider quality cohort
(`T-008-02-04`), canonical common gate, and independent assessment. The current
additional correction uses effective parent-EOF body reads, deterministic
content-discriminative ranking without fixed version slots, specific mechanism
comparison guidance and bounded lexical topic-paraphrase/focal-gap handling.
Ordinary maintenance-child waiting leaves the parent active; explicit
unresolved-result/input holds use pause/resume elapsed accounting. Inspected
progress derives from applied item detail/completed children, not admission.
The initial quality acquisition and first repaired acquisition both failed;
Master approved one additional bounded original-set experiment with unchanged
criteria. That experiment failed useful retrieval at 1/4 (required 3/4),
although focal gaps/uncertainty passed and unhelpful extras met the original
allowance. A later opposite-qualifier guard correction passed current race unit,
controlled SQLite and build checks; it does not repair the missed candidate
pairs or relabel the earlier cohort. Task02 is not accepted, no fourth cohort
is authorized, and the full common and later dependent Tasks have not started.
Master subsequently approved the bounded two-stage semantic candidate nomination
amendment (`Mode = "semantic_nomination"`, Pass-1 `structural_ready` ordering
before global nomination batches `nomination:p{pass}:b{index}`, initially at the
historical `<= 2048`-byte projected-card / `900`-byte excerpt checkpoint within
a `<= 144 KiB` frame, strict `AuthorizeMaintenance` prompt/digest/card equality
and batch-item guards, `PV01` continuation, and `N4`/`PV02` purge
tombstones/unpinning) and one new `<= 25`-attempt original-set provider quality
experiment. That semantic-nomination cohort completed (`25` saved attempts,
`286` findings, `6` `signal_error_drained` cleanup qualifications) and failed
independent grading (`2/4` useful `< 3/4`, `3` non-actionable limitation-derived
pseudo-gaps `> 1`; all `24` cards truncated at `900 B`). Master then approved a
bounded two-correction amendment (full-fit-first `<= 8192`-byte raw and
JSON-encoded card projection with UTF-8 head/tail truncation inside the
`<= 144 KiB` frame + `8 KiB` reserve without average quota, and removal of
`extractFocalGuaranteeGapFromLimitation` so `limitations` remain uncertainty
records only) and one additional `<= 25`-attempt original-set quality
experiment. The five affected focused scopes and independent original-rule
quality component passed. The actual cohort saved all 25 attempts, retained all
24 cards in full and produced 290 independently inspected proposals. Three
`signal_error_drained` qualifications, zero project-source inputs, two accepted
borderline analogies and prior failed cohorts remain explicit in the
[quality record](docs/testing/knowledge-quality.md).
The subsequent full common failed on two stale contract digest records and one
invalid CLI test status expectation; integration, persistence and all 95 E2E
cases passed. The bounded metadata/test correction passed its CLI race case and
current prepare stage, with no product-code change. The failed aggregate is not
relabelled as passing. Independent final original-Goal and scoped contribution
judgments, explicit settlement of all 21 original findings, current composition
and CO product review establish the bounded delivery. All five native Goal
criteria are satisfied, and CO accepted the exact member under Master delegation.
The normal completion commit and native finalization follow; Tasks03–06 and
Master overall Epic acceptance remain future.

TASK-008-03 adds exact-target freshness-only assessment at SQLite revision 22,
fixed per-unit history/coverage, guarded complete-file source context and canonical
document/Library/Evidence freshness separate from Review. The
[freshness record](docs/testing/region-freshness.md) preserves original source,
provider, semantic, browser/native and cleanup qualifications, including failed
cohorts and five historical unknown native drains. Task02 prompt/schema/ranking
and accepted quality remain applicable when unchanged.
The full common remains failed on a stale public E2E null expectation; all 1,423
Go and 256 other Python passes compose with the corrected complete testcase and
current documentation checks on unchanged product inputs. No fresh aggregate
pass is claimed. Independent current judgments and criterion composition support
the bounded delivery, accepted by CO under Master delegation. The normal commit
and native finalization precede Task04. Task04 overlays, Task05 decisions/undo,
Task06 drafts/assembled exit and Master overall Epic acceptance remain future.


TASK-008-04 adds schema23 separate origin-labelled relation/topic repositories,
global active-effect key reuse and finding-effect revision links, guarded
category application, closed management reads and shared Maintenance/Reader/
settings consumers. [Management evidence](docs/testing/management-overlays.md)
retains the nonempty authored-preservation execution, nondisclosing unavailable
AI links and canonical/legacy topic-basis corrections. Optional decisions export
remains unavailable. The original canonical common remains FAILED with 1,692
passes and one RF06 failure; current coverage composes the exact five-name
test-only correction and one whole-container race/persistence pass with the
unchanged members and current documentation evidence, never an aggregate pass.
Bounded browser/native observations retain the browser aggregate failure,
late Close 404, unknown pre-browser canonical baseline and ReaderMode-only scope.
Original source14+8/original16-call, partial/invalid and cleanup qualifications
remain unchanged; no model regrading, installed or physical-device proof is added.
Delegated Coordinator member acceptance establishes the bounded Task04 delivery.
Its normal completion commit and native finalization precede Task05; this record
does not start Task05. Task05 decisions/pins/undo must reconcile all linked
findings; Task06 retains drafts/full report/run-level membership discovery.
Overall Epic/Stage B and Master acceptance remain future.

TASK-008-05 adds schema24 owner decision/history/bases and current effect links,
guarded finding approve/reject, actual Topic PUT, atomic all-linked Undo memory
and exact owner-only finding-bound scan admission. The
[finding-decision record](docs/testing/finding-decisions.md) owns settled source
corrections, focused checks and qualified controlled browser/native observations.
The browser selection passed; native action/frame rows passed but its broad
Library DTO invariant aggregate failed. Separate GET-only poststate proof and
source review explain the expected fixed no-C membership coverage projection
and matching whole-DTO validators; this fixture exercises no genuine C case.
The current canonical common passed with 1,464 mandatory Go tuples exactly once
and 259 Python cases, including all 97 E2E cases; all child commands exited zero.
Current factual documentation passed its separate affected check.
Source-qualified empty selections and captured policy two/current configuration
three remain distinct. Final independent criterion composition and Coordinator
delegated member acceptance establish the bounded Task05 delivery. No commit or
native finalization is recorded here. Task06 drafts/full report/assembled exit,
overall Epic and Stage B acceptance, and physical-device qualification retain
their ordered owners.
