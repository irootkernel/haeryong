"""Selected additive host-local copy DTOs; synthetic contracts, not execution proof."""
import pytest
import contract as c

pytestmark = pytest.mark.integration
ID = '11111111-1111-4111-8111-111111111111'
STAMP = '2026-10-25T00:00:00Z'


def test_backup_status_retains_prior_set_failed_attempt_and_independent_trial():
    trial = dict(state='not_run', completed_at=None)
    complete = dict(set_id=ID, completed_at=STAMP, verified_at=STAMP, age_seconds=3)
    status = dict(mode='manual', available=True, last_complete_verified=complete,
                  last_attempt=dict(job_id=ID, state='failed', stage='copying', completed_at=None, error='insufficient_space'), last_trial_restore=trial)
    c.validate('backup_status', status)
    c.validate('backup_status', status | dict(last_complete_verified=None))
    with pytest.raises(c.ContractError):c.validate('backup_status', status | dict(last_complete_verified=complete | dict(age_seconds=-1)))
    with pytest.raises(c.ContractError):c.validate('backup_status', status | dict(last_trial_restore=dict(state='not_run')))
    with pytest.raises(c.ContractError):c.validate('backup_status', status | dict(destination='/private/host/path'))


def test_local_copy_request_has_one_explicit_selector_and_no_generic_host_read():
    common = dict(action='export.plan', runtime_generation=ID, destination='/selected/output', format='zip')
    for selector in ['version_id','document_id','project_id']:
        c.validate('local_copy_request', common | {selector:ID})
    for invalid in [dict(document_id=ID,version_id=ID), dict(input='/etc/passwd'), dict(version_id=ID,encryption='age'), dict(version_id=ID,versions='all')]:
        with pytest.raises(c.ContractError):c.validate('local_copy_request', common | invalid)
    c.validate('copy_acceptance', dict(operation_id=ID,set_id=ID,job_id=ID,state='queued'))
    c.validate('export_index',dict(format_version=1,library_id=ID,versions=[dict(document_id=ID,version_id=ID,payload_sha256='a'*64,manifest_sha256='b'*64,payload_root='versions/'+ID,inventory_path='metadata/versions/'+ID+'/inventory.jsonl',metadata_root=None)]))

    with pytest.raises(c.ContractError):c.validate('export_index',dict(format_version=1,library_id=ID,versions=[]))

    member = dict(document_id=ID,version_id=ID,payload_sha256='a'*64,manifest_sha256='b'*64)
    c.validate('copy_plan_page',dict(set_id=ID,plan_id=ID,state='planned',digest='c'*64,items=[member],next_cursor=None))
    with pytest.raises(c.ContractError):c.validate('copy_selection',member | dict(payload_root='guessed-before-execution'))
    c.validate('copy_abandon_result',dict(operation_id=ID,set_id=ID,kind='export',state='abandoned'))
