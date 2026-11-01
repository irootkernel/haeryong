package imports

import (
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	generation "haeryong.local/haeryong/internal/application/generation"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/domain"
	"haeryong.local/haeryong/internal/security/session"
)

// NewGeneration delivers the existing imports and the Task04 generation/draft
// routes. Read-only/status clients do not gain generation by adapter presence.
var draftValidator = regexp.MustCompile(`^"draft-[0-9a-f]{64}"$`)

func NewGeneration(core *importer.Service, gen *generation.Service) http.Handler {
	return &handler{core: core, generation: gen}
}
func (h *handler) generationRoutes(w http.ResponseWriter, r *http.Request, token, path string, parts []string, q url.Values) bool {
	if h.generation == nil || path != "generations" && path != "drafts" && !strings.HasPrefix(path, "drafts/") {
		return false
	}
	isRead := r.Method == http.MethodGet || r.Method == http.MethodHead
	if isRead && parts[0] == "drafts" {
		var value any
		var err error
		switch {
		case len(parts) == 1:
			if !only(q, "project_id", "cursor") || q.Has("project_id") && q.Get("project_id") == "" || q.Has("cursor") && q.Get("cursor") == "" {
				fail(w, session.ErrInvalid)
				return true
			}
			value, err = h.core.DraftPage(r.Context(), token, q.Get("project_id"), q.Get("cursor"))
		case len(parts) == 2:
			if !only(q) {
				fail(w, session.ErrInvalid)
				return true
			}
			var draft importer.DraftView
			draft, err = h.core.Draft(r.Context(), token, parts[1])
			value = draft
			if err == nil {
				w.Header().Set("ETag", draft.ETag)
			}
		case len(parts) == 3 && parts[2] == "history":
			if !only(q, "cursor") || q.Has("cursor") && q.Get("cursor") == "" {
				fail(w, session.ErrInvalid)
				return true
			}
			value, err = h.core.DraftHistoryPage(r.Context(), token, parts[1], q.Get("cursor"))
		default:
			fail(w, domain.ErrNotFound)
			return true
		}
		if err != nil {
			fail(w, err)
			return true
		}
		err = h.core.Auth.Guard(r.Context(), token, func() error {
			if r.Method == http.MethodHead {
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(200)
			} else {
				respond(w, 200, value)
			}
			return nil
		})
		if err != nil {
			fail(w, err)
		}
		return true
	}
	mutation := path == "generations" && r.Method == http.MethodPost || len(parts) == 2 && parts[0] == "drafts" && r.Method == http.MethodPatch || len(parts) == 3 && parts[0] == "drafts" && parts[2] == "publish" && r.Method == http.MethodPost
	if !mutation {
		fail(w, domain.ErrNotFound)
		return true
	}
	if !only(q) {
		fail(w, session.ErrInvalid)
		return true
	}
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 {
		fail(w, session.ErrInvalid)
		return true
	}
	key, e := operationKey(r, false)
	if e != nil {
		fail(w, e)
		return true
	}
	if path == "generations" {
		input, e := generation.DecodeRequest(r.Body)
		if e != nil {
			fail(w, e)
			return true
		}
		response, e := h.generation.Start(r.Context(), token, key, input)
		durable(w, response, e)
		return true
	}
	etag, e := singleHeader(r, "If-Match", true)
	if e != nil {
		if len(r.Header.Values("If-Match")) == 0 {
			e = session.ErrMissing
		}
		fail(w, e)
		return true
	}
	if !draftValidator.MatchString(etag) {
		fail(w, session.ErrInvalid)
		return true
	}
	if len(parts) == 3 {
		var empty struct{}
		if e = closedJSON(r.Body, &empty, nil); e != nil {
			fail(w, e)
			return true
		}
		response, e := h.core.PublishDraft(r.Context(), token, key, parts[1], etag)
		durable(w, response, e)
		return true
	}
	var input struct {
		Action   string              `json:"action"`
		Upload   *string             `json:"upload_id"`
		Metadata *importer.ObjectRef `json:"metadata_ref"`
	}
	if e = closedJSON(r.Body, &input, []string{"action"}); e != nil {
		fail(w, e)
		return true
	}
	switch input.Action {
	case "reject":
		if input.Upload != nil || input.Metadata != nil {
			fail(w, session.ErrInvalid)
			return true
		}
		response, e := h.core.RejectDraft(r.Context(), token, key, parts[1], etag)
		durable(w, response, e)
	case "replace":
		if input.Upload == nil {
			fail(w, session.ErrInvalid)
			return true
		}
		response, e := h.core.ReplaceDraft(r.Context(), token, key, parts[1], etag, *input.Upload, input.Metadata)
		durable(w, response, e)
	default:
		fail(w, session.ErrInvalid)
	}
	return true
}
