"""Closed Review additions; runtime owner authority/atomicity use product tests."""
from copy import deepcopy
import pytest
import contract as c

pytestmark = pytest.mark.integration
ID = '11111111-1111-4111-8111-111111111111'


def test_review_closed_request_absent_and_existing_values():
    for action, state in [('start', 'reviewing'), ('complete', 'reviewed'), ('reopen', 'reviewing'), ('clear', 'unreviewed')]:
        c.validate('review_request', dict(action=action, state=state))
    absent = dict(version_id=ID, exists=False, value=dict(state='unreviewed', reviewed_at=None),
                  revision=None, etag=None, page_position_url=None, required_precondition='if_none_match_star')
    c.validate('review_view', absent)
    for change in [dict(etag='"guessed"'), dict(revision=0), dict(value=dict(state='reviewed', reviewed_at=None)), dict(page_path='a.html')]:
        with pytest.raises(c.ContractError):
            c.validate('review_view', absent | change)
    stored = absent | dict(exists=True, revision=1, etag='"opaque"', value=dict(state='reviewed', reviewed_at='2026-10-24T07:00:00Z'))
    del stored['required_precondition']
    c.validate('review_view', stored)
    with pytest.raises(c.ContractError):
        c.validate('review_view', stored | dict(required_precondition='if_none_match_star'))
    for request in [dict(action='scroll', state='reviewed'), dict(action='start', state='reviewed'), dict(action='complete', state='reviewed', page_path='a.html')]:
        with pytest.raises(c.ContractError):
            c.validate('review_request', request)


def test_review_history_is_exact_version_and_no_page_progress():
    row = dict(event_id=ID, version_id=ID, action='complete', old_state='unreviewed', new_state='reviewed', actor_session=ID, operation_id=ID, created_at='2026-10-24T07:00:00Z')
    page = dict(items=[row], next_cursor=None)
    c.validate('review_history_page', page)
    invalid = deepcopy(page)
    invalid['items'][0]['page_path'] = 'other.html'
    with pytest.raises(c.ContractError):
        c.validate('review_history_page', invalid)
