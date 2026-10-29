async page => {
 const recordJSON=value=>JSON.stringify(value,(_key,item)=>item&&typeof item==="object"&&!Array.isArray(item)?Object.fromEntries(Object.keys(item).sort().map(key=>[key,item[key]])):item);
 const c=OWNER_CONSTANTS, rows=[],errors=[],knownNotes=new Set(),observations=[];let stage='open';
 const observe=response=>{const request=response.request();if(/\/annotations(?:\/[^/?]+)?$/.test(request.url().slice(c.origin.length).split('?')[0])&&['POST','PATCH','DELETE'].includes(request.method()))observations.push((async()=>{if([200,201].includes(response.status())){const wire=await response.json();if(wire.data?.annotation_id)knownNotes.add(wire.data.annotation_id);}})().catch(error=>errors.push('supporting response observation: '+String(error))));};page.on('response',observe);
 page.setDefaultTimeout(15000);
 page.on('pageerror',error=>errors.push(String(error)));
 const check=async(name,fn)=>{try{rows.push({name,status:'pass',detail:await fn()});}catch(error){
  const detail={error:String(error).split('\n')[0],phone:error.phone,stage,route:page.url().split('?')[0],alerts:await page.locator('[role="alert"]').allTextContents(),editor:await page.locator('#note-body').evaluate(e=>({length:e.value.length,visible:e.checkVisibility(),disabled:e.disabled,dialogOpen:e.closest('dialog')?.open,rect:e.getBoundingClientRect().toJSON()})).catch(()=>null),diagnostic:await page.evaluate(async()=>({drafts:Object.keys(localStorage).filter(k=>k.startsWith('haeryong.notes.v1:')).map(key=>({key,record:JSON.parse(localStorage.getItem(key))})),contextTiming:window.__ownerContextTiming,contextRequests:performance.getEntriesByType('resource').filter(e=>e.name.includes('/annotations/')&&e.name.includes('/context?')).map(e=>({name:e.name,startTime:e.startTime,responseStart:e.responseStart,responseEnd:e.responseEnd,duration:e.duration,encodedBodySize:e.encodedBodySize,decodedBodySize:e.decodedBodySize})),contexts:await Promise.all([...document.querySelectorAll('.notes-panel pre')].map(async pre=>({length:pre.textContent.length,prefix:pre.textContent.slice(0,100),sha256:[...new Uint8Array(await crypto.subtle.digest('SHA-256',new TextEncoder().encode(pre.textContent)))].map(n=>n.toString(16).padStart(2,'0')).join('')})))}))};
  rows.push({name,status:'fail',detail});await page.screenshot({path:c.work+'/failure-'+c.invocation+'-'+name.replace(/[^a-zA-Z0-9.-]/g,'_')+'.png'});
  // Retain the failed row/input; use the real Cancel action to isolate later
  // ordinary cases after a pre-Save failure. Never clear an unknown mutation.
  try{if(await page.locator('#note-body').count()&&!await page.getByRole('button',{name:'원래 저장 요청 확인',exact:true}).count()){
   const panel=page.locator('#reader-context-panel');if(!await panel.evaluate(e=>e.open))await page.locator('#reader-context-toggle').click();
   const cancel=page.locator('.annotation-editor').getByRole('button',{name:'취소',exact:true});if(await cancel.isVisible()){await cancel.click();await page.locator('#note-confirmation [data-note-confirm]').click();await page.waitForFunction(()=>!document.querySelector('#note-body'));detail.failedDraftCancelledThroughUI=true;}
  }}catch(cleanupError){detail.cleanupError=String(cleanupError);}
 }};
 const api=async(method,path,body,headers={})=>page.evaluate(async a=>{const response=await fetch('/api/v1'+a.path,{method:a.method,headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1'),...(a.body===undefined?{}:{'Content-Type':'application/json'}),...a.headers},body:a.body===undefined?undefined:JSON.stringify(a.body)});const text=await response.text();return {status:response.status,wire:text?JSON.parse(text):null};},{method,path,body,headers});
 const open=async route=>{await page.goto(c.origin+route);await page.locator('.service-state[data-state="verified"]').waitFor();await page.locator('#review-state').waitFor();await page.frameLocator('iframe').locator('body').waitFor();};
 const notes=async()=>{const panel=page.locator('#reader-context-panel');if(!await panel.evaluate(e=>e.open))await page.locator('#reader-context-toggle').click();await page.locator('#reader-notes-tab').click();await page.locator('#note-new').waitFor();};
 const closeNotes=async()=>{await page.locator('#reader-context-panel').evaluate(e=>e.dispatchEvent(new Event('cancel',{cancelable:true})));};
 if(c.phase==='context-classes'){
  await open(c.fixture.routes['no-region.html']);await notes();
  await page.exposeFunction('__ownerContextClassKey',async(id,name)=>{await page.locator('[data-owner-context-key="'+id+'"]').focus();await page.keyboard.press(name);});
  for(const value of c.fixture.context_cases)await check('ui.context.'+value.name,async()=>{
   const entry=page.locator('.note-entry').filter({hasText:'Task6 supporting context '+value.name}).first();await entry.getByRole('button',{name:'저장된 context 읽기',exact:true}).evaluate(e=>{document.querySelectorAll('[data-owner-context-read]').forEach(old=>old.removeAttribute('data-owner-context-read'));e.dataset.ownerContextRead='true';});
   const actual=await page.evaluate(async({source,value})=>{const run=(new Function(source+';return readNoteContextCases;'))(),wait=async(fn,label)=>{const deadline=performance.now()+15000;while(performance.now()<deadline){const result=await fn();if(result)return result;await new Promise(resolve=>setTimeout(resolve,50));}throw Error(label);};const key=async(e,name,label)=>{if(!e||e.disabled)throw Error('context target unavailable '+label);const id=crypto.randomUUID();e.dataset.ownerContextKey=id;e.scrollIntoView({block:'nearest'});e.focus();const parts=name.split('+'),base=parts.pop();let observed=false;const capture=event=>{if(event.isTrusted&&event.target===e&&event.key===base&&event.shiftKey===parts.includes('Shift')&&event.ctrlKey===parts.includes('Control')&&event.altKey===parts.includes('Alt')&&event.metaKey===parts.includes('Meta'))observed=true;};addEventListener('keydown',capture,true);try{await window.__ownerContextClassKey(id,name);if(!observed)throw Error('trusted context key absent '+label);}finally{removeEventListener('keydown',capture,true);}};return run({readButton:document.querySelector('[data-owner-context-read]'),key,wait,expected:(value.prefix||'')+value.pattern.repeat(value.repeat)+(value.suffix||''),className:value.name,expectedBytes:value.bytes,expectedSha256:value.sha256});},{source:OWNER_CONTEXT_SOURCE,value});
   if(actual.textBytes!==value.bytes||actual.sha256!==value.sha256)throw Error('independent supporting stream/DOM digest differs');return actual;
  });
 }

 if(c.phase==='context-classes'){
  await page.exposeFunction('__ownerReadResize',(width,height)=>page.setViewportSize({width,height}));
  const input=c.fixture.context_cases.find(value=>value.name==='plain-json');
  const actual=await page.evaluate(async({source,input})=>{
   const run=(new Function(source+';return notesReadLifetimeCases;'))();
   const wait=async(fn,label)=>{const end=performance.now()+15000;while(performance.now()<end){const value=await fn();if(value)return value;await new Promise(resolve=>setTimeout(resolve,50));}throw Error('owned read wait: '+label);};
   const key=async(element,name,label)=>{if(!element||element.disabled)throw Error('owned read input unavailable '+label);const id=crypto.randomUUID();element.dataset.ownerContextKey=id;element.focus();let observed=false;const capture=event=>{if(event.isTrusted&&event.target===element&&event.key===name)observed=true;};addEventListener('keydown',capture,true);try{await window.__ownerContextClassKey(id,name);if(!observed)throw Error('trusted owned-read input absent '+label);}finally{removeEventListener('keydown',capture,true);}};
   return run({key,wait,resize:(width,height)=>window.__ownerReadResize(width,height),noteId:input.annotation_id,expected:input.pattern.repeat(input.repeat)});
  },{source:OWNER_CONTEXT_SOURCE,input});rows.push(...actual);
 }

 if(c.phase==='consent'){
  await open(c.fixture.routes['no-region.html']);await notes();
  const input=[];await page.exposeFunction('__ownerConsentKey',async(id,name)=>{await page.locator('[data-owner-consent-key="'+id+'"]').focus();await page.keyboard.press(name);});
  for(const size of [[1440,900],[820,1180],[844,390],[390,844]]){await page.setViewportSize({width:size[0],height:size[1]});
  const actual=await page.evaluate(async source=>{const run=(0,eval)('('+source+')');const wait=async(fn,label)=>{const end=Date.now()+30000;while(Date.now()<end){const v=await fn();if(v)return v;await new Promise(r=>setTimeout(r,100));}throw Error('browser consent wait: '+label);};
   const key=async(e,name,label)=>{if(!e||e.disabled)throw Error('input target unavailable: '+label);const id=crypto.randomUUID();e.dataset.ownerConsentKey=id;e.scrollIntoView({block:'center'});e.focus();let observed=false;const capture=event=>{if(event.isTrusted&&event.target===e&&event.key===name.split('+').at(-1)&&event.shiftKey===name.split('+').includes('Shift')&&event.ctrlKey===name.split('+').includes('Control')&&event.altKey===name.split('+').includes('Alt')&&event.metaKey===name.split('+').includes('Meta'))observed=true;};addEventListener('keydown',capture,true);try{await window.__ownerConsentKey(id,name);if(!observed)throw Error('actual browser keyboard event absent: '+label);}finally{removeEventListener('keydown',capture,true);}};return await run({key,wait});},OWNER_CONSENT_SOURCE);
  rows.push(...actual.map(row=>({...row,name:row.name.replace('ui.notes.','ui.notes.'+size.join('x')+'.')})));}
  await check('ui.notes.touch-confirmation',async()=>{
   await page.locator('#note-new').click();await page.locator('#note-body').fill('Task6 touch discard draft');await page.locator('.annotation-editor').getByRole('button',{name:'취소',exact:true}).click();const confirm=page.locator('#note-confirmation [data-note-confirm]');await confirm.scrollIntoViewIfNeeded();const box=await confirm.boundingBox();if(!box||box.width<44||box.height<44)throw Error('touch confirmation target unreachable');
   await confirm.evaluate(element=>{window.__ownerTouch=[];element.addEventListener('click',event=>window.__ownerTouch.push({trusted:event.isTrusted,pointer:event.pointerType}),{once:true});});
   const cdp=await page.context().newCDPSession(page);try{await cdp.send('Emulation.setTouchEmulationEnabled',{enabled:true});await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x:box.x+box.width/2,y:box.y+box.height/2}]});await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});}finally{await cdp.detach();}
   await page.waitForFunction(()=>!document.querySelector('#note-body'));const events=await page.evaluate(()=>window.__ownerTouch);if(events.length!==1||!events[0].trusted||events[0].pointer!=='touch')throw Error('actual trusted touch confirmation absent');return {emulatedTouch:true,physicalDevice:false,events};
  });
 }
 const save=async(text,scope)=>{await page.getByLabel('새 노트·재연결 범위').selectOption(scope);await page.locator('#note-new').focus();await page.keyboard.press('Enter');await notes();const editor=page.locator('#note-body');await editor.waitFor();if(!await editor.isEditable())throw Error('visible host editor is not editable');
  if(scope==='region'&&(await page.evaluate(()=>new URL(location.href).searchParams.get('page')))==='a.html')await page.waitForFunction(()=>document.querySelector('#note-selection')?.value==='Task6 exact native fallback'&&Object.keys(localStorage).some(k=>k.startsWith('haeryong.notes.v1:')&&JSON.parse(localStorage.getItem(k)).draft?.anchor.version_id===document.querySelector('.review-control')?.dataset.versionId&&JSON.parse(localStorage.getItem(k)).draft?.contextRef));
  // Native DOM input avoids serializing the entire large value into Playwright's
  // action log. The actual visible React textarea and host Save remain in use.
  if(text.length>65536)await editor.evaluate((e,value)=>{Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(e,value);e.dispatchEvent(new Event('input',{bubbles:true}));},text);else await editor.fill(text);
  if(await editor.inputValue()!==text)throw Error('host text input bytes changed');await page.locator('#note-save').focus();await page.keyboard.press('Enter');await page.waitForFunction(()=>!document.querySelector('#note-body'));};
 if(c.phase==='create'){
  for(const name of ['mulgae','gaori','a.html','b.html','no-region.html'])await check('ui.save-review.'+name,async()=>{
   await open(c.fixture.routes[name]);
   const identity=c.fixture.originals[name]||c.fixture.boundary;
   const before=await api('GET','/versions/'+identity.version_id+'/review');
   if(['mulgae','gaori','a.html'].includes(name)&&before.wire.data.value.state!=='unreviewed')throw Error('reading implicitly Reviewed');
   if(name==='mulgae'){
    const frame=page.frameLocator('iframe');for(const [selector,value,expected]of [['#compose-mapping','omit','Omitting the failed selected optional role'],['#compose-mapping','replace','CompositeRoleAlreadySatisfied'],['#compose-publication','cli','exact returned composite run_id'],['#compose-publication','mcp','retryable: false'],['#compose-publication','none','CompositeRoleAlreadySatisfied'],['#compose-source','root-missing','diagnostics cannot replace'],['#compose-source','ancestor-missing','CompositeLineageMismatch']]){await frame.locator(selector).selectOption(value);if(!(await frame.locator('[data-output]').first().innerText()).includes(expected))throw Error('original Mulgae fact missing: '+expected);}
   }
   if(name==='gaori'){
    const frame=page.frameLocator('iframe');for(const [selector,value,expected]of [['#lifecycle-observation','await','waiter only'],['#lifecycle-observation','cancel','does not guarantee killed'],['#lifecycle-session','lost','identical-looking ID'],['#lifecycle-observation','disconnect','reconciliation of the command and final artifacts']]){await frame.locator(selector).selectOption(value);if(!(await frame.locator('[data-output]').first().innerText()).includes(expected))throw Error('original Gaori fact missing: '+expected);}
   }
   if(name==='a.html')await page.getByText('선택한 영역: retry',{exact:true}).waitFor();
   await notes();await save('Task6 browser original '+name,name==='no-region.html'?'page':'region');await closeNotes();
   if(['mulgae','gaori','a.html'].includes(name)){
    await page.locator('#review-start').focus();await page.keyboard.press('Enter');await page.waitForFunction(()=>document.querySelector('#review-state')?.textContent.startsWith('검토 중'));
    await page.locator('#review-complete').focus();await page.keyboard.press('Enter');await page.waitForFunction(()=>document.querySelector('#review-state')?.textContent.startsWith('검토 완료'));
    await page.locator('#reader-revisit').focus();await page.keyboard.press('Enter');await page.waitForFunction(()=>document.querySelector('#reader-revisit')?.getAttribute('aria-pressed')==='true');
   }
   return {client:'actual browser host UI',version:identity.version_id,page:name};
  });
  await check('ui.large-body-context',async()=>{
   await open(c.fixture.routes['a.html']);
   // One successful fixture selection per fresh frame; no competing injection.
   await page.getByText('선택한 영역: retry',{exact:true}).waitFor();await notes();
   await save('Task6 large body 🌊\n'.repeat(5000),'version');
   await save('Task6 large captured context','region');await closeNotes();
   const version=c.fixture.boundary.version_id, result=await api('GET','/versions/'+version+'/annotations');
   const note=result.wire.data.items.find(n=>n.body==='Task6 large captured context');if(!note?.context_ref)throw Error('context not adopted');
   const bytes=await page.evaluate(async id=>{const r=await fetch('/api/v1/annotations/'+id+'/context?revision=1',{headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1')}});if(!r.ok)throw Error('context stream failed');return await r.text();},note.annotation_id);
   const expected=JSON.stringify({selected_text:'Task6 exact native fallback',text:'Task6 native retained context 🌊'.repeat(90000)});
   if(bytes!==expected)throw Error('complete large context bytes differ');return {bodyStreamed:true,contextBytes:await page.evaluate(text=>new TextEncoder().encode(text).length,bytes)};
  });
  await check('ui.explicit-reattach',async()=>{
   await open(c.fixture.routes['no-region.html']);await notes();await page.getByLabel('새 노트·재연결 범위').selectOption('page');
   const entry=page.locator('.note-entry').filter({hasText:'Task6 browser original b.html'}).first();await entry.getByRole('button',{name:'현재 읽는 위치로 재연결',exact:true}).click();
   await page.locator('#note-confirmation [data-note-confirm]').focus();await page.keyboard.press('Enter');await entry.getByText('개정 2',{exact:false}).waitFor();await closeNotes();return {explicitNamedConfirmation:true};
  });
 }
 if(c.phase==='ambiguity')await check('ui.ambiguous-successor-explicit-page',async()=>{
  await open(c.fixture.successor_routes.boundary);if(!(await page.locator('#review-state').innerText()).startsWith('미검토'))throw Error('successor automatically Reviewed');await notes();
  await page.getByLabel('노트가 저장된 버전').selectOption(c.fixture.boundary.version_id);const entry=page.locator('.note-entry').filter({hasText:'Task6 browser original b.html'}).first();await entry.waitFor();
  const notesBefore=await api('GET','/versions/'+c.fixture.boundary.version_id+'/annotations'),note=notesBefore.wire.data.items.find(n=>n.body==='Task6 browser original b.html');if(!note)throw Error('original known Note missing');
  await page.getByLabel('새 노트·재연결 범위').selectOption('region');await entry.getByRole('button',{name:'현재 읽는 위치로 재연결',exact:true}).click();await page.locator('#note-confirmation [data-note-confirm]').click();await page.getByRole('alert').filter({hasText:'재연결 위치'}).waitFor();
  const refused=await api('GET','/annotations/'+note.annotation_id);if(JSON.stringify(refused.wire.data)!==JSON.stringify(note))throw Error('unproved region changed original Note');
  await page.getByLabel('새 노트·재연결 범위').selectOption('page');await entry.getByRole('button',{name:'현재 읽는 위치로 재연결',exact:true}).click();await page.locator('#note-confirmation [data-note-confirm]').click();await entry.waitFor({state:'hidden'});
  await page.getByLabel('노트가 저장된 버전').selectOption(c.fixture.successors.boundary.version_id);await entry.waitFor();await entry.getByRole('button',{name:'이력',exact:true}).click();await page.getByLabel('노트 이력').waitFor();await closeNotes();if(!(await page.locator('#review-state').innerText()).startsWith('미검토'))throw Error('Note reattachment completed Review');return {unprovedRegionRefused:true,explicitPageFallback:true};
 });
 if(c.phase==='reopen')for(const [name,identity]of Object.entries({...c.fixture.originals,boundary:c.fixture.boundary}))await check('ui.reopen.'+name,async()=>{
  await open(c.fixture.routes[name==='boundary'?'a.html':name]);if(name==='boundary')await page.getByText('선택한 영역: retry',{exact:true}).waitFor();await notes();
  const text=await page.locator('[aria-label="Notes"]').innerText();if(!text.includes('Task6 browser original '+(name==='boundary'?'a.html':name)))throw Error('original UI note absent');
   let contextProof=null;
  if(name==='boundary'){
   stage='reopen complete large body in host editor';await page.getByRole('button',{name:'전체 본문 읽기·편집',exact:true}).click();await page.waitForFunction(()=>document.querySelector('#note-body')?.value==='Task6 large body 🌊\n'.repeat(5000));
   await page.locator('.annotation-editor').getByRole('button',{name:'취소',exact:true}).click();await page.locator('#note-confirmation [data-note-confirm]').click();await page.waitForFunction(()=>!document.querySelector('#note-body'));
   stage='reopen complete saved context in host';const original=page.locator('.note-entry').filter({hasText:'Task6 browser original a.html'}).first();
    if(!await page.evaluate(()=>typeof window.__ownerContextKey==='function'))await page.exposeFunction('__ownerContextKey',async(id,name)=>{await page.locator('[data-owner-context-key="'+id+'"]').focus();await page.keyboard.press(name);});
        await original.getByRole('button',{name:'저장된 context 읽기',exact:true}).evaluate(e=>{document.querySelectorAll('[data-owner-context-read]').forEach(old=>old.removeAttribute('data-owner-context-read'));e.dataset.ownerContextRead='true';});
        contextProof=await page.evaluate(async source=>{
         const run=(new Function(source+';return readNoteContextCases;'))();
         const wait=async(fn,label)=>{const deadline=performance.now()+15000;while(performance.now()<deadline){const value=await fn();if(value)return value;await new Promise(resolve=>setTimeout(resolve,50));}throw Error(label);};
         const key=async(e,name,label)=>{if(!e||e.disabled)throw Error('context input unavailable: '+label);const id=crypto.randomUUID();e.dataset.ownerContextKey=id;e.scrollIntoView({block:'nearest'});e.focus();let observed=false;const capture=event=>{if(event.isTrusted&&event.target===e&&event.key===name.split('+').at(-1)&&event.shiftKey===name.split('+').includes('Shift')&&event.ctrlKey===name.split('+').includes('Control')&&event.altKey===name.split('+').includes('Alt')&&event.metaKey===name.split('+').includes('Meta'))observed=true;};addEventListener('keydown',capture,true);try{await window.__ownerContextKey(id,name);if(!observed)throw Error('trusted context key absent: '+label);}finally{removeEventListener('keydown',capture,true);}};
         return await run({readButton:document.querySelector('[data-owner-context-read]'),key,wait,expected:JSON.stringify({selected_text:'Task6 exact native fallback',text:'Task6 native retained context 🌊'.repeat(90000)}),expectedBytes:3060057,expectedSha256:'8427a983dc9d185b12f8b487775b9cea423b219bc9a87cf15c7c05e32a962eb6'});
        },OWNER_CONTEXT_SOURCE);
  }
  await page.getByRole('button',{name:'이력',exact:true}).first().click();await page.getByLabel('노트 이력').waitFor();await closeNotes();
  if(!(await page.locator('#review-state').innerText()).startsWith('검토 완료'))throw Error('old Review missing');
  await page.getByRole('button',{name:'검토 이력',exact:true}).click();await page.getByText('검토 중 → 검토 완료',{exact:false}).waitFor();
  return {version:identity.version_id,historyVisible:true,context:contextProof};
 });
 if(c.phase==='loss')for(const boundary of ['before','after'])await check('ui.loss.'+boundary,async()=>{
  await open(c.fixture.routes['no-region.html']);await notes();let dropped=false,allow=false,original;
  const target='**/api/v1/versions/'+c.fixture.boundary.version_id+'/annotations';
  await page.route('**/api/v1/operations/*',async route=>{if(!allow){await route.fulfill({status:503,body:'{}'});return;}await route.continue();});
  await page.route(target,async route=>{if(route.request().method()!=='POST'){await route.continue();return;}const request=route.request();const captured={key:request.headers()['idempotency-key'],body:request.postData(),path:request.url().slice(c.origin.length).split('?')[0]};if(!original)original=captured;else if(JSON.stringify(captured)!==JSON.stringify(original))throw Error('unknown Save intent changed');if(!dropped){dropped=true;if(boundary==='after'){const response=await route.fetch();if(response.status()!==201)throw Error('lost response was not committed');}await route.abort('failed');return;}await route.continue();});
  try{
   await page.getByLabel('새 노트·재연결 범위').selectOption('page');await page.locator('#note-new').click();await page.locator('#note-body').fill('Task6 browser lost '+boundary);await page.locator('#note-save').focus();await page.keyboard.press('Enter');
   await page.getByRole('button',{name:'원래 저장 요청 확인',exact:true}).waitFor();await page.waitForFunction(()=>[...document.querySelectorAll('button')].some(button=>button.textContent==='원래 저장 요청 확인'&&!button.disabled));if(!dropped)throw Error('declared loss boundary did not fire');allow=true;await page.getByRole('button',{name:'원래 저장 요청 확인',exact:true}).click();await page.waitForFunction(()=>!document.querySelector('#note-body'));
   const found=await api('GET','/versions/'+c.fixture.boundary.version_id+'/annotations');if(found.wire.data.items.filter(n=>n.body==='Task6 browser lost '+boundary).length!==1)throw Error('duplicate or missing recovered Save');
   return {boundary,original,committedResponseLost:boundary==='after'};
  }finally{await page.unroute(target);await page.unroute('**/api/v1/operations/*');}
 });
 if(c.phase==='stale')await check('ui.stale-deliberate-merge',async()=>{
  await open(c.fixture.routes['no-region.html']);await notes();const entry=page.locator('.note-entry').filter({hasText:'Task6 browser original no-region.html'}).first();await entry.getByRole('button',{name:'편집',exact:true}).click();await page.locator('#note-body').fill('Task6 local draft retained');
  const notesNow=await api('GET','/versions/'+c.fixture.boundary.version_id+'/annotations');const note=notesNow.wire.data.items.find(n=>n.body==='Task6 browser original no-region.html');
  const remote=await api('PATCH','/annotations/'+note.annotation_id,{action:'edit',body:'Task6 remote edit',body_ref:null,selected_text:null,context:null,context_ref:note.context_ref},{Authorization:'Bearer '+c.peer_token,'If-Match':note.etag,'Idempotency-Key':await page.evaluate(()=>crypto.randomUUID())});if(remote.status!==200)throw Error('supporting peer edit failed');
  await page.locator('#note-save').focus();await page.keyboard.press('Enter');await page.getByLabel('노트 충돌 비교').waitFor();if(await page.locator('#note-body').inputValue()!=='Task6 local draft retained')throw Error('local draft lost');
  await page.getByRole('button',{name:'현재 개정과 합친 초안으로 저장 준비',exact:true}).click();await page.locator('#note-body').fill('Task6 deliberately merged');await page.locator('#note-save').focus();await page.keyboard.press('Enter');await page.waitForFunction(()=>!document.querySelector('#note-body'));return {supportingPeer:'direct authenticated API',uiMerge:true};
 });
 if(c.phase==='reload')await check('ui.server-reload-explicit-consent',async()=>{
  await open(c.fixture.routes['no-region.html']);await notes();const entry=page.locator('.note-entry').filter({hasText:'Task6 deliberately merged'}).first();await entry.getByRole('button',{name:'편집',exact:true}).click();await page.locator('#note-body').fill('Task6 reload local text');
  const items=await api('GET','/versions/'+c.fixture.boundary.version_id+'/annotations'),note=items.wire.data.items.find(n=>n.body==='Task6 deliberately merged');
  const remote=await api('PATCH','/annotations/'+note.annotation_id,{action:'edit',body:'Task6 browser reload peer revision',body_ref:null,selected_text:null,context:null,context_ref:note.context_ref},{Authorization:'Bearer '+c.peer_token,'If-Match':note.etag,'Idempotency-Key':await page.evaluate(()=>crypto.randomUUID())});if(remote.status!==200)throw Error('supporting reload peer failed');
  await page.locator('#note-save').click();await page.getByLabel('노트 충돌 비교').waitFor();await page.getByRole('button',{name:'서버 내용으로 다시 불러오기',exact:true}).click();await page.locator('#note-confirmation [data-note-cancel]').focus();await page.keyboard.press('Enter');if(await page.locator('#note-body').inputValue()!=='Task6 reload local text')throw Error('cancelled server reload erased local text');
  await page.getByRole('button',{name:'서버 내용으로 다시 불러오기',exact:true}).click();await page.locator('#note-confirmation [data-note-confirm]').focus();await page.keyboard.press('Enter');await page.waitForFunction(()=>document.querySelector('#note-body')?.value==='Task6 browser reload peer revision');
  await page.locator('.annotation-editor').getByRole('button',{name:'취소',exact:true}).click();await page.locator('#note-confirmation [data-note-confirm]').click();await page.waitForFunction(()=>!document.querySelector('#note-body'));return {serverReloadCancelledThenExplicitlyConfirmed:true,supportingPeer:true};
 });
 if(c.phase==='dialog-pending')await check('ui.unconfirmed-dialog-before-restart',async()=>{
  await open(c.fixture.routes['no-region.html']);await notes();await page.locator('#note-new').click();await page.locator('#note-body').fill('Task6 restart retained local draft');
  const draft=await page.evaluate(()=>{const key=Object.keys(localStorage).find(key=>key.startsWith('haeryong.notes.v1:')&&JSON.parse(localStorage.getItem(key)).draft?.anchor.version_id===document.querySelector('.review-control')?.dataset.versionId&&JSON.parse(localStorage.getItem(key)).draft?.text==='Task6 restart retained local draft');return key?JSON.parse(localStorage.getItem(key)).draft:null;});if(!draft)throw Error('restart draft not durably stored');
  await page.locator('.annotation-editor').getByRole('button',{name:'취소',exact:true}).click();await page.locator('#note-confirmation:modal').waitFor();return {draft,confirmed:false};
 });
 if(c.phase==='dialog-reopen')await check('ui.unconfirmed-dialog-restart-retains-draft',async()=>{
  await open(c.fixture.routes['no-region.html']);await notes();await page.waitForFunction(()=>document.querySelector('#note-body')?.value==='Task6 restart retained local draft');if(await page.locator('#note-confirmation').evaluate(dialog=>dialog.open))throw Error('old confirmation revived');
  const actual=await page.evaluate(()=>{const key=Object.keys(localStorage).find(key=>key.startsWith('haeryong.notes.v1:')&&JSON.parse(localStorage.getItem(key)).draft?.anchor.version_id===document.querySelector('.review-control')?.dataset.versionId&&JSON.parse(localStorage.getItem(key)).draft?.text==='Task6 restart retained local draft');return JSON.parse(localStorage.getItem(key));});if(recordJSON(actual.draft)!==recordJSON(c.fixture.restart_draft)||actual.pending!==null)throw Error('restart changed draft or created intent: '+JSON.stringify({actual,expected:{draft:c.fixture.restart_draft,pending:null}}));
  await page.locator('.annotation-editor').getByRole('button',{name:'취소',exact:true}).click();await page.locator('#note-confirmation [data-note-confirm]').click();await page.waitForFunction(()=>!document.querySelector('#note-body'));return {ephemeralConsentGone:true,exactDraftRetained:true,explicitDiscardAfterReopen:true};
 });
 if(c.phase==='managed-purge-sources'){
  const sources=c.fixture.managed_purge_sources,document=c.fixture.originals.mulgae.document_id;
  const list=async path=>{const items=[];let cursor=null;do{const query='limit=64'+(cursor?'&cursor='+encodeURIComponent(cursor):'');const r=await api('GET',path+'?'+query);if(r.status!==200)throw Error('public list failed '+path);items.push(...r.wire.data.items);cursor=r.wire.data.next_cursor;}while(cursor);return items;};
  for(const mode of ['keyboard','touch'])await check('ui.managed-purge.'+mode,async()=>{
   const touch=mode==='touch';let context,target=page;const plans=[],pending=[],consoleMessages=[];let rendererBoundary=null;
   if(touch){context=await page.context().browser().newContext({hasTouch:true,viewport:{width:390,height:844}});await context.addInitScript(({origin,token})=>{if(window===top&&location.origin===origin)localStorage.setItem('haeryong.owner-session.v1',token);},{origin:c.origin,token:c.token});target=await context.newPage();target.on('pageerror',e=>errors.push(String(e)));}
   else await page.setViewportSize({width:1280,height:900});
   await target.context().addInitScript(()=>{window.__managedPurgeInputs=[];for(const name of ['keydown','pointerdown'])addEventListener(name,e=>{if(e.isTrusted)window.__managedPurgeInputs.push({type:name,key:e.key,pointerType:e.pointerType});},true);});
   const observeConsole=message=>{if(['error','warning'].includes(message.type())&&message.location().url?.startsWith(c.origin))consoleMessages.push({type:message.type(),text:message.text(),path:message.location().url.slice(c.origin.length).split('?')[0]});};target.on('console',observeConsole);
   const observePlan=response=>{if(response.request().method()==='POST'&&response.url().includes('/documents/'+document+'/lifecycle-plans'))pending.push((async()=>{const wire=await response.json();if(response.status()===201||response.status()===200)plans.push(wire.data);})());};target.on('response',observePlan);
   const activate=async locator=>{await locator.scrollIntoViewIfNeeded();if(touch)await locator.tap();else{await locator.focus();await target.keyboard.press('Enter');}};
   const choose=async name=>{const menu=target.locator('.lifecycle-control details').first();if(await menu.getAttribute('open')===null)await activate(menu.locator('summary'));await activate(target.getByRole('button',{name,exact:true}));await target.locator('[data-lifecycle-confirm]').waitFor();};
   const confirm=async()=>{await activate(target.locator('[data-lifecycle-confirm]'));await target.getByRole('dialog',{name:'수명주기 영향 확인'}).waitFor({state:'hidden'});};
   try{
    await target.goto(c.origin+c.fixture.routes.mulgae);await target.locator('#review-state').waitFor();
    await target.waitForFunction(()=>{const form=document.querySelector('.reader-content form');if(!form?.hasAttribute('action'))return false;const url=new URL(form.action);return ['http:','https:'].includes(url.protocol)&&url.origin!==location.origin&&url.pathname==='/__haeryong/handoff';});
    rendererBoundary=await target.locator('.reader-content form').evaluate(form=>({origin:new URL(form.action).origin,expectedOriginSource:'existing handoff form action; secret is not read',hostOrigin:location.origin,declaredFrameSrc:document.querySelector('iframe').getAttribute('src'),sandbox:document.querySelector('iframe').sandbox.value}));
    const frame=await (await target.locator('iframe').elementHandle()).contentFrame();if(!frame)throw Error('renderer child frame absent');
    await frame.waitForURL(rendererBoundary.origin+'/**');await frame.locator('body').waitFor();
    rendererBoundary.loaded=await frame.evaluate(()=>({url:location.href,origin:location.origin,bodyReady:document.body!==null}));
    if(!rendererBoundary.loaded.bodyReady||!rendererBoundary.loaded.url.startsWith(rendererBoundary.origin+'/')||rendererBoundary.loaded.origin!==rendererBoundary.origin||rendererBoundary.loaded.origin===rendererBoundary.hostOrigin)throw Error('loaded renderer is not on its expected separate origin');
    if(!(await target.title())||await target.locator('body').innerText()==='')throw Error('blank or unidentified host page');
    await choose('휴지통으로 이동');await confirm();
    await target.goto(c.origin+'/trash');await target.getByRole('heading',{name:'휴지통',exact:true}).waitFor();
    const before=(await list('/trash')).find(row=>row.document_id===document);if(!before)throw Error('actual document not in Trash');
    const jobs=(await list('/jobs')).map(row=>row.job_id).sort();
    await choose('영구 삭제');
    const dialog=target.getByRole('dialog',{name:'수명주기 영향 확인'});
    while(await dialog.getByRole('button',{name:'영향 목록 더 보기',exact:true}).count())await activate(dialog.getByRole('button',{name:'영향 목록 더 보기',exact:true}));
    await Promise.all(pending);const plan=plans.filter(row=>row.operation==='purge').at(-1);
    if(!plan||plan.impact.managed_source_snapshot!==1)throw Error('actual public impact must count exactly one selected snapshot');
    const records=plan.items.filter(row=>row.kind==='managed_source_snapshot');
    if(records.length!==1||records[0].id!==sources.snapshot_impact_id||records.some(row=>row.id===sources.reference_impact_id))throw Error('reference-only same-hash ownership entered purge impact');
    const countRow=dialog.locator('dl > div').filter({hasText:'보관한 소스 스냅샷'});
    if(await countRow.count()!==1||(await countRow.locator('dd').innerText()).trim()!=='1')throw Error('managed snapshot label/count not rendered');
    const item=dialog.locator('li').filter({hasText:'보관한 소스 스냅샷'});if(await item.count()!==1)throw Error('managed snapshot list item missing');
    await activate(item.locator('summary'));if(!(await item.innerText()).includes(sources.snapshot_impact_id))throw Error('actual managed snapshot item identity not rendered');
    if(await dialog.evaluate(e=>e.scrollWidth>e.clientWidth+1))throw Error('purge dialog horizontal overflow');
    const explainedConsoleMessages=[],unexpectedConsoleMessages=[];
    for(const message of consoleMessages){
     if(message.type==='error'&&message.path==='/favicon.ico'&&message.text==='Failed to load resource: the server responded with a status of 404 (Not Found)')explainedConsoleMessages.push({...message,reason:'Existing optional favicon route is absent; not a failed app resource.'});
     else if(message.type==='warning'&&message.text==="An iframe which has both allow-scripts and allow-same-origin for its sandbox attribute can escape its sandboxing."&&rendererBoundary.loaded.origin!==rendererBoundary.hostOrigin)explainedConsoleMessages.push({...message,reason:'Existing Chrome sandbox advisory for the separately originated renderer; actual renderer origin differs from trusted host.'});
     else unexpectedConsoleMessages.push(message);
    }
    if(unexpectedConsoleMessages.length||await target.locator('vite-error-overlay').count())throw Error('trusted host console or framework overlay error: '+JSON.stringify(unexpectedConsoleMessages));
    const screenshot=c.work+'/managed-purge-'+mode+'.png';await target.screenshot({path:screenshot,fullPage:false});
    const inputsBefore=await target.evaluate(()=>window.__managedPurgeInputs);
    if(touch)await activate(dialog.getByRole('button',{name:'닫기',exact:true}));else await target.keyboard.press('Escape');
    await dialog.waitFor({state:'hidden'});
    const inputs=await target.evaluate(()=>window.__managedPurgeInputs);
    if(!touch&&(!inputs.some(e=>e.type==='keydown'&&e.key==='Enter')||!inputs.some(e=>e.type==='keydown'&&e.key==='Escape')))throw Error('actual keyboard interaction evidence missing');
    if(touch&&!inputs.some(e=>e.type==='pointerdown'&&e.pointerType==='touch'))throw Error('actual emulated touch evidence missing');
    const after=(await list('/trash')).find(row=>row.document_id===document);
    if(recordJSON(after)!==recordJSON(before)||recordJSON((await list('/jobs')).map(row=>row.job_id).sort())!==recordJSON(jobs))throw Error('cancel changed Trash revision or created a job');
    await choose('복원');await confirm();
    const restored=await api('GET','/documents/'+document);if(restored.status!==200||restored.wire.data.lifecycle!=='active')throw Error('cancelled document did not restore');
    return {label:'보관한 소스 스냅샷',count:1,hostConsoleMessages:consoleMessages,explainedConsoleMessages,unexpectedConsoleMessages,rendererBoundary,frameworkOverlay:false,pageTitle:await target.title(),sameHashReferenceExcluded:true,publicPlanObserved:true,plan_id:plan.plan_id,screenshot,inputMethod:mode,programmaticFocus:!touch,physicalDevice:false,purgeCommitted:false,unchangedTrashRevision:before.revision,noNewJob:true,inputsBeforeCancel:inputsBefore.length,inputs};
   }catch(e){await target.screenshot({path:c.work+'/managed-purge-'+mode+'-failure.png'}).catch(()=>{});throw e;}
   finally{await Promise.all(pending);target.off('response',observePlan);target.off('console',observeConsole);if(context)await context.close();}
  });
 }
 if(c.phase==='lifecycle'){
  const document=c.fixture.originals.mulgae.document_id;
  const exclusion=async(kind,hidden)=>{
   const target=c.fixture.originals.mulgae.version_id,source=c.fixture.originals.gaori.version_id,selected=kind==='target'?target:source;
   const enumerate=async path=>{const items=[];let cursor=null;do{const result=await api('GET',path+(path.includes('?')?'&':'?')+(await page.evaluate(cursor=>new URLSearchParams({limit:'1',...(cursor?{cursor}:{})}).toString(),cursor)));if(result.status!==200)throw Error('supporting enumeration failed');items.push(...result.wire.data.items);cursor=result.wire.data.next_cursor;}while(cursor);return items;};
   if(hidden){if((await api('GET','/versions/'+selected)).status!==404)throw Error('hidden version exposed');const hits=await enumerate('/search?'+(await page.evaluate(kind=>new URLSearchParams({q:kind==='target'?'failed_run_recovery':'"accepted cancellation"',lifecycle:'all',versions:'all'}).toString(),kind)));if(hits.some(hit=>hit.version_id===selected))throw Error('hidden version searchable');}
   const refs=kind==='referrer'&&hidden?null:await enumerate('/versions/'+source+'/references');
   if(kind==='target'&&hidden){if(!refs.length||refs.some(ref=>ref.resolution!=='unavailable'||ref.target!==null))throw Error('hidden target reference leaked');}
   else if(!hidden&&(!refs.some(ref=>ref.id==='prior-compose'&&ref.resolution==='resolved'&&ref.target.version_id===target&&ref.target.page_path==='recovery/compose.html'&&ref.target.region_id==='M11')))throw Error('exact reference not restored');
   if(kind==='referrer'||!hidden){const backlinks=await enumerate('/versions/'+target+'/backlinks');if(backlinks.some(row=>row.from.version_id===source)===hidden)throw Error('referrer backlink eligibility wrong');}
  };
  const choose=async(name,target=page)=>{const menu=target.locator('.lifecycle-control details').first();if(await menu.getAttribute('open')===null)await menu.locator('summary').click();await target.getByRole('button',{name,exact:true}).click();await target.locator('[data-lifecycle-confirm]').waitFor();};
  const confirm=async(target=page,touch=false)=>{const button=target.locator('[data-lifecycle-confirm]');if(touch)await button.tap();else{await button.focus();await target.keyboard.press('Enter');}await target.getByRole('dialog',{name:'수명주기 영향 확인'}).waitFor({state:'hidden'});};
  for(const size of [[1440,900],[820,1180],[844,390],[390,844]])await check('ui.safe-confirmation.'+size.join('x'),async()=>{
   await page.setViewportSize({width:size[0],height:size[1]});await open(c.fixture.routes.mulgae);await choose('보관');const dialog=page.getByRole('dialog',{name:'수명주기 영향 확인'});if(await dialog.evaluate(e=>e.scrollWidth>e.clientWidth+1||document.activeElement?.hasAttribute('data-lifecycle-confirm')))throw Error('unsafe focus/overflow');await page.keyboard.press('Escape');await dialog.waitFor({state:'hidden'});return {viewport:size,cancelled:true};
  });
  await check('ui.real-archive-trash-restore',async()=>{
   await page.setViewportSize({width:1280,height:900});await open(c.fixture.routes.mulgae);await choose('보관');await confirm();await choose('휴지통으로 이동');await confirm();
   const hidden=await api('GET','/versions/'+c.fixture.originals.mulgae.version_id);if(hidden.status!==404)throw Error('trash target readable');
   await exclusion('target',true);
   await page.goto(c.origin+'/trash');await page.getByRole('heading',{name:'휴지통',exact:true}).waitFor();await choose('복원');await confirm();
   const restored=await api('GET','/documents/'+document);if(restored.wire.data.lifecycle!=='archived')throw Error('prior state not restored');await exclusion('target',false);return {actualHostConfirmations:true};
  });
  await check('ui.real-referrer-trash-restore',async()=>{
   await open(c.fixture.routes.gaori);await choose('휴지통으로 이동');await confirm();await exclusion('referrer',true);
   await page.goto(c.origin+'/trash');await page.getByRole('heading',{name:'휴지통',exact:true}).waitFor();await choose('복원');await confirm();await exclusion('referrer',false);return {actualHostConfirmations:true,supportingPublicEligibilityReads:true};
  });
  await check('ui.touch-trash-restore-no-default-purge',async()=>{
   const context=await page.context().browser().newContext({hasTouch:true,viewport:{width:390,height:844}});let phone,awaited='create phone',lastCompleted=null,target=null;
   const step=async(label,selector,fn)=>{awaited=label;target=selector;const value=await fn();lastCompleted=label;return value;};
   const choosePhone=async name=>{const menu=phone.locator('.lifecycle-control details').first();if(await step('inspect lifecycle menu','.lifecycle-control details',()=>menu.getAttribute('open'))===null)await step('open lifecycle menu','.lifecycle-control details summary',()=>menu.locator('summary').click());await step('choose '+name,'button[name="'+name+'"]',()=>phone.getByRole('button',{name,exact:true}).click());await step('await impact plan','[data-lifecycle-confirm]',()=>phone.locator('[data-lifecycle-confirm]').waitFor());};
   try{await step('install owner session','top same-origin localStorage',()=>context.addInitScript(({origin,token})=>{if(window===top&&location.origin===origin)localStorage.setItem('haeryong.owner-session.v1',token);},{origin:c.origin,token:c.token}));phone=await context.newPage();await step('open original reader',c.fixture.routes.mulgae,()=>phone.goto(c.origin+c.fixture.routes.mulgae));await step('await original Review','#review-state',()=>phone.locator('#review-state').waitFor());
    await choosePhone('휴지통으로 이동');await step('safe default Enter','dialog cancel focus',()=>phone.keyboard.press('Enter'));await step('safe dialog closed','dialog[name="수명주기 영향 확인"]',()=>phone.getByRole('dialog',{name:'수명주기 영향 확인'}).waitFor({state:'hidden'}));if((await api('GET','/documents/'+document)).wire.data.lifecycle!=='archived')throw Error('default Enter mutated');
    await choosePhone('휴지통으로 이동');await step('touch confirm trash','[data-lifecycle-confirm]',()=>confirm(phone,true));await step('open Trash','/trash',()=>phone.goto(c.origin+'/trash'));await step('await Trash heading','heading[name="휴지통"]',()=>phone.getByRole('heading',{name:'휴지통',exact:true}).waitFor());await choosePhone('영구 삭제');const dialog=phone.getByRole('dialog',{name:'수명주기 영향 확인'});if(await dialog.evaluate(e=>e.scrollWidth>e.clientWidth+1||document.activeElement?.hasAttribute('data-lifecycle-confirm')))throw Error('unsafe purge confirmation');await step('cancel purge Escape','dialog[name="수명주기 영향 확인"]',()=>phone.keyboard.press('Escape'));await step('purge dialog closed','dialog[name="수명주기 영향 확인"]',()=>dialog.waitFor({state:'hidden'}));await choosePhone('복원');await step('touch confirm restore','[data-lifecycle-confirm]',()=>confirm(phone,true));return {emulatedTouch:true,physicalDevice:false,purgeCancelled:true,lastCompleted};
   }catch(error){error.phone={awaited,target,lastCompleted,error:String(error),stack:error.stack,expectedDocument:document,url:phone?.url(),state:phone?await phone.evaluate(()=>({title:document.title,documentIdentity:document.querySelector('[data-document-id]')?.getAttribute('data-document-id'),review:document.querySelector('#review-state')?.textContent,dialogs:[...document.querySelectorAll('dialog')].map(e=>({id:e.id,open:e.open,text:e.textContent,busy:e.getAttribute('aria-busy'),confirm:[...e.querySelectorAll('[data-lifecycle-confirm]')].map(b=>({disabled:b.disabled,text:b.textContent}))})),alerts:[...document.querySelectorAll('[role="alert"]')].map(e=>e.textContent),buttons:[...document.querySelectorAll('.lifecycle-control button')].map(e=>({text:e.textContent,disabled:e.disabled}))})).catch(captureError=>({captureError:String(captureError)})):null};throw error;
   }finally{await context.close();}
  });
 }
 if(errors.length)rows.push({name:'page.errors',status:'fail',detail:errors});
 await Promise.all(observations);page.off('response',observe);
 return {phase:c.phase,rows,expected:c.expected,known_note_ids:[...knownNotes],complete:rows.length===c.expected.length&&c.expected.every(name=>rows.some(r=>r.name===name&&r.status==='pass'))};
}
