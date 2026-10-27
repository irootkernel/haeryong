"""Task6 actual Wails owner-record campaign, with supporting prepaired setup."""
import ctypes
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import time


def run(repo, work, state, product, owner, launch, proof):
    def load(name, path):
        spec = importlib.util.spec_from_file_location(name, path)
        module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
        return module
    support = load('native_owner_support', repo/'tests/manual/owner_records_support.py')
    h = load('native_owner_harness', repo/'tests/e2e/harness.py')
    fixture = support.prepare(product, owner)
    peer=product.owner('Task6 supporting native concurrent peer')
    proof['owner_fixture'] = fixture
    proof['owner_setup_limit'] = 'Public bootstrap and prepaired native-session file are supporting setup; no production in-app pairing/store qualification'
    proof['owner_runs'] = []
    proof['owner_http'] = []
    proof['owner_lifecycle_observations'] = []
    control = {'drop': False, 'dropped': False, 'peer': None}
    quartz = ctypes.CDLL('/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices')
    quartz.CGEventCreateKeyboardEvent.argtypes = [ctypes.c_void_p, ctypes.c_ushort, ctypes.c_bool]
    quartz.CGEventCreateKeyboardEvent.restype = ctypes.c_void_p
    quartz.CGEventPostToPid.argtypes = [ctypes.c_int, ctypes.c_void_p]
    quartz.CGEventSetFlags.argtypes = [ctypes.c_void_p, ctypes.c_uint64]
    quartz.CFRelease.argtypes = [ctypes.c_void_p]
    def key(pid, name):
        parts = name.split('+'); base = parts.pop()
        codes = {'Enter':36,'Escape':53,'Tab':48,'ArrowDown':125,'ArrowUp':126,'ArrowLeft':123,'ArrowRight':124,'Home':115,'End':119,'PageUp':116,'PageDown':121}
        modifiers = {'Shift':1 << 17,'Control':1 << 18,'Alt':1 << 19,'Meta':1 << 20}
        if base not in codes or any(part not in modifiers for part in parts) or len(parts) != len(set(parts)):
            raise AssertionError('unsupported native key: '+name)
        flags = sum(modifiers[part] for part in parts)
        for down in (True,False):
            event = quartz.CGEventCreateKeyboardEvent(None, codes[base], down)
            assert event, 'native input unavailable'
            quartz.CGEventSetFlags(event,flags)
            quartz.CGEventPostToPid(pid,event); quartz.CFRelease(event)
    def after(method, path, headers, body, status, payload, forwarded=True):
        if method in ('POST','PATCH','DELETE') and 'annotations' in path and status in (200,201):
            value=json.loads(payload).get('data',{})
            if value.get('annotation_id') and value['annotation_id'] not in fixture.setdefault('known_note_ids',[]):
                fixture['known_note_ids'].append(value['annotation_id'])
        if method=='POST' and path.endswith('/commit') and '/lifecycle-plans/' in path and status==200:
            value=json.loads(payload)['data']
            if value.get('document_id') in [item['document_id'] for item in fixture['originals'].values()]:
                try:proof['owner_lifecycle_observations'].append({'ok':True,'value':support.lifecycle_observation(product,owner,fixture,value['document_id'],value['lifecycle'])})
                except Exception as error:proof['owner_lifecycle_observations'].append({'ok':False,'error':str(error),'value':value})
        if method in ('POST','PATCH','PUT','DELETE') and any(kind in path for kind in ('annotations','review','lifecycle','revisit')):
            proof['owner_http'].append({'method':method,'path':path,'key':headers.get('Idempotency-Key'),
                                       'if_match':headers.get('If-Match'),'if_none_match':headers.get('If-None-Match'),
                                       'body_sha256':hashlib.sha256(body).hexdigest(),'status':status,'reply_sha256':hashlib.sha256(payload).hexdigest(),'upstream_forwarded':forwarded})
    def before(method, path, headers, body):
        if control['peer'] and method == 'PATCH' and path.endswith('/annotations/'+control['peer']['annotation_id']):
            note = control['peer']; control['peer'] = None
            result = product.data('PATCH', path, client=peer, headers={'If-Match':note['etag'],'Idempotency-Key':support.key()}, body={
                'action':'edit','body':control.get('peer_body','Task6 native remote edit'),'body_ref':None,'selected_text':None,'context':None,'context_ref':note['context_ref']})
            proof.setdefault('supporting_concurrent_native_peers',[]).append({'annotation_id':result['annotation_id'],'revision':result['revision'],'body':result['body'],'actor_session':peer['session']['session_id']})
    def drop(method, path, status):
        if control['drop'] and not control['dropped'] and method == 'POST' and path.endswith('/annotations') and status == 201:
            control['dropped'] = True
            return True
        return False
    def phase(name, selection, expected, *, boundary=None, narrow=False, width=None):
        if selection in ('ambiguity','reopen','context-classes') and not fixture.get('declared_records_validated'):
            proof.setdefault('blocked_ui_rows',[]).extend({'phase':name,'name':case,'status':'blocked','reason':'own Save/Review prerequisite failed'} for case in expected)
            raise AssertionError('BLOCKED: own Save/Review prerequisite failed')
        constants = {'fixture':fixture,'phase':selection,'expected':expected,'boundary':boundary}
        probe = work/('owner-probe-'+name+'.js')
        probe.write_text((repo/'tests/manual/notes_context_cases.js').read_text()+'\n'+(repo/'tests/manual/notes_confirmation_cases.js').read_text()+'\n'+(repo/'spikes/viewer/owner_records_probe.js').read_text().replace('OWNER_CONSTANTS',json.dumps(constants,ensure_ascii=False)))
        sent, rows = set(), None
        native_inputs={'session_sha256':hashlib.sha256((state/'native-session').read_bytes()).hexdigest(),
                       'binary_sha256':hashlib.sha256((work/'native').read_bytes()).hexdigest(),
                       'assets':{str(path.relative_to(work/'dist-native')):hashlib.sha256(path.read_bytes()).hexdigest() for path in (work/'dist-native').rglob('*') if path.is_file()}}
        with h.Proxy(product, before=before, after=after, drop=drop) as proxy:
            if boundary == 'before':
                proxy.blocked_path = ('POST','/api/v1/versions/'+fixture['boundary']['version_id']+'/annotations')
            process = launch(['--reader','--reader-port','9271','--reader-path',fixture['routes']['mulgae'],
                              '--reader-probe',str(probe),'--reader-width',str(width) if width else '390' if narrow else '1180',
                              '--reader-height','844' if narrow else '880','--reader-close-on-done'],'300s',base=proxy.endpoint)
            try:
                deadline = time.monotonic()+320
                while time.monotonic()<deadline:
                    path = state/f'native-reader-selfcheck-{process.pid}.json'
                    if path.exists():
                        try: current = json.loads(path.read_text())['rows']
                        except (ValueError,KeyError): current = []
                        hidden = next((row for row in current if row.get('status') == 'fail' and all(value in row.get('detail', {}).get('error', '') for value in ('"documentVisible":false', '"connected":true', '"visible":true'))), None)
                        if hidden:
                            proof['native_prerequisite_block'] = {'phase':name,'pid':process.pid,'reason':'hidden document admission','observation':hidden}
                            raise SystemExit('STOP: hidden native document admission; original partial and guard retained')
                        for row in current:
                            if row['name'].startswith('input.request.') and row['name'] not in sent:
                                if 'reconcile' in row['detail']['label']: proxy.release()
                                key(process.pid,row['detail']['key']); sent.add(row['name'])
                        if any(row['name']=='page.done' and row['status']=='pass' for row in current):
                            rows = current; break
                    if process.poll() is not None: break
                    time.sleep(.2)
                assert rows is not None, 'no final page.done; partial native report cannot qualify'
                exit_code = process.wait(timeout=330)
                # Snapshot shared and per-PID originals BEFORE any further launch.
                guard = json.loads((state/'native-guard.json').read_text())
                facade = json.loads((state/f'native-reader-receipts-{process.pid}.json').read_text())
                calls_path = state/f'native-call-observations-{process.pid}.json'
                calls = json.loads(calls_path.read_text()) if calls_path.exists() else []
                network=json.loads((state/f'native-network-observations-{process.pid}.json').read_text())
                case_rows = [row for row in rows if row['name'].startswith('ui.')]
                observed = {row['name'].removeprefix('input.observed.') for row in rows if row['name'].startswith('input.observed.')}
                requested = {name.removeprefix('input.request.') for name in sent}
                case = {'name':name,'pid':process.pid,'rows':rows,'expected':expected,'exit_code':exit_code,'guard':guard,'facade':facade,'calls':calls,'sent':sorted(sent),
                        'probe_sha256':hashlib.sha256(probe.read_bytes()).hexdigest(),'native_inputs':native_inputs}
                proof['owner_runs'].append(case)
                case['ok'] = exit_code == 0 and all(row['status']=='pass' for row in rows) and [row['name'] for row in case_rows] == expected
                case['ok'] = case['ok'] and requested == observed and guard.get('pid') == process.pid and guard.get('installed') in (True,1)
                attached=any(row['event']=='attachSession' for row in facade)
                case['ok'] = case['ok'] and (not attached and any(row.get('status')==401 for row in network) if selection=='old-authority' else attached) and any(row['event']=='windowClose' for row in facade)
                assert case['ok'], 'actual native rows/process/input/guard/facade denominator failed'
                if selection=='dialog-pending': fixture['restart_draft']=case_rows[0]['detail']['draft']
                assert len({row['call_id'] for row in calls}) == len(calls) and all(row['forwarded']==1 and row['reply_marshaled'] for row in calls)
                if selection == 'create':
                    assert sum(row['operation']=='annotationCreate' for row in calls) == 6
                    assert sum(row['operation']=='reviewPut' for row in calls) == 6
                for call in calls:
                    if call['operation'] not in ('annotationCreate','annotationPatch','annotationDelete','reviewPut','revisitPut','lifecyclePlan','lifecyclePlanCommit'): continue
                    if call.get('operation_id'): matching = [row for row in proof['owner_http'] if row['key']==call['operation_id']]
                    elif call['operation']=='lifecyclePlanCommit': matching=[row for row in proof['owner_http'] if row['path']=='/api/v1/lifecycle-plans/'+call['plan_id']+'/commit']
                    else: matching=[row for row in proof['owner_http'] if row['path']=='/api/v1/documents/'+call['document_id']+'/lifecycle-plans' and row['body_sha256']==call['body_sha256']]
                    assert matching, 'native mutation lacks real upstream operation observation'
                    if 'body_sha256' in call: assert all(row['body_sha256']==call['body_sha256'] for row in matching), 'native body changed before real upstream'
                    if 'etag' in call: assert all(row['if_match']==call['etag'] for row in matching)
                    if 'version_id' in call: assert all('/versions/'+call['version_id']+'/' in row['path'] for row in matching)
                    if 'annotation_id' in call: assert all(row['path'].endswith('/annotations/'+call['annotation_id']) for row in matching)
                    condition=call.get('precondition',{})
                    if 'if_match' in condition: assert all(row['if_match']==condition['if_match'] for row in matching)
                    if condition.get('create'): assert all(row['if_none_match']=='*' for row in matching)
            finally:
                proxy.release()
                if process.poll() is None: process.kill(); process.wait(timeout=10)
                # Failure originals are retained too, before another PID can
                # overwrite the shared guard. No progress file qualifies.
                case = next((item for item in proof['owner_runs'] if item['pid'] == process.pid), None)
                if case is None:
                    case = {'name':name,'pid':process.pid,'expected':expected,'ok':False,'exit_code':process.returncode,'native_inputs':native_inputs,'probe_sha256':hashlib.sha256(probe.read_bytes()).hexdigest()}
                    proof['owner_runs'].append(case)
                for field, filename in [('guard','native-guard.json'),('facade',f'native-reader-receipts-{process.pid}.json'),
                                        ('calls',f'native-call-observations-{process.pid}.json'),('network',f'native-network-observations-{process.pid}.json'),
                                        ('original_report',f'native-reader-selfcheck-{process.pid}.json')]:
                    path = state/filename
                    if path.exists(): case[field] = json.loads(path.read_text())
                case['proxy_transport_errors'] = list(proxy.transport_errors)
        return {'phase':name,'final':True}
    def check(name, fn):
        try: proof['cases'].append({'kind':'Task6 '+name,'ok':True,'detail':fn()})
        except Exception as error: proof['cases'].append({'kind':'Task6 '+name,'ok':False,'error':str(error)})
    create = ['ui.save-review.'+name for name in ['mulgae','gaori','a.html','b.html','no-region.html']]+['ui.large-body','ui.explicit-reattach']
    reopen = ['ui.reopen.'+name for name in ['mulgae','gaori','boundary']]
    check('create',lambda:phase('create','create',create))
    created = support.all_items(product,owner,'/api/v1/versions/'+fixture['boundary']['version_id']+'/annotations')
    def check_large_context():
        context_note = next((note for note in created if note['body']=='Task6 native original a.html'),None)
        assert context_note and context_note['context_ref'], 'core UI Save did not adopt the original large context'
        status, body, _ = product.request('GET','/api/v1/annotations/'+context_note['annotation_id']+'/context?revision=1',client=owner,raw=True)
        assert status == 200 and body == support.LARGE_CONTEXT_BYTES and len(body) >= 2*1024*1024
        proof['native_large_context'] = {'annotation_id':context_note['annotation_id'],'size':len(body),'sha256':hashlib.sha256(body).hexdigest()}
        return proof['native_large_context']
    check('large-context',check_large_context)
    check('consent',lambda:phase('consent','consent',support.CONSENT_CASES,narrow=True))
    check('consent-wide',lambda:phase('consent-wide','consent',support.CONSENT_CASES,width=1440))
    def declared_records():
        snapshot = support.validate_declared_records(product,owner,fixture,'native')
        whole = [row['current'] for row in snapshot[fixture['boundary']['version_id']]['notes'].values() if row['current']['body_ref'] and row['current']['anchor']['page_path'] is None]
        assert len(whole) == 1
        note = whole[0]
        fixture['large_body_identity'] = {field:note[field] for field in ('annotation_id','revision','etag')}
        return snapshot
    check('declared-records',declared_records)
    check('context-case-inputs',lambda:support.context_cases(product,owner,fixture))
    check('context-classes',lambda:phase('context-classes','context-classes',['ui.context.'+row['name'] for row in fixture.get('context_cases',[])]))
    check('successors',lambda:(support.require_core(fixture),support.successors(product,owner,fixture))[1])
    check('ambiguity',lambda:phase('ambiguity','ambiguity',['ui.ambiguous-successor-explicit-page']))
    for boundary in ('before','after'):
        control.update(drop=boundary=='after',dropped=False)
        check('loss-'+boundary,lambda boundary=boundary:phase('loss-'+boundary,'loss',['ui.loss.'+boundary],boundary=boundary))
    control['drop'] = False
    notes = support.all_items(product,owner,'/api/v1/versions/'+fixture['boundary']['version_id']+'/annotations')
    control['peer'] = next((note for note in notes if note['body']=='Task6 native original no-region.html'),None)
    check('stale',lambda:phase('stale','stale',['ui.stale-deliberate-merge']))
    notes=support.all_items(product,owner,'/api/v1/versions/'+fixture['boundary']['version_id']+'/annotations')
    control['peer']=next((note for note in notes if note['body']=='Task6 native deliberately merged'),None)
    control['peer_body']='Task6 native reload peer revision'
    check('server-reload',lambda:phase('reload','reload',['ui.server-reload-explicit-consent']))
    check('validate-written-intents',lambda:support.validate_written_intents(product,owner,fixture,'native'))
    before_lifecycle=support.records(product,owner,support.recovery_versions(fixture),extra_ids=fixture.get('known_note_ids',()));proof['owner_before_lifecycle']=before_lifecycle
    check('lifecycle',lambda:phase('lifecycle','lifecycle',['ui.native-narrow-safe-confirmation','ui.real-archive-trash-restore','ui.native-no-default-purge'],narrow=True))
    check('lifecycle-exclusions',lambda:all(row['ok'] for row in proof['owner_lifecycle_observations']) and len(proof['owner_lifecycle_observations'])>=5 or (_ for _ in ()).throw(AssertionError('missing/failed target/referrer lifecycle observations')))
    check('dialog-pending',lambda:phase('dialog-pending','dialog-pending',['ui.unconfirmed-dialog-before-restart']))
    expected = support.records(product,owner,support.recovery_versions(fixture),known=before_lifecycle);support.assert_after_reader_actions(before_lifecycle,expected);proof['owner_expected_before_recovery'] = expected
    product.stop(); product.start()
    assert support.records(product,owner,support.recovery_versions(fixture),extra_ids=fixture.get('known_note_ids',())) == expected
    check('dialog-reopen',lambda:phase('dialog-reopen','dialog-reopen',['ui.unconfirmed-dialog-restart-retains-draft']))
    check('reopen',lambda:phase('reopen','reopen',reopen))
    after_reader=support.records(product,owner,support.recovery_versions(fixture),known=expected);support.assert_after_reader_actions(expected,after_reader);expected=after_reader
    generator = work/'owner-f12-generator'
    subprocess.run(['go','build','-o',str(generator),'./tests/fixtures/f12'],cwd=repo,capture_output=True,check=True,timeout=180)
    backup, interruption = support.interrupted_backup(product,owner,generator); proof['owner_interruption'] = interruption
    owner, authority = support.recover(product,owner,backup); proof['owner_recovery_authority'] = authority
    assert support.records(product,owner,support.recovery_versions(fixture),extra_ids=fixture.get('known_note_ids',())) == expected
    proof['old_new_recovery_payloads']=support.assert_recovery_payloads(product,owner,fixture)
    check('old-native-authority-refused',lambda:phase('old-authority','old-authority',['ui.old-authority-refused']))
    (state/'native-session').write_text(owner['token']+'\n'); (state/'native-session').chmod(0o600)
    check('recovered-reopen',lambda:phase('recovered-reopen','reopen',reopen))
    product.stop(); product.start()
    assert product.data('GET','/api/v1/capabilities',client=owner)['runtime_generation'] == authority['after']['runtime_generation']
    check('remembered-reopen',lambda:phase('remembered-reopen','reopen',reopen))
    check('export-reimport',lambda:support.export_roundtrip(product,owner,fixture))
    check('immutable-originals',lambda:support.assert_originals(product,owner,fixture))
    assert all(case.get('ok',True) for case in proof['cases']), 'Task6 native required case failed or incomplete'
