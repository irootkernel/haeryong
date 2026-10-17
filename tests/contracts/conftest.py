"""Keep selected contract coverage and source-bound reports with native pytest."""
import hashlib
import json
import os
from pathlib import Path

import pytest

HERE = Path(__file__).parent


def pytest_addoption(parser):
    parser.addoption("--contract-report", help="New absolute private selected-contract JSON report")


def pytest_configure(config):
    config._haeryong_contract_outcomes = {}
    value = config.getoption("--contract-report") or os.environ.get("HAERYONG_CONTRACT_REPORT")
    if value:
        path = Path(value)
        if not path.is_absolute() or path.exists() or any(p.is_symlink() for p in [path, *path.parents]):
            raise pytest.UsageError("Contract report must be a new absolute non-symlink private path")
    config._haeryong_contract_report = value


def pytest_collection_finish(session):
    ids = [item.nodeid for item in session.items if Path(str(item.path)).parent == HERE]
    if not ids:
        raise pytest.UsageError("Selected contract suite collected no tests")
    matrix = json.loads((HERE / "cases.json").read_text())
    for row in matrix["cases"] + matrix["supplemental_groups"]:
        if sum(row["test_prefix"] in name for name in ids) != 1:
            raise pytest.UsageError("Selected contract case/group is missing or ambiguous: " + row["id"])
    session.config._haeryong_contract_ids = ids


@pytest.hookimpl(hookwrapper=True)
def pytest_runtest_makereport(item, call):
    outcome = yield
    report = outcome.get_result()
    if Path(str(item.path)).parent != HERE:
        return
    results = item.config._haeryong_contract_outcomes
    if report.failed or report.skipped:
        results[item.nodeid] = {"result": "fail" if report.failed else "skip", "detail": str(report.longrepr)}
    elif report.when == "call" and item.nodeid not in results:
        results[item.nodeid] = {"result": "pass"}


def pytest_sessionfinish(session, exitstatus):
    config = session.config
    value = config._haeryong_contract_report
    if not value:
        return
    ids = getattr(config, "_haeryong_contract_ids", [])
    outcomes = config._haeryong_contract_outcomes
    skipped = [(name, row["detail"]) for name, row in outcomes.items() if row["result"] == "skip"]
    success = bool(ids) and len(outcomes) == len(ids) and not skipped and all(row["result"] == "pass" for row in outcomes.values())
    report = {"kind": "Executed pure contract fixture/model tests; no production service/durability/provider claim",
              "tests_run": len(outcomes), "success": success,
              "tests": [{"test": name, "result": outcomes.get(name, {"result": "not_run"})["result"]} for name in ids],
              "failures": {name: row["detail"] for name, row in outcomes.items() if row["result"] == "fail"},
              "scope": json.loads((HERE / "cases.json").read_text()),
              "source_hashes": {path.name: hashlib.sha256(path.read_bytes()).hexdigest()
                                for path in sorted(HERE.iterdir()) if path.suffix in {".py", ".json"}},
              "skipped": skipped}
    with Path(value).open("x") as stream:
        os.chmod(value, 0o600)
        json.dump(report, stream, ensure_ascii=False, indent=2)
        stream.write("\n")
    if not success:
        session.exitstatus = pytest.ExitCode.TESTS_FAILED
