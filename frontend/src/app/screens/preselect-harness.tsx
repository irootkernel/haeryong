// Labelled Reader/Versions exact-document preselection harness. This is a
// verification entry point, NOT a product navigation flow: it mounts the SAME
// production ImportControl with an exact document context so the control's
// locked-target/preselected-base behavior stays provable before the real
// Reader (TASK-003-03) and Versions (TASK-004-05) screens ship. It is built
// only into the scratch verification page (frontend/harness.html via
// vite.harness.config.ts) and never into the committed production bundle.
import { useEffect, useRef, useState } from "react";
import { ShellController } from "../../client/controller.ts";
import { HttpHaeryongClient } from "../../client/http.ts";
import { BrowserCredentialStore } from "../../client/store.ts";
import type { DocumentView, ProjectView } from "../../client/types.ts";
import { ImportControl } from "./import-control.tsx";
import "../theme.css";

export function PreselectionHarness() {
  const [controller] = useState(() => new ShellController(new HttpHaeryongClient(), new BrowserCredentialStore()));
  const [projects, setProjects] = useState<ProjectView[]>([]);
  const [documents, setDocuments] = useState<DocumentView[]>([]);
  const [selected, setSelected] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [phase, setPhase] = useState(controller.state.phase);
  const [epoch, setEpoch] = useState("");
  const epochRef = useRef("");

  useEffect(() => { controller.start(); return () => controller.stop(); }, [controller]);
  useEffect(() => {
    const stop = controller.subscribe((state) => {
      setPhase(state.phase);
      if (state.phase === "unauthenticated" || state.phase === "incompatible") {
        epochRef.current = "";
        setEpoch("");
        setSelected("");
        setProjects([]);
        setDocuments([]);
        return;
      }
      if (state.phase !== "verified") return;
      const current = `${state.capabilities?.library_id}|${state.session?.principal_id}|${state.capabilities?.runtime_generation}`;
      if (epochRef.current !== current) {
        epochRef.current = current;
        setEpoch(current);
        setSelected("");
        setProjects([]);
        setDocuments([]);
      }
      void controller.client
        .listProjects()
        .then((page) => { if (epochRef.current === current) setProjects(page.items); })
        .catch(() => undefined);
      void controller.client
        .listDocuments({})
        .then((page) => { if (epochRef.current === current) setDocuments(page.items); })
        .catch((cause) => { if (epochRef.current === current) setError(cause instanceof Error ? cause.message : String(cause)); });
    });
    return stop;
  }, [controller]);

  const target = (() => {
    const document = documents.find((candidate) => candidate.document_id === selected);
    return document ? { projectId: document.project_id, documentId: document.document_id } : null;
  })();

  return (
    <main className="shell-content">
      <section className="panel" data-testid="preselection-harness">
        <h1>Reader/Versions 진입 지점 harness (검증 전용)</h1>
        <p data-state={phase}>연결 상태: {phase}</p>
        <p className="hint">
          이 화면은 검증용 진입 지점입니다. 동일한 가져오기 컨트롤에 문서가 미리 선택되어 잠긴 대상과 업로드 전 기준 버전이 표시되는지 확인합니다. 실제 Reader/Versions 화면 마운트는 각 소유 작업(TASK-003-03 / TASK-004-05)에서 수행합니다.
        </p>
        {error ? (
          <p className="hint" role="alert">
            {error}
          </p>
        ) : null}
        <div className="form-row">
          <label>
            문서
            <select value={selected} onChange={(event) => setSelected(event.target.value)}>
              <option value="">문서를 선택하세요</option>
              {documents.map((document) => (
                <option key={document.document_id} value={document.document_id}>
                  {document.title}
                </option>
              ))}
            </select>
          </label>
        </div>
      </section>
      {target && epoch && (phase === "verified" || phase === "degraded") ? <ImportControl key={`${epoch}:${target.projectId}:${target.documentId}`} controller={controller} projects={projects} documents={documents} preselected={target} /> : null}
    </main>
  );
}
