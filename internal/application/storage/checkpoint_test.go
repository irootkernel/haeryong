package storage

import (
	"encoding/json"
	"haeryong.local/haeryong/internal/domain"
	"strings"
	"testing"
)

func TestCheckpointExactFourTypedFieldsAndUnboundedDecimal(t *testing.T) {
	c := domain.Checkpoint{Generation: 1, InventorySHA256: strings.Repeat("a", 64), AcceptedBytes: strings.Repeat("9", 100), Complete: false}
	data, _ := json.Marshal(c)
	parsed, err := ParseCheckpoint(data)
	if err != nil || parsed != c {
		t.Fatalf("valid decimal/checkpoint: %v", err)
	}
	for _, bad := range []string{
		strings.Replace(string(data), "complete_input", "complete", 1),
		strings.Replace(string(data), `"generation":1`, `"generation":0`, 1),
		strings.Replace(string(data), `"generation":1`, `"generation":null`, 1),
		strings.Replace(string(data), `"complete_input":false`, `"complete_input":null`, 1),
		strings.Replace(string(data), `"complete_input":false`, `"complete_input":"false"`, 1),
		strings.Replace(string(data), `"generation":1`, `"generation":1,"generation":2`, 1),
		strings.Replace(string(data), `"accepted_bytes":"`+c.AcceptedBytes+`"`, `"accepted_bytes":"01"`, 1),
		string(data) + ` {}`,
	} {
		if _, err := ParseCheckpoint([]byte(bad)); err == nil {
			t.Errorf("accepted invalid checkpoint: %s", bad)
		}
	}
}
func TestClosedPurposeTargetsAndNullableBinding(t *testing.T) {
	uuid := "00000000-0000-4000-8000-000000000001"
	result := []byte(`{"project_id":"` + uuid + `","document_id":null}`)
	normalized, err := NormalizeTarget("result", result)
	if err != nil || !strings.Contains(string(normalized), `"draft_id":null`) {
		t.Fatalf("result omitted default: %s %v", normalized, err)
	}
	variants := map[string]string{
		"context":  `{"version_id":"` + uuid + `","page_path":null,"render_instance_id":null}`,
		"body":     `{"operation_id":"` + uuid + `","action":"evidence_verification","version_id":"` + uuid + `","evidence_id":"Unicode 任意"}`,
		"metadata": `{"operation_id":"` + uuid + `","action":"draft_replace","draft_id":"` + uuid + `"}`,
	}
	for purpose, data := range variants {
		if _, err := NormalizeTarget(purpose, []byte(data)); err != nil {
			t.Errorf("valid %s target: %v", purpose, err)
		}
	}
	for _, bad := range []struct{ purpose, data string }{
		{"result", `{"project_id":"` + uuid + `"}`},
		{"context", `{"version_id":"` + uuid + `","page_path":null}`},
		{"context", `{"version_id":"` + uuid + `","page_path":null,"render_instance_id":null,"extra":1}`},
		{"result", `{"project_id":"` + uuid + `","project_id":"` + uuid + `","document_id":null}`},
		{"body", `{"operation_id":"` + uuid + `","action":"unknown","version_id":"` + uuid + `","page_path":null}`},
	} {
		if _, err := NormalizeTarget(bad.purpose, []byte(bad.data)); err == nil {
			t.Errorf("accepted incomplete/changed target: %s", bad.data)
		}
	}
}
func TestFingerprintCanonicalBodyPreconditionsAndOriginalOmissions(t *testing.T) {
	r := domain.Request{Key: domain.OperationKey{PrincipalID: "owner", OperationID: "00000000-0000-4000-8000-000000000001"}, Method: "PUT", Target: "/exact/target", Body: []byte(`{"a":1,"b":"exact"}`)}
	first, err := Fingerprint(r, []byte(`{"generated":"once"}`))
	if err != nil {
		t.Fatal(err)
	}
	r.Body = []byte("{ \"b\":\"exact\", \"a\":1 }")
	r.Preconditions = map[string]string{}
	same, err := Fingerprint(r, []byte(`{"generated":"once"}`))
	if err != nil || same != first {
		t.Fatal("transport ordering/absence changed semantic fingerprint")
	}
	r.Omitted = []string{"base_version_id"}
	changed, err := Fingerprint(r, []byte(`{"generated":"once"}`))
	if err != nil || changed == first {
		t.Fatal("omission marker lost")
	}
	r.Key.OperationID = `"00000000-0000-4000-8000-000000000001"`
	if _, err := Fingerprint(r, nil); err == nil {
		t.Fatal("quoted operation ID accepted")
	}
}
