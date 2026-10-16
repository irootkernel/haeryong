"""Run the declared contract matrix and retain a private structured result."""
from pathlib import Path
import argparse
import hashlib
import json
import sys
import unittest


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--report', type=Path, required=True)
    args = parser.parse_args()
    if not args.report.is_absolute() or args.report.exists():
        parser.error('report must be a new explicit absolute private file')
    here = Path(__file__).resolve().parent
    matrix = json.loads((here / 'cases.json').read_text())
    suite = unittest.defaultTestLoader.discover(str(here), pattern='test_contracts.py')
    def flatten(node):
        for child in node:
            if isinstance(child, unittest.TestSuite):
                yield from flatten(child)
            else:
                yield child
    test_ids = [test.id() for test in flatten(suite)]
    for row in matrix['cases'] + matrix['supplemental_groups']:
        assert sum(row['test_prefix'] in name for name in test_ids) == 1, row['id']
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    failed = {test.id(): detail for test, detail in result.failures + result.errors}
    report = {
        'kind': 'Executed pure contract fixture/model tests; no production service/durability/provider claim',
        'tests_run': result.testsRun, 'success': result.wasSuccessful(),
        'tests': [{'test': name, 'result': 'fail' if name in failed else 'pass'} for name in test_ids],
        'failures': failed, 'scope': matrix,
        'source_hashes': {path.name: hashlib.sha256(path.read_bytes()).hexdigest()
                          for path in sorted(here.iterdir()) if path.suffix in ('.py', '.json')},
        'skipped': [(test.id(), reason) for test, reason in result.skipped],
    }
    with args.report.open('x') as stream:
        json.dump(report, stream, ensure_ascii=False, indent=2)
        stream.write('\n')
    return 0 if result.wasSuccessful() else 1


if __name__ == '__main__':
    sys.exit(main())
