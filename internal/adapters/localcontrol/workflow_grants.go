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
	"strconv"
	"strings"
	"time"

	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

const workflowGrantsPath = "/local/v1/workflow-grants"

func workflowRequestProof(p service.Profile, id service.Identity, nonce, method, path, key, token string, body []byte) string {
	digest := sha256.Sum256(body)
	credential := sha256.Sum256([]byte(token))
	return signature(p.ControlKey, "workflow-request-v1", nonce, []byte(method+"\n"+path+"\n"+id.InstanceID+"\n"+p.ProfileID+"\n"+p.LibraryID+"\n"+key+"\n"+hex.EncodeToString(credential[:])+"\n"+hex.EncodeToString(digest[:])))
}
func workflowResponseProof(p service.Profile, nonce string, status int, body []byte) string {
	return signature(p.ControlKey, "workflow-response-v1", nonce, append([]byte(strconv.Itoa(status)+"\n"), body...))
}
func (h *HTTP) workflowGrant(w http.ResponseWriter, r *http.Request, p service.Profile, id service.Identity) {
	if r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Forwarded-Host") != "" || r.Header.Get("X-Forwarded-Proto") != "" {
		w.WriteHeader(403)
		return
	}
	peer, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil || net.ParseIP(peer) == nil || !net.ParseIP(peer).IsLoopback() {
		w.WriteHeader(403)
		return
	}
	nonce := r.Header.Get("X-Haeryong-Nonce")
	decoded, e := hex.DecodeString(nonce)
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	key := r.Header.Get("Idempotency-Key")
	if e != nil || len(decoded) != 32 || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || len(r.Header.Values("Authorization")) != 1 || len(r.Header.Values("X-Haeryong-Nonce")) != 1 || len(r.Header.Values("X-Haeryong-Proof")) != 1 {
		w.WriteHeader(401)
		return
	}
	// This route carries complete selected-source/context inventories. Authenticate
	// the owner before reading them; pairing/session control limits remain separate.
	writeResponse := func(response domain.Response) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Haeryong-Proof", workflowResponseProof(p, nonce, response.Status, response.Body))
		w.WriteHeader(response.Status)
		_, _ = w.Write(response.Body)
	}
	actor, e := h.Auth.Authenticate(r.Context(), token)
	if e != nil {
		writeResponse(workflowFailure(e))
		return
	}
	if actor.Scope.Kind != "owner" {
		writeResponse(workflowFailure(domain.ErrDenied))
		return
	}
	body, e := io.ReadAll(r.Body)
	if e != nil {
		w.WriteHeader(400)
		return
	}
	if !hmac.Equal([]byte(r.Header.Get("X-Haeryong-Proof")), []byte(workflowRequestProof(p, id, nonce, r.Method, r.URL.Path, key, token, body))) {
		w.WriteHeader(401)
		return
	}
	var response domain.Response
	switch {
	case r.Method == "POST" && r.URL.Path == workflowGrantsPath:
		if len(r.Header.Values("Idempotency-Key")) != 1 {
			e = sec.ErrMissing
			break
		}
		var input appauth.WorkflowGrantRequest
		if e = decodeWorkflowGrant(body, &input); e == nil {
			response, e = h.Auth.IssueWorkflowGrant(r.Context(), token, key, input)
		}
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, workflowGrantsPath+"/") && len(body) == 0:
		binding := strings.TrimPrefix(r.URL.Path, workflowGrantsPath+"/")
		if strings.Contains(binding, "/") {
			e = domain.ErrNotFound
			break
		}
		var grant appauth.WorkflowGrant
		grant, e = h.Auth.WorkflowGrant(r.Context(), token, binding)
		if e == nil {
			response.Status = 200
			response.Body, _ = json.Marshal(grant)
		}
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, workflowGrantsPath+"/") && strings.HasSuffix(r.URL.Path, "/revoke"):
		binding := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, workflowGrantsPath+"/"), "/revoke")
		if strings.Contains(binding, "/") || len(r.Header.Values("Idempotency-Key")) != 1 || string(body) != "{}" {
			e = sec.ErrInvalid
			break
		}
		response, e = h.Auth.RevokeWorkflowGrant(r.Context(), token, key, binding)
	default:
		e = domain.ErrNotFound
	}
	if e != nil {
		response = workflowFailure(e)
	}
	writeResponse(response)
}

// Preauthentication and issuance share the original observable error mapping.
// A storage/context failure is not evidence of an invalid owner credential.
func workflowFailure(e error) domain.Response {
	code, status := "SERVICE_RECOVERING", 503
	switch {
	case errors.Is(e, sec.ErrRequired), errors.Is(e, sec.ErrExpired), errors.Is(e, sec.ErrRevoked):
		code, status = "AUTH_REQUIRED", 401
	case errors.Is(e, domain.ErrDenied):
		code, status = "FORBIDDEN", 403
	case errors.Is(e, domain.ErrNotFound):
		code, status = "NOT_FOUND", 404
	case errors.Is(e, sec.ErrMissing):
		code, status = "PRECONDITION_REQUIRED", 428
	case errors.Is(e, domain.ErrPrecondition):
		code, status = "PRECONDITION_FAILED", 412
	case errors.Is(e, domain.ErrConflict):
		code, status = "OPERATION_CONFLICT", 409
	case errors.Is(e, sec.ErrInvalid), errors.Is(e, domain.ErrInvalidTarget):
		code, status = "INVALID_REQUEST", 400
	}
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": "local owner workflow request not accepted"}})
	return domain.Response{Status: status, Body: body}
}

func decodeWorkflowGrant(raw []byte, out *appauth.WorkflowGrantRequest) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return sec.ErrInvalid
	}
	seen := map[string]bool{}
	for d.More() {
		name, e := d.Token()
		if e != nil {
			return sec.ErrInvalid
		}
		key, ok := name.(string)
		if !ok || seen[key] {
			return sec.ErrInvalid
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return sec.ErrInvalid
		}
	}
	if _, e = d.Token(); e != nil {
		return sec.ErrInvalid
	}
	if _, e = d.Token(); e != io.EOF {
		return sec.ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return sec.ErrInvalid
	}
	return nil
}
func IssueWorkflowGrant(ctx context.Context, p service.Profile, id service.Identity, owner, key string, input appauth.WorkflowGrantRequest) (appauth.WorkflowGrant, error) {
	body, e := json.Marshal(input)
	if e != nil {
		return appauth.WorkflowGrant{}, e
	}
	raw, e := workflowCall(ctx, p, id, owner, "POST", workflowGrantsPath, key, body)
	if e != nil {
		return appauth.WorkflowGrant{}, e
	}
	var out appauth.WorkflowGrant
	e = json.Unmarshal(raw, &out)
	return out, e
}
func WorkflowGrant(ctx context.Context, p service.Profile, id service.Identity, owner, binding string) (appauth.WorkflowGrant, error) {
	raw, e := workflowCall(ctx, p, id, owner, "GET", workflowGrantsPath+"/"+binding, "", nil)
	if e != nil {
		return appauth.WorkflowGrant{}, e
	}
	var out appauth.WorkflowGrant
	e = json.Unmarshal(raw, &out)
	return out, e
}
func RevokeWorkflowGrant(ctx context.Context, p service.Profile, id service.Identity, owner, key, binding string) error {
	_, e := RevokeWorkflowGrantResult(ctx, p, id, owner, key, binding)
	return e
}
func RevokeWorkflowGrantResult(ctx context.Context, p service.Profile, id service.Identity, owner, key, binding string) (domain.Response, error) {
	raw, e := workflowCall(ctx, p, id, owner, "POST", workflowGrantsPath+"/"+binding+"/revoke", key, []byte(`{}`))
	return domain.Response{Status: 200, Body: raw}, e
}
func workflowCall(ctx context.Context, p service.Profile, id service.Identity, owner, method, path, key string, body []byte) ([]byte, error) {
	if _, e := Verify(ctx, p, id); e != nil {
		return nil, e
	}
	u, e := endpointURL(id.Endpoint)
	if e != nil {
		return nil, e
	}
	if !sec.ValidSecret(owner) {
		return nil, sec.ErrRequired
	}
	random := make([]byte, 32)
	if _, e = rand.Read(random); e != nil {
		return nil, e
	}
	nonce := hex.EncodeToString(random)
	request, e := http.NewRequestWithContext(ctx, method, u.String()+path, bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	request.Header.Set("Authorization", "Bearer "+owner)
	request.Header.Set("X-Haeryong-Nonce", nonce)
	request.Header.Set("X-Haeryong-Proof", workflowRequestProof(p, id, nonce, method, path, key, owner, body))
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(request)
	if e != nil {
		return nil, e
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(response.Body)
	if e != nil {
		return nil, e
	}
	if len(response.Header.Values("X-Haeryong-Proof")) != 1 || !hmac.Equal([]byte(response.Header.Get("X-Haeryong-Proof")), []byte(workflowResponseProof(p, nonce, response.StatusCode, raw))) {
		return nil, errors.New("local workflow response not verified")
	}
	if response.StatusCode != 200 {
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &failure)
		return nil, &BootstrapError{Status: response.StatusCode, Code: failure.Error.Code}
	}
	return raw, nil
}
