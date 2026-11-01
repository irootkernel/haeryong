package clientstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/client"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/transfer"
	sec "haeryong.local/haeryong/internal/security/session"
)

func workflowID() string { id, e := service.NewID(); Expect(e).NotTo(HaveOccurred()); return id }

var _ = It("GGR04 private pre-dispatch journal preserves one secret and refuses changed library owner request or unsafe file replacement", Label("unit"), func() {
	s, e := New(GinkgoT().TempDir())
	Expect(e).NotTo(HaveOccurred())
	secret, e := sec.Secret()
	Expect(e).NotTo(HaveOccurred())
	identity := transfer.Identity{LibraryID: "library", PrincipalID: "owner"}
	input := filepath.Join(s.Root, "selection.json")
	binding, e := s.WorkflowBinding(context.Background(), identity, input, workflowID())
	Expect(e).NotTo(HaveOccurred())
	again, e := s.WorkflowBinding(context.Background(), identity, input, workflowID())
	Expect(e).NotTo(HaveOccurred())
	Expect(again).To(Equal(binding))
	other := identity
	other.LibraryID = "other-library"
	separate, e := s.WorkflowBinding(context.Background(), other, input, workflowID())
	Expect(e).NotTo(HaveOccurred())
	Expect(separate).NotTo(Equal(binding))
	j := WorkflowJournal{Action: "create", OperationID: workflowID(), Identity: identity, InputPath: input, Request: appauth.WorkflowGrantRequest{BindingID: binding, CredentialID: workflowID(), ProjectID: workflowID(), Workflow: "generation", TokenVerifier: sec.Verifier("workflow", secret)}, Credential: client.Credential{LibraryID: identity.LibraryID, Profile: "test", Endpoint: "http://127.0.0.1:9270", Token: secret}}
	Expect(s.SaveWorkflow(j)).To(Succeed())
	saved, e := s.LoadWorkflow(j.OperationID)
	Expect(e).NotTo(HaveOccurred())
	Expect(saved.Credential.Token == secret).To(BeTrue())
	Expect(saved.Response).To(BeNil())
	info, e := os.Stat(filepath.Join(s.Root, "journals", "workflow-"+j.OperationID+".json"))
	Expect(e).NotTo(HaveOccurred())
	Expect(info.Mode().Perm() & 0077).To(BeZero())
	for _, mode := range []string{"library", "owner", "request", "secret"} {
		changed := j
		switch mode {
		case "library":
			changed.Identity.LibraryID = "other"
			changed.Credential.LibraryID = "other"
		case "owner":
			changed.Identity.PrincipalID = "other"
		case "request":
			changed.Request.ProjectID = workflowID()
		case "secret":
			changed.Credential.Token, _ = sec.Secret()
			changed.Request.TokenVerifier = sec.Verifier("workflow", changed.Credential.Token)
		}
		Expect(s.SaveWorkflow(changed)).To(HaveOccurred(), mode)
	}
	Expect(s.ActivateWorkflow(j)).To(HaveOccurred(), "unknown issuance is not an active credential")
	Expect(os.Chmod(filepath.Join(s.Root, "journals", "workflow-"+j.OperationID+".json"), 0644)).To(Succeed())
	_, e = s.LoadWorkflow(j.OperationID)
	Expect(e).To(HaveOccurred())
	Expect(errors.Is(e, os.ErrNotExist)).To(BeFalse())
})
