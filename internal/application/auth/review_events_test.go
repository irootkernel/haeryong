package auth_test

import (
	"context"
	"database/sql"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"path/filepath"
	"strings"
	"time"
)

var _ = It("C1 Review mixed event batches equal individual filtering for owner and scoped readers", Label("persistence"), func() {
	ctx := context.Background()
	selection := service.Selection{Profile: "review-events", DataDir: filepath.Join(GinkgoT().TempDir(), "library")}
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
	id := func() string { value, err := service.NewID(); Expect(err).NotTo(HaveOccurred()); return value }
	auth := appauth.New(store)
	pair, err := auth.CreatePairing(ctx, "http://127.0.0.1:9270", "review events", "fixture")
	Expect(err).NotTo(HaveOccurred())
	_, err = auth.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Action: "approve", Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: id()})
	Expect(err).NotTo(HaveOccurred())
	owner, err := auth.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	Expect(err).NotTo(HaveOccurred())
	health, err := store.Health(ctx)
	Expect(err).NotTo(HaveOccurred())
	project := health.Library.UnfiledProjectID
	visible, hidden, visibleDoc, hiddenDoc := id(), id(), id(), id()
	Expect(store.Write(ctx, func(tx app.WriteTx) error {
		for _, row := range []struct{ version, doc string }{{visible, visibleDoc}, {hidden, hiddenDoc}} {
			if err := tx.CreateDocument(domain.Document{ID: row.doc, ProjectID: project, Title: "Review events"}); err != nil {
				return err
			}
			set := id()
			if err := tx.CreateContentSet(set, strings.Repeat("a", 64)); err != nil {
				return err
			}
			if err := tx.SealContentSet(set); err != nil {
				return err
			}
			if err := tx.PutVersion(domain.Version{ID: row.version, DocumentID: row.doc, ContentSetID: set, Title: "Events", ViewerProfile: "html_js_v1", SourceBasis: "unverified"}); err != nil {
				return err
			}
			if err := tx.PublishVersion(row.version, 1); err != nil {
				return err
			}
		}
		return nil
	})).To(Succeed())
	db, err := sql.Open("sqlite", filepath.Join(selection.DataDir, "storage", "library.sqlite"))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(db.Close()).To(Succeed()) })
	_, err = db.Exec(`UPDATE documents SET lifecycle='trash' WHERE document_id=?`, hiddenDoc)
	Expect(err).NotTo(HaveOccurred())
	scoped, err := sec.Secret()
	Expect(err).NotTo(HaveOccurred())
	principal := id()
	scope := domain.CredentialScope{Kind: "workflow", Actions: []string{"project.read", "version.read"}, ProjectIDs: []string{project}, VersionIDs: []string{visible}}
	var epoch string
	var after int64
	Expect(store.Write(ctx, func(tx app.WriteTx) error {
		authState, err := tx.AuthState()
		if err != nil {
			return err
		}
		current, err := tx.Scope(project)
		if err != nil {
			return err
		}
		if err := tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: principal, BindingID: principal, Active: true, Scope: scope, Revision: 1}); err != nil {
			return err
		}
		if err := tx.PutScopedCredential(domain.ScopedCredential{ID: principal, PrincipalID: principal, TokenHash: sec.Verifier("workflow", scoped), AuthEpoch: authState.AuthEpoch, Scope: scope, Permissions: map[string]int64{project: current.Project.PermissionsRevision}, ConfigRevision: current.ConfigRevision, CreatedAt: time.Now().UTC().Truncate(time.Millisecond), Revision: 1}); err != nil {
			return err
		}
		epoch, after, err = tx.EventHead()
		if err != nil {
			return err
		}
		for _, row := range []struct{ resource, id, kind string }{
			{"review", visible, "review.updated"}, {"review", hidden, "review.updated"},
			{"document", hiddenDoc, "document.updated"}, {"version", hidden, "version.updated"},
			{"review", visible, "review.updated"},
		} {
			if err := tx.AppendEvent(row.resource, row.id, row.kind, 7, []byte(`{"private":"audit"}`)); err != nil {
				return err
			}
		}
		return nil
	})).To(Succeed())
	raw, err := store.Events(ctx, epoch, after, 64)
	Expect(err).NotTo(HaveOccurred())
	Expect(raw).To(HaveLen(5))
	for _, token := range []string{owner.Token, scoped} {
		expected := make([]domain.Event, len(raw))
		for i, event := range raw {
			expected[i], err = auth.FilterEvent(ctx, token, event)
			Expect(err).NotTo(HaveOccurred())
		}
		batch, err := auth.Events(ctx, token, epoch, after)
		Expect(err).NotTo(HaveOccurred())
		Expect(batch).To(Equal(expected))
		for i, event := range batch {
			if token == owner.Token && (i == 0 || i == 4) {
				Expect(event).To(Equal(raw[i]))
			} else {
				Expect(event).To(Equal(domain.Event{StreamEpoch: raw[i].StreamEpoch, Sequence: raw[i].Sequence, ResourceKind: "library", Kind: "library.invalidated"}))
			}
		}
	}
})
