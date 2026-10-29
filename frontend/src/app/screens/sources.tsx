import { useEffect, useState } from "react";
import type { ShellController } from "../../client/controller.ts";
import type { ProjectSourceView, ProjectView, SourceMapping } from "../../client/types.ts";
import { navigate } from "../router.tsx";
import { ProjectRegistrationFlow, SourceConfigurationFlow, sourceConfirmation, sourcesForPreview } from "./source-configuration.ts";
import { coverageReason } from "./source-presentation.ts";

async function collect<T>(read: (cursor?: string) => Promise<{ items: T[]; next_cursor: string | null }>): Promise<T[]> {
  const rows: T[] = [];
  let cursor: string | undefined;
  do { const page = await read(cursor); rows.push(...page.items); cursor = page.next_cursor ?? undefined; } while (cursor !== undefined);
  return rows;
}

export function ProjectsScreen({ controller }: { controller: ShellController }) {
  const [projects, setProjects] = useState<ProjectView[]>([]);
  const [registration] = useState(() => new ProjectRegistrationFlow(controller.client, controller));
  useEffect(() => () => registration.dispose(), [registration]);
  const [name, setName] = useState(() => registration.pending?.name ?? "");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const pending = registration.pending;
  const [reload, setReload] = useState(0);
  useEffect(() => {
    let live = true;
    void collect((cursor) => controller.client.listProjects(cursor)).then((rows) => { if (live) setProjects(rows); }).catch((e) => { if (live) setError(String(e)); });
    return () => { live = false; };
  }, [controller, reload]);
  const register = async () => {
    setBusy(true); setError(null);
    try {
      await registration.run(name);
      setName(""); setReload((n) => n + 1);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally { setBusy(false); }
  };
  return <section className="panel" aria-label="프로젝트 목록"><h1>프로젝트</h1>
    {error && <p role="alert">{error}</p>}
    <ul>{projects.map((p) => <li key={p.project_id}><button className="link" onClick={() => navigate(`/projects/${p.project_id}/sources`)}>{p.name}</button> · <span className="muted-code">{p.project_id}</span></li>)}</ul>
    <form onSubmit={(event) => { event.preventDefault(); void register(); }}><label>프로젝트 이름<input type="text" value={name} disabled={busy || pending !== null} onChange={(e) => setName(e.target.value)} required maxLength={128} /></label>
      <button type="submit" disabled={busy || controller.state.phase !== "verified"}>{pending ? "등록 결과 다시 확인" : "프로젝트 등록"}</button></form>
  </section>;
}

export function SourcesScreen({ controller, projectId }: { controller: ShellController; projectId: string }) {
  const [rows, setRows] = useState<ProjectSourceView[]>([]);
  const [phase, setPhase] = useState("loading");
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let live = true; setRows([]); setPhase("loading"); setError(null);
    void collect((cursor) => controller.client.projectSources(projectId, cursor)).then((rows) => { if (live) { setRows(rows); setPhase("ready"); } }).catch((e) => { if (live) { setError(String(e)); setPhase("error"); } });
    return () => { live = false; };
  }, [controller, projectId]);
  return <section className="panel" aria-label="프로젝트 출처"><h1>원본</h1><p>프로젝트 <span className="muted-code">{projectId}</span></p>
    <p className="muted">기록된 관측만 표시합니다. 이 화면을 열어도 소스를 새로 읽지 않습니다.</p>
    {phase === "loading" && <p role="status">출처를 불러오는 중…</p>}{error && <p role="alert">{error}</p>}
    {phase === "ready" && rows.length === 0 && <p>등록된 출처 없음</p>}
    <ul>{rows.map((source) => <li key={source.source_id}><h2>{source.source_id}</h2><p>종류 {source.kind} · 저장소 {source.repo_id ?? "없음"}</p>
      <p>보관 {source.retention_mode} · 공개 대상 {source.provider_ids.length ? source.provider_ids.join(", ") : "없음"}</p>
      {source.last_observed_at === null ? <p>관측 기록 없음</p> : <p>마지막 관측 {source.last_observed_at} · 리비전 {source.last_observed_revision ?? "캡처 없음"} · 가용성 {source.last_availability}{source.last_observation_reason ? ` · ${coverageReason(source.last_observation_reason)}` : ""}</p>}
    </li>)}</ul>
    <button onClick={() => navigate(`/settings/projects?project=${projectId}`)}>출처 설정 편집</button>
  </section>;
}

const emptySource = (): SourceMapping => ({ source_id: crypto.randomUUID(), kind: "git", root_path: "", repo_id: null, retention_mode: "reference_only", provider_ids: [] });

export function SourceSettings({ controller, projectId }: { controller: ShellController; projectId: string }) {
  const [flow] = useState(() => new SourceConfigurationFlow(controller.client, projectId, controller));
  const [state, setState] = useState(flow.snapshot);
  const [sources, setSources] = useState<SourceMapping[]>([]);
  const [providerText, setProviderText] = useState<Record<string, string>>({});
  const [sourceMode, setSourceMode] = useState<"pinned" | "current">("pinned");
  const [providerId, setProviderId] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);
  useEffect(() => {
    const off = flow.subscribe(() => setState(flow.snapshot()));
    void flow.load();
    return () => { off(); flow.dispose(); };
  }, [flow]);
  useEffect(() => {
    if (!state.configuration) return;
    const configured = structuredClone(state.configuration.configuration.projects[projectId]?.sources ?? []);
    setSources(configured);
    setSourceMode(state.configuration.configuration.projects[projectId]?.source_mode_default ?? "pinned");
    setProviderId(state.configuration.configuration.projects[projectId]?.provider_id ?? "");
    setProviderText(Object.fromEntries(configured.map((source) => [source.source_id, source.provider_ids.join(", ")])));
  }, [state.configuration, projectId]);
  useEffect(() => {
    if (!state.plan) return;
    // A recovered server plan is also the editable draft after cancellation.
    setSources(structuredClone(state.previewSources));
    setSourceMode(state.previewDefaults.source_mode_default);
    setProviderId(state.previewDefaults.provider_id ?? "");
    setProviderText(Object.fromEntries(state.previewSources.map((source) => [source.source_id, source.provider_ids.join(", ")])));
  }, [state.plan, state.previewSources]);
  const update = (index: number, change: Partial<SourceMapping>) => setSources((old) => old.map((s, i) => i === index ? { ...s, ...change } : s));
  const attempt = (action: () => Promise<void>) => { setLocalError(null); void action().catch((error) => setLocalError(String(error))); };
  const editing = state.configuration !== null && ["editing", "error"].includes(state.phase) && !state.plan;
  const confirmation = sourceConfirmation(state);
  return <section className="panel source-settings-panel" aria-label="출처 설정"><h1>프로젝트 설정 · 출처</h1><p className="muted-code">{projectId}</p>
    {(state.reason || localError) && <p role="alert">{localError ?? state.reason}</p>}
    {state.phase === "loading" && <p role="status">설정을 불러오는 중…</p>}
    {state.configuration === null && state.phase === "error" && <div><p>현재 설정을 읽지 못했습니다. 값을 확인한 뒤 편집할 수 있습니다.</p><button onClick={() => attempt(() => flow.load())}>설정 다시 불러오기</button></div>}
    {editing && <form className="source-settings" onSubmit={(event) => { event.preventDefault(); setConfirmed(false); attempt(() => flow.preview(sourcesForPreview(sources, providerText), { source_mode_default: sourceMode, provider_id: providerId.trim() || null })); }}>
      <label>기본 소스 모드<select aria-label="기본 소스 모드" value={sourceMode} onChange={(event) => setSourceMode(event.target.value as "pinned" | "current")}><option value="pinned">Pinned · 선택한 정확한 리비전</option><option value="current">Current · 요청 시작 시 캡처</option></select></label>
      <label>기본 Provider ID<input aria-label="기본 Provider ID" type="text" value={providerId} onChange={(event) => setProviderId(event.target.value)} /></label>
      <p>기본 Provider 선택은 각 출처의 공개 허용 목록을 넓히지 않습니다. 공개 대상이 없으면 소유자 원본 읽기만 허용됩니다.</p>
      {sources.map((source, index) => <fieldset key={source.source_id}><legend>출처 {index + 1}</legend>
        <label>출처 ID<input type="text" value={source.source_id} readOnly required /></label>
        <label>종류<select aria-label="종류" value={source.kind} onChange={(e) => update(index, { kind: e.target.value as "git" | "files", repo_id: e.target.value === "files" ? null : source.repo_id })}><option value="git">Git</option><option value="files">Files</option></select></label>
        {source.kind === "git" && <label>저장소 ID<input type="text" value={source.repo_id ?? ""} onChange={(e) => update(index, { repo_id: e.target.value || null })} required /></label>}
        <label>승인할 원본 루트<input type="text" value={source.root_path} onChange={(e) => update(index, { root_path: e.target.value })} required /></label>
        <label>보관 방식<select aria-label="보관 방식" value={source.retention_mode} onChange={(e) => update(index, { retention_mode: e.target.value as SourceMapping["retention_mode"] })}><option value="reference_only">참조만 보관</option><option value="selected_snapshot">선택한 스냅샷 보관</option></select></label>
        <label>공개할 Provider ID(쉼표 구분)<input type="text" value={providerText[source.source_id] ?? ""} onChange={(e) => setProviderText((old) => ({ ...old, [source.source_id]: e.target.value }))} /></label>
        <button type="button" onClick={() => setSources((old) => old.filter((_, i) => i !== index))}>출처 제거</button>
      </fieldset>)}
      <div className="form-row"><button type="button" onClick={() => setSources((old) => [...old, emptySource()])}>출처 추가</button><button type="submit" disabled={controller.state.phase !== "verified"}>변경 미리보기</button></div>
    </form>}
    {state.plan && state.phase !== "done" && <div aria-label="설정 계획 미리보기"><h2>설정 계획 미리보기</h2><p>기준 리비전 {state.plan.base_config_revision} · 만료 {state.plan.expires_at}</p>
      <p>기본 소스 모드 {state.previewDefaults.source_mode_default} · 기본 Provider {state.previewDefaults.provider_id ?? "없음"}</p>
      {state.providerChange && <div aria-label="기본 Provider 변경 영향"><p>기본 Provider: {state.providerChange.before ?? "없음"} → {state.providerChange.after ?? "없음"}</p><p>새 작업의 기본 선택에 적용됩니다. 이미 캡처한 작업의 Provider는 바뀌지 않습니다. 자격 증명이나 과금 방식은 변경하지 않습니다. 기본 Provider 변경만으로는 출처별 공개 허용 목록이 바뀌지 않습니다. 공개 허용 목록 변경은 아래 출처 계획에 따릅니다. 제출할 Provider는 해당 출처의 공개 허용 목록에 있어야 합니다.</p></div>}
      <p>권한 영향 프로젝트 {Object.keys(state.plan.affected_permissions_revisions).join(", ") || "없음"}</p>
      <ul>{state.previewSources.map((source) => <li key={source.source_id}>{source.source_id} · {source.kind} · 루트 {source.root_path} · 보관 {source.retention_mode} · 공개 대상 {source.provider_ids.join(", ") || "없음"}</li>)}</ul>
      <p>출처·공개 범위 변경은 즉시 적용됩니다. 기존 권한 제한은 계속 적용됩니다.{state.plan.impact.controlled_restart_required === true ? " 적용 후 서비스를 다시 시작합니다." : ""}</p>
      {confirmation && <label className="choice"><input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} />{confirmation}</label>}
      {state.phase === "preview" && <button disabled={controller.state.phase !== "verified" || (confirmation !== null && !confirmed)} onClick={() => attempt(() => flow.commit(confirmed))}>확인 후 적용</button>}
      <button disabled={state.phase === "committing" || state.pending !== null} onClick={() => { flow.cancel(); setConfirmed(false); }}>취소</button>
    </div>}
    {["previewing", "committing"].includes(state.phase) && <p role="status">설정 작업 결과 확인 중…</p>}
    {state.phase === "pending" && <button onClick={() => attempt(() => flow.recover())}>원래 설정 작업 결과 다시 확인</button>}
    {state.phase === "stale" && <div><p>계획이 오래되었습니다. 현재 설정으로 새 미리보기가 필요합니다.</p><button onClick={() => attempt(() => flow.load())}>현재 설정 다시 불러오기</button></div>}
    {state.phase === "done" && <p role="status">출처 설정 적용됨</p>}
    <p className="muted">Haeryong 원본 reader는 선택한 루트의 파일을 읽고 원본을 수정하지 않습니다. 실제 제공자는 아직 연결되지 않았습니다. 외부 에이전트는 소유자의 기존 도구·권한을 사용하며, 이 설정은 OS 읽기 격리 경계가 아닙니다.</p>
    <button onClick={() => navigate(`/projects/${projectId}/sources`)}>원본 목록으로</button>
  </section>;
}
