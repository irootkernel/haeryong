# Design system proposal

## Direction

A restrained document workspace: neutral surfaces, clear typography, one action accent, generous reading width and compact operational metadata. Prefer persistent meaning-bearing labels to icon-only status. Avoid a decorative knowledge graph on Home, excessive cards, gradients behind code, or simulating a full IDE.

## Proposed tokens

| Token | Light | Dark | Usage |
| --- | --- | --- | --- |
| canvas | `#F5F6F8` | `#111827` | App background |
| surface | `#FFFFFF` | `#1F2937` | Reader/context surfaces |
| text.primary | `#172033` | `#F3F4F6` | Main reading text |
| text.secondary | `#526070` | `#C3CBD5` | Metadata, not disabled text |
| border | `#D8DEE7` | `#475569` | Dividers and controls |
| accent | `#175CD3` | `#93C5FD` | Primary actions and selected tab |
| warning.text | `#854D0E` | `#FDE68A` | Source mismatch with explicit label |
| danger.text | `#B42318` | `#FCA5A5` | Destructive confirmation |

These are proposals, not measured contrast results. Validate foreground/background combinations in implementation. Personal Reviewed uses a check icon plus label, not a general green status that implies factual correctness. Freshness and lifecycle are different rows/badges.

Typography: system sans-serif stack including platform Korean fonts, base reading text 16 px, metadata 13..14 px, body line-height 1.6, code monospace 13..14 px. Reading text width target 65..85 characters; allow larger canvases for diagrams without shrinking all text. Do not bundle proprietary fonts. Use 4/8/12/16/24/32 px spacing, 8 px control radius and restrained shadow for temporary sheets only.

## Responsive behavior

| Viewport proposal | Layout |
| --- | --- |
| >= 1280 px | 224 px global sidebar, flexible reader, 352 px context panel; context can collapse |
| 768..1279 px | Compact navigation rail/drawer, reader remains primary, overlay context panel with focus containment |
| < 768 px | Single column, top connection/version header, bottom or sticky host actions, full-height context sheet |

Breakpoint values are implementation proposals. Validate 390x844 phone, 844x390 landscape, 820x1180 tablet and 1440x900 desktop fixtures. Do not require landscape-only reading. A virtual keyboard must not cover question input/send status. Safe-area insets, pinch zoom and text scaling must remain usable.

## Accessibility requirements for this project

Use semantic buttons/links/headings and keyboard-reachable actions. Target 44x44 CSS px touch controls. Main text contrast target at least 4.5:1, large text 3:1; document measured combinations instead of claiming compliance from token values. Provide visible focus, logical tab order, modal focus trapping/return, Esc to close non-destructive sheets, reduced-motion support and no color-only meaning. The Files/Pages picker needs keyboard navigation, searchable accessible names and clear current-path state. Virtualized rows must remain reachable without assuming every file is simultaneously in the DOM. Region selection needs a keyboard alternative through a host-owned outline/list when available; raw pages without declared regions remain usable with page/text context.

Do not globally capture common browser shortcuts or interfere with source/code text selection. Proposed shortcuts: `/` focuses library search outside inputs; `Escape` closes context sheet; use explicit buttons for Review completion and deletion rather than accidental single-key mutation. Confirmation dialogs announce impact and cannot default-focus permanent deletion.

## State vocabulary and copy

The documentation is in English. The Korean localization examples below are preserved as JSON-escaped string literals. Decode them when using them as localized UI copy; they are example data, not documentation prose.

| English label | Korean copy proposal (JSON-escaped literals) | Meaning |
| --- | --- | --- |
| My review | `"\ub0b4 \uac80\ud1a0"` | Owner state, never AI verification |
| Unreviewed / Reviewing / Reviewed | `"\ubbf8\uac80\ud1a0"` / `"\uac80\ud1a0 \uc911"` / `"\uac80\ud1a0 \uc644\ub8cc"` | Version-specific personal state |
| Updated since your review | `"\uac80\ud1a0\ud55c \uc774\ud6c4 \uc5c5\ub370\uc774\ud2b8\ub428"` | New explanation version, old record preserved |
| Outdated against target | `"\ube44\uad50 \uae30\uc900\uacfc \ubd88\uc77c\uce58"` | Evidence-backed scoped assessment |
| Unable to verify | `"\ud655\uc778\ud560 \uc218 \uc5c6\uc74c"` | Required source/input not available |
| Not saved. Reconnect to retry. | `"\uc800\uc7a5\ub418\uc9c0 \uc54a\uc558\uc2b5\ub2c8\ub2e4. \ub2e4\uc2dc \uc5f0\uacb0\ud55c \ub4a4 \ud655\uc778\ud574 \uc8fc\uc138\uc694."` | No false success on disconnect |
| Reference unavailable | `"\ucc38\uc870 \uc790\ub8cc\ub97c \uc0ac\uc6a9\ud560 \uc218 \uc5c6\uc74c"` | No unauthorized target metadata disclosure |

This table proposes copy and localization-ready keys; it does not add full multilingual release support without a scope decision. Korean titles/search content must work regardless of UI language.
## Core component contract

`ResultFiles`, `PageLocation`, `TransferProgress`, `ReviewControl`, `FreshnessBanner`, `VersionSelector`, `EvidenceCard`, `RelatedLinkCard`, `AnnotationEditor`, `SourceModeSelector`, `JobProgress`, `CoverageSummary`, `PolicyPlanDialog`, `LifecycleImpactDialog` are host-owned. Each receives typed API state, loading/error/permission status and action callbacks. ResultFiles and TransferProgress distinguish loaded/enumerated/total counts and never imply a truncated list is complete. These shell layout and accessibility rules do not impose widths, DOM schemas or script restrictions on author HTML. None trusts a generated frame's message to authorize a mutation. UI optimistic previews are clearly pending and roll back/reconcile on failure; server acknowledgement alone marks Saved.
