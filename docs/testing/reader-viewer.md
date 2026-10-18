# TASK-003-03 Reader and renderer delivery

**Task:** [TASK-003-03](../tasks/EPIC-003/TASK-003-03.md)\
**Implementation scope:** The shared Reader, renderer, personal resources and
closed native adapter are implemented and checked. This record describes
browser interaction/recovery and the reused Wails harness; the canonical
[Task record](../tasks/EPIC-003/TASK-003-03.md) owns delivery status and
acceptance. The production desktop companion and completed Epic gate retain
their ordered owners.

## Origin ownership and delivery

Renderer origins use a per-OS-user product-runtime ledger outside library
profiles. One origin permanently belongs to its library/version/content-set/
payload tuple. Closing a view, changing libraries or deleting a profile never
makes that origin available to a different tuple. Existing reservations remain
sticky after pool changes; exhaustion affects new renderer reservations, with
pool expansion and daemon restart as the recovery path. Port conflicts retain
the reservation and report an unavailable renderer instead of choosing an
unrecorded fallback.

The default pool is `http://127.0.0.1:9281` through port 9288. Configured
management and renderer-mapping upstream ports are excluded; the actual
management endpoint is also excluded when a CLI listener override is in use.
The historical fixture origins on ports 9272–9273 remain unavailable to this
ledger. Renderer origins never become management `allowed_origins`.

Production state is rooted in the effective UID's account-database home:
`Library/Application Support/Haeryong` on macOS and `.local/share/haeryong` on
Linux. `HOME`, `XDG_DATA_HOME`, profile selection and `--data-dir` do not choose
this namespace. Darwin's native Go account lookup works with or without cgo;
Linux requires its cgo account lookup. `osusergo` and unsupported account
resolvers fail the renderer closed while ordinary library operations remain
available. Tests use an explicit internal app-state directory. The production
binary has no app-state-path override; the separately tagged test-state binary
provides only the documented fixture path seam. Checks do not initialize or
migrate the operator's real per-user app state.

An app marker and origin ledger share an OS lock. First initialization and
interrupted initialization are reconciled under that lock; a ready marker with
a lost, corrupt, foreign or unknown-shaped ledger fails closed. Reservations
are synced before binding a listener or returning a handoff. An uncertain
post-rename persistence failure poisons that process's ledger access until
restart. Library-local binding markers detect a different ledger identity.
Whole-app-state deletion, account-home relocation and whole-machine rollback
are not supported ways to reset origin ownership.

The trusted host opens a delivery session with a retained operation identity.
A one-use form POST establishes a narrow, HttpOnly delivery cookie. Ordinary
content and loader requests require the exact renderer Host plus the current
same-origin request context. A same-site request from another loopback port is
rejected even when the browser attaches cookies shared by the hostname.
The renderer mounts no management API. Authored paths such as `api/data.json`
and `__haeryong/loader.js` remain ordinary payload files; only the loader's
versioned query selects the integration resource at the latter path.

Current owner/session or scoped authority, exact version visibility and
document lifecycle are rechecked during protected delivery. Bounded progressing
reads/writes extend the idle lease. A transient authority lookup returns 503
without destroying a cookie or burning a handoff; definitive denial terminates
delivery. Terminal close stays possible after trash. Restoring a document or
restarting the daemon does not revive an old delivery session. Controlled
restart stops the management endpoint and work, drains renderer handlers and
their read pins, then closes storage and releases the writer lock.

## Verified bytes and derivatives

The object store verifies a private pinned descriptor before any consumer
receives it. HTML uses a seekable splice over that same descriptor to add a
small external integration tag. The splice scans bounded buffers, preserves
authored head attributes, BOM/doctype/comments, and does not create a complete
HTML copy in memory. Range and conditional delivery use the same representation.
The derivative's strong opaque ETag identifies the verified content digest,
canonical page path, loader version and splice; it is not labelled as the hash
of delivered derivative bytes. Stored and exported author bytes are unchanged.

HTML delivery chooses a BOM first, then the first valid declaration in the
original first 1024 bytes, including charset and content-type pragmas, with
first duplicate attributes retained. Meta UTF-16 labels normalize to UTF-8;
meta x-user-defined normalizes to windows-1252. No declaration keeps the
product's deliberate UTF-8 default. This prescan window is not an author limit.
UTF-16 uses code-unit boundaries; ISO-2022-JP tracks physical offsets and
encoding designations so the tag does not introduce escape sequences or
change Roman yen/overline text. A malformed prefix that cannot be integrated
safely serves the exact original with its raw strong validator and source
identity headers, omitting only the derivative marker. Real descriptor I/O
failures propagate instead of becoming successful fallback responses.

The v2 integration tag URI-encodes the exact UTF-8 page identity (including
literal percent, plus, backslash, control characters and U+FFFD), additionally
escapes tilde, then applies HTML attribute escaping. The loader decodes once
without plus-to-space conversion and refuses malformed URI encodings. Authored
CSS and classic JavaScript retain their browser encoding selection without an
automatically appended UTF-8 MIME parameter; the owned loader is explicitly
UTF-8, while module scripts follow the browser's UTF-8 rules.

Exact paths precede a unique NFC-equivalent fallback. Whole-entry in-version
symlinks resolve with cycle/outside-tree checks and report the canonical page.
Traversal through an intermediate symlink directory is not implemented; an
unresolved alias returns an honest error rather than reading an OS path.
Ordinary relative URLs retain browser behavior. There is no extension admission
allowlist, compulsory manifest/index or content size/count/depth quota.

The unprivileged loader is built from `frontend/src/viewer/loader.ts` and
`snapshot.ts`, using the already pinned `@noble/hashes` dependency. The separate
IIFE build is committed as `internal/webassets/dist/renderer/loader.js`, served
through the authenticated renderer adapter and covered by the shared asset
manifest and fresh-build drift check. It imports no trusted client or native
bridge and receives no owner bearer.

## Personal resources and context transport

Reader preference, version reading pointer and per-page position have distinct
owner/version/resource validators. Validators include the complete resource
identity and current value/revision and are compared inside the transaction.
A page A validator cannot update page B or the version pointer. Absent state
uses the registry's closed wire shape (`exists: false`, the family's default
visible value, explicit null `revision`/`etag`, `required_precondition`), a
stored record — including one storing the default value — keeps `exists: true`
with a real validator, and every response carries the mandatory nullable
`page_position_url` key (the pointer's once-encoded canonical page link, or
explicit null). Renderer page-identity headers are percent-encoded once and
decoded once by receivers. The shared Reader now connects these resources to separate default, Continue,
and current-page position actions. The Task record owns completion status and its native review boundary.

Context records preserve immutable bytes and the originating principal,
session, version, canonical page and render instance. Direct and sealed-upload
forms retain the original consumer identity for identical bytes/binding;
alternate transport does not create a new semantic operation. Context reads
use current authorization and protected streaming. General authorized recovery
must preserve the original binding; it is distinct from accepting a record as
a fresh active page's selection.

The loader clones a serializable snapshot at begin and walks that clone in
deterministic UTF-8 pieces. Even a single large string is escaped in bounded
pieces. Resuming an offset reconstructs the same bytes and incremental digest;
losing the snapshot reports interruption. The trusted host journal preserves
separate upload-create U, seal S and consumer Q identities before dispatch.
HTTP-confirmed ranges own durable resume; message acknowledgements alone do
not. A ready response requires an actual sealed object and matching complete
digest. Channel disposal does not abandon staging or delete pins. Save/Ask and
adoption into their durable records remain with EPIC-005.

Fresh ready/echo verification has a concrete client/server transport path.
The exported host verification adapter performs the server's closed
fresh-binding proof: one GET carrying the `Haeryong-Context-Binding` request-header pair
(canonical unpadded base64url, exactly the three nullable tuple keys, strict
duplicate/CR-LF/non-canonical-encoding/identifier/path rejection) plus the
expected strong `If-None-Match` digest from the receipt. The server proves
current authorization, the exact originating session, the persisted tuple
and the digest in a single transaction without opening the staged object
and answers with a body-less 304 carrying the record ETag and the
`Haeryong-Context-Verified: fresh` marker (exposed to permitted cross-origin
trusted callers alongside ETag; supplying the response marker as a request
header is not allowed). Tuple, session or digest mismatches are 412 binding
rejections; binding-header absence keeps the ordinary authorized recovery
GET byte-exact — standalone conditional or Range headers stay ignored with
a full-byte 200 and no marker. The shared client exposes
`verifyContextFresh(context_ref, binding, bytes_sha256)` on both the HTTP
transport (exact 304/marker/ETag checks; a 304 is not response.ok) and a
typed closed native operation implemented by the harness's Go ReaderService,
and the exported real adapter wires
it into the transfer host before any ready reply and before every echo
acceptance.

Renderer availability is request-context honest. The exact trusted runtime
endpoints (actual local management endpoint plus the configured external
origin) classify ingress; a request is remote when its supplied Origin is
the external origin, or its Host is the external host while differing from
the actual local host (local wins ties, and equal-host tuples are refused
at startup and at configuration plan/commit). Remote ingress sees the
renderer feature flags off with `renderer_unavailable_reason` on
capabilities/bootstrap, and open/renew fail 503 FEATURE_NOT_READY with
`details.reason=renderer_remote_ingress`, retryable false and no
Retry-After before any lazy renderer construction, operation reservation
or listener; ordinary queries, imports, downloads and known-owned-session
close stay usable remotely. No physical-location claim is made from
Forwarded/User-Agent or pairing history.

## Reader recovery and native local logout

Each logical host history entry owns its Open journal independently. A retiring
generation retains its actual in-flight Open/initial Renew and learned session.
A successor waits before touching that journal. Canceling an intermediate
waiting screen does not remove the predecessor's cleanup obligation. An unknown
Open retains its original key; a learned session retires its own key durably
before Close, including when the first Renew failed. Storage failure prevents
Close. A settled Close failure remains retryable against the same session;
generic authorization or transition failures never establish successful Close.
Same-instance effect reacquisition is reversible only before key retirement.
After that boundary it waits and obtains a fresh key/session. Old responses
update their captured generation, never a successor's frame or journal.

The waiting/error screen offers full-host refresh even while cleanup is pending.
The error screen's reopen action retries captured cleanup before a new Open.
For an unresponsive desktop WebView, restart the app. Reload preserves an unknown
Open key when no Close could have been dispatched; if Close could have been
dispatched, that key has already been removed, so the new session is independent.
Unconfirmed old sessions fall back to their server lease expiry; refresh is not
reported as a successful Close. Coordination is within one JavaScript realm;
duplicating a tab with cloned history/storage is not a cross-tab lease lock.

The reused native ReaderMode keeps the bearer in Go and returns only a captured
attachment identity to the shared frontend. Explicit logout clears usable RAM
authority and attempts credential-file removal before server revocation. A real
local I/O failure returns a typed error immediately, even when the captured
best-effort server revoke is still pending. The connection screen warns that
restart may restore the surviving file and offers a cleanup retry. Disposal
retains that retry identity after detaching the live session. File absence is
success; a different credential is left intact. No successful local forget or
server acknowledgement is inferred from a failed unlink or a lost reply.

Focused controlled regressions cover the canceled-waiter chain, held Open,
held/rejected first Renew, durable-key removal failure, same-instance reacquisition
on either side of that boundary, manual Close retry and authority replacement.
Native Go race tests use an actual read-only temporary directory and a blocked
HTTP response to check immediate local error and successful retry after disposal.
These controlled tests do not replace the actual browser or Wails checks below.

## Checks observed during implementation

These focused checks have executed through Gaori; private runtime identities
and logs are retained locally rather than cited as durable project evidence.
They do not replace the final same-candidate gates or independent result
assessments.

| Check | Observed scope |
| --- | --- |
| Origin ledger unit and persistence specs with race detection | Existing reservations, concurrent initialization, partial publication and corrupt-state refusal; a regression first reproduced `records: null` reassigning a reserved origin, then verified rejection without rewriting the ledger or ready marker |
| Renderer persistence specs with race detection | Actual initialized SQLite, owner pairing, directory import/seal/publication and loopback HTTP delivery; HTML/ranges/ETag, authored API-like paths, loader cross-port denial, transient handoff, concurrent single consumption, lease progress/idle, terminal close after trash, renderer-facade restart on its sticky origin and old-cookie denial |
| Active delivery and integrity faults | One response progresses beyond its initial simulated lease; shutdown drains an active verified read and its durable pin; a same-size corrupted HTML object yields no authored bytes or success validator |
| Renderer/service unit and integration specs | Bounded HTML splice and exact ranges; unused lazy renderer does not initialize account state; dependent listeners close before storage/writer, including startup failure after preparation |
| Incremental snapshot and host transport tests | Large individual Unicode strings, immutable begin snapshot, arbitrary-offset replay/digest; retained U/S/Q after lost create/chunk/seal/consume replies, server-confirmed resume, journal-denial dispatch block, late authority invalidation and no ready before binding verification |
| Page-channel tests | Exact source/origin/page handshake, private port, full binding/sequence, forged management/native-message rejection, large ordinary display JSON, foreign ref and stale/inactive callback rejection |
| Actual loader protocol module with DOM seams | Real MessagePorts transfer over 17MiB of snapshotted Unicode state under credit, retry the same transfer/offset after a transient error, reject wrong binding/credit/digest, retain initial page errors and stop scheduled retry on disposal; this is Node unit evidence, not a browser/Wails result |
| Actual Chrome renderer foundation | Real daemon with test-only app-state path isolation, owner pairing/import/publication; inline scripts, ES modules/module Workers, CSS/fetch, Unicode links/query/fragment, ranges and unchanged original bytes; terminal close and actual daemon-restart old-cookie denial. An authored root Service Worker controls A but cannot control library B's distinct origin after A stops. This is a top-level renderer check, not Reader UI, iframe host-channel, Wails or private-ingress qualification |
| Actual Chrome encoding and automatic channel | Current test-isolated daemon and bundled loader, real iframe/private MessageChannel; windows-1252, EUC-KR, Shift_JIS, both UTF-16 BOM endiannesses, ISO-2022-JP Roman and leading JIS text, exact C1/U+FFFD filenames; early authored scripts, legacy CSS/classic scripts, UTF-8 modules, exact source export and derivative ranges. This is foundation evidence, not the shared Reader UI or Wails qualification |
| Context binding header parser | Closed three-key nullable JSON form with literal-`"null"`/JSON-null distinction, escaped-Unicode single-decode parity, duplicate/omitted/unknown/mistyped member rejection, and strict canonical base64url (embedded CR/LF and non-canonical trailing bits rejected by encode round-trip) |
| Fresh-proof HTTP-handler persistence specs | In-process HTTP handler with real SQLite: body-less 304 with exact ETag plus `Haeryong-Context-Verified` marker and no Content-Length; 412 on tuple/session/digest mismatch; 400 for the closed malformed-request matrix; plain recovery GET unchanged with standalone If-None-Match/If-Match/Range ignored (200 full bytes, no marker); unknown refs nondisclosing; service-level originating-session, scoped-session and trash-lifecycle proofs without opening the staged object |
| Ingress classification and remote denial | Remote Host without Origin and local Host with external Origin classified remote (flags off with reason, bootstrap attachment fallback, open/renew 503 FEATURE_NOT_READY retryable false with no Retry-After and no renderer construction); known-owned close from remote still 204; equal-host tie defense classifies local; nil wiring keeps the all-local default; local/native bearer reuse unaffected |
| External endpoint collision | Static validator rejects an external host equal to the configured listener host; effective-endpoint checks at configuration plan and commit use the candidate restart endpoint without an override and the trusted actual runtime endpoint under one, failing fresh proposals and previously persisted plans with no revision/effects/restart; product wiring refuses startup on an actual-endpoint collision, including remote-disabled neutrality |
| CORS proof headers | Preflight admits the request `Haeryong-Context-Binding` alongside If-None-Match; the response marker is exposed with ETag to permitted cross-origin trusted callers and is not an allowed request header |
| Account resolver runtime | Actual Darwin supported lookup; cached Linux container without network access, supported cgo lookup and unsupported pure-Go refusal; no production app-state initialization |

The M1 foundation received its independent milestone assessments. The subsequent
assembled Reader browser, recovery, Import, Wails, root and frontend checks
described below have also executed successfully. Whole-Task acceptance requires the Task Judge and Epic Judge assessments
followed by Main's final review.
An unsupported Host-rewriting/Origin-less proxy can
still consume sticky permanent origin reservations through misclassified-local
opens; that boundary is documented in
[deployment](../operations/deployment.md) with pool expansion and restart as
the only recovery, and actual private-ingress qualification stays EPIC-007.
Production companion/chooser belongs to TASK-003-05,
full cross-client/responsive qualification to TASK-003-06, private deployment to
EPIC-007. None is claimed by these foundation checks.

## Shared Reader integration

The trusted Reader owns document/version metadata, cursor file lists, personal
resource actions and the existing exact-document Import control. Its import
mount identity is authority/project/document; a version change under that same
document does not remove an active import. Closing the import panel stops the
mounted flow without abandoning retained upload state. Attachments reuse the
consent-gated streaming destination engine with authenticated file retrieval;
author bytes never become a trusted-origin Blob preview.

`IsolatedViewer` owns one host-selected browsing context, initial one-use POST
handoff and captured delivery identity. Child navigation, hash changes and
history calls report through the current private channel; the host mirrors with
`replaceState`. Host selection/Continue creates one host history entry and a new
frame. New documents establish their canonical stored page independently before
connecting. Public new-window mappings use the browser management origin, not
the native static origin. Loader family v3 distinguishes the changed integration
from cached earlier derivatives. The page binding includes `document_id`; the
context upload target is explicitly narrowed to its distinct closed three-key
version/page/render binding before dispatch.

Personal requests are journalled before effects. A later observation queues
separately from an immutable in-flight request. Unknown outcomes keep the same
key/body/preconditions; confirmed 412s use the objective three-way comparison
on every round. Genuine unchanged bases permit another key without a retry-count
cutoff. Conflicts remain visible until a confirmed explicit server/local choice.
Scrolling saves only the current page position; setting or clearing a default
does not itself write the last-opened pointer. Continue reads the pointer and
that page's independent position. Correlated restores yield to intervening user
input; snapshots are accepted only for the outstanding current-page request.

The portable browser entrypoint is documented in the
[frontend README](../../frontend/README.md#reader-browser-regression). Its actual
shared-Reader checks complement the foundation checks above. They do not
establish native attachment, Wails byte-stream/guard behavior, full cross-client
qualification or final Task acceptance. The complete root gate and both
Judge approvals followed by Main's final review remain required on the final
integrated Task candidate.


## Assembled verification candidate (2026-10-18)

The common `make test` gate passed its serial prepare, unit, integration,
additional persistence and Python E2E stages. Regeneration followed by normal
`frontend/check_build.py` verification passed. The native module's
`go test -race -count=1 -timeout=120s ./...` also passed; its SDK linker emits
the existing duplicate-Objective-C-library and deployment-target warnings.

The actual Chrome Reader entrypoint exercised ordinary interaction, local
module/Worker and external Preact, immutable multi-page routes, default/Continue,
scroll restoration and the preselected Import control. Its staged context had
18,000,037 independently hashed UTF-8 bytes. The recovery entrypoint now covers
nine scenarios, including away/Back with a committed Open or first Renew reply
held, an Open reply lost after commit, and a Close acknowledgement lost after
actual 204. Manual retry preserves captured cleanup; a separate independently
closed-session case preserves real dead-receipt recovery. Each driver checks
stable source/embed inputs and closes its browser and daemon.

The portable [native Reader check](../../spikes/viewer/check_reader_native.py)
uses the existing beta.28 harness and real tagged test daemon. Its declared
five-stage run covers 23 raw-facade rows, actual shared App interaction and
context, labelled pending-request persistence/replay across restart, and a
forced window crash followed by a healthy attachment. The app's 960,131-byte
context contains actual local module/Worker, external Preact, WASM and authored
handler results, compared with an independently constructed expected digest.
The native handler invocation is programmatic, not a physical-user observation.
A captured-attachment regression additionally checks that stale disposal cannot
erase another logout's authenticated renderer cleanup, while the same disposed
identity can still retry a failed local forget. TASK-003-04 extends the same
driver with an `evidence` stage (included in its default stage list) for the
shared Reader's evidence presentation; [the evidence record](evidence-presentation.md)
owns that contract and its checks.

These are agent-operated functional observations. Native popup target mapping,
privileged-handler removal and repeated sender/scheme denials are distinguished
from popup DOM authentication and measured lease expiry. The restart pending
record is explicitly seeded after a real server write; it does not establish a
native network-loss experiment. Actual private-device access remains EPIC-007.
Independent whole-Task approval and Main acceptance are recorded at the Task
boundary; this candidate record alone does not complete Task03 or EPIC-003.

A focused actual Chrome observation also activated named page links and the
exact-document Import control using keyboard Enter/Space and taps in a
touch-enabled 820×1180 context. This establishes changed-control input
activation; comprehensive responsive/accessibility verification remains with
TASK-003-06, and no physical-user study is claimed.
