package scans

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/scan"
	"haeryong.local/haeryong/internal/domain"
	"haeryong.local/haeryong/internal/security/session"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type handler struct {
	core *scan.Service
}

func New(core *scan.Service) http.Handler {
	return &handler{core: core}
}

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

func (h *handler) Handles(r *http.Request) bool {
	if h == nil || h.core == nil {
		return false
	}
	p := route(r)
	if len(p) == 3 && p[1] != "" && ((p[0] == "findings" && p[2] == "decisions" && r.Method == http.MethodPost) || (p[0] == "overlays" && p[2] == "undo" && r.Method == http.MethodPost)) {
		return true
	}
	if len(p) == 2 && p[0] == "topic-assignments" && p[1] != "" && r.Method == http.MethodPut {
		return true
	}
	if len(p) == 1 && p[0] == "scans" && r.Method == http.MethodPost {
		return true
	}
	if len(p) == 3 && p[0] == "jobs" && p[1] != "" && p[2] == "findings" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		return true
	}
	return false
}

type missingHeader string

func (e missingHeader) Error() string { return "missing required header" }

func operationKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) == 0 {
		return "", missingHeader("Idempotency-Key")
	}
	if len(values) != 1 || !uuidPattern.MatchString(values[0]) {
		return "", session.ErrInvalid
	}
	return values[0], nil
}

func bearer(r *http.Request) (string, error) {
	values := r.Header.Values("Authorization")
	if len(values) == 0 {
		return "", session.ErrRequired
	}
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return "", session.ErrInvalid
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if !session.ValidSecret(token) {
		return "", session.ErrInvalid
	}
	return token, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if ctx, err := appauth.ValidatePassiveRead(r.Context(), r.Method, r.Header.Values("Haeryong-Passive-Read")); err != nil {
		fail(w, r, err)
		return
	} else {
		r = r.WithContext(ctx)
	}
	token, err := bearer(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	if !h.Handles(r) {
		fail(w, r, domain.ErrNotFound)
		return
	}
	p := route(r)
	if p[0] == "findings" || p[0] == "overlays" || p[0] == "topic-assignments" {
		h.decision(w, r, token, p)
		return
	}
	if len(p) == 1 && p[0] == "scans" && r.Method == http.MethodPost {
		h.startScan(w, r, token)
		return
	}
	if len(p) == 3 && p[0] == "jobs" && p[2] == "findings" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		h.listFindings(w, r, token, p[1])
		return
	}
	fail(w, r, domain.ErrNotFound)
}

func (h *handler) startScan(w http.ResponseWriter, r *http.Request, token string) {
	if r.URL.RawQuery != "" {
		fail(w, r, session.ErrInvalid)
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 {
		fail(w, r, session.ErrInvalid)
		return
	}
	key, err := operationKey(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		fail(w, r, session.ErrInvalid)
		return
	}
	req, err := decodeScanRequest(raw)
	if err != nil {
		fail(w, r, err)
		return
	}
	resp, err := h.core.StartScan(r.Context(), token, key, raw, req)
	if err != nil {
		fail(w, r, err)
		return
	}
	for k, v := range resp.Headers {
		if k == "ETag" || k == "Location" {
			w.Header().Set(k, v)
		}
	}
	respond(w, r, resp.Status, json.RawMessage(resp.Body))
}

func decodeScanRequest(raw []byte) (domain.ScanRequest, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return domain.ScanRequest{}, session.ErrInvalid
	}
	// Reject duplicate keys at the top level.
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return domain.ScanRequest{}, session.ErrInvalid
	}
	seen := map[string]bool{}
	for dec.More() {
		kTok, e := dec.Token()
		if e != nil {
			return domain.ScanRequest{}, session.ErrInvalid
		}
		kStr, ok := kTok.(string)
		if !ok || seen[kStr] {
			return domain.ScanRequest{}, session.ErrInvalid
		}
		seen[kStr] = true
		var val json.RawMessage
		if dec.Decode(&val) != nil {
			return domain.ScanRequest{}, session.ErrInvalid
		}
	}
	endTok, err := dec.Token()
	if err != nil || endTok != json.Delim('}') {
		return domain.ScanRequest{}, session.ErrInvalid
	}
	var extra any
	if !errors.Is(dec.Decode(&extra), io.EOF) {
		return domain.ScanRequest{}, session.ErrInvalid
	}

	strictDec := json.NewDecoder(bytes.NewReader(trimmed))
	strictDec.DisallowUnknownFields()
	var req domain.ScanRequest
	if err := strictDec.Decode(&req); err != nil {
		return domain.ScanRequest{}, session.ErrInvalid
	}
	if !errors.Is(strictDec.Decode(&extra), io.EOF) {
		return domain.ScanRequest{}, session.ErrInvalid
	}
	return req, nil
}

func (h *handler) listFindings(w http.ResponseWriter, r *http.Request, token, jobID string) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		fail(w, r, session.ErrInvalid)
		return
	}
	allowed := map[string]bool{"kind": true, "status": true, "cursor": true, "limit": true}
	for k, vals := range q {
		if !allowed[k] || len(vals) != 1 || vals[0] == "" {
			fail(w, r, session.ErrInvalid)
			return
		}
	}
	limit := 50
	if q.Has("limit") {
		n, e := strconv.Atoi(q.Get("limit"))
		if e != nil || n < 1 || n > 200 {
			fail(w, r, session.ErrInvalid)
			return
		}
		limit = n
	}
	page, err := h.core.ListJobFindings(r.Context(), token, domain.FindingFilter{
		JobID:  jobID,
		Kind:   q.Get("kind"),
		Status: q.Get("status"),
		Cursor: q.Get("cursor"),
		Limit:  limit,
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	err = h.core.Auth.Guard(r.Context(), token, func() error {
		if page.ETag != "" {
			w.Header().Set("ETag", page.ETag)
			if matchIfNoneMatch(r.Header.Values("If-None-Match"), page.ETag) {
				w.WriteHeader(http.StatusNotModified)
				return nil
			}
		}
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			return nil
		}
		respond(w, r, http.StatusOK, map[string]any{
			"items":       page.Items,
			"next_cursor": page.NextCursor,
		})
		return nil
	})
	if err != nil {
		fail(w, r, err)
	}
}

func matchIfNoneMatch(headers []string, etag string) bool {
	if etag == "" || len(headers) == 0 {
		return false
	}
	for _, h := range headers {
		for _, part := range strings.Split(h, ",") {
			candidate := strings.TrimSpace(part)
			candidate = strings.TrimPrefix(candidate, "W/")
			if candidate == "*" || candidate == etag {
				return true
			}
		}
	}
	return false
}

func respond(w http.ResponseWriter, r *http.Request, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if status != http.StatusNoContent && r.Method != http.MethodHead {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":       value,
			"request_id": requestID(),
		})
	}
}

func requestID() string {
	id, err := session.Secret()
	if err != nil {
		return "unavailable"
	}
	return id[:16]
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	status, code, retryable := 503, "SERVICE_RECOVERING", true
	details := map[string]any{}
	for _, item := range []struct {
		err    error
		status int
		code   string
	}{
		{session.ErrRequired, 401, "AUTH_REQUIRED"},
		{session.ErrExpired, 401, "SESSION_EXPIRED"},
		{session.ErrRevoked, 401, "SESSION_REVOKED"},
		{session.ErrOrigin, 403, "ORIGIN_REJECTED"},
		{session.ErrInvalid, 400, "INVALID_REQUEST"},
		{session.ErrLimited, 429, "RATE_LIMITED"},
		{domain.ErrInvalidTarget, 400, "INVALID_REQUEST"},
		{session.ErrMissing, 428, "PRECONDITION_REQUIRED"},
		{domain.ErrPrecondition, 412, "PRECONDITION_FAILED"},
		{domain.ErrNotFound, 404, "NOT_FOUND"},
		{domain.ErrDenied, 403, "FORBIDDEN"},
		{domain.ErrConflict, 409, "OPERATION_CONFLICT"},
		{domain.ErrTransition, 409, "INVALID_TRANSITION"},
		{domain.ErrReconciliation, 409, "OPERATION_RECONCILIATION_REQUIRED"},
		{domain.ErrFeatureNotReady, 503, "FEATURE_NOT_READY"},
	} {
		if errors.Is(err, item.err) {
			status, code, retryable = item.status, item.code, false
			break
		}
	}
	var ae *domain.FindingActionError
	if errors.As(err, &ae) {
		status, code, retryable = ae.Status, ae.Code, false
		details["reason"] = ae.Reason
	}
	var mh missingHeader
	if errors.As(err, &mh) {
		status, code, retryable = 428, "PRECONDITION_REQUIRED", false
		details["missing"] = string(mh)
	}
	if errors.Is(err, domain.ErrFeatureNotReady) {
		details["reason"] = "finding_reassessment_not_ready"
	}
	if status == 429 || (status == 503 && code != "FEATURE_NOT_READY") {
		w.Header().Set("Retry-After", "2")
		retryable = true
	}
	message := http.StatusText(status)
	if code == "FEATURE_NOT_READY" {
		message = "This capability is not implemented yet."
	}
	body, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"code":      code,
			"message":   message,
			"retryable": retryable,
			"details":   details,
		},
		"request_id": requestID(),
	})
	body = append(body, '\n')
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
