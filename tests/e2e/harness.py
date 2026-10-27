"""Test-owned processes and public CLI/HTTP clients. No storage implementation access."""
from contextlib import contextmanager
import hashlib
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import queue
import signal
import socket
import subprocess
import threading
import time
import urllib.parse
import uuid


def operation():
    return str(uuid.uuid4())


def port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def write_json(path, value):
    with Path(path).open("x", encoding="utf-8") as stream:
        os.chmod(path, 0o600)
        json.dump(value, stream)


def digest(data):
    return hashlib.sha256(data).hexdigest()


class PrivateClient(dict):
    def __repr__(self):
        return "<test-owned client; credential fields redacted>"


class Product:
    def __init__(self, binary, root):
        self.binary = str(binary)
        self.root = Path(root).resolve()
        assert self.root.is_dir() and not self.root.is_symlink()
        self.profile = "e2e-" + uuid.uuid4().hex
        self.data_dir = self.root / "library"
        self.address = port()
        self.endpoint = f"http://127.0.0.1:{self.address}"
        self.process = None
        self.identities = []
        self.clients = []

    @property
    def local_flags(self):
        return ["--profile", self.profile, "--data-dir", str(self.data_dir), "--json"]

    def cli(self, args, *, client=None, code=0, stdin=None, timeout=30, ndjson=False):
        flags = client["flags"] if client else self.local_flags
        result = subprocess.run(
            [self.binary, *args, *flags],
            input=None if stdin is None else json.dumps(stdin),
            text=True, capture_output=True, timeout=timeout,
        )
        # Credential-bearing diagnostics remain in process memory, never assertion text.
        failure_code = None
        if result.returncode != code:
            try:
                failure_code = json.loads(result.stdout).get("error", {}).get("code")
            except (ValueError, AttributeError):
                pass
        assert result.returncode == code, f"CLI exit {result.returncode}; expected {code}; error_code={failure_code}; command={args[:2]}"
        if not result.stdout.strip():
            return None
        if ndjson:
            return [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
        return json.loads(result.stdout)

    def initialize(self):
        value = self.cli(["init"])["data"]
        self.initial = value
        return value

    def start(self, *, configured=False):
        assert self.process is None
        assert self.data_dir.is_relative_to(self.root)
        assert not self.data_dir.is_symlink()
        self.process = subprocess.Popen(
            [self.binary, "daemon", "run", *([] if configured else ["--port", str(self.address)]), *self.local_flags],
            stdout=subprocess.PIPE, stderr=getattr(self, 'diagnostic_stderr', subprocess.DEVNULL), text=True,
        )
        process = self.process
        ready = queue.Queue()

        def drain():
            first = process.stdout.readline()
            ready.put(first)
            for _ in process.stdout:
                pass

        self.reader = threading.Thread(target=drain, daemon=True)
        self.reader.start()
        try:
            row = json.loads(ready.get(timeout=15))
            identity = row["data"]
            assert identity["storage"]["ready"] is True
            assert identity["endpoint"] == self.endpoint
            if self.identities:
                assert identity["library_id"] == self.identities[0]["library_id"]
                assert identity["profile_id"] == self.identities[0]["profile_id"]
                assert identity["instance_id"] != self.identities[-1]["instance_id"]
            self.identities.append(identity)
        except BaseException:
            self.close()
            raise
        return identity

    def stop(self, *, kill=False):
        process = self.process
        assert process is not None
        if kill:
            process.kill()
        else:
            self.cli(["service", "stop"])
        result = process.wait(timeout=10)
        assert result == (-signal.SIGKILL if kill else 0)
        self.reader.join(timeout=5)
        process.stdout.close()
        self.process = None

    def close(self):
        if self.process is not None:
            if self.process.poll() is None:
                self.process.kill()
            self.process.wait(timeout=10)
            self.reader.join(timeout=5)
            self.process.stdout.close()
            self.process = None

    def request(self, method, path, *, client=None, body=None, headers=None, raw=False):
        connection = http.client.HTTPConnection("127.0.0.1", self.address, timeout=30)
        fields = {} if (path.startswith("/local/v1/") or path == "/health/live") else {"Origin": self.endpoint}
        if client:
            fields["Authorization"] = "Bearer " + client["token"]
        if body is not None and not isinstance(body, bytes):
            body = json.dumps(body).encode()
            fields["Content-Type"] = "application/json"
        fields.update(headers or {})
        try:
            connection.request(method, path, body=body, headers=fields)
            response = connection.getresponse()
            payload = response.read()
            status, returned = response.status, dict(response.getheaders())
        finally:
            connection.close()
        if raw or not payload:
            return status, payload, returned
        return status, json.loads(payload), returned

    def data(self, method, path, **kwargs):
        status, value, _ = self.request(method, path, **kwargs)
        assert status in {200, 201, 202, 204}, f"HTTP {status}: {method} {path.split('?')[0]}"
        return value.get("data") if isinstance(value, dict) else value

    def owner(self, label="test owner"):
        pairing = self.data("POST", "/api/v1/auth/pairings", body={"device_label": label})
        self.cli(["auth", "pairings"])
        self.cli(["auth", "approve", pairing["pairing_id"]], stdin={
            "code": pairing["code"], "origin": pairing["origin"],
            "etag": pairing["etag"], "operation_id": operation(),
        })
        poll = self.data("POST", f"/api/v1/auth/pairings/{pairing['pairing_id']}/poll",
                         body={"polling_secret": pairing["polling_secret"]})
        assert poll["token"] and poll["session"]["session_id"]
        caller = self.root / ("caller-" + uuid.uuid4().hex)
        caller.mkdir(mode=0o700)
        credential = caller / "credential.json"
        write_json(credential, {"endpoint": self.endpoint, "profile": self.profile,
                               "library_id": poll["session"]["library_id"], "token": poll["token"]})
        client = PrivateClient({"token": poll["token"], "session": poll["session"],
                  "caller": caller, "credential": credential,
                  "flags": ["--profile", self.profile, "--client-dir", str(caller),
                            "--credential", str(credential), "--json"]})
        self.clients.append(client)
        return client

    def job(self, client, job_id, *, timeout=30):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            value = self.data("GET", "/api/v1/jobs/" + job_id, client=client)
            if value["state"] in {"completed", "failed", "cancelled", "interrupted"}:
                assert value["state"] == "completed", f"job ended {value['state']}"
                return value.get("result")
            time.sleep(0.1)
        raise AssertionError("job readiness deadline")

    def import_tree(self, client, source, *, operation_id=None, timeout=30, extra=()):
        key = operation_id or operation()
        accepted = self.cli(["import", str(source), "--operation-id", key, *extra],
                            client=client, timeout=timeout)["data"]
        assert accepted["operation_id"] == key
        result = self.job(client, accepted["result"]["job_id"], timeout=timeout)
        return key, accepted, result

    def files(self, client, version, limit=17):
        items, cursor = [], None
        while True:
            params = {"limit": str(limit)}
            if cursor:
                params["cursor"] = cursor
            page = self.data("GET", f"/api/v1/versions/{version}/files?" + urllib.parse.urlencode(params), client=client)
            items.extend(page["items"])
            next_cursor = page.get("next_cursor")
            if not next_cursor:
                return items
            assert next_cursor != cursor
            cursor = next_cursor


class Proxy:
    """A test-owned loopback transport for lost acknowledgements and interrupted bodies."""
    def __init__(self, product, *, drop=None, interrupt_download=False, block_after_drop=False, before=None, after=None):
        self.product = product
        self.drop = drop
        self.interrupt_download = interrupt_download
        self.dropped = False
        self.block_uploads = False
        self.block_after_drop = block_after_drop
        self.blocked_path = None
        self.before = before
        self.after = after
        self.requests = []
        self.transport_errors = []
        owner = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, *_):
                pass

            def do_GET(self):
                self.forward()

            do_POST = do_PUT = do_PATCH = do_DELETE = do_HEAD = do_GET

            def forward(self):
                size = int(self.headers.get("Content-Length", "0"))
                if self.headers.get("Transfer-Encoding", "").lower() == "chunked":
                    chunks = []
                    while True:
                        size = int(self.rfile.readline().split(b";", 1)[0], 16)
                        if size == 0:
                            while self.rfile.readline() not in {b"\r\n", b"\n", b""}:
                                pass
                            break
                        segment = self.rfile.read(size)
                        if len(segment) != size or self.rfile.read(2) != b"\r\n":
                            raise ValueError("Incomplete test-proxy chunked request")
                        chunks.append(segment)
                    body = b"".join(chunks)
                else:
                    body = self.rfile.read(size)
                headers = {name: value for name, value in self.headers.items()
                           if name.lower() not in {"transfer-encoding", "content-length", "expect"}}
                headers["Content-Length"] = str(len(body))
                headers["Host"] = urllib.parse.urlsplit(product.endpoint).netloc
                headers["Origin"] = product.endpoint
                headers["Connection"] = "close"
                owner.requests.append((self.command, self.path, dict(self.headers), body))
                if owner.before:
                    owner.before(self.command, self.path, headers, body)
                if (owner.block_uploads and self.command == "PUT") or owner.blocked_path == (self.command, self.path):
                    if owner.after:
                        owner.after(self.command, self.path, headers, body, 503, b"", False)
                    self.send_response(503)
                    self.send_header("Content-Length", "0")
                    self.send_header("Connection", "close")
                    self.end_headers()
                    self.close_connection = True
                    return
                connection = http.client.HTTPConnection("127.0.0.1", product.address, timeout=120)
                try:
                    connection.request(self.command, self.path, body, headers)
                    upstream = connection.getresponse()
                    payload = upstream.read()
                    fields = upstream.getheaders()
                    if owner.after:
                        owner.after(self.command, self.path, headers, body, upstream.status, payload)
                    matched = owner.drop and owner.drop(self.command, self.path, upstream.status)
                    if matched and not owner.dropped:
                        owner.dropped = True
                        owner.block_uploads = owner.block_after_drop
                        if owner.block_after_drop:
                            owner.blocked_path = (self.command, self.path)
                        self.close_connection = True
                        self.connection.shutdown(socket.SHUT_RDWR)
                        return
                    self.send_response(upstream.status)
                    for name, value in fields:
                        if name.lower() not in {"connection", "transfer-encoding", "server", "date"}:
                            self.send_header(name, value)
                    self.send_header("Connection", "close")
                    self.end_headers()
                    if owner.interrupt_download and not owner.dropped and upstream.status == 200 and "/file?" in self.path:
                        owner.dropped = True
                        self.wfile.write(payload[:1 << 20])
                        self.wfile.flush()
                        self.connection.shutdown(socket.SHUT_RDWR)
                    elif self.command != "HEAD":
                        self.wfile.write(payload)
                except (OSError, http.client.HTTPException) as error:
                    owner.transport_errors.append((self.command, self.path.split("?", 1)[0], type(error).__name__))
                    self.send_error(502)
                finally:
                    connection.close()
                    self.close_connection = True

        class ForwardingServer(ThreadingHTTPServer):
            # The real shared App opens parallel reads. Python's default five
            # pending connects caused observed ECONNRESET at this test listener
            # on Darwin; use the platform listen queue, without retrying calls.
            request_queue_size = socket.SOMAXCONN

        self.server = ForwardingServer(("127.0.0.1", 0), Handler)
        self.server.daemon_threads = True
        self.endpoint = f"http://127.0.0.1:{self.server.server_port}"
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    def client(self, original):
        caller = self.product.root / ("proxy-caller-" + uuid.uuid4().hex)
        caller.mkdir(mode=0o700)
        credential = caller / "credential.json"
        value = json.loads(original["credential"].read_text())
        value["endpoint"] = self.endpoint
        write_json(credential, value)
        return PrivateClient(original | {"caller": caller, "credential": credential,
                           "flags": ["--profile", self.product.profile, "--client-dir", str(caller),
                                     "--credential", str(credential), "--json"]})

    def release(self):
        self.block_uploads = False
        self.blocked_path = None

    def __enter__(self):
        self.thread.start()
        return self

    def __exit__(self, exc_type, exception, traceback):
        if exception and self.transport_errors:
            exception.add_note(f"Test-proxy transport failures: {self.transport_errors[-5:]}")
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=5)
