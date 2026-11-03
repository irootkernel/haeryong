package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
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

var _ = It("GGR04 actual scoped import CLI completes and recovers the same job and published bytes without widening authority", Label("persistence"), func() {
	t := GinkgoT()
	ctx := context.Background()
	selected := selection(t)
	initProfile(t, selected)
	daemon := startDaemon(t, selected, availablePort(t))
	owner, token := bootstrapOwnerCLI(t, selected, daemon.id.Endpoint, "scoped import owner")
	root, ownerFlags := callerProfile(t, selected, owner, token, daemon.id.Endpoint)
	ownerFlags = append(ownerFlags, "--data-dir", selected.DataDir)
	ownerAPI, e := client.New(daemon.id.Endpoint, selected.Profile, client.Credential{Endpoint: daemon.id.Endpoint, Profile: selected.Profile, LibraryID: owner.LibraryID, Token: token})
	Expect(e).NotTo(HaveOccurred())
	identity, e := ownerAPI.Identity(ctx)
	Expect(e).NotTo(HaveOccurred())
	id := func() string { value, err := service.NewID(); Expect(err).NotTo(HaveOccurred()); return value }
	type issuedGrant struct {
		Principal string                 `json:"principal_id"`
		Path      string                 `json:"credential_path"`
		Scope     domain.CredentialScope `json:"scope"`
		Operation string                 `json:"operation_id"`
		InputPath string                 `json:"-"`
	}
	issue := func() issuedGrant {
		GinkgoHelper()
		path := filepath.Join(t.TempDir(), "import-grant.json")
		body, err := json.Marshal(workflowSelectionInput{BindingID: id(), Workflow: "import", ProjectID: identity.UnfiledProjectID})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, body, 0600)).To(Succeed())
		key := id()
		output := cli(t, 0, append([]string{"auth", "grant", "create", "--input", path, "--operation-id", key}, ownerFlags...)...)
		var response struct {
			Data issuedGrant `json:"data"`
		}
		Expect(json.Unmarshal(output, &response)).To(Succeed())
		Expect(response.Data.Principal).NotTo(BeEmpty())
		Expect(response.Data.Operation).To(Equal(key))
		response.Data.InputPath = path
		Expect(response.Data.Scope.Actions).To(ConsistOf("project.read", "version.read", "source.read", "import.publish", "job.read"))
		return response.Data
	}
	grant := issue()
	credential, e := client.LoadCredential(grant.Path)
	Expect(e).NotTo(HaveOccurred())
	// Omitted source_selection stays the same remembered choice after its
	// frozen JSON representation becomes null. It must not rotate the secret.
	var remembered struct {
		Data map[string]any `json:"data"`
	}
	Expect(json.Unmarshal(cli(t, 0, append([]string{"auth", "grant", "create", "--input", grant.InputPath}, ownerFlags...)...), &remembered)).To(Succeed())
	Expect(remembered.Data["reused"]).To(Equal(true))
	Expect(remembered.Data["operation_id"]).To(Equal(grant.Operation))
	Expect(remembered.Data["principal_id"]).To(Equal(grant.Principal))
	Expect(remembered.Data["credential_path"]).To(Equal(grant.Path))
	unchanged, e := client.LoadCredential(grant.Path)
	Expect(e).NotTo(HaveOccurred())
	Expect(unchanged.Token == credential.Token).To(BeTrue())
	api, e := client.New(daemon.id.Endpoint, selected.Profile, credential)
	Expect(e).NotTo(HaveOccurred())
	flags := append([]string{}, ownerFlags...)
	for n, value := range flags {
		if value == "--credential" {
			flags[n+1] = grant.Path
		}
	}
	input := filepath.Join(t.TempDir(), "scoped.html")
	content := []byte("<!doctype html><title>Scoped import</title><p>Complete immutable scoped bytes.</p>")
	Expect(os.WriteFile(input, content, 0600)).To(Succeed())
	operation := id()
	args := append([]string{"import", input, "--project", identity.UnfiledProjectID, "--new-document", "--operation-id", operation}, flags...)
	type imported struct {
		Operation string     `json:"operation_id"`
		Document  string     `json:"document_id"`
		Version   string     `json:"version_id"`
		Job       domain.Job `json:"job_outcome"`
	}
	var first struct {
		Data imported `json:"data"`
	}
	Expect(json.Unmarshal(cli(t, 0, args...), &first)).To(Succeed())
	Expect(first.Data.Operation).To(Equal(operation))
	Expect(first.Data.Document).NotTo(BeEmpty())
	Expect(first.Data.Version).NotTo(BeEmpty())
	Expect(first.Data.Job.ID).NotTo(BeEmpty())
	Expect(first.Data.Job.State).To(Equal("completed"))
	state, e := clientstate.New(root)
	Expect(e).NotTo(HaveOccurred())
	journal, e := state.Load(operation)
	Expect(e).NotTo(HaveOccurred())
	Expect(journal.Identity.PrincipalID).To(Equal(grant.Principal))
	Expect(journal.Response).NotTo(BeNil())
	Expect(journal.Response.Status).To(Equal(202))
	var creation struct {
		Job string `json:"job_id"`
	}
	Expect(json.Unmarshal(journal.Response.Body, &creation)).To(Succeed())
	Expect(creation.Job).To(Equal(first.Data.Job.ID))
	jobIDs := func() []string {
		GinkgoHelper()
		var page struct {
			Items []domain.Job `json:"items"`
			Next  *string      `json:"next_cursor"`
		}
		_, err := api.JSON(ctx, "GET", "/api/v1/jobs", "", nil, &page)
		Expect(err).NotTo(HaveOccurred())
		Expect(page.Next).To(BeNil())
		ids := make([]string, 0, len(page.Items))
		for _, job := range page.Items {
			ids = append(ids, job.ID)
		}
		return ids
	}
	beforeJobs := jobIDs()
	Expect(beforeJobs).To(ContainElement(first.Data.Job.ID))
	// Recovery uses the frozen intent and receipt even after local input is gone.
	Expect(os.Remove(input)).To(Succeed())
	var replay struct {
		Data imported `json:"data"`
	}
	Expect(json.Unmarshal(cli(t, 0, args...), &replay)).To(Succeed())
	Expect(replay.Data).To(Equal(first.Data))
	Expect(jobIDs()).To(ConsistOf(beforeJobs))
	var versions struct {
		Items []struct {
			ID string `json:"version_id"`
		} `json:"items"`
		Next *string `json:"next_cursor"`
	}
	_, e = ownerAPI.JSON(ctx, "GET", "/api/v1/documents/"+first.Data.Document+"/versions", "", nil, &versions)
	Expect(e).NotTo(HaveOccurred())
	Expect(versions.Items).To(HaveLen(1))
	Expect(versions.Items[0].ID).To(Equal(first.Data.Version))
	Expect(versions.Next).To(BeNil())
	// The newly published version was not in the captured read selection. The
	// owner's independent read verifies its bytes without expanding that scope.
	response, e := ownerAPI.Call(ctx, "GET", "/api/v1/versions/"+first.Data.Version+"/file?path=scoped.html", "", nil, nil)
	Expect(e).NotTo(HaveOccurred())
	actual, e := io.ReadAll(response.Body)
	Expect(response.Body.Close()).To(Succeed())
	Expect(e).NotTo(HaveOccurred())
	Expect(actual).To(Equal(content))
	foreign := issue()
	Expect(foreign.Principal).NotTo(Equal(grant.Principal))
	foreignCredential, e := client.LoadCredential(foreign.Path)
	Expect(e).NotTo(HaveOccurred())
	foreignAPI, e := client.New(daemon.id.Endpoint, selected.Profile, foreignCredential)
	Expect(e).NotTo(HaveOccurred())
	_, e = foreignAPI.Job(ctx, first.Data.Job.ID)
	Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue(), "foreign principal job read: %v", e)
	_, e = api.JSON(ctx, "POST", "/api/v1/jobs/"+first.Data.Job.ID+"/cancel", id(), map[string]any{}, nil)
	Expect(errors.Is(e, domain.ErrDenied)).To(BeTrue(), "cancel unexpectedly authorized: %v", e)
	response, e = api.Call(ctx, "GET", "/api/v1/jobs/"+first.Data.Job.ID, "", nil, nil)
	Expect(e).NotTo(HaveOccurred())
	match := response.Header.Get("ETag")
	Expect(response.Body.Close()).To(Succeed())
	Expect(match).NotTo(BeEmpty())
	_, e = api.Call(ctx, "POST", "/api/v1/jobs/"+first.Data.Job.ID+"/resume", id(),
		bytes.NewReader([]byte(`{"decision":"continue","expected_attempt_generation":1}`)),
		map[string]string{"Content-Type": "application/json", "If-Match": match})
	Expect(errors.Is(e, domain.ErrDenied)).To(BeTrue(), "resume unexpectedly authorized: %v", e)
	job, e := api.Job(ctx, first.Data.Job.ID)
	Expect(e).NotTo(HaveOccurred())
	Expect(job).To(Equal(first.Data.Job))
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(selected)...)...)
	daemon.wait(t)
})
