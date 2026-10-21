package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// assessments list --coverage-set follows the cursor required-unit view to
// the end (cli.md); a failed later page fails the command with no partial
// set (W3d).
var _ = It("TestActualCLIAssessmentsCoverageFollowsTheCursor", Label("persistence"), func() {
	t := GinkgoT()
	e := newExpectationFixture(t)
	_, _, flags := e.callerRoot(t, e.f.base)
	// One coverage set with 51 units: one more than the server's default page.
	run, set, stamp := "3b000000-0000-4000-8000-000000000001", "3b000000-0000-4000-8000-000000000002", "2026-10-01T09:00:00.000Z"
	Expect(e.f.store.Write(context.Background(), func(tx storage.WriteTx) error {
		if err := tx.PutObservationRun(domain.ObservationRun{ID: run, VersionID: e.version, PrincipalID: "owner", OperationID: "3b000000-0000-4000-8000-000000000003", RequestedSelection: []byte(`{"project_id":"p","mode":"current","source_ids":["git-1"]}`), ScopeJSON: []byte(`{"kind":"version","page_path":null,"region_id":null,"declared":true,"reason":null}`), CreatedAt: stamp}); err != nil {
			return err
		}
		if err := tx.PutSourceObservation(domain.SourceObservation{ID: "3b000000-0000-4000-8000-000000000004", RunID: run, VersionID: e.version, SourceID: "git-1", Availability: "unavailable", Classification: "unavailable", Reason: "missing_source", ConfigRevision: 1, PermissionsRevision: 1, ObservedAt: stamp, OperationID: "3b000000-0000-4000-8000-000000000003"}); err != nil {
			return err
		}
		if err := tx.PutCoverageSet(domain.CoverageSet{ID: set, VersionID: e.version, RunID: run, ScopeJSON: []byte(`{"kind":"version"}`), UnitKind: "page", CapturedAt: stamp, Origin: "source_observation"}); err != nil {
			return err
		}
		for i := 0; i < 51; i++ {
			if err := tx.PutRequiredUnit(domain.RequiredUnit{CoverageSetID: set, Kind: "page", PagePath: fmt.Sprintf("pages/p%02d.html", i), Reason: "missing_source"}); err != nil {
				return err
			}
		}
		return nil
	})).To(Succeed())
	args := []string{"assessments", "list", "--version", e.version, "--coverage-set", set}
	out := cli(t, 0, append(args, flags...)...)
	var listed struct {
		Data struct {
			Items []map[string]any `json:"items"`
			Next  any              `json:"next_cursor"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &listed)).NotTo(HaveOccurred(), string(out))
	Expect(listed.Data.Items).To(HaveLen(51), "every unit across the pages")
	Expect(listed.Data.Items[50]["page_path"]).To(Equal("pages/p50.html"))
	Expect(listed.Data.Next).To(BeNil())
	// The second coverage page fails: the command fails, nothing partial.
	var coverageCalls atomic.Int32
	forward := &faultProxy{inner: e.f.handler, host: hostOf(t, e.f.base)}
	failing := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/assessments/coverage") && coverageCalls.Add(1) == 2 {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"SERVICE_RECOVERING","message":"Service Unavailable","retryable":true,"details":{}},"request_id":"fault"}`))
			return
		}
		forward.ServeHTTP(w, r)
	})
	front := httptest.NewServer(failing)
	DeferCleanup(front.Close)
	_, _, proxyFlags := e.callerRoot(t, front.URL)
	out = cli(t, 5, append(args, proxyFlags...)...)
	Expect(string(out)).NotTo(ContainSubstring("pages/p00.html"), "no partial set is emitted")
	Expect(coverageCalls.Load()).To(BeNumerically(">=", 2))
})
