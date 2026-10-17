package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"haeryong.local/haeryong/internal/adapters/client"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/transfer"
	"haeryong.local/haeryong/internal/domain"
)

type f12Identity struct {
	Size int64  `json:"byte_size"`
	Hash string `json:"sha256"`
}
type f12Byte byte

func (b f12Byte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(b)
	}
	return len(p), nil
}

// These are the exact original Task04 F12 bytes/ZIP construction, not reduced
// examples or application admission limits. The independent CLI test imports
// once and reuses that version for cursor/range/download/restart checks.
func f12Archive(t *testing.T, root string) (string, int64, int64, map[string]f12Identity) {
	t.Helper()
	name := filepath.Join(root, "large-result.zip")
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(file)
	expected := map[string]f12Identity{}
	var expanded int64
	add := func(path string, size int64, input io.Reader) {
		t.Helper()
		out, err := w.CreateHeader(&zip.FileHeader{Name: path, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.New()
		n, err := io.CopyBuffer(io.MultiWriter(out, hash), io.LimitReader(input, size), make([]byte, 64<<10))
		if err != nil || n != size {
			t.Fatal("canonical F12 generation failed", err)
		}
		expanded += n
		expected[path] = f12Identity{n, hex.EncodeToString(hash.Sum(nil))}
	}
	const htmlSize = 40 << 20
	prefix, suffix := "<!doctype html><html><body>", "</body></html>"
	add("deep/nested/large.html", htmlSize, io.MultiReader(strings.NewReader(prefix), io.LimitReader(f12Byte('a'), htmlSize-int64(len(prefix)+len(suffix))), strings.NewReader(suffix)))
	add("unknown.attachment", 12<<20, rand.New(rand.NewSource(20261016)))
	const first = "<!doctype html><title>Small first page</title><p>F12 import proof</p>"
	add("first.html", int64(len(first)), strings.NewReader(first))
	for i := 0; i < 1024; i++ {
		add(fmt.Sprintf("entries/%04d.data", i), int64(i+1), f12Byte(byte(i)))
	}
	if _, err := w.Create("represented-empty/"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if info.Size() != 12768177 || expanded != 55050821 || len(expected) != 1027 || len(first) != 69 {
		t.Fatalf("canonical F12 identity changed: zip=%d expanded=%d files=%d small=%d", info.Size(), expanded, len(expected), len(first))
	}
	return name, info.Size(), expanded, expected
}

func f12CLI(t *testing.T, ctx context.Context, output string, want int, args ...string) {
	t.Helper()
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, testBinary, args...)
	command.Stdout = file
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err = command.Run()
	closeErr := file.Close()
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else {
			t.Fatal("F12 CLI launch/fixture deadline failed", err)
		}
	}
	if code != want {
		t.Fatalf("F12 CLI exit=%d want=%d stderr=%s", code, want, &stderr)
	}
}
func f12Decode[T any](t *testing.T, name string) T {
	t.Helper()
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out struct {
		Data T `json:"data"`
	}
	if err := json.NewDecoder(f).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Data
}
func f12Hash(t *testing.T, name string) f12Identity {
	t.Helper()
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	hash := sha256.New()
	n, err := io.CopyBuffer(hash, f, make([]byte, 64<<10))
	if err != nil {
		t.Fatal(err)
	}
	return f12Identity{n, hex.EncodeToString(hash.Sum(nil))}
}

func f12Inventory(t *testing.T, name string, expected map[string]f12Identity) int {
	t.Helper()
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	seen := map[string]bool{}
	dirs := map[string]bool{"deep": false, "deep/nested": false, "entries": false, "represented-empty": false}
	for {
		var row struct {
			Data query.File `json:"data"`
		}
		err := decoder.Decode(&row)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		e := row.Data
		if seen[e.Path] {
			t.Fatal("CLI cursor duplicated exact path", e.Path)
		}
		seen[e.Path] = true
		if e.Kind == "directory" {
			if _, ok := dirs[e.Path]; !ok {
				t.Fatal("unexpected F12 directory", e.Path)
			}
			dirs[e.Path] = true
			continue
		}
		want, ok := expected[e.Path]
		if !ok || e.Kind != "file" || e.Hash == nil || *e.Hash != want.Hash || e.Size != strconv.FormatInt(want.Size, 10) {
			t.Fatal("CLI cursor omitted/changed exact identity", e.Path)
		}
	}
	if len(seen) != len(expected)+len(dirs) {
		t.Fatalf("CLI inventory incomplete: %d/%d", len(seen), len(expected)+len(dirs))
	}
	for name, seen := range dirs {
		if !seen {
			t.Fatal("CLI inventory omitted represented directory", name)
		}
	}
	return len(seen)
}
func f12Downloaded(t *testing.T, root string, expected map[string]f12Identity) {
	t.Helper()
	seen := map[string]bool{}
	dirs := map[string]bool{"deep": false, "deep/nested": false, "entries": false, "represented-empty": false}
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if _, ok := dirs[relative]; !ok {
				return fmt.Errorf("unexpected output directory %q", relative)
			}
			dirs[relative] = true
			return nil
		}
		want, ok := expected[relative]
		if !ok || !entry.Type().IsRegular() {
			return fmt.Errorf("unexpected output type/path %q", relative)
		}
		if got := f12Hash(t, name); got != want {
			return fmt.Errorf("downloaded bytes changed %q", relative)
		}
		seen[relative] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != len(expected) {
		t.Fatalf("directory download omitted files %d/%d", len(seen), len(expected))
	}
	for name, present := range dirs {
		if !present {
			t.Fatal("directory download omitted empty/parent directory", name)
		}
	}
}

// Failure diagnostics run before deferred daemon/proxy cleanup with a separate
// finite context, so a cancelled CLI cannot erase the last observable state.
// Only synthetic resource IDs, counters and registry codes are recorded; no
// credentials, source/destination paths or arbitrary server error text escape.
func f12FailureState(api *client.Client, upload, operation, journalFile, phase string, started time.Time, phases map[string]int64) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	out := map[string]any{"phase": phase, "elapsed_ms": time.Since(started).Milliseconds(), "completed_phases": phases, "upload_id": upload, "operation_id": operation}
	code := func(err error) string {
		var remote *client.Error
		if errors.As(err, &remote) {
			return remote.Code
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return "DIAGNOSTIC_TIMEOUT"
		}
		return "DIAGNOSTIC_UNAVAILABLE"
	}
	registryError := func(value string) string {
		for _, c := range value {
			if c != '_' && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
				return "NON_REGISTRY_ERROR_TEXT_WITHHELD"
			}
		}
		return value
	}
	var journal transfer.Journal
	if data, err := os.ReadFile(journalFile); err == nil && json.Unmarshal(data, &journal) == nil {
		out["journal"] = map[string]any{"dispatch_phase": journal.Phase, "preparation_phase": journal.Result.Phase, "checkpoint": journal.Result.Checkpoint, "seal_job_id": journal.Result.JobID, "final_request_saved": journal.FinalBody != nil, "receipt_saved": journal.Response != nil}
	}
	jobs := map[string]bool{}
	var detail importer.UploadDetail
	if _, err := api.JSON(ctx, "GET", "/api/v1/uploads/"+upload, "", nil, &detail); err != nil {
		out["upload_query_error"] = code(err)
	} else {
		errorCode := ""
		if detail.Error != nil {
			errorCode = registryError(*detail.Error)
		}
		out["upload"] = map[string]any{"state": detail.State, "checkpoint": detail.Checkpoint, "byte_size": detail.ByteSize, "staging_bytes": detail.StagingBytes, "revision": detail.Revision, "error": errorCode}
		if detail.SealJobID != nil {
			jobs[*detail.SealJobID] = true
		}
	}
	if journal.Result.JobID != "" {
		jobs[journal.Result.JobID] = true
	}
	if receipt, err := api.Operation(ctx, operation); err == nil {
		var result struct {
			Job string `json:"job_id"`
		}
		if json.Unmarshal(receipt.Body, &result) == nil && result.Job != "" {
			jobs[result.Job] = true
		}
		out["operation_receipt_status"] = receipt.Status
	} else {
		out["operation_query_error"] = code(err)
	}
	states := []map[string]any{}
	for id := range jobs {
		job, err := api.Job(ctx, id)
		if err != nil {
			states = append(states, map[string]any{"job_id": id, "query_error": code(err)})
		} else {
			states = append(states, map[string]any{"job_id": id, "kind": job.Kind, "state": job.State, "attempt_generation": job.AttemptGeneration, "revision": job.Revision, "cancel_requested": job.CancelRequested, "error": registryError(job.Error)})
		}
	}
	out["jobs"] = states
	entries, complete, accepted := 0, 0, int64(0)
	cursor := ""
	out["entry_progress_complete"] = false
	for {
		var page importer.Page[importer.EntryProgress]
		if _, err := api.JSON(ctx, "GET", "/api/v1/uploads/"+upload+"/entries?cursor="+url.QueryEscape(cursor), "", nil, &page); err != nil {
			out["entry_progress_error"] = code(err)
			out["entry_progress_has_continuation"] = cursor != ""
			break
		}
		for _, entry := range page.Items {
			entries++
			if entry.Complete {
				complete++
			}
			n, _ := strconv.ParseInt(entry.AcceptedBytes, 10, 64)
			accepted += n
		}
		if page.NextCursor == nil {
			out["entry_progress_complete"] = true
			out["entry_progress_has_continuation"] = false
			break
		}
		cursor = *page.NextCursor
	}
	out["transport_entry_progress"] = map[string]any{"entries_observed": entries, "final_entries_observed": complete, "accepted_bytes_observed": accepted}
	return out
}

func f12RecordFailure(t *testing.T, report string, value map[string]any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Log("Transfer diagnostic encoding failed")
		return
	}
	if report != "" {
		file, err := os.OpenFile(report+".failure.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Log("Transfer diagnostic report creation failed")
			return
		}
		_, err = file.Write(append(data, '\n'))
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Log("Transfer diagnostic report write failed")
		}
	}
	t.Logf("Structured transfer failure state: %s", data)
}

// This deliberately small diagnostic isolates partial-upload restart from F12
// workload cost. It is not a replacement for canonical F12 acceptance.
func TestActualCLISmallArchiveAcknowledgementLossRestartAndCapturedResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	started := time.Now()
	root := t.TempDir()
	archive := filepath.Join(root, "small-diagnostic.zip")
	file, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(file)
	expected := map[string]f12Identity{}
	for _, entry := range []struct {
		path  string
		size  int64
		input io.Reader
	}{{"payload.bin", 4 << 20, rand.New(rand.NewSource(20261016))}, {"first.html", int64(len("<p>diagnostic</p>")), strings.NewReader("<p>diagnostic</p>")}} {
		out, err := w.CreateHeader(&zip.FileHeader{Name: entry.path, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.New()
		n, err := io.CopyBuffer(io.MultiWriter(out, hash), io.LimitReader(entry.input, entry.size), make([]byte, 64<<10))
		if err != nil || n != entry.size {
			t.Fatal("small diagnostic generation failed", err)
		}
		expected[entry.path] = f12Identity{n, hex.EncodeToString(hash.Sum(nil))}
	}
	if err := errors.Join(w.Close(), file.Sync(), file.Close()); err != nil {
		t.Fatal(err)
	}
	archiveIdentity := f12Hash(t, archive)
	if archiveIdentity.Size <= 3<<20 {
		t.Fatal("small diagnostic cannot reach actual third-chunk acknowledgement")
	}
	s := selection(t)
	initProfile(t, s)
	daemon := startDaemon(t, s, availablePort(t))
	session, token := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "small restart diagnostic owner")
	backend, err := url.Parse(daemon.id.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	var lost, blocked atomic.Bool
	transport := &http.Transport{DisableKeepAlives: true, DisableCompression: true}
	defer transport.CloseIdleConnections()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if blocked.Load() && r.Method == "PUT" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(503)
			io.WriteString(w, `{"error":{"code":"TRANSFER_INTERRUPTED","message":"controlled transport outage","retryable":true,"details":{}}}`)
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
		if r.Method == "PUT" && r.Header.Get("Haeryong-Upload-Offset") == strconv.Itoa(2<<20) && response.StatusCode == 200 && lost.CompareAndSwap(false, true) {
			if _, err := io.Copy(io.Discard, response.Body); err != nil {
				t.Error("small diagnostic committed acknowledgement was not observable")
				return
			}
			blocked.Store(true)
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			connection.Close()
			return
		}
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.CopyBuffer(w, response.Body, make([]byte, 64<<10))
	}))
	defer proxy.Close()
	caller, flags := callerProfile(t, s, session, token, proxy.URL)
	operation, err := service.NewID()
	if err != nil {
		t.Fatal(err)
	}
	journalFile := filepath.Join(caller, "journals", operation+".json")
	api, err := client.New(proxy.URL, s.Profile, client.Credential{Endpoint: proxy.URL, Profile: s.Profile, LibraryID: session.LibraryID, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	phase, upload := "small_acknowledgement_loss", ""
	phases := map[string]int64{}
	defer func() {
		if t.Failed() {
			f12RecordFailure(t, os.Getenv("HAERYONG_TASK005_SMALL_REPORT"), f12FailureState(api, upload, operation, journalFile, phase, started, phases))
		}
	}()
	arguments := append([]string{"import", archive, "--operation-id", operation}, flags...)
	f12CLI(t, ctx, filepath.Join(root, "interrupted.json"), 5, arguments...)
	if !lost.Load() {
		t.Fatal("small diagnostic actual acknowledgement-loss barrier not reached")
	}
	blocked.Store(false)
	var journal transfer.Journal
	data, err := os.ReadFile(journalFile)
	if err != nil || json.Unmarshal(data, &journal) != nil {
		t.Fatal("small diagnostic durable journal unavailable", err)
	}
	upload = journal.Result.UploadID
	var before importer.UploadDetail
	if _, err := api.JSON(ctx, "GET", "/api/v1/uploads/"+upload, "", nil, &before); err != nil {
		t.Fatal(err)
	}
	if journal.Result.Checkpoint.AcceptedBytes != strconv.Itoa(2<<20) || before.Checkpoint.AcceptedBytes != strconv.Itoa(3<<20) || before.State != "open" {
		t.Fatal("small diagnostic did not establish observed versus durable prefix")
	}
	phase = "small_restart_and_source_removal"
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(s)...)...)
	daemon.wait(t)
	port, err := strconv.Atoi(backend.Port())
	if err != nil {
		t.Fatal(err)
	}
	daemon = startDaemon(t, s, port)
	var after importer.UploadDetail
	if _, err := api.JSON(ctx, "GET", "/api/v1/uploads/"+upload, "", nil, &after); err != nil || before.Checkpoint != after.Checkpoint {
		t.Fatal("small diagnostic restart checkpoint changed", err)
	}
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	phases["ack_loss_restart_source_removal_ms"] = time.Since(started).Milliseconds()
	phase = "small_resumed_seal_and_publication"
	acceptedFile := filepath.Join(root, "accepted.json")
	f12CLI(t, ctx, acceptedFile, 0, arguments...)
	accepted := f12Decode[struct {
		Operation string `json:"operation_id"`
		Upload    string `json:"upload_id"`
		Create    string `json:"upload_create_operation_id"`
		Complete  string `json:"upload_complete_operation_id"`
		Result    struct {
			Job string `json:"job_id"`
		} `json:"result"`
	}](t, acceptedFile)
	if accepted.Operation != operation || accepted.Upload != upload || accepted.Create != journal.Result.CreateKey || accepted.Complete != journal.Result.CompleteKey {
		t.Fatal("small resumed diagnostic changed captured composite intent")
	}
	job, err := api.Job(ctx, accepted.Result.Job)
	if err != nil || job.State != "completed" {
		t.Fatal("small resumed diagnostic did not publish", err)
	}
	var result struct {
		Version string `json:"version_id"`
	}
	if json.Unmarshal(job.Result, &result) != nil || result.Version == "" {
		t.Fatal("small diagnostic published version missing")
	}
	phase = "small_published_exact_byte_verification"
	for path, want := range expected {
		response, err := api.Call(ctx, "GET", "/api/v1/versions/"+result.Version+"/file?path="+url.QueryEscape(path), "", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.New()
		n, readErr := io.CopyBuffer(hash, response.Body, make([]byte, 64<<10))
		closeErr := response.Body.Close()
		if response.StatusCode != 200 || readErr != nil || closeErr != nil || n != want.Size || hex.EncodeToString(hash.Sum(nil)) != want.Hash {
			t.Fatal("small resumed publication byte identity changed")
		}
	}
	if report := os.Getenv("HAERYONG_TASK005_SMALL_REPORT"); report != "" {
		data, _ := json.MarshalIndent(map[string]any{"scope": "Separate synthetic small partial-upload restart diagnostic; not canonical F12", "archive": archiveIdentity, "regular_file_count": len(expected), "caller_checkpoint_before_loss": 2 << 20, "server_checkpoint_before_loss": 3 << 20, "restart_checkpoint_equal": true, "original_source_removed_before_resume": true, "same_composite_ids": true, "published_all_bytes_verified": true, "elapsed_ms": time.Since(started).Milliseconds(), "phases": phases}, "", "  ")
		file, err := os.OpenFile(report, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = file.Write(append(data, '\n'))
		if err := errors.Join(err, file.Close()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestActualCLIF12ResumeCursorRangeDownloadAndRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	defer cancel()
	started := time.Now()
	phases := map[string]int64{}
	root := t.TempDir()
	var resources syscall.Statfs_t
	if err := syscall.Statfs(root, &resources); err != nil {
		t.Fatal(err)
	}
	archive, zipSize, expanded, expected := f12Archive(t, root)
	archiveIdentity := f12Hash(t, archive)
	s := selection(t)
	initProfile(t, s)
	daemon := startDaemon(t, s, availablePort(t))
	session, token := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "F12 CLI owner")
	backend, err := url.Parse(daemon.id.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	var lostUpload, blockedUpload, armDownload, lostDownload, resumedDownload atomic.Bool
	transport := &http.Transport{DisableKeepAlives: true, DisableCompression: true}
	defer transport.CloseIdleConnections()
	// The disposable loopback transport closes a connection only after a real
	// durable acknowledgement, and later after actual download bytes. It grants
	// no identity: all forwarded requests retain the genuine owner bearer.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if blockedUpload.Load() && r.Method == "PUT" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(503)
			io.WriteString(w, `{"error":{"code":"TRANSFER_INTERRUPTED","message":"controlled transport outage","retryable":true,"details":{}}}`)
			return
		}
		request := r.Clone(r.Context())
		request.URL.Scheme, request.URL.Host = backend.Scheme, backend.Host
		request.Host = backend.Host
		request.RequestURI = ""
		response, err := transport.RoundTrip(request)
		if err != nil {
			w.WriteHeader(502)
			return
		}
		defer response.Body.Close()
		if r.Method == "PUT" && r.Header.Get("Haeryong-Upload-Offset") == strconv.Itoa(2<<20) && response.StatusCode == 200 && lostUpload.CompareAndSwap(false, true) {
			if _, err := io.Copy(io.Discard, response.Body); err != nil {
				t.Error("could not observe committed upload acknowledgement")
				return
			}
			blockedUpload.Store(true)
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			connection.Close()
			return
		}
		if lostDownload.Load() && r.Header.Get("Range") == "bytes=1048576-" && r.Header.Get("If-Range") != "" {
			resumedDownload.Store(true)
		}
		for key, values := range response.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
		w.WriteHeader(response.StatusCode)
		if armDownload.Load() && r.Method == "GET" && r.URL.Query().Get("path") == "deep/nested/large.html" && r.Header.Get("Range") == "" && response.StatusCode == 200 && lostDownload.CompareAndSwap(false, true) {
			if _, err := io.CopyN(w, response.Body, 1<<20); err != nil {
				t.Error("could not deliver actual interrupted download prefix")
				return
			}
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Error(err)
				return
			}
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			connection.Close()
			return
		}
		_, _ = io.CopyBuffer(w, response.Body, make([]byte, 64<<10))
	}))
	defer proxy.Close()
	caller, flags := callerProfile(t, s, session, token, proxy.URL)
	operation, err := service.NewID()
	if err != nil {
		t.Fatal(err)
	}
	arguments := append([]string{"import", archive, "--operation-id", operation}, flags...)
	f12CLI(t, ctx, filepath.Join(root, "interrupted-import.json"), 5, arguments...)
	if !lostUpload.Load() {
		t.Fatal("actual acknowledged upload response-loss barrier not reached")
	}
	blockedUpload.Store(false)
	var journal transfer.Journal
	journalFile := filepath.Join(caller, "journals", operation+".json")
	data, err := os.ReadFile(journalFile)
	if err != nil || json.Unmarshal(data, &journal) != nil {
		t.Fatal("interrupted CLI did not preserve caller journal", err)
	}
	if journal.Result.UploadID == "" || journal.Result.Checkpoint.AcceptedBytes != strconv.Itoa(2<<20) {
		t.Fatal("journal did not preserve last actually observed prefix")
	}
	api, err := client.New(proxy.URL, s.Profile, client.Credential{Endpoint: proxy.URL, Profile: s.Profile, LibraryID: session.LibraryID, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	phase := "confirmed_checkpoint_and_restart"
	defer func() {
		if !t.Failed() {
			return
		}
		value := f12FailureState(api, journal.Result.UploadID, operation, journalFile, phase, started, phases)
		f12RecordFailure(t, os.Getenv("HAERYONG_TASK005_WORKLOAD_REPORT"), value)
	}()
	var detail importer.UploadDetail
	if _, err := api.JSON(ctx, "GET", "/api/v1/uploads/"+journal.Result.UploadID, "", nil, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Checkpoint.AcceptedBytes != strconv.Itoa(3<<20) || detail.State != "open" {
		t.Fatal("server lost committed-but-unobserved prefix")
	}
	_, recoveryFlags := callerProfile(t, s, session, token, proxy.URL)
	discovery := filepath.Join(root, "owner-discovery.json")
	f12CLI(t, ctx, discovery, 0, append([]string{"uploads", "list"}, recoveryFlags...)...)
	uploads := f12Decode[importer.Page[importer.UploadView]](t, discovery)
	found := false
	for _, upload := range uploads.Items {
		if upload.ID == journal.Result.UploadID {
			found = true
		}
	}
	if !found {
		t.Fatal("independent owner client without upload ID could not discover retained staging")
	}
	restart := func() {
		t.Helper()
		cli(t, 0, append([]string{"service", "stop"}, profileArgs(s)...)...)
		daemon.wait(t)
		port, err := strconv.Atoi(backend.Port())
		if err != nil {
			t.Fatal(err)
		}
		daemon = startDaemon(t, s, port)
	}
	restart()
	var reopened importer.UploadDetail
	if _, err := api.JSON(ctx, "GET", "/api/v1/uploads/"+journal.Result.UploadID, "", nil, &reopened); err != nil {
		t.Fatal(err)
	}
	if reopened.Checkpoint != detail.Checkpoint {
		t.Fatal("daemon restart changed acknowledged checkpoint")
	}
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	} // Resume must use the durable captured snapshot, not reread author input.
	acceptedPath := filepath.Join(root, "accepted-import.json")
	phase = "resumed_import_seal_and_publication"
	f12CLI(t, ctx, acceptedPath, 0, arguments...)
	accepted := f12Decode[struct {
		Operation string `json:"operation_id"`
		Upload    string `json:"upload_id"`
		Create    string `json:"upload_create_operation_id"`
		Complete  string `json:"upload_complete_operation_id"`
		Result    struct {
			Job string `json:"job_id"`
		} `json:"result"`
	}](t, acceptedPath)
	if accepted.Operation != operation || accepted.Upload != journal.Result.UploadID || accepted.Create != journal.Result.CreateKey || accepted.Complete != journal.Result.CompleteKey || accepted.Result.Job == "" {
		t.Fatal("resumed CLI changed composite IDs or created a second upload")
	}
	var published domain.Job
	for {
		if _, err := api.JSON(ctx, "GET", "/api/v1/jobs/"+accepted.Result.Job, "", nil, &published); err != nil {
			t.Fatal(err)
		}
		if published.State == "completed" {
			break
		}
		if published.State == "failed" || published.State == "cancelled" {
			t.Fatalf("F12 publication state=%s error=%s", published.State, published.Error)
		}
		select {
		case <-ctx.Done():
			t.Fatal("F12 fixture budget expired waiting for atomic publication")
		case <-time.After(100 * time.Millisecond):
		}
	}
	var result struct {
		Version  string `json:"version_id"`
		Document string `json:"document_id"`
	}
	if json.Unmarshal(published.Result, &result) != nil || result.Version == "" {
		t.Fatal("published version result missing")
	}
	phases["import_resume_publish_ms"] = time.Since(started).Milliseconds()
	phase = "all_entry_cursor_and_large_range"
	listed := filepath.Join(root, "files-before.ndjson")
	f12CLI(t, ctx, listed, 0, append([]string{"files", "list", "--version", result.Version, "--limit", "37"}, flags...)...)
	entryCount := f12Inventory(t, listed, expected)
	large := expected["deep/nested/large.html"]
	ranges := [][2]int64{{0, (1 << 20) - 1}, {1 << 20, large.Size - (1 << 20) - 1}, {large.Size - (1 << 20), large.Size - 1}}
	reassembled := sha256.New()
	var rangedBytes int64
	for _, part := range ranges {
		response, err := api.Call(ctx, "GET", "/api/v1/versions/"+result.Version+"/file?path="+url.QueryEscape("deep/nested/large.html"), "", nil, map[string]string{"Range": fmt.Sprintf("bytes=%d-%d", part[0], part[1]), "If-Range": `"sha256-` + large.Hash + `"`})
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 206 || response.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", part[0], part[1], large.Size) {
			response.Body.Close()
			t.Fatal("large range did not retain exact immutable representation")
		}
		n, err := io.CopyBuffer(reassembled, response.Body, make([]byte, 64<<10))
		closeErr := response.Body.Close()
		if err != nil || closeErr != nil || n != part[1]-part[0]+1 {
			t.Fatal("large range incomplete", err, closeErr)
		}
		rangedBytes += n
	}
	if rangedBytes != large.Size || hex.EncodeToString(reassembled.Sum(nil)) != large.Hash {
		t.Fatal("large ranged reassembly SHA differs from source")
	}
	armDownload.Store(true)
	phase = "interrupted_large_get_and_resume"
	getArgs := append([]string{"files", "get", "--version", result.Version, "--path", "deep/nested/large.html", "--output", filepath.Join(root, "large-received.html")}, flags...)
	f12CLI(t, ctx, filepath.Join(root, "interrupted-get.json"), 5, getArgs...)
	if !lostDownload.Load() {
		t.Fatal("actual download response-loss barrier not reached")
	}
	getOutput := filepath.Join(root, "resumed-get.json")
	f12CLI(t, ctx, getOutput, 0, getArgs...)
	download := f12Decode[client.Download](t, getOutput)
	if !download.Resumed || !resumedDownload.Load() || f12Hash(t, filepath.Join(root, "large-received.html")) != large {
		t.Fatal("CLI did not resume and verify the actual large-file prefix")
	}
	phases["cursor_range_resumed_get_ms"] = time.Since(started).Milliseconds()
	phase = "whole_directory_download_before_restart"
	before := filepath.Join(root, "download-before")
	f12CLI(t, ctx, filepath.Join(root, "download-before.json"), 0, append([]string{"files", "download", "--version", result.Version, "--output", before}, flags...)...)
	f12Downloaded(t, before, expected)
	phase = "whole_directory_cursor_and_download_after_restart"
	restart()
	listedAfter := filepath.Join(root, "files-after.ndjson")
	f12CLI(t, ctx, listedAfter, 0, append([]string{"files", "list", "--version", result.Version, "--limit", "37"}, flags...)...)
	if f12Inventory(t, listedAfter, expected) != entryCount {
		t.Fatal("restart changed complete typed inventory")
	}
	after := filepath.Join(root, "download-after")
	f12CLI(t, ctx, filepath.Join(root, "download-after.json"), 0, append([]string{"files", "download", "--version", result.Version, "--output", after}, flags...)...)
	f12Downloaded(t, after, expected)
	phases["all_files_restart_download_ms"] = time.Since(started).Milliseconds()
	if report := os.Getenv("HAERYONG_TASK005_WORKLOAD_REPORT"); report != "" {
		value := map[string]any{"fixture": "Canonical F12 synthetic bytes through actual CLI/daemon/HTTP/SQLite", "scope": "Task05 upload response-loss/resume/owner discovery, complete cursor listing, verified stream/range/download and restart; no renderer/native/provider/global F12 claim", "archive_bytes": zipSize, "archive_sha256": archiveIdentity.Hash, "expanded_file_bytes": expanded, "regular_file_count": len(expected), "ordinary_files": 1024, "represented_directory_count": 4, "all_entry_count": entryCount, "largest_file_bytes": large.Size, "poorly_compressible_attachment_bytes": 12 << 20, "attachment_deterministic_seed": 20261016, "small_first_html_bytes": 69, "upload_response_lost_after_committed_bytes": 3 << 20, "caller_checkpoint_before_loss": 2 << 20, "independent_owner_discovery_without_original_upload_id": true, "restart_checkpoint_equal": true, "original_source_removed_before_resume": true, "same_composite_ids": true, "range_requests": len(ranges), "ranged_bytes_reassembled": rangedBytes, "range_reassembly_sha256": large.Hash, "actual_interrupted_download_resumed": true, "restart_all_paths_types_hashes_equal": true, "full_directory_downloads": 2, "phases": phases, "environment": map[string]any{"goos": runtime.GOOS, "goarch": runtime.GOARCH, "available_disk_bytes_before_fixture": uint64(resources.Bavail) * uint64(resources.Bsize)}, "files": expected}
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(report, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = file.Write(append(data, '\n'))
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Fatal("F12 evidence write failed", err, closeErr)
		}
	}
}
