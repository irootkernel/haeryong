package foundation_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type productionDaemon struct {
	command *exec.Cmd
	done    chan error
	id      service.Identity
	stopped bool
}

func startProductionDaemon(t *testing.T, binary string, selection service.Selection, port int) *productionDaemon {
	t.Helper()
	command := exec.Command(binary, "daemon", "run", "--profile", selection.Profile, "--data-dir", selection.DataDir, "--port", fmt.Sprint(port), "--json")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	d := &productionDaemon{command: command, done: make(chan error, 1)}
	go func() { d.done <- command.Wait() }()
	t.Cleanup(func() { d.kill(t) })
	ready := make(chan []byte, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- append([]byte(nil), scanner.Bytes()...)
		} else {
			ready <- nil
		}
		for scanner.Scan() {
		}
	}()
	select {
	case raw := <-ready:
		var response struct {
			Data service.Identity `json:"data"`
		}
		if json.Unmarshal(raw, &response) != nil || !response.Data.Storage.Ready {
			d.kill(t)
			t.Fatalf("production daemon did not become ready: %s", stderr.String())
		}
		d.id = response.Data
	case <-time.After(15 * time.Second):
		d.kill(t)
		t.Fatal("production daemon readiness deadline")
	}
	return d
}

func (d *productionDaemon) kill(t *testing.T) {
	t.Helper()
	if d.stopped {
		return
	}
	d.stopped = true
	if err := d.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Error(err)
	}
	select {
	case err := <-d.done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || !exit.Sys().(syscall.WaitStatus).Signaled() || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			t.Error("production daemon termination was not actual SIGKILL")
		}
	case <-time.After(10 * time.Second):
		t.Error("production daemon did not release after process kill")
	}
}

func daemonJSON(t *testing.T, endpoint, token, method, path, operation string, body []byte, result any) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", endpoint)
	}
	if operation != "" {
		request.Header.Set("Idempotency-Key", operation)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if result != nil {
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if json.NewDecoder(response.Body).Decode(&envelope) != nil || json.Unmarshal(envelope.Data, result) != nil {
			t.Fatal("invalid production response envelope")
		}
	}
	return response.StatusCode
}

func TestActualProductionDaemonRecoversAcceptedClientExitAndSurvivesSIGKILL(t *testing.T) {
	f := newFixture(t)
	upload := f.create(t)
	checkpoint := f.put(t, upload, []byte("<html>real daemon independent of the accepted caller</html>"))
	seal := f.complete(t, upload, checkpoint, key(t))
	if err := f.core.Process(f.ctx, seal); err != nil {
		t.Fatal(err)
	}
	principal, err := f.auth.Authenticate(f.ctx, f.token)
	if err != nil {
		t.Fatal(err)
	}
	operation, version := key(t), key(t)
	// Kill the creator process after its actual import acceptance transaction,
	// then run the unmodified production daemon with its real background worker.
	killAtBoundary(t, f, childRequest{Action: "import", Boundary: "accepted_commit_before_return", Upload: upload, Operation: operation, Version: version})
	receipt, err := f.store.Receipt(f.ctx, domain.OperationKey{PrincipalID: principal.PrincipalID, OperationID: operation})
	if err != nil || receipt.State != "completed" || receipt.Response.Status != 202 {
		t.Fatal("accepted caller's original creation receipt did not persist")
	}
	job := responseID(t, *receipt.Response, "job_id")
	health, err := f.store.Health(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.close(t)
	binary := filepath.Join(t.TempDir(), "haeryong")
	build := exec.Command("go", "build", "-race", "-o", binary, "../../../cmd/haeryong")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("production build: %v %s", err, out)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	daemon := startProductionDaemon(t, binary, f.selection, port)
	if daemon.id.LibraryID != health.Library.ID || daemon.id.ProfileID != health.Library.ProfileID {
		t.Fatal("production service loaded a substitute library")
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		var current domain.Job
		if status := daemonJSON(t, daemon.id.Endpoint, f.token, "GET", "/api/v1/jobs/"+job, "", nil, &current); status != 200 {
			t.Fatalf("production job status=%d", status)
		}
		if current.State == "completed" {
			break
		}
		if current.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("accepted local work did not complete: state=%s code=%s", current.State, current.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}
	caller := filepath.Join(t.TempDir(), "caller")
	if err := os.Mkdir(caller, 0700); err != nil {
		t.Fatal(err)
	}
	credential := filepath.Join(caller, "credential.json")
	encoded, _ := json.Marshal(client.Credential{Endpoint: daemon.id.Endpoint, Profile: f.selection.Profile, LibraryID: health.Library.ID, Token: f.token})
	if err := os.WriteFile(credential, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "operations", "show", operation, "--profile", f.selection.Profile, "--client-dir", caller, "--credential", credential, "--json")
	var callerDiagnostic bytes.Buffer
	command.Stderr = &callerDiagnostic
	if _, err := command.Output(); err != nil {
		t.Fatalf("real protected CLI could not reconcile the creator's original operation: %v %s", err, callerDiagnostic.String())
	}
	firstInstance := daemon.id.InstanceID
	daemon.kill(t)
	f.reopen(t)
	after, err := f.store.Health(f.ctx)
	if err != nil || after.Library != health.Library {
		t.Fatal("actual daemon SIGKILL changed logical runtime/auth/stream identity")
	}
	var head int64
	if err := f.store.Write(f.ctx, func(tx app.WriteTx) error { _, h, err := tx.EventHead(); head = h; return err }); err != nil {
		t.Fatal(err)
	}
	f.close(t)
	restarted := startProductionDaemon(t, binary, f.selection, port)
	if restarted.id.InstanceID == firstInstance || restarted.id.LibraryID != health.Library.ID {
		t.Fatal("production restart instance/library identity is dishonest")
	}
	body, _ := json.Marshal(map[string]any{"upload_id": upload, "document_id": nil, "version_id": version})
	var replay map[string]string
	if status := daemonJSON(t, restarted.id.Endpoint, f.token, "POST", "/api/v1/imports", operation, body, &replay); status != 202 || replay["job_id"] != job {
		t.Fatal("production restart did not replay the original accepted job")
	}
	restarted.kill(t)
	f.reopen(t)
	if err := f.store.Write(f.ctx, func(tx app.WriteTx) error {
		_, current, err := tx.EventHead()
		if err != nil || current != head {
			return errors.New("replay duplicated a mutation or event")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if published, err := f.store.Version(f.ctx, version); err != nil || published.Sequence != 1 {
		t.Fatal("production restart lost or duplicated the accepted immutable version")
	}
}
