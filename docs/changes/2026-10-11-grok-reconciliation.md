# Grok readiness review and approved private renderer ports

**Documentation bundle:** 1.5.0 -> 1.6.0

**Owner decision:** On 2026-10-11 the owner authorized additional tailnet-only HTTPS renderer-port experiments and requested the necessary Grok feedback be reflected in the plan. D-10 records that approval. This revision edits specifications only; it does not configure Tailscale, expose a port, start a service, use a provider account or pass an implementation gate.

**Historical reviewed input:** `review/grok-1/implementation-readiness.md` was the review source for 1.6.0. It is absent from the current checkout at the 1.6.2 check; its path is retained as unavailable provenance, not a working link. D-01 through D-09 remain established, including ordinary JavaScript, multiple HTML pages per result directory, no arbitrary artifact size/count caps, remembered owner access, Wails and private remote Stage A.

## Approved renderer direction

D-10 permits additional HTTPS ports inside the existing tailnet to test distinct renderer origins on the same private node name. Select ports/count/reuse after inspecting actual conflicts, current Serve/Funnel mappings and affected services. The first candidate maps two private renderer ports to different internal renderer routes on the same daemon; the external origin, not a browser-visible path prefix, distinguishes each root-relative namespace. Management remains on its own endpoint. See [the experiment plan](../operations/deployment.md#approved-additional-port-renderer-experiment).

This is permission to try that construction, not a declaration that it works. G-VIEW must still prove simultaneous versions, scripts/modules/workers, scoped delivery, storage and safe origin reuse on the required real environments. Do not reinterpret a finite example of two ports as a stored-document, file-count or single-version product limit. No public Internet exposure, custom domain, wildcard certificate or unrelated service reconfiguration is authorized. This update performs no network changes.

## Import control-plane disposition

The new [import workflow contract](../contracts/import-workflow.md) is the single normative location for these specific shapes and transitions. The shared registry delegates to it, and HTTP/CLI/data-model/Tasks reference it rather than defining competing variants.

| Review item | Disposition and resulting rule |
| --- | --- |
| C1, upload scope/purpose | Accepted. Existing action scopes authorize only their own preparation. draft.write can stage a draft-specific result without import.publish; direct publication remains unavailable. Body/metadata actions have closed exact target variants. No new upload-admin role or author permission ceremony. |
| C2, one CLI ID versus multiple K calls | Accepted with storage-location correction. The user-facing import ID belongs only to POST /imports. Create and seal use different automatically stored keys. The caller journal records associations and the original target/base before a final server receipt exists; no imaginary preexisting receipt or new workflow engine. |
| C3, omitted document target | Clarified, proposed server-memory fallback rejected. CLI/UI/workflow launchers resolve remembered choices. POST /imports requires document_id as an existing ID or explicit null; missing is invalid. The handler never guesses a last document. |
| C4, cancelling a seal | Gap accepted, cancellation ban rejected. Cancel stops/fences verification and preserves bytes; a non-abandoned upload returns to open only after worker stop is confirmed. Re-verification uses a new completion key/job. Explicit abandon prevents future reuse and reclaims only safe references. |
| C5, list/detail fields | Accepted. Lists carry summary fields and detail_url. Checkpoint, error, detailed ranges and seal-result links belong to detail. The canonical table is referenced everywhere. |
| Explicit immutable version conflict | Add IMMUTABLE_VERSION_CONFLICT, distinct from stale-base VERSION_CONFLICT, changed-operation OPERATION_CONFLICT and corrupt chunk hashes. Never overwrite a published version ID. |
| 202 and job kinds | Clarify existing response/job distinction. POST /imports commits a 202 receipt for an import job; upload_seal only seals staging. A completed receipt is not a completed job. |
| New completion key during an active seal | Its own completed 202 receipt references the same job at the same checkpoint. A different checkpoint conflicts; replay still returns the original response after the job finishes. |
| Body/context/metadata target shapes | Accepted. Closed purpose/action variants identify exact consumers and bindings, including future consumer operation IDs without requiring those receipts to exist yet. |
| Base capture timing | Normal clients keep the existing pre-transfer base and send it explicitly. A low-level omitted-base POST /imports captures latest once at its first valid reservation. Neither path refreshes the base on retry or silently rebases while a large upload runs. |
| moving_target and explanation references | Source intent does not force current mode. Explicit mode wins; historical defaults pinned, otherwise use the remembered project default. Explanation-only references are separate version/page/hash objects, not project-source citation variants or fake verified target sets. |

Staged data remains recoverable after mobile suspension or delivery lease expiry. Cancel is not discard. All transport preparation stays automatic, and absent source/region metadata still does not block ordinary import/rendering.

## One closing checklist per early gate

The complete [TASK-001-04 checklist](../tasks/EPIC-001/TASK-001-04.md#observable-completion-and-tests) closes G-VIEW. Required environments are pinned Wails, the selected primary browser and one real iOS or iPadOS Safari device through private HTTPS. The harness proves renderer/delivery/context behavior. It does not need the future preferred-default, Continue or Notes/Ask product UI, durable adoption or complete restore services. Their required production checks remain assigned to their feature Epics.

The complete [TASK-001-05 checklist](../tasks/EPIC-001/TASK-001-05.md#observable-completion-and-tests), not merely its final checkbox, closes G-AI. Require a supported real account and one real source-grounded question with truthful evidence, actual permission/authentication mode and thin-harness error/cancel/uncertainty handling. Label injected faults. Multi-page generation/usefulness remains G-VALUE/TASK-001-07; durable provider recovery is EPIC-006. No quota exhaustion or shared-account logout is required to provoke a test error.

The remaining agreed P-11 targets are later release obligations, not hidden early gate environments or silently dropped support. TASK-003-03 can reuse the earlier Wails harness for its bounded M14 contribution; the full companion remains TASK-003-05. TASK-007-03 names its physical-device closing set and later target coverage explicitly.

The ten-Epic, 61-Task sequence stays intact. TASK-001-01 can begin baseline/preflight work; an authorized experiment can produce real evidence inside EPIC-001. EPIC-002 still waits for all six readiness gates under the current plan. Writing this revision does not make a Task ready/done or a gate pass.

## Filesystem and historical-input treatment

User inputs may reside on another internal/external volume. Copy/stream into staging on the destination content filesystem, verify and flush, then use same-filesystem final publication. A cross-volume final rename fails explicitly and is not replaced by copy-and-delete advertised as atomic. Store exact author paths in the inventory and use content-addressed physical object names; a destination filesystem's case behavior is not an input-name ban.

At the 1.6.0 check, the nine historical links in the 1.4.0 change record pointed to absent reviews/2026-10-10-opus files. The earlier 1.5.0 PASS described a prior file snapshot, not this missing-input state. Their original relative paths are retained as explicitly unavailable provenance rather than dangling links or fabricated replacement files. No older review contents were reconstructed, silently substituted or claimed to have been inspected in this update. That check read the then-present Grok review; availability for the current patch is recorded below.

## Validation and remaining decisions

[Bundle validation](../testing/bundle-validation.md) records the actual final structural checks and their limits. [MANIFEST.md](../MANIFEST.md) is regenerated from final bytes. The unchanged Revision 4 source and worked-example payload/metadata hashes remain verification inputs. Documentation checks do not establish API behavior, browser compatibility, filesystem durability, a supported provider account or measured performance.

D-10 removes the extra-private-port approval question. No further owner product decision blocks this plan update. Real source/account/device authorization and actual host configuration are resolved only when the relevant experiment needs them. A demonstrated need for public exposure, custom domains/certificates, changed billing or reduced product scope remains a separate decision. Original user staging is preserved; no commit or push is part of this revision.

## 2026-10-12 follow-up (1.6.1)

The owner requested corrections after the 1.6.0 readiness re-review at `reviews/2026-10-12-grok/implementation-readiness.md`. That historical input is absent from the current checkout at the 1.6.2 check; it is not reconstructed or replaced here. This patch aligns older wording with the existing canonical contracts; it adds no product scope, task, gate, content limit or approval ceremony. D-10 still authorizes the private-port experiment, not a tested renderer or an actual network change.

| Review item | Applied correction |
| --- | --- |
| Document target and publication | Separate caller association/upload preparation from server publication. Require an explicit document_id or null; only null allocates a new document ID. An omitted project comes only from the sealed target. Preserve the normal pre-transfer base and the separate low-level omitted-base timing. |
| Composite operation IDs | Generic receipt summaries now distinguish each HTTP request's key from the final CLI domain key. Preparation keeps separate persisted keys; no single-key reuse across routes. |
| Upload list/detail | Storage UI and glossary use the summary fields and fetch detail_url for error/checkpoint/ranges. Missing list error is not evidence of success. |
| Context render binding | Preserve explicit nullable JSON upload keys. Query omission normalizes to null, instead of adopting the review's blanket omission error. Compare the complete normalized tuple before adoption; omission cannot clear R1 or infer the current renderer. The contract defines page-null transport and rejects empty/duplicate binding parameters without reserving a literal filename as null. |
| Draft replacement | HTTP and schema summaries require the exact draft/project/document target and matching metadata consumer. Owner authority does not rebind another draft's upload. |
| Abandoned sealing | Failure/recovery cannot reopen abandoned uploads. Active seal jobs receive cancel intent and settle only after confirmed stop; committed seals and adopted objects remain intact. |
| Gate summaries | T-001-04-04 explicitly includes the selected primary browser. Architecture refers to the full TASK-001-05 checklist; generation/value and durable recovery stay with their existing owners. |
| Review guide | The previously blocked refresh is now applied. The guide shows 1.6.1, D-10, canonical import rules and early versus release evidence. |

Existing fixtures now name wrong-draft rejection, normalized-null versus concrete context bindings and delayed seal failure after abandonment. They remain planned contract/runtime checks, not executed service tests. No new Epic or Task is added and no planning state or gate is advanced. That patch preserved the then-present review; this historical note does not claim that its file remains available. Final document checks and their exact scope are recorded in [bundle validation](../testing/bundle-validation.md).

## 2026-10-12 implementation handoff (1.6.2)

The owner requested this final specification cleanup before starting implementation. The input actually inspected is `reviews/2026-10-12-grok/follow-up-1.6.1.md`, with SHA-256 `fb2bc462643c8f3e2672c000d02916f5fa9f9625f15c7fe772aa50dc5e59ad1c`. Review paths are provenance; absent older inputs above are not fabricated, substituted or claimed as currently readable.

The schema now distinguishes this request's receipt identity from a body/metadata upload's future-consumer target.operation_id. Different upload and consumer keys are valid; changing the target on same-key replay still conflicts, and another consumer cannot adopt the bytes. The existing composite-ID fixture covers both question-body and import-metadata preparation. No field, endpoint or key format changes.

Architecture, security and deployment summaries reference the complete TASK-001-04 checklist instead of maintaining different early environment lists. The three missing historical links are replaced by explicit unavailable-path records. The validator's link and integrity checks remain enabled.

Implementation begins with [TASK-001-01](../tasks/EPIC-001/TASK-001-01.md) and follows the existing dependencies, including TASK-001-07 before TASK-001-06. No additional specification-only review round is a prerequisite. Concrete implementation failures and necessary corrections are handled in their owning tasks under the existing execution protocol. This handoff adds no product requirement, Epic, Task, gate or approval step, and does not pass a runtime gate. D-08/D-09 and D-10 remain unchanged; the approved private-port experiment requires the existing impact checks, not another approval of the same scope.
