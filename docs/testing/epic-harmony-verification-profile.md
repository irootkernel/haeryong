# EPIC-005 verification profile — vp-6

Applies to the current EPIC-005 implementation and completion checks.
TESTING.md remains the root command authority. This profile records the selected
EPIC-005 verification and grants no waiver or extra execution authority.
The assessed selection is implemented by the native handlers. This document defines
selection, not a passing execution or acceptance claim.

```yaml
verification_profile:
  revision: vp-6
  tests:
    prepare: make test-prepare KEEP_GOING=1
    unit: make test-unit KEEP_GOING=1
    integration: make test-int KEEP_GOING=1
    persistence: make test-persistence KEEP_GOING=1
    e2e: .venv/bin/python -B tests/run_checks.py e2e --keep-going
    aggregate: make test KEEP_GOING=1
    frontend: .venv/bin/python -B frontend/check_build.py
    desktop: >-
      sh -c 'export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off; go -C desktop test -race -count=1 ./...; t=$?; go -C desktop vet ./...; v=$?; echo "desktop test exit=$t vet exit=$v"; [ $t -eq 0 ] && [ $v -eq 0 ]' 
    completion_suite: [aggregate, frontend, desktop]
    document_checks: python3 -B docs/testing/validate_docs.py
    default_environment: >-
      Native Darwin ARM64 host; exact go.mod toolchain, GOTOOLCHAIN=local,
      GOPROXY=off, GOSUMDB=off, GOFLAGS=-mod=readonly. Prepared .venv Python
      (observed 3.14.7), pytest 9.1.1 and PyYAML 6.0.3. Root handlers set
      native GOOS/GOARCH, CGO_ENABLED=1, disable ambient pytest plugins and
      reject PYTEST_ADDOPTS; use private physical-path profiles/ports/artifacts.
      Frontend uses existing frontend/node_modules and installed Node;
      no gate installs or downloads dependencies. Record observed tools and
      uncaptured inputs per execution. Same environment for Writer checks/gates.
    keep_going: >-
      KEEP_GOING=1 causes the five
      existing root stages to run serially once, without hiding failures;
      leaf --keep-going omits pytest -x, adds Ginkgo --keep-going, attempts
      Python after a Go failure and independent prepare checks after failure.
      Formatting is checked without rewriting in this mode. Each child outcome
      remains in private artifacts; any failure yields nonzero. Cancellation
      and unsafe/unavailable global prerequisites are explicit stops, not passes.
      Leaf control exits are 0 success, 1 completed check failure, 2 fatal setup,
      and 128+signal cancellation. Raw child exits/signals/timeouts stay separate
      in per-command status JSON. Default unset/0 remains fail-fast, including
      its existing gofmt rewriting; opted-in prepare checks formatting only.
      Record and complete any still-unexecuted mandatory cases on the same
      frozen candidate before corrections. Existing runtime adapter is only
      historical/baseline evidence, not the post-change native gate.
    delta_gate:
      always: [root formatting check without rewrite, Python syntax and layer checks, go mod verify, go vet ./..., dependency boundary, four headless cross-builds, docs and contract digests, frontend validation and native isolation build, desktop race and vet]
      changed_code: >-
        Identify every changed file's tests and direct/transitive consumers
        from actual imports, wire operations, shared fixtures and layer/driver
        selections. Run all consumers plus C_g failures and repository-required
        checks; uncertainty or any H full-suite invalidator requires whole suite.
    opt_in_campaigns:
      - scope: TASK-005-05 Chrome backup/lifecycle driver with keyboard and emulated touch; native parity/build checks
        required_for_task: true
        load_sensitive: true
      - scope: TASK-005-06 two-project production owner records, browser/native lifecycle/recovery and export/reimport
        required_for_task: true
        load_sensitive: true
      - scope: real-sample search/reference worksheets and other manual labels
        required_for_task: only if canonical Task or inspected changed behavior requires them
        load_sensitive: true
  verification_authorities: [Makefile, tests/run_checks.py, conftest.py, tests/unit/test_orchestration.py, TESTING.md, frontend/check_build.py]
  shared_test_resources:
    worktree: One root stage or aggregate per checkout; Ginkgo writes transient package artifacts. Freeze candidate or use Coordinator-created snapshots; no concurrent candidate-modifying commands.
    drivers: Serialize browser/native drivers and heavy gates unless explicit resource isolation is established. Never overlap a load-sensitive campaign and whole suite/heavy Writer work.
    artifacts: Private per-execution physical paths; preserve pre-existing roots and only clean owned artifacts. Coordinator owns snapshot/Git creation; SOs never modify Git or shared repository.
  candidate_id:
    method: >-
      Temporary-index tree hash outside worktree, exported for every command in
      one isolated subshell: git read-tree HEAD; git add -A -- .
      ':(exclude).mulgae' ':(exclude).podway' ':(exclude)*.test'
      ':(exclude)*/Volumes/RootKernel/tmp/haeryong-tests-*'; git write-tree.
      Chain each step only on success. Preserve existing HEAD entries for
      excluded paths; remove only the owned temporary index. Never use real index.
    fallback: HEAD plus SHA-256 of staged/unstaged binary patch and sorted nonignored untracked path/mode/hashes.
    uncaptured_inputs: [prepared .venv and frontend/node_modules, Go compiler and caches, Chrome/playwright-cli and native harness environment, Gaori configuration, read-only real sample sources when selected, host resources/load]
  contracts: [docs/contracts, selected tests/contracts schema and digest manifest, frontend client transport and native-wire shapes, desktop facade operation parity, capability flags and CLI error registry]
  risk_paths:
    shared_owner_records: Transactional authority and receipt replay in storage.ExecuteNormalized/Replay, auth Authenticate/AuthenticateWrite and personal Review/Note/Revisit writes; exact anchors/history/current visibility.
    remembered_configuration: configuration validation/plan/commit and source eligibility; restore hold cannot be bypassed by another configuration path.
    trusted_delivery: Authenticated HTTP routing and renderer/session revocation, narrow native transport/facade operations and the generated trusted shell that consumes them; no host DOM/token/native authority reaches imported content.
    snapshot_and_pins: SnapshotDatabase and InstallCopyPins plus copy capture/closure/finalization and Collect handoff in sqlite/foundation; persisted owner sets and GC barriers.
    restore_authority: PlanRestore/applyRestore, ProtectRestoredDatabase, ReviewRecovery, OpenRecoveryWriter and recovery-switch/profile identity; token/epoch/policy holds and uncertain operation protection.
    local_authorization: localcontrol copyRequestProof/copyRequest/CopyCall and recovery.Validate/Submit; closed request, bearer/profile proof, receipt/replay and destination authority.
    lifecycle_concurrency: purge reservation/generation/pin guards, source eligibility, dispatcher start/recovery admission and restored destructive cleanup holds.
    schema: Append-only migration 14 and reachable declaration validation through revisions 8..14, startup validation and old-database recovery.
    proofs_and_output: Backup/export manifest verification, scope and payload roots, status publication and complete/verified/durable claims consumed by clients.
  normative_docs: [docs/decisions.md, docs/requirements.md, docs/architecture.md, docs/data-model.md, docs/execution-protocol.md, docs/contracts, docs/testing/contract-cases.md, docs/testing/acceptance-catalog.md, docs/testing/knowledge-quality.md, docs/operations, docs/ui, docs/epics, docs/tasks, docs/MANIFEST.md, TESTING.md, AGENTS.md]
  scenario_map:
    review_notes: Exact Review and Notes/history/anchors/context, Revisit and saved reader state across restart/restore.
    backup_restore_export: Consistent snapshot/transitive ownership, interruption/finalization, epoch/token invalidation, policy review, exact directory and metadata fidelity.
    lifecycle: Archive/trash/purge/restore with pinned/shared/source object survival, search/references/backlinks exclusion and terminal ID reservations.
  units:
    module: Go package; Python module/package; frontend top-level source directory
    area: Same module, behavior, contract, defect class or lineage
  mechanical_checks: {prose: unavailable, expect: unavailable}
  evidence_reuse:
    applicability_sources: [actual imports and go list -deps, root layer and pytest selection, frontend input digest/test registration, contract manifest, original runner-produced reports]
    known_gaps: [no measured coverage, dynamic consumers need manual inspection, browser/native/manual drivers outside root aggregate, Gaori generic extraction does not alone prove test counts]
  calibration: {size_default: {production_files: 3, production_lines: 50}, basis: canonical defaults, approval: none required for unchanged defaults}
  operating_budget: {warning: null, hard_limit: null}
  known_limits: [physical devices and real providers not covered by fixtures, native build/parity not equivalent to actual Wails interaction, AMD64 compilation not runtime evidence, unresolved original failure records retained]
```

Baseline, execution receipts, applicability and configuration observations remain in
the private Context Brief/run records. Runtime paths/identities are not durable tracked
evidence. Historical failed runs retain their outcomes. Original Epic starting_head
and baseline are preserved; no allowance is inferred from missing records.
