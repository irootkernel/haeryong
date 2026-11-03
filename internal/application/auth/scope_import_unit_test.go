package auth

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// This port exercises route authorization only; it is not a persisted job or
// old-token integration fixture. GGR04 covers normal CLI/daemon/SQLite import.
type importJobScopeTx struct {
	app.WriteTx
	job   domain.Job
	reads int
}

func (t *importJobScopeTx) Job(string) (domain.Job, error) {
	t.reads++
	return t.job, nil
}

var _ = It("GGR05 import job reads retain principal purpose target and captured-scope boundaries", Label("unit"), func() {
	scope := domain.CredentialScope{Kind: "workflow", Actions: workflowActions("import"), ProjectIDs: []string{"selected"}}
	actor := domain.Session{PrincipalID: "importer", Scope: scope}
	job := func(principal, purpose, project string) domain.Job {
		body, err := json.Marshal(map[string]any{"principal_id": principal, "purpose": purpose, "target": map[string]any{"project_id": project, "document_id": nil}})
		Expect(err).NotTo(HaveOccurred())
		return domain.Job{ID: "job", Kind: "import", Scope: body}
	}
	tx := &importJobScopeTx{job: job(actor.PrincipalID, "result", "selected")}
	read := domain.Request{Method: "GET", Target: "/api/v1/jobs/job"}
	Expect(authorizeScope(tx, actor, read)).To(Succeed())
	Expect(tx.reads).To(Equal(1))
	for _, mismatch := range []struct {
		principal, purpose, project string
		expected                    error
	}{
		{"other", "result", "selected", domain.ErrNotFound},
		{"importer", "context", "selected", domain.ErrDenied},
		{"importer", "result", "other", domain.ErrDenied},
	} {
		tx.job = job(mismatch.principal, mismatch.purpose, mismatch.project)
		Expect(authorizeScope(tx, actor, read)).To(MatchError(mismatch.expected))
	}
	tx.job = job(actor.PrincipalID, "result", "selected")
	for _, action := range []string{"cancel", "resume"} {
		before := tx.reads
		Expect(authorizeScope(tx, actor, domain.Request{Method: "POST", Target: "/api/v1/jobs/job/" + action})).To(MatchError(domain.ErrDenied))
		Expect(tx.reads).To(Equal(before))
	}
	// An immutable pre-fix credential scope does not acquire job.read merely
	// because its current principal now has the newly issued import actions.
	captured := scope
	captured.Actions = []string{"project.read", "version.read", "source.read", "import.publish"}
	actor.Scope = RestrictScope(captured, scope)
	Expect(actor.Scope.Actions).To(Equal(captured.Actions))
	Expect(actor.Scope.ProjectIDs).To(Equal(captured.ProjectIDs))
	before := tx.reads
	Expect(authorizeScope(tx, actor, read)).To(MatchError(domain.ErrDenied))
	Expect(tx.reads).To(Equal(before))
	Expect(scope.Actions).To(ConsistOf("project.read", "version.read", "source.read", "import.publish", "job.read"))
})
