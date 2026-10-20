# TASK-003-06 responsive Reader and cross-client gate

Task: [TASK-003-06](../tasks/EPIC-003/TASK-003-06.md).
Status: Task completion candidate accepted by Main under delegated authority.
Browser/native verification, the final common aggregate and required whole-Task
reviews passed. Assembled Epic review and actual overall owner acceptance remain separate.
The [design-system baseline](../ui/design-system.md), [viewer protocol](../contracts/viewer-protocol.md),
and [knowledge-quality checkpoint](knowledge-quality.md#progressive-use-checkpoints) govern this work.

## Shared Reader behavior

The wide layout retains the 224px global navigation, Files column, flexible
Reader and collapsible 340px Context. Tablet widths use compact navigation and
a modal Context overlay. Below 768px the Reader uses one column with full-height
Files/Context sheets, safe-area padding and sticky host actions. Panel changes
and resizing retain the same viewer/channel; layout does not remount authored
content or create a render delivery. Files, Reader and Context own explicit
grid row/column slots at every declared breakpoint, with the Reader in the
single first column on phone widths; no script restores scroll on resize.

Sheets use one persistent native dialog element. Mode changes close and reopen
that same element with the correct modal/non-modal semantics. Modal Tab and
Shift+Tab wrap within reachable host controls; Escape and close restore focus.
The persistent wide Files column stays in normal grid flow. Context also closes
with Escape in its wide mode. Ordinary browser shortcuts and authored-frame
keyboard handling remain separate.

The Files filter matches already loaded paths. Its cursor notice states that
unloaded entries are not searched, and Load more remains available. Current-page
links retain their accessible path and current-page state. Choosing a non-HTML
file closes the phone Files sheet before focusing the reachable download
region; the choice carries its exact version and path, and a same-document
version switch discards the choice and its consent instead of silently
retargeting the file.

Each Library document row exposes one semantic `결과 읽기` action with a
minimum 44px accessible target. When a stored latest version exists it links
the already implemented exact-version Reader route; an unmodified primary
click uses the shared router navigation while modified clicks keep ordinary
browser link semantics. Documents without a latest version keep the action
disabled with an honest explanation and never navigate.

The optional host region outline binds the exact version/page/context/reload
selection. A modal Context closes before dispatching focus to its captured live
channel; both entrypoints (the optional `#region` deep-link hash and the
outline) share one channel-captured frame focus under the captured page guard
before the guarded dispatch. Unmount, navigation, reload or channel
replacement cancels a pending attempt. The loader reports whether an element
was found and actually focused; a missing logical region never blocks reading.
Focus is best-effort in the native viewer (see the native section); the child's
found/focused acknowledgment is the only success authority and upgrades no
selection, evidence, verification or native authority.

Selects opt out of system appearance and draw a themed current-color chevron
with adequate right padding, so the shared 44px control floor applies as a
real box on every client in both color schemes; semantics, values and
selection behavior are unchanged.

## Verified browser results

Real Chrome runs exercised all four declared layout fixtures (390x844,
844x390, 820x1180, 1440x900), Home/Library/Reader, light/dark styles, reduced
motion, 200% root text scaling, 44px host targets, computed contrast,
keyboard/touch actions and real dialog mode transitions with no horizontal
host overflow. Tab/Shift+Tab containment, Escape/return, single-modal
behavior, in-flow Files columns and no extra render deliveries were observed.

The current integrated browser regression passes whole: 298 assertions —
the original 294 of the responsive Reader suite (G-VALUE imports, R1–R4
controls including the await-deadline branch, inline evidence, exact
reopening, original ZIP and raw-file byte comparison, L1 Library entry via
click/Enter/touch and retained reopening, reading-position continuity with
explicit grid slots and a dimension-matched raw control) plus four added
select checks at phone width in both color schemes: real CDP keyboard
typeahead, state and render verification, all selects at 45.1875px with the
visible currentColor chevron, visible focus, the archived-library URL route,
unchanged existing-document import state and no overflow. Separately, the
full transfer regression — the heavy recovery/fault scenario suite with one
persistent authenticated browser session — passed as its own run against
transfer code unchanged since the earlier milestone. Earlier CLI-session
failures were traced to a checker VM limitation and corrected without product
change. The dedicated Task Judge and two non-author Workers passed the
earlier browser milestone as a scoped result approval. The browser session
uses a genuine public CLI-issued device credential only at the trusted main
origin, labelled as test setup; it is not native UI-pairing proof.

Frontend regeneration and normal byte-equality validation pass on the current
source. A fresh canonical `make test` passed on the current post-select-CSS
source with unchanged application and documentation hashes throughout execution:
prepare, Go unit/integration/additional persistence, and Python unit (10),
integration (40) and public E2E (46), with no selected failures, errors or skips.
The tagged desktop race suite, vet, production build and actual
untagged `--child-probe` rejection passed on the unchanged desktop source set
(linker warnings degraded log extraction separately from exit zero).

## Original reader scenarios: expected and observed

The questions and expected answers were bound to the original sample bytes
before execution. They assess the packages' explanations and controls, not
new Mulgae or Gaori executions or independent verification of their upstream
claims. Both clients retained the authored Illustration / not-an-observed-
execution labels.

| Scenario / original page | Expected answer and meaningful control changes | Evidence inspected | Observed reading result |
| --- | --- | --- | --- |
| R1: Mulgae `recovery/inventory.html` | Retained recovery cannot replace an accepted role. A mismatched cleanup receipt refuses recovery before persistence; preparation plus a matching receipt alone grants no replay authority. Change role, cleanup and proof. | M03/M04 and inline M-S02/M-S03/M-S09/M-S10 excerpts. | Browser and native authored outputs matched. |
| R2: Mulgae `recovery/compose.html` | Missing selected optional roles still need committed reruns; accepted roots cannot be replaced. Complete mapping does not promise publication or CI success. CLI/MCP uncertainty requires inspecting the exact returned run identity before retry. | M08–M12 and inline M-S05/M-S06/M-S07/M-S09/M-S10 excerpts. | Browser and native matched, including the MCP incomplete-publication boundary. |
| R3: Gaori `gaori/outcomes.html` | Keep child exit separate from evidence-pipeline status: child zero can coexist with internal-error artifacts and CLI exit four; child seven remains a failure. Summarize executes no child, parser miss preserves the child result, and oversize failure invents no numeric exit or complete receipt. | G01–G06 and inline G-S01/G-S04/G-S06/G-S15/G-S19 excerpts. | Browser and native matched; the summarize branch also disabled the child control. |
| R4: Gaori `gaori/lifecycle.html` | Wait expiry or cancellation acceptance does not settle command completion. Continue the same original invocation; losing its server does not transfer its live identity. Finished is terminal but need not mean passed. | G08–G12 and inline G-S11/G-S12/G-S17/G-S18 excerpts. | Browser and native matched, including the await-deadline input in the passing native journey. The pinned prior-compose explanation remains an analogy. |

Browser and native completed exact reopening and original-directory/raw-file
byte comparison where stated below; native complete source-section captures,
browser inspected excerpts and authored control outputs support these reading
observations. An anchor identifier alone is not the claim-support check.
Routine owner usefulness remains unmeasured.

## Native qualification mechanism

The production companion is tested through its real entrypoint and facade.
The observer/input seams require both `haeryong_test_state` and
`haeryong_native_automation`; they are absent from production builds.
`--child-probe` requires an absolute, owned, private regular UTF-8 file; the
public WKUserScript observer is restricted to the expected renderer origins
and a nonce loopback collector and grants imported content no credential,
management route or native binding. Own-window public AppKit resize, snapshot
and keyDown/keyUp actions report completion separately from actual viewport,
PNG, focus and activation observations; synthetic DOM walkthroughs are
labelled and never substitute for physical input. Native pairing is
UI-created, CLI-approved and adopted by the real Go flow, then remembered
across restart. No native credential or operation journal is seeded.

The tagged automation harness terminates its script at the first failed
action; a failed-step sentinel negative confirmed that later actions never
execute, and the terminal report records the script as finished, never as a
pass. The companion sets the public Wails `TabFocusesLinks` webview
preference so keyboard navigation includes links and controls in this
WebView; measured with real keys, host controls became reachable at both
window sizes, and a separate focused run performed a real Tab+Enter
activation of the authored skip link with the parent fragment mirror.
Authored skip links keep their authored skip behavior; no width
generalization is claimed beyond those measurements.

## Bounded native failures and dispositions

An early assembled native journey completed genuine pairing, both corpus
imports and the R1–R4 authored-control and evidence assertions but timed out
at the region-outline stage with no child-focus event; the incomplete-journey
cause is not retroactively proven. The predeclared R4 await-deadline branch
was added to the checkers after that run and later observed natively in two
subsequent assembled journeys before their own unrelated failures. Focused single-factor diagnostics on the unchanged compiled product ruled out
delayed/retried dispatch, child self-focus, parent-driven child window focus,
an exact-origin focus permission and every page-observable readiness
predicate, establishing an interaction-dependent best-effort boundary on
programmatic frame-to-region focus in this native viewer; the exact engine
condition is not claimed. The six-case matrix measured: initial hash and
compact-outline attempts report honest `focused=false` (frame focus arrives,
region focus does not), the wide outline focuses with a real acknowledgment,
and missing regions report honest absence — preserved as the original
observations. With the bounded frame-focus repair and the app-owned
TabFocusesLinks option, the full six-case matrix passed with unchanged
product packets: honest first `focused=false` results, real arrow-key reading
followed by re-selection focuses the declared region in all four recovery
cases, and missing regions stay honest. The generic `focused=false` notice
now appends the measured keyboard-reading recovery hint as a possibility and
was exercised by this matrix; keyboard recovery is the only measured recovery
path, with no pointer recovery, password action or lock-policy change claimed.
Earlier locked-desktop interruptions remain environment observations.

A later assembled full native journey failed one measured accessibility row —
Library selects rendered below the shared 44px floor natively (23px) while
the browser flow passed — resolved by the appearance-free themed select CSS
(now a real ~45px box on both clients; before/after measurements, chevron,
focus and overflow checks passed in the current browser regression in both
color schemes, and the native select/typeahead run verified the dark scheme).
That journey's failure is preserved. A subsequent assembled run failed after
real Tab navigation reached a host personal-state recovery button on the old
Mulgae compose page — a legitimate UI control, not a product keyboard defect —
and no stored state was hidden or cleared; the failure is preserved. A real
Tab+Enter activation of the authored skip link and a native select/typeahead
run (four real keys plus archive-only fixture preparation proving the existing
React import state) passed. Native select popup opening remains unmeasured
and is not a separate gate.

## Native and cross-client results

A fresh native execution passed preparation, genuine UI-created pairing and
protected CLI approval, the native reading journey, remembered restart,
native-to-browser and browser-to-native publication checks, the supplemental
tree and the instrumented child-denial regression. Both original corpora
passed R1–R4 controls and complete source excerpts, exact page/query/fragment
reopening, every non-HTML file download and whole-directory ZIP byte checks.
Browser and native transport metadata and complete cursor-file inventories
matched for each exact version; device/session differences were expected.

X18 preserved all seven entries of the indexless tree, including the Unicode
HTML path, hidden binary, unknown attachment, empty directory and symlink.
File bytes, entry kinds and the original relative symlink target matched the
stored inventory and downloaded ZIP exactly; no basename substitution is
accepted. X07 retained module, worker and meaningful click behavior while
host DOM/storage access was denied, with eight denied native requests, six
blocked scheme requests and zero privileged dispatches after child-observer
instrumentation. Normal host/child errors were absent, apart from separately
classified expected attack-page noise. Owned apps, daemon and temporary idle
assertion were closed; application source hashes stayed unchanged.

This is the approved composed verification: every original inline native
journey stage passed, with the six independently completed region cases,
separate skip-link activation and current select/typeahead observations
bound by source, binary, separate dist-tree and select-only CSS applicability
checks. The partial runs' completed cases are identified individually; their
whole-run failures remain failures. It does not claim one fresh inline
six-case keyboard matrix. Independent script reviews and meaningful negative
preflight checks preceded execution. Checker corrections preserved the
original checks while isolating new-consumer stage state and comparing
original symlink text as exact UTF-8 bytes.

Native chooser fixtures exercise the delivered transfer stack;
[Task005's genuine chooser callbacks](desktop-companion.md) retain their
separate scope. Additional physical devices, native popup-menu opening and
routine owner usefulness remain unmeasured.

## Review and handoff

The dedicated Task Judge and two non-author Workers passed the whole result.
The Epic Judge then independently approved the same Task candidate in its Epic
context, and Main accepted it after personally reviewing the implementation,
actual results and phone/wide captures against project and product goals.
One required documentation correction separated both-width host-control
measurements from the wide-only authored skip-link activation; affected reviews
confirmed the correction without repeating unchanged product checks. A proposed
stale focus-acknowledgment concern was inspected by both Judges and Main; no
concrete defect was established, so no speculative change or new gate was added.
Original failures and assessments remain preserved.

The Main-owned completion commit and its verification belong to the runtime
handoff. The assembled Epic review and actual overall owner acceptance remain
separate boundaries; Task acceptance is Main's delegated decision. Runtime
receipts and captures stay private; this record contains no runtime paths or
credentials. EPIC-007 retains remote/private-device delivery and later mobile
workflow qualification.
