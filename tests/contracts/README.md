# Selected executable contract baseline

This directory is the one machine field authority for the selected TASK-001-06
initial contract baseline, recorded in bounded G-CONTRACT `pass`. `schema.json` supplies closed typed shapes, shared enum
vocabulary and HTTP error mappings; `config-default.json` supplies the selected
configuration example. The semantic Markdown registry delegates these fields
here. `contract.py` validates those same definitions for package, request,
configuration and fixture-client/model representations. It intentionally rejects
unsupported JSON Schema keywords rather than silently weakening validation.
It is a small fixture interpreter, not a general JSON Schema implementation.

`cases.json` preserves all24 original C1–C5 expected results, five supplemental
groups, eleven authority/implementation owners and nineteen future UI actions.
`test_contracts.py` executes each declared row/group through actual data/state
assertions. `run.py` verifies coverage and retains a structured result and source
hashes at an explicitly supplied new private absolute report path:

```sh
python3 -B tests/contracts/run.py --report /absolute/private/new-contract-results.json
```

Python3 and existing PyYAML are required; no package installation is needed.
The [readiness record](../../docs/testing/contract-freeze.md) gives actual results
and current acceptance state. These tests never start a listener or call AI.

## Concrete selected choices

- Requests/configuration and structural metadata use closed fields; extensible
  authored display/payload bytes remain ordinary content. Missing is distinct
  from explicit null. Integers reject booleans/fractions; byte sizes are canonical
  nonnegative decimal strings. Nullable objects use an explicit object-or-null
  union, never an empty-object sentinel. Unknown preview extensions remain files.
- An empty upload checkpoint is generation1, SHA256 of empty bytes, accepted
  bytes `"0"` and `complete_input:false`. Equality is all four typed fields;
  equal-checkpoint new seal keys link the one job. Checkpoint representation is a
  contract fixture, not a claim about a production transport's inventory storage.
- Principal-wide lowercase unquoted UUID operation keys identify a request;
  upload-create U, seal S and final I are distinct. Future consumer Q in an object
  target is semantic data, not U's receipt identity. Route plus validated semantic
  body are deterministically UTF8 JSON serialized with sorted keys, no whitespace,
  finite values and SHA256. Equal payload/metadata through another upload is
  equivalent; changed caller title, project, base, IDs or metadata conflicts.
- Import requires `document_id`; null creates a document. The first valid
  reservation captures project/document/version/base, generated time and title,
  schema/normalizer version and original omission markers once. A transport ID
  cannot replace semantic identity. Tests use a fixed explicit fixture clock;
  that clock is not a runtime timestamp policy. Explicit optional metadata
  document/project/version/base IDs must match the once-normalized request
  identities before reservation/adoption/publication; omitted IDs remain valid.
  Completed replay keeps its original result before stale defaults are reevaluated. Matching metadata references
  resolve exact sealed bytes and the bound final operation/action/target before
  adoption; inline and object inputs share the metadata shape.
- Current authorization precedes receipt replay. A completed identical receipt
  replays before stale base evaluation; different semantics conflicts. New work
  checks base before immutable publication. Wrong purpose/target or malformed
  fields fail before staging; existing explicit version with changed immutable
  bytes is `IMMUTABLE_VERSION_CONFLICT`, separate from base/fingerprint/hash
  conflicts. Natural configuration-plan IDs and aliases share that same
  principal-wide namespace; cross-family collisions fail with OPERATION_CONFLICT
  in both directions, including an alias-first commit. Error pointers identify the submitted field. Cross-field precedence
  and current authority checks are exercised, not inferred from prose matching.
- Seal cancel fences until confirmed worker stop, retaining accepted bytes.
  Abandon prevents writes/adoption/finalization while retaining active/recovery
  and adopted references. List excludes checkpoint/error/ranges; detail adds
  their exact nullable fields and cursor URLs. `pin_summary` contains nonnegative
  counts for staging, adopted, active_work and recovery, not a proof of real GC.
  Unavailable sealed context/body/metadata detail stays readable with truthful
  unavailability and a nullable sealed object reference; consumption/adoption
  still requires reusable bytes.
- Configuration validates strict JSON/YAML, duplicate keys and unsafe tags,
  anchors/aliases/merges; manual/interval/calendar fields, cron/timezone, exact
  origins and distinct renderer origins are checked together. Project overrides
  merge objects and replace arrays against inherited validated defaults.
  Configuration and source-permission revisions remain separate; impact plans
  require confirmation and stable replay. No global configuration is modified.
- Directory/ZIP/single-file fixtures preserve complete paths/bytes, implied and
  represented empty directories, symlink text, Unicode/case and hidden/unknown
  files. Canonical inventory is exact-path UTF8 order with compact JSONL arrays,
  decimal byte sizes and file hashes. Exact lookup wins; unique NFC-equivalent
  fallback is allowed, ambiguity fails. Symlink resolution stays within snapshot.
  No manifest, index, extension allowlist or content count/size/depth cap is added.
- Citation observed_input must match an actual producing-attempt observation;
  posthoc checks have their own successful exact target record. Partial/zero or
  unavailable coverage never becomes aligned. Exact prior-result references are
  explanation only, not evidence that the new source was checked. The shared
  validator also enforces their existing cross-field identity rule: a non-null
  region requires an exact page; null/null permits a whole-version reference. Unknown
  required state/authority fails safely; only optional nonauthority response
  additions are directionally ignorable. Disabled capabilities report
  `FEATURE_NOT_READY`.

Selected UTC timestamps follow [RFC3339 calendar/clock semantics](https://datatracker.ietf.org/doc/html/rfc3339#section-5.7)
and validate Gregorian dates and clock bounds with ASCII
four-digit years, including year0000, and preserve arbitrary fractional digits
without conversion or truncation. The existing uppercase T/Z representation is
retained. Announced positive leap seconds are accepted at23:59:60 only on the27
[IERS leap days](https://hpiers.obspm.fr/iers/bul/bulc/Leap_Second.dat) known
through Bulletin72 (July2026); the shared validator includes
these factual dates rather than a runtime network dependency. Existing parser
owners TASK-002-04/05 must incorporate later announcements and repeat their
regressions; no prediction or new product policy is introduced.

## Calibration and digest scope

`calibration-plan.json` is the unchanged pre-run declaration: real selected
SQLite driver/engine, synthetic workload, all40 queries, metrics and threshold
miss dispositions. `spikes/stack/calibration_test.go` executes it only when a new
private output directory and observed hardware JSON are explicitly configured.
The ordinary stack suite skips this opt-in test; its actual separate execution
is recorded in readiness. The fixture uses temporary databases and no daemon.

`manifest.json` covers exact selected machine definitions, defaults, tests,
model/interpreter, coverage matrix, predeclared calibration plan, calibration
implementation and pinned module files. It excludes itself, status documents,
this README and derived docs/MANIFEST, avoiding self-hashes and status cycles.
Its version names this fixture contract, not a product release or deployed
runtime compatibility. Digest changes require an explicit recorded contract
revision; later implementation owners repeat their actual feature checks.

Pure in-memory models, copied recovery snapshots and synthetic caller journals
are contract assertions. They are not a durable receipt ledger, crash/power-loss
proof, streamed importer, provider attempt budget enforcement, real owner UI or
production recovery. SQLite CAS/rollback is actual temporary-database evidence;
production storage/restore belongs to TASK-002-02/04/05 and EPIC-005. Real
private/browser/native/provider proof remains in the original owning gate
records and is not replaced by these fixtures.

## Whole-Epic IR1 authority correction

The first whole-Epic static review found an omitted C1-03 consumer branch after
the original29-method pass. Body `evidence_verification` creation already used
`source.read`; adoption now uses that same existing action scope, rather than
requiring owner authority. Schema1.0/APIv1 and the11-entry digest domain remain
unchanged. This is a recorded selected-model correction, not a new product
authorization policy or release.

The original C1-03 method now executes create, write, complete, settle and adopt
with the declared source/version-read authority at creation and adoption. The
existing seal model has no authority argument and remains a pure contract
abstraction. Missing source-read authority is forbidden; mismatched operation,
action, version or evidence targets produce the existing typed binding error
when the caller otherwise has that action's authority. Every rejected branch
compares the complete model state, including bytes and pins. The successful
consumer adds exactly one adopted pin and retains the exact target and bytes.
All29 current methods pass; the original passing methods and later failing
baseline are separate evidence. Only model/test digest entries changed. No
calibration, source-generation or provider execution was repeated.

## TASK-002-05 selected response extension

The existing request/config/author-metadata shapes and schema1.0/APIv1 remain unchanged. Additive current query/list/bootstrap/config-view/staging-summary definitions now select previously provisional read fields, including per-item validators and nullable cursors. The current additional pure fixture validates those shapes and rejects direct-array lists/missing validators/numeric byte sizes. Actual authenticated SQLite/HTTP and CLI query/transfer/configuration regressions live in the production test packages and are recorded in [Task05 evidence](../../docs/testing/query-cli-configuration.md); they are not replaced by this pure fixture. The existing manifest digest domain remains unchanged and its schema/test digests reflect this recorded extension.
