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
