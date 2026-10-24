// Package v1 adapts the current query and exact-byte APIs. Authored files are
// attachments, never executable documents at the trusted management origin.
package v1

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/render"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Handler struct {
	Core   *query.Service
	Render *render.Lazy
	// Endpoints supplies the trusted runtime origins from product wiring:
	// the actual local management origin and the configured external origin
	// (empty when remote ingress is disabled). A nil closure classifies
	// every request as local; requests reaching this handler have already
	// passed the auth adapter's exact Host/Origin policy.
	Endpoints func() (local, external string)
}

func New(core *query.Service, renderer *render.Lazy, endpoints func() (local, external string)) *Handler {
	return &Handler{Core: core, Render: renderer, Endpoints: endpoints}
}

// ingressRemote classifies the trusted ingress endpoint of a request from
// exact runtime wiring, never an inferred client location: with a
// configured external origin, a request is remote when its supplied Origin
// is the external origin, or its Host is the external host while differing
// from the actual local management host (the local endpoint wins ties;
// construction refuses ambiguous equal hosts). An absent Origin never
// proves locality, and no Forwarded or User-Agent signal is consulted.
func (h *Handler) ingressRemote(r *http.Request) bool {
	if h.Endpoints == nil {
		return false
	}
	local, external := h.Endpoints()
	if external == "" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin == external {
		return true
	}
	externalURL, err := url.Parse(external)
	if err != nil || externalURL.Host == "" || r.Host != externalURL.Host {
		return false
	}
	if local == "" {
		return true
	}
	localURL, localErr := url.Parse(local)
	if localErr != nil || localURL.Host == "" {
		return true
	}
	return localURL.Host != r.Host
}
func route(r *http.Request) []string {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSuffix(r.URL.EscapedPath(), "/"), "/api/v1/"), "/")
	for i, part := range parts {
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return nil
		}
		parts[i] = decoded
	}
	return parts
}
func (h *Handler) Handles(r *http.Request) bool {
	p := route(r)
	if len(p) == 0 {
		return false
	}
	if reviewRoute(p) {
		return r.Method == "GET" || r.Method == "HEAD" || len(p) == 3 && r.Method == "PUT"
	}
	if r.Method == http.MethodPost {
		if len(p) == 4 && p[0] == "versions" && p[2] == "indexes" && p[3] == "rebuild" {
			return true
		}
		if len(p) == 3 && p[0] == "versions" && p[2] == "render-sessions" {
			return true
		}
		return len(p) == 5 && p[0] == "versions" && p[2] == "render-sessions" && (p[4] == "renew" || p[4] == "close")
	}
	if len(p) == 3 && p[0] == "versions" && (p[2] == "reading-position" || p[2] == "reader-preference" || p[2] == "page-position") {
		return r.Method == "GET" || r.Method == "HEAD" || r.Method == "PUT"
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		return false
	}
	if r.Method == "HEAD" && p[0] == "versions" && (len(p) == 3 && p[2] == "source-evidence-verifications" || len(p) == 5 && p[2] == "source-evidence" && p[4] == "verifications") {
		return false
	}
	if len(p) == 1 {
		return p[0] == "search" || p[0] == "capabilities" || p[0] == "projects" || p[0] == "documents"
	}
	if p[0] == "documents" {
		return len(p) == 2 || len(p) == 3 && p[2] == "versions"
	}
	if p[0] == "projects" {
		return len(p) == 3 && p[2] == "sources"
	}
	if p[0] == "versions" {
		if len(p) == 2 {
			return true
		}
		if len(p) == 3 {
			return p[2] == "backlinks" || p[2] == "references" || p[2] == "files" || p[2] == "file" || p[2] == "metadata" || p[2] == "render-bootstrap" || p[2] == "package" || p[2] == "source-evidence" || p[2] == "source-evidence-verifications" || p[2] == "assessments"
		}
		if len(p) == 4 {
			return p[2] == "indexes" && p[3] == "validate" || p[2] == "source-evidence" || p[2] == "assessments" && p[3] == "coverage" || referenceRoute(p)
		}
		return len(p) == 5 && p[2] == "source-evidence" && p[4] == "verifications"
	}
	return false
}
func bearer(r *http.Request) (string, error) {
	values := r.Header.Values("Authorization")
	if len(values) == 0 {
		return "", sec.ErrRequired
	}
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || strings.TrimSpace(strings.TrimPrefix(values[0], "Bearer ")) != strings.TrimPrefix(values[0], "Bearer ") {
		return "", sec.ErrInvalid
	}
	return strings.TrimPrefix(values[0], "Bearer "), nil
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v1/search" || referenceRoute(route(r)) || connectedRoute(route(r)) {
		ctx, cancel := context.WithTimeout(r.Context(), query.SearchTimeout)
		defer cancel()
		r = r.WithContext(ctx)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	token, err := bearer(r)
	if err != nil {
		failure(w, r, err)
		return
	}
	if _, err = h.Core.Auth.Authenticate(r.Context(), token); err != nil {
		failure(w, r, err)
		return
	}
	if !h.Handles(r) {
		failure(w, r, domain.ErrNotFound)
		return
	}
	p := route(r)
	if reviewRoute(p) {
		h.review(w, r, token, p)
		return
	}
	if connectedRoute(p) {
		h.connected(w, r, token, p)
		return
	}
	if referenceRoute(p) {
		h.references(w, r, token, p)
		return
	}
	if len(p) == 1 && p[0] == "search" {
		h.search(w, r, token)
		return
	}
	allowed := []string{"cursor", "limit"}
	if p[0] == "documents" && len(p) == 1 {
		allowed = append(allowed, "project_id", "lifecycle", "review")
	}
	if p[0] == "projects" && len(p) == 3 && p[2] == "sources" {
		allowed = []string{"cursor", "limit"}
	}
	if p[0] == "versions" && len(p) > 2 {
		switch p[2] {
		case "source-evidence-verifications":
			allowed = []string{"evidence_id", "cursor", "limit"}
		case "files":
			allowed = append(allowed, "directory", "kind")
		case "file":
			allowed = []string{"path", "resolve_link"}
		case "metadata":
			allowed = []string{"kind"}
		case "source-evidence":
			allowed = nil
			if len(p) == 3 {
				allowed = []string{"evidence_id"}
			}
			if len(p) == 5 && p[4] == "verifications" {
				// G12: the catalogued path form accepts the same paging keys
				// as the canonical query alias.
				allowed = []string{"cursor", "limit"}
			}
		case "render-bootstrap":
			allowed = []string{"page_path"}
		case "assessments":
			allowed = []string{"target_set_id", "scope_kind", "page_path", "region_id", "cursor", "limit"}
			if len(p) == 4 && p[3] == "coverage" {
				allowed = []string{"coverage_set_id", "attempt_run_id", "cursor", "limit"}
			}
		case "reading-position", "reader-preference":
			allowed = []string{}
		case "page-position":
			allowed = []string{"page"}
		default:
			allowed = nil
		}
	}
	values, err := parameters(r, allowed)
	if err != nil {
		failure(w, r, err)
		return
	}
	o := query.Options{Cursor: values.Get("cursor"), Project: values.Get("project_id"), Lifecycle: values.Get("lifecycle"), Directory: values.Get("directory"), Kind: values.Get("kind"), Review: values.Get("review")}
	if values.Has("limit") {
		o.Limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil || o.Limit < 1 {
			failure(w, r, &query.Error{Reason: "invalid_page_size"})
			return
		}
	}
	if r.Method == http.MethodPost {
		h.renderSession(w, r, token, p)
		return
	}
	var data any
	etag := ""
	version := ""
	switch {
	case len(p) == 1 && p[0] == "capabilities":
		var capabilities map[string]any
		capabilities, err = h.Core.Capabilities(r.Context(), token)
		if err == nil && h.ingressRemote(r) {
			// The loopback renderer construction is unreachable from remote
			// ingress; the honest advertisement keeps the feature flags off
			// and names the condition. Remote construction is EPIC-007.
			capabilities["renderer_unavailable_reason"] = render.ReasonRemoteIngress
		} else if err == nil && h.Render != nil && !h.Render.Unavailable() {
			features := capabilities["features"].(map[string]bool)
			features["html_js_v1"], features["multi_page_loader"], features["renderer"] = true, true, true
		}
		data = capabilities
	case len(p) == 1 && p[0] == "projects":
		data, err = h.Core.Projects(r.Context(), token, o)
	case len(p) == 1 && p[0] == "documents":
		data, err = h.Core.Documents(r.Context(), token, o)
	case p[0] == "documents" && len(p) == 2:
		var item query.Document
		item, err = h.Core.Document(r.Context(), token, p[1])
		data = item
		etag = item.ETag
	case p[0] == "documents" && len(p) == 3:
		data, err = h.Core.Versions(r.Context(), token, p[1], o)
	case p[0] == "versions" && len(p) == 2:
		var item query.Version
		item, err = h.Core.Version(r.Context(), token, p[1])
		data = item
		etag = item.ETag
		version = p[1]
	case p[0] == "projects" && len(p) == 3 && p[2] == "sources":
		data, err = h.Core.ProjectSources(r.Context(), token, p[1], o)
	case p[0] == "versions" && len(p) == 3 && p[2] == "source-evidence-verifications":
		evidence := ""
		if raw := values["evidence_id"]; len(raw) == 1 {
			evidence = raw[0]
		}
		if evidence == "" {
			failure(w, r, &query.Error{Reason: "evidence_id_required"})
			return
		}
		data, err = h.Core.Verifications(r.Context(), token, p[1], evidence, o)
		version = p[1]
	case p[0] == "versions" && len(p) == 3 && p[2] == "assessments":
		assessmentQuery := query.AssessmentQuery{Options: o, TargetSetID: values.Get("target_set_id"), ScopeKind: values.Get("scope_kind"), PagePath: values.Get("page_path"), RegionID: values.Get("region_id")}
		data, err = h.Core.Assessments(r.Context(), token, p[1], assessmentQuery)
		version = p[1]
	case p[0] == "versions" && len(p) == 4 && p[2] == "assessments" && p[3] == "coverage":
		coverageSet := values.Get("coverage_set_id")
		if coverageSet == "" {
			failure(w, r, &query.Error{Reason: "coverage_set_id_required"})
			return
		}
		data, err = h.Core.AssessmentCoverage(r.Context(), token, p[1], coverageSet, values.Get("attempt_run_id"), o)
		version = p[1]
	case p[0] == "versions" && len(p) == 5 && p[2] == "source-evidence" && p[4] == "verifications":
		// S27/G12: the catalogued path form serves the same history read
		// for simple ids and accepts the same cursor/limit query keys as
		// the canonical query alias.
		data, err = h.Core.Verifications(r.Context(), token, p[1], p[3], o)
		version = p[1]
	case p[0] == "versions" && len(p) == 3 && p[2] == "files":
		data, err = h.Core.Files(r.Context(), token, p[1], o)
		version = p[1]
	case p[0] == "versions" && len(p) == 3 && p[2] == "render-bootstrap":
		var bootstrap query.Bootstrap
		bootstrap, err = h.Core.RenderBootstrap(r.Context(), token, p[1], values.Get("page_path"))
		if err == nil && h.ingressRemote(r) {
			bootstrap.RendererUnavailableReason = render.ReasonRemoteIngress
		} else if err == nil && h.Render != nil && !h.Render.Unavailable() {
			bootstrap.RendererAvailable = true
			bootstrap.Delivery = "loopback_renderer_origin"
			bootstrap.RenderSessionsURL = "/api/v1/versions/" + p[1] + "/render-sessions"
			bootstrap.RendererConstruction = "loopback_renderer_origin"
		}
		data = bootstrap
		version = p[1]
	case p[0] == "versions" && len(p) == 3 && (p[2] == "reading-position" || p[2] == "reader-preference" || p[2] == "page-position") && (r.Method == "GET" || r.Method == "HEAD"):
		var kind query.PersonalKind
		page := ""
		switch p[2] {
		case "reading-position":
			kind = query.ReadingPosition
		case "reader-preference":
			kind = query.ReaderPreference
		default:
			kind = query.PagePosition
			page = values.Get("page")
			if page == "" {
				failure(w, r, &query.Error{Reason: "page_required"})
				return
			}
		}
		var view query.PersonalView
		view, err = h.Core.PersonalGet(r.Context(), token, p[1], kind, page)
		data = view
		if view.Exists && view.ETag != nil {
			etag = *view.ETag
		}
		version = p[1]
	case p[0] == "versions" && len(p) == 3 && (p[2] == "reading-position" || p[2] == "reader-preference" || p[2] == "page-position") && r.Method == "PUT":
		h.personalPut(w, r, token, p[1], p[2], values)
		return
	case p[0] == "versions" && len(p) == 3 && p[2] == "source-evidence":
		if !values.Has("evidence_id") {
			failure(w, r, &query.Error{Reason: "evidence_id_required"})
			return
		}
		data, err = h.Core.Evidence(r.Context(), token, p[1], values.Get("evidence_id"))
		version = p[1]
	case p[0] == "versions" && len(p) == 4:
		data, err = h.Core.Evidence(r.Context(), token, p[1], p[3])
		version = p[1]
	case p[0] == "versions" && len(p) == 3 && (p[2] == "file" || p[2] == "metadata"):
		h.deliver(w, r, token, p[1], p[2], values)
		return
	case p[0] == "versions" && len(p) == 3 && p[2] == "package":
		h.deliverPackage(w, r, token, p[1])
		return
	default:
		err = domain.ErrNotFound
	}
	if err == nil && version != "" {
		err = h.Core.Guard(r.Context(), token, version)
	}
	if err != nil {
		failure(w, r, err)
		return
	}
	if etag != "" {
		w.Header().Set("ETag", etag)
		if !matchIfMatch(r.Header.Get("If-Match"), etag) {
			failure(w, r, domain.ErrPrecondition)
			return
		}
		if matchNone(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	respond(w, r, 200, data)
}

// renderSession serves the trusted-host render-session lifecycle routes:
// open (durable K receipt), renew and close (natural session identity, no
// receipt). The renderer subsystem is constructed lazily on first use.
func (h *Handler) renderSession(w http.ResponseWriter, r *http.Request, token string, p []string) {
	if err := emptyRenderControl(r.Body); err != nil {
		failure(w, r, err)
		return
	}
	key := ""
	if len(p) == 3 && p[2] == "render-sessions" {
		var err error
		key, err = personalOperationKey(r)
		if err != nil {
			failure(w, r, err)
			return
		}
	}
	// Remote ingress never constructs the renderer subsystem, reserves an
	// operation or binds a listener: the loopback construction is a known
	// unavailable capability for this caller (remote construction belongs to
	// EPIC-007). Renew shares the boundary; close stays usable so a known
	// owned session can still be cleaned up from any ingress.
	if (len(p) == 3 && p[2] == "render-sessions" || len(p) == 5 && p[4] == "renew") && h.ingressRemote(r) {
		failure(w, r, &render.Error{Reason: render.ReasonRemoteIngress})
		return
	}
	if h.Render == nil {
		failure(w, r, &render.Error{Reason: render.ReasonUnsupportedAccount})
		return
	}
	core, err := h.Render.Get()
	if err != nil {
		failure(w, r, err)
		return
	}
	var response domain.Response
	switch {
	case len(p) == 3 && p[2] == "render-sessions":
		response, err = core.Open(r.Context(), token, key, p[1])
	case len(p) == 5 && p[2] == "render-sessions" && p[4] == "renew":
		response, err = core.Renew(r.Context(), token, p[1], p[3])
	case len(p) == 5 && p[2] == "render-sessions" && p[4] == "close":
		response, err = core.Close(r.Context(), token, p[1], p[3])
	default:
		failure(w, r, domain.ErrNotFound)
		return
	}
	if err != nil {
		failure(w, r, err)
		return
	}
	if response.Status == 204 {
		w.WriteHeader(204)
		return
	}
	respond(w, r, response.Status, json.RawMessage(response.Body))
}

// Render control has no author data or optional authority fields. Empty input
// and an empty object are equivalent; null, unknown fields and trailing values
// fail before constructing the lazy renderer or reserving an operation.
func emptyRenderControl(body io.Reader) error {
	if body == nil {
		return nil
	}
	decoder := json.NewDecoder(body)
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil || fields == nil || len(fields) != 0 {
		return sec.ErrInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return sec.ErrInvalid
	}
	return nil
}

// missingPrecondition names a required personal-save control header.
type missingPrecondition string

func (e missingPrecondition) Error() string        { return "missing required header" }
func (e missingPrecondition) Is(target error) bool { return target == sec.ErrMissing }

var personalKeyPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func personalOperationKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) == 0 {
		return "", missingPrecondition("Idempotency-Key")
	}
	if len(values) != 1 || !personalKeyPattern.MatchString(values[0]) {
		return "", sec.ErrInvalid
	}
	return values[0], nil
}

// personalPut performs one durable owner-only personal save. Body parsing
// stays typed and strictly validated; there is no author-field, string or
// serialized-state admission quota, and no truncation — an unrepresentable
// payload fails honestly rather than being capped.
func (h *Handler) personalPut(w http.ResponseWriter, r *http.Request, token, version, kindName string, values url.Values) {
	kind := query.PersonalKind(kindName)
	page := ""
	if kind == query.PagePosition {
		page = values.Get("page")
	}
	key, err := personalOperationKey(r)
	if err != nil {
		failure(w, r, err)
		return
	}
	etag, create, err := personalConditions(r)
	if err != nil {
		failure(w, r, err)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		failure(w, r, sec.ErrInvalid)
		return
	}
	response, err := h.Core.PersonalPut(r.Context(), token, key, version, kind, page, etag, create, body)
	if err != nil {
		failure(w, r, err)
		return
	}
	if value, ok := response.Headers["ETag"]; ok {
		w.Header().Set("ETag", value)
	}
	respond(w, r, response.Status, json.RawMessage(response.Body))
}

func parameters(r *http.Request, allowed []string) (url.Values, error) {
	v, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, &query.Error{Reason: "invalid_query"}
	}
	for key, values := range v {
		found := false
		for _, a := range allowed {
			if key == a {
				found = true
				break
			}
		}
		if !found || len(values) != 1 || values[0] == "" {
			return nil, &query.Error{Reason: "unknown_duplicate_or_empty_query"}
		}
	}
	return v, nil
}
func matchNone(header, etag string) bool {
	for _, v := range strings.Split(header, ",") {
		v = strings.TrimSpace(v)
		if v == "*" || strings.TrimPrefix(v, "W/") == etag {
			return true
		}
	}
	return false
}
func matchIfMatch(header, etag string) bool {
	if header == "" {
		return true
	}
	for _, v := range strings.Split(header, ",") {
		v = strings.TrimSpace(v)
		if v == "*" || v == etag {
			return true
		}
	}
	return false
}

func (h *Handler) deliver(w http.ResponseWriter, r *http.Request, token, id, kind string, v url.Values) {
	started := false
	consume := func(info query.ResolvedFile, handle io.ReadSeeker) error {
		if err := h.Core.Guard(r.Context(), token, id); err != nil {
			return err
		}
		if handle == nil {
			if r.Header.Get("Range") != "" {
				failureCode(w, r, 416, "RANGE_NOT_SATISFIABLE", nil, false)
				started = true
				return nil
			}
			w.Header().Set("ETag", info.ETag)
			if !matchIfMatch(r.Header.Get("If-Match"), info.ETag) {
				return domain.ErrPrecondition
			}
			if matchNone(r.Header.Get("If-None-Match"), info.ETag) {
				w.WriteHeader(304)
			} else {
				respond(w, r, 200, info)
			}
			started = true
			return nil
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(info.Path)}))
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		w.Header().Set("ETag", info.ETag)
		w.Header().Set("Haeryong-Requested-Path", url.QueryEscape(info.RequestedPath))
		w.Header().Set("Haeryong-Resolved-Path", url.QueryEscape(info.ResolvedPath))
		if info.Hash != nil {
			raw, err := hex.DecodeString(*info.Hash)
			if err != nil {
				return err
			}
			w.Header().Set("Repr-Digest", "sha-256=:"+base64.StdEncoding.EncodeToString(raw)+":")
		}
		// ServeContent treats an empty file's unsatisfiable Range as 200 for
		// compatibility. Our exact-byte contract requires explicit 416, after
		// conditionals and a mismatching If-Range have been accounted for.
		if info.Object.Size == 0 && r.Header.Get("Range") != "" && (r.Header.Get("If-Range") == "" || r.Header.Get("If-Range") == info.ETag) && matchIfMatch(r.Header.Get("If-Match"), info.ETag) && !matchNone(r.Header.Get("If-None-Match"), info.ETag) {
			w.Header().Set("Content-Range", "bytes */0")
			failureCode(w, r, 416, "RANGE_NOT_SATISFIABLE", nil, false)
			started = true
			return nil
		}
		writer := &deliveryWriter{ResponseWriter: w, head: r.Method == "HEAD", check: func() error { return h.Core.Guard(r.Context(), token, id) }}
		http.ServeContent(writer, r, info.Path, time.Time{}, handle)
		started = writer.started
		if writer.err != nil {
			return writer.err
		}
		if writer.status >= 400 {
			code := "INVALID_REQUEST"
			if writer.status == 416 {
				code = "RANGE_NOT_SATISFIABLE"
			} else if writer.status == 412 {
				code = "PRECONDITION_FAILED"
			}
			w.Header().Del("Content-Length")
			failureCode(w, r, writer.status, code, nil, writer.status == 503)
			started = true
		}
		return nil
	}
	var err error
	if kind == "metadata" {
		err = h.Core.ReadMetadata(r.Context(), token, id, v.Get("kind"), consume)
	} else {
		resolve := false
		if v.Has("resolve_link") {
			if v.Get("resolve_link") != "true" && v.Get("resolve_link") != "false" {
				failure(w, r, &query.Error{Reason: "invalid_resolve_link"})
				return
			}
			resolve = v.Get("resolve_link") == "true"
		}
		err = h.Core.ReadFile(r.Context(), token, id, v.Get("path"), resolve, consume)
	}
	if err != nil && !started {
		w.Header().Del("Content-Length")
		w.Header().Del("Content-Disposition")
		failure(w, r, err)
	}
}

// deliverPackage streams the faithful original-directory archive. The archive
// is a generated representation, so the response carries no Content-Length
// and no representation digest: payload identity stays with the per-entry
// bytes_sha256 inventory, never with these wire bytes. A failure before the
// first byte returns the ordinary JSON registry error; a failure after the
// body started aborts the connection so a partial archive can never end in
// the clean EOF a client could mistake for completion.
func (h *Handler) deliverPackage(w http.ResponseWriter, r *http.Request, token, id string) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "haeryong-" + id + ".zip"}))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	sink := &packageResponseWriter{ResponseWriter: w, check: func() error { return h.Core.GuardPackage(r.Context(), token, id) }}
	if err := h.Core.Package(r.Context(), token, id, sink, sink.check); err != nil {
		if !sink.started {
			w.Header().Del("Content-Length")
			failure(w, r, err)
			return
		}
		panic(http.ErrAbortHandler)
	}
}

// packageResponseWriter renews export authority and the socket write deadline
// around every body write, mirroring the guarded file-delivery writer.
type packageResponseWriter struct {
	http.ResponseWriter
	check   func() error
	started bool
}

func (w *packageResponseWriter) Write(p []byte) (int, error) {
	if err := w.check(); err != nil {
		return 0, err
	}
	if err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return 0, err
	}
	if !w.started {
		// The underlying Write commits the headers even when it returns a
		// partial write or an error, so the response is marked committed
		// BEFORE that first Write: any first-write or first-flush failure
		// then aborts the connection instead of appending a canonical JSON
		// error to an already started body.
		w.started = true
	}
	n, err := w.ResponseWriter.Write(p)
	if err == nil {
		// Serialize the headers while the handler is still running so the
		// archive is a streamed response: the server must not retroactively
		// compute a Content-Length for a small, fully buffered archive.
		if flushErr := http.NewResponseController(w.ResponseWriter).Flush(); flushErr != nil && !errors.Is(flushErr, http.ErrNotSupported) {
			return n, flushErr
		}
	}
	return n, err
}

// ServeContent decides conditionals/ranges while this wrapper postpones error
// bodies for the canonical JSON registry and renews each socket-write deadline.
type deliveryWriter struct {
	http.ResponseWriter
	check   func() error
	status  int
	started bool
	head    bool
	err     error
}

func (w *deliveryWriter) WriteHeader(status int) {
	w.status = status
	if status >= 400 {
		return
	}
	if status == 200 && !w.head {
		w.Header().Set("Content-Digest", w.Header().Get("Repr-Digest"))
	} else {
		w.Header().Del("Content-Digest")
	}
	if err := w.check(); err != nil {
		w.err = err
		return
	}
	if err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		w.err = err
		return
	}
	w.started = true
	w.ResponseWriter.WriteHeader(status)
}
func (w *deliveryWriter) Write(p []byte) (int, error) {
	if w.status >= 400 {
		return len(p), nil
	}
	if w.err != nil {
		return 0, w.err
	}
	if err := w.check(); err != nil {
		w.err = err
		return 0, err
	}
	if err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		w.err = err
		return 0, err
	}
	if !w.started {
		w.WriteHeader(200)
	}
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.ResponseWriter.Write(p)
	w.err = err
	return n, err
}

func respond(w http.ResponseWriter, r *http.Request, status int, data any) {
	id, _ := service.NewID()
	b, _ := json.Marshal(map[string]any{"data": data, "request_id": id})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(status)
	if r.Method != "HEAD" {
		_, _ = w.Write(b)
	}
}
func failure(w http.ResponseWriter, r *http.Request, err error) {
	status, code := 503, "SERVICE_RECOVERING"
	details := map[string]any{}
	var invalid *query.Error
	var missing *query.Missing
	var referenceInvalid *query.ReferenceInvalid
	var targetGone *query.TargetGone
	switch {
	case errors.As(err, &referenceInvalid):
		status, code = 400, "INVALID_REQUEST"
		details["pointer"] = referenceInvalid.Pointer
		details["reason"] = referenceInvalid.Reason
	case errors.As(err, &targetGone):
		status, code = 410, "TARGET_GONE"
		details["document_id"] = targetGone.Target.DocumentID
		details["version_id"] = targetGone.Target.VersionID
	case errors.As(err, &invalid):
		status, code = 400, "INVALID_REQUEST"
		details["reason"] = invalid.Reason
	case errors.Is(err, sec.ErrRequired):
		status, code = 401, "AUTH_REQUIRED"
	case errors.Is(err, sec.ErrRevoked):
		status, code = 401, "SESSION_REVOKED"
	case errors.Is(err, sec.ErrExpired):
		status, code = 401, "SESSION_EXPIRED"
	case errors.Is(err, sec.ErrInvalid), errors.Is(err, domain.ErrInvalidTarget):
		status, code = 400, "INVALID_REQUEST"
	case errors.Is(err, domain.ErrDenied):
		status, code = 403, "FORBIDDEN"
	case errors.Is(err, domain.ErrNotFound):
		status, code = 404, "NOT_FOUND"
		if errors.As(err, &missing) {
			details["reason"] = missing.Reason
			for key, value := range missing.Details {
				details[key] = value
			}
		}
	case errors.Is(err, domain.ErrReconciliation):
		status, code = 409, "OPERATION_RECONCILIATION_REQUIRED"
	case errors.Is(err, domain.ErrConflict):
		status, code = 409, "OPERATION_CONFLICT"
	case errors.Is(err, domain.ErrPrecondition):
		status, code = 412, "PRECONDITION_FAILED"
	case errors.Is(err, sec.ErrMissing):
		status, code = 428, "PRECONDITION_REQUIRED"
	case errors.Is(err, domain.ErrContentIntegrity):
		status, code = 503, "CONTENT_INTEGRITY_ERROR"
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT), errors.Is(err, syscall.ENOMEM):
		status, code = 503, "RESOURCE_EXHAUSTED"
	case errors.Is(err, os.ErrPermission), errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.EIO), errors.Is(err, syscall.EROFS):
		status, code = 503, "FILE_ACCESS_ERROR"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status, code = 503, "TRANSFER_INTERRUPTED"
	}
	var review *query.ReviewError
	if errors.As(err, &review) {
		code = review.Code
		status = 400
		if code == "INVALID_TRANSITION" {
			status = 409
		}
		if code == "PRECONDITION_FAILED" {
			status = 412
			if review.Current != nil {
				details["revision"] = review.Current.Revision
				details["etag"] = review.Current.ETag
			}
		}
	}
	var lifecycle *query.Transition
	if errors.As(err, &lifecycle) {
		status, code = 409, "INVALID_TRANSITION"
		details["lifecycle"] = lifecycle.Lifecycle
	}
	var renderer *render.Error
	if errors.As(err, &renderer) {
		details["reason"] = renderer.Reason
		switch renderer.Reason {
		case render.ReasonUnknownSession:
			status, code = 404, "NOT_FOUND"
		case render.ReasonClosedSession, render.ReasonStaleSession, render.ReasonDeniedDelivery:
			status, code = 409, "INVALID_TRANSITION"
		case render.ReasonExhausted:
			status, code = 409, "INVALID_TRANSITION"
			details["expansion"] = "widen network.renderer_pool and restart the daemon"
		case render.ReasonBindConflict:
			status, code = 409, "INVALID_TRANSITION"
		case render.ReasonTransientOwner:
			status, code = 503, "SERVICE_RECOVERING"
		case render.ReasonUnsupportedAccount, render.ReasonRemoteIngress:
			status, code = 503, "FEATURE_NOT_READY"
		default:
			status, code = 503, "SERVICE_RECOVERING"
		}
	}
	var requiredHeader missingPrecondition
	if errors.As(err, &requiredHeader) {
		details["missing"] = string(requiredHeader)
	}
	retryable := status == 503
	if renderer != nil && renderer.Reason != render.ReasonTransientOwner {
		// Account/ledger repair requires an explicit supported remedy. A
		// definite delivery denial must not trigger logout or endless retries.
		retryable = false
	}
	if (r.URL.Path == "/api/v1/search" || referenceRoute(route(r)) || connectedRoute(route(r))) && status == 503 {
		code = "SERVICE_RECOVERING"
		details = map[string]any{}
		retryable = true
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			details["reason"] = "query_timeout"
		}
		w.Header().Set("Retry-After", "2")
	}
	failureCode(w, r, status, code, details, retryable)
}
func failureCode(w http.ResponseWriter, r *http.Request, status int, code string, details map[string]any, retryable bool) {
	w.Header().Del("Content-Disposition")
	w.Header().Del("Content-Digest")
	w.Header().Del("Repr-Digest")
	if details == nil {
		details = map[string]any{}
	}
	id, _ := service.NewID()
	b, _ := json.Marshal(map[string]any{"error": map[string]any{"code": code, "message": code, "retryable": retryable, "details": details}, "request_id": id})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(status)
	if r.Method != "HEAD" {
		_, _ = w.Write(b)
	}
}
