package query

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"strconv"
	"time"
)

type PersonalConflict struct {
	Revision *int64
	ETag     *string
}

func (e *PersonalConflict) Error() string { return "PRECONDITION_FAILED" }
func (e *PersonalConflict) Unwrap() error { return domain.ErrPrecondition }

type AnnotationBodyRef struct {
	BytesSHA256 string `json:"bytes_sha256"`
	ByteSize    string `json:"byte_size"`
	MediaType   string `json:"media_type"`
	ContentURL  string `json:"content_url"`
}
type AnchorResolution struct {
	Status string  `json:"status"`
	Reason *string `json:"reason"`
}
type AnnotationView struct {
	AnnotationID   string                  `json:"annotation_id"`
	DocumentID     string                  `json:"document_id"`
	OriginalAnchor domain.AnnotationAnchor `json:"original_anchor"`
	Anchor         domain.AnnotationAnchor `json:"anchor"`
	Body           *string                 `json:"body"`
	BodyRef        *AnnotationBodyRef      `json:"body_ref"`
	SelectedText   *string                 `json:"selected_text"`
	ContextRef     *string                 `json:"context_ref"`
	CreatedAt      string                  `json:"created_at"`
	UpdatedAt      string                  `json:"updated_at"`
	DeletedAt      *string                 `json:"deleted_at"`
	Revision       int64                   `json:"revision"`
	ETag           string                  `json:"etag"`
	HistoryURL     string                  `json:"history_url"`
	Resolution     AnchorResolution        `json:"resolution"`
	Action         string                  `json:"action"`
	ActorSession   string                  `json:"actor_session"`
	OperationID    string                  `json:"operation_id,omitempty"`
}

func annotationETag(n domain.Annotation) string {
	return personalETag("annotation", n.OwnerID+"/"+n.ID, n.Revision, encodeJSON(n))
}
func annotationView(tx app.WriteTx, n domain.Annotation) AnnotationView {
	v := AnnotationView{AnnotationID: n.ID, DocumentID: n.DocumentID, OriginalAnchor: n.OriginalAnchor, Anchor: n.Anchor, Body: n.Body, SelectedText: n.SelectedText, ContextRef: n.ContextRef, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, DeletedAt: n.DeletedAt, Revision: n.Revision, ETag: annotationETag(n), HistoryURL: "/api/v1/annotations/" + n.ID + "/history", Resolution: AnchorResolution{Status: "resolved"}, Action: n.Action, ActorSession: n.ActorSession}
	if n.Anchor.RegionID != nil {
		if _, err := tx.VersionRegion(n.Anchor.VersionID, *n.Anchor.PagePath, *n.Anchor.RegionID); errors.Is(err, domain.ErrNotFound) {
			v.Resolution = AnchorResolution{Status: "unresolved", Reason: nullable("region_unavailable")}
		}
	}
	if n.BodyObject != nil {
		v.BodyRef = &AnnotationBodyRef{n.BodyObject.Hash, strconv.FormatInt(n.BodyObject.Size, 10), "text/markdown; charset=utf-8", fmt.Sprintf("/api/v1/annotations/%s/body?revision=%d", n.ID, n.Revision)}
	}
	if n.DeletedAt != nil {
		v.Body = nil
		v.BodyRef = nil
		v.ContextRef = nil
		v.SelectedText = nil
	}
	return v
}
func (s *Service) noteOwner(ctx context.Context, tx app.WriteTx, token string) (domain.Session, error) {
	a, err := s.Auth.AuthenticateWrite(ctx, tx, token)
	if err == nil && a.Scope.Kind != "owner" {
		err = domain.ErrDenied
	}
	return a, err
}
func (s *Service) noteTx(ctx context.Context, tx app.WriteTx, token, id string) (domain.Annotation, error) {
	a, err := s.noteOwner(ctx, tx, token)
	if err != nil {
		return domain.Annotation{}, err
	}
	n, err := tx.Annotation(id)
	if err != nil {
		return n, err
	}
	if n.OwnerID != a.PrincipalID {
		return n, domain.ErrDenied
	}
	_, err = visibleDocument(tx, n.DocumentID)
	return n, err
}

// The owner write contract exposes a known target's blocked lifecycle;
// reads and receipt filtering continue to use the masked visibility helpers.
func personalWriteDocument(tx app.WriteTx, id string) (domain.Document, error) {
	d, err := tx.Document(id)
	if err != nil {
		return d, err
	}
	if _, err = tx.Scope(d.ProjectID); err != nil {
		return d, err
	}
	if d.Lifecycle == "trash" || d.Lifecycle == "purged" {
		return d, &Transition{Lifecycle: d.Lifecycle}
	}
	return d, nil
}
func (s *Service) noteWriteTx(ctx context.Context, tx app.WriteTx, token, id string) (domain.Annotation, error) {
	a, err := s.noteOwner(ctx, tx, token)
	if err != nil {
		return domain.Annotation{}, err
	}
	n, err := tx.Annotation(id)
	if err != nil {
		return n, err
	}
	if n.OwnerID != a.PrincipalID {
		return n, domain.ErrNotFound
	}
	_, err = personalWriteDocument(tx, n.DocumentID)
	return n, err
}
func (s *Service) AnnotationGet(ctx context.Context, token, id string) (AnnotationView, error) {
	var out AnnotationView
	err := s.read(ctx, token, "/api/v1/annotations/"+id, func(tx app.WriteTx) error {
		n, err := s.noteTx(ctx, tx, token, id)
		if err == nil {
			out = annotationView(tx, n)
		}
		return err
	})
	return out, err
}
func (s *Service) AnnotationList(ctx context.Context, token, version string, o Options) (List[AnnotationView], error) {
	out := List[AnnotationView]{Items: []AnnotationView{}}
	err := s.read(ctx, token, "/api/v1/versions/"+version+"/annotations", func(tx app.WriteTx) error {
		a, err := s.noteOwner(ctx, tx, token)
		if err != nil {
			return err
		}
		if _, err = visibleVersion(tx, version); err != nil {
			return err
		}
		c, limit, _, err := s.cursorStart(ctx, token, tx, "annotations/"+version, o)
		if err != nil {
			return err
		}
		rows, err := tx.Annotations(a.PrincipalID, version, c.Key, limit+1)
		if err != nil {
			return err
		}
		for i, n := range rows {
			if i == limit {
				c.Key = rows[i-1].ID
				out.NextCursor = cursorNext(c)
				break
			}
			out.Items = append(out.Items, annotationView(tx, n))
		}
		return nil
	})
	return out, err
}
func (s *Service) AnnotationHistory(ctx context.Context, token, id string, o Options) (List[AnnotationView], error) {
	out := List[AnnotationView]{Items: []AnnotationView{}}
	err := s.read(ctx, token, "/api/v1/annotations/"+id+"/history", func(tx app.WriteTx) error {
		current, err := s.noteTx(ctx, tx, token, id)
		if err != nil {
			return err
		}
		c, limit, _, err := s.cursorStart(ctx, token, tx, "annotation-history/"+id, o)
		if err != nil {
			return err
		}
		rows, err := tx.AnnotationHistory(id, c.Sequence, limit+1)
		if err != nil {
			return err
		}
		for i, n := range rows {
			if i == limit {
				c.Sequence = rows[i-1].Revision
				out.NextCursor = cursorNext(c)
				break
			}
			n.OwnerID = current.OwnerID
			view := annotationView(tx, n)
			view.OperationID = n.OperationID
			out.Items = append(out.Items, view)
		}
		return nil
	})
	return out, err
}

type annotationInput struct {
	Action                   string
	Anchor                   domain.AnnotationAnchor
	Body                     *string
	UploadID                 string
	SelectedText, ContextRef *string
	Context                  json.RawMessage
}

func decodeAnchor(raw []byte) (domain.AnnotationAnchor, error) {
	var a domain.AnnotationAnchor
	f, err := strictObject(raw)
	if err != nil || len(f) != 5 {
		return a, &Error{"invalid_anchor"}
	}
	if a.VersionID, err = requiredString(f, "version_id"); err != nil {
		return a, err
	}
	if err = validOperationKey(a.VersionID); err != nil {
		return a, &Error{"invalid_anchor"}
	}
	if a.PagePath, err = nullableField(f, "page_path", true); err != nil {
		return a, err
	}
	if a.RegionID, err = nullableField(f, "region_id", true); err != nil {
		return a, err
	}
	if a.PageQuery, err = requiredString(f, "page_query"); err != nil {
		return a, err
	}
	if a.PageFragment, err = requiredString(f, "page_fragment"); err != nil {
		return a, err
	}
	if a.PagePath == nil && (a.RegionID != nil || a.PageQuery != "" || a.PageFragment != "") {
		return a, &Error{"invalid_anchor"}
	}
	if a.RegionID != nil && *a.RegionID == "" {
		return a, &Error{"invalid_anchor"}
	}
	return a, nil
}
func decodeAnnotation(method, version string, body []byte) (annotationInput, error) {
	var in annotationInput
	if method == "DELETE" {
		in.Action = "delete"
		if len(bytes.TrimSpace(body)) != 0 {
			return in, &Error{"invalid_body"}
		}
		return in, nil
	}
	f, err := strictObject(body)
	if err != nil {
		return in, err
	}
	in.Action = "create"
	if method == "PATCH" {
		in.Action, err = requiredString(f, "action")
		if err != nil {
			return in, err
		}
		delete(f, "action")
		if in.Action != "edit" && in.Action != "reattach" {
			return in, &Error{"invalid_action"}
		}
	}
	if in.Action == "create" {
		a := map[string]json.RawMessage{"version_id": encodeJSON(version)}
		for _, k := range []string{"page_path", "region_id", "page_query", "page_fragment"} {
			a[k] = f[k]
			delete(f, k)
		}
		in.Anchor, err = decodeAnchor(encodeJSON(a))
	} else if in.Action == "reattach" {
		in.Anchor, err = decodeAnchor(f["anchor"])
		delete(f, "anchor")
	}
	if err != nil {
		return in, err
	}
	if in.SelectedText, err = nullableField(f, "selected_text", true); err != nil {
		return in, err
	}
	delete(f, "selected_text")
	if in.ContextRef, err = nullableField(f, "context_ref", true); err != nil {
		return in, err
	}
	delete(f, "context_ref")
	raw, ok := f["context"]
	if !ok {
		return in, &Error{"invalid_body"}
	}
	delete(f, "context")
	if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if in.ContextRef != nil {
			return in, &Error{"invalid_context"}
		}
		in.Context = raw
	}
	if in.ContextRef != nil && validOperationKey(*in.ContextRef) != nil {
		return in, &Error{"invalid_context"}
	}
	if in.Action != "reattach" {
		if in.Body, err = nullableField(f, "body", true); err != nil {
			return in, err
		}
		delete(f, "body")
		ref, ok := f["body_ref"]
		if !ok {
			return in, &Error{"invalid_body"}
		}
		delete(f, "body_ref")
		if !bytes.Equal(bytes.TrimSpace(ref), []byte("null")) {
			r, e := strictObject(ref)
			if e != nil || len(r) != 1 {
				return in, &Error{"invalid_body_ref"}
			}
			in.UploadID, err = requiredString(r, "upload_id")
			if err != nil || validOperationKey(in.UploadID) != nil {
				return in, &Error{"invalid_body_ref"}
			}
		}
		if (in.Body == nil) == (in.UploadID == "") {
			return in, &Error{"body_required"}
		}
	}
	if len(f) != 0 {
		return in, &Error{"invalid_body"}
	}
	return in, nil
}

// Inline context has the same binding as the record we will store. Capture
// that implicit default in the receipt, so replay keeps the original binding
// after another session or a later reattachment changes the current state.
type annotationContextBinding struct {
	VersionID        string  `json:"version_id"`
	PagePath         *string `json:"page_path"`
	RenderInstanceID *string `json:"render_instance_id"`
	SessionID        string  `json:"session_id"`
}
type annotationNormalization struct {
	InlineContextBinding *annotationContextBinding `json:"inline_context_binding,omitempty"`
}

func noteContextBinding(c domain.ContextRecord) annotationContextBinding {
	return annotationContextBinding{c.VersionID, c.PagePath, c.RenderInstanceID, c.SessionID}
}
func capturedNoteContextBinding(tx app.WriteTx, key domain.OperationKey) (*annotationContextBinding, bool, error) {
	r, err := tx.Receipt(key)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	var defaults annotationNormalization
	if err = json.Unmarshal(r.Normalization, &defaults); err != nil {
		return nil, true, err
	}
	return defaults.InlineContextBinding, true, nil
}
func (s *Service) inlineNoteContextBinding(ctx context.Context, token string, actor domain.Session, key string, in annotationInput, targetID string) (annotationContextBinding, error) {
	var binding annotationContextBinding
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		captured, existing, err := capturedNoteContextBinding(tx, domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key})
		if err != nil {
			return err
		}
		if captured != nil {
			binding = *captured
			return nil
		}
		anchor := in.Anchor
		if in.Action != "create" {
			var n domain.Annotation
			if existing {
				n, err = s.noteTx(ctx, tx, token, targetID)
			} else {
				n, err = s.noteWriteTx(ctx, tx, token, targetID)
			}
			if err != nil {
				return err
			}
			if in.Action == "edit" {
				anchor = n.Anchor
			}
		}
		if in.Action == "create" || in.Action == "reattach" {
			v, e := tx.PublishedVersion(anchor.VersionID)
			if e != nil {
				return e
			}
			if existing {
				_, err = visibleDocument(tx, v.DocumentID)
			} else {
				_, err = personalWriteDocument(tx, v.DocumentID)
			}
			if err != nil {
				return err
			}
			anchor.PagePath, err = s.canonicalRefTx(ctx, tx, anchor.VersionID, v.ContentSetID, anchor.PagePath, false)
			if err != nil {
				return err
			}
		}
		binding = annotationContextBinding{VersionID: anchor.VersionID, PagePath: anchor.PagePath, SessionID: actor.ID}
		return nil
	})
	return binding, err
}

// Immutable preparation uses the existing object writer; no nested transaction or HTTP call.
type annotationBytesWriter interface {
	Put(context.Context, io.Reader, func(app.WriteTx, domain.Object) error) (domain.Object, error)
}

func (s *Service) AnnotationMutate(ctx context.Context, token, key, method, targetID, etag string, body []byte) (domain.Response, error) {
	in, err := decodeAnnotation(method, targetID, body)
	if err != nil {
		return domain.Response{}, err
	}
	actor, err := s.Auth.Authenticate(ctx, token)
	if err != nil {
		return domain.Response{}, err
	}
	if actor.Scope.Kind != "owner" {
		return domain.Response{}, domain.ErrNotFound
	}
	if err = validOperationKey(key); err != nil {
		return domain.Response{}, err
	}
	target := "/api/v1/annotations/" + targetID
	if method == "POST" {
		target = "/api/v1/versions/" + targetID + "/annotations"
	}
	var staged *domain.ContextRecord
	var prepare string
	var inlineBinding annotationContextBinding
	if len(in.Context) > 0 {
		inlineBinding, err = s.inlineNoteContextBinding(ctx, token, actor, key, in, targetID)
		if err != nil {
			return domain.Response{}, err
		}
		writer, ok := s.Bytes.(annotationBytesWriter)
		if !ok {
			return domain.Response{}, &Error{"context_storage_unavailable"}
		}
		prepare, err = service.NewID()
		if err != nil {
			return domain.Response{}, err
		}
		data := []byte(in.Context)
		media := "application/json"
		var text string
		if json.Unmarshal(in.Context, &text) == nil {
			data = []byte(text)
			media = "text/plain"
		}
		object, e := writer.Put(ctx, bytes.NewReader(data), func(tx app.WriteTx, o domain.Object) error {
			return tx.AcquirePin(domain.Pin{ID: prepare, Kind: "context_prepare", OwnerID: "annotation-request:" + key, Hash: o.Hash})
		})
		if e != nil {
			return domain.Response{}, e
		}
		defer s.Store.Write(context.Background(), func(tx app.WriteTx) error { return tx.ReleasePin(prepare) })
		staged = &domain.ContextRecord{PrincipalID: actor.PrincipalID, SessionID: actor.ID, MediaType: media, Object: object}
	}
	// Handles are interchangeable only when both bytes and semantic bindings
	// match. Receipt replay precedes adoption, so adoption cannot check a
	// changed binding on our behalf.
	normalized := map[string]any{}
	if method != "DELETE" {
		_ = json.Unmarshal(body, &normalized)
	}
	var uploadObject domain.Object
	var bodyBinding any
	if in.Action == "create" {
		bodyBinding = map[string]any{"operation_id": key, "action": "annotation_create", "version_id": targetID, "page_path": in.Anchor.PagePath}
	} else if in.Action == "edit" {
		bodyBinding = map[string]any{"operation_id": key, "action": "annotation_edit", "annotation_id": targetID}
	}
	if in.UploadID != "" {
		err = s.Store.Write(ctx, func(tx app.WriteTx) error {
			u, e := tx.Upload(in.UploadID)
			if e != nil {
				return e
			}
			if u.PrincipalID != actor.PrincipalID || u.Purpose != "body" {
				return domain.ErrDenied
			}
			uploadObject, e = tx.Object(u.ObjectHash)
			bodyBinding = json.RawMessage(u.Target)
			return e
		})
		if err != nil {
			return domain.Response{}, err
		}
	}
	if in.Action == "create" || in.Action == "edit" {
		hash := uploadObject.Hash
		if in.Body != nil {
			sum := sha256.Sum256([]byte(*in.Body))
			hash = hex.EncodeToString(sum[:])
		}
		delete(normalized, "body")
		delete(normalized, "body_ref")
		normalized["body_bytes_sha256"] = hash
		normalized["body_binding"] = bodyBinding
	}
	delete(normalized, "context")
	delete(normalized, "context_ref")
	normalized["context_identity"] = nil
	if staged != nil {
		normalized["context_identity"] = map[string]any{"bytes_sha256": staged.Object.Hash, "media_type": staged.MediaType, "binding": inlineBinding}
	}
	if in.ContextRef != nil {
		var c domain.ContextRecord
		err = s.Store.Write(ctx, func(tx app.WriteTx) error {
			var e error
			c, e = tx.Context(*in.ContextRef)
			if e == nil && c.PrincipalID != actor.PrincipalID {
				return domain.ErrDenied
			}
			return e
		})
		if err != nil {
			return domain.Response{}, err
		}
		normalized["context_identity"] = map[string]any{"bytes_sha256": c.Object.Hash, "media_type": c.MediaType, "binding": noteContextBinding(c)}
	}
	request := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key}, Method: method, Target: target, Body: encodeJSON(normalized), Preconditions: map[string]string{}}
	if method != "POST" {
		request.Preconditions["if-match"] = etag
	}
	response, err := app.ExecuteNormalized(ctx, s.Store, s.Auth.Authority(token), request, func(_ app.WriteTx) ([]byte, error) {
		defaults := annotationNormalization{}
		if staged != nil {
			defaults.InlineContextBinding = &inlineBinding
		}
		return encodeJSON(defaults), nil
	}, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		a, err := s.noteOwner(ctx, tx, token)
		if err != nil {
			return domain.Response{}, err
		}
		var n domain.Annotation
		if method == "POST" {
			id, e := service.NewID()
			if e != nil {
				return domain.Response{}, e
			}
			n = domain.Annotation{ID: id, OwnerID: a.PrincipalID, Anchor: in.Anchor, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		} else {
			n, err = s.noteWriteTx(ctx, tx, token, targetID)
			if err != nil {
				return domain.Response{}, err
			}
			tag := annotationETag(n)
			if tag != etag {
				return domain.Response{}, &PersonalConflict{Revision: &n.Revision, ETag: &tag}
			}
			if n.DeletedAt != nil {
				return domain.Response{}, domain.ErrNotFound
			}
		}
		if in.Action == "create" || in.Action == "reattach" {
			v, e := tx.PublishedVersion(in.Anchor.VersionID)
			if e != nil {
				return domain.Response{}, e
			}
			if _, e = personalWriteDocument(tx, v.DocumentID); e != nil {
				return domain.Response{}, e
			}
			if in.Action == "reattach" && v.DocumentID != n.DocumentID {
				return domain.Response{}, &Error{"wrong_document"}
			}
			in.Anchor.PagePath, e = s.canonicalRefTx(ctx, tx, in.Anchor.VersionID, v.ContentSetID, in.Anchor.PagePath, false)
			if e != nil {
				return domain.Response{}, e
			}
			if in.Anchor.RegionID != nil && in.Action == "reattach" {
				if _, e = tx.VersionRegion(in.Anchor.VersionID, *in.Anchor.PagePath, *in.Anchor.RegionID); e != nil {
					return domain.Response{}, &Error{"unresolved_destination"}
				}
			}
			n.Anchor = in.Anchor
			n.DocumentID = v.DocumentID
			if in.Action == "create" {
				n.OriginalAnchor = n.Anchor
			}
		}
		n.Revision++
		n.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		n.Action = in.Action
		n.ActorSession = a.ID
		n.OperationID = key
		if in.Action == "delete" {
			n.DeletedAt = &n.UpdatedAt
			n.Body = nil
			n.BodyObject = nil
			n.ContextRef = nil
			n.SelectedText = nil
		} else {
			n.SelectedText = in.SelectedText
			if in.Action != "reattach" {
				n.Body = in.Body
				n.BodyObject = nil
				if in.UploadID != "" {
					target := map[string]any{"operation_id": key, "action": "annotation_edit", "annotation_id": n.ID}
					if in.Action == "create" {
						target = map[string]any{"operation_id": key, "action": "annotation_create", "version_id": n.Anchor.VersionID, "page_path": n.Anchor.PagePath}
					}
					if err = app.AuthorizeTarget(tx, encodeJSON(target)); err != nil && in.Action != "create" {
						return domain.Response{}, err
					}
					if err = tx.AdoptUploadObject(in.UploadID, "body", encodeJSON(target), domain.Reference{Kind: "annotation_history", ID: fmt.Sprintf("%s/%d", n.ID, n.Revision), Hash: uploadObject.Hash}, ""); err != nil {
						return domain.Response{}, err
					}
					n.BodyObject = &uploadObject
				}
			}
			n.ContextRef = in.ContextRef
			if staged != nil {
				c := *staged
				c.ID, err = service.NewID()
				if err != nil {
					return domain.Response{}, err
				}
				c.VersionID = n.Anchor.VersionID
				c.PagePath = n.Anchor.PagePath
				c.CreatedAt = n.UpdatedAt
				if err = tx.PutContext(c); err != nil {
					return domain.Response{}, err
				}
				n.ContextRef = &c.ID
			}
			if n.ContextRef != nil {
				c, e := tx.Context(*n.ContextRef)
				if e != nil {
					return domain.Response{}, e
				}
				if c.PrincipalID != a.PrincipalID || c.VersionID != n.Anchor.VersionID || !sameNotePage(c.PagePath, n.Anchor.PagePath) {
					return domain.Response{}, &Error{"context_binding_mismatch"}
				}
				if err = tx.AcquirePin(domain.Pin{ID: fmt.Sprintf("annotation:%s:%d:context", n.ID, n.Revision), Kind: "annotation_history", OwnerID: fmt.Sprintf("%s/%d", n.ID, n.Revision), Hash: c.Object.Hash}); err != nil {
					return domain.Response{}, err
				}
				if err = tx.Adopt(domain.Reference{Kind: "annotation_history", ID: fmt.Sprintf("%s/%d", n.ID, n.Revision), Hash: c.Object.Hash}, ""); err != nil {
					return domain.Response{}, err
				}
			}
			if n.BodyObject != nil {
				if err = tx.Adopt(domain.Reference{Kind: "annotation_history", ID: fmt.Sprintf("%s/%d", n.ID, n.Revision), Hash: n.BodyObject.Hash}, ""); err != nil {
					return domain.Response{}, err
				}
			}
		}
		if err = tx.SaveAnnotation(n); err != nil {
			return domain.Response{}, err
		}
		view := annotationView(tx, n)
		view.OperationID = key
		status := 200
		if method == "POST" {
			status = 201
		}
		return domain.Response{Status: status, Body: encodeJSON(view), Headers: map[string]string{"ETag": view.ETag}}, nil
	})
	// A concurrent same-key inline request may have committed its captured
	// default after our preparation read. Recompare against that receipt only;
	// this path cannot reserve or apply a second mutation.
	if errors.Is(err, domain.ErrConflict) && staged != nil {
		var captured *annotationContextBinding
		readErr := s.Store.Write(ctx, func(tx app.WriteTx) error {
			var e error
			captured, _, e = capturedNoteContextBinding(tx, request.Key)
			return e
		})
		if readErr != nil {
			return domain.Response{}, readErr
		}
		if captured != nil {
			normalized["context_identity"] = map[string]any{"bytes_sha256": staged.Object.Hash, "media_type": staged.MediaType, "binding": captured}
			request.Body = encodeJSON(normalized)
			replay, found, replayErr := app.Replay(ctx, s.Store, s.Auth.Authority(token), request)
			if found || replayErr != nil {
				return replay, replayErr
			}
		}
	}
	return response, err
}
func sameNotePage(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func (s *Service) AnnotationObject(ctx context.Context, token, id string, revision int64, kind string) (domain.Object, string, error) {
	var object domain.Object
	media := "text/markdown; charset=utf-8"
	err := s.read(ctx, token, "/api/v1/annotations/"+id, func(tx app.WriteTx) error {
		if _, err := s.noteTx(ctx, tx, token, id); err != nil {
			return err
		}
		n, err := tx.AnnotationRevision(id, revision)
		if err != nil {
			return err
		}
		if n.DeletedAt != nil {
			return domain.ErrNotFound
		}
		if kind == "context" {
			if n.ContextRef == nil {
				return &Missing{Reason: "context_absent"}
			}
			c, e := tx.Context(*n.ContextRef)
			if e != nil {
				return e
			}
			object = c.Object
			media = c.MediaType
		} else {
			if n.BodyObject == nil {
				return &Missing{Reason: "body_not_referenced"}
			}
			object = *n.BodyObject
		}
		return nil
	})
	return object, media, err
}
func (s *Service) ReadAnnotationObject(ctx context.Context, token, id string, revision int64, kind string, consume func(domain.Object, string, io.ReadSeeker) error) error {
	object, media, err := s.AnnotationObject(ctx, token, id, revision, kind)
	if err != nil {
		return err
	}
	return s.Bytes.Read(ctx, object, domain.Pin{Kind: "read", OwnerID: "annotation:" + id}, func(r io.ReadSeeker) error { return consume(object, media, r) })
}
