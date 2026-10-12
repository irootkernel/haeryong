# Documentation integrity report

**Checked:** 2026-10-13 KST

**Bundle:** 1.6.2, with the current R5 design prototype organized and linked for implementation handoff. D-10, product scope and the task sequence are unchanged.

**Scope:** Canonical prototype source/reproduction integrity and read-only specification integrity checks. This is not an application, browser, provider, network or filesystem-durability test. All 61 Tasks and 10 Epics remain planned; G-STACK, G-AUTH, G-VIEW, G-AI, G-VALUE and G-CONTRACT remain not_run.

## Reproduction

Run from the checkout with Python 3 and PyYAML:

```sh
python3 -B docs/testing/validate_docs.py
```

The validator performs no project writes. --root selects another checkout. --skip-manifest is only an intermediate editing check and is not the final full-integrity result. --manifest prints a proposed inventory; the inventory file is updated separately and the full command is then rerun.

## Checks and interpretation

| Check | Result / meaning |
| --- | --- |
| Original source | Exact SHA-256 matches the preserved source constant; the complete verified value is recorded below. |
| Markdown inventory | The canonical prototype contains five Markdown files, including the preserved original brief. The current checkout has 148 Markdown documents while the redundant dated folder remains; removing its four Markdown files yields 144. The validator reports the actual count at each run. No Epic or Task was added. |
| Review guide | README and REVIEW-GUIDE both identify 1.6.2; the existing validator checks their agreement. |
| Execution records | 10 Epics, 61 Tasks, 71 frontmatter records; all still planned. |
| Requirement and acceptance coverage | R01-R40 and all 29 original C/M sentences remain covered; original acceptance text is preserved verbatim. |
| Supplemental ownership | X01-X20 named roles match Task metadata, with collective requirement coverage; early contributions are not claimed as full scenarios. |
| Assertions and graph | 207 distinct owning-prefix assertions; dependencies exist, membership agrees and the graph has no cycle or later-Epic prerequisite. These are test definitions, not executed product tests. |
| Markdown and examples | Balanced fences/tables; YAML, JSON and JSONL examples parse. Relative file links and checked heading anchors resolve. Counts are emitted by each validator run rather than hard-coded into this report. |
| Worked directory | Seven files, including three HTML pages, and three represented directories retain the recorded bytes/inventory. Eight local HTML link targets resolve. |
| Whitespace | git diff --check for docs reports no findings. |
| Final inventory | MANIFEST.md covers all remaining Markdown documents except itself, plus the read-only validator. Full validation compares each final size/hash and inventory membership. Regenerate the inventory after removing a document or directory. |

Exact preserved Revision 4 source SHA-256:

```text
8f26fea2833cd3cc3312c1afadea74e042d1d895b079bfa1bdff9cc02cf563e5
```

| Worked-example identity | Preserved SHA-256 |
| --- | --- |
| payload_sha256 | df1a2aaa44139da7592c4a5c158addfec74b438351c5561ca8e64b4cfe6363da |
| supplied_metadata_sha256 | 1f2cf1cb072cbabb9f3f0a5722d031cefec0f42d27bf86bae6cf30d1f303f7ae |
| manifest_sha256 | 7e8b471f187350c2781098e6038aa7d89bc70b3d7d441bd4bc63334b14a95f86 |

## Prototype organization and verification

The current R5 concept is now under [docs/design/prototype/](../design/prototype/README.md), with a [screen-to-task handoff](../design/prototype/HANDOFF.md). The [original brief](../design/prototype/00-BRIEF.md) and [review history](../design/prototype/REVIEW.md) are preserved in the same canonical folder. The former dated working folder is redundant and can be removed; active documentation and the assembler no longer depend on it. Main/UI navigation, screen/flow/design-system references, Epic and Task indexes, EPIC-003 and TASK-001-01 now link the concept to the existing implementation sequence.

The [organization record](../design/prototype/ORGANIZATION.json) verifies that all 34 copied source, artifact and review files are byte-identical to their dated counterparts. The [local reproduction record](../design/prototype/LOCAL-REPRODUCTION.json) records one execution of the copied assembler in an independent local canonical mirror. Both generated HTML aliases match the original R5 exactly: 309,429 bytes, SHA-256 `719567a66a51350b02f49c073f7103061d71b7e60c196b8209aa3c766b0e4f1a`. The Mac assembler was not executed for this check. No temporary transfer markers remain in the canonical files.

The pre-organization full documentation check failed only because four existing dated-design Markdown files were absent from MANIFEST: one inventory mismatch and four missing size/hash entries. The intermediate check after source organization found 147 Markdown documents and no link, heading, syntax, ownership or dependency issues; its explicitly skipped manifest result was not treated as a final pass. MANIFEST is regenerated after this report and the remaining documentation edits, then the full command above is run with manifest checking enabled.

R5 remains an unapproved concept using sample data. Its prior captures describe the old R3 HTML/JavaScript with temporary R4/R5 CSS previews, as qualified in the handoff. This organization did not load the complete R5 in a browser or rerun its interactions. The handoff records existing visual-proposal differences without introducing a new approval gate. The existing specification still starts at TASK-001-01; all implementation statuses and gates are unchanged.

## Redundant design-folder removal

The original brief was additionally preserved byte-for-byte at `design/prototype/00-BRIEF.md`: 9,764 bytes, SHA-256 `6084d9a78102593ed75ef67beab587749d3476d60acba03eebd6254316b86f31`. Together with the earlier 34 identical files, the canonical folder contains the required source and review material. The old README is superseded by the canonical entry. Working links to the dated design folder were replaced; historical inspection paths remain unchanged as dated provenance.

This check prepares removal; physical deletion of `docs/design/2026-10-12-grok/` has not been performed in the recorded checkout. After removing that directory, regenerate MANIFEST using `python3 -B docs/testing/validate_docs.py --manifest` and rerun the full validator. Earlier JSON validation results retain their recorded snapshot and counts.

## Preserved v1.6.2 wording-cleanup qualifications

The input inspected for the preceding wording-cleanup patch is `reviews/2026-10-12-grok/follow-up-1.6.1.md`, SHA-256 `fb2bc462643c8f3e2672c000d02916f5fa9f9625f15c7fe772aa50dc5e59ad1c`. It remains unchanged. The pre-edit full check exited 1 with three broken links to two absent historical inputs: `review/grok-1/implementation-readiness.md` and `reviews/2026-10-12-grok/implementation-readiness.md`. Their original paths are now explicitly unavailable provenance, not working links. Neither file was recreated or replaced with this follow-up review. Earlier PASS records describe earlier file snapshots, not present availability. Historical Opus paths remain unavailable as already recorded.

The REVIEW-GUIDE refresh and version check introduced in 1.6.1 are retained. Its version now matches this handoff. The validator utility is unchanged in 1.6.2; manifest generation continues to read the edition date from README. Link, guide-version and file-integrity checks remain enabled. No missing-source exemption or disabled check is used to obtain a clean result.

## Preserved semantic review scope

The remaining schema sentence now distinguishes the current request's receipt identity from body/metadata target.operation_id, which identifies a future consumer and can differ from the upload-create key. The existing composite-ID fixture includes valid U/Q bindings, same-key target changes and wrong-consumer rejection for question bodies and import metadata. Architecture, security and deployment summaries defer to the complete TASK-001-04 checklist instead of maintaining different environment lists. Earlier document/base, context, draft and cancellation rules are unchanged. No product gate, Task status, API field, content limit or approval requirement was added or advanced. The handoff begins at TASK-001-01 without another specification-only review prerequisite.

These meanings were inspected as specifications. Their planned request, cancellation, race, permission, proxy and real-device cases still require executable implementation evidence. No semantic theorem, universal browser support or error-free implementation follows from a clean documentation validator.

## No runtime actions

No Go/Wails build, live AI call, provider login, private-source submission, Safari device run, Tailscale change, opened port, production import/export, migration or trial restore was performed. The user Git index was preserved, and no commit or push was made. The manifest inventories final bytes, not a signature or software-correctness certificate.
