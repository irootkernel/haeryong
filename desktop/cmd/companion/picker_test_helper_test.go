//go:build haeryong_test_state

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	facade "haeryong.local/haeryong/desktop/readerfacade"
)

// Synthetic session responses establish only the unit fixture's attachment.
// Picker assertions then exercise the real closed Invoke dispatch.
func newTestOnlyService(t *testing.T) *facade.ReaderService {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/session" && r.URL.Path != "/api/v1/capabilities" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	t.Cleanup(server.Close)
	service := facade.NewReaderService(server.URL, t.TempDir(), "unit-fixture-token")
	t.Cleanup(service.CloseOwned)
	if result := pickerTestCall(t, service, "attachSession", `{}`); result["ok"] != true {
		t.Fatalf("unit attachment failed: %v", result)
	}
	return service
}

func pickerTestCall(t *testing.T, service *facade.ReaderService, operation, payload string) map[string]any {
	t.Helper()
	result, err := service.Invoke(struct {
		Operation string          `json:"Operation"`
		Payload   json.RawMessage `json:"Payload"`
	}{operation, json.RawMessage(payload)})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
