import { useEffect, useState } from "react";
import type { ShellController } from "../../client/controller.ts";
import type { BackupStatus } from "../../client/types.ts";
import { backupSummary, readBackupStatus, backupAttemptLabel } from "./backup-state.ts";

export function BackupScreen({ controller }: { controller: ShellController }) {
  const [status, setStatus] = useState<BackupStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [revision, setRevision] = useState(0);
  const [loading, setLoading] = useState(true);
  const authority = `${controller.state.capabilities?.library_id}:${controller.state.capabilities?.runtime_generation}:${controller.state.session?.principal_id}`;
  useEffect(() => { setStatus(null); setError(null); }, [authority]);
  useEffect(() => {
    let active = true;
    setLoading(true);
    void controller.client.serviceStatus().then(value => {
      const next = readBackupStatus(value);
      if (active) { setStatus(next); setError(null); }
    }).catch(cause => { if (active) setError(cause instanceof Error ? cause.message : String(cause)); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [controller, authority, revision]);
  return <section className="screen backup-screen" data-backup-screen>
    <h1>백업 및 내보내기</h1>
    <p>백업·보호 복원·개인 기록 내보내기는 해룡 daemon이 실행되는 호스트의 로컬 CLI에서 수행합니다.</p>
    <p>브라우저와 데스크톱의 디렉터리 다운로드는 선택한 버전의 원본 파일을 받는 기능입니다.</p>
    <button disabled={loading} onClick={() => setRevision(value => value + 1)}>백업 상태 새로 고침</button>
    {loading && <p role="status">백업 상태 확인 중…</p>}
    {error && <p role="alert">{error}{status ? " 마지막으로 확인한 상태를 표시합니다." : ""}</p>}
    {status && <div data-backup-status>
      <p>{backupSummary(status)}</p>
      <p>생성 방식: 수동. 자동 백업 일정은 설정되지 않았습니다.</p>
      {status.last_complete_verified && <dl><dt>백업 세트</dt><dd>{status.last_complete_verified.set_id}</dd><dt>마지막 checksum 검증</dt><dd>{new Date(status.last_complete_verified.verified_at).toLocaleString()}</dd><dt>완료 후 경과</dt><dd>{Math.floor(status.last_complete_verified.age_seconds / 3600)}시간</dd></dl>}
      {status.last_attempt && <p>마지막 시도: {backupAttemptLabel(status.last_attempt.state)} · {backupAttemptLabel(status.last_attempt.stage)}{status.last_attempt.error ? ` · ${status.last_attempt.error}` : ""}. 실패하거나 중단된 시도는 이전 완전한 백업을 대체하지 않습니다.</p>}
      <p>실제 복원 검증: {status.last_trial_restore.state === "not_run" ? "실행하지 않음" : status.last_trial_restore.state === "passed" ? "통과" : "실패"}{status.last_trial_restore.completed_at ? ` · ${new Date(status.last_trial_restore.completed_at).toLocaleString()}` : ""}. Checksum 검증과 실제 복원 검증은 서로 다른 기록입니다.</p>
    </div>}
    <h2>호스트에서 실행</h2>
    <p>아래의 BACKUP_DIR, SET, DEST, VERSION_ID, OUTPUT은 직접 선택한 값으로 바꾸세요. 원격 endpoint에서는 실행할 수 없습니다.</p>
    <pre>haeryong backup create --destination BACKUP_DIR --profile DEST{`\n`}haeryong backup verify --input SET{`\n`}haeryong export create --version VERSION_ID --format directory --destination OUTPUT --profile DEST</pre>
    <p>Notes·Review 등을 포함하려면 export plan에서 --records를 명시하고 별도 metadata 위치를 선택하세요. 추가 보관 소스는 --include-retained-sources로 선택하며, 암호화는 현재 지원하지 않습니다.</p>
    <p>보호 복원은 daemon을 정상 중지한 후 restore plan --input SET --profile DEST로 영향을 확인하고 restore apply PLAN_ID --profile DEST --confirm으로 실행합니다. 모든 이전 세션은 무효화됩니다. 새 bootstrap 후 restore review로 소스·제공자·외부 노출·자동화·삭제 정책을 검토하세요.</p>
    <p>새 백업 생성은 영구 삭제의 필수 조건이 아닙니다. 같은 볼륨의 사본은 볼륨 전체 손실을 보호하지 않으며, checksum은 제삼자가 바꾼 사본의 진위를 보장하지 않습니다.</p>
  </section>;
}
