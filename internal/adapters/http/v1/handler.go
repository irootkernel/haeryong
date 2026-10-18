// Package v1 adapts the current query and exact-byte APIs. Authored files are
// attachments, never executable documents at the trusted management origin.
package v1

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Handler struct{ Core *query.Service }

func New(core *query.Service) *Handler { return &Handler{Core: core} }
func route(r *http.Request) []string {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSuffix(r.URL.EscapedPath(), "/"), "/api/v1/"), "/")
	for i, part := range parts {
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return nil
		}
		parts[i] = decoded
	}
	return parts
}
func (h *Handler) Handles(r *http.Request) bool {
	if r.Method != "GET" && r.Method != "HEAD" {
		return false
	}
	p := route(r)
	if len(p) == 0 {
		return false
	}
	if len(p) == 1 {
		return p[0] == "capabilities" || p[0] == "projects" || p[0] == "documents"
	}
	if p[0] == "documents" {
		return len(p) == 2 || len(p) == 3 && p[2] == "versions"
	}
	if p[0] == "versions" {
		if len(p) == 2 {
			return true
		}
		if len(p) == 3 {
			return p[2] == "files" || p[2] == "file" || p[2] == "metadata" || p[2] == "render-bootstrap" || p[2] == "package"
		}
		return len(p) == 4 && p[2] == "source-evidence"
	}
	return false
}
func bearer(r *http.Request) (string, error) {
	values := r.Header.Values("Authorization")
	if len(values) == 0 {
		return "", sec.ErrRequired
	}
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || strings.TrimSpace(strings.TrimPrefix(values[0], "Bearer ")) != strings.TrimPrefix(values[0], "Bearer ") {
		return "", sec.ErrInvalid
	}
	return strings.TrimPrefix(values[0], "Bearer "), nil
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	token, err := bearer(r)
	if err != nil {
		failure(w, r, err)
		return
	}
	if _, err = h.Core.Auth.Authenticate(r.Context(), token); err != nil {
		failure(w, r, err)
		return
	}
	if !h.Handles(r) {
		failure(w, r, domain.ErrNotFound)
		return
	}
	p := route(r)
	allowed := []string{"cursor", "limit"}
	if p[0] == "documents" && len(p) == 1 {
		allowed = append(allowed, "project_id", "lifecycle")
	}
	if p[0] == "versions" && len(p) > 2 {
		switch p[2] {
		case "files":
			allowed = append(allowed, "directory", "kind")
		case "file":
			allowed = []string{"path", "resolve_link"}
		case "metadata":
			allowed = []string{"kind"}
		case "render-bootstrap":
			allowed = []string{"page_path"}
		default:
			allowed = nil
		}
	}
	values, err := parameters(r, allowed)
	if err != nil {
		failure(w, r, err)
		return
	}
	o := query.Options{Cursor: values.Get("cursor"), Project: values.Get("project_id"), Lifecycle: values.Get("lifecycle"), Directory: values.Get("directory"), Kind: values.Get("kind")}
	if values.Has("limit") {
		o.Limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil || o.Limit < 1 {
			failure(w, r, &query.Error{Reason: "invalid_page_size"})
			return
		}
	}
	var data any
	etag := ""
	version := ""
	switch {
	case len(p) == 1 && p[0] == "capabilities":
		data, err = h.Core.Capabilities(r.Context(), token)
	case len(p) == 1 && p[0] == "projects":
		data, err = h.Core.Projects(r.Context(), token, o)
	case len(p) == 1 && p[0] == "documents":
		data, err = h.Core.Documents(r.Context(), token, o)
	case p[0] == "documents" && len(p) == 2:
		var item query.Document
		item, err = h.Core.Document(r.Context(), token, p[1])
		data = item
		etag = item.ETag
	case p[0] == "documents" && len(p) == 3:
		data, err = h.Core.Versions(r.Context(), token, p[1], o)
	case p[0] == "versions" && len(p) == 2:
		var item query.Version
		item, err = h.Core.Version(r.Context(), token, p[1])
		data = item
		etag = item.ETag
		version = p[1]
	case p[0] == "versions" && len(p) == 3 && p[2] == "files":
		data, err = h.Core.Files(r.Context(), token, p[1], o)
		version = p[1]
	case p[0] == "versions" && len(p) == 3 && p[2] == "render-bootstrap":
		data, err = h.Core.RenderBootstrap(r.Context(), token, p[1], values.Get("page_path"))
		version = p[1]
	case p[0] == "versions" && len(p) == 4:
		data, err = h.Core.Evidence(r.Context(), token, p[1], p[3])
		version = p[1]
	case p[0] == "versions" && len(p) == 3 && (p[2] == "file" || p[2] == "metadata"):
		h.deliver(w, r, token, p[1], p[2], values)
		return
	case p[0] == "versions" && len(p) == 3 && p[2] == "package":
		h.deliverPackage(w, r, token, p[1])
		return
	default:
		err = domain.ErrNotFound
	}
	if err == nil && version != "" {
		err = h.Core.Guard(r.Context(), token, version)
	}
	if err != nil {
		failure(w, r, err)
		return
	}
	if etag != "" {
		w.Header().Set("ETag", etag)
		if !matchIfMatch(r.Header.Get("If-Match"), etag) {
			failure(w, r, domain.ErrPrecondition)
			return
		}
		if matchNone(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	respond(w, r, 200, data)
}

func parameters(r *http.Request, allowed []string) (url.Values, error) {
	v, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, &query.Error{Reason: "invalid_query"}
	}
	for key, values := range v {
		found := false
		for _, a := range allowed {
			if key == a {
				found = true
				break
			}
		}
		if !found || len(values) != 1 || values[0] == "" {
			return nil, &query.Error{Reason: "unknown_duplicate_or_empty_query"}
		}
	}
	return v, nil
}
func matchNone(header, etag string) bool {
	for _, v := range strings.Split(header, ",") {
		v = strings.TrimSpace(v)
		if v == "*" || strings.TrimPrefix(v, "W/") == etag {
			return true
		}
	}
	return false
}
func matchIfMatch(header, etag string) bool {
	if header == "" {
		return true
	}
	for _, v := range strings.Split(header, ",") {
		v = strings.TrimSpace(v)
		if v == "*" || v == etag {
			return true
		}
	}
	return false
}

func (h *Handler) deliver(w http.ResponseWriter, r *http.Request, token, id, kind string, v url.Values) {
	started := false
	consume := func(info query.ResolvedFile, handle io.ReadSeeker) error {
		if err := h.Core.Guard(r.Context(), token, id); err != nil {
			return err
		}
		if handle == nil {
			if r.Header.Get("Range") != "" {
				failureCode(w, r, 416, "RANGE_NOT_SATISFIABLE", nil)
				started = true
				return nil
			}
			w.Header().Set("ETag", info.ETag)
			if !matchIfMatch(r.Header.Get("If-Match"), info.ETag) {
				return domain.ErrPrecondition
			}
			if matchNone(r.Header.Get("If-None-Match"), info.ETag) {
				w.WriteHeader(304)
			} else {
				respond(w, r, 200, info)
			}
			started = true
			return nil
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(info.Path)}))
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		w.Header().Set("ETag", info.ETag)
		w.Header().Set("Haeryong-Requested-Path", url.QueryEscape(info.RequestedPath))
		w.Header().Set("Haeryong-Resolved-Path", url.QueryEscape(info.ResolvedPath))
		if info.Hash != nil {
			raw, err := hex.DecodeString(*info.Hash)
			if err != nil {
				return err
			}
			w.Header().Set("Repr-Digest", "sha-256=:"+base64.StdEncoding.EncodeToString(raw)+":")
		}
		// ServeContent treats an empty file's unsatisfiable Range as 200 for
		// compatibility. Our exact-byte contract requires explicit 416, after
		// conditionals and a mismatching If-Range have been accounted for.
		if info.Object.Size == 0 && r.Header.Get("Range") != "" && (r.Header.Get("If-Range") == "" || r.Header.Get("If-Range") == info.ETag) && matchIfMatch(r.Header.Get("If-Match"), info.ETag) && !matchNone(r.Header.Get("If-None-Match"), info.ETag) {
			w.Header().Set("Content-Range", "bytes */0")
			failureCode(w, r, 416, "RANGE_NOT_SATISFIABLE", nil)
			started = true
			return nil
		}
		writer := &deliveryWriter{ResponseWriter: w, head: r.Method == "HEAD", check: func() error { return h.Core.Guard(r.Context(), token, id) }}
		http.ServeContent(writer, r, info.Path, time.Time{}, handle)
		started = writer.started
		if writer.err != nil {
			return writer.err
		}
		if writer.status >= 400 {
			code := "INVALID_REQUEST"
			if writer.status == 416 {
				code = "RANGE_NOT_SATISFIABLE"
			} else if writer.status == 412 {
				code = "PRECONDITION_FAILED"
			}
			w.Header().Del("Content-Length")
			failureCode(w, r, writer.status, code, nil)
			started = true
		}
		return nil
	}
	var err error
	if kind == "metadata" {
		err = h.Core.ReadMetadata(r.Context(), token, id, v.Get("kind"), consume)
	} else {
		resolve := false
		if v.Has("resolve_link") {
			if v.Get("resolve_link") != "true" && v.Get("resolve_link") != "false" {
				failure(w, r, &query.Error{Reason: "invalid_resolve_link"})
				return
			}
			resolve = v.Get("resolve_link") == "true"
		}
		err = h.Core.ReadFile(r.Context(), token, id, v.Get("path"), resolve, consume)
	}
	if err != nil && !started {
		w.Header().Del("Content-Length")
		w.Header().Del("Content-Disposition")
		failure(w, r, err)
	}
}

// deliverPackage streams the faithful original-directory archive. The archive
// is a generated representation, so the response carries no Content-Length
// and no representation digest: payload identity stays with the per-entry
// bytes_sha256 inventory, never with these wire bytes. A failure before the
// first byte returns the ordinary JSON registry error; a failure after the
// body started aborts the connection so a partial archive can never end in
// the clean EOF a client could mistake for completion.
func (h *Handler) deliverPackage(w http.ResponseWriter, r *http.Request, token, id string) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "haeryong-" + id + ".zip"}))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	sink := &packageResponseWriter{ResponseWriter: w, check: func() error { return h.Core.GuardPackage(r.Context(), token, id) }}
	if err := h.Core.Package(r.Context(), token, id, sink, sink.check); err != nil {
		if !sink.started {
			w.Header().Del("Content-Length")
			failure(w, r, err)
			return
		}
		panic(http.ErrAbortHandler)
	}
}

// packageResponseWriter renews export authority and the socket write deadline
// around every body write, mirroring the guarded file-delivery writer.
type packageResponseWriter struct {
	http.ResponseWriter
	check   func() error
	started bool
}

func (w *packageResponseWriter) Write(p []byte) (int, error) {
	if err := w.check(); err != nil {
		return 0, err
	}
	if err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return 0, err
	}
	if !w.started {
		// The underlying Write commits the headers even when it returns a
		// partial write or an error, so the response is marked committed
		// BEFORE that first Write: any first-write or first-flush failure
		// then aborts the connection instead of appending a canonical JSON
		// error to an already started body.
		w.started = true
	}
	n, err := w.ResponseWriter.Write(p)
	if err == nil {
		// Serialize the headers while the handler is still running so the
		// archive is a streamed response: the server must not retroactively
		// compute a Content-Length for a small, fully buffered archive.
		if flushErr := http.NewResponseController(w.ResponseWriter).Flush(); flushErr != nil && !errors.Is(flushErr, http.ErrNotSupported) {
			return n, flushErr
		}
	}
	return n, err
}

// ServeContent decides conditionals/ranges while this wrapper postpones error
// bodies for the canonical JSON registry and renews each socket-write deadline.
type deliveryWriter struct {
	http.ResponseWriter
	check   func() error
	status  int
	started bool
	head    bool
	err     error
}

func (w *deliveryWriter) WriteHeader(status int) {
	w.status = status
	if status >= 400 {
		return
	}
	if status == 200 && !w.head {
		w.Header().Set("Content-Digest", w.Header().Get("Repr-Digest"))
	} else {
		w.Header().Del("Content-Digest")
	}
	if err := w.check(); err != nil {
		w.err = err
		return
	}
	if err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		w.err = err
		return
	}
	w.started = true
	w.ResponseWriter.WriteHeader(status)
}
func (w *deliveryWriter) Write(p []byte) (int, error) {
	if w.status >= 400 {
		return len(p), nil
	}
	if w.err != nil {
		return 0, w.err
	}
	if err := w.check(); err != nil {
		w.err = err
		return 0, err
	}
	if err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		w.err = err
		return 0, err
	}
	if !w.started {
		w.WriteHeader(200)
	}
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.ResponseWriter.Write(p)
	w.err = err
	return n, err
}

func respond(w http.ResponseWriter, r *http.Request, status int, data any) {
	id, _ := service.NewID()
	b, _ := json.Marshal(map[string]any{"data": data, "request_id": id})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(status)
	if r.Method != "HEAD" {
		_, _ = w.Write(b)
	}
}
func failure(w http.ResponseWriter, r *http.Request, err error) {
	status, code := 503, "SERVICE_RECOVERING"
	details := map[string]any{}
	var invalid *query.Error
	var missing *query.Missing
	switch {
	case errors.As(err, &invalid):
		status, code = 400, "INVALID_REQUEST"
		details["reason"] = invalid.Reason
	case errors.Is(err, sec.ErrRequired):
		status, code = 401, "AUTH_REQUIRED"
	case errors.Is(err, sec.ErrRevoked):
		status, code = 401, "SESSION_REVOKED"
	case errors.Is(err, sec.ErrExpired):
		status, code = 401, "SESSION_EXPIRED"
	case errors.Is(err, sec.ErrInvalid), errors.Is(err, domain.ErrInvalidTarget):
		status, code = 400, "INVALID_REQUEST"
	case errors.Is(err, domain.ErrDenied):
		status, code = 403, "FORBIDDEN"
	case errors.Is(err, domain.ErrNotFound):
		status, code = 404, "NOT_FOUND"
		if errors.As(err, &missing) {
			details["reason"] = missing.Reason
		}
	case errors.Is(err, domain.ErrPrecondition):
		status, code = 412, "PRECONDITION_FAILED"
	case errors.Is(err, domain.ErrContentIntegrity):
		status, code = 503, "CONTENT_INTEGRITY_ERROR"
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT), errors.Is(err, syscall.ENOMEM):
		status, code = 503, "RESOURCE_EXHAUSTED"
	case errors.Is(err, os.ErrPermission), errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.EIO), errors.Is(err, syscall.EROFS):
		status, code = 503, "FILE_ACCESS_ERROR"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status, code = 503, "TRANSFER_INTERRUPTED"
	}
	failureCode(w, r, status, code, details)
}
func failureCode(w http.ResponseWriter, r *http.Request, status int, code string, details map[string]any) {
	w.Header().Del("Content-Disposition")
	w.Header().Del("Content-Digest")
	w.Header().Del("Repr-Digest")
	if details == nil {
		details = map[string]any{}
	}
	id, _ := service.NewID()
	b, _ := json.Marshal(map[string]any{"error": map[string]any{"code": code, "message": code, "retryable": status == 503, "details": details}, "request_id": id})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(status)
	if r.Method != "HEAD" {
		_, _ = w.Write(b)
	}
}
