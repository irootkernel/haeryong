async page => {
  const input=INPUT;
  await page.setViewportSize({width:1280,height:900});
  await page.goto(input.origin+'/documents/'+input.document+'/versions');
  const old=page.locator('[data-review-version="'+input.before+'"]'),fresh=page.locator('[data-review-version="'+input.after+'"]');
  await old.filter({hasText:'검토 완료'}).waitFor();await fresh.filter({hasText:'미검토'}).waitFor();
  if(await page.locator('#versions-from').inputValue()!==input.before||await page.locator('#versions-to').inputValue()!==input.after)throw Error('Last Reviewed default pair lost');
  await page.goto(input.origin+'/');
  const updated=page.locator('.home-section').filter({has:page.getByRole('heading',{name:'검토한 이후 업데이트됨',exact:true})});
  await updated.locator('a[href*="'+input.after+'"]').waitFor();
  // Continue can point at an older in-progress version after publication of latest.
  const result=await page.evaluate(async input=>{
    const headers={Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1')};
    const get=await fetch('/api/v1/versions/'+input.before+'/review',{headers});const current=(await get.json()).data;
    const response=await fetch('/api/v1/versions/'+input.before+'/review',{method:'PUT',headers:{...headers,'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID(),'If-Match':current.etag},body:JSON.stringify({action:'reopen',state:'reviewing'})});
    if(!response.ok)throw Error('owner fixture reopen failed');return (await response.json()).data;
  },input);
  const continuing=page.locator('.home-section').filter({has:page.getByRole('heading',{name:'검토 이어가기',exact:true})});
  await continuing.locator('a[href*="'+input.before+'"]').waitFor();
  return {oldVersion:input.before,newVersion:input.after,oldReviewedNewUnreviewed:true,lastReviewedDefault:true,updatedLatestLink:true,olderContinueLink:true,reopened:result.value.state};
}
