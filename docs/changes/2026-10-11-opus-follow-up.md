# Additional Opus review reconciliation: 2026-10-11

**Documentation bundle:** 1.4.0 -> 1.5.0

**Authority:** The owner requested applying the accepted findings in reviews/2026-10-11-opus and asked whether further product decisions were needed. This revision preserves D-01 through D-09 and the existing ten-Epic delivery scope. Existing unstaged amendments were inspected and retained while completing contract integration. The original concept and reviewer files remain unchanged.

**Status:** Documentation and validation utility only. No Haeryong runtime, browser, provider or device test is marked passed by these changes.

## Accepted corrections

| Review area | Current disposition and authoritative location |
| --- | --- |
| Existing-document import, persistent binding and new-operation base | Shared import controls offer New document or Existing document, new version. Reader/Versions preselect their document. The [package binding table](../contracts/package.md#remembered-association-and-operation-base) separates persistent project/document choices from the base captured once per operation. A retry keeps its original base; a later independent operation may capture a newer base. Paths/hashes never silently merge document identity. |
| Forgotten uploads and sealing recovery | The [HTTP contract](../contracts/http-api.md#transfers-and-on-demand-files) and CLI expose upload list/status/abandon with owner recovery, usage, checkpoints and reusable/unavailable state. upload_seal is a durable job kind. Explicit abandon and pin/adoption accounting determine cleanup; a lost tab, lease or elapsed age does not discard an upload. |
| Imported source verification | TASK-004-01 implements an explicit exact-target check and history; TASK-004-05 connects the Evidence action. [Source access](../contracts/source-access.md#checking-imported-evidence) and the [wire registry](../contracts/schema-registry.md#source-and-assessment-views) distinguish author capture declarations from service captured/matched/mismatch/unavailable results. Optional expected quotes/digests do not become author admission requirements. |
| Real provider-input observation | Direct adapter-supplied evidence or actual exact-byte tool observations can establish observed_input; making a snapshot directory available alone cannot. A later source check is posthoc_verified, not proof of what a model previously read. Acquisition and semantic support remain separate. The [AI adapter](../contracts/ai-adapter.md) requires a tested path for the first selected provider/version without requiring native telemetry from every external agent. |
| Per-unit freshness | Persist the required version/page/region tuples and assessment outcomes rather than only counts. Same-target page checks can contribute to a compatible whole-version page summary without double-counting. Partial regions cannot certify a complete page. Target selection and captured_at are explicit, and status reads do not fetch source or call AI. See [shared registry](../contracts/schema-registry.md#source-and-assessment-views). |
| Renderer proof and lifecycle | TASK-001-04 explicitly owns concurrent versions with different bytes at identical root-relative fetch/module/worker paths, pinned Wails and actual private HTTPS Safari iOS/iPadOS evidence. Select construction-dependent wire/configuration shapes before G-CONTRACT. Required proof cannot be replaced by a labelled limitation. Endpoint-specific implementations remain possible without a mandatory wildcard domain or new public service. |
| Service Workers, storage and native origin | The [viewer contract](../contracts/viewer-protocol.md#artifact-and-trusted-host-separation) distinguishes ordinary workers from Service Workers, tests authored root-scope registration and origin reuse, and records the actual trusted Wails origin/Go-proxy path and same-site/cross-site storage behavior. Stored-byte integrity is not a claim that author-generated runtime responses are immutable. |
| Delivery authority | [Authentication](../contracts/authentication.md#content-delivery-authority) is the normative lifetime/credential definition. Normal renewal needs no new pairing. Unexpected disconnect is not an observed close, expiry does not abandon uploads, and progressing downloads have no fixed whole-transfer deadline. Ordinary script/network capability remains unchanged. |
| Unicode paths and actual picker fidelity | Preserve original names and hashes; exact lookup wins, then a unique canonical-equivalent Unicode fallback is allowed. Ambiguity fails explicitly; no case folding, compatibility normalization or renaming is introduced. FileList-only selection cannot invent empty directories; a complete tree uses a path that actually represents its entries. |
| Job/receipt closure | Reconcile paused uncertain work without dispatching another attempt; keep cancellation monotonic and distinct from failure. Distinguish accepted reservation, definitive response, job creation and later job outcome. Concurrent duplicates receive pending status rather than a second effect. |
| UI and execution ownership | Connect import/export, upload recovery, source checks and inspection of coverage to their actual APIs and Task owners. First feature proof, bounded early evidence and release regression stay distinct. No new Epic is added and no future-feature proof is fabricated in an earlier Task. |
| Everyday usefulness and operating information | Reader checkpoints record the actual reason for reopening and observed friction/benefit, not an inferred human study or compulsory multiweek experiment. Backup age/outcomes are visible, but no backup scheduler, purge delay or pre-purge backup requirement is introduced. |

## Qualified or not adopted

A mandatory quotation for every citation, compulsory source metadata at import, provider-wide native-read telemetry, one enormous transferable buffer as the only context path, automatic staging TTL, or shrinking the application to CLI-only use are not adopted. Small pre-materialized contexts may use a simpler negotiated transfer path; incremental transfer still handles larger content without artificial quotas.

Unfiled is a normal configurable project, not a permanent inability to use source features. Moving existing documents between projects is not added implicitly: it affects historical membership, source authority and in-flight work and remains a separate product enhancement. Current project selection/new-version import does not depend on it.

Position saves are coalesced and serialized, not emitted with a receipt per scroll event. Existing resource conflict handling remains; switching all devices to last-writer-wins is not silently approved. Notes, explicit preferences and Review retain their existing stronger semantics.

Provider authentication is checked for the actual managed invocation. Run-local protection from unintended billing must not rewrite global configuration, alter the owner's shell environment, clear shared credentials or force logout. Q&A/assessment use an appropriate supported non-writing mode where available without introducing an OS jail or globally disabling owner tools.

The review's broken-link count came from its renamed sandbox review folder, not this checkout. The original crosscheck script is retained as review evidence. The portable [documentation validator](../testing/validate_docs.py) resolves the actual repository root, checks named role edges and collective case coverage, and reports its scope rather than claiming runtime correctness.

## Remaining decisions

**No new product decision is required to complete this documentation revision.** Implementers select chunk sizes, ordinary retry timings, a proven renderer construction and the first supported provider binary under existing requirements.

A later owner decision is required only when observed evidence requires new deployment/account authority or a change to agreed product scope, such as a custom domain/certificate or exposing extra ports, changing a billing route, or changing Wails/private-browser/concurrent-version support. Demonstrate supported alternatives first. Optional project moves and a different conflict policy can be proposed separately; they do not block the planned work.

## Verification and preservation

The [integrity report](../testing/bundle-validation.md) records the checks actually run on final files. The [manifest](../MANIFEST.md) covers every other Markdown document and the validation utility. The original concept hash and the 29 inherited acceptance sentences remain unchanged; current applicability follows the owner amendments. All 61 Tasks and 10 Epics remain planned and all six runtime gates remain not_run. No staging, commit, push, deployment, provider call or real-device test is authorized or performed by this documentation revision.
