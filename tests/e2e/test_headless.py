"""Public-interface regressions migrated from the original CLI process tests."""
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import shutil
import socket
import signal
import subprocess
import threading
import time
import urllib.parse

import pytest

from .harness import PrivateClient, Product, Proxy, digest, operation, port, write_json

pytestmark = pytest.mark.e2e


def tree(root):
    source = root / "result-tree"
    source.mkdir(mode=0o700)
    (source / "empty" / "nested").mkdir(parents=True)
    bodies = {"first.html": b"<html>first<script>ordinary()</script></html>",
              ".hidden": b"hidden bytes", "literal\\u2028.txt": b"exact backslash",
              "한글.htm": b"<p>Unicode</p>"}
    for name, body in bodies.items():
        (source / name).write_bytes(body)
    (source / "link").symlink_to("first.html")
    return source, bodies


def test_daemon_serves_committed_shell_assets_without_dev_server(product):
    status, body, headers = product.request("GET", "/", raw=True)
    assert status == 200
    assert headers["Content-Type"] == "text/html; charset=utf-8"
    assert headers["Cache-Control"] == "no-store"
    csp = headers["Content-Security-Policy"]
    assert "script-src 'self'" in csp
    assert "style-src 'self'" in csp
    text = body.decode()
    assert 'id="root"' in text
    assert 'name="haeryong-session-defaults" content="remember-device=true"' in text
    for marker in ("@vite", "localhost:5173", "wails", "window.runtime"):
        assert marker not in text
    script = re.search(r'src="(/assets/[^"]+\.js)"', text).group(1)
    style = re.search(r'href="(/assets/[^"]+\.css)"', text).group(1)
    for asset, fragment in ((script, "javascript"), (style, "css")):
        status, body, headers = product.request("GET", asset, raw=True)
        assert status == 200, asset
        assert fragment in headers["Content-Type"]
        assert headers["Cache-Control"] == "no-store"
        assert b"wails" not in body
        assert b"NATIVE_TRANSPORT_NOT_IMPLEMENTED" not in body
    assert product.request("GET", "/assets/missing.js")[0] == 404
    assert product.request("GET", "/assets/../index.html")[0] == 404


def test_bootstrap_remembered_session_stop_restart_and_all_revoked_recovery(product):
    for path in ["/api/v1/session", "/api/v1/sessions", "/api/v1/operations/unknown",
                 "/api/v1/jobs", "/api/v1/events", "/api/v1/versions/unknown/file"]:
        assert product.request("GET", path)[0] == 401
    assert product.request("GET", "/local/v1/pairings")[0] == 401
    assert product.request("GET", "/local/v1/pairings", headers={"Origin": product.endpoint})[0] == 403
    owner = product.owner("original owner")
    stream = http.client.HTTPConnection("127.0.0.1", product.address, timeout=5)
    stream.request("GET", "/api/v1/events", headers={"Authorization": "Bearer " + owner["token"]})
    response = stream.getresponse()
    assert response.status == 200
    closed = threading.Event()

    def read_to_close():
        try:
            response.read()
        except (OSError, http.client.HTTPException):
            pass
        finally:
            closed.set()

    reader = threading.Thread(target=read_to_close, daemon=True)
    reader.start()
    try:
        product.stop()
        assert closed.wait(2), "active stream outlived service stop"
        product.start()
        remembered = product.data("GET", "/api/v1/session", client=owner)
        assert remembered["session_id"] == owner["session"]["session_id"]
        assert remembered["principal_id"] == owner["session"]["principal_id"]
        assert product.request("DELETE", "/api/v1/session", client=owner)[0] == 204
        assert product.request("GET", "/api/v1/session", client=owner)[0] == 401
        recovery = product.owner("recovery owner")
        assert recovery["session"]["principal_id"] == owner["session"]["principal_id"]
    finally:
        stream.close()
        reader.join(timeout=5)


def test_init_is_explicit_alias_excludes_writer_and_stop_releases_it(binaries, tmp_path):
    product = Product(binaries / "haeryong", tmp_path)
    try:
        product.cli(["daemon", "run"], code=5)
        assert not product.data_dir.exists()
        product.initialize()
        product.cli(["init"], code=5)
        product.start()
        alias = tmp_path / "alias"
        alias.symlink_to(product.data_dir, target_is_directory=True)
        result = subprocess.run([product.binary, "daemon", "run", "--profile", product.profile,
                                 "--data-dir", str(alias), "--port", str(port()), "--json"],
                                capture_output=True, timeout=10)
        assert result.returncode == 4
        product.stop()
        assert not (product.data_dir / "runtime" / "endpoint.json").exists()
        product.start()
        product.stop()
    finally:
        product.close()


def test_listener_collision_and_public_health_do_not_publish_private_metadata(binaries, tmp_path):
    product = Product(binaries / "haeryong", tmp_path)
    try:
        product.initialize()
        with socket.socket() as occupied:
            occupied.bind(("127.0.0.1", 0))
            occupied.listen()
            product.cli(["daemon", "run", "--port", str(occupied.getsockname()[1])], code=4)
        assert not (product.data_dir / "runtime" / "endpoint.json").exists()
        product.start()
        status, body, _ = product.request("GET", "/health/live", raw=True)
        assert status == 200 and body == b'{"live":true}\n'
        product.cli(["status"])
    finally:
        product.close()


def test_signal_shutdown_crash_recovery_and_missing_storage_never_create_substitute(product):
    for selected in (signal.SIGTERM, signal.SIGINT):
        product.process.send_signal(selected)
        assert product.process.wait(timeout=10) == 0
        product.reader.join(timeout=5)
        product.process.stdout.close()
        product.process = None
        assert not (product.data_dir / "runtime" / "endpoint.json").exists()
        product.start()
    product.stop(kill=True)
    product.cli(["status"], code=5)
    product.start()
    product.stop()
    hidden = product.data_dir.with_name("library-offline")
    product.data_dir.rename(hidden)
    product.cli(["daemon", "run"], code=5)
    product.cli(["status"], code=5)
    assert not product.data_dir.exists()
    if os.uname().sysname == "Darwin":
        absent = Path("/Volumes") / ("haeryong-unavailable-" + operation())
        result = subprocess.run([product.binary, "init", "--data-dir", str(absent / "library"), "--json"],
                                capture_output=True, timeout=10)
        assert result.returncode == 5 and not absent.exists()
    product.cli(["init", "--volume-root", str(product.root)], code=5)
    assert not product.data_dir.exists()


@pytest.mark.parametrize("args", [
    ["events", "--metadata", "unused"],
    ["references", "list", "--version", "11111111-1111-4111-8111-111111111111", "--operation-id", "unused"],
    ["references", "resolve", "--version", "11111111-1111-4111-8111-111111111111", "--reference-id", "r", "--region", "orphan"],
    ["files", "download", "--version", "fixture", "--output", "out", "--directory", "ignored"],
    ["auth", "sessions", "list", "--project", "ignored"],
    ["package", "validate", "input", "--base-version", "ignored"],
    ["config", "show", "--expected-config-revision", "7"],
    ["config", "plan", "input", "--scope", "library", "--project", "conflicting"],
])
def test_command_flags_cannot_silently_change_or_ignore_intent(binaries, tmp_path, args):
    Product(binaries / "haeryong", tmp_path).cli(args, code=2)


@pytest.mark.parametrize("value,valid,warning", [
    ("0", False, False), ("-1", False, False), ("random", False, False),
    ("1", False, False), ("1023", False, False), ("1024", True, True),
    ("8999", True, True), ("9000", True, False), ("9270", True, False),
    ("9999", True, False), ("10000", True, True), ("65535", True, True), ("65536", False, False),
])
def test_port_boundaries_and_warnings(binaries, value, valid, warning):
    result = subprocess.run([str(binaries / "haeryong"), "--json", "version", "--port", value],
                            text=True, capture_output=True, timeout=10)
    assert result.returncode == (0 if valid else 2)
    assert isinstance(json.loads(result.stdout), dict)
    assert ("outside preferred range 9000..9999" in result.stderr) is warning


@pytest.mark.parametrize("args,code", [
    (["service", "install"], 5), (["auth", "pair"], 5),
    (["init", "--endpoint", "https://example.invalid"], 2),
    (["auth", "grant"], 5),
])
def test_unsupported_capabilities_fail_honestly(binaries, tmp_path, args, code):
    Product(binaries / "haeryong", tmp_path).cli(args, code=code)


def test_import_query_retry_exact_files_and_restart(product, owner, tmp_path):
    source, bodies = tree(tmp_path)
    key, accepted, published = product.import_tree(owner, source)
    assert len({key, accepted["upload_create_operation_id"], accepted["upload_complete_operation_id"]}) == 3
    version = published["version_id"]
    repeated = product.cli(["import", str(source), "--operation-id", key], client=owner)["data"]
    assert repeated["result"]["job_id"] == accepted["result"]["job_id"]
    listing = product.files(owner, version, limit=1)
    actual = {entry["path"]: entry for entry in listing}
    assert set(actual) == set(bodies) | {"empty", "empty/nested", "link"}
    for name, body in bodies.items():
        assert actual[name]["kind"] == "file"
        status, data, _ = product.request("GET", f"/api/v1/versions/{version}/file?" + urllib.parse.urlencode({"path": name}), client=owner, raw=True)
        assert status == 200 and data == body
    destination = tmp_path / "first-copy.html"
    product.cli(["files", "get", "--version", version, "--path", "first.html", "--output", str(destination)], client=owner)
    assert destination.read_bytes() == bodies["first.html"]
    copied = tmp_path / "download"
    product.cli(["files", "download", "--version", version, "--output", str(copied)], client=owner)
    assert {name: (copied / name).read_bytes() for name in bodies} == bodies
    assert (copied / "empty" / "nested").is_dir()
    assert (copied / "link").is_symlink() and os.readlink(copied / "link") == "first.html"
    before = [(entry["path"], entry["kind"]) for entry in listing]
    product.stop(kill=True)
    product.start()
    assert [(entry["path"], entry["kind"]) for entry in product.files(owner, version, 1)] == before
    product.cli(["documents", "show", published["document_id"], "--version", version], client=owner)


def test_sessions_natural_revocation_and_custom_selected_credential(product, owner):
    second = product.owner("second device")
    listing = product.cli(["auth", "sessions", "list"], client=owner)["data"]["items"]
    assert {owner["session"]["session_id"], second["session"]["session_id"]} <= {row["session_id"] for row in listing}
    assert all(row["revision"] > 0 and row["etag"] for row in listing)
    product.cli(["auth", "sessions", "revoke", second["session"]["session_id"]], client=owner)
    product.cli(["auth", "sessions", "list"], client=second, code=3)
    product.cli(["auth", "sessions", "list"], client=owner)
    product.cli(["auth", "sessions", "revoke", second["session"]["session_id"], "--operation-id", operation()], client=owner, code=2)
    before = digest(owner["credential"].read_bytes())
    product.cli(["auth", "connect", "--endpoint", product.endpoint, "--no-input"], client=owner)
    assert digest(owner["credential"].read_bytes()) == before


def test_protected_cli_connect_requires_approval_and_reuses_its_selected_credential(product, tmp_path):
    caller = tmp_path / "normal-client"
    caller.mkdir(mode=0o700)
    flags = ["--profile", product.profile, "--client-dir", str(caller), "--endpoint", product.endpoint, "--json"]
    client = PrivateClient(flags=flags)
    pending = product.cli(["auth", "connect"], client=client)["data"]
    assert pending["connected"] is False and bool(pending["code"])
    product.cli(["auth", "approve", pending["pairing_id"]], stdin={
        "code": pending["code"], "origin": pending["origin"], "etag": pending["etag"], "operation_id": operation(),
    })
    connected = product.cli(["auth", "connect"], client=client)
    assert connected["data"]["connected"] is True
    assert "token" not in json.dumps(connected)
    credential = caller / "credential.json"
    before = digest(credential.read_bytes())
    product.cli(["auth", "connect", "--no-input"], client=client)
    assert digest(credential.read_bytes()) == before
    product.cli(["auth", "sessions", "list"], client=client)


@pytest.mark.parametrize("boundary", ["create", "complete", "metadata_create", "metadata_complete", "final"])
def test_lost_acknowledgement_recovers_original_dispatch_once(product, owner, tmp_path, boundary):
    source, _ = tree(tmp_path)
    key = operation()
    metadata = tmp_path / "metadata.json"
    metadata.write_text('{"document":{"title":"Captured original title"}}')
    matched = 0

    def drop(method, path, status):
        nonlocal matched
        phase = boundary.removeprefix("metadata_")
        matches = method == "POST" and status in {200, 201, 202} and {
            "create": path == "/api/v1/uploads", "complete": path.endswith("/complete"),
            "final": path == "/api/v1/imports",
        }[phase]
        if matches:
            matched += 1
        return matches and matched == (2 if boundary.startswith("metadata_") else 1)

    with Proxy(product, drop=drop, block_after_drop=True) as proxy:
        caller = proxy.client(owner)
        args = ["import", str(source), "--metadata", str(metadata), "--operation-id", key]
        product.cli(args, client=caller, code=5)
        assert proxy.dropped
        journal_file = caller["caller"] / "journals" / (key + ".json")
        before = json.loads(journal_file.read_text())
        source.rename(tmp_path / "author-input-moved")
        metadata.unlink()
        proxy.release()
        accepted = product.cli(args, client=caller)["data"]
        published = product.job(owner, accepted["result"]["job_id"])
        assert accepted["operation_id"] == key
        after = json.loads(journal_file.read_text())
        assert after["operation_id"] == before["operation_id"]
        for field in ("snapshot", "create_operation_id", "complete_operation_id"):
            assert after["result"][field] == before["result"][field]
            assert after["metadata"][field] == before["metadata"][field]
        version = product.data("GET", "/api/v1/versions/"+published["version_id"], client=owner)
        assert "Captured original title" in json.dumps(version)
        versions = product.data("GET", f"/api/v1/documents/{published['document_id']}/versions", client=owner)
        assert len(versions["items"]) == 1


def test_retained_manifest_prefix_cannot_publish_and_original_snapshot_recovers(product, owner, tmp_path):
    source, _ = tree(tmp_path)
    key = operation()
    args = ["import", str(source), "--operation-id", key]
    with Proxy(product, drop=lambda method, path, status: method == "POST" and path == "/api/v1/uploads" and status == 201, block_after_drop=True) as proxy:
        caller = proxy.client(owner)
        product.cli(args, client=caller, code=5)
        assert proxy.dropped
        proxy.release()
        journal_path = caller["caller"] / "journals" / (key+".json")
        before = json.loads(journal_path.read_text())
        manifest = caller["caller"] / "snapshots" / before["result"]["snapshot"]["id"] / "entries.jsonl"
        original = manifest.read_bytes()
        prefix = original[:original.index(b"\n")+1]
        assert len(prefix) < len(original)
        manifest.write_bytes(prefix)
        shutil.rmtree(source)
        product.cli(args, client=caller, code=4)
        failed = json.loads(journal_path.read_text())
        assert not failed.get("response") and failed["dispatch_phase"] != "final_dispatch"
        assert product.request("GET", "/api/v1/operations/"+key, client=owner)[0] == 404
        manifest.write_bytes(original)
        product.cli(args, client=caller)
        final = json.loads(journal_path.read_text())
        assert final["result"]["create_operation_id"] == before["result"]["create_operation_id"]
        assert final["result"]["complete_operation_id"] == before["result"]["complete_operation_id"]
        assert final["response"]


def test_pretransfer_base_survives_competing_publication_and_retry(product, owner, tmp_path):
    base = tmp_path / "base.html"
    base.write_text("<p>base</p>")
    _, _, first = product.import_tree(owner, base)
    document, version = first["document_id"], first["version_id"]
    a, b = tmp_path / "a.html", tmp_path / "b.html"
    a.write_text("<p>captured A</p>")
    b.write_text("<p>competing B</p>")
    arrived, release, finished = threading.Event(), threading.Event(), threading.Event()
    failures = []

    def pause(method, path, headers, body):
        if method == "PUT" and not arrived.is_set():
            arrived.set()
            assert release.wait(30), "competing publication barrier deadline"

    with Proxy(product, before=pause) as proxy:
        caller = proxy.client(owner)
        key = operation()
        args = ["import", str(a), "--document", document, "--operation-id", key]

        def publish_a():
            try:
                product.cli(args, client=caller, code=4, timeout=60)
            except BaseException as error:
                failures.append(error)
            finally:
                finished.set()

        worker = threading.Thread(target=publish_a)
        worker.start()
        try:
            assert arrived.wait(15), "A did not reach its captured pretransfer boundary"
            _, _, other = product.import_tree(owner, b, extra=["--document", document])
        finally:
            release.set()
            worker.join(timeout=60)
        assert finished.is_set() and not failures
        journal = json.loads((caller["caller"] / "journals" / (key+".json")).read_text())
        assert journal["base_version_id"] == version
        assert journal["final_request"]["base_version_id"] == version
        product.cli(args, client=caller, code=4)
        current = product.data("GET", "/api/v1/documents/"+document, client=owner)
        assert current["latest_version_id"] == other["version_id"]
        versions = product.data("GET", f"/api/v1/documents/{document}/versions", client=owner)
        assert len(versions["items"]) == 2


def test_small_archive_response_loss_restart_and_captured_resume(product, owner, tmp_path):
    import random
    import zipfile

    source = tmp_path / "small.zip"
    payload = random.Random(20261016).randbytes(3 << 20)
    with zipfile.ZipFile(source, "w", zipfile.ZIP_DEFLATED) as archive:
        archive.writestr("first.html", b"<p>small resumed archive</p>")
        archive.writestr("attachment.bin", payload)
    key = operation()
    with Proxy(product, drop=lambda method, path, status: method == "PUT" and status == 200,
               block_after_drop=True) as proxy:
        caller = proxy.client(owner)
        args = ["import", str(source), "--operation-id", key]
        product.cli(args, client=caller, code=5)
        assert proxy.dropped
        journal = json.loads((caller["caller"] / "journals" / (key+".json")).read_text())
        upload = journal["result"]["upload_id"]
        checkpoint = product.data("GET", f"/api/v1/uploads/{upload}", client=owner)["checkpoint"]
        product.stop()
        product.start()
        assert product.data("GET", f"/api/v1/uploads/{upload}", client=owner)["checkpoint"] == checkpoint
        source.unlink()
        proxy.release()
        accepted = product.cli(args, client=caller)["data"]
        assert accepted["upload_id"] == upload and accepted["operation_id"] == key
        published = product.job(owner, accepted["result"]["job_id"])
        status, data, _ = product.request("GET", f"/api/v1/versions/{published['version_id']}/file?path=attachment.bin", client=owner, raw=True)
        assert status == 200 and data == payload


def test_configuration_runtime_restart_and_protected_connect(product, owner, tmp_path):
    # Persist the test-selected listener through the public API, then remove the
    # ephemeral CLI override so a later committed listener change can restart it.
    initial = product.data("GET", "/api/v1/configuration", client=owner)
    selected_plan = product.data("POST", "/api/v1/configuration/plans", client=owner, body={
        "scope": {"kind": "library"}, "expected_config_revision": initial["config_revision"],
        "changes": {"network": {"listener": {"port": product.address}, "allowed_origins": [product.endpoint]}},
    })
    product.data("POST", "/api/v1/configuration/plans/"+selected_plan["plan_id"]+"/commit", client=owner,
                 body={"expected_config_revision": selected_plan["base_config_revision"], "confirmed": True})
    product.stop(kill=True)
    product.start(configured=True)
    policy = tmp_path / "security.yaml"
    policy.write_text("security:\n  remember_device_default: false\n  session_idle_seconds: 30\n  pairing_expiry_seconds: 120\n")
    plan = product.cli(["config", "plan", str(policy)], client=owner)["data"]["result"]
    product.cli(["config", "apply", plan["plan_id"], "--expected-config-revision", str(plan["base_config_revision"]), "--confirmed"], client=owner)
    status, html, _ = product.request("GET", "/", raw=True)
    assert status == 200 and b'type="checkbox" checked' not in html
    current = product.data("GET", "/api/v1/configuration", client=owner)
    old_address, old_endpoint = product.address, product.endpoint
    selected = port()
    while selected == old_address:
        selected = port()
    endpoint = f"http://127.0.0.1:{selected}"
    network = product.data("POST", "/api/v1/configuration/plans", client=owner, body={
        "scope": {"kind": "library"}, "expected_config_revision": current["config_revision"],
        "changes": {"network": {"listener": {"port": selected}, "allowed_origins": [endpoint]}},
    })
    try:
        product.request("POST", "/api/v1/configuration/plans/"+network["plan_id"]+"/commit", client=owner,
                        body={"expected_config_revision": network["base_config_revision"], "confirmed": True})
    except (OSError, http.client.HTTPException):
        pass  # Committed response loss is reconciled by the original public receipt.
    product.address, product.endpoint = selected, endpoint
    deadline = time.monotonic()+15
    while True:
        try:
            status, _, _ = product.request("GET", "/health/live", raw=True)
            if status == 200:
                break
        except (OSError, http.client.HTTPException):
            pass
        assert time.monotonic() < deadline, "configured listener did not become ready"
        time.sleep(0.05)
    actual = product.cli(["service", "status"])["data"]
    assert actual["library_id"] == product.identities[-1]["library_id"]
    assert actual["profile_id"] == product.identities[-1]["profile_id"]
    assert actual["instance_id"] != product.identities[-1]["instance_id"]
    with socket.socket() as probe:
        assert probe.connect_ex(("127.0.0.1", old_address)) != 0
    receipt = product.data("GET", "/api/v1/operations/"+network["plan_id"], client=owner)
    assert receipt["original_status"] == 200
    assert receipt["result_metadata"]["config_revision"] == network["base_config_revision"]+1
    assert product.request("GET", "/api/v1/session", client=owner)[0] == 200
    product.cli(["auth", "sessions", "list", "--endpoint", endpoint], client=owner, code=3)
    product.stop()
    product.address, product.endpoint = old_address, old_endpoint
    product.start()
    status = product.data("GET", "/api/v1/status", client=owner)
    assert status["listener"]["ephemeral_override"] is True


@pytest.mark.parametrize("explicit", [False, True])
def test_configuration_plan_keeps_original_file_and_revision_after_response_loss(product, owner, tmp_path, explicit):
    policy = tmp_path / "policy.yaml"
    policy.write_text("security:\n  remember_device_default: false\n")
    args = ["config", "plan", str(policy)]
    if explicit:
        args.extend(["--operation-id", operation()])
    with Proxy(product, drop=lambda method, path, status: method == "POST" and path == "/api/v1/configuration/plans" and status in {200, 201}, block_after_drop=True) as proxy:
        caller = proxy.client(owner)
        product.cli(args, client=caller, code=5)
        assert proxy.dropped
        journals = list((caller["caller"] / "journals").glob("*.json"))
        assert len(journals) == 1
        before = json.loads(journals[0].read_text())
        other = tmp_path / "other.yaml"
        other.write_text("security:\n  pairing_expiry_seconds: 121\n")
        competing = product.cli(["config", "plan", str(other)], client=owner)["data"]["result"]
        product.cli(["config", "apply", competing["plan_id"], "--expected-config-revision", str(competing["base_config_revision"]), "--confirmed"], client=owner)
        policy.unlink()
        proxy.release()
        recovered = product.cli(args, client=caller)["data"]
        after = json.loads(journals[0].read_text())
        assert after["final_request"] == before["final_request"]
        assert recovered["operation_id"] == journals[0].stem
        assert recovered["result"]["base_config_revision"] < competing["base_config_revision"] + 1


def test_configuration_natural_commit_replays_lost_acknowledgement(product, owner, tmp_path):
    policy = tmp_path / "policy.yaml"
    policy.write_text("security:\n  remember_device_default: false\n")
    plan = product.cli(["config", "plan", str(policy)], client=owner)["data"]["result"]
    args = ["config", "apply", plan["plan_id"], "--expected-config-revision", str(plan["base_config_revision"]), "--confirmed"]
    with Proxy(product, drop=lambda method, path, status: method == "POST" and path.endswith("/commit") and status == 200, block_after_drop=True) as proxy:
        caller = proxy.client(owner)
        product.cli(args, client=caller, code=5)
        assert proxy.dropped
        proxy.release()
        product.cli(args, client=caller)
        current = product.data("GET", "/api/v1/configuration", client=owner)
        assert current["config_revision"] == plan["base_config_revision"] + 1


def test_other_owner_discovers_and_resumes_arbitrary_confirmed_ranges(product, owner, tmp_path):
    recovery = product.owner("recovering owner")
    # The selected bootstrap API publishes the actual unfiled project identity.
    bootstrap = product.data("GET", "/api/v1/capabilities", client=owner)
    project = bootstrap["unfiled_project_id"]
    created = product.data("POST", "/api/v1/uploads", client=owner,
                           headers={"Idempotency-Key": operation()}, body={
                               "purpose": "result", "input_mode": "file", "selection_name": "original.html",
                               "target": {"project_id": project, "document_id": None},
                           })
    upload = created["upload_id"]
    body = b"exact arbitrary-range input\n" * 14000
    for begin, end in [(17, 1003), (5009, 17011), (len(body)-127, len(body))]:
        segment = body[begin:end]
        status, _, _ = product.request("PUT", f"/api/v1/uploads/{upload}/entries?path=_input&kind=file", client=owner, body=segment, headers={
            "Content-Type": "application/octet-stream", "Haeryong-Upload-Offset": str(begin),
            "Haeryong-Upload-Length": str(len(body)), "Haeryong-Upload-Chunk-SHA256": digest(segment),
        })
        assert status == 200
    assert product.request("DELETE", "/api/v1/sessions/"+owner["session"]["session_id"], client=owner)[0] == 204
    with Proxy(product) as proxy:
        caller = proxy.client(recovery)
        discovered = product.cli(["uploads", "list", "--unadopted"], client=caller)["data"]["items"]
        assert upload in {row["upload_id"] for row in discovered}
        product.cli(["uploads", "show", upload], client=caller)
        source = tmp_path / "renamed-local-file.html"
        changed = bytearray(body)
        changed[18] ^= 1
        source.write_bytes(changed)
        product.cli(["uploads", "resume", upload, "--input", str(source), "--operation-id", operation()], client=caller, code=4)
        assert not any(method == "PUT" for method, *_ in proxy.requests)
        source.write_bytes(body)
        completed = product.cli(["uploads", "resume", upload, "--input", str(source)], client=caller)["data"]
        journal = json.loads((caller["caller"] / "journals" / (completed["operation_id"]+".json")).read_text())
        assert journal["result"]["upload_id"] == upload
        assert journal["result"]["snapshot"]["selection_name"] == "original.html"
        assert journal["result"]["checkpoint"]["complete_input"] is True
        offsets = [int(headers["Haeryong-Upload-Offset"]) for method, _, headers, _ in proxy.requests if method == "PUT"]
        assert offsets == [0, 1003, 17011, len(body)-127]
