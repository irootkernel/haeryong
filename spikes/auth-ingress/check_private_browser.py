"""Check an already-running, explicitly selected private HTTPS auth fixture.

Normal TLS validation; no network setup, physical-peer claim or provider calls.
Only exact requests created by this checker are approved. No credentials printed.
"""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
from urllib.parse import urlparse
import uuid

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--fixture-binary', type=Path, required=True)
parser.add_argument('--runtime-directory', type=Path, required=True)
parser.add_argument('--browser-cli', type=Path, required=True)
parser.add_argument('--local-origin', required=True)
parser.add_argument('--remote-origin', required=True)
options = parser.parse_args()
local, remote = urlparse(options.local_origin), urlparse(options.remote_origin)
assert local.scheme == 'http' and local.hostname == '127.0.0.1' and local.port
assert remote.scheme == 'https' and remote.hostname
for origin in (local, remote):
    assert not (origin.username or origin.password or origin.path or origin.query or origin.fragment)
runtime = options.runtime_directory.resolve(strict=True)
assert runtime.stat().st_mode & 0o777 == 0o700
assert (runtime / 'endpoint').read_text() == options.local_origin
binary = options.fixture_binary.resolve(strict=True)
root = Path('/Volumes/RootKernel')
temporary = root / 'tmp' if os.path.ismount(root) and os.access(root, os.W_OK) else Path(tempfile.gettempdir())
owned = Path(tempfile.mkdtemp(prefix='hy-https-', dir=temporary))
env = os.environ | {'TMPDIR': str(owned), 'PLAYWRIGHT_DAEMON_SESSION_DIR': str(owned / 'registry'),
                    'PLAYWRIGHT_DAEMON_SOCKETS_DIR': str(owned / 'sockets')}
cli = [str(options.browser_cli.resolve(strict=True)), '-s=h' + uuid.uuid4().hex[:8]]
opened = False


def call(args, result=False):
    checked = subprocess.run(cli + args, cwd=owned, env=env, capture_output=True, text=True, timeout=45)
    if checked.returncode or '### Error' in checked.stdout:
        detail = checked.stdout.split('### Error', 1)[-1] if '### Error' in checked.stdout else checked.stderr
        raise RuntimeError(re.sub(r'(?i)[0-9a-f]{8,}', '[redacted]', '\n'.join(detail.splitlines()[:2])))
    if result:
        marker = re.search(r'### Result\s*\n', checked.stdout)
        assert marker, 'browser returned no result'
        return json.JSONDecoder().raw_decode(checked.stdout[marker.end():].lstrip())[0]


try:
    call(['open', options.remote_origin + '/connect?device-check=1', '--profile', str(owned / 'profile')])
    opened = True
    request = call(['run-code', """async(page)=>{
      await page.locator('#label').fill('Private route proof');
      await page.getByRole('button',{name:'Connect device',exact:true}).click();
      await page.waitForFunction(()=>document.querySelector('#state').textContent==='Waiting for owner approval');
      return page.evaluate(()=>({ID:pending.ID,Code:pending.Code}));
    }"""], True)
    approved = subprocess.run([str(binary), 'approve', '--directory', str(runtime)], input=json.dumps(request),
                              capture_output=True, text=True, timeout=10)
    assert approved.returncode == 0, 'protected exact test approval failed'
    del request
    proof = call(['run-code', """async(page)=>{
      await page.waitForFunction(()=>document.querySelector('#device-check').textContent==='Device check passed. Observation stored in the local fixture.',null,{timeout:10000});
      await page.getByRole('button',{name:'Read private fixture',exact:true}).click();
      await page.waitForFunction(()=>document.querySelector('#private').textContent==='synthetic private value');
      await page.reload();await page.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');
      const tab=await page.context().newPage();await tab.goto(REMOTE);
      await tab.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');await tab.close();
      const local=await page.context().newPage();await local.goto(LOCAL);
      await local.waitForFunction(()=>document.querySelector('#state').textContent==='Disconnected'&&document.querySelector('#owner').hidden);
      if(!await local.evaluate(()=>localStorage.getItem('haeryong.auth-ingress.v1')===null))throw new Error('remote credential crossed origin');
      if(await local.evaluate(async()=>(await fetch('/private/data')).status)!==401)throw new Error('unpaired local client gained private access');
      await local.close();
      return {private_https_read:true,remote_remembered_reload:true,remote_new_tab:true,local_origin_has_no_remote_credential:true,unpaired_local_private_401:true};
    }""".replace('REMOTE', json.dumps(options.remote_origin)).replace('LOCAL', json.dumps(options.local_origin))], True)
    call(['close']); opened = False
    call(['open', options.remote_origin, '--profile', str(owned / 'profile')]); opened = True
    proof.update(call(['run-code', """async(page)=>{
      await page.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');
      const remoteID=await page.evaluate(()=>deviceID);
      const local=await page.context().newPage();
      try{
        await local.goto(LOCAL);await local.locator('#label').fill('Local route proof');
        await local.getByRole('button',{name:'Connect device',exact:true}).click();
        await local.waitForFunction(()=>document.querySelector('#state').textContent==='Waiting for owner approval');
        const request=await local.evaluate(()=>({ID:pending.ID,Code:pending.Code}));
        await page.locator('#request-id').fill(request.ID);await page.locator('#code').fill(request.Code);
        await page.getByRole('button',{name:'Approve device',exact:true}).click();
        await local.waitForFunction(()=>document.querySelector('#state').textContent==='Connected and remembered');
        await local.reload();await local.waitForFunction(()=>document.querySelector('#state').textContent==='Remembered connection');
        await local.getByRole('button',{name:'Read private fixture',exact:true}).click();
        await local.waitForFunction(()=>document.querySelector('#private').textContent==='synthetic private value');
        await local.getByRole('button',{name:'Revoke Private route proof — '+remoteID,exact:true}).click();
        await page.waitForFunction(()=>document.querySelector('#owner').hidden&&document.querySelector('#private').textContent==='',null,{timeout:6000});
        if(!await page.evaluate(()=>localStorage.getItem('haeryong.auth-ingress.v1')===null))throw new Error('revoked remote credential retained');
        if(await page.evaluate(async()=>(await fetch('/private/data')).status)!==401)throw new Error('remote private read remained authorized');
        await local.getByRole('button',{name:'Log out',exact:true}).click();
        await local.waitForFunction(()=>document.querySelector('#state').textContent==='Logged out');
      }finally{await local.close();}
      return {remote_full_browser_restart:true,trusted_remote_approves_local:true,local_remembered_reload:true,local_private_read:true,cross_origin_remote_revocation:true,remote_private_view_cleared:true,revoked_remote_private_401:true,local_logout:true};
    }""".replace('LOCAL', json.dumps(options.local_origin))], True))
    proof.update({'transport': 'normal validated HTTPS through the selected private proxy', 'physical_peer': False,
                  'scope': 'same-host, origin-separated private/local browser clients; not physical Safari'})
    print(json.dumps(proof))
finally:
    if opened:
        call(['close'])
    shutil.rmtree(owned)
