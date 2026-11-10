# Scan inventory and deterministic structure evidence (TASK-008-01)

**Task:** [TASK-008-01](../tasks/EPIC-008/TASK-008-01.md)
**Implementation status:** done (bounded member accepted by the Coordinator under Master delegation; normal completion commit and native finalization follow)
**Evidence status:** pass with explicit execution qualification. The corrected native common passed all canonical layers (1,390 Go and 256 Python cases; zero required failures/skips), focused fixture checks passed, and independent affected Task/result and Epic-contribution assessments plus current composition support the bounded outcome. The first common and first panel remain negative historical evidence. The enclosing corrected-run guard exited `1` for an unrelated Coordinator-created bytecode cache; native Make/Gaori exited `0`. Its exact ownership, removal and unchanged executable inputs were separately established and accepted by the affected assessors; no pristine wrapper success is claimed.
**Source revision:** frozen implementation/test candidate over `49cf4c8b65eda1404f83b6f01b255334f590826c` (design HEAD). The corrected full common and current assessments apply to that exact pre-closeout candidate. This final factual/lifecycle record overlay has separate affected documentation verification; it does not retarget the full common to later record bytes. The normal implementation commit follows this accepted composition.
**Environment/toolchain:** Darwin `arm64`, offline Go local toolchain (`-mod=readonly`, `-race`), prepared Python `.venv`, Node/TypeScript bundle verification
**Implementer / independent reviewer:** Task01 implementation writer and bounded correction writer; Coordinator substantive review; independent Task Evaluator and non-author Task Operators; continuous Epic Evaluator. Original first-round judgments remain negative. The bounded affected assessments and eligible closures establish the corrected Task result and Epic contribution without claiming completion of later members or overall Epic acceptance.

## Actual outcome

`TASK-008-01` implements schema revision 20 scan admission, incremental `job_items` enumeration, deterministic structure and reference-index inspection, owned `derived_index` (`references`) repair child fencing, and exact coverage reporting across SQLite, application services, HTTP (`POST /api/v1/scans`, `GET/HEAD /api/v1/jobs/{id}/findings`), CLI (`haeryong scan`, `haeryong findings list`), and the shared browser/native client and desktop `readerfacade` adapters (`scanStart`, `jobFindingsList`) under the [personal-tool applicability](../changes/2026-10-10-personal-tool-revision.md) and [directory-results applicability](../changes/2026-10-10-directory-results-revision.md) baseline.

Key delivered boundaries and limits:

1. **Schema revision 20 and immutable denominator capture (`T-008-01-01`)**: Migration 20 (`internal/adapters/sqlite/scan_schema.go`, wired by `internal/adapters/sqlite/schema.go`) adds `scan_runs`, `scan_captured_versions`, `job_items`, and `findings` with indexes `scan_runs_by_scope_hash`, `scan_captured_versions_by_job_doc`, `job_items_by_job_state_ordinal`, `job_items_by_document`, `job_items_by_version`, `job_items_by_repair_job`, `findings_by_job_order`, `findings_by_version_status`, `findings_by_project_status`, and `findings_by_fingerprint`. Admission captures the eligible document/version roster (`ORDER BY document_id COLLATE BINARY`), immutable denominator (`projects`, `documents`, `versions`, `pages`, `regions`, `entries`, `sources`, `indexes`, `excluded_documents`, `total_units`), effective maintenance policy snapshot (narrowed by project-level `max_concurrent_ai_jobs`, `max_provider_attempts_per_run`, and `max_run_seconds` overrides when tighter than global defaults, with normalized `depth` in `change_focused` | `full_scope` | `deep_semantic`), and pre-dispatch source target sets (`pinned` vs `current` for `kind = "git"` and `kind = "files"` sources with `selected_snapshot` or `reference_only` retention). Later document/version publications, source edits, or configuration changes never alter the captured denominator (`result.captured`) or silently retarget captured versions.
2. **Incremental 64-unit batch enumeration, entry-kind verification, and structure inspection without AI (`T-008-01-02`)**: The worker materializes `document`, `version`, `page`, `region`, `entry`, `source`, and `index` `job_items` in bounded 64-unit SQLite transactions (`EnumVersionOrdinal`, `EnumAfterPath`, `EnumAfterRegion`, `EntryAfterPath`, `SourceAfterIdx`) without capping directory size, file count, or depth. Structural inspection verifies version manifests, canonical inventory entries across all stored entry kinds (`file` with ready object bytes/SHA-256, `directory` with `content_hash IS NULL`, and `symlink` with `content_hash IS NULL` and preserved `exact_link_text` validated via `domain.InventoryLine` without reading an empty object hash or following external/dangling targets), optional supplied metadata (`regions`, `evidence_map`, `references`), HTML target existence, and canonical reference index parity (`authored_references`, `authored_edges`, and `reference_materializations` keyed by `from_version_id`) against authored declarations. Pages without optional metadata or regions and unpreviewable binary files pass structure checks (`structural.checked`) while recording `regionless_versions` / `metadata_less_versions` and skipping only the undeclared source placeholder unit (`no_source_declared`). When stored reference rows diverge from authored declarations, the scan records a deterministic `backlink_index_repair` finding: under effective `auto_apply.backlink_index_repair = "auto"` it queues or reuses an owned `derived_index` (`references`) child job guarded by the storage `ReferenceFence` (`scanRepairAuthority`) and waits in stage `repair` before marking the finding `applied`; under `"propose"` it records a `proposed` finding (`approval_required = true`) without mutating reference tables. No LLM/provider call is made during admission, inspection, or repair.
3. **Current-authority, Trash/purge, and principal-isolation fences (`T-008-01-03`)**: Pre-existing trashed documents in a project are excluded at admission (`document_trashed` in `result.captured.excluded_documents`; archived documents remain eligible) without polluting `active_document_ids` or `active_version_ids`, so derived workflow credentials can immediately read their admitted project scan jobs and receipts. Active coalescing (`ScopeHash`) binds `PrincipalID`, `AuthKind`, and `CredentialScope` so distinct workflow principals and the owner principal never coalesce across principals, while equivalent requests sharing the same authorized principal, `auth_kind`, and `credential_scope` (including different remembered owner device sessions) may coalesce onto an active `queued`, `running`, or `interrupted` scan job. Centralizing `domain.RestrictCredentialScope` across `internal/domain`, `internal/application/auth`, and `internal/adapters/sqlite` ensures that when a workflow principal's scope is narrowed or a targeted document moves to Trash or is purged, `GET /api/v1/jobs/{id}`, `GET /api/v1/jobs`, `GET /api/v1/events`, `GET /api/v1/operations/{id}`, `POST /api/v1/jobs/{id}/cancel`, and `GET /api/v1/jobs/{id}/findings` enforce current authority (`404 NOT_FOUND` for hidden single-target or narrowed-scope jobs). When a target document moves to Trash or is manually purged during a scan, the lifecycle transaction transitions unfinished items (`document_trashed` / `document_purged`), marks `proposed` findings `stale` or `blocked` as the path dictates, scrubs private paths, region IDs, source selections, and finding payloads (`{"purged":true}`) on purge while retaining minimal opaque identifiers and immutable denominator counts per the permanent minimal reservation policy, and immediately refreshes `ScanRun.Report` (recording `incomplete_reference_repairs` with `rescan_recommended = true` when a linked repair child is interrupted) while preserving healthy sibling documents, versions, and `source_targets`.
4. **Excluded work and honest readiness**: Structural completion never claims semantic verification; the semantic aggregate reports `status` in `not_assessed` | `unable_to_verify` and `reason` in `semantic_assessment_not_run` | `structural_or_source_blocked` | `run_budget_exhausted` (`provider_attempts = 0`). Premature finding-reassessment fields (`finding_id`, `expected_finding_revision` on `POST /api/v1/scans`) return `503 FEATURE_NOT_READY`, and CLI `haeryong findings approve|reject` exits `5` (`UNAVAILABLE` envelope code with `FEATURE_NOT_READY` text); finding decision HTTP routes remain owned by `TASK-008-05`. General daemon downtime remains conservative first-claim elapsed wall-time accounting (`max_run_seconds` from first `ClaimJob`); only explicit protected-restore hold time (`restore_job_hold`) is excluded under the accepted plan, and budget exhaustion skips remaining `pending`/`running` items with `run_budget_exhausted` (`budget_exhausted = true`).

## Case evidence

| Task-local / inherited / supplemental case | Fixture or exact source target | Expected observable | Command or procedure | Actual result | Evidence location |
| --- | --- | --- | --- | --- | --- |
| Scan policy & scope-hash unit (`SCAN-POL`) | `internal/application/scan/policy_test.go` (`4` unit specs) | Parse/merge maintenance policy with tighter project numeric budget overrides (`max_concurrent_ai_jobs`, `max_provider_attempts_per_run`, `max_run_seconds`), normalize depth (`change_focused`, `full_scope`, `deep_semantic`), and bind `PrincipalID`/`AuthKind`/`CredentialScope` into `ComputeAuthorizedScopeHash` | `go tool ginkgo --race --fail-on-empty --fail-on-pending --no-color --label-filter=unit ./internal/application/scan` | `pass` (`4/4` specs) | [policy.go](../../internal/application/scan/policy.go), [policy_test.go](../../internal/application/scan/policy_test.go) |
| `T-008-01-01`, `T-008-01-02`, `T-008-01-03` & persistence regressions (`SCAN-BUDGET-01`, `SCAN-GUARDS-01`) | `internal/adapters/sqlite/scan_test.go` (`5` selected persistence specs) | Immutable captured denominator across post-capture publications/source edits (`T-008-01-01`); zero-provider `backlink_index_repair` (`auto` child `derived_index` vs `propose` finding) and >64-unit batch boundary pagination (`71` total units across `2` pages + `64` regions + `2` entries + `1` version + `1` source unit + `1` index, `T-008-01-02`); valid directory/binary/symlink entry inspection with zero empty-hash object reads, Trash/purge/revocation mid-scan blocking (`document_trashed` / `document_purged`), multi-target healthy sibling preservation, immediate report refresh on trashed repair child, `max_run_seconds` deadline (`run_budget_exhausted`), and `restore_job_hold` exclusion (`T-008-01-03`, `SCAN-BUDGET-01`, `SCAN-GUARDS-01`) | `go tool ginkgo --race --fail-on-empty --fail-on-pending --no-color --label-filter=persistence --focus="T-008-01\|SCAN-" ./internal/adapters/sqlite` | `pass` (`5/5` selected specs; nonselected SQLite specs filtered) | [scan.go](../../internal/adapters/sqlite/scan.go), [scan_test.go](../../internal/adapters/sqlite/scan_test.go), [worker.go](../../internal/application/scan/worker.go), [worker_inspect.go](../../internal/application/scan/worker_inspect.go) |
| HTTP scans adapter & principal/workflow scope visibility | `internal/adapters/http/scans/handler_test.go` (`2` integration specs) | Closed `POST /api/v1/scans` decoding, `503 FEATURE_NOT_READY` on premature finding reassessment, owner-only `GET/HEAD /api/v1/jobs/{id}/findings` with strong ETag/304 & cursor pagination, workflow scope narrowing (`404` across `Job`, `Jobs`, `Events`, `Operation`, `CancelJob`), owner single-target Trash `404`, pre-existing trashed document exclusion readability, and cross-principal non-coalescing vs same-principal coalescing | `go tool ginkgo --race --fail-on-empty --fail-on-pending --no-color --label-filter=integration ./internal/adapters/http/scans` | `pass` (`2/2` specs after sorting fake `ProjectDocumentsAll` by `document_id`) | [handler.go](../../internal/adapters/http/scans/handler.go), [handler_test.go](../../internal/adapters/http/scans/handler_test.go), [scope.go](../../internal/application/auth/scope.go) |
| Public CLI/API scan, normal directory/symlink/binary import, `files`-source A/B captures, and 40-page/83-unit mid-scan `SIGKILL` restart recovery | `tests/e2e/test_scans.py` (`2` pytest E2E cases) | Public `haeryong scan --json` on a normally imported package containing `index.html`, subdirectory `sub`, unpreviewable binary `sub/raw.bin`, and symlink `sub/alias.bin -> ../outside-dangling-target.bin` (`8` units: `7` checked, `1` skipped `no_source_declared`, `0` failed, `0` blocked), lost-202 caller journal recovery, `haeryong findings list --job`, single-target Trash/Restore visibility, `files`-source (`selected_snapshot`) captures A/B with mutated second file (`beta.txt`), and 40-page (`83`-unit: `1` version + `40` pages + `40` entries + `1` source unit + `1` index, `0` declared regions) mid-scan `SIGKILL` during positive running progress (validated against `scan_report` contract schema in nonterminal `inventory`/`inspect`/`repair` and terminal `complete` stages) followed by normal restart, transient `interrupted` recovery to `completed`, increased `attempt_generation`, unchanged denominator, and exact `82` checked / `1` skipped (`no_source_declared`) / `0` failed / `0` blocked | `.venv/bin/python -B -m pytest --strict-config --strict-markers -W error -x tests/e2e/test_scans.py` | `pass` (`2/2` pytest cases) | [test_scans.py](../../tests/e2e/test_scans.py), [scan_cli.go](../../cmd/haeryong/scan_cli.go) |
| Populated schema 19 -> 20 upgrade & baseline observation | Disposable profile initialized with revision 19 binary, upgraded with revision 20 binary | Revision 19 imports `index.html`, `sub/page.html`, and directory `sub`, returns `503 FEATURE_NOT_READY` on `POST /api/v1/scans` and CLI `UNAVAILABLE` on `haeryong scan`; revision 20 starts the populated profile, preserves exact directory/file listing and file bytes, completes public scan and findings read, and cleans up disposable profile | Populated schema 19 -> 20 migration check via Gaori | `pass` (exit `0`, disposable products closed and removed) | [scan_schema.go](../../internal/adapters/sqlite/scan_schema.go), [schema.go](../../internal/adapters/sqlite/schema.go), [scan.go](../../internal/adapters/sqlite/scan.go) |
| Full-root static analysis (`go vet`) | Root Go module (`./...`) | Zero import cycles or static diagnostics after centralizing `domain.RestrictCredentialScope` and updating symlink entry inspection | `go vet ./...` | `pass` (exit `0`) | [scan.go](../../internal/domain/scan.go), [scope.go](../../internal/application/auth/scope.go), [scan.go](../../internal/adapters/sqlite/scan.go) |
| Contract schema (`scan_report` stages) and retained frontend bundle, webassets, CLI unit, and desktop facade checks | `tests/contracts`, `frontend/`, `internal/webassets`, `cmd/haeryong`, `desktop/readerfacade` | Contract schema validation (`scan_report.properties.stage` aligned to `inventory`/`inspect`/`repair`/`complete`, rejecting `structural`/`semantic`, plus manifest parity), frontend `tsc`/Node/bundle check, embedded asset manifest parity, `SCLI01` CLI unit spec, and `TestReaderTask0801ScanStartAndJobFindingsList` desktop facade check | `.venv/bin/python -B tests/contracts/run.py`, `python3 -B frontend/check_build.py`, `ginkgo ./internal/webassets`, `ginkgo --focus=SCLI01 ./cmd/haeryong`, `go test -C desktop -race ./readerfacade -run TestReaderTask0801ScanStartAndJobFindingsList` | `pass` (refreshed contract suite passed; frontend/webassets/CLI/desktop retained on verified unchanged input digests) | [schema.json](../../tests/contracts/schema.json), [test_provider_contracts.py](../../tests/contracts/test_provider_contracts.py), [check_build.py](../../frontend/check_build.py), [scan_test.go](../../cmd/haeryong/scan_test.go), [generation.go](../../desktop/readerfacade/generation.go) |
| Complete root gate (`make test KEEP_GOING=1`) | Full root repository (`prepare`, Go `unit`/`integration`/`persistence`, Python `unit`/`integration`/`e2e`) | Zero failures across all canonical layers | `make test KEEP_GOING=1` | Corrected frozen candidate: native Make/Gaori exit `0`, prepare pass, Go unit `184/184`, integration `100/100`, persistence `1106/1106`; Python unit `96/96`, integration `65/65`, E2E `95/95`; zero required failures/skips. Enclosing guard exit `1` for the separately settled CO non-input bytecode cache (see qualification below). Initial candidate remains `fail`/exit `2`: one SC10 persistence and three disposable documentation-fixture failures; all other layers passed. | [test_validate_docs.py](test_validate_docs.py), [search_migration_test.go](../../internal/adapters/sqlite/search_migration_test.go) |

### Retained historical failures, diagnoses and qualification limits

- **Fixture and DTO mismatches corrected before final pass**: Earlier persistence and E2E test revisions failed compilation or assertions due to calling a nonexistent `UpsertFinding`/`CreateFinding` helper instead of `tx.PutFinding(domain.Finding) error`, querying `source_version_id` instead of `from_version_id` on `authored_edges` / `authored_references`, reading `denominator` instead of `result.captured` on the public job projection, checking `entry_type` instead of `kind` on version file listings, asserting `captured.sources == 1` instead of `0` on a version with no declared sources (where the undeclared placeholder unit counts toward `total_units`), and submitting two queued equivalent requests that legitimately coalesced before separating them into a completed historical scan and a queued scan.
- **Worker resumed final-region boundary fix**: In `internal/application/scan/worker.go`, when a version's last region unit was materialized on a batch boundary and resumed in the next batch with `cv.EnumAfterPath != "" && cv.EnumAfterRegion != ""`, finishing the remaining regions on that last page reached `cv.MaterializedUnits >= pEnd` and previously fell through into the outer page loop, which queried the next page past the end of the version and recorded a false integrity error. Adding `if cv.MaterializedUnits >= pEnd || budget <= 0 { break }` immediately after completing the resumed page's regions resolved the defect.
- **Valid symlink entry-kind inspection and closed `scan_report.stage` schema alignment**: During CO substantive review, `inspectEntryItem` in `internal/application/scan/worker_inspect.go` was found to special-case only `directory` and call `tx.Object(entry.Hash)` on `symlink` entries (whose `content_hash` is `NULL` by `schema.go:17` and whose link target is stored in `exact_link_text`), causing a false `content_integrity_failed` on valid imported symlinks. `worker.go` and `worker_inspect.go` were updated to validate `domain.InventoryLine(entry)` and verify `directory`, `symlink` (preserving `link_text` without reading an empty object hash or following external/dangling targets), and object-backed `file` entries distinctly. Simultaneously, `tests/contracts/schema.json` `$defs.scan_report.properties.stage` was corrected from `["inventory", "structural", "semantic", "complete"]` to actual `["inventory", "inspect", "repair", "complete"]`, with contract and E2E validation of both nonterminal running and completed scan reports.
- **Multi-batch E2E crash/restart fixture sizing and `interrupted` polling**: An initial 300-file E2E import fixture hit the existing 30-second CLI harness timeout during result import before scan admission. Sizing the crash/restart fixture to 40 ordinary HTML pages (`83` typed units: `1` version + `40` pages + `40` entries + `1` source unit + `1` index, with `0` declared regions) still exceeds the 64-unit worker batch limit and proves positive mid-scan progress, `SIGKILL`, and post-restart continuation without establishing a 300-file pass or imposing a product content cap. Because the shared `Product.job` E2E helper treats transient startup `interrupted` state as terminal failure, `tests/e2e/test_scans.py` uses scenario-local bounded public `GET /api/v1/jobs/{id}` polling through `interrupted`/`queued`/`running` until `completed` without altering global helper or production startup recovery semantics.
- **Import-cycle cleanup and fake-store ordering fix**: Importing `internal/application/auth` from `internal/adapters/sqlite/scan.go` caused a `go vet ./...` import cycle (`auth -> foundation -> sqlite -> auth`). Centralizing `domain.RestrictCredentialScope` in `internal/domain/scan.go` (with `auth.RestrictScope` delegating to it) resolved the cycle cleanly. On the immediately following run, `internal/adapters/http/scans` failed (`1` passed, `1` failed at same-principal equivalent coalescing) because the test's in-memory `fakeScanTx.ProjectDocumentsAll` iterated an unsorted Go map while real SQLite orders documents by `document_id COLLATE BINARY` (which determines `VersionOrdinal` in `ScopeHash`). Sorting `fakeScanTx.ProjectDocumentsAll` by `document_id` aligned the fake with the real storage port, and both `internal/adapters/http/scans` and full-root `go vet ./...` passed.
- **Wall-time budget accounting and non-adopted downtime exclusion**: `max_run_seconds` is enforced against elapsed wall time from first `ClaimJob` (`StartedAt`), excluding only explicit protected-restore hold intervals (`restore_job_hold`). A preliminary suggestion to exclude general daemon downtime from `max_run_seconds` was rejected as unsupported by the approved first-claim deadline and explicit restore-hold contract.
- **Initial complete `make test KEEP_GOING=1` fixture failures and applied test-fixture corrections**: The first full root common run exited `2` while `prepare`, Go unit (`184/184`), Go integration (`100/100`), Python unit (`96/96`), and Python E2E (`95/95`) passed. Two test-fixture regressions accounted for the failures: (1) `TestValidatorAdmission._documents` in `docs/testing/test_validate_docs.py` copied only `('docs', 'spikes', 'tests', 'skills')` plus `('TESTING.md', 'frontend/README.md')` into its disposable `tmp_path` checkout, omitting repository-local files outside those roots linked from `docs/testing/scan-inventory.md` and causing `3` of `6` pytest integration cases to report `broken_link`; and (2) `searchPriorRows` in `internal/adapters/sqlite/search_migration_test.go` omitted the four revision-20 additive scan tables (`'scan_runs','scan_captured_versions','job_items','findings'`) from its `sqlite_master` `NOT IN` exclusion list (unlike `referencePriorRows` in `internal/adapters/sqlite/reference_migration_test.go` and `assertEmptyProviderMigrationTables`), causing `1` of `1106` Go persistence specs (`SC10 schema8 recovery and postings`) to fail at `search_migration_test.go:280` after retry-upgrade on the first `BeforeSnapshotSync` branch (with truncated map output and remaining fault branches unreached in that run). `docs/testing/test_validate_docs.py` now copies repository-local regular files linked from `docs/**/*.md` into the disposable fixture while preserving all `6` admission cases, and `searchPriorRows` now excludes the four additive scan tables while preserving `assertEmptyProviderMigrationTables`, all `4` `SC10` fault branches, and all prior-row/object/two-project equalities. Focused verification passed all `6` documentation admission cases and all `3` selected SC10 specs, including all four fault branches (`snapshot_sync`, `snapshot_object_copy`, `snapshot_object_sync`, `migration_commit`). The complete replacement common passed all native layers and the counts above. During that run, a separate CO identity-inspection helper imported the unchanged frontend checker without `-B`, creating one non-input bytecode cache outside frontend source/assets and explicit Go/Python test selections. The enclosing input guard therefore exited `1` despite native Make/Gaori exit `0`. CO retained the exact owned cache privately, removed only that cache and its empty new parent, and verified the complete original Git-visible input set unchanged. All affected assessors accepted this qualified settlement; the original guard failure is retained and no product test was replayed for it.

## Knowledge and user evidence, when applicable

`TASK-008-01` is the deterministic inventory, structural inspection, and reference-index repair foundation for `EPIC-008`. It performs no LLM/provider inference and never marks pages or regions semantically verified (`semantic.status` remains `not_assessed` or `unable_to_verify`). Semantic relationship/topic discovery (`TASK-008-02`), region-scoped freshness assessment (`TASK-008-03`), and the labeled [knowledge-quality worksheet](knowledge-quality.md) gate (`TASK-008-06`) remain with their ordered owners.

## Contract, state and recovery effects

- **SQLite schema revision 20**: Migration 20 (`internal/adapters/sqlite/scan_schema.go`, wired by `internal/adapters/sqlite/schema.go`) adds `scan_runs`, `scan_captured_versions`, `job_items`, and `findings` plus `scan_runs_by_scope_hash`, `scan_captured_versions_by_job_doc`, `job_items_by_job_state_ordinal`, `job_items_by_document`, `job_items_by_version`, `job_items_by_repair_job`, `findings_by_job_order`, `findings_by_version_status`, `findings_by_project_status`, and `findings_by_fingerprint`. Populated revision 19 profiles migrate in place to revision 20 while preserving all existing versions, entries, objects, and personal records.
- **HTTP, CLI, and native/desktop contracts**:
  - `POST /api/v1/scans` (keyed by `Idempotency-Key`, owner or derived workflow credential with `scan.request`) admits or coalesces a `scan` job (`202 Accepted`) and records an operation receipt. Premature `finding_id` / `expected_finding_revision` fields return `503 FEATURE_NOT_READY`.
  - `GET/HEAD /api/v1/jobs/{id}/findings` (owner-only, optional `kind`, `status`, `limit`, `cursor`) returns a deterministic finding page with a strong SHA-256 `ETag` and `304 Not Modified` support.
  - CLI `haeryong findings approve|reject` exits `5` (`UNAVAILABLE` envelope code with `FEATURE_NOT_READY` text); finding decision mutations remain owned by `TASK-008-05`.
  - `haeryong scan` persists a caller journal entry (`operation_id`, `runtime_generation`, `library_id`, `principal_id`, canonical request body) before dispatch and recovers via `GET /api/v1/operations/{id}` on a lost `202` acknowledgement before resending under the same runtime generation.
  - Shared browser/native clients and `desktop/readerfacade` expose closed `scanStart` and `jobFindingsList` operations gated by `capabilities.features.scans`.
- **Process crash, restore hold, and lifecycle recovery**: Interrupted `running` scan jobs recover through startup `recoverLocalJobs` (marking the job `interrupted`, advancing `attempt_generation`, resetting `running` `job_items` to `pending`, and resuming from the last committed 64-unit batch cursor without duplicating items or findings). Protected offline restore places active scans in `restore_job_hold` and reconciles restored targets before resuming. Document Trash and manual purge transactionally transition unfinished items (`document_trashed` / `document_purged`), mark proposed findings `stale` or `blocked`, scrub private paths/regions/source selections/payloads (`{"purged":true}`) on purge while retaining minimal opaque identifiers and immutable denominator counts, and refresh `ScanRun.Report` while preserving healthy sibling targets.

## Review and handoff

- **Completed in this task**: Schema revision 20 storage and migration, scan admission and policy snapshot narrowing, pre-dispatch source target resolution and object pinning, 64-unit incremental `job_items` enumeration and deterministic structure inspector (including valid `file`/`directory`/`symlink` entry inspection), owned `derived_index` (`references`) repair child fencing, closed `scan_report.stage` schema and coverage/finding projections across HTTP/CLI/frontend/desktop, source-audit and CO substantive review reconciliations, the focused task-local unit, persistence, HTTP, E2E, migration, contract, and `go vet` verification set, the first independent result/EE panel round (which completed negatively on the initial candidate), and the two test-fixture corrections (`docs/testing/test_validate_docs.py` and `internal/adapters/sqlite/search_migration_test.go`) after the initial `make test KEEP_GOING=1` run. Corrected fixture checks, populated revision-19 public upgrade observation, current desktop facade observation, unchanged-input frontend evidence reuse, corrected complete common, independent affected result/Epic-contribution assessments, eligible original-finding closures and current composition are complete. CO accepts this exact bounded implementation/test basis and the separately verified record-only closeout overlay under Master delegation.
- **Remaining for task closeout and next task**: The normal completion commit and native finalization precede [TASK-008-02](../tasks/EPIC-008/TASK-008-02.md) (`Implement bounded relationship/topic candidate discovery`). Later semantic, overlay, approval/undo and assembled quality gates remain with their ordered owners. This member acceptance is not Master overall Epic acceptance or physical-device/Wails GUI qualification.

## Bounded relationship and topic candidate discovery additions (TASK-008-02)

`TASK-008-02` extends the scan worker and SQLite persistence from schema
revision 20 to revision 21 (`maintenance_intents` table and
`maintenance_intents_by_scan_item` index in
`internal/adapters/sqlite/scan_schema.go`). Source-context dependencies use a
query-local CTE in `internal/adapters/sqlite/source_capture.go`:

- **Candidate retrieval and pre-call excerpt inspection (`B05`, `B06`, `QR2`, `QR4`)**: During
  `inspect`, each structurally checked active semantic unit (`region`, or `page`
  when that page has no regions even on a mixed version (`B05`), or `version`
  when pageless) runs `shortlistCandidates` (region-scoped `ScanSearchText` with
  `page_anchors.label` headings + region-scoped SQL `ScanCandidateUnits` +
  effective parent-EOF body readiness + deterministic discriminative content
  ranking over at most 64 candidates, with canonical-stem deduplication and
  same-version body-redundancy damping, capped at `maxShortlistCandidates = 6`
  without fixed cross-version slots, `B06`, `QR2`, `QR4`) and inspects actual
  HTML/region excerpts before any provider call. The report's
  `inspected_candidate_relations` is populated from applied item detail and
  completed-child shortlist totals; it is not a pre-call live counter.
- **Child `job.kind = "maintenance"` lifecycle, per-version capture bindings, and budget/restore preservation (`B01`, `B04`, `B08`, `B09`, `B10`)**:
  `stepUnitSemanticEvaluation` groups source capture steps per exact
  `(ProjectID, VersionID)` so every `MaintenanceReadBinding` preserves a
  non-empty `VersionID` and version-specific source revisions;
  `CaptureMaintenanceAndAdmit` preserves `ReadBindings` across multi-step
  captures and binds `current`-mode Git captures to the scan-start
  `ResolvedRevision` while admission-unavailable `files` targets fail closed
  without live fallback (`B01`). `SemanticRequestSHA` normalizes maintenance
  prompt whitespace (`normalizeMaintenancePrompt`) and breaks ties on
  `MaintenanceExplanation` coordinates deterministically (`B04`).
  `exhaustRunBudget` and `revokeRunAuthority` check
  `curJob.ProviderAttempts.Unknown > 0` (`B08`), `checkScanResumeBudget`
  enforces `effectiveScanMaxProviderAttempts` on `decision == "retry"` (`B09`),
  and `copy_restore.go` orders restored jobs by `ORDER BY rowid,job_id` so
  parent `scan` jobs precede child `maintenance` jobs (`B10`).
- **Deterministic post-model verification (`T-008-02-01..03`, `B02`, `B03`, `B07`, `B12`, `QR1`..`QR5`) and schema 20 -> 21 migration**:
  `PrepareMaintenance` and `ApplyMaintenance` in
  `internal/application/scan/candidates.go` enforce unit-scoped source evidence
  (`VerifiedEvidenceByUnit`, `B02`) and verified module/repository directional
  import checks for `depends_on` / `source_fact` with qualified rationale
  withdrawal on downgraded claims (`T-008-02-01`, `B12`), four-boundary
  Trash/authority filtering, `$.origin_unit` and `evidence_json`
  lifecycle/purge/pagination fencing (`B03`, `B07`), exact
  `(VersionID, PagePath, RegionID)` coordinate containment (`T-008-02-02`),
  distinct same-pair mechanism fingerprinting (`mechanism_key`) vs
  true-equivalent deduplication (`QR2`), stem/paraphrase topic grounding and
  normalization with per-proposal `proposal_decisions` audit (`QR3`),
  focal-scope content-gap direction verification (`QR1`), `source0`
  (`uncertainty = "source_unverified"` and `no_source_declared`) qualification
  (`QR4`), and separation of relationship inspection from `TASK-008-03`
  freshness (`semantic.assessed = 0`, `semantic.complete = false`, `QR5`).
  Offline unit (`internal/application/scan`), SQLite persistence and revision
  20 -> 21 migration rollback (`internal/adapters/sqlite`), and populated
  revision 20 binary -> revision 21 binary public upgrade checks
  (`GET /api/v1/status` `schema_revision: 20` -> `21`,
  `pre-migration-*.sqlite`/`.json`/`.objects`, exact HTML byte/SHA-256
  round-trip, preserved revision 20 scan/findings, and post-upgrade
  `deep-semantic` scan + CLI `findings list`) verify these invariants while the
  live held-out provider quality cohort (`T-008-02-04`) and full canonical
  common gate are coordinated separately.

The owner-approved additional correction uses the ordinary search EOF projection,
content-discriminative candidate ranking without fixed version slots, specific
mechanism/reader-utility provider guidance, conservative focal limitation-to-gap
promotion and bounded lexical topic-paraphrase grounding. Parent scans remain
active during ordinary child waiting. Explicit unresolved/saved-result or input
holds pause the parent and snapshot elapsed time; owner resume retains its
budget checks. The earlier source-qualified checks above predate this correction.
Its affected offline checks passed as qualified below; the actual quality gate
failed and the full common remains unexecuted on the latest correction.

The original quality acquisition failed (useful cases 1/4, required focal gaps
0/2; page-only input scope). The first repaired 24-region acquisition also failed
(useful cases 2/4 against a 3/4 requirement, required gaps 1/2, and 32 unhelpful
cross-project relations against the predeclared allowance of at most one
unhelpful thematic extra). Its 24 saved attempts and natural service exit were
acquisition facts, not a quality pass. Both corresponding common executions were
cancelled with native `killed`/130; neither proves the aggregate gate. Master
approved one additional bounded correction and original-set quality experiment
under unchanged criteria. No Task02 acceptance or current common pass is claimed.

The approved additional original-set experiment also failed: useful cases 1/4
(required 3/4), focal gaps 2/2, uncertainty cases 2/2 and one unhelpful extra
(within the original allowance). All 24 attempts saved results, all children
completed, all 277 findings were independently graded, and the service exited
naturally with unchanged recorded inputs. Three required useful pairs were
never shortlisted. The completed experiment is a pre-qualifier-fix observation;
its failure and original target remain preserved.

A later finite correction rejects opposite-polarity topic qualifiers before both
early and paraphrase acceptance, while preserving genuinely described negative
qualifiers and valid topics. Current race unit, controlled SQLite QR and build
checks passed for that correction; a missing test import and its initial compile
failure remain in the local execution record. The original finding owner
independently confirmed the bounded correction after inspecting current source
and negative/positive cases and applying those check receipts. This changes topic
post-processing, not the candidate retrieval that missed those three pairs.
At that checkpoint, the full common was not started after the failed quality
result, Task02 remained unaccepted, and no fourth provider cohort or later
dependent Task had started.

Master subsequently approved the bounded two-stage semantic candidate nomination
design amendment (`Mode = "semantic_nomination"`, Pass-1 `structural_ready`
readiness ordering across all semantic leaves before global nomination,
deterministic `nomination:p{pass}:b{index}` batching initially at the historical
`<= 2048`-byte projected-card / `900`-byte excerpt checkpoint within the
`<= 144 KiB` frame budget, strict closed-schema output
validation requiring exact per-card cardinality (`minItems == maxItems ==
len(cards)`) and required non-blank `abstain_reason` when `abstained == true`
versus omitted/empty `""` when `abstained == false`, strict
`AuthorizeMaintenance` prompt/digest/projected-card equality and pending/running
batch-item guards, `unverified_card_routing_hint: ` signal labeling in the
shared `K <= 6` Stage-2 shortlist, `PV01` saved-result continuation, and
`N4`/`PV02` minimal purge tombstones with result-object unpinning) and one new
`<= 25`-attempt live provider quality experiment under unchanged criteria.
Focused race evidence covered that historical checkpoint across the
nomination/card, actual adapter-frame and
SQLite worker/guard/continuation/purge boundaries; a finite source-contract
correction aligned exact focal-row cardinality, `abstain_reason` rules, unverified
routing-hint signal labeling and contract documentation. The affected checks
retain their source qualifications and failed parent runs. A later saved-result
continuation defect was corrected by queuing the parent before resuming its
children in the existing atomic write transaction; independent source-delta
assessments and affected race checks cover that correction. The malformed-output
fixture isolates each actual request in its own authenticated disposable
store without bypassing unresolved-result identity guards. The two final
fixture checks passed; earlier failures remain historical evidence.

The one authorized semantic-nomination experiment completed on that historical
`<= 2048`-byte / `900`-byte card candidate: one nomination attempt and 24 regular evaluation
attempts were dispatched and saved, all 25 maintenance children completed, and
286 proposed findings were retained. All 25 saved public result hashes and byte
counts match the durable postmortem. The scan inspected 144 candidate relations
across two projects, two versions, four pages and 24 regions, with no declared
project sources. All transports recorded completed results and positive owned
process drain; six retained `signal_error_drained` reasons qualify cleanup and
must not be reported as error-free transport cleanup. The daemon exited `0`
naturally and recorded runtime inputs were unchanged. These acquisition facts
are not a reader-quality pass. Independent grading of all 24 regular outputs
and all 286 findings under the unchanged original 12-case rule returned `fail`:
useful relations were 2/4 against the required 3/4, and three non-actionable
limitation-derived gap proposals exceeded the allowance of one unhelpful extra.
Safety, negative controls, both focal gaps and both uncertainty cases passed
within their recorded scope. Two required useful pairs were neither nominated
nor recovered by lexical fill. All 24 nomination cards were truncated at `900 B`; this
identifies a coverage limitation without proving that a larger excerpt would
have produced the missing relations. Three ordinary limitation/abstention notes
were incorrectly promoted into proposed gaps by the prose-based promotion path.

Master then approved a bounded two-correction amendment and one additional
`<= 25`-attempt original-set quality experiment under unchanged criteria:
full-fit-first adaptive Stage-1 card projection up to `<= 8192` raw and
JSON-encoded bytes per card within the unchanged `<= 144 KiB` (`8 KiB` fixed
reserve) nomination frame without an average per-card quota (using deterministic
UTF-8 `60%/40%` head/tail truncation from the unspliced original excerpt only
when a card exceeds `8192` raw/encoded bytes or when the batch exceeds the
`<= 144 KiB` frame budget with `8 KiB` reserve), and removal of
`extractFocalGuaranteeGapFromLimitation` so `limitations` remain
uncertainty/abstention records only while explicit `content_gaps` and declared
missing scope themes (`evaluateTopicFocalGrounding`) remain the sole content-gap
sources. All five affected focused scopes passed by exact-input composition.
The additional cohort completed `25` dispatched/saved attempts and `25` completed,
positively drained children, with all `24` cards full (`54,096/54,096` body bytes).
Its `290` proposals (`152` relationships, `106` topics, `32` typed gaps) and all
saved regular public outputs received independent original-profile grading.
The unchanged original-set quality component passed; the
[quality record](knowledge-quality.md) preserves the qualified useful case, two
accepted borderline analogies, nonretrieved negatives, three
`signal_error_drained` cleanups, zero project-source inputs and all historical
failures.

The subsequent full common failed with exit `2` on two stale contract digest
records and an invalid CLI test status expectation. Integration, persistence and
all `95` E2E cases passed. The aggregate remains failed; only the two metadata
rows and test literals/negative cases changed afterward, with no runtime change.
The corrected CLI race case and current prepare stage passed. Independent final
original-Goal/scoped contribution judgments, all `21` original issue settlements,
current composition and CO product review establish the bounded delivery under
delegated member acceptance. The exact applicable layers and affected cases cover
`1,407` Go and `256` Python cases, without claiming a fresh aggregate pass.
All five native Goal criteria are satisfied. This factual/lifecycle overlay has
separate affected documentation checks; the normal commit and native finalization
precede Task03.
Semantic freshness remains with TASK-008-03; `semantic.assessed = 0` and
`semantic.complete = false` are preserved.

## Task03 freshness continuation

The independent freshness stage now follows the existing scan inventory and
candidate stages. It fixes complete required membership and schedules one exact
unit at a time against a shared captured target. Semantic work counts are distinct
from inspected relationships and from published applicable contributions; a
propose-only result changes neither applicable freshness nor source triggers.
The [freshness record](region-freshness.md) owns current verification and the
[quality declaration](knowledge-quality.md#task-008-03-frozen-predeclaration)
owns expected outcomes. Earlier Task01/02 receipts and their original source0,
cleanup, model and quality qualifications remain unchanged.
