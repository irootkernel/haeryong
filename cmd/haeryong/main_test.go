package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"

	"os/exec"
	"path/filepath"

	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/service"
)

var testBinary string

// The invoking check normally selects TMPDIR. Direct developer execution also
// chooses the mounted writable agent scratch volume before creating artifacts.

func cli(t GinkgoTInterface, expected int, args ...string) []byte {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, testBinary, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			Fail(fmt.Sprint(err))
		}
	}
	Expect(code != expected).To(BeFalse(), func() string {
		return fmt.Sprintf("%v: exit=%d want=%d stdout=%s stderr=%s", args, code, expected, out, &stderr)
	})

	return out
}

func selection(t GinkgoTInterface) service.Selection {
	GinkgoHelper()
	return service.Selection{Profile: "test", DataDir: filepath.Join(t.TempDir(), "library")}
}
func initProfile(t GinkgoTInterface, s service.Selection) service.Profile {
	GinkgoHelper()
	cli(t, 0, "init", "--profile", s.Profile, "--data-dir", s.DataDir, "--json")
	p, err := service.Load(s)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	return p
}
func profileArgs(s service.Selection) []string {
	return []string{"--profile", s.Profile, "--data-dir", s.DataDir, "--json"}
}
func availablePort(t GinkgoTInterface) int {
	GinkgoHelper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port
}

type daemon struct {
	cmd    *exec.Cmd
	done   chan error
	id     service.Identity
	stderr *bytes.Buffer
}

func startDaemon(t GinkgoTInterface, s service.Selection, port int, extra ...string) *daemon {
	GinkgoHelper()
	args := append([]string{"daemon", "run", "--port", fmt.Sprint(port)}, profileArgs(s)...)
	args = append(args, extra...)
	cmd := exec.Command(testBinary, args...)
	stderr := new(bytes.Buffer)
	cmd.Stderr = stderr
	out, err := cmd.StdoutPipe()
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	{

		err := cmd.Start()
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	d := &daemon{cmd: cmd, done: make(chan error, 1), stderr: stderr}
	DeferCleanup(func() {
		cmd.Process.Kill()
		select {
		case <-d.done:
		case <-time.After(8 * time.Second):
			Fail(fmt.Sprint("daemon cleanup timed out"))
		}
	})
	ready := make(chan []byte, 1)
	go func() {
		defer GinkgoRecover()
		scan := bufio.NewScanner(out)
		if scan.Scan() {
			ready <- append([]byte(nil), scan.Bytes()...)
		} else {
			ready <- nil
		}
		io.Copy(io.Discard, out)
	}()
	go func() { defer GinkgoRecover(); d.done <- cmd.Wait() }()
	select {
	case data := <-ready:
		var result struct {
			Data service.Identity `json:"data"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			Fail(fmt.Sprintf("startup not ready: %s (%v)", data, err))
		}
		d.id = result.Data
	case <-time.After(10 * time.Second):
		Fail(fmt.Sprint("daemon readiness timed out"))
	}
	Expect(!d.id.ControlReady || !d.id.Storage.Ready || d.id.Storage.State != "ready").To(BeFalse(), func() string { return fmt.Sprintf("incorrect readiness: %+v", d.id) })

	return d
}
func (d *daemon) wait(t GinkgoTInterface) {
	GinkgoHelper()
	select {
	case err := <-d.done:
		if err != nil {
			Fail(fmt.Sprintf("daemon exit: %v: %s", err, d.stderr))
		}
		d.done <- nil
	case <-time.After(8 * time.Second):
		Fail(fmt.Sprint("daemon stop timed out"))
	}
}

// Reject browser origins even with no product browser API implemented.

// Preserve a crash-like stale discovery record, then occupy its exact endpoint
// with an unrelated process returning the known identity but no private proof.

// Existing ordinary directories are not accepted as mounted volumes. This
// injects the unavailable mount condition without unmounting owner storage.

// Run an actual child test binary through TestMain without any selected test.
// The directory's prefix deliberately matches the old unsafe cleanup heuristic.
