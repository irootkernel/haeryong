"""Black-box E2E scan inventory, deterministic structural checks, CLI lost-response recovery, and managed capture binding."""
import importlib.util
import json
from pathlib import Path
import time
import uuid

import pytest

from .harness import Proxy, operation

pytestmark = pytest.mark.e2e


def _load_contract_validator():
    spec = importlib.util.spec_from_file_location(
        "haeryong_contract", Path(__file__).resolve().parents[1] / "contracts" / "contract.py"
    )
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


CONTRACT = _load_contract_validator()


def test_scans_T00801_public_api_cli_lost_response_and_findings_flow(product, owner, tmp_path):
    pkg_dir = tmp_path / "pkg-v1"
    pkg_dir.mkdir()
    (pkg_dir / "index.html").write_text(
        '<!doctype html><html><body><section id="intro"><a href="missing.html">Broken local link</a></section></body></html>',
        encoding="utf-8",
    )
    sub_dir = pkg_dir / "sub"
    sub_dir.mkdir()
    (sub_dir / "raw.bin").write_bytes(b"\x00\x01\xffunpreviewable")
    (sub_dir / "alias.bin").symlink_to("../outside-dangling-target.bin")
    _, _, imported = product.import_tree(owner, pkg_dir)
    version_id = imported["version_id"]
    document_id = imported["document_id"]

    files_list = {f["path"]: f for f in product.files(owner, version_id)}
    assert set(files_list) == {"index.html", "sub", "sub/alias.bin", "sub/raw.bin"}
    assert files_list["sub"]["kind"] == "directory"
    assert files_list["sub/alias.bin"]["kind"] == "symlink"
    assert files_list["sub/alias.bin"]["link_target"] == "../outside-dangling-target.bin"
    assert files_list["sub/raw.bin"]["kind"] == "file"

    # 1. Premature finding reassessment returns 503 FEATURE_NOT_READY
    for body in (
        {"finding_id": str(uuid.uuid4())},
        {"expected_finding_revision": 1},
    ):
        status, err_payload, _ = product.request(
            "POST",
            "/api/v1/scans",
            client=owner,
            body=body,
            headers={"Idempotency-Key": operation()},
        )
        assert status == 503
        assert err_payload["error"]["code"] == "FEATURE_NOT_READY"

    # 2. CLI scan submission with dropped 202 acknowledgement recovers pending operation_id via GET /api/v1/operations/{id}
    with Proxy(
        product,
        drop=lambda method, path, status: method == "POST" and path == "/api/v1/scans" and status == 202,
    ) as proxy:
        proxy_owner = proxy.client(owner)
        scan_cmd = [
            "scan",
            "--document",
            document_id,
            "--version",
            version_id,
            "--depth",
            "change-focused",
            "--source-mode",
            "pinned",
        ]
        lost_env = product.cli(scan_cmd, client=proxy_owner, code=5)
        lost_op = lost_env["data"]["operation_id"]
        assert lost_op

        retry_out = product.cli(scan_cmd, client=proxy_owner, code=0)["data"]
        assert retry_out["operation_id"] == lost_op
        post_scans = [r for r in proxy.requests if r[0] == "POST" and r[1] == "/api/v1/scans"]
        get_ops = [r for r in proxy.requests if r[0] == "GET" and r[1] == f"/api/v1/operations/{lost_op}"]
        assert len(post_scans) == 1
        assert len(get_ops) >= 1

    job_id = retry_out["result"]["job_id"]
    assert retry_out["result"]["job_url"] == f"/api/v1/jobs/{job_id}"
    assert retry_out["result"]["coalesced"] is False
    assert isinstance(retry_out["result"]["target_set_id"], str) and retry_out["result"]["target_set_id"]

    # Idempotent replay with explicit --operation-id returns the same job_id
    replay_out = product.cli([*scan_cmd, "--operation-id", lost_op], client=owner)["data"]
    assert replay_out["result"]["job_id"] == job_id

    report = product.job(owner, job_id)
    CONTRACT.validate("scan_report", report)
    assert report["stage"] == "complete"
    assert report["enumeration_complete"] is True
    assert report["captured"]["versions"] == 1
    assert report["captured"]["pages"] == 1
    assert report["captured"]["regions"] == 0
    assert report["captured"]["entries"] == 4
    assert report["captured"]["sources"] == 0
    assert report["captured"]["indexes"] == 1
    assert report["captured"]["total_units"] == 8
    assert report["structural"]["checked"] == 7
    assert report["structural"]["skipped"] == 1
    assert report["structural"]["failed"] == 0
    assert report["structural"]["blocked"] == 0
    assert report["structural"]["regionless_versions"] == 1
    assert report["structural"]["anomalies_reported"] == 0
    assert report["findings"]["total"] == 0

    # 3. HEAD/GET /api/v1/jobs/{id}/findings and conditional 304 Not Modified
    head_status, head_body, head_headers = product.request(
        "HEAD", f"/api/v1/jobs/{job_id}/findings", client=owner, raw=True
    )
    assert head_status == 200
    assert head_body == b""
    etag = next((v for k, v in head_headers.items() if k.lower() == "etag"), None)
    assert etag and etag.startswith('"sha256-')

    not_mod_status, _, _ = product.request(
        "GET",
        f"/api/v1/jobs/{job_id}/findings",
        client=owner,
        headers={"If-None-Match": etag},
        raw=True,
    )
    assert not_mod_status == 304

    # 4. CLI findings list with canonical filters and premature approve/reject refusal
    findings_page = product.cli(
        ["findings", "list", "--job", job_id, "--kind", "backlink_index_repair", "--status", "proposed"],
        client=owner,
    )["data"]
    assert findings_page["items"] == []
    assert findings_page["next_cursor"] is None
    assert str(tmp_path) not in json.dumps(findings_page)

    dummy_id = str(uuid.uuid4())
    product.cli(["findings", "approve", dummy_id, "--if-match", f'"finding-{dummy_id}-r1"'], client=owner, code=5)
    product.cli(["findings", "reject", dummy_id, "--if-match", f'"finding-{dummy_id}-r1"'], client=owner, code=5)

    # 5. Moving the single targeted document to Trash hides the single-target scan job, findings, and receipt until Restore
    doc_rev = product.data("GET", f"/api/v1/documents/{document_id}", client=owner)["revision"]
    trash_plan = product.data(
        "POST",
        f"/api/v1/documents/{document_id}/lifecycle-plans",
        client=owner,
        body={"operation": "trash", "expected_revision": doc_rev},
    )
    product.data(
        "POST",
        f"/api/v1/lifecycle-plans/{trash_plan['plan_id']}/commit",
        client=owner,
        headers={"Idempotency-Key": operation()},
        body={
            "plan_id": trash_plan["plan_id"],
            "expected_revision": trash_plan["expected_revision"],
            "confirmed": True,
        },
    )
    assert product.request("GET", f"/api/v1/jobs/{job_id}", client=owner)[0] == 404
    assert product.request("HEAD", f"/api/v1/jobs/{job_id}", client=owner, raw=True)[0] == 404
    assert product.request("GET", f"/api/v1/jobs/{job_id}/findings", client=owner)[0] == 404
    assert product.request("GET", f"/api/v1/operations/{lost_op}", client=owner)[0] == 404
    assert all(j["job_id"] != job_id for j in product.data("GET", "/api/v1/jobs", client=owner)["items"])

    trash_items = product.data("GET", "/api/v1/trash", client=owner)["items"]
    trash_row = next(r for r in trash_items if r["document_id"] == document_id)
    restore_plan = product.data(
        "POST",
        f"/api/v1/documents/{document_id}/lifecycle-plans",
        client=owner,
        body={"operation": "restore", "expected_revision": trash_row["revision"]},
    )
    product.data(
        "POST",
        f"/api/v1/lifecycle-plans/{restore_plan['plan_id']}/commit",
        client=owner,
        headers={"Idempotency-Key": operation()},
        body={
            "plan_id": restore_plan["plan_id"],
            "expected_revision": restore_plan["expected_revision"],
            "confirmed": True,
        },
    )
    assert product.data("GET", f"/api/v1/jobs/{job_id}", client=owner)["job_id"] == job_id
    assert product.data("GET", f"/api/v1/jobs/{job_id}/findings", client=owner)["items"] == []
    assert product.data("GET", f"/api/v1/operations/{lost_op}", client=owner)["operation_id"] == lost_op


def test_scans_T00801_managed_files_captures_and_crash_restart_recovery(product, owner, tmp_path):
    project = product.data("GET", "/api/v1/capabilities", client=owner)["unfiled_project_id"]
    source_id = str(uuid.uuid4())
    src_root = tmp_path / "managed-src"
    src_root.mkdir()
    alpha_bytes = b"alpha v1 content\n"
    beta_bytes_v1 = b"beta v1 content\n"
    beta_bytes_v2 = b"beta v2 mutated content\n"
    (src_root / "alpha.txt").write_bytes(alpha_bytes)
    (src_root / "beta.txt").write_bytes(beta_bytes_v1)

    product.cli(
        [
            "sources",
            "approve",
            "--project",
            project,
            "--source-id",
            source_id,
            "--kind",
            "files",
            "--root-path",
            str(src_root),
            "--retention-mode",
            "selected_snapshot",
            "--confirmed",
        ],
        client=owner,
    )

    pkg_dir = tmp_path / "pkg-src"
    pkg_dir.mkdir()
    (pkg_dir / "index.html").write_text("<html><body>Source-backed page</body></html>", encoding="utf-8")
    _, _, imported = product.import_tree(owner, pkg_dir, extra=["--project", project])
    version_id = imported["version_id"]

    # 1. Implicit current mode on files source without target_set_id -> source_capture_required
    implicit_job = product.cli(
        ["scan", "--version", version_id, "--source-mode", "current", "--depth", "full-scope"],
        client=owner,
    )["data"]["result"]["job_id"]
    implicit_report = product.job(owner, implicit_job)
    assert implicit_report["enumeration_complete"] is True
    assert implicit_report["structural"]["blocked"] >= 1
    assert any(t.get("reason") == "source_capture_required" for t in implicit_report.get("source_targets", []))

    # 2. Capture A (alpha.txt + beta.txt), then mutate beta.txt, then Capture B -> distinct scans
    cap_req = {
        "source_mode": "current",
        "selections": [
            {
                "source_id": source_id,
                "paths": [{"path": "alpha.txt"}, {"path": "beta.txt"}],
            }
        ],
    }
    cap_file = tmp_path / "capture-req.json"
    cap_file.write_text(json.dumps(cap_req), encoding="utf-8")
    cap_a = product.cli(
        ["sources", "capture", "--project", project, "--input", str(cap_file)], client=owner
    )["data"]["result"]
    assert cap_a["complete"] is True and cap_a["target_set_id"]

    (src_root / "beta.txt").write_bytes(beta_bytes_v2)
    cap_b = product.cli(
        ["sources", "capture", "--project", project, "--input", str(cap_file)], client=owner
    )["data"]["result"]
    assert cap_b["complete"] is True and cap_b["target_set_id"]
    assert cap_b["target_set_id"] != cap_a["target_set_id"]

    job_a = product.cli(
        [
            "scan",
            "--version",
            version_id,
            "--source-mode",
            "current",
            "--capture",
            cap_a["target_set_id"],
        ],
        client=owner,
    )["data"]["result"]["job_id"]
    job_b = product.cli(
        [
            "scan",
            "--version",
            version_id,
            "--source-mode",
            "current",
            "--capture",
            cap_b["target_set_id"],
        ],
        client=owner,
    )["data"]["result"]["job_id"]
    assert job_a != job_b
    rep_a = product.job(owner, job_a)
    rep_b = product.job(owner, job_b)
    assert rep_a["target_set_id"] == cap_a["target_set_id"]
    assert rep_b["target_set_id"] == cap_b["target_set_id"]
    assert rep_a["structural"]["blocked"] == 0
    assert rep_b["structural"]["blocked"] == 0
    assert rep_a["source_targets"][0]["snapshot_sha256"] != rep_b["source_targets"][0]["snapshot_sha256"]

    # 3. Real mid-scan daemon crash (SIGKILL while scan has positive durable structural progress below total) and restart recovery
    many_dir = tmp_path / "pkg-mid-scan-restart"
    many_dir.mkdir()
    page_count = 40
    for idx in range(page_count):
        (many_dir / f"page-{idx:03d}.html").write_text(
            f'<!doctype html><html><body><section id="s-{idx}">Page {idx}</section></body></html>',
            encoding="utf-8",
        )
    _, _, mid_imported = product.import_tree(owner, many_dir, extra=["--project", project])
    mid_version_id = mid_imported["version_id"]
    mid_doc_id = mid_imported["document_id"]

    mid_status, mid_accepted, _ = product.request(
        "POST",
        "/api/v1/scans",
        client=owner,
        headers={"Idempotency-Key": operation()},
        body={
            "document_ids": [mid_doc_id],
            "version_id": mid_version_id,
            "depth": "full_scope",
        },
    )
    assert mid_status == 202, f"Expected 202 Accepted for mid-scan job, got {mid_status}: {mid_accepted}"
    mid_job_id = mid_accepted["data"]["job_id"]

    # 1 version + 40 pages + 40 entries + 1 source placeholder (no_source_declared) + 1 index = 83 units (> 64 batch limit)
    expected_total_units = 1 + page_count + page_count + 1 + 1
    expected_checked_units = expected_total_units - 1

    mid_before = None
    deadline = time.monotonic() + 10.0
    while time.monotonic() < deadline:
        probe = product.data("GET", f"/api/v1/jobs/{mid_job_id}", client=owner)
        checked_now = probe.get("result", {}).get("structural", {}).get("checked", 0)
        if probe["state"] == "running" and 0 < checked_now < expected_checked_units:
            mid_before = probe
            break
        if probe["state"] not in {"queued", "running"}:
            mid_before = probe
            break

    assert mid_before is not None, "Timed out waiting for mid-scan running progress"
    CONTRACT.validate("scan_report", mid_before["result"])
    assert mid_before["result"]["stage"] in {"inventory", "inspect", "repair"}
    checked_before = mid_before["result"]["structural"]["checked"]
    assert mid_before["state"] == "running", f"Expected running mid-scan state before kill, got {mid_before}"
    assert 0 < checked_before < expected_checked_units, (
        f"Expected positive mid-scan progress below {expected_checked_units}, got checked={checked_before}"
    )
    denom_before = mid_before["result"]["captured"]
    scope_before = mid_before["scope"]
    assert denom_before["total_units"] == expected_total_units

    product.stop(kill=True)
    product.start()

    mid_after = None
    resume_deadline = time.monotonic() + 30.0
    while time.monotonic() < resume_deadline:
        probe = product.data("GET", f"/api/v1/jobs/{mid_job_id}", client=owner)
        if probe["state"] in {"completed", "failed", "cancelled"}:
            mid_after = probe
            break
        time.sleep(0.05)
    assert mid_after is not None, "Timed out waiting for resumed mid-scan job to complete"
    assert mid_after["state"] == "completed", f"Resumed scan ended in {mid_after['state']}: {mid_after}"
    assert mid_after["attempt_generation"] > mid_before["attempt_generation"]
    mid_report = mid_after["result"]
    CONTRACT.validate("scan_report", mid_report)
    assert mid_after["scope"] == scope_before
    assert mid_report["stage"] == "complete"
    assert mid_report["enumeration_complete"] is True
    assert mid_report["captured"] == denom_before
    assert mid_report["captured"]["versions"] == 1
    assert mid_report["captured"]["pages"] == page_count
    assert mid_report["structural"]["checked"] == expected_checked_units
    assert mid_report["structural"]["checked"] > checked_before
    assert mid_report["structural"]["skipped"] == 1
    assert mid_report["structural"]["failed"] == 0
    assert mid_report["structural"]["blocked"] == 0
    assert (
        mid_report["structural"]["checked"]
        + mid_report["structural"]["skipped"]
        + mid_report["structural"]["failed"]
        + mid_report["structural"]["blocked"]
    ) == expected_total_units
    assert mid_report["semantic"]["skipped"] == 1

    assert product.data("GET", f"/api/v1/jobs/{job_a}", client=owner)["state"] == "completed"
    assert product.data("GET", f"/api/v1/jobs/{job_b}", client=owner)["state"] == "completed"
    assert isinstance(
        product.data("GET", f"/api/v1/jobs/{job_a}/findings", client=owner)["items"], list
    )
