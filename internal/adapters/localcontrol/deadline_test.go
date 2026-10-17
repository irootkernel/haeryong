package localcontrol

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/service"
)

var _ = It("TestProductResponseSurvivesControlDeadlineAndStillHonorsCancellation", Label("integration"), func() {
	entered, release := make(chan struct{}), make(chan struct{})
	cancelEntered, cancelled := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	endpoint := &HTTP{Product: func(string) (http.Handler, error) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/cancel" {
				close(cancelEntered)
				<-r.Context().Done()
				close(cancelled)
				return
			}
			close(entered)
			select {
			case <-release:
				io.WriteString(w, "verified result")
			case <-r.Context().Done():
			}
		}), nil
	}}
	profile := service.Profile{LibraryID: "library", ProfileID: "profile", ControlKey: strings.Repeat("ab", 32)}
	id, err := endpoint.Start(context.Background(), profile, service.Identity{
		LibraryID: profile.LibraryID, ProfileID: profile.ProfileID, InstanceID: "instance", API: service.APIVersion,
		Storage: service.StorageState{State: "ready", Ready: true},
	}, 0, nil)
	Expect(err).To(Succeed())
	DeferCleanup(func() {
		releaseOnce.Do(func() { close(release) })
		Expect(endpoint.Close(context.Background())).To(Succeed())
	})
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 15 * time.Second}
	DeferCleanup(client.CloseIdleConnections)
	type result struct {
		body string
		err  error
	}
	completed := make(chan result, 1)
	go func() {
		response, err := client.Get(id.Endpoint + "/api/v1/slow")
		out := result{err: err}
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			out.body, out.err = string(body), errors.Join(readErr, response.Body.Close())
		}
		completed <- out
	}()
	Eventually(entered, 2*time.Second).Should(BeClosed())
	// Cross the real server's five-second control-response deadline. The fake
	// product's work is released only afterward; no product test hook is used.
	timer := time.NewTimer(6 * time.Second)
	DeferCleanup(func() { timer.Stop() })
	_, err = Verify(context.Background(), profile, id)
	Expect(err).To(Succeed())
	<-timer.C
	releaseOnce.Do(func() { close(release) })
	var out result
	Eventually(completed, 5*time.Second).Should(Receive(&out))
	Expect(out.err).To(Succeed())
	Expect(out.body).To(Equal("verified result"))

	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, id.Endpoint+"/api/v1/cancel", nil)
	Expect(err).To(Succeed())
	requestDone := make(chan error, 1)
	go func() {
		response, err := client.Do(request)
		if response != nil {
			response.Body.Close()
		}
		requestDone <- err
	}()
	Eventually(cancelEntered, 2*time.Second).Should(BeClosed())
	cancel()
	Eventually(cancelled, 2*time.Second).Should(BeClosed())
	Eventually(requestDone, 2*time.Second).Should(Receive(&err))
	Expect(errors.Is(err, context.Canceled)).To(BeTrue())
})
