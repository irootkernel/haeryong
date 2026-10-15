"""Injected byte-stream/process tests. No subprocess, provider or network starts."""
import json
import signal
import subprocess
import tempfile
import unittest
from pathlib import Path

from hardened_harness import Blocked, Client, DispatchLedger, State, TransportFault, UnknownOutcome
from stdio_connector import DeadlineClient, FrameFault, OwnedConnector, StdioTransport, encode_frame, launch_owned


class Clock:
    def __init__(self): self.now = 0.0
    def __call__(self): return self.now


class ByteIO:
    def __init__(self, chunks=(), writes=(), clock=None, advance=0):
        self.chunks, self.writes = list(chunks), list(writes)
        self.clock, self.advance = clock, advance
        self.sent = bytearray()
        self.waits = []
        self.closed_in = self.closed_out = 0
    def wait(self, direction, timeout):
        self.waits.append((direction, timeout))
        if self.clock: self.clock.now += min(self.advance, timeout)
        return True
    def read(self):
        value = self.chunks.pop(0) if self.chunks else b""
        if isinstance(value, Exception): raise value
        return value
    def write(self, data):
        value = self.writes.pop(0) if self.writes else len(data)
        if isinstance(value, Exception): raise value
        self.sent.extend(data[:max(0, value)])
        return value
    def close_input(self): self.closed_in += 1
    def close_output(self): self.closed_out += 1


class FakeProcess:
    pid = 31337
    def __init__(self, waits):
        self.waits, self.wait_calls = list(waits), []
        self.exit_code = None
        self.stdin = self.stdout = None
    def poll(self): return self.exit_code
    def wait(self, timeout):
        self.wait_calls.append(timeout)
        value = self.waits.pop(0)
        if isinstance(value, Exception): raise value
        self.exit_code = value
        return value


def timeout(): return subprocess.TimeoutExpired("injected-owned-process", 0)


class ByteStreamBehavior(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(dir=Path(__file__).resolve().parent)
        self.root, self.clock = Path(self.temp.name), Clock()
    def tearDown(self): self.temp.cleanup()
    def transport(self, chunks=(), writes=(), advance=0, deadline=10):
        io = ByteIO(chunks, writes, self.clock, advance)
        return StdioTransport(io, deadline, self.clock)
    def client(self, transport):
        state = State("thread-A", "fixed source")
        return DeadlineClient(state, transport, DispatchLedger(self.root / "intent.json"))
    def start(self, client):
        return client.request("turn/start", {"threadId": "thread-A", "input": [{"type": "text", "text": "fixed source"}]})

    def test_utf8_and_newline_can_split_at_every_byte(self):
        message = {"id": 1, "result": {"text": "해룡"}}
        transport = self.transport([bytes([byte]) for byte in encode_frame(message)])
        self.assertEqual(transport.receive(5), message)

    def test_coalesced_frames_preserve_next_frame_and_clean_eof(self):
        a, b = {"id": 1, "result": {}}, {"method": "notice", "params": {}}
        transport = self.transport([encode_frame(a) + encode_frame(b)])
        self.assertEqual(transport.receive(5), a)
        self.assertEqual(transport.receive(5), b)
        with self.assertRaises(EOFError): transport.receive(5)
        self.assertEqual(len(transport.io.waits), 2)

    def test_partial_write_and_nonblocking_interrupt_preserve_exact_wire_once(self):
        message = {"id": 1, "method": "read", "params": {"text": "해룡"}}
        transport = self.transport(writes=[1, BlockingIOError(), 2, InterruptedError(), 3])
        transport.send(message)
        self.assertEqual(bytes(transport.io.sent), encode_frame(message))
        self.assertTrue(bytes(transport.io.sent).endswith(b"\n"))

    def test_zero_write_after_partial_dispatch_is_unknown_and_never_redispatched(self):
        transport = self.transport(writes=[4, 0])
        client = self.client(transport)
        with self.assertRaises(UnknownOutcome): self.start(client)
        self.assertEqual(client.state.outcome(), "unknown_outcome")
        self.assertEqual(len(transport.io.sent), 4)
        with self.assertRaises(Blocked): self.start(client)
        self.assertEqual(len(transport.io.sent), 4)

    def test_broken_pipe_after_partial_write_is_unknown(self):
        transport = self.transport(writes=[7, BrokenPipeError()])
        client = self.client(transport)
        with self.assertRaises(UnknownOutcome): self.start(client)
        self.assertEqual(len(transport.io.sent), 7)
        self.assertIsNone(client.state.turn_id)

    def test_clean_eof_before_turn_does_not_invent_execution(self):
        client = self.client(self.transport())
        with self.assertRaises(TransportFault) as result:
            client.request("account/read", {"refreshToken": False})
        self.assertNotIsInstance(result.exception, UnknownOutcome)
        self.assertEqual(client.state.outcome(), "not_dispatched")
        self.assertFalse((self.root / "intent.json").exists())

    def test_clean_eof_after_turn_write_is_unknown(self):
        client = self.client(self.transport())
        with self.assertRaises(UnknownOutcome): self.start(client)
        self.assertIsNone(client.state.turn_id)
        self.assertTrue((self.root / "intent.json").exists())

    def test_eof_without_delimiter_rejects_even_complete_looking_json(self):
        for data in (b'{"id":1,"result":', b'{"id":1,"result":{}}'):
            with self.subTest(data=data):
                transport = self.transport([data])
                with self.assertRaises(FrameFault) as result: transport.receive(5)
                self.assertEqual(str(result.exception), "EOF with truncated JSON-lines frame")
                self.assertEqual(transport.buffer, b"")

    def test_truncated_dispatch_response_maps_to_unknown_not_ack(self):
        client = self.client(self.transport([b'{"id":1,"result":{"turn":{"id":"turn-A"}}}']))
        with self.assertRaises(UnknownOutcome): self.start(client)
        self.assertIsNone(client.state.turn_id)

    def test_malformed_utf8_json_duplicate_keys_nan_and_nonobject_fail_closed(self):
        for frame in (b'\xff\n', b'not json\n', b'{"id":1,"id":2}\n', b'{"x":NaN}\n', b'[]\n', b'\n'):
            with self.subTest(frame=frame):
                transport = self.transport([frame])
                with self.assertRaises(FrameFault) as result: transport.receive(5)
                self.assertNotIn(repr(frame), str(result.exception))

    def test_trickled_read_cannot_extend_absolute_session_deadline(self):
        transport = self.transport([b'{', b'"', b'x', b'"', b':'], advance=0.4, deadline=1)
        with self.assertRaises(TimeoutError): transport.receive(100)
        self.assertAlmostEqual(self.clock.now, 1)
        self.assertEqual(len(transport.io.waits), 3)
        self.assertLess(transport.io.waits[-1][1], 0.21)

    def test_buffered_frame_cannot_bypass_expired_deadline(self):
        transport = self.transport([b'{"a":1}\n{"b":2}\n'])
        self.assertEqual(transport.receive(1), {"a": 1})
        self.clock.now = 1
        with self.assertRaises(TimeoutError): transport.receive(1)

    def test_trickled_write_uses_same_absolute_deadline_and_blocks_redispatch(self):
        transport = self.transport(writes=[1] * 100, advance=0.4, deadline=1)
        client = self.client(transport)
        with self.assertRaises(UnknownOutcome): self.start(client)
        self.assertEqual(len(transport.io.sent), 2)
        with self.assertRaises(Blocked): self.start(client)

    def test_request_deadline_can_shorten_session_deadline(self):
        transport = self.transport([b'{'], advance=4, deadline=10)
        with self.assertRaises(TimeoutError): transport.receive(2)
        self.assertEqual(self.clock.now, 2)
        self.assertEqual(transport.io.waits, [("read", 2)])

    def test_framed_ack_exact_echo_and_terminal_bind_to_original_client(self):
        messages = [
            {"method": "turn/completed", "params": {"threadId": "foreign", "turn": {"id": "wrong", "status": "completed", "items": []}}},
            {"id": 1, "result": {"turn": {"id": "turn-A"}}},
            {"method": "item/completed", "params": {"threadId": "thread-A", "turnId": "turn-A", "item": {"type": "userMessage", "content": [{"type": "text", "text": "fixed source"}]}}},
            {"method": "turn/completed", "params": {"threadId": "thread-A", "turn": {"id": "turn-A", "status": "completed", "items": [{"id": "answer", "type": "agentMessage", "phase": "final_answer", "text": "bounded answer"}]}}}]
        data = b''.join(encode_frame(message) for message in messages)
        client = self.client(self.transport([data[:31], data[31:59], data[59:]]))
        self.start(client)
        self.assertEqual(client.wait_terminal(), "completed")
        self.assertEqual(client.state.final_answer(), "bounded answer")
        self.assertEqual(client.state.input_observation()["basis"], "injected_exact_input_echo")
        self.assertEqual(client.state.rejected_events, 1)

    def owned(self, waits):
        process, io, signals = FakeProcess(waits), ByteIO(), []
        connector = OwnedConnector(process, io, 10, self.clock, lambda pid, sig: signals.append((pid, sig)))
        return connector, process, io, signals

    def test_natural_exit_is_reaped_without_signaling_unrelated_process(self):
        connector, process, io, signals = self.owned([0])
        receipt = connector.close(2)
        self.assertEqual(receipt["status"], "reaped")
        self.assertEqual(receipt["exit_code"], 0)
        self.assertEqual(signals, [])
        self.assertEqual((io.closed_in, io.closed_out), (1, 1))
        self.assertEqual(process.wait_calls, [0.25])

    def test_owned_session_term_then_kill_and_exact_wait_result(self):
        connector, process, io, signals = self.owned([timeout(), timeout(), -9])
        receipt = connector.close(2)
        self.assertEqual(signals, [(process.pid, signal.SIGTERM), (process.pid, signal.SIGKILL)])
        self.assertEqual(receipt["signals"], ["SIGTERM", "SIGKILL"])
        self.assertEqual(receipt["exit_code"], -9)
        self.assertEqual(len(process.wait_calls), 3)

    def test_unreaped_process_is_explicitly_unresolved_with_closed_pipes(self):
        connector, process, io, signals = self.owned([timeout(), timeout(), timeout()])
        receipt = connector.close(0)
        self.assertEqual(receipt["status"], "cleanup_unresolved")
        self.assertIsNone(receipt["exit_code"])
        self.assertEqual(process.wait_calls, [0, 0, 0])
        self.assertEqual((io.closed_in, io.closed_out), (1, 1))

    def test_closed_transport_rejects_io_and_cleanup_is_idempotent(self):
        connector, process, io, signals = self.owned([0])
        receipt = connector.close(2)
        self.assertIs(connector.close(2), receipt)
        with self.assertRaises(OSError): connector.send({"id": 1})
        with self.assertRaises(OSError): connector.receive(2)
        self.assertEqual(len(process.wait_calls), 1)

    def test_default_launch_denied_before_process_factory(self):
        calls = []
        with self.assertRaises(Blocked):
            launch_owned(["never-run"], str(self.root), {}, 10, process_factory=lambda *a, **k: calls.append(k))
        self.assertEqual(calls, [])

    def test_injected_launch_preserves_parent_and_normal_hooks_plugins(self):
        parent = {"OPENAI_API_KEY": "synthetic", "CODEX_API_KEY": "synthetic", "PATH": "kept", "CODEX_HOME": "kept", "HOOK_CONTEXT": "kept", "PLUGIN_CONTEXT": "kept"}
        original, calls, process, io = dict(parent), [], FakeProcess([0]), ByteIO()
        def factory(argv, **kwargs):
            calls.append((argv, kwargs))
            return process
        connector = launch_owned(["fake-codex", "-c", 'model_provider="openai"', "app-server"], str(self.root), parent, 10,
            launch_authorized=True, process_factory=factory, io_factory=lambda p: io, clock=self.clock)
        argv, kwargs = calls[0]
        self.assertEqual(parent, original)
        self.assertEqual(kwargs["env"], {k: v for k, v in original.items() if k not in ("OPENAI_API_KEY", "CODEX_API_KEY")})
        self.assertTrue(kwargs["start_new_session"])
        self.assertEqual(kwargs["stderr"], subprocess.DEVNULL)
        self.assertFalse(any("disable" in arg for arg in argv))
        self.assertEqual(connector.close(2)["status"], "reaped")

    def test_pipe_setup_failure_kills_and_reaps_only_created_process(self):
        process, signals = FakeProcess([-9]), []
        def broken_io(_process): raise OSError("injected setup failure")
        with self.assertRaises(OSError):
            launch_owned(["fake-codex"], str(self.root), {}, 10, launch_authorized=True,
                process_factory=lambda *a, **k: process, io_factory=broken_io, clock=self.clock,
                signal_group=lambda pid, sig: signals.append((pid, sig)))
        self.assertEqual(signals, [(process.pid, signal.SIGKILL)])
        self.assertEqual(process.exit_code, -9)
        self.assertEqual(process.wait_calls, [10])

    def test_failed_setup_unresolved_wait_retains_owned_handle_and_closes_pipes(self):
        from stdio_connector import LaunchSetupFault
        class Stream:
            def __init__(self): self.closed = False
            def close(self): self.closed = True
        process, signals = FakeProcess([timeout()]), []
        process.stdin, process.stdout = Stream(), Stream()
        def broken_io(_process): raise OSError("private path must not surface")
        with self.assertRaises(LaunchSetupFault) as failure:
            launch_owned(["fake-codex"], str(self.root), {}, 10, launch_authorized=True,
                process_factory=lambda *a, **k: process, io_factory=broken_io, clock=self.clock,
                signal_group=lambda pid, sig: signals.append((pid, sig)))
        self.assertIs(failure.exception.owned_process, process)
        self.assertEqual(failure.exception.cleanup_receipt["status"], "cleanup_unresolved")
        self.assertEqual(failure.exception.cleanup_receipt["dispatch"], "not_dispatched")
        self.assertTrue(process.stdin.closed and process.stdout.closed)
        self.assertNotIn("private path", str(failure.exception))


    def test_request_timeout_bounds_partial_send_before_larger_session_deadline(self):
        transport = self.transport(writes=[1] * 100, advance=0.4, deadline=100)
        client = self.client(transport)
        with self.assertRaises(UnknownOutcome):
            client.request("turn/start", {"threadId": "thread-A", "input": [{"type": "text", "text": "fixed source"}]}, timeout=1)
        self.assertAlmostEqual(self.clock.now, 1)
        self.assertEqual(len(transport.io.sent), 2)
        self.assertEqual(transport.operation_deadline, 100)
        self.assertEqual(client.state.outcome(), "unknown_outcome")
