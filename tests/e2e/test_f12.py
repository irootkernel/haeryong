"""The unchanged canonical F12 bytes through production public interfaces."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import time
import urllib.parse
import zipfile

import pytest

from .harness import Proxy, digest, operation, write_json
from .test_search import F12SearchProbe

pytestmark = pytest.mark.e2e


def hash_file(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def assert_download(root, expected):
    actual = {str(path.relative_to(root)): path for path in root.rglob("*") if path.is_file()}
    assert set(actual) == set(expected)
    for name, identity in expected.items():
        assert actual[name].stat().st_size == identity["byte_size"]
        assert hash_file(actual[name]) == identity["sha256"]
    assert (root / "represented-empty").is_dir()


def test_canonical_f12_response_loss_resume_cursor_range_download_and_restart(product, owner, canonical_f12, tmp_path, request, record_testsuite_property):
    started = time.monotonic()
    deadline = started + 1800
    source = tmp_path / "large-result.zip"
    shutil.copyfile(canonical_f12["archive"], source)
    archive_hash = hash_file(source)
    expected = canonical_f12["files"]
    key = operation()

    def remaining():
        value = deadline - time.monotonic()
        assert value > 0, "canonical F12 exceeded its 1800-second hang guard"
        return value

    def drop(method, path, status):
        headers = proxy.requests[-1][2]
        return method == "PUT" and status == 200 and int(headers.get("Haeryong-Upload-Offset", "-1")) == 2 << 20

    with Proxy(product, drop=drop, block_after_drop=True) as proxy:
        caller = proxy.client(owner)
        args = ["import", str(source), "--operation-id", key]
        product.cli(args, client=caller, code=5, timeout=remaining())
        assert proxy.dropped
        journal_file = caller["caller"] / "journals" / (key+".json")
        journal = json.loads(journal_file.read_text())
        upload = journal["result"]["upload_id"]
        assert journal["result"]["checkpoint"]["accepted_bytes"] == str(2 << 20)
        detail = product.data("GET", f"/api/v1/uploads/{upload}", client=owner)
        assert detail["state"] == "open"
        assert detail["checkpoint"]["accepted_bytes"] == str(3 << 20)
        another = product.owner("F12 discovery without originating caller state")
        discovered = product.cli(["uploads", "list"], client=another)["data"]["items"]
        assert upload in {item["upload_id"] for item in discovered}
        product.stop()
        product.start()
        reopened = product.data("GET", f"/api/v1/uploads/{upload}", client=owner)
        assert reopened["checkpoint"] == detail["checkpoint"]
        source.unlink()
        proxy.release()
        accepted = product.cli(args, client=caller, timeout=remaining())["data"]
        assert accepted["operation_id"] == key and accepted["upload_id"] == upload
        assert len({key, accepted["upload_create_operation_id"], accepted["upload_complete_operation_id"]}) == 3
        published = product.job(owner, accepted["result"]["job_id"], timeout=remaining())
        version = published["version_id"]

        entries = product.files(owner, version, 37)
        listed = product.cli(["files", "list", "--version", version, "--limit", "37"], client=caller, timeout=remaining(), ndjson=True)
        assert [row["data"] for row in listed] == entries
        actual_files = {row["path"]: row for row in entries if row["kind"] == "file"}
        assert len(entries) == 1031 and set(actual_files) == set(expected)
        for name, row in actual_files.items():
            assert row["byte_size"] == str(expected[name]["byte_size"])
            assert row["bytes_sha256"] == expected[name]["sha256"]
        status, first, fields = product.request("GET", f"/api/v1/versions/{version}/file?path=first.html", client=owner, raw=True)
        assert status == 200 and len(first) == 69
        assert digest(first) == expected["first.html"]["sha256"]
        etag = next(value for name, value in fields.items() if name.lower() == "etag")
        status, part, _ = product.request("GET", f"/api/v1/versions/{version}/file?path=first.html", client=owner,
                                          raw=True, headers={"Range": "bytes=16-47", "If-Range": etag})
        assert status == 206 and part == first[16:48]
        large = expected["deep/nested/large.html"]
        reassembled = hashlib.sha256()
        ranged_bytes = 0
        for start, end in [(0, (1 << 20)-1), (1 << 20, large["byte_size"]-(1 << 20)-1),
                           (large["byte_size"]-(1 << 20), large["byte_size"]-1)]:
            status, part, _ = product.request("GET", f"/api/v1/versions/{version}/file?path=deep/nested/large.html",
                                              client=owner, raw=True, headers={"Range": f"bytes={start}-{end}"})
            assert status == 206 and len(part) == end-start+1
            ranged_bytes += len(part)
            reassembled.update(part)
        assert ranged_bytes == large["byte_size"] and reassembled.hexdigest() == large["sha256"]
        remaining()
        proxy.interrupt_download = True
        proxy.dropped = False
        downloaded = tmp_path / "large-copy.html"
        get_args = ["files", "get", "--version", version, "--path", "deep/nested/large.html", "--output", str(downloaded)]
        product.cli(get_args, client=caller, code=5, timeout=remaining())
        assert proxy.dropped
        resumed = product.cli(get_args, client=caller, timeout=remaining())["data"]
        assert resumed["resumed"] is True
        assert hash_file(downloaded) == expected["deep/nested/large.html"]["sha256"]
        assert any(method == "GET" and headers.get("Range") == "bytes=1048576-" and headers.get("If-Range")
                   for method, _, headers, _ in proxy.requests)
        before = tmp_path / "download-before"
        product.cli(["files", "download", "--version", version, "--output", str(before)], client=caller, timeout=remaining())
        assert_download(before, expected)
        remaining()
        product.stop()
        product.start()
        search_probe = F12SearchProbe(product, owner, canonical_f12, version)
        request.addfinalizer(search_probe.close)
        assert product.files(owner, version, 37) == entries
        listed = product.cli(["files", "list", "--version", version, "--limit", "37"], client=caller, timeout=remaining(), ndjson=True)
        assert [row["data"] for row in listed] == entries
        after = tmp_path / "download-after"
        product.cli(["files", "download", "--version", version, "--output", str(after)], client=caller, timeout=remaining())
        assert_download(after, expected)
        remaining()
        transfer_elapsed = time.monotonic() - started
        # The original transfer must finish within its 1800-second hang guard. Index
        # observation has its own guard and does not extend that transfer deadline.
        record_testsuite_property("SC20", json.dumps(search_probe.finish(1200)))
    report = {"fixture": "Canonical F12 through actual CLI/daemon/public API", "archive_bytes": canonical_f12["archive_bytes"],
              "archive_sha256": archive_hash, "expanded_bytes": canonical_f12["expanded_bytes"],
              "files": expected, "represented_entries": len(entries), "elapsed_seconds": transfer_elapsed,
              "complete_resume_cursor_range_download_restart": True}
    if path := os.environ.get("HAERYONG_TASK005_WORKLOAD_REPORT"):
        write_json(path, report)


def test_canonical_f12_streamed_http_import_checkpoint_and_restart(product, owner, canonical_f12, tmp_path):
    started = time.monotonic()
    source = Path(canonical_f12["archive"])
    library = product.data("GET", "/api/v1/capabilities", client=owner)
    upload = product.data("POST", "/api/v1/uploads", client=owner,
                          headers={"Idempotency-Key": operation()}, body={
                              "purpose": "result", "input_mode": "archive", "selection_name": "large-result.zip",
                              "target": {"project_id": library["unfiled_project_id"], "document_id": None},
                          })["upload_id"]
    offset, checkpoint = 0, None
    with source.open("rb") as stream:
        while segment := stream.read(1 << 20):
            status, ack, _ = product.request("PUT", f"/api/v1/uploads/{upload}/entries?path=_input&kind=file", client=owner, body=segment,
                                             headers={"Content-Type": "application/octet-stream", "Haeryong-Upload-Offset": str(offset),
                                                      "Haeryong-Upload-Length": str(canonical_f12["archive_bytes"]),
                                                      "Haeryong-Upload-Chunk-SHA256": digest(segment),
                                                      **({"Haeryong-Upload-Final": "true"} if offset+len(segment) == canonical_f12["archive_bytes"] else {})})
            assert status == 200
            checkpoint = ack["data"]["checkpoint"]
            offset += len(segment)
            assert checkpoint["accepted_bytes"] == str(offset)
            if offset == 3 << 20:
                product.stop()
                product.start()
                assert product.data("GET", f"/api/v1/uploads/{upload}", client=owner)["checkpoint"] == checkpoint
    complete = checkpoint | {"generation": checkpoint["generation"]+1, "complete_input": True}
    accepted = product.data("POST", f"/api/v1/uploads/{upload}/complete", client=owner,
                            headers={"Idempotency-Key": operation()}, body={"checkpoint": complete})
    product.job(owner, accepted["job_id"], timeout=300)
    sealed = product.data("GET", f"/api/v1/uploads/{upload}", client=owner)
    assert sealed["state"] == "sealed"
    accepted = product.data("POST", "/api/v1/imports", client=owner, headers={"Idempotency-Key": operation()},
                            body={"upload_id": upload, "project_id": library["unfiled_project_id"], "document_id": None, "base_version_id": None})
    published = product.job(owner, accepted["job_id"], timeout=30)
    version = published["version_id"]
    entries = product.files(owner, version, 37)
    actual = {entry["path"]: entry for entry in entries if entry["kind"] == "file"}
    assert len(entries) == 1031 and set(actual) == set(canonical_f12["files"])
    for name, expected in canonical_f12["files"].items():
        assert actual[name]["byte_size"] == str(expected["byte_size"])
        assert actual[name]["bytes_sha256"] == expected["sha256"]
    product.stop()
    product.start()
    assert product.files(owner, version, 37) == entries
    for name in ("first.html", "deep/nested/large.html", "unknown.attachment"):
        status, data, _ = product.request("GET", f"/api/v1/versions/{version}/file?"+urllib.parse.urlencode({"path": name}), client=owner, raw=True)
        assert status == 200 and digest(data) == canonical_f12["files"][name]["sha256"]
    if path := os.environ.get("HAERYONG_TASK004_WORKLOAD_REPORT"):
        write_json(path, {"fixture": "Canonical F12 through actual public HTTP/production daemon", "archive_bytes": offset,
                          "expanded_bytes": canonical_f12["expanded_bytes"], "files": canonical_f12["files"],
                          "checkpoint_restart_and_complete_files_equal": True,
                          "elapsed_seconds": time.monotonic()-started})
