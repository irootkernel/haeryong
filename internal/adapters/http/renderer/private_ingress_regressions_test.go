package renderer_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	httprenderer "haeryong.local/haeryong/internal/adapters/http/renderer"
	httpv1 "haeryong.local/haeryong/internal/adapters/http/v1"
	"haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/render"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func privateNetwork(f *fixture, change func(map[string]any)) error {
	return f.store.Write(ctx, func(tx app.WriteTx) error {
		configuration, err := tx.Configuration()
		if err != nil {
			return err
		}
		var value map[string]any
		if err := json.Unmarshal(configuration.Value, &value); err != nil {
			return err
		}
		change(value["network"].(map[string]any))
		configuration.Value, err = json.Marshal(value)
		if err != nil {
			return err
		}
		return tx.SetConfiguration(configuration, configuration.Revision)
	})
}

var _ = It("refuses cold unmapped private controls without a constructor, origin files or receipts", Label("persistence"), func() {
	f := newFixture()
	const parent = "https://node.private:9443"
	Expect(privateNetwork(f, func(network map[string]any) {
		network["remote_enabled"] = true
		network["external_base_url"] = parent
		network["allowed_origins"] = []any{"http://127.0.0.1:9270", parent}
		network["renderer_mappings"] = []any{}
	})).To(Succeed())
	calls := 0
	lazy := render.NewLazy(func() (*render.Service, error) { calls++; return nil, errors.New("must remain cold") })
	DeferCleanup(func() { Expect(lazy.Shutdown()).To(Succeed()) })
	handler := httpv1.New(query.New(f.store, f.auth, f.store), lazy, func() (string, string) { return "http://127.0.0.1:9270", parent })
	keys := []string{}
	for _, item := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/v1/capabilities", 200},
		{"GET", "/api/v1/versions/" + f.version + "/render-bootstrap", 200},
		{"POST", "/api/v1/versions/" + f.version + "/render-sessions", 503},
		{"POST", "/api/v1/versions/" + f.version + "/render-sessions/" + key() + "/renew", 503},
	} {
		request := httptest.NewRequest(item.method, parent+item.path, strings.NewReader("{}"))
		request.Header.Set("Origin", parent)
		request.Header.Set("Authorization", "Bearer "+f.token)
		operation := key()
		keys = append(keys, operation)
		request.Header.Set("Idempotency-Key", operation)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		Expect(response.Code).To(Equal(item.status), item.path)
	}
	Expect(calls).To(BeZero())
	for _, path := range []string{"app-state.json", "origin-state"} {
		_, err := os.Stat(filepath.Join(f.state, path))
		Expect(os.IsNotExist(err)).To(BeTrue(), path)
	}
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		session, err := f.auth.AuthenticateWrite(ctx, tx, f.token)
		if err != nil {
			return err
		}
		for _, operation := range keys {
			_, err := tx.Receipt(domain.OperationKey{PrincipalID: session.PrincipalID, OperationID: operation})
			if !errors.Is(err, domain.ErrNotFound) {
				return fmt.Errorf("unexpected cold control receipt: %v", err)
			}
		}
		return nil
	})).To(Succeed())
})

// Change the DB-current route immediately after the first actual body write.
// This leaves listener/restart timing out of the property under test.
type routeTransitionWriter struct {
	http.ResponseWriter
	once       *sync.Once
	transition func()
}

func (w routeTransitionWriter) Write(body []byte) (int, error) {
	n, err := w.ResponseWriter.Write(body)
	if n > 0 {
		w.once.Do(w.transition)
	}
	return n, err
}

var _ = It("stops private byte streaming at the next guarded write after route removal or disablement", Label("persistence"), func() {
	for _, disable := range []bool{false, true} {
		f := newFixture()
		Expect(f.core.Shutdown()).To(Succeed())
		var core *render.Service
		var once sync.Once
		transitioned := make(chan error, 1)
		var armed atomic.Bool
		var err error
		core, err = render.New(f.control, auth.New(f.control), f.store, render.Options{
			AppStateDir: f.state, Lease: time.Minute,
			ManagementOrigin: func() string { return "http://127.0.0.1:9270" },
			NewServer: func(origin string, binding render.Binding) render.Server {
				server := httprenderer.New(core, origin, binding)
				original := server.Handler
				server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/large.bin" && armed.Load() {
						w = routeTransitionWriter{w, &once, func() {
							transitioned <- privateNetwork(f, func(network map[string]any) {
								if disable {
									network["remote_enabled"] = false
								} else {
									network["renderer_mappings"] = []any{}
								}
							})
						}}
					}
					original.ServeHTTP(w, r)
				})
				return server
			},
		})
		Expect(err).NotTo(HaveOccurred())
		f.core = core
		DeferCleanup(func() { Expect(core.Shutdown()).To(Succeed()) })
		local := f.open()
		const parent, remote = "https://node.private:9443", "https://node.private:9491"
		pool := 9401
		if strings.HasSuffix(local.Origin, ":9401") {
			pool = 9402
		}
		Expect(privateNetwork(f, func(network map[string]any) {
			network["renderer_pool"] = map[string]any{"base_port": pool, "count": 1}
			network["remote_enabled"] = true
			network["external_base_url"] = parent
			network["allowed_origins"] = []any{"http://127.0.0.1:9270", parent}
			network["renderer_mappings"] = []any{map[string]any{"version_id": f.version, "origin": remote, "upstream": local.Origin, "reassignable": false}}
		})).To(Succeed())
		ingress := render.Ingress{Remote: true, ParentOrigin: parent}
		opened, err := core.OpenWithIngress(ctx, f.token, key(), f.version, ingress)
		Expect(err).NotTo(HaveOccurred())
		var delivery delivery
		Expect(json.Unmarshal(opened.Body, &delivery)).To(Succeed())
		handoff, err := http.NewRequest("POST", local.Origin+"/__haeryong/handoff", strings.NewReader(url.Values{"secret": {delivery.Handoff.Secret}}.Encode()))
		Expect(err).NotTo(HaveOccurred())
		handoff.Host = "node.private:9491"
		handoff.Header.Set("Origin", parent)
		handoff.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		accepted, err := f.client.Do(handoff)
		Expect(err).NotTo(HaveOccurred())
		accepted.Body.Close()
		Expect(accepted.StatusCode).To(Equal(200))
		cookies := accepted.Cookies()
		Expect(cookies).To(HaveLen(1))
		request, err := http.NewRequest("GET", local.Origin+"/large.bin", nil)
		Expect(err).NotTo(HaveOccurred())
		request.Host = "node.private:9491"
		request.Header.Set("Origin", remote)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		request.AddCookie(cookies[0])
		armed.Store(true)
		response, err := f.client.Do(request)
		Expect(err).NotTo(HaveOccurred())
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		Expect(<-transitioned).To(Succeed())
		Expect(response.StatusCode).To(Equal(200))
		Expect(readErr).To(HaveOccurred())
		Expect(body).To(HaveLen(32 * 1024))
		Expect(string(body)).To(Equal(strings.Repeat("0123456789abcdef", 2048)))
		// Re-enabling the same route never revives the terminated delivery.
		Expect(privateNetwork(f, func(network map[string]any) {
			network["remote_enabled"] = true
			network["renderer_mappings"] = []any{map[string]any{"version_id": f.version, "origin": remote, "upstream": local.Origin, "reassignable": false}}
		})).To(Succeed())
		_, err = core.RenewWithIngress(ctx, f.token, f.version, delivery.ID, ingress)
		Expect(err).To(HaveOccurred())
	}
})
