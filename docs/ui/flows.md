# Product interaction flows

The [R5 interactive concept](../design/prototype/README.md) and its [UI handoff](../design/prototype/HANDOFF.md) provide current UI baseline inputs for walking these flows. The handoff distinguishes sample interactions and reviewed states from the production behavior specified below.

## Connect once and resume

A new browser shows a comparison code and Remember this device, selected by default. An approved owner can reject that exact request; Stop waiting only ends local polling and discards the pending secret, leaving server expiry/rejection semantics explicit. Approve the exact request from local bootstrap or an existing owner device. Subsequent reloads, tabs and browser restarts reuse the stored credential and open the original version/page/query/fragment/region after a session check. A revoked, optionally expired or restore-invalidated session reconnects explicitly. Temporary mode omits persistence. Routine reading, JavaScript execution and already configured source/provider use do not prompt again.

## Import and open a result directory

Choose New document (default) or Existing document, new version, then select the AI's output folder, ZIP or single HTML. Reader/Versions preselect the exact document; the shared [import controls](screens.md#import-export-and-file-operations) capture and display its base before upload and retain both target/base for the same retry. A name/path/hash never silently binds a document. The service enumerates represented selected entries, transfers/hash-verifies bytes incrementally and persists resumable staging. No author manifest, manual IDs/digests, region declarations or source metadata are required. The UI shows actual files/bytes and an unknown total while enumeration is incomplete, then one complete published directory version. Cancellation or I/O failure never publishes a partial result. A FileList picker cannot report empty directories; state selected-entry fidelity and offer an entry-aware/native/CLI or ZIP route with directory entries for whole-tree preservation. Lost upload IDs are recoverable from Settings/Storage's owner upload list, with actual progress, target, state and explicit Resume/Abandon.

An explicit page link opens that target; Continue resumes the separate last-opened pointer. Otherwise use a valid owner preferred default, immutable supplied default, root index.html or the sole HTML; if none applies show Files/Pages. Choose any page, follow ordinary local links and use Back/Forward inside that same version. Large folders use paginated navigation and on-demand file loading. Unsupported preview types remain downloadable. Download original streams the selected original paths/bytes through the shared browser/Wails action. Selected personal records and separately retained sources use the labelled host-local CLI portability export; the ordinary download is not a full backup.

## Read, verify and review

```mermaid
flowchart TD
  A[Home or Library] --> B[Open exact version and page]
  B --> C[Read and navigate result pages]
  C --> D{Need context?}
  D -->|Evidence| E[Open approved source at recorded revision]
  D -->|Question| F[Use pinned/current mode and submit]
  D -->|Related explanation| G[Open exact target page and region]
  E --> C
  F --> H[Saved answer with citations and limitations]
  H --> C
  G --> C
  C --> I[Explicit Mark as reviewed]
  I --> J[Persist personal record for this version]
```

Local page navigation stays in the selected directory version. An authored link to another result opens its exact target version/page and preserves the return path. Page open, cross-page navigation and diagram clicks never imply review completion. Explicit Mark as reviewed can complete an Unreviewed or Reviewing version; Reopen/Clear updates current personal state and retains history. Navigating to a new version leaves the historical record intact and the new version Unreviewed. Revisit applies independently to the document. A missing source or unknown source basis is visible and does not block reading the saved explanation. From Evidence, Check source evidence performs the later authorized exact-source verification once TASK-004-01 is available; original evidence remains immutable and check outcome/history is a separate overlay. This action does not require AI or make verification an import condition. Notes/questions capture the selected page, optional region and actual scenario. Large context transfers automatically without author field/byte caps; an owner Ask/Save action remains explicit.

## Pinned links and exact version comparison

Reader Copy link includes its selected page, opaque artifact query/fragment and optional
page-local region under the approved current base. A local-only link is labelled. A
credential loss automatically captures that logical route for Connect; verification
returns once per entry, including a later reconnect in the same running app. Reload,
Back/Forward and S05 round trips preserve exact selection without moving personal state.
Versions offers exact manual pairs, progressive structural differences and Import new
version with this document/base captured before upload. Later cursor failures retain
visible earlier results and retry the same cursor; incomplete scope cannot say complete.
Open latest is separate and resolves once when activated. TASK-005-01 adds explicit
whole-directory Review and its history. With `features.review`, an omitted comparison
source defaults to the last still-Reviewed version; explicit selected IDs keep priority.
Each version displays its own Review read, never the latest document summary as an
older version's state. Source observation and supplied semantic assessment coverage
cannot stand in for personal completion. A 412 retains the attempted action while the
owner reads current state and explicitly chooses again; unknown outcomes retain the
original operation and precondition.

## Maintenance proposal lifecycle

```mermaid
flowchart TD
  A[Manual or configured trigger] --> B[Capture scope and policy]
  B --> C[Deterministic integrity checks]
  C --> D[Bounded semantic assessment]
  D --> E[Findings with evidence and coverage]
  E --> F{Current policy and preconditions}
  F -->|Allowed overlay| G[Apply audited reversible metadata]
  F -->|Owner decision needed| H[Propose for approval]
  F -->|Stale or denied| I[Blocked or stale finding]
  H --> J{Owner decision}
  J -->|Reject| K[Record rejection fingerprint]
  J -->|Approve overlay| G
  J -->|Create content| L[Generate unpublished draft]
  L --> M[Explicit review and publication]
```

A finding cannot directly modify original HTML, mark Reviewed, expand source access or delete outdated documents. Create draft dispatches the explicit finding-bound generation request; Request reassessment starts the finding-bound scoped scan. Draft rejection fences linked application, and replacing a ready draft's files creates a new draft revision with its captured base/history preserved. Neither replacement nor overlay approval publishes it; publication remains a separate current-revision action.

## Reader preferences and position

Set as preferred default edits the owner preference for the exact directory version. It does not change the immutable package default or last-opened pointer. Open/navigation records the version pointer; scrolling records only that page's query/fragment/position with its own ETag. Reopening a different page reads that page's record. Two pages with independent position changes do not conflict through a shared scroll field; concurrent edits to the same page record still use normal revision conflict handling.

## Multi-device conflict

Device A reads note revision 3. Device B updates it to revision 4. A's Save with If-Match 3 receives 412 and current revision metadata. The local unsaved text remains visible for comparison; the UI offers reload or deliberate merged edit against revision 4. It does not silently overwrite B or invent a successful save. If an acknowledgement was lost, reconnect authenticates and reconciles the existing operation ID first. The service returns an authorized stored outcome before reevaluating that operation's original If-Match, so a successful save is not misreported as a conflict. A genuinely new merged edit uses the current revision and a new operation ID.

If the other editor reattached the note, conflict comparison shows the old and
current server locations and keeps the old selection/context inspectable. Preparing
the merged edit explicitly adopts the server anchor and excludes the old context
and selection, preserving local note text. The next Save uses the new validator
and sends no context bound to the previous page/version; it does not reattach the
note back. A same-location merge may retain its original local context.

## Destructive lifecycle

Request an impact plan, inspect eligible content and references, confirm the exact plan once as the owner, and commit only after the service rechecks lifecycle and revision. There is no separate administrative grant or elevation step. The UI updates after acknowledgement or receipt reconciliation. Lifecycle is document-level: restore returns a trashed document to its prior active or archived state, while purge is terminal for that identity. Restore and purge compete through the same preconditions. Auto purge uses explicitly confirmed policy and execution-time eligibility, not AI classification. Active source reads and backup/export jobs also protect their captured objects from garbage collection.

## Cross-device and restored-session refresh

Events invalidate visible review, lifecycle, Revisit, overlays, findings, drafts and assessment state by typed resource/revision. When a resource becomes hidden or access is revoked, clear its existing renderer/cache without transmitting the now-hidden body. A stream epoch mismatch after supported restore, an expired cursor or a cursor ahead of the current stream triggers authorized state resynchronization. Library identity stays stable, but old credentials and event cursors do not become valid again.

## UI review checklist

At each feature gate, exercise the delivered subset below and name the later owner of unavailable production features; repeat the full set at the applicable integrated gate. Before approving implementation, walk the wide/tablet/phone Reader with a multi-HTML directory, nested/Unicode paths, relative assets and local Back/Forward navigation, a large file listing, an attachment with download fallback, an export round-trip, a real JavaScript interaction, remembered reload/new-tab/restart, one source-unavailable example, one stale note conflict, a revoked session, an outdated-but-Reviewed document, a referenced trashed document, a partial semantic scan, and a retention change that does not enable purge. Confirm that every warning says what is known and what action is available without requiring a graph or CLI for ordinary reading.


The TASK-005-02 Notes comparison offers a server reload or preparation of the retained
local draft against the current note ETag, followed by explicit Save with a new key.
It never automatically merges a note after 412. Unknown saves first reconcile the
original operation; a runtime-generation change never causes automatic new dispatch.
Drafts are scoped to owner/library/exact version and retain their captured anchor across
page/tab changes. A real browser storage failure leaves the text visible with an error.
Reattachment and deletion each use explicit owner confirmation and their own note CAS.

The shared Notes confirmation is a trusted-host HTML modal. New/Edit replacement,
server reload, reattachment, deletion and draft discard retain the captured action,
draft revision, destination and validator across the wait. Close, default Enter
and Escape are safe; only the named confirmation admits the action. Route, owner,
runtime, panel/tab or modal-mode changes invalidate unconfirmed consent without
clearing drafts or unresolved requests. Confirmed local replacement becomes visible
after its guarded journal write; newer text and other-window changes refuse it.
An admitted request retains its original identity when the screen later closes.

TASK-005-03's concrete lifecycle flow is More → request impact → inspect all pages →
explicit plan confirmation → acknowledged/reconciled result. Cancel/Escape does not
commit; current document/impact changes require refreshed review. Automatic position
saves do not stale the open dialog. Unknown commits preserve the original plan and
captured runtime generation; reconnect only reconciles. Trash lists deletion time and
prior lifecycle; Restore returns to active/archived without moving personal anchors.

TASK-005-05 adds Settings → Backup → Refresh for real backup and independent trial
status. The shared browser/native UI directs host-local portability operations to CLI;
normal version download remains available. Offline restore follows stop → plan → explicit
apply → new bootstrap → exact configuration review → normal restart. A pending hold
cannot be bypassed by an ordinary config commit, and old uncertain work is not retried.


### Saved context reading implementation boundary

The current Notes context attempt keeps its exact captured revision and complete
literal inline spans in a wrapping noneditable scroll area. Start, End, Select text and Close are
reading actions; they do not save, discard, reattach or confirm a Note mutation.
Typing and responsive resize retain a still-authorized view. Closing, changing
the active reader scope or losing current authority retires the owned read and
its protected presentation. Complete large-context access and focus/cancellation
execution remain unqualified. Selection.toString() is a rendered serialization observation for the three
identified terminal-newline workloads; complete decoded DOM and actual selected
Range text/global offsets remain required for every interval; the
[Task6 record](../testing/owner-records.md) records the unresolved boundary.
