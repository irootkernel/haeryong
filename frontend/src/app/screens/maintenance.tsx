import { FindingActions, MaintenanceRecovery } from "./finding-actions.tsx";
import { useCallback, useEffect, useRef, useState } from "react";
import type { ShellController } from "../../client/controller.ts";
import type { FindingKind, FindingStatus, FindingView, JobView, ScanDepth, ScanRequest } from "../../client/types.ts";
import { navigate } from "../router.tsx";
import { assessmentDetailUnits, compareWithOverlay, deriveCoverageSummary, renderAssessmentText } from "./evidence-view.ts";
import { FreshnessSummary } from "./source-panel.tsx";
import { useReviewInvalidation } from "./review-refresh.ts";
import { exactReaderLink, FINDING_GROUPS, FINDING_STATUSES, freshnessProposal, MaintenanceFreshnessState, MaintenanceState, object, scanContext, scopeLabel, statusLabel } from "./maintenance-state.ts";
import type { FindingScope, PageState } from "./maintenance-state.ts";
import { topicEvidenceBasisLabel } from "./management-presentation.ts";

export function MaintenanceScreen({ controller, jobId }: { controller: ShellController; jobId?: string }) {
  return <MaintenanceSelection key={jobId ?? "overview"} controller={controller} jobId={jobId} />;
}

function MaintenanceSelection({ controller, jobId }: { controller: ShellController; jobId?: string }) {
  const [model] = useState(() => new MaintenanceState(controller, jobId));
  const [snap, setSnap] = useState(() => model.snapshot());
  const [scanOpen, setScanOpen] = useState(false);
  const [scanActivationError, setScanActivationError] = useState<string | null>(null);
  useEffect(() => {
    const off = model.subscribe(() => setSnap(model.snapshot()));
    model.connect();
    setSnap(model.snapshot());
    void model.refresh();
    return () => { off(); model.dispose(); };
  }, [model]);
  const refreshPassive = useCallback(async (event?: { resync?: boolean }) => {
    await model.refresh(true, Boolean(event?.resync));
  }, [model]);
  useReviewInvalidation(controller, true, refreshPassive, () => model.scanBusy);
  const enabled = controller.state.phase === "verified" && controller.state.capabilities?.features.scans === true;
  const visible = enabled && !snap.accessLost;
  const grouped = FINDING_GROUPS.map(group => ({ ...group, rows: snap.findings.items.filter(finding => (group.kinds as readonly string[]).includes(finding.kind)) }));
  const unknown = snap.findings.items.filter(finding => !FINDING_GROUPS.some(group => (group.kinds as readonly string[]).includes(finding.kind)));
  return <section className="panel maintenance-screen" aria-label="관리 및 발견 항목">
    <h1>{jobId ? "스캔 발견 항목" : "관리"}</h1>
    <p className="muted">저장된 작업과 발견 항목을 표시합니다. 화면 열기와 새로고침은 스캔·소스 읽기·AI 실행을 시작하지 않습니다. 전체 실행 간 최종 보고서는 아직 제공하지 않습니다.</p>
    <div className="form-row">
      {jobId && <button type="button" onClick={() => navigate("/maintenance")}>관리 목록으로</button>}
      <button type="button" disabled={!enabled} onClick={() => { void model.refresh(); }}>목록/작업 새로고침</button>
      <button type="button" disabled={!enabled || !controller.client.scanStart || snap.scanBusy} onClick={() => {
        try { controller.requireVerifiedDispatch(controller.captureDispatchAuthority()); setScanActivationError(null); setScanOpen(true); void model.loadScanOptions(); }
        catch (error) { setScanActivationError(error instanceof Error ? error.message : String(error)); }
      }}>수동 스캔 준비</button>
      <button type="button" onClick={() => navigate("/settings/maintenance")}>관리 정책 설정</button>
    </div>
    {visible && <MaintenanceRecovery controller={controller} onChanged={() => model.refresh()} />}
    {scanActivationError && <p role="alert">{scanActivationError}</p>}
    {!enabled && <p role="status">현재 검증된 연결 또는 스캔 기능을 사용할 수 없습니다. 저장된 상세 정보는 표시하지 않습니다.</p>}
    {enabled && snap.accessLost && <p role="alert">이 대상의 접근 권한을 확인할 수 없어 보호된 정보를 지웠습니다. 현재 권한으로 새로 확인하세요.</p>}
    {visible && !jobId && <>
      <h2>활성 및 과거 스캔 작업</h2>
      <PageControls page={snap.jobs} noun="작업" onMore={() => { void model.jobs.more(); }} />
      <ul>{snap.jobs.items.map(job => <li key={job.job_id}>
        <button type="button" className="link" onClick={() => navigate(`/maintenance/jobs/${encodeURIComponent(job.job_id)}`)}>{job.job_id}</button>
        {" · "}{job.state} · revision {job.revision}{job.error && ` · ${job.error}`}
      </li>)}</ul>
    </>}
    {visible && jobId && <>
      <p className="muted-code">작업 {jobId}</p>
      {snap.jobLoading && <p role="status">작업을 확인하는 중…</p>}
      {snap.jobError && <p role="alert">작업 확인 실패: {snap.jobError} — 이전에 확인된 상태가 있으면 유지합니다.</p>}
      {snap.job && <JobReport job={snap.job} />}
      <div className="form-row" aria-label="발견 항목 필터">
        <label>종류<select value={snap.filter.kind ?? ""} onChange={event => { void model.setFilter({ ...snap.filter, kind: event.target.value as FindingKind || undefined }); }}>
          <option value="">모든 종류</option>
          {FINDING_GROUPS.flatMap(group => group.kinds.map(kind => <option key={kind} value={kind}>{group.label} · {kind}</option>))}
        </select></label>
        <label>상태<select value={snap.filter.status ?? ""} onChange={event => { void model.setFilter({ ...snap.filter, status: event.target.value as FindingStatus || undefined }); }}>
          <option value="">모든 상태</option>{FINDING_STATUSES.map(status => <option key={status} value={status}>{statusLabel(status)}</option>)}
        </select></label>
      </div>
      <PageControls page={snap.findings} noun="발견 항목" onMore={() => { void model.findings.more(); }} />
      {grouped.map(group => <section key={group.id} aria-label={group.label} className="maintenance-group">
        <h2>{group.label} · 불러온 {group.rows.length}건</h2>
        {group.rows.length === 0 && <p className="muted">현재 불러온 페이지에는 이 종류의 항목이 없습니다.{!snap.findings.complete && " 아직 전체 열거가 끝나지 않았습니다."}</p>}
        {group.rows.map(finding => <FindingCard key={finding.finding_id} finding={finding} job={snap.job} controller={controller} onChanged={() => model.refresh()} onDenied={() => model.clearProtected()} />)}
      </section>)}
      {unknown.length > 0 && <section aria-label="해석할 수 없는 발견 항목"><h2>해석할 수 없는 종류</h2>{unknown.map(row => <FindingCard key={row.finding_id} finding={row} job={snap.job} controller={controller} onChanged={() => model.refresh()} onDenied={() => model.clearProtected()} />)}</section>}
    </>}
    {scanOpen && enabled && !snap.accessLost && <ScanDialog key={JSON.stringify(controller.captureDispatchAuthority())} controller={controller} model={model} onClose={() => setScanOpen(false)} />}
  </section>;
}

function PageControls<T>({ page, noun, onMore }: { page: PageState<T>; noun: string; onMore(): void }) {
  return <div>
    <p role="status">불러온 {noun} {page.items.length}건 · {page.error ? "이전 결과 보존 · 현재 확인 실패" : page.complete ? "현재 필터 열거 완료" : "열거 미완료"}{page.loading && " · 불러오는 중…"}</p>
    {page.error && <p role="alert">{page.error} — 앞선 {page.items.length}건을 유지합니다. 다시 시도하면 같은 실패 지점부터 확인합니다.</p>}
    {(!page.complete || page.error) && <button type="button" disabled={page.loading} aria-busy={page.loading} onClick={onMore}>{page.error ? "다시 불러오기" : page.loadedPages ? "더 불러오기" : "불러오기"}</button>}
  </div>;
}

function RecordFields({ value, fields }: { value: Record<string, unknown>; fields: readonly string[] }) {
  return <dl className="maintenance-facts">{fields.map(field => <div key={field}><dt>{field}</dt><dd>{renderAssessmentText(value[field])}</dd></div>)}</dl>;
}

function JobReport({ job }: { job: JobView }) {
  const context = scanContext(job);
  return <section aria-label="실제 스캔 진행 및 캡처 정책">
    <p>상태 {job.state} · 작업 revision {job.revision}{job.cancel_requested && " · 취소 요청됨"}{job.error && ` · ${job.error}`}</p>
    {!context ? <p>스캔 보고서의 상세 형식을 확인할 수 없습니다. 작업 상태를 완전한 의미 평가로 추정하지 않습니다.</p> : <>
      <RecordFields value={context.report} fields={["stage", "enumeration_complete", "captured", "structural", "semantic", "inspected_candidate_relations", "overlays", "drafts", "outdated_units", "findings", "provider_attempts", "config_revision", "permissions_revisions"]} />
      <p className="muted">구조 검사·후보 검사·의미 평가·게시된 평가는 별도입니다. 작업의 assessed/eligible 및 complete는 현재 적용된 버전 전체 정렬 판정이 아닙니다.</p>
      <details><summary>요청 범위와 캡처된 유효 정책</summary><RecordFields value={context.report} fields={["requested_scope", "effective_policy"]} /></details>
      <details><summary>작업에 기록된 소스 선택·캡처</summary>
        <p className="muted">요청 모드와 소스 의도, 원래 입력 관측, 이후 인용 소스 검증은 별도 기록입니다. 작업의 source_targets를 개별 평가의 대상과 임의로 결합하지 않습니다.</p>
        {renderAssessmentText(context.targets)}
      </details>
    </>}
  </section>;
}

function FindingCard({ finding, job, controller, onChanged, onDenied }: { finding: FindingView; job: JobView | null; controller: ShellController; onChanged(): void | Promise<void>; onDenied(): void }) {
  const [historyOpen, setHistoryOpen] = useState(false);
  const proposal = object(finding.proposal), assessment = freshnessProposal(finding);
  const scope = assessment ? assessment.scope as FindingScope : null;
  const ownLink = exactReaderLink(finding.document_id, assessment?.version_id ?? finding.version_id, assessment ? scope!.page_path : finding.page_path ?? null, assessment ? scope!.region_id : finding.region_id ?? null);
  const targetLink = proposal ? exactReaderLink(proposal.target_document_id, proposal.target_version_id, proposal.target_page_path ?? null, proposal.target_region_id ?? null) : null;
  const content = finding.kind === "new_document_proposal" || finding.kind === "new_version_proposal";
  const context = scanContext(job, finding);
  return <article className="maintenance-finding" aria-label={`발견 항목 ${finding.finding_id}`}>
    <h3>{finding.kind} · {statusLabel(finding.status)}</h3>
    <p><strong>{finding.origin === "ai" ? "AI가 추가한 관리 정보" : "결정적 구조 검사"}</strong> · {finding.finding_id} · revision {finding.revision}</p>
    <p>결정 주체 {renderAssessmentText(finding.decided_by)} · 결정 시각 {renderAssessmentText(finding.decided_at)} · 승인 필요 {finding.approval_required ? "예" : "아니오"}</p>
    {content && <p><strong>미게시 콘텐츠 제안</strong> · 이 항목은 새 문서·버전 또는 준비된 초안이 아닙니다. 게시된 결과 링크를 만들지 않습니다.</p>}
    {finding.kind === "topic_assignment" && <p>나중에 제안/적용한 관리 주제입니다. 작성된 본문의 주제·내용 커버리지로 간주하지 않습니다.</p>}
    {finding.kind === "topic_assignment" && <p>근거 {topicEvidenceBasisLabel(proposal?.evidence_basis)}</p>}
    {finding.kind === "related_explanation" && <p>AI 관계/제안입니다. 작성된 참조·역링크와 별개이며, 주제적 유사성은 사용 관계나 의존성을 뜻하지 않습니다.</p>}
    <RecordFields value={finding as unknown as Record<string, unknown>} fields={["project_id", "document_id", "version_id", "page_path", "region_id", "target_set_id", "captured_config_revision", "permissions_revision", "reason", "applied_ref", "etag", "created_at", "updated_at"]} />
    <p><strong>근거와 이유</strong></p><p className="maintenance-prose">{finding.rationale}</p>
    <p><strong>불확실성/제한</strong></p><p className="maintenance-prose">{finding.uncertainty === "" ? "빈 기록(없음)" : finding.uncertainty}</p>
    {assessment ? <section aria-label="제안된 최신성 평가">
      <h4>{finding.status === "proposed" ? "최신성 평가 후보 · 적용된 최신성 아님" : "발견 항목에 보관된 평가 · 현재 적용 상태는 별도 조회"}</h4>
      <p>실제 평가 범위: {scopeLabel(scope)} · {assessment.state}</p>
      <RecordFields value={assessment} fields={["assessment_id", "version_id", "target_set_id", "coverage_set_id", "scope", "state", "assessed_at", "config_revision", "permissions_revision", "origin"]} />
      <p>이 제안에는 적용 뷰의 캡처 시각·재평가 필요·전체 버전 커버리지 집계를 임의로 붙이지 않습니다. 실제 단위와 제한은 보관된 평가 근거에서 확인합니다.</p>
      {renderAssessmentText(assessment.evidence)}
      {ownLink && <button type="button" aria-expanded={historyOpen} onClick={() => { controller.requireVerifiedDispatch(controller.captureDispatchAuthority()); setHistoryOpen(value => !value); }}>동일 대상/범위의 적용된 평가와 이력 {historyOpen ? "닫기" : "보기"}</button>}
      {historyOpen && <PublishedFreshness key={JSON.stringify([assessment.version_id, assessment.target_set_id, scope, finding.revision])} controller={controller} versionId={assessment.version_id} targetSetId={assessment.target_set_id!} scope={scope!} />}
    </section> : finding.kind === "freshness_assessment" && <p role="status">보관된 평가 형식/범위를 해석할 수 없습니다. 바깥 page_path가 비어 있어도 버전 전체 평가로 추정하지 않습니다.</p>}
    {context && <details><summary>이 작업의 요청 소스 모드와 캡처 기록</summary>
      <p>요청 소스 모드: {renderAssessmentText(context.requested?.source_mode)} · 소스 의도 명시 기록: {renderAssessmentText(context.requested?.source_intent)}</p>
      <p>아래는 작업에 기록된 선택입니다. 원래 입력 관측이나 개별 평가 대상의 캡처/최신성으로 승격하지 않습니다.</p>{renderAssessmentText(context.targets)}
    </details>}
    <details><summary>완전한 근거·제안·적용 참조</summary><RecordFields value={finding as unknown as Record<string, unknown>} fields={["evidence", "evidence_fingerprint", "proposal", "applied_ref"]} /></details>
    <div className="form-row">
      {ownLink && <button type="button" className="link" onClick={() => navigate(ownLink)}>원래 버전/페이지/영역 열기</button>}
      {targetLink && <button type="button" className="link" onClick={() => navigate(targetLink)}>정확한 관계 대상 열기</button>}
    </div>
    <FindingActions controller={controller} finding={finding} onChanged={onChanged} onDenied={onDenied} />
  </article>;
}

function PublishedFreshness({ controller, versionId, targetSetId, scope }: { controller: ShellController; versionId: string; targetSetId: string; scope: FindingScope }) {
  const [model] = useState(() => new MaintenanceFreshnessState(controller, versionId, scope, targetSetId));
  const [snap, setSnap] = useState(() => model.snapshot());
  const [unitsOpen, setUnitsOpen] = useState(false);
  useEffect(() => { const off = model.subscribe(() => setSnap(model.snapshot())); model.connect(); void model.refresh(); return () => { off(); model.dispose(); }; }, [model]);
  const passive = useCallback(async (event?: { resync?: boolean }) => { await model.refresh(true, Boolean(event?.resync)); }, [model]);
  useReviewInvalidation(controller, true, passive);
  const summary = snap.coverage.complete ? deriveCoverageSummary(snap.coverage.items, { targetSetId, scope: { kind: scope.kind, pagePath: scope.page_path ?? undefined, regionId: scope.region_id ?? undefined }, unitKind: snap.applicable?.coverage.unit_kind }) : null;
  const overlay = summary === null ? null : compareWithOverlay(summary, snap.applicable, targetSetId, { kind: scope.kind, pagePath: scope.page_path ?? undefined, regionId: scope.region_id ?? undefined });
  const coverage = { phase: snap.coverage.loading ? "loading" : snap.coverage.error ? "error" : snap.coverage.loadedPages ? "ready" : snap.applicable?.coverage.units_url ? "not_loaded" : "idle", reason: snap.coverage.error,
    units: { phase: snap.coverage.loading ? "loading" : "ready", reason: snap.coverage.error, items: snap.coverage.items, complete: snap.coverage.complete } };
  if (snap.accessLost) return <div role="alert"><p>평가의 접근 권한을 확인할 수 없어 보호된 이력을 지웠습니다.</p><button type="button" onClick={() => { void model.refresh(); }}>현재 권한으로 다시 확인</button></div>;
  return <section aria-label="적용된 최신성 및 저장 이력">
    <h4>별도 조회한 적용된 최신성 · 저장 이력</h4>
    {snap.applicable?.coverage.units_url && snap.coverage.loadedPages === 0 && !snap.coverage.loading && !snap.coverage.error && <p>고정 커버리지 단위는 아직 불러오지 않았습니다. 아래에서 확인할 수 있습니다.</p>}
    {snap.history.loadedPages > 0 ? <FreshnessSummary applicable={snap.applicable} sourceIntent={null} coverage={coverage} coverageSummary={summary} overlay={overlay} unitsOpen={unitsOpen} onUnitsOpen={() => setUnitsOpen(open => !open)} onRetryCoverage={() => { void model.coverage.more(); }} scopeTextValue={scopeLabel(scope)} /> : <p>이 대상/범위의 적용 뷰는 아직 확인되지 않았습니다. 조회 전·실패 상태를 미평가 또는 실제 캡처 없음으로 단정하지 않습니다.</p>}
    <PageControls page={snap.history} noun="평가 이력" onMore={() => { void model.history.more(); }} />
    {snap.history.items.map(item => <details key={item.assessment_id}><summary>{item.assessment_id} · {item.state ?? "상태 기록 없음"} · {item.assessed_at ?? "평가 시각 기록 없음"}</summary>
      {renderAssessmentText(item)}
      {assessmentDetailUnits(item, versionId) === null && <p>세부 단위를 해석할 수 없는 과거 기록입니다. 없는 커버리지를 만들지 않습니다.</p>}
    </details>)}
    {snap.applicable?.coverage.units_url && <PageControls page={snap.coverage} noun="고정 평가 단위" onMore={() => { void model.coverage.more(); }} />}
    <p className="muted">이 조회는 원래 입력 관측과 이후 소스 검증을 대신하지 않습니다. 현재 source availability를 새로 확인하거나 AI 평가를 시작하지 않습니다.</p>
  </section>;
}

function ScanDialog({ controller, model, onClose }: { controller: ShellController; model: MaintenanceState; onClose(): void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [snap, setSnap] = useState(() => model.snapshot());
  const [selected, setSelected] = useState<string[]>([]);
  const [allProjects, setAllProjects] = useState(false);
  const [depth, setDepth] = useState<ScanDepth>("change_focused");
  const [sourceMode, setSourceMode] = useState<"" | "pinned" | "current">("");
  const [versionId, setVersionId] = useState("");
  const [page, setPage] = useState("");
  const [region, setRegion] = useState("");
  const [targetSet, setTargetSet] = useState("");
  const [localError, setLocalError] = useState<string | null>(null);
  useEffect(() => { dialog.current?.showModal(); const off = model.subscribe(() => setSnap(model.snapshot())); return off; }, [model]);
  const verified = controller.state.phase === "verified" && controller.state.capabilities?.features.scans === true && !snap.accessLost;
  const pending = snap.scanBusy || snap.unconfirmedOperation !== null;
  const configuration = snap.configuration?.configuration;
  return <dialog ref={dialog} aria-labelledby="maintenance-scan-title" className="maintenance-scan-dialog" onCancel={() => onClose()} onClose={onClose}>
    <h2 id="maintenance-scan-title">수동 스캔 요청</h2>
    <p>확인하고 시작을 눌러야 요청됩니다. 이 요청은 선택한 범위와 정책에 따라 소스 읽기와 AI 작업을 시작할 수 있습니다.</p>
    {snap.optionsLoading && <p role="status">허용 프로젝트와 현재 정책을 확인하는 중…</p>}
    {snap.optionsError && <p role="alert">{snap.optionsError}</p>}
    <button type="button" disabled={!verified || snap.optionsLoading || snap.scanBusy} onClick={() => { void model.loadScanOptions(); }}>프로젝트/정책 다시 확인</button>
    <form onSubmit={event => { event.preventDefault();
      try {
        controller.requireVerifiedDispatch(controller.captureDispatchAuthority());
        if (!allProjects && selected.length === 0) throw new Error("스캔할 프로젝트를 선택하세요.");
        if (region && !page || page && !versionId) throw new Error("페이지에는 정확한 버전, 영역에는 정확한 페이지가 필요합니다.");
        const request: ScanRequest = { scope: allProjects ? "all_allowed_projects" : "selected_projects", ...(allProjects ? {} : { project_ids: selected }), depth,
          ...(sourceMode ? { source_mode: sourceMode } : {}), ...(versionId ? { version_id: versionId } : {}), ...(page ? { page_path: page } : {}), ...(region ? { region_id: region } : {}), ...(targetSet ? { target_set_id: targetSet } : {}) };
        setLocalError(null); void model.startScan(request, crypto.randomUUID()).catch(error => setLocalError(error instanceof Error ? error.message : String(error)));
      } catch (error) { setLocalError(error instanceof Error ? error.message : String(error)); }
    }}>
      <fieldset disabled={!verified || pending}><legend>허용 범위</legend>
        <label><input type="checkbox" checked={allProjects} onChange={event => setAllProjects(event.target.checked)} />허용된 모든 프로젝트를 명시적으로 선택</label>
        {!allProjects && <div>{snap.projects.items.map(project => <label key={project.project_id}><input type="checkbox" checked={selected.includes(project.project_id)} onChange={event => setSelected(values => event.target.checked ? [...values, project.project_id] : values.filter(id => id !== project.project_id))} />{project.name} · {project.project_id}</label>)}<PageControls page={snap.projects} noun="허용 프로젝트" onMore={() => { void model.projects.more(); }} /></div>}
        <label>검사 깊이<select value={depth} onChange={event => setDepth(event.target.value as ScanDepth)}><option value="change_focused">변경 중심</option><option value="full_scope">전체 범위</option><option value="deep_semantic">깊은 의미 평가</option></select></label>
        <label>소스 모드<select value={sourceMode} onChange={event => setSourceMode(event.target.value as typeof sourceMode)}><option value="">기억된 프로젝트/소스 선택 사용</option><option value="pinned">고정 소스</option><option value="current">현재 소스</option></select></label>
        <label>정확한 버전 ID(선택)<input value={versionId} onChange={event => setVersionId(event.target.value)} /></label>
        <label>페이지 경로(선택)<input value={page} onChange={event => setPage(event.target.value)} /></label>
        <label>페이지의 영역 ID(선택)<input value={region} onChange={event => setRegion(event.target.value)} /></label>
        <label>정확한 대상 집합 ID(선택)<input value={targetSet} onChange={event => setTargetSet(event.target.value)} /></label>
      </fieldset>
      <h3>현재 정책과 예산</h3>
      <p>설정 revision {snap.configuration?.config_revision ?? "확인되지 않음"}. 프로젝트 정책은 라이브러리 범위를 좁힐 수 있습니다. 수락된 작업의 캡처된 유효 정책이 실제 적용 기준입니다.</p>
      {renderAssessmentText(configuration?.maintenance)}
      {(allProjects ? snap.projects.items.map(project => project.project_id) : selected).map(id => <details key={id}><summary>프로젝트 정책 · {id}</summary>{renderAssessmentText(object(configuration?.projects[id])?.maintenance)}</details>)}
      {localError && <p role="alert">{localError}</p>}{snap.scanError && <p role="alert">{snap.scanError}</p>}
      <div className="form-row"><button type="submit" disabled={!verified || pending || !snap.configuration || snap.optionsLoading || (!allProjects && selected.length === 0)} aria-busy={snap.scanBusy}>확인하고 스캔 시작</button>
        <button type="button" onClick={onClose}>닫기</button>
        {snap.unconfirmedOperation && <button type="button" disabled={!verified || snap.scanBusy} onClick={() => { void model.recoverScan().catch(error => setLocalError(String(error))); }}>기존 요청 영수증 확인</button>}
      </div>
      {snap.unconfirmedOperation && <p>미확인 요청 {snap.unconfirmedOperation} · 스캔을 다시 제출하지 않습니다.</p>}
      {snap.accepted && <p role="status">스캔 요청 수락됨 · <button type="button" className="link" onClick={() => { onClose(); navigate(`/maintenance/jobs/${encodeURIComponent(snap.accepted!.job_id)}`); }}>{snap.accepted.job_id}</button></p>}
    </form>
  </dialog>;
}
