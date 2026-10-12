#!/usr/bin/env python3
"""Read-only checks for Haeryong docs. Requires Python 3 and PyYAML.

No application is built or run. --manifest prints an inventory to stdout;
it never writes it. --skip-manifest is for checking an in-progress revision.
"""
from __future__ import annotations

import argparse
from collections import Counter, defaultdict
import hashlib
from html.parser import HTMLParser
import json
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys
from urllib.parse import unquote, urlsplit

try:
    import yaml
except ImportError:
    sys.exit("PyYAML is required. Use a Python environment with PyYAML installed.")

SOURCE_HASH = "8f26fea2833cd3cc3312c1afadea74e042d1d895b079bfa1bdff9cc02cf563e5"
TASK_RE = re.compile(r"\bTASK-\d{3}-\d{2}\b")
FENCES = re.compile(r"^```([^\n]*)\n(.*?)^```[ \t]*$", re.M | re.S)


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def tables(text: str):
    """Return source table rows; escaped pipes do not split cells."""
    for line in text.splitlines():
        if line.startswith("|"):
            yield [part.strip().replace("\\|", "|") for part in
                   re.split(r"(?<!\\)\|", line.strip())[1:-1]]


def headings(text: str) -> set[str]:
    found, counts = set(), Counter()
    for line in FENCES.sub("", text).splitlines():
        match = re.match(r"^#{1,6}\s+(.+?)\s*#*\s*$", line)
        if not match:
            continue
        title = re.sub(r"\[([^]]+)\]\([^)]*\)", r"\1", match[1])
        base = re.sub(r"[^\w\s-]", "", title.lower()).replace(" ", "-")
        n = counts[base]
        found.add(base if n == 0 else f"{base}-{n}")
        counts[base] += 1
    return found


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, help="Repository checkout, not docs directory")
    parser.add_argument("--skip-manifest", action="store_true")
    parser.add_argument("--manifest", action="store_true", help="Print inventory only; no writes")
    args = parser.parse_args()
    root = (args.root or Path(__file__).resolve().parents[2]).resolve()
    docs = root / "docs"
    if not (docs / "README.md").is_file():
        parser.error(f"Not a Haeryong checkout: {root}")
    md = sorted(docs.rglob("*.md"))
    inventory = sorted([p for p in md if p.name != "MANIFEST.md"] +
                       [docs / "testing" / "validate_docs.py"])
    readme = (docs / "README.md").read_text(encoding="utf-8")
    version_match = re.search(r"\*\*Bundle version:\*\*\s+(\S+)", readme)
    version = version_match[1] if version_match else "unknown"
    prepared_match = re.search(r"\*\*Prepared:\*\*\s+(\d{4}-\d{2}-\d{2})", readme)
    prepared = prepared_match[1] if prepared_match else "unspecified"
    if args.manifest:
        print("# Documentation file manifest\n")
        print(f"**Bundle:** {version}\n\n**Generated:** {prepared}\n")
        print(f"This inventory covers {len(inventory)} other files: all Markdown documents "
              "and the read-only validation utility. Paths are relative to docs; sizes and "
              "SHA-256 cover exact bytes. MANIFEST.md excludes itself. This is not runtime evidence.\n")
        print("| File | Bytes | SHA-256 |\n| --- | --- | --- |")
        for path in inventory:
            data = path.read_bytes()
            print(f"| `{path.relative_to(docs).as_posix()}` | {len(data)} | `{digest(data)}` |")
        return 0

    issues: list[dict] = []
    counts: dict = {"bundle": version, "markdown_files": len(md)}
    def check(ok: bool, kind: str, detail) -> None:
        if not ok:
            issues.append({"kind": kind, "detail": detail})

    texts = {p: p.read_text(encoding="utf-8") for p in md}
    guide = texts.get(docs / "REVIEW-GUIDE.md", "")
    guide_match = re.search(r"\*\*(\d+\.\d+\.\d+) personal directory-reader execution specification\*\*", guide)
    guide_version = guide_match[1] if guide_match else None
    check(version != "unknown" and guide_version == version, "review_guide_version",
          {"bundle": version, "guide": guide_version})
    counts["review_guide_version"] = guide_version
    records: dict[str, dict] = {}
    assertions: list[str] = []
    syntax = Counter()
    for path, text in texts.items():
        rel = path.relative_to(root).as_posix()
        check(len(re.findall(r"^```", text, re.M)) % 2 == 0, "fences", rel)
        if text.startswith("---\n"):
            try:
                fm = yaml.safe_load(text.split("---", 2)[1])
                if isinstance(fm, dict) and "id" in fm:
                    ident = fm["id"]
                    check(ident not in records, "duplicate_id", ident)
                    records[ident] = {**fm, "_path": path, "_text": text}
                    check(fm.get("status") == "planned", "planning_status", ident)
            except (yaml.YAMLError, IndexError) as exc:
                check(False, "frontmatter", f"{rel}: {exc}")
        for lang, body in FENCES.findall(text):
            try:
                if lang.strip() == "json":
                    json.loads(body); syntax["json"] += 1
                elif lang.strip() == "jsonl":
                    for line in body.splitlines():
                        if line.strip(): json.loads(line)
                    syntax["jsonl"] += 1
                elif lang.strip() in {"yaml", "yml"}:
                    yaml.safe_load(body); syntax["yaml"] += 1
            except (ValueError, yaml.YAMLError) as exc:
                check(False, "example_syntax", f"{rel}: {exc}")
        arity = None
        for line in FENCES.sub("", text).splitlines():
            if line.startswith("|"):
                row = next(tables(line))
                if arity is None: arity = len(row)
                check(len(row) == arity, "table_arity", f"{rel}: {line[:100]}")
            else:
                arity = None

    tasks = {k: v for k, v in records.items() if k.startswith("TASK-")}
    epics = {k: v for k, v in records.items() if k.startswith("EPIC-")}
    counts.update(tasks=len(tasks), epics=len(epics), frontmatter=len(records), syntax_blocks=dict(syntax))
    check(len(tasks) == 61 and len(epics) == 10, "execution_inventory", [len(tasks), len(epics)])
    known_r = {f"R{i:02d}" for i in range(1, 41)}
    known_a = {f"C{i:02d}" for i in range(1, 10)} | {f"M{i:02d}" for i in range(1, 21)}
    r_tasks, a_tasks, x_tasks = defaultdict(set), defaultdict(set), defaultdict(set)
    graph = {}
    for ident, record in records.items():
        dependencies = record.get("depends_on", []) + record.get("completion_requires", [])
        graph[ident] = dependencies
        for dep in dependencies:
            check(dep in records, "missing_dependency", [ident, dep])
        if ident in tasks:
            epic = record.get("epic")
            check(epic in epics, "task_epic", ident)
            for dep in record.get("depends_on", []):
                check(int(dep.split("-")[1]) <= int(ident.split("-")[1]), "future_epic", [ident, dep])
            for r in record.get("requirements", []):
                check(r in known_r, "unknown_requirement", [ident, r]); r_tasks[r].add(ident)
            for a in record.get("acceptance", []):
                check(a in known_a, "unknown_acceptance", [ident, a]); a_tasks[a].add(ident)
            for x in record.get("supplemental_tests", []): x_tasks[x].add(ident)
            found = re.findall(r"^- \[ \] (T-\d{3}-\d{2}-\d{2}):", record["_text"], re.M)
            check(bool(found), "missing_assertions", ident)
            for assertion in found:
                check(assertion.startswith(ident.replace("TASK", "T") + "-"), "assertion_owner", assertion)
            assertions += found
    check(len(assertions) == len(set(assertions)), "duplicate_assertions", assertions)
    counts["task_assertions"] = len(assertions)
    for epic, record in epics.items():
        members = {t for t, r in tasks.items() if r.get("epic") == epic}
        check(set(record.get("completion_requires", [])) == members, "epic_membership", epic)
    active, visited = set(), set()
    def visit(node):
        if node in active:
            check(False, "dependency_cycle", node); return
        if node in visited: return
        active.add(node)
        for dep in graph.get(node, []): visit(dep)
        active.remove(node); visited.add(node)
    for ident in graph: visit(ident)
    check(set(r_tasks) == known_r, "requirement_coverage", sorted(known_r - set(r_tasks)))
    check(set(a_tasks) == known_a, "acceptance_coverage", sorted(known_a - set(a_tasks)))

    source = docs / "sources" / "haeryong-concept-proposal-v4.md"
    counts["source_sha256"] = digest(source.read_bytes())
    check(counts["source_sha256"] == SOURCE_HASH, "source_hash", counts["source_sha256"])
    source_a = {row[0][:3]: row[1] for row in tables(texts[source])
                if len(row) == 2 and re.match(r"^[CM]\d\d\.", row[0])}
    catalog = texts[docs / "testing" / "acceptance-catalog.md"]
    catalog_sections = re.split(r"^## ([CM]\d\d)\.[^\n]*\n", catalog, flags=re.M)
    catalog_a = {}
    named_edges = 0
    for i in range(1, len(catalog_sections), 2):
        ident, body = catalog_sections[i:i+2]
        original = re.search(r"^\*\*Original acceptance requirement:\*\* (.+)$", body, re.M)
        if original: catalog_a[ident] = original[1].strip()
        for line in body.splitlines():
            if re.match(r"\*\*(?:Primary work|Early / additional work|Release regression|Stage A[^:]*):", line):
                for task in set(TASK_RE.findall(line)):
                    named_edges += 1
                    check(task in a_tasks[ident], "catalog_named_role", [ident, task])
    check(source_a == catalog_a and len(source_a) == 29, "source_acceptance_preservation",
          sorted(k for k in set(source_a) | set(catalog_a) if source_a.get(k) != catalog_a.get(k)))
    trace = texts[docs / "testing" / "traceability.md"]
    for row in tables(trace):
        if len(row) >= 4 and row[0] in known_r:
            check(set(TASK_RE.findall(row[2])) == r_tasks[row[0]], "requirement_trace", row[0])
        if len(row) >= 5 and row[0] in known_a:
            for task in set(TASK_RE.findall(" ".join(row[2:]))):
                check(task in a_tasks[row[0]], "trace_named_role", [row[0], task])
            if row[0] in {"M08", "M14", "M20"}:
                check(all(int(t.split("-")[1]) <= 7 for t in TASK_RE.findall(row[2])),
                      "stage_a_future_primary", row[0])
    cases = {}
    for row in tables(texts[docs / "testing" / "contract-cases.md"]):
        if row and re.fullmatch(r"X\d{2}", row[0]):
            expected = set(TASK_RE.findall(" ".join(row[3:])))
            cases[row[0]] = expected
            check(expected == x_tasks[row[0]], "supplemental_roles",
                  {"case": row[0], "table_only": sorted(expected-x_tasks[row[0]]),
                   "task_only": sorted(x_tasks[row[0]]-expected)})
            collective = set().union(*(set(tasks.get(t, {}).get("requirements", [])) for t in expected))
            check(set(re.findall(r"R\d{2}", row[2])) <= collective, "supplemental_collective_coverage", row[0])
    check(set(cases) == set(x_tasks), "unknown_supplemental", sorted(set(cases)^set(x_tasks)))
    counts.update(requirements=len(r_tasks), acceptance=len(source_a), supplemental_cases=len(cases),
                  catalog_named_edges=named_edges)

    link_count, anchor_count = 0, 0
    for path, text in texts.items():
        for match in re.finditer(r"\]\(([^\n)]+)\)", FENCES.sub("", text)):
            target = match[1].strip().split(' "', 1)[0].strip("<>")
            if re.match(r"^[A-Za-z][A-Za-z0-9+.-]*:", target) or target.startswith("//"):
                continue
            part, _, anchor = target.partition("#")
            filename = unquote(part.split("?", 1)[0])
            dest = (path.parent / filename).resolve() if filename else path
            link_count += 1
            check(dest.exists(), "broken_link", [str(path.relative_to(root)), target])
            if anchor and dest in texts:
                anchor_count += 1
                check(unquote(anchor) in headings(texts[dest]), "heading_link",
                      [str(path.relative_to(root)), target])
    counts.update(relative_links=link_count, heading_links=anchor_count)

    example = texts[docs / "examples" / "package-example.md"]
    payload = {}
    for name, body in re.findall(r"^## Payload file: `([^`]+)`\n.*?^```[^\n]*\n(.*?)^```", example, re.M | re.S):
        payload[name] = body.encode("utf-8")
    blocks = FENCES.findall(example)
    inv_blocks = [body for lang, body in blocks if lang == "jsonl"]
    if len(inv_blocks) == 1:
        actual = [json.loads(line) for line in inv_blocks[0].splitlines() if line]
        empty = re.findall(r"^## Empty directory: `([^`]+)`", example, re.M)
        directories = set(empty)
        for name in payload:
            directories.update(str(p) for p in PurePosixPath(name).parents if str(p) != ".")
        expected = [[p, "directory"] for p in directories]
        expected += [[p, "file", str(len(b)), digest(b)] for p, b in payload.items()]
        expected.sort(key=lambda row: row[0].encode("utf-8"))
        check(actual == expected, "example_inventory", "payload path/type/size/hash mismatch")
        canonical = "".join(json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n" for row in expected)
        check(canonical == inv_blocks[0], "example_inventory_serialization", "JSONL mismatch")
        expected_hashes = {"payload_sha256": digest(canonical.encode())}
        for heading, key in [("Optional submitted metadata", "supplied_metadata_sha256"),
                             ("Generated service metadata", "manifest_sha256")]:
            section = re.search(r"^## " + re.escape(heading) + r"\n(.*?)(?=^## |\Z)", example, re.M | re.S)
            body = next((b for lang, b in FENCES.findall(section[1]) if lang == "json"), None) if section else None
            check(body is not None, "example_metadata_block", heading)
            if body: expected_hashes[key] = digest(body.encode())
        for key, value in expected_hashes.items():
            check(value in example, "example_digest", [key, value])
        counts["example_digests"] = expected_hashes
        counts.update(payload_files=len(payload), html_pages=sum(p.endswith(".html") for p in payload),
                      directory_entries=len(directories))
    else:
        check(False, "example_inventory", "Expected one JSONL inventory")

    class LocalLinks(HTMLParser):
        def __init__(self):
            super().__init__(); self.urls = []
        def handle_starttag(self, tag, attrs):
            self.urls.extend(value for key, value in attrs
                             if key in {"href", "src"} and value)
    local_targets = 0
    import posixpath
    for name, data in payload.items():
        if not name.endswith(".html"):
            continue
        links = LocalLinks(); links.feed(data.decode("utf-8"))
        for value in links.urls:
            parsed = urlsplit(value)
            if parsed.scheme or parsed.netloc or not parsed.path:
                continue
            target = posixpath.normpath(posixpath.join(posixpath.dirname(name), unquote(parsed.path)))
            local_targets += 1
            check(target in payload, "example_local_link", [name, value, target])
    counts["example_local_html_targets"] = local_targets

    if not args.skip_manifest:
        manifest_text = texts.get(docs / "MANIFEST.md", "")
        supplied = {m[1]: (int(m[2]), m[3]) for m in re.finditer(
            r"^\| `([^`]+)` \| (\d+) \| `([0-9a-f]{64})` \|$", manifest_text, re.M)}
        expected = {p.relative_to(docs).as_posix(): (p.stat().st_size, digest(p.read_bytes())) for p in inventory}
        check(set(supplied) == set(expected), "manifest_inventory",
              {"missing": sorted(set(expected)-set(supplied)), "extra": sorted(set(supplied)-set(expected))})
        for name, value in expected.items():
            check(supplied.get(name) == value, "manifest_bytes", name)
        counts["manifest_entries"] = len(expected)
    else:
        counts["manifest_check"] = "skipped explicitly; not a final integrity pass"
    git = subprocess.run(["git", "diff", "--check", "--", "docs"], cwd=root, text=True, capture_output=True)
    check(git.returncode == 0, "git_diff_check", git.stdout + git.stderr)
    counts["scope"] = "Documentation only; no browser/provider/runtime evidence"
    print(json.dumps({"counts": counts, "issues": issues}, ensure_ascii=False, indent=2))
    return 1 if issues else 0


if __name__ == "__main__":
    raise SystemExit(main())
