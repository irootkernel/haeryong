"""RF02/03/05/08 public import, guarded read, cursor and CLI evidence; no SQLite access."""
import json
import time
import urllib.parse
import uuid

import pytest

pytestmark = pytest.mark.e2e


def reference_jobs(product, owner, version):
    return [row for row in product.data("GET", "/api/v1/jobs", client=owner)["items"]
            if row["kind"] == "derived_index"
            and row.get("result", {}).get("version_id") == version
            and row.get("result", {}).get("work_kind") == "references"]


def reference_ready(product, owner, version, timeout=60):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        rows = reference_jobs(product, owner, version)
        assert len(rows) == 1
        job = product.data("GET", "/api/v1/jobs/" + rows[0]["job_id"], client=owner)
        assert job["state"] not in {"failed", "cancelled"}, "reference maintenance failed"
        if job["state"] == "completed":
            result = job["result"]
            assert result["stage"] == "complete" and result["failed"] == "0"
            assert result["files_done"] == result["files_total"] == "1"
            assert result["bytes_done"] == result["bytes_total"]
            assert len(result["checkpoint"]) == 64
            return job
        time.sleep(0.05)
    pytest.fail("reference work did not complete within its test workload budget")


def test_references_RF02_RF06_public_import_restart_and_stable_derived_identity(product, owner, tmp_path):
    target = tmp_path / "target"
    target.mkdir()
    target_bytes = b"<p id='retry'>Immutable target</p>"
    (target / "target.html").write_bytes(target_bytes)
    _, _, published_target = product.import_tree(owner, target)
    reference_ready(product, owner, published_target["version_id"])
    source = tmp_path / "source"
    source.mkdir()
    source_bytes = "<p id='origin'><a data-haeryong-reference='ref/../참조/%2F'>Original source remains readable</a></p>".encode()
    (source / "source.html").write_bytes(source_bytes)
    metadata = tmp_path / "reference-metadata.json"
    value = {"references": [
        {"id": f"ref-{number:03}", "document_id": published_target["document_id"],
         "version_id": published_target["version_id"], "page_path": "target.html", "region_id": "retry",
         "from": {"page_path": "source.html", "region_id": "origin"}, "relation": "context"}
        for number in range(70)]}
    late_version = str(uuid.uuid4())
    value["references"].extend([
        dict(value["references"][0], id=f"000-late-{number}", version_id=late_version)
        for number in range(3)])
    value["references"].append(dict(value["references"][0], id="ref/../참조/%2F",
                                    page_query="mode=prior+%2F", page_fragment="artifact#part"))
    metadata.write_text(json.dumps(value))
    _, _, published = product.import_tree(owner, source, extra=("--metadata", str(metadata), "--new-document"))
    version = published["version_id"]
    original = reference_jobs(product, owner, version)
    assert len(original) == 1
    product.stop(kill=True)
    product.start()
    final = reference_ready(product, owner, version)
    assert final["job_id"] == original[0]["job_id"]
    product.stop()
    product.start()
    assert reference_ready(product, owner, version)["job_id"] == final["job_id"]
    assert reference_jobs(product, owner, version)[0]["job_id"] == final["job_id"]
    for identity, path, expected in ((published, "source.html", source_bytes),
                                     (published_target, "target.html", target_bytes)):
        status, body, _ = product.request("GET", f"/api/v1/versions/{identity['version_id']}/file?"
                                        + urllib.parse.urlencode({"path": path}), client=owner, raw=True)
        assert status == 200 and body == expected
    assert product.data("GET", "/api/v1/capabilities", client=owner)["features"]["references"] is True

    route = f"/api/v1/versions/{version}/references"
    first = product.data("GET", route + "?limit=1", client=owner)
    assert first["materialization"] == {"state": "ready", "reason": None}
    assert first["items"][0]["resolution"] == "unavailable"
    assert first["items"][0]["target"] is None and first["items"][0]["label"] is None
    assert first["next_cursor"]
    status, empty, headers = product.request("HEAD", route + "?limit=1", client=owner, raw=True)
    assert status == 200 and empty == b"" and int(headers["Content-Length"]) > 0
    cli_first = product.cli(["references", "list", "--version", version, "--limit", "1"], client=owner)["data"]
    assert cli_first == first
    late_tree = tmp_path / "late-target"
    late_tree.mkdir()
    (late_tree / "target.html").write_bytes(target_bytes + b"<p>Newer exact revision</p>")
    _, _, late = product.import_tree(owner, late_tree, extra=(
        "--document", published_target["document_id"], "--base-version", published_target["version_id"],
        "--version", late_version))
    reference_ready(product, owner, late["version_id"])
    again = product.data("GET", route + "?limit=1", client=owner)
    assert again["next_cursor"] == first["next_cursor"]
    assert again["items"][0]["resolution"] == "resolved"
    continued = product.data("GET", route + "?" + urllib.parse.urlencode(
        {"limit": "1", "cursor": first["next_cursor"]}), client=owner)
    assert continued["items"][0]["id"] == "000-late-1"
    seen, cursor = [], None
    while True:
        params = {"limit": "17"}
        if cursor:
            params["cursor"] = cursor
        page = product.data("GET", route + "?" + urllib.parse.urlencode(params), client=owner)
        seen.extend(page["items"])
        cursor = page["next_cursor"]
        if cursor is None:
            break
    assert [row["id"] for row in seen] == sorted(row["id"] for row in value["references"])
    assert len(seen) == 74 and all(row["resolution"] == "resolved" for row in seen)
    params = {"reference_id": "ref/../참조/%2F", "page_path": "source.html", "region_id": "origin"}
    resolved = product.data("GET", route + "/resolve?" + urllib.parse.urlencode(params), client=owner)
    assert resolved["target"]["version_id"] == published_target["version_id"]
    assert resolved["target"]["page_query"] == "mode=prior+%2F"
    assert resolved["target"]["page_fragment"] == "artifact#part"
    cli_row = product.cli(["references", "resolve", "--version", version, "--reference-id", params["reference_id"],
                           "--page", "source.html", "--region", "origin"], client=owner)["data"]
    assert cli_row == resolved
    assert reference_ready(product, owner, version) == final
    for suffix in ("?limit=0", "?limit=201", "?cursor=bad", "?unknown=x", "?limit=1&limit=2",
                   "/resolve", "/resolve?reference_id=x&region_id=r", "/resolve?reference_id="):
        status, error, _ = product.request("GET", route + suffix, client=owner)
        assert status == 400 and error["error"]["code"] == "INVALID_REQUEST"
        assert set(error["error"]["details"]) == {"pointer", "reason"}
        status, empty, _ = product.request("HEAD", route + suffix, client=owner, raw=True)
        assert status == 400 and empty == b""
    status, empty, _ = product.request("HEAD", route, raw=True)
    assert status == 401 and empty == b""
    status, error, _ = product.request("GET", route + "/resolve?reference_id=unknown", client=owner)
    assert status == 404 and error["error"]["details"] == {}
    product.cli(["references", "resolve", "--version", version, "--reference-id", "unknown"], client=owner, code=3)
