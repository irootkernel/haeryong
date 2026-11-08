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


def test_scan_request_accepted_report_and_findings_page_shapes():
    u1 = "11111111-1111-4111-8111-111111111111"
    u2 = "22222222-2222-4222-8222-222222222222"
    c.validate("scan_request", {"scope": "selected_projects", "project_ids": [u1],
               "depth": "change_focused", "source_mode": "pinned"})
    c.validate("scan_request", {"scope": "all_allowed_projects", "depth": "full-scope",
               "source_mode": "current", "target_set_id": u2})
    for invalid in ({"scope": "unknown"},
                    {"depth": "invalid"},
                    {"source_mode": "live"},
                    {"path_prefix": "../escape"},
                    {"extra_field": True}):
        with pytest.raises(c.ContractError):
            c.validate("scan_request", invalid)

    c.validate("scan_accepted", {"operation_id": u2, "job_id": u1,
               "job_url": "/api/v1/jobs/" + u1, "coalesced": False, "target_set_id": None})
    with pytest.raises(c.ContractError):
        c.validate("scan_accepted", {"operation_id": u2, "job_id": u1, "job_url": "/api/v1/jobs/" + u1})

    report = {
        "job_id": u1,
        "stage": "complete",
        "enumeration_complete": True,
        "config_revision": 1,
        "permissions_revisions": {u1: 1},
        "requested_scope": {"scope": "selected_projects", "project_ids": [u1], "depth": "change_focused"},
        "effective_policy": {"scope": "selected_projects", "project_ids": [u1], "depth": "change_focused"},
        "captured": {
            "projects": 1,
            "documents": 1,
            "versions": 1,
            "pages": 2,
            "regions": 1,
            "entries": 2,
            "sources": 1,
            "indexes": 1,
            "excluded_documents": 0,
            "total_units": 7,
        },
        "structural": {
            "checked": 7,
            "checked_index_items": 1,
            "repaired": 0,
            "failed": 0,
            "anomalies_reported": 0,
            "excluded": 0,
            "blocked": 0,
            "skipped": 0,
            "regionless_versions": 0,
            "metadata_less_versions": 0,
        },
        "semantic": {
            "eligible": 0,
            "assessed": 0,
            "skipped": 0,
            "unresolved": 0,
            "complete": False,
            "status": "not_assessed",
            "reason": "semantic_assessment_not_run",
        },
        "inspected_candidate_relations": 0,
        "overlays": {"applied": 0, "proposed": 0, "rejected": 0},
        "drafts": 0,
        "outdated_units": 0,
        "affected_reviewed_versions": 0,
        "provider_attempts": 0,
        "findings": {
            "total": 1,
            "proposed": 1,
            "applied": 0,
            "rejected": 0,
            "stale": 0,
            "blocked": 0,
            "undone": 0,
        },
    }
    for stage in ("inventory", "inspect", "repair", "complete"):
        c.validate("scan_report", {**report, "stage": stage, "enumeration_complete": stage != "inventory"})
    for bad_stage in ("structural", "semantic", "unknown"):
        with pytest.raises(c.ContractError):
            c.validate("scan_report", {**report, "stage": bad_stage})
    with pytest.raises(c.ContractError):
        c.validate("scan_report", {**report, "captured": {"projects": 1}})

    finding = {
        "finding_id": u1,
        "job_id": u2,
        "kind": "backlink_index_repair",
        "origin": "deterministic",
        "project_id": u1,
        "document_id": u1,
        "version_id": u1,
        "page_path": "docs/index.html",
        "region_id": "sec-1",
        "target_set_id": None,
        "evidence": {"missing_edges": 1},
        "evidence_fingerprint": "fp-123",
        "rationale": "Reference index diverged from authored declarations.",
        "uncertainty": "low",
        "captured_config_revision": 1,
        "permissions_revision": 1,
        "proposal": {"action": "reindex_references", "version_id": u1},
        "status": "proposed",
        "approval_required": True,
        "reason": "index_divergence",
        "applied_ref": None,
        "revision": 1,
        "etag": '"finding-' + u1 + '-r1"',
        "created_at": "2026-11-08T00:00:00Z",
        "updated_at": "2026-11-08T00:00:00Z",
    }
    c.validate("finding_view", finding)
    c.validate("finding_page", {"items": [finding], "next_cursor": None})
    for bad in (
        {**finding, "origin": "semantic"},
        {**finding, "kind": "broken_reference"},
        {**finding, "status": "open"},
        {**finding, "private_path": "/secret"},
    ):
        with pytest.raises(c.ContractError):
            c.validate("finding_view", bad)
