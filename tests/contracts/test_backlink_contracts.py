"""BL closed response shapes; runtime authority/recovery are separate product checks."""
from copy import deepcopy
import pytest
import contract as c

pytestmark = pytest.mark.integration
ID = '11111111-1111-4111-8111-111111111111'


def test_backlinks_closed_counts_and_exact_page_local_endpoints():
    endpoint = dict(document_id=ID, version_id=ID, page_path='nested/한글.html', region_id='retry')
    row = dict(edge_id=ID, reference_id='local/ref', **{'from': endpoint}, target=endpoint,
               label=None, relation='analogy', reader_url=f'/documents/{ID}/versions/{ID}?page=nested%2F%ED%95%9C%EA%B8%80.html#region=retry')
    page = dict(scope=dict(version_id=ID, page_path=None, region_id=None), items=[row],
                counts=dict(edge_count='3', referring_document_count='1'), next_cursor=None)
    c.validate('backlink_page', page)
    for field, value in [('hidden_count', '1'), ('counts', dict(edge_count=3, referring_document_count='1'))]:
        with pytest.raises(c.ContractError):
            c.validate('backlink_page', dict(page, **{field: value}))
    invalid = deepcopy(row)
    invalid['from']['page_path'] = None
    with pytest.raises(c.ContractError):
        c.validate('backlink_view', invalid)


def test_index_validation_honest_states_and_closed_rebuild_kind():
    for state in ['consistent', 'inconsistent', 'incomplete']:
        c.validate('index_validation', dict(version_id=ID, kind='references', state=state,
                   counts=dict(checked='1', missing='0', unexpected='0', mismatched='0'), reasons=[]))
    pending = dict(version_id=ID, kind='references', state='incomplete',
                   counts=dict(checked='1', missing='0', unexpected='0', mismatched='0'),
                   reasons=['anchor_prerequisite_pending'])
    c.validate('index_validation', pending)
    with pytest.raises(c.ContractError):
        c.validate('index_validation', dict(pending, reasons=['private_target_diagnostic']))
    c.validate('index_rebuild_request', dict(kind='all'))
    with pytest.raises(c.ContractError):
        c.validate('index_rebuild_request', dict(kind='references', sql='DROP INDEX authored_edges_target'))
