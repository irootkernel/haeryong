package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

var fixtureChild = flag.Bool("fixture-child", false, "Run the actual fixture command in an isolated test child")

func TestFixtureProcess(t *testing.T) {
	if !*fixtureChild {
		return
	}
	os.Args = append([]string{"fixture"}, flag.Args()...)
	main()
	os.Exit(0)
}

func childCommand(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestFixtureProcess$", "-fixture-child", "--"}, args...)...)
}

func ownedDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	return directory
}

func startFixture(t *testing.T, directory string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	cmd := childCommand(ctx, "serve", "--directory", directory)
	var errors, output bytes.Buffer
	cmd.Stderr = &errors
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	drained := make(chan struct{})
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		<-drained
		err := cmd.Wait()
		cancel()
		control, readErr := os.ReadFile(filepath.Join(directory, "control"))
		if readErr != nil {
			t.Error("cannot verify control credential absence from serve output")
		} else {
			secret := bytes.TrimSpace(control)
			if len(secret) != 64 {
				t.Error("invalid control credential prevents leak verification")
			}
			record, err := os.ReadFile(filepath.Join(directory, "endpoint"))
			if err != nil {
				t.Error("endpoint read failed")
			}
			for _, data := range [][]byte{output.Bytes(), errors.Bytes(), record} {
				if len(secret) > 0 && bytes.Contains(data, secret) {
					t.Error("serve output or endpoint exposed control credential")
				}
			}
		}
		if err != nil {
			t.Errorf("fixture shutdown failed: %v", err)
		}
	})
	type announcement struct {
		Endpoint string
		PID      int
	}
	ready := make(chan announcement, 1)
	go func() {
		reader := io.TeeReader(stdout, &output)
		var a announcement
		_ = json.NewDecoder(reader).Decode(&a)
		ready <- a
		io.Copy(io.Discard, reader)
		close(drained)
	}()
	select {
	case a := <-ready:
		if a.PID != cmd.Process.Pid || !strings.HasPrefix(a.Endpoint, "http://127.0.0.1:") {
			t.Fatal("fixture did not announce listener")
		}
		return a.Endpoint
	case <-time.After(5 * time.Second):
		t.Fatal("fixture did not start")
	}
	return ""
}

func TestActualServeExcludesConcurrentRuntimeAndRewritesRetainedEndpoint(t *testing.T) {
	directory := ownedDirectory(t)
	t.Run("first server", func(t *testing.T) {
		endpoint := startFixture(t, directory)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		output, err := childCommand(ctx, "serve", "--directory", directory).CombinedOutput()
		if err == nil || !bytes.Contains(output, []byte("already has a running server")) {
			t.Fatal("actual second serve bypassed runtime lock", err)
		}
		retained, err := os.ReadFile(filepath.Join(directory, "endpoint"))
		if err != nil || string(retained) != endpoint {
			t.Fatal("second serve overwrote live endpoint", err)
		}
	}) // Actual SIGTERM/Wait releases the lock before the next run.
	if err := os.WriteFile(filepath.Join(directory, "endpoint"), []byte("http://127.0.0.1:1"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Run("restart", func(t *testing.T) {
		endpoint := startFixture(t, directory)
		record, err := os.ReadFile(filepath.Join(directory, "endpoint"))
		if err != nil || string(record) != endpoint {
			t.Fatal("actual restart did not replace stale endpoint", err)
		}
		client := &http.Client{Timeout: time.Second}
		response, err := client.Get(endpoint + "/session")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("restarted listener is not authenticated", response.StatusCode)
		}
	})
}

func TestActualServeRejectsUnsafeDirectoryAndStableSymlinks(t *testing.T) {
	for _, target := range []string{"directory-mode", "endpoint", "serve.lock", "auth.db", "auth.db-wal", "auth.db-shm"} {
		t.Run(target, func(t *testing.T) {
			directory := ownedDirectory(t)
			sentinel := filepath.Join(t.TempDir(), "sentinel")
			if err := os.WriteFile(sentinel, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			if target == "directory-mode" {
				if err := os.Chmod(directory, 0755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(sentinel, filepath.Join(directory, target)); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := childCommand(ctx, "serve", "--directory", directory).Run(); err == nil {
				t.Fatal("unsafe runtime admitted")
			}
			if ctx.Err() != nil {
				t.Fatal("unsafe runtime hung instead of rejecting")
			}
			data, err := os.ReadFile(sentinel)
			if err != nil || string(data) != "untouched" {
				t.Fatal("runtime redirected write", err)
			}
		})
	}
}

func TestActualApproveReportsDenialAndNonsecretMetadata(t *testing.T) {
	directory := ownedDirectory(t)
	endpoint := startFixture(t, directory)
	label := "CLI \"comparison\"\nfixture\x1b"
	pairBody, err := json.Marshal(map[string]string{"Label": label})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest("POST", endpoint+"/pair", bytes.NewReader(pairBody))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", endpoint)
	client := &http.Client{Timeout: time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var pending struct {
		ID, Code, Secret string
		Created, Expires int64
	}
	err = json.NewDecoder(response.Body).Decode(&pending)
	response.Body.Close()
	if err != nil || response.StatusCode != 201 {
		t.Fatal("pairing setup failed", err)
	}
	control, err := os.ReadFile(filepath.Join(directory, "control"))
	if err != nil {
		t.Fatal(err)
	}
	control = bytes.TrimSpace(control)
	for _, correct := range []bool{false, true} {
		code := "incorrect"
		if correct {
			code = pending.Code
		}
		body, _ := json.Marshal(map[string]string{"ID": pending.ID, "Code": code})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := childCommand(ctx, "approve", "--directory", directory)
		cmd.Stdin = bytes.NewReader(body)
		output, err := cmd.CombinedOutput()
		cancel()
		for _, secret := range [][]byte{control, []byte(pending.Secret), []byte(pending.Code)} {
			if bytes.Contains(output, secret) {
				t.Fatal("approval output exposed credential or comparison secret")
			}
		}
		if correct {
			prefix := []byte("Approved exact fixture request: ")
			if err != nil || !bytes.HasPrefix(output, prefix) {
				t.Fatal("approval descriptor missing", err)
			}
			var accepted struct {
				ID, Label, Decision string
				Created, Expires    int64
			}
			decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(output, prefix)))
			if decoder.Decode(&accepted) != nil || accepted.ID != pending.ID || accepted.Label != label || accepted.Decision != "approved" || accepted.Created != pending.Created || accepted.Expires != pending.Expires || accepted.Created <= 0 || accepted.Expires-accepted.Created != int64(5*time.Minute) {
				t.Fatal("approval did not preserve exact request context")
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF || bytes.Count(output, []byte("\n")) != 1 || bytes.Contains(output, []byte{0x1b}) {
				t.Fatal("approval label was not escaped as one JSON record")
			}
		} else if err == nil || !bytes.Contains(output, []byte("HTTP 403: decision denied")) {
			t.Fatal("actual denial body lost", err)
		}
	}
	request, err = http.NewRequest("POST", endpoint+"/pair/redeem", strings.NewReader(`{"ID":"`+pending.ID+`","Secret":"`+pending.Secret+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("wrong-code attempt consumed pending request", response.StatusCode)
	}
}

func TestActualServeRejectsNonprivateDatabaseRecords(t *testing.T) {
	for _, name := range []string{"auth.db", "auth.db-wal", "auth.db-shm"} {
		t.Run(name, func(t *testing.T) {
			directory := ownedDirectory(t)
			if err := os.WriteFile(filepath.Join(directory, name), []byte("untouched"), 0644); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := childCommand(ctx, "serve", "--directory", directory).Run(); err == nil || ctx.Err() != nil {
				t.Fatal("nonprivate database record not rejected")
			}
			data, err := os.ReadFile(filepath.Join(directory, name))
			if err != nil || string(data) != "untouched" {
				t.Fatal("rejected record changed", err)
			}
		})
	}
}

func TestActualServeBoundsStalledPairBody(t *testing.T) {
	endpoint := startFixture(t, ownedDirectory(t))
	address, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", address.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(7 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(conn, "POST /pair/poll HTTP/1.1\r\nHost: %s\r\nContent-Length: 64\r\n\r\n{", address.Host); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal("stalled pairing body lacked bounded response", err)
	}
	defer response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal("stalled body not rejected", response.StatusCode)
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
}

func TestActualApproveSeparatesRetryableResponses(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			directory := ownedDirectory(t)
			control := strings.Repeat("b", 64)
			if err := os.WriteFile(filepath.Join(directory, "control"), []byte(control+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+control {
					t.Error("CLI did not use protected credential")
				}
				w.Header().Set("Retry-After", "2")
				http.Error(w, "temporarily unavailable", status)
			}))
			defer srv.Close()
			if err := os.WriteFile(filepath.Join(directory, "endpoint"), []byte(srv.URL), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := childCommand(ctx, "approve", "--directory", directory)
			cmd.Stdin = strings.NewReader(`{"ID":"owned-request","Code":"01234567"}`)
			output, err := cmd.CombinedOutput()
			if err == nil || !bytes.Contains(output, []byte("temporarily unavailable")) || !bytes.Contains(output, []byte("Retry-After: 2")) || bytes.Contains(output, []byte("denied")) || bytes.Contains(output, []byte(control)) {
				t.Fatal("CLI lost retry distinction or exposed control credential")
			}
		})
	}
}

func TestOwnedRuntimeLockAndStaleRecord(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "serve.lock")
	first, err := privateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(first.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	second, err := privateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := syscall.Flock(int(second.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		t.Fatal("two servers acquired the same runtime")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(second.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal("stopped server kept runtime locked", err)
	}
	endpoint := filepath.Join(directory, "endpoint")
	if err := os.WriteFile(endpoint, []byte("http://127.0.0.1:9999"), 0600); err != nil {
		t.Fatal(err)
	}
	record, err := privateFile(endpoint)
	if err != nil {
		t.Fatal("retained endpoint prevented restart", err)
	}
	defer record.Close()
	if err := record.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := record.WriteString("http://127.0.0.1:9270"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(endpoint)
	if err != nil || string(got) != "http://127.0.0.1:9270" {
		t.Fatal("stale endpoint not replaced", err)
	}
}

func TestRuntimeRecordRejectsSymlinkAndPermissions(t *testing.T) {
	directory := t.TempDir()
	sentinel := filepath.Join(directory, "sentinel")
	if err := os.WriteFile(sentinel, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"endpoint", "serve.lock"} {
		path := filepath.Join(directory, name)
		if err := os.Symlink(sentinel, path); err != nil {
			t.Fatal(err)
		}
		if file, err := privateFile(path); err == nil {
			file.Close()
			t.Fatal("symlink accepted")
		}
	}
	got, err := os.ReadFile(sentinel)
	if err != nil || string(got) != "untouched" {
		t.Fatal("redirected runtime record modified sentinel", err)
	}
	public := filepath.Join(directory, "public")
	if err := os.WriteFile(public, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if file, err := privateFile(public); err == nil {
		file.Close()
		t.Fatal("nonprivate runtime record accepted")
	}
}

func TestRemoteOriginFlagRejectsNonHTTPSAndNonOrigins(t *testing.T) {
	original := os.Args
	defer func() { os.Args = original }()
	for _, value := range []string{"http://fixture.test", "https://owner@fixture.test", "https://fixture.test/path", "https://fixture.test?secret=x", "https://fixture.test#fragment"} {
		os.Args = []string{"fixture", "serve", "--directory", ownedDirectory(t), "--remote-origin", value}
		if err := run(); err == nil || err.Error() != "exact private HTTPS origin required" {
			t.Fatal("invalid private origin admitted", value)
		}
	}
}
