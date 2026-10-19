// Original-directory archive download control. An EXPLICIT consent action
// (acknowledging replacement semantics, existing empty files included, with
// no existence/size inference) always precedes any picker, drop or write.
// Acquisition is the owner's native save picker or a real host directory
// drop feeding the same implemented write path; a dropped directory's write
// permission is requested from a genuine owner click with granted/prompt/
// denied/pending handled honestly. Directory-backed targets get an honest,
// best-effort existence check with an explicit conflict action (replace
// under consent / new name / abort); a created-after-check race is absorbed
// by the consented replacement semantics. Completion requires a clean
// stream end plus close(); failures and cancellation abort and are never
// labelled complete, and a stale authority never feeds bytes onward.
import { useCallback, useEffect, useRef, useState } from "react";
import type { DispatchAuthority, ShellController } from "../../client/controller.ts";
import { directoryTarget, destinationTransportSupport, downloadPackage, packageFileName, savePickerTarget } from "../../client/download.ts";
import type { DownloadTarget } from "../../client/download.ts";
import { PackageDownloadIncomplete } from "../../client/download.ts";
import { nativeDownloadTarget } from "../../client/native-fs.ts";
import type { NativeFsClient } from "../../client/native-fs.ts";
import type { NativeDestinationSelection } from "../../client/types.ts";

interface DownloadPanelProps {
  controller: ShellController;
  versionId: string;
  filePath?: string;
}

type PanelStage =
  | { kind: "idle" }
  | { kind: "consent" }
  | { kind: "acquire" }
  | { kind: "permission"; directory: FileSystemDirectoryHandle }
  | { kind: "conflict"; target: DownloadTarget }
  | { kind: "downloading"; bytes: number }
  | { kind: "done"; bytes: number; warning?: string }
  | { kind: "incomplete"; stage: string; detail: string }
  | { kind: "unavailable-transport" };

type PermissionState = "granted" | "prompt" | "denied" | "pending";

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

export function DownloadPanel({ controller, versionId, filePath }: DownloadPanelProps) {
  const client = controller.client;
  const support = destinationTransportSupport();
  const nativeFs = typeof (client as unknown as Partial<NativeFsClient>).chooseDownloadDestination === "function" ? (client as unknown as NativeFsClient) : null;
  const suggestedName = filePath?.split("/").at(-1) ?? packageFileName(versionId);
  const [stage, setStage] = useState<PanelStage>({ kind: "idle" });
  const [customName, setCustomName] = useState(suggestedName);
  const [consented, setConsented] = useState(false);
  const [permissionState, setPermissionState] = useState<PermissionState | null>(null);
  const [acquireBusy, setAcquireBusy] = useState(false);
  const cancelRef = useRef(false);
  const authorityRef = useRef<DispatchAuthority | null>(null);
  const resultRef = useRef(false);
  // Late-choice/lifecycle ownership (M3 C2/C3 F4): a picker or download that
  // resolves after the panel left the screen must never write, publish or
  // resurrect UI state; a discarded native selection is aborted explicitly.
  const mountedRef = useRef(true);
  const pickEpochRef = useRef(0);
  const nativeSelectionRef = useRef<{ handle: string; authority: DispatchAuthority | null } | null>(null);
  // Single-flight ownership (M3 C3 F4): the ENTIRE destination acquisition
  // plus its download effect is one synchronous-owner flight. A duplicate
  // chooser/effect entry is rejected BEFORE any ref mutation or chooser
  // call, so a second handle can never be created, overwrite the owned one
  // or reset a live cancel. The conflict-replace re-entry takes its own
  // flight after the acquisition flight ends at the conflict stop.
  const flightRef = useRef<symbol | null>(null);

  const acquireFlight = (): symbol | null => {
    if (flightRef.current !== null) return null;
    const token = Symbol("download-flight");
    flightRef.current = token;
    return token;
  };
  const releaseFlight = (token: symbol): void => {
    if (flightRef.current === token) flightRef.current = null;
  };
  useEffect(() => {
    // StrictMode-safe lifecycle: each effect (re)mount restores liveness and
    // invalidates prior picks; only the real unmount leaves it dead.
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      pickEpochRef.current += 1;
      cancelRef.current = true;
      const owned = nativeSelectionRef.current;
      nativeSelectionRef.current = null;
      if (owned) {
        // Best effort after unmount: a typed Go cleanup failure cannot render
        // in this dead component, so it routes to the controller's runtime
        // cleanup alert under the authority THAT selection was captured with
        // (never the mutable ref at catch time; a replaced authority drops
        // the report instead of reinserting an old era's path).
        void nativeFs?.destAbort(owned.handle).catch((error: unknown) => {
          controller.reportRuntimeCleanupFailure(error as { code?: string; details?: { temp?: string } }, owned.authority);
        });
      }
    };
  }, [controller, nativeFs]);

  /**
   * Explicitly discard a not-yet-published native selection (new-name
   * choice, replacement, unmount). While mounted the abort is AWAITED and a
   * typed cleanup failure keeps the obligation: the handle stays owned for
   * retry and the exact surviving temp path is shown; nothing is claimed
   * released. Returns the typed failure when cleanup did not complete.
   */
  const discardNativeSelection = useCallback(async (): Promise<{ code?: string; details?: { temp?: string } } | null> => {
    const owned = nativeSelectionRef.current;
    if (!owned || !nativeFs) return null;
    try {
      await nativeFs.destAbort(owned.handle);
      if (nativeSelectionRef.current === owned) nativeSelectionRef.current = null;
      return null;
    } catch (error) {
      // Retain the handle: the discard is not done, and a later discard or
      // era teardown must retry the removal with the same typed path.
      return error as { code?: string; details?: { temp?: string } };
    }
  }, [nativeFs]);

  /** Typed cleanup-failure copy preserving the exact surviving temp path. */
  const cleanupDetail = useCallback((failure: { code?: string; details?: { temp?: string } }): string => {
    if (failure.code === "NATIVE_CLEANUP_FAILED" && failure.details?.temp) {
      return `임시 파일 정리에 실패했습니다. 남아 있을 수 있는 경로: ${failure.details.temp} 다시 시도하면 정리를 재시도합니다.`;
    }
    return "선택한 이전 대상을 중단하지 못했습니다. 다시 시도하면 정리를 재시도합니다.";
  }, []);

  const beginConsent = useCallback(() => {
    setStage(support.savePicker || support.directoryDrop || nativeFs !== null ? { kind: "consent" } : { kind: "unavailable-transport" });
  }, [support.directoryDrop, support.savePicker, nativeFs]);

  /**
   * Explicit owner reset after a stopped flow (done or incomplete): consent,
   * result latch, permission state and the suggested name return to a clean
   * start so another version or destination can be downloaded normally.
   */
  const resetForNewDownload = useCallback(() => {
    resultRef.current = false;
    cancelRef.current = false;
    pickEpochRef.current += 1; // An explicit reset is a new intent: old picks die.
    setConsented(false);
    setPermissionState(null);
    setCustomName(suggestedName);
    setStage({ kind: "idle" });
  }, [versionId, suggestedName]);

  const confirmConsent = useCallback(() => {
    // Capture the authority at the explicit consent action; every later
    // protected step re-validates it synchronously.
    authorityRef.current = controller.captureDispatchAuthority();
    controller.requireVerifiedDispatch(authorityRef.current);
    setConsented(true);
    setStage({ kind: "acquire" });
  }, [controller]);

  /**
   * Internal download effect body (M3 C3): callers either already own the
   * single-flight token (the native acquisition flow calling straight into
   * its own effect — never a self-reject) or are the external entry below.
   */
  const runDownloadEffect = useCallback(
    async (target: DownloadTarget, intent: { replace?: boolean } = {}) => {
      if (resultRef.current) return;
      if (!mountedRef.current) return; // A left panel never starts or resumes a real effect.
      cancelRef.current = false;
      setStage({ kind: "downloading", bytes: 0 });
      try {
        // A single-file native delivery MUST carry its own strong validator
        // (Repr-Digest of the same response); without it the save fails
        // typed instead of silently writing unverified bytes.
        const transport =
          filePath === undefined
            ? client
            : {
                packageStream: (id: string) => {
                  const streamPromise = client.fileStream
                    ? client.fileStream(id, filePath)
                    : Promise.reject(new Error("현재 연결은 첨부 파일 스트리밍을 지원하지 않습니다."));
                  return streamPromise.then((stream) => {
                    if (nativeFs && !stream.validator) {
                      stream.cancel();
                      const failure = new Error("이 파일의 검증 정보(Repr-Digest)가 없어 검증된 저장을 진행할 수 없습니다.");
                      (failure as Error & { code?: string }).code = "NATIVE_VALIDATOR_MISSING";
                      throw failure;
                    }
                    return stream;
                  });
                },
              };
        const result = await downloadPackage(transport, versionId, target, {
          guard: () => {
            // The synchronous authority gate also refuses to open a
            // writable after the panel left the screen (effect guard before
            // any destination mutation).
            controller.requireVerifiedDispatch(authorityRef.current);
            if (!mountedRef.current) throw new Error("내려받기 화면을 벗어나 전송을 중단했습니다.");
          },
          shouldContinue: () => mountedRef.current && !cancelRef.current,
          onProgress: (bytes) => {
            if (mountedRef.current && !resultRef.current) setStage({ kind: "downloading", bytes });
          },
        }, intent);
        if (!mountedRef.current) return;
        resultRef.current = true;
        // A published attempt reached its terminal state Go-side; this panel
        // no longer owes a discard for it.
        nativeSelectionRef.current = null;
        setStage({ kind: "done", bytes: result.bytes, warning: result.warning });
      } catch (error) {
        if (resultRef.current) return; // A completed result is never reset by a late stale flow.
        if (!mountedRef.current) return; // No UI resurrection after unmount.
        if (error instanceof PackageDownloadIncomplete) {
          const code = (error.cause as { code?: string } | undefined)?.code;
          if (code === "NATIVE_CLOSE_UNCERTAIN") {
            setStage({ kind: "incomplete", stage: error.stage, detail: "완료 여부를 확인할 수 없습니다. 대상에 이미 파일이 있을 수 있으니 확인 후 다시 시도하세요." });
            return;
          }
          if (code === "NATIVE_VALIDATOR_MISSING") {
            setStage({ kind: "incomplete", stage: error.stage, detail: (error.cause as Error).message });
            return;
          }
          if (code === "NATIVE_DESTINATION_CONFLICT" && error.stage === "close") {
            // The target appeared between the probe and publication: the
            // original stays untouched and the owner decides again through
            // the explicit conflict path (consented replace or a new name).
            setStage({ kind: "conflict", target });
            return;
          }
          const cleanup = error.cleanupError as { code?: string; details?: { temp?: string } } | undefined;
          let detail = "전송이 완료되지 않았습니다. 부분 결과는 완료로 표시되지 않습니다.";
          if (cleanup?.code === "NATIVE_CLEANUP_FAILED" && cleanup.details?.temp) {
            detail += ` 임시 파일 정리에 실패했습니다. 남아 있을 수 있는 경로: ${cleanup.details.temp}`;
          } else if (code === "NATIVE_CLEANUP_FAILED") {
            const temp = (error.cause as { details?: { temp?: string } } | undefined)?.details?.temp;
            if (temp) detail += ` 임시 파일 정리에 실패했습니다. 남아 있을 수 있는 경로: ${temp}`;
          }
          setStage({ kind: "incomplete", stage: error.stage, detail });
          return;
        }
        setStage({ kind: "incomplete", stage: "reader", detail: error instanceof Error ? error.message : String(error) });
      }
    },
    [client, controller, versionId, filePath, nativeFs],
  );

  /**
   * External download entry (conflict-replace consent, browser save picker,
   * dropped directory): takes the single-flight for its own effect. A
   * duplicate entry while a flight is active is rejected synchronously —
   * no cancel reset, no second effect.
   */
  const startDownload = useCallback(
    async (target: DownloadTarget, intent: { replace?: boolean } = {}) => {
      const token = acquireFlight();
      if (token === null) return;
      try {
        await runDownloadEffect(target, intent);
      } finally {
        releaseFlight(token);
      }
    },
    [runDownloadEffect],
  );

  // Native owner-selected destination (TASK-003-05 M3): exact existence
  // probe, private sibling temp, default no-replace publication and explicit
  // consented replace — through the same shared download engine. The WHOLE
  // acquisition+effect holds one single-flight token (duplicate chooser
  // clicks are rejected before any chooser call); the nested effect call
  // reuses this flight's ownership instead of taking a second one.
  const acquireNativeDestination = useCallback(async () => {
    if (!nativeFs) return;
    const token = acquireFlight();
    if (token === null) return;
    setAcquireBusy(true);
    const epoch = pickEpochRef.current;
    // THAT operation's authority: captured before any await so a late
    // failure reports under the era that owned the choice, never the
    // mutable ref read after the fact.
    const operationAuthority = authorityRef.current;
    try {
      controller.requireVerifiedDispatch(operationAuthority);
      // Discard any PREVIOUS selection BEFORE choosing a new one: a failed
      // old cleanup retains the old handle for retry and stops this flow —
      // a second fresh handle is never created (and never leaked) while the
      // previous obligation is unresolved.
      const previousFailure = await discardNativeSelection();
      if (previousFailure) {
        if (mountedRef.current) setStage({ kind: "incomplete", stage: "write", detail: cleanupDetail(previousFailure) });
        return;
      }
      // The awaited discard may have outlived this screen or intent.
      if (!mountedRef.current || epoch !== pickEpochRef.current) return;
      const selection: NativeDestinationSelection = await nativeFs.chooseDownloadDestination(customName || suggestedName, "save-file");
      if (!mountedRef.current || epoch !== pickEpochRef.current) {
        // The panel left the screen or a newer choice superseded this one:
        // abort the late selection's handle; no stage change, no download.
        // A failed abort routes its typed path to the runtime cleanup alert
        // (dropped only when that authority was replaced).
        if (!selection.canceled) {
          void nativeFs.destAbort(selection.handle).catch((error: unknown) => {
            controller.reportRuntimeCleanupFailure(error as { code?: string; details?: { temp?: string } }, operationAuthority);
          });
        }
        return;
      }
      if (selection.canceled) {
        setStage({ kind: "acquire" });
        return;
      }
      nativeSelectionRef.current = { handle: selection.handle, authority: operationAuthority };
      const target = nativeDownloadTarget(nativeFs, selection);
      if (selection.exists) {
        setStage({ kind: "conflict", target });
        return;
      }
      // This flight already owns the effect slot: run the body directly
      // (never a second token, never a self-reject).
      await runDownloadEffect(target);
    } catch (error) {
      if (mountedRef.current) setStage({ kind: "incomplete", stage: "write", detail: error instanceof Error ? error.message : String(error) });
    } finally {
      releaseFlight(token);
      if (mountedRef.current) setAcquireBusy(false);
    }
  }, [cleanupDetail, controller, customName, discardNativeSelection, nativeFs, runDownloadEffect, suggestedName]);

  const acquireSavePicker = useCallback(async () => {
    const picker = (globalThis as { showSaveFilePicker?: (options: unknown) => Promise<FileSystemFileHandle> }).showSaveFilePicker;
    if (!picker) return;
    try {
      controller.requireVerifiedDispatch(authorityRef.current);
      const handle = await picker({
        suggestedName: customName || suggestedName,
        ...(filePath === undefined ? { types: [{ description: "Haeryong original-directory archive", accept: { "application/zip": [".zip"] } }] } : {}),
      });
      // Save-picker targets cannot detect prior existence; consent governs.
      await startDownload(savePickerTarget(handle));
    } catch (error) {
      if (error instanceof DOMException && error.name === "AbortError") {
        setStage({ kind: "acquire" });
        return;
      }
      setStage({ kind: "incomplete", stage: "write", detail: error instanceof Error ? error.message : String(error) });
    }
  }, [controller, customName, startDownload, versionId, suggestedName, filePath]);

  const onDrop = useCallback(async (event: React.DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    const item = event.dataTransfer.items[0];
    const asHandle = (item as DataTransferItem & { getAsFileSystemHandle?: () => Promise<FileSystemHandle | null> }).getAsFileSystemHandle;
    if (!asHandle) return;
    const handle = await asHandle.call(item as DataTransferItem);
    if (!handle || handle.kind !== "directory") {
      setStage({ kind: "incomplete", stage: "write", detail: "폴더(디렉터리)를 놓아주세요." });
      return;
    }
    // The write permission itself is requested from a genuine owner click
    // on the permission stage; the drop only nominates the directory.
    setPermissionState("pending");
    setStage({ kind: "permission", directory: handle as FileSystemDirectoryHandle });
  }, []);

  const grantFromClick = useCallback(async () => {
    if (stage.kind !== "permission") return;
    const handle = stage.directory as FileSystemDirectoryHandle & {
      queryPermission?: (mode: { mode: string }) => Promise<PermissionState>;
      requestPermission?: (mode: { mode: string }) => Promise<PermissionState>;
    };
    try {
      controller.requireVerifiedDispatch(authorityRef.current);
      let state: PermissionState = (await handle.queryPermission?.({ mode: "readwrite" })) ?? "granted";
      if (state === "prompt") {
        state = (await handle.requestPermission?.({ mode: "readwrite" })) ?? "denied";
      }
      setPermissionState(state);
      if (state !== "granted") {
        return; // denied/prompt-unresolved stay honestly visible.
      }
      const name = customName || suggestedName;
      const target = directoryTarget(stage.directory, name);
      const exists = await target.exists();
      if (exists) {
        setStage({ kind: "conflict", target });
        return;
      }
      await startDownload(target);
    } catch (error) {
      setStage({ kind: "incomplete", stage: "write", detail: error instanceof Error ? error.message : String(error) });
    }
  }, [controller, customName, stage, startDownload, versionId]);

  const resolveConflict = useCallback(
    async (action: "replace" | "new-name") => {
      if (stage.kind !== "conflict") return;
      if (action === "replace") {
        // The owner explicitly consented to replacing the exact existing
        // target; only this consent ever reaches the destination attempt.
        await startDownload(stage.target, { replace: true });
        return;
      }
      // Choosing a different name discards this selection: the abort is
      // awaited and an unresolved cleanup keeps its typed path visible
      // instead of silently advancing.
      const token = acquireFlight();
      if (token === null) return;
      const epoch = pickEpochRef.current;
      setAcquireBusy(true);
      try {
        const failure = await discardNativeSelection();
        if (!mountedRef.current || epoch !== pickEpochRef.current || flightRef.current !== token) return;
        if (failure) {
          setStage({ kind: "incomplete", stage: "write", detail: cleanupDetail(failure) });
          return;
        }
        setStage({ kind: "acquire" });
      } finally {
        if (flightRef.current === token) {
          releaseFlight(token);
          if (mountedRef.current) setAcquireBusy(false);
        }
      }
    },
    [cleanupDetail, discardNativeSelection, stage, startDownload],
  );

  return (
    <section className="panel" data-testid="download-panel" data-version={versionId}>
      <h2>{filePath === undefined ? "원본 디렉터리 전체 내려받기" : "파일 내려받기: " + filePath}</h2>
      <p className="hint">
        {filePath === undefined ? "선택한 버전의 원본 디렉터리를 ZIP 아카이브로 스트리밍합니다. 서비스 메타데이터는 포함되지 않습니다. 저장소에서 검증된 원본 항목을 내보냅니다. 이 화면은 수신한 ZIP을 다시 풀어 항목별 해시를 검증하지 않습니다." : "선택한 원본 파일을 인증된 연결로 스트리밍해 저장합니다."}
      </p>
      {stage.kind === "idle" ? (
        <div className="form-row">
          <button type="button" className="primary" onClick={beginConsent}>
            내려받기 시작
          </button>
        </div>
      ) : null}
      {stage.kind === "done" || stage.kind === "incomplete" ? (
        <div className="form-row">
          <button type="button" className="primary" onClick={resetForNewDownload}>
            새 내려받기 시작 (동의 초기화)
          </button>
        </div>
      ) : null}
      {stage.kind === "consent" ? (
        <>
          <p className="hint" role="alert" data-testid="download-consent">
            {nativeFs
              ? "시스템 선택기로 저장 위치를 정합니다. 같은 이름의 파일이 이미 있으면 정확히 감지되며, 기본적으로 기존 파일은 절대 덮어쓰지 않습니다. 교체는 충돌 화면에서 명시적으로 동의할 때에만 진행되고, 그 전까지 원본은 그대로 보존됩니다."
              : <>선택한 이름의 파일이 이미 있으면 — 빈 파일도 포함입니다 — 내용이 교체될 수 있습니다. Haeryong은 기존 존재 여부를 감지하거나 이전 내용을 보존할 수 없습니다. 교체를 원하지 않으면 다른 이름을 사용하세요. 엄격한 덮어쓰기 금지가 필요하면 CLI(<span className="muted-code">haeryong files download --version … --output …</span>)를 사용하세요.</>}
          </p>
          <div className="form-row">
            <label className="choice">
              <input type="checkbox" checked={consented} onChange={(event) => setConsented(event.target.checked)} /> 위 교체 규칙을 이해하고 동의합니다.
            </label>
          </div>
          <div className="form-row">
            <button type="button" className="primary" disabled={!consented} onClick={confirmConsent}>
              동의하고 계속
            </button>
          </div>
        </>
      ) : null}
      {stage.kind === "acquire" ? (
        <>
          <div className="form-row">
            <label>
              파일 이름
              <input type="text" value={customName} onChange={(event) => setCustomName(event.target.value)} />
            </label>
            {nativeFs ? (
              <button type="button" className="primary" disabled={acquireBusy} onClick={() => void acquireNativeDestination()}>
                저장 위치 선택 (시스템 저장 대화상자)
              </button>
            ) : null}
            {support.savePicker && !nativeFs ? (
              <button type="button" className="primary" onClick={() => void acquireSavePicker()}>
                저장 위치 선택 (시스템 저장 대화상자)
              </button>
            ) : null}
          </div>
          {support.directoryDrop ? (
            <div className="drop-zone" data-testid="download-drop-zone" onDrop={(event) => void onDrop(event)} onDragOver={(event) => event.preventDefault()}>
              또는 실제 호스트 폴더를 이 영역에 놓아 대상으로 지정
            </div>
          ) : null}
        </>
      ) : null}
      {stage.kind === "permission" ? (
        <>
          <p className="hint" data-testid="download-permission">
            놓인 폴더(<span className="muted-code">{stage.directory.name}</span>)에 대한 쓰기 권한: {permissionState ?? "pending"}
            {permissionState === "denied" ? " — 브라우저 설정에서 이 사이트의 폴더 쓰기를 허용한 뒤 다시 시도하세요." : ""}
          </p>
          <div className="form-row">
            <button type="button" className="primary" onClick={() => void grantFromClick()}>
              이 폴더에 저장
            </button>
            <button type="button" onClick={() => setStage({ kind: "acquire" })}>
              다른 대상 선택
            </button>
          </div>
        </>
      ) : null}
      {stage.kind === "conflict" ? (
        <>
          <p className="hint" role="alert" data-testid="download-conflict">
            대상 폴더에 같은 이름의 파일이 이미 있습니다. 교체(동의한 경우)하거나 다른 이름을 사용하세요. 자동으로 이름을 바꾸지 않습니다.
          </p>
          <div className="form-row">
            <button type="button" className="primary" disabled={acquireBusy} onClick={() => void resolveConflict("replace")}>
              동의하고 교체
            </button>
            <button type="button" disabled={acquireBusy} onClick={() => void resolveConflict("new-name")}>
              다른 이름 사용
            </button>
          </div>
        </>
      ) : null}
      {stage.kind === "downloading" ? (
        <>
          <p className="status" role="status" data-testid="download-progress">
            스트리밍 중… 수신 {formatBytes(stage.bytes)}
          </p>
          <div className="form-row">
            <button
              type="button"
              onClick={() => {
                cancelRef.current = true;
              }}
            >
              취소
            </button>
          </div>
        </>
      ) : null}
      {stage.kind === "done" ? (
        <p className="status" role="status" data-testid="download-done">
          완료: {formatBytes(stage.bytes)}를 지정한 대상에 기록했습니다.
          {stage.warning ? ` ${stage.warning}` : ""}
        </p>
      ) : null}
      {stage.kind === "incomplete" ? (
        <>
          <p className="hint" role="alert" data-testid="download-incomplete">
            미완료({stage.stage}): {stage.detail}
          </p>
          <div className="form-row">
            <button type="button" onClick={() => setStage({ kind: "acquire" })}>
              다시 시도
            </button>
          </div>
        </>
      ) : null}
      {stage.kind === "unavailable-transport" ? (
        <p className="hint" role="alert" data-testid="download-unavailable">
          이 브라우저는 스트리밍 저장 전송을 지원하지 않습니다. CLI(<span className="muted-code">haeryong files download --version {versionId} --output &lt;폴더&gt;</span>)로 동일한 원본 디렉터리를 내려받을 수 있습니다.
        </p>
      ) : null}
    </section>
  );
}
