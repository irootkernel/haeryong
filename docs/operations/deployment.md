# Deployment and local/remote operation

**Planning runbook:** Haeryong commands below are to be implemented. No listener, service, firewall or tailnet was changed while preparing this bundle.

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

Keep management on its own origin. Bind experimental renderer origin A and B to two immutable versions, each with a root-relative namespace. The first candidate is distinct external ports mapped to separate internal renderer prefixes on the same daemon. The prefix is an upstream routing label, not the browser's version-isolation mechanism; distinct external origins provide that separation. A possible future harness mapping is:

| Private external endpoint | Candidate daemon target | Meaning |
| --- | --- | --- |
| Existing node name, HTTPS 9270 | http://127.0.0.1:9270 | Trusted management UI/API |
| Same node name, HTTPS 9272 | http://127.0.0.1:9270/__renderer/origin-a/ | Version A renderer only |
| Same node name, HTTPS 9273 | http://127.0.0.1:9270/__renderer/origin-b/ | Version B renderer only |

The [Serve CLI](https://tailscale.com/docs/reference/tailscale-cli/serve) documents HTTPS port selection and reverse-proxy targets including paths. Verify the installed proxy's actual prefix joining, escaped paths, Host forwarding, ranges and redirects with the harness; this table is not a proven Serve configuration. Requests for /data.json on A must arrive at A's internal mapping, and B at B's mapping, independent of Referer. Do not point both renderer ports at an indistinguishable backend root. The mapping prefix never authenticates a request; content delivery still follows the [delivery authority](../contracts/authentication.md#content-delivery-authority). Renderer routes cannot expose the trusted shell or management API, including through traversal or redirect fallback.

Run the [TASK-001-04 closing checklist](../tasks/EPIC-001/TASK-001-04.md#observable-completion-and-tests) on pinned Wails, the selected primary browser and one real iOS or iPadOS Safari device. Record each chosen upstream mapping, delivery mechanism, automatic reconnect, storage behavior and removal procedure before G-CONTRACT. Other agreed support targets remain later release obligations, not additional early closing environments.

Port allocation/reuse is an implementation result, not a port per stored document. Do not reassign an origin while an old client/Service Worker can control its new version; prove safe reuse or keep it unavailable for reassignment. Available endpoint resources may fail explicitly, but never become an HTML/file-count/storage quota, silent single-version fallback or false pass. Additional public exposure, custom domains or wildcard certificate operations still require an explicit owner decision. If this candidate fails, record the actual failed mapping and alternatives instead of repeating path-prefix-only experiments.

After private HTTPS works, pair the remote browser through an existing owner device or local bootstrap. Requests without a valid owner session or its scoped content-delivery authority receive no library metadata/assets. G-AUTH remains `not_run` until a real second-device flow, remembered reconnect, Host/Origin handling and session revocation have recorded results.

## Large result directories and transfer

Accept a selected directory, ZIP or standalone HTML and automatically preserve/inventory its files. Multiple HTML pages, nested folders and accompanying file types require no special install setting, manual manifest or artificial size/count/depth quota. The owner may choose an artifact destination inside a project, while unrelated source files remain outside the generation/import write set.

Use incremental import/export, lazy page/asset loading, cursor-based file inventories, range responses where applicable and progress/cancellation/resumption. Configure the actual private proxy and download transport to stream large requests/responses; its body buffering, size ceiling or idle timeout must not silently reinstate the removed package limits. Use the upload-session protocol where needed to transfer successive chunks without making one giant request. Use authenticated `GET /uploads` discovery and per-upload status to recover pending work, actual staged bytes and accepted progress after reconnect/restart. Offer authorized resume or explicit abandonment while preserving current adoption and persisted recovery pins. Delivery lease expiry or mobile suspension does not abandon those uploads. Report a real proxy, disk, network or device failure with the interrupted operation's recovery state, not success with omitted files.

The complete directory need not fit in browser memory before its first page opens or before an export starts. Verify streaming browser/native download behavior on each declared initial target, including private iOS/iPadOS Safari and the pinned Wails client. Record whether the chosen transport uses a trusted fetch/service-worker handoff, native file streaming or a content-delivery credential under [authentication](../contracts/authentication.md#content-delivery-authority). Demonstrate interrupted multi-request Range/retry/resume, a download lifetime independent of page-channel closure/suspension, observed progress, automatic authorized recovery and streaming without a whole-file Blob. Record actual platform suspension behavior without promising background execution that the device does not provide. An owner's finite device/storage capacity and a browser's supported preview formats remain real capabilities; no universal infinite-resource or native-program execution claim is made.

## Source and provider availability

Reading saved explanations requires a reachable daemon/content store. Source-grounded Q&A also needs the selected exact source and a supported provider route. Project/source/provider selections are remembered; a routine read does not prompt again. A missing provider does not prevent reading saved content.

Pinned retained evidence may remain usable while its checkout is offline, but it does not become the current working copy. Show source identity, availability, bytes origin and limits separately. A phone cannot access a sleeping laptop repository merely because it reaches Haeryong. A current-source request without that source remains unavailable rather than silently substituting an old snapshot.

An external agent may use the owner's existing environment and sign-in. A managed adapter reports its native permission/configuration limits; a separate worker jail is not an install prerequisite. Haeryong does not copy provider tokens to browser devices or autonomously modify the source checkout.

## Startup and recovery

Check profile lock/identity, schema compatibility, content references/digests, the configured listener, client/API compatibility, discoverable resumable uploads and pending recovery jobs. Do not migrate with competing daemon binaries. Normal startup preserves valid sessions and remembered source/provider choices.

Application-managed restore invalidates restored owner/agent and content-delivery sessions once, pauses scheduled AI/timed purge/exposure and presents the relevant policy/reconciliation impact. After the owner reconnects and resumes that policy, subsequent normal restarts remain ordinary startup. Reconcile unknown external attempts before redispatch. Full OS/VM rollback detection is not guaranteed. A missing daemon prevents remote writes and must not be represented as Saved. On a headless host, an owner CLI invoked through an already authorized SSH session can use the local bootstrap/recovery path with that OS user's protected local access. Restore does not preserve an old browser credential to bypass reconciliation; document this host access path for the selected deployment.

## Executed stack experiment

[The TASK-001-02 experiment](../testing/stack-spike.md) records the actual macOS ARM64 Wails/WebKit and Linux ARM64 headless combination, reproduction commands and lifetime observations. It is an isolated loopback fixture without owner authentication, service installation or private ingress. The declared macOS/Linux AMD64 release targets still require their advertised workflow checks; this record does not broaden tested support. TASK-001-03 owns the next authenticated private-route experiment.
