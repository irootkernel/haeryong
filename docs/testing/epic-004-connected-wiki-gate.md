# EPIC-004 connected wiki and search gate

**Epic:** [EPIC-004](../epics/EPIC-004.md)
**Decision:** `pass` for the implemented connected wiki on the available macOS/browser environment.
**Reviewed implementation:** member commits below plus one integration correction, assembled at `51a61e9`.
**Review:** agent-operated; the Epic Evaluator and two fresh non-authoring Executor reviewers independently assessed the assembled result, followed by the Coordinator's project/product review.
**Acceptance:** the Coordinator accepted individual Tasks under delegated authority. Master accepted the assembled Epic for this closeout; these are separate decisions.

## Member deliveries

| Task | Local completion commit | Implementation and actual evidence | Review |
| --- | --- | --- | --- |
| [TASK-004-01](../tasks/EPIC-004/TASK-004-01.md) | `38ae9d3` | [Source verification and observation](source-observation.md) | Task Evaluator, two non-authoring reviewers, Epic Evaluator and Coordinator: pass/accepted |
| [TASK-004-02](../tasks/EPIC-004/TASK-004-02.md) | `c5a99b5` | [Static text and region search](text-region-search.md), [relevance worksheet](search-relevance-worksheet.md) | Task Evaluator, two non-authoring reviewers, Epic Evaluator and Coordinator: pass/accepted |
| [TASK-004-03](../tasks/EPIC-004/TASK-004-03.md) | `56299c9` | [Pinned references](pinned-references.md) | Task Evaluator, two non-authoring reviewers, Epic Evaluator and Coordinator: pass/accepted |
| [TASK-004-04](../tasks/EPIC-004/TASK-004-04.md) | `8898abc` | [Backlinks and deterministic index recovery](backlinks.md) | Task Evaluator, two non-authoring reviewers, Epic Evaluator and Coordinator: pass/accepted |
| [TASK-004-05](../tasks/EPIC-004/TASK-004-05.md) | `d53a08b` | [Deep links and version navigation](deep-links-versions.md) | Task Evaluator, two non-authoring reviewers, Epic Evaluator and Coordinator: pass/accepted |
| [TASK-004-06](../tasks/EPIC-004/TASK-004-06.md) | `eb58fc9` | [Two-project knowledge navigation](two-project-knowledge.md), including the [EPIC-004 evidence index](two-project-knowledge.md#epic-004-evidence) | Task Evaluator, two non-authoring reviewers, Epic Evaluator and Coordinator: pass/accepted |

The Epic-scoped supporting commits `da4d1f4` (metadata whitespace) and `5f94cd1`
(canonical test limits as hang guards) were reviewed and accepted separately.
The assembled review found that an unborn source HEAD or a missing HEAD commit
object failed as an infrastructure error instead of the contracted
`missing_source` observation; integration correction `51a61e9` fixed it, with
signal-terminated git kept an infrastructure error, after two delta reviews.
Each completion commit was inspected against its accepted candidate; the
assembled review is separate from those Task approvals. Detailed checks and
qualifications stay with the linked records.

## Assembled outcome and verification

Owners register projects and approved source roots. Exact-revision checks of
imported evidence run without AI and keep the original evidence separate from
verification history and capture time; source observation sets target-scoped
`reassessment_required` for direct changes and discloses indirect-dependency
and missing-source limits. Static text and region search covers both projects
under the same eligibility as every other read. Authored references resolve to
canonical pinned targets with exact cross-project Reader navigation; backlinks
and live counts come from the same eligible edges, and owner validation and
rebuild recover derived indexes deterministically. Copied links carry only
logical identity under the approved base and survive login, reload and history;
the shared Versions view compares exact versions progressively and imports new
versions against a captured base. The two-project gate composes these surfaces
on the real Mulgae and Gaori samples without regeneration.

| Final check | Actual result and boundary |
| --- | --- |
| Canonical `make test` | pass on the integration-correction candidate, whose tree equals the assembled commit: prepare; Go unit 137, integration 41, persistence 451; Python unit 10, integration 53, public E2E 57. Its tree, inputs and selection are those of the assembled Epic. |
| Frontend validation and desktop checks | pass: committed web assets equal their inputs; desktop race and vet clean |
| Real-sample search | pass: unchanged worksheet and driver, 67 observations without unexpected misses, `manual_sc19`, Chrome and native corpus clients; RC03 keeps rank 4 after its recorded 6 → 4 repair |
| Real-sample references, evidence and recovery | pass: exact Gaori G12 → Mulgae M11 reference kept after a successor; ten source checks across both projects with history, replay and capture time preserved across restart; real reverse-index divergence repaired by the existing owner rebuild without AI; direct change sets scoped `reassessment_required` |
| Reader outcome | agent-only pass for the predeclared replacement cross-project question; owner usefulness unmeasured |
| Documentation integrity | pass at the closeout commit boundary |

## Boundaries and next dependency

The reader walkthroughs are agent-run, not owner usability results. Seeded
grants and lifecycle rows prove filtering only; production trash, purge and
restore belong to EPIC-005, whose verified restore also owns repair of a
missing or altered reverse-index definition. Saved Q&A and managed AI belong to
EPIC-006, semantic assessment production to EPIC-008, and private ingress and
second-device use to EPIC-007. Native ReaderMode keeps full qualification false;
no native file chooser or transfer from the Versions view is claimed. This is
not a signed release, remote publication or complete Stage A acceptance.
Runtime reports, receipts and session identities remain private.

All six member Tasks and the Epic exit gate pass for this scope. The next
ordered implementation is [TASK-005-01](../tasks/EPIC-005/TASK-005-01.md).
Closeout satisfies its Epic dependency but does not activate it or authorize
new source or provider access, installation, host changes or deployment.
