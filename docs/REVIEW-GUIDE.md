# Haeryong Execution Specification Review Guide

This bundle is the **1.6.2 personal directory-reader execution specification**, translating Revision 4, the useful review corrections and the owner's personal-developer/multi-page direction into implementation work. Each result is a directory, with no Haeryong-imposed HTML/package size, file-count or depth cap. Concrete runtime candidates remain proposed until their gates pass. It does not claim that code changes, service configuration, authentication integration, or real-device testing have been performed. Authored specification prose is in English. The current [Grok reconciliation and follow-up](changes/2026-10-11-grok-reconciliation.md) records D-10 and the contract cleanup. [Import workflow](contracts/import-workflow.md) is the authority for import targets, preparation keys, context binding and cancellation. The [read-only validator](testing/validate_docs.py) checks documentation integrity, not runtime behavior.

## Recommended reading order

| Order | Document | What to review |
| --- | --- | --- |
| 1 | [decisions.md](decisions.md) | Previously established decisions and the detailed implementation choices proposed in this bundle |
| 2 | [ui/wireframes.md](ui/wireframes.md) | Desktop, tablet, and mobile layouts |
| 3 | [ui/screens.md](ui/screens.md) | Actions, states, and failure behavior for each screen |
| 4 | [roadmap.md](roadmap.md) | The sequence and exit criteria for the 10 Epics |
| 5 | [tasks/README.md](tasks/README.md) | The detailed execution order of the current 61 Tasks, including the early usefulness spike |
| 6 | [testing/knowledge-quality.md](testing/knowledge-quality.md) and [testing/traceability.md](testing/traceability.md) | Semantic support, usefulness, and initial implementation versus release-regression ownership |

## Preserved product direction

A shared Go daemon manages SQLite and content, while the Wails v3 application and web browsers use the same library. A Haeryong-owned skill creates explanations; document/version/page/region references and backlinks form the wiki. Personal review remains `Unreviewed → Reviewing → Reviewed`, separate from content freshness and lifecycle state. The scope includes manual and scheduled AI maintenance, configurable policies, deletion and restoration, and private remote web access.

## Additional defaults proposed in this bundle

| Area | Proposal | Decision status |
| --- | --- | --- |
| Shared UI | React + TypeScript + Vite, with a CSS-variable-based design system | Newly proposed here, unlike the established Go/Wails direction |
| Screen layout | Library navigation on the left, explanation in the center, and an Evidence/Notes/Ask/Links panel on the right | UI proposal to review before implementation |
| Service contract | `/api/v1` HTTP JSON, SSE job events, and optimistic concurrency | Proposed implementation contract |
| Port | One configurable management listener, with D-10-approved additional tailnet-only HTTPS renderer ports | Inspect existing routes and conflicts before setup; actual mapping and safe origin reuse remain G-VIEW experiments, not completed configuration |
| Authentication | Pair once, remember the revocable device bearer, and manage the library as its owner | G-AUTH checks normal persistence, actual storage-loss recovery and revoke; any content-delivery credential is explicitly scoped and tested, with no extra elevation flow |
| Generated explanations | A complete result directory with multiple HTML pages, modules, libraries and supporting files | Owner directions D-08/D-09; no compulsory manifest, entry page, byte/count cap or format allowlist; normal browser capabilities |
| Import/export and navigation | Stream/resume transfers, generate an inventory, load pages on demand, preserve original bytes/paths and page-specific links/notes | Concrete contracts remain proposed; actual resource failures are reported without silently truncating content |
| Source retention | Reference-only by default; retain selected snapshots only for user-approved material | Baseline intended to avoid unauthorized source copying |
| Initial policies | Manual scans, approval proposals for semantic changes, and automatic purge disabled | Numeric limits, durations, and automation scope remain configurable |
| Priority environments | macOS ARM64 desktop, macOS/Linux service, iOS/iPadOS Safari, and Android Chrome | Early G-VIEW requires pinned Wails, the selected primary browser and one real iOS or iPadOS Safari device; remaining agreed targets retain their release checks |

D-08 and D-09 establish the owner's personal-tool and directory-result directions. D-10 authorizes additional private renderer-port experiments, not public exposure or a passing implementation gate. Their concrete mechanisms, such as origin-scoped bearer storage and renderer construction, remain proposed implementation choices to validate; they must preserve the selected low-friction workflow. Changes to product direction are recorded as explicit decisions.

## Execution sequence

`Value/risk validation and contracts → Service and durable storage foundation → Shared reader → Links, search and source-change observation → Personal records, lifecycle and backup/export → AI generation and Q&A → Remote web completion → Manual maintenance → Scheduled operations → Integrated release`

Task count follows distinct outcomes. TASK-001-07 runs after the real provider spike and before contract freeze. Earlier Epics own the foundations their consumers need: local jobs/receipts/outbox/pins in EPIC-002 and the minimal approved source reader in EPIC-004. A dependency check must inspect these semantic prerequisites as well as numeric Task references. Early tests are rerun as integrated regressions in later Epics; postponing their first execution until release is not acceptable. The existing epic-handler format remains unverified and requires a contract-neutral metadata check.

## Key UI proposals

The default screen is Home, showing **material to review and material updated since review**, rather than a graph. The Reader gives the explanation most of the space and opens one of Evidence/Notes/Ask/Links on the right. On mobile, that panel becomes a full-height sheet. Above the explanation, show personal review state, source provenance, and freshness on separate rows, with Files/Pages and the current relative path. Review covers the whole directory version; notes/questions preserve the exact selected page. Missing source metadata is shown as unknown/unverified and does not block ordinary import or reading. Do not place review-completion, deletion, or policy-change controls inside AI-generated HTML.

All eight wireframes are editable layout tables within Markdown. They are explicitly distinguished from static image mockups and functioning UI prototypes.

## Evidence required before implementation proceeds

The first Epic starts with a cheap reader-task worksheet, checks provider/deployment eligibility early and requires passing evidence for the stack, authentication, viewer, real AI integration, usefulness and final contracts. The usefulness gate compares real source-grounded explanations and an ordinary Markdown/ADR explanation, separating citation resolution from claim support. The complete Observable completion sections of TASK-001-04 and TASK-001-05 are the sole G-VIEW and G-AI closing lists. G-VIEW checks normal JavaScript, concurrent-version mapping and focused host/native separation; G-AI requires a real cited question and actual adapter behavior. Multi-page generation/usefulness belongs to TASK-001-07; production personal state and durable provider recovery stay in their feature Epics. Authentication checks remembered use; no mandatory OS worker jail is added. All six gates must pass before the next Epic starts; a blocked unrelated proof still permits bounded work inside the current Epic. Recording a failure is not completion. Reader, AI and remote milestones repeat the actual-use checkpoint. Early bounded evidence, first integrated implementation and release regression have separate ownership. These are bounded compatibility and correctness checks, not a universal adversarial-content containment program.

## Decisions for the owner

The owner has resolved JavaScript freedom, directory/multi-page input/output freedom and permission for additional tailnet-only HTTPS renderer-port experiments. No new product decision blocks this documentation revision. [The decision register](decisions.md) specifies the remaining conditional choices: an unsupported subscription/deployment route, missing source/account authorization, or actual host/signing/network authority. Reuse existing authorization; routine reads, implementation choices and repairs do not need repeated confirmation. [The current revision record](changes/2026-10-11-grok-reconciliation.md) explains the corrections and preserves Stage A and unrestricted artifact behavior. No new approval is needed for these wording repairs. Start TASK-001-01 under the existing execution sequence; another specification-only review round is not a prerequisite. Actual network setup remains work in the assigned D-10 experiment after the already required impact checks.

**All Tasks remain planned.** Bundle preparation checks the documents themselves, including file links, IDs, predecessor dependencies, coverage of the original 29 acceptance scenarios, and JSON example syntax. These checks are separate from security and functional verification of the actual product.
