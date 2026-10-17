package client

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("TestPollEventsTraversesActualAuthenticatedBatchesWithNumericSequence", Label("persistence"), func() {

	ctx := context.Background()
	base := os.TempDir()
	var volume, parent syscall.Stat_t
	if syscall.Stat("/Volumes/RootKernel", &volume) == nil && syscall.Stat("/Volumes", &parent) == nil && volume.Dev != parent.Dev && syscall.Access("/Volumes/RootKernel", 2) == nil {
		base = "/Volumes/RootKernel/tmp"
	}
	root, err := os.MkdirTemp(base, "haeryong-task005-event-client-")
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	defer os.RemoveAll(root)
	selection := service.Selection{Profile: "event-client", DataDir: filepath.Join(root, "library")}
	{
		_, err := service.Initialize(selection)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	writer, err := service.OpenWriter(selection)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	defer writer.Close()
	{
		err := writer.PrepareStorageBootstrap(nil)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	store := foundation.New(nil)
	_, closeStore, err := store.Prepare(ctx, writer.Profile)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	defer closeStore()
	{
		err := writer.MarkStorageReady(nil)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	core := appauth.New(store)
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	server.Config.Handler, err = httpauth.New(core, httpauth.Policy{Hosts: []string{server.Listener.Addr().String()}, Origins: []string{origin}}, nil)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	server.Start()
	defer server.Close()
	pairing, err := core.CreatePairing(ctx, origin, "synthetic event client", "event-client-fixture")
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	operation, err := service.NewID()
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	{

		_, err := core.BootstrapDecide(ctx, domain.PairingDecision{ID: pairing.ID, Code: pairing.Code, Origin: origin, ETag: pairing.ETag, OperationID: operation, Action: "approve"})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	poll, err := core.Poll(ctx, pairing.ID, pairing.PollingSecret, origin)
	Expect(err != nil || poll.Session == nil).To(BeFalse(), func() string { return fmt.Sprint("actual single-use session issuance failed", err) })

	api, err := New(origin, selection.Profile, Credential{Endpoint: origin, Profile: selection.Profile, LibraryID: poll.Session.LibraryID, Token: poll.Token})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	epoch, head, err := core.EventHead(ctx, poll.Token)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	{

		err := store.Write(ctx, func(tx app.WriteTx) error {
			for i := 0; i < 70; i++ {
				if _, err := tx.MutateResource(domain.Resource{Kind: "fixture", ID: fmt.Sprintf("event-%d", i), Value: []byte(`{}`)}, 0, "resource.invalidated"); err != nil {
					return err
				}
			}
			return nil
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	cursor, seen := fmt.Sprintf("%s:%d", epoch, head), 0
	for seen < 70 {
		page, err := api.PollEvents(ctx, cursor)
		Expect(err != nil || page.Resync != nil || len(page.Items) == 0).To(BeFalse(), func() string { return fmt.Sprint("actual retained replay did not progress", err) })

		for _, event := range page.Items {
			sequence, err := strconv.ParseInt(event.Sequence, 10, 64)
			Expect(err != nil || sequence != head+int64(seen)+1 || event.LibraryID != poll.Session.LibraryID || event.StreamEpoch != epoch).To(BeFalse(), func() string { return fmt.Sprint("replay changed numeric ordering or authorized library binding") })

			seen++
		}
		cursor = page.NextCursor
	}
	Expect(seen != 70 || cursor != fmt.Sprintf("%s:%d", epoch, head+70)).To(BeFalse(), func() string { return fmt.Sprint("bounded poll batch omitted retained notifications") })

	page, err := api.PollEvents(ctx, "malformed")
	Expect(err != nil || page.Resync == nil || len(page.Items) != 0 || page.Resync.Reason != "malformed_cursor" || page.Resync.ResumeCursor != cursor).To(BeFalse(), func() string {
		return fmt.Sprint("malformed reconnect did not return explicit current-head resync", err)
	})

})
