 const recordJSON=value=>JSON.stringify(value,(_key,item)=>item&&typeof item==="object"&&!Array.isArray(item)?Object.fromEntries(Object.keys(item).sort().map(key=>[key,item[key]])):item);
const c=OWNER_CONSTANTS, rows=[], inputs=[], errors=[];
if(window.__ownerRecordsProbeStarted)throw Error('duplicate owner-records probe injection');
window.__ownerRecordsProbeStarted=true;
let done=false, sequence=0, awaiting=null;
const delay=()=>new Promise(resolve=>setTimeout(resolve,100));
const wait=async(fn,label)=>{const end=Date.now()+30000;while(Date.now()<end){const value=await fn();if(value)return value;await delay();}throw Error('native wait: '+label);};
const report=()=>window.haeryongNativeCall(701101,[{Rows:[...rows,...inputs,{name:'page.done',status:done?'pass':'pending',detail:{expected:c.expected,context:window.__ownerContextTiming??null}}]}]);
const interval=setInterval(()=>void report().catch(()=>{}),1000);
addEventListener('error',e=>errors.push(String(e.message)));
addEventListener('unhandledrejection',e=>errors.push(String(e.reason)));
addEventListener('keydown',e=>{if(!awaiting)return;const parts=awaiting.key.split('+'),base=parts.pop();if(e.isTrusted&&e.key===base&&e.shiftKey===parts.includes('Shift')&&e.ctrlKey===parts.includes('Control')&&e.altKey===parts.includes('Alt')&&e.metaKey===parts.includes('Meta')&&document.activeElement===awaiting.element){awaiting.observed=true;inputs.push({name:'input.observed.'+awaiting.id,status:'pass',detail:{trusted:true,key:e.key,modifiers:parts,element:e.target.id||(e.target.tagName==='BUTTON'?e.target.textContent:e.target.tagName)}});}},true);
const check=async(name,fn)=>{try{rows.push({name,status:'pass',detail:await fn()});}catch(error){rows.push({name,status:'fail',detail:{error:String(error),route:location.pathname+location.search+location.hash,alerts:[...document.querySelectorAll('[role="alert"]')].map(e=>e.textContent),context:window.__ownerContextTiming,connection:connectionObservation(),pageAnnouncement,readyChannel,regionAck,heading:document.querySelector('main h1')?.textContent,status:document.querySelector('[aria-label="연결 상태"]')?.textContent,service:document.querySelector('.service-state')?.dataset.state}});}await report();};
function connectionObservation(){
 const section=document.querySelector('main h1')?.closest('section');if(!section)return null;
 let fiber=section[Object.keys(section).find(key=>key.startsWith('__reactFiber$'))];
 for(;fiber;fiber=fiber.return){const props=fiber.memoizedProps;if(props?.controller&&props?.state)return {controllerPhase:props.controller.state.phase,controllerNotice:props.controller.state.notice,renderedPhase:props.state.phase,renderedNotice:props.state.notice};}
 return null;
}
const visible=e=>e&&e.getClientRects().length>0;
const button=(text,root=document)=>[...root.querySelectorAll('button')].find(e=>e.textContent===text&&visible(e));
const element=async selector=>wait(()=>{const e=document.querySelector(selector);return visible(e)?e:null;},selector);
const key=async(element,keyName='Enter',label='action',guard=()=>{})=>{
 const state=()=>{return {connected:element?.isConnected===true,disabled:element?.disabled===true,inert:!!element?.closest('[inert]'),visible:!!element?.getClientRects().length,documentVisible:document.visibilityState==='visible',modalDialogs:[...document.querySelectorAll('dialog:modal')].map(node=>node.id),focused:document.activeElement===element};};
 try{await wait(()=>{guard();const current=state();return current.connected&&!current.disabled&&!current.inert&&current.visible&&current.documentVisible;},'actual eligible control '+label);}catch(error){throw Error(String(error)+'; control='+JSON.stringify(state()));}
 guard();element.scrollIntoView({block:'center'});element.focus();
 const control=state();if(!control.focused||control.disabled||control.inert||!control.documentVisible)throw Error('actual control unavailable: '+label+'; control='+JSON.stringify(control));
 const id=String(++sequence),request={id,key:keyName,element,observed:false};awaiting=request;
 inputs.push({name:'input.request.'+id,status:'pass',detail:{key:keyName,label,element:element.id||element.tagName,control}});await report();
 await wait(()=>request.observed,'PID-native trusted key '+label);awaiting=null;
};
const scroll=(_element,direction,label,guard)=>key(button(direction>0?'문맥 끝':'문맥 처음'),'Enter',label,guard);
const setValue=(element,value)=>{const proto=element instanceof HTMLTextAreaElement?HTMLTextAreaElement.prototype:HTMLSelectElement.prototype;Object.getOwnPropertyDescriptor(proto,'value').set.call(element,value);element.dispatchEvent(new Event(element instanceof HTMLSelectElement?'change':'input',{bubbles:true}));};
// Observe the real newly-created page channel before opening the Notes sheet.
let pageAnnouncement=null,announcedFrame=null,readyChannel=null,regionAck=null;
const originalPost=MessagePort.prototype.postMessage,observedPorts=new WeakSet();
MessagePort.prototype.postMessage=function(message,...rest){
 if(!observedPorts.has(this)){observedPorts.add(this);this.addEventListener('message',event=>{if(event.data?.type==='region.focused')regionAck=event.data;});}
 if(message?.type==='reference.navigation')readyChannel=message;
 return originalPost.call(this,message,...rest);
};
addEventListener('message',event=>{if(event.source===document.querySelector('iframe[title="결과 뷰어"]')?.contentWindow&&event.data?.type==='viewer.loaded'){pageAnnouncement=event.data;announcedFrame=event.source;}});
const go=async(route,version)=>{
 document.querySelector('#reader-context-panel')?.dispatchEvent(new Event('cancel',{cancelable:true}));
 const previous=document.querySelector('iframe[title="결과 뷰어"]');pageAnnouncement=announcedFrame=readyChannel=regionAck=null;
 history.pushState({haeryongReaderContext:crypto.randomUUID()},'',route);dispatchEvent(new PopStateEvent('popstate'));
 await wait(()=>document.querySelector('.service-state[data-state="verified"]'),'verified native host');
 if(version)await wait(()=>document.querySelector('.review-control')?.dataset.versionId===version,'exact version host');
 await wait(()=>{const frame=document.querySelector('iframe[title="결과 뷰어"]');return frame&&frame!==previous;},'fresh actual reader frame');
 await wait(()=>!document.body.textContent.includes('페이지를 여는 중…'),'actual page loaded');
 const target=new URL(route,location.origin),region=new URLSearchParams(target.hash.slice(1)).get('region');
 await wait(()=>pageAnnouncement&&announcedFrame===document.querySelector('iframe[title="결과 뷰어"]')?.contentWindow&&readyChannel?.render_instance_id===pageAnnouncement.render_instance_id&&readyChannel.page_path===target.searchParams.get('page')&&(!version||readyChannel.version_id===version),'exact current ready page channel');
 if(region)await wait(()=>regionAck?.render_instance_id===readyChannel.render_instance_id&&regionAck.payload.region_id===region,'initial exact region acknowledgment before Notes');
};
const notes=async()=>{
 const panel=await wait(()=>document.querySelector('#reader-context-panel'),'context panel');if(!panel.open)(await element('#reader-context-toggle')).click();
 (await element('#reader-notes-tab')).click();await element('#note-new');
};
const closeNotes=async()=>{document.querySelector('#reader-context-panel').dispatchEvent(new Event('cancel',{cancelable:true}));await delay();};
if(c.phase==='context-classes'){
 await go(c.fixture.routes['no-region.html'],c.fixture.boundary.version_id);await notes();
 for(const value of c.fixture.context_cases)await check('ui.context.'+value.name,async()=>{
  const entry=await wait(()=>[...document.querySelectorAll('.note-entry')].find(e=>e.textContent.includes('Task6 supporting context '+value.name)),'supporting exact context Note');
  const actual=await readNoteContextCases({scroll,readButton:button('저장된 context 읽기',entry),key,wait,expected:(value.prefix||'')+value.pattern.repeat(value.repeat)+(value.suffix||''),className:value.name,expectedBytes:value.bytes,expectedSha256:value.sha256});
  if(actual.textBytes!==value.bytes||actual.sha256!==value.sha256)throw Error('independent supporting stream/DOM digest differs');return actual;
 });
}
const noteBinding=entry=>{const fiber=entry?.[Object.keys(entry).find(name=>name.startsWith('__reactFiber$'))],key=fiber?.key;if(typeof key!=='string')return null;const split=key.lastIndexOf(':');return {annotation_id:key.slice(0,split),revision:Number(key.slice(split+1))};};
const notesProps=()=>{const node=document.querySelector('.notes-panel');let fiber=node?.[Object.keys(node).find(name=>name.startsWith('__reactFiber$'))];for(;fiber;fiber=fiber.return){const p=fiber.memoizedProps;if(p?.controller&&p.anchor?.version_id)return p;}throw Error('mounted Notes owner unavailable');};
const readLargeBody=async()=>{
 const expected=c.fixture.large_body_identity;if(!expected)throw Error('declared exact large-body identity missing');
 const props=notesProps(),client=props.controller.client,namespace=JSON.stringify([props.controller.state.capabilities?.library_id,props.controller.state.session?.principal_id,props.anchor.version_id]);
 const snapshot=async()=>{const raw=localStorage.getItem('haeryong.notes.v1:'+namespace),record=raw?JSON.parse(raw):{},text=document.querySelector('#note-body')?.value,bytes=text===undefined?null:new TextEncoder().encode(text);return {entries:[...document.querySelectorAll('.note-entry')].map(noteBinding),more:!!button('노트 더 보기',document.querySelector('.notes-panel')),phase:props.controller.state.phase,consent:document.querySelector('#note-confirmation')?.open===true,alerts:[...document.querySelectorAll('[role="alert"]')].map(e=>e.textContent),draft:record.draft?{id:record.draft.id,revision:record.draft.revision,anchor:record.draft.anchor,base:record.draft.base?{annotation_id:record.draft.base.annotation_id,revision:record.draft.base.revision}:null}:null,pending:record.pending??null,editor:bytes?{length:text.length,bytes:bytes.length,sha256:[...new Uint8Array(await crypto.subtle.digest('SHA-256',bytes))].map(n=>n.toString(16).padStart(2,'0')).join('')}:null};};
 const observation={expected,before:await snapshot(),acquisitions:[]},original=client.annotationBodyStream;
 client.annotationBodyStream=async function(id,revision){const item={annotation_id:id,revision};observation.acquisitions.push(item);try{const stream=await original.call(client,id,revision);item.acquired={etag:stream.etag,byte_size:stream.byte_size,bytes_sha256:stream.bytes_sha256};return stream;}catch(error){item.error={name:error.name,message:error.message,status:error.status,code:error.code,details:error.details};throw error;}};
 try{
  if(observation.before.draft||observation.before.pending&&!observation.before.pending.rejected||observation.before.consent)throw Error('BLOCKED: reconcile actual draft/intent/consent before exact body read');
  const entry=await wait(()=>[...document.querySelectorAll('.note-entry')].find(e=>{const n=noteBinding(e);return visible(e)&&n?.annotation_id===expected.annotation_id&&n.revision===expected.revision;}),'exact large-body annotation/revision');observation.selected=noteBinding(entry);
  const action=button('전체 본문 읽기·편집',entry);if(!action)throw Error('bound full-body action absent');await key(action,'Enter','read exact saved large body');await wait(()=>document.querySelector('#note-body')?.value==='Task6 native large body 🌊\n'.repeat(5000),'complete large body in native host editor');observation.after=await snapshot();if(observation.after.draft?.base?.annotation_id!==expected.annotation_id||observation.after.draft.base.revision!==expected.revision)throw Error('published editor identity differs');return observation;
 }catch(error){observation.after=await snapshot();throw Error(String(error)+'; exactBodyRead='+JSON.stringify(observation));}finally{client.annotationBodyStream=original;}
};
const scope=async value=>{const label=[...document.querySelectorAll('.notes-panel label')].find(e=>e.textContent.startsWith('새 노트·재연결 범위'));const select=label?.querySelector('select');if(!select||[...select.options].find(e=>e.value===value)?.disabled)throw Error('scope unavailable: '+value);setValue(select,value);await delay();};
const save=async(text,target)=>{
 // Reader exposes selection only after matching its current page/render binding.
 if(notesProps().anchor.version_id===c.fixture.boundary.version_id&&new URL(location.href).searchParams.get('page')==='a.html')await wait(()=>{const p=notesProps();return p.anchor.page_path==='a.html'&&p.selection?.contextRef&&p.selection.selectedText==='Task6 exact native fallback'&&readyChannel?.render_instance_id===pageAnnouncement?.render_instance_id;},'current bound page context before Note admission');
 await scope(target);await key(await element('#note-new'),'Enter','new note');
 if(target==='region'&&new URL(location.href).searchParams.get('page')==='a.html')await wait(()=>document.querySelector('#note-selection')?.value==='Task6 exact native fallback'&&Object.keys(localStorage).some(k=>k.startsWith('haeryong.notes.v1:')&&JSON.parse(localStorage.getItem(k)).draft?.anchor.version_id===c.fixture.boundary.version_id&&JSON.parse(localStorage.getItem(k)).draft?.contextRef),'bound host large-context draft adoption');
 setValue(await element('#note-body'),text);await delay();await key(await element('#note-save'),'Enter','explicit Note Save');
 await wait(()=>!document.querySelector('#note-body'),'Note committed');
};
const closeMenu=()=>{for(const node of document.querySelectorAll('.lifecycle-control details[open]'))node.removeAttribute('open');};
const choose=async text=>{
 closeMenu();const menu=await element('.lifecycle-control details');menu.querySelector('summary').click();
 const action=await wait(()=>button(text,menu),'lifecycle '+text);action.click();await element('[data-lifecycle-confirm]');
};
const confirm=async()=>{await key(await element('[data-lifecycle-confirm]'),'Enter','explicit lifecycle confirmation');await wait(()=>!document.querySelector('[aria-label="수명주기 영향 확인"]')?.open,'confirmation acknowledgement');};
await wait(()=>window.__haeryongNativeTransport==='HAEYONG_NATIVE_TRANSPORT_BOUND','actual native transport marker');
if(c.phase==='old-authority')await check('ui.old-authority-refused',async()=>{
 // Match the real Connect surface and controller-supported unauthenticated
 // notices; refusal itself still requires the original upstream 401/no attach.
 const notices=['이 브라우저는 아직 연결되지 않았습니다.','이 장치의 연결이 해제되었습니다. 다시 연결하세요.','연결이 만료되었습니다. 다시 연결하세요.','저장된 연결 자격이 유효하지 않습니다. 다시 연결하세요.'];
 await wait(()=>document.querySelector('main h1')?.textContent==='Haeryong 연결'&&notices.includes(document.querySelector('[aria-label="연결 상태"]')?.textContent.trim()),'actual unauthenticated Connect heading/status');
 if(document.querySelector('.notes-panel,.review-control,iframe[title="결과 뷰어"]'))throw Error('old authority exposed protected Reader');
 const completed=connectionObservation();if(completed?.controllerPhase!=='unauthenticated'||completed.renderedPhase!=='unauthenticated'||completed.controllerNotice!==completed.renderedNotice)throw Error('Connect did not publish the completed refusal snapshot: '+JSON.stringify(completed));
 return {actualNativeHostRefusal:true,connection:connectionObservation(),heading:document.querySelector('main h1')?.textContent,status:document.querySelector('[aria-label="연결 상태"]')?.textContent,productionPairingStoreNotQualified:true};
});
if(c.phase==='consent'){await go(c.fixture.routes['no-region.html'],c.fixture.boundary.version_id);rows.push(...await notesConfirmationCases({key,wait}));}
if(c.phase==='ambiguity')await check('ui.ambiguous-successor-explicit-page',async()=>{
 await go(c.fixture.successor_routes.boundary,c.fixture.successors.boundary.version_id);if(!document.querySelector('#review-state')?.textContent.startsWith('미검토'))throw Error('successor automatically Reviewed');await notes();
 const versionSelect=[...document.querySelectorAll('.notes-panel label')].find(e=>e.textContent.startsWith('노트가 저장된 버전')).querySelector('select');setValue(versionSelect,c.fixture.boundary.version_id);
 const entry=()=>[...document.querySelectorAll('.note-entry')].find(e=>e.textContent.includes('Task6 native original b.html'));await wait(entry,'known original Note at old version');const revision=Number(entry().textContent.match(/개정 (\d+)/)[1]);
 await scope('region');await key(button('현재 읽는 위치로 재연결',entry()),'Enter','open unproved successor region');await key(await element('#note-confirmation [data-note-confirm]'),'Enter','explicit unproved region attempt');await wait(()=>[...document.querySelectorAll('[role="alert"]')].some(e=>e.textContent.includes('재연결 위치')),'actual unproved-region refusal');
 if(!entry().textContent.includes('개정 '+revision))throw Error('refused reattach changed revision');await scope('page');await key(button('현재 읽는 위치로 재연결',entry()),'Enter','open reliable page fallback');await key(await element('#note-confirmation [data-note-confirm]'),'Enter','explicit reliable page fallback');await wait(()=>!entry(),'old current listing excludes reattached Note');
 setValue(versionSelect,c.fixture.successors.boundary.version_id);await wait(()=>entry()?.textContent.includes('개정 '+(revision+1)),'exact next revision at explicit successor');button('이력',entry()).click();await element('[aria-label="노트 이력"]');await closeNotes();if(!document.querySelector('#review-state')?.textContent.startsWith('미검토'))throw Error('Note action completed Review');return {unprovedRegionRefused:true,explicitPageFallback:true,retainedOriginalHistory:true};
});
if(c.phase==='create'){
 for(const name of ['mulgae','gaori','a.html','b.html','no-region.html'])await check('ui.save-review.'+name,async()=>{
  const identity=c.fixture.originals[name]||c.fixture.boundary;
  await go(c.fixture.routes[name],identity.version_id);if(name==='a.html')await wait(()=>[...document.querySelectorAll('[role="status"]')].some(e=>e.textContent.includes('선택한 영역: retry')),'actual large context adoption ready');await notes();await save('Task6 native original '+name,name==='no-region.html'?'page':'region');await closeNotes();
  if(['mulgae','gaori','a.html'].includes(name)){
   await key(await element('#review-start'),'Enter','explicit whole-version Review start');await element('#review-complete');
   await key(await element('#review-complete'),'Enter','explicit whole-version Review complete');
   await wait(()=>document.querySelector('#review-state')?.textContent.startsWith('검토 완료'),'whole-version Review saved');
   await key(await element('#reader-revisit'),'Enter','independent Revisit');
   await wait(()=>document.querySelector('#reader-revisit')?.getAttribute('aria-pressed')==='true','Revisit saved');
  }
  return {version:identity.version_id,page:name,actualHostActions:true};
 });
 await check('ui.large-body',async()=>{await go(c.fixture.routes['a.html'],c.fixture.boundary.version_id);await notes();await save('Task6 native large body 🌊\n'.repeat(5000),'version');await closeNotes();return {streamedBodyInput:true};});
 await check('ui.explicit-reattach',async()=>{
  await go(c.fixture.routes['no-region.html'],c.fixture.boundary.version_id);await notes();await scope('page');
  const entry=await wait(()=>[...document.querySelectorAll('.note-entry')].find(e=>e.textContent.includes('Task6 native original b.html')),'original b tuple');
  await key(button('현재 읽는 위치로 재연결',entry),'Enter','open Note reattachment confirmation');
  await key(await element('#note-confirmation [data-note-confirm]'),'Enter','explicit named Note reattachment');
  await wait(()=>[...document.querySelectorAll('.note-entry')].some(e=>e.textContent.includes('Task6 native original b.html')&&e.textContent.includes('개정 2')),'reattached current revision');
  await closeNotes();return {originalAnchorRetained:true,explicitConfirmation:true};
 });
}
if(c.phase==='reopen')for(const [name,identity]of Object.entries({...c.fixture.originals,boundary:c.fixture.boundary}))await check('ui.reopen.'+name,async()=>{
 await go(c.fixture.routes[name==='boundary'?'a.html':name],identity.version_id);if(name==='boundary')await wait(()=>[...document.querySelectorAll('[role="status"]')].some(e=>e.textContent.includes('선택한 영역: retry')),'new frame selection before saved-body read');await notes();
 await wait(()=>document.querySelector('.notes-panel')?.textContent.includes('Task6 native original '+(name==='boundary'?'a.html':name)),'original native UI Note');
 let contextProof=null,bodyRead=null;
  if(name==='boundary'){
  bodyRead=await readLargeBody();
  await key(button('취소',document.querySelector('.annotation-editor')),'Enter','request local read-draft discard');await key(await element('#note-confirmation [data-note-confirm]'),'Enter','explicit local read-draft discard');await wait(()=>!document.querySelector('#note-body'),'read draft discarded');
  const original=[...document.querySelectorAll('.note-entry')].find(e=>e.textContent.includes('Task6 native original a.html'));contextProof=await readNoteContextCases({scroll,readButton:button('저장된 context 읽기',original),key,wait,expected:JSON.stringify({selected_text:'Task6 exact native fallback',text:'Task6 native retained context 🌊'.repeat(90000)}),expectedBytes:3060057,expectedSha256:'8427a983dc9d185b12f8b487775b9cea423b219bc9a87cf15c7c05e32a962eb6'});
 }
 const history=button('이력',document.querySelector('.note-entry'));if(!history)throw Error('Note history action absent');history.click();await element('[aria-label="노트 이력"]');await closeNotes();
 if(!document.querySelector('#review-state')?.textContent.startsWith('검토 완료'))throw Error('old Review not retained');
 await key(await wait(()=>button('검토 이력'),'Review history'),'Enter','view Review history');
 await wait(()=>document.body.textContent.includes('검토 중 → 검토 완료'),'exact old Review event');
 return {version:identity.version_id,historyVisible:true,context:contextProof,bodyRead};
});
if(c.phase==='loss')await check('ui.loss.'+c.boundary,async()=>{
 await go(c.fixture.routes['no-region.html'],c.fixture.boundary.version_id);await notes();await scope('page');await key(await element('#note-new'),'Enter','new loss note');setValue(await element('#note-body'),'Task6 native lost '+c.boundary);await delay();await key(await element('#note-save'),'Enter','loss Save');
 const outcome=await wait(()=>{if(!document.querySelector('#note-body'))return {saved:true};const reconcile=button('원래 저장 요청 확인');return reconcile?.isConnected&&!reconcile.disabled?{reconcile}:null;},'settled original Save outcome');
 if(outcome.reconcile){await key(outcome.reconcile,'Enter','reconcile original Save');await wait(()=>!document.querySelector('#note-body'),'reconciled original Save');}
 return {boundary:c.boundary,originalIntentOnly:true};
});
if(c.phase==='stale')await check('ui.stale-deliberate-merge',async()=>{
 await go(c.fixture.routes['no-region.html'],c.fixture.boundary.version_id);await notes();
 const entry=await wait(()=>[...document.querySelectorAll('.note-entry')].find(e=>e.textContent.includes('Task6 native original no-region.html')),'original note');button('편집',entry).click();setValue(await element('#note-body'),'Task6 native local draft retained');await delay();
 await key(await element('#note-save'),'Enter','stale Note Save');await element('[aria-label="노트 충돌 비교"]');
 if(document.querySelector('#note-body').value!=='Task6 native local draft retained')throw Error('stale local text lost');
 button('현재 개정과 합친 초안으로 저장 준비').click();setValue(await element('#note-body'),'Task6 native deliberately merged');await delay();await key(await element('#note-save'),'Enter','explicit merged Save');await wait(()=>!document.querySelector('#note-body'),'merged commit');return {peer:'supporting real concurrent API write',localDraftRetained:true};
});
if(c.phase==='reload')await check('ui.server-reload-explicit-consent',async()=>{
 await go(c.fixture.routes['no-region.html'],c.fixture.boundary.version_id);await notes();const entry=await wait(()=>[...document.querySelectorAll('.note-entry')].find(e=>e.textContent.includes('Task6 native deliberately merged')),'merged Note');await key(button('편집',entry),'Enter','edit before server reload conflict');setValue(await element('#note-body'),'Task6 native reload local text');await delay();await key(await element('#note-save'),'Enter','stale Save before explicit server reload');await element('[aria-label="노트 충돌 비교"]');
 await key(button('서버 내용으로 다시 불러오기'),'Enter','request server reload');await key(await element('#note-confirmation [data-note-cancel]'),'Enter','cancel server reload');if(document.querySelector('#note-body').value!=='Task6 native reload local text')throw Error('cancelled server reload erased local text');
 await key(button('서버 내용으로 다시 불러오기'),'Enter','request explicit server reload');await key(await element('#note-confirmation [data-note-confirm]'),'Enter','named server reload confirmation');await wait(()=>document.querySelector('#note-body')?.value==='Task6 native reload peer revision','captured server revision loaded');
 await key(button('취소',document.querySelector('.annotation-editor')),'Enter','discard server-read draft');await key(await element('#note-confirmation [data-note-confirm]'),'Enter','explicit discard of server-read draft');await wait(()=>!document.querySelector('#note-body'),'server-read draft discarded');return {cancelThenExplicitReload:true};
});
if(c.phase==='dialog-pending')await check('ui.unconfirmed-dialog-before-restart',async()=>{
 await go(c.fixture.routes['no-region.html'],c.fixture.boundary.version_id);await notes();if(document.querySelector('#note-body')||button('원래 저장 요청 확인'))throw Error('BLOCKED: prior draft/intent must be explicitly reconciled before restart fixture');await key(await element('#note-new'),'Enter','new restart draft');setValue(await element('#note-body'),'Task6 restart retained local draft');
 const draft=await wait(()=>{const key=Object.keys(localStorage).find(key=>key.startsWith('haeryong.notes.v1:')&&JSON.parse(localStorage.getItem(key)).draft?.anchor.version_id===c.fixture.boundary.version_id&&JSON.parse(localStorage.getItem(key)).draft?.text==='Task6 restart retained local draft');return key?JSON.parse(localStorage.getItem(key)).draft:null;},'durable restart draft');
 await key(button('취소',document.querySelector('.annotation-editor')),'Enter','unconfirmed discard before normal close');await element('#note-confirmation:modal');return {draft,confirmed:false};
});
if(c.phase==='dialog-reopen')await check('ui.unconfirmed-dialog-restart-retains-draft',async()=>{
 await go(c.fixture.routes['no-region.html'],c.fixture.boundary.version_id);await notes();await wait(()=>document.querySelector('#note-body')?.value==='Task6 restart retained local draft','retained local draft');if(document.querySelector('#note-confirmation').open)throw Error('old consent revived');
 const keyName=Object.keys(localStorage).find(key=>key.startsWith('haeryong.notes.v1:')&&JSON.parse(localStorage.getItem(key)).draft?.anchor.version_id===c.fixture.boundary.version_id&&JSON.parse(localStorage.getItem(key)).draft?.text==='Task6 restart retained local draft');const actual=JSON.parse(localStorage.getItem(keyName));if(recordJSON(actual.draft)!==recordJSON(c.fixture.restart_draft)||actual.pending!==null)throw Error('restart changed draft/intent: '+JSON.stringify({actual,expected:{draft:c.fixture.restart_draft,pending:null}}));
 await key(button('취소',document.querySelector('.annotation-editor')),'Enter','request discard after restart');await key(await element('#note-confirmation [data-note-confirm]'),'Enter','explicit discard after restart');await wait(()=>!document.querySelector('#note-body'),'retained draft discarded explicitly');return {ephemeralConsentGone:true,exactDraftRetained:true};
});
if(c.phase==='lifecycle'){
 await check('ui.native-narrow-safe-confirmation',async()=>{
  await go(c.fixture.routes.mulgae,c.fixture.originals.mulgae.version_id);await choose('보관');
  const dialog=document.querySelector('[aria-label="수명주기 영향 확인"]');
  if(innerWidth>420||dialog.scrollWidth>dialog.clientWidth+1||document.activeElement?.hasAttribute('data-lifecycle-confirm'))throw Error('actual narrow viewport/focus unsafe');
  const size={width:innerWidth,height:innerHeight};await key(document.activeElement,'Enter','safe default closes confirmation');await wait(()=>!dialog.open,'default close');return {actualViewport:size,noCommit:true};
 });
 await check('ui.real-archive-trash-restore',async()=>{
  await go(c.fixture.routes.mulgae,c.fixture.originals.mulgae.version_id);await choose('보관');await confirm();await choose('휴지통으로 이동');await confirm();
  history.pushState({},'','/trash');dispatchEvent(new PopStateEvent('popstate'));await wait(()=>document.querySelector('h1')?.textContent==='휴지통','Trash screen');await choose('복원');await confirm();
  return {hostConfirmations:true};
 });
 await check('ui.native-no-default-purge',async()=>{
  await go(c.fixture.routes.gaori,c.fixture.originals.gaori.version_id);await choose('휴지통으로 이동');await confirm();history.pushState({},'','/trash');dispatchEvent(new PopStateEvent('popstate'));await wait(()=>document.querySelector('h1')?.textContent==='휴지통','Trash screen');await choose('영구 삭제');
  const dialog=document.querySelector('[aria-label="수명주기 영향 확인"]');if(dialog.scrollWidth>dialog.clientWidth+1||document.activeElement?.hasAttribute('data-lifecycle-confirm'))throw Error('purge default unsafe');
  await key(document.activeElement,'Enter','safe purge default closes');await wait(()=>!dialog.open,'purge cancelled');await choose('복원');await confirm();return {purgeNotConfirmed:true};
 });
}
if(errors.length)rows.push({name:'page.errors',status:'fail',detail:errors});
const caseRows=rows.filter(row=>row.name.startsWith('ui.'));
if(caseRows.length!==c.expected.length||!c.expected.every((name,index)=>caseRows[index]?.name===name&&caseRows[index].status==='pass'))rows.push({name:'case.denominator',status:'fail',detail:{expected:c.expected,actual:caseRows.map(row=>row.name)}});
done=true;clearInterval(interval);await report();
