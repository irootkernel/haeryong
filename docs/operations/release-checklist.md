# Release acceptance checklist

**Status:** Not run. This checklist defines evidence to collect in EPIC-010, not a completed release. Use the task and Epic evidence templates and retain links to actual implementation commits, build outputs and test records.

Apply the [personal-tool amendment](../changes/2026-10-10-personal-tool-revision.md) and [directory-results amendment](../changes/2026-10-10-directory-results-revision.md) and the [Opus reconciliation](../changes/2026-10-11-opus-review-reconciliation.md) throughout. Early G-VIEW evidence requires pinned Wails and a real private Safari iOS/iPadOS device through TASK-001-04 before G-CONTRACT; an unavailable required target remains blocked. Other early evidence starts with the owner's primary setup and a private second device. The initial targets in decisions P-11 and the deployment tiers remain in scope; release claims need evidence for the functionality advertised on each. Superseded threat assumptions and combinations outside the declared targets are not hidden blockers.

## Entry

- [ ] All preceding Epic gates and their completion_requires tasks pass with reviewed actual evidence.
- [ ] All six readiness gates, including G-VALUE, pass their current narrow practical procedures for the chosen implementation and required targets; unavailable required evidence is not a pass.
- [ ] Source-derived requirements, proposed decisions and any approved scope changes are reconciled.
- [ ] Release/version identifiers, toolchain and package/API/skill/config compatibility matrix are pinned.
- [ ] Actual epic-handler metadata has been checked where integration is intended.

## Product and UI

- [ ] The current procedures for all 29 preserved inherited scenarios, X01–X20 and knowledge-quality checks pass with separate early bounded, first integrated feature and release-regression evidence; a review script's all-requirements-per-owner heuristic is not the coverage rule.
- [ ] Desktop and browser use the same library, versions, notes and personal review state.
- [ ] Core workflows work on a real private second device with Wails closed. Functional compatibility for the declared initial desktop/headless/browser targets is recorded before claiming support; extra combinations remain explicitly unverified.
- [ ] Source version, explanation version, personal review, freshness and lifecycle are visibly distinct.
- [ ] New directory versions do not inherit Reviewed; visiting any or every HTML page never completes the whole version automatically.
- [ ] Multiple HTML pages, nested relative modules/assets, optional landing choice or page/file list, query/fragment and Back/Forward work; exact version/page/optional region notes never collide across pages. The selected Wails and private-browser constructions preserve concurrent versions with distinct bytes at identical root-relative fetch/module/worker paths, including new tabs.
- [ ] No compulsory manifest/index/region/source metadata, type whitelist or arbitrary file-size/count/depth cap blocks intake. Missing source metadata is visible as unknown/unverified, while supported previews and downloads expose preserved files.
- [ ] Screen routes, keyboard/touch controls, non-color status cues and disconnected/unsaved behavior are checked.
- [ ] Links preserve exact targets through login/reload; no canonical localhost, delivery credential or silent latest-version substitution. Preferred default, last-opened pointer and per-page position actions have independent state/ETags.

- [ ] Citation resolution, semantic claim support, useful JavaScript/module interaction and Markdown comparison and held-out freshness/relation cases pass the [knowledge-quality method](../testing/knowledge-quality.md); sample statistics are not general guarantees.

## Data and operations

- [ ] Plain directory/ZIP/single-HTML import is atomic; actual corrupt archives/paths/bytes, duplicates and same-ID conflicts have tested behavior. Unknown attachments, absent metadata, retained link entries and normal executable-looking files remain valid content.
- [ ] X18/X20 demonstrate complete content beyond every former package/file-count/context threshold using streams/chunks/cursors/ranges and automatic context_ref. Actual disk/I/O/resource failure has progress, cancellation and resume/retry without partial publication, truncation or skipped files.
- [ ] Shared assets, canonical backlinks, denied/trash visibility and restore identities remain correct.
- [ ] Private local backup captures a coherent DB and referenced content/source inventory with validated checksums; read/staging/backup pins protect concurrent collection.
- [ ] Restore rotates runtime_generation/auth_epoch/stream_epoch, invalidates prior device tokens/cursors and preserves uncertain attempts. A normal owner review precedes resuming affected restored source/provider choices, schedules, purge or exposure.
- [ ] Migration failure, file flush/rename/DB commit faults and interrupted import have tested recovery on isolated profiles; process interruption alone is not claimed as a power-loss test.
- [ ] Config changes are validated, revisioned and visible; remembered source/provider/output settings and mid-run restriction are tested. config_revision and source/disclosure permissions_revision retain their separate roles.
- [ ] Explicit/remembered/new-document association, stable Unfiled fallback, same-operation normalization replay, wrong-target failure, draft replace/reject and monotonic cancel/resume have distinct contract fixtures.
- [ ] Upload discovery/status shows actual persisted staged bytes after reconnect/restart. Sealing/explicit abandonment and Save/Ask adoption races preserve acknowledged bytes and active/unknown/saved/recovery pins; cleanup cannot erase resumable context/result staging based on age, lost heartbeat, mobile suspension or expired delivery lease alone.
- [ ] Scheduled work is durable and bounded; missed runs are honest, not claimed as completed.
- [ ] Retention duration alone cannot enable purge; impact preview and execution-time eligibility are tested.

- [ ] Directory export preserves all selected file paths/types/bytes, subdirectories, empty directories and recorded links without flattening, forced manifest or arbitrary export caps; selected personal metadata keeps exact page anchors and reference states. Separately retained source snapshots are opt-in, while ordinary source files already in the imported directory remain included. Verification is separate from backup recovery and promises no general personal-state merge.
- [ ] No backup yet, a previous complete verified set/time/age, the latest incomplete/failed attempt and a real trial-restore outcome are distinct. Manual backup remains the normal default; checksum verification never claims an unrun restore succeeded.
- [ ] Incomplete or checksum-mismatched backups fail verification. Optional export encryption, when implemented and selected, has a verified round trip and clear missing/wrong-key failure; local backup does not depend on a new key-management platform.

## Host access and AI

- [ ] html_js_v1 runs useful ordinary JavaScript, modules/frameworks, WASM/workers and browser-permitted network/CDNs. The generated frame cannot read host DOM/storage/tokens, invoke native Go bindings or use fabricated messages for management actions; local-byte integrity and broken-page recovery are checked. Authored Service Worker registration/replacement, safe origin reuse and same-site/cross-site storage after reload/restart/foreground follow the [G-VIEW proof](../contracts/viewer-protocol.md#focused-g-view-evidence); cross-version mixing fails.
- [ ] Owner authentication covers private JSON, source, content and events; revoked/expired owner sessions stop protected delivery. The normative [delivery-authority checks](../contracts/authentication.md#content-delivery-authority) pass: scoped Range/retry/resume, observed closure, automatic authorized lease/restart recovery, independent download lifetime, credential placement/exposure and secret redaction. Resumable uploads remain governed by their own adoption/abandonment and persisted pins.
- [ ] First local bootstrap, trusted-device approval, single-use pairing, safe returns and remembered bearer reload pass locally and remotely. Host-origin localStorage remembers access by default while browser storage is available; actual storage loss, explicit expiry, memory-only recovery and optional Home Screen use as a separate storage context are tested separately from normal restart, and normal owner impact confirmation handles management actions.
- [ ] Managed source/provider calls respect remembered owner choices, report actual permissions and cite same-byte exact revisions with truthful observed_input or posthoc_verified basis; provider-input proof is separate from semantic support, and pages without regions have nonvacuous freshness coverage; new scope uses normal owner confirmation. Existing external agents retain their normal environment, without a mandatory OS jail or blanket plugin/hook removal.
- [ ] A supported real provider path is verified for this distribution; no implicit API billing or account/provider fallback exists.
- [ ] Same-daemon attempt_generation, cancellation dispatch rechecks, orphan accounting and restored uncertainty prevent stale application or blind duplicate external work; local worker expiry does not imply a remote call stopped.
- [ ] Logs, exports, fixture data and diagnostic artifacts contain no secret/private-source leakage.

## Packaging and sign-off

- [ ] Headless installation requires neither Wails nor a GUI session or production development server.
- [ ] Desktop quit/crash cannot stop the daemon; explicit service control behaves separately.
- [ ] The same configurable application listener (default 127.0.0.1:9270) serves local access and the optional private HTTPS proxy. Explicit endpoint changes, port conflicts and local recovery work without a second library or a silently exposed API listener.
- [ ] Fresh install, upgrade, uninstall-with-data-retention choice and local recovery are exercised.
- [ ] Dependency/license inventory, software bill of materials where produced, build checksums and signing/notarization requirements are documented for the chosen distribution.
- [ ] Operator commands and UI documentation match implemented behavior; unsupported paths are labelled.
- [ ] A known-issues register names severity, impact, workaround and explicit release decision.
- [ ] Actual two-project reader/quality evaluation and required release decision are recorded with participant roles; a human owner walkthrough is never inferred from an agent-only run or this checklist.

## Release record

Record release commit, artifact hashes, actual selected devices, provider integration mode, effective-default configuration, focused host/session evidence, full acceptance outcomes, restoration evidence, known issues and final approving actor. Store only sanitized artifacts. Public distribution, signing identities and commercial/provider eligibility remain explicit decisions; this bundle does not authorize or perform publication.
