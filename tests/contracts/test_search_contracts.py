"""SC08/SC11 selected additive search shapes; no implemented search endpoint claim."""
from copy import deepcopy
import pytest
import contract as c

pytestmark = pytest.mark.integration


def test_search_SC08_closed_fields_and_required_safe_reasons():
    # Reversal: schema search_index.required/reasons and closed nested objects.
    counts = {k: '0' for k in ('versions_total', 'versions_complete', 'files_total',
                             'files_discovered', 'html_total', 'html_complete',
                             'html_pending', 'html_failed', 'unsupported_bodies')}
    index = dict(counts, state='partial', discovery_complete=False,
                 limitations=['static_only'], reasons=[{'code': 'read_failed', 'version_count': '1'}])
    page = {'query': {'text': '', 'normalized': '', 'mode': 'empty'},
            'filters': {'project_id': None, 'lifecycle': 'active', 'versions': 'latest', 'kind': None},
            'items': [], 'next_cursor': None, 'total_matches': '0', 'index': index}
    c.validate('search_response', {'data': page, 'request_id': 'fixture'})
    identifier = '11111111-1111-4111-8111-111111111111'
    hit = {'kind': 'region', 'project_id': identifier, 'document_id': identifier,
           'version_id': identifier, 'sequence': 1, 'title': 'Supplied title',
           'lifecycle': 'active', 'page_path': 'pages/cafe\u0301.html', 'region_id': 'retry',
           'source_id': None, 'anchor_state': 'present', 'body_state': 'complete',
           'match_fields': ['region', 'body'], 'snippet': 'Plain readSource'}
    c.validate('search_hit', hit)
    c.validate('search_page', {**page, 'items': [hit], 'total_matches': '1'})
    for field in hit:
        missing = dict(hit)
        del missing[field]
        with pytest.raises(c.ContractError):
            c.validate('search_hit', missing)
    for invalid_hit in ({**hit, 'private_path': 'forbidden'}, {**hit, 'project_id': None},
                        {**hit, 'kind': 'topic'}, {**hit, 'anchor_state': 'resolved'},
                        {**hit, 'body_state': 'truncated'}, {**hit, 'match_fields': ['sql']},
                        {**hit, 'snippet': 5}, {**hit, 'sequence': 0}):
        with pytest.raises(c.ContractError):
            c.validate('search_hit', invalid_hit)
    for invalid_response in ({'data': page}, {'data': page, 'request_id': 'fixture', 'extra': True}):
        with pytest.raises(c.ContractError):
            c.validate('search_response', invalid_response)
    for field in page['filters']:
        missing = dict(page['filters'])
        del missing[field]
        with pytest.raises(c.ContractError):
            c.validate('search_filters', missing)
    for reasons in ([], [{'code': 'not_indexed', 'version_count': '2'},
                         {'code': 'unsupported_body', 'version_count': '1'}]):
        c.validate('search_index', {**index, 'reasons': reasons})
    invalid = [dict(page, surprise=True), dict(page, total_matches=0),
               dict(page, filters={**page['filters'], 'search.read': True})]
    for key in index:
        missing = deepcopy(page)
        del missing['index'][key]
        invalid.append(missing)
    for reasons in (None, [{'code': 'raw_io_error', 'version_count': '1'}],
                    [{'code': 'read_failed', 'version_count': '0'}],
                    [{'code': 'read_failed', 'version_count': '01'}],
                    [{'code': 'read_failed', 'version_count': 1}],
                    [{'code': 'read_failed', 'version_count': '1', 'job_id': 'private'}],
                    [{'code': 'read_failed'}]):
        invalid.append(dict(page, index={**index, 'reasons': reasons}))
    for field in counts:
        invalid.append(dict(page, index={**index, field: 0}))
        invalid.append(dict(page, index={**index, field: '01'}))
    for value in invalid:
        with pytest.raises(c.ContractError):
            c.validate('search_page', value)
    c.validate('search_query', {'q': '', 'lifecycle': 'all', 'versions': 'all', 'limit': 200})
    c.validate('search_query', {})
    for query in ({'q': None}, {'q': 'x' * 4097}, {'limit': 0}, {'limit': 201},
                  {'kind': 'topic'}, {'lifecycle': 'trash'}, {'versions': 'oldest'},
                  {'cursor': ''}, {'extra': 'x'}):
        with pytest.raises(c.ContractError):
            c.validate('search_query', query)
    for reason in c.SCHEMA['$defs']['search_error_details']['properties']['reason']['enum']:
        c.validate('search_error_details', {'reason': reason})


def test_search_SC11_additive_kind_preserves_existing_scopes():
    # Reversal: remove derived_index from the registry's closed selected enum.
    kinds = c.SCHEMA['shared_enums']['job.kind']
    assert 'derived_index' in kinds
    # TASK-005-05 adds offline restore; all nine existing kinds stay selected.
    assert len(kinds) == len(set(kinds)) == 10
    assert set(kinds) == {'upload_seal', 'import', 'generation', 'question',
                          'scan', 'lifecycle', 'backup', 'export', 'derived_index', 'restore'}
    assert 'future_kind' not in kinds
    assert c.SCHEMA['shared_enums']['workflow.scope'] == [
        'project.read', 'source.read', 'version.read', 'package.export',
        'context.write', 'import.publish', 'generation.request', 'draft.write',
        'draft.publish', 'scan.request', 'job.read', 'job.cancel']
