// Actual trusted-host controls only. `key` is supplied by Playwright or the
// owned-PID native input driver; supporting fault hooks are explicitly named.
async function notesConfirmationCases({key,wait}) {
 const rows=[];
 const visible=e=>e&&e.getClientRects().length>0;
 const button=(name,root=document)=>[...root.querySelectorAll('button')].find(e=>visible(e)&&e.textContent===name);
 const find=async selector=>wait(()=>{const e=document.querySelector(selector);return visible(e)?e:null;},selector);
 const value=(e,text)=>{Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(e,text);e.dispatchEvent(new Event('input',{bubbles:true}));};
 // Read only this actual component's owner/version journal. WKWebView may
 // retain unrelated namespaces from earlier isolated test libraries.
 let fiber=document.querySelector('.notes-panel');fiber=fiber[Object.keys(fiber).find(k=>k.startsWith('__reactFiber$'))];let state;
 for(;fiber;fiber=fiber.return)if(fiber.memoizedProps?.controller){state=fiber.memoizedProps.controller.state;break;}
 if(!state)throw Error('actual Notes owner state absent');
 const journal='haeryong.notes.v1:'+JSON.stringify([state.capabilities.library_id,state.session.principal_id,document.querySelector('.review-control').dataset.versionId]);
 const record=()=>JSON.parse(localStorage.getItem(journal)||'null');
 const unchanged=(before)=>{if(JSON.stringify(record())!==JSON.stringify(before))throw Error('cancel changed captured draft/pending');};
 const dialog=()=>document.querySelector('#note-confirmation');
 // dialog.close() removes open before queuing close; verify focus after the
 // actual close handlers, not merely after the attribute changes (HTML dialog).
 const dismissWithKey=async(element,name,label)=>{
  const node=dialog();let closed=false;const observed=()=>{closed=true;};node.addEventListener('close',observed,{once:true});
  try{await key(element,name,label);await wait(()=>closed&&!node.open,'actual confirmation close event');}finally{node.removeEventListener('close',observed);}
 };
 const close=async()=>{if(dialog()?.open)await dismissWithKey(await find('#note-confirmation [data-note-cancel]'),'Enter','safe confirmation cleanup');};
 const check=async(name,fn)=>{try{rows.push({name,status:'pass',detail:await fn()});}catch(error){rows.push({name,status:'fail',detail:{error:String(error),alerts:[...document.querySelectorAll('[role="alert"]')].map(e=>e.textContent)}});await close();}};
 const notes=async()=>{const panel=document.querySelector('#reader-context-panel');if(!panel.open)document.querySelector('#reader-context-toggle').click();document.querySelector('#reader-notes-tab').click();await find('#note-new');await wait(()=>panel.open&&panel.matches(':modal')===(innerWidth<1280)&&document.querySelector('#reader-notes-tab')?.getAttribute('aria-selected')==='true','actual parent Sheet mode and active Notes');};
 await notes();
 const scope=[...document.querySelectorAll('.notes-panel select')][0];Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set.call(scope,'page');scope.dispatchEvent(new Event('change',{bubbles:true}));
 await wait(()=>scope.value==='page','page-only consent destination');
 const entry=()=>[...document.querySelectorAll('.note-entry')].find(e=>visible(e)&&e.textContent.includes('Task6 consent saved note'));
 await check('ui.notes.setup-own-save',async()=>{
  if(document.querySelector('#note-body'))throw Error('unsafe prerequisite: existing draft must be reconciled');
  await key(await find('#note-new'),'Enter','consent case new note');value(await find('#note-body'),'Task6 consent saved note');await key(await find('#note-save'),'Enter','consent case real Save');await wait(()=>!document.querySelector('#note-body')&&entry(),'consent note committed');
  await key(await find('#note-new'),'Enter','new local draft');value(await find('#note-body'),'Task6 consent retained draft');await wait(()=>record()?.draft?.text==='Task6 consent retained draft','draft persisted');return {actualUISave:true};
 });
 if(!entry()||!document.querySelector('#note-body'))return [...rows,{name:'ui.notes.prerequisite',status:'fail',detail:'dependent cases unexecuted: own Save/draft prerequisite failed'}];
 const openers={new:()=>document.querySelector('#note-new'),edit:()=>button('편집',entry()),reattach:()=>button('현재 읽는 위치로 재연결',entry()),delete:()=>button('노트 삭제',entry()),discard:()=>button('취소',document.querySelector('.annotation-editor'))};
 for(const [kind,opener] of Object.entries(openers))await check('ui.notes.safe.'+kind,async()=>{
  const before=record(),source=opener(),frame=document.querySelector('iframe[title="결과 뷰어"]');
  for(const dismissal of ['Enter','Escape','button']){
   await key(source,'Enter','open '+kind+' confirmation');await wait(()=>dialog()?.matches(':modal'),'trusted HTML modal');
   const cancel=await find('#note-confirmation [data-note-cancel]'),confirm=await find('#note-confirmation [data-note-confirm]');
   if(document.activeElement!==cancel||dialog().scrollWidth>dialog().clientWidth+1)throw Error('unsafe initial focus or horizontal overflow');
   document.querySelector('#reader-context-toggle').focus();if(!dialog().contains(document.activeElement))throw Error('background gained focus despite modal');
   await key(cancel,'Shift+Tab','confirmation reverse focus containment');if(document.activeElement!==confirm)throw Error('Shift+Tab escaped confirmation');
   await key(confirm,'Tab','confirmation forward focus containment');if(document.activeElement!==cancel)throw Error('Tab escaped confirmation');
   await dismissWithKey(cancel,dismissal==='button'?'Enter':dismissal,dismissal==='button'?'explicit safe close':'safe default '+dismissal);
   await wait(()=>!dialog().open,'safe close');unchanged(before);if(document.querySelector('#note-body')?.value!==before.draft.text)throw Error('safe close replaced visible text');if(document.querySelector('iframe[title="결과 뷰어"]')!==frame)throw Error('confirmation remounted the Reader');if(document.activeElement!==source)throw Error('focus did not return to actual opener');
  }
  return {dismissals:['default Enter','Escape','named Close'],tab:true,shiftTab:true,viewport:{width:innerWidth,height:innerHeight},draftUnchanged:true};
 });
 await check('ui.notes.invalidate.panel',async()=>{
  const before=record();await key(openers.discard(),'Enter','open discard before panel closure');await wait(()=>dialog()?.open,'discard modal');
  // Supporting route/visibility transition; no synthetic consent or mutation.
  document.querySelector('#reader-context-panel').dispatchEvent(new Event('cancel',{cancelable:true}));await wait(()=>!dialog().open,'hidden panel invalidates ticket');unchanged(before);await notes();return {supportingVisibilityTransition:true};
 });
 await check('ui.notes.invalidate.tab',async()=>{
  const before=record();await key(openers.discard(),'Enter','open discard before tab transition');await wait(()=>dialog()?.open,'discard modal');
  document.querySelector('#reader-evidence-tab').click();await wait(()=>!dialog().open,'hidden Notes tab invalidates ticket');unchanged(before);await notes();return {supportingTabTransition:true};
 });
 await check('ui.notes.invalidate.route',async()=>{
  const before=record(),opener=openers.discard();await key(opener,'Enter','open discard before route replacement');await wait(()=>dialog()?.open,'discard modal');
  const route=new URL(location.href);route.searchParams.set('task6-consent-route','changed');history.pushState({haeryongReaderContext:crypto.randomUUID()},'',route);dispatchEvent(new PopStateEvent('popstate'));
  await wait(()=>!document.querySelector('#note-confirmation')?.open,'replacement entry invalidates consent');unchanged(before);if(document.activeElement===opener)throw Error('obsolete opener retained focus');await notes();return {supportingRouteTransition:true,draftRetained:true};
 });
 await check('ui.notes.storage-refusal',async()=>{
  const before=record(),original=Storage.prototype.setItem;let failures=0;
  try{Storage.prototype.setItem=function(name,value){if(name.startsWith('haeryong.notes.v1:')){failures++;throw Error('Task6 supporting storage fault');}return Reflect.apply(original,this,[name,value]);};
   await key(openers.new(),'Enter','open replacement before storage fault');await key(await find('#note-confirmation [data-note-confirm]'),'Enter','confirmed replacement storage refusal');
   await wait(()=>failures===1&&!document.querySelector('#note-new').disabled&&[...document.querySelectorAll('[role="alert"]')].some(e=>e.textContent.includes('Task6 supporting storage fault')),'actual storage error and settled admission');unchanged(before);
   if(document.querySelector('#note-body').value!==before.draft.text)throw Error('failed replacement erased visible text');return {supportingStorageFault:true,actualFaultCalls:failures,noVisibleReplacement:true};
  }finally{Storage.prototype.setItem=original;}
 });
 await check('ui.notes.delayed-lock-draft-change',async()=>{
  const before=record(),original=navigator.locks.request.bind(navigator.locks);let release,entered=false,callbacks=0;
  const gate=new Promise(resolve=>release=resolve);
  try{navigator.locks.request=function(name,...args){if(name.startsWith('haeryong.notes.v1:')&&!entered){entered=true;const callback=args.at(-1);args[args.length-1]=(...values)=>{callbacks++;return callback(...values);};return gate.then(()=>original(name,...args));}return original(name,...args);};
   await key(openers.new(),'Enter','open replacement before delayed lock');await key(await find('#note-confirmation [data-note-confirm]'),'Enter','confirmed replacement waiting for lock');await wait(()=>entered,'actual journal lock requested');
   value(await find('#note-body'),'Task6 newer text while lock waits');release();
   await wait(()=>record()?.draft?.text==='Task6 newer text while lock waits','newer typing stored after stale replacement refusal');
   if(callbacks!==1||record().draft.id!==before.draft.id||document.querySelector('#note-body').value!=='Task6 newer text while lock waits')throw Error('queued replacement erased newer draft or callback absent');return {supportingLockBarrier:true,actualLockedCallbacks:callbacks,newerDraftRetained:true};
  }finally{navigator.locks.request=original;release?.();}
 });
 await check('ui.notes.confirm.new',async()=>{
  const before=record();await key(openers.new(),'Enter','open replacement');await key(await find('#note-confirmation [data-note-confirm]'),'Enter','explicit replace draft');await wait(()=>record()?.draft?.id!==before.draft.id,'new draft persisted');
  if(document.querySelector('#note-body').value!=='')throw Error('new draft body not empty');return {capturedDraftReplaced:true};
 });
 await check('ui.notes.confirm.edit',async()=>{
  await key(openers.edit(),'Enter','open Edit replacement');await key(await find('#note-confirmation [data-note-confirm]'),'Enter','explicit Edit replacement');await wait(()=>document.querySelector('#note-body')?.value==='Task6 consent saved note','captured saved note loaded');return {explicitReload:true};
 });
 await check('ui.notes.confirm.reattach',async()=>{
  const before=record();await key(openers.reattach(),'Enter','open reattach');await key(await find('#note-confirmation [data-note-confirm]'),'Enter','explicit reattach');await wait(()=>entry()?.textContent.includes('개정 2'),'reattach committed');
  if(record()?.draft?.id!==before.draft.id||record()?.draft?.text!==before.draft.text)throw Error('reattach erased unrelated draft');return {currentRevision:2,unrelatedDraftRetained:true};
 });
 await check('ui.notes.confirm.delete',async()=>{
  const before=record();await key(openers.delete(),'Enter','open delete');await key(await find('#note-confirmation [data-note-confirm]'),'Enter','explicit delete');await wait(()=>!entry(),'soft delete acknowledged');
  if(record()?.draft?.id!==before.draft.id||record()?.draft?.text!==before.draft.text)throw Error('delete erased unrelated draft');return {knownDeletedIdentityMustBeRetained:true};
 });
 await check('ui.notes.confirm.discard',async()=>{
  await key(openers.discard(),'Enter','open draft discard');await key(await find('#note-confirmation [data-note-confirm]'),'Enter','explicit discard');await wait(()=>!document.querySelector('#note-body'),'captured draft discarded');return {noServerDelete:true};
 });
 return rows;
}
