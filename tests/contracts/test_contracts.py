"""C1–C5 and supplemental behavior contracts; no production claim."""
from copy import deepcopy
from pathlib import Path
import io
import json
import re
import pytest

pytestmark = pytest.mark.integration
import zipfile
import contract as c
P = '11111111-1111-4111-8111-111111111111'
D = '22222222-2222-4222-8222-222222222222'
V = '33333333-3333-4333-8333-333333333333'
R = '44444444-4444-4444-8444-444444444444'
B = '55555555-5555-4555-8555-555555555555'
OTHER = '66666666-6666-4666-8666-666666666666'

def key(number):
    return f'00000000-0000-4000-8000-{number:012x}'

def checkpoint(raw=b'complete original bytes', generation=1):
    return {'generation': generation, 'inventory_sha256': c.digest(raw), 'accepted_bytes': str(len(raw)), 'complete_input': True}

class TestContracts:

    def test_Task00205_selected_query_lists_and_staging_accounting(self):
        project = {'project_id': P, 'name': 'Unfiled', 'lifecycle': 'active', 'revision': 1, 'permissions_revision': 1, 'updated_at': '2026-10-16T20:00:00.000000000Z', 'etag': '"opaque-project"'}
        document = {'document_id': D, 'project_id': P, 'title': 'ordinary', 'lifecycle': 'active', 'latest_version_id': V, 'revision': 1, 'updated_at': project['updated_at'], 'versions_url': '/api/v1/documents/' + D + '/versions', 'etag': '"opaque-document"'}
        version = {'version_id': V, 'document_id': D, 'sequence': 1, 'base_version_id': None, 'title_snapshot': 'ordinary', 'default_page': None, 'viewer_profile': 'html_js_v1', 'source_basis': 'unverified', 'payload_sha256': 'a' * 64, 'manifest_sha256': 'b' * 64, 'supplied_metadata_sha256': None, 'manifest_url': '/api/v1/versions/' + V + '/metadata', 'supplied_metadata_url': None, 'files_url': '/api/v1/versions/' + V + '/files', 'render_bootstrap_url': '/api/v1/versions/' + V + '/render-bootstrap', 'etag': '"opaque-version"'}
        file = {'path': 'literal\\u2028.html', 'kind': 'file', 'byte_size': '41943040', 'bytes_sha256': 'c' * 64, 'media_type': 'application/octet-stream', 'preview': 'download', 'file_url': '/api/v1/versions/' + V + '/file?path=literal', 'etag': '"opaque-file"'}
        for name, item in [('query_project', project), ('query_document', document), ('query_version', version), ('query_file', file)]:
            c.validate(name, item)
            c.validate(name + '_list', {'items': [item], 'next_cursor': None})
            with pytest.raises(c.ContractError):
                c.validate(name + '_list', [item])
            invalid = dict(item)
            del invalid['etag']
            with pytest.raises(c.ContractError):
                c.validate(name, invalid)
        c.validate('staging_summary', {'staging_bytes': '0', 'pending_upload_count': 0, 'pending_uploads_url': '/api/v1/uploads'})
        with pytest.raises(c.ContractError):
            c.validate('staging_summary', {'staging_bytes': 0, 'pending_upload_count': 0, 'pending_uploads_url': '/api/v1/uploads'})

    def test_Task00303RendererBootstrapDeliveryAndRemoteReason(self):
        base = {'version_id': V, 'viewer_profile': 'html_js_v1', 'default_page': None, 'selected_page': 'page.html', 'requested_page': 'page.html', 'navigation_path': None, 'files_url': '/api/v1/versions/' + V + '/files', 'selected_file_url': '/api/v1/versions/' + V + '/file?path=page.html'}
        available = {**base, 'delivery': 'loopback_renderer_origin', 'renderer_available': True, 'render_sessions_url': '/api/v1/versions/' + V + '/render-sessions', 'renderer_construction': 'loopback_renderer_origin'}
        private_remote = {**base, 'delivery': 'private_renderer_origin', 'renderer_available': True, 'render_sessions_url': '/api/v1/versions/' + V + '/render-sessions', 'renderer_construction': 'private_https_renderer_origin'}
        remote = {**base, 'selected_file_url': None, 'delivery': 'authenticated_attachment', 'renderer_available': False, 'render_sessions_url': '', 'renderer_construction': '', 'renderer_unavailable_reason': 'renderer_remote_ingress'}
        unavailable = {**remote}
        del unavailable['renderer_unavailable_reason']
        for item in (available, private_remote, remote, unavailable):
            c.validate('render_bootstrap', item)
        for invalid in (
            {**available, 'delivery': 'untrusted_proxy'},
            {**remote, 'renderer_unavailable_reason': 7},
            {**remote, 'renderer_available': 'yes'},
            {**remote, 'phantom_feature': True},
            {**available, 'render_sessions_url': None},
        ):
            with pytest.raises(c.ContractError):
                c.validate('render_bootstrap', invalid)

    def test_Task00204_upload_validators_and_end_input_declaration(self):
        upload = self.model.create(key(701), {'purpose': 'result', 'target': {'project_id': P, 'document_id': None}, 'input_mode': 'directory'})
        before = self.model.summary(upload)
        confirmed = {'generation': 4, 'inventory_sha256': c.digest(b'ledger'), 'accepted_bytes': '1048576', 'complete_input': False}
        end_input = dict(confirmed, generation=confirmed['generation'] + 1, complete_input=True)
        c.validate('checkpoint', confirmed)
        c.validate('checkpoint', end_input)
        assert not confirmed['complete_input']
        assert end_input['accepted_bytes'] == confirmed['accepted_bytes']
        assert end_input['inventory_sha256'] == confirmed['inventory_sha256']
        assert before['revision'] == 1
        for field in ('revision', 'etag'):
            invalid = dict(before)
            del invalid[field]
            with pytest.raises(c.ContractError):
                c.validate('upload_summary', invalid)
        self.model.abandon(upload)
        after = self.model.summary(upload)
        assert after['revision'] > before['revision']
        assert after['etag'] != before['etag']

    def test_Task00203_selected_auth_control_shapes(self):
        c.validate('pairing_create', {'device_label': 'Remembered 기기'})
        c.validate('pairing_poll', {'polling_secret': 'a' * 64})
        c.validate('pairing_decision', {'code': 'comparison', 'origin': 'http://127.0.0.1:9270'})
        for body in ({}, {'code': None, 'origin': 'http://127.0.0.1:9270'}, {'code': 'x', 'origin': 'null'}, {'code': 'x', 'origin': 'http://127.0.0.1:9270', 'expected_state': 'pending'}):
            with pytest.raises(c.ContractError):
                c.validate('pairing_decision', body)
        with pytest.raises(c.ContractError):
            c.strict_json('{"code":"a","code":"b","origin":"http://127.0.0.1:9270"}')
        with pytest.raises(c.ContractError):
            c.validate('pairing_create', {'device_label': 'x' * 129})
        with pytest.raises(c.ContractError):
            c.validate('pairing_poll', {'polling_secret': None})
        c.validate('pairing_list', {'items': [], 'next_cursor': None})
        c.validate('device_session_list', {'items': [], 'next_cursor': None})
        for shape in ('pairing_list', 'device_session_list'):
            with pytest.raises(c.ContractError):
                c.validate(shape, [])
            with pytest.raises(c.ContractError):
                c.validate(shape, {'items': []})
        assert c.ERROR_HTTP['SESSION_REVOKED'] == 401
        assert c.ERROR_HTTP['SERVICE_RECOVERING'] == 503

    def test_Task00401_project_registration_and_recorded_sources(self):
        c.validate('project_register_request', {'name': '기억된 프로젝트'})
        self.error('INVALID_REQUEST', c.validate, 'project_register_request', {'name': 'x', 'root_path': '/private'})
        project = {'project_id': P, 'name': 'Source', 'lifecycle': 'active', 'revision': 1, 'permissions_revision': 1, 'updated_at': '2026-10-21T00:00:00Z', 'etag': '"project"'}
        c.validate('project_register_response', project)
        source = {'source_id': 'logical-source', 'kind': 'git', 'repo_id': P, 'retention_mode': 'reference_only', 'provider_ids': [], 'last_observed_revision': None, 'last_observed_at': None, 'last_availability': None, 'last_observation_reason': None}
        c.validate('project_sources_view', {'items': [source], 'next_cursor': None})
        observed = {**source, 'last_observed_revision': 'a' * 40, 'last_observed_at': project['updated_at'], 'last_availability': 'unavailable', 'last_observation_reason': 'missing_source'}
        c.validate('project_sources_view', {'items': [observed], 'next_cursor': 'opaque'})
        self.error('INVALID_REQUEST', c.validate, 'project_source_row', {**source, 'root_path': '/private'})
        for field in source:
            bad = dict(source)
            del bad[field]
            self.error('INVALID_REQUEST', c.validate, 'project_source_row', bad)

    def test_Task00401_verification_requests_results_and_pending(self):
        for body in ({}, {'expected_bytes_sha256': 'a' * 64}, {'expected_excerpt': '정확한 bytes\r\n'}, {'expected_bytes_sha256': 'a' * 64, 'expected_excerpt_ref': {'upload_id': R}}):
            c.validate('evidence_verification_request', body)
        for body in (None, {'expected_excerpt': None}, {'expected_excerpt_ref': R}, {'expected_excerpt_ref': {'upload_id': 'bad'}}, {'expected_excerpt': 'x', 'expected_excerpt_ref': {'upload_id': R}}, {'unknown': True}):
            self.error('INVALID_REQUEST', c.validate, 'evidence_verification_request', body)
        stamp = '2026-10-21T00:00:00Z'
        result = {'operation_id': R, 'verification_id': D, 'version_id': V, 'evidence_id': '../한글/%2e', 'outcome': 'matched', 'target_set_id': B, 'source': {'id': 'authored', 'kind': 'git', 'retention_mode': 'reference_only', 'repo_id': P, 'object_format': 'sha1', 'exact_revision': 'a' * 40}, 'location': {'path': 'retry.go', 'line_start': 1, 'line_end': 2}, 'bytes_sha256': 'b' * 64, 'bytes_origin': 'approved_source', 'checked_at': stamp, 'verified_at': stamp, 'reason': None}
        c.validate('evidence_verification_response', result)
        c.validate('evidence_verification_response', {**result, 'outcome': 'unavailable', 'target_set_id': None, 'bytes_sha256': None, 'bytes_origin': None, 'verified_at': None, 'reason': 'revision_unavailable'})
        self.error('INVALID_ENUM', c.validate, 'evidence_verification_response', {**result, 'bytes_origin': 'working_copy'})
        for reason in (None, 'missing_source'):
            row = {'verification_id': D, 'outcome': 'unavailable', 'checked_at': stamp, 'verified_at': None}
            if reason:
                row['reason'] = reason
            c.validate('verification_history_page', {'items': [row], 'next_cursor': None})
        pending = {'operation_id': R, 'state': 'accepted', 'operation_url': '/api/v1/operations/' + R}
        c.validate('source_check_pending', pending)
        self.error('INVALID_REQUEST', c.validate, 'source_check_pending', {**pending, 'operation_id': 'replacement'})

    def test_Task00401_observation_request_and_five_field_scope(self):
        for body in ({}, {'source_ids': []}, {'source_ids': ['logical-source'], 'scope': {'kind': 'version'}}, {'scope': {'kind': 'page', 'page_path': '한글.html'}}, {'scope': {'kind': 'region', 'page_path': '한글.html', 'region_id': 'same'}}):
            c.validate('source_observation_request', body)
        for body in (None, [], {'scope': None}, {'scope': {'kind': 'page'}}, {'scope': {'kind': 'version', 'page_path': None}}, {'scope': {'kind': 'region', 'page_path': 'a.html'}}, {'scope': {'kind': 'version', 'declared': True}}, {'source_ids': [None]}, {'extra': True}):
            with pytest.raises(c.ContractError):
                c.validate('source_observation_request', body)
        scope = {'kind': 'region', 'page_path': 'a.html', 'region_id': 'undeclared', 'declared': False, 'reason': 'region_not_declared'}
        c.validate('source_observation_scope', scope)
        self.error('INVALID_REQUEST', c.validate, 'scope', scope)
        frozen = {key: scope[key] for key in ('kind', 'page_path', 'region_id')}
        c.validate('scope', frozen)
        self.error('INVALID_REQUEST', c.validate, 'source_observation_scope', frozen)
        row = {'source_id': 'logical-source', 'observation_id': D, 'availability': 'unavailable', 'classification': 'unavailable', 'reason': 'missing_source', 'changed_paths': [], 'dependency_scope': [], 'reassessment_required': False}
        result = {'operation_id': R, 'run_id': B, 'target_set_id': None, 'captured_at': None, 'observed_at': '2026-10-21T00:00:00Z', 'scope': scope, 'coverage_set_id': P, 'sources': [row]}
        c.validate('source_observation_response', result)
        for field in ('reason', 'changed_paths', 'dependency_scope'):
            bad = dict(row)
            del bad[field]
            self.error('INVALID_REQUEST', c.validate, 'source_observation_row', bad)

    def test_Task00401_attempt_attributed_coverage_and_assessments_page(self):
        query = {'coverage_set_id': P, 'attempt_run_id': R, 'cursor': 'opaque', 'limit': 200}
        c.validate('observation_coverage_query', query)
        c.validate('observation_coverage_query', {'coverage_set_id': P})
        for bad in ({'attempt_run_id': R}, {**query, 'attempt_run_id': 'not-uuid'}, {**query, 'limit': 201}, {**query, 'extra': True}):
            self.error('INVALID_REQUEST', c.validate, 'observation_coverage_query', bad)
        unit = {'kind': 'page', 'page_path': 'a.html', 'region_id': None, 'outcome': 'not_checked', 'reason': 'missing_source', 'discrepancy': None, 'assessment_id': None, 'target_set_id': B}
        c.validate('observation_coverage_page', {'items': [unit], 'next_cursor': 'next'})
        c.validate('observation_coverage_unit_row', {**unit, 'target_set_id': None})
        self.error('INVALID_ENUM', c.validate, 'observation_coverage_unit_row', {**unit, 'outcome': 'assessed'})
        coverage = {'coverage_set_id': P, 'unit_kind': 'page', 'eligible': 1, 'assessed': 0, 'complete': False, 'units_url': '/api/v1/versions/' + V + '/assessments/coverage?coverage_set_id=' + P + '&attempt_run_id=' + R}
        applicable = {'source_intent': 'moving_target', 'target_set_id': B, 'captured_at': '2026-10-21T00:00:00Z', 'scope': {'kind': 'version', 'page_path': None, 'region_id': None}, 'status': 'unable_to_verify', 'coverage': coverage, 'assessed_at': None, 'reassessment_required': True}
        page = {'items': [], 'next_cursor': None, 'applicable': applicable}
        c.validate('observation_assessments_page', page)
        c.validate('observation_assessments_page', {**page, 'applicable': None})
        self.error('INVALID_REQUEST', c.validate, 'observation_assessments_page', {'items': [], 'applicable': applicable})
        self.error('INVALID_REQUEST', c.validate, 'observation_applicable', {**applicable, 'scope': {**applicable['scope'], 'declared': True}})

    def test_Task00401_evidence_item_excerpt_presence_and_omission(self):
        view = {'version_id': V, 'evidence_id': 'local', 'authored': {'id': 'local', 'source_id': 'source', 'capture_status': 'verified', 'path': 'page.html'}, 'source': {'id': 'source', 'kind': 'snapshot', 'retention_mode': 'selected_snapshot', 'snapshot_sha256': 'a' * 64, 'asset_path': 'page.html'}, 'availability': 'available', 'bytes_origin': 'retained_snapshot', 'reason': None, 'service_verification': None, 'verifications_url': '/api/v1/versions/' + V + '/source-evidence-verifications?evidence_id=local'}
        c.validate('evidence_item_view', view)
        c.validate('evidence_item_view', {**view, 'excerpt': {'bytes_sha256': c.digest(b'retained'), 'data_base64': 'cmV0YWluZWQ='}})
        c.validate('evidence_item_view', {**view, 'excerpt': {'bytes_sha256': c.digest(b'')}})
        c.validate('evidence_item_view', {**view, 'excerpt': {'bytes_sha256': c.digest(b'x' * 65537)}})
        c.validate('evidence_item_view', {**view, 'availability': 'unavailable', 'bytes_origin': None, 'reason': 'not_read_by_view'})
        for excerpt in (None, {}, {'data_base64': 'cmV0YWluZWQ='}, {'bytes_sha256': 'bad'}, {'bytes_sha256': 'a' * 64, 'data_base64': None}, {'bytes_sha256': 'a' * 64, 'data_base64': 'not base64'}, {'bytes_sha256': 'a' * 64, 'private_path': '/private'}):
            self.error('INVALID_REQUEST', c.validate, 'evidence_item_view', {**view, 'excerpt': excerpt})

    def test_Task00401_status_events_and_missing_detail_extensions(self):
        summary = {'available': False, 'reason': 'not_observed', 'configured_source_count': 0, 'last_observed_at': None}
        c.validate('source_status_summary', summary)
        for reason in ('source_not_registered', 'missing_source', 'missing_retained_bytes', 'retained_bytes_corrupt', 'source_denied'):
            c.validate('source_status_summary', {**summary, 'reason': reason})
        self.error('INVALID_ENUM', c.validate, 'source_status_summary', {**summary, 'reason': 'healthy'})
        c.validate('source_changed_event_body', {'version_id': V, 'run_id': R, 'classification': 'direct'})
        c.validate('source_availability_changed_event_body', {'version_id': V, 'run_id': R, 'availability': 'unavailable'})
        for field in ('target_set_id', 'coverage_set_id', 'attempt_run_id'):
            details = {'reason': field, field: R}
            c.validate('source_read_missing_details', details)
            c.validate('error', {'code': 'NOT_FOUND', 'message': 'not found', 'retryable': False, 'details': details})
        for status, code, details in ((409, 'SOURCE_UNAVAILABLE', {'reason': 'mapping_ambiguous'}), (400, 'INVALID_REQUEST', {'page_path': 'gone.html'}), (503, 'SERVICE_RECOVERING', {'reason': 'interrupted_before_commit'}), (503, 'FILE_ACCESS_ERROR', {'reason': 'source_read_failed'})):
            assert c.ERROR_HTTP[code] == status
            c.validate('error', {'code': code, 'message': 'source check failed', 'retryable': False, 'details': details})

    @pytest.fixture(autouse=True)
    def _model(self):
        self.model = c.Model()
        self.next_key = 0

    def newkey(self):
        self.next_key += 1
        return key(self.next_key)

    def error(self, code, function, *args, field=None, **kwargs):
        with pytest.raises(c.ContractError) as raised:
            function(*args, **kwargs)
        assert raised.value.code == code
        assert raised.value.http == c.ERROR_HTTP[code]
        if field is not None:
            assert raised.value.field == field
        return raised.value

    def staged(self, target=None, raw=b'complete original bytes', purpose='result', sealed=True):
        target = deepcopy(target or {'project_id': P, 'document_id': D})
        body = {'purpose': purpose, 'target': target, 'input_mode': 'directory' if purpose == 'result' else 'object', 'selection_name': 'Explanation'}
        upload = self.model.create(self.newkey(), body)
        cp = checkpoint(raw)
        self.model.write(upload, raw, cp)
        if sealed:
            self.model.complete(self.newkey(), upload, cp)
            assert self.model.settle(upload, 1)
        return (upload, cp)

    def test_C1_01_draft_scope_never_publishes(self):
        target = {'project_id': P, 'document_id': D, 'draft_id': R}
        body = {'purpose': 'result', 'input_mode': 'directory', 'target': target}
        upload = self.model.create(self.newkey(), body, {'draft.write'})
        assert self.model.uploads[upload]['state'] == 'open'
        self.error('FORBIDDEN', self.model.import_result, self.newkey(), {'upload_id': upload, 'document_id': D}, {'draft.write'})
        assert self.model.versions == {}

    def test_C1_02_replacement_exact_draft_history(self):
        draft = {'project_id': P, 'document_id': D, 'draft_id': R, 'state': 'ready', 'base_version_id': B, 'revision': 3, 'history': ['old']}
        for wrong in (None, OTHER):
            upload, _ = self.staged({'project_id': P, 'document_id': D, 'draft_id': wrong})
            before = deepcopy(draft)
            target = deepcopy(self.model.uploads[upload]['target'])
            self.error('INVALID_REQUEST', self.model.replace_draft, upload, draft)
            assert draft == before
            assert self.model.uploads[upload]['target'] == target
        upload, _ = self.staged({'project_id': P, 'document_id': D, 'draft_id': R})
        self.model.replace_draft(upload, draft)
        assert (draft['base_version_id'], draft['revision'], draft['history'][0]) == (B, 4, 'old')
        assert self.model.versions == {}

    def test_C1_03_body_consumer_authority_binding(self):
        operation = self.newkey()
        target = {'operation_id': operation, 'action': 'question', 'version_id': V, 'page_path': None}
        body = {'purpose': 'body', 'target': target, 'input_mode': 'object'}
        self.error('FORBIDDEN', self.model.create, self.newkey(), body, {'context.write'})
        upload, _ = self.staged(target, purpose='body')
        for action, actual_operation in [('annotation_create', operation), ('question', self.newkey())]:
            wrong = {**target, 'action': action}
            self.error('INVALID_REQUEST', self.model.adopt_object, upload, wrong, actual_operation)
        assert self.model.adopt_object(upload, target, operation) == c.digest(b'complete original bytes')
        final_key = self.newkey()
        meta_target = {'operation_id': final_key, 'action': 'import', 'project_id': P, 'document_id': D}
        meta_upload, _ = self.staged(meta_target, raw=b'{"document":{"title":"Bound metadata"}}', purpose='metadata')
        reference = self.model.object_reference(meta_upload)
        result_upload, _ = self.staged()
        self.model.latest[D] = B
        final_body = {'upload_id': result_upload, 'document_id': D, 'base_version_id': B, 'metadata_ref': reference}
        self.error('INVALID_REQUEST', self.model.import_result, self.newkey(), final_body, field='/metadata_ref')
        assert self.model.import_result(final_key, final_body)['status'] == 201
        assert self.model.uploads[meta_upload]['adopted']
        for action, purpose in [('import', 'metadata'), ('draft_replace', 'metadata'), ('generation', 'body'), ('annotation_edit', 'body'), ('evidence_verification', 'body')]:
            fields = {'import': {'project_id': P, 'document_id': None}, 'draft_replace': {'draft_id': R}, 'generation': {'project_id': P, 'document_id': D}, 'annotation_edit': {'annotation_id': R}, 'evidence_verification': {'version_id': V, 'evidence_id': '근거'}}[action]
            c.validate('upload_create', {'purpose': purpose, 'input_mode': 'object', 'target': {'operation_id': self.newkey(), 'action': action, **fields}})
        operation = self.newkey()
        target = {'operation_id': operation, 'action': 'evidence_verification', 'version_id': V, 'evidence_id': '근거'}
        body = {'purpose': 'body', 'input_mode': 'object', 'target': target}
        scopes = {'version.read', 'source.read'}
        for insufficient in ({'version.read'}, {'context.write'}, set()):
            before = deepcopy(self.model.__dict__)
            self.error('FORBIDDEN', self.model.create, self.newkey(), body, insufficient)
            assert self.model.__dict__ == before
        upload = self.model.create(self.newkey(), body, scopes)
        raw = b'complete verification body'
        cp = checkpoint(raw)
        self.model.write(upload, raw, cp)
        self.model.complete(self.newkey(), upload, cp)
        assert self.model.settle(upload, 1)
        for insufficient in ({'version.read'}, {'context.write'}, set()):
            before = deepcopy(self.model.__dict__)
            self.error('FORBIDDEN', self.model.adopt_object, upload, target, operation, insufficient)
            assert self.model.__dict__ == before
        for wrong, actual_operation, authority in [(target, self.newkey(), scopes), ({**target, 'version_id': OTHER}, operation, scopes), ({**target, 'evidence_id': '다른 근거'}, operation, scopes), ({**target, 'action': 'generation'}, operation, scopes | {'generation.request'})]:
            before = deepcopy(self.model.__dict__)
            self.error('INVALID_REQUEST', self.model.adopt_object, upload, wrong, actual_operation, authority, field='/target')
            assert self.model.__dict__ == before
        before = deepcopy(self.model.uploads[upload])
        assert self.model.adopt_object(upload, target, operation, scopes) == c.digest(raw)
        assert self.model.uploads[upload]['pins'] == {**before['pins'], 'adopted': before['pins']['adopted'] + 1}
        assert self.model.uploads[upload]['adopted']
        assert self.model.uploads[upload]['bytes'] == before['bytes']
        assert self.model.uploads[upload]['target'] == before['target']
        self._metadata_identity_regression()

    def test_C1_04_context_missing_null_literal_and_query(self):
        target = {'version_id': V, 'page_path': 'null', 'render_instance_id': R}
        upload, _ = self.staged(target, purpose='context')
        actual = c.context_binding([('version_id', V), ('page_path', 'null')])
        assert actual['render_instance_id'] is None
        assert actual['page_path'] == 'null'
        self.error('INVALID_REQUEST', self.model.adopt_object, upload, actual, self.newkey())
        assert c.context_binding([('version_id', V)]) == {'version_id': V, 'page_path': None, 'render_instance_id': None}
        for pairs in [[('version_id', V), ('render_instance_id', OTHER)], [('version_id', V), ('page_path', '')], [('version_id', V), ('version_id', V)], [('version_id', V), ('render_instance_id', 'null')]]:
            if pairs[-1][1] == OTHER:
                normalized = c.context_binding(pairs)
                self.error('INVALID_REQUEST', self.model.adopt_object, upload, normalized, self.newkey())
            else:
                self.error('INVALID_REQUEST', c.context_binding, pairs)
        c.validate('context_target', {'version_id': V, 'page_path': None, 'render_instance_id': None})
        self.error('INVALID_REQUEST', c.validate, 'context_target', {'version_id': V, 'page_path': None}, field='/render_instance_id')

    def test_C1_05_closed_targets_and_purpose_modes(self):
        for purpose, mode, target in [('result', 'object', {'project_id': P, 'document_id': D}), ('context', 'directory', {'version_id': V, 'page_path': None, 'render_instance_id': None})]:
            self.error('INVALID_ENUM', c.validate, 'upload_create', {'purpose': purpose, 'input_mode': mode, 'target': target})
        for target in [{'action': 'publish_everything', 'operation_id': self.newkey()}, {'action': 'question', 'operation_id': self.newkey(), 'version_id': V, 'page_path': None, 'admin': True}]:
            self.error('INVALID_REQUEST', c.validate, 'upload_create', {'purpose': 'body', 'input_mode': 'object', 'target': target})

    def test_C2_01_three_keys_and_header_errors(self):
        upload, cp = self.staged(sealed=False)
        final, seal = (self.newkey(), self.newkey())
        self.model.complete(seal, upload, cp)
        self.model.settle(upload, 1)
        self.model.latest[D] = B
        result = self.model.import_result(final, {'upload_id': upload, 'document_id': D, 'base_version_id': B})
        assert result['status'] == 201
        assert len(self.model.receipts) == 3
        self.error('OPERATION_CONFLICT', self.model.request, final, '/other', {}, lambda: 'wrong')
        self.error('PRECONDITION_REQUIRED', self.model.request, None, '/route', {}, lambda: 'wrong', field='/Idempotency-Key')
        for bad in ['"' + final + '"', [final, seal], final.upper().replace('00000000', 'ABCDEFAB'), 'wrong']:
            self.error('INVALID_REQUEST', self.model.request, bad, '/route', {}, lambda: 'wrong', field='/Idempotency-Key')

    def test_C2_02_response_loss_caller_journal(self):
        upload, cp = self.staged()
        journal = {'final_key': self.newkey(), 'create_key': key(1), 'seal_key': key(2), 'target': {'project_id': P, 'document_id': D}, 'base': B, 'phase': 'before-final-dispatch', 'upload_id': upload, 'checkpoint': cp}
        recovered = c.recover_journal(journal, None)
        assert recovered['journal'] == journal
        assert not recovered['publication_known_absent']
        assert not recovered['new_dispatch_authorized']
        self.model.latest[D] = B
        body = {'upload_id': upload, 'document_id': D, 'base_version_id': B}
        first = self.model.import_result(journal['final_key'], body)
        assert self.model.import_result(journal['final_key'], body) == first
        assert len(self.model.versions) == 1

    def test_C2_03_future_Q_not_current_U(self):
        U, Q = (self.newkey(), self.newkey())
        target = {'operation_id': Q, 'action': 'import', 'project_id': P, 'document_id': D}
        body = {'purpose': 'metadata', 'input_mode': 'object', 'target': target}
        upload = self.model.create(U, body)
        assert U != Q
        assert self.model.create(U, body) == upload
        changed = deepcopy(body)
        changed['target']['operation_id'] = self.newkey()
        self.error('OPERATION_CONFLICT', self.model.create, U, changed)
        assert Q not in self.model.receipts

    def test_C2_04_same_checkpoint_new_keys_one_job(self):
        upload, cp = self.staged(sealed=False)
        first_key, second_key = (self.newkey(), self.newkey())
        first = self.model.complete(first_key, upload, cp)
        assert self.model.complete(second_key, upload, deepcopy(cp)) == first
        assert len(self.model.jobs) == 1
        changed = {**cp, 'generation': 2}
        self.error('INVALID_TRANSITION', self.model.complete, self.newkey(), upload, changed)
        self.model.settle(upload, 1)
        assert self.model.complete(first_key, upload, cp)['status'] == 202
        assert self.model.complete(self.newkey(), upload, cp)['status'] == 200
        assert len(self.model.jobs) == 1

    def test_C2_05_equivalent_transport_replays_original_intent(self):
        first, _ = self.staged({'project_id': P, 'document_id': None})
        second, _ = self.staged({'project_id': P, 'document_id': None})
        operation = self.newkey()
        body = {'upload_id': first, 'document_id': None, 'metadata': {'document': {'title': 'Recorded'}}}
        result = self.model.import_result(operation, body)
        assert self.model.import_result(operation, {**body, 'upload_id': second}) == result
        for change in [{'metadata': {'document': {'title': 'Changed'}}}, {'version_id': V}, {'base_version_id': None}]:
            self.error('OPERATION_CONFLICT', self.model.import_result, operation, {**body, **change})
        another_project, _ = self.staged({'project_id': OTHER, 'document_id': None})
        self.error('OPERATION_CONFLICT', self.model.import_result, operation, {**body, 'upload_id': another_project})
        assert len(self.model.versions) == 1

    def test_C3_01_omitted_is_not_null(self):
        upload, _ = self.staged({'project_id': P, 'document_id': None})
        self.error('INVALID_REQUEST', self.model.import_result, self.newkey(), {'upload_id': upload}, field='/document_id')
        operation = self.newkey()
        body = {'upload_id': upload, 'document_id': None}
        result = self.model.import_result(operation, body)
        assert result['document_id'] is not None
        assert self.model.import_result(operation, body) == result
        self.error('INVALID_REQUEST', self.model.import_result, self.newkey(), {'upload_id': upload, 'document_id': D})

    def test_C3_02_pretransfer_base_survives_publication(self):
        self.model.latest[D] = B
        upload, _ = self.staged()
        body = {'upload_id': upload, 'document_id': D, 'base_version_id': B}
        self.model.latest[D] = OTHER
        operation = self.newkey()
        self.error('VERSION_CONFLICT', self.model.import_result, operation, body)
        assert self.model.reusable(upload)
        assert self.model.latest[D] == OTHER
        assert self.model.import_result(self.newkey(), {**body, 'base_version_id': OTHER})['status'] == 201

    def test_C3_03_omitted_base_reservation_once_and_replay_order(self):
        self.model.latest[D] = B
        upload, _ = self.staged()
        self.model.latest[D] = OTHER
        operation = self.newkey()
        body = {'upload_id': upload, 'document_id': D}
        self.model.import_result(operation, body, reserve_only=True)
        saved = deepcopy(self.model.plans[operation])
        assert saved['normalized']['base_version_id'] == OTHER
        assert 'base_version_id' in saved['normalized']['omitted_fields']
        self.model.latest[D] = V
        assert self.model.import_result(operation, body)['state'] == 'accepted'
        assert self.model.plans[operation] == saved
        self.error('VERSION_CONFLICT', self.model.publish, saved['normalized'], saved['semantic'])
        self.model.latest[D] = OTHER
        result = self.model.import_result(self.newkey(), body)
        assert result['status'] == 201
        completed = next((k for k, v in self.model.receipts.items() if v['result'] == result))
        self.model.latest[D] = V
        assert self.model.import_result(completed, body) == result
        self.error('FORBIDDEN', self.model.import_result, completed, body, {'draft.write'})

    def test_C3_04_client_association_precedence_no_fallback(self):
        remembered = {'project_id': OTHER, 'document_id': D}
        docs = {D: P}
        assert c.association(remembered, {'document_id': D}, docs) == {'project_id': P, 'document_id': D}
        assert c.association(remembered, {'project_id': P}, docs) == {'project_id': P, 'document_id': None}
        assert c.association(remembered, {'new_document': True}, docs) == {'project_id': OTHER, 'document_id': None}
        assert c.association({}, {}, docs, unfiled=P)['project_id'] == P
        self.error('NOT_FOUND', c.association, {}, {}, docs, owner=False, unfiled=P)
        self.error('NOT_FOUND', c.association, remembered, {}, {})
        self.error('INVALID_REQUEST', c.association, remembered, {'project_id': OTHER, 'document_id': D}, docs)
        self.error('INVALID_REQUEST', c.association, remembered, {'new_document': True, 'document_id': D}, docs)

    def test_C3_05_explicit_immutable_version_conflict(self):
        self.model.latest[D] = B
        upload, _ = self.staged()
        body = {'upload_id': upload, 'document_id': D, 'base_version_id': B, 'version_id': V}
        assert self.model.import_result(self.newkey(), body)['status'] == 201
        body['base_version_id'] = V
        assert self.model.import_result(self.newkey(), body)['status'] == 200
        changed, _ = self.staged(raw=b'changed immutable bytes')
        before = deepcopy(self.model.versions[V])
        self.error('IMMUTABLE_VERSION_CONFLICT', self.model.import_result, self.newkey(), {**body, 'upload_id': changed})
        assert self.model.versions[V] == before

    def test_C4_01_cancel_fences_until_confirmed_stop_retains(self):
        upload, cp = self.staged(sealed=False)
        old_key = self.newkey()
        job = self.model.complete(old_key, upload, cp)['job_id']
        self.model.cancel(upload)
        assert self.model.uploads[upload]['state'] == 'sealing'
        assert not self.model.settle(upload, 1)
        self.error('INVALID_TRANSITION', self.model.write, upload, b'wrong', checkpoint(b'wrong'))
        assert not self.model.settle(upload, 1, stopped=True)
        assert self.model.uploads[upload]['state'] == 'open'
        assert self.model.uploads[upload]['bytes'] == b'complete original bytes'
        assert self.model.jobs[job]['state'] == 'cancelled'
        next_job = self.model.complete(self.newkey(), upload, cp)['job_id']
        assert job != next_job
        assert self.model.complete(old_key, upload, cp)['job_id'] == job

    def test_C4_02_commit_wins_late_cancel(self):
        upload, _ = self.staged()
        assert self.model.cancel(upload) == 'already_completed'
        assert self.model.uploads[upload]['state'] == 'sealed'
        assert self.model.jobs[self.model.uploads[upload]['job']]['state'] == 'completed'

    def test_C4_03_abandon_fences_active_work_and_pins(self):
        upload, cp = self.staged(sealed=False)
        self.model.complete(self.newkey(), upload, cp)
        self.model.abandon(upload)
        state = self.model.uploads[upload]
        assert state['state'] == 'abandoned'
        assert state['pins']['active_work'] == 1
        assert not self.model.settle(upload, 1)
        assert self.model.jobs[state['job']]['state'] == 'cancelling'
        assert not self.model.reusable(upload)

    def test_C4_04_abandon_adopted_preserves_references(self):
        target = {'version_id': V, 'page_path': None, 'render_instance_id': None}
        upload, _ = self.staged(target, purpose='context')
        adopted = self.model.adopt_object(upload, target, self.newkey())
        self.model.uploads[upload]['pins']['recovery'] = 1
        self.model.abandon(upload)
        assert self.model.jobs[self.model.uploads[upload]['job']]['state'] == 'completed'
        assert self.model.uploads[upload]['adopted']
        assert self.model.uploads[upload]['pins']['adopted'] == 1
        assert self.model.uploads[upload]['pins']['recovery'] == 1
        assert c.digest(self.model.uploads[upload]['bytes']) == adopted

    def test_C4_05_late_failed_result_cannot_reopen(self):
        upload, cp = self.staged(sealed=False)
        self.model.complete(self.newkey(), upload, cp)
        self.model.abandon(upload)
        assert not self.model.settle(upload, 1, success=False)
        assert self.model.uploads[upload]['state'] == 'abandoned'
        assert not self.model.settle(upload, 1, success=False, stopped=True)
        assert self.model.uploads[upload]['state'] == 'abandoned'
        assert self.model.uploads[upload]['pins']['active_work'] == 0

    def test_C4_06_simulated_recovery_coupled_state(self):
        upload, cp = self.staged(sealed=False)
        self.model.complete(self.newkey(), upload, cp)
        self.model.cancel(upload)
        snapshot = deepcopy(self.model.__dict__)
        recovered = c.Model()
        recovered.__dict__.update(snapshot)
        self.error('INVALID_TRANSITION', recovered.write, upload, b'new', checkpoint(b'new'))
        recovered.settle(upload, 1, stopped=True)
        assert recovered.uploads[upload]['state'] == 'open'
        assert recovered.uploads[upload]['bytes'] == b'complete original bytes'
        assert recovered.jobs[recovered.uploads[upload]['job']]['state'] == 'cancelled'
        self.error('INVALID_TRANSITION', recovered.resume, recovered.uploads[upload]['job'])
        recovered.abandon(upload)
        self.error('INVALID_TRANSITION', recovered.complete, self.newkey(), upload, cp)

    def test_C5_01_summary_and_owner_recovery(self):
        upload, _ = self.staged()
        summary = self.model.summary(upload)
        expected = {'upload_id', 'originating_principal_id', 'originating_device_id', 'purpose', 'input_mode', 'selection_name', 'target', 'state', 'byte_size', 'staging_bytes', 'created_at', 'updated_at', 'reusable', 'unavailable_reason', 'pin_summary', 'pins_url', 'detail_url', 'revision', 'etag'}
        assert set(summary) == expected
        assert 'error' not in summary
        assert summary['reusable']
        assert not self.model.summary(upload, authorized=False)['reusable']
        assert summary['originating_device_id'] is None

    def test_C5_02_detail_exact_nullable_and_cursor_resources(self):
        upload, _ = self.staged()
        summary, detail = (self.model.summary(upload), self.model.summary(upload, detail=True))
        extra = {'checkpoint', 'ranges_url', 'entries_url', 'error', 'seal_job_id', 'seal_operation_id', 'sealed_content_set_id', 'sealed_object_ref'}
        assert set(detail) - set(summary) == extra
        assert {k: detail[k] for k in summary} == summary
        assert detail['sealed_object_ref'] is None
        c.validate('object_ref', {'object_id': V, 'bytes_sha256': c.digest(b'object'), 'byte_size': '6', 'media_type': 'text/plain'})
        self.error('INVALID_REQUEST', c.validate, 'upload_summary', detail)
        self.error('INVALID_REQUEST', c.validate, 'upload_detail', {k: v for k, v in detail.items() if k != 'error'}, field='/error')
        self._unavailable_detail_regression()

    def test_C5_03_sealed_not_necessarily_reusable(self):
        upload, _ = self.staged()
        self.model.uploads[upload]['available'] = False
        summary = self.model.summary(upload)
        assert summary['state'] == 'sealed'
        assert not summary['reusable']
        assert summary['unavailable_reason'] is not None
        assert self.model.uploads[upload]['bytes'] == b'complete original bytes'
        self.model.uploads[upload]['available'] = True
        self.model.uploads[upload]['adopted'] = True
        self.model.uploads[upload]['pins']['adopted'] = 1
        assert self.model.reusable(upload)
        assert self.model.uploads[upload]['state'] == 'sealed'
        open_upload = self.model.create(self.newkey(), {'purpose': 'result', 'input_mode': 'file', 'target': {'project_id': P, 'document_id': None}})
        assert not self.model.summary(open_upload, detail=True)['checkpoint']['complete_input']

    def test_SUP_PACKAGE_raw_examples_and_metadata(self):
        text = (Path(__file__).parents[2] / 'docs/examples/package-example.md').read_text()
        payload = {}
        for name, block in re.findall('## Payload file: `([^`]+)`\\n\\n```[^\\n]*\\n(.*?)```', text, re.S):
            payload[name] = ('file', block.encode())
        payload['exports'] = ('directory', None)
        submitted_text = text.split('## Optional submitted metadata')[1].split('```json\n')[1].split('```')[0]
        generated_text = text.split('## Generated service metadata')[1].split('```json\n')[1].split('```')[0]
        declared_inventory = text.split('```jsonl\n')[1].split('```')[0].encode()
        assert c.inventory(payload) == declared_inventory
        assert c.digest(declared_inventory) == 'df1a2aaa44139da7592c4a5c158addfec74b438351c5561ca8e64b4cfe6363da'
        assert c.digest(submitted_text.encode()) == '1f2cf1cb072cbabb9f3f0a5722d031cefec0f42d27bf86bae6cf30d1f303f7ae'
        assert c.digest(generated_text.encode()) == '7e8b471f187350c2781098e6038aa7d89bc70b3d7d441bd4bc63334b14a95f86'
        submitted, generated = (c.strict_json(submitted_text), c.strict_json(generated_text))
        c.metadata(submitted, payload)
        c.validate('generated_metadata', generated)
        assert c.metadata(None, payload)['source_basis'] == 'unverified'
        assert c.capture(('single.htm', b'<p>single'), 'file') == {'single.htm': ('file', b'<p>single')}
        richer = {**payload, '.hidden': ('file', b'secret-shaped ordinary payload'), 'unknown.weird': ('file', b'\x00\xff'), 'nested/empty': ('directory', None), 'alias': ('symlink', 'pages/결과.html')}
        richer.pop('index.html')
        archive_bytes = io.BytesIO()
        with zipfile.ZipFile(archive_bytes, 'w') as archive:
            for name, (kind, raw) in richer.items():
                info = zipfile.ZipInfo(name + ('/' if kind == 'directory' else ''))
                if kind == 'symlink':
                    info.external_attr = 41471 << 16
                archive.writestr(info, raw if kind == 'file' else raw.encode() if kind == 'symlink' else b'')
        assert c.inventory(c.capture(archive_bytes.getvalue(), 'archive')) == c.inventory(richer)
        assert c.capture(richer, 'directory') == richer
        bad = deepcopy(submitted)
        bad['sources'][0]['snapshot_sha256'] = '0' * 64
        self.error('INVALID_EVIDENCE', c.metadata, bad, payload, field='/sources/0/snapshot_sha256')
        bad = deepcopy(submitted)
        bad['document']['extra'] = True
        self.error('PACKAGE_INVALID', c.metadata, bad, payload, field='/document/extra')
        git = {'id': 'git-source', 'kind': 'git', 'repo_id': P, 'object_format': 'sha1', 'exact_revision': 'a' * 40, 'retention_mode': 'selected_snapshot', 'asset_path': 'source.txt', 'bytes_sha256': c.digest(payload['source.txt'][1])}
        c.metadata({'sources': [git]}, payload)
        self.error('INVALID_EVIDENCE', c.metadata, {'sources': [{k: v for k, v in git.items() if k != 'bytes_sha256'}]}, payload)
        self.error('INVALID_EVIDENCE', c.metadata, {'sources': [{**git, 'snapshot_sha256': 'b' * 64}]}, payload)
        assert payload['source.txt'][1] == b'Fixture rule: successful roles remain completed; only failed roles are queued again.\n'
        self._utc_regression(generated)

    def test_SUP_INVENTORY_exact_bytes_equivalence_and_links(self):
        entries = {'한글.htm': ('file', b'a'), 'A': ('file', b'A'), 'a': ('file', b'a'), 'line\n\t"\\': ('file', b''), 'empty': ('directory', None), 'link': ('symlink', '한글.htm')}
        actual = c.inventory(entries)
        assert [json.loads(x)[0] for x in actual.splitlines()] == sorted(entries, key=lambda x: x.encode('utf-8'))
        assert b'"0"' in actual
        assert b'\\n\\t\\"\\\\' in actual
        assert b'\\uD55C' not in actual
        assert c.lookup(entries, 'link', True)['resolved_path'] == '한글.htm'
        assert c.lookup(entries, 'link')['kind'] == 'symlink'
        assert c.lookup({'é': ('file', b'one')}, 'é')['resolved_path'] == 'é'
        ambiguous = {'Å': ('file', b'one'), 'Å': ('file', b'two')}
        self.error('INVALID_REQUEST', c.lookup, ambiguous, 'Å')
        assert c.lookup(ambiguous, 'Å')['data'] == b'one'
        for link in ['../outside', '/outside', 'loop', 'missing']:
            self.error('NOT_FOUND', c.lookup, {'loop': ('symlink', link)}, 'loop', True)
        for name in ['../escape', '/absolute', 'C:/drive', 'a//b', 'a/./b', 'a\x00b']:
            self.error('UNSAFE_PATH', c.inventory, {name: ('file', b'x')})
        self.error('INVALID_REQUEST', c.validate, 'decimal', '01')
        self.error('INVALID_REQUEST', c.validate, 'decimal', 1)

    def test_SUP_CONFIG_strict_modes_origins_revision_and_plan(self):
        default = json.loads(Path(__file__).with_name('config-default.json').read_text())
        c.configuration(default)
        for raw in ['{"a":1,"a":2}', '{"a":NaN}']:
            self.error('INVALID_REQUEST', c.strict_json, raw)
        for raw in ['a: 1\na: 2', 'a: &a [1]\nb: *a', 'a: !!str hello', 'a: {<<: {b: 2}}']:
            self.error('INVALID_REQUEST', c.strict_yaml, raw)
        for mode, interval, cron in [('interval', 60, None), ('calendar', None, '*/5 * * * *')]:
            valid = deepcopy(default)
            valid['maintenance']['schedule'].update(mode=mode, interval_seconds=interval, cron=cron)
            c.configuration(valid)
        for mode, interval, cron in [('manual', 60, None), ('interval', None, None), ('calendar', None, '99 * * * *'), ('calendar', None, '* * *')]:
            bad = deepcopy(default)
            bad['maintenance']['schedule'].update(mode=mode, interval_seconds=interval, cron=cron)
            self.error('INVALID_REQUEST', c.configuration, bad)
        for bad_origin in ['https://*.example', 'https://user:pass@example', 'https://example/path', 'http://example', 'https://example#x']:
            bad = deepcopy(default)
            bad['network'].update(remote_enabled=True, external_base_url=bad_origin, allowed_origins=['http://127.0.0.1:9270', bad_origin])
            self.error('INVALID_REQUEST', c.configuration, bad)
        remote_valid = deepcopy(default)
        remote_valid['network'].update(
            remote_enabled=True,
            external_base_url='https://node.tailnet.ts.net',
            allowed_origins=['http://127.0.0.1:9270', 'https://node.tailnet.ts.net'],
            renderer_mappings=[
                {'version_id': V, 'origin': 'https://node.tailnet.ts.net:444', 'upstream': 'http://127.0.0.1:9291', 'reassignable': False},
                {'version_id': R, 'origin': 'https://node.tailnet.ts.net:9292', 'upstream': 'http://127.0.0.1:9292', 'reassignable': False},
            ],
        )
        c.configuration(remote_valid)
        for bad_mappings, field in [
            ([{'version_id': V, 'origin': 'https://Node.Tailnet.ts.net:443', 'upstream': 'http://127.0.0.1:9291', 'reassignable': False}], '/network/renderer_mappings/0/origin'),
            ([{'version_id': V, 'origin': 'https://node.tailnet.ts.net:9291', 'upstream': 'http://127.0.0.1:9291', 'reassignable': False}, {'version_id': V, 'origin': 'https://node.tailnet.ts.net:9292', 'upstream': 'http://127.0.0.1:9292', 'reassignable': False}], '/network/renderer_mappings/1/version_id'),
            ([{'version_id': V, 'origin': 'https://node.tailnet.ts.net:9291', 'upstream': 'http://127.0.0.1:9291', 'reassignable': False}, {'version_id': R, 'origin': 'https://node.tailnet.ts.net:9292', 'upstream': 'http://127.0.0.1:9291', 'reassignable': False}], '/network/renderer_mappings/1/upstream'),
        ]:
            bad = deepcopy(remote_valid)
            bad['network']['renderer_mappings'] = bad_mappings
            self.error('INVALID_REQUEST', c.configuration, bad, field=field)
        self.model.policy = deepcopy(default)
        first = self.model.commit_config(P, 1, default, True)
        assert first == {'config_revision': 2, 'permissions_revision': 1}
        assert self.model.commit_config(P, 1, default, True, alias=D, expired=True) == first
        assert self.model.config_revision == 2
        changed = deepcopy(default)
        changed['source']['default_retention'] = 'selected_snapshot'
        self.error('OPERATION_CONFLICT', self.model.commit_config, P, 1, changed, True)
        assert self.model.commit_config(R, 2, changed, True)['permissions_revision'] == 2
        bad = deepcopy(changed)
        bad['unknown'] = True
        before = deepcopy(self.model.__dict__)
        self.error('INVALID_REQUEST', self.model.commit_config, V, 3, bad, True)
        assert self.model.__dict__ == before
        self.error('INVALID_REQUEST', self.model.commit_config, V, 3, changed, False, field='/confirmed')
        self.error('STALE_PLAN', self.model.commit_config, V, 3, changed, True, expired=True)
        presentation = {'projects': {P: {'name': 'Presentation only', 'maintenance': {'depth': 'full_scope'}}}}
        assert self.model.commit_config(V, 3, presentation, True) == {'config_revision': 4, 'permissions_revision': 2}
        assert self.model.policy['maintenance'] == changed['maintenance']
        assert self.model.policy['projects'][P]['maintenance']['depth'] == 'full_scope'
        assert self.model.policy['source'] == changed['source']
        partial = {'freshness_assessment': 'disabled'}
        override = {'projects': {P: {'maintenance': {'auto_apply': partial}}}}
        submitted = c.merge_configuration(default, override)
        c.validate('config', submitted)
        assert c.configuration(submitted) == submitted
        assert self.model.commit_config(self.newkey(), 4, override, True) == {'config_revision': 5, 'permissions_revision': 2}
        effective = c.merge_configuration(self.model.policy['maintenance'], self.model.policy['projects'][P]['maintenance'])
        expected_actions = {**default['maintenance']['auto_apply'], **partial}
        assert effective['auto_apply'] == expected_actions
        assert self.model.policy['maintenance']['auto_apply'] == default['maintenance']['auto_apply']
        assert self.model.policy['projects'][P]['maintenance']['auto_apply'] == partial
        assert c.configuration(c.merge_configuration(default, {'projects': {P: {'maintenance': {'auto_apply': {}}}}})) == c.merge_configuration(default, {'projects': {P: {'maintenance': {'auto_apply': {}}}}})
        for invalid, code, field in [({'unknown_action': 'disabled'}, 'INVALID_REQUEST', '/projects/' + P + '/maintenance/auto_apply/unknown_action'), ({'freshness_assessment': 'unknown'}, 'INVALID_ENUM', '/projects/' + P + '/maintenance/auto_apply/freshness_assessment'), ({'freshness_assessment': True}, 'INVALID_ENUM', '/projects/' + P + '/maintenance/auto_apply/freshness_assessment'), (None, 'INVALID_REQUEST', '/projects/' + P + '/maintenance/auto_apply')]:
            before = deepcopy(self.model.__dict__)
            invalid_override = {'projects': {P: {'maintenance': {'auto_apply': invalid}}}}
            self.error(code, self.model.commit_config, self.newkey(), 5, invalid_override, True, field=field)
            assert self.model.__dict__ == before
        incomplete = deepcopy(default)
        del incomplete['maintenance']['auto_apply']['topic_assignment']
        self.error('INVALID_REQUEST', c.configuration, incomplete, field='/maintenance/auto_apply/topic_assignment')
        self.error('OPERATION_CONFLICT', self.model.commit_config, OTHER, 4, {}, True, alias=P)
        unknown = deepcopy(default)
        unknown['network']['allowed_origins'] += ['https://unknown.example']
        self.error('INVALID_REQUEST', c.configuration, unknown, field='/network/allowed_origins')
        self.error('UNSUPPORTED_SCHEMA', c.configuration, {**default, 'schema_version': '2.0'}, field='/schema_version')
        wrong = deepcopy(default)
        wrong['security']['pairing_expiry_seconds'] = True
        self.error('INVALID_REQUEST', c.configuration, wrong, field='/security/pairing_expiry_seconds')
        self._receipt_family_regression(default)

    def test_SUP_CITATION_attempt_later_unavailable_partial_explanation(self):
        identity = {'target_set_id': P, 'source': {'kind': 'git', 'repo_id': D, 'object_format': 'sha1', 'exact_revision': 'a' * 40, 'snapshot_sha256': None}, 'location': {'path': 'source.go', 'line_start': 1, 'line_end': 2}, 'bytes_sha256': c.digest(b'actual bytes')}
        observed = {**identity, 'basis': 'observed_input', 'read_id': V, 'verification_id': None, 'input_origin': 'daemon_supplied'}
        self.error('INVALID_EVIDENCE', c.citation, observed, {}, {})
        observations = {V: {**identity, 'input_origin': 'daemon_supplied', 'attempt_id': R}}
        c.citation(observed, observations, {}, attempt_id=R)
        self.error('INVALID_EVIDENCE', c.citation, observed, observations, {}, attempt_id=OTHER)
        later = {**identity, 'basis': 'posthoc_verified', 'read_id': None, 'verification_id': B, 'input_origin': None}
        verified = {B: {**identity, 'outcome': 'captured', 'verified_at': '2026-10-16T00:00:00Z'}}
        c.citation(later, {}, verified)
        for outcome in ['mismatch', 'unavailable']:
            self.error('INVALID_EVIDENCE', c.citation, later, {}, {B: {**verified[B], 'outcome': outcome}})
        self.error('INVALID_EVIDENCE', c.citation, {**later, 'read_id': V}, observations, verified)
        explanation = {'version_id': V, 'page_path': 'nested/page.html', 'region_id': 'M11', 'bytes_sha256': c.digest(b'original exact page')}
        for page, region in [(None, None), ('nested/page.html', None), ('nested/page.html', 'M11'), ('null', 'M11')]:
            reference = {**explanation, 'page_path': page, 'region_id': region}
            assert c.validate('explanation_reference', reference) == reference
        self.error('INVALID_REQUEST', c.validate, 'explanation_reference', {**explanation, 'page_path': None}, field='/page_path')
        self.error('INVALID_REQUEST', c.validate, 'explanation_reference', {**explanation, 'region_id': 7}, field='/region_id')
        self.error('INVALID_REQUEST', c.validate, 'explanation_reference', {**explanation, 'page_path': ''}, field='/page_path')
        missing_page = {k: v for k, v in explanation.items() if k != 'page_path'}
        self.error('INVALID_REQUEST', c.validate, 'explanation_reference', missing_page, field='/page_path')
        self.error('INVALID_REQUEST', c.validate, 'explanation_reference', {**explanation, 'basis': 'observed_input'})
        assert c.coverage_status(False, 0, []) == 'unable_to_verify'
        assert c.coverage_status(True, 0, []) == 'not_assessed'
        assert c.coverage_status(True, 2, ['assessed', 'partial']) == 'not_assessed'
        assert c.coverage_status(True, 2, ['assessed', 'assessed']) == 'aligned_within_scope'
        assert c.coverage_status(True, 2, ['assessed', 'partial'], discrepancy=True) == 'outdated_against_target'

    def test_SUP_COMPATIBILITY_directional_safe_required_and_capability(self):
        known = {'state': ['sealed'], 'authority': ['read_only']}
        response = {'state': 'sealed', 'authority': 'read_only', 'optional_label': 'new additive display'}
        assert c.compatibility('1.0', known, response) == {'state': 'sealed', 'authority': 'read_only'}
        for change in [{'state': 'new_state'}, {'authority': 'admin'}]:
            self.error('INVALID_STATE_SCHEMA', c.compatibility, '1.0', known, {**response, **change})
        self.error('UNSUPPORTED_SCHEMA', c.compatibility, '2.0', known, response)
        self.error('INVALID_STATE_SCHEMA', c.compatibility, '1.0', known, {'state': 'sealed'})
        self.error('FEATURE_NOT_READY', c.fail, 'FEATURE_NOT_READY', '/questions', 'production capability absent')

    def _metadata_identity_regression(self):

        def prepare(metadata, referenced, document=D, explicit=True):
            self.model = c.Model()
            if document is not None:
                self.model.latest[document] = B
            upload, _ = self.staged({'project_id': P, 'document_id': document})
            operation = self.newkey()
            body = {'upload_id': upload, 'document_id': document}
            if explicit:
                body.update(version_id=V, base_version_id=B if document else None)
            if referenced:
                target = {'operation_id': operation, 'action': 'import', 'project_id': P, 'document_id': document}
                obj, _ = self.staged(target, raw=c.canonical(metadata), purpose='metadata')
                body['metadata_ref'] = self.model.object_reference(obj)
            elif metadata is not None:
                body['metadata'] = metadata
            return (operation, body, upload)
        matching = {'document': {'id': D, 'project_id': P}, 'version': {'id': V, 'base_version_id': B}}
        for referenced in (False, True):
            prefix = '/metadata_ref' if referenced else '/metadata'
            for section, field in [('document', 'id'), ('document', 'project_id'), ('version', 'id'), ('version', 'base_version_id')]:
                bad = deepcopy(matching)
                bad[section][field] = OTHER
                for reservation in (False, True):
                    operation, body, upload = prepare(bad, referenced)
                    before = deepcopy(self.model.__dict__)
                    self.error('INVALID_REQUEST', self.model.import_result, operation, body, reserve_only=reservation, field=prefix + '/' + section + '/' + field)
                    assert self.model.__dict__ == before
                    assert self.model.uploads[upload]['bytes'] == b'complete original bytes'
            for metadata in (matching, {}):
                operation, body, _ = prepare(metadata, referenced)
                assert self.model.import_result(operation, body) == {'status': 201, 'version_id': V, 'document_id': D}
            metadata = {'document': {'id': D, 'project_id': P}, 'version': {'base_version_id': B}}
            operation, body, _ = prepare(metadata, referenced, explicit=False)
            result = self.model.import_result(operation, body)
            self.model.latest[D] = OTHER
            before = deepcopy(self.model.__dict__)
            assert self.model.import_result(operation, body) == result
            assert self.model.__dict__ == before
            operation, body, _ = prepare({'version': {'id': OTHER}}, referenced, explicit=False)
            before = deepcopy(self.model.__dict__)
            self.error('INVALID_REQUEST', self.model.import_result, operation, body, field=prefix + '/version/id')
            assert self.model.__dict__ == before
            for metadata in ({}, {'document': {'project_id': P}, 'version': {'base_version_id': None}}):
                operation, body, _ = prepare(metadata, referenced, document=None, explicit=False)
                result = self.model.import_result(operation, body)
                assert result['status'] == 201
                assert result['document_id'] is not None
            operation, body, _ = prepare({'document': {'id': OTHER}}, referenced, document=None, explicit=False)
            before = deepcopy(self.model.__dict__)
            self.error('INVALID_REQUEST', self.model.import_result, operation, body, field=prefix + '/document/id')
            assert self.model.__dict__ == before
        operation, body, _ = prepare(None, False)
        assert self.model.import_result(operation, body)['status'] == 201

    def _unavailable_detail_regression(self):
        for purpose in ('context', 'body', 'metadata'):
            operation = self.newkey()
            target = {'context': {'version_id': V, 'page_path': None, 'render_instance_id': R}, 'body': {'operation_id': operation, 'action': 'question', 'version_id': V, 'page_path': None}, 'metadata': {'operation_id': operation, 'action': 'import', 'project_id': P, 'document_id': D}}[purpose]
            upload, _ = self.staged(target, purpose=purpose)
            reference = self.model.object_reference(upload)
            assert self.model.summary(upload, detail=True)['sealed_object_ref'] == reference
            self.model.uploads[upload]['available'] = False
            before = deepcopy(self.model.__dict__)
            summary, detail = (self.model.summary(upload), self.model.summary(upload, detail=True))
            c.validate('upload_summary', summary)
            c.validate('upload_detail', detail)
            assert {k: detail[k] for k in summary} == summary
            assert detail['state'] == 'sealed'
            assert not detail['reusable']
            assert detail['unavailable_reason'] is not None
            assert detail['sealed_object_ref'] is None
            self.error('INVALID_TRANSITION', self.model.object_reference, upload)
            self.error('INVALID_REQUEST', self.model.adopt_object, upload, target, operation)
            assert self.model.__dict__ == before
            self.model.uploads[upload]['available'] = True
            assert self.model.object_reference(upload) == reference
            self.model.abandon(upload)
            assert self.model.summary(upload, detail=True)['sealed_object_ref'] is None
            self.error('INVALID_TRANSITION', self.model.object_reference, upload)

    def _receipt_family_regression(self, default):
        body = {'purpose': 'result', 'input_mode': 'file', 'target': {'project_id': P, 'document_id': None}}
        for first_alias in (None, D):
            self.model = c.Model()
            self.model.policy = deepcopy(default)
            result = self.model.commit_config(P, 1, {}, True, alias=first_alias)
            assert result == {'config_revision': 2, 'permissions_revision': 1}
            for identity in {P, first_alias or P}:
                before = deepcopy(self.model.__dict__)
                self.error('OPERATION_CONFLICT', self.model.create, identity, body)
                self.error('FORBIDDEN', self.model.create, identity, body, set())
                self.error('FORBIDDEN', self.model.commit_config, P, 1, {}, True, authority=set())
                assert self.model.__dict__ == before
            for alias in (None, first_alias, V):
                assert self.model.commit_config(P, 1, {}, True, alias=alias, expired=True) == result
                assert self.model.config_revision == 2
                assert self.model.permissions_revision == 1
            before = deepcopy(self.model.__dict__)
            self.error('OPERATION_CONFLICT', self.model.commit_config, R, 2, {}, True, alias=P)
            self.error('OPERATION_CONFLICT', self.model.create, V, body)
            assert self.model.__dict__ == before
        for regular_key, alias in [(P, None), (P, D), (D, D)]:
            self.model = c.Model()
            self.model.policy = deepcopy(default)
            upload = self.model.create(regular_key, body)
            before = deepcopy(self.model.__dict__)
            self.error('OPERATION_CONFLICT', self.model.commit_config, P, 1, {}, True, alias=alias)
            assert self.model.__dict__ == before
            assert self.model.create(regular_key, body) == upload
            assert self.model.__dict__ == before

    def _utc_regression(self, generated):
        valid = ['0000-02-29T00:00:00Z', '0001-01-01T00:00:00Z', '2000-02-29T23:59:59Z', '2400-02-29T00:00:00Z', '9999-12-31T23:59:59Z', '2026-10-16T00:00:00.' + '1234567890' * 100 + 'Z']
        leap_days = '1972-06-30 1972-12-31 1973-12-31 1974-12-31 1975-12-31\n1976-12-31 1977-12-31 1978-12-31 1979-12-31 1981-06-30 1982-06-30\n1983-06-30 1985-06-30 1987-12-31 1989-12-31 1990-12-31 1992-06-30\n1993-06-30 1994-06-30 1995-12-31 1997-06-30 1998-12-31 2005-12-31\n2008-12-31 2012-06-30 2015-06-30 2016-12-31'.split()
        valid += [day + 'T23:59:60.12345678901234567890Z' for day in leap_days]
        invalid = ['2026-99-99T99:99:99Z', '2026-02-30T00:00:00Z', '1900-02-29T00:00:00Z', '0001-02-29T00:00:00Z', '2026-04-31T00:00:00Z', '2026-01-01T24:00:00Z', '2026-01-01T00:60:00Z', '2016-12-31T23:59:61Z', '2016-12-30T23:59:60Z', '2016-12-31T23:58:60Z', '2017-12-31T23:59:60Z', '２０２６-01-01T00:00:00Z', '2026-00-01T00:00:00Z', '2026-01-00T00:00:00Z', '10000-01-01T00:00:00Z', '2026-01-01T00:00:00.Z']
        for value in valid:
            assert c.validate('time', value) == value
            nested = deepcopy(generated)
            nested['version']['created_at'] = value
            assert c.validate('generated_metadata', nested) == nested
        for value in invalid:
            self.error('INVALID_REQUEST', c.validate, 'time', value)
            nested = deepcopy(generated)
            nested['version']['created_at'] = value
            self.error('INVALID_REQUEST', c.validate, 'generated_metadata', nested, field='/version/created_at')

    def test_C5_04_selected_personal_resource_wire(self):
        # Absent state: exact default visible value, explicit null validator
        # keys, and the create precondition. Stored state keeps exists true
        # with a real revision/etag, and the pointer's page_position_url is a
        # nullable canonical page link that never lends the pointer validator.
        absent = [
            ('personal_reader_preference', {'exists': False, 'value': {'default_page': None}, 'revision': None, 'etag': None, 'required_precondition': 'if_none_match_star', 'page_position_url': None}),
            ('personal_reading_position', {'exists': False, 'value': {'page_path': None, 'page_query': '', 'page_fragment': '', 'opened': False, 'first_opened_at': None, 'last_opened_at': None}, 'revision': None, 'etag': None, 'required_precondition': 'if_none_match_star', 'page_position_url': None}),
            ('personal_page_position', {'exists': False, 'value': {'page_query': '', 'page_fragment': '', 'position': {}}, 'revision': None, 'etag': None, 'required_precondition': 'if_none_match_star', 'page_position_url': None}),
        ]
        for name, item in absent:
            c.validate(name, item)
            # Unknown keys and stripped null validator keys are both invalid.
            self.error('INVALID_REQUEST', c.validate, name, {**item, 'extra': 1}, field='/extra')
            stripped = {k: v for k, v in item.items() if k not in ('revision', 'etag')}
            if name == 'personal_reading_position':
                stripped = {k: v for k, v in stripped.items() if k != 'page_position_url'}
            self.error('INVALID_REQUEST', c.validate, name, stripped, field='/revision')
            wrong_precondition = {**item, 'required_precondition': 'if-match'}
            self.error('INVALID_ENUM', c.validate, name, wrong_precondition, field='/required_precondition')
        stored_default = {'exists': True, 'value': {'default_page': None}, 'revision': 1, 'etag': '"sha256-stored"', 'page_position_url': None}
        c.validate('personal_reader_preference', stored_default)
        c.validate('personal_reader_preference', {'exists': True, 'value': {'default_page': 'flows/retry.html'}, 'revision': 4, 'etag': '"sha256-stored"', 'page_position_url': None})
        pointer = {'exists': True, 'value': {'page_path': 'flows/retry.html', 'page_query': 's=1', 'page_fragment': 'f', 'opened': True, 'first_opened_at': '2026-10-18T00:00:00.000000000Z', 'last_opened_at': '2026-10-18T01:00:00.000000000Z'}, 'revision': 2, 'etag': '"sha256-pointer"', 'page_position_url': '/api/v1/versions/' + V + '/page-position?page=flows%2Fretry.html'}
        c.validate('personal_reading_position', pointer)
        c.validate('personal_reading_position', {**pointer, 'value': {**pointer['value'], 'page_path': None}, 'page_position_url': None})
        c.validate('personal_page_position', {'exists': True, 'value': {'page_query': 's=1', 'page_fragment': 'f', 'position': {'top': 120, 'region_id': 'retry'}}, 'revision': 3, 'etag': '"sha256-page"', 'page_position_url': None})
        self.error('INVALID_REQUEST', c.validate, 'personal_page_position', {'exists': True, 'value': {'page_query': 's=1', 'page_fragment': 'f', 'position': {}}, 'revision': None, 'etag': None, 'page_position_url': None}, field='/revision')
        # Half-null validators fail in BOTH directions: stored state with one
        # null validator, absent state with one real validator.
        self.error('INVALID_REQUEST', c.validate, 'personal_reader_preference', {'exists': True, 'value': {'default_page': None}, 'revision': 1, 'etag': None, 'page_position_url': None}, field='/revision')
        self.error('INVALID_REQUEST', c.validate, 'personal_reader_preference', {'exists': True, 'value': {'default_page': None}, 'revision': None, 'etag': '"sha256-stored"', 'page_position_url': None}, field='/revision')
        self.error('INVALID_REQUEST', c.validate, 'personal_reader_preference', {'exists': False, 'value': {'default_page': None}, 'revision': 1, 'etag': None, 'required_precondition': 'if_none_match_star', 'page_position_url': None}, field='/revision')
        self.error('INVALID_REQUEST', c.validate, 'personal_reading_position', {'exists': False, 'value': absent[1][1]['value'], 'revision': None, 'etag': '"sha256-stored"', 'required_precondition': 'if_none_match_star', 'page_position_url': None}, field='/revision')
        # Absent state carries the exact family default value, never a
        # stored-looking one.
        self.error('INVALID_REQUEST', c.validate, 'personal_reader_preference', {**absent[0][1], 'value': {'default_page': 'flows/retry.html'}}, field='/value')
        self.error('INVALID_REQUEST', c.validate, 'personal_reading_position', {**absent[1][1], 'value': {'page_path': None, 'page_query': 's=1', 'page_fragment': '', 'opened': False, 'first_opened_at': None, 'last_opened_at': None}}, field='/value')
        self.error('INVALID_REQUEST', c.validate, 'personal_page_position', {**absent[2][1], 'value': {'page_query': '', 'page_fragment': '', 'position': {'top': 1}}}, field='/value')
        # Link contradictions: absent state and non-pointer families link
        # nothing; the pointer's link follows its stored page exactly.
        self.error('INVALID_REQUEST', c.validate, 'personal_reader_preference', {**absent[0][1], 'page_position_url': '/api/v1/x'}, field='/page_position_url')
        self.error('INVALID_REQUEST', c.validate, 'personal_page_position', {'exists': True, 'value': {'page_query': '', 'page_fragment': '', 'position': {}}, 'revision': 1, 'etag': '"sha256-page"', 'page_position_url': '/api/v1/x'}, field='/page_position_url')
        self.error('INVALID_REQUEST', c.validate, 'personal_reading_position', {**pointer, 'page_position_url': None}, field='/page_position_url')
        self.error('INVALID_REQUEST', c.validate, 'personal_reading_position', {**pointer, 'page_position_url': None}, field='/page_position_url')
        self.error('INVALID_REQUEST', c.validate, 'personal_reading_position', {**pointer, 'value': {**pointer['value'], 'page_path': None}}, field='/page_position_url')
        # Wrong-typed stored values raise the typed ContractError from shape
        # or semantic validation, never an uncaught crash from this block.
        self.error('INVALID_REQUEST', c.validate, 'personal_reading_position', {'exists': True, 'value': [1], 'revision': 1, 'etag': '"sha256-x"', 'page_position_url': None})
        self.error('INVALID_REQUEST', c.validate, 'personal_reading_position', {'exists': True, 'value': 1, 'revision': 1, 'etag': '"sha256-x"', 'page_position_url': '/api/v1/link'})
