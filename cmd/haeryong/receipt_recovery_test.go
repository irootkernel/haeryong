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
	"testing"
	"time"

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

func TestActualCLIRecoveryDistinguishesPendingReconcilingAndCommitted(t *testing.T) {
	for _, stateName := range []string{"accepted", "reconciling", "post_reconciling", "failed", "failed_auto", "post_accepted"} {
		t.Run(stateName, func(t *testing.T) {
			ctx := context.Background()
			selection := selection(t)
			p := initProfile(t, selection)
			writer, e := service.OpenWriter(selection)
			if e != nil {
				t.Fatal(e)
			}
			defer writer.Close()
			if e = writer.PrepareStorageBootstrap(nil); e != nil {
				t.Fatal(e)
			}
			store := foundation.New(nil)
			_, closeStore, e := store.Prepare(ctx, writer.Profile)
			if e != nil {
				t.Fatal(e)
			}
			defer closeStore()
			if e = writer.MarkStorageReady(nil); e != nil {
				t.Fatal(e)
			}
			auth := appauth.New(store)
			imports := importer.New(store, auth, store, staging.New())
			server := httptest.NewUnstartedServer(nil)
			base := "http://" + server.Listener.Addr().String()
			u, _ := url.Parse(base)
			h, e := httpauth.New(auth, httpauth.Policy{Hosts: []string{u.Host}, Origins: []string{base}}, productRoutes{v1.New(query.New(store, auth, store)), httpimports.New(imports)})
			if e != nil {
				t.Fatal(e)
			}
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
			if e != nil {
				t.Fatal(e)
			}
			approval, _ := service.NewID()
			if _, e = auth.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: approval, Action: "approve"}); e != nil {
				t.Fatal(e)
			}
			poll, e := auth.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
			if e != nil {
				t.Fatal(e)
			}
			session, e := auth.Authenticate(ctx, poll.Token)
			if e != nil {
				t.Fatal(e)
			}
			root, flags := callerProfile(t, selection, session, poll.Token, base)
			caller, e := clientstate.New(root)
			if e != nil {
				t.Fatal(e)
			}
			api, e := client.New(base, selection.Profile, client.Credential{Endpoint: base, Profile: selection.Profile, LibraryID: p.LibraryID, Token: poll.Token})
			if e != nil {
				t.Fatal(e)
			}
			identity, e := api.Identity(ctx)
			if e != nil {
				t.Fatal(e)
			}
			operation, _ := service.NewID()
			request := domain.Request{Key: domain.OperationKey{PrincipalID: session.PrincipalID, OperationID: operation}, Method: "POST", Target: "/api/v1/packages/validate", Body: []byte(`{}`)}
			args := append([]string{"package", "validate", filepath.Join(t.TempDir(), "missing-original"), "--operation-id", operation}, flags...)
			var automaticInput transfer.Input
			if stateName == "failed_auto" {
				automaticInput = transfer.Input{Path: args[2], Validate: true}
				if saved, e := caller.UsePending(ctx, identity, automaticInput, operation); e != nil || saved != operation {
					t.Fatal("automatic intent fixture", e)
				}
				args = append([]string{"package", "validate", automaticInput.Path}, flags...)
			}
			uploadID := ""
			if strings.HasPrefix(stateName, "post_") {
				create, _ := service.NewID()
				body, _ := json.Marshal(map[string]any{"project_id": identity.UnfiledProjectID, "document_id": nil})
				r, e := imports.Create(ctx, poll.Token, create, importer.CreateRequest{Purpose: "result", InputMode: "file", SelectionName: "fixture", Target: body})
				if e != nil {
					t.Fatal(e)
				}
				var upload struct {
					ID string `json:"upload_id"`
				}
				if e = json.Unmarshal(r.Body, &upload); e != nil {
					t.Fatal(e)
				}
				uploadID = upload.ID
				request.Target = "/api/v1/uploads/" + upload.ID + "/abandon"
				args = append([]string{"uploads", "abandon", upload.ID, "--operation-id", operation}, flags...)
			} else {
				if e = caller.Save(transfer.Journal{OperationID: operation, Purpose: "validate", Identity: identity, Selection: transfer.Selection{ProjectID: identity.UnfiledProjectID}, Phase: "final_dispatch", FinalBody: request.Body}); e != nil {
					t.Fatal(e)
				}
			}
			if e = store.Write(ctx, func(tx app.WriteTx) error {
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
			}); e != nil {
				t.Fatal(e)
			}
			commandCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			command := exec.CommandContext(commandCtx, testBinary, args...)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			if e = command.Start(); e != nil {
				t.Fatal(e)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			if stateName == "accepted" || stateName == "post_accepted" {
				select {
				case e := <-done:
					t.Fatalf("direct pending POST was treated as definitive success: %v %s", e, &stdout)
				case <-lookedUp:
				case <-commandCtx.Done():
					t.Fatal("original receipt was not queried")
				}
				select {
				case e := <-done:
					t.Fatalf("pending receipt was treated as definitive success: %v %s %s", e, &stdout, &stderr)
				case <-time.After(400 * time.Millisecond):
				}
				if e = store.Write(ctx, func(tx app.WriteTx) error {
					if stateName == "post_accepted" {
						if e := tx.AbandonUpload(uploadID); e != nil {
							return e
						}
					}
					return tx.CompleteOperation(request.Key, domain.Response{Status: 200, Body: []byte(`{"valid":true}`)})
				}); e != nil {
					t.Fatal(e)
				}
				select {
				case e := <-done:
					if e != nil {
						t.Fatal("original accepted receipt did not finish", e, &stderr)
					}
				case <-commandCtx.Done():
					t.Fatal("same receipt was not polled")
				}
				journal, e := caller.Load(operation)
				if e != nil || journal.Response == nil || journal.Response.Status != 200 {
					t.Fatal("completed original response not retained", e)
				}
			} else {
				e := <-done
				var exit *exec.ExitError
				want := 6
				if strings.HasPrefix(stateName, "failed") {
					want = 2
				}
				if !errors.As(e, &exit) || exit.ExitCode() != want {
					t.Fatalf("reconciliation exit=%v want%d stdout=%s stderr=%s", e, want, &stdout, &stderr)
				}
				if strings.HasPrefix(stateName, "failed") {
					journal, e := caller.Load(operation)
					if e != nil || journal.Response == nil || journal.Response.Status != 422 || journal.Phase != "failed" {
						t.Fatal("recorded definitive failure was not preserved", e)
					}
					if stateName == "failed_auto" {
						next, _ := service.NewID()
						saved, e := caller.UsePending(ctx, identity, automaticInput, next)
						if e != nil || saved != next {
							t.Fatal("acknowledged failure blocks a new intended command", e)
						}
					}
				}
				if stateName == "reconciling" {
					journal, e := caller.Load(operation)
					if e != nil || journal.Response != nil || journal.Phase != "final_dispatch" {
						t.Fatal("uncertain journal was replaced by synthetic success", e)
					}
				}
			}
			receipt, e := store.Receipt(ctx, request.Key)
			if e != nil {
				t.Fatal(e)
			}
			if stateName != "accepted" && stateName != "post_accepted" && !strings.HasPrefix(stateName, "failed") && receipt.State != "reconciling" {
				t.Fatal("unknown operation was redispatched", receipt.State)
			}
		})
	}
}
