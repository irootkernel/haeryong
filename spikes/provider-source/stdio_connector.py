"""Private preparation: JSON-lines transport for the unchanged State/Client.

Importing or executing this module never starts a process. Formal ownership must
supply argv and complete account/config/thread/source guards before turn/start.
No model, effort, permission, hook, plugin, account or global config is selected
here. No raw frames, stderr, reasoning, credentials or environment values logged.
"""
import json
import os
import select
import signal
import subprocess
import time
from pathlib import Path

from hardened_harness import Blocked, Client, selected_environment


class LaunchSetupFault(OSError):
    def __init__(self, process, receipt):
        super().__init__("owned pipe setup failed; inspect cleanup receipt")
        self.owned_process = process
        self.cleanup_receipt = receipt


class FrameFault(OSError):
    """Framing failure. Client maps this to its observed execution outcome."""


def encode_frame(message):
    return (json.dumps(message, ensure_ascii=False, separators=(",", ":"),
                       allow_nan=False) + "\n").encode("utf-8")


def _pairs(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON key")
        result[key] = value
    return result


def _constant(_value):
    raise ValueError("non-JSON constant")


class PipeIO:
    """Only the supplied owned child's nonblocking stdin/stdout file descriptors."""
    def __init__(self, process):
        self.stdin, self.stdout = process.stdin, process.stdout
        self.read_fd, self.write_fd = self.stdout.fileno(), self.stdin.fileno()
        os.set_blocking(self.read_fd, False)
        os.set_blocking(self.write_fd, False)

    def wait(self, direction, timeout):
        if direction == "read":
            ready, _, _ = select.select([self.read_fd], [], [], timeout)
        else:
            _, ready, _ = select.select([], [self.write_fd], [], timeout)
        return bool(ready)

    def read(self):
        return os.read(self.read_fd, 65536)

    def write(self, data):
        return os.write(self.write_fd, data)

    def close_input(self):
        self.stdin.close()

    def close_output(self):
        self.stdout.close()


class StdioTransport:
    """No retries; one absolute session deadline also bounds partial writes.

    pipe_io and clock are injected in offline tests. receive's per-operation
    deadline may shorten, but can never extend, the session deadline.
    """
    def __init__(self, pipe_io, deadline, clock=time.monotonic):
        self.io, self.deadline, self.clock = pipe_io, deadline, clock
        self.operation_deadline = deadline
        self.buffer = bytearray()
        self.closed = False

    def _wait(self, direction, deadline):
        if self.closed:
            raise OSError("owned transport closed")
        remaining = min(self.deadline, self.operation_deadline, deadline) - self.clock()
        if remaining <= 0 or not self.io.wait(direction, remaining):
            raise TimeoutError("absolute stdio deadline reached")
        if self.clock() >= min(self.deadline, self.operation_deadline, deadline):
            raise TimeoutError("absolute stdio deadline reached")

    def send(self, message):
        wire = encode_frame(message)
        offset = 0
        while offset < len(wire):
            self._wait("write", self.deadline)
            try:
                count = self.io.write(wire[offset:])
            except (BlockingIOError, InterruptedError):
                continue
            if type(count) is not int or count <= 0 or count > len(wire) - offset:
                raise OSError("owned stdin write failed")
            offset += count

    def receive(self, deadline):
        deadline = min(self.deadline, self.operation_deadline, deadline)
        while True:
            if self.closed:
                raise OSError("owned transport closed")
            if self.clock() >= deadline:
                raise TimeoutError("absolute stdio deadline reached")
            newline = self.buffer.find(b"\n")
            if newline >= 0:
                frame = bytes(self.buffer[:newline])
                del self.buffer[:newline + 1]
                try:
                    message = json.loads(frame.decode("utf-8"), object_pairs_hook=_pairs,
                                         parse_constant=_constant)
                except (ValueError, UnicodeError):
                    raise FrameFault("invalid JSON-lines frame") from None
                if not isinstance(message, dict):
                    raise FrameFault("non-object JSON-lines frame")
                return message
            self._wait("read", deadline)
            try:
                data = self.io.read()
            except (BlockingIOError, InterruptedError):
                continue
            if not data:
                if self.buffer:
                    self.buffer.clear()
                    raise FrameFault("EOF with truncated JSON-lines frame")
                raise EOFError("owned stdout EOF")
            self.buffer.extend(data)


class DeadlineClient(Client):
    """Retain State/Client semantics; bound send and receive by one operation clock."""
    def request(self, method, params, timeout=35):
        if self.active:
            raise Blocked("concurrent requests are unsupported by this serial adapter")
        prior = self.transport.operation_deadline
        self.transport.operation_deadline = min(prior, self.transport.clock() + timeout)
        try:
            return super().request(method, params, timeout)
        finally:
            self.transport.operation_deadline = prior

    def wait_terminal(self, timeout=60):
        prior = self.transport.operation_deadline
        self.transport.operation_deadline = min(prior, self.transport.clock() + timeout)
        try:
            return super().wait_terminal(timeout)
        finally:
            self.transport.operation_deadline = prior


class OwnedConnector(StdioTransport):
    """Own exactly the process/session returned by launch_owned's factory.

    Cleanup records real wait results independently of State terminal receipts.
    A reaped leader does not prove all descendants exited; group signals cover
    the active session while its leader is still alive. No unrelated PID lookup.
    """
    def __init__(self, process, pipe_io, deadline, clock=time.monotonic,
                 signal_group=os.killpg):
        super().__init__(pipe_io, deadline, clock)
        self.process, self.signal_group = process, signal_group
        self.cleanup_receipt = None

    def close(self, deadline):
        if self.cleanup_receipt is not None:
            return self.cleanup_receipt
        self.closed = True
        self.buffer.clear()
        signals = []
        reaped = False
        result = None
        input_closed = False
        output_closed = False
        try:
            try:
                self.io.close_input()
                input_closed = True
            except OSError:
                pass
            for stage in (None, signal.SIGTERM, signal.SIGKILL):
                if self.process.poll() is None and stage is not None:
                    try:
                        self.signal_group(self.process.pid, stage)
                        signals.append(stage.name)
                    except ProcessLookupError:
                        pass
                remaining = max(0.0, deadline - self.clock())
                allowance = remaining if stage == signal.SIGKILL else min(0.25, remaining)
                try:
                    result = self.process.wait(timeout=allowance)
                    reaped = True
                    break
                except subprocess.TimeoutExpired:
                    continue
        finally:
            try:
                self.io.close_output()
                output_closed = True
            except OSError:
                pass
        self.cleanup_receipt = {"status": "reaped" if reaped else "cleanup_unresolved",
            "exit_code": result, "signals": signals,
            "stdin_closed": input_closed, "stdout_closed": output_closed,
            "scope": "owned process and its created session only"}
        return self.cleanup_receipt


def launch_owned(argv, cwd, parent_env, deadline, *, launch_authorized=False,
                 process_factory=subprocess.Popen, io_factory=PipeIO,
                 clock=time.monotonic, signal_group=os.killpg):
    """Explicit future call only. Authorization flag is not a guard substitute.

    The owner must wire actual initialize/account/read(refreshToken:false),
    effective explicit-openai config, permission/thread checks and frozen source
    scope before any turn. Tests use injected factories, never native processes.
    """
    if launch_authorized is not True:
        raise Blocked("native launch requires explicit formal owner dispatch")
    if not isinstance(argv, (list, tuple)) or not argv or any(not isinstance(x, str) or not x for x in argv):
        raise Blocked("explicit native argv required")
    if not Path(cwd).is_absolute() or clock() >= deadline:
        raise Blocked("absolute cwd and unexpired session deadline required")
    process = process_factory(list(argv), cwd=str(cwd), env=selected_environment(parent_env),
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
        bufsize=0, start_new_session=True)
    try:
        pipe_io = io_factory(process)
    except Exception:
        # Setup failed before protocol input. Keep the handle if reaping is unknown.
        reaped, result = False, None
        try:
            try:
                signal_group(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            try:
                result = process.wait(timeout=max(0.0, deadline - clock()))
                reaped = True
            except (subprocess.TimeoutExpired, OSError):
                pass
        finally:
            for stream in (process.stdin, process.stdout):
                if stream is not None:
                    try:
                        stream.close()
                    except OSError:
                        pass
        raise LaunchSetupFault(process, {"status": "reaped" if reaped else "cleanup_unresolved",
            "exit_code": result, "dispatch": "not_dispatched"}) from None
    return OwnedConnector(process, pipe_io, deadline, clock, signal_group)
