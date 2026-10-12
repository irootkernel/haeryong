# Editable UI wireframes

**Eight low-fidelity proposals.** Titles, hashes, counts and findings are synthetic. The layout tables describe placement and behavior; they are not screenshots or a functioning app. See [screens](screens.md) and [information architecture](information-architecture.md) for authoritative interaction and route behavior. D-09 makes the result directory the library item and individual HTML files its pages.

## WF-01: Desktop Home

| Area | Proposed content | Action |
| --- | --- | --- |
| Global header | Haeryong, Search library, Local/default connection | Search or inspect connection |
| Left navigation | Home, Library, Projects, Maintenance, Archive, Trash, Settings | Change workspace section |
| Continue reviewing | Retry execution model, version 3, Reviewing; last page flows/retry.html | Continue at the saved page and position |
| Updated since review | Integration walkthrough, Reviewed version 2, latest version 3 | Compare exact versions |
| Needs attention | Retry execution model, source reassessment needed | Open assessment scope |
| Recent maintenance | 12 structural checks, 4 semantic assessments | Open report |

Each row represents a directory result. Review badges describe an exact whole-directory version. No summary implies all content is current. New explanation offers generation or a folder/ZIP/single-HTML import. The shared import form shows New document by default or Existing document, new version with a searchable target and captured base before transfer.

## WF-02: Library with explicit filters

| Area | Proposed content | Behavior |
| --- | --- | --- |
| Search | selective retry | Search text, page paths and optional regions |
| Filters | All allowed projects, My review, Active, Freshness, retry topic | Preserve filters across reader navigation |
| Result row 1 | Retry execution / Project A, version 3, Reviewed, reassessment needed | Open preferred/default page; Continue separately resumes last-opened; Files/Pages opens this result's tree |
| Result row 2 | Queue integration / Project B, version 2, Unreviewed, not assessed | Match: flows/retry.html, region retry; open that exact match |
| Pagination | 2 visible matches in this sample | Load the next cursor; never imply a partial list is complete |

Wrap or accessibly expand long titles and Unicode paths. File counts appear when known; a result is not split into hundreds of library rows because it contains hundreds of HTML files. While indexing is incomplete, show its coverage without withholding the saved pages.

## WF-03: Desktop Reader

| Placement | Proposed content | Behavior |
| --- | --- | --- |
| Result header | Project A / Retry execution model; version 3; Copy link; Import new version; More | Pin the exact directory version; import preselects this document and captures its base before transfer |
| Source/status rows | Source v0.4 / a1b2c3d; My review: Reviewing; reassessment against b2c3d4e | Keep provenance, personal state and freshness separate |
| Personal actions | Mark as reviewed; Revisit | Explicit owner actions; Review covers this entire version |
| Page toolbar | Files/Pages; flows/retry.html; optional Outline; Set as preferred default | Open any page or attachment; preference changes no imported file or last-opened pointer |
| Main canvas | Authored HTML chart/simulation, scenario Partial failure, Step back/Next | Ordinary JavaScript, modules, workers and external resources |
| Context panel | Evidence / Notes / Ask / Links; page flows/retry.html; region retry | Notes and questions use exact page/scenario identity |
| Evidence detail | source/retry.go, commit a1b2c3d, lines 42..71; author evidence; latest source check/history | Open authorized evidence; Check source evidence is enabled with TASK-004-01 and preserves the original record |

The optional Files/Pages picker opens beside or over the canvas and closes after selection. Example entries:

| Relative path | Kind | Reader behavior |
| --- | --- | --- |
| index.html | HTML | Conventional landing page in this example; not mandatory |
| architecture.html | HTML | Open another page in version 3 |
| flows/retry.html | HTML | Open selected scenario page |
| guides/overview.html | HTML | Preserve its path even if another folder has overview.html |
| assets/chart.js | Script | Serve by its original path when a page requests it |
| data/results.csv | Data | Preview where supported or download exact bytes |
| attachments/raw-output.bin | Other file | Preserve, list and download |

Only the selected page and requested dependencies need to load. A plain directory without index.html or metadata gets a useful page/file listing. A broken link shows its exact missing target while other pages remain usable. Two HTML pages may both use region retry; their notes do not collide. Header/sidebar/context controls belong to Haeryong; ordinary reading and page scripts have no repeated approval prompt.

## WF-04: Tablet Reader with temporary context

| Placement | Proposed content | Behavior |
| --- | --- | --- |
| Compact header | Menu, Project A / Retry model, version 3, Copy | Preserve title and exact version |
| Status row | Source a1b2c3d, My review: Reviewing, reassessment needed | Expand details without shrinking the page |
| Page toolbar | Files/Pages, flows/retry.html, Outline | Directory navigation in a temporary picker |
| Reading area | Authored interaction and normal links between pages | Keep readable width and page-local scroll position |
| Temporary side panel | Evidence / Notes / Ask, page and region identity, Close | Contain focus only while overlaying content |
| Host actions | Context, Revisit, Mark as reviewed | Explicit whole-version review action |

Closing a picker or panel returns focus to its initiating control and preserves the selected page. Tablet reading does not require the desktop three-column width.

## WF-05: Phone Reader and sheets

| Surface | Proposed content | Behavior |
| --- | --- | --- |
| Reader header | Retry execution, Project A, version 3, Source details, My review | Compact sticky context |
| Page row | Files/Pages; flows/retry.html | Full-height page/file sheet, accessible current-path label |
| Canvas | Selected authored HTML and scenario | Ordinary touch, zoom and keyboard behavior |
| Files/Pages sheet | Search within result, folders, HTML pages, attachments | Incremental listing with load-more state; return to chosen page |
| Ask sheet header | flows/retry.html, region retry, version 3 | Exact context survives sheet navigation |
| Ask inputs | Pinned source, provider available, scenario partial failure, step 3 | Large context preparation shows progress without a fixed content cap |
| Ask history | Prior answer, captured target and citations | Saved history from the service |
| Footer | Context, Mark as reviewed; keyboard-safe question submission | A tap on a page never completes review |

Ask and Files/Pages use full-height sheets. A disconnected session shows Not submitted; reconnect reconciles the same operation before any new submission. Normal Back/Forward restores page/query/fragment. Download remains available if a device cannot preview a stored file.

## WF-06: Maintenance report

| Area | Proposed content | Behavior |
| --- | --- | --- |
| Run header | Run example, completed with unassessed items, Projects A/B | New scan and schedule settings |
| Coverage | Structural 12/12; semantic 4 assessed, 6 skipped, 2 unavailable | Counts reconcile to captured scope |
| Work budget | Provider attempts 4/20; config revision 7 | Owner's AI work budget, not artifact-size restriction |
| Tabs | Coverage, Freshness, Relationships, Topics, Drafts, Activity | Inspect one concern at a time |
| Finding | Retry claim differs from target; AI assessment; Proposed | Show version 3, flows/retry.html, region retry, target b2c3d4e |
| Evidence and limitation | source/retry.go; external worker configuration unavailable | State what was actually checked |
| Finding actions | Open source, Open affected page/region, Approve, Reject | Recheck current applicability |
| Relationship suggestion | Queue integration relates to Retry model | Explain rationale without claiming an implementation dependency |

An approved finding does not publish rewritten HTML or mark personal Review complete. Unassessed pages remain visible in coverage. A draft offers Reject or Replace files at its current revision; publication stays a separate action. A stale finding offers Request reassessment.

## WF-07: Settings and retention impact

| Area | Proposed content | Meaning |
| --- | --- | --- |
| Sections | General, Connection, Sessions, Projects, AI, Maintenance, Retention, Storage/uploads, Backup, About | Shared owner settings |
| Upload row | Selected result; accepted bytes; exact target; last activity; open/sealed state | Find retained upload after losing its local ID; inspect Resume/Reuse/Abandon availability |
| Schedule | Manual, timezone Asia/Seoul | Explicit automation policy |
| AI work | Concurrency 1, attempts per run 20 | Configurable operational defaults; no artifact byte/file cap |
| Automatic application | Backlink repair Auto; related documents/topics/freshness Propose | Ordinary transactional consistency still runs independently |
| Retention | Trash Indefinite; automatic purge unchecked | A duration alone does not authorize deletion |
| Backup observation | Last complete verified set/time/age or Not yet created; trial restore result or Not run | Inform the retention/purge decision without a backup prerequisite or forced delay |
| Impact preview | 3 trashed documents become eligible; no active document affected | Exact affected data and effective timing |
| Confirmation | Cancel or Confirm exact policy change | Recheck config/resource revision at commit |

The counts are fictional. A stale preview cannot authorize a changed operation. There is no routine maximum-HTML-size or maximum-files setting the owner must raise to import a larger result.

## WF-08: Device pairing

| Area | Proposed content | Behavior |
| --- | --- | --- |
| Header | Connect this device | Content-free until authenticated |
| Service | Configured private HTTPS endpoint | No assumed deployment or actual credential |
| Comparison | H7KP 3QWX; My phone | Synthetic code and untrusted device label |
| Persistence | Remember this device selected | Optional temporary session |
| Instruction | Approve from local CLI or an existing owner device | First-use pairing only |
| Status | Waiting for approval; expires in 04:32; Stop waiting | Pending/expired/rejected states differ; stopping locally is not server cancellation |
| Return destination | Exact document/version/page/query/fragment/region route | Resume after successful session check |

The short-lived pairing secret stays out of the URL. Reloads, new tabs and ordinary browser restarts reuse the remembered owner session. Page location is preserved independently from authentication credentials.
