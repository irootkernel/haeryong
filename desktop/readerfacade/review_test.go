package readerfacade

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestReviewNamedOperationsPreserveIntentPreconditionsAndListCursor(t *testing.T) {
	for _, filter := range []string{"reviewing", "updated_since_review"} {
		t.Run(filter, func(t *testing.T) {
			count := 0
			s, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/api/v1/documents" && r.URL.Path != "/api/v1/versions/v3/review" && r.URL.Path != "/api/v1/versions/v3/review/history" {
					return false
				}
				count++
				if r.URL.Path == "/api/v1/documents" {
					if r.URL.Query().Get("review") != filter || r.URL.Query().Get("cursor") != "next/+%" {
						t.Error("N3 filter/cursor lost", r.URL.RawQuery)
					}
				}
				if r.Method == "PUT" {
					if r.Header.Get("If-Match") != `"original"` || r.Header.Get("Idempotency-Key") != "same-key" {
						t.Error("Review original controls lost")
					}
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !reflect.DeepEqual(body, map[string]string{"action": "complete", "state": "reviewed"}) {
						t.Error("Review intent lost", body)
					}
				}
				if r.URL.Path == "/api/v1/versions/v3/review/history" && r.URL.Query().Get("cursor") != "history/+%" {
					t.Error("history cursor lost")
				}
				dataReply(w, 200, map[string]any{"version_id": "v3", "items": []any{}, "next_cursor": nil})
				return true
			})
			payload, _ := json.Marshal(map[string]any{"review": filter, "cursor": "next/+%"})
			if _, e := s.invoke("listDocuments", payload); e != nil {
				t.Fatal(e)
			}
			invokeOK(t, s, "reviewGet", `{"version_id":"v3"}`)
			invokeOK(t, s, "reviewPut", `{"version_id":"v3","body":{"action":"complete","state":"reviewed"},"precondition":{"if_match":"\"original\""},"operation_id":"same-key"}`)
			invokeOK(t, s, "reviewHistory", `{"version_id":"v3","cursor":"history/+%"}`)
			if count != 4 {
				t.Fatal("missing operation", count)
			}
			if _, e := s.invoke("reviewPut", json.RawMessage(`{"version_id":"v3","body":{},"operation_id":"key","precondition":{"if_match":"etag","create":true}}`)); e == nil {
				t.Fatal("ambiguous condition admitted")
			}
		})
	}
}
