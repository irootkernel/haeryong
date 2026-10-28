package sqlite

import (
	"context"
	"database/sql"
	"errors"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// RegisterProject inserts a newly registered project and emits the
// implemented project.updated event in the same transaction.
func (t *writeTx) RegisterProject(p domain.Project) error {
	if p.Revision == 0 {
		p.Revision = 1
	}
	if p.PermissionsRevision == 0 {
		p.PermissionsRevision = 1
	}
	if p.Lifecycle == "" {
		p.Lifecycle = "active"
	}
	if err := t.exec("INSERT INTO projects(project_id,name,lifecycle,revision,permissions_revision) VALUES(?,?,?,?,?)", p.ID, p.Name, p.Lifecycle, p.Revision, p.PermissionsRevision); err != nil {
		return err
	}
	return t.appendEvent("project", p.ID, "project.updated", p.Revision, nil)
}

func (t *writeTx) PutTargetSet(ts domain.TargetSet) error {
	return t.exec("INSERT INTO captured_target_sets VALUES(?,?,?,?,?)", ts.ID, ts.ProjectID, ts.RequestedSelection, ts.TargetsJSON, ts.CapturedAt)
}

func (t *writeTx) PutCapturedRead(r domain.CapturedRead) error {
	return t.exec("INSERT INTO captured_reads VALUES(?,?,?,?,?,?,?,?,?)", r.ID, r.TargetSetID, r.Basis, r.SourceJSON, r.LocationJSON, nullableString(r.BytesSHA), r.ConfigRevision, r.PermissionsRevision, r.CreatedAt)
}
func (t *writeTx) CapturedRead(id string) (domain.CapturedRead, error) {
	r := domain.CapturedRead{ID: id}
	e := t.tx.QueryRowContext(t.ctx, `SELECT target_set_id,basis,source_json,location_json,coalesce(bytes_sha256,''),config_revision,permissions_revision,created_at FROM captured_reads WHERE read_id=?`, id).Scan(&r.TargetSetID, &r.Basis, &r.SourceJSON, &r.LocationJSON, &r.BytesSHA, &r.ConfigRevision, &r.PermissionsRevision, &r.CreatedAt)
	return r, notFound(e)
}

func (t *writeTx) PutVerification(v domain.CitationVerification) error {
	return t.exec("INSERT INTO citation_verifications VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", v.ID, v.PrincipalID, v.VersionID, v.EvidenceID, nullableString(v.ReadID), nullableString(v.TargetSetID), v.SourceJSON, v.LocationJSON, nullableString(v.BytesSHA), nullableString(v.BytesOrigin), v.Outcome, nullableString(v.ExpectedBytesSHA), nullableBytes(v.ExpectedExcerpt), nullableString(v.ExpectedExcerptRef), v.ConfigRevision, v.PermissionsRevision, v.CheckedAt, nullableString(v.VerifiedAt), v.Reason, v.OperationID)
}

func (t *writeTx) PutCheckIntent(i domain.CheckIntent) error {
	return t.exec("INSERT INTO check_intents VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)", i.ID, i.OperationID, i.PrincipalID, i.Kind, i.CanonicalTarget, nullableBytes(i.ValidatedBody), nullableString(i.ExpectationUpload), i.ConfigRevision, i.PermissionsRevision, i.State, i.FailureReason, i.CreatedAt, nullableString(i.ResolvedAt))
}

func nullableBytes(b []byte) any {
	if b == nil {
		return nil
	}
	return b
}

func checkIntent(ctx context.Context, q rowReader, id string) (domain.CheckIntent, error) {
	var i domain.CheckIntent
	var body []byte
	var resolved, upload sql.NullString
	err := q.QueryRowContext(ctx, "SELECT intent_id,operation_id,principal_id,kind,canonical_target,validated_body,expectation_upload,config_revision,permissions_revision,state,failure_reason,created_at,resolved_at FROM check_intents WHERE intent_id=?", id).Scan(&i.ID, &i.OperationID, &i.PrincipalID, &i.Kind, &i.CanonicalTarget, &body, &upload, &i.ConfigRevision, &i.PermissionsRevision, &i.State, &i.FailureReason, &i.CreatedAt, &resolved)
	if resolved.Valid {
		i.ResolvedAt = resolved.String
	}
	if upload.Valid {
		i.ExpectationUpload = upload.String
	}
	i.ValidatedBody = body
	return i, notFound(err)
}

func (t *writeTx) CheckIntent(id string) (domain.CheckIntent, error) {
	return checkIntent(t.ctx, t.tx, id)
}

func (t *writeTx) OpenCheckIntents() ([]domain.CheckIntent, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT intent_id FROM check_intents WHERE state='open' ORDER BY created_at,intent_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.CheckIntent{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		i, err := checkIntent(t.ctx, t.tx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (t *writeTx) ResolveCheckIntent(id, state, reason string, resolvedAt string) error {
	if state != "completed" && state != "failed" {
		return domain.ErrTransition
	}
	result, err := t.tx.ExecContext(t.ctx, "UPDATE check_intents SET state=?,failure_reason=?,resolved_at=? WHERE intent_id=? AND state='open'", state, reason, resolvedAt, id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return domain.ErrTransition
	}
	return nil
}

// verificationColumns lists citation_verifications in insert/scan order.
const verificationColumns = "verification_id,principal_id,version_id,evidence_id,read_id,target_set_id,source_json,location_json,bytes_sha256,bytes_origin,outcome,expected_bytes_sha256,expected_excerpt,expected_excerpt_ref,config_revision,permissions_revision,checked_at,verified_at,reason,operation_id"

func scanVerification(scan func(...any) error) (domain.CitationVerification, error) {
	var v domain.CitationVerification
	var excerpt []byte
	var readID, targetSet, bytesSHA, origin, digest, ref, verified sql.NullString
	err := scan(&v.ID, &v.PrincipalID, &v.VersionID, &v.EvidenceID, &readID, &targetSet, &v.SourceJSON, &v.LocationJSON, &bytesSHA, &origin, &v.Outcome, &digest, &excerpt, &ref, &v.ConfigRevision, &v.PermissionsRevision, &v.CheckedAt, &verified, &v.Reason, &v.OperationID)
	if readID.Valid {
		v.ReadID = readID.String
	}
	if targetSet.Valid {
		v.TargetSetID = targetSet.String
	}
	if bytesSHA.Valid {
		v.BytesSHA = bytesSHA.String
	}
	if digest.Valid {
		v.ExpectedBytesSHA = digest.String
	}
	v.ExpectedExcerpt = excerpt
	if ref.Valid {
		v.ExpectedExcerptRef = ref.String
	}
	if verified.Valid {
		v.VerifiedAt = verified.String
	}
	return v, err
}

// VerificationByOperation is keyed by (principal_id, operation_id) so one
// UUID reused by two principals never crosses receipts (C5).
func (t *writeTx) VerificationByOperation(principalID, operationID string) (domain.CitationVerification, bool, error) {
	v, err := scanVerification(t.tx.QueryRowContext(t.ctx, "SELECT "+verificationColumns+" FROM citation_verifications WHERE principal_id=? AND operation_id=?", principalID, operationID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CitationVerification{}, false, nil
	}
	if err != nil {
		return domain.CitationVerification{}, false, err
	}
	return v, true, nil
}

func (s *Store) VerificationByOperation(ctx context.Context, principalID, operationID string) (domain.CitationVerification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := scanVerification(s.db.QueryRowContext(ctx, "SELECT "+verificationColumns+" FROM citation_verifications WHERE principal_id=? AND operation_id=?", principalID, operationID).Scan)
	return v, notFound(err)
}

func (t *writeTx) Verifications(versionID, evidenceID, afterCheckedAt, afterID string, limit int) ([]domain.CitationVerification, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT "+verificationColumns+" FROM citation_verifications WHERE version_id=? AND evidence_id=? AND (checked_at,verification_id)>(?,?) ORDER BY checked_at,verification_id LIMIT ?", versionID, evidenceID, afterCheckedAt, afterID, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.CitationVerification{}
	for rows.Next() {
		v, err := scanVerification(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (t *writeTx) LatestVerification(versionID, evidenceID string) (domain.CitationVerification, error) {
	v, err := scanVerification(t.tx.QueryRowContext(t.ctx, "SELECT "+verificationColumns+" FROM citation_verifications WHERE version_id=? AND evidence_id=? ORDER BY checked_at DESC,verification_id DESC LIMIT 1", versionID, evidenceID).Scan)
	return v, notFound(err)
}

func (t *writeTx) LatestEvidenceObservation(versionID, sourceID string) (domain.SourceObservation, error) {
	return latestObservationWhere(t.ctx, t.tx, "version_id=? AND source_id=?", versionID, sourceID)
}

func latestObservationWhere(ctx context.Context, q rowReader, where string, args ...any) (domain.SourceObservation, error) {
	var o domain.SourceObservation
	var targetSet, observed sql.NullString
	var changed, dependency []byte
	err := q.QueryRowContext(ctx, "SELECT observation_id,run_id,version_id,target_set_id,source_id,base_revision,observed_revision,availability,classification,changed_paths_json,dependency_scope_json,reason,config_revision,permissions_revision,observed_at,operation_id FROM source_observations WHERE "+where+" ORDER BY observed_at DESC,observation_id DESC LIMIT 1", args...).Scan(&o.ID, &o.RunID, &o.VersionID, &targetSet, &o.SourceID, &o.BaseRevision, &observed, &o.Availability, &o.Classification, &changed, &dependency, &o.Reason, &o.ConfigRevision, &o.PermissionsRevision, &o.ObservedAt, &o.OperationID)
	if targetSet.Valid {
		o.TargetSetID = targetSet.String
	}
	if observed.Valid {
		o.ObservedRevision = observed.String
	}
	o.ChangedPathsJSON, o.DependencyScopeJSON = changed, dependency
	return o, notFound(err)
}

func (s *Store) LatestObservation(ctx context.Context) (domain.SourceObservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return latestObservationWhere(ctx, s.db, "1=1")
}

func (t *writeTx) SourceCaptures(projectID string) ([]domain.TargetSet, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT target_set_id,project_id,requested_selection,targets_json,captured_at FROM captured_target_sets WHERE project_id=? ORDER BY captured_at,target_set_id", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.TargetSet{}
	for rows.Next() {
		var ts domain.TargetSet
		if err := rows.Scan(&ts.ID, &ts.ProjectID, &ts.RequestedSelection, &ts.TargetsJSON, &ts.CapturedAt); err != nil {
			return nil, err
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}

func (t *writeTx) ReleaseCheckIntentPins(intentID string) error {
	return t.ReleaseOwnerPins(domain.CheckIntentPinOwner, intentID)
}

func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// RegisterProjectRow registers a project through the store writer.
func (s *Store) RegisterProjectRow(ctx context.Context, p domain.Project) error {
	return s.Write(ctx, func(tx app.WriteTx) error { return tx.RegisterProject(p) })
}

// QueryProjectRow reads one registered project row for read-side views.
func (s *Store) QueryProjectRow(ctx context.Context, id string) (domain.QueryProject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var p domain.QueryProject
	p.ID = id
	err := s.db.QueryRowContext(ctx, "SELECT name,lifecycle,revision,permissions_revision,updated_at FROM projects WHERE project_id=?", id).Scan(&p.Name, &p.Lifecycle, &p.Revision, &p.PermissionsRevision, &p.UpdatedAt)
	return p, notFound(err)
}

func putCoverageSet(tx app.WriteTx, id, version, targetSet, runID, scopeJSON, unitKind, capturedAt string) error {
	writer, ok := tx.(*writeTx)
	if !ok {
		return errors.New("coverage writes require the sqlite transaction")
	}
	return writer.exec("INSERT INTO coverage_sets VALUES(?,?,?,?,?,?,?,'source_observation')", id, version, nullableString(targetSet), nullableString(runID), scopeJSON, unitKind, capturedAt)
}

func putRequiredUnit(tx app.WriteTx, set, kind, page, region, reason, targetSet string) error {
	writer, ok := tx.(*writeTx)
	if !ok {
		return errors.New("unit writes require the sqlite transaction")
	}
	return writer.exec("INSERT INTO required_units VALUES(?,?,?,?,?,?)", set, kind, page, region, reason, nullableString(targetSet))
}

// CheckIntentByOperation returns the latest intent of one principal's
// operation key (C6 replay proof).
func (t *writeTx) CheckIntentByOperation(principalID, operationID string) (domain.CheckIntent, error) {
	var id string
	if err := t.tx.QueryRowContext(t.ctx, "SELECT intent_id FROM check_intents WHERE principal_id=? AND operation_id=? ORDER BY created_at DESC,intent_id DESC LIMIT 1", principalID, operationID).Scan(&id); err != nil {
		return domain.CheckIntent{}, notFound(err)
	}
	return checkIntent(t.ctx, t.tx, id)
}

// SourceCheckObservations reports the counts of live check-intent pins and
// verification-owned object references for test observation.
func (s *Store) SourceCheckObservations(ctx context.Context) (pins, references int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM object_pins WHERE owner_kind='check_intent'").Scan(&pins); err != nil {
		return 0, 0, err
	}
	err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM object_references WHERE owner_kind='verification'").Scan(&references)
	return pins, references, err
}

// CleanupCheckIntentPins releases check-intent pins whose owning intent is
// no longer open: no check survives a restart (S15).
func (t *writeTx) CleanupCheckIntentPins() error {
	return t.exec("DELETE FROM object_pins WHERE owner_kind='check_intent' AND owner_id NOT IN (SELECT intent_id FROM check_intents WHERE state='open')")
}

// AppendEvent exposes the transactional outbox to application services.
func (t *writeTx) AppendEvent(resourceKind, id, kind string, revision int64, body []byte) error {
	return t.appendEvent(resourceKind, id, kind, revision, body)
}

// TestTrashDocument moves a document to trash for authorization-loss tests.
func (t *writeTx) TestTrashDocument(id string) error {
	return t.exec("UPDATE documents SET lifecycle='trash' WHERE document_id=?", id)
}

// TestReplaceEvidence overwrites one evidence row's metadata for specs.
func (t *writeTx) TestReplaceEvidence(version, evidence string, metadata []byte) error {
	return t.exec("UPDATE evidence SET metadata=? WHERE version_id=? AND evidence_id=?", metadata, version, evidence)
}

// TestReopenIntent marks a resolved intent open again to reproduce a crash
// between the result commit and the intent resolution; it fails when no
// intent matched, so a seeded state is never silently absent.
func (t *writeTx) TestReopenIntent(operationID, principalID string) error {
	return t.execOne("UPDATE check_intents SET state='open',resolved_at=NULL,failure_reason='' WHERE operation_id=? AND principal_id=?", operationID, principalID)
}

// TestReopenResolveFail marks an open intent resolved so a later
// settlement's ResolveCheckIntent returns ErrTransition (G2 seam).
func (t *writeTx) TestReopenResolveFail(operationID string) error {
	return t.execOne("UPDATE check_intents SET state='failed',resolved_at='2026-01-01T00:00:00.000Z' WHERE operation_id=? AND state='open'", operationID)
}

// execOne runs a test seam update that must change exactly one row.
func (t *writeTx) execOne(query string, args ...any) error {
	result, err := t.tx.ExecContext(t.ctx, query, args...)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return domain.ErrNotFound
	}
	return nil
}

// SourceCheckReads reports the count of captured read rows for test
// observation (G15: no read rows survive a crashed check).
func (s *Store) SourceCheckReads(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var reads int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM captured_reads").Scan(&reads); err != nil {
		return 0, err
	}
	return reads, nil
}
