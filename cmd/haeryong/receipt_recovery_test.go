package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"

	"time"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	httpimports "haeryong.local/haeryong/internal/adapters/http/imports"
	"haeryong.local/haeryong/internal/adapters/http/v1"
	appauth "haeryong.local/haeryong/internal/application/auth"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/transfer"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("TestActualCLIRecoveryDistinguishesPendingReconcilingAndCommitted", Label("persistence"), func() {
	t := GinkgoT()

	for _, stateName := range []string{"accepted", "reconciling", "post_reconciling", "failed", "failed_auto", "post_accepted"} {
		func() {
			By(stateName)
			ctx := context.Background()
			selection := selection(t)
			p := initProfile(t, selection)
			writer, e := service.OpenWriter(selection)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			defer writer.Close()
			{
				e = writer.PrepareStorageBootstrap(nil)
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}

			store := foundation.New(nil)
			_, closeStore, e := store.Prepare(ctx, writer.Profile)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			defer closeStore()
			{
				e = writer.MarkStorageReady(nil)
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}

			auth := appauth.New(store)
			imports := importer.New(store, auth, store, staging.New())
			server := httptest.NewUnstartedServer(nil)
			base := "http://" + server.Listener.Addr().String()
			u, _ := url.Parse(base)
			h, e := httpauth.New(auth, httpauth.Policy{Hosts: []string{u.Host}, Origins: []string{base}}, productRoutes{v1.New(query.New(store, auth, store), nil, nil), httpimports.New(imports)})
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			lookedUp := make(chan struct{}, 1)
			server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				h.ServeHTTP(w, r)
				if strings.HasPrefix(r.URL.Path, "/api/v1/operations/") {
					select {
					case lookedUp <- struct{}{}:
					default:
					}
				}
			})
			server.Start()
			defer server.Close()
			pair, e := auth.CreatePairing(ctx, base, "typed receipt fixture", "fixture")
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			approval, _ := service.NewID()
			{
				_, e = auth.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: approval, Action: "approve"})
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}

			poll, e := auth.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			session, e := auth.Authenticate(ctx, poll.Token)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			root, flags := callerProfile(t, selection, session, poll.Token, base)
			caller, e := clientstate.New(root)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			api, e := client.New(base, selection.Profile, client.Credential{Endpoint: base, Profile: selection.Profile, LibraryID: p.LibraryID, Token: poll.Token})
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			identity, e := api.Identity(ctx)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			operation, _ := service.NewID()
			request := domain.Request{Key: domain.OperationKey{PrincipalID: session.PrincipalID, OperationID: operation}, Method: "POST", Target: "/api/v1/packages/validate", Body: []byte(`{}`)}
			args := append([]string{"package", "validate", filepath.Join(t.TempDir(), "missing-original"), "--operation-id", operation}, flags...)
			var automaticInput transfer.Input
			if stateName == "failed_auto" {
				automaticInput = transfer.Input{Path: args[2], Validate: true}
				{
					saved, e := caller.UsePending(ctx, identity, automaticInput, operation)
					Expect(e != nil || saved != operation).To(BeFalse(), func() string { return fmt.Sprint("automatic intent fixture", e) })
				}

				args = append([]string{"package", "validate", automaticInput.Path}, flags...)
			}
			uploadID := ""
			if strings.HasPrefix(stateName, "post_") {
				create, _ := service.NewID()
				body, _ := json.Marshal(map[string]any{"project_id": identity.UnfiledProjectID, "document_id": nil})
				r, e := imports.Create(ctx, poll.Token, create, importer.CreateRequest{Purpose: "result", InputMode: "file", SelectionName: "fixture", Target: body})
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

				var upload struct {
					ID string `json:"upload_id"`
				}
				{
					e = json.Unmarshal(r.Body, &upload)
					Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
				}

				uploadID = upload.ID
				request.Target = "/api/v1/uploads/" + upload.ID + "/abandon"
				args = append([]string{"uploads", "abandon", upload.ID, "--operation-id", operation}, flags...)
			} else {
				{
					e = caller.Save(transfer.Journal{OperationID: operation, Purpose: "validate", Identity: identity, Selection: transfer.Selection{ProjectID: identity.UnfiledProjectID}, Phase: "final_dispatch", FinalBody: request.Body})
					Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
				}

			}
			{
				e = store.Write(ctx, func(tx app.WriteTx) error {
					_, _, e := tx.ReserveOperation(request, nil)
					if e != nil {
						return e
					}
					if strings.HasPrefix(stateName, "failed") {
						return tx.CompleteOperation(request.Key, domain.Response{Status: 422, Body: []byte(`{"error":{"code":"PACKAGE_INVALID","message":"PACKAGE_INVALID","retryable":false,"details":{}}}`)})
					}
					if stateName != "accepted" && stateName != "post_accepted" {
						return tx.ReconcileOperation(request.Key)
					}
					return nil
				})
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}

			commandCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			command := exec.CommandContext(commandCtx, testBinary, args...)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			{
				e = command.Start()
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}

			done := make(chan error, 1)
			go func() { defer GinkgoRecover(); done <- command.Wait() }()
			if stateName == "accepted" || stateName == "post_accepted" {
				select {
				case e := <-done:
					Fail(fmt.Sprintf("direct pending POST was treated as definitive success: %v %s", e, &stdout))
				case <-lookedUp:
				case <-commandCtx.Done():
					Fail(fmt.Sprint("original receipt was not queried"))
				}
				select {
				case e := <-done:
					Fail(fmt.Sprintf("pending receipt was treated as definitive success: %v %s %s", e, &stdout, &stderr))
				case <-time.After(400 * time.Millisecond):
				}
				{
					e = store.Write(ctx, func(tx app.WriteTx) error {
						if stateName == "post_accepted" {
							if e := tx.AbandonUpload(uploadID); e != nil {
								return e
							}
						}
						return tx.CompleteOperation(request.Key, domain.Response{Status: 200, Body: []byte(`{"valid":true}`)})
					})
					Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
				}

				select {
				case e := <-done:
					if e != nil {
						Fail(fmt.Sprint("original accepted receipt did not finish", e, &stderr))
					}
				case <-commandCtx.Done():
					Fail(fmt.Sprint("same receipt was not polled"))
				}
				journal, e := caller.Load(operation)
				Expect(e != nil || journal.Response == nil || journal.Response.Status != 200).To(BeFalse(), func() string { return fmt.Sprint("completed original response not retained", e) })

			} else {
				e := <-done
				var exit *exec.ExitError
				want := 6
				if strings.HasPrefix(stateName, "failed") {
					want = 2
				}
				Expect(!errors.As(e, &exit) || exit.ExitCode() != want).To(BeFalse(), func() string {
					return fmt.Sprintf("reconciliation exit=%v want%d stdout=%s stderr=%s", e, want, &stdout, &stderr)
				})

				if strings.HasPrefix(stateName, "failed") {
					journal, e := caller.Load(operation)
					Expect(e != nil || journal.Response == nil || journal.Response.Status != 422 || journal.Phase != "failed").To(BeFalse(), func() string { return fmt.Sprint("recorded definitive failure was not preserved", e) })

					if stateName == "failed_auto" {
						next, _ := service.NewID()
						saved, e := caller.UsePending(ctx, identity, automaticInput, next)
						Expect(e != nil || saved != next).To(BeFalse(), func() string { return fmt.Sprint("acknowledged failure blocks a new intended command", e) })

					}
				}
				if stateName == "reconciling" {
					journal, e := caller.Load(operation)
					Expect(e != nil || journal.Response != nil || journal.Phase != "final_dispatch").To(BeFalse(), func() string { return fmt.Sprint("uncertain journal was replaced by synthetic success", e) })

				}
			}
			receipt, e := store.Receipt(ctx, request.Key)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			Expect(stateName != "accepted" && stateName != "post_accepted" && !strings.HasPrefix(stateName, "failed") && receipt.State != "reconciling").To(BeFalse(), func() string { return fmt.Sprint("unknown operation was redispatched", receipt.State) })

		}()
	}
})
