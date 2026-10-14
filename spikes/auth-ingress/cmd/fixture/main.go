// Disposable auth-ingress harness; G-AUTH requires separate gate evidence.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	preparation "haeryong.local/auth-ingress"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return errors.New("serve or approve required")
	}
	flags := flag.NewFlagSet(os.Args[1], flag.ContinueOnError)
	directory := flags.String("directory", "", "Owned private fixture directory")
	listen := flags.String("listen", "127.0.0.1:0", "Loopback listener; use the same port for restart checks")
	remoteOrigin := flags.String("remote-origin", "", "Explicit approved private HTTPS origin")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if *directory == "" {
		return errors.New("private fixture directory required")
	}
	if os.Args[1] == "approve" {
		return approve(*directory)
	}
	if os.Args[1] != "serve" {
		return errors.New("unknown command")
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || host != "127.0.0.1" {
		return errors.New("loopback listen address required")
	}
	if err := os.MkdirAll(*directory, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(*directory)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return errors.New("fixture directory must be private with mode 0700")
	}
	lock, err := privateFile(filepath.Join(*directory, "serve.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("fixture directory already has a running server")
	}
	listener, err := net.Listen("tcp4", *listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	endpoint := "http://" + listener.Addr().String()
	hosts := map[string]bool{listener.Addr().String(): true}
	origins := map[string]bool{endpoint: true}
	if *remoteOrigin != "" {
		remote, err := url.Parse(*remoteOrigin)
		if err != nil || remote.Scheme != "https" || remote.Host == "" || remote.User != nil || remote.Path != "" || remote.RawQuery != "" || remote.Fragment != "" {
			return errors.New("exact private HTTPS origin required")
		}
		hosts[remote.Host] = true
		origins[*remoteOrigin] = true
	}
	database := filepath.Join(*directory, "auth.db")
	if err := privateDatabase(database); err != nil {
		return err
	}
	handler, closeDB, err := preparation.Fixture(database, filepath.Join(*directory, "control"), hosts, origins)
	if err != nil {
		return err
	}
	defer closeDB()
	file, err := privateFile(filepath.Join(*directory, "endpoint"))
	if err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		file.Close()
		return err
	}
	_, writeErr := file.WriteString(endpoint)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	json.NewEncoder(os.Stdout).Encode(map[string]any{"fixture": "auth-ingress", "endpoint": endpoint, "pid": os.Getpid()})
	select {
	case err := <-finished:
		return err
	case <-ctx.Done():
		// Leave room for the five-second header timeout on browser preconnections.
		deadline, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		defer cancel()
		return server.Shutdown(deadline)
	}
}

// A retained record is reusable only inside the exclusively locked owned runtime.
// O_NOFOLLOW prevents a stale record from redirecting a write outside it.
func privateFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		file.Close()
		return nil, errors.New("fixture record must be a regular mode-0600 file")
	}
	return file, nil
}

// Reject stable redirected/private-file violations inside the exclusively owned
// runtime before SQLite opens its database or sidecars. This is not a boundary
// against another process with the same user's authority racing filesystem edits.
func privateDatabase(path string) error {
	file, err := privateFile(path)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecar := path + suffix
		if _, err := os.Lstat(sidecar); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		file, err := privateFile(sidecar)
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	return nil
}

func approve(directory string) error {
	controlPath := filepath.Join(directory, "control")
	if _, err := os.Stat(controlPath); err != nil {
		return err
	}
	control, err := preparation.LocalControl(controlPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(directory, "endpoint"))
	if err != nil {
		return err
	}
	endpoint, err := url.Parse(string(data))
	if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" || endpoint.Port() == "" || endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("invalid local endpoint")
	}
	var input struct{ ID, Code string }
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{"ID": input.ID, "Code": input.Code, "Decision": "approved"})
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, strings.TrimSuffix(endpoint.String(), "/")+"/pair/decision", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+control)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
		state := "not confirmed"
		if response.StatusCode == 400 || response.StatusCode == 403 {
			state = "denied"
		}
		if response.StatusCode == 429 || response.StatusCode == 503 {
			return fmt.Errorf("bootstrap approval temporarily unavailable: HTTP %d: %s; Retry-After: %s; wait before retrying the same exact request", response.StatusCode, strings.TrimSpace(string(detail)), response.Header.Get("Retry-After"))
		}
		return fmt.Errorf("bootstrap approval %s: HTTP %d: %s", state, response.StatusCode, strings.TrimSpace(string(detail)))
	}
	var accepted struct {
		ID, Label, Decision string
		Created, Expires    int64
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&accepted); err != nil {
		return err
	}
	// JSON escapes descriptive labels rather than interpreting terminal control bytes.
	fmt.Print("Approved exact fixture request: ")
	return json.NewEncoder(os.Stdout).Encode(accepted)
}
