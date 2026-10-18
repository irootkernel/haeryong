package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"time"

	. "github.com/onsi/ginkgo/v2"
	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	httpconfig "haeryong.local/haeryong/internal/adapters/http/configuration"
	httpimports "haeryong.local/haeryong/internal/adapters/http/imports"
	"haeryong.local/haeryong/internal/adapters/http/v1"
	appauth "haeryong.local/haeryong/internal/application/auth"
	config "haeryong.local/haeryong/internal/application/configuration"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"

	// The fixture retains the actual profile writer, SQLite and production router.
	// Its executor is explicitly driven by each test; no authentication is bypassed.
	"fmt"
	. "github.com/onsi/gomega"
)

type commandFixture struct {
	store         *foundation.Store
	auth          *appauth.Service
	imports       *importer.Service
	configuration *config.Service
	selection     service.Selection
	base, token   string
	session       domain.Session
}

func newCommandFixture(t GinkgoTInterface) commandFixture {
	GinkgoHelper()
	ctx := context.Background()
	s := selection(t)
	initProfile(t, s)
	writer, e := service.OpenWriter(s)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	DeferCleanup(func() { writer.Close() })
	{
		e = writer.PrepareStorageBootstrap(nil)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	store := foundation.New(nil)
	_, closeStore, e := store.Prepare(ctx, writer.Profile)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	DeferCleanup(func() { closeStore() })
	{
		e = writer.MarkStorageReady(nil)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	auth := appauth.New(store)
	imports := importer.New(store, auth, store, staging.New())
	configuration := config.New(store, auth, nil)
	server := httptest.NewUnstartedServer(nil)
	base := "http://" + server.Listener.Addr().String()
	endpoint, _ := url.Parse(base)
	handler, e := httpauth.New(auth, httpauth.Policy{Hosts: []string{endpoint.Host}, Origins: []string{base}}, productRoutes{httpconfig.New(configuration), v1.New(query.New(store, auth, store), nil, nil), httpimports.New(imports)})
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	server.Config.Handler = handler
	server.Start()
	DeferCleanup(server.Close)
	pair, e := auth.CreatePairing(ctx, base, "command boundary owner", "fixture")
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	id, _ := service.NewID()
	{
		_, e = auth.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: id, Action: "approve"})
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	poll, e := auth.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	session, e := auth.Authenticate(ctx, poll.Token)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	return commandFixture{store, auth, imports, configuration, s, base, poll.Token, session}
}

var _ = It("TestActualCLIConfigurationPostcommitExpiryRetainsPendingUntilOriginalOutcome", Label("persistence"), func() {
	t := GinkgoT()

	f := newCommandFixture(t)
	ctx := context.Background()
	future := time.Now().UTC().Add(2 * time.Hour)
	f.auth.Now = func() time.Time { return future }
	f.configuration.Now = func() time.Time { return future }
	root, flags := callerProfile(t, f.selection, f.session, f.token, f.base)
	input := filepath.Join(t.TempDir(), "expiry.json")
	{
		e := os.WriteFile(input, []byte(`{"security":{"session_absolute_seconds":3600}}`), 0600)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	planned := cli(t, 0, append([]string{"config", "plan", input}, flags...)...)
	var plan struct {
		Data struct {
			Result domain.ConfigurationPlan `json:"result"`
		} `json:"data"`
	}
	{
		e := json.Unmarshal(planned, &plan)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	args := append([]string{"config", "apply", plan.Data.Result.ID, "--expected-config-revision", strconv.FormatInt(plan.Data.Result.BaseRevision, 10), "--confirmed"}, flags...)
	cli(t, 3, args...)
	journal := readCallerJournal(t, root, plan.Data.Result.ID)
	Expect(journal.Response != nil || journal.Phase == "failed").To(BeFalse(), func() string { return fmt.Sprint("response guard denial was recorded as original mutation failure") })

	current, e := f.store.Configuration(ctx)
	Expect(e != nil || current.Revision != plan.Data.Result.BaseRevision+1).To(BeFalse(), func() string { return fmt.Sprint("valid approved policy was not durable", e) })

	// The same principal's fresh device can reconcile the original natural plan
	// outcome. The original response guard denial did not allocate another plan.
	pair, e := f.auth.CreatePairing(ctx, f.base, "new valid owner", "fresh")
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	i, _ := service.NewID()
	{
		_, e = f.auth.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: i, Action: "approve"})
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	poll, e := f.auth.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	{

		e = client.SaveCredential(filepath.Join(root, "credential.json"), client.Credential{Endpoint: f.base, Profile: f.selection.Profile, LibraryID: f.session.LibraryID, Token: poll.Token})
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	cli(t, 0, args...)
	journal = readCallerJournal(t, root, plan.Data.Result.ID)
	Expect(journal.Response == nil || journal.Response.Status != 200).To(BeFalse(), func() string { return fmt.Sprint("original natural outcome was not reconciled") })

	after, e := f.store.Configuration(ctx)
	Expect(e != nil || after.Revision != current.Revision).To(BeFalse(), func() string { return fmt.Sprint("recovery changed effective configuration twice", e) })

})
var _ = It("TestActualCLIQueuedSealCancelRetainsInputUntilExplicitAbandon", Label("persistence"), func() {
	t := GinkgoT()

	f := newCommandFixture(t)
	ctx := context.Background()
	_, flags := callerProfile(t, f.selection, f.session, f.token, f.base)
	api, e := client.New(f.base, f.selection.Profile, client.Credential{Endpoint: f.base, Profile: f.selection.Profile, LibraryID: f.session.LibraryID, Token: f.token})
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	identity, e := api.Identity(ctx)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	target, _ := json.Marshal(map[string]any{"project_id": identity.UnfiledProjectID, "document_id": nil})
	u, _ := service.NewID()
	created, e := api.Create(ctx, u, importer.CreateRequest{Purpose: "result", InputMode: "file", SelectionName: "retained.html", Target: target})
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	content := []byte("retained input after explicit job cancel")
	hash := sha256.Sum256(content)
	size := int64(len(content))
	ack, e := api.Put(ctx, created.ID, importer.Chunk{Path: "_input", Kind: "file", Hash: hex.EncodeToString(hash[:]), Length: &size, Final: true}, bytes.NewReader(content))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	checkStatus := func(wantBytes string, wantCount int64) {
		GinkgoHelper()
		command := "status"
		if wantCount == 0 {
			command = "doctor"
		}
		out := cli(t, 0, append([]string{command}, flags...)...)
		var view struct {
			Data struct {
				Storage domain.StagingSummary `json:"storage"`
				Source  struct {
					Available bool   `json:"available"`
					Reason    string `json:"reason"`
				} `json:"source"`
			} `json:"data"`
		}
		{
			e := json.Unmarshal(out, &view)
			Expect(e != nil || view.Data.Storage.Bytes != wantBytes || view.Data.Storage.PendingUploads != wantCount || view.Data.Storage.PendingUploadsURL != "/api/v1/uploads" || view.Data.Source.Available || view.Data.Source.Reason != "source_read_not_implemented").To(BeFalse(), func() string { return fmt.Sprint("status disagrees with retained staging", view, e) })
		}

	}
	checkStatus(strconv.Itoa(len(content)), 1)
	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	s, _ := service.NewID()
	response, e := api.Complete(ctx, created.ID, s, cp)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	var job struct {
		ID string `json:"job_id"`
	}
	{
		e = json.Unmarshal(response.Body, &job)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	cli(t, 0, append([]string{"jobs", "cancel", job.ID}, flags...)...)
	j, e := f.store.Job(ctx, job.ID)
	Expect(e != nil || j.State != "cancelled").To(BeFalse(), func() string { return fmt.Sprint("queued cancellation was not stop confirmed", j, e) })

	detail, e := api.Upload(ctx, created.ID)
	Expect(e != nil || detail.State != "open" || detail.StagingBytes != strconv.Itoa(len(content))).To(BeFalse(), func() string {
		return fmt.Sprint("cancel discarded retained staging", detail.State, detail.StagingBytes, e)
	})

	cli(t, 0, append([]string{"uploads", "abandon", created.ID}, flags...)...)
	detail, e = api.Upload(ctx, created.ID)
	Expect(e != nil || detail.State != "abandoned").To(BeFalse(), func() string { return fmt.Sprint("explicit abandonment missing", e) })
	{

		e = f.store.Collect(ctx, domain.Object{Hash: hex.EncodeToString(hash[:]), Size: size})
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("abandoned unowned bytes remain pinned", e) })
	}

	checkStatus("0", 0)
})
var _ = It("TestActualHTTPConfigurationAndStatusScopedDenialUsesCanonicalCode", Label("persistence"), func() {
	t := GinkgoT()

	f := newCommandFixture(t)
	ctx := context.Background()
	principal, binding, credential := newIDForCommand(t), newIDForCommand(t), newIDForCommand(t)
	token, e := sec.Secret()
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	{

		e = f.store.Write(ctx, func(tx app.WriteTx) error {
			library, e := tx.Library()
			if e != nil {
				return e
			}
			scope := domain.CredentialScope{Kind: "workflow", Actions: []string{"project.read"}, ProjectIDs: []string{library.UnfiledProjectID}}
			if e = tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: principal, BindingID: binding, Active: true, Scope: scope}); e != nil {
				return e
			}
			project, e := tx.Scope(library.UnfiledProjectID)
			if e != nil {
				return e
			}
			return tx.PutScopedCredential(domain.ScopedCredential{ID: credential, PrincipalID: principal, TokenHash: sec.Verifier("workflow", token), AuthEpoch: library.AuthEpoch, Scope: scope, Permissions: map[string]int64{library.UnfiledProjectID: project.Project.PermissionsRevision}, ConfigRevision: project.ConfigRevision, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)})
		})
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	api, e := client.New(f.base, f.selection.Profile, client.Credential{Endpoint: f.base, Profile: f.selection.Profile, LibraryID: f.session.LibraryID, Token: token})
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	for _, path := range []string{"/api/v1/configuration", "/api/v1/status"} {
		_, e = api.JSON(ctx, "GET", path, "", nil, nil)
		var denied *client.Error
		Expect(!errors.As(e, &denied) || denied.Status != 403 || denied.Code != "FORBIDDEN").To(BeFalse(), func() string { return fmt.Sprintf("scoped %s canonical denial: %v", path, e) })

	}
})

func newIDForCommand(t GinkgoTInterface) string {
	GinkgoHelper()
	id, e := service.NewID()
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	return id
}

var _ = It("TestProductHandlerRefusesExternalHostCollisionWithActualEndpoint", Label("persistence"), func() {
	t := GinkgoT()
	f := newCommandFixture(t)
	configuration := []byte(`{"network":{"remote_enabled":true,"external_base_url":"https://127.0.0.1:9443","allowed_origins":["http://127.0.0.1:9443","https://127.0.0.1:9443"]}}`)
	// The actual --port endpoint sharing the external host cannot be
	// distinguished on the wire (the Host header carries no scheme), so
	// startup fails visibly instead of misclassifying local callers.
	_, e := productHandler(f.auth, "http://127.0.0.1:9443", configuration)
	Expect(e == nil).To(BeFalse(), func() string { return "ambiguous endpoint tuple accepted" })
	Expect(e.Error()).To(ContainSubstring("differ"))
	// A distinct actual local host keeps serving.
	_, e = productHandler(f.auth, "http://127.0.0.1:9270", configuration)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("noncolliding endpoint rejected: ", e) })
	// Remote disabled ignores the external URL entirely.
	_, e = productHandler(f.auth, "http://127.0.0.1:9443", []byte(`{"network":{"remote_enabled":false}}`))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("remote-disabled startup rejected: ", e) })
})
