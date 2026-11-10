# Knowledge quality and usefulness evaluation

**Status:** Evaluation contract for G-VALUE, Stage A and maintenance gates. The bounded current [Task07 experiment](knowledge-value-spike.md) records G-VALUE `pass` for its exact released Mulgae/Gaori corpus, with structural, semantic and reader results separate. All six normative conditions, the three fresh static reviews and Judge EVIDENCE are complete; Task07 closing authority follows the execution protocol. No human usability study or general quality guarantee is claimed. Delivery and evidence clarifications follow the [1.5.0 follow-up reconciliation](../changes/2026-10-11-opus-follow-up.md). This method elaborates R05, R09, R11, R20, R24 and R40 under owner directions D-08/D-09. It evaluates source-grounded explanation quality while preserving normal JavaScript and directory-result freedom.

## What must be demonstrated

An explanation must help a reader answer a concrete question about approved source at an exact revision. A citation that resolves proves that the referenced bytes can be located; it does not prove that the bytes support the explanation. Evaluate structural validity, claim support and reader usefulness separately.

A saved explanation may remain useful when its source is unavailable. Its availability and freshness labels must honestly describe what was retained, what can be read now, and which target was actually checked. Personal Reviewed is an owner record, not a certification of factual accuracy.

This is an implementation quality evaluation for Haeryong-generated/source-verified explanations, not a semantic admission test for arbitrary imported files. A plain AI result directory can be imported and read without source metadata, declared regions or a Haeryong manifest. Its source basis stays unknown/unverified until evidence is actually established; import success is never a factual-correctness label.

## Early corpus and ownership

TASK-001-01 checks whether the intended provider/deployment is supported before expensive integration work and prepares one concrete reader question/comparison from already authorized existing material. Record expected source facts and current effort, or the exact unavailable prerequisite, plus benefit_observed, no_difference or not_available. Carry the latter two as predeclared risks into TASK-001-07; these are comparison observations, not additional gate statuses. This cheap checkpoint does not require a new model call, future UI, fixed trial duration or a G-VALUE pass. TASK-001-05 proves one real bounded cited question. TASK-001-07 then runs G-VALUE before TASK-001-06 freezes contracts.

Use two real projects within the owner's configured source and disclosure scopes; reuse existing authorization without per-read approval. The current TASK-001-07 experiment selects approved released Mulgae/Gaori Git objects under the owner's minimum stable support direction. The earlier Mulgae/Haepari final-report candidate selection remains historical in the initial worksheet below and is superseded for this experiment. Exact targets, captured bytes and disclosure scope are recorded before generation; selecting a tool never proves its behavior or permits unspecified code disclosure. Select actual revisions and actual workflows before writing expected answers. A retry or approval scenario is suitable only if the selected source supports it.

Start with 3–5 explanations containing roughly 20–30 material claims, including one explanation that can also be expressed well as ordinary Markdown or an ADR and one directory containing multiple linked HTML pages with nested assets. These are initial corpus-sizing proposals, not a statistical confidence claim, production accuracy promise or universal minimum. Expand the sample to resolve a concrete uncovered failure; do not report a percentage as proof of general correctness.

Record the corpus revision, exact source targets, questions, expected source facts and evaluation rubric before comparing candidates. Retain a small held-out set of questions/change cases when tuning prompts, retrieval or rendering. Do not silently replace failed cases with easier ones.

| Role | Responsibility |
| --- | --- |
| Corpus author | Select approved sources, reader tasks and expected facts with exact citations; keep uncertainty explicit |
| Generator | Produce explanations under the recorded package, source and provider contracts |
| Independent evaluator | Check claims against source without treating the generator's confidence or another model vote as evidence |
| Gate reviewer | Review failures, corrections, unresolved limitations and candidate choice; record pass/fail/blocked/not_run with evidence |

The evaluator may be a source-capable reviewer or a separately run evaluation agent. Record that choice and its limitations. Owner participation is useful for product preference, but routine fixture preparation and contract correction do not require repeated owner approval. Do not claim a human usability study when only agents performed the evaluation.

## Evaluation record

Each run records the following, in a test profile or approved evidence location:

- Source project/repository IDs, exact commits or snapshot digests, selected paths, access/disclosure scope and availability.
- Provider/product/version, supported authentication route, viewer profile, package/schema version and generation settings that affect the result.
- Reader question, document/version/page_path/region identity, source mode, captured target and actual captured display state or context_ref.
- Each material claim, its category, supporting evidence IDs, expected scope and evaluator verdict. Apply the [central source-evidence rules](../contracts/source-access.md) to distinguish observed provider input, later exact-target checks and unchanged authored evidence. Deterministic acquisition/verification and semantic support remain separate findings.
- Explanation/package validation results, reader-task results, unsupported or misleading statements, retrieval misses and corrective action.
- Attempts, elapsed time and provider usage only where observable; unavailable metrics remain unavailable.
- Gate status, reviewer, date and links to repeatable evidence. Private source, credentials and raw hidden model reasoning are not required audit artifacts.

Store sensitive evaluation evidence outside public fixtures under the same source/disclosure policy. Committed fixtures use approved nonsensitive or synthetic material.

## Claim categories and checks

A material claim is a statement a reader could rely on to understand behavior, make a change or assess a risk. Check diagrams, captions and interaction outcomes as well as prose. Labels apply to each relevant region or claim; a disclaimer at the end cannot relabel a misleading diagram.

| Category | Required support | Failure example |
| --- | --- | --- |
| Source observation | Exact revision and relevant location; the source directly establishes the statement | Saying that a retry covers every role when the cited implementation selects only failed roles |
| Inference | Supporting observations plus an explicit reasoning limitation; plausible alternatives where material | Presenting an inferred cross-project dependency as a confirmed caller relationship |
| Illustration | Declared model/assumptions and no claim of actual execution | Animation labelled as a real request trace without trace data |
| Trace-backed replay | Captured trace identity, provenance and mapping to the shown events | Invented timing/order displayed as measured production behavior |

Use verdicts such as supported, qualified inference, unsupported, contradicted and unable to verify in the evaluation worksheet. These are evaluation labels, not additions to the runtime freshness enum.

Deterministic checks resolve every declared evidence/reference target at the captured version, validate regions and display-state schema, and identify any unavailable bytes. Semantic checks ask whether the cited material supports the actual statement, with the stated scope. Unavailable evidence cannot be reported as semantically verified.

## Reader tasks and comparison

Use the same reader tasks for the interactive explanation and the Markdown/ADR comparison. Before evaluating either, record each task's expected answer facts, exact supporting evidence, required interaction if any, permissible uncertainty and a concrete success observable. The comparison need not establish commercial differentiation or universal superiority. Suitable tasks include locating the decision point, explaining a branch condition, predicting a failure/retry outcome, tracing an approved cross-project reference, and finding the evidence for an asserted limitation.

Record whether the reader reached the expected answer, which evidence they used, where they were misled, and how much interaction was needed. Timing is a diagnostic measure on the recorded setup, not an automatic claim that one format is better.

Use ordinary generated HTML/CSS/JavaScript in html_js_v1. A result may span multiple HTML files; the evaluator follows links, returns to the exact page/query/fragment, and checks page-specific notes and citations. Shared region names in different pages must not collide. Large selected context must transfer without silent truncation. Demonstrate interactions the reader task needs, such as changing a simulation parameter, exploring a chart, stepping through an illustrative flow or comparing outcomes. Libraries, modules, workers and external resources may support those interactions under ordinary browser rules. JavaScript freedom is already the owner's product choice; the evaluation measures how well the explanation uses it, without requiring a DSL comparison or API allowlist.

A mostly textual explanation is valid when the material needs no interaction. At least one appropriate interactive scenario must still pass to establish the interactive product requirement. If the renderer prevents a useful normal-script scenario, repair the construction while preserving host/native separation. A static fallback does not complete that work.

Network-loaded live data is display input, not automatically a verified source fact. A factual claim still needs recorded evidence for the exact material used. Recommended local bundling improves repeatability; an external dependency is not a permission failure and does not need a per-library approval prompt.

## Reader decisions and bounded reconsideration

Use one concise worksheet per recorded run; several tasks may link it without duplicate reports.

| Field | Required record |
| --- | --- |
| Reader task and role | Concrete question/action; owner, another human or evaluation agent |
| Expected outcome | Material answer facts and exact evidence, required interaction, permitted abstention and scope |
| Observed outcome | Actual answer, evidence used, misleading steps, successful reuse, effort and unavailable metrics |
| Preliminary comparison | TASK-001-01 records benefit_observed, no_difference or not_available; TASK-001-07 retains the latter two as risks with their evidence or missing input |
| Decision | `pass`, `fail`, `blocked` or `not_run`, with the unmet observable and evidence link |
| Next action | Bounded repair/alternative, what result ends that attempt and when to reconsider |

A required reader task passes when the observed answer satisfies its predeclared material facts and evidence, performs its required interaction and does not mislead about source, scope or certainty. Missing a needed real input is `blocked`; an executed incorrect or unusable outcome is `fail`. An unavailable-source scenario may pass by producing its predeclared honest unavailable result. Correcting or withdrawing an unsupported statement does not pass a task whose required answer has become incomplete. Keep the original failed case and rerun it; do not replace it with an easier question.

For relationship/theme and freshness work, predeclare the held-out candidate/change cases, expected supported/unsupported/uncertain outcomes and any evidence-calibrated quality threshold before tuning. Report false dependencies, false outdated claims, missed relevant changes, abstentions and assessed coverage separately. The required source-change cases below and host/data/personal-state invariants cannot be traded for an average score. A partial or zero/unknown-coverage assessment cannot be labelled aligned for the whole scope; a regionless page or version is evaluated at its actual scope.

Record the next repair's bounded work and stop/reconsideration condition before repeating a failed experiment. Routine prompt, UI, retrieval and contract corrections proceed within the current scope. If that attempt still fails the required reader outcome, no supported provider route is available, or the next alternative changes an owner direction, present the failed question, comparison, attempted repair and concrete alternatives to the owner. Do not force an indefinite retry loop, infer a scope waiver or impose a universal numeric advantage or fixed trial period.

## Initial reader comparison

TASK-001-01 predeclares this question for reuse in TASK-001-07: **After Mulgae reports an incomplete review, which exact recovery operation is supported, which accepted role results remain valid, and what happens if the source publication is unavailable? How does that differ from Haepari's approval boundary at its selected final-report revision?** This is a question, not a claim about either implementation.

| Worksheet field | Initial state / required observable |
| --- | --- |
| Approved source target | Owner-selected Mulgae and Haepari final reports after approved validation; exact committed revisions and disclosure scope must be bound before reading/generation. |
| Expected answer/evidence | Source-grounded recovery/approval decision points, preserved-versus-repeated work and honest unavailable behavior, each with exact revision/path/byte evidence. Expected implementation facts are unavailable until those snapshots are bound; do not invent names or semantics from a skill description. |
| Existing comparison | Open the approved project's final report and Markdown/ADR, follow their referenced implementation and explain the same decision path. Existing explanations and current reader steps have not been inspected at the selected revisions. |
| HTML interaction | An interactive recovery/approval branch walkthrough must reveal the evidence for each choice and distinguish known, inferred and unavailable inputs. It must answer the same question as the Markdown/ADR comparison. |
| Observation | `not_available`: selected final-report revisions and corresponding existing explanations are missing. No reader timing, effort saving, owner preference or answer correctness is measured. |
| Predeclared TASK-001-07 risk | The explanations may add no useful understanding beyond Markdown/ADR, or source/expected answers may remain unavailable. A dynamic diagram alone does not establish benefit. |
| Next experiment | Bind both final-report commits, inspect their existing reports/Markdown/ADR within authorized source scope, and predeclare the material answers before generation. Compare the same question on both formats with a named independent source-capable evaluator. |
| Reconsideration | Missing source blocks the consuming experiment. If the required answer/interaction fails, make one bounded evidence-backed repair; if it still fails or requires scope/billing change, present the actual results and alternatives to the owner. |

This preliminary worksheet satisfies the explicit unavailable-input path only. It is neither G-VALUE evidence nor a substitute for TASK-001-05 real-provider proof. Agent-only evaluation must remain labelled, and owner usefulness stays unmeasured until observed.

## Progressive use checkpoints

| Owning checkpoint | Concrete use at the delivered capability | Later evidence still required |
| --- | --- | --- |
| TASK-001-01 | [Unavailable-input worksheet](#initial-reader-comparison), predeclared question and missing final-report revisions; expected implementation facts and friction remain unmeasured | Resolve the inputs, then perform actual provider, viewer and two-project G-VALUE proof |
| TASK-001-04 / TASK-001-07 | Real script interaction on the spike harness, then the two-project generated/evidence-backed reader corpus | Production import, storage, notes, Q&A and remote workflows |
| TASK-003-06 | Browser/Wails import, open a page, use its control, reopen exact content and download original files | Connected search/links, real personal records and private remote use |
| TASK-004-06 | Find and reuse the answer in an existing explanation and follow its exact cross-project reference | Saved Q&A records and full lifecycle UI |
| TASK-005-06 | Save a real note, explicitly Review, create/reopen a new version and recover original records | Production cited AI answers and remote device reconciliation |
| TASK-006-06 | Generate, ask and reopen the saved cited answer using the actual supported provider | Complete private second-device Stage A proof |
| TASK-007-06 | Repeat the complete reader/notes/Review/Q&A flow on the private second device with Wails closed | Manual maintenance, scheduling and release regression |

Each checkpoint records the same task's expected and observed result where applicable; it cannot fail merely because a later feature is absent, nor pass that future feature with a fixture. TASK-003-06 is the first suitable checkpoint for routine owner imports, with later checkpoints reusing those saved results when available.

Recommend one owner-performed reader task at G-VALUE and TASK-007-06. Record actual participation or the reason it was unavailable/declined; agent-only runs cannot establish the owner's preferences or ongoing usefulness. Owner participation is a recommendation, not another required reviewer, approval or gate.

Alongside the existing worksheet, record observed real-use facts when available: actual results imported since the previous checkpoint, explanations reopened or reused with the observed interval/context, and source/evidence inspection. Reuse existing records or concise owner observations; do not add mandatory telemetry. First/last-opened timestamps describe the recorded span and do not prove every intervening visit. Missing observations are unmeasured, never zero use or a claimed benefit. No fixed interval, reuse count or universal advantage threshold is required; use these facts with the existing bounded reconsideration decision.

## Source-change and semantic assessment corpus

### TASK-008-03 frozen predeclaration

This specification precedes Task03 provider execution and tuning. It binds the
approved complete eight-file Mulgae/Gaori explanation inventory at the Task03
base `a9d3ae990366282c80ce096b94b730643226dc2e`, Mulgae source
`cef337cf927b820316f42a41f1441fbc653203f8`, and Gaori source
`393af5999bbbb30b5d11e2a17a2325c8cf1833b7`. Materialization uses only immutable
Git/manifest bytes in disposable private copies, never the sample working tree.
Fixture commit IDs and hashes are recorded before their provider submission.
Controlled descendants are test mutations, not natural-release history.

The general production selector reads complete cited files and captured
observation `dependency_scope` paths at one exact authorized target. Original
cited spans remain separate. No special caller lines, expected verdicts or
case IDs enter product selection. In particular I must acquire the complete
Gaori `internal/cli/cli.go` through this rule, including its caller and callee,
with actual per-attempt byte-delivery provenance. Historical receipts are not
expanded into new observations. The present delegated Coordinator grant covers
these selected files at the approved bases and explicit controlled descendants,
with the same remembered provider/account and enforced runtime restrictions.

After the corrected original baseline retained legitimate partial coverage,
the Coordinator approved optional preparation of twenty additional finite full files at
the same A identities: fourteen Mulgae caller/composition/publication helpers and
six Gaori process/configuration/safety files. Their exact path/hash inventory is
frozen in the private context manifest before any further model submission.
The existing explicit completed same-version capture carries this selected
context; it does not fabricate observation dependencies for unchanged files or
expand implicit/default selection. The original eight explanation files, four
page units, claims, expected outcomes and stopping rule below remain unchanged.
Their essentiality to the original qualified claims was not established. They
are not selected for the next original-four-page baseline proposal, which uses
the original fourteen source files and eight explanation files. Preparation is
not a new model execution grant or a successful baseline.

The general freshness instructions now explicitly assess material claims at
their stated conditional, documented-contract, historical or abstraction scope,
including explicit evidence limits. Documented contracts do not assert observed
execution, and illustrations do not assert that their preconditions held.
Material caller/configuration/dependency gaps still require specific incomplete
outcomes. This clarification changes no output shape, validator, expected
outcome, corpus identity or prior result; actual quality remains due.

| Case | Frozen change/input and required observable |
| --- | --- |
| Baseline | Unchanged approved source and original qualified claims: actual complete page-unit evaluation and scoped alignment. Full input alone does not prove full semantic coverage. |
| D | Delete Mulgae `SourceFromRecovery`'s `if attempt.State == domain.AttemptSucceeded` refusal block (original lines 31-33). M03 must have an evidenced discrepancy against D; do not infer universal replay success. |
| I | Change only Gaori `executeRunContext`'s `materializeArtifacts` final argument from `materializationExecutedCommand` to `materializationSummarizedRaw` (original line 485). Callee 577-587 stays identical. Under the explanation's safe-materialization/extraction-error assumptions G02/G03 must expose the changed `internal_error/4/degraded` result. |
| U | Append only `// Controlled freshness fixture: no behavior change.` to Gaori `cli.go`. Its structural direct-change trigger is not an outdated verdict. Full matching assessment must support scoped alignment. |
| Historical | Keep pinned A and its original truth separate from explicit D/I comparisons; neither original bytes nor A assessment is overwritten. |
| Missing/denied | Controlled profile access fault: unable_to_verify, actual reasons and unchanged denominator; no verified empty target or alignment. |
| Partial | Stop after a first committed page result before the next admission in fresh isolated state. Preserve every unfinished tuple/reason; complete is false and the unchanged, trigger-free whole scope remains not_assessed. |
| Thematic | Original Gaori G12 -> Mulgae M11 analogy never propagates Mulgae D's discrepancy or implementation dependency into Gaori. This is familiar regression evidence. |
| Offline configuration | Explicit synthetic `CanRetry(failures,cfg) { return failures < cfg.MaxAttempts }` and unchanged loader; `policy.json` changes max_attempts 3 to 1. At failures=1 the claim that a retry is allowed changes true to false. This cross-file indirect fixture is offline only and creates no additional live cohort. |

The full-version denominator is every HTML page: two required pages for each
project/version/target. Exact region checks are separate and cannot complete a
page by adding region counts. Exclusions, partial results, unavailable input and
budget exhaustion never shrink membership. Each case starts without an earlier
applicable contribution that could silently fill its missing work, except an
explicitly declared history/precedence regression. The disposable real cohort
uses freshness auto; a separate same-target propose regression verifies no
published/applicable change. Production owner configuration remains untouched.

The same-name cross-page fixture adds only optional supplied region bindings for
the existing `main` element in each original Gaori HTML page. Each binding keeps
that page's original evidence links; HTML, source claims and source bytes do not
change. Each selected region is one fixed tuple, and the other page cannot
inherit its contribution. An explicit completed same-version capture of the
original authorized source paths may supply the bounded full context. Region
contributions never sum into page/version completion. This controlled metadata
binding is labelled separately from original author metadata.

D and I with available fully inspected inputs require an evidenced discrepancy.
Abstention is reported separately and also counts as a missed required change
and a failed case. Required caller acquisition failure is blocked acquisition,
not a successful abstention. False outdated claims, missed changes, abstentions,
blocked acquisition and assessed/partial/blocked/not_checked coverage are separate
counters by evidence class; there is no averaged replacement for a required
outcome and no new numeric score or quality budget. Baseline partial coverage is
honest product behavior but does not pass the required full-coverage proof.

The path-level observer labels D, I and U direct and records triggers on their
actual cited anchors. I proves semantic caller impact, not structural cross-file
indirect discovery. The offline config fixture is indirect/unexamined dependency
scope without an observer direct-change trigger. A semantic assessment clears
only covered triggers of its own target. If full-file acquisition creates C2,
the earlier observation C and its trigger remain separate history; no transfer
or clearance across capture IDs is inferred. The latest matching availability-
blocked attempt exception still takes precedence over discrepancy/trigger state.

Real provider/source integration, controlled-source changes, synthetic offline
faults, browser/native observations and physical-device evidence remain distinct.
The known corpus and author familiarity are disclosed; no blinded or statistical
accuracy claim is made. A failed required result remains failed. Any further
bounded repair states its work and stopping condition before another acquisition;
no easier case substitution, indefinite tuning or unapproved source expansion.

EPIC-004 records cheap source-change observations. It does not claim to perform EPIC-008's semantic maintenance. Preserve the observation target, comparison base, inspected path/dependency coverage and unavailable inputs.

| Case | Required outcome |
| --- | --- |
| A material change in directly cited behavior | Record the observed change; assess the affected claim against the exact target before declaring it outdated |
| An indirect caller/configuration change with unchanged cited lines | Surface a candidate impact or disclose unexamined dependency scope; unchanged cited bytes cannot establish global alignment |
| An unrelated commit | Do not mark the explanation outdated merely because the commit ID changed |
| An explanation explicitly about historical release A while current target B changes | Preserve truth about A; report a separate comparison against B when requested |
| Missing pinned bytes, denied root or unavailable provider | Preserve the saved explanation and report unable to verify for the affected assessment |
| A thematic related link with no established implementation dependency | Do not propagate outdated status as if the link proved a dependency |
| Rejected/pinned topic or relationship, followed by equivalent evidence | Respect the recorded decision; only materially new evidence can justify a new proposal |
| Partial scan or changed input during a run | Report completed, excluded, stale and unresolved scope; do not convert a partial run into an all-current result |

A negative source-change result is bounded by observed inputs. An aligned semantic result is bounded by the assessed claims and target. Neither is a certificate that the whole project is current.

## Search and retrieval checks

Use real Korean terms, English prose, file paths, camelCase and snake_case identifiers from the approved corpus alongside [deterministic fixtures](fixtures.md). Include exact symbols, partial identifiers, 1–2-character Korean queries and longer phrases. Record expected eligible documents/pages/regions before tuning ranking. Search includes every HTML page as indexing progresses, not only a chosen landing page; record unindexed/unsupported content separately from no-match results.

Evaluate authorization and lifecycle filtering before recall/ranking. The short-query fallback, if needed, searches the authorized project corpus; it cannot search only an already empty full-text result set. Any normalization/tokenizer or bounded prefix/substring fallback must be explicit and measured, including its cost and false positives. EPIC-001 checks feasibility; EPIC-004 owns the complete retrieval contract and tests.

Correct filtering of configured scope/lifecycle and pinned-target resolution are deterministic invariants. Recall and ranking are measured on the recorded corpus, with unresolved misses shown; a handful of successful queries cannot justify broad search-quality claims.

## Gate decisions and later reuse

G-VALUE passes for the selected candidate only when:

1. The approved corpus, exact targets and reader tasks are recorded and repeatable.
2. Package/evidence/reference integrity checks pass for the supported cases. Missing or denied inputs produce the specified unavailable behavior.
3. Material contradictions and unsupported factual assertions discovered by evaluation have been corrected or explicitly withdrawn from the candidate. Inferences and illustrations carry the correct local labels.
4. Every required reader task meets its predeclared material answer/evidence and interaction observables under the reader decision worksheet, including an appropriate interactive scenario and the Markdown/ADR comparison. Misleading interaction results or an incomplete required answer after withdrawing a claim remain failures.
5. Seeded change/unavailable/relationship cases preserve historical scope and honest coverage at the capability available to the spike.
6. The evaluator records limitations, held-out outcomes, selected viewer/provider candidates and the canonical evidence status `pass`, `fail`, `blocked` or `not_run`. Only `pass` closes the gate; unresolved failures include a bounded next action and reconsideration trigger.

A passing small corpus is permission to proceed with that baseline, not proof of universal accuracy. A failed check is evidence to repair the contract or candidate; evidence collection alone does not pass a gate.

Reuse and extend the same corpus through EPIC-006 generation/Q&A, EPIC-007's connected-reader gate, EPIC-008 semantic maintenance and EPIC-010 release regression. Later gates use the implemented workflows, including saved answer citations, source-mode selection, cross-device reopen, rejected/pinned decisions and partial scan reporting. A fake provider or a documentation-only walkthrough cannot replace the required real integration.

See [contract cases](contract-cases.md), [traceability](traceability.md), [test strategy](test-strategy.md) and [decision register](../decisions.md).


### Static search predeclared expectations

The [TASK-004-02 worksheet](search-relevance-worksheet.md) preserves the fixed
40-query denominator and >=36/40 top-five target, explicit repair/limitation
rules, alias rank control and approved sample exact identities. M1/M2 do not
execute retrieval or claim usefulness. Owner-authored queries are separate;
absence remains owner use unmeasured. Partial extraction is never complete no-match.

### Stage A connected-reader progressive use checkpoint (TASK-007-06)

The [private-web record](stage-a-gate.md) repeats the approved real Mulgae
(`cef337cf927b820316f42a41f1441fbc653203f8`) and Gaori
(`393af5999bbbb30b5d11e2a17a2325c8cf1833b7`) corpus on actual Tailscale HTTPS.
Before acquisition, the Coordinator fixed this reader question:

> After an incomplete Mulgae review with an accepted required role and failed
> selected optional role, what recovery is supported, what remains accepted,
> and what happens with uncertain publication or missing required sources?
> Compare Gaori after a waiter ends, cancellation is accepted, or its MCP
> connection disappears. Follow its exact prior-compose reference and state
> what the analogy establishes and what it does not.

The six material fact families were observed through fixed search, the original
interactive controls and exact Reader navigation:

1. Inspect original status/get_run, exactly replay available returned attempts,
   then compose committed reruns; do not omit a failed selected optional role.
2. Accepted roles remain accepted; recovery cannot replace acceptance.
3. Uncertain publication needs exact-run inspection. Missing required sources
   block the source-validated path; a new review needs authorization.
4. Wait expiry ends the waiter. Accepted cancellation is neither terminal status
   nor success, and re-await needs the original surviving session.
5. Disconnect requires command/final-artifact reconciliation. A new server
   cannot recover the old invocation ID; missing artifacts do not prove
   non-execution.
6. Gaori `gaori/lifecycle.html#G12` still resolves the exact old Mulgae
   `recovery/compose.html#M11` after successor publication. The link supplies an
   analogy, not a dependency, Gaori source proof or freshness propagation.

Each project's own fixed citations remain separate. This was a familiar,
agent-operated task; it was not blinded, and owner preference, time saving and
ongoing use remain unmeasured. The synthetic successor tests the pin and
version-specific Notes/Review without becoming another real explanation.

Recommended optional owner task: open Gaori G12 in the private Reader, follow
its prior-compose link and explain why a lost MCP session does not authorize a
replacement command. Owner performance remains `unmeasured`; this recommendation
adds no permission question, required participation or gate.

### Bounded relationship, topic, and content-gap candidate discovery (TASK-008-02)

`TASK-008-02` implements schema revision 21 (`maintenance_intents`), the
scoped search/shared-source/reference candidate shortlist
(`CandidateShortlist`, `maxShortlistCandidates = 6`, `B06`), pre-call candidate
HTML/region excerpt inspection (`inspected_candidate_relations`), child
`job.kind = "maintenance"` admission and multi-project/multi-version source
input guarding (`CaptureMaintenanceAndAdmit`, `GuardSourceInput`, `B01`), the
closed native JSON schema (`maintenanceFrame` / `MaintenanceOutputEnvelope`),
and deterministic post-model verification in
`internal/application/scan/candidates.go`:

- `T-008-02-01` (`keyword-dependency`): `depends_on` / `source_dependency`
  (`evidence_basis = "source_fact"`) requires full non-truncated (including
  `> 256 KiB`) retained source reads outside the SQLite writer scoped to each
  focal and candidate unit's own cited evidence paths (`VerifiedEvidenceByUnit`,
  `B02`) and verified directional import parsing
  (`verifySourceDependencyFactWithRepo`) anchored by either a verified
  module/namespace declaration (`go.mod`, canonical Go `// import "..."`
  comment, `package.json`, `pyproject.toml`, `Cargo.toml`) or a resolved local
  relative import in the same verified repository (`RepoKey`), case-sensitively
  and without multi-segment suffix guessing. Keyword-only, reverse-direction,
  partial-source, or unverified-module proposals are never emitted as
  `depends_on` or `source_fact` and downgrade to `related` (`evidence_basis =
  "thematic_analogy"`) with `uncertainty = "source_unverified"` (or
  `"partial_source"`) and a qualified `rationale` that explicitly withdraws the
  unverified code-dependency claim (`B12`).
- `T-008-02-02` (`denied-trash`): Trashed, purged, or unauthorized targets are
  filtered across shortlist selection, byte inspection, child provider dispatch,
  and parent `ApplyMaintenance`, as well as `$.origin_unit` and `evidence_json`
  lifecycle/purge/pagination fencing (`B03`, `B07`); distinct region/page
  candidates within the same target version `(VersionID, PagePath, RegionID)`
  and multiple genuinely distinct relationship mechanisms on the same
  `(source, target, relation_type)` pair (`mechanism_key`, `QR2`) are preserved
  without overwrite while true-equivalent duplicates and uninspected coordinates
  are rejected.
- `T-008-02-03` (`missing-theme`, `QR1`, `QR3`, `QR4`): `topic_assignment`
  normalizes labels (`normalizeTopicLabel`) and uses
  `evaluateTopicFocalGrounding` for focal body/headings, unit-scoped verified
  source, compound/stem matches and bounded paraphrases supported by specific
  focal concepts and rationale. This lexical check is not a general semantic
  entailment guarantee. Declared themes absent from the focal body can convert
  to `new_version_proposal` gaps, while ungrounded topics retain per-proposal
  rejection reasons (`QR3`). Provider `limitations` remain uncertainty/abstention
  records only and do not synthesize content-gap findings; generic
  source absence alone does not create a gap. Content-gap proposals must
  identify a genuine missing explanation or unelaborated detail in the focal
  unit's own available scope rather than restating a focal-covered theme based
  on neighbor omission (`QR1`), and explanation-only (`source0`) units qualify
  findings with `uncertainty = "source_unverified"` and `no_source_declared`
  limitations (`QR4`).
- `T-008-02-04` (`heldout-quality`, `QR5`): Offline unit
  (`internal/application/scan`), SQLite persistence (`internal/adapters/sqlite`,
  including `B01`..`B12` and `QR1`..`QR5` regressions), and populated schema
  20 -> 21 public upgrade checks verify `T-008-02-01..03`, `QR1`..`QR5`
  (including preserving `result.semantic.assessed = 0` and
  `result.semantic.complete = false` for `TASK-008-03` freshness separation),
  and schema 21 migration invariants; the live held-out provider quality cohort
  and full canonical common gate are coordinated separately before final task
  closeout.

The first recorded relationship-quality acquisition and its single predeclared
repair both failed their required reader outcomes. The latter used all 24
registered regions but met only 2/4 useful cases (required 3/4), 1/2 required
focal-gap cases and produced 32 unhelpful cross-project relations. Acquisition
success, scoped offline checks and a cancelled common are not quality or whole
Task passage. Under the reconsideration rule above, Master explicitly approved
one additional bounded correction and one original-set quality experiment.
The original cases, full-output inspection, safety/coverage rules and thresholds
remain unchanged; another failure ends that attempt without an automatic further
cohort.

That additional experiment completed naturally with 24 saved attempts and 277
findings. Independent agent grading inspected all 12 original cases and all 277
outputs: useful connections failed at 1/4 (required 3/4); required focal gaps and
uncertainty cases passed 2/2 each, and one unhelpful extra was within the original
allowance. The other three useful pairs never entered either direction's
shortlist. Cross-project inspection slots fell from 96/144 to 33/144. This
records a retrieval failure, not an excuse to replace cases or lower thresholds.
Source0, familiar-corpus and unexercised restricted-target qualifications remain.
No fourth cohort is authorized, the full common was not started, and Task02 is
not accepted.

The separate static opposite-qualifier defect was not observed among that
cohort's 102 topics. Its later bounded guard correction has current race unit
and controlled SQLite checks for contradicted qualifiers, reverse contradictions,
actual negative-token presence and retained valid topics. These checks do not
relabel the earlier native cohort as a post-fix execution or repair the missed
retrieval cases. The original finding owner independently confirmed this bounded
correction using current source, negative/positive cases and those check receipts;
that confirmation does not establish general semantic entailment or Task passage.

To address the cross-project candidate shortlist bottleneck without altering the
held-out benchmark or its thresholds, Master approved the bounded two-stage
semantic candidate nomination amendment (`internal/application/scan/nomination.go`,
`Mode = "semantic_nomination"`): Pass 1 verifies backing explanation object
availability and marks eligible semantic leaves `structural_ready` while
completing all structural/repair work first; Pass 2 runs bounded global
card-only nomination batches (`nomination:p0:b0000` for `<= 64` units, or
two-pass cross-project interleaved batches for `> 64` units, initially at the
historical `<= 2048`-byte serialized JSON card / `900`-byte excerpt checkpoint
within the `<= 144 KiB` total frame budget) under strict
closed-schema output validation (`minItems == maxItems == len(cards)` requiring
one entry per supplied focal card, required non-blank `abstain_reason` with
`candidates: []` when `abstained == true`, and `1..6` candidates with omitted or
empty `""` `abstain_reason` when `abstained == false`), exact
prompt/digest/projected-card `AuthorizeMaintenance` verification (`EE-SN-P01`,
`N1`), `PV01` saved-result continuation, and `N4`/`PV02` purge tombstoning.
Batch state is stored on `maintenance_intents` and per-unit coverage in
`job_items.detail` (`nomination_coverage`, `visited_nomination_batches`,
`nominated_candidates`, `nomination_abstained`). Structural anchors, validated
nominations (`semantic_nomination` signals labeled with
`unverified_card_routing_hint: `) and current-authority lexical fallbacks share
the existing `K <= 6` Stage-2 shortlist, with up to `6` same-page sibling
regions appended only when a region unit's shortlist would otherwise be empty.

Focused offline race evidence covered that historical `<= 2048`-byte / `900`-byte
checkpoint across card projection and strict decoding, actual serialized adapter
frames (including decoded JSON schema assertions), structural worker ordering,
all-card authority, invalid-output continuation without another Execute, and
secondary-card Trash/Purge with result-pin reclamation, retaining the earlier
failed compile, parent-ordering and fixture runs. The one authorized
semantic-nomination quality cohort on that frozen candidate completed with `25`
saved attempts (`1` nomination + `24` Stage-2 evaluations), `286` proposed
findings, and `6` `signal_error_drained` transport cleanup qualifications, and
failed independent grading under the unchanged 12-case rule: useful relations
were `2/4` (required `3/4`) and `3` non-actionable limitation-derived pseudo-gaps
exceeded the `<= 1` unhelpful-extra allowance, while safety, negative controls,
both focal gaps, and both uncertainty cases passed (`all 24` Stage-1 cards were
truncated at `900 B`, and `extractFocalGuaranteeGapFromLimitation` promoted `3`
epistemic limitation notes into extra gap findings).

Following that cohort failure, Master approved a bounded two-correction
amendment and one additional `<= 25`-attempt original-set quality experiment
under unchanged thresholds: (1) full-fit-first adaptive Stage-1 card projection
up to `<= 8192` raw and JSON-string re-encoded bytes per card within the
unchanged `<= 144 KiB` (`8 KiB` fixed reserve) nomination frame without an
average per-card quota, using deterministic UTF-8 head (`60%`) + `"\n...\n"` +
tail (`40%`) truncation from the unspliced original excerpt only when a card
exceeds `8192` raw/encoded bytes or when the batch exceeds the `<= 144 KiB`
(`8 KiB` reserve) frame budget, and (2) removal of
`extractFocalGuaranteeGapFromLimitation` so `limitations` remain
uncertainty/abstention records only while explicit `content_gaps` and declared
missing scope themes (`evaluateTopicFocalGrounding`) remain the sole content-gap
sources. All five affected focused scopes passed with their exact-input reuse;
failed parent runs retain their original outcomes.

The additional original-set cohort completed with `25` dispatched/saved attempts
(`1` nomination + `24` regular evaluations), `25` completed children and positive
drain for every child. All `24` cards retained their full `54,096` original body
bytes. The resulting `290` proposed findings comprise `152` relationships,
`106` topics and `32` typed gaps; `144` ordered candidate pairs were inspected.
The original Claude Opus 5.5/high Task Evaluator inspected every saved regular
public output and all `290` findings under the unchanged predeclared rule.
The quality component passed: `4/4` useful cases (`3/4` even without the qualified
fourth), both focal gaps, both uncertainty cases, no material false fact,
dependency, source or coverage claims, and no unhelpful thematic extras.
The four negative cases were not retrieved; this is not observed abstention.
Two accepted borderline analogies retain their mechanism-specific comparison
and explicit non-transfer qualification; classifying both as unhelpful would
exceed the original extras allowance. The independent judgment and Coordinator
review accepted them without changing the rule.

Three attempts retain `signal_error_drained`; positive drain is not pristine
cleanup. No project-source bytes were supplied. Two malformed target identities
and eleven unsupported topics were filtered. These agent-only corpus observations
do not prove general precision, human usefulness, physical-device behavior,
positive live dependency evidence or live restricted-target isolation.
`semantic.assessed = 0` and `semantic.complete = false` retain Task03 ownership.
All four earlier failed quality cohorts remain failed on their original inputs.

The subsequent complete `make test KEEP_GOING=1` execution failed with exit `2`:
two stale selected-contract digest records and one CLI test expecting unregistered
finding statuses. Its integration, persistence and E2E layers passed, including
all `95` E2E cases; the aggregate remains failed. The bounded correction changes
only the test's status literals/negative cases and those two digest records,
with no product-code change. The corrected CLI race spec and current prepare
stage passed. Independent final original-Goal and scoped contribution judgments
and the four original roles' explicit settlement of all `21` original findings
establish the current composition. Its executed layer/affected-case composition
covers `1,407` Go and `256` Python cases; this is not a fresh full aggregate.
CO substantive review and delegated acceptance establish the bounded Task02
delivery, and all five native Goal criteria are satisfied. This factual/lifecycle
overlay receives separate affected documentation checks. The normal completion
commit/native finalization and Tasks03–06 remain separate; no overall Epic,
general usefulness or physical-device acceptance is claimed.
