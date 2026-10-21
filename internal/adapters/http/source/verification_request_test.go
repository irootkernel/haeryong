package source_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func rejectVerificationBodies(bodies []string) {
	GinkgoHelper()
	f := newHTTPFixture(GinkgoT())
	f.coreReader.blobs[httpRevision+"\x00f.txt"] = "exact bytes\n"
	f.buildEvidenceVersion(GinkgoT(), []string{"simple"}, httpRevision)
	actor, err := f.auth.Authenticate(context.Background(), f.token)
	Expect(err).NotTo(HaveOccurred())
	for _, body := range bodies {
		key, err := service.NewID()
		Expect(err).NotTo(HaveOccurred())
		reply := f.post("/api/v1/versions/"+f.version+"/source-evidence-verifications?evidence_id=simple", f.token, key, body)
		Expect(reply.Code).To(Equal(400), body+": "+reply.Body.String())
		Expect(decode(GinkgoT(), reply.Body.Bytes())["error"].(map[string]any)["code"]).To(Equal("INVALID_REQUEST"))
		_, err = f.store.Receipt(context.Background(), domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key})
		Expect(err).To(MatchError(domain.ErrNotFound), "invalid request must not reserve a receipt")
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			_, err := tx.CheckIntentByOperation(actor.PrincipalID, key)
			Expect(err).To(MatchError(domain.ErrNotFound), "invalid request must not create a check intent")
			return nil
		})).To(Succeed())
	}
	// The empty object remains an actual captured check, not a blanket denial.
	key, err := service.NewID()
	Expect(err).NotTo(HaveOccurred())
	Expect(f.post("/api/v1/versions/"+f.version+"/source-evidence-verifications?evidence_id=simple", f.token, key, `{}`).Code).To(Equal(201))
}

var _ = It("rejects non-object verification requests before reservation", Label("persistence"), func() {
	rejectVerificationBodies([]string{`null`, `[]`, `"text"`, `42`, `false`})
})

var _ = It("rejects non-closed verification expectation references before reservation", Label("persistence"), func() {
	rejectVerificationBodies([]string{
		`{"expected_excerpt_ref":null}`,
		`{"expected_excerpt_ref":[]}`,
		`{"expected_excerpt_ref":"reference"}`,
		`{"expected_excerpt_ref":{}}`,
		`{"expected_excerpt_ref":{"upload_id":null}}`,
		`{"expected_excerpt_ref":{"other":"11111111-1111-4111-8111-111111111111"}}`,
		`{"expected_excerpt_ref":{"upload_id":"11111111-1111-4111-8111-111111111111","unknown":null}}`,
		`{"expected_excerpt_ref":{"upload_id":"11111111-1111-4111-8111-111111111111","unknown":true}}`,
	})
})
