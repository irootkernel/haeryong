# Screen behavior specifications

**All screens below are proposed.** Route/task mapping is in [information architecture](information-architecture.md). Every implementation must include loading, empty, failure, permission, disconnected and relevant conflict states, not just the happy path.

The [R5 interactive concept](../design/prototype/README.md) is the current visual reference for these screens. Its [UI handoff](../design/prototype/HANDOFF.md) records baseline inputs and the sample states reviewed; the screen contracts and owning Task checks remain the implementation requirements.

## S01 Connect and device approval

Show configured endpoint, connection security status, profile hint only if safely available, and Connect. Request pairing, show comparison code and time remaining, explain approval from the local CLI or another approved device, then wait. Do not display private library metadata before approval. Pending/expired/rejected states have distinct copy. An approved owner or local bootstrap can reject the exact request. Stop waiting ends local polling and discards its pending secret; it does not claim server cancellation or revoke an existing session. Approval returns to the exact internal version/page/query/fragment/region route. Untrusted device label never substitutes for comparison verification. An existing owner device can approve the new connection. Remember this device is on by default, with a temporary-session option. A remembered device reuses its session across reload, new tabs and ordinary browser restart. Show this connect screen only when a valid stored credential is absent, explicitly revoked, expired under an optional policy, or invalidated by restore. If browser storage is unavailable, explain the temporary session once.

Disconnected and session-expired differ: unreachable server offers Retry connection; expired/revoked session offers Reconnect device. In Wails, a local not-running service may offer controlled Start local service; the web client cannot. Switching endpoint requires explicit library confirmation rather than reusing credentials on an arbitrary URL.

## S02 Home

Show Continue reviewing, Updated since your review, Revisit and Needs attention as prioritized lists, with modest counts. A count includes only eligible visible material. Each item shows project, title, exact explanation version, My review and a distinct freshness notice. A background maintenance summary links to its report rather than presenting all documents as current.

Empty state: explain creating/importing the first result directory and offer the shared [import controls](#import-export-and-file-operations) with folder/ZIP/single-HTML selection. Show transfer/enumeration progress, cancellation and resumable state; no maximum HTML size or number-of-files warning is part of the normal import flow. Provider unavailable must not hide existing documents. Do not recommend an unread document simply because it has an arbitrary large graph degree.

## S03 Library and project view

Top search bar, project scope selector, personal review filter, lifecycle filter, freshness filter and topics. Default Active; Archive is explicit and Trash is separate. List columns: title/project, version, My review, freshness, updated time. On narrow screens rows become stacked list items, not an unreadable shrunken table.

Each library row is one directory result, with a Files/Pages action and file count when enumeration is known. Search results identify the matching page path, optional region and match context without leaking trash or unauthorized excerpts. Filter state survives reader/back navigation. Sorting must remain deterministic under pagination. Empty results offer clearing filters; empty library offers import. Multi-select destructive actions are deferred initially to reduce accidental bulk deletion. Project source settings show approved identities, not a server file browser.

TASK-004-02 M5 delivers the static-search portion of S03. A labelled query form
supports Enter and Clear; project/lifecycle/version/kind filters survive URL,
reload and Back, including explicit Search All (`lifecycle=all`). No submitted
query retains the ordinary Library list; an empty submitted query asks for a term
and shows safe coverage. Loading, empty eligible corpus, complete zero match,
pending/partial/failed coverage, matched items, more-page reset, transient errors
and access loss are distinct. Safe `index.reasons` labels include distinct-version
counts for owner and scoped responses without job reads or diagnostics. Failed
coverage offers no maintenance action; manual rebuild remains TASK-004-04.

Search rows show exact project/lifecycle/version/path and optional region/source,
plain-text snippets and static/unsupported limitations. Open matching page uses
the returned document/version and exactly once-encoded page path; it never follows
latest or focuses a region. Region and anchor state are data only. Attachment
hits open their exact version for the existing file-list/download controls.
First/more responses belong to a request and confirmed authority epoch; replaced
filters, Back, remount and access loss cannot repopulate old rows. Double More is
single flight. A genuine cursor binding change visibly reloads page one. Partial
coverage uses bounded authenticated GET polling and focus/explicit refresh;
coalesced content-free event hints refetch the first page under current authority;
search never schedules index work. Phone controls stack with usable touch targets.

## S04 Reader

Header: breadcrumb/project, document title, exact version selector, source release/short hash with expandable full identity, and Copy link. Offer Import new version using the shared import controls with this exact document preselected and its base captured before upload. Keep My review and freshness separate. Actions: Start review, Mark as reviewed, Reopen review or Clear review according to the exact state; document-level Revisit toggle; More menu for Archive/Trash. An explicit Mark as reviewed may go directly from Unreviewed to Reviewed. Reopen/Clear changes the current state while retaining the audited history. Merely opening never starts or completes review.

Main area: explanation canvas supporting ordinary JavaScript, chart libraries, modules, simulations and external resources. **Files / Pages** opens the result's full directory tree and page list; an optional **Outline** covers the current page. Show the current relative path and allow direct opening of any HTML. On mobile, use a full-height picker that returns to the page without losing its position. Paginate/virtualize long file lists and fetch the selected page/dependencies on demand. Context tabs: **Evidence, Notes, Ask, Links**. The selected page path and optional region remain visible in the context header. Personal/library actions and authoritative status appear in the host chrome. Generated content runs in its own page and cannot grant itself management authority. Do not put a security approval in the ordinary reading flow. Local HTML links preserve the same directory version and normal relative/root-relative/query/fragment behavior. Back/Forward and copied/new-tab links restore the exact page context. Authored library links open pinned target pages/regions with a return path; Open latest is a separate action.

An explicit page URL opens that exact page, and Continue resumes the last-opened pointer. Opening a result without a page uses the owner's valid preferred default for this version, then a supplied valid immutable default, root index.html, the sole HTML, or the Files/Pages listing. Offer Set as preferred default and Clear preference in the host Files/Pages controls; they change a separate owner record and never rewrite imported metadata, republish a version or change Continue's pointer. Neither index.html nor a special manifest is required. Render HTML and supported media previews directly; retain and offer download for other files. Names and extensions are not an import allowlist. A missing asset/page is reported at its exact path without rejecting or hiding other pages.

Review applies to this whole directory version. Notes, questions, outline selection and saved position include the exact page_path; two pages may use the same region ID. Open/navigation updates the version's last-opened page/query/fragment pointer. Scrolling saves only that page's position with its own revision; it cannot overwrite a sibling page or silently move the last-opened pointer. Preferred default, version pointer and each page position remain separate records. Visiting one or every page never automatically starts or completes Review.

Evidence tab lists source ID/revision/path/symbol/line range, retained-snapshot information, current authorized availability and observation/inference label using the [source evidence contract](../contracts/source-access.md). Present immutable author evidence separately from the service's later verification/history. Check source evidence runs the exact authorized check after TASK-004-01 ships; before then the action is capability-disabled. Show captured, matched, mismatch or unavailable honestly: locating bytes alone does not establish author-byte agreement, original provider input or semantic support. Freshness shows source intent, exact target, captured_at, version/page/region scope, assessment time and inspectable per-unit coverage. Expand partial, blocked and not-checked units and their reasons through units_url; aggregate badges derive from those records. Incomplete or zero/unknown coverage never becomes an all-aligned badge. A plain directory without source metadata shows Source basis unknown/unverified and remains fully readable; file integrity is not source verification. A missing source has explanatory state and does not automatically switch to current. Notes tab supports explicit Save/Cancel, unsaved indicator, original version/page/region anchor and clear reattachment. Ask tab shows the remembered pinned/current source mode (initial default pinned), permits an explicit change and shows selected page/region/display scenario, provider availability/disclosure scope and Submit. Existing approved source/provider choices are reused without a per-question setup prompt. Display saved question/answer history with citations, captured targets and partial/error states; do not resend automatically on reconnection. Reopening on another authorized device restores that history through the read contract. A current-source answer identifies the actual captured revision and excerpt even when it differs from the explanation's immutable original evidence. Large selected JSON context transfers automatically and exposes preparation progress; it is not silently truncated to a fixed field/string/byte budget. Provider context or usage capacity, when actually encountered, is reported with the selected coverage and never presented as a successful full-input answer.

Links tab separates Authored references, Backlinks and AI-related suggestions. TASK-004-04 implements Evidence (default) and Links in the existing context Sheet; Notes is added by TASK-005-02; Ask retains its later owner. Links shows current visible edge/distinct-document counts, exact version/page/region backlink scope, explicit pagination/refresh, loading/empty/error states and a non-clickable minimal tombstone. Suggestions is an honest empty/unavailable section; /related remains FEATURE_NOT_READY. Arrow/Home/End operate the tabs, Escape returns Sheet focus, and controls retain touch-sized targets. A suggested link has rationale, origin and decision; it is not labelled Used by unless source evidence establishes that relation. Deleted targets show a nondisclosing tombstone, not silent removal or replacement.

## S05 Versions and changes

List immutable versions with creation source basis, personal review and new-version notice. Offer Import new version with this document preselected, showing its captured base before transfer; reuse the shared import control and keep conflict resolution explicit. Default comparison is last personally Reviewed versus selected/latest; user can choose exact versions. Display added/removed/changed file paths, document metadata differences and page-specific region changes; an AI summary is labelled interpretation, not a proof of semantic equivalence. Never transfer Reviewed for an inferred minor change. Initial implementation may progressively merge the two immutable path-ordered file inventories and show side-by-side exact file/text/region lists. Display results while fetching remaining cursors; do not require both whole directories in memory or claim the diff is complete before enumeration ends. Paginated diffs remain complete, with explicit unindexed or unavailable content.

Cross-version notes remain at their original version and page path. A reliable mapping can be offered with explicit reattach action. Missing anchors show unresolved; selecting a similarly named region does not silently move the note.

TASK-004-05 delivers S05's exact selectors and structural comparison. TASK-005-01
adds capability-gated personal Review: each version's actual state/time is read by its
exact version ID. Explicit pair choices win; otherwise use latest as target and the
last still-personally-Reviewed version as source, falling back to its predecessor or
the single version twice. Freeze the pair; a refreshed latest notice remains a
separate choice. Missing/false Review capability retains an honest unavailable state.
File batches, metadata/page-region changes,
on-demand side-by-side text blocks, original downloads and exact page links expose
unfinished or unavailable scope until enumeration ends. A missing old page stays
missing. Import new version mounts the shared locked document control with its captured
pre-transfer base. Copy link, Versions entry, pair selectors and Open latest support
keyboard and narrow touch; returning restores the selected Reader context and opener
focus after the actual frame/region readiness. The existing Evidence/Links Sheet
keyboard contracts continue unchanged. Source observation, semantic assessment and
personal Review are separate sections; supplied assessment proofs are labelled synthetic.

## S06 Maintenance and findings

Overview has Manual scan, active jobs and historical runs. Scan dialog selects allowed projects, depth, scope and budget; it displays what automatic actions are enabled. Capture is explicit. Progress separates inventory enumeration, structural checks, semantic assessments and proposals. A partially assessed run never shows a single misleading 100% current score.

Report tabs: Coverage, Freshness, Relationships, Topics, Drafts, Activity. Each finding shows origin, exact version/page/region/target, evidence, rationale, limitations and current applicability. Actions: Approve/reject proposal, open source/affected region, create draft, or undo an applied overlay when still compatible. Rejected suggestions remain dismissed until materially new evidence. Stale finding shows why it cannot be applied, with Request reassessment rather than Force apply. Create draft records an explicit draft-generation request tied to the finding revision and authorized scope; reassessment starts a scoped scan tied to that finding, and neither action implicitly approves or publishes.

Show source-change observation separately from semantic freshness and include the observed target and coverage. An unchanged cited file does not establish whole-project alignment. Highlight affected Reviewed versions without asserting that the owner believes something false. A new-version draft opens review/publish flow; Auto apply does not mean Auto publish. Reject a preparing/ready draft as an explicit current-revision action and fence further linked job application. Replace files only for a ready draft using a sealed replacement upload; keep its captured base and revision history, then validate the replacement before separate publication. A stale draft preserves the owner's unsaved intent and requires a current-revision decision.

## S07 Settings

Current TASK-006-03 keeps /settings and /settings/general on the existing General
placeholder, with explicit AI navigation to /settings/ai and the existing
/connect owner tools. The AI screen also links to General and Connect. General
does not mount or probe provider status. These navigation controls do not deliver
the future General/Sessions editors or change owner approval/revoke/logout.

Sections: General/appearance, Connection, Sessions, Projects/source scope, AI provider, Maintenance, Retention, Storage/uploads, Backup status, About/compatibility. Editable operating policy reads the same daemon revision in web and Wails. Provider credentials and native bootstrap secrets are never shown. UI displays unsupported settings as unavailable until implemented.

Backup status shows the last complete checksum-verified set and its time/age, the latest attempt separately, and the last actual trial-restore result/time. No complete set is Not yet created; no trial restore is Not run. A failed latest attempt does not erase the prior usable set. These observations are informational and do not impose a new scheduler or backup gate.

Storage/uploads uses the [summary/detail contract](../contracts/import-workflow.md#upload-list-and-detail-shapes) to list retained uploads even after this browser loses its upload ID. Rows show the summary's selection name, purpose/mode, accepted and staging bytes, created/last-activity times, authorized target, state, reusability, unavailable reason and pin summary. Fetch detail_url when expanding an item or preparing Resume to obtain error, checkpoint and range/entry links. A missing list error field never means that verification succeeded. Staging bytes are not guaranteed reclaimable; inspect current pin/adoption references before cleanup. Offer explicit Abandon and link actual operation/job outcomes. Age alone never expires staging or deletes adopted data.

Sessions lists remembered devices and offers Forget this device or Revoke another device. Default remember behavior and optional expiry are visible; no separate administrator-role or elevation screen is required. Project/provider settings are remembered, so routine Q&A and generation do not repeat source/provider approval.

Maintenance exposes manual/interval/calendar, timezone, scope, depth and actual enforceable limits. Per-action matrix offers disabled/propose/auto only where allowed. Retention separates duration from Enable automatic purge. Its impact preview shows the last complete verified backup time or Not yet created as information, without requiring a backup or delay before the owner can proceed. Apply opens an impact preview with affected items/jobs and effective timing. Invalid settings leave prior configuration active. A concurrent settings change causes conflict resolution, not last-writer-wins. An owner session can edit supported source/connection settings with the same explicit impact confirmation. OS file selection, installation and external network enrollment remain local host actions; a browser does not become a generic server shell.

## S08 Archive, Trash and purge

Archive remains searchable via explicit filter. Trash lists only owner-eligible deleted documents with deletion time and expiry eligibility. Lifecycle applies to the document and its versions. Restore preserves IDs and returns the document to its recorded active or archived state before trash. Purged documents cannot be restored in place. Permanent purge displays affected versions, notes, answers, review history, distinct referring documents and edge count separately, alongside the last complete verified backup time or Not yet created; warn about retained quotes/backups/provider records. Require an unexpired plan at the current revision and one explicit owner confirmation. Do not require a second administrative or consequential credential. No automatic default button that purges when pressing Enter.

A restore/purge race returns a clear changed-state notice. Shared assets still referenced elsewhere must not be removed. An AI scan cannot move an item out of Trash or silently recreate a replacement.

## S09 Sources

Show logical project/repository identity, approved root availability without exposing unnecessary absolute paths to remote clients, pinned/available revisions and retention/disclosure policy. An unavailable laptop checkout does not hide already retained explanations. A recorded digest alone is not an available snapshot. Ask/current-source controls describe the missing evidence. The owner selects a source root through the local picker or an explicit supported path-setting action; the service remembers it. A paired owner may edit supported mappings, provider/disclosure choices and the default pinned/current question mode through project-scoped configuration plan/commit. Existing settings are remembered; a broader source or disclosure choice shows its concrete impact before confirmation. The browser does not gain an arbitrary server-filesystem browsing API.

## Import, export and file operations

Import begins with New document (default) or Existing document, new version. The latter uses a searchable document selector; Reader and Versions preselect their exact document. Display and preserve document_id, base_version_id and the pending operation intent before upload starts. Keep that target/base and operation ID for a retry or reconnect; a stale base prompts an explicit new-version decision instead of silently retargeting or creating a replacement document. Paths, names and matching content hashes never select the target automatically. Use the [central binding rules](../contracts/package.md#remembered-association-and-operation-base).

Record the actual picker/transfer route and the entries it represents. A FileList-based folder picker does not include empty directories, so its result promises fidelity to selected represented entries and states that limitation. Whole-tree fidelity uses an entry-aware route, Wails native chooser, CLI or ZIP only when it actually includes directory entries; do not claim that every mobile browser lacks folder selection. Preserve represented empty directories and record destination limitations.

Accept the selected directory as authored. Do not silently remove hidden files, filter unfamiliar extensions, force bundling or rewrite links/HTML. The service generates its inventory and IDs. Optional Haeryong metadata can supply titles, a default page, evidence or authored references; unresolved optional targets appear as unresolved rather than making ordinary page content unreadable.

The shared Download original action offers the complete selected original directory through a supported destination handoff or a streamed ZIP, preserving bytes and paths. Optional service metadata remains separate. Selected personal/relationship records and separately retained sources belong to the host-local CLI portability export in EPIC-005; the UI identifies that path and its instructions without claiming a remote portable-export API. Existing files at a chosen destination are handled with a concrete conflict action; no silent overwrite or path renaming. If the destination cannot represent a stored path/link, retain the source result and report the affected entries. Transfer cancellation, disk-full and interruption retain the actual resumable staging/job or client-download failure state; a download need not invent a daemon job. Closing a delivery session or suspending a mobile browser does not abandon its upload. No partial operation is labelled successful.

## Page errors and external resources

If an explanation script fails, a dependency is unavailable, the device cannot render a page or a page stops responding, show a concise error and offer Reload page, Close page or reopen in the supported viewer. Keep the library's saved state separate from page execution. Do not promise that every browser provides process isolation or that a synchronous infinite loop can always be stopped from the same event loop. Document app-restart recovery where page controls cannot respond. Keep the saved directory available for download or opening on another supported device; do not impose a smaller import ceiling for every device. External network content may be live; label it as such when it affects a factual claim. Ordinary network access needs no Haeryong-specific permission ceremony.

## Shared error model

401 clears protected rendered content and prompts reauthentication. 412 preserves a still-authorized unsaved edit for comparison and offers reload or a deliberate merge. A response lost after commit first reconciles the same operation ID; replay does not become a false stale-write error. 409 VERSION_CONFLICT asks for an explicit new-version base decision; OPERATION_CONFLICT reports changed request identity; uncertain outcomes link to receipt/job reconciliation without automatic resend. A transport's chunk-size rejection triggers automatic smaller-chunk retry/resume; it is not a total artifact size rejection. Actual storage/transfer/render failure reports its cause and affected operation without dropping source material. Provider quota/auth failure keeps reading/review usable. Daemon disconnection marks pending edits Not saved and disables consequential actions until server-confirmed state is retrieved. Region/context selection itself stays local and does not create paid work.


TASK-005-02 adds the capability-gated Notes tab and independent Revisit toggle.
Notes shows exact version/page/optional region, original anchor, selected-text/context
fallback, explicit Save/Cancel, unsaved draft, paginated history and unresolved anchors.
A newer-version view may inspect an origin version without silently moving its notes.
Reattach requires explicit confirmation of the selected same-document destination;
unproved regions are rejected and an explicit page/version fallback remains available.
A 412 comparison keeps local text and the newer server revision separate; reload or a
deliberately merged new Save is required. A changed server anchor is shown alongside
the prior location; an explicit merge adopts it and excludes stale context/selection
while retaining local note text. Saved context reads work after page disposal.
Pending/unknown requests retain their original identities and cannot be cancelled as
though a committed effect were known not to exist. Revisit populates Home's independent
cursor-backed list. Home omits raw project/version UUID display when a readable cue is
unavailable; Review history uses Korean labels and localized time. Continue and Versions
retain the TASK-005-01 behavior.

TASK-005-03 implements the Reader More menu's Archive/Unarchive and Trash controls,
plus the owner Trash list and Restore. One host impact dialog shows the exact plan,
expiry, preserved records and current authorized incoming counts with full pagination.
Reader positions are labelled as-of-plan information. Cancel/Escape and default Enter
never commit a destructive action; deliberate confirmation button activation does.
Expired/conflicting plans retain intent and offer a fresh impact review. Unknown results
retain the original plan until reconciliation or an explicit same-plan retry. Trash
closes the current renderer; restore opens a new session only on explicit navigation.
Retention eligibility is informational; permanent purge is not enabled by this Task.

### S08 delivered manual purge

TASK-005-04 enables owner purge in Trash behind features.purge. The safe-close-first
impact sheet lists version/note/history/referring-document/edge counts, real
backup status and retained-copy limits; one explicit confirmation accepts terminal
purge. Enter on initial focus never deletes. All impact pages remain reviewable.
Restore conflicts/stale plans preserve intent and require fresh impact confirmation.
The Trash view retains job identity across reload and shows automatic I/O retry,
protected-byte waits and metadata-removal progress without claiming physical erasure.

The owner may dismiss a completed or failed purge result after reading it. An
unavailable job is shown separately with its own dismissal action; one lookup
failure does not hide or stop polling other jobs. Pending and paused work remains
remembered and visible. A transient lookup error retains its record and retries.
Dismissing a local result record does not cancel or repeat the server operation.

### S08 delivered local backup visibility (TASK-005-05)

Settings → Backup shows real manual-mode status, complete identity/time/age, failed or
incomplete latest attempt separately and actual trial status. Refresh is keyboard/touch
accessible; reload reads current service state. The shared browser/native screen explains
host-local CLI create/verify/export/restore/review, unsupported encryption and same-volume
limits. Existing original-directory download is unchanged. Purge shows the same real
status and still requires no fresh backup. No browser remote export action is invented.

## Sources settings implementation extension (TASK-006-02)

The existing owner editor includes pinned/current default source mode and an
optional default Provider ID. The server-normalized preview shows both defaults
and source/root/retention/disclosure effects. A non-null provider choice remains
an explicit confirmed owner change, independently of each source's disclosure
allow list. Reload and fresh native App processes restore saved choices; clearing
the provider submits null. The ordinary source list continues showing logical
identities rather than host roots. The editor states that the managed collector
is not an OS jail and that production provider execution remains unavailable
until its implementing owner. No generic filesystem browser or new per-read
confirmation is introduced.

The settings preview distinguishes source/disclosure expansion from changing
the default provider and names both effects when combined. It shows the proposed
source mode and, when the provider changes, old→new provider defaults with
explicit null. Source narrowing remains visible even when a provider change also needs confirmation. Failed initial loading shows
an error and Retry rather than invented editable values. Keyboard and
emulated-touch browser evidence is separate from the native harness's
trusted-host DOM interactions.

## Shared Ask implementation (TASK-006-05)

Reader Ask separates preparing the current location from submitting the captured
question. It displays the saved exact anchor/context, provider, pinned/current
mode and selected sources, including remembered disclosure/retention and honest
unverified availability. Source defaults refresh for the next new question;
changes do not rewrite an existing draft. Provider settings links use the normal
host route and do not probe or launch a paid request automatically.

Recovery rows and active job controls show their captured scope and original
identity. Stop, receipt recovery and explicit resume acknowledgement retain that
identity; selecting another record cannot attach an old job's actions to a new
request. Saved history displays complete text/context, exact targets and final/
partial/error state, with observation, posthoc verification, current availability
and limitations visibly separated. Invalidated or denied server views refresh
or close independently of the unsent/uncertain local draft. The shared responsive
layout keeps controls and long immutable identifiers readable; actual keyboard,
mobile and native evidence remains an execution obligation, not a design claim.

## Sessions and security policy implementation (TASK-007-02)

`/connect` hosts the pending/approved-unredeemed pairing queue
(`GET /api/v1/auth/pairings`), exact-request comparison-code `approve`/`reject`
form, and a connected device list (`GET /api/v1/sessions`) showing each device's
`device_label`, current-device marker (`(이 장치)`), `state`, peer revocation
(`DELETE /api/v1/sessions/{id}`), current-device logout (`DELETE /api/v1/session`)
and navigation to `/settings/sessions` (without creation, last-activity or
derived-expiry timestamps on `/connect` device rows). Selecting a pairing row
populates its request ID; approve and reject require the exact comparison code,
origin, item ETag (`If-Match`) and an `Idempotency-Key`. A stale or concurrent
`409`/`412` decision refreshes the queue and device list and reports the current
active row state and revision (such as `approved` but unredeemed) or, if the row
is absent from the active queue, reports that it is no longer active (ambiguously
consumed, rejected or expired without inventing which terminal state occurred).
`/connect` and `/settings/sessions` display transport security context (local
loopback vs private HTTPS vs native IPC) and credential storage behavior
(`localStorage` vs memory-only vs native credential store). Neither screen
automatically dispatches active touching reads on background SSE stream
open/close/invalidation; refreshes after initial load use explicit owner actions
so open management screens cannot reset `last_seen_at`.

`/settings/sessions` links to `/connect` for pairing approvals and displays the
current session summary alongside the full device session inventory
(`GET /api/v1/sessions`) with `device_label`, `(이 장치)`, `session_id`, `origin`,
`state`, `created_at`, `last_seen_at`, `expires_at`, current-device logout
(`DELETE /api/v1/session`) and peer device revocation
(`DELETE /api/v1/sessions/{id}`). It edits `security` configuration
(`remember_device_default`, `session_idle_seconds`, `session_absolute_seconds`)
through explicit plan preview and confirmation
(`POST /api/v1/configuration/plans` and `/plans/{id}/commit`). Positive integer
bounds are validated before plan creation, and a fresh authorized
`GET /api/v1/sessions` read is required before presenting a confirmable preview:
an inventory-read failure surfaces the error and preserves the user's draft (and
any already-reconciled plan) for safe retry without falling back to stale cached
devices, leaving an undispatched plan as `UNKNOWN`, or sending a duplicate plan
`POST`. The preview shows revision, current vs proposed values, estimated
affected sessions and immediate self-expiry warnings without claiming hard push
revocation. A stale plan or revision (`412`/`409`) refreshes the server revision
while preserving the user's proposed draft inputs for explicit re-preview. If a
committed policy immediately expires the submitting session and returns `401`
(`SESSION_EXPIRED` or `SESSION_REVOKED`), encounters a retryable `503
SERVICE_RECOVERING` or network error after dispatch, or if receipt lookup returns
`404` or fails, the original `operation_id` remains `UNKNOWN` until an authorized
same-owner session reconciles the receipt via `GET /api/v1/operations/{id}`. See
[private device session evidence](../testing/private-device-sessions.md).
