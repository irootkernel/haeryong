"""Verify the viewer using an installed Playwright module and an owned profile.

The Browser plugin is unavailable. The CLI's automatic download hook terminated
its daemon on canceled downloads, so this checker uses the same installed
Playwright package directly. It installs nothing and makes no physical claim.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import signal
import time
from check_support import restart_daemon

p = argparse.ArgumentParser()
p.add_argument('--playwright-module', required=True)
p.add_argument('--profile',type=Path,required=True)
p.add_argument('--headed',action='store_true')
p.add_argument('--chrome-binary',type=Path,required=True)
p.add_argument('--daemon-binary',type=Path,required=True)
p.add_argument('--endpoint', required=True)
p.add_argument('--output', type=Path, required=True)
p.add_argument('--state', type=Path, required=True)
a = p.parse_args()
a.output.mkdir(mode=0o700, parents=True, exist_ok=True)
assert a.output.stat().st_mode & 0o777 == 0o700
assert a.profile.is_dir() and a.profile.stat().st_mode & 0o777 == 0o700
env = os.environ | {'TMPDIR': str(a.output)}

script = r'''async page => {
 const endpoint=ENDPOINT, output=OUTPUT;
 const assert=(v,m)=>{if(!v)throw new Error(m);};

 await page.unrouteAll({behavior:'ignoreErrors'});const hostErrors=[];page.on('pageerror',e=>hostErrors.push(String(e)));
 await page.goto(endpoint+'/?attempt=checked-browser-final&autorun=1');
 const popupGesture=(async()=>{await page.waitForFunction(()=>window.fixturePhase==='unprivileged-authored-popup',null,{timeout:60000});const authored=page.frames().find(f=>f.url().includes('/pages/nested/detail.html'));await authored.getByRole('link',{name:'Verify authored popup boundary',exact:true}).click();})();
 await page.waitForFunction(()=>!!window.fixtureReport,null,{timeout:150000});await popupGesture;
 for(const popup of page.context().pages())if(popup!==page)await popup.close();await page.bringToFront();
 const original=await page.evaluate(()=>fixtureReport);const originalDevice=await page.evaluate(()=>fixtureApi('/session'));
 assert(original.results.every(r=>r.status==='pass'),'self-running viewer reported failures: '+JSON.stringify(original.results.filter(r=>r.status!=='pass')));
 assert(await page.title()==='Haeryong viewer proof','wrong page');
 assert((await page.locator('h1').first().innerText())==='Haeryong viewer proof','blank host');
 let frame=page.frames().find(f=>f.url().includes('/pages/nested/detail.html'));
 assert(frame,'nested frame missing');
 await frame.locator('#budget').focus();await page.keyboard.press('ControlOrMeta+A');await page.keyboard.type('9');await page.keyboard.press('Tab');await page.keyboard.press('Enter');
 const keyboard=await frame.evaluate(()=>({focus:document.activeElement.id,budget:document.querySelector('#budget').value}));assert(keyboard.focus==='simulate'&&keyboard.budget==='9','Keyboard action lost focus/input');
 await frame.waitForFunction(()=>document.querySelector('#result').textContent.includes('18 retry units'));
 await frame.waitForFunction(()=>window.fixtureScenario.network!=='pending');
 const network=await frame.evaluate(()=>fixtureScenario.network);assert(network==='pass','ordinary external framework failed');
 await page.screenshot({path:output+'/browser-interaction.png'});
 // A read of an absent database must not create an empty v1 that breaks the next write.
 await frame.evaluate(()=>new Promise((resolve,reject)=>{const r=indexedDB.deleteDatabase('author-fixture');r.onsuccess=resolve;r.onerror=()=>reject(r.error);}));
 const absent=await page.evaluate(()=>fixtureCommand('a','storage-read'));
 const databases=await frame.evaluate(()=>indexedDB.databases());assert(absent.indexedDB===null&&!databases.some(db=>db.name==='author-fixture'),'Read-only absent IDB probe committed an empty database');
 const fresh=await page.evaluate(()=>fixtureCommand('a','storage-write',{value:'new-idb-proof'}));assert(fresh.indexedDB==='new-idb-proof','Write after absent read failed');
 await frame.evaluate(()=>new Promise((resolve,reject)=>{const r=indexedDB.deleteDatabase('author-fixture');r.onsuccess=resolve;r.onerror=()=>reject(r.error);}));
 await frame.evaluate(()=>new Promise((resolve,reject)=>{const r=indexedDB.open('author-fixture',1);r.onsuccess=()=>{r.result.close();resolve();};r.onerror=()=>reject(r.error);}));
 const upgraded=await page.evaluate(()=>fixtureCommand('a','storage-write',{value:'legacy-empty-idb-proof'}));assert(upgraded.indexedDB==='legacy-empty-idb-proof','Owned empty IDB did not upgrade/create its store');
 const idbProbe={absent_read:absent,no_db_created:true,write_after_absent:fresh,legacy_upgrade:upgraded};
 const nav={path:'pages/nested/detail.html',query:'scenario=partial%20retry',fragment:'step-2'};
 const reloadInstance=await page.evaluate(()=>fixtureViews.get('a').binding.render_instance_id);await page.getByRole('button',{name:'Reload version A',exact:true}).click();
 await page.waitForFunction(id=>fixtureViews.get('a')?.binding?.render_instance_id!==id&&fixtureViews.get('a')?.state?.page_fragment==='step-2',reloadInstance);frame=page.frames().find(f=>f.url().includes('/pages/nested/detail.html'));
 assert(await page.evaluate(()=>fixtureViews.get('a').state.page_query)===nav.query,'reload lost query');
 await page.evaluate(()=>fixtureCommand('a','history',{path:'/pages/nested/detail.html?scenario=client#client-fragment'}));
 await page.waitForFunction(()=>fixtureViews.get('a').state.page_fragment==='client-fragment');
 await frame.evaluate(()=>history.back());
 await page.waitForFunction(()=>fixtureViews.get('a').state.page_fragment==='step-2');
 const [logical]=await Promise.all([page.waitForEvent('popup'),page.locator('#logical-tab').click()]);
 await logical.waitForFunction(()=>window.fixtureViews?.get('a')?.state?.page_fragment==='step-2');
 const logicalState=await logical.evaluate(()=>fixtureViews.get('a').state);assert(logicalState.page_path==='pages/nested/detail.html'&&logicalState.page_query==='scenario=partial%20retry'&&logicalState.page_fragment==='step-2','logical new-tab lost query');await logical.close();
 const [middle]=await Promise.all([page.context().waitForEvent('page'),frame.getByRole('link',{name:'Back to simulation',exact:true}).click({button:'middle'})]);
 await middle.waitForFunction(()=>window.fixtureScenario?.worker);const middleState=await middle.evaluate(()=>({page_path:location.pathname.slice(1),page_query:location.search.slice(1),page_fragment:location.hash.slice(1),scenario:fixtureScenario}));assert(middleState.page_path==='pages/scenario.html'&&middleState.page_query==='from=nested'&&middleState.page_fragment==='result'&&middleState.scenario.version==='a'&&middleState.scenario.worker.version==='a','Authored middle-click changed immutable selection');await middle.close();await page.bringToFront();
 const stale=await page.evaluate(()=>({binding:fixtureViews.get('a').binding.render_instance_id,rejected:fixtureViews.get('a').rejectedNotices||0}));
 await frame.evaluate(()=>parent.postMessage({type:'viewer.loaded',page_path:'stale/queued.html'},'*'));
 await page.waitForFunction(count=>(fixtureViews.get('a').rejectedNotices||0)>count,stale.rejected);assert(await page.evaluate(()=>fixtureViews.get('a').binding.render_instance_id)===stale.binding,'stale notice replaced current document');
 const acceptedBefore=await page.evaluate(()=>fixtureViews.get('a').channelEvents.length);
 await frame.evaluate(()=>parent.postMessage({type:'viewer.loaded',page_path:'pages/scenario.html'},'*'));
 await page.waitForFunction(count=>fixtureViews.get('a').channelEvents.length>count,acceptedBefore);
 const accepted=await page.evaluate(count=>fixtureViews.get('a').channelEvents.slice(count),acceptedBefore);assert(accepted.every(e=>e.page_path==='pages/nested/detail.html'),'queued valid old page admitted');
 const beforeNull=await page.evaluate(()=>fixtureViews.get('a').rejectedNotices||0);
 await frame.evaluate(()=>{window.savedPortPost=MessagePort.prototype.postMessage;MessagePort.prototype.postMessage=function(m,...rest){return savedPortPost.call(this,m?.type==='viewer.ready'?{...m,payload:null}:m,...rest);};parent.postMessage({type:'viewer.loaded',page_path:location.pathname.slice(1)},'*');});
 await page.waitForFunction(n=>(fixtureViews.get('a').rejectedNotices||0)>n,beforeNull);
 const beforeRecovery=await page.evaluate(()=>fixtureViews.get('a').channelEvents.length);await frame.evaluate(()=>{MessagePort.prototype.postMessage=savedPortPost;parent.postMessage({type:'viewer.loaded',page_path:location.pathname.slice(1)},'*');});
 await page.waitForFunction(n=>fixtureViews.get('a').channelEvents.length>n&&fixtureViews.get('a').state?.scenario?.worker,beforeRecovery);
 const replay=await page.evaluate(async()=>{const transfer_id=crypto.randomUUID(),args={transfer_id,bytes:fixtureConfig.context_bytes,digest:fixtureConfig.context_digest};const first=await fixtureCommand('a','context',args);const next=await fixtureCommand('a','context',args);return {same_ref:first.context_ref===next.context_ref,resumed_offset:next.exact_bytes,replay_transferred_buffers:next.transferred_buffers};});assert(replay.same_ref&&replay.replay_transferred_buffers===0,'context begin replay lost offset/sealed reference');
 // Hold actual seal completion across a real document navigation.
 let held,release;const heldReady=new Promise(r=>held=r);const gate=new Promise(r=>release=r);
 await page.route('**/viewer/context?id=*',async route=>{if(route.request().method()==='PATCH'){held();await gate;await route.continue();}else await route.continue();});
 await page.evaluate(()=>{window.raceResult=fixtureCommand('a','context',{bytes:fixtureConfig.context_bytes,digest:fixtureConfig.context_digest}).then(()=>({unexpected_ready:true}),error=>({interrupted:String(error)}));});
 await heldReady;
 await frame.goto(frame.url().split('/pages/')[0]+'/pages/scenario.html?race=new#result');
 await page.waitForFunction(()=>fixtureViews.get('a')?.state?.page_query==='race=new');release();
 await page.waitForTimeout(250);
 const race=await page.evaluate(async()=>({result:await raceResult,sealed:fixtureViews.get('a').sealedContext,refs:fixtureViews.get('a').sealedRefs.size,page:fixtureViews.get('a').state.page_path}));
 assert(!race.result.unexpected_ready&&race.refs===0&&race.sealed===null,'old seal attached to new document');await page.unroute('**/viewer/context?id=*');
 const brokenRecovery=await page.evaluate(()=>fixtureBrokenRecovery());
 const health=await page.evaluate(()=>fetch('/health').then(r=>r.json()));assert(health.review===false,'recovery changed Review');
 const cdp=await browser.newBrowserCDPSession();await cdp.send('Browser.setDownloadBehavior',{behavior:'allowAndName',downloadPath:output+'/downloads',eventsEnabled:true});
 let resolveBegin,resolveEnd,guid;const begin=new Promise(r=>resolveBegin=r),end=new Promise(r=>resolveEnd=r);cdp.on('Browser.downloadWillBegin',event=>{guid=event.guid;resolveBegin({...event,observed_at:Date.now()});});cdp.on('Browser.downloadProgress',event=>{if(event.guid===guid&&['completed','canceled'].includes(event.state))resolveEnd({...event,observed_at:Date.now()});});
 await page.getByRole('button',{name:'Stream attachment download',exact:true}).click();const started=await Promise.race([begin,new Promise((_,reject)=>setTimeout(()=>reject(Error('No actual download start')),15000).unref())]);
 await page.getByRole('button',{name:'Close version A',exact:true}).click();const closedAt=Date.now();
 const finished=await Promise.race([end,new Promise((_,reject)=>setTimeout(()=>reject(Error('No actual download completion')),80000).unref())]);assert(finished.state==='completed'&&started.observed_at<closedAt&&closedAt<finished.observed_at,'Download/actual close order failed');
 const observations=await page.evaluate(()=>fixtureApi('/viewer/observations'));
 assert(!observations.requests.some(r=>r.Path.startsWith('/unused/')),'eager loading fetched unused entries');
 assert(observations.requests.some(r=>r.Kind==='sender-denied'&&r.Site==='same-site'),'cross-port denial not corroborated by server');
 const browserVersion=browser.version();
 return {passed:true,endpoint,browser:browserVersion,keyboard,middleState,idbProbe,download_events:{started,closedAt,finished},originalDevice,original,network,logicalState,race,brokenRecovery,health,hostErrors,observations,download:'downloads/'+guid,fixture_only:true};
}'''
script = script.replace('ENDPOINT', json.dumps(a.endpoint)).replace('OUTPUT', json.dumps(str(a.output.resolve())))
runner = a.output/'browser-check-driver.js'
runner.write_text("const fs=require('fs');const {chromium}=require(process.argv[2]);\n"+
 "(async()=>{const browser=await chromium.connectOverCDP('http://127.0.0.1:'+process.argv[3]);try{const context=browser.contexts()[0],page=await context.newPage();for(const old of context.pages())if(old!==page)await old.close();await context.clearCookies({name:/^hy_view_/});const report=await ("+script+")(page);fs.writeFileSync("+json.dumps(str(a.output/'browser-ui-result.json'))+",JSON.stringify(report,null,2));await page.goto("+json.dumps(a.endpoint)+"+'/?attempt=checked-browser-final&lifecycle=write');await page.waitForFunction(()=>window.fixturePhase==='waiting-for-actual-daemon-restart',null,{timeout:30000});fs.writeFileSync("+json.dumps(str(a.output/'restart-ready.json'))+",JSON.stringify({actual_selected_views:true}));await page.waitForFunction(()=>!!window.fixtureLifecycleReport,null,{timeout:90000});const lifecycle=await page.evaluate(()=>fixtureLifecycleReport);if(lifecycle.status!=='pass')throw Error(JSON.stringify(lifecycle));report.active_daemon_restart=lifecycle;fs.writeFileSync("+json.dumps(str(a.output/'browser-result-before-restart.json'))+",JSON.stringify(report,null,2));}finally{await browser.close();}})().catch(error=>{console.error(error.stack);process.exitCode=1;});\n")


def launch():
    (a.profile/'DevToolsActivePort').unlink(missing_ok=True)
    argv=[str(a.chrome_binary.resolve()),*([] if a.headed else ['--headless=new']),'--user-data-dir='+str(a.profile.resolve()),'--remote-debugging-address=127.0.0.1','--remote-debugging-port=0','--no-first-run','--no-default-browser-check','about:blank']
    (a.output/'chrome-launch.json').write_text(json.dumps({'argv':argv},indent=2))
    log=(a.output/'chrome-original.log').open('ab');process=subprocess.Popen(argv,stdout=log,stderr=subprocess.STDOUT,start_new_session=True);log.close()
    deadline=time.monotonic()+20
    while not(a.profile/'DevToolsActivePort').exists():
        assert process.poll() is None,'owned Chrome exited before CDP startup'
        if time.monotonic()>deadline:raise RuntimeError('Owned CDP startup timed out')
        time.sleep(.1)
    return process,int((a.profile/'DevToolsActivePort').read_text().splitlines()[0])


def stop(process):
    if process.poll() is None:
        os.killpg(process.pid,signal.SIGTERM)
        try:process.wait(timeout=10)
        except subprocess.TimeoutExpired:os.killpg(process.pid,signal.SIGKILL);process.wait(timeout=10)


process,port=launch();node=None
try:
    with(a.output/'browser-driver-original.txt').open('w')as log:
        node=subprocess.Popen(['node',str(runner),a.playwright_module,str(port)],env=env,cwd=a.output,stdout=log,stderr=subprocess.STDOUT)
        deadline=time.monotonic()+300;restarted=False
        while node.poll()is None:
            if (a.output/'restart-ready.json').exists()and not restarted:
                restart_daemon(a.daemon_binary,a.state,a.output);restarted=True
            if time.monotonic()>deadline:raise RuntimeError('Bounded full browser check timed out')
            time.sleep(.2)
        assert node.returncode==0,'browser checker failed; inspect browser-driver-original.txt'
    stop(process);closed_pid=process.pid
    report=json.loads((a.output/'browser-result-before-restart.json').read_text())
    process,port=launch()
    restart_script=a.output/'browser-restart-driver.js'
    restart_script.write_text("const fs=require('fs');const {chromium}=require(process.argv[2]);(async()=>{const browser=await chromium.connectOverCDP('http://127.0.0.1:'+process.argv[3]);try{const page=await browser.contexts()[0].newPage();await page.goto("+json.dumps(a.endpoint)+"+'/?attempt=checked-browser-final&lifecycle=read');await page.waitForFunction(()=>!!window.fixtureLifecycleReport,null,{timeout:40000});const r=await page.evaluate(()=>fixtureLifecycleReport);if(r.status!=='pass')throw Error(JSON.stringify(r));fs.writeFileSync("+json.dumps(str(a.output/'browser-restart-result.json'))+",JSON.stringify(r,null,2));}finally{await browser.close();}})().catch(e=>{console.error(e.stack);process.exitCode=1;});")
    with(a.output/'browser-restart-original.txt').open('w')as log:result=subprocess.run(['node',str(restart_script),a.playwright_module,str(port)],stdout=log,stderr=subprocess.STDOUT,timeout=60)
    assert result.returncode==0,'actual browser restart failed'
    report['actual_browser_restart']={'closed_pid':closed_pid,'new_pid':process.pid,'original':json.loads((a.output/'browser-restart-result.json').read_text())}
    inventory=json.loads((a.state/'inventory-a.json').read_text());expected=inventory['attachment.bin'];file=a.output/report['download']
    with file.open('rb')as stream:actual=hashlib.file_digest(stream,'sha256').hexdigest()
    assert file.stat().st_size==expected['bytes']and actual==expected['sha256']
    report['download_verification']={'bytes':file.stat().st_size,'sha256':actual,'matches_immutable_inventory':True}
    report['input_entries_per_version']=len(inventory);report['input_bytes_per_version']=sum(v['bytes']for v in inventory.values())
    (a.output/'browser-result.json').write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps({'status':'pass','browser':report['browser'],'checks':len(report['original']['results']),'output':str(a.output/'browser-result.json'),'physical_device_claim':False}))
finally:
    if node and node.poll()is None:node.terminate();node.wait(timeout=10)
    stop(process)
