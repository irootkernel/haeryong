package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("TestOccupiedListenerProvidesExplicitRecoveryWithoutTakingOver", Label("persistence"), func() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("existing-listener"))
	}))
	DeferCleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	Expect(err).NotTo(HaveOccurred())
	selected := selection(GinkgoT())
	initProfile(GinkgoT(), selected)
	out := cli(GinkgoT(), 4, append([]string{"daemon", "run", "--port", endpoint.Port()}, profileArgs(selected)...)...)
	var result struct {
		Error struct{ Code, Message string }
	}
	Expect(json.Unmarshal(out, &result)).To(Succeed())
	Expect(result.Error.Code).To(Equal("CONFLICT"))
	Expect(result.Error.Message).To(ContainSubstring("explicit unused --port"))
	Expect(result.Error.Message).To(ContainSubstring("same --profile/--data-dir"))
	Expect(result.Error.Message).To(ContainSubstring("persisted network.listener"))
	response, err := http.Get(server.URL)
	Expect(err).NotTo(HaveOccurred())
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	Expect(err).NotTo(HaveOccurred())
	Expect(string(body)).To(Equal("existing-listener"))
})
