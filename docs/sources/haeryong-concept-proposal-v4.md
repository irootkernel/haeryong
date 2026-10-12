# Haeryong Concept Proposal

**An AI-maintained, source-grounded knowledge wiki**

**Revision 4** | Markdown baseline extended with first-class browser access, private remote operation, and a configurable 9000-series service port.

> **Status:** Planning baseline, not an implementation or test-completion claim. Preserve the product direction and configurable policies established in Revisions 2 and 3. This revision requires both a Wails v3 desktop client and a browser client backed by the same Go service. Private remote access is part of the product scope, not a deferred optional reader.

## Current direction and Revision 4 decisions

| Area | Direction |
| --- | --- |
| Project documentation | Deliver and maintain Haeryong planning documents as Markdown (`.md`). This file supersedes the Revision 2 DOCX as the working proposal. |
| Implementation language | Use Go for the core, CLI, daemon, storage integration, and AI orchestration. |
| Desktop application | Retain Wails v3 as the desktop framework, with an explicit compatibility and security validation gate. |
| Browser access | Support the same knowledge library from local and remote web browsers without a running desktop application. Share the frontend and service contracts rather than creating an unrelated web product. |
| Service port | The owner prefers the 9000-9999 range. Recommend TCP **9270** as the initial configurable Haeryong port; this exact number is a design proposal, not a checked or reserved allocation. |
| Remote operation | Include authenticated private remote access in the initial connected-reader scope. Recommend a loopback service behind a private HTTPS proxy, with Tailscale Serve as the first deployment candidate. |
| Maintenance cadence | Make schedules and manual-only operation configurable rather than fixing a universal interval. |
| Automatic application | Configure allowed action categories separately; preserve the existing non-negotiable safety and provenance boundaries. |
| Retention | Make retention periods and automatic purge enablement configurable. No fixed number of days is selected here. |

The owner selected Go, Wails v3, Markdown delivery, configurable operating policies, browser access for remote work, and a preference for 9000-series ports. The exact port 9270, shared frontend adapters, remote feature-parity targets, and private HTTPS deployment are recommended implementations of those requirements. No host port availability or running deployment has been checked. A specific JavaScript/TypeScript UI library, operational policy defaults, and a tested Wails release remain to be selected.

This documentation-format decision does **not** replace Haeryong's interactive HTML explanation packages with Markdown. The runtime package remains HTML, local assets, and a manifest.

## Contents

- [1. How the user would work](#1-how-the-user-would-work)
- [2. Explanation packages and source grounding](#2-explanation-packages-and-source-grounding)
- [3. Architecture and security boundaries](#3-architecture-and-security-boundaries)
- [4. Subscription based AI integration](#4-subscription-based-ai-integration)
- [5. Validation and decisions before implementation](#5-validation-and-decisions-before-implementation)

## Purpose

Haeryong helps a person understand AI-generated work through interactive explanations of documents, codebases, commits, and related materials. It stores those explanations as private pages, connects them across projects, and supports annotations, source-grounded questions, and ongoing knowledge maintenance.

Its central value is the connection between an explanation, the exact source behind it, other relevant explanations, and the reader's questions. A visually attractive page without that connection does not meet the goal. The wiki grows from real questions rather than requiring a complete taxonomy in advance.

## Requirements expressed by the owner

| **Capability** | **Direction captured in this revision** |
| --- | --- |
| Create and inspect | Generate interactive explanations with a **Haeryong-owned skill**. Retain source evidence, region notes, and source-grounded AI questions. |
| Connect knowledge | Reuse relevant explanations through stable document, version, and region links within a project and across authorized projects. Provide backlinks. |
| Maintain the wiki | Support user-requested and scheduled scans, relationship discovery, topic enrichment, outdated indicators, and proposals for updates or new documents. |
| Manage personal progress | Use **Review** as an action and **Unreviewed / Reviewing / Reviewed** as personal review states. Keep these separate from AI assessments. |
| Manage the library | Provide search, deletion and recovery, and persistent records. Retain the requirement for supported subscription-based AI access without mandatory separate API billing. |
| Work remotely | Open the knowledge library from a browser as well as the desktop app. Use a configurable service port with a preferred 9000-series default. The remote browser must not require a local Wails installation. |

## Proposed product direction

Use a single-user, local-first baseline: a Go core and one shared Go daemon per operating-system user, SQLite for structured records, a filesystem content store, and desktop and browser clients for the same library. The daemon serves the web application and the versioned API; Wails v3 supplies the desktop shell. Local-first does not mean local-access-only. Haeryong owns its skill and contracts; Aquarium is an integrating orchestrator, not a mandatory runtime dependency. Maintenance cadence, permitted automatic actions, retention periods, and network exposure are explicit user-configurable policies.

The product name is **Haeryong**; use **haeryong** for lowercase identifiers. Go, Wails v3, Markdown delivery, configurable operating policies, browser access, and the preferred port range are the established direction. The detailed mechanisms and delivery stages remain planning recommendations unless explicitly settled. They are not claims of implemented or tested behavior. The web frontend library, supported release matrix, authentication implementation, policy defaults, and other open details are listed in Section 5.

## 1. How the user would work

### Create and open an explanation

The user asks an AI, for example through Aquarium, to explain a selected commit or workflow. The AI follows the Haeryong skill. It first searches eligible existing knowledge, then inspects the authorized source, prepares a package, and submits it through Haeryong's CLI or local API. The daemon validates the package, stores an immutable version, and returns a document reference.

Search within the current project first and expand only to allowed projects. An existing explanation is useful context, not a substitute for checking the source relevant to the new question. Successful import proves contract compliance, not factual correctness.

### Open from desktop or web

The owner must be able to open the same library from the Wails desktop app or an authenticated browser on another device. Browser access is a first-class capability for remote work, not an HTML export or a streamed desktop window. The Go daemon must serve the browser UI even when Wails is not installed or running on that host.

Recommend core workflow parity: search, interactive reading, source/version inspection, region links and backlinks, notes, Q&A when the server-side provider is available, personal review, Revisit, and maintenance reports. Extend library actions, manual scan requests, and policy editing through the same authorized APIs as their features ship. Destructive actions retain the existing confirmation and retention boundaries. This is a recommended scope interpretation of the remote-work requirement, not a claim that the owner separately requested every remote administration feature.

Use a responsive layout for desktop, tablet, and phone browsers. Specify a tested browser matrix, including Safari on iOS/iPadOS and Chrome on Android as initial targets. Keep review buttons and region selection usable with touch; local native dialogs, tray controls, and operating-system service installation remain desktop or local-CLI capabilities. A browser can select an upload from its own device only through a deliberate browser action; it cannot use a native dialog to browse the server's filesystem.

One owner's desktop and browser sessions share version-specific review and notes in the same daemon database. Do not create a new identity or duplicate library for each device. Reconnection must fetch server-confirmed state; do not show an interrupted mutation as saved before acknowledgement.

### Build a wiki through reuse

When a prior explanation already covers a topic, the new document should include enough local context to remain understandable and link to the relevant region for depth. Do not duplicate an entire explanation merely to make a new page self-contained.

> **Illustrative example**  
> Project A has a document explaining selective retry. A later integration explanation briefly describes the retry step and links to that document's pinned version and retry region. Project B may reference the same region when access is authorized. The target shows the new reference as a backlink.

Reference the version actually consulted. A newer target version may be offered separately, but must not silently replace the reference. A later maintenance suggestion is a related-document recommendation, not a claim that the author originally consulted that document.

### Annotate and ask

The reader selects a paragraph, diagram node, or named region and adds a note or question. Haeryong supplies the document version, selected region, question, authorized source references, and the minimum relevant interactive state, such as the chosen scenario, current step, or entered value. This state describes the displayed example; it does not prove real program behavior or grant new permissions.

Questions can target the source snapshot used for the explanation or a selected current source revision. These are separate modes. The UI states the active mode and warns about differences. Answers identify the source revision, cite inspectable evidence, and separate observations, interpretations, and unresolved questions.

Keep each question bound to its explanation version, region, source mode, and provider session. Retain the answer and references for later reading, without storing the model's private reasoning.

### Use interaction where it helps

Controls should demonstrate meaningful behavior: step through a workflow, compare before and after, reveal dependencies, or inspect a state transition. Clearly distinguish a conceptual simulation, an interpretation of source, and a replay backed by a specific execution or test artifact. Smooth interaction is not a correctness certificate.

### Personal review and the reader interface

#### My review status

Adopt **Review** for the action and **Unreviewed / Reviewing / Reviewed** for the state of a user's review of a particular explanation version. Reviewed means only that the user explicitly marked that version as having been examined. It does not certify correctness, approval, or complete understanding.

| **State or action** | **Meaning** |
| --- | --- |
| Unreviewed | No personal review has been started or completed for this version. |
| Reviewing | The user has started examining this version and has not marked it complete. |
| Reviewed | The user explicitly used **Mark as reviewed** for this version. |
| Review / Mark as reviewed | Start-review and completion actions, respectively. These are not AI verification actions. |

Opening a page can record first-opened time, last-opened time, and reading position; opening, scrolling, or an AI scan must never automatically mark it Reviewed. Provide a deliberate way to reopen or clear a personal review state. Merely returning to a completed page need not clear its record.

#### Keep version-specific progress

Preserve the reviewed version and review timestamp. If version 4 is created after the user reviewed version 3, retain the version 3 record and show version 4 as not yet reviewed. An AI judgement that a change is minor may inform the notice, but must not transfer personal completion to the new version.

> **Version-aware reader notice**  
> My review: Reviewed, version 3  
> Latest explanation: version 4, Unreviewed  
> Notice: Updated since your review  
> Action: Review changes

#### Keep three independent state axes

| **Axis** | **Examples** |
| --- | --- |
| Personal review | Unreviewed, Reviewing, Reviewed, recorded per explanation version. |
| Content freshness | Not assessed, reassessment needed, aligned within checked scope, outdated against a target, unable to verify. |
| Lifecycle | Active, archived, in trash, permanently purged. |

A separate **Revisit** flag may coexist with Reviewed. Archive is a lifecycle choice, not a personal review state. An old reviewed document can still be outdated against a newer implementation without changing the historical review record.

#### A focused interface

Provide a library, reader, source panel, and maintenance-results view. Show My review status separately from content freshness. Useful views include Continue reviewing, Updated since review, Revisit, and Needs attention. Keep annotations, questions, related-document recommendations, and status controls outside generated HTML. Reading and permitted state changes should remain available without an AI connection while the selected daemon is reachable. A remote browser losing its server connection is different from the provider being offline: show disconnection and unsaved changes explicitly. Offline browser caching and mutation synchronization are not part of this baseline.

### Lifecycle, deletion, and references

#### Archive without deleting knowledge

Archive removes a document from the default active-library view while retaining its versions, notes, review history, and links. Make archived material discoverable through an explicit search filter. AI discovery should distinguish archived from active material and disclose whether a scan includes it; reduced ranking is a recommended default, not a deletion rule.

#### Delete through a recoverable trash flow

Move a document and its versions to trash before permanent purge. Exclude trashed content from ordinary search, new-reference discovery, and AI source collection. Existing references remain identifiable, but point to an unavailable or in-trash state rather than silently displaying hidden content. Apply the same rule to direct asset access.

Before deletion, show the affected versions, assets, notes, conversations, personal review records, and incoming references. Distinguish unique referring documents from the number of link edges. Let the user inspect those references without forcing them to keep the document.

Restoration should preserve the document and version identities so existing links and personal records reconnect. Background maintenance must not restore, re-index as active, or recreate trashed material just because another document references it. Any proposal to replace deleted knowledge must remain a visible proposal.

#### Purge deliberately

Permanent purge must identify what is being removed and what remains. Remove the selected Haeryong content and its recoverable derivatives according to the retention policy, including indexes and eligible assets. Never delete the original repository, commit, or source document. Assets shared by retained versions must not be removed while still referenced.

Keep only an approved minimal tombstone where needed to explain an existing broken reference. A tombstone should not retain the deleted body or sensitive title by default. An incoming link may report that the target was deleted without exposing its former content. The exact tombstone and retention policy remains an implementation decision.

Purge of a target does not erase quotations already authored into a different immutable document, nor automatically remove copies in existing backups or an external provider's records. State these limits in confirmation and define any separate redaction or backup-retention workflow. Do not claim total erasure from every historical copy.

#### Do not silently rewrite other documents

If document A references deleted document B, retain A's original reference and display the target state through Haeryong's viewer. Do not edit A's immutable HTML or redirect the reference to a vaguely related replacement. A replacement can be proposed and linked as a separate management record, or adopted in a new version of A.

#### Configurable retention policy

Trash retention and backup retention must be configurable, with explicit keep-until-manually-purged behavior. Scheduled purge requires separate user enablement; setting a retention duration alone must not silently authorize deletion. No automatic 30-day purge or other fixed period is selected by this proposal.

The daemon must show the effective policy and eligible items before a change that enables purge or shortens retention. Retention expiry makes an item eligible for the authorized lifecycle workflow; it does not override access rules, references from retained versions to shared assets, or preservation of active documents. Restore, shared-asset handling, minimal tombstones, and the limits of historical-copy erasure remain as described above. Detailed tombstone content and shipped defaults still require implementation decisions.

## 2. Explanation packages and source grounding

### A small versioned package

Treat an explanation as an HTML entry point, local assets, and a manifest. A single self-contained HTML file may be supported, but import still requires metadata. Haeryong owns the package contract and the skill guidance for producing it; the executable enforces the contract.

| **Manifest area** | **Proposed content** |
| --- | --- |
| Identity | Stable document ID, immutable version ID, title, project association, creation time, schema version, and content hash. |
| Content and regions | Entry point, asset inventory, intended explanation question, stable region IDs, and region or claim-to-evidence bindings. |
| Source provenance | Repository or document identity; release label when available; exact commit SHA or snapshot hash; relevant paths, symbols, line ranges, or sections. Record each repository separately. |
| Explanation references | Pinned target document, version, and optional region IDs; relation type; origin or source region; references actually consulted during generation. |
| Interaction context | Declared, bounded state fields needed to explain the current scenario or step. No credentials, arbitrary host requests, or permission declarations. |
| Generation context | Producing tool, skill and package versions, and optional model/version details. No credentials or hidden reasoning. |

### Bind claims to evidence

A document-level list of files is not enough. A reader should be able to move from an important claim or interactive region to the source that supports it. An evidence link is inspectable grounding, not automatic proof that the interpretation is correct. Label illustrative behavior and inferred behavior; cite the actual test or trace when claiming a replay of execution.

For uncommitted work, record a reproducible snapshot or appropriate digest and clearly label working-copy material. A branch name alone is insufficient. A hash identifies captured content but does not itself retain it; missing or unresolvable source must remain an explicit limitation.

### Reliable annotation anchors

Store notes outside generated HTML and bind them to a version and stable region ID, with selected text and nearby context as fallback. A CSS selector alone is too fragile. Keep old notes at their original anchors after regeneration; migrate or reattach only through a reliable mapping, otherwise show the unresolved state.

### Portable rather than machine-bound

Keep local project-to-repository path mappings outside the package. Use logical identities rather than absolute paths for cross-machine portability. Whether selected source snapshots are embedded or referenced only remains a storage and privacy decision. A multi-project explanation may have several independent source revisions, not one global Git hash.

### Knowledge links and backlinks

#### Make region links first-class

Use a stable logical target consisting of document ID, exact explanation version ID, and optional region ID, together with project context. Do not rely on a title, filename, or mutable slug as identity. The exact route grammar remains to be specified, but an ordinary browser-compatible path is required, for example `/documents/{document_id}/versions/{version_id}#region={region_id}`. This is an illustrative route, not a finalized API. Resolve logical targets against the configured deployment base URL; never embed the generating machine's localhost address, port, or filesystem path as canonical package identity. A custom desktop URI may supplement, but must not replace, browser links.

Resolve links through Haeryong's controlled navigation. A deep link opens the intended version and focuses the target region. A latest-version route may be provided for navigation, but a reference used as historical provenance must remain pinned. Show Open referenced version and Open latest as distinct actions when a newer version exists. Browser bookmarks and copied deep links must preserve the exact target across login, reload, and same-library desktop/web navigation. Validate post-login return routes; sharing a link does not grant access.

#### Distinguish relation types

| **Relation** | **Meaning and handling** |
| --- | --- |
| Explicit reference | The authoring AI or user actually used the target explanation. Store it with the immutable originating version and the target version consulted. |
| Source relationship | A relation supported by a specific source fact, such as the same symbol or an interface dependency. Retain source evidence; shared text alone is insufficient. |
| Suggested relationship | A later AI judgement that documents are thematically useful together. Show the reason and AI origin separately from authored references and actual code dependencies. |

#### One edge, two directions

Store a canonical directed relation and derive outgoing references and incoming backlinks from it. Keep backlinks consistent during normal import and updates. A full scan can check or rebuild the index without asking an LLM to infer links that already exist. Backlink counts must remain scoped to visible, eligible records.

The package manifest and generated internal links must agree; validate resolvable targets and region IDs at import. A later unavailable, trashed, or purged target is a visible link state, not a reason to delete provenance. Do not silently substitute another region when an anchor disappears in a newer version.

#### Separate authored content from management overlays

Keep original explanation content and references immutable. Store later topics, AI-related links, freshness findings, and replacement suggestions in auditable management records displayed by the host. An AI may enrich navigation without rewriting the original HTML or pretending those links existed at authoring time.

Only add a topic when existing content genuinely covers it. A useful but absent topic calls for an extension or new-document proposal. Keep user-pinned categories and rejected suggestions intact unless the user changes them or materially new evidence justifies a clearly identified new proposal.

#### Search and scope

Support same-project and authorized cross-project discovery using titles, topics, region summaries, extracted text, and source identities. Start with explicit links and a related-documents panel. Graph visualization, automatic clustering, and a dedicated graph database are not initial prerequisites.

### Freshness, source versions, and historical truth

#### Keep authorship and later assessment separate

Record the explanation version, the source release label and exact hash used at creation, and each later assessment's target revision, scope, time, evidence, and result. Never replace the original source hash merely because a later scan checked a newer commit. Pin a selected release or branch to the actual commit assessed. Show the explanation version, source release label when available, and Git hash together in the reader; identify later assessment targets separately.

Declare whether a document explains a historical snapshot or is intended to describe a moving target. A correct explanation of an old release is not false merely because a new release exists. It can remain a valid historical record while being unsuitable as guidance for the current implementation.

#### Recommended assessment states

| **State** | **What it communicates** |
| --- | --- |
| Not assessed | No applicable semantic assessment is available for the stated target and scope. |
| Reassessment needed | A relevant source, dependency, configuration, or referenced claim may have changed. Impact is not yet established. |
| Aligned within checked scope | The assessment found no discrepancy within its recorded scope. This is not a global correctness or completeness guarantee. |
| Outdated against target | Evidence indicates a specific claim no longer matches the stated comparison target. Show the affected region and reasoning. |
| Unable to verify | Source, permission, provider capacity, or another required input is unavailable. Do not treat this as an up-to-date result. |

#### A changed hash is a trigger, not a verdict

A new repository commit alone does not establish outdated content. Compare relevant source evidence, then inspect possible indirect impacts in callers, configuration, interfaces, dependencies, and tests. Unchanged referenced files are useful evidence but not proof that behavior is unchanged everywhere.

Assess at region level when possible. For example, a document's registration and approval sections may still match while its retry section describes behavior replaced by selective retry. Show that local discrepancy rather than marking every statement in the document wrong.

#### Do not propagate uncertainty as fact

A changed target explanation may make a dependent claim worth reassessing; it does not automatically invalidate every incoming link. A thematic suggestion is not an implementation dependency. Historical references remain pinned, and original sources remain the authority for source-based claims.

Store evidence and uncertainty for AI findings. If a target revision or document version changes during a scan, preserve the result as a historical assessment or requeue it; do not present it as a check of unseen content. My review status remains the user's record even when freshness changes.

## 3. Architecture and security boundaries

### One shared daemon per user

Use one default Haeryong daemon per operating-system user on a machine. It serves a shared library of registered projects, rather than starting a separate server and database inside every repository. Do not require a root-level system service. SQLite is the embedded store; the daemon exists to coordinate requests, the viewer, persistent work, and schedules.

Provide CLI and daemon modes in a headless Go executable as the recommended initial packaging. The Wails v3 desktop application is a separate client process and may be shipped with that executable as a companion. The daemon serves compiled browser UI assets, a versioned API, and authorized event delivery from one application endpoint. CLI, desktop, and browser clients use the same service use cases. Recommend port 9270, configurable per instance. Development or test instances may run separately only with isolated profiles, data directories, endpoints, and identifiers. Starting in another project must not silently create an unrelated default library.

### Implementation stack and client boundaries

#### Selected technology direction

| Layer | Direction and responsibility |
| --- | --- |
| Core and application logic | Go. Implement package validation, references, review state, lifecycle rules, policy evaluation, and maintenance use cases without a Wails dependency. |
| CLI and persistent service | Go. The daemon owns the database, content store, web delivery, API, scheduling, job checkpoints, and final mutation authorization. |
| Desktop shell | Wails v3. Provide the application window and trusted host UI, with a thin client adapter to the daemon. |
| Shared frontend | One web UI codebase for a normal browser and the Wails shell, with platform-specific adapters kept at the edge. React with TypeScript remains a candidate, not an owner-approved selection. |
| Structured storage and content | SQLite plus immutable filesystem packages, retaining the Revision 2 separation. |

**Framework assessment retained from Revision 3, checked 2026-10-10:** Official Wails documentation describes a Go backend and a web frontend using the operating system's WebView [3], and supports multiple frontend frameworks [4]. Its public status describes v3 as beta software with a stable desktop API and recommends thorough deployment testing [5]. These are framework facts, not verification of Haeryong. Do not describe Wails v3 as a final stable 3.0 release on this basis.

Pin the tested Wails module, CLI/tooling, frontend runtime, and build dependencies to compatible versions. Validate startup, navigation, event delivery, packaging, shutdown, and untrusted-content isolation on the selected OS/WebView matrix before accepting a release. Do not use an unqualified latest version as the reproducible release contract.

#### Keep the daemon independent of the UI

Recommended process boundary:

```text
Compatible AI + Haeryong skill -> Go CLI ---------------------+
                                                            |
Wails v3 desktop client -> thin client adapter ---------------+--> Go daemon
                                                            |    Web UI + API
Local browser -> shared web UI ------------------------------+    127.0.0.1:9270
                                                            |         |
Remote browser -> private HTTPS proxy -> shared web UI -------+         |
                                                                      |
                                   +----------------------------------+
                                   |                  |               |
                                SQLite          Content store    Jobs + AI adapter
```

Only the daemon owns library writes and durable schedules. Closing the last window, quitting the desktop client, or a WebView crash must not stop the daemon or cancel its accepted jobs. Explicit Stop service and Cancel job operations are distinct user actions. If a local daemon is not running, the desktop client can offer a controlled start operation; it must not silently create a second writer or a new library. A remote browser cannot start an unreachable host or daemon. Service installation, local start/stop, and network-exposure bootstrap remain explicit local administration actions.

Keep the headless Go executable buildable and runnable without importing the Wails application runtime or requiring a graphical session. Put Wails-specific bindings and window lifecycle code in the desktop adapter, outside core/domain packages. The ability to launch a Wails application without an open window is not a substitute for this process and dependency boundary.

Wails' Go/frontend bridge connects the trusted frontend to registered Go services [3, 6]. It is not the cross-process protocol to the independent daemon. The desktop adapter uses Haeryong's versioned API with user/profile-scoped authentication, endpoint discovery, and compatibility checks; the browser uses an authenticated HTTP client for the same contract. An incompatible client must fail safely instead of migrating or opening the database directly. Remote access is supported, but network exposure remains opt-in and private by default.

This refines Revision 2's packaging recommendation: the CLI and daemon may still share one executable, but the Wails desktop shell is independently executable and does not own the service lifetime. A single repository or installer can deliver both; this is not a requirement for additional microservices.

### Shared web delivery and private remote access

#### Reuse the product UI, not a desktop-only transport

Keep page components, navigation, review semantics, domain types, and API contracts shared. Put Wails imports and native actions in a desktop adapter; ordinary browser builds must not require a native bridge. A small frontend service interface can use a desktop Go proxy or authenticated browser HTTP transport without duplicating business rules. Desktop/browser branding differences must not create separate document state.

Serve compiled assets with the Go daemon, preferably as packaged or embedded build artifacts. The production service must not require a development server or a second production JavaScript server. Keep trusted UI and API on one browser origin where practical; treat generated explanations as separately isolated content even when delivered through the same service. Use an authenticated event stream or bounded polling for job progress, with reconnect/reconciliation support. Exact SSE versus WebSocket transport remains an implementation detail.

**Framework fact checked for Revision 4:** Wails v3 documents a `server` build mode that serves HTTP without native windows and can preserve service bindings [7]. Therefore browser delivery is not ruled out by the framework. Haeryong nevertheless retains its independently buildable Go daemon and versioned API as the baseline. Wails server mode is an optional adapter alternative, not permission to merge the desktop lifecycle into the service or expose all native-bound methods remotely.

#### Port and endpoint policy

| Setting | Recommended baseline | Required behavior |
| --- | --- | --- |
| Application port | TCP `9270`, within the owner's preferred `9000-9999` range. | Configurable; this is neither a registered allocation nor a claim that the owner's machine has the port free. Do not reserve the whole range. |
| Daemon bind address | `127.0.0.1` for local access and the private reverse-proxy topology. | No implicit `0.0.0.0` or `::` exposure. Any direct non-loopback listener requires explicit configuration, authentication, and the approved TLS/network boundary. |
| Private HTTPS endpoint | Prefer `9270` on the private proxy when practical. | Proxy port and daemon port are separate settings. An existing HTTPS gateway may expose `443` or another explicitly chosen port while forwarding to daemon port `9270`. |
| Public base URL | An explicitly configured, validated HTTPS origin for remote links. | Do not construct trusted URLs from an arbitrary Host or forwarded header. Preserve logical document identities when deployment addresses change. |
| Endpoint discovery | Show the configured local address, remote address, profile, and actual listener status. | Detect collisions, display a useful error, and require an explicit port change. Do not silently choose a random port or attach to another service with a matching number. |

The trusted web UI, API, and job events should share the main application endpoint. Do not allocate different public ports for every project or client. An isolated content-serving origin may be added only if the chosen security design needs it; it is not an automatic new network exposure. Bind-address or port changes may require a controlled listener restart and updated proxy rules. Show this before applying the setting and preserve a local recovery path.

Illustrative endpoints, not an existing deployment:

```text
Local browser:   http://127.0.0.1:9270
Remote browser:  https://<node>.<tailnet>.ts.net:9270
Proxy upstream:  http://127.0.0.1:9270
```

#### Private remote deployment

Recommend Tailscale Serve as the initial private HTTPS proxy candidate. Its official documentation describes exposing a local service to authorized tailnet devices, with HTTPS and tailnet access controls; the CLI supports selecting an HTTPS listening port [8, 9]. Use Serve rather than public Funnel for this baseline. Tailscale is a deployment option, not a required library dependency in Haeryong.

Keep the daemon's upstream on loopback and deliberately configure the proxy, allowed origins, and application sessions. Confirm the installed Tailscale version, HTTPS setup, allowed owner/devices, and existing listeners before installation. Do not turn on public access, modify unrelated Serve routes, or assume every tailnet member is the Haeryong owner. Another private HTTPS reverse proxy can satisfy the same contract. Direct LAN/private-interface serving is an alternative only after equivalent controls are configured; opening a port alone is insufficient.

A standalone server should need the Go service and web assets, not a Wails installation or logged-in graphical desktop. Multiple owner devices connect to that one service and database. Running a different Haeryong daemon on a laptop creates a different library unless explicitly connected or migrated; remote access does not imply replication or automatic multi-daemon synchronization.

#### Authentication and browser security

Require owner authentication for private data and mutations, including HTML assets, source excerpts, links, and event streams. Recommend local bootstrap or short-lived pairing into a revocable application session; finalize the exact login flow before implementing remote exposure. Use secure browser session handling with HttpOnly/Secure cookies over remote HTTPS and an explicit SameSite policy. Do not store long-lived service credentials in URLs, HTML, or browser web storage. These controls follow the session guidance in [10]; local HTTP bootstrap needs its own tested loopback-only credential path rather than disabling remote cookie protection.

Enforce authorization in the daemon, not by hiding UI controls. Validate expected Host/Origin, restrict cross-origin access, protect cookie-authenticated mutations against CSRF, and never use GET for state changes [11]. CORS is not authentication. Native desktop requests use their authenticated adapter contract, not a blanket exception for all requests lacking Origin. Proxy identity headers, if supported, require an explicitly trusted ingress and owner mapping; never accept client-supplied identity or forwarding headers as proof. Baseline application sessions remain required even when the private network is authenticated.

Keep generated HTML outside trusted UI privileges in both browser and Wails modes. A different URL path or port alone is not a credential boundary. No native bridge, ambient application credentials, arbitrary network access, or privileged event channel may be exposed to imported scripts. Include hostile iframe/API requests in acceptance tests. Prevent private pages, source data, and responses from entering shared caches; do not install offline caching that defeats lifecycle or logout rules.

#### Remote availability, sources, and in-flight work

Remote reading requires the daemon host and its content store to be reachable. Closing Wails must not stop service, but host sleep, shutdown, or network loss can make the library unavailable. For continuous access, deploy on an always-on host or deliberately configure the chosen machine's availability. This document does not select or provision that host.

Q&A, generation, and freshness checks run where the daemon and approved AI worker can access source material. A phone browser does not expose a laptop's working copy to a remote server. Use an authorized repository checkout, retained snapshot, or a separately designed source connector; otherwise show the exact missing-source limitation. Preserve source revision identity and privacy when transferring material. Remote access does not confer new provider entitlement or authorize copying provider credentials to the browser.

An interrupted phone connection must not cancel a submitted scan or cause an automatic duplicate AI request. Reconnect using the job/request identity and retrieve saved progress. Concurrent desktop and browser edits require mutation preconditions or conflict handling; a stale client must not overwrite newer notes, policies, or review decisions silently. Apply session revocation to subscriptions as well as subsequent HTTP requests. Keep this as one owner's multi-device access, not a multi-user collaboration feature.

### Ownership and responsibilities

| **Component** | **Responsibility** |
| --- | --- |
| Haeryong skill | Own the explanation-generation and wiki-maintenance guidance. Search eligible knowledge, inspect source, produce packages or findings, and invoke supported Haeryong interfaces. |
| CLI and service API | Resolve caller/project context, enforce authenticated requests, and return document or job references. Share use cases across desktop and web; do not permit direct SQL from clients. |
| Daemon | Serve the trusted web UI and authenticated API, validate requests, enforce access and lifecycle rules, manage records and immutable versions, derive backlinks, schedule work, and authorize result application. |
| AI adapter / worker | Use approved inputs and bounded tools; return explanations, answers, evidence, or management proposals. Do not write directly to the database. |
| Reader and content viewer | Show library and host-managed controls. Render generated HTML in isolation and resolve approved knowledge links through the host. |
| Wails desktop adapter | Expose narrow, trusted host actions and translate them into daemon API calls. Do not own SQLite, schedules, or the generated page's execution privileges. |
| Browser adapter | Authenticate to the selected service, invoke the same versioned API, and reconcile server state after disconnection. No native bindings, direct SQLite access, or implicit access to either device's filesystem. |

### The skill belongs to Haeryong

Aquarium orchestrates explanation generation using the Haeryong skill. Haeryong owns the package contract and the instructions for creating compliant explanations. A standalone compatible agent must be able to use the same skill without Aquarium. Keep skill guidance and package/API versions compatible; the executable remains the final validator even when guidance is stale.

Begin with one skill containing generation and maintenance procedures; split it only when complexity warrants it. The skill teaches the process but does not itself run on a timer. The service starts manual or scheduled work and invokes an AI that follows the maintenance procedure. Aquarium may request a scan after work, but need not be running for Haeryong maintenance to operate.

### Storage, consistency, and source mapping

#### SQLite plus immutable content files

Use SQLite for structured relationships and state, with a local file store for explanation packages and assets. This preserves portable packages while allowing consistent search, review history, links, and maintenance records. The database contains original user data, not merely a disposable index.

| **Record group** | **Examples** |
| --- | --- |
| Projects and source mappings | Project identities, allowed repository roots, release/commit references, snapshot availability, and project-specific access policies. |
| Documents and versions | Stable document/version/region IDs, provenance, asset hashes and locations, source-evidence bindings, and lifecycle state. |
| Knowledge and management | Canonical links, topics, topic assignments, AI suggestions, rejected decisions, freshness assessments, and audit records. |
| Reader-owned records | Version-specific personal review state and timestamp, revisit flag, annotations, questions, answers, and reading position. |
| Work and recovery | Scan configuration, run scope, item checkpoints, provider request/result identifiers when available, outcomes, and retry/application receipts. |

Store HTML, styles, scripts, images, manifests, and any retained source snapshots in the content store. Keep absolute project paths in local mappings. Extract searchable text for indexing; SQLite FTS5 is a candidate baseline, subject to testing Korean text and code-symbol retrieval. A separate search service or vector store is not required by the concept.

#### One authority for writes

Route mutations through the daemon. Read the needed version and source context, end the database transaction, perform AI analysis, then check preconditions and apply the result in a short transaction. Do not hold a write transaction while waiting for an AI response. WAL and bounded write serialization are recommended implementation choices to validate under expected usage.

Validate whether the reviewed document, source target, lifecycle state, and user decisions are still applicable before committing a finding. Use stable operation identities to avoid duplicate imports or repeated management changes after retries. Reject conflicting content for an existing immutable version ID.

#### Coordinate files and database records

Validate imports in staging, finalize immutable assets, then publish the database records that reference them. A failure must not expose a half-imported library item. Support recovery and cleanup of abandoned staging data or unreferenced files, while preserving assets still referenced by retained versions.

#### Keep the database local to its daemon

Do not have different machines open a shared SQLite file directly. Both desktop and remote browser clients use the authenticated service API, with the database and content on the daemon host. Private remote access is included in this baseline; the network, authentication, and source-availability boundaries above apply. Cross-machine library replication and automatic source synchronization remain outside the initial scope.

### AI-managed wiki maintenance

#### Full scan means coverage, not indiscriminate AI rereading

Support a user-requested full scan and configurable periodic scans over selected projects or the authorized library. Capture the eligible documents, versions, and source targets at the start. A full scan covers that inventory; it must report structural checks separately from semantic AI assessments. Excluded, changed, inaccessible, or budget-limited items must remain visible as such.

#### Recommended scan pipeline

**1. Inspect structure.** Check package identities, region targets, canonical links, backlink indexes, lifecycle visibility, source availability, and revision changes. Deterministic repairs do not require an LLM.

**2. Find candidates.** Use search, shared source identities, topics, and region summaries to narrow possible connections. Inspect candidate content before proposing a relationship; avoid treating keyword overlap as evidence of an implementation dependency.

**3. Assess meaning.** Compare potentially affected claims with the approved source target. Find missing relationships, relevant themes already covered by existing content, gaps that merit new material, and regions that may require a new version.

**4. Apply or propose.** Submit grounded findings through the daemon. Apply only policy-authorized metadata changes; create proposals or new-version drafts for meaning-changing edits. Publish a run report with coverage, evidence, applied changes, and unresolved items.

#### Recommended automation boundary

| **Change** | **Default treatment** |
| --- | --- |
| Existing-edge backlink repair | Apply deterministically and record the repair; do not invent new semantic relations. |
| Topics and related-document suggestions | May be added as reversible, AI-labelled overlays under the user's policy. Record why the existing content supports them. |
| Freshness findings | Show target, scope, affected regions, AI origin, evidence, and uncertainty. A repository change alone is not an outdated verdict. |
| New material or changed explanation | Propose a new document, or create a new-version draft. Do not silently rewrite published content or original references. |
| Review, deletion, or discarded suggestions | Never mark the user Reviewed, erase review history, purge content, or resurrect rejected material as an incidental scan action. |

Preserve user-pinned classifications and rejection records. Do not repeat the same dismissed proposal without materially changed evidence. Record the actor, run, rationale, affected versions, and reversible metadata operations. Later rollback must detect conflicting user edits rather than overwrite them.

### Persistent work, scheduling, and recovery

#### Make maintenance a durable Haeryong job

Register a job before starting a scan. Closing the CLI, browser, or Wails desktop client must not cancel a job while the independent daemon is running. Persist scope, captured versions, stages, item checkpoints, outcomes, and application receipts so interrupted work can be reconciled after restart. A full run is not complete merely because an invocation returned.

Distinguish queued, running, paused or interrupted, completed, failed, and cancelled work in the implementation. Bound concurrency, avoid overlapping equivalent scans, support explicit cancellation, and make skipped or unresolved items inspectable. Resume recorded work rather than blindly repeating every AI call.

#### Do not assume exactly-once provider execution

When the provider result is uncertain, reconcile available request/session identifiers and saved results before retrying. Do not blindly reapply metadata or silently consume another request when completion cannot be established. Never hold an open database write transaction across a provider call.

Before applying a finding, recheck the captured version, source target, lifecycle state, and relevant user decisions. If a document was updated, trashed, purged, or manually reclassified during the scan, do not apply an obsolete result as if it assessed the new state. Save its limited historical result or schedule a fresh assessment.

#### Scheduling and resource policy

Use change-focused checks during ordinary operation, periodic full-scope inspection, and an explicit deeper semantic reassessment mode. Maintenance cadence, automatic application scope, and retention are required configurable capabilities, not unresolved whether-to-support decisions. Expose project scope, scheduling enablement, interval or calendar schedule with timezone, AI usage limits, concurrency, and permitted action categories. Numeric defaults and the configuration syntax are not fixed in this concept.

The lightweight service can wait for requests without keeping a heavy AI process active. For periodic execution, configure user-level startup deliberately. A sleeping or stopped machine cannot execute scans on schedule; on return, coalesce missed runs according to policy and show the coverage gap rather than claiming continuous verification.

A run report should distinguish documents enumerated, links structurally checked or repaired, documents semantically assessed, AI relationships proposed, classifications changed, outdated findings, and incomplete work. Highlight changes affecting personally Reviewed material without treating that as proof the user now holds an incorrect belief.

### Configurable operating policies

#### Policy surface

The owner requested configurable maintenance intervals, automatic application scope, and retention periods. The following policy model is a recommended implementation of that requirement. Expose effective operating settings in the shared desktop/web UI and through the CLI/API so service operation does not require an open desktop app. Network-exposure bootstrap and operating-system service installation remain explicit local administration; remote policy edits require the same authenticated authority and impact previews.

| Policy group | Configurable choices | Required boundary |
| --- | --- | --- |
| Scheduling | Manual-only or periodic execution; interval or calendar schedule and timezone; full-scope versus change-focused/deeper assessment; selected projects; missed-run handling. | Disabling schedules must not disable manual scans. Do not claim a sleeping or stopped host performed checks. |
| AI resource use | Concurrency, per-run request/usage budget where the provider exposes it, execution limits, retry and pause behavior. | Show which limit can actually be enforced. No unapproved billing/provider fallback or invisible unlimited scan. |
| Automatic application | Per-category disabled, propose-for-approval, or automatic-apply modes where meaningful: scan repair, suggested relationships, topics, and freshness overlays. | Semantic additions remain AI-labelled, evidence-backed, auditable, and reversible; normal transactional reference/backlink consistency is mandatory regardless of scan policy. |
| Content generation | Whether maintenance may propose an update, generate a new-version draft, or draft a new document. | Published content and authored references remain immutable. Automatic draft creation is not automatic publication; publication remains an explicit user action in this baseline. |
| Retention | Separate periods for trash, backups, completed-job detail, and disposable diagnostic/cache data; explicit indefinite retention where meaningful. | Do not use a generic expiration rule to remove active documents, user review history, pinned provenance, or referenced assets. Preserve necessary recovery and audit records. |
| Purge execution | Manual-only purge or explicitly enabled timed purge; confirmation of enabling or shortening the policy; eligible-item preview. | Retention duration does not itself grant purge permission. AI maintenance cannot enable purge or decide that outdated content must be deleted. |
| Network and sessions | Bind address, application port, validated external base URL, approved remote deployment, session expiry/revocation, and allowed origins. Recommend port 9270. | Local bootstrap explicitly enables remote exposure; no public/no-auth fallback, silent port reassignment, or weakened generated-content isolation. Preserve local recovery for endpoint changes. |

Use duration units explicitly. Specify keep-indefinitely as a distinct value rather than overloading zero to mean both immediate deletion and no expiry. A project-specific policy may be offered for schedules and eligible metadata actions; instance-wide backups and service-level data require instance-wide settings. Settings must not broaden separately approved source access or cross-project disclosure permissions.

#### Authority, validation, and change application

The daemon owns one validated, versioned effective configuration. UI and CLI edits go through the same authorization and validation path. Configuration import/export may be offered, but neither a repository file nor an explanation package may silently replace trusted user policy. Final serialization format and import/export precedence are implementation details still to specify.

Record the effective configuration revision with each maintenance or retention job. Validate duration ranges, timezone/schedule syntax, unsupported action modes, and conflicting settings before activation. Invalid changes leave the last valid policy active and return a visible error. Show when each setting takes effect and whether restart is needed; ordinary schedule and policy edits should apply without rebuilding the app.

A running job keeps its captured scope and policy for reproducibility. Before starting another external call or applying a mutation, also recheck the current permissions, relevant policy restrictions, lifecycle state, and user decisions. Revoked permission or a newly disabled automatic action must stop further affected work or leave it pending approval. A later policy expansion must not silently authorize additional actions in an already-running job; use a new authorized run or explicit approval.

#### Safe startup and retention behavior

Recommended first-use behavior is manual scans, semantic changes proposed for approval, and manual purge until the user deliberately enables more automation. These are recommended safe defaults, not owner-selected numeric intervals or retention periods. Structural consistency remains enforced during ordinary import and updates.

Show an impact preview when enabling timed purge or shortening retention for existing items. Once that policy is explicitly confirmed, the dedicated lifecycle job may purge eligible items under it without treating each maintenance finding as a deletion instruction. Recheck eligibility at execution so concurrent restore or active references cannot cause a shared retained asset to be removed.

Configurable automation must never mark the user Reviewed, transfer a review record to an unseen version, rewrite an immutable explanation, broaden source permissions, recreate trashed content, silently override user-pinned or rejected decisions, or switch AI billing mode. These remain product invariants, not settings to disable.

### Backup, migration, and restore

Back up a consistent database snapshot together with the immutable content it references. Restore must recover notes, personal review records, rejected proposals, lifecycle states, and source mappings or explicitly report missing source access. Test interrupted imports, failed schema upgrades, and rollback or recovery paths.

Do not migrate a live database from multiple competing service versions. Keep development and release profiles isolated. After restore, reconcile interrupted jobs and provider sessions before continuing; restoring records must not silently reissue external work. Restore configuration with an explicit review of scheduled AI activity, destructive retention settings, and remote exposure before re-enabling them. Do not revive revoked remote sessions from a database backup; rotate or invalidate restored session secrets and require reauthentication according to the defined recovery policy. Retention is configurable; shipped defaults, export format, and detailed restore semantics remain implementation decisions.

### Security and ecosystem boundaries

#### Generated HTML is untrusted content

Interactive HTML can contain executable JavaScript, malicious links, or embedded instructions. Use an isolated origin or sandboxed frame with a restrictive content policy. Do not grant the page the application origin, authentication cookies, filesystem access, arbitrary network access, or a general-purpose host bridge.

**Wails-specific acceptance requirement:** The trusted application frontend and AI-generated explanation HTML must not share a privilege boundary. Wails exposes registered Go service methods to its frontend [3, 6]; only the trusted host UI may use Haeryong's desktop adapter. Do not render an imported explanation as the privileged top-level application page, inject the Wails runtime into it, or expose daemon credentials to it.

Prove the chosen isolated viewer on each supported native WebView. An iframe or a second window alone is not evidence that native bindings and local API access are unreachable. Test direct and forged bridge calls, navigation attempts, child-frame messages, and direct asset/API requests. If the embedded arrangement cannot enforce the boundary, isolate the content in a verified non-privileged viewer rather than relaxing the requirement. Host-controlled review, policy, and deletion actions require trusted application context.

Permit only narrowly defined messages, such as selecting a region, supplying a bounded display-state snapshot, or requesting approved knowledge navigation. Validate schema, sender, size, document/version identity, and the relevant user action. A valid-looking message must not grant new privileges. Personal review and lifecycle controls remain host-owned.

Reject path traversal, unsafe symlinks, oversized archives, and unexpected executable payloads at import. Keep external asset fetching disabled by default. Validate package references and serve only eligible content. Test browser isolation and the local service's authorization boundary, including protection against untrusted web pages issuing local API mutations.

#### Source access and AI actions

Approve project roots explicitly. A source file, commit message, HTML explanation, note, or linked document is data to explain, not authority to open unrelated directories, retrieve credentials, change source code, or send external messages. Cross-project search, source reads, and outgoing links must respect their separate access scopes.

Enforce Q&A read-only limits with operating-system or tool boundaries, not just a prompt. Disable write tools, unrestricted shell execution, and unnecessary inherited network or connector permissions. Generation may write only to approved output staging; maintenance returns bounded findings or authorized metadata requests, not unrestricted database or filesystem writes.

Do not expose hidden or deleted content through backlink counts, search snippets, related-topic labels, or direct asset URLs. Sending local source to an external model is still disclosure. Make the provider, selected scope, and proposed source submission visible; a retrieved link cannot authorize additional collection.

#### Relation to existing Aquarium tools

Keep Haeryong focused on understanding explanations, connected knowledge, personal review, and its own maintenance jobs. Gul remains the proposed operational console for sessions and workflow state; Doksuri remains the proposed general memo and knowledge experience. Link to those tools when useful rather than rebuilding their broad responsibilities.

Aquarium can invoke Haeryong and request post-work inspection, but must not own its package schema, skill, shared library, or periodic scheduler. Haeryong's internal work queue should stay limited to its needs rather than becoming a general orchestration platform. These ecosystem boundaries are proposals to check against current integration contracts.

## 4. Subscription based AI integration

### Retain a narrow integration candidate, not an entitlement assumption

The original proposal identified Codex subscription sign-in as a candidate and cited the authentication and App Server documentation [1, 2]. This revision carries forward that candidate and the owner's requirement for supported subscription-based use. It does not re-verify provider authentication policy, commercial eligibility, supported versions, or present availability.

Before implementation, verify the chosen local integration and the owner's plan through official documentation and a small test. Do not infer that a consumer subscription can be embedded in any hosted or multi-user service. A commercial or hosted edition, and every additional provider, needs its own supported integration and eligibility review.

### Recommended adapter behavior

**Authentication.** Use an official supported flow. Keep credentials in the provider-managed host environment, never in HTML, packages, browser storage, exported notes, or AI-maintenance findings.

**Visibility and limits.** Show provider, account mode, applicable usage state, and the scope submitted. Explain that Q&A, generation, and maintenance consume the relevant allowance. Configure job limits rather than allowing an unbounded full-library AI pass.

**Failure handling.** When login expires or quota is exhausted, preserve local reading, annotations, personal review, library management, and available deterministic checks. Pause new AI work and provide supported reconnect or retry handling. Clearly label unfinished semantic assessment.

**No hidden fallback.** Never silently switch to separate API billing, another account, or another provider. Any change in billing or disclosure requires an explicit user decision.

**Compatibility.** Pin and validate the supported adapter version. Test startup, authentication, streaming, cancellation, interruption recovery, errors, and permission boundaries before accepting the integration.

### First milestone scope

The first connected-wiki milestone should combine the Haeryong skill, shared daemon, desktop and browser clients, private remote authentication, safe package import, searchable library, version metadata, region links and backlinks, region notes, personal review, recoverable deletion, and one source-grounded AI Q&A path. Prove the complete understanding workflow, including a cross-project reference and same-library access from a second device while Wails is closed, before adding more providers. Provider-based operations remain gated by a supported, verified integration on the service host; document reading and review do not depend on that integration.

Manual AI maintenance and then durable periodic maintenance are recommended follow-on stages within the product direction, not discarded requirements. Freeze exact release boundaries only after feasibility testing. See Section 5 for the rollout proposal and acceptance coverage.

### Defer until justified

Defer shared workspaces, public publishing, cloud synchronization, full IDE capabilities, autonomous source-code changes, silent automatic rewriting of every explanation, video production, broad multi-provider support, and elaborate graph visualization. Private remote browser access, including responsive mobile reading, is now an initial requirement and is not deferred with those features. Native mobile applications, offline browser synchronization, and public multi-user hosting remain outside the initial scope.

## 5. Validation and decisions before implementation

### Acceptance criteria: the connected reader

These are proposed acceptance targets for the expanded concept. They are not completed tests. Apply each criterion to the delivery stage that introduces its feature; do not claim a smaller prototype satisfies the complete AI-managed-wiki scope.

| **Scenario** | **Required observable result** |
| --- | --- |
| C01. Skill ownership | Generate through the Haeryong-owned skill both from an Aquarium invocation and from a compatible standalone agent. Both use the same validated package contract; no Aquarium-owned duplicate generation rules are required. |
| C02. Import and persistence | Import a valid package and reopen it after restart with identical assets and provenance. Invalid or unsafe imports leave no partially visible document. Repeated submission does not duplicate the same immutable version. |
| C03. Interactive reading | A representative control changes a meaningful explanation state. Keyboard navigation, touch interaction, and readable layouts work in the supported Wails WebView and the declared desktop/mobile browser matrix. Local reading and annotations work without a model connection. |
| C04. Evidence-grounded questions | A pinned-source question returns inspectable citations and the recorded source revision. Current-source mode is explicit. Missing evidence and illustration versus observed execution are distinguishable; selected interactive state is carried safely. |
| C05. Pinned knowledge links | Navigate to the exact version and region in the same project and an allowed different project. A copied browser URL works after login and reload without a localhost or Wails dependency. A newer target version does not redirect the original reference. Forbidden targets and their metadata are not disclosed. |
| C06. Backlink consistency | Forward references and backlinks reflect the same canonical edges. Repairing a derived backlink index needs no AI. AI-related suggestions stay separate from authored provenance and actual source dependencies. |
| C07. Stable annotations | A note survives reopening. After regeneration, it remains on its original version or is explicitly reattached through a reliable mapping. Ambiguous mappings never silently attach it to another claim. |
| C08. Personal review | Review starts a personal review; Mark as reviewed records the selected version. Opening, scrolling, AI assessment, or a minor-change judgement cannot complete it. A new version is not automatically Reviewed; history and Revisit remain intact. |
| C09. Lifecycle and references | Archive, trash, restore, and purge have distinct effects. Incoming references show target state. Restore preserves identities. Purge removes only eligible Haeryong data and does not remove shared retained assets or original source. |

A successful import or a Reviewed badge is never displayed as a correctness certification. A search result or link must not bypass a document's current lifecycle or access policy.

### Acceptance criteria: maintenance and operations

| **Scenario** | **Required observable result** |
| --- | --- |
| M01. Scoped freshness | A new unrelated commit does not produce an unsupported outdated verdict. Test a changed claim, an indirect dependency/configuration change, missing source, and a valid historical explanation. Show affected regions and exact comparison targets. |
| M02. Honest scan coverage | A full run reports its captured inventory, deterministic checks, actual semantic assessments, exclusions, failures, and budget-limited items separately. Unassessed material is not labelled current merely because structural checks completed. |
| M03. Useful enrichment | Find and explain a genuinely relevant document relationship and an existing theme. Show AI origin. A missing topic produces a content proposal rather than a misleading tag; published body and original references remain unchanged. |
| M04. Respect user decisions | A rejected suggestion is not repeatedly reintroduced without new evidence. User-pinned metadata and review history survive scanning. Metadata rollback does not overwrite later user edits. A scan cannot restore or newly expose trashed material. |
| M05. Safe application | Change or delete a document while an AI assessment is running. The old result cannot be applied as a check of the replacement. Retries and duplicate results do not create repeated edges, versions, or conflicting management actions. |
| M06. Durable work | Quit the Wails desktop process or close another client, restart the independent daemon, cancel work, and simulate unknown provider outcomes. Saved progress is reconciled, available results are reused, and no blind duplicate external work or metadata application is claimed as safe recovery. |
| M07. Schedule and allowance | Test coalescing of missed/overlapping runs, configured scope and limits, and expired login or exhausted allowance. Local functions remain usable, unfinished AI work stays visible, and no unapproved billable or provider fallback occurs. |
| M08. Isolation | Malicious HTML and source instructions cannot access host credentials, write source, read outside scope, or issue privileged API operations. Untrusted pages cannot mark a document Reviewed, delete it, or gain access through fabricated link/state messages. |
| M09. Restore and migration | Restore database and referenced content together, including notes, review records, rejection decisions, and lifecycle state. Test interrupted imports and migration failure. Development and release profiles cannot mutate one another's library. |
| M10. Headless core and desktop lifecycle | Build and run the Go CLI/daemon without Wails GUI dependencies. Connect the desktop client to the existing profile, quit or crash that client, and verify the daemon and accepted jobs remain alive. Reconnect without a second writer or library. |
| M11. Configurable operations | Change schedule, automatic-action category, and retention settings without rebuilding. Persist them across restart, expose the effective values through UI and CLI, reject invalid configuration, and support manual-only and keep-indefinitely modes. |
| M12. Mid-run policy change | Restrict automatic application or revoke access while a scan is running. Further affected calls or mutations stop or require approval. Expanding policy does not silently expand an existing job. Retain the captured policy revision and report the difference. |
| M13. Retention and restore safety | Setting a duration alone does not enable purge. Confirm an impact preview when enabling or shortening destructive retention. Restore an item during a purge window and retain shared assets correctly. Restoring a backup does not silently reactivate destructive or externally billable work. |
| M14. Wails bridge and compatibility | On the pinned Wails/OS/WebView matrix, verify that untrusted explanation code cannot invoke native bindings or privileged daemon endpoints. Test client/API mismatch, process restart, navigation, events, and packaging. A framework status claim is not a substitute for these tests. |
| M15. Browser and headless delivery | Run the Go daemon and compiled UI without Wails or a graphical session. Read the same library from a local browser and an authenticated second device. Test desktop/mobile core workflow parity, provider-unavailable behavior, and authenticated assets without a development server. |
| M16. Port and exposure configuration | Start on configured port 9270, change it explicitly, and test an occupied port, an unrelated service, and an invalid bind/base URL. No random fallback, second writer, or public wildcard binding occurs. Validate private proxy ingress and local recovery after an endpoint change. |
| M17. Remote authentication | Unauthenticated, expired, revoked, and unauthorized requests cannot retrieve private data, source excerpts, assets, or events, nor mutate review or lifecycle state. Test CSRF, hostile origins, forged proxy identity, unsafe return URLs, and hostile generated HTML in a normal browser. |
| M18. Multi-device consistency | Change a note or review state while another client has stale data. Persist the intended document version and detect conflicting edits. Reconnect after mobile suspension without a duplicate mutation or AI request; accepted daemon work continues after both clients close. |
| M19. Remote source boundaries | Serve a retained explanation while its original laptop repository is unavailable. Reading works, but current-source Q&A and freshness checks expose missing evidence rather than claiming access. An authorized host-side checkout/snapshot is reported with its actual revision. |
| M20. Remote session and restore safety | Revoke a device/session during event delivery and stop further protected delivery. Restore a backup without reactivating revoked sessions or unapproved remote listeners. An offline daemon produces an explicit disconnected/unsaved state, not a false saved result. |

### Recommended delivery stages

**Stage A: Connected reader, desktop and web.** Validate the skill, headless Go service, shared frontend with browser and separate Wails v3 clients, storage, evidence, linked documents, personal review, lifecycle, and one Q&A adapter. Prove authenticated private access from a second device, mobile layouts, independent service lifetime, configured 9000-series port behavior, and generated-content isolation before accepting this stage. Establish validated configuration and effective-policy display early; settings for later features must be clearly unavailable until those features exist.

**Stage B: Manual maintenance.** Add deterministic full-scope inspection, semantic candidate review, grounded freshness findings, reversible overlays, and update/new-document proposals. Enforce per-category automatic-application policy and conflict-safe policy changes.

**Stage C: Periodic maintenance.** Add configurable persistent schedules, bounded execution, interruption reconciliation, coverage reports, and conflict-safe result application. Add explicitly enabled retention execution under its own lifecycle policy. Expand automation only after its effects are inspectable and reversible.

### Decisions to resolve and next step

#### Open decisions, not reopened product direction

Go, Wails v3, Markdown documentation, configurable maintenance/retention behavior, browser access for remote work, and the preferred 9000-series port range are no longer open direction questions. Port 9270 and the private HTTPS topology are current recommendations, configurable rather than hard-coded. The details below remain to be resolved without reopening the required capabilities.

| Area | Remaining decision |
| --- | --- |
| Distribution and release matrix | Choose supported operating systems and minimum versions, the tested Go/Wails toolchain, headless companion packaging and installation, and open-source or commercial distribution. The daemon stays independent of the desktop process. |
| Web frontend and presentation | Choose the shared JavaScript/TypeScript UI framework, component library, browser compatibility matrix, exact isolated viewer, and HTTP/event transport adapters. Wails v3 is the desktop framework, not a selection of React, Vue, or Svelte. |
| Private remote deployment | Choose the always-on host, owner login/pairing implementation, session policy, validated proxy/TLS setup, and packaging. Confirm availability of proposed port 9270 and existing network routes at deployment. These are how-to-deliver decisions, not whether to support web access. |
| Source retention and privacy | Choose referenced-only versus selected snapshots, size limits, sensitive-source rules, local path mapping, and cross-project search/read permissions. Retention must respect preserved source evidence and active references. |
| Contract details | Finalize schema and skill compatibility, daemon API and endpoint discovery, URL/URI format, region/claim bindings, bounded interactive-state fields, and annotation fallback/migration details. Stable version/region identity remains the baseline. |
| Configuration details | Specify serialization and import/export behavior, allowed values and validation ranges, optional project overrides, shipped defaults, semantic-depth/resource limits, and user-level startup behavior. Cadence, automatic-action scope, and retention must be configurable. Meaning-changing updates remain versioned proposals or drafts. |
| Provider integration | Verify eligible authentication, supported versions, and the chosen read-only source access mechanism. Recheck the original integration references rather than treating them as current authorization. |
| Lifecycle and recovery | Define default retention values, explicit purge enablement and confirmation, minimal tombstones, export/restore behavior, migration recovery, and treatment of retained quotes or provider-side records. A duration alone is not purge authorization. |

#### Recommended next step

Start with the Go CLI/daemon serving the shared web UI on configurable port 9270, plus a separately running Wails v3 desktop client. Prove browser access without Wails, then private authenticated access from a second device. Verify that desktop exit does not stop the service and that imported HTML cannot reach native bindings, browser sessions, or privileged API operations. Pin the validated framework/toolchain and browser versions. Then run a small end-to-end prototype using two projects and a few real explanations. Create a region-level cross-project reference and a persisted note, ask a source-cited question, then mark the original version Reviewed. Introduce a relevant source change and generate a new explanation version; confirm that provenance, personal review history, and pinned references remain distinct.

Run a manual library scan to repair a derived backlink index, suggest a supported relationship or topic, identify a changed claim, and produce a new-version proposal. Exercise trash/restore, interruption, source isolation, quota failure, mobile reconnection, stale client writes, revoked sessions, unavailable server-side source, and port conflicts. Exercise configurable automatic-application and retention policies, including invalid settings and a mid-run policy restriction. Add configurable scheduled execution only after the same persisted work path is reliable. Convert the validated decisions into Markdown requirements, architecture decisions, and sequential Epic/Task plans.

### Revision basis and integration references

#### Revision history and source boundary

| Revision | Basis and change |
| --- | --- |
| Original proposal | Interactive explanations, annotations, source-grounded questions, isolated rendering, and a local-first integration candidate. |
| Revision 2 | The supplied `Haeryong_Concept_Proposal_Updated.docx`: Haeryong-owned skill, connected wiki, AI maintenance, per-user daemon/SQLite, version-specific personal review, and lifecycle controls. |
| Revision 3 | The owner's subsequent request: Markdown delivery, Go implementation, Wails v3 desktop direction, and configurable cadence, automatic application, and retention. Preserves the five main sections and all 18 prior acceptance scenarios, extends relevant scenarios, and adds five implementation/configuration scenarios. |
| Revision 4 | The owner requests browser access for remote work and prefers ports in the 9000s. Promote browser/private remote access from optional or deferred to first-class scope. Recommend configurable port 9270, shared UI with independent Go web delivery, authenticated private HTTPS, and remote source/availability boundaries. Retain all 23 prior scenarios and add six remote-operation scenarios, for 29 total. |

Revision 3 process and policy details remain in force except where explicitly refined by the browser requirement. The shared frontend, exact proposed port, recommended feature parity, network/session design, and new acceptance details in Revision 4 are architecture recommendations, not independently approved low-level contracts. External framework/network facts are separated below. No repository implementation was inspected or changed, no running service or firewall was configured, and no application acceptance test was executed by preparing this document.

#### Carried-forward provider references

These original references are retained for verification before integration. Their current provider policy, availability, and eligibility were **not** revalidated in Revisions 3 or 4. Private browser access does not remove this integration gate.

[1] OpenAI Codex authentication. Original reference: `https://learn.chatgpt.com/docs/auth`

[2] OpenAI Codex App Server, Auth endpoints. Original reference: `https://learn.chatgpt.com/docs/app-server#auth-endpoints`

#### Wails references checked for Revision 3

The following official documentation was inspected on **2026-10-10** for the narrow framework assessment in this revision. It does not establish that the proposed Haeryong integration or sandbox has been tested.

[3] Wails v3, *How Wails Works*. Go backend, native WebView frontend, and registered-service bridge. `https://v3.wails.io/concepts/architecture/`

[4] Wails v3, *Using Other Frontend Frameworks*. The web UI framework is a choice in addition to the desktop framework. `https://v3.wails.io/guides/dev/frontend-frameworks/`

[5] Wails v3 homepage, *Wails v3 Beta* status notice. Beta designation and recommendation to test before deployment. `https://v3.wails.io/`

[6] Wails v3, *Go-Frontend Bridge*. Go/frontend service communication. `https://v3.wails.io/concepts/bridge/`


#### Web and remote-access references checked for Revision 4

The following official sources were inspected on **2026-10-10** to verify narrow framework, proxy, and web-security facts. They do not verify a working Haeryong deployment. The exact port 9270 and Haeryong's client/service separation are design choices, not recommendations made by these sources.

[7] Wails v3, *Server Build*. HTTP server mode without native windows, configurable listener, and server-side bindings. `https://v3.wails.io/guides/server-build/`

[8] Tailscale, *Tailscale Serve*. Private tailnet service exposure, HTTPS/access-control prerequisites, and proxy identity trust considerations. `https://tailscale.com/docs/features/tailscale-serve`

[9] Tailscale, *tailscale serve command*. HTTPS listener port selection and local HTTP reverse-proxy targets. `https://tailscale.com/docs/reference/tailscale-cli/serve`

[10] OWASP, *Session Management Cheat Sheet*. Session credentials, cookie controls, revocation, and browser storage boundaries. `https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html`

[11] OWASP, *Cross-Site Request Forgery Prevention Cheat Sheet*. CSRF protection and origin verification for browser mutations. `https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html`
