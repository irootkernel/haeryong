package sqlite

import (
	"encoding/json"
	"errors"
	"reflect"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func (t *writeTx) Question(id string) (domain.Question, error) {
	var q domain.Question
	var owner, project, document, version, job, created string
	var raw []byte
	e := t.tx.QueryRowContext(t.ctx, `SELECT owner_id,project_id,document_id,version_id,job_id,created_at,value FROM questions WHERE question_id=?`, id).Scan(&owner, &project, &document, &version, &job, &created, &raw)
	if e != nil {
		return q, notFound(e)
	}
	if e = json.Unmarshal(raw, &q); e != nil {
		return q, e
	}
	if q.ID != id || q.OwnerID != owner || q.ProjectID != project || q.DocumentID != document || q.VersionID != version || q.JobID != job || q.CreatedAt != created {
		return q, domain.ErrContentIntegrity
	}
	return q, nil
}

func (t *writeTx) QuestionByJob(id string) (domain.Question, error) {
	var question string
	if e := t.tx.QueryRowContext(t.ctx, `SELECT question_id FROM questions WHERE job_id=?`, id).Scan(&question); e != nil {
		return domain.Question{}, notFound(e)
	}
	return t.Question(question)
}

func (t *writeTx) CreateQuestion(q domain.Question) error {
	j, e := t.Job(q.JobID)
	if e != nil {
		return e
	}
	var scope domain.ProviderScope
	if json.Unmarshal(j.Scope, &scope) != nil || scope.Invocation == nil || scope.Invocation.Question == nil || scope.Invocation.Generation != nil || scope.Invocation.Question.QuestionID != q.ID || scope.PrincipalID != q.OwnerID || j.Kind != "question" || j.State != "queued" || j.CancelRequested || q.PreparedAnswer != nil || len(q.RequestHash) != 64 || len(q.Text.Hash) != 64 {
		return domain.ErrInvalidTarget
	}
	v, e := t.PublishedVersion(q.VersionID)
	if e != nil {
		return e
	}
	d, e := t.Document(v.DocumentID)
	if e != nil {
		return e
	}
	if d.ID != q.DocumentID || d.ProjectID != q.ProjectID {
		return domain.ErrInvalidTarget
	}
	if e = t.exec(`INSERT INTO questions VALUES(?,?,?,?,?,?,?,?)`, q.ID, q.OwnerID, q.ProjectID, q.DocumentID, q.VersionID, q.JobID, q.CreatedAt, jsonBytes(q)); e != nil {
		return e
	}
	objects, e := app.ProviderInputObjects(t, scope)
	if e != nil {
		return e
	}
	for _, object := range objects {
		if e = t.Adopt(domain.Reference{Kind: "question", ID: q.ID, Hash: object.Hash}, ""); e != nil {
			return e
		}
		if e = t.AcquirePin(domain.Pin{ID: "question:" + q.ID + ":" + object.Hash, Kind: "question", OwnerID: q.ID, Hash: object.Hash}); e != nil {
			return e
		}
	}
	return t.AppendEvent("question", q.ID, "question.created", 1, jsonBytes(map[string]string{"question_id": q.ID, "version_id": q.VersionID}))
}

func (t *writeTx) Questions(owner, version, created, id string, limit int) ([]domain.Question, error) {
	rows, e := t.tx.QueryContext(t.ctx, `SELECT question_id FROM questions WHERE owner_id=? AND version_id=? AND (created_at>? OR(created_at=? AND question_id>?)) ORDER BY created_at,question_id LIMIT ?`, owner, version, created, created, id, limit)
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []domain.Question{}
	for _, id := range ids {
		q, e := t.Question(id)
		if e != nil {
			return nil, e
		}
		out = append(out, q)
	}
	return out, nil
}

// AdmitQuestion is the atomic completed-capture handoff to the existing queue.
// It cannot alter the accepted question's scope or create a provider attempt.
func (t *writeTx) AdmitQuestion(next domain.Job) error {
	old, e := t.Job(next.ID)
	if e != nil {
		return e
	}
	q, e := t.QuestionByJob(next.ID)
	if e != nil {
		return e
	}
	if old.Kind != "question" || old.State != "paused" || old.Error != "source_capture_pending" || old.CancelRequested || q.AdmissionReady || old.AttemptGeneration != next.AttemptGeneration {
		return domain.ErrStaleWorker
	}
	before, after := old, next
	before.Scope, after.Scope = nil, nil
	before.Permissions, after.Permissions = nil, nil
	if !reflect.DeepEqual(before, after) {
		return domain.ErrInvalidTarget
	}
	attempts, e := t.ProviderAttempts(next.ID)
	if e != nil {
		return e
	}
	if len(attempts) != 0 {
		return domain.ErrReconciliation
	}
	var scope domain.ProviderScope
	if json.Unmarshal(next.Scope, &scope) != nil || scope.Invocation == nil || scope.Invocation.Question == nil || scope.Invocation.Question.QuestionID != q.ID || len(scope.RequestSHA) != 64 {
		return domain.ErrInvalidTarget
	}
	if e = app.AuthorizeQuestion(t, scope); e != nil {
		return e
	}
	q.AdmissionReady = true
	if e = t.exec(`UPDATE questions SET value=? WHERE question_id=?`, jsonBytes(q), q.ID); e != nil {
		return e
	}
	if e = t.exec(`UPDATE jobs SET scope=?,permissions_revisions=?,state='queued',error='',revision=revision+1 WHERE job_id=?`, next.Scope, jsonBytes(next.Permissions), next.ID); e != nil {
		return e
	}
	return t.jobUpdated(next.ID)
}

func (t *writeTx) QuestionCaptureUnavailable(id string, limitations []domain.QuestionLimitation) error {
	j, e := t.Job(id)
	if e != nil {
		return e
	}
	q, e := t.QuestionByJob(id)
	if e != nil {
		return e
	}
	if j.Kind != "question" || j.State != "paused" || j.Error != "source_capture_pending" || j.CancelRequested || q.AdmissionReady {
		return domain.ErrStaleWorker
	}
	q.Limitations = limitations
	if e = t.exec(`UPDATE questions SET value=? WHERE question_id=?`, jsonBytes(q), q.ID); e != nil {
		return e
	}
	return t.PauseProviderJob(id, "source_capture_unavailable")
}

func (t *writeTx) PrepareQuestionAnswer(id string, answer domain.QuestionAnswer) error {
	q, e := t.Question(id)
	if e != nil {
		return e
	}
	if answer.QuestionID != id || (answer.State != "final" && answer.State != "partial") {
		return domain.ErrInvalidTarget
	}
	a, e := t.ProviderAttempt(answer.AttemptID)
	if e != nil {
		return e
	}
	if a.JobID != q.JobID || a.State != "result_saved" {
		return domain.ErrStaleWorker
	}
	if q.PreparedAnswer != nil {
		if reflect.DeepEqual(*q.PreparedAnswer, answer) {
			return nil
		}
		return domain.ErrConflict
	}
	object, e := t.Object(answer.Body.Hash)
	if e != nil {
		return e
	}
	if object.State != "ready" || object.Size != answer.Body.Size {
		return domain.ErrContentIntegrity
	}
	if e = t.AcquirePin(domain.Pin{ID: "question-answer-prepare:" + q.ID, Kind: "question_answer_prepare", OwnerID: q.ID, Hash: object.Hash}); e != nil {
		return e
	}
	q.PreparedAnswer = &answer
	return t.exec(`UPDATE questions SET value=? WHERE question_id=?`, jsonBytes(q), q.ID)
}

func (t *writeTx) QuestionAnswer(id string) (domain.QuestionAnswer, error) {
	var a domain.QuestionAnswer
	var question, attempt, state, created string
	var raw []byte
	e := t.tx.QueryRowContext(t.ctx, `SELECT question_id,attempt_id,state,created_at,value FROM answers WHERE answer_id=?`, id).Scan(&question, &attempt, &state, &created, &raw)
	if e != nil {
		return a, notFound(e)
	}
	if e = json.Unmarshal(raw, &a); e != nil {
		return a, e
	}
	if a.ID != id || a.QuestionID != question || a.AttemptID != attempt || a.State != state || a.CreatedAt != created {
		return a, domain.ErrContentIntegrity
	}
	return a, nil
}

func (t *writeTx) QuestionAnswerForAttempt(attempt, state string) (domain.QuestionAnswer, error) {
	var id string
	if e := t.tx.QueryRowContext(t.ctx, `SELECT answer_id FROM answers WHERE attempt_id=? AND state=?`, attempt, state).Scan(&id); e != nil {
		return domain.QuestionAnswer{}, notFound(e)
	}
	return t.QuestionAnswer(id)
}

func (t *writeTx) PutQuestionAnswer(answer domain.QuestionAnswer) error {
	old, e := t.QuestionAnswerForAttempt(answer.AttemptID, answer.State)
	if e == nil {
		if reflect.DeepEqual(old, answer) {
			return nil
		}
		return domain.ErrConflict
	}
	if !errors.Is(e, domain.ErrNotFound) {
		return e
	}
	q, e := t.Question(answer.QuestionID)
	if e != nil {
		return e
	}
	a, e := t.ProviderAttempt(answer.AttemptID)
	if e != nil {
		return e
	}
	if a.JobID != q.JobID || a.State != "result_saved" || answer.ID == "" || answer.CreatedAt == "" || (answer.State != "final" && answer.State != "partial" && answer.State != "error") {
		return domain.ErrInvalidTarget
	}
	object, e := t.Object(answer.Body.Hash)
	if e != nil {
		return e
	}
	if object.State != "ready" || object.Size != answer.Body.Size {
		return domain.ErrContentIntegrity
	}
	if e = t.exec(`INSERT INTO answers VALUES(?,?,?,?,?,?)`, answer.ID, answer.QuestionID, answer.AttemptID, answer.State, answer.CreatedAt, jsonBytes(answer)); e != nil {
		return e
	}
	if e = t.Adopt(domain.Reference{Kind: "answer", ID: answer.ID, Hash: object.Hash}, ""); e != nil {
		return e
	}
	if e = t.AcquirePin(domain.Pin{ID: "answer:" + answer.ID, Kind: "answer", OwnerID: answer.ID, Hash: object.Hash}); e != nil {
		return e
	}
	return t.AppendEvent("question", q.ID, "answer.saved", 1, jsonBytes(map[string]string{"question_id": q.ID, "answer_id": answer.ID, "state": answer.State}))
}

func (t *writeTx) QuestionAnswers(question, created, id string, limit int) ([]domain.QuestionAnswer, error) {
	rows, e := t.tx.QueryContext(t.ctx, `SELECT answer_id FROM answers WHERE question_id=? AND(created_at>? OR(created_at=? AND answer_id>?)) ORDER BY created_at,answer_id LIMIT ?`, question, created, created, id, limit)
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []domain.QuestionAnswer{}
	for _, id := range ids {
		a, e := t.QuestionAnswer(id)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, nil
}

func (t *writeTx) LatestCompletedAnswer(question string) (*domain.QuestionAnswer, error) {
	var id string
	e := t.tx.QueryRowContext(t.ctx, `SELECT answer_id FROM answers WHERE question_id=? AND state='final' ORDER BY created_at DESC,answer_id DESC LIMIT 1`, question).Scan(&id)
	if errors.Is(notFound(e), domain.ErrNotFound) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	a, e := t.QuestionAnswer(id)
	return &a, e
}

func (t *writeTx) Verification(id string) (domain.CitationVerification, error) {
	var principal, operation string
	if e := t.tx.QueryRowContext(t.ctx, `SELECT principal_id,operation_id FROM citation_verifications WHERE verification_id=?`, id).Scan(&principal, &operation); e != nil {
		return domain.CitationVerification{}, notFound(e)
	}
	v, found, e := t.VerificationByOperation(principal, operation)
	if e == nil && !found {
		e = domain.ErrNotFound
	}
	return v, e
}
