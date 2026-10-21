package configuration_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	httpconfig "haeryong.local/haeryong/internal/adapters/http/configuration"
	appauth "haeryong.local/haeryong/internal/application/auth"
	appconfig "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// ownerProfile prepares a real profile with one remembered owner session.
func ownerProfile(t GinkgoTInterface) (*foundation.Store, *appauth.Service, string) {
	GinkgoHelper()
	selection := service.Selection{Profile: "test", DataDir: t.TempDir() + "/library"}
	_, e := service.Initialize(selection)
	Expect(e).NotTo(HaveOccurred())
	w, e := service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { w.Close() })
	store := foundation.New(nil)
	state, closeStore, e := store.Prepare(context.Background(), w.Profile)
	Expect(e).NotTo(HaveOccurred())
	Expect(state.Ready).To(BeTrue())
	DeferCleanup(func() { _ = closeStore() })
	Expect(w.MarkStorageReady(nil)).To(Succeed())
	auth := appauth.New(store)
	auth.Now = func() time.Time { return time.Now() }
	pairing, e := auth.CreatePairing(context.Background(), "http://127.0.0.1:9270", "owner", "client")
	Expect(e).NotTo(HaveOccurred())
	op, _ := service.NewID()
	_, e = auth.BootstrapDecide(context.Background(), domain.PairingDecision{ID: pairing.ID, Action: "approve", Code: pairing.Code, Origin: pairing.Origin, ETag: pairing.ETag, OperationID: op})
	Expect(e).NotTo(HaveOccurred())
	redeemed, e := auth.Poll(context.Background(), pairing.ID, pairing.PollingSecret, pairing.Origin)
	Expect(e).NotTo(HaveOccurred())
	return store, auth, redeemed.Token
}

// summaryFailingStore fails the recorded-observation summary read with an
// ordinary storage error.
type summaryFailingStore struct{ *foundation.Store }

func (summaryFailingStore) SourceObservationSummaries(context.Context) ([]domain.SourceStatus, error) {
	return nil, errors.New("database disk image is malformed")
}

var _ = Describe("status source section", Label("persistence"), func() {
	t := GinkgoT()

	// W3b L8: a storage error deriving the source section fails /status with
	// 503 instead of reading as never observed.
	It("fails the status request on a source-summary storage error", func() {
		store, auth, token := ownerProfile(t)
		status := func(s app.Store) *httptest.ResponseRecorder {
			handler := httpconfig.New(appconfig.New(s, auth, nil))
			r := httptest.NewRequest("GET", "/api/v1/status", nil)
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			return w
		}
		healthy := status(store)
		Expect(healthy.Code).To(Equal(200), healthy.Body.String())
		Expect(healthy.Body.String()).To(ContainSubstring(`"reason":"not_observed"`), "no source configured is honest unavailability")
		failed := status(summaryFailingStore{store})
		Expect(failed.Code).To(Equal(503), failed.Body.String())
		Expect(failed.Body.String()).To(ContainSubstring("SERVICE_RECOVERING"))
		Expect(failed.Body.String()).NotTo(ContainSubstring("not_observed"), "a storage failure never reads as never observed")
	})
})
