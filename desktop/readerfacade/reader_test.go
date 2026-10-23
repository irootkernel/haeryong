package readerfacade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeDaemon struct {
	mu       sync.Mutex
	requests []string
	handler  func(w http.ResponseWriter, r *http.Request) bool
}

type readerCall struct {
	Operation string          `json:"Operation"`
	Payload   json.RawMessage `json:"Payload"`
}

func (f *fakeDaemon) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func (f *fakeDaemon) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
	f.mu.Unlock()
	if f.handler != nil && f.handler(w, r) {
		return
	}
	w.WriteHeader(404)
}

func dataReply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"data": value})
}

func newReaderFixture(t *testing.T, handler func(w http.ResponseWriter, r *http.Request) bool) (*ReaderService, *fakeDaemon, string) {
	t.Helper()
	daemon := &fakeDaemon{handler: handler}
	server := httptest.NewServer(http.HandlerFunc(daemon.serve))
	t.Cleanup(server.Close)
	state := t.TempDir()
	credential := filepath.Join(state, "native-session")
	if err := os.WriteFile(credential, []byte("test-owner-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	service := NewReaderService(server.URL, state, "test-owner-credential")
	// Attach once so protected operations have a current authority. The
	// caller's specific handler runs first so per-test faults are never
	// masked by these defaults.
	daemon.handler = func(w http.ResponseWriter, r *http.Request) bool {
		if handler != nil && handler(w, r) {
			return true
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/events" && r.URL.Query().Get("transport") == "poll":
			// Per-pull liveness validation target for streams.
			dataReply(w, 200, map[string]any{"items": []any{}, "stream_epoch": "e", "head": "1", "next_cursor": "e:1", "resync": nil})
			return true
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v1/versions/") && !strings.Contains(strings.TrimPrefix(r.URL.Path, "/api/v1/versions/"), "/"):
			// Exact version visibility for per-pull stream guards.
			dataReply(w, 200, map[string]any{"version_id": "v1", "document_id": "d1"})
			return true
		case r.Method == "GET" && r.URL.Path == "/api/v1/session":
			dataReply(w, 200, map[string]any{"session_id": "s1", "library_id": "lib", "principal_id": "p1"})
			return true
		case r.Method == "GET" && r.URL.Path == "/api/v1/capabilities":
			dataReply(w, 200, map[string]any{"api_version": "1.0", "library_id": "lib", "runtime_generation": "g1"})
			return true
		}
		return false
	}
	value, werr := service.attach()
	if werr != nil {
		t.Fatal("attach failed", werr)
	}
	if value.(map[string]any)["public_origin"] != server.URL {
		t.Fatal("public origin is not the trusted Go management origin")
	}
	return service, daemon, server.URL
}

func invokeOK(t *testing.T, service *ReaderService, operation string, payload string) any {
	t.Helper()
	result, err := service.Invoke(readerCall{Operation: operation, Payload: json.RawMessage(payload)})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	var envelope struct {
		OK    bool            `json:"ok"`
		Value json.RawMessage `json:"value"`
		Error *wireError      `json:"error"`
	}
	if json.Unmarshal(encoded, &envelope) != nil || !envelope.OK {
		t.Fatalf("operation %s failed: %s", operation, encoded)
	}
	var value any
	json.Unmarshal(envelope.Value, &value)
	return value
}

func invokeFail(t *testing.T, service *ReaderService, operation string, payload string) *wireError {
	t.Helper()
	result, err := service.Invoke(readerCall{Operation: operation, Payload: json.RawMessage(payload)})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	var envelope struct {
		OK    bool       `json:"ok"`
		Error *wireError `json:"error"`
	}
	if json.Unmarshal(encoded, &envelope) != nil || envelope.OK || envelope.Error == nil {
		t.Fatalf("operation %s unexpectedly succeeded: %s", operation, encoded)
	}
	return envelope.Error
}

func TestReaderAttachUsesProductionSessionRoutesWithGoHeldBearer(t *testing.T) {
	var bearer []string
	service, daemon, base := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		bearer = append(bearer, r.Header.Get("Authorization"), r.Header.Get("Origin"))
		return false
	})
	value := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	if value["public_origin"] != base {
		t.Fatal("wrong public origin")
	}
	if _, ok := value["attachment_id"].(string); !ok || value["attachment_id"] == "" {
		t.Fatal("missing opaque attachment id")
	}
	seen := daemon.seen()
	if len(seen) != 4 {
		t.Fatal("attach must verify exactly the production session routes", seen)
	}
	for _, row := range seen {
		if row != "GET /api/v1/session" && row != "GET /api/v1/capabilities" {
			t.Fatal("unexpected route during attach", row)
		}
	}
	for index, header := range bearer {
		if index%2 == 0 && header != "Bearer test-owner-credential" {
			t.Fatal("bearer not held Go-side")
		}
		if index%2 == 1 && header != "" {
			t.Fatal("unexpected Origin header")
		}
	}
}

func TestReaderPairingExcludedWhileDecideIsTypedMapped(t *testing.T) {
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "POST" && r.URL.Path == "/api/v1/auth/pairings/p1/approve" {
			if r.Header.Get("If-Match") != `"etag-1"` || r.Header.Get("Idempotency-Key") != "op-9" {
				t.Error("missing pairing decision preconditions")
			}
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["code"] != "123456" || body["origin"] != "http://127.0.0.1:9270" {
				t.Error("wrong decision body", body)
			}
			dataReply(w, 202, map[string]any{"operation_id": "op-9", "state": "accepted"})
			return true
		}
		return false
	})
	pairing := invokeFail(t, service, "pair", `{"device_label":"native"}`)
	if pairing.Status != 501 || pairing.Code != "FEATURE_NOT_READY" {
		t.Fatal("pairing must be honestly unsupported", pairing)
	}
	decided := invokeOK(t, service, "decidePairing", `{"pairing_id":"p1","code":"123456","origin":"http://127.0.0.1:9270","etag":"\"etag-1\"","operation_id":"op-9","action":"approve"}`).(map[string]any)
	if decided["kind"] != "accepted" {
		t.Fatal("202 reservation must map to accepted", decided)
	}
}

func TestReaderPutUploadEntryBuildsFixedRequest(t *testing.T) {
	payload := bytes0255(300)
	encoded := base64.StdEncoding.EncodeToString(payload)
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != "PUT" || r.URL.Path != "/api/v1/uploads/up1/entries" {
			return false
		}
		switch r.URL.Query().Get("kind") {
		case "file":
			if r.URL.Query().Get("path") != "dir/big.bin" {
				t.Error("wrong entry query", r.URL.RawQuery)
			}
			if r.Header.Get("Content-Type") != "application/octet-stream" || r.Header.Get("Haeryong-Upload-Offset") != "0" ||
				r.Header.Get("Haeryong-Upload-Chunk-SHA256") != "digest-1" || r.Header.Get("Haeryong-Upload-Length") != "300" || r.Header.Get("Haeryong-Upload-Final") != "true" {
				t.Error("wrong upload headers")
			}
			body, _ := io.ReadAll(r.Body)
			if len(body) != len(payload) {
				t.Error("body bytes changed")
			}
			dataReply(w, 200, map[string]any{"accepted_offset": "300"})
			return true
		case "symlink":
			if r.Header.Get("Content-Type") != "application/json" {
				t.Error("symlink chunk must be JSON")
			}
			body, _ := io.ReadAll(r.Body)
			if string(body) != `{"link_target":"../target"}` {
				t.Error("symlink body not built Go-side", string(body))
			}
			dataReply(w, 200, map[string]any{"accepted_offset": "0"})
			return true
		}
		return false
	})
	acked := invokeOK(t, service, "putUploadEntry", fmt.Sprintf(`{"upload_id":"up1","chunk":{"path":"dir/big.bin","kind":"file","offset":0,"bytes_b64":%q,"chunkSha256":"digest-1","totalLength":300,"final":true}}`, encoded))
	if acked.(map[string]any)["accepted_offset"] != "300" {
		t.Fatal("ack not returned", acked)
	}
	link := invokeOK(t, service, "putUploadEntry", `{"upload_id":"up1","chunk":{"path":"a-link","kind":"symlink","offset":0,"bytes_b64":"","linkTarget":"../target"}}`)
	if link == nil {
		t.Fatal("symlink chunk not accepted")
	}
}

func TestReaderJSONBodiesCarryTheStrictJSONMediaType(t *testing.T) {
	media := map[string]string{}
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Body == nil || r.ContentLength == 0 {
			return false
		}
		if previous, seen := media[r.URL.Path]; !seen || previous != r.Header.Get("Content-Type") {
			media[r.URL.Path] = r.Header.Get("Content-Type")
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/uploads":
			dataReply(w, 201, map[string]any{"upload_id": "up9"})
			return true
		case r.Method == "POST" && r.URL.Path == "/api/v1/uploads/up9/complete":
			dataReply(w, 200, map[string]any{"sealed": true})
			return true
		case r.Method == "POST" && r.URL.Path == "/api/v1/imports":
			dataReply(w, 202, map[string]any{"import_id": "im1"})
			return true
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/reject"):
			dataReply(w, 200, map[string]any{"decision": "rejected"})
			return true
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/render-sessions"):
			dataReply(w, 200, map[string]any{"session_id": "r1", "version_id": "v1"})
			return true
		}
		return false
	})
	invokeOK(t, service, "createUpload", `{"request":{"target":{"project_id":"p","document_id":null}},"operation_id":"k1"}`)
	invokeOK(t, service, "createContextUpload", `{"binding":{"version_id":"v1","page_path":null,"render_instance_id":null},"operation_id":"k2"}`)
	invokeOK(t, service, "completeUpload", `{"upload_id":"up9","checkpoint":{},"operation_id":"k3"}`)
	invokeOK(t, service, "importPublish", `{"request":{"upload_id":"up9","base":null},"operation_id":"k4"}`)
	invokeOK(t, service, "decidePairing", `{"pairing_id":"p9","code":"1","origin":"http://127.0.0.1:9270","etag":"\"e\"","operation_id":"k5","action":"reject"}`)
	invokeOK(t, service, "renderSessionOpen", `{"version_id":"v1","operation_id":"k6"}`)
	for path, contentType := range media {
		if contentType != "application/json" {
			t.Fatalf("route %s sent Content-Type %q; the strict daemon decoder requires application/json", path, contentType)
		}
	}
	if len(media) < 5 {
		t.Fatal("expected the JSON routes to carry bodies", media)
	}
}

func TestReaderEmptyNon204SuccessIsUncertainAnd204IsNull(t *testing.T) {
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/approve") {
			w.WriteHeader(201)
			return true
		}
		if r.Method == "DELETE" && r.URL.Path == "/api/v1/sessions/other" {
			w.WriteHeader(204)
			return true
		}
		return false
	})
	empty := invokeFail(t, service, "decidePairing", `{"pairing_id":"p2","code":"1","origin":"http://127.0.0.1:9270","etag":"\"e\"","operation_id":"k8","action":"approve"}`)
	if empty.Code != "UNCERTAIN_RESPONSE" {
		t.Fatal("an empty committed 201 must stay uncertain for original-K recovery", empty)
	}
	invokeOK(t, service, "revokeSession", `{"session_id":"other"}`)
}

func TestReaderRetryAfterHeaderPropagates(t *testing.T) {
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/reject") {
			w.Header().Set("Retry-After", "7")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(429)
			w.Write([]byte(`{"error":{"code":"RATE_LIMITED","retryable":true}}`))
			return true
		}
		return false
	})
	werr := invokeFail(t, service, "decidePairing", `{"pairing_id":"p3","code":"1","origin":"http://127.0.0.1:9270","etag":"\"e\"","operation_id":"k9","action":"reject"}`)
	if werr.Code != "RATE_LIMITED" || werr.RetryAfter != 7 {
		t.Fatal("Retry-After header seconds must reach the wire error", werr)
	}
}

func bytes0255(length int) []byte {
	body := make([]byte, length)
	for index := range body {
		body[index] = byte(index % 256)
	}
	return body
}

func TestReaderVerifyContextFreshExact304Proof(t *testing.T) {
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != "GET" || r.URL.Path != "/api/v1/contexts/ref-1" {
			return false
		}
		if r.Header.Get("If-None-Match") != `"sha256-abc"` {
			t.Error("missing exact strong ETag")
		}
		expected, err := base64.RawURLEncoding.DecodeString(r.Header.Get("Haeryong-Context-Binding"))
		if err != nil || string(expected) != `{"version_id":"v1","page_path":null,"render_instance_id":null}` {
			t.Error("context binding header is not the canonical three-key form", r.Header.Get("Haeryong-Context-Binding"))
		}
		if r.URL.Query().Encode() != "" {
			t.Error("fresh proof must be bodyless with no query")
		}
		w.Header().Set("ETag", `"sha256-abc"`)
		w.Header().Set("Haeryong-Context-Verified", "fresh")
		w.WriteHeader(304)
		return true
	})
	proof := invokeOK(t, service, "verifyContextFresh", `{"context_ref":"ref-1","binding":{"version_id":"v1","page_path":null,"render_instance_id":null},"bytes_sha256":"abc"}`).(map[string]any)
	if proof["verified"] != true || proof["etag"] != `"sha256-abc"` {
		t.Fatal("proof not verified", proof)
	}
}

func TestReaderVerifyContextFreshRejectsInvalidProof(t *testing.T) {
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v1/contexts/ref-2" {
			return false
		}
		w.Header().Set("ETag", `"sha256-abc"`)
		// Missing the verified marker must not pass as a fresh proof.
		w.WriteHeader(304)
		return true
	})
	werr := invokeFail(t, service, "verifyContextFresh", `{"context_ref":"ref-2","binding":{"version_id":"v1","page_path":null,"render_instance_id":null},"bytes_sha256":"abc"}`)
	if werr.Code != "CONTEXT_PROOF_INVALID" {
		t.Fatal("unmarked 304 accepted", werr)
	}
}

func TestReaderStreamsDeliverExactBytesEofAndEraGuards(t *testing.T) {
	expected := bytes0255(256 * 513)
	streamBody := append([]byte(nil), expected...)
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && r.URL.Path == "/api/v1/versions/v1/file" && r.URL.Query().Get("path") == "attachment.bin" {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(200)
			w.Write(streamBody)
			return true
		}
		return false
	})
	opened := invokeOK(t, service, "fileStream", `{"version_id":"v1","path":"attachment.bin"}`).(map[string]any)
	streamID := opened["stream_id"].(string)
	total := 0
	for {
		part := invokeOK(t, service, "streamRead", fmt.Sprintf(`{"stream_id":%q,"max_bytes":65536}`, streamID)).(map[string]any)
		if data, ok := part["data_b64"].(string); ok && data != "" {
			raw, err := base64.StdEncoding.DecodeString(data)
			if err != nil {
				t.Fatal("bad base64")
			}
			if len(raw) > 65536 {
				t.Fatalf("reply exceeded max_bytes: %d", len(raw))
			}
			if string(raw) != string(expected[total:total+len(raw)]) {
				t.Fatalf("byte drift at %d", total)
			}
			total += len(raw)
		}
		if part["eof"] == true {
			break
		}
	}
	if total != len(expected) {
		t.Fatalf("stream did not deliver exact bytes: %d of %d", total, len(expected))
	}
	if werr := invokeFail(t, service, "streamRead", fmt.Sprintf(`{"stream_id":%q,"max_bytes":1024}`, streamID)); werr.Code != "STREAM_NOT_FOUND" {
		t.Fatal("completed stream must be dropped", werr)
	}
	if werr := invokeFail(t, service, "streamRead", `{"stream_id":"foreign","max_bytes":1024}`); werr.Status != 404 {
		t.Fatal("foreign stream ids are typed errors", werr)
	}
	if werr := invokeFail(t, service, "streamRead", `{"stream_id":"x","max_bytes":0}`); werr.Code != "NATIVE_PAYLOAD_INVALID" {
		t.Fatal("out-of-range max_bytes must be rejected", werr)
	}

	second := invokeOK(t, service, "fileStream", `{"version_id":"v1","path":"attachment.bin"}`).(map[string]any)
	secondID := second["stream_id"].(string)
	// A new attachment era invalidates every stream owned by the old one,
	// including a pull that started before the replacement landed.
	if _, werr := service.attach(); werr != nil {
		t.Fatal(werr)
	}
	stale := invokeFail(t, service, "streamRead", fmt.Sprintf(`{"stream_id":%q,"max_bytes":1024}`, secondID))
	if stale.Status != 409 || stale.Code != "STREAM_ATTACHMENT_REPLACED" {
		t.Fatal("era replacement must fail typed without clean EOF", stale)
	}
}

func TestReaderStreamPullFailsClosedAfterOtherDeviceRevocation(t *testing.T) {
	expected := bytes0255(256 * 300)
	revoked := false
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if revoked && r.Method == "GET" && r.URL.Path == "/api/v1/events" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			w.Write([]byte(`{"error":{"code":"SESSION_REVOKED","retryable":false}}`))
			return true
		}
		if r.Method == "GET" && r.URL.Path == "/api/v1/versions/v1/file" && r.URL.Query().Get("path") == "attachment.bin" {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(200)
			w.Write(expected)
			return true
		}
		return false
	})
	opened := invokeOK(t, service, "fileStream", `{"version_id":"v1","path":"attachment.bin"}`).(map[string]any)
	streamID := opened["stream_id"].(string)
	first := invokeOK(t, service, "streamRead", fmt.Sprintf(`{"stream_id":%q,"max_bytes":256}`, streamID)).(map[string]any)
	if first["eof"] != false {
		t.Fatal("first pull should deliver bytes without EOF")
	}
	// Another device revokes this session while bytes are already prefetched.
	revoked = true
	denied := invokeFail(t, service, "streamRead", fmt.Sprintf(`{"stream_id":%q,"max_bytes":256}`, streamID))
	if denied.Status != 401 || denied.Code != "SESSION_REVOKED" {
		t.Fatal("revocation must fail the pull typed without leaking bytes", denied)
	}
	// The owned stream is gone; nothing completes cleanly afterwards.
	if werr := invokeFail(t, service, "streamRead", fmt.Sprintf(`{"stream_id":%q,"max_bytes":256}`, streamID)); werr.Code != "STREAM_NOT_FOUND" {
		t.Fatal("revoked stream must be dropped", werr)
	}
}

func TestReaderStaleDisposeNeverKillsNewerAttachmentHandles(t *testing.T) {
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && r.URL.Path == "/api/v1/versions/v1/file" && r.URL.Query().Get("path") == "attachment.bin" {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(200)
			w.Write(bytes0255(512))
			return true
		}
		return false
	})
	first := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	invokeOK(t, service, "attachSession", "{}")
	stream := invokeOK(t, service, "fileStream", `{"version_id":"v1","path":"attachment.bin"}`).(map[string]any)
	// The OLD attachment's disposal callback arrives late: it must not touch
	// the newer attachment's stream or attachment.
	invokeOK(t, service, "disposeSession", fmt.Sprintf(`{"attachment_id":%q}`, first["attachment_id"]))
	pull := invokeOK(t, service, "streamRead", fmt.Sprintf(`{"stream_id":%q,"max_bytes":512}`, stream["stream_id"].(string))).(map[string]any)
	if pull["eof"] != true {
		t.Fatal("the newer attachment's stream must stay usable", pull)
	}
	invokeOK(t, service, "capabilities", "{}")
}

func TestReaderDisposeValidatesCapturedAttachmentAndClosesOwnedRenders(t *testing.T) {
	closed := []string{}
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/render-sessions") {
			dataReply(w, 200, map[string]any{"session_id": "render-1", "version_id": "v1"})
			return true
		}
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/close") {
			closed = append(closed, r.URL.Path)
			w.WriteHeader(204)
			return true
		}
		if r.Method == "DELETE" && r.URL.Path == "/api/v1/session" {
			w.WriteHeader(204)
			return true
		}
		return false
	})
	invokeOK(t, service, "renderSessionOpen", `{"version_id":"v1","operation_id":"k1"}`)
	// A stale callback cannot dispose a newer attachment.
	invokeOK(t, service, "disposeSession", `{"attachment_id":"not-the-current"}`)
	attachment := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	if _, werr := service.invoke("revokeOwnSession", nil); werr != nil {
		t.Fatal("revoke failed", werr)
	}
	invokeOK(t, service, "disposeSession", fmt.Sprintf(`{"attachment_id":%q}`, attachment["attachment_id"]))
	if len(closed) != 1 || !strings.Contains(closed[0], "render-1") {
		t.Fatal("disposal must explicitly close the owned renderer delivery", closed)
	}
	if _, err := os.Stat(filepath.Join(service.state, "native-session")); !os.IsNotExist(err) {
		t.Fatal("explicit logout disposal must drop the Go-held credential file")
	}
}

func TestReaderNormalDisposeRetainsRememberedCredential(t *testing.T) {
	service, _, _ := newReaderFixture(t, nil)
	attachment := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	invokeOK(t, service, "disposeSession", fmt.Sprintf(`{"attachment_id":%q}`, attachment["attachment_id"]))
	if _, err := os.Stat(filepath.Join(service.state, "native-session")); err != nil {
		t.Fatal("non-logout disposal must retain the remembered credential")
	}
	if werr := invokeFail(t, service, "capabilities", "{}"); werr.Code != "NATIVE_SESSION_NOT_ATTACHED" {
		t.Fatal("operations after disposal must fail closed", werr)
	}
	// An ordinary close/restart cycle reattaches with the same remembered
	// credential as a genuinely current authority.
	reattached := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	if reattached["attachment_id"] == "" {
		t.Fatal("ordinary reattach failed")
	}
}

func TestReaderEventsPollLivenessOnly(t *testing.T) {
	polls := 0
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && r.URL.Path == "/api/v1/events" && r.URL.Query().Get("transport") == "poll" {
			polls++
			if polls == 1 || polls == 2 {
				dataReply(w, 200, map[string]any{"items": []any{}, "stream_epoch": "e", "head": "1", "next_cursor": "e:1", "resync": nil})
				return true
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			w.Write([]byte(`{"error":{"code":"SESSION_REVOKED","retryable":false}}`))
			return true
		}
		return false
	})
	subscription := invokeOK(t, service, "subscribeEvents", "{}").(map[string]any)
	id := subscription["subscription_id"].(string)
	first := invokeOK(t, service, "eventsPoll", fmt.Sprintf(`{"subscription_id":%q}`, id)).(map[string]any)
	if first["closed"] != false {
		t.Fatal("healthy poll must stay open", first)
	}
	dead := invokeOK(t, service, "eventsPoll", fmt.Sprintf(`{"subscription_id":%q}`, id)).(map[string]any)
	if dead["closed"] != true {
		t.Fatal("definite 401 must close the subscription", dead)
	}
	if werr := invokeFail(t, service, "eventsPoll", `{"subscription_id":"foreign"}`); werr.Status != 404 {
		t.Fatal("foreign subscription ids are typed errors", werr)
	}
}

func TestReaderPersonalPutPreconditionsAndTypedErrors(t *testing.T) {
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "PUT" && r.URL.Path == "/api/v1/versions/v1/reading-position" {
			if r.URL.Query().Encode() != "" {
				t.Error("reading-position must not carry a page query")
			}
			if r.Header.Get("If-None-Match") != "*" && r.Header.Get("If-Match") != `"opaque-2"` {
				t.Error("missing personal precondition")
			}
			if r.Header.Get("Idempotency-Key") != "op-3" {
				t.Error("missing idempotency key")
			}
			dataReply(w, 200, map[string]any{"exists": true, "etag": `"opaque-3"`})
			return true
		}
		if r.Method == "PUT" && r.URL.Path == "/api/v1/versions/v1/page-position" {
			if r.URL.Query().Get("page") != "pages/nested/한글.html" {
				t.Error("page-position must carry its exact page", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(412)
			w.Write([]byte(`{"error":{"code":"PRECONDITION_FAILED","retryable":false}}`))
			return true
		}
		return false
	})
	create := invokeOK(t, service, "personalPut", `{"kind":"reading-position","version_id":"v1","page":null,"body":{"page_path":"a.html"},"precondition":{"create":true},"operation_id":"op-3"}`).(map[string]any)
	if create["etag"] != `"opaque-3"` {
		t.Fatal("create result missing", create)
	}
	update := invokeOK(t, service, "personalPut", `{"kind":"reading-position","version_id":"v1","page":null,"body":{"page_path":"b.html"},"precondition":{"if_match":"\"opaque-2\""},"operation_id":"op-3"}`).(map[string]any)
	if update["etag"] != `"opaque-3"` {
		t.Fatal("update result missing", update)
	}
	conflict := invokeFail(t, service, "personalPut", `{"kind":"page-position","version_id":"v1","page":"pages/nested/한글.html","body":{"position":{}},"precondition":{"if_match":"\"opaque-1\""},"operation_id":"op-4"}`)
	if conflict.Status != 412 || conflict.Code != "PRECONDITION_FAILED" || conflict.Retryable {
		t.Fatal("typed conflict lost", conflict)
	}
}

func TestReaderUnknownOperationIsRejected(t *testing.T) {
	service, _, _ := newReaderFixture(t, nil)
	if werr := invokeFail(t, service, "genericProxy", `{"path":"/api/v1/session"}`); werr.Code != "NATIVE_UNKNOWN_OPERATION" {
		t.Fatal("undeclared operation accepted", werr)
	}
}

func TestReaderTransportFailureIsNetworkError(t *testing.T) {
	// A fresh service whose base endpoint is already closed.
	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	base := closed.URL
	closed.Close()
	orphan := NewReaderService(base, t.TempDir(), "token")
	if _, werr := orphan.attach(); werr == nil || werr.Code != "NETWORK_ERROR" {
		t.Fatal("transport failure must map to NETWORK_ERROR", werr)
	}
}

func TestReaderRejectsRedirectsWithBearerParity(t *testing.T) {
	var redirected bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !redirected {
			redirected = true
			http.Redirect(w, r, "http://127.0.0.1:9/nope", http.StatusFound)
			return
		}
		dataReply(w, 200, map[string]any{"session_id": "s1"})
	}))
	defer server.Close()
	service := NewReaderService(server.URL, t.TempDir(), "secret-token")
	werr := invokeFail(t, service, "attachSession", "{}")
	if werr == nil || werr.Code != "NETWORK_ERROR" {
		t.Fatal("redirect must be rejected like the browser redirect:error parity", werr)
	}
}

// coEofReader models a transport whose final read returns (n>0, io.EOF).
type coEofReader struct{ remaining []byte }

func (r *coEofReader) Read(p []byte) (int, error) {
	n := copy(p, r.remaining)
	r.remaining = r.remaining[n:]
	if len(r.remaining) == 0 {
		return n, io.EOF
	}
	return n, nil
}

func TestReaderStreamFinalNonemptyChunkCarriesEofTogether(t *testing.T) {
	service, _, _ := newReaderFixture(t, nil)
	auth, werr := service.authority()
	if werr != nil {
		t.Fatal(werr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx
	// A body whose final read returns (n>0, io.EOF) — exactly what a real
	// transport may deliver — must carry its bytes together with the EOF.
	service.mu.Lock()
	service.streams["synthetic"] = &readerStream{id: "synthetic", body: io.NopCloser(&coEofReader{remaining: bytes0255(250)}), cancel: cancel, auth: auth, versionID: "v1", epoch: "e"}
	service.mu.Unlock()
	for index := 0; index < 2; index++ {
		pull := invokeOK(t, service, "streamRead", `{"stream_id":"synthetic","max_bytes":100}`).(map[string]any)
		if pull["eof"] != false {
			t.Fatal("early pull must not report EOF", pull)
		}
	}
	final := invokeOK(t, service, "streamRead", `{"stream_id":"synthetic","max_bytes":100}`).(map[string]any)
	if final["eof"] != true {
		t.Fatal("final pull must deliver EOF with its bytes", final)
	}
	raw, err := base64.StdEncoding.DecodeString(final["data_b64"].(string))
	if err != nil || len(raw) != 50 {
		t.Fatal("final nonempty EOF chunk lost bytes", len(raw), err)
	}
	if werr := invokeFail(t, service, "streamRead", `{"stream_id":"synthetic","max_bytes":100}`); werr.Code != "STREAM_NOT_FOUND" {
		t.Fatal("completed stream must be dropped", werr)
	}
}

func TestReaderStreamPullFailsTypedWhenVersionHidden(t *testing.T) {
	hidden := false
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if hidden && r.Method == "GET" && r.URL.Path == "/api/v1/versions/v1" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(404)
			w.Write([]byte(`{"error":{"code":"NOT_FOUND","retryable":false}}`))
			return true
		}
		if r.Method == "GET" && r.URL.Path == "/api/v1/versions/v1/file" && r.URL.Query().Get("path") == "attachment.bin" {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(200)
			w.Write(bytes0255(1024))
			return true
		}
		return false
	})
	opened := invokeOK(t, service, "fileStream", `{"version_id":"v1","path":"attachment.bin"}`).(map[string]any)
	streamID := opened["stream_id"].(string)
	if pull := invokeOK(t, service, "streamRead", fmt.Sprintf(`{"stream_id":%q,"max_bytes":512}`, streamID)); pull == nil {
		t.Fatal("healthy pull failed")
	}
	hidden = true
	werr := invokeFail(t, service, "streamRead", fmt.Sprintf(`{"stream_id":%q,"max_bytes":512}`, streamID))
	if werr.Status != 404 || werr.Code != "STREAM_TARGET_HIDDEN" {
		t.Fatal("a hidden/purged target must fail typed without old bytes", werr)
	}
}

func TestReaderStreamPullFailsTypedAfterDaemonRestart(t *testing.T) {
	epoch := "e1"
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && r.URL.Path == "/api/v1/events" && r.URL.Query().Get("transport") == "poll" {
			dataReply(w, 200, map[string]any{"items": []any{}, "stream_epoch": epoch, "head": "1", "next_cursor": "e:1", "resync": nil})
			return true
		}
		if r.Method == "GET" && r.URL.Path == "/api/v1/versions/v1/file" && r.URL.Query().Get("path") == "attachment.bin" {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(200)
			w.Write(bytes0255(1024))
			return true
		}
		return false
	})
	opened := invokeOK(t, service, "fileStream", `{"version_id":"v1","path":"attachment.bin"}`).(map[string]any)
	streamID := opened["stream_id"].(string)
	if pull := invokeOK(t, service, "streamRead", fmt.Sprintf(`{"stream_id":%q,"max_bytes":512}`, streamID)); pull == nil {
		t.Fatal("healthy pull failed")
	}
	epoch = "e2"
	werr := invokeFail(t, service, "streamRead", fmt.Sprintf(`{"stream_id":%q,"max_bytes":512}`, streamID))
	if werr.Status != 409 || werr.Code != "STREAM_DAEMON_ERA_CHANGED" {
		t.Fatal("a daemon era change must fail typed without clean EOF", werr)
	}
}

func TestReaderUncertainSuccessKeepsOriginalKRecoveryPath(t *testing.T) {
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/approve") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			// A committed decision whose body is unreadable: the original K
			// must stay recoverable, so this must signal uncertainty.
			w.Write([]byte(`{"data":{"brok`))
			return true
		}
		return false
	})
	werr := invokeFail(t, service, "decidePairing", `{"pairing_id":"p1","code":"123456","origin":"http://127.0.0.1:9270","etag":"\"etag-1\"","operation_id":"k-7","action":"approve"}`)
	if werr.Code != "UNCERTAIN_RESPONSE" || !werr.Retryable {
		t.Fatal("unreadable committed body must be a closed uncertain signal", werr)
	}
}

func TestReaderDatalessSuccessIsNullNotFailure(t *testing.T) {
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && r.URL.Path == "/api/v1/status" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			w.Write([]byte(`{}`))
			return true
		}
		return false
	})
	if value := invokeOK(t, service, "serviceStatus", "{}"); value != nil {
		t.Fatal("a data-less success must map to null like the browser", value)
	}
}

func TestReaderLogoutForgetsCredentialFileBeforePendingRevokeCompletes(t *testing.T) {
	block := make(chan struct{})
	var releaseBlock sync.Once
	defer releaseBlock.Do(func() { close(block) })
	service, daemon, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "DELETE" && r.URL.Path == "/api/v1/session" {
			// The server ack hangs: a process kill in this window must still
			// leave no durable credential behind.
			<-block
			w.WriteHeader(204)
			return true
		}
		return false
	})
	done := make(chan map[string]any, 1)
	go func() {
		result, err := service.Invoke(readerCall{Operation: "revokeOwnSession"})
		if err != nil {
			done <- nil
			return
		}
		done <- result
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(service.state, "native-session")); os.IsNotExist(err) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(service.state, "native-session")); !os.IsNotExist(err) {
		t.Fatal("explicit logout must persist the local forget before the revoke completes")
	}
	// While the revoke is still pending, no attach may reuse the forgotten
	// bearer: the typed logged-out error must arrive without any dispatch.
	before := len(daemon.seen())
	if werr := invokeFail(t, service, "attachSession", "{}"); werr.Code != "NATIVE_SESSION_LOGGED_OUT" {
		t.Fatal("pending logout attach must fail honestly", werr)
	}
	if len(daemon.seen()) != before {
		t.Fatal("pending logout attach dispatched with the forgotten bearer", daemon.seen()[before:])
	}
	// The in-memory captured bearer still serves this one revoke attempt.
	releaseBlock.Do(func() { close(block) })
	result := <-done
	if result == nil || result["ok"] != true {
		t.Fatal("retained captured bearer must complete the revoke attempt", result)
	}
}

func TestReaderLogoutDisposeThenAttachStaysUnauthenticated(t *testing.T) {
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "DELETE" && r.URL.Path == "/api/v1/session" {
			w.WriteHeader(503)
			w.Header().Set("Retry-After", "1")
			return true
		}
		return false
	})
	attachment := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	// Unknown server ack: the local forget already happened either way.
	if werr := invokeFail(t, service, "revokeOwnSession", "{}"); werr == nil || werr.Status != 503 {
		t.Fatal("expected the unknown revoke outcome to surface honestly", werr)
	}
	invokeOK(t, service, "disposeSession", fmt.Sprintf(`{"attachment_id":%q}`, attachment["attachment_id"]))
	if werr := invokeFail(t, service, "attachSession", "{}"); werr.Code != "NATIVE_SESSION_LOGGED_OUT" {
		t.Fatal("post-logout attach must never reauthenticate the forgotten credential", werr)
	}
	if werr := invokeFail(t, service, "capabilities", "{}"); werr.Code != "NATIVE_SESSION_LOGGED_OUT" {
		t.Fatal("post-logout operations must fail closed", werr)
	}
}

// slowBody drips bytes so a pull is still blocking when the stream is
// concurrently cancelled; concurrent Read/Close is safe per net/http.
type slowBody struct {
	mu     sync.Mutex
	closed bool
}

func (b *slowBody) Read(p []byte) (int, error) {
	time.Sleep(50 * time.Millisecond)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, errors.New("read on closed body")
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = 0xAB
	return 1, nil
}

func (b *slowBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return nil
}

func TestReaderConcurrentCancelDuringBlockingPullIsRaceSafe(t *testing.T) {
	service, _, _ := newReaderFixture(t, nil)
	auth, werr := service.authority()
	if werr != nil {
		t.Fatal(werr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx
	body := &slowBody{}
	service.mu.Lock()
	service.streams["slow"] = &readerStream{id: "slow", body: body, cancel: cancel, auth: auth, versionID: "v1", epoch: "e"}
	service.mu.Unlock()
	results := make(chan *wireError, 1)
	go func() {
		_, werr := service.invoke("streamRead", json.RawMessage(`{"stream_id":"slow","max_bytes":4096}`))
		results <- werr
	}()
	// Cancel while the pull above is still blocking inside Read.
	time.Sleep(15 * time.Millisecond)
	service.dropStream("slow")
	select {
	case werr := <-results:
		// Either a typed failure or nothing clean-fake: a cancelled pull may
		// fail typed, but must never panic or fabricate a clean EOF alone.
		if werr != nil && werr.Code != "NETWORK_ERROR" && werr.Code != "STREAM_NOT_FOUND" {
			t.Fatal("unexpected typed failure for cancelled pull", werr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled pull never returned")
	}
	if werr := invokeFail(t, service, "streamRead", `{"stream_id":"slow","max_bytes":64}`); werr.Code != "STREAM_NOT_FOUND" {
		t.Fatal("cancelled stream must be gone", werr)
	}
}

func TestReaderLocalForgetFailsHonestlyAndStaysRetryable(t *testing.T) {
	state := t.TempDir()
	credential := filepath.Join(state, "native-session")
	if err := os.WriteFile(credential, []byte("owner-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "DELETE" && r.URL.Path == "/api/v1/session" {
			w.WriteHeader(204)
			return true
		}
		return false
	})
	service.state = state
	service.mu.Lock()
	service.token = "owner-token"
	service.mu.Unlock()
	// A genuinely NEWER authority is a newly prepaired credential: install
	// one and attach with it while the old revoke is still pending.
	// Make the state directory read-only so the unlink genuinely fails.
	if err := os.Chmod(state, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(state, 0700) })
	attachment := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	werr := invokeFail(t, service, "revokeOwnSession", "{}")
	if werr.Code != "NATIVE_LOCAL_FORGET_FAILED" || !werr.Retryable {
		t.Fatal("a real unlink failure must surface even with a server 204 ack", werr)
	}
	if werr.Details["server_status"] != nil || werr.Details["server_outcome"] != "unknown" {
		t.Fatal("a prompt local failure cannot invent a server acknowledgement", werr.Details)
	}
	if _, err := os.Stat(credential); err != nil {
		t.Fatal("the credential file must still exist after the failed local forget")
	}
	// RAM is already blocked: attach cannot reuse the forgotten bearer. A
	// capture observer proves the blocked attach emits no attachment receipt.
	var captureMu sync.Mutex
	attachments := 0
	service.SetDiagnostics(func(entry map[string]any) {
		captureMu.Lock()
		defer captureMu.Unlock()
		if entry["event"] == "attachSession" {
			attachments++
		}
	})
	if werr := invokeFail(t, service, "attachSession", "{}"); werr.Code != "NATIVE_SESSION_LOGGED_OUT" {
		t.Fatal("RAM forget must block reauthentication immediately", werr)
	}
	captureMu.Lock()
	blockedAttachments := attachments
	captureMu.Unlock()
	if blockedAttachments != 0 {
		t.Error("blocked attach cannot produce an attachment")
	}
	// Restore permission; the disposal retries the obligation and completes.
	failedDispose := invokeFail(t, service, "disposeSession", fmt.Sprintf(`{"attachment_id":%q}`, attachment["attachment_id"]))
	if failedDispose.Code != "NATIVE_LOCAL_FORGET_FAILED" {
		t.Fatal("disposal must retain its pending forget obligation", failedDispose)
	}
	if raw, err := os.ReadFile(credential); err != nil || strings.TrimSpace(string(raw)) != "owner-token" {
		t.Fatal("our surviving credential must stay identifiable", err)
	}
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	invokeOK(t, service, "disposeSession", fmt.Sprintf(`{"attachment_id":%q}`, attachment["attachment_id"]))
	if _, err := os.Stat(credential); !os.IsNotExist(err) {
		t.Fatal("the retry after permission restoration must complete the forget")
	}
}

func TestReaderLateForgetRetryNeverDeletesNewerCredential(t *testing.T) {
	state := t.TempDir()
	credential := filepath.Join(state, "native-session")
	if err := os.WriteFile(credential, []byte("old-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "DELETE" && r.URL.Path == "/api/v1/session" {
			w.WriteHeader(503)
			w.Header().Set("Retry-After", "1")
			return true
		}
		return false
	})
	service.state = state
	service.mu.Lock()
	service.token = "old-token"
	service.mu.Unlock()
	attachment := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	if werr := invokeFail(t, service, "revokeOwnSession", "{}"); werr == nil {
		t.Fatal("expected the revoke outcome to surface")
	}
	// The old forget succeeded here; simulate the pending-obligation shape:
	// a NEWER credential file appears (new binding), and a late cleanup
	// retry must leave it intact.
	if err := os.WriteFile(credential, []byte("genuinely-newer-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	service.forgetNeeded = true
	service.forgetDone = false
	service.logoutBearer = "old-token"
	service.mu.Unlock()
	service.mu.Lock()
	service.forgetCredentialFileLocked()
	service.mu.Unlock()
	if raw, err := os.ReadFile(credential); err != nil || strings.TrimSpace(string(raw)) != "genuinely-newer-token" {
		t.Fatal("a late old forget retry deleted a newer binding's credential", err)
	}
	service.mu.Lock()
	retained := service.logoutBearer != ""
	service.mu.Unlock()
	if !retained {
		t.Fatal("the guarded retry must retain the captured bearer when it declined to unlink")
	}
	werr := invokeFail(t, service, "disposeSession", fmt.Sprintf(`{"attachment_id":%q}`, attachment["attachment_id"]))
	if werr.Code != "NATIVE_LOCAL_FORGET_FAILED" {
		t.Fatal("a failed guarded forget must surface as a closed typed local failure on disposal", werr)
	}
	if raw, err := os.ReadFile(credential); err != nil || strings.TrimSpace(string(raw)) != "genuinely-newer-token" {
		t.Fatal("the failed disposal retry still deleted the newer credential", err)
	}
}

func TestReaderAlreadyAbsentFileCountsAsForgotten(t *testing.T) {
	state := t.TempDir()
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "DELETE" && r.URL.Path == "/api/v1/session" {
			w.WriteHeader(204)
			return true
		}
		return false
	})
	service.state = state
	service.mu.Lock()
	service.token = "owner-token"
	service.mu.Unlock()
	// The file never existed (or an earlier attempt removed it): absence is
	// already-forgotten and the revoke outcome is the plain server result.
	os.Remove(filepath.Join(state, "native-session"))
	value := invokeOK(t, service, "revokeOwnSession", "{}")
	if value != nil {
		t.Fatal("204 revoke maps to null value", value)
	}
}

func TestReaderNormalForgetPrecedesPendingRevoke(t *testing.T) {
	block := make(chan struct{})
	var releaseBlock sync.Once
	defer releaseBlock.Do(func() { close(block) })
	state := t.TempDir()
	credential := filepath.Join(state, "native-session")
	if err := os.WriteFile(credential, []byte("owner-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "DELETE" && r.URL.Path == "/api/v1/session" {
			<-block
			w.WriteHeader(204)
			return true
		}
		return false
	})
	service.state = state
	service.mu.Lock()
	service.token = "owner-token"
	service.mu.Unlock()
	invokeOK(t, service, "attachSession", "{}")
	done := make(chan *wireError, 1)
	go func() {
		_, werr := service.invoke("revokeOwnSession", nil)
		done <- werr
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(credential); os.IsNotExist(err) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(credential); !os.IsNotExist(err) {
		t.Fatal("normal forget must remove the file before the network completes")
	}
	releaseBlock.Do(func() { close(block) })
	if werr := <-done; werr != nil {
		t.Fatal("normal forget with a later server ack must not report failure", werr)
	}
	if _, err := os.Stat(credential); !os.IsNotExist(err) {
		t.Fatal("restart must stay logged out: the credential file is gone")
	}
}

func TestReaderLateRevokeNeverMarksNewerAttachment(t *testing.T) {
	block := make(chan struct{})
	var releaseBlock sync.Once
	defer releaseBlock.Do(func() { close(block) })
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "DELETE" && r.URL.Path == "/api/v1/session" {
			<-block
			w.WriteHeader(503)
			w.Header().Set("Retry-After", "1")
			return true
		}
		return false
	})
	done := make(chan map[string]any, 1)
	go func() {
		result, _ := service.Invoke(readerCall{Operation: "revokeOwnSession"})
		done <- result
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(service.state, "native-session")); os.IsNotExist(err) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(service.state, "native-session")); !os.IsNotExist(err) {
		t.Fatal("logout forget must stay persisted")
	}
	// A genuinely NEWER authority is a newly prepaired credential, never the
	// forgotten bearer: install one and attach with it while the old revoke
	// is still pending.
	if err := os.WriteFile(filepath.Join(service.state, "native-session"), []byte("genuinely-new-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	service.token = "genuinely-new-credential"
	service.mu.Unlock()
	newer := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	releaseBlock.Do(func() { close(block) })
	result := <-done
	if result == nil || result["ok"] != false {
		t.Fatal("the old revoke's unknown outcome must surface honestly", result)
	}
	// The new attachment is NOT marked for a second forget: disposing it
	// retains the newly installed credential file.
	invokeOK(t, service, "disposeSession", fmt.Sprintf(`{"attachment_id":%q}`, newer["attachment_id"]))
	if _, err := os.Stat(filepath.Join(service.state, "native-session")); err != nil {
		t.Fatal("a genuinely newer binding's credential must survive; the late old revoke must not mark it")
	}
}

func TestReaderLocalForgetErrorDoesNotWaitForHangingRevoke(t *testing.T) {
	state := t.TempDir()
	credential := filepath.Join(state, "native-session")
	if err := os.WriteFile(credential, []byte("owner-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "DELETE" && r.URL.Path == "/api/v1/session" {
			close(entered)
			<-release
			w.WriteHeader(503)
			return true
		}
		return false
	})
	// The local result must not wait for the hanging remote revoke. Test
	// cleanup does wait for its final observation before fixture directories
	// are removed; the historical file observer could otherwise recreate a
	// receipt while TempDir cleanup was removing that directory.
	finished := make(chan struct{})
	started := false
	service.SetDiagnostics(func(entry map[string]any) {
		if entry["event"] == "revokeOwnSession" {
			close(finished)
		}
	})
	t.Cleanup(func() {
		close(release)
		if started {
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Error("best-effort revoke did not finish before fixture cleanup")
			}
		}
	})
	defer os.Chmod(state, 0700)
	service.state = state
	service.mu.Lock()
	service.token = "owner-token"
	service.mu.Unlock()
	if err := os.Chmod(state, 0500); err != nil {
		t.Fatal(err)
	}
	attachment := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	done := make(chan *wireError, 1)
	started = true
	go func() { _, werr := service.invoke("revokeOwnSession", nil); done <- werr }()
	select {
	case werr := <-done:
		if werr == nil || werr.Code != "NATIVE_LOCAL_FORGET_FAILED" || werr.Details["server_outcome"] != "unknown" {
			t.Fatal("local failure must arrive without a fabricated server outcome", werr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("local failure was blocked behind the hanging revoke")
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("captured best-effort revoke was not dispatched")
	}
	if werr := invokeFail(t, service, "disposeSession", fmt.Sprintf(`{"attachment_id":%q}`, attachment["attachment_id"])); werr.Code != "NATIVE_LOCAL_FORGET_FAILED" {
		t.Fatal(werr)
	}
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	invokeOK(t, service, "disposeSession", fmt.Sprintf(`{"attachment_id":%q}`, attachment["attachment_id"]))
	if _, err := os.Stat(credential); !os.IsNotExist(err) {
		t.Fatal("cleanup retry after disposal must remove our file", err)
	}
}

func TestReaderStaleDisposeCannotEraseNewLogoutCleanupBearer(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var closeAuth string
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/render-sessions"):
			dataReply(w, 201, map[string]any{"session_id": "B-render", "version_id": "v1"})
			return true
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/close"):
			closeAuth = r.Header.Get("Authorization")
			if closeAuth != "Bearer test-owner-credential" {
				w.WriteHeader(401)
			} else {
				w.WriteHeader(204)
			}
			return true
		case r.Method == "DELETE" && r.URL.Path == "/api/v1/session":
			close(entered)
			<-release
			w.WriteHeader(204)
			return true
		}
		return false
	})
	var once sync.Once
	defer once.Do(func() { close(release) })
	a := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	b := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	invokeOK(t, service, "renderSessionOpen", `{"version_id":"v1","operation_id":"B-open"}`)
	done := make(chan *wireError, 1)
	go func() { _, werr := service.invoke("revokeOwnSession", nil); done <- werr }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("B revoke did not reach the controlled barrier")
	}
	invokeOK(t, service, "disposeSession", fmt.Sprintf(`{"attachment_id":%q}`, a["attachment_id"]))
	invokeOK(t, service, "disposeSession", fmt.Sprintf(`{"attachment_id":%q}`, b["attachment_id"]))
	if closeAuth != "Bearer test-owner-credential" {
		t.Fatal("stale A disposal erased B's authenticated cleanup", closeAuth)
	}
	once.Do(func() { close(release) })
	if werr := <-done; werr != nil {
		t.Fatal(werr)
	}
}

func TestReaderEvidenceQueriesBuildFixedRoutes(t *testing.T) {
	service, daemon, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/versions/v1/source-evidence/e/1":
			dataReply(w, 200, map[string]any{"version_id": "v1", "evidence_id": "e/1", "authored": map[string]any{"capture_status": "reference_only"}, "source": map[string]any{}, "availability": "unavailable", "bytes_origin": nil, "reason": "not_read_by_view", "service_verification": nil, "verifications_url": "/api/v1/versions/v1/source-evidence-verifications?evidence_id=e%2F1"})
			return true
		case r.Method == "GET" && r.URL.Path == "/api/v1/versions/v1/source-evidence-verifications":
			dataReply(w, 200, map[string]any{"items": []any{}, "next_cursor": nil})
			return true
		case r.Method == "GET" && r.URL.Path == "/api/v1/versions/v1/assessments":
			dataReply(w, 200, map[string]any{"items": []any{}, "next_cursor": nil, "applicable": nil})
			return true
		case r.Method == "GET" && r.URL.Path == "/api/v1/versions/v1/assessments/coverage":
			dataReply(w, 200, map[string]any{"items": []any{}, "next_cursor": nil})
			return true
		}
		return false
	})
	invokeOK(t, service, "versionEvidence", `{"version_id":"v1","evidence_id":"e/1"}`)
	invokeOK(t, service, "evidenceVerifications", `{"version_id":"v1","evidence_id":"e1","cursor":"cur-2"}`)
	invokeOK(t, service, "versionAssessments", `{"version_id":"v1","target_set_id":"t1","scope_kind":"region","page_path":"a.html","region_id":"개요","cursor":"c"}`)
	invokeOK(t, service, "assessmentCoverage", `{"version_id":"v1","coverage_set_id":"c1","cursor":"page-2"}`)
	seen := daemon.seen()
	expect := []string{
		"GET /api/v1/versions/v1/source-evidence/e%2F1",
		"GET /api/v1/versions/v1/source-evidence-verifications?cursor=cur-2&evidence_id=e1",
		"GET /api/v1/versions/v1/assessments?cursor=c&page_path=a.html&region_id=%EA%B0%9C%EC%9A%94&scope_kind=region&target_set_id=t1",
		"GET /api/v1/versions/v1/assessments/coverage?coverage_set_id=c1&cursor=page-2",
	}
	for _, want := range expect {
		found := false
		for _, request := range seen {
			if request == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing fixed route %q in %v", want, seen)
		}
	}
	if werr := invokeFail(t, service, "versionAssessments", `{"version_id":"v1","scope_kind":"chapter"}`); werr.Code != "NATIVE_PAYLOAD_INVALID" {
		t.Fatalf("invalid scope kind must be a typed payload error, got %+v", werr)
	}
	if werr := invokeFail(t, service, "assessmentCoverage", `{"version_id":"v1"}`); werr.Code != "NATIVE_PAYLOAD_INVALID" {
		t.Fatalf("coverage without a coverage set must be a typed payload error, got %+v", werr)
	}
}

func TestReaderEvidenceQueriesPreserveOpaqueIDs(t *testing.T) {
	service, daemon, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && r.URL.Path == "/api/v1/versions/v1/source-evidence-verifications" {
			dataReply(w, 200, map[string]any{"items": []any{map[string]any{"verification_id": r.URL.Query().Get("evidence_id")}}, "next_cursor": nil})
			return true
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v1/versions/v1/source-evidence/") {
			dataReply(w, 200, map[string]any{"evidence_id": strings.TrimPrefix(r.URL.Path, "/api/v1/versions/v1/source-evidence/")})
			return true
		}
		return false
	})
	for _, id := range []string{".", "..", "%2e", "evidence/상세", "a +"} {
		payload := fmt.Sprintf(`{"version_id":"v1","evidence_id":%q,"cursor":"c/2"}`, id)
		history := invokeOK(t, service, "evidenceVerifications", payload).(map[string]any)
		if history["items"].([]any)[0].(map[string]any)["verification_id"] != id {
			t.Fatal("native history changed the exact query ID", id, history)
		}
		view := invokeOK(t, service, "versionEvidence", fmt.Sprintf(`{"version_id":"v1","evidence_id":%q}`, id)).(map[string]any)
		if view["evidence_id"] != id {
			t.Fatal("native legacy evidence read changed the exact path ID", id, view)
		}
		want := "GET /api/v1/versions/v1/source-evidence-verifications?cursor=c%2F2&evidence_id=" + url.QueryEscape(id)
		found := false
		for _, request := range daemon.seen() {
			if request == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("native history did not reach the fixed alias with one encoded query ID", want, daemon.seen())
		}
	}
}

func TestReaderSuppliedMetadataStreamCarriesEtagAndExactBytes(t *testing.T) {
	body := []byte(`{"version":{"source_intent":"historical"},"sources":[],"evidence":[],"regions":[]}`)
	service, daemon, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && r.URL.Path == "/api/v1/versions/v1/metadata" && r.URL.Query().Get("kind") == "supplied" {
			sum := sha256.Sum256(body)
			w.Header().Set("ETag", `"sha256-`+hex.EncodeToString(sum[:])+`"`)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			w.Write(body)
			return true
		}
		return false
	})
	opened, ok := invokeOK(t, service, "versionSuppliedMetadata", `{"version_id":"v1"}`).(map[string]any)
	if !ok || opened["stream_id"] == "" || opened["etag"] == "" {
		t.Fatalf("metadata open must return a stream handle beside the strong ETag: %+v", opened)
	}
	etag, _ := opened["etag"].(string)
	sum := sha256.Sum256(body)
	if etag != `"sha256-`+hex.EncodeToString(sum[:])+`"` {
		t.Fatalf("returned ETag is not the object digest: %s", etag)
	}
	streamID, _ := opened["stream_id"].(string)
	var accumulated []byte
	for {
		part, ok := invokeOK(t, service, "streamRead", `{"stream_id":"`+streamID+`","max_bytes":4096}`).(map[string]any)
		if !ok {
			t.Fatalf("stream read failed: %+v", part)
		}
		if encoded, _ := part["data_b64"].(string); encoded != "" {
			raw, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Fatal(err)
			}
			accumulated = append(accumulated, raw...)
		}
		if eof, _ := part["eof"].(bool); eof {
			break
		}
	}
	if !bytes.Equal(accumulated, body) {
		t.Fatalf("streamed metadata bytes differ: %d bytes", len(accumulated))
	}
	found := false
	for _, request := range daemon.seen() {
		if request == "GET /api/v1/versions/v1/metadata?kind=supplied" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("metadata open did not use the exact fixed supplied route")
	}
}

func TestReaderSuppliedMetadataAbsentIsTypedNotFound(t *testing.T) {
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && r.URL.Path == "/api/v1/versions/v1/metadata" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(404)
			w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"absent","retryable":false,"details":{"reason":"metadata_not_supplied"}}}`))
			return true
		}
		return false
	})
	werr := invokeFail(t, service, "versionSuppliedMetadata", `{"version_id":"v1"}`)
	if werr.Status != 404 || werr.Code != "NOT_FOUND" {
		t.Fatalf("absent supplied metadata must map to the typed 404, got %+v", werr)
	}
	if werr.Details["reason"] != "metadata_not_supplied" {
		t.Fatalf("typed reason must be preserved: %+v", werr.Details)
	}
}

func TestReaderSourceConfigurationOperationsAndCheckPostOutcomes(t *testing.T) {
	for _, status := range []int{http.StatusCreated, http.StatusAccepted} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests []*http.Request
			var bodies []string
			service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/api/v1/configuration" || strings.HasPrefix(r.URL.Path, "/api/v1/configuration/plans") || r.URL.Path == "/api/v1/projects" || strings.HasSuffix(r.URL.Path, "/sources") || strings.HasSuffix(r.URL.Path, "/source-evidence-verifications") || strings.HasSuffix(r.URL.Path, "/source-observations") || strings.HasSuffix(r.URL.Path, "/assessments/coverage") {
					requests = append(requests, r.Clone(r.Context()))
					body, _ := io.ReadAll(r.Body)
					bodies = append(bodies, string(body))
					code := status
					if r.Method == "GET" {
						code = 200
					}
					dataReply(w, code, map[string]any{"operation_id": "original", "marker": r.URL.Path})
					return true
				}
				return false
			})
			cases := []struct{ operation, payload, method, path, query, body string }{
				{"configurationView", `{}`, "GET", "/api/v1/configuration", "", ""},
				{"configurationPlan", `{"request":{"scope":{"kind":"project","project_id":"p"},"expected_config_revision":2,"changes":{"sources":[]}},"operation_id":"original"}`, "POST", "/api/v1/configuration/plans", "", `{"scope":{"kind":"project","project_id":"p"},"expected_config_revision":2,"changes":{"sources":[]}}`},
				{"configurationPlanCommit", `{"plan_id":"plan","request":{"expected_config_revision":2,"confirmed":true},"operation_id":"original"}`, "POST", "/api/v1/configuration/plans/plan/commit", "", `{"expected_config_revision":2,"confirmed":true}`},
				{"projectRegister", `{"name":"Project","operation_id":"original"}`, "POST", "/api/v1/projects", "", `{"name":"Project"}`},
				{"projectSources", `{"project_id":"p","cursor":"next"}`, "GET", "/api/v1/projects/p/sources", "cursor=next", ""},
				{"verifySourceEvidence", `{"version_id":"v","evidence_id":"../evidence/상세","request":{"expected_excerpt_ref":{"upload_id":"11111111-2222-4333-8444-555555555555"}},"operation_id":"original"}`, "POST", "/api/v1/versions/v/source-evidence-verifications", "evidence_id=" + url.QueryEscape("../evidence/상세"), `{"expected_excerpt_ref":{"upload_id":"11111111-2222-4333-8444-555555555555"}}`},
				{"runSourceObservation", `{"version_id":"v","request":{"scope":{"kind":"page","page_path":"a.html"}},"operation_id":"original"}`, "POST", "/api/v1/versions/v/source-observations", "", `{"scope":{"kind":"page","page_path":"a.html"}}`},
				{"assessmentCoverage", `{"version_id":"v","coverage_set_id":"c","attempt_run_id":"11111111-2222-4333-8444-555555555555","cursor":"next"}`, "GET", "/api/v1/versions/v/assessments/coverage", "attempt_run_id=11111111-2222-4333-8444-555555555555&coverage_set_id=c&cursor=next", ""},
			}
			for _, c := range cases {
				value := invokeOK(t, service, c.operation, c.payload).(map[string]any)
				request := requests[len(requests)-1]
				if request.Method != c.method || request.URL.Path != c.path || request.URL.RawQuery != c.query || bodies[len(bodies)-1] != c.body {
					t.Fatalf("%s forwarding: %s %s %s", c.operation, request.Method, request.URL, bodies[len(bodies)-1])
				}
				if c.method == "POST" {
					if request.Header.Get("Idempotency-Key") != "original" {
						t.Fatal("original operation key lost")
					}
					kind := "committed"
					if status == 202 {
						kind = "accepted"
					}
					if value["kind"] != kind || value["body"].(map[string]any)["operation_id"] != "original" {
						t.Fatalf("lost 201/202 outcome: %#v", value)
					}
				}
			}
		})
	}
}

func TestReaderReferenceNamedCallsPreserveEncodingAndTypedErrors(t *testing.T) {
	for _, status := range []int{200, 404, 410} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				if !strings.HasPrefix(r.URL.Path, "/api/v1/versions/v1/references") {
					return false
				}
				calls++
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer test-owner-credential" {
					t.Error("reference request lost its fixed authenticated read")
				}
				if strings.HasSuffix(r.URL.Path, "/resolve") {
					expected := url.Values{"reference_id": {"ref/%2F + 한글?#"}, "page_path": {"nested/%2F + 한글.html"}, "region_id": {"영역/%2F +"}}
					if !reflect.DeepEqual(r.URL.Query(), expected) {
						t.Error("reference fields decoded twice or lost", r.URL.Query())
					}
				} else if r.URL.Query().Get("cursor") != "opaque/+%" || r.URL.Query().Get("limit") != "0" {
					t.Error("list cursor/limit changed", r.URL.Query())
				}
				if status == 200 {
					dataReply(w, 200, map[string]any{"id": "reference-result"})
				} else {
					details := map[string]any{}
					code := "NOT_FOUND"
					if status == 410 {
						details = map[string]any{"document_id": "d1", "version_id": "v1"}
						code = "TARGET_GONE"
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "retryable": false, "details": details}})
				}
				return true
			})
			for operation, payload := range map[string]string{"versionReferences": `{"version_id":"v1","cursor":"opaque/+%","limit":0}`, "resolveReference": `{"version_id":"v1","reference_id":"ref/%2F + 한글?#","page_path":"nested/%2F + 한글.html","region_id":"영역/%2F +"}`} {
				if status == 200 {
					value := invokeOK(t, service, operation, payload).(map[string]any)
					if value["id"] != "reference-result" {
						t.Fatal(value)
					}
				} else {
					err := invokeFail(t, service, operation, payload)
					if err.Status != status || (status == 404 && len(err.Details) != 0) || (status == 410 && !reflect.DeepEqual(err.Details, map[string]any{"document_id": "d1", "version_id": "v1"})) {
						t.Fatal("reference error details changed", err)
					}
				}
			}
			if calls != 2 {
				t.Fatal("missing named reference call", calls)
			}
		})
	}
}

func TestReaderReferencePayloadAndDetachedAuthorityAreDenied(t *testing.T) {
	service, daemon, _ := newReaderFixture(t, nil)
	before := len(daemon.seen())
	for operation, payloads := range map[string][]string{
		"versionReferences": {`{"version_id":"v1","url":"/api/v1/configuration"}`, `{"version_id":null}`, `{"version_id":"v1","limit":"10"}`, `{"version_id":"v1","cursor":null}`, `{}`, `null`},
		"resolveReference":  {`{"version_id":"v1","reference_id":"r","target":"/settings"}`, `{"version_id":"v1"}`, `{"version_id":"v1","reference_id":3}`, `{"version_id":"v1","reference_id":"r","page_path":null}`},
	} {
		for _, payload := range payloads {
			if err := invokeFail(t, service, operation, payload); err.Status != 400 || err.Code != "NATIVE_PAYLOAD_INVALID" {
				t.Fatal("open reference payload admitted", err)
			}
		}
	}
	if len(daemon.seen()) != before {
		t.Fatal("invalid native payload reached HTTP")
	}
	auth, err := service.authority()
	if err != nil {
		t.Fatal(err)
	}
	if failure := service.dispose(auth.attachmentID); failure != nil {
		t.Fatal(failure)
	}
	before = len(daemon.seen())
	for _, operation := range []string{"versionReferences", "resolveReference"} {
		if failure := invokeFail(t, service, operation, `{"version_id":"v1","reference_id":"r"}`); failure.Status != 401 {
			t.Fatal("detached reference call admitted", failure)
		}
	}
	if len(daemon.seen()) != before {
		t.Fatal("detached reference request reached HTTP")
	}
}

func TestReaderReferenceCancellationDiscardsAnAttachmentRead(t *testing.T) {
	for _, operation := range []string{"versionReferences", "resolveReference"} {
		t.Run(operation, func(t *testing.T) {
			entered, cancelled := make(chan struct{}), make(chan struct{})
			service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				if !strings.HasPrefix(r.URL.Path, "/api/v1/versions/v1/references") {
					return false
				}
				close(entered)
				<-r.Context().Done()
				close(cancelled)
				return true
			})
			t.Cleanup(service.CloseOwned)
			done := make(chan *wireError, 1)
			payload := `{"version_id":"v1"}`
			if operation == "resolveReference" {
				payload = `{"version_id":"v1","reference_id":"r"}`
			}
			go func() { _, err := service.invoke(operation, json.RawMessage(payload)); done <- err }()

			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("reference request never entered")
			}
			auth, failure := service.authority()
			if failure != nil {
				t.Fatal(failure)
			}
			if failure = service.dispose(auth.attachmentID); failure != nil {
				t.Fatal(failure)
			}
			select {
			case <-cancelled:
			case <-time.After(10 * time.Second):
				t.Fatal("attachment disposal did not cancel reference HTTP")
			}
			select {
			case err := <-done:
				if err == nil || err.Code != "NATIVE_SESSION_REPLACED" {
					t.Fatal("stale reference result escaped", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("cancelled reference call did not return")
			}
		})
	}
}
