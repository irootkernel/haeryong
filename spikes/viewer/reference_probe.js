const c=REFERENCE_CONSTANTS,rows=[],traffic=[];let done=false;
addEventListener('error',e=>rows.push({name:'page.error',status:'fail',detail:String(e.message)}));
addEventListener('unhandledrejection',e=>rows.push({name:'page.unhandled',status:'fail',detail:String(e.reason)}));
const report=()=>window.haeryongNativeCall(701101,[{Rows:[...rows,{name:'page.done',status:done?'pass':'pending',detail:{}}]}]).catch(()=>{});
setInterval(()=>void report(),1000);
const wait=async(fn,label)=>{const end=Date.now()+15000;while(Date.now()<end){const value=fn();if(value)return value;await new Promise(r=>setTimeout(r,100));}throw Error('native wait: '+label);};
const record=(name,ok,detail={})=>{rows.push({name,status:ok?'pass':'fail',detail});};
let step='start';
const check=async(name,fn)=>{try{record(name,true,await fn());}catch(e){record(name,false,{error:String(e),step,url:location.pathname+location.search+location.hash,state:history.state,text:document.body.textContent.slice(-1800),traffic:traffic.slice(-12)});}await report();};
const post=MessagePort.prototype.postMessage,seen=new WeakSet();let currentPort;
MessagePort.prototype.postMessage=function(value,...rest){if(!seen.has(this)){seen.add(this);this.addEventListener('message',e=>{if(e.data?.type)traffic.push({direction:'child',message:e.data});});}if(value?.type){traffic.push({direction:'host',message:value});if(value.type==='reference.navigation')currentPort=this;}return post.call(this,value,...rest);};
const commands=[];let announcement=null;
addEventListener('message',e=>{
 if(e.source!==document.querySelector('iframe[title="결과 뷰어"]')?.contentWindow)return;
 if(e.data?.type==='viewer.loaded')announcement=e.data;
 if(e.data?.reference_fixture)commands.push(e.data.value);
});
const command=(action,extra={})=>document.querySelector('iframe[title="결과 뷰어"]').contentWindow.postMessage({reference_fixture_command:true,action,...extra},'*');
const status=async()=>{const r=await window.haeryongNativeCall(701100,[{Operation:'serviceStatus',Payload:{}}]);if(!r.ok)throw Error('native fixture status probe failed');return r.value;};
const button=name=>[...document.querySelectorAll('button')].find(b=>b.textContent===name);
const url=()=>location.pathname+location.search+location.hash;
const loaded=(identity,path)=>wait(()=>{const m=traffic.filter(r=>r.direction==='host'&&r.message.type==='reference.navigation').at(-1)?.message;return announcement?.document_id===identity.document_id&&announcement.version_id===identity.version_id&&announcement.page_path===path&&m?.render_instance_id===announcement.render_instance_id&&m.version_id===identity.version_id&&m.page_path===path;},'actual ready channel '+path);
const go=async route=>{const previous=document.querySelector('iframe[title="결과 뷰어"]');traffic.length=0;announcement=null;history.pushState({haeryongReaderContext:crypto.randomUUID()},'',route);dispatchEvent(new PopStateEvent('popstate'));await wait(()=>{const frame=document.querySelector('iframe[title="결과 뷰어"]');return frame&&frame!==previous;},'new source context frame');await loaded(c.source,c.source_page);};
const source=()=>go(c.source_route);
const sourceState=async previous=>{
 const frame=document.querySelector('iframe[title="결과 뷰어"]'),port=currentPort,start=commands.length;
 command('inspect');const inspection=await wait(()=>commands.slice(start).find(v=>v.command==='inspect'),'fresh source inspection');
 await loaded(c.source,c.source_page);
 const m=traffic.filter(r=>r.direction==='host'&&r.message.type==='reference.navigation').at(-1)?.message,ready=inspection.ready;
 const rect=frame?.getBoundingClientRect();let visible=!!rect&&rect.width>0&&rect.height>0;
 for(let n=frame;n;n=n.parentElement){const style=getComputedStyle(n);if(style.display==='none'||style.visibility!=='visible'||Number(style.opacity)===0)visible=false;}
 if(!frame?.isConnected||frame!==document.querySelector('iframe[title="결과 뷰어"]')||currentPort!==port||!visible||!ready||ready.document_id!==c.source.document_id||ready.version_id!==c.source.version_id||ready.page_path!==c.source_page||ready.render_instance_id!==m?.render_instance_id||!['interactive','complete'].includes(inspection.ready_state)||!inspection.origin_present||!inspection.origin_visible)throw Error('native source frame/channel/content missing, changed or hidden');
 if(previous&&(frame!==previous.frame||port!==previous.port||ready.render_instance_id!==previous.render_instance_id))throw Error('native source frame, channel or render instance replaced');
 return {frame,port,render_instance_id:ready.render_instance_id,page_path:ready.page_path,source_usable:true};
};
const focus=async(identity,path,region,found=true)=>{
 const ack=await wait(()=>{const current=traffic.filter(r=>r.direction==='host'&&r.message.type==='reference.navigation').at(-1)?.message;return traffic.filter(r=>r.direction==='child'&&r.message.type==='region.focused'&&r.message.render_instance_id===current?.render_instance_id&&r.message.version_id===identity.version_id&&r.message.page_path===path&&r.message.payload.region_id===region&&r.message.payload.found===found).at(-1)?.message;},'focus ack '+path+'/'+region);
 const heading=document.querySelector('.source-panel h2')?.textContent||'';
 if(!heading.includes(path)||!heading.includes('영역 '+region))throw Error('native host selection mismatch');
 // A false focused result is an observed native limit, never upgraded to true.
 return {host:heading,ack,native_focus_limit:found&&!ack.payload.focused};
};
const target=async()=>{await loaded(c.target,c.target_page);return focus(c.target,c.target_page,c.region);};
if(c.versions) {
 const v=c.versions;
 const key=async(name,element)=>{element.focus();if(document.activeElement!==element)throw Error('host keyboard focus unavailable: '+name);record('V keyboard '+name,true,{focused:element.id});await report();};
 await check('T5 native copied pinned tuple and S05 keyboard round trip',async()=>{
   await wait(()=>document.querySelector('#reader-copy-link'),'native Reader Copy control');
   await loaded(c.source,c.source_page);await focus(c.source,c.source_page,c.region);
   const sheet=document.querySelector('#reader-context-panel');
   if(sheet instanceof HTMLDialogElement&&sheet.matches(':modal')){button('문맥 닫기').click();await wait(()=>!sheet.matches(':modal'),'context Sheet close before host keyboard');}
   await key('copy',document.querySelector('#reader-copy-link'));
   const field=await wait(()=>document.querySelector('input[aria-label="복사할 링크"]'),'copied link');
   const parsed=new URL(field.value),query=parsed.searchParams;
   if(parsed.origin!==c.public_origin||query.get('page')!==v.special||query.get('page_query')!==v.query||query.get('page_fragment')!==v.fragment||new URLSearchParams(parsed.hash.slice(1)).get('region')!==v.region||!document.body.textContent.includes('local-only'))throw Error('native copy tuple/base');
   await key('versions',document.querySelector('#reader-versions'));
   await wait(()=>document.querySelector('#versions-from'),'actual S05');
   if(document.querySelector('#versions-from').value!==v.old.version_id||document.querySelector('#versions-to').value!==v.new.version_id)throw Error('native pair default');
   await wait(()=>document.querySelector('table[aria-label="파일 차이"] tbody tr'),'real comparison rows');
   button('새 버전 가져오기').click();
   await wait(()=>document.querySelector('[data-testid="base-preview"]')?.textContent.includes(v.new.version_id),'pre-transfer base');
   const control=document.querySelector('[data-testid="import-control"]');
   if(control.dataset.preselected!=='true')throw Error('native ImportControl preselection');
   record('T5 native S05 actual ImportControl exact document/base',true,{document:v.old.document_id,base:v.new.version_id});
   button('시스템 선택기로 선택').click();
   await wait(()=>document.body.textContent.includes('NATIVE_PICKER_UNAVAILABLE'),'actual ReaderMode picker limitation');
   record('T5 native picker attempt',true,{outcome:'blocked',code:'NATIVE_PICKER_UNAVAILABLE',scope:'ReaderMode has no PickerPresenter; no OS chooser or transfer proof claimed'});
   await key('return',document.querySelector('#versions-return'));
   await loaded(c.source,c.source_page);
   await wait(()=>document.activeElement?.id==='reader-versions','S05 return opener focus');
   const current=new URL(location.href);
   if(current.searchParams.get('page')!==v.special||current.searchParams.get('page_query')!==v.query||current.searchParams.get('page_fragment')!==v.fragment||current.hash!==parsed.hash)throw Error('native round-trip tuple');
   await key('latest',document.querySelector('#reader-open-latest'));
   await wait(()=>location.pathname.endsWith('/'+v.new.version_id),'explicit latest');
   if(location.hash)throw Error('latest inherited old region');
   return {copy:true,actual_comparison:true,keyboard:true,round_trip:true,latest:true,full_qualification:false};
 });
 await check('T5 native no-HTML Versions keeps whole-version context',async()=>{
   const id=v.no_html;
   history.pushState({haeryongReaderContext:crypto.randomUUID()},'',`/documents/${id.document_id}/versions`);dispatchEvent(new PopStateEvent('popstate'));
   await wait(()=>document.querySelector('table[aria-label="파일 차이"]')?.textContent.includes('readme.txt'),'no-HTML real inventory');
   await wait(()=>document.querySelector('.source-panel h2')?.textContent.includes('페이지 없음'),'whole-version SourcePanel');
   return {no_html:true,scope:'version',real_import:true};
 });
 await check('T5 native Links backlink exact source and explicit return',async()=>{
   const link=c.links;
   history.pushState({haeryongReaderContext:crypto.randomUUID()},'',link.target_route);dispatchEvent(new PopStateEvent('popstate'));
   await loaded(link.target,link.target_page);
   const toggle=document.querySelector('#reader-context-toggle');if(toggle.getAttribute('aria-expanded')!=='true')toggle.click();
   await wait(()=>document.querySelector('#reader-links-tab'),'Links tab');document.querySelector('#reader-links-tab').click();
   const backlink=await wait(()=>[...document.querySelectorAll('[aria-label="Backlinks"] button')].find(b=>b.textContent==='exact'),'real native backlink');backlink.click();
   await loaded(link.source,link.source_page);
   const sheet=document.querySelector('#reader-context-panel');if(sheet instanceof HTMLDialogElement&&sheet.matches(':modal')){button('문맥 닫기').click();await wait(()=>!sheet.matches(':modal'),'source context close');}
   const back=await wait(()=>button('참조한 페이지로 돌아가기'),'native explicit referring return');back.click();
   await loaded(link.target,link.target_page);
   if(url()!==link.target_route)throw Error('native backlink return lost pinned tuple');
   return {real_backlink:true,exact_source:true,explicit_return:true};
 });
 done=true;await report();
} else {
await wait(()=>window.__haeryongNativeTransport==='HAEYONG_NATIVE_TRANSPORT_BOUND'&&document.querySelector('.service-state[data-state="verified"]'),'real native verified shell');
const reload=JSON.parse(sessionStorage.getItem('reference-reload')||'null');
if(reload){
 rows.push(...reload.rows);
 await check('RF11 actual native '+reload.phase,async()=>{
   const expected=reload.phase==='reload'?reload.url:c.encoded_route;
   await wait(()=>url()===expected,'reloaded/connect exact route');
   if(reload.phase==='reload'&&history.state.haeryongReaderContext!==reload.context)throw Error('reload context lost');
   const path=reload.phase==='reload'?c.target_page:'설명/공백 %2F +.html';
   await loaded(c.target,path);return {url:url(),focus:await focus(c.target,path,reload.phase==='reload'?c.region:'영역/%2F +')};
 });
 if(reload.phase==='reload'){
   sessionStorage.setItem('reference-reload',JSON.stringify({phase:'explicit-connect-return',rows}));
   location.assign('/connect?'+new URLSearchParams({return:c.encoded_route}));
 }else{sessionStorage.removeItem('reference-reload');done=true;await report();}
 return;
}
await loaded(c.source,c.source_page);
await check('RF09-G '+c.mode+' actual marked activation',async()=>{
 const enabled=c.mode==='true'||c.mode==='loss';
 await wait(()=>traffic.some(r=>r.direction==='host'&&r.message.type==='reference.navigation'&&r.message.payload.enabled===enabled),'capability channel');
 const before=(await status()).reference_calls;
 if(c.mode==='loss'){
   let fiber=document.querySelector('.reader');fiber=fiber[Object.keys(fiber).find(k=>k.startsWith('__reactFiber$'))];let client;
   for(;fiber;fiber=fiber.return){const candidate=fiber.memoizedProps?.controller?.client;if(candidate?.resolveReference){client=candidate;break;}}
   if(!client)throw Error('actual native reference client missing');
   const original=client.resolveReference.bind(client);let settled=false;
   client.resolveReference=async request=>{try{return await original(request);}finally{settled=true;}};
   try{
   command('click',{id:'exact'});
   await wait(()=>traffic.some(r=>r.direction==='child'&&r.message.type==='reference.requested'),'pending callback');
   const deadline=Date.now()+15000;while(!(await status()).resolver_started){if(Date.now()>deadline)throw Error('delayed native resolver not started');await new Promise(r=>setTimeout(r,100));}
   document.dispatchEvent(new Event('visibilitychange'));
   await wait(()=>traffic.some(r=>r.direction==='host'&&r.message.type==='reference.navigation'&&!r.message.payload.enabled),'live loss');
   await status();await wait(()=>settled,'actual delayed native client settlement');
   if(url()!==c.source_route)throw Error('settled stale native result navigated');
   const bound=traffic.filter(r=>r.direction==='host'&&r.message.type==='reference.navigation').at(-1).message;
   const count=traffic.filter(r=>r.direction==='child'&&r.message.type==='reference.requested').length;
   command('fabricate',{id:'exact',envelope:bound,sequence:100000});
   await wait(()=>traffic.filter(r=>r.direction==='child'&&r.message.type==='reference.requested').length>count,'fabricated live disabled native request');
   if((await status()).reference_calls!==before+1)throw Error('disabled native fabricated call');
   command('click',{id:'exact'});await loaded(c.source,'fallback.html');
   const after=(await status()).reference_calls;if(after-before!==1)throw Error('native loss resolver count');
   return {seeded:true,live_loss:true,settled_before_fallback:true,fabricated_disabled:true,stale_navigation:false,resolver_calls:after-before};
   }finally{client.resolveReference=original;}
 }
 command('click',{id:'exact'});
 if(enabled){const detail=await target();if(url()!==c.target_route||(await status()).reference_calls-before!==1)throw Error('native exact pin route/count mismatch');return {...detail,resolver_calls:1};}
 await loaded(c.source,'fallback.html');
 const bound=traffic.filter(r=>r.direction==='host'&&r.message.type==='reference.navigation').at(-1).message;
 command('fabricate',{id:'exact',envelope:bound,sequence:100000});
 await wait(()=>traffic.some(r=>r.direction==='child'&&r.message.type==='reference.requested'),'fabricated disabled request');
 if((await status()).reference_calls!==before)throw Error('disabled/fabricated native resolver call');
 return {seeded:true,fallback:true,fabricated_request:true,resolver_calls:0};
});
if(c.mode==='true'){
 await check('RF11 return saved position, Back/Forward and reload context',async()=>{
   await source();command('scroll',{y:450});await wait(()=>commands.some(v=>v.command==='scroll'&&v.scroll_y===450),'source scroll');
   command('click',{id:'exact'});await target();const saved=history.state.haeryongReferenceReturn;
   if(saved.url!==c.source_route||saved.position.scroll_y!==450)throw Error('native saved return tuple');
   button('참조한 페이지로 돌아가기').click();await loaded(c.source,c.source_page);
   if(url()!==c.source_route||history.state.haeryongReaderContext!==saved.context)throw Error('native return context');
   await wait(()=>traffic.some(r=>r.direction==='child'&&r.message.type==='reader.position.applied'&&r.message.payload.applied==='scroll'),'actual restore ack');
   commands.length=0;command('inspect');const actual=await wait(()=>commands.find(v=>v.command==='inspect'),'actual source scroll');if(actual.scroll_y!==450)throw Error('native actual scroll not restored');
   history.back();await target();history.forward();await loaded(c.source,c.source_page);
   return {source:saved.url,position:saved.position,actual_scroll:actual.scroll_y,context:saved.context};
 });
 await check('RF11 nonadjacent ordinary page return and nested replacement',async()=>{
   step='nonadjacent initial target';await source();command('click',{id:'exact'});await target();command('click',{id:'ordinary'});await loaded(c.target,'b.html');
   step='nonadjacent return source';
   button('참조한 페이지로 돌아가기').click();await loaded(c.source,c.source_page);if(url()!==c.source_route)throw Error('native nonadjacent return');
   step='nested initial target';await source();command('click',{id:'exact'});await target();const immediate=url();command('click',{id:'nested'});await loaded(c.nested,'b.html');
   if(history.state.haeryongReferenceReturn.url!==immediate||history.state.haeryongReferenceReturn.haeryongReferenceReturn)throw Error('nested return context');
   step='nested return immediate target';
   button('참조한 페이지로 돌아가기').click();await target();return {nonadjacent:true,nested_source:immediate};
 });
 await check('RF11 actual native Enter on focused authored anchor and encoding',async()=>{
   await source();command('focus',{id:'encoded'});await wait(()=>commands.some(v=>v.command==='focus'&&v.active==='encoded'),'authored keyboard focus');
   document.querySelector('iframe[title="결과 뷰어"]').focus();record('input.request.enter',true);await report();
   await loaded(c.target,'설명/공백 %2F +.html');if(url()!==c.encoded_route)throw Error('native encoded route');
   return focus(c.target,'설명/공백 %2F +.html','영역/%2F +');
 });
 await check('RF11 native legacy whole-version and page-only references',async()=>{
   for(const id of ['legacy','page-only']){
     await source();const before=(await status()).reference_calls;command('click',{id});
     await wait(()=>location.pathname.endsWith('/versions/'+c.target.version_id),'legacy exact version');
     if(id==='legacy')await wait(()=>document.body.textContent.includes('페이지를 선택하거나 원본 파일을 내려받으세요.')&&!document.querySelector('iframe[title="결과 뷰어"]'),'whole-version has no invented default page');
     else await loaded(c.target,'a.html');
     if((await status()).reference_calls-before!==1)throw Error('legacy call count');
   }return {whole_version:true,page_only:true};
 });
 await check('RF10 same-page hash, sibling same ID, no-region and failed anchor',async()=>{
   const route=(p,r)=>`/documents/${c.target.document_id}/versions/${c.target.version_id}?`+new URLSearchParams({page:p})+(r?'#region='+encodeURIComponent(r):'');
   const open=async(p,r)=>{traffic.length=0;announcement=null;history.pushState({haeryongReaderContext:crypto.randomUUID()},'',route(p,r));dispatchEvent(new PopStateEvent('popstate'));await loaded(c.target,p);};
   await open('a.html','retry');const initial=await focus(c.target,'a.html','retry');
   const results=[initial];for(const r of ['data-only','both','runtime-only','duplicate','missing']){location.hash='region='+encodeURIComponent(r);const result=await focus(c.target,'a.html',r,!['duplicate','missing'].includes(r));if(result.ack.render_instance_id!==initial.ack.render_instance_id)throw Error('native hash-only selection replaced frame');results.push(result);}
   await open('b.html','retry');results.push(await focus(c.target,'b.html','retry'));
   const notice=[...document.querySelectorAll('[role="status"]')].map(n=>n.textContent).join('|');
   currentPort.dispatchEvent(new MessageEvent('message',{data:{...initial.ack,sequence:100000,payload:{region_id:'retry',found:false,focused:false}}}));await status();
   if([...document.querySelectorAll('[role="status"]')].map(n=>n.textContent).join('|')!==notice)throw Error('stale native sibling ack changed notice');
   await open('no-region.html',null);if(document.querySelector('.source-panel h2')?.textContent.includes('영역 retry'))throw Error('stale native region');
   return {observed_focus:results,no_region:true,stale_ack_rejected:true};
 });
 for(const id of ['unavailable','gone','denied','runtime-only'])await check('RF11 '+id+' local notice',async()=>{
   await source();const preserved=await sourceState();command('click',{id});const notice=await wait(()=>[...document.querySelectorAll('[role="status"]')].find(n=>n.textContent.includes('참조'))?.textContent,'failure notice');
   if(url()!==c.source_route)throw Error('failure navigated');const after=await sourceState(preserved);return {seeded:['gone','denied'].includes(id),notice,preserved_source:{render_instance_id:after.render_instance_id,page_path:after.page_path,source_usable:after.source_usable}};
 });
 await check('RF10 native overlay close before focus message',async()=>{
   await source();command('click',{id:'exact'});await target();
   document.querySelector('iframe[title="결과 뷰어"]').focus();record('input.request.down',true);await report();
   await wait(()=>commands.some(v=>v.key==='ArrowDown'),'actual native reading input');button('근거 패널').click();
   await wait(()=>document.querySelector('#reader-context-panel')?.matches(':modal'),'native modal overlay');
   const outline=[...document.querySelectorAll('#reader-context-panel button')].find(b=>b.textContent.includes('retry'));if(!outline)throw Error('outline missing');
   for(let i=traffic.length-1;i>=0;i--)if(traffic[i].message.type==='region.focused')traffic.splice(i,1);
   outline.click();await wait(()=>!document.querySelector('#reader-context-panel')?.open,'actual overlay close');const result=await focus(c.target,c.target_page,'retry');
   if(result.ack.payload.found!==true||result.ack.payload.focused!==true)throw Error("native recovery did not acknowledge successful focus");
   return {...result,after_actual_reading_input:true,overlay_closed:true};
 });
 await check('RF11 native modifier/new-window handler keeps href fallback',async()=>{
   await source();const before=(await status()).reference_calls;command('modifier',{id:'exact'});command('new-window',{id:'exact'});
   await wait(()=>commands.some(v=>v.command==='new-window'),'native ordinary popup actions');
   if(url()!==c.source_route||(await status()).reference_calls!==before)throw Error('native modifier/window intercepted reference');
   return {synthetic_authored_activations:true,resolver_calls:0,original_source:true,popup_destinations_checked_by_native_guard:true};
 });
 // Reload/explicit return must also survive popup creation without changing
 // the remembered target; restore the target route before that real reload.
 await source();command('click',{id:'exact'});await target();
 sessionStorage.setItem('reference-reload',JSON.stringify({phase:'reload',url:url(),context:history.state.haeryongReaderContext,rows}));
 location.reload();return;
}
done=true;await report();

}
