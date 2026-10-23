// RF12 observes the real loader and host. No observer is added to sample bytes.
const c=REFERENCE_CONSTANTS,rows=[],traffic=[];let done=false,announcement=null,currentPort;
const report=()=>window.haeryongNativeCall(701101,[{Rows:[...rows,{name:'page.done',status:done?'pass':'pending',detail:{}}]}]).catch(()=>{});
setInterval(()=>void report(),1000);
const wait=async(fn,label)=>{const end=Date.now()+20000;while(Date.now()<end){const v=fn();if(v)return v;await new Promise(r=>setTimeout(r,100));}throw Error('RF12 native wait: '+label);};
const check=async(name,fn)=>{try{rows.push({name,status:'pass',detail:await fn()});}catch(e){rows.push({name,status:'fail',detail:{error:String(e),url:location.pathname+location.search+location.hash,traffic:traffic.slice(-12)}});}await report();};
addEventListener('error',e=>rows.push({name:'page.error',status:'fail',detail:String(e.message)}));
addEventListener('unhandledrejection',e=>rows.push({name:'page.unhandled',status:'fail',detail:String(e.reason)}));
const post=MessagePort.prototype.postMessage,seen=new WeakSet();
MessagePort.prototype.postMessage=function(value,...rest){if(!seen.has(this)){seen.add(this);this.addEventListener('message',e=>{if(e.data?.type)traffic.push({direction:'child',message:e.data});});}if(value?.type){traffic.push({direction:'host',message:value});if(value.type==='reference.navigation')currentPort=this;}return post.call(this,value,...rest);};
addEventListener('message',e=>{if(e.source===document.querySelector('iframe[title="결과 뷰어"]')?.contentWindow&&e.data?.type==='viewer.loaded')announcement=e.data;});
const url=()=>location.pathname+location.search+location.hash;
const loaded=async(identity,path)=>wait(()=>{const m=traffic.filter(r=>r.direction==='host'&&r.message.type==='reference.navigation').at(-1)?.message;return announcement?.document_id===identity.document_id&&announcement.version_id===identity.version_id&&announcement.page_path===path&&m?.render_instance_id===announcement.render_instance_id&&m.version_id===identity.version_id&&m.page_path===path?{...announcement}:null;},'current ready '+path);
const focus=async(identity,path,region)=>{
 await loaded(identity,path);
 const ack=await wait(()=>traffic.filter(r=>r.direction==='child'&&r.message.type==='region.focused'&&r.message.render_instance_id===announcement.render_instance_id&&r.message.version_id===identity.version_id&&r.message.page_path===path&&r.message.payload.region_id===region).at(-1)?.message,'region focus '+region);
 const heading=document.querySelector('.source-panel h2')?.textContent||'';
 if(!heading.includes(path)||!heading.includes('영역 '+region)||ack.payload.found!==true)throw Error('RF12 host selection/anchor mismatch');
 return {host:heading,ack,native_focus_limit:ack.payload.focused!==true};
};
const coordinates=p=>({scroll_x:p?.scroll_x,scroll_y:p?.scroll_y});
const samePosition=(a,b)=>Number.isFinite(a?.scroll_x)&&Number.isFinite(a?.scroll_y)&&a.scroll_x===b?.scroll_x&&a.scroll_y===b?.scroll_y;
const snapshot=(render,after=0)=>traffic.slice(after).filter(r=>r.direction==='child'&&r.message.type==='reader.position.snapshot'&&r.message.render_instance_id===render).at(-1)?.message;
const inputPosted=async name=>{const end=Date.now()+20000;for(;;){const status=await window.haeryongNativeCall(701100,[{Operation:'serviceStatus',Payload:{}}]);if(status.value?.native_inputs?.includes(name))return;if(Date.now()>end)throw Error('owned native input not posted: '+name);await new Promise(r=>setTimeout(r,100));}};
let positionProof,sourceFresh;
const button=name=>[...document.querySelectorAll('button')].find(b=>b.textContent===name);
const reload=JSON.parse(sessionStorage.getItem('rf12-reload')||'null');
if(reload){
 rows.push(...reload.rows);sessionStorage.removeItem('rf12-reload');
 await check('RF12 actual native reload retains pinned old target',async()=>{if(url()!==c.target_route)throw Error('reload lost exact target');return {actual_target:await loaded(c.target,c.target_page),focus:await focus(c.target,c.target_page,c.region)};});
 await check('RF12 native return, Back and Forward retain exact tuples and source context',async()=>{
   const targetContext=history.state?.haeryongReaderContext;
   button('참조한 페이지로 돌아가기')?.click();await wait(()=>url()===c.source_route,'return source route');
   const source=await loaded(c.source,c.source_page);
   if(history.state?.haeryongReaderContext!==reload.source_context||!history.state?.haeryongReaderPosition)throw Error('saved source context/position lost');
   const saved=reload.position_proof?.saved;
   if(!samePosition(saved,history.state.haeryongReaderPosition.position))throw Error('saved source coordinates changed');
   const apply=await wait(()=>traffic.find(r=>r.direction==='host'&&r.message.type==='reader.position.apply'&&r.message.render_instance_id===source.render_instance_id)?.message,'current source restore request');
   if(!samePosition(apply.payload.position,saved)||typeof apply.payload.correlation_id!=='string'||!apply.payload.correlation_id)throw Error('restore request lost saved coordinates/correlation');
   const ack=await wait(()=>traffic.find(r=>r.direction==='child'&&r.message.type==='reader.position.applied'&&r.message.render_instance_id===source.render_instance_id&&r.message.payload.correlation_id===apply.payload.correlation_id)?.message,'correlated source restore ack');
   if(ack.payload.applied!=='scroll')throw Error('source position was not applied as scroll');
   const afterAck=traffic.findIndex(r=>r.message===ack)+1;
   const actual=await wait(()=>snapshot(source.render_instance_id,afterAck),'actual source position after restore');
   if(!samePosition(actual.payload.position,saved))throw Error('actual source coordinates differ from saved departure');
   history.back();await wait(()=>url()===c.target_route,'Back target');await loaded(c.target,c.target_page);
   if(history.state?.haeryongReaderContext!==targetContext)throw Error('Back target context lost');
   history.forward();await wait(()=>url()===c.source_route,'Forward source');await loaded(c.source,c.source_page);
   return {actual_source:source,source_context:reload.source_context,back_forward:true,saved_position_restored:true,position_proof:reload.position_proof,apply,ack,actual_position:actual.payload.position};
 });
 done=true;await report();return;
}
await check('RF12 native source recovery after real input asserts focus success',async()=>{
 const initialSource=await loaded(c.source,c.source_page);sourceFresh=coordinates(initialSource.location);const initial=await focus(c.source,c.source_page,c.source_region);
 document.querySelector('iframe[title="결과 뷰어"]').focus();rows.push({name:'input.request.down',status:'pass',detail:{}});await report();
 await inputPosted('input.request.down');
 button('근거 패널')?.click();await wait(()=>document.querySelector('#reader-context-panel')?.matches(':modal'),'actual overlay');
 const outline=[...document.querySelectorAll('#reader-context-panel button')].find(b=>b.textContent.includes(c.source_region));if(!outline)throw Error('source outline missing');
 for(let i=traffic.length-1;i>=0;i--)if(traffic[i].message.type==='region.focused')traffic.splice(i,1);
 outline.click();await wait(()=>!document.querySelector('#reader-context-panel')?.open,'overlay closed');
 const recovered=await focus(c.source,c.source_page,c.source_region);
 if(recovered.ack.payload.found!==true||recovered.ack.payload.focused!==true)throw Error('RF12 source recovery failed focus');
 return {initial,recovered,actual_source:announcement};
});
const sourceContext=history.state?.haeryongReaderContext;
await check('RF12 actual native keyboard click opens old Mulgae M11 despite synthetic successor',async()=>{
 // G12 contains two citation anchors before its marked prior-compose link.
 // The complete original HTML is unchanged; real PID-targeted Option+Tab x3 + Enter
 // activates that authored link, and the actual loader request is the oracle.
 const sourceRender=announcement.render_instance_id;
 const fresh=sourceFresh;
 const focusIndex=traffic.findLastIndex(r=>r.direction==='child'&&r.message.type==='region.focused'&&r.message.render_instance_id===sourceRender);
 const focused=await wait(()=>snapshot(sourceRender,focusIndex+1),'region-only source coordinates');
 const baseline=coordinates(focused.payload.position);
 const beforeScroll=traffic.length;
 rows.push({name:'input.request.scroll',status:'pass',detail:{}});await report();await inputPosted('input.request.scroll');
 await wait(()=>{const m=snapshot(sourceRender,beforeScroll);return m&&!samePosition(m.payload.position,baseline)?m:null;},'real native discriminating scroll');
 rows.push({name:'input.request.tabs-enter',status:'pass',detail:{}});await report();
 await wait(()=>traffic.some(r=>r.direction==='child'&&r.message.type==='reference.requested'&&r.message.payload.reference_id==='prior-compose'),'actual marked activation');
 await wait(()=>url()===c.target_route,'exact pinned target route');
 if(c.latest.version_id===c.target.version_id)throw Error('successor does not discriminate');
 const saved=history.state?.haeryongReferenceReturn;
 const activation=traffic.findIndex(r=>r.direction==='child'&&r.message.type==='reference.requested'&&r.message.render_instance_id===sourceRender&&r.message.payload.reference_id==='prior-compose');
 const departure=snapshot(sourceRender,activation+1);
 if(saved?.context!==sourceContext||!departure||!samePosition(saved.position,departure.payload.position))throw Error('saved source position differs from actual departure snapshot');
 if(!Number.isFinite(fresh?.scroll_x)||!Number.isFinite(fresh?.scroll_y)||samePosition(saved.position,fresh)||samePosition(saved.position,baseline))throw Error('saved position does not discriminate fresh load and region-only focus');
 positionProof={fresh:coordinates(fresh),region_focus:baseline,saved:coordinates(saved.position),departure};
 return {expected_source:c.expected_source,expected_target:c.expected_target,actual_target:await loaded(c.target,c.target_page),focus:await focus(c.target,c.target_page,c.region),newer_target_not_selected:true,position_proof:positionProof};
});
sessionStorage.setItem('rf12-reload',JSON.stringify({rows,source_context:sourceContext,position_proof:positionProof}));location.reload();
