# Information architecture and routes

The [schema registry](../contracts/schema-registry.md) is the canonical route and shared-state authority. This document maps those routes to screen intent; [HTTP API](../contracts/http-api.md) defines resource reads and actions.

## Navigation

Primary navigation: **Home, Library, Projects, Maintenance**. Secondary navigation: **Archive, Trash, Settings**. A persistent service indicator shows profile/library identity and local/remote connection. Do not put raw provider credentials, server paths or an all-project source browser into navigation.

Home answers what to continue, what changed since personal review, and what needs attention. Library is the complete filtered content view, not an automatic AI-generated taxonomy. Projects applies explicit project scope; Topics are filters and management overlays rather than a competing root hierarchy. Graph visualization remains deferred.

| Screen | Route | Primary intent | Owning tasks |
| --- | --- | --- | --- |
| S01 Connect | `/connect` | Pair/reconnect the current device, show safe connection state | TASK-002-03, TASK-003-01, TASK-007-02 |
| S02 Home | `/` | Continue reviewing, inspect updates and important findings | TASK-003-02, TASK-005-01 |
| S03 Library / Projects | `/library`, `/projects`, `/projects/{id}` | Search/filter and open exact content | TASK-003-02, TASK-004-02 |
| S04 Reader | `/documents/{id}/versions/{vid}?page={page_path}` with optional page_query/page_fragment and region | Navigate a directory version, inspect the selected page, source and personal state | TASK-003-03, TASK-003-04, TASK-004-01, TASK-004-04, TASK-005-01, TASK-005-02, TASK-006-05 |
| S05 Versions | `/documents/{id}/versions` | Select pinned/latest versions and inspect changes | TASK-004-05, TASK-005-01 |
| S06 Maintenance | `/maintenance`, `/maintenance/jobs/{id}` | Request scan, inspect coverage/findings, approve bounded changes | TASK-006-01, TASK-008-01, TASK-008-04, TASK-008-05, TASK-008-06, TASK-009-06 |
| S07 Settings | `/settings/{section}` | Configure policy, sessions and connection status; inspect uploads/storage | TASK-002-05, TASK-003-02, TASK-007-02, TASK-009-01 |
| S08 Trash | `/trash` | Inspect impact, restore or deliberately purge | TASK-005-03, TASK-005-04 |
| S09 Project sources | `/projects/{id}/sources` | View approved source identity/availability and disclosure scope | TASK-004-01, TASK-006-02 |

Native service install/start/stop and OS file selection remain local operations. A paired owner can manage supported settings and project mappings through the shared UI without a separate admin grant. There is no generic remote shell or server file browser. A disconnected browser cannot start an unreachable daemon.

## Desktop/browser feature parity

Both clients share search, reading, source/version display, region navigation, references/backlinks, annotations, personal review, Revisit, Q&A, lifecycle confirmations, maintenance results and authorized policy editing as those features ship. Local OS dialogs/tray are native-only. Browser artifact uploads accept a user-selected directory, ZIP or files using the supported picker/transfer route. Record the actual picker capability on each browser/device. A FileList folder picker represents files and their relative paths, not empty directories; disclose selected-entry fidelity and offer an entry-aware/native/CLI or ZIP route that actually represents empty directory entries when whole-tree fidelity is needed. A picker without folder selection offers ZIP/files or CLI import without a product file-count limit. This is independent of source mappings and never means browsing the server filesystem.

## URL and history behavior

Use stable document/version IDs and exact relative UTF-8 page_path, not titles. Copying a link preserves the version, page, optional page query/fragment and page-local region across login/reload. The canonical parameters are page, page_query and page_fragment; each carries one percent-encoded raw value, and the trusted-shell #region fragment identifies optional Haeryong selection. Artifact query/fragment are not interpreted as host commands. A copied browser URL uses the configured reachable transport base; it is not the logical library identity. When only a local URL exists, label the copied link as local to this host rather than implying another device can open localhost.

A direct external link first loads a content-free app shell. The shell preserves the internal route, page/query/fragment and region selection, loads the remembered bearer from its own origin storage, and checks the session before fetching content. Both local and remote browsers reuse that credential across reload and new tabs; missing, revoked or optionally expired credentials start pairing. Cookie presence is not part of this flow. Haeryong never adds its owner/agent bearer or captured source excerpts to a URL. Preserve the artifact's own query/fragment as navigation context. Local HTML links, redirects, query changes and browser Back/Forward preserve the exact directory version. Browser history restores the prior page and its position, then prior library filters on leaving the result. Opening a page never silently changes to latest. Return routes must be internal/validated. Native deep links supplement ordinary browser URLs, not replace them.

An explicit page/deep link always opens that exact target; Continue separately uses the version's last-opened pointer. A result opened without a page uses the valid owner reader preference, then its immutable supplied default page, root index.html, the sole HTML, and otherwise the Files/Pages listing. Owner preference, version last-opened pointer and each page's saved position use separate records and individual validators; editing one never republishes content or overwrites another. A directory with no HTML remains a browsable/downloadable result. Preserve all nested paths; a selected attachment uses a browser-supported preview or download. Local root-relative URLs resolve within this version's content root. Ordinary external URLs retain their browser semantics.

Reader bootstrap and subsequent resource responses provide saved questions/answers/citations, per-version review and page-specific reading positions, document-level Revisit/lifecycle, assessment history and visible link/topic decisions. Settings and Sources use status/summary resources for pending pairings, remembered devices, configured sources, provider availability and backup state. A screen need not own a separate endpoint for every widget, but its data and actions must map to a documented contract.

## Action-to-contract ownership

The following map identifies concrete contracts and first UI owners. It does not claim that later capabilities are already implemented; disabled states follow capabilities until their owning task ships. Use the HTTP catalog and schema registry for complete bodies, preconditions and error semantics.

| UI action | Contract / behavior | Owning UI task |
| --- | --- | --- |
| Request/poll/approve/reject pairing | POST /auth/pairings, /auth/pairings/{id}/poll, /approve and /reject. Stop waiting clears local pending UI/polling secret; the server request expires unless the owner rejects it | TASK-002-03 shared auth; TASK-007-02 complete device UX |
| List/revoke devices | GET /sessions; DELETE /session or /sessions/{id} | TASK-007-02 |
| Import folder/ZIP/HTML or new version | New document is default; Existing document, new version selects a document and captures base_version_id before upload. Reader/Versions reuse this action with the exact document preselected. POST /uploads, GET /uploads/{id}, entry chunks, complete, then POST /imports use the [binding rules](../contracts/package.md#remembered-association-and-operation-base); retry preserves target/base and operation identity | TASK-003-02 shared control; Reader TASK-003-03 and Versions TASK-004-05 mount it; native chooser TASK-003-05; private/mobile regression TASK-007-03 |
| Find/resume/abandon retained upload | Owner GET /uploads and GET /uploads/{id} expose actual bytes, activity, target, state and reusability in Settings/Storage; resume existing staging or POST /uploads/{id}/abandon. Reconcile accepted operation/job results before retrying | TASK-002-05 service discovery; TASK-003-02 UI |
| Check imported source evidence | POST /versions/{id}/source-evidence/{evidence_id}/verifications checks the exact authorized source; GET the same history and existing source-evidence view display the immutable author record plus service_verification | TASK-003-04 presentation and disabled action; TASK-004-01 source check and enabled action |
| Download original directory/files | GET /versions/{id}/package or /file?path=..., authenticated stream to the supported destination; personal-record portability is the labelled host-local CLI export path | TASK-003-02 / TASK-003-05; CLI instructions TASK-005-05 |
| Open page / Continue | Exact page URL wins. Continue reads the version last-opened pointer; page navigation writes GET/PUT /versions/{id}/reading-position with page_path, page_query, page_fragment and opened | TASK-003-03 |
| Choose preferred default page | GET/PUT /versions/{id}/reader-preference, default_page path or null, using that record's individual precondition. Does not republish or move the last-opened pointer | TASK-003-03 |
| Save/restore this page's position | GET/PUT /versions/{id}/page-position?page={path}, page_query/page_fragment/position with the page record's own ETag. Never reuse the version pointer or another page's validator | TASK-003-03; multi-device conflict TASK-007-04 |
| Review / Revisit / notes | Existing individual review, revisit and annotation GET/mutation routes; explicit owner actions and per-record ETags | TASK-005-01 / TASK-005-02 |
| Ask / reopen saved answer | POST /versions/{id}/questions with the remembered/default or explicit source mode; GET version questions / GET /questions/{id} only reads saved history | TASK-006-05 |
| Compare exact versions | Progressively merge two path-ordered GET /versions/{id}/files cursors plus metadata/file reads; show complete versus unfinished/unavailable diff scope | TASK-004-05 |
| Approve/reject finding / undo overlay | POST /findings/{id}/decisions with its If-Match; POST /overlays/{id}/undo with current overlay validator | TASK-008-05 |
| Create draft from finding | POST /generations with finding_id, expected_finding_revision, intent=draft and existing authorized generation scope. Never implicitly approves or publishes the finding | TASK-008-06 |
| Request reassessment | POST /scans with finding_id and expected_finding_revision plus captured allowed scope/depth/target. A stale finding is not force-applied | TASK-008-05 |
| Reject / replace / publish draft | PATCH /drafts/{id} with If-Match: action=reject for preparing/ready; action=replace plus sealed upload_id and optional metadata_ref for ready. Publish uses the separate POST /drafts/{id}/publish | TASK-008-06 |
| Edit source/provider/default question mode | GET /configuration and project-scoped POST /configuration/plans (scope.kind=project, project_id), then existing plan commit; source_mode_default is pinned/current, initially pinned. Use remembered authorized choices, not a remote filesystem browser | TASK-004-01 source settings; TASK-006-02 provider/source integration |
| Operating policy / retention | Existing configuration read/plan/commit with current revision and explicit impact confirmation | TASK-009-01 |
| Archive/trash/restore/purge | Document lifecycle plan then exact plan commit, with current revisions and explicit owner confirmation | TASK-005-03 / TASK-005-04 |

## Progressive capability disclosure

Before later Epics ship, hide unavailable feature actions or show a clearly disabled explanation. Never display a working-looking Ask, Schedule or Approve button backed only by a fake success. A capabilities response controls availability; public demo fixture screens are explicitly labelled development-only and contain no private data.
