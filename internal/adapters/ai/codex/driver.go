package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const Version = "0.164.0"

type Driver struct {
	Store     app.Store
	Bytes     capturedBytes
	Binary    string
	BinarySHA string
	Scratch   string
	mu        sync.Mutex
	active    map[string]activeTurn
}
type activeTurn struct {
	transport    *transport
	thread, turn string
	interrupt    chan struct{}
}

func New(store app.Store, bytes capturedBytes, scratch string) *Driver {
	d := &Driver{Store: store, Bytes: bytes, Scratch: scratch, active: map[string]activeTurn{}}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		d.BinarySHA = "d8990e3ee7acfbc26f91265b12a1a83f0fee40d0e0f8f1f40a37d4ec21f499cf"
	}
	return d
}
func (d *Driver) binary(ctx context.Context) (string, string, error) {
	binary := d.Binary
	if binary == "" {
		var e error
		binary, e = exec.LookPath("codex")
		if e != nil {
			return "", "", errTransport
		}
	}
	binary, e := filepath.Abs(binary)
	if e != nil {
		return "", "", errTransport
	}
	f, e := os.Open(binary)
	if e != nil {
		return "", "", errTransport
	}
	defer f.Close()
	hash := sha256.New()
	if _, e = io.Copy(hash, f); e != nil {
		return "", "", errTransport
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if d.BinarySHA == "" || actual != d.BinarySHA {
		return "", "", errVersion
	}
	command := exec.CommandContext(ctx, binary, "--version")
	command.Env = subscriptionEnvironment()
	command.Stderr = io.Discard
	data, e := command.Output()
	if e != nil || strings.TrimSpace(string(data)) != "codex-cli "+Version {
		return "", "", errVersion
	}
	return binary, actual, nil
}

type Status = domain.ProviderStatus

type setup struct {
	Approval string `json:"approvalPolicy"`
	CWD      string `json:"cwd"`
	Model    string `json:"model"`
	Provider string `json:"modelProvider"`
	Effort   string `json:"reasoningEffort"`
	Sandbox  struct {
		Type    string `json:"type"`
		Network bool   `json:"networkAccess"`
	} `json:"sandbox"`
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
	Instructions    []string `json:"instructionSources"`
	DisabledPlugins []string `json:"disabledPluginIds"`
}

func inspect(ctx context.Context, t *transport, cwd string) (Status, error) {
	status := Status{Provider: "codex", InputFormats: []string{"utf8_text"}, OutputFormat: "public_final_text", State: "provider_error", Reason: "transport_unavailable", Version: Version, Authentication: "unknown", Models: []string{}}
	var account struct {
		RequiresOpenaiAuth *bool `json:"requiresOpenaiAuth"`
		Account            *struct {
			Type string `json:"type"`
		} `json:"account"`
	}
	if e := t.call(ctx, "account/read", map[string]bool{"refreshToken": false}, &account); e != nil {
		status.Reason = "account_read_failed"
		return status, e
	}
	if account.RequiresOpenaiAuth == nil || !*account.RequiresOpenaiAuth {
		status.State = "auth_required"
		status.Reason = "unsupported_authentication"
		return status, domain.ErrDenied
	}
	if account.Account == nil {
		status.State = "auth_required"
		status.Reason = "login_required"
		return status, domain.ErrDenied
	}
	status.Authentication = account.Account.Type
	if status.Authentication != "chatgpt" {
		status.State = "auth_required"
		status.Reason = "unsupported_authentication"
		return status, domain.ErrDenied
	}
	var config struct {
		Config *struct {
			Provider       *string                    `json:"model_provider"`
			Providers      map[string]json.RawMessage `json:"model_providers"`
			ChatGPTBaseURL *string                    `json:"chatgpt_base_url"`
			OpenAIBaseURL  *string                    `json:"openai_base_url"`
		} `json:"config"`
	}
	if e := t.call(ctx, "config/read", map[string]any{"includeLayers": false, "cwd": cwd}, &config); e != nil {
		status.Reason = "config_read_failed"
		return status, e
	}
	if config.Config == nil {
		status.Reason = "config_unavailable"
		return status, domain.ErrDenied
	}
	custom := false
	if raw, ok := config.Config.Providers["openai"]; ok && string(raw) != "null" && string(raw) != "{}" {
		custom = true
	}
	if config.Config.ChatGPTBaseURL != nil {
		u, e := url.Parse(*config.Config.ChatGPTBaseURL)
		if e != nil || u.Scheme != "https" || u.Host != "chatgpt.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || strings.TrimSuffix(u.Path, "/") != "/backend-api" {
			custom = true
		}
	}
	if config.Config.OpenAIBaseURL != nil && *config.Config.OpenAIBaseURL != "https://api.openai.com/v1" {
		custom = true
	}
	if custom || config.Config.Provider != nil && *config.Config.Provider != "openai" {
		status.State = "paused"
		status.Reason = "unsupported_provider"
		return status, domain.ErrDenied
	}
	var allowance struct {
		Allowed *bool `json:"ordinaryUsageAllowed"`
	}
	// Missing quota disclosure is unknown; it is never converted to zero/unlimited.
	if t.call(ctx, "account/rateLimits/read", map[string]any{}, &allowance) == nil {
		status.OrdinaryUsageAllowed = allowance.Allowed
	}
	if allowance.Allowed != nil && !*allowance.Allowed {
		status.State = "paused"
		status.Reason = "usage_not_allowed"
		return status, domain.ErrDenied
	}
	cursor := ""
	seen := map[string]bool{}
	for {
		var catalog struct {
			Data []struct {
				Model string `json:"model"`
			} `json:"data"`
			Next *string `json:"nextCursor"`
		}
		params := map[string]any{"includeHidden": true}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if e := t.call(ctx, "model/list", params, &catalog); e != nil {
			status.Reason = "catalog_read_failed"
			return status, e
		}
		for _, m := range catalog.Data {
			if m.Model != "" {
				status.Models = append(status.Models, m.Model)
			}
		}
		if catalog.Next == nil || *catalog.Next == "" {
			break
		}
		if seen[*catalog.Next] {
			return status, errTransport
		}
		seen[*catalog.Next] = true
		cursor = *catalog.Next
	}
	status.State = "ready"
	status.Reason = ""
	if status.OrdinaryUsageAllowed == nil {
		status.State = "quota_unavailable"
		status.Reason = "quota_unknown"
	}
	status.ReadOnly = true
	status.Lookup = true
	status.Cancellation = true
	return status, nil
}
func startThread(ctx context.Context, t *transport, inv domain.ProviderInvocation, ephemeral bool) (setup, error) {
	var result setup
	if !filepath.IsAbs(inv.CWD) {
		return result, domain.ErrInvalidTarget
	}
	mode, expected := "read-only", "readOnly"
	if inv.Generation != nil {
		mode, expected = "workspace-write", "workspaceWrite"
	}
	params := map[string]any{"cwd": inv.CWD, "modelProvider": "openai", "approvalPolicy": "never", "sandbox": mode, "ephemeral": ephemeral}
	if inv.Model != "" {
		params["model"] = inv.Model
	}
	if inv.Effort != "" {
		params["config"] = map[string]any{"model_reasoning_effort": inv.Effort}
	}
	if e := t.call(ctx, "thread/start", params, &result); e != nil {
		return result, e
	}
	if result.Thread.ID == "" || result.Provider != "openai" || result.Approval != "never" || result.Sandbox.Type != expected || result.Sandbox.Network || filepath.Clean(result.CWD) != filepath.Clean(inv.CWD) || inv.Model != "" && result.Model != inv.Model || inv.Effort != "" && result.Effort != inv.Effort {
		return result, domain.ErrDenied
	}
	return result, nil
}
func (d *Driver) Status(ctx context.Context, cwd string) (status Status) {
	status = Status{Provider: "codex", InputFormats: []string{"utf8_text"}, OutputFormat: "public_final_text", State: "provider_error", Reason: "transport_unavailable", Authentication: "unknown", Models: []string{}}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	binary, _, e := d.binary(probeCtx)
	if e != nil {
		if errors.Is(e, errVersion) {
			status.State = "unsupported_version"
			status.Reason = "unverified_binary"
		}
		return status
	}
	t, e := start(probeCtx, binary)
	if e != nil {
		status.Reason = "initialize_failed"
		if errors.Is(e, errCleanup) {
			status.Reason = "cleanup_unknown"
		}
		return status
	}
	defer func() {
		if t.close() != nil {
			status.State = "provider_error"
			status.Reason = "cleanup_unknown"
			status.ReadOnly = false
		}
	}()
	stop := context.AfterFunc(probeCtx, func() { _ = t.close() })
	defer stop()
	status, e = inspect(probeCtx, t, cwd)
	if e != nil {
		return status
	}
	actual, e := startThread(probeCtx, t, domain.ProviderInvocation{CWD: cwd}, true)
	if e != nil {
		status.State = "paused"
		status.Reason = "mode_unavailable"
		status.ReadOnly = false
		return status
	}
	status.Model = actual.Model
	status.Effort = actual.Effort
	return status
}
func (d *Driver) Execute(ctx context.Context, a domain.ProviderAttempt, inputs []domain.ProviderInput, beforeSend func() error, ack func(work.ProviderProgress) error) (out work.ProviderOutcome, err error) {
	return d.execute(ctx, 30*time.Second, a, inputs, beforeSend, ack)
}

func (d *Driver) execute(ctx context.Context, setupTimeout time.Duration, a domain.ProviderAttempt, inputs []domain.ProviderInput, beforeSend func() error, ack func(work.ProviderProgress) error) (out work.ProviderOutcome, err error) {
	out = work.ProviderOutcome{Reason: "provider_unavailable", LocalClosed: true}
	inv, e := d.invocation(ctx, a)
	if e != nil {
		return out, e
	}
	inputs, e = d.orderedInputs(ctx, a, inputs)
	if e != nil {
		return out, e
	}
	setupCtx, setupCancel := context.WithTimeout(ctx, setupTimeout)
	defer setupCancel()
	generation, e := d.prepareGeneration(a, &inv)
	if e != nil {
		out.Reason = "generation_setup_failed"
		return out, e
	}
	question, e := d.prepareQuestion(a, &inv)
	if e != nil {
		out.Reason = "question_setup_failed"
		return out, e
	}
	// Preserve the actual local preparation deadline, independently of caller
	// cancellation and the later one-use dispatch fence.
	defer func() {
		if errors.Is(err, context.DeadlineExceeded) && setupCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			out.Reason = "setup_timeout"
		}
	}()
	binary, binarySHA, e := d.binary(setupCtx)
	if e != nil {
		if errors.Is(e, errVersion) {
			out.Reason = "unsupported_version"
		}
		if setupCtx.Err() != nil {
			return out, setupCtx.Err()
		}
		return out, nil
	}
	out.LocalClosed = false
	t, e := start(setupCtx, binary)
	if e != nil {
		out.LocalClosed = !errors.Is(e, errCleanup)
		if errors.Is(e, errCleanup) {
			out.CleanupError = errCleanup
		}
		return out, e
	}
	var metadata *domain.ProviderTransport
	backendState := ""
	defer func() {
		cleanup := t.close()
		out.CleanupError = cleanup
		out.LocalClosed = cleanup == nil
		out.LocalCleanupReason = t.cleanupReason
		err = errors.Join(err, cleanup)
		if metadata == nil {
			return
		}
		metadata.LocalClosed = cleanup == nil
		metadata.CleanupReason = t.cleanupReason
		metadata.CleanupState = "drained"
		if cleanup != nil {
			metadata.CleanupState = "unknown"
		}
		metadata.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		metadata.BackendState = backendState
		if backendState != "" {
			metadata.BackendSource = "live_event"
		}
		metadata.DriverOutcome = out.State
		if out.State == "result_saved" {
			metadata.DriverOutcome = "result_ready"
		}
		out.Transport = metadata
		if generation != nil && out.Body != nil {
			var captureErr error
			out, captureErr = d.captureGeneration(ctx, a, out, generation, false)
			err = errors.Join(err, captureErr)
			metadata.DriverOutcome = out.State
			if out.State == "result_saved" {
				metadata.DriverOutcome = "result_ready"
			}
		}
	}()
	stop := context.AfterFunc(ctx, func() { _ = t.close() })
	defer stop()
	setupStop := context.AfterFunc(setupCtx, func() { _ = t.close() })
	defer setupStop()
	status, e := inspect(setupCtx, t, inv.CWD)
	if e != nil {
		out.Reason = status.Reason
		if status.State == "auth_required" {
			out.Reason = "auth_required"
		}
		if setupCtx.Err() != nil {
			return out, setupCtx.Err()
		}
		return out, nil
	}
	actual, e := startThread(setupCtx, t, inv, false)
	if e != nil {
		out.Reason = "mode_unavailable"
		if setupCtx.Err() != nil {
			return out, setupCtx.Err()
		}
		return out, nil
	}
	if e = discoverGenerationSkill(setupCtx, t, inv.CWD, generation); e != nil {
		out.Reason = "skill_unavailable"
		if setupCtx.Err() != nil {
			return out, setupCtx.Err()
		}
		return out, e
	}
	capabilities := inheritedCapabilities(setupCtx, t, inv.CWD, actual.Thread.ID)
	if setupCtx.Err() != nil {
		return out, setupCtx.Err()
	}
	select {
	case <-t.done:
		return out, errTransport
	default:
	}
	if !setupStop() {
		if setupCtx.Err() != nil {
			return out, setupCtx.Err()
		}
		return out, errTransport
	}
	setupCancel()
	disclosures := []domain.ProviderDisclosure{}
	if e = d.Store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.GuardProviderPayload(a.JobID, a.ID, false); e != nil {
			return e
		}
		for _, input := range inputs {
			read, e := tx.CapturedRead(input.ReadID)
			if e != nil {
				return e
			}
			object, e := tx.Object(input.Hash)
			if e != nil {
				return e
			}
			disclosures = append(disclosures, domain.ProviderDisclosure{ReadID: read.ID, Hash: input.Hash, Size: object.Size, Source: read.SourceJSON, Location: read.LocationJSON})
		}
		return nil
	}); e != nil {
		if !work.ProviderAuthorityRefusal(e) && !errors.Is(e, context.Canceled) && !errors.Is(e, context.DeadlineExceeded) {
			out.Reason = "provider_accounting_unavailable"
		}
		return out, e
	}
	mode := "read-only"
	if generation != nil {
		mode = "workspace-write"
	}
	metadata = &domain.ProviderTransport{Generation: generation, Question: question, Capabilities: &capabilities, Disclosure: disclosures, OrdinaryUsageAllowed: status.OrdinaryUsageAllowed, Provider: "codex", Version: Version, BinarySHA: binarySHA, PreparedAt: time.Now().UTC().Format(time.RFC3339Nano), SetupThread: actual.Thread.ID, AuthMode: "chatgpt", Model: actual.Model, Effort: actual.Effort, CWD: actual.CWD, Approval: actual.Approval, Sandbox: mode, InstructionCount: len(actual.Instructions), DisabledPluginCount: len(actual.DisabledPlugins), FrameSHA: "", AllowanceKnown: status.OrdinaryUsageAllowed != nil}
	record := func() error {
		e := d.Store.Write(context.WithoutCancel(ctx), func(tx app.WriteTx) error { return tx.RecordProviderTransport(a.ID, *metadata) })
		if e != nil && out.State == "" && !work.ProviderAuthorityRefusal(e) {
			out.Reason = "provider_accounting_unavailable"
		}
		return e
	}
	if e = record(); e != nil {
		return out, e
	}
	frame, frameSHA, e := d.frame(ctx, a, inputs, inv, actual.Thread.ID)
	if e != nil {
		out.Reason = "source_unavailable"
		if errors.Is(e, errInputFormat) {
			out.Reason = "unsupported_input_format"
		}
		if !errors.Is(e, errInputFormat) {
			return out, e
		}
		return out, nil
	}
	defer func() { name := frame.Name(); frame.Close(); os.Remove(name) }()
	metadata.FrameSHA = frameSHA
	if e = record(); e != nil {
		return out, e
	}
	if e = beforeSend(); e != nil {
		out.Reason = "dispatch_not_authorized"
		return out, nil
	}
	out = work.ProviderOutcome{State: "outcome_unknown", Reason: "transport_unknown"}
	accepted, e := t.submit(ctx, guardedReader{frame, func() error { return d.guard(ctx, a, false) }})
	if e != nil {
		return out, nil
	}
	out.RequestID = accepted.ID
	out.SessionID = actual.Thread.ID
	metadata.AcceptedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if generation != nil {
		generation.SkillInputAcknowledged = true
		generation.PromptInputAcknowledged = generation.Prompt != nil
		generation.ReferencesAcknowledged = len(generation.References) > 0
	}
	if question != nil {
		question.PromptInputAcknowledged = true
		question.ContextInputAcknowledged = question.Context != nil
		question.ExplanationInputAcknowledged = true
	}
	observed := append([]domain.ProviderInput(nil), inputs...)
	for i := range observed {
		observed[i].ObservedOrigin = "daemon_supplied"
	}
	out.Observed = observed
	if e = record(); e != nil {
		return out, e
	}
	if e = ack(work.ProviderProgress{RequestID: accepted.ID, SessionID: actual.Thread.ID, Observed: observed}); e != nil {
		return out, e
	}
	d.mu.Lock()
	interrupt := make(chan struct{})
	d.active[a.ID] = activeTurn{t, actual.Thread.ID, accepted.ID, interrupt}
	d.mu.Unlock()
	defer func() { d.mu.Lock(); delete(d.active, a.ID); d.mu.Unlock() }()
	current, e := d.Store.Job(ctx, a.JobID)
	if e != nil {
		return out, e
	}
	if current.CancelRequested || current.AttemptGeneration != a.Generation {
		_ = d.Cancel(ctx, a)
	}
	terminal := accepted
	backendState = accepted.Status
	var cancelDeadline <-chan time.Time
	var interrupted <-chan struct{} = interrupt
	for terminal.Status == "inProgress" || terminal.Status == "" {
		select {
		case <-ctx.Done():
			return out, nil
		case <-interrupted:
			interrupted = nil
			cancelDeadline = time.After(5 * time.Second)
		case <-cancelDeadline:
			return out, nil
		case <-t.done:
			return out, nil
		case event := <-t.turns:
			if event.ThreadID == actual.Thread.ID && event.Turn.ID == accepted.ID {
				terminal = event.Turn
			}
		}
	}
	backendState = terminal.Status
	if terminal.Status == "completed" && terminal.ItemsView != "full" {
		loaded, lookupErr := loadTurn(ctx, t, actual.Thread.ID, accepted.ID, func() error { return d.guard(ctx, a, false) })
		if lookupErr != nil {
			return completedUnavailable(out, lookupErr)
		}
		if loaded.Status != "completed" {
			if loaded.Status == "failed" {
				return completedUnavailable(out, domain.ErrConflict)
			}
			return completedUnavailable(out, errTransport)
		}
		terminal = loaded
	}
	return d.result(ctx, a, terminal, out, false, "live_event")
}
func (d *Driver) result(ctx context.Context, a domain.ProviderAttempt, terminal turn, out work.ProviderOutcome, reconcile bool, source string) (work.ProviderOutcome, error) {
	switch terminal.Status {
	case "interrupted":
		if source != "live_event" {
			out.State = "outcome_unknown"
			out.Reason = "transport_unknown"
			return out, nil
		}
		out.State = "cancelled"
		out.Reason = "cancel_confirmed"
	case "failed":
		out.State = "failed"
		out.Reason = errorReason(terminal)
	case "completed":
		out.CompletionObserved = true
		out.State = "result_saved"
		out.Reason = ""
		if terminal.ItemsView != "full" {
			out.State = "failed"
			out.Reason = "result_unavailable"
			return out, nil
		}
		if e := d.guard(ctx, a, reconcile); e != nil {
			return completedUnavailable(out, e)
		}
		file, e := os.CreateTemp(d.Scratch, "provider-output-*")
		if e != nil {
			return completedUnavailable(out, e)
		}
		count := 0
		for _, item := range terminal.Items {
			if item.Type == "agentMessage" && item.Phase == "final_answer" {
				if e = d.guard(ctx, a, reconcile); e != nil {
					file.Close()
					os.Remove(file.Name())
					return completedUnavailable(out, e)
				}
				if count > 0 {
					_, e = io.WriteString(file, "\n")
				}
				if e == nil {
					_, e = io.WriteString(file, item.Text)
				}
				if e != nil {
					file.Close()
					os.Remove(file.Name())
					return completedUnavailable(out, e)
				}
				count++
			}
		}
		if count == 0 {
			file.Close()
			os.Remove(file.Name())
			out.State = "failed"
			out.Reason = "result_unavailable"
			return out, nil
		}
		if _, e = file.Seek(0, 0); e != nil {
			file.Close()
			os.Remove(file.Name())
			return completedUnavailable(out, e)
		}
		out.Body = &resultReader{file: file, guard: func() error { return d.guard(context.WithoutCancel(ctx), a, reconcile) }}
	default:
		out.State = "outcome_unknown"
		out.Reason = "transport_unknown"
	}
	return out, nil
}
func completedUnavailable(out work.ProviderOutcome, cause error) (work.ProviderOutcome, error) {
	out.CompletionObserved = true
	out.State, out.Reason = "outcome_unknown", "result_retrieval_pending"
	if work.ProviderAuthorityRefusal(cause) {
		out.State, out.Reason = "failed", "result_unavailable"
		return out, nil
	}
	return out, cause
}

func errorReason(t turn) string {
	if t.Error == nil {
		return "provider_failed"
	}
	var name string
	if json.Unmarshal(t.Error.Info, &name) != nil {
		return "provider_failed"
	}
	switch name {
	case "unauthorized":
		return "auth_required"
	case "usageLimitExceeded":
		return "quota_exhausted"
	case "rateLimitExceeded":
		return "provider_rate_limited"
	case "sessionBudgetExceeded":
		return "provider_budget_exhausted"
	case "contextWindowExceeded":
		return "provider_context_limit"
	default:
		return "provider_failed"
	}
}

type resultReader struct {
	file  *os.File
	guard func() error
}

func (r *resultReader) Read(p []byte) (int, error) {
	if e := r.guard(); e != nil {
		return 0, e
	}
	return r.file.Read(p)
}
func (r *resultReader) Close() error {
	name := r.file.Name()
	return errors.Join(r.file.Close(), os.Remove(name))
}
func (d *Driver) Cancel(ctx context.Context, a domain.ProviderAttempt) error {
	d.mu.Lock()
	active, ok := d.active[a.ID]
	if !ok {
		d.mu.Unlock()
		return domain.ErrReconciliation
	}
	// Reserve before writing: concurrent Cancel and post-registration recheck may
	// request only one exact interrupt. Closing also starts Execute's bounded wait.
	select {
	case <-active.interrupt:
		d.mu.Unlock()
		return nil
	default:
		close(active.interrupt)
	}
	d.mu.Unlock()
	interruptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stop := context.AfterFunc(interruptCtx, func() { _ = active.transport.close() })
	defer stop()
	return active.transport.call(interruptCtx, "turn/interrupt", map[string]string{"threadId": active.thread, "turnId": active.turn}, nil)
}
func (d *Driver) Reconcile(ctx context.Context, a domain.ProviderAttempt, outputAllowed bool) (out work.ProviderOutcome, err error) {
	lookupCtx, lookupCancel := context.WithTimeout(ctx, 30*time.Second)
	defer lookupCancel()
	ctx = lookupCtx
	out = work.ProviderOutcome{State: "outcome_unknown", Reason: "provider_unavailable", RequestID: a.RequestID, SessionID: a.SessionID, LocalClosed: true}
	if a.RequestID == "" || a.SessionID == "" || a.Transport == nil || a.Transport.Provider != "codex" || a.Transport.Version != Version || !filepath.IsAbs(a.Transport.CWD) {
		return out, nil
	}
	binary, binarySHA, e := d.binary(ctx)
	if e != nil || binarySHA != a.Transport.BinarySHA || a.Transport.SetupThread != a.SessionID {
		return out, nil
	}
	out.LocalClosed = false
	t, e := start(ctx, binary)
	if e != nil {
		out.LocalClosed = !errors.Is(e, errCleanup)
		if errors.Is(e, errCleanup) {
			out.CleanupError = errCleanup
		}
		return out, e
	}
	backendState := ""
	defer func() {
		cleanup := t.close()
		out.CleanupError = cleanup
		out.LocalClosed = cleanup == nil
		out.LocalCleanupReason = t.cleanupReason
		err = errors.Join(err, cleanup)
		if backendState != "" {
			observation := domain.ProviderLookup{ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), BackendState: backendState, LocalClosed: cleanup == nil, CleanupReason: t.cleanupReason}
			observation.Source = "history"
			out.Lookup = &observation
		}
		if outputAllowed && a.Transport.Generation != nil && out.Body != nil {
			if !a.Transport.LocalClosed || a.Transport.CleanupState != "drained" {
				closeErr := out.Body.Close()
				out.Body = nil
				out.CleanupError = errors.Join(out.CleanupError, errCleanup, closeErr)
				out, _ = completedUnavailable(out, errCleanup)
				err = errors.Join(err, errCleanup, closeErr)
				return
			}
			copied := *a.Transport
			generation := *a.Transport.Generation
			copied.Generation = &generation
			var captureErr error
			out, captureErr = d.captureGeneration(ctx, a, out, &generation, true)
			out.Transport = &copied
			err = errors.Join(err, captureErr)
		}
	}()
	stop := context.AfterFunc(ctx, func() { _ = t.close() })
	defer stop()
	status, e := inspect(ctx, t, a.Transport.CWD)
	if e != nil && status.Reason != "usage_not_allowed" {
		return out, nil
	}
	lookup := func(view string) (turn, error) {
		cursor := ""
		seen := map[string]bool{}
		for {
			// Metadata lookup needs an executing intent too, but never reads source or
			// complete history when this is a status-only carrier.
			e = d.Store.Write(ctx, func(tx app.WriteTx) error { return tx.GuardProviderLookup(a.JobID, a.ID) })
			if e != nil {
				return turn{}, e
			}
			if view == "full" {
				if e = d.guard(ctx, a, true); e != nil {
					return turn{}, e
				}
			}
			params := map[string]any{"threadId": a.SessionID, "itemsView": view}
			if cursor != "" {
				params["cursor"] = cursor
			}
			var page struct {
				Data []turn  `json:"data"`
				Next *string `json:"nextCursor"`
			}
			if e = t.call(ctx, "thread/turns/list", params, &page); e != nil {
				return turn{}, e
			}
			for _, candidate := range page.Data {
				if candidate.ID == a.RequestID {
					return candidate, nil
				}
			}
			if page.Next == nil || *page.Next == "" {
				return turn{}, domain.ErrNotFound
			}
			if seen[*page.Next] {
				return turn{}, errTransport
			}
			seen[*page.Next] = true
			cursor = *page.Next
		}
	}
	terminal, e := lookup("notLoaded")
	if e != nil {
		return out, nil
	}
	backendState = terminal.Status
	if terminal.Status == "completed" {
		if !outputAllowed {
			out.State = "failed"
			out.Reason = "result_unavailable"
			out.CompletionObserved = true
			return out, nil
		}
		terminal, e = lookup("full")
		if e != nil {
			return completedUnavailable(out, e)
		}
		if terminal.Status != "completed" {
			if terminal.Status == "failed" {
				return completedUnavailable(out, domain.ErrConflict)
			}
			return completedUnavailable(out, errTransport)
		}
		if terminal.ItemsView != "full" {
			out.State = "failed"
			out.Reason = "result_unavailable"
			out.CompletionObserved = true
			return out, nil
		}
	}
	return d.result(ctx, a, terminal, out, true, "history")
}

func loadTurn(ctx context.Context, t *transport, threadID, turnID string, guard func() error) (turn, error) {
	cursor := ""
	seen := map[string]bool{}
	for {
		if e := guard(); e != nil {
			return turn{}, e
		}
		params := map[string]any{"threadId": threadID, "itemsView": "full"}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Data []turn  `json:"data"`
			Next *string `json:"nextCursor"`
		}
		if e := t.call(ctx, "thread/turns/list", params, &page); e != nil {
			return turn{}, e
		}
		for _, candidate := range page.Data {
			if candidate.ID == turnID {
				return candidate, nil
			}
		}
		if page.Next == nil || *page.Next == "" {
			return turn{}, domain.ErrNotFound
		}
		if seen[*page.Next] {
			return turn{}, errTransport
		}
		seen[*page.Next] = true
		cursor = *page.Next
	}
}
