"""Default and sole runnable mode: injected offline tests, never native dispatch."""
import hashlib
import argparse
import json
import os
import sys
import unittest
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--output', type=Path, help='New private result file; default: stdout only')
parser.add_argument('--original-manifest', type=Path,
    help='Optional historical file-hash inventory; not required for standalone tests')
args = parser.parse_args()
root = Path(__file__).resolve().parent
if args.output and args.output.resolve().is_relative_to(root):
    parser.error('--output must be outside the code directory')
# Reserve before testing, so an existing evidence file is never overwritten.
output = (os.fdopen(os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), 'w')
    if args.output else None)
class Result(unittest.TextTestResult):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.passed_ids = []
    def addSuccess(self, test):
        self.passed_ids.append(test.id())
        super().addSuccess(test)
suite = unittest.TestSuite(unittest.defaultTestLoader.discover(str(root), pattern=pattern)
    for pattern in ('test_adoption.py', 'test_stdio.py', 'test_native_owner.py'))
result = unittest.TextTestRunner(verbosity=2, resultclass=Result).run(suite)
original = json.loads(args.original_manifest.read_text()) if args.original_manifest else None
unchanged = (all(hashlib.sha256(Path(row['path']).read_bytes()).hexdigest() == row['sha256'] for row in original)
    if original is not None else None)
evidence = {'kind':'injected offline behavioral tests', 'tests_run':result.testsRun,
    'passed_test_ids':result.passed_ids, 'failure_count':len(result.failures), 'error_count':len(result.errors),
    'success':result.wasSuccessful() and unchanged is not False, 'original_files_unchanged':unchanged,
    'live_provider_calls':0, 'native_app_server_processes':0, 'native_child_processes':0,
    'limitations':['Byte readiness, writes, clock, process factory, signals and process waits are injected.',
        'No native pipe/backpressure/framing or OS process-group behavior was exercised.',
        'Native owner orchestration uses an injected fragmented-byte server; actual account/config/thread observations remain untested in this candidate.']}
if output:
    with output:
        output.write(json.dumps(evidence,indent=2)+'\n')
else:
    print(json.dumps(evidence, indent=2))
print(json.dumps({k:evidence[k] for k in ('kind','tests_run','success','original_files_unchanged','live_provider_calls','native_app_server_processes')}))
raise SystemExit(0 if evidence['success'] else 1)
