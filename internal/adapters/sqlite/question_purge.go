package sqlite

// Detach bounded answer/question ownership before the existing version FK
// parents. Existing durable purge phase numbers retain their original meaning.
func (t *writeTx) detachQuestions(job, document string, limit int) (bool, error) {
	rows, e := t.tx.QueryContext(t.ctx, `SELECT a.answer_id FROM answers a JOIN questions q USING(question_id) WHERE q.document_id=? ORDER BY a.answer_id LIMIT ?`, document, batch(limit))
	if e != nil {
		return false, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return false, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return false, e
	}
	for _, id := range ids {
		if e = t.detachQuestionOwner(job, "answer", id); e != nil {
			return false, e
		}
		if e = t.exec(`DELETE FROM answers WHERE answer_id=?`, id); e != nil {
			return false, e
		}
	}
	if len(ids) > 0 {
		return true, nil
	}
	rows, e = t.tx.QueryContext(t.ctx, `SELECT question_id FROM questions WHERE document_id=? ORDER BY question_id LIMIT ?`, document, batch(limit))
	if e != nil {
		return false, e
	}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return false, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return false, e
	}
	for _, id := range ids {
		if e = t.detachQuestionOwner(job, "question", id); e != nil {
			return false, e
		}
		if e = t.detachQuestionOwner(job, "question_answer_prepare", id); e != nil {
			return false, e
		}
		if e = t.exec(`DELETE FROM questions WHERE question_id=?`, id); e != nil {
			return false, e
		}
	}
	return len(ids) > 0, nil
}
func (t *writeTx) detachQuestionOwner(job, kind, id string) error {
	if e := t.candidateSelect(job, `o.content_hash IN(SELECT content_hash FROM object_references WHERE owner_kind=? AND owner_id=? UNION SELECT content_hash FROM object_pins WHERE owner_kind=? AND owner_id=?)`, kind, id, kind, id); e != nil {
		return e
	}
	if e := t.exec(`DELETE FROM object_references WHERE owner_kind=? AND owner_id=?`, kind, id); e != nil {
		return e
	}
	return t.ReleaseOwnerPins(kind, id)
}
