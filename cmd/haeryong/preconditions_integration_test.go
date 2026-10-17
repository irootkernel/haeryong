package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
)

func TestActualCLIPreTransferBaseSurvivesCompetingPublicationAndRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	s := selection(t)
	initProfile(t, s)
	daemon := startDaemon(t, s, availablePort(t))
	session, token := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "synthetic competing-publication owner")
	_, directFlags := callerProfile(t, s, session, token, daemon.id.Endpoint)
	newID := func() string {
		t.Helper()
		id, err := service.NewID()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	input := func(name, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	published := func(out []byte) (string, string) {
		t.Helper()
		var result struct {
			Data struct {
				Document string `json:"document_id"`
				Version  string `json:"version_id"`
			} `json:"data"`
		}
		if json.Unmarshal(out, &result) != nil || result.Data.Document == "" || result.Data.Version == "" {
			t.Fatal("actual CLI did not report a completed publication")
		}
		return result.Data.Document, result.Data.Version
	}
	baseInput := input("first.html", "<p>original immutable base</p>")
	document, base := published(cli(t, 0, append([]string{"import", baseInput, "--operation-id", newID()}, directFlags...)...))
	backend, err := url.Parse(daemon.id.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	arrived, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var blocked atomic.Bool
	var puts atomic.Int64
	var conflicts atomic.Int64
	var wrongConflict atomic.Bool
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	// Unlike the after-commit lostResponse helper, this barrier stops the first
	// actual PUT before upstream transfer. The original owner bearer is forwarded
	// unchanged; no fixture grants authority or substitutes an API response.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			puts.Add(1)
			if blocked.CompareAndSwap(false, true) {
				close(arrived)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
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
		if r.Method == "POST" && r.URL.Path == "/api/v1/imports" && response.StatusCode == 409 {
			var captured bytes.Buffer
			if _, err := io.Copy(io.MultiWriter(w, &captured), response.Body); err != nil {
				t.Error("actual conflict response could not be delivered")
				return
			}
			var result struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal(captured.Bytes(), &result) != nil || result.Error.Code != "VERSION_CONFLICT" {
				wrongConflict.Store(true)
			}
			conflicts.Add(1)
			return
		}
		_, _ = io.CopyBuffer(w, response.Body, make([]byte, 64<<10))
	}))
	defer proxy.Close()
	aRoot, aFlags := callerProfile(t, s, session, token, proxy.URL)
	aID := newID()
	aInput := input("a.html", "<p>A captured before competing publication</p>")
	aArgs := append([]string{"import", aInput, "--document", document, "--operation-id", aID}, aFlags...)
	command := exec.CommandContext(ctx, testBinary, aArgs...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	var commandErr error
	go func() { commandErr = command.Wait(); close(finished) }()
	defer func() {
		unblock()
		_ = command.Process.Kill()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("competing CLI process was not reaped")
		}
	}()
	select {
	case <-arrived:
	case <-finished:
		t.Fatal("CLI A exited before its actual pre-transfer barrier")
	case <-ctx.Done():
		t.Fatal("CLI A did not reach its bounded transfer barrier")
	}
	captured := readCallerJournal(t, aRoot, aID)
	if captured.OperationID != aID || captured.BaseVersionID == nil || *captured.BaseVersionID != base || captured.Selection.DocumentID == nil || *captured.Selection.DocumentID != document || captured.Result.CreateKey == "" || captured.Result.CompleteKey == "" || captured.Result.UploadID == "" || captured.Result.Snapshot.ID == "" || captured.FinalBody != nil || captured.Result.Checkpoint.AcceptedBytes != "0" {
		t.Fatal("A did not durably capture its exact original base and composite IDs before transferring")
	}
	api, err := client.New(daemon.id.Endpoint, s.Profile, client.Credential{Endpoint: daemon.id.Endpoint, Profile: s.Profile, LibraryID: session.LibraryID, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	beforeB, err := api.PollEvents(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	_, bFlags := callerProfile(t, s, session, token, daemon.id.Endpoint)
	bInput := input("b.html", "<p>B wins with different immutable bytes</p>")
	bDocument, bVersion := published(cli(t, 0, append([]string{"import", bInput, "--document", document, "--operation-id", newID()}, bFlags...)...))
	if bDocument != document || bVersion == base {
		t.Fatal("competing real CLI did not publish a new version of the exact target")
	}
	unblock()
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("CLI A did not resolve after competing publication")
	}
	var exit *exec.ExitError
	if !errors.As(commandErr, &exit) || exit.ExitCode() != 4 || conflicts.Load() == 0 || wrongConflict.Load() {
		t.Fatal("A did not return the actual VERSION_CONFLICT precondition failure")
	}
	failed := readCallerJournal(t, aRoot, aID)
	if failed.OperationID != captured.OperationID || failed.BaseVersionID == nil || *failed.BaseVersionID != base || failed.Result.CreateKey != captured.Result.CreateKey || failed.Result.CompleteKey != captured.Result.CompleteKey || failed.Result.UploadID != captured.Result.UploadID || failed.Result.Snapshot != captured.Result.Snapshot {
		t.Fatal("conflict replaced A's captured base, selected bytes or original composite IDs")
	}
	var final struct {
		Base     string `json:"base_version_id"`
		Document string `json:"document_id"`
	}
	if json.Unmarshal(failed.FinalBody, &final) != nil || final.Base != base || final.Document != document {
		t.Fatal("final dispatch silently adopted the competing latest version")
	}
	journalPath := filepath.Join(aRoot, "journals", aID+".json")
	failedBytes, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	firstFailureHead, err := api.PollEvents(ctx, beforeB.NextCursor)
	if err != nil || firstFailureHead.Resync != nil {
		t.Fatal("actual publication outbox could not be inspected", err)
	}
	documentEvents := 0
	for _, event := range firstFailureHead.Items {
		if event.Resource != nil && event.Resource.Type == "document" && event.Resource.ID == document {
			documentEvents++
		}
	}
	if documentEvents != 1 {
		t.Fatal("conflicting A created an additional document publication/outbox effect")
	}
	putsBeforeRetry := puts.Load()
	if err := os.Remove(aInput); err != nil {
		t.Fatal(err)
	}
	cli(t, 4, aArgs...)
	afterRetry, err := os.ReadFile(journalPath)
	if err != nil || !bytes.Equal(failedBytes, afterRetry) || puts.Load() != putsBeforeRetry || wrongConflict.Load() {
		t.Fatal("same-I retry recaptured input, retransferred or changed original preconditions")
	}
	retriedHead, err := api.PollEvents(ctx, firstFailureHead.NextCursor)
	if err != nil || retriedHead.Resync != nil || len(retriedHead.Items) != 0 || retriedHead.Head != firstFailureHead.Head {
		t.Fatal("same-I conflict retry produced another outbox effect", err)
	}
	var versions query.List[query.Version]
	if _, err := api.JSON(ctx, "GET", "/api/v1/documents/"+document+"/versions", "", nil, &versions); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, version := range versions.Items {
		seen[version.ID] = true
	}
	if len(versions.Items) != 2 || versions.NextCursor != nil || !seen[base] || !seen[bVersion] {
		t.Fatal("conflict/retry published a third immutable version")
	}
	var current query.Document
	if _, err := api.JSON(ctx, "GET", "/api/v1/documents/"+document, "", nil, &current); err != nil || current.LatestVersionID == nil || *current.LatestVersionID != bVersion {
		t.Fatal("A conflict/retry moved the winning latest pointer", err)
	}
}
