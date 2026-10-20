"""TASK-003-06 actual responsive Reader and cross-client journey in real Chrome.

Phase initial (companion absent): standalone mode owns one isolated tagged
daemon/library through tests/e2e/harness.py, or attaches to the native
driver's shared daemon/library via --shared-info (0600 endpoint, genuine
Go-issued browser device token, corpus roots, browser session/profile).
It imports BOTH unchanged G-VALUE corpus roots through the real shared UI,
runs R1--R4 authored controls/inline evidence/exact reopen, archive + raw
file per-entry byte comparison, the declared responsive/a11y matrix
(390x844, 844x390, 820x1180, 1440x900 across Home/Library/Reader with real
keyboard, emulated touch, dialog containment, text scaling, contrast and
reduced motion), the region-capable fixture (standalone only, labelled CLI
setup) and honest X05/provider-absence checks. Phase cross reopens the
native-published exact versions on the SAME library without reseeding auth.
The genuine token is used only once for trusted-main-origin localStorage
setup (labelled CLI session setup, never UI-pairing proof) and never reaches
authored frames or receipts. No bearer in the receipt; runtime paths private.
"""
import argparse
import hashlib
import importlib.util
import json
import os
import pathlib
import re
import shutil
import subprocess
import tempfile
import time
import urllib.parse
import urllib.request
import zipfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--repo', type=pathlib.Path, required=True)
parser.add_argument('--shared-info', type=pathlib.Path)
parser.add_argument('--receipt', type=pathlib.Path, required=True)
parser.add_argument('--phase', choices=['initial', 'cross'], default='initial')
parser.add_argument('--browser-cli', type=pathlib.Path, required=True)
parser.add_argument('--native-versions', type=pathlib.Path)
args = parser.parse_args()
REPO = args.repo.resolve()
RECEIPT = args.receipt.resolve()
assert RECEIPT.parent.is_dir(), 'receipt parent must exist'
CLI = str(args.browser_cli.resolve(strict=True))
CAPTURES = RECEIPT.parent / (RECEIPT.stem + '-captures')
volume = pathlib.Path('/Volumes/RootKernel')
base = volume / 'tmp' if os.path.ismount(volume) and os.access(volume, os.W_OK) else pathlib.Path(tempfile.gettempdir())
os.umask(0o077)
work = pathlib.Path(tempfile.mkdtemp(prefix='h6r-', dir=base))
proof = {'scope': __doc__, 'phase': args.phase, 'runtime': str(work), 'complete': False, 'cases': []}
failures = []


def require(name, condition, detail=None):
    if not condition:
        failures.append(name)
        raise AssertionError('responsive/cross check failed: ' + name + ' ' + json.dumps(detail, ensure_ascii=False, default=str)[:600])
    proof['cases'].append({'name': name, 'ok': True})


# Predeclared expected facts (T06-reader-scenarios-predeclared.md at HEAD):
# exact SHA256 per corpus file, exact authored prediction markers, evidence
# anchors and honest 503/disabled states. Nothing here invents product facts.
CORPUS = {
    'mulgae': {
        'recovery/inventory.html': 'bba7a488f4baaa0a8b2b2e3c758f3776811e7f4548fd062945a3a60cb5e0ece3',
        'recovery/compose.html': '9a83060a540a69158d0483acb4c98ca67fe3f301722bdd11696bb72a3bf357e9',
        'assets/recovery.js': 'c7e5057b7d4f0902ba603e8a8f492e11bbbff53708c493d21d71bec7040647d9',
        'assets/style.css': '7b9f83f57b0b3b01550604428eb3d4de0983c9e7e2f761d3877f4c350c596017'},
    'gaori': {
        'gaori/outcomes.html': '818aba4bf6b1f57b631612ddfe6d2e800b1a94be93b9b003916cbf07445d11e9',
        'gaori/lifecycle.html': '643cba53861f8508b0fbac304068d5c99d57112a8cd338c7f6fe8845446fe2e6',
        'assets/gaori/illustrations.js': '9ee71556cb8358ff50d34a509731f75929d9341530f71b0c598c38843ac507f1',
        'assets/style.css': '92c828a5fa3ccf757a025494fedf26c7ef7910b68eb14f4d2dada04fa0459ea6'}}
ILLUSTRATION_PREFIX = 'Illustration — not an observed execution.'
R_EXPECTED = {
    'R1': [
        ({'source': 'retained', 'role': 'accepted', 'proof': 'verified', 'cleanup': 'matching'},
         'recovery rerun cannot replace an accepted role', ['M-S02', 'M-S09']),
        ({'source': 'retained', 'role': 'failed', 'proof': 'verified', 'cleanup': 'mismatched'},
         'recovery_cleanup_receipt_invalid before PersistFailedRunRecovery', ['M-S03', 'M-S10']),
        ({'source': 'retained', 'role': 'failed', 'proof': 'prepared', 'cleanup': 'matching'},
         'Preparation alone establishes no replay authority', ['M-S03', 'M-S10'])],
    'R2': [
        ({'source': 'present', 'mapping': 'omit', 'publication': 'none'},
         'Every missing selected role needs a committed rerun', None),
        ({'source': 'present', 'mapping': 'replace', 'publication': 'none'},
         'CompositeRoleAlreadySatisfied', None),
        ({'source': 'present', 'mapping': 'all', 'publication': 'none'},
         'predicts neither successful publication nor a CI pass', None),
        ({'source': 'present', 'mapping': 'all', 'publication': 'cli'},
         'inspect its exact returned composite run_id', None),
        ({'source': 'present', 'mapping': 'all', 'publication': 'mcp'},
         'composite_publication_incomplete', None)],
    'R3': [
        ({'branch': 'internal', 'child': '0'}, 'status=internal_error, exit_code=0, extractor_status=degraded; Gaori CLI exit=4', None),
        ({'branch': 'internal', 'child': '7'}, 'status=failed, exit_code=7, extractor_status=degraded; Gaori CLI exit=7', None),
        ({'branch': 'summarize-internal'}, 'status=internal_error, exit_code=4, extractor_status=degraded; Gaori CLI exit=4. No child is executed', None),
        ({'branch': 'miss', 'child': '0'}, 'status=passed, exit_code=0, extractor_status=no_match; Gaori CLI exit=0', None),
        ({'branch': 'miss', 'child': '7'}, 'status=failed, exit_code=7, extractor_status=degraded; Gaori CLI exit=7', None),
        ({'branch': 'oversize', 'child': '0'}, 'does not assert a numeric process exit', None)],
    'R4': [
        ({'observation': 'wait', 'session': 'same'}, 'does not settle command completion', 'inspect or await the same invocation'),
        ({'observation': 'await', 'session': 'same'}, 'The host deadline ends the await_run waiter only. This observation does not cancel or settle the command.', 'inspect or await the same invocation'),
        ({'observation': 'cancel', 'session': 'same'}, 'does not guarantee killed', 'inspect or await the same invocation'),
        ({'observation': 'finished', 'session': 'lost'}, 'terminal for that original invocation', 'finished does not mean passed'),
        ({'observation': 'wait', 'session': 'lost'}, 'Do not assume an identical-looking ID', None)]}
EVIDENCE_ANCHORS = {
    'mulgae': {'R1': ['M03', 'source-M-S02', 'source-M-S09', 'source-M-S03', 'source-M-S10'], 'R2': ['M08', 'M09', 'M11', 'source-M-S05', 'source-M-S09']},
    'gaori': {'R3': ['G01', 'G03', 'source-G-S01', 'source-G-S04'], 'R4': ['G08', 'G10', 'source-G-S11', 'source-G-S12']}}
PAGE_OF = {'R1': ('recovery/inventory.html', 'Recover roles from a proven source'),
           'R2': ('recovery/compose.html', None), 'R3': ('gaori/outcomes.html', None), 'R4': ('gaori/lifecycle.html', None)}

session = None
opened = False
product = None
standalone = args.shared_info is None
shared = None
endpoint = None
token = None
library_id = None
corpus_root = {}
profile = work / 'profile'
native_versions = None

if standalone:
    require('phase_standalone_initial', args.phase == 'initial', {'phase': args.phase})
    env = os.environ | {'TMPDIR': str(work), 'HAERYONG_TEST_STATE_DIR': str(work / 'app-state'), 'PYTHONDONTWRITEBYTECODE': '1', 'GOTOOLCHAIN': 'local', 'GOPROXY': 'off', 'GOSUMDB': 'off', 'GOFLAGS': '-mod=readonly'}
    os.environ['HAERYONG_TEST_STATE_DIR'] = env['HAERYONG_TEST_STATE_DIR']
    spec = importlib.util.spec_from_file_location('h', REPO / 'tests/e2e/harness.py')
    h = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(h)

    def fingerprints():
        paths = sorted([p for folder in ['cmd', 'internal'] for p in (REPO / folder).rglob('*.go') if not p.name.endswith('_test.go')]
                       + [REPO / 'go.mod', REPO / 'go.sum']
                       + [p for p in (REPO / 'internal/webassets/dist').rglob('*') if p.is_file()])
        return {str(p.relative_to(REPO)): hashlib.sha256(p.read_bytes()).hexdigest() for p in paths}
    before = fingerprints()
    subprocess.run(['go', 'build', '-tags', 'haeryong_test_state', '-o', str(work / 'haeryong'), './cmd/haeryong'],
                   cwd=REPO, env=env, capture_output=True, check=True, timeout=240)
    assert fingerprints() == before, 'Go/embed inputs changed during isolated build; no stable-candidate proof'
    product = h.Product(work / 'haeryong', work)
    product.initialize()
    product.start()
    owner = product.owner('Responsive cross-client browser journey')
    endpoint, token = product.endpoint, owner['token']
    library_id = product.data('GET', '/api/v1/capabilities', client=owner)['library_id']
    corpus_root = {'mulgae': REPO / 'spikes/knowledge-value/samples/mulgae', 'gaori': REPO / 'spikes/knowledge-value/samples/gaori'}
    proof['standalone_daemon'] = {'endpoint_origin': urllib.parse.urlparse(endpoint).netloc, 'owner_session': 'isolated harness owner (labelled standalone self-verify)'}
else:
    shared_path = args.shared_info.resolve()
    assert shared_path.stat().st_mode & 0o077 == 0, 'shared-info must be private (0600)'
    shared = json.loads(shared_path.read_text())
    endpoint, token, library_id = shared['endpoint'], shared['token'], shared['library_id']
    corpus_root = {name: pathlib.Path(shared['corpus'][name]) for name in ('mulgae', 'gaori')}
    profile = pathlib.Path(shared['browser_profile'])
    session = shared['browser_session']
    proof['attached_shared_daemon'] = {'endpoint_origin': urllib.parse.urlparse(endpoint).netloc, 'library_id': library_id,
                                       'browser_session': session, 'daemon_owned_by': 'native cross-client driver'}
    if args.phase == 'cross':
        assert args.native_versions is not None, 'cross phase requires --native-versions'
        native_versions = json.loads(args.native_versions.resolve().read_text())
session = session or work.name


def call(argv, result=False):
    proc = subprocess.run([CLI, '--session=' + session, *argv], cwd=work, capture_output=True, text=True, timeout=300)
    if proc.returncode != 0 or '### Error' in proc.stdout:
        diagnostic = proc.stdout.split('### Error', 1)[-1].split('###', 1)[0] if '### Error' in proc.stdout else (proc.stderr or proc.stdout)
        if token:
            diagnostic = diagnostic.replace(token, '[redacted fixture credential]')
        proof['browser_error'] = diagnostic[:1800]
        raise AssertionError('Browser operation failed; see bounded private receipt')
    if result:
        match = re.search(r'### Result\s*\n', proc.stdout)
        assert match
        return json.JSONDecoder().raw_decode(proc.stdout[match.end():].lstrip())[0]
    return None


node_binary = shutil.which('node')
_variant = [0]


def js(code):
    # Syntax-check EVERY generated variant before browser execution: a broken
    # template substitution must fail loudly here, never as a browser error.
    if node_binary is None:
        raise AssertionError('node is required to syntax-check generated JS variants')
    _variant[0] += 1
    variant = work / f'js-variant-{_variant[0]}.js'
    variant.write_text(code)
    checked = subprocess.run([node_binary, '--check', str(variant)], capture_output=True, text=True)
    if checked.returncode != 0:
        proof['js_syntax_error'] = checked.stderr[:1200]
        raise AssertionError('generated JS variant failed node --check; see bounded private receipt')
    return call(['run-code', code], True)



def browser_metadata(version_id, page_path):
    """Actual browser-origin HTTP reads under its already attached session.
    No bearer is returned; this does not create another session or transport.
    """
    return js(("""async page=>{return await page.evaluate(async({version_id,page_path})=>{
      const auth={headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1')},credentials:'omit'};
      const get=async path=>{const r=await fetch(path,auth);if(!r.ok)throw Error('metadata HTTP '+r.status);return (await r.json()).data;};
      const v=await get('/api/v1/versions/'+version_id);
      const files=[];let cursor=null;
      do{const q=cursor?'?cursor='+encodeURIComponent(cursor):'';const p=await get('/api/v1/versions/'+version_id+'/files'+q);files.push(...p.items);cursor=p.next_cursor;}while(cursor!==null);
      const b=await get('/api/v1/versions/'+version_id+'/render-bootstrap?page_path='+encodeURIComponent(page_path));
      const session=await get('/api/v1/session');
      const pick=(o,keys)=>Object.fromEntries(keys.map(k=>[k,o[k]??null]));
      return {version:pick(v,['version_id','document_id','sequence','base_version_id','default_page','viewer_profile','source_basis','payload_sha256','manifest_sha256','supplied_metadata_sha256']),
        files:files.map(f=>pick(f,['path','kind','byte_size','bytes_sha256','link_target'])).sort((a,b)=>a.path<b.path?-1:a.path>b.path?1:0),
        bootstrap:pick(b,['version_id','viewer_profile','default_page','selected_page','requested_page','renderer_available','renderer_unavailable_reason','renderer_construction','delivery']),
        capabilities:await get('/api/v1/capabilities'),session_id:session.session_id};
    },ARGS);}""").replace('ARGS', json.dumps({'version_id': version_id, 'page_path': page_path})))


def shot(name):
    CAPTURES.mkdir(parents=True, exist_ok=True)
    call(['screenshot', '--filename', str(CAPTURES / (name + '.png'))])


def grant_write_guard(profile_dir, origin):
    prefs_path = profile_dir / 'Default' / 'Preferences'
    prefs = json.loads(prefs_path.read_text()) if prefs_path.exists() else {}
    prefs.setdefault('profile', {}).setdefault('content_settings', {}).setdefault('exceptions', {})['file_system_write_guard'] = {
        origin.rstrip('/') + ',*': {'setting': 1}}
    prefs_path.parent.mkdir(parents=True, exist_ok=True)
    prefs_path.write_text(json.dumps(prefs))


corpus = {}
for package in ('mulgae', 'gaori'):
    copy = work / ('corpus-' + package)
    entries = {}
    for relative, expected in CORPUS[package].items():
        original = corpus_root[package] / relative
        digest = hashlib.sha256(original.read_bytes()).hexdigest()
        assert digest == expected, f'corpus file changed: {package}/{relative}'
        destination = copy / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(original, destination)
        assert hashlib.sha256(destination.read_bytes()).hexdigest() == expected, f'copy rehash mismatch: {package}/{relative}'
        entries[relative] = expected
    corpus[package] = {'root': copy, 'entries': entries}
require('corpus_predeclared_hashes_and_copy_rehash', True, {'packages': sorted(corpus)})

published = {}
try:
    urllib.request.urlopen(endpoint + '/health/live', timeout=10).read()
    # The loopback file_system_write_guard allow (official Chromium content
    # setting) must exist in the profile BEFORE Chrome launches, exactly like
    # the committed transfer driver; a runtime Preferences edit would not
    # apply to the already-running browser.
    grant_write_guard(profile, endpoint)
    proof['destination_grant_label'] = 'download destination uses a transparent pre-launch file_system_write_guard allow plus CDP-delivered real host directory grant in a disposable/private profile (test control, never chooser-UX proof)'
    call(['open', endpoint + '/health/live', '--browser', 'chrome', '--profile', str(profile)])
    opened = True
    js("async page=>{page.qaErrors=[];page.qaExpectedWarnings=[];page.on('pageerror',error=>page.qaErrors.push(error.message));"
       "page.on('console',message=>{if(message.type()==='warning'&&message.text()===\"An iframe which has both allow-scripts and allow-same-origin for its sandbox attribute can escape its sandboxing.\"){page.qaExpectedWarnings.push(message.text());return;}"
       "if(['error','warning'].includes(message.type())&&!/Failed to load resource|CORS policy|Access to fetch|net::|ERR_/.test(message.text()))page.qaErrors.push(message.text())});return true;}")
    if args.phase == 'initial':
        # CLI session setup with the genuine browser device token: one write to
        # the trusted main origin only, honestly labelled, never UI pairing.
        js("async page=>{await page.evaluate(token=>localStorage.setItem('haeryong.owner-session.v1',token),"
           + json.dumps(token) + ");return true;}")
        proof['auth_setup'] = 'genuine Go-issued browser device token written once to trusted main origin localStorage (labelled CLI session setup)'
    else:
        stored = js("async page=>await page.evaluate(()=>({present:localStorage.getItem('haeryong.owner-session.v1')!==null}))")
        require('cross_no_auth_reseed', stored['present'], 'attached profile must retain its session without reseeding')
    js(("async page=>{await page.goto(ORIGIN+'/library');await page.waitForSelector('[data-state=\"verified\"]',{timeout:30000});return true;}").replace('ORIGIN', json.dumps(endpoint)))
    capabilities = js("async page=>await page.evaluate(async()=>{const response=await fetch('/api/v1/capabilities',{headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1')},credentials:'omit'});return await response.json();})")
    proof['capabilities'] = capabilities.get('data', capabilities)

    if args.phase == 'initial':
        # ---- Real shared-UI import of both unchanged corpus roots ----
        for package in ('mulgae', 'gaori'):
            # Capture EVERY existing document before the real UI import. The
            # attached owner already has region/attack fixtures; those cannot
            # be mistaken for this import's freshly published result.
            seen_ids = js("""async page=>await page.evaluate(async()=>{
              const auth={headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1')},credentials:'omit'};
              const ids=[];let cursor=null;
              do{const r=await fetch('/api/v1/documents'+(cursor?'?cursor='+encodeURIComponent(cursor):''),auth);if(!r.ok)throw Error('document enumeration HTTP '+r.status);const p=(await r.json()).data;ids.push(...p.items.map(d=>d.document_id));cursor=p.next_cursor;}while(cursor!==null);
              return ids;
            })""")
            js(("async (page) => {await page.getByRole('link',{name:'서재',exact:true}).click();"
                "await page.getByRole('button',{name:'가져오기',exact:true}).click();"
                "const reset=page.getByRole('button',{name:'새 가져오기 시작',exact:true});if(await reset.count())await reset.click();"
                "await page.getByLabel('폴더 선택',{exact:true}).setInputFiles(PATH);"
                "await page.getByRole('button',{name:'가져오기 시작',exact:true}).click();return true;}")
               .replace('PATH', json.dumps(str(corpus[package]['root']))))
            js(("async (page) => {await page.waitForFunction(()=>document.querySelector('[data-testid=\"import-stage\"]')"
                "&&document.querySelector('[data-testid=\"import-stage\"]').textContent.includes('발행 완료'),null,{timeout:240000});return true;}"))
            # Identity of the UI import is read through the authenticated
            # document/version API (the import itself stays real-UI). The
            # freshly published document is the first page entry NOT yet
            # captured, independent of list ordering.
            ids = js(("""async page=>{
              return await page.evaluate(async()=>{
              const auth={headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1')},credentials:'omit'};
              const rows=[];let cursor=null;
              do{const r=await fetch('/api/v1/documents'+(cursor?'?cursor='+encodeURIComponent(cursor):''),auth);if(!r.ok)throw Error('document enumeration HTTP '+r.status);const p=(await r.json()).data;rows.push(...p.items);cursor=p.next_cursor;}while(cursor!==null);
              const candidates=rows.filter(item=>!SEEN.includes(item.document_id));
              if(candidates.length!==1||!candidates[0].latest_version_id)throw Error('UI publication did not produce one exact new document/version');
              const document=candidates[0];
              return {document_id:document.document_id,version_id:document.latest_version_id,title:document.title||null};});}""")
               .replace('SEEN', json.dumps(seen_ids)))
            require('ui_import_published_' + package, bool(ids.get('version_id')), ids)
            published[package] = ids
            shot('library-' + package)
        # ---- R1..R4 authored controls, inline evidence, exact identity ----
        journeys = {}
        for task, package in (('R1', 'mulgae'), ('R2', 'mulgae'), ('R3', 'gaori'), ('R4', 'gaori')):
            page_path = PAGE_OF[task][0]
            route = endpoint + f"/documents/{published[package]['document_id']}/versions/{published[package]['version_id']}?" + urllib.parse.urlencode({'page': page_path})
            result = js(("""async page=>{
              await page.goto(ROUTE);
              const frame=page.frameLocator('iframe[title="결과 뷰어"]');
              const panel=frame.locator('[data-illustration]');
              await panel.locator('output[data-output]').waitFor({timeout:20000});
              const initial=await panel.locator('output[data-output]').textContent();
              if(!initial.includes(PREFIX))throw Error('illustration honesty prefix missing');
              const observed=[];
              for(const step of STEPS){
                for(const [name,value] of Object.entries(step.inputs)){
                  const select=panel.locator(`[data-input=${name}]`);
                  if(value===null){continue;}
                  await select.selectOption(value);
                }
                const text=await panel.locator('output[data-output]').textContent();
                if(!text.includes(step.expect))throw Error('expected prediction missing: '+step.expect+' :: '+text.slice(0,200));
                const basisNode=panel.locator('[data-illustration-sources]');
                const basis=(await basisNode.count())?(await basisNode.first().textContent()).slice(0,220):null;
                observed.push({inputs:step.inputs,expect:step.expect,actualText:text,basis});
              }
              const evidence={};
              for(const anchor of ANCHORS){
                const node=frame.locator('#'+anchor);
                evidence[anchor]=(await node.count())?(await node.first().textContent()).trim().slice(0,300):null;
              }
              const identity={url:page.url(),heading:await frame.locator('h1').first().textContent()};
              return {initial:initial.slice(0,160),observed,evidence,identity};}""")
               .replace('ROUTE', json.dumps(route)).replace('PREFIX', json.dumps(ILLUSTRATION_PREFIX))
               .replace('STEPS', json.dumps([{'inputs': {k: v for k, v in inputs.items() if v is not None}, 'expect': expect} for inputs, expect, _ in R_EXPECTED[task]], ensure_ascii=False))
               .replace('ANCHORS', json.dumps(EVIDENCE_ANCHORS[package][task])))
            for anchor, text in result['evidence'].items():
                require(f'{task}_evidence_{anchor}', isinstance(text, str) and len(text.strip()) > 20, {'excerpt': text})
            journeys[task] = result
            require(task + '_controls_and_predictions', len(result['observed']) == len(R_EXPECTED[task]), result['observed'])
        # Child select disabled under summarize-internal (R3 authored honesty).
        disabled = js(("""async page=>{
          await page.goto(ROUTE);
          const frame=page.frameLocator('iframe[title="결과 뷰어"]');
          const panel=frame.locator('[data-illustration]');
          await panel.locator('output[data-output]').waitFor({timeout:20000});
          await panel.locator('[data-input=branch]').selectOption('summarize-internal');
          return await panel.locator('[data-input=child]').isDisabled();}""")
           .replace('ROUTE', json.dumps(endpoint + f"/documents/{published['gaori']['document_id']}/versions/{published['gaori']['version_id']}?" + urllib.parse.urlencode({'page': 'gaori/outcomes.html'}))))
        require('R3_summarize_disables_child', disabled)
        # ---- Local multi-page link then exact version/page/query/fragment reopen ----
        mulgae_route = endpoint + f"/documents/{published['mulgae']['document_id']}/versions/{published['mulgae']['version_id']}"
        reopen = js(("""async page=>{
          await page.goto(INDEX);
          const frame=page.frameLocator('iframe[title="결과 뷰어"]');
          await frame.getByRole('link',{name:'Compose and reconciliation',exact:true}).click();
          await page.waitForFunction(()=>new URL(location.href).searchParams.get('page')==='recovery/compose.html');
          const afterLink={url:page.url(),heading:await page.frameLocator('iframe[title="결과 뷰어"]').locator('h1').first().textContent()};
          await page.goto(EXACT);
          await page.frameLocator('iframe[title="결과 뷰어"]').locator('#M11').waitFor({timeout:20000});
          await page.waitForFunction(()=>{const q=new URL(location.href).searchParams;return q.get('page')==='recovery/compose.html'&&q.get('page_fragment')==='M11';});
          return {afterLink,reopened:{url:page.url(),heading:await page.frameLocator('iframe[title="결과 뷰어"]').locator('h1').first().textContent()}};}""")
           .replace('INDEX', json.dumps(mulgae_route + '?' + urllib.parse.urlencode({'page': 'recovery/inventory.html'})))
           .replace('EXACT', json.dumps(f"{endpoint}/documents/{published['mulgae']['document_id']}/versions/{published['mulgae']['version_id']}?" + urllib.parse.urlencode({'page': 'recovery/compose.html', 'page_query': '', 'page_fragment': 'M11'})))
           .replace('DOC', published['mulgae']['document_id']).replace('VER', published['mulgae']['version_id']))
        require('link_then_exact_reopen_identity', reopen['afterLink']['heading'] == reopen['reopened']['heading'] and 'page_fragment=M11' in reopen['reopened']['url'], reopen)
        # ---- Archive + raw file downloads: per-entry path/kind/byte compare ----
        for package in ('mulgae', 'gaori'):
            destination = work / ('download-' + package)
            destination.mkdir()
            js(("""async (page) => {
              await page.getByRole('link',{name:'서재',exact:true}).click();
              const row=page.locator('[data-testid="library-document"]',{hasText:PACKAGE}).first();
              await row.getByRole('button',{name:'원본 디렉터리 내려받기',exact:true}).click();
              const reset=page.getByRole('button',{name:'새 내려받기 시작 (동의 초기화)',exact:true});
              if(await reset.count())await reset.click();
              await page.getByRole('button',{name:'내려받기 시작',exact:true}).click();
              await page.getByLabel('위 교체 규칙을 이해하고 동의합니다.').check();
              await page.getByRole('button',{name:'동의하고 계속',exact:true}).click();
              const zone=page.locator('[data-testid="download-drop-zone"]');
              await zone.scrollIntoViewIfNeeded();const box=await zone.boundingBox();
              const session=await page.context().newCDPSession(page);
              const data={items:[],files:[DEST],dragOperationsMask:1};
              for(const type of ['dragEnter','dragOver','drop'])await session.send('Input.dispatchDragEvent',{type,x:box.x+30,y:box.y+30,data});
              await page.waitForSelector('[data-testid="download-permission"]',{timeout:20000});
              await page.getByRole('button',{name:'이 폴더에 저장',exact:true}).click();
              await page.waitForSelector('[data-testid="download-done"]',{timeout:240000});
              return true;}""")
               .replace('PACKAGE', json.dumps(package)).replace('DEST', json.dumps(str(destination))))
            archive = next(destination.glob('*.zip'))
            with zipfile.ZipFile(archive) as bundle:
                names = [name for name in bundle.namelist() if not name.endswith('/')]
                require('archive_entries_exact_' + package, set(names) == set(corpus[package]['entries']), {'archive': sorted(names)})
                per_file = {}
                for name in names:
                    data = bundle.read(name)
                    digest = hashlib.sha256(data).hexdigest()
                    require('archive_bytes_' + package + '_' + name, digest == corpus[package]['entries'][name], {'entry': name})
                    per_file[name] = digest
                proof.setdefault('archive', {})[package] = {'entries': len(names), 'sha256': per_file,
                                                             'container_sha256_note': 'whole-ZIP hash is not identity; per-entry bytes compared',
                                                             'container_sha256': hashlib.sha256(archive.read_bytes()).hexdigest()}
            raw_file = work / ('raw-' + package)
            raw_file.mkdir()
            js(("""async (page) => {
              await page.goto(ROUTE);
              const files=page.locator('dialog[aria-label="페이지 및 파일 목록"]');
              await files.getByRole('button',{name:FILE,exact:true}).click();
              await page.getByRole('button',{name:'내려받기 시작',exact:true}).click();
              await page.getByLabel('위 교체 규칙을 이해하고 동의합니다.').check();
              await page.getByRole('button',{name:'동의하고 계속',exact:true}).click();
              const zone=page.locator('[data-testid="download-drop-zone"]');
              await zone.scrollIntoViewIfNeeded();const box=await zone.boundingBox();
              const session=await page.context().newCDPSession(page);
              const data={items:[],files:[DEST],dragOperationsMask:1};
              for(const type of ['dragEnter','dragOver','drop'])await session.send('Input.dispatchDragEvent',{type,x:box.x+30,y:box.y+30,data});
              await page.waitForSelector('[data-testid="download-permission"]',{timeout:20000});
              await page.getByRole('button',{name:'이 폴더에 저장',exact:true}).click();
              await page.waitForSelector('[data-testid="download-done"]',{timeout:120000});
              return true;}""")
               .replace('ROUTE', json.dumps(mulgae_route if package == 'mulgae' else endpoint + f"/documents/{published['gaori']['document_id']}/versions/{published['gaori']['version_id']}"))
               .replace('FILE', json.dumps('assets/style.css' if package == 'mulgae' else 'assets/gaori/illustrations.js'))
               .replace('DEST', json.dumps(str(raw_file))))
            raw_bytes = next(raw_file.iterdir()).read_bytes()
            relative = 'assets/style.css' if package == 'mulgae' else 'assets/gaori/illustrations.js'
            require('raw_file_bytes_' + package, hashlib.sha256(raw_bytes).hexdigest() == corpus[package]['entries'][relative], {'file': relative, 'bytes': len(raw_bytes)})
        proof['coordination'] = {'phase': 'initial', 'library_id': library_id,
                                 'published': {name: {'document_id': ids['document_id'], 'version_id': ids['version_id'],
                                                      'library_id': library_id, 'entry_count': len(corpus[name]['entries']),
                                                      'entry_sha256': corpus[name]['entries']} for name, ids in published.items()}}
        # The native owner reads the agreed TOP-LEVEL receipt fields.
        proof['published'] = proof['coordination']['published']
        proof['transport_metadata'] = {name: browser_metadata(ids['version_id'], 'recovery/inventory.html' if name == 'mulgae' else 'gaori/lifecycle.html') for name, ids in published.items()}
        proof['journeys'] = journeys
        proof['reopen'] = reopen

        # ---- Declared responsive/a11y matrix on real layout + input ----
        # Every fixture context reuses the genuine authenticated storageState
        # of the attached browser device session (no second daemon, no
        # reseed, no native credential and no token ever leaves browser
        # memory or receipts). Context options are injected as whole JSON
        # values, so no string-spliced object literal can produce invalid JS.
        VIEWPORTS = [(390, 844), (844, 390), (820, 1180), (1440, 900)]
        READER_URL = mulgae_route + '?' + urllib.parse.urlencode({'page': 'recovery/inventory.html'})
        SURFACES = [{'name': 'home', 'url': endpoint + '/', 'wait': 'verified'},
                    {'name': 'library', 'url': endpoint + '/library', 'wait': 'verified'},
                    {'name': 'reader', 'url': READER_URL, 'wait': 'verified', 'frame': True}]
        CAPTURES.mkdir(parents=True, exist_ok=True)
        MEASURE_JS = """async page=>{
          const state=await page.context().storageState();
          const context=await page.context().browser().newContext({...OPTIONS,storageState:state});
          const target=await context.newPage();
          const errors=[];target.on('pageerror',error=>errors.push(error.message));
          const surfaces=[];
          for(const surface of SURFACES){
            await target.goto(surface.url);
            await target.waitForSelector('[data-state="verified"]',{timeout:30000});
            if(surface.frame)await target.frameLocator('iframe[title="결과 뷰어"]').locator('h1').first().waitFor({timeout:20000});
            const capture=(MODE==='light'||(MODE==='dark'&&surface.name==='reader'))?CAPTUREDIR+'/'+OPTIONS.viewport.width+'x'+OPTIONS.viewport.height+'-'+MODE+'-'+surface.name+'.png':null;
            if(capture)await target.screenshot({path:capture,fullPage:false});
            let stickyActions=null;
            if(surface.frame&&OPTIONS.viewport.width<768){
              await target.locator('iframe[title="결과 뷰어"]').scrollIntoViewIfNeeded();
              stickyActions=await target.locator('.reader-actions').evaluate(el=>{const r=el.getBoundingClientRect();return {position:getComputedStyle(el).position,top:Math.round(r.top),bottom:Math.round(r.bottom),height:innerHeight};});
              if(MODE==='light')await target.screenshot({path:CAPTUREDIR+'/phone-reading-actions.png',fullPage:false});
            }
            const measureContrast=()=>target.evaluate(()=>{
              const lum=(color)=>{const [r,g,b]=color.match(/\\d+(\\.\\d+)?/g).slice(0,3).map(Number).map(v=>{v/=255;return v<=0.03928?v/12.92:Math.pow((v+0.055)/1.055,2.4);});return 0.2126*r+0.7152*g+0.0722*b;};
              const bgOf=(el)=>{let node=el;while(node&&node!==document.documentElement){const c=getComputedStyle(node).backgroundColor;if(c&&!c.includes('0, 0, 0, 0')&&c!=='transparent')return c;node=node.parentElement;}return getComputedStyle(document.body).backgroundColor;};
              const offenders=[],samples=[],seen=new Set();
              for(const el of document.querySelectorAll('body *')){
                if(el.closest('iframe')||el.closest('dialog:not([open])')||!el.textContent.trim()||el.children.length)continue;
                if(typeof el.checkVisibility==='function'&&!el.checkVisibility())continue;
                if(typeof el.checkVisibility!=='function'&&!el.offsetParent)continue;
                const style=getComputedStyle(el);
                const size=parseFloat(style.fontSize),weight=parseInt(style.fontWeight);
                const large=size>=24||(size>=18.66&&weight>=700);
                const ratio=(a,b)=>{const l1=lum(a),l2=lum(b);return (Math.max(l1,l2)+0.05)/(Math.min(l1,l2)+0.05);};
                const background=bgOf(el),value=ratio(style.color,background);
                const scope=el.closest('dialog')?.getAttribute('aria-label')||'host';
                const key=JSON.stringify([scope,style.color,background,size,weight]);
                if(!seen.has(key)){seen.add(key);samples.push({scope,tag:el.tagName,text:el.textContent.trim().slice(0,40),foreground:style.color,background,fontSize:size,fontWeight:weight,lineHeight:style.lineHeight,ratio:value,minimum:large?3:4.5});}
                if(value<(large?3:4.5))offenders.push({text:el.textContent.trim().slice(0,40),ratio:Math.round(value*100)/100});
              }
              const base=getComputedStyle(document.body);
              return {offenders:offenders.slice(0,10),samples,body:{fontSize:parseFloat(base.fontSize),lineHeight:parseFloat(base.lineHeight),ratio:parseFloat(base.lineHeight)/parseFloat(base.fontSize)}};});
            const contrast=await measureContrast();
            const panelContrast=[];
            if(surface.frame){
              if(OPTIONS.viewport.width<768){await target.getByRole('button',{name:'파일 목록',exact:true}).click();await target.locator('#reader-files-panel[open]').waitFor();}
              panelContrast.push({panel:'Files',measurement:await measureContrast()});
              if(OPTIONS.viewport.width<768){await target.keyboard.press('Escape');await target.locator('#reader-files-panel').waitFor({state:'hidden'});}
              await target.getByRole('button',{name:'근거 패널',exact:true}).click();await target.locator('#reader-context-panel[open]').waitFor();
              panelContrast.push({panel:'Context',measurement:await measureContrast()});
              await target.keyboard.press('Escape');await target.locator('#reader-context-panel').waitFor({state:'hidden'});
            }
            const smallTargets=await target.evaluate(()=>{
              const small=[];
              for(const el of document.querySelectorAll('button,a,input,select,[role=button]')){
                if(el.closest('iframe')||!el.offsetParent)continue;
                if(typeof el.checkVisibility==='function'&&!el.checkVisibility())continue;
                const box=el.getBoundingClientRect();
                if(box.width>0&&box.height>0&&Math.min(box.width,box.height)<44)small.push({tag:el.tagName,text:(el.textContent||el.getAttribute('aria-label')||'').trim().slice(0,30),w:Math.round(box.width),h:Math.round(box.height)});
              }
              return small.slice(0,12);});
            const overflow=await target.evaluate(()=>document.documentElement.scrollWidth-document.documentElement.clientWidth);
            await target.evaluate(()=>document.body.focus());
            const tabStops=[];
            for(let i=0;i<6;i++){await target.keyboard.press('Tab');tabStops.push(await target.evaluate(()=>{const el=document.activeElement;if(!el||el===document.body)return null;return {tag:el.tagName,text:(el.textContent||el.getAttribute('aria-label')||'').trim().slice(0,26)};}));}
            let focusVisible=await target.evaluate(()=>{const el=document.activeElement;if(!el)return null;const s=getComputedStyle(el);return {tag:el.tagName,outline:s.outlineStyle!=='none'&&s.outlineWidth!=='0px',boxShadow:s.boxShadow!=='none',scope:'host'};});
            // Parent activeElement becomes the iframe anchor when keyboard
            // focus enters its browsing context. Inspect the ACTUAL focused
            // authored element with the external browser driver; parent CSS
            // :focus does not represent that child's visible focus indicator.
            if(focusVisible?.tag==='IFRAME'){
              for(const frame of target.frames().slice(1)){
                const child=await frame.evaluate(()=>{const el=document.activeElement;if(!document.hasFocus()||!el||el.tagName==='BODY'||el.tagName==='IFRAME')return null;const s=getComputedStyle(el);return {tag:el.tagName,outline:s.outlineStyle!=='none'&&s.outlineWidth!=='0px',boxShadow:s.boxShadow!=='none',scope:'actual focused authored child; external observer only'};});
                if(child){focusVisible=child;break;}
              }
            }
            await target.evaluate(()=>{document.documentElement.style.fontSize='32px';});
            const scaledOverflow=await target.evaluate(()=>document.documentElement.scrollWidth-document.documentElement.clientWidth);
            const scaledOffenders=await target.evaluate(()=>[...document.querySelectorAll('body *')].filter(el=>{const r=el.getBoundingClientRect();return r.width>0&&(r.right>innerWidth+1||r.left < -1)&&!el.closest('dialog:not([open])');}).slice(0,12).map(el=>({tag:el.tagName,class:el.className,text:(el.textContent||'').trim().slice(0,60),right:Math.round(el.getBoundingClientRect().right)})));
            await target.evaluate(()=>{document.documentElement.style.fontSize='';});
            surfaces.push({name:surface.name,contrast,panelContrast,smallTargets,overflow,tabStops,focusVisible,scaledOverflow,scaledOffenders,capture,stickyActions});
          }
          const reducedMatches=await target.evaluate(()=>matchMedia('(prefers-reduced-motion: reduce)').matches);
          await context.close();
          return {viewport:OPTIONS.viewport,mode:MODE,requestedReduce:REQUESTED_REDUCE,surfaces,reducedMatches,errors};}"""

        def measure(width, height, mode, options):
            return js(MEASURE_JS
                      .replace('SURFACES', json.dumps(SURFACES))
                      .replace('CAPTUREDIR', json.dumps(str(CAPTURES)))
                      .replace('OPTIONS', json.dumps(options))
                      .replace('MODE', json.dumps(mode))
                      .replace('REQUESTED_REDUCE', 'true' if options.get('reducedMotion') == 'reduce' else 'false'))

        matrix = []
        for width, height in VIEWPORTS:
            for scheme in ('light', 'dark'):
                matrix.append(measure(width, height, scheme, {'viewport': {'width': width, 'height': height}, 'colorScheme': scheme}))
            matrix.append(measure(width, height, 'reduce', {'viewport': {'width': width, 'height': height}, 'colorScheme': 'light', 'reducedMotion': 'reduce'}))
        proof['matrix'] = matrix
        for row in matrix:
            require('matrix_framework_health_' + str(row['viewport']) + ':' + row['mode'], not row['errors'], row['errors'])
            for surface in row['surfaces']:
                tag = f"{row['viewport']['width']}x{row['viewport']['height']}:{row['mode']}:{surface['name']}"
                require('no_horizontal_overflow_' + tag, surface['overflow'] <= 1 and surface['scaledOverflow'] <= 1, surface)
                if surface['stickyActions'] is not None:
                    pinned = surface['stickyActions']
                    require('phone_reading_actions_reachable_' + tag, pinned['position'] == 'sticky' and pinned['top'] >= -1 and pinned['bottom'] <= pinned['height'], pinned)
                require('contrast_' + tag, len(surface['contrast']['offenders']) == 0, surface['contrast'])
                require('base_type_measured_' + tag, abs(surface['contrast']['body']['fontSize'] - 16) < .01 and abs(surface['contrast']['body']['ratio'] - 1.7) < .01, surface['contrast']['body'])
                for panel in surface['panelContrast']:
                    samples = panel['measurement']['samples']
                    require('open_panel_contrast_' + tag + ':' + panel['panel'], not panel['measurement']['offenders'] and any(panel['panel'].lower() in ('files' if '파일' in sample['scope'] else 'context' if '문맥' in sample['scope'] else '') for sample in samples), panel)
                require('touch_targets_44_' + tag, len(surface['smallTargets']) == 0, surface['smallTargets'])
                require('visible_focus_' + tag, surface['focusVisible'] is not None and (surface['focusVisible']['outline'] or surface['focusVisible']['boxShadow']), surface['focusVisible'])
        require('reduced_motion_honored', all(row['reducedMatches'] for row in matrix if row['requestedReduce']), [r for r in matrix if r['requestedReduce'] and not r['reducedMatches']][:2])
        proof['matrix'] = matrix
        proof['matrix_note'] = 'host chrome measured on Home/Library/Reader surfaces; authored iframe CSS is preserved and reported separately, never restyled'

        # Keyboard activation (Enter on links, Space on buttons) at desktop and
        # emulated touch activation on phone/tablet viewports.
        activation = js(("""async page=>{
          const state=await page.context().storageState();
          const context=await page.context().browser().newContext({viewport:{width:1440,height:900},storageState:state});
          const target=await context.newPage();
          await target.goto(ORIGIN+'/');
          await target.waitForSelector('[data-state="verified"]',{timeout:30000});
          await target.getByRole('link',{name:'서재',exact:true}).focus();
          await target.keyboard.press('Enter');
          await target.waitForSelector('[data-testid="library-document"]');
          const enterNavigation=true;
          const toggle=target.getByRole('button',{name:'근거 패널',exact:true});
          await target.goto(READER);
          await target.frameLocator('iframe[title="결과 뷰어"]').locator('h1').first().waitFor({timeout:20000});
          await toggle.focus();await target.keyboard.press(' ');
          const expanded=await target.getByRole('button',{name:'근거 패널',exact:true}).getAttribute('aria-expanded');
          await target.keyboard.press('Escape');
          const collapsed=await target.getByRole('button',{name:'근거 패널',exact:true}).getAttribute('aria-expanded');
          await context.close();
          return {enterNavigation,spaceToggle:expanded==='true',escapeCollapse:collapsed==='false'||collapsed===null};}""")
           .replace('ORIGIN', json.dumps(endpoint)).replace('READER', json.dumps(READER_URL)))
        require('keyboard_enter_space_activation', activation['enterNavigation'] and activation['spaceToggle'] and activation['escapeCollapse'], activation)
        touchActivation = js(("""async page=>{
          const state=await page.context().storageState();
          const results={};
          for(const viewport of [{width:390,height:844},{width:820,height:1180}]){
            const context=await page.context().browser().newContext({viewport,hasTouch:true,storageState:state});
            const target=await context.newPage();
            await target.goto(ORIGIN+'/');
            await target.waitForSelector('[data-state="verified"]',{timeout:30000});
            await target.getByRole('link',{name:'서재',exact:true}).tap();
            await target.waitForSelector('[data-testid="library-document"]');
            await target.goto(READER);
            await target.frameLocator('iframe[title="결과 뷰어"]').locator('h1').first().waitFor({timeout:20000});
            const files=viewport.width<768;
            if(files){await target.getByRole('button',{name:'파일 목록',exact:true}).tap();
              await target.locator('dialog[aria-label="페이지 및 파일 목록"]').waitFor({timeout:10000});
              await target.getByRole('button',{name:'파일 목록 닫기',exact:true}).tap();}
            await target.getByRole('button',{name:'근거 패널',exact:true}).tap();
            await target.locator('dialog[aria-label="문맥 및 근거 패널"]').waitFor({timeout:10000});
            const filesGone=files?!(await target.locator('dialog[aria-label="페이지 및 파일 목록"][open]').count()):true;
            results[viewport.width]={libraryNavigationTapped:true,contextSheet:true,filesClosedBySingleModal:filesGone,readerSetup:'existing exact-version deep link; Library result entry assessed separately'};
            await context.close();}
          return results;}""")
           .replace('ORIGIN', json.dumps(endpoint)).replace('READER', json.dumps(READER_URL)))
        for width, row in touchActivation.items():
            require(f'touch_activation_{width}', row['libraryNavigationTapped'] and row['contextSheet'] and row['filesClosedBySingleModal'], row)

        # Dialog containment/return/Esc at phone width + REAL mode transitions
        # across breakpoints (top layer/inert/focus/deliveries all observed).
        dialogBehavior = js((r"""async page=>{
          const opens=[];
          const state=await page.context().storageState();
          const context=await page.context().browser().newContext({viewport:{width:390,height:844},storageState:state});
          const target=await context.newPage();
          target.on('request',r=>{if(r.method()==='POST'&&/\/render-sessions(\?|$)/.test(r.url()))opens.push(r.url());});
          await target.goto(READER);
          await target.frameLocator('iframe[title="결과 뷰어"]').locator('h1').first().waitFor({timeout:20000});
          const frameElement=await target.locator('iframe[title="결과 뷰어"]').elementHandle();
          const authored=await frameElement.contentFrame();
          await authored.locator('#inventory-source').selectOption('missing');
          await authored.locator('#M03').evaluate(el=>{el.scrollIntoView();const text=el.querySelector('p')||el;const range=document.createRange();range.selectNodeContents(text);const selection=getSelection();selection.removeAllRanges();selection.addRange(range);document.activeElement?.blur();});
          const stateOf=async()=>({route:await target.evaluate(()=>({url:location.href,context:history.state?.haeryongReaderContext})),child:await authored.evaluate(()=>({url:location.href,control:document.getElementById('inventory-source').value,x:scrollX,y:scrollY,selection:getSelection().toString(),anchor:getSelection().anchorNode?.parentElement?.closest('[id]')?.id,anchorTop:document.getElementById('M03').getBoundingClientRect().top}))});
          const retainedBefore=await stateOf();
          const filesButton=target.getByRole('button',{name:'파일 목록',exact:true});
          await filesButton.click();
          const files=target.locator('dialog[aria-label="페이지 및 파일 목록"]');
          await files.waitFor({timeout:10000});
          let contained=true;const tabChain=[];
          for(let i=0;i<12;i++){await target.keyboard.press('Tab');
            const inside=await target.evaluate(()=>!!document.activeElement&&!!document.activeElement.closest('dialog[open]'));
            tabChain.push(await target.evaluate(()=>({tag:document.activeElement?.tagName,id:document.activeElement?.id,documentFocused:document.hasFocus()})));
            if(!inside)contained=false;}
          for(let i=0;i<8;i++){await target.keyboard.press('Shift+Tab');if(!await target.evaluate(()=>!!document.activeElement?.closest('dialog[open]')))contained=false;}
          const iframeStillLive=await target.locator('iframe[title="결과 뷰어"]').count();
          await target.keyboard.press('Escape');
          const closed=!(await target.locator('dialog[aria-label="페이지 및 파일 목록"][open]').count());
          const returned=await target.evaluate(()=>document.activeElement===document.getElementById('reader-files-toggle'));
          const deliveriesAfterSheets=opens.length;
          const afterPanelOnly=await stateOf();
          await filesButton.click();await files.waitFor({timeout:10000});
          const filter=target.locator('#reader-files-filter');
          await filter.click();
          const filterFocused=await target.evaluate(()=>document.activeElement===document.getElementById('reader-files-filter'));
          const inertWhileModal=await target.evaluate(()=>{const header=document.getElementById('reader-context-toggle');header.focus();return document.activeElement!==header;});
          const modalBefore=await files.evaluate(node=>node.matches(':modal'));
          await target.setViewportSize({width:1440,height:900});
          await target.waitForTimeout(400);
          const wideState=await target.evaluate(()=>{
            const node=document.querySelector('dialog[aria-label="페이지 및 파일 목록"]');
            const focusPreserved=document.activeElement===document.getElementById('reader-files-filter');
            const header=document.getElementById('reader-context-toggle');header.focus();
            const backgroundFocusable=document.activeElement===header;
            document.getElementById('reader-files-filter').focus();
            return {open:node.open,modal:node.matches(':modal'),backgroundFocusable,focusPreserved};});
          await target.setViewportSize({width:390,height:844});
          await target.waitForTimeout(400);
          const phoneState=await target.evaluate(()=>{
            const node=document.querySelector('dialog[aria-label="페이지 및 파일 목록"]');
            const header=document.getElementById('reader-files-toggle');header.focus();
            return {open:node.open,modal:node.matches(':modal'),backgroundInert:document.activeElement!==header,focusInDialog:!!document.activeElement&&!!document.activeElement.closest('dialog[open]')};});
          await target.keyboard.press('Escape');
          await target.getByRole('button',{name:'근거 패널',exact:true}).click();
          const contextNode=target.locator('dialog[aria-label="문맥 및 근거 패널"]');
          await contextNode.waitFor({timeout:10000});
          const contextModal=await contextNode.evaluate(node=>node.matches(':modal'));
          await target.setViewportSize({width:1440,height:900});
          await target.waitForTimeout(400);
          const contextWide=await target.evaluate(()=>{
            const node=document.querySelector('dialog[aria-label="문맥 및 근거 패널"]');
            const header=document.getElementById('reader-context-toggle');header.focus();
            return {open:node.open,modal:node.matches(':modal'),backgroundFocusable:document.activeElement===header};});
          await target.setViewportSize({width:390,height:844});
          await target.waitForTimeout(400);
          const modals=await target.evaluate(()=>document.querySelectorAll('dialog[open]:modal').length);
          const deliveriesTotal=opens.length;
          const iframeFinal=await target.locator('iframe[title="결과 뷰어"]').count();
          const retainedAfter=await stateOf();
          const sameFrame=await target.locator('iframe[title="결과 뷰어"]').evaluate((node,old)=>node===old,frameElement);
          await frameElement.dispose();
          await context.close();
          return {sameFrame,retainedBefore,afterPanelOnly,retainedAfter,tabContained:contained,tabChain,iframeCount:iframeStillLive,escapeClosed:closed,focusReturned:returned,
            filterFocused,inertWhileModal,filesModalBefore:modalBefore,filesWide:wideState,filesPhone:phoneState,
            contextModal,contextWide,phoneSingleModal:modals<=1,modals,
            zeroExtraDeliveries:deliveriesAfterSheets===deliveriesTotal,iframeFinal};}""")
           .replace('READER', json.dumps(mulgae_route + '?' + urllib.parse.urlencode({'page': 'recovery/inventory.html'}))))
        proof['dialog_behavior'] = dialogBehavior
        before, after = dialogBehavior['retainedBefore'], dialogBehavior['retainedAfter']
        require('actual_frame_control_selection_context_retained', dialogBehavior['sameFrame'] and before['route'] == after['route'] and before['child']['url'] == after['child']['url'] and before['child']['control'] == after['child']['control'] == 'missing' and bool(before['child']['selection']) and before['child']['selection'] == after['child']['selection'], dialogBehavior)
        # Responsive width changes reflow author content. A literal scrollY can
        # clamp at a shorter wide viewport; compare the original/final phone.
        panel_only = dialogBehavior['afterPanelOnly']
        require('authored_scroll_preserved_panel_only', before['child']['y'] > 0 and abs(before['child']['y'] - panel_only['child']['y']) <= 1 and before['child']['x'] == panel_only['child']['x'], {'before': before, 'after': panel_only})
        require('authored_reading_anchor_retained_after_reflow', after['child']['y'] > 0 and abs(before['child']['anchorTop'] - after['child']['anchorTop']) <= 2, {'before': before, 'after': after})
        require('phone_dialog_containment_return_esc', dialogBehavior['tabContained'] and dialogBehavior['escapeClosed'] and dialogBehavior['focusReturned'] and dialogBehavior['iframeCount'] == 1, dialogBehavior)
        require('mode_transition_real_toplayer_and_inert', dialogBehavior['filesModalBefore'] and dialogBehavior['inertWhileModal']
                and dialogBehavior['filesWide']['open'] and not dialogBehavior['filesWide']['modal'] and dialogBehavior['filesWide']['backgroundFocusable'] and dialogBehavior['filesWide']['focusPreserved']
                and dialogBehavior['filesPhone']['open'] and dialogBehavior['filesPhone']['modal'] and dialogBehavior['filesPhone']['backgroundInert'] and dialogBehavior['filesPhone']['focusInDialog']
                and dialogBehavior['contextModal'] and dialogBehavior['contextWide']['open'] and not dialogBehavior['contextWide']['modal'] and dialogBehavior['contextWide']['backgroundFocusable'], dialogBehavior)
        require('single_modal_and_zero_extra_deliveries', dialogBehavior['phoneSingleModal'] and dialogBehavior['zeroExtraDeliveries'] and dialogBehavior['iframeFinal'] == 1, dialogBehavior)
        proof['dialog_behavior'] = dialogBehavior

        # ---- S1.1 quantitative per-transition widths (explicit grid slots) ----
        # Every intermediate iframe resize observation is retained. Intended
        # settled widths are MEASURED endpoints (never product caps); the
        # unintended narrow Files-column sample (the observed 254px) must be
        # absent. The final return to the original phone width matches the
        # pre-transition baseline scroll/anchor within 1px, identical to the
        # dimension-matched raw natural-reflow baseline. Raw wide reflow
        # (including the anchor leaving the viewport) stays unforced.
        s11 = js((r"""async page=>{
          const opens=[];
          const state=await page.context().storageState();
          const context=await page.context().browser().newContext({viewport:{width:390,height:844},storageState:state});
          const target=await context.newPage();
          target.on('request',r=>{if(r.method()==='POST'&&/\/render-sessions(\?|$)/.test(r.url()))opens.push(r.url());});
          await target.goto(READER);
          const frameLocator=target.frameLocator('iframe[title="결과 뷰어"]');
          await frameLocator.locator('#M03').waitFor({timeout:20000});
          const frameElement=await target.locator('iframe[title="결과 뷰어"]').elementHandle();
          const authored=await frameElement.contentFrame();
          await authored.locator('#inventory-source').selectOption('missing');
          await authored.locator('#M03').evaluate(el=>{el.scrollIntoView();const text=el.querySelector('p')||el;const range=document.createRange();range.selectNodeContents(text);const selection=getSelection();selection.removeAllRanges();selection.addRange(range);document.activeElement?.blur();});
          const childState=()=>authored.evaluate(()=>({control:document.getElementById('inventory-source').value,w:innerWidth,h:innerHeight,x:scrollX,y:scrollY,anchorTop:document.getElementById('M03').getBoundingClientRect().top,active:document.activeElement?(document.activeElement.id||document.activeElement.tagName):null,selection:getSelection().toString()}));
          // One box basis for both observations: clientWidth (content box)
          // matches ResizeObserver contentRect samples; the iframe's 1px
          // borders would otherwise add a false 2px difference.
          const width=()=>target.evaluate(()=>document.querySelector('iframe[title="결과 뷰어"]').clientWidth);
          const samples=[];
          await target.evaluate(()=>{const node=document.querySelector('iframe[title="결과 뷰어"]');
            window.__resizeSamples=[];window.__observer=new ResizeObserver(entries=>{for(const entry of entries)window.__resizeSamples.push(Math.round(entry.contentRect.width*10)/10);});
            window.__observer.observe(node);});
          const collect=async()=>{const got=await target.evaluate(()=>window.__resizeSamples.slice());samples.push(...got);await target.evaluate(()=>{window.__resizeSamples.length=0;});};
          const settle=async(label)=>{await target.waitForTimeout(500);const value=await width();const child=await childState();await collect();return {label,width:Math.round(value*10)/10,child};};
          const baseline=await childState();
          const points=[];
          points.push(await settle('phone-1'));
          await target.setViewportSize({width:1440,height:900});
          points.push(await settle('wide-1'));
          await target.setViewportSize({width:390,height:844});
          points.push(await settle('phone-2'));
          // Reviewed sequence: open Context while PHONE (its actual modal
          // mode) BEFORE resizing wide — the approved raw/control fixture
          // 356 -> 894 -> 356 -> 538 -> 356. No extra unmatched steps are
          // introduced, and no baseline is claimed for any other sequence.
          await target.getByRole('button',{name:'근거 패널',exact:true}).click();
          await target.locator('dialog[aria-label="문맥 및 근거 패널"]').waitFor({timeout:10000});
          const contextModalAtPhone=await target.locator('dialog[aria-label="문맥 및 근거 패널"]').evaluate(node=>node.matches(':modal'));
          await target.setViewportSize({width:1440,height:900});
          points.push(await settle('wide-context'));
          await target.setViewportSize({width:390,height:844});
          points.push(await settle('phone-final'));
          await collect();
          const finalChild=await childState();
          const route=await target.evaluate(()=>({url:location.pathname+location.search,context:history.state?.haeryongReaderContext}));
          const sameFrame=await target.locator('iframe[title="결과 뷰어"]').evaluate((node,old)=>node===old,frameElement);
          await frameElement.dispose();
          await context.close();
          return {points,samples,baseline,finalChild,route,sameFrame,contextModalAtPhone,deliveries:opens.length};}""")
           .replace('READER', json.dumps(mulgae_route + '?' + urllib.parse.urlencode({'page': 'recovery/inventory.html'}))))
        proof['s11_widths'] = s11
        allow = [point['width'] for point in s11['points']]
        offintent = [sample for sample in s11['samples'] if not any(abs(sample - value) <= 1 for value in allow)]
        narrowTransient = [sample for sample in s11['samples'] if any(abs(sample - value) <= 16 for value in (254,))]
        require('s11_all_samples_match_intended_widths', len(offintent) == 0, {'intended': sorted(set(allow)), 'offending': offintent[:12], 'samples': s11['samples'][:40]})
        require('s11_no_254_files_column_transient', len(narrowTransient) == 0, narrowTransient)
        phone_base = s11['points'][0]
        phone_final = s11['points'][-1]
        require('s11_phone_endpoint_stable', abs(phone_base['width'] - phone_final['width']) <= 1, s11['points'])
        require('s11_final_phone_matches_baseline_scroll_anchor', abs(s11['finalChild']['y'] - s11['baseline']['y']) <= 1 and abs(s11['finalChild']['anchorTop'] - s11['baseline']['anchorTop']) <= 1, {'baseline': s11['baseline'], 'final': s11['finalChild']})
        require('s11_identity_and_zero_extra_delivery', s11['sameFrame'] and s11['baseline']['control'] == s11['finalChild']['control'] == 'missing' and s11['baseline']['selection'] == s11['finalChild']['selection'] and s11['deliveries'] == 1 and s11['route']['context'], s11)

        # Expanded sequence as EXPLICIT natural raw equivalence (not the strict
        # original-return gate): 356 -> 894 -> 356 -> 894 -> 538 -> 356, with
        # Context opened while wide. Expected endpoints come from the executed
        # dimension-matched raw plain-iframe control (Main Gaori 20261019
        # 203010 PASS, receipt /Volumes/RootKernel/tmp/h6raw-g2i_gky6): at the
        # wide-538 step y=4665 / anchorTop=-1324.75 and final phone
        # y=6630 / anchorTop=-1949.5. The candidate must match that NATURAL
        # raw behavior within 1px; this is not a predicate waiver and no
        # script restores scroll. The strict gate above keeps the reviewed
        # phone-modal sequence and its original-baseline return.
        expanded = js((r"""async page=>{
          const opens=[];
          const state=await page.context().storageState();
          const context=await page.context().browser().newContext({viewport:{width:390,height:844},storageState:state});
          const target=await context.newPage();
          target.on('request',r=>{if(r.method()==='POST'&&/\/render-sessions(\?|$)/.test(r.url()))opens.push(r.url());});
          await target.goto(READER);
          const frameLocator=target.frameLocator('iframe[title="결과 뷰어"]');
          await frameLocator.locator('#M03').waitFor({timeout:20000});
          const frameElement=await target.locator('iframe[title="결과 뷰어"]').elementHandle();
          const authored=await frameElement.contentFrame();
          await authored.locator('#inventory-source').selectOption('missing');
          await authored.locator('#M03').evaluate(el=>{el.scrollIntoView();const text=el.querySelector('p')||el;const range=document.createRange();range.selectNodeContents(text);const selection=getSelection();selection.removeAllRanges();selection.addRange(range);document.activeElement?.blur();});
          const childState=()=>authored.evaluate(()=>({control:document.getElementById('inventory-source').value,y:scrollY,anchorTop:document.getElementById('M03').getBoundingClientRect().top,selection:getSelection().toString()}));
          const width=()=>target.evaluate(()=>document.querySelector('iframe[title="결과 뷰어"]').clientWidth);
          const samples=[];
          await target.evaluate(()=>{const node=document.querySelector('iframe[title="결과 뷰어"]');
            window.__resizeSamples=[];window.__observer=new ResizeObserver(entries=>{for(const entry of entries)window.__resizeSamples.push(Math.round(entry.contentRect.width*10)/10);});
            window.__observer.observe(node);});
          const collect=async()=>{const got=await target.evaluate(()=>window.__resizeSamples.slice());samples.push(...got);await target.evaluate(()=>{window.__resizeSamples.length=0;});};
          const settle=async(label)=>{await target.waitForTimeout(500);const value=await width();const child=await childState();await collect();return {label,width:Math.round(value*10)/10,child};};
          const baseline=await childState();
          const points=[];
          points.push(await settle('phone-1'));
          await target.setViewportSize({width:1440,height:900});
          points.push(await settle('wide-1'));
          await target.setViewportSize({width:390,height:844});
          points.push(await settle('phone-2'));
          await target.setViewportSize({width:1440,height:900});
          points.push(await settle('wide-2'));
          await target.getByRole('button',{name:'근거 패널',exact:true}).click();
          await target.locator('dialog[aria-label="문맥 및 근거 패널"]').waitFor({timeout:10000});
          points.push(await settle('wide-context'));
          await target.setViewportSize({width:390,height:844});
          points.push(await settle('phone-final'));
          await collect();
          const finalChild=await childState();
          const sameFrame=await target.locator('iframe[title="결과 뷰어"]').evaluate((node,old)=>node===old,frameElement);
          await frameElement.dispose();
          await context.close();
          return {points,samples,baseline,finalChild,sameFrame,deliveries:opens.length};}""")
           .replace('READER', json.dumps(mulgae_route + '?' + urllib.parse.urlencode({'page': 'recovery/inventory.html'}))))
        proof['s11_expanded_raw_equivalence'] = expanded
        raw_wide_context = {'y': 4665, 'anchorTop': -1324.75}
        raw_final = {'y': 6630, 'anchorTop': -1949.5}
        wide_context_point = next(point for point in expanded['points'] if point['label'] == 'wide-context')
        require('s11_expanded_matches_raw_control', abs(wide_context_point['child']['y'] - raw_wide_context['y']) <= 1 and abs(wide_context_point['child']['anchorTop'] - raw_wide_context['anchorTop']) <= 1
                and abs(expanded['finalChild']['y'] - raw_final['y']) <= 1 and abs(expanded['finalChild']['anchorTop'] - raw_final['anchorTop']) <= 1,
                {'raw_wide_context': raw_wide_context, 'raw_final': raw_final, 'points': expanded['points'], 'final': expanded['finalChild'], 'note': 'natural raw equivalence of the expanded sequence; the strict original-return gate remains the reviewed phone-modal sequence above'})
        allow_expanded = [point['width'] for point in expanded['points']]
        offintent_expanded = [sample for sample in expanded['samples'] if not any(abs(sample - value) <= 1 for value in allow_expanded)]
        require('s11_expanded_no_unintended_widths', len(offintent_expanded) == 0 and expanded['sameFrame'] and expanded['deliveries'] == 1 and expanded['baseline']['selection'] == expanded['finalChild']['selection'], {'intended': sorted(set(allow_expanded)), 'offending': offintent_expanded[:10]})

        # Supplemental owned A/B fixture: real public CLI imports, same
        # document/path with different bytes. Route changes stay inside one
        # mounted app; full navigation would hide stale-selection regressions.
        if standalone:
            download_source = work / 'download-version-fixture'
            download_source.mkdir(mode=0o700)
            (download_source / 'first.html').write_text('<!doctype html><h1>Download version fixture</h1>')
            a_bytes, b_bytes = b'captured version A\n', b'explicit version B\n'
            (download_source / 'same.txt').write_bytes(a_bytes)
            _, _, a_pub = product.import_tree(owner, download_source)
            (download_source / 'same.txt').write_bytes(b_bytes)
            _, _, b_pub = product.import_tree(owner, download_source, extra=['--document', a_pub['document_id']])
            require('download_fixture_same_document_different_version_bytes', a_pub['document_id'] == b_pub['document_id'] and a_pub['version_id'] != b_pub['version_id'] and hashlib.sha256(a_bytes).hexdigest() != hashlib.sha256(b_bytes).hexdigest(), {'document_id': a_pub['document_id'], 'a': a_pub['version_id'], 'b': b_pub['version_id'], 'a_sha256': hashlib.sha256(a_bytes).hexdigest(), 'b_sha256': hashlib.sha256(b_bytes).hexdigest()})
            a_route = f"/documents/{a_pub['document_id']}/versions/{a_pub['version_id']}?page=first.html"
            b_route = f"/documents/{b_pub['document_id']}/versions/{b_pub['version_id']}?page=first.html"
            download_flow = js(("""async page=>{
              const state=await page.context().storageState(),results=[];
              for(const input of ['keyboard','touch']){
                const context=await page.context().browser().newContext({viewport:{width:390,height:844},hasTouch:true,storageState:state});
                const target=await context.newPage();await target.goto(ORIGIN+AROUTE);
                await target.frameLocator('iframe[title="결과 뷰어"]').locator('h1').waitFor();
                const activate=async(node)=>{if(input==='touch')await node.tap();else{await node.focus();await target.keyboard.press('Enter');}};
                await activate(target.getByRole('button',{name:'파일 목록',exact:true}));
                await activate(target.locator('#reader-files-panel').getByRole('button',{name:'same.txt',exact:true}));
                await target.locator('#reader-files-panel').waitFor({state:'hidden'});
                const region=target.getByRole('region',{name:'선택한 파일 내려받기',exact:true});await region.waitFor();
                const focused=await region.evaluate(node=>document.activeElement===node);
                const start=region.getByRole('button',{name:'내려받기 시작',exact:true});
                const reachable=await start.isVisible()&&await start.isEnabled();
                if(input==='keyboard'){await target.keyboard.press('Tab');await target.keyboard.press('Tab');}
                const keyboardReached=input!=='keyboard'||await start.evaluate(node=>document.activeElement===node);
                await activate(start);
                const consentIsA=await target.locator('[data-testid="download-panel"]').getAttribute('data-version')===AVERSION;
                // Existing router observation, labelled route setup. No reload,
                // credential change or ReaderOwner replacement by document ID.
                await target.evaluate(path=>{history.pushState({haeryongReaderContext:crypto.randomUUID()},'',path);dispatchEvent(new PopStateEvent('popstate'));},BROUTE);
                await target.waitForFunction(version=>document.querySelector('.reader-header .muted-code')?.textContent===version,BVERSION);
                await region.waitFor({state:'hidden'});
                await activate(target.getByRole('button',{name:'파일 목록',exact:true}));
                await activate(target.locator('#reader-files-panel').getByRole('button',{name:'same.txt',exact:true}));
                await region.waitFor();
                const explicitB=await target.locator('[data-testid="download-panel"]').getAttribute('data-version')===BVERSION&&await region.getByRole('button',{name:'내려받기 시작',exact:true}).isVisible();
                await target.evaluate(path=>{history.pushState({haeryongReaderContext:crypto.randomUUID()},'',path);dispatchEvent(new PopStateEvent('popstate'));},AROUTE);
                await target.waitForFunction(version=>document.querySelector('.reader-header .muted-code')?.textContent===version,AVERSION);
                await region.waitFor({state:'hidden'});
                results.push({input,focused,reachable,keyboardReached,consentIsA,oldSelectionDiscarded:true,explicitB,returnADiscarded:true,routeSetup:'same mounted router pushState/popstate; no page reload'});
                await context.close();
              }return results;}""")
              .replace('ORIGIN', json.dumps(endpoint)).replace('AROUTE', json.dumps(a_route)).replace('BROUTE', json.dumps(b_route))
              .replace('AVERSION', json.dumps(a_pub['version_id'])).replace('BVERSION', json.dumps(b_pub['version_id'])))
            proof['download_selection_flow'] = download_flow
            for flow in download_flow:
                require('phone_download_choice_and_captured_version_' + flow['input'], all(flow[key] for key in ('focused', 'reachable', 'keyboardReached', 'consentIsA', 'oldSelectionDiscarded', 'explicitB', 'returnADiscarded')), flow)
        else:
            proof['download_selection_fixture'] = 'Standalone owned A/B regression; attached native qualification retains its separately owned published corpus and native raw-byte downloads.'

        # Layout verified by MEASURED computed position + bounding rects on
        # desktop AND tablet (never CSS text): in-flow Files grid item, one
        # flexible viewer canvas, the declared 340px Context column at wide.
        wideGeometry = js(("""async page=>{
          const state=await page.context().storageState();
          const results={};
          for(const viewport of [{width:1440,height:900},{width:820,height:1180}]){
            const context=await page.context().browser().newContext({viewport,storageState:state});
            const target=await context.newPage();
            await target.goto(READER);
            await target.frameLocator('iframe[title="결과 뷰어"]').locator('h1').first().waitFor({timeout:20000});
            if(viewport.width>=1280){await target.getByRole('button',{name:'근거 패널',exact:true}).click();
              await target.locator('dialog[aria-label="문맥 및 근거 패널"]').waitFor({timeout:10000});}
            results[viewport.width]=await target.evaluate(()=>{
              const measure=(selector)=>{const el=document.querySelector(selector);if(!el)return null;const r=el.getBoundingClientRect();const s=getComputedStyle(el);
                return {x:Math.round(r.x),y:Math.round(r.y),w:Math.round(r.width),h:Math.round(r.height),position:s.position,margin:s.margin};};
              return {shell:measure('.shell-sidebar'),files:measure('dialog[aria-label="페이지 및 파일 목록"]'),viewer:measure('iframe[title="결과 뷰어"]'),contextPanel:measure('dialog[aria-label="문맥 및 근거 패널"]'),iframes:document.querySelectorAll('iframe').length};});
            await context.close();}
          return results;}""")
           .replace('READER', json.dumps(READER_URL)))
        wide = wideGeometry['1440']
        tablet = wideGeometry['820']
        require('wide_files_in_flow', wide['files']['position'] == 'static' and wide['files']['margin'] == '0px' and wide['files']['w'] >= 150 and wide['files']['x'] >= wide['shell']['x'] + wide['shell']['w'] - 2, wide)
        require('wide_340_context_column_measured', wide['contextPanel'] is not None and 300 <= wide['contextPanel']['w'] <= 380
                and wide['contextPanel']['position'] == 'static' and wide['contextPanel']['x'] >= wide['viewer']['x'] + wide['viewer']['w'] and 200 <= wide['shell']['w'] <= 240, wide)
        require('wide_viewer_canvas_single_and_flexible', wide['iframes'] == 1 and wide['viewer']['w'] >= 400 and wide['viewer']['x'] > wide['files']['x'] + wide['files']['w'] - 2, wide)
        require('tablet_files_in_flow_two_columns', tablet['files']['position'] == 'static' and tablet['files']['w'] >= 150 and tablet['files']['x'] >= tablet['shell']['x'] + tablet['shell']['w'] - 2
                and tablet['viewer']['w'] >= 300 and tablet['viewer']['x'] > tablet['files']['x'] + tablet['files']['w'] - 2 and tablet['iframes'] == 1, tablet)
        proof['wide_geometry'] = wideGeometry

        # ---- L1 semantic exact-version Library entry (결과 읽기) ----
        # Actual UI entry only (no deep-link substitute): plain click, keyboard
        # Enter and emulated touch activate the semantic link; a retained
        # browser session reopens the SAME stored version from Library and
        # history/back returns coherently. The missing-latest action is
        # verified once through a labelled synthetic response-level row and
        # never presented as product data.
        libraryEntry = js(("""async page=>{
          const root='/documents/'+DOC+'/versions/'+VID;
          const open=async(label)=>{
            await page.goto(ORIGIN+'/library');
            const row=page.locator('[data-testid="library-document"][data-document="'+DOC+'"]');
            const link=row.getByRole('link',{name:'결과 읽기',exact:true});
            await link.waitFor({timeout:20000});
            const href=await link.getAttribute('href');
            const activate=async()=>{if(label==='enter'){await link.focus();await page.keyboard.press('Enter');}else await link.click();};
            await activate();
            await page.waitForURL(u=>u.pathname===root,{timeout:20000});
            let backCoherent=true;
            if(label==='click'){
              // Back from the exact-version ROOT, before a later explicit
              // Files page choice creates its own legitimate history entry.
              await page.goBack();
              backCoherent=await page.evaluate(()=>location.pathname==='/library');
              await link.waitFor();await link.click();
              await page.waitForURL(u=>u.pathname===root,{timeout:20000});
            }
            await page.waitForFunction(()=>!!document.querySelector('iframe[title="결과 뷰어"]')||[...document.querySelectorAll('p')].some(p=>p.textContent==='페이지를 선택하거나 원본 파일을 내려받으세요.'),null,{timeout:20000});
            if(!await page.locator('iframe[title="결과 뷰어"]').count()){
              await page.locator('#reader-files-panel').getByRole('link',{name:'recovery/inventory.html',exact:true}).click();
            }
            await page.frameLocator('iframe[title="결과 뷰어"]').locator('h1').first().waitFor({timeout:20000});
            const version=(await page.locator('.reader-header p.muted-code').first().textContent()).trim();
            return {href,url:page.url(),version,backCoherent,loadedAuthoredPage:true};};
          const clickEntry=await open('click'),enterEntry=await open('enter'),retained=await open('retained');
          return {clickEntry,expectedVersion:VID,hrefMatches:clickEntry.href===root,versionShown:clickEntry.version===VID,backCoherent:clickEntry.backCoherent,enterMatches:enterEntry.href===root&&enterEntry.version===VID,retainedMatches:retained.href===root&&retained.version===VID&&retained.loadedAuthoredPage};}""")
           .replace('ORIGIN', json.dumps(endpoint)).replace('DOC', json.dumps(published['mulgae']['document_id'])).replace('VID', json.dumps(published['mulgae']['version_id'])))
        require('library_entry_exact_version_click', libraryEntry['hrefMatches'] and libraryEntry['versionShown'] and libraryEntry['clickEntry']['loadedAuthoredPage'], libraryEntry)
        require('library_entry_enter_and_back', libraryEntry['enterMatches'] and libraryEntry['backCoherent'], libraryEntry)
        require('library_entry_retained_browser_reopen', libraryEntry['retainedMatches'], libraryEntry)
        touchEntry = js(("""async page=>{
          const state=await page.context().storageState();
          const context=await page.context().browser().newContext({viewport:{width:390,height:844},hasTouch:true,storageState:state});
          const target=await context.newPage();
          await target.goto(ORIGIN+'/library');
          const link=target.locator('[data-testid="library-document"][data-document="'+DOC+'"]').getByRole('link',{name:'결과 읽기',exact:true});
          await link.waitFor({timeout:20000});const href=await link.getAttribute('href');await link.tap();
          await target.waitForURL(u=>u.pathname==='/documents/'+DOC+'/versions/'+VID,{timeout:20000});
          await target.waitForFunction(version=>document.querySelector('.reader-header p.muted-code')?.textContent===version,VID,{timeout:20000});
          const toggle=target.getByRole('button',{name:'파일 목록',exact:true});await toggle.waitFor({timeout:10000});
          await target.waitForFunction(()=>!!document.querySelector('iframe[title="결과 뷰어"]')||[...document.querySelectorAll('p')].some(p=>p.textContent==='페이지를 선택하거나 원본 파일을 내려받으세요.'),null,{timeout:20000});
          if(!await target.locator('iframe[title="결과 뷰어"]').count()){
            await toggle.tap();await target.locator('#reader-files-panel').getByRole('link',{name:'recovery/inventory.html',exact:true}).tap();
          }
          await target.frameLocator('iframe[title="결과 뷰어"]').locator('h1').first().waitFor({timeout:20000});
          const version=(await target.locator('.reader-header p.muted-code').first().textContent()).trim();
          await context.close();return {href,version,phoneFilesToggle:true,loadedAuthoredPage:true};}""")
           .replace('ORIGIN', json.dumps(endpoint)).replace('DOC', json.dumps(published['mulgae']['document_id'])).replace('VID', json.dumps(published['mulgae']['version_id'])))
        require('library_entry_emulated_touch', touchEntry['href']==f"/documents/{published['mulgae']['document_id']}/versions/{published['mulgae']['version_id']}" and touchEntry['version']==published['mulgae']['version_id'] and touchEntry['loadedAuthoredPage'], touchEntry)
        missingLatest = js(("""async page=>{
          const state=await page.context().storageState();
          const context=await page.context().browser().newContext({viewport:{width:1440,height:900},storageState:state});
          const target=await context.newPage();
          // Labelled synthetic response-level absence check: nullify the
          // latest version id in ONE served list response; no product data or
          // route is rewritten.
          await context.route('**/api/v1/documents*',async route=>{
            const response=await route.fetch();
            const body=await response.json();
            for(const item of body?.data?.items??[])item.latest_version_id=null;
            await route.fulfill({response,body:JSON.stringify(body)});
          });
          await target.goto(ORIGIN+'/library');
          await target.waitForSelector('[data-testid="library-document"]');
          const action=target.locator('[data-testid="library-document"]').first().getByRole('button',{name:'결과 읽기',exact:true});
          const disabled=await action.isDisabled();
          // The missing-latest explanation is visibly readable (phone touch),
          // not title-only, and describes only the null latest pointer.
          const describedBy=await action.getAttribute('aria-describedby');
          const hint=describedBy?await target.locator('#'+describedBy).textContent():null;
          const hintVisible=!!hint&&hint.includes('현재 읽을 최신 버전이 없습니다')&&await target.locator('#'+describedBy).isVisible();
          await action.evaluate(node=>node.click());
          const stayed=!target.url().includes('/documents/');
          await context.close();
          return {disabled,hintVisible,stayed};}""")
           .replace('ORIGIN', json.dumps(endpoint)))
        require('library_entry_missing_latest_honest_disabled', missingLatest['disabled'] and missingLatest['stayed'] and missingLatest['hintVisible'], missingLatest)
        proof['library_entry'] = {**libraryEntry, 'touch': touchEntry, 'missing_latest_synthetic': missingLatest}

        # ---- Region-capable fixture (standalone session, labelled CLI setup):
        # real focus success with a matching DOM id AND the honest missing-id
        # refusal; logical regions never become a mandatory author DOM schema.
        if standalone:
            region_root = work / 'region-fixture'
            (region_root / 'nested').mkdir(parents=True)
            (region_root / 'first.html').write_text(
                '<!doctype html><meta charset=utf-8><title>Region first</title><h1>영역 픽스처</h1>'
                '<section id="overview"><h2>첫 개요</h2><p>개요 본문</p></section>'
                '<section id="body"><h2>본문</h2><p>본문 내용</p></section>', encoding='utf-8')
            (region_root / 'nested' / 'second.html').write_text('<!doctype html><meta charset=utf-8><title>Region second</title><h1>둘째</h1>', encoding='utf-8')
            region_meta = {'version': {'source_intent': 'historical'},
                           'sources': [{'id': 'repo-region', 'kind': 'git', 'retention_mode': 'reference_only',
                                        'repo_id': '11111111-2222-4333-8444-555555555555', 'object_format': 'sha1',
                                        'exact_revision': '0123456789abcdef0123456789abcdef01234567'}],
                           'evidence': [{'id': 'ev-overview', 'source_id': 'repo-region', 'capture_status': 'reference_only', 'path': 'notes/first.txt', 'line_start': 1, 'line_end': 4}],
                           'regions': [{'id': 'overview', 'page_path': 'first.html', 'label': '첫 개요', 'evidence_ids': ['ev-overview']},
                                       {'id': 'missing-in-dom', 'page_path': 'first.html', 'label': '문서에 없는 영역', 'evidence_ids': []}]}
            meta_path = work / 'region-metadata.json'
            meta_path.write_text(json.dumps(region_meta, ensure_ascii=False))
            _, _, region_published = product.import_tree(owner, region_root, extra=['--metadata', str(meta_path)])
            region_route = endpoint + f"/documents/{region_published['document_id']}/versions/{region_published['version_id']}?" + urllib.parse.urlencode({'page': 'first.html'})
            regionCheck = js(("""async page=>{
              await page.goto(ROUTE);
              await page.frameLocator('iframe[title="결과 뷰어"]').locator('h1').first().waitFor({timeout:20000});
              await page.getByRole('button',{name:'근거 패널',exact:true}).click();
              const outline=page.locator('.region-outline');
              const panel=page.locator('section[aria-label="근거 및 출처 패널"]');
              await outline.waitFor({timeout:10000});
              const notice=page.locator('.reader > p[role="status"]');
              await outline.getByRole('button',{name:'첫 개요',exact:true}).click();
              await notice.filter({hasText:'영역 overview에 초점을 맞췄습니다.'}).waitFor({timeout:10000});
              const focused=true;
              // The selection binds the EXACT region context: the panel names
              // the region and the outline marks the chosen entry.
              await panel.locator('h2').filter({hasText:'영역 overview'}).waitFor({timeout:10000});
              const bound=true;
              const selected=await outline.getByRole('button',{name:'첫 개요',exact:true}).getAttribute('data-selected');
              await outline.getByRole('button',{name:'문서에 없는 영역',exact:true}).click();
              await notice.filter({hasText:'missing-in-dom'}).filter({hasText:'찾지 못했습니다'}).waitFor({timeout:10000});
              const missing=true;
              await outline.getByRole('button',{name:'첫 개요',exact:true}).focus();
              await page.keyboard.press('Enter');
              await notice.filter({hasText:'영역 overview에 초점을 맞췄습니다.'}).waitFor({timeout:10000});
              const keyboardRoute=true;
              return {focused,bound,selected:selected==='true',missing,keyboardRoute};}""")
               .replace('ROUTE', json.dumps(region_route)))
            require('region_selection_binds_context_and_focuses', regionCheck['focused'] and regionCheck['bound'] and regionCheck['selected'] and regionCheck['keyboardRoute'], regionCheck)
            require('region_missing_dom_id_stays_honest', regionCheck['missing'], regionCheck)
            # Narrow modal flow: selecting a region CLOSES the modal Context
            # first, then the same live channel receives real focus; the
            # reopened panel still shows the chosen region.
            modalRegion = js(("""async page=>{
              const state=await page.context().storageState();
              const context=await page.context().browser().newContext({viewport:{width:390,height:844},hasTouch:true,storageState:state});
              const target=await context.newPage();
              await target.goto(ROUTE);
              await target.frameLocator('iframe[title="결과 뷰어"]').locator('h1').first().waitFor({timeout:20000});
              await target.getByRole('button',{name:'근거 패널',exact:true}).tap();
              const dialog=target.locator('dialog[aria-label="문맥 및 근거 패널"]');
              await dialog.waitFor({timeout:10000});
              const modalBefore=await dialog.evaluate(node=>node.matches(':modal'));
              await target.locator('.region-outline').getByRole('button',{name:'첫 개요',exact:true}).tap();
              await target.waitForFunction(()=>![...document.querySelectorAll('p[role="status"]')].some(p=>p.textContent.includes('초점을 맞추지 못했습니다'))&&[...document.querySelectorAll('p[role="status"]')].some(p=>p.textContent.includes('초점을 맞췄습니다')),null,{timeout:10000});
              const closedAfterSelect=!(await target.locator('dialog[aria-label="문맥 및 근거 패널"][open]').count());
              await target.getByRole('button',{name:'근거 패널',exact:true}).tap();
              await dialog.waitFor({timeout:10000});
              const panel=target.locator('section[aria-label="근거 및 출처 패널"]');
              await panel.locator('h2').filter({hasText:'영역 overview'}).waitFor({timeout:10000});
              const selectedAfterReopen=await target.locator('.region-outline').getByRole('button',{name:'첫 개요',exact:true}).getAttribute('data-selected');
              await context.close();
              return {modalBefore,closedAfterSelect,reopenKeepsSelection:selectedAfterReopen==='true'};}""")
               .replace('ROUTE', json.dumps(region_route)))
            require('region_modal_closes_then_focuses_same_channel', modalRegion['modalBefore'] and modalRegion['closedAfterSelect'] and modalRegion['reopenKeepsSelection'], modalRegion)
            proof['region_fixture'] = {'setup': 'labelled CLI metadata import in the standalone session only', **regionCheck, 'modal_flow': modalRegion}
        else:
            proof['region_fixture'] = {'skipped': 'attached shared-daemon session keeps fixture setup with its daemon owner (native driver); standalone self-verify covers the region fixture'}

        # Provider absence never hides stored pages; X05 honest not-ready.
        providerAbsence = js(("""async page=>{
          await page.goto(READER);
          await page.frameLocator('iframe[title="결과 뷰어"]').locator('h1').first().waitFor({timeout:20000});
          const storedReadable=await page.frameLocator('iframe[title="결과 뷰어"]').locator('#M01').count();
          await page.getByRole('button',{name:'근거 패널',exact:true}).click();
          const panel=page.locator('section[aria-label="근거 및 출처 패널"]');
          await panel.waitFor({timeout:10000});
          const absent=await page.getByText('공급된 소스/근거 메타데이터 없음',{exact:false}).first().isVisible();
          const disabled=await panel.getByRole('button',{name:'출처 근거 확인(제공되지 않음)',exact:true}).isDisabled();
          return {storedReadable:storedReadable===1,metadataAbsent:absent,sourceCheckDisabled:disabled};}""")
           .replace('READER', json.dumps(mulgae_route + '?' + urllib.parse.urlencode({'page': 'recovery/inventory.html'}))))
        require('provider_absence_keeps_stored_pages', providerAbsence['storedReadable'] and providerAbsence['metadataAbsent'] and providerAbsence['sourceCheckDisabled'], providerAbsence)
    else:
        # ---- Cross phase: reopen the native-published exact versions ----
        comparison = {}
        for package in ('mulgae', 'gaori'):
            native = native_versions[package]
            route = endpoint + f"/documents/{native['document_id']}/versions/{native['version_id']}?" + urllib.parse.urlencode({'page': PAGE_OF['R1' if package == 'mulgae' else 'R3'][0]})
            observed = js(("""async page=>{
              await page.goto(ORIGIN+'/library');
              const root='/documents/'+DOC+'/versions/'+VID;
              const link=page.locator('[data-testid="library-document"][data-document="'+DOC+'"]').getByRole('link',{name:'결과 읽기',exact:true});
              await link.waitFor({timeout:20000});const href=await link.getAttribute('href');await link.click();
              await page.waitForURL(u=>u.pathname===root,{timeout:20000});
              await page.waitForFunction(version=>document.querySelector('.reader-header p.muted-code')?.textContent===version,VID,{timeout:20000});
              const libraryEntry={href,version:VID,exactRoot:href===root,retainedSession:true};
              // Separate exact-page setup after real retained-session Library
              // activation. It does not stand in for the entry interaction.
              await page.goto(ROUTE);
              const frame=page.frameLocator('iframe[title="결과 뷰어"]');
              await frame.locator('[data-illustration] output[data-output]').waitFor({timeout:20000});
              const heading=await frame.locator('h1').first().textContent();
              await page.getByRole('button',{name:'근거 패널',exact:true}).click();
              const panel=page.locator('section[aria-label="근거 및 출처 패널"]');
              await panel.waitFor({timeout:10000});
              const absent=await page.getByText('공급된 소스/근거 메타데이터 없음',{exact:false}).first().isVisible();
              const disabled=await panel.getByRole('button',{name:'출처 근거 확인(제공되지 않음)',exact:true}).isDisabled();
              const files=page.locator('dialog[aria-label="페이지 및 파일 목록"]');
              const more=files.getByRole('button',{name:'더 불러오기',exact:true});
              let pages=0;
              while(await more.count()&&!(await more.isDisabled())){await more.click();pages+=1;await page.waitForTimeout(200);}
              const list=await files.locator('ul').allInnerTexts();
              return {url:page.url(),libraryEntry,heading,metadataAbsent:absent,sourceCheckDisabled:disabled,files:list.join('\\n'),pages};}""")
               .replace('ROUTE', json.dumps(route)).replace('ORIGIN', json.dumps(endpoint)).replace('DOC', json.dumps(native['document_id'])).replace('VID', json.dumps(native['version_id'])))
            grant_write_guard(profile, endpoint)
            destination = work / ('cross-download-' + package)
            destination.mkdir()
            js(("""async (page) => {
              await page.getByRole('link',{name:'서재',exact:true}).click();
              const row=page.locator('[data-testid="library-document"][data-document="'+DOC+'"]');
              await row.getByRole('button',{name:'원본 디렉터리 내려받기',exact:true}).click();
              await page.getByRole('button',{name:'내려받기 시작',exact:true}).click();
              await page.getByLabel('위 교체 규칙을 이해하고 동의합니다.').check();
              await page.getByRole('button',{name:'동의하고 계속',exact:true}).click();
              const zone=page.locator('[data-testid="download-drop-zone"]');
              await zone.scrollIntoViewIfNeeded();const box=await zone.boundingBox();
              const session=await page.context().newCDPSession(page);
              const data={items:[],files:[DEST],dragOperationsMask:1};
              for(const type of ['dragEnter','dragOver','drop'])await session.send('Input.dispatchDragEvent',{type,x:box.x+30,y:box.y+30,data});
              await page.waitForSelector('[data-testid="download-permission"]',{timeout:20000});
              await page.getByRole('button',{name:'이 폴더에 저장',exact:true}).click();
              await page.waitForSelector('[data-testid="download-done"]',{timeout:240000});
              return true;}""")
               .replace('DOC', json.dumps(native['document_id'])).replace('DEST', json.dumps(str(destination))))
            archive = next(destination.glob('*.zip'))
            with zipfile.ZipFile(archive) as bundle:
                names = [name for name in bundle.namelist() if not name.endswith('/')]
                hashes = {name: hashlib.sha256(bundle.read(name)).hexdigest() for name in names}
            expected = native.get('entry_sha256') or corpus[package]['entries']
            transport_metadata = browser_metadata(native['version_id'], 'recovery/inventory.html' if package == 'mulgae' else 'gaori/lifecycle.html')
            comparison[package] = {
                'transport_metadata': transport_metadata,
                'library_entry': observed['libraryEntry'],
                'document_id': native['document_id'], 'version_id': native['version_id'],
                'heading': observed['heading'], 'metadata_absent_state': observed['metadataAbsent'],
                'source_check_disabled': observed['sourceCheckDisabled'],
                'files_listed': observed['files'],
                'entry_set_equal': set(names) == set(corpus[package]['entries']),
                'entry_hashes_equal': hashes == {name: expected[name] for name in names if name in expected},
                'expected_device_differences': 'session/attachment identities and native-only affordances may differ; payloads compared by exact identity/bytes'}
            require('cross_library_retained_exact_entry_' + package, observed['libraryEntry']['exactRoot'] and observed['libraryEntry']['version'] == native['version_id'], observed['libraryEntry'])
            require('cross_entries_' + package, comparison[package]['entry_set_equal'] and comparison[package]['entry_hashes_equal'], comparison[package])
            shot('cross-' + package)
        # The pinned native owner reads receipt.cross.{mulgae,gaori}.
        proof['cross'] = comparison
        js(("""async page=>{await page.goto(ROUTE);await page.frameLocator('iframe[title="결과 뷰어"]').locator('h1').first().waitFor({timeout:20000});return true;}""").replace('ROUTE', json.dumps(route)))
        proof['cross_isolation_setup'] = 'Returned to the observed exact Reader after real Library archive download, solely for the separate authored-isolation probe.'

    # ---- Shared closing observations (both phases) ----
    isolation = js("""async page=>await page.frameLocator('iframe[title="결과 뷰어"]').locator('html').evaluate(()=>{
      let parentDOMDenied=false,parentStorageDenied=false;
      try{void parent.document.body;}catch(e){parentDOMDenied=true;}
      try{void parent.localStorage.length;}catch(e){parentStorageDenied=true;}
      return {origin:location.origin,parentDOMDenied,parentStorageDenied,nativeBridgeAbsent:typeof window.haeryongNativeCall==='undefined'};
    })""")
    require('actual_authored_origin_keeps_parent_dom_storage_native_bridge_separate', isolation['parentDOMDenied'] and isolation['parentStorageDenied'] and isolation['nativeBridgeAbsent'], isolation)
    proof['authored_isolation'] = isolation
    health = js("async page=>({errors:page.qaErrors||[],expectedSandboxWarnings:page.qaExpectedWarnings||[],url:page.url()})")
    health['sandbox_warning_disposition'] = 'Exact Chromium warning observed for the initially empty about:blank sandbox (allow-scripts + allow-same-origin). Authored bytes load only at the separate renderer origin; actual loaded-child DOM/storage/bridge denial is asserted. Sandbox permissions and product guards are unchanged.'

    require('console_health', not health['errors'], health['errors'][:6])
    proof['console_health'] = health
    proof['companion_absent'] = 'no Wails runtime/companion process was launched or contacted; browser reading used only the isolated daemon'
    # Every required observation passed: mark the receipt complete for the
    # native owner. A raised require() never reaches this line, so a failed
    # run is never falsely marked complete.
    proof['complete'] = True
finally:
    if opened:
        call(['close'])
        proof['browser_closed'] = True
    if product is not None and product.process is not None:
        product.stop()
        proof['daemon_closed'] = product.process is None
    else:
        proof['daemon_closed'] = not standalone
    RECEIPT.write_text(json.dumps(proof, ensure_ascii=False, indent=2) + '\n')
if standalone:
    shutil.rmtree(work, ignore_errors=True)
print(json.dumps({'complete': proof.get('complete', False), 'phase': args.phase, 'cases': len(proof['cases']), 'failures': failures,
                  'browser_closed': proof.get('browser_closed', False), 'daemon_closed': proof.get('daemon_closed', False)}))
assert proof.get('complete') and proof.get('browser_closed') and proof.get('daemon_closed'), 'run did not complete every required observation'
