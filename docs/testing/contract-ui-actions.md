# UI action and contract ownership

This Task06 map covers enabled controls in the disposable auth/viewer/value
experiments and all19 proposed product actions. The experiments use their actual
fixture handlers, not production `/api/v1`. Current means enabled in an authorized
fixture mode after connection, not currently running: owned services/tabs were
settled. Source selectors below are exact; automatic checker-only commands are
not presented as product UI. R5 remains unapproved.

## Current disposable controls

| Surface and controls | Actual read/mutation contract | Existing proof and limit |
| --- | --- | --- |
| auth `#connect`, `#label`, `#remember`, `#expiry`, submit button | POST `/pair` with label, then POST `/pair/poll` and `/pair/redeem`; local remember/optional expiry policy. GET `/session` when a stored connection exists. | [Task03 auth record](auth-ingress-spike.md), complete local/private Chrome pairing/remembered access tests. Pending secret is local and dropped by reload; no product stop-waiting control is implemented. |
| auth `#request-id`, `#code`, `#approve` approved/rejected submit buttons | Authorized POST `/pair/decision` with exact ID/code/decision. | Task03 exact comparison, owner authorization and single terminal decision/concurrency checks; no production pairing endpoint claim. |
| auth `#read` | Authorized GET `/private/data`. | Task03 actual remembered/private read and denial after revocation. |
| auth `#logout` and dynamic `#devices button[data-device-id]` | Authorized POST `/devices/revoke`, self clears stored bearer; other-device result refreshes GET `/devices`. | Task03 self/cross-client revocation and retry/error presentation; no automatic success after failed response. |
| auth `#refresh-devices`, `#retry-connection` | GET `/devices`; retry GET `/session` then device refresh, retaining remembered credentials during transient failure. | Task03 actual browser checks and error/recovery paths; retry shown only when applicable. |
| auth `#check-reload` | Local device-check journal plus actual document reload; restored GET `/session`. | Task03 actual reload checks; a reload alone is not a second physical device. |
| viewer `#run` | Executes disposable checks through authorized `/viewer/config`, deliver/page/renew/context/report/observation handlers and page channel; deliberately mutates only fixture state. | [Task04 viewer record](viewer-spike.md), all required constructions composed with retained original failures. This is a test runner button, hidden in knowledge mode. |
| viewer/knowledge `#reload` | Close old selected grant, POST `/viewer/deliver`, GET `/viewer/page`, fresh page channel; preserve exact selected path/query/fragment. | Task04 actual reload, stale binding rejection and independent sibling observations. Knowledge uses the same unchanged recovery bytes. |
| viewer/knowledge `#close` | POST `/viewer/close`, dispose channel, timers, ephemeral source/context state. | Task04 actual close and host boundary; Task07 current source-pane clearing. No delete/version/personal-state mutation. |
| viewer/knowledge `#reopen` | POST `/viewer/deliver`, GET `/viewer/page`, exact selection/channel remount. | Task04 actual reopen and sibling independence; old delivery authority not reusable. |
| viewer `#logical-tab` | Ordinary exact `/reader?version=...&page=...&page_query=...&page_fragment=...` route; native supported popup branch. | Task04 actual logical tabs, native boundary, Aside limits retained. Knowledge route still mounts the configured exact sample; logical link is fixture routing, not production Continue. |
| viewer `#download` | Browser POST `/viewer/download-issue`, then credentialed GET `/viewer/download?id=...`; native registered binding701006 streams file. | Task04 actual Chrome/native destination proof; Aside destination/UI limit retained, separate incremental stream proof distinguished. Hidden in knowledge mode; no production package export. |
| viewer `#confirm-restart` | Explicit device confirmation writes fixture lifecycle report, then mobile storage proof. | Android-only conditional control, not enabled on the actual Aside construction and **not physically verified**. Later platform owners retain this obligation. |
| viewer corpus scenario/detail `#budget`, `#simulate` | Local authored JS/worker scenario prediction, selected ordinary data reads and page-state message. | Task04 ordinary JS/module/worker/DOM behavior and channel checks; parameter is a scenario work count, not a product content cap or provider budget. |
| viewer corpus `#nested`, `#root`, `#popup`, nested detail back link | Relative/root/popup exact authored page navigation plus host location announcement. | Task04 exact path/query/fragment navigation and distinct unprivileged popup; known whole Back-after-reload failures stay failed. |
| viewer stuck page inline button | Deliberate `while(true){}` authored script for hang behavior. | Task04 fallback/recovery limit; no guarantee against arbitrary JS resource exhaustion. |
| knowledge `#open-a`, `#open-b` | POST `/viewer/deliver` and GET `/viewer/page` for configured immutable sample/version. | [Task07 value record](knowledge-value-spike.md), actual paired R1–R4 tasks, default/accepted states and 29 control observations. Names a/b are fixture slots, not canonical service IDs. |
| Mulgae inventory `#inventory-source`, `#inventory-role`, `#inventory-proof`, `#inventory-cleanup` | Local illustrative prediction and display state, then unchanged page-state channel. | Task07 actual original cleanup mismatch, one repair, paired reader/current controls; no Mulgae execution or evidence verification caused by changing a select. |
| Mulgae compose `#compose-source`, `#compose-mapping`, `#compose-publication` | Local source/mapping/publication illustration and page-state channel. | Task07 R1/R3 current observations and exact pinned compose reference; does not adopt/publish a real artifact. |
| Gaori outcomes `#evidence-branch`, `#child-exit` | Local outcome illustration/page-state channel; child exit disabled for standalone extractor branch. | Task07 original standalone gap, one repair, actual required branches and paired readers; not a real command/parser run. |
| Gaori lifecycle `#lifecycle-observation`, `#lifecycle-session` | Local lifecycle illustration/page-state channel. | Task07 current paired controls; no process/session action performed by select. |
| All knowledge page/contents/claim/region anchors | Ordinary exact authored navigation/fragment location; host page channel observes accepted location. | Task07 all four readers and exact page/reference tasks. Anchors never create personal Reviewed status. |
| Knowledge `#source-*` anchors and source pane | Authorized GET `/viewer/knowledge?version=...&kind=evidence&id=...`; client validates exact captured text digest, current binding/generation and displays identity/text. | Task0712 actual pane texts matched original captures; actual known200/unknown404 clear, then injected delayed actual200 plus remount rejects stale completion. Actual/injected scope remains distinct. This reads prebound captures, not a new source check. |
| Knowledge `#reference-*` anchors | GET same knowledge handler with `kind=reference`, exact pinned prior version/page/region mount, POST `/viewer/report` receipt. | Task07 exact current Mulgae compose/M11 pointer. Qualified analogy/explanation only, no new-source dependency assertion. |

Auth controls are in `spikes/auth-ingress/ui.html`; viewer controls in
`spikes/viewer/host.html`/`host.js` and `corpus/pages`; knowledge authored controls
in `spikes/knowledge-value/samples/{mulgae,gaori}`. Ordinary payload links/styles
remain authored bytes, with no extension allowlist or compulsory index/manifest.
Page context-transfer messages use actual temporary POST/PUT/PATCH/DELETE
`/viewer/context?id=...`, byte digest and exact render binding; they are not a
saved Ask action. The actual selected DOM Range native followup used visible M11
text, while host `selected_text` was empty. No production saved question,
context_ref adoption, reading-position/preference, personal Review or durable
annotation UI is enabled by these fixtures.

## Future product actions

Every row remains provisional and capability-disabled until its first named
implementing owner supplies actual behavior. These route meanings are delegated
to [HTTP API](../contracts/http-api.md), [import workflow](../contracts/import-workflow.md),
[registry](../contracts/schema-registry.md) and [UI information architecture](../ui/information-architecture.md).
The fixture map above does not activate these production routes.

| Proposed action | Concrete contract | First UI/implementation owner |
| --- | --- | --- |
| Request/poll/approve/reject pairing | POST /auth/pairings, /auth/pairings/{id}/poll, /approve and /reject. Stop waiting clears local pending UI/polling secret; the server request expires unless the owner rejects it | TASK-002-03 shared auth; TASK-007-02 complete device UX |
| List/revoke devices | GET /sessions; DELETE /session or /sessions/{id} | TASK-007-02 |
| Import folder/ZIP/HTML or new version | New document is default; Existing document, new version selects a document and captures base_version_id before upload. Reader/Versions reuse this action with the exact document preselected. POST /uploads, GET /uploads/{id}, entry chunks, complete, then POST /imports use the binding rules in package.md; retry preserves target/base and operation identity | TASK-003-02 shared control; Reader TASK-003-03 and Versions TASK-004-05 mount it; native chooser TASK-003-05; private/mobile regression TASK-007-03 |
| Find/resume/abandon retained upload | Owner GET /uploads and GET /uploads/{id} expose actual bytes, activity, target, state and reusability in Settings/Storage; resume existing staging or POST /uploads/{id}/abandon. Reconcile accepted operation/job results before retrying | TASK-002-05 service discovery; TASK-003-02 UI |
| Check imported source evidence | POST /versions/{id}/source-evidence/{evidence_id}/verifications checks the exact authorized source; GET the same history and existing source-evidence view display the immutable author record plus service_verification | TASK-003-04 presentation and disabled action; TASK-004-01 source check and enabled action |
| Download original directory/files | GET /versions/{id}/package or /file?path=..., authenticated stream to the supported destination; personal-record portability is the labelled host-local CLI export path | TASK-003-02 / TASK-003-05; CLI instructions TASK-005-05 |
| Open page / Continue | Exact page URL wins. Continue reads the version last-opened pointer; page navigation writes GET/PUT /versions/{id}/reading-position with page_path, page_query, page_fragment and opened | TASK-003-03 |
| Choose preferred default page | GET/PUT /versions/{id}/reader-preference, default_page path or null, using that record's individual precondition. Does not republish or move the last-opened pointer | TASK-003-03 |
| Save/restore this page's position | GET/PUT /versions/{id}/page-position?page={path}, page_query/page_fragment/position with the page record's own ETag. Never reuse the version pointer or another page's validator | TASK-003-03; multi-device conflict TASK-007-04 |
| Review / Revisit / notes | Existing individual review, revisit and annotation GET/mutation routes; explicit owner actions and per-record ETags | TASK-005-01 / TASK-005-02 |
| Ask / reopen saved answer | POST /versions/{id}/questions with the remembered/default or explicit source mode; GET version questions / GET /questions/{id} only reads saved history | TASK-006-05 |
| Compare exact versions | Progressively merge two path-ordered GET /versions/{id}/files cursors plus metadata/file reads; show complete versus unfinished/unavailable diff scope | TASK-004-05 |
| Approve/reject finding / undo overlay | Owner POST /findings/{id}/decisions with individual If-Match; POST /overlays/{id}/undo with current effect validator; Topic PUT pins/rejects with all linked revisions. Current undone Topic PUT is refused. Recover the same key through GET receipt; no automatic resend/ETag replacement | TASK-008-05; [scoped evidence](finding-decisions.md) |
| Create draft from finding | POST /generations with finding_id, expected_finding_revision, intent=draft and existing authorized generation scope. Never implicitly approves or publishes the finding | TASK-008-06 |
| Request reassessment | Owner-only POST /scans with paired finding_id/expected_finding_revision and explicitly confirmed same-document version/page/region, depth/source selection; keep exact origin under coalescing. New proposals require their own decision; original history remains terminal | TASK-008-05 |
| Reject / replace / publish draft | PATCH /drafts/{id} with If-Match: action=reject for preparing/ready; action=replace plus sealed upload_id and optional metadata_ref for ready. Publish uses the separate POST /drafts/{id}/publish | TASK-008-06 |
| Edit source/provider/default question mode | GET /configuration and project-scoped POST /configuration/plans (scope.kind=project, project_id), then existing plan commit; source_mode_default is pinned/current, initially pinned. Use remembered authorized choices, not a remote filesystem browser | TASK-004-01 source settings; TASK-006-02 provider/source integration |
| Operating policy / retention | Existing configuration read/plan/commit with current revision and explicit impact confirmation | TASK-009-01 |
| Archive/trash/restore/purge | Document lifecycle plan then exact plan commit, with current revisions and explicit owner confirmation | TASK-005-03 / TASK-005-04 |

Unknown or unavailable production actions return `FEATURE_NOT_READY` rather than fixture success. Android Chrome, Safari, real StageA second-device and personal-state/multi-device regressions remain their named Tasks and release owners. None is silently waived by the initial Aside-only owner amendment.
