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
}

func start(ctx context.Context, binary string) (*transport, error) {
	cmd := exec.Command(binary, "app-server", "--stdio")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = subscriptionEnvironment()
	discard, e := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if e != nil {
		return nil, errTransport
	}
	defer discard.Close()
	cmd.Stderr = discard
	in, e := cmd.StdinPipe()
	if e != nil {
		return nil, errTransport
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		in.Close()
		return nil, errTransport
	}
	if e = cmd.Start(); e != nil {
		in.Close()
		out.Close()
		return nil, errTransport
	}
	t := &transport{cmd: cmd, in: in, out: out, pending: map[string]chan envelope{}, done: make(chan struct{}), turns: make(chan completed, 8), signal: syscall.Kill}
	go t.read()
	if e = t.call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "haeryong", "version": "1"}}, nil); e != nil {
		return nil, errors.Join(e, t.close())
	}
	if e = t.send(map[string]any{"jsonrpc": "2.0", "method": "initialized"}); e != nil {
		return nil, errors.Join(e, t.close())
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
