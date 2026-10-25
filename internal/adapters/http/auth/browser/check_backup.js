async page=>{
 const input=INPUT;
 await page.goto(input.origin+'/health/live');
 await page.evaluate(token=>localStorage.setItem('haeryong.owner-session.v1',token),input.token);
 await page.goto(input.origin+'/settings/backup');
 await page.getByRole('heading',{name:'백업 및 내보내기',exact:true}).waitFor();
 const statusBox=page.locator('[data-backup-status]');await statusBox.waitFor();
 const status=await page.evaluate(async()=>{const r=await fetch('/api/v1/status',{headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1')}});return (await r.json()).data.backup;});
 const text=await statusBox.innerText();
 if(input.phase==='absent'&&(!text.includes('아직 생성')||status.last_complete_verified!==null))throw Error('absent status mismatch');
 if(['complete','failed','unavailable'].includes(input.phase)&&(!status.last_complete_verified||!text.includes(status.last_complete_verified.set_id)))throw Error('real complete set missing');
 if(input.phase==='failed'&&(status.last_attempt.state!=='failed'||!text.includes('실패')))throw Error('replacement failure hidden');
 if(input.phase==='unavailable'&&(status.available||!text.includes('위치를 사용할 수 없습니다')))throw Error('unavailable destination hidden');
 const trial=input.phase==='trial-passed'?'passed':input.phase==='trial-failed'?'failed':'not_run';
 if(status.last_trial_restore.state!==trial||!text.includes(trial==='passed'?'실제 복원 검증: 통과':trial==='failed'?'실제 복원 검증: 실패':'실행하지 않음'))throw Error('actual trial observation mismatch');
 if(!(await page.locator('[data-backup-screen]').innerText()).includes('호스트의 로컬 CLI'))throw Error('host-local instruction absent');
 const button=page.getByRole('button',{name:'백업 상태 새로 고침'});await button.focus();await page.keyboard.press('Enter');await button.waitFor({state:'visible'});await page.waitForFunction(()=>!document.querySelector('[data-backup-screen] button').disabled);
 const context=await page.context().browser().newContext({hasTouch:true,viewport:{width:390,height:844}});
 try{
  await context.addInitScript(({token,origin})=>{if(window===top&&location.origin===origin)localStorage.setItem('haeryong.owner-session.v1',token)},input);
  const phone=await context.newPage();await phone.goto(input.origin+'/settings/backup');await phone.locator('[data-backup-status]').waitFor();
  const refresh=phone.getByRole('button',{name:'백업 상태 새로 고침'});await refresh.tap();await phone.waitForFunction(()=>!document.querySelector('[data-backup-screen] button').disabled);
  await phone.reload();await phone.locator('[data-backup-status]').waitFor();
  if(await phone.locator('[data-backup-screen]').evaluate(e=>e.scrollWidth>innerWidth))throw Error('backup instructions overflow touch viewport');
  if((await phone.locator('[data-backup-status]').innerText())!==text)throw Error('status changed across touch/reload');
  return {phase:input.phase,keyboardRefresh:true,hasTouch:true,tapRefresh:true,reloadResync:true,trial,completeSet:status.last_complete_verified?.set_id??null,hostLocalInstructions:true};
 }finally{await context.close()}
}
