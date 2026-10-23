"""M3 public durable work plus M4 safe reason/readback and restart query extensions."""
import time
import urllib.parse

import pytest

pytestmark = pytest.mark.e2e


def derived_jobs(product, owner, version):
    rows = product.data("GET", "/api/v1/jobs", client=owner)["items"]
    return [row for row in rows if row["kind"] == "derived_index"
            and row.get("result", {}).get("version_id") == version
            and row.get("result", {}).get("work_kind") == "search"]


def assert_progress(value, version):
    assert set(value) == {"version_id", "work_kind", "stage", "files_done", "files_total",
                          "bytes_done", "bytes_total", "pages_done", "pages_total", "failed", "checkpoint"}
    assert value["version_id"] == version
    assert value["work_kind"] == "search"
    assert value["stage"] in {"inventory", "extract", "commit", "complete"}
    for key in ("files_done", "files_total", "bytes_done", "bytes_total", "pages_done", "pages_total", "failed"):
        assert value[key] is None or (isinstance(value[key], str) and value[key].isdecimal())
    assert len(value["checkpoint"]) == 64


def test_search_jobs_public_progress_restart_and_original_bytes(product, owner, tmp_path):
    source = tmp_path / "search-pages"
    source.mkdir()
    bodies = {}
    # Enough serial pages to stop real work between public checkpoints, without
    # timing the parser or adding a test-only production switch.
    for number in range(80):
        name = f"page-{number:03}.html"
        body = (f"<html><title>Page {number}</title><p id='retry'>readSource 한국어 "
                + "retained text " * 400 + f" unique-tail-{number}</p></html>").encode()
        (source / name).write_bytes(body)
        bodies[name] = body
    (source / "raw.bin").write_bytes(b"original attachment\x00")
    key, accepted, published = product.import_tree(owner, source, timeout=60)
    version = published["version_id"]
    deadline = time.monotonic() + 30
    running = None
    while time.monotonic() < deadline:
        rows = derived_jobs(product, owner, version)
        assert len(rows) == 1
        job = product.data("GET", "/api/v1/jobs/" + rows[0]["job_id"], client=owner)
        assert_progress(job["result"], version)
        if 0 < int(job["result"]["pages_done"]) < 80:
            running = job
            break
        assert job["state"] not in {"failed", "cancelled", "completed"}
        time.sleep(0.02)
    assert running is not None, "no public partial-page checkpoint observed"
    product.stop(kill=True)
    product.start()
    resumed = product.data("GET", "/api/v1/jobs/" + running["job_id"], client=owner)
    assert resumed["attempt_generation"] > running["attempt_generation"]
    assert int(resumed["result"]["pages_done"]) >= int(running["result"]["pages_done"])
    final = product.job(owner, running["job_id"], timeout=90)
    assert_progress(final, version)
    assert final["pages_done"] == final["pages_total"] == "80"
    assert final["files_done"] == final["files_total"] == "81"
    assert final["failed"] == "0"
    assert final["stage"] == "complete"
    # M4: restarted persisted derived bodies are queried by their exact immutable version/page.
    hits = product.data("GET", "/api/v1/search?q=unique-tail-79&kind=page", client=owner)
    assert [(row["version_id"], row["page_path"]) for row in hits["items"]] == [(version, "page-079.html")]
    assert hits["index"]["state"] == "complete"

    # Replaying the completed CLI import retains both import and derived identity.
    replay = product.cli(["import", str(source), "--operation-id", key], client=owner, timeout=60)["data"]
    assert replay["result"]["job_id"] == accepted["result"]["job_id"]
    assert [row["job_id"] for row in derived_jobs(product, owner, version)] == [running["job_id"]]
    for name in ("page-000.html", "page-079.html", "raw.bin"):
        status, body, _ = product.request("GET", f"/api/v1/versions/{version}/file?"
                                          + urllib.parse.urlencode({"path": name}), client=owner, raw=True)
        assert status == 200
        assert body == bodies.get(name, b"original attachment\x00")
    features = product.data("GET", "/api/v1/capabilities", client=owner)["features"]
    assert features["search"] is True


def test_search_jobs_labelled_spool_failure_is_terminal_and_preserves_public_bytes(product, owner, tmp_path, monkeypatch):
    """SC14/G5 fault fixture: TMPDIR is a regular file, denying only search spooling.

    No internal SQL/state inspection or production fault switch is used. Original
    objects and delivery copies use the normal private content-store directory.
    """
    product.stop()
    invalid_spool = tmp_path / "labelled-spool-failure"
    invalid_spool.write_bytes(b"not a directory")
    source = tmp_path / "failure-input"
    source.mkdir()
    original = b"<section id='retry'>Original file stays readable after derived failure</section>"
    (source / "page.html").write_bytes(original)
    with monkeypatch.context() as fault:
        fault.setenv("TMPDIR", str(invalid_spool))
        product.start()
        _, _, published = product.import_tree(owner, source)
        version = published["version_id"]
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            rows = derived_jobs(product, owner, version)
            assert len(rows) == 1
            job = product.data("GET", "/api/v1/jobs/" + rows[0]["job_id"], client=owner)
            if job["state"] in {"completed", "failed", "cancelled"}:
                break
            time.sleep(0.05)
        assert job["state"] == "failed"
        assert job["error"] == "storage_failed"
        assert_progress(job["result"], version)
        assert job["result"]["stage"] == "complete"
        assert job["result"]["failed"] == "1"
        assert job["result"]["pages_done"] == "0"
        # M4: safe aggregate failure explanation survives owner HTTP/CLI without job diagnostics.
        failure_page = product.data("GET", "/api/v1/search?q=page.html&kind=page", client=owner)
        assert failure_page["index"]["state"] == "failed"
        assert failure_page["index"]["html_failed"] == "1"
        assert failure_page["index"]["html_pending"] == "0"
        assert failure_page["items"][0]["body_state"] == "failed"
        assert failure_page["index"]["reasons"] == [
            {"code": "storage_failed", "version_count": "1"},
        ]
        cli_page = product.cli(["search", "--query", "page.html", "--kind", "page"], client=owner)["data"]
        assert cli_page == failure_page
        assert "labelled-spool-failure" not in str(failure_page)

        status, body, _ = product.request("GET", f"/api/v1/versions/{version}/file?path=page.html", client=owner, raw=True)
        assert status == 200 and body == original
        product.stop()
    product.start()
    # The fault is removed: any forbidden retry could now complete, so observe
    # stable state/generation/progress repeatedly after startup sweep runs.
    for _ in range(8):
        current = product.data("GET", "/api/v1/jobs/" + job["job_id"], client=owner)
        assert current["state"] == "failed"
        assert current["attempt_generation"] == job["attempt_generation"]
        assert current["result"] == job["result"]
        assert [row["job_id"] for row in derived_jobs(product, owner, version)] == [job["job_id"]]
        time.sleep(0.1)
    status, body, _ = product.request("GET", f"/api/v1/versions/{version}/file?path=page.html", client=owner, raw=True)
    assert status == 200 and body == original


def test_service_stop_during_large_single_token_search_indexing(product, owner, tmp_path):
    """G6: cancellation must interrupt Next while it reads one enormous token."""
    source = tmp_path / "single-token-input"
    source.mkdir()
    (source / "large.html").write_bytes(b"<html><body>" + b"a" * (40 << 20) + b"</body></html>")
    _, _, published = product.import_tree(owner, source, timeout=120)
    version = published["version_id"]
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        job = derived_jobs(product, owner, version)[0]
        if job["state"] == "running" and job["result"]["stage"] == "extract":
            break
        time.sleep(0.02)
    else:
        raise AssertionError("large-page extraction did not start")
    # Inventory was committed before the page turn. Let that turn enter the
    # tokenizer; the separate deterministic unit test cancels between reads.
    time.sleep(0.5)
    assert product.request("GET", "/health/live")[0] == 200
    product.stop()
    product.start()
    resumed = derived_jobs(product, owner, version)
    assert len(resumed) == 1 and resumed[0]["job_id"] == job["job_id"]
    assert resumed[0]["attempt_generation"] > job["attempt_generation"]
    product.stop()
