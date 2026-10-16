package imports

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestActualHTTPResponseLossReplaysOriginalImportWithoutDuplicatePublication(t *testing.T) {
	f := newHTTPFixture(t)
	id := f.upload("directory", "response loss")
	status, _, data := f.put(id, "readme.html", "file", []byte("<html>saved</html>"), 0, true)
	requireStatus(t, status, 200, data)
	f.seal(id)
	key := operation(t)
	body := map[string]any{"upload_id": id, "document_id": nil}
	raw, _ := json.Marshal(body)
	f.dropOperation.Store(&key)
	r, err := http.NewRequest("POST", f.server.URL+"/api/v1/imports", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Origin", f.server.URL)
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", key)
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport}).Do(r)
	if response != nil {
		response.Body.Close()
	}
	if err == nil {
		t.Fatal("owned response-loss injector did not interrupt acknowledgement")
	}
	status, data = f.json("POST", "/api/v1/imports", key, body)
	requireStatus(t, status, 202, data)
	job := valueAs[struct {
		ID string `json:"job_id"`
	}](t, data)
	completed := waitJob(t, f, job.ID, "completed")
	status, replay := f.json("POST", "/api/v1/imports", key, body)
	requireStatus(t, status, 202, replay)
	again := valueAs[struct {
		ID string `json:"job_id"`
	}](t, replay)
	if again.ID != job.ID {
		t.Fatal("response loss dispatched another import")
	}
	var result struct {
		VersionID  string `json:"version_id"`
		DocumentID string `json:"document_id"`
	}
	if json.Unmarshal(completed.Result, &result) != nil || result.VersionID == "" {
		t.Fatal("job result missing published identity")
	}
	version, err := f.store.Version(context.Background(), result.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	document, err := f.store.Document(context.Background(), result.DocumentID)
	if err != nil || document.LatestVersionID != version.ID {
		t.Fatal("latest pointer did not match complete publication")
	}
	health, err := f.store.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	after := int64(0)
	published := 0
	for {
		events, err := f.store.Events(context.Background(), health.Library.StreamEpoch, after, 64)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 0 {
			break
		}
		for _, event := range events {
			if event.Kind == "version.published" && event.ResourceID == document.ID {
				published++
			}
			after = event.Sequence
		}
	}
	if published != 1 {
		t.Fatalf("publication event count=%d", published)
	}
	body["version_id"] = operation(t)
	status, data = f.json("POST", "/api/v1/imports", key, body)
	requireStatus(t, status, 409, data)
	f.restart()
	f.verify(version, map[string]string{"readme.html": "<html>saved</html>"}, nil)
	status, _, data = f.request("GET", "/api/v1/operations/"+key, nil, nil)
	requireStatus(t, status, 200, data)
}
