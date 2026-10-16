"""Build an operator-only viewer sidecar from complete frozen result directories.

No author manifest, source checkout mutation, provider, service or browser action.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path


def inventory(root):
    files = {}
    for directory, dirs, names in os.walk(root, followlinks=False):
        for name in dirs:
            if (Path(directory) / name).is_symlink():
                raise ValueError('This disposable fixture cannot capture a selected directory symlink')
        for name in names:
            p = Path(directory) / name
            if p.is_symlink() or not p.is_file():
                raise ValueError('This disposable fixture cannot capture a selected special entry')
            h = hashlib.sha256()
            with p.open('rb') as f:
                for chunk in iter(lambda: f.read(65536), b''):
                    h.update(chunk)
            files[p.relative_to(root).as_posix()] = {'bytes': p.stat().st_size, 'sha256': h.hexdigest()}
    return files


def inventory_digest(files):
    # Match existing json.MarshalIndent(fileRecord map), including Go HTML escapes.
    body = json.dumps(files, ensure_ascii=False, sort_keys=True, indent=2)
    for character in '<>&\u2028\u2029':
        body = body.replace(character, '\\u%04x' % ord(character))
    return hashlib.sha256(body.encode()).hexdigest()


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--result-a', type=Path, required=True)
    p.add_argument('--result-b', type=Path, required=True)
    p.add_argument('--page-a', required=True)
    p.add_argument('--page-b', required=True)
    p.add_argument('--title-a', default='First explanation')
    p.add_argument('--title-b', default='Second explanation')
    p.add_argument('--project-a', required=True)
    p.add_argument('--project-b', required=True)
    p.add_argument('--source-bindings', type=Path)
    p.add_argument('--references', type=Path, help='Operator map by slot then ID: version/page/region')
    p.add_argument('--output', type=Path, required=True)
    a = p.parse_args()
    roots = {'a': a.result_a.resolve(strict=True), 'b': a.result_b.resolve(strict=True)}
    if any(a.output.resolve().is_relative_to(root) for root in roots.values()):
        p.error('Sidecar output must be outside original result directories')
    versions = {}
    captures = json.loads(a.source_bindings.read_text())['captures'] if a.source_bindings else []
    for key in ('a', 'b'):
        files = inventory(roots[key])
        page = getattr(a, 'page_' + key)
        if page not in files:
            p.error('Selected initial page missing from complete result inventory')
        evidence = {}
        for item in captures:
            if item['project'] != getattr(a, 'project_' + key):
                continue
            data = Path(item['capture']).read_bytes()
            if hashlib.sha256(data).hexdigest() != item['excerpt_sha256']:
                raise ValueError('Captured source changed before sidecar construction')
            evidence[item['handle']] = {'project': item['project'], 'revision': item['commit'],
                'path': item['path'], 'line_start': item['line_start'], 'line_end': item['line_end'],
                'capture_path': str(Path(item['capture']).resolve()), 'sha256': item['excerpt_sha256']}
        versions[key] = {'root': str(roots[key]), 'title': getattr(a, 'title_' + key),
            'default_page': page, 'files': files, 'evidence': evidence, 'references': {}}
    if a.references:
        for key, refs in json.loads(a.references.read_text()).items():
            for ident, target in refs.items():
                slot = target['version']
                if target['page'] not in versions[slot]['files']:
                    raise ValueError('Pinned prior reference page missing')
                versions[key]['references'][ident] = {**target,
                    'inventory_sha256': inventory_digest(versions[slot]['files'])}
    fd = os.open(a.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as f:
        json.dump({'versions': versions}, f, ensure_ascii=False, indent=2)
        f.write('\n')
    print(json.dumps({'sidecar': str(a.output), 'versions': {key: {'file_count': len(v['files']),
        'inventory_sha256': inventory_digest(v['files'])} for key, v in versions.items()},
        'provider_calls': 0, 'original_files_modified': False}))


if __name__ == '__main__':
    main()
