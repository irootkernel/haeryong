# Acceptance catalog

**Inherited source:** [Revision 4](../sources/haeryong-concept-proposal-v4.md). The original 29 scenario texts below are preserved verbatim. Test procedures are new execution elaborations, not completed tests. All statuses are `not_run`. Shared evidence and definition-of-done rules are in [execution protocol](../execution-protocol.md); record exact fixtures/revisions/device/procedure and actual result using [task evidence](../templates/task-evidence.md).

## Current applicability

The [D-08/D-09 decisions](../decisions.md), [personal-tool amendment](../changes/2026-10-10-personal-tool-revision.md) and [directory-results amendment](../changes/2026-10-10-directory-results-revision.md) govern the procedures and gate decisions below. The 29 original acceptance sentences are retained for provenance, including their earlier threat assumptions. They are not an additional veto over the owner's current direction.

Use html_js_v1 with ordinary JavaScript/DOM/modules/frameworks/WASM/workers and browser-permitted network/CDNs. Verify the generated frame cannot read host DOM/storage/management tokens, invoke native Go bindings or perform management actions through fabricated messages; provide broken-page recovery. Local and remote access use remembered per-device bearer sessions for one owner on the same configurable listener. Existing external AI agents retain normal owner-environment permissions; managed adapters report their actual behavior and remember source/provider choices. Baseline backup is coherent private local data with checksums.

Early C03/M14 evidence at TASK-001-04 requires the pinned Wails build, primary browser and actual private HTTPS Safari iOS/iPadOS through TASK-001-03's route. Concurrent immutable versions must return different bytes at the same root-relative fetch/module/worker paths, with the required delivery-session/lifecycle proofs; missing required evidence blocks G-VIEW. Full production C03/M14 scope remains with the primary Tasks below. Initial support targets in decisions P-11 and deployment remain in scope; validate advertised functionality before claiming target support, while extra version/device combinations remain unverified. M08 does not require a zero-egress renderer or an OS jail for the owner's external agents. M17 is a bearer-session check, without an added cookie/administrator subsystem. Exact citations, explicit version-specific Review, mutation/recovery integrity and actual private second-device use remain required.

D-09 makes one selected result directory, with any number of HTML pages and supporting files, the document unit. A version snapshots that entire tree. No manifest, index.html, source metadata, region declaration, type whitelist or fixed HTML/package/file-count/depth quota is an admission requirement. Stream/chunk/range file data and stage large context automatically; unsupported preview types remain preserved and downloadable. Missing source information is unknown/unverified, never fabricated.

| Inherited scenarios | Current directory applicability |
| --- | --- |
| C01/C02 | Ordinary AI output directories/ZIPs/single HTML import without author-created inventory or transport metadata; actual corrupt bytes/paths fail atomically, and absent optional metadata does not |
| C03/C05/C07 | Multiple pages, relative assets, browser history and exact version/page/optional region anchors work; same-named regions on different pages are distinct |
| C04/M19 | Capture the exact page and full selected context; reading needs no source evidence, while claims of source verification still need actual exact evidence |
| C08 | Explicit Review applies to the complete selected directory version; opening pages does not complete it |
| M07/M11 | Owner-selected AI work budgets govern scheduled/provider work, not total directory/file/HTML size or arbitrary input/output quotas |
| M09/M15 | Preserve every selected entry across restart/export/recovery and serve requested pages/files on demand on the declared supported clients |

## C01. Skill ownership

**Original acceptance requirement:** Generate through the Haeryong-owned skill both from an Aquarium invocation and from a compatible standalone agent. Both use the same validated package contract; no Aquarium-owned duplicate generation rules are required.

**Primary work:** [TASK-006-04](../tasks/EPIC-006/TASK-006-04.md); [TASK-006-06](../tasks/EPIC-006/TASK-006-06.md)  
**Early / additional work:** [TASK-001-07](../tasks/EPIC-001/TASK-001-07.md). Early/additional contribution; full scenario still requires its primary production evidence.
**Status:** not_run

- **Given:** One fixture request and the same skill version available to Aquarium and a standalone compatible agent.
- **When:** Generate from each entrypoint, inspect searched context/source and import contracts.
- **Then:** Both use the Haeryong-owned guidance; no independent Aquarium schema is needed.

## C02. Import and persistence

**Original acceptance requirement:** Import a valid package and reopen it after restart with identical assets and provenance. Invalid or unsafe imports leave no partially visible document. Repeated submission does not duplicate the same immutable version.

**Primary work:** [TASK-002-04](../tasks/EPIC-002/TASK-002-04.md); [TASK-002-06](../tasks/EPIC-002/TASK-002-06.md)  
**Status:** not_run

- **Given:** Plain directory/ZIP/single-HTML inputs, multiple HTML pages, real JavaScript/module assets, Unicode paths, unknown attachments, absent metadata/index, above-former-limit workloads, corrupt archive fixtures and a response-loss/crash injector.
- **When:** Import/retry/restart at staging, object finalization, transaction and acknowledgement boundaries.
- **Then:** Exactly one complete immutable directory version is visible for the same operation; every selected path/type/file byte survives restart. Actual corrupt inputs have no partial records; missing metadata, file type, size or count alone never invalidates an otherwise valid input.

## C03. Interactive reading

**Original acceptance requirement:** A representative control changes a meaningful explanation state. Keyboard navigation, touch interaction, and readable layouts work in the supported Wails WebView and the declared desktop/mobile browser matrix. Local reading and annotations work without a model connection.

**Current applicability:** Ordinary author JavaScript, multiple linked HTML pages and local/module/CDN assets are supported by html_js_v1 without whole-directory bundling. TASK-001-04's required early G-VIEW runtime/device and concurrent-version proofs apply; all agreed initial support targets remain in scope before claiming their supported functionality, without requiring every additional OS/browser combination.

**Primary work:** [TASK-003-03](../tasks/EPIC-003/TASK-003-03.md); [TASK-007-03](../tasks/EPIC-007/TASK-007-03.md)  
**Early / additional work:** [TASK-001-04](../tasks/EPIC-001/TASK-001-04.md); [TASK-001-07](../tasks/EPIC-001/TASK-001-07.md). Early/additional contribution; full scenario still requires its primary production evidence.
**Status:** not_run

- **Given:** A multi-page directory with a meaningful JavaScript interaction, nested local module/framework assets, optional external/CDN dependency and a page without declared regions on the recorded available clients.
- **When:** Follow relative page links, use the file/page list and Back/Forward, operate controls with keyboard/touch, inspect page-local context and repeat with the model unavailable; separately report an unavailable external dependency.
- **Then:** The selected explanation task is useful, annotations remain host-owned and a broken page has close/reload or documented app-restart recovery; model-offline does not falsely promise all external dependencies are offline-capable.

## C04. Evidence-grounded questions

**Original acceptance requirement:** A pinned-source question returns inspectable citations and the recorded source revision. Current-source mode is explicit. Missing evidence and illustration versus observed execution are distinguishable; selected interactive state is carried safely.

**Primary work:** [TASK-006-05](../tasks/EPIC-006/TASK-006-05.md); [TASK-006-06](../tasks/EPIC-006/TASK-006-06.md)  
**Early / additional work:** [TASK-001-05](../tasks/EPIC-001/TASK-001-05.md); [TASK-001-07](../tasks/EPIC-001/TASK-001-07.md). Early/additional contribution; full scenario still requires its primary production evidence.
**Status:** not_run

- **Given:** Question with known evidence and a deliberately missing/out-of-scope citation candidate.
- **When:** Submit pinned and current modes with captured exact version/page/optional region and complete state, using context_ref for large serialized context, then validate the final answer.
- **Then:** Exact revision/locations and limitations remain visible; invented evidence is rejected or labelled unresolved. observed_input requires the linked attempt input observation and correct input_origin; posthoc_verified identifies a later exact cited-source check without asserting original provider inputs or automatic semantic correctness.

## C05. Pinned knowledge links

**Original acceptance requirement:** Navigate to the exact version and region in the same project and an allowed different project. A copied browser URL works after login and reload without a localhost or Wails dependency. A newer target version does not redirect the original reference. Forbidden targets and their metadata are not disclosed.

**Primary work:** [TASK-004-03](../tasks/EPIC-004/TASK-004-03.md); [TASK-004-05](../tasks/EPIC-004/TASK-004-05.md)  
**Early / additional work:** [TASK-001-07](../tasks/EPIC-001/TASK-001-07.md). Early/additional contribution; full scenario still requires its primary production evidence.
**Status:** not_run

- **Given:** Two projects with exact version/page/region references, duplicate region names on different pages, nested Unicode paths, query/fragment state and a newer target version.
- **When:** Copy link, log out/in, reload, follow link and test denied target.
- **Then:** Exact referenced version/page/region and raw page query/fragment open after a single encoding pass; latest is separate and unauthorized metadata is not disclosed. Ordinary HTML links need no canonical-library registration; unresolved optional targets do not block the result directory.

## C06. Backlink consistency

**Original acceptance requirement:** Forward references and backlinks reflect the same canonical edges. Repairing a derived backlink index needs no AI. AI-related suggestions stay separate from authored provenance and actual source dependencies.

**Primary work:** [TASK-004-04](../tasks/EPIC-004/TASK-004-04.md); [TASK-004-06](../tasks/EPIC-004/TASK-004-06.md)  
**Early / additional work:** [TASK-001-07](../tasks/EPIC-001/TASK-001-07.md). Early/additional contribution; full scenario still requires its primary production evidence.
**Status:** not_run

- **Given:** Known canonical edge set and a corrupted disposable reverse index.
- **When:** Compare outgoing/incoming views and rebuild reverse index deterministically.
- **Then:** Counts/edges reconcile without an LLM; authored/source/suggested meanings remain distinct.

## C07. Stable annotations

**Original acceptance requirement:** A note survives reopening. After regeneration, it remains on its original version or is explicitly reattached through a reliable mapping. Ambiguous mappings never silently attach it to another claim.

**Primary work:** [TASK-005-02](../tasks/EPIC-005/TASK-005-02.md); [TASK-007-04](../tasks/EPIC-007/TASK-007-04.md)  
**Early / additional work:** [TASK-001-07](../tasks/EPIC-001/TASK-001-07.md). Early optional harness contribution only; full production acceptance remains with the primary tasks.
**Status:** not_run

- **Given:** Notes on two v1 pages sharing region id retry, a page with no regions, and v2 with changed/ambiguous page or region structure.
- **When:** Restart, publish v2 and exercise original and explicit reattachment views.
- **Then:** Original notes remain on the exact version/page/optional region; ambiguity produces unresolved status, not automatic reassignment to another page or version.

## C08. Personal review

**Original acceptance requirement:** Review starts a personal review; Mark as reviewed records the selected version. Opening, scrolling, AI assessment, or a minor-change judgement cannot complete it. A new version is not automatically Reviewed; history and Revisit remain intact.

**Primary work:** [TASK-005-01](../tasks/EPIC-005/TASK-005-01.md); [TASK-005-06](../tasks/EPIC-005/TASK-005-06.md)  
**Early / additional work:** [TASK-001-07](../tasks/EPIC-001/TASK-001-07.md). Early/additional contribution; full scenario still requires its primary production evidence.
**Status:** not_run

- **Given:** An authenticated owner, v3 and v4, and an AI minor-change assessment plus generated-frame messages.
- **When:** Open/scroll and navigate several pages, inspect the assessment, invoke explicit Review/Mark as reviewed for the complete directory version and create v4.
- **Then:** Only the explicit owner action completes v3; AI output or frame messages do not, and v4 remains Unreviewed with history preserved.

## C09. Lifecycle and references

**Original acceptance requirement:** Archive, trash, restore, and purge have distinct effects. Incoming references show target state. Restore preserves identities. Purge removes only eligible Haeryong data and does not remove shared retained assets or original source.

**Primary work:** [TASK-005-03](../tasks/EPIC-005/TASK-005-03.md); [TASK-005-04](../tasks/EPIC-005/TASK-005-04.md)  
**Status:** not_run

- **Given:** Referenced document, notes/review history and an asset shared by another retained version.
- **When:** Archive, trash, restore and confirm purge with a concurrent reference/restore test.
- **Then:** Distinct lifecycle behavior and exact restoration hold; source/shared retained bytes are untouched.

## M01. Scoped freshness

**Original acceptance requirement:** A new unrelated commit does not produce an unsupported outdated verdict. Test a changed claim, an indirect dependency/configuration change, missing source, and a valid historical explanation. Show affected regions and exact comparison targets.

**Primary work:** [TASK-008-03](../tasks/EPIC-008/TASK-008-03.md)  
**Early / additional work:** [TASK-001-07](../tasks/EPIC-001/TASK-001-07.md); [TASK-004-01](../tasks/EPIC-004/TASK-004-01.md). Early/additional contribution; full scenario still requires its primary production evidence.
**Status:** not_run

- **Given:** Historical explanation, unrelated commit, direct changed claim and indirect config/caller change.
- **When:** Run scoped freshness assessment and repeat with missing source.
- **Then:** Results distinguish not affected, reassess, evidenced mismatch and unable to verify without global correctness claims. Each assessment exposes the exact target, source intent, version/page/region scope, eligible/assessed/complete coverage and time; regionless results use available page/version scope, and zero/unknown coverage cannot be fully aligned.

## M02. Honest scan coverage

**Original acceptance requirement:** A full run reports its captured inventory, deterministic checks, actual semantic assessments, exclusions, failures, and budget-limited items separately. Unassessed material is not labelled current merely because structural checks completed.

**Primary work:** [TASK-008-01](../tasks/EPIC-008/TASK-008-01.md); [TASK-008-06](../tasks/EPIC-008/TASK-008-06.md)  
**Status:** not_run

- **Given:** Captured inventory with eligible, excluded, failed and budget-limited items.
- **When:** Run structural and semantic stages with forced incomplete work.
- **Then:** Reported denominators and coverage match actual work; no structural-only item is labelled semantically current.

## M03. Useful enrichment

**Original acceptance requirement:** Find and explain a genuinely relevant document relationship and an existing theme. Show AI origin. A missing topic produces a content proposal rather than a misleading tag; published body and original references remain unchanged.

**Primary work:** [TASK-008-02](../tasks/EPIC-008/TASK-008-02.md); [TASK-008-04](../tasks/EPIC-008/TASK-008-04.md)  
**Status:** not_run

- **Given:** Two genuinely related documents, a misleading keyword match and a missing theme.
- **When:** Generate candidates, inspect supporting text and propose/apply allowed overlays.
- **Then:** Supported relations/topics are AI-labelled; absent content yields draft proposal instead of misleading tag.

## M04. Respect user decisions

**Original acceptance requirement:** A rejected suggestion is not repeatedly reintroduced without new evidence. User-pinned metadata and review history survive scanning. Metadata rollback does not overwrite later user edits. A scan cannot restore or newly expose trashed material.

**Primary work:** [TASK-008-05](../tasks/EPIC-008/TASK-008-05.md)  
**Status:** not_run

- **Given:** Rejected suggestion, pinned classification, Reviewed record and trashed target.
- **When:** Repeat scan with same evidence and attempt undo after a user edit.
- **Then:** Rejections/pins/review remain; undo conflicts rather than overwriting and no deleted material is revived.

## M05. Safe application

**Original acceptance requirement:** Change or delete a document while an AI assessment is running. The old result cannot be applied as a check of the replacement. Retries and duplicate results do not create repeated edges, versions, or conflicting management actions.

**Primary work:** [TASK-006-01](../tasks/EPIC-006/TASK-006-01.md); [TASK-008-05](../tasks/EPIC-008/TASK-008-05.md)  
**Early / additional work:** [TASK-002-02](../tasks/EPIC-002/TASK-002-02.md); [TASK-002-04](../tasks/EPIC-002/TASK-002-04.md); [TASK-002-06](../tasks/EPIC-002/TASK-002-06.md). Early/additional contribution; full scenario still requires its primary production evidence.
**Status:** not_run

- **Given:** In-flight assessment with captured version, targets and operation identity.
- **When:** Update/trash/reclassify target, then deliver old and duplicate results.
- **Then:** Current preconditions prevent stale or repeated application; historical assessment is labelled appropriately.

## M06. Durable work

**Original acceptance requirement:** Quit the Wails desktop process or close another client, restart the independent daemon, cancel work, and simulate unknown provider outcomes. Saved progress is reconciled, available results are reused, and no blind duplicate external work or metadata application is claimed as safe recovery.

**Primary work:** [TASK-006-06](../tasks/EPIC-006/TASK-006-06.md); [TASK-009-06](../tasks/EPIC-009/TASK-009-06.md)  
**Early / additional work:** [TASK-002-02](../tasks/EPIC-002/TASK-002-02.md); [TASK-002-06](../tasks/EPIC-002/TASK-002-06.md); [TASK-005-05](../tasks/EPIC-005/TASK-005-05.md); [TASK-006-01](../tasks/EPIC-006/TASK-006-01.md). Early/additional contribution; full scenario still requires its primary production evidence.
**Status:** not_run

- **Given:** Durable job at dispatch/result/application boundaries.
- **When:** Close clients, kill/restart daemon, cancel and simulate unknown provider outcome.
- **Then:** Checkpoint/attempt reconciliation is inspectable; no blind second call or duplicate mutation is presumed safe.

## M07. Schedule and allowance

**Original acceptance requirement:** Test coalescing of missed/overlapping runs, configured scope and limits, and expired login or exhausted allowance. Local functions remain usable, unfinished AI work stays visible, and no unapproved billable or provider fallback occurs.

**Primary work:** [TASK-006-03](../tasks/EPIC-006/TASK-006-03.md); [TASK-009-03](../tasks/EPIC-009/TASK-009-03.md)  
**Status:** not_run

- **Given:** Configured budget and schedules plus controllable auth/quota failures.
- **When:** Advance fake clock, overlap requests, exhaust attempts and expire login.
- **Then:** Coverage gaps and paused work are honest; local reader survives and no unauthorized billing fallback occurs.

## M08. Isolation

**Original acceptance requirement:** Malicious HTML and source instructions cannot access host credentials, write source, read outside scope, or issue privileged API operations. Untrusted pages cannot mark a document Reviewed, delete it, or gain access through fabricated link/state messages.

**Current applicability:** For this personal-tool baseline, isolation means separation of the generated frame from host DOM/storage/management tokens, native Go bindings and privileged host messages. Browser-permitted networking and ordinary scripts are allowed. Existing external agents use their normal owner environment; a managed adapter must state its actual permissions and remembered scope rather than claim universal containment.

**Primary work:** [TASK-003-03](../tasks/EPIC-003/TASK-003-03.md); [TASK-006-02](../tasks/EPIC-006/TASK-006-02.md); [TASK-007-06](../tasks/EPIC-007/TASK-007-06.md)

**Early / additional work:** [TASK-001-04](../tasks/EPIC-001/TASK-001-04.md); [TASK-001-05](../tasks/EPIC-001/TASK-001-05.md). Bounded contribution only; Stage A completion is owned by the primary tasks above.
**Release regression:** [TASK-010-03](../tasks/EPIC-010/TASK-010-03.md). Repeat the already completed Stage A procedure on the selected release installation; this is not a prerequisite for EPIC-007.
**Status:** not_run

- **Given:** Useful html_js_v1 pages, focused host DOM/token/bridge/message probes and an actual managed-source fixture using remembered owner choices.
- **When:** Run a real script/module interaction, attempt host access or fabricated Review/delete messages and exercise a broken-page recovery path; inspect the adapter's exact source reads and permission record.
- **Then:** Useful JavaScript works, generated pages have no host management authority and personal state remains correct. The adapter reports its actual source/provider permissions and exact evidence without imposing an unimplemented OS or network guarantee.

## M09. Restore and migration

**Original acceptance requirement:** Restore database and referenced content together, including notes, review records, rejection decisions, and lifecycle state. Test interrupted imports and migration failure. Development and release profiles cannot mutate one another's library.

**Primary work:** [TASK-005-05](../tasks/EPIC-005/TASK-005-05.md); [TASK-010-02](../tasks/EPIC-010/TASK-010-02.md)  
**Early / additional work:** [TASK-002-02](../tasks/EPIC-002/TASK-002-02.md); [TASK-002-06](../tasks/EPIC-002/TASK-002-06.md). Early/additional contribution; full scenario still requires its primary production evidence.
**Status:** not_run

- **Given:** Multi-page directory versions with all original files plus notes/review/rejections/lifecycle and active backup.
- **When:** Interrupt imports/migration, back up with concurrent changes and restore isolated profile.
- **Then:** DB/content agree; every selected directory entry and exact page anchor survives, original records remain intact and release/dev profiles remain isolated.

## M10. Headless core and desktop lifecycle

**Original acceptance requirement:** Build and run the Go CLI/daemon without Wails GUI dependencies. Connect the desktop client to the existing profile, quit or crash that client, and verify the daemon and accepted jobs remain alive. Reconnect without a second writer or library.

**Primary work:** [TASK-002-01](../tasks/EPIC-002/TASK-002-01.md); [TASK-003-05](../tasks/EPIC-003/TASK-003-05.md)  
**Early / additional work:** [TASK-001-02](../tasks/EPIC-001/TASK-001-02.md); [TASK-007-06](../tasks/EPIC-007/TASK-007-06.md). Early/additional contribution; full scenario still requires its primary production evidence.
**Status:** not_run

- **Given:** Independent service and matching desktop build.
- **When:** Build/run headless without GUI, attach, close/crash desktop, reconnect.
- **Then:** Daemon and accepted jobs survive with one library/writer; no hidden Wails dependency in core.

## M11. Configurable operations

**Original acceptance requirement:** Change schedule, automatic-action category, and retention settings without rebuilding. Persist them across restart, expose the effective values through UI and CLI, reject invalid configuration, and support manual-only and keep-indefinitely modes.

**Primary work:** [TASK-009-01](../tasks/EPIC-009/TASK-009-01.md)  
**Status:** not_run

- **Given:** Effective configuration revision and implemented policy controls.
- **When:** Change valid/invalid schedule, action and retention settings via different clients, then restart.
- **Then:** Values persist consistently; invalid changes keep prior state and manual/indefinite modes behave distinctly.

## M12. Mid-run policy change

**Original acceptance requirement:** Restrict automatic application or revoke access while a scan is running. Further affected calls or mutations stop or require approval. Expanding policy does not silently expand an existing job. Retain the captured policy revision and report the difference.

**Primary work:** [TASK-009-04](../tasks/EPIC-009/TASK-009-04.md)  
**Status:** not_run

- **Given:** Running scan with captured policy and allowed source.
- **When:** Revoke source/narrow auto-apply, then later broaden policy.
- **Then:** Restrictions stop affected new work; expansions do not retroactively enlarge captured scope.

## M13. Retention and restore safety

**Original acceptance requirement:** Setting a duration alone does not enable purge. Confirm an impact preview when enabling or shortening destructive retention. Restore an item during a purge window and retain shared assets correctly. Restoring a backup does not silently reactivate destructive or externally billable work.

**Primary work:** [TASK-005-04](../tasks/EPIC-005/TASK-005-04.md); [TASK-009-05](../tasks/EPIC-009/TASK-009-05.md)  
**Status:** not_run

- **Given:** Eligible trash, shared asset, running backup and concurrent restore.
- **When:** Set duration without enablement, preview/enable purge, then race restore/retention and restore an old backup.
- **Then:** No implicit purge, shared loss or revived destructive/externally billable activity occurs.

## M14. Wails bridge and compatibility

**Original acceptance requirement:** On the pinned Wails/OS/WebView matrix, verify that untrusted explanation code cannot invoke native bindings or privileged daemon endpoints. Test client/API mismatch, process restart, navigation, events, and packaging. A framework status claim is not a substitute for these tests.

**Current applicability:** Run focused host/native-boundary and lifecycle checks on the pinned Wails/OS/WebView build. TASK-001-04 must also pass its actual private HTTPS Safari iOS/iPadOS and concurrent-version delivery proofs before G-VIEW closes; early contribution does not defer missing required evidence to Stage A. Additional combinations outside the declared support targets remain explicitly unverified.

**Primary work:** [TASK-003-05](../tasks/EPIC-003/TASK-003-05.md); [TASK-007-06](../tasks/EPIC-007/TASK-007-06.md)

**Early / additional work:** [TASK-001-04](../tasks/EPIC-001/TASK-001-04.md). Bounded contribution only; Stage A completion is owned by the primary tasks above.
**Release regression:** [TASK-010-03](../tasks/EPIC-010/TASK-010-03.md). Repeat the already completed Stage A procedure on the selected release installation; this is not a prerequisite for EPIC-007.
**Status:** not_run

- **Given:** The selected Wails/toolchain build and a useful JavaScript/module fixture with focused native-bridge probes.
- **When:** Try a generated-frame native call or fabricated host message, test client/API mismatch and restart/quit, then recover a broken page.
- **Then:** The actual build is recorded, JavaScript works and generated content cannot use native or privileged daemon authority; untested platforms remain explicitly unverified.

## M15. Browser and headless delivery

**Original acceptance requirement:** Run the Go daemon and compiled UI without Wails or a graphical session. Read the same library from a local browser and an authenticated second device. Test desktop/mobile core workflow parity, provider-unavailable behavior, and authenticated assets without a development server.

**Primary work:** [TASK-007-05](../tasks/EPIC-007/TASK-007-05.md); [TASK-007-06](../tasks/EPIC-007/TASK-007-06.md)  
**Early / additional work:** [TASK-001-07](../tasks/EPIC-001/TASK-001-07.md). Early optional harness contribution only; full production acceptance remains with the primary tasks.
**Status:** not_run

- **Given:** Headless service with compiled assets, a multi-page directory exceeding the former package limits, paired desktop/phone browsers and unavailable provider case.
- **When:** Open requested pages without fetching every asset and perform core actions with Wails absent; compare exact version/page/source/review identities across devices.
- **Then:** Same library works with no development server, native install or fake provider requirement for basic reading.

## M16. Port and exposure configuration

**Original acceptance requirement:** Start on configured port 9270, change it explicitly, and test an occupied port, an unrelated service, and an invalid bind/base URL. No random fallback, second writer, or public wildcard binding occurs. Validate private proxy ingress and local recovery after an endpoint change.

**Primary work:** [TASK-007-01](../tasks/EPIC-007/TASK-007-01.md)  
**Status:** not_run

- **Given:** Free proposed port, occupied port, unrelated service and invalid origin/bind configurations.
- **When:** Start, collide, explicitly change listener and validate private ingress/local recovery.
- **Then:** No silent random port, second writer, wildcard exposure or untrusted base-URL construction.

## M17. Remote authentication

**Original acceptance requirement:** Unauthenticated, expired, revoked, and unauthorized requests cannot retrieve private data, source excerpts, assets, or events, nor mutate review or lifecycle state. Test CSRF, hostile origins, forged proxy identity, unsafe return URLs, and hostile generated HTML in a normal browser.

**Current applicability:** One-owner per-device bearer tokens are remembered in host-origin localStorage by default until revoked. Optional expiry or memory-only mode remains explicit. Local and private remote access share the application listener; no cookie/CSRF subsystem, separate remote port or administrator-grant tier is required.

**Primary work:** [TASK-007-02](../tasks/EPIC-007/TASK-007-02.md)  
**Early / additional work:** [TASK-001-03](../tasks/EPIC-001/TASK-001-03.md); [TASK-002-03](../tasks/EPIC-002/TASK-002-03.md). Early/additional contribution; full scenario still requires its primary production evidence.
**Status:** not_run

- **Given:** Unpaired, remembered, explicitly expiring and revoked devices on the chosen private route, plus a generated frame without the host token.
- **When:** Pair through local bootstrap/trusted-device approval, reopen after reload, read private data/events, revoke a device and attempt tokenless requests or unsafe login returns.
- **Then:** Remembered owner access is convenient; invalid/revoked tokens and generated frames cannot use private management authority. Network/proxy identity does not replace the bearer.

## M18. Multi-device consistency

**Original acceptance requirement:** Change a note or review state while another client has stale data. Persist the intended document version and detect conflicting edits. Reconnect after mobile suspension without a duplicate mutation or AI request; accepted daemon work continues after both clients close.

**Primary work:** [TASK-007-04](../tasks/EPIC-007/TASK-007-04.md)  
**Status:** not_run

- **Given:** Two devices editing the same note/review with stale revisions and a pending AI job.
- **When:** Save conflicting edits, suspend phone and lose an acknowledgement before reconnect.
- **Then:** Stale writes do not overwrite silently; pending operations reconcile without duplicate external execution.

## M19. Remote source boundaries

**Original acceptance requirement:** Serve a retained explanation while its original laptop repository is unavailable. Reading works, but current-source Q&A and freshness checks expose missing evidence rather than claiming access. An authorized host-side checkout/snapshot is reported with its actual revision.

**Primary work:** [TASK-007-05](../tasks/EPIC-007/TASK-007-05.md)  
**Early / additional work:** [TASK-001-05](../tasks/EPIC-001/TASK-001-05.md); [TASK-004-01](../tasks/EPIC-004/TASK-004-01.md). Early/additional contribution; full scenario still requires its primary production evidence.
**Status:** not_run

- **Given:** A retained multi-page explanation with original laptop repo disconnected and optional approved host snapshot, plus an ordinary imported directory with no source metadata.
- **When:** Read package, ask current-source question, then ask supported pinned snapshot question.
- **Then:** Every saved page remains readable, including output with unknown/unverified source metadata; actual source availability and revision are reported. Missing requested pinned/current evidence yields unable_to_verify, not a successful source check based only on the explanation. A permitted later cited-source verification is labelled posthoc_verified and cannot retroactively claim that original provider inputs were observed.

## M20. Remote session and restore safety

**Original acceptance requirement:** Revoke a device/session during event delivery and stop further protected delivery. Restore a backup without reactivating revoked sessions or unapproved remote listeners. An offline daemon produces an explicit disconnected/unsaved state, not a false saved result.

**Primary work:** [TASK-005-05](../tasks/EPIC-005/TASK-005-05.md); [TASK-007-02](../tasks/EPIC-007/TASK-007-02.md); [TASK-007-06](../tasks/EPIC-007/TASK-007-06.md)

**Early / additional work:** See task metadata. Bounded contribution only; Stage A completion is owned by the primary tasks above.
**Release regression:** [TASK-010-02](../tasks/EPIC-010/TASK-010-02.md). Repeat the already completed Stage A procedure on the selected release installation; this is not a prerequisite for EPIC-007.
**Status:** not_run

- **Given:** Active event session and a backup containing older session/config state.
- **When:** Revoke during delivery, restore backup and disconnect a pending browser mutation.
- **Then:** No protected delivery, restored token reuse, silent exposure restart or false Saved state occurs. After restore the owner bootstraps fresh device access and reviews affected operating preferences through the normal owner flow.


## Stage and quality closure

EPIC-001 closes only after G-STACK, G-AUTH, G-VIEW, G-AI, G-VALUE and G-CONTRACT pass. TASK-001-07 evaluates real generated explanations on existing spike harnesses; it does not replace later full persistence, source integration or remote implementation.

The EPIC-007 Stage A gate completes C01–C09, M08, M10, M14 and M15–M20 under the current applicability above using the listed primary Stage A owners, with no prerequisite in EPIC-010. Run on the actual selected environment, including supported real subscription AI and private second-device interaction. Use [knowledge quality](knowledge-quality.md) for the real claim/evidence/reader corpus. EPIC-008 adds evaluated relation/freshness maintenance and EPIC-009 scheduled/retention behavior. EPIC-010 repeats applicable inherited procedures and supplemental cases as release regression; the preserved older wording does not reintroduce superseded security or platform gates.

Structural resolution is distinct from semantic support. Missing evidence, partial coverage, historical source intent and uncertain provider outcomes remain explicit. A failed or not-run required current check blocks its owning gate; sample averages cannot hide data loss or unsupported source claims. Untested extra platforms and the expressly superseded security assumptions are outside this baseline's required checks.
