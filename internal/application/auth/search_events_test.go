package auth_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

var _ = It("SC07 masks replay/live search events exactly like Check and keeps maintenance jobs owner-only", Label("persistence"), func() {
	ctx := context.Background()
	selection := service.Selection{Profile: "search-events", DataDir: filepath.Join(GinkgoT().TempDir(), "library")}
	_, err := service.Initialize(selection)
	Expect(err).NotTo(HaveOccurred())
	writer, err := service.OpenWriter(selection)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(writer.Close()).To(Succeed()) })
	store := foundation.New(nil)
	_, closeStore, err := store.Prepare(ctx, writer.Profile)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(closeStore()).To(Succeed()) })
	Expect(writer.MarkStorageReady(nil)).To(Succeed())
	id := func() string { v, e := service.NewID(); Expect(e).NotTo(HaveOccurred()); return v }
	auth := appauth.New(store)
	pair, err := auth.CreatePairing(ctx, "http://127.0.0.1:9270", "search events", "fixture")
	Expect(err).NotTo(HaveOccurred())
	_, err = auth.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Action: "approve", Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: id()})
	Expect(err).NotTo(HaveOccurred())
	poll, err := auth.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	Expect(err).NotTo(HaveOccurred())
	h, err := store.Health(ctx)
	Expect(err).NotTo(HaveOccurred())
	project := h.Library.UnfiledProjectID
	hiddenProject := id()
	Expect(store.Write(ctx, func(tx app.WriteTx) error { return tx.CreateProject(domain.Project{ID: hiddenProject, Name: "Hidden"}) })).To(Succeed())
	version, doc, set := id(), id(), id()
	var epoch string
	var after int64
	Expect(store.Write(ctx, func(tx app.WriteTx) error {
		var e error
		epoch, after, e = tx.EventHead()
		if e != nil {
			return e
		}
		if e = tx.CreateDocument(domain.Document{ID: doc, ProjectID: project, Title: "Events"}); e != nil {
			return e
		}
		if e = tx.CreateContentSet(set, strings.Repeat("a", 64)); e != nil {
			return e
		}
		if e = tx.SealContentSet(set); e != nil {
			return e
		}
		if e = tx.PutVersion(domain.Version{ID: version, DocumentID: doc, ContentSetID: set, Title: "Events", ViewerProfile: "html_js_v1", SourceBasis: "unverified"}); e != nil {
			return e
		}
		return tx.PublishVersion(version, 1)
	})).To(Succeed())
	var index domain.SearchIndex
	Expect(store.Write(ctx, func(tx app.WriteTx) error { var e error; index, e = tx.SearchIndex(version); return e })).To(Succeed())
	all, err := store.Events(ctx, epoch, after, 64)
	Expect(err).NotTo(HaveOccurred())
	var event domain.Event
	for _, v := range all {
		if v.Kind == "search.index_updated" {
			event = v
		}
	}
	Expect(event.ResourceID).To(Equal(version))
	Expect(event.Body).To(BeEmpty())
	type reader struct {
		token   string
		visible bool
	}
	readers := []reader{{poll.Token, true}}
	for _, scope := range []domain.CredentialScope{
		{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{project}, VersionIDs: []string{version}},
		{Kind: "workflow", Actions: []string{"version.read", "job.read"}, ProjectIDs: []string{project}, VersionIDs: []string{version}},
		{Kind: "workflow", Actions: []string{"version.read", "job.read", "import.publish"}, ProjectIDs: []string{project}, VersionIDs: []string{version}},
		{Kind: "workflow", Actions: []string{"version.read", "job.read"}, ProjectIDs: []string{project}},
		{Kind: "workflow", Actions: []string{"version.read", "job.read"}, VersionIDs: []string{version}},
		{Kind: "workflow", Actions: []string{"job.read"}, ProjectIDs: []string{project}, VersionIDs: []string{version}},
		{Kind: "workflow", Actions: []string{"version.read", "job.read"}, ProjectIDs: []string{hiddenProject}, VersionIDs: []string{version}},
	} {
		token, e := sec.Secret()
		Expect(e).NotTo(HaveOccurred())
		principal := id()
		var eligible bool
		Expect(store.Write(ctx, func(tx app.WriteTx) error {
			a, e := tx.AuthState()
			if e != nil {
				return e
			}
			current, e := tx.Scope(project)
			if e != nil {
				return e
			}
			if e = tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: principal, BindingID: principal, Active: true, Scope: scope, Revision: 1}); e != nil {
				return e
			}
			permissions := map[string]int64{}
			for _, projectID := range scope.ProjectIDs {
				p, e := tx.Scope(projectID)
				if e != nil {
					return e
				}
				permissions[projectID] = p.Project.PermissionsRevision
			}
			if e = tx.PutScopedCredential(domain.ScopedCredential{ID: principal, PrincipalID: principal, TokenHash: sec.Verifier("workflow", token), AuthEpoch: a.AuthEpoch, Scope: scope, Permissions: permissions, ConfigRevision: current.ConfigRevision, CreatedAt: time.Now().UTC().Truncate(time.Millisecond), Revision: 1}); e != nil {
				return e
			}
			_, e = source.Check(tx, domain.Session{Scope: scope}, version)
			eligible = e == nil
			return nil
		})).To(Succeed())
		readers = append(readers, reader{token, eligible})
		// Even a maintenance scope containing a matching principal/valid upload target
		// cannot make system job progress a workflow-readable body.
		Expect(store.Write(ctx, func(tx app.WriteTx) error {
			j, e := tx.Job(index.JobID)
			if e != nil {
				return e
			}
			j.Scope, _ = json.Marshal(map[string]any{"principal_id": principal, "purpose": "result", "target": map[string]any{"project_id": project, "document_id": doc}})
			return tx.ConfigureJobScope(j)
		})).To(Succeed())
		_, e = auth.Job(ctx, token, index.JobID)
		Expect(e).To(HaveOccurred())
		jobs, e := auth.Jobs(ctx, token)
		hasJobRead := false
		for _, action := range scope.Actions {
			if action == "job.read" {
				hasJobRead = true
			}
		}
		if hasJobRead {
			Expect(e).NotTo(HaveOccurred())
			Expect(jobs).To(BeEmpty())
		} else {
			Expect(e).To(HaveOccurred())
		}
	}
	for _, r := range readers {
		visible, e := auth.FilterEvent(ctx, r.token, event)
		Expect(e).NotTo(HaveOccurred())
		replay, e := auth.Events(ctx, r.token, epoch, after)
		Expect(e).NotTo(HaveOccurred())
		found := false
		for _, v := range replay {
			if v.Sequence == event.Sequence {
				Expect(v.Kind).To(Equal(visible.Kind))
				found = true
			}
		}
		Expect(found).To(BeTrue())
		if r.visible {
			Expect(visible.Kind).To(Equal("search.index_updated"))
			Expect(visible.ResourceID).To(Equal(version))
		} else {
			Expect(visible.Kind).To(Equal("library.invalidated"))
			Expect(visible.ResourceID).To(BeEmpty())
			Expect(visible.Revision).To(BeZero())
		}
		Expect(visible.Body).To(BeEmpty())
	}
	// A pending immutable version is not readable even to the owner: Check
	// requires publication, independently of lifecycle and scope.
	unpublished := id()
	Expect(store.Write(ctx, func(tx app.WriteTx) error {
		return tx.PutVersion(domain.Version{ID: unpublished, DocumentID: doc, BaseVersionID: version, ContentSetID: set, Title: "Pending", ViewerProfile: "html_js_v1", SourceBasis: "unverified"})
	})).To(Succeed())
	hiddenEvent := event
	hiddenEvent.ResourceID = unpublished
	masked, e := auth.FilterEvent(ctx, poll.Token, hiddenEvent)
	Expect(e).NotTo(HaveOccurred())
	Expect(masked.Kind).To(Equal("library.invalidated"))
	Expect(masked.ResourceID).To(BeEmpty())
	db, err := sql.Open("sqlite", filepath.Join(selection.DataDir, "storage", "library.sqlite"))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(db.Close()).To(Succeed()) })
	for _, lifecycle := range []string{"archived", "trash", "purged"} {
		_, err = db.Exec(`UPDATE documents SET lifecycle=? WHERE document_id=?`, lifecycle, doc)
		Expect(err).NotTo(HaveOccurred())
		for _, r := range readers {
			v, e := auth.FilterEvent(ctx, r.token, event)
			Expect(e).NotTo(HaveOccurred())
			if lifecycle == "archived" && r.visible {
				Expect(v.Kind).To(Equal("search.index_updated"))
			} else {
				Expect(v.Kind).To(Equal("library.invalidated"))
				Expect(v.ResourceID).To(BeEmpty())
				Expect(v.Body).To(BeEmpty())
			}
		}
	}
})
