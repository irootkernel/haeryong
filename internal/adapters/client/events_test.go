package client

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"haeryong.local/haeryong/internal/adapters/foundation"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func TestPollEventsTraversesActualAuthenticatedBatchesWithNumericSequence(t *testing.T) {
	ctx := context.Background()
	base := os.TempDir()
	var volume, parent syscall.Stat_t
	if syscall.Stat("/Volumes/RootKernel", &volume) == nil && syscall.Stat("/Volumes", &parent) == nil && volume.Dev != parent.Dev && syscall.Access("/Volumes/RootKernel", 2) == nil {
		base = "/Volumes/RootKernel/tmp"
	}
	root, err := os.MkdirTemp(base, "haeryong-task005-event-client-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	selection := service.Selection{Profile: "event-client", DataDir: filepath.Join(root, "library")}
	if _, err := service.Initialize(selection); err != nil {
		t.Fatal(err)
	}
	writer, err := service.OpenWriter(selection)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.PrepareStorageBootstrap(nil); err != nil {
		t.Fatal(err)
	}
	store := foundation.New(nil)
	_, closeStore, err := store.Prepare(ctx, writer.Profile)
	if err != nil {
		t.Fatal(err)
	}
	defer closeStore()
	if err := writer.MarkStorageReady(nil); err != nil {
		t.Fatal(err)
	}
	core := appauth.New(store)
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	server.Config.Handler, err = httpauth.New(core, httpauth.Policy{Hosts: []string{server.Listener.Addr().String()}, Origins: []string{origin}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	pairing, err := core.CreatePairing(ctx, origin, "synthetic event client", "event-client-fixture")
	if err != nil {
		t.Fatal(err)
	}
	operation, err := service.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.BootstrapDecide(ctx, domain.PairingDecision{ID: pairing.ID, Code: pairing.Code, Origin: origin, ETag: pairing.ETag, OperationID: operation, Action: "approve"}); err != nil {
		t.Fatal(err)
	}
	poll, err := core.Poll(ctx, pairing.ID, pairing.PollingSecret, origin)
	if err != nil || poll.Session == nil {
		t.Fatal("actual single-use session issuance failed", err)
	}
	api, err := New(origin, selection.Profile, Credential{Endpoint: origin, Profile: selection.Profile, LibraryID: poll.Session.LibraryID, Token: poll.Token})
	if err != nil {
		t.Fatal(err)
	}
	epoch, head, err := core.EventHead(ctx, poll.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(ctx, func(tx app.WriteTx) error {
		for i := 0; i < 70; i++ {
			if _, err := tx.MutateResource(domain.Resource{Kind: "fixture", ID: fmt.Sprintf("event-%d", i), Value: []byte(`{}`)}, 0, "resource.invalidated"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cursor, seen := fmt.Sprintf("%s:%d", epoch, head), 0
	for seen < 70 {
		page, err := api.PollEvents(ctx, cursor)
		if err != nil || page.Resync != nil || len(page.Items) == 0 {
			t.Fatal("actual retained replay did not progress", err)
		}
		for _, event := range page.Items {
			sequence, err := strconv.ParseInt(event.Sequence, 10, 64)
			if err != nil || sequence != head+int64(seen)+1 || event.LibraryID != poll.Session.LibraryID || event.StreamEpoch != epoch {
				t.Fatal("replay changed numeric ordering or authorized library binding")
			}
			seen++
		}
		cursor = page.NextCursor
	}
	if seen != 70 || cursor != fmt.Sprintf("%s:%d", epoch, head+70) {
		t.Fatal("bounded poll batch omitted retained notifications")
	}
	page, err := api.PollEvents(ctx, "malformed")
	if err != nil || page.Resync == nil || len(page.Items) != 0 || page.Resync.Reason != "malformed_cursor" || page.Resync.ResumeCursor != cursor {
		t.Fatal("malformed reconnect did not return explicit current-head resync", err)
	}
}
