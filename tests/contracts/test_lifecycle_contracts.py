"""Additive lifecycle shapes and labelled confirmation fixtures, not runtime proof."""
import json
from pathlib import Path
import pytest
import contract as c
pytestmark = pytest.mark.integration
ID='11111111-1111-4111-8111-111111111111'
def test_lifecycle_closed_shapes_and_confirmation_fixture():
    for action in ('archive','trash','restore','purge'):
        c.validate('lifecycle_plan_request',dict(operation=action,expected_revision=2))
    valid=dict(plan_id=ID,expected_revision=2,confirmed=True)
    c.validate('lifecycle_commit_request',valid)
    rows=json.loads((Path(__file__).resolve().parents[1]/'fixtures/lifecycle/confirmation.json').read_text())['cases']
    for row in rows:
        body=valid.copy()
        if 'confirmed' in row: body['confirmed']=row['confirmed']
        else: del body['confirmed']
        if row['status']==400:
            with pytest.raises(c.ContractError): c.validate('lifecycle_commit_request',body)
        else: c.validate('lifecycle_commit_request',body)
    for body in (valid|dict(expected_revision=0),valid|dict(extra=True)):
        with pytest.raises(c.ContractError):c.validate('lifecycle_commit_request',body)
    c.validate('trash_page',dict(items=[],next_cursor=None))
    c.validate('lifecycle_impact_item',dict(kind='annotation',id=ID,version_id=ID,page_path='한글.html',region_id='retry',revision=2,source_document_id=None,resolution=None))


def test_purge_closed_202_and_honest_backup():
    result=dict(operation_id=ID,plan_id=ID,document_id=ID,lifecycle='purged',previous_lifecycle=None,trashed_at=None,revision=3,changed=True,job_id=ID,job_url='/api/v1/jobs/'+ID)
    c.validate('lifecycle_result',result)
    backup=dict(state='unavailable',last_complete_at=None,reason='full_backup_engine_not_implemented')
    c.validate('purge_backup',backup)
    with pytest.raises(c.ContractError):c.validate('purge_backup',backup|dict(last_complete_at='invented'))
    with pytest.raises(c.ContractError):c.validate('lifecycle_result',result|dict(title='must not retain'))

    fixture=json.loads((Path(__file__).resolve().parents[1]/'fixtures/lifecycle/purge.json').read_text())
    for shape,key in [('lifecycle_result','result'),('purge_backup','backup'),('purge_job_progress','progress')]:c.validate(shape,fixture[key])
