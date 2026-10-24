async page => {
  const c = REFERENCE_CONSTANTS, cases = [], errors = [], requests = [], consoleMessages=[];
  page.setDefaultTimeout(12000);
  page.on('pageerror', e => errors.push(String(e)));
  page.on('console',m=>{if(['error','warning'].includes(m.type()))consoleMessages.push({level:m.type(),text:m.text()});});
  page.on('request', r => { if (r.url().includes('/references')) requests.push({url:r.url(),method:r.method()}); });
  const observer = () => {
    window.__rf = [];
    addEventListener('message',e=>{if(e.source===document.querySelector('iframe[title="결과 뷰어"]')?.contentWindow&&e.data?.type==='viewer.loaded')window.__rfAnnouncement=e.data;});
    const original = MessagePort.prototype.postMessage, seen = new WeakSet();
    MessagePort.prototype.postMessage = function(value, ...rest) {
      if (!seen.has(this)) { seen.add(this); this.addEventListener('message', e => { if(e.data?.type) window.__rf.push({direction:'child',message:e.data}); }); }
      if (value?.type) {window.__rf.push({direction:'host',message:value});if(value.type==='reference.navigation')window.__rfCurrentPort=this;}
      return original.call(this,value,...rest);
    };
  };
  await page.addInitScript(observer);
  await page.evaluate(token => localStorage.setItem('haeryong.owner-session.v1',token),c.token);
  const check = async (name, fn) => { try { cases.push({name,ok:true,detail:await fn()}); } catch(e) { cases.push({name,ok:false,error:String(e),observed:await page.evaluate(()=>({url:location.pathname+location.search+location.hash,text:document.body.textContent.slice(-1800),traffic:(window.__rf||[]).slice(-8)}))}); } };
  const frame = () => page.frameLocator('iframe[title="결과 뷰어"]');
  const current = () => page.evaluate(() => location.pathname+location.search+location.hash);
  const resolveCount = () => requests.filter(r => r.url.includes('/references/resolve?')).length;
  const go = async url => { await page.goto(c.base+'/health/live');await page.goto(c.base+url); await page.locator('.service-state[data-state="verified"]').waitFor(); await frame().locator('body').waitFor(); };
  const loaded = async (identity,path) => {
    const encoded=encodeURIComponent(path).replace(/%2B/g,'+').replace(/~/g,'%7E');
    await frame().locator('script[data-haeryong-version="'+identity.version_id+'"][data-haeryong-page="'+encoded+'"]').waitFor({state:'attached'});
    await page.waitForFunction(({v,p}) => {const m=window.__rf.filter(r=>r.direction==='host'&&r.message.type==='reference.navigation').at(-1)?.message;return m?.version_id===v&&m.page_path===p;},{v:identity.version_id,p:path});
    const actual=await frame().locator('script[data-haeryong-page]').evaluate(s=>({...s.dataset}));
    if(actual.haeryongDocument!==identity.document_id||actual.haeryongVersion!==identity.version_id||decodeURIComponent(actual.haeryongPage)!==path)throw Error('actual frame identity mismatch');
    return actual;
  };
  const selection = async (identity,path,region,found=true) => {
    await page.waitForFunction(({v,p,r,found})=>{const current=window.__rf.filter(row=>row.direction==='host'&&row.message.type==='reference.navigation').at(-1)?.message;return window.__rf.some(row=>{const m=row.message;return row.direction==='child'&&m.type==='region.focused'&&m.render_instance_id===current?.render_instance_id&&m.version_id===v&&m.page_path===p&&m.payload.region_id===r&&m.payload.found===found;});},{v:identity.version_id,p:path,r:region,found});
    const header=await page.locator('.source-panel h2').textContent();
    if(!header.includes(path)||!header.includes('영역 '+region))throw Error('host selection mismatch: '+header);
    const ack=await page.evaluate(({v,p,r})=>window.__rf.filter(row=>row.direction==='child'&&row.message.type==='region.focused'&&row.message.version_id===v&&row.message.page_path===p&&row.message.payload.region_id===r).at(-1).message,{v:identity.version_id,p:path,r:region});
    if(found&&!ack.payload.focused)throw Error('frame did not acknowledge focus');
    return {host:header,ack,active:await frame().locator('body').evaluate(()=>({id:document.activeElement?.id,region:document.activeElement?.getAttribute('data-haeryong-region')}))};
  };
  const source = async () => { await go(c.source_route); await loaded(c.source,c.source_page); };
  const sourceState = async (capture, previous=null) => {
    await loaded(c.source,c.source_page);
    const binding=await page.evaluate(({capture,c})=>{
      const f=document.querySelector('iframe[title="결과 뷰어"]'),m=window.__rf.filter(r=>r.direction==='host'&&r.message.type==='reference.navigation').at(-1)?.message,a=window.__rfAnnouncement;
      if(!f?.isConnected||!f.checkVisibility({checkOpacity:true,checkVisibilityCSS:true})||!m||m.document_id!==c.source.document_id||m.version_id!==c.source.version_id||m.page_path!==c.source_page||a?.render_instance_id!==m.render_instance_id||a.document_id!==m.document_id||a.version_id!==m.version_id||a.page_path!==m.page_path)throw Error('source frame is not visible on its ready source channel');
      if(capture){window.__rfSourceFrame=f;window.__rfSourcePort=window.__rfCurrentPort;}
      else if(f!==window.__rfSourceFrame||window.__rfCurrentPort!==window.__rfSourcePort)throw Error('source frame or channel was replaced');
      return {render_instance_id:m.render_instance_id,page_path:m.page_path};
    },{capture,c});
    if(previous&&binding.render_instance_id!==previous.render_instance_id)throw Error('source render instance changed');
    if(!await frame().locator('#origin').isVisible())throw Error('source content missing or hidden');
    return {...binding,source_usable:true};
  };
  const anchor = id => frame().locator('a[data-haeryong-reference="'+id+'"]');
  const target = async () => { await loaded(c.target,c.target_page); return selection(c.target,c.target_page,c.region); };

  await check((c.synthetic?'RF11':'RF12')+' exact cross-project pinned click, host selection and actual frame focus',async()=>{
    await source();const before=resolveCount();await anchor(c.reference).click();const detail=await target();
    if(resolveCount()!==before+1||(await current())!==c.target_route)throw Error('resolver count or exact route mismatch');
    if(c.latest&&c.target.version_id===c.latest.version_id)throw Error('fixture lacks newer target');
    await page.screenshot({path:c.work+'/desktop.png'});return {...detail,resolver_calls:1,actual_route:await current(),actual_source:c.source,actual_target:await loaded(c.target,c.target_page),expected_source:c.expected_source||null,expected_target:c.expected_target||null,newer_target_not_selected:!!c.latest};
  });
  await check((c.synthetic?'RF11':'RF12')+' return, Back/Forward, reload and saved source position',async()=>{
    await source();await frame().locator('body').evaluate(()=>scrollTo(0,450));const saved=await frame().locator('body').evaluate(()=>scrollY);
    await anchor(c.reference).evaluate(el=>el.click());await target();await page.reload();await target();
    await page.getByRole('button',{name:'참조한 페이지로 돌아가기',exact:true}).click();await loaded(c.source,c.source_page);
    if((await current())!==c.source_route)throw Error('source route not restored');
    const state=await page.evaluate(()=>history.state);
    if(state.haeryongReaderPosition.position.scroll_y!==saved)throw Error('saved scroll lost');
    await page.waitForFunction(()=>window.__rf.some(r=>r.direction==='child'&&r.message.type==='reader.position.applied'&&r.message.payload.applied==='scroll'));
    const actual=await frame().locator('body').evaluate(()=>scrollY);if(Math.abs(actual-saved)>2)throw Error('actual source scroll not restored');
    await page.goBack();await target();await page.goForward();await loaded(c.source,c.source_page);
    return {source:await current(),saved_scroll:saved,actual_scroll:actual,restored_context:state.haeryongReaderContext};
  });
  if(c.knowledge_gate) {
    await check('T6 saved reader question: search, original controls and cited facts (agent-only)',async()=>{
      const observations=[];
      for(const [name,identity,q,path] of [['mulgae',c.target,'failed_run_recovery','recovery/inventory.html'],['gaori',c.source,'await_run',c.source_page],['gaori',c.source,'"replacement start"',c.source_page],['mulgae',c.target,'reconciliation_state',c.target_page]]) {
        // Include all versions because the fixture deliberately publishes a successor.
        await page.goto(c.base+'/library?q='+encodeURIComponent(q)+'&versions=all');
        const hit=page.getByTestId('search-hit').filter({hasText:path}).first();await hit.waitFor();
        await hit.getByRole('link',{name:'일치하는 페이지 열기',exact:true}).click();await loaded(identity,path);
        const controls=path.includes('inventory') ? [] : name==='mulgae' ? [
          ['#compose-mapping','omit','Omitting the failed selected optional role'],
          ['#compose-mapping','replace','CompositeRoleAlreadySatisfied'],
          ['#compose-publication','cli','exact returned composite run_id'],
          ['#compose-publication','mcp','retryable: false'],
          ['#compose-publication','none','CompositeRoleAlreadySatisfied'],
          ['#compose-source','root-missing','diagnostics cannot replace'],
          ['#compose-source','ancestor-missing','CompositeLineageMismatch']
        ] : [
          ['#lifecycle-observation','await','waiter only'],
          ['#lifecycle-observation','cancel','does not guarantee killed'],
          ['#lifecycle-session','lost','identical-looking ID'],
          ['#lifecycle-observation','disconnect','reconciliation of the command and final artifacts']
        ];
        const output=frame().locator('[data-output]').first();
        for(const [selector,value,expected] of controls){
          await frame().locator(selector).selectOption(value);
          const text=await output.textContent();if(!text.includes(expected))throw Error('reader prediction missing '+expected);
          observations.push({project:name,control:selector,value,observed:text});
        }
        const claims=await frame().locator('.claim-text').allTextContents();
        observations.push({project:name,page:path,claims,evidence:await frame().locator('.sources .identity').allTextContents()});
      }
      // Preserve actual source text and statement scope for the agent's answer.
      if(!observations.some(r=>r.project==='mulgae'&&r.claims?.some(t=>t.includes('accepted root role'))))throw Error('accepted-role fact absent');
      await page.screenshot({path:c.work+'/knowledge-controls.png'});
      return {reader:'agent-only; familiar with corpus',observations,regeneration:false};
    });
    await check('T6 real-source Evidence action and exact Reader reopen',async()=>{
      await go(c.target_route);await loaded(c.target,c.target_page);await selection(c.target,c.target_page,c.region);
      const open=async()=>{
        await page.getByRole('button',{name:'근거 패널',exact:true}).click();
        const panel=page.locator('section[aria-label="근거 및 출처 패널"]');await panel.waitFor();
        await panel.getByRole('button',{name:/M-S09 · reference_only/}).first().click();
        return panel.locator('.evidence-region .evidence-detail').first();
      };
      let detail=await open();
      const observed=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().includes('/source-evidence-verifications?'));
      await detail.getByRole('button',{name:'출처 근거 확인',exact:true}).click();
      const response=await observed;const checkResult=(await response.json()).data;
      if(checkResult.outcome!=='captured'||!checkResult.target_set_id)throw Error('real evidence capture failed');
      await detail.getByText('확인 결과: 캡처됨',{exact:false}).waitFor();
      await detail.getByLabel('작성자 선언 다이제스트와 비교',{exact:true}).check();
      const matching=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().includes('/source-evidence-verifications?'));
      await detail.getByRole('button',{name:'출처 근거 확인',exact:true}).click();
      const match=(await (await matching).json()).data;if(match.outcome!=='matched')throw Error('real expected bytes did not match');
      await page.locator('#reader-copy-link').click();
      const field=page.getByRole('textbox',{name:'복사할 링크',exact:true});await field.waitFor();const copied=await field.inputValue();
      if(copied!==c.base+c.target_route||copied.includes(c.token))throw Error('saved explanation copy lost exact tuple');
      await page.goto(c.base+'/health/live');await page.goto(copied);await loaded(c.target,c.target_page);await selection(c.target,c.target_page,c.region);detail=await open();
      await detail.getByRole('button',{name:'검증 이력 보기/숨기기',exact:true}).click();
      await detail.locator('li').filter({hasText:'일치'}).first().waitFor();
      if(!(await detail.textContent()).includes('reference_only(작성자 선언)'))throw Error('original provenance label lost');
      await page.screenshot({path:c.work+'/knowledge-evidence.png'});
      return {capture:checkResult,match,actual_target:await loaded(c.target,c.target_page),copied_url:copied,history_reopened:true,authored_declaration:'reference_only'};
    });
    await check('T6 browser identity, nonblank, overlays and console',async()=>{
      if(!await page.title()||!await frame().locator('h1').textContent())throw Error('blank/wrong page');
      if(await page.locator('vite-error-overlay').count()||errors.length)throw Error('runtime/framework error');
      const unexpected=consoleMessages.filter(m=>!(m.level==='warning'&&m.text.startsWith('An iframe which has both allow-scripts and allow-same-origin')));if(unexpected.length)throw Error('unexpected console error '+JSON.stringify(unexpected));
      return {title:await page.title(),url:await current(),page_errors:errors,console:consoleMessages};
    });
  }
  if(!c.synthetic) return {cases,errors,requests,consoleMessages,scope:'M11 corpus opt-in; synthetic matrix belongs to default run'};
  await check('RF11 nonadjacent child history return and nested immediate replacement',async()=>{
    await source();await anchor('exact').click();await target();
    await frame().locator('#ordinary').click();await loaded(c.target,'b.html');
    await page.getByRole('button',{name:'참조한 페이지로 돌아가기',exact:true}).click();await loaded(c.source,c.source_page);
    if((await current())!==c.source_route)throw Error('nonadjacent return missed A');
    await source();await anchor('exact').click();await target();const immediate=await current();
    await frame().locator('#nested').click();await loaded(c.nested,'b.html');
    const state=await page.evaluate(()=>history.state.haeryongReferenceReturn);if(state.url!==immediate||state.haeryongReferenceReturn)throw Error('nested return chain');
    await page.getByRole('button',{name:'참조한 페이지로 돌아가기',exact:true}).click();await target();return {nonadjacent:true,nested_source:state.url};
  });
  await check('RF11 keyboard Enter and exact percent/plus/space/query/fragment encoding',async()=>{
    await source();await anchor('encoded').focus();await page.keyboard.press('Enter');await loaded(c.target,'설명/공백 %2F +.html');
    const detail=await selection(c.target,'설명/공백 %2F +.html','영역/%2F +');
    if((await current())!==c.encoded_route)throw Error('encoded route changed: '+await current());
    await page.reload();await loaded(c.target,'설명/공백 %2F +.html');await selection(c.target,'설명/공백 %2F +.html','영역/%2F +');return detail;
  });
  await check('RF11 modifier/new-window activation keeps ordinary fallback with zero resolver calls',async()=>{
    const activations=[];
    for(const activation of ['modifier','target-blank']){
    await source();const before=resolveCount();if(activation==='target-blank')await anchor('exact').evaluate(el=>el.target='_blank');
    const opened=page.context().waitForEvent('page');await anchor('exact').click(activation==='modifier'?{modifiers:['Meta']}:{});const popup=await opened;
    try{
      await popup.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'Ordinary fallback',exact:true}).waitFor();
      if(resolveCount()!==before||(await current())!==c.source_route)throw Error('modifier reference intercepted');
      activations.push({activation,resolver_calls:0,original_source:true,safe_fallback_page:true});
    }finally{await popup.close();}
    }return activations;
  });
  await check('RF11 legacy whole-version and page-only references',async()=>{
    const opened=[];
    for(const id of ['legacy','page-only']){
      await source();const before=resolveCount();await anchor(id).click();await page.waitForFunction(v=>location.pathname.endsWith('/versions/'+v),c.target.version_id);
      if(id==='legacy'){
        await page.getByText('페이지를 선택하거나 원본 파일을 내려받으세요.',{exact:true}).waitFor();
        if(resolveCount()!==before+1||await page.locator('iframe[title="결과 뷰어"]').count())throw Error('whole-version invented a page');
        opened.push({id,version:c.target.version_id,page:null});
      }else{
        await loaded(c.target,'a.html');if(resolveCount()!==before+1)throw Error('page-only call count');opened.push({id,version:c.target.version_id,page:'a.html'});
      }
    }return opened;
  });
  await check('RF10 initial/hash-only, sibling retry, data/both, no-region and failed anchors',async()=>{
    const route=(p,r)=>page.evaluate(({c,p,r})=>`/documents/${c.target.document_id}/versions/${c.target.version_id}?`+new URLSearchParams({page:p})+(r?'#region='+encodeURIComponent(r):''),{c,p,r});
    await go(await route('a.html','retry'));await loaded(c.target,'a.html');const first=await selection(c.target,'a.html','retry');
    for(const r of ['data-only','both','runtime-only','duplicate','missing']){
      await page.evaluate(r=>{location.hash='region='+encodeURIComponent(r);},r);
      const changed=await selection(c.target,'a.html',r,!['duplicate','missing'].includes(r));
      if(changed.ack.render_instance_id!==first.ack.render_instance_id)throw Error('hash-only selection replaced the frame');
    }
    await go(await route('b.html','retry'));await loaded(c.target,'b.html');await selection(c.target,'b.html','retry');
    const beforeNotice=await page.getByRole('status').allTextContents();
    await page.evaluate(old=>window.__rfCurrentPort.dispatchEvent(new MessageEvent('message',{data:{...old,sequence:100000,payload:{region_id:'retry',found:false,focused:false}}})),first.ack);
    if(!(await page.locator('.source-panel h2').textContent()).includes('b.html'))throw Error('stale sibling ack retagged host');
    if(JSON.stringify(await page.getByRole('status').allTextContents())!==JSON.stringify(beforeNotice))throw Error('stale sibling ack replaced the notice');
    await go(await route('no-region.html',null));await loaded(c.target,'no-region.html');
    if((await page.locator('.source-panel h2').textContent()).includes('영역 retry'))throw Error('old region retained');
    return {initial:true,hash_only:true,sibling_same_id:true,stale_ack_rejected:true,no_region:true,duplicate_and_missing_found_false:true};
  });
  for(const mode of ['absent','false','true']) await check('RF09-G seeded capability '+mode+' fallback and exact resolver count',async()=>{
    const caps=JSON.parse(JSON.stringify(c.capabilities));if(mode==='absent')delete caps.features.references;else caps.features.references=mode==='true';
    await page.route('**/api/v1/capabilities',r=>r.fulfill({json:{data:caps}}));
    try{
      await source();await page.waitForFunction(enabled=>window.__rf.some(r=>r.direction==='host'&&r.message.type==='reference.navigation'&&r.message.payload.enabled===enabled),mode==='true');
      const before=resolveCount();await anchor('exact').click();
      if(mode==='true'){await target();if(resolveCount()!==before+1)throw Error('enabled count');}
      else{await loaded(c.source,'fallback.html');if(resolveCount()!==before)throw Error('disabled resolver call');}
      return {seeded:true,resolver_calls:resolveCount()-before,fallback:mode!=='true'};
    }finally{await page.unroute('**/api/v1/capabilities');}
  });
  await check('RF09-G live true→false rejects delayed result and fabricated callback',async()=>{
    await source();let release,started;const waiting=new Promise(r=>started=r);const pending=new Promise(r=>release=r);
    await page.evaluate(()=>{
      let fiber=document.querySelector('.reader');fiber=fiber[Object.keys(fiber).find(k=>k.startsWith('__reactFiber$'))];let client;
      for(;fiber;fiber=fiber.return){const candidate=fiber.memoizedProps?.controller?.client;if(candidate?.resolveReference){client=candidate;break;}}
      if(!client)throw Error('actual mounted reference client missing');
      const original=client.resolveReference.bind(client);window.__rfResolutionSettled=false;
      client.resolveReference=async request=>{try{return await original(request);}finally{window.__rfResolutionSettled=true;}};
      window.__rfRestoreResolver=()=>{client.resolveReference=original;};
    });
    const caps=JSON.parse(JSON.stringify(c.capabilities));caps.features.references=false;
    await page.route('**/api/v1/versions/*/references/resolve?**',async r=>{const response=await r.fetch();started();await pending;try{await r.fulfill({response});}catch{}});
    try{
      await anchor('exact').click();await Promise.race([waiting,page.waitForTimeout(12000).then(()=>{throw Error('delayed resolver did not start');})]);
      await page.route('**/api/v1/capabilities',r=>r.fulfill({json:{data:caps}}));
      await page.evaluate(()=>document.dispatchEvent(new Event('visibilitychange')));
      await page.waitForFunction(()=>window.__rf.some(r=>r.direction==='host'&&r.message.type==='reference.navigation'&&r.message.payload.enabled===false));
      const before=resolveCount();release();await page.waitForFunction(()=>window.__rfResolutionSettled);
      if((await current())!==c.source_route)throw Error('settled stale callback navigated before fallback');
      // A correctly bound but fabricated child request is injected on the live
      // disabled port; the ordinary host callback must still remain absent.
      const bound=await page.evaluate(()=>window.__rf.filter(r=>r.direction==='host'&&r.message.type==='reference.navigation').at(-1).message);
      await page.locator('iframe[title="결과 뷰어"]').evaluate((f,bound)=>f.contentWindow.postMessage({reference_fixture_command:true,action:'fabricate',id:'exact',envelope:bound,sequence:100000},'*'),bound);
      await page.waitForFunction(()=>window.__rf.some(r=>r.direction==='child'&&r.message.type==='reference.requested'&&r.message.sequence===100000));
      if(resolveCount()!==before||(await current())!==c.source_route)throw Error('fabricated disabled request escaped gate');
      await anchor('exact').click();await loaded(c.source,'fallback.html');
      if(resolveCount()!==before)throw Error('disabled ordinary fallback called resolver');
      return {seeded:true,delayed_real_resolver:true,settled_before_fallback:true,disabled_new_calls:0,stale_navigation:false};
    }finally{release();await page.evaluate(()=>window.__rfRestoreResolver?.());await page.unroute('**/api/v1/capabilities');await page.unroute('**/api/v1/versions/*/references/resolve?**');}
  });
  for(const id of ['unavailable','gone','denied','runtime-only']) await check('RF11 '+id+' notice leaves source readable',async()=>{
    const seeded=['gone','denied'].includes(id);
    if(seeded)await page.route('**/api/v1/versions/*/references/resolve?**',r=>r.fulfill({status:id==='gone'?410:403,json:{error:{code:id==='gone'?'TARGET_GONE':'FORBIDDEN',message:'labelled fixture',retryable:false,details:{}}}}));
    try{
      await source();const preserved=await sourceState(true);const before=resolveCount();await anchor(id).click();await page.getByRole('status').filter({hasText:/参照|참조/}).waitFor();
      if((await current())!==c.source_route||resolveCount()!==before+1)throw Error('failure navigated or never resolved');
      return {seeded,notice:await page.getByRole('status').filter({hasText:'참조'}).textContent(),preserved_source:await sourceState(false,preserved)};
    }finally{if(seeded)await page.unroute('**/api/v1/versions/*/references/resolve?**');}
  });
  await check('RF11 phone touch, overlay closes before real frame focus',async()=>{
    const desktop=page, context=await page.context().browser().newContext({viewport:{width:390,height:844},hasTouch:true,isMobile:true});
    await context.addInitScript(observer);page=await context.newPage();
    try{
    await page.goto(c.base+'/health/live');await page.evaluate(token=>localStorage.setItem('haeryong.owner-session.v1',token),c.token);
    await go(c.target_route);await target();
    await page.getByRole('button',{name:'근거 패널',exact:true}).click();const dialog=page.locator('#reader-context-panel');await dialog.waitFor({state:'visible'});
    const outline=dialog.getByRole('button',{name:/retry/}).first();const box=await outline.boundingBox();if(!box||box.height<44)throw Error('touch target below 44px');
    await page.evaluate(()=>{window.__rf=window.__rf.filter(r=>r.message.type!=='region.focused');});
    await page.touchscreen.tap(box.x+box.width/2,box.y+box.height/2);await dialog.waitFor({state:'hidden'});
    if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('phone overflow');
    await page.screenshot({path:c.work+'/phone.png'});return {viewport:'390x844',actual_touch:true,overlay_closed:true,outline_height:box.height,focus:await selection(c.target,c.target_page,'retry')};
    }finally{await context.close();page=desktop;}
  });
  await check('RF11 explicit safe connect return and reload; automatic return handoff',async()=>{
    const login=await page.evaluate(c=>c.base+'/connect?'+new URLSearchParams({return:c.encoded_route}),c);
    await page.goto(login);await loaded(c.target,'설명/공백 %2F +.html');
    if((await current())!==c.encoded_route)throw Error('explicit connect return lost identity');
    await page.reload();await loaded(c.target,'설명/공백 %2F +.html');return {explicit_return:true,reload:true,automatic_reconnect_owner:'TASK-004-05',automatic_reconnect:'not exercised by explicit authenticated return'};
  });
  await page.setViewportSize({width:1280,height:900});
  await check('Browser page identity, nonblank, no framework overlay and console health',async()=>{
    if(!await page.title()||!await page.locator('iframe[title="결과 뷰어"]').count())throw Error('blank/wrong page');
    const renderer=await frame().locator('body').evaluate(()=>location.origin),host=await page.evaluate(()=>location.origin);
    if(renderer===host)throw Error('artifact shared the trusted host origin');
    const expected=m=>/^Failed to load resource: the server responded with a status of (403|404|410) /.test(m.text)||(m.level==='warning'&&m.text.startsWith('An iframe which has both allow-scripts and allow-same-origin for its sandbox attribute'));
    if(await page.locator('vite-error-overlay').count()||errors.length||consoleMessages.some(m=>!expected(m)))throw Error('page/console error');
    return {title:await page.title(),url:page.url(),errors,consoleMessages,renderer_origin_distinct:renderer!==host,expected_diagnostics:'403/404/410 failure cases and Chrome sandbox-attribute warning on the existing distinct renderer origin'};
  });
  return {cases,errors,requests,consoleMessages};
}
