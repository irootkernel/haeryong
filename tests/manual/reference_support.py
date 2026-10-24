"""Public-import F03/F13 fixtures shared by the explicit browser/native drivers.

The authored observer is synthetic test instrumentation. It reports actual loader
traffic and DOM state; it supplies no host capability, route or focus result.
"""
import hashlib
import html
import json
import os
import sqlite3
import subprocess
from pathlib import Path
import time
import urllib.parse
import uuid

REPO = Path(__file__).resolve().parents[2]
SPECIAL = '설명/공백 %2F +.html'
REGION = '영역/%2F +'
QUERY = 'q=%2F+ 공백/% 한글'
FRAGMENT = '문서/%2F + 공백'
OBSERVER = r'''<script>
(()=>{
 const emit=value=>parent.postMessage({reference_fixture:true,value},'*');
 const seen=new WeakSet();let channel,ready;
 const watch=port=>{if(seen.has(port))return;seen.add(port);port.addEventListener('message',e=>{
   if(e.data?.type==='reference.navigation')ready=e.data;
   if(e.data?.type)emit({direction:'host',message:e.data});
 });};
 const post=MessagePort.prototype.postMessage;
 MessagePort.prototype.postMessage=function(value,...rest){watch(this);channel=this;
   if(value?.type)emit({direction:'child',message:value});return post.call(this,value,...rest);};
 addEventListener('message',e=>{
   if(e.source!==parent||!e.data?.reference_fixture_command)return;
   const c=e.data,el=c.id?document.getElementById(c.id):null;
   if(c.action==='click')el?.click();
   if(c.action==='modifier')el?.dispatchEvent(new MouseEvent('click',{bubbles:true,cancelable:true,button:0,metaKey:true}));
   if(c.action==='new-window'&&el){el.target='_blank';el.click();}
   if(c.action==='focus')el?.focus();
   if(c.action==='scroll')scrollTo(0,c.y);
   if(c.action==='fabricate'&&channel){channel.postMessage({...c.envelope,type:'reference.requested',sequence:c.sequence,payload:{reference_id:c.id}});}
   const inspection={};
   if(c.action==='inspect'){
     const origin=document.getElementById('origin'),rect=origin?.getBoundingClientRect();
     let visible=!!rect&&rect.width>0&&rect.height>0;
     for(let n=origin;n;n=n.parentElement){const style=getComputedStyle(n);if(style.display==='none'||style.visibility!=='visible'||Number(style.opacity)===0)visible=false;}
     Object.assign(inspection,{ready_state:document.readyState,origin_present:!!origin,origin_visible:visible,
       ready:ready?{document_id:ready.document_id,version_id:ready.version_id,page_path:ready.page_path,render_instance_id:ready.render_instance_id}:null});
   }
   emit({command:c.action,scroll_y:scrollY,active:document.activeElement?.id,href:location.href,...inspection});
 });
 addEventListener('focusin',()=>emit({active:document.activeElement?.id,scroll_y:scrollY}));
 addEventListener('keydown',e=>emit({key:e.key,scroll_y:scrollY}));
})();
</script>'''


def page(title, body):
    return '<!doctype html><html><head><meta charset="utf-8"><title>'+title+'</title></head><body>'+body+OBSERVER+'</body></html>'


def route(identity, path, region=None, query='', fragment=''):
    fields={'page':path}
    if query:fields['page_query']=query
    if fragment:fields['page_fragment']=fragment
    value = '/documents/'+identity['document_id']+'/versions/'+identity['version_id']+'?'+urllib.parse.urlencode(fields)
    return value + ('#'+urllib.parse.urlencode({'region':region}) if region else '')


def ready(product, owner, version):
    deadline=time.monotonic()+120
    while time.monotonic()<deadline:
        jobs=[j for j in product.data('GET','/api/v1/jobs',client=owner)['items'] if j['kind']=='derived_index' and j.get('result',{}).get('version_id')==version]
        if len(jobs)==2 and all(j['state']=='completed' for j in jobs):return
        assert not any(j['state'] in ('failed','cancelled') for j in jobs),'fixture derived work failed'
        time.sleep(.1)
    raise AssertionError('fixture readiness hang guard')


def publish(product, owner, name, files, metadata=None, extra=()):
    root=product.root/name;root.mkdir()
    for relative,body in files.items():
        p=root/relative;p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(body.encode() if isinstance(body,str) else body)
    flags=list(extra)
    if metadata is not None:
        p=product.root/(name+'-metadata.json');p.write_text(json.dumps(metadata,ensure_ascii=False));flags+=['--metadata',str(p)]
    _,_,identity=product.import_tree(owner,root,extra=flags,timeout=120)
    ready(product,owner,identity['version_id'])
    view=product.data('GET','/api/v1/versions/'+identity['version_id'],client=owner)
    identity.update({key:view[key] for key in ('payload_sha256','manifest_sha256','supplied_metadata_sha256')})
    hashes={name:hashlib.sha256(body.encode() if isinstance(body,str) else body).hexdigest() for name,body in files.items()}
    assert sorted(row['path'] for row in product.files(owner,identity['version_id']) if row['kind']=='file')==sorted(files),'imported file set differs from the explicit list'
    for path,digest in hashes.items():
        status,body,_=product.request('GET','/api/v1/versions/'+identity['version_id']+'/file?'+urllib.parse.urlencode({'path':path}),client=owner,raw=True)
        assert status==200 and hashlib.sha256(body).hexdigest()==digest,'fixture payload byte mismatch'
    return identity,hashes


def prepare(product, owner, corpus=False):
    if corpus:return prepare_corpus(product,owner)
    projects=[product.cli(['projects','register','--name','F03 '+label],client=owner)['data']['result']['project_id'] for label in ('target A','source B')]
    # Explicit immutable file list; no generated manifest or required index.
    files={
      'architecture.html':page('F03 pinned architecture','<h1>F03 target v1</h1><p id="retry" tabindex="-1">architecture retry</p><a id="nested" data-haeryong-reference="nested" href="b.html">nested reference</a><a id="ordinary" href="b.html">ordinary target move</a>'),
      'a.html':page('F13 a','<h1>F13 a</h1><p id="retry">a retry</p><p data-haeryong-region="data-only">data only</p><p id="both" data-haeryong-region="both">both</p><i id="duplicate">one</i><i id="duplicate">two</i><script>const runtime=document.createElement("p");runtime.id="runtime-only";runtime.textContent="runtime anchor";document.body.append(runtime);</script>'),
      'b.html':page('F13 b','<h1>F13 b</h1><p id="retry">b retry</p>'),
      SPECIAL:page('F13 encoded target','<h1>F13 encoded v1</h1><p data-haeryong-region="'+html.escape(REGION,quote=True)+'">encoded region</p>'),
      'no-region.html':page('F13 no region','<h1>No region page</h1>'),
    }
    child,_=publish(product,owner,'reference-nested',{'b.html':files['b.html']},extra=['--project',projects[0],'--new-document'])
    nested={'id':'nested','document_id':child['document_id'],'version_id':child['version_id'],'page_path':'b.html','region_id':'retry','from':{'page_path':'architecture.html','region_id':None}}
    regions=[{'id':r,'page_path':p} for p,r in [('architecture.html','retry'),('a.html','retry'),('b.html','retry'),(SPECIAL,REGION),('a.html','data-only'),('a.html','both'),('a.html','duplicate'),('a.html','missing'),('a.html','runtime-only')]]
    target,target_hashes=publish(product,owner,'reference-target',files,{'references':[nested],'regions':regions},['--project',projects[0],'--new-document'])
    doc,ver=target['document_id'],target['version_id']
    refs=[]
    for identity,path,region in [('exact','architecture.html','retry'),('encoded',SPECIAL,REGION),('gone','architecture.html','retry'),('denied','architecture.html','retry'),('runtime-only','a.html','runtime-only')]:
        refs.append({'id':identity,'document_id':doc,'version_id':ver,'page_path':path,'region_id':region,'from':{'page_path':'source.html','region_id':'origin'},'page_query':QUERY if identity=='encoded' else '', 'page_fragment':FRAGMENT if identity=='encoded' else ''})
    refs += [{'id':'unavailable','document_id':str(uuid.uuid4()),'version_id':str(uuid.uuid4()),'page_path':'missing.html','region_id':None,'from':{'page_path':'source.html','region_id':'origin'}},
             {'id':'legacy','document_id':doc,'version_id':ver,'page_path':None,'region_id':None},
             {'id':'page-only','document_id':doc,'version_id':ver,'page_path':'a.html','region_id':None}]
    links=''.join('<p><a id="'+r['id']+'" data-haeryong-reference="'+r['id']+'" href="fallback.html">'+r['id']+'</a></p>' for r in refs)
    source,source_hashes=publish(product,owner,'reference-source',{'source.html':page('F03 source B','<h1>F03 source B</h1><section id="origin">'+links+'</section><div style="height:2500px"></div>'),'fallback.html':page('Ordinary fallback','<h1>Ordinary fallback</h1>')},{'references':refs,'regions':[{'id':'origin','page_path':'source.html'}]},['--project',projects[1],'--new-document'])
    latest,_=publish(product,owner,'reference-successor',{'latest.html':page('Successor','<h1>New target without prior pages</h1>')},extra=['--document',doc])
    source_route=route(source,'source.html','origin',QUERY,FRAGMENT)
    expected=product.data('GET','/api/v1/versions/'+source['version_id']+'/references/resolve?'+urllib.parse.urlencode({'reference_id':'exact','page_path':'source.html','region_id':'origin'}),client=owner)
    assert expected['resolution']=='resolved' and expected['target']['version_id']==ver
    return {'source':source,'target':target,'nested':child,'latest':latest,'projects':projects,'source_route':source_route,'target_route':expected['reader_url'],'encoded_route':route(target,SPECIAL,REGION,QUERY,FRAGMENT),'source_page':'source.html','target_page':'architecture.html','reference':'exact','region':'retry','tree_hashes':{'source':source_hashes,'target':target_hashes},'synthetic':True}


CORPUS_FILES = {
    'mulgae': ('assets/style.css','assets/recovery.js','recovery/compose.html','recovery/inventory.html'),
    'gaori': ('assets/style.css','assets/gaori/illustrations.js','gaori/outcomes.html','gaori/lifecycle.html'),
}


def prepare_corpus(product, owner, knowledge=False):
    """RF12 complete checked-in four-file trees, explicit lists and original bytes.

    Mulgae is imported first. Historical prose labels remain authored bytes;
    service UUIDs belong only to the external Gaori metadata and private receipt.
    """
    imported={};hashes={};projects={};source_inputs={}
    for name,paths in CORPUS_FILES.items():
        assert len(paths)==len(set(paths))==4
        files={p:(REPO/'spikes/knowledge-value/samples'/name/p).read_bytes() for p in paths}
        project=product.cli(['projects','register','--name','RF12 '+name],client=owner)['data']['result']['project_id']
        projects[name]=project
        metadata={'regions':[{'id':'M11','page_path':'recovery/compose.html'}]}
        if name=='gaori':metadata={'regions':[{'id':'G12','page_path':'gaori/lifecycle.html'}],'references':[{'id':'prior-compose','document_id':imported['mulgae']['document_id'],'version_id':imported['mulgae']['version_id'],'page_path':'recovery/compose.html','region_id':'M11','from':{'page_path':'gaori/lifecycle.html','region_id':'G12'},'relation':'explanatory analogy'}]}
        if knowledge:
            source_inputs[name]=knowledge_metadata(product,owner,name,project,metadata)
        imported[name],hashes[name]=publish(product,owner,'corpus-'+name,files,metadata,['--project',project,'--new-document'])
        assert sorted(hashes[name])==sorted(paths)
    s,t=imported['gaori'],imported['mulgae']
    latest,_=publish(product,owner,'corpus-synthetic-successor',{'successor.html':b'<!doctype html><title>Synthetic successor</title><h1>Labelled synthetic successor without the old compose page</h1>'},extra=['--document',t['document_id']])
    assert latest['document_id']==t['document_id'] and latest['version_id']!=t['version_id']
    return {'source':s,'target':t,'latest':latest,'successor_label':'synthetic successor; old compose page removed',
      'knowledge_sources':source_inputs,'knowledge_gate':knowledge,
      'projects':projects,'source_route':route(s,'gaori/lifecycle.html','G12'),'target_route':route(t,'recovery/compose.html','M11'),
      'source_page':'gaori/lifecycle.html','target_page':'recovery/compose.html','reference':'prior-compose','region':'M11',
      'source_region':'G12','tree_hashes':hashes,'explicit_files':CORPUS_FILES,'synthetic':False,
      'expected_source':{'project_id':projects['gaori'],'document_id':s['document_id'],'version_id':s['version_id'],'page_path':'gaori/lifecycle.html','region_id':'G12'},
      'expected_target':{'project_id':projects['mulgae'],'document_id':t['document_id'],'version_id':t['version_id'],'page_path':'recovery/compose.html','region_id':'M11'}}


def prepare_versions(product, owner):
    """T5 bounded synthetic immutable inventories, shared by existing drivers."""
    files={f'files/{i:03}.txt':f'old {i}\n' for i in range(105)}
    files[SPECIAL]=page('Versions old', '<h1>Versions old</h1><p id="'+html.escape(REGION,quote=True)+'">old region</p>')
    old,_=publish(product,owner,'versions-old',files,{'document':{'title':'Versions old metadata'},'regions':[{'page_path':SPECIAL,'id':REGION,'label':'old region label'}]},['--new-document'])
    files={path:body for path,body in files.items() if path!=SPECIAL}
    files['new.html']=page('Versions new','<h1>Versions new</h1><p id="same">new page region</p>')
    files['files/000.txt']='changed text\n'
    new,_=publish(product,owner,'versions-new',files,{'document':{'title':'Versions new metadata'},'regions':[{'page_path':'new.html','id':'same','label':'new region label'}]},['--document',old['document_id']])
    no_html,_=publish(product,owner,'versions-no-html',{'readme.txt':'No HTML; actual version scope\n'},extra=['--new-document'])
    return {'no_html':no_html,'old':old,'new':new,'encoded_route':route(old,SPECIAL,REGION,QUERY,FRAGMENT),'special':SPECIAL,'region':REGION,'query':QUERY,'fragment':FRAGMENT,'synthetic':True}

# T6 opt-in composition only. Default RF12 metadata/worksheets remain unchanged.
KNOWLEDGE_SOURCES = {
 'mulgae': ('cef337cf927b820316f42a41f1441fbc653203f8','README.md',757,782,'M-S09','M11'),
 'gaori': ('393af5999bbbb30b5d11e2a17a2325c8cf1833b7','docs/integration-guide.md',298,298,'G-S18','G12'),
}


def knowledge_metadata(product,owner,name,project,metadata):
    revision,path,start,end,evidence,region=KNOWLEDGE_SOURCES[name]
    root=REPO.parent/name
    blob=subprocess.check_output(['git','-C',str(root),'show',revision+':'+path])
    # Canonical rule: split only on LF; preserve CR and the final LF exactly.
    selected=b'\n'.join(blob.split(b'\n')[start-1:end])
    if end<len(blob.split(b'\n')):selected+=b'\n'
    repo_id=str(uuid.uuid4());source_id=str(uuid.uuid4());denied_repo=str(uuid.uuid4());denied_source=str(uuid.uuid4())
    source={'id':source_id,'kind':'git','retention_mode':'reference_only','repo_id':repo_id,'object_format':'sha1','exact_revision':revision,'release_label':'T6 fixed released '+name}
    metadata['sources']=[source,dict(source,id=denied_source,repo_id=denied_repo)]
    declaration={'id':evidence,'source_id':source_id,'capture_status':'reference_only','path':path,'line_start':start,'line_end':end,'bytes_sha256':hashlib.sha256(selected).hexdigest()}
    metadata['evidence']=[declaration,dict(declaration,id='unapproved-root',source_id=denied_source)]
    metadata['regions'][0]['evidence_ids']=[evidence]
    item={'root':str(root),'revision':revision,'path':path,'line_start':start,'line_end':end,'evidence_id':evidence,'bytes_sha256':declaration['bytes_sha256'],'source_id':source_id,'repo_id':repo_id,'project_id':project}
    approve_knowledge_source(product,owner,item,root)
    return item


def approve_knowledge_source(product,owner,item,root):
    return product.cli(['sources','approve','--project',item['project_id'],'--source-id',item['source_id'],'--kind','git','--root-path',str(root),'--repo-id',item['repo_id'],'--retention-mode','reference_only','--confirmed'],client=owner)


def knowledge_checks(product,owner,fixture,result):
    """Actual public source/graph composition; raw corruption only while stopped."""
    result.update(label='new T6 observation; real sample, synthetic fault/change targets',checks=[])
    versions={'mulgae':fixture['target'],'gaori':fixture['source']}
    def get(version,suffix):return product.data('GET','/api/v1/versions/'+version+suffix,client=owner)
    def verify(version,evidence,body):
        key=str(uuid.uuid4());route='/api/v1/versions/'+version+'/source-evidence-verifications?'+urllib.parse.urlencode({'evidence_id':evidence})
        response=product.data('POST',route,client=owner,body=body,headers={'Idempotency-Key':key})
        assert response['operation_id']==key
        assert product.data('POST',route,client=owner,body=body,headers={'Idempotency-Key':key})==response
        return response
    def authored(version):
        status,body,_=product.request('GET','/api/v1/versions/'+version+'/metadata?kind=supplied',client=owner,raw=True)
        assert status==200
        return hashlib.sha256(body).hexdigest()
    original={name:authored(v['version_id']) for name,v in versions.items()}
    checks=[]
    for name,item in fixture['knowledge_sources'].items():
        version=versions[name]['version_id'];evidence=item['evidence_id']
        for body,outcome in [({},'captured'),({'expected_bytes_sha256':item['bytes_sha256']},'matched'),({'expected_bytes_sha256':'0'*64},'mismatch')]:
            row=verify(version,evidence,body);assert row['outcome']==outcome,(name,outcome,row)
            assert row['target_set_id'] and row['checked_at']
            captured=get(version,'/assessments?'+urllib.parse.urlencode({'target_set_id':row['target_set_id']}))['applicable']['captured_at']
            assert captured
            checks.append((version,evidence,body,row,captured));result['checks'].append(dict(row,captured_at=captured))
        denied=verify(version,'unapproved-root',{});assert denied['outcome']=='unavailable' and denied['reason'];result['checks'].append(denied);checks.append((version,'unapproved-root',{},denied,None))
        missing_root=product.root/('missing-'+name)
        subprocess.run(['git','init','--quiet',str(missing_root)],check=True,capture_output=True)
        approve_knowledge_source(product,owner,item,missing_root)
        hidden_root=product.root/('hidden-'+name);missing_root.rename(hidden_root)
        try:
            missing=verify(version,evidence,{});assert missing['outcome']=='unavailable' and missing['reason'];result['checks'].append(missing);checks.append((version,evidence,{},missing,None))
        finally:hidden_root.rename(missing_root)
        approve_knowledge_source(product,owner,item,item['root'])
    product.stop();product.start()
    for version,evidence,body,row,stamp in checks:
        history=get(version,'/source-evidence-verifications?'+urllib.parse.urlencode({'evidence_id':evidence}))
        assert any(r['verification_id']==row['verification_id'] and r['outcome']==row['outcome'] and r['checked_at']==row['checked_at'] for r in history['items'])
        replay=product.data('POST','/api/v1/versions/'+version+'/source-evidence-verifications?'+urllib.parse.urlencode({'evidence_id':evidence}),client=owner,body=body,headers={'Idempotency-Key':row['operation_id']})
        assert replay==row
        if stamp is not None:
            captured=get(version,'/assessments?'+urllib.parse.urlencode({'target_set_id':row['target_set_id']}))
            assert captured['applicable']['captured_at']==stamp and captured['applicable']['target_set_id']==row['target_set_id']
    result['verification_restart_persistence']=True
    source,target=fixture['source']['version_id'],fixture['target']['version_id']
    before_refs=get(source,'/references');before_back=get(target,'/backlinks')
    assert before_refs['counts']=={'edge_count':'1','referenced_document_count':'1'}
    assert before_back['counts']=={'edge_count':'1','referring_document_count':'1'}
    assert before_refs['items'][0]['edge_id']==before_back['items'][0]['edge_id']
    validations=[]
    for version in (source,target):
        for kind in ('references','search'):
            started=time.monotonic();v=get(version,'/indexes/validate?kind='+kind)
            assert v['state']=='consistent',v
            validations.append({'version_id':version,'kind':kind,'elapsed_ms':(time.monotonic()-started)*1000,'observed':v})
    # Same intact-DDL wrong-key recipe as BL C06. No running product connection.
    product.stop();dbpath=product.data_dir/'storage/library.sqlite'
    def connect():return sqlite3.connect(dbpath)
    with connect() as db:
        definition=db.execute("SELECT sql FROM sqlite_master WHERE name='authored_edges_target'").fetchone()[0]
        rows=db.execute('SELECT * FROM authored_edges NOT INDEXED ORDER BY rowid').fetchall()
        wrong=definition.replace('target_version_id,',"(CASE WHEN metadata_reference_id='prior-compose' THEN '"+fixture['latest']['version_id']+"' ELSE target_version_id END),",1)
        assert wrong!=definition
        db.execute('PRAGMA writable_schema=ON');db.execute("UPDATE sqlite_master SET sql=? WHERE name='authored_edges_target'",(wrong,))
    with connect() as db:db.execute('REINDEX main.authored_edges_target')
    with connect() as db:
        db.execute('PRAGMA writable_schema=ON');db.execute("UPDATE sqlite_master SET sql=? WHERE name='authored_edges_target'",(definition,))
    with connect() as db:
        canonical=db.execute('SELECT count(*) FROM authored_edges NOT INDEXED WHERE target_version_id=?',(target,)).fetchone()[0]
        indexed=db.execute('SELECT count(*) FROM authored_edges INDEXED BY authored_edges_target WHERE target_version_id=?',(target,)).fetchone()[0]
        assert canonical==1 and indexed==0
    product.start()
    damaged=get(target,'/indexes/validate?kind=references');assert damaged['state']=='inconsistent',damaged
    key=str(uuid.uuid4());route='/api/v1/versions/'+target+'/indexes/rebuild'
    repair=product.data('POST',route,client=owner,body={'kind':'references'},headers={'Idempotency-Key':key})
    deadline=time.monotonic()+120
    while True:
        jobs=[product.data('GET','/api/v1/jobs/'+j,client=owner) for j in repair['job_ids']]
        assert all(j['state'] not in ('failed','cancelled') for j in jobs),jobs
        if all(j['state']=='completed' for j in jobs):break
        assert time.monotonic()<deadline,'T6 owner rebuild hang guard'
        time.sleep(.1)
    assert product.data('POST',route,client=owner,body={'kind':'references'},headers={'Idempotency-Key':key})==repair
    assert get(source,'/references')==before_refs and get(target,'/backlinks')==before_back
    started=time.monotonic();repaired=get(target,'/indexes/validate?kind=references');assert repaired['state']=='consistent'
    result['C06']={'canonical_count':canonical,'damaged_index_count':indexed,'detected':damaged,'repair':repair,'repaired':repaired,'validation_elapsed_ms':(time.monotonic()-started)*1000,'initial_validations':validations}
    product.stop()
    with connect() as db:assert db.execute('SELECT * FROM authored_edges NOT INDEXED ORDER BY rowid').fetchall()==rows
    product.start();assert get(target,'/backlinks')==before_back
    # T3 permits a local copy only for this direct-change connection.
    item=fixture['knowledge_sources']['mulgae'];copy=product.root/'direct-change-mulgae'
    # Object-only copy/setup: no checkout, filters, hooks, signing or fetch.
    # Only the disposable copy's objects, index and HEAD may be written.
    git_env={k:v for k,v in os.environ.items() if not k.startswith('GIT_')}
    git_env.update(GIT_AUTHOR_NAME='T6 synthetic',GIT_AUTHOR_EMAIL='fixture@example.invalid',GIT_COMMITTER_NAME='T6 synthetic',GIT_COMMITTER_EMAIL='fixture@example.invalid',GIT_NO_REPLACE_OBJECTS='1',GIT_NO_LAZY_FETCH='1',GIT_TERMINAL_PROMPT='0')
    subprocess.run(['git','-c','core.hooksPath=/dev/null','clone','--bare','--local','--no-hardlinks','--quiet',item['root'],str(copy)],env=git_env,check=True,capture_output=True)
    def git(*args,input=None):
        return subprocess.check_output(['git','-c','core.hooksPath=/dev/null','-c','commit.gpgsign=false','-C',str(copy),*args],env=git_env,input=input)
    original_blob=git('show',item['revision']+':'+item['path'])
    assert b'do not blindly retry an uncertain mutation' in original_blob
    changed=original_blob.replace(b'do not blindly retry an uncertain mutation',b'SYNTHETIC changed publication instruction',1)
    blob=git('hash-object','-w','--stdin',input=changed).decode().strip()
    mode=git('ls-tree',item['revision'],'--',item['path']).decode().split()[0]
    git('read-tree',item['revision'])
    git('update-index','--cacheinfo',mode+','+blob+','+item['path'])
    tree=git('write-tree').decode().strip()
    commit=git('commit-tree',tree,'-p',item['revision'],input=b'Synthetic direct cited-source change\n').decode().strip()
    git('update-ref','--no-deref','HEAD',commit)
    approve_knowledge_source(product,owner,item,copy)
    observation=product.data('POST','/api/v1/versions/'+target+'/source-observations',client=owner,body={'scope':{'kind':'region','page_path':'recovery/compose.html','region_id':'M11'}} ,headers={'Idempotency-Key':str(uuid.uuid4())})
    assessment=get(target,'/assessments?'+urllib.parse.urlencode({'page_path':'recovery/compose.html','region_id':'M11','scope_kind':'region','target_set_id':observation['target_set_id']}))
    assert assessment['applicable']['reassessment_required'] is True,assessment
    result['direct_change']={'label':'synthetic commit in disposable local copy','observation':observation,'assessment':assessment}
    approve_knowledge_source(product,owner,item,item['root'])
    product.stop();product.start()
    for name,v in versions.items():assert authored(v['version_id'])==original[name]
    assert get(source,'/references')==before_refs and get(target,'/backlinks')==before_back
    persisted=get(target,'/assessments?'+urllib.parse.urlencode({'scope_kind':'region','page_path':'recovery/compose.html','region_id':'M11','target_set_id':observation['target_set_id']}))
    assert persisted['applicable']==assessment['applicable']
    result['direct_change']['unchanged_after_restart']=True
    result['authored_provenance_unchanged']=True
    return result
