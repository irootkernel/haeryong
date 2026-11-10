package v1

import (
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/domain"
	"net/http"
	"strconv"
)

func managementRoute(p []string) bool {
	return len(p) == 1 && p[0] == "topic-assignments" || len(p) == 3 && p[0] == "versions" && p[2] == "related"
}
func (h *Handler) management(w http.ResponseWriter, r *http.Request, token string, p []string) {
	v, e := parameters(r, []string{"document_id", "version_id", "page_path", "region_id", "cursor", "limit"})
	if e != nil {
		failure(w, r, e)
		return
	}
	f := domain.ManagementFilter{Kind: "topic_assignment", DocumentID: v.Get("document_id"), VersionID: v.Get("version_id")}
	if len(p) == 3 {
		if v.Has("document_id") || v.Has("version_id") {
			failure(w, r, domain.ErrInvalidTarget)
			return
		}
		f.Kind = "related_explanation"
		f.VersionID = p[1]
	}
	if v.Has("page_path") {
		a := v.Get("page_path")
		f.PagePath = &a
	}
	if v.Has("region_id") {
		a := v.Get("region_id")
		f.RegionID = &a
	}
	o := query.Options{Cursor: v.Get("cursor")}
	if v.Has("limit") {
		o.Limit, e = parseManagementLimit(v.Get("limit"))
		if e != nil {
			failure(w, r, e)
			return
		}
	}
	data, e := h.Core.Management(r.Context(), token, f, o)
	if e != nil {
		failure(w, r, e)
		return
	}
	etag := "\"management-" + domain.ManagementDigest(data) + "\""
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	respond(w, r, http.StatusOK, data)
}

func parseManagementLimit(s string) (int, error) {
	n, e := strconv.Atoi(s)
	if e != nil || n < 1 || n > 200 {
		return 0, domain.ErrInvalidTarget
	}
	return n, nil
}
