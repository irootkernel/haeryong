import type { BackupStatus, PurgeBackup } from "../../client/types.ts";

export function backupLine(value: PurgeBackup | undefined): string {
  if (!value || value.state === "unavailable") return "마지막 완전한 검증 백업: 상태를 제공할 수 없습니다.";
  if (value.state === "not_created") return "마지막 완전한 검증 백업: 아직 생성되지 않았습니다.";
  return `마지막 완전한 검증 백업: ${value.last_complete_at ? new Date(value.last_complete_at).toLocaleString() : "시간을 확인할 수 없습니다"}`;
}

export function backupSummary(value: BackupStatus): string {
  if (!value.available) return "백업 위치를 사용할 수 없습니다. 마지막 검증 기록은 아래에 표시됩니다.";
  if (!value.last_complete_verified) return "아직 생성된 완전한 검증 백업이 없습니다.";
  return `마지막 완전한 검증 백업: ${new Date(value.last_complete_verified.completed_at).toLocaleString()}`;
}

export function readBackupStatus(value: unknown): BackupStatus {
  if (!value || typeof value !== "object" || !("backup" in value)) throw new Error("백업 상태를 제공할 수 없습니다.");
  const backup = (value as { backup?: BackupStatus }).backup;
  if (!backup || backup.mode !== "manual" || typeof backup.available !== "boolean" || !("last_complete_verified" in backup) || !backup.last_trial_restore) throw new Error("백업 상태 응답을 확인할 수 없습니다.");
  return backup;
}

export function backupAttemptLabel(state: string): string {
  return ({ queued: "대기", running: "진행 중", completed: "완료", failed: "실패", interrupted: "중단", cancelled: "취소됨", cancelling: "취소 중", paused: "일시 정지", capturing: "스냅샷 준비", copying: "파일 복사", publishing: "완료 확인", complete: "완료", abandoned: "폐기됨", restored_paused: "복원 후 보류" } as Record<string, string>)[state] ?? "상태 확인 필요";
}
