package sqlite

import (
	"bytes"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	scanhttp "haeryong.local/haeryong/internal/adapters/http/scans"
	"haeryong.local/haeryong/internal/domain"
	"net/http"
	"net/http/httptest"
)

var _ = It("T00805 HTTP adapter uses real persisted owner mutations validators and exact replay", Label("persistence"), func() {
	f, core, token, record := decisionFixture("topic_assignment")
	handler := scanhttp.New(core)
	call := func(method, path, etag, key, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		if etag != "" {
			req.Header.Set("If-Match", etag)
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	findingPath := "/api/v1/findings/" + record.ID + "/decisions"
	Expect(call("POST", findingPath, "", f.id(), `{"decision":"approve"}`).Code).To(Equal(428))
	Expect(call("POST", findingPath, domain.FindingETag(record), f.id(), `{"decision":"approve","decision":"reject"}`).Code).To(Equal(400))
	approved := call("POST", findingPath, domain.FindingETag(record), f.id(), `{"decision":"approve"}`)
	Expect(approved.Code).To(Equal(200))
	var view struct {
		Data domain.FindingActionResult `json:"data"`
	}
	Expect(json.Unmarshal(approved.Body.Bytes(), &view)).To(Succeed())
	effect := view.Data.Effect
	Expect(effect.Availability).To(Equal("available"))
	initialEffectTag := effect.ETag // later decode reuses the existing pointer
	Expect(approved.Header().Get("ETag")).To(Equal(view.Data.Finding.ETag))
	topicPath := "/api/v1/topic-assignments/" + effect.ID
	pinned := call("PUT", topicPath, effect.ETag, f.id(), `{"pinned":true,"decision":"accepted"}`)
	Expect(pinned.Code).To(Equal(200))
	Expect(json.Unmarshal(pinned.Body.Bytes(), &view)).To(Succeed())
	Expect(view.Data.Effect.Pinned).To(BeTrue())
	Expect(call("POST", "/api/v1/overlays/"+effect.ID+"/undo", initialEffectTag, f.id(), "{}").Code).To(Equal(http.StatusPreconditionFailed))
	unpinned := call("PUT", topicPath, view.Data.Effect.ETag, f.id(), `{"pinned":false,"decision":"accepted"}`)
	Expect(unpinned.Code).To(Equal(200))
	Expect(json.Unmarshal(unpinned.Body.Bytes(), &view)).To(Succeed())
	tag := view.Data.Effect.ETag
	key := f.id()
	path := "/api/v1/overlays/" + effect.ID + "/undo"
	undone := call("POST", path, tag, key, "{}")
	Expect(undone.Code).To(Equal(200))
	replayed := call("POST", path, tag, key, "{}")
	Expect(replayed.Code).To(Equal(200))
	var originalEnvelope, replayEnvelope struct {
		Data json.RawMessage `json:"data"`
	}
	Expect(json.Unmarshal(undone.Body.Bytes(), &originalEnvelope)).To(Succeed())
	Expect(json.Unmarshal(replayed.Body.Bytes(), &replayEnvelope)).To(Succeed())
	Expect(string(replayEnvelope.Data)).To(MatchJSON(string(originalEnvelope.Data))) // request_id is fresh delivery accounting
	Expect(json.Unmarshal(undone.Body.Bytes(), &view)).To(Succeed())
	refused := call("PUT", topicPath, view.Data.Effect.ETag, f.id(), `{"pinned":false,"decision":"accepted"}`)
	Expect(refused.Code).To(Equal(409))
	Expect(refused.Body.String()).To(ContainSubstring("undone_requires_reassessment_approve"))
})
