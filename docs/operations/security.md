# Personal-tool security and reliability baseline

## Scope

Haeryong is a personal developer tool for an owner using their chosen generated/imported HTML and existing AI environment. Normal author JavaScript and network/CDN use are supported. Public hostile-content hosting, a multi-user service, universal browser egress control and containment of an unrestricted process in the owner's OS account are outside this baseline.

Protect management credentials, native application privileges, source/provenance accuracy, personal review, lifecycle intent and recoverable data. The baseline uses ordinary authentication, an artifact/host boundary and transactional correctness rather than a separate security platform.

| Boundary | Practical concern | Baseline control | Evidence owner |
| --- | --- | --- | --- |
| Result directory -> store | Paths outside the selected root, interrupted transfer, corrupt content | Exact path handling, incremental inventory/streaming, recorded digests and atomic publication | TASK-002-04, TASK-002-06 |
| Artifact -> trusted host | Accidental or scripted access to management credentials/native calls | Separate execution origin/context, no owner/agent bearer forwarding or generic privileged message bridge | TASK-001-04, TASK-003-03 |
| Browser -> API | Unpaired access, wrong Host/Origin, leaked bearer | Revocable paired bearers, exact Host/Origin checks, closed CORS, private HTTPS | TASK-001-03, TASK-002-03, TASK-007-02 |
| Private network -> library | Assuming network access is owner authentication | Same application pairing/session checks on local and remote routes | TASK-007-01, TASK-007-02 |
| Source/AI -> answer | Wrong revision, accidental scope expansion, unsupported claim | Remembered selections, captured same-byte evidence, native provider permission mode and honest limits | TASK-004-01, TASK-006-02, TASK-006-05 |
| Agent -> owner state | AI marking review, purging or changing settings | Automatically workflow-scoped Haeryong API sessions | TASK-006-05, TASK-008-05 |
| Lifecycle -> shared data | Wrong deletion target, purge race, lost shared object | Owner impact confirmation, exact plans, revisions, pins and guarded GC | TASK-005-04, TASK-005-05, TASK-009-05 |
| Restore -> sessions/jobs | Reusing old sessions or duplicating external work | One-time session invalidation, policy reconciliation and attempt recovery | TASK-005-05, TASK-010-02 |

## Authentication and routine use

Use one loopback API/UI listener with optional private HTTPS reverse proxy. Local and remote browsers send a revocable bearer in the Authorization header; remembered devices store it in trusted-origin localStorage by default. Memory-only use is optional. Idle and absolute expiry default to null until the owner configures limits. Reload, new tab, browser restart and normal daemon restart reuse valid remembered sessions.

The first device pairs through local bootstrap. An existing owner session may approve the next exact displayed code. All owner sessions have ordinary owner management authority; delete/purge, source expansion, exposure and relevant retention changes use explicit impact/confirmation without a second grant or elevation workflow. A confirmed timed-purge policy can run automatically while current eligibility and policy still permit it.

Owner/agent bearers, bootstrap secrets and provider credentials never enter artifact frames, messages, URLs, logs or generated packages. Store only session-token hashes on the server. Renderer/download authority, renewal, observed closure, restart recovery and credential exposure follow the normative [content delivery authority](../contracts/authentication.md#content-delivery-authority). Private reads, assets, events, receipt replay and mutations require their applicable current authority; redact delivery credentials alongside management secrets. Bootstrap remains a local recovery path.

## Trusted UI and artifact execution

Run `html_js_v1` outside the trusted host origin and native bridge, with no authenticated API proxy exposed to artifact code. Allow normal DOM, scripts, modules, frameworks, WebAssembly, network/CDNs and browser permission behavior. Do not treat network access as a viewer security failure or require a JavaScript DSL. The exact origin/loading construction must support multiple HTML pages and ordinary relative/root-relative assets, modules, workers and media from the same immutable directory version.

The trusted UI renders titles/errors as text and notes/answers through a safe Markdown policy. Treat raw answer HTML as text and reject executable link schemes; untrusted Markdown is not a route for installing application scripts or privileged frames. Its own scripts and management requests use a build-defined CSP structure and response policy. Generate source values from validated runtime configuration and the active renderer mapping: trusted application/runtime script sources, actual API connection sources and renderer frame sources, no arbitrary objects or base-URL injection, and no third-party framing of the trusted UI. Renderer frame-source allowances remain separate from management Origin authorization. Record the effective browser/Wails policy, including any required native-runtime sources, and keep no-store, nosniff and no-referrer on private responses. These rules govern trusted UI code; the renderer must separately retain ordinary inline/module/framework/WASM/CDN behavior, including when an embedding mechanism would otherwise inherit CSP.

Raw file/export responses on the trusted origin use `Content-Disposition: attachment`, `X-Content-Type-Options: nosniff` and a restrictive document policy such as `Content-Security-Policy: sandbox; default-src 'none'`. Previewing executable HTML, SVG or XHTML uses the unprivileged renderer, never a trusted-origin document created by direct navigation, iframe, object or a Blob URL. A known inert thumbnail may be displayed safely by the host; other format preview support remains separate from preserving/downloading its bytes. Renderer page/asset responses use the selected normal-web execution policy rather than inheriting the restrictive raw-download policy. Verify these response distinctions in the implemented loader.

Review, delete, settings and publication controls stay in the host. Viewer messages may update reader context or request a declared library reference; they cannot make authenticated management calls. An external link does not load into a privileged Wails document. Browser feature prompts follow the selected platform's normal permission rules rather than a blanket generated-content feature denial.

A separate origin does not promise a separate process or CPU/memory containment. Provide load errors, reload/close actions and an application restart recovery path when a script hangs. Saved data and durable jobs remain the daemon's responsibility. Artifact network requests and previously displayed content cannot be recalled.

## Source and AI behavior

Remember owner project/source/provider selections and automatically recheck them at reads and result application. Do not prompt for each unchanged read. Haeryong does not autonomously edit source repositories, send its bootstrap/provider tokens as evidence or silently widen collection/billing mode. Captured source bytes and recorded provenance remain distinct from artifact runtime network data.

External agents use the owner's existing environment. Managed adapters record native provider permission mode, working directory and relevant configuration. Haeryong API scopes are enforced within the service; broader tool/read limitations may be best-effort and must be described that way. Artifact runtime context and source excerpts are data for the owner's requested question, not instructions to expand collection, perform source edits or invoke Haeryong management actions. Use the provider's supported native controls for the task and record their actual limits; a tool-free adapter remains optional. A worker jail, disabling every plugin/hook or proving zero egress is not a baseline gate. See [AI adapter](../contracts/ai-adapter.md) and [source access](../contracts/source-access.md).

## Storage, recovery and audit

Immutable publication requires durable content before the DB references it: staging write, file flush, publish/rename, required directory flush, referencing DB commit, then success response. Use the same digest-verified stored bytes for rendering/source results; authored runtime/network-derived responses remain distinct under the [viewer integrity contract](../contracts/viewer-protocol.md#incremental-retrieval-and-integrity). Verify requested content incrementally into immutable delivery storage or by recorded chunks and then stream it; neither rendering nor export requires the whole directory or a large file in RAM. Report mismatches and interrupted finalization as content/recovery failures.

There is no Haeryong-imposed HTML/package size, file-count, directory-depth or file-type limit. Automatic inventory and optional metadata accept ordinary result directories without author-written manifests. Preserve every selected file, including unknown attachments, and all relative paths on import/export. Missing source metadata gives an unknown/unverified basis while the files remain usable. Progress, backpressure, cancellation/resumption and truthful disk/browser/connection failures handle actual resource limits. An internal buffer or batch size must not become a hidden content cap or silently discard files.

Default backup is a coherent set of private local files with a consistent database snapshot, referenced objects and checksums. Encryption is optional for owner-selected exported/shared copies, using an established supported format if chosen. There is no mandatory encrypted-container or recovery-key platform gate. Checksums detect corruption and do not authenticate a maliciously replaced backup. See [backup and recovery](backup-recovery.md).

Application-managed restore invalidates restored sessions once and reconciles source/automation policy and uncertain external attempts. After recovery, normal restarts retain new sessions and remembered selections. Purge removes eligible live records/objects and does not promise forensic erasure from storage history, backups or provider records.

Use existing audit/outbox records for pairing, session revocation, source selection changes, confirmed policy/lifecycle actions and restore. Record actor, target and result without secrets. This remains ordinary product history, not a tamper-proof audit platform.

## Focused release checks

G-AUTH, G-VIEW and G-AI remain `not_run`. Record versions and results for the selected Wails/browser/provider construction. Check remembered authentication and storage-loss recovery, a real private HTTPS device, artifact owner/agent-token/native-bridge separation, two concurrent versions with distinct root-relative asset bytes, multi-page navigation with normal script/module/worker/WASM/CDN functionality, large streamed import/export and page-context transfer/adoption, source-grounded provider work and failure/recovery behavior. The complete [TASK-001-04 closing checklist](../tasks/EPIC-001/TASK-001-04.md#observable-completion-and-tests) owns G-VIEW's required environments and evidence before G-CONTRACT; its referenced [viewer procedure](../contracts/viewer-protocol.md#focused-g-view-evidence) supplies worker replacement, origin reuse and storage details. The feature-level checks above retain their assigned Epics: production import/export and saved-context adoption are not additional early prerequisites. Required unavailable gate evidence remains blocked.

Verify the release build exposes only intended native bindings/listeners and does not ship accidental development entrypoints. Check native calls from artifact child frames and after navigation, trusted Markdown/link handling, raw-file response headers and executable-preview origin; safe stored bytes do not establish a safe response origin. Repeat relevant checks when their configuration changes. Do not require a universal hostile-content, WebRTC/DNS or unrestricted external-agent containment matrix for personal use.

Public documentation must describe host/source availability, remembered-session behavior, external dependencies/provider retention and recovery limits plainly. A later public or commercial service reopens its own deployment and authorization design. This specification records no completed application tests.
