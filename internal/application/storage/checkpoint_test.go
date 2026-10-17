package storage

import (
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/domain"
	"strings"
)

var _ = It("TestCheckpointExactFourTypedFieldsAndUnboundedDecimal", Label("unit"), func() {

	c := domain.Checkpoint{Generation: 1, InventorySHA256: strings.Repeat("a", 64), AcceptedBytes: strings.Repeat("9", 100), Complete: false}
	data, _ := json.Marshal(c)
	parsed, err := ParseCheckpoint(data)
	Expect(err != nil || parsed != c).To(BeFalse(), func() string { return fmt.Sprintf("valid decimal/checkpoint: %v", err) })

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
		{
			_, err := ParseCheckpoint([]byte(bad))
			Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprintf("accepted invalid checkpoint: %s", bad) })
		}

	}
})
var _ = It("TestClosedPurposeTargetsAndNullableBinding", Label("unit"), func() {

	uuid := "00000000-0000-4000-8000-000000000001"
	result := []byte(`{"project_id":"` + uuid + `","document_id":null}`)
	normalized, err := NormalizeTarget("result", result)
	Expect(err != nil || !strings.Contains(string(normalized), `"draft_id":null`)).To(BeFalse(), func() string { return fmt.Sprintf("result omitted default: %s %v", normalized, err) })

	variants := map[string]string{
		"context":  `{"version_id":"` + uuid + `","page_path":null,"render_instance_id":null}`,
		"body":     `{"operation_id":"` + uuid + `","action":"evidence_verification","version_id":"` + uuid + `","evidence_id":"Unicode 任意"}`,
		"metadata": `{"operation_id":"` + uuid + `","action":"draft_replace","draft_id":"` + uuid + `"}`,
	}
	for purpose, data := range variants {
		{
			_, err := NormalizeTarget(purpose, []byte(data))
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprintf("valid %s target: %v", purpose, err) })
		}

	}
	for _, bad := range []struct{ purpose, data string }{
		{"result", `{"project_id":"` + uuid + `"}`},
		{"context", `{"version_id":"` + uuid + `","page_path":null}`},
		{"context", `{"version_id":"` + uuid + `","page_path":null,"render_instance_id":null,"extra":1}`},
		{"result", `{"project_id":"` + uuid + `","project_id":"` + uuid + `","document_id":null}`},
		{"body", `{"operation_id":"` + uuid + `","action":"unknown","version_id":"` + uuid + `","page_path":null}`},
	} {
		{
			_, err := NormalizeTarget(bad.purpose, []byte(bad.data))
			Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprintf("accepted incomplete/changed target: %s", bad.data) })
		}

	}
})
var _ = It("TestFingerprintCanonicalBodyPreconditionsAndOriginalOmissions", Label("unit"), func() {

	r := domain.Request{Key: domain.OperationKey{PrincipalID: "owner", OperationID: "00000000-0000-4000-8000-000000000001"}, Method: "PUT", Target: "/exact/target", Body: []byte(`{"a":1,"b":"exact"}`)}
	first, err := Fingerprint(r, []byte(`{"generated":"once"}`))
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	r.Body = []byte("{ \"b\":\"exact\", \"a\":1 }")
	r.Preconditions = map[string]string{}
	same, err := Fingerprint(r, []byte(`{"generated":"once"}`))
	Expect(err != nil || same != first).To(BeFalse(), func() string { return fmt.Sprint("transport ordering/absence changed semantic fingerprint") })

	r.Omitted = []string{"base_version_id"}
	changed, err := Fingerprint(r, []byte(`{"generated":"once"}`))
	Expect(err != nil || changed == first).To(BeFalse(), func() string { return fmt.Sprint("omission marker lost") })

	r.Key.OperationID = `"00000000-0000-4000-8000-000000000001"`
	{
		_, err := Fingerprint(r, nil)
		Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprint("quoted operation ID accepted") })
	}

})
