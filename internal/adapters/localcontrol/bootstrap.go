package localcontrol

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	sec "haeryong.local/haeryong/internal/security/session"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
)

const pairingsPath = "/local/v1/pairings"

// This separate domain binds every enrollment intent field. Lifecycle proofs
// cannot be reused as pairing proofs, nor can a signed body/header be substituted.
func bootstrapProof(key, nonce, instance, method, path, operation, etag string, body []byte) string {
	digest := sha256.Sum256(body)
	return signature(key, "bootstrap-request", nonce, []byte(method+"\n"+path+"\n"+instance+"\n"+operation+"\n"+etag+"\n"+hex.EncodeToString(digest[:])))
}
func (h *HTTP) mountBootstrap(mux *http.ServeMux, p service.Profile, id service.Identity) {
	mux.HandleFunc(pairingsPath, func(w http.ResponseWriter, r *http.Request) { h.bootstrap(w, r, p, id) })
	mux.HandleFunc(pairingsPath+"/", func(w http.ResponseWriter, r *http.Request) { h.bootstrap(w, r, p, id) })
}
func (h *HTTP) bootstrap(w http.ResponseWriter, r *http.Request, p service.Profile, id service.Identity) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16385))
	if err != nil || len(body) > 16384 {
		w.WriteHeader(400)
		return
	}
	nonce := r.Header.Get("X-Haeryong-Nonce")
	b, e := hex.DecodeString(nonce)
	if e != nil || len(b) != 32 || !hmac.Equal([]byte(r.Header.Get("X-Haeryong-Proof")), []byte(bootstrapProof(p.ControlKey, nonce, id.InstanceID, r.Method, r.URL.Path, r.Header.Get("Idempotency-Key"), r.Header.Get("If-Match"), body))) {
		w.WriteHeader(401)
		return
	}
	status := 200
	var data any
	if r.Method == "GET" && r.URL.Path == pairingsPath && len(body) == 0 {
		data, err = h.Auth.BootstrapPairings(r.Context())
	} else if r.Method == "POST" {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, pairingsPath+"/"), "/")
		if len(parts) != 2 || (parts[1] != "approve" && parts[1] != "reject") {
			w.WriteHeader(404)
			return
		}
		var request struct {
			Code   string `json:"code"`
			Origin string `json:"origin"`
		}
		// Only exact control envelopes are accepted. Duplicate fields are rejected.
		fields := map[string]json.RawMessage{}
		decoder := json.NewDecoder(bytes.NewReader(body))
		start, e := decoder.Token()
		if e != nil || start != json.Delim('{') {
			w.WriteHeader(400)
			return
		}
		valid := true
		for decoder.More() {
			k, e := decoder.Token()
			if e != nil {
				valid = false
				break
			}
			name, ok := k.(string)
			if !ok || fields[name] != nil {
				valid = false
				break
			}
			var value json.RawMessage
			if decoder.Decode(&value) != nil {
				valid = false
				break
			}
			fields[name] = value
		}
		if _, e := decoder.Token(); e != nil {
			valid = false
		}
		if _, e := decoder.Token(); e != io.EOF {
			valid = false
		}
		if !valid || len(fields) != 2 || fields["code"] == nil || fields["origin"] == nil || json.Unmarshal(body, &request) != nil || request.Code == "" || request.Origin == "" {
			w.WriteHeader(400)
			return
		}
		var result domain.Response
		result, err = h.Auth.BootstrapDecide(r.Context(), domain.PairingDecision{ID: parts[0], Action: parts[1], Code: request.Code, Origin: request.Origin, ETag: r.Header.Get("If-Match"), OperationID: r.Header.Get("Idempotency-Key")})
		if err == nil {
			status = result.Status
			data = json.RawMessage(result.Body)
			for k, v := range result.Headers {
				w.Header().Set(k, v)
			}
		}
	} else {
		w.WriteHeader(405)
		return
	}
	if err != nil {
		status = 503
		code := "SERVICE_RECOVERING"
		switch {
		case errors.Is(err, sec.ErrMissing):
			status = 428
			code = "PRECONDITION_REQUIRED"
		case errors.Is(err, sec.ErrInvalid):
			status = 400
			code = "INVALID_REQUEST"
		case errors.Is(err, domain.ErrPrecondition):
			status = 412
			code = "PRECONDITION_FAILED"
		case errors.Is(err, domain.ErrConflict):
			status = 409
			code = "OPERATION_CONFLICT"
		case errors.Is(err, domain.ErrTransition):
			status = 409
			code = "INVALID_TRANSITION"
		case errors.Is(err, domain.ErrNotFound):
			status = 404
			code = "NOT_FOUND"
		}
		data = map[string]any{"error": map[string]string{"code": code, "message": "local bootstrap request not accepted"}}
	}
	response, _ := json.Marshal(data)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Haeryong-Proof", bootstrapResponseProof(p.ControlKey, nonce, status, response))
	w.WriteHeader(status)
	w.Write(response)
}
func BootstrapPairings(ctx context.Context, p service.Profile, id service.Identity) ([]domain.Pairing, error) {
	body, err := bootstrapCall(ctx, p, id, "GET", pairingsPath, "", "", nil)
	if err != nil {
		return nil, err
	}
	var rows []domain.Pairing
	err = json.Unmarshal(body, &rows)
	return rows, err
}
func BootstrapDecision(ctx context.Context, p service.Profile, id service.Identity, d domain.PairingDecision) (domain.Pairing, error) {
	body, _ := json.Marshal(map[string]string{"code": d.Code, "origin": d.Origin})
	result, err := bootstrapCall(ctx, p, id, "POST", pairingsPath+"/"+d.ID+"/"+d.Action, d.OperationID, d.ETag, body)
	if err != nil {
		return domain.Pairing{}, err
	}
	var out domain.Pairing
	err = json.Unmarshal(result, &out)
	return out, err
}
func bootstrapCall(ctx context.Context, p service.Profile, id service.Identity, method, path, operation, etag string, body []byte) ([]byte, error) {
	if _, err := Verify(ctx, p, id); err != nil {
		return nil, err
	}
	u, err := endpointURL(id.Endpoint)
	if err != nil {
		return nil, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	nonce := hex.EncodeToString(b)
	request, err := http.NewRequestWithContext(ctx, method, u.String()+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Haeryong-Nonce", nonce)
	request.Header.Set("Idempotency-Key", operation)
	request.Header.Set("If-Match", etag)
	request.Header.Set("X-Haeryong-Proof", bootstrapProof(p.ControlKey, nonce, id.InstanceID, method, path, operation, etag, body))
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	result, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil {
		return nil, err
	}
	if len(result) > 16384 || !hmac.Equal([]byte(response.Header.Get("X-Haeryong-Proof")), []byte(bootstrapResponseProof(p.ControlKey, nonce, response.StatusCode, result))) {
		return nil, errors.New("local bootstrap response not verified")
	}
	if response.StatusCode != 200 {
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(result, &failure)
		return nil, &BootstrapError{Status: response.StatusCode, Code: failure.Error.Code}
	}
	return result, nil
}

type BootstrapError struct {
	Status int
	Code   string
}

func (e *BootstrapError) Error() string { return "local bootstrap request failed: " + e.Code }
func bootstrapResponseProof(key, nonce string, status int, body []byte) string {
	return signature(key, "bootstrap-response", nonce, append([]byte(strconv.Itoa(status)+"\n"), body...))
}
