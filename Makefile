PYTHON ?= $(if $(wildcard .venv/bin/python),.venv/bin/python,python3)

# Only the opted-in path consumes leaf control statuses: 1 is an ordinary
# failed check; setup/cancellation stops the sequence. Raw child exits are logged
# by the leaf and are never used as these control statuses.
define keep-going-stages
	@result=0; for stage in $(1); do \
		$(PYTHON) -B tests/run_checks.py $$stage --keep-going; code=$$?; \
		case $$code in 0) ;; 1) result=1 ;; *) exit $$code ;; esac; \
	done; exit $$result
endef

ifeq ($(KEEP_GOING),1)
LEAF_FLAGS = --keep-going
endif

.PHONY: test-deps test test-prepare test-unit test-int test-e2e test-persistence

test:
ifeq ($(KEEP_GOING),1)
	$(call keep-going-stages,prepare unit integration persistence e2e)
else
	$(MAKE) test-prepare
	$(MAKE) test-unit
	$(MAKE) test-int
	$(MAKE) test-e2e
endif

# Explicit one-time dependency preparation; common test stages never install.
test-deps:
	test -x .venv/bin/python || python3 -m venv .venv
	.venv/bin/python -m pip install -r requirements-test.txt

test-prepare:
	$(PYTHON) -B tests/run_checks.py prepare $(LEAF_FLAGS)

test-unit:
	$(PYTHON) -B tests/run_checks.py unit $(LEAF_FLAGS)

test-int:
	$(PYTHON) -B tests/run_checks.py integration $(LEAF_FLAGS)

test-e2e:
ifeq ($(KEEP_GOING),1)
	$(call keep-going-stages,persistence e2e)
else
	$(MAKE) test-persistence
	$(PYTHON) -B tests/run_checks.py e2e
endif

# Additional internal-storage gate; its results are not black-box E2E evidence.
test-persistence:
	$(PYTHON) -B tests/run_checks.py persistence $(LEAF_FLAGS)
