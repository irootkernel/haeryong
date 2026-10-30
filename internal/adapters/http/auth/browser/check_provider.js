async page => {
  const input=INPUT, errors=[], metadataReplies=[], responseReads=[];
  const metadataBase=input.origin+'/api/v1/versions/'+input.version+'/metadata';
  const metadataURL=metadataBase+'?kind=supplied';
  const classify=(observations,replies)=>{
    const available=replies.map(reply=>({...reply,used:false}));
    return observations.filter(error=>{
      if(error.kind!=='console_error'||error.url!==metadataURL||error.message!=='Failed to load resource: the server responded with a status of 404 (Not Found)')return true;
      const reply=available.find(reply=>!reply.used&&reply.device===error.device&&reply.url===error.url&&reply.method==='GET'&&reply.status===404&&reply.code==='NOT_FOUND'&&reply.reason==='metadata_not_supplied');
      if(!reply)return true;reply.used=true;return false;
    });
  };
  const baseline={device:'desktop',kind:'console_error',url:metadataURL,message:'Failed to load resource: the server responded with a status of 404 (Not Found)'};
  const reply={device:'desktop',url:metadataURL,method:'GET',status:404,code:'NOT_FOUND',reason:'metadata_not_supplied'};
  if(classify([baseline],[reply]).length!==0)throw Error('expected response control failed');
  const controls=[{...reply,url:metadataBase+'?kind=generated'},{...reply,method:'POST'},{...reply,status:500},{...reply,reason:'other'},{...reply,device:'touch'}];
  if(controls.some(control=>classify([baseline],[control]).length!==1)||classify([baseline,baseline],[reply]).length!==1)throw Error('metadata observer accepted an unrelated or reused response');
  const observe=(target,device)=>{
    target.on('pageerror',error=>errors.push({device,kind:'pageerror',message:String(error).slice(0,300)}));
    target.on('console',message=>{if(message.type()==='error')errors.push({device,kind:'console_error',message:message.text().slice(0,300),url:message.location().url})});
    target.on('response',response=>{
      const url=response.url();
      if(url===metadataBase||url.startsWith(metadataBase+'?'))responseReads.push(response.json().then(body=>metadataReplies.push({device,url:response.url(),method:response.request().method(),status:response.status(),code:body.error?.code,reason:body.error?.details?.reason})).catch(()=>errors.push({device,kind:'response_unreadable'})));
    });
  };
  observe(page,'desktop');
  let providerRequests=0;
  page.on('request',request=>{if(request.url()===input.origin+'/api/v1/providers'&&request.method()==='GET')providerRequests++});
  await page.goto(input.origin+'/library');
  await page.getByRole('navigation',{name:'주요 탐색'}).getByRole('link',{name:'설정',exact:true}).click();
  await page.getByRole('heading',{name:'설정',exact:true}).waitFor();
  const generalText=await page.locator('main').innerText();
  if(!generalText.includes('이 화면은 아직 구현되지 않았습니다.')||await page.locator('[data-provider-screen]').count())throw Error('default Settings is not the existing General landing');
  if(input.screenshot)await page.screenshot({path:input.screenshot.replace(/\.png$/,'-general.png'),fullPage:true});
  await page.getByRole('button',{name:'연결 화면',exact:true}).click();
  await page.getByRole('heading',{name:'Haeryong 연결',exact:true}).waitFor();
  await page.getByRole('heading',{name:'연결된 소유자',exact:true}).waitFor();
  if(!(await page.getByRole('button',{name:'로그아웃',exact:true}).isVisible()))throw Error('existing Connect owner tools missing');
  await page.goto(input.origin+'/settings/general');
  await page.getByRole('heading',{name:'설정',exact:true}).waitFor();
  if((await page.locator('main').innerText())!==generalText)throw Error('empty and explicit General differ');
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  if(providerRequests!==0)throw Error('General or Connect triggered provider status');
  const generalAI=page.getByRole('link',{name:'AI 제공자',exact:true});
  const modifiedDefault=await generalAI.evaluate(link=>{
    let prevented=null;
    document.addEventListener('click',event=>{prevented=event.defaultPrevented;event.preventDefault()},{once:true});
    link.dispatchEvent(new MouseEvent('click',{bubbles:true,cancelable:true,ctrlKey:true}));
    return prevented;
  });
  if(modifiedDefault!==false||!page.url().endsWith('/settings/general'))throw Error('modified AI link intercepted SPA navigation');
  await page.evaluate(()=>{window.__C03_SPA_DOCUMENT=true});
  await generalAI.focus();await page.keyboard.press('Shift+Tab');await page.keyboard.press('Tab');
  if(!(await generalAI.evaluate(element=>element===document.activeElement)))throw Error('Tab did not reach explicit AI link');
  const initialProviderReply=page.waitForResponse(response=>response.url()===input.origin+'/api/v1/providers'&&response.request().method()==='GET'&&response.status()===200);
  await page.keyboard.press('Enter');await initialProviderReply;
  if(!(await page.evaluate(()=>window.__C03_SPA_DOCUMENT===true))||!page.url().endsWith('/settings/ai'))throw Error('AI link did not navigate within shared shell');
  await page.getByRole('heading',{name:'AI 제공자',exact:true}).waitFor();
  await page.locator('[data-provider-status]').waitFor({timeout:45000});
  const actual=await page.evaluate(async()=>{
    const r=await fetch('/api/v1/providers',{headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1')}});
    if(r.status!==200)throw Error('actual provider status refused');return (await r.json()).data;
  });
  if(!['ready','auth_required','quota_unavailable','quota_exhausted','unsupported_version','source_unavailable','paused','provider_error'].includes(actual.state))throw Error('noncanonical status');
  if(actual.catalog_proves_access!==false)throw Error('catalog presented as entitlement');
  const text=await page.locator('[data-provider-screen]').innerText();
  if(text.includes('@')||text.includes('auth.json')||text.includes('Bearer '))throw Error('private account material displayed');
  if(input.screenshot)await page.screenshot({path:input.screenshot,fullPage:true});
  const states=[
    ['auth_required','로그인을 확인하세요','unsupported_authentication'],
    ['quota_exhausted','소진되었습니다',null],
    ['quota_unavailable','사용량 정보가 없습니다','quota_unknown'],
    ['paused','일시 중지되었습니다','unsupported_provider'],
    ['paused','일시 중지되었습니다','mode_unavailable'],
    ['paused','일시 중지되었습니다','usage_not_allowed'],
  ];
  let selected='auth_required',selectedReason='unsupported_authentication',queries=0,absentUI=0,pendingProviderReply=null;
  const metadataPerState=[];
  const dto=()=>({provider_id:'codex',state:selected,reason:selectedReason,authentication_mode:selected==='auth_required'?'apiKey':'unknown',ordinary_usage_allowed:selectedReason==='usage_not_allowed'?false:null,models:[],read_only:false,lookup_supported:true,cancellation_supported:true,catalog_proves_access:false});
  await page.route('**/api/v1/providers',async route=>{queries++;if(pendingProviderReply)await pendingProviderReply;await route.fulfill({json:{data:dto()}})});
  for(const [index,[state,label,reason]]of states.entries()){
    selected=state;selectedReason=reason;const before=queries;
    const refresh=page.getByRole('button',{name:'제공자 상태 새로 고침',exact:true});
    const announcement=page.locator('[data-provider-screen] [role="status"]');
    if(await announcement.getAttribute('aria-live')!=='polite'||await announcement.getAttribute('aria-atomic')!=='true')throw Error('provider announcement semantics missing');
    const statusNode=await announcement.elementHandle();
    await refresh.focus();await page.keyboard.press('Shift+Tab');await page.keyboard.press('Tab');
    if(!(await refresh.evaluate(element=>element===document.activeElement)))throw Error('Tab did not reach refresh');
    let releaseReply;pendingProviderReply=new Promise(resolve=>{releaseReply=resolve});
    const refreshResponse=page.waitForResponse(response=>response.url()===input.origin+'/api/v1/providers'&&response.request().method()==='GET'&&response.status()===200);
    await page.keyboard.press('Enter');
    await page.waitForFunction(()=>document.querySelector('[data-provider-screen] button').getAttribute('aria-disabled')==='true');
    if(!(await statusNode.evaluate(node=>node.isConnected&&node===document.querySelector('[data-provider-screen] [role="status"]')))||(await announcement.innerText())!=='제공자 상태 확인 중…')throw Error('busy announcement node changed or was not updated');
    pendingProviderReply=null;releaseReply();await refreshResponse;
    await page.waitForFunction(()=>document.querySelector('[data-provider-screen] button').getAttribute('aria-disabled')==='false');
    if(!(await refresh.evaluate(element=>element===document.activeElement)))throw Error('refresh lost keyboard focus');
    if(!(await statusNode.evaluate(node=>node.isConnected&&node===document.querySelector('[data-provider-screen] [role="status"]'))))throw Error('final announcement node changed');
    await page.locator('[data-provider-status]').getByText(label,{exact:false}).waitFor();
    if(queries<=before)throw Error('keyboard did not refresh');
    if(state==='auth_required')await page.getByText('API 키 로그인 (지원되지 않음)',{exact:true}).waitFor();
    const reasonLabels={unsupported_provider:'제공자 또는 연결 주소는 지원되지 않습니다',mode_unavailable:'읽기 전용 실행 모드를 확인할 수 없습니다',usage_not_allowed:'소진 여부는 확인되지 않았습니다'};
    if(reasonLabels[reason])await page.locator('[data-provider-reason]').filter({hasText:reasonLabels[reason]}).waitFor();
    if(reason==='usage_not_allowed')await page.getByText('현재 허용되지 않음',{exact:true}).waitFor();

    await page.goto(input.origin+'/library');await page.getByRole('button',{name:'AI 질문',exact:true}).waitFor();
    if(!(await page.getByRole('button',{name:'AI 질문',exact:true}).isDisabled()))throw Error('future question action enabled');
    const repliesBefore=metadataReplies.length,readsBefore=responseReads.length;
    const metadataResponse=page.waitForResponse(response=>response.url()===metadataURL&&response.request().method()==='GET');
    await page.goto(input.route);await page.frameLocator('iframe[title="결과 뷰어"]').locator('#review-fixture').waitFor();
    await metadataResponse;
    const version=await page.evaluate(async id=>(await (await fetch('/api/v1/versions/'+id,{headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1')}})).json()).data,input.version);
    if(version.supplied_metadata_sha256!==null||version.supplied_metadata_url!==null)throw Error('fixture no longer has absent optional metadata');
    const absent=page.getByText('공급된 소스/근거 메타데이터 없음',{exact:false});
    const opened=!(await absent.isVisible());if(opened)await page.locator('#reader-context-toggle').click();
    await absent.waitFor();absentUI++;
    await Promise.all(responseReads.slice(readsBefore));
    const observed=metadataReplies.slice(repliesBefore);
    if(!observed.length||observed.some(reply=>reply.device!=='desktop'||reply.url!==metadataURL||reply.method!=='GET'||reply.status!==404||reply.code!=='NOT_FOUND'||reply.reason!=='metadata_not_supplied'))throw Error('current Reader state has no exact expected metadata response');
    metadataPerState.push({state,reason,responses:observed.length});
    if(opened)await page.locator('#reader-context-toggle').click();
    const action=index===0?'검토 시작':index%2===1?'검토 완료로 표시':'검토 다시 열기';
    await page.locator('.review-control').getByRole('button',{name:action,exact:true}).click();
    await page.locator('#review-state').filter({hasText:index%2===1?'검토 완료':'검토 중'}).waitFor();
    await page.goto(input.origin+'/settings/ai');await page.locator('[data-provider-status]').waitFor();
  }
  let phoneQueries=0;
  const context=await page.context().browser().newContext({hasTouch:true,viewport:{width:390,height:844}});
  try {
    const token=await page.evaluate(()=>localStorage.getItem('haeryong.owner-session.v1'));
    await context.addInitScript(({origin,token})=>{if(window===top&&location.origin===origin)localStorage.setItem('haeryong.owner-session.v1',token)}, {origin:input.origin,token});
    await context.route('**/api/v1/providers',route=>{phoneQueries++;return route.fulfill({json:{data:dto()}})});
    const phone=await context.newPage();observe(phone,'touch');await phone.goto(input.origin+'/settings');
    await phone.getByRole('heading',{name:'설정',exact:true}).waitFor();
    if(phoneQueries!==0||await phone.locator('[data-provider-screen]').count())throw Error('touch General triggered provider status');
    const touchAI=phone.getByRole('link',{name:'AI 제공자',exact:true});
    const aiBox=await touchAI.boundingBox();const connectBox=await phone.getByRole('button',{name:'연결 화면',exact:true}).boundingBox();
    if(!aiBox||aiBox.height<44||!connectBox||connectBox.height<44)throw Error('General navigation target too small');
    if(await phone.locator('main').evaluate(element=>element.scrollWidth>innerWidth))throw Error('General overflow phone');
    if(input.screenshot)await phone.screenshot({path:input.screenshot.replace(/\.png$/,'-general-touch.png'),fullPage:true});
    await touchAI.tap();await phone.locator('[data-provider-status]').waitFor();
    const beforeTouch=phoneQueries;const touchReply=phone.waitForResponse(response=>response.url()===input.origin+'/api/v1/providers'&&response.status()===200);
    await phone.getByRole('button',{name:'제공자 상태 새로 고침',exact:true}).tap();await touchReply;await phone.waitForFunction(()=>document.querySelector('[data-provider-screen] button').getAttribute('aria-disabled')==='false');
    if(phoneQueries<=beforeTouch)throw Error('touch refresh did not dispatch');
    await phone.reload();await phone.locator('[data-provider-status]').waitFor();
    if(await phone.locator('[data-provider-screen]').evaluate(e=>e.scrollWidth>innerWidth))throw Error('provider settings overflow phone');
    if(input.screenshot)await phone.screenshot({path:input.screenshot.replace(/\.png$/,'-touch.png'),fullPage:true});
    await phone.getByRole('navigation',{name:'다른 설정'}).getByRole('link',{name:'연결 및 장치',exact:true}).tap();
    await phone.getByRole('heading',{name:'Haeryong 연결',exact:true}).waitFor();
    await phone.getByRole('heading',{name:'연결된 소유자',exact:true}).waitFor();
    await phone.getByRole('navigation',{name:'주요 탐색'}).getByRole('link',{name:'설정',exact:true}).tap();
    await phone.getByRole('heading',{name:'설정',exact:true}).waitFor();
    if(await phone.locator('[data-provider-screen]').count())throw Error('touch sidebar Settings did not return General');
  } finally {await context.close()}
  await Promise.all(responseReads);
  const unexpected=classify(errors,metadataReplies);
  if(absentUI!==states.length||metadataReplies.some(reply=>reply.url!==metadataURL||reply.method!=='GET'||reply.status!==404||reply.code!=='NOT_FOUND'||reply.reason!=='metadata_not_supplied'))throw Error('optional metadata negative response or absent UI not established');
  if(unexpected.length)throw Error('unexpected page or console errors: '+JSON.stringify(unexpected));
  const links=page.getByRole('navigation',{name:'다른 설정'}).getByRole('link');
  for(const link of await links.all()){const box=await link.boundingBox();if(!box||box.height<44)throw Error('settings link touch target too small');}
  await page.evaluate(()=>{window.__C03_SPA_DOCUMENT=true});
  await page.getByRole('navigation',{name:'다른 설정'}).getByRole('link',{name:'프로젝트 및 소스',exact:true}).click();await page.getByRole('heading',{name:'프로젝트',exact:true}).waitFor();
  if(!(await page.evaluate(()=>window.__C03_SPA_DOCUMENT===true)))throw Error('settings link reloaded document');
  await page.goBack();await page.locator('[data-provider-status]').waitFor();
  if(!page.url().endsWith('/settings/ai'))throw Error('Back did not restore AI route');
  await page.goForward();await page.getByRole('heading',{name:'프로젝트',exact:true}).waitFor();
  await page.goBack();await page.locator('[data-provider-status]').waitFor();
  const connect=page.getByRole('navigation',{name:'다른 설정'}).getByRole('link',{name:'연결 및 장치',exact:true});
  await connect.focus();await page.keyboard.press('Shift+Tab');await page.keyboard.press('Tab');
  if(!(await connect.evaluate(element=>element===document.activeElement)))throw Error('Tab did not reach Connect');
  await page.keyboard.press('Enter');await page.getByRole('heading',{name:'Haeryong 연결',exact:true}).waitFor();
  await page.getByRole('heading',{name:'연결된 소유자',exact:true}).waitFor();
  if(await page.locator('vite-error-overlay').count()||!(await page.title()).includes('Haeryong'))throw Error('wrong page identity or framework overlay');
  await Promise.all(responseReads);
  if(classify(errors,metadataReplies).length)throw Error('navigation introduced a console/page error');
  return {generalLanding:true,emptyEqualsGeneral:true,generalProviderRequests:0,connectOwnerTools:true,explicitAIKeyboard:true,connectKeyboard:true,generalTouchTargets44:true,touchGeneralAIConnect:true,backForward:true,modifiedAILinkSyntheticDefault:true,actualStatus:actual.state,actualAuthentication:actual.authentication_mode,actualVersion:actual.version,catalogNotEntitlement:true,controlledStates:states.map(x=>({state:x[0],reason:x[2]})),keyboardRefresh:true,hasTouch:true,tapRefresh:true,reload:true,readerUsable:true,reviewVisible:true,futureQuestionDisabled:true,unexpectedConsoleErrors:0,expectedMetadata404:errors.length-unexpected.length,metadataNegativeControls:controls.length+1,refreshFocusRetained:true,tabAccess:true,stablePoliteAnnouncement:true,metadataPerState,settingsLinksSPA:true,touchTargets44:true,actualMetadataReplies:metadataReplies.length,absentUIStates:absentUI,touchConsoleObserved:true,touchStatusRequests:phoneQueries,title:await page.title(),url:await page.url(),browser:'Chrome',browserPlugin:'not available; existing Playwright CLI workflow'};
}
