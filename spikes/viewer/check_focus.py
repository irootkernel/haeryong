"""Focused current Task04 regression using owned Chrome or pinned Wails.

No provider, service restart, pairing approval, install or attachment download.
An authorized coordinator approves only an exact pending test request if needed.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import time
import urllib.request

p = argparse.ArgumentParser()
p.add_argument('--mode', choices=['chrome', 'native'], required=True)
p.add_argument('--endpoint', default='http://127.0.0.1:9270')
p.add_argument('--output', type=Path, required=True)
p.add_argument('--binary', type=Path, required=True)
p.add_argument('--profile', type=Path)
p.add_argument('--playwright-module')
p.add_argument('--state', type=Path)
p.add_argument('--ui-only', action='store_true')
p.add_argument('--recovery-only', action='store_true')
p.add_argument('--pathname-only', action='store_true')
p.add_argument('--required-pathname', action='store_true')
p.add_argument('--attempt', default='task04-rework-local-v4')
a = p.parse_args()
a.output.mkdir(mode=0o700, parents=True, exist_ok=True)
assert a.output.stat().st_mode & 0o777 == 0o700
with urllib.request.urlopen(a.endpoint+'/health', timeout=5) as response:
    baseline = json.load(response)

def stop(process):
    if process.poll() is None:
        if a.mode == 'chrome': os.killpg(process.pid, signal.SIGTERM)
        else: process.terminate()
        try: process.wait(timeout=8)
        except subprocess.TimeoutExpired: process.kill(); process.wait(timeout=5)

if a.mode == 'chrome':
    assert a.profile and a.profile.is_dir() and a.profile.stat().st_mode & 0o777 == 0o700
    assert a.playwright_module and Path(a.playwright_module).is_dir()
    (a.profile/'DevToolsActivePort').unlink(missing_ok=True)
    argv = [str(a.binary.resolve()), '--headless=new', '--user-data-dir='+str(a.profile.resolve()), '--remote-debugging-address=127.0.0.1', '--remote-debugging-port=0', '--no-first-run', '--no-default-browser-check', 'about:blank']
else:
    assert a.state and a.state.is_dir() and a.state.stat().st_mode & 0o777 == 0o700
    before = set(a.state.glob('report-*.json'))
    argv = [str(a.binary.resolve()), '--base', a.endpoint, '--state', str(a.state.resolve()), *(['--lifecycle','required-pathname'] if a.required_pathname else ['--pathname-only'] if a.pathname_only else ['--recovery-only'] if a.recovery_only else ['--focus', *(['--focus-ui-only'] if a.ui_only else [])]), '--close-after', '90s' if a.recovery_only or a.pathname_only or a.required_pathname else '60s']
(a.output/'launch.json').write_text(json.dumps({'argv':argv,'binary_sha256':hashlib.sha256(a.binary.read_bytes()).hexdigest(),'daemon':baseline},indent=2)+'\n')
with (a.output/'process-original.log').open('w') as log:
    process = subprocess.Popen(argv, stdout=log, stderr=subprocess.STDOUT, start_new_session=a.mode=='chrome')
    launched_at = time.monotonic()
    node = None
    try:
        if a.mode == 'chrome':
            deadline = time.monotonic()+20
            while not (a.profile/'DevToolsActivePort').exists():
                assert process.poll() is None, 'owned Chrome exited before CDP'
                if time.monotonic()>deadline: raise TimeoutError('owned CDP startup')
                time.sleep(.1)
            port = int((a.profile/'DevToolsActivePort').read_text().splitlines()[0])
            script = a.output/'driver.js'
            script.write_text("const fs=require('fs');const {chromium}=require(process.argv[2]);(async()=>{const browser=await chromium.connectOverCDP('http://127.0.0.1:'+process.argv[3]);try{const page=await browser.contexts()[0].newPage();page.setDefaultTimeout(180000);await page.addInitScript(origin=>{if(location.origin===origin)localStorage.setItem('haeryong.viewer.requested','1');},"+json.dumps(a.endpoint)+");await page.goto("+json.dumps(a.endpoint+'/?attempt='+a.attempt)+");await page.waitForFunction(()=>!!window.fixtureConfig,null,{timeout:180000});await page.evaluate(()=>fixtureApi('/viewer/observations'));await page.waitForTimeout(500);const explicitRun=await page.evaluate(()=>({views:fixtureViews.size,phase:window.fixturePhase||null,full_report:!!window.fixtureReport,run_enabled:!document.querySelector('#run').disabled}));fs.writeFileSync("+json.dumps(str(a.output/'explicit-run-observation.json'))+",JSON.stringify(explicitRun,null,2));if(explicitRun.views!==0||explicitRun.phase!==null||explicitRun.full_report||!explicitRun.run_enabled)throw Error('Remembered old Run flag started unsolicited checks');await page.goto("+json.dumps(a.endpoint+ '/?attempt='+a.attempt+'&page=pages%2Fscenario.html')+");try{await page.waitForFunction(()=>!!window.fixtureConfig,null,{timeout:180000});await page.waitForFunction(()=>!!window.fixtureViews?.get('a')?.state?.scenario?.worker,null,{timeout:30000});}catch(error){const readiness=await page.evaluate(()=>({config_ready:!!window.fixtureConfig,phase:window.fixturePhase||null,status:document.querySelector('#status')?.textContent,pairing_pending:document.querySelector('#pairing')?.textContent.includes('awaiting local agent approval')||false}));fs.writeFileSync("+json.dumps(str(a.output/'readiness-failure.json'))+",JSON.stringify(readiness,null,2));throw error;}const report=await page.evaluate(uiOnly=>fixtureFocus({ui:true,ui_only:uiOnly,ended_access:true}),"+json.dumps(a.ui_only)+");report.explicit_run_guard=explicitRun;fs.writeFileSync("+json.dumps(str(a.output/'focused-result.json'))+",JSON.stringify(report,null,2));if(report.status!=='pass')throw Error(JSON.stringify(report));await page.screenshot({path:"+json.dumps(str(a.output/'focused-final.png'))+",fullPage:true});console.log(JSON.stringify({status:report.status,rows:report.rows.length,implementation:report.implementation}));}finally{await browser.close();}})().catch(e=>{console.error(e.stack);process.exitCode=1;});")
            if a.recovery_only or a.pathname_only or a.required_pathname:
                driver = r'''const fs=require('fs');const {chromium}=require(process.argv[2]);
(async()=>{const browser=await chromium.connectOverCDP('http://127.0.0.1:'+process.argv[3]);try{const page=await browser.contexts()[0].newPage();page.setDefaultTimeout(120000);const observedErrors=[];page.on('pageerror',error=>observedErrors.push({message:String(error),stack:error.stack||''}));await page.goto(ENTRY);await page.waitForFunction(()=>!!window.fixtureRecoveryReport,null,{timeout:120000});const observed=await page.evaluate(()=>({report:fixtureRecoveryReport,renderers:fixtureConfig.renderers}));const report=observed.report;const authored=entry=>entry.message==='Error: Intentional authored-script failure'&&observed.renderers.some(origin=>entry.stack.includes(origin+'/pages/broken.html'));const errors=observedErrors.filter(entry=>!authored(entry));report.observed_errors=observedErrors;report.authored_errors=observedErrors.filter(authored);report.trusted_errors=errors;if(report.status==='pass'){const expected=await page.evaluate(()=>({state:fixtureViews.get('b').state,owner:fixtureRecoveryReport.owner}));const [logical]=await Promise.all([page.waitForEvent('popup'),page.locator('#logical-tab').click()]);await logical.waitForFunction(()=>window.fixtureViews?.get('b')?.state?.scenario?.worker,null,{timeout:30000});const proof=await logical.evaluate(async()=>({state:fixtureViews.get('b').state,owner:await fixtureApi('/session'),read:await fixtureCommand('b','read-status'),url:location.href}));const originalOwner=await page.evaluate(()=>fixtureApi('/session'));if(proof.state.page_path!==expected.state.page_path||proof.state.page_query!==expected.state.page_query||proof.state.page_fragment!==expected.state.page_fragment||proof.state.scenario.worker.version!=='b'||proof.read.status!==200||JSON.stringify(proof.owner)!==JSON.stringify(originalOwner))throw Error('Actual logical B tab changed ordinary history selection/owner');await logical.close();report.rows.push({name:'ordinary-history-browser-logical-tab',status:'pass',value:proof});const ended=await page.evaluate(()=>pendingOwnerEndProof());report.rows.push({name:'failed-refresh-owner-end',status:'pass',value:ended});}fs.writeFileSync(RESULT,JSON.stringify(report,null,2));await page.screenshot({path:SCREENSHOT,fullPage:true});if(report.status!=='pass'||errors.length)throw Error(JSON.stringify(report));console.log(JSON.stringify({status:report.status,rows:report.rows.length,implementation:report.implementation}));}finally{await browser.close();}})().catch(error=>{console.error(error.stack);process.exitCode=1;});'''
                driver=driver.replace('ENTRY',json.dumps(a.endpoint+'/?attempt='+a.attempt+'&recovery_focus=1&ir2_focus=1&version=a&page=pages%2Fnested%2Fdetail.html&page_query=loading%3Dexact%2520selection&page_fragment=step-2&sentinel=retain#host-fragment')).replace('RESULT',json.dumps(str(a.output/'focused-result.json'))).replace('SCREENSHOT',json.dumps(str(a.output/'focused-final.png')))
                if a.pathname_only or a.required_pathname:
                    entry=a.endpoint+'/?attempt='+a.attempt+('&lifecycle=required-pathname' if a.required_pathname else '&pathname_focus=1')+'&sentinel=retain#host-fragment'
                    old_entry=a.endpoint+'/?attempt='+a.attempt+'&recovery_focus=1&ir2_focus=1&version=a&page=pages%2Fnested%2Fdetail.html&page_query=loading%3Dexact%2520selection&page_fragment=step-2&sentinel=retain#host-fragment'
                    driver=driver.replace(json.dumps(old_entry),json.dumps(entry)).replace('fixtureRecoveryReport','fixtureRequiredPathnameReport' if a.required_pathname else 'fixturePathnameReport').replace("get('b')","get('a')").replace("fixtureCommand('b','read-status')","fixtureCommand('a','read-status')").replace("worker.version!=='b'","worker.version!=='a'").replace('ordinary-history-browser-logical-tab','required-pathname-browser-current-logical-tab' if a.required_pathname else 'pathname-browser-current-logical-tab')
                script.write_text(driver)
            with (a.output/'driver-original.log').open('w') as driver_log:
                node = subprocess.Popen(['node',str(script),a.playwright_module,str(port)], stdout=driver_log, stderr=subprocess.STDOUT, env=os.environ|{'TMPDIR':str(a.output)})
                try: code = node.wait(timeout=240)
                except subprocess.TimeoutExpired: node.kill(); node.wait(); raise
            assert code == 0, 'focused Chrome driver failed; retain original log/result'
        else:
            deadline = time.monotonic()+(85 if a.recovery_only or a.pathname_only or a.required_pathname else 55)
            found = None
            while time.monotonic()<deadline:
                assert process.poll() is None, 'native exited before focused report'
                for file in set(a.state.glob('report-*.json'))-before:
                    report = json.loads(file.read_text())
                    if report.get('kind')==('required-pathname-closing' if a.required_pathname else 'pathname-transition-recovery' if a.pathname_only else 'current-review-recovery' if a.recovery_only else 'focused-rework') and 'wails.io' in report.get('user_agent',''):
                        found = file; break
                if found: break
                time.sleep(.2)
            assert found, 'native focused report missing'
            (a.output/'focused-result.json').write_bytes(found.read_bytes())
            # Preserve semantic failure before independently observing the scheduled normal close.
            terminal = {'pid':process.pid, 'semantic_status':report['status'], 'normal_exit_observed':False}
            try:
                terminal['exit_code'] = process.wait(timeout=max(1, launched_at+(98 if a.recovery_only or a.pathname_only or a.required_pathname else 68)-time.monotonic()))
                terminal['normal_exit_observed'] = True
            except subprocess.TimeoutExpired:
                terminal['cleanup_error'] = 'scheduled normal close was not observed before existing deadline'
                (a.output/'native-process-observation.json').write_text(json.dumps(terminal,indent=2)+'\n')
                raise
            receipt=a.state/f'native-close-{process.pid}.json'
            terminal['window_close_receipt_present'] = receipt.is_file()
            (a.output/'native-process-observation.json').write_text(json.dumps(terminal,indent=2)+'\n')
            if receipt.is_file(): (a.output/'normal-close-receipt.json').write_bytes(receipt.read_bytes())
            assert terminal['exit_code']==0 and terminal['window_close_receipt_present'], 'owned native normal cleanup failed'
            assert report['status']=='pass', json.dumps(report)
        report = json.loads((a.output/'focused-result.json').read_text())
        assert report['implementation']==baseline['implementation'] and report['instance']==baseline['instance']
        print(json.dumps({'status':'pass','mode':a.mode,'rows':len(report['rows']),'result':str(a.output/'focused-result.json')}))
    finally:
        if node and node.poll() is None: node.kill(); node.wait()
        stop(process)
