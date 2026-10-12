"""Reproduce the reviewed single-file design prototype from Grok fragments and local adapters."""
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent
f = json.loads((ROOT / 'grok-fragments.json').read_text())
reader_revision = json.loads((ROOT / 'grok-reader-r3.json').read_text())['applied']

# Reviewed R5 corrections are applied in order to the preserved Grok originals.
# Exact, unique matching makes a stale or overlapping revision fail visibly.
r5_records = []
for revision_file in (
    'grok-product-r5.json', 'grok-management-r5.json',
    'grok-settings-r5.json', 'grok-context-r5.json',
):
    revision = json.loads((ROOT / revision_file).read_text())['applied']
    if not isinstance(revision, dict) or not isinstance(revision.get('css'), str):
        raise ValueError(f'Incomplete R5 revision: {revision_file}')
    for index, edit in enumerate(revision.get('fragmentEdits', []), 1):
        key, old, new = edit['fragment'], edit['oldText'], edit['newText']
        if not old or f[key].count(old) != 1:
            raise ValueError(f'R5 edit must match once: {revision_file}:{index}:{key}')
        f[key] = f[key].replace(old, new, 1)
    r5_records.append(revision)

# Keep Grok source fragments intact. These are integration and language corrections.
reader = f['reader'].replace('<p class="eyebrow">해룡</p>', '<p class="eyebrow">Podway · 실행기 설명</p>')
reader = reader.replace('>노트</button>', '>메모</button>').replace('>버전 목록</button>', '>버전 비교</button>').replace('<summary>그 외</summary>', '<summary>문서 동작</summary>')
source_copy = [
    '이전 실행이 늦게 끝나도, 커밋 전에는 그 결과가 현재 소유자와 같은지 본다. 소유자가 바뀌었으면 그 결과는 이번 커밋에 넣지 않는다.',
    '아래 코드와 표는 설명을 읽기 위한 샘플이다. 예시 경로는 저장소의 실제 트리와 따로 둔다.',
    '버전이나 페이지를 바꾸면 이 본문만 갈아 끼운다. 선택과 도구는 본문 밖에 남는다.'
]
copy = [
    '이전 실행이 늦게 끝나더라도 결과를 저장하기 전에 현재 작업 소유권을 확인합니다. 재시도가 새 소유권을 발급했다면, 이전 결과가 들고 있는 값은 더 이상 현재 값이 아닙니다.',
    '결과의 소유권과 저장소의 현재 소유권이 같을 때만 결과를 반영합니다. 서로 다르면 오래된 결과로 처리합니다. 새 실행이 이어받은 작업을 이전 결과가 덮어쓰지 않도록 하는 규칙입니다.',
    '아래 예시에서 결과를 만든 뒤 새 실행에 소유권을 넘기고, 이전 결과를 도착시켜 보세요. 같은 작업이라도 실행 세대가 달라지면 결과를 다르게 판단해야 하는 이유를 확인할 수 있습니다.'
]
for old, text in zip(source_copy, copy):
    reader = reader.replace(old, text)
reader = reader.replace('>시뮬레이션</button>', '>소유권 비교 시작</button>')
reader = reader.replace('<tr><td>원본</td><td>2026-10-12 이후 변경</td><td>다시 읽기</td></tr>', '<tr><td>새 실행</td><td>현재 소유권과 일치</td><td>결과 반영</td></tr>')
reader, toolbar_count = re.subn(
    r'<div class="reader-toolbar">.*?</div>\s*(?=<article\b)',
    lambda match: reader_revision['readerToolbarHtml'] + '\n',
    reader, count=1, flags=re.S,
)
if toolbar_count != 1:
    raise ValueError('Expected one original Reader toolbar before the article')
reader = reader.replace('>소유권 비교 시작</button>', '>' + reader_revision['simulationButtonLabels'][0] + '</button>')
reader = reader.replace('아직 실행하지 않았습니다.', reader_revision['simulationStatusCopy'][0])
f['reader'] = reader

shell = f['shell'].replace("class='context hidden'", "class='context' hidden aria-label='읽기 도구'")
shell = shell.replace("<aside class='sidebar'>", "<aside class='sidebar' aria-label='주 메뉴'>")
shell = shell.replace('</svg>라이브러리</button>', '</svg>서재</button>').replace('</svg>유지보수</button>', '</svg>정비</button>')
shell = shell.replace("<span class='chip'>미리보기</span>", "<span class='chip'>디자인 시안 · 샘플 데이터</span>")
shell = shell.replace('>시나리오</button>', '>시안 상태</button>')
f['shell'] = shell

for key in ('home','library','versions'):
    f[key] = f[key].replace('선호 개정','기본 버전').replace('개정','버전')
    f[key] = f[key].replace('개인 검토','내 검토').replace('검토함','검토 완료')

if 'value="reviewing"' not in f['library']:
    f['library'] = re.sub(r'(<select[^>]*id="reviewFilter"[^>]*>.*?)(</select>)', r'\1<option value="reviewing">검토 중</option>\2', f['library'], flags=re.S)

f['sources'] = f['sources'].replace('연결이 끊긴 원본이 있어도 저장된 설명은 그대로 읽을 수 있다. 질문마다 다시 확인하지 않는다.', '원본을 사용할 수 없어도 저장된 설명은 계속 읽을 수 있습니다. 질문을 시작할 때 사용할 기본 범위를 선택하세요.')

styles = '\n'.join([f.pop('css'), f.pop('cssAppend', ''), (ROOT/'prototype-integration.css').read_text(), reader_revision['cssAppend']])
styles += '\n' + '\n'.join(revision['css'] for revision in r5_records)
styles += '\n' + (ROOT / 'prototype-r5-safety.css').read_text()
shell_html = f['shell']
scripts = 'window.HR = {fragments:' + json.dumps(f, ensure_ascii=False).replace('</', '<\\/') + '};\n'
for filename in [
    'prototype-core.js', 'prototype-reader.js', 'prototype-management.js', 'prototype-settings.js',
    'prototype-product-r5.js', 'prototype-management-r5.js', 'prototype-settings-r5.js',
    'prototype-context-r5.js', 'prototype-recovery-r5.js',
]:
    scripts += '\n' + (ROOT/filename).read_text() + '\n'
scripts += '\nHR.start();\n'
scripts = scripts.replace('</script', '<\\/script')
page = '''<!DOCTYPE html>
<html lang="ko" data-theme="light" data-prototype-revision="r5-unapproved">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="color-scheme" content="light dark">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; connect-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'">
<title>해룡 · 전체 제품 디자인 시안</title>
<style>''' + styles + '''</style>
</head><body>
''' + shell_html + '''
<button id="navBackdrop" data-action="nav:close" aria-label="메뉴 닫기" hidden></button>
<dialog id="appDialog" class="dialog" aria-labelledby="dialogTitle">
<header class="dialog-head"><h2 id="dialogTitle"></h2><button type="button" class="button ghost" data-action="dialog:close" aria-label="대화상자 닫기">닫기</button></header>
<div id="dialogBody" class="dialog-body"></div><footer id="dialogFooter" class="dialog-foot"></footer>
</dialog>
<div id="toast" role="status" aria-live="polite" hidden></div>
<noscript>이 시안의 화면 전환에는 JavaScript가 필요합니다. 일반 브라우저에서 파일을 열어 주세요.</noscript>
<script>''' + scripts + '''</script>
</body></html>
'''
(ROOT/'prototype.html').write_text(page)
(ROOT/'haeryong-prototype.html').write_text(page)
print(json.dumps({'artifact':'prototype.html','bytes':len(page.encode()),'fragments':len(f)}, ensure_ascii=False))
