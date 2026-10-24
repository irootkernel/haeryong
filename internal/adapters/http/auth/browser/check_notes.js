async page => {
 const input=INPUT;
 let stage="open",notes,note;
 const errors=[];page.on('pageerror',e=>errors.push(e.message));
 const api=async(method,path,body,extra={})=>page.evaluate(async args=>{const headers={Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1'),...args.extra};if(args.body!==undefined)headers['Content-Type']='application/json';const r=await fetch('/api/v1'+args.path,{method:args.method,headers,body:args.body===undefined?undefined:JSON.stringify(args.body)});const text=await r.text();return {status:r.status,body:text?JSON.parse(text):null};},{method,path,body,extra});
 const openNotes=async()=>{await page.locator('#reader-context-toggle').click();await page.locator('#reader-notes-tab').click();await page.locator('#note-new').waitFor();};
 try{
 if(input.phase==="reattach-conflict"){
 stage="C2 concurrent reattach conflict";
 const closeContext=page.getByRole('button',{name:'context 닫기',exact:true});if(await closeContext.count())await closeContext.click();
 const created=await api('POST','/versions/'+input.version+'/annotations',{page_path:'first.html',region_id:null,page_query:'',page_fragment:'',body:'C2 stale edit note',body_ref:null,selected_text:'C2 old selection',context:'C2 old context',context_ref:null},{'Idempotency-Key':await page.evaluate(()=>crypto.randomUUID())});if(created.status!==201)throw Error('C2 note fixture failed');
 const original=created.body.data;
 await page.getByRole('button',{name:'목록 새로고침',exact:true}).click();await page.locator('.note-entry').filter({hasText:'C2 stale edit note'}).getByRole('button',{name:'편집',exact:true}).click();await page.locator('#note-body').fill('C2 local text preserved');
 const destination={version_id:input.version,page_path:'nested/둘째.html',region_id:null,page_query:'',page_fragment:''};
 const moved=await api('PATCH','/annotations/'+original.annotation_id,{action:'reattach',anchor:destination,selected_text:'C2 new selection',context:'C2 new context',context_ref:null},{'Idempotency-Key':await page.evaluate(()=>crypto.randomUUID()),'If-Match':original.etag});if(moved.status!==200)throw Error('C2 concurrent reattach failed');
 await page.locator('#note-save').click();await page.getByLabel('변경된 노트 위치와 이전 문맥').waitFor();if(await page.locator('#note-body').inputValue()!=='C2 local text preserved')throw Error('C2 conflict lost local text');
 await page.getByRole('button',{name:'이전 초안 문맥 읽기',exact:true}).click();await page.getByText('C2 old context',{exact:true}).waitFor();await page.getByRole('button',{name:'context 닫기',exact:true}).click();
 await page.getByRole('button',{name:'서버 위치로 병합 준비 · 이전 문맥 제외',exact:true}).click();
 await page.waitForFunction(()=>{const key=Object.keys(localStorage).find(k=>k.startsWith('haeryong.notes.v1:'));const r=JSON.parse(localStorage.getItem(key));return r.draft?.anchor.page_path==='nested/둘째.html'&&r.draft.contextRef===null&&r.draft.context===null&&r.pending===null;});
 if(await page.locator('#note-body').inputValue()!=='C2 local text preserved')throw Error('C2 reconciliation replaced local text');await page.locator('#note-save').focus();await page.keyboard.press('Enter');await page.waitForFunction(()=>!document.querySelector('#note-body'));
 const saved=await api('GET','/annotations/'+original.annotation_id);if(saved.status!==200||saved.body.data.body!=='C2 local text preserved'||saved.body.data.anchor.page_path!==destination.page_path||saved.body.data.context_ref!==null||saved.body.data.selected_text!==null)throw Error('C2 merged Save kept stale binding or text changed');
 if(errors.length)throw Error('trusted page error '+errors.join(';'));
 return {concurrentReattachMerge:true,staleContextDropped:true,localTextPreserved:true};
 }
 if(input.phase==="prepare"){
 await page.setViewportSize({width:1280,height:900});await page.goto(input.route);await page.frameLocator('iframe').locator('#review-fixture').waitFor();await openNotes();
 const frame=page.frames().find(f=>f!==page.mainFrame());
 stage="context selection";
 await frame.evaluate(async()=>{await window.__haeryong.select('retry',{selected_text:'exact fallback',scenario:'saved context 🌊'});});
 await page.getByText('선택한 영역: retry',{exact:true}).waitFor();
 await page.getByLabel('새 노트·재연결 범위').selectOption('region');
 await page.locator('#note-new').focus();await page.keyboard.press('Enter');
 const hostile='<script>window.noteExecuted=true</script>\n<img src=x onerror="window.noteExecuted=true">\n<svg onload="window.noteExecuted=true"></svg>\n[bad](javascript:window.noteExecuted=true)\n\n**safe Markdown**';
 await page.locator('#note-body').fill(hostile);await page.locator('#note-save').focus();await page.keyboard.press('Enter');
 await page.waitForFunction(()=>!document.querySelector('#note-body'));
 notes=await api('GET','/versions/'+input.version+'/annotations');note=notes.body.data.items[0];
 if(!note||note.anchor.page_path!=='first.html'||note.anchor.region_id!=='retry'||!note.context_ref)throw Error('exact page/region/context Save lost');
 if(await page.evaluate(()=>window.noteExecuted===true||!!document.querySelector('.note-markdown script,.note-markdown img,.note-markdown svg,.note-markdown iframe,.note-markdown a[href^="javascript:"]')))throw Error('hostile markup executed or became active');
 const review=await api('GET','/versions/'+input.version+'/review');if(review.body.data.exists)throw Error('notes completed Review');
 stage="saved context";
 const current=page.locator('.note-entry').first();await current.getByRole('button',{name:'저장된 context 읽기',exact:true}).click();await page.getByText('saved context 🌊',{exact:false}).waitFor();await page.getByRole('button',{name:'context 닫기',exact:true}).click();
 stage="conflict";
 await current.getByRole('button',{name:'편집',exact:true}).click();await page.locator('#note-body').fill('local unsaved comparison');
 const remoteKey=await page.evaluate(()=>crypto.randomUUID()),serverText='newer server revision '+ '🌊'.repeat(20000);
 const upload=await api('POST','/uploads',{purpose:'body',input_mode:'object',target:{operation_id:remoteKey,action:'annotation_edit',annotation_id:note.annotation_id}},{'Idempotency-Key':await page.evaluate(()=>crypto.randomUUID())});if(upload.status!==201)throw Error('comparison body upload create failed');
 const uploadID=upload.body.data.upload_id;
 const ack=await page.evaluate(async({id,text})=>{const bytes=new TextEncoder().encode(text),hash=Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes))).map(n=>n.toString(16).padStart(2,'0')).join('');const r=await fetch('/api/v1/uploads/'+id+'/entries?path=_input&kind=file',{method:'PUT',headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1'),'Content-Type':'application/octet-stream','Haeryong-Upload-Offset':'0','Haeryong-Upload-Length':String(bytes.length),'Haeryong-Upload-Chunk-SHA256':hash,'Haeryong-Upload-Final':'true'},body:bytes});if(r.status!==200)throw Error('comparison body chunk failed');return (await r.json()).data;},{id:uploadID,text:serverText});
 const sealed=await api('POST','/uploads/'+uploadID+'/complete',{checkpoint:{...ack.checkpoint,generation:ack.checkpoint.generation+1,complete_input:true}},{'Idempotency-Key':await page.evaluate(()=>crypto.randomUUID())});if(sealed.status!==202)throw Error('comparison body sealing failed');
 // waitForFunction treats an async predicate's Promise as truthy in the
 // installed CLI. Await each read and bound the sealing readiness poll.
 await page.evaluate(async id=>{const deadline=performance.now()+30000;for(;;){const r=await fetch('/api/v1/uploads/'+id,{headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1')}});const u=(await r.json()).data;if(u.state==='failed')throw Error('comparison body sealing failed');if(u.state==='sealed'&&u.sealed_object_ref)return;if(performance.now()>=deadline)throw Error('comparison body sealing timed out');await new Promise(resolve=>setTimeout(resolve,100));}},uploadID);
 const remote=await api('PATCH','/annotations/'+note.annotation_id,{action:'edit',body:null,body_ref:{upload_id:uploadID},selected_text:null,context:null,context_ref:note.context_ref},{'Idempotency-Key':remoteKey,'If-Match':note.etag});if(remote.status!==200||!remote.body.data.body_ref){const current=await api('GET','/annotations/'+note.annotation_id),detail=await api('GET','/uploads/'+uploadID),context=await api('GET','/contexts/'+note.context_ref);throw Error('concurrent owner body_ref edit failed: status='+remote.status+' code='+(remote.body?.error?.code??'none')+' note='+current.status+' upload='+detail.status+' upload_state='+(detail.body?.data?.state??'none')+' context='+context.status+' body_upload='+uploadID); }
 await page.locator('#note-save').click();await page.getByLabel('노트 충돌 비교').waitFor();
 await page.getByLabel('노트 충돌 비교').locator('.note-markdown').waitFor();
 if(await page.getByLabel('노트 충돌 비교').locator('.note-markdown').innerText()!==serverText)throw Error('412 body_ref server comparison bytes missing');
 if(await page.locator('#note-body').inputValue()!=='local unsaved comparison')throw Error('412 lost local draft');
 const recovery=await page.evaluate(()=>{const key=Object.keys(localStorage).find(k=>k.startsWith('haeryong.notes.v1:'));return JSON.parse(localStorage.getItem(key));});if(recovery.draft.text!=='local unsaved comparison'||!recovery.pending.rejected||recovery.pending.etag!==note.etag)throw Error('comparison changed local draft or pending intent');
 await page.getByRole('button',{name:'현재 개정과 합친 초안으로 저장 준비',exact:true}).click();await page.locator('#note-body').fill('deliberately merged');await page.locator('#note-save').click();await page.waitForFunction(()=>!document.querySelector('#note-body'));
 return {exactAnchor:true,contextAdopted:true,hostileMarkupInert:true,keyboardSave:true,reviewIndependent:true,conflictDraftPreserved:true,referencedBodyComparison:true,deliberateMerge:true};
 }
 stage="reattach completion";
 await page.locator('.note-entry').first().getByText('개정 4',{exact:false}).waitFor();
 notes=await api('GET','/versions/'+input.version+'/annotations');note=notes.body.data.items[0];if(note.anchor.region_id!==null||note.original_anchor.region_id!=='retry')throw Error('explicit page fallback lost history');
 await page.locator('.note-entry').first().getByRole('button',{name:'이력',exact:true}).click();await page.getByLabel('노트 이력').waitFor();if(!await page.getByLabel('노트 이력').getByText('앵커 미해결',{exact:false}).count())throw Error('original unresolved state missing');
 stage="touch revisit";
 await page.setViewportSize({width:390,height:844});await page.keyboard.press('Escape');
 const revisit=page.locator('#reader-revisit');await revisit.scrollIntoViewIfNeeded();const box=await revisit.boundingBox();if(!box||box.width<44||box.height<44)throw Error('Revisit target too small');const cdp=await page.context().newCDPSession(page);await cdp.send('Emulation.setTouchEmulationEnabled',{enabled:true});await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x:box.x+box.width/2,y:box.y+box.height/2}]});await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});await cdp.detach();await page.waitForFunction(()=>document.querySelector('#reader-revisit')?.getAttribute('aria-pressed')==='true');
 await page.goto(input.origin+'/');const home=page.locator('.home-section').filter({has:page.getByRole('heading',{name:'다시 볼 문서',exact:true})});await home.locator('a[href*="'+input.version+'"]').waitFor();if((await home.innerText()).includes(input.version)||(input.project&&(await home.innerText()).includes(input.project)))throw Error('Home raw UUID display');
 stage="reopen after renderer disposal";
 await page.goto(input.route);await page.locator('#review-start').waitFor();await openNotes();await page.locator('.note-entry').first().getByRole('button',{name:'저장된 context 읽기',exact:true}).click();await page.getByText('saved context 🌊',{exact:false}).waitFor();
 if(errors.length)throw Error('trusted page error '+errors.join(';'));
 return {version:input.version,note:note.annotation_id,exactAnchor:true,contextAdopted:true,contextAfterRendererDisposal:true,conflictDraftPreserved:true,referencedBodyComparison:true,deliberateMerge:true,explicitReattachHistory:true,hostileMarkupInert:true,keyboardSave:true,touchRevisit:true,homeRevisit:true,reviewIndependent:true};
 }catch(e){throw Error('Notes '+stage+': '+e.message+'; alerts: '+await page.locator('[role="alert"]').allTextContents());}
}
