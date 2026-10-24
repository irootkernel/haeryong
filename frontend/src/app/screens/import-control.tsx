// Shared import control. New document is the default; Existing document
// captures and displays the exact published base before upload; an absent
// published base cannot start an existing-document import.
// Folder (FileList), ZIP and single-HTML inputs run through one engine with
// acknowledgement-based progress, transfer Cancel that retains
// staging/journal/intent, explicit Resume with full reselection range
// verification, distinct seal-verification cancel and Abandon, and stale-base
// VERSION_CONFLICT resolved only by an explicit owner decision. A journal
// whose create acknowledgement was lost replays its original U instead of
// starting a new operation. The labelled Reader/Versions preselection
// harness feeds this same control with a locked document context.
import { useCallback, useEffect, useRef, useState } from "react";
import type { ShellController } from "../../client/controller.ts";
import { BrowserJournalStore, DispatchRecordError, ImportJobError, JournalReadError, JournalWriteError, TransferEngine, journalAuthority, planFromArchive, planFromFolder, planFromSingleFile } from "../../client/transfer.ts";
import type { ImportJournal, PlannedInput, TransferProgress } from "../../client/transfer.ts";
import { ApiError, DispatchBlockedError } from "../../client/types.ts";
import type { DocumentView, ProjectView } from "../../client/types.ts";
import { planFromNativeSource, verifyNativeSourceStable } from "../../client/native-fs.ts";
import type { NativeFsClient } from "../../client/native-fs.ts";
import { JOURNAL_UNAVAILABLE_NOTICE, runImportPipeline } from "./pipeline.ts";

export interface PreselectedDocument {
  projectId: string;
  documentId: string;
}

interface ImportControlProps {
  controller: ShellController;
  projects: ProjectView[];
  documents: DocumentView[];
  /** Reader/Versions harness context: locks the exact document and base. */
  preselected?: PreselectedDocument;
  /** Publication notification (e.g. list refresh); the receipt stays visible. */
  onFinished?: (documentId: string, versionId: string) => void;
  /** Explicit owner dismissal of the control once the receipt is visible. */
  onClose?: () => void;
}

type Stage =
  | { kind: "configure" }
  | { kind: "transferring"; journal: ImportJournal }
  | { kind: "cancelled"; journal: ImportJournal }
  | { kind: "sealing"; journal: ImportJournal }
  | { kind: "sealed"; journal: ImportJournal }
  | { kind: "publishing"; journal: ImportJournal }
  | { kind: "published"; journal: ImportJournal }
  | { kind: "stale-base"; journal: ImportJournal }
  | { kind: "mismatch"; journal: ImportJournal | null; detail: string }
  | { kind: "error"; journal: ImportJournal | null; message: string };

type BaseState =
  | { kind: "unselected" }
  | { kind: "resolving" }
  | { kind: "resolved"; base: string | null }
  | { kind: "failed"; message: string };

function formatBytes(bytes: number | null): string {
  if (bytes === null) return "크기 집계 중";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

function phaseLabel(kind: Stage["kind"]): string {
  switch (kind) {
    case "configure":
      return "준비";
    case "transferring":
      return "전송 중";
    case "cancelled":
      return "전송 취소됨(스테이징 유지)";
    case "sealing":
      return "봉인 검증 중";
    case "sealed":
      return "봉인 완료";
    case "publishing":
      return "발행 확인 중";
    case "published":
      return "발행 완료";
    case "stale-base":
      return "기준 버전 충돌";
    case "mismatch":
      return "재선택 불일치";
    case "error":
      return "오류";
  }
}

export function ImportControl({ controller, projects, documents, preselected, onFinished, onClose }: ImportControlProps) {
  const client = controller.client;
  const nativeFs = typeof (client as unknown as Partial<NativeFsClient>).chooseImportSource === "function" ? (client as unknown as NativeFsClient) : null;
  const [stage, setStage] = useState<Stage>({ kind: "configure" });
  const [progress, setProgress] = useState<TransferProgress | null>(null);
  const [recoveryNotice, setRecoveryNotice] = useState<string | null>(null);
  const [mode, setMode] = useState<"new" | "existing">(preselected ? "existing" : "new");
  const [projectId, setProjectId] = useState(preselected ? preselected.projectId : projects[0]?.project_id ?? "");
  const [documentId, setDocumentId] = useState(preselected?.documentId ?? "");
  const [base, setBase] = useState<BaseState>(preselected ? { kind: "resolving" } : { kind: "unselected" });
  const [planned, setPlanned] = useState<PlannedInput | null>(null);
  const [limitsNote, setLimitsNote] = useState<string | null>(null);
  const [documentQuery, setDocumentQuery] = useState("");
  const [documentChoices, setDocumentChoices] = useState<DocumentView[]>(documents);
  const [documentCursor, setDocumentCursor] = useState<string | null>(null);
  const [recovery, setRecovery] = useState<ImportJournal | null>(null);
  const cancelRef = useRef(false);
  const engineRef = useRef<TransferEngine | null>(null);
  const journalRef = useRef<ImportJournal | null>(null);
  const startingRef = useRef(false);
  const baseRequestRef = useRef(0);
  useEffect(() => {
    cancelRef.current = false;
    return () => { cancelRef.current = true; };
  }, []);

  // Resolve and display the exact pre-transfer base whenever the selected
  // existing document changes; races resolve to the latest selection only.
  const resolveBase = useCallback(
    async (chosen: string) => {
      if (!chosen) {
        setBase({ kind: "unselected" });
        return;
      }
      const request = baseRequestRef.current + 1;
      baseRequestRef.current = request;
      setBase({ kind: "resolving" });
      try {
        const detail = await client.documentDetail(chosen);
        if (baseRequestRef.current !== request) return;
        setBase({ kind: "resolved", base: detail.latest_version_id });
      } catch (cause) {
        if (baseRequestRef.current !== request) return;
        setBase({ kind: "failed", message: cause instanceof Error ? cause.message : String(cause) });
      }
    },
    [client],
  );

  useEffect(() => {
    if (preselected) void resolveBase(preselected.documentId);
  }, [preselected?.documentId, preselected?.projectId, resolveBase]);

  // The projects prop can arrive after mount (the Library loads it once per
  // mount/epoch): the mount-time initializer then captured ''. Fill the
  // default target exactly once when the list arrives and no choice exists
  // yet — a preselected target and an explicit owner choice (any non-empty
  // projectId) are never reset, and later list identities never override a
  // selection.
  useEffect(() => {
    if (preselected) return;
    setProjectId((current) => (current === "" && projects.length > 0 ? projects[0].project_id : current));
  }, [preselected, projects]);

  // An interrupted create whose acknowledgement was lost: the original U is
  // replayed, never replaced with a new begin/new identity. Reload recovery
  // also covers retained records already past transfer (same S / same I).
  useEffect(() => {
    const session = controller.state.session;
    const libraryId = controller.state.capabilities?.library_id;
    if (!session || !libraryId) return;
    try {
      const bucket = new BrowserJournalStore().load(journalAuthority(libraryId, session.principal_id));
      const resumable = bucket.records.find(
        (record) =>
          !record.stagingOnly && record.importKey === bucket.active &&
          (!preselected || (record.target.projectId === preselected.projectId && record.target.documentId === preselected.documentId)) && (
          (record.phase === "prepared" && record.uploadId === null) ||
          record.phase === "completing" ||
          record.phase === "sealing" ||
          record.phase === "sealed" ||
          record.phase === "dispatching"),
      );
      setRecovery(resumable ?? null);
    } catch {
      setRecovery(null);
      setStage({ kind: "error", journal: null, message: JOURNAL_UNAVAILABLE_NOTICE });
    }
  }, [controller.state.capabilities?.library_id, controller.state.session?.principal_id, preselected?.projectId, preselected?.documentId]);

  const onDocumentChange = useCallback(
    (event: React.ChangeEvent<HTMLSelectElement>) => {
      const chosen = event.target.value;
      setDocumentId(chosen);
      void resolveBase(chosen);
    },
    [resolveBase],
  );

  // The existing-document picker searches and paginates the real eligible
  // set instead of showing raw IDs from one Library page; stale responses
  // from superseded project/query choices never overwrite the current set.
  const searchRequestRef = useRef(0);
  const searchDocuments = useCallback(
    async (query: string, cursor: string | null) => {
      const request = searchRequestRef.current + 1;
      searchRequestRef.current = request;
      try {
        const page = await client.listDocuments({ projectId: projectId || undefined, lifecycle: "active", cursor: cursor ?? undefined });
        if (searchRequestRef.current !== request) return;
        setDocumentChoices((previous) => {
          const known = new Map((cursor === null ? [] : previous).map((document) => [document.document_id, document]));
          for (const item of page.items) {
            if (!query || item.title.includes(query)) known.set(item.document_id, item);
          }
          return [...known.values()];
        });
        setDocumentCursor(page.next_cursor);
      } catch {
        if (searchRequestRef.current === request && cursor === null) setDocumentChoices([]);
      }
    },
    [client, projectId],
  );

  useEffect(() => {
    if (mode !== "existing") return;
    void searchDocuments("", null);
  }, [mode, searchDocuments]);

  // Native owner-selected source (TASK-003-05 M3): the opaque Go handle
  // backs one PlannedInput through the SAME engine; the handle stays owned
  // for reselection verification until an explicit new selection, unmount
  // or publication releases it. Late picks are epoch-guarded so a result
  // arriving after the control left the screen (or a newer choice) never
  // owns a handle or resurrects UI state.
  const [nativePicking, setNativePicking] = useState(false);
  const [nativeMode, setNativeMode] = useState<"directory" | "archive" | "file">("directory");
  const nativeSourceRef = useRef<string | null>(null);
  const mountedRef = useRef(true);
  const pickEpochRef = useRef(0);
  // Synchronous pending-ownership token: exactly one native pick in flight.
  const nativePickingRef = useRef<symbol | null>(null);
  const releaseNativeSource = useCallback(
    async (next: string | null) => {
      const previous = nativeSourceRef.current;
      nativeSourceRef.current = next;
      if (previous && previous !== next && nativeFs) {
        try {
          await nativeFs.releaseSource(previous);
        } catch {
          // A failed release is typed by Go; the handle dies with its era.
        }
      }
    },
    [nativeFs],
  );
  useEffect(() => {
    // StrictMode-safe lifecycle: each effect (re)mount restores liveness and
    // invalidates in-flight picks; only the real unmount releases the handle.
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      pickEpochRef.current += 1;
      void releaseNativeSource(null);
    };
  }, [releaseNativeSource]);

  const onFolderPicked = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
    const files = event.target.files;
    // EVERY browser input path — including an emptied/cancelled file dialog
    // — is a new selection intent: it synchronously invalidates any in-flight
    // native pick (epoch) and releases a previously owned native handle.
    pickEpochRef.current += 1;
    void releaseNativeSource(null);
    setLimitsNote("폴더 선택(FileList)은 빈 디렉터리와 심볼릭 링크를 표현하지 못합니다. 전체 트리 보존이 필요하면 ZIP 경로를 사용하세요.");
    if (!files || files.length === 0) {
      setPlanned(null);
      return;
    }
    const first = files[0] as File & { webkitRelativePath?: string };
    const root = (first.webkitRelativePath ?? first.name).split("/")[0];
    setPlanned(planFromFolder([...files], root));
  }, [releaseNativeSource]);

  const onArchivePicked = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
    const files = event.target.files;
    pickEpochRef.current += 1;
    void releaseNativeSource(null);
    const file = files?.[0];
    if (!file) { setPlanned(null); setLimitsNote("선택된 파일이 없습니다. 가져올 입력을 다시 선택하세요."); return; }
    setPlanned(planFromArchive(file));
    setLimitsNote("ZIP 입력은 디렉터리 항목과 링크를 포함해 전체 트리를 보존합니다.");
  }, [releaseNativeSource]);

  const onSinglePicked = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
    const files = event.target.files;
    pickEpochRef.current += 1;
    void releaseNativeSource(null);
    const file = files?.[0];
    if (!file) { setPlanned(null); setLimitsNote("선택된 파일이 없습니다. 가져올 입력을 다시 선택하세요."); return; }
    setPlanned(planFromSingleFile(file));
    setLimitsNote("단일 파일 가져오기는 선택한 파일만 포함합니다. 관련 파일이 필요하면 폴더/ZIP을 사용하세요.");
  }, [releaseNativeSource]);

  const onNativePicked = useCallback(
    async (mode: "directory" | "archive" | "file") => {
      if (!nativeFs) return;
      // Synchronous pending ownership: exactly one native pick is in flight;
      // a duplicate click is rejected before any state or ref mutation.
      const pendingToken = Symbol("native-pick");
      if (nativePickingRef.current !== null) return;
      nativePickingRef.current = pendingToken;
      // Each native pick is a new selection intent (invalidates older picks).
      const epoch = ++pickEpochRef.current;
      setNativePicking(true);
      try {
        const planned = await planFromNativeSource(nativeFs, mode);
        if (!mountedRef.current || epoch !== pickEpochRef.current) {
          // The control left the screen or a newer choice superseded this
          // one: release ONLY this late result's handle; no UI state changes.
          if (!("canceled" in planned)) {
            try {
              await nativeFs.releaseSource(planned.handle);
            } catch {
              // Typed by Go; the handle dies with its era.
            }
          }
          return;
        }
        if ("canceled" in planned) {
          setLimitsNote("네이티브 선택이 취소되었습니다.");
          return;
        }
        // Installs the new handle in the ref, then awaits the PREVIOUS
        // handle's release — unmount or a newer intent can happen during
        // that await, so the post-await state is rechecked below before
        // any UI record.
        await releaseNativeSource(planned.handle);
        if (!mountedRef.current || epoch !== pickEpochRef.current) {
          // Stale after the awaited release: clean up ONLY this flow's own
          // handle and only if it is still the owned one — a newer intent
          // (browser pick, empty input, unmount, restart) already cleared
          // the ref and released it; a stale flow never touches a newer
          // selection, records no UI and starts no new operation.
          if (nativeSourceRef.current === planned.handle) {
            void releaseNativeSource(null);
          }
          return;
        }
        setPlanned(planned.input);
        setLimitsNote("네이티브 선택은 빈 디렉터리와 심볼릭 링크를 포함해 선택한 트리를 그대로 반영합니다.");
      } catch (error) {
        // The same staleness predicate as the success path: an error from a
        // superseded pick (browser B / empty input changed the epoch) must
        // not overwrite the CURRENT selection's limits note or any UI state.
        if (mountedRef.current && epoch === pickEpochRef.current) {
          setLimitsNote(error instanceof Error ? error.message : String(error));
        }
      } finally {
        // Only the current owner clears the pending latch: a stale flow
        // never clears a newer pick's busy state.
        if (nativePickingRef.current === pendingToken) {
          nativePickingRef.current = null;
          if (mountedRef.current) setNativePicking(false);
        }
      }
    },
    [nativeFs, releaseNativeSource],
  );
  const nativeVerifyGate = useCallback((): (() => Promise<void>) | undefined => {
    const owned = nativeSourceRef.current;
    if (!owned || !nativeFs) return undefined;
    // The gate runs ONLY when the shared engine reaches its first new S;
    // building hooks never triggers a verification.
    return () => verifyNativeSourceStable(nativeFs, owned);
  }, [nativeFs]);

  const runPipeline = useCallback(
    async (journal: ImportJournal, input: PlannedInput | null, flowGuard?: () => void) => {
      const engine = engineRef.current;
      if (!engine) return;
      const useGuard = flowGuard ?? (() => controller.requireVerifiedDispatch());
      const current = await runImportPipeline(client, engine, journal, input, {
        guard: useGuard,
        onProgress: setProgress,
        shouldContinue: () => !cancelRef.current,
        verifyInputStable: nativeVerifyGate(),
        onStage: (stageKind, record) => {
          journalRef.current = record;
          if (stageKind === "abandoned") {
            setStage({ kind: "error", journal: record, message: "업로드가 폐기되어 이 흐름을 계속할 수 없습니다." });
            return;
          }
          const mapped =
            stageKind === "published"
              ? "published"
              : stageKind === "publishing"
                ? "publishing"
                : stageKind === "sealing"
                  ? "sealing"
                  : stageKind === "sealed"
                    ? "sealed"
                    : stageKind === "cancelled"
                      ? "cancelled"
                      : "transferring";
          setStage({ kind: mapped, journal: record });
        },
        onNotice: setRecoveryNotice,
      });
      if (current.phase === "published" && current.versionId) {
        void releaseNativeSource(null);
        onFinished?.(current.target.documentId ?? "", current.versionId);
      }
    },
    [client, controller, onFinished],
  );

  const handlePipelineError = useCallback((error: unknown, journal: ImportJournal | null) => {
    if (error instanceof JournalReadError || error instanceof JournalWriteError) {
      setStage({ kind: "error", journal, message: JOURNAL_UNAVAILABLE_NOTICE });
      return;
    }
    if (error instanceof DispatchBlockedError) {
      setStage({ kind: "error", journal, message: "확인된 연결 상태에서만 진행할 수 있습니다. 다시 연결 후 시도하세요." });
      return;
    }
    if ((error instanceof ApiError && error.status === 409 && error.code === "VERSION_CONFLICT") || (error instanceof ImportJobError && error.code === "VERSION_CONFLICT")) {
      setStage(journal ? { kind: "stale-base", journal } : { kind: "error", journal, message: "기준 버전 충돌(VERSION_CONFLICT)." });
      return;
    }
    const name = (error as { name?: string })?.name ?? "";
    if (name === "TransferCancelled") {
      setStage(journal ? { kind: "cancelled", journal } : { kind: "error", journal, message: "전송이 취소되었습니다." });
      return;
    }
    if (name === "ReselectionMismatchError") {
      setStage({ kind: "mismatch", journal, detail: (error as { detail?: string }).detail ?? "재선택한 입력이 보관된 바이트와 일치하지 않습니다." });
      return;
    }
    setStage({ kind: "error", journal, message: error instanceof Error ? error.message : String(error) });
  }, []);

  const start = useCallback(
    async (resumeRecord?: ImportJournal) => {
      if (!planned || startingRef.current) return;
      startingRef.current = true;
      setRecoveryNotice(null);
      // One immutable authority per flow: every guard in this flow rechecks
      // the same captured owner/library/generation, so a phase that turned
      // verified under a DIFFERENT authority never unblocks this flow.
      const authority = controller.captureDispatchAuthority();
      const flowGuard = () => controller.requireVerifiedDispatch(authority);
      cancelRef.current = false;
      const store = new BrowserJournalStore();
      engineRef.current = new TransferEngine(client, store);
      try {
        controller.requireVerifiedDispatch(authority);
        let journal: ImportJournal;
        if (resumeRecord) {
          journal = resumeRecord;
        } else {
          const target =
            mode === "existing" && documentId && base.kind === "resolved"
              ? { kind: "existing" as const, projectId, documentId, baseVersionId: base.base }
              : { kind: "new" as const, projectId };
          const session = controller.state.session;
          try {
            journal = await engineRef.current.begin(controller.state.capabilities?.library_id ?? "", session?.principal_id ?? "", target, planned, { guard: flowGuard });
          } catch (error) {
            if (error instanceof DispatchRecordError) {
              // The exact saved prepared record (original U) is retained for
              // this attempt's recovery; no new begin/identity is started.
              journalRef.current = error.journal;
              setRecovery(error.journal);
              throw error.cause ?? error;
            }
            throw error;
          }
        }
        journalRef.current = journal;
        setRecovery(null);
        setStage({ kind: "transferring", journal });
        await runPipeline(journal, planned, flowGuard);
      } catch (error) {
        handlePipelineError(error, journalRef.current);
      } finally {
        startingRef.current = false;
      }
    },
    [base, client, controller, documentId, handlePipelineError, mode, planned, projectId, runPipeline],
  );

  const continueRecovered = useCallback(async () => {
    if (!recovery) return;
    // Records already past transfer need no reselected input; a prepared
    // record without an upload id replays the original U with input present.
    const needsInput = recovery.phase === "prepared";
    if (needsInput && !planned) return;
    const authority = controller.captureDispatchAuthority();
    const flowGuard = () => controller.requireVerifiedDispatch(authority);
    engineRef.current = engineRef.current ?? new TransferEngine(client, new BrowserJournalStore());
    cancelRef.current = false;
    try {
      const engine = engineRef.current;
      const journal = needsInput ? await engine.attachUpload(recovery, { guard: flowGuard }) : recovery;
      journalRef.current = journal;
      setRecovery(null);
      setStage({ kind: "transferring", journal });
      await runPipeline(journal, planned, flowGuard);
    } catch (error) {
      handlePipelineError(error, journalRef.current);
    }
  }, [client, controller, handlePipelineError, planned, recovery, runPipeline]);

  const cancelTransfer = useCallback(() => {
    cancelRef.current = true;
  }, []);

  const retryFailedCompletion = useCallback(async () => {
    const record = journalRef.current;
    if (!record || !engineRef.current) return;
    const authority = controller.captureDispatchAuthority();
    try {
      const current = await engineRef.current.prepareCompletionRetry(record, { guard: () => controller.requireVerifiedDispatch(authority) });
      journalRef.current = current;
      setStage({ kind: "cancelled", journal: current });
    } catch (error) {
      handlePipelineError(error, record);
    }
  }, [controller, handlePipelineError]);

  const resumeTransfer = useCallback(async () => {
    const journal = stage.kind === "cancelled" ? stage.journal : journalRef.current;
    if (!journal || !planned || !engineRef.current) return;
    const authority = controller.captureDispatchAuthority();
    const flowGuard = () => controller.requireVerifiedDispatch(authority);
    cancelRef.current = false;
    try {
      // Resume always re-verifies every actual server-confirmed range
      // against the reselected input before sending the exact gaps.
      setStage({ kind: "transferring", journal });
      const resumed = await engineRef.current.resume(journal, planned, {
        guard: flowGuard,
        onProgress: setProgress,
        shouldContinue: () => !cancelRef.current,
        verifyInputStable: nativeVerifyGate(),
      });
      journalRef.current = resumed;
      await runPipeline(resumed, planned, flowGuard);
    } catch (error) {
      handlePipelineError(error, journalRef.current);
    }
  }, [controller, handlePipelineError, planned, runPipeline, stage]);

  const abandon = useCallback(async () => {
    const journal = journalRef.current;
    if (!journal?.uploadId) return;
    const authority = controller.captureDispatchAuthority();
    try {
      controller.requireVerifiedDispatch(authority);
      await client.abandonUpload(journal.uploadId, crypto.randomUUID());
      setStage({ kind: "error", journal: null, message: "업로드를 폐기했습니다. 보관된 스테이징은 더 이상 재사용되지 않습니다." });
      journalRef.current = null;
    } catch (error) {
      handlePipelineError(error, journal);
    }
  }, [client, controller, handlePipelineError]);

  const cancelSeal = useCallback(async () => {
    const journal = stage.kind === "sealing" ? stage.journal : null;
    if (!journal?.uploadId) return;
    const authority = controller.captureDispatchAuthority();
    const flowGuard = () => controller.requireVerifiedDispatch(authority);
    try {
      const detail = await client.uploadDetail(journal.uploadId);
      if (!detail.seal_job_id) return;
      // The synchronous authority gate runs immediately before the actual
      // protected dispatch, not before the preceding reads.
      flowGuard();
      await client.jobCancel(detail.seal_job_id, crypto.randomUUID());
      // The active pipeline alone observes worker settlement and renews S.
      // A second poller could apply an old open observation after Resume.
    } catch (error) {
      handlePipelineError(error, journal);
    }
  }, [client, controller, handlePipelineError, stage]);

  const retryWithFreshBase = useCallback(async () => {
    const journal = stage.kind === "stale-base" ? stage.journal : null;
    if (!journal || journal.stagingOnly || !engineRef.current) return;
    const authority = controller.captureDispatchAuthority();
    const flowGuard = () => controller.requireVerifiedDispatch(authority);
    try {
      flowGuard();
      const detail = await client.uploadDetail(journal.uploadId as string);
      if (!detail.reusable) {
        setStage({ kind: "error", journal, message: "보관된 페이로드가 현재 재사용 가능하지 않습니다(reusable=false)." });
        return;
      }
      const document = await client.documentDetail(journal.target.documentId as string);
      // A deliberate new operation: new I with a freshly captured base on
      // the exact same target, reusing the retained sealed payload.
      const fresh: ImportJournal = {
        ...journal,
        importKey: crypto.randomUUID(),
        target: { ...journal.target, baseVersionId: document.latest_version_id },
        phase: "sealed",
        jobId: null,
        versionId: null,
      };
      journalRef.current = fresh;
      setStage({ kind: "publishing", journal: fresh });
      const published = await engineRef.current.publish(fresh, { guard: flowGuard });
      journalRef.current = published;
      setStage({ kind: published.phase === "published" ? "published" : "publishing", journal: published });
      if (published.phase === "published" && published.versionId) {
        onFinished?.(published.target.documentId ?? "", published.versionId);
      }
    } catch (error) {
      handlePipelineError(error, journalRef.current ?? journal);
    }
  }, [client, controller, handlePipelineError, onFinished, stage]);
  const retryPublish = useCallback(async () => {
    const journal = stage.kind === "publishing" ? stage.journal : journalRef.current;
    if (!journal || !engineRef.current) return;
    const authority = controller.captureDispatchAuthority();
    const flowGuard = () => controller.requireVerifiedDispatch(authority);
    try {
      const published = await engineRef.current.publish(journal, { guard: flowGuard });
      journalRef.current = published;
      setStage({ kind: published.phase === "published" ? "published" : "publishing", journal: published });
      if (published.phase === "published" && published.versionId) {
        onFinished?.(published.target.documentId ?? "", published.versionId);
      }
    } catch (error) {
      handlePipelineError(error, journal);
    }
  }, [controller, handlePipelineError, onFinished, stage]);

  /**
   * Response-loss recovery from an error state: the retained journal's own
   * phase decides the exact replay — prepared replays the original U,
   * completing/sealing replays the stored S checkpoint, dispatching replays
   * the original I, and a transferring record re-verifies ranges first.
   * Phases at or past completion need no reselected input.
   */
  const retryRecovery = useCallback(async () => {
    const journal = journalRef.current ?? recovery;
    if (!journal) return;
    const needsInput = journal.phase === "prepared" || journal.phase === "transferring" || journal.phase === "transfer-cancelled";
    if (needsInput && !planned) return;
    engineRef.current = engineRef.current ?? new TransferEngine(client, new BrowserJournalStore());
    const authority = controller.captureDispatchAuthority();
    const flowGuard = () => controller.requireVerifiedDispatch(authority);
    cancelRef.current = false;
    try {
      if (journal.phase === "prepared" && journal.uploadId === null) {
        const attached = await engineRef.current.attachUpload(journal, { guard: flowGuard });
        journalRef.current = attached;
        setRecovery(null);
        setStage({ kind: "transferring", journal: attached });
        await runPipeline(attached, planned, flowGuard);
        return;
      }
      if (journal.phase === "dispatching") {
        const published = await engineRef.current.publish(journal, { guard: flowGuard });
        journalRef.current = published;
        setStage({ kind: published.phase === "published" ? "published" : "publishing", journal: published });
        if (published.phase === "published" && published.versionId) {
          onFinished?.(published.target.documentId ?? "", published.versionId);
        }
        return;
      }
      setStage({ kind: "transferring", journal });
      await runPipeline(journal, planned, flowGuard);
    } catch (error) {
      handlePipelineError(error, journalRef.current);
    }
  }, [client, controller, handlePipelineError, onFinished, planned, recovery, runPipeline]);

  const restartNewUpload = useCallback(() => {
    setStage({ kind: "configure" });
    setPlanned(null);
    setLimitsNote(null);
    cancelRef.current = false;
    journalRef.current = null;
    setRecoveryNotice(null);
    void releaseNativeSource(null);
    if (mode === "existing" && documentId) void resolveBase(documentId);
  }, [documentId, mode, releaseNativeSource, resolveBase]);

  const busy = stage.kind === "transferring" || stage.kind === "sealing" || stage.kind === "publishing";
  const baseReady = mode === "new" || (mode === "existing" && base.kind === "resolved" && base.base !== null && documentId !== "");
  const currentJournal = "journal" in stage ? stage.journal : null;

  return (
    <section className="panel" data-testid="import-control" data-preselected={preselected ? "true" : undefined}>
      <h2>{preselected ? "선택한 문서에 새 버전 추가" : "결과 디렉터리 가져오기"}</h2>
      {preselected ? <p className="hint">새 버전을 추가할 문서가 미리 선택되어 있습니다.</p> : null}
      {limitsNote ? <p className="hint" data-testid="input-limits">{limitsNote}</p> : null}
      {recoveryNotice ? <p className="hint" role="status">{recoveryNotice}</p> : null}
      {recovery && stage.kind === "configure" ? (
        <p className="hint" role="status" data-testid="import-recovery">
          보관된 원래 작업을 이어서 확인할 수 있습니다. 대상: {recovery.target.documentId ?? "새 문서"} · 프로젝트: {recovery.target.projectId} · 기준: {recovery.target.baseVersionId ?? "없음"} · 작업: {recovery.importKey}.
          {recovery.phase === "prepared" ? " 원래 입력을 다시 선택하세요." : " 전송이 끝난 작업의 결과 확인에는 입력 재선택이 필요하지 않습니다."}
        </p>
      ) : null}
      {stage.kind === "configure" ? (
        <>
          <div className="form-row">
            <label>
              대상 문서
              <select value={mode} disabled={preselected ? true : undefined} onChange={(event) => { setMode(event.target.value as "new" | "existing"); setDocumentId(""); setBase({ kind: "unselected" }); baseRequestRef.current += 1; }}>
                <option value="new">새 문서(기본)</option>
                <option value="existing">기존 문서에 새 버전</option>
              </select>
            </label>
            <label>
              프로젝트
              <select value={projectId} onChange={(event) => { setProjectId(event.target.value); setDocumentId(""); setBase({ kind: "unselected" }); baseRequestRef.current += 1; }} disabled={mode === "existing" && preselected ? true : undefined}>
                {preselected && !projects.some(project => project.project_id === preselected.projectId) && <option value={preselected.projectId}>이 문서의 프로젝트 · {preselected.projectId}</option>}
                {projects.map((project) => (
                  <option key={project.project_id} value={project.project_id}>
                    {project.name}
                  </option>
                ))}
              </select>
            </label>
          </div>
          {mode === "existing" ? (
            <>
              <div className="form-row">
                <label>
                  문서 검색
                  <input type="text" value={documentQuery} onChange={(event) => setDocumentQuery(event.target.value)} placeholder="제목으로 검색" />
                </label>
                <button type="button" onClick={() => void searchDocuments(documentQuery, null)}>
                  검색
                </button>
                {documentCursor !== null ? (
                  <button type="button" onClick={() => void searchDocuments(documentQuery, documentCursor)}>
                    문서 더 불러오기
                  </button>
                ) : null}
              </div>
              <div className="form-row">
                <label>
                  기존 문서
                  <select value={documentId} disabled={preselected ? true : undefined} onChange={onDocumentChange}>
                    <option value="">문서를 선택하세요</option>
                    {documentChoices.map((document) => (
                      <option key={document.document_id} value={document.document_id}>
                        {document.title}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
              <p className="hint" data-testid="base-preview">
                {base.kind === "resolving"
                  ? "업로드 전 기준 버전(base)을 확인하는 중…"
                  : base.kind === "resolved"
                    ? base.base === null ? "이 문서에는 기준이 될 발행 버전이 없습니다. 다른 대상을 명시적으로 선택하세요." : `업로드 전 기준 버전(base): ${base.base}`
                    : base.kind === "failed"
                      ? `기준 버전을 확인하지 못했습니다: ${base.message}`
                      : "문서를 선택하면 기준 버전이 표시됩니다."}
              </p>
            </>
          ) : null}
          <div className="form-row">
            <label>
              폴더 선택
              <input
                type="file"
                multiple
                {...({ webkitdirectory: "", directory: "" } as React.InputHTMLAttributes<HTMLInputElement>)}
                onChange={onFolderPicked}
                disabled={busy}
              />
            </label>
            <label>
              ZIP 선택
              <input type="file" accept=".zip" onChange={onArchivePicked} disabled={busy} />
            </label>
            <label>
              단일 HTML
              <input type="file" accept=".html,.htm" onChange={onSinglePicked} disabled={busy} />
            </label>
          </div>
          {nativeFs ? (
            <div className="form-row">
              <label>
                네이티브 선택
                <select value={nativeMode} onChange={(event) => setNativeMode(event.target.value as "directory" | "archive" | "file")} disabled={busy || nativePicking}>
                  <option value="directory">폴더(전체 트리)</option>
                  <option value="archive">ZIP 파일</option>
                  <option value="file">단일 파일</option>
                </select>
              </label>
              <button type="button" disabled={busy || nativePicking} onClick={() => void onNativePicked(nativeMode)}>
                {nativePicking ? "네이티브 선택 대기 중…" : "시스템 선택기로 선택"}
              </button>
            </div>
          ) : null}
          <div className="form-row">
            {recovery && (planned || recovery.phase !== "prepared") ? (
              <button type="button" className="primary" onClick={() => void continueRecovered()}>
                원래 작업으로 이어서 진행
              </button>
            ) : null}
            <button
              type="button"
              className="primary"
              disabled={!planned || !baseReady || !projectId}
              onClick={() => void start()}
            >
              가져오기 시작
            </button>
          </div>
        </>
      ) : (
        <>
          <p className="status" role="status" data-testid="import-stage">
            단계: {phaseLabel(stage.kind)}
            {progress ? ` — 확인된 바이트 ${formatBytes(progress.ackedBytes)}${progress.knownTotalBytes !== null ? ` / ${formatBytes(progress.knownTotalBytes)}` : ""}, 항목 ${progress.entriesDone}/${progress.entriesTotal ?? "?"}` : ""}
          </p>
          {progress?.note ? <p className="hint">{progress.note}</p> : null}
          {currentJournal ? (
            <p className="hint">
              대상: {currentJournal.target.documentId ?? "새 문서"} · 기준: <span className="muted-code">{currentJournal.target.baseVersionId ?? "없음"}</span>
            </p>
          ) : null}
          <div className="form-row">
            {stage.kind === "error" && journalRef.current && (planned || !["prepared", "transferring", "transfer-cancelled"].includes(journalRef.current.phase)) ? (
              <button type="button" className="primary" onClick={() => void retryRecovery()}>
                보관된 작업으로 이어서 시도 (원래 U/S/I 재실행)
              </button>
            ) : null}
            {stage.kind === "error" && stage.journal?.phase === "completing" ? (
              <button type="button" onClick={() => void retryFailedCompletion()}>실패가 확인된 봉인 요청 다시 준비</button>
            ) : null}
            {stage.kind === "error" && !stage.journal ? (
              <button type="button" onClick={() => { setPlanned(null); setLimitsNote(null); setStage({ kind: "configure" }); if (mode === "existing" && documentId) void resolveBase(documentId); }}>입력과 대상 다시 선택</button>
            ) : null}
            {stage.kind === "transferring" ? (
              <button type="button" onClick={cancelTransfer}>
                전송 취소 (스테이징 유지)
              </button>
            ) : null}
            {stage.kind === "cancelled" ? (
              <>
                <p className="hint">반드시 원래 입력을 다시 선택하세요. 원본 전체 다이제스트가 없는 미전송 구간은 원본과 같은지 확인할 수 없습니다.</p>
                <label>
                  입력 다시 선택
                  <input
                    type="file"
                    multiple
                    {...({ webkitdirectory: "", directory: "" } as React.InputHTMLAttributes<HTMLInputElement>)}
                    onChange={onFolderPicked}
                  />
                </label>
                <label>
                  ZIP 다시 선택
                  <input type="file" accept=".zip" onChange={onArchivePicked} />
                </label>
                <button type="button" className="primary" disabled={!planned} onClick={() => void resumeTransfer()}>
                  다시 선택 후 이어서 전송 (범위 재검증)
                </button>
              </>
            ) : null}
            {stage.kind === "sealing" ? (
              <button type="button" onClick={() => void cancelSeal()}>
                봉인 검증 취소 (입력 유지)
              </button>
            ) : null}
            {stage.kind === "publishing" ? (
              <button type="button" onClick={() => void retryPublish()}>
                발행 결과 다시 확인
              </button>
            ) : null}
            {stage.kind === "stale-base" ? (
              <button type="button" className="primary" onClick={() => void retryWithFreshBase()}>
                새 기준으로 새 작업 발행 (보관 페이로드 재사용)
              </button>
            ) : null}
            {stage.kind === "mismatch" ? (
              <button type="button" className="primary" onClick={restartNewUpload}>
                새 가져오기 시작 (명시적 새 작업)
              </button>
            ) : null}
            {stage.kind === "published" && stage.journal.versionId ? (
              <>
                <span className="hint">
                  발행된 버전: <span className="muted-code">{stage.journal.versionId}</span>
                </span>
                <button type="button" onClick={restartNewUpload}>
                  새 가져오기 시작
                </button>
                {onClose ? (
                  <button type="button" onClick={onClose}>
                    닫기
                  </button>
                ) : null}
              </>
            ) : null}
            {stage.kind !== "published" && journalRef.current?.uploadId ? (
              <button type="button" onClick={() => void abandon()}>
                업로드 폐기 (Abandon)
              </button>
            ) : null}
          </div>
          {stage.kind === "stale-base" ? (
            <p className="hint" role="alert">
              업로드 전에 확인한 기준 버전이 최신이 아닙니다(VERSION_CONFLICT). 새 기준으로 새 작업을 만들거나 중단할 수 있습니다. 원래 작업의 의도는 별도로 보존됩니다.
            </p>
          ) : null}
          {stage.kind === "mismatch" ? (
            <p className="hint" role="alert">
              {stage.detail} 같은 업로드로는 이어서 진행할 수 없습니다. 항목 초기화 API는 존재하지 않습니다. 새 가져오기를 시작하거나 폐기하세요.
            </p>
          ) : null}
          {stage.kind === "error" ? (
            <p className="hint" role="alert">
              {stage.message}
            </p>
          ) : null}
        </>
      )}
    </section>
  );
}
