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
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"haeryong.local/haeryong/internal/adapters/foundation"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	storage "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type policyWriter struct {
	http.ResponseWriter
	done bool
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
	core.Now = func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		return err
	}
	_, boundPort, _ := net.SplitHostPort(listener.Addr().String())
	local := "http://127.0.0.1:" + boundPort
	remote := "http://localhost:" + boundPort
	handler, err := httpauth.New(core, httpauth.Policy{Hosts: []string{strings.TrimPrefix(local, "http://"), strings.TrimPrefix(remote, "http://")}, Origins: []string{local, remote}}, nil)
	if err != nil {
		listener.Close()
		return err
	}
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
	json.NewEncoder(os.Stdout).Encode(map[string]any{"fixture": "task00203-production-auth", "endpoint": local, "peer_endpoint": remote, "pid": os.Getpid()})
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			var input struct {
				Action  string `json:"action"`
				ID      string `json:"pairing_id"`
				Code    string `json:"code"`
				Seconds int64  `json:"seconds"`
			}
			err := json.Unmarshal(scanner.Bytes(), &input)
			if err == nil {
				switch input.Action {
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
				case "expiry":
					config, e := store.Configuration(context.Background())
					err = e
					if err == nil {
						var data map[string]any
						err = json.Unmarshal(config.Value, &data)
						if err == nil {
							security := data["security"].(map[string]any)
							if input.Seconds == 0 {
								security["session_absolute_seconds"] = nil
							} else {
								security["session_absolute_seconds"] = input.Seconds
							}
							config.Value, err = json.Marshal(data)
							config.Actor = "browser-harness"
							if err == nil {
								err = store.Write(context.Background(), func(tx storage.WriteTx) error { return tx.SetConfiguration(config, config.Revision) })
							}
						}
					}
				default:
					err = errors.New("unknown private control")
				}
			}
			json.NewEncoder(os.Stdout).Encode(map[string]bool{"ok": err == nil})
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
