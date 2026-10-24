package sqlite

import (
	"errors"
	"haeryong.local/haeryong/internal/domain"
)

func (t *writeTx) Review(owner, version string) (domain.Review, error) {
	r := domain.Review{OwnerID: owner, VersionID: version}
	err := t.tx.QueryRowContext(t.ctx, `SELECT state,reviewed_at,revision,event_sequence FROM review_states WHERE owner_id=? AND version_id=?`, owner, version).Scan(&r.State, &r.ReviewedAt, &r.Revision, &r.EventSequence)
	return r, notFound(err)
}

// History, current state and the audit-bearing outbox share the caller's receipt transaction.
func (t *writeTx) SaveReview(r domain.Review, e domain.ReviewEvent) error {
	result, err := t.tx.ExecContext(t.ctx, `INSERT INTO review_events(event_id,owner_id,version_id,action,old_state,new_state,actor_session,operation_id,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, e.ID, r.OwnerID, r.VersionID, e.Action, e.OldState, e.NewState, e.ActorSession, e.OperationID, e.CreatedAt)
	if err != nil {
		return err
	}
	sequence, err := result.LastInsertId()
	if err != nil {
		return err
	}
	if err = t.exec(`INSERT INTO review_states(owner_id,version_id,state,reviewed_at,revision,event_sequence) VALUES(?,?,?,?,?,?) ON CONFLICT(owner_id,version_id) DO UPDATE SET state=excluded.state,reviewed_at=excluded.reviewed_at,revision=excluded.revision,event_sequence=excluded.event_sequence`, r.OwnerID, r.VersionID, r.State, r.ReviewedAt, r.Revision, sequence); err != nil {
		return err
	}
	return t.appendEvent("review", r.VersionID, "resource.invalidated", r.Revision, jsonBytes(map[string]any{"owner_id": r.OwnerID, "version_id": r.VersionID, "actor_session": e.ActorSession, "operation_id": e.OperationID, "action": e.Action, "old_state": e.OldState, "new_state": e.NewState}))
}

func (t *writeTx) ReviewHistory(owner, version string, after int64, limit int) ([]domain.ReviewEvent, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT sequence,event_id,version_id,action,old_state,new_state,actor_session,operation_id,created_at FROM review_events WHERE owner_id=? AND version_id=? AND sequence>? ORDER BY sequence LIMIT ?`, owner, version, after, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ReviewEvent{}
	for rows.Next() {
		var e domain.ReviewEvent
		if err := rows.Scan(&e.Sequence, &e.ID, &e.VersionID, &e.Action, &e.OldState, &e.NewState, &e.ActorSession, &e.OperationID, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (t *writeTx) ReviewSummary(owner, document string) (domain.ReviewSummary, error) {
	out := domain.ReviewSummary{}
	d, err := t.QueryDocument(document)
	if err != nil {
		return out, err
	}
	updated := false
	out.UpdatedSinceReview = &updated
	if d.LatestVersionID != "" {
		state := "unreviewed"
		out.State = &state
		r, err := t.Review(owner, d.LatestVersionID)
		if err == nil {
			out.State = &r.State
			out.ReviewedAt = r.ReviewedAt
		} else if !errors.Is(err, domain.ErrNotFound) {
			return out, err
		}
	}
	var id string
	var sequence int64
	err = t.tx.QueryRowContext(t.ctx, `SELECT r.version_id,v.sequence FROM review_states r JOIN document_versions v ON v.version_id=r.version_id WHERE r.owner_id=? AND v.document_id=? AND r.state='reviewed' ORDER BY r.event_sequence DESC LIMIT 1`, owner, document).Scan(&id, &sequence)
	if err == nil {
		out.LastReviewedVersionID = &id
		if d.LatestVersionID != "" {
			var latest int64
			if err := t.tx.QueryRowContext(t.ctx, `SELECT sequence FROM document_versions WHERE version_id=?`, d.LatestVersionID).Scan(&latest); err != nil {
				return out, err
			}
			updated = sequence < latest
		}
	} else if !errors.Is(notFound(err), domain.ErrNotFound) {
		return out, err
	}
	var continuing string
	err = t.tx.QueryRowContext(t.ctx, `SELECT r.version_id FROM review_states r JOIN document_versions v ON v.version_id=r.version_id WHERE r.owner_id=? AND v.document_id=? AND r.state='reviewing' ORDER BY r.event_sequence DESC LIMIT 1`, owner, document).Scan(&continuing)
	if err == nil {
		out.ContinueVersionID = &continuing
	} else if !errors.Is(notFound(err), domain.ErrNotFound) {
		return out, err
	}
	return out, nil
}

func (t *writeTx) QueryReviewDocuments(owner, project, lifecycle, review, afterUpdated, afterID string, limit int) ([]domain.QueryDocument, error) {
	// Personal predicates precede pagination. A newer version never hides an older in-progress review.
	rows, err := t.tx.QueryContext(t.ctx, `SELECT d.document_id,d.project_id,d.title,d.lifecycle,coalesce(d.latest_version_id,''),d.revision,d.updated_at FROM documents d JOIN projects p ON p.project_id=d.project_id WHERE d.lifecycle NOT IN ('trash','purged') AND (?='' OR d.project_id=?) AND (?='' OR d.lifecycle=?) AND (d.updated_at,d.document_id)>(?,?) AND ((?='reviewing' AND EXISTS(SELECT 1 FROM review_states r JOIN document_versions v ON v.version_id=r.version_id WHERE r.owner_id=? AND v.document_id=d.document_id AND r.state='reviewing')) OR (?='updated_since_review' AND (SELECT v.sequence FROM review_states r JOIN document_versions v ON v.version_id=r.version_id WHERE r.owner_id=? AND v.document_id=d.document_id AND r.state='reviewed' ORDER BY r.event_sequence DESC LIMIT 1)<(SELECT sequence FROM document_versions WHERE version_id=d.latest_version_id))) ORDER BY d.updated_at COLLATE BINARY,d.document_id COLLATE BINARY LIMIT ?`, project, project, lifecycle, lifecycle, afterUpdated, afterID, review, owner, review, owner, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.QueryDocument{}
	for rows.Next() {
		var d domain.QueryDocument
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.Title, &d.Lifecycle, &d.LatestVersionID, &d.Revision, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
