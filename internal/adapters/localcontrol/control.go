// Package localcontrol adapts the service lifecycle to protected loopback HTTP.
// This is OS-user local control, not browser pairing or product API authority.
package localcontrol

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
)

const livePath = "/health/live"
const identityPath = "/local/v1/identity"
const stopPath = "/local/v1/stop"

// HTTP implements service.Endpoint. Its lifetime is independent of any desktop.
type HTTP struct {
	Auth           *appauth.Service
	Product        func(string) (http.Handler, error)
	server         *http.Server
	listener       net.Listener
	done           chan error
	cancelRequests context.CancelFunc
	requestMu      sync.Mutex
	closing        bool
	active         sync.WaitGroup
}

func signature(key, domain, nonce string, body []byte) string {
	bytes, _ := hex.DecodeString(key) // Profile loading validates the key once.
	mac := hmac.New(sha256.New, bytes)
	mac.Write([]byte(domain))
	mac.Write([]byte{0})
	mac.Write([]byte(nonce))
	mac.Write([]byte{0})
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func requestProof(key, nonce, method, path, instance string) string {
	return signature(key, "request", nonce, []byte(method+"\n"+path+"\n"+instance))
}

func (h *HTTP) Start(ctx context.Context, p service.Profile, id service.Identity, port int, stop func()) (service.Identity, error) {
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return id, fmt.Errorf("configured listener unavailable (no fallback): %w", err)
	}
	id.Endpoint = "http://" + listener.Addr().String()
	id.ControlReady = true
	mux := http.NewServeMux()
	mux.HandleFunc(livePath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"live":true}`+"\n")
	})
	respond := func(w http.ResponseWriter, r *http.Request, method string, after func()) {
		if r.Method != method {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		nonce := r.Header.Get("X-Haeryong-Nonce")
		decoded, err := hex.DecodeString(nonce)
		if err != nil || len(decoded) != 32 || !hmac.Equal([]byte(r.Header.Get("X-Haeryong-Proof")), []byte(requestProof(p.ControlKey, nonce, r.Method, r.URL.Path, id.InstanceID))) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// The instance is in every request proof. Captured requests from a previous
		// daemon cannot stop a restarted daemon. Stop is idempotent within an instance.
		body, err := json.Marshal(id)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Haeryong-Proof", signature(p.ControlKey, "response", nonce, body))
		w.Write(body)
		if after != nil {
			after()
		}
	}
	mux.HandleFunc(identityPath, func(w http.ResponseWriter, r *http.Request) { respond(w, r, http.MethodGet, nil) })
	mux.HandleFunc(stopPath, func(w http.ResponseWriter, r *http.Request) { respond(w, r, http.MethodPost, stop) })
	if id.Storage.Ready && h.Auth != nil {
		h.mountBootstrap(mux, p, id)
	}
	var product http.Handler
	if id.Storage.Ready && h.Product != nil {
		product, err = h.Product(id.Endpoint)
		if err != nil {
			listener.Close()
			return id, err
		}
	}
	expectedHost := listener.Addr().String()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.requestMu.Lock()
		if h.closing {
			h.requestMu.Unlock()
			w.WriteHeader(503)
			return
		}
		h.active.Add(1)
		h.requestMu.Unlock()
		defer h.active.Done()
		w.Header().Set("Cache-Control", "no-store")
		// Control is not a browser API. Reject browser origins and alternate Host
		// values even if they can reach loopback; no proxy/forwarded trust exists.
		if product != nil && !strings.HasPrefix(r.URL.Path, "/local/v1") && r.URL.Path != livePath {
			product.ServeHTTP(w, r)
			return
		}
		if r.Host != expectedHost || r.Header.Get("Origin") != "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
	requests, cancelRequests := context.WithCancel(ctx)
	h.cancelRequests = cancelRequests
	h.listener = listener
	h.server = &http.Server{Handler: handler, BaseContext: func(net.Listener) context.Context { return requests }, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	h.done = make(chan error, 1)
	go func() {
		err := h.server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		h.done <- err
	}()
	return id, nil
}

func (h *HTTP) Done() <-chan error { return h.done }
func (h *HTTP) Close(ctx context.Context) error {
	h.requestMu.Lock()
	h.closing = true
	h.requestMu.Unlock()
	if h.cancelRequests != nil {
		h.cancelRequests()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := h.server.Shutdown(ctx)
	if err != nil {
		err = errors.Join(err, h.server.Close())
	}
	h.active.Wait()
	return err
}

func endpointURL(endpoint string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1024 || port > 65535 || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.Contains(u.Host, "%") {
		return nil, errors.New("local endpoint must be an exact http://127.0.0.1:<port> origin")
	}
	return u, nil
}

// Verify authenticates the server's challenge response, then compares all
// identities with protected local discovery. The shared secret is never sent to
// a listener, redirect target, HTTP proxy, argv, log or public health response.
func Verify(ctx context.Context, p service.Profile, expected service.Identity) (service.Identity, error) {
	return call(ctx, p, expected, http.MethodGet, identityPath)
}
func Stop(ctx context.Context, p service.Profile, expected service.Identity) error {
	if _, err := Verify(ctx, p, expected); err != nil {
		return err
	}
	_, err := call(ctx, p, expected, http.MethodPost, stopPath)
	return err
}

func call(ctx context.Context, p service.Profile, expected service.Identity, method, path string) (service.Identity, error) {
	u, err := endpointURL(expected.Endpoint)
	if err != nil {
		return service.Identity{}, err
	}
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		return service.Identity{}, err
	}
	nonce := hex.EncodeToString(nonceBytes)
	req, err := http.NewRequestWithContext(ctx, method, u.String()+path, nil)
	if err != nil {
		return service.Identity{}, err
	}
	req.Header.Set("X-Haeryong-Nonce", nonce)
	req.Header.Set("X-Haeryong-Proof", requestProof(p.ControlKey, nonce, method, path, expected.InstanceID))
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return service.Identity{}, fmt.Errorf("local service unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return service.Identity{}, fmt.Errorf("local identity not verified: HTTP %d", response.StatusCode)
	}
	// Local-control responses have a finite protocol envelope, not content limits.
	body, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil {
		return service.Identity{}, err
	}
	if len(body) > 16384 || !hmac.Equal([]byte(response.Header.Get("X-Haeryong-Proof")), []byte(signature(p.ControlKey, "response", nonce, body))) {
		return service.Identity{}, errors.New("local server proof invalid; identity not verified")
	}
	var actual service.Identity
	if err := json.Unmarshal(body, &actual); err != nil {
		return actual, err
	}
	if actual != expected || actual.LibraryID != p.LibraryID || actual.ProfileID != p.ProfileID || actual.API != service.APIVersion || !actual.ControlReady {
		return service.Identity{}, errors.New("local library/profile/API/instance identity mismatch")
	}
	return actual, nil
}
