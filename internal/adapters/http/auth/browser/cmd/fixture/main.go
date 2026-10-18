// Command fixture is a disposable loopback browser proof adapter, not a release
// entrypoint. Its private stdin owns bootstrap/test policy control; HTTP always
// executes the production session service, SQLite storage and auth middleware.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	httpimports "haeryong.local/haeryong/internal/adapters/http/imports"
	httpv1 "haeryong.local/haeryong/internal/adapters/http/v1"
	appauth "haeryong.local/haeryong/internal/application/auth"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	storage "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

type policyWriter struct {
	http.ResponseWriter
	done bool
}

// apiRoutes dispatches between the query and import adapters exactly like
// the production router composition.
type apiRoutes struct {
	queries *httpv1.Handler
	imports http.Handler
}

func (m *apiRoutes) Handles(r *http.Request) bool {
	return m.queries.Handles(r) || m.imports.(interface{ Handles(*http.Request) bool }).Handles(r)
}

func (m *apiRoutes) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m.queries.Handles(r) {
		m.queries.ServeHTTP(w, r)
		return
	}
	m.imports.ServeHTTP(w, r)
}

// redactedControlError classifies private-control failures without echoing
// codes, origins, IDs or other request material.
func redactedControlError(err error) string {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return "pairing_not_found"
	case errors.Is(err, domain.ErrPrecondition):
		return "stale_precondition"
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrTransition):
		return "state_conflict"
	case errors.Is(err, sec.ErrInvalid), errors.Is(err, sec.ErrOrigin):
		return "code_or_origin_mismatch"
	default:
		return "error"
	}
}

func (w *policyWriter) prepare() {
	if !w.done {
		w.done = true
		w.Header().Set("Content-Security-Policy", w.Header().Get("Content-Security-Policy")+"; frame-src 'self'")
	}
}
func (w *policyWriter) WriteHeader(status int) { w.prepare(); w.ResponseWriter.WriteHeader(status) }
func (w *policyWriter) Write(data []byte) (int, error) {
	w.prepare()
	return w.ResponseWriter.Write(data)
}

func run() error {
	dir := flag.String("directory", "", "explicit private disposable directory")
	port := flag.Int("port", 0, "loopback listener port; 0 only for a new harness")
	clockOffset := flag.Int64("clock-offset-seconds", 0, "synthetic clock offset retained by the harness across restarts")
	alias := flag.String("alias", "", "additional trusted origin (same model as the private HTTPS proxy forwarding to this listener)")
	flag.Parse()
	if *dir == "" {
		return errors.New("directory required")
	}
	selection := service.Selection{Profile: "browser-proof", DataDir: filepath.Join(*dir, "profile")}
	if _, err := os.Stat(filepath.Join(selection.DataDir, "profile.json")); errors.Is(err, os.ErrNotExist) {
		if _, err := service.Initialize(selection); err != nil {
			return err
		}
	}
	writer, err := service.OpenWriter(selection)
	if err != nil {
		return err
	}
	defer writer.Close()
	if err := writer.PrepareStorageBootstrap(nil); err != nil {
		return err
	}
	store := foundation.New(nil)
	_, closeStore, err := store.Prepare(context.Background(), writer.Profile)
	if err != nil {
		return err
	}
	defer closeStore()
	if err := writer.MarkStorageReady(nil); err != nil {
		return err
	}
	core := appauth.New(store)
	var offset atomic.Int64
	offset.Store(int64(time.Duration(*clockOffset) * time.Second))
	core.Now = func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		return err
	}
	_, boundPort, _ := net.SplitHostPort(listener.Addr().String())
	local := "http://127.0.0.1:" + boundPort
	remote := "http://localhost:" + boundPort
	hosts := []string{strings.TrimPrefix(local, "http://"), strings.TrimPrefix(remote, "http://")}
	origins := []string{local, remote}
	if *alias != "" {
		parsed, err := url.Parse(*alias)
		if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("alias must be an exact loopback http origin")
		}
		hosts = append(hosts, parsed.Host)
		origins = append(origins, *alias)
	}
	// The same product query and import routes as the daemon keep
	// capabilities live for shell compatibility gating and let browser
	// proofs exercise the real streamed import/publication surface.
	queries := query.New(store, core, store)
	imports := importer.New(store, core, store, staging.New())
	api := &apiRoutes{queries: httpv1.New(queries, nil, nil), imports: httpimports.New(imports)}
	handler, err := httpauth.New(core, httpauth.Policy{Hosts: hosts, Origins: origins}, api)
	if err != nil {
		listener.Close()
		return err
	}
	// The import worker owns durable store writes: it is cancelled AND
	// awaited before the deferred storage close runs (LIFO below), on every
	// exit path including SIGTERM, so no seal/publication work races the
	// closing store. The await has no deadline: an unsettled worker leaves
	// the store unclosed rather than pretending quiescence.
	workerCtx, stopWorker := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	workerDone := make(chan error, 1)
	go func() { workerDone <- imports.Run(workerCtx) }()
	defer func() {
		stopWorker()
		if workerError := <-workerDone; workerError != nil && !errors.Is(workerError, context.Canceled) {
			fmt.Fprintln(os.Stderr, "import worker:", workerError)
		}
	}()
	outer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/probe-child" && r.Method == "GET" && (r.Host == strings.TrimPrefix(local, "http://") || r.Host == strings.TrimPrefix(remote, "http://")) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; connect-src http:; frame-ancestors 'self'")
			fmt.Fprint(w, `<!doctype html><title>Synthetic isolated auth probe</title><p>Not the product renderer.</p><script>
window.boundary={};for(const [name,read] of Object.entries({parentDOM:()=>parent.document.body.textContent,parentStorage:()=>parent.localStorage.length,ownStorage:()=>localStorage.length,parentScript:()=>parent.eval('client')})){try{read();boundary[name]=true}catch{boundary[name]=false}}
parent.postMessage({type:'management',action:'revoke',method:'DELETE',path:'/api/v1/session'},'*');
fetch('/api/v1/auth/pairings/arbitrary/approve',{method:'POST',mode:'no-cors',headers:{'Content-Type':'text/plain'},body:'{}'}).catch(()=>{});
</script>`)
			return
		}
		if r.URL.Path == "/" || r.URL.Path == "/connect" || r.URL.Path == "/reader" {
			handler.ServeHTTP(&policyWriter{ResponseWriter: w}, r)
			return
		}
		handler.ServeHTTP(w, r)
	})
	server := &http.Server{Handler: outer, ReadHeaderTimeout: 3 * time.Second, IdleTimeout: 5 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server.BaseContext = func(net.Listener) context.Context { return ctx }
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	json.NewEncoder(os.Stdout).Encode(map[string]any{"fixture": "task00301-production-auth-shell", "endpoint": local, "peer_endpoint": remote, "pid": os.Getpid()})
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			var input struct {
				Action   string `json:"action"`
				ID       string `json:"pairing_id"`
				Code     string `json:"code"`
				Seconds  int64  `json:"seconds"`
				Remember *bool  `json:"remember"`
			}
			reason := ""
			err := json.Unmarshal(scanner.Bytes(), &input)
			if err == nil {
				switch input.Action {
				case "seed_lists":
					// Labelled synthetic rows exercise the production read contract;
					// the later project/lifecycle editing UI is not implemented here.
					err = store.Write(context.Background(), func(tx storage.WriteTx) error {
						projects := make([]string, 2)
						for i, name := range []string{"Fixture project A", "Fixture project B"} {
							id, e := service.NewID()
							if e != nil {
								return e
							}
							projects[i] = id
							if e := tx.CreateProject(domain.Project{ID: id, Name: name, Lifecycle: "active", Revision: 1, PermissionsRevision: 1}); e != nil {
								return e
							}
						}
						for _, row := range []struct {
							project          int
							title, lifecycle string
						}{
							{0, "Fixture active A", "active"}, {0, "Fixture archived A", "archived"},
							{1, "Fixture active B", "active"}, {0, "Fixture hidden trashed document", "trash"},
						} {
							id, e := service.NewID()
							if e != nil {
								return e
							}
							if e := tx.CreateDocument(domain.Document{ID: id, ProjectID: projects[row.project], Title: row.title, Lifecycle: row.lifecycle, Revision: 1}); e != nil {
								return e
							}
						}
						for i := 0; i < 67; i++ {
							id, e := service.NewID()
							if e != nil {
								return e
							}
							if e := tx.CreateDocument(domain.Document{ID: id, ProjectID: projects[0], Title: fmt.Sprintf("Fixture active A %02d", i), Lifecycle: "active", Revision: 1}); e != nil {
								return e
							}
						}
						return nil
					})
				case "advance":
					offset.Add(int64(time.Duration(input.Seconds) * time.Second))
				case "approve":
					rows, e := core.BootstrapPairings(context.Background())
					err = e
					found := false
					for _, p := range rows {
						if p.ID == input.ID {
							found = true
							id, e := service.NewID()
							if e != nil {
								err = e
								break
							}
							_, err = core.BootstrapDecide(context.Background(), domain.PairingDecision{ID: p.ID, Code: input.Code, Origin: p.Origin, ETag: p.ETag, OperationID: id, Action: "approve"})
							break
						}
					}
					if err == nil && !found {
						err = domain.ErrNotFound
					}
					if err != nil {
						reason = redactedControlError(err)
					}
				case "expiry", "remember_default":
					reason = "configuration"
					config, e := store.Configuration(context.Background())
					err = e
					if err == nil {
						var data map[string]any
						err = json.Unmarshal(config.Value, &data)
						if err == nil {
							security := data["security"].(map[string]any)
							if input.Action == "remember_default" {
								if input.Remember == nil {
									err = errors.New("remember bool required")
								} else {
									security["remember_device_default"] = *input.Remember
								}
							} else if input.Seconds == 0 {
								security["session_absolute_seconds"] = nil
							} else {
								security["session_absolute_seconds"] = input.Seconds
							}
							if err == nil {
								config.Value, err = json.Marshal(data)
							}
							config.Actor = "browser-harness"
							if err == nil {
								err = store.Write(context.Background(), func(tx storage.WriteTx) error { return tx.SetConfiguration(config, config.Revision) })
							}
						}
					}
				default:
					reason = "unknown_control"
					err = errors.New("unknown private control")
				}
			} else {
				reason = "malformed_control"
			}
			if err != nil {
				if reason == "" || reason == "error" {
					reason = redactedControlError(err)
				}
				json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": false, "reason": reason})
			} else {
				json.NewEncoder(os.Stdout).Encode(map[string]bool{"ok": true})
			}
		}
	}()
	select {
	case <-ctx.Done():
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	return server.Shutdown(shutdown)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "browser fixture failed; private details suppressed")
		os.Exit(1)
	}
}
