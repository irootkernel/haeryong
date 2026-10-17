package imports

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"time"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("TestHTTPUploadStrictKeysControlHeadersAndDigest", Label("persistence"), func() {
	t := GinkgoT()

	f := newHTTPFixture(t)
	body := map[string]any{"purpose": "result", "target": map[string]any{"project_id": f.project, "document_id": nil}, "input_mode": "directory"}
	encoded, _ := json.Marshal(body)
	status, _, data := f.request("POST", "/api/v1/uploads", bytes.NewReader(encoded), map[string]string{"Content-Type": "application/json"})
	requireStatus(t, status, 428, data)
	var missing struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	Expect(json.Unmarshal(data, &missing) != nil || missing.Error.Details["missing"] != "Idempotency-Key").To(BeFalse(), func() string { return fmt.Sprint("missing operation key detail changed") })

	for _, key := range []string{"quoted", "\"11111111-1111-4111-8111-111111111111\"", "11111111-1111-4111-8111-11111111111F"} {
		status, _, data = f.request("POST", "/api/v1/uploads", bytes.NewReader(encoded), map[string]string{"Content-Type": "application/json", "Idempotency-Key": key})
		requireStatus(t, status, 400, data)
	}
	r, err := http.NewRequest("POST", f.server.URL+"/api/v1/uploads", bytes.NewReader(encoded))
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	r.Header.Set("Origin", f.server.URL)
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Add("Idempotency-Key", operation(t))
	r.Header.Add("Idempotency-Key", operation(t))
	response, err := f.server.Client().Do(r)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	response.Body.Close()
	Expect(response.StatusCode != 400).To(BeFalse(), func() string { return fmt.Sprint("multiple operation keys accepted") })

	for _, raw := range []string{`{"purpose":"result","purpose":"result","input_mode":"directory","target":{}}`, `{"purpose":null,"input_mode":"directory","target":{}}`, `{"purpose":"result","input_mode":"directory","target":{},"admin":true}`} {
		status, _, data = f.request("POST", "/api/v1/uploads", strings.NewReader(raw), map[string]string{"Content-Type": "application/json", "Idempotency-Key": operation(t)})
		requireStatus(t, status, 400, data)
	}
	status, _, data = f.request("GET", "/api/v1/uploads", nil, nil)
	requireStatus(t, status, 200, data)
	var list struct {
		Items []any `json:"items"`
	}
	list = valueAs[struct {
		Items []any `json:"items"`
	}](t, data)
	Expect(len(list.Items) != 0).To(BeFalse(), func() string { return fmt.Sprint("invalid control request mutated upload state") })

	id := f.upload("directory", "strict")
	digest := sha256.Sum256([]byte("valid"))
	base := map[string]string{"Content-Type": "application/octet-stream", "Haeryong-Upload-Offset": "0", "Haeryong-Upload-Chunk-SHA256": hex.EncodeToString(digest[:]), "Haeryong-Upload-Final": "true"}
	for _, offset := range []string{"00", "-1", "1.0", "9223372036854775808"} {
		headers := map[string]string{}
		for k, v := range base {
			headers[k] = v
		}
		headers["Haeryong-Upload-Offset"] = offset
		status, _, data = f.request("PUT", "/api/v1/uploads/"+id+"/entries?path=file&kind=file", strings.NewReader("valid"), headers)
		requireStatus(t, status, 400, data)
	}
	base["Haeryong-Upload-Chunk-SHA256"] = strings.Repeat("0", 64)
	status, _, data = f.request("PUT", "/api/v1/uploads/"+id+"/entries?path=file&kind=file", strings.NewReader("valid"), base)
	requireStatus(t, status, 422, data)
	Expect(f.detail(id).Checkpoint.AcceptedBytes != "0").To(BeFalse(), func() string { return fmt.Sprint("digest failure claimed accepted bytes") })

	status, _, data = f.request("POST", "/api/v1/not-a-route", strings.NewReader("{}"), map[string]string{"Content-Type": "application/json"})
	requireStatus(t, status, 404, data)
	status, _, data = f.request("GET", "/api/v1/projects", nil, nil)
	requireStatus(t, status, 503, data)
	var unavailable struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	Expect(json.Unmarshal(data, &unavailable) != nil || unavailable.Error.Code != "FEATURE_NOT_READY").To(BeFalse(), func() string { return fmt.Sprint("delegation changed future capability semantics") })

})
var _ = It("TestOwnerDiscoversAndAbandonsAfterOriginatingSessionRevocation", Label("persistence"), func() {
	t := GinkgoT()

	f := newHTTPFixture(t)
	id := f.upload("directory", "owner recovery")
	status, _, data := f.put(id, "kept.bin", "file", []byte("retained"), 0, true)
	requireStatus(t, status, 200, data)
	old := f.token
	identity, err := f.auth.Authenticate(context.Background(), old)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	p, err := f.auth.CreatePairing(context.Background(), f.server.URL, "replacement owner", "second-fixture")
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	{

		_, err = f.auth.Decide(context.Background(), old, domain.PairingDecision{ID: p.ID, Code: p.Code, Origin: p.Origin, ETag: p.ETag, OperationID: operation(t), Action: "approve"})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	poll, err := f.auth.Poll(context.Background(), p.ID, p.PollingSecret, p.Origin)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	{

		_, err = f.auth.Revoke(context.Background(), poll.Token, identity.ID)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	for _, path := range []string{"uploads", "uploads/" + id, "uploads/" + id + "/entries", "uploads/" + id + "/ranges", "uploads/" + id + "/pins"} {
		status, _, data = f.request("GET", "/api/v1/"+path, nil, nil)
		requireStatus(t, status, 401, data)
	}
	f.token = poll.Token
	status, _, data = f.request("GET", "/api/v1/uploads?unadopted=true", nil, nil)
	requireStatus(t, status, 200, data)
	list := valueAs[struct {
		Items []struct {
			ID string `json:"upload_id"`
		} `json:"items"`
	}](t, data)
	Expect(len(list.Items) != 1 || list.Items[0].ID != id).To(BeFalse(), func() string { return fmt.Sprint("owner could not discover retained upload") })

	status, data = f.json("POST", "/api/v1/uploads/"+id+"/abandon", operation(t), map[string]any{})
	requireStatus(t, status, 200, data)
	Expect(f.detail(id).State != "abandoned").To(BeFalse(), func() string { return fmt.Sprint("explicit abandon not durable") })

	status, _, data = f.put(id, "late.bin", "file", []byte("late"), 0, true)
	requireStatus(t, status, 409, data)
	f.restart()
	Expect(f.detail(id).State != "abandoned").To(BeFalse(), func() string { return fmt.Sprint("abandoned upload resurrected") })

})
var _ = It("TestUploadCompleteRequiresEveryFinalRangeAndCorruptZIPStaysUnpublished", Label("persistence"), func() {
	t := GinkgoT()

	f := newHTTPFixture(t)
	id := f.upload("directory", "incomplete")
	status, _, data := f.put(id, "gap.bin", "file", []byte("last"), 5, true)
	requireStatus(t, status, 200, data)
	cp := f.detail(id).Checkpoint
	cp.Generation++
	cp.Complete = true
	status, data = f.json("POST", "/api/v1/uploads/"+id+"/complete", operation(t), map[string]any{"checkpoint": cp})
	requireStatus(t, status, 409, data)
	Expect(f.detail(id).State != "open").To(BeFalse(), func() string { return fmt.Sprint("missing ranges entered sealing") })

	archive := f.upload("archive", "corrupt.zip")
	status, _, data = f.put(archive, "_input", "file", []byte("not a ZIP archive"), 0, true)
	requireStatus(t, status, 200, data)
	cp = f.detail(archive).Checkpoint
	cp.Generation++
	cp.Complete = true
	status, data = f.json("POST", "/api/v1/uploads/"+archive+"/complete", operation(t), map[string]any{"checkpoint": cp})
	requireStatus(t, status, 202, data)
	job := valueAs[struct {
		ID string `json:"job_id"`
	}](t, data)
	waitJob(t, f, job.ID, "failed")
	detail := f.detail(archive)
	Expect(detail.State != "open" || detail.Error == nil || detail.ContentSetID != nil).To(BeFalse(), func() string { return fmt.Sprint("corrupt archive became reusable content") })
	Expect(*detail.Error != "PACKAGE_INVALID").To(BeFalse(), func() string { return fmt.Sprintf("corrupt ZIP error lost typed diagnosis: %s", *detail.Error) })

	status, data = f.json("POST", "/api/v1/imports", operation(t), map[string]any{"upload_id": archive, "document_id": nil})
	requireStatus(t, status, 409, data)
})

func waitJob(t GinkgoTInterface, f *httpFixture, id, state string) domain.Job {
	GinkgoHelper()
	for i := 0; i < 1500; i++ {
		job, err := f.store.Job(context.Background(), id)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		if job.State == state {
			return job
		}
		Expect(job.State == "completed" || job.State == "failed" || job.State == "cancelled").To(BeFalse(), func() string { return fmt.Sprintf("job state=%s want=%s", job.State, state) })

		time.Sleep(10 * time.Millisecond)
	}
	Fail(fmt.Sprint("worker did not reach terminal state"))
	return domain.Job{}
}

var _ = It("TestHTTPControlUnicodeNeverSilentlyReplacesNames", Label("persistence"), func() {
	t := GinkgoT()

	f := newHTTPFixture(t)
	request := func(rawName string) (int, []byte) {
		body := `{"purpose":"result","input_mode":"directory","selection_name":` + rawName + `,"target":{"project_id":"` + f.project + `","document_id":null}}`
		status, _, data := f.request("POST", "/api/v1/uploads", strings.NewReader(body), map[string]string{"Content-Type": "application/json", "Idempotency-Key": operation(t)})
		return status, data
	}
	for _, raw := range []string{"\"raw\xffname\"", `"\ud800"`, `"\udc00"`, `"\ud800\u0041"`} {
		status, data := request(raw)
		requireStatus(t, status, 400, data)
	}
	status, data := request(`"\ud83d\ude00"`)
	requireStatus(t, status, 201, data)
	created := valueAs[struct {
		ID string `json:"upload_id"`
	}](t, data)
	name := f.detail(created.ID).SelectionName
	Expect(name == nil || *name != "😀").To(BeFalse(), func() string { return fmt.Sprint("valid surrogate pair name changed") })

	status, _, data = f.put(created.ID, "alias", "symlink", []byte(`{"link_target":"\ud800"}`), 0, true)
	requireStatus(t, status, 400, data)
})
var _ = It("TestHTTPUploadSelectionNamePresenceAndNullableSummary", Label("persistence"), func() {
	t := GinkgoT()

	f := newHTTPFixture(t)
	input := map[string]any{"purpose": "result", "input_mode": "directory", "target": map[string]any{"project_id": f.project, "document_id": nil}, "selection_name": ""}
	status, data := f.json("POST", "/api/v1/uploads", operation(t), input)
	requireStatus(t, status, 400, data)
	status, _, data = f.request("GET", "/api/v1/uploads", nil, nil)
	requireStatus(t, status, 200, data)
	list := valueAs[struct {
		Items []map[string]any `json:"items"`
	}](t, data)
	Expect(len(list.Items) != 0).To(BeFalse(), func() string { return fmt.Sprint("explicit empty selection name created an upload") })

	delete(input, "selection_name")
	status, data = f.json("POST", "/api/v1/uploads", operation(t), input)
	requireStatus(t, status, 201, data)
	created := valueAs[struct {
		ID string `json:"upload_id"`
	}](t, data)
	Expect(f.detail(created.ID).SelectionName != nil).To(BeFalse(), func() string { return fmt.Sprint("omitted selection name did not remain null") })

	status, _, data = f.request("GET", "/api/v1/uploads", nil, nil)
	requireStatus(t, status, 200, data)
	list = valueAs[struct {
		Items []map[string]any `json:"items"`
	}](t, data)
	Expect(len(list.Items) != 1).To(BeFalse(), func() string { return fmt.Sprint("omitted selection name upload absent from list") })
	{

		name, present := list.Items[0]["selection_name"]
		Expect(!present || name != nil).To(BeFalse(), func() string { return fmt.Sprint("summary selection_name must be present and null when omitted") })
	}

	input["selection_name"] = "chosen result"
	status, data = f.json("POST", "/api/v1/uploads", operation(t), input)
	requireStatus(t, status, 201, data)
	created = valueAs[struct {
		ID string `json:"upload_id"`
	}](t, data)
	{
		name := f.detail(created.ID).SelectionName
		Expect(name == nil || *name != "chosen result").To(BeFalse(), func() string { return fmt.Sprint("explicit nonempty selection name changed") })
	}

})
