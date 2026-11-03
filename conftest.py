"""Required tests cannot become a passing gate through skips or collection-only runs."""
import os
from pathlib import Path
import time

import pytest


def pytest_addoption(parser):
    parser.addoption("--haeryong-progress", type=Path,
                     help="Write test milestones for the canonical Make console")


def pytest_configure(config):
    config._haeryong_executed = set()
    config._haeryong_progress_index = 0
    config._haeryong_progress_stream = None
    path = config.getoption("haeryong_progress")
    if path is not None:
        stream = path.open("x", encoding="utf-8")
        config._haeryong_progress_stream = stream
        config.add_cleanup(stream.close)


def progress(config, message):
    stream = config._haeryong_progress_stream
    if stream is not None:
        stream.write(message + "\n")
        stream.flush()


def pytest_collection_finish(session):
    progress(session.config, f"PYTEST collected {len(session.items)} tests")


@pytest.hookimpl(hookwrapper=True)
def pytest_runtest_protocol(item, nextitem):
    config = item.config
    if config._haeryong_progress_stream is None:
        yield
        return
    config._haeryong_progress_index += 1
    position = f"{config._haeryong_progress_index}/{len(item.session.items)}"
    # Omit parameter values and all captured product output from console metadata.
    name = f"{item.location[0]}::{item.name.split('[', 1)[0]}"
    started = time.monotonic()
    item._haeryong_progress_result = "not-run"
    progress(config, f"PYTEST [{position}] START {name}")
    yield
    # The protocol includes fixture teardown, so a passed call alone is not PASS.
    progress(config, f"PYTEST [{position}] {item._haeryong_progress_result.upper()} "
                     f"{name}; elapsed={time.monotonic()-started:.1f}s")


def pytest_collection_modifyitems(items):
    for item in items:
        marks = [name for name in ("unit", "integration", "e2e") if item.get_closest_marker(name)]
        if len(marks) != 1:
            raise pytest.UsageError(f"Exactly one test layer is required: {item.nodeid}")


@pytest.hookimpl(hookwrapper=True)
def pytest_runtest_makereport(item, call):
    outcome = yield
    report = outcome.get_result()
    if report.when == "call" and report.passed:
        item.config._haeryong_executed.add(item.nodeid)
    if item.config._haeryong_progress_stream is not None:
        if report.failed:
            item._haeryong_progress_result = "failed"
        elif item._haeryong_progress_result != "failed":
            if hasattr(report, "wasxfail"):
                item._haeryong_progress_result = "xpassed" if report.passed else "xfailed"
            elif report.skipped:
                item._haeryong_progress_result = "skipped"
            elif report.when == "call":
                item._haeryong_progress_result = "passed"


def pytest_sessionfinish(session, exitstatus):
    reporter = session.config.pluginmanager.get_plugin("terminalreporter")
    if reporter and (reporter.stats.get("skipped") or reporter.stats.get("xfailed") or reporter.stats.get("xpassed")):
        session.exitstatus = pytest.ExitCode.TESTS_FAILED
    if os.environ.get("HAERYONG_REQUIRED_TEST_STAGE"):
        if session.config.option.collectonly or not session.items or len(session.config._haeryong_executed) != len(session.items):
            session.exitstatus = pytest.ExitCode.TESTS_FAILED
