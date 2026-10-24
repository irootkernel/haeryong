package v1

import (
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"io"
	"net/http"
	"strconv"
)

func lifecycleRoute(p []string) bool {
	return len(p) == 1 && p[0] == "trash" || len(p) == 3 && p[0] == "documents" && p[2] == "lifecycle-plans" || len(p) >= 2 && p[0] == "lifecycle-plans" && (len(p) == 2 || len(p) == 3 && p[2] == "commit")
}
func (h *Handler) lifecycle(w http.ResponseWriter, r *http.Request, token string, p []string) {
	allowed := []string{}
	if r.Method == "GET" || r.Method == "HEAD" {
		allowed = []string{"cursor", "limit"}
		if p[0] == "trash" {
			allowed = append(allowed, "project_id")
		}
	}
	values, err := parameters(r, allowed)
	if err != nil {
		failure(w, r, err)
		return
	}
	if r.Method == "GET" || r.Method == "HEAD" {
		o := query.Options{Cursor: values.Get("cursor"), Project: values.Get("project_id")}
		if values.Has("limit") {
			o.Limit, err = strconv.Atoi(values.Get("limit"))
			if err != nil || o.Limit < 1 {
				failure(w, r, sec.ErrInvalid)
				return
			}
		}
		var result any
		if p[0] == "trash" {
			result, err = h.Core.Trash(r.Context(), token, o)
		} else {
			result, err = h.Core.LifecyclePlanGet(r.Context(), token, p[1], o)
		}
		if err != nil {
			failure(w, r, err)
			return
		}
		respond(w, r, 200, result)
		return
	}
	key := ""
	if len(r.Header.Values("Idempotency-Key")) > 0 {
		key, err = personalOperationKey(r)
		if err != nil {
			failure(w, r, err)
			return
		}
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		failure(w, r, err)
		return
	}
	var result domain.Response
	if p[0] == "lifecycle-plans" {
		result, err = h.Core.LifecycleCommit(r.Context(), token, p[1], key, body)
	} else {
		result, err = h.Core.LifecyclePlanCreate(r.Context(), token, key, p[1], body)
	}
	if err != nil {
		failure(w, r, err)
		return
	}
	respond(w, r, result.Status, json.RawMessage(result.Body))
}

// Keep the lifecycle error registry in the common adapter.
func lifecycleFailure(err error) (int, string) {
	if errors.Is(err, query.ErrStaleLifecyclePlan) {
		return 409, "STALE_PLAN"
	}
	if errors.Is(err, query.ErrLifecycleNotReady) {
		return 503, "FEATURE_NOT_READY"
	}
	return 0, ""
}
