package scans

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"haeryong.local/haeryong/internal/domain"
	"haeryong.local/haeryong/internal/security/session"
)

func (h *handler) decision(w http.ResponseWriter, r *http.Request, token string, p []string) {
	media, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || media != "application/json" || r.URL.RawQuery != "" {
		fail(w, r, session.ErrInvalid)
		return
	}
	key, e := operationKey(r)
	if e != nil {
		fail(w, r, e)
		return
	}
	tags := r.Header.Values("If-Match")
	if len(tags) == 0 {
		fail(w, r, missingHeader("If-Match"))
		return
	}
	if len(tags) != 1 || !strings.HasPrefix(tags[0], "\"") || !strings.HasSuffix(tags[0], "\"") || strings.Contains(tags[0], ",") {
		fail(w, r, session.ErrInvalid)
		return
	}
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 65536))
	if e != nil {
		fail(w, r, session.ErrInvalid)
		return
	}
	// The shared strict object decoder rejects duplicate and unknown keys.
	var result domain.Response
	switch p[0] {
	case "findings":
		var in struct {
			Decision *string `json:"decision"`
		}
		if e = decodeDecision(raw, &in); e == nil && in.Decision != nil {
			result, e = h.core.FindingAction(r.Context(), token, key, p[1], tags[0], *in.Decision, raw)
		} else {
			e = session.ErrInvalid
		}
	case "topic-assignments":
		var in struct {
			Pinned   *bool   `json:"pinned"`
			Decision *string `json:"decision"`
		}
		if e = decodeDecision(raw, &in); e == nil && in.Pinned != nil && in.Decision != nil {
			result, e = h.core.EffectAction(r.Context(), token, key, p[1], tags[0], in.Pinned, *in.Decision, raw)
		} else {
			e = session.ErrInvalid
		}
	case "overlays":
		var in struct{}
		if e = decodeDecision(raw, &in); e == nil {
			result, e = h.core.EffectAction(r.Context(), token, key, p[1], tags[0], nil, "undone", raw)
		}
	}
	if e != nil {
		fail(w, r, e)
		return
	}
	for k, v := range result.Headers {
		if k == "ETag" || k == "Location" {
			w.Header().Set(k, v)
		}
	}
	if result.Status >= 400 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(result.Status)
		_, _ = w.Write(result.Body)
		return
	}
	respond(w, r, result.Status, json.RawMessage(result.Body))
}
