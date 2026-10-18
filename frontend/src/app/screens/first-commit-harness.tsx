// Labelled FIRST-COMMIT verification harness (TASK-003-04 R9). This is a
// verification entry point, NOT a product navigation flow: it mounts the
// ACTUAL ReaderScreen and SourcePanel with a minimally scripted client whose
// deterministic seeded markers distinguish metadata provenance from service
// query history/overlay, under a parent CommitProbe that snapshots the DOM
// inside a public useLayoutEffect — i.e. on the FIRST commit of a new prop
// selection, strictly BEFORE any child passive effect can reset stale state.
// Selected ONLY by the harness URL parameter ?mode=committest (see
// harness-main.tsx); the default preselection harness mode is unchanged.
// The scripted client is a labelled seeded presentation fixture: this page
// proves React commit-time identity behavior only and is NEVER a real
// source observation or verification claim.
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type { ShellController } from "../../client/controller.ts";
import { ShellController as RealShellController } from "../../client/controller.ts";
import { HttpHaeryongClient } from "../../client/http.ts";
import { BrowserCredentialStore } from "../../client/store.ts";
import type { Capabilities, DocumentView, FileEntryView, HaeryongClient, ListPage, PersonalView, ProjectView, RenderBootstrapView, SessionInfo, SuppliedMetadataStream, VersionView } from "../../client/types.ts";
import { useShellRoute } from "../router.tsx";
import { App } from "../shell.tsx";
import { ReaderScreen } from "./reader.tsx";
import { SourcePanel } from "./source-panel.tsx";
import type { MetadataState, SuppliedMetadata } from "./evidence-view.ts";
import "../theme.css";

const encoder = new TextEncoder();
const sha256Hex = async (bytes: Uint8Array): Promise<string> => {
  const digest = await crypto.subtle.digest("SHA-256", bytes as BufferSource);
  return [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
};
const hex = (length: number, seed: string): string => {
  let value = "";
  let index = 0;
  while (value.length < length) {
    value += (seed.charCodeAt(index % seed.length) % 16).toString(16);
    index += 1;
  }
  return value;
};

const documentId = "doc-1";
const projectId = "prj-1";

interface ScriptedVersion {
  id: string;
  sequence: number;
  marker: string;
  metadataJson: string;
}

const versions: ScriptedVersion[] = [
  { id: "ver-A", sequence: 1, marker: "MARK-META-A", metadataJson: "" },
  { id: "ver-B", sequence: 2, marker: "MARK-META-B", metadataJson: "" },
];
for (const version of versions) {
  version.metadataJson = JSON.stringify({
    version: { source_intent: "historical" },
    sources: [{ id: `src-${version.id}`, kind: "git", retention_mode: "reference_only", repo_id: "11111111-2222-4333-8444-555555555555", object_format: "sha1", exact_revision: hex(40, version.id), release_label: version.marker }],
    evidence: [{ id: `evd-${version.id}`, source_id: `src-${version.id}`, capture_status: "reference_only", path: `notes/${version.id}.txt` }],
    regions: [
      { id: "overview", page_path: "first.html", label: `${version.id}-첫개요`, evidence_ids: [`evd-${version.id}`] },
      { id: "overview2", page_path: "nested/second.html", label: `${version.id}-둘개요`, evidence_ids: [`evd-${version.id}`] },
    ],
  });
}

const panelMetadataObject = JSON.parse(versions[0].metadataJson) as SuppliedMetadata;

// Seeded service-query markers: per (version, page) for the reader panel and
// per region for the directly controlled SourcePanel. Only history ids and
// applicable target sets are marker-bearing; no real assessment exists.
const queryMarker = (versionId: string, pagePath: string): { target: string; history: string } => ({
  target: `tset${versionId === "ver-A" ? "A" : "B"}${pagePath === "first.html" ? "1" : "2"}`,
  history: `AQMARK${versionId === "ver-A" ? "A" : "B"}${pagePath === "first.html" ? "1" : "2"}`,
});
const regionMarker = (regionId: string): { target: string; history: string } => ({ target: `tset${regionId.toUpperCase()}`, history: `AQMARK${regionId.toUpperCase()}` });

const applicableFor = (target: string, scope: { kind: string; page_path: string | null; region_id: string | null }) => ({
  source_intent: "historical", target_set_id: target, captured_at: "2026-10-19T00:00:00Z", scope, status: "aligned_within_scope",
  coverage: { coverage_set_id: null, unit_kind: "page", eligible: 0, assessed: 0, complete: false, units_url: null },
  assessed_at: null, reassessment_required: false,
});

const notExists: PersonalView = { exists: false, value: null, revision: null, etag: null, required_precondition: "if_none_match_star", page_position_url: null };

const session: SessionInfo = { session_id: "s-fx", principal_id: "p-fx", library_id: "lib-fx", device_label: "fx", origin: "about:blank", state: "active", created_at: "2026-10-19T00:00:00Z", last_seen_at: "2026-10-19T00:00:00Z", expires_at: null, revision: 1, etag: '"s"', scope: { kind: "owner" } };
const capabilities: Capabilities = { library_id: "lib-fx", api_version: "1.0", runtime_generation: "g-fx", stream_epoch: "e-fx", unfiled_project_id: "prj-0", features: {} };
const project: ProjectView = { project_id: projectId, name: "fx", lifecycle: "active", revision: 1, permissions_revision: 1, updated_at: "2026-10-19T00:00:00Z", etag: '"p"' };
const fixtureDocument: DocumentView = { document_id: documentId, project_id: projectId, title: "fx-doc", lifecycle: "active", latest_version_id: versions[1].id, revision: 1, updated_at: "2026-10-19T00:00:00Z", versions_url: "/v", etag: '"d"' };

const digests = new Map<string, string>();
const fileEntries: FileEntryView[] = [
  { path: "first.html", kind: "file", byte_size: "1", preview: "download", file_url: "/f1", etag: '"f1"' },
  { path: "nested/second.html", kind: "file", byte_size: "1", preview: "download", file_url: "/f2", etag: '"f2"' },
];

const scriptedClient = {
  publicOrigin: null,
  async versionDetail(versionId: string): Promise<VersionView> {
    const version = versions.find((candidate) => candidate.id === versionId);
    if (!version) throw new Error("unknown fixture version");
    return { version_id: version.id, document_id: documentId, sequence: version.sequence, base_version_id: version.sequence === 1 ? null : versions[0].id, title_snapshot: `fx-${version.id}`, default_page: "first.html", viewer_profile: "html_js_v1", source_basis: "evidence_recorded", payload_sha256: hex(64, "payload" + version.id), manifest_sha256: hex(64, "manifest" + version.id), supplied_metadata_sha256: digests.get(version.id) ?? null, manifest_url: "/m", supplied_metadata_url: "/sm", files_url: "/files", render_bootstrap_url: "/rb", etag: '"v"' };
  },
  async documentDetail(): Promise<DocumentView> { return fixtureDocument; },
  async listProjects(): Promise<ListPage<ProjectView>> { return { items: [project], next_cursor: null }; },
  async versionFiles(): Promise<ListPage<FileEntryView>> { return { items: fileEntries, next_cursor: null }; },
  async renderBootstrap(versionId: string, pagePath?: string): Promise<RenderBootstrapView> {
    return { version_id: versionId, viewer_profile: "html_js_v1", default_page: "first.html", selected_page: pagePath ?? "first.html", requested_page: pagePath ?? null, navigation_path: null, files_url: "/files", selected_file_url: null, delivery: "authenticated_attachment", renderer_available: false, render_sessions_url: "/rs", renderer_construction: "fx-stub" };
  },
  async versionSuppliedMetadata(versionId: string): Promise<SuppliedMetadataStream> {
    const version = versions.find((candidate) => candidate.id === versionId);
    if (!version) throw new Error("unknown fixture version");
    const bytes = encoder.encode(version.metadataJson);
    const digest = digests.get(versionId) ?? await sha256Hex(bytes);
    const body = new ReadableStream<Uint8Array>({ start(controller) { controller.enqueue(bytes); controller.close(); } });
    return { etag: `"sha256-${digest}"`, stream: { body, cancel() {} } };
  },
  async versionAssessments(versionId: string, filter?: { scope?: { kind: "version" | "page" | "region"; pagePath?: string; regionId?: string } }) {
    const scope = filter?.scope;
    if (scope?.kind === "region" && scope.pagePath && scope.regionId) {
      const marker = regionMarker(scope.regionId);
      return { items: [{ assessment_id: `${marker.history}`, version_id: versionId, scope: { kind: "region", page_path: scope.pagePath, region_id: scope.regionId }, target_set_id: marker.target, state: "published" }], next_cursor: null, applicable: applicableFor(marker.target, { kind: "region", page_path: scope.pagePath, region_id: scope.regionId }) };
    }
    const pagePath = scope?.kind === "page" && scope.pagePath ? scope.pagePath : "first.html";
    const marker = queryMarker(versionId, pagePath);
    return { items: [{ assessment_id: `${marker.history}`, version_id: versionId, scope: { kind: "page", page_path: pagePath, region_id: null }, target_set_id: marker.target, state: "published" }], next_cursor: null, applicable: applicableFor(marker.target, { kind: "page", page_path: pagePath, region_id: null }) };
  },
  async evidenceVerifications(): Promise<never> { throw new Error("unavailable in fixture"); },
  async assessmentCoverage(): Promise<never> { throw new Error("unavailable in fixture"); },
  async personalGet(): Promise<PersonalView> { return notExists; },
  async personalPut(_kind: unknown, _versionId: unknown, _page: unknown, body: unknown): Promise<PersonalView> { return { exists: true, value: body, revision: 1, etag: '"e1"', page_position_url: null }; },
} as unknown as HaeryongClient;

const fixtureController = {
  client: scriptedClient,
  state: { phase: "verified", capabilities, session },
  captureDispatchAuthority: () => ({ nonce: 1 }),
  requireVerifiedDispatch() {},
} as unknown as ShellController;

const panelMetadataState: MetadataState = { kind: "ready", metadata: panelMetadataObject };

type CommitSnapshot = { tag: string; reader: string; panel: string };

export function FirstCommitHarness() {
  // The Reader's host-context gate reads history.state before any effect:
  // stamp one stable logical context exactly once for this harness page.
  if (!history.state || typeof (history.state as { haeryongReaderContext?: unknown }).haeryongReaderContext !== "string") {
    history.replaceState({ haeryongReaderContext: "fx-1" }, "", location.href);
  }
  const armed = useRef<string | null>(null);
  const [selection, setSelection] = useState({ versionId: versions[0].id, page: "first.html" });
  const [panelSelection, setPanelSelection] = useState({ regionId: "r1", contextKey: "ctx-1" });
  const globalWindow = window as unknown as {
    __commitLog?: CommitSnapshot[];
    __armCommit?(tag: string): void;
    __select?(next: { versionId?: string; page?: string }): void;
    __selectPanel?(next: { regionId?: string; contextKey?: string }): void;
  };
  globalWindow.__commitLog = globalWindow.__commitLog ?? [];
  globalWindow.__armCommit = (tag: string) => { armed.current = tag; };
  globalWindow.__select = (next) => { setSelection((old) => ({ ...old, ...next })); };
  globalWindow.__selectPanel = (next) => { setPanelSelection((old) => ({ ...old, ...next })); };
  // CommitProbe: a public useLayoutEffect on the parent fires synchronously
  // after THIS commit's DOM mutations and strictly before any child passive
  // effect could reset stale state — the deterministic first-commit oracle.
  useLayoutEffect(() => {
    const tag = armed.current;
    if (tag === null) return;
    armed.current = null;
    (window as unknown as { __commitLog: CommitSnapshot[] }).__commitLog.push({
      tag,
      reader: document.getElementById("reader-mount")?.textContent ?? "",
      panel: document.getElementById("panel-mount")?.textContent ?? "",
    });
  });
  return <div>
    <div id="reader-mount"><ReaderScreen controller={fixtureController} documentId={documentId} versionId={selection.versionId} page={selection.page} pageQuery="" pageFragment="" /></div>
    <div id="panel-mount"><SourcePanel controller={fixtureController} version={null} versionId={versions[0].id} pagePath="first.html" regionId={panelSelection.regionId} contextKey={panelSelection.contextKey} metadata={panelMetadataState} /></div>
  </div>;
}

type RealCommitSnapshot = { tag: string; dom: string; panelH2: string };

/**
 * Labelled REAL caller-identity harness (TASK-003-04 R10): the ACTUAL
 * production App with a REAL HttpHaeryongClient/ShellController/Browser
 * credential store, driven through the actual router/sidebar/navigation of
 * the committed Reader. This is a fixture-only App wrapper — it creates no
 * product route — and its proofs are labelled REAL caller-identity checks,
 * never native/source verification. The branch is chosen once from the
 * harness mode parameter and the URL is rewritten to the exact real Reader
 * route BEFORE the App mounts, so the App's own router owns navigation.
 */
export function RealCallerHarness(props: { documentId: string; versionId: string }) {
  const [controller] = useState(() => new RealShellController(new HttpHaeryongClient(), new BrowserCredentialStore()));
  const armed = useRef<string | null>(null);
  // The parent subscribes to the SAME public route hook the App uses: an
  // actual popstate re-renders this parent in the same commit as the child
  // App, so the layout probe below observes the child's FIRST commit DOM
  // for the new route strictly before any child passive effect runs.
  useShellRoute();
  useLayoutEffect(() => {
    const tag = armed.current;
    if (tag === null) return;
    armed.current = null;
    (window as unknown as { __realCommitLog: RealCommitSnapshot[] }).__realCommitLog.push({
      tag,
      dom: document.getElementById("real-app-mount")?.textContent ?? "",
      panelH2: document.querySelector('section[aria-label="근거 패널"] h2')?.textContent ?? "",
    });
  });
  useEffect(() => {
    controller.start();
    void controller.verify();
    return () => { controller.stop(); };
  }, [controller]);
  const globalWindow = window as unknown as { __realCommitLog?: RealCommitSnapshot[]; __armReal?(tag: string): void };
  globalWindow.__realCommitLog = globalWindow.__realCommitLog ?? [];
  globalWindow.__armReal = (tag: string) => { armed.current = tag; };
  // Rewrite once to the exact real Reader route BEFORE the App mounts so its
  // router resolves the Reader screen natively (page preselected).
  const stamped = useRef(false);
  if (!stamped.current) {
    stamped.current = true;
    const query = new URLSearchParams({ page: "first.html" });
    history.replaceState(null, "", `/documents/${encodeURIComponent(props.documentId)}/versions/${encodeURIComponent(props.versionId)}?${query}`);
  }
  return <div id="real-app-mount"><App controller={controller} /></div>;
}
