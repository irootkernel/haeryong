#!/usr/bin/env python3
"""Exercise documentation-validator admission against disposable full-doc fixtures."""
from pathlib import Path
import json
import itertools
import os
import re
import shutil
import subprocess
import tempfile
import unittest


class ValidatorAdmissionTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        parent = os.environ.get("HAERYONG_DOCS_TEST_TMP_ROOT")
        cls.scratch = tempfile.TemporaryDirectory(prefix="haeryong-docs-test-", dir=parent)
        cls.addClassCleanup(cls.scratch.cleanup)
        cls.root = Path(cls.scratch.name)
        source = Path(__file__).resolve().parents[2]
        shutil.copytree(source / "docs", cls.root / "docs")
        subprocess.run(["git", "init", "--quiet", str(cls.root)], check=True)
        cls.task = cls.root / "docs/tasks/EPIC-001/TASK-001-01.md"
        cls.original = cls.task.read_text(encoding="utf-8")

    def run_fixture(self, status, *, missing=False, markers=None, extra_assertion=None):
        text = re.sub(r"^status:.*$", "" if missing else "status: " + json.dumps(status),
                      self.original, count=1, flags=re.M)
        if markers is not None:
            pattern = r"^- \[[^\]]*\](?= T-001-01-)"
            count = len(re.findall(pattern, text, flags=re.M))
            self.assertGreater(count, 0, "Fixture must contain Task assertions")
            values = itertools.cycle(markers)
            text, rewritten = re.subn(pattern, lambda _: "- [" + next(values) + "]",
                                      text, flags=re.M)
            self.assertEqual(rewritten, count)
            expected = list(itertools.islice(itertools.cycle(markers), count))
            self.assertEqual(re.findall(r"^- \[([^\]]*)\](?= T-001-01-)", text, re.M), expected)
        if extra_assertion is not None:
            text += "\n" + extra_assertion + "\n"
        self.task.write_text(text, encoding="utf-8")
        self.assertEqual(self.task.read_text(encoding="utf-8"), text)
        process = subprocess.run(
            ["python3", "-B", str(self.root / "docs/testing/validate_docs.py"),
             "--root", str(self.root), "--skip-manifest"], capture_output=True, text=True)
        self.assertTrue(process.stdout, process.stderr)
        return process.returncode, json.loads(process.stdout)

    def test_each_protocol_planning_state_is_admitted(self):
        for status in ("planned", "ready", "in_progress", "blocked", "in_review", "done"):
            with self.subTest(status=status):
                code, report = self.run_fixture(status)
                self.assertEqual(code, 0, report["issues"])

    def test_foreign_missing_and_nonscalar_statuses_are_reported(self):
        for status in ("complete", "pass", "fail", "not_run", "", None, [], {"bad": "value"}):
            with self.subTest(status=status):
                code, report = self.run_fixture(status)
                self.assertNotEqual(code, 0)
                self.assertIn("planning_status", {issue["kind"] for issue in report["issues"]})
        code, report = self.run_fixture(None, missing=True)
        self.assertNotEqual(code, 0)
        self.assertIn("planning_status", {issue["kind"] for issue in report["issues"]})

    def test_mixed_checked_assertions_keep_the_same_inventory(self):
        _, baseline = self.run_fixture("in_review", markers=[" "])
        for markers in (["x", "X", " "], ["X"]):
            with self.subTest(markers=markers):
                code, report = self.run_fixture("in_review", markers=markers)
                self.assertEqual(code, 0, report["issues"])
                self.assertEqual(report["counts"]["task_assertions"],
                                 baseline["counts"]["task_assertions"])

    def test_invalid_markers_do_not_satisfy_assertion_presence(self):
        code, report = self.run_fixture("in_review", markers=["y", "*"])
        self.assertNotEqual(code, 0)
        kinds = {issue["kind"] for issue in report["issues"]}
        self.assertIn("missing_assertions", kinds)
        self.assertIn("invalid_assertion_marker", kinds)

    def test_invalid_markers_cannot_hide_beside_valid_assertions(self):
        code, baseline = self.run_fixture("in_review", markers=[" "])
        self.assertEqual(code, 0, baseline["issues"])
        for markers in (["y", "x", " "], ["X", "*", "x"]):
            with self.subTest(markers=markers):
                code, report = self.run_fixture("in_review", markers=markers)
                self.assertNotEqual(code, 0)
                kinds = {issue["kind"] for issue in report["issues"]}
                self.assertIn("invalid_assertion_marker", kinds)
                self.assertNotIn("missing_assertions", kinds)
                self.assertEqual(report["counts"]["task_assertions"],
                                 baseline["counts"]["task_assertions"])
        for ident, issue in (("T-001-02-01", "assertion_owner"),
                             ("T-001-01-01", "duplicate_assertions")):
            with self.subTest(ident=ident):
                code, report = self.run_fixture("in_review", extra_assertion=f"- [y] {ident}: Fixture.")
                self.assertNotEqual(code, 0)
                kinds = {item["kind"] for item in report["issues"]}
                self.assertIn("invalid_assertion_marker", kinds)
                self.assertIn(issue, kinds)

    def test_checked_foreign_and_duplicate_ids_are_rejected(self):
        cases = [("T-001-02-01", "assertion_owner"), ("T-001-01-01", "duplicate_assertions")]
        for ident, issue in cases:
            with self.subTest(ident=ident):
                code, report = self.run_fixture("in_review", extra_assertion=f"- [x] {ident}: Fixture.")
                self.assertNotEqual(code, 0)
                self.assertIn(issue, {item["kind"] for item in report["issues"]})


if __name__ == "__main__":
    unittest.main()
