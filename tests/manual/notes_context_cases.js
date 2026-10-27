// Exact-revision complete-context usability. Intended outcomes: Task6 T01/T03,
// schema-registry saved-context contract, ui/flows, design-system accessibility.
// Input drivers send actual browser/PID keys. Measurements are observations only.
async function readNoteContextCases({readButton,key,scroll,wait,expected,className=null,expectedBytes,expectedSha256}) {
 const renderedObservation=['crlf-tabs','trailing-newline','large-multiline'].includes(className);
 const started=performance.now(),times=[],frame=document.querySelector('iframe[title="결과 뷰어"]');
 const draft=()=>Object.keys(localStorage).filter(k=>k.startsWith('haeryong.notes.v1:')).sort().map(k=>[k,localStorage.getItem(k)]);
 const before=JSON.stringify(draft()),budget=()=>{const elapsed=performance.now()-started;if(elapsed>15000)throw Error('complete context read/layout/input exceeded unchanged 15000ms guard: '+elapsed);return elapsed;};
 const find=name=>[...document.querySelectorAll('.note-context-controls button')].find(e=>e.textContent===name);
 const rangeAt=(pre,start,end)=>{const range=document.createRange(),walk=document.createTreeWalker(pre,NodeFilter.SHOW_TEXT);let node,offset=0,found=false;while((node=walk.nextNode())){const next=offset+node.length;if(!found&&start<=next){range.setStart(node,start-offset);found=true;}if(found&&end<=next){range.setEnd(node,end-offset);return range;}offset=next;}if(start===0&&end===0&&!pre.textContent){range.selectNodeContents(pre);range.collapse(true);return range;}throw Error('actual mounted offset absent');};
 const geometry=(pre,end)=>{if(!expected.length)return {empty:true,offset:0};const offset=end?expected.length:0,range=rangeAt(pre,offset,offset),cluster=new Intl.Segmenter(undefined,{granularity:'grapheme'}).segment(expected).containing(end?expected.length-1:0),fragments=[...rangeAt(pre,cluster.index,cluster.index+cluster.segment.length).getClientRects()],rects=[...range.getClientRects()].filter(r=>r.height>0);if(!rects.length&&fragments.length===1&&fragments[0].height>0)rects.push(fragments[0]);const box=pre.getBoundingClientRect(),viewport={left:box.left+pre.clientLeft,top:box.top+pre.clientTop,right:box.left+pre.clientLeft+pre.clientWidth,bottom:box.top+pre.clientTop+pre.clientHeight};const visible=rects.length>0&&rects.every(r=>r.left>=viewport.left-1&&r.right<=viewport.right+1&&r.top>=viewport.top-1&&r.bottom<=viewport.bottom+1);return {offset,cluster:[cluster.index,cluster.index+cluster.segment.length],rects:rects.map(r=>r.toJSON()),fragments:fragments.map(r=>r.toJSON()),viewport,visible,scrollLeft:pre.scrollLeft,scrollTop:pre.scrollTop,scrollWidth:pre.scrollWidth,scrollHeight:pre.scrollHeight};};
 const observe=label=>{window.__ownerContextTiming={className,stage:label,elapsed:performance.now()-started,visibility:document.visibilityState,focused:document.hasFocus(),modal:!!document.querySelector('.note-context'),textPresent:!!document.querySelector('.note-context-text'),alerts:[...document.querySelectorAll('.note-context [role="alert"]')].map(e=>e.textContent)};};
 const within=async(promise,label)=>{observe(label);let timer;try{return await Promise.race([promise,new Promise((_,reject)=>{timer=setTimeout(()=>{observe(label+' timed out');reject(Error('complete context unchanged 15000ms deadline: '+JSON.stringify(window.__ownerContextTiming)));},Math.max(0,15000-(performance.now()-started)));})]);}finally{clearTimeout(timer);budget();}};
 const input=async(...args)=>within(key(...args,budget),'input '+args[2]);
 const turn=()=>within(new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))),'two actual animation frames'),failures=[];
 const settled=async(pre,axis)=>{let previous=pre[axis],same=0;while(same<3){await within(new Promise(resolve=>requestAnimationFrame(resolve)),'settled actual animation frame');budget();const current=pre[axis];same=current===previous?same+1:0;previous=current;}return previous;};
 const capture=async(label,fn)=>{try{return await fn();}catch(error){failures.push({label,error:String(error)});return null;}};
 await input(readButton,'Enter','open exact saved context');budget();
 const pre=await within(wait(()=>{budget();const p=document.querySelector('.note-context-text');return p?.textContent===expected?p:null;},'complete literal context text'),'complete literal context DOM');
 observe('complete literal DOM reached');const box=pre.getBoundingClientRect();times.push({stage:'stream-dom-layout',elapsed:budget()});
 if(box.width<=0||box.height<=0||box.height>Math.min(320,innerHeight*.35)+2||document.documentElement.scrollWidth>innerWidth+1)throw Error('context viewport unbounded or outer overflow');
 // Normal render observation is reached independently of endpoint assertions.
 await turn();budget();const style=getComputedStyle(pre),rendered={rect:pre.getBoundingClientRect().toJSON(),whiteSpace:style.whiteSpace,overflowWrap:style.overflowWrap,wordBreak:style.wordBreak,visibility:style.visibility,display:style.display,userSelect:style.userSelect};await capture('ordinary visible literal styles',async()=>{if(style.whiteSpace!=='pre-wrap'||style.overflowWrap!=='anywhere'||style.wordBreak!=='normal'||style.visibility!=='visible'||style.display==='none'||style.userSelect==='none'||pre.isContentEditable||[...pre.children].some(e=>getComputedStyle(e).display!=='inline'||getComputedStyle(e).unicodeBidi!=='normal'))throw Error(JSON.stringify(rendered));});budget();
 const first=await capture('first actual offset',async()=>{await input(find('문맥 처음'),'Enter','context Start');const value=geometry(pre,false);budget();if(!value.empty&&!value.visible)throw Error(JSON.stringify(value));return value;});
 const movement=[];for(const [axis,forward,backward]of [['scrollTop','PageDown','PageUp'],['scrollLeft','ArrowRight','ArrowLeft']])await capture('movement '+axis,async()=>{const overflow=axis==='scrollTop'?pre.scrollHeight>pre.clientHeight+1:pre.scrollWidth>pre.clientWidth+1;if(!overflow){movement.push({axis,overflow:false});return;}const gesture=axis==='scrollLeft'&&scroll;
  // Semantic Start/End expose logical text positions. A combining grapheme's
  // ink can overflow even when both caret positions are already in view.
  if(gesture){const endpoints=[geometry(pre,false),geometry(pre,true)],inView=point=>point.empty||point.rects.length>0&&point.rects.every(rect=>rect.left>=point.viewport.left-1&&rect.right<=point.viewport.right+1);if(endpoints.every(inView)){movement.push({axis,overflow:true,method:'logical endpoints already horizontally visible',endpoints});return;}}
  const send=(direction,label)=>gesture?within(scroll(pre,direction,label,budget),'native context control '+label):input(pre,direction>0?forward:backward,label);const before=await settled(pre,axis);await send(1,'context '+axis);const after=await settled(pre,axis);if(after===before)throw Error('trusted '+(gesture?'context control':'key')+' did not move overflowing '+axis);await send(-1,'reverse context '+axis);const reversed=await settled(pre,axis);if((after-before)*(reversed-after)>=0)throw Error('reverse gesture did not reverse '+axis);movement.push({axis,overflow:true,method:gesture?'trusted native horizontal control':'trusted keyboard',before,after,reversed});});
 const final=await capture('final actual offset',async()=>{await input(find('문맥 끝'),'Enter','context End');const value=geometry(pre,true);times.push({stage:'real-end-offset-layout',elapsed:budget()});if(!value.empty&&!value.visible)throw Error(JSON.stringify(value));return value;});
 const walk=document.createTreeWalker(pre,NodeFilter.SHOW_TEXT),nodes=[];let node;while((node=walk.nextNode()))nodes.push(node);
 const offsetFor=(container,offset)=>{if(container!==pre&&!pre.contains(container))throw Error('selection escaped actual context tree');let prefix=0;for(const node of nodes){if(node===container)return prefix+offset;if(!container.contains(node)&&(node.compareDocumentPosition(container)&Node.DOCUMENT_POSITION_FOLLOWING))prefix+=node.length;}if(container.nodeType===Node.ELEMENT_NODE)return prefix+[...container.childNodes].slice(0,offset).reduce((sum,n)=>sum+n.textContent.length,0);throw Error('native endpoint absent from mounted text');};
 const digest=async text=>[...new Uint8Array(await crypto.subtle.digest('SHA-256',new TextEncoder().encode(text)))].map(n=>n.toString(16).padStart(2,'0')).join('');
 const selected={},selectionAt=async(label,a,b)=>{const selection=window.getSelection(),range=selection?.rangeCount===1?selection.getRangeAt(0):null;if(!range)throw Error('one native associated Range required');const raw=range.toString(),native=selection.toString(),slice=expected.slice(a,b),start=offsetFor(range.startContainer,range.startOffset),end=offsetFor(range.endContainer,range.endOffset);const observation={start,end,expectedStart:a,expectedEnd:b,rawExact:raw===slice,rawLength:raw.length,nativeLength:native.length,nativeExact:native===slice,nativeSha256:await digest(native),serialization:renderedObservation?'rendered observation':'strict decoded equality'};selected[label]=observation;if(raw!==slice||start!==a||end!==b)throw Error('actual associated Range/offset differs: '+JSON.stringify(observation));if(!renderedObservation&&native!==slice)throw Error('native string differs: '+JSON.stringify(observation));budget();return observation;};
 await capture('full selection',async()=>{await input(find('문맥 전체 선택'),'Enter','select complete context');return selectionAt('full',0,expected.length);});
 await capture('partial/cross-node selection',async()=>{if(!expected.length){const selection=window.getSelection();selection.removeAllRanges();selection.addRange(rangeAt(pre,0,0));return selectionAt('partial',0,0);}const cut=nodes.length>1?nodes[0].length:Math.floor(expected.length/2),segments=new Intl.Segmenter(undefined,{granularity:'grapheme'}).segment(expected),first=segments.containing(Math.max(0,cut-2)),last=segments.containing(Math.min(expected.length-1,cut+4)),a=first.index,b=last.index+last.segment.length,selection=window.getSelection();selection.removeAllRanges();selection.addRange(rangeAt(pre,a,b));return selectionAt('partial',a,b);});
 await capture('restored full selection',async()=>{await input(find('문맥 전체 선택'),'Enter','restore complete native selection');return selectionAt('restoredFull',0,expected.length);});
 const focusState=()=>{const e=document.activeElement;return {tag:e?.tagName,id:e?.id,label:e?.getAttribute('aria-label'),button:e?.tagName==='BUTTON'?e.textContent:null,viewport:e===pre,contained:!!e&&!!pre.closest('dialog')?.contains(e)};},keyboardTraversal=[];
 await capture('Tab/ShiftTab',async()=>{const events=[],observed=e=>{if(e.key==='Tab')events.push({trusted:e.isTrusted,shift:e.shiftKey,alt:e.altKey,defaultPrevented:e.defaultPrevented});};addEventListener('keydown',observed);try{const before=focusState();await input(pre,'Tab','leave context viewport');await turn();const after=focusState();keyboardTraversal.push({direction:'forward',before,after,events:[...events]});if(document.activeElement!==find('문맥 처음'))throw Error('Tab destination differs from first semantic context control: '+JSON.stringify(after));await input(find('문맥 처음'),'Shift+Tab','return to context viewport');await turn();keyboardTraversal.push({direction:'reverse',after:focusState(),events:[...events]});if(document.activeElement!==pre)throw Error('ShiftTab did not return: '+JSON.stringify(focusState()));budget();}finally{removeEventListener('keydown',observed);}});
 const bytes=new TextEncoder().encode(pre.textContent),hash=[...new Uint8Array(await crypto.subtle.digest('SHA-256',bytes))].map(n=>n.toString(16).padStart(2,'0')).join('');
 await capture('complete decoded DOM/bytes/hash',async()=>{if(pre.textContent!==expected||bytes.length!==expectedBytes||hash!==expectedSha256)throw Error('independent decoded/byte/hash mismatch '+JSON.stringify({bytes:bytes.length,expectedBytes,hash,expectedSha256}));});
 const close=find('context 닫기');await input(close,'Enter','close complete saved context');budget();
 if(document.querySelector('.note-context')||document.querySelector('iframe[title="결과 뷰어"]')!==frame||JSON.stringify(draft())!==before)throw Error('context controls changed records/frame or failed to close');
 const result={guardMs:15000,elapsed:budget(),textBytes:bytes.length,sha256:hash,className,renderedObservation,textLength:expected.length,nodes:nodes.length,defaultCopy:'unchanged native path; no clipboard operation',first,final,selected,rendered,movement,keyboardTraversal,times,failures,frameRetained:true,journalUnchanged:true};if(failures.length)throw Error('complete context failed: '+JSON.stringify(result));return result;
}

// Supporting stream barriers around the actual mounted NotesPanel and real client.
// Browser-only fault coverage; ordinary native owner paths retain their inputs.
async function notesReadLifetimeCases({key,wait,resize,noteId,expected}) {
 const rows=[],button=name=>[...document.querySelectorAll('.note-context-controls button')].find(e=>e.textContent===name);
 const panel=()=>document.querySelector('.notes-panel'),frame=document.querySelector('iframe[title="결과 뷰어"]');
 const journals=()=>JSON.stringify(Object.keys(localStorage).filter(k=>k.startsWith('haeryong.notes.v1:')).sort().map(k=>[k,localStorage.getItem(k)]));
 let fiber=panel();fiber=fiber[Object.keys(fiber).find(k=>k.startsWith('__reactFiber$'))];let controller;
 for(;fiber;fiber=fiber.return)if(fiber.memoizedProps?.controller?.client){controller=fiber.memoizedProps.controller;break;}
 if(!controller)throw Error('actual NotesPanel controller absent');
 const client=controller.client,original=client.annotationContextStream;
 const readButton=()=>[...document.querySelectorAll('.note-entry')].find(e=>e.textContent.includes('Task6 supporting context plain-json'))?.querySelectorAll('button');
 const opener=()=>[...readButton()].find(e=>e.textContent==='저장된 context 읽기');
 const notes=async()=>{const sheet=document.querySelector('#reader-context-panel');if(!sheet.open)document.querySelector('#reader-context-toggle').click();document.querySelector('#reader-notes-tab').click();await wait(()=>panel()?.checkVisibility(),'actual Notes active');};
 const check=async(name,fn)=>{let release=()=>{};try{const before=journals();const value=await fn(fn=>release=fn);if(frame!==document.querySelector('iframe[title="결과 뷰어"]'))throw Error('read lifetime remounted frame');if(name!=='typing-resize'&&journals()!==before)throw Error('read lifetime changed journal bytes');rows.push({name:'ui.read-lifetime.'+name,status:'pass',detail:value});}catch(e){rows.push({name:'ui.read-lifetime.'+name,status:'fail',detail:{error:String(e),stack:e.stack?.split('\n').slice(0,6),context:document.querySelector('.note-context')?.textContent}});}finally{client.annotationContextStream=original;release();if(button('context 닫기'))await key(button('context 닫기'),'Enter','read lifetime cleanup');await notes();}};
 const hold=(kind,setRelease)=>{
  let release,entered=false,cancelled=0,bodyRead=false,finished=false,handle,output;
  const gate=new Promise(resolve=>release=resolve);setRelease(release);
  client.annotationContextStream=async function(...args){
   if(entered)return Reflect.apply(original,this,args);
   if(args[0]!==noteId)throw Error('supporting barrier wrong exact note');
   handle=await Reflect.apply(original,this,args);entered=true;
   const cancel=()=>{cancelled++;handle.cancel();try{output?.error(new DOMException('Supporting cancelled read','AbortError'));}catch{}};
   if(kind==='acquisition'||kind==='error'){await gate;if(kind==='error'){handle.cancel();finished=true;throw Error('Task6 supporting obsolete acquisition error');}finished=true;return {...handle,cancel};}
   const reader=handle.body.getReader();const body=new ReadableStream({start(c){output=c;},async pull(c){bodyRead=true;await gate;try{const part=await reader.read();if(part.done)c.close();else c.enqueue(part.value);}catch(e){try{c.error(e);}catch{}}},cancel(){handle.cancel();}});
   finished=true;return {...handle,body,cancel};
  };
  return {release,get entered(){return entered;},get cancelled(){return cancelled;},get bodyRead(){return bodyRead;},get finished(){return finished;}};
 };
 for(const kind of ['acquisition','body'])await check(kind+'-Close',async setRelease=>{
  const held=hold(kind,setRelease);await key(opener(),'Enter','held '+kind+' context open');await wait(()=>held.entered&&(kind==='acquisition'||held.bodyRead),'actual held '+kind);
  if(document.querySelector('.note-context-text'))throw Error('held read published text');
  await key(button('context 닫기'),'Enter','Close during '+kind);document.querySelector('#note-new').focus();held.release();
  await wait(()=>held.cancelled>0,'late/acquired handle cancelled');if(document.querySelector('.note-context')||document.activeElement!==document.querySelector('#note-new'))throw Error('retired read reopened or stole focus');return {realStream:true,lateHandle:kind==='acquisition',cancelCalls:held.cancelled};
 });
 for(const kind of ['acquisition','error'])await check('superseded-'+(kind==='error'?'error':'success'),async setRelease=>{
  const held=hold(kind,setRelease);await key(opener(),'Enter','old held context');await wait(()=>held.entered,'old exact acquired handle');
  await key(opener(),'Enter','replacement actual read');await wait(()=>document.querySelector('.note-context-text')?.textContent===expected,'replacement exact text');
  const view=document.querySelector('.note-context'),focused=document.activeElement;held.release();await wait(()=>held.finished&&(kind==='error'||held.cancelled>0),'obsolete completion settles');
  // Flush the already-resolved promise continuations, without a timing retry.
  await Promise.resolve();await Promise.resolve();
  if(document.querySelector('.note-context')!==view||document.querySelector('.note-context-text')?.textContent!==expected||view.querySelector('[role="alert"]')||document.activeElement!==focused)throw Error('obsolete text/error/focus published');return {actualReplacement:true,obsolete:kind,lateCancel:held.cancelled};
 });
 await check('protected-scope',async setRelease=>{
  const held=hold('acquisition',setRelease);await key(opener(),'Enter','read before tab retirement');await wait(()=>held.entered,'exact handle before scope retirement');
  document.querySelector('#reader-evidence-tab').click();await wait(()=>!document.querySelector('.note-context'),'hidden Notes retires context');held.release();await wait(()=>held.cancelled>0,'scope-retired handle cancellation');
  if(document.activeElement===opener())throw Error('hidden opener gained focus');
  await notes();client.annotationContextStream=original;
  const scoped=hold('acquisition',setRelease),capabilities=client.capabilities;
  try{
   await key(opener(),'Enter','read before protected capability retirement');await wait(()=>scoped.entered,'held handle before authority scope retirement');
   // Supporting capability projection through the ACTUAL controller. No server
   // configuration, credential, API or production authority is modified.
   client.capabilities=async function(){const actual=await Reflect.apply(capabilities,this,[]);return {...actual,features:{...actual.features,annotations:false}};};
   await controller.verify();await wait(()=>!document.querySelector('.note-context'),'controller scope retirement');scoped.release();await wait(()=>scoped.cancelled>0,'protected-scope late handle cancelled');
   return {activeTabRetired:true,controllerCapabilityRetired:true,supportingProjection:true,cancelCalls:[held.cancelled,scoped.cancelled]};
  }finally{client.capabilities=capabilities;scoped.release();await controller.verify();}

 });
 await check('typing-resize',async setRelease=>{
  if(document.querySelector('#note-body'))throw Error('existing draft prerequisite must be reconciled');
  await key(document.querySelector('#note-new'),'Enter','read lifetime retained draft');
  const editor=await wait(()=>document.querySelector('#note-body'),'new draft editor committed'),set=value=>{Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(editor,value);editor.dispatchEvent(new Event('input',{bubbles:true}));};
  set('Task6 read lifetime draft');await wait(()=>journals().includes('Task6 read lifetime draft'),'real draft persisted');
  const held=hold('body',setRelease);await key(opener(),'Enter','read while typing');await wait(()=>held.bodyRead,'actual held body');const view=document.querySelector('.note-context');
  set('Task6 typing during owned context');await wait(()=>journals().includes('Task6 typing during owned context'),'new typing persisted');const before=journals();
  await resize(1440,900);await wait(()=>innerWidth===1440&&!document.querySelector('#reader-context-panel').matches(':modal'),'actual desktop resize');
  if(document.querySelector('.note-context')!==view||held.cancelled)throw Error('typing/resize retired current read');held.release();await wait(()=>document.querySelector('.note-context-text')?.textContent===expected,'retained read publishes real text');
  if(journals()!==before||document.querySelector('#note-body')!==editor||editor.value!=='Task6 typing during owned context')throw Error('read replaced newer draft');
  await key(button('context 닫기'),'Enter','Close retained read');
  await key([...document.querySelectorAll('.annotation-editor button')].find(e=>e.textContent==='취소'),'Enter','explicit test draft discard');await key(document.querySelector('#note-confirmation [data-note-confirm]'),'Enter','confirm test draft discard');await wait(()=>!document.querySelector('#note-body'),'test draft discarded');
  return {typingRetained:true,resizeRetained:true,realBody:true};
 });
 return rows;
}
