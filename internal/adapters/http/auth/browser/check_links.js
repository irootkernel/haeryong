async page => {
  const c = LINKS_CONSTANTS, cases = [], errors = [], consoleMessages = [], requests = [];
  page.setDefaultTimeout(15000);
  page.on('pageerror', error => errors.push(String(error)));
  page.on('console', message => { if (['error', 'warning'].includes(message.type())) consoleMessages.push({level:message.type(),text:message.text()}); });
  page.on('request', request => { if (request.url().includes('/api/v1/versions/')) requests.push({url:request.url(),method:request.method()}); });
  await page.evaluate(token => localStorage.setItem('haeryong.owner-session.v1',token),c.token);
  const current = () => page.evaluate(() => location.pathname + location.search + location.hash);
  const check = async (name, run) => { try { cases.push({name,ok:true,detail:await run()}); } catch (error) { cases.push({name,ok:false,error:String(error),url:await current()}); } };
  const openLinks = async () => {
    const toggle = page.locator('#reader-context-toggle');
    if (await toggle.getAttribute('aria-expanded') !== 'true') await toggle.click();
    await page.getByRole('tab',{name:'Links',exact:true}).click();
    await page.getByRole('region',{name:'연결 패널',exact:true}).waitFor();
  };
  await page.setViewportSize({width:1440,height:1000});
  await page.goto(c.base+c.target_route);
  await page.locator('.service-state[data-state="verified"]').waitFor();
  await page.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'F03 target v1',exact:true}).waitFor();
  await check('BL production Links keyboard tabs, canonical counts and honest suggestions',async()=>{
    await page.locator('#reader-context-toggle').click();
    const evidence=page.getByRole('tab',{name:'Evidence',exact:true});await evidence.focus();await evidence.press('ArrowRight');
    const links=page.getByRole('tab',{name:'Links',exact:true});
    if(await links.getAttribute('aria-selected')!=='true'||!await links.evaluate(element=>element===document.activeElement))throw Error('tab keyboard selection/focus failed');
    const back=page.getByRole('region',{name:'Backlinks',exact:true});
    await back.getByText('3개 연결 · 1개 참조 문서',{exact:true}).waitFor();
    await back.getByRole('button',{name:'exact',exact:true}).waitFor();
    await page.getByRole('region',{name:'Authored references',exact:true}).waitFor();
    await page.getByRole('region',{name:'Suggestions',exact:true}).getByText('제안이 없습니다. AI 관련 제안은 아직 제공하지 않습니다.',{exact:true}).waitFor();
    await page.screenshot({path:c.work+'/desktop-links.png'});
    return {url:await current(),edge_count:'3',referring_document_count:'1',keyboard:true,screenshot:c.work+'/desktop-links.png'};
  });
  await check('BL production backlink opens exact referring anchor and returns',async()=>{
    await page.getByRole('region',{name:'Backlinks',exact:true}).getByRole('button',{name:'exact',exact:true}).click();
    await page.waitForFunction(id=>location.pathname.endsWith('/versions/'+id),c.source.version_id);
    await page.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'F03 source B',exact:true}).waitFor();
    await openLinks();
    await page.getByRole('region',{name:'Backlinks',exact:true}).getByText('현재 범위에 검증된 백링크가 없습니다.',{exact:true}).waitFor();
    await page.getByRole('button',{name:'참조한 페이지로 돌아가기',exact:true}).click();
    await page.waitForFunction(id=>location.pathname.endsWith('/versions/'+id),c.target.version_id);
    if(await current()!==c.target_route)throw Error('return changed pinned page/query/region');
    return {exact_source:true,actual_empty_backlinks:true,return:await current()};
  });
  await check('BL authored action resolves exact target from host declaration',async()=>{
    await page.goto(c.base+c.source_route);await page.locator('.service-state[data-state="verified"]').waitFor();await openLinks();
    await page.getByRole('region',{name:'Authored references',exact:true}).getByRole('button',{name:'exact',exact:true}).click();
    await page.waitForFunction(id=>location.pathname.endsWith('/versions/'+id),c.target.version_id);
    if(await current()!==c.target_route)throw Error('host-authored action lost target identity');
    return {target:await current()};
  });
  await check('BL explicit test-owned unavailable and tombstone screen states',async()=>{
    await page.route('**/api/v1/versions/*/backlinks?**',route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{code:'SERVICE_RECOVERING',retryable:true,details:{}}})}));
    await openLinks();await page.getByRole('button',{name:'백링크 새로고침',exact:true}).click();
    await page.getByRole('region',{name:'Backlinks',exact:true}).getByRole('alert').waitFor();
    await page.unroute('**/api/v1/versions/*/backlinks?**');
    await page.route('**/api/v1/versions/'+c.source.version_id+'/references?**',async route=>{
      const response=await route.fetch(),payload=await response.json(),row=payload.data.items.find(row=>row.id==='gone');
      row.resolution='tombstoned';row.target={document_id:c.target.document_id,version_id:c.target.version_id};row.label=null;row.edge_id=null;row.reader_url=null;row.reason=null;
      payload.data.counts.edge_count=(BigInt(payload.data.counts.edge_count)-1n).toString();
      await route.fulfill({response,body:JSON.stringify(payload)});
    });
    await page.goto(c.base+c.source_route);await page.locator('.service-state[data-state="verified"]').waitFor();await openLinks();
    const authored=page.getByRole('region',{name:'Authored references',exact:true});await authored.getByText('삭제된 대상',{exact:true}).waitFor();
    if(await authored.getByRole('button',{name:'gone',exact:true}).count())throw Error('tombstone is clickable');
    await page.unroute('**/api/v1/versions/'+c.source.version_id+'/references?**');
    return {evidence:'test-owned HTTP response projections; not a lifecycle mutation',unavailable:true,tombstone:true};
  });
  await check('BL F5 authored and backlink personal reads cannot navigate after tab context changes',async()=>{
    const results=[];
    for(const kind of ['authored','backlink']) {
      const route=kind==='authored'?c.source_route:c.target_route;
      const version=kind==='authored'?c.source.version_id:c.target.version_id;
      await page.goto(c.base+route);await page.locator('.service-state[data-state="verified"]').waitFor();
      await openLinks();
      const section=page.getByRole('region',{name:kind==='authored'?'Authored references':'Backlinks',exact:true});
      const button=section.getByRole('button',{name:'exact',exact:true});await button.waitFor();
      let entered,release;
      const seen=new Promise(done=>{entered=done;}), held=new Promise(done=>{release=done;});
      const pattern='**/api/v1/versions/'+version+'/page-position?**';
      await page.route(pattern,async request=>{
        const response=await request.fetch();entered();await held;await request.fulfill({response});
      });
      try {
        await button.click();await seen;
        await page.getByRole('tab',{name:'Evidence',exact:true}).click();
        const before=await page.evaluate(()=>({url:location.pathname+location.search+location.hash,length:history.length,state:JSON.stringify(history.state)}));
        const finished=page.waitForResponse(response=>response.url().includes('/versions/'+version+'/page-position?'));
        release();await (await finished).finished();
        await page.evaluate(()=>new Promise(done=>requestAnimationFrame(()=>requestAnimationFrame(done))));
        const after=await page.evaluate(()=>({url:location.pathname+location.search+location.hash,length:history.length,state:JSON.stringify(history.state)}));
        if(JSON.stringify(before)!==JSON.stringify(after))throw Error(kind+' stale navigation/history/return position');
        if(await page.getByRole('tab',{name:'Evidence',exact:true}).getAttribute('aria-selected')!=='true')throw Error('changed tab lost');
        results.push({kind,discarded:true,unchanged_history:true});
      } finally {release();await page.unroute(pattern);}
    }
    return {barrier:'real page-position response deferred; tab changed before release',results};
  });
  await check('BL narrow touch controls, Escape and focus return',async()=>{
    await page.setViewportSize({width:390,height:844});
    await page.goto(c.base+c.target_route);await page.locator('.service-state[data-state="verified"]').waitFor();
    // Initial hash focus can close the narrow Sheet by design. Wait for its
    // actual frame result before opening the context panel for this check.
    await page.getByText('영역 '+c.region+'에 초점을 맞췄습니다.',{exact:true}).waitFor();await openLinks();
    const exact=page.getByRole('region',{name:'Backlinks',exact:true}).getByRole('button',{name:'exact',exact:true});await exact.waitFor();
    const box=await exact.boundingBox();if(!box||box.height<44)throw Error('touch control smaller than 44px');
    await page.screenshot({path:c.work+'/phone-links.png'});
    await page.keyboard.press('Escape');
    await page.locator('#reader-context-panel').waitFor({state:'hidden'});
    if(!await page.locator('#reader-context-toggle').evaluate(element=>element===document.activeElement))throw Error('sheet focus not returned');
    return {width:390,touch_height:box.height,escape:true,screenshot:c.work+'/phone-links.png'};
  });
  if(errors.length)throw Error('uncaught browser errors: '+JSON.stringify(errors));
  if(requests.some(request=>request.url.includes('/related')))throw Error('empty suggestions called unimplemented related');
  return {cases,errors,consoleMessages,requests,complete:cases.every(row=>row.ok),browser_plugin:'not available'};
}
