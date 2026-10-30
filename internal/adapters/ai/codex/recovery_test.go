package codex

import (
	"context"
	"encoding/json"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var _ = It("C03W4 completed full-history RPC and spool failures preserve recoverable exact identity", Label("integration"), func() {
	for _, mode := range []string{"full_rpc_error", "spool_error"} {
		d, a, inputs, _ := fixture(mode)
		out, e := d.Execute(context.Background(), a, inputs, func() error { return nil }, func(work.ProviderProgress) error {
			if mode == "spool_error" {
				d.Scratch = filepath.Join(d.Scratch, "absent")
			}
			return nil
		})
		Expect(e).To(HaveOccurred())
		Expect(out.State).To(Equal("outcome_unknown"))
		Expect(out.Reason).To(Equal("result_retrieval_pending"))
		Expect(out.CompletionObserved).To(BeTrue())
		Expect(out.Body).To(BeNil())
		Expect(out.RequestID).To(Equal("turn"))
		Expect(out.SessionID).To(Equal("thread"))
		Expect(out.Transport.BackendState).To(Equal("completed"))
		Expect(out.Transport.LocalClosed).To(BeTrue())
	}
	d, a, _, _ := fixture("full_rpc_error")
	a.RequestID = "turn"
	a.SessionID = "thread"
	a.Transport = &domain.ProviderTransport{Provider: "codex", Version: Version, BinarySHA: d.BinarySHA, SetupThread: "thread", CWD: d.Scratch}
	out, e := d.Reconcile(context.Background(), a, true)
	Expect(e).To(HaveOccurred())
	Expect(out.State).To(Equal("outcome_unknown"))
	Expect(out.Reason).To(Equal("result_retrieval_pending"))
	Expect(out.Lookup.BackendState).To(Equal("completed"))
	Expect(out.Body).To(BeNil())
})

var _ = It("C03W4 complete-write ACK fallback retains buffered identity and rejects absent malformed or error responses", Label("unit"), func() {
	for _, fallback := range []error{context.Canceled, errTransport} {
		for _, raw := range []string{`{"turn":{"id":"accepted","status":"inProgress"}}`, `{"turn":{}}`, `{`} {
			ch := make(chan envelope, 1)
			ch <- envelope{Result: json.RawMessage(raw)}
			value, e := receivedTurn(ch, fallback)
			if strings.Contains(raw, "accepted") {
				Expect(e).NotTo(HaveOccurred())
				Expect(value.ID).To(Equal("accepted"))
			} else {
				Expect(e).To(MatchError(errTransport))
				Expect(value.ID).To(BeEmpty())
			}
		}
		ch := make(chan envelope, 1)
		value, e := receivedTurn(ch, fallback)
		Expect(errors.Is(e, fallback)).To(BeTrue())
		Expect(value.ID).To(BeEmpty())
		ch <- envelope{Error: &rpcError{Code: -1}, Result: json.RawMessage(`{"turn":{"id":"invalid"}}`)}
		value, e = receivedTurn(ch, fallback)
		Expect(e).To(MatchError(errTransport))
		Expect(value.ID).To(BeEmpty())
	}
})

type submitWriter struct{ write func([]byte) (int, error) }

func (w submitWriter) Write(b []byte) (int, error) { return w.write(b) }
func (w submitWriter) Close() error                { return nil }

var _ = It("C03W4 submit accepts ACK only after the entire frame and keeps buffered EOF/cancel responses", Label("unit"), func() {
	for _, mode := range []string{"eof", "cancel", "partial", "absent", "malformed"} {
		ctx, cancel := context.WithCancel(context.Background())
		t := &transport{pending: map[string]chan envelope{}, done: make(chan struct{})}
		t.closeOnce.Do(func() {})
		wire := ""
		t.in = submitWriter{func(b []byte) (int, error) {
			wire += string(b)
			if mode == "partial" {
				t.pending["1"] <- envelope{Result: json.RawMessage(`{"turn":{"id":"must-not-ack"}}`)}
				return 0, io.ErrShortWrite
			}
			if string(b) == "}\n" {
				if mode != "absent" {
					raw := `{"turn":{"id":"accepted","status":"inProgress"}}`
					if mode == "malformed" {
						raw = `{"turn":{}}`
					}
					t.pending["1"] <- envelope{Result: json.RawMessage(raw)}
				}
				if mode == "cancel" {
					cancel()
				} else {
					close(t.done)
				}
			}
			return len(b), nil
		}}
		value, e := t.submit(ctx, strings.NewReader(`{"threadId":"thread","input":[]}`))
		cancel()
		if mode == "eof" || mode == "cancel" {
			Expect(e).NotTo(HaveOccurred())
			Expect(value.ID).To(Equal("accepted"))
			Expect(json.Valid([]byte(wire))).To(BeTrue())
		} else {
			Expect(e).To(HaveOccurred())
			Expect(value.ID).To(BeEmpty())
		}
	}
})

var _ = It("C03W4 every early preflight and fence exit closes owned transport without fabricating audit identity", Label("integration"), func() {
	for _, mode := range []string{"api_key", "quota", "wrong_mode", "null_config", "full_rpc_error"} {
		d, a, inputs, _ := fixture(mode)
		out, e := d.Execute(context.Background(), a, inputs, func() error { return domain.ErrDenied }, func(work.ProviderProgress) error { Fail("refused preparation cannot ACK"); return nil })
		Expect(e).NotTo(HaveOccurred())
		Expect(out.RequestID).To(BeEmpty())
		Expect(out.SessionID).To(BeEmpty())
		if mode == "full_rpc_error" {
			Expect(out.Transport).NotTo(BeNil())
			Expect(out.Transport.LocalClosed).To(BeTrue())
			Expect(out.Transport.CleanupState).To(Equal("drained"))
		} else {
			Expect(out.Transport).To(BeNil())
		}
		Expect(out.CleanupError).NotTo(HaveOccurred())
	}
})

var _ = It("C03W4 initialization disclosure audit and frame failures retain truthful available cleanup only", Label("integration"), func() {
	for _, mode := range []string{"initialize_error", "disclosure", "audit", "frame"} {
		fixtureMode := "complete"
		if mode == "initialize_error" {
			fixtureMode = mode
		}
		d, a, inputs, s := fixture(fixtureMode)
		switch mode {
		case "disclosure":
			s.tx.failGuardAt = 3
		case "audit":
			s.tx.recordError = io.ErrClosedPipe
		case "frame":
			d.Scratch = filepath.Join(d.Scratch, "absent")
		}
		out, e := d.Execute(context.Background(), a, inputs, func() error { Fail("setup failure cannot dispatch"); return nil }, func(work.ProviderProgress) error { Fail("setup failure cannot ACK"); return nil })
		Expect(e).To(HaveOccurred())
		Expect(out.RequestID).To(BeEmpty())
		Expect(out.SessionID).To(BeEmpty())
		Expect(out.CleanupError).NotTo(HaveOccurred())
		if mode == "audit" || mode == "frame" {
			Expect(out.Transport).NotTo(BeNil())
			Expect(out.Transport.CleanupState).To(Equal("drained"))
			Expect(out.Transport.LocalClosed).To(BeTrue())
			Expect(out.Transport.AcceptedAt).To(BeEmpty())
		} else {
			Expect(out.Transport).To(BeNil())
		}
	}
})

var _ = It("C03W4 caller stop after known completion stays pending but actual payload refusal stays unavailable", Label("unit"), func() {
	for _, refuse := range []bool{false, true} {
		d, a, _, s := fixture("complete")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if refuse {
			s.tx.deny = true
		} else {
			cancel()
		}
		out, e := d.result(ctx, a, turn{ID: "turn", Status: "completed", ItemsView: "full"}, work.ProviderOutcome{RequestID: "turn", SessionID: "thread"}, false, "live_event")
		Expect(out.CompletionObserved).To(BeTrue())
		Expect(out.Body).To(BeNil())
		if refuse {
			Expect(e).NotTo(HaveOccurred())
			Expect(out.State).To(Equal("failed"))
			Expect(out.Reason).To(Equal("result_unavailable"))
		} else {
			Expect(e).To(MatchError(context.Canceled))
			Expect(out.State).To(Equal("outcome_unknown"))
			Expect(out.Reason).To(Equal("result_retrieval_pending"))
		}
	}
})

var _ = It("C03W4 conflicting reliable full-history terminal is explicit without erasing the known completed metadata", Label("integration"), func() {
	d, a, _, _ := fixture("full_failed")
	a.RequestID = "turn"
	a.SessionID = "thread"
	a.Transport = &domain.ProviderTransport{Provider: "codex", Version: Version, BinarySHA: d.BinarySHA, SetupThread: "thread", CWD: d.Scratch}
	out, e := d.Reconcile(context.Background(), a, true)
	Expect(errors.Is(e, domain.ErrConflict)).To(BeTrue())
	Expect(out.CompletionObserved).To(BeTrue())
	Expect(out.State).To(Equal("outcome_unknown"))
	Expect(out.Lookup.BackendState).To(Equal("completed"))
	Expect(out.Body).To(BeNil())
})

var _ = It("C03W5 internal setup deadline stays local while caller cancellation keeps its own authority", Label("integration"), func() {
	d, a, inputs, _ := fixture("happy")
	caller := context.Background()
	out, e := d.execute(caller, -time.Second, a, inputs, func() error { Fail("expired internal setup dispatched"); return nil }, func(work.ProviderProgress) error { Fail("expired setup acknowledged"); return nil })
	Expect(e).To(MatchError(context.DeadlineExceeded))
	Expect(caller.Err()).NotTo(HaveOccurred())
	Expect(out.Reason).To(Equal("setup_timeout"))
	Expect(out.State).To(BeEmpty())
	Expect(out.RequestID).To(BeEmpty())
	Expect(out.Transport).To(BeNil())
	cancelled, cancel := context.WithCancel(caller)
	cancel()
	out, e = d.execute(cancelled, -time.Second, a, inputs, func() error { Fail("cancelled setup dispatched"); return nil }, func(work.ProviderProgress) error { return nil })
	Expect(e).To(MatchError(context.Canceled))
	Expect(out.Reason).NotTo(Equal("setup_timeout"))
})

func lateSetupTrace(binary string, stage string) int {
	var pid int
	Eventually(func() bool {
		raw, e := os.ReadFile(binary + ".trace")
		if e != nil {
			return false
		}
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			var event struct {
				Method string `json:"method"`
				PID    int    `json:"pid"`
			}
			if json.Unmarshal([]byte(line), &event) == nil && event.Method == stage {
				pid = event.PID
				return true
			}
		}
		return false
	}, 5*time.Second).Should(BeTrue(), stage)
	Expect(pid).To(BeNumerically(">", 0))
	return pid
}
func expectLateSetupClosed(out work.ProviderOutcome, binary string, pid int) {
	Expect(out.State).To(BeEmpty())
	Expect(out.RequestID).To(BeEmpty())
	Expect(out.SessionID).To(BeEmpty())
	Expect(out.Transport).To(BeNil())
	Expect(out.Lookup).To(BeNil())
	Expect(out.Body).To(BeNil())
	Expect(out.LocalClosed).To(BeTrue())
	Expect(out.CleanupError).NotTo(HaveOccurred())
	Expect(providerTestDrainedReason(out.LocalCleanupReason)).To(BeTrue())
	raw, e := os.ReadFile(binary + ".trace")
	Expect(e).NotTo(HaveOccurred())
	Expect(string(raw)).NotTo(ContainSubstring(`"method": "turn/start"`))
	Expect(syscall.Kill(pid, 0)).To(Equal(syscall.ESRCH))
}
func providerTestDrainedReason(reason string) bool {
	return reason == "" || reason == "signal_error_drained"
}

var _ = It("C03L6 actual late inspect thread and MCP internal deadlines preserve live caller reason and drain without sending", Label("integration"), func() {
	for _, row := range []struct{ mode, stage string }{{"stalled", "account/read"}, {"stalled_thread", "thread/start"}, {"stalled_mcp", "mcpServerStatus/list"}} {
		d, a, inputs, _ := fixture(row.mode)
		type response struct {
			out work.ProviderOutcome
			err error
		}
		done := make(chan response, 1)
		caller := context.Background()
		go func() {
			defer GinkgoRecover()
			out, e := d.execute(caller, 2*time.Second, a, inputs, func() error { Fail("expired late setup dispatched"); return nil }, func(work.ProviderProgress) error { Fail("late setup ACK"); return nil })
			done <- response{out, e}
		}()
		pid := lateSetupTrace(d.Binary, row.stage)
		var got response
		Eventually(done, 10*time.Second).Should(Receive(&got))
		Expect(caller.Err()).NotTo(HaveOccurred())
		Expect(errors.Is(got.err, context.DeadlineExceeded)).To(BeTrue())
		Expect(got.out.Reason).To(Equal("setup_timeout"))
		expectLateSetupClosed(got.out, d.Binary, pid)
	}
})
var _ = It("C03L6 actual late MCP caller cancel and caller deadline do not become an internal setup timeout", Label("integration"), func() {
	for _, deadline := range []bool{false, true} {
		d, a, inputs, _ := fixture("stalled_mcp")
		caller, cancel := context.WithCancel(context.Background())
		if deadline {
			cancel()
			caller, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		}
		defer cancel()
		type response struct {
			out work.ProviderOutcome
			err error
		}
		done := make(chan response, 1)
		go func() {
			defer GinkgoRecover()
			out, e := d.execute(caller, 30*time.Second, a, inputs, func() error { Fail("cancelled late setup dispatched"); return nil }, func(work.ProviderProgress) error { Fail("late setup ACK"); return nil })
			done <- response{out, e}
		}()
		pid := lateSetupTrace(d.Binary, "mcpServerStatus/list")
		Expect(caller.Err()).NotTo(HaveOccurred())
		if !deadline {
			cancel()
		}
		var got response
		Eventually(done, 10*time.Second).Should(Receive(&got))
		expected := context.Canceled
		if deadline {
			expected = context.DeadlineExceeded
		}
		Expect(errors.Is(got.err, expected)).To(BeTrue())
		Expect(got.out.Reason).NotTo(Equal("setup_timeout"))
		expectLateSetupClosed(got.out, d.Binary, pid)
	}
})
var _ = It("C03L6 pre-audit refusal and no-observation reconciliation report actual local closure without complete native identity", Label("integration"), func() {
	d, a, inputs, s := fixture("complete")
	s.tx.failGuardAt = 3
	out, e := d.Execute(context.Background(), a, inputs, func() error { Fail("disclosure refusal dispatched"); return nil }, func(work.ProviderProgress) error { return nil })
	Expect(e).To(HaveOccurred())
	Expect(out.LocalClosed).To(BeTrue())
	Expect(out.CleanupError).NotTo(HaveOccurred())
	Expect(out.Transport).To(BeNil())
	Expect(out.Lookup).To(BeNil())
	Expect(out.RequestID).To(BeEmpty())
	d, a, _, _ = fixture("api_key")
	a.RequestID = "turn"
	a.SessionID = "thread"
	a.Transport = &domain.ProviderTransport{Provider: "codex", Version: Version, BinarySHA: d.BinarySHA, SetupThread: "thread", CWD: d.Scratch}
	out, e = d.Reconcile(context.Background(), a, false)
	Expect(e).NotTo(HaveOccurred())
	Expect(out.LocalClosed).To(BeTrue())
	Expect(out.Lookup).To(BeNil())
	Expect(out.Transport).To(BeNil())
	Expect(out.CompletionObserved).To(BeFalse())
	Expect(out.State).To(Equal("outcome_unknown"))
	d, a, inputs, _ = fixture("complete")
	d.BinarySHA = strings.Repeat("0", 64)
	out, e = d.Execute(context.Background(), a, inputs, func() error { Fail("unverified binary dispatched"); return nil }, func(work.ProviderProgress) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	Expect(out.Reason).To(Equal("unsupported_version"))
	Expect(out.LocalClosed).To(BeTrue())
	Expect(out.CleanupError).NotTo(HaveOccurred())
	Expect(out.Transport).To(BeNil())
	_, e = os.Stat(d.Binary + ".trace")
	Expect(os.IsNotExist(e)).To(BeTrue())
})
