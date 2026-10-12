# Personal-developer revision: 2026-10-10

**Documentation bundle:** 1.1.0 → 1.2.0

**Historical scope:** D-08 remains established. The later [1.3.0 directory-result amendment](2026-10-10-directory-results-revision.md), D-09, removes remaining artifact byte/count limits, mandatory manifest/entry-page assumptions and fixed host-context maxima. Apply the current contracts for directory input/output; this record preserves the earlier revision history.

**Authority:** Explicit owner direction, decision D-08. The owner clarified that Haeryong serves a personal developer, that strict security workflows impede their speed, and that JavaScript produced by tools such as Codex, Claude or Grok should support rich explanations without unnecessary restrictions.

**Status:** The product direction is selected. Renderer, authentication and adapter mechanisms remain implementation proposals. No application or runtime gate was completed by editing these documents.

## Product model

Haeryong is a personal knowledge reader for work the owner performs with AI tools. The owner chooses the agents, projects, packages and external resources they use. Normal HTML/CSS/JavaScript execution is the default. The product must make it easy to generate, import, read, interact, question and revisit explanations.

This direction supersedes the stricter trust model introduced by the [earlier review revision](2026-10-10-review-revision.md). Public hostile-content hosting, universal network containment, isolation from an unrestricted same-user process, and a new identity/permission platform are outside this baseline.

## Concrete changes

| Area | Current direction | Implementation consequence |
| --- | --- | --- |
| JavaScript | One html_js_v1 profile with ordinary author code | DOM manipulation, modules, frameworks, chart libraries, WebAssembly and useful worker code follow normal browser capability rules. No required DSL or JavaScript API allowlist. |
| External resources | Normal network/CDN use is allowed | CORS, HTTPS/mixed-content rules and browser permissions still apply. No all-egress blocking guarantee, dependency-by-dependency approval or mandatory RTC/DNS containment test. |
| Viewer and application | Page execution stays separate from Haeryong management | Do not supply the trusted host origin's token/DOM, native Go bridge or generic management proxy to the page. Region/state messages convey context. Notes, Ask, Review, delete and settings remain explicit host actions. |
| Device connection | Pair once and remember the device by default | Proposed per-device bearer in the trusted UI's origin-scoped localStorage for local and remote browsers. Reload, new tabs and ordinary browser restart reuse it. Temporary mode and optional expiry remain available. |
| Owner authority | One owner manages their library | An ordinary owner session can approve a device and manage settings/lifecycle. Destructive impact confirmation remains; separate administrative or consequential grants are removed. |
| Private access | One loopback service endpoint | Proposed 127.0.0.1:9270 with optional private HTTPS proxy to the same endpoint. No required second listener or exclusive cookie hostname. Authentication uses explicit bearer headers, not ambient cookies. |
| AI workflow | Reuse the owner's configured agent environment | Haeryong-owned skills and compatible package imports work with external agents. Built-in adapter support remains a separate convenience. Source/provider choices persist; no prompt for each ordinary read. |
| Source access | Preserve exact evidence and configured scope | The daemon captures actual source reads and uses suitable provider permissions. It does not claim to confine every tool in an external agent or require a new OS worker jail. |
| Backup | Coherent private local backup with integrity checks | Optional encryption for owner-selected exports or storage destinations. A separate encrypted archive/key-recovery platform is not a baseline prerequisite. |
| Verification | Focus on the personal workflow and concrete failures | Verify real JavaScript/library compatibility, remembered connections, host/native separation, useful cited explanations and data recovery. Repeat or expand checks for actual uncovered risks. |

The token-storage mechanism is a practical proposal for a trusted personal application origin; scripts running in that origin can access its storage. Generated pages therefore execute elsewhere. This boundary is not a claim that owner-selected scripts cannot send the data they receive to external services. Network use is deliberately allowed. The application does not silently pass its account secrets to those pages.

Local bundling is recommended when an explanation should reproduce without external services. An external dependency may change or become unavailable; the immutable package hash verifies the bundled bytes, not future responses from a CDN. Runtime-fetched data does not automatically become captured source evidence. Material project-source claims still require exact supporting evidence, and a missing requested pinned/current source is not converted into a verified explanation-only answer.

## Applicability to the preserved source

[Revision 4](../sources/haeryong-concept-proposal-v4.md) and all 29 acceptance texts are retained verbatim as historical source. Current requirements and tests apply the following explicit amendment. Original wording cannot be used to reintroduce restrictions that the owner rejected.

| Source requirement | Current application |
| --- | --- |
| C02 import/persistence | Validate package integrity, paths and atomic publication. Normal JavaScript, modules and external resource references are valid content. |
| C03 interactive reading | Preserve useful normal web interaction across desktop and browser clients. A static or DSL-only substitute does not satisfy the chosen JavaScript capability. |
| C04 and M19 source evidence | Retain exact-source citations, declared source mode and honest missing-source behavior. JavaScript freedom does not waive factual grounding. |
| M08 isolation | Retain host-credential/native/API separation and owner-only personal/lifecycle actions. Replace the broad adversarial-source/tool-confinement assumption with configured owner workflows; no universal network or external-agent jail guarantee. Haeryong itself does not autonomously edit source. |
| M14 Wails compatibility | Verify the selected runtime's real bridge boundary and normal-script compatibility. Do not require a comprehensive adversarial browser platform or claim isolation from iframe flags alone. |
| M16 endpoint policy | Keep explicit configurable port, visible collision, private proxy and recovery. A second credential-channel listener is not required. |
| M17 authentication | Keep valid-bearer checks, revocation, exact browser origins, private HTTPS and safe return paths. With no ambient cookie authentication, test rejection of unauthenticated cross-origin mutation; do not add a cookie-CSRF subsystem. |
| M13 and M20 retention/restore | Preserve impact confirmation, shared-object protection and restore-time session/automation reconciliation. No additional administrative credential or mandatory encrypted portable container is implied. |

Other source-grounding, versioning, link, personal-review, lifecycle, durability and maintenance requirements retain their meaning. This amendment changes the product trust model explicitly; it does not falsely report the older, broader containment promise as implemented or tested.

## Corrections retained from the review

- Immutable versions, exact source/reference/region identity, and citations tied to the bytes actually supplied.
- Personal review owned by the user and exact version, distinct from freshness and document lifecycle.
- Per-resource revisions, operation receipts and retry ordering that avoid lost edits and duplicate mutations.
- Durable jobs, provider-attempt fencing and honest uncertain-result recovery.
- Shared-object pins, coherent backup/export, and restore rules that preserve the owner's data.
- Early useful-explanation evidence and corrected prerequisite ownership in the ten-Epic plan.

These are direct product behavior and data-correctness requirements. They remain useful for a single developer and should not introduce extra routine approvals.

## Execution and remaining decisions

The ten-Epic sequence and current 61 Tasks remain planned. G-STACK, G-AUTH, G-VIEW, G-AI, G-VALUE and G-CONTRACT remain not_run; their scope now follows this personal-tool baseline. The implementation should choose a renderer that supports real modules, relative assets and workers where supported, rather than freezing an opaque-origin construction that silently disables them.

No additional owner decision is needed about allowing JavaScript, choosing sandbox flags, or accepting this lighter workflow. Reuse existing project/account authorization. Only an actual unsupported provider/billing/deployment choice or missing host/account/source authority requires a concrete later owner decision, as described in [decisions](../decisions.md).

See [viewer protocol](../contracts/viewer-protocol.md), [authentication](../contracts/authentication.md), [configuration](../contracts/configuration.md), [knowledge quality](../testing/knowledge-quality.md) and [documentation validation](../testing/bundle-validation.md) for the current contracts and evidence status.
