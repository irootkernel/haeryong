package sqlite

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	"haeryong.local/haeryong/internal/domain"
)

func (t *writeTx) CopyBackupStatus() (domain.BackupStatus, error) {
	out := domain.BackupStatus{Mode: "manual", Available: true, LastTrial: domain.TrialRestore{State: "not_run"}}
	after := ""
	var latest, complete time.Time
	for {
		sets, e := t.CopySets("backup", after, 64)
		if e != nil {
			return out, e
		}
		if len(sets) == 0 {
			break
		}
		for _, set := range sets {
			attempt := set.AttemptAt
			if attempt == "" {
				attempt = set.CreatedAt
			}
			created, e := time.Parse(time.RFC3339Nano, attempt)
			if e != nil {
				return out, e
			}
			if created.After(latest) {
				latest = created
				j, e := t.Job(set.JobID)
				if e != nil {
					return out, e
				}
				var stamp *string
				if set.CompletedAt != "" {
					v := set.CompletedAt
					stamp = &v
				}
				out.LastAttempt = &domain.BackupAttempt{JobID: j.ID, State: j.State, Stage: set.Stage, CompletedAt: stamp, Error: set.Error}
			}
			if set.Stage == "complete" {
				stamp, e := time.Parse(time.RFC3339Nano, set.CompletedAt)
				if e != nil {
					return out, e
				}
				if stamp.After(complete) {
					complete = stamp
					out.LastComplete = &domain.BackupComplete{ID: set.ID, CompletedAt: set.CompletedAt, VerifiedAt: set.VerifiedAt, Age: max(0, int64(time.Since(stamp).Seconds()))}
					out.Available = true
					out.Reason = ""
					if _, e = os.Stat(set.Directory); e != nil {
						out.Available = false
						out.Reason = "destination_unavailable"
					}
				}
			}
			after = set.ID
		}
	}
	r, e := t.ResourceRow("backup_trial_restore", "last")
	if e == nil {
		if e = json.Unmarshal(r.Value, &out.LastTrial); e != nil {
			return out, e
		}
	} else if !errors.Is(e, domain.ErrNotFound) {
		return out, e
	}
	return out, nil
}
