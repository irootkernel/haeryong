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
  if(c.versions) {
    const v=c.versions, versionsPath='/documents/'+v.old.document_id+'/versions';
    const open=async()=>{await page.goto(c.base+v.encoded_route);await page.locator('#reader-versions').waitFor();};
    const enter=async()=>{await page.locator('#reader-versions').focus();await page.keyboard.press('Enter');await page.locator('#versions-from').waitFor();};
    await page.setViewportSize({width:1440,height:1000});
    await check('T5 Copy link and S05 keyboard round trip preserves exact selection',async()=>{
      await open(); await page.locator('#reader-copy-link').focus();await page.keyboard.press('Enter');
      const field=page.getByRole('textbox',{name:'복사할 링크',exact:true});await field.waitFor();
      const copied=await field.inputValue(), parsed=await page.evaluate(value=>{const u=new URL(value);return {origin:u.origin,page:u.searchParams.get('page'),query:u.searchParams.get('page_query'),fragment:u.searchParams.get('page_fragment'),region:new URLSearchParams(u.hash.slice(1)).get('region'),hash:u.hash};},copied);
      if(parsed.origin!==c.base||parsed.page!==v.special||parsed.query!==v.query||parsed.fragment!==v.fragment||parsed.region!==v.region||copied.includes(c.token))throw Error('copied tuple/base/secret');
      await enter();
      if(await page.locator('#versions-from').inputValue()!==v.old.version_id||await page.locator('#versions-to').inputValue()!==v.new.version_id)throw Error('default selected/latest pair');
      await page.locator('#versions-return').focus();await page.keyboard.press('Enter');
      await page.locator('#reader-versions').waitFor();
      await page.waitForFunction(()=>document.activeElement?.id==='reader-versions');
      const actual=await page.evaluate(()=>({page:new URLSearchParams(location.search).get('page'),query:new URLSearchParams(location.search).get('page_query'),fragment:new URLSearchParams(location.search).get('page_fragment'),hash:location.hash}));
      if(actual.page!==v.special||actual.query!==v.query||actual.fragment!==v.fragment||actual.hash!==parsed.hash)throw Error('S05 return changed raw tuple');
      // R2-1: bootstrap stays real; fail only the returned Reader's session
      // open so the terminal failure originates inside IsolatedViewer.
      await enter();let failedOpens=0;
      const sessionOpen='**/api/v1/versions/'+v.old.version_id+'/render-sessions';
      await page.route(sessionOpen,route=>{failedOpens++;return route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{code:'SERVICE_RECOVERING',retryable:true,details:{}}})});});
      try {
        await page.locator('#versions-return').click();
        await page.getByRole('alert').filter({hasText:'페이지 연결을 확인하지 못했습니다:'}).waitFor();
        await page.waitForFunction(()=>document.activeElement?.id==='reader-versions'&&history.state?.haeryongVersionsFocus===false);
        if(failedOpens<1)throw Error('viewer failure fixture never intercepted session open');
      } finally {await page.unroute(sessionOpen);}
      return {copy:copied,keyboard:true,focus:true,local_only:true,viewer_failure_return_focus:true,viewer_failure_flag_consumed:true,failed_render_opens:failedOpens,fault:'labelled render-session 503 only; bootstrap remains real'};
    });
    await check('T5 automatic remembered-session return twice reload and Back Forward',async()=>{
      await open();
      for(let cycle=0;cycle<2;cycle++) {
        await page.evaluate(()=>{localStorage.removeItem('haeryong.owner-session.v1');dispatchEvent(new StorageEvent('storage',{key:'haeryong.owner-session.v1',newValue:null}));});
        await page.waitForFunction(()=>location.pathname==='/connect'&&new URLSearchParams(location.search).has('return'));
        const returned=await page.evaluate(()=>new URLSearchParams(location.search).get('return'));
        await page.evaluate(token=>{localStorage.setItem('haeryong.owner-session.v1',token);dispatchEvent(new StorageEvent('storage',{key:'haeryong.owner-session.v1',newValue:token}));},c.token);
        await page.locator('#reader-versions').waitFor();
        if(await current()!==returned)throw Error('automatic login return lost route');
      }
      const before=await current();await page.reload();await page.locator('#reader-versions').waitFor();
      if(await current()!==before)throw Error('reload retargeted');
      await enter();await page.goBack();await page.locator('#reader-versions').waitFor();
      await page.goForward();await page.locator('#versions-from').waitFor();
      // R2-2: each recovered action starts on a S05 instance mounted while
      // degraded, rather than on one already verified before the outage.
      await page.locator('#versions-return').click();
      await page.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'Versions old',exact:true}).waitFor();
      const sessionPattern='**/api/v1/session';
      const degrade=async()=>{
        await page.route(sessionPattern,route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{code:'SERVICE_RECOVERING',retryable:true,details:{}}})}));
        await page.evaluate(()=>document.dispatchEvent(new Event('visibilitychange')));
        await page.locator('.service-state[data-state="degraded"]').waitFor();
      };
      const recover=async()=>{
        await page.unroute(sessionPattern);
        await page.evaluate(()=>document.dispatchEvent(new Event('visibilitychange')));
        await page.locator('.service-state[data-state="verified"]').waitFor();
      };
      try {
        for(const action of ['latest','return','pair']) {
          await degrade();await page.goBack();await page.locator('#versions-from').waitFor();
          await page.locator('.service-state[data-state="degraded"]').waitFor();
          await recover();
          if(action==='latest') {
            await page.locator('#versions-open-latest').click();
            await page.waitForFunction(id=>location.pathname.endsWith('/'+id),v.new.version_id);
            await page.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'Versions new',exact:true}).waitFor();
          } else {
            if(action==='pair') {
              await page.locator('#versions-to').selectOption(v.old.version_id);
              await page.waitForFunction(id=>document.querySelector('#versions-to')?.value===id,v.old.version_id);
            }
            await page.locator('#versions-return').click();
            await page.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'Versions old',exact:true}).waitFor();
            await page.waitForFunction(()=>document.activeElement?.id==='reader-versions'&&history.state?.haeryongVersionsFocus===false);
          }
        }
        // Fail the initial history query on another degraded mount. Detail
        // stays readable, allowing the real ImportControl to remain open.
        const historyPattern='**/api/v1/documents/'+v.old.document_id+'/versions*';
        await page.route(historyPattern,route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{code:'SERVICE_RECOVERING',retryable:true,details:{}}})}));
        try {
          await degrade();await page.goBack();
          await page.getByRole('alert').filter({hasText:'비교를 사용할 수 없습니다:'}).waitFor();
          const pinned=await current();
          await page.getByRole('button',{name:'새 버전 가져오기',exact:true}).click();
          const control=page.getByTestId('import-control');await control.getByTestId('base-preview').waitFor();
          await control.evaluate(element=>{window.__t5LoadRetryImport=element;});
          await page.unroute(historyPattern);await recover();
          await page.getByRole('button',{name:'버전 이력 다시 불러오기',exact:true}).click();
          await page.locator('#versions-from').waitFor();
          if(await current()!==pinned||await page.locator('#versions-from').inputValue()!==v.old.version_id||await page.locator('#versions-to').inputValue()!==v.old.version_id||!await control.evaluate(element=>element===window.__t5LoadRetryImport))throw Error('load retry lost pinned pair or ImportControl');
          await page.locator('#versions-return').click();await page.locator('#reader-versions').waitFor();
        } finally {await page.unroute(historyPattern);}
      } finally {await page.unroute(sessionPattern);await page.evaluate(()=>document.dispatchEvent(new Event('visibilitychange')));}
      await page.goto(c.base+'/connect?return=https%3A%2F%2Fexternal.invalid%2F');await page.waitForFunction(()=>location.pathname==='/');
      return {unsafe_external_rejected:true,cycles:2,reload:true,back_forward:true,degraded_entry_recovered_actions:['latest','return','pair'],initial_load_retry:true,retry_kept_pair_and_import:true,evidence:'real App recheck/history/actions; labelled session/history 503 and cross-tab credential removal/restoration'};
    });
    await check('T5 S1 actual progressive rows metadata regions same-cursor retry both EOFs',async()=>{
      await open();await enter();
      await page.locator('table[aria-label="파일 차이"] tbody tr').first().waitFor();
      await page.locator('#versions-metadata').getByText('document',{exact:true}).count();
      await page.waitForFunction(()=>{const text=document.querySelector('#versions-metadata')?.textContent||'';return text.includes('Versions old metadata')&&text.includes('Versions new metadata')&&text.includes('old region label')&&text.includes('new region label');});
      if(!(await page.locator('#versions-progress').textContent()).includes('미완료'))throw Error('premature complete');
      const changed=page.locator('table[aria-label="파일 차이"] tbody tr').filter({hasText:'files/000.txt'});
      await changed.getByRole('button',{name:'텍스트 보기 · 이전',exact:true}).click();
      await changed.getByRole('button',{name:'텍스트 보기 · 대상',exact:true}).click();
      const texts=page.getByRole('region',{name:'선택한 파일 텍스트',exact:true});
      for(let i=0;i<2;i++){await texts.nth(i).getByRole('button',{name:'다음 텍스트 블록',exact:true}).click();await texts.nth(i).getByText('텍스트 열거 완료 · 무결성 확인됨',{exact:false}).waitFor();}
      if(await texts.nth(0).locator('pre').textContent()!=='old 0\n'||await texts.nth(1).locator('pre').textContent()!=='changed text\n')throw Error('side-by-side exact text');
      await changed.getByRole('button',{name:'텍스트 보기 · 이전',exact:true}).click();
      await texts.nth(0).getByRole('button',{name:'다음 텍스트 블록',exact:true}).waitFor();
      // R1-4: the destination is an explicitly synthetic in-memory FSA
      // handle. Product file bytes are real; this proves panel ownership,
      // fresh consent and late-picker disposal, not an OS/disk transfer.
      await page.evaluate(()=>{
        window.__t5SavePicker=window.showSaveFilePicker;
        const state=window.__t5Download={opens:0,closes:0,bytes:[],hold:false,release:null};
        window.showSaveFilePicker=async()=>{
          if(state.hold)await new Promise(resolve=>{state.release=resolve;});
          return {name:'synthetic-t5-target',createWritable:async()=>{state.opens++;return {
            write:async chunk=>{state.bytes.push(...new Uint8Array(chunk));},close:async()=>{state.closes++;},abort:async()=>{},
          };}};
        };
      });
      try {
        await changed.getByRole('button',{name:'원본 내려받기',exact:true}).nth(0).click();
        const download=page.getByTestId('download-panel');
        const consent=async()=>{await download.getByRole('button',{name:'내려받기 시작',exact:true}).click();await download.getByRole('checkbox').check();await download.getByRole('button',{name:'동의하고 계속',exact:true}).click();};
        await consent();await download.getByRole('textbox',{name:'파일 이름',exact:true}).fill('first-consented-name.txt');
        await download.getByRole('button',{name:'저장 위치 선택 (시스템 저장 대화상자)',exact:true}).click();
        await download.getByTestId('download-done').waitFor();
        if(await page.evaluate(()=>new TextDecoder().decode(new Uint8Array(window.__t5Download.bytes)))!=='old 0\n')throw Error('synthetic target did not receive exact old file');
        await changed.getByRole('button',{name:'원본 내려받기',exact:true}).nth(1).click();
        if(await download.getByTestId('download-done').count())throw Error('completion carried to another version');
        await download.getByRole('button',{name:'내려받기 시작',exact:true}).click();
        if(await download.getByRole('checkbox').isChecked()||!await download.getByRole('button',{name:'동의하고 계속',exact:true}).isDisabled())throw Error('consent carried to another version');
        await download.getByRole('checkbox').check();await download.getByRole('button',{name:'동의하고 계속',exact:true}).click();
        if(await download.getByRole('textbox',{name:'파일 이름',exact:true}).inputValue()!=='000.txt')throw Error('destination name carried to another version');
        await page.evaluate(()=>{window.__t5Download.hold=true;});
        await download.getByRole('button',{name:'저장 위치 선택 (시스템 저장 대화상자)',exact:true}).click();
        await page.waitForFunction(()=>window.__t5Download.release!==null);
        await page.locator('table[aria-label="파일 차이"] tbody tr').filter({hasText:'files/001.txt'}).getByRole('button',{name:'원본 내려받기',exact:true}).nth(1).click();
        await download.getByRole('heading',{name:'파일 내려받기: files/001.txt',exact:true}).waitFor();
        await download.getByRole('button',{name:'내려받기 시작',exact:true}).waitFor();
        await page.evaluate(async()=>{window.__t5Download.release();await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));});
        if(await page.evaluate(()=>window.__t5Download.opens!==1||window.__t5Download.closes!==1)||await download.getByTestId('download-done').count())throw Error('late old destination wrote or completed under new path');
      } finally {await page.evaluate(()=>{window.__t5Download.release?.();window.showSaveFilePicker=window.__t5SavePicker;});}
      let entered,release,failed=false;const seen=new Promise(done=>entered=done),held=new Promise(done=>release=done),cursors=[];
      const pattern='**/api/v1/versions/*/files?**';
      await page.route(pattern,async route=>{
        const url=route.request().url(),cursor=url.includes('cursor=')?url.split('cursor=')[1].split('&')[0]:null;
        if(!cursor||!url.includes(v.old.version_id)){await route.continue();return;}
        cursors.push(cursor);
        if(!failed){failed=true;entered();await held;await route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{code:'SERVICE_RECOVERING',retryable:true,details:{}}})});}
        else await route.continue();
      });
      try {
        await page.locator('#versions-more').click();await seen;
        if(await page.locator('table[aria-label="파일 차이"] tbody tr').count()===0||!(await page.locator('#versions-progress').textContent()).includes('미완료'))throw Error('held later page hid initial rows');
        await page.screenshot({path:c.work+'/versions-progress.png'});release();
        const retry=page.getByRole('button',{name:'실패한 지점부터 다시 시도',exact:true});await retry.waitFor();await retry.click();
        await page.waitForFunction(()=>!document.querySelector('#versions-more')?.disabled);
        if(cursors[0]!==cursors[1])throw Error('retry cursor changed');
        for(let i=0;i<12&&await page.locator('#versions-more').count();i++){await page.locator('#versions-more').click();await page.waitForFunction(()=>!document.querySelector('#versions-more')?.disabled);}
        await page.getByText(/비교 완료 · 두 버전의 모든 경로 확인/).waitFor();
        return {metadata:true,page_regions:true,side_by_side_text:true,text_reopen:true,download_exact_selection:true,download_target:'labelled in-memory FSA handle; no disk/OS claim',unfinished_during_hold:true,same_cursor:true,both_eofs:true,barrier:'test-owned delay and one 503; inventories/metadata are real immutable imports'};
      } finally {release();await page.unroute(pattern);}
    });
    await check('T5 S3 cross-document pair is unavailable without foreign title/files',async()=>{
      await page.goto(c.base+versionsPath+'?from_version='+c.source.version_id+'&to_version='+v.new.version_id);
      await page.getByRole('alert').filter({hasText:'비교를 사용할 수 없습니다'}).waitFor();
      if(await page.locator('table[aria-label="파일 차이"]').count()||await page.getByText('F03 source B',{exact:true}).count())throw Error('foreign comparison disclosure');
      return {foreign_unavailable:true};
    });
    await check('T5 S2 emulated narrow touch controls and exact pair choice',async()=>{
      const context=await page.context().browser().newContext({hasTouch:true,viewport:{width:390,height:844}});
      await context.addInitScript(({token,base})=>{if(window===top&&location.origin===base)localStorage.setItem('haeryong.owner-session.v1',token);},{token:c.token,base:c.base});
      const phone=await context.newPage();
      try {
        await phone.goto(c.base+v.encoded_route);
        await phone.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'Versions old',exact:true}).waitFor();
        // The initial region focus can scroll the host; wait for its actual
        // acknowledgment before targeting a stable touch control.
        await phone.getByRole('status').filter({hasText:'영역 '+v.region+'에 '}).waitFor();
        await phone.locator('#reader-copy-link').tap();await phone.getByRole('textbox',{name:'복사할 링크',exact:true}).waitFor();
        await phone.locator('#reader-versions').tap();await phone.locator('#versions-from').waitFor();
        // Activate the native select by keyboard and touch; selectOption is
        // Playwright's exact native-select choice action (not a DOM injection).
        await phone.locator('#versions-to').focus();await phone.keyboard.press('Enter');await phone.keyboard.press('Escape');
        await phone.locator('#versions-to').selectOption(v.old.version_id);
        await phone.waitForFunction(id=>document.querySelector('#versions-to')?.value===id,v.old.version_id);
        await phone.locator('#versions-from').tap();await phone.keyboard.press('Escape');
        await phone.locator('#versions-from').selectOption(v.new.version_id);
        await phone.waitForFunction(id=>document.querySelector('#versions-from')?.value===id,v.new.version_id);
        await phone.screenshot({path:c.work+'/versions-phone.png'});
        await phone.locator('#versions-open-latest').tap();await phone.waitForFunction(id=>location.pathname.endsWith('/'+id),v.new.version_id);
        return {width:390,touch:'hasTouch emulation; no physical device',pair_keyboard_activation:true,exact_choice_action:'Playwright native selectOption',latest:v.new.version_id};
      } finally {await context.close();}
    });
    await check('T5 actual S05 Import control exact document base and publication',async()=>{
      await page.setViewportSize({width:1440,height:1000});await open();await enter();
      await page.getByRole('button',{name:'새 버전 가져오기',exact:true}).click();
      const control=page.getByTestId('import-control');await control.getByTestId('base-preview').filter({hasText:v.new.version_id}).waitFor();
      if(await control.getAttribute('data-preselected')!=='true')throw Error('not actual preselected import');
      await control.locator('input[type="file"][accept=".html,.htm"]').setInputFiles(c.work+'/third.html');
      const writes=[];let winner;
      const pattern='**/api/v1/imports';
      await page.route(pattern,async route=>{
        const request=route.request();writes.push({key:request.headers()['idempotency-key'],body:request.postDataJSON()});
        if(writes.length===1){const response=await page.request.get(c.competing_url);if(!response.ok())throw Error('competing fixture writer failed');winner=await response.json();await route.continue();}
        else if(writes.length===2){await route.fetch();await route.abort('connectionreset');}
        else await route.continue();
      });
      try {
        await control.getByRole('button',{name:'가져오기 시작',exact:true}).click();
        const fresh=control.getByRole('button',{name:'새 기준으로 새 작업 발행 (보관 페이로드 재사용)',exact:true});await fresh.waitFor({timeout:60000});
        await fresh.click();
        const recover=control.getByRole('button',{name:'보관된 작업으로 이어서 시도 (원래 U/S/I 재실행)',exact:true});await recover.waitFor();await recover.click();
        await page.getByRole('link',{name:/4 ·/}).waitFor({timeout:60000});
        await page.getByTestId('import-stage').filter({hasText:'발행 완료'}).waitFor();
        if(writes.length!==3||writes[0].body.document_id!==v.old.document_id||writes[0].body.base_version_id!==v.new.version_id||writes[1].key===writes[0].key||writes[2].key!==writes[1].key||writes[1].body.base_version_id!==winner.version_id||JSON.stringify(writes[1].body)!==JSON.stringify(writes[2].body))throw Error('target/base/new-intent/original-retry key mismatch');
        if(await page.locator('#versions-from').inputValue()!==v.old.version_id||await page.locator('#versions-to').inputValue()!==v.new.version_id)throw Error('publication silently retargeted comparison');
        // R1-5: reuse this driver's route barrier for one same-authority
        // disconnected/recovered cycle. The actual App foreground handler
        // verifies the session; do not edit React/controller state directly.
        const sessionPattern='**/api/v1/session';
        await control.evaluate(element=>{window.__t5ImportElement=element;});
        await page.route(sessionPattern,route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{code:'SERVICE_RECOVERING',retryable:true,details:{}}})}));
        try {
          await page.evaluate(()=>document.dispatchEvent(new Event('visibilitychange')));
          await page.locator('.service-state[data-state="degraded"]').waitFor();
          if(!await control.evaluate(element=>element===window.__t5ImportElement))throw Error('degradation remounted ImportControl');
          await control.getByTestId('import-stage').filter({hasText:'발행 완료'}).waitFor();
        } finally {await page.unroute(sessionPattern);}
        await page.evaluate(()=>document.dispatchEvent(new Event('visibilitychange')));
        await page.locator('.service-state[data-state="verified"]').waitFor();
        if(!await control.evaluate(element=>element===window.__t5ImportElement))throw Error('recovery remounted ImportControl');
        await control.getByTestId('import-stage').filter({hasText:'발행 완료'}).waitFor();
        const pinned=await current();
        const samePair=async()=>{if(await page.locator('#versions-from').inputValue()!==v.old.version_id||await page.locator('#versions-to').inputValue()!==v.new.version_id||await current()!==pinned)throw Error('resolved pair retargeted to published latest');};
        await samePair();await page.reload();await page.locator('#versions-from').waitFor();await samePair();
        await page.goBack();await page.locator('#reader-versions').waitFor();await page.goForward();await page.locator('#versions-from').waitFor();await samePair();
        return {receipt_visible:true,document:writes[0].body.document_id,captured_base:writes[0].body.base_version_id,real_conflict:true,fresh_base:winner.version_id,lost_response_original_key_reused:true,pair_frozen:true,actual_publication:true,phase_cycle_import_preserved:true,pinned_reload_and_history:true,fault:'test-owned second response loss and session 503; publication/conflict/receipt replay are real'};
      } finally {await page.unroute(pattern);}
    });
    await check('T5 stale same-page Copy action is cancelled when selected region changes',async()=>{
      const route=await page.evaluate(c=>`/documents/${c.target.document_id}/versions/${c.target.version_id}?`+new URLSearchParams({page:'a.html'})+'#region=retry',c);
      await page.goto(c.base+route);await page.frameLocator('iframe[title="결과 뷰어"]').getByRole('heading',{name:'F13 a',exact:true}).waitFor();
      await page.locator('#reader-context-toggle').click();
      await page.evaluate(()=>{window.__t5Copies=[];const original=navigator.clipboard.writeText.bind(navigator.clipboard);navigator.clipboard.writeText=value=>{window.__t5Copies.push(value);return original(value);};});
      let entered,release;const seen=new Promise(done=>entered=done),held=new Promise(done=>release=done);
      const pattern='**/api/v1/configuration';
      await page.route(pattern,async request=>{const response=await request.fetch();entered();await held;await request.fulfill({response});});
      try {
        await page.locator('#reader-copy-link').click();await seen;
        await page.locator('.region-outline').getByRole('button',{name:'both',exact:true}).click();
        const settled=page.waitForResponse(response=>response.url().endsWith('/api/v1/configuration'));
        release();await (await settled).finished();
        await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
        if(await page.getByRole('textbox',{name:'복사할 링크',exact:true}).count()||await page.evaluate(()=>window.__t5Copies.length))throw Error('old region Copy action survived its selection lifetime');
        return {unchanged_host_url:true,old_copy_cancelled:true,barrier:'test-owned delayed configuration and clipboard observer; actual mounted client/action'};
      } finally {release();await page.unroute(pattern);}
    });
    await check('T5 no-HTML directory remains addressable in actual version assessment scope',async()=>{
      const id=v.no_html;
      const assessment=page.waitForRequest(request=>request.url().includes('/versions/'+id.version_id+'/assessments'));
      await page.goto(c.base+'/documents/'+id.document_id+'/versions');
      await page.locator('table[aria-label="파일 차이"] tbody tr').filter({hasText:'readme.txt'}).waitFor();
      const request=await assessment;
      if(request.url().includes('scope_kind')||request.url().includes('page_path')||request.url().includes('region_id'))throw Error('no-HTML version invented a page scope');
      await page.locator('.source-panel h2').filter({hasText:'페이지 없음'}).waitFor();
      await page.goto(c.base+'/documents/'+id.document_id+'/versions/'+id.version_id);
      await page.getByText('페이지를 선택하거나 원본 파일을 내려받으세요.',{exact:true}).waitFor();
      await enter();await page.locator('#versions-return').click();
      await page.waitForFunction(()=>document.activeElement?.id==='reader-versions'&&history.state?.haeryongVersionsFocus===false);
      if(await page.locator('iframe[title="결과 뷰어"]').count())throw Error('no-HTML return invented a viewer');
      return {real_no_html_import:true,assessment_scope:'version',no_fabricated_page:true,frameless_return_focus:true,return_flag_consumed:true};
    });
    if(errors.length)throw Error('uncaught browser errors: '+JSON.stringify(errors));
    return {cases,errors,consoleMessages,requests,complete:cases.every(row=>row.ok),browser_plugin:'not available',scope:'T5 actual Chrome plus labelled barrier and emulated touch'};
  }
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
