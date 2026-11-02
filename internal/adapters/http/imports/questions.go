package imports

import (
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"haeryong.local/haeryong/internal/application/generation"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/questions"
	"haeryong.local/haeryong/internal/domain"
	"haeryong.local/haeryong/internal/security/session"
)

func NewQuestions(core *importer.Service, gen *generation.Service, ask *questions.Service) http.Handler {
	return &handler{core: core, generation: gen, questions: ask}
}
func (h *handler) questionPath(path string) bool {
	if h.questions == nil {
		return false
	}
	parts := strings.Split(path, "/")
	return len(parts) == 3 && parts[0] == "versions" && parts[2] == "questions" || parts[0] == "questions" || parts[0] == "answers"
}
func (h *handler) questionRoutes(w http.ResponseWriter, r *http.Request, token, path string, parts []string, q url.Values) bool {
	if !h.questionPath(path) {
		return false
	}
	if len(parts) == 3 && parts[0] == "versions" && parts[2] == "questions" && r.Method == http.MethodPost {
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
		input, e := questions.DecodeRequest(r.Body)
		if e != nil {
			fail(w, e)
			return true
		}
		response, e := h.questions.Start(r.Context(), token, key, parts[1], input)
		durable(w, response, e)
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		fail(w, domain.ErrNotFound)
		return true
	}
	if len(parts) == 5 && parts[0] == "answers" && parts[2] == "citations" && parts[4] == "source" {
		index, e := strconv.Atoi(parts[3])
		if !only(q) || e != nil || index < 0 || strconv.Itoa(index) != parts[3] {
			fail(w, session.ErrInvalid)
			return true
		}
		started := false
		e = h.questions.ReadCitation(r.Context(), token, parts[1], index, func(row domain.SourceCaptureRead, reader io.Reader) error {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", "attachment")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Length", strconv.FormatInt(row.Size, 10))
			w.Header().Set("ETag", `"sha256-`+row.Hash+`"`)
			started = true
			if r.Method == http.MethodHead {
				w.WriteHeader(200)
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
		return true
	}
	if len(parts) == 3 && parts[0] == "questions" && parts[2] == "context" {
		if !only(q) {
			fail(w, session.ErrInvalid)
			return true
		}
		started := false
		e := h.questions.ReadContext(r.Context(), token, parts[1], func(context domain.QuestionContext, object domain.Object, reader io.Reader) error {
			w.Header().Set("Content-Type", context.MediaType)
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Length", strconv.FormatInt(object.Size, 10))
			w.Header().Set("ETag", `"sha256-`+object.Hash+`"`)
			started = true
			if r.Method == http.MethodHead {
				w.WriteHeader(200)
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
		return true
	}
	if len(parts) == 3 && (parts[0] == "answers" && parts[2] == "body" || parts[0] == "questions" && parts[2] == "text") {
		if !only(q) {
			fail(w, session.ErrInvalid)
			return true
		}
		started := false
		e := h.questions.ReadText(r.Context(), token, parts[1], parts[0] == "answers", func(object domain.Object, reader io.Reader) error {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("ETag", `"sha256-`+object.Hash+`"`)
			w.Header().Set("Content-Length", strconv.FormatInt(object.Size, 10))
			started = true
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
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
		return true
	}
	var value any
	var e error
	switch {
	case len(parts) == 3 && parts[0] == "versions" && parts[2] == "questions":
		if !only(q, "cursor") || q.Has("cursor") && q.Get("cursor") == "" {
			fail(w, session.ErrInvalid)
			return true
		}
		value, e = h.questions.Questions(r.Context(), token, parts[1], q.Get("cursor"))
	case len(parts) == 2 && parts[0] == "questions":
		if !only(q) {
			fail(w, session.ErrInvalid)
			return true
		}
		value, e = h.questions.Question(r.Context(), token, parts[1])
	case len(parts) == 3 && parts[0] == "questions" && parts[2] == "answers":
		if !only(q, "cursor") || q.Has("cursor") && q.Get("cursor") == "" {
			fail(w, session.ErrInvalid)
			return true
		}
		value, e = h.questions.Answers(r.Context(), token, parts[1], q.Get("cursor"))
	case len(parts) == 2 && parts[0] == "answers":
		if !only(q) {
			fail(w, session.ErrInvalid)
			return true
		}
		value, e = h.questions.Answer(r.Context(), token, parts[1])
	default:
		fail(w, domain.ErrNotFound)
		return true
	}
	if e != nil {
		fail(w, e)
		return true
	}
	e = h.core.Auth.Guard(r.Context(), token, func() error {
		if r.Method == http.MethodHead {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(200)
		} else {
			respond(w, 200, value)
		}
		return nil
	})
	if e != nil {
		fail(w, e)
	}
	return true
}
