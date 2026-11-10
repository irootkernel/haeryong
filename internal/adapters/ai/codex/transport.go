// Package codex implements the personal host-local Codex app-server transport.
// Wire payloads, inherited configuration and native diagnostics are never logs.
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var errTransport = errors.New("codex transport unavailable")
var errCleanup = errors.New("owned codex cleanup unverified")
var errVersion = errors.New("unverified codex binary or version")

type rpcError struct {
	Code int `json:"code"`
}
type envelope struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}
type turn struct {
	ItemsView string `json:"itemsView"`
	ID        string `json:"id"`
	Status    string `json:"status"`
	Items     []struct {
		Type  string `json:"type"`
		Text  string `json:"text"`
		Phase string `json:"phase"`
	} `json:"items"`
	Error *struct {
		Info json.RawMessage `json:"codexErrorInfo"`
	} `json:"error"`
}

// Omission has the pinned defaultFull meaning; explicit null is not full.
func (v *turn) UnmarshalJSON(raw []byte) error {
	type wire turn
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return errTransport
	}
	decoded := wire{ItemsView: "full"}
	if json.Unmarshal(raw, &decoded) != nil {
		return errTransport
	}
	if value, ok := fields["itemsView"]; ok && string(value) == "null" {
		decoded.ItemsView = "invalid"
	}
	*v = turn(decoded)
	return nil
}

type completed struct {
	ThreadID string `json:"threadId"`
	Turn     turn   `json:"turn"`
}
type transport struct {
	cmd           *exec.Cmd
	in            io.WriteCloser
	out           io.ReadCloser
	writes        sync.Mutex
	mu            sync.Mutex
	pending       map[string]chan envelope
	next          atomic.Uint64
	done          chan struct{}
	turns         chan completed
	closeOnce     sync.Once
	closeErr      error
	waitDone      chan struct{}
	cleanupReason string
	signal        func(int, syscall.Signal) error
	owned         ownedNativeProcess
	setupErrors   *setupErrorClassifier
}

type ownedNativeProcess struct {
	PID      int  `json:"pid"`
	PGID     *int `json:"pgid"`
	UID      *int `json:"uid"`
	Observed bool `json:"observed"`
}

// Native diagnostics are never retained as text. Only fatal setup lines select
// these finite classes; harmless PATH-alias warnings cannot establish a cause.
type setupErrorClassifier struct {
	mu           sync.Mutex
	prefix, tail string
	class        string
}

func (w *setupErrorClassifier) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, b := range p {
		if b == '\n' {
			w.prefix, w.tail = "", ""
			continue
		}
		if len(w.prefix) < 16 {
			w.prefix += string(b)
		}
		w.tail += string(b)
		if len(w.tail) > 256 {
			w.tail = w.tail[len(w.tail)-256:]
		}
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(w.prefix)), "error:") {
			continue
		}
		s := strings.ToLower(w.tail)
		switch {
		case strings.Contains(s, "permission") && strings.Contains(s, "profile") && strings.Contains(s, "required"):
			w.class = "permission_profile_required"
		case strings.Contains(s, "operation not permitted"):
			w.class = "operation_not_permitted"
		case strings.Contains(s, "permission denied"):
			w.class = "permission_denied"
		case strings.Contains(s, "config") && (w.class == "" || w.class == "setup_error"):
			w.class = "configuration_error"
		case w.class == "":
			w.class = "setup_error"
		}
	}
	return len(p), nil
}

func (w *setupErrorClassifier) Class() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.class
}

func observeOwnedProcess(pid int) ownedNativeProcess {
	fact := ownedNativeProcess{PID: pid}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "pid=,pgid=,uid=,stat=")
	command.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	command.Stderr = io.Discard
	raw, err := command.Output()
	fields := strings.Fields(string(raw))
	if err != nil || len(fields) != 4 {
		return fact
	}
	observedPID, p := strconv.Atoi(fields[0])
	group, g := strconv.Atoi(fields[1])
	uid, u := strconv.Atoi(fields[2])
	if p == nil && g == nil && u == nil && observedPID == pid && uid == os.Geteuid() {
		fact.PGID, fact.UID, fact.Observed = &group, &uid, true
	}
	return fact
}

type initializationFailure struct {
	cause         error
	exitCode      *int
	localClosed   bool
	cleanupReason string
	owned         ownedNativeProcess
	errorClass    string
}

func (e *initializationFailure) Error() string { return "native initialization failed" }
func (e *initializationFailure) Unwrap() error { return e.cause }

func start(ctx context.Context, binary string) (*transport, error) {
	return startPrepared(ctx, binary, nil)
}

func startPrepared(ctx context.Context, binary string, retain func(ownedNativeProcess) error) (*transport, error) {
	cmd := exec.Command(binary, "app-server", "--stdio")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = subscriptionEnvironment()
	setupErrors := &setupErrorClassifier{}
	cmd.Stderr = setupErrors
	in, e := cmd.StdinPipe()
	if e != nil {
		return nil, errors.Join(errTransport, e)
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		in.Close()
		return nil, errors.Join(errTransport, e)
	}
	if e = cmd.Start(); e != nil {
		in.Close()
		out.Close()
		return nil, errors.Join(errTransport, e)
	}
	t := &transport{cmd: cmd, in: in, out: out, pending: map[string]chan envelope{}, done: make(chan struct{}), turns: make(chan completed, 8), signal: syscall.Kill, owned: observeOwnedProcess(cmd.Process.Pid), setupErrors: setupErrors}
	go t.read()
	failed := func(cause error) (*transport, error) {
		cleanup := t.close()
		fact := &initializationFailure{cause: errors.Join(cause, cleanup), localClosed: cleanup == nil, cleanupReason: t.cleanupReason, owned: t.owned, errorClass: setupErrors.Class()}
		if state := t.cmd.ProcessState; state != nil && state.Exited() {
			code := state.ExitCode()
			fact.exitCode = &code
		}
		return nil, fact
	}
	if retain != nil {
		if e = retain(t.owned); e != nil {
			return failed(e)
		}
	}
	if e = t.call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "haeryong", "version": "1"}}, nil); e != nil {
		return failed(e)
	}
	if e = t.send(map[string]any{"jsonrpc": "2.0", "method": "initialized"}); e != nil {
		return failed(e)
	}
	return t, nil
}
func (t *transport) send(v any) error {
	t.writes.Lock()
	defer t.writes.Unlock()
	if e := json.NewEncoder(t.in).Encode(v); e != nil {
		return errTransport
	}
	return nil
}
func (t *transport) call(ctx context.Context, method string, params, out any) error {
	id := fmt.Sprint(t.next.Add(1))
	ch := make(chan envelope, 1)
	t.mu.Lock()
	t.pending[id] = ch
	t.mu.Unlock()
	defer func() { t.mu.Lock(); delete(t.pending, id); t.mu.Unlock() }()
	if e := t.send(map[string]any{"jsonrpc": "2.0", "id": json.Number(id), "method": method, "params": params}); e != nil {
		return e
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.done:
		return errTransport
	case v := <-ch:
		if v.Error != nil {
			return errTransport
		}
		if out != nil && json.Unmarshal(v.Result, out) != nil {
			return errTransport
		}
		return nil
	}
}
func (t *transport) read() {
	defer close(t.done)
	decoder := json.NewDecoder(t.out)
	for {
		var v envelope
		if decoder.Decode(&v) != nil {
			return
		}
		if len(v.ID) > 0 {
			if v.Method != "" {
				// Unsolicited permission/tool requests cannot expand this adapter's scope.
				if t.send(map[string]any{"jsonrpc": "2.0", "id": v.ID, "error": map[string]any{"code": -32601, "message": "unsupported request"}}) != nil {
					return
				}
			} else {
				t.mu.Lock()
				ch := t.pending[string(v.ID)]
				t.mu.Unlock()
				if ch != nil {
					select {
					case ch <- v:
					default:
						return
					}
				}
			}
		} else if v.Method == "turn/completed" {
			var event completed
			if json.Unmarshal(v.Params, &event) != nil || event.ThreadID == "" || event.Turn.ID == "" {
				return
			}
			switch event.Turn.Status {
			case "completed", "failed", "interrupted":
			default:
				return
			}
			{
				select {
				case t.turns <- event:
				default:
					return
				}
			}
		}
	}
}
func (t *transport) close() error {
	t.closeOnce.Do(func() {
		_ = t.in.Close()
		// Keep the leader unreaped until all signals and group checks finish.
		// This prevents group-ID reuse from redirecting a later cleanup signal.
		term := t.signal(-t.cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-t.done:
		case <-time.After(200 * time.Millisecond):
		}
		kill := t.signal(-t.cmd.Process.Pid, syscall.SIGKILL)
		_ = t.out.Close()
		groupStopped := false
		deadline := time.Now().Add(2 * time.Second)
		for {
			stopped, e := ownedGroupStopped(t.cmd.Process.Pid)
			if e != nil {
				t.cleanupReason = "group_probe_failed"
				break
			}
			if stopped {
				groupStopped = true
				break
			}
			if time.Now().After(deadline) {
				t.cleanupReason = "owned_group_still_running"
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		waited := make(chan error, 1)
		t.waitDone = make(chan struct{})
		go func() { waited <- t.cmd.Wait(); close(t.waitDone) }()
		leaderStopped := false
		select {
		case e := <-waited:
			var exit *exec.ExitError
			leaderStopped = t.cmd.ProcessState != nil && (e == nil || errors.As(e, &exit))
			if !leaderStopped {
				t.cleanupReason = "process_wait_failed"
			}
		case <-time.After(3 * time.Second):
			t.cleanupReason = "process_wait_unknown"
		}
		readersStopped := false
		select {
		case <-t.done:
			readersStopped = true
		case <-time.After(time.Second):
			t.cleanupReason = "reader_wait_unknown"
		}
		if !groupStopped || !leaderStopped || !readersStopped {
			t.closeErr = errCleanup
		}
		if (term != nil && !errors.Is(term, syscall.ESRCH)) || (kill != nil && !errors.Is(kill, syscall.ESRCH)) {
			if t.closeErr == nil {
				t.cleanupReason = "signal_error_drained"
			} else {
				t.cleanupReason = "signal_error_cleanup_unknown"
			}
		}
	})
	return t.closeErr
}

// Like the common runner, accept only absence or same-user zombies as a
// drained owned group. A successful ps exit with empty/partial data is no proof.
func ownedGroupStopped(group int) (bool, error) {
	if e := syscall.Kill(-group, 0); errors.Is(e, syscall.ESRCH) {
		return true, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,pgid=,uid=,stat=")
	command.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	command.Stderr = io.Discard
	raw, e := command.Output()
	if e != nil {
		return false, errTransport
	}
	caller := false
	stopped := true
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 4 {
			return false, errTransport
		}
		pid, e := strconv.Atoi(fields[0])
		if e != nil {
			return false, errTransport
		}
		pgid, e := strconv.Atoi(fields[1])
		if e != nil {
			return false, errTransport
		}
		uid, e := strconv.Atoi(fields[2])
		if e != nil {
			return false, errTransport
		}
		if pid == os.Getpid() && uid == os.Geteuid() {
			caller = true
		}
		if pgid == group && (uid != os.Geteuid() || !strings.HasPrefix(fields[3], "Z")) {
			stopped = false
		}
	}
	if !caller {
		return false, errTransport
	}
	return stopped, nil
}

func (t *transport) submit(ctx context.Context, params io.Reader) (turn, error) {
	stop := context.AfterFunc(ctx, func() { _ = t.close() })
	defer stop()
	id := fmt.Sprint(t.next.Add(1))
	ch := make(chan envelope, 1)
	t.mu.Lock()
	t.pending[id] = ch
	t.mu.Unlock()
	defer func() { t.mu.Lock(); delete(t.pending, id); t.mu.Unlock() }()
	t.writes.Lock()
	_, e := fmt.Fprintf(t.in, `{"jsonrpc":"2.0","id":%s,"method":"turn/start","params":`, id)
	if e == nil {
		_, e = io.Copy(t.in, params)
	}
	if e == nil {
		_, e = io.WriteString(t.in, "}\n")
	}
	t.writes.Unlock()
	if e != nil {
		return turn{}, errTransport
	}
	var response envelope
	select {
	case <-ctx.Done():
		return receivedTurn(ch, ctx.Err())
	case <-t.done:
		return receivedTurn(ch, errTransport)
	case response = <-ch:
	}
	return decodeAcceptedTurn(response)
}

// Only submit's complete frame write reaches this receive. A response already
// delivered by the reader remains authoritative when local cancellation/EOF wins.
func receivedTurn(ch <-chan envelope, fallback error) (turn, error) {
	select {
	case response := <-ch:
		return decodeAcceptedTurn(response)
	default:
		return turn{}, fallback
	}
}
func decodeAcceptedTurn(response envelope) (turn, error) {
	var result struct {
		Turn turn `json:"turn"`
	}
	if response.Error != nil || json.Unmarshal(response.Result, &result) != nil || result.Turn.ID == "" {
		return turn{}, errTransport
	}
	return result.Turn, nil
}

func subscriptionEnvironment() []string {
	environment := []string{}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "OPENAI_API_KEY", "CODEX_API_KEY", "OPENAI_ADMIN_KEY", "AZURE_OPENAI_API_KEY", "OPENAI_BASE_URL":
			continue
		}
		environment = append(environment, entry)
	}

	return environment
}
