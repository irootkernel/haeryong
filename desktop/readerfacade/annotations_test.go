package readerfacade

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAnnotationRevisitRequestParityAndSavedContext(t *testing.T) {
	var denied atomic.Bool
	var requests []string
	s, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasPrefix(r.URL.Path, "/api/v1/annotations/") || r.URL.Path == "/api/v1/versions/v1/annotations" || r.URL.Path == "/api/v1/documents/d/revisit" || r.URL.Path == "/api/v1/documents" {
			requests = append(requests, r.Method+" "+r.URL.RequestURI())
			if r.URL.Path == "/api/v1/annotations/n" && denied.Load() {
				w.WriteHeader(403)
				fmt.Fprint(w, `{"error":{"code":"FORBIDDEN","retryable":false}}`)
				return true
			}
			if strings.HasSuffix(r.URL.Path, "/context") || strings.HasSuffix(r.URL.Path, "/body") {
				if r.URL.Query().Get("revision") != "2" {
					t.Error("saved revision lost")
				}
				w.Header().Set("Content-Type", "text/plain")
				w.Header().Set("Content-Length", "3")
				w.Header().Set("ETag", `"sha256-`+strings.Repeat("a", 64)+`"`)
				fmt.Fprint(w, "abc")
				return true
			}
			if r.Method == "PATCH" || r.Method == "DELETE" {
				if r.Header.Get("If-Match") != `"individual"` || r.Header.Get("Idempotency-Key") != "same-key" {
					t.Error("note condition/key lost")
				}
			}
			if r.Method == "PATCH" {
				var v map[string]any
				if err := json.NewDecoder(r.Body).Decode(&v); err != nil || v["action"] != "reattach" {
					t.Error("reattach discriminator lost")
				}
			}
			if r.Method == "PUT" {
				if r.Header.Get("If-None-Match") != "*" {
					t.Error("Revisit create control lost")
				}
			}
			if r.URL.Path == "/api/v1/documents" && (r.URL.Query().Get("revisit") != "true" || r.URL.Query().Get("cursor") != "next/+%") {
				t.Error("Revisit filter/cursor lost")
			}
			dataReply(w, 200, map[string]any{"annotation_id": "n", "document_id": "d", "items": []any{}, "next_cursor": nil})
			return true
		}
		return false
	})
	for _, call := range []struct{ op, payload string }{
		{"annotationCreate", `{"version_id":"v1","body":{"page_path":"pages/한글.html","region_id":"retry","page_query":"q=+%2F","page_fragment":"part"},"operation_id":"create-key"}`},
		{"annotationList", `{"version_id":"v1","cursor":"next/+%"}`},
		{"annotationGet", `{"annotation_id":"n"}`},
		{"annotationHistory", `{"annotation_id":"n","cursor":"history/+%"}`},
		{"annotationPatch", `{"annotation_id":"n","body":{"action":"reattach","anchor":{"version_id":"v2","page_path":null,"region_id":null,"page_query":"","page_fragment":""}},"etag":"\"individual\"","operation_id":"same-key"}`},
		{"annotationDelete", `{"annotation_id":"n","etag":"\"individual\"","operation_id":"same-key"}`},
		{"revisitGet", `{"document_id":"d"}`},
		{"revisitPut", `{"document_id":"d","flag":true,"precondition":{"create":true},"operation_id":"flag-key"}`},
		{"listDocuments", `{"revisit":"true","cursor":"next/+%"}`},
	} {
		invokeOK(t, s, call.op, call.payload)
	}
	for _, op := range []string{"annotationBodyStream", "annotationContextStream"} {
		opened := invokeOK(t, s, op, `{"annotation_id":"n","revision":2}`).(map[string]any)
		if opened["byte_size"] != "3" || opened["media_type"] != "text/plain" {
			t.Fatal("stream metadata lost", opened)
		}
		id := opened["stream_id"].(string)
		part := invokeOK(t, s, "streamRead", fmt.Sprintf(`{"stream_id":%q,"max_bytes":65536}`, id)).(map[string]any)
		data, err := base64.StdEncoding.DecodeString(part["data_b64"].(string))
		if err != nil || string(data) != "abc" {
			t.Fatal("exact saved context lost", part)
		}
		invokeOK(t, s, "streamCancel", fmt.Sprintf(`{"stream_id":%q}`, id))
	}
	opened := invokeOK(t, s, "annotationContextStream", `{"annotation_id":"n","revision":2}`).(map[string]any)
	denied.Store(true)
	failure := invokeFail(t, s, "streamRead", fmt.Sprintf(`{"stream_id":%q,"max_bytes":65536}`, opened["stream_id"]))
	if failure.Status != 403 {
		t.Fatal("saved context authority loss ignored", failure)
	}
	if len(requests) < 11 {
		t.Fatal("missing requests", requests)
	}
	if _, e := s.invoke("annotationContextStream", json.RawMessage(`{"annotation_id":"n","revision":2,"context_ref":"foreign"}`)); e == nil {
		t.Fatal("arbitrary context selector accepted")
	}
}
