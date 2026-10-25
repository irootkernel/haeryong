"""Guarded manual purge through real public CLI/HTTP and daemon recovery.
Synthetic owner libraries; source inputs never become deletion targets.
"""
import json
import time
import uuid
from concurrent.futures import ThreadPoolExecutor
from urllib.parse import urlencode
import pytest
from .test_lifecycle import transition, commit
from .test_references import reference_ready
from .test_annotations import create
pytestmark=pytest.mark.e2e

def key():return str(uuid.uuid4())
def purge_plan(product,owner,document):
    row=next(r for r in product.data('GET','/api/v1/trash',client=owner)['items'] if r['document_id']==document)
    return product.data('POST',f'/api/v1/documents/{document}/lifecycle-plans',client=owner,body=dict(operation='purge',expected_revision=row['revision']))
def wait_metadata(product,owner,job):
    end=time.monotonic()+60
    while time.monotonic()<end:
        row=product.data('GET','/api/v1/jobs/'+job,client=owner)
        assert row['state']!='failed',row
        if row.get('result',{}).get('metadata_removed'):return row
        time.sleep(.05)
    raise AssertionError('purge metadata stage did not finish')

def test_purge_minimal_history_shared_bytes_source_identity_and_restart(product,owner,tmp_path):
    target=tmp_path/'target';target.mkdir();private=b'<h1 id="private">SensitivePurgeCanary</h1>';shared=b'shared-retained-asset'
    (target/'target.html').write_bytes(private);(target/'shared.bin').write_bytes(shared)
    _,accepted,t=product.import_tree(owner,target);reference_ready(product,owner,t['version_id'])
    original_upload=product.data('GET','/api/v1/jobs/'+accepted['result']['job_id'],client=owner)['upload_id']
    note=create(product,owner,t['version_id'],'target.html',context='purge saved context')
    product.data('PUT',f"/api/v1/versions/{t['version_id']}/review",client=owner,headers={'Idempotency-Key':key(),'If-None-Match':'*'},body=dict(action='complete',state='reviewed'))
    source=tmp_path/'source';source.mkdir();(source/'source.html').write_text('<h1>Surviving referrer</h1>');(source/'shared.bin').write_bytes(shared)
    metadata=tmp_path/'ref.json';metadata.write_text(json.dumps({'references':[dict(id='old-target',document_id=t['document_id'],version_id=t['version_id'],page_path='target.html',region_id=None)]}))
    _,_,s=product.import_tree(owner,source,extra=('--new-document','--metadata',str(metadata)));reference_ready(product,owner,s['version_id'])
    snapshot={str(p.relative_to(target)):p.read_bytes() for p in target.rglob('*') if p.is_file()}
    transition(product,owner,t['document_id'],'trash');p=purge_plan(product,owner,t['document_id'])
    assert p['backup']=={'state': 'not_created', 'set_id': None, 'last_complete_at': None, 'reason': None, 'last_attempt': None, 'last_trial_restore': {'state': 'not_run', 'completed_at': None}}
    assert p['impact']['version']==1 and p['impact']['annotation']==1 and p['impact']['review_event']==1
    assert p['impact']['referring_documents']==1 and p['impact']['incoming_edges']==1
    alias=key();r=commit(product,owner,p,alias);assert r['lifecycle']=='purged' and r['job_id']
    assert commit(product,owner,p,alias)==r
    row=wait_metadata(product,owner,r['job_id']);assert row['state'] in ('running','paused','completed')
    assert product.request('POST',f"/api/v1/jobs/{r['job_id']}/cancel",client=owner,headers={'Idempotency-Key':key()},body={})[0]==409
    refs=product.data('GET',f"/api/v1/versions/{s['version_id']}/references",client=owner)
    gone=refs['items'][0];assert gone['resolution']=='tombstoned' and 'SensitivePurgeCanary' not in json.dumps(gone)
    status,error,_=product.request('GET',f"/api/v1/versions/{s['version_id']}/references/resolve?"+urlencode(dict(reference_id='old-target')),client=owner)
    assert status==410 and error['error']['code']=='TARGET_GONE'
    assert product.request('GET',f"/api/v1/versions/{s['version_id']}/file?path=shared.bin",client=owner,raw=True)[1]==shared
    assert product.request('GET',f"/api/v1/annotations/{note['annotation_id']}/history",client=owner)[0]==404
    for route,body in [(f"/api/v1/versions/{t['version_id']}/review",dict(action='complete',state='reviewed'))]:
        status,error,_=product.request('PUT',route,client=owner,headers={'Idempotency-Key':key(),'If-None-Match':'*'},body=body)
        assert status==409 and error['error']['details']['lifecycle']=='purged'
    status,error,_=product.request('POST',f"/api/v1/versions/{t['version_id']}/annotations",client=owner,headers={'Idempotency-Key':key()},body=dict(page_path=None,region_id=None,page_query='',page_fragment='',body='cannot recreate',body_ref=None,selected_text=None,context=None,context_ref=None))
    assert status==409 and error['error']['details']['lifecycle']=='purged'

    assert product.data('GET','/api/v1/search?q=SensitivePurgeCanary&lifecycle=all',client=owner)['items']==[]
    assert snapshot=={str(p.relative_to(target)):p.read_bytes() for p in target.rglob('*') if p.is_file()}
    for restart in (False,True):
        if restart:product.stop();product.start()
        # The original still-sealed upload has the same exact payload and can
        # normally be adopted repeatedly; a purged version ID is never reusable.
        status,error,_=product.request('POST','/api/v1/imports',client=owner,headers={'Idempotency-Key':key()},body=dict(upload_id=original_upload,document_id=None,version_id=t['version_id']))
        assert status==409 and error['error']['code']=='IMMUTABLE_VERSION_CONFLICT'
        result=product.cli(['import',str(target),'--new-document','--version',t['version_id']],client=owner,code=4)
        assert result['error']['code']=='CONFLICT' and 'IMMUTABLE_VERSION_CONFLICT' in result['error']['message']
        assert commit(product,owner,p,alias)==r


def test_purge_restore_race_has_one_terminal_decision(product,owner,tmp_path):
    tree=tmp_path/'race';tree.mkdir();(tree/'page.html').write_text('<p>race</p>');_,_,t=product.import_tree(owner,tree)
    transition(product,owner,t['document_id'],'trash');p=purge_plan(product,owner,t['document_id']);restore=None
    restore=product.data('POST',f"/api/v1/documents/{t['document_id']}/lifecycle-plans",client=owner,body=dict(operation='restore',expected_revision=p['expected_revision']))
    peer=product.owner('purge race peer')
    def send(plan,client):return product.request('POST',f"/api/v1/lifecycle-plans/{plan['plan_id']}/commit",client=client,body=dict(plan_id=plan['plan_id'],expected_revision=plan['expected_revision'],confirmed=True))
    with ThreadPoolExecutor(max_workers=2) as pool:
        a=pool.submit(send,p,owner);b=pool.submit(send,restore,peer);results=[a.result(),b.result()]
    assert sorted(status for status,_,_ in results) in ([200,412],[202,412])


def test_purge_daemon_live_io_retry_then_upload_release_terminal_restart(product,owner,tmp_path):
    """Real local filesystem failure; only this disposable profile is changed."""
    import os
    tree=tmp_path/'io-target';tree.mkdir();(tree/'page.html').write_text('<h1>Purge IO recovery</h1>')
    _,accepted,t=product.import_tree(owner,tree);reference_ready(product,owner,t['version_id'])
    imported=product.data('GET','/api/v1/jobs/'+accepted['result']['job_id'],client=owner)
    upload=imported['upload_id']
    transition(product,owner,t['document_id'],'trash');p=purge_plan(product,owner,t['document_id'])
    objects=product.data_dir/'storage'/'objects'
    os.chmod(objects,0o500)
    try:
        r=commit(product,owner,p)
        end=time.monotonic()+60
        while time.monotonic()<end:
            row=product.data('GET','/api/v1/jobs/'+r['job_id'],client=owner)
            if row['state']=='paused' and row.get('result',{}).get('reason')=='io_retry':break
            time.sleep(.05)
        else:raise AssertionError('live daemon never exposed recoverable collection IO failure')
        remaining=row['result']['remaining']
        assert product.process.poll() is None
        # Restore startup-required directory permissions. The still-sealed
        # upload keeps candidates protected and the job nonterminal at restart.
        checkpoint=dict(row['result'])
        os.chmod(objects,0o700)
        product.stop();product.start()
        resumed=product.data('GET','/api/v1/jobs/'+r['job_id'],client=owner)
        assert resumed['state'] in ('paused','running','interrupted')
        assert resumed['result']['stage']==checkpoint['stage']
        assert resumed['result']['metadata_removed']==checkpoint['metadata_removed']
        assert commit(product,owner,p)==r
        jobs=product.data('GET','/api/v1/jobs',client=owner)['items']
        decisions=[j for j in jobs if j['kind']=='lifecycle' and j['scope'].get('purge_plan_id')==p['plan_id']]
        assert len(decisions)==1 and decisions[0]['job_id']==r['job_id']
    finally:os.chmod(objects,0o700)
    # Release the independent upload using its public explicit-abandon action.
    product.data('POST',f'/api/v1/uploads/{upload}/abandon',client=owner,headers={'Idempotency-Key':key()},body={})
    end=time.monotonic()+60
    while time.monotonic()<end:
        final=product.data('GET','/api/v1/jobs/'+r['job_id'],client=owner)
        if final['state']=='completed':break
        time.sleep(.05)
    else:raise AssertionError('pin release and restored storage did not finish original purge')
    assert remaining>0 and final['result']['remaining']==0
    assert commit(product,owner,p)==r
    product.stop();product.start()
    assert product.data('GET','/api/v1/jobs/'+r['job_id'],client=owner)==final
    assert commit(product,owner,p)==r


def test_purge_abandon_before_cleanup_with_supplied_metadata(product,owner,tmp_path):
    tree=tmp_path/'abandoned-before-purge';tree.mkdir();(tree/'only.html').write_text('<h1>Unique private payload before purge</h1>')
    metadata=tmp_path/'supplied.json';metadata.write_text(json.dumps({'document':{'title':'Unique private supplied title'}}))
    _,_,target=product.import_tree(owner,tree,extra=('--metadata',str(metadata)))
    for upload in product.data('GET','/api/v1/uploads',client=owner)['items']:
        product.data('POST',f"/api/v1/uploads/{upload['upload_id']}/abandon",client=owner,headers={'Idempotency-Key':key()},body={})
    transition(product,owner,target['document_id'],'trash');plan=purge_plan(product,owner,target['document_id']);result=commit(product,owner,plan)
    end=time.monotonic()+60
    while time.monotonic()<end:
        job=product.data('GET','/api/v1/jobs/'+result['job_id'],client=owner)
        if job['state']=='completed':break
        time.sleep(.05)
    else:raise AssertionError('abandoned upload or terminal metadata index still retained payload')
    assert job['result']['remaining']==0 and job['result']['metadata_removed'] is True
    assert commit(product,owner,plan)==result
