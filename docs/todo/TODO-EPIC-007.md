# EPIC-007 execution dossier

**Consumer:** [EPIC-007](../epics/EPIC-007.md) only.
**Role:** Detailed SOT: a thin integration map linking canonical owners. It owns no product requirement, identity, order,
dependency, planning state or evidence result.
**Lifecycle authority:** The [roadmap](../roadmap.md) and canonical Epic/Task frontmatter. Read member status from those canonical records;
this dossier records no transition.

## Authority and precedence

Product truth follows the [documentation index authority order](../README.md#what-is-authoritative). The [execution
protocol](../execution-protocol.md) owns [status and gate semantics](../execution-protocol.md#status-and-gate-semantics), the
[shared definition of done](../execution-protocol.md#shared-definition-of-done) and [responsibilities and
evidence](../execution-protocol.md#responsibilities-and-evidence). The root [TESTING.md](../../TESTING.md) owns common commands
and the exact-candidate mandatory gate policy. Gate applicability comes from the [acceptance
catalog](../testing/acceptance-catalog.md#current-applicability),
[traceability](../testing/traceability.md#inherited-acceptance-to-primary-work), [contract cases](../testing/contract-cases.md)
and [knowledge quality](../testing/knowledge-quality.md).

The original T-007-* assertion texts in each Task file are canonical; this dossier only maps them. If this dossier conflicts with
a canonical owner, the owner prevails and the dossier is corrected. The dossier never broadens scope, adds a gate or authorizes
an action.

## Invariants carried through every Task

- Complete result directories and ordinary html_js_v1 JavaScript, without requiring an author manifest or index.html, imposing an extension allowlist, or introducing
size/count/depth caps; proxy, browser or device limits are recorded limitations, never product ceilings:
[personal-tool](../changes/2026-10-10-personal-tool-revision.md) and
[directory-results](../changes/2026-10-10-directory-results-revision.md) amendments, [large result
directories](../operations/deployment.md#large-result-directories-and-transfer).
- One owner, per-device bearer sessions and the same configurable listener locally and remotely; private reachability or
forwarded identity never authenticates: [remembered sessions](../contracts/authentication.md#remembered-sessions-and-transport),
[request protections](../contracts/authentication.md#request-protections), [network
baseline](../operations/deployment.md#simple-network-baseline).
- Generated frames get no host DOM, storage, token, native-binding or management authority, and a broken page has a recovery
path: [viewer protocol](../contracts/viewer-protocol.md), M08 [current
applicability](../testing/acceptance-catalog.md#current-applicability).
- Remembered source/provider choices; no automatic laptop source or credential access; visible current-source gaps;
`observed_input` distinct from `posthoc_verified`: [remote source
behavior](../contracts/source-access.md#remote-source-behavior), [source and provider
availability](../operations/deployment.md#source-and-provider-availability), [AI adapter](../contracts/ai-adapter.md).
- Explicit whole-version Review, page-exact notes, independent resource revisions and no false Saved: [multi-device
conflict](../ui/flows.md#multi-device-conflict), [preconditions and
receipts](../contracts/http-api.md#preconditions-receipts-and-retry-matrix).
- D-10 boundary: tailnet-only private HTTPS; no Funnel, public exposure, new domain or certificate: [Tailscale
candidate](../operations/deployment.md#tailscale-serve-candidate), [renderer-port
experiment](../operations/deployment.md#approved-additional-port-renderer-experiment).
- Excluded: tailnet enrollment automation, host provisioning, native mobile app, offline mutation sync, source sync connector,
maintenance (EPIC-008/009) and release qualification (EPIC-010).

## Task map

| Order | Task | Depends on | Integration inputs beyond the Task's own list | Hands off |
| --- | --- | --- | --- | --- |
| 1 | [TASK-007-01](../tasks/EPIC-007/TASK-007-01.md) | EPIC-006 | [Tailscale candidate](../operations/deployment.md#tailscale-serve-candidate), [renderer-port experiment](../operations/deployment.md#approved-additional-port-renderer-experiment), [listener configuration](../contracts/configuration.md#listener-origin-and-remembered-sessions), [render endpoints](../contracts/http-api.md#endpoint-catalog), [origin ownership](../testing/reader-viewer.md#origin-ownership-and-delivery) | Chosen route, endpoint doctor, route guidance and private renderer construction for TASK-007-02 |
| 2 | [TASK-007-02](../tasks/EPIC-007/TASK-007-02.md) | TASK-007-01 | [Pairing sequence](../contracts/authentication.md#pairing-sequence), [content delivery authority](../contracts/authentication.md#content-delivery-authority), [S01](../ui/screens.md#s01-connect-and-device-approval), [security](../operations/security.md) | Remembered remote device and revocation, including renderer delivery, for TASK-007-03 |
| 3 | [TASK-007-03](../tasks/EPIC-007/TASK-007-03.md) | TASK-007-02 | [Environment scope](../tasks/EPIC-007/TASK-007-03.md#environment-prerequisites-and-completion-scope), [accessibility criteria](../ui/design-system.md#accessibility-requirements-for-this-project), [TASK-003-06 baseline](../testing/responsive-cross-client-gate.md) | Mobile workflow on the selected physical device for TASK-007-04 |
| 4 | [TASK-007-04](../tasks/EPIC-007/TASK-007-04.md) | TASK-007-03 | [SSE and invalidation](../contracts/jobs-events.md#sse-and-invalidation-contract), [reader preferences and position](../ui/flows.md#reader-preferences-and-position), [schema registry](../contracts/schema-registry.md) | Revision-aware client and reconnect behavior for TASK-007-05 |
| 5 | [TASK-007-05](../tasks/EPIC-007/TASK-007-05.md) | TASK-007-04 | [Startup and recovery](../operations/deployment.md#startup-and-recovery), [remote source behavior](../contracts/source-access.md#remote-source-behavior) | Headless host and source/provider boundary proof for TASK-007-06 |
| 6 | [TASK-007-06](../tasks/EPIC-007/TASK-007-06.md) | TASK-007-05 | [Stage closure](../testing/acceptance-catalog.md#stage-and-quality-closure), [progressive checkpoints](../testing/knowledge-quality.md#progressive-use-checkpoints), [Epic gate template](../templates/epic-gate.md) | Stage A gate record and EPIC-007 closeout |

## Cross-Task execution boundaries

These boundaries resolve handoffs inside the existing Tasks. They change no membership, dependency, requirement, acceptance or
supplemental metadata.

### Private renderer construction

The [render endpoints](../contracts/http-api.md#endpoint-catalog) currently deny remote ingress: bootstrap reports
`renderer_unavailable_reason=renderer_remote_ingress`, and open/renew return `503 FEATURE_NOT_READY` with that reason, assigning
remote construction to EPIC-007. Within its outcome of connecting the service to the chosen private route, TASK-007-01 owns that
construction:

- trusted-origin and private renderer-origin mapping under D-10 and the existing exact Host/Origin ingress classification in the
[network baseline](../operations/deployment.md#simple-network-baseline);
- validated private render-bootstrap, render-session open and renew delivery through the route, with the [content delivery
authority](../contracts/authentication.md#content-delivery-authority) binding and the ambiguous-tie refusals preserved;
- the corresponding contract, schema-registry, deployment and Reader record updates, replacing the current denial only where the
construction is proven.

TASK-007-02 repeats revocation, expiry and memory-only behavior against delivery sessions. TASK-007-03 consumes the construction
and proves actual Reader behavior on the selected physical phone or tablet. If TASK-007-01 cannot establish a construction, its
affected assertions are `blocked` and the [owner-decision rule](../decisions.md#owner-decisions-and-when-they-are-needed) for
renderer paths applies; a denial or attachment-only fallback cannot pass Stage A Reader use.

### Second device and physical device
- TASK-007-01 and TASK-007-02 require an actual private second owner device on the chosen route (T-007-01-02; TASK-007-02 work
item 3). This Task pair does not itself require a phone or tablet. An origin-separated browser on the service host is not a
second device.
- TASK-007-03 and TASK-007-06 require the recorded owner-provided physical phone or tablet under the [TASK-007-03 environment
scope](../tasks/EPIC-007/TASK-007-03.md#environment-prerequisites-and-completion-scope). The same device may serve both roles
when it is selected early; that choice is not imposed at TASK-007-01 start.

### Reconnect and independent validators

TASK-007-04 owns the client reconciliation over the existing contracts:

- On a changed `stream_epoch`, or a cursor the server can no longer replay, discard incremental assumptions and fetch the
authorized full state before resuming events; preserve unresolved local intent and never treat a request receipt as the current
resource version ([SSE and invalidation](../contracts/jobs-events.md#sse-and-invalidation-contract), [replay and
resync](../contracts/jobs-events.md#replay-and-resync)).
- Reader preference, the version last-opened pointer (reading-position) and each page-position are separate resources with
independent ETags. A page-position save never moves the pointer or another page's record; a 412 retains local state, re-reads
that resource and reconciles before another write ([endpoint catalog](../contracts/http-api.md#endpoint-catalog), [schema
registry](../contracts/schema-registry.md), [reader preferences and position](../ui/flows.md#reader-preferences-and-position)).
- Lost acknowledgements reconcile the original operation ID before any retry ([multi-device
conflict](../ui/flows.md#multi-device-conflict)).

## Environment and permission decisions

No tracked document records a Stage A selection. The dated preflight in
[TASK-001-01](../tasks/EPIC-001/TASK-001-01.md#environment-prerequisites-and-owners) is historical, and the agent-operated Aside
browser chosen for G-VIEW is not a device result ([owner
amendment](../decisions.md#owner-amendment-2026-10-15-agent-operated-private-browser-gate)).

| Decision | Decision owner | Needed before | Current state |
| --- | --- | --- | --- |
| Private second owner device for route and session proof | Master supplies the device and access | TASK-007-01 completion (T-007-01-02) | Unselected |
| Physical phone or tablet for the complete mobile workflow | Master supplies the device and access | TASK-007-03 start | Unselected |
| Stage A service host, and whether the TASK-007-05 headless host is the same machine | Master, per the [owner-decision table](../decisions.md#owner-decisions-and-when-they-are-needed) | TASK-007-01 route application; headless host before TASK-007-05 | Unselected |
| Private route: ingress adapter, external origin and renderer ports | Implementer proposes after inspecting existing routes; Master authorizes the network change and confirms exposure impact | TASK-007-01 implementation step 2 | Unselected; no Stage A route configured |
| Provider host-mode use and host source/disclosure scope | Master | Provider before the TASK-007-03 Ask observation; source scope before TASK-007-05 | No tracked EPIC-007 grant |
| Primary browser/WebView and Wails build versions | Implementer records observed versions | TASK-007-06 | Recorded by the consuming Task |
| Owner-performed reader task | Master; participation optional | TASK-007-06 | Recommendation, not a gate |

Record a selection only when actually made, with observed device, OS, browser, host and route identities, in the consuming Task's
evidence. A missing required selection makes the dependent assertion `blocked`, never `pass`; emulation is labelled and is no
substitute. Untested P-11 targets remain release obligations ([P-11](../decisions.md#proposed-execution-choices), [support
tiers](../operations/deployment.md#proposed-support-tiers)).

## External-effect authority

| Effect | Required authority | First consumer | Boundary |
| --- | --- | --- | --- |
| Private route or Serve change on the host | Master's network-change authorization plus the product's owner impact confirmation for exposure | TASK-007-01 | Inspect existing routes first; tailnet-only under D-10; rollback removes only Haeryong routes |
| Tailnet enrollment | Master | TASK-007-01 | Never automated |
| Operating a private second device or physical phone/tablet | Master supplies the device and access; an authorized implementer may operate it | TASK-007-01; TASK-007-03 | Record observed identities |
| Host selection, provisioning, user-level service install or long-running service on the selected host | Master | TASK-007-01 host; TASK-007-05 headless host | Disposable test profiles keep their test-layer rules |
| Real provider calls | Explicit scoped authorization and the live-tag procedure in [provider reproduction](../testing/provider-adapter.md#reproduction) | TASK-007-03 Ask; TASK-007-05; TASK-007-06 | No billing fallback, account change or runs outside scope |
| Host source/disclosure scope | Master authorizes the exact host source | TASK-007-05 | Grants no laptop repository or credential access |
| Pairing approval, rejection and revocation on real devices | An existing trusted owner session under the product's own authorization; an authorized implementer may operate it with the access Master supplied | TASK-007-02 | Generated frames cannot approve or initiate |
| Restore, purge or other destructive operations | Disposable profiles by default; a real library needs owner impact confirmation and Master's authorization | TASK-007-06 | [Restore protocol](../operations/backup-recovery.md#restore-protocol) |
| Staging, commits, push or publication | Separate authorization under the repository commit rules | Every Task | Release remains EPIC-010 |

## Assertion map

A derived reading; the Task files' assertion texts and frontmatter lists remain authoritative, and each Task records its actual
bounded contribution.

| Assertion | Contributes to | Canonical owners | Prerequisite |
| --- | --- | --- | --- |
| T-007-01-01 | M16 | [Network baseline](../operations/deployment.md#simple-network-baseline), [listener configuration](../contracts/configuration.md#listener-origin-and-remembered-sessions) | Disposable profile; no network change |
| T-007-01-02 | M15, M17, X04 | [Tailscale candidate](../operations/deployment.md#tailscale-serve-candidate), [render endpoints](../contracts/http-api.md#endpoint-catalog) | Private second device, host and route; route-change authority |
| T-007-01-03 | M16, M17, X04 | [Request protections](../contracts/authentication.md#request-protections), [network baseline](../operations/deployment.md#simple-network-baseline) | Same route as T-007-01-02 |
| T-007-02-01 | M17, X03 | [Pairing sequence](../contracts/authentication.md#pairing-sequence), [S01](../ui/screens.md#s01-connect-and-device-approval) | Local or remote client |
| T-007-02-02 | C05, M17, M20, X04 | [Remembered sessions](../contracts/authentication.md#remembered-sessions-and-transport), [content delivery authority](../contracts/authentication.md#content-delivery-authority) | Private second device on the route |
| T-007-02-03 | M17, X03, X04 | [Pairing sequence](../contracts/authentication.md#pairing-sequence), [remembered sessions](../contracts/authentication.md#remembered-sessions-and-transport) | Same as T-007-02-02 |
| T-007-03-01 | C03, C08, M15, X02, X19, X20 | [Environment scope](../tasks/EPIC-007/TASK-007-03.md#environment-prerequisites-and-completion-scope), [flows](../ui/flows.md), [viewer protocol](../contracts/viewer-protocol.md) | Selected physical device; TASK-007-01 renderer construction; provider authority for Ask |
| T-007-03-02 | C03, M15 | [Accessibility criteria](../ui/design-system.md#accessibility-requirements-for-this-project), [TASK-003-06 baseline](../testing/responsive-cross-client-gate.md) | Same device |
| T-007-03-03 | M15, X18 | [Large result directories](../operations/deployment.md#large-result-directories-and-transfer), [screens](../ui/screens.md) | Same device; actual picker and destination |
| T-007-04-01 | C07, C08, M18, X11, X13 | [Preconditions and receipts](../contracts/http-api.md#preconditions-receipts-and-retry-matrix), [reader preferences and position](../ui/flows.md#reader-preferences-and-position) | Two clients, including a remote device |
| T-007-04-02 | M18, X12 | [Jobs and events](../contracts/jobs-events.md) | A real provider job only under provider authority |
| T-007-04-03 | M18, M20, X11, X12, X13 | [Replay and resync](../contracts/jobs-events.md#replay-and-resync), [multi-device conflict](../ui/flows.md#multi-device-conflict) | Offline faults first; real provider only within scope |
| T-007-05-01 | M15 | [Startup and recovery](../operations/deployment.md#startup-and-recovery), [source and provider availability](../operations/deployment.md#source-and-provider-availability) | Selected headless host without Wails; original checkout unavailable |
| T-007-05-02 | M19 | [Remote source behavior](../contracts/source-access.md#remote-source-behavior) | Same host |
| T-007-05-03 | M19 | [Source access](../contracts/source-access.md), [AI adapter](../contracts/ai-adapter.md) | Host source scope and provider host-mode authority |
| T-007-06-01 | C01–C09, M15–M19, X03, X04, X11, X12, X13, X15, X19, X20 | [Stage closure](../testing/acceptance-catalog.md#stage-and-quality-closure), [traceability](../testing/traceability.md#inherited-acceptance-to-primary-work) | Complete selected Stage A environment, real provider, physical device with Wails closed |
| T-007-06-02 | M08, M10, M14, M20, X04, X07, X13, X16 | [Current applicability](../testing/acceptance-catalog.md#current-applicability), [startup and recovery](../operations/deployment.md#startup-and-recovery), [restore protocol](../operations/backup-recovery.md#restore-protocol) | Selected installation; disposable profile for restore |
| T-007-06-03 | C03, C04, C08, M19, X15 | [Progressive checkpoints](../testing/knowledge-quality.md#progressive-use-checkpoints) | Real corpus; evaluator recorded |

Initial supplemental owners and procedures stay in [contract cases](../testing/contract-cases.md); EPIC-007 repeats only the
regressions its Tasks list. The Stage A closing set is TASK-007-06's three assertions. EPIC-010 references to M08/M14/M20 are
release regression, not prerequisites.

## Prior evidence applicability

| Record | Usable as | Not Stage A proof because |
| --- | --- | --- |
| [G-AUTH experiment](../testing/auth-ingress-spike.md) | Precedent for private HTTPS forwarding to the one listener, exact Host/Origin checks and header bearer | Disposable single-host fixture; not production ingress or a second device |
| [G-VIEW experiment](../testing/viewer-spike.md) and [Reader delivery](../testing/reader-viewer.md#origin-ownership-and-delivery) | Renderer-origin construction precedent and the current local construction | Remote ingress is denied today; no physical device |
| [TASK-003-06 gate](../testing/responsive-cross-client-gate.md) | Responsive and accessibility baseline linked by T-007-03-02 | Phone-width captures and emulation; no private remote device |
| [Desktop companion](../testing/desktop-companion.md) | Native lifecycle and independent daemon basis for M10/M14 | Not the selected installation during remote use with Wails closed |
| [Two-project knowledge](../testing/two-project-knowledge.md), [owner records](../testing/owner-records.md), [backup and recovery](../testing/backup-recovery.md) | Regression bases for C05–C09, X13, X19 and M20 restore | Local clients; no remote second-device reconciliation |
| [EPIC-006 gate](../testing/epic-006-ai-gate.md), [AI workflows](../testing/ai-workflows.md) | Real provider, saved Q&A and semantic observations with their own qualifications | Claims no physical private-device proof; Stage A Q&A runs on the selected environment |

Reuse requires recorded applicability to the current candidate source and environment. Reuse never replaces an actual Stage A
observation, and earlier failures keep their results.

## Documentation and check obligations
- Each Task creates its evidence record under `docs/testing/` from the [task evidence template](../templates/task-evidence.md);
the Task chooses its name. It updates only canonical owners it actually changes, for example
deployment/configuration/authentication/HTTP/schema registry/Reader record (TASK-007-01), authentication/screens/security
(TASK-007-02), design system/wireframes/flows/screens (TASK-007-03), HTTP/jobs-events/flows (TASK-007-04), deployment/source
access/AI adapter (TASK-007-05), and the Stage A gate record, knowledge-quality worksheet and install notes (TASK-007-06).
- Keep the root documentation index, `AGENTS.md` project index and this dossier factually current. After a documentation change,
regenerate [MANIFEST](../MANIFEST.md) and run `python3 -B docs/testing/validate_docs.py`; neither proves product behavior.
- Mandatory gates follow [TESTING.md](../../TESTING.md) and the execution protocol for the exact current candidate, including
frontend and desktop checks when those inputs change. Reuse the existing browser drivers and native ReaderMode stages where
applicable. Device and route procedures are recorded manual procedures with observed identities; real-provider work uses only the
live-tag procedure.
- After a repair, rerun the affected checks and dependent checks the change invalidates, and retain still-valid evidence, per the
[evidence bundle rule](../tasks/EPIC-007/TASK-007-06.md#evidence-bundles-and-affected-checks). This dossier adds no rerun
requirement; a prose-only repair carries its affected documentation checks.
- Independent review and acceptance follow the execution protocol. Entries in [deferred feedback](../deferred-feedback/README.md)
keep their owner unless a Task adopts one; current correctness is never deferred.
- Runtime identities and paths, private device/account identifiers beyond the observed version facts evidence needs, secrets and
hidden model reasoning stay out of tracked evidence.

## Handoffs and closeout

- Each Task hands off to its frontmatter successor after its evidence, review, acceptance and completion commit under existing
authority.
- TASK-007-06 hands the Stage A gate record ([Epic gate template](../templates/epic-gate.md)) to whole-Epic internal validation
and Master's overall acceptance of the prepared closeout. The next ordered owner is then
[TASK-008-01](../tasks/EPIC-008/TASK-008-01.md) after its entry gate check. Stage A claims no maintenance, schedules or release.
- Dossier disposition: EPIC-007 is the sole consumer. At its closeout, promote durable content to canonical owners, replace the
Detailed SOT links in the EPIC-007 file, roadmap and documentation index with Canonical Outcomes links, and delete this file and
its [TODO index](README.md) entry only when EPIC-007 is terminal, no other canonical reference remains, and the approved closeout
envelope explicitly includes the deletion. Deletion is never automatic; until then, link new Task facts rather than copying
them.
