"""C1–C5 and supplemental behavior contracts; no production claim."""
from copy import deepcopy
from pathlib import Path
import io
import json
import re
import unittest
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
    return {'generation': generation, 'inventory_sha256': c.digest(raw),
            'accepted_bytes': str(len(raw)), 'complete_input': True}


class Contracts(unittest.TestCase):
    def test_Task00204_upload_validators_and_end_input_declaration(self):
        upload = self.model.create(key(701), {'purpose': 'result', 'target': {'project_id': P, 'document_id': None}, 'input_mode': 'directory'})
        before = self.model.summary(upload)
        confirmed = {'generation': 4, 'inventory_sha256': c.digest(b'ledger'), 'accepted_bytes': '1048576', 'complete_input': False}
        end_input = dict(confirmed, generation=confirmed['generation'] + 1, complete_input=True)
        c.validate('checkpoint', confirmed)
        c.validate('checkpoint', end_input)
        self.assertFalse(confirmed['complete_input'])
        self.assertEqual(end_input['accepted_bytes'], confirmed['accepted_bytes'])
        self.assertEqual(end_input['inventory_sha256'], confirmed['inventory_sha256'])
        self.assertEqual(before['revision'], 1)
        for field in ('revision', 'etag'):
            invalid = dict(before)
            del invalid[field]
            with self.assertRaises(c.ContractError):
                c.validate('upload_summary', invalid)
        self.model.abandon(upload)
        after = self.model.summary(upload)
        self.assertGreater(after['revision'], before['revision'])
        self.assertNotEqual(after['etag'], before['etag'])

    def test_Task00203_selected_auth_control_shapes(self):
        c.validate('pairing_create', {'device_label': 'Remembered 기기'})
        c.validate('pairing_poll', {'polling_secret': 'a' * 64})
        c.validate('pairing_decision', {'code': 'comparison', 'origin': 'http://127.0.0.1:9270'})
        for body in ({}, {'code': None, 'origin': 'http://127.0.0.1:9270'},
                     {'code': 'x', 'origin': 'null'},
                     {'code': 'x', 'origin': 'http://127.0.0.1:9270', 'expected_state': 'pending'}):
            with self.assertRaises(c.ContractError):
                c.validate('pairing_decision', body)
        with self.assertRaises(c.ContractError):
            c.strict_json('{"code":"a","code":"b","origin":"http://127.0.0.1:9270"}')
        with self.assertRaises(c.ContractError):
            c.validate('pairing_create', {'device_label': 'x' * 129})
        with self.assertRaises(c.ContractError):
            c.validate('pairing_poll', {'polling_secret': None})
        c.validate('pairing_list', {'items': [], 'next_cursor': None})
        c.validate('device_session_list', {'items': [], 'next_cursor': None})
        for shape in ('pairing_list', 'device_session_list'):
            with self.assertRaises(c.ContractError):
                c.validate(shape, [])
            with self.assertRaises(c.ContractError):
                c.validate(shape, {'items': []})
        self.assertEqual(c.ERROR_HTTP['SESSION_REVOKED'], 401)
        self.assertEqual(c.ERROR_HTTP['SERVICE_RECOVERING'], 503)

    def setUp(self):
        self.model = c.Model()
        self.next_key = 0

    def newkey(self):
        self.next_key += 1
        return key(self.next_key)

    def error(self, code, function, *args, field=None, **kwargs):
        with self.assertRaises(c.ContractError) as raised:
            function(*args, **kwargs)
        self.assertEqual(raised.exception.code, code)
        self.assertEqual(raised.exception.http, c.ERROR_HTTP[code])
        if field is not None:
            self.assertEqual(raised.exception.field, field)
        return raised.exception

    def staged(self, target=None, raw=b'complete original bytes', purpose='result', sealed=True):
        target = deepcopy(target or {'project_id': P, 'document_id': D})
        body = {'purpose': purpose, 'target': target,
                'input_mode': 'directory' if purpose == 'result' else 'object',
                'selection_name': 'Explanation'}
        upload = self.model.create(self.newkey(), body)
        cp = checkpoint(raw)
        self.model.write(upload, raw, cp)
        if sealed:
            self.model.complete(self.newkey(), upload, cp)
            self.assertTrue(self.model.settle(upload, 1))
        return upload, cp

    def test_C1_01_draft_scope_never_publishes(self):
        target = {'project_id': P, 'document_id': D, 'draft_id': R}
        body = {'purpose': 'result', 'input_mode': 'directory', 'target': target}
        upload = self.model.create(self.newkey(), body, {'draft.write'})
        self.assertEqual(self.model.uploads[upload]['state'], 'open')
        self.error('FORBIDDEN', self.model.import_result, self.newkey(),
                   {'upload_id': upload, 'document_id': D}, {'draft.write'})
        self.assertEqual(self.model.versions, {})

    def test_C1_02_replacement_exact_draft_history(self):
        draft = {'project_id': P, 'document_id': D, 'draft_id': R,
                 'state': 'ready', 'base_version_id': B, 'revision': 3, 'history': ['old']}
        for wrong in (None, OTHER):
            upload, _ = self.staged({'project_id': P, 'document_id': D, 'draft_id': wrong})
            before = deepcopy(draft)
            target = deepcopy(self.model.uploads[upload]['target'])
            self.error('INVALID_REQUEST', self.model.replace_draft, upload, draft)
            self.assertEqual(draft, before)
            self.assertEqual(self.model.uploads[upload]['target'], target)
        upload, _ = self.staged({'project_id': P, 'document_id': D, 'draft_id': R})
        self.model.replace_draft(upload, draft)
        self.assertEqual((draft['base_version_id'], draft['revision'], draft['history'][0]), (B, 4, 'old'))
        self.assertEqual(self.model.versions, {})

    def test_C1_03_body_consumer_authority_binding(self):
        operation = self.newkey()
        target = {'operation_id': operation, 'action': 'question', 'version_id': V, 'page_path': None}
        body = {'purpose': 'body', 'target': target, 'input_mode': 'object'}
        self.error('FORBIDDEN', self.model.create, self.newkey(), body, {'context.write'})
        upload, _ = self.staged(target, purpose='body')
        for action, actual_operation in [('annotation_create', operation), ('question', self.newkey())]:
            wrong = {**target, 'action': action}
            self.error('INVALID_REQUEST', self.model.adopt_object, upload, wrong, actual_operation)
        self.assertEqual(self.model.adopt_object(upload, target, operation), c.digest(b'complete original bytes'))
        final_key = self.newkey()
        meta_target = {'operation_id': final_key, 'action': 'import', 'project_id': P, 'document_id': D}
        meta_upload, _ = self.staged(meta_target, raw=b'{"document":{"title":"Bound metadata"}}', purpose='metadata')
        reference = self.model.object_reference(meta_upload)
        result_upload, _ = self.staged()
        self.model.latest[D] = B
        final_body = {'upload_id': result_upload, 'document_id': D, 'base_version_id': B, 'metadata_ref': reference}
        self.error('INVALID_REQUEST', self.model.import_result, self.newkey(), final_body, field='/metadata_ref')
        self.assertEqual(self.model.import_result(final_key, final_body)['status'], 201)
        self.assertTrue(self.model.uploads[meta_upload]['adopted'])
        for action, purpose in [('import', 'metadata'), ('draft_replace', 'metadata'), ('generation', 'body'), ('annotation_edit', 'body'), ('evidence_verification', 'body')]:
            fields = {'import': {'project_id': P, 'document_id': None}, 'draft_replace': {'draft_id': R}, 'generation': {'project_id': P, 'document_id': D}, 'annotation_edit': {'annotation_id': R}, 'evidence_verification': {'version_id': V, 'evidence_id': '근거'}}[action]
            c.validate('upload_create', {'purpose': purpose, 'input_mode': 'object', 'target': {'operation_id': self.newkey(), 'action': action, **fields}})

        # Exercise the delegated source-read consumer through actual staging and seal.
        operation = self.newkey()
        target = {'operation_id': operation, 'action': 'evidence_verification',
                  'version_id': V, 'evidence_id': '근거'}
        body = {'purpose': 'body', 'input_mode': 'object', 'target': target}
        scopes = {'version.read', 'source.read'}
        for insufficient in ({'version.read'}, {'context.write'}, set()):
            before = deepcopy(self.model.__dict__)
            self.error('FORBIDDEN', self.model.create, self.newkey(), body, insufficient)
            self.assertEqual(self.model.__dict__, before)
        upload = self.model.create(self.newkey(), body, scopes)
        raw = b'complete verification body'
        cp = checkpoint(raw)
        self.model.write(upload, raw, cp)
        self.model.complete(self.newkey(), upload, cp)
        self.assertTrue(self.model.settle(upload, 1))
        for insufficient in ({'version.read'}, {'context.write'}, set()):
            before = deepcopy(self.model.__dict__)
            self.error('FORBIDDEN', self.model.adopt_object, upload, target, operation, insufficient)
            self.assertEqual(self.model.__dict__, before)
        for wrong, actual_operation, authority in [
            (target, self.newkey(), scopes),
            ({**target, 'version_id': OTHER}, operation, scopes),
            ({**target, 'evidence_id': '다른 근거'}, operation, scopes),
            ({**target, 'action': 'generation'}, operation, scopes | {'generation.request'}),
        ]:
            before = deepcopy(self.model.__dict__)
            self.error('INVALID_REQUEST', self.model.adopt_object, upload, wrong,
                       actual_operation, authority, field='/target')
            self.assertEqual(self.model.__dict__, before)
        before = deepcopy(self.model.uploads[upload])
        self.assertEqual(self.model.adopt_object(upload, target, operation, scopes), c.digest(raw))
        self.assertEqual(self.model.uploads[upload]['pins'], {**before['pins'], 'adopted': before['pins']['adopted'] + 1})
        self.assertTrue(self.model.uploads[upload]['adopted'])
        self.assertEqual(self.model.uploads[upload]['bytes'], before['bytes'])
        self.assertEqual(self.model.uploads[upload]['target'], before['target'])

        self._metadata_identity_regression()

    def test_C1_04_context_missing_null_literal_and_query(self):
        target = {'version_id': V, 'page_path': 'null', 'render_instance_id': R}
        upload, _ = self.staged(target, purpose='context')
        actual = c.context_binding([('version_id', V), ('page_path', 'null')])
        self.assertIsNone(actual['render_instance_id'])
        self.assertEqual(actual['page_path'], 'null')
        self.error('INVALID_REQUEST', self.model.adopt_object, upload, actual, self.newkey())
        self.assertEqual(c.context_binding([('version_id', V)]), {'version_id': V, 'page_path': None, 'render_instance_id': None})
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
        final, seal = self.newkey(), self.newkey()
        self.model.complete(seal, upload, cp)
        self.model.settle(upload, 1)
        self.model.latest[D] = B
        result = self.model.import_result(final, {'upload_id': upload, 'document_id': D, 'base_version_id': B})
        self.assertEqual(result['status'], 201)
        self.assertEqual(len(self.model.receipts), 3)
        self.error('OPERATION_CONFLICT', self.model.request, final, '/other', {}, lambda: 'wrong')
        self.error('PRECONDITION_REQUIRED', self.model.request, None, '/route', {}, lambda: 'wrong', field='/Idempotency-Key')
        for bad in ['"' + final + '"', [final, seal], final.upper().replace('00000000', 'ABCDEFAB'), 'wrong']:
            self.error('INVALID_REQUEST', self.model.request, bad, '/route', {}, lambda: 'wrong', field='/Idempotency-Key')

    def test_C2_02_response_loss_caller_journal(self):
        upload, cp = self.staged()
        journal = {'final_key': self.newkey(), 'create_key': key(1), 'seal_key': key(2), 'target': {'project_id': P, 'document_id': D}, 'base': B, 'phase': 'before-final-dispatch', 'upload_id': upload, 'checkpoint': cp}
        recovered = c.recover_journal(journal, None)
        self.assertEqual(recovered['journal'], journal)
        self.assertFalse(recovered['publication_known_absent'])
        self.assertFalse(recovered['new_dispatch_authorized'])
        self.model.latest[D] = B
        body = {'upload_id': upload, 'document_id': D, 'base_version_id': B}
        first = self.model.import_result(journal['final_key'], body)
        self.assertEqual(self.model.import_result(journal['final_key'], body), first)
        self.assertEqual(len(self.model.versions), 1)

    def test_C2_03_future_Q_not_current_U(self):
        U, Q = self.newkey(), self.newkey()
        target = {'operation_id': Q, 'action': 'import', 'project_id': P, 'document_id': D}
        body = {'purpose': 'metadata', 'input_mode': 'object', 'target': target}
        upload = self.model.create(U, body)
        self.assertNotEqual(U, Q)
        self.assertEqual(self.model.create(U, body), upload)
        changed = deepcopy(body); changed['target']['operation_id'] = self.newkey()
        self.error('OPERATION_CONFLICT', self.model.create, U, changed)
        self.assertNotIn(Q, self.model.receipts)

    def test_C2_04_same_checkpoint_new_keys_one_job(self):
        upload, cp = self.staged(sealed=False)
        first_key, second_key = self.newkey(), self.newkey()
        first = self.model.complete(first_key, upload, cp)
        self.assertEqual(self.model.complete(second_key, upload, deepcopy(cp)), first)
        self.assertEqual(len(self.model.jobs), 1)
        changed = {**cp, 'generation': 2}
        self.error('INVALID_TRANSITION', self.model.complete, self.newkey(), upload, changed)
        self.model.settle(upload, 1)
        self.assertEqual(self.model.complete(first_key, upload, cp)['status'], 202)
        self.assertEqual(self.model.complete(self.newkey(), upload, cp)['status'], 200)
        self.assertEqual(len(self.model.jobs), 1)

    def test_C2_05_equivalent_transport_replays_original_intent(self):
        first, _ = self.staged({'project_id': P, 'document_id': None})
        second, _ = self.staged({'project_id': P, 'document_id': None})
        operation = self.newkey()
        body = {'upload_id': first, 'document_id': None, 'metadata': {'document': {'title': 'Recorded'}}}
        result = self.model.import_result(operation, body)
        self.assertEqual(self.model.import_result(operation, {**body, 'upload_id': second}), result)
        for change in [{'metadata': {'document': {'title': 'Changed'}}}, {'version_id': V}, {'base_version_id': None}]:
            self.error('OPERATION_CONFLICT', self.model.import_result, operation, {**body, **change})
        another_project, _ = self.staged({'project_id': OTHER, 'document_id': None})
        self.error('OPERATION_CONFLICT', self.model.import_result, operation, {**body, 'upload_id': another_project})
        self.assertEqual(len(self.model.versions), 1)

    def test_C3_01_omitted_is_not_null(self):
        upload, _ = self.staged({'project_id': P, 'document_id': None})
        self.error('INVALID_REQUEST', self.model.import_result, self.newkey(), {'upload_id': upload}, field='/document_id')
        operation = self.newkey(); body = {'upload_id': upload, 'document_id': None}
        result = self.model.import_result(operation, body)
        self.assertIsNotNone(result['document_id'])
        self.assertEqual(self.model.import_result(operation, body), result)
        self.error('INVALID_REQUEST', self.model.import_result, self.newkey(), {'upload_id': upload, 'document_id': D})

    def test_C3_02_pretransfer_base_survives_publication(self):
        self.model.latest[D] = B
        upload, _ = self.staged()
        body = {'upload_id': upload, 'document_id': D, 'base_version_id': B}
        self.model.latest[D] = OTHER
        operation = self.newkey()
        self.error('VERSION_CONFLICT', self.model.import_result, operation, body)
        self.assertTrue(self.model.reusable(upload))
        self.assertEqual(self.model.latest[D], OTHER)
        self.assertEqual(self.model.import_result(self.newkey(), {**body, 'base_version_id': OTHER})['status'], 201)

    def test_C3_03_omitted_base_reservation_once_and_replay_order(self):
        self.model.latest[D] = B
        upload, _ = self.staged()
        self.model.latest[D] = OTHER  # after create/seal, before API reservation
        operation = self.newkey(); body = {'upload_id': upload, 'document_id': D}
        self.model.import_result(operation, body, reserve_only=True)
        saved = deepcopy(self.model.plans[operation])
        self.assertEqual(saved['normalized']['base_version_id'], OTHER)
        self.assertIn('base_version_id', saved['normalized']['omitted_fields'])
        self.model.latest[D] = V
        self.assertEqual(self.model.import_result(operation, body)['state'], 'accepted')
        self.assertEqual(self.model.plans[operation], saved)
        self.error('VERSION_CONFLICT', self.model.publish, saved['normalized'], saved['semantic'])
        self.model.latest[D] = OTHER
        result = self.model.import_result(self.newkey(), body)
        self.assertEqual(result['status'], 201)
        completed = next(k for k, v in self.model.receipts.items() if v['result'] == result)
        self.model.latest[D] = V
        self.assertEqual(self.model.import_result(completed, body), result)
        self.error('FORBIDDEN', self.model.import_result, completed, body, {'draft.write'})

    def test_C3_04_client_association_precedence_no_fallback(self):
        remembered = {'project_id': OTHER, 'document_id': D}
        docs = {D: P}
        self.assertEqual(c.association(remembered, {'document_id': D}, docs), {'project_id': P, 'document_id': D})
        self.assertEqual(c.association(remembered, {'project_id': P}, docs), {'project_id': P, 'document_id': None})
        self.assertEqual(c.association(remembered, {'new_document': True}, docs), {'project_id': OTHER, 'document_id': None})
        self.assertEqual(c.association({}, {}, docs, unfiled=P)['project_id'], P)
        self.error('NOT_FOUND', c.association, {}, {}, docs, owner=False, unfiled=P)
        self.error('NOT_FOUND', c.association, remembered, {}, {})
        self.error('INVALID_REQUEST', c.association, remembered, {'project_id': OTHER, 'document_id': D}, docs)
        self.error('INVALID_REQUEST', c.association, remembered, {'new_document': True, 'document_id': D}, docs)

    def test_C3_05_explicit_immutable_version_conflict(self):
        self.model.latest[D] = B
        upload, _ = self.staged()
        body = {'upload_id': upload, 'document_id': D, 'base_version_id': B, 'version_id': V}
        self.assertEqual(self.model.import_result(self.newkey(), body)['status'], 201)
        body['base_version_id'] = V
        self.assertEqual(self.model.import_result(self.newkey(), body)['status'], 200)
        changed, _ = self.staged(raw=b'changed immutable bytes')
        before = deepcopy(self.model.versions[V])
        self.error('IMMUTABLE_VERSION_CONFLICT', self.model.import_result, self.newkey(), {**body, 'upload_id': changed})
        self.assertEqual(self.model.versions[V], before)

    def test_C4_01_cancel_fences_until_confirmed_stop_retains(self):
        upload, cp = self.staged(sealed=False)
        old_key = self.newkey(); job = self.model.complete(old_key, upload, cp)['job_id']
        self.model.cancel(upload)
        self.assertEqual(self.model.uploads[upload]['state'], 'sealing')
        self.assertFalse(self.model.settle(upload, 1))
        self.error('INVALID_TRANSITION', self.model.write, upload, b'wrong', checkpoint(b'wrong'))
        self.assertFalse(self.model.settle(upload, 1, stopped=True))
        self.assertEqual(self.model.uploads[upload]['state'], 'open')
        self.assertEqual(self.model.uploads[upload]['bytes'], b'complete original bytes')
        self.assertEqual(self.model.jobs[job]['state'], 'cancelled')
        next_job = self.model.complete(self.newkey(), upload, cp)['job_id']
        self.assertNotEqual(job, next_job)
        self.assertEqual(self.model.complete(old_key, upload, cp)['job_id'], job)

    def test_C4_02_commit_wins_late_cancel(self):
        upload, _ = self.staged()
        self.assertEqual(self.model.cancel(upload), 'already_completed')
        self.assertEqual(self.model.uploads[upload]['state'], 'sealed')
        self.assertEqual(self.model.jobs[self.model.uploads[upload]['job']]['state'], 'completed')

    def test_C4_03_abandon_fences_active_work_and_pins(self):
        upload, cp = self.staged(sealed=False)
        self.model.complete(self.newkey(), upload, cp)
        self.model.abandon(upload)
        state = self.model.uploads[upload]
        self.assertEqual(state['state'], 'abandoned')
        self.assertEqual(state['pins']['active_work'], 1)
        self.assertFalse(self.model.settle(upload, 1))
        self.assertEqual(self.model.jobs[state['job']]['state'], 'cancelling')
        self.assertFalse(self.model.reusable(upload))

    def test_C4_04_abandon_adopted_preserves_references(self):
        target = {'version_id': V, 'page_path': None, 'render_instance_id': None}
        upload, _ = self.staged(target, purpose='context')
        adopted = self.model.adopt_object(upload, target, self.newkey())
        self.model.uploads[upload]['pins']['recovery'] = 1
        self.model.abandon(upload)
        self.assertEqual(self.model.jobs[self.model.uploads[upload]['job']]['state'], 'completed')
        self.assertTrue(self.model.uploads[upload]['adopted'])
        self.assertEqual(self.model.uploads[upload]['pins']['adopted'], 1)
        self.assertEqual(self.model.uploads[upload]['pins']['recovery'], 1)
        self.assertEqual(c.digest(self.model.uploads[upload]['bytes']), adopted)

    def test_C4_05_late_failed_result_cannot_reopen(self):
        upload, cp = self.staged(sealed=False)
        self.model.complete(self.newkey(), upload, cp); self.model.abandon(upload)
        self.assertFalse(self.model.settle(upload, 1, success=False))
        self.assertEqual(self.model.uploads[upload]['state'], 'abandoned')
        self.assertFalse(self.model.settle(upload, 1, success=False, stopped=True))
        self.assertEqual(self.model.uploads[upload]['state'], 'abandoned')
        self.assertEqual(self.model.uploads[upload]['pins']['active_work'], 0)

    def test_C4_06_simulated_recovery_coupled_state(self):
        upload, cp = self.staged(sealed=False)
        self.model.complete(self.newkey(), upload, cp); self.model.cancel(upload)
        snapshot = deepcopy(self.model.__dict__)  # simulated checkpoint, not crash durability
        recovered = c.Model(); recovered.__dict__.update(snapshot)
        self.error('INVALID_TRANSITION', recovered.write, upload, b'new', checkpoint(b'new'))
        recovered.settle(upload, 1, stopped=True)
        self.assertEqual(recovered.uploads[upload]['state'], 'open')
        self.assertEqual(recovered.uploads[upload]['bytes'], b'complete original bytes')
        self.assertEqual(recovered.jobs[recovered.uploads[upload]['job']]['state'], 'cancelled')
        self.error('INVALID_TRANSITION', recovered.resume, recovered.uploads[upload]['job'])
        recovered.abandon(upload)
        self.error('INVALID_TRANSITION', recovered.complete, self.newkey(), upload, cp)

    def test_C5_01_summary_and_owner_recovery(self):
        upload, _ = self.staged()
        summary = self.model.summary(upload)
        expected = {'upload_id', 'originating_principal_id', 'originating_device_id', 'purpose', 'input_mode', 'selection_name', 'target', 'state', 'byte_size', 'staging_bytes', 'created_at', 'updated_at', 'reusable', 'unavailable_reason', 'pin_summary', 'pins_url', 'detail_url', 'revision', 'etag'}
        self.assertEqual(set(summary), expected)
        self.assertNotIn('error', summary)
        self.assertTrue(summary['reusable'])
        self.assertFalse(self.model.summary(upload, authorized=False)['reusable'])
        self.assertIsNone(summary['originating_device_id'])  # no original device credential needed

    def test_C5_02_detail_exact_nullable_and_cursor_resources(self):
        upload, _ = self.staged()
        summary, detail = self.model.summary(upload), self.model.summary(upload, detail=True)
        extra = {'checkpoint', 'ranges_url', 'entries_url', 'error', 'seal_job_id', 'seal_operation_id', 'sealed_content_set_id', 'sealed_object_ref'}
        self.assertEqual(set(detail) - set(summary), extra)
        self.assertEqual({k: detail[k] for k in summary}, summary)
        self.assertIsNone(detail['sealed_object_ref'])
        c.validate('object_ref', {'object_id': V, 'bytes_sha256': c.digest(b'object'), 'byte_size': '6', 'media_type': 'text/plain'})
        self.error('INVALID_REQUEST', c.validate, 'upload_summary', detail)
        self.error('INVALID_REQUEST', c.validate, 'upload_detail', {k: v for k, v in detail.items() if k != 'error'}, field='/error')

        self._unavailable_detail_regression()

    def test_C5_03_sealed_not_necessarily_reusable(self):
        upload, _ = self.staged()
        self.model.uploads[upload]['available'] = False
        summary = self.model.summary(upload)
        self.assertEqual(summary['state'], 'sealed')
        self.assertFalse(summary['reusable'])
        self.assertIsNotNone(summary['unavailable_reason'])
        self.assertEqual(self.model.uploads[upload]['bytes'], b'complete original bytes')
        self.model.uploads[upload]['available'] = True
        self.model.uploads[upload]['adopted'] = True
        self.model.uploads[upload]['pins']['adopted'] = 1
        self.assertTrue(self.model.reusable(upload))
        self.assertEqual(self.model.uploads[upload]['state'], 'sealed')  # elapsed age has no state transition
        open_upload = self.model.create(self.newkey(), {'purpose': 'result', 'input_mode': 'file', 'target': {'project_id': P, 'document_id': None}})
        self.assertFalse(self.model.summary(open_upload, detail=True)['checkpoint']['complete_input'])

    def test_SUP_PACKAGE_raw_examples_and_metadata(self):
        text = (Path(__file__).parents[2] / 'docs/examples/package-example.md').read_text()
        payload = {}
        for name, block in re.findall(r'## Payload file: `([^`]+)`\n\n```[^\n]*\n(.*?)```', text, re.S):
            payload[name] = ('file', block.encode())
        payload['exports'] = ('directory', None)
        submitted_text = text.split('## Optional submitted metadata')[1].split('```json\n')[1].split('```')[0]
        generated_text = text.split('## Generated service metadata')[1].split('```json\n')[1].split('```')[0]
        declared_inventory = text.split('```jsonl\n')[1].split('```')[0].encode()
        self.assertEqual(c.inventory(payload), declared_inventory)
        self.assertEqual(c.digest(declared_inventory), 'df1a2aaa44139da7592c4a5c158addfec74b438351c5561ca8e64b4cfe6363da')
        self.assertEqual(c.digest(submitted_text.encode()), '1f2cf1cb072cbabb9f3f0a5722d031cefec0f42d27bf86bae6cf30d1f303f7ae')
        self.assertEqual(c.digest(generated_text.encode()), '7e8b471f187350c2781098e6038aa7d89bc70b3d7d441bd4bc63334b14a95f86')
        submitted, generated = c.strict_json(submitted_text), c.strict_json(generated_text)
        c.metadata(submitted, payload); c.validate('generated_metadata', generated)
        self.assertEqual(c.metadata(None, payload)['source_basis'], 'unverified')
        self.assertEqual(c.capture(('single.htm', b'<p>single'), 'file'), {'single.htm': ('file', b'<p>single')})
        richer = {**payload, '.hidden': ('file', b'secret-shaped ordinary payload'), 'unknown.weird': ('file', b'\x00\xff'), 'nested/empty': ('directory', None), 'alias': ('symlink', 'pages/결과.html')}
        richer.pop('index.html')
        archive_bytes = io.BytesIO()
        with zipfile.ZipFile(archive_bytes, 'w') as archive:
            for name, (kind, raw) in richer.items():
                info = zipfile.ZipInfo(name + ('/' if kind == 'directory' else ''))
                if kind == 'symlink': info.external_attr = 0o120777 << 16
                archive.writestr(info, raw if kind == 'file' else raw.encode() if kind == 'symlink' else b'')
        self.assertEqual(c.inventory(c.capture(archive_bytes.getvalue(), 'archive')), c.inventory(richer))
        self.assertEqual(c.capture(richer, 'directory'), richer)
        bad = deepcopy(submitted); bad['sources'][0]['snapshot_sha256'] = '0' * 64
        self.error('INVALID_EVIDENCE', c.metadata, bad, payload, field='/sources/0/snapshot_sha256')
        bad = deepcopy(submitted); bad['document']['extra'] = True
        self.error('PACKAGE_INVALID', c.metadata, bad, payload, field='/document/extra')
        git = {'id': 'git-source', 'kind': 'git', 'repo_id': P, 'object_format': 'sha1', 'exact_revision': 'a' * 40, 'retention_mode': 'selected_snapshot', 'asset_path': 'source.txt', 'bytes_sha256': c.digest(payload['source.txt'][1])}
        c.metadata({'sources': [git]}, payload)
        self.error('INVALID_EVIDENCE', c.metadata, {'sources': [{k: v for k, v in git.items() if k != 'bytes_sha256'}]}, payload)
        self.error('INVALID_EVIDENCE', c.metadata, {'sources': [{**git, 'snapshot_sha256': 'b' * 64}]}, payload)
        self.assertEqual(payload['source.txt'][1], b'Fixture rule: successful roles remain completed; only failed roles are queued again.\n')

        self._utc_regression(generated)

    def test_SUP_INVENTORY_exact_bytes_equivalence_and_links(self):
        entries = {'한글.htm': ('file', b'a'), 'A': ('file', b'A'), 'a': ('file', b'a'), 'line\n\t"\\': ('file', b''), 'empty': ('directory', None), 'link': ('symlink', '한글.htm')}
        actual = c.inventory(entries)
        self.assertEqual([json.loads(x)[0] for x in actual.splitlines()], sorted(entries, key=lambda x: x.encode('utf-8')))
        self.assertIn(b'"0"', actual)
        self.assertIn(b'\\n\\t\\"\\\\', actual)
        self.assertNotIn(b'\\uD55C', actual)
        self.assertEqual(c.lookup(entries, 'link', True)['resolved_path'], '한글.htm')
        self.assertEqual(c.lookup(entries, 'link')['kind'], 'symlink')
        self.assertEqual(c.lookup({'e\u0301': ('file', b'one')}, '\u00e9')['resolved_path'], 'e\u0301')
        ambiguous = {'\u00c5': ('file', b'one'), 'A\u030a': ('file', b'two')}
        self.error('INVALID_REQUEST', c.lookup, ambiguous, '\u212b')
        self.assertEqual(c.lookup(ambiguous, '\u00c5')['data'], b'one')
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
            valid = deepcopy(default); valid['maintenance']['schedule'].update(mode=mode, interval_seconds=interval, cron=cron)
            c.configuration(valid)
        for mode, interval, cron in [('manual', 60, None), ('interval', None, None), ('calendar', None, '99 * * * *'), ('calendar', None, '* * *')]:
            bad = deepcopy(default); bad['maintenance']['schedule'].update(mode=mode, interval_seconds=interval, cron=cron)
            self.error('INVALID_REQUEST', c.configuration, bad)
        for bad_origin in ['https://*.example', 'https://user:pass@example', 'https://example/path', 'http://example', 'https://example#x']:
            bad = deepcopy(default); bad['network'].update(remote_enabled=True, external_base_url=bad_origin, allowed_origins=['http://127.0.0.1:9270', bad_origin])
            self.error('INVALID_REQUEST', c.configuration, bad)
        self.model.policy = deepcopy(default)
        first = self.model.commit_config(P, 1, default, True)
        self.assertEqual(first, {'config_revision': 2, 'permissions_revision': 1})
        self.assertEqual(self.model.commit_config(P, 1, default, True, alias=D, expired=True), first)
        self.assertEqual(self.model.config_revision, 2)
        changed = deepcopy(default); changed['source']['default_retention'] = 'selected_snapshot'
        self.error('OPERATION_CONFLICT', self.model.commit_config, P, 1, changed, True)
        self.assertEqual(self.model.commit_config(R, 2, changed, True)['permissions_revision'], 2)
        bad = deepcopy(changed); bad['unknown'] = True
        before = deepcopy(self.model.__dict__)
        self.error('INVALID_REQUEST', self.model.commit_config, V, 3, bad, True)
        self.assertEqual(self.model.__dict__, before)
        self.error('INVALID_REQUEST', self.model.commit_config, V, 3, changed, False, field='/confirmed')
        self.error('STALE_PLAN', self.model.commit_config, V, 3, changed, True, expired=True)
        presentation = {'projects': {P: {'name': 'Presentation only', 'maintenance': {'depth': 'full_scope'}}}}
        self.assertEqual(self.model.commit_config(V, 3, presentation, True), {'config_revision': 4, 'permissions_revision': 2})
        self.assertEqual(self.model.policy['maintenance'], changed['maintenance'])
        self.assertEqual(self.model.policy['projects'][P]['maintenance']['depth'], 'full_scope')
        self.assertEqual(self.model.policy['source'], changed['source'])
        partial = {'freshness_assessment': 'disabled'}
        override = {'projects': {P: {'maintenance': {'auto_apply': partial}}}}
        submitted = c.merge_configuration(default, override)
        c.validate('config', submitted)
        self.assertEqual(c.configuration(submitted), submitted)
        self.assertEqual(self.model.commit_config(self.newkey(), 4, override, True),
                         {'config_revision': 5, 'permissions_revision': 2})
        effective = c.merge_configuration(
            self.model.policy['maintenance'],
            self.model.policy['projects'][P]['maintenance'])
        expected_actions = {**default['maintenance']['auto_apply'], **partial}
        self.assertEqual(effective['auto_apply'], expected_actions)
        self.assertEqual(self.model.policy['maintenance']['auto_apply'],
                         default['maintenance']['auto_apply'])
        self.assertEqual(self.model.policy['projects'][P]['maintenance']['auto_apply'], partial)
        self.assertEqual(c.configuration(c.merge_configuration(
            default, {'projects': {P: {'maintenance': {'auto_apply': {}}}}})),
            c.merge_configuration(default, {'projects': {P: {'maintenance': {'auto_apply': {}}}}}))
        for invalid, code, field in [
            ({'unknown_action': 'disabled'}, 'INVALID_REQUEST',
             '/projects/' + P + '/maintenance/auto_apply/unknown_action'),
            ({'freshness_assessment': 'unknown'}, 'INVALID_ENUM',
             '/projects/' + P + '/maintenance/auto_apply/freshness_assessment'),
            ({'freshness_assessment': True}, 'INVALID_ENUM',
             '/projects/' + P + '/maintenance/auto_apply/freshness_assessment'),
            (None, 'INVALID_REQUEST', '/projects/' + P + '/maintenance/auto_apply')]:
            before = deepcopy(self.model.__dict__)
            invalid_override = {'projects': {P: {'maintenance': {'auto_apply': invalid}}}}
            self.error(code, self.model.commit_config, self.newkey(), 5,
                       invalid_override, True, field=field)
            self.assertEqual(self.model.__dict__, before)
        incomplete = deepcopy(default)
        del incomplete['maintenance']['auto_apply']['topic_assignment']
        self.error('INVALID_REQUEST', c.configuration, incomplete,
                   field='/maintenance/auto_apply/topic_assignment')
        self.error('OPERATION_CONFLICT', self.model.commit_config, OTHER, 4, {}, True, alias=P)
        unknown = deepcopy(default); unknown['network']['allowed_origins'] += ['https://unknown.example']
        self.error('INVALID_REQUEST', c.configuration, unknown, field='/network/allowed_origins')
        self.error('UNSUPPORTED_SCHEMA', c.configuration, {**default, 'schema_version': '2.0'}, field='/schema_version')
        wrong = deepcopy(default); wrong['security']['pairing_expiry_seconds'] = True
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
        for page, region in [(None, None), ('nested/page.html', None),
                             ('nested/page.html', 'M11'), ('null', 'M11')]:
            reference = {**explanation, 'page_path': page, 'region_id': region}
            self.assertEqual(c.validate('explanation_reference', reference), reference)
        self.error('INVALID_REQUEST', c.validate, 'explanation_reference',
                   {**explanation, 'page_path': None}, field='/page_path')
        self.error('INVALID_REQUEST', c.validate, 'explanation_reference',
                   {**explanation, 'region_id': 7}, field='/region_id')
        self.error('INVALID_REQUEST', c.validate, 'explanation_reference',
                   {**explanation, 'page_path': ''}, field='/page_path')
        missing_page = {k: v for k, v in explanation.items() if k != 'page_path'}
        self.error('INVALID_REQUEST', c.validate, 'explanation_reference', missing_page,
                   field='/page_path')
        self.error('INVALID_REQUEST', c.validate, 'explanation_reference', {**explanation, 'basis': 'observed_input'})
        self.assertEqual(c.coverage_status(False, 0, []), 'unable_to_verify')
        self.assertEqual(c.coverage_status(True, 0, []), 'not_assessed')
        self.assertEqual(c.coverage_status(True, 2, ['assessed', 'partial']), 'not_assessed')
        self.assertEqual(c.coverage_status(True, 2, ['assessed', 'assessed']), 'aligned_within_scope')
        self.assertEqual(c.coverage_status(True, 2, ['assessed', 'partial'], discrepancy=True), 'outdated_against_target')

    def test_SUP_COMPATIBILITY_directional_safe_required_and_capability(self):
        known = {'state': ['sealed'], 'authority': ['read_only']}
        response = {'state': 'sealed', 'authority': 'read_only', 'optional_label': 'new additive display'}
        self.assertEqual(c.compatibility('1.0', known, response), {'state': 'sealed', 'authority': 'read_only'})
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
            return operation, body, upload

        matching = {'document': {'id': D, 'project_id': P}, 'version': {'id': V, 'base_version_id': B}}
        for referenced in (False, True):
            prefix = '/metadata_ref' if referenced else '/metadata'
            for section, field in [('document', 'id'), ('document', 'project_id'), ('version', 'id'), ('version', 'base_version_id')]:
                bad = deepcopy(matching)
                bad[section][field] = OTHER
                for reservation in (False, True):
                    operation, body, upload = prepare(bad, referenced)
                    before = deepcopy(self.model.__dict__)
                    self.error('INVALID_REQUEST', self.model.import_result, operation, body,
                               reserve_only=reservation, field=prefix + '/' + section + '/' + field)
                    self.assertEqual(self.model.__dict__, before)
                    self.assertEqual(self.model.uploads[upload]['bytes'], b'complete original bytes')
            for metadata in (matching, {}):
                operation, body, _ = prepare(metadata, referenced)
                self.assertEqual(self.model.import_result(operation, body),
                                 {'status': 201, 'version_id': V, 'document_id': D})
            metadata = {'document': {'id': D, 'project_id': P}, 'version': {'base_version_id': B}}
            operation, body, _ = prepare(metadata, referenced, explicit=False)
            result = self.model.import_result(operation, body)
            self.model.latest[D] = OTHER
            before = deepcopy(self.model.__dict__)
            self.assertEqual(self.model.import_result(operation, body), result)
            self.assertEqual(self.model.__dict__, before)
            operation, body, _ = prepare({'version': {'id': OTHER}}, referenced, explicit=False)
            before = deepcopy(self.model.__dict__)
            self.error('INVALID_REQUEST', self.model.import_result, operation, body, field=prefix + '/version/id')
            self.assertEqual(self.model.__dict__, before)
            for metadata in ({}, {'document': {'project_id': P}, 'version': {'base_version_id': None}}):
                operation, body, _ = prepare(metadata, referenced, document=None, explicit=False)
                result = self.model.import_result(operation, body)
                self.assertEqual(result['status'], 201)
                self.assertIsNotNone(result['document_id'])
            operation, body, _ = prepare({'document': {'id': OTHER}}, referenced, document=None, explicit=False)
            before = deepcopy(self.model.__dict__)
            self.error('INVALID_REQUEST', self.model.import_result, operation, body, field=prefix + '/document/id')
            self.assertEqual(self.model.__dict__, before)
        operation, body, _ = prepare(None, False)
        self.assertEqual(self.model.import_result(operation, body)['status'], 201)

    def _unavailable_detail_regression(self):
        for purpose in ('context', 'body', 'metadata'):
            operation = self.newkey()
            target = {'context': {'version_id': V, 'page_path': None, 'render_instance_id': R},
                      'body': {'operation_id': operation, 'action': 'question', 'version_id': V, 'page_path': None},
                      'metadata': {'operation_id': operation, 'action': 'import', 'project_id': P, 'document_id': D}}[purpose]
            upload, _ = self.staged(target, purpose=purpose)
            reference = self.model.object_reference(upload)
            self.assertEqual(self.model.summary(upload, detail=True)['sealed_object_ref'], reference)
            self.model.uploads[upload]['available'] = False
            before = deepcopy(self.model.__dict__)
            summary, detail = self.model.summary(upload), self.model.summary(upload, detail=True)
            c.validate('upload_summary', summary)
            c.validate('upload_detail', detail)
            self.assertEqual({k: detail[k] for k in summary}, summary)
            self.assertEqual(detail['state'], 'sealed')
            self.assertFalse(detail['reusable'])
            self.assertIsNotNone(detail['unavailable_reason'])
            self.assertIsNone(detail['sealed_object_ref'])
            self.error('INVALID_TRANSITION', self.model.object_reference, upload)
            self.error('INVALID_REQUEST', self.model.adopt_object, upload, target, operation)
            self.assertEqual(self.model.__dict__, before)
            self.model.uploads[upload]['available'] = True
            self.assertEqual(self.model.object_reference(upload), reference)
            self.model.abandon(upload)
            self.assertIsNone(self.model.summary(upload, detail=True)['sealed_object_ref'])
            self.error('INVALID_TRANSITION', self.model.object_reference, upload)

    def _receipt_family_regression(self, default):
        body = {'purpose': 'result', 'input_mode': 'file', 'target': {'project_id': P, 'document_id': None}}
        for first_alias in (None, D):
            self.model = c.Model()
            self.model.policy = deepcopy(default)
            result = self.model.commit_config(P, 1, {}, True, alias=first_alias)
            self.assertEqual(result, {'config_revision': 2, 'permissions_revision': 1})
            for identity in {P, first_alias or P}:
                before = deepcopy(self.model.__dict__)
                self.error('OPERATION_CONFLICT', self.model.create, identity, body)
                self.error('FORBIDDEN', self.model.create, identity, body, set())
                self.error('FORBIDDEN', self.model.commit_config, P, 1, {}, True, authority=set())
                self.assertEqual(self.model.__dict__, before)
            for alias in (None, first_alias, V):
                self.assertEqual(self.model.commit_config(P, 1, {}, True, alias=alias, expired=True), result)
                self.assertEqual(self.model.config_revision, 2)
                self.assertEqual(self.model.permissions_revision, 1)
            before = deepcopy(self.model.__dict__)
            self.error('OPERATION_CONFLICT', self.model.commit_config, R, 2, {}, True, alias=P)
            self.error('OPERATION_CONFLICT', self.model.create, V, body)
            self.assertEqual(self.model.__dict__, before)
        for regular_key, alias in [(P, None), (P, D), (D, D)]:
            self.model = c.Model()
            self.model.policy = deepcopy(default)
            upload = self.model.create(regular_key, body)
            before = deepcopy(self.model.__dict__)
            self.error('OPERATION_CONFLICT', self.model.commit_config, P, 1, {}, True, alias=alias)
            self.assertEqual(self.model.__dict__, before)
            self.assertEqual(self.model.create(regular_key, body), upload)
            self.assertEqual(self.model.__dict__, before)

    def _utc_regression(self, generated):
        valid = ['0000-02-29T00:00:00Z', '0001-01-01T00:00:00Z', '2000-02-29T23:59:59Z',
                 '2400-02-29T00:00:00Z', '9999-12-31T23:59:59Z',
                 '2026-10-16T00:00:00.' + '1234567890' * 100 + 'Z']
        leap_days = '''1972-06-30 1972-12-31 1973-12-31 1974-12-31 1975-12-31
1976-12-31 1977-12-31 1978-12-31 1979-12-31 1981-06-30 1982-06-30
1983-06-30 1985-06-30 1987-12-31 1989-12-31 1990-12-31 1992-06-30
1993-06-30 1994-06-30 1995-12-31 1997-06-30 1998-12-31 2005-12-31
2008-12-31 2012-06-30 2015-06-30 2016-12-31'''.split()
        valid += [day + 'T23:59:60.12345678901234567890Z' for day in leap_days]
        invalid = ['2026-99-99T99:99:99Z', '2026-02-30T00:00:00Z', '1900-02-29T00:00:00Z',
                   '0001-02-29T00:00:00Z', '2026-04-31T00:00:00Z', '2026-01-01T24:00:00Z',
                   '2026-01-01T00:60:00Z', '2016-12-31T23:59:61Z', '2016-12-30T23:59:60Z',
                   '2016-12-31T23:58:60Z', '2017-12-31T23:59:60Z', '２０２６-01-01T00:00:00Z',
                   '2026-00-01T00:00:00Z', '2026-01-00T00:00:00Z', '10000-01-01T00:00:00Z',
                   '2026-01-01T00:00:00.Z']
        for value in valid:
            self.assertEqual(c.validate('time', value), value)
            nested = deepcopy(generated)
            nested['version']['created_at'] = value
            self.assertEqual(c.validate('generated_metadata', nested), nested)
        for value in invalid:
            self.error('INVALID_REQUEST', c.validate, 'time', value)
            nested = deepcopy(generated)
            nested['version']['created_at'] = value
            self.error('INVALID_REQUEST', c.validate, 'generated_metadata', nested, field='/version/created_at')


if __name__ == '__main__':
    unittest.main(verbosity=2)
