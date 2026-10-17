PYTHON ?= $(if $(wildcard .venv/bin/python),.venv/bin/python,python3)

.PHONY: test-deps test test-prepare test-unit test-int test-e2e test-persistence

test:
	$(MAKE) test-prepare
	$(MAKE) test-unit
	$(MAKE) test-int
	$(MAKE) test-e2e

# Explicit one-time dependency preparation; common test stages never install.
test-deps:
	test -x .venv/bin/python || python3 -m venv .venv
	.venv/bin/python -m pip install -r requirements-test.txt

test-prepare:
	$(PYTHON) -B tests/run_checks.py prepare

test-unit:
	$(PYTHON) -B tests/run_checks.py unit

test-int:
	$(PYTHON) -B tests/run_checks.py integration

test-e2e:
	$(MAKE) test-persistence
	$(PYTHON) -B tests/run_checks.py e2e

# Additional internal-storage gate; its results are not black-box E2E evidence.
test-persistence:
	$(PYTHON) -B tests/run_checks.py persistence
