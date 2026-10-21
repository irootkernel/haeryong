import { useEffect, useState } from "react";
import type { ShellController } from "../../client/controller.ts";
import type { ProjectSourceView, ProjectView, SourceMapping } from "../../client/types.ts";
import { navigate } from "../router.tsx";
import { ProjectRegistrationFlow, SourceConfigurationFlow, sourcesForPreview } from "./source-configuration.ts";
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
    setProviderText(Object.fromEntries(configured.map((source) => [source.source_id, source.provider_ids.join(", ")])));
  }, [state.configuration, projectId]);
  useEffect(() => {
    if (!state.plan) return;
    // A recovered server plan is also the editable draft after cancellation.
    setSources(structuredClone(state.previewSources));
    setProviderText(Object.fromEntries(state.previewSources.map((source) => [source.source_id, source.provider_ids.join(", ")])));
  }, [state.plan, state.previewSources]);
  const update = (index: number, change: Partial<SourceMapping>) => setSources((old) => old.map((s, i) => i === index ? { ...s, ...change } : s));
  const attempt = (action: () => Promise<void>) => { setLocalError(null); void action().catch((error) => setLocalError(String(error))); };
  const editing = ["editing", "error"].includes(state.phase) && !state.plan;
  return <section className="panel source-settings-panel" aria-label="출처 설정"><h1>프로젝트 설정 · 출처</h1><p className="muted-code">{projectId}</p>
    {(state.reason || localError) && <p role="alert">{localError ?? state.reason}</p>}
    {state.phase === "loading" && <p role="status">설정을 불러오는 중…</p>}
    {editing && <form className="source-settings" onSubmit={(event) => { event.preventDefault(); setConfirmed(false); attempt(() => flow.preview(sourcesForPreview(sources, providerText))); }}>
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
      <p>권한 영향 프로젝트 {Object.keys(state.plan.affected_permissions_revisions).join(", ") || "없음"}</p>
      <ul>{state.previewSources.map((source) => <li key={source.source_id}>{source.source_id} · {source.kind} · 루트 {source.root_path} · 보관 {source.retention_mode} · 공개 대상 {source.provider_ids.join(", ") || "없음"}</li>)}</ul>
      <p>출처·공개 범위 변경은 즉시 적용됩니다. 기존 권한 제한은 계속 적용됩니다.{state.plan.impact.controlled_restart_required === true ? " 적용 후 서비스를 다시 시작합니다." : ""}</p>
      {state.expandsScope && <label><input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} />출처/공개 범위 확대 영향을 확인했습니다</label>}
      {state.phase === "preview" && <button disabled={controller.state.phase !== "verified" || (state.expandsScope && !confirmed)} onClick={() => attempt(() => flow.commit(confirmed))}>확인 후 적용</button>}
      <button disabled={state.phase === "committing" || state.pending !== null} onClick={() => { flow.cancel(); setConfirmed(false); }}>취소</button>
    </div>}
    {["previewing", "committing"].includes(state.phase) && <p role="status">설정 작업 결과 확인 중…</p>}
    {state.phase === "pending" && <button onClick={() => attempt(() => flow.recover())}>원래 설정 작업 결과 다시 확인</button>}
    {state.phase === "stale" && <div><p>계획이 오래되었습니다. 현재 설정으로 새 미리보기가 필요합니다.</p><button onClick={() => attempt(() => flow.load())}>현재 설정 다시 불러오기</button></div>}
    {state.phase === "done" && <p role="status">출처 설정 적용됨</p>}
    <button onClick={() => navigate(`/projects/${projectId}/sources`)}>원본 목록으로</button>
  </section>;
}
