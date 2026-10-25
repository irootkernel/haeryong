import test from "node:test";
import assert from "node:assert/strict";
import { backupLine, backupSummary, readBackupStatus } from "./backup-state.ts";
import type { BackupStatus } from "../../client/types.ts";

test("backup absence, failed replacement and actual trial restore remain separate", () => {
  const status: BackupStatus = { mode: "manual", available: true, last_complete_verified: null, last_attempt: null, last_trial_restore: { state: "not_run", completed_at: null } };
  assert.match(backupSummary(status), /아직 생성/);
  status.last_complete_verified = { set_id: "set", completed_at: "2026-10-25T00:00:00Z", verified_at: "2026-10-25T00:00:00Z", age_seconds: 10 };
  status.last_attempt = { job_id: "failed", state: "failed", stage: "copying", completed_at: null, error: "insufficient_space" };
  assert.match(backupSummary(status), /마지막 완전한/);
  assert.equal(readBackupStatus({ backup: status }).last_trial_restore.state, "not_run");
  assert.throws(() => readBackupStatus({ backup: {} }));
  assert.match(backupLine({ state: "not_created", set_id: null, last_complete_at: null, reason: null, last_attempt: null, last_trial_restore: status.last_trial_restore }), /아직 생성/);
  assert.match(backupLine(undefined), /제공할 수 없습니다/);
});
