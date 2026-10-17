package importer_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"time"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	config "haeryong.local/haeryong/internal/application/configuration"
	importer "haeryong.local/haeryong/internal/application/import"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

var _ = It("TestAcceptedScopedSealUsesCapturedAndCurrentAuthority", Label("persistence"), func() {
	t := GinkgoT()

	for _, change := range []string{"binding_deactivated", "action_restricted", "project_permissions_changed"} {
		func() {
			By(change)
			f := newFixture(t)
			draft, secondDraft, principal, binding := key(t), key(t), key(t), key(t)
			token, e := sec.Secret()
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			scope := domain.CredentialScope{Kind: "workflow", Actions: []string{"draft.write"}, ProjectIDs: []string{f.project}, DraftIDs: []string{draft}}
			p := domain.WorkflowPrincipal{ID: principal, BindingID: binding, Active: true, Scope: scope}
			var permissions int64
			{
				e = f.store.Write(ctx, func(tx app.WriteTx) error {
					for _, id := range []string{draft, secondDraft} {
						if _, e := tx.MutateResource(domain.Resource{Kind: "draft", ID: id, Value: encodedTest(map[string]any{"project_id": f.project, "document_id": nil, "state": "ready"})}, 0, "resource.invalidated"); e != nil {
							return e
						}
					}
					if e := tx.PutWorkflowPrincipal(p); e != nil {
						return e
					}
					library, e := tx.Library()
					if e != nil {
						return e
					}
					project, e := tx.Scope(f.project)
					if e != nil {
						return e
					}
					permissions = project.Project.PermissionsRevision
					return tx.PutScopedCredential(domain.ScopedCredential{ID: key(t), PrincipalID: principal, TokenHash: sec.Verifier("workflow", token), AuthEpoch: library.AuthEpoch, Scope: scope, Permissions: map[string]int64{f.project: permissions}, ConfigRevision: project.ConfigRevision, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)})
				})
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}

			// Expanding the stable binding cannot expand an already captured token.
			p.Scope.DraftIDs = []string{draft, secondDraft}
			{
				e = f.store.Write(ctx, func(tx app.WriteTx) error { return tx.PutWorkflowPrincipal(p) })
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}

			target := encodedTest(map[string]any{"project_id": f.project, "document_id": nil, "draft_id": secondDraft})
			{
				_, e = f.core.Create(ctx, token, key(t), importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: target})
				Expect(!errors.Is(e, domain.ErrDenied)).To(BeFalse(), func() string { return fmt.Sprint("current expansion widened captured credential", e) })
			}

			target = encodedTest(map[string]any{"project_id": f.project, "document_id": nil, "draft_id": draft})
			created, e := f.core.Create(ctx, token, key(t), importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: target})
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			var handle struct {
				ID string `json:"upload_id"`
			}
			{
				e = json.Unmarshal(created.Body, &handle)
				Expect(e != nil || handle.ID == "").To(BeFalse(), func() string { return fmt.Sprint("created upload response", e) })
			}

			upload := handle.ID
			content := []byte("scoped pending bytes")
			hash := sha256.Sum256(content)
			length := int64(len(content))
			ack, e := f.core.Put(ctx, token, upload, importer.Chunk{Path: "first.html", Kind: "file", Hash: hex.EncodeToString(hash[:]), Length: &length, Final: true}, bytes.NewReader(content))
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			cp := ack.Checkpoint
			cp.Generation++
			cp.Complete = true
			accepted, e := f.core.Complete(ctx, token, upload, key(t), cp)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			jobID := decoded(t, accepted)["job_id"]
			switch change {
			case "binding_deactivated":
				p.Active = false
				e = f.store.Write(ctx, func(tx app.WriteTx) error { return tx.PutWorkflowPrincipal(p) })
			case "action_restricted":
				p.Scope.Actions = []string{}
				e = f.store.Write(ctx, func(tx app.WriteTx) error { return tx.PutWorkflowPrincipal(p) })
			case "project_permissions_changed":
				core := config.New(f.store, f.auth, nil)
				current, err := core.Read(ctx, f.token)
				if err != nil {
					Fail(fmt.Sprint(err))
				}
				response, err := core.Plan(ctx, f.token, key(t), config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: current.ConfigRevision, Changes: map[string]any{"source": map[string]any{"default_retention": "selected_snapshot"}}})
				if err != nil {
					Fail(fmt.Sprint(err))
				}
				var plan domain.ConfigurationPlan
				if err = json.Unmarshal(response.Body, &plan); err != nil {
					Fail(fmt.Sprint(err))
				}
				_, e = core.Commit(ctx, f.token, plan.ID, "", config.CommitRequest{ExpectedRevision: current.ConfigRevision, Confirmed: true})
			}
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			{

				_, e = f.core.Create(ctx, token, key(t), importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: target})
				Expect(e == nil).To(BeFalse(), func() string { return fmt.Sprint("new transfer ignored current restriction") })
			}
			{

				e = f.core.Process(ctx, jobID)
				Expect(!errors.Is(e, domain.ErrDenied)).To(BeFalse(), func() string { return fmt.Sprint("accepted attempt ignored current restriction", e) })
			}

			job, e := f.store.Job(ctx, jobID)
			Expect(e != nil || job.State != "failed").To(BeFalse(), func() string { return fmt.Sprint("restricted job published", job, e) })

			u, e := f.store.Upload(ctx, upload)
			Expect(e != nil || u.State != "open" || u.ContentSetID != "").To(BeFalse(), func() string { return fmt.Sprint("denied attempt made sealed bytes visible", u, e) })

			if change == "project_permissions_changed" {
				project, e := f.store.Project(ctx, f.project)
				Expect(e != nil || project.PermissionsRevision != permissions+1).To(BeFalse(), func() string { return fmt.Sprint("actual policy switch did not advance permission revision", e) })

			}
		}()
	}
})
