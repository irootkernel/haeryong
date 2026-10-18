// Package renderer serves one loopback renderer origin: authored version
// bytes verified before consumption, the automatic loader derivative, the
// one-use authenticated handoff and the narrow per-delivery cookie. It
// never hosts the management API and never enters management
// allowed_origins; ordinary requests are same-origin only, with same-site
// cross-port initiators rejected because loopback ports share cookies.
package renderer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"haeryong.local/haeryong/internal/application/render"
	"haeryong.local/haeryong/internal/domain"
	"haeryong.local/haeryong/internal/webassets"
)

// Handler serves exactly one renderer origin bound to one immutable version.
type Handler struct {
	Core    *render.Service
	Origin  string
	Binding render.Binding
	// PublicReaderBase supplies the trusted management browser origin for
	// the unprivileged top-level redirect document. Nil keeps ordinary
	// delivery for every request.
	PublicReaderBase func() string
}

// New builds the per-origin HTTP server product wiring hands to the render
// service's lazy listener lifecycle.
func New(core *render.Service, origin string, binding render.Binding) *Server {
	handler := &Handler{Core: core, Origin: origin, Binding: binding}
	server := &Server{handler: handler}
	server.Server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.mu.Lock()
		if server.closed {
			server.mu.Unlock()
			http.Error(w, "renderer stopped", http.StatusServiceUnavailable)
			return
		}
		server.requests.Add(1)
		server.mu.Unlock()
		defer server.requests.Done()
		handler.ServeHTTP(w, r)
	})}
	return server
}

// Server closes sockets and drains every handler before storage is released.
// http.Server.Close alone does not wait for active handlers to return.
type Server struct {
	*http.Server
	handler  *Handler
	mu       sync.Mutex
	closed   bool
	requests sync.WaitGroup
}

// SetPublicReaderBase supplies the trusted management browser origin used by
// the unprivileged top-level redirect document.
func (s *Server) SetPublicReaderBase(base func() string) { s.handler.PublicReaderBase = base }

func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	err := s.Server.Close()
	s.requests.Wait()
	return err
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Exact origin binding: Host names this renderer origin only.
	if r.Host == "" || "http://"+r.Host != h.Origin {
		http.Error(w, "unknown origin", http.StatusNotFound)
		return
	}
	// No management handlers are mounted here. Authored directories named
	// api or __haeryong remain ordinary payload paths.
	if r.URL.Path == "/__haeryong/handoff" && r.Method == http.MethodPost {
		h.handoff(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "read only", http.StatusMethodNotAllowed)
		return
	}
	// A proven top-level navigation (Sec-Fetch-Dest=document + navigate), or
	// the conservative headerless missing-cookie navigation-shaped fallback,
	// receives the small daemon-owned UNPRIVILEGED redirect document instead
	// of delivery: the document reads its own exact location and moves the
	// tab to the credential-free public management browser Reader route.
	if h.topLevelNavigation(r) && h.redirectDocument(w, r) {
		return
	}
	// Ordinary content requests are same-origin only. Cookies share a
	// hostname across ports, so a same-site cross-port initiator (another
	// renderer origin or the trusted host) is rejected even when this
	// origin's cookie is attached; only the one-use authenticated handoff is
	// cross-port.
	supplied := r.Header.Get("Origin")
	site := r.Header.Get("Sec-Fetch-Site")
	if (supplied != "" && supplied != h.Origin) || (supplied == "" && site != "same-origin" && site != "none") {
		http.Error(w, "same-origin delivery context required", http.StatusForbidden)
		return
	}
	delivery := h.authorize(w, r)
	if delivery == nil {
		return
	}
	if r.URL.Path == "/__haeryong/loader.js" && r.URL.Query().Get("__haeryong_loader") == render.RendererVersion {
		h.loader(w, r, delivery)
		return
	}
	h.serve(w, r, delivery)
}

// topLevelNavigation identifies a proven top-level document navigation, or
// the conservative fallback for headerless navigation-shaped requests with
// no delivery cookie (authenticated ordinary HTML fetches stay unchanged).
func (h *Handler) topLevelNavigation(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	dest, mode := r.Header.Get("Sec-Fetch-Dest"), r.Header.Get("Sec-Fetch-Mode")
	if dest != "" || mode != "" {
		return dest == "document" && mode == "navigate"
	}
	if !strings.Contains(r.Header.Get("Accept"), "text/html") {
		return false
	}
	for _, cookie := range r.Cookies() {
		if strings.HasPrefix(cookie.Name, "hyr_view_"+h.Binding.VersionID+"_") {
			return false
		}
	}
	return true
}

// redirectDocument serves the daemon-owned unprivileged redirect page for a
// top-level navigation of an HTML entry. Non-HTML targets keep ordinary
// delivery. The page composes the public Reader route from its own exact
// location: the authored query string and fragment never pass through the
// server, and page_query/page_fragment names are never reinterpreted as
// host parameters — they may be legitimate artifact query names.
func (h *Handler) redirectDocument(w http.ResponseWriter, r *http.Request) bool {
	if h.PublicReaderBase == nil {
		return false
	}
	base := h.PublicReaderBase()
	if base == "" {
		return false
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	content, err := h.Core.ResolveVersionContent(r.Context(), h.Binding.VersionID, name)
	if err != nil || content.Kind != "file" || !content.HTML {
		return false
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return true
	}
	// Canonical identity from the verified stored metadata: the resolved
	// page path (exact, NFC/symlink resolved) and its document, never the
	// request's percent-encoded spelling.
	route, err := json.Marshal(base + "/documents/" + content.DocumentID + "/versions/" + h.Binding.VersionID)
	if err != nil {
		http.Error(w, "reader route unavailable", http.StatusServiceUnavailable)
		return true
	}
	page, err := json.Marshal(content.Path)
	if err != nil {
		http.Error(w, "reader route unavailable", http.StatusServiceUnavailable)
		return true
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The page owns its location: the raw artifact query moves to the host
	// page_query parameter exactly once, and the artifact hash moves to
	// page_fragment once. No parameter name is parsed or reinterpreted.
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Haeryong Reader</title><script>`+
		`((base,page)=>{const q=new URLSearchParams();q.set("page",page);`+
		`if(location.search.length>1)q.set("page_query",location.search.slice(1));`+
		`if(location.hash.length>1)q.set("page_fragment",location.hash.slice(1));`+
		`location.replace(base+"?"+q.toString());})(%s,%s)</script>`, route, page)
	return true
}

// authorize validates the narrow per-delivery cookie and the current owner
// authority. A definitively invalid or expired cookie is cleared; a
// transient owner check is retryable.
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) *render.DeliveryView {
	prefix := "hyr_view_" + h.Binding.VersionID + "_"
	for _, cookie := range r.Cookies() {
		if !strings.HasPrefix(cookie.Name, prefix) {
			continue
		}
		id, secret, ok := strings.Cut(cookie.Value, ".")
		if !ok {
			h.clearCookie(w, cookie.Name)
			continue
		}
		delivery, err := h.Core.AuthorizeDelivery(r.Context(), h.Binding.VersionID, h.Origin, id, secret)
		if err == nil {
			return delivery
		}
		var typed *render.Error
		if errors.As(err, &typed) && typed.Reason == render.ReasonTransientOwner {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "owner check retryable", http.StatusServiceUnavailable)
			return nil
		}
		// Definitive denial: the narrow cookie never authorized this
		// request, so it is cleared before the honest denial.
		h.clearCookie(w, cookie.Name)
	}
	http.Error(w, "delivery denied", http.StatusUnauthorized)
	return nil
}

func (h *Handler) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

// handoff consumes the one-use secret (the only cross-port allowance) and
// establishes the narrow delivery cookie, landing the frame on the target
// page through a client redirect.
func (h *Handler) handoff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "handoff is a form POST", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "handoff requires the one-use secret", http.StatusBadRequest)
		return
	}
	secret := r.PostFormValue("secret")
	if secret == "" {
		http.Error(w, "handoff requires the one-use secret", http.StatusBadRequest)
		return
	}
	delivery, err := h.Core.ConsumeHandoff(r.Context(), h.Binding.VersionID, h.Origin, secret)
	if err != nil {
		var typed *render.Error
		if errors.As(err, &typed) && typed.Reason == render.ReasonTransientOwner {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "owner check retryable", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "delivery denied", http.StatusUnauthorized)
		return
	}
	name, value, ok := h.Core.Cookie(delivery.ID)
	if !ok {
		http.Error(w, "delivery denied", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	target := render.SanitizeHandoffTarget(r.URL.Query().Get("page"))
	landing, err := json.Marshal(target)
	if err != nil {
		landing = []byte(`"/"`)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><title>Delivery established</title><script>location.replace(%s)</script>`, landing)
}

// loader serves the automatic integration loader with its strong validator.
func (h *Handler) loader(w http.ResponseWriter, r *http.Request, delivery *render.DeliveryView) {
	if err := h.Core.GuardDelivery(r.Context(), h.Binding.VersionID, delivery.ID); err != nil {
		h.fail(w, r, err)
		return
	}
	script, err := webassets.RendererLoader()
	if err != nil {
		http.Error(w, "renderer integration unavailable", http.StatusServiceUnavailable)
		return
	}
	digest := sha256.Sum256(script)
	etag := `"sha256-` + hex.EncodeToString(digest[:]) + `"`
	w.Header().Set("ETag", etag)
	if matchNone(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(script)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(script)
}

// serve streams one resolved entry. HTML pages are served as the loader
// derivative keyed by (content digest, exact page path, renderer version);
// every file is the same verified pinned handle the guard rechecks around
// each write. Ranges and If-Range follow the trusted file-delivery
// discipline, including the explicit empty-file 416.
func (h *Handler) serve(w http.ResponseWriter, r *http.Request, delivery *render.DeliveryView) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	content, err := h.Core.ResolveVersionContent(r.Context(), h.Binding.VersionID, name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if content.Kind != "file" {
		if err := h.Core.GuardDelivery(r.Context(), h.Binding.VersionID, delivery.ID); err != nil {
			h.fail(w, r, err)
			return
		}
		h.metadata(w, r, content)
		return
	}
	writer := &protectedWriter{ResponseWriter: w, check: func() error {
		return h.Core.GuardDelivery(r.Context(), h.Binding.VersionID, delivery.ID)
	}}
	err = h.Core.ReadVersionContent(r.Context(), delivery.ID, h.Binding.VersionID, content, func(handle io.ReadSeeker) error {
		media := contentMediaType(content.Path)
		etag := "\"sha256-" + content.Hash + "\""
		representation := handle
		integrated := false
		if content.HTML {
			// The delivered page is a derivative; stored/exported bytes are
			// never rewritten. Derivative identity = content digest + exact
			// page path + renderer version. A malformed encoding prefix
			// serves the raw original bytes honestly with only the
			// derivative marker omitted.
			var derivative render.Derivative
			var err error
			derivative, err = render.LoaderPage(handle, content.Size, content.Hash, h.Binding.VersionID, content.DocumentID, content.Path)
			if err != nil {
				return err
			}
			representation, etag, media, integrated = derivative.Body, derivative.ETag, derivative.MIME, derivative.Integrate
			if integrated {
				w.Header().Set("Haeryong-Derivative", render.RendererVersion)
			}
			w.Header().Set("Haeryong-Content-Digest", content.Hash)
			w.Header().Set("Haeryong-Payload-Digest", h.Binding.PayloadSHA256)
			// Path headers are percent-encoded exactly once, like the v1 file
			// handler: Go's header serialization would otherwise replace raw
			// CR/LF with spaces and corrupt exact page identity on the wire.
			w.Header().Set("Haeryong-Page-Path", url.QueryEscape(content.Path))
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Type", media)
		w.Header().Set("Haeryong-Resolved-Path", url.QueryEscape(content.Path))
		if !integrated && content.Object.Size == 0 && r.Header.Get("Range") != "" && (r.Header.Get("If-Range") == "" || r.Header.Get("If-Range") == etag) && matchIfMatch(r.Header.Get("If-Match"), etag) && !matchNone(r.Header.Get("If-None-Match"), etag) {
			w.Header().Set("Content-Range", "bytes */0")
			writer.status = http.StatusRequestedRangeNotSatisfiable
			return nil
		}
		http.ServeContent(writer, r, content.Path, time.Time{}, representation)
		return writer.err
	})
	if err != nil {
		if writer.started {
			panic(http.ErrAbortHandler)
		}
		clearContentHeaders(w)
		h.fail(w, r, err)
		return
	}
	if writer.status >= 400 {
		clearContentHeaders(w)
		if err := writer.check(); err != nil {
			h.fail(w, r, err)
			return
		}
		http.Error(w, http.StatusText(writer.status), writer.status)
	}
}

// metadata answers a directory or symlink request with typed JSON metadata,
// never a filesystem listing outside the version.
func (h *Handler) metadata(w http.ResponseWriter, r *http.Request, content render.ResolvedContent) {
	body, err := json.Marshal(map[string]any{"path": content.Path, "kind": content.Kind})
	if err != nil {
		http.Error(w, "metadata error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// fail maps typed renderer conditions to honest plain responses; renderer
// origins never speak the management error registry.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var typed *render.Error
	if errors.As(err, &typed) {
		switch typed.Reason {
		case render.ReasonTransientOwner:
			w.Header().Set("Retry-After", "1")
			http.Error(w, "owner check retryable", http.StatusServiceUnavailable)
			return
		case render.ReasonDeniedDelivery, render.ReasonClosedSession:
			http.Error(w, "delivery denied", http.StatusUnauthorized)
			return
		case "unresolved_link":
			http.Error(w, "unresolved link", http.StatusNotFound)
			return
		case "ambiguous_path", "invalid_path":
			http.Error(w, typed.Reason, http.StatusBadRequest)
			return
		}
	}
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, domain.ErrPrecondition) {
		http.Error(w, "precondition failed", http.StatusPreconditionFailed)
		return
	}
	if errors.Is(err, domain.ErrContentIntegrity) {
		http.Error(w, "content integrity error", http.StatusServiceUnavailable)
		return
	}
	http.Error(w, "renderer error", http.StatusServiceUnavailable)
}

func contentMediaType(name string) string {
	if strings.EqualFold(path.Ext(name), ".html") || strings.EqualFold(path.Ext(name), ".htm") {
		return "text/html; charset=utf-8"
	}
	if media := mime.TypeByExtension(path.Ext(name)); media != "" {
		// Authored CSS/classic scripts keep browser encoding fallback.
		// Modules decode as UTF-8; our own loader stays explicitly UTF-8.
		kind, _, err := mime.ParseMediaType(media)
		if err == nil {
			switch kind {
			case "text/css", "text/javascript", "application/javascript", "text/ecmascript", "application/ecmascript":
				return kind
			}
		}
		return media
	}
	return "application/octet-stream"
}

func matchNone(header, etag string) bool {
	for _, value := range strings.Split(header, ",") {
		value = strings.TrimSpace(value)
		if value == "*" || strings.TrimPrefix(value, "W/") == etag {
			return true
		}
	}
	return false
}

func matchIfMatch(header, etag string) bool {
	if header == "" {
		return true
	}
	for _, value := range strings.Split(header, ",") {
		value = strings.TrimSpace(value)
		if value == "*" || value == etag {
			return true
		}
	}
	return false
}
