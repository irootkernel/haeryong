"""Injected behavioral checks only. No native client, provider, login or network."""
import json
import tempfile
import unittest
from pathlib import Path
from hardened_harness import (Blocked, TransportFault, UnknownOutcome, ProtocolFault, RequestRejected,
    Client, State, ScopePolicy, DispatchLedger, Capture, selected_environment,
    normalize_error, read_captures, guard_identity, prepare_dispatch, sha)


def terminal(status="completed", thread="thread-A", turn="turn-A", items=None):
    return {"method": "turn/completed", "params": {"threadId": thread,
        "turn": {"id": turn, "status": status, "items": items or []}}}


def item(value, thread="thread-A", turn="turn-A"):
    return {"method": "item/completed", "params": {"threadId": thread,
        "turnId": turn, "completedAtMs": 1, "item": value}}


def answer(text="correct answer", phase="final_answer", ident="answer-A"):
    return {"id": ident, "type": "agentMessage", "phase": phase, "text": text}


def error(code, retry=False, thread="thread-A", turn="turn-A"):
    return {"method": "error", "params": {"threadId": thread, "turnId": turn,
        "willRetry": retry, "error": {"codexErrorInfo": code, "message": "discard-me"}}}


class InjectedTransport:
    def __init__(self, frames, send_error=None):
        self.frames = list(frames)
        self.sent = []
        self.deadlines = []
        self.send_error = send_error

    def send(self, frame):
        self.sent.append(frame)
        if self.send_error:
            raise self.send_error

    def receive(self, deadline):
        self.deadlines.append(deadline)
        if not self.frames:
            raise EOFError("injected transport lost")
        frame = self.frames.pop(0)
        if isinstance(frame, Exception):
            raise frame
        return frame


class AdapterBehavior(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(dir=Path(__file__).resolve().parent)
        self.root = Path(self.temp.name)
        self.state = State("thread-A", "source bytes")

    def tearDown(self):
        self.temp.cleanup()

    def client(self, frames=(), send_error=None):
        transport = InjectedTransport(frames, send_error)
        return Client(self.state, transport, DispatchLedger(self.root / "intent.json"))

    def start(self, client):
        return client.request("turn/start", {"threadId": "thread-A",
            "input": [{"type": "text", "text": "source bytes"}]})

    def ack(self):
        self.state.dispatch_state = "sending"
        self.state.acknowledge({"id": "turn-A"}, 1, "wire-hash")

    def test_notification_before_reply_waits_for_exact_ack(self):
        c = self.client([item(answer("foreign"), turn="foreign"),
            terminal(items=[answer()]), {"id": 1, "result": {"turn": {"id": "turn-A"}}}])
        self.start(c)
        self.assertEqual(self.state.turn_id, "turn-A")
        self.assertEqual(self.state.final_answer(), "correct answer")
        self.assertEqual(self.state.rejected_events, 1)

    def test_wrong_thread_cannot_supply_terminal_or_answer(self):
        self.ack()
        self.state.event(terminal(thread="other", items=[answer("wrong")]))
        self.assertIsNone(self.state.terminal)
        self.assertEqual(self.state.answers, {})

    def test_wrong_turn_does_not_rebind_current_identity(self):
        self.ack()
        self.state.event({"method": "turn/started", "params": {"threadId": "thread-A", "turn": {"id": "foreign"}}})
        self.state.event(terminal(turn="foreign", items=[answer("wrong")]))
        self.assertEqual(self.state.turn_id, "turn-A")
        self.assertEqual(self.state.outcome(), "unknown_outcome")

    def test_missing_item_identity_is_not_answer(self):
        self.ack()
        self.state.event({"method": "item/completed", "params": {"item": answer("wrong")}})
        self.assertEqual(self.state.answers, {})

    def test_early_terminal_without_dispatch_is_not_adopted(self):
        self.state.event(terminal())
        self.assertIsNone(self.state.terminal)
        self.assertEqual(self.state.outcome(), "not_dispatched")

    def test_lost_dispatch_response_is_unknown_even_with_early_completion(self):
        c = self.client([terminal(items=[answer()]), EOFError()])
        with self.assertRaises(UnknownOutcome):
            self.start(c)
        self.assertIsNone(self.state.turn_id)
        self.assertIsNone(self.state.terminal)
        self.assertIsNone(self.state.input_observation())
        self.assertEqual(self.state.outcome(), "unknown_outcome")
        with self.assertRaises(Blocked):
            self.start(c)
        self.assertEqual(len(c.transport.sent), 1)

    def test_uncertain_write_blocks_second_process_same_intent(self):
        c = self.client(send_error=OSError("injected partial write"))
        with self.assertRaises(UnknownOutcome):
            self.start(c)
        other_state = State("thread-A", "source bytes")
        second = Client(other_state, InjectedTransport([]), DispatchLedger(self.root / "intent.json"))
        with self.assertRaises(Blocked):
            second.request("turn/start", {"threadId": "thread-A", "input": [{"type": "text", "text": "source bytes"}]})
        self.assertEqual(second.transport.sent, [])

    def test_wrong_response_ids_and_boolean_id_do_not_unblock_request(self):
        c = self.client([{"id": 2, "result": {"turn": {"id": "foreign"}}},
            {"id": True, "result": {"turn": {"id": "foreign"}}},
            {"id": 1, "result": {"turn": {"id": "turn-A"}}}])
        self.start(c)
        self.assertEqual(c.rejected_replies, 2)
        self.assertEqual(self.state.turn_id, "turn-A")
        self.assertEqual(len(set(c.transport.deadlines)), 1)

    def test_json_rpc_rejection_is_not_success_or_retry_permission(self):
        c = self.client([{"id": 1, "error": {"message": "secret diagnostic"}}])
        with self.assertRaises(RequestRejected) as result:
            self.start(c)
        self.assertNotIn("secret", str(result.exception))
        self.assertEqual(self.state.outcome(), "request_rejected")
        with self.assertRaises(Blocked):
            self.start(c)

    def test_malformed_reply_is_not_accepted(self):
        c = self.client([{"id": 1, "result": {}, "error": {}}])
        with self.assertRaises(ProtocolFault):
            self.start(c)
        self.assertEqual(self.state.outcome(), "unknown_outcome")

    def test_server_effect_request_is_denied_without_retaining_payload(self):
        c = self.client([{"method": "item/commandExecution/requestApproval", "id": 70,
            "params": {"secret": "never retain"}}, {"id": 1, "result": {"turn": {"id": "turn-A"}}}])
        self.start(c)
        self.assertEqual(c.transport.sent[1]["error"]["code"], -32601)
        self.assertNotIn("never retain", repr(vars(self.state)))

    def test_cancel_request_ack_is_not_terminal(self):
        self.ack()
        c = self.client([{"id": 1, "result": {}}])
        c.request("turn/interrupt", {"threadId": "thread-A", "turnId": "turn-A"})
        self.assertTrue(self.state.cancel_requested)
        self.assertTrue(self.state.cancel_acknowledged)
        self.assertEqual(self.state.cancellation_outcome(), "cancellation_unresolved")
        self.assertIsNone(self.state.terminal)

    def test_cancel_completion_race_retains_completed_result(self):
        self.ack()
        c = self.client([terminal(items=[answer()]), {"id": 1, "result": {}}])
        c.request("turn/interrupt", {"threadId": "thread-A", "turnId": "turn-A"})
        self.assertEqual(self.state.cancellation_outcome(), "completed")
        self.assertEqual(self.state.final_answer(), "correct answer")

    def test_interrupt_ack_lost_after_terminal_does_not_erase_terminal(self):
        self.ack()
        c = self.client([terminal("interrupted"), EOFError()])
        with self.assertRaises(TransportFault) as result:
            c.request("turn/interrupt", {"threadId": "thread-A", "turnId": "turn-A"})
        self.assertEqual(str(result.exception), "interrupted")
        self.assertFalse(self.state.cancel_acknowledged)
        self.assertEqual(self.state.cancellation_outcome(), "interrupted")

    def test_read_request_transport_loss_before_dispatch_does_not_invent_execution(self):
        c = self.client([EOFError()])
        with self.assertRaises(TransportFault) as result:
            c.request("account/read", {"refreshToken": False})
        self.assertNotIsInstance(result.exception, UnknownOutcome)
        self.assertEqual(self.state.outcome(), "not_dispatched")
        self.assertFalse((self.root / "intent.json").exists())

    def test_interrupt_nonempty_response_cannot_supply_terminal_status(self):
        self.ack()
        c = self.client([{"id": 1, "result": {"accepted": True, "status": "interrupted"}}])
        with self.assertRaises(ProtocolFault):
            c.request("turn/interrupt", {"threadId": "thread-A", "turnId": "turn-A"})
        self.assertFalse(self.state.cancel_acknowledged)
        self.assertIsNone(self.state.terminal)

    def test_interrupt_foreign_or_unknown_turn_is_blocked_before_send(self):
        c = self.client([])
        with self.assertRaises(Blocked):
            c.request("turn/interrupt", {"threadId": "thread-A", "turnId": "turn-A"})
        self.ack()
        with self.assertRaises(Blocked):
            c.request("turn/interrupt", {"threadId": "other", "turnId": "turn-A"})
        self.assertEqual(c.transport.sent, [])

    def test_late_transport_loss_preserves_each_definitive_terminal(self):
        for status in ("completed", "failed", "interrupted"):
            with self.subTest(status=status):
                s = State("thread-A", "source bytes")
                s.acknowledge({"id": "turn-A"}, 1, "hash")
                s.event(terminal(status))
                self.assertEqual(s.outcome(), status)

    def test_wait_terminal_ignores_foreign_completion_and_keeps_absolute_deadline(self):
        self.ack()
        c = self.client([terminal(thread="wrong"), terminal("failed")])
        self.assertEqual(c.wait_terminal(), "failed")
        self.assertEqual(len(set(c.transport.deadlines)), 1)

    def test_wait_loss_without_terminal_is_unknown(self):
        self.ack()
        c = self.client([TimeoutError()])
        with self.assertRaises(UnknownOutcome):
            c.wait_terminal()
        self.assertEqual(self.state.outcome(), "unknown_outcome")

    def test_partial_commentary_reasoning_and_started_items_not_final(self):
        self.ack()
        for phase in ("partial_answer", "commentary"):
            self.state.event(item(answer("must not retain", phase)))
        self.state.event(item({"type": "reasoning", "text": "hidden reasoning"}))
        self.state.event({"method": "item/started", "params": {"threadId": "thread-A", "turnId": "turn-A", "item": answer("early")}})
        self.assertNotIn("must not retain", repr(vars(self.state)))
        self.assertNotIn("hidden reasoning", repr(vars(self.state)))
        self.state.event(terminal())
        with self.assertRaises(Blocked):
            self.state.final_answer()

    def test_unknown_phase_legacy_answer_only_after_completed(self):
        self.ack()
        self.state.event(item(answer("legacy", None)))
        with self.assertRaises(Blocked):
            self.state.final_answer()
        self.state.event(terminal())
        self.assertEqual(self.state.final_answer(), "legacy")

    def test_explicit_final_takes_precedence_and_late_items_cannot_append(self):
        self.ack()
        self.state.event(item(answer("legacy", None, "old")))
        self.state.event(terminal(items=[answer("final")]))
        self.state.event(item(answer("late", ident="late")))
        self.assertEqual(self.state.final_answer(), "final")

    def test_conflicting_terminal_does_not_overwrite_first(self):
        self.ack()
        self.state.event(terminal("completed"))
        self.state.event(terminal("failed"))
        self.assertEqual(self.state.terminal["status"], "completed")
        self.assertEqual(self.state.outcome(), "protocol_conflict")

    def test_exact_input_echo_requires_matching_ack_and_identity(self):
        echo = {"type": "userMessage", "content": [{"type": "text", "text": "source bytes"}]}
        self.ack()
        self.state.event(item(echo, turn="wrong"))
        self.assertIsNone(self.state.input_observation())
        self.state.event(item({"type": "userMessage", "content": [{"type": "text", "text": "wrong bytes"}]}))
        self.assertIsNone(self.state.input_observation())
        self.state.event(item(echo))
        observed = self.state.input_observation()
        self.assertEqual(observed["basis"], "injected_exact_input_echo")
        self.assertEqual(observed["turn_id"], "turn-A")

    def test_error_notifications_require_exact_thread_turn_and_provenance(self):
        self.ack()
        self.state.event(error("unauthorized", thread="other"))
        self.state.event(error("usageLimitExceeded", turn="other"))
        self.assertEqual(self.state.errors, [])
        self.state.event(error("unauthorized", retry=True))
        self.assertEqual(self.state.errors[0]["category"], "auth_required")
        self.assertEqual(self.state.errors[0]["execution_hint"], "native_retry_pending")
        self.assertEqual(self.state.errors[0]["evidence_origin"], "injected")
        self.assertIsNone(self.state.terminal)

    def test_auth_quota_rate_capacity_and_unknown_are_not_conflated(self):
        expected = {"unauthorized": "auth_required", "usageLimitExceeded": "quota_exhausted",
            "rateLimitExceeded": "rate_limited", "flexUnavailable": "provider_unavailable",
            "serverOverloaded": "provider_unavailable", "sessionBudgetExceeded": "session_budget_exceeded",
            "contextWindowExceeded": "context_limit", "unrecognized-code": "provider_error"}
        for code, category in expected.items():
            self.assertEqual(normalize_error(code)["category"], category)
        self.assertEqual(normalize_error({"responseStreamDisconnected": {"httpStatusCode": 500}})["execution_hint"], "unknown_outcome")
        self.assertEqual(normalize_error("usageLimitExceeded", origin="live")["evidence_origin"], "live")
        # This explicitly constructs a provenance projection; it is not a live observation.

    def test_child_environment_excludes_only_documented_api_keys(self):
        parent = {"OPENAI_API_KEY": "sentinel", "CODEX_API_KEY": "sentinel",
            "PATH": "kept", "CODEX_HOME": "kept", "MCP_CONTEXT": "kept", "HOOK_CONTEXT": "kept"}
        before = dict(parent)
        self.assertEqual(selected_environment(parent), {k: v for k, v in parent.items() if k not in ("OPENAI_API_KEY", "CODEX_API_KEY")})
        self.assertEqual(parent, before)

    def fixture_capture(self):
        capture = self.root / "source.txt"
        capture.write_bytes(b"fixed source bytes\n")
        row = {"project": "p", "commit": "fixed", "path": "file.go", "line_start": 1,
            "line_end": 1, "capture_path": str(capture), "excerpt_sha256": sha(capture.read_bytes())}
        manifest = self.root / "manifest.json"
        manifest.write_text(json.dumps([row]))
        return capture, manifest, sha(manifest.read_bytes())

    def test_missing_changed_manifest_or_capture_blocks_before_dispatch(self):
        capture, manifest, digest = self.fixture_capture()
        capture.write_bytes(b"changed source")
        with self.assertRaises(Blocked):
            read_captures(manifest, digest, [self.root])
        capture.unlink()
        with self.assertRaises(Blocked):
            read_captures(manifest, digest, [self.root])
        manifest.write_text("[]")
        with self.assertRaises(Blocked):
            read_captures(manifest, digest, [self.root])
        manifest.unlink()
        with self.assertRaises(Blocked):
            read_captures(manifest, digest, [self.root])

    def test_frozen_bytes_survive_concurrent_checkout_and_capture_change(self):
        capture, manifest, digest = self.fixture_capture()
        captures = read_captures(manifest, digest, [self.root])
        (self.root / "mutable-checkout").write_text("concurrent working bytes")
        capture.write_text("later capture mutation")
        self.assertEqual(captures[0].data, b"fixed source bytes\n")
        self.assertEqual(captures[0].sha256, sha(captures[0].data))

    def test_capture_root_escape_is_not_read(self):
        capture, manifest, digest = self.fixture_capture()
        with self.assertRaises(Blocked):
            read_captures(manifest, digest, [self.root / "not-the-root"])

    def test_remembered_scope_skips_reprompt_but_expansion_needs_explicit_true(self):
        known = {("project", "p", "/approved", "private"), ("revision", "p", "fixed"),
            ("provider", "openai", "chatgpt", "local-owner-only")}
        policy = ScopePolicy(known)
        asked = []
        callback = lambda missing: asked.append(missing) or False
        policy.require(known, callback)
        policy.require(known, callback)
        self.assertEqual(asked, [])
        for expansion in [("project", "other", "/outside", "private"),
                          ("project", "p", "/different-root", "private"),
                          ("provider", "other", "api-key", "remote"),
                          ("project", "p", "/approved", "public")]:
            with self.assertRaises(Blocked):
                policy.require(known | {expansion}, callback)
            self.assertNotIn(expansion, policy.approved)
        with self.assertRaises(Blocked):
            policy.require(known | {("revision", "p", "unapproved")}, lambda missing: "yes")
        approved = ("revision", "p", "explicitly-approved-new")
        policy.require(known | {approved}, lambda missing: missing == {approved})
        self.assertIn(approved, policy.approved)

    def test_identity_guards_and_frozen_payload_scope_precede_dispatch(self):
        account = {"type": "chatgpt", "planType": "pro", "requiresOpenaiAuth": True}
        config = {"model_provider": "openai"}
        thread = {"modelProvider": "openai", "approvalPolicy": "never", "sandbox": {"type": "readOnly"}}
        scopes = {("project", "p", "/approved", "private"), ("revision", "p", "fixed"),
            ("provider", "openai", "chatgpt", "local-owner-only")}
        captures = (Capture(("p", "fixed", "file", 1, 1), b"bytes", sha(b"bytes")),)
        expected = sha(b"question\nbytes")
        self.assertEqual(prepare_dispatch(ScopePolicy(scopes), scopes, account, config, thread, captures, "question", expected), "question\nbytes")
        with self.assertRaises(Blocked):
            prepare_dispatch(ScopePolicy(scopes), scopes - {("revision", "p", "fixed")}, account, config, thread, captures, "question", expected)
        with self.assertRaises(Blocked):
            prepare_dispatch(ScopePolicy(scopes), scopes, account, config, thread, captures, "changed question", expected)
        for a, c, t in [({**account, "type": "apiKey"}, config, thread),
                         (account, {"model_provider": "other"}, thread),
                         (account, config, {**thread, "approvalPolicy": "on-request"})]:
            with self.assertRaises(Blocked):
                guard_identity(a, c, t)


if __name__ == "__main__":
    unittest.main(verbosity=2)
