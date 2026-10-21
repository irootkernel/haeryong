package auth_test

import (
	"context"
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	appauth "haeryong.local/haeryong/internal/application/auth"
	config "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// The plan fixture validates event delivery, not host-root containment.
type sourceEventPaths struct{}

func (sourceEventPaths) Canonical(path string, _ bool) (string, error) { return path, nil }

var _ = It("delivers project source scope events after a committed plan while masking hidden targets", Label("persistence"), func() {
	ctx := context.Background()
	selection := service.Selection{Profile: "events", DataDir: GinkgoT().TempDir() + "/library"}
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
	pair, err := auth.CreatePairing(ctx, "http://127.0.0.1:9270", "event owner", "fixture")
	Expect(err).NotTo(HaveOccurred())
	_, err = auth.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Action: "approve", Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: id()})
	Expect(err).NotTo(HaveOccurred())
	poll, err := auth.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	Expect(err).NotTo(HaveOccurred())
	project := id()
	Expect(store.Write(ctx, func(tx app.WriteTx) error { return tx.CreateProject(domain.Project{ID: project, Name: "Events"}) })).To(Succeed())
	var epoch string
	var after int64
	Expect(store.Write(ctx, func(tx app.WriteTx) error { var err error; epoch, after, err = tx.EventHead(); return err })).To(Succeed())
	core := config.New(store, auth, sourceEventPaths{})
	baseline, err := core.Read(ctx, poll.Token)
	Expect(err).NotTo(HaveOccurred())
	response, err := core.Plan(ctx, poll.Token, id(), config.PlanRequest{Scope: config.Scope{Kind: "project", ProjectID: project}, ExpectedRevision: baseline.ConfigRevision, Changes: map[string]any{"sources": []any{map[string]any{"source_id": id(), "kind": "git", "root_path": "/fixture/repository", "repo_id": id(), "retention_mode": "reference_only", "provider_ids": []any{}}}}})
	Expect(err).NotTo(HaveOccurred())
	var plan domain.ConfigurationPlan
	Expect(json.Unmarshal(response.Body, &plan)).To(Succeed())
	_, err = core.Commit(ctx, poll.Token, plan.ID, id(), config.CommitRequest{ExpectedRevision: baseline.ConfigRevision, Confirmed: true})
	Expect(err).NotTo(HaveOccurred())
	events, err := store.Events(ctx, epoch, after, 100)
	Expect(err).NotTo(HaveOccurred())
	kinds := []string{}
	var scopeEvent domain.Event
	for _, event := range events {
		filtered, err := auth.FilterEvent(ctx, poll.Token, event)
		Expect(err).NotTo(HaveOccurred())
		if event.Kind == "source.scope_changed" || event.Kind == "project.updated" {
			Expect(filtered.Kind).To(Equal(event.Kind))
			Expect(filtered.ResourceID).To(Equal(project))
			Expect(filtered.Body).To(BeEmpty())
			kinds = append(kinds, filtered.Kind)
		}
		if event.Kind == "source.scope_changed" {
			Expect(filtered.ResourceKind).To(Equal("source"))
			scopeEvent = event
		}
	}
	Expect(kinds).To(ConsistOf("source.scope_changed", "project.updated"))
	assertMasked := func(event domain.Event, token string) {
		filtered, err := auth.FilterEvent(ctx, token, event)
		Expect(err).NotTo(HaveOccurred())
		Expect(filtered.Kind).To(Equal("library.invalidated"))
		Expect(filtered.ResourceID).To(BeEmpty())
		Expect(filtered.Body).To(BeEmpty())
	}
	hidden := scopeEvent
	hidden.ResourceID = id()
	assertMasked(hidden, poll.Token)
	for _, kind := range []string{"source.changed", "source.availability_changed"} {
		versionEvent := scopeEvent
		versionEvent.Kind = kind
		assertMasked(versionEvent, poll.Token) // A project id never substitutes for a version body.
	}

	// Current scoped readers keep the existing source-event invalidation.
	token, err := sec.Secret()
	Expect(err).NotTo(HaveOccurred())
	principal := id()
	scope := domain.CredentialScope{Kind: "workflow", Actions: []string{"project.read", "version.read", "source.read"}, ProjectIDs: []string{project}}
	Expect(store.Write(ctx, func(tx app.WriteTx) error {
		state, err := tx.AuthState()
		if err != nil {
			return err
		}
		if err := tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: principal, BindingID: principal, Active: true, Scope: scope, Revision: 1}); err != nil {
			return err
		}
		current, err := tx.Scope(project)
		if err != nil {
			return err
		}
		return tx.PutScopedCredential(domain.ScopedCredential{ID: principal, PrincipalID: principal, TokenHash: sec.Verifier("workflow", token), AuthEpoch: state.AuthEpoch, Scope: scope, Permissions: map[string]int64{project: current.Project.PermissionsRevision}, ConfigRevision: current.ConfigRevision, CreatedAt: time.Now().UTC().Truncate(time.Millisecond), Revision: 1})
	})).To(Succeed())
	assertMasked(scopeEvent, token)
})
