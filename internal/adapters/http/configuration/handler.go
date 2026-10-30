package configuration

import (
	"bytes"
	"encoding/json"
	"errors"
	app "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"net/http"
	"strings"
)

type Handler struct{ Core *app.Service }

func New(core *app.Service) *Handler { return &Handler{core} }
func (*Handler) Handles(r *http.Request) bool {
	p := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	return p == "status" && r.Method == "GET" || p == "configuration" && r.Method == "GET" || p == "configuration/plans" && r.Method == "POST" || strings.HasPrefix(p, "configuration/plans/") && strings.HasSuffix(p, "/commit") && r.Method == "POST"
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestID, _ := service.NewID()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fail := func(e error) {
		status, code := 503, "SERVICE_RECOVERING"
		var invalid *app.Error
		switch {
		case errors.As(e, &invalid) || errors.Is(e, sec.ErrInvalid):
			status, code = 400, "INVALID_REQUEST"
		case errors.Is(e, sec.ErrRequired) || errors.Is(e, sec.ErrRevoked) || errors.Is(e, sec.ErrExpired):
			status, code = 401, e.Error()
		case errors.Is(e, domain.ErrDenied):
			status, code = 403, "FORBIDDEN"
		case errors.Is(e, domain.ErrNotFound):
			status, code = 404, "NOT_FOUND"
		case errors.Is(e, app.ErrStalePlan):
			status, code = 409, "STALE_PLAN"
		case errors.Is(e, domain.ErrConflict):
			status, code = 409, "OPERATION_CONFLICT"
		case errors.Is(e, domain.ErrPrecondition):
			status, code = 412, "PRECONDITION_FAILED"
		case errors.Is(e, domain.ErrReconciliation):
			status, code = 409, "OPERATION_RECONCILIATION_REQUIRED"
		}
		detail := map[string]any{}
		if invalid != nil {
			detail["field"] = invalid.Field
			detail["reason"] = invalid.Reason
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": nil, "error": map[string]any{"code": code, "message": code, "retryable": status == 503, "details": detail}, "request_id": requestID})
	}
	tokens := r.Header.Values("Authorization")
	if len(tokens) != 1 || !strings.HasPrefix(tokens[0], "Bearer ") {
		fail(sec.ErrRequired)
		return
	}
	token := strings.TrimPrefix(tokens[0], "Bearer ")
	// Authentication and owner authority precede control parsing and receipt lookup.
	view, e := h.Core.Read(r.Context(), token)
	if e != nil {
		fail(e)
		return
	}
	if len(r.URL.Query()) != 0 {
		fail(sec.ErrInvalid)
		return
	}

	if r.URL.Path == "/api/v1/status" {
		var configured struct {
			Network struct {
				Listener json.RawMessage `json:"listener"`
			} `json:"network"`
		}
		if e = json.Unmarshal(view.Value, &configured); e != nil {
			fail(e)
			return
		}
		health, e := h.Core.Store.Health(r.Context())
		if e != nil {
			fail(e)
			return
		}
		staging, e := h.Core.Staging(r.Context(), token)
		if e != nil {
			fail(e)
			return
		}
		e = h.Core.Auth.Guard(r.Context(), token, func() error {
			// W3b L8: a storage error deriving the source section fails the
			// request instead of reading as never observed.
			source, err := h.Core.SourceSummary(r.Context())
			if err != nil {
				return err
			}
			var backup any = map[string]any{"mode": "manual", "available": false, "reason": "backup_status_unavailable", "last_complete_verified": nil, "last_attempt": nil, "last_trial_restore": map[string]any{"state": "not_run", "completed_at": nil}}
			if h.Core.Backup != nil {
				backup, err = h.Core.Backup(r.Context())
				if err != nil {
					return err
				}
			}
			provider := map[string]any{"available": false, "reason": "feature_not_ready"}
			if h.Core.ProviderTransportAvailable {
				provider = map[string]any{"transport_available": true, "available": nil, "reason": "status_not_observed", "status_url": "/api/v1/providers", "generation_available": false, "question_available": false}
			}
			return json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"library_id": health.Library.ID, "schema_revision": health.Library.SchemaRevision, "runtime_generation": health.Library.RuntimeGeneration, "storage": map[string]any{"state": "ready", "journal": health.Journal, "foreign_keys": health.ForeignKeys, "staging_bytes": staging.Bytes, "pending_upload_count": staging.PendingUploads, "pending_uploads_url": staging.PendingUploadsURL}, "source": source, "config_revision": view.ConfigRevision, "listener": map[string]any{"endpoint": h.Core.RuntimeEndpoint, "ephemeral_override": h.Core.ListenerOverride, "configured": configured.Network.Listener}, "provider": provider, "scheduler": map[string]any{"available": false, "reason": "feature_not_ready"}, "backup": backup}, "request_id": requestID})
		})
		if e != nil {
			fail(e)
		}
		return
	}
	if r.Method == "GET" {
		w.Header().Set("ETag", view.ETag)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": view, "request_id": requestID})
		return
	}
	value, e := app.Decode(r.Body, "json")
	if e != nil {
		fail(e)
		return
	}
	body := app.Canonical(value)
	key := ""
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) > 1 || len(keys) == 1 && keys[0] == "" {
		fail(sec.ErrInvalid)
		return
	}
	if len(keys) == 1 {
		key = keys[0]
	}
	var response domain.Response
	if r.URL.Path == "/api/v1/configuration/plans" {
		for field := range value {
			if field != "scope" && field != "changes" && field != "expected_config_revision" {
				fail(sec.ErrInvalid)
				return
			}
		}
		var input app.PlanRequest
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		if e = decoder.Decode(&input); e != nil {
			fail(sec.ErrInvalid)
			return
		}
		scope, ok := value["scope"].(map[string]any)
		if !ok {
			fail(sec.ErrInvalid)
			return
		}
		for field := range scope {
			if field != "kind" && field != "project_id" {
				fail(sec.ErrInvalid)
				return
			}
		}
		response, e = h.Core.Plan(r.Context(), token, key, input)
	} else {
		for field := range value {
			if field != "confirmed" && field != "expected_config_revision" {
				fail(sec.ErrInvalid)
				return
			}
		}
		var input app.CommitRequest
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		if e = decoder.Decode(&input); e != nil {
			fail(sec.ErrInvalid)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 6 {
			fail(domain.ErrNotFound)
			return
		}
		response, e = h.Core.Commit(r.Context(), token, parts[4], key, input)
	}
	if e != nil {
		fail(e)
		return
	}
	for k, v := range response.Headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(response.Status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": json.RawMessage(response.Body), "request_id": requestID})
}
