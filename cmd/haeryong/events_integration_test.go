package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/foundation"
	config "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/transfer"
	"haeryong.local/haeryong/internal/domain"
)

func eventID(t GinkgoTInterface) string {
	GinkgoHelper()
	id, err := service.NewID()
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	return id
}

// The daemon is fully stopped before this fixture takes the real profile lock.
// Typed writes seed ordinary rows/outbox entries. Narrow offline SQL simulates
// an expired history segment or later lifecycle transition for which no product
// engine exists yet; neither a restore nor retention implementation is claimed.
func seedEventProfile(t GinkgoTInterface, selection service.Selection, seed func(app.WriteTx) error, offline func(*sql.DB) error) {
	GinkgoHelper()
	writer, err := service.OpenWriter(selection)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	defer writer.Close()
	{
		err := writer.PrepareStorageBootstrap(nil)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	store := foundation.New(nil)
	_, closeStore, err := store.Prepare(context.Background(), writer.Profile)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	if err := store.Write(context.Background(), seed); err != nil {
		closeStore()
		Fail(fmt.Sprint(err))
	}
	{
		err := closeStore()
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	if offline != nil {
		db, err := sql.Open("sqlite", filepath.Join(writer.Profile.DataDir, "storage", "library.sqlite"))
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		defer db.Close()
		{
			err := offline(db)
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

	}
}

var _ = It("TestActualCLIEventsResyncFreshStateReplayPreservesPendingIntent", Label("persistence"), func() {
	t := GinkgoT()

	ctx := context.Background()
	s := selection(t)
	initProfile(t, s)
	port := availablePort(t)
	daemon := startDaemon(t, s, port)
	session, token := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "synthetic events owner")
	backend, err := url.Parse(daemon.id.Endpoint)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	var writes atomic.Int64
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes.Add(1)
		}
		if r.Method == "PUT" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(503)
			io.WriteString(w, `{"error":{"code":"TRANSFER_INTERRUPTED","message":"controlled unsent payload","retryable":true,"details":{}}}`)
			return
		}
		request := r.Clone(r.Context())
		request.URL.Scheme, request.URL.Host = backend.Scheme, backend.Host
		request.Host, request.RequestURI = backend.Host, ""
		response, err := transport.RoundTrip(request)
		if err != nil {
			w.WriteHeader(502)
			return
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	defer proxy.Close()
	caller, flags := callerProfile(t, s, session, token, proxy.URL)
	api, err := client.New(proxy.URL, s.Profile, client.Credential{Endpoint: proxy.URL, Profile: s.Profile, LibraryID: session.LibraryID, Token: token})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	direct, err := client.New(daemon.id.Endpoint, s.Profile, client.Credential{Endpoint: daemon.id.Endpoint, Profile: s.Profile, LibraryID: session.LibraryID, Token: token})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	identity, err := direct.Identity(ctx)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	input := filepath.Join(t.TempDir(), "unsent.html")
	{
		err := os.WriteFile(input, []byte("<p>actual pending caller input</p>"), 0600)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	operation := eventID(t)
	cli(t, 5, append([]string{"import", input, "--operation-id", operation}, flags...)...)
	journalPath := filepath.Join(caller, "journals", operation+".json")
	originalJournal, err := os.ReadFile(journalPath)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	var pending transfer.Journal
	Expect(json.Unmarshal(originalJournal, &pending) != nil || pending.Response != nil || pending.FinalBody != nil || pending.Result.UploadID == "" || pending.Result.CreateKey == "" || pending.Result.CompleteKey == "" || pending.Result.Checkpoint.Complete).To(BeFalse(), func() string { return fmt.Sprint("real failed transfer did not retain an unfinished composite intent") })

	writeCount := writes.Load()
	poll := func(cursor string) client.EventPage {
		GinkgoHelper()
		args := []string{"events"}
		if cursor != "" {
			args = append(args, "--cursor", cursor)
		}
		var result struct {
			Data client.EventPage `json:"data"`
		}
		{
			err := json.Unmarshal(cli(t, 0, append(args, flags...)...), &result)
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

		return result.Data
	}
	initial := poll("")
	head, err := strconv.ParseInt(initial.Head, 10, 64)
	Expect(err != nil || initial.StreamEpoch == "").To(BeFalse(), func() string { return fmt.Sprint("actual polling head absent", err) })

	stop := func() {
		GinkgoHelper()
		cli(t, 0, append([]string{"service", "stop"}, profileArgs(s)...)...)
		daemon.wait(t)
	}
	document := eventID(t)
	stop()
	seedEventProfile(t, s, func(tx app.WriteTx) error {
		if err := tx.CreateDocument(domain.Document{ID: document, ProjectID: identity.UnfiledProjectID, Title: "synthetic event-visible document", Lifecycle: "active", Revision: 1}); err != nil {
			return err
		}
		for _, resource := range []domain.Resource{{Kind: "fixture", ID: eventID(t), Value: []byte(`{}`)}, {Kind: "fixture", ID: eventID(t), Value: []byte(`{}`)}, {Kind: "document", ID: document, Value: []byte(`{}`)}} {
			if _, err := tx.MutateResource(resource, 0, "resource.invalidated"); err != nil {
				return err
			}
		}
		return nil
	}, func(db *sql.DB) error {
		result, err := db.Exec("DELETE FROM events WHERE sequence=?", head+1)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			return fmt.Errorf("offline fixture did not remove exactly one retained event")
		}
		return nil
	})
	daemon = startDaemon(t, s, port)
	gap := poll(initial.StreamEpoch + ":" + initial.Head)
	Expect(gap.Resync == nil || gap.Resync.Reason != "sequence_gap" || gap.Resync.Head != strconv.FormatInt(head+3, 10) || gap.Resync.StreamEpoch != initial.StreamEpoch || len(gap.Items) != 0).To(BeFalse(), func() string {
		return fmt.Sprint("real retained-history gap did not force explicit current-head resync")
	})

	for cursor, reason := range map[string]string{"malformed": "malformed_cursor", eventID(t) + ":0": "epoch_changed", initial.StreamEpoch + ":" + strconv.FormatInt(head+100, 10): "future_cursor"} {
		page := poll(cursor)
		Expect(page.Resync == nil || page.Resync.Reason != reason || page.Resync.ResumeCursor != gap.Resync.ResumeCursor || len(page.Items) != 0).To(BeFalse(), func() string { return fmt.Sprint("reconnect control fabricated replay or lost current resume head") })

	}
	// Capture head first, then fetch actual authorized state. A later real config
	// commit races with this recovery cycle and must be replayed after that head.
	cli(t, 0, append([]string{"projects", "list"}, flags...)...)
	cli(t, 0, append([]string{"jobs", "list"}, flags...)...)
	cli(t, 0, append([]string{"uploads", "list"}, flags...)...)
	var documents query.List[query.Document]
	{
		_, err := api.JSON(ctx, "GET", "/api/v1/documents", "", nil, &documents)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	visible := false
	for _, row := range documents.Items {
		visible = visible || row.ID == document
	}
	Expect(!visible).To(BeFalse(), func() string {
		return fmt.Sprint("fresh authorized document state did not contain the visible seeded row")
	})

	var current config.View
	{
		_, err := direct.JSON(ctx, "GET", "/api/v1/configuration", "", nil, &current)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	var plan domain.ConfigurationPlan
	{
		_, err := direct.JSON(ctx, "POST", "/api/v1/configuration/plans", eventID(t), config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: current.ConfigRevision, Changes: map[string]any{"security": map[string]any{"remember_device_default": false}}}, &plan)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		_, err := direct.JSON(ctx, "POST", "/api/v1/configuration/plans/"+plan.ID+"/commit", eventID(t), config.CommitRequest{ExpectedRevision: plan.BaseRevision, Confirmed: true}, nil)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	replay := poll(gap.Resync.ResumeCursor)
	Expect(replay.Resync != nil || len(replay.Items) != 1 || replay.Items[0].Type != "policy.changed" || replay.Items[0].Sequence != strconv.FormatInt(head+4, 10)).To(BeFalse(), func() string {
		return fmt.Sprint("head-before-refetch recovery lost the concurrent confirmed mutation")
	})

	stop()
	daemon = startDaemon(t, s, port)
	restarted := poll(replay.NextCursor)
	Expect(restarted.Resync != nil || restarted.StreamEpoch != initial.StreamEpoch || restarted.NextCursor != replay.NextCursor).To(BeFalse(), func() string { return fmt.Sprint("ordinary daemon restart invalidated current event epoch/cursor") })

	stop()
	seedEventProfile(t, s, func(app.WriteTx) error { return nil }, func(db *sql.DB) error {
		result, err := db.Exec("UPDATE documents SET lifecycle='trash',revision=revision+1 WHERE document_id=? AND lifecycle='active'", document)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			return fmt.Errorf("offline fixture did not hide exactly the current target")
		}
		return nil
	})
	daemon = startDaemon(t, s, port)
	hidden := poll(initial.StreamEpoch + ":" + strconv.FormatInt(head+1, 10))
	encoded, _ := json.Marshal(hidden)
	foundInvalidation := false
	for _, event := range hidden.Items {
		if event.Sequence == strconv.FormatInt(head+3, 10) {
			foundInvalidation = event.Type == "library.invalidated" && event.Resource == nil
		}
	}
	Expect(hidden.Resync != nil || !foundInvalidation || bytes.Contains(encoded, []byte(document)) || bytes.Contains(encoded, []byte("synthetic event-visible"))).To(BeFalse(), func() string {
		return fmt.Sprint("replayed hidden target leaked identity or suppressed content-free invalidation")
	})
	{

		_, err := api.JSON(ctx, "GET", "/api/v1/documents/"+document, "", nil, nil)
		Expect(!client.IsNotFound(err)).To(BeFalse(), func() string { return fmt.Sprint("fresh private query did not apply current lifecycle") })
	}

	status, _ := authRequest(t, daemon.id.Endpoint, "DELETE", "/api/v1/session", token, nil)
	Expect(status != 204).To(BeFalse(), func() string { return fmt.Sprint("actual session revocation failed") })

	denied := cli(t, 3, append([]string{"events", "--cursor", replay.NextCursor}, flags...)...)
	Expect(strings.Contains(string(denied), "stream_epoch") || strings.Contains(string(denied), "items")).To(BeFalse(), func() string { return fmt.Sprint("revoked event polling returned private state") })
	Expect(writes.Load() != writeCount).To(BeFalse(), func() string { return fmt.Sprint("event/refetch recovery issued a POST or retried a pending write") })

	afterJournal, err := os.ReadFile(journalPath)
	Expect(err != nil || !bytes.Equal(originalJournal, afterJournal)).To(BeFalse(), func() string {
		return fmt.Sprint("resync/replay/auth failure acknowledged or changed unsent caller intent")
	})

})
var _ = It("TestActualCLIAndSSEInteriorEventGapRequiresResyncBeforeDomainDelivery", Label("persistence"), func() {
	t := GinkgoT()

	s := selection(t)
	initProfile(t, s)
	port := availablePort(t)
	daemon := startDaemon(t, s, port)
	session, token := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "synthetic interior-gap owner")
	_, flags := callerProfile(t, s, session, token, daemon.id.Endpoint)
	api, err := client.New(daemon.id.Endpoint, s.Profile, client.Credential{Endpoint: daemon.id.Endpoint, Profile: s.Profile, LibraryID: session.LibraryID, Token: token})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	initial, err := api.PollEvents(context.Background(), "")
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	head, err := strconv.ParseInt(initial.Head, 10, 64)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	cli(t, 0, append([]string{"service", "stop"}, profileArgs(s)...)...)
	daemon.wait(t)
	seedEventProfile(t, s, func(tx app.WriteTx) error {
		for i := 0; i < 3; i++ {
			if _, err := tx.MutateResource(domain.Resource{Kind: "fixture", ID: eventID(t), Value: []byte(`{}`)}, 0, "resource.invalidated"); err != nil {
				return err
			}
		}
		return nil
	}, func(db *sql.DB) error {
		result, err := db.Exec("DELETE FROM events WHERE sequence=?", head+2)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			return fmt.Errorf("offline fixture did not remove exactly the middle event")
		}
		return nil
	})
	daemon = startDaemon(t, s, port)
	cursor := initial.StreamEpoch + ":" + initial.Head
	resume := initial.StreamEpoch + ":" + strconv.FormatInt(head+3, 10)
	func() {
		By("poll")
		var result struct {
			Data client.EventPage `json:"data"`
		}
		{
			err := json.Unmarshal(cli(t, 0, append([]string{"events", "--cursor", cursor}, flags...)...), &result)
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

		page := result.Data
		Expect(page.Resync == nil || page.Resync.Reason != "sequence_gap" || page.Resync.ResumeCursor != resume || page.NextCursor != resume || len(page.Items) != 0).To(BeFalse(), func() string {
			return fmt.Sprint("interior gap was skipped or domain notifications preceded current-head resync")
		})

	}()
	func() {
		By("SSE")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, "GET", daemon.id.Endpoint+"/api/v1/events", nil)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Last-Event-ID", cursor)
		response, err := http.DefaultClient.Do(request)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		defer response.Body.Close()
		Expect(response.StatusCode != 200).To(BeFalse(), func() string { return fmt.Sprint("actual authenticated SSE unavailable") })

		scanner := bufio.NewScanner(response.Body)
		kind, eventCursor := "", ""
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event: ") {
				kind = strings.TrimPrefix(line, "event: ")
			}
			if strings.HasPrefix(line, "id: ") {
				eventCursor = strings.TrimPrefix(line, "id: ")
			}
			if strings.HasPrefix(line, "data: ") {
				var control client.EventResync
				{
					err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &control)
					Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
				}
				Expect(kind != "resync.required" || control.Reason != "sequence_gap" || control.ResumeCursor != resume || eventCursor != resume).To(BeFalse(), func() string {
					return fmt.Sprint("SSE skipped an interior gap or emitted a domain notification before resync")
				})

				return
			}
		}
		Fail(fmt.Sprint("actual SSE did not return bounded gap control", scanner.Err()))
	}()
})
