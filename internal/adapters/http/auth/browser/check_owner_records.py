"""Task6 actual Chrome owner-record journey; no native/onboarding qualification."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

REPO = Path(__file__).resolve().parents[5]
import importlib.util

def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    return module

support = load('owner_support', REPO / 'tests/manual/owner_records_support.py')
h = load('owner_harness', REPO / 'tests/e2e/harness.py')
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--browser-cli', type=Path, required=True)
parser.add_argument('--receipt', type=Path, required=True)
parser.add_argument('--focus-context', action='store_true', help='Focused own-Save/Review and context classes only; never full campaign qualification')
parser.add_argument('--focus-purge-sources', action='store_true', help='Only actual managed snapshot purge-plan rendering and cancellation; not the full owner campaign')
args = parser.parse_args()
assert not (args.focus_context and args.focus_purge_sources), 'select one bounded focus'
receipt = args.receipt.resolve(); assert receipt.parent.is_dir()
os.umask(0o077)
volume = Path('/Volumes/RootKernel')
scratch = Path('/tmp')
work = Path(tempfile.mkdtemp(prefix='h6b-', dir=scratch)).resolve()
sockets = work / 'sockets'; sockets.mkdir()
env = os.environ | {'TMPDIR': str(work), 'PYTHONDONTWRITEBYTECODE': '1', 'HAERYONG_TEST_STATE_DIR': str(work/'app-state'),
                    'PLAYWRIGHT_DAEMON_SOCKETS_DIR': str(sockets), 'PLAYWRIGHT_DAEMON_SESSION_DIR': str(work/'registry'),
                    'GOTOOLCHAIN': 'local', 'GOPROXY': 'off', 'GOSUMDB': 'off', 'GOFLAGS': '-mod=readonly'}
os.environ['HAERYONG_TEST_STATE_DIR'] = env['HAERYONG_TEST_STATE_DIR']
proof = {'scope': __doc__, 'complete': False, 'runtime': str(work), 'cases': [], 'client': 'Chrome',
         'reader': 'agent-only, familiar, not blinded; no human usefulness claim', 'provider_calls': 0,
         'requested': ['create','successors','loss','stale','lifecycle','reopen','interruption','recovery','export'], 'cleanup_errors': []}
proof['focused_selection'] = 'managed-purge-sources' if args.focus_purge_sources else args.focus_context
proof['browser_path'] = 'Browser plugin not available; existing Playwright CLI fallback'
proof['full_qualification'] = False
if args.focus_context:
    proof['excluded'] = list(proof['requested'][1:])+['consent','normal-restart','protected-recovery']
    proof['requested'] = ['create','declared-records','context-case-inputs','context-classes']
if args.focus_purge_sources:
    proof['excluded'] = list(proof['requested'])
    proof['requested'] = ['managed-source-setup', 'purge-render-cancel-keyboard-touch', 'retained-after-cancel']
class FocusedContextFinished(Exception):
    pass
product = None; opened = False
before = support.fingerprints()

def call(arguments, result=False):
    number=len(proof.setdefault('cli_calls',[]))
    actual_argv=[str(args.browser_cli.resolve(strict=True)), '--session='+work.name, *arguments]
    input_path=work/('cli-call-'+str(number)+'.input.json')
    input_path.write_text(json.dumps({'argv':actual_argv,'cwd':str(work),'environment':{key:env[key] for key in ('TMPDIR','GIT_OPTIONAL_LOCKS','PLAYWRIGHT_DAEMON_SOCKETS_DIR','PLAYWRIGHT_DAEMON_SESSION_DIR')}},ensure_ascii=False)+'\n')
    input_path.chmod(0o600)
    run = subprocess.run(actual_argv, cwd=work, env=env, capture_output=True, text=True, timeout=600)
    raw=work/('cli-call-'+str(number)+'.stdout')
    raw.write_text(run.stdout);raw.chmod(0o600)
    stderr=work/('cli-call-'+str(number)+'.stderr');stderr.write_text(run.stderr);stderr.chmod(0o600)
    daemon_errors=[]
    for original in (work/'registry').rglob('*.err'):
        saved=work/('cli-call-'+str(number)+'-'+original.name);saved.write_bytes(original.read_bytes());saved.chmod(0o600);daemon_errors.append(str(saved))
    proof['cli_calls'].append({'action':arguments[0],'exit':run.returncode,'stdout':str(raw),'stderr':str(stderr),'daemon_errors':daemon_errors,'effective_input':str(input_path),'effective_input_sha256':hashlib.sha256(input_path.read_bytes()).hexdigest()})
    if run.returncode or '### Error' in run.stdout:
        message = (run.stdout if '### Error' in run.stdout else run.stderr)
        for client in product.clients if product else []:
            message = message.replace(client['token'], '[redacted]')
        raise AssertionError('browser CLI: ' + message[-3000:])
    if result:
        match = re.search(r'### Result\s*\n', run.stdout); assert match, 'browser result absent'
        return json.JSONDecoder().raw_decode(run.stdout[match.end():].lstrip())[0]
    return run.stdout


def phase(name, expected):
    if name=='reopen' and not fixture.get('declared_records_validated'):
        proof['cases'].append({'phase':name,'complete':False,'rows':[{'name':case,'status':'blocked','detail':'own Save/Review prerequisite failed'} for case in expected]})
        raise AssertionError('BLOCKED: own Save/Review prerequisite failed')
    constants = {'origin': product.endpoint, 'token': owner['token'], 'peer_token':peer['token'], 'fixture': fixture, 'phase': name, 'expected': expected,'work':str(work),'invocation':len(proof['cli_calls'])}
    source = Path(__file__).with_suffix('.js').read_text().replace('OWNER_CONSTANTS', json.dumps(constants, ensure_ascii=False)).replace('OWNER_CONSENT_SOURCE',json.dumps((REPO/'tests/manual/notes_confirmation_cases.js').read_text())).replace('OWNER_CONTEXT_SOURCE',json.dumps((REPO/'tests/manual/notes_context_cases.js').read_text()))
    result = call(['run-code', source], True)
    proof['cases'].append(result)
    fixture.setdefault('known_note_ids',[]).extend(note for note in result.get('known_note_ids',[]) if note not in fixture.get('known_note_ids',[]))
    if name=='dialog-pending' and result['complete']: fixture['restart_draft']=result['rows'][0]['detail']['draft']
    assert result['complete'], 'browser phase failed: ' + name


def check(name, fn):
    try:
        value = fn(); proof['cases'].append({'name': name, 'status': 'pass', 'detail': value}); return True
    except Exception as error:
        proof['cases'].append({'name': name, 'status': 'fail', 'error': str(error)})


def leave_with_draft():
    # MCP can return the actual native-dialog observation before run-code's
    # navigation Promise settles. The next command observes that SAME action;
    # it never repeats navigation or confirms a Notes operation.
    observed=call(['run-code', 'async page=>{await page.goto('+json.dumps(product.endpoint+'/health/live')+');await page.evaluate(()=>{window.__ownerBrowserLeave={supportingBrowserLeave:true,retained:Object.keys(localStorage).some(k=>k.startsWith("haeryong.notes.v1:")&&JSON.parse(localStorage.getItem(k)).draft?.text==="Task6 restart retained local draft")};});}'])
    assert '["beforeunload" dialog' in observed, 'actual browser leave prompt was not observed'
    call(['dialog-accept'])
    result=call(['run-code','async page=>{await page.waitForFunction(()=>window.__ownerBrowserLeave!==undefined);return await page.evaluate(()=>window.__ownerBrowserLeave);}'],True)
    result['beforeunload']=1
    assert result['retained'], 'browser leave erased the unconfirmed local draft'
    return result


try:
    for name, package in [('haeryong', './cmd/haeryong'), ('f12', './tests/fixtures/f12')]:
        subprocess.run(['go','build','-tags','haeryong_test_state','-o',str(work/name),package], cwd=REPO, env=env, capture_output=True, check=True, timeout=180)
    assert support.fingerprints() == before
    product = h.Product(work/'haeryong', work); product.initialize(); product.start(); owner = product.owner('Task6 browser setup')
    peer=product.owner('Task6 supporting browser concurrent peer')
    fixture = support.prepare(product, owner); proof['fixture'] = fixture
    call(['open', product.endpoint+'/health/live', '--browser', 'chrome', '--profile', str(work/'browser-profile')]); opened = True
    call(['run-code', 'async page=>{await page.evaluate(token=>localStorage.setItem("haeryong.owner-session.v1",token),'+json.dumps(owner['token'])+');return {setup:true};}'], True)
    if args.focus_purge_sources:
        check('managed-source-setup', lambda: support.prepare_managed_purge_sources(product, owner, fixture))
        check('purge-render-cancel', lambda: phase('managed-purge-sources', ['ui.managed-purge.keyboard', 'ui.managed-purge.touch']))
        check('retained-after-cancel', lambda: support.verify_managed_purge_cancel(product, owner, fixture))
        proof['focused_complete'] = len(proof['cases']) == 4 and all(row.get('status','pass') == 'pass' and row.get('complete',True) for row in proof['cases'])
        raise FocusedContextFinished
    check('create', lambda: phase('create', ['ui.save-review.'+n for n in ['mulgae','gaori','a.html','b.html','no-region.html']]+['ui.large-body-context','ui.explicit-reattach']))
    if not args.focus_context:
        check('consent',lambda:phase('consent',[name.replace('ui.notes.','ui.notes.'+size+'.') for size in ('1440x900','820x1180','844x390','390x844') for name in support.CONSENT_CASES]+['ui.notes.touch-confirmation']))
    check('declared-records',lambda:support.validate_declared_records(product,owner,fixture,'browser'))
    check('context-case-inputs',lambda:support.context_cases(product,owner,fixture))
    check('context-classes',lambda:phase('context-classes',['ui.context.'+row['name'] for row in fixture.get('context_cases',[])]+['ui.read-lifetime.'+name for name in ['acquisition-Close','body-Close','superseded-success','superseded-error','protected-scope','typing-resize']]))
    if args.focus_context:
        proof['focused_complete'] = bool(fixture.get('context_cases')) and all(row.get('status','pass') == 'pass' and row.get('complete',True) for row in proof['cases'])
        raise FocusedContextFinished
    check('successors', lambda: (support.require_core(fixture),support.successors(product, owner, fixture))[1])
    check('ambiguity',lambda:phase('ambiguity',['ui.ambiguous-successor-explicit-page']))
    check('loss', lambda: phase('loss', ['ui.loss.before','ui.loss.after']))
    check('stale', lambda: phase('stale', ['ui.stale-deliberate-merge']))
    check('server-reload',lambda:phase('reload',['ui.server-reload-explicit-consent']))
    # Independent lifecycle cases continue after ordinary personal-record failures.
    check('validate-written-intents',lambda:support.validate_written_intents(product,owner,fixture,'browser'))
    before_lifecycle=support.records(product,owner,support.recovery_versions(fixture),extra_ids=fixture.get('known_note_ids',()));proof['before_lifecycle']=before_lifecycle
    check('lifecycle', lambda: phase('lifecycle', ['ui.safe-confirmation.'+s for s in ['1440x900','820x1180','844x390','390x844']]+['ui.real-archive-trash-restore','ui.real-referrer-trash-restore','ui.touch-trash-restore-no-default-purge']))
    check('dialog-pending',lambda:phase('dialog-pending',['ui.unconfirmed-dialog-before-restart']))
    expected = support.records(product, owner, support.recovery_versions(fixture),known=before_lifecycle);support.assert_after_reader_actions(before_lifecycle,expected);proof['expected_records_before_recovery'] = expected
    # Supporting browser leave: accept only Chrome's actual beforeunload, not
    # the Notes HTML confirmation. Unload the renderer before CLI shutdown.
    proof['draft_browser_leave']=leave_with_draft()
    call(['close']); opened = False
    product.stop(); product.start()
    call(['open', product.endpoint+'/health/live', '--browser', 'chrome', '--profile', str(work/'browser-profile')]); opened = True
    check('normal-record-comparison', lambda: {'equal': support.records(product, owner, support.recovery_versions(fixture),known=expected) == expected} if support.records(product, owner, support.recovery_versions(fixture),known=expected) == expected else (_ for _ in ()).throw(AssertionError('records changed on restart')))
    check('dialog-reopen',lambda:phase('dialog-reopen',['ui.unconfirmed-dialog-restart-retains-draft']))
    check('normal-reopen', lambda: phase('reopen', ['ui.reopen.'+n for n in ['mulgae','gaori','boundary']]))
    after=support.records(product,owner,support.recovery_versions(fixture),known=expected);support.assert_after_reader_actions(expected,after);expected=after
    call(['close']); opened = False
    path, interruption = support.interrupted_backup(product, owner, work/'f12'); proof['interruption'] = interruption
    owner, authority = support.recover(product, owner, path); proof['recovery_authority'] = authority
    assert support.records(product, owner, support.recovery_versions(fixture),known=expected) == expected
    proof['old_new_recovery_payloads']=support.assert_recovery_payloads(product,owner,fixture)
    # Fresh bootstrap/credential installation is setup. Subsequent host reads are real.
    call(['open', product.endpoint+'/health/live', '--browser', 'chrome', '--profile', str(work/'browser-profile')]); opened = True
    call(['run-code', 'async page=>{await page.goto('+json.dumps(product.endpoint+'/connect')+');await page.waitForFunction(()=>!localStorage.getItem("haeryong.owner-session.v1"));await page.evaluate(token=>localStorage.setItem("haeryong.owner-session.v1",token),'+json.dumps(owner['token'])+');return {freshSetup:true,oldAuthorityInvalidationSettled:true};}'], True)
    check('recovered-reopen', lambda: phase('reopen', ['ui.reopen.'+n for n in ['mulgae','gaori','boundary']]))
    stored=call(['run-code','async page=>({stored:await page.evaluate(token=>localStorage.getItem("haeryong.owner-session.v1")===token,'+json.dumps(owner['token'])+')})'],True)
    assert stored['stored'],'fresh remembered credential absent before normal close'
    call(['close']); opened = False
    product.stop(); product.start()
    assert product.data('GET','/api/v1/capabilities',client=owner)['runtime_generation'] == authority['after']['runtime_generation']
    call(['open', product.endpoint+'/health/live', '--browser', 'chrome', '--profile', str(work/'browser-profile')]); opened = True
    check('fresh-authority-remembered-reopen', lambda: phase('reopen', ['ui.reopen.'+n for n in ['mulgae','gaori','boundary']]))
    check('export-reimport', lambda: support.export_roundtrip(product, owner, fixture))
    check('immutable-originals', lambda: support.assert_originals(product, owner, fixture))
    assert support.fingerprints() == before, 'consumed input changed during campaign'
    proof['inputs'] = before; proof['input_sha256'] = hashlib.sha256(json.dumps(before,sort_keys=True).encode()).hexdigest()
    proof['binary_sha256'] = hashlib.sha256((work/'haeryong').read_bytes()).hexdigest()
    proof['complete'] = all(row.get('status','pass') == 'pass' and row.get('complete',True) for row in proof['cases'])
except FocusedContextFinished:
    pass
except Exception as error:
    proof['fatal_or_dependency_failure'] = {'type': type(error).__name__, 'message': str(error)}
finally:
    if opened:
        try: call(['close'])
        except Exception as error: proof['cleanup_errors'].append(str(error))
    if product:
        try: product.close()
        except Exception as error: proof['cleanup_errors'].append(str(error))
    proof['daemon_closed'] = product is None or product.process is None
    proof['inputs'] = before
    proof['inputs_unchanged'] = support.fingerprints() == before
    proof['input_sha256'] = hashlib.sha256(json.dumps(before,sort_keys=True).encode()).hexdigest()
    proof['uncaptured_inputs'] = ['prepared Go module/build caches and native SDK','prepared frontend/node_modules','installed Go/Python/Node/Chrome/browser CLI tools']
    if (work/'haeryong').exists():proof['binary_sha256'] = hashlib.sha256((work/'haeryong').read_bytes()).hexdigest()
    proof['complete'] = proof['complete'] and proof['daemon_closed'] and proof['inputs_unchanged'] and not proof['cleanup_errors']
    if args.focus_context or args.focus_purge_sources:
        proof['focused_complete'] = proof.get('focused_complete',False) and proof['daemon_closed'] and proof['inputs_unchanged'] and not proof['cleanup_errors']
    receipt.write_text(json.dumps(proof,ensure_ascii=True,indent=2)+'\n'); receipt.chmod(0o600)
print(json.dumps({'complete':proof['complete'],'cases':len(proof['cases']),'receipt':str(receipt),'cleanup_errors':proof['cleanup_errors']}))
assert proof.get('focused_complete',False) if (args.focus_context or args.focus_purge_sources) else proof['complete'], 'Task6 selected browser checks incomplete; inspect original receipt'
