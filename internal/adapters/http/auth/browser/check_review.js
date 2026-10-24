async page => {
  let stage="open";
  try {
  const route = INPUT.route, version = INPUT.version;
  await page.goto(route);
  await page.locator('#review-start').waitFor();
  const read = () => page.evaluate(async version => {
    const response = await fetch('/api/v1/versions/' + version + '/review', {headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1')}});
    if(!response.ok)throw Error('review read '+response.status);return (await response.json()).data;
  }, version);
  await page.frameLocator('iframe').locator('#review-fixture').waitFor();
  const frame = page.frames().find(frame => frame !== page.mainFrame());
  if(!frame)throw Error('Reader frame not ready');
  await frame.waitForSelector('#review-fixture');
  await frame.evaluate(() => {
    scrollTo(0,500);
    parent.postMessage({type:'review.complete',state:'reviewed'},'*');
    parent.postMessage({type:'assessment.updated',state:'minor_change',reviewed:true},'*');
  });
  await page.frameLocator('iframe').locator('#next').click();
  await page.frameLocator('iframe').locator('#second-review-page').waitFor();
  const before=await read();if(before.exists||before.value.state!=='unreviewed')throw Error('Reading/frame/assessment completed Review');
  stage="keyboard start";
  await page.locator('#review-start').focus();await page.keyboard.press('Enter');
  await page.waitForFunction(()=>document.querySelector('#review-state')?.textContent?.startsWith('검토 중'));
  stage="touch complete";
  await page.setViewportSize({width:390,height:844});
  const button=page.locator('#review-complete');await button.scrollIntoViewIfNeeded();
  const box=await button.boundingBox();if(!box||box.width<44||box.height<44)throw Error('Review touch target too small');
  const cdp=await page.context().newCDPSession(page);
  await cdp.send('Emulation.setTouchEmulationEnabled',{enabled:true});
  await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x:box.x+box.width/2,y:box.y+box.height/2}]});
  await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});
  await cdp.detach();
  await page.waitForFunction(()=>document.querySelector('#review-state')?.textContent?.startsWith('검토 완료'));
  const saved=await read();if(saved.value.state!=='reviewed'||!saved.value.reviewed_at)throw Error('Explicit touch completion not saved');
  stage="history";
  await page.getByRole('button',{name:'검토 이력',exact:true}).click();
  await page.getByText('unreviewed → reviewing',{exact:false}).waitFor();
  await page.getByText('reviewing → reviewed',{exact:false}).waitFor();
  return {version,before,saved,keyboardStart:true,touchCompletion:true,frameRejected:true,syntheticAssessmentRejected:true,multiPageReadingDidNotComplete:true,viewport:{width:390,height:844}};
  } catch(error) { throw Error('Review '+stage+': '+error.message+'; control: '+await page.locator('.review-control').innerText().catch(()=>'<unavailable>')); }
}
