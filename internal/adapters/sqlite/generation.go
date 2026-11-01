package sqlite

import (
	"encoding/json"
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"reflect"
)

func (t *writeTx) GenerationIntent(id string) (domain.GenerationIntent, error) {
	var v domain.GenerationIntent
	var raw []byte
	var set string
	if e := t.tx.QueryRowContext(t.ctx, `SELECT coalesce(content_set_id,''),value FROM generation_intents WHERE job_id=?`, id).Scan(&set, &raw); e != nil {
		return v, notFound(e)
	}
	if e := json.Unmarshal(raw, &v); e != nil {
		return v, e
	}
	if v.ContentSetID != set || v.JobID != id || v.ProjectID == "" || v.VersionID == "" || v.AllocatedDocumentID == "" || v.DraftID == "" || (v.Intent != "publish" && v.Intent != "draft") {
		return v, domain.ErrInvalidTarget
	}
	return v, nil
}

func (t *writeTx) CreateGenerationIntent(v domain.GenerationIntent) error {
	j, e := t.Job(v.JobID)
	if e != nil {
		return e
	}
	if j.Kind != "generation" || j.State != "queued" || j.CancelRequested || v.ContentSetID != "" || v.PreparationAttemptID != "" || v.ProjectID == "" || v.DraftID == "" || v.VersionID == "" || v.AllocatedDocumentID == "" || len(v.RequestHash) != 64 || (v.Intent != "publish" && v.Intent != "draft") {
		return domain.ErrInvalidTarget
	}
	d, e := t.Draft(v.DraftID)
	if e != nil {
		return e
	}
	if d.JobID != v.JobID || d.ProjectID != v.ProjectID || d.State != "preparing" || d.ProposedVersionID != v.VersionID || d.AllocatedDocumentID != v.AllocatedDocumentID || !reflect.DeepEqual(d.DocumentID, v.DocumentID) || !reflect.DeepEqual(d.BaseVersionID, v.BaseVersionID) {
		return domain.ErrInvalidTarget
	}
	return t.exec(`INSERT INTO generation_intents(job_id,content_set_id,value) VALUES(?,NULL,?)`, v.JobID, jsonBytes(v))
}

// AdmitGeneration is the one local handoff from complete captured inputs to
// the existing dispatcher. No provider attempt exists before this transition.
func (t *writeTx) AdmitGeneration(next domain.Job) error {
	old, e := t.Job(next.ID)
	if e != nil {
		return e
	}
	g, e := t.GenerationIntent(next.ID)
	if e != nil {
		return e
	}
	if old.Kind != "generation" || old.State != "paused" || old.Error != "source_capture_pending" || old.CancelRequested || old.AttemptGeneration != next.AttemptGeneration || g.AdmissionReady {
		return domain.ErrStaleWorker
	}
	attempts, e := t.ProviderAttempts(next.ID)
	if e != nil {
		return e
	}
	if len(attempts) != 0 {
		return domain.ErrReconciliation
	}
	before, after := old, next
	before.Scope, after.Scope = nil, nil
	before.Permissions, after.Permissions = nil, nil
	if !reflect.DeepEqual(before, after) {
		return domain.ErrInvalidTarget
	}
	var scope domain.ProviderScope
	if json.Unmarshal(next.Scope, &scope) != nil || scope.Invocation == nil || scope.Invocation.Generation == nil || len(scope.RequestSHA) != 64 || scope.ProviderID != "codex" {
		return domain.ErrInvalidTarget
	}
	if e = app.AuthorizeGeneration(t, scope); e != nil {
		return e
	}
	if e = app.AuthorizeTarget(t, scope.Target); e != nil {
		return e
	}
	g.AdmissionReady = true
	if e = t.exec(`UPDATE generation_intents SET value=? WHERE job_id=?`, jsonBytes(g), g.JobID); e != nil {
		return e
	}
	if e = t.exec(`UPDATE jobs SET scope=?,permissions_revisions=?,state='queued',error='',revision=revision+1 WHERE job_id=?`, next.Scope, jsonBytes(next.Permissions), next.ID); e != nil {
		return e
	}
	return t.jobUpdated(next.ID)
}

func (t *writeTx) PrepareGenerationIntent(v domain.GenerationIntent) error {
	old, e := t.GenerationIntent(v.JobID)
	if e != nil {
		return e
	}
	before, after := old, v
	before.PreparedVersion, after.PreparedVersion = nil, nil
	before.OutputUploadID, after.OutputUploadID = "", ""
	before.ContentSetID, after.ContentSetID = "", ""
	before.MetadataHash, after.MetadataHash = "", ""
	before.PayloadSHA, after.PayloadSHA = "", ""
	before.PreparationAttemptID, after.PreparationAttemptID = "", ""
	if !reflect.DeepEqual(before, after) || v.PreparationAttemptID == "" || v.ContentSetID == "" || len(v.PayloadSHA) != 64 {
		return domain.ErrInvalidTarget
	}
	if old.ContentSetID != "" {
		if reflect.DeepEqual(old, v) {
			return nil
		}
		return domain.ErrConflict
	}
	a, e := t.ProviderAttempt(v.PreparationAttemptID)
	if e != nil {
		return e
	}
	if a.JobID != v.JobID || a.State != "result_saved" {
		return domain.ErrTransition
	}
	j, e := t.Job(v.JobID)
	if e != nil {
		return e
	}
	if j.State == "queued" {
		e = app.GuardProviderContinuation(t, a)
	} else {
		e = app.GuardProvider(t, a)
	}
	if e != nil {
		return e
	}
	hash, sealed, e := t.ContentSet(v.ContentSetID)
	if e != nil {
		return e
	}
	if !sealed || hash != v.PayloadSHA {
		return domain.ErrInvalidTarget
	}
	prepared := v.PreparedVersion
	base := ""
	if v.BaseVersionID != nil {
		base = *v.BaseVersionID
	}
	if prepared == nil || prepared.ID != v.VersionID || prepared.DocumentID != v.AllocatedDocumentID || prepared.BaseVersionID != base || prepared.ContentSetID != v.ContentSetID || prepared.PayloadSHA != v.PayloadSHA || prepared.MetadataHash != v.MetadataHash || len(prepared.ManifestSHA) != 64 {
		return domain.ErrInvalidTarget
	}
	manifest, e := t.Object(prepared.ManifestSHA)
	if e != nil {
		return e
	}
	if manifest.State != "ready" {
		return domain.ErrDeleting
	}
	return t.exec(`UPDATE generation_intents SET content_set_id=?,value=? WHERE job_id=?`, v.ContentSetID, jsonBytes(v), v.JobID)
}

// Inventory rows are owned alongside their objects. Check this before any
// destructive batch; a later FK rejection cannot restore earlier deleted rows.
func (t *writeTx) generationSetOwners(set string) (int, error) {
	var n int
	e := t.tx.QueryRowContext(t.ctx, `SELECT
 (SELECT count(*) FROM drafts WHERE content_set_id=?)+
 (SELECT count(*) FROM draft_history WHERE content_set_id=?)+
 (SELECT count(*) FROM generation_intents WHERE content_set_id=?)`, set, set, set).Scan(&n)
	return n, e
}

func (t *writeTx) Draft(id string) (domain.Draft, error) {
	var v domain.Draft
	var raw []byte
	var project, document, job, state, set string
	var revision int64
	if e := t.tx.QueryRowContext(t.ctx, `SELECT project_id,coalesce(document_id,''),job_id,state,revision,coalesce(content_set_id,''),value FROM drafts WHERE draft_id=?`, id).Scan(&project, &document, &job, &state, &revision, &set, &raw); e != nil {
		return v, notFound(e)
	}
	if e := json.Unmarshal(raw, &v); e != nil {
		return v, e
	}
	actualDocument := ""
	if v.DocumentID != nil {
		actualDocument = *v.DocumentID
	}
	if v.ID != id || v.ProjectID != project || actualDocument != document || v.JobID != job || v.State != state || v.Revision != revision || v.ContentSetID != set {
		return v, domain.ErrInvalidTarget
	}
	return v, nil
}

func (t *writeTx) CreateDraft(v domain.Draft) error {
	if v.ID == "" || v.ProjectID == "" || v.JobID == "" || v.AllocatedDocumentID == "" || v.ProposedVersionID == "" || v.State != "preparing" || v.ContentSetID != "" || (v.Intent != "draft" && v.Intent != "publish") {
		return domain.ErrInvalidTarget
	}
	j, e := t.Job(v.JobID)
	if e != nil {
		return e
	}
	if j.Kind != "generation" || j.State != "queued" || j.CancelRequested {
		return domain.ErrTransition
	}
	if _, e := t.Scope(v.ProjectID); e != nil {
		return e
	}
	if v.DocumentID != nil {
		d, e := t.Document(*v.DocumentID)
		if e != nil {
			return e
		}
		if d.ProjectID != v.ProjectID || d.ID != v.AllocatedDocumentID || d.Lifecycle == "trash" || d.Lifecycle == "purged" || v.BaseVersionID == nil {
			return domain.ErrInvalidTarget
		}
	} else if v.BaseVersionID != nil {
		return domain.ErrInvalidTarget
	}
	v.Revision = 1
	if e := t.exec(`INSERT INTO drafts(draft_id,project_id,document_id,job_id,state,revision,content_set_id,value) VALUES(?,?,?,?,?,1,NULL,?)`, v.ID, v.ProjectID, v.DocumentID, v.JobID, v.State, jsonBytes(v)); e != nil {
		return e
	}
	return t.saveDraftHistory(v)
}

func (t *writeTx) saveDraftHistory(v domain.Draft) error {
	if e := t.exec(`INSERT INTO draft_history(draft_id,revision,content_set_id,value) VALUES(?,?,?,?)`, v.ID, v.Revision, nullableString(v.ContentSetID), jsonBytes(v)); e != nil {
		return e
	}
	if v.ContentSetID != "" {
		if e := t.exec(`INSERT OR IGNORE INTO object_references(owner_kind,owner_id,content_hash) SELECT 'draft',?,content_hash FROM version_entries WHERE content_set_id=? AND kind='file'`, v.ID, v.ContentSetID); e != nil {
			return e
		}
	}
	for _, hash := range []string{v.ManifestSHA, v.MetadataHash} {
		if hash != "" {
			if e := t.Adopt(domain.Reference{Kind: "draft", ID: v.ID, Hash: hash}, ""); e != nil {
				return e
			}
		}
	}
	return t.appendEvent("draft", v.ID, "draft.changed", v.Revision, nil)
}

func (t *writeTx) UpdateDraft(v domain.Draft, expected int64) error {
	old, e := t.Draft(v.ID)
	if e != nil {
		return e
	}
	if expected != old.Revision {
		return domain.ErrPrecondition
	}
	if old.ProjectID != v.ProjectID || old.JobID != v.JobID || old.AllocatedDocumentID != v.AllocatedDocumentID || old.Intent != v.Intent || old.CreatedAt != v.CreatedAt || !reflect.DeepEqual(old.DocumentID, v.DocumentID) || !reflect.DeepEqual(old.BaseVersionID, v.BaseVersionID) {
		return domain.ErrInvalidTarget
	}
	allowed := old.State == "preparing" && (v.State == "ready" || v.State == "rejected" || v.State == "failed") || old.State == "ready" && (v.State == "ready" || v.State == "rejected" || v.State == "published")
	if !allowed {
		return domain.ErrTransition
	}
	if old.State == "preparing" && old.ProposedVersionID != v.ProposedVersionID {
		return domain.ErrInvalidTarget
	}
	if v.State != "ready" && (old.ProposedVersionID != v.ProposedVersionID || old.ContentSetID != v.ContentSetID || old.PayloadSHA != v.PayloadSHA || old.ManifestSHA != v.ManifestSHA || old.MetadataHash != v.MetadataHash || !reflect.DeepEqual(old.Metadata, v.Metadata)) {
		return domain.ErrInvalidTarget
	}
	if v.State == "ready" || v.State == "published" {
		payload, sealed, e := t.ContentSet(v.ContentSetID)
		if e != nil {
			return e
		}
		if !sealed || payload != v.PayloadSHA || len(v.ManifestSHA) != 64 || v.ProposedVersionID == "" {
			return domain.ErrInvalidTarget
		}
		object, e := t.Object(v.ManifestSHA)
		if e != nil {
			return e
		}
		if object.State != "ready" {
			return domain.ErrDeleting
		}
	}
	v.Revision = expected + 1
	if e := t.exec(`UPDATE drafts SET state=?,revision=?,content_set_id=?,value=? WHERE draft_id=? AND revision=?`, v.State, v.Revision, nullableString(v.ContentSetID), jsonBytes(v), v.ID, expected); e != nil {
		return e
	}
	return t.saveDraftHistory(v)
}

func (t *writeTx) Drafts(project, after string, limit int, scope domain.CredentialScope) ([]domain.Draft, error) {
	owner := scope.Kind == "owner"
	if !owner && !scopeHas(scope.Actions, "draft.write") && !scopeHas(scope.Actions, "draft.publish") {
		return nil, domain.ErrDenied
	}
	if limit < 1 || limit > 64 {
		return nil, domain.ErrInvalidTarget
	}
	rows, e := t.tx.QueryContext(t.ctx, `SELECT d.draft_id FROM drafts d WHERE d.draft_id>? AND (?='' OR d.project_id=?)
 AND (? OR (d.project_id IN (SELECT value FROM json_each(?)) AND d.draft_id IN (SELECT value FROM json_each(?))))
 AND (d.document_id IS NULL OR EXISTS(SELECT 1 FROM documents x WHERE x.document_id=d.document_id AND x.lifecycle IN ('active','archived')))
 AND (d.state!='published' OR EXISTS(SELECT 1 FROM documents x WHERE x.document_id=json_extract(d.value,'$.AllocatedDocumentID') AND x.lifecycle IN ('active','archived')))
 ORDER BY d.draft_id LIMIT ?`, after, project, project, owner, jsonBytes(scope.ProjectIDs), jsonBytes(scope.DraftIDs), limit)
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
	if e := errors.Join(rows.Err(), rows.Close()); e != nil {
		return nil, e
	}
	out := []domain.Draft{}
	for _, id := range ids {
		v, e := t.Draft(id)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}

func (t *writeTx) DraftHistory(id string, after int64, limit int) ([]domain.Draft, error) {
	if limit < 1 || limit > 64 || after < 0 {
		return nil, domain.ErrInvalidTarget
	}
	rows, e := t.tx.QueryContext(t.ctx, `SELECT value FROM draft_history WHERE draft_id=? AND revision>? ORDER BY revision LIMIT ?`, id, after, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Draft{}
	for rows.Next() {
		var raw []byte
		var v domain.Draft
		if e := rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e := json.Unmarshal(raw, &v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (t *writeTx) WorkflowPrincipalByBinding(id string) (domain.WorkflowPrincipal, error) {
	var principal string
	if e := t.tx.QueryRowContext(t.ctx, `SELECT principal_id FROM workflow_principals WHERE binding_id=?`, id).Scan(&principal); e != nil {
		return domain.WorkflowPrincipal{}, notFound(e)
	}
	return t.WorkflowPrincipal(principal)
}

func (t *writeTx) WorkflowSelection(id string) (domain.WorkflowSelection, error) {
	var raw []byte
	var v domain.WorkflowSelection
	if e := t.tx.QueryRowContext(t.ctx, `SELECT value FROM workflow_selections WHERE binding_id=?`, id).Scan(&raw); e != nil {
		return v, notFound(e)
	}
	if e := json.Unmarshal(raw, &v); e != nil {
		return v, e
	}
	if v.BindingID != id {
		return v, domain.ErrInvalidTarget
	}
	return v, nil
}

func (t *writeTx) PutWorkflowSelection(v domain.WorkflowSelection, expected int64) error {
	p, e := t.WorkflowPrincipal(v.PrincipalID)
	if e != nil {
		return e
	}
	if p.BindingID != v.BindingID || !p.Active || !scopeHas(p.Scope.ProjectIDs, v.ProjectID) {
		return domain.ErrDenied
	}
	if _, e := t.Scope(v.ProjectID); e != nil {
		return e
	}
	if v.DocumentID != nil {
		d, e := t.Document(*v.DocumentID)
		if e != nil {
			return e
		}
		if d.ProjectID != v.ProjectID || d.Lifecycle == "trash" || d.Lifecycle == "purged" {
			return domain.ErrInvalidTarget
		}
	}
	old, e := t.WorkflowSelection(v.BindingID)
	if errors.Is(e, domain.ErrNotFound) {
		if expected != 0 {
			return domain.ErrPrecondition
		}
		v.Revision = 1
		return t.exec(`INSERT INTO workflow_selections(binding_id,principal_id,project_id,document_id,revision,value) VALUES(?,?,?,?,1,?)`, v.BindingID, v.PrincipalID, v.ProjectID, v.DocumentID, jsonBytes(v))
	}
	if e != nil {
		return e
	}
	if expected != old.Revision || old.PrincipalID != v.PrincipalID {
		return domain.ErrPrecondition
	}
	v.Revision = old.Revision
	if reflect.DeepEqual(old, v) {
		return nil
	}
	v.Revision++
	return t.exec(`UPDATE workflow_selections SET project_id=?,document_id=?,revision=?,value=? WHERE binding_id=? AND revision=?`, v.ProjectID, v.DocumentID, v.Revision, jsonBytes(v), v.BindingID, expected)
}

func scopeHas(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
