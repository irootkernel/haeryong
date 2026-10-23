package main

import (
	"context"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	client "haeryong.local/haeryong/internal/adapters/client"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
)

var _ = Describe("reference CLI", Label("unit"), func() {
	const version = "11111111-1111-4111-8111-111111111111"
	It("RF08 admits only exact feature flags and encodes reference IDs once", func() {
		for _, args := range [][]string{{"references", "list", "--version", version, "--limit", "1", "--cursor", "opaque", "--json"}, {"references", "resolve", "--version", version, "--reference-id", "../참조/%2F", "--page", "page.html", "--region", "retry"}} {
			opt, err := parse(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(validateCommandFlags(opt)).To(Succeed())
			Expect(isProductCommand(opt)).To(BeTrue())
			path, err := referenceCLIPath(opt)
			Expect(err).NotTo(HaveOccurred())
			u, err := url.Parse(path)
			Expect(err).NotTo(HaveOccurred())
			if opt.command[1] == "resolve" {
				Expect(u.Query().Get("reference_id")).To(Equal("../참조/%2F"))
			}
		}
		for _, args := range [][]string{{"references", "list"}, {"references", "list", "--version", "bad"}, {"references", "list", "--version", version, "--limit", "201"}, {"references", "resolve", "--version", version}, {"references", "resolve", "--version", version, "--reference-id", "r", "--region", "r"}, {"references", "list", "--version", version, "--operation-id", "o"}, {"references", "list", "--version", version, "--page", "a.html"}, {"references", "resolve", "--version", version, "--reference-id", "r", "--cursor", "c"}} {
			opt, err := parse(args)
			if err == nil {
				err = validateCommandFlags(opt)
			}
			Expect(err).To(HaveOccurred())
			var code commandError
			Expect(err).To(BeAssignableToTypeOf(code))
		}
	})
	It("RF08 issues one feature GET without retry and maps auth/gone/unavailable exits", func() {
		for _, status := range []int{200, 400, 401, 403, 404, 410, 503} {
			count := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				Expect(r.Method).To(Equal("GET"))
				Expect(r.URL.Query().Get("reference_id")).To(Equal("../참조/%2F"))
				w.WriteHeader(status)
				if status == 200 {
					json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"resolution": "unresolved"}})
				} else {
					json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": map[int]string{400: "INVALID_REQUEST", 401: "AUTH_REQUIRED", 403: "FORBIDDEN", 404: "NOT_FOUND", 410: "TARGET_GONE", 503: "SERVICE_RECOVERING"}[status], "details": map[string]any{}}})
				}
			}))
			api, err := client.New(server.URL, "default", client.Credential{Endpoint: server.URL, Profile: "default", LibraryID: version, Token: strings.Repeat("a", 64)})
			Expect(err).NotTo(HaveOccurred())
			opt, err := parse([]string{"references", "resolve", "--version", version, "--reference-id", "../참조/%2F"})
			Expect(err).NotTo(HaveOccurred())
			emitted := false
			err = referenceCLI(context.Background(), api, opt, func(any) error { emitted = true; return nil })
			server.Close()
			Expect(count).To(Equal(1))
			if status == 200 {
				Expect(err).NotTo(HaveOccurred())
				Expect(emitted).To(BeTrue())
			} else {
				Expect(emitted).To(BeFalse())
				code := map[int]int{400: 2, 401: 3, 403: 3, 404: 3, 410: 4, 503: 5}[status]
				Expect(err.(commandError).code).To(Equal(code))
			}
		}
	})
})
