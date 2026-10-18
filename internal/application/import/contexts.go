// Sealed context consumer. POST /contexts adopts either the exact direct
// UTF-8 JSON/text request bytes or an already sealed purpose=context upload
// whose normalized target tuple matches the request binding exactly
// (import-workflow context binding normalization). Both forms share one
// semantic retry identity: binding tuple plus content digest and media type;
// the transport selector never replaces content identity, so the same key
// with changed bytes conflicts and identical bytes through an alternate
// sealed handle replay. The record owns a context pin and preserves the
// originating owner-session relation; channel disposal never abandons staged
// bytes, and adoption into durable notes/questions remains EPIC-005.
package importer

import (
	"context"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"io"
	"net/url"
)

type ContextQuery struct {
	VersionID        string
	PagePath         *string
	RenderInstanceID *string
	Input            string
	UploadID         string
	MediaType        string
}

// NormalizeContextQuery applies the transport normalization: version_id is
// required; a present parameter is one nonempty once-decoded value; render
// identifiers are canonical UUIDs; input defaults to direct. The upload_id is
// a closed body field of input=upload, never a query parameter.
func NormalizeContextQuery(values url.Values) (ContextQuery, error) {
	q := ContextQuery{Input: "direct"}
	var err error
	q.VersionID = values.Get("version_id")
	if !idPattern.MatchString(q.VersionID) {
		return q, sec.ErrInvalid
	}
	if q.PagePath, err = contextOptionalPath(values, "page_path"); err != nil {
		return q, err
	}
	render, err := contextOptionalPath(values, "render_instance_id")
	if err != nil {
		return q, err
	}
	if render != nil {
		if !idPattern.MatchString(*render) {
			return q, sec.ErrInvalid
		}
		q.RenderInstanceID = render
	}
	if input := values.Get("input"); input != "" {
		if input != "direct" && input != "upload" {
			return q, sec.ErrInvalid
		}
		q.Input = input
	}
	return q, nil
}

func contextOptionalPath(values url.Values, name string) (*string, error) {
	if !values.Has(name) {
		return nil, nil
	}
	value := values.Get(name)
	if value == "" {
		return nil, sec.ErrInvalid
	}
	if name == "page_path" {
		if err := ValidatePath(value); err != nil {
			return nil, sec.ErrInvalid
		}
	}
	return &value, nil
}

func (q ContextQuery) target() []byte {
	return encoded(map[string]any{"version_id": q.VersionID, "page_path": nullableStringOf(q.PagePath), "render_instance_id": nullableStringOf(q.RenderInstanceID)})
}

// identity is the semantic fingerprint body: the normalized binding plus the
// exact content digest and media type shared by both transport forms.
func (q ContextQuery) identity(hash, media string) []byte {
	return encoded(map[string]any{"version_id": q.VersionID, "page_path": nullableStringOf(q.PagePath), "render_instance_id": nullableStringOf(q.RenderInstanceID), "bytes_sha256": hash, "media_type": media})
}

func nullableStringOf(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func (q ContextQuery) canonicalTarget() string {
	// The fingerprint target carries the semantic binding only: the transport
	// input selector and upload handle are excluded so identical bytes,
	// binding and logical media replay across direct/upload/alternate-handle
	// forms under the same key, while changed bytes or media intent conflict.
	values := url.Values{"version_id": {q.VersionID}}
	if q.PagePath != nil {
		values.Set("page_path", *q.PagePath)
	}
	if q.RenderInstanceID != nil {
		values.Set("render_instance_id", *q.RenderInstanceID)
	}
	return "/api/v1/contexts?" + values.Encode()
}

// sameBinding compares the normalized request tuple with a stored upload
// target: null pairs with an explicit JSON null and never with an omitted or
// different value; no implicit rebinding is permitted.
func (q ContextQuery) sameBinding(raw []byte) bool {
	var stored struct {
		VersionID        string  `json:"version_id"`
		PagePath         *string `json:"page_path"`
		RenderInstanceID *string `json:"render_instance_id"`
	}
	if json.Unmarshal(raw, &stored) != nil {
		return false
	}
	if stored.VersionID != q.VersionID {
		return false
	}
	same := func(a, b *string) bool {
		if (a == nil) != (b == nil) {
			return false
		}
		return a == nil || *a == *b
	}
	return same(stored.PagePath, q.PagePath) && same(stored.RenderInstanceID, q.RenderInstanceID)
}

// CreateContext performs the durable K consumer operation.
func (s *Service) CreateContext(ctx context.Context, token, key string, q ContextQuery, body io.Reader) (domain.Response, error) {
	actor, err := s.Auth.Authenticate(ctx, token)
	if err != nil {
		return domain.Response{}, err
	}
	if !idPattern.MatchString(key) {
		return domain.Response{}, sec.ErrInvalid
	}
	if q.Input != "direct" && q.Input != "upload" {
		return domain.Response{}, sec.ErrInvalid
	}
	if q.Input == "direct" && q.MediaType != "application/json" && q.MediaType != "text/plain" {
		return domain.Response{}, sec.ErrInvalid
	}
	prepareID, err := service.NewID()
	if err != nil {
		return domain.Response{}, err
	}
	prepare := domain.Pin{ID: prepareID, Kind: "context_prepare", OwnerID: "context-request:" + key}
	var object domain.Object
	var media string
	logical := "text/plain"
	if q.Input == "direct" {
		object, err = s.Bytes.Put(ctx, validatedContextReader(body, q.MediaType, &logical), func(tx app.WriteTx, staged domain.Object) error {
			prepare.Hash = staged.Hash
			// Unique per-call pin identity: admission never unpins another
			// attempt's bytes; exact-ID release/adoption and startup cleanup
			// own the lifetime.
			return tx.AcquirePin(prepare)
		})
		if err != nil {
			if errors.Is(err, errInvalidContextBytes) {
				return domain.Response{}, sec.ErrInvalid
			}
			return domain.Response{}, err
		}
		media = q.MediaType
		// The declared type is the family constraint (application/json must
		// actually be JSON); the fingerprint uses the logical media derived
		// from the complete bytes so both transport forms share one identity.
	} else {
		if !idPattern.MatchString(q.UploadID) {
			return domain.Response{}, sec.ErrInvalid
		}
		media, object, err = s.adoptSealedContext(ctx, token, q)
		if err != nil {
			return domain.Response{}, err
		}
		logical = media
	}
	request := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key}, Method: "POST", Target: q.canonicalTarget(), Body: q.identity(object.Hash, logical)}
	// Per-call exact-ID cleanup: idempotent after the atomic in-apply handoff,
	// and it reclaims the pin a completed-receipt replay stages before Execute.
	release := func() {
		if q.Input == "direct" {
			_ = s.Store.Write(context.Background(), func(tx app.WriteTx) error { return tx.ReleasePin(prepare.ID) })
		}
	}
	response, err := app.Execute(ctx, s.Store, s.Auth.Authority(token), request, nil, func(tx app.WriteTx, receipt domain.Receipt) (domain.Response, error) {
		writer, err := s.Auth.AuthenticateWrite(ctx, tx, token)
		if err != nil {
			return domain.Response{}, err
		}
		if writer.Scope.Kind != "owner" {
			if !containsAction(writer.Scope.Actions, "context.write") {
				return domain.Response{}, domain.ErrDenied
			}
			if err := authorizeContextAnchors(tx, writer.Scope, q); err != nil {
				return domain.Response{}, err
			}
		}
		v, err := tx.PublishedVersion(q.VersionID)
		if err != nil {
			return domain.Response{}, err
		}
		_ = v
		// A present page binding must name a known page of the exact version,
		// identically to the sealed-upload authority path (tx.Page).
		if q.PagePath != nil {
			if _, err := tx.Page(q.VersionID, *q.PagePath); err != nil {
				return domain.Response{}, err
			}
		}
		if q.Input == "upload" {
			upload, err := s.authorizedContextUpload(tx, writer, q)
			if err != nil {
				return domain.Response{}, err
			}
			stored, err := tx.Object(upload.ObjectHash)
			if err != nil {
				return domain.Response{}, err
			}
			object = stored
		}
		id, err := service.NewID()
		if err != nil {
			return domain.Response{}, err
		}
		pinID, err := service.NewID()
		if err != nil {
			return domain.Response{}, err
		}
		record := domain.ContextRecord{ID: id, PrincipalID: writer.PrincipalID, VersionID: q.VersionID, PagePath: q.PagePath, RenderInstanceID: q.RenderInstanceID, SessionID: writer.ID, MediaType: media, Object: object, CreatedAt: s.now()}
		if err := tx.PutContext(record); err != nil {
			return domain.Response{}, err
		}
		if err := tx.AcquirePin(domain.Pin{ID: pinID, Kind: "context", OwnerID: id, Hash: object.Hash}); err != nil {
			return domain.Response{}, err
		}
		if q.Input == "direct" {
			// Atomic ownership handoff: the durable context pin adopts the
			// staged object in the same transaction that releases this exact
			// preparation pin (broad owner release is avoided so a concurrent
			// same-key preparation keeps its own pin).
			if err := tx.ReleasePin(prepare.ID); err != nil {
				return domain.Response{}, err
			}
		}
		body := encoded(map[string]string{"context_ref": id, "bytes_sha256": object.Hash, "media_type": media, "created_at": record.CreatedAt})
		return domain.Response{Status: 201, Body: body}, nil
	})
	if err != nil {
		release()
		return domain.Response{}, err
	}
	release()
	return response, nil
}

func containsAction(actions []string, action string) bool {
	for _, item := range actions {
		if item == action {
			return true
		}
	}
	return false
}

func authorizeContextAnchors(tx app.WriteTx, scope domain.CredentialScope, q ContextQuery) error {
	allowed := false
	for _, id := range scope.VersionIDs {
		if id == q.VersionID {
			allowed = true
			break
		}
	}
	if !allowed {
		return domain.ErrNotFound
	}
	v, err := tx.PublishedVersion(q.VersionID)
	if err != nil {
		return err
	}
	document, err := tx.Document(v.DocumentID)
	if err != nil {
		return err
	}
	for _, id := range scope.ProjectIDs {
		if id == document.ProjectID {
			return nil
		}
	}
	return domain.ErrNotFound
}

// adoptSealedContext validates a sealed purpose=context upload, its exact
// binding tuple and derives the media type by one complete bounded-memory
// validation pass over the stored bytes: application/json only when the whole
// object is exactly one valid JSON value, else text/plain.
func (s *Service) adoptSealedContext(ctx context.Context, token string, q ContextQuery) (string, domain.Object, error) {
	var upload domain.Upload
	var object domain.Object
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		session, err := s.Auth.AuthenticateWrite(ctx, tx, token)
		if err != nil {
			return err
		}
		upload, err = s.authorizedContextUpload(tx, session, q)
		if err != nil {
			return err
		}
		if !q.sameBinding(upload.Target) {
			return sec.ErrInvalid
		}
		stored, err := tx.Object(upload.ObjectHash)
		if err != nil {
			return err
		}
		object = stored
		return nil
	})
	if err != nil {
		return "", domain.Object{}, err
	}
	media, err := s.detectContextMediaOver(ctx, object, q.UploadID)
	if err != nil {
		return "", domain.Object{}, err
	}
	return media, object, nil
}

func (s *Service) authorizedContextUpload(tx app.WriteTx, writer domain.Session, q ContextQuery) (domain.Upload, error) {
	upload, err := tx.Upload(q.UploadID)
	if err != nil {
		return domain.Upload{}, domain.ErrNotFound
	}
	if upload.Purpose != "context" || upload.State != "sealed" || upload.ObjectHash == "" {
		return domain.Upload{}, sec.ErrInvalid
	}
	if writer.Scope.Kind != "owner" && writer.PrincipalID != upload.PrincipalID {
		return domain.Upload{}, domain.ErrNotFound
	}
	if !q.sameBinding(upload.Target) {
		return domain.Upload{}, sec.ErrInvalid
	}
	return upload, nil
}

func (s *Service) detectContextMediaOver(ctx context.Context, object domain.Object, uploadID string) (string, error) {
	media := "text/plain"
	err := s.Bytes.Read(ctx, object, domain.Pin{Kind: "read", OwnerID: "context-sniff:" + uploadID}, func(handle io.ReadSeeker) error {
		detected, err := detectContextMedia(handle)
		if err != nil {
			return err
		}
		media = detected
		return nil
	})
	if err != nil {
		return "", err
	}
	return media, nil
}

// authorizeContextTx is the single shared CURRENT-authorization closure for
// context reads and per-write stream guards.
func (s *Service) authorizeContextTx(ctx context.Context, tx app.WriteTx, token, id string) (domain.ContextRecord, error) {
	writer, err := s.Auth.AuthenticateWrite(ctx, tx, token)
	if err != nil {
		return domain.ContextRecord{}, err
	}
	return s.authorizeContextRecord(tx, writer, id)
}

// authorizeContextRecord applies the record-level CURRENT authorization for
// an already authenticated writer: scoped principals must own the record and
// still hold its version/project anchors, and the version/document lifecycle
// stays visible to every reader form.
func (s *Service) authorizeContextRecord(tx app.WriteTx, writer domain.Session, id string) (domain.ContextRecord, error) {
	record, err := tx.Context(id)
	if err != nil {
		return domain.ContextRecord{}, err
	}
	if writer.Scope.Kind != "owner" {
		if writer.PrincipalID != record.PrincipalID {
			return domain.ContextRecord{}, domain.ErrNotFound
		}
		if err := authorizeContextAnchors(tx, writer.Scope, ContextQuery{VersionID: record.VersionID}); err != nil {
			return domain.ContextRecord{}, err
		}
	}
	v, err := tx.PublishedVersion(record.VersionID)
	if err != nil {
		return domain.ContextRecord{}, err
	}
	document, err := tx.Document(v.DocumentID)
	if err != nil {
		return domain.ContextRecord{}, err
	}
	if document.Lifecycle == "trash" || document.Lifecycle == "purged" {
		return domain.ContextRecord{}, domain.ErrNotFound
	}
	return record, nil
}

// VerifyContextFresh proves fresh use of a context record for the currently
// active page, in ONE transaction and without opening the staged object:
// the caller holds the record's CURRENT authorization, is the exact
// originating session, and the persisted principal tuple plus object digest
// match the expected values supplied by the trusted host. A mismatch fails
// as a binding precondition; it never restricts the ordinary authorized
// recovery read, which stays available to any current-authorized reader
// under the original persisted binding.
func (s *Service) VerifyContextFresh(ctx context.Context, token, id string, expected ContextQuery, etag string) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error {
		writer, err := s.Auth.AuthenticateWrite(ctx, tx, token)
		if err != nil {
			return err
		}
		record, err := s.authorizeContextRecord(tx, writer, id)
		if err != nil {
			return err
		}
		if writer.ID != record.SessionID || writer.PrincipalID != record.PrincipalID ||
			record.VersionID != expected.VersionID ||
			!sameOptionalString(record.PagePath, expected.PagePath) ||
			!sameOptionalString(record.RenderInstanceID, expected.RenderInstanceID) ||
			"\"sha256-"+record.Object.Hash+"\"" != etag {
			return domain.ErrPrecondition
		}
		return nil
	})
}

// sameOptionalString compares an explicitly nullable tuple member: null
// pairs only with null and a present value only with its exact string.
func sameOptionalString(stored, expected *string) bool {
	if (stored == nil) != (expected == nil) {
		return false
	}
	return stored == nil || *stored == *expected
}

// GuardContext rechecks the record's CURRENT authorization (session, record
// ownership or scoped anchors, version visibility and document lifecycle) for
// per-write use during protected streaming.
func (s *Service) GuardContext(ctx context.Context, token, id string) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error {
		_, err := s.authorizeContextTx(ctx, tx, token, id)
		return err
	})
}

// ReadContext streams the exact sealed bytes under the record's CURRENT
// authorization: the owner, or the scoped principal that both owns the record
// and still holds version/project anchors; version and document lifecycle
// are rechecked before streaming. Delivery-time rechecks belong to the HTTP
// adapter's guarded writer.
func (s *Service) ReadContext(ctx context.Context, token, id string, consume func(domain.ContextRecord, io.ReadSeeker) error) error {
	var record domain.ContextRecord
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		var err error
		record, err = s.authorizeContextTx(ctx, tx, token, id)
		return err
	})
	if err != nil {
		return err
	}
	return s.Bytes.Read(ctx, record.Object, domain.Pin{Kind: "read", OwnerID: id}, func(handle io.ReadSeeker) error {
		return consume(record, handle)
	})
}
