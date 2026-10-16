---
id: "EPIC-001"
type: "epic"
title: "Value, contracts and critical-risk validation"
status: "done"
depends_on: []
completion_requires: ["TASK-001-01","TASK-001-02","TASK-001-03","TASK-001-04","TASK-001-05","TASK-001-07","TASK-001-06"]
---

# EPIC-001: Value, contracts and critical-risk validation

## Goal

Prove the chosen provider, shared stack, remembered private authentication, useful ordinary JavaScript and real two-project understanding workflow before freezing current durable contracts.

## Entry gate

The current execution baseline is accepted or updated through recorded bounded changes. Use isolated profiles; required account/source/device authority must exist before the relevant experiment. Apply the [execution protocol](../execution-protocol.md) and current [personal-tool amendment](../changes/2026-10-10-personal-tool-revision.md) and [directory-results amendment](../changes/2026-10-10-directory-results-revision.md). No task depends on an unimplemented future Epic to meet its stated exit.

## Ordered tasks

| Order | Task | Outcome area | Dependency |
| --- | --- | --- | --- |
| 1 | [TASK-001-01](../tasks/EPIC-001/TASK-001-01.md) | Ratify baseline, UI brief and execution conventions | Baseline review |
| 2 | [TASK-001-02](../tasks/EPIC-001/TASK-001-02.md) | Prove headless stack and native companion | TASK-001-01 |
| 3 | [TASK-001-03](../tasks/EPIC-001/TASK-001-03.md) | Validate pairing and private ingress design | TASK-001-02 |
| 4 | [TASK-001-04](../tasks/EPIC-001/TASK-001-04.md) | Prove useful JavaScript and host separation | TASK-001-03 |
| 5 | [TASK-001-05](../tasks/EPIC-001/TASK-001-05.md) | Validate subscription adapter and exact source reads | TASK-001-04 |
| 6 | [TASK-001-07](../tasks/EPIC-001/TASK-001-07.md) | Validate the two-project understanding workflow | TASK-001-05 |
| 7 | [TASK-001-06](../tasks/EPIC-001/TASK-001-06.md) | Freeze contracts and close readiness gate | TASK-001-07 |

Task IDs remain stable; use the explicit order and dependencies. Task count follows coherent outcomes, not a fixed per-Epic quota.

## Exit gate

**G-STACK, G-AUTH, G-VIEW, G-AI, G-VALUE and G-CONTRACT all pass for the selected personal-tool implementation and recorded available environment, using the current owner-amended applicability. Evidence collection alone, a recorded failure, a blocked test or a fixture substituted for required real proof cannot close this Epic.**

Every task listed in completion_requires must be done and reviewed under the shared definition of done. Record actual evidence with the canonical pass/fail/blocked/not_run status. Required failures or not-run checks prevent completion; a future Epic is not evidence for this exit.

## Acceptance contribution

C01, C02, C03, C04, C05, C06, C07, C08, M01, M07, M08, M09, M10, M11, M14, M15, M16, M17, M19, M20. See [traceability](../testing/traceability.md) and the task-specific supplemental cases. Contribution does not certify an unexercised full scenario. Knowledge-related gates use [knowledge quality](../testing/knowledge-quality.md).

## Handoff

Passing spike evidence, the real-source quality corpus and draft Haeryong skill, current frozen/provisional contract boundaries and independently repeatable fixtures. Full production Stage A remains later work. Use the [Epic gate template](../templates/epic-gate.md) as an index to actual artifacts and identify the exact next task.

## Prepared closeout record

Prepared on 2026-10-16 after final internal validation of commit
`9ce7d9263e8461324f13013a94efd26005cc3d49` (tree
`6c3ef7c66f53c2906ddd5290bde578be63b44bc2`). This record and the prepared
`done` frontmatter take effect only when committed after actual Master overall
acceptance of the exact closeout candidate. They do not record an acceptance
that has not occurred. All seven member Tasks are done and reviewed; the
committed Epic remains in progress until that conditional commit.

### Gate and evidence index

| Gate | Result and selected scope | Canonical evidence |
| --- | --- | --- |
| G-STACK | `pass`: pinned Go/SQLite, real headless Linux ARM64, native/service lifetime, snapshot/transaction and Korean/code cases on declared hosts. | [Stack record](../testing/stack-spike.md) |
| G-AUTH | `pass`: remembered one-owner access, exact pairing, revocation, private HTTPS and bounded restore behavior. | [Authentication record](../testing/auth-ingress-spike.md) |
| G-VIEW | Bounded `pass`: all five owning assertions on pinned Wails, primary Chrome and actual agent-operated Aside/private HTTPS; useful ordinary JavaScript with separate host/token/native authority. | [Viewer record](../testing/viewer-spike.md), including the subsequent remembered-access correction |
| G-AI | Bounded `pass`: supported real subscription question, exact source/input/citation and truthful permissions/error boundaries. | [Provider/source record](../testing/provider-source-spike.md) |
| G-VALUE | Bounded recorded-corpus `pass`: released Mulgae 0.1.24/Gaori 0.1.18, all six normative conditions, four paired readers, held-outs and nine seeded cases. | [Value record](../testing/knowledge-value-spike.md) and [quality method](../testing/knowledge-quality.md) |
| G-CONTRACT | Bounded `pass`: selected shared schema, all 24 C1–C5 cases and five supplemental groups, actual 29 methods, named SQL calibration and current/future UI ownership. | [Contract record](../testing/contract-freeze.md), [machine manifest](../../tests/contracts/manifest.json) and [UI map](../testing/contract-ui-actions.md) |

The whole-Epic review on 2026-10-16 used three fresh static roles: implementation
correctness/regressions; requirements/interfaces/integration; and test evidence/
failure boundaries. All three original REQUEST_CHANGES reports remain unchanged.
Their three unique issues were corrected in the owning Tasks: remembered access
on retryable auth failures, the historical TASK-001-05 handoff, and source-read
verification-body adoption. Meaningful affected checks and actual agent-operated
Aside/native outage/restoration evidence support the corrections. The retained
Judge confirmed the local composition, then final internal validation passed
all 65 original criteria with zero mandatory gaps on the committed revision above.
Root accepted that internal result. No new provider review of corrected bytes
or actual Master overall acceptance is inferred from it.

### Delivered commits

Seven normal member commits and seven owned corrections form the delivered
sequence below. The unrelated setup commit `a94b3ed7252cb8858acf0f2a552ed9e0762af75f`
is excluded from Epic implementation evidence.

| Owner | Normal member commit | Owned corrections, in order |
| --- | --- | --- |
| TASK-001-01 | `7cdab7e2a08ba05322c58c6b2d55759583715efc` | None |
| TASK-001-02 | `3d5fcf04e637d3996c4c4522fbee057df876cfa7` | None |
| TASK-001-03 | `cf94e8145bf0abc5af5722a73dbce793974c51ed` | `60aefb600e47ff10f9374ea171987a2630e717da` |
| TASK-001-04 | `faa05165e1f8d19bd9a171b3eb60a5c6d898069c` | `dca002c2c633e8aaccc1901fc2ae6d04f544156a`, `399f46636826bd1d41425f16ac5793a230d5266b` |
| TASK-001-05 | `059bde0fe4accc8d8202c208fe2491339f675fee` | `730b3a93cb0dbeccfbd852e9b1bd24f6e2b48c9a` |
| TASK-001-07 | `788c3c1854ace3aa6ad1de7e5d2d679f2a7e06a7` | `f1eeb95808eaa145755f51840ee717f3f139404c` |
| TASK-001-06 | `3f8cb7eb57dff89586aeabb0da46979e0b1c9862` | `a4c07154ec3249475463c38722fdb4274d65bbff`, `9ce7d9263e8461324f13013a94efd26005cc3d49` |

### Verification and remaining scope

The latest correction has 15 recorded Gaori attempts: 13 passes and two expected
failing original baselines. Current command families include `node --test` on the actual
host authentication harness (nine cases), `go test -race` on the affected viewer
package, `go -C spikes/viewer/desktop test -run TestNativeSession -count=1 .` (five startup cases), and
`python3 -B tests/contracts/run.py` (29 methods), plus focused binding/docs checks
and current viewer/native builds. Native tests/builds retain 23 linker warnings.
Passing successor checks do not rewrite failed baselines or earlier failed
metadata verifiers. Exact argv, environments, source digests and original
results remain local; canonical records above describe their bounded outcomes.

Actual current recovery used desktop Aside/Chromium 153 at 1440x900 on macOS 27.0.1
ARM64 and the pinned native build. No physical Android, Safari or Stage A second
device is certified. The original extra native Back-after-reload failure remains
failed with unknown cause. TASK-001-05's actual 10,475-byte input versus 11,642-byte
preflight and Q6 uncertainty remain; TASK-001-07 retains one quality repair, agent
familiarity/order/carryover and unmeasured owner usefulness. TASK-001-06 models are
not a production importer or durable receipt ledger; SQL calibration is curated,
setup-excluded, repeats the first 50 metadata rows and leaves OS cache uncontrolled.
API latency and general retrieval quality are unmeasured. Current keyboard
activation was unconfirmed and full browser-console coverage unavailable.
These limits and later owners remain in the linked records.

Complete ordinary result directories and normal HTML/CSS/JavaScript remain the
baseline; no mandatory author manifest/index/SDK or artificial content quota was
introduced. Production APIs/storage/migrations/recovery, saved personal-state
workflows and platform/release proof remain later work. The next Task after
accepted closeout is [TASK-002-01](../tasks/EPIC-002/TASK-002-01.md); this record
neither starts it nor changes any later Epic/Task status. There is no release,
release-note enrollment, separate dossier, promoted evidence package or new
hardening record. Original execution evidence and report custody remain local.
