package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	client "haeryong.local/haeryong/internal/adapters/client"
)

// S5/S20/S21: sources approve merges the full array, keeps unspecified
// fields, sends repo_id:null for files sources, and separates the
// preparation key from the commit key.
var _ = It("TestActualCLISourcesApproveMergesAndKeepsSources", Label("persistence"), func() {
	t := GinkgoT()
	f := newSourceCommandFixture(t)
	_, flags := callerProfile(t, f.sel, f.session, f.token, f.base)

	out := cli(t, 0, append([]string{"projects", "register", "--name", "Approve Project"}, flags...)...)
	var registered struct {
		Data struct {
			Result struct {
				ProjectID string `json:"project_id"`
			} `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &registered)).NotTo(HaveOccurred(), string(out))
	project := registered.Data.Result.ProjectID

	// First approval: preview prints the plan and its id.
	first := cli(t, 0, append([]string{"sources", "approve", "--project", project, "--source-id", "95000000-0000-4000-8000-000000000001", "--kind", "git", "--root-path", t.TempDir(), "--repo-id", "90000000-0000-4000-8000-000000000001", "--provider-ids", "p1"}, flags...)...)
	var firstOut struct {
		Data struct {
			PlanID string `json:"plan_id"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(first, &firstOut)).NotTo(HaveOccurred(), string(first))
	Expect(firstOut.Data.PlanID).NotTo(BeEmpty())
	// Commit the first approval so the stored selection exists.
	cli(t, 0, append([]string{"sources", "approve", "--project", project, "--source-id", "95000000-0000-4000-8000-000000000001", "--kind", "git", "--root-path", t.TempDir(), "--repo-id", "90000000-0000-4000-8000-000000000001", "--provider-ids", "p1", "--confirmed", "--operation-id", "91000000-0000-4000-8000-000000000009"}, flags...)...)

	// Second approval (confirmed): the full merged array keeps the first
	// source, adds the second with repo_id null (files kind), and the
	// commit uses --operation-id.
	commitKey := "91000000-0000-4000-8000-000000000010"
	second := cli(t, 0, append([]string{"sources", "approve", "--project", project, "--source-id", "95000000-0000-4000-8000-000000000002", "--kind", "files", "--root-path", t.TempDir(), "--confirmed", "--operation-id", commitKey}, flags...)...)
	Expect(second).To(ContainSubstring("plan_id"))

	// Verify the persisted configuration: both sources remain.
	var config struct {
		Configuration struct {
			Projects map[string]struct {
				Sources []map[string]any `json:"sources"`
			} `json:"projects"`
		} `json:"configuration"`
	}
	Expect(f.ReadConfigurationForTest(t, &config)).To(Succeed())
	sources := config.Configuration.Projects[project].Sources
	Expect(sources).To(HaveLen(2))
	byID := map[string]map[string]any{}
	for _, source := range sources {
		byID[source["source_id"].(string)] = source
	}
	Expect(byID["95000000-0000-4000-8000-000000000001"]).NotTo(BeNil(), "the first approval survived the second")
	Expect(byID["95000000-0000-4000-8000-000000000001"]["provider_ids"]).To(Equal([]any{"p1"}))
	Expect(byID).To(HaveKey("95000000-0000-4000-8000-000000000002"))
	Expect(byID["95000000-0000-4000-8000-000000000002"]).To(HaveKeyWithValue("repo_id", BeNil()), "files sources send repo_id null")

	// S20: a third approve runs fine — the preparation pending key was
	// released, so a new intent is stored without conflict.
	cli(t, 0, append([]string{"sources", "approve", "--project", project, "--source-id", "95000000-0000-4000-8000-000000000002", "--kind", "files", "--root-path", t.TempDir()}, flags...)...)
	// Root-path remap keeps provider_ids/retention of the stored entry.
	remap := cli(t, 0, append([]string{"sources", "approve", "--project", project, "--source-id", "95000000-0000-4000-8000-000000000001", "--kind", "git", "--root-path", t.TempDir(), "--repo-id", "90000000-0000-4000-8000-000000000001", "--confirmed", "--operation-id", "91000000-0000-4000-8000-000000000011"}, flags...)...)
	Expect(remap).To(ContainSubstring("plan_id"))
	Expect(f.ReadConfigurationForTest(t, &config)).To(Succeed())
	sources = config.Configuration.Projects[project].Sources
	Expect(sources).To(HaveLen(2))
	for _, source := range sources {
		if source["source_id"] == "95000000-0000-4000-8000-000000000001" {
			Expect(source["provider_ids"]).To(Equal([]any{"p1"}), "remap keeps unspecified fields")
		}
	}
})

func hostOf(t GinkgoTInterface, base string) string {
	t.Helper()
	parsed, e := url.Parse(base)
	ExpectWithOffset(1, e).NotTo(HaveOccurred())
	return parsed.Host
}

// runKeyless runs the verify without --operation-id through the proxy.
func (e *expectationFixture) runKeyless(t GinkgoTInterface, proxy http.Handler, base string, expectExit int) []byte {
	t.Helper()
	_, _, flags := e.callerRoot(t, base)
	return cli(t, expectExit, append([]string{"evidence", "verify", "--version", e.version, "--evidence", "ev", "--expected-excerpt-file", e.filePath}, flags...)...)
}

// pendingJournalFor returns the pending command journal file names still on
// disk for this caller profile.
func pendingJournalFor(t GinkgoTInterface, flags []string, e *expectationFixture) []string {
	t.Helper()
	clientDir := ""
	for i, flag := range flags {
		if flag == "--client-dir" && i+1 < len(flags) {
			clientDir = flags[i+1]
		}
	}
	ExpectWithOffset(1, clientDir).NotTo(BeEmpty())
	entries, err := os.ReadDir(filepath.Join(clientDir, "pending"))
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// E1: an omitted --operation-id is recovered from the pending journal, so an
// interrupted run reruns with the same key instead of a new operation.
var _ = It("TestActualCLIEvidenceVerifyOmittedKeyRecoversAfterInterrupt", Label("persistence"), func() {
	t := GinkgoT()
	if _, err := exec.LookPath("git"); err != nil {
		Skip("git binary unavailable")
	}
	e := newExpectationFixture(t)
	// The shared caller profile is created by the interrupted run and reused
	// by the rerun; the key is omitted in both.
	proxy := &interruptingProxy{inner: e.f.handler, host: hostOf(t, e.f.base), killPath: "/uploads/", method: http.MethodPut, remaining: 3}
	front := httptest.NewServer(proxy)
	DeferCleanup(front.Close)
	e.runKeyless(t, proxy, front.URL, 5)
	uploads, verifications := e.counts(t)
	Expect(uploads).To(Equal(1))
	Expect(verifications).To(Equal(0))
	// The pending journal recorded the generated key.
	_, _, rerunFlags := e.callerRoot(t, e.f.base)
	// The rerun omits --operation-id: the same consumer key is recovered.
	out := cli(t, 0, append([]string{"evidence", "verify", "--version", e.version, "--evidence", "ev", "--expected-excerpt-file", e.filePath}, rerunFlags...)...)
	var verified struct {
		Data struct {
			Result struct {
				Outcome   string `json:"outcome"`
				Operation string `json:"operation_id"`
			} `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &verified)).NotTo(HaveOccurred(), string(out))
	Expect(verified.Data.Result.Outcome).To(Equal("mismatch"))
	uploads, verifications = e.counts(t)
	Expect(uploads).To(Equal(1), "the rerun reused the staged upload")
	Expect(verifications).To(Equal(1))
	// The pending key was released after the terminal result.
	pending := pendingJournalFor(t, rerunFlags, e)
	Expect(pending).To(BeEmpty(), "the pending key is released after completion")
})

// E2: a changed request under the same key is rejected.
var _ = It("TestActualCLIEvidenceVerifyChangedRequestSameKeyRejected", Label("persistence"), func() {
	t := GinkgoT()
	if _, err := exec.LookPath("git"); err != nil {
		Skip("git binary unavailable")
	}
	e := newExpectationFixture(t)
	_, _, flags := e.callerRoot(t, e.f.base)
	key := "92000000-0000-4000-8000-000000000020"
	cli(t, 0, append([]string{"evidence", "verify", "--version", e.version, "--evidence", "ev", "--operation-id", key}, flags...)...)
	// A different digest under the same key must not replay the first result.
	cli(t, 4, append([]string{"evidence", "verify", "--version", e.version, "--evidence", "ev", "--expected-sha256", "0000000000000000000000000000000000000000000000000000000000000000", "--operation-id", key}, flags...)...)
	_, verifications := e.counts(t)
	Expect(verifications).To(Equal(1), "no second verification for a changed request")
})

// S23: a scoped principal holding exactly version.read+source.read stages
// and seals the expectation by polling the upload, never the job route.
var _ = It("TestActualCLIScopedExpectationStagingWithoutJobRead", Label("persistence"), func() {
	t := GinkgoT()
	if _, err := exec.LookPath("git"); err != nil {
		Skip("git binary unavailable")
	}
	e := newExpectationFixture(t)
	token := e.f.seedScopedSourceWith(t, e.project, e.version)
	root := filepath.Join(t.TempDir(), "caller")
	path := filepath.Join(root, "credential.json")
	raw, _ := json.Marshal(client.Credential{Endpoint: e.f.base, Profile: e.f.sel.Profile, LibraryID: e.f.session.LibraryID, Token: token})
	Expect(os.MkdirAll(root, 0o700)).To(Succeed())
	Expect(os.WriteFile(path, raw, 0o600)).To(Succeed())
	flags := []string{"--profile", e.f.sel.Profile, "--client-dir", root, "--credential", path, "--json"}
	out := cli(t, 0, append([]string{"evidence", "verify", "--version", e.version, "--evidence", "ev", "--expected-excerpt-file", e.filePath, "--operation-id", "93000000-0000-4000-8000-000000000030"}, flags...)...)
	var verified struct {
		Data struct {
			Result struct {
				Outcome string `json:"outcome"`
			} `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &verified)).NotTo(HaveOccurred(), string(out))
	Expect(verified.Data.Result.Outcome).To(Equal("mismatch"))
	uploads, _ := e.counts(t)
	Expect(uploads).To(Equal(1))
})
