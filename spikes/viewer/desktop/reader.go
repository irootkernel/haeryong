// ReaderMode for the reused pinned beta28 harness. This mode registers ONLY
// the typed ReaderService binding and rejects the legacy generic fixture
// bindings and message kinds: there is no arbitrary URL/path/method proxy.
// Every operation is a fixed production /api/v1 route built Go-side with the
// Go-held credential; JavaScript never receives a bearer.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// wireError is the closed error envelope the native wire expects.
type wireError struct {
	Status     int            `json:"status"`
	Code       string         `json:"code"`
	Retryable  bool           `json:"retryable"`
	RetryAfter float64        `json:"retry_after,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

func (e *wireError) Error() string {
	return fmt.Sprintf("native op failed: HTTP %d %s", e.Status, e.Code)
}

func networkError() *wireError { return &wireError{Status: 0, Code: "NETWORK_ERROR", Retryable: true} }

type readerStream struct {
	id     string
	body   io.ReadCloser
	cancel context.CancelFunc
	auth   authority
	// The exact version and the daemon stream epoch captured at open time;
	// both are reverified on every pull.
	versionID string
	epoch     string
}

type readerSubscription struct {
	id  string
	gen uint64
}

type renderRef struct {
	VersionID string `json:"version_id"`
	SessionID string `json:"session_id"`
}

// outcome carries an HTTP status alongside the wire value so decision
// operations can map the canonical 202 reservation honestly.
type outcome struct {
	status int
	value  any
	werr   *wireError
}

type ReaderService struct {
	base, state string

	mu    sync.Mutex
	token string
	// The captured bearer of an explicit logout, kept ONLY for the original
	// revoke attempt and owned-cleanup closes; s.token is cleared at intent.
	logoutBearer       string
	forgetAttachmentID string
	attachmentID       string
	generation         uint64
	logoutSeen         bool
	// Local forget obligation: RAM is cleared at intent, the credential
	// FILE unlink is tracked separately and stays retryable until it truly
	// succeeds (or the file is proven already absent).
	forgetNeeded bool
	forgetDone   bool
	forgetErr    string
	streams      map[string]*readerStream
	subs         map[string]*readerSubscription
	renders      []renderRef
	receipts     []map[string]any
	client       *http.Client
}

func newReaderService(base, state, token string) *ReaderService {
	return &ReaderService{
		base: base, state: state, token: token,
		streams: map[string]*readerStream{}, subs: map[string]*readerSubscription{},
		// Browser parity: redirect:"error". The Go-held bearer is never
		// forwarded to any other listener through a redirect.
		client: &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return errRedirectRejected
		}},
	}
}

var errRedirectRejected = errors.New("native client rejects redirects")

func (s *ReaderService) receipt(event string, rows map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := map[string]any{"pid": os.Getpid(), "event": event, "at": time.Now().UTC()}
	for key, value := range rows {
		entry[key] = value
	}
	s.receipts = append(s.receipts, entry)
	encoded, err := json.MarshalIndent(s.receipts, "", " ")
	if err != nil {
		return
	}
	os.WriteFile(filepath.Join(s.state, fmt.Sprintf("native-reader-receipts-%d.json", os.Getpid())), encoded, 0600)
}

type authority struct {
	token        string
	attachmentID string
	gen          uint64
}

func (s *ReaderService) authority() (authority, *wireError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token == "" {
		return authority{}, &wireError{Status: 401, Code: "NATIVE_SESSION_LOGGED_OUT", Retryable: false}
	}
	if s.attachmentID == "" {
		return authority{}, &wireError{Status: 401, Code: "NATIVE_SESSION_NOT_ATTACHED", Retryable: false}
	}
	return authority{token: s.token, attachmentID: s.attachmentID, gen: s.generation}, nil
}

func (s *ReaderService) currentGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

func (s *ReaderService) currentToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
}

// request performs one fixed production request. Routes are always built here.
func (s *ReaderService) request(method, path string, query url.Values, headers map[string]string, body []byte, auth authority) outcome {
	target := s.base + path
	if len(query) != 0 {
		target += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequest(method, target, reader)
	if err != nil {
		return outcome{werr: networkError()}
	}
	ctx, cancel := context.WithCancel(context.Background())
	request = request.WithContext(ctx)
	if auth.token != "" {
		request.Header.Set("Authorization", "Bearer "+auth.token)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	// The real daemon's strict decoder requires the JSON media type on every
	// JSON body; binary uploads set their own Content-Type explicitly.
	if body != nil && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.client.Do(request)
	if err != nil {
		cancel()
		return outcome{werr: networkError()}
	}
	defer func() { response.Body.Close(); cancel() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		// Headers arrived and the server may have committed the write; the
		// outcome is honestly unknown, never a definitive failure.
		return outcome{status: response.StatusCode, werr: uncertain(response.StatusCode)}
	}
	value, werr := typedResult(response.StatusCode, data)
	if werr != nil {
		// Retry-After is a real HTTP header, not an error-body field.
		if seconds, parseErr := strconv.ParseFloat(response.Header.Get("Retry-After"), 64); parseErr == nil && seconds >= 0 {
			werr.RetryAfter = seconds
		}
	}
	return outcome{status: response.StatusCode, value: value, werr: werr}
}

// uncertain is the closed wire signal for a successful status whose body
// could not be read or parsed; the native wire maps it to the same
// UncertainResponseError the browser transport raises, so the original K
// identity survives for receipt recovery.
func uncertain(status int) *wireError {
	return &wireError{Status: status, Code: "UNCERTAIN_RESPONSE", Retryable: true}
}

// typedResult classifies a daemon reply: 2xx bodies carry {data:...}; errors
// carry {error:{code,retryable,details}}; only 204 carries no body.
func typedResult(status int, data []byte) (any, *wireError) {
	if status >= 200 && status < 300 {
		if status == 204 {
			return nil, nil
		}
		if len(data) == 0 {
			// The browser transport fails to parse an empty 200/201 body and
			// keeps the original K recoverable; so must the native wire.
			return nil, uncertain(status)
		}
		var parsed struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(data, &parsed); err != nil {
			// A committed write with an unreadable body keeps its original K
			// recovery path (browser UncertainResponseError parity).
			return nil, uncertain(status)
		}
		if parsed.Data == nil {
			// Browser parity: a successful envelope without a data member is
			// a null success, not a transport failure.
			return nil, nil
		}
		var value any
		if err := json.Unmarshal(parsed.Data, &value); err != nil {
			return nil, uncertain(status)
		}
		return value, nil
	}
	code, retryable, details := "TRANSPORT_ERROR", true, map[string]any{}
	var parsed struct {
		Error struct {
			Code      string         `json:"code"`
			Retryable *bool          `json:"retryable"`
			Details   map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &parsed); err == nil {
		if parsed.Error.Code != "" {
			code = parsed.Error.Code
		}
		if parsed.Error.Retryable != nil {
			retryable = *parsed.Error.Retryable
		}
		if parsed.Error.Details != nil {
			details = parsed.Error.Details
		}
	}
	return nil, &wireError{Status: status, Code: code, Retryable: retryable, Details: details}
}

// forgetCredentialFile performs one honest local-forget attempt. Absence is
// already-forgotten; only a real removal or proven absence completes the
// obligation. A file holding a DIFFERENT credential is never unlinked (a late
// old retry cannot destroy a newer binding's credential). Callers hold mu;
// receipt() locks internally, so the receipt writes happen AFTER unlocking.
func (s *ReaderService) forgetCredentialFileLocked() {
	path := filepath.Join(s.state, "native-session")
	raw, readErr := os.ReadFile(path)
	switch {
	case readErr == nil:
		if strings.TrimSpace(string(raw)) != s.logoutBearer {
			// The file no longer holds OUR forgotten credential; leave any
			// newer binding intact and record it truthfully.
			s.forgetDone = false
			s.forgetErr = "credential file now holds a different credential; left intact"
			s.receipts = append(s.receipts, map[string]any{"pid": os.Getpid(), "event": "logoutLocalForget", "at": time.Now().UTC(), "status": "foreign-credential-intact"})
			return
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			s.forgetDone = false
			s.forgetErr = err.Error()
			s.receipts = append(s.receipts, map[string]any{"pid": os.Getpid(), "event": "logoutLocalForget", "at": time.Now().UTC(), "status": "local-forget-failed", "io_error": err.Error()})
			return
		}
	case os.IsNotExist(readErr):
		// Already forgotten (an earlier attempt or pre-existing absence).
	default:
		s.forgetDone = false
		s.forgetErr = readErr.Error()
		s.receipts = append(s.receipts, map[string]any{"pid": os.Getpid(), "event": "logoutLocalForget", "at": time.Now().UTC(), "status": "local-forget-failed", "io_error": readErr.Error()})
		return
	}
	s.forgetDone = true
	s.forgetErr = ""
	s.receipts = append(s.receipts, map[string]any{"pid": os.Getpid(), "event": "logoutLocalForget", "at": time.Now().UTC(), "status": "forgotten"})
}

func randomID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return ""
	}
	return hex.EncodeToString(raw)
}

func decodePayload(raw json.RawMessage, into any) *wireError {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID", Retryable: false}
	}
	return nil
}

// Invoke dispatches one named typed operation. The switch is the whitelist.
func (s *ReaderService) Invoke(in struct {
	Operation string          `json:"Operation"`
	Payload   json.RawMessage `json:"Payload"`
}) (map[string]any, error) {
	value, werr := s.invoke(in.Operation, in.Payload)
	if werr != nil {
		return map[string]any{"ok": false, "error": werr}, nil
	}
	return map[string]any{"ok": true, "value": value}, nil
}

func (s *ReaderService) invoke(operation string, payload json.RawMessage) (any, *wireError) {
	switch operation {
	case "attachSession":
		return s.attach()
	case "disposeSession":
		var in struct {
			AttachmentID string `json:"attachment_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		werr := s.dispose(in.AttachmentID)
		if werr != nil {
			return nil, werr
		}
		return nil, nil
	case "pair", "pollPairing":
		// Task05 onboarding is explicitly out of scope for ReaderMode.
		return nil, &wireError{Status: 501, Code: "FEATURE_NOT_READY", Retryable: false}
	}

	auth, werr := s.authority()
	if werr != nil {
		return nil, werr
	}
	jsonBody := func(value any) []byte {
		encoded, _ := json.Marshal(value)
		return encoded
	}
	plain := func(result outcome) (any, *wireError) { return result.value, result.werr }
	// decision maps the canonical 202 reservation like the browser client.
	decision := func(result outcome) (any, *wireError) {
		if result.werr != nil {
			return nil, result.werr
		}
		kind := "committed"
		if result.status == 202 {
			kind = "accepted"
		}
		return map[string]any{"kind": kind, "body": result.value}, nil
	}
	collection := func(params map[string]string) url.Values {
		query := url.Values{}
		for key, value := range params {
			if value != "" {
				query.Set(key, value)
			}
		}
		return query
	}
	itemsOnly := func(result outcome) (any, *wireError) {
		if result.werr != nil {
			return nil, result.werr
		}
		page, ok := result.value.(map[string]any)
		if !ok {
			return nil, networkError()
		}
		return page["items"], nil
	}

	switch operation {
	case "currentSession":
		return plain(s.request("GET", "/api/v1/session", nil, nil, nil, auth))
	case "capabilities":
		return plain(s.request("GET", "/api/v1/capabilities", nil, nil, nil, auth))
	case "decidePairing":
		var in struct {
			PairingID   string `json:"pairing_id"`
			Code        string `json:"code"`
			Origin      string `json:"origin"`
			Etag        string `json:"etag"`
			OperationID string `json:"operation_id"`
			Action      string `json:"action"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		if in.Action != "approve" && in.Action != "reject" {
			return nil, &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID", Retryable: false}
		}
		return decision(s.request("POST", "/api/v1/auth/pairings/"+url.PathEscape(in.PairingID)+"/"+in.Action, nil,
			map[string]string{"If-Match": in.Etag, "Idempotency-Key": in.OperationID},
			jsonBody(map[string]string{"code": in.Code, "origin": in.Origin}), auth))
	case "pendingPairings":
		return itemsOnly(s.request("GET", "/api/v1/auth/pairings", nil, nil, nil, auth))
	case "listSessions":
		return itemsOnly(s.request("GET", "/api/v1/sessions", nil, nil, nil, auth))
	case "operation":
		var in struct {
			OperationID string `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/operations/"+url.PathEscape(in.OperationID), nil, nil, nil, auth))
	case "revokeSession":
		var in struct {
			SessionID string `json:"session_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("DELETE", "/api/v1/sessions/"+url.PathEscape(in.SessionID), nil, nil, nil, auth))
	case "revokeOwnSession":
		// Capture the explicit logout intent BEFORE the revoke is dispatched,
		// under the matching attachment guard. RAM forgets immediately; the
		// credential FILE unlink is attempted before the network call so a
		// local I/O failure can never be masked by the server outcome. The
		// captured bearer lives on only inside this revoke attempt and the
		// later owned cleanup; a late response can never mark a NEWER
		// attachment, and a new attach resets the intent flag.
		s.mu.Lock()
		mine := s.attachmentID != "" && s.attachmentID == auth.attachmentID && s.generation == auth.gen
		if mine {
			s.logoutSeen = true
			s.logoutBearer = s.token
			s.forgetAttachmentID = auth.attachmentID
			s.token = ""
			s.forgetNeeded = true
			s.forgetDone = false
			s.forgetCredentialFileLocked()
		}
		localFailed := s.forgetNeeded && !s.forgetDone
		forgetErr := s.forgetErr
		s.mu.Unlock()
		if localFailed {
			// The local error cannot wait for an unknown network outcome.
			// This one captured revoke continues independently; its eventual
			// receipt never updates a newer attachment or claims local success.
			go func() {
				result := s.request("DELETE", "/api/v1/session", nil, nil, nil, auth)
				rows := map[string]any{"status": result.status}
				if result.werr != nil {
					rows["error"] = result.werr.Code
				}
				s.receipt("revokeOwnSession", rows)
			}()
			return nil, &wireError{
				Status:    500,
				Code:      "NATIVE_LOCAL_FORGET_FAILED",
				Retryable: true,
				Details:   map[string]any{"server_status": nil, "server_outcome": "unknown", "io_error": forgetErr},
			}
		}
		result := s.request("DELETE", "/api/v1/session", nil, nil, nil, auth)
		if result.werr == nil {
			s.receipt("revokeOwnSession", map[string]any{"status": result.status})
		}
		return result.value, result.werr
	case "serviceStatus":
		return plain(s.request("GET", "/api/v1/status", nil, nil, nil, auth))
	case "listProjects":
		var in struct {
			Cursor string `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/projects", collection(map[string]string{"cursor": in.Cursor}), nil, nil, auth))
	case "listDocuments":
		var in struct {
			ProjectID string `json:"project_id"`
			Lifecycle string `json:"lifecycle"`
			Cursor    string `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/documents", collection(map[string]string{"project_id": in.ProjectID, "lifecycle": in.Lifecycle, "cursor": in.Cursor}), nil, nil, auth))
	case "documentDetail":
		var in struct {
			DocumentID string `json:"document_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/documents/"+url.PathEscape(in.DocumentID), nil, nil, nil, auth))
	case "documentVersions":
		var in struct {
			DocumentID string `json:"document_id"`
			Cursor     string `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/documents/"+url.PathEscape(in.DocumentID)+"/versions", collection(map[string]string{"cursor": in.Cursor}), nil, nil, auth))
	case "versionDetail":
		var in struct {
			VersionID string `json:"version_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/versions/"+url.PathEscape(in.VersionID), nil, nil, nil, auth))
	case "versionFiles":
		var in struct {
			VersionID string `json:"version_id"`
			Cursor    string `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/files", collection(map[string]string{"cursor": in.Cursor}), nil, nil, auth))
	case "renderBootstrap":
		var in struct {
			VersionID string  `json:"version_id"`
			PagePath  *string `json:"page_path"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		query := url.Values{}
		if in.PagePath != nil {
			query.Set("page_path", *in.PagePath)
		}
		return plain(s.request("GET", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/render-bootstrap", query, nil, nil, auth))
	case "renderSessionOpen":
		var in struct {
			VersionID   string `json:"version_id"`
			OperationID string `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		result := s.request("POST", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/render-sessions", nil,
			map[string]string{"Idempotency-Key": in.OperationID}, jsonBody(map[string]any{}), auth)
		if result.werr == nil {
			if view, ok := result.value.(map[string]any); ok {
				if session, ok := view["session_id"].(string); ok {
					s.mu.Lock()
					s.renders = append(s.renders, renderRef{VersionID: in.VersionID, SessionID: session})
					s.mu.Unlock()
					s.receipt("renderSessionOpen", map[string]any{"version_id": in.VersionID, "session_id": session})
				}
			}
		}
		return result.value, result.werr
	case "renderSessionRenew":
		var in struct {
			VersionID string `json:"version_id"`
			SessionID string `json:"session_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("POST", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/render-sessions/"+url.PathEscape(in.SessionID)+"/renew", nil, nil, jsonBody(map[string]any{}), auth))
	case "renderSessionClose":
		var in struct {
			VersionID string `json:"version_id"`
			SessionID string `json:"session_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		result := s.request("POST", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/render-sessions/"+url.PathEscape(in.SessionID)+"/close", nil, nil, jsonBody(map[string]any{}), auth)
		if result.werr == nil {
			s.mu.Lock()
			kept := s.renders[:0]
			for _, ref := range s.renders {
				if ref.SessionID != in.SessionID {
					kept = append(kept, ref)
				}
			}
			s.renders = kept
			s.mu.Unlock()
			s.receipt("renderSessionClose", map[string]any{"version_id": in.VersionID, "session_id": in.SessionID})
		}
		return result.value, result.werr
	case "personalGet":
		var in struct {
			Kind      string  `json:"kind"`
			VersionID string  `json:"version_id"`
			Page      *string `json:"page"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		query := url.Values{}
		if in.Kind == "page-position" && in.Page != nil {
			query.Set("page", *in.Page)
		}
		return plain(s.request("GET", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/"+in.Kind, query, nil, nil, auth))
	case "personalPut":
		var in struct {
			Kind         string          `json:"kind"`
			VersionID    string          `json:"version_id"`
			Page         *string         `json:"page"`
			Body         json.RawMessage `json:"body"`
			Precondition struct {
				IfMatch *string `json:"if_match"`
				Create  *bool   `json:"create"`
			} `json:"precondition"`
			OperationID string `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		headers := map[string]string{"Idempotency-Key": in.OperationID}
		if in.Precondition.IfMatch != nil {
			headers["If-Match"] = *in.Precondition.IfMatch
		} else {
			headers["If-None-Match"] = "*"
		}
		query := url.Values{}
		if in.Kind == "page-position" && in.Page != nil {
			query.Set("page", *in.Page)
		}
		result := s.request("PUT", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/"+in.Kind, query, headers, in.Body, auth)
		if result.werr == nil {
			// Nonsecret request identity: a bounded operation-id prefix plus
			// the exact body digest and precondition kind let harnesses prove
			// a same-K replay dispatched the IDENTICAL request.
			keyPrefix := in.OperationID
			if len(keyPrefix) > 8 {
				keyPrefix = keyPrefix[:8]
			}
			digest := sha256.Sum256(in.Body)
			condition := "if_match"
			if in.Precondition.IfMatch == nil {
				condition = "create"
			}
			s.receipt("personalPut", map[string]any{"kind": in.Kind, "status": result.status, "operation_prefix": keyPrefix, "body_sha256": hex.EncodeToString(digest[:]), "condition": condition})
		}
		return result.value, result.werr
	case "listUploads":
		var in struct {
			State     *string `json:"state"`
			Unadopted *bool   `json:"unadopted"`
			Cursor    string  `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		params := map[string]string{"cursor": in.Cursor}
		if in.State != nil {
			params["state"] = *in.State
		}
		if in.Unadopted != nil {
			params["unadopted"] = fmt.Sprintf("%t", *in.Unadopted)
		}
		return plain(s.request("GET", "/api/v1/uploads", collection(params), nil, nil, auth))
	case "uploadDetail":
		var in struct {
			UploadID string `json:"upload_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/uploads/"+url.PathEscape(in.UploadID), nil, nil, nil, auth))
	case "uploadEntries":
		var in struct {
			UploadID string `json:"upload_id"`
			Cursor   string `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/uploads/"+url.PathEscape(in.UploadID)+"/entries", collection(map[string]string{"cursor": in.Cursor}), nil, nil, auth))
	case "uploadRanges":
		var in struct {
			UploadID string `json:"upload_id"`
			Path     string `json:"path"`
			Cursor   string `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/uploads/"+url.PathEscape(in.UploadID)+"/ranges", collection(map[string]string{"path": in.Path, "cursor": in.Cursor}), nil, nil, auth))
	case "uploadPins":
		var in struct {
			UploadID string `json:"upload_id"`
			Cursor   string `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/uploads/"+url.PathEscape(in.UploadID)+"/pins", collection(map[string]string{"cursor": in.Cursor}), nil, nil, auth))
	case "createUpload":
		var in struct {
			Request     json.RawMessage `json:"request"`
			OperationID string          `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		result := s.request("POST", "/api/v1/uploads", nil, map[string]string{"Idempotency-Key": in.OperationID}, in.Request, auth)
		if result.werr == nil {
			s.receipt("createUpload", map[string]any{"status": result.status})
		}
		return result.value, result.werr
	case "createContextUpload":
		var in struct {
			Binding     json.RawMessage `json:"binding"`
			OperationID string          `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		body := map[string]any{"purpose": "context", "input_mode": "object"}
		var target any
		if err := json.Unmarshal(in.Binding, &target); err != nil {
			return nil, &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID", Retryable: false}
		}
		body["target"] = target
		result := s.request("POST", "/api/v1/uploads", nil, map[string]string{"Idempotency-Key": in.OperationID}, jsonBody(body), auth)
		if result.werr == nil {
			s.receipt("createContextUpload", map[string]any{"status": result.status})
		}
		return result.value, result.werr
	case "putUploadEntry":
		return s.putUploadEntry(payload, auth)
	case "completeUpload":
		var in struct {
			UploadID    string          `json:"upload_id"`
			Checkpoint  json.RawMessage `json:"checkpoint"`
			OperationID string          `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		result := s.request("POST", "/api/v1/uploads/"+url.PathEscape(in.UploadID)+"/complete", nil,
			map[string]string{"Idempotency-Key": in.OperationID}, jsonBody(map[string]any{"checkpoint": in.Checkpoint}), auth)
		if result.werr == nil {
			s.receipt("completeUpload", map[string]any{"status": result.status})
		}
		return decision(result)
	case "abandonUpload":
		var in struct {
			UploadID    string `json:"upload_id"`
			OperationID string `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return decision(s.request("POST", "/api/v1/uploads/"+url.PathEscape(in.UploadID)+"/abandon", nil,
			map[string]string{"Idempotency-Key": in.OperationID}, jsonBody(map[string]any{}), auth))
	case "importPublish":
		var in struct {
			Request     json.RawMessage `json:"request"`
			OperationID string          `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		result := s.request("POST", "/api/v1/imports", nil, map[string]string{"Idempotency-Key": in.OperationID}, in.Request, auth)
		if result.werr == nil {
			s.receipt("importPublish", map[string]any{"status": result.status})
		}
		return decision(result)
	case "jobGet":
		var in struct {
			JobID string `json:"job_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/jobs/"+url.PathEscape(in.JobID), nil, nil, nil, auth))
	case "jobCancel":
		var in struct {
			JobID       string `json:"job_id"`
			OperationID string `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return decision(s.request("POST", "/api/v1/jobs/"+url.PathEscape(in.JobID)+"/cancel", nil,
			map[string]string{"Idempotency-Key": in.OperationID}, jsonBody(map[string]any{}), auth))
	case "consumeContext":
		var in struct {
			Binding struct {
				VersionID        string  `json:"version_id"`
				PagePath         *string `json:"page_path"`
				RenderInstanceID *string `json:"render_instance_id"`
			} `json:"binding"`
			UploadID    string `json:"upload_id"`
			OperationID string `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		query := url.Values{}
		query.Set("version_id", in.Binding.VersionID)
		query.Set("input", "upload")
		if in.Binding.PagePath != nil {
			query.Set("page_path", *in.Binding.PagePath)
		}
		if in.Binding.RenderInstanceID != nil {
			query.Set("render_instance_id", *in.Binding.RenderInstanceID)
		}
		result := s.request("POST", "/api/v1/contexts", query,
			map[string]string{"Idempotency-Key": in.OperationID}, jsonBody(map[string]string{"upload_id": in.UploadID}), auth)
		if result.werr == nil {
			receipt := map[string]any{"status": result.status}
			if body, ok := result.value.(map[string]any); ok {
				if digest, ok := body["bytes_sha256"].(string); ok {
					// Nonsecret exact-byte digest of the consumed context.
					receipt["bytes_sha256"] = digest
				}
			}
			s.receipt("consumeContext", receipt)
		}
		return decision(result)
	case "verifyContextFresh":
		return s.verifyContextFresh(payload, auth)
	case "packageStream":
		var in struct {
			VersionID string `json:"version_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return s.openStream(in.VersionID, "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/package", nil, auth)
	case "fileStream":
		var in struct {
			VersionID string `json:"version_id"`
			Path      string `json:"path"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		query := url.Values{}
		query.Set("path", in.Path)
		return s.openStream(in.VersionID, "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/file", query, auth)
	case "streamRead":
		return s.streamRead(payload)
	case "streamCancel":
		var in struct {
			StreamID string `json:"stream_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		s.dropStream(in.StreamID)
		return nil, nil
	case "subscribeEvents":
		return s.subscribeEvents(auth)
	case "eventsPoll":
		return s.eventsPoll(payload, auth)
	case "eventsClose":
		var in struct {
			SubscriptionID string `json:"subscription_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		s.mu.Lock()
		delete(s.subs, in.SubscriptionID)
		s.mu.Unlock()
		return nil, nil
	default:
		return nil, &wireError{Status: 400, Code: "NATIVE_UNKNOWN_OPERATION", Retryable: false}
	}
}

func (s *ReaderService) attach() (any, *wireError) {
	auth := authority{token: s.currentToken(), attachmentID: "bootstrap"}
	if auth.token == "" {
		// A forgotten (explicitly logged-out) credential must never
		// reauthenticate; a same-process reload or a pending logout cannot
		// restore it.
		return nil, &wireError{Status: 401, Code: "NATIVE_SESSION_LOGGED_OUT", Retryable: false}
	}
	sessionResult := s.request("GET", "/api/v1/session", nil, nil, nil, auth)
	if sessionResult.werr != nil {
		return nil, sessionResult.werr
	}
	capsResult := s.request("GET", "/api/v1/capabilities", nil, nil, nil, auth)
	if capsResult.werr != nil {
		return nil, capsResult.werr
	}
	attachment := randomID()
	if attachment == "" {
		return nil, networkError()
	}
	s.mu.Lock()
	s.generation++
	s.attachmentID = attachment
	s.logoutSeen = false
	s.mu.Unlock()
	s.receipt("attachSession", map[string]any{"attachment_prefix": attachment[:8]})
	return map[string]any{
		"attachment_id": attachment,
		"session":       sessionResult.value,
		"capabilities":  capsResult.value,
		"public_origin": s.base,
	}, nil
}

func (s *ReaderService) dispose(captured string) *wireError {
	s.mu.Lock()
	mine := s.attachmentID != "" && captured != "" && captured == s.attachmentID
	// A stale old-attachment callback must never erase or close handles that
	// belong to a newer attachment.
	var streams []*readerStream
	var renders []renderRef
	dropFile := false
	if mine {
		dropFile = s.logoutSeen
		s.attachmentID = ""
		s.generation++
		s.logoutSeen = false
		streams = make([]*readerStream, 0, len(s.streams))
		for _, stream := range s.streams {
			streams = append(streams, stream)
		}
		s.streams = map[string]*readerStream{}
		s.subs = map[string]*readerSubscription{}
		renders = s.renders
		s.renders = nil
	}
	cleanupBearer := s.token
	ownsForget := captured != "" && captured == s.forgetAttachmentID
	forgetStillNeeded := false
	forgetErr := ""
	dropped := false
	// The local-forget obligation is retried under the same lock on EVERY
	// disposal while it is still needed and not honestly done — including
	// disposals after the attachment is gone (no no-op trap). The guarded
	// retry never unlinks a newer binding's credential file.
	if ownsForget && s.forgetNeeded && !s.forgetDone {
		s.forgetCredentialFileLocked()
	}
	if ownsForget && s.token == "" {
		// Owned-cleanup closes after an explicit logout use the captured
		// logout bearer only; the bearer is preserved until the owned
		// cleanup/captured revoke attempt finishes, never for attach.
		cleanupBearer = s.logoutBearer
	}
	if ownsForget {
		forgetStillNeeded = s.forgetNeeded && !s.forgetDone
		if !forgetStillNeeded {
			s.logoutBearer = ""
		}
		forgetErr = s.forgetErr
		dropped = s.forgetDone
	}
	s.mu.Unlock()
	for _, stream := range streams {
		stream.cancel()
		stream.body.Close()
	}
	if len(renders) > 0 {
		// Disposal closes owned renderer deliveries explicitly under their
		// captured version/session identity.
		auth := authority{token: cleanupBearer}
		for _, ref := range renders {
			s.request("POST", "/api/v1/versions/"+url.PathEscape(ref.VersionID)+"/render-sessions/"+url.PathEscape(ref.SessionID)+"/close", nil, nil, []byte("{}"), auth)
		}
		s.receipt("disposeClosedRenders", map[string]any{"count": len(renders)})
	}
	s.receipt("disposeSession", map[string]any{"captured_match": mine, "credential_file_dropped": dropped && dropFile})
	if forgetStillNeeded {
		// A failed local forget is a CLOSED typed local failure: the disposal
		// cannot honestly claim completion while the credential file survives.
		return &wireError{
			Status:    500,
			Code:      "NATIVE_LOCAL_FORGET_FAILED",
			Retryable: true,
			Details:   map[string]any{"io_error": forgetErr},
		}
	}
	return nil
}

func (s *ReaderService) putUploadEntry(payload json.RawMessage, auth authority) (any, *wireError) {
	var in struct {
		UploadID string `json:"upload_id"`
		Chunk    struct {
			Path        string  `json:"path"`
			Kind        string  `json:"kind"`
			Offset      int64   `json:"offset"`
			BytesB64    string  `json:"bytes_b64"`
			ChunkSha256 string  `json:"chunkSha256"`
			TotalLength *int64  `json:"totalLength"`
			LinkTarget  *string `json:"linkTarget"`
			Final       bool    `json:"final"`
		} `json:"chunk"`
	}
	if werr := decodePayload(payload, &in); werr != nil {
		return nil, werr
	}
	var body []byte
	headers := map[string]string{}
	switch in.Chunk.Kind {
	case "file":
		raw, err := base64.StdEncoding.DecodeString(in.Chunk.BytesB64)
		if err != nil {
			return nil, &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID", Retryable: false}
		}
		body = raw
		headers["Content-Type"] = "application/octet-stream"
		headers["Haeryong-Upload-Offset"] = fmt.Sprintf("%d", in.Chunk.Offset)
		headers["Haeryong-Upload-Chunk-SHA256"] = in.Chunk.ChunkSha256
		if in.Chunk.TotalLength != nil {
			headers["Haeryong-Upload-Length"] = fmt.Sprintf("%d", *in.Chunk.TotalLength)
		}
		if in.Chunk.Final {
			headers["Haeryong-Upload-Final"] = "true"
		}
	case "directory":
		body = []byte("{}")
		headers["Content-Type"] = "application/json"
	case "symlink":
		target := ""
		if in.Chunk.LinkTarget != nil {
			target = *in.Chunk.LinkTarget
		}
		headers["Content-Type"] = "application/json"
		body, _ = json.Marshal(map[string]string{"link_target": target})
	default:
		return nil, &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID", Retryable: false}
	}
	query := url.Values{}
	query.Set("path", in.Chunk.Path)
	query.Set("kind", in.Chunk.Kind)
	result := s.request("PUT", "/api/v1/uploads/"+url.PathEscape(in.UploadID)+"/entries", query, headers, body, auth)
	if result.werr == nil {
		s.receipt("putUploadEntry", map[string]any{"kind": in.Chunk.Kind, "path": in.Chunk.Path})
	}
	return result.value, result.werr
}

func (r outcome) unpack() (any, *wireError) { return r.value, r.werr }

// encodeContextBinding mirrors the canonical browser form: the exact UTF-8
// JSON of the three tuple keys (null for absent members), unpadded base64url.
func encodeContextBinding(versionID string, pagePath, renderInstanceID *string) string {
	encoded, _ := json.Marshal(struct {
		VersionID        string  `json:"version_id"`
		PagePath         *string `json:"page_path"`
		RenderInstanceID *string `json:"render_instance_id"`
	}{VersionID: versionID, PagePath: pagePath, RenderInstanceID: renderInstanceID})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func (s *ReaderService) verifyContextFresh(payload json.RawMessage, auth authority) (any, *wireError) {
	var in struct {
		ContextRef  string `json:"context_ref"`
		BytesSHA256 string `json:"bytes_sha256"`
		Binding     struct {
			VersionID        string  `json:"version_id"`
			PagePath         *string `json:"page_path"`
			RenderInstanceID *string `json:"render_instance_id"`
		} `json:"binding"`
	}
	if werr := decodePayload(payload, &in); werr != nil {
		return nil, werr
	}
	etag := `"sha256-` + in.BytesSHA256 + `"`
	result := s.requestRaw("GET", "/api/v1/contexts/"+url.PathEscape(in.ContextRef), nil,
		map[string]string{
			"Haeryong-Context-Binding": encodeContextBinding(in.Binding.VersionID, in.Binding.PagePath, in.Binding.RenderInstanceID),
			"If-None-Match":            etag,
		}, auth, false)
	if result.werr != nil {
		return nil, result.werr
	}
	if result.status == http.StatusNotModified {
		if result.header.Get("ETag") != etag || result.header.Get("Haeryong-Context-Verified") != "fresh" {
			return nil, &wireError{Status: 304, Code: "CONTEXT_PROOF_INVALID", Retryable: false}
		}
		s.receipt("verifyContextFresh", map[string]any{"etag": etag})
		return map[string]any{"verified": true, "etag": etag}, nil
	}
	if result.status >= 200 && result.status < 300 {
		// A body-carrying response is never a fresh proof.
		return nil, &wireError{Status: result.status, Code: "CONTEXT_PROOF_INVALID", Retryable: false}
	}
	_, werr := typedResult(result.status, result.body)
	return nil, werr
}

func (s *ReaderService) openStream(versionID, path string, query url.Values, auth authority) (any, *wireError) {
	// Capture the daemon stream epoch at open time; every pull reverifies it.
	epoch, werr := s.currentEpoch(auth)
	if werr != nil {
		return nil, werr
	}
	result := s.requestRaw("GET", path, query, nil, auth, true)
	if result.werr != nil {
		return nil, result.werr
	}
	if result.status != 200 {
		_, werr := typedResult(result.status, result.body)
		result.close()
		return nil, werr
	}
	id := randomID()
	if id == "" {
		result.close()
		return nil, networkError()
	}
	s.mu.Lock()
	s.streams[id] = &readerStream{id: id, body: result.response, cancel: result.cancel, auth: auth, versionID: versionID, epoch: epoch}
	s.mu.Unlock()
	return map[string]any{"stream_id": id}, nil
}

// currentEpoch reads the liveness poll's server-era metadata. The poll stays
// liveness-only for the shell; its epoch guards the transport here.
func (s *ReaderService) currentEpoch(auth authority) (string, *wireError) {
	query := url.Values{}
	query.Set("transport", "poll")
	result := s.request("GET", "/api/v1/events", query, nil, nil, auth)
	if result.werr != nil {
		return "", result.werr
	}
	value, ok := result.value.(map[string]any)
	if !ok {
		return "", &wireError{Status: 0, Code: "UNCERTAIN_RESPONSE", Retryable: true}
	}
	epoch, _ := value["stream_epoch"].(string)
	if epoch == "" {
		return "", &wireError{Status: 0, Code: "UNCERTAIN_RESPONSE", Retryable: true}
	}
	return epoch, nil
}

// validatePull revalidates the stream's OWN captured attachment era, the live
// daemon authority and stream epoch, and the exact version's visibility
// before bytes are handed to JavaScript on every pull.
func (s *ReaderService) validatePull(stream *readerStream) *wireError {
	s.mu.Lock()
	current := s.attachmentID != "" && s.attachmentID == stream.auth.attachmentID && s.generation == stream.auth.gen
	s.mu.Unlock()
	if !current {
		return &wireError{Status: 409, Code: "STREAM_ATTACHMENT_REPLACED", Retryable: false}
	}
	epoch, werr := s.currentEpoch(stream.auth)
	if werr != nil {
		if werr.Status == 401 {
			// The credential is definitively dead server-side: no prefetched
			// byte may leak or complete cleanly after revocation.
			return &wireError{Status: 401, Code: werr.Code, Retryable: false, Details: werr.Details}
		}
		return werr
	}
	if epoch != stream.epoch {
		// A daemon restart/generation change invalidates prefetched bytes.
		return &wireError{Status: 409, Code: "STREAM_DAEMON_ERA_CHANGED", Retryable: false}
	}
	detail := s.request("GET", "/api/v1/versions/"+url.PathEscape(stream.versionID), nil, nil, nil, stream.auth)
	if detail.werr != nil {
		if detail.werr.Status == 404 {
			return &wireError{Status: 404, Code: "STREAM_TARGET_HIDDEN", Retryable: false, Details: map[string]any{"code": detail.werr.Code}}
		}
		return detail.werr
	}
	return nil
}

func (s *ReaderService) streamRead(payload json.RawMessage) (any, *wireError) {
	var in struct {
		StreamID string `json:"stream_id"`
		MaxBytes int64  `json:"max_bytes"`
	}
	if werr := decodePayload(payload, &in); werr != nil {
		return nil, werr
	}
	if in.MaxBytes < 1 || in.MaxBytes > 4<<20 {
		return nil, &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID", Retryable: false}
	}
	s.mu.Lock()
	stream, owned := s.streams[in.StreamID]
	s.mu.Unlock()
	if !owned {
		return nil, &wireError{Status: 404, Code: "STREAM_NOT_FOUND", Retryable: false}
	}
	// Guard the stream's OWN captured attachment era, daemon epoch and exact
	// version visibility before the blocking read.
	if werr := s.validatePull(stream); werr != nil {
		s.dropStream(in.StreamID)
		return nil, werr
	}
	s.mu.Lock()
	stillOwned := s.streams[in.StreamID] == stream
	s.mu.Unlock()
	if !stillOwned {
		return nil, &wireError{Status: 404, Code: "STREAM_NOT_FOUND", Retryable: false}
	}
	buffer := make([]byte, in.MaxBytes)
	total, atEof := 0, false
	for attempts := 0; total == 0 && !atEof && attempts < 4; attempts++ {
		read, err := stream.body.Read(buffer[total:])
		total += read
		if err == io.EOF {
			atEof = true
		} else if err != nil {
			s.dropStream(in.StreamID)
			return nil, networkError()
		}
	}
	if total == 0 && !atEof {
		// A body that stalls without bytes or EOF fails typed, never a fake
		// clean end.
		s.dropStream(in.StreamID)
		return nil, networkError()
	}
	// Revalidate after the awaited read: a revocation, era replacement, daemon
	// restart or hidden target that landed while this pull was blocking must
	// still fail the pull.
	if werr := s.validatePull(stream); werr != nil {
		s.dropStream(in.StreamID)
		return nil, werr
	}
	if total > 0 {
		// At most max_bytes per reply, never an over-read lookahead. A final
		// n>0 read that also reports EOF delivers the bytes together with the
		// clean EOF in one reply.
		if atEof {
			s.dropStream(in.StreamID)
		}
		return map[string]any{"data_b64": base64.StdEncoding.EncodeToString(buffer[:total]), "eof": atEof}, nil
	}
	s.dropStream(in.StreamID)
	return map[string]any{"data_b64": nil, "eof": true}, nil
}

func (s *ReaderService) dropStream(id string) {
	s.mu.Lock()
	stream, owned := s.streams[id]
	if owned {
		delete(s.streams, id)
	}
	s.mu.Unlock()
	if owned {
		stream.cancel()
		stream.body.Close()
	}
}

func (s *ReaderService) subscribeEvents(auth authority) (any, *wireError) {
	query := url.Values{}
	query.Set("transport", "poll")
	result := s.request("GET", "/api/v1/events", query, nil, nil, auth)
	if result.werr != nil {
		return nil, result.werr
	}
	id := randomID()
	if id == "" {
		return nil, networkError()
	}
	s.mu.Lock()
	s.subs[id] = &readerSubscription{id: id, gen: auth.gen}
	s.mu.Unlock()
	return map[string]any{"subscription_id": id}, nil
}

func (s *ReaderService) eventsPoll(payload json.RawMessage, auth authority) (any, *wireError) {
	var in struct {
		SubscriptionID string `json:"subscription_id"`
	}
	if werr := decodePayload(payload, &in); werr != nil {
		return nil, werr
	}
	s.mu.Lock()
	_, known := s.subs[in.SubscriptionID]
	current := s.generation
	s.mu.Unlock()
	if !known {
		return nil, &wireError{Status: 404, Code: "SUBSCRIPTION_NOT_FOUND", Retryable: false}
	}
	if current != auth.gen {
		return map[string]any{"closed": true}, nil
	}
	query := url.Values{}
	query.Set("transport", "poll")
	result := s.request("GET", "/api/v1/events", query, nil, nil, auth)
	if result.werr != nil {
		if result.werr.Status == 401 {
			// A definite session death closes the subscription for liveness.
			return map[string]any{"closed": true}, nil
		}
		return nil, result.werr
	}
	// Liveness only: the poll body never becomes UI authority.
	return map[string]any{"closed": false}, nil
}

// closeOwned is the normal window-close path: explicit typed closes for every
// renderer delivery and stream this attachment still owns.
func (s *ReaderService) closeOwned() {
	s.mu.Lock()
	renders := s.renders
	s.renders = nil
	streams := make([]*readerStream, 0, len(s.streams))
	for _, stream := range s.streams {
		streams = append(streams, stream)
	}
	s.streams = map[string]*readerStream{}
	token := s.token
	s.mu.Unlock()
	rows := []map[string]any{}
	for _, ref := range renders {
		result := s.request("POST", "/api/v1/versions/"+url.PathEscape(ref.VersionID)+"/render-sessions/"+url.PathEscape(ref.SessionID)+"/close", nil, nil, []byte("{}"), authority{token: token})
		rows = append(rows, map[string]any{"version_id": ref.VersionID, "session_id": ref.SessionID, "status": result.status})
	}
	for _, stream := range streams {
		stream.cancel()
		stream.body.Close()
	}
	s.receipt("windowClose", map[string]any{"rows": rows, "streams_closed": len(streams)})
}

// SelfcheckReport is registered ONLY in --reader-selfcheck mode: structured
// harness rows, never a generic transport.
func (s *ReaderService) SelfcheckReport(in struct {
	Rows json.RawMessage `json:"Rows"`
}) (map[string]any, error) {
	var rows []map[string]any
	if err := json.Unmarshal(in.Rows, &rows); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(map[string]any{"pid": os.Getpid(), "rows": rows}, "", " ")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(s.state, fmt.Sprintf("native-reader-selfcheck-%d.json", os.Getpid()))
	if err := os.WriteFile(path, append(encoded, '\n'), 0600); err != nil {
		return nil, err
	}
	return map[string]any{"written": true, "count": len(rows)}, nil
}

const nativeBootstrapReader = `(()=>{if(window.haeryongNativeCall)return;let calls=new Map();window.haeryongNativeReply=m=>{let p=calls.get(m.id);if(p){calls.delete(m.id);m.error?p.reject(new Error(m.error)):p.resolve(m.value);}};window.haeryongNativeCall=(methodID,args=[])=>new Promise((resolve,reject)=>{let id=crypto.randomUUID();calls.set(id,{resolve,reject});window.webkit.messageHandlers.external.postMessage(JSON.stringify({kind:'haeyong.native',id,object:0,method:0,args:{'call-id':id,methodID,args}}));});window.dispatchEvent(new Event('haeryong-native-bridge'));})();`

func readerCredential(state string) (string, error) {
	path := filepath.Join(state, "native-session")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("no prepaired native credential in state; ReaderMode performs no pairing")
	}
	token := strings.TrimSpace(string(raw))
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		return "", errors.New("native credential file must stay mode 0600")
	}
	if token == "" {
		return "", errors.New("empty native credential file")
	}
	return token, nil
}

type rawResult struct {
	status   int
	body     []byte
	header   http.Header
	response io.ReadCloser
	cancel   context.CancelFunc
	werr     *wireError
}

func (r rawResult) close() {
	if r.response != nil {
		r.response.Close()
	}
	if r.cancel != nil {
		r.cancel()
	}
}

// requestRaw either keeps the response body open (stream opens) or reads it
// fully and exposes headers for exact fresh-proof verification.
func (s *ReaderService) requestRaw(method, path string, query url.Values, headers map[string]string, auth authority, keepBody bool) rawResult {
	target := s.base + path
	if len(query) != 0 {
		target += "?" + query.Encode()
	}
	request, err := http.NewRequest(method, target, nil)
	if err != nil {
		return rawResult{werr: networkError()}
	}
	ctx, cancel := context.WithCancel(context.Background())
	request = request.WithContext(ctx)
	if auth.token != "" {
		request.Header.Set("Authorization", "Bearer "+auth.token)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := s.client.Do(request)
	if err != nil {
		cancel()
		return rawResult{werr: networkError()}
	}
	if keepBody && response.StatusCode == 200 {
		return rawResult{status: response.StatusCode, header: response.Header, response: response.Body, cancel: cancel}
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	cancel()
	if err != nil {
		return rawResult{status: response.StatusCode, header: response.Header, werr: networkError()}
	}
	return rawResult{status: response.StatusCode, body: data, header: response.Header}
}

func runReader(base, state, dist, port string, appPath string, selfcheck bool, probePath string, closeAfter time.Duration) error {
	token, err := readerCredential(state)
	if err != nil {
		return err
	}
	// Test-owned probe instrumentation: enabled ONLY when the driver passes an
	// explicit probe script, injected into the ACTUAL compiled app after the
	// guard and bridge bootstrap. It grants the imported child nothing.
	var probeScript string
	if probePath != "" {
		raw, err := os.ReadFile(probePath)
		if err != nil {
			return fmt.Errorf("reader probe script unreadable: %w", err)
		}
		probeScript = string(raw)
	}
	address := "127.0.0.1:" + port
	listener, err := net.Listen("tcp", address)
	if err != nil {
		// No silent fallback: the fixed static origin must be exactly this.
		return fmt.Errorf("fixed native static origin %s unavailable: %w", address, err)
	}
	service := newReaderService(base, state, token)
	origin := "http://" + address
	originURL, err := url.Parse(origin)
	if err != nil {
		return err
	}
	withSelfcheck := selfcheck
	schemeDenied := &atomic.Int64{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if withSelfcheck && r.URL.Path == "/-selfcheck.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, readerSelfcheckPage)
			return
		}
		clean := filepath.Clean("/" + r.URL.Path)
		if strings.Contains(clean, "..") {
			http.Error(w, "not found", 404)
			return
		}
		candidate := filepath.Join(dist, clean)
		if info, err := os.Stat(candidate); err != nil || info.IsDir() {
			// SPA routing fallback: only the compiled entry document.
			entry := filepath.Join(dist, "index.html")
			if _, err := os.Stat(entry); err != nil {
				entry = filepath.Join(dist, "native.html")
			}
			http.ServeFile(w, r, entry)
			return
		}
		http.ServeFile(w, r, candidate)
	})}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("native static origin ended: %v", err)
		}
	}()
	defer server.Close()

	transport := &fixtureTransport{}
	application.RegisterBindingMethodID((*ReaderService).Invoke, 701100)
	allowReport := selfcheck || probeScript != ""
	if allowReport {
		application.RegisterBindingMethodID((*ReaderService).SelfcheckReport, 701101)
	}
	var app *application.App
	// The pinned SDK runs each raw message handler and window event in its
	// own goroutine (application.go:705/717), so the guard-ready publication
	// must be synchronized, not a plain bool.
	var guardReady atomic.Bool
	app = application.New(application.Options{
		Name: "Haeryong Reader native", Transport: transport,
		Services: []application.Service{application.NewService(service)},
		Mac:      application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		Assets: application.AssetOptions{Middleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "native scheme endpoints remain disabled", 403)
				schemeDenied.Add(1)
			})
		}},
		RawMessageHandler: func(window application.Window, message string, originInfo *application.OriginInfo) {
			if originInfo == nil || !originInfo.IsMainFrame || !guardReady.Load() {
				return
			}
			sender, err := url.Parse(originInfo.Origin)
			if err != nil || sender.Scheme != originURL.Scheme || sender.Host != originURL.Host {
				return
			}
			var envelope struct {
				Kind, ID       string
				Object, Method int
				Args           json.RawMessage
			}
			if json.Unmarshal([]byte(message), &envelope) != nil || envelope.Kind != "haeyong.native" {
				// ReaderMode rejects the legacy generic fixture kind.
				return
			}
			if envelope.Object != 0 || envelope.Method != 0 {
				return
			}
			// Closed method whitelist: only the typed ReaderService invoke (and
			// the selfcheck report in the explicit selfcheck mode) dispatch.
			// The pinned SDK gives a nonempty args.methodName precedence over
			// methodID, so a name must never accompany an allowed ID.
			var call struct {
				MethodID   int    `json:"methodID"`
				MethodName string `json:"methodName"`
			}
			if json.Unmarshal(envelope.Args, &call) != nil {
				return
			}
			allowed := call.MethodName == "" && (call.MethodID == 701100 || (allowReport && call.MethodID == 701101))
			if !allowed {
				return
			}
			var request application.RuntimeRequest
			if json.Unmarshal([]byte(message), &request) != nil {
				return
			}
			request.WebviewWindowID = uint32(window.ID())
			response, err := transport.processor.HandleRuntimeCallWithIDs(context.Background(), &request)
			value := map[string]any{"id": envelope.ID, "value": response}
			if err != nil {
				value["error"] = err.Error()
			}
			encoded, _ := json.Marshal(value)
			application.InvokeSync(func() { evalNative(window, "window.haeryongNativeReply("+string(encoded)+")") })
		},
	})
	target := origin + appPath
	if selfcheck {
		// The harness selfcheck page receives its parameters through the
		// query carried by --reader-path.
		target = origin + "/-selfcheck.html" + appPath
	}
	options := application.WebviewWindowOptions{Title: "Haeryong Reader native", Width: 1180, Height: 880, URL: target}
	options.Mac.WebviewPreferences.JavaScriptCanOpenWindowsAutomatically.Set(true)
	window := app.Window.NewWithOptions(options)
	window.RegisterHook(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		service.closeOwned()
		// Final guard statistics at close time: imported-frame denials and
		// scheme rejections accumulate after the navigation-time snapshot.
		application.InvokeSync(func() {
			stats := guardStats()
			stats["pid"] = os.Getpid()
			stats["scheme_denied"] = schemeDenied.Load()
			encoded, _ := json.Marshal(stats)
			os.WriteFile(filepath.Join(state, "native-guard.json"), encoded, 0600)
		})
	})
	window.OnWindowEvent(events.Mac.WebViewDidFinishNavigation, func(_ *application.WindowEvent) {
		application.InvokeSync(func() {
			if !installGuard(window, origin) {
				log.Print("native guard installation failed")
				window.Close()
				return
			}
			// The facade exists only after the guard: the bridge bootstrap is
			// injected here, once per navigation, without replacing the guard.
			guardReady.Store(true)
			evalNative(window, nativeBootstrapReader)
			stats := guardStats()
			stats["pid"] = os.Getpid()
			stats["scheme_denied"] = schemeDenied.Load()
			encoded, _ := json.Marshal(stats)
			os.WriteFile(filepath.Join(state, "native-guard.json"), encoded, 0600)
			// Test-owned instrumentation over the ACTUAL compiled app: the
			// probe runs only after the guard and bridge exist and adds no
			// privilege for imported content.
			if probeScript != "" {
				evalNative(window, "(async()=>{"+probeScript+"})()")
			}
		})
	})
	if closeAfter > 0 {
		go func() { time.Sleep(closeAfter); window.Close(); time.Sleep(time.Second); app.Quit() }()
	}
	return app.Run()
}

const readerSelfcheckPage = `<!doctype html><meta charset=utf-8><title>Haeryong native selfcheck</title><body style="font-family:system-ui;padding:24px"><h1>Haeryong Reader native selfcheck</h1><div id=out style="white-space:pre-wrap"></div>
<script>
window.__pageError=null;
window.onerror=(m,s,l,c)=>{window.__pageError=String(m)+"@"+l+":"+c;};
window.addEventListener('unhandledrejection',e=>{window.__pageError=String(e.reason&&(e.reason.stack||e.reason.message||e.reason));});
window.__progress=[];
window.__done=false;
const params=new URLSearchParams(location.search);
const version=params.get("version")||"";
const call=(op,payload={})=>window.haeryongNativeCall(701100,[{Operation:op,Payload:payload}]);
const rows=[];const log=(name,ok,detail={})=>{rows.push({name,status:ok?"pass":"fail",detail});document.getElementById("out").textContent+=name+": "+(ok?"pass":"FAIL "+JSON.stringify(detail))+"\\n";};
const step=(name)=>{window.__progress.push(name);};
const report=async()=>{try{const r=await window.haeryongNativeCall(701101,[{Rows:[...rows,{name:"page.progress",status:"pass",detail:{steps:window.__progress}},{name:"page.error",status:window.__pageError?"fail":"pass",detail:{error:window.__pageError}},{name:"page.done",status:window.__done?"pass":"pending",detail:{}}]}]);return !!(r&&r.written===true);}catch(e){return false;}};
setInterval(()=>{void report();},2000);
const b64=(bytes)=>{let binary="";for(let i=0;i<bytes.length;i+=0x8000)binary+=String.fromCharCode(...bytes.subarray(i,i+0x8000));return btoa(binary);};
const hex=(buffer)=>[...new Uint8Array(buffer)].map(b=>b.toString(16).padStart(2,"0")).join("");
const noReply=(envelope)=>new Promise((resolve)=>{
  // Only wrap the REAL installed reply handler; never restore an undefined
  // capture from before the bridge bootstrap.
  const original=window.haeryongNativeReply;
  if(typeof original!=="function"){resolve(false);return;}
  let done=false;
  const restore=()=>{window.haeryongNativeReply=original;};
  const timer=setTimeout(()=>{if(!done){done=true;restore();resolve(true);}},700);
  window.haeryongNativeReply=(m)=>{if(!done&&m.id===envelope.id){done=true;clearTimeout(timer);restore();resolve(false);return;}original(m);};
  window.webkit.messageHandlers.external.postMessage(JSON.stringify(envelope));
});
(async()=>{try{
  step("start");
  // The Go harness injects the bridge after the guard on navigation finish;
  // wait for the actual callable bridge before any wrapper runs.
  const bridgeReady=await new Promise((resolve)=>{const deadline=Date.now()+15000;const check=()=>{if(typeof window.haeryongNativeCall==="function"&&typeof window.haeryongNativeReply==="function")resolve(true);else if(Date.now()>deadline)resolve(false);else setTimeout(check,100);};check();});
  log("bridge.ready",bridgeReady,{});
  if(!bridgeReady)throw Error("native bridge was never injected");
  const legacyIgnored=await noReply({kind:"fixture.runtime",id:"legacy-1",object:0,method:0,args:{"call-id":"legacy-1",methodID:701005,args:[]}});
  log("dispatch.legacy-kind-ignored",legacyIgnored,{});
  step("legacy-probe");
  const namedIgnored=await noReply({kind:"haeyong.native",id:"named-1",object:0,method:0,args:{"call-id":"named-1",methodID:701100,methodName:"Probe",args:[]}});
  log("dispatch.methodname-shadow-ignored",namedIgnored,{});
  step("named-probe");
  const attach=await call("attachSession");
  step("attach-replied");
  log("attach.verified",attach.ok&&typeof attach.value.attachment_id==="string"&&attach.value.public_origin===params.get("base"),attach.error||{origin:attach.value&&attach.value.public_origin});
  const caps=await call("capabilities");step("caps-replied");log("capabilities.read",caps.ok,caps.error||{});
  const current=await call("currentSession");step("session-replied");log("session.read",current.ok&&current.value.library_id===attach.value.session.library_id,{});
  const pairing=await call("pair",{device_label:"native"});step("pair-replied");log("pairing.feature_not_ready",!pairing.ok&&pairing.error.status===501,pairing.error||{});
  const projects=await call("listProjects");step("projects-replied");log("projects.read",projects.ok&&!!projects.value&&Array.isArray(projects.value.items)&&projects.value.items.length>0,projects.error||{});
  if(projects.ok&&projects.value&&Array.isArray(projects.value.items)&&projects.value.items.length){
    const projectId=projects.value.items[0].project_id;
    const created=await call("createUpload",{request:{purpose:"result",input_mode:"file",selection_name:"native-selfcheck.html",target:{project_id:projectId,document_id:null}},operation_id:self.crypto.randomUUID()});
    step("upload-create-replied");
    log("upload.create",created.ok&&!!created.value.upload_id,created.error||{});
    if(created.ok){
      const bytes=new TextEncoder().encode("<!doctype html><meta charset=utf-8><title>native upload</title><h1>native typed upload</h1>");
      const digest=hex(await crypto.subtle.digest("SHA-256",bytes));
      const entry=await call("putUploadEntry",{upload_id:created.value.upload_id,chunk:{path:"_input",kind:"file",offset:0,bytes_b64:b64(bytes),chunkSha256:digest,totalLength:bytes.length,final:true}});
      step("entry-replied");
      log("upload.entry-final",entry.ok&&entry.value.accepted_offset===String(bytes.length),entry.error||entry.value);
      const detail=await call("uploadDetail",{upload_id:created.value.upload_id});
      step("detail-replied");
      if(detail.ok&&detail.value.state==="open"&&detail.value.checkpoint){
        const checkpoint=detail.value.checkpoint;
        const completed=await call("completeUpload",{upload_id:created.value.upload_id,checkpoint:{generation:checkpoint.generation+1,inventory_sha256:checkpoint.inventory_sha256,accepted_bytes:checkpoint.accepted_bytes,complete_input:true},operation_id:self.crypto.randomUUID()});
        step("complete-replied");
        log("upload.complete",completed.ok,completed.error||completed.value);
        // Sealing is asynchronous: poll the real upload state like the
        // browser transfer before dispatching the publish.
        let sealed=false;
        for(let i=0;i<40;i++){
          const poll=await call("uploadDetail",{upload_id:created.value.upload_id});
          if(poll.ok&&poll.value.state==="sealed"){sealed=true;break;}
          if(!poll.ok||poll.value.state==="open"||poll.value.state==="abandoned")break;
          await new Promise(r=>setTimeout(r,500));
        }
        log("upload.sealed",sealed,{});
        if(sealed){
          const published=await call("importPublish",{request:{upload_id:created.value.upload_id,document_id:null},operation_id:self.crypto.randomUUID()});
          step("publish-replied");
          log("import.publish-dispatched",published.ok,published.error||published.value);
        }
      }else{
        log("upload.detail-open",false,detail.error||detail.value);
      }
    }
  }
  const unknownStream=await call("streamRead",{stream_id:"not-owned",max_bytes:1024});log("stream.foreign-typed-error",!unknownStream.ok&&unknownStream.error.status===404,unknownStream.error||{});
  if(version){
    const opened=await call("fileStream",{version_id:version,path:params.get("file")||"attachment.bin"});
    log("stream.opened",opened.ok&&!!opened.value.stream_id,opened.error||{});
    if(opened.ok){
      const expected=Uint8Array.from({length:256*513},(_,i)=>i%256);
      let total=0,bytesOk=true,sawNonemptyEof=false,sawEmptyEof=false;
      for(;;){
        const part=await call("streamRead",{stream_id:opened.value.stream_id,max_bytes:65536});
        if(!part.ok){log("stream.read-typed-error",false,part.error);break;}
        const v=part.value;
        if(v.data_b64!==null){
          const bytes=Uint8Array.from(atob(v.data_b64),c=>c.charCodeAt(0));
          if(bytes.length>65536){bytesOk=false;break;}
          let same=total+bytes.length<=expected.length;
          for(let i=0;same&&i<bytes.length;i++)same=bytes[i]===expected[total+i];
          if(!same){bytesOk=false;break;}
          total+=bytes.length;
          if(v.eof)sawNonemptyEof=bytes.length>0;
        }else if(v.eof)sawEmptyEof=true;
        if(v.eof)break;
      }
      log("stream.allbytes-exact-eof",bytesOk&&total===expected.length&&(sawNonemptyEof||sawEmptyEof),{total});
    }
    const before=await call("attachSession");
    // A FRESH owned stream of the old era, still in flight.
    const eraStream=await call("fileStream",{version_id:version,path:params.get("file")||"attachment.bin"});
    // A genuinely NEW attachment era WITHOUT disposal must invalidate the old
    // era's owned stream with the typed 409.
    const newer=await call("attachSession");
    const stale=await call("streamRead",{stream_id:(eraStream.ok?eraStream.value.stream_id:""),max_bytes:1024});
    log("era.attach-replacement-409",newer.ok&&eraStream.ok&&!stale.ok&&stale.error.status===409,stale.error||{});
    log("era.reattach-new-attachment",newer.ok&&newer.value.attachment_id!==before.value.attachment_id,{});
    // Disposal physically closes every owned stream of that attachment and
    // leaves no attachment: any later operation of that context fails closed
    // with the typed 401, never a clean completion.
    const ownedStream=await call("fileStream",{version_id:version,path:params.get("file")||"attachment.bin"});
    const disposed=await call("disposeSession",{attachment_id:newer.value.attachment_id});
    const dropped=await call("streamRead",{stream_id:(ownedStream.ok?ownedStream.value.stream_id:""),max_bytes:1024});
    log("era.post-dispose-fails-closed",disposed.ok&&ownedStream.ok&&!dropped.ok&&dropped.error.status===401,dropped.error||{});
    await call("attachSession");
  }
}catch(error){log("selfcheck.exception",false,String(error));}
window.__done=true;
let reported=false;
for(let i=0;i<3&&!reported;i++)reported=await report();
log("report.written",reported,{});
await report();
})();</script>`
