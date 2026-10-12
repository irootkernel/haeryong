# Review-driven revision: 2026-10-10

**Documentation bundle:** 1.0.0 → 1.1.0

**Historical record:** The owner subsequently selected the [1.2.0 personal-developer amendment](2026-10-10-personal-tool-revision.md). Its normal-JavaScript, remembered-device and proportionate-boundary requirements supersede stricter viewer, authentication, worker-containment and backup proposals below. Data-correctness corrections remain in the current contracts. The subsequent [1.3.0 directory-result amendment](2026-10-10-directory-results-revision.md) also removes artificial artifact byte/count and compulsory packaging constraints. Read this file as revision history, not as an override of the later owner direction.

**Authorization:** The owner requested that the validated review feedback be incorporated into docs.

**Implementation status:** No application code or runtime gate is completed by this revision.

**Historical inputs:** [Revision 4](../sources/haeryong-concept-proposal-v4.md) and the external reviews remain unchanged.

This record explains what the revision changes and which choices remain conditional. It is not a claim that every finding in the reviews described an implementation defect: this repository contains execution specifications, and several risks were already recorded as unrun gates.

## Review disposition

**Historical-path note added 2026-10-11:** The five original inputs were cited under `reviews/2026-10-10`, which is absent from the current checkout. Their filenames below preserve the historical attribution without broken links. The separately supplied `reviews/2026-10-10-opus` reports are different inputs, addressed in [the current reconciliation](2026-10-11-opus-review-reconciliation.md); they are not substitutes for these missing originals.

| Review area | Accepted change | Qualification |
| --- | --- | --- |
| Usefulness (historical `01-usefulness.md`) | Add G-VALUE and a two-project explanation/claim corpus before contract freeze; reuse it for generation, remote reading and semantic maintenance; distinguish evidence resolution from claim support | Initial sample sizes and reader-task observations do not prove general accuracy or an arbitrary adoption/retention target |
| Feasibility (historical `02-feasibility.md`) | Check intended provider/deployment eligibility early; compare safe viewer candidates; move prerequisite local-job/event/pin infrastructure before its consumers | Preserve Go, Wails v3, subscription-first integration and initial remote scope; do not infer a delivery duration from document count |
| Consistency (historical `03-consistency.md`) | Centralize shared schemas/routes/enums; complete UI resource reads; specify retry/precondition order, state transitions, local ID scope and missing invalidations | Existing invariant statements were often directionally correct; the correction is the concrete contract and traceability needed to implement them consistently |
| Security and operations (historical `04-security-ops.md`) | Separate local/remote ingress, correct cookie assumptions, strengthen viewer/provider containment evidence, restore invalidation, object pins and durable publication | Do not claim a demonstrated Wails vulnerability or universal sandbox impossibility; unrestricted same-user host control and full OS rollback remain outside the stated guarantees |
| Delivery plan (historical `05-delivery-plan.md`) | Remove the six-task invariant, add TASK-001-07, make gate passes mandatory, distinguish initial tests from release regressions, and centralize common task completion rules | TASK-001-05 already included a real AI question. EPIC-006 still owns product generation/Q&A; the revision strengthens early eligibility/value evidence rather than pretending AI was entirely absent before then |

## Product commitments preserved

The independent Go daemon, SQLite plus immutable content, Wails v3 client and shared private browser reader remain the baseline. Stage A includes authenticated remote access and one real supported subscription-capable AI integration. The Haeryong-owned skill and contracts remain usable without Aquarium as a runtime dependency.

Document versions, evidence and authored references remain immutable and pinned. Personal review belongs to the owner and exact version; new versions do not inherit Reviewed. Revisit and lifecycle are document-level, while freshness is a scoped assessment. Schedules, action policy and retention remain configurable, with explicit publication and purge authority.

This revision does not authorize enrollment, provider login, private-source submission, deployment or a billing change. Those implementation actions use the owner's existing authorization where available and obtain concrete missing authority only when needed.

## Concrete contract changes

| Area | Revised contract |
| --- | --- |
| Retry identity | Receipt key is principal plus operation ID. Method, canonical route/target, body and meaningful original preconditions define its fingerprint. Current authorization precedes receipt replay; existing outcome reconciliation precedes reevaluation of the original If-Match. |
| Personal state | Review uses one PUT contract with explicit start/complete/reopen/clear behavior and retained audit. Revisit is document-level. Per-item validators support notes and other independently mutable resources. |
| Lifecycle | Archive/trash/restore/purge act on a document and its versions. Restore recovers the pre-trash active/archived state; purge is terminal. |
| Background work | Minimal local jobs, receipts, revisions, outbox and pins are EPIC-002 foundations. Provider attempts in EPIC-006 add active-attempt/generation fencing and uncertain-outcome reconciliation. |
| Events | Epoch plus sequence identifies replay position. All user-visible mutable resources have invalidation behavior, including content that becomes hidden after it was rendered. |
| Source/provenance | Immutable identity and intent, retention policy, retained bytes and current authorized availability are separate. Source-change observation records target and coverage without declaring semantic alignment. |
| Files and recovery | Active reads/jobs, drafts, publication staging and backup/export snapshots protect consumed objects. Success requires validated flush/publication/database ordering. Supported restore resets credentials/streams and reconciles uncertain work; it cannot certify arbitrary OS-image rollback safety. |
| Authentication | Local browser and remote proxy use distinct ingress channels. Proposed ports are 9270 local and 9271 remote upstream. Local tabs use memory-held credentials; remote cookie use requires an exclusive HTTPS host. The exact UX and implementation must pass G-AUTH. |
| Viewer | Candidate profiles are trusted_interaction_v1 and restricted_script_v1. Useful scenarios and actual egress/navigation/native-bridge behavior determine selection; a static fallback cannot close interactive scope. |
| Provider boundary | Official support for the actual deployment, controlled tool/configuration loading, narrow reads and explicit disclosure are required. A provider's read-only label or externalSandbox setting is not a containment proof. |
| Portability | Provide an owner-authorized export of packages and exact version-anchored records, distinct from backup. Do not promise arbitrary cross-library personal-record merging or include source snapshots without authority. |

The [schema registry](../contracts/schema-registry.md) owns shared vocabulary. The detailed [HTTP](../contracts/http-api.md), [jobs/events](../contracts/jobs-events.md), [authentication](../contracts/authentication.md), [viewer](../contracts/viewer-protocol.md), [package](../contracts/package.md) and [source](../contracts/source-access.md) contracts define behavior. Runtime schema 1.0 remains a proposed, unfrozen interface; documentation version 1.1.0 is not a runtime compatibility guarantee.

## Revised execution and evidence

There are still ten sequential Epics. The current breakdown contains 61 Tasks, with TASK-001-07 executed after TASK-001-05 and before TASK-001-06. A fixed task count is no longer a planning invariant.

G-STACK, G-AUTH, G-VIEW, G-AI, G-VALUE and G-CONTRACT all start not_run and must pass for the selected baseline before EPIC-001 closes. A failed or blocked gate permits bounded repair/evidence work within the Epic, not a claim of completion. Stage A and release reuse earlier security/recovery tests rather than postponing their first execution.

[Knowledge-quality evaluation](../testing/knowledge-quality.md) covers real source-grounded explanations, a Markdown/ADR comparison, suitable interaction, source-change coverage and saved Q&A. The initial 3–5 explanations and roughly 20–30 material claims are a proposed starting corpus. Actual project revisions, provider use, findings and evaluator limitations are recorded during implementation.

[Contract cases](../testing/contract-cases.md) identify first implementation ownership and later regression ownership. The [traceability matrix](../testing/traceability.md) maps the unchanged inherited acceptance scenarios and the elaborated requirements.

## Conditional owner decisions

No new owner decision blocks this documentation update. The [decision register](../decisions.md) records the concrete trigger for each later decision:

- A safe viewer cannot express an essential selected scenario: review working alternatives and their limitations.
- The intended subscription route is unsupported for the actual deployment: choose a supported deployment, an explicit billing change or a pause.
- A real spike needs source/account access: identify approved projects, revisions and disclosure scope.
- Deployment/distribution needs owner authority: select the host, network enrollment, signing identity and license policy.

Exact toolchain versions, driver selection, routine contract repairs and task sizing are implementation decisions. Do not turn them into repeated owner-approval gates.

## Documentation validation

The source hash remains the authority for preserving Revision 4. The [bundle-validation report](../testing/bundle-validation.md) records actual link/identifier/dependency/example/inventory checks for this revision. [MANIFEST.md](../MANIFEST.md) is regenerated after the document edits. These are documentation checks; no Wails/browser attack test, provider entitlement, application migration or source-project quality result is represented as passed.
