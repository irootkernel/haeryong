package v1

import (
	"haeryong.local/haeryong/internal/application/query"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

func referenceRoute(p []string) bool {
	return len(p) >= 3 && p[0] == "versions" && p[2] == "references" && (len(p) == 3 || len(p) == 4 && p[3] == "resolve")
}
func (h *Handler) references(w http.ResponseWriter, r *http.Request, token string, p []string) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		failure(w, r, &query.ReferenceInvalid{Pointer: "/query", Reason: "invalid_query"})
		return
	}
	allowed := map[string]bool{"cursor": true, "limit": true}
	if len(p) == 4 {
		allowed = map[string]bool{"reference_id": true, "page_path": true, "region_id": true}
	}
	keys := []string{}
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !allowed[key] || len(values[key]) != 1 || values[key][0] == "" {
			pointer := "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
			failure(w, r, &query.ReferenceInvalid{Pointer: pointer, Reason: "unknown_duplicate_or_empty_query"})
			return
		}
	}
	var data any
	if len(p) == 3 {
		o := query.Options{Cursor: values.Get("cursor")}
		if values.Has("limit") {
			o.Limit, err = strconv.Atoi(values.Get("limit"))
			if err != nil || o.Limit < 1 || o.Limit > 200 {
				failure(w, r, &query.ReferenceInvalid{Pointer: "/limit", Reason: "invalid_page_size"})
				return
			}
		}
		data, err = h.Core.References(r.Context(), token, p[1], o)
	} else {
		o := query.ReferenceOptions{}
		if values.Has("page_path") {
			v := values.Get("page_path")
			o.Page = &v
		}
		if values.Has("region_id") {
			v := values.Get("region_id")
			o.Region = &v
		}
		data, err = h.Core.ResolveReference(r.Context(), token, p[1], values.Get("reference_id"), o)
	}
	if err != nil {
		failure(w, r, err)
		return
	}
	if err = r.Context().Err(); err != nil {
		failure(w, r, &query.ReferenceUnavailable{Cause: err})
		return
	}
	respond(w, r, http.StatusOK, data)
}
