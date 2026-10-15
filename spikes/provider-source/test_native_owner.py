"""Injected bootstrap/byte/process boundaries only. No native provider is launched."""
import contextlib
import copy
import io
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import native_owner as owner
import stdio_connector as stdio
from hardened_harness import Blocked, Capture, ScopePolicy, UnknownOutcome, sha
from test_stdio import Clock, FakeProcess, timeout

ARGV = ['fake-codex', 'app-server', '--listen', 'stdio://', '-c', 'model_provider="openai"']
PRIVATE_MARKER = 'SYNTHETIC-PRIVATE-DO-NOT-RETAIN'


class ProtocolIO:
    """Fake server responds to actual encoded writes, then fragments reply bytes."""
    def __init__(self, cwd, mutate=None, drop=None):
        self.cwd, self.mutate, self.drop = cwd, mutate, drop
        self.pending = bytearray()
        self.replies = bytearray()
        self.messages = []
        self.closed_in = self.closed_out = False
        self.config = {'model': 'inherited-model', 'model_provider': 'openai',
            'model_reasoning_effort': 'high', 'forced_login_method': None}
    def wait(self, direction, timeout): return True
    def write(self, data):
        # Force partial writes through the real transport for every request.
        part = data[:11]
        self.pending.extend(part)
        while b'\n' in self.pending:
            wire, _, rest = self.pending.partition(b'\n')
            self.pending = bytearray(rest)
            request = json.loads(wire)
            self.messages.append(request)
            if 'id' not in request: continue
            method, ident = request.get('method'), request['id']
            if method == self.drop: continue
            if method == 'initialize':
                result = {'userAgent': 'haeryong_preparatory_source/0.162.1 (test)',
                    'platformFamily': 'unix', 'platformOs': 'macos', 'codexHome': PRIVATE_MARKER}
            elif method == 'account/read':
                result = {'account': {'type': 'chatgpt', 'planType': 'pro',
                    'email': PRIVATE_MARKER, 'token': PRIVATE_MARKER}, 'requiresOpenaiAuth': True}
            elif method == 'config/read':
                result = {'config': {**self.config, 'private_plugin_config': PRIVATE_MARKER},
                    'origins': {'private': PRIVATE_MARKER}, 'layers': [PRIVATE_MARKER]}
            elif method == 'thread/start':
                result = {'thread': {'id': 'thread-A', 'cliVersion': '0.162.1', 'ephemeral': True,
                    'cwd': self.cwd, 'modelProvider': 'openai', 'preview': PRIVATE_MARKER},
                    'cwd': self.cwd, 'model': self.config['model'], 'modelProvider': 'openai',
                    'reasoningEffort': self.config['model_reasoning_effort'], 'approvalPolicy': 'never',
                    'sandbox': {'type': 'readOnly', 'networkAccess': False},
                    'instructionSources': [PRIVATE_MARKER], 'disabledPluginIds': [PRIVATE_MARKER]}
            elif method == 'turn/start':
                result = {'turn': {'id': 'turn-A'}}
            else:
                raise AssertionError('unexpected injected request method')
            if self.mutate: result = self.mutate(method, result)
            self.replies.extend(stdio.encode_frame({'id': ident, 'result': result}))
        return len(part)
    def read(self):
        if not self.replies: return b''
        result = bytes(self.replies[:7])
        del self.replies[:7]
        return result
    def close_input(self): self.closed_in = True
    def close_output(self): self.closed_out = True


class NativeOwnerBehavior(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(dir=Path(__file__).resolve().parent)
        self.root, self.clock = Path(self.temp.name), Clock()
        self.launches = []
    def tearDown(self): self.temp.cleanup()

    @contextlib.contextmanager
    def fake_native(self, mutate=None, drop=None, waits=None):
        server = ProtocolIO(str(self.root), mutate, drop)
        process = FakeProcess([0] if waits is None else waits)
        signals = []
        def factory(argv, **kwargs):
            self.launches.append((argv, kwargs))
            return process
        def launch(argv, cwd, parent_env, deadline, **kwargs):
            return stdio.launch_owned(argv, cwd, parent_env, deadline,
                process_factory=factory, io_factory=lambda _: server, clock=self.clock,
                signal_group=lambda pid, sig: signals.append((pid, sig)), **kwargs)
        # All native entrypoints are replaced; process factory never invokes Popen.
        with patch.object(owner, 'launch_owned', launch), patch.object(owner.time, 'monotonic', self.clock):
            yield server, process, signals

    def open(self, **kwargs):
        return owner.open_owner_session(ARGV, str(self.root), 20, 30, execute=True, **kwargs)
    def scopes(self):
        return {('project', 'p', '/approved', 'private'), ('revision', 'p', 'fixed'),
                ('provider', 'openai', 'chatgpt', 'local-owner-only')}
    def captures(self):
        return (Capture(('p', 'fixed', 'file.go', 1, 1), b'source bytes', sha(b'source bytes')),)
    def handoff(self, session, scopes=None, captures=None, digest=None, confirm=None):
        requested = self.scopes() if scopes is None else scopes
        return session.prepare_cited_client(ScopePolicy(self.scopes()), requested,
            self.captures() if captures is None else captures, 'question',
            sha(b'question\nsource bytes') if digest is None else digest,
            self.root/'intent.json', confirm)

    def test_default_cli_and_library_optout_never_launch(self):
        with patch.object(owner, 'launch_owned', side_effect=AssertionError('native launch forbidden')):
            with patch('sys.argv', ['native_owner.py']), contextlib.redirect_stdout(io.StringIO()) as out:
                self.assertEqual(owner.main(), 0)
            self.assertEqual(json.loads(out.getvalue())['execution'], 'not_run')
            with self.assertRaises(Blocked): owner.open_owner_session(ARGV, str(self.root), 20, 30)

    def test_injected_smoke_allowlist_has_no_turn_and_safe_output(self):
        with self.fake_native() as (server, process, signals):
            record = owner.smoke(ARGV, str(self.root), 20, 30, execute=True)
        self.assertEqual([m['method'] for m in server.messages],
            ['initialize', 'initialized', 'account/read', 'config/read', 'thread/start'])
        self.assertEqual(record['provider_outcome'], 'not_dispatched')
        self.assertEqual(record['turn_start_count'], 0)
        self.assertEqual(record['cleanup']['exit_code'], 0)
        self.assertEqual(process.wait_calls, [0.25])
        self.assertNotIn(PRIVATE_MARKER, json.dumps(record))
        self.assertTrue(server.closed_in and server.closed_out)
        self.assertEqual(signals, [])

    def test_bootstrap_rejects_model_login_write_and_refresh_methods_before_io(self):
        with self.fake_native() as (server, _, _):
            session = self.open()
            before = len(server.messages)
            for method in ('turn/start', 'turn/interrupt', 'account/login/start', 'account/logout', 'config/value/write'):
                with self.subTest(method=method), self.assertRaises(Blocked):
                    session.bootstrap.request(method, {})
            self.assertEqual(len(server.messages), before)
            session.close()

    def test_initialization_version_platform_failure_stops_before_account(self):
        for key, value in [('userAgent', 'wrong/0.162.0 (test)'), ('platformOs', 'linux'), ('platformFamily', 'windows')]:
            def mutate(method, result):
                if method == 'initialize': result[key] = value
                return result
            with self.subTest(field=key), self.fake_native(mutate) as (server, process, _):
                with self.assertRaises(owner.OwnerPreparationFault) as failure: self.open()
                self.assertEqual([m['method'] for m in server.messages], ['initialize'])
                self.assertEqual(failure.exception.record['cleanup']['status'], 'reaped')
                self.assertEqual(process.exit_code, 0)

    def test_account_type_plan_and_typed_auth_failure_stop_before_config(self):
        cases = [('type', 'apiKey'), ('planType', 'plus'), ('requiresOpenaiAuth', False), ('requiresOpenaiAuth', 1)]
        for key, value in cases:
            def mutate(method, result):
                if method == 'account/read':
                    (result if key == 'requiresOpenaiAuth' else result['account'])[key] = value
                return result
            with self.subTest(field=key, variant=type(value).__name__), self.fake_native(mutate) as (server, _, _):
                with self.assertRaises(owner.OwnerPreparationFault) as failure: self.open()
                self.assertEqual(server.messages[-1]['method'], 'account/read')
                self.assertEqual(failure.exception.record['provider_outcome'], 'not_dispatched')

    def test_effective_provider_missing_model_effort_or_forced_login_blocks_thread(self):
        cases = [('model_provider', 'other'), ('model_provider', None), ('model', None),
                 ('model_reasoning_effort', {}), ('forced_login_method', 'api')]
        for key, value in cases:
            def mutate(method, result):
                if method == 'config/read': result['config'][key] = value
                return result
            with self.subTest(field=key), self.fake_native(mutate) as (server, _, _):
                with self.assertRaises(owner.OwnerPreparationFault): self.open()
                self.assertEqual(server.messages[-1]['method'], 'config/read')
                self.assertNotIn('thread/start', [m['method'] for m in server.messages])

    def test_thread_identity_permission_and_default_mismatches_block_handoff(self):
        cases = [('thread.id', ''), ('thread.cliVersion', '0.162.0'), ('thread.ephemeral', False),
                 ('thread.cwd', '/other'), ('cwd', '/other'), ('thread.modelProvider', 'other'),
                 ('modelProvider', 'other'), ('approvalPolicy', 'on-request'),
                 ('sandbox.type', 'workspaceWrite'), ('sandbox.networkAccess', True),
                 ('sandbox.networkAccess', 0), ('model', 'different'), ('reasoningEffort', 'low')]
        for field, value in cases:
            def mutate(method, result):
                if method == 'thread/start':
                    keys = field.split('.')
                    target = result[keys[0]] if len(keys) == 2 else result
                    target[keys[-1]] = value
                return result
            with self.subTest(field=field), self.fake_native(mutate) as (server, _, _):
                with self.assertRaises(owner.OwnerPreparationFault) as failure: self.open()
                self.assertEqual(failure.exception.record['phase'], 'thread/start')
                self.assertEqual(failure.exception.record['provider_outcome'], 'not_dispatched')
                self.assertNotIn('turn/start', [m['method'] for m in server.messages])

    def test_child_environment_selection_preserves_parent_and_normal_features(self):
        parent = {'OPENAI_API_KEY':'synthetic', 'CODEX_API_KEY':'synthetic', 'PATH':'kept',
                  'HOOK_CONTEXT':'kept', 'PLUGIN_CONTEXT':'kept', 'CODEX_HOME':'kept'}
        original = dict(parent)
        with self.fake_native() as (server, _, _):
            session = self.open(parent_env=parent)
            session.close()
        self.assertEqual(parent, original)
        self.assertEqual(self.launches[0][1]['env'], {k:v for k,v in original.items() if k not in ('OPENAI_API_KEY','CODEX_API_KEY')})
        self.assertEqual(self.launches[0][0], ARGV)
        thread_request = next(m for m in server.messages if m.get('method') == 'thread/start')
        self.assertNotIn('model', thread_request['params'])
        self.assertNotIn('reasoningEffort', thread_request['params'])
        self.assertFalse(any('disable' in value or 'forced_login' in value for value in ARGV))

    def test_stale_account_or_defaults_recheck_blocks_client_and_intent(self):
        for failure_kind in ('account', 'model', 'effort'):
            with self.subTest(kind=failure_kind), self.fake_native() as (server, _, _):
                session = self.open()
                if failure_kind == 'account':
                    server.mutate = lambda method,result: ({'account':{'type':'apiKey'},'requiresOpenaiAuth':True}
                        if method=='account/read' else result)
                elif failure_kind == 'model': server.config['model'] = 'changed-model'
                else: server.config['model_reasoning_effort'] = 'low'
                with self.assertRaises(Blocked): self.handoff(session)
                self.assertFalse((self.root/'intent.json').exists())
                self.assertNotIn('turn/start', [m['method'] for m in server.messages])
                session.close()

    def test_source_policy_denial_or_payload_change_never_creates_intent(self):
        with self.fake_native() as (server, _, _):
            session = self.open()
            new_scope = self.scopes() | {('revision','p','unapproved')}
            with self.assertRaises(Blocked): self.handoff(session, scopes=new_scope, confirm=lambda missing:False)
            with self.assertRaises(Blocked): self.handoff(session, digest=sha(b'changed'))
            self.assertFalse((self.root/'intent.json').exists())
            self.assertNotIn('turn/start', [m['method'] for m in server.messages])
            session.close()

    def test_single_client_handoff_keeps_request_ids_unique_and_bootstrap_closed(self):
        with self.fake_native() as (server, _, _):
            session = self.open()
            client, payload = self.handoff(session)
            self.assertEqual(payload, 'question\nsource bytes')
            self.assertEqual(client.next_id, 7)
            self.assertFalse((self.root/'intent.json').exists())
            before = len(server.messages)
            with self.assertRaises(Blocked): self.handoff(session)
            with self.assertRaises(Blocked): session.bootstrap.request('account/read', {'refreshToken':False})
            self.assertEqual(len(server.messages), before)
            ids = [m['id'] for m in server.messages if 'id' in m]
            self.assertEqual(ids, [1,2,3,4,5,6])
            session.close()

    def test_lost_injected_future_ack_retains_intent_and_forbids_redispatch(self):
        with self.fake_native(drop='turn/start') as (server, _, _):
            session = self.open()
            client, payload = self.handoff(session)
            client.state.origin = 'injected' # Honest provenance for this fake server.
            with self.assertRaises(UnknownOutcome):
                client.request('turn/start', {'threadId':'thread-A','input':[{'type':'text','text':payload}]})
            with self.assertRaises(Blocked):
                client.request('turn/start', {'threadId':'thread-A','input':[{'type':'text','text':payload}]})
            self.assertEqual(sum(m.get('method')=='turn/start' for m in server.messages),1)
            self.assertEqual(client.state.outcome(),'unknown_outcome')
            intent=json.loads((self.root/'intent.json').read_text())
            self.assertEqual(intent['request_id'],7)
            self.assertEqual(intent['input_sha256'],sha(payload.encode()))
            session.close()

    def test_guard_failure_and_transport_eof_have_separate_cleanup_receipts(self):
        with self.fake_native(drop='account/read', waits=[timeout(),timeout(),timeout()]) as (server, process, signals):
            with self.assertRaises(owner.OwnerPreparationFault) as failure: self.open()
            record=failure.exception.record
            self.assertEqual(record['phase'],'account/read')
            self.assertEqual(record['provider_outcome'],'not_dispatched')
            self.assertEqual(record['cleanup']['status'],'cleanup_unresolved')
            self.assertIs(failure.exception.owned_connector.process,process)
            self.assertEqual(len(signals),2)
            self.assertEqual(len(process.wait_calls),3)
            self.assertNotIn(PRIVATE_MARKER,json.dumps(record))

    def test_smoke_reaped_exit_is_not_model_completion_and_unknown_cleanup_not_pass(self):
        with self.fake_native(waits=[timeout(),timeout(),timeout()]):
            record=owner.smoke(ARGV,str(self.root),20,30,execute=True)
        self.assertEqual(record['phase'],'startup_guards_passed_no_turn')
        self.assertEqual(record['provider_outcome'],'not_dispatched')
        self.assertEqual(record['cleanup']['status'],'cleanup_unresolved')

    def cli_args(self, receipt):
        return ['native_owner.py','--execute-smoke','--argv-json',json.dumps(ARGV),'--cwd',str(self.root),
            '--deadline-monotonic','20','--cleanup-deadline-monotonic','30','--receipt',str(receipt)]

    def test_exclusive_receipt_blocks_before_launch_and_preserves_existing_bytes(self):
        receipt=self.root/'receipt.json';receipt.write_bytes(b'original immutable receipt')
        with self.fake_native(), patch('sys.argv',self.cli_args(receipt)):
            with self.assertRaises(FileExistsError):owner.main()
        self.assertEqual(receipt.read_bytes(),b'original immutable receipt')
        self.assertEqual(self.launches,[])

    def test_cli_writes_only_safe_new_receipt_and_cleanup_controls_exit(self):
        for waits, expected in [([0],0),([timeout(),timeout(),timeout()],1)]:
            receipt=self.root/f'receipt-{expected}.json'
            with self.subTest(exit=expected), self.fake_native(waits=waits), patch('sys.argv',self.cli_args(receipt)), contextlib.redirect_stdout(io.StringIO()) as out:
                self.assertEqual(owner.main(),expected)
            self.assertNotIn(PRIVATE_MARKER,receipt.read_text())
            self.assertEqual(receipt.stat().st_mode & 0o777,0o600)
            self.assertEqual(json.loads(out.getvalue())['provider_outcome'],'not_dispatched')

    def test_nonbaseline_argv_or_expired_deadline_prevents_launch(self):
        with self.fake_native():
            for argv in (ARGV+['--disable','hooks'],ARGV[:-1]+['forced_login_method="chatgpt"'],ARGV[:-1]+['model="other"']):
                with self.assertRaises(Blocked):owner.open_owner_session(argv,str(self.root),20,30,execute=True)
            with self.assertRaises(Blocked):owner.open_owner_session(ARGV,str(self.root),0,30,execute=True)
            with self.assertRaises(Blocked):owner.open_owner_session(ARGV,str(self.root),20,10,execute=True)
        self.assertEqual(self.launches,[])

    def test_bootstrap_account_refresh_or_ambiguous_params_block_before_write(self):
        with self.fake_native() as (server, _, _):
            session=self.open()
            before=len(server.messages)
            for params in ({'refreshToken':True},{},{'refreshToken':0},{'refreshToken':False,'extra':True}):
                with self.subTest(variant=list(params)), self.assertRaises(Blocked):
                    session.bootstrap.request('account/read',params)
            self.assertEqual(len(server.messages),before)
            session.close()
