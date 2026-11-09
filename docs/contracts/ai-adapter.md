# AI adapter and Haeryong skill contract

**Documentation baseline 1.6.0, proposed runtime v1.** **Inherited:** supported subscription access without mandatory separate API billing; one verified provider first. **Owner direction:** reuse a developer's chosen agent environment and favor a straightforward working integration. **Candidate:** official Codex integration on the service host, subject to G-AI. This document does not assert implemented support or grant provider usage entitlement.

## Adapter boundary

The application calls a typed internal adapter with capability probe, authentication/availability status, generation/question/assessment start, normalized progress, cancellation request, supported attempt-status query and output recovery. Unsupported capabilities remain explicit.

Haeryong supplies selected evidence as excerpts or through source-reading functions when useful. For managed Q&A and assessment, select and record a supported read-only/non-writing mode when available; report the actual effective behavior rather than claiming universal tool containment. Generation separately needs the selected output destination. A tool-free broker is an implementation option, not a required architecture. No separate OS worker jail, blanket disabling of hooks/plugins/MCP, neutral working directory or universal tool/network containment proof is required for the personal baseline.

An external agent invoked by the owner uses that owner's existing environment, including its configured instructions, tools and provider sign-in. Haeryong-issued credentials limit what that agent can do through Haeryong's API; they do not restrict everything the process can read or execute on the host. For a managed provider adapter, record the chosen working directory, effective settings, native permission mode and relevant inherited capabilities. Describe application-selected scope and best-effort behavior accurately instead of claiming OS isolation.

Haeryong does not autonomously edit source repositories. Q&A and maintenance request reading and interpretation, and generation writes its result directory to the selected artifact destination or Haeryong staging. An owner-selected result directory may be inside the project; generation must not overwrite unrelated source files. Use supported native provider controls to avoid source edits where available and report their actual limits. A provider's `externalSandbox` setting does not create a sandbox; do not enable it merely to describe ordinary trusted-owner execution as isolated.

Normalized status distinguishes `ready`, `auth_required`, `quota_unavailable`, `quota_exhausted`, `unsupported_version`, `source_unavailable`, `paused` and `provider_error`. Unknown remaining quota is not zero or unlimited. Application limits such as attempts, concurrency and deadline are recorded separately from provider telemetry.

## Remembered selection and input

A request records job/attempt IDs, explicit user intent, document/version/page context and optional page-local region, JSON display state or its context_ref, source mode and exact targets, selected evidence, provider/configuration, operation output contract and deadline/cancellation behavior. Large context is staged automatically and read incrementally; it does not have to fit a small control-message envelope. The owner selects the project, source roots, provider and permitted disclosure/output use; these choices are remembered. Reusing an unchanged selection does not prompt for every read or provider request. An actual scope expansion or billing/provider change remains an explicit owner action.

Source text and existing explanations are evidence, not authority to mark personal review, purge or change Haeryong settings. An owner's external agent may load its ordinary instruction files under that host's rules; Haeryong must not present that environment as if all repository instructions and tools had been disabled.

Before a managed submission the daemon automatically checks the saved current source/project/provider policy. For evidence it actually supplies, capture the permitted bytes and record their read_id, target_set_id, exact revision/digest, size and an attempt-linked disclosure inventory and input observation with input_origin daemon_supplied. Record input_origin native_observed only when supported native-tool telemetry identifies the actual bytes and exact source identity for that attempt. A native tool's reported path, successful exit or checkout HEAD alone is not byte-level input observation. Unobserved external/native reads remain unobserved; the selected disclosure inventory describes Haeryong's supplied/observed inputs, not every ambient action of an unrestricted owner process. Enumerate and stream large selections incrementally. Provider context, attachment, quota and deadline limits are reported as capabilities of that provider or the selected job settings, not imposed as Haeryong directory-import, HTML-size or file-count limits. Do not copy raw source or secrets into diagnostic logs. Package-local evidence IDs retain their declaring version scope. Source-reader calls follow the same saved selections and exact-target rules.

Provider credentials stay in supported host-side storage/transport. Haeryong does not copy tokens into browser sessions, artifacts, prompts or findings and does not deliberately include its bootstrap/profile or provider-secret files as source evidence. This is an application-handling rule, not a promise to contain an unrestricted external agent in the owner's account. Provider and external-agent network use follows their normal supported environment.

Restored profiles reconnect through the supported provider authentication path and reconcile potentially newer attempts before further external work. The absence of a receipt in an old backup does not authorize a duplicate provider call.

## Output contract

| Operation | Required output |
| --- | --- |
| Generate explanation | A result directory using normal `html_js_v1` pages, related files and subdirectories; optional page/region/evidence/reference metadata and truthful source-basis/limitation reporting |
| Answer question | Answer with exact source targets, typed citations, observation/inference distinction and missing-evidence limits |
| Assess freshness | Exact version and scope kind version/page/region with nullable page/region as appropriate, comparison target, typed evidence, coverage, assessment state and rationale; no automatic source edits |
| Discover related content | Candidate IDs, relationship type, supporting content and reason |
| Propose update/new document | Proposed scope, affected version and reason, intended evidence and estimated operation count if available; draft remains unpublished |

The agent may generate multiple independent or linked HTML pages with ordinary JavaScript, frameworks, modules, WebAssembly, shared assets and network/CDN use under the [viewer contract](viewer-protocol.md). It need not merge them into one HTML file, rename every page, inline every asset, satisfy byte/file-count/depth/type limits, create an index page or hand-author a manifest and file hashes. Haeryong automatically snapshots and inventories the selected directory. Optional metadata enriches navigation, annotations, citations and wiki links; ordinary files and links render without it. Runtime-fetched content is not automatically verified captured source.

Importing/viewing an existing result succeeds without source metadata or captured-read evidence. Keep that source basis unknown/unverified and preserve any supplied provenance as supplied. A source-verified claim or freshness conclusion requires matching actual evidence; inventing citations cannot upgrade the result's status. Generation can report incomplete source coverage while retaining its useful files. Missing metadata is not a reason to discard or silently rewrite them. Files the chosen provider cannot interpret remain valid result attachments.

Validate completed project-source citations using the normative [shared citation union](schema-registry.md#source-and-assessment-views) and [source checking workflow](source-access.md#checking-imported-evidence). observed_input needs an actual attempt input observation; posthoc_verified needs a successful exact-target check, not invented telemetry. The latter may be a limited captured result without an expected quote/digest, or matched against supplied expectations. No quote is compulsory for raw import. Acquisition, author-byte agreement, semantic claim support and current availability are separate facts. The producing attempt and evidence lifetime rules remain required; save checks before permitted temporary evidence is cleaned.

Current-source Q&A at revision B cannot reinterpret revision A's evidence or rewrite its immutable explanation. Saved question responses compose authorized citations/excerpts with current availability; cleaned or absent bytes remain explicitly unavailable. Missing evidence is not a freshness verdict or grounds for an invented confident answer. A requested pinned/current source answer cannot silently become an explanation-only success or a verified empty target set. Saved explanations may support clearly labelled commentary, with the missing source basis and unable-to-verify limitation visible. Conceptual illustrations need no project-source citations. Explanation-only references use the registry's separate explanation_references collection with exact stored version/page/hash identities; they never fabricate a project target, read observation or post-hoc source verification. Source-specific generated claims need actual matching evidence before they are represented as source-supported; otherwise expose their unverified basis while retaining the useful files. Optional metadata and incomplete evidence never become an import admission gate. Model-written paths do not expand the daemon's selected collection scope.

## Skill packaging and workflows

Implement `skills/use-haeryong/SKILL.md` with version-compatible generation and maintenance guidance. The [skill draft](../examples/use-haeryong-skill.md) is shared by Aquarium and standalone agents. Validate installation in the actual agent host.

Generation: use the remembered project/source/provider selection; inspect service capabilities and eligible prior documents; inspect actual source when the task calls for it; build a useful directory of ordinary HTML/JavaScript pages and related files; optionally record page-local regions, evidence and references; write to the configured result destination; let Haeryong inventory and validate the complete directory; import with a stable operation ID and the owner's publication intent; return the logical directory link, useful page links and limitations. A selected directory, ZIP or standalone HTML may enter the same workflow without hand-authored packaging metadata. Preserve original files and relative paths on import and export.

Maintenance: request a scoped inventory; inspect relevant source changes and prior content; submit typed findings through Haeryong interfaces. Do not directly edit its DB, rewrite immutable HTML, author personal review or set deletion policy. The skill is workflow guidance, not the scheduler or an OS security boundary. Workflow-scoped sessions are issued automatically from the selected work rather than configured through a separate manual grant system.

## Focused G-AI evidence

[The bounded provider/source experiment](../testing/provider-source-spike.md) records G-AI `pass`; this does not implement the full proposed production adapter. Its sole closing list is the entire [TASK-001-05 observable checklist](../tasks/EPIC-001/TASK-001-05.md#observable-completion-and-tests), including source checks, actual remembered permissions and supported real authentication/billing, not only its last checkbox. One successful real cited question is required. A thin real-adapter harness checks missing source, error/cancel and interrupted/unknown-outcome reporting without blind retry; distinguish injected transport faults from live observations. The first working input may be daemon-supplied excerpts. Post-hoc verification cannot pretend to observe original input; native_observed is claimed only when actually demonstrated.

Multi-page generation/usefulness belongs to G-VALUE/TASK-001-07. Production manifest-free directory import belongs to EPIC-002, and durable provider-attempt recovery, saved questions and end-to-end generation/import belong to EPIC-006. These remain required product behavior, not extra G-AI closing prerequisites. A fake provider can support a fault test but cannot replace the real supported-account question. Do not implement future production services merely to close this early gate.

For the selected provider binary/version, record a tested input path: actual daemon-supplied excerpts/source-tool responses, native telemetry correlated to exact captured bytes, or explicitly labelled post-hoc verification. A snapshot directory made available to a CLI is not proof that all its files were read. Do not require native telemetry from every supported external agent or infer current CLI behavior from another provider/version.

Before managed dispatch, verify the effective provider/account and authentication/billing mode without logging secrets. Prevent unintended API-key environment overrides in the child invocation where the supported integration permits it; never edit the owner's global environment, shared login store or global CLI configuration, trigger logout, or switch account/billing as a side effect. If the effective mode cannot be confirmed, report that AI action as unavailable rather than guessing or silently falling back; ordinary reading/import remain usable. Any run-local login restriction must be tested for shared-credential side effects before use. [Official authentication documentation](../references.md) supplies the external basis; configuration snippets are not permission to mutate the owner's account.

Official documentation distinguishes subscription sign-in from API-key billing; [references](../references.md) records that narrow basis. Public/commercial distribution and a different managed deployment need their own eligibility review. A working fake does not close the real-provider condition.

Do not automatically request an API key, copy account tokens, use an unsupported sign-in path or silently switch billing/provider to bypass a failure. If the chosen supported route is unavailable, show that state and let the owner choose a supported alternative. G-AI is not conditional on eliminating every ambient capability from an external owner's agent environment.

## TASK-006-03 production transport

The production `internal/adapters/ai/codex` factory uses Codex app-server over
host-local stdio. Its verified deployment is Codex CLI 0.164.0 on Darwin ARM64,
SHA-256 `d8990e3ee7acfbc26f91265b12a1a83f0fee40d0e0f8f1f40a37d4ec21f499cf`.
The version-specific stable JSON schemas are regenerated from that installed
binary. An unverified version/hash is unavailable; the adapter never upgrades,
downgrades, installs or substitutes another provider. `daemon run
--provider-binary /absolute/pinned/codex` selects an already installed executable;
without it PATH discovery still requires the same verified pin.

Before inference, account/read with refreshToken=false must identify ChatGPT
and requiresOpenaiAuth=true. Effective configuration is read for the exact selected absolute CWD; a null
configuration is unavailable. Effective configuration and the returned thread
must agree with the supported OpenAI route, native read-only sandbox, disabled
sandbox network access, approvalPolicy=never and selected working directory.
Ordinary documented OpenAI and ChatGPT backend URLs are accepted. Unsupported
provider declarations or nonstandard root OpenAI/ChatGPT endpoints are refused.
API-key override
variables are removed only from child environments. Owner account/configuration,
instructions, plugins and the default model/effort are not rewritten. The audit
records actual model/effort, working directory, native permission mode and
instruction/disabled-plugin counts. A bounded pre-send snapshot distinguishes
configured web-search mode from observed connected MCP server/tool counts.
Unsupported, incomplete or failed telemetry remains unknown; app availability
is unknown. Names, descriptions, raw configuration and secrets are not retained.
This is neither a complete inherited-tool inventory nor an OS read jail.

The initial supported input path supplies complete exact captured UTF-8 text;
unsupported encoding fails that attempt before inference without changing stored
source or directory import. Canonical source-identity order is used in both the
semantic request digest and actual disclosure frame. Source/output reads recheck
current intent, runtime and policy through their consumers. Full turn/start
write plus its matching accepted turn ID precede durable daemon_supplied input
observations. A buffered matching ACK wins over simultaneous local cancellation
or EOF only after that complete write; absent/malformed/error ACK or an incomplete
frame cannot invent delivery. Local RPC correlation is not provider deduplication.
Native input
telemetry is not claimed.

Only an exact successful completed turn and explicit final_answer agent messages
can produce a saved public result. Commentary, partial_answer, reasoning, unknown
phase and EOF cannot complete it. Completed with no supported final result is
result_unavailable, not backend failure. Known native failure and live interrupted events retain their terminal facts
separately. Pinned history APIs synthesize interrupted for inactive unfinished
turns, so persisted interrupted remains unresolved even after interrupt ACK. Cancellation requests the exact owned turn;
interrupt acknowledgement alone proves no cessation. Local cleanup verifies the
owned process group, leader and readers with bounded waits; cleanup uncertainty
is explicit and independent of backend state on every post-start return,
including no-send preparation. Available complete audit facts retain cleanup;
early incomplete setup reports a bounded diagnostic without invented identity.
No-send stays prepared with its primary reason. Recognized copy/Apply authority
refusal is ordinary refusal, while genuine cleanup/storage/audit errors remain
visible at Stop without ending Poll/Reader/Review service.

Reconciliation uses persisted exact version/hash/thread/turn identity and a
current executing intent. Metadata-only pages explicitly request itemsView
notLoaded. Status-only/hidden/orphan carriers never load source or output bodies;
authorized full-history/output reads recheck current policy on each page/chunk.
A completed live event with summary/notLoaded/unknown view cannot supply saved
output: recover the exact turn with full history. Temporary retrieval/context/
spool/persistence failure keeps
outcome_unknown/result_retrieval_pending with known completion and exact IDs,
so explicit reconciliation can recover without another Execute. Definitively
unsupported full output, status-only suppression and actual authority refusal
remain failed/result_unavailable. Omitted itemsView has the pinned defaultFull
meaning; explicit
unknown/null does not. Summary contains the whole last message, but can omit
earlier final messages. Every supported final message from the full view is
retained. Wrong identities and missing full history cannot substitute output.
No uncertain request is automatically resent. Saved continuation remains the
existing verified no-Execute branch.

GET/HEAD /providers, CLI providers status and the shared Settings/AI screen expose
credential-free operational status. A fresh catalog is not entitlement. Unknown
quota stays unknown; ordinaryUsageAllowed=false alone reports paused usage,
without inventing an exhaustion cause. Explicit native usage-limit exhaustion,
auth failure, rate limiting, session budget and context limits remain distinct.
Task03 leaves generation/publication and saved-Q&A admission to TASK-006-04/05.
The Task04 factory exposes implemented generation separately from provider
readiness; Task05 now composes typed questions separately. features.provider denotes
the transport rather than entitlement.
See the [implementation record](../testing/provider-adapter.md).

The [official app-server authentication notes](https://learn.chatgpt.com/docs/app-server#auth-endpoints)
preserve existing personal local/open-source authentication use. This bounded
host-local integration does not claim vendor production support or commercial/
hosted eligibility and does not adopt the separate token-sharing route.


Preflight/status/lookup use bounded local operation contexts; normal model waits
and progressing completed-output copies have no new fixed total duration cap.
Cancellation before accepted-ID registration closes the owned stalled writer;
after registration, one interrupt reservation targets only that owned turn.
Unknown capability telemetry cannot permit dispatch after setup has expired.

The adapter returns native outcome, complete spool and audit attachments to the
existing coordinator. Complete authorized output is saved independently before
final audit and application; known failed/live-interrupted facts also commit
independently of secondary audit. Required audit failure stays visible and
cannot become success. The exact finite reconciliation campaign is abandoned
with its pending siblings, without running the successful carrier finalizer.
No audit-repair RPC, automatic retry queue or fabricated carrier completion is
introduced. A later ordinary saved-result continuation does not call Execute.
Handled AI accounting failure does not stop Reader/Review or dispatcher service;
Process and Stop retain it, including after Poll has collected the phase.
Only an already-collected diagnostic whose durable settlement and owned drain
were positively verified can pass through controlled restart; the first such
diagnostic remains reportable at final daemon stop. Current phase errors,
unverified cleanup and unrelated joined errors continue to refuse restart.
The concrete producer reports explicit local cleanup/no-owned-transport facts
before optional complete native audit is available. Internal LocalClosed with a
known successful LocalCleanupReason is separate from backend cessation or input
observation; default/unknown facts and nil CleanupError alone are insufficient.
A safely closed pre-audit disclosure failure or no-observation lookup can thereby
remain a handled historical diagnostic after durable settlement and collection,
without fabricating a Transport, Lookup or accepted identity.
An internal setup deadline with a live caller is setup_timeout, preserving
prepared/no-send accounting and truthful caller/fence refusal semantics.

## TASK-006-04 managed complete-directory generation

The trusted generation service captures explicit project/document/base,
publication-versus-draft intent, current source selection and the one owned
skill version/hash before provider dispatch. Generation has a fresh private
per-attempt CWD and result directory; runtime-derived destination/skill paths and
operation IDs are not semantic request entropy. A new attempt cannot merge an
older partial tree. The adapter verifies the materialized guidance bytes,
actual enabled `skills/list` path and explicit native skill input. Normal
owner tools/instructions remain inherited subject to the supported native
controls and bounded capability disclosure; this is not an OS containment claim.

The prompt is a retained exact UTF-8 object. Selected immutable explanation
contexts are separately labelled and streamed in full, with their exact
version/page/optional region and bytes identity. They are not project-source
reads. Prompt/context/skill acknowledgement requires the complete written frame
and matching native ACK; it does not prove model comprehension. Actual selected
project-source disclosure and its observations retain Task02/03's accounting.

Native completed/public-final facts are independent of the directory archive.
After a completed native turn and positively drained owned process/readers, the
adapter captures the whole ordinary result tree as a service-derived ZIP. Native
public-final SHA/size and ZIP/result-object SHA/size stay separate. No author
manifest, required index, file type/count/size/depth cap or prefix result is
introduced. Corrupt paths/bytes, incomplete output, unsafe drain and lost output
authority remain explicit. An unknown/reconciliation path cannot treat public
final text alone as a complete directory. Recovery uses only the exact retained
owned output tree under current authority, never another model turn implicitly.

The complete ZIP is durable `result_saved` before importer preparation. Heavy
expansion/hash/copy runs outside the writer with operation cancellation and
current payload guards. Short writer transactions reserve/adopt the exact
candidate and atomically publish or leave a ready draft. Cancellation, revoked
source/output authority, changed generation and replaced/rejected draft decisions
fence application. A stale base leaves the exact ready candidate available;
continuation consumes its saved result without Execute. Prepare failures retain
the existing typed accounting/cleanup diagnostic behavior and normal Reader,
Review and dispatcher availability; unsafe cleanup errors are not swallowed.

The managed host owns saved-tree capture and publication. The same guidance
in an external compatible host uses its advertised whole-directory CLI/API
consumer for explicit publication, with a stable operation key and captured base.
It must not also import a managed-generation result itself. Actual Aquarium and
standalone skill discovery/load/invocation must each be observed with the same
guidance identity; a controlled peer or two labels on one fixture is insufficient.
See [generation workflow](../testing/generation-workflow.md).

## Typed read-only question consumer (TASK-006-05)

Question invocation is distinct from Generation and uses no writable result
directory or generation skill. The existing dispatcher supplies a fresh private
attempt CWD and the pinned adapter's read-only native controls; this remains a
supported-host restriction, not a universal OS jail. The trusted product frame
specifies the exact captured version/page/region/query/fragment, resolved source
mode, output union and missing-evidence limitations. Source/explanation/page
content is untrusted data and cannot expand management or collection authority.
The user's question text, authorized sources and selected context are normal
provider input; author-only expected answers, scoring rubrics and assessment
oracles are excluded.

The full question, full adopted display snapshot and exact immutable explanation
context are separately streamed and acknowledged. Whole-version Ask enumerates
the complete directory and supplies every file with its canonical inventory:
UTF-8 text stays exact, binary uses labelled base64, and directory/link metadata
retains its type. Page/region Ask supplies the exact selected page; a region is
an anchor on that page, not permission to invent a smaller verified source set.
The question consumer does not silently substitute a manifest for all file bytes.
This does not change the earlier Generation reference consumer or qualify its
independent evidence.

The private typed audit records complete matching-frame prompt/context/
explanation ACK separately from each actual source observation. A supplied
citation_candidate includes the full nullable citation union; the explanation
header includes its separate reference. Final JSON must use final or partial,
complete body/citations/explanation_references/limitations and explicit nullable
union members. Service validation checks exact selected identity, location,
hash and producing-attempt ACK for observed_input. Normal posthoc verification
can corroborate the same selected bytes, but cannot manufacture original input
observation or semantic support. Context/reference handles, fresh renderer IDs
and attempt CWD are not equivalent-intent entropy; actual captured bytes and
anchors remain semantic input. HTTP operation identity retains the full original
render binding independently.

Complete output is saved under the existing result ledger before preparation
and guarded answer application. A prepared answer cannot authorize a new retry;
explicit continuation reuses its saved output with Execute zero. Current
execution epoch/source/disclosure restrictions, cancellation, hidden targets,
restore/orphan uncertainty and bounded retry authority remain enforced. Saved
history uses current owner read authority without repeating a provider turn.

## Typed read-only maintenance candidate consumer (TASK-008-02)

`MaintenanceInvocation` is the third mutually exclusive consumer on
`ProviderInvocation` (`job.kind = "maintenance"`), alongside `Generation` and
`Question`. It runs in read-only mode in a fresh private attempt directory with
no writable result tree or generation skill. The trusted `maintenance_context`
frame (`internal/adapters/ai/codex/maintenance.go` and `input.go`, with prompt
whitespace normalized via `normalizeMaintenancePrompt` and candidate explanations
sorted deterministically by `(ProjectID, DocumentID, VersionID, PagePath, RegionID)`
in `SemanticRequestSHA`) supplies the focus `MaintenanceExplanation` (`Focal`:
exact `project_id`, `document_id`, `version_id`, `page_path`, `region_id`,
`title`, `heading`, `body_excerpt`, `declared_topics`, `source_ids`, `signals`,
and `existing_refs`), the pre-inspected `Candidates` (`[]MaintenanceExplanation`,
bounded to `K <= 6`), the vocabulary of `declared_terms` across the scope, and
`ReadBindings` (`[]MaintenanceReadBinding` preserving `read_id`, `project_id`,
`version_id`, and `target_set_id` across multi-step `CaptureMaintenanceAndAdmit`).
Actual retained source file bytes are streamed through `orderedInputs` under
`GuardSourceInput` / `AttachSourceInputs` (binding each `read_id` to its exact
`MaintenanceReadTarget` `project_id`, `version_id`, `source_id`, and
`bytes_sha256` across multi-project candidate scopes) so `observed_input` is
recorded only after complete native frame transmission and matching turn
acceptance.

The native turn enforces a closed JSON output schema (`maintenanceFrame` /
`MaintenanceOutputEnvelope`) whose top-level object requires `relations`,
`topics`, `content_gaps`, and `limitations` (`additionalProperties: false` at
the root and every item object):

- `relations`: items require `target_version_id`, `target_page_path`,
  `target_region_id`, `relation_type` (`depends_on` | `source_dependency` |
  `related` | `complementary_explanation` | `shared_source` |
  `thematic_analogy`), `rationale`, and `uncertainty`.
- `topics`: items require `topic`, `rationale`, and `uncertainty`.
- `content_gaps`: items require `kind` (`new_version_proposal` |
  `new_document_proposal`), `theme`, `rationale`, and `uncertainty`.
- `limitations`: items require `kind`, `reason`, `target_version_id`, and
  `message`.

The complete raw public-final text is saved as the attempt's result object before
`PrepareMaintenance` runs outside the SQLite writer (`decodeMaintenanceOutput`
plus streaming full non-truncated retained source objects, including `> 256 KiB`,
for each unit's cited evidence paths). `PrepareMaintenance` and
`ApplyMaintenance` (`internal/application/scan/candidates.go`) enforce the
deterministic domain invariants before persisting `PreparedMaintenancePayload`
on `maintenance_intents` and findings on `findings`. The maintenance prompt
requires a concrete mechanism comparison and reader-useful connection, with
abstention on generic similarity. These instructions require actual quality
evaluation; their presence does not establish useful model behavior:

1. **Directional source-dependency verification and unit-scoped source evidence (`T-008-02-01`, `B01`, `B02`, `B12`)**:
   A proposed `relation_type = "depends_on"` / `"source_dependency"`
   (`evidence_basis = "source_fact"`) is accepted only when the focal unit and
   candidate target unit each have complete, non-truncated retained source reads
   scoped to that unit's own cited evidence paths (`VerifiedEvidenceByUnit` and
   `AllSourceReadsReadyByUnit`, without leaking sibling region source files from
   the same version) and `verifySourceDependencyFactWithRepo` confirms a
   directional import or call from a focal unit source file to a candidate
   target unit source file using either a verified module/namespace declaration
   (`go.mod` `module` directive, canonical Go `// import "..."` comment on the
   `package` line, `package.json` `"name"`, `pyproject.toml` `name`, or
   `Cargo.toml` `[package] name`) or a resolved local relative import (`./...`,
   `../...`, or Python `.pkg`/`..pkg`) within the same verified repository
   (`RepoKey`), case-sensitively and without multi-segment path-suffix guessing.
   In `current` mode, Git targets resolve to the exact revision captured at scan
   admission (`st.ResolvedRevision`), and `files` targets without an admission
   capture fail closed (`source_unavailable` / `source_capture_required`)
   without falling back to live working-copy files. When source reads are
   missing, unverified-module, reverse-direction, or keyword-only, the
   relationship is never emitted as `depends_on` or `source_fact`; if the target
   remains an inspected candidate, it is downgraded to `relation_type = "related"`
   with `evidence_basis = "thematic_analogy"`, explicit `uncertainty`
   (`"source_unverified"` or `"partial_source"`), and a qualified `rationale`
   that explicitly withdraws the unverified code-dependency claim (`B12`).
2. **Candidate target, coordinate containment, and lifecycle/pagination fencing (`T-008-02-02`, `B03`, `B07`)**:
   Every proposed relation target must belong to the inspected
   `CandidateShortlist` (`K <= 6`), pass current active authority and
   non-Trash/non-purged checks across shortlist, inspection, dispatch, and
   `ApplyMaintenance`, and cite an actually inspected
   `(target_version_id, target_page_path, target_region_id)` coordinate
   (preserving multiple distinct region/page candidates within the same version
   while rejecting invented uninspected coordinates on an inspected version).
   Emitted findings record `proposal.origin_unit` (`version_id`, `document_id`,
   `project_id`, `page_path`, `region_id`); document Trash, manual purge, and
   `ListJobFindings` cursor pagination fence both `$.origin_unit` and
   `evidence_json` target version references so trashed or purged origin/target
   findings are hidden or scrubbed and paginated queries continue past filtered
   rows until `limit` is filled or the table is exhausted.
3. **Topic grounding and content-gap proposals (`T-008-02-03`)**:
   `evaluateTopicFocalGrounding` checks normalized topic tokens, compounds and
   stems against focal body/headings and unit-scoped verified source tokens.
   It can accept a bounded paraphrase when specific focal concepts and a
   focal-grounded rationale support it; it no longer requires a literal match
   for every slug token. This is a lexical eligibility check, not proof of
   general semantic entailment. Opposite-polarity qualifiers supported only by
   a contradictory focal counterpart are rejected before generic and paraphrase
   acceptance; actual negative-token/stem matches remain eligible. This is
   bounded lexical protection, not a general natural-language verifier.
   Declared themes missing from the focal body
   can become source-qualified content gaps; other ungrounded topics are
   rejected with per-proposal reasons. Provider `limitations` remain
   uncertainty/abstention records and never synthesize content-gap proposals or
   findings; only explicit `content_gaps` and declared missing scope themes
   (`evaluateTopicFocalGrounding`) enter the gap-validation path, preserving the
   original limitations and raw output. Generic source absence alone and
   neighbor absence do not establish a missing focal explanation. Actual
   quality evaluation must still detect false coverage or unhelpful gaps.
4. **Stage-1 global semantic nomination frame and strict closed-schema validation (`Mode = "semantic_nomination"`, `EE-SN-P01`, `N2`, `N5`)**:
   When `MaintenanceInvocation.Mode == "semantic_nomination"`, the Codex driver
   builds `maintenanceNominationFrame` (`internal/adapters/ai/codex/maintenance.go`)
   from `Invocation.Prompt` (`MaintenanceNominationBatchPrompt`) without reading
   full explanation HTML or attaching source-read files. Each projected card
   (`ProjectNominationCard`, `ProjectNominationBatchCards`) is full-fit-first up
   to `<= 8192` raw serialized JSON bytes and `<= 8192` JSON-string re-encoded
   bytes without a fixed `900`-byte excerpt cap or per-card average quota. When a
   single card exceeds `8192` raw or JSON-encoded bytes, or when a batch of
   `<= 64` cards exceeds the `<= 144 KiB` total frame budget (`8 KiB` fixed frame
   reserve), `body_excerpt` is deterministically shrunk from the unspliced
   original available excerpt via UTF-8-safe head (`60%`) + `"\n...\n"` + tail
   (`40%`) truncation (falling back to single-slice UTF-8 prefix truncation when
   a tiny byte budget cannot fit both non-empty ends and the separator) and
   secondary metadata dropping while preserving service identity (`unit_id`,
   `unit_kind`, `item_id`, `project_id`, `document_id`, `version_id`,
   `page_path`, `region_id`), recording exact `total_body_bytes` and
   `excerpt_coverage = "truncated"` only when trimmed. The complete nomination
   frame is capped at `<= 144 KiB` (`8 KiB` fixed reserve) across both raw and
   JSON-string re-encoded prompt bytes. Both nomination and
   Stage-2 verification normalize `SemanticRequestSHA` across mode, batch/focal
   identity, candidate order, prompt digest, and sorted source bindings (`N5`).
   `decodeMaintenanceNominationOutput` enforces a strict closed JSON schema with
   `DisallowUnknownFields`, exact batch cardinality (`minItems == maxItems ==
   len(cards)`, requiring one nomination entry for every supplied focal card),
   and required field presence (`focal_unit_id`, `abstained`, `candidates`,
   `candidate_unit_id`, `rationale`), requiring a non-blank `abstain_reason` and
   `candidates: []` when `abstained == true`, requiring `1..6` candidates and an
   omitted or empty (`""`) `abstain_reason` when `abstained == false`, and
   rejecting missing focal rows, missing `abstained`, unknown/out-of-batch/self/duplicate
   unit IDs, or `> 6` candidates per focal unit without silent repair. Validated
   nominations merged into Stage-2 candidate shortlists carry signal `kind =
   "semantic_nomination"` with `detail` prefixed `unverified_card_routing_hint: `
   to distinguish card-level routing hints from Stage-2 inspected evidence.
