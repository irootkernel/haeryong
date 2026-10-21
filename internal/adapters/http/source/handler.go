// Package source exposes the authenticated project-registration and
// imported-evidence verification transport. The surrounding auth adapter owns
// Host/Origin policy; the core rechecks authority inside every durable
// mutation and before serving private data.
package source

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
	"strings"
	"syscall"

	core "haeryong.local/haeryong/internal/application/source"
	"haeryong.local/haeryong/internal/domain"
	"haeryong.local/haeryong/internal/security/session"
)

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type handler struct {
	core *core.Service
}

func New(core *core.Service) http.Handler { return &handler{core: core} }

// route splits the escaped request path into once-decoded segments below
// /api/v1 (C2): the query form is [versions, {id}, source-evidence-
// verifications] (3 segments) and the catalog path form is [versions, {id},
// source-evidence, {evidence_id}, verifications] (5 segments).
func route(r *http.Request) []string {
	trimmed := strings.Trim(strings.TrimPrefix(r.URL.EscapedPath(), "/api/v1/"), "/")
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, "/")
	for i, part := range parts {
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return nil
		}
		parts[i] = decoded
	}
	return parts
}

// Handles lets the existing auth router retain its frozen unimplemented-
// feature and unknown-route responses for APIs outside this adapter.
func (*handler) Handles(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	p := route(r)
	if p == nil {
		return false
	}
	if len(p) == 1 && p[0] == "projects" {
		return true
	}
	if len(p) == 3 && p[0] == "versions" && p[2] == "source-evidence-verifications" {
		return true
	}
	if len(p) == 3 && p[0] == "versions" && p[2] == "source-observations" {
		return true
	}
	return len(p) == 5 && p[0] == "versions" && p[2] == "source-evidence" && p[4] == "verifications"
}

type missing string

func (e missing) Error() string { return "missing required header" }

func operationKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) == 0 {
		return "", missing("Idempotency-Key")
	}
	if len(values) != 1 || !uuid.MatchString(values[0]) {
		return "", session.ErrInvalid
	}
	return values[0], nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if _, err := h.core.Auth.Authenticate(r.Context(), token); err != nil {
		fail(w, err)
		return
	}
	if r.Method != http.MethodPost || !h.Handles(r) {
		fail(w, domain.ErrNotFound)
		return
	}
	p := route(r)
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
	key, err := operationKey(r)
	if err != nil {
		fail(w, err)
		return
	}
	if len(p) == 1 && p[0] == "projects" {
		// E4/D10: POST /projects has no query parameters at all — reject
		// any non-empty query, including an empty-named value.
		for name := range query {
			_ = name
			fail(w, session.ErrInvalid)
			return
		}
		h.register(w, r, token, key)
		return
	}
	if len(p) == 3 && p[2] == "source-observations" {
		// The owner-only observation POST carries its inputs in the body;
		// the route accepts no query parameters.
		for name := range query {
			_ = name
			fail(w, session.ErrInvalid)
			return
		}
		h.observe(w, r, token, key, p[1])
		return
	}
	// Unknown query keys are rejected exactly like the GET alias (C2/B).
	allowed := map[string]bool{"evidence_id": true}
	for name := range query {
		if !allowed[name] {
			fail(w, session.ErrInvalid)
			return
		}
	}
	var versionID, evidenceID string
	if len(p) == 3 {
		values := query["evidence_id"]
		if len(values) != 1 || values[0] == "" {
			fail(w, session.ErrInvalid)
			return
		}
		versionID, evidenceID = p[1], values[0]
	} else {
		versionID, evidenceID = p[1], p[3]
	}
	h.verify(w, r, token, key, versionID, evidenceID)
}

func (h *handler) register(w http.ResponseWriter, r *http.Request, token, key string) {
	if media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || media != "application/json" {
		fail(w, session.ErrInvalid)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		fail(w, session.ErrInvalid)
		return
	}
	var fields map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil || len(fields) != 1 {
		fail(w, session.ErrInvalid)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if _, ok := fields["name"]; !ok || json.Unmarshal(raw, &body) != nil {
		fail(w, session.ErrInvalid)
		return
	}
	response, err := h.core.Register(r.Context(), token, key, body.Name)
	durable(w, response, err)
}

func (h *handler) verify(w http.ResponseWriter, r *http.Request, token, key, versionID, evidenceID string) {
	if media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || media != "application/json" {
		fail(w, session.ErrInvalid)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, core.MaxInlineExcerpt+(1<<20)))
	if err != nil {
		fail(w, session.ErrInvalid)
		return
	}
	response, err := h.core.Verify(r.Context(), token, versionID, evidenceID, key, raw)
	durable(w, response, err)
}

// observe serves the owner-only observation POST: `{}` or
// `{source_ids?, scope?}` on one staged receipt-backed run.
func (h *handler) observe(w http.ResponseWriter, r *http.Request, token, key, versionID string) {
	if media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || media != "application/json" {
		fail(w, session.ErrInvalid)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		fail(w, session.ErrInvalid)
		return
	}
	response, err := h.core.Observe(r.Context(), token, versionID, key, raw)
	durable(w, response, err)
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

// durable writes receipt-backed responses. Successes use the ordinary data
// envelope; a failure status carries the stored top-level error envelope
// exactly as recorded — never wrapped under data (C11) — including replays.
func durable(w http.ResponseWriter, value domain.Response, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	if value.Status >= 400 {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(value.Status)
		_ = json.NewEncoder(w).Encode(json.RawMessage(value.Body))
		return
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
		{session.ErrRequired, 401, "AUTH_REQUIRED"}, {session.ErrExpired, 401, "SESSION_EXPIRED"}, {session.ErrRevoked, 401, "SESSION_REVOKED"}, {session.ErrInvalid, 400, "INVALID_REQUEST"}, {session.ErrMissing, 428, "PRECONDITION_REQUIRED"}, {domain.ErrDenied, 403, "FORBIDDEN"}, {domain.ErrNotFound, 404, "NOT_FOUND"}, {domain.ErrPrecondition, 412, "PRECONDITION_FAILED"}, {domain.ErrConflict, 409, "OPERATION_CONFLICT"}, {domain.ErrTransition, 409, "INVALID_TRANSITION"}, {domain.ErrReconciliation, 409, "OPERATION_RECONCILIATION_REQUIRED"},
		{syscall.ENOSPC, 503, "RESOURCE_EXHAUSTED"}, {syscall.EDQUOT, 503, "RESOURCE_EXHAUSTED"}, {syscall.ENOMEM, 503, "RESOURCE_EXHAUSTED"},
		{os.ErrPermission, 503, "FILE_ACCESS_ERROR"}, {syscall.EIO, 503, "FILE_ACCESS_ERROR"}, {syscall.EROFS, 503, "FILE_ACCESS_ERROR"},
		{io.ErrUnexpectedEOF, 503, "TRANSFER_INTERRUPTED"}, {context.Canceled, 503, "TRANSFER_INTERRUPTED"}, {context.DeadlineExceeded, 503, "TRANSFER_INTERRUPTED"},
	} {
		if errors.Is(err, item.err) {
			status, code, retryable = item.status, item.code, false
			break
		}
	}
	// A missing Idempotency-Key is the registry's 428 with the named header
	// in details (C11).
	var header missing
	if errors.As(err, &header) {
		status, code = 428, "PRECONDITION_REQUIRED"
	}
	if status == 429 || status == 503 {
		w.Header().Set("Retry-After", "2")
		retryable = true
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	details := map[string]any{}
	if errors.As(err, &header) {
		details["missing"] = string(header)
	}
	message := http.StatusText(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": retryable, "details": details}, "request_id": requestID()})
}
