# Configuration and policy contract

**Proposed schema v1, documentation baseline 1.6.0.** One daemon-owned validated effective configuration is stored as a revisioned record. Human-readable import/export uses strict YAML; HTTP uses JSON. Repository/package files cannot override it. Reject unknown fields, duplicate keys, unsafe YAML tags and expansion features. Shared enums are in the [wire registry](schema-registry.md).

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

Validate ports 1024..65535, with 9000..9999 preferred. Other valid explicitly chosen ports receive a preference warning rather than an implicit ban. A collision fails visibly; no random fallback or listener takeover. The daemon bind remains loopback. An optional private proxy handles remote reachability and TLS.

Remote enablement requires a validated HTTPS external origin. A dedicated hostname is convenient but not required. The trusted UI/API base URLs reject embedded credentials, wildcard trust entries, arbitrary forwarded origins and unconfigured hosts. allowed_origins contains exact trusted browser origins; Host and Origin checks use the configured local/remote endpoints and validated proxy information. Renderer addresses belong to a separate delivery mapping and never become trusted UI/API origins merely by being reachable through the same listener. The first approved renderer candidate uses distinct private HTTPS ports with exact upstream mappings; any variable address is checked against its active version/delivery mapping. Choose unused ports after inspecting actual service impact, preserve unrelated routes and never enable Funnel/public exposure. A custom domain or wildcard certificate is not authorized by D-10. TASK-001-03 records trusted authentication/private ingress; TASK-001-04 records the actual renderer address, proxy, authority, revocation and safe reuse behavior. G-CONTRACT freezes the selected configuration shape only after those distinct closing checklists pass. No wildcard DNS/certificate, second API service or broad origin allowlist is required in advance. Listening on a port or possessing network access does not authenticate a caller.

Remember device is on by default. A paired browser stores its revocable owner bearer in origin-scoped localStorage and reuses it after reload, new tab and browser restart while that storage remains available. Browser-cleared storage, private browsing or a changed origin may require pairing again; the application does not add a periodic reauthentication rule. A device can choose memory-only storage and pair again after that credential is lost. Owner/agent management bearers stay out of URLs, generated frames, logs and package content. Cookie authentication is not used for owner/agent management authority. A renderer-only scoped cookie or another content-delivery credential remains a candidate governed by the [authentication contract](authentication.md) and actual G-VIEW delivery evidence; it never carries management authority. That contract owns delivery renewal, termination and recreation. Ending or expiring delivery authority never proves that a staged upload was abandoned. Remote traffic uses HTTPS.

session_idle_seconds and session_absolute_seconds are null by default, meaning no automatic expiry before logout/revocation. A positive integer enables the chosen optional timeout. pairing_expiry_seconds is a positive lifetime for a pending one-time pairing request, independent of a remembered session's lifetime. First setup uses local bootstrap; an existing owner session can approve another device. All paired owner sessions have the same personal-library authority.

## Validation, enums and precedence

CLI startup selects a profile/data directory and listener settings through explicit validated configuration. Ephemeral overrides are shown in effective policy and cannot silently enable remote ingress. The DB is authoritative for ordinary policy; YAML is a snapshot. Import uses the same preview/commit validator as UI edits. There is no file-watcher/API race deciding the effective policy.

config_revision is the one effective configuration counter, exposed with the configuration resource's opaque ETag. Every committed effective library/project setting change advances it atomically with audit/outbox records. Jobs, assessments, findings and plans capture that counter; policy_revision is not a synonym. Each project also has permissions_revision, a distinct positive access generation initially 1. Changes to its selected roots, disclosure providers, retention permissions or permitted output destination advance that generation; presentation-only changes do not. Workflow credentials/jobs record the relevant project generations and captured scopes, and current policy is rechecked before new reads, submissions and application. A later expansion never enlarges an existing workflow. A validated listener/host change shows restart and reachability effects before owner confirmation, then uses a controlled restart with a recovery path. Starting an OS service or configuring an external proxy may need local OS access; this is not a second Haeryong owner role.

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
