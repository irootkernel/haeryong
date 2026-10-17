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
	"testing"
	"time"

	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/foundation"
	config "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/transfer"
	"haeryong.local/haeryong/internal/domain"
)

func eventID(t *testing.T) string {
	t.Helper()
	id, err := service.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// The daemon is fully stopped before this fixture takes the real profile lock.
// Typed writes seed ordinary rows/outbox entries. Narrow offline SQL simulates
// an expired history segment or later lifecycle transition for which no product
// engine exists yet; neither a restore nor retention implementation is claimed.
func seedEventProfile(t *testing.T, selection service.Selection, seed func(app.WriteTx) error, offline func(*sql.DB) error) {
	t.Helper()
	writer, err := service.OpenWriter(selection)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.PrepareStorageBootstrap(nil); err != nil {
		t.Fatal(err)
	}
	store := foundation.New(nil)
	_, closeStore, err := store.Prepare(context.Background(), writer.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(context.Background(), seed); err != nil {
		closeStore()
		t.Fatal(err)
	}
	if err := closeStore(); err != nil {
		t.Fatal(err)
	}
	if offline != nil {
		db, err := sql.Open("sqlite", filepath.Join(writer.Profile.DataDir, "storage", "library.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if err := offline(db); err != nil {
			t.Fatal(err)
		}
	}
}

func TestActualCLIEventsResyncFreshStateReplayPreservesPendingIntent(t *testing.T) {
	ctx := context.Background()
	s := selection(t)
	initProfile(t, s)
	port := availablePort(t)
	daemon := startDaemon(t, s, port)
	session, token := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "synthetic events owner")
	backend, err := url.Parse(daemon.id.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	direct, err := client.New(daemon.id.Endpoint, s.Profile, client.Credential{Endpoint: daemon.id.Endpoint, Profile: s.Profile, LibraryID: session.LibraryID, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := direct.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "unsent.html")
	if err := os.WriteFile(input, []byte("<p>actual pending caller input</p>"), 0600); err != nil {
		t.Fatal(err)
	}
	operation := eventID(t)
	cli(t, 5, append([]string{"import", input, "--operation-id", operation}, flags...)...)
	journalPath := filepath.Join(caller, "journals", operation+".json")
	originalJournal, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	var pending transfer.Journal
	if json.Unmarshal(originalJournal, &pending) != nil || pending.Response != nil || pending.FinalBody != nil || pending.Result.UploadID == "" || pending.Result.CreateKey == "" || pending.Result.CompleteKey == "" || pending.Result.Checkpoint.Complete {
		t.Fatal("real failed transfer did not retain an unfinished composite intent")
	}
	writeCount := writes.Load()
	poll := func(cursor string) client.EventPage {
		t.Helper()
		args := []string{"events"}
		if cursor != "" {
			args = append(args, "--cursor", cursor)
		}
		var result struct {
			Data client.EventPage `json:"data"`
		}
		if err := json.Unmarshal(cli(t, 0, append(args, flags...)...), &result); err != nil {
			t.Fatal(err)
		}
		return result.Data
	}
	initial := poll("")
	head, err := strconv.ParseInt(initial.Head, 10, 64)
	if err != nil || initial.StreamEpoch == "" {
		t.Fatal("actual polling head absent", err)
	}
	stop := func() {
		t.Helper()
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
	if gap.Resync == nil || gap.Resync.Reason != "sequence_gap" || gap.Resync.Head != strconv.FormatInt(head+3, 10) || gap.Resync.StreamEpoch != initial.StreamEpoch || len(gap.Items) != 0 {
		t.Fatal("real retained-history gap did not force explicit current-head resync")
	}
	for cursor, reason := range map[string]string{"malformed": "malformed_cursor", eventID(t) + ":0": "epoch_changed", initial.StreamEpoch + ":" + strconv.FormatInt(head+100, 10): "future_cursor"} {
		page := poll(cursor)
		if page.Resync == nil || page.Resync.Reason != reason || page.Resync.ResumeCursor != gap.Resync.ResumeCursor || len(page.Items) != 0 {
			t.Fatal("reconnect control fabricated replay or lost current resume head")
		}
	}
	// Capture head first, then fetch actual authorized state. A later real config
	// commit races with this recovery cycle and must be replayed after that head.
	cli(t, 0, append([]string{"projects", "list"}, flags...)...)
	cli(t, 0, append([]string{"jobs", "list"}, flags...)...)
	cli(t, 0, append([]string{"uploads", "list"}, flags...)...)
	var documents query.List[query.Document]
	if _, err := api.JSON(ctx, "GET", "/api/v1/documents", "", nil, &documents); err != nil {
		t.Fatal(err)
	}
	visible := false
	for _, row := range documents.Items {
		visible = visible || row.ID == document
	}
	if !visible {
		t.Fatal("fresh authorized document state did not contain the visible seeded row")
	}
	var current config.View
	if _, err := direct.JSON(ctx, "GET", "/api/v1/configuration", "", nil, &current); err != nil {
		t.Fatal(err)
	}
	var plan domain.ConfigurationPlan
	if _, err := direct.JSON(ctx, "POST", "/api/v1/configuration/plans", eventID(t), config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: current.ConfigRevision, Changes: map[string]any{"security": map[string]any{"remember_device_default": false}}}, &plan); err != nil {
		t.Fatal(err)
	}
	if _, err := direct.JSON(ctx, "POST", "/api/v1/configuration/plans/"+plan.ID+"/commit", eventID(t), config.CommitRequest{ExpectedRevision: plan.BaseRevision, Confirmed: true}, nil); err != nil {
		t.Fatal(err)
	}
	replay := poll(gap.Resync.ResumeCursor)
	if replay.Resync != nil || len(replay.Items) != 1 || replay.Items[0].Type != "policy.changed" || replay.Items[0].Sequence != strconv.FormatInt(head+4, 10) {
		t.Fatal("head-before-refetch recovery lost the concurrent confirmed mutation")
	}
	stop()
	daemon = startDaemon(t, s, port)
	restarted := poll(replay.NextCursor)
	if restarted.Resync != nil || restarted.StreamEpoch != initial.StreamEpoch || restarted.NextCursor != replay.NextCursor {
		t.Fatal("ordinary daemon restart invalidated current event epoch/cursor")
	}
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
	if hidden.Resync != nil || !foundInvalidation || bytes.Contains(encoded, []byte(document)) || bytes.Contains(encoded, []byte("synthetic event-visible")) {
		t.Fatal("replayed hidden target leaked identity or suppressed content-free invalidation")
	}
	if _, err := api.JSON(ctx, "GET", "/api/v1/documents/"+document, "", nil, nil); !client.IsNotFound(err) {
		t.Fatal("fresh private query did not apply current lifecycle")
	}
	status, _ := authRequest(t, daemon.id.Endpoint, "DELETE", "/api/v1/session", token, nil)
	if status != 204 {
		t.Fatal("actual session revocation failed")
	}
	denied := cli(t, 3, append([]string{"events", "--cursor", replay.NextCursor}, flags...)...)
	if strings.Contains(string(denied), "stream_epoch") || strings.Contains(string(denied), "items") {
		t.Fatal("revoked event polling returned private state")
	}
	if writes.Load() != writeCount {
		t.Fatal("event/refetch recovery issued a POST or retried a pending write")
	}
	afterJournal, err := os.ReadFile(journalPath)
	if err != nil || !bytes.Equal(originalJournal, afterJournal) {
		t.Fatal("resync/replay/auth failure acknowledged or changed unsent caller intent")
	}
}

func TestActualCLIAndSSEInteriorEventGapRequiresResyncBeforeDomainDelivery(t *testing.T) {
	s := selection(t)
	initProfile(t, s)
	port := availablePort(t)
	daemon := startDaemon(t, s, port)
	session, token := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "synthetic interior-gap owner")
	_, flags := callerProfile(t, s, session, token, daemon.id.Endpoint)
	api, err := client.New(daemon.id.Endpoint, s.Profile, client.Credential{Endpoint: daemon.id.Endpoint, Profile: s.Profile, LibraryID: session.LibraryID, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := api.PollEvents(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	head, err := strconv.ParseInt(initial.Head, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
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
	t.Run("poll", func(t *testing.T) {
		var result struct {
			Data client.EventPage `json:"data"`
		}
		if err := json.Unmarshal(cli(t, 0, append([]string{"events", "--cursor", cursor}, flags...)...), &result); err != nil {
			t.Fatal(err)
		}
		page := result.Data
		if page.Resync == nil || page.Resync.Reason != "sequence_gap" || page.Resync.ResumeCursor != resume || page.NextCursor != resume || len(page.Items) != 0 {
			t.Fatal("interior gap was skipped or domain notifications preceded current-head resync")
		}
	})
	t.Run("SSE", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, "GET", daemon.id.Endpoint+"/api/v1/events", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Last-Event-ID", cursor)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("actual authenticated SSE unavailable")
		}
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
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &control); err != nil {
					t.Fatal(err)
				}
				if kind != "resync.required" || control.Reason != "sequence_gap" || control.ResumeCursor != resume || eventCursor != resume {
					t.Fatal("SSE skipped an interior gap or emitted a domain notification before resync")
				}
				return
			}
		}
		t.Fatal("actual SSE did not return bounded gap control", scanner.Err())
	})
}
