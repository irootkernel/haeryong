package sqlite

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
)

// ReferenceSearchTerminal is deliberately independent of search index coverage:
// failed/cancelled work is terminal but cannot certify a missing anchor.
func (t *writeTx) ReferenceSearchTerminal(version string) (bool, error) {
	var blocked bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM derived_work w JOIN jobs j USING(job_id) WHERE w.version_id=? AND w.work_kind='search' AND w.extractor_revision=? AND j.state NOT IN ('completed','failed','cancelled'))`, version, domain.SearchExtractorRevision).Scan(&blocked)
	return !blocked, err
}
func (t *writeTx) EnqueueReferences(version string, revision int64) (string, error) {
	return t.enqueueReferences(version, revision, false)
}
func (t *writeTx) enqueueReferences(version string, revision int64, rebuild bool) (string, error) {
	var id string
	err := t.tx.QueryRowContext(t.ctx, `SELECT job_id FROM derived_work WHERE version_id=? AND work_kind='references' AND extractor_revision=?`, version, revision).Scan(&id)
	if err == nil {
		return id, nil
	} // terminal same-key jobs never re-enter
	if !errors.Is(notFound(err), domain.ErrNotFound) {
		return "", err
	}
	v, err := t.PublishedVersion(version)
	if err != nil {
		return "", err
	}
	d, err := t.Document(v.DocumentID)
	if err != nil {
		return "", err
	}
	p, err := t.Scope(d.ProjectID)
	if err != nil {
		return "", err
	}
	if d.Lifecycle == "trash" || d.Lifecycle == "purged" || p.Project.Lifecycle == "trash" || p.Project.Lifecycle == "purged" {
		return "", nil
	}
	l, err := t.Library()
	if err != nil {
		return "", err
	}
	id, err = service.NewID()
	if err != nil {
		return "", err
	}
	capture := domain.SearchMaintenanceScope{Actor: "system", LibraryID: l.ID, ProjectID: d.ProjectID, DocumentID: d.ID, VersionID: v.ID, ContentSetID: v.ContentSetID, PayloadSHA: v.PayloadSHA, ManifestSHA: v.ManifestSHA, ExtractorRevision: revision}
	raw, err := json.Marshal(capture)
	if err != nil {
		return "", err
	}
	if err = t.CreateJob(domain.Job{ID: id, Kind: "derived_index", Scope: raw, ConfigRevision: p.ConfigRevision, Permissions: map[string]int64{d.ProjectID: p.Project.PermissionsRevision}}); err != nil {
		return "", err
	}
	if err = t.PutDerivedWork(domain.DerivedWork{JobID: id, VersionID: v.ID, WorkKind: "references", ExtractorRevision: revision}); err != nil {
		return "", err
	}
	old, err := t.ReferenceMaterialization(v.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return "", err
	}
	if rebuild && old.JobID != "" && old.ReferenceRevision == revision {
		previous, e := t.Job(old.JobID)
		if e != nil {
			return "", e
		}
		if !terminal(previous.State) || old.ManifestSHA != v.ManifestSHA || old.ReferenceRevision != revision {
			return "", domain.ErrConflict
		}
		err = t.exec(`UPDATE reference_materializations SET job_id=?,state='building',revision=revision+1 WHERE from_version_id=? AND job_id=? AND revision=?`, id, v.ID, old.JobID, old.Revision)
	} else {
		err = t.PutReferenceMaterialization(domain.ReferenceMaterialization{FromVersionID: v.ID, ManifestSHA: v.ManifestSHA, ReferenceRevision: revision, Revision: old.Revision + 1, State: "building", JobID: id})
	}
	if err != nil {
		return "", err
	}
	return id, t.ReferenceProgress(id, "inventory", "", 0, 0)
}
func (t *writeTx) referenceAuthority(j domain.Job, w domain.DerivedWork) error {
	if w.WorkKind != "references" {
		return domain.ErrTransition
	}
	m, err := t.ReferenceMaterialization(w.VersionID)
	if err != nil {
		return err
	}
	if m.JobID != j.ID || m.ReferenceRevision != w.ExtractorRevision {
		return domain.ErrStaleWorker
	}
	var capture domain.SearchMaintenanceScope
	if json.Unmarshal(j.Scope, &capture) != nil {
		return domain.ErrDenied
	}
	v, err := t.PublishedVersion(w.VersionID)
	if err != nil {
		return err
	}
	d, err := t.Document(v.DocumentID)
	if err != nil {
		return err
	}
	p, err := t.Scope(d.ProjectID)
	if err != nil {
		return err
	}
	l, err := t.Library()
	if err != nil {
		return err
	}
	if capture.Actor != "system" || capture.LibraryID != l.ID || capture.ProjectID != d.ProjectID || capture.DocumentID != d.ID || capture.VersionID != v.ID || capture.ContentSetID != v.ContentSetID || capture.PayloadSHA != v.PayloadSHA || capture.ManifestSHA != v.ManifestSHA || capture.ExtractorRevision != w.ExtractorRevision {
		return domain.ErrDenied
	}
	if d.Lifecycle == "trash" || d.Lifecycle == "purged" || p.Project.Lifecycle == "trash" || p.Project.Lifecycle == "purged" || j.Permissions[d.ProjectID] != p.Project.PermissionsRevision {
		return domain.ErrSearchRestricted
	}
	return nil
}
func (t *writeTx) ReferenceFence(id string, generation int64) (domain.DerivedWork, error) {
	w, err := t.DerivedWork(id)
	if err != nil {
		return w, err
	}
	j, err := t.Job(id)
	if err != nil {
		return w, err
	}
	if j.Kind != "derived_index" || j.State != "running" || j.CancelRequested || j.AttemptGeneration != generation {
		return w, domain.ErrStaleWorker
	}
	if err = t.referenceAuthority(j, w); err != nil {
		return w, err
	}
	ready, err := t.ReferenceSearchTerminal(w.VersionID)
	if err != nil {
		return w, err
	}
	if !ready {
		return w, domain.ErrStaleWorker
	}
	return w, nil
}
func (t *writeTx) SuspendReferences(id string, generation int64) error {
	j, err := t.Job(id)
	if err != nil {
		return err
	}
	if j.State != "running" || j.CancelRequested || j.AttemptGeneration != generation {
		return domain.ErrStaleWorker
	}
	if err = t.exec(`UPDATE jobs SET state='paused',attempt_generation=attempt_generation+1,revision=revision+1 WHERE job_id=?`, id); err != nil {
		return err
	}
	return t.jobUpdated(id)
}
func (t *writeTx) ResumeReferences(id string, generation int64) error {
	j, err := t.Job(id)
	if err != nil {
		return err
	}
	if j.State != "paused" || j.CancelRequested || j.AttemptGeneration != generation {
		return domain.ErrStaleWorker
	}
	w, err := t.DerivedWork(id)
	if err != nil {
		return err
	}
	if err = t.referenceAuthority(j, w); err != nil {
		return err
	}
	if err = t.exec(`UPDATE jobs SET state='queued',revision=revision+1 WHERE job_id=?`, id); err != nil {
		return err
	}
	return t.jobUpdated(id)
}
func (t *writeTx) CompleteReferences(id string) error { return t.setReferencesState(id, "ready") }
func (t *writeTx) FailReferences(id string) error     { return t.setReferencesState(id, "failed") }
func (t *writeTx) setReferencesState(id, state string) error {
	w, err := t.DerivedWork(id)
	if err != nil {
		return err
	}
	m, err := t.ReferenceMaterialization(w.VersionID)
	if err != nil {
		return err
	}
	if m.JobID != id || m.State != "building" {
		return nil
	}
	m.State = state
	m.Revision++
	if err = t.PutReferenceMaterialization(m); err != nil {
		return err
	}
	return t.appendEvent("version", w.VersionID, "references.updated", m.Revision, nil)
}
func (t *writeTx) ReferenceProgress(id, stage, checkpoint string, offset, size int64) error {
	w, err := t.DerivedWork(id)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(id + ":" + checkpoint))
	files := "0"
	if stage != "inventory" {
		files = "1"
		offset = size
	}
	total, bytes := "1", fmt.Sprint(size)
	progress := domain.SearchProgress{VersionID: w.VersionID, WorkKind: "references", Stage: stage, FilesDone: files, FilesTotal: &total, BytesDone: fmt.Sprint(offset), BytesTotal: &bytes, PagesDone: "0", Failed: "0", Checkpoint: fmt.Sprintf("%x", sum)}
	if size == 0 {
		progress.BytesTotal = nil
	}
	j, err := t.Job(id)
	if err != nil {
		return err
	}
	if j.State == "failed" || j.State == "cancelled" {
		// Stop reports preserve the last committed source-only counters. A
		// failed read is not a completed manifest, even at terminal stage.
		if len(j.Result) > 0 {
			var previous domain.SearchProgress
			if err = json.Unmarshal(j.Result, &previous); err != nil {
				return err
			}
			progress = previous
		}
		progress.Stage = "complete"
		progress.Checkpoint = fmt.Sprintf("%x", sum)
		progress.Failed = "1"
	}
	raw, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	if err = t.SetJobResult(id, raw); err != nil {
		return err
	}
	if err = t.exec(`UPDATE jobs SET revision=revision+1 WHERE job_id=?`, id); err != nil {
		return err
	}
	return t.jobUpdated(id)
}

func (t *writeTx) ClearReferenceData(version string, limit int) (bool, error) {
	for _, table := range []string{"authored_edges", "authored_references"} {
		result, err := t.tx.ExecContext(t.ctx, `DELETE FROM `+table+` WHERE rowid IN (SELECT rowid FROM `+table+` WHERE from_version_id=? LIMIT ?)`, version, batch(limit))
		if err != nil {
			return false, err
		}
		n, err := result.RowsAffected()
		if err != nil || n > 0 {
			return false, err
		}
	}
	return true, t.exec(`DELETE FROM reference_materializations WHERE from_version_id=?`, version)
}
