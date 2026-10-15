"""Thin source/permission/turn state machine with an explicit transport.

Running this file executes only neighboring injected behavioral tests. Historical
live evidence remains a separately verified observation, never a test result.
"""
import hashlib
import json
import time
from dataclasses import dataclass
from pathlib import Path


def sha(data):
    return hashlib.sha256(data).hexdigest()


def selected_environment(parent):
    return {k: v for k, v in parent.items() if k not in ("OPENAI_API_KEY", "CODEX_API_KEY")}


class Blocked(Exception):
    pass


class TransportFault(Exception):
    """Transport failed; its message records the independently known execution state."""
    pass


class UnknownOutcome(TransportFault):
    pass


class ProtocolFault(Exception):
    pass


class RequestRejected(Exception):
    pass


def normalize_error(code, will_retry=False, origin="injected"):
    """Normalize known protocol codes, never infer billing from an integer/message."""
    if origin not in ("injected", "live") or type(will_retry) is not bool:
        raise ProtocolFault("invalid error provenance/retry flag")
    kinds = {"unauthorized": "auth_required", "usageLimitExceeded": "quota_exhausted",
             "rateLimitExceeded": "rate_limited", "sessionBudgetExceeded": "session_budget_exceeded",
             "contextWindowExceeded": "context_limit", "flexUnavailable": "provider_unavailable",
             "serverOverloaded": "provider_unavailable"}
    category = kinds.get(code, "provider_error") if isinstance(code, str) else "provider_error"
    if isinstance(code, dict) and any(k in code for k in (
            "responseStreamDisconnected", "responseStreamConnectionFailed", "httpConnectionFailed")):
        category = "transport_error"
    return {"category": category, "will_retry": will_retry, "evidence_origin": origin,
            "execution_hint": "native_retry_pending" if will_retry else
            "unknown_outcome" if category == "transport_error" else "not_a_terminal_receipt"}


class ScopePolicy:
    """Exact project/root/revision/disclosure and provider grants, local to this object."""
    def __init__(self, approved):
        self.approved = set(approved)

    def require(self, requested, confirm=None):
        requested = frozenset(requested)
        missing = requested - self.approved
        if not missing:
            return
        # Only an explicit affirmative callback for this exact expansion adds it.
        if confirm is None or confirm(missing) is not True:
            raise Blocked("explicit project/provider/disclosure expansion required")
        self.approved.update(missing)


@dataclass(frozen=True)
class Capture:
    identity: tuple
    data: bytes
    sha256: str


def read_captures(manifest_path, expected_manifest_sha, capture_roots):
    """Read once into immutable bytes; later working-tree/capture edits cannot swap input."""
    try:
        raw = Path(manifest_path).read_bytes()
    except FileNotFoundError as exc:
        raise Blocked("source manifest unavailable") from exc
    if sha(raw) != expected_manifest_sha:
        raise Blocked("source manifest changed")
    roots = [Path(root).resolve() for root in capture_roots]
    captures = []
    for row in json.loads(raw):
        path = Path(row["capture_path"]).resolve()
        if not any(path.is_relative_to(root) for root in roots):
            raise Blocked("capture outside approved private roots")
        try:
            data = path.read_bytes()
        except FileNotFoundError as exc:
            raise Blocked("source unavailable") from exc
        if sha(data) != row["excerpt_sha256"]:
            raise Blocked("captured source bytes changed")
        identity = (row["project"], row["commit"], row["path"], row["line_start"], row["line_end"])
        captures.append(Capture(identity, data, sha(data)))
    return tuple(captures)


def guard_identity(account, config, thread):
    if account != {"type": "chatgpt", "planType": "pro", "requiresOpenaiAuth": True}:
        raise Blocked("subscription identity not confirmed")
    if config.get("model_provider") != "openai":
        raise Blocked("effective provider is not explicit openai")
    if (thread.get("modelProvider") != "openai" or thread.get("approvalPolicy") != "never"
            or (thread.get("sandbox") or {}).get("type") != "readOnly"):
        raise Blocked("actual thread permission/provider response not confirmed")


class DispatchLedger:
    """One durable intent per logical attempt, written before any possible dispatch."""
    def __init__(self, path):
        self.path = Path(path)

    def reserve(self, receipt):
        try:
            with self.path.open("x", encoding="utf-8") as stream:
                self.path.chmod(0o600)
                json.dump(receipt, stream, sort_keys=True)
                stream.write("\n")
                stream.flush()
        except FileExistsError as exc:
            raise Blocked("attempt already reserved; reconcile, never blindly redispatch") from exc
        # This is a single-managed-process guard, not a claimed power-loss-proof ledger.


class State:
    def __init__(self, thread_id, expected_input, origin="injected"):
        if not isinstance(thread_id, str) or not thread_id or origin not in ("injected", "live"):
            raise ProtocolFault("invalid expected thread/provenance")
        self.thread_id = thread_id
        self.input_sha = sha(expected_input.encode())
        self.origin = origin
        self.turn_id = None
        self.dispatch_state = "not_dispatched"
        self.terminal = None
        self.answers = {}
        self.legacy_answers = {}
        self.echoes = []
        self.errors = []
        self.pending = []
        self.cancel_requested = False
        self.cancel_acknowledged = False
        self.rejected_events = 0
        self.protocol_conflict = False
        self.receipt = None

    def acknowledge(self, turn, request_id, wire_sha):
        if self.turn_id is not None or not isinstance(turn, dict) or not isinstance(turn.get("id"), str) or not turn["id"]:
            raise ProtocolFault("invalid/duplicate turn acknowledgment")
        self.turn_id = turn["id"]
        self.dispatch_state = "acknowledged"
        self.receipt = {"request_id": request_id, "thread_id": self.thread_id,
                        "turn_id": self.turn_id, "wire_sha256": wire_sha,
                        "evidence_origin": self.origin}
        pending, self.pending = self.pending, []
        for projection in pending:
            self._apply(projection)

    def event(self, message):
        """Keep only safe projections, and never rebind from a notification."""
        method = message.get("method")
        p = message.get("params")
        if not isinstance(p, dict) or method not in ("turn/started", "turn/completed", "item/completed", "error"):
            return
        turn = p.get("turn") if method.startswith("turn/") else None
        tid = turn.get("id") if isinstance(turn, dict) else p.get("turnId")
        if p.get("threadId") != self.thread_id or not isinstance(tid, str) or not tid:
            self.rejected_events += 1
            return
        projection = {"method": method, "turn_id": tid}
        if method == "turn/completed":
            if turn.get("status") not in ("completed", "failed", "interrupted"):
                self.rejected_events += 1
                return
            projection["status"] = turn["status"]
            projection["items"] = [self._item(item) for item in turn.get("items", []) if isinstance(item, dict)]
            err = turn.get("error") or {}
            projection["error"] = normalize_error(err.get("codexErrorInfo"), origin=self.origin) if err else None
        elif method == "item/completed":
            projection["item"] = self._item(p.get("item") or {})
        elif method == "error":
            if type(p.get("willRetry")) is not bool:
                self.rejected_events += 1
                return
            projection["error"] = normalize_error((p.get("error") or {}).get("codexErrorInfo"), p["willRetry"], self.origin)
        if self.turn_id is None:
            if self.dispatch_state != "sending":
                self.rejected_events += 1
                return
            self.pending.append(projection)
        else:
            self._apply(projection)

    def _item(self, item):
        # Never retain reasoning, commentary, partial answers, raw error messages,
        # tool output, account identity, encrypted content, or arbitrary item fields.
        if item.get("type") == "userMessage":
            content = item.get("content") or []
            if not isinstance(content, list) or any(not isinstance(x.get("text", ""), str) for x in content if isinstance(x, dict)):
                return None
            text = "".join(x.get("text", "") for x in content if isinstance(x, dict) and x.get("type") == "text")
            return {"kind": "echo", "sha256": sha(text.encode()), "exact": sha(text.encode()) == self.input_sha}
        if item.get("type") == "agentMessage" and item.get("phase") in (None, "final_answer"):
            if isinstance(item.get("id"), str) and isinstance(item.get("text"), str):
                return {"kind": "answer", "id": item["id"], "text": item["text"], "phase": item.get("phase")}
        return None

    def _apply(self, p):
        if p["turn_id"] != self.turn_id:
            self.rejected_events += 1
            return
        if p["method"] == "turn/completed":
            terminal = {"thread_id": self.thread_id, "turn_id": self.turn_id,
                        "status": p["status"], "error": p["error"], "evidence_origin": self.origin}
            if self.terminal is not None and self.terminal != terminal:
                self.protocol_conflict = True
                return
            self.terminal = terminal
            for item in p["items"]:
                self._accept_item(item)
        elif p["method"] == "item/completed":
            if self.terminal is None:
                self._accept_item(p["item"])
        elif p["method"] == "error":
            self.errors.append(p["error"])

    def _accept_item(self, item):
        if item is None:
            return
        if item["kind"] == "echo":
            self.echoes.append(item)
        else:
            store = self.answers if item["phase"] == "final_answer" else self.legacy_answers
            if item["id"] in store and store[item["id"]] != item["text"]:
                self.protocol_conflict = True
                return
            store[item["id"]] = item["text"]

    def outcome(self):
        if self.protocol_conflict:
            return "protocol_conflict"
        if self.terminal is not None:
            return self.terminal["status"]
        if self.dispatch_state == "not_dispatched":
            return "not_dispatched"
        if self.dispatch_state == "rejected":
            return "request_rejected"
        return "unknown_outcome"

    def final_answer(self):
        if self.outcome() != "completed":
            raise Blocked("no matching successful terminal receipt")
        texts = self.answers or self.legacy_answers
        if not texts:
            raise Blocked("completed turn supplied no usable answer")
        return "\n".join(texts.values())

    def input_observation(self):
        if self.receipt is None or not any(x["exact"] for x in self.echoes):
            return None
        return {**self.receipt, "input_sha256": self.input_sha,
                "basis": "injected_exact_input_echo" if self.origin == "injected" else "daemon_supplied_exact_input_echo"}

    def cancellation_outcome(self):
        if self.terminal is not None:
            return self.outcome()
        return "cancellation_unresolved" if self.cancel_requested else "not_requested"


class Client:
    """Serial JSON-RPC requests over a caller-injected complete-frame transport.

    The transport implements send(dict) and receive(deadline). This candidate
    deliberately has no native transport factory. Framing/process wait remain
    adoption work, and no request retries occur here.
    """
    def __init__(self, state, transport, ledger):
        self.state, self.transport, self.ledger = state, transport, ledger
        self.next_id = 1
        self.active = False
        self.rejected_replies = 0

    def request(self, method, params, timeout=35):
        if self.active:
            raise Blocked("concurrent requests are unsupported by this serial adapter")
        ident = self.next_id
        self.next_id += 1
        message = {"id": ident, "method": method, "params": params}
        wire = (json.dumps(message, ensure_ascii=False, separators=(",", ":")) + "\n").encode()
        if method == "turn/start":
            if params.get("threadId") != self.state.thread_id or self.state.dispatch_state != "not_dispatched":
                raise Blocked("different thread or prior dispatch requires reconciliation")
            text = "".join(x.get("text", "") for x in params.get("input", []) if x.get("type") == "text")
            if sha(text.encode()) != self.state.input_sha:
                raise Blocked("dispatch input differs from frozen source payload")
            self.ledger.reserve({"thread_id": self.state.thread_id, "request_id": ident,
                                 "wire_sha256": sha(wire), "input_sha256": self.state.input_sha})
            self.state.dispatch_state = "sending"
        if method == "turn/interrupt":
            if self.state.turn_id is None or params != {"threadId": self.state.thread_id, "turnId": self.state.turn_id}:
                raise Blocked("interrupt requires exact acknowledged thread/turn")
            self.state.cancel_requested = True
        self.active = True
        deadline = time.monotonic() + timeout
        try:
            self.transport.send(message)
            while True:
                m = self.transport.receive(deadline)
                if not isinstance(m, dict):
                    raise ProtocolFault("non-object frame")
                if "method" in m:
                    if "id" in m:
                        # Deny additional effects; do not retain request payload.
                        self.transport.send({"id": m["id"], "error": {"code": -32601, "message": "Additional effects denied"}})
                    else:
                        self.state.event(m)
                    continue
                if type(m.get("id")) is not int or m["id"] != ident:
                    self.rejected_replies += 1
                    continue
                if ("result" in m) == ("error" in m):
                    raise ProtocolFault("response must contain exactly one result/error")
                if "error" in m:
                    if method == "turn/start":
                        self.state.dispatch_state = "rejected"
                        self.state.pending.clear()
                    raise RequestRejected("native request rejected; raw details discarded")
                result = m["result"]
                if method == "turn/start":
                    if not isinstance(result, dict):
                        raise ProtocolFault("invalid turn result")
                    self.state.acknowledge(result.get("turn"), ident, sha(wire))
                elif method == "turn/interrupt":
                    if result != {}:
                        raise ProtocolFault("interrupt acknowledgment is not an empty response")
                    self.state.cancel_acknowledged = True
                return result
        except (EOFError, TimeoutError, OSError) as exc:
            outcome = self.state.outcome()
            fault = UnknownOutcome if outcome == "unknown_outcome" else TransportFault
            raise fault(outcome) from exc
        finally:
            self.active = False

    def wait_terminal(self, timeout=60):
        if self.state.turn_id is None:
            raise Blocked("no acknowledged turn to observe; reconcile dispatch first")
        deadline = time.monotonic() + timeout
        while self.state.terminal is None:
            try:
                m = self.transport.receive(deadline)
                if not isinstance(m, dict):
                    raise ProtocolFault("non-object frame")
                if "method" in m and "id" not in m:
                    self.state.event(m)
                elif "method" in m:
                    self.transport.send({"id": m["id"], "error": {"code": -32601, "message": "Additional effects denied"}})
                else:
                    self.rejected_replies += 1
            except (EOFError, TimeoutError, OSError) as exc:
                outcome = self.state.outcome()
                fault = UnknownOutcome if outcome == "unknown_outcome" else TransportFault
                raise fault(outcome) from exc
        return self.state.outcome()


def prepare_dispatch(policy, requested_scopes, account, config, thread, captures, question, expected_payload_sha, confirm=None):
    for capture in captures:
        project, commit = capture.identity[:2]
        if ("revision", project, commit) not in requested_scopes or not any(
                len(grant) == 4 and grant[:2] == ("project", project) for grant in requested_scopes):
            raise Blocked("captured project/revision not declared in requested scope")
    if ("provider", "openai", "chatgpt", "local-owner-only") not in requested_scopes:
        raise Blocked("local subscription provider scope not declared")
    policy.require(requested_scopes, confirm)
    guard_identity(account, config, thread)
    payload = question + "\n" + "\n".join(c.data.decode("utf-8") for c in captures)
    if sha(payload.encode()) != expected_payload_sha:
        raise Blocked("exact source/question payload binding changed")
    return payload


if __name__ == "__main__":
    import unittest
    import sys
    if len(sys.argv) != 1:
        raise SystemExit("Offline only: no live/provider mode is implemented")
    class OfflineResult(unittest.TextTestResult):
        def __init__(self, *args, **kwargs):
            super().__init__(*args, **kwargs)
            self.passed_ids = []

        def addSuccess(self, test):
            self.passed_ids.append(test.id())
            super().addSuccess(test)

    root = Path(__file__).resolve().parent
    suite = unittest.defaultTestLoader.discover(str(root), pattern="test_adoption.py")
    result = unittest.TextTestRunner(verbosity=2, resultclass=OfflineResult).run(suite)
    evidence = {"kind": "injected behavioral checks only", "tests_run": result.testsRun,
                "passed_test_ids": result.passed_ids, "failure_count": len(result.failures),
                "error_count": len(result.errors), "success": result.wasSuccessful(),
                "live_provider_calls": 0, "native_app_server_processes": 0,
                "limitation": "No live transport exists in this candidate. These results do not prove native framing, provider behavior or real cancellation."}
    (root / "offline-checks.json").write_text(json.dumps(evidence, indent=2) + "\n")
    print(json.dumps({k: evidence[k] for k in ("kind", "tests_run", "success", "live_provider_calls")}))
    raise SystemExit(0 if result.wasSuccessful() else 1)
