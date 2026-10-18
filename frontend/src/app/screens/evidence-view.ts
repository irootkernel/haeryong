// TASK-003-04 presentation derivation: pure functions over the exact supplied
// metadata bytes and the registry's assessment/coverage view shapes. Nothing
// here performs IO; the SourcePanel owns queries, cursors and selection
// guards, and every displayed count/status derives only from the enumerated
// fixed required-unit records (zero/unknown is never aligned).
import type { ApplicableFreshness, CoverageUnit, ListPage } from "../../client/types.ts";

export interface SuppliedSourceRecord {
  id: string;
  kind?: string;
  retention_mode?: string;
  release_label?: string;
  repo_id?: string;
  object_format?: string;
  exact_revision?: string;
  snapshot_sha256?: string;
  asset_path?: string;
  bytes_sha256?: string;
  [key: string]: unknown;
}

export interface SuppliedEvidenceRecord {
  id: string;
  source_id?: string;
  capture_status?: string;
  path?: string;
  line_start?: number;
  line_end?: number;
  bytes_sha256?: string;
  [key: string]: unknown;
}

export interface SuppliedRegionRecord {
  id: string;
  page_path: string;
  label?: string;
  evidence_ids?: string[];
  [key: string]: unknown;
}

export interface SuppliedMetadata {
  sources: SuppliedSourceRecord[];
  evidence: SuppliedEvidenceRecord[];
  regions: SuppliedRegionRecord[];
  sourceIntent: string | null;
}

const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === "object" && !Array.isArray(value);

/**
 * Defensive parse of the verified supplied-metadata bytes. Malformed optional
 * metadata is an independent metadata error in the panel, never a Reader
 * failure; unknown fields stay preserved for display but never invent
 * evidence. Region identity is page-local by construction (page_path + id),
 * and the author's source intent lives on the version object exactly as the
 * import schema stores it (version.source_intent), never a flattened root.
 */
export function parseSuppliedMetadata(value: unknown): SuppliedMetadata | null {
  if (!record(value)) return null;
  const sources: SuppliedSourceRecord[] = [];
  const evidence: SuppliedEvidenceRecord[] = [];
  const regions: SuppliedRegionRecord[] = [];
  const version = record(value.version) ? value.version : null;
  const sourceIntent = version !== null && typeof version.source_intent === "string" ? version.source_intent : null;
  if (value.sources !== undefined) {
    if (!Array.isArray(value.sources)) return null;
    for (const item of value.sources) {
      if (!record(item) || typeof item.id !== "string" || item.id === "") return null;
      sources.push(item as SuppliedSourceRecord);
    }
  }
  if (value.evidence !== undefined) {
    if (!Array.isArray(value.evidence)) return null;
    for (const item of value.evidence) {
      if (!record(item) || typeof item.id !== "string" || item.id === "") return null;
      evidence.push(item as SuppliedEvidenceRecord);
    }
  }
  if (value.regions !== undefined) {
    if (!Array.isArray(value.regions)) return null;
    for (const item of value.regions) {
      if (!record(item) || typeof item.id !== "string" || item.id === "" || typeof item.page_path !== "string" || item.page_path === "") return null;
      regions.push(item as SuppliedRegionRecord);
    }
  }
  return { sources, evidence, regions, sourceIntent };
}

export function sha256Hex(bytes: Uint8Array): Promise<string> {
  return crypto.subtle.digest("SHA-256", bytes as BufferSource).then((digest) => [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, "0")).join(""));
}

/** Accumulate one stream to a clean EOF; a stale selection aborts the read. */
export async function readStream(body: ReadableStream<Uint8Array>, stale: () => boolean): Promise<Uint8Array> {
  const reader = body.getReader();
  const chunks: Uint8Array[] = [];
  for (;;) {
    const chunk = await reader.read();
    if (stale()) { await reader.cancel().catch(() => undefined); throw new Error("이전 선택의 응답입니다."); }
    if (chunk.done) break;
    chunks.push(chunk.value);
  }
  const total = chunks.reduce((sum, part) => sum + part.length, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const part of chunks) { out.set(part, offset); offset += part.length; }
  return out;
}

/**
 * Own-property-safe boolean toggle for arbitrary valid nonempty local IDs:
 * inherited object keys (a legitimate evidence id such as "constructor" or
 * "toString") must read as absent, never as already-true.
 */
export function toggledOpen(map: Record<string, boolean>, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(map, key) ? !map[key] : true;
}

export interface MetadataVerification { ok: boolean; digest: string | null; problems: string[] }

/**
 * Verify the exact captured supplied-metadata bytes against every supplied
 * validator before anything parses them. The strong ETag carries the
 * immutable object digest; the version view's supplied_metadata_sha256 is
 * compared as well when present. With no usable validator at all the bytes
 * are rejected honestly — an independent metadata error, never a silent
 * unverified parse.
 */
export async function verifyMetadataBytes(bytes: Uint8Array, validators: { etag: string | null; expectedSha256: string | null }): Promise<MetadataVerification> {
  const problems: string[] = [];
  const digest = await sha256Hex(bytes).catch(() => null);
  if (digest === null) return { ok: false, digest: null, problems: ["digest-unavailable"] };
  if (validators.etag === null && validators.expectedSha256 === null) {
    return { ok: false, digest, problems: ["no-usable-validator"] };
  }
  if (validators.etag !== null) {
    const match = /^"sha256-([0-9a-f]{64})"$/.exec(validators.etag);
    if (!match) problems.push("etag-format");
    else if (match[1] !== digest) problems.push("etag-mismatch");
  }
  if (validators.expectedSha256 !== null && validators.expectedSha256 !== digest) problems.push("supplied-sha-mismatch");
  return { ok: problems.length === 0, digest, problems };
}

/** Regions of one exact page; same region names on other pages never leak in. */
export function regionsOfPage(metadata: SuppliedMetadata, pagePath: string): SuppliedRegionRecord[] {
  return metadata.regions.filter((region) => region.page_path === pagePath);
}

export interface RegionEvidenceBinding { region: SuppliedRegionRecord; resolved: SuppliedEvidenceRecord[]; unresolved: string[] }

/** Bind a page-local region to stored evidence records; unknown ids stay visibly unresolved. */
export function bindRegionEvidence(metadata: SuppliedMetadata, region: SuppliedRegionRecord): RegionEvidenceBinding {
  const resolved: SuppliedEvidenceRecord[] = [];
  const unresolved: string[] = [];
  for (const id of region.evidence_ids ?? []) {
    const found = metadata.evidence.find((candidate) => candidate.id === id);
    if (found) resolved.push(found); else unresolved.push(id);
  }
  return { region, resolved, unresolved };
}

export interface CoverageSummary {
  eligible: number;
  assessed: number;
  notChecked: number;
  blocked: number;
  partial: number;
  unknownOutcome: number;
  discrepancyUnits: number;
  /** Units recorded against a different captured target than the selected one; they never count as assessed here. */
  foreignTarget: number;
  /** Units outside the selected version/page/region scope; visible but not members of this required set. */
  outOfScope: number;
  /** Invalid required-set records (duplicates across cursors, impossible null/path combinations, mixed or overlapping unit kinds). */
  invalidUnits: number;
  /** Non-null when the enumerated set violates the registry's fixed nonoverlapping required-set shape; nothing is silently deduplicated. */
  aggregationProblem: string | null;
  derivedComplete: boolean;
  aligned: boolean;
}

export interface CoverageScopeFilter {
  kind: "version" | "page" | "region";
  pagePath?: string;
  regionId?: string;
}

export interface CoverageFilter {
  targetSetId: string | null;
  scope?: CoverageScopeFilter;
  /** The selected applicable coverage set's recorded unit_kind; a homogeneous set of another kind is rejected, preserving rows. */
  unitKind?: string;
}

const unitKey = (unit: { kind: string; page_path: string | null; region_id: string | null }): string =>
  `${unit.kind}\u0000${unit.page_path ?? ""}\u0000${unit.region_id ?? ""}`;

/**
 * Counts derive ONLY from the fixed required-unit records. Partial, blocked
 * and not-checked units stay visible with their reasons and can never shrink
 * the denominator; contributions recorded against another captured target or
 * outside the selected scope never count as assessed; and a set that violates
 * the registry's shape — duplicate tuples across cursor pages, impossible
 * null/path combinations, mixed unit kinds, or a page unit overlapping its
 * own region units — stays an inspectable aggregation error: no silent
 * deduplication into a new valid denominator, and zero/unknown eligibility
 * is never aligned. Membership comes only from the enumerated service set,
 * never from DOM structure or partially loaded file lists.
 */
export function deriveCoverageSummary(units: CoverageUnit[], filter?: CoverageFilter): CoverageSummary {
  let assessed = 0, notChecked = 0, blocked = 0, partial = 0, unknownOutcome = 0, discrepancyUnits = 0, foreignTarget = 0, outOfScope = 0, invalidUnits = 0;
  const problems: string[] = [];
  const seen = new Set<string>();
  // An absent scope consistently means the VERSION scope (matching
  // deriveStatus): admission accepts only distinct HTML page units or one
  // whole-version unit, so a version selection can never admit region
  // coverage and align from it.
  const scope = filter?.scope ?? { kind: "version" as const };
  const pageUnits = new Set<string>();
  const regionUnitsByPage = new Map<string, number>();
  const kinds = new Set<string>();
  for (const unit of units) {
    // Admission first: shape, duplicates, foreign target and scope/kind
    // membership decide whether a row may drive THIS selection at all. A
    // discrepancy recorded on an invalid, foreign or out-of-scope row stays
    // visible on that row but never relabels the selected scope.
    const shapeValid = unit.kind === "version" ? unit.page_path === null && unit.region_id === null
      : unit.kind === "page" ? unit.page_path !== null && unit.region_id === null
        : unit.kind === "region" ? unit.page_path !== null && unit.region_id !== null
          : false;
    if (!shapeValid) { invalidUnits++; if (!problems.includes("invalid-unit-shape")) problems.push("invalid-unit-shape"); continue; }
    const key = unitKey(unit);
    if (seen.has(key)) { invalidUnits++; if (!problems.includes("duplicate-unit-tuple")) problems.push("duplicate-unit-tuple"); continue; }
    seen.add(key);
    if (filter?.targetSetId != null && unit.target_set_id !== null && unit.target_set_id !== filter.targetSetId) { foreignTarget++; continue; }
    {
      const pageMatches = scope.pagePath !== undefined ? unit.page_path === (scope.pagePath ?? null) : true;
      const allowed = scope.kind === "version"
        ? unit.kind === "page" || unit.kind === "version"
        : scope.kind === "page"
          ? pageMatches && (unit.kind === "page" || unit.kind === "region")
          : unit.kind === "region" && pageMatches && unit.region_id === (scope.regionId ?? null);
      if (!allowed) { outOfScope++; continue; }
    }
    kinds.add(unit.kind);
    if (filter?.unitKind !== undefined && unit.kind !== filter.unitKind) {
      // The selected coverage set records its unit kind; a homogeneous set of
      // another kind is rejected as an aggregation error. The row stays
      // visible but is never admitted: it cannot drive this selection's
      // counts, discrepancy or status.
      invalidUnits++;
      if (!problems.includes("unit-kind-mismatch")) problems.push("unit-kind-mismatch");
      continue;
    }
    if (unit.kind === "page" && unit.page_path !== null) pageUnits.add(unit.page_path);
    if (unit.kind === "region" && unit.page_path !== null) regionUnitsByPage.set(unit.page_path, (regionUnitsByPage.get(unit.page_path) ?? 0) + 1);
    // Contribution linkage: only a unit linked to a matching recorded
    // contribution (non-null assessment_id AND target_set_id) can drive
    // assessed/blocked/partial/discrepancy. A null-link row with outcome
    // not_checked is the honest unassessed placeholder; any other outcome or
    // an evidenced discrepancy on a null-link row is a missing/contradictory
    // linkage — an aggregation problem that never silently repairs the
    // record, derives aligned or drives outdated. The row stays eligible and
    // inspectable.
    if (unit.assessment_id === null || unit.target_set_id === null) {
      if (unit.outcome === "not_checked") {
        notChecked++;
      } else {
        invalidUnits++;
        if (!problems.includes("missing-contribution-link")) problems.push("missing-contribution-link");
      }
      continue;
    }
    // Only admitted, linked rows drive status: an evidenced discrepancy is
    // true or a non-empty recorded value; an explicit false is NOT one, and
    // an unrecognized representation stays unknown instead of inventing
    // truth.
    const discrepancy = unit.discrepancy;
    if (discrepancy === null || discrepancy === undefined || discrepancy === false) {
      // no evidenced discrepancy on this unit
    } else if (discrepancy === true || (typeof discrepancy === "string" && discrepancy !== "")) {
      discrepancyUnits++;
    } else {
      unknownOutcome++;
    }
    switch (unit.outcome) {
      case "assessed": assessed++; break;
      case "not_checked": notChecked++; break;
      case "blocked": blocked++; break;
      case "partial": partial++; break;
      default: unknownOutcome++;
    }
  }
  if (kinds.size > 1 && !problems.includes("mixed-unit-kinds")) problems.push("mixed-unit-kinds");
  for (const page of pageUnits) {
    if ((regionUnitsByPage.get(page) ?? 0) > 0 && !problems.includes("overlapping-page-region-aggregation")) problems.push("overlapping-page-region-aggregation");
  }
  const eligible = units.length;
  const aggregationProblem = problems.length > 0 ? problems.join(",") : null;
  const clean = eligible > 0 && unknownOutcome === 0 && blocked === 0 && partial === 0 && notChecked === 0 && foreignTarget === 0 && outOfScope === 0 && invalidUnits === 0 && aggregationProblem === null;
  return {
    eligible, assessed, notChecked, blocked, partial, unknownOutcome, discrepancyUnits, foreignTarget, outOfScope, invalidUnits,
    aggregationProblem,
    derivedComplete: clean,
    aligned: clean && assessed === eligible && discrepancyUnits === 0,
  };
}

export type DerivedStatus = "aligned_within_scope" | "outdated_against_target" | "reassessment_needed" | "unable_to_verify" | "not_assessed";

export interface StatusTarget {
  /** The selected applicable freshness view: exact target availability and the reassessment flag come from it. */
  overlay: ApplicableFreshness | null;
  targetSetId: string | null;
  scope?: CoverageScopeFilter;
}

/**
 * Registry aggregation over the enumerated units with the selected target's
 * own facts: an evidenced discrepancy outranks everything; an outstanding
 * reassessment trigger outranks blocked units; a recorded overlay
 * unable_to_verify keeps verification unavailable even when every unit is
 * assessed — completed coverage plus verification unavailable, never derived
 * aligned. Alignment additionally requires an available exact target
 * (target_set_id/captured_at actually recorded), so an all-assessed unit list
 * alone never becomes aligned for a missing or unavailable target; capture
 * ID/time alone is not current source availability. An absent/unknown overlay
 * stays unknown (not_assessed), never "unavailable". The overlay must itself
 * describe the SELECTED scope (kind plus exact page/region; an undefined
 * selection is the version scope): an incompatible overlay cannot supply the
 * selected status, reassessment or availability flags — it remains separately
 * recorded and history-inspectable.
 */
export function deriveStatus(summary: CoverageSummary, target: StatusTarget): DerivedStatus {
  if (summary.eligible === 0) return "not_assessed";
  if (summary.discrepancyUnits > 0) return "outdated_against_target";
  const overlayCompatible = target.overlay !== null && scopeMatchesSelection(target.overlay.scope, target.scope);
  if (overlayCompatible && target.overlay !== null && target.overlay.reassessment_required) return "reassessment_needed";
  if (overlayCompatible && target.overlay !== null && target.overlay.status === "unable_to_verify") return "unable_to_verify";
  if (summary.blocked > 0) return "unable_to_verify";
  const targetAvailable = overlayCompatible && target.overlay !== null && target.overlay.target_set_id !== null && target.overlay.captured_at !== null && (target.targetSetId === null || target.overlay.target_set_id === target.targetSetId);
  // An explicitly positive applicable assessment is required: an
  // unknown/not_assessed overlay never upgrades to aligned merely because an
  // ID/time exists and every enumerated unit is assessed. Historical
  // assessments and current availability stay distinct.
  const positivelyAssessed = overlayCompatible && target.overlay !== null && target.overlay.status === "aligned_within_scope";
  if (summary.aligned && targetAvailable && positivelyAssessed) return "aligned_within_scope";
  return "not_assessed";
}

function scopeMatchesSelection(overlayScope: ApplicableFreshness["scope"], selection: CoverageScopeFilter | undefined): boolean {
  if (selection === undefined) return overlayScope.kind === "version" && overlayScope.page_path === null && overlayScope.region_id === null;
  if (overlayScope.kind !== selection.kind) return false;
  if (selection.pagePath !== undefined && overlayScope.page_path !== selection.pagePath) return false;
  if (selection.regionId !== undefined && overlayScope.region_id !== selection.regionId) return false;
  if (selection.kind === "page" && (selection.pagePath === undefined || selection.regionId !== undefined)) return false;
  if (selection.kind === "region" && (selection.pagePath === undefined || selection.regionId === undefined)) return false;
  return true;
}

export { scopeMatchesSelection };

export interface OverlayComparison {
  derived: DerivedStatus;
  overlayStatus: string | null;
  overlayComplete: boolean | null;
  conflict: boolean;
}

/**
 * The recorded overlay badge is displayed but never trusted: a conflict with
 * the unit-derived status is surfaced instead of turning zero/unknown or
 * partial coverage aligned.
 */
export function compareWithOverlay(summary: CoverageSummary, overlay: ApplicableFreshness | null, targetSetId: string | null = null, scope?: CoverageScopeFilter): OverlayComparison {
  const derived = deriveStatus(summary, { overlay, targetSetId, scope });
  if (overlay === null) return { derived, overlayStatus: null, overlayComplete: null, conflict: false };
  const overlayAligned = overlay.status === "aligned_within_scope" && overlay.coverage.complete;
  const derivedAligned = derived === "aligned_within_scope";
  return { derived, overlayStatus: overlay.status, overlayComplete: overlay.coverage.complete, conflict: overlayAligned !== derivedAligned };
}

/**
 * units_url is validated as DATA, never fetched as a URL: the request is
 * rebuilt from typed fields only when it names the exact fixed local service
 * route for this version and coverage set. Foreign absolute origins (and
 * protocol-relative references) are rejected; an absolute URL is accepted
 * only when its origin equals the current service origin.
 */
export function coverageUnitsParams(unitsUrl: string | null, versionId: string, coverageSetId: string | null, origin: string | null): { coverageSetId: string } | null {
  if (typeof unitsUrl !== "string" || unitsUrl === "") return null;
  let path: string, search: string;
  if (unitsUrl.startsWith("/")) {
    if (unitsUrl.startsWith("//")) return null;
    const clean = unitsUrl.indexOf("#") === -1 ? unitsUrl : unitsUrl.slice(0, unitsUrl.indexOf("#"));
    const queryAt = clean.indexOf("?");
    path = queryAt === -1 ? clean : clean.slice(0, queryAt);
    search = queryAt === -1 ? "" : clean.slice(queryAt + 1);
  } else {
    let parsed: URL;
    try { parsed = new URL(unitsUrl); } catch { return null; }
    if (origin === null || parsed.origin !== origin) return null;
    path = parsed.pathname;
    search = parsed.search.startsWith("?") ? parsed.search.slice(1) : parsed.search;
  }
  if (path !== `/api/v1/versions/${versionId}/assessments/coverage`) return null;
  const params = new URLSearchParams(search);
  const ids = params.getAll("coverage_set_id");
  const extraKeys = [...params.keys()].filter((key) => key !== "coverage_set_id");
  if (ids.length !== 1 || ids[0] === "" || extraKeys.length > 0) return null;
  if (coverageSetId !== null && ids[0] !== coverageSetId) return null;
  return { coverageSetId: ids[0] };
}

export interface SelectionKey {
  versionId: string;
  pagePath: string | null;
  regionId: string | null;
  generation: number;
}

/** Shared supplied-metadata load state owned by the Reader (version-scoped). */
export type MetadataState =
  | { kind: "loading" }
  | { kind: "absent" }
  | { kind: "ready"; metadata: SuppliedMetadata }
  | { kind: "error"; problem: string };

/**
 * A region selection event carries the channel's CAPTURED version/page
 * identity beside the region id. The host accepts it only when the captured
 * tuple still matches the current reader selection, so late or cross-page
 * events (including a same-name region from another page) never depend on
 * React commit ordering or current callback props.
 */
export interface RegionSelectionEvent {
  versionId: string;
  pagePath: string;
  regionId: string;
}

export function acceptRegionSelection(event: RegionSelectionEvent, current: { versionId: string; pagePath: string | null }): boolean {
  if (event.versionId !== current.versionId) return false;
  if (current.pagePath === null || event.pagePath !== current.pagePath) return false;
  return event.regionId !== "";
}

/**
 * Render-time region ownership: a stored region selection belongs to the
 * exact [version,page,host-context,reload] identity it was captured under.
 * Any mismatch — a new page or version, a new host context, or an explicit
 * same-context reload — renders as unselected; the stored record is never
 * retagged onto the new selection.
 */
export interface RegionOwnership {
  versionId: string;
  pagePath: string | null;
  context: string | null;
  reload: number;
}

export function regionSelectionMatches(
  selection: { versionId: string; pagePath: string; context: string | null; reload: number; regionId: string },
  current: RegionOwnership,
): boolean {
  if (selection.versionId !== current.versionId) return false;
  if (current.pagePath === null || selection.pagePath !== current.pagePath) return false;
  if (selection.context !== current.context) return false;
  return selection.reload === current.reload;
}

export function sameSelection(a: SelectionKey, b: SelectionKey): boolean {
  return a.versionId === b.versionId && a.pagePath === b.pagePath && a.regionId === b.regionId && a.generation === b.generation;
}

/**
 * Selection-specific query identity: every page/version/render-context change
 * bumps the generation, so a late success/error/retry from an earlier
 * selection (including a same-name region on another page) can never label
 * the current one. Per-resource sequence numbers additionally reject
 * order-reversed responses within one selection.
 */
export class SelectionQueryGuard {
  #generation = 0;
  #sequences = new Map<string, number>();
  readonly key: SelectionKey;

  constructor(versionId: string, pagePath: string | null, regionId: string | null) {
    this.key = { versionId, pagePath, regionId, generation: this.#generation };
  }

  /** Clear/rebind on page/version/render/history-context changes. Returns the new key when the selection changed. */
  rebind(versionId: string, pagePath: string | null, regionId: string | null): SelectionKey | null {
    if (this.key.versionId === versionId && this.key.pagePath === pagePath && this.key.regionId === regionId) return null;
    this.#generation++;
    this.#sequences.clear();
    this.key.versionId = versionId;
    this.key.pagePath = pagePath;
    this.key.regionId = regionId;
    this.key.generation = this.#generation;
    return this.key;
  }

  /** Bump the generation without changing the page (explicit region reselection). */
  bump(): SelectionKey {
    this.#generation++;
    this.#sequences.clear();
    this.key.generation = this.#generation;
    return this.key;
  }

  /** Issue a request under the current selection; the returned token must be presented to accept(). */
  begin(resource: string): { key: SelectionKey; seq: number } {
    const seq = (this.#sequences.get(resource) ?? 0) + 1;
    this.#sequences.set(resource, seq);
    return { key: { ...this.key }, seq };
  }

  /** A response applies only to the exact selection and only when no newer request of its resource exists. */
  accept(resource: string, token: { key: SelectionKey; seq: number }): boolean {
    if (!sameSelection(token.key, this.key)) return false;
    return token.seq === this.#sequences.get(resource);
  }
}

export interface EnumerationState<T> {
  items: T[];
  nextCursor: string | null;
  failed: boolean;
  /** Cursor already served before the failure; retry resumes exactly there. */
  resumeFrom: string | null;
}

export function initialEnumeration<T>(): EnumerationState<T> {
  return { items: [], nextCursor: null, failed: false, resumeFrom: null };
}

/** Merge one served cursor page; failure keeps the accumulated items and its resume cursor. */
export function mergeEnumerationPage<T>(state: EnumerationState<T>, page: ListPage<T> | null): EnumerationState<T> {
  if (page === null) return { ...state, failed: true, resumeFrom: state.nextCursor };
  return { items: [...state.items, ...page.items], nextCursor: page.next_cursor, failed: false, resumeFrom: null };
}

/**
 * Counts/status derive only after the complete fixed enumeration; while a
 * cursor remains (or a page failed) everything stays honestly
 * unknown/incomplete with no denominator claims.
 */
export function enumerationComplete<T>(state: EnumerationState<T>): boolean {
  return !state.failed && state.nextCursor === null;
}
