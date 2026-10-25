async page=>{
 const input=INPUT;let step="initial Reader";
 try {
 const api=async(method,path,body)=>page.evaluate(async({method,path,body})=>{
  const response=await fetch(path,{method,headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1'),...(body?{'Content-Type':'application/json'}:{}),...(path.endsWith('/annotations')?{'Idempotency-Key':crypto.randomUUID()}:{})},body:body?JSON.stringify(body):undefined});const wire=await response.json();if(!response.ok)throw Error(wire.error.code);return wire.data;
 },{method,path,body});
 const sessions=[];page.on('response',async response=>{if(response.request().method()==='POST'&&response.url().endsWith('/render-sessions')&&response.ok()){const wire=await response.json();sessions.push(wire.data.session_id);}});
 const documentPath='/api/v1/documents/'+input.document;
 const openMenu=async(target=page)=>{const menu=target.locator('.lifecycle-control details').first();if(await menu.getAttribute('open')===null)await menu.locator('summary').click();};
 const dialog=()=>page.getByRole('dialog',{name:'수명주기 영향 확인'});
 const assertImpact=async(target,name)=>{
  const box=target.getByRole('dialog',{name:'수명주기 영향 확인'}),text=await box.locator('[data-lifecycle-effects]').innerText();
  const expected=name==='보관'?'보관 필터에서 계속 읽을 수 있습니다':name==='휴지통으로 이동'?'일반 화면과 검색에서 숨겨지고 파일과 링크로도 열 수 없습니다':'원래 위치에서 같은 버전과 기록으로 복원됩니다';
  if(!text.includes(expected)||!text.includes('원본과 개인 기록'))throw Error('operation effects missing');
  const incoming=await box.locator('ul li summary').filter({hasText:'들어오는 참조'}).allTextContents();
  const available=name==='휴지통으로 이동'?'복원 전까지 사용할 수 없음':name==='복원'?'이전 접근 상태로 복원':'참조 대상의 접근 상태 유지';
  if(incoming.length<2||!incoming.some(value=>value.includes('미해결 참조'))||incoming.some(value=>!value.includes(available)||value.includes('보존')))throw Error('incoming reference effect was not shown');
 };
 const assertTrashDisplay=async target=>{
  await target.locator('.lifecycle-control summary').first().waitFor();
  const item=(await api('GET','/api/v1/trash')).items.find(row=>row.document_id===input.document);
  if(!item?.purge_eligible_at)throw Error('retention fixture has no eligible timestamp');
  const expected=await target.evaluate(item=>({removed:new Date(item.trashed_at).toLocaleString(),eligible:new Date(item.purge_eligible_at).toLocaleString(),state:item.previous_lifecycle==='archived'?'보관':'활성'}),item);
  const row=target.getByRole('listitem').filter({has:target.getByRole('heading',{name:item.title,exact:true})}).first();
  await row.getByText('삭제: '+expected.removed+' · 복원 위치: '+expected.state,{exact:true}).waitFor();
  await row.getByText('영구 삭제 가능 시점: '+expected.eligible+'. 표시한 시점과 무관하게 수동 영구 삭제는 영향 확인 후 선택할 수 있습니다.',{exact:true}).waitFor();
 };
 const choose=async(name)=>{await openMenu();await page.getByRole('button',{name,exact:true}).click();await page.locator('[data-lifecycle-confirm]').waitFor();await assertImpact(page,name);};
 const confirm=async()=>{const button=page.locator('[data-lifecycle-confirm]');await button.focus();await page.keyboard.press('Enter');await dialog().waitFor({state:'hidden'});};
 step='Reader visit 1';
 await page.goto(input.route);await page.frameLocator('iframe').locator('#review-fixture').waitFor();
 const boundary=await page.frames().find(f=>f!==page.mainFrame()).evaluate(()=>{let denied=false;try{parent.localStorage;}catch{denied=true;}parent.postMessage({type:'delete',document_id:'forged'},'*');return {hostStorageDenied:denied,nativeAbsent:!(window.wails||window.runtime||window.haeryongNative)};});
 if(!boundary.hostStorageDenied||!boundary.nativeAbsent)throw Error('host separation');
 step='viewport cancellation';
 for(const [width,height]of [[1440,900],[820,1180],[844,390],[390,844]]){
  await page.setViewportSize({width,height});await choose('보관');const overflow=await dialog().evaluate(e=>e.scrollWidth>e.clientWidth+1);if(overflow)throw Error('impact horizontal overflow');
  const safe=await page.evaluate(()=>document.activeElement?.id);if(safe?.startsWith('lifecycle-confirm-'))throw Error('destructive default focus');
  await page.keyboard.press('Escape');await dialog().waitFor({state:'hidden'});
 }
 step='safe default Enter';
 await choose('휴지통으로 이동');await page.keyboard.press('Enter');await dialog().waitFor({state:'hidden'});if((await api('GET',documentPath)).lifecycle!=='active')throw Error('Enter default mutated target');
 step='changed note impact';
 await choose('보관');await api('POST','/api/v1/versions/'+input.version+'/annotations',{page_path:'first.html',region_id:null,page_query:'',page_fragment:'',body:'Owner changed note during preview',body_ref:null,selected_text:null,context:null,context_ref:null});
 await page.locator('[data-lifecycle-confirm]').click();await page.getByRole('button',{name:'변경된 상태에서 영향 다시 확인',exact:true}).waitFor();await page.getByRole('button',{name:'변경된 상태에서 영향 다시 확인',exact:true}).click();await page.locator('[data-lifecycle-confirm]').waitFor();await page.keyboard.press('Escape');await dialog().waitFor({state:'hidden'});
 step="delayed position save";
 let releaseSave,noticeSave,finishedSave;
 const heldSave=new Promise(resolve=>{releaseSave=resolve;}),saveObserved=new Promise(resolve=>{noticeSave=resolve;}),saveFinished=new Promise(resolve=>{finishedSave=resolve;});
 await page.route('**/api/v1/versions/'+input.version+'/page-position?*',async route=>{noticeSave();await heldSave;const response=await route.fetch();await route.fulfill({response});finishedSave();});
 await page.frames().find(f=>f!==page.mainFrame()).evaluate(()=>scrollTo(0,500));await saveObserved;
 await choose('보관');releaseSave();await saveFinished;await confirm();await page.unroute('**/api/v1/versions/'+input.version+'/page-position?*');
 if((await api('GET',documentPath)).lifecycle!=='archived')throw Error('automatic position save made plan stale');
 step='trash current Reader';
 await choose('휴지통으로 이동');await confirm();await page.waitForFunction(()=>!document.querySelector('iframe'));
 step='Trash Restore UI';
 await page.goto(input.origin+'/trash');await page.getByRole('heading',{name:'휴지통',exact:true}).waitFor();await assertTrashDisplay(page);await choose('복원');await confirm();await page.waitForURL(u=>u.pathname==='/library');
 if((await api('GET',documentPath)).lifecycle!=='archived')throw Error('prior archived state not restored');
 step='old session renewal';
 const oldStatus=await page.evaluate(async({version,session})=>(await fetch('/api/v1/versions/'+version+'/render-sessions/'+session+'/renew',{method:'POST',headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1')}})).status,{version:input.version,session:sessions[0]});if(oldStatus<400)throw Error('old delivery revived after restore');
 step='Reader visit 2';
 await page.goto(input.route);await page.frameLocator('iframe').locator('#review-fixture').waitFor();
 step='peer Reader';
 const sibling=await page.context().newPage();await sibling.goto(input.route);await sibling.frameLocator('iframe').locator('#review-fixture').waitFor();await sibling.close();
 const peerContext=await page.context().browser().newContext();const peer=await peerContext.newPage();await peer.goto(input.origin+'/health/live');
 await peer.evaluate(token=>localStorage.setItem('haeryong.owner-session.v1',token),input.peer_token);
 await peer.evaluate(async({document})=>{const headers={Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1'),'Content-Type':'application/json'};const call=async(method,path,body)=>{const r=await fetch(path,{method,headers,body:body?JSON.stringify(body):undefined});const wire=await r.json();if(!r.ok)throw Error(wire.error.code);return wire.data;};const path='/api/v1/documents/'+document;const d=await call('GET',path);const p=await call('POST',path+'/lifecycle-plans',{operation:'trash',expected_revision:d.revision});await call('POST','/api/v1/lifecycle-plans/'+p.plan_id+'/commit',{plan_id:p.plan_id,expected_revision:p.expected_revision,confirmed:true});},{document:input.document});
 await page.waitForFunction(()=>!document.querySelector('iframe'));await page.getByRole('heading',{name:'이 문서를 더 이상 표시할 수 없습니다.'}).waitFor();await peerContext.close();
 step='peer closure and explicit restore';
 const trash=await api('GET','/api/v1/trash');const d=trash.items.find(i=>i.document_id===input.document);const p=await api('POST',documentPath+'/lifecycle-plans',{operation:'restore',expected_revision:d.revision});await api('POST','/api/v1/lifecycle-plans/'+p.plan_id+'/commit',{plan_id:p.plan_id,expected_revision:p.expected_revision,confirmed:true});
 if(await page.locator('iframe').count()!==0)throw Error('restore remounted an old renderer');
 step='Reader visit 3';
 await page.goto(input.route);await page.frameLocator('iframe').locator('#review-fixture').waitFor();
 const sizes=await page.locator('.lifecycle-control summary').first().boundingBox();if(sizes.height<44)throw Error('minimum target too small');
 const gate=()=>{let finish,released=false;const promise=new Promise(done=>{finish=done;});return {resolve:()=>{released=true;finish();},wait:()=>released?Promise.resolve():Promise.race([promise,page.waitForTimeout(30000).then(()=>{throw Error("lifecycle browser barrier timed out");})])};};
 const peerCycle=async peer=>peer.evaluate(async({document})=>{
  const headers={Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1'),'Content-Type':'application/json'};
  const call=async(method,path,body)=>{const r=await fetch(path,{method,headers,body:body?JSON.stringify(body):undefined});const w=await r.json();if(!r.ok)throw Error(w.error.code);return w.data;};
  for(const operation of ['trash','restore']){
   const d=operation==='trash'?await call('GET','/api/v1/documents/'+document):(await call('GET','/api/v1/trash')).items.find(d=>d.document_id===document);
   const p=await call('POST','/api/v1/documents/'+document+'/lifecycle-plans',{operation,expected_revision:d.revision});
   await call('POST','/api/v1/lifecycle-plans/'+p.plan_id+'/commit',{plan_id:p.plan_id,expected_revision:p.expected_revision,confirmed:true});
  }
 },{document:input.document});
 const raceContext=await page.context().browser().newContext(),racePeer=await raceContext.newPage();
 try {
  await racePeer.goto(input.origin+'/health/live');await racePeer.evaluate(token=>localStorage.setItem('haeryong.owner-session.v1',token),input.peer_token);
  step='fast trash restore before readable recheck';
  const versionPattern='**/api/v1/versions/'+input.version,renewPattern=versionPattern+'/render-sessions/*/renew';
  const cycled=gate(),read=gate(),renewed=gate();let readableStatus=0,renewStatus=0;
  const originalSession=sessions.at(-1),opens=sessions.length;
  await page.route(versionPattern,async route=>{await cycled.wait();try{const r=await route.fetch();readableStatus=r.status();await route.fulfill({response:r});}finally{read.resolve();}});
  await page.route(renewPattern,async route=>{await read.wait();const r=await route.fetch();renewStatus=r.status();if(!route.request().url().includes('/'+originalSession+'/renew'))throw Error('renew retargeted a new delivery');await route.fulfill({response:r});renewed.resolve();});
  try {
   await peerCycle(racePeer);cycled.resolve();await read.wait();await renewed.wait();
   if(readableStatus!==200||renewStatus<400)throw Error('current readability did not remain separate from revoked delivery');
   await page.waitForFunction(()=>!document.querySelector('iframe')&&!document.querySelector('#reader-context-panel')&&!document.querySelector('.reader-header'));
   if(sessions.length!==opens||(await api('GET',documentPath)).lifecycle!=='archived')throw Error('automatic replacement after fast restore');
  } finally {cycled.resolve();read.resolve();await page.unroute(versionPattern);await page.unroute(renewPattern);}
  step='missed events then resync';
  await page.addInitScript(()=>{
   if(window!==top)return;
   const nativeFetch=window.fetch.bind(window),controllers=new Set();
   window.__dropDeliveryEvents=false;window.__droppedDeliveryEvents=0;
   window.__resumeDeliveryEvents=()=>{window.__dropDeliveryEvents=false;for(const c of controllers)c.abort();};
   window.fetch=async(target,options={})=>{
    const url=new URL(typeof target==='string'?target:target.url,location.href);
    if(url.pathname!=='/api/v1/events')return nativeFetch(target,options);
    const controller=new AbortController();controllers.add(controller);
    const response=await nativeFetch(target,{...options,signal:AbortSignal.any([controller.signal,...(options.signal?[options.signal]:[])])});
    const body=response.body.pipeThrough(new TransformStream({transform(chunk,out){if(window.__dropDeliveryEvents)window.__droppedDeliveryEvents++;else out.enqueue(chunk);}}));
    return new Response(body,{status:response.status,headers:response.headers});
   };
  });
  const resumed=gate(),cycledAgain=gate(),denied=gate();let holdRenew=false,resyncRenewStatus=0;
  await page.route(renewPattern,async route=>{if(holdRenew)await resumed.wait();const r=await route.fetch();if(holdRenew){resyncRenewStatus=r.status();denied.resolve();}await route.fulfill({response:r});});
  try {
   await page.goto(input.route);await page.frameLocator('iframe').locator('#review-fixture').waitFor();
   const opensBeforeResync=sessions.length;
   holdRenew=true;
   await page.route(versionPattern,async route=>{await cycledAgain.wait();const r=await route.fetch();if(r.status()!==200)throw Error('resync document is not currently readable');await route.fulfill({response:r});});
   await page.evaluate(()=>{window.__dropDeliveryEvents=true;});await peerCycle(racePeer);cycledAgain.resolve();
   await page.waitForFunction(()=>window.__droppedDeliveryEvents>0);
   if(await page.locator('iframe').count()!==1)throw Error('fixture did not preserve old frame through missed events');
   if((await api('GET',documentPath)).lifecycle!=='archived')throw Error('peer did not restore before resync');
   await page.evaluate(()=>window.__resumeDeliveryEvents());resumed.resolve();await denied.wait();
   await page.waitForFunction(()=>!document.querySelector('iframe')&&!document.querySelector('#reader-context-panel'));
   if(resyncRenewStatus<400||sessions.length!==opensBeforeResync)throw Error('resync failed to retire the original delivery');
  } finally {resumed.resolve();cycledAgain.resolve();await page.unroute(versionPattern);await page.unroute(renewPattern);}
 } finally {await raceContext.close();}
 step='fresh explicit navigation after resync';
 await page.goto(input.route);await page.frameLocator('iframe').locator('#review-fixture').waitFor();
 step='hasTouch phone lifecycle';
 const touchContext=await page.context().browser().newContext({hasTouch:true,viewport:{width:390,height:844}});let touch;
 try {
  await touchContext.addInitScript(({token,origin})=>{if(window!==top||location.origin!==origin)return;localStorage.setItem('haeryong.owner-session.v1',token);window.__lifecycleTouchStarts=0;document.addEventListener('touchstart',event=>{if(event.isTrusted)window.__lifecycleTouchStarts++;},true);},{token:input.peer_token,origin:input.origin});
  const phone=await touchContext.newPage();await phone.goto(input.route);await phone.frameLocator('iframe').locator('#review-fixture').waitFor();
  const tapChoose=async name=>{const menu=phone.locator('.lifecycle-control details').first();if(await menu.getAttribute('open')===null)await menu.locator('summary').tap();await phone.getByRole('button',{name,exact:true}).tap();await phone.locator('[data-lifecycle-confirm]').waitFor();await assertImpact(phone,name);};
  const tapConfirm=async()=>{await phone.locator('[data-lifecycle-confirm]').tap();await phone.getByRole('dialog',{name:'수명주기 영향 확인'}).waitFor({state:'hidden'});};
  await tapChoose('보관 해제');await phone.getByRole('dialog',{name:'수명주기 영향 확인'}).getByRole('button',{name:'닫기',exact:true}).tap();
  if((await api('GET',documentPath)).lifecycle!=='archived')throw Error('touch cancel changed lifecycle');
  await tapChoose('보관 해제');await tapConfirm();if((await api('GET',documentPath)).lifecycle!=='active')throw Error('touch unarchive did not commit');
  await tapChoose('휴지통으로 이동');await tapConfirm();await phone.waitForFunction(()=>!document.querySelector('iframe'));
  const beforeTrashPage=await phone.evaluate(()=>({starts:window.__lifecycleTouchStarts,maxTouchPoints:navigator.maxTouchPoints}));
  await phone.goto(input.origin+'/trash');await phone.getByRole('heading',{name:'휴지통',exact:true}).waitFor();await assertTrashDisplay(phone);await tapChoose('복원');await tapConfirm();await phone.waitForURL(u=>u.pathname==='/library');
  const restoreTouches=await phone.evaluate(()=>window.__lifecycleTouchStarts);
  if(beforeTrashPage.maxTouchPoints<1||beforeTrashPage.starts<6||restoreTouches<3||(await api('GET',documentPath)).lifecycle!=='active')throw Error('trusted touch flow or Restore not executed');
  touch={hasTouch:true,width:390,moreDialogCancel:true,explicitConfirm:true,trashRestore:true,trustedTouchStarts:beforeTrashPage.starts+restoreTouches};
 } finally {await touchContext.close();}
 step='final explicit Reader';await page.goto(input.route);await page.frameLocator('iframe').locator('#review-fixture').waitFor();
 step='guarded mobile purge';
 // Reuse the real imported target/referrer, then exercise the new destructive
 // action with trusted phone taps and safe keyboard focus before acceptance.
 const live=await api('GET',documentPath),trashPlan=await api('POST',documentPath+'/lifecycle-plans',{operation:'trash',expected_revision:live.revision});
 await api('POST','/api/v1/lifecycle-plans/'+trashPlan.plan_id+'/commit',{plan_id:trashPlan.plan_id,expected_revision:trashPlan.expected_revision,confirmed:true});
 const purgeContext=await page.context().browser().newContext({hasTouch:true,viewport:{width:390,height:844}});
 let purge;
 try {
  await purgeContext.addInitScript(({token,origin})=>{if(window===top&&location.origin===origin)localStorage.setItem('haeryong.owner-session.v1',token);},{token:input.peer_token,origin:input.origin});
  const phone=await purgeContext.newPage();await phone.goto(input.origin+'/trash');
  const choosePurge=async()=>{const menu=phone.locator('.lifecycle-control details').first();if(await menu.getAttribute('open')===null)await menu.locator('summary').tap();await phone.getByRole('button',{name:'영구 삭제',exact:true}).tap();await phone.locator('[data-purge-backup]').waitFor();while(await phone.getByRole('button',{name:'영향 목록 더 보기',exact:true}).count())await phone.getByRole('button',{name:'영향 목록 더 보기',exact:true}).tap();};
  await choosePurge();
  const box=phone.getByRole('dialog',{name:'수명주기 영향 확인'});
  if(!(await box.innerText()).includes('복원과 취소가 불가능')||!(await box.locator('[data-purge-backup]').innerText()).includes('상태를 제공할 수 없습니다'))throw Error('purge confirmation effects/backup absent');
  await phone.keyboard.press('Enter');await box.waitFor({state:'hidden'});
  if(!(await api('GET','/api/v1/trash')).items.some(row=>row.document_id===input.document))throw Error('opening Enter purged target');
  await choosePurge();await box.getByRole('button',{name:'닫기',exact:true}).tap();
  await choosePurge();const confirm=phone.locator('[data-lifecycle-confirm]');const target=await confirm.boundingBox();if(!target||target.height<44)throw Error('purge confirmation target too small');
  let lostResult,acceptances=0,receiptReads=0,delivered;
  const accepted=new Promise(resolve=>{delivered=resolve;});
  await phone.route('**/api/v1/lifecycle-plans/*/commit',async route=>{const response=await route.fetch();if(response.status()!==202)throw Error('lost response fixture did not accept purge');lostResult=(await response.json()).data;acceptances++;await route.abort('failed');delivered();});
  phone.on('request',request=>{if(lostResult&&request.method()==='GET'&&request.url().endsWith('/api/v1/operations/'+lostResult.plan_id))receiptReads++;});
  await confirm.focus();await phone.keyboard.press('Enter');await accepted;
  const journal=await phone.evaluate(()=>Object.keys(localStorage).filter(key=>key.startsWith('haeryong.lifecycle.v1:')).map(key=>JSON.parse(localStorage.getItem(key))));
  if(!journal.some(row=>row.body.plan_id===lostResult.plan_id))throw Error('lost acceptance cleared original journal');
  await phone.reload();await phone.getByRole('heading',{name:'휴지통',exact:true}).waitFor();
  await phone.getByRole('status').filter({hasText:'영구 삭제'}).first().waitFor();
  const recovered=await phone.evaluate(({plan,job})=>({pending:Object.keys(localStorage).filter(key=>key.startsWith('haeryong.lifecycle.v1:')).some(key=>JSON.parse(localStorage.getItem(key)).body.plan_id===plan),job:Object.keys(localStorage).filter(key=>key.startsWith('haeryong.purge-jobs.v1:')).some(key=>JSON.parse(localStorage.getItem(key)).includes(job))}),{plan:lostResult.plan_id,job:lostResult.job_id});
  const receipt=await api('GET','/api/v1/operations/'+lostResult.plan_id);
  if(acceptances!==1||receiptReads<1||recovered.pending||!recovered.job||receipt.result_metadata.job_id!==lostResult.job_id)throw Error('reload did not recover original plan and job without another acceptance');
  // A remembered job can disappear after a restore/visibility change. A real
  // 404 must neither hide the live purge nor stop its next poll.
  const missing=await phone.evaluate(async job=>{const bucket=Object.keys(localStorage).find(key=>key.startsWith('haeryong.purge-jobs.v1:'));if(!bucket)throw Error('job bucket absent');const id=crypto.randomUUID();await navigator.locks.request(bucket,()=>{const ids=JSON.parse(localStorage.getItem(bucket));if(!ids.includes(job))throw Error('original job absent');localStorage.setItem(bucket,JSON.stringify([...ids,id]));});return {bucket,id};},lostResult.job_id);
  await phone.reload();
  const liveRow=phone.locator(`[data-purge-job="${lostResult.job_id}"]`),missingRow=phone.locator(`[data-purge-job="${missing.id}"]`);
  await liveRow.waitFor();await missingRow.getByText('영구 삭제 작업: 이 계정 또는 서비스에서 더 이상 조회할 수 없습니다.',{exact:true}).waitFor();
  if(await liveRow.locator('[data-purge-dismiss]').count())throw Error('pending/paused job became dismissible');
  const livePoll=await phone.waitForResponse(response=>response.request().method()==='GET'&&response.url().endsWith('/api/v1/jobs/'+lostResult.job_id));if(!livePoll.ok())throw Error('another failed lookup stopped live polling');
  await missingRow.getByRole('button',{name:'작업 기록 닫기',exact:true}).tap();await missingRow.waitFor({state:'hidden'});
  const saved=await phone.evaluate(key=>JSON.parse(localStorage.getItem(key)),missing.bucket);if(saved.includes(missing.id)||!saved.includes(lostResult.job_id))throw Error('dismiss removed live job or retained missing job');
  await phone.reload();await liveRow.waitFor();if(await missingRow.count())throw Error('dismissed job returned after reload');
  if((await api('GET','/api/v1/trash')).items.some(row=>row.document_id===input.document))throw Error('purge did not commit');
  await page.waitForFunction(()=>!document.querySelector('iframe')&&!document.querySelector('#reader-context-panel')&&!document.querySelector('.reader-header'));
  purge={hasTouch:true,width:390,height:844,explicitKeyboardConfirmation:true,safeEnterDefault:true,tapCancel:true,tapImpact:true,backupUnavailable:true,progressVisible:true,lostAcceptanceReload:true,samePlanAndJob:true,acceptances,receiptReads,missingLookupIsolated:true,livePollingContinues:true,missingDismissed:true,pendingRememberedAfterReload:true};
 } finally {await purgeContext.close();}
 return {purge,archiveTrashRestore:true,restorePreviousArchived:true,keyboardConfirmation:true,delayedPositionSaveKeepsPlan:true,changedNoteRefreshKeepsIntent:true,oldDeliveryCannotRevive:true,noEnterDefault:true,cancel:true,fourViewports:true,touchTarget44:true,twoSessionRendererClosure:true,sameContextSiblingReader:true,noAutomaticRendererRevival:true,fastTrashRestoreDisposesReadableDelivery:true,missedEventResyncDisposesDelivery:true,refusedRenewalDisposesFrameAndHost:true,touch,operationEffects:true,incomingReferenceEffects:true,unresolvedIncomingEffect:true,localizedTrashMetadata:true,...boundary};
 }catch(cause){if(input.screenshot)await page.screenshot({path:input.screenshot,fullPage:true});throw Error(step+": "+cause.message+"; host="+(await page.locator('body').innerText()).slice(-1200));}
}
