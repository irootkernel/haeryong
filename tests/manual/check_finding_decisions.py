"""One public-process Task05 composition over an explicitly prepared offline fixture.

The fixture is controlled proposal input, not semantic/provider evidence. Setup
copies it into the existing Product harness; every asserted decision, receipt,
restart and scan uses the actual service/CLI. No test reads or writes SQLite.
Main must supply the matching corrected source and fresh fixture before execution.
Explicit command: python3 -B -m pytest tests/manual/check_finding_decisions.py
This check_ module is outside canonical tests/e2e and default testpaths.
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
from urllib.parse import urlencode

import pytest

from tests.e2e.harness import Product, Proxy, operation, write_json

# Explicit selection only: reuse sibling E2E fixtures without changing defaults.
pytest_plugins = ["tests.e2e.conftest"]

pytestmark = pytest.mark.e2e


@pytest.fixture
def decision_case(request, tmp_path):
    # Missing required controlled inputs is a failure, never a skipped/passed gate.
    manifest_name = os.environ.get("HAERYONG_TASK05_DECISION_FIXTURE")
    expected_hash = os.environ.get("HAERYONG_TASK05_DECISION_FIXTURE_SHA256")
    assert manifest_name and expected_hash, "Main must provide a source-bound controlled decision fixture and its SHA-256"
    manifest_path = Path(manifest_name).resolve(strict=True)
    raw = manifest_path.read_bytes()
    assert hashlib.sha256(raw).hexdigest() == expected_hash
    fixture = json.loads(raw)
    assert fixture["provider_configured"] is False
    assert fixture["native_provider_calls"] == fixture["source_acquisition"] == 0
    assert fixture["quality_claim"] is False
    assert fixture["invariants"]["provider_attempts"] == 0
    for name in ("pending_approve", "pending_reject"):
        assert fixture[name]["status"] == "proposed"
        assert fixture[name]["kind"] == "topic_assignment"
        assert fixture[name]["version_id"] == fixture["version_id"]

    original = Path(fixture["profile_dir"])
    assert original.is_dir() and not original.is_symlink()
    assert original.resolve().is_relative_to(manifest_path.parent)
    assert not any(path.is_symlink() for path in original.rglob("*"))
    # The normal existing binaries fixture builds the production executable
    # from THIS checkout. No old externally prepared binary is silently selected.
    binaries = request.getfixturevalue("binaries")
    product = Product(binaries / "haeryong", tmp_path)

    def transient(directory, _names):
        return {"profile.json", "writer.lock", "runtime"} if Path(directory) == original else set()

    shutil.copytree(original, product.data_dir, ignore=transient)
    product.data_dir.chmod(0o700)
    (product.data_dir / "runtime").mkdir(mode=0o700)
    # The prebuilt fixture is relocated, not initialized over or re-seeded.
    # Rebind only this disposable copy's local profile path; preserve its IDs,
    # protected key and original profile name. Never log/copy its owner-token file.
    profile = json.loads((original / "profile.json").read_text())
    product.profile = profile["profile"]
    profile["data_dir"] = str(product.data_dir)
    write_json(product.data_dir / "profile.json", profile)
    del profile
    try:
        product.start()
        owner = product.owner("Task05 public process owner")
        yield product, owner, fixture
    finally:
        # Normal owned service stop is part of the test result. close() remains
        # the existing failure fallback, not a success-labelled natural stop.
        try:
            if product.process is not None:
                product.stop()
        finally:
            product.close()
        # The existing autouse cleanup_private_case removes the complete owned
        # copy and caller credentials after this teardown. Originals stay intact.


def _finding(product, owner, job_id, finding_id):
    cursor = None
    seen = set()
    while True:
        params = {"limit": 200}
        if cursor is not None:
            assert cursor not in seen
            seen.add(cursor)
            params["cursor"] = cursor
        page = product.data("GET", f"/api/v1/jobs/{job_id}/findings?" + urlencode(params), client=owner)
        for finding in page["items"]:
            if finding["finding_id"] == finding_id:
                assert finding["etag"] and finding["job_id"] == job_id
                return finding
        cursor = page["next_cursor"]
        assert cursor is not None, "Expected source-bound controlled finding was not publicly available"


def _topics(product, owner, version):
    items, cursor, seen = [], None, set()
    while True:
        params = {"version_id": version, "limit": 200}
        if cursor is not None:
            assert cursor not in seen
            seen.add(cursor)
            params["cursor"] = cursor
        page = product.data("GET", "/api/v1/topic-assignments?" + urlencode(params), client=owner)
        items.extend(page["items"])
        cursor = page["next_cursor"]
        if cursor is None:
            return items


def test_T00805_public_decisions_cli_journal_restart_and_exact_reassessment(decision_case):
    product, owner, fixture = decision_case
    job, version, document = fixture["job_id"], fixture["version_id"], fixture["document_id"]
    approval_id = fixture["pending_approve"]["finding_id"]
    rejection_id = fixture["pending_reject"]["finding_id"]
    approve = _finding(product, owner, job, approval_id)
    reject = _finding(product, owner, job, rejection_id)
    assert approve["status"] == reject["status"] == "proposed"
    assert approve["document_id"] == reject["document_id"] == document
    assert approve["version_id"] == reject["version_id"] == version
    before_references = product.data("GET", f"/api/v1/versions/{version}/references", client=owner)
    before_review = product.data("GET", f"/api/v1/versions/{version}/review", client=owner)
    status, original, _ = product.request("GET", f"/api/v1/versions/{version}/file?" + urlencode({"path": fixture["page_path"]}), client=owner, raw=True)
    assert status == 200

    route = f"/api/v1/findings/{approval_id}/decisions"
    command = ["findings", "approve", approval_id, "--if-match", approve["etag"]]
    # Drop only the real successful upstream application. The second CLI process
    # must use its saved journal after a NORMAL service process restart.
    with Proxy(product, drop=lambda method, path, status: method == "POST" and path == route and status == 200) as proxy:
        caller = proxy.client(owner)
        lost = product.cli(command, client=caller, code=5)["data"]
        lost_key = lost["operation_id"]
        assert lost_key and proxy.dropped
        pending = [json.loads(path.read_text()) for path in (caller["caller"] / "pending").glob("*.json")]
        assert any(item["operation_id"] == lost_key for item in pending)
        product.stop()
        product.start()
        recovered = product.cli(command, client=caller)["data"]
        assert recovered["operation_id"] == lost_key
        # Only scalar safe facts enter assertions; Proxy keeps authorization
        # headers in memory, and failed pytest introspection must not print them.
        post_count = sum(row[0] == "POST" and row[1] == route for row in proxy.requests)
        receipt_count = sum(row[0] == "GET" and row[1] == f"/api/v1/operations/{lost_key}" for row in proxy.requests)
        sent_condition = next(value for method, path, fields, _ in proxy.requests if method == "POST" and path == route
                              for name, value in fields.items() if name.lower() == "if-match")
        assert post_count == 1 and receipt_count >= 1
        assert sent_condition == approve["etag"]
        pending = [json.loads(path.read_text()) for path in (caller["caller"] / "pending").glob("*.json")]
        assert all(item["operation_id"] != lost_key for item in pending)

    applied = recovered["result"]
    assert applied["state"] == "completed" and applied["finding"]["status"] == "applied"
    effect = applied["effect"]
    assert effect["origin"] == "ai" and effect["kind"] == "topic_assignment"
    effect_id = effect["overlay_id"]
    matching = [row for row in _topics(product, owner, version) if row.get("topic") == approve["proposal"]["topic"]]
    assert len(matching) == 1 and matching[0]["overlay_id"] == effect_id

    reject = _finding(product, owner, job, rejection_id)
    assert reject["status"] == "proposed"
    rejected = product.cli(["findings", "reject", rejection_id, "--if-match", reject["etag"]], client=owner)["data"]["result"]
    assert rejected["finding"]["status"] == "rejected"
    pinned = product.cli(["topics", "set", effect_id, "--pinned", "true", "--decision", "accepted", "--if-match", effect["etag"]], client=owner)["data"]["result"]["effect"]
    assert pinned["pinned"] is True and pinned["revision"] > effect["revision"]
    status, refused, _ = product.request("PUT", f"/api/v1/topic-assignments/{effect_id}", client=owner,
                                         body={"pinned": False, "decision": "accepted"},
                                         headers={"Idempotency-Key": operation(), "If-Match": effect["etag"]})
    assert status == 412 and refused["error"]["code"] == "PRECONDITION_FAILED"
    current = next(row for row in _topics(product, owner, version) if row["overlay_id"] == effect_id)
    assert current == pinned
    unpinned = product.cli(["topics", "set", effect_id, "--pinned", "false", "--decision", "accepted", "--if-match", current["etag"]], client=owner)["data"]["result"]["effect"]
    undone = product.cli(["overlays", "undo", effect_id, "--if-match", unpinned["etag"]], client=owner)["data"]["result"]
    assert undone["effect"]["decision"] == "undone"
    terminal = [row for row in _topics(product, owner, version) if row["overlay_id"] == effect_id]
    assert len(terminal) == 1 and terminal[0]["decision"] == "undone"
    assert terminal[0]["decision"] != "accepted" and terminal[0] == undone["effect"]

    product.stop()
    product.start()
    saved = _finding(product, owner, job, approval_id)
    assert saved["status"] == "undone" and saved["effect"]["decision"] == "undone"
    assert saved["owner_decision"]["action"] == "undo"
    assert _finding(product, owner, job, rejection_id)["status"] == "rejected"
    # The old successful receipt is history. Its explicit recovery cannot apply
    # a second effect or reverse the later owner Undo.
    replay = product.cli([*command, "--operation-id", lost_key], client=owner)["data"]
    assert replay["operation_id"] == lost_key
    assert _finding(product, owner, job, approval_id)["status"] == "undone"
    after_replay = [row for row in _topics(product, owner, version) if row["overlay_id"] == effect_id]
    assert after_replay == terminal and after_replay[0]["decision"] == "undone"
    assert after_replay[0]["decision"] != "accepted"

    request = {"scope": "selected_projects", "project_ids": [saved["project_id"]], "document_id": document, "version_id": version,
               "page_path": fixture["page_path"], "depth": "change_focused", "source_mode": "pinned",
               "finding_id": approval_id, "expected_finding_revision": saved["revision"]}
    status, _, _ = product.request("POST", "/api/v1/scans", body=request, headers={"Idempotency-Key": operation()})
    assert status == 401  # Authentication refusal only; no derived-workflow 403 claim.
    stale = request | {"expected_finding_revision": approve["revision"]}
    status, _, _ = product.request("POST", "/api/v1/scans", client=owner, body=stale, headers={"Idempotency-Key": operation()})
    assert status == 412
    accepted = product.cli(["scan", "--project", saved["project_id"], "--document", document, "--version", version,
                            "--path", fixture["page_path"], "--depth", "change-focused", "--source-mode", "pinned",
                            "--finding", approval_id, "--expected-finding-revision", str(saved["revision"])], client=owner)["data"]["result"]
    report = product.job(owner, accepted["job_id"])
    capture = report["requested_scope"]
    for name in ("finding_id", "expected_finding_revision", "document_id", "version_id", "page_path", "depth", "source_mode"):
        assert capture[name] == request[name]
    # The existing capture mirrors a validated exact page into path_prefix.
    # Worker matching is path==prefix or prefix+"/"; a regular page cannot
    # have descendant entries. Never accept a broader directory/prefix.
    assert capture.get("path_prefix") in ("", None, request["page_path"])
    assert not capture.get("region_id")
    assert report["provider_attempts"] == 0
    assert _finding(product, owner, job, approval_id)["status"] == "undone"
    assert product.data("GET", f"/api/v1/versions/{version}/references", client=owner) == before_references
    assert product.data("GET", f"/api/v1/versions/{version}/review", client=owner) == before_review
    status, unchanged, _ = product.request("GET", f"/api/v1/versions/{version}/file?" + urlencode({"path": fixture["page_path"]}), client=owner, raw=True)
    assert status == 200 and unchanged == original
