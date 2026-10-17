"""Exercise documentation-validator admission against disposable full-doc fixtures."""
from pathlib import Path
import json
import itertools
import os
import re
import shutil
import subprocess
import tempfile
import pytest
pytestmark = pytest.mark.integration

class TestValidatorAdmission:

    @pytest.fixture(autouse=True)
    def _documents(self, tmp_path):
        self.root = tmp_path
        source = Path(__file__).resolve().parents[2]
        for name in ('docs', 'spikes', 'tests', 'skills'):
            shutil.copytree(source / name, self.root / name)
        shutil.copyfile(source / 'TESTING.md', self.root / 'TESTING.md')
        subprocess.run(['git', 'init', '--quiet', str(self.root)], check=True)
        self.task = self.root / 'docs/tasks/EPIC-001/TASK-001-01.md'
        self.original = self.task.read_text(encoding='utf-8')

    def run_fixture(self, status, *, missing=False, markers=None, extra_assertion=None):
        text = re.sub('^status:.*$', '' if missing else 'status: ' + json.dumps(status), self.original, count=1, flags=re.M)
        if markers is not None:
            pattern = '^- \\[[^\\]]*\\](?= T-001-01-)'
            count = len(re.findall(pattern, text, flags=re.M))
            assert count > 0, 'Fixture must contain Task assertions'
            values = itertools.cycle(markers)
            text, rewritten = re.subn(pattern, lambda _: '- [' + next(values) + ']', text, flags=re.M)
            assert rewritten == count
            expected = list(itertools.islice(itertools.cycle(markers), count))
            assert re.findall('^- \\[([^\\]]*)\\](?= T-001-01-)', text, re.M) == expected
        if extra_assertion is not None:
            text += '\n' + extra_assertion + '\n'
        self.task.write_text(text, encoding='utf-8')
        assert self.task.read_text(encoding='utf-8') == text
        process = subprocess.run(['python3', '-B', str(self.root / 'docs/testing/validate_docs.py'), '--root', str(self.root), '--skip-manifest'], capture_output=True, text=True)
        assert process.stdout, process.stderr
        return (process.returncode, json.loads(process.stdout))

    def test_each_protocol_planning_state_is_admitted(self):
        for status in ('planned', 'ready', 'in_progress', 'blocked', 'in_review', 'done'):
            code, report = self.run_fixture(status)
            assert code == 0, report['issues']

    def test_foreign_missing_and_nonscalar_statuses_are_reported(self):
        for status in ('complete', 'pass', 'fail', 'not_run', '', None, [], {'bad': 'value'}):
            code, report = self.run_fixture(status)
            assert code != 0
            assert 'planning_status' in {issue['kind'] for issue in report['issues']}
        code, report = self.run_fixture(None, missing=True)
        assert code != 0
        assert 'planning_status' in {issue['kind'] for issue in report['issues']}

    def test_mixed_checked_assertions_keep_the_same_inventory(self):
        _, baseline = self.run_fixture('in_review', markers=[' '])
        for markers in (['x', 'X', ' '], ['X']):
            code, report = self.run_fixture('in_review', markers=markers)
            assert code == 0, report['issues']
            assert report['counts']['task_assertions'] == baseline['counts']['task_assertions']

    def test_invalid_markers_do_not_satisfy_assertion_presence(self):
        code, report = self.run_fixture('in_review', markers=['y', '*'])
        assert code != 0
        kinds = {issue['kind'] for issue in report['issues']}
        assert 'missing_assertions' in kinds
        assert 'invalid_assertion_marker' in kinds

    def test_invalid_markers_cannot_hide_beside_valid_assertions(self):
        code, baseline = self.run_fixture('in_review', markers=[' '])
        assert code == 0, baseline['issues']
        for markers in (['y', 'x', ' '], ['X', '*', 'x']):
            code, report = self.run_fixture('in_review', markers=markers)
            assert code != 0
            kinds = {issue['kind'] for issue in report['issues']}
            assert 'invalid_assertion_marker' in kinds
            assert 'missing_assertions' not in kinds
            assert report['counts']['task_assertions'] == baseline['counts']['task_assertions']
        for ident, issue in (('T-001-02-01', 'assertion_owner'), ('T-001-01-01', 'duplicate_assertions')):
            code, report = self.run_fixture('in_review', extra_assertion=f'- [y] {ident}: Fixture.')
            assert code != 0
            kinds = {item['kind'] for item in report['issues']}
            assert 'invalid_assertion_marker' in kinds
            assert issue in kinds

    def test_checked_foreign_and_duplicate_ids_are_rejected(self):
        cases = [('T-001-02-01', 'assertion_owner'), ('T-001-01-01', 'duplicate_assertions')]
        for ident, issue in cases:
            code, report = self.run_fixture('in_review', extra_assertion=f'- [x] {ident}: Fixture.')
            assert code != 0
            assert issue in {item['kind'] for item in report['issues']}
if __name__ == '__main__':
    raise SystemExit(pytest.main([__file__]))
