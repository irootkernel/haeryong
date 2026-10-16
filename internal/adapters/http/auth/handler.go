// Package auth adapts current owner-session authority to the application HTTP
// listener. Protected local-control HMAC routing remains a separate boundary.
package auth

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/domain"
	"haeryong.local/haeryong/internal/security/session"
)

//go:embed web/index.html web/client.js
var assets embed.FS

type Policy struct{ Hosts, Origins []string }
type identityKey struct{}

func SessionFromContext(ctx context.Context) (domain.Session, bool) {
	value, ok := ctx.Value(identityKey{}).(domain.Session)
	return value, ok
}

type handler struct {
	core           *appauth.Service
	hosts, origins map[string]bool
	protected      http.Handler
}

func New(core *appauth.Service, policy Policy, protected http.Handler) (http.Handler, error) {
	if core == nil || len(policy.Hosts) == 0 || len(policy.Origins) == 0 {
		return nil, errors.New("auth core and exact hosts/origins required")
	}
	h := &handler{core: core, hosts: map[string]bool{}, origins: map[string]bool{}, protected: protected}
	for _, host := range policy.Hosts {
		u, err := url.Parse("http://" + host)
		if err != nil || u.Host != host || u.User != nil || u.Hostname() == "" || u.Path != "" || strings.ContainsAny(host, "*\\\r\n\t /?#") {
			return nil, errors.New("invalid expected Host")
		}
		if port := u.Port(); port != "" {
			number, err := strconv.Atoi(port)
			if err != nil || number < 1024 || number > 65535 {
				return nil, errors.New("invalid expected Host port")
			}
		}
		h.hosts[host] = true
	}
	for _, origin := range policy.Origins {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.String() != origin || !h.hosts[u.Host] {
			return nil, errors.New("invalid exact browser Origin")
		}
		h.origins[origin] = true
	}
	return h, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; connect-src 'self'; img-src data:; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'")
	if !h.hosts[r.Host] {
		fail(w, r, session.ErrOrigin)
		return
	}
	origin := r.Header.Get("Origin")
	if origin != "" && (!h.origins[origin] || len(r.Header.Values("Origin")) != 1) {
		fail(w, r, session.ErrOrigin)
		return
	}
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Expose-Headers", "ETag, Retry-After")
	}
	if r.Method == http.MethodOptions && strings.HasPrefix(r.URL.Path, "/api/v1/") {
		if origin == "" {
			fail(w, r, session.ErrOrigin)
			return
		}
		for _, header := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
			switch strings.ToLower(strings.TrimSpace(header)) {
			case "", "authorization", "content-type", "if-match", "idempotency-key", "last-event-id", "haeryong-upload-offset", "haeryong-upload-chunk-sha256", "haeryong-upload-length", "haeryong-upload-final":
			default:
				fail(w, r, session.ErrOrigin)
				return
			}
		}
		switch r.Header.Get("Access-Control-Request-Method") {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
		default:
			fail(w, r, session.ErrOrigin)
			return
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-Match, Idempotency-Key, Last-Event-ID, Haeryong-Upload-Offset, Haeryong-Upload-Chunk-SHA256, Haeryong-Upload-Length, Haeryong-Upload-Final")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodGet && (shellPath(r.URL.Path) || r.URL.Path == "/auth-client.js") {
		name := "web/index.html"
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/auth-client.js" {
			name = "web/client.js"
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		}
		body, _ := assets.ReadFile(name)
		if name == "web/index.html" {
			settings, err := h.core.Settings(r.Context())
			if err != nil {
				fail(w, r, err)
				return
			}
			if !settings.RememberDeviceDefault {
				body = []byte(strings.Replace(string(body), `type="checkbox" checked`, `type="checkbox"`, 1))
			}
		}
		w.Write(body)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
		fail(w, r, domain.ErrNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	parts := strings.Split(path, "/")
	public := r.Method == http.MethodPost && (path == "auth/pairings" || (len(parts) == 4 && parts[0] == "auth" && parts[1] == "pairings" && parts[3] == "poll"))
	token := bearer(r)
	if public {
		if origin == "" {
			fail(w, r, session.ErrOrigin)
			return
		}
	} else {
		identity, err := h.core.Authenticate(r.Context(), token)
		if err != nil {
			fail(w, r, err)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), identityKey{}, identity))
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && origin == "" && r.Header.Get("Sec-Fetch-Site") != "" {
		fail(w, r, session.ErrOrigin)
		return
	}
	switch {
	case path == "auth/pairings" && r.Method == http.MethodPost:
		var input struct {
			Label string `json:"device_label"`
		}
		if err := decode(w, r, &input); err != nil {
			fail(w, r, err)
			return
		}
		client, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			client = "unattributed"
		}
		value, err := h.core.CreatePairing(r.Context(), origin, input.Label, client)
		respond(w, r, 201, value, err)
	case path == "auth/pairings" && r.Method == http.MethodGet:
		value, err := h.core.Pairings(r.Context(), token)
		h.private(w, r, token, func() { respond(w, r, 200, commonList(value), err) })
	case len(parts) == 4 && parts[0] == "auth" && parts[1] == "pairings" && parts[3] == "poll" && r.Method == http.MethodPost:
		var input struct {
			Secret string `json:"polling_secret"`
		}
		if err := decode(w, r, &input); err != nil {
			fail(w, r, err)
			return
		}
		value, err := h.core.Poll(r.Context(), parts[2], input.Secret, origin)
		respond(w, r, 200, value, err)
	case len(parts) == 4 && parts[0] == "auth" && parts[1] == "pairings" && (parts[3] == "approve" || parts[3] == "reject") && r.Method == http.MethodPost:
		if err := mutationHeaders(r, true); err != nil {
			fail(w, r, err)
			return
		}
		var input struct {
			Code   string `json:"code"`
			Origin string `json:"origin"`
		}
		if err := decode(w, r, &input); err != nil {
			fail(w, r, err)
			return
		}
		value, err := h.core.Decide(r.Context(), token, domain.PairingDecision{ID: parts[2], Code: input.Code, Origin: input.Origin, ETag: r.Header.Get("If-Match"), OperationID: r.Header.Get("Idempotency-Key"), Action: parts[3]})
		durable(w, r, value, err)
	case path == "session" && r.Method == http.MethodGet:
		value, err := h.core.Authenticate(r.Context(), token)
		h.private(w, r, token, func() {
			if err == nil {
				w.Header().Set("ETag", value.ETag)
			}
			respond(w, r, 200, value, err)
		})
	case path == "session" && r.Method == http.MethodDelete:
		identity, _ := SessionFromContext(r.Context())
		_, err := h.core.Revoke(r.Context(), token, identity.ID)
		respond(w, r, 204, nil, err)
	case path == "sessions" && r.Method == http.MethodGet:
		value, err := h.core.Sessions(r.Context(), token)
		h.private(w, r, token, func() { respond(w, r, 200, commonList(value), err) })
	case len(parts) == 2 && parts[0] == "sessions" && r.Method == http.MethodDelete:
		_, err := h.core.Revoke(r.Context(), token, parts[1])
		respond(w, r, 204, nil, err)
	case len(parts) == 2 && parts[0] == "operations" && r.Method == http.MethodGet:
		value, err := h.core.Operation(r.Context(), token, parts[1])
		if err != nil {
			fail(w, r, err)
			return
		}
		data := map[string]any{"operation_id": value.Key.OperationID, "state": value.State, "original_status": nil, "result_ref": nil, "job_id": nil, "result_metadata": nil}
		if value.Response != nil {
			data["original_status"] = value.Response.Status
			data["result_metadata"] = json.RawMessage(value.Response.Body)
		}
		h.private(w, r, token, func() { respond(w, r, 200, data, nil) })
	case path == "jobs" && r.Method == http.MethodGet:
		value, err := h.core.Jobs(r.Context(), token)
		items := make([]map[string]any, 0, len(value))
		for _, job := range value {
			items = append(items, jobWire(job))
		}
		h.private(w, r, token, func() { respond(w, r, 200, commonList(items), err) })
	case len(parts) == 2 && parts[0] == "jobs" && r.Method == http.MethodGet:
		value, err := h.core.Job(r.Context(), token, parts[1])
		data := jobWire(value)
		h.private(w, r, token, func() {
			if err == nil {
				w.Header().Set("ETag", data["etag"].(string))
			}
			respond(w, r, 200, data, err)
		})
	case len(parts) == 3 && parts[0] == "jobs" && parts[2] == "cancel" && r.Method == http.MethodPost:
		if err := mutationHeaders(r, false); err != nil {
			fail(w, r, err)
			return
		}
		var input struct{}
		if err := decode(w, r, &input); err != nil {
			fail(w, r, err)
			return
		}
		value, err := h.core.CancelJob(r.Context(), token, parts[1], r.Header.Get("Idempotency-Key"))
		durable(w, r, value, err)
	case path == "status" && r.Method == http.MethodGet:
		value, err := h.core.Health(r.Context(), token)
		h.private(w, r, token, func() { respond(w, r, 200, value, err) })
	case path == "events" && r.Method == http.MethodGet:
		h.events(w, r, token)
	default:
		if h.protected != nil {
			router, scoped := h.protected.(interface{ Handles(*http.Request) bool })
			if !scoped || router.Handles(r) {
				h.private(w, r, token, func() { h.protected.ServeHTTP(w, r) })
				return
			}
		}
		if futureCapability(r.Method, path) {
			fail(w, r, errFeatureNotReady)
		} else {
			fail(w, r, domain.ErrNotFound)
		}
	}
}

func commonList(items any) map[string]any { return map[string]any{"items": items, "next_cursor": nil} }

var errFeatureNotReady = errors.New("FEATURE_NOT_READY")

// This is the frozen HTTP catalog's unimplemented capability boundary, not a
// dispatcher. Implemented routes above take precedence; unmatched methods and
// paths stay unknown instead of pretending that an outage or feature exists.
var futureRoutes = func() [][]string {
	const catalog = `GET capabilities
GET projects
POST projects
GET projects/{id}/sources
GET documents
GET documents/{id}
GET documents/{id}/versions
GET versions/{id}
POST uploads
GET uploads
GET uploads/{id}
GET uploads/{id}/pins
GET uploads/{id}/entries
GET uploads/{id}/ranges
PUT uploads/{id}/entries
POST uploads/{id}/complete
POST uploads/{id}/abandon
POST imports
POST packages/validate
GET versions/{id}/render-bootstrap
GET versions/{id}/files
GET versions/{id}/file
GET versions/{id}/metadata
GET versions/{id}/package
GET versions/{id}/source-evidence/{evidence_id}
POST versions/{id}/source-evidence/{evidence_id}/verifications
GET versions/{id}/source-evidence/{evidence_id}/verifications
GET versions/{id}/assessments
GET versions/{id}/assessments/coverage
GET search
GET versions/{id}/references
GET versions/{id}/backlinks
GET versions/{id}/related
GET topic-assignments
PUT topic-assignments/{id}
GET versions/{id}/review
PUT versions/{id}/review
GET documents/{id}/revisit
PUT documents/{id}/revisit
GET versions/{id}/reading-position
PUT versions/{id}/reading-position
GET versions/{id}/page-position
PUT versions/{id}/page-position
GET versions/{id}/reader-preference
PUT versions/{id}/reader-preference
GET versions/{id}/annotations
GET annotations/{id}
POST contexts
GET contexts/{id}
POST versions/{id}/annotations
PATCH annotations/{id}
DELETE annotations/{id}
POST documents/{id}/lifecycle-plans
POST lifecycle-plans/{id}/commit
POST versions/{id}/questions
GET versions/{id}/questions
GET questions/{id}
POST generations
GET drafts
GET drafts/{id}
PATCH drafts/{id}
POST drafts/{id}/publish
POST scans
POST jobs/{id}/resume
GET jobs/{id}/findings
POST findings/{id}/decisions
POST overlays/{id}/undo
GET configuration
POST configuration/plans
POST configuration/plans/{id}/commit`
	var routes [][]string
	for _, line := range strings.Split(catalog, "\n") {
		method, path, _ := strings.Cut(line, " ")
		routes = append(routes, append([]string{method}, strings.Split(path, "/")...))
	}
	return routes
}()

func futureCapability(method, path string) bool {
	actual := append([]string{method}, strings.Split(path, "/")...)
	for _, route := range futureRoutes {
		if len(route) != len(actual) {
			continue
		}
		matched := true
		for i, part := range route {
			if (strings.HasPrefix(part, "{") && actual[i] != "") || part == actual[i] {
				continue
			}
			matched = false
			break
		}
		if matched {
			return true
		}
	}
	return false
}

func (h *handler) private(w http.ResponseWriter, r *http.Request, token string, deliver func()) {
	delivered := false
	err := h.core.Guard(r.Context(), token, func() error { delivered = true; deliver(); return nil })
	if err != nil && !delivered {
		fail(w, r, err)
	}
}

func bearer(r *http.Request) string {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return ""
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if !session.ValidSecret(token) {
		return ""
	}
	return token
}

func decode(w http.ResponseWriter, r *http.Request, value any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return session.ErrInvalid
	}
	// Only small authentication control envelopes use this bound, never content.
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	decoder := json.NewDecoder(r.Body)
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return session.ErrInvalid
	}
	fields := map[string]reflect.Value{}
	target := reflect.ValueOf(value).Elem()
	for i := 0; i < target.NumField(); i++ {
		fields[target.Type().Field(i).Tag.Get("json")] = target.Field(i)
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return session.ErrInvalid
		}
		name, ok := key.(string)
		field, allowed := fields[name]
		if !ok || !allowed || seen[name] {
			return session.ErrInvalid
		}
		seen[name] = true
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil || string(raw) == "null" {
			return session.ErrInvalid
		}
		if err := json.Unmarshal(raw, field.Addr().Interface()); err != nil {
			return session.ErrInvalid
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || len(seen) != len(fields) {
		return session.ErrInvalid
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return session.ErrInvalid
	}
	return nil
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var logicalShell = regexp.MustCompile(`^/(?:projects/[0-9a-f-]{36}(?:/sources)?|documents/[0-9a-f-]{36}/versions(?:/[0-9a-f-]{36})?|maintenance/jobs/[0-9a-f-]{36}|settings/(?:general|connection|sessions|projects|ai|maintenance|storage|retention|backup|about))$`)

func shellPath(path string) bool {
	switch path {
	case "/", "/connect", "/reader", "/library", "/projects", "/maintenance", "/trash", "/settings":
		return true
	}
	if !logicalShell.MatchString(path) {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if len(part) == 36 && !uuidPattern.MatchString(part) {
			return false
		}
	}
	return true
}

var pairingValidator = regexp.MustCompile(`^"pairing-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}-r[1-9][0-9]*"$`)

type missingHeader string

func (e missingHeader) Error() string        { return "missing precondition" }
func (e missingHeader) Is(target error) bool { return target == session.ErrMissing }
func mutationHeaders(r *http.Request, validator bool) error {
	for _, name := range []string{"Idempotency-Key", "If-Match"} {
		if name == "If-Match" && !validator {
			continue
		}
		values := r.Header.Values(name)
		if len(values) == 0 {
			return missingHeader(name)
		}
		if len(values) != 1 {
			return session.ErrInvalid
		}
		if (name == "Idempotency-Key" && !uuidPattern.MatchString(values[0])) || (name == "If-Match" && !pairingValidator.MatchString(values[0])) {
			return session.ErrInvalid
		}
	}
	return nil
}

func requestID() string {
	id, err := session.Secret()
	if err != nil {
		return "unavailable"
	}
	return id[:16]
}
func respond(w http.ResponseWriter, r *http.Request, status int, value any, err error) {
	if err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if status != 204 {
		json.NewEncoder(w).Encode(map[string]any{"data": value, "request_id": requestID()})
	}
}
func durable(w http.ResponseWriter, r *http.Request, response domain.Response, err error) {
	if err != nil {
		fail(w, r, err)
		return
	}
	for key, value := range response.Headers {
		if key == "ETag" || key == "Location" {
			w.Header().Set(key, value)
		}
	}
	var body any = json.RawMessage(response.Body)
	if strings.HasPrefix(r.URL.Path, "/api/v1/jobs/") && strings.HasSuffix(r.URL.Path, "/cancel") {
		var job domain.Job
		if json.Unmarshal(response.Body, &job) == nil {
			body = jobWire(job)
			w.Header().Set("ETag", body.(map[string]any)["etag"].(string))
		}
	}
	respond(w, r, response.Status, body, nil)
}
func jobWire(job domain.Job) map[string]any {
	bytes, _ := json.Marshal(job)
	digest := sha256.Sum256(bytes)
	var value map[string]any
	_ = json.Unmarshal(bytes, &value)
	value["etag"] = fmt.Sprintf(`"job-%s-%x"`, job.ID, digest[:])
	return value
}
func fail(w http.ResponseWriter, r *http.Request, err error) {
	status, code, retryable := 503, "SERVICE_RECOVERING", true
	for _, item := range []struct {
		err    error
		status int
		code   string
	}{
		{session.ErrRequired, 401, "AUTH_REQUIRED"}, {session.ErrExpired, 401, "SESSION_EXPIRED"}, {session.ErrRevoked, 401, "SESSION_REVOKED"},
		{session.ErrOrigin, 403, "ORIGIN_REJECTED"}, {session.ErrInvalid, 400, "INVALID_REQUEST"}, {session.ErrLimited, 429, "RATE_LIMITED"},
		{domain.ErrInvalidTarget, 400, "INVALID_REQUEST"},
		{session.ErrMissing, 428, "PRECONDITION_REQUIRED"}, {domain.ErrPrecondition, 412, "PRECONDITION_FAILED"}, {domain.ErrNotFound, 404, "NOT_FOUND"},
		{domain.ErrDenied, 403, "FORBIDDEN"}, {domain.ErrConflict, 409, "OPERATION_CONFLICT"}, {domain.ErrTransition, 409, "INVALID_TRANSITION"},
		{domain.ErrReconciliation, 409, "OPERATION_RECONCILIATION_REQUIRED"},
		{errFeatureNotReady, 503, "FEATURE_NOT_READY"},
	} {
		if errors.Is(err, item.err) {
			status, code, retryable = item.status, item.code, false
			break
		}
	}
	if status == 429 || (status == 503 && code != "FEATURE_NOT_READY") {
		w.Header().Set("Retry-After", "2")
		retryable = true
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	details := map[string]any{}
	var missing missingHeader
	if errors.As(err, &missing) {
		details["missing"] = string(missing)
	}
	message := http.StatusText(status)
	if code == "FEATURE_NOT_READY" {
		message = "This capability is not implemented yet."
	}
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": retryable, "details": details}, "request_id": requestID()})
}

func (h *handler) events(w http.ResponseWriter, r *http.Request, token string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, r, session.ErrUnavailable)
		return
	}
	health, err := h.core.Health(r.Context(), token)
	if err != nil {
		fail(w, r, err)
		return
	}
	currentEpoch, head, err := h.core.EventHead(r.Context(), token)
	if err != nil {
		fail(w, r, err)
		return
	}
	epoch := r.URL.Query().Get("stream_epoch")
	after := int64(0)
	resync := ""
	if len(r.Header.Values("Last-Event-ID")) > 1 {
		resync = "malformed_cursor"
	}
	if cursor := r.Header.Get("Last-Event-ID"); cursor != "" {
		parts := strings.Split(cursor, ":")
		if len(parts) != 2 || !uuidPattern.MatchString(parts[0]) {
			resync = "malformed_cursor"
		} else {
			epoch = parts[0]
			after, err = strconv.ParseInt(parts[1], 10, 64)
			if err != nil || after < 0 || strconv.FormatInt(after, 10) != parts[1] {
				resync = "malformed_cursor"
			}
		}
	}
	if value := r.URL.Query().Get("after"); value != "" {
		after, err = strconv.ParseInt(value, 10, 64)
		if err != nil || after < 0 {
			resync = "malformed_cursor"
		}
	}
	if epoch == "" {
		epoch = currentEpoch
	}
	if epoch != currentEpoch {
		resync = "epoch_changed"
	}
	if after > head {
		resync = "future_cursor"
	}
	if resync != "" {
		epoch = currentEpoch
		after = head
	}
	if _, err := h.core.Events(r.Context(), token, epoch, after); err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	write := func(event, id string, value any) error {
		if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		return h.core.Guard(r.Context(), token, func() error {
			var err error
			if event == "" {
				_, err = io.WriteString(w, ": connected\n\n")
			} else {
				body, e := json.Marshal(value)
				if e != nil {
					return e
				}
				_, err = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", id, event, body)
			}
			if err == nil {
				flusher.Flush()
			}
			return err
		})
	}
	if resync != "" {
		if err := write("resync.required", fmt.Sprintf("%s:%d", epoch, head), map[string]any{"reason": resync, "stream_epoch": epoch, "head": strconv.FormatInt(head, 10), "resume_cursor": fmt.Sprintf("%s:%d", epoch, head)}); err != nil {
			return
		}
	} else if write("", "", nil) != nil {
		return
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if write("", "", nil) != nil {
				return
			}
			continue
		case <-ticker.C:
		}
		events, err := h.core.Events(r.Context(), token, epoch, after)
		if err != nil {
			return
		}
		for _, event := range events {
			event, err = h.core.FilterEvent(r.Context(), token, event)
			if err != nil {
				return
			}
			kind := event.Kind
			if kind == "configuration.changed" {
				kind = "policy.changed"
			}
			if kind == "session_revoked" {
				kind = "session.revoked"
			}
			if !strings.Contains(kind, ".") {
				kind = "resource.invalidated"
			}
			data := map[string]any{"library_id": health.Library.ID, "stream_epoch": event.StreamEpoch, "sequence": strconv.FormatInt(event.Sequence, 10), "type": kind, "resource": map[string]any{"type": event.ResourceKind, "id": event.ResourceID}, "revision": event.Revision}
			if event.ResourceKind == "library" && kind == "library.invalidated" {
				data["resource"] = nil
			}
			if err := write(kind, fmt.Sprintf("%s:%d", event.StreamEpoch, event.Sequence), data); err != nil {
				return
			}
			after = event.Sequence
		}
	}
}
