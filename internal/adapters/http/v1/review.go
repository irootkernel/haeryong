package v1

import (
	"encoding/json"
	"haeryong.local/haeryong/internal/application/query"
	sec "haeryong.local/haeryong/internal/security/session"
	"io"
	"net/http"
	"regexp"
	"strconv"
)

var personalETag = regexp.MustCompile(`^"[^"\r\n]+"$`)

func reviewRoute(p []string) bool {
	return len(p) >= 3 && p[0] == "versions" && p[2] == "review" && (len(p) == 3 || len(p) == 4 && p[3] == "history")
}
func personalConditions(r *http.Request) (string, bool, error) {
	match, none := r.Header.Values("If-Match"), r.Header.Values("If-None-Match")
	if len(match) == 1 && len(none) == 0 && personalETag.MatchString(match[0]) {
		return match[0], false, nil
	}
	if len(match) == 0 && len(none) == 1 && none[0] == "*" {
		return "", true, nil
	}
	if len(match) == 0 && len(none) == 0 {
		return "", false, missingPrecondition("If-Match")
	}
	return "", false, sec.ErrInvalid
}
func (h *Handler) review(w http.ResponseWriter, r *http.Request, token string, p []string) {
	allowed := []string{}
	if len(p) == 4 {
		allowed = []string{"cursor", "limit"}
	}
	values, err := parameters(r, allowed)
	if err != nil {
		failure(w, r, err)
		return
	}
	if len(p) == 4 {
		o := query.Options{Cursor: values.Get("cursor")}
		if values.Has("limit") {
			o.Limit, err = strconv.Atoi(values.Get("limit"))
			if err != nil || o.Limit < 1 {
				failure(w, r, &query.Error{Reason: "invalid_page_size"})
				return
			}
		}
		view, err := h.Core.ReviewHistory(r.Context(), token, p[1], o)
		if err != nil {
			failure(w, r, err)
			return
		}
		respond(w, r, 200, view)
		return
	}
	if r.Method == "PUT" {
		key, err := personalOperationKey(r)
		if err != nil {
			failure(w, r, err)
			return
		}
		etag, create, err := personalConditions(r)
		if err != nil {
			failure(w, r, err)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			failure(w, r, sec.ErrInvalid)
			return
		}
		result, err := h.Core.ReviewPut(r.Context(), token, key, p[1], etag, create, body)
		if err != nil {
			failure(w, r, err)
			return
		}
		if etag := result.Headers["ETag"]; etag != "" {
			w.Header().Set("ETag", etag)
		}
		respond(w, r, result.Status, json.RawMessage(result.Body))
		return
	}
	view, err := h.Core.ReviewGet(r.Context(), token, p[1])
	if err != nil {
		failure(w, r, err)
		return
	}
	if view.ETag != nil {
		w.Header().Set("ETag", *view.ETag)
	}
	respond(w, r, 200, view)
}
