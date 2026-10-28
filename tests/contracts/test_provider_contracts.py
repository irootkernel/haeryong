"""Closed job resume intent; runtime fencing is tested in the Go persistence suite."""
import pytest
import contract as c

pytestmark = pytest.mark.integration


def test_provider_resume_requires_explicit_decision_and_generation():
    for decision in ("reconcile_only", "continue", "retry"):
        c.validate("job_resume", {"decision": decision, "expected_attempt_generation": 7})
    for invalid in ({"decision": "retry"},
                    {"decision": "automatic", "expected_attempt_generation": 1},
                    {"decision": "retry", "expected_attempt_generation": 0},
                    {"decision": "retry", "expected_attempt_generation": True},
                    {"decision": "retry", "expected_attempt_generation": 1, "clear_cancel": True}):
        with pytest.raises(c.ContractError):
            c.validate("job_resume", invalid)


def test_provider_summary_accounts_older_uncertainty_without_native_identity():
    c.validate("provider_attempt_summary", {"count": 3, "unknown": 2,
               "latest_state": "result_saved", "reconciliation_pending": 2,
               "unknown_outcome_reason": "outcome_unknown"})
    c.validate("provider_attempt_summary", {"count": 0, "unknown": 0,
               "reconciliation_pending": 0})
    for invalid in ({"count": 1, "unknown": True, "reconciliation_pending": 0},
                    {"count": 1, "unknown": 1, "reconciliation_pending": -1},
                    {"count": 1, "unknown": 1, "reconciliation_pending": 0,
                     "provider_request_id": "must-stay-private"}):
        with pytest.raises(c.ContractError):
            c.validate("provider_attempt_summary", invalid)
