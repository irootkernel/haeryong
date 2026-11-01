package importer

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// DraftView excludes private generation/transport paths and reserved document IDs.
// Its validator belongs to this complete public draft snapshot.
type DraftView struct {
	ID                 string  `json:"draft_id"`
	ProjectID          string  `json:"project_id"`
	DocumentID         *string `json:"document_id"`
	BaseVersionID      *string `json:"base_version_id"`
	JobID              string  `json:"job_id"`
	State              string  `json:"state"`
	Intent             string  `json:"intent"`
	Revision           int64   `json:"revision"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
	PayloadSHA         *string `json:"payload_sha256"`
	MetadataSHA        *string `json:"metadata_sha256"`
	PublishedVersionID *string `json:"published_version_id"`
	FailureReason      *string `json:"failure_reason"`
	ETag               string  `json:"etag"`
}

func draftView(d domain.Draft) DraftView {
	v := DraftView{ID: d.ID, ProjectID: d.ProjectID, DocumentID: d.DocumentID, BaseVersionID: d.BaseVersionID, JobID: d.JobID, State: d.State, Intent: d.Intent, Revision: d.Revision, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt, PayloadSHA: strptr(d.PayloadSHA), MetadataSHA: strptr(d.MetadataHash), PublishedVersionID: strptr(d.PublishedVersionID), FailureReason: strptr(d.FailureReason)}
	v.ETag = fmt.Sprintf("\"draft-%x\"", sha256.Sum256(encoded(v)))
	return v
}
func (s *Service) draftCurrent(ctx context.Context, tx app.WriteTx, token, id, method, action string) (domain.Draft, error) {
	r, e := s.Auth.AuthenticateWrite(ctx, tx, token)
	if e != nil {
		return domain.Draft{}, e
	}
	target := "/api/v1/drafts/" + id
	if action == "publish" {
		target += "/publish"
	}
	request := domain.Request{Key: domain.OperationKey{PrincipalID: r.PrincipalID}, Method: method, Target: target, Body: encoded(map[string]string{"action": action})}
	if e = s.Auth.Authority(token).AuthorizeWrite(ctx, tx, request); e != nil {
		return domain.Draft{}, e
	}
	d, e := tx.Draft(id)
	if e != nil {
		return d, e
	}
	if _, e = tx.Scope(d.ProjectID); e != nil {
		return d, e
	}
	doc := value(d.DocumentID)
	if d.State == "published" {
		doc = d.AllocatedDocumentID
	}
	if doc != "" {
		current, e := tx.Document(doc)
		if e != nil {
			return d, e
		}
		if current.ProjectID != d.ProjectID || current.Lifecycle == "trash" || current.Lifecycle == "purged" {
			return d, domain.ErrNotFound
		}
	}
	return d, nil
}
func (s *Service) Draft(ctx context.Context, token, id string) (out DraftView, err error) {
	if !idPattern.MatchString(id) {
		return out, sec.ErrInvalid
	}
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		d, e := s.draftCurrent(ctx, tx, token, id, "GET", "")
		if e == nil {
			out = draftView(d)
		}
		return e
	})
	return
}
func (s *Service) Drafts(ctx context.Context, token, project, after string) (out Page[DraftView], err error) {
	out.Items = []DraftView{}
	if project != "" && !idPattern.MatchString(project) || after != "" && !idPattern.MatchString(after) {
		return out, sec.ErrInvalid
	}
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		actor, e := s.Auth.AuthenticateWrite(ctx, tx, token)
		if e != nil {
			return e
		}
		r := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID}, Method: "GET", Target: "/api/v1/drafts"}
		if e = s.Auth.Authority(token).AuthorizeWrite(ctx, tx, r); e != nil {
			return e
		}
		rows, e := tx.Drafts(project, after, 64, actor.Scope)
		if e != nil {
			return e
		}
		for _, d := range rows {
			out.Items = append(out.Items, draftView(d))
		}
		if len(rows) == 64 {
			out.NextCursor = strptr(rows[len(rows)-1].ID)
		}
		return nil
	})
	return
}
func (s *Service) DraftHistory(ctx context.Context, token, id string, after int64) (out Page[DraftView], err error) {
	out.Items = []DraftView{}
	if !idPattern.MatchString(id) || after < 0 {
		return out, sec.ErrInvalid
	}
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		if _, e := s.draftCurrent(ctx, tx, token, id, "GET", ""); e != nil {
			return e
		}
		rows, e := tx.DraftHistory(id, after, 64)
		if e != nil {
			return e
		}
		for _, d := range rows {
			out.Items = append(out.Items, draftView(d))
		}
		if len(rows) == 64 {
			cursor := strconv.FormatInt(rows[len(rows)-1].Revision, 10)
			out.NextCursor = &cursor
		}
		return nil
	})
	return
}
func (s *Service) RejectDraft(ctx context.Context, token, key, id, etag string) (domain.Response, error) {
	if !idPattern.MatchString(id) || etag == "" {
		return domain.Response{}, sec.ErrInvalid
	}
	r, e := s.request(ctx, token, key, "PATCH", "/api/v1/drafts/"+id, map[string]string{"action": "reject"})
	if e != nil {
		return domain.Response{}, e
	}
	r.Preconditions = map[string]string{"if-match": etag}
	return app.Execute(ctx, s.Store, s.Auth.Authority(token), r, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		d, e := s.draftCurrent(ctx, tx, token, id, "PATCH", "reject")
		if e != nil {
			return domain.Response{}, e
		}
		if draftView(d).ETag != etag {
			return domain.Response{}, domain.ErrPrecondition
		}
		if d.State != "preparing" && d.State != "ready" {
			return domain.Response{}, domain.ErrTransition
		}
		if _, e = tx.CancelJob(d.JobID); e != nil {
			return domain.Response{}, e
		}
		d.State = "rejected"
		d.UpdatedAt = s.now()
		if e = tx.UpdateDraft(d, d.Revision); e != nil {
			return domain.Response{}, e
		}
		d, e = tx.Draft(id)
		if e != nil {
			return domain.Response{}, e
		}
		return draftResponse(d), nil
	})
}
func draftResponse(d domain.Draft) domain.Response {
	v := draftView(d)
	return domain.Response{Status: 200, Headers: map[string]string{"ETag": v.ETag}, Body: encoded(v)}
}

// PublishDraft parses only optional metadata outside the writer; publication,
// current scope/base checks and the draft revision commit atomically together.
func (s *Service) PublishDraft(ctx context.Context, token, key, id, etag string) (domain.Response, error) {
	if !idPattern.MatchString(id) || etag == "" {
		return domain.Response{}, sec.ErrInvalid
	}
	r, e := s.request(ctx, token, key, "POST", "/api/v1/drafts/"+id+"/publish", map[string]any{})
	if e != nil {
		return domain.Response{}, e
	}
	r.Preconditions = map[string]string{"if-match": etag}
	if response, found, e := app.Replay(ctx, s.Store, s.Auth.Authority(token), r); e != nil || found {
		return response, e
	}
	var d domain.Draft
	var g domain.GenerationIntent
	var metadata domain.Object
	e = s.Store.Write(ctx, func(tx app.WriteTx) error {
		var e error
		d, e = s.draftCurrent(ctx, tx, token, id, "POST", "publish")
		if e != nil {
			return e
		}
		if draftView(d).ETag != etag {
			return domain.ErrPrecondition
		}
		if d.State != "ready" {
			return domain.ErrTransition
		}
		g, e = tx.GenerationIntent(d.JobID)
		if e != nil {
			return e
		}
		if d.MetadataHash != "" {
			metadata, e = tx.Object(d.MetadataHash)
		}
		return e
	})
	if e != nil {
		return domain.Response{}, e
	}
	check := func(tx app.WriteTx) error {
		current, e := s.draftCurrent(ctx, tx, token, id, "POST", "publish")
		if e != nil {
			return e
		}
		if draftView(current).ETag != etag {
			return domain.ErrPrecondition
		}
		return nil
	}
	preparation, e := s.prepareMetadataGuarded(ctx, token, metadata, d.ContentSetID, check)
	if e != nil {
		return domain.Response{}, e
	}
	defer s.releaseMetadataPreparation(preparation)
	i := generationImport(g)
	i.JobID = preparation
	i.ContentSetID = d.ContentSetID
	i.PayloadSHA = d.PayloadSHA
	i.MetadataHash = d.MetadataHash
	i.VersionID = d.ProposedVersionID
	v := normalizedVersion(i, nil)
	v.ManifestSHA = d.ManifestSHA
	v.Metadata = d.Metadata
	var summary struct {
		SourceBasis string `json:"source_basis"`
		Document    struct {
			Title string `json:"title"`
		} `json:"document"`
		Content struct {
			DefaultPage *string `json:"default_page"`
		} `json:"content"`
	}
	if json.Unmarshal(d.Metadata, &summary) != nil {
		return domain.Response{}, ErrPackageInvalid
	}
	v.SourceBasis = summary.SourceBasis
	v.Title = summary.Document.Title
	v.DefaultPage = summary.Content.DefaultPage
	if metadata.Hash != "" {
		e = s.Bytes.Read(ctx, metadata, domain.Pin{Kind: "import_prepare", OwnerID: preparation}, func(reader io.ReadSeeker) error {
			return s.parseMetadata(ctx, bufio.NewReader(&generationReader{Reader: reader, ctx: ctx, store: s.Store, check: check}), i, &v, nil)
		})
		if e != nil {
			return domain.Response{}, e
		}
	}
	i.Title = v.Title
	return app.Execute(ctx, s.Store, s.Auth.Authority(token), r, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		current, e := s.draftCurrent(ctx, tx, token, id, "POST", "publish")
		if e != nil {
			return domain.Response{}, e
		}
		if draftView(current).ETag != etag {
			return domain.Response{}, domain.ErrPrecondition
		}
		if current.State != "ready" {
			return domain.Response{}, domain.ErrTransition
		}
		base, e := generationBaseCurrent(tx, g)
		if e != nil {
			return domain.Response{}, e
		}
		if !base {
			return domain.Response{}, ErrVersionConflict
		}
		if e = validatePreparedMetadata(tx, preparation, metadata, d.ContentSetID, r.Key.PrincipalID); e != nil {
			return domain.Response{}, e
		}
		if e = tx.FinalizeImport(i, v); e != nil {
			return domain.Response{}, e
		}
		current.State = "published"
		current.PublishedVersionID = v.ID
		current.UpdatedAt = s.now()
		if e = tx.UpdateDraft(current, current.Revision); e != nil {
			return domain.Response{}, e
		}
		current, e = tx.Draft(id)
		if e != nil {
			return domain.Response{}, e
		}
		return draftResponse(current), nil
	})
}

// ReplaceDraft retains the captured base and every previous candidate. Sealed
// staging is an input capability for this exact draft, not a retargetable ID.
func (s *Service) ReplaceDraft(ctx context.Context, token, key, id, etag, upload string, ref *ObjectRef) (domain.Response, error) {
	if ref != nil && (!idPattern.MatchString(ref.ID) || !hashPattern.MatchString(ref.Hash) || ref.MediaType != "application/json") {
		return domain.Response{}, sec.ErrInvalid
	}
	if !idPattern.MatchString(id) || !idPattern.MatchString(upload) || etag == "" {
		return domain.Response{}, sec.ErrInvalid
	}
	var d domain.Draft
	var g domain.GenerationIntent
	var u domain.Upload
	var payload string
	var metadata domain.Object
	inspect := func(tx app.WriteTx, fresh bool) error {
		var e error
		current, e := s.draftCurrent(ctx, tx, token, id, "PATCH", "replace")
		if e != nil {
			return e
		}
		u, e = tx.Upload(upload)
		if e != nil {
			return e
		}
		if u.Purpose != "result" {
			return sec.ErrInvalid
		}
		var target struct {
			Project  string  `json:"project_id"`
			Document *string `json:"document_id"`
			Draft    *string `json:"draft_id"`
		}
		if json.Unmarshal(u.Target, &target) != nil || target.Draft == nil || *target.Draft != id || target.Project != current.ProjectID || value(target.Document) != value(current.DocumentID) {
			return sec.ErrInvalid
		}
		if fresh {
			if e = s.current(ctx, tx, token, u); e != nil {
				return e
			}
			if draftView(current).ETag != etag {
				return domain.ErrPrecondition
			}
			if current.State != "ready" || u.State != "sealed" {
				return domain.ErrTransition
			}
		}
		var sealed bool
		payload, sealed, e = tx.ContentSet(u.ContentSetID)
		if e != nil {
			return e
		}
		if !sealed {
			return domain.ErrTransition
		}
		if ref != nil {
			m, e := tx.Upload(ref.ID)
			if e != nil {
				return e
			}
			if m.Purpose != "metadata" || m.ObjectHash != ref.Hash || m.Checkpoint.AcceptedBytes != ref.Size {
				return sec.ErrInvalid
			}
			var mt struct {
				Action    string  `json:"action"`
				Operation string  `json:"operation_id"`
				Project   string  `json:"project_id"`
				Document  *string `json:"document_id"`
				Draft     *string `json:"draft_id"`
			}
			if json.Unmarshal(m.Target, &mt) != nil || mt.Action != "draft_replace" || mt.Operation != key || mt.Draft == nil || *mt.Draft != id {
				return sec.ErrInvalid
			}
			if fresh {
				if e = s.current(ctx, tx, token, m); e != nil {
					return e
				}
				if m.State != "sealed" {
					return domain.ErrTransition
				}
			}
			metadata, e = tx.Object(m.ObjectHash)
			if e != nil {
				return e
			}
			if strconv.FormatInt(metadata.Size, 10) != ref.Size {
				return sec.ErrInvalid
			}
		}
		d = current
		g, e = tx.GenerationIntent(d.JobID)
		return e
	}
	if e := s.Store.Write(ctx, func(tx app.WriteTx) error { return inspect(tx, false) }); e != nil {
		return domain.Response{}, e
	}
	r, e := s.request(ctx, token, key, "PATCH", "/api/v1/drafts/"+id, map[string]any{"action": "replace", "payload_sha256": payload, "metadata_sha256": metadata.Hash})
	if e != nil {
		return domain.Response{}, e
	}
	r.Preconditions = map[string]string{"if-match": etag}
	if response, found, e := app.Replay(ctx, s.Store, s.Auth.Authority(token), r); e != nil || found {
		return response, e
	}
	// Snapshot the authorized candidate and the exact staged inputs before doing
	// any new parsing. Later guards prohibit candidate changes or handle reuse.
	if e = s.Store.Write(ctx, func(tx app.WriteTx) error { return inspect(tx, true) }); e != nil {
		return domain.Response{}, e
	}
	originalSet, originalPayload := u.ContentSetID, payload
	check := func(tx app.WriteTx) error {
		if e := inspect(tx, true); e != nil {
			return e
		}
		if u.ContentSetID != originalSet || payload != originalPayload {
			return domain.ErrConflict
		}
		return nil
	}
	preparation, e := s.prepareMetadataGuarded(ctx, token, metadata, u.ContentSetID, check)
	if e != nil {
		return domain.Response{}, e
	}
	defer s.releaseMetadataPreparation(preparation)
	version, e := service.NewID()
	if e != nil {
		return domain.Response{}, e
	}
	i := generationImport(g)
	i.JobID = preparation
	i.VersionID = version
	i.ContentSetID = u.ContentSetID
	i.PayloadSHA = payload
	i.MetadataHash = metadata.Hash
	defer s.Store.Write(context.Background(), func(tx app.WriteTx) error { return tx.ReleasePin("metadata-request:" + version) })
	v, e := s.prepareDraftCandidate(ctx, i, metadata, check)
	if e != nil {
		return domain.Response{}, e
	}
	return app.Execute(ctx, s.Store, s.Auth.Authority(token), r, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		if e := check(tx); e != nil {
			return domain.Response{}, e
		}
		if e := validatePreparedMetadata(tx, preparation, metadata, originalSet, r.Key.PrincipalID); e != nil {
			return domain.Response{}, e
		}
		// The old provider reservation cannot apply over this explicit replacement.
		// Its complete saved object and accounting remain retained independently.
		if _, e := tx.CancelJob(d.JobID); e != nil {
			return domain.Response{}, e
		}
		d.ProposedVersionID = v.ID
		d.ContentSetID = v.ContentSetID
		d.PayloadSHA = v.PayloadSHA
		d.MetadataHash = v.MetadataHash
		d.ManifestSHA = v.ManifestSHA
		d.Metadata = v.Metadata
		d.UpdatedAt = s.now()
		if e := tx.UpdateDraft(d, d.Revision); e != nil {
			return domain.Response{}, e
		}
		current, e := tx.Draft(id)
		if e != nil {
			return domain.Response{}, e
		}
		return draftResponse(current), nil
	})
}
