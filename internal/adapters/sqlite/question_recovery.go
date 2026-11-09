package sqlite

import (
	"context"
	"haeryong.local/haeryong/internal/domain"
)

// Exclusive startup settles interrupted local capture preparation, never a
// provider effect. Accepted question objects have independent ownership.
func (s *Store) recoverQuestions(ctx context.Context) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	t := &writeTx{ctx: ctx, tx: tx}
	rows, e := tx.QueryContext(ctx, `SELECT q.job_id FROM questions q JOIN jobs j ON j.job_id=q.job_id WHERE j.state='paused' AND j.error='source_capture_pending' ORDER BY q.question_id`)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		if e = t.QuestionCaptureUnavailable(id, []domain.QuestionLimitation{{Code: "unable_to_verify", Message: "Exact source capture was interrupted before admission. No provider execution was admitted."}}); e != nil {
			return e
		}
	}
	mRows, e := tx.QueryContext(ctx, `SELECT m.job_id FROM maintenance_intents m JOIN jobs j ON j.job_id=m.job_id WHERE j.state='paused' AND j.error='source_capture_pending' ORDER BY m.job_id`)
	if e != nil {
		return e
	}
	mIDs := []string{}
	for mRows.Next() {
		var id string
		if e = mRows.Scan(&id); e != nil {
			mRows.Close()
			return e
		}
		mIDs = append(mIDs, id)
	}
	e = mRows.Err()
	mRows.Close()
	if e != nil {
		return e
	}
	for _, id := range mIDs {
		if e = t.PauseMaintenanceCapture(id, "source_capture_unavailable", []domain.MaintenanceLimitation{{Code: "unable_to_verify", Message: "Exact source capture was interrupted before admission. No provider execution was admitted."}}); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func questionTransportObjects(q *domain.QuestionTransport) []domain.ProviderText {
	if q == nil {
		return nil
	}
	objects := []domain.ProviderText{q.Text, {Hash: q.Explanation.ObjectHash, Size: q.Explanation.Size}}
	if q.Context != nil {
		objects = append(objects, domain.ProviderText{Hash: q.Context.Hash, Size: q.Context.Size})
	}
	return objects
}

func transportOwnedObjects(t *domain.ProviderTransport) []domain.ProviderText {
	if t == nil {
		return nil
	}
	return append(generationTransportObjects(t.Generation), questionTransportObjects(t.Question)...)
}
