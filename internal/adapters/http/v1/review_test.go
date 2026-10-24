package v1

import (
	"bytes"
	"context"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("C1 Review HTTP preserves a reconciling receipt and returns its public error", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	upload := f.create()
	f.put(upload, "hello.html", "file", []byte("<h1>Review</h1>"))
	version := f.publish(upload, nil)
	actor, err := f.auth.Authenticate(context.Background(), f.token)
	Expect(err).NotTo(HaveOccurred())
	body := []byte(`{"action":"complete","state":"reviewed"}`)
	request := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: id(GinkgoT())}, Method: "PUT", Target: "/api/v1/versions/" + version.ID + "/review", Body: body, Preconditions: map[string]string{"if-none-match": "*"}}
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		if _, _, err := tx.ReserveOperation(request, nil); err != nil {
			return err
		}
		return tx.ReconcileOperation(request.Key)
	})).To(Succeed())
	status, _, data := f.request("PUT", request.Target, bytes.NewReader(body), map[string]string{"Content-Type": "application/json", "Idempotency-Key": request.Key.OperationID, "If-None-Match": "*"})
	want(GinkgoT(), status, 409, data)
	var failure struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	Expect(json.Unmarshal(data, &failure)).To(Succeed())
	Expect(failure.Error.Code).To(Equal("OPERATION_RECONCILIATION_REQUIRED"))
	status, _, data = f.request("GET", request.Target, nil, nil)
	want(GinkgoT(), status, 200, data)
	Expect(decoded[struct {
		Exists bool `json:"exists"`
	}](GinkgoT(), data).Exists).To(BeFalse())
	status, _, data = f.request("GET", request.Target+"/history", nil, nil)
	want(GinkgoT(), status, 200, data)
	Expect(decoded[struct {
		Items []json.RawMessage `json:"items"`
	}](GinkgoT(), data).Items).To(BeEmpty())
	// Repeating the original request must still reconcile, without a new mutation.
	status, _, data = f.request("PUT", request.Target, bytes.NewReader(body), map[string]string{"Content-Type": "application/json", "Idempotency-Key": request.Key.OperationID, "If-None-Match": "*"})
	want(GinkgoT(), status, 409, data)
	Expect(json.Unmarshal(data, &failure)).To(Succeed())
	Expect(failure.Error.Code).To(Equal("OPERATION_RECONCILIATION_REQUIRED"))
})
