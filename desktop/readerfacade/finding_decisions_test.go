package readerfacade

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestT00805FindingMutationMethodsAndF4Error(t *testing.T) {
	const op = "11111111-1111-4111-8111-111111111111"
	var method, path, tag, key string
	var body map[string]any
	undone := false
	service, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v1/findings/fnd-opaque-01/decisions" && r.URL.Path != "/api/v1/topic-assignments/topic-one" && r.URL.Path != "/api/v1/overlays/topic-one/undo" {
			return false
		}
		method, path, tag, key = r.Method, r.URL.Path, r.Header.Get("If-Match"), r.Header.Get("Idempotency-Key")
		if r.Header.Get("Haeryong-Passive-Read") != "" {
			t.Fatal("mutation was sent as passive read")
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if undone && r.Method == "PUT" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "INVALID_TRANSITION", "retryable": false, "details": map[string]any{"reason": "undone_requires_reassessment_approve"}}})
			return true
		}
		dataReply(w, 200, map[string]any{"operation_id": op, "state": "completed", "finding": nil, "effect": nil, "job_id": nil})
		return true
	})
	invokeOK(t, service, "findingDecision", fmt.Sprintf(`{"finding_id":"fnd-opaque-01","decision":"reject","etag":"\"finding-fnd-opaque-01-r1\"","operation_id":%q}`, op))
	if method != "POST" || path != "/api/v1/findings/fnd-opaque-01/decisions" || key != op || tag != "\"finding-fnd-opaque-01-r1\"" {
		t.Fatalf("wrong finding request: %s %s %s %s", method, path, tag, key)
	}
	invokeOK(t, service, "topicAssignmentSet", fmt.Sprintf(`{"assignment_id":"topic-one","pinned":true,"decision":"accepted","etag":"\"overlay-topic-one-r1\"","operation_id":%q}`, op))
	if method != "PUT" || path != "/api/v1/topic-assignments/topic-one" || body["pinned"] != true {
		t.Fatalf("Topic PUT contract not preserved: %s %s %#v", method, path, body)
	}
	invokeOK(t, service, "overlayUndo", fmt.Sprintf(`{"overlay_id":"topic-one","etag":"\"overlay-topic-one-r1\"","operation_id":%q}`, op))
	if method != "POST" || path != "/api/v1/overlays/topic-one/undo" {
		t.Fatal("wrong undo method/path")
	}
	undone = true
	e := invokeFail(t, service, "topicAssignmentSet", fmt.Sprintf(`{"assignment_id":"topic-one","pinned":false,"decision":"accepted","etag":"\"overlay-topic-one-r2\"","operation_id":%q}`, op))
	if e.Status != 409 || e.Code != "INVALID_TRANSITION" || e.Details["reason"] != "undone_requires_reassessment_approve" {
		t.Fatalf("F4 error changed: %#v", e)
	}
}
