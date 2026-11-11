# Configuration and policy contract

**Selected schema v1, documentation baseline 1.6.2; bounded production authority is implemented by TASK-002-05.** The selected machine field authority and strict executable fixtures are [tests/contracts](../../tests/contracts/README.md). One daemon-owned validated effective configuration is stored as a revisioned record. Human-readable import/export uses strict YAML; HTTP uses JSON. Repository/package files cannot override it. Reject unknown fields, duplicate keys, unsafe YAML tags and expansion features. Shared enums are in the [wire registry](schema-registry.md).

## Proposed initial configuration

Values below are proposed operational defaults to validate, not content-size limits. Haeryong is a personal developer tool: owner choices are remembered and reused, and ordinary work does not require repeated permission prompts.

```yaml
schema_version: "1.0"
network:
  listener:
    bind: "127.0.0.1"
    port: 9270
  remote_enabled: false
  external_base_url: null
  allowed_origins: ["http://127.0.0.1:9270"]
security:
  remember_device_default: true
  session_idle_seconds: null
  session_absolute_seconds: null
  pairing_expiry_seconds: 300
maintenance:
  schedule:
    mode: "manual"
    interval_seconds: null
    cron: null
    timezone: "Asia/Seoul"
  scope: "selected_projects"
  project_ids: []
  depth: "change_focused"
  missed_runs: "coalesce_one"
  max_concurrent_ai_jobs: 1
  max_provider_attempts_per_run: 20
  max_run_seconds: 1800
  auto_apply:
    backlink_index_repair: "auto"
    related_explanation: "propose"
    topic_assignment: "propose"
    freshness_assessment: "propose"
  content_generation: "propose"
retention:
  trash: "indefinite"
  backups: "indefinite"
  completed_job_detail: "indefinite"
  diagnostic_data: "indefinite"
  auto_purge_enabled: false
source:
  default_retention: "reference_only"
projects: {}
```

Asia/Seoul is an initial display/schedule timezone, not timestamp storage. Retention values are indefinite or positive unit-bearing strings such as 30d; zero is invalid. Indefinite means no automatic expiry eligibility, while owner-confirmed manual purge remains possible. There are no Haeryong limits on HTML bytes, result-directory bytes, file/page count, directory depth or output size. Large input is streamed/staged, inventories are cursor-listed and the viewer loads selected pages/assets on demand. Actual disk, memory, filesystem, browser and provider constraints are reported for the affected operation; they are not converted into blanket package rejection rules.

## Listener, origin and remembered sessions

One loopback management listener serves the owner UI and API, default 127.0.0.1:9270. Private management HTTPS proxies to it. D-10 separately authorizes additional tailnet-only HTTPS renderer ports on the existing node name; start with the [same-daemon mapping experiment](../operations/deployment.md#approved-additional-port-renderer-experiment). This does not require a second management API, library or identity system. remote_enabled and external_base_url describe trusted ingress; selected renderer mappings are separate and must not be added to allowed_origins.

Validate local listener/upstream ports 1024..65535, with 9000..9999 preferred. External HTTPS origins accept ports 1..65535, including the implicit default 443. Other valid explicitly chosen ports receive a preference warning rather than an implicit ban. A collision fails visibly; no random fallback or listener takeover. The daemon bind remains loopback. An optional private proxy handles remote reachability and TLS.

Remote enablement requires a validated HTTPS external origin. A dedicated hostname is convenient but not required. The trusted UI/API base URLs reject embedded credentials, wildcard trust entries, arbitrary forwarded origins and unconfigured hosts. allowed_origins contains exact trusted browser origins; Host and Origin checks use the configured local/remote endpoints and validated proxy information. Renderer addresses belong to a separate delivery mapping and never become trusted UI/API origins merely by being reachable through the same listener. The first approved renderer candidate uses distinct private HTTPS ports with exact upstream mappings; any variable address is checked against its active version/delivery mapping. Choose unused ports after inspecting actual service impact, preserve unrelated routes and never enable Funnel/public exposure. A custom domain or wildcard certificate is not authorized by D-10. TASK-001-03 records trusted authentication/private ingress; TASK-001-04 records the actual renderer address, proxy, authority, revocation and safe reuse behavior. G-CONTRACT freezes the selected configuration shape only after those distinct closing checklists pass. No wildcard DNS/certificate, second API service or broad origin allowlist is required in advance. Listening on a port or possessing network access does not authenticate a caller.

An external base URL whose host equals the effective local management host is invalid at every acceptance point, because the request Host header carries no scheme and the two endpoints would be indistinguishable on the wire: the static validator rejects it against the configured listener host; configuration plan and commit compare it against the effective endpoint — the candidate's own listener port (its restart destination) without a CLI listener override, or the trusted actual runtime endpoint (`--port`) with one — so an older persisted plan that collides only under today's override fails commit with no revision, effects or restart; and product wiring refuses startup when it collides with the actually bound local endpoint, including externally modified persisted configuration. Recovery is an owner change of the external host or the listener/override, not a silent classification.

Remember device is on by default. A paired browser stores its revocable owner bearer in origin-scoped localStorage and reuses it after reload, new tab and browser restart while that storage remains available. Browser-cleared storage, private browsing or a changed origin may require pairing again; the application does not add a periodic reauthentication rule. A device can choose memory-only storage and pair again after that credential is lost. Owner/agent management bearers stay out of URLs, generated frames, logs and package content. Cookie authentication is not used for owner/agent management authority. A renderer-only scoped cookie or another content-delivery credential remains a candidate governed by the [authentication contract](authentication.md) and actual G-VIEW delivery evidence; it never carries management authority. That contract owns delivery renewal, termination and recreation. Ending or expiring delivery authority never proves that a staged upload was abandoned. Remote traffic uses HTTPS.

session_idle_seconds and session_absolute_seconds are null by default, meaning no automatic expiry before logout/revocation. A positive integer enables the chosen optional timeout. pairing_expiry_seconds is a positive lifetime for a pending one-time pairing request, independent of a remembered session's lifetime. First setup uses local bootstrap; an existing owner session can approve another device. All paired owner sessions have the same personal-library authority.

## Validation, enums and precedence

CLI startup selects a profile/data directory and listener settings through explicit validated configuration. Ephemeral overrides are shown in effective policy and cannot silently enable remote ingress. The DB is authoritative for ordinary policy; YAML is a snapshot. Import uses the same preview/commit validator as UI edits. There is no file-watcher/API race deciding the effective policy.

config_revision is the one effective configuration counter, exposed with the configuration resource's opaque ETag. Every committed effective library/project setting change advances it atomically with audit/outbox records. Jobs, assessments, findings and plans capture that counter; policy_revision is not a synonym. Each project also has permissions_revision, a distinct positive access generation initially 1. Changes to its selected roots, disclosure providers, retention permissions or permitted output destination advance that generation; presentation-only changes do not. Workflow credentials/jobs record the relevant project generations and captured scopes, and current policy is rechecked before new reads, submissions and application. A later expansion never enlarges an existing workflow. A validated listener/host change shows restart and reachability effects before owner confirmation, then uses a controlled restart with a recovery path. A previously collected provider accounting diagnostic does not prevent that restart when durable settlement and owned drain were positively verified. The daemon retains its first historical diagnostic until final stop; current/unknown cleanup and any unrelated joined endpoint, dependent, storage or output error still stop restart. Starting an OS service or configuring an external proxy may need local OS access; this is not a second Haeryong owner role.

Schedule mode is manual/interval/calendar. Manual has no active interval/cron. Interval requires interval_seconds and no cron; calendar requires a validated five-field cron plus IANA timezone and no interval_seconds. Proposed minimum interval is 60 seconds. Run once per unique scheduled instant, skip nonexistent local time, and do not duplicate a repeated wall time without an explicit defined policy. Missed runs use coalesce_one; a sleeping host does not invent completed checks.

Scope is selected_projects with explicit configured project_ids, or all_allowed_projects within the owner-approved workflow scope. Depth is change_focused, full_scope or deep_semantic. Full scope captures the permitted inventory, not a promise every item receives semantic analysis. Deep semantic still obeys budget/source bounds. CLI may accept hyphenated spelling but normalizes and reports the registry's underscore values.

The html_js_v1 profile supports normal HTML/JavaScript and ordinary browser capabilities. Host selection data is ordinary JSON without fixed field, string, enum or total-state maxima. Clients automatically stream large text/state into an immutable context_ref; finite control-message/chunk buffers do not impose a total input/output limit. API text, file inventories, notes and answers likewise use streamed objects/cursors where needed, rather than a hidden whole-body or render-envelope ceiling.

Backpressure, worker concurrency, recoverable idle timeouts and incremental disk use are implementation controls. They must not silently drop files, impose a total import deadline on a progressing transfer or count only a prefix as a completed directory. Expose progress, cancellation and resumable staging. Maintenance concurrency, attempt counts and run duration govern owner-configured automatic work/cost; they do not limit imported file size or truncate an AI-produced result. Provider context/output limits are reported as the selected provider's capability, with chunked inputs or explicit incomplete results where appropriate, not as a Haeryong storage limit.

## Automatic action semantics

| Setting | Disabled | Propose | Auto |
| --- | --- | --- | --- |
| backlink_index_repair | Report deterministic anomaly | Repair awaits explicit approval | Repair derived state with audit |
| related_explanation / topic_assignment | No semantic proposal of that category | Save evidence-backed suggestion | Apply reversible origin-labelled overlay |
| freshness_assessment | No semantic assessment of that category | Save candidate for approval | Publish scoped AI-labelled assessment with evidence and coverage |

Ordinary transactional edge/backlink consistency never depends on a scan setting. A relevant deterministic source-change observation within configured scope may set reassessment_required; it does not authorize an unconfigured provider call, publish an unapproved semantic finding or prove content outdated. Unapproved candidates remain findings and do not change applied freshness summaries.

content_generation is disabled/propose/draft. Disabled starts no maintenance generation. Propose stores a scope/reason/evidence finding for owner decision without creating a generation attempt. Draft may start generation within the captured approved source/provider/output scope and budget, and retains an unpublished candidate; it cannot publish it. Missing required authority or capability produces a visible blocked finding/job instead of automatic expansion. There is no maintenance auto-publish mode. Explicit owner generation/import is a separate operation. Automatic policy cannot write personal review or expand source disclosure or billing mode by itself. Auto freshness retains its configurable meaning, including scoped aligned assessments when policy permits them; it never implies owner Reviewed or global correctness.

Project, source roots, provider and disclosure choices are selected once and reused within their configured scope. Routine source reads and already approved provider work do not trigger per-read approval. Project overrides can narrow scopes/budgets/action modes; a package or generated script cannot change owner settings. Managed workflow credentials carry only their configured publication/read/job scope and do not carry personal review or purge authority.

source.default_retention uses reference_only/selected_snapshot. A reference/digest is not retained bytes. Active job pins protect only permitted retained or execution-temporary objects that actually exist; they do not turn reference-only sources into permanent snapshots. If requested pinned/current bytes are unavailable, pause/fail or report unable_to_verify; explanation-only commentary cannot claim the requested source was checked. Execution-temporary capture is bounded to the job and cleaned once safe. Permanent selected snapshots follow the remembered owner retention choice.

## Remembered project settings and source mapping

The effective configuration contains a projects map keyed by existing project_id. An absent project override inherits the library defaults; it does not register an external source or permit provider disclosure. The stable owner Unfiled project used by ordinary import is a normal configurable project, initially without source/disclosure selection. Its name, source/provider choices and other project settings use the same owner configuration flow; v1 adds no document-move operation. Import association is separate client/profile/workflow context, normalized into explicit request IDs as described in [CLI](cli.md) and [package](package.md); a folder name, hash or imported path never creates a source mapping.

| Project setting | Shape and meaning |
| --- | --- |
| name / lifecycle | Project display name and project.lifecycle active/archived, edited through the same project-scoped configuration plan. Categorization has no document/source cascade. |
| source_mode_default | pinned or current; default pinned. Historical source intent stays pinned unless an explicit request selects another exact target. The registry selects the latest actually captured target for the same requested selection and exposes captured_at; an import declaration alone is no capture. No capture means not_assessed. Reading a list starts neither AI nor a source/branch lookup. |
| provider_id | Nullable configured provider identity used as the default for a new operation. Changing it does not copy credentials or silently change billing. Each source's permitted provider_ids must still include the selected provider before submission. |
| output_destination | Nullable owner-selected path on the service host for generated results. Validate and canonicalize the destination during the ordinary configuration plan; this does not expose a generic browser filesystem API or permit overwriting unrelated source files. |
| sources | Array of explicitly registered source selections. Each entry has source_id, kind (the source_mapping.kind enum: git or files), canonical host root_path, nullable repo_id (required for Git), retention_mode reference_only or selected_snapshot, and provider_ids. Captured files use source.kind snapshot; Git committed captures use git, while an explicit working-copy snapshot uses snapshot. An empty provider_ids list permits owner source viewing within the selected root but no provider disclosure. Preserve stable logical source/repository identity when explicitly remapping a moved checkout. |
| post_work_scan_enabled | Boolean, default false. A remembered selected-project workflow with this enabled automatically derives scan.request within that project and existing source/provider/budget bounds; it does not select all projects or authorize new disclosure. |
| maintenance | Optional overrides of the existing schedule/scope/depth/budget/action fields. Overrides may narrow the selected workflow; expansion uses the ordinary impact plan and affects only newly captured work. No artifact byte/count/depth knob is introduced. |

The owner can select a local directory through the native chooser or configure a known service-host path through owner settings/CLI. The plan validates the selected location and shows source/disclosure/output effects once; subsequent unchanged reads reuse the choice. GET /projects/{id}/sources exposes source identities and availability to its scoped reader without host paths. GET /configuration may show configured paths only to the owner in the settings context, with all secrets redacted. A model-authored path, content-delivery credential or scoped source-view response cannot register or broaden these settings.

POST /configuration/plans accepts {scope, expected_config_revision, changes}. scope is {kind:library} or {kind:project, project_id}; project scope applies changes to that project's settings only. Omitted fields are unchanged, explicit arrays replace the selected array and null clears only nullable settings. Nested objects merge specified known fields. Reject unknown fields, invalid IDs and invalid combinations without changing effective policy. The plan response returns the normalized changes, base config_revision, affected permissions_revision values and impact. Commit uses the existing configuration-plan route and atomic revision/precondition rules.

A remembered workflow stores a server-allocated workflow_binding_id and stable principal_id, independent of its current project/action choices and revocable credential. Credential issuance/rotation and revocation use the owner-authenticated local CLI/IPC route represented by auth grant create/revoke; a trusted workflow launcher may invoke it automatically. No remote issuance endpoint or per-invocation manual grant ceremony is required. Reusing that explicit binding preserves the principal and receipt namespace; a new remembered workflow allocates a new binding. Never derive identity from a hash of a scope/project set. Each issuance snapshots only the requested actions within current remembered selections and project access generations; changing a binding cannot widen an already issued credential. The workflow defaults to its selected project and cannot fall back to owner Unfiled or all projects without current authority. Persistent project/document choices and each operation's captured base follow the [package binding table](package.md#remembered-association-and-operation-base). The derived credential cannot add personal review, session management or purge rights, and does not restrict the external agent's other OS tools.

## Configuration plan and commit

1. Read config_revision and individual ETag.
2. Submit proposed changes for a validated plan with diff, affected jobs, retention candidates and exact current revision.
3. Show consequential effects such as remote exposure, expanded disclosure, billing-mode change, retention reduction or purge enablement, and ask the owner to confirm the intended changes in the normal UI/CLI flow.
4. Commit the exact unexpired plan with its plan_id, expected revision and confirmed: true. A plan describes the change; it is not a credential or a second authority tier. One plan is the natural idempotent commit identity and produces one retained result.
5. Recompute critical eligibility under the current owner session and configuration, atomically save config revision/audit/outbox, then report immediate, next-job or controlled-restart effects.

Current authentication checks precede replay of a committed plan; identical replay returns its safe original outcome before treating the old config revision as a new stale write. Changed plan input or a new stale plan conflicts. A paired owner can manage these settings locally or remotely. No separate administrative grant, per-plan capability or mandatory reauthentication ceremony is added.

Running jobs retain captured policy for audit and obey newly stricter current rules before new provider calls and result application. Later expansion never widens a captured run. Cancellation and revocation take precedence. These checks reuse configured choices without prompting for every read or attempt. Invalid changes leave the previous configuration effective.

## Retention and recovery

A retention period determines automatic eligibility. Auto purge is enabled through its normal impact plan; that owner-confirmed policy permits the daemon to execute subsequent eligible runs without a new per-run confirmation. The dedicated lifecycle job purges only eligible trash, never active content because an AI calls it outdated. Manual purge uses its own owner-confirmed exact plan. Preserve shared objects, job/staging/copy pins, unknown attempts/receipts, required recovery facts and published provenance.

Completed job-detail cleanup cannot remove saved answers/citations, personal review history, rejection decisions or minimal receipts needed to reconcile a mutation. Diagnostic cleanup cannot substitute for source redaction. Precise disposable-detail/audit minima are finalized with recovery evidence, not silently inherited from one generic log duration.

Backup retention applies to whole coherent sets and is separate from live document/source retention and portable export. Never expire the last complete set merely because an incomplete replacement exists. Coherence, checksums and verified restoration are the baseline; encryption is an optional owner setting. Follow [backup recovery](../operations/backup-recovery.md) and the CLI export contract for snapshot and integrity semantics.

Restore creates new runtime/auth/stream generations, invalidates restored sessions and workflow credentials, and pauses scheduled AI, timed purge and remote exposure for recovery reconciliation. The owner pairs once, reviews recovery status and re-enables the desired remembered configuration. Reconcile potentially newer external work before restarting jobs; an old backup's queued state does not prove a provider call never occurred.

## Current authority and runtime effects (TASK-002-05)

The production API/CLI share `internal/application/configuration` and the selected machine config definition. Strict JSON/YAML decode preserves integer semantics; fractional/exponent lexemes are not accepted as integers. Implicit standard YAML values are allowed, while explicit tags, anchors/aliases/merges, duplicate/unknown fields and multiple documents are rejected. Timezone Local is host-dependent and invalid; UTC and valid IANA zones remain accepted.

A plan is based on one captured current configuration revision; both initial and final serialized checks must match the requested revision. Path canonicalization happens outside the writer, with that captured revision rechecked before saving the plan. Natural commit and an optional canonical UUID alias retain one principal-wide original result; a supplied empty key is invalid, while absent optional keys remain valid. Current owner authority is checked under the authorizing policy immediately before the atomic switch. If the new optional session expiry expires that owner, the settings/receipts/outbox remain committed and the response/future requests use the new expiry. A fresh authorized owner device can read the original result without another commit.

TASK-003-03 adds optional `network.renderer_pool: {base_port, count}` with
defaults 9281/8 for both new and existing libraries. The range must fit the
non-privileged port space and exclude the management listener, configured
renderer-mapping upstreams and frozen fixture ports 9272–9273. Configuration
plans report its controlled-restart effect. Existing per-user tuple/origin
reservations remain sticky when the pool changes; expansion provides addresses
for new reservations rather than releasing or reassigning old ones. The actual
management port is excluded even under an ephemeral CLI listener override.
This is renderer address capacity, not a package/version admission limit;
imports and ordinary authenticated queries remain usable when it is exhausted.

Remember-device defaults and session/pairing expiry use DB-current policy. The trusted connect shell receives only the remember-default boolean; existing explicit storage choices remain unchanged. Listener/Host/Origin changes advertise controlled restart and use the same daemon/profile/library, with drained handlers/storage before lock release. Explicit local `--port` is shown as an ephemeral recovery override; no random port fallback or external ingress change is performed. Provider, scheduler, full backup/restore and future workflow-grant issuance remain explicitly unavailable. Source-path validation stores selected policy only; it does not read/disclose source bytes or invoke a provider. [Current evidence](../testing/query-cli-configuration.md) records the actual scope and limits.

## Project registration and source observation (TASK-004-01)

Additive delivery: owner POST /projects registers `{name}` with replay-stable
identity and returns the shared project projection, including its ETag. It does
not approve a host root or disclosure. Source editing uses the existing
project-scoped configuration plan/commit, base revision, impact preview and
confirmation rules. No configuration schema field/version changes are needed:
project sources, retention_mode, provider_ids and source_mode_default already
exist. `features.source_verification` is a capability response flag, not a new
configuration option; absent/false disables the UI action.

S09 displays logical source identities and recorded last_observed_* facts without
host paths. Registration and configuration intents survive route/runtime remounts
in the controller-owned store. Unknown outcomes reconcile their original receipt;
a stale plan is shown for reconciliation rather than silently recommitted.
Cancelling a preview discards only the originating flow's preview intent, and
cannot cancel an in-flight commit. Source changes advance project permissions
and emit source.scope_changed (resource source / project_id, nil body) together
with project.updated (resource project / project_id, nil body) via the existing
outbox. The owner receives the scope event while the project remains visible;
scoped source-event masking is unchanged. Public notifications omit stored
bodies and clients refetch the authorized project/source view.

## Restore reconciliation (TASK-005-05)

A pending restore hold retains original and known-current choices, installs a validated
safe loopback/manual/no-timed-purge configuration and blocks normal configuration commits.
The exclusive owner-authenticated `restore review` path supplies the exact hold identity
and choices through the existing validator/plan/commit; configuration and hold release
commit atomically. Only listed same-intent purge jobs are reconciled under current guards;
other old jobs remain held. Reviewing settings does not implement future ingress,
scheduler or provider workflows. retention.backups is enforced opportunistically after
successful complete backup creation; indefinite keeps all, and whole-set deletion checks
active verify/restore ownership and a current verified fallback.

## Source defaults owner flow (TASK-006-02 implementation)

The normal shared Sources settings editor now submits source_mode_default and
nullable provider_id together with the existing complete source array through
configurationView/Plan/Commit. Server normalization is shown in the preview;
changing a non-null default provider requires the explicit visible impact
confirmation. Null clears the remembered default. This selection does not add
the provider to any source's provider_ids, copy credentials or change billing.
Cancel/stale/reentry/receipt recovery retain the existing revisioned behavior.
The browser and actual native App use the same flow and narrow existing named
configuration facade, without a new native filesystem or source-control API.

Source/disclosure expansion and a non-null default-provider change have
distinct preview reasons, combined into one confirmation when both apply.
Provider-only or provider-plus-source-narrowing changes do not claim source
expansion or an unchanged allow list. The preview shows the proposed source
mode and, when the provider changes, the old and new default provider, including
null. An initial configuration read failure hides editable defaults and offers the ordinary
Retry; it does not fabricate an editable configuration. Actual loaded drafts
and retained plan/commit identities keep their existing recovery behavior.

## Provider transport status (TASK-006-03 implementation)

The provider transport uses the existing logical provider/source disclosure
choices. No provider credential, binary path or new policy field is added to
schema-v1 effective configuration. An optional absolute --provider-binary on
local daemon startup selects an already installed verified executable only.
The normal Settings/AI screen reads the same owner provider status as CLI and
native clients. It does not expose login/account/billing mutation controls.

Service health reports transport_available separately from nullable current
availability and supplies status_url=/api/v1/providers. Pending generation and
question admission remain unavailable. Current native authentication/usage and
permission limitations are reported without modifying remembered source or
provider settings, inferring entitlement from catalog entries, or overriding
owner model/effort defaults. Existing current-policy and restore-hold fences
still govern every job payload and dispatch.

## Controlled private ingress (TASK-007-01)

The supported browser construction uses the configured HTTPS management origin
and distinct HTTPS renderer origins on the same hostname, each forwarded to its
own content-only `http://127.0.0.1:PORT` upstream. Configure
`network.renderer_mappings` entries with `version_id`, `origin`, `upstream` and
`reassignable: false`. One mapping per version, external origin and upstream is
required. Normalized URL origins compare case-insensitively by hostname, omit
default ports and normalize numeric ports; equivalent spellings cannot allocate
an origin twice. Management and content origins/Host authorities must remain
distinct, including under an explicit runtime listener override. Other HTTPS
host constructions remain unavailable for rendering rather than falling back to
local addresses. Configuration validation alone does not prove proxy reachability.

External origins and sticky loopback tuple reservations survive delivery close,
restart, mapping removal and pool changes. A remote-first Open claims its exact
configured upstream even when it is outside the current pool. A prior local
reservation for the same version must match that upstream. A fresh local-first
Open uses the dynamic pool outside all mapping upstreams; it does not claim a
configured remote mapping. Discover its actual sticky port through authenticated
local Open, close that ephemeral session, then map the returned port and shift or
expand the pool in the same plan as described in the
[setup runbook](../operations/deployment.md#current-controlled-private-browser-setup-task-007-01). Previously owned
origins/upstreams cannot be transferred to another version or library; a conflict
requires an unused mapping or pool expansion, never reassignment. Existing
seven-field origin-ledger records remain readable; permanent external-origin
aliases are an additive field, without a SQLite schema change.

Every new configuration plan includes `impact.exposure`: `changed`, `before`,
`after`, `private_proxy_action` and `proxy_configuration_changed_by_commit: false`.
Each snapshot contains `remote_enabled`, nullable `management_origin`,
`configured_local_endpoint`, `local_forwarding_endpoint` and sorted
`renderer_mappings` tuples. The forwarding endpoint includes an actual ephemeral
listener override separately from persisted intent. Apply requires the existing
explicit owner confirmation. A still-fresh pre-upgrade exposure-changing plan
without that preview must be replanned; completed natural/alias receipt replay
and non-exposure plans retain their existing semantics. Commit never executes
Tailscale or installs a certificate. Inspect, apply, verify and remove private
proxy routes separately under owner authority; preserve unrelated routes.

See [private ingress evidence](../testing/private-ingress.md) for executed scope
and the distinction between same-host web proof and physical-device qualification.

## Implemented freshness policy (TASK-008-03)

The existing `maintenance.auto_apply.freshness_assessment` policy selects the
freshness-only producer: `disabled` admits no freshness model work, `propose`
keeps a finding without publishing assessments, and `auto` publishes validated
exact-target contributions. It does not authorize content publication or source
edits. The captured policy and current effective project/library policy are
restricted together at admission and application; later relaxation cannot widen
an accepted run. Current provider/source permission and lifecycle fences still
apply to saved-result continuation. No new owner setting or author metadata is
required for ordinary imports.

## Task04 relation/topic policy application

The existing per-category disabled/propose/auto matrix now connects to separate
relation/topic effect repositories. Both captured and current library/project
restrictions apply before creating or reusing an effect. Relaxation cannot widen
an accepted run. Current owner-state/lifecycle/source guards still apply to reuse.
Shared Settings/Maintenance uses the existing revisioned impact plan and explicit
confirmation; conflicts preserve local inputs. Content remains disabled/propose/
draft without automatic publication; finding-to-draft production remains Task06.
No new setting, disclosure grant or scheduling engine is introduced.

## Task05 explicit owner application

Manual decisions retain the captured category policy and compare it with the
current effective policy. A disabled category refuses application; broadening
policy does not widen retained evidence or the captured target. Topic pin/reject
and durable Undo decisions remain independent owner constraints, consulted by
automatic producers and manual current-revision application. Reassessment is
new explicit scope/source work, not a force or Review bypass.
