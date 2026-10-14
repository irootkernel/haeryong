// Disposable auth-ingress HTTP proof, not a production service.
package authingress

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

func handler(s *store, bootstrap string, hosts, origins map[string]bool) http.Handler {
	type window struct {
		start           time.Time
		count, inFlight int
	}
	var createsMu sync.Mutex
	creates := map[string]*window{}
	var failuresMu sync.Mutex
	type failureWindow struct {
		start           time.Time
		count, inFlight int
	}
	failures := map[string]*failureWindow{}
	reserveAttempt := func(remote string) (func(int), int) {
		client, _, err := net.SplitHostPort(remote)
		if err != nil {
			client = "unknown-client"
		}
		failuresMu.Lock()
		defer failuresMu.Unlock()
		now := s.now()
		for key, entry := range failures {
			if now.Sub(entry.start) >= time.Minute {
				delete(failures, key)
			}
		}
		entry := failures[client]
		if entry == nil {
			entry = &failureWindow{start: now}
			failures[client] = entry
		}
		if entry.count >= 8 {
			return nil, 60
		}
		if entry.count+entry.inFlight >= 8 {
			return nil, 2
		}
		entry.inFlight++
		// The captured window survives a minute rollover until this request finishes.
		return func(status int) {
			failuresMu.Lock()
			defer failuresMu.Unlock()
			entry.inFlight--
			if status == 400 || status == 403 {
				entry.count++
			}
		}, 0
	}
	reserveCreate := func(remote string) (func(bool), bool) {
		client, _, err := net.SplitHostPort(remote)
		if err != nil {
			client = "unknown-client"
		}
		createsMu.Lock()
		defer createsMu.Unlock()
		now := s.now()
		for key, entry := range creates {
			if now.Sub(entry.start) >= time.Minute {
				delete(creates, key)
			}
		}
		entry := creates[client]
		if entry == nil {
			entry = &window{start: now}
			creates[client] = entry
		}
		if entry.count+entry.inFlight >= 2 {
			return nil, false
		}
		entry.inFlight++
		return func(committed bool) {
			createsMu.Lock()
			defer createsMu.Unlock()
			entry.inFlight--
			if committed {
				entry.count++
			}
		}, true
	}
	jsonReply := func(w http.ResponseWriter, status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(value)
	}
	storeError := func(w http.ResponseWriter, err error, deniedStatus int, deniedText string) {
		if errors.Is(err, errDenied) {
			http.Error(w, deniedText, deniedStatus)
			return
		}
		w.Header().Set("Retry-After", "2")
		http.Error(w, "store temporarily unavailable", http.StatusServiceUnavailable)
	}
	token := func(r *http.Request) string {
		value := r.Header.Get("Authorization")
		if !strings.HasPrefix(value, "Bearer ") {
			return ""
		}
		return strings.TrimPrefix(value, "Bearer ")
	}
	mux := http.NewServeMux()
	// Fixture-only browser observations; this is not a production API or a gate verdict.
	mux.HandleFunc("POST /private/device-proof", func(w http.ResponseWriter, r *http.Request) {
		credential := token(r)
		if _, err := s.session(credential); err != nil {
			storeError(w, err, 401, "not authorized")
			return
		}
		var input struct {
			Phase           string `json:"phase"`
			Remembered      bool   `json:"remembered"`
			RememberedUI    bool   `json:"remembered_ui"`
			PrivateRead     bool   `json:"private_read"`
			UnpairedDenials bool   `json:"unpaired_denials"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || (input.Phase != "connected" && input.Phase != "reloaded" && input.Phase != "later-load") {
			http.Error(w, "invalid device observation", 400)
			return
		}
		payload, err := json.Marshal(struct {
			Observation any    `json:"observation"`
			UserAgent   string `json:"user_agent"`
		}{input, r.UserAgent()})
		if err != nil {
			storeError(w, err, 401, "not authorized")
			return
		}
		// The SELECT binds the row to the current credential and rechecks revocation
		// atomically with the write; no client-supplied device ID is authoritative.
		result, err := s.db.Exec(`INSERT INTO device_proofs(device_id,phase,payload,updated)
			SELECT id,?,?,? FROM sessions WHERE token_hash=? AND revoked=0 AND (expires=0 OR expires>?)
			ON CONFLICT(device_id,phase) DO UPDATE SET payload=excluded.payload,updated=excluded.updated`,
			input.Phase, string(payload), s.now().UnixNano(), hash(credential), s.now().UnixNano())
		if err != nil {
			storeError(w, err, 401, "not authorized")
			return
		}
		count, err := result.RowsAffected()
		if err != nil {
			storeError(w, err, 401, "not authorized")
			return
		}
		if count != 1 {
			http.Error(w, "not authorized", 401)
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /pair/poll", func(w http.ResponseWriter, r *http.Request) {
		var input struct{ ID, Secret string }
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, "invalid poll", 400)
			return
		}
		decision, err := s.poll(input.ID, input.Secret)
		if errors.Is(err, errTooSoon) {
			w.Header().Set("Retry-After", "2")
			http.Error(w, "poll too soon", 429)
			return
		}
		if err != nil {
			storeError(w, err, 403, "poll denied")
			return
		}
		jsonReply(w, 200, map[string]string{"decision": decision})
	})
	mux.HandleFunc("POST /pair", func(w http.ResponseWriter, r *http.Request) {
		if !origins[r.Header.Get("Origin")] {
			http.Error(w, "origin required", http.StatusForbidden)
			return
		}
		var input struct{ Label string }
		if json.NewDecoder(r.Body).Decode(&input) != nil || len([]rune(input.Label)) > 128 {
			http.Error(w, "invalid device label", 400)
			return
		}
		if input.Label == "" {
			input.Label = "Fixture device"
		}
		finish, admitted := reserveCreate(r.RemoteAddr)
		if !admitted {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "pair creation too frequent", 429)
			return
		}
		committed := false
		defer func() { finish(committed) }()
		value, err := s.createDevice(input.Label)
		if errors.Is(err, errDenied) {
			http.Error(w, "five unexpired pairing requests are waiting; this page cannot cancel abandoned requests. Wait for one to finish or expire before connecting again", 429)
			return
		}
		if err != nil {
			storeError(w, err, 403, "pair creation denied")
			return
		}
		committed = true
		jsonReply(w, 201, value)
	})
	mux.HandleFunc("POST /pair/decision", func(w http.ResponseWriter, r *http.Request) {
		var input struct{ ID, Code, Decision string }
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, "invalid decision", 400)
			return
		}
		isBootstrap := bootstrap != "" && r.Header.Get("Origin") == "" && hash(token(r)) == hash(bootstrap)
		value, err := s.decide(input.ID, input.Code, input.Decision, token(r), isBootstrap)
		if err != nil {
			storeError(w, err, 403, "decision denied")
			return
		}
		jsonReply(w, 200, value)
	})
	mux.HandleFunc("POST /pair/redeem", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ID, Secret      string
			LifetimeSeconds int64
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.LifetimeSeconds < 0 || input.LifetimeSeconds > 86400 {
			http.Error(w, "invalid redemption", 400)
			return
		}
		value, err := s.redeem(input.ID, input.Secret, time.Duration(input.LifetimeSeconds)*time.Second)
		if err != nil {
			storeError(w, err, 403, "redemption denied")
			return
		}
		jsonReply(w, 200, map[string]string{"token": value})
	})
	mux.HandleFunc("POST /devices/revoke", func(w http.ResponseWriter, r *http.Request) {
		var input struct{ ID string }
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, "invalid revocation", 400)
			return
		}
		if err := s.revoke(token(r), input.ID); err != nil {
			storeError(w, err, 403, "revocation denied")
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /session", func(w http.ResponseWriter, r *http.Request) {
		value, err := s.session(token(r))
		if err != nil {
			storeError(w, err, 401, "not authorized")
			return
		}
		jsonReply(w, 200, value)
	})
	mux.HandleFunc("GET /devices", func(w http.ResponseWriter, r *http.Request) {
		value, err := s.devices(token(r))
		if err != nil {
			storeError(w, err, 401, "not authorized")
			return
		}
		jsonReply(w, 200, value)
	})
	mux.HandleFunc("POST /private/action", func(w http.ResponseWriter, r *http.Request) {
		var input struct{ OperationID string }
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.OperationID == "" {
			http.Error(w, "operation required", 400)
			return
		}
		value, err := s.action(token(r), input.OperationID)
		if err != nil {
			storeError(w, err, 401, "not authorized")
			return
		}
		jsonReply(w, 200, map[string]int{"value": value})
	})
	mux.HandleFunc("GET /private/events", func(w http.ResponseWriter, r *http.Request) {
		credential := token(r)
		if _, err := s.session(credential); err != nil {
			storeError(w, err, 401, "not authorized")
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "stream unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: synthetic private event\n\n"))
		flusher.Flush()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
				if _, err := s.session(credential); err != nil {
					if !errors.Is(err, errDenied) {
						w.Write([]byte("event: retryable\ndata: {\"reason\":\"store_unavailable\",\"retry_after\":2}\n\n"))
						flusher.Flush()
					}
					return
				}
				if _, err := w.Write([]byte(": fixture keepalive\n\n")); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	})
	for _, path := range []string{"/private/data", "/private/assets", "/private/download"} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			tx, err := s.db.Begin()
			if err != nil {
				storeError(w, err, 401, "not authorized")
				return
			}
			defer tx.Rollback()
			if err := s.authorize(tx, token(r)); err != nil {
				storeError(w, err, 401, "not authorized")
				return
			}
			jsonReply(w, 200, map[string]string{"fixture": "synthetic private value", "principal": "single-owner"})
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if !hosts[r.Host] {
			http.Error(w, "host denied", 403)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" && !origins[origin] {
			http.Error(w, "origin denied", 403)
			return
		}
		// Pairing metadata is small; this does not cap artifact/content transport.
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, 8192)
		}
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/pair/") {
			finish, delay := reserveAttempt(r.RemoteAddr)
			if finish == nil {
				w.Header().Set("Retry-After", fmt.Sprint(delay))
				http.Error(w, "pairing attempt budget busy; wait before retrying", 429)
				return
			}
			observed := &pairingResponse{ResponseWriter: w}
			defer func() { finish(observed.status) }()
			mux.ServeHTTP(observed, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

type pairingResponse struct {
	http.ResponseWriter
	status int
}

func (w *pairingResponse) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
