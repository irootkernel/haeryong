"""Selected closed additive note/Revisit shapes, including B-1 stream identity."""
import json
from pathlib import Path
import pytest
import contract as c

pytestmark = pytest.mark.integration
ID = '11111111-1111-4111-8111-111111111111'


def test_annotation_anchor_and_edit_reattach_are_distinct_closed_intents():
    anchor = dict(version_id=ID, page_path='pages/한글.html', region_id='retry', page_query='q=+%2F', page_fragment='part')
    c.validate('annotation_anchor', anchor)
    c.validate('annotation_anchor', anchor | dict(page_path=None, region_id=None, page_query='', page_fragment=''))
    for invalid in [dict(page_path=None), dict(region_id=''), dict(page_path=None, region_id=None)]:
        with pytest.raises(c.ContractError):
            c.validate('annotation_anchor', anchor | invalid)
    content = dict(body='**note**', body_ref=None, selected_text=None, context=None, context_ref=None)
    create = {k: v for k, v in anchor.items() if k != 'version_id'} | content
    c.validate('annotation_create', create)
    c.validate('annotation_create', create | dict(context=3.14))
    c.validate('annotation_create', create | dict(context=10**1000))
    c.validate('annotation_create', create | dict(body=None, body_ref=dict(upload_id=ID)))
    c.validate('annotation_patch', dict(action='edit', **content))
    c.validate('annotation_patch', dict(action='reattach', anchor=anchor, selected_text=None, context=None, context_ref=ID))
    for invalid in [dict(action='edit', anchor=anchor, **content), dict(action='reattach', **content), dict(action='edit', **(content | dict(body_ref=dict(upload_id=ID))))]:
        with pytest.raises(c.ContractError):
            c.validate('annotation_patch', invalid)
    with pytest.raises(c.ContractError):
        c.validate('annotation_create', create | dict(context={'scenario': True}, context_ref=ID))
    c.validate('annotation_stream_request', dict(annotation_id=ID, revision=2))
    for invalid in [dict(annotation_id=ID, revision=0), dict(annotation_id=ID, revision=2, context_ref=ID)]:
        with pytest.raises(c.ContractError):
            c.validate('annotation_stream_request', invalid)
    c.validate('annotation_page', dict(items=[], next_cursor=None))


def test_revisit_absent_and_stored_false_are_distinct():
    absent = dict(document_id=ID, exists=False, value=dict(flag=False), revision=None, etag=None, page_position_url=None, required_precondition='if_none_match_star')
    c.validate('revisit_view', absent)
    stored = absent | dict(exists=True, revision=1, etag='"individual"')
    del stored['required_precondition']
    c.validate('revisit_view', stored)
    for invalid in [absent | dict(value=dict(flag=True)), stored | dict(required_precondition='if_none_match_star'), absent | dict(etag='"guessed"')]:
        with pytest.raises(c.ContractError):
            c.validate('revisit_view', invalid)
    for value in [True, False]:
        c.validate('revisit_request', dict(flag=value))
    with pytest.raises(c.ContractError):
        c.validate('revisit_request', dict(flag=None))


def test_note_conflict_fixture_keeps_server_revision_and_local_draft_separate():
    fixture = json.loads((Path(__file__).resolve().parents[1] / 'fixtures/annotations/conflict.json').read_text())
    c.validate('annotation_view', fixture['base'])
    c.validate('annotation_view', fixture['server'])
    c.validate('annotation_patch', fixture['local_patch'])
    assert fixture['base']['annotation_id'] == fixture['server']['annotation_id']
    assert fixture['base']['etag'] != fixture['server']['etag']
    assert fixture['server']['revision'] == fixture['base']['revision'] + 1
    assert fixture['expected_status'] == 412
    assert fixture['local_patch']['body'] == fixture['expected_preserved_draft']
