"""M4 public search/CLI, complete-body and unchanged-F12 responsiveness evidence."""
import hashlib
import importlib.util
import json
import math
from pathlib import Path
import subprocess
import time
import threading
import urllib.parse

import pytest

from .harness import digest
from .test_search_jobs import derived_jobs

pytestmark = pytest.mark.e2e


def lookup(product, owner, text="", **filters):
    return product.data("GET", "/api/v1/search?" + urllib.parse.urlencode({"q": text, **filters}), client=owner)


def ready(product, owner, version, timeout=120, probe=None):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        rows = derived_jobs(product, owner, version)
        assert len(rows) == 1
        job = product.data("GET", "/api/v1/jobs/" + rows[0]["job_id"], client=owner)
        if probe:
            probe(job)
        if job["state"] == "completed":
            return job
        assert job["state"] not in {"failed", "cancelled"}, "search indexing failed"
        time.sleep(0.1)
    pytest.fail("search did not complete within the test workload budget")


def test_search_SC08_public_cli_errors_head_literals_and_pagination(product, owner, tmp_path):
    source = tmp_path / "input"
    source.mkdir()
    (source / "z.html").write_text("<p>readSource 검토 승인</p>")
    (source / "a.html").write_text("<p>resourceful 승인 검토</p>")
    (source / "unknown.attachment").write_bytes(b"preserved")
    _, _, published = product.import_tree(owner, source)
    version = published["version_id"]
    ready(product, owner, version)
    page = lookup(product, owner, "source", kind="page", limit=1)
    assert [row["page_path"] for row in page["items"]] == ["z.html"]
    assert page["total_matches"] == "2" and page["next_cursor"]
    assert page["index"]["reasons"] == [{"code": "unsupported_body", "version_count": "1"}]
    cli = product.cli(["search", "--query", "source", "--kind", "page", "--limit", "1"], client=owner)["data"]
    assert cli == page
    product.stop()
    product.start()
    next_page = lookup(product, owner, "source", kind="page", limit=1, cursor=page["next_cursor"])
    assert [row["page_path"] for row in next_page["items"]] == ["a.html"]
    for text in ("", " \t\n "):
        empty = lookup(product, owner, text)
        assert empty["items"] == [] and empty["total_matches"] == "0" and empty["next_cursor"] is None
        assert empty["index"]["state"] == "complete"
        assert product.cli(["search", "--query", text], client=owner)["data"] == empty
    assert [row["page_path"] for row in lookup(product, owner, '"검토 승인"', kind="page")["items"]] == ["z.html"]
    for text in ("OR", "NEAR", "*", "-", "body:source", '"read source"'):
        assert lookup(product, owner, text, kind="page")["items"] == []
    for suffix, reason in [
        ("q=x&q=y", "unknown_duplicate_or_empty_query"), ("unknown=x", "unknown_duplicate_or_empty_query"),
        ("kind=", "unknown_duplicate_or_empty_query"), ("q=%FF", "invalid_query"), ("q=%GG", "invalid_query"),
        ("q=%00", "invalid_query"), ("q=%01", "invalid_query"), ("q=%22unclosed", "malformed_phrase"),
        ("lifecycle=trash", "invalid_lifecycle"), ("versions=oldest", "invalid_versions"),
        ("kind=topic", "invalid_kind"), ("limit=0", "invalid_page_size"), ("limit=201", "invalid_page_size"),
        ("limit=no", "invalid_page_size"), ("cursor=bad", "cursor_mismatch"),
        (urllib.parse.urlencode({"q": "a" * 4097}), "query_too_long"),
        (urllib.parse.urlencode({"q": '"a' + "ß" * 3000 + 'z"'}), "query_too_long"),
    ]:
        path = "/api/v1/search?" + suffix
        status, body, _ = product.request("GET", path, client=owner)
        assert status == 400 and body["error"]["code"] == "INVALID_REQUEST"
        assert body["error"]["details"]["reason"] == reason
        assert body["error"]["retryable"] is False
        status, body, fields = product.request("HEAD", path, client=owner, raw=True)
        assert status == 400 and body == b"" and int(fields["Content-Length"]) > 0
    for method in ("GET", "HEAD"):
        status, body, _ = product.request(method, "/api/v1/search?q=source", raw=True)
        assert status == 401
        if method == "HEAD":
            assert body == b""
    status, body, fields = product.request("HEAD", "/api/v1/search?q=source", client=owner, raw=True)
    assert status == 200 and body == b"" and fields["Cache-Control"] == "no-store"
    assert int(fields["Content-Length"]) > 0
    product.cli(["search", "--query", "source", "--version", version], client=owner, code=2)
    product.cli(["search", "--query", "source", "--new-document"], client=owner, code=2)
    product.cli(["search", "--query", "source", "--lifecycle", "trash"], client=owner, code=2)
    assert product.data("GET", "/api/v1/capabilities", client=owner)["features"]["search"] is True


def test_search_SC02_maximum_expanded_boundary_through_public_service(product, owner, tmp_path):
    source = tmp_path / "expanded"
    source.mkdir()
    phrases = ["a" + "ß" * 2047 + "z", "b" + "ﬃ" * 1364 + "xyz"]
    for index, phrase in enumerate(phrases):
        (source / f"boundary{index}.html").write_text("<p>" + "x" * 28673 + phrase + "</p>")
        (source / f"inside{index}.html").write_text("<p>" + phrase + "</p>")
    metadata_nfd = "a" + "e\u0301" * 4092 + "z"
    metadata_nfc = "a" + "é" * 4092 + "z"
    for name, phrase in [("nfd", metadata_nfd), ("nfc", metadata_nfc)]:
        (source / f"metadata-{name}.html").write_text('<meta name="keywords" content="' + "x" * 27768 + phrase + '"><p>metadata control</p>')
    _, _, published = product.import_tree(owner, source)
    ready(product, owner, published["version_id"])
    for index, phrase in enumerate(phrases):
        result = lookup(product, owner, '"' + phrase + '"', kind="page")
        assert {row["page_path"] for row in result["items"]} == {f"boundary{index}.html", f"inside{index}.html"}
        assert result["total_matches"] == "2"
        assert result["index"]["state"] == "complete"
    metadata = lookup(product, owner, '"' + metadata_nfc + '"', kind="page")
    assert {hit["page_path"] for hit in metadata["items"]} == {"metadata-nfd.html", "metadata-nfc.html"}
    for hit in metadata["items"]:
        if hit["page_path"] == "metadata-nfd.html":
            assert "e\u0301" in hit["snippet"]



def test_search_SC16_independent_large_unique_boundaries_and_terminal_tail(product, owner, tmp_path, record_testsuite_property):
    generator = Path(__file__).parents[1] / "fixtures" / "search-large" / "generate.py"
    spec = importlib.util.spec_from_file_location("search_large", generator)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    source = tmp_path / "large-body"
    identity = module.generate(source)
    # Freeze actual bytes, offsets and exact literals before import/query execution.
    (tmp_path / "large-oracle.json").write_text(json.dumps(identity, sort_keys=True))
    started = time.monotonic()
    _, _, published = product.import_tree(owner, source, timeout=120)
    version = published["version_id"]
    ready(product, owner, version, timeout=500)
    for row in identity["markers"]:
        result = lookup(product, owner, '"' + row["literal"] + '"', kind="page")
        assert [(hit["version_id"], hit["page_path"]) for hit in result["items"]] == [(version, "large.html")], row["literal"]
        assert result["index"]["html_complete"] == result["index"]["html_total"] == "1"
        assert result["index"]["state"] == "complete"
    status, payload, _ = product.request("GET", f"/api/v1/versions/{version}/file?path=large.html", client=owner, raw=True)
    assert status == 200 and hashlib.sha256(payload).hexdigest() == identity["sha256"]
    record_testsuite_property("SC16", json.dumps(identity | {"index_and_import_seconds": time.monotonic() - started}))


def percentiles(values):
    values = sorted(values)
    return {"n": len(values), "p50_ms": values[math.ceil(len(values) * .50) - 1],
            "p95_ms": values[math.ceil(len(values) * .95) - 1], "max_ms": values[-1]}


class F12SearchProbe:
    """SC20 observer after the canonical transfer restart; no second F12 import."""
    def __init__(self, product, owner, canonical, version):
        self.product, self.owner, self.canonical, self.version = product, owner, canonical, version
        self.started = time.monotonic()
        self.stopped = threading.Event()
        self.reads, self.metadata, self.searches, self.rss = [], [], [], []
        self.stages, self.errors = set(), []
        self.completed = None
        self.retryable_searches = 0
        self.thread = threading.Thread(target=self.observe, daemon=True)
        self.thread.start()

    def observe(self):
        try:
            while not self.stopped.is_set():
                rows = derived_jobs(self.product, self.owner, self.version)
                assert len(rows) == 1
                job = self.product.data("GET", "/api/v1/jobs/" + rows[0]["job_id"], client=self.owner)
                self.stages.add(job["result"]["stage"])
                before = time.monotonic()
                status, payload, _ = self.product.request(
                    "GET", f"/api/v1/versions/{self.version}/file?path=first.html", client=self.owner, raw=True)
                self.reads.append((time.monotonic() - before) * 1000)
                assert status == 200 and digest(payload) == self.canonical["files"]["first.html"]["sha256"]
                before = time.monotonic()
                files = self.product.data("GET", f"/api/v1/versions/{self.version}/files?limit=1", client=self.owner)
                self.metadata.append((time.monotonic() - before) * 1000)
                assert len(files["items"]) == 1
                before = time.monotonic()
                status, response, _ = self.product.request("GET", "/api/v1/search?q=first.html&kind=page", client=self.owner)
                self.searches.append((time.monotonic() - before) * 1000)
                if status == 503:
                    assert response["error"]["code"] == "SERVICE_RECOVERING" and response["error"]["retryable"] is True
                    assert response["error"]["details"]["reason"] == "query_timeout"
                    self.retryable_searches += 1
                else:
                    assert status == 200
                    result = response["data"]
                    assert int(result["index"]["html_complete"]) <= int(result["index"]["html_total"])
                usage = subprocess.check_output(["ps", "-o", "rss=", "-p", str(self.product.process.pid)], text=True)
                self.rss.append(int(usage.strip()) * 1024)
                assert job["state"] not in {"failed", "cancelled"}
                if job["state"] == "completed":
                    assert job["result"]["files_done"] == job["result"]["files_total"] == "1031"
                    self.completed = time.monotonic()
                    return
                self.stopped.wait(2)
        except BaseException as error:
            self.errors.append(error)

    def close(self):
        self.stopped.set()
        self.thread.join(31)
        assert not self.thread.is_alive(), "SC20 observer did not stop"

    def finish(self, timeout):
        self.thread.join(timeout)
        assert not self.thread.is_alive(), "SC20 resumed index observation exceeded its separate hang guard"
        assert not self.errors, "SC20 public read/progress observation failed"
        assert self.completed is not None and len(self.reads) > 1 and "extract" in self.stages
        for term, kind, path in [("first.html", "page", "first.html"),
                                 ("deep/nested/large.html", "page", "deep/nested/large.html"),
                                 ("unknown.attachment", "attachment", "unknown.attachment")]:
            result = lookup(self.product, self.owner, term, kind=kind)
            assert path in {hit["page_path"] for hit in result["items"]}
            assert result["index"]["state"] == "complete"
            assert result["index"]["files_total"] == result["index"]["files_discovered"] == "1031"
        before = time.monotonic()
        short = lookup(self.product, self.owner, "aa", kind="page")
        short_ms = (time.monotonic() - before) * 1000
        assert [(hit["version_id"], hit["page_path"]) for hit in short["items"]] == [(self.version, "deep/nested/large.html")]
        assert short["total_matches"] == "1"
        sizes = {suffix or "db": (self.product.data_dir / "storage" / ("library.sqlite" + suffix)).stat().st_size
                 for suffix in ("", "-wal")}
        return {"two_character_full_body_fallback_ms": short_ms, "resumed_index_observation_seconds": self.completed - self.started,
                "small_file_reads": percentiles(self.reads), "metadata_reads": percentiles(self.metadata),
                "searches": percentiles(self.searches), "max_sampled_rss_bytes": max(self.rss), "sizes": sizes,
                "transaction_timing": "separate instrumented persistence spec", "sampling_interval_seconds": 2, "retryable_searches": self.retryable_searches,
                "observation_scope": "resumed extraction after canonical transfer restart; overlaps final download",
                "canonical_bytes_unchanged": True}
