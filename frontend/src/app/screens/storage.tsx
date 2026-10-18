// Settings/Storage upload discovery: the owner list with actual
// state/checkpoint/target/bytes/activity/reusability/pin summaries plus
// entries/ranges/pins detail across cursor pages. A journal owned by THIS
// browser resumes with a reselected input through full range verification;
// a journal-LESS client (lost local ID or a second owner device) can still
// recover retained OPEN staging — it reselects the input, the engine
// verifies every retained range with honest mixed-input disclosure for the
// unknown original digest, completes with a fresh S and STOPS AT SEALED:
// publication remains a separately confirmed explicit new operation and is
// never automatic. Every async mutation captures one immutable
// owner/library/generation authority per flow and rechecks it immediately
// before each protected dispatch. Input-mode honesty: only a folder
// (FileList) reselect cannot represent empty directories or symlinks.
import { useCallback, useEffect, useRef, useState } from "react";
import type { ShellController } from "../../client/controller.ts";
import { ApiError } from "../../client/types.ts";
import type { DocumentView, ListPage, UploadConfirmedRange, UploadDetail, UploadEntryProgress, UploadPin, UploadSummary } from "../../client/types.ts";
import { BrowserJournalStore, JournalReadError, JournalWriteError, journalAuthority, journalKey, planFromArchive, planFromFolder, planFromSingleFile, TransferEngine } from "../../client/transfer.ts";
import type { ImportJournal, PlannedInput } from "../../client/transfer.ts";
import { DispatchBlockedError } from "../../client/types.ts";
import { JOURNAL_UNAVAILABLE_NOTICE, runImportPipeline } from "./pipeline.ts";

interface StorageScreenProps {
  controller: ShellController;
}

type ResumeStage =
  | { kind: "idle" }
  | { kind: "reselect"; upload: UploadSummary; owned: boolean; record: ImportJournal | null }
  | { kind: "resuming"; upload: UploadSummary }
  | { kind: "sealed-stopped"; notice: string }
  | { kind: "resumed-published"; versionId: string }
  | { kind: "resume-error"; message: string };

type NewOperationStage =
  | { kind: "idle" }
  | { kind: "confirm"; upload: UploadSummary }
  | { kind: "publishing" }
  | { kind: "published"; versionId: string }
  | { kind: "error"; message: string };

function formatBytes(value: string | number): string {
  const bytes = typeof value === "number" ? value : Number(value) || 0;
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

async function collectPage<T>(fetch: (cursor?: string) => Promise<ListPage<T>>): Promise<{ items: T[]; exhausted: boolean }> {
  const items: T[] = [];
  let cursor: string | undefined;
  do {
    const page = await fetch(cursor);
    items.push(...page.items);
    cursor = page.next_cursor ?? undefined;
  } while (cursor);
  return { items, exhausted: true };
}

export function StorageScreen({ controller }: StorageScreenProps) {
  const client = controller.client;
  const [uploads, setUploads] = useState<UploadSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const reloadRequest = useRef(0);
  const [cursor, setCursor] = useState<string | null>(null);
  const [detail, setDetail] = useState<Record<string, UploadDetail>>({});
  const [expanded, setExpanded] = useState<Record<string, { entries: UploadEntryProgress[]; ranges: UploadConfirmedRange[]; pins: UploadPin[] }>>({});
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [statusSummary, setStatusSummary] = useState<string | null>(null);
  const [journals, setJournals] = useState<ImportJournal[]>([]);
  const [resume, setResume] = useState<ResumeStage>({ kind: "idle" });
  const [newOperation, setNewOperation] = useState<NewOperationStage>({ kind: "idle" });
  const [planned, setPlanned] = useState<PlannedInput | null>(null);
  const cancelRef = useRef(false);
  const engineRef = useRef<TransferEngine | null>(null);
  // Loaded protected state is scoped to the confirmed authority epoch; a
  // genuine library/principal/runtime-generation change clears it while a
  // routine same-authority recheck keeps the last-confirmed view.
  const epoch = `${controller.state.capabilities?.library_id ?? ""}|${controller.state.session?.principal_id ?? ""}|${controller.state.capabilities?.runtime_generation ?? ""}`;
  const epochRef = useRef(epoch);

  const engine = useCallback(() => {
    engineRef.current = engineRef.current ?? new TransferEngine(client, new BrowserJournalStore());
    return engineRef.current;
  }, [client]);

  const reload = useCallback(
    async (nextCursor: string | null) => {
      const request = ++reloadRequest.current;
      setLoading(true);
      setError(null);
      try {
        const page = await client.listUploads({ cursor: nextCursor ?? undefined });
        if (request !== reloadRequest.current) return;
        setUploads((previous) => (nextCursor === null ? page.items : [...previous, ...page.items]));
        setCursor(page.next_cursor);
        const store = new BrowserJournalStore();
        const session = controller.state.session;
        if (session) {
          const authority = journalAuthority(controller.state.capabilities?.library_id ?? "", session.principal_id);
          setJournals(store.load(authority).records);
        }
      } catch (cause) {
        if (request === reloadRequest.current) {
          setError(cause instanceof JournalReadError || cause instanceof JournalWriteError ? JOURNAL_UNAVAILABLE_NOTICE : cause instanceof ApiError ? `오류 ${cause.status} ${cause.code}` : cause instanceof Error ? cause.message : String(cause));
        }
      } finally {
        if (request === reloadRequest.current) setLoading(false);
      }
    },
    [client, epoch],
  );

  useEffect(() => {
    void reload(null);
    void client
      .serviceStatus()
      .then((status) => {
        const storage = (status as { storage?: { staging_bytes?: string; pending_upload_count?: number } }).storage;
        if (storage) {
          setStatusSummary(`스테이징 ${formatBytes(storage.staging_bytes ?? "0")} · 대기 업로드 ${storage.pending_upload_count ?? 0}개`);
        }
      })
      .catch(() => setStatusSummary(null));
  }, [client, reload]);

  useEffect(() => {
    if (epochRef.current === epoch) return;
    epochRef.current = epoch;
    setUploads([]);
    setCursor(null);
    setDetail({});
    setExpanded({});
    setJournals([]);
    setResume({ kind: "idle" });
    setNewOperation({ kind: "idle" });
    void reload(null);
    // reload identity changes with controller state; the epoch guard above is
    // the reset authority.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [epoch]);

  const toggleDetail = useCallback(
    async (upload: UploadSummary) => {
      if (detail[upload.upload_id]) {
        setDetail((previous) => {
          const next = { ...previous };
          delete next[upload.upload_id];
          return next;
        });
        setExpanded((previous) => {
          const next = { ...previous };
          delete next[upload.upload_id];
          return next;
        });
        return;
      }
      try {
        const value = await client.uploadDetail(upload.upload_id);
        setDetail((previous) => ({ ...previous, [upload.upload_id]: value }));
        const entries = await collectPage((pageCursor) => client.uploadEntries(upload.upload_id, pageCursor));
        const ranges = await collectPage((pageCursor) => client.uploadRanges(upload.upload_id, undefined, pageCursor));
        const pins = await collectPage((pageCursor) => client.uploadPins(upload.upload_id, pageCursor));
        setExpanded((previous) => ({ ...previous, [upload.upload_id]: { entries: entries.items, ranges: ranges.items, pins: pins.items } }));
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : String(cause));
      }
    },
    [client, detail],
  );

  const beginResume = useCallback((upload: UploadSummary, record: ImportJournal | null) => {
    setPlanned(null);
    setResume({ kind: "reselect", upload, owned: record !== null && !record.stagingOnly, record });
  }, []);

  const onFolderPicked = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
    const files = event.target.files;
    if (!files || files.length === 0) { setPlanned(null); return; }
    const first = files[0] as File & { webkitRelativePath?: string };
    const root = (first.webkitRelativePath ?? first.name).split("/")[0];
    setPlanned(planFromFolder([...files], root));
  }, []);

  const onArchivePicked = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) { setPlanned(null); return; }
    setPlanned(planFromArchive(file));
  }, []);

  const onSinglePicked = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) { setPlanned(null); return; }
    setPlanned(planFromSingleFile(file));
  }, []);

  const continueResume = useCallback(async () => {
    if (resume.kind !== "reselect" || !planned) return;
    const authority = controller.captureDispatchAuthority();
    const flowGuard = () => controller.requireVerifiedDispatch(authority);
    cancelRef.current = false;
    setResume({ kind: "resuming", upload: resume.upload });
    try {
      controller.requireVerifiedDispatch(authority);
      let record = resume.record;
      if (!record) {
        // Journal-less staging recovery: a fresh recovery record carries the
        // exact retained upload with a FRESH S for the deliberate completion;
        // publication intent is NOT reconstructed and stays unset.
        const value = await client.uploadDetail(resume.upload.upload_id);
        if (value.state !== "open") {
          setResume({ kind: "resume-error", message: "열린(open) 상태의 보관 업로드만 입력 재선택으로 이어갈 수 있습니다." });
          return;
        }
        const target = value.target as { project_id?: string; document_id?: string | null };
        if (!target.project_id || !(typeof target.document_id === "string" || target.document_id === null)) {
          setResume({ kind: "resume-error", message: "업로드 대상 정보를 확인할 수 없습니다." });
          return;
        }
        const now = new Date().toISOString();
        record = {
          stagingOnly: true,
          libraryId: controller.state.capabilities?.library_id ?? "",
          principalId: controller.state.session?.principal_id ?? "",
          uploadId: value.upload_id,
          createKey: null,
          completeKey: crypto.randomUUID(),
          importKey: null,
          target: { projectId: target.project_id, documentId: target.document_id },
          inputMode: value.input_mode as ImportJournal["inputMode"],
          selectionName: value.selection_name,
          phase: "transferring",
          entries: {},
          createdAt: now,
          updatedAt: now,
          jobId: null,
          versionId: null,
          completeCheckpoint: null,
        };
        engine().journal.save(record);
      }
      flowGuard();
      const finished = await runImportPipeline(client, engine(), record, planned, {
        guard: flowGuard,
        shouldContinue: () => !cancelRef.current,
        stopAtSealed: record.stagingOnly,
        onStage: () => undefined,
        onNotice: (value) => setNotice(value),
      });
      if (finished.phase === "sealed") {
        setResume({ kind: "sealed-stopped", notice: "복구가 봉인에서 중단되었습니다. 발행은 명시적 새 작업 확인으로만 진행됩니다." });
      } else if (finished.phase === "published" && finished.versionId) {
        setResume({ kind: "resumed-published", versionId: finished.versionId });
      } else {
        setResume({ kind: "resume-error", message: "이어가기가 봉인으로 끝나지 않았습니다. 원래 상태는 보존됩니다." });
      }
      await reload(null);
    } catch (cause) {
      if (cause instanceof DispatchBlockedError) {
        setResume({ kind: "resume-error", message: "확인된 연결 상태에서만 진행할 수 있습니다." });
        return;
      }
      setResume({ kind: "resume-error", message: cause instanceof Error ? cause.message : String(cause) });
    }
  }, [client, controller, engine, planned, reload, resume]);

  const reconcileJournal = useCallback(async (upload: UploadSummary, record: ImportJournal) => {
    const authority = controller.captureDispatchAuthority();
    const flowGuard = () => controller.requireVerifiedDispatch(authority);
    try {
      flowGuard();
      setResume({ kind: "resuming", upload });
      const current = await runImportPipeline(client, engine(), record, null, {
        guard: flowGuard,
        onStage: () => undefined,
        onNotice: setNotice,
      });
      flowGuard();
      if (current.phase === "published" && current.versionId) {
        setResume({ kind: "resumed-published", versionId: current.versionId });
      } else if (current.stagingOnly && current.phase === "sealed") {
        setResume({ kind: "sealed-stopped", notice: "봉인이 확인되었습니다. 원래 발행 의도는 알 수 없으며 자동 발행하지 않습니다." });
      } else {
        setResume({ kind: "idle" });
      }
      await reload(null);
    } catch (cause) {
      setResume({ kind: "resume-error", message: cause instanceof Error ? cause.message : String(cause) });
    }
  }, [client, controller, engine, reload]);

  const reconcileCreation = useCallback(async (record: ImportJournal) => {
    const authority = controller.captureDispatchAuthority();
    try {
      await engine().attachUpload(record, { guard: () => controller.requireVerifiedDispatch(authority) });
      controller.requireVerifiedDispatch(authority);
      await reload(null);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    }
  }, [controller, engine, reload]);

  const publishAsNewOperation = useCallback(async () => {
    if (newOperation.kind !== "confirm") return;
    const authority = controller.captureDispatchAuthority();
    const flowGuard = () => controller.requireVerifiedDispatch(authority);
    setNewOperation({ kind: "publishing" });
    try {
      controller.requireVerifiedDispatch(authority);
      const value = await client.uploadDetail(newOperation.upload.upload_id);
      if (value.state !== "sealed" || !value.reusable) {
        setNewOperation({ kind: "error", message: "봉인되었고 실제 재사용 가능한 업로드만 새 작업으로 발행할 수 있습니다." });
        return;
      }
      const target = value.target as { project_id?: string; document_id?: string | null };
      if (!target.project_id || !(typeof target.document_id === "string" || target.document_id === null)) {
        setNewOperation({ kind: "error", message: "업로드 대상 정보를 확인할 수 없습니다." });
        return;
      }
      const document: DocumentView | null = target.document_id === null ? null : await client.documentDetail(target.document_id);
      if (document && document.project_id !== target.project_id) throw new Error("보관 업로드와 대상 프로젝트가 일치하지 않습니다.");
      if (document && document.latest_version_id === null) throw new Error("이 대상에는 기준이 될 발행 버전이 없습니다.");
      flowGuard();
      // An explicit NEW operation: new I with a freshly captured base on the
      // exact same target, reusing the retained sealed payload. The original
      // operation's uncertainty is preserved, never resolved by declaration.
      const now = new Date().toISOString();
      const record: ImportJournal = {
        stagingOnly: false,
        libraryId: controller.state.capabilities?.library_id ?? "",
        principalId: controller.state.session?.principal_id ?? "",
        uploadId: value.upload_id,
        createKey: null,
        completeKey: crypto.randomUUID(),
        importKey: crypto.randomUUID(),
        target: { projectId: target.project_id, documentId: target.document_id, baseVersionId: document?.latest_version_id ?? null },
        inputMode: value.input_mode as ImportJournal["inputMode"],
        selectionName: value.selection_name,
        phase: "sealed",
        entries: {},
        createdAt: now,
        updatedAt: now,
        jobId: null,
        versionId: null,
        completeCheckpoint: null,
      };
      engine().journal.save(record);
      const published = await engine().publish(record, { guard: flowGuard });
      if (published.phase === "published" && published.versionId) {
        setNewOperation({ kind: "published", versionId: published.versionId });
      } else {
        setNewOperation({ kind: "error", message: "발행 결과가 확정되지 않았습니다." });
      }
    } catch (cause) {
      setNewOperation({ kind: "error", message: cause instanceof Error ? cause.message : String(cause) });
    }
  }, [client, controller, engine, newOperation]);

  const retryFailedCompletion = useCallback(async (record: ImportJournal) => {
    const authority = controller.captureDispatchAuthority();
    try {
      await engine().prepareCompletionRetry(record, { guard: () => controller.requireVerifiedDispatch(authority) });
      await reload(null);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    }
  }, [controller, engine, reload]);

  const abandonUpload = useCallback(
    async (upload: UploadSummary) => {
      const authority = controller.captureDispatchAuthority();
      try {
        controller.requireVerifiedDispatch(authority);
        await client.abandonUpload(upload.upload_id, crypto.randomUUID());
        setNotice("업로드를 폐기했습니다. 채택된 참조와 활성 핀은 보존됩니다.");
        await reload(null);
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : String(cause));
      }
    },
    [client, controller, reload],
  );

  const cancelSeal = useCallback(
    async (upload: UploadSummary) => {
      const authority = controller.captureDispatchAuthority();
      const flowGuard = () => controller.requireVerifiedDispatch(authority);
      try {
        const value = detail[upload.upload_id] ?? (await client.uploadDetail(upload.upload_id));
        if (!value.seal_job_id) return;
        // The synchronous gate runs immediately before the actual dispatch.
        flowGuard();
        await client.jobCancel(value.seal_job_id, crypto.randomUUID());
        setNotice("봉인 검증 취소를 요청했습니다. 작업자 정지가 확인되면 업로드가 열린 상태로 돌아갑니다.");
        // A confirmed return to open resets completion for the OWNED journal
        // (deliberate NEW S); unknown outcomes elsewhere keep the same S.
        for (;;) {
          const current = await client.uploadDetail(upload.upload_id);
          if (current.state === "open" || current.state === "sealed" || current.state === "abandoned") {
            if (current.state === "open") {
              for (const record of journals.filter((item) => item.uploadId === upload.upload_id)) {
                if (record.phase === "completing" || record.phase === "sealing") engine().markSealSettled(record);
              }
              setNotice("봉인이 열린 상태로 확정되었습니다. 이어하기는 새로운 완료 키로 진행됩니다.");
            }
            await reload(null);
            return;
          }
          await new Promise((resolve) => setTimeout(resolve, 300));
        }
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : String(cause));
      }
    },
    [client, controller, detail, engine, journals, reload],
  );

  return (
    <section className="panel" data-testid="storage-screen">
      <h1>설정 · 저장소</h1>
      {statusSummary ? (
        <p className="hint" data-testid="storage-status">
          {statusSummary}
        </p>
      ) : null}
      {notice ? (
        <p className="hint" role="status">
          {notice}
        </p>
      ) : null}
      {error ? (
        <p className="hint" role="alert">
          {error}
        </p>
      ) : null}
      <h2>보관된 업로드</h2>
      {journals.filter((record) => !record.stagingOnly && record.uploadId === null && record.phase === "prepared").map((record) => (
        <p className="hint" key={journalKey(record)}>
          생성 확인이 필요한 작업: {record.selectionName} · {record.importKey} · 대상 {record.target.documentId ?? "새 문서"}
          <button type="button" onClick={() => void reconcileCreation(record)}>원래 생성 작업 결과 확인</button>
        </p>
      ))}
      <p className="hint">브라우저를 닫아도 업로드는 폐기되지 않으며, 보관 기간으로 자동 만료되지 않습니다.</p>
      {loading ? <p className="hint" role="status">업로드를 불러오는 중…</p> : null}
      {uploads.length === 0 && !loading && !error ? (
        <p className="hint" data-testid="storage-empty">
          보관된 업로드가 없습니다.
        </p>
      ) : null}
      <ul className="document-list">
        {uploads.map((upload) => {
          const value = detail[upload.upload_id];
          const expansion = expanded[upload.upload_id];
          const records = journals.filter((record) => record.uploadId === upload.upload_id);
          const intents = records.filter((record) => !record.stagingOnly);
          const recoveries = records.filter((record) => record.stagingOnly);
          const owned = intents.length > 0;
          return (
            <li key={upload.upload_id} data-testid="storage-upload" data-upload={upload.upload_id}>
              <div>
                <strong>{upload.selection_name ?? "(이름 없음)"}</strong>
                <span className="hint">
                  {" "}
                  · {upload.state} · {upload.purpose} · 입력 {upload.input_mode} · 확인됨 {formatBytes(upload.byte_size)} / 스테이징 {formatBytes(upload.staging_bytes)} · 갱신 {upload.updated_at}
                  {upload.reusable ? " · 재사용 가능" : upload.unavailable_reason ? ` · 재사용 불가(${upload.unavailable_reason})` : ""}
                </span>
              </div>
              <div className="hint">
                대상: <span className="muted-code">{JSON.stringify(upload.target)}</span>
              </div>
              <div className="hint">
                핀 요약: {Object.entries(upload.pin_summary).map(([kind, count]) => `${kind} ${count}`).join(", ") || "없음"}
                {owned ? " · 이 브라우저의 저널이 원래 U/S/I와 기준을 보유합니다" : " · 이 브라우저에는 원래 발행 의도 저널이 없습니다"}
              </div>
              <div className="form-row">
                <button type="button" onClick={() => void toggleDetail(upload)}>
                  상세(항목·범위·핀) 보기
                </button>
                {upload.purpose === "result" && upload.state === "open" && !owned && recoveries.length === 0 ? (
                  <button type="button" className="primary" onClick={() => beginResume(upload, null)}>
                    입력 재선택해 스테이징 이어가기 (봉인에서 중단)
                  </button>
                ) : null}
                {records.map((record) => (
                  <span key={journalKey(record)} className="form-row">
                    <span className="hint">{record.stagingOnly ? "스테이징 복구" : `원래 작업 ${record.importKey} · 기준 ${record.target.baseVersionId ?? "없음"}`} · {record.phase}</span>
                    {upload.state === "open" && (record.phase === "transferring" || record.phase === "transfer-cancelled") ? (
                      <button type="button" onClick={() => beginResume(upload, record)}>
                        {record.stagingOnly ? "스테이징 이어가기 (봉인에서 중단)" : "이어서 전송 (재선택 후 범위 재검증)"}
                      </button>
                    ) : record.phase === "completing" || record.phase === "sealing" || record.phase === "sealed" || record.phase === "dispatching" || (record.phase !== "published" && (upload.state === "sealed" || upload.state === "sealing")) ? (
                      <button type="button" onClick={() => void reconcileJournal(upload, record)}>보관된 작업 결과 확인</button>
                    ) : null}
                    {record.phase === "completing" && upload.state === "open" ? <button type="button" onClick={() => void retryFailedCompletion(record)}>실패가 확인된 봉인 요청 다시 준비</button> : null}
                  </span>
                ))}
                {upload.purpose === "result" && !owned && upload.state === "sealed" && upload.reusable ? (
                  <button type="button" onClick={() => setNewOperation({ kind: "confirm", upload })}>
                    명시적 새 작업으로 발행
                  </button>
                ) : null}
                {upload.state === "sealing" ? (
                  <button type="button" onClick={() => void cancelSeal(upload)}>
                    봉인 검증 취소 (입력 유지)
                  </button>
                ) : null}
                {upload.state !== "abandoned" ? (
                  <button type="button" onClick={() => void abandonUpload(upload)}>
                    폐기 (Abandon)
                  </button>
                ) : null}
              </div>
              {value ? (
                <div className="data-readout">
                  체크포인트 세대 {value.checkpoint.generation} · 확인 바이트 {formatBytes(value.checkpoint.accepted_bytes)} · 완전 입력 {String(value.checkpoint.complete_input)}
                  {value.error ? ` · 오류 ${value.error}` : ""}
                  {value.seal_job_id ? ` · 봉인 작업 ${value.seal_job_id}` : ""}
                </div>
              ) : null}
              {expansion ? (
                <div className="data-readout" data-testid="storage-upload-detail">
                  <div>항목 {expansion.entries.length}개</div>
                  <div>{expansion.entries.map((entry) => `${entry.complete ? "✔" : "…"} ${entry.kind[0]} ${entry.path} (${entry.accepted_bytes}/${entry.byte_size}B)`).join("\n")}</div>
                  <div>확인 범위 {expansion.ranges.length}개</div>
                  <div>{expansion.ranges.map((range) => `${range.path}:${range.offset}+${range.byte_size}`).join("\n")}</div>
                  <div>핀 {expansion.pins.length}개</div>
                  <div>{expansion.pins.map((pin) => `${pin.kind}@${pin.owner_id}`).join("\n")}</div>
                </div>
              ) : null}
              {!owned ? (
                <p className="hint">
                  원래 클라이언트의 저널이 없어 자동 발행은 불가능합니다. 열린 스테이징은 입력 재선택 후 범위 재검증으로 봉인까지만 이어갈 수 있고, 발행은 명시적으로 확인된 새 작업으로만 수행됩니다. 브라우저 폴더 재선택(FileList)은 빈 디렉터리·심볼릭 링크를 표현하지 못하며, 원본 입력이 그것들을 포함하면 CLI 경로로 정확한 입력을 제공하세요.
                </p>
              ) : null}
            </li>
          );
        })}
      </ul>
      {cursor !== null ? (
        <div className="form-row">
          <button type="button" onClick={() => void reload(cursor)}>
            업로드 더 불러오기
          </button>
        </div>
      ) : null}
      {resume.kind === "reselect" ? (
        <div className="data-readout" data-testid="storage-resume">
          <div>
            {resume.upload.selection_name ?? resume.upload.upload_id} —{" "}
            {resume.owned ? "원래 작업(U/S/I·기준)을 그대로 사용합니다." : "원래 발행 의도 없이 스테이징만 이어갑니다(새 완료 키, 봉인에서 중단)."} 입력을 다시 선택하세요.
            {resume.upload.input_mode === "directory" ? " 폴더 재선택(FileList)은 빈 디렉터리/심볼릭 링크를 표현하지 못합니다." : ""}
            원래 입력을 다시 선택하세요. 원본 전체 다이제스트가 없는 미전송 구간은 원본과 같은지 확인할 수 없습니다.
          </div>
          <div className="form-row">
            <label>
              폴더 재선택
              <input
                type="file"
                multiple
                {...({ webkitdirectory: "", directory: "" } as React.InputHTMLAttributes<HTMLInputElement>)}
                onChange={onFolderPicked}
              />
            </label>
            <label>
              ZIP 재선택
              <input type="file" accept=".zip" onChange={onArchivePicked} />
            </label>
            <label>
              단일 파일 재선택
              <input type="file" accept=".html,.htm" onChange={onSinglePicked} />
            </label>
            <button type="button" className="primary" disabled={!planned} onClick={() => void continueResume()}>
              범위 재검증 후 이어가기
            </button>
          </div>
        </div>
      ) : null}
      {resume.kind === "resuming" ? (
        <p className="hint" role="status">
          이어가기 중… 미확정 구간과 원본 다이제스트를 알 수 없는 경우 혼합 입력 가능성 고지가 표시됩니다.
        </p>
      ) : null}
      {resume.kind === "sealed-stopped" ? (
        <p className="hint" role="status" data-testid="storage-resume-sealed-stopped">
          {resume.notice}
        </p>
      ) : null}
      {resume.kind === "resumed-published" ? (
        <p className="hint" role="status" data-testid="storage-resume-published">
          이어서 진행이 완료되었습니다. 발행된 버전: <span className="muted-code">{resume.versionId}</span>
        </p>
      ) : null}
      {resume.kind === "resume-error" ? (
        <p className="hint" role="alert">
          {resume.message}
        </p>
      ) : null}
      {newOperation.kind === "confirm" ? (
        <div className="data-readout" data-testid="storage-new-operation">
          <p className="hint" role="alert">
            이 브라우저에 원래 발행 의도가 없습니다. 명시적인 새 작업(새 작업 ID, 새로 확인한 기준, 동일 대상)으로 발행하시겠습니까? 원래 작업의 불확실성은 이 선언으로 해소되지 않습니다.
          </p>
          <div className="form-row">
            <button type="button" className="primary" onClick={() => void publishAsNewOperation()}>
              새 작업으로 발행 확인
            </button>
            <button type="button" onClick={() => setNewOperation({ kind: "idle" })}>
              취소
            </button>
          </div>
        </div>
      ) : null}
      {newOperation.kind === "publishing" ? (
        <p className="hint" role="status">
          새 작업 발행 확인 중…
        </p>
      ) : null}
      {newOperation.kind === "published" ? (
        <p className="hint" role="status" data-testid="storage-new-operation-published">
          새 작업으로 발행되었습니다. 버전: <span className="muted-code">{newOperation.versionId}</span>
        </p>
      ) : null}
      {newOperation.kind === "error" ? (
        <p className="hint" role="alert">
          {newOperation.message}
        </p>
      ) : null}
    </section>
  );
}
