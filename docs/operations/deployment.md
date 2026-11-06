# Deployment and local/remote operation

**Planning runbook:** The initial foreground lifecycle and protected local-control subset is implemented by [TASK-002-01](../testing/profile-lifecycle.md). [TASK-002-02](../testing/storage-foundation.md) implements storage preparation and its fail-closed recovery boundary. TASK-002-03 adds current owner/browser sessions and the trusted connect shell. TASK-002-04 runs the durable upload-seal/import worker on that same daemon and listener. Shutdown drains request handlers and worker activity before closing storage or releasing the profile writer lock. TASK-007-01 adds controlled private browser ingress and endpoint doctor; OS service installation remains separately owned. The [executed fixture experiment](#executed-authentication-and-private-ingress-experiment) separately records its temporary listener and private foreground proxy; it does not establish a production installation.

## Proposed support tiers

| Tier | Scope | Gate |
| --- | --- | --- |
| Initial native desktop | macOS ARM64 Wails v3 with matching Go daemon | G-STACK/G-VIEW and desktop lifecycle checks |
| Initial headless host | macOS and Linux AMD64/ARM64 service | No GUI/Wails runtime dependency; provider route verified separately |
| Browser clients | Desktop Safari/Chrome, Safari iOS/iPadOS, Chrome Android | Record tested versions and normal interactive behavior |
| Later candidates | Windows service/desktop, Linux Wails desktop, native mobile | No implied initial acceptance claim |

Minimum versions and binary hashes are evidence recorded by TASK-001-02 and release tasks. One service host with several owner devices is not multi-daemon synchronization. The host may be a local Mac or another owner-selected machine; no host is provisioned by this document.

## Filesystem and install

Use user-owned profile directories; proposed macOS base `~/Library/Application Support/Haeryong/profiles/`, Linux data base under the appropriate user data directory. The owner may choose another supported local path or available local external volume. Keep DB, immutable content, staging, locks and runtime discovery separate. The live DB/WAL belongs on supported local storage, not a network share or a background synchronization folder. Copy coherent completed backups when synchronizing storage.

Fail clearly if a configured volume is unavailable; do not initialize an empty substitute at its mount point. Package the headless CLI/daemon with compiled web assets and a separate matching Wails client. A production JavaScript server is not required. Use an explicit user-level service install for the selected OS. Closing Wails leaves the daemon running; Stop is a separate operation. Port 9270 needs no root service.

## Simple network baseline

| Endpoint | Proposed binding | Purpose |
| --- | --- | --- |
| Shared local listener | `127.0.0.1:9270` | Trusted UI and authenticated API for local clients and the private proxy |
| Optional private external URL | Owner-configured HTTPS URL | Remote devices reach the same daemon through private ingress |

Configure `network.listener` with bind/port, `remote_enabled: false` initially and an explicit `external_base_url` when enabling remote use. There is no mandatory second upstream listener or exclusive cookie hostname. Port 9270 is configurable and not claimed reserved. Show the actual profile/library, listener and external URL. A collision fails visibly instead of selecting a random port.

The owner enables private exposure through its impact/confirmation flow using an existing owner session or bootstrap. Validate exact external Host/Origin values and the intended ingress route. Remote traffic requires HTTPS; do not infer its external URL or identity from arbitrary forwarded headers. The daemon still requires the applicable current owner/agent session or narrowly scoped content-delivery authority on private requests. Private-network access alone is not authentication.

Both local and remote trusted browsers use remembered bearer sessions. With Remember device selected, origin-scoped localStorage reuses the session across reload, new tab and browser restart while the browser retains its storage. Optional memory-only use reconnects after its document closes. A new external origin needs pairing once; normal daemon restart does not. Logout/revocation removes that device's access. If browser storage is cleared or unavailable, follow the existing Connect/bootstrap/other-owner-device flow and explain the storage state; do not add a routine seven-day pairing requirement. Optional Home Screen use may help remembered access, but has a separate storage context and no guarantee that an existing Safari pairing transfers. Verify that mode separately under the [browser storage rules](../contracts/authentication.md#remembered-sessions-and-transport). Use authenticated streaming transport for imports, content and downloads as specified in [authentication](../contracts/authentication.md).

Origin is scheme, host and port, not a URL path. One loopback Go/API listener can underlie more than one browser origin; it does not settle renderer routing. The trusted application's origin hosts its own UI code, while executable artifacts use a separate origin/context without owner/agent credentials or native authority under the [viewer contract](../contracts/viewer-protocol.md). D-10 approves additional tailnet-only HTTPS renderer ports on the existing private node name as the first experiment. Host routing/proxy mapping and native contexts remain implementation choices; no wildcard domain, owner-managed certificate or second daemon is mandatory. Exact trusted Host/Origin checks stay in force; choosing renderer addresses must not broaden management authorization.

Renderer availability is classified from the exact trusted runtime endpoints only (the actual local management endpoint from product wiring plus the configured external origin when `remote_enabled`), never from Forwarded/User-Agent headers, `Session.Origin` pairing history or any physical-location claim: a request is remote ingress when its supplied Origin is the external origin, or its Host is the external host while differing from the actual local host. Supported private ingress therefore **preserves the upstream Host** and forwards any browser Origin unchanged or absent. This is also a resource-integrity condition, not only an authorization nicety: through an unsupported Host-rewriting, Origin-stripping proxy a remote caller is indistinguishable from a local headerless native client, is classified local, and any (unreachable) render session it opens permanently consumes that library's sticky origin reservation — under the no-reassignment ownership policy the only recovery is renderer-pool expansion and a daemon restart. Such ambiguous ties (an external URL whose host equals the actual local endpoint) are refused at startup and at configuration plan/commit time rather than served; TASK-007-01 implements the exact same-host HTTPS renderer construction described below; its web evidence does not establish physical-device qualification.

G-VIEW selects and records each endpoint's construction before G-CONTRACT; Wails and private browsers may differ while passing the same acceptance behavior. Record actual trusted/render URLs or contexts, same-site/cross-site relation, proxy-to-listener mapping, trusted/render route separation and the narrow Wails Go-proxy API path. Record delivery placement/lifetime through the normative [authority rules](../contracts/authentication.md#content-delivery-authority), leaving construction-dependent config/wire values TBD until evidence. Use the complete [TASK-001-04 closing checklist](../tasks/EPIC-001/TASK-001-04.md#observable-completion-and-tests) as the authority for required environments and early proof, including concurrent root-relative fetch/module/worker paths and exact navigation/new-tab context. Apply the [viewer proof](../contracts/viewer-protocol.md#focused-g-view-evidence) for storage, authored worker replacement and safe origin reuse. A missing required target remains blocked; a shared path prefix, Referer guess or labelled cross-version mixup cannot pass.

## Tailscale Serve candidate

Tailscale Serve is an optional private deployment adapter, not a dependency in the Go domain. Official documentation describes HTTPS port selection and persistent Serve mode; see [EXT-TAILSCALE](../references.md). Verify the installed version, HTTPS prerequisites, intended owner devices and existing Serve routes before adding one. Identity headers are not Haeryong credentials.

Illustrative operator commands, after choosing the private route:

```sh
# Inspect existing routes before adding Haeryong.
tailscale serve status --json
# Example private HTTPS forwarding to the one daemon listener:
tailscale serve --bg --https=9270 http://127.0.0.1:9270
```

Use the exact HTTPS URL reported by the installed Serve version, including its non-default port, when configuring the trusted external origin. This management example forwards to the single daemon listener; the approved renderer experiment below supplies distinct private origins rather than pretending the management route alone proves rendering. Record the actual upstream Host, supplied Origin and forwarding behavior before relying on them for routing; unverified proxy headers cannot authorize requests. Test the selected renderer endpoints through the same private deployment rather than assuming a loopback-only result also works remotely.

These are runbook examples, not executed changes. Do not reset unrelated routes, serve the private content store as a generic directory or enable public Funnel. Rollback removes only Haeryong routes added by this experiment.

### Approved additional-port renderer experiment

The owner approved D-10 on 2026-10-11. Inspect existing Serve/Funnel mappings, port use, HTTPS prerequisites and intended device access before changing the selected host. Choose unused private HTTPS ports, preferably in the 9000 series; 9272 and 9273 below are examples, not reservations or a simultaneous-document quota. No network configuration is changed by this plan update.

Keep management on its own origin. TASK-001-04 selected distinct external origins with direct content-only upstreams on one daemon:

| Private external endpoint | Selected daemon target | Meaning |
| --- | --- | --- |
| Existing node name, HTTPS 9271 | http://127.0.0.1:9270 | Trusted management UI/API |
| Same node name, HTTPS 9272 | http://127.0.0.1:9272 | Version A renderer only |
| Same node name, HTTPS 9273 | http://127.0.0.1:9273 | Version B renderer only |

The earlier same-listener internal-prefix mapping was a candidate, not the selected setup. A separate synthetic HTTPS 9274 to loopback 9284 `/__renderer/origin-a/` probe verified prefix joining, escaped paths, Host forwarding, ranges and redirect behavior and was removed afterward. That wire probe does not replace actual renderer evidence. Direct upstreams keep trusted and content-only handlers distinct while retaining the same external-origin separation. Requests for `/data.json` resolve through the exact active version mapping, independent of Referer. The [Serve CLI](https://tailscale.com/docs/reference/tailscale-cli/serve) supports HTTPS ports and path targets; its capabilities alone do not prove a configured renderer. Neither reachability nor an internal prefix authenticates a request. Follow [delivery authority](../contracts/authentication.md#content-delivery-authority); renderer routes must not expose trusted shell/API through fallback, traversal or redirect.

Run the [TASK-001-04 closing checklist](../tasks/EPIC-001/TASK-001-04.md#observable-completion-and-tests) on pinned Wails, the selected primary browser and an actual agent-operated Aside browser on private HTTPS under the [2026-10-15 owner amendment](../decisions.md#owner-amendment-2026-10-15-agent-operated-private-browser-gate). Record each chosen upstream mapping, delivery mechanism, automatic reconnect, storage behavior and removal procedure before G-CONTRACT. Other agreed support targets remain later release obligations, not additional early closing environments.

Port allocation/reuse is an implementation result, not a port per stored document. Do not reassign an origin while an old client/Service Worker can control its new version; prove safe reuse or keep it unavailable for reassignment. Available endpoint resources may fail explicitly, but never become an HTML/file-count/storage quota, silent single-version fallback or false pass. Additional public exposure, custom domains or wildcard certificate operations still require an explicit owner decision. If this candidate fails, record the actual failed mapping and alternatives instead of repeating path-prefix-only experiments.

After private HTTPS works, pair the remote browser through an existing owner device or local bootstrap. Requests without a valid owner session or its scoped content-delivery authority receive no library metadata/assets. The [bounded G-AUTH experiment](../testing/auth-ingress-spike.md) records passing pairing, remembered reconnect, Host/Origin and revocation checks; production deployment must repeat applicable checks against its actual implementation.

## Large result directories and transfer

Accept a selected directory, ZIP or standalone HTML and automatically preserve/inventory its files. Multiple HTML pages, nested folders and accompanying file types require no special install setting, manual manifest or artificial size/count/depth quota. The owner may choose an artifact destination inside a project, while unrelated source files remain outside the generation/import write set.

Use incremental import/export, lazy page/asset loading, cursor-based file inventories, range responses where applicable and progress/cancellation/resumption. Configure the actual private proxy and download transport to stream large requests/responses; its body buffering, size ceiling or idle timeout must not silently reinstate the removed package limits. Use the upload-session protocol where needed to transfer successive chunks without making one giant request. Use authenticated `GET /uploads` discovery and per-upload status to recover pending work, actual staged bytes and accepted progress after reconnect/restart. Offer authorized resume or explicit abandonment while preserving current adoption and persisted recovery pins. Delivery lease expiry or mobile suspension does not abandon those uploads. Report a real proxy, disk, network or device failure with the interrupted operation's recovery state, not success with omitted files.

The complete directory need not fit in browser memory before its first page opens or before an export starts. For TASK-001-04, verify the selected streaming browser/native download behavior on its pinned Wails, primary-browser and actual agent-operated Aside private HTTPS constructions. Later feature/release verification covers the complete declared target set, including Android Chrome and Safari iOS/iPadOS; the early Aside choice does not establish mobile support. Record whether the chosen transport uses a trusted fetch/service-worker handoff, native file streaming or a content-delivery credential under [authentication](../contracts/authentication.md#content-delivery-authority). Demonstrate interrupted multi-request Range/retry/resume, a download lifetime independent of page-channel closure/suspension, observed progress, automatic authorized recovery and streaming without a whole-file Blob. Record actual platform suspension behavior without promising background execution that the device does not provide. An owner's finite device/storage capacity and a browser's supported preview formats remain real capabilities; no universal infinite-resource or native-program execution claim is made.

## Source and provider availability

[The headless-source record](../testing/headless-source-boundaries.md) exercises
the normal embedded service through private HTTPS after owned original source
and import directories are removed. Current source failure, retained snapshot
availability and historical provider permission evidence remain separate. It
records same-Mac web scope, not clean-machine installation or another platform.


Reading saved explanations requires a reachable daemon/content store. Source-grounded Q&A also needs the selected exact source and a supported provider route. Project/source/provider selections are remembered; a routine read does not prompt again. A missing provider does not prevent reading saved content.

Pinned retained evidence may remain usable while its checkout is offline, but it does not become the current working copy. Show source identity, availability, bytes origin and limits separately. A phone cannot access a sleeping laptop repository merely because it reaches Haeryong. A current-source request without that source remains unavailable rather than silently substituting an old snapshot.

An external agent may use the owner's existing environment and sign-in. A managed adapter reports its native permission/configuration limits; a separate worker jail is not an install prerequisite. Haeryong does not copy provider tokens to browser devices or autonomously modify the source checkout.

## Startup and recovery

Check profile lock/identity, schema compatibility, content references/digests, the configured listener, client/API compatibility, discoverable resumable uploads and pending recovery jobs. Do not migrate with competing daemon binaries. Normal startup preserves valid sessions and remembered source/provider choices.

Application-managed restore invalidates restored owner/agent and content-delivery sessions once, pauses scheduled AI/timed purge/exposure and presents the relevant policy/reconciliation impact. After the owner reconnects and resumes that policy, subsequent normal restarts remain ordinary startup. Reconcile unknown external attempts before redispatch. Full OS/VM rollback detection is not guaranteed. A missing daemon prevents remote writes and must not be represented as Saved. On a headless host, an owner CLI invoked through an already authorized SSH session can use the local bootstrap/recovery path with that OS user's protected local access. Restore does not preserve an old browser credential to bypass reconciliation; document this host access path for the selected deployment.

## Executed stack experiment

[The TASK-001-02 experiment](../testing/stack-spike.md) records the actual macOS ARM64 Wails/WebKit and Linux ARM64 headless combination, reproduction commands and lifetime observations. It is an isolated loopback fixture without owner authentication, service installation or private ingress. The declared macOS/Linux AMD64 release targets still require their advertised workflow checks; this record does not broaden tested support.

## Executed authentication and private-ingress experiment

[The TASK-001-03 experiment](../testing/auth-ingress-spike.md) records an executed
disposable auth fixture, separate from the illustrative runbook ports above.
Foreground tailnet-only HTTPS on 8451 forwards to loopback 9270 with exact
trusted Host/Origin checks and header bearer authentication. Local Chrome and
Aside Browser directly exercised the real private URL, remembered access and
separate local/private client approval and revocation. Existing Serve/Funnel
routes were preserved; this experiment added no public exposure. These are
origin-separated clients on one host, not physical Safari evidence. Independent
Task review and delegated Task acceptance are complete. The unchanged fixture
passed full Go race/build and local/private-HTTPS Chrome checks again on
2026-10-15. G-AUTH passes its bounded authentication checklist; the current agent-operated
Aside private-browser renderer and streaming-download proof remain TASK-001-04
under the subsequent owner amendment. Android Chrome and Safari remain later
platform-support obligations.

## Current controlled private browser setup (TASK-007-01)

Use the existing one-owner daemon/profile and authenticated configuration
plan/apply flow under the
[configuration contract](../contracts/configuration.md#controlled-private-ingress-task-007-01).
Keep a verified local owner session and the exact protected
`--profile`/`--data-dir` selection available throughout setup and recovery.
Applying a configuration plan updates persisted Haeryong configuration and
performs a controlled daemon restart; it never mutates Tailscale Serve, installs
certificates or probes secondary endpoints.

### 1. Fresh remote-first versus already-local / unknown-history versions

Each rendered version binds its immutable tuple to one permanent sticky loopback
port in the per-account origin ledger. `haeryong doctor` reports configured
intent only and never reads the cold origin ledger or probes renderer ports.
Before configuring `network.renderer_mappings`, classify each target version:

- **Fresh remote-first version (imported via CLI and never opened locally):**
  Choose an unused loopback `upstream` (`http://127.0.0.1:<DEDICATED_PORT>`)
  **outside** `network.renderer_pool` (`[base_port, base_port + count - 1]`) and
  distinct from the management listener and frozen ports `9272`/`9273`. Its
  first remote `Open` reserves that exact port and records its canonical
  external HTTPS origin alias. Do **not** open a fresh mapped version locally
  before its first remote `Open`: local `Open` allocates from the dynamic pool
  (which excludes configured mapping upstreams), permanently binding a pool port
  so a later remote `Open` on the different configured upstream fails with
  `409 INVALID_TRANSITION` (`reason: "renderer_bind_conflict"`).
- **Already-local or unknown-history version:**
  A version that has ever been opened locally already owns a fixed sticky
  loopback port that cannot be reassigned. Discover that exact port through the
  verified local management endpoint's existing
  `POST /api/v1/versions/{version_id}/render-sessions` route using a current
  local owner credential, read **only** `render_origin` (and `session_id`), and
  immediately close the ephemeral session via
  `POST /api/v1/versions/{version_id}/render-sessions/{session_id}/close`.
  Never guess a port, read or edit `origin-state/ledger.json`, or send a
  credential across origins.

Retain the exact UUID in a private operation record before dispatch. A pending,
reconciling, missing or failed receipt stops this recipe; reconcile that original
key before any new operation. Never print the Open response, which includes a
one-use handoff secret. The following is a standalone shell script with
placeholder-only values and immediate close (using a
mode-`0600` header file so the bearer token never appears in process arguments
or logs):

```sh
#!/bin/sh
set -eu
umask 077
# 1. Verify the protected local owner credential matches the local management endpoint.
LOCAL_ENDPOINT="http://127.0.0.1:9270"
CREDENTIAL_FILE="/path/to/protected/clients/<PROFILE>/credential.json"
VERSION_ID="<LOCAL_VERSION_ID>"
OPERATION_UUID="<LOWERCASE_UUID_V4>"

jq -e --arg ep "$LOCAL_ENDPOINT" '.endpoint == $ep' "$CREDENTIAL_FILE" >/dev/null

HEADER_FILE="$(mktemp -t haeryong-auth-header.XXXXXX)"
RESPONSE_FILE="$(mktemp -t haeryong-render-open.XXXXXX)"
trap 'rm -f "$HEADER_FILE" "$RESPONSE_FILE"' EXIT
chmod 600 "$HEADER_FILE" "$RESPONSE_FILE"
printf 'Authorization: Bearer %s\n' "$(jq -r '.token' "$CREDENTIAL_FILE")" >"$HEADER_FILE"

# 2. Request a local render session with the closed empty JSON body {} and retained UUID.
if HTTP_CODE="$(
  curl -sS -o "$RESPONSE_FILE" -w '%{http_code}' \
    -X POST "${LOCAL_ENDPOINT}/api/v1/versions/${VERSION_ID}/render-sessions" \
    --header @"$HEADER_FILE" \
    --header "Idempotency-Key: ${OPERATION_UUID}" \
    --header "Content-Type: application/json" \
    --data '{}'
)"; then
  CURL_STATUS=0
else
  CURL_STATUS=$?
  HTTP_CODE=""
fi

# 3. Handle the outcome without minting a new UUID on transport loss or 202:
if [ "$CURL_STATUS" -eq 0 ] && [ "$HTTP_CODE" = "201" ]; then
  jq -e --arg ver "$VERSION_ID" '
    .data.version_id == $ver and
    (.data.render_origin | type == "string" and length > 0) and
    (.data.session_id | type == "string" and length > 0)
  ' "$RESPONSE_FILE" >/dev/null
  STICKY_UPSTREAM="$(jq -r '.data.render_origin' "$RESPONSE_FILE")"
  SESSION_ID="$(jq -r '.data.session_id' "$RESPONSE_FILE")"
elif [ "$CURL_STATUS" -ne 0 ] || [ "$HTTP_CODE" = "202" ]; then
  # Uncertain transport or accepted pending state: reconcile the original OPERATION_UUID first.
  haeryong --profile "<PROFILE>" --credential "$CREDENTIAL_FILE" \
    operations show "$OPERATION_UUID" --json >"$RESPONSE_FILE"
  jq -e --arg ver "$VERSION_ID" '
    .data.state == "completed" and
    .data.original_status == 201 and
    .data.result_metadata.version_id == $ver and
    (.data.result_metadata.render_origin | type == "string" and length > 0) and
    (.data.result_metadata.session_id | type == "string" and length > 0)
  ' "$RESPONSE_FILE" >/dev/null
  STICKY_UPSTREAM="$(jq -r '.data.result_metadata.render_origin' "$RESPONSE_FILE")"
  SESSION_ID="$(jq -r '.data.result_metadata.session_id' "$RESPONSE_FILE")"
else
  # Definitive HTTP failure (4xx/5xx): stop setup and inspect the typed error code/details.
  jq '.error' "$RESPONSE_FILE" >&2
  exit 1
fi

# 4. Immediately close the owned ephemeral delivery session; the sticky port reservation remains durable.
CLOSE_CODE="$(curl -sS -o /dev/null -w '%{http_code}' \
  -X POST "${LOCAL_ENDPOINT}/api/v1/versions/${VERSION_ID}/render-sessions/${SESSION_ID}/close" \
  --header @"$HEADER_FILE" \
  --header "Content-Type: application/json" \
  --data '{}')"
[ "$CLOSE_CODE" = "204" ] || exit 1

printf 'Discovered sticky upstream for %s: %s\n' "$VERSION_ID" "$STICKY_UPSTREAM"
```

If discovery or a subsequent open returns a definitive error (`409`
`INVALID_TRANSITION` with `reason: "renderer_bind_conflict"` or
`"renderer_pool_exhausted"`, a `503` ledger/binding error, or `401`/`403`), stop
setup for that version and follow section 4 below; never infer a port or
reassign a reservation.

### 2. Preview exposure impact, confirm apply, verify restart and add private Serve routes

`haeryong config plan` merges the supplied JSON/YAML changes into the current
configuration. When an already-local version's discovered `STICKY_UPSTREAM`
port lies inside the current `network.renderer_pool` (for example,
`http://127.0.0.1:9281` when `base_port` is `9281`), shift or expand
`network.renderer_pool` **in the same configuration plan** so every mapped
`upstream` is outside the new dynamic pool. Existing reservations remain sticky
across pool changes.

```sh
# 1. Inspect existing Tailscale Serve routes, daemon identity and current config_revision.
tailscale serve status --json
haeryong --profile "<PROFILE>" service status --json
haeryong --profile "<PROFILE>" config show --json

# 2. Prepare a network patch combining:
#    - <LOCAL_VERSION_ID>: already-local version mapped to its discovered sticky upstream (9281)
#    - <FRESH_VERSION_ID>: fresh remote-first version mapped to an unused outside-pool upstream (9292)
#    - renderer_pool shifted to base_port 9283 (count 8: 9283..9290) in the same plan so 9281 and 9292 lie outside the pool.
cat >./private-ingress.json <<'EOF'
{
  "network": {
    "remote_enabled": true,
    "external_base_url": "https://<NODE_NAME>.<TAILNET>.ts.net:9271",
    "allowed_origins": [
      "http://127.0.0.1:9270",
      "https://<NODE_NAME>.<TAILNET>.ts.net:9271"
    ],
    "renderer_pool": {
      "base_port": 9283,
      "count": 8
    },
    "renderer_mappings": [
      {
        "version_id": "<LOCAL_VERSION_ID>",
        "origin": "https://<NODE_NAME>.<TAILNET>.ts.net:9291",
        "upstream": "http://127.0.0.1:9281",
        "reassignable": false
      },
      {
        "version_id": "<FRESH_VERSION_ID>",
        "origin": "https://<NODE_NAME>.<TAILNET>.ts.net:9292",
        "upstream": "http://127.0.0.1:9292",
        "reassignable": false
      }
    ]
  }
}
EOF

# 3. Preview the plan and verify impact.exposure (changed, before, after,
#    private_proxy_action, proxy_configuration_changed_by_commit: false) and controlled_restart_required.
haeryong --profile "<PROFILE>" \
  config plan ./private-ingress.json --expected-config-revision "<REV>" --json

# 4. Commit with explicit --confirmed (omitting --confirmed is refused with exit 2).
haeryong --profile "<PROFILE>" \
  config apply "<PLAN_ID>" --expected-config-revision "<REV>" --confirmed --json

# 5. After the automatic controlled restart, verify that instance_id rotated while
#    library_id, profile_id and endpoint remained intact, and check doctor endpoint_checks.
haeryong --profile "<PROFILE>" service status --json
haeryong --profile "<PROFILE>" doctor --json

# 6. Add only the approved private HTTPS Tailscale Serve routes (preserving upstream Host and browser Origin).
tailscale serve --bg --https=9271 http://127.0.0.1:9270
tailscale serve --bg --https=9291 http://127.0.0.1:9281
tailscale serve --bg --https=9292 http://127.0.0.1:9292
tailscale serve status --json
```

The bounded web proof ([private ingress evidence](../testing/private-ingress.md))
selected these disposable routes after fresh host inspection:

| External private HTTPS port | Loopback upstream | Role |
| --- | --- | --- |
| 9271 | `http://127.0.0.1:9270` | Trusted application and API |
| 9291 | `http://127.0.0.1:9291` | First immutable version renderer (already-local discovery branch) |
| 9292 | `http://127.0.0.1:9292` | Second immutable version renderer (fresh remote-first branch) |

These ports are observed fixture values, not reservations or a version/storage
quota. Pair the private browser origin once through normal Connect and an
existing local owner through its trusted Connect approval controls. Protected
CLI approval requires the complete exact JSON intent in the
[authenticated session instructions](../testing/authenticated-sessions.md).
Local and HTTPS origins keep separate remembered bearer credentials. Remove only owned routes
with `tailscale serve --https=<PORT> off` and compare
`tailscale serve status --json` with the pre-change baseline; never reset Serve,
delete unrelated mappings or enable Funnel.

### 3. Manual per-version provisioning handoff (TASK-007-02 / TASK-007-03 / TASK-007-04)

Haeryong does not automatically provision a `renderer_mappings` entry or a
Tailscale Serve route when a version is imported or generated. Every additional
version to be rendered over private HTTPS requires its own non-reassignable
`renderer_mappings` entry, a confirmed `config plan` / `config apply ... --confirmed`
cycle (which restarts the daemon), and its matching `tailscale serve --bg --https=<PORT>`
route. This is a current endpoint-provisioning procedure handed off to
[TASK-007-03](../tasks/EPIC-007/TASK-007-03.md) (and retained across
TASK-007-02 and TASK-007-04), never a package size, HTML page count or storage
quota. Unmapped remote versions remain available for metadata, search, evidence,
review, notes, Ask/Q&A and authenticated attachment downloads while reporting
`renderer_available: false` (`renderer_unavailable_reason: "renderer_remote_ingress"`),
whereas every version selected for private browser or mobile/tablet Reader
verification must be mapped so its multi-page HTML/CSS/JS renders in its
isolated HTTPS origin (attachment fallback never satisfies a Reader gate).

### 4. Occupied-listener, bind-conflict and forward recovery

- **Occupied management listener recovery (`T-007-01-01`):** An occupied
  configured `network.listener.port` fails startup (`address already in use`)
  without listener takeover or silent fallback, leaving the existing listener on
  that port untouched. Recover locally by starting the daemon with an explicit
  unused `--port <UNUSED_PORT>` on the same `--profile`/`--data-dir`, then run
  `haeryong doctor --json` through that verified local runtime endpoint
  (`ephemeral_override: true`) and review or update the persisted
  `network.listener.port` via `haeryong config plan` and
  `haeryong config apply ... --confirmed`.
- **Renderer bind conflict or pool exhaustion recovery:** If `Open` returns
  `409 INVALID_TRANSITION` with `reason: "renderer_bind_conflict"`, either the
  version was opened locally before its first remote open and owns a different
  sticky pool port, another immutable tuple already owns the requested
  `upstream` or external origin alias, or another local process occupies that
  port. Do not delete the ledger or reassign ports: discover the version's
  actual sticky `render_origin` locally (section 1) and update its mapping
  `upstream` (and shift/expand `renderer_pool` if needed) in a confirmed config
  plan, or choose an unused external origin if another tuple owns that alias.
  An existing sticky loopback port never moves: if another process occupies it,
  preserve that listener and stop setup until the port is legitimately available.
  A different unused loopback port is an option only for a never-reserved version. On `reason: "renderer_pool_exhausted"`, widen
  `network.renderer_pool` and apply with `--confirmed`.
- **Old-binary forward recovery (no ledger deletion):** Remote `Open` records
  the tuple's canonical HTTPS origin in the additive `external_origins` field of
  `origin-state/ledger.json` (purely local seven-field records remain
  unchanged). Once `external_origins` is present, an older pre-`TASK-007-01`
  binary may fail closed when reading that ledger. Always recover **forward** by
  running the current compatible `haeryong` binary; **never** delete, truncate
  or edit `app-state.json` or `origin-state/ledger.json`, and never attempt to
  release or reassign reserved ports or external origin aliases as rollback.

[Private ingress evidence](../testing/private-ingress.md) owns the actual browser,
route preservation, failures, cleanup and qualification limits. Physical mobile
and second-device use were excluded by the current owner direction; no Stage A
or other platform passage follows from the same-host HTTPS proof.

### 5. Internal darwin/arm64 test package and Stage A handoff (TASK-007-06)

For `TASK-007-06` (`EPIC-007`), the internal test package is staged outside the
repository working tree and contains only:

- `haeryong`: the normal untagged `darwin/arm64` headless binary built from `go build ./cmd/haeryong` (`sha256:86e42c5654ce33eca31f72129cc402565d65aa7985d5a35d58b2da2f02c9d222`, verified free of `haeryong_test_state` or provider live/replay build tags);
- `SHA256SUMS`: exact SHA-256 digests for the staged files;
- `INSTALL.md`: concise operator notes using only supported flags (`--profile <PROFILE>`, `--data-dir <PATH>`, `--client-dir <PATH>`, `--credential <PATH>`, `--port <PORT>`, `--provider-binary <PATH>`) and `<existing-node>` / `<tailnet>` placeholders for `network.external_base_url`, `network.allowed_origins`, `network.renderer_mappings`, and `tailscale serve --bg --https=<PORT> http://127.0.0.1:<UPSTREAM_PORT>`.

The internal package bundles no owner credentials, no library data, no Codex/provider binary, and no Tailscale certificates, performs no global installation (`/usr/local/bin` or launchd registration), and makes no production release pipeline (`EPIC-010`) or desktop `Wails` GUI (`EPIC-009`) claim. See [`docs/testing/stage-a-gate.md`](../testing/stage-a-gate.md) for the Stage A verification record and qualification limits.
