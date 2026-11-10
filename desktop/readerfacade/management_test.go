package readerfacade

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func TestT00804ManagementUnavailableAndLegacyDTOSurviveNamedReads(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555555"
	unavailable := map[string]any{"availability": "unavailable", "overlay_id": "opaque-overlay", "kind": "related_explanation",
		"from":   map[string]any{"document_id": id, "version_id": id, "page_path": "page.html", "region_id": nil},
		"origin": "ai", "decision": "accepted", "revision": 2, "etag": "unavailable"}
	legacy := map[string]any{"availability": "available", "kind": "topic_assignment", "evidence_basis": "legacy_unknown"}
	pages := map[string]any{
		"versionRelated":       map[string]any{"items": []any{unavailable}, "next_cursor": nil},
		"topicAssignmentsList": map[string]any{"items": []any{legacy}, "next_cursor": nil},
	}
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		operation := "versionRelated"
		if r.URL.Path == "/api/v1/topic-assignments" {
			operation = "topicAssignmentsList"
		} else if r.URL.Path != "/api/v1/versions/"+id+"/related" {
			return false
		}
		dataReply(w, 200, pages[operation])
		return true
	})
	for operation, want := range pages {
		got, err := service.invoke(operation, json.RawMessage(`{"version_id":"`+id+`"}`))
		if err != nil {
			t.Fatal(operation, err)
		}
		actual, e := json.Marshal(got)
		if e != nil {
			t.Fatal(e)
		}
		expected, e := json.Marshal(want)
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(actual, expected) {
			t.Fatalf("%s changed a closed DTO: %s", operation, actual)
		}
	}
}

func TestT00804ManagementClosedReadsPassiveAndValidation(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555555"
	calls := 0
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v1/versions/"+id+"/related" && r.URL.Path != "/api/v1/topic-assignments" && r.URL.Path != "/api/v1/jobs" {
			return false
		}
		calls++
		if r.Header.Get("Authorization") != "Bearer test-owner-credential" || r.Method != "GET" || r.Header.Get("Haeryong-Passive-Read") != "1" {
			t.Error("management read authority/passive mismatch")
		}
		dataReply(w, 200, map[string]any{"items": []any{}, "next_cursor": nil})
		return true
	})
	for operation, payload := range map[string]string{
		"jobsList":             `{"passive":true}`,
		"versionRelated":       `{"version_id":"` + id + `","page_path":"nested/검토 %2F #?.html","region_id":"main","limit":1,"passive":true}`,
		"topicAssignmentsList": `{"version_id":"` + id + `","page_path":"other.html","region_id":"main","passive":true}`,
	} {
		if _, e := service.invoke(operation, json.RawMessage(payload)); e != nil {
			t.Fatal(operation, e)
		}
	}
	if calls != 3 {
		t.Fatal("missing current named reads", calls)
	}
	for _, c := range []struct{ op, body string }{
		{"jobsList", `{"url":"http://wrong/"}`},
		{"versionRelated", `{}`},
		{"versionRelated", `{"version_id":"` + id + `","document_id":"` + id + `"}`},
		{"topicAssignmentsList", `{"region_id":"main"}`},
		{"topicAssignmentsList", `{"version_id":"` + id + `","limit":0}`},
		{"topicAssignmentsList", `{"version_id":"` + id + `","limit":1,"limit":2}`},
	} {
		if _, e := service.invoke(c.op, json.RawMessage(c.body)); e == nil || e.Code != "NATIVE_PAYLOAD_INVALID" {
			t.Fatal("invalid management payload admitted", c, e)
		}
	}
	if calls != 3 {
		t.Fatal("invalid input dispatched")
	}
}
