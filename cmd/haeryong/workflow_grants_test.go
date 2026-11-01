package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	"haeryong.local/haeryong/internal/application/service"
	sec "haeryong.local/haeryong/internal/security/session"
)

var _ = It("GGR03 actual CLI remembers one scoped credential and frozen secret key journal without owner fallback after revocation", Label("persistence"), func() {
	t := GinkgoT()
	ctx := context.Background()
	selection := selection(t)
	initProfile(t, selection)
	daemon := startDaemon(t, selection, availablePort(t))
	owner, token := bootstrapOwnerCLI(t, selection, daemon.id.Endpoint, "workflow CLI owner")
	root, flags := callerProfile(t, selection, owner, token, daemon.id.Endpoint)
	flags = append(flags, "--data-dir", selection.DataDir)
	api, e := client.New(daemon.id.Endpoint, selection.Profile, client.Credential{Endpoint: daemon.id.Endpoint, Profile: selection.Profile, LibraryID: owner.LibraryID, Token: token})
	Expect(e).NotTo(HaveOccurred())
	identity, e := api.Identity(ctx)
	Expect(e).NotTo(HaveOccurred())
	input := filepath.Join(t.TempDir(), "workflow.json")
	body, _ := json.Marshal(workflowSelectionInput{Workflow: "generation", ProjectID: identity.UnfiledProjectID, ProviderID: "codex", SourceSelection: []byte(`{}`)})
	Expect(os.WriteFile(input, body, 0600)).To(Succeed())
	key, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	first := cli(t, 0, append([]string{"auth", "grant", "create", "--input", input, "--operation-id", key}, flags...)...)
	var response struct {
		Data struct {
			Binding   string `json:"workflow_binding_id"`
			Principal string `json:"principal_id"`
			Path      string `json:"credential_path"`
			Operation string `json:"operation_id"`
			Reused    bool   `json:"reused"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(first, &response)).To(Succeed())
	Expect(response.Data.Binding).NotTo(BeEmpty())
	Expect(response.Data.Operation).To(Equal(key))
	credential, e := client.LoadCredential(response.Data.Path)
	Expect(e).NotTo(HaveOccurred())
	Expect(bytes.Contains(first, []byte(credential.Token))).To(BeFalse())
	Expect(bytes.Contains(first, []byte(token))).To(BeFalse())
	state, e := clientstate.New(root)
	Expect(e).NotTo(HaveOccurred())
	journal, e := state.LoadWorkflow(key)
	Expect(e).NotTo(HaveOccurred())
	Expect(journal.Response).NotTo(BeNil())
	Expect(journal.Request.TokenVerifier == sec.Verifier("workflow", credential.Token)).To(BeTrue())
	Expect(journal.Credential.Token == credential.Token).To(BeTrue())
	// An exact completed key consumes the frozen private request even when the
	// original selection file is gone; it cannot mint a new secret or principal.
	Expect(os.Remove(input)).To(Succeed())
	replay := cli(t, 0, append([]string{"auth", "grant", "create", "--input", input, "--operation-id", key}, flags...)...)
	var replayed struct {
		Data map[string]any `json:"data"`
	}
	Expect(json.Unmarshal(replay, &replayed)).To(Succeed())
	Expect(replayed.Data["principal_id"]).To(Equal(response.Data.Principal))
	same, e := client.LoadCredential(response.Data.Path)
	Expect(e).NotTo(HaveOccurred())
	Expect(same.Token == credential.Token).To(BeTrue())
	Expect(os.WriteFile(input, body, 0600)).To(Succeed())
	reused := cli(t, 0, append([]string{"auth", "grant", "create", "--input", input}, flags...)...)
	var remembered struct {
		Data struct {
			Operation string `json:"operation_id"`
			Reused    bool   `json:"reused"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(reused, &remembered)).To(Succeed())
	Expect(remembered.Data.Reused).To(BeTrue())
	Expect(remembered.Data.Operation).To(Equal(key))
	workflowFlags := append([]string{}, flags...)
	for n, v := range workflowFlags {
		if v == "--credential" {
			workflowFlags[n+1] = response.Data.Path
		}
	}
	cli(t, 3, append([]string{"auth", "grant", "create", "--input", input}, workflowFlags...)...)
	cli(t, 0, append([]string{"auth", "grant", "revoke", response.Data.Binding}, flags...)...)
	cli(t, 3, append([]string{"projects", "list"}, workflowFlags...)...)
	// The owner's separate credential still works; workflow failure never chose it.
	cli(t, 0, append([]string{"projects", "list"}, flags...)...)
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(selection)...)...)
	daemon.wait(t)
})
