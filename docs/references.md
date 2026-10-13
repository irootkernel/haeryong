# Source and external verification boundary

## Product source

The supplied [Haeryong Concept Proposal, Revision 4](sources/haeryong-concept-proposal-v4.md) is preserved byte-for-byte. The owner requested review of docs and reviews/2026-10-10 in the supplied repository, authorized documentation changes, and then selected the [personal-developer amendment](changes/2026-10-10-personal-tool-revision.md) and [directory-result amendment](changes/2026-10-10-directory-results-revision.md) with normal JavaScript, multiple HTML pages and minimal input/output constraints. No application implementation, existing epic-handler parser schema, provider account, deployed endpoint or tested frontend/framework combination was established. The [earlier revision record](changes/2026-10-10-review-revision.md) is historical; its original review paths are absent from this checkout and identified as such. The owner authorized the [Opus review reconciliation](changes/2026-10-11-opus-review-reconciliation.md) on 2026-10-11. It distinguishes accepted contract/delivery corrections, qualified findings and untested implementation candidates without reopening D-08/D-09.

## Narrow technical checks

The following official documentation was inspected on 2026-10-10 solely to avoid asserting unsupported framework/security facts while drafting implementation proposals. These notes are not tests of Haeryong. URLs are references, not configured service endpoints.

| ID | Source | Fact used and limitation |
| --- | --- | --- |
| EXT-WAILS | [Wails v3 architecture](https://v3.wails.io/concepts/architecture/) | Documents Go backend, native WebView and registered service bridge. It does not prove generated HTML isolation in this product. |
| EXT-WAILS-SERVER | [Wails v3 Server Build](https://v3.wails.io/guides/server-build/) | Documents an HTTP/server mode. The independent Go daemon remains the selected architectural boundary rather than assuming native bindings are the public API. |
| EXT-TAILSCALE | [Tailscale Serve command](https://tailscale.com/docs/reference/tailscale-cli/serve) | Documents `--https` port choice, loopback reverse-proxy targets, status inspection and persistent Serve mode. The proposed port 9270 is not checked or reserved. |
| EXT-SQLITE | [SQLite WAL](https://sqlite.org/wal.html) | WAL allows concurrent readers with a writer but does not permit multiple simultaneous writers. Long provider calls therefore stay outside DB write transactions. Driver/backup semantics still require a spike. |
| EXT-FRAME | [HTML Standard, iframe](https://html.spec.whatwg.org/multipage/iframe-embed-object.html) | Defines script/same-origin sandbox behavior and separate-origin protection. This supports keeping normal generated scripts apart from the trusted host; it does not establish Wails bridge or process containment. |
| EXT-WEB-STORAGE | [HTML Standard, Web storage](https://html.spec.whatwg.org/multipage/webstorage.html) | localStorage provides origin-associated state across windows and sessions, subject to browser storage policy. It is readable by scripts in that origin; generated pages must not share the trusted application origin. |
| EXT-CODEX-AUTH | [Official Codex authentication](https://learn.chatgpt.com/docs/auth) | Distinguishes ChatGPT subscription login from API-key usage. This alone does not settle distribution/deployment eligibility. |
| EXT-CODEX-SERVER | [Official Codex App Server](https://learn.chatgpt.com/docs/app-server) | Documents sandbox policies, explicit read-access controls and externalSandbox. Read-only does not imply restricted roots; externalSandbox relies on externally supplied isolation. The personal-tool baseline uses appropriate owner-selected provider permissions without claiming a universal worker jail. Exact transport versions need G-AI evidence. |
| EXT-SIWC | [Sign in with ChatGPT](https://developers.openai.com/siwc/token-sharing-open-source) | Describes supported integration/deployment paths. Eligibility is checked for Haeryong's actual personal host and distribution; a working consumer login is not proof that every third-party deployment is supported. |
| EXT-COOKIE-PORT | [RFC 6265, section 8.5](https://www.rfc-editor.org/rfc/rfc6265#section-8.5) | Cookies are not isolated by TCP port. A host-only or __Host- cookie therefore does not establish isolation among unrelated localhost services. This informed the earlier review; 1.2 uses explicit bearer headers for both browser paths. |
| EXT-CSP | [Content Security Policy Level 3](https://www.w3.org/TR/CSP3/) | Section 7.8 specifies policy inheritance for srcdoc/blob and similar local documents. A child policy cannot be assumed to relax its inherited parent policy. Specification text does not prove deployment/browser support or complete egress containment. |
| EXT-PERMISSIONS | [Permissions Policy](https://www.w3.org/TR/permissions-policy/) | Features have individual default allowlists and browser support. An empty iframe allow attribute is not a universal deny-all policy. The personal-tool baseline permits normal browser capabilities and does not require blanket feature denial. |
| EXT-FTS5 | [SQLite FTS5](https://sqlite.org/fts5.html) | Unicode tokenization is not a Korean morphological-search guarantee. Trigram full-text queries shorter than three Unicode characters have no matching rows; short-query behavior needs an explicit scoped fallback and measurement. |
| EXT-GIT-CONFIG | [Git configuration](https://git-scm.com/docs/git-config) | Git operations may consult configured helpers such as fsmonitor and filters. Avoiding shell interpolation alone is not proof that a read command confines helper execution. The personal-tool baseline respects the owner's agent environment and makes no universal same-user confinement claim. |

Additional primary references inspected for the directory-result revision:

| ID | Source | Fact used and limitation |
| --- | --- | --- |
| EXT-URL | [WHATWG URL Standard](https://url.spec.whatwg.org/) | Relative URL parsing uses a base URL; path, query and fragment have distinct encoding/interpretation. This informs preserving the artifact's directory URL semantics and separately encoding host route context. It does not prove Haeryong's selected loader works. |
| EXT-STREAMS | [WHATWG Streams Standard](https://streams.spec.whatwg.org/) | Streaming and backpressure provide incremental transfer and queue control. They do not make a page's own DOM/data memory use unlimited. |
| EXT-HTTP-RANGE | [RFC 9110, section 14](https://www.rfc-editor.org/rfc/rfc9110.html#section-14) | Defines optional byte-range request/response semantics and Accept-Ranges/Content-Range. Haeryong proposes support for its immutable file endpoints; intermediary/browser behavior must be verified during implementation. |

Additional primary sources checked on 2026-10-11 for the follow-up corrections:

| ID | Source | Narrow use and limitation |
| --- | --- | --- |
| EXT-UNICODE-NORMALIZATION | [Unicode UAX 15](https://unicode.org/reports/tr15/) | Canonical equivalence is distinct from compatibility equivalence. Lookup may compare canonical forms without renaming stored entries; ambiguous candidates still need explicit handling. No assertion that every Mac filename has one normalization form. |
| EXT-ENTRIES | [File and Directory Entries API](https://wicg.github.io/entries-api/) | FileList selection and entry-aware traversal provide different information. Haeryong must record what its actual input adapter represents; this specification is not a test of a particular browser's picker. |
| EXT-SERVICE-WORKERS | [Service Workers specification](https://w3c.github.io/ServiceWorker/) | Registration, client control and scope affect renderer mapping/origin reuse. Actual OS/WebView/scheme support must be tested; a specification is not implementation evidence. |
| EXT-WEBKIT-STORAGE | [WebKit tracking prevention](https://webkit.org/tracking-prevention/) | Documents partitioned ephemeral third-party storage. Record actual embedding/site/storage behavior rather than promising identical persistence in all contexts. |
| EXT-CODEX-LOGIN-EFFECTS | [Official Codex authentication](https://learn.chatgpt.com/docs/auth) | Forced login restrictions can log out mismatching active credentials. Managed invocation checks must not mutate shared owner login/configuration as an incidental billing-mode repair. No account or paid execution was tested. |

Those documentation checks established no commercial entitlement, stable Wails release number, package version pin, host availability, provider quota or installed toolchain. Exact choices and evidence are recorded during EPIC-001. Historical framework descriptions inside the unchanged Revision 4 source are historical source text, not new claims of current verification in this bundle.

## Subscription eligibility preflight: 2026-10-13

The owner selected ChatGPT subscription use for a personal Haeryong instance on the owner's Mac, with Wails and authenticated private browser clients on iPhone/Android. No commercial distribution, multi-user service, API billing or public exposure is selected. Codex app-server is the first local adapter candidate; account access has not been exercised.

| Official source checked | Applicable fact / limit |
| --- | --- |
| [Codex authentication](https://learn.chatgpt.com/docs/auth) | ChatGPT login uses subscription access; API-key login uses usage-based access. These modes must not be silently substituted. |
| [Codex App Server authentication](https://learn.chatgpt.com/docs/app-server#authentication) | Local/open-source integrations may continue; migration to Sign in with ChatGPT is recommended. Commercial or hosted services are not permitted through app-server auth. `account/read` exposes the actual account mode/plan; a working CLI alone proves neither entitlement nor supported deployment. |
| [Sign in with ChatGPT plan usage](https://developers.openai.com/siwc/token-sharing-open-source) | Optional plan usage covers eligible Responses API calls for open-source/locally hosted apps. Paid/remotely hosted apps require a separate interest process. This is a possible supported alternative, not an approved adapter switch. |

**Open deployment question:** Does this single-owner local Mac adapter with private remote reader clients fit the documented local-app route? The pages do not expressly classify that precise combination. Private tailnet access alone does not settle it. Before TASK-001-05 provider execution, establish supported applicability from official guidance for the actual construction or bring concrete supported alternatives to the owner. No API-billing fallback, token extraction, forced logout or shared-login rewrite is authorized. Actual account mode, quota, source permissions and exact cited-question behavior remain TASK-001-05 proof; this preflight does not pass G-AI.
