package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/storage"
)

// The observation CLI family: sources observe runs the owner POST through the
// automatic pending key (E1), an explicit --operation-id replays identically
// after completion, an uncertain outcome keeps the key, and assessments list
// reads the composed summary and one coverage set's units.
var _ = It("TestActualCLISourcesObserveRunsReplaysAndRecovers", Label("persistence"), func() {
	t := GinkgoT()
	if _, err := exec.LookPath("git"); err != nil {
		Skip("git binary unavailable")
	}
	e := newExpectationFixture(t)
	_, _, flags := e.callerRoot(t, e.f.base)

	observe := append([]string{"sources", "observe", "--version", e.version}, flags...)
	out := cli(t, 0, observe...)
	var first struct {
		Data struct {
			OperationID string `json:"operation_id"`
			Result      struct {
				RunID       string `json:"run_id"`
				TargetSetID any    `json:"target_set_id"`
				CoverageSet any    `json:"coverage_set_id"`
				Sources     []struct {
					SourceID       string `json:"source_id"`
					Classification string `json:"classification"`
				} `json:"sources"`
			} `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &first)).NotTo(HaveOccurred(), string(out))
	Expect(first.Data.Result.RunID).NotTo(BeEmpty())
	Expect(first.Data.Result.TargetSetID).NotTo(BeNil())
	Expect(first.Data.Result.Sources).To(HaveLen(1))
	Expect(first.Data.Result.Sources[0].Classification).To(Equal("unchanged"))

	// A direct change on the cited path classifies the next run direct.
	Expect(os.WriteFile(filepath.Join(e.repo, "pkg", "main.go"), []byte("package main\n\nfunc main() { changed() }\n"), 0o644)).To(Succeed())
	gitIn(e.repo, "add", "-A")
	gitIn(e.repo, "commit", "-m", "advance")
	out = cli(t, 0, observe...)
	var second struct {
		Data struct {
			Result struct {
				RunID   string `json:"run_id"`
				Sources []struct {
					Classification string   `json:"classification"`
					ChangedPaths   []string `json:"changed_paths"`
				} `json:"sources"`
			} `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &second)).NotTo(HaveOccurred(), string(out))
	Expect(second.Data.Result.Sources[0].Classification).To(Equal("direct"))
	Expect(second.Data.Result.Sources[0].ChangedPaths).To(ContainElement("pkg/main.go"))

	// An explicit --operation-id replays the completed run identically.
	replay := append([]string{"sources", "observe", "--version", e.version, "--operation-id", first.Data.OperationID}, flags...)
	out = cli(t, 0, replay...)
	var replayed struct {
		Data struct {
			OperationID string `json:"operation_id"`
			Result      struct {
				RunID string `json:"run_id"`
			} `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &replayed)).NotTo(HaveOccurred(), string(out))
	Expect(replayed.Data.OperationID).To(Equal(first.Data.OperationID))
	Expect(replayed.Data.Result.RunID).To(Equal(first.Data.Result.RunID), "the identical rerun replays the recorded run")

	// A lost response is not definitive: the pending key survives and a
	// rerun completes the same operation (E1).
	dropped := &faultProxy{inner: e.f.handler, host: hostOf(t, e.f.base), method: http.MethodPost, path: "/source-observations", drop: true}
	front := httptest.NewServer(dropped)
	DeferCleanup(front.Close)
	root, _, proxyFlags := e.callerRoot(t, front.URL)
	cli(t, 5, append([]string{"sources", "observe", "--version", e.version}, proxyFlags...)...)
	pending := pendingOperations(t, root)
	Expect(pending).To(HaveLen(1), "the uncertain outcome keeps the automatic key")
	out = cli(t, 0, append([]string{"sources", "observe", "--version", e.version}, flags...)...)
	var recovered struct {
		Data struct {
			OperationID string `json:"operation_id"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &recovered)).NotTo(HaveOccurred(), string(out))
	Expect(recovered.Data.OperationID).To(Equal(pending[0]), "the rerun resumes the same operation")
	Expect(pendingOperations(t, root)).To(BeEmpty(), "released after the result was emitted")

	// assessments list reads the composed summary and one coverage set.
	out = cli(t, 0, append([]string{"assessments", "list", "--version", e.version}, flags...)...)
	var assessments struct {
		Data struct {
			Items []any `json:"items"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &assessments)).NotTo(HaveOccurred(), string(out))
	Expect(assessments.Data.Items).To(BeEmpty())
	setID, ok := first.Data.Result.CoverageSet.(string)
	Expect(ok).To(BeTrue())
	out = cli(t, 0, append([]string{"assessments", "list", "--version", e.version, "--coverage-set", setID}, flags...)...)
	var coverage struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &coverage)).NotTo(HaveOccurred(), string(out))
	Expect(coverage.Data.Items).To(HaveLen(1), "a pageless version carries one whole-version unit")
	Expect(coverage.Data.Items[0]["kind"]).To(Equal("version"))

	// The store holds exactly the dispatched runs.
	runs := 0
	Expect(e.f.store.Write(context.Background(), func(tx storage.WriteTx) error {
		list, err := tx.ObservationRunsByVersion(e.version)
		runs = len(list)
		return err
	})).To(Succeed())
	Expect(runs).To(Equal(3), "two automatic runs plus the recovered in-flight one")
})

func gitIn(dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	ExpectWithOffset(1, cmd.Run()).To(Succeed(), "git %v: %s", args, out.String())
}
