# Glossary and state vocabulary

| Term | Meaning |
| --- | --- |
| html_js_v1 | Normal generated HTML/CSS/JavaScript, including modules, libraries and network resources under ordinary browser rules; separate from host/native authority. |
| Remembered device | An owner-paired browser that persists its revocable bearer in the trusted application origin; ordinary reload and new tabs do not require new pairing. |
| Library | One daemon-owned collection identified by a persistent `library_id`. Multiple devices accessing it do not create replicas. |
| Profile | Isolated runtime/data/security settings on one host. A development profile cannot mutate the production library. |
| Project | A logical grouping plus remembered owner-approved source mappings and AI disclosure rules, not a separate daemon. The stable owner Unfiled fallback is a normal configurable project, initially without source/provider permission. Workflows default to their selected authorized project; existing documents do not move projects in v1. |
| Document | Stable logical identity for one AI result directory, which can contain multiple HTML pages and supporting files. |
| Version | One complete immutable snapshot of a result directory. A displayed sequence such as v3 is not its primary key. |
| Page | An HTML file identified by (version_id, page_path), where page_path preserves the exact relative UTF-8 path of the resolved HTML entry. Page query/fragment and an optional authored-link navigation_path are navigation context. |
| Result inventory | Service-generated exact path/type/byte/digest records for all provided selected entries; streamed without an author manifest. Entry-aware/native/ZIP input can represent empty directories/links; FileList-only input cannot invent missing entries. |
| Optional metadata | Explicitly supplied Haeryong titles/default-page/source/region/reference information, stored separately from original user files. Ordinary manifest.json remains an ordinary file. |
| Generated manifest | Service-normalized version metadata, including generated identity and payload digest. Its digest is distinct from exact supplied-metadata bytes and the canonical payload inventory digest; authors do not maintain it. |
| Remembered import association | Persistent exact project/document choice, resolved by the package flag table. It does not freeze the base: a new operation may capture latest, but same-operation retries retain their original normalized binding/base. No path/hash deduplication. |
| Workflow binding | Server-allocated remembered workflow identity, with a stable principal across credential rotation. Current project/action choices and issued scope are separate; no scope/project-set hash defines identity. |
| Region | Optional page-local semantic anchor keyed by (version_id, page_path, region_id). Different pages can reuse an ID; cross-page/version correspondence is explicit. |
| Context reference | Opaque handle for captured JSON/text display context transferred incrementally when needed; not a credential or management command. Save/Ask adoption assigns durable record lifetime independently of the originating page channel. |
| Content-delivery credential | Optional narrow read credential for a delivery session, explicitly distinct from the owner/agent bearer and from a nonauthorizing object reference. |
| Delivery session | Multi-request lifetime for one authorized exact version/object/export, including Range/retry; current owner/resource authority still applies. |
| Evidence | Exact source revision, bytes and location used to assess a claim, not a guarantee of correct interpretation. |
| Observed input | Exact captured bytes with an actual provider-attempt observation: daemon_supplied or native_observed. Merely reading a source elsewhere in the daemon does not prove that the provider received it. |
| Posthoc verification | A later exact-target check with captured/matched/mismatch/unavailable outcome. Without supplied byte expectations it records only resolution/capture; with expectations it compares them. It never proves original provider input or claim truth and does not rewrite author capture_status/provenance. |
| Reference | An authored link to an exact directory version and optional page/region. Only resolved authorized targets create validated derived edges; unresolved metadata remains visible. |
| Related suggestion | A later AI/user management overlay, not authored provenance or proof of a code dependency. |
| Backlink | Incoming view of a canonical edge; never a second independently maintained source of truth. |
| Review | Explicit owner state for a selected whole-directory version. Merely visiting any or all pages does not change it. |
| Unreviewed / Reviewing / Reviewed | Personal review states. Only explicit owner action can produce Reviewed. |
| Revisit | Independent owner flag on a document, including one whose selected version is Reviewed. |
| Freshness assessment | A comparison against an actually captured exact target, with capture time, one fixed non-overlapping required tuple set and persisted per-unit outcomes/reasons/assessment linkage. Counts derive from those records. Unknown/partial/zero coverage cannot imply alignment or alter original provenance. |
| Reader preference and position | Owner/version preferred default, owner/version last-opened pointer and owner/version/page reading positions are independently mutable resources. They never change the immutable default or personal review. |
| Full scan | Inventory-wide examination with separately reported structural and semantic coverage. It does not promise every page was reread by AI. |
| Draft | Unpublished candidate package; not a silent replacement of published content. |
| Management overlay | Audited mutable classification, suggestion, or assessment displayed by the host outside generated HTML. |
| Gate | Required evidence with an explicit not_run/pass/fail/blocked decision before dependent implementation or release. Merely recording a result does not make it a pass. |
| Source-change observation | A comparison of recorded source identities/bytes within declared coverage; it can request assessment but is not itself a semantic freshness verdict. |
| Runtime source availability | Whether the currently authorized request can obtain exact source bytes now; separate from immutable provenance and snapshot-retention policy. |
| Operation receipt | Durable state keyed by principal and canonical UUID operation ID. accepted reserves work; completed commits its definitive response (including 202 job creation); the job has its own outcome. Pending duplicates do not imply unknown external execution. |
| Pending upload | Owner-discoverable purpose/target-bound staging. [List summaries](contracts/import-workflow.md#upload-list-and-detail-shapes) report bytes/activity, reusability and pin counts; detail_url supplies errors, checkpoint and detailed ranges. Sealed does not guarantee permanent availability; connection or delivery loss is not abandonment. |
| Attempt generation | Monotonic fence for provider attempts within a job, including retries in one daemon run. |
| Stream epoch | Replay namespace changed by supported restore/stream rotation; distinct from persistent library identity. |
| Not assessed | No applicable semantic assessment for the requested target/scope. |
| Reassessment needed | Relevant change may affect a claim; a discrepancy has not yet been established. |
| Aligned within checked scope | No discrepancy found within recorded scope, not globally current or correct. |
| Outdated against target | An evidenced discrepancy affecting a claim relative to an explicit target. |
| Unable to verify | Evidence, permission, or provider/input availability prevents assessment. |

The [schema registry](contracts/schema-registry.md) owns canonical enum values and wire identity/state scopes. Review uses `unreviewed`, `reviewing`, `reviewed`; freshness uses `not_assessed`, `reassessment_needed`, `aligned_within_scope`, `outdated_against_target`, `unable_to_verify`; document lifecycle uses `active`, `archived`, `trash`, `purged`. Human labels may be localized without altering stored semantics. CLI hyphens map explicitly to JSON underscores. Do not reintroduce Learned or Finished as alternative completion states.
