// Shared import pipeline used by the import control and storage discovery:
// acknowledgement-driven transfer, honest seal polling (sealing stays until
// the worker stop is confirmed; a confirmed return to open resets completion
// for a deliberate NEW S while an unknown outcome keeps the original S),
// then publication reconciliation that marks publication only after the
// import job completes and the version view is confirmed. The caller owns
// journal identity and UI state transitions; a journal-less recovery flow
// stops at sealed.
import type { HaeryongClient } from "../../client/types.ts";
import type { ImportJournal, PlannedInput, TransferCallbacks } from "../../client/transfer.ts";
import type { TransferEngine } from "../../client/transfer.ts";

export interface PipelineHooks {
  onStage: (stage: "transferring" | "sealing" | "sealed" | "publishing" | "published" | "cancelled" | "abandoned", journal: ImportJournal) => void;
  onProgress?: TransferCallbacks["onProgress"];
  shouldContinue?: () => boolean;
  guard?: () => void;
  /** First-completion source stability gate; flows into the engine's completion. */
  verifyInputStable?: TransferCallbacks["verifyInputStable"];
  waitMs?: (ms: number) => Promise<void>;
  /** Journal-less recovery stops at sealed instead of publishing. */
  stopAtSealed?: boolean;
  onNotice?: (notice: string) => void;
}

function defaultWait(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export const JOURNAL_UNAVAILABLE_NOTICE = "브라우저 복구 기록에 접근할 수 없어 이 흐름을 계속할 수 없습니다. 브라우저 저장소를 허용한 뒤 다시 시도하거나 호스트의 CLI를 사용하세요. 보관된 업로드는 저장소 화면에서 확인할 수 있습니다.";

/** Run one journal from its current phase through publication. */
export async function runImportPipeline(client: HaeryongClient, engine: TransferEngine, journal: ImportJournal, input: PlannedInput | null, hooks: PipelineHooks): Promise<ImportJournal> {
  const waitMs = hooks.waitMs ?? defaultWait;
  let current = journal;
  hooks.guard?.();
  if (current.phase === "prepared" && current.uploadId === null) {
    current = await engine.attachUpload(current, { guard: hooks.guard });
  }
  if (current.uploadId && (current.phase === "prepared" || current.phase === "transferring" || current.phase === "transfer-cancelled")) {
    const detail = await client.uploadDetail(current.uploadId);
    hooks.guard?.();
    if (detail.state === "sealed" || detail.state === "sealing") {
      current.phase = detail.state;
      engine.journal.save(current);
    } else if (detail.state === "abandoned") {
      hooks.onStage("abandoned", current);
      return current;
    }
  }
  if (current.phase === "transferring" || current.phase === "transfer-cancelled" || current.phase === "prepared") {
    if (!input) throw new Error("this phase requires a reselected input");
    hooks.onStage("transferring", current);
    current = await engine.resume(current, input, {
      guard: hooks.guard,
      onProgress: hooks.onProgress,
      onNotice: hooks.onNotice,
      shouldContinue: hooks.shouldContinue,
      verifyInputStable: hooks.verifyInputStable,
    });
  }
  if (current.phase === "completing") {
    current = await engine.complete(current, { guard: hooks.guard, shouldContinue: hooks.shouldContinue });
  }
  if (current.phase === "sealing" && current.uploadId) {
    hooks.onStage("sealing", current);
    // Poll the ACTUAL upload state with no artificial deadline: sealing
    // stays until the worker stop is confirmed; a non-abandoned upload
    // returns open with retained bytes only then, which resets completion
    // for a deliberate new S (the unknown-outcome path keeps the same S).
    for (;;) {
      if (hooks.shouldContinue && !hooks.shouldContinue()) {
        hooks.onStage("cancelled", current);
        return current;
      }
      const detail = await client.uploadDetail(current.uploadId);
      hooks.guard?.();
      if (detail.state === "sealed") break;
      if (detail.state === "open") {
        current = engine.markSealSettled(current);
        hooks.onNotice?.("봉인이 열린 상태로 확정되었습니다. 이어하기는 새로운 완료 키로 진행됩니다.");
        hooks.onStage("cancelled", current);
        return current;
      }
      if (detail.state === "abandoned") {
        hooks.onStage("abandoned", current);
        return current;
      }
      await waitMs(300);
    }
    current.phase = "sealed";
    engine.journal.save(current);
  }
  if (current.phase === "sealed") {
    hooks.onStage("sealed", current);
    if (hooks.stopAtSealed || current.stagingOnly) {
      hooks.onStage("sealed", current);
      hooks.onNotice?.("복구는 봉인에서 중단합니다. 발행은 별도의 명시적 새 작업 확인으로만 진행됩니다.");
      return current;
    }
  }
  if (current.phase === "sealed" || current.phase === "dispatching") {
    if (hooks.shouldContinue && !hooks.shouldContinue()) return current;
    hooks.onStage("publishing", current);
    current = await engine.publish(current, { guard: hooks.guard }, waitMs);
    hooks.onStage(current.phase === "published" ? "published" : "publishing", current);
  }
  return current;
}
