package source

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"

	core "haeryong.local/haeryong/internal/application/source"
	"haeryong.local/haeryong/internal/domain"
	"haeryong.local/haeryong/internal/security/session"
)

func captureRoute(r *http.Request) bool {
	p := route(r)
	if len(p) == 3 && p[0] == "projects" && p[2] == "source-reads" {
		return r.Method == http.MethodGet || r.Method == http.MethodHead
	}
	if len(p) == 3 && p[0] == "projects" && p[2] == "source-captures" {
		return r.Method == http.MethodPost
	}
	if len(p) >= 2 && (p[0] == "source-captures" || p[0] == "source-reads") {
		return r.Method == http.MethodGet || r.Method == http.MethodHead
	}
	return false
}
func (h *handler) capture(w http.ResponseWriter, r *http.Request, token string) {
	p := route(r)
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		fail(w, session.ErrInvalid)
		return
	}
	if len(p) == 3 && p[0] == "projects" && p[2] == "source-reads" {
		if len(q["source_id"]) != 1 || q.Get("source_id") == "" || len(q["cursor"]) > 1 || len(q) > 2 || len(q) == 2 && len(q["cursor"]) != 1 {
			fail(w, session.ErrInvalid)
			return
		}
		out, e := h.core.RetainedReads(r.Context(), token, p[1], q.Get("source_id"), q.Get("cursor"))
		if e != nil {
			fail(w, e)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "private, no-store")
		if r.Method != http.MethodHead {
			json.NewEncoder(w).Encode(map[string]any{"data": out, "error": nil})
		}
		return
	}
	if len(p) == 3 && p[0] == "projects" {
		if len(q) != 0 {
			fail(w, session.ErrInvalid)
			return
		}
		key, e := operationKey(r)
		if e != nil {
			fail(w, e)
			return
		}
		media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || media != "application/json" {
			fail(w, session.ErrInvalid)
			return
		}
		var input core.CaptureRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if e = decoder.Decode(&input); e != nil {
			fail(w, session.ErrInvalid)
			return
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			fail(w, session.ErrInvalid)
			return
		}
		response, e := h.core.Capture(r.Context(), token, key, p[1], input)
		durable(w, response, e)
		return
	}
	if len(p) == 2 && p[0] == "source-reads" {
		if len(q) != 0 {
			fail(w, session.ErrInvalid)
			return
		}
		started := false
		e := h.core.ReadCaptured(r.Context(), token, p[1], func(row domain.SourceCaptureRead, reader io.ReadSeeker) error {
			var location struct {
				Path string `json:"path"`
			}
			if e := json.Unmarshal(row.LocationJSON, &location); e != nil {
				return e
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(location.Path)}))
			w.Header().Set("ETag", `"`+row.Hash+`"`)
			w.Header().Set("Cache-Control", "private, no-store")
			started = true
			if r.Method == http.MethodHead {
				return nil
			}
			_, e := io.Copy(w, reader)
			return e
		})
		if e != nil {
			if started {
				panic(http.ErrAbortHandler)
			}
			fail(w, e)
		}
		return
	}
	if len(p) >= 2 && p[0] == "source-captures" {
		if len(p) == 3 && p[2] == "diff" {
			if len(q["other"]) != 1 || q.Get("other") == "" || len(q) > 2 || len(q["cursor"]) > 1 || (len(q) == 2 && len(q["cursor"]) != 1) {
				fail(w, session.ErrInvalid)
				return
			}
			out, e := h.core.CompareCaptures(r.Context(), token, p[1], q.Get("other"), q.Get("cursor"))
			if e != nil {
				fail(w, e)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"data": out, "error": nil})
			return
		}
		if len(p) > 3 || len(p) == 3 && p[2] != "reads" {
			fail(w, domain.ErrNotFound)
			return
		}
		for k, v := range q {
			if k != "cursor" || len(v) != 1 {
				fail(w, session.ErrInvalid)
				return
			}
		}
		out, e := h.core.CaptureView(r.Context(), token, p[1], q.Get("cursor"))
		if e != nil {
			fail(w, e)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "private, no-store")
		if r.Method != http.MethodHead {
			json.NewEncoder(w).Encode(map[string]any{"data": out, "error": nil})
		}
		return
	}
	fail(w, domain.ErrNotFound)
}
