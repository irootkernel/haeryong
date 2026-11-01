// Package imports exposes the authenticated upload and import transport. The
// surrounding auth adapter owns Host/Origin policy; the core rechecks authority
// inside every durable mutation and before serving private result data.
package imports

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	generation "haeryong.local/haeryong/internal/application/generation"
	importer "haeryong.local/haeryong/internal/application/import"
	appstorage "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"haeryong.local/haeryong/internal/security/session"
)

type handler struct {
	core       *importer.Service
	generation *generation.Service
}

func New(core *importer.Service) http.Handler { return &handler{core: core} }

// Handles lets the existing auth router retain its frozen unimplemented-feature
// and unknown-route responses for APIs outside this adapter's ownership.
func (h *handler) Handles(r *http.Request) bool {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	return h.generation != nil && (path == "generations" || path == "drafts" || strings.HasPrefix(path, "drafts/")) || path == "uploads" || strings.HasPrefix(path, "uploads/") || path == "imports" || path == "packages/validate" || path == "contexts" || strings.HasPrefix(path, "contexts/")
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if _, err := h.core.Auth.Authenticate(r.Context(), token); err != nil {
		fail(w, err)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	parts := strings.Split(path, "/")
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		fail(w, session.ErrInvalid)
		return
	}
	for _, values := range query {
		if len(values) != 1 {
			fail(w, session.ErrInvalid)
			return
		}
	}
	if h.generationRoutes(w, r, token, path, parts, query) {
		return
	}
	if r.Method == http.MethodPost && (path == "uploads" || path == "imports" || path == "packages/validate" || (len(parts) == 3 && parts[0] == "uploads" && (parts[2] == "complete" || parts[2] == "abandon"))) {
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			fail(w, session.ErrInvalid)
			return
		}
	}
	key := ""
	keyed := path == "uploads" || path == "imports" || path == "packages/validate" || path == "contexts" || (len(parts) == 3 && parts[0] == "uploads" && (parts[2] == "complete" || parts[2] == "abandon"))
	if keyed && r.Method == http.MethodPost {
		key, err = operationKey(r, path == "packages/validate")
		if err != nil {
			fail(w, err)
			return
		}
	}
	private := func(value any, err error) {
		if err != nil {
			fail(w, err)
			return
		}
		err = h.core.Auth.Guard(r.Context(), token, func() error { respond(w, 200, value); return nil })
		if err != nil {
			fail(w, err)
		}
	}
	switch {
	case path == "uploads" && r.Method == http.MethodPost:
		var in importer.CreateRequest
		if err := closedJSON(r.Body, &in, []string{"purpose", "target", "input_mode"}); err != nil {
			fail(w, err)
			return
		}
		var target any
		if json.Unmarshal(in.Target, &target) != nil {
			fail(w, session.ErrInvalid)
			return
		}
		response, err := h.core.Create(r.Context(), token, key, in)
		durable(w, response, err)
	case path == "uploads" && r.Method == http.MethodGet:
		if !only(query, "cursor", "state", "unadopted") {
			fail(w, session.ErrInvalid)
			return
		}
		unadopted := false
		if value := query.Get("unadopted"); value != "" {
			if value != "true" && value != "false" {
				fail(w, session.ErrInvalid)
				return
			}
			unadopted = value == "true"
		}
		value, err := h.core.Uploads(r.Context(), token, importer.UploadQuery{Cursor: query.Get("cursor"), State: query.Get("state"), Unadopted: unadopted})
		private(value, err)
	case len(parts) == 2 && parts[0] == "uploads" && r.Method == http.MethodGet:
		if !only(query) {
			fail(w, session.ErrInvalid)
			return
		}
		value, err := h.core.Upload(r.Context(), token, parts[1])
		if err == nil {
			w.Header().Set("ETag", value.ETag)
		}
		private(value, err)
	case len(parts) == 3 && parts[0] == "uploads" && parts[2] == "entries" && r.Method == http.MethodPut:
		if !only(query, "path", "kind") || query.Get("path") == "" || query.Get("kind") == "" {
			fail(w, session.ErrInvalid)
			return
		}
		chunk := importer.Chunk{Path: query.Get("path"), Kind: query.Get("kind")}
		if chunk.Kind == "file" {
			media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || media != "application/octet-stream" {
				fail(w, session.ErrInvalid)
				return
			}
			chunk.Offset, err = decimalHeader(r, "Haeryong-Upload-Offset", true)
			if err != nil {
				fail(w, err)
				return
			}
			value, err := singleHeader(r, "Haeryong-Upload-Chunk-SHA256", true)
			if err != nil || !sha.MatchString(value) {
				fail(w, session.ErrInvalid)
				return
			}
			chunk.Hash = value
			if len(r.Header.Values("Haeryong-Upload-Length")) > 0 {
				length, err := decimalHeader(r, "Haeryong-Upload-Length", false)
				if err != nil {
					fail(w, err)
					return
				}
				chunk.Length = &length
			}
			if len(r.Header.Values("Haeryong-Upload-Final")) > 0 {
				value, err := singleHeader(r, "Haeryong-Upload-Final", false)
				if err != nil || value != "true" {
					fail(w, session.ErrInvalid)
					return
				}
				chunk.Final = true
			}
		} else {
			media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || media != "application/json" {
				fail(w, session.ErrInvalid)
				return
			}
			if chunk.Kind == "directory" {
				var in struct{}
				if err := closedJSON(r.Body, &in, nil); err != nil {
					fail(w, err)
					return
				}
			} else if chunk.Kind == "symlink" {
				var in struct {
					Target string `json:"link_target"`
				}
				if err := closedJSON(r.Body, &in, []string{"link_target"}); err != nil {
					fail(w, err)
					return
				}
				chunk.LinkTarget = in.Target
			} else {
				fail(w, session.ErrInvalid)
				return
			}
		}
		value, err := h.core.Put(r.Context(), token, parts[1], chunk, r.Body)
		private(value, err)
	case len(parts) == 3 && parts[0] == "uploads" && parts[2] == "complete" && r.Method == http.MethodPost:
		var in struct {
			Checkpoint json.RawMessage `json:"checkpoint"`
		}
		if err := closedJSON(r.Body, &in, []string{"checkpoint"}); err != nil {
			fail(w, err)
			return
		}
		cp, err := appstorage.ParseCheckpoint(in.Checkpoint)
		if err != nil {
			fail(w, session.ErrInvalid)
			return
		}
		response, err := h.core.Complete(r.Context(), token, parts[1], key, cp)
		durable(w, response, err)
	case len(parts) == 3 && parts[0] == "uploads" && parts[2] == "abandon" && r.Method == http.MethodPost:
		var in struct{}
		if err := closedJSON(r.Body, &in, nil); err != nil {
			fail(w, err)
			return
		}
		response, err := h.core.Abandon(r.Context(), token, parts[1], key)
		durable(w, response, err)
	case len(parts) == 3 && parts[0] == "uploads" && r.Method == http.MethodGet:
		if !only(query, "cursor", "path") || (parts[2] != "ranges" && query.Has("path")) {
			fail(w, session.ErrInvalid)
			return
		}
		switch parts[2] {
		case "entries":
			value, err := h.core.Entries(r.Context(), token, parts[1], query.Get("cursor"))
			private(value, err)
		case "ranges":
			value, err := h.core.Ranges(r.Context(), token, parts[1], query.Get("cursor"), query.Get("path"))
			items := make([]map[string]any, 0, len(value.Items))
			for _, item := range value.Items {
				items = append(items, map[string]any{"path": item.Path, "offset": strconv.FormatInt(item.Offset, 10), "byte_size": strconv.FormatInt(item.Size, 10), "bytes_sha256": item.Hash})
			}
			private(importer.Page[map[string]any]{Items: items, NextCursor: value.NextCursor}, err)
		case "pins":
			value, err := h.core.Pins(r.Context(), token, parts[1], query.Get("cursor"))
			items := make([]map[string]any, 0, len(value.Items))
			for _, item := range value.Items {
				items = append(items, map[string]any{"pin_id": item.ID, "kind": item.Kind, "owner_id": item.OwnerID, "bytes_sha256": item.Hash})
			}
			private(importer.Page[map[string]any]{Items: items, NextCursor: value.NextCursor}, err)
		default:
			fail(w, domain.ErrNotFound)
		}
	case (path == "imports" || path == "packages/validate") && r.Method == http.MethodPost:
		if !only(query) {
			fail(w, session.ErrInvalid)
			return
		}
		in, cleanup, err := importInput(r.Body, path == "packages/validate")
		if err != nil {
			fail(w, err)
			return
		}
		defer cleanup()
		var response domain.Response
		if path == "imports" {
			response, err = h.core.Import(r.Context(), token, key, in)
		} else {
			response, err = h.core.Validate(r.Context(), token, key, in)
		}
		durable(w, response, err)
	case path == "contexts" && r.Method == http.MethodPost:
		if !only(query, "version_id", "page_path", "render_instance_id", "input") {
			fail(w, session.ErrInvalid)
			return
		}
		contextQuery, err := importer.NormalizeContextQuery(query)
		if err != nil {
			fail(w, err)
			return
		}
		if contextQuery.Input == "upload" {
			// The sealed-handle selector is a closed body field, never a query parameter.
			var in struct {
				UploadID string `json:"upload_id"`
			}
			if err := closedJSON(r.Body, &in, []string{"upload_id"}); err != nil {
				fail(w, err)
				return
			}
			contextQuery.UploadID = in.UploadID
		} else {
			media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || (media != "application/json" && media != "text/plain") {
				fail(w, session.ErrInvalid)
				return
			}
			contextQuery.MediaType = media
		}
		response, err := h.core.CreateContext(r.Context(), token, key, contextQuery, r.Body)
		durable(w, response, err)
	case len(parts) == 2 && parts[0] == "contexts" && r.Method == http.MethodGet:
		if !only(query) {
			fail(w, session.ErrInvalid)
			return
		}
		// The fresh-proof mode is selected only by the binding header's
		// presence; without it the ordinary authorized recovery GET keeps
		// its exact current behavior, including ignoring standalone
		// conditional or Range headers.
		if binding := r.Header.Values("Haeryong-Context-Binding"); len(binding) > 0 {
			if err := h.proveContextFresh(w, r, token, parts[1], binding); err != nil {
				fail(w, err)
			}
			return
		}
		err := h.core.ReadContext(r.Context(), token, parts[1], func(record domain.ContextRecord, handle io.ReadSeeker) error {
			w.Header().Set("Content-Type", record.MediaType)
			w.Header().Set("ETag", "\"sha256-"+record.Object.Hash+"\"")
			w.Header().Set("Content-Length", strconv.FormatInt(record.Object.Size, 10))
			writer := &guardedWriter{ResponseWriter: w, check: func() error { return h.core.GuardContext(r.Context(), token, parts[1]) }}
			_, err := io.Copy(writer, handle)
			if err != nil || writer.err != nil {
				if writer.started {
					// Exact bytes already began: abort the response rather
					// than append an error envelope to protected content.
					return errStreamStarted
				}
				if writer.err != nil {
					return writer.err
				}
				return err
			}
			return nil
		})
		if errors.Is(err, errStreamStarted) {
			panic(http.ErrAbortHandler)
		}
		if err != nil {
			// No protected byte was sent; present the ordinary error with
			// content headers cleared.
			w.Header().Del("Content-Length")
			w.Header().Del("ETag")
			w.Header().Del("Content-Type")
			fail(w, err)
		}
	default:
		fail(w, domain.ErrNotFound)
	}
}

// errStreamStarted marks a protected context body whose first byte already
// left the handler; the response must abort, never append an error envelope.
var errStreamStarted = errors.New("context stream already started")

var strongContextETag = regexp.MustCompile(`^"sha256-[0-9a-f]{64}"$`)

// proveContextFresh serves the closed fresh-binding proof mode selected by
// the Haeryong-Context-Binding request header. The mode requires exactly one
// strong sha256 If-None-Match naming the expected object digest and rejects
// a combined Range. Success is a body-less 304 carrying the record's ETag
// and the fresh-verification marker: current authority, the exact
// originating session, the persisted tuple and the digest are proved in one
// transaction and the staged object is never opened for this metadata
// proof. Its If-None-Match semantics (match is a 304 success, mismatch a
// 412 binding rejection) are a documented private mode, not standard
// conditional-GET handling.
func (h *handler) proveContextFresh(w http.ResponseWriter, r *http.Request, token, id string, binding []string) error {
	if len(binding) != 1 || binding[0] == "" || len(r.Header.Values("Range")) > 0 {
		return session.ErrInvalid
	}
	match := r.Header.Values("If-None-Match")
	if len(match) != 1 || !strongContextETag.MatchString(match[0]) {
		return session.ErrInvalid
	}
	expected, err := importer.ParseContextBinding(binding[0])
	if err != nil {
		return err
	}
	if err := h.core.VerifyContextFresh(r.Context(), token, id, expected, match[0]); err != nil {
		return err
	}
	w.Header().Set("ETag", match[0])
	w.Header().Set("Haeryong-Context-Verified", "fresh")
	w.WriteHeader(http.StatusNotModified)
	return nil
}

// guardedWriter renews delivery authority around every response write, mirroring the guarded file-delivery writer.
type guardedWriter struct {
	http.ResponseWriter
	check   func() error
	err     error
	started bool
}

func (w *guardedWriter) Write(p []byte) (int, error) {
	if err := w.check(); err != nil {
		w.err = err
		return 0, err
	}
	w.started = true
	if err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		w.err = err
		return 0, err
	}
	n, err := w.ResponseWriter.Write(p)
	w.err = err
	return n, err
}

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var sha = regexp.MustCompile(`^[0-9a-f]{64}$`)
var decimal = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

type missing string

func (e missing) Error() string { return "missing required header" }
func operationKey(r *http.Request, optional bool) (string, error) {
	if optional && len(r.Header.Values("Idempotency-Key")) == 0 {
		return "", nil
	}
	value, err := singleHeader(r, "Idempotency-Key", true)
	if err != nil {
		return "", err
	}
	if !uuid.MatchString(value) {
		return "", session.ErrInvalid
	}
	return value, nil
}
func singleHeader(r *http.Request, name string, required bool) (string, error) {
	values := r.Header.Values(name)
	if len(values) == 0 && required {
		if name == "Idempotency-Key" {
			return "", missing(name)
		}
		return "", session.ErrInvalid
	}
	if len(values) != 1 || values[0] == "" {
		return "", session.ErrInvalid
	}
	return values[0], nil
}
func decimalHeader(r *http.Request, name string, required bool) (int64, error) {
	value, err := singleHeader(r, name, required)
	if err != nil {
		return 0, err
	}
	if !decimal.MatchString(value) {
		return 0, session.ErrInvalid
	}
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, session.ErrInvalid
	}
	return number, nil
}
func only(q url.Values, keys ...string) bool {
	for key := range q {
		found := false
		for _, allowed := range keys {
			if key == allowed {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if status != 204 {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": value, "request_id": requestID()})
	}
}
func requestID() string {
	value, err := session.Secret()
	if err != nil {
		return "unavailable"
	}
	return value[:16]
}
func durable(w http.ResponseWriter, value domain.Response, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	for key, value := range value.Headers {
		if key == "ETag" || key == "Location" {
			w.Header().Set(key, value)
		}
	}
	respond(w, value.Status, json.RawMessage(value.Body))
}
func fail(w http.ResponseWriter, err error) {
	status, code, retryable := 503, "SERVICE_RECOVERING", true
	for _, item := range []struct {
		err    error
		status int
		code   string
	}{
		{session.ErrRequired, 401, "AUTH_REQUIRED"}, {session.ErrExpired, 401, "SESSION_EXPIRED"}, {session.ErrRevoked, 401, "SESSION_REVOKED"}, {session.ErrOrigin, 403, "ORIGIN_REJECTED"}, {session.ErrInvalid, 400, "INVALID_REQUEST"}, {session.ErrMissing, 428, "PRECONDITION_REQUIRED"}, {domain.ErrDenied, 403, "FORBIDDEN"}, {domain.ErrNotFound, 404, "NOT_FOUND"}, {domain.ErrPrecondition, 412, "PRECONDITION_FAILED"}, {domain.ErrConflict, 409, "OPERATION_CONFLICT"}, {domain.ErrTransition, 409, "INVALID_TRANSITION"}, {domain.ErrReconciliation, 409, "OPERATION_RECONCILIATION_REQUIRED"}, {importer.ErrInputChanged, 409, "INPUT_CHANGED"}, {importer.ErrHashMismatch, 422, "HASH_MISMATCH"}, {importer.ErrPackageInvalid, 422, "PACKAGE_INVALID"}, {importer.ErrVersionConflict, 409, "VERSION_CONFLICT"}, {importer.ErrImmutableConflict, 409, "IMMUTABLE_VERSION_CONFLICT"},
		{syscall.ENOSPC, 503, "RESOURCE_EXHAUSTED"}, {syscall.EDQUOT, 503, "RESOURCE_EXHAUSTED"}, {syscall.ENOMEM, 503, "RESOURCE_EXHAUSTED"},
		{domain.ErrInvalidTarget, 400, "INVALID_REQUEST"},
		{os.ErrPermission, 503, "FILE_ACCESS_ERROR"}, {syscall.EIO, 503, "FILE_ACCESS_ERROR"}, {syscall.EROFS, 503, "FILE_ACCESS_ERROR"}, {syscall.EXDEV, 503, "FILE_ACCESS_ERROR"},
		{io.ErrUnexpectedEOF, 503, "TRANSFER_INTERRUPTED"}, {context.Canceled, 503, "TRANSFER_INTERRUPTED"}, {context.DeadlineExceeded, 503, "TRANSFER_INTERRUPTED"},
	} {
		if errors.Is(err, item.err) {
			status, code, retryable = item.status, item.code, false
			break
		}
	}
	details := map[string]any{}
	var metadata *importer.MetadataError
	if errors.As(err, &metadata) {
		details["field"] = metadata.Pointer
	}
	var required missing
	if errors.As(err, &required) {
		status, code, retryable = 428, "PRECONDITION_REQUIRED", false
		details["missing"] = string(required)
	}
	if status == 503 {
		retryable = true
	}
	if retryable {
		w.Header().Set("Retry-After", "2")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": http.StatusText(status), "retryable": retryable, "details": details}, "request_id": requestID()})
}
