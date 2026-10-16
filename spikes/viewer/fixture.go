// Package viewer implements a disposable, authenticated directory viewer proof.
package viewer

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	auth "haeryong.local/auth-ingress"
)

//go:embed host.html
var hostPage []byte

//go:embed host.js
var hostScript []byte

//go:embed loader.js
var loaderScript []byte

//go:embed fixture.go corpus.go knowledge.go
var implementationSources embed.FS

type Config struct {
	State, Base, Remote        string
	Knowledge                  string
	Renderers, RemoteRenderers [2]string
}
type delivery struct {
	ID, TokenHash, Owner, Version, Origin, Kind string
	Expires                                     time.Time
	Closed                                      bool
	Delay                                       time.Duration
}
type handoff struct {
	SecretHash, DeliveryID, Page string
	Expires                      time.Time
}
type contextTransfer struct {
	mu                          sync.Mutex
	Deleted                     bool
	Owner, Binding, Media, File string
	Offset                      int64
	Digest, Ref                 string
	Sealed                      bool
}
type Observation struct {
	Time                                  time.Time `json:"time"`
	Kind, Version, Path, Origin, Site     string
	Host, Destination, Method, DeliveryID string
	Bytes                                 int64
	Error                                 string
}
type Fixture struct {
	config         Config
	knowledge      *knowledgeCorpus
	auth           http.Handler
	closeAuth      func() error
	mu             sync.Mutex
	deliveries     map[string]*delivery
	handoffs       map[string]handoff
	contexts       map[string]*contextTransfer
	observations   []Observation
	reports        []json.RawMessage
	servers        []*http.Server
	listeners      []net.Listener
	instance       string
	implementation string
	contextBytes   int64
	contextDigest  string
	inventories    map[string]map[string]fileRecord
	versionStates  map[string]string
}

func random() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func digest(b []byte) string { d := sha256.Sum256(b); return hex.EncodeToString(d[:]) }
func originHost(s string) string {
	u, _ := url.Parse(s)
	if u == nil {
		return ""
	}
	return u.Host
}
func New(c Config) (*Fixture, error) {
	info, e := os.Lstat(c.State)
	if e != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, errors.New("state must be an existing private directory")
	}
	hosts, origins := map[string]bool{}, map[string]bool{}
	for _, s := range []string{c.Base, c.Remote} {
		if s != "" {
			u, e := url.Parse(s)
			if e != nil || u.Host == "" || u.User != nil || u.Path != "" {
				return nil, errors.New("exact trusted origins required")
			}
			hosts[u.Host] = true
			origins[s] = true
		}
	}
	ah, closeAuth, e := auth.Fixture(filepath.Join(c.State, "auth.db"), filepath.Join(c.State, "control"), hosts, origins)
	if e != nil {
		return nil, e
	}
	f := &Fixture{config: c, auth: ah, closeAuth: closeAuth, deliveries: map[string]*delivery{}, handoffs: map[string]handoff{}, contexts: map[string]*contextTransfer{}, instance: random()[:16], inventories: map[string]map[string]fileRecord{}}
	f.versionStates = map[string]string{"a": "available", "b": "available"}
	if saved, e := os.ReadFile(filepath.Join(c.State, "fixture-versions.json")); e == nil {
		if e = json.Unmarshal(saved, &f.versionStates); e != nil {
			closeAuth()
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		closeAuth()
		return nil, e
	}
	implementation := append([]byte(nil), hostPage...)
	implementation = append(implementation, hostScript...)
	implementation = append(implementation, loaderScript...)
	for _, name := range []string{"fixture.go", "corpus.go", "knowledge.go"} {
		source, _ := implementationSources.ReadFile(name)
		implementation = append(implementation, source...)
	}
	f.implementation = digest(implementation)
	hash := sha256.New()
	pattern := []byte("Selected retry scenario: 한글🌊\n")
	f.contextBytes = (17 * 1024 * 1024 / int64(len(pattern))) * int64(len(pattern))
	chunk := make([]byte, 65536)
	for offset := int64(0); offset < f.contextBytes; {
		n := int64(len(chunk))
		if f.contextBytes-offset < n {
			n = f.contextBytes - offset
		}
		for j := int64(0); j < n; j++ {
			chunk[j] = pattern[(offset+j)%int64(len(pattern))]
		}
		hash.Write(chunk[:n])
		offset += n
	}
	f.contextDigest = hex.EncodeToString(hash.Sum(nil))
	if e = f.loadKnowledge(); e != nil {
		closeAuth()
		return nil, e
	}
	if e = f.prepareCorpus(); e != nil {
		closeAuth()
		return nil, e
	}
	if e = f.bindKnowledge(); e != nil {
		closeAuth()
		return nil, e
	}
	if e = f.bindOrigins(); e != nil {
		closeAuth()
		return nil, e
	}
	return f, nil
}
func (f *Fixture) bindOrigins() error {
	file := filepath.Join(f.config.State, "origin-bindings.json")
	bindings := map[string]string{}
	if b, e := os.ReadFile(file); e == nil {
		if e = json.Unmarshal(b, &bindings); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	for index, version := range []string{"a", "b"} {
		b, _ := jsonBytes(f.inventories[version])
		identity := version + ":" + digest(b)
		for _, origin := range []string{f.config.Renderers[index], f.config.RemoteRenderers[index]} {
			if origin == "" {
				continue
			}
			if previous, exists := bindings[origin]; exists && previous != identity {
				return errors.New("renderer origin quarantined for its previously assigned immutable version")
			}
			bindings[origin] = identity
		}
	}
	b, _ := jsonBytes(bindings)
	return os.WriteFile(file, b, 0600)
}
func (f *Fixture) Start() error {
	handlers := []http.Handler{http.HandlerFunc(f.trusted), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.render(w, r, "a") }), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.render(w, r, "b") })}
	for i, s := range []string{f.config.Base, f.config.Renderers[0], f.config.Renderers[1]} {
		u, e := url.Parse(s)
		if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" {
			f.Close()
			return errors.New("local listeners require exact loopback HTTP origins")
		}
		ln, e := net.Listen("tcp", u.Host)
		if e != nil {
			f.Close()
			return e
		}
		server := &http.Server{Handler: handlers[i], ReadHeaderTimeout: 10 * time.Second}
		f.listeners = append(f.listeners, ln)
		f.servers = append(f.servers, server)
		go server.Serve(ln)
	}
	return nil
}
func (f *Fixture) Close() error {
	for _, s := range f.servers {
		s.Close()
	}
	if f.closeAuth != nil {
		return f.closeAuth()
	}
	return nil
}
func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}
func (f *Fixture) ownerStatus(t string) (int, string) {
	if t == "" {
		return http.StatusUnauthorized, ""
	}
	r := httptest.NewRequest("GET", f.config.Base+"/session", nil)
	r.Header.Set("Authorization", "Bearer "+t)
	w := httptest.NewRecorder()
	f.auth.ServeHTTP(w, r)
	return w.Code, w.Header().Get("Retry-After")
}
func ownerFailure(w http.ResponseWriter, code int, retryAfter string) {
	if retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	http.Error(w, "owner authorization failed", code)
}
func (f *Fixture) requireOwner(w http.ResponseWriter, token string) bool {
	code, retryAfter := f.ownerStatus(token)
	if code == http.StatusOK {
		return true
	}
	ownerFailure(w, code, retryAfter)
	return false
}
func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
func headers(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}
func (f *Fixture) observe(r *http.Request, kind, version string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := ""
	if kind == "authorized-request" {
		id = r.Header.Get("X-Fixture-Authorized-Delivery")
	}
	f.observations = append(f.observations, Observation{Time: time.Now(), Kind: kind, Version: version, Path: r.URL.EscapedPath(), Origin: r.Header.Get("Origin"), Site: r.Header.Get("Sec-Fetch-Site"), Host: r.Host, Destination: r.Header.Get("Sec-Fetch-Dest"), Method: r.Method, DeliveryID: id})
}
func (f *Fixture) trusted(w http.ResponseWriter, r *http.Request) {
	headers(w)
	if r.Host != originHost(f.config.Base) && (f.config.Remote == "" || r.Host != originHost(f.config.Remote)) {
		http.Error(w, "host denied", 403)
		return
	}
	supplied := r.Header.Get("Origin")
	if supplied != "" && supplied != f.config.Base && supplied != f.config.Remote {
		http.Error(w, "origin denied", 403)
		return
	}
	switch r.URL.Path {
	case "/", "/reader":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-src http: https:; form-action http: https:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Write(hostPage)
		return
	case "/host.js":
		w.Header().Set("Content-Type", "application/javascript")
		w.Write(hostScript)
		return
	case "/health":
		reply(w, 200, map[string]any{"instance": f.instance, "implementation": f.implementation, "fixture": "viewer", "review": false})
		return
	case "/favicon.ico":
		w.WriteHeader(204)
		return
	case "/viewer/download":
		f.download(w, r)
		return
	case "/viewer/restore-fixture":
		control, e := os.ReadFile(filepath.Join(f.config.State, "control"))
		if r.Method != "POST" || r.Host != originHost(f.config.Base) || r.Header.Get("Origin") != "" || e != nil || subtle.ConstantTimeCompare([]byte(bearer(r)), []byte(strings.TrimSpace(string(control)))) != 1 {
			http.Error(w, "local control required", 403)
			return
		}
		if e = auth.RestoreFixtureAuth(filepath.Join(f.config.State, "auth.db")); e != nil {
			http.Error(w, "fixture restore failed", 503)
			return
		}
		f.mu.Lock()
		for _, d := range f.deliveries {
			d.Closed = true
		}
		f.mu.Unlock()
		w.WriteHeader(204)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/viewer/") {
		f.auth.ServeHTTP(w, r)
		return
	}
	if !f.requireOwner(w, bearer(r)) {
		return
	}
	if r.Method != "GET" && supplied == "" && r.Header.Get("X-Native-Proxy") != "1" {
		http.Error(w, "origin required", 403)
		return
	}
	switch r.URL.Path {
	case "/viewer/download-issue":
		if r.Method != "POST" {
			http.Error(w, "method", 405)
			return
		}
		var input struct {
			Version string
			Slow    bool
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || (input.Version != "a" && input.Version != "b") {
			http.Error(w, "version", 400)
			return
		}
		f.mu.Lock()
		available := f.versionStates[input.Version] == "available"
		f.mu.Unlock()
		if !available {
			http.Error(w, "version unavailable", 410)
			return
		}
		id, token := random(), random()
		origin := f.config.Base
		if f.config.Remote != "" && r.Host == originHost(f.config.Remote) {
			origin = f.config.Remote
		}
		f.mu.Lock()
		delay := time.Duration(0)
		if input.Slow {
			delay = 35 * time.Millisecond
		}
		f.deliveries[id] = &delivery{ID: id, TokenHash: digest([]byte(token)), Owner: bearer(r), Version: input.Version, Origin: origin, Kind: "download", Expires: time.Now().Add(30 * time.Second), Delay: delay}
		f.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "hy_download_" + id, Value: token, Path: "/viewer/download", HttpOnly: true, Secure: strings.HasPrefix(origin, "https:"), SameSite: http.SameSiteStrictMode})
		reply(w, 201, map[string]string{"id": id, "credential": token, "path": "attachment.bin", "origin": origin})
	case "/viewer/version-state":
		if r.Method != "POST" {
			http.Error(w, "method", 405)
			return
		}
		var input struct{ Version, State string }
		if json.NewDecoder(r.Body).Decode(&input) != nil || (input.Version != "a" && input.Version != "b") || (input.State != "available" && input.State != "trash") {
			http.Error(w, "fixture state", 400)
			return
		}
		f.mu.Lock()
		f.versionStates[input.Version] = input.State
		if input.State == "trash" {
			for _, d := range f.deliveries {
				if d.Version == input.Version {
					d.Closed = true
				}
			}
		}
		saved, _ := jsonBytes(f.versionStates)
		e := os.WriteFile(filepath.Join(f.config.State, "fixture-versions.json"), saved, 0600)
		f.mu.Unlock()
		if e != nil {
			http.Error(w, "fixture state storage", 503)
			return
		}
		w.WriteHeader(204)
	case "/viewer/config":
		remote := f.config.Remote != "" && r.Host == originHost(f.config.Remote)
		origins := f.config.Renderers
		if remote {
			origins = f.config.RemoteRenderers
		}
		reply(w, 200, map[string]any{"renderers": origins, "instance": f.instance, "implementation": f.implementation, "fixture_only": true, "versions": []string{"a", "b"}, "context_bytes": f.contextBytes, "context_digest": f.contextDigest, "attachment": f.inventories["a"]["attachment.bin"], "knowledge": f.knowledgeSummary()})
	case "/viewer/deliver":
		if r.Method != "POST" {
			http.Error(w, "method", 405)
			return
		}
		var input struct{ Version, Page, Kind string }
		if json.NewDecoder(r.Body).Decode(&input) != nil || (input.Version != "a" && input.Version != "b") {
			http.Error(w, "version", 400)
			return
		}
		f.mu.Lock()
		available := f.versionStates[input.Version] == "available"
		f.mu.Unlock()
		if !available {
			http.Error(w, "fixture version unavailable", 410)
			return
		}
		index := 0
		if input.Version == "b" {
			index = 1
		}
		renderer := f.config.Renderers[index]
		if f.config.Remote != "" && r.Host == originHost(f.config.Remote) {
			renderer = f.config.RemoteRenderers[index]
		}
		if renderer == "" {
			http.Error(w, "renderer unavailable", 503)
			return
		}
		if input.Page == "" {
			input.Page = "pages/scenario.html"
		}
		if input.Kind == "" {
			input.Kind = "renderer"
		}
		if input.Kind != "renderer" {
			http.Error(w, "kind", 400)
			return
		}
		id, token, key := random(), random(), random()
		d := &delivery{ID: id, TokenHash: digest([]byte(token)), Owner: bearer(r), Version: input.Version, Origin: renderer, Kind: input.Kind, Expires: time.Now().Add(30 * time.Second)}
		f.mu.Lock()
		f.deliveries[id] = d
		f.handoffs[key] = handoff{SecretHash: digest([]byte(token)), DeliveryID: id, Page: input.Page, Expires: time.Now().Add(20 * time.Second)}
		f.mu.Unlock()
		reply(w, 201, map[string]string{"id": id, "handoff": key, "credential": token, "origin": renderer, "page": input.Page})
	case "/viewer/renew", "/viewer/close":
		if r.Method != "POST" {
			http.Error(w, "method", 405)
			return
		}
		var in struct{ ID string }
		json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		d := f.deliveries[in.ID]
		if d == nil || d.Owner != bearer(r) {
			f.mu.Unlock()
			http.Error(w, "delivery", 404)
			return
		}
		if r.URL.Path == "/viewer/renew" && d.Closed {
			f.mu.Unlock()
			http.Error(w, "delivery closed", 410)
			return
		}
		if r.URL.Path == "/viewer/close" {
			d.Closed = true
			f.observations = append(f.observations, Observation{Time: time.Now(), Kind: "delivery-explicit-close", Version: d.Version, DeliveryID: d.ID, Host: r.Host, Origin: r.Header.Get("Origin"), Method: r.Method})
		} else if !d.Closed {
			d.Expires = time.Now().Add(30 * time.Second)
		}
		f.mu.Unlock()
		w.WriteHeader(204)
	case "/viewer/observations":
		f.mu.Lock()
		defer f.mu.Unlock()
		leases := []map[string]any{}
		for id, d := range f.deliveries {
			if d.Owner == bearer(r) {
				leases = append(leases, map[string]any{"id": id, "version": d.Version, "origin": d.Origin, "kind": d.Kind, "closed": d.Closed, "expires_unix_ms": d.Expires.UnixMilli(), "remaining_ms": max(int64(0), time.Until(d.Expires).Milliseconds())})
			}
		}
		reply(w, 200, map[string]any{"requests": f.observations, "reports": f.reports, "review": false, "delivery_leases": leases})
	case "/viewer/knowledge":
		f.knowledgeRead(w, r)
	case "/viewer/page":
		version := r.URL.Query().Get("version")
		name := r.URL.Query().Get("path")
		record, ok := f.inventories[version][name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		reply(w, 200, map[string]any{"page_path": name, "bytes_sha256": record.SHA256})
	case "/viewer/report":
		if r.Method != "POST" {
			http.Error(w, "method", 405)
			return
		}
		var value json.RawMessage
		if json.NewDecoder(r.Body).Decode(&value) != nil {
			http.Error(w, "report", 400)
			return
		}
		f.mu.Lock()
		f.reports = append(f.reports, value)
		f.mu.Unlock()
		if e := os.WriteFile(filepath.Join(f.config.State, "report-"+random()+".json"), value, 0600); e != nil {
			http.Error(w, "report storage unavailable", 503)
			return
		}
		w.WriteHeader(204)
	case "/viewer/context":
		f.context(w, r)
	default:
		http.NotFound(w, r)
	}
}
func (f *Fixture) render(w http.ResponseWriter, r *http.Request, version string) {
	headers(w)
	i := 0
	if version == "b" {
		i = 1
	}
	local, remote := f.config.Renderers[i], f.config.RemoteRenderers[i]
	expected := local
	if remote != "" && r.Host == originHost(remote) {
		expected = remote
	}
	if r.Host != originHost(expected) {
		http.Error(w, "host denied", 403)
		return
	}
	f.observe(r, "request", version)
	if r.URL.Path == "/__handoff" && r.Method == "POST" {
		origin := r.Header.Get("Origin")
		if origin != f.config.Base && origin != f.config.Remote && origin != "wails://localhost" && origin != "null" {
			http.Error(w, "handoff origin", 403)
			return
		}
		if e := r.ParseForm(); e != nil {
			http.Error(w, "handoff", 400)
			return
		}
		key := r.Form.Get("handoff")
		token := r.Form.Get("credential")
		f.mu.Lock()
		h, ok := f.handoffs[key]
		d := f.deliveries[h.DeliveryID]
		f.mu.Unlock()
		if !ok || d == nil || d.Version != version || d.Origin != expected || time.Now().After(h.Expires) || subtle.ConstantTimeCompare([]byte(h.SecretHash), []byte(digest([]byte(token)))) != 1 {
			http.Error(w, "handoff denied", 403)
			return
		}
		if !f.requireOwner(w, d.Owner) {
			return
		}
		// Auth I/O leaves the one-use handle intact; consume only the same live entry.
		f.mu.Lock()
		current, active := f.handoffs[key]
		if !active || current != h || f.deliveries[h.DeliveryID] != d || time.Now().After(h.Expires) {
			f.mu.Unlock()
			http.Error(w, "handoff denied", 403)
			return
		}
		delete(f.handoffs, key)
		f.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "hy_view_" + version + "_" + d.ID, Value: d.ID + "." + token, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(expected, "https:"), SameSite: http.SameSiteStrictMode})
		page := h.Page
		if !strings.HasPrefix(page, "/") {
			page = "/" + page
		}
		if strings.HasPrefix(page, "//") || strings.Contains(page, "\n") {
			http.Error(w, "page", 400)
			return
		}
		encoded, _ := json.Marshal(page)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><title>Delivery established</title><script>location.replace(%s)</script>", encoded)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "read only", 405)
		return
	}
	supplied := r.Header.Get("Origin")
	site := r.Header.Get("Sec-Fetch-Site")
	// Cookies share a hostname across ports. Reject author A's cross-port
	// subresources/navigation even when the browser also sends B's cookie.
	if (supplied != "" && supplied != expected) || (supplied == "" && site != "same-origin" && site != "none") {
		f.observe(r, "sender-denied", version)
		http.Error(w, "same-origin delivery context required", 403)
		return
	}
	var d *delivery
	for _, cookie := range r.Cookies() {
		if !strings.HasPrefix(cookie.Name, "hy_view_"+version+"_") {
			continue
		}
		id, token, _ := strings.Cut(cookie.Value, ".")
		if selected := r.Header.Get("X-Fixture-Delivery"); selected != "" && selected != id {
			continue
		}
		f.mu.Lock()
		candidate := f.deliveries[id]
		valid := candidate != nil && cookie.Name == "hy_view_"+version+"_"+id && candidate.Kind == "renderer" && candidate.Version == version && candidate.Origin == expected && !candidate.Closed && time.Now().Before(candidate.Expires) && subtle.ConstantTimeCompare([]byte(candidate.TokenHash), []byte(digest([]byte(token)))) == 1
		owner := ""
		if candidate != nil {
			owner = candidate.Owner
		}
		f.mu.Unlock()
		if valid {
			code, retryAfter := f.ownerStatus(owner)
			if code == http.StatusOK {
				d = candidate
				break
			}
			if code != http.StatusUnauthorized {
				ownerFailure(w, code, retryAfter)
				return
			}
		}
		http.SetCookie(w, &http.Cookie{Name: cookie.Name, Path: "/", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(expected, "https:"), SameSite: http.SameSiteStrictMode})
	}
	if d == nil {
		http.Error(w, "delivery denied", 401)
		return
	}
	r.Header.Set("X-Fixture-Authorized-Delivery", d.ID)
	f.observe(r, "authorized-request", version)
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "__loader.js" {
		w.Header().Set("Content-Type", "application/javascript")
		w.Write(loaderScript)
		return
	}
	if strings.HasPrefix(name, "__") || name == "" || path.Clean("/"+name) != "/"+name || strings.Contains(name, "\\") {
		http.NotFound(w, r)
		return
	}
	file, e := f.verifiedFile(version, name)
	if e != nil {
		http.Error(w, "stored content missing or corrupt", 503)
		return
	}
	defer file.Close()
	if name == "sw.js" {
		w.Header().Set("Service-Worker-Allowed", "/")
	}
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	if strings.HasSuffix(name, ".html") {
		// Derivative integration; exact original file remains in corpus/verified.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer := &deliveryWriter{ResponseWriter: w, fixture: f, delivery: d}
		if _, e := io.Copy(writer, file); e != nil {
			return
		}
		fmt.Fprintf(writer, "<script src=\"/__loader.js\"></script>")
		return
	}
	if t := mime.TypeByExtension(filepath.Ext(name)); t != "" {
		w.Header().Set("Content-Type", t)
	}
	if strings.HasSuffix(name, ".bin") {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	// Range reads and retries use this exact verified descriptor.
	http.ServeContent(&deliveryWriter{ResponseWriter: w, fixture: f, delivery: d}, r, name, time.Time{}, file)
	f.mu.Lock()
	if d != nil && !d.Closed {
		d.Expires = time.Now().Add(30 * time.Second)
	}
	f.mu.Unlock()
}
func (f *Fixture) verifiedFile(version, name string) (*os.File, error) {
	expected, ok := f.inventories[version][name]
	if !ok {
		return nil, os.ErrNotExist
	}
	file, e := os.Open(filepath.Join(f.config.State, "verified", version, filepath.FromSlash(name)))
	if e != nil {
		return nil, e
	}
	hash := sha256.New()
	n, e := io.Copy(hash, file)
	if e != nil || n != expected.Bytes || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		file.Close()
		return nil, errors.New("stored bytes differ from immutable inventory")
	}
	if _, e = file.Seek(0, io.SeekStart); e != nil {
		file.Close()
		return nil, e
	}
	return file, nil
}
func (f *Fixture) download(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "read only", 405)
		return
	}
	if r.URL.Query().Get("path") != "" {
		http.Error(w, "exact selected object only", 403)
		return
	}
	id := r.URL.Query().Get("id")
	credential, headerDelivery := strings.CutPrefix(r.Header.Get("Authorization"), "Delivery ")
	if !headerDelivery {
		cookie, e := r.Cookie("hy_download_" + id)
		if e != nil {
			http.Error(w, "download credential required", 401)
			return
		}
		credential = cookie.Value
		origin := r.Header.Get("Origin")
		site := r.Header.Get("Sec-Fetch-Site")
		expected := f.config.Base
		if r.Host == originHost(f.config.Remote) {
			expected = f.config.Remote
		}
		if (origin != "" && origin != expected) || (origin == "" && site != "same-origin" && site != "none") {
			f.observe(r, "download-sender-denied", "")
			http.Error(w, "same-origin download context required", 403)
			return
		}
	}
	f.mu.Lock()
	d := f.deliveries[id]
	valid := d != nil && d.Kind == "download" && !d.Closed && time.Now().Before(d.Expires) && subtle.ConstantTimeCompare([]byte(d.TokenHash), []byte(digest([]byte(credential)))) == 1
	var owner, version string
	if d != nil {
		owner = d.Owner
		version = d.Version
	}
	f.mu.Unlock()
	if !valid {
		http.Error(w, "download denied", 401)
		return
	}
	if !f.requireOwner(w, owner) {
		return
	}
	if r.Host != originHost(d.Origin) {
		http.Error(w, "download origin denied", 403)
		return
	}
	file, e := f.verifiedFile(version, "attachment.bin")
	if e != nil {
		http.Error(w, "stored content missing or corrupt", 503)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=haeryong-viewer-attachment.bin")
	f.mu.Lock()
	d.Expires = time.Now().Add(30 * time.Second)
	f.mu.Unlock()
	writer := &deliveryWriter{ResponseWriter: w, fixture: f, delivery: d}
	f.observeDownload(d, "download-start", 0, "")
	http.ServeContent(writer, r, "attachment.bin", time.Time{}, file)
	f.observeDownload(d, "download-finished", writer.bytes, writer.failure)
}

type deliveryWriter struct {
	http.ResponseWriter
	fixture  *Fixture
	delivery *delivery
	bytes    int64
	failure  string
}

func (w *deliveryWriter) Write(b []byte) (int, error) {
	if code, _ := w.fixture.ownerStatus(w.delivery.Owner); code != http.StatusOK {
		w.failure = "owner authentication unavailable during stream"
		if code == http.StatusUnauthorized {
			w.failure = "owner access ended during stream"
		}
		return 0, errors.New(w.failure)
	}
	w.fixture.mu.Lock()
	closed := w.delivery.Closed
	if !closed {
		w.delivery.Expires = time.Now().Add(30 * time.Second)
	}
	w.fixture.mu.Unlock()
	if closed {
		w.failure = "delivery closed"
		return 0, errors.New(w.failure)
	}
	if w.delivery.Delay > 0 {
		time.Sleep(w.delivery.Delay)
	}
	if code, _ := w.fixture.ownerStatus(w.delivery.Owner); code != http.StatusOK {
		w.failure = "owner authentication unavailable during stream"
		if code == http.StatusUnauthorized {
			w.failure = "owner access ended during stream"
		}
		return 0, errors.New(w.failure)
	}
	w.fixture.mu.Lock()
	closed = w.delivery.Closed
	w.fixture.mu.Unlock()
	if closed {
		w.failure = "delivery closed"
		return 0, errors.New(w.failure)
	}
	n, e := w.ResponseWriter.Write(b)
	if w.bytes == 0 && n > 0 {
		w.fixture.observeDownload(w.delivery, "download-first-byte", int64(n), "")
	}
	w.bytes += int64(n)
	if e != nil {
		w.failure = e.Error()
	}
	return n, e
}
func (f *Fixture) observeDownload(d *delivery, kind string, n int64, message string) {
	if d.Kind != "download" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observations = append(f.observations, Observation{Time: time.Now(), Kind: kind, Version: d.Version, Path: "attachment.bin", DeliveryID: d.ID, Bytes: n, Error: message})
}
func (f *Fixture) context(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	binding := r.Header.Get("X-Page-Binding")
	if id == "" || binding == "" {
		http.Error(w, "binding required", 400)
		return
	}
	media := strings.Split(r.Header.Get("Content-Type"), ";")[0]
	if r.Method == "POST" && media != "text/plain" && media != "application/json" {
		http.Error(w, "JSON or UTF-8 text required", 400)
		return
	}
	// Map membership is brief; each transfer serializes its own IO and lifetime.
	created := false
	f.mu.Lock()
	t := f.contexts[id]
	if t == nil && r.Method == "POST" {
		t = &contextTransfer{Owner: bearer(r), Binding: binding, Media: media, File: filepath.Join(f.config.State, "context-"+random())}
		t.mu.Lock()
		f.contexts[id] = t
		created = true
	}
	f.mu.Unlock()
	if t == nil {
		http.Error(w, "binding", 409)
		return
	}
	if !created {
		t.mu.Lock()
	}
	defer t.mu.Unlock()
	if t.Deleted {
		http.Error(w, "binding", 409)
		return
	}
	if !f.requireOwner(w, bearer(r)) {
		if created {
			t.Deleted = true
			f.mu.Lock()
			if f.contexts[id] == t {
				delete(f.contexts, id)
			}
			f.mu.Unlock()
		}
		return
	}
	switch r.Method {
	case "POST":
		if !created {

			if t.Owner != bearer(r) || t.Binding != binding || t.Media != media {
				http.Error(w, "binding conflict", 409)
				return
			}
			reply(w, 200, map[string]any{"offset": t.Offset})
			return
		}
		file, e := os.OpenFile(t.File, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Deleted = true
			f.mu.Lock()
			if f.contexts[id] == t {
				delete(f.contexts, id)
			}
			f.mu.Unlock()
			http.Error(w, "storage", 503)
			return
		}
		file.Close()
		reply(w, 201, map[string]any{"offset": 0})
	case "PUT":
		if t == nil || t.Owner != bearer(r) || t.Binding != binding || t.Sealed {
			http.Error(w, "binding", 409)
			return
		}
		offset, e := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
		if e != nil {
			http.Error(w, "offset", 400)
			return
		}
		b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 256*1024))
		if e != nil {
			http.Error(w, "chunk transport", 413)
			return
		}
		if !f.requireOwner(w, bearer(r)) {
			return
		}
		if digest(b) != r.Header.Get("X-Bytes-SHA256") {
			http.Error(w, "digest", 409)
			return
		}
		if offset < t.Offset {
			file, e := os.Open(t.File)
			if e != nil {
				http.Error(w, "storage", 503)
				return
			}
			existing := make([]byte, len(b))
			_, e = file.ReadAt(existing, offset)
			file.Close()
			if e != nil || offset+int64(len(b)) > t.Offset || !bytes.Equal(existing, b) {
				http.Error(w, "replay conflict", 409)
				return
			}
			reply(w, 200, map[string]any{"offset": t.Offset})
			return
		}
		if offset != t.Offset {
			http.Error(w, "offset gap", 409)
			return
		}
		file, e := os.OpenFile(t.File, os.O_WRONLY|os.O_APPEND, 0600)
		if e != nil {
			http.Error(w, "storage", 503)
			return
		}
		n, e := file.Write(b)
		file.Close()
		if e != nil {
			http.Error(w, "storage", 503)
			return
		}
		t.Offset += int64(n)
		reply(w, 200, map[string]any{"offset": t.Offset})
	case "PATCH":
		if t == nil || t.Owner != bearer(r) || t.Binding != binding {
			http.Error(w, "binding", 409)
			return
		}
		file, e := os.Open(t.File)
		if e != nil {
			http.Error(w, "storage", 503)
			return
		}
		hash := sha256.New()
		_, e = io.Copy(hash, file)
		if e == nil {
			_, e = file.Seek(0, io.SeekStart)
		}
		if e == nil {
			reader := bufio.NewReader(file)
			for {
				r, n, readErr := reader.ReadRune()
				if readErr == io.EOF {
					break
				}
				if readErr != nil {
					e = readErr
					break
				}
				if r == utf8.RuneError && n == 1 {
					e = errors.New("invalid UTF-8")
					break
				}
			}
		}
		if e == nil && t.Media == "application/json" {
			_, e = file.Seek(0, io.SeekStart)
			if e == nil {
				decoder := json.NewDecoder(file)
				var depth, roots int
				for {
					token, readErr := decoder.Token()
					if readErr == io.EOF {
						break
					}
					if readErr != nil {
						e = readErr
						break
					}
					if depth == 0 {
						roots++
					}
					if delim, ok := token.(json.Delim); ok {
						if delim == '{' || delim == '[' {
							depth++
						} else {
							depth--
						}
					}
					if roots > 1 {
						e = errors.New("multiple JSON values")
						break
					}
				}
				if depth != 0 || roots != 1 {
					e = errors.New("incomplete JSON")
				}
			}
		}
		file.Close()
		sum := hex.EncodeToString(hash.Sum(nil))
		if e != nil || sum != r.Header.Get("X-Bytes-SHA256") {
			http.Error(w, "digest or text validation", 409)
			return
		}
		if !f.requireOwner(w, bearer(r)) {
			return
		}
		t.Sealed = true
		t.Digest = sum
		if t.Ref == "" {
			t.Ref = "temporary-context-" + random()
		}
		reply(w, 200, map[string]any{"context_ref": t.Ref, "bytes_sha256": sum, "bytes": t.Offset, "temporary": true})
	case "DELETE":
		if t == nil || t.Owner != bearer(r) || t.Binding != binding {
			http.Error(w, "binding", 409)
			return
		}
		if !t.Sealed {
			if e := os.Remove(t.File); e != nil {
				http.Error(w, "storage", 503)
				return
			}
			t.Deleted = true
			f.mu.Lock()
			if f.contexts[id] == t {
				delete(f.contexts, id)
			}
			f.mu.Unlock()
		}
		w.WriteHeader(204)
	default:
		http.Error(w, "method", 405)
	}
}
