# Haeryong full-product design prototype brief

Date: 2026-10-12 (Asia/Seoul)
Owner request: Karl asked ChatGPT to use the installed Grok through the terminal to create a coherent full-product design proposal, inspect it, and request revisions directly. ChatGPT is the design lead and reviewer; Grok is the author. This task is authorized design/prototype work, not production implementation.
Baseline: current Haeryong documentation v1.6.2, commit b82793a.
Working area: docs/design/2026-10-12-grok/ only.

## Goal

Produce an attractive, calm, usable, interactive design prototype covering Haeryong as a whole product. The main experience is reading interactive source explanations and asking questions, with review status and evidence available without taking over the reading surface. The user previously got a visually cluttered app from specification-only implementation; this prototype must establish an actual visual baseline.

Use the existing product direction: neutral surfaces, readable Korean typography, blue action accent, restrained elevation, clear list hierarchy, generous useful reading space. Do not turn Home into a metrics dashboard or the Reader into an IDE.

## Read these current sources

Read docs/ui/README.md, docs/ui/design-system.md, docs/ui/information-architecture.md, docs/ui/screens.md, docs/ui/wireframes.md and docs/ui/flows.md. Read docs/examples/use-haeryong-skill.md and the Page integration channel/theme.set section of docs/contracts/viewer-protocol.md for the generated-HTML boundary. Product facts already defined there remain authoritative.

## Deliverables for this pass

1. prototype.html: one self-contained UTF-8 HTML/CSS/JavaScript file. No build step, CDN, web font, external network request, provider call, third-party account, or production API. It must open as a local file and also from a basic static server. All required styling and script belong in this file.
2. DESIGN-NOTES.md in English: design decisions, complete screen/state coverage table, working mock interactions, limitations, and how to open the prototype. Clearly identify synthetic demonstration data and simulated actions. Do not claim browser verification unless actually performed.
3. No changes outside this working area. Do not change original requirements/contracts/tasks, initialize a framework, commit, push, deploy, install software, alter user sessions, use real credentials, or send messages to other people.

Use real semantic HTML controls. No decorative photos or invented brand illustrations are necessary. A simple text Haeryong wordmark is sufficient. Prefer labeled actions and a consistent available icon source if any; do not make a forest of decorative badges.

## Coverage: full product

Include genuinely different screens/views and appropriate dialogs/sheets, linked from the prototype:

- Home: continue reading, recently updated since review, small attention section, primary import/create entry.
- Library: realistic list, search, project/review/freshness filters, empty results, long Korean titles and Unicode paths; directory result is one item.
- Projects and project source detail: project scope, remembered source identity, availability, provider/source preferences.
- Reader: useful authored explanation, page navigation, Files/Pages, optional page outline, direct Ask action, exact version, compact independent review/freshness information, links/back navigation, return to saved position.
- Reader Evidence, Notes, Ask, Links: all four states must contain realistic designed content and functional switching. Ask includes selected passage/context preview and a mock answer with citations. Notes support draft/save/cancel, visibly pending or saved state and draft retention across panel/tab transitions. Links distinguishes authored references, backlinks and suggestions.
- Versions: last reviewed versus current comparison, changed-file list, old/new version selection, incomplete diff state, unresolved old note anchor.
- Import: New document versus New version, human-readable target/base, file/folder/ZIP mock picker choices, progress, pause/interrupted/resume, finished and version-conflict states. Simulated file selection must be clearly a demo. No raw IDs dominate the normal view.
- Create explanation: a focused request entry using remembered project/source/provider, progress and result opening. This is a design proposal for a creation entrypoint, clearly mock-only.
- Maintenance: overview, report tabs Coverage/Freshness/Relationships/Topics/Drafts/Activity, partial scope, actionable finding detail, approve/reject/reassess, draft preview/reject/replace/publish as explicit simulated interactions.
- Archive and Trash: restored prior state, clear item-level actions, meaningful purge impact dialog with cancel-default and an explicit mock confirmation. Do not mutate any real data.
- Settings: General/appearance, Connection, Sessions, Projects/source scope, AI, Maintenance, Retention, Storage/uploads, Backup status, About/compatibility. All sidebar sections must show designed content, not the same placeholder.
- Connect: pairing comparison code, remembered device, waiting/rejected/expired/connected examples, reconnect distinction.
- Component/state gallery: buttons, fields, tabs, list rows, statuses, notices, sheet/modal; loading, empty, disconnected, source-unavailable, stale-note conflict, provider-unavailable examples. Keep demo controls outside ordinary product navigation, e.g. a small clearly labeled design-preview menu.

## Required UX choices

- Reader's default surface shows title/version/page and direct Ask; operational details expand on demand. Keep review and freshness distinct but compact.
- Context can collapse. Wide viewport must retain usable reading space with both side areas open. Define default visible state and responsive transitions deliberately.
- Mobile has a real single-column composition with a direct Ask control, not a shrunken desktop. Full-height Ask/Files sheets preserve context and return path.
- A review action applies to the entire directory version. User-visible Korean wording must make this scope clear without repeated confirmation popups.
- Draft Notes and the text of an unsent question survive context tab/sheet changes in this mock. Closing or navigating must make retained draft state understandable.
- Ask displays a concise selected passage and source mode; detailed provider/hash settings are secondary.
- Import shows target title and base version, not internal IDs as the main UI. Interrupted imports are discoverable at the import/Home entry as well as Storage.
- Ordinary unknown source basis is informative; it is not a giant generic alarm.
- Preserve the product distinction between personal Reviewed, changed source/freshness, and archive/trash lifecycle.
- Demonstrate generated explanation content designed to sit comfortably inside the shell, including light/dark styling, code, a table and one meaningful interactive scenario. Arbitrary imported HTML remains a supported product concept; do not invent mandatory author schemas or force transformations of originals.

## Visual and content requirements

- Korean-first UI and realistic Korean explanations; keep proper project/code names as written. Examples: Haeryong, Podway, Dolgorae, Aquarium, Gul.
- Include a long Korean document title, a nested Unicode file path, multi-page result, code, citation, realistic empty/error states and a partially assessed result.
- Synthetic sample dates are anchored on 2026-10-12, not unrelated old template dates.
- Proposed original tokens are the base. Refine visual rhythm and state styles; describe departures in DESIGN-NOTES.
- Reading text about 16px and line-height about 1.6, deliberate heading scale, metadata secondary but readable, consistent 4/8/12/16/24/32 spacing rhythm.
- Meet project touch/focus/contrast intentions in the design. Use at least 44px mobile primary targets, visible keyboard focus, labels, focus return, Escape handling, modal semantics and reduced motion.
- Use responsive layouts that work at 390x844, 844x390, 820x1180, 1280x800 and 1440x900. Avoid global horizontal overflow; long code/tables may have contained overflow.
- A light/dark switch updates both shell and the cooperative sample explanation.
- No gratuitous charts, numbers, gradients or card nesting merely to fill space.
- Do not present the whole application as a tiny card in the middle of the page.
- Make the initial default route the polished Reader or Home, with an obvious way to reach every product screen. Mark the artifact once as an interactive design demo; avoid repeating technical disclaimers in ordinary user flows.

## Validation and review handoff

Functional mock interactions must be real in browser memory or localStorage where appropriate. Search filters change rows; page selection changes content; notes can be edited/saved/cancelled; Ask displays a clearly simulated answer; versions change; modal actions produce meaningful demo state. No fake successful network/backend claims.

If already available, use local browser tooling to check; do not install anything or assume an uninspected browser exists. Any temporary files must follow the workspace AGENTS instructions. At minimum run a meaningful syntax check of embedded JavaScript without writing outside the work area. Do not create a broad test framework.

Return concise evidence:
- files created and sizes,
- design approach and main navigation,
- what actually works in the mock,
- exact validation performed and limitations,
- any remaining gaps needing the design lead's review.

Root will inspect actual rendering and send explicit revision requests. Do not stop after a plan or prose description: create the requested full-product prototype.
