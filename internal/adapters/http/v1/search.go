package v1

import (
	"context"
	"errors"
	"haeryong.local/haeryong/internal/application/query"
	"net/http"
	"net/url"
	"strconv"
	"unicode"
	"unicode/utf8"
)

// Search alone accepts q=. Other closed parameter handlers retain their rules.
func searchParameters(r *http.Request) (query.SearchOptions, error) {
	o := query.SearchOptions{}
	values, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return o, &query.Error{Reason: "invalid_query"}
	}
	for key, v := range values {
		allowed := false
		for _, a := range []string{"q", "project_id", "lifecycle", "versions", "kind", "cursor", "limit"} {
			if key == a {
				allowed = true
			}
		}
		if !allowed || len(v) != 1 || (key != "q" && v[0] == "") {
			return o, &query.Error{Reason: "unknown_duplicate_or_empty_query"}
		}
		if !utf8.ValidString(v[0]) {
			return o, &query.Error{Reason: "invalid_query"}
		}
		for _, c := range v[0] {
			if unicode.IsControl(c) && !(key == "q" && (c == '\t' || c == '\n' || c == '\r')) {
				return o, &query.Error{Reason: "invalid_query"}
			}
		}
	}
	o.Text, o.Project, o.Lifecycle, o.Versions, o.Kind, o.Cursor = values.Get("q"), values.Get("project_id"), values.Get("lifecycle"), values.Get("versions"), values.Get("kind"), values.Get("cursor")
	if values.Has("limit") {
		o.Limit, e = strconv.Atoi(values.Get("limit"))
		if e != nil || o.Limit < 1 {
			return o, &query.Error{Reason: "invalid_page_size"}
		}
	}
	return o, nil
}
func (h *Handler) search(w http.ResponseWriter, r *http.Request, token string) {
	o, e := searchParameters(r)
	if e != nil {
		failure(w, r, e)
		return
	}
	data, e := h.Core.Search(r.Context(), token, o)
	if e != nil {
		searchFailure(w, r, e)
		return
	}

	respond(w, r, 200, data)
}

func searchFailure(w http.ResponseWriter, r *http.Request, e error) {
	var unavailable *query.SearchUnavailable
	if errors.As(e, &unavailable) {
		details := map[string]any{}
		if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
			details["reason"] = "query_timeout"
		}
		w.Header().Set("Retry-After", "2")
		failureCode(w, r, 503, "SERVICE_RECOVERING", details, true)
		return
	}
	failure(w, r, e)
}
