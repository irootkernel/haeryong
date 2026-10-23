package v1

import (
	"encoding/json"
	"haeryong.local/haeryong/internal/application/query"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

func connectedRoute(p []string) bool {
	return len(p) >= 3 && p[0] == "versions" && (p[2] == "backlinks" && len(p) == 3 || p[2] == "indexes" && len(p) == 4 && (p[3] == "validate" || p[3] == "rebuild"))
}
func (h *Handler) connected(w http.ResponseWriter, r *http.Request, token string, p []string) {
	invalid := func(pointer, reason string) { failure(w, r, &query.ReferenceInvalid{Pointer: pointer, Reason: reason}) }
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		invalid("/query", "invalid_query")
		return
	}
	allowed := map[string]bool{"kind": true}
	if p[2] == "backlinks" {
		allowed = map[string]bool{"page_path": true, "region_id": true, "cursor": true, "limit": true}
	} else if p[3] == "rebuild" {
		allowed = map[string]bool{}
	}
	for key, v := range values {
		if !allowed[key] || len(v) != 1 || v[0] == "" {
			invalid("/query", "unknown_duplicate_or_empty_query")
			return
		}
	}
	var data any
	if p[2] == "backlinks" {
		o := query.BacklinkOptions{Options: query.Options{Cursor: values.Get("cursor")}}
		if values.Has("page_path") {
			v := values.Get("page_path")
			o.Page = &v
		}
		if values.Has("region_id") {
			v := values.Get("region_id")
			o.Region = &v
		}
		if values.Has("limit") {
			o.Limit, err = strconv.Atoi(values.Get("limit"))
			if err != nil || o.Limit < 1 || o.Limit > 200 {
				invalid("/limit", "invalid_page_size")
				return
			}
		}
		data, err = h.Core.Backlinks(r.Context(), token, p[1], o)
	} else if p[3] == "validate" {
		data, err = h.Core.ValidateVersionIndexes(r.Context(), token, p[1], values.Get("kind"))
	} else {
		key, e := personalOperationKey(r)
		if e != nil {
			failure(w, r, e)
			return
		}
		var body struct {
			Kind string `json:"kind"`
		}
		d := json.NewDecoder(r.Body)
		opening, e := d.Token()
		if e != nil || opening != json.Delim('{') {
			invalid("/body", "invalid_rebuild_request")
			return
		}
		field, e := d.Token()
		if e != nil || field != "kind" || d.Decode(&body.Kind) != nil {
			invalid("/body", "invalid_rebuild_request")
			return
		}
		closing, e := d.Token()
		if e != nil || closing != json.Delim('}') || d.Decode(new(any)) != io.EOF {
			invalid("/body", "invalid_rebuild_request")
			return
		}

		response, e := h.Core.RebuildVersionIndexes(r.Context(), token, p[1], key, body.Kind)
		if e != nil {
			failure(w, r, e)
			return
		}
		respond(w, r, response.Status, json.RawMessage(response.Body))
		return
	}
	if err != nil {
		failure(w, r, err)
		return
	}
	if err = r.Context().Err(); err != nil {
		failure(w, r, &query.ReferenceUnavailable{Cause: err})
		return
	}
	respond(w, r, 200, data)
}
