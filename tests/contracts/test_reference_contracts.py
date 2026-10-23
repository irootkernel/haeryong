"""RF01/RF08 closed metadata and reference wire fixtures; product evidence is separate."""
from copy import deepcopy
import json
from pathlib import Path
import pytest
import contract as c

pytestmark = pytest.mark.integration


def test_reference_RF01_optional_fields_closed_from_and_legacy():
    # RV-FIELDS/RV-CLOSED: optional fields, required nested keys, null/unknown rules.
    legacy = {'id': '../참조/%2F', 'document_id': '11111111-1111-4111-8111-111111111111',
              'version_id': '22222222-2222-4222-8222-222222222222',
              'page_path': None, 'region_id': None, 'resolution': 'unresolved'}
    c.metadata({'references': [legacy]}, [])
    enriched = dict(legacy, page_path='설명/cafe\u0301 %2F +.html', region_id='retry',
                    **{'from': {'page_path': 'source.html', 'region_id': 'retry'},
                       'relation': 'explanatory analogy', 'page_query': 'q=한글 +%2F',
                       'page_fragment': 'artifact#fragment'})
    assert c.metadata({'references': [enriched]}, [])['references'][0] == enriched
    c.metadata({'references': [dict(enriched, **{'from': {'page_path': 'source.html', 'region_id': None}})]}, [])
    for key in ('from', 'relation', 'page_query', 'page_fragment'):
        with pytest.raises(c.ContractError):
            c.metadata({'references': [dict(enriched, **{key: None})]}, [])
    for source in ({}, {'page_path': 'p'}, {'page_path': None, 'region_id': None},
                   {'page_path': '../p', 'region_id': 'r'}, {'page_path': 'p', 'region_id': ''},
                   {'page_path': 'p', 'region_id': None, 'latest': True}):
        with pytest.raises(c.ContractError):
            c.metadata({'references': [dict(enriched, **{'from': source})]}, [])
    for bad in (dict(enriched, latest=True), dict(legacy, region_id='r')):
        with pytest.raises(c.ContractError):
            c.metadata({'references': [bad]}, [])
    with pytest.raises(c.ContractError):
        c.metadata({'references': [legacy, deepcopy(legacy)]}, [])


def test_reference_RF01_embedded_definitions_equal_selected_contract():
    # RV-SCHEMA-PARITY: an embedded-only or machine-only field breaks equality.
    repo = Path(__file__).resolve().parents[2]
    machine = json.loads((repo / 'tests/contracts/schema.json').read_text())['$defs']
    for source in ('internal/application/import/metadata-schema.json',
                   'internal/application/configuration/schema.json'):
        embedded = json.loads((repo / source).read_text())
        definitions = embedded.get('$defs', embedded)
        for name, definition in definitions.items():
            assert definition == machine[name], (source, name)


def test_reference_RF08_provisional_response_shapes_never_validate_hidden_edges():
    """Pure wire examples/negatives; actual authority assertions live in product specs."""
    identifier = '11111111-1111-4111-8111-111111111111'
    target = {'document_id': identifier, 'version_id': identifier, 'page_path': 'a.html',
              'region_id': 'retry', 'requested_path': 'a.html', 'resolved_path': 'a.html',
              'page_query': 'q=+%2F', 'page_fragment': 'artifact'}
    row = {'id': 'r', 'from': {'page_path': None, 'region_id': None}, 'relation': None,
           'label': None, 'resolution': 'resolved', 'reason': None, 'edge_id': identifier,
           'target': target, 'reader_url': f'/documents/{identifier}/versions/{identifier}?page=a.html#region=retry'}
    c.validate('reference_resolution_response', {'data': row, 'request_id': 'fixture'})
    unavailable = dict(row, resolution='unavailable', reason='target_unavailable',
                       target=None, edge_id=None, reader_url=None)
    gone = dict(row, resolution='tombstoned', target={'document_id': identifier, 'version_id': identifier},
                edge_id=None, reader_url=None)
    unresolved = dict(row, resolution='unresolved', reason='resolution_pending', edge_id=None, reader_url=None)
    page = {'counts': {'edge_count': '1', 'referenced_document_count': '1'}, 'items': [row, unavailable, gone, unresolved], 'next_cursor': None,
            'materialization': {'state': 'ready', 'reason': None}}
    c.validate('reference_response', {'data': page, 'request_id': 'fixture'})
    fallback = dict(unresolved, reader_url=f'/documents/{identifier}/versions/{identifier}?page=a.html')
    c.validate('reference_view', fallback)
    for state in ('pending', 'failed'):
        c.validate('reference_page', dict(page, items=[], materialization={
            'state': state, 'reason': 'materialization_' + state}))
    for invalid in (dict(unavailable, edge_id=identifier), dict(unavailable, target=target),
                    dict(unavailable, label='Hidden canary'), dict(gone, target=target),
                    dict(row, reader_url='https://hidden.example/path'),
                    dict(fallback, reader_url='//hidden.example/path'),
                    dict(fallback, edge_id=identifier),
                    dict(fallback, target=None),
                    dict(fallback, target=dict(target, resolved_path=None)),
                    dict(gone, target=dict(gone['target'], title='retained private title')), dict(row, edge_id=None),
                    dict(row, **{'from': {'page_path': None, 'region_id': 'retry'}}),
                    dict(row, target=dict(target, page_path=None)), dict(row, hidden_count='1')):
        with pytest.raises(c.ContractError):
            c.validate('reference_view', invalid)
