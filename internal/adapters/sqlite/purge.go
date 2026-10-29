package sqlite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"strings"
	"time"
)

func (t *writeTx) TombstonedVersion(id string) (bool, error) {
	var n int
	err := t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM purged_versions WHERE version_id=?`, id).Scan(&n)
	return n != 0, err
}

// PurgeImpact adds deletion-relevant ownership to the ordinary lifecycle view.
// Hashes and row revisions, never private bodies, bind confirmation freshness.
func (t *writeTx) PurgeImpact(document string) ([]domain.LifecycleItem, error) {
	items, err := t.LifecycleImpact(document)
	if err != nil {
		return nil, err
	}
	rows, err := t.tx.QueryContext(t.ctx, `SELECT context_id,version_id,page_path,content_hash FROM contexts WHERE version_id IN (SELECT version_id FROM document_versions WHERE document_id=?) ORDER BY context_id`, document)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, v, h string
		var page *string
		if err = rows.Scan(&id, &v, &page, &h); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, domain.LifecycleItem{Kind: "context_object", ID: id + "/" + h, VersionID: &v, PagePath: page})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Values are hashed inside the serialized planning/commit transaction. Include
	// all history body/context bindings and explicit source/derived private rows.
	for _, q := range []struct{ kind, sql string }{
		{"annotation_value", `SELECT a.value FROM annotations a WHERE a.document_id=? ORDER BY annotation_id`},
		{"annotation_revision", `SELECT h.value FROM annotation_history h JOIN annotations a USING(annotation_id) WHERE a.document_id=? ORDER BY h.annotation_id,h.revision`},
		{"source_history", `SELECT value FROM (SELECT json_object('id',verification_id,'excerpt',hex(expected_excerpt),'ref',expected_excerpt_ref,'read',read_id,'outcome',outcome) AS value FROM citation_verifications WHERE version_id IN(SELECT version_id FROM document_versions WHERE document_id=?) ORDER BY verification_id)`},
		{"managed_source_snapshot", `SELECT json_object('capture',c.capture_id,'read',r.read_id,'hash',r.content_hash,'retention',r.retention_mode,'availability',r.availability) FROM source_capture_reads r JOIN source_captures c ON c.capture_id=r.capture_id WHERE json_extract(c.requested_selection,'$.version_id') IN(SELECT version_id FROM document_versions WHERE document_id=?) AND EXISTS(SELECT 1 FROM object_references own WHERE own.owner_kind='selected_snapshot' AND own.owner_id=r.read_id AND own.content_hash=r.content_hash) ORDER BY c.capture_id,r.read_id`},
	} {
		rs, e := t.tx.QueryContext(t.ctx, q.sql, document)
		if e != nil {
			return nil, e
		}
		for rs.Next() {
			var raw []byte
			if e = rs.Scan(&raw); e != nil {
				rs.Close()
				return nil, e
			}
			sum := sha256.Sum256(raw)
			items = append(items, domain.LifecycleItem{Kind: q.kind, ID: hex.EncodeToString(sum[:])})
		}
		e = rs.Err()
		rs.Close()
		if e != nil {
			return nil, e
		}
	}
	return items, nil
}

func (t *writeTx) StartPurge(p domain.LifecyclePlan, r domain.LifecycleResult) error {
	lib, err := t.Library()
	if err != nil {
		return err
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	i := domain.PurgeIntent{JobID: r.JobID, PlanID: p.ID, DocumentID: p.DocumentID, OwnerID: p.OwnerID, Runtime: lib.RuntimeGeneration, LibraryID: lib.ID, Revision: r.Revision, DeletedAt: stamp}
	if err = t.CreateJob(domain.Job{ID: r.JobID, Kind: "lifecycle", Scope: jsonBytes(map[string]any{"purge_plan_id": p.ID, "owner_id": p.OwnerID}), ConfigRevision: p.ConfigRevision}); err != nil {
		return err
	}
	if err = t.exec(`INSERT INTO purge_intents VALUES(?,?,?,?)`, r.JobID, p.ID, p.DocumentID, jsonBytes(i)); err != nil {
		return err
	}
	return t.exec(`INSERT INTO purged_versions SELECT version_id,document_id,? FROM document_versions WHERE document_id=?`, stamp, p.DocumentID)
}
func (t *writeTx) PurgeIntent(id string) (domain.PurgeIntent, error) {
	var i domain.PurgeIntent
	var b []byte
	err := t.tx.QueryRowContext(t.ctx, `SELECT value FROM purge_intents WHERE job_id=?`, id).Scan(&b)
	if err != nil {
		return i, notFound(err)
	}
	err = json.Unmarshal(b, &i)
	return i, err
}
func (t *writeTx) savePurge(i domain.PurgeIntent) error {
	return t.exec(`UPDATE purge_intents SET value=? WHERE job_id=?`, jsonBytes(i), i.JobID)
}
func (t *writeTx) purgeGuard(i domain.PurgeIntent) error {
	if hold, e := t.RecoveryHold(); e != nil {
		return e
	} else if hold != nil && hold.State != "reviewed" {
		return domain.ErrPrecondition
	}
	a, err := t.AuthState()
	if err != nil {
		return err
	}
	l, err := t.Library()
	if err != nil {
		return err
	}
	d, err := t.Document(i.DocumentID)
	if err != nil {
		return err
	}
	if a.OwnerID != i.OwnerID || l.ID != i.LibraryID || l.RuntimeGeneration != i.Runtime || d.Lifecycle != "purged" || d.Revision != i.Revision {
		return domain.ErrPrecondition
	}
	p, err := t.LifecyclePlan(i.PlanID)
	if err != nil {
		return err
	}
	if p.State != "committed" || p.OwnerID != i.OwnerID || p.DocumentID != i.DocumentID || p.Operation != "purge" || p.Result == nil || p.Result.JobID != i.JobID {
		return domain.ErrPrecondition
	}
	var changed int
	err = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM document_versions WHERE document_id=? AND version_id NOT IN(SELECT version_id FROM purged_versions WHERE document_id=?)`, i.DocumentID, i.DocumentID).Scan(&changed)
	if err != nil {
		return err
	}
	if changed != 0 {
		return domain.ErrPrecondition
	}
	return nil
}
func (t *writeTx) ContinuePurge(id string, revision, generation int64) (domain.Job, error) {
	j, err := t.Job(id)
	if err != nil {
		return j, err
	}
	if j.Kind != "lifecycle" || terminal(j.State) || j.CancelRequested || j.Revision != revision || j.AttemptGeneration != generation {
		return j, domain.ErrStaleWorker
	}
	i, err := t.PurgeIntent(id)
	if err != nil {
		return j, err
	}
	if err = t.purgeGuard(i); err != nil {
		return j, err
	}
	if j.State == "running" {
		return j, nil
	}
	j.AttemptGeneration++
	j.Revision++
	j.State = "running"
	if err = t.exec(`UPDATE jobs SET state='running',attempt_generation=?,revision=?,error='' WHERE job_id=?`, j.AttemptGeneration, j.Revision, id); err != nil {
		return j, err
	}
	return j, t.jobUpdated(id)
}
func (t *writeTx) PausePurge(id string, generation int64, reason, next string) error {
	j, err := t.Job(id)
	if err != nil {
		return err
	}
	if terminal(j.State) || j.AttemptGeneration != generation {
		return domain.ErrStaleWorker
	}
	i, err := t.PurgeIntent(id)
	if err != nil {
		return err
	}
	i.Reason = reason
	i.NextRetry = next
	i.Retry++
	if err = t.savePurge(i); err != nil {
		return err
	}
	if err = t.exec(`UPDATE jobs SET state='paused',error=?,revision=revision+1 WHERE job_id=?`, reason, id); err != nil {
		return err
	}
	return t.purgeProgress(i)
}
func (t *writeTx) purgeProgress(i domain.PurgeIntent) error {
	if err := t.exec(`UPDATE jobs SET revision=revision+1 WHERE job_id=?`, i.JobID); err != nil {
		return err
	}
	var remaining int
	if err := t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM purge_candidates WHERE job_id=?`, i.JobID).Scan(&remaining); err != nil {
		return err
	}
	if err := t.SetJobResult(i.JobID, jsonBytes(map[string]any{"purge_plan_id": i.PlanID, "stage": purgeStage(i), "reason": i.Reason, "next_retry_at": i.NextRetry, "remaining": remaining, "metadata_removed": i.MetadataRemoved})); err != nil {
		return err
	}
	return t.jobUpdated(i.JobID)
}

// Child-first bounded deletes. Target selectors use the permanent inventory so
// their authority survives removal of the private version rows.
var purgeDeletes = []struct{ table, where string }{
	{"annotation_history", `annotation_id IN(SELECT annotation_id FROM annotations WHERE document_id=?)`},
	{"annotations", `document_id=?`},
	{"review_states", `version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?)`},
	{"review_events", `version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?)`},
	{"revisit_flags", `document_id=?`},
	{"resource_revisions", `resource_kind IN('reading_position','page_position','reader_preference') AND EXISTS(SELECT 1 FROM purged_versions v CROSS JOIN auth_owner o WHERE v.document_id=? AND (resource_id=o.principal_id||'/'||v.version_id OR substr(resource_id,1,length(o.principal_id)+length(v.version_id)+2)=o.principal_id||'/'||v.version_id||'/'))`},
	{"assessment_units", `coverage_set_id IN(SELECT coverage_set_id FROM coverage_sets WHERE version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?))`},
	{"required_units", `coverage_set_id IN(SELECT coverage_set_id FROM coverage_sets WHERE version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?))`},
	{"assessments", `version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?)`},
	{"coverage_sets", `version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?)`},
	{"reassessment_triggers", `version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?)`},
	{"source_observations", `version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?)`},
	{"observation_runs", `version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?)`},
	{"citation_verifications", `version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?)`},
	{"region_evidence", `version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?)`},
	{"evidence", `version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?)`},
	{"regions", `version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?)`},
	{"page_anchors", `version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?)`},
	{"version_pages", `version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?)`},
	{"version_sources", `version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?)`},
}

func (t *writeTx) candidateSelect(id, query string, args ...any) error {
	return t.exec(`INSERT OR IGNORE INTO purge_candidates SELECT ?,o.content_hash,o.byte_size FROM content_objects o WHERE `+query, append([]any{id}, args...)...)
}

// Detach a note revision's own references only; every historical revision is
// visited before deleting its row, including revisions of soft-deleted notes.
func (t *writeTx) detachHistory(id, document string, limit int) error {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT h.annotation_id||'/'||h.revision FROM annotation_history h JOIN annotations a USING(annotation_id) WHERE a.document_id=? ORDER BY h.annotation_id,h.revision LIMIT ?`, document, limit)
	if err != nil {
		return err
	}
	var owners []string
	for rows.Next() {
		var o string
		if err = rows.Scan(&o); err != nil {
			rows.Close()
			return err
		}
		owners = append(owners, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, owner := range owners {
		if err = t.candidateSelect(id, `o.content_hash IN(SELECT content_hash FROM object_references WHERE owner_kind='annotation_history' AND owner_id=? UNION SELECT content_hash FROM object_pins WHERE owner_kind='annotation_history' AND owner_id=?)`, owner, owner); err != nil {
			return err
		}
		if err = t.exec(`DELETE FROM object_references WHERE owner_kind='annotation_history' AND owner_id=?`, owner); err != nil {
			return err
		}
		if err = t.ReleaseOwnerPins("annotation_history", owner); err != nil {
			return err
		}
	}
	return nil
}

func (t *writeTx) detachVerifications(id, document string, limit int) error {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT verification_id FROM citation_verifications WHERE version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?) ORDER BY verification_id LIMIT ?`, document, limit)
	if err != nil {
		return err
	}
	var owners []string
	for rows.Next() {
		var owner string
		if err = rows.Scan(&owner); err != nil {
			rows.Close()
			return err
		}
		owners = append(owners, owner)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, owner := range owners {
		if err = t.candidateSelect(id, `o.content_hash IN(SELECT content_hash FROM object_references WHERE owner_kind='verification' AND owner_id=?)`, owner); err != nil {
			return err
		}
		if err = t.exec(`DELETE FROM object_references WHERE owner_kind='verification' AND owner_id=?`, owner); err != nil {
			return err
		}
	}
	return nil
}

func (t *writeTx) PurgeStep(id string, generation int64, limit int) (bool, error) {
	j, err := t.Job(id)
	if err != nil {
		return false, err
	}
	if j.State != "running" || j.AttemptGeneration != generation {
		return false, domain.ErrStaleWorker
	}
	i, err := t.PurgeIntent(id)
	if err != nil {
		return false, err
	}
	if err = t.purgeGuard(i); err != nil {
		return false, err
	}
	// Do not remove a running worker's ownership ledger before it confirms
	// stop. The serial dispatcher gives accepted cancellation its normal turn.
	var active int
	if err = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM jobs WHERE state NOT IN('completed','failed','cancelled') AND job_id IN(SELECT job_id FROM derived_work WHERE version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?) UNION SELECT job_id FROM import_intents WHERE json_extract(value,'$.DocumentID')=? UNION SELECT job_id FROM jobs WHERE kind IN ('generation','question') AND (json_extract(scope,'$.target.document_id')=? OR json_extract(scope,'$.target.version_id') IN(SELECT version_id FROM purged_versions WHERE document_id=?)) UNION SELECT i.job_id FROM job_inputs i JOIN source_capture_reads r ON r.read_id=i.read_id JOIN source_captures c ON c.capture_id=r.capture_id WHERE json_extract(c.requested_selection,'$.version_id') IN(SELECT version_id FROM purged_versions WHERE document_id=?))`, i.DocumentID, i.DocumentID, i.DocumentID, i.DocumentID, i.DocumentID).Scan(&active); err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	if err = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM check_intents WHERE state='open' AND json_extract(canonical_target,'$.version_id') IN(SELECT version_id FROM purged_versions WHERE document_id=?)`, i.DocumentID).Scan(&active); err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	if err = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM source_captures WHERE state='capturing' AND json_extract(requested_selection,'$.version_id') IN(SELECT version_id FROM purged_versions WHERE document_id=?)`, i.DocumentID).Scan(&active); err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	if blocked, e := t.providerBlocksPurge(i.DocumentID); e != nil {
		return false, e
	} else if blocked {
		return false, nil
	}
	// Always finish derived cleanup before any private/FK parents disappear.
	var version string
	err = t.tx.QueryRowContext(t.ctx, `SELECT version_id FROM document_versions WHERE document_id=? AND (EXISTS(SELECT 1 FROM derived_work w WHERE w.version_id=document_versions.version_id) OR EXISTS(SELECT 1 FROM search_indexes s WHERE s.version_id=document_versions.version_id) OR EXISTS(SELECT 1 FROM reference_materializations r WHERE r.from_version_id=document_versions.version_id)) ORDER BY version_id LIMIT 1`, i.DocumentID).Scan(&version)
	if err == nil {
		_, err = t.DeleteDerivedVersion(version, limit)
		return false, err
	}
	if !errors.Is(notFound(err), domain.ErrNotFound) {
		return false, err
	}
	// Resolve receipt ownership while annotation/context identities still exist.
	if !i.ReceiptsMinimized {
		done, e := t.minimizePurgeReceipts(&i)
		if e != nil {
			return false, e
		}
		i.ReceiptsMinimized = done
		return false, t.savePurge(i)
	}
	if i.Phase < len(purgeDeletes) {
		if i.Phase == 0 {
			if err = t.detachHistory(id, i.DocumentID, limit); err != nil {
				return false, err
			}
		}
		d := purgeDeletes[i.Phase]
		order := ""
		if i.Phase == 0 {
			order = " ORDER BY annotation_id,revision"
		}
		if d.table == "citation_verifications" {
			if err = t.detachVerifications(id, i.DocumentID, limit); err != nil {
				return false, err
			}
			order = " ORDER BY verification_id"
		}
		result, err := t.tx.ExecContext(t.ctx, `DELETE FROM `+d.table+` WHERE rowid IN(SELECT rowid FROM `+d.table+` WHERE `+d.where+order+` LIMIT ?)`, i.DocumentID, limit)
		if err != nil {
			return false, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if n == 0 {
			i.Phase++
		}
		if err = t.savePurge(i); err != nil {
			return false, err
		}
		return false, t.purgeProgress(i)
	}
	// Version-context snapshot ownership ends with the explicit document
	// purge; standalone project snapshots and every other owner remain separate.
	if detached, e := t.detachManagedSnapshots(id, i.DocumentID, limit); e != nil {
		return false, e
	} else if detached {
		return false, nil
	}
	// Contexts are removed only after all saved-note owners are gone. Unknown and
	// independent pins/references remain and ReserveGC will protect their bytes.
	rows, err := t.tx.QueryContext(t.ctx, `SELECT context_id FROM contexts c WHERE version_id IN(SELECT version_id FROM purged_versions WHERE document_id=?) AND NOT EXISTS(SELECT 1 FROM annotation_history h WHERE json_extract(h.value,'$.context_ref')=c.context_id) LIMIT ?`, i.DocumentID, limit)
	if err != nil {
		return false, err
	}
	var contexts []string
	for rows.Next() {
		var c string
		if err = rows.Scan(&c); err != nil {
			rows.Close()
			return false, err
		}
		contexts = append(contexts, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, c := range contexts {
		if err = t.candidateSelect(id, `o.content_hash IN(SELECT content_hash FROM contexts WHERE context_id=?)`, c); err != nil {
			return false, err
		}
		if err = t.ReleaseOwnerPins("context", c); err != nil {
			return false, err
		}
		if err = t.exec(`DELETE FROM contexts WHERE context_id=?`, c); err != nil {
			return false, err
		}
	}
	if len(contexts) > 0 {
		return false, nil
	}
	// Terminal import indexes no longer own these sets. Remove them before
	// deciding which payload ownership is still live.
	if err = t.exec(`DELETE FROM metadata_indexes WHERE job_id IN(SELECT job_id FROM import_intents WHERE json_extract(value,'$.DocumentID')=? AND job_id IN(SELECT job_id FROM jobs WHERE state IN('completed','failed','cancelled')))`, i.DocumentID); err != nil {
		return false, err
	}
	if i.PendingSet != "" {
		return false, t.reclaimPurgeSet(&i, limit)
	}

	// Remove at most one whole version's final metadata per turn. Entries/sets
	// stay while a retained version or resumable upload still owns the set.
	err = t.tx.QueryRowContext(t.ctx, `SELECT version_id FROM document_versions WHERE document_id=? ORDER BY version_id LIMIT 1`, i.DocumentID).Scan(&version)
	if err == nil {
		var set string
		if err = t.tx.QueryRowContext(t.ctx, `SELECT content_set_id FROM document_versions WHERE version_id=?`, version).Scan(&set); err != nil {
			return false, err
		}
		if err = t.candidateSelect(id, `o.content_hash IN(SELECT content_hash FROM object_references WHERE owner_kind IN('version_manifest','version_metadata') AND owner_id=?)`, version); err != nil {
			return false, err
		}
		if err = t.exec(`DELETE FROM object_references WHERE owner_kind IN('version_manifest','version_metadata') AND owner_id=?`, version); err != nil {
			return false, err
		}
		if err = t.exec(`UPDATE document_versions SET base_version_id=NULL WHERE document_id=?`, i.DocumentID); err != nil {
			return false, err
		}
		if err = t.exec(`DELETE FROM document_versions WHERE version_id=?`, version); err != nil {
			return false, err
		}
		i.PendingSet = set
		if err = t.savePurge(i); err != nil {
			return false, err
		}
		if err = t.reclaimPurgeSet(&i, limit); err != nil {
			return false, err
		}
		return false, nil
	}
	if !errors.Is(notFound(err), domain.ErrNotFound) {
		return false, err
	}
	if err = t.exec(`DELETE FROM import_intents WHERE json_extract(value,'$.DocumentID')=? AND job_id IN(SELECT job_id FROM jobs WHERE state IN('completed','failed','cancelled'))`, i.DocumentID); err != nil {
		return false, err
	}
	if !i.MetadataRemoved {
		i.MetadataRemoved = true
		i.Reason = ""
		i.NextRetry = ""
		if err = t.savePurge(i); err != nil {
			return false, err
		}
		if err = t.purgeProgress(i); err != nil {
			return false, err
		}
	}

	// Sets retained by another live owner are durable work, not forgotten
	// candidates. A later due turn rechecks them after that owner releases.
	for n, set := range i.RetainedSets {
		if n == 64 {
			break
		}
		retained, e := t.purgeSetOwners(set)
		if e != nil {
			return false, e
		}
		if retained == 0 {
			i.RetainedSets = append(i.RetainedSets[:n], i.RetainedSets[n+1:]...)
			i.PendingSet, i.SetAfterPath = set, ""
			return false, t.savePurge(i)
		}
	}
	if len(i.RetainedSets) > 64 {
		i.RetainedSets = append(i.RetainedSets[64:], i.RetainedSets[:64]...)
		if err = t.savePurge(i); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (t *writeTx) purgeSetOwners(set string) (int, error) {
	var retained int
	err := t.tx.QueryRowContext(t.ctx, `SELECT (SELECT count(*) FROM document_versions WHERE content_set_id=?)+(SELECT count(*) FROM uploads WHERE sealed_content_set_id=? AND state<>'abandoned')+(SELECT count(*) FROM metadata_indexes WHERE content_set_id=?)+(SELECT count(*) FROM import_intents i JOIN jobs j USING(job_id) WHERE json_extract(i.value,'$.ContentSetID')=? AND j.state NOT IN('completed','failed','cancelled'))`, set, set, set, set).Scan(&retained)
	return retained, err
}

func (t *writeTx) reclaimPurgeSet(i *domain.PurgeIntent, limit int) error {
	set := i.PendingSet
	retained, err := t.purgeSetOwners(set)
	if err != nil {
		return err
	}
	entries, err := t.Entries(set, i.SetAfterPath, limit)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Kind == "file" {
			if err = t.candidateSelect(i.JobID, `o.content_hash=?`, e.Hash); err != nil {
				return err
			}
		}
		if retained == 0 {
			if err = t.exec(`DELETE FROM version_entries WHERE content_set_id=? AND relative_path=?`, set, e.Path); err != nil {
				return err
			}
		}
		i.SetAfterPath = e.Path
	}
	if len(entries) == 0 {
		if retained == 0 {
			var remaining int
			if err = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM version_entries WHERE content_set_id=?`, set).Scan(&remaining); err != nil {
				return err
			}
			if remaining > 0 {
				i.SetAfterPath = ""
				return t.savePurge(*i)
			}
			if err = t.exec(`DELETE FROM object_references WHERE owner_kind='content_set' AND owner_id=?`, set); err != nil {
				return err
			}
			if err = t.exec(`UPDATE uploads SET sealed_content_set_id=NULL WHERE state='abandoned' AND sealed_content_set_id=?`, set); err != nil {
				return err
			}
			if err = t.exec(`DELETE FROM content_sets WHERE content_set_id=?`, set); err != nil {
				return err
			}
		} else {
			found := false
			for _, previous := range i.RetainedSets {
				found = found || previous == set
			}
			if !found {
				i.RetainedSets = append(i.RetainedSets, set)
			}
		}
		i.PendingSet = ""
		i.SetAfterPath = ""
	}
	return t.savePurge(*i)
}

func (t *writeTx) PurgeCandidates(id string, limit int) ([]domain.Object, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT content_hash,byte_size FROM purge_candidates WHERE job_id=? ORDER BY (EXISTS(SELECT 1 FROM object_pins p WHERE p.content_hash=purge_candidates.content_hash) OR EXISTS(SELECT 1 FROM object_references r WHERE r.content_hash=purge_candidates.content_hash)),content_hash LIMIT ?`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Object{}
	for rows.Next() {
		var o domain.Object
		if err = rows.Scan(&o.Hash, &o.Size); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func (t *writeTx) PurgeCandidateDone(id, hash string) error {
	return t.exec(`DELETE FROM purge_candidates WHERE job_id=? AND content_hash=?`, id, hash)
}

// Ordinary target receipts remain inaccessible. Keep their original fingerprint,
// state and nonsecret comparison facts rather than forgetting uncertain work.
func (t *writeTx) minimizePurgeReceipts(i *domain.PurgeIntent) (bool, error) {
	removed, err := t.tx.ExecContext(t.ctx, `DELETE FROM lifecycle_plan_items WHERE rowid IN(SELECT rowid FROM lifecycle_plan_items WHERE plan_id IN(SELECT plan_id FROM lifecycle_plans WHERE document_id=?) OR json_extract(value,'$.source_document_id')=? LIMIT 64)`, i.DocumentID, i.DocumentID)
	if err != nil {
		return false, err
	}
	n, err := removed.RowsAffected()
	if err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}

	// Current/old lifecycle results contain no title/body. Other response copies
	// are addressed by exact target/result identities, never a title substring.
	rows, err := t.tx.QueryContext(t.ctx, `SELECT principal_id,operation_id FROM operation_receipts WHERE json_extract(normalization_context,'$.purged_document_id') IS NULL AND (principal_id,operation_id)>(?,?) ORDER BY principal_id,operation_id LIMIT 64`, i.ReceiptPrincipal, i.ReceiptID)
	if err != nil {
		return false, err
	}
	var keys []domain.OperationKey
	for rows.Next() {
		var k domain.OperationKey
		if err = rows.Scan(&k.PrincipalID, &k.OperationID); err != nil {
			rows.Close()
			return false, err
		}
		keys = append(keys, k)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, k := range keys {
		i.ReceiptPrincipal = k.PrincipalID
		i.ReceiptID = k.OperationID
		r, e := t.Receipt(k)
		if e != nil {
			return false, e
		}
		if r.Original.Target == "/api/v1/lifecycle-plans/"+i.PlanID+"/commit" {
			continue
		}
		if lifecycleReceipt(r.Original.Target) {
			if r.Response != nil {
				var page domain.LifecyclePage
				if json.Unmarshal(r.Response.Body, &page) == nil && page.Items != nil {
					affected := page.DocumentID == i.DocumentID
					clean := []domain.LifecycleItem{}
					for _, item := range page.Items {
						if page.DocumentID == i.DocumentID || item.SourceDocumentID != nil && *item.SourceDocumentID == i.DocumentID {
							affected = true
							continue
						}
						clean = append(clean, item)
					}
					if affected {
						page.Items = clean
						page.Impact = map[string]int{}
						page.NextCursor = nil
						r.Response.Body = jsonBytes(page)
						if e = t.exec(`UPDATE operation_receipts SET result=? WHERE principal_id=? AND operation_id=?`, jsonBytes(r.Response), k.PrincipalID, k.OperationID); e != nil {
							return false, e
						}
					}
				}
			}
			continue
		}
		// Decode response bodies to select exact ownership, including annotation IDs.
		var body map[string]any
		if r.Response != nil {
			_ = json.Unmarshal(r.Response.Body, &body)
		}
		owned := body["document_id"] == i.DocumentID
		parts := strings.Split(strings.Trim(strings.SplitN(r.Original.Target, "?", 2)[0], "/"), "/")
		if len(parts) >= 4 && parts[0] == "api" && parts[1] == "v1" {
			if parts[2] == "annotations" {
				note, e := t.Annotation(parts[3])
				if e == nil {
					owned = owned || note.DocumentID == i.DocumentID
				} else if !errors.Is(e, domain.ErrNotFound) {
					return false, e
				}
			}
			if parts[2] == "versions" {
				var n int
				if e := t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM purged_versions WHERE version_id=? AND document_id=?`, parts[3], i.DocumentID).Scan(&n); e != nil {
					return false, e
				}
				owned = owned || n > 0
			}
		}
		if len(parts) == 5 && parts[2] == "projects" && parts[4] == "source-captures" {
			var input struct {
				Version string `json:"version_id"`
			}
			if e = json.Unmarshal(r.Original.Body, &input); e != nil {
				return false, e
			}
			var n int
			if e = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM purged_versions WHERE version_id=? AND document_id=?`, input.Version, i.DocumentID).Scan(&n); e != nil {
				return false, e
			}
			owned = owned || n > 0
		}
		for _, name := range []string{"context_ref", "context_id"} {
			if ref, ok := body[name].(string); ok {
				c, e := t.Context(ref)
				if e == nil {
					var n int
					if e = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM purged_versions WHERE version_id=? AND document_id=?`, c.VersionID, i.DocumentID).Scan(&n); e != nil {
						return false, e
					}
					owned = owned || n > 0
				} else if !errors.Is(e, domain.ErrNotFound) {
					return false, e
				}
			}
		}

		if v, ok := body["version_id"].(string); ok {
			var matching int
			if e := t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM purged_versions WHERE version_id=? AND document_id=?`, v, i.DocumentID).Scan(&matching); e != nil {
				return false, e
			}
			owned = owned || matching > 0
		}
		// Original normalized import and annotation bodies carry exact document/version
		// identity even when a committed response was only asynchronous acceptance.
		var norm map[string]any
		_ = json.Unmarshal(r.Normalization, &norm)
		for _, name := range []string{"document_id", "DocumentID"} {
			owned = owned || norm[name] == i.DocumentID
		}
		if !owned {
			continue
		}
		sum := sha256.Sum256(r.Normalization)
		requestDigest, e := app.Fingerprint(r.Original, nil)
		if e != nil {
			return false, e
		}
		safe := map[string]any{"purged_document_id": i.DocumentID, "normalization_sha256": hex.EncodeToString(sum[:]), "request_sha256": requestDigest}
		r.Original.Body = nil
		var result any
		if r.Response != nil {
			r.Response.Body = jsonBytes(map[string]any{"document_id": i.DocumentID, "purged": true})
			result = jsonBytes(r.Response)
		}
		if e = t.exec(`UPDATE operation_receipts SET original_request=?,normalization_context=?,result=? WHERE principal_id=? AND operation_id=?`, jsonBytes(r.Original), jsonBytes(safe), result, k.PrincipalID, k.OperationID); e != nil {
			return false, e
		}
	}
	return len(keys) < 64, nil
}
func lifecycleReceipt(target string) bool {
	return len(target) >= 24 && (strings.Contains(target, "/lifecycle-plans") || strings.Contains(target, "/api/v1/trash"))
}

var _ app.WriteTx = (*writeTx)(nil)

func (t *writeTx) FinishPurge(id string, generation int64) error {
	i, err := t.PurgeIntent(id)
	if err != nil {
		return err
	}
	if err = t.purgeGuard(i); err != nil {
		return err
	}
	rows, err := t.PurgeCandidates(id, 1)
	if err != nil {
		return err
	}
	if !i.MetadataRemoved || i.Phase < len(purgeDeletes) || i.PendingSet != "" || len(i.RetainedSets) > 0 || len(rows) > 0 {
		return domain.ErrTransition
	}
	i.Reason = "complete"
	i.NextRetry = ""
	return t.FinishJob(id, generation, "", func(app.WriteTx) error {
		if err := t.savePurge(i); err != nil {
			return err
		}
		return t.SetJobResult(id, jsonBytes(map[string]any{"purge_plan_id": i.PlanID, "stage": "complete", "remaining": 0, "metadata_removed": true}))
	})
}

func purgeStage(i domain.PurgeIntent) string {
	phase := i.Phase
	if i.MetadataRemoved {
		return "gc"
	}
	if phase >= len(purgeDeletes) {
		return "versions"
	}
	if phase <= 5 {
		return "personal"
	}
	if phase <= 13 {
		return "source"
	}
	return "versions"
}

func (t *writeTx) detachManagedSnapshots(job, document string, limit int) (bool, error) {
	rows, e := t.tx.QueryContext(t.ctx, `SELECT r.read_id,r.content_hash FROM source_capture_reads r JOIN source_captures c ON c.capture_id=r.capture_id WHERE json_extract(c.requested_selection,'$.version_id') IN(SELECT version_id FROM purged_versions WHERE document_id=?) AND EXISTS(SELECT 1 FROM object_references own WHERE own.owner_kind='selected_snapshot' AND own.owner_id=r.read_id AND own.content_hash=r.content_hash) ORDER BY r.read_id LIMIT ?`, document, batch(limit))
	if e != nil {
		return false, e
	}
	type binding struct{ id, hash string }
	page := []binding{}
	for rows.Next() {
		var b binding
		if e = rows.Scan(&b.id, &b.hash); e != nil {
			rows.Close()
			return false, e
		}
		page = append(page, b)
	}
	e = errors.Join(rows.Err(), rows.Close())
	if e != nil {
		return false, e
	}
	for _, b := range page {
		if e = t.candidateSelect(job, `o.content_hash=?`, b.hash); e != nil {
			return false, e
		}
		if e = t.exec(`DELETE FROM object_references WHERE owner_kind='selected_snapshot' AND owner_id=? AND content_hash=?`, b.id, b.hash); e != nil {
			return false, e
		}
		if e = t.exec(`UPDATE source_capture_reads SET availability='unavailable',reason='version_purged' WHERE read_id=?`, b.id); e != nil {
			return false, e
		}
	}
	return len(page) > 0, nil
}
