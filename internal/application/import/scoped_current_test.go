package importer_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	config "haeryong.local/haeryong/internal/application/configuration"
	importer "haeryong.local/haeryong/internal/application/import"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

func TestAcceptedScopedSealUsesCapturedAndCurrentAuthority(t *testing.T) {
	for _, change := range []string{"binding_deactivated", "action_restricted", "project_permissions_changed"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t)
			draft, secondDraft, principal, binding := key(t), key(t), key(t), key(t)
			token, e := sec.Secret()
			if e != nil {
				t.Fatal(e)
			}
			scope := domain.CredentialScope{Kind: "workflow", Actions: []string{"draft.write"}, ProjectIDs: []string{f.project}, DraftIDs: []string{draft}}
			p := domain.WorkflowPrincipal{ID: principal, BindingID: binding, Active: true, Scope: scope}
			var permissions int64
			if e = f.store.Write(ctx, func(tx app.WriteTx) error {
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
			}); e != nil {
				t.Fatal(e)
			}
			// Expanding the stable binding cannot expand an already captured token.
			p.Scope.DraftIDs = []string{draft, secondDraft}
			if e = f.store.Write(ctx, func(tx app.WriteTx) error { return tx.PutWorkflowPrincipal(p) }); e != nil {
				t.Fatal(e)
			}
			target := encodedTest(map[string]any{"project_id": f.project, "document_id": nil, "draft_id": secondDraft})
			if _, e = f.core.Create(ctx, token, key(t), importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: target}); !errors.Is(e, domain.ErrDenied) {
				t.Fatal("current expansion widened captured credential", e)
			}
			target = encodedTest(map[string]any{"project_id": f.project, "document_id": nil, "draft_id": draft})
			created, e := f.core.Create(ctx, token, key(t), importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: target})
			if e != nil {
				t.Fatal(e)
			}
			var handle struct {
				ID string `json:"upload_id"`
			}
			if e = json.Unmarshal(created.Body, &handle); e != nil || handle.ID == "" {
				t.Fatal("created upload response", e)
			}
			upload := handle.ID
			content := []byte("scoped pending bytes")
			hash := sha256.Sum256(content)
			length := int64(len(content))
			ack, e := f.core.Put(ctx, token, upload, importer.Chunk{Path: "first.html", Kind: "file", Hash: hex.EncodeToString(hash[:]), Length: &length, Final: true}, bytes.NewReader(content))
			if e != nil {
				t.Fatal(e)
			}
			cp := ack.Checkpoint
			cp.Generation++
			cp.Complete = true
			accepted, e := f.core.Complete(ctx, token, upload, key(t), cp)
			if e != nil {
				t.Fatal(e)
			}
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
					t.Fatal(err)
				}
				response, err := core.Plan(ctx, f.token, key(t), config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: current.ConfigRevision, Changes: map[string]any{"source": map[string]any{"default_retention": "selected_snapshot"}}})
				if err != nil {
					t.Fatal(err)
				}
				var plan domain.ConfigurationPlan
				if err = json.Unmarshal(response.Body, &plan); err != nil {
					t.Fatal(err)
				}
				_, e = core.Commit(ctx, f.token, plan.ID, "", config.CommitRequest{ExpectedRevision: current.ConfigRevision, Confirmed: true})
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.core.Create(ctx, token, key(t), importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: target}); e == nil {
				t.Fatal("new transfer ignored current restriction")
			}
			if e = f.core.Process(ctx, jobID); !errors.Is(e, domain.ErrDenied) {
				t.Fatal("accepted attempt ignored current restriction", e)
			}
			job, e := f.store.Job(ctx, jobID)
			if e != nil || job.State != "failed" {
				t.Fatal("restricted job published", job, e)
			}
			u, e := f.store.Upload(ctx, upload)
			if e != nil || u.State != "open" || u.ContentSetID != "" {
				t.Fatal("denied attempt made sealed bytes visible", u, e)
			}
			if change == "project_permissions_changed" {
				project, e := f.store.Project(ctx, f.project)
				if e != nil || project.PermissionsRevision != permissions+1 {
					t.Fatal("actual policy switch did not advance permission revision", e)
				}
			}
		})
	}
}
