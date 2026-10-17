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

	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

func startProductionDaemon(t GinkgoTInterface, binary string, selection service.Selection, port int) *productionDaemon {
	GinkgoHelper()
	command := exec.Command(binary, "daemon", "run", "--profile", selection.Profile, "--data-dir", selection.DataDir, "--port", fmt.Sprint(port), "--json")
	stdout, err := command.StdoutPipe()
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	var stderr bytes.Buffer
	command.Stderr = &stderr
	{
		err := command.Start()
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	d := &productionDaemon{command: command, done: make(chan error, 1)}
	go func() { defer GinkgoRecover(); d.done <- command.Wait() }()
	DeferCleanup(func() { d.kill(t) })
	ready := make(chan []byte, 1)
	go func() {
		defer GinkgoRecover()
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
			Fail(fmt.Sprintf("production daemon did not become ready: %s", stderr.String()))
		}
		d.id = response.Data
	case <-time.After(15 * time.Second):
		d.kill(t)
		Fail(fmt.Sprint("production daemon readiness deadline"))
	}
	return d
}

func (d *productionDaemon) kill(t GinkgoTInterface) {
	GinkgoHelper()
	if d.stopped {
		return
	}
	d.stopped = true
	{
		err := d.command.Process.Kill()
		Expect(err != nil && !errors.Is(err, os.ErrProcessDone)).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	select {
	case err := <-d.done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || !exit.Sys().(syscall.WaitStatus).Signaled() || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			Fail(fmt.Sprint("production daemon termination was not actual SIGKILL"))
		}
	case <-time.After(10 * time.Second):
		Fail(fmt.Sprint("production daemon did not release after process kill"))
	}
}

func daemonJSON(t GinkgoTInterface, endpoint, token, method, path, operation string, body []byte, result any) int {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(body))
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", endpoint)
	}
	if operation != "" {
		request.Header.Set("Idempotency-Key", operation)
	}
	response, err := http.DefaultClient.Do(request)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	defer response.Body.Close()
	if result != nil {
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		Expect(json.NewDecoder(response.Body).Decode(&envelope) != nil || json.Unmarshal(envelope.Data, result) != nil).To(BeFalse(), func() string { return fmt.Sprint("invalid production response envelope") })

	}
	return response.StatusCode
}

var _ = It("TestActualProductionDaemonRecoversAcceptedClientExitAndSurvivesSIGKILL", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	upload := f.create(t)
	checkpoint := f.put(t, upload, []byte("<html>real daemon independent of the accepted caller</html>"))
	seal := f.complete(t, upload, checkpoint, key(t))
	{
		err := f.core.Process(f.ctx, seal)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	principal, err := f.auth.Authenticate(f.ctx, f.token)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	operation, version := key(t), key(t)
	// Kill the creator process after its actual import acceptance transaction,
	// then run the unmodified production daemon with its real background worker.
	killAtBoundary(t, f, childRequest{Action: "import", Boundary: "accepted_commit_before_return", Upload: upload, Operation: operation, Version: version})
	receipt, err := f.store.Receipt(f.ctx, domain.OperationKey{PrincipalID: principal.PrincipalID, OperationID: operation})
	Expect(err != nil || receipt.State != "completed" || receipt.Response.Status != 202).To(BeFalse(), func() string { return fmt.Sprint("accepted caller's original creation receipt did not persist") })

	job := responseID(t, *receipt.Response, "job_id")
	health, err := f.store.Health(f.ctx)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	f.close(t)
	binary := filepath.Join(t.TempDir(), "haeryong")
	build := exec.Command("go", "build", "-race", "-o", binary, "../../../cmd/haeryong")
	{
		out, err := build.CombinedOutput()
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprintf("production build: %v %s", err, out) })
	}

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	daemon := startProductionDaemon(t, binary, f.selection, port)
	Expect(daemon.id.LibraryID != health.Library.ID || daemon.id.ProfileID != health.Library.ProfileID).To(BeFalse(), func() string { return fmt.Sprint("production service loaded a substitute library") })

	deadline := time.Now().Add(20 * time.Second)
	for {
		var current domain.Job
		{
			status := daemonJSON(t, daemon.id.Endpoint, f.token, "GET", "/api/v1/jobs/"+job, "", nil, &current)
			Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("production job status=%d", status) })
		}

		if current.State == "completed" {
			break
		}
		Expect(current.State == "failed" || time.Now().After(deadline)).To(BeFalse(), func() string {
			return fmt.Sprintf("accepted local work did not complete: state=%s code=%s", current.State, current.Error)
		})

		time.Sleep(50 * time.Millisecond)
	}
	caller := filepath.Join(t.TempDir(), "caller")
	{
		err := os.Mkdir(caller, 0700)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	credential := filepath.Join(caller, "credential.json")
	encoded, _ := json.Marshal(client.Credential{Endpoint: daemon.id.Endpoint, Profile: f.selection.Profile, LibraryID: health.Library.ID, Token: f.token})
	{
		err := os.WriteFile(credential, encoded, 0600)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	command := exec.Command(binary, "operations", "show", operation, "--profile", f.selection.Profile, "--client-dir", caller, "--credential", credential, "--json")
	var callerDiagnostic bytes.Buffer
	command.Stderr = &callerDiagnostic
	{
		_, err := command.Output()
		Expect(err != nil).To(BeFalse(), func() string {
			return fmt.Sprintf("real protected CLI could not reconcile the creator's original operation: %v %s", err, callerDiagnostic.String())
		})
	}

	firstInstance := daemon.id.InstanceID
	daemon.kill(t)
	f.reopen(t)
	after, err := f.store.Health(f.ctx)
	Expect(err != nil || after.Library != health.Library).To(BeFalse(), func() string { return fmt.Sprint("actual daemon SIGKILL changed logical runtime/auth/stream identity") })

	var head int64
	{
		err := f.store.Write(f.ctx, func(tx app.WriteTx) error { _, h, err := tx.EventHead(); head = h; return err })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	f.close(t)
	restarted := startProductionDaemon(t, binary, f.selection, port)
	Expect(restarted.id.InstanceID == firstInstance || restarted.id.LibraryID != health.Library.ID).To(BeFalse(), func() string { return fmt.Sprint("production restart instance/library identity is dishonest") })

	body, _ := json.Marshal(map[string]any{"upload_id": upload, "document_id": nil, "version_id": version})
	var replay map[string]string
	{
		status := daemonJSON(t, restarted.id.Endpoint, f.token, "POST", "/api/v1/imports", operation, body, &replay)
		Expect(status != 202 || replay["job_id"] != job).To(BeFalse(), func() string { return fmt.Sprint("production restart did not replay the original accepted job") })
	}

	restarted.kill(t)
	f.reopen(t)
	{
		err := f.store.Write(f.ctx, func(tx app.WriteTx) error {
			_, current, err := tx.EventHead()
			if err != nil || current != head {
				return errors.New("replay duplicated a mutation or event")
			}
			return nil
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		published, err := f.store.Version(f.ctx, version)
		Expect(err != nil || published.Sequence != 1).To(BeFalse(), func() string {
			return fmt.Sprint("production restart lost or duplicated the accepted immutable version")
		})
	}

})
