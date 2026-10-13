# UI proposal: a reader with inspectable context

**Status:** Proposed product UI and behavior specification. The [initial execution baseline](design-system.md#initial-execution-baseline) governs layout. The separate [R5 interactive concept](../design/prototype/README.md) is an unapproved visual input; its persistent tablet context column was not selected. The [UI handoff](../design/prototype/HANDOFF.md) records differences and review limits. Delivery/action mappings follow the [1.4.0 review reconciliation](../changes/2026-10-11-opus-review-reconciliation.md). Basis: Revision 4 as amended by D-08/D-09: personal development, directory results with multiple HTML pages, personal review and first-class desktop/mobile browser use.

## Brief

The owner uses tools such as Codex, Claude or Grok to produce explanations of their work and source changes, then reads them in Haeryong, inspects evidence, revisits links and notices changes to previously reviewed material. Each result is a directory of any number of HTML pages and related files. Normal JavaScript supports charts, simulations and other useful interaction without per-script or per-library approval. The main job is reading and questioning, not watching agent terminals or managing a general project workflow.

Choose a calm, text-first **reader workspace** with clear result and page navigation. Share one UI between browser and Wails. TASK-001-01 selects React + TypeScript + Vite, CSS variables and small semantic components as [the initial execution baseline](design-system.md#initial-execution-baseline); these frontend choices were not fixed in Revision 4. Their actual accessibility and cross-client behavior still require implementation proof.

## Primary layout

On a wide desktop: a persistent library sidebar, a flexible explanation canvas, and one context panel with **Evidence / Notes / Ask / Links** tabs. A compact **Files / Pages** picker inside the result opens its directory tree and page list without turning each HTML into a separate library document. Keep version/evidence metadata and personal/library actions in the host chrome; let the explanation use ordinary HTML/CSS/JavaScript inside its own page. On a phone: a single reading column, compact version/source header, accessible Review/Context actions, and full-height file/context sheets. Tablet preserves reading width and opens a temporary side panel.

## Connection and authoring

Pair a browser once and remember the device by default. Reload, new tabs and ordinary browser restart return to the selected explanation without a new approval. Reuse configured project/provider choices. The owner may select a plain output folder, ZIP or single HTML produced by an external AI tool. No manifest, designated entry page, manual hashes or provenance fields are needed to open it. Haeryong's built-in adapter is another creation path, not a prerequisite for reading those outputs. The [shared import controls](screens.md#import-export-and-file-operations) default to New document and offer an existing document's new version with its base captured before upload. Progress, resume and the owner upload list preserve the same target and retry intent. Picker capability and represented-entry fidelity are explicit; no product size/count ceiling applies.

## Reading path

Open Home or Library, choose a result version, then follow an exact page link, Continue from the last-opened pointer, or use its owner preferred/default page or Files/Pages picker. Preferred defaults and each page's saved position are owner records separate from immutable imported files. Follow local HTML links within that same version, inspect evidence, and save a page-specific note or question. Mark as reviewed explicitly for the whole directory version. Updated source does not erase the review history. A concise source/freshness status distinguishes unknown/unverified basis, observed generation input, later source verification, unavailable evidence and a scoped assessment against an exact target. A source pointer or post-hoc check does not by itself prove a claim; show the assessed scope and incomplete coverage. Missing source metadata does not block opening a raw result directory. Normal page opening has no generic security warning or repeated trust prompt.

## Large and mixed-content results

Render the selected page and fetch its dependencies as needed. File lists are paginated/virtualized without silently hiding additional files. Preserve represented paths, file bytes, hidden files and nested/empty directory entries; unknown formats remain listed and downloadable. A large result must not be rejected because all its content would not fit an in-memory envelope. Actual transfer/device failures show progress and recovery, not an instruction to reduce the result to a single page.

## Documents

- [Information architecture](information-architecture.md): routes, navigation and feature parity.
- [Screen specifications](screens.md): components, actions, transitions and error states.
- [Eight editable wireframes](wireframes.md): wide desktop, tablet, phone and management examples.
- [Flows](flows.md): pairing, review, publication and maintenance interaction.
- [Design system](design-system.md): visual tokens, responsive rules, language and accessibility.

The wireframes use synthetic labels/counts and conceptual widths. They are not screenshots of Haeryong and do not prove accessibility or browser compatibility. Implementation captures and interaction evidence belong to the owning Task.
