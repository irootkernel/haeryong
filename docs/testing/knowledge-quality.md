# Knowledge quality and usefulness evaluation

**Status:** Proposed evaluation contract for G-VALUE, Stage A and maintenance gates. No real-project corpus, model run, user study or quality result is claimed by this document. Delivery and evidence clarifications follow the [1.5.0 follow-up reconciliation](../changes/2026-10-11-opus-follow-up.md). This method elaborates R05, R09, R11, R20, R24 and R40 under owner directions D-08/D-09. It evaluates source-grounded explanation quality while preserving normal JavaScript and directory-result freedom.

## What must be demonstrated

An explanation must help a reader answer a concrete question about approved source at an exact revision. A citation that resolves proves that the referenced bytes can be located; it does not prove that the bytes support the explanation. Evaluate structural validity, claim support and reader usefulness separately.

A saved explanation may remain useful when its source is unavailable. Its availability and freshness labels must honestly describe what was retained, what can be read now, and which target was actually checked. Personal Reviewed is an owner record, not a certification of factual accuracy.

This is an implementation quality evaluation for Haeryong-generated/source-verified explanations, not a semantic admission test for arbitrary imported files. A plain AI result directory can be imported and read without source metadata, declared regions or a Haeryong manifest. Its source basis stays unknown/unverified until evidence is actually established; import success is never a factual-correctness label.

## Early corpus and ownership

TASK-001-01 checks whether the intended provider/deployment is supported before expensive integration work and prepares one concrete reader question/comparison from already authorized existing material. Record expected source facts and current effort, or the exact unavailable prerequisite, plus benefit_observed, no_difference or not_available. Carry the latter two as predeclared risks into TASK-001-07; these are comparison observations, not additional gate statuses. This cheap checkpoint does not require a new model call, future UI, fixed trial duration or a G-VALUE pass. TASK-001-05 proves one real bounded cited question. TASK-001-07 then runs G-VALUE before TASK-001-06 freezes contracts.

Use two real projects within the owner's configured source and disclosure scopes; reuse existing authorization without per-read approval. The owner selected Mulgae and Haepari at final reporting after approved validation. Exact committed revisions and disclosure scope remain unbound; the selection is not a claim that source bytes were inspected or permission to submit unspecified code. Select actual revisions and actual workflows before writing expected answers. A retry or approval scenario is suitable only if the selected source supports it.

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
