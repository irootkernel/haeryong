package readerfacade

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestLifecycleTransportParityConfirmationAndOptionalKey(t *testing.T) {
	var calls []string
	s, _, _ := newReaderFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.Contains(r.URL.Path, "lifecycle-plans") && r.URL.Path != "/api/v1/trash" {
			return false
		}
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		if r.Method == "POST" {
			var body map[string]any
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Fatal(e)
			}
			if strings.HasSuffix(r.URL.Path, "/commit") {
				if body["plan_id"] != "plan" {
					t.Error("plan identity lost")
				}
				status, code := 200, ""
				if body["confirmed"] != true {
					status, code = 400, "INVALID_REQUEST"
				} else if body["expected_revision"] != float64(2) {
					status, code = 409, "OPERATION_CONFLICT"
				}
				if status != 200 {
					w.WriteHeader(status)
					fmt.Fprintf(w, `{"error":{"code":%q,"retryable":false}}`, code)
					return true
				}
			}
		}
		dataReply(w, 200, map[string]any{"plan_id": "plan", "items": []any{}, "next_cursor": nil})
		return true
	})
	invokeOK(t, s, "lifecyclePlan", `{"document_id":"doc","body":{"operation":"trash","expected_revision":2}}`)
	invokeOK(t, s, "lifecyclePlanGet", `{"plan_id":"plan","request":{"cursor":"next/+","limit":1}}`)
	invokeOK(t, s, "trashList", `{"request":{"project_id":"project/+","cursor":"next/+","limit":2}}`)
	for _, key := range []string{"", `,"operation_id":"alias"`} {
		for _, confirmed := range []string{`,"confirmed":false`, `,"confirmed":"true"`, ""} {
			e := invokeFail(t, s, "lifecyclePlanCommit", `{"plan_id":"plan","body":{"plan_id":"plan","expected_revision":2`+confirmed+`}`+key+`}`)
			if e.Status != 400 || e.Code != "INVALID_REQUEST" {
				t.Fatal(e)
			}
		}
		e := invokeFail(t, s, "lifecyclePlanCommit", `{"plan_id":"plan","body":{"plan_id":"plan","expected_revision":3,"confirmed":true}`+key+`}`)
		if e.Status != 409 || e.Code != "OPERATION_CONFLICT" {
			t.Fatal(e)
		}
		invokeOK(t, s, "lifecyclePlanCommit", `{"plan_id":"plan","body":{"plan_id":"plan","expected_revision":2,"confirmed":true}`+key+`}`)
	}
	if len(calls) != 13 {
		t.Fatal(calls)
	}
}
