package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"haeryong.local/haeryong/internal/application/service"
)

var testBinary string

func TestMain(m *testing.M) { os.Exit(testMain(m)) }

func testMain(m *testing.M) int {
	// The invoking check normally selects TMPDIR. Direct developer execution also
	// chooses the mounted writable agent scratch volume before creating artifacts.
	if runtime.GOOS == "darwin" {
		mounts, err := exec.Command("mount").Output()
		if err == nil && bytes.Contains(mounts, []byte(" on /Volumes/RootKernel (")) {
			parent := "/Volumes/RootKernel/tmp"
			if info, err := os.Stat(parent); err == nil && info.IsDir() {
				if scratch, err := os.MkdirTemp(parent, "haeryong-lifecycle-tests-*"); err == nil {
					defer os.RemoveAll(scratch)
					if err := os.Setenv("TMPDIR", scratch); err != nil {
						fmt.Fprintln(os.Stderr, err)
						return 1
					}
				}
			}
		}
	}
	scratch, err := os.MkdirTemp(os.TempDir(), "haeryong-binary-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(scratch)
	testBinary = filepath.Join(scratch, "haeryong")
	cmd := exec.Command("go", "build", "-race", "-o", testBinary, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintln(os.Stderr, string(out), err)
		return 1
	}
	return m.Run()
}

func cli(t *testing.T, expected int, args ...string) []byte {
	t.Helper()
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
			t.Fatal(err)
		}
	}
	if code != expected {
		t.Fatalf("%v: exit=%d want=%d stdout=%s stderr=%s", args, code, expected, out, &stderr)
	}
	return out
}

func selection(t *testing.T) service.Selection {
	t.Helper()
	return service.Selection{Profile: "test", DataDir: filepath.Join(t.TempDir(), "library")}
}
func initProfile(t *testing.T, s service.Selection) service.Profile {
	t.Helper()
	cli(t, 0, "init", "--profile", s.Profile, "--data-dir", s.DataDir, "--json")
	p, err := service.Load(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func profileArgs(s service.Selection) []string {
	return []string{"--profile", s.Profile, "--data-dir", s.DataDir, "--json"}
}
func availablePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
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

func startDaemon(t *testing.T, s service.Selection, port int) *daemon {
	t.Helper()
	args := append([]string{"daemon", "run", "--port", fmt.Sprint(port)}, profileArgs(s)...)
	cmd := exec.Command(testBinary, args...)
	stderr := new(bytes.Buffer)
	cmd.Stderr = stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	d := &daemon{cmd: cmd, done: make(chan error, 1), stderr: stderr}
	t.Cleanup(func() {
		cmd.Process.Kill()
		select {
		case <-d.done:
		case <-time.After(8 * time.Second):
			t.Error("daemon cleanup timed out")
		}
	})
	ready := make(chan []byte, 1)
	go func() {
		scan := bufio.NewScanner(out)
		if scan.Scan() {
			ready <- append([]byte(nil), scan.Bytes()...)
		} else {
			ready <- nil
		}
		io.Copy(io.Discard, out)
	}()
	go func() { d.done <- cmd.Wait() }()
	select {
	case data := <-ready:
		var result struct {
			Data service.Identity `json:"data"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatalf("startup not ready: %s (%v)", data, err)
		}
		d.id = result.Data
	case <-time.After(10 * time.Second):
		t.Fatal("daemon readiness timed out")
	}
	if !d.id.ControlReady || !d.id.Storage.Ready || d.id.Storage.State != "ready" {
		t.Fatalf("incorrect readiness: %+v", d.id)
	}
	return d
}
func (d *daemon) wait(t *testing.T) {
	t.Helper()
	select {
	case err := <-d.done:
		if err != nil {
			t.Fatalf("daemon exit: %v: %s", err, d.stderr)
		}
		d.done <- nil
	case <-time.After(8 * time.Second):
		t.Fatal("daemon stop timed out")
	}
}

func TestExplicitInitializationAndAliasWriter(t *testing.T) {
	s := selection(t)
	cli(t, 5, append([]string{"daemon", "run"}, profileArgs(s)...)...)
	if _, err := os.Stat(s.DataDir); !os.IsNotExist(err) {
		t.Fatal("daemon implicitly initialized profile")
	}
	p := initProfile(t, s)
	cli(t, 5, append([]string{"init"}, profileArgs(s)...)...)
	d := startDaemon(t, s, availablePort(t))
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(s.DataDir, alias); err != nil {
		t.Fatal(err)
	}
	aliasSelection := s
	aliasSelection.DataDir = alias
	cli(t, 4, append([]string{"daemon", "run", "--port", fmt.Sprint(availablePort(t))}, profileArgs(aliasSelection)...)...)
	actual, err := service.Load(aliasSelection)
	if err != nil || actual.LibraryID != p.LibraryID || actual.ProfileID != p.ProfileID || actual.ControlKey != p.ControlKey || actual.DataDir != p.DataDir || actual.StorageState != "ready" {
		t.Fatalf("alias did not resolve same profile: %v", err)
	}
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(aliasSelection)...)...)
	writer, err := service.OpenWriter(s)
	if err != nil {
		t.Fatalf("stop returned before writer release: %v", err)
	}
	writer.Close()
	d.wait(t)
	if _, err := os.Stat(filepath.Join(s.DataDir, "runtime", "endpoint.json")); !os.IsNotExist(err) {
		t.Fatal("shutdown retained readiness")
	}
	restarted := startDaemon(t, s, availablePort(t))
	if restarted.id.LibraryID != p.LibraryID || restarted.id.ProfileID != p.ProfileID || restarted.id.InstanceID == d.id.InstanceID {
		t.Fatal("restart identity continuity failed")
	}
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(s)...)...)
	restarted.wait(t)
}

func TestListenerCollisionAndImpersonation(t *testing.T) {
	s := selection(t)
	initProfile(t, s)
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := occupied.Addr().(*net.TCPAddr).Port
	cli(t, 4, append([]string{"daemon", "run", "--port", fmt.Sprint(port)}, profileArgs(s)...)...)
	occupied.Close()
	if _, err := os.Stat(filepath.Join(s.DataDir, "runtime", "endpoint.json")); !os.IsNotExist(err) {
		t.Fatal("collision published readiness")
	}
	d := startDaemon(t, s, port)
	cli(t, 0, append([]string{"status"}, profileArgs(s)...)...)
	response, err := http.Get(d.id.Endpoint + "/health/live")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "{\"live\":true}\n" {
		t.Fatalf("public health exposed metadata: %s", body)
	}
	for _, path := range []string{"/local/v1/identity", "/local/v1/stop", "/api/v1/capabilities"} {
		method := http.MethodGet
		if strings.HasSuffix(path, "/stop") {
			method = http.MethodPost
		}
		req, _ := http.NewRequest(method, d.id.Endpoint+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 401 && resp.StatusCode != 404 {
			t.Errorf("unauthorized %s: %d", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
	// Reject browser origins even with no product browser API implemented.
	req, _ := http.NewRequest("GET", d.id.Endpoint+"/health/live", nil)
	req.Header.Set("Origin", d.id.Endpoint)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("browser origin not rejected")
	}
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(s)...)...)
	d.wait(t)
	// Preserve a crash-like stale discovery record, then occupy its exact endpoint
	// with an unrelated process returning the known identity but no private proof.
	raw, _ := json.Marshal(d.id)
	if err := os.WriteFile(filepath.Join(s.DataDir, "runtime", "endpoint.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	requestedStop := make(chan bool, 1)
	fake := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Authorization"), "Bearer") {
			t.Error("client disclosed bearer credential")
		}
		if r.URL.Path == "/local/v1/stop" {
			requestedStop <- true
		}
		w.Header().Set("X-Haeryong-Proof", r.Header.Get("X-Haeryong-Proof"))
		w.Write(raw)
	}))
	fake.Listener = listener
	fake.Start()
	defer fake.Close()
	cli(t, 5, append([]string{"status"}, profileArgs(s)...)...)
	cli(t, 5, append([]string{"service", "stop"}, profileArgs(s)...)...)
	select {
	case <-requestedStop:
		t.Fatal("sent stop to unverified listener")
	default:
	}
}

func TestSignalsCrashAndRestart(t *testing.T) {
	s := selection(t)
	p := initProfile(t, s)
	port := availablePort(t)
	d := startDaemon(t, s, port)
	if err := d.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	d.wait(t)
	interrupted := startDaemon(t, s, port)
	if err := interrupted.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	interrupted.wait(t)
	restarted := startDaemon(t, s, port)
	if err := restarted.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-restarted.done:
		if err == nil {
			t.Fatal("SIGKILL unexpectedly clean")
		}
		restarted.done <- err
	case <-time.After(8 * time.Second):
		t.Fatal("SIGKILL timeout")
	}
	cli(t, 5, append([]string{"status"}, profileArgs(s)...)...)
	recovered := startDaemon(t, s, port)
	if recovered.id.LibraryID != p.LibraryID || recovered.id.InstanceID == restarted.id.InstanceID {
		t.Fatal("crash recovery identity failed")
	}
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(s)...)...)
	recovered.wait(t)
}

func TestUnavailableVolumeNoSubstitute(t *testing.T) {
	s := selection(t)
	initProfile(t, s)
	hidden := s.DataDir + "-offline"
	if err := os.Rename(s.DataDir, hidden); err != nil {
		t.Fatal(err)
	}
	cli(t, 5, append([]string{"daemon", "run"}, profileArgs(s)...)...)
	cli(t, 5, append([]string{"status"}, profileArgs(s)...)...)
	if _, err := os.Stat(s.DataDir); !os.IsNotExist(err) {
		t.Fatal("missing configured path recreated")
	}
	if runtime.GOOS == "darwin" {
		id, _ := service.NewID()
		root := filepath.Join("/Volumes", "haeryong-unavailable-"+id)
		s.DataDir = filepath.Join(root, "library")
		cli(t, 5, append([]string{"init"}, profileArgs(s)...)...)
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("missing volume substitute created")
		}
	}
	// Existing ordinary directories are not accepted as mounted volumes. This
	// injects the unavailable mount condition without unmounting owner storage.
	root := t.TempDir()
	s.DataDir = filepath.Join(root, "library")
	args := append([]string{"init", "--volume-root", root}, profileArgs(s)...)
	cli(t, 5, args...)
	if _, err := os.Stat(s.DataDir); !os.IsNotExist(err) {
		t.Fatal("unmounted volume root initialized")
	}
}

func TestInvalidPortsAndUnsupportedCapabilities(t *testing.T) {
	opt, err := parse([]string{"daemon", "run"})
	if err != nil || opt.port != 9270 {
		t.Fatal("default port changed")
	}
	for _, port := range []string{"0", "-1", "65536", "random"} {
		cli(t, 2, "daemon", "run", "--port", port, "--json")
	}
	cli(t, 5, "service", "install", "--json")
	cli(t, 5, "auth", "pair", "--json")
	cli(t, 2, "init", "--endpoint", "https://example.invalid", "--json")
}

func TestPortBoundariesAndWarnings(t *testing.T) {
	for _, test := range []struct {
		port           string
		valid, warning bool
	}{
		{"1", false, false}, {"1023", false, false}, {"1024", true, true},
		{"8999", true, true}, {"9000", true, false}, {"9270", true, false},
		{"9999", true, false}, {"10000", true, true}, {"65535", true, true}, {"65536", false, false},
	} {
		t.Run(test.port, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), []string{"--json", "version", "--port", test.port}, &stdout, &stderr)
			expected := 0
			if !test.valid {
				expected = 2
			}
			if code != expected {
				t.Fatalf("exit=%d want=%d stderr=%s", code, expected, &stderr)
			}
			decoder := json.NewDecoder(&stdout)
			var response envelope
			if err := decoder.Decode(&response); err != nil {
				t.Fatalf("stdout not JSON: %v", err)
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				t.Fatalf("extra stdout: %v", err)
			}
			warning := strings.Contains(stderr.String(), "outside preferred range 9000..9999")
			if warning != test.warning {
				t.Fatalf("warning=%v want=%v stderr=%s", warning, test.warning, &stderr)
			}
		})
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"version", "--json"}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("default port warning: exit=%d stderr=%s", code, &stderr)
	}
}

func TestTestMainPreservesCallerTemporaryDirectory(t *testing.T) {
	caller, err := os.MkdirTemp(t.TempDir(), "haeryong-lifecycle-tests-caller-*")
	if err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(caller, "owner-sentinel")
	if err := os.WriteFile(sentinel, []byte("caller-owned"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Run an actual child test binary through TestMain without any selected test.
	// The directory's prefix deliberately matches the old unsafe cleanup heuristic.
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "TMPDIR="+caller)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child test process: %v: %s", err, out)
	}
	content, err := os.ReadFile(sentinel)
	if err != nil || string(content) != "caller-owned" {
		t.Fatalf("caller scratch removed or altered: %v", err)
	}
	entries, err := os.ReadDir(caller)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "owner-sentinel" {
		t.Fatalf("owned test artifacts not cleaned: %v", entries)
	}
}
