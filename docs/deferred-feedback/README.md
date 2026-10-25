# Deferred feedback

This is the canonical owner for independent remaining work: findings that do
not block current correctness or acceptance and that no ordered Task owns yet.
Current Task corrections stay with their Task, and Epic-sized work becomes a
TODO candidate or roadmap unit instead. A Task that adopts an entry moves it
into its own scope; the change that resolves an entry removes it here.

## Open entries

Recorded at the [EPIC-004](../epics/EPIC-004.md) closeout.

### DF-01 Driver first-attempt diagnostics

During the TASK-004-03 regression round, two manual drivers each failed once
and passed on identical-input reruns; the cause is unknown. The Chrome evidence
driver (`internal/adapters/http/auth/browser/check_reader_evidence.py`) calls
the in-frame `select` before the page channel reports ready. The native search
probe in `spikes/viewer/check_reader_native.py` raises the same timeout message
for each of its 30-second waits, so the failing wait could not be identified.
Wait for channel readiness before selecting and label each native wait.

### DF-02 Read pins after a crash

Ordinary read pins have no startup reclaim. A process crash during a pinned
read leaves the durable pin row in place with no automatic release, so its
objects stay protected from collection. This is storage retention, not data
loss, and it predates EPIC-004 in the EPIC-002 foundation. A reclaim must never
remove backup or job pins. [TASK-005-05](../tasks/EPIC-005/TASK-005-05.md)
deliberately defers automatic read-pin reclamation: crash-leaked pins remain
conservative retaining owners. Coherent capture/resume/abandon does not reclaim
read, backup or job ownership by age or presumed process death. This entry stays
open; DF-03's synchronous release and renderer drain remain unchanged.

### DF-03 Cancellation during a read-pin release

`foundation.Store.Read` releases its pin with a deferred write under
`context.Background()`, so a cancelled or expired request returns only after
the store gate frees ([recorded limit](../testing/pinned-references.md)).
Writers are bounded, so nothing leaks or blocks indefinitely. One reviewer
holds that cancellation propagation is a behavioral contract separate from
elapsed time; that disagreement is preserved. A fix must keep the renderer
drain guarantee that shutdown returns only after active reads release their
durable pins, which an earlier background-release attempt broke.

### DF-04 Empty scratch directories after canonical runs

Each `make test` run leaves empty `haeryong-tests-*` directories inside Go
package directories, at the scratch parent's absolute path re-rooted under each
package. The likely source is Ginkgo's per-suite report handling of the
absolute `--json-report` path that `tests/run_checks.py` passes. Git ignores
empty directories, so status stays clean. Existing directories are kept until
the owner removes them, and a runner fix is a separate development change.

### DF-05 Versions comparison keeps one batch on screen

The Versions view shows file differences one bounded batch at a time and does
not keep earlier batches on screen. This is a usability refinement, not a
defect.

### DF-06 Reader metadata 404 mapping

The Reader treats every 404 from the supplied-metadata read as absent metadata
(`frontend/src/app/screens/reader.tsx`), a behavior from TASK-003-04. A 404 for
another reason, such as lost access to the version, therefore shows as absent.
The Versions view accepts only the `metadata_not_supplied` reason as absence;
align the Reader so other 404s show its error state.
