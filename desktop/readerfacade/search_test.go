package readerfacade

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

func TestSearchOriginalRequestAndSafeReasonParity(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		name := "owner"
		if scoped {
			name = "scoped"
		}
		t.Run(name, func(t *testing.T) {
			reasons := []any{map[string]any{"code": "read_failed", "version_count": "1"}}
			if !scoped {
				reasons = append(reasons, map[string]any{"code": "storage_failed", "version_count": "1"})
			}
			// Seeded transport projection, distinct from real server eligibility SC14.
			page := map[string]any{"items": []any{map[string]any{"document_id": "d", "version_id": "old", "page_path": "nested/검토 %2F #?.html", "region_id": "retry", "anchor_state": "ambiguous"}}, "next_cursor": "opaque/+%", "index": map[string]any{"state": "partial", "reasons": reasons}}
			expected := url.Values{"q": {`검토 "source/path.go" %2F`}, "project_id": {"A"}, "lifecycle": {"all"}, "versions": {"all"}, "kind": {"region"}, "cursor": {"opaque/+%"}, "limit": {"17"}}
			calls := 0
			service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/api/v1/search" {
					return false
				}
				calls++
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer test-owner-credential" || !reflect.DeepEqual(r.URL.Query(), expected) {
					t.Errorf("search did not preserve original request: %s %s", r.Method, r.URL.RequestURI())
				}
				dataReply(w, 200, page)
				return true
			})
			payload, _ := json.Marshal(map[string]any{"q": expected.Get("q"), "project_id": "A", "lifecycle": "all", "versions": "all", "kind": "region", "cursor": "opaque/+%", "limit": 17})
			got, werr := service.invoke("search", payload)
			if werr != nil || !reflect.DeepEqual(got, page) || calls != 1 {
				t.Fatalf("search reasons/identity parity failed: %v %v calls=%d", got, werr, calls)
			}
			// Empty submitted query must survive the ordinary empty-value collection helper.
			expected = url.Values{"q": {""}}
			got, werr = service.invoke("search", json.RawMessage(`{"q":""}`))
			if werr != nil || !reflect.DeepEqual(got, page) || calls != 2 {
				t.Fatalf("empty search query lost: %v", werr)
			}
			expected = url.Values{"q": {""}, "project_id": {""}, "lifecycle": {""}, "versions": {""}, "kind": {""}, "cursor": {""}, "limit": {"0"}}
			_, werr = service.invoke("search", json.RawMessage(`{"q":"","project_id":"","lifecycle":"","versions":"","kind":"","cursor":"","limit":0}`))
			if werr != nil || calls != 3 {
				t.Fatal("explicit empty/zero search controls were lost", werr)
			}
			_, werr = service.invoke("search", json.RawMessage(`{"q":3}`))
			if werr == nil || werr.Code != "NATIVE_PAYLOAD_INVALID" {
				t.Fatal("invalid search argument admitted")
			}
		})
	}
}

func TestSearchEventHintsFollowAuthorizedCursorWithoutResourceBodies(t *testing.T) {
	polls := 0
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v1/events" {
			return false
		}
		polls++
		if polls > 1 && (r.URL.Query().Get("stream_epoch") != "epoch" || r.URL.Query().Get("after") != "1") {
			t.Error("event cursor not preserved", r.URL.RawQuery)
		}
		items := []any{}
		cursor := "epoch:1"
		if polls > 1 {
			items = append(items, map[string]any{"type": "search.index_updated", "resource": map[string]any{"id": "private-resource"}})
			cursor = "epoch:2"
		}
		dataReply(w, 200, map[string]any{"items": items, "next_cursor": cursor, "resync": nil})
		return true
	})
	opened := invokeOK(t, service, "subscribeEvents", "{}").(map[string]any)
	payload, _ := json.Marshal(map[string]any{"subscription_id": opened["subscription_id"]})
	got := invokeOK(t, service, "eventsPoll", string(payload)).(map[string]any)
	if !reflect.DeepEqual(got, map[string]any{"closed": false, "invalidated": true, "cursor": "epoch:2"}) {
		t.Fatal("expected only content-free hint", got)
	}
}
