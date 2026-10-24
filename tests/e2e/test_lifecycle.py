"""Real lifecycle actions repeat EPIC-004 exclusion and preserve owner records.
Synthetic inputs; no provider/production maintenance/purge qualification.
"""
import json
import uuid
from urllib.parse import urlencode
import pytest
from .test_annotations import create, content
from .test_references import reference_ready
pytestmark=pytest.mark.e2e

def key(): return str(uuid.uuid4())
def plan(product,owner,document,operation):
    if operation=='restore':
        trash=product.data('GET','/api/v1/trash',client=owner)['items']
        row=next((r for r in trash if r['document_id']==document),None)
    else: row=None
    revision=row['revision'] if row else product.data('GET','/api/v1/documents/'+document,client=owner)['revision']
    return product.data('POST',f'/api/v1/documents/{document}/lifecycle-plans',client=owner,body=dict(operation=operation,expected_revision=revision))
def commit(product,owner,p,alias=None):
    return product.data('POST',f"/api/v1/lifecycle-plans/{p['plan_id']}/commit",client=owner,headers={'Idempotency-Key':alias} if alias else {},body=dict(plan_id=p['plan_id'],expected_revision=p['expected_revision'],confirmed=True))
def transition(product,owner,doc,action): return commit(product,owner,plan(product,owner,doc,action))
def test_lifecycle_real_archive_trash_restore_preserves_exact_records_links_assets_and_restart(product,owner,tmp_path):
    target=tmp_path/'target';target.mkdir();raw=b'<h1 id="retry">LifecycleCanary</h1>';(target/'target.html').write_bytes(raw);shared=b'shared lifecycle object';(target/'shared.bin').write_bytes(shared)
    _,_,t=product.import_tree(owner,target);reference_ready(product,owner,t['version_id'])
    source=tmp_path/'source';source.mkdir();(source/'source.html').write_text('<h1 id="origin">Referring document</h1>');(source/'shared.bin').write_bytes(shared)
    metadata=tmp_path/'metadata.json';metadata.write_text(json.dumps({'references':[dict(id=f'ref-{n}',document_id=t['document_id'],version_id=t['version_id'],page_path='target.html',region_id='retry',**{'from':dict(page_path='source.html',region_id='origin')}) for n in range(3)]}))
    _,_,s=product.import_tree(owner,source,extra=('--metadata',str(metadata),'--new-document'));reference_ready(product,owner,s['version_id'])
    note=create(product,owner,t['version_id'],'target.html',context='Saved exact lifecycle context 🌊')
    review_key=key();review=product.data('PUT',f"/api/v1/versions/{t['version_id']}/review",client=owner,headers={'Idempotency-Key':review_key,'If-None-Match':'*'},body=dict(action='complete',state='reviewed'))
    product.data('PUT',f"/api/v1/documents/{t['document_id']}/revisit",client=owner,headers={'Idempotency-Key':key(),'If-None-Match':'*'},body=dict(flag=True))
    # A new immutable version shares bytes but never inherits the old Review/note.
    _,_,new=product.import_tree(owner,target,extra=('--document',t['document_id'],'--base-version',t['version_id']))
    reference_ready(product,owner,new['version_id'])
    assert product.data('GET',f"/api/v1/versions/{new['version_id']}/review",client=owner)['exists'] is False
    assert product.data('GET',f"/api/v1/versions/{new['version_id']}/annotations",client=owner)['items']==[]
    refs=f"/api/v1/versions/{s['version_id']}/references";before=product.data('GET',refs,client=owner);assert before['counts']['edge_count']=='3'
    backlinks=f"/api/v1/versions/{t['version_id']}/backlinks";back_before=product.data('GET',backlinks,client=owner)
    transition(product,owner,t['document_id'],'archive')
    assert product.data('GET',f"/api/v1/versions/{t['version_id']}/review",client=owner)['value']==review['value']
    archived_search=product.data('GET','/api/v1/search?'+urlencode(dict(q='LifecycleCanary',lifecycle='archived',versions='all',limit=1)),client=owner);assert archived_search['items']
    trash=transition(product,owner,t['document_id'],'trash');assert trash['previous_lifecycle']=='archived' and trash['trashed_at']
    for route in [f"/api/v1/versions/{t['version_id']}",f"/api/v1/versions/{t['version_id']}/review",f"/api/v1/versions/{t['version_id']}/review/history",f"/api/v1/documents/{t['document_id']}/revisit",f"/api/v1/annotations/{note['annotation_id']}",f"/api/v1/annotations/{note['annotation_id']}/history",f"/api/v1/annotations/{note['annotation_id']}/context?revision=1",f"/api/v1/operations/{review_key}",backlinks]:
        assert product.request('GET',route,client=owner)[0]==404,route
    for method in ['GET','HEAD']:
        assert product.request(method,f"/api/v1/versions/{t['version_id']}/file?path=target.html",client=owner,raw=True,headers={'Range':'bytes=0-4'})[0]==404
    hidden=product.data('GET',refs+'?limit=1',client=owner);assert hidden['items'][0]['resolution']=='unavailable' and hidden['items'][0]['target'] is None;assert hidden['counts']['edge_count']=='0'
    search=product.data('GET','/api/v1/search?q=LifecycleCanary&lifecycle=all',client=owner)
    assert search['items']==[] and search['total_matches']=='0' and search['next_cursor'] is None and 'LifecycleCanary' not in json.dumps(search['items'])
    cursor=hidden['next_cursor'];rows=hidden['items']
    while cursor:
        batch=product.data('GET',refs+'?'+urlencode(dict(limit=1,cursor=cursor)),client=owner);rows+=batch['items'];cursor=batch['next_cursor']
    assert len(rows)==3 and all(r['resolution']=='unavailable' and r['target'] is None for r in rows)
    if archived_search['next_cursor']:
        status,value,_=product.request('GET','/api/v1/search?'+urlencode(dict(q='LifecycleCanary',lifecycle='archived',versions='all',limit=1,cursor=archived_search['next_cursor'])),client=owner)
        assert status==400 or status==200 and value['data']['items']==[]
    assert product.request('GET',f"/api/v1/versions/{s['version_id']}/file?path=shared.bin",client=owner,raw=True)[1]==shared
    assert (target/'target.html').read_bytes()==raw and (target/'shared.bin').read_bytes()==shared
    # Owner writes preserve the established explicit lifecycle error contract.
    status,error,_=product.request('PUT',f"/api/v1/documents/{t['document_id']}/revisit",client=owner,headers={'Idempotency-Key':key(),'If-None-Match':'*'},body=dict(flag=False));assert status==409 and error['error']['details']['lifecycle']=='trash'
    body=dict(page_path='target.html',region_id=None,page_query='',page_fragment='',**content('must not save'))
    status,error,_=product.request('POST',f"/api/v1/versions/{t['version_id']}/annotations",client=owner,headers={'Idempotency-Key':key()},body=body);assert status==409 and error['error']['code']=='INVALID_TRANSITION'
    second=product.owner('restore session');result=transition(product,second,t['document_id'],'restore');assert result['lifecycle']=='archived' and result['document_id']==t['document_id']
    product.stop();product.start()
    assert product.data('GET',refs,client=second)==before
    assert product.data('GET',backlinks,client=second)==back_before
    assert product.data('GET',f"/api/v1/annotations/{note['annotation_id']}",client=second)['anchor']==note['anchor']
    assert product.data('GET',f"/api/v1/versions/{t['version_id']}/review",client=second)['value']==review['value']
    status,data,_=product.request('GET',f"/api/v1/annotations/{note['annotation_id']}/context?revision=1",client=second,raw=True);assert status==200 and data=='Saved exact lifecycle context 🌊'.encode()
    assert product.data('GET',f"/api/v1/operations/{review_key}",client=second)['state']=='completed'
    assert product.data('GET',f"/api/v1/versions/{new['version_id']}/review",client=second)['exists'] is False
    assert {row['version_id'] for row in product.data('GET',f"/api/v1/documents/{t['document_id']}/versions",client=second)['items']}=={t['version_id'],new['version_id']}
    assert product.request('GET',f"/api/v1/versions/{t['version_id']}/file?path=target.html",client=second,raw=True)[1]==raw
    transition(product,second,t['document_id'],'restore');transition(product,second,t['document_id'],'trash');assert transition(product,second,t['document_id'],'restore')['lifecycle']=='active'

def test_lifecycle_confirmation_matrix_keyless_alias_and_plan_impact_filter(product,owner,tmp_path):
    tree=tmp_path/'input';tree.mkdir();(tree/'page.html').write_text('<h1>Lifecycle</h1>')
    _,_,t=product.import_tree(owner,tree);reference_ready(product,owner,t['version_id'])
    # CLI plan/show and keyless HTTP plan refer to one stable plan identity.
    doc=product.data('GET','/api/v1/documents/'+t['document_id'],client=owner)
    p=product.cli(['lifecycle','plan','--document',t['document_id'],'--action','trash','--expected-revision',str(doc['revision'])],client=owner)['data']
    assert product.cli(['lifecycle','show',p['plan_id']],client=owner)['data']['plan_id']==p['plan_id']
    route=f"/api/v1/lifecycle-plans/{p['plan_id']}/commit";valid=dict(plan_id=p['plan_id'],expected_revision=p['expected_revision'],confirmed=True)
    for committed in [False,True]:
        if committed: saved=product.data('POST',route,client=owner,body=valid)
        for value in [False,None,'true','missing']:
            body=valid.copy()
            if value=='missing':del body['confirmed']
            else:body['confirmed']=value
            for headers in [{},{'Idempotency-Key':key()}]:
                status,error,_=product.request('POST',route,client=owner,headers=headers,body=body)
                assert status==400 and error['error']['code']=='INVALID_REQUEST'
    alias=key();assert product.data('POST',route,client=owner,headers={'Idempotency-Key':alias},body=valid)==saved
    assert product.data('GET','/api/v1/operations/'+alias,client=owner)['result_metadata']==saved
    for headers in [{},{'Idempotency-Key':alias},{'Idempotency-Key':key()}]:
        status,error,_=product.request('POST',route,client=owner,headers=headers,body=valid|dict(expected_revision=p['expected_revision']+1));assert status==409 and error['error']['code']=='OPERATION_CONFLICT'
    assert product.cli(['trash','list'],client=owner)['data']['items'][0]['document_id']==t['document_id']
    restore=plan(product,owner,t['document_id'],'restore')
    product.cli(['lifecycle','commit',restore['plan_id'],'--expected-revision',str(restore['expected_revision']),'--confirm'],client=owner)
    assert product.data('GET','/api/v1/documents/'+t['document_id'],client=owner)['lifecycle']=='active'

def test_lifecycle_saved_impact_pages_preview_replay_and_receipt_recheck_incoming_source(product,owner,tmp_path):
    target=tmp_path/'impact-target';target.mkdir();(target/'target.html').write_text('<p>Target</p>')
    _,_,t=product.import_tree(owner,target);reference_ready(product,owner,t['version_id'])
    source=tmp_path/'impact-source';source.mkdir();(source/'source.html').write_text('<p>Source</p>')
    metadata=tmp_path/'impact.json';metadata.write_text(json.dumps({'references':[dict(id='incoming-private',document_id=t['document_id'],version_id=t['version_id'],page_path='target.html',region_id=None)]}))
    _,_,s=product.import_tree(owner,source,extra=('--metadata',str(metadata),'--new-document'));reference_ready(product,owner,s['version_id'])
    revision=product.data('GET','/api/v1/documents/'+t['document_id'],client=owner)['revision'];body=dict(operation='trash',expected_revision=revision);operation=key();headers={'Idempotency-Key':operation};route=f"/api/v1/documents/{t['document_id']}/lifecycle-plans"
    preview=product.data('POST',route,client=owner,headers=headers,body=body);assert preview['impact']['incoming']==1
    first=product.data('GET',f"/api/v1/lifecycle-plans/{preview['plan_id']}?limit=1",client=owner)
    transition(product,owner,s['document_id'],'trash')
    current=product.data('POST',route,client=owner,headers=headers,body=body)
    receipt=product.data('GET','/api/v1/operations/'+operation,client=owner)['result_metadata']
    for value in [current,receipt]:
        assert value['impact']['incoming']==0 and value['impact']['incoming_edges']==0 and value['impact']['referring_documents']==0
        assert all(item['kind']!='incoming' for item in value['items'])
        assert s['version_id'] not in json.dumps(value)
    cursor=first['next_cursor']
    while cursor:
        page=product.data('GET',f"/api/v1/lifecycle-plans/{preview['plan_id']}?"+urlencode(dict(cursor=cursor,limit=1)),client=owner)
        assert page['impact']['incoming']==0 and all(item['kind']!='incoming' for item in page['items'])
        cursor=page['next_cursor']
    status,error,_=product.request('POST',f"/api/v1/lifecycle-plans/{preview['plan_id']}/commit",client=owner,body=dict(plan_id=preview['plan_id'],expected_revision=revision,confirmed=True))
    assert status==409 and error['error']['code']=='STALE_PLAN'

def test_lifecycle_preserves_referenced_note_body_and_prior_context_history(product,owner,tmp_path):
    import hashlib
    tree=tmp_path/'note-target';tree.mkdir();(tree/'page.html').write_text('<h1>Exact note</h1>')
    _,_,target=product.import_tree(owner,tree);reference_ready(product,owner,target['version_id'])
    final=key();text=('Immutable note body 🌊\n'*5000).encode()
    upload=product.data('POST','/api/v1/uploads',client=owner,headers={'Idempotency-Key':key()},body=dict(purpose='body',input_mode='object',target=dict(operation_id=final,action='annotation_create',version_id=target['version_id'],page_path='page.html')))['upload_id']
    ack=product.data('PUT',f'/api/v1/uploads/{upload}/entries?path=_input&kind=file',client=owner,body=text,headers={'Content-Type':'application/octet-stream','Haeryong-Upload-Offset':'0','Haeryong-Upload-Length':str(len(text)),'Haeryong-Upload-Chunk-SHA256':hashlib.sha256(text).hexdigest(),'Haeryong-Upload-Final':'true'})
    cp=ack['checkpoint'];sealed=product.data('POST',f'/api/v1/uploads/{upload}/complete',client=owner,headers={'Idempotency-Key':key()},body=dict(checkpoint=cp|dict(generation=cp['generation']+1,complete_input=True)));product.job(owner,sealed['job_id'])
    body=dict(page_path='page.html',region_id=None,page_query='',page_fragment='',**content(context='original saved context'))|dict(body=None,body_ref=dict(upload_id=upload))
    note=product.data('POST',f"/api/v1/versions/{target['version_id']}/annotations",client=owner,headers={'Idempotency-Key':final},body=body)
    route='/api/v1/annotations/'+note['annotation_id']
    updated=product.data('PATCH',route,client=owner,headers={'Idempotency-Key':key(),'If-Match':note['etag']},body=dict(action='edit',**content('current body',context='current context')))
    transition(product,owner,target['document_id'],'archive');transition(product,owner,target['document_id'],'trash')
    assert product.request('GET',route+'/body?revision=1',client=owner,raw=True)[0]==404
    transition(product,owner,target['document_id'],'restore');product.stop();product.start()
    assert product.request('GET',route+'/body?revision=1',client=owner,raw=True)[1]==text
    assert product.request('GET',route+'/context?revision=1',client=owner,raw=True)[1]==b'original saved context'
    assert product.request('GET',route+'/context?revision=2',client=owner,raw=True)[1]==b'current context'
    history=product.data('GET',route+'/history',client=owner)['items'];assert [r['revision'] for r in history]==[1,2] and history[0]['body_ref']==note['body_ref']
    assert product.data('GET',route,client=owner)['etag']==updated['etag']
