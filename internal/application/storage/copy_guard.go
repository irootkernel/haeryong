package storage

import (
	"haeryong.local/haeryong/internal/domain"
	"time"
)

// CopyOwner separates current attempt ownership from authority to publish.
// It permits recording cessation after permission/hold revocation, never after
// runtime/owner replacement, linked retry or terminal set mutation.
func CopyOwner(tx WriteTx, set domain.CopySet, jobID string, generation int64) (domain.Job, error) {
	current, e := tx.CopySet(set.ID)
	if e != nil {
		return domain.Job{}, e
	}
	if set.JobID != jobID || current.JobID != jobID || current.Stage == "complete" || current.Stage == "abandoned" || current.Stage == "expired" || current.Stage == "restored_paused" {
		return domain.Job{}, domain.ErrStaleWorker
	}
	job, e := tx.Job(set.JobID)
	if e != nil {
		return job, e
	}
	if job.ID != set.JobID || job.AttemptGeneration != generation {
		return job, domain.ErrStaleWorker
	}
	lib, e := tx.Library()
	if e != nil {
		return job, e
	}
	owner, e := tx.AuthState()
	if e != nil {
		return job, e
	}
	if current.LibraryID != set.LibraryID || current.Runtime != set.Runtime || current.OwnerID != set.OwnerID || lib.ID != set.LibraryID || lib.RuntimeGeneration != set.Runtime || owner.OwnerID != set.OwnerID {
		return job, domain.ErrReconciliation
	}
	return job, nil
}

// GuardCopy fences every detached checkpoint and publication against both the
// captured owner and the current execution/publication authority.
func GuardCopy(tx WriteTx, set domain.CopySet, jobID string, generation int64) error {
	job, e := CopyOwner(tx, set, jobID, generation)
	if e != nil {
		return e
	}
	if job.State != "running" || job.CancelRequested {
		return domain.ErrStaleWorker
	}
	hold, e := tx.RecoveryHold()
	if e != nil {
		return e
	}
	if hold != nil && hold.State != "reviewed" {
		return domain.ErrReconciliation
	}
	for id, revision := range set.Permissions {
		scope, e := tx.Scope(id)
		if e != nil {
			return e
		}
		if scope.Project.PermissionsRevision != revision {
			return domain.ErrDenied
		}
	}
	if set.Kind == "export" && set.Request.RetainedSources {
		actor := domain.Session{PrincipalID: set.OwnerID, Scope: domain.CredentialScope{Kind: "owner"}}
		for _, id := range set.RetainedReadIDs {
			r, e := tx.SourceCaptureRead(id)
			if e != nil {
				return e
			}
			c, e := tx.SourceCapture(r.CaptureID)
			if e != nil {
				return e
			}
			if e = CheckSourceReadAuthority(tx, actor, id, c.ProjectID, ""); e != nil {
				return e
			}
		}
	}

	return nil
}

// FailCopy is used only before byte work starts or after its owned handles drain.
// No capture barrier, checkpoint or pin is released by truthful failure recording.
func FailCopy(tx WriteTx, set domain.CopySet, jobID string, generation int64, code string) error {
	job, e := CopyOwner(tx, set, jobID, generation)
	if e != nil {
		return e
	}
	if job.CancelRequested {
		return tx.ConfirmStop(job.ID, job.AttemptGeneration)
	}
	if job.State == "failed" || job.State == "cancelled" || job.State == "completed" {
		return nil
	}
	if job.State != "running" {
		return domain.ErrStaleWorker
	}
	current, e := tx.CopySet(set.ID)
	if e != nil {
		return e
	}
	current.Stage = "failed"
	current.Error = code
	current.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if e = tx.PutCopySet(current); e != nil {
		return e
	}
	return tx.FinishJob(job.ID, generation, code, nil)
}
