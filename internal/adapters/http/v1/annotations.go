package v1

import (
	"encoding/json"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"io"
	"net/http"
	"strconv"
)

func noteRoute(p []string) bool {
	return len(p) == 3 && ((p[0] == "versions" && p[2] == "annotations") || (p[0] == "documents" && p[2] == "revisit")) || p[0] == "annotations" && (len(p) == 2 || len(p) == 3 && (p[2] == "history" || p[2] == "body" || p[2] == "context"))
}
func noteMethod(m string, p []string) bool {
	if m == "GET" || m == "HEAD" {
		return true
	}
	if p[0] == "versions" {
		return m == "POST"
	}
	if p[0] == "documents" {
		return m == "PUT"
	}
	return len(p) == 2 && (m == "PATCH" || m == "DELETE")
}
func (h *Handler) notes(w http.ResponseWriter, r *http.Request, token string, p []string) {
	allowed := []string{}
	if p[0] == "versions" || len(p) == 3 && p[2] == "history" {
		if r.Method == "GET" || r.Method == "HEAD" {
			allowed = []string{"cursor", "limit"}
		}
	}
	if p[0] == "annotations" && len(p) == 3 && p[2] != "history" {
		allowed = []string{"revision"}
	}
	values, err := parameters(r, allowed)
	if err != nil {
		failure(w, r, err)
		return
	}
	o := query.Options{Cursor: values.Get("cursor")}
	if values.Has("limit") {
		o.Limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil || o.Limit < 1 {
			failure(w, r, sec.ErrInvalid)
			return
		}
	}
	if p[0] == "annotations" && len(p) == 3 && (p[2] == "body" || p[2] == "context") {
		revision, e := strconv.ParseInt(values.Get("revision"), 10, 64)
		if e != nil || revision < 1 {
			failure(w, r, sec.ErrInvalid)
			return
		}
		started := false
		err = h.Core.ReadAnnotationObject(r.Context(), token, p[1], revision, p[2], func(obj domain.Object, media string, reader io.ReadSeeker) error {
			guard := func() error {
				_, _, err := h.Core.AnnotationObject(r.Context(), token, p[1], revision, p[2])
				return err
			}
			if err := guard(); err != nil {
				return err
			}
			w.Header().Set("Content-Type", media)
			w.Header().Set("Content-Disposition", "attachment")
			w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
			w.Header().Set("ETag", `"sha256-`+obj.Hash+`"`)
			w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
			sink := &deliveryWriter{ResponseWriter: w, head: r.Method == "HEAD", check: guard}
			sink.WriteHeader(200)
			if sink.err == nil && r.Method != "HEAD" {
				_, err = io.Copy(sink, reader)
			}
			started = sink.started
			if sink.err != nil {
				return sink.err
			}
			return err
		})
		if err != nil {
			if started {
				panic(http.ErrAbortHandler)
			}
			w.Header().Del("Content-Length")
			failure(w, r, err)
		}
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		key, e := personalOperationKey(r)
		if e != nil {
			failure(w, r, e)
			return
		}
		etag := ""
		create := false
		if r.Method != "POST" {
			etag, create, e = personalConditions(r)
			if e != nil {
				failure(w, r, e)
				return
			}
			if p[0] == "annotations" && create {
				failure(w, r, sec.ErrInvalid)
				return
			}
		}
		body, e := io.ReadAll(r.Body)
		if e != nil {
			failure(w, r, e)
			return
		}
		var result domain.Response
		if p[0] == "documents" {
			result, err = h.Core.RevisitPut(r.Context(), token, key, p[1], etag, create, body)
		} else {
			result, err = h.Core.AnnotationMutate(r.Context(), token, key, r.Method, p[1], etag, body)
		}
		if err != nil {
			failure(w, r, err)
			return
		}
		w.Header().Set("ETag", result.Headers["ETag"])
		respond(w, r, result.Status, json.RawMessage(result.Body))
		return
	}
	var data any
	switch {
	case p[0] == "documents":
		var view query.RevisitView
		view, err = h.Core.RevisitGet(r.Context(), token, p[1])
		data = view
		if view.ETag != nil {
			w.Header().Set("ETag", *view.ETag)
		}
	case p[0] == "versions":
		data, err = h.Core.AnnotationList(r.Context(), token, p[1], o)
	case len(p) == 3:
		data, err = h.Core.AnnotationHistory(r.Context(), token, p[1], o)
	default:
		var n query.AnnotationView
		n, err = h.Core.AnnotationGet(r.Context(), token, p[1])
		data = n
		w.Header().Set("ETag", n.ETag)
	}
	if err != nil {
		failure(w, r, err)
		return
	}
	respond(w, r, 200, data)
}
