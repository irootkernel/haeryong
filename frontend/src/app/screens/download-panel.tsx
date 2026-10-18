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
import { useCallback, useRef, useState } from "react";
import type { DispatchAuthority, ShellController } from "../../client/controller.ts";
import { directoryTarget, destinationTransportSupport, downloadPackage, packageFileName, savePickerTarget } from "../../client/download.ts";
import type { DownloadTarget } from "../../client/download.ts";
import { PackageDownloadIncomplete } from "../../client/download.ts";

interface DownloadPanelProps {
  controller: ShellController;
  versionId: string;
}

type PanelStage =
  | { kind: "idle" }
  | { kind: "consent" }
  | { kind: "acquire" }
  | { kind: "permission"; directory: FileSystemDirectoryHandle }
  | { kind: "conflict"; target: DownloadTarget }
  | { kind: "downloading"; bytes: number }
  | { kind: "done"; bytes: number }
  | { kind: "incomplete"; stage: string; detail: string }
  | { kind: "unavailable-transport" };

type PermissionState = "granted" | "prompt" | "denied" | "pending";

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

export function DownloadPanel({ controller, versionId }: DownloadPanelProps) {
  const client = controller.client;
  const support = destinationTransportSupport();
  const [stage, setStage] = useState<PanelStage>({ kind: "idle" });
  const [customName, setCustomName] = useState(packageFileName(versionId));
  const [consented, setConsented] = useState(false);
  const [permissionState, setPermissionState] = useState<PermissionState | null>(null);
  const cancelRef = useRef(false);
  const authorityRef = useRef<DispatchAuthority | null>(null);
  const resultRef = useRef(false);

  const beginConsent = useCallback(() => {
    setStage(support.savePicker || support.directoryDrop ? { kind: "consent" } : { kind: "unavailable-transport" });
  }, [support.directoryDrop, support.savePicker]);

  /**
   * Explicit owner reset after a stopped flow (done or incomplete): consent,
   * result latch, permission state and the suggested name return to a clean
   * start so another version or destination can be downloaded normally.
   */
  const resetForNewDownload = useCallback(() => {
    resultRef.current = false;
    cancelRef.current = false;
    setConsented(false);
    setPermissionState(null);
    setCustomName(packageFileName(versionId));
    setStage({ kind: "idle" });
  }, [versionId]);

  const confirmConsent = useCallback(() => {
    // Capture the authority at the explicit consent action; every later
    // protected step re-validates it synchronously.
    authorityRef.current = controller.captureDispatchAuthority();
    controller.requireVerifiedDispatch(authorityRef.current);
    setConsented(true);
    setStage({ kind: "acquire" });
  }, [controller]);

  const startDownload = useCallback(
    async (target: DownloadTarget) => {
      if (resultRef.current) return;
      cancelRef.current = false;
      setStage({ kind: "downloading", bytes: 0 });
      try {
        const result = await downloadPackage(client, versionId, target, {
          guard: () => controller.requireVerifiedDispatch(authorityRef.current),
          shouldContinue: () => !cancelRef.current,
          onProgress: (bytes) => {
            if (!resultRef.current) setStage({ kind: "downloading", bytes });
          },
        });
        resultRef.current = true;
        setStage({ kind: "done", bytes: result.bytes });
      } catch (error) {
        if (resultRef.current) return; // A completed result is never reset by a late stale flow.
        if (error instanceof PackageDownloadIncomplete) {
          setStage({ kind: "incomplete", stage: error.stage, detail: "전송이 완료되지 않았습니다. 부분 결과는 완료로 표시되지 않습니다." });
          return;
        }
        setStage({ kind: "incomplete", stage: "reader", detail: error instanceof Error ? error.message : String(error) });
      }
    },
    [client, controller, versionId],
  );

  const acquireSavePicker = useCallback(async () => {
    const picker = (globalThis as { showSaveFilePicker?: (options: unknown) => Promise<FileSystemFileHandle> }).showSaveFilePicker;
    if (!picker) return;
    try {
      controller.requireVerifiedDispatch(authorityRef.current);
      const handle = await picker({
        suggestedName: customName || packageFileName(versionId),
        types: [{ description: "Haeryong original-directory archive", accept: { "application/zip": [".zip"] } }],
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
  }, [controller, customName, startDownload, versionId]);

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
      const name = customName || packageFileName(versionId);
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
        await startDownload(stage.target);
        return;
      }
      setStage({ kind: "acquire" });
    },
    [stage, startDownload],
  );

  return (
    <section className="panel" data-testid="download-panel" data-version={versionId}>
      <h2>원본 디렉터리 전체 내려받기</h2>
      <p className="hint">
        선택한 버전의 원본 디렉터리를 ZIP 아카이브로 스트리밍합니다. 서비스 메타데이터는 포함되지 않습니다. 저장소에서 검증된 원본 항목을 내보냅니다. 이 화면은 수신한 ZIP을 다시 풀어 항목별 해시를 검증하지 않습니다.
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
            선택한 이름의 파일이 이미 있으면 — 빈 파일도 포함입니다 — 내용이 교체될 수 있습니다. Haeryong은 기존 존재 여부를 감지하거나 이전 내용을 보존할 수 없습니다. 교체를 원하지 않으면 다른 이름을 사용하세요. 엄격한 덮어쓰기 금지가 필요하면 CLI(<span className="muted-code">haeryong files download --version … --output …</span>)를 사용하세요.
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
            {support.savePicker ? (
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
            <button type="button" className="primary" onClick={() => void resolveConflict("replace")}>
              동의하고 교체
            </button>
            <button type="button" onClick={() => void resolveConflict("new-name")}>
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
