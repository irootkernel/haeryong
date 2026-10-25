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
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"haeryong.local/haeryong/internal/application/recovery"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
)

const copiesPath = "/local/v1/copy-requests"

func copyRequestProof(p service.Profile, id service.Identity, nonce, key, token string, body []byte) string {
	digest := sha256.Sum256(body)
	credential := sha256.Sum256([]byte(token))
	return signature(p.ControlKey, "copy-request-v1", nonce, []byte("POST\n"+copiesPath+"\n"+id.InstanceID+"\n"+p.ProfileID+"\n"+p.LibraryID+"\n"+key+"\n"+hex.EncodeToString(credential[:])+"\n"+hex.EncodeToString(digest[:])))
}
func (h *HTTP) copyRequest(w http.ResponseWriter, r *http.Request, p service.Profile, id service.Identity) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	if r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Forwarded-Host") != "" || r.Header.Get("X-Forwarded-Proto") != "" {
		w.WriteHeader(403)
		return
	}
	peer, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil || net.ParseIP(peer) == nil || !net.ParseIP(peer).IsLoopback() {
		w.WriteHeader(403)
		return
	}
	body, e := io.ReadAll(io.LimitReader(r.Body, 16385))
	if e != nil || len(body) > 16384 {
		w.WriteHeader(400)
		return
	}
	nonce := r.Header.Get("X-Haeryong-Nonce")
	decoded, e := hex.DecodeString(nonce)
	token := ""
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	key := r.Header.Get("Idempotency-Key")
	if e != nil || len(decoded) != 32 || !hmac.Equal([]byte(r.Header.Get("X-Haeryong-Proof")), []byte(copyRequestProof(p, id, nonce, key, token, body))) {
		w.WriteHeader(401)
		return
	}
	// Reject duplicate keys as well as unknown/trailing fields.
	d := json.NewDecoder(bytes.NewReader(body))
	start, e := d.Token()
	seen := map[string]bool{}
	if e != nil || start != json.Delim('{') {
		w.WriteHeader(400)
		return
	}
	for d.More() {
		name, e := d.Token()
		if e != nil {
			w.WriteHeader(400)
			return
		}
		k, ok := name.(string)
		if !ok || seen[k] {
			w.WriteHeader(400)
			return
		}
		seen[k] = true
		var raw json.RawMessage
		if d.Decode(&raw) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			w.WriteHeader(400)
			return
		}
	}
	if _, e = d.Token(); e != nil {
		w.WriteHeader(400)
		return
	}
	if _, e = d.Token(); e != io.EOF {
		w.WriteHeader(400)
		return
	}
	var request domain.CopyRequest
	d = json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(&request) != nil {
		w.WriteHeader(400)
		return
	}
	if request.Runtime == "" {
		w.WriteHeader(400)
		return
	}
	response, e := h.Copies.Submit(r.Context(), token, key, request)
	status := response.Status
	result := response.Body
	if e != nil {
		code := recovery.ErrorCode(e)
		status = 503
		switch code {
		case "AUTH_REQUIRED", "SESSION_REVOKED", "SESSION_EXPIRED":
			status = 401
		case "PRECONDITION_REQUIRED":
			status = 428
		case "RATE_LIMITED":
			status = 429
		case "INVALID_REQUEST":
			status = 400
		case "FORBIDDEN":
			status = 403
		case "NOT_FOUND":
			status = 404
		case "OPERATION_CONFLICT", "INVALID_TRANSITION", "OPERATION_RECONCILIATION_REQUIRED":
			status = 409
		}
		result, _ = json.Marshal(map[string]any{"error": map[string]string{"code": code}})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Haeryong-Proof", bootstrapResponseProof(p.ControlKey, nonce, status, result))
	w.WriteHeader(status)
	_, _ = w.Write(result)
}
func CopyCall(ctx context.Context, p service.Profile, id service.Identity, token, key string, r domain.CopyRequest) (domain.Response, error) {
	if _, e := Verify(ctx, p, id); e != nil {
		return domain.Response{}, e
	}
	body, e := json.Marshal(r)
	if e != nil {
		return domain.Response{}, e
	}
	b := make([]byte, 32)
	if _, e = rand.Read(b); e != nil {
		return domain.Response{}, e
	}
	nonce := hex.EncodeToString(b)
	req, e := http.NewRequestWithContext(ctx, "POST", id.Endpoint+copiesPath, bytes.NewReader(body))
	if e != nil {
		return domain.Response{}, e
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("X-Haeryong-Nonce", nonce)
	req.Header.Set("X-Haeryong-Proof", copyRequestProof(p, id, nonce, key, token, body))
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := client.Do(req)
	if e != nil {
		return domain.Response{}, e
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(res.Body)
	if e != nil {
		return domain.Response{}, e
	}
	if !hmac.Equal([]byte(res.Header.Get("X-Haeryong-Proof")), []byte(bootstrapResponseProof(p.ControlKey, nonce, res.StatusCode, raw))) {
		return domain.Response{}, errors.New("copy response proof invalid")
	}
	out := domain.Response{Status: res.StatusCode, Body: raw}
	if res.StatusCode >= 400 {
		var failure struct {
			Error struct {
				Code string `json:"code"`
			}
		}
		_ = json.Unmarshal(raw, &failure)
		return out, &BootstrapError{Status: res.StatusCode, Code: failure.Error.Code}
	}
	return out, nil
}
