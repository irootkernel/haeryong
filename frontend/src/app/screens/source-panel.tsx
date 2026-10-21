// SourcePanel (Evidence context tab): presents authored evidence, its separate
// service verification/history and assessment coverage for the exact
// version/page/region selection. All enumeration, retry and selection-guard
// behavior lives in SourceQueries; counts and status derive only from fully
// enumerated fixed required-unit records plus the selected target's own
// facts, the recorded overlay badge is shown but never trusted, unavailable
// backend capabilities stay honest 503 states, and the source-check action
// is guarded by the verified authority and source_verification capability. No display upgrades the
// author's capture_status or claims observed provider input, author-byte
// agreement or correctness.
import { useEffect, useRef, useState } from "react";
import type { ShellController } from "../../client/controller.ts";
import { featureEnabled } from "./library-data.ts";
import { evidenceAvailability, evidenceExcerpt, coverageReason } from "./source-presentation.ts";
import type { VerificationAction } from "./source-queries.ts";
import type { VersionView } from "../../client/types.ts";
import { bindRegionEvidence, compareWithOverlay, deriveCoverageSummary, regionsOfPage, toggledOpen, type MetadataState, type SuppliedEvidenceRecord, type SuppliedMetadata, type SuppliedSourceRecord } from "./evidence-view.ts";
import { SourceQueries, type SourceQuerySnapshot } from "./source-queries.ts";

interface SourcePanelProps {
  controller: ShellController;
  version: VersionView | null;
  versionId: string;
  pagePath: string | null;
  regionId: string | null;
  /** Same-page history/render/reload context: a change clears and rebinds every query. */
  contextKey: string;
  /** Version-scoped supplied-metadata load owned by the Reader header area. */
  metadata: MetadataState;
}

const captureLabel = (value: unknown): string => {
  switch (value) {
    case "verified": return "verified(작성자 선언)";
    case "reference_only": return "reference_only(작성자 선언)";
    case "unavailable": return "unavailable(작성자 선언)";
    default: return "선언 없음";
  }
};

const outcomeLabel = (outcome: string): string => {
  switch (outcome) {
    case "captured": return "캡처됨(기대 비교 없음 — 작성자 바이트 일치 아님)";
    case "matched": return "일치(기대 비교 — 사후 검증)";
    case "mismatch": return "불일치";
    case "unavailable": return "사용 불가";
    default: return `미정(원본 표기: ${outcome})`;
  }
};

const unitOutcomeLabel = (outcome: string): string => {
  switch (outcome) {
    case "assessed": return "평가됨";
    case "partial": return "부분";
    case "blocked": return "차단";
    case "not_checked": return "미확인";
    default: return `미정(원본 표기: ${outcome})`;
  }
};

const statusLabel = (status: string): string => {
  switch (status) {
    case "aligned_within_scope": return "범위 내 정렬";
    case "outdated_against_target": return "대비 구식";
    case "reassessment_needed": return "재평가 필요";
    case "unable_to_verify": return "확인 불가";
    case "not_assessed": return "미평가";
    default: return status;
  }
};

const scopeText = (scope: { kind: string; page_path: string | null; region_id: string | null }): string =>
  scope.kind === "version" ? "버전 전체" : scope.kind === "page" ? `페이지 ${scope.page_path ?? ""}` : `영역 ${scope.region_id ?? ""}(${scope.page_path ?? ""})`;

export function SourcePanel(props: SourcePanelProps) {
  // Keyed INNER selection boundary: every caller receives a fresh local
  // snapshot, disclosure state and SourceQueries instance on the FIRST commit
  // of a new [version,page,region,context] selection — the JSON array form
  // preserves arbitrary Unicode IDs unambiguously. All local state lives
  // inside the keyed inner component; nothing outside the panel resets.
  return <SourcePanelSelection key={JSON.stringify([props.versionId, props.pagePath, props.regionId, props.contextKey])} {...props} />;
}

function SourcePanelSelection(props: SourcePanelProps) {
  const { controller, versionId, pagePath, regionId, contextKey, metadata } = props;
  const queriesRef = useRef<SourceQueries | null>(null);
  if (queriesRef.current === null) queriesRef.current = new SourceQueries(controller.client, versionId, pagePath, regionId, contextKey, controller.sourceWriteIntents());
  const queries = queriesRef.current;
  const [snap, setSnap] = useState<SourceQuerySnapshot>(() => queries.snapshot());
  const [actionError, setActionError] = useState<string | null>(null);
  const [unitsOpen, setUnitsOpen] = useState(false);
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [verificationsOpen, setVerificationsOpen] = useState<Record<string, boolean>>({});

  useEffect(() => {
    const notify = () => setSnap(queries.snapshot());
    const unsubscribe = queries.subscribe(notify);
    notify();
    return () => { unsubscribe(); };
  }, [queries]);

  useEffect(() => () => queries.dispose(), [queries]);

  // Clear/rebind on version/page/region changes AND same-page
  // history/render/reload context changes; then (re)start the enumeration.
  useEffect(() => {
    if (queries.rebind(versionId, pagePath, regionId, contextKey) || snap.assessments.phase === "idle") {
      setActionError(null);
      setUnitsOpen(false);
      setExpanded({});
      setVerificationsOpen({});
      void queries.loadAssessments();
    }
  }, [versionId, pagePath, regionId, contextKey, queries, snap.assessments.phase]);

  const toggleItem = (evidenceId: string) => {
    setExpanded((old) => ({ ...old, [evidenceId]: toggledOpen(old, evidenceId) }));
    void queries.loadEvidence(evidenceId);
  };
  const toggleVerifications = (evidenceId: string) => {
    setVerificationsOpen((old) => ({ ...old, [evidenceId]: toggledOpen(old, evidenceId) }));
    void queries.loadVerifications(evidenceId);
  };

  const enabled = featureEnabled(controller.state.capabilities?.features, "source_verification");
  const verify = (evidence: SuppliedEvidenceRecord, compareDigest: boolean) => {
    setActionError(null);
    const request = compareDigest && typeof evidence.bytes_sha256 === "string" ? { expected_bytes_sha256: evidence.bytes_sha256 } : {};
    void queries.runVerification(evidence.id, request, controller).catch((error) => setActionError(error instanceof Error ? error.message : String(error)));
  };
  const applicable = snap.assessments.applicable;
  const coverageSummary = snap.coverage.phase === "ready" && snap.coverage.units.complete
    ? deriveCoverageSummary(snap.coverage.units.items, { targetSetId: applicable?.target_set_id ?? null, scope: queries.scope(), unitKind: applicable?.coverage.unit_kind })
    : null;
  const overlay = coverageSummary === null ? null : compareWithOverlay(coverageSummary, applicable, applicable?.target_set_id ?? null, queries.scope());

  return <section className="source-panel" aria-label="근거 및 출처 패널">
    <h2>근거 — {versionId.slice(0, 8)}…{pagePath !== null ? ` · ${pagePath}` : " · 페이지 없음"}{regionId !== null ? ` · 영역 ${regionId}` : ""}</h2>
    <p className="muted">서비스 확인은 작성자 선언을 승격하지 않으며, 원본 제공 입력 관측·작성자 바이트 일치·옳음을 주장하지 않습니다. 근거 포인터는 정확성의 증명이 아닙니다.</p>
    {!enabled && <p><button type="button" disabled title="서비스가 출처 검증을 제공하지 않습니다.">출처 근거 확인(제공되지 않음)</button></p>}
    {enabled && <p className="muted">증거를 펼쳐 출처 근거 확인을 실행할 수 있습니다. 기본 확인은 기대 비교 없이 바이트를 캡처합니다.</p>}
    {actionError && <p role="alert">{actionError}</p>}

    {metadata.kind === "loading" && <p role="status">공급 메타데이터를 확인하는 중…(헤더 영역에서 상태/재시도)</p>}
    {metadata.kind === "absent" && <p>공급된 소스/근거 메타데이터 없음 — 출처 기준 알 수 없음/검증되지 않음. 페이지 읽기와 평범한 JavaScript는 그대로 사용할 수 있습니다.</p>}
    {metadata.kind === "error" && <p className="muted">메타데이터를 확인하지 못했습니다(헤더 영역에서 재시도): {metadata.problem}</p>}

    {metadata.kind === "ready" && <div className="evidence-list">
      <h3>이 페이지의 영역 근거</h3>
      {pagePath !== null && regionsOfPage(metadata.metadata, pagePath).map((region) => {
        const binding = bindRegionEvidence(metadata.metadata, region);
        return <div key={region.id} className="evidence-region" data-selected={regionId === region.id}>
          <p><strong>{typeof region.label === "string" && region.label !== "" ? region.label : region.id}</strong> ({region.page_path} · 영역 ID {region.id})</p>
          {binding.resolved.length === 0 && binding.unresolved.length === 0 && <p className="muted">결속된 증거 없음 — 페이지/버전 문맥은 유지됩니다.</p>}
          <ul>{binding.resolved.map((evidence) => <EvidenceRow key={evidence.id} evidence={evidence} source={findSource(metadata.metadata, evidence)} expanded={expanded[evidence.id] === true} onToggle={() => toggleItem(evidence.id)} item={snap.items[evidence.id]} verificationsOpen={verificationsOpen[evidence.id] === true} onToggleVerifications={() => toggleVerifications(evidence.id)} verifications={snap.verifications[evidence.id]} onRetryVerifications={() => queries.retryVerifications(evidence.id)} enabled={enabled && controller.state.phase === "verified"} check={snap.checks[evidence.id]} onVerify={(compare) => verify(evidence, compare)} />)}
            {binding.unresolved.map((id) => <li key={id} className="muted">미해결 증거 참조 {id}</li>)}</ul>
        </div>;
      })}
      {pagePath !== null && regionsOfPage(metadata.metadata, pagePath).length === 0 && <p className="muted">이 페이지에 선언된 영역이 없습니다 — 페이지/버전 문맥으로 표시합니다.</p>}
      {pagePath === null && <p className="muted">페이지 없음 — 버전 문맥으로 표시합니다.</p>}
      {metadata.metadata.evidence.length === 0 && <p className="muted">공급된 증거 기록 없음.</p>}
      {/* Every valid authored evidence record stays inspectable in an
          explicit VERSION context: optional region binding never removes an
          opening path, and nothing here associates an unbound record with
          the current region. */}
      {metadata.metadata.evidence.length > 0 && <div className="version-evidence">
        <h4>버전 전체 증거(현재 영역과 무관)</h4>
        <ul>{metadata.metadata.evidence.map((evidence) => <EvidenceRow key={evidence.id} evidence={evidence} source={findSource(metadata.metadata, evidence)} expanded={expanded[evidence.id] === true} onToggle={() => toggleItem(evidence.id)} item={snap.items[evidence.id]} verificationsOpen={verificationsOpen[evidence.id] === true} onToggleVerifications={() => toggleVerifications(evidence.id)} verifications={snap.verifications[evidence.id]} onRetryVerifications={() => queries.retryVerifications(evidence.id)} enabled={enabled && controller.state.phase === "verified"} check={snap.checks[evidence.id]} onVerify={(compare) => verify(evidence, compare)} />)}</ul>
      </div>}
    </div>}

    <div className="assessment-block">
      <h3>신선도 요약(선택된 대상)</h3>
      {snap.assessments.phase === "idle" && <p className="muted">아직 확인하지 않았습니다.</p>}
      {snap.assessments.phase === "loading" && <p role="status">평가 이력/요약을 확인하는 중…</p>}
      {snap.assessments.phase === "unavailable" && <p>{snap.assessments.reason}</p>}
      {snap.assessments.phase === "error" && <div className="banner" role="alert">
        <p>평가 이력 페이지를 확인하지 못했습니다: {snap.assessments.reason} — 이미 열거된 {snap.assessments.history.items.length}건은 유지되며 완전해질 때까지 집계하지 않습니다.</p>
        <button type="button" onClick={() => queries.retryAssessments()}>실패한 지점부터 다시 불러오기</button>
      </div>}
      {snap.assessments.phase === "ready" && <FreshnessSummary applicable={applicable} sourceIntent={metadata.kind === "ready" ? metadata.metadata.sourceIntent : null} coverage={snap.coverage} coverageSummary={coverageSummary} overlay={overlay} unitsOpen={unitsOpen} onUnitsOpen={() => setUnitsOpen((value) => !value)} onRetryCoverage={() => queries.retryCoverage()} scopeTextValue={queries.scope() === undefined ? "버전 전체" : scopeText({ kind: queries.scope()!.kind, page_path: queries.scope()!.pagePath ?? null, region_id: queries.scope()!.regionId ?? null })} />}
      {snap.assessments.incompatibleApplicable && <p className="muted">반환된 적용 가능 요약이 선택된 범위와 일치하지 않아 선택된 집합으로 채택하지 않습니다 — 이력 행은 검사 가능합니다.</p>}
      {snap.assessments.history.items.length > 0 && <div>
        <h4>평가 이력</h4>
        <ul>{snap.assessments.history.items.map((item) => <li key={item.assessment_id}><span className="muted-code">{item.assessment_id.slice(0, 8)}…</span> · {scopeText(item.scope)} · 대상 {item.target_set_id ?? "없음"}{typeof item.state === "string" ? ` · ${item.state}` : ""}</li>)}</ul>
      </div>}
      {!snap.assessments.history.complete && snap.assessments.history.items.length > 0 && <p className="muted">이력 열거가 완료되지 않았습니다(커서 남음/실패) — 집계하지 않습니다.</p>}
    </div>
  </section>;
}

function findSource(metadata: SuppliedMetadata, evidence: SuppliedEvidenceRecord): SuppliedSourceRecord | null {
  return metadata.sources.find((source) => source.id === evidence.source_id) ?? null;
}

export function EvidenceRow(props: {
  enabled: boolean;
  check: VerificationAction | undefined;
  onVerify(compareDigest: boolean): void;
  evidence: SuppliedEvidenceRecord;
  source: SuppliedSourceRecord | null;
  expanded: boolean;
  onToggle(): void;
  item: { phase: string; reason: string | null; view: import("../../client/types.ts").EvidenceItemView | null } | undefined;
  verificationsOpen: boolean;
  onToggleVerifications(): void;
  verifications: { phase: string; reason: string | null; records: { phase: string; reason: string | null; items: Array<{ verification_id: string; outcome: string; checked_at: string | null; verified_at: string | null; reason?: string | null }>; complete: boolean } } | undefined;
  onRetryVerifications(): void;
}) {
  const [compareDigest, setCompareDigest] = useState(() => props.check?.request.expected_bytes_sha256 !== undefined);
  const { enabled, check, onVerify, evidence, source, expanded, onToggle, item, verificationsOpen, onToggleVerifications, verifications, onRetryVerifications } = props;
  return <li className="evidence-row">
    <button type="button" className="link" aria-expanded={expanded} onClick={onToggle}>
      {evidence.id} · {captureLabel(evidence.capture_status)}{typeof evidence.path === "string" ? ` · ${evidence.path}` : ""}{typeof evidence.line_start === "number" || typeof evidence.line_end === "number" ? `:${evidence.line_start ?? ""}-${evidence.line_end ?? ""}` : ""}
    </button>
    {source !== null && <p className="muted">소스 {source.id}({source.kind ?? "알 수 없음"}{typeof source.release_label === "string" && source.release_label !== "" ? `, ${source.release_label}` : ""}{typeof source.exact_revision === "string" ? `, ${source.exact_revision.slice(0, 12)}…` : ""})</p>}
    {expanded && <div className="evidence-detail">
      {typeof evidence.bytes_sha256 === "string" && <label><input type="checkbox" checked={compareDigest} disabled={check?.phase === "loading" || check?.phase === "pending"} onChange={(event) => setCompareDigest(event.target.checked)} />작성자 선언 다이제스트와 비교</label>}
      <p><button type="button" disabled={!enabled || check?.phase === "loading"} onClick={() => onVerify(compareDigest)}>{check?.phase === "pending" ? "원래 작업 결과 다시 확인" : check?.phase === "loading" ? "출처 근거 확인 중…" : "출처 근거 확인"}</button></p>
      {check?.reason && <p role="alert">{check.reason}</p>}
      {check?.result && <p role="status">확인 결과: {outcomeLabel(check.result.outcome)} · 확인 {check.result.checked_at ?? "시간 없음"} · 검증 {check.result.verified_at ?? "없음"}{check.result.reason ? ` · 사유 ${check.result.reason}` : ""}</p>}
      {item === undefined && <p role="status">서비스 뷰를 확인하는 중…</p>}
      {item?.phase === "loading" && <p role="status">서비스 뷰를 확인하는 중…</p>}
      {item?.phase === "unavailable" && <p>{item.reason}</p>}
      {item?.phase === "error" && <p role="alert">서비스 뷰 오류: {item.reason}</p>}
      {item?.phase === "ready" && item.view !== null && <div>
        <p>작성자 선언(불변): {captureLabel(item.view.authored.capture_status)}{typeof item.view.authored.path === "string" ? ` · ${item.view.authored.path}` : ""}</p>
        <p>서비스 가용성(별도): {evidenceAvailability(item.view)}{item.view.reason !== null ? ` · 사유 ${item.view.reason}` : ""}{item.view.bytes_origin !== null ? ` · 바이트 ${item.view.bytes_origin}` : ""}</p>
        <p className="muted">서비스 확인({item.view.service_verification === null ? "기록 없음" : "최근 기록 있음"})은 작성자 선언을 승격하지 않습니다.</p>
        {item.view.service_verification && <p>최근 서비스 확인: {String(item.view.service_verification.outcome ?? "미정")} · 확인 {String(item.view.service_verification.checked_at ?? "시간 없음")} · 검증 {String(item.view.service_verification.verified_at ?? "없음")}</p>}
        {item.view.availability === "available" && item.view.excerpt && <div><p>보관된 근거 바이트 · SHA-256 <span className="muted-code">{item.view.excerpt.bytes_sha256}</span></p>{evidenceExcerpt(item.view.excerpt) !== null ? <pre className="evidence-excerpt">{evidenceExcerpt(item.view.excerpt)}</pre> : <p className="muted">이 서비스 뷰에는 인라인 발췌가 없습니다.</p>}</div>}
        <button type="button" className="link" aria-expanded={verificationsOpen} onClick={onToggleVerifications}>검증 이력 보기/숨기기</button>
        {verificationsOpen && verifications && <div>
          {verifications.phase === "loading" && <p role="status">검증 이력을 확인하는 중…</p>}
          {verifications.phase === "unavailable" && <p>{verifications.reason}</p>}
          {verifications.phase === "error" && <div>
            <p role="alert">검증 이력 오류: {verifications.reason} — 이미 열거된 {verifications.records.items.length}건은 유지되며 완전해질 때까지 집계하지 않습니다.</p>
            <button type="button" onClick={onRetryVerifications}>검증 이력 다시 불러오기</button>
          </div>}
          {verifications.records.items.length > 0 && <ul>{verifications.records.items.map((record) => <li key={record.verification_id}><span className="muted-code">{record.verification_id.slice(0, 8)}…</span> · {outcomeLabel(record.outcome)} · 확인 {record.checked_at ?? "시간 없음"}{record.verified_at !== null ? ` · 검증 ${record.verified_at}` : ""}{record.reason ? ` · 사유 ${record.reason}` : ""}</li>)}</ul>}
          {!verifications.records.complete && verifications.records.items.length > 0 && <p className="muted">이력 열거가 완료되지 않았습니다 — 집계하지 않습니다.</p>}
          {verifications.phase === "ready" && verifications.records.items.length === 0 && <p className="muted">기록 없음.</p>}
          <p className="muted">사후 검증(posthoc)은 원본 제공 입력을 관측했다고 주장하지 않습니다.</p>
        </div>}
      </div>}
    </div>}
  </li>;
}

export function FreshnessSummary(props: {
  applicable: import("../../client/types.ts").ApplicableFreshness | null;
  sourceIntent: string | null;
  coverage: { phase: string; reason: string | null; units: { phase: string; reason: string | null; items: Array<{ kind: string; page_path: string | null; region_id: string | null; outcome: string; reason: string | null; discrepancy?: unknown; assessment_id: string | null; target_set_id: string | null }>; complete: boolean } };
  coverageSummary: ReturnType<typeof deriveCoverageSummary> | null;
  overlay: ReturnType<typeof compareWithOverlay> | null;
  unitsOpen: boolean;
  onUnitsOpen(): void;
  onRetryCoverage(): void;
  scopeTextValue: string;
}) {
  const { applicable, sourceIntent, coverage, coverageSummary, overlay, unitsOpen, onUnitsOpen, onRetryCoverage, scopeTextValue } = props;
  if (applicable === null) {
    return <p>선택된 실제 캡처 대상이 없습니다 — 상태 미평가, 커버리지 알 수 없음.{sourceIntent !== null ? ` (작성자 소스 의도: ${sourceIntent})` : ""}</p>;
  }
  return <div>
    <p>
      소스 의도 {applicable.source_intent ?? (sourceIntent ?? "없음")} · 대상 집합 {applicable.target_set_id ?? "없음"} · 캡처 {applicable.captured_at ?? "없음"} · 범위 {scopeText(applicable.scope)} · 평가 시각 {applicable.assessed_at ?? "없음"} · 재평가 필요 {applicable.reassessment_required ? "예" : "아니오"}
    </p>
    <p>
      기록된 배지: {statusLabel(applicable.status)} · 커버리지 {applicable.coverage.assessed}/{applicable.coverage.eligible}(완전 {applicable.coverage.complete ? "참" : "거짓"}, 단위 {applicable.coverage.unit_kind})
    </p>
    {coverage.phase === "idle" && <p className="muted">커버리지 단위 집합이 없습니다(대상 집합 없음) — 알 수 없음/미평가.</p>}
    {coverage.phase === "loading" && <p role="status">커버리지 단위를 열거하는 중…</p>}
    {coverage.phase === "unavailable" && <p>{coverage.reason}</p>}
    {(coverage.phase === "loading" || coverage.phase === "unavailable" || coverage.phase === "error") && coverage.units.items.length > 0 && <ul>{coverage.units.items.map((unit, index) => <li key={`${unit.kind}:${unit.page_path ?? ""}:${unit.region_id ?? ""}:${index}`}>
      {scopeText({ kind: unit.kind, page_path: unit.page_path, region_id: unit.region_id })} · {unitOutcomeLabel(unit.outcome)}{unit.reason !== null && unit.reason !== undefined ? ` · 사유 ${coverageReason(unit.reason)}` : ""}{unit.discrepancy === true || (typeof unit.discrepancy === "string" && unit.discrepancy !== "") ? " · 불일치 있음" : ""}{unit.assessment_id !== null ? ` · 기여 ${unit.assessment_id.slice(0, 8)}…` : " · 기여 없음"}{unit.target_set_id !== null ? ` · 대상 ${unit.target_set_id.slice(0, 8)}…` : ""}
    </li>)}</ul>}
    {coverage.phase === "error" && <div className="banner" role="alert">
      <p>커버리지 단위 확인 실패: {coverage.reason} — 이미 열거된 {coverage.units.items.length}개 단위는 유지되며 완전해질 때까지 집계하지 않습니다.</p>
      <button type="button" onClick={onRetryCoverage}>커버리지 다시 불러오기</button>
    </div>}
    {coverage.phase === "ready" && (coverageSummary === null
      ? <p className="muted">단위 열거가 완료되지 않았습니다 — 카운트/상태는 알 수 없음으로 표시합니다(분모 축소 없음).</p>
      : coverageSummary.aggregationProblem !== null
        ? <div>
          <p role="alert">집계 오류({coverageSummary.aggregationProblem}) — 열거된 단위 집합이 고정 비중첩 필수 집합 형태와 일치하지 않습니다. 단위는 검사 가능하지만 파생 카운트/상태는 알 수 없음으로 유지합니다(묵시적 중복 제거 없음).</p>
          <button type="button" className="link" aria-expanded={unitsOpen} onClick={onUnitsOpen}>단위/사유 펼치기/접기</button>
          {unitsOpen && <ul>{coverage.units.items.map((unit, index) => <li key={`${unit.kind}:${unit.page_path ?? ""}:${unit.region_id ?? ""}:${index}`}>
            {scopeText({ kind: unit.kind, page_path: unit.page_path, region_id: unit.region_id })} · {unitOutcomeLabel(unit.outcome)}{unit.reason !== null && unit.reason !== undefined ? ` · 사유 ${coverageReason(unit.reason)}` : ""}{unit.discrepancy === true || (typeof unit.discrepancy === "string" && unit.discrepancy !== "") ? " · 불일치 있음" : ""}{unit.assessment_id !== null ? ` · 기여 ${unit.assessment_id.slice(0, 8)}…` : " · 기여 없음"}{unit.target_set_id !== null ? ` · 대상 ${unit.target_set_id.slice(0, 8)}…` : ""}
          </li>)}</ul>}
        </div>
        : <div>
        <p>단위 파생: 평가됨 {coverageSummary.assessed}/{coverageSummary.eligible} · 부분 {coverageSummary.partial} · 차단 {coverageSummary.blocked} · 미확인 {coverageSummary.notChecked}{coverageSummary.foreignTarget > 0 ? ` · 다른 대상 기여 ${coverageSummary.foreignTarget}` : ""}{coverageSummary.outOfScope > 0 ? ` · 범위 밖 ${coverageSummary.outOfScope}` : ""} · 파생 상태 {statusLabel(overlay?.derived ?? "not_assessed")}</p>
        {overlay?.conflict === true && <p role="alert">기록된 배지가 단위 파생 결과와 충돌합니다 — 파생 결과를 표시하며 배지를 승격하지 않습니다.</p>}
        <button type="button" className="link" aria-expanded={unitsOpen} onClick={onUnitsOpen}>단위/사유 펼치기/접기</button>
        {unitsOpen && <ul>{coverage.units.items.map((unit, index) => <li key={`${unit.kind}:${unit.page_path ?? ""}:${unit.region_id ?? ""}:${index}`}>
          {scopeText({ kind: unit.kind, page_path: unit.page_path, region_id: unit.region_id })} · {unitOutcomeLabel(unit.outcome)}{unit.reason !== null && unit.reason !== undefined ? ` · 사유 ${coverageReason(unit.reason)}` : ""}{unit.discrepancy === true || (typeof unit.discrepancy === "string" && unit.discrepancy !== "") ? " · 불일치 있음" : ""}{unit.assessment_id !== null ? ` · 기여 ${unit.assessment_id.slice(0, 8)}…` : " · 기여 없음"}{unit.target_set_id !== null ? ` · 대상 ${unit.target_set_id.slice(0, 8)}…` : ""}
        </li>)}</ul>}
      </div>)}
    {coverage.units.items.some((unit) => unit.reason === "unexamined_dependency_scope") && <p className="muted">의존 범위 미검사: 인용 바이트 밖의 영향은 확인하지 않았으며 의미적 최신성이나 재평가를 판정하지 않습니다.</p>}
    <p className="muted">선택 범위: {scopeTextValue}. 불완전·차단·미확인 단위는 분모에서 사라지지 않으며, 0/알 수 없음은 정렬로 표시되지 않습니다. 캡처 ID/시각은 기록된 사실이지 현재 소스 가용성이 아닙니다.</p>
  </div>;
}
