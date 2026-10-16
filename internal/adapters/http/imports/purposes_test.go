package imports

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	importer "haeryong.local/haeryong/internal/application/import"
)

func TestActualHTTPOpaquePurposeTransportAndSealedValidation(t *testing.T) {
	f := newHTTPFixture(t)
	result := f.upload("file", "selected.html")
	status, _, data := f.put(result, "_input", "file", []byte("<html>version</html>"), 0, true)
	requireStatus(t, status, 200, data)
	f.seal(result)
	status, data = f.json("POST", "/api/v1/packages/validate", "", map[string]any{"upload_id": result})
	requireStatus(t, status, 200, data)
	status, data = f.json("POST", "/api/v1/packages/validate", "quoted", map[string]any{"upload_id": result})
	requireStatus(t, status, 400, data)
	version := f.publish(result, operation(t))
	for _, purpose := range []string{"context", "body", "metadata"} {
		t.Run(purpose, func(t *testing.T) {
			var target any
			switch purpose {
			case "context":
				target = map[string]any{"version_id": version.ID, "page_path": nil, "render_instance_id": nil}
			case "body":
				target = map[string]any{"operation_id": operation(t), "action": "question", "version_id": version.ID, "page_path": nil}
			case "metadata":
				target = map[string]any{"operation_id": operation(t), "action": "import", "project_id": f.project, "document_id": nil}
			}
			status, data := f.json("POST", "/api/v1/uploads", operation(t), map[string]any{"purpose": purpose, "input_mode": "object", "target": target})
			requireStatus(t, status, 201, data)
			created := valueAs[struct {
				ID    string `json:"upload_id"`
				Entry string `json:"transport_entry"`
			}](t, data)
			if created.Entry == "" {
				t.Fatal("service did not choose transport label")
			}
			payload := []byte(`{"state":"opaque"}`)
			if purpose == "context" {
				payload = append([]byte(`{"state":"`), bytes.Repeat([]byte{'x'}, 2<<20)...)
				payload = append(payload, []byte(`"}`)...)
			}
			status, _, data = f.put(created.ID, created.Entry, "file", payload, 0, true)
			requireStatus(t, status, 200, data)
			detail := f.seal(created.ID)
			digest := sha256.Sum256(payload)
			if detail.ObjectRef == nil || detail.ObjectRef.ID != created.ID || detail.ObjectRef.Hash != hex.EncodeToString(digest[:]) || detail.ObjectRef.Size != detail.Checkpoint.AcceptedBytes || !detail.Reusable {
				t.Fatal("sealed opaque reference did not identify exact reusable bytes")
			}
			status, _, data = f.request("GET", "/api/v1/uploads/"+created.ID, nil, nil)
			requireStatus(t, status, 200, data)
			var wire map[string]any
			wire = valueAs[map[string]any](t, data)
			reference, ok := wire["sealed_object_ref"].(map[string]any)
			if !ok || len(reference) != 4 {
				t.Fatal("object reference wire is not canonical typed object")
			}
			status, data = f.json("POST", "/api/v1/imports", operation(t), map[string]any{"upload_id": created.ID, "document_id": nil})
			requireStatus(t, status, 400, data)
			status, _, data = f.put(created.ID, "retargeted", "file", []byte("different"), 0, true)
			requireStatus(t, status, 409, data)
		})
	}
}

func TestHTTPListsUseSummaryAndCanonicalRangePinEnvelopes(t *testing.T) {
	f := newHTTPFixture(t)
	id := f.upload("directory", "list schemas")
	status, _, data := f.put(id, "exact", "file", []byte("bytes"), 0, true)
	requireStatus(t, status, 200, data)
	status, _, data = f.request("GET", "/api/v1/uploads", nil, nil)
	requireStatus(t, status, 200, data)
	list := valueAs[importer.Page[map[string]any]](t, data)
	if len(list.Items) != 1 {
		t.Fatal("upload list lost item")
	}
	counts, ok := list.Items[0]["pin_summary"].(map[string]any)
	if !ok || counts["staging"] != float64(1) {
		t.Fatal("compact pin summary did not match actual retained upload pin")
	}
	for _, key := range []string{"checkpoint", "error", "ranges_url", "entries_url"} {
		if _, ok := list.Items[0][key]; ok {
			t.Fatalf("detail-only field leaked into summary: %s", key)
		}
	}
	for _, resource := range []string{"ranges", "pins"} {
		status, _, data = f.request("GET", "/api/v1/uploads/"+id+"/"+resource, nil, nil)
		requireStatus(t, status, 200, data)
		page := valueAs[importer.Page[map[string]any]](t, data)
		if len(page.Items) != 1 {
			t.Fatalf("%s count=%d", resource, len(page.Items))
		}
		for key := range page.Items[0] {
			if key != strings.ToLower(key) {
				t.Fatal("generic domain field leaked into wire")
			}
		}
		if resource == "ranges" && (page.Items[0]["offset"] != "0" || page.Items[0]["byte_size"] != "5") {
			t.Fatal("range byte counts are not exact decimal strings")
		}
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil {
		t.Fatal("invalid response JSON")
	}
}
