"""Required tests cannot become a passing gate through skips or collection-only runs."""
import os

import pytest


def pytest_configure(config):
    config._haeryong_executed = set()


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


def pytest_sessionfinish(session, exitstatus):
    reporter = session.config.pluginmanager.get_plugin("terminalreporter")
    if reporter and (reporter.stats.get("skipped") or reporter.stats.get("xfailed") or reporter.stats.get("xpassed")):
        session.exitstatus = pytest.ExitCode.TESTS_FAILED
    if os.environ.get("HAERYONG_REQUIRED_TEST_STAGE"):
        if session.config.option.collectonly or not session.items or len(session.config._haeryong_executed) != len(session.items):
            session.exitstatus = pytest.ExitCode.TESTS_FAILED
