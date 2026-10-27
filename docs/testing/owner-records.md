# Owner records composition (TASK-005-06)

Status: completed Task scope. The actual browser and Wails owner campaigns,
production persistence/recovery composition and final-candidate checks establish
this delivery. Exact candidate, command, inputs, outcomes and independent review
records remain private; historical failed attempts remain preserved. This is
agent-operated seeded proof, not a human study, physical-device qualification,
real-provider result or overall Epic acceptance.

## Authority, layers and fixtures

[TASK-005-06](../tasks/EPIC-005/TASK-005-06.md) owns this integrated reader task.
Assertion authority is [C07/C08/C09 and M09/M13/M18](acceptance-catalog.md),
[X06/X10/X13/X14/X18](contract-cases.md), [personal schemas](../contracts/schema-registry.md),
[owner UI flows](../ui/flows.md), and [protected recovery](../operations/backup-recovery.md).
The existing [Notes](anchored-notes.md), [lifecycle](document-lifecycle.md),
[purge](manual-purge.md) and [backup](backup-recovery.md) records retain their
qualifications. The [Epic handoff index](epic-005-owner-gate.md) separates member
Task delivery from assembled review and overall acceptance.

`tests/integration/owner-records` uses real SQLite/filesystem persistence and
application services. Its exact tuple/history and concurrent snapshot specs are
labelled `persistence`, despite the directory name. The snapshot case observes
the actual adapter `CopyFault("snapshot")` callback and releases/joins its worker
on failure. These internal checks are distinct from public E2E and native UI.

`tests/e2e/test_owner_records.py` uses real production CLI/HTTP/processes for
populated recovery/portability, original-intent response loss,
restore-first/purge-first/competing outcomes and process interruption. The shared
proxy forwards PATCH. Its platform listen queue avoids observed Darwin resets
at Python's default five-entry test listener; no failed call is retried by the
proxy. Observations distinguish actual upstream outcomes from precommit refusal.

`tests/manual/owner_records_support.py` copies the exact four checked-in files
from each approved Mulgae/Gaori sample to disposable inputs. The original
question, six facts and fixed source identities remain those of the
[two-project worksheet](two-project-knowledge.md). No new generation/provider
call or sibling source-repository write is part of this campaign.

Synthetic boundaries are separate: two v1 pages share `retry`, another has no
region, and payload includes Unicode paths, unknown types, literal links and an
empty directory. Declared large-context/body inputs are workloads, not limits.
Synthetic successors follow each client's own UI Save/Review phase. Reused
paths/slugs do not authorize mapping; successors remain Unreviewed and empty
until an explicit action. Original anchors/history must survive.

## Oracles, faults and client boundaries

Independent lstat inventories retain missing/extra paths, types, complete file
hashes, literal link targets and empty directories. Public inventories enumerate
all cursors. Directory/ZIP output and real reimported payload are compared with
those expectations; exporter verification is additional. Portable Note/history/context, Review/history, references, Revisit and every
selected reader resource are checked separately, retaining known deleted IDs.
Declared tuples/bytes are validated before assigned identities/stamps are frozen. Payload import does not
claim personal-data merge. Full canonical F12 export/reimport remains required.

Bootstrap, prepaired native-session installation, direct concurrent peers and
CLI backup/restore are supporting evidence. They do not certify production
in-app pairing/credential-store UI. Each selected client must use actual host
Save, explicit whole-version Review and lifecycle confirmations. Familiar
agent-operated reading is not human/blinded usefulness proof. Physical private
device sessions remain EPIC-007.

Interruption requires an observed running job, killed owned process, surviving
incomplete checkpoint and absent final publication. Completed-before-kill does
not qualify; no unobserved fsync/rename boundary is inferred. Original create
replay retains its acceptance. Explicit backup resume is a separate operation
linked to the same set when required. Protected recovery invalidates old
authority, rotates required epochs and retains cleanup/configuration review
guards. DF-02/DF-03 remain as recorded.

## Actual clients and commands

Use prepared dependencies and a new private physical receipt parent. Serialize
these with other consuming campaigns and freeze source during execution:

```sh
.venv/bin/python -B internal/adapters/http/auth/browser/check_owner_records.py \
  --browser-cli /absolute/prepared/playwright-cli --receipt /private/browser.json
H3N_RETAIN=1 .venv/bin/python -B spikes/viewer/check_reader_native.py \
  --stages owner-records --receipt /private/native.json
```

The new native stage is standalone with broader `full_qualification=false`.
Defaults and the old offline allowlist (exactly `app,corpus` or
`app,reference-corpus`) retain their meanings. Browser/HTTP/build evidence cannot
substitute for native proof. ReaderMode defaults remain 1180x880; this campaign
observes narrow content dimensions and actual focused PID-targeted key events.
Horizontal overflow is exercised through the existing semantic End/Start host
buttons, activated by trusted native Enter. Actual forward and reverse
scrollLeft movement remains required. Vertical overflow retains native
PageDown/PageUp input; no custom mouse-wheel injection is used.

Probe-only observers forward the actual guarded call and HTTP transport once,
preserving arguments/results/errors and granting no imported-content authority.
Each PID's final rows, exit, guard, facade and call observations are retained
before another launch. `page.done` marks completion, not a verdict: the exact UI
denominator must pass, input must have trusted observations, and normal process
exit/actual receipts are required. Partial/missing rows fail. Diagnostic rows
are separate from the UI denominator.

Ordinary independent failures continue; unsafe setup stops dependent work
explicitly. Source bindings include helpers/probes/fixtures, original samples,
configuration and compiled inputs; prepared tools/dependencies/caches are
recorded uncaptured inputs. Secrets and raw private evidence stay untracked.

## Current behavior and verification boundary

Notes replacement uses trusted HTML consent with captured owner/scope, draft,
ETag and pending identity, one admission and guarded journal writes. Safe
Enter/Escape/Close, containment, focus return and explicit confirmation remain
required. Consent checks await the actual dialog close event before inspecting
returned focus or admitting the next interaction; open=false alone precedes the
queued close handlers. Restart compares exact structured records independently of JSON object
key order; journal-byte and mutation-body checks remain exact. Native observers
select the current fixture's journal rather than an older retained namespace.
Dependent reads require their own completed Save/Review and reconciled draft.
Native authoring on the large-context fixture first waits for its actual current
page/render-bound capture, before admitting a new Note and body preparation.
Native control admission waits for the actual connected, enabled, non-inert,
visible control and UA focus before one trusted action. ReaderMode activates its
own initial window once after guard installation and before bridge/probe bootstrap;
it does not repeatedly focus on navigation. Admission does not infer modal
eligibility from containment in the first dialog: Notes confirmation may be
portalled above the Reader Sheet. Product confirmation remains mandatory.

Complete context is rendered as ordinary inline spans at grapheme boundaries,
near 4096 UTF-16 units, without a content quota or omission. Preparation yields
to host tasks. The viewport uses pre-wrap/anywhere/normal styles. Owned reads
cancel acquired or late handles and fence obsolete text, error and focus;
typing and resize preserve the current read. Finite browser barriers exercise
the actual NotesPanel and real streams, including Close during acquisition/body,
superseded completion/error and tab/protected-capability retirement. Capability
projection is supporting setup, not server authorization or native proof.

One unchanged 15000ms interval includes reading, layout, selection/input and
Close. Pending frame/input waits are bounded failures, never synthetic render
success. The original 3,060,057-byte workload and complete DOM/UTF-8/hash checks
remain. Full, partial and restored selection always require exact associated
Range text and global offsets. Only crlf-tabs, trailing-newline and
large-multiline treat rendered Selection.toString() as an observation; other
native strings remain strict. No normalization, Copy override or OS clipboard
proof is introduced. Native logical endpoint access uses the product's End/Start controls with
trusted Enter input. Forward/reverse horizontal movement is required when a
logical endpoint lies horizontally outside the viewport. Combining-mark ink
overhang alone does not require movement when both caret positions are already
in view; independent first/end visibility and exact text selection remain required.
Keyboard observations wait for real default-action frames and retain actual
destinations as part of the complete native campaign. The body
reopen action binds the intended annotation/revision and records acquisition, consent/draft/pending state
and exact editor bytes/hash without clearing uncertain intent.

Recovery compares the complete original/successor inventory, known identities,
history and streams before subsequent actions. All six payload trees remain
independently checked; original-only X18 exports and portable personal metadata
are separate. Short physical/private socket roots follow the mounted writable
RootKernel policy. Reference proxies use the shared forwarding listener's
platform queue, without retries; network failures and readiness observations
retain their actual outcomes. Versions' explicit clipboard-unavailable test
checks the real fallback link field and exact tuples, not OS clipboard output.

The complete browser and actual native campaigns exercise explicit Save/Review
before successor versions, safe confirmation at wide/narrow layouts, all twelve
literal-context classes, lost-response recovery, deliberate conflict resolution,
archive/trash/restore and cancelled purge, normal restart, protected recovery,
remembered reopen, original payload export/reimport and separate personal metadata.
The native Save-outcome observer waits for committed editor removal or a current
enabled reconciliation action. It does not capture a transient disabled action
while the original same-key operation is still reconciling. A disconnected
control is a case failure, not evidence that the whole GUI resource was lost.

The final-candidate backstop follows the verification profile and includes the
failed whole-suite CLI fixture's corrected retained-cursor case, its full CLI
persistence consumer and every changed driver/helper consumer. The complete
whole-suite anchor retains its original nonzero result; it is not relabelled as
a passing whole suite. Final focused/delta evidence is distinct from that anchor.

Restored replay now selects only the current stored epoch in both SQLite readers
and computes the head from that epoch. Retained historical rows remain unchanged.
Cursorless replay may begin above sequence 1. The implementation conservatively
resyncs when a positive cursor’s current-epoch row is unavailable; acknowledged-row
membership is an implementation policy, rather than an original contract requirement. Explicit header/query zero resumes and live SSE positions
require continuous history from their position, including zero. Empty current
history remains valid; uncertain restored sequence origins require resync. SSE
delivers its validated initial batch once before strict live continuation.
Poll/SSE preserve safe resync for unavailable/old/future/
malformed cursors and true interior gaps, plus final per-delivery authority checks.
Focused race regressions include both readers, ordinary reopen, actual protected
restore/review followed by poll, both wire transports, and existing event-scope/
expiry/delayed-delivery consumers. These and the complete browser owner campaign
passed on their recorded correction source. Added zero-resume regressions exposed
silent suffix delivery before the correction and passed afterward, including
header/query polling and SSE, empty history, sequence-one resumes and atomic
live-prefix loss. Actual post-restore native body/context and final-candidate checks are part of
the completed owner composition; their exact execution identities remain private.

Reproduce the focused restored-event classes with prepared offline Go dependencies:

```sh
go test -race -count=1 ./cmd/haeryong ./internal/adapters/sqlite ./internal/adapters/http/auth ./internal/application/query \
  -ginkgo.focus 'Event replay excludes retained prior epochs|Restored event replay and resync|Zero-position live event continuity|Copies protected restore invalidates old tokens' \
  -ginkgo.label-filter persistence
go test -race -count=1 ./cmd/haeryong -ginkgo.label-filter persistence
```

Historical native failures, the old phone timeout and the later browser
missing-key/menu observations remain preserved privately. The old phone capture
loss is established; its execution cause is UNKNOWN. Later instrumentation left
the browser actions and assertions unchanged and passed, without capturing a
failed boundary or establishing a cause. Temporary observers were removed for
final execution. No historical failure is rewritten as a pass, environment
cause, repaired-cause claim or flake allowance. Task acceptance, local commit,
assembled Epic review and Master's overall acceptance remain separate decisions.
