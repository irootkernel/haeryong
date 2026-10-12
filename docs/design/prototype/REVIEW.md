# Haeryong design review record

Date: 2026-10-12 / Latest evidence: 2026-10-13 KST.  
Baseline: docs v1.6.2, commit b82793a.  
현재 판정: **R5 전체 제품 소스 통합. 기존 탭 63개 캡처와 일부 R5 CSS 배치 확인. 새 R5 HTML·JavaScript의 브라우저 검증 대기. 디자인 미확정.**

## R5 전체 제품 검토 결과

### 범위와 진행 방식

Aside로 사용자가 이미 열어 둔 정확한 탭에 연결했습니다. 주요 11개 화면 경로, 가져오기·설명 만들기, 설정 10개 항목, 정비 보고서와 주요 상태·대화상자를 실제로 열어 확인했습니다. 초기 화면과 상태, 부분 수정 확인 49장 및 R5 CSS 임시 미리보기 14장, 총 63장과 메타데이터를 보존했습니다.

새 파일을 열거나 새로고침하지 않았습니다. 실제 탭의 HTML·JavaScript는 R3이며, R4 및 단계적으로 검토한 R5 CSS만 임시 적용했습니다. 이는 새로운 R5 소스 전체의 브라우저 실행 검증이 아닙니다.

### 실제 Grok 요청과 보정

| 묶음 | 범위 | 실제 세션 | 보정 요청 |
|---|---|---|---|
| A | 홈·서재·프로젝트·원본·버전 비교 | `d8dd1f86-fd09-4fae-af1b-e9667da1afde` | `2ab67b65-0ca8-4843-9fb7-74a88b35384e` |
| B | 정비·발견·초안·가져오기·생성·보관·삭제 | `1769fc93-6bd8-41e8-bf04-018be5701b0f` | `434a6e0d-6c23-49cf-bc0f-7ae325fea662` |
| C | 설정·연결 | `6e69a362-ce66-4155-a111-9e8d4421c4a7` | `27e62815-f67f-4af6-8513-a13e3e423a9b` |
| D | Reader 보조 도구 | A와 같은 세션 | `cf437be5-a696-4892-a078-9d6d652fe83b` |

모델은 실제 CLI의 `grok-4.7-build-fast`입니다. 타임아웃 뒤 export에서 회수한 응답, 실패와 재요청도 원본 기록에 남겼습니다. 요청 ID를 확인할 수 없던 첫 응답에 임의의 ID를 붙이지 않았습니다. 캡처는 ChatGPT가 직접 검토했고, Grok에는 소스와 텍스트 관찰을 전달했습니다. 이미지 픽셀을 Grok에 전달한 것으로 기록하지 않습니다.

Grok 후보의 전역 스타일 영향, 다크 모드 버튼 색상, 구조 확인·의미 확인의 구분, 파일 첨부 의미 보존 등을 검토하고 재수정을 요청했습니다. 최종 후보를 원본 화면 조각에 정확한 문자열 치환으로 적용하고 별도 상태 어댑터로 통합했습니다.

### 확인한 핵심 문제와 대응

1. **닫힌 메뉴 배경막이 클릭을 차단:** 920×1324 실제 화면과 DOM hit test에서 확인했습니다. 숨김 상태가 표시 규칙보다 우선하도록 고친 뒤 같은 크기의 화면과 입력 대상, 메뉴 열기·닫기, 샘플 메모 저장을 확인했습니다.
2. **버전 비교가 사용 가능한 폭의 절반에 갇힘:** 중첩 그리드 규칙을 바로잡아 두 비교 열이 사용 가능한 폭을 나누게 했습니다. 기존 탭의 R5 CSS 미리보기에서 두 열이 펼쳐지는 것을 확인했습니다.
3. **정비의 제목·수치·상자가 반복돼 결과가 아래로 밀림:** 제목 크기와 상자 중첩을 줄이고 보고서 영역을 위로 끌어올렸습니다. 범위·최신성·발견 항목의 CSS 배치를 확인했습니다.
4. **설정 적용 범위가 모호하고 백업 결과가 정보 화면에 남음:** 일반 설정을 즉시 반영과 미리보기로 정리하고 다른 항목의 초안·적용·취소를 분리했습니다. 피드백을 해당 항목에 한정했습니다. 새로운 상태 처리는 소스 및 메모리 상태 검사 결과입니다.
5. **연결 상태와 주요 버튼의 관계가 약함:** 비교 코드와 상태를 앞에 두고 단계별 행동을 구분했습니다. 시연 버튼을 접고 승인 결과를 확인한 뒤 원래 문서로 돌아가도록 작성했습니다. 코드·카드 배치는 실제 CSS 미리보기, 새로운 상태 행동은 소스 검사입니다.
6. **예외 배너가 문제 확인 위치 대신 일반 상태로만 돌아감:** 원본·질문 설정·메모·확인 범위·초안으로 이어지는 행동으로 변경했습니다. 시나리오와 작성 중인 내용은 유지합니다.

### 통합 검토에서 추가로 고친 두 가지

- 메모 복구 버튼이 기존 Reader의 패널 열기 경로를 건너뛰어 입력 포커스와 닫기 후 복귀를 놓치던 부분을 수정했습니다. 재렌더 후 살아 있는 배너 버튼을 원래 Reader에 전달합니다. 실제 원본 모듈을 사용하는 메모리 DOM 검사 9건이 통과했습니다.
- 보존 확인창을 Escape로 닫을 때 확인 대기 값이 남는 부분을 정리했습니다. 사용자 설정 초안과 저장된 값은 유지하고, 취소가 다른 리스너에 의해 막혔으면 대기 값도 유지합니다. 제한된 이벤트·상태 확인 6건이 통과했습니다.

이는 브라우저의 실제 키보드 순환을 검증한 결과가 아닙니다. 상태 검사 범위와 재현 정보는 각 R5 기록 및 `recovery-r5-validation.json`에 있습니다.

### 검증 판정

| 구분 | 결과 |
|---|---|
| 주요 화면·대표 상태의 기존 탭 조작·캡처 | 완료, 63개 이미지와 개별 메타데이터 |
| R5 CSS의 대표 화면 배치 | 14개 캡처 확인, 단계별 적용 범위 기록 |
| 닫힌 메뉴의 클릭 차단 수정 | 같은 920×1324 크기에서 확인 |
| 새 R5 HTML·JS 통합 및 제한된 상태 확인 | 완료, 최종 구문·재생성 결과는 `STATIC-R5.json` |
| 새로운 전체 R5 파일의 브라우저 로드 | 미실행 |
| 휴대폰·터치·체계적 크기 변경 | 미검증 |
| 전체 키보드·스크린리더·접근성 | 미검증 |
| 실데이터·실제 AI·인증·파일·백업 연동 | 시안 범위 밖 |
| 사용자 과제 기반 사용성 평가·디자인 승인 | 미완료 |

초기 1280×1368과 후반 920×1324 캡처는 서로 다른 관찰 조건입니다. 창 크기를 직접 조절한 통제된 반응형 비교로 취급하지 않습니다. 낮은 JPEG 품질은 구조 확인에는 쓸 수 있지만 미세한 색과 글자 품질의 확정 근거가 되지 않습니다.

현재 결과물은 `haeryong-prototype.html`, `haeryong-design-review-r5.html`, `haeryong-design-r5.zip`입니다. 생산 코드와 제품 요구사항을 변경하거나 커밋·배포하지 않았습니다.

## R0–R4 검토 이력 — 당시 결과 보존

아래의 상태·수치·한계는 해당 과거 버전의 기록입니다. 현재 판정은 위 R5 절을 기준으로 봅니다.

### Authorship and actual revision loop

- Grok was invoked through the installed terminal CLI, using its existing configured login.
- Herdr was not controlled because its required caller context could not be verified.
- First full draft session: `2de6d66c-9dc6-4f68-a1f2-4a1764d6170b`. Rejected.
- R1 full rewrite in the same session timed out without an assistant artifact.
- R1 was split into bounded real Grok modules:
  - Visual shell/Reader: `8c3d33ed-779e-4e6e-8ea6-08d8ae701ea1`.
  - Browse/Projects/Versions: `870fcbde-c7e3-4aee-9144-65dd2903a8d6`.
  - Maintenance/Lifecycle: `e7a3d124-d18b-4e90-8503-31d42d2dcc9e`.
  - Settings/Connect/Entry: `cf66eb2f-f3a4-455d-ae7d-8cc1b299d50a`.
- R2 Reader/Source/CSS correction: `f3a78245-7440-4fc8-8d17-50a8ab252c48`.
- R3 Reader correction after an actual R2 screenshot: `015f1532-c438-4c74-8bd2-2be0c4fc1f67`, two actual requests. Prompts, initial response, follow-up and applied changes are preserved in `grok-reader-r3.json`.
- R4 responsive correction continued in the same actual Grok session. The timed-out initial reply was recovered through the documented `grok export` command; selector/scroll corrections were requested as `353fe3ba-e719-432c-9c72-212da59b1d0d`. Requests, replies and applied CSS are preserved in `grok-responsive-r4.json`.
- Successful bounded generations used `grok-4.7-build-fast` with low reasoning effort and tools disabled.
- ChatGPT reviewed the specification, authored revision requests, integrated Grok's visual fragments, and corrected shared-state behavior and inconsistent copy/styles.

No production implementation, source requirement changes, project dependency installation, commits, pushes, public deployment, real provider requests from the prototype, or real user-data mutations were performed.

### Rejected R0 findings

1. Extra closing braces in saveNote and fakePair prevented the entire inline script from parsing.
2. CSS display rules overrode hidden panes.
3. Notes and questions were global and lost their document/version/page identity.
4. Personal review was incorrectly described as an independent review request.
5. Default desktop context was always open and narrow layouts could retain three columns.
6. Import/create/maintenance/settings frequently changed only explanatory text.
7. Invented upload caps and extension rules contradicted the specification.
8. Purge showed its impact after mutation, while restoration did not update the shared lists.
9. Dark inputs, pane closure and focus semantics were incomplete.

### R1 and R2 review changes

- Split screen authoring with a common component vocabulary.
- Requested a quiet Reader, grouped navigation, explicit source scope and full product coverage.
- Requested Reader primary actions above content and an inner prose-only article slot.
- Corrected invented source modes, document-version/source-revision confusion, maritime specimen copy, old dates and inconsistent Korean terminology.
- Integrated per-target draft state, explicit save/cancel, original question target/source capture, and independent review/freshness/lifecycle.
- Connected imports and publication to a shared document/version model.
- Added pre-mutation purge/retention impact and prior-lifecycle restoration.
- Corrected separate preferred-open and continue-reading routes, valid comparison for single-version documents, project-local source defaults, and access to deleted content.
- Corrected closed-dialog display, dark theme token conflicts, minimum target height and mobile panel closure in CSS.
- Preserved original Grok fragments and actual revision prompts in the bundle.

### 실제 R2 Reader 시각 검토와 R3 수정

사용자가 Mac Chrome에서 직접 연 R2 시안을 정상 화면 캡처 권한으로 촬영했습니다. ChatGPT와 별도 시각 리뷰어가 같은 이미지를 확인했습니다. Grok에는 두 리뷰어의 관찰을 전달했으며, Grok이 이미지를 직접 보았다고 주장하지 않습니다.

| 캡처 증거 | 값 |
|---|---|
| 파일 | `haeryong-reader-before-20261012-2215.png` |
| 원본 HTML SHA-256 | `2d1dabec98c667a9225c0aa5001e57f1cfd1fa598bc59349332a0a7147917aee` |
| PNG | 271,715 bytes, 1326×1456 |
| PNG SHA-256 | `954f9024ce5a4f90231ae9ab0dfcbe1b78653d2d677bdcc33322924a30f94eba` |
| 대상 | Chrome 창 16359, 논리 크기 1280×1410 |
| 확인 범위 | R2 데스크톱 Reader 한 화면. CSS 뷰포트·확대율 미확인 |

본문·코드·표의 읽기 비율과 절제된 색상은 안정적이었고, 확인한 화면에 큰 겹침이나 붕괴는 없었습니다. 다음 네 항목은 실제 관찰에 근거해 수정했습니다.

| R2 관찰 | R3 반영 |
|---|---|
| 문서 동작 메뉴만 다음 줄로 떨어짐 | 위치 선택과 문서 작업을 두 그룹으로 나누고 버전 비교를 문서 관리 메뉴로 이동 |
| 긴 페이지 제목이 선택 칸에서 잘림 | 버전 칸보다 페이지 칸에 넓은 폭을 배정하고 전체 선택 제목 툴팁 추가 |
| 내 검토·원본 상태·날짜의 시각적 구분이 약함 | 독립된 상태 배지, 원본 변경 색상, 명시적인 버전 생성일 문구 적용 |
| 예제 안내와 다음 단계 확인 버튼이 대응하지 않음 | 결과 만들기, 소유권 넘기기, 이전 결과 도착, 초기화의 네 버튼명과 단계 설명 적용 |

Grok의 첫 R3 응답에서 메뉴 펼침 표시가 빠지고, 상태 배지의 접두어가 중복되며, 예전 결과의 도착 시점이 모호했던 부분을 반려해 다시 요청했습니다. 최종 통합에서는 기존 동작 식별자와 네 상태 예제를 보존하고, 480px 이하에 두 열 작업 배치를 작성했습니다. Reader 읽기 면은 테마의 캔버스 색을 사용합니다.

위 표는 수정 구현 기록입니다. 이후 실제 R3 화면을 다시 캡처한 결과는 다음 절에 구분해 기록합니다.

### 실제 R3 Reader 재캡처 결과

사용자가 같은 Mac Chrome 창을 수동으로 새로고침한 후, 실제 Grok 실행을 통해 정상 OS 화면 캡처를 수행했습니다. ChatGPT와 별도 시각 리뷰어가 이미지를 직접 확인했습니다.

| 캡처 증거 | 값 |
|---|---|
| 파일 | `haeryong-reader-r3-20261012-2246.png` |
| 원본 HTML SHA-256 | `8b91299e21733fa3e76cc20bfb2dc65241b7087aaab2ba2961d48c58141ad135` |
| PNG | 275,966 bytes, 1326×1456 |
| PNG SHA-256 | `394617956d8568dfc5e0421c7794c9db80dd206e3e7758bf3fab6310d393ba3a` |
| 대상 | Chrome 창 16359, 논리 크기 1280×1410 |
| Grok 캡처 세션 | `a4d91e2b-90c8-4edb-a24c-22c0d6831b69` |
| 캡처 요청 | `66dbcf6c-dd69-467a-9fd9-8c0cb872e0aa` |
| 확인 범위 | R3 기본 데스크톱 Reader 한 화면. CSS 뷰포트·확대율 미확인 |

두 리뷰어가 확인한 결과는 다음과 같습니다.

1. 상단 두 줄이 위치 선택과 문서 작업의 의도적인 그룹으로 읽힙니다. 전체 높이가 크게 줄어든 것은 아닙니다.
2. 현재 선택한 페이지 제목이 전체 표시됩니다.
3. 검토·원본 배지와 버전 생성 날짜를 구분하기 쉬워졌습니다.
4. 결과 만들기 버튼과 현재 실행 세대 1의 준비 상태 상자가 예제의 첫 행동을 분명하게 안내합니다.

관찰한 화면에서 새로 생긴 명확한 겹침이나 잘림은 없었습니다. **이 기본 Reader 화면은 추가 UI 수정 없이 시각 검토를 마쳤습니다.** 이 캡처에서는 실제 버튼 클릭, 예제 후속 상태, 펼친 문서 관리 메뉴·질문 패널을 확인하지 않았습니다. 이후 추가로 확인한 질문 패널 초기 상태는 다음 절에 기록합니다.

### 실제 R3 질문 패널 초기 상태 검토

사용자가 질문 패널을 연 후, 실제 Grok 실행이 기존 화면 캡처 권한으로 해당 창을 촬영했습니다. ChatGPT와 별도 시각 리뷰어가 이미지를 직접 확인했습니다. HTML과 코드는 변경하지 않았습니다.

| 캡처 증거 | 값 |
|---|---|
| 시각 | 2026-10-12 23:16 KST |
| 파일 | `haeryong-reader-ask-20261012-2316.png` |
| 원본 HTML | 241,213 bytes, SHA-256 `8b91299e21733fa3e76cc20bfb2dc65241b7087aaab2ba2961d48c58141ad135` |
| PNG | 336,032 bytes, 1326×1456 |
| PNG SHA-256 | `95f9fa271d8edb2b4de5cfac03d728b338bb4a5702e45c309b62fa9493bcda7b` |
| 대상 | Chrome 창 16359, `해룡 · 전체 제품 디자인 시안`, 논리 위치 (3840, 30), 크기 1280×1410 |
| 권한 상태 | 화면 캡처 허용, 추가 권한 요청 없음 |
| Grok 캡처 세션 | `a4d91e2b-90c8-4edb-a24c-22c0d6831b69` |
| 캡처 요청 | `4f1d09a4-f548-4385-bc8c-e8163f43727f` |
| 확인 범위 | R3 질문 패널 초기 상태 한 화면. CSS 뷰포트·확대율 미확인 |

Reader와 오른쪽 340px 질문 패널은 충돌하지 않았고 긴 문서 제목이 표시됐습니다. 문서명·버전·경로·인용 선택이 구분되며, 원본 선택 → 입력칸 → 보내기의 위계가 명확했습니다. 질문 탭 활성 상태와 닫기 조작의 표시도 분명했습니다. 두 리뷰어 모두 새 시각 결함을 발견하지 않았습니다.

**이 캡처에서 확인한 것은 입력 전 초기 상태의 실제 렌더링입니다.** 당시에는 입력·전송·문맥 유지와 키보드·모바일 사용을 검증하지 않았습니다. 이후 Aside에서 확인한 실제 조작과 다른 크기의 결함은 다음 절에 구분합니다.

### Aside 실제 조작과 R4 수정 증거

사용자가 이미 열어 둔 정확한 시안 탭에 Aside CLI로 연결했습니다. 추가 로그인이나 권한 요청 없이 연결·스냅샷·클릭·입력을 수행했습니다. 이때 열린 HTML과 JavaScript는 R3였습니다.

확인한 동선은 질문하기 클릭 → 한국어 샘플 질문 입력 → 페이지 `index`에서 `flow`로 전환 → 초안과 원래 `index` 대상 유지 → 패널 닫기·재열기 후 유지 → 보내기 → 대기 상태 → 샘플 답변 완료입니다. 답변에 원래 `index` 대상과 실제 AI 호출이 없는 샘플이라는 안내가 유지됐습니다. 다른 보조 도구로의 탭 전환이나 실제 AI 응답 검증은 포함하지 않습니다.

| 캡처와 측정 | 결과와 검증 범위 |
|---|---|
| `haeryong-aside-ask-current.png`, 155,761 bytes, 920×1324, 2026-10-12 23:45:23 KST | 패널이 제목·본문 줄 끝·표의 결과 열을 가림. 예제 코드 세 줄 자체는 보임. 촬영 당시 CSS 뷰포트 미기록 |
| 이후 같은 탭, 2026-10-12 23:51:36 KST, 동시에 측정한 CSS 1280×1368·DPR 1 | 본문 영역 716px + 패널 340px, 패널은 static, x=940. 앞의 920px PNG와 다른 시점의 기록 |
| `haeryong-aside-r4-style-preview.png`, 224,722 bytes, 1280×1368, 2026-10-13 00:10:15 KST | 열린 R3 탭에 R4 CSS 2,068자만 임시 적용. 같은 호출의 CSS 1280×1368·DPR 1, 본문 영역 716px + 패널 340px. ChatGPT가 실제 이미지를 보고 겹침 없음을 확인 |

위 두 PNG의 SHA-256은 각각 `72a38e8b04e3ae1cd633f1a8ad35b9ecfc90258eb67a2f66a59156d8e5af16fd`, `eecf875e60f9c775903f1ae56dd8cf9040b91a4f94c8edfb941062bfd8c174e2`입니다. 전체 측정과 조작 기록은 `ASIDE-VALIDATION.json`에 있습니다. 이후 1280px 측정값을 앞선 920px 이미지에 결합하지 않습니다.

코드에서는 CSS 폭 901~1279px에 패널을 고정하면서 본문 공간을 확보하지 않는 규칙을 확인했습니다. Grok R4 제안은 768px 이상에서 본문과 패널을 실제 열로 배치하고, 768~1279px 탐색을 펼침 메뉴로 바꾸며, 768px 미만에서 패널을 전체 높이로 전환합니다. 첫 제안의 메뉴 선택자를 실제 `.sidebar.open`과 맞추고 본문 전체의 가로 스크롤을 제거하도록 다시 요청했습니다. 최종 CSS는 `prototype-integration.css`에 통합했고, `prototype-core.js`의 Tab 제한 조건은 `innerWidth<1280`에서 `innerWidth<768`로 바꿨습니다. 기존 Grok 화면 조각과 R3 기록은 변경하지 않았습니다.

**R4 미리보기는 기존 탭에 임시 CSS를 적용한 결과입니다. 파일 열기·새로고침은 실행하지 않았고 JavaScript는 R3 그대로였습니다.** 좁은 폭 수정, R4 키보드 변경과 새 파일 로드를 검증한 것으로 취급하지 않습니다. CLI의 `setViewportSize`는 지원되지 않았으며, 일부 요소 선택 API는 관찰한 새 스냅샷 참조로 대체했습니다. Aside 연결과 일부 조작 자동화는 확인했지만 모든 API나 모든 제품 동선을 검증한 것은 아닙니다.

### Evidence-based gate status

D03·D05·D09의 기존 상태 검사 결과는 R2 이력입니다. 별도로 R3 질문 동선을 Aside에서 확인했고, R4 소스 검사와 CSS 임시 미리보기를 수행했습니다. 각 범위를 합쳐 R4 전체 검증으로 취급하지 않습니다.

| Gate | Status | Evidence / remaining work |
|---|---|---|
| D01 Full product screen authoring | Present | 11 main routes plus Reader contexts, report tabs, Settings sections and dialogs |
| D02 Reading-first hierarchy | R3 desktop review completed; narrow capture found a defect | R4 temporary CSS has no overlap at paired 1280×1368; narrow correction remains unverified |
| D03 Draft/context semantics | Historical state tests plus actual R3 Ask flow | R3 draft/target retained across index→flow and panel reopen; mock answer retains original target |
| D04 Responsive layout | R4 source integrated; narrow recheck pending | 920×1324 R3 PNG obscures article, CSS viewport unknown; only 1280×1368 R4 live CSS preview verified |
| D05 Cross-screen local transitions | State tests passed for representative flows | Imports, versions, review, drafts, lifecycle, pairing; no browser end-to-end assertion |
| D06 Product terminology and boundaries | Corrected in integration | No artifact cap/extension allowlist; separate review, freshness and lifecycle |
| D07 Color and touch intent | Token math / declarations checked | Selected foreground/background pairs below; actual component and interaction audit pending |
| D08 Modal/focus and keyboard | R4 source change, pending browser QA | Tab-trap threshold changed to below 768px; live preview retained R3 JavaScript |
| D09 Shared model consistency | Four root regression cases passed | Preferred vs continue, project source override, v1 creation, purge/read guard |
| D10 Offline artifact integrity | R4 static checks passed | One inline script parses; applied CSS and 768px condition match source; no external script tags or direct fetch/XHR/WebSocket calls |

### Historical R2 verification

#### R2 whole artifact

- Extracted the R2 HTML's one inline script and ran `node --check`: exit 0.
- Checked external script tags and direct fetch/XMLHttpRequest/WebSocket invocations: zero.
- The standalone file declares a Content Security Policy that disables external resources and connections.
- The prototype is self-contained. Runtime installation is not required.

#### Reader module

Two bounded Node VM state checks covered per-target Korean note drafts, Save/edit/cancel, retained input after navigation, captured question target/source mode, delayed answers, escaped angle brackets, independent review/freshness, disconnected/provider/session states, explicit conflict merge and non-overwriting reattachment. Reference-string and exact action wiring were also checked.

#### Management module

Eight bounded checks covered new imports without duplicate results, retained paused target and stale-base resolution, escaped requests and an inspectable generated draft, explicit publication, stale draft blocking/replacement, metadata-only approval/undo, captured scan scope with honest denominator, timer cleanup and expired-session mutation blocking.

#### Settings module

Bounded state checks covered independent retention duration/automatic purge and cancel/confirm, device revocation, pairing capture/return/re-entry/rejection/expiry/temporary device behavior, last successful backup preservation, restore-trial state, retry connection, and avoiding a whole-screen redraw during text input.

#### Shared core

Four additional isolated state cases passed:
1. Continue opens the saved v3/flow while Library opens configured v2/index.
2. A Podway source preference updates that project's override, leaving the global default unchanged.
3. A new document without versions receives a valid unreviewed v1.
4. Purge clears that sample document's version/note/question state, changes selection, and direct Reader routing rejects the deleted document.

These checks used no browser and do not establish visual, focus, touch or complete route execution correctness.

### 과거 R3 정적 검증

- 생성한 HTML의 단일 인라인 스크립트에 `node --check`를 실행했고 종료 코드 0을 확인했습니다.
- Reader 조각의 태그 구조, `versionSelect`, `pageSelect`, `article`, `simulation`, `simulationResult`, `reviewState`, `freshnessState`의 보존과 중복 부재를 확인했습니다.
- 질문·파일·메모·버전 비교·검토·다시 보기·더보기·예제 동작 식별자를 보존했습니다. 페이지 선택은 위치 그룹에, 버전 비교는 문서 관리에, 본문은 툴바 밖에 있습니다.
- 최종 HTML의 예제 버튼·상태 문구와 적용된 CSS가 R3 수정 기록 및 소스와 일치합니다. 별도 코드 리뷰에서도 구체적인 통합 회귀를 발견하지 않았습니다.
- 외부 스크립트와 직접적인 fetch/XMLHttpRequest/WebSocket 호출은 0건입니다.
- `prototype.html`과 `haeryong-prototype.html`은 각각 241,213 bytes이며 SHA-256은 `8b91299e21733fa3e76cc20bfb2dc65241b7087aaab2ba2961d48c58141ad135`로 동일합니다.
- Mac 저장소의 `docs/design/2026-10-12-grok/prototype.html`에도 R3를 반영했고, 로컬 결과물과 크기·해시가 일치함을 확인했습니다. 추적 중인 제품 소스에는 변경이 없습니다.

이 절의 검사는 구조와 연결의 정적 확인입니다. R2 상태 검사 전체를 R3에서 반복한 것은 아닙니다. 이후 실제로 수행한 R3 질문 동선은 Aside 절에 별도로 기록했습니다.

### R4 정적 통합 검사

- 두 HTML 별칭은 각각 243,200 bytes이며 SHA-256 `d19bee5cc68b9442078da793d5da012b5dbdbb87ebab731fd1802ec6f9cfcf9e`로 동일합니다.
- 생성 HTML의 인라인 스크립트 1개에 `node --check`를 실행했고 종료 코드 0을 확인했습니다. 외부 스크립트 및 직접 fetch/XMLHttpRequest/WebSocket 호출은 0건입니다.
- `grok-responsive-r4.json`의 적용 CSS가 통합 CSS와 생성 HTML에 포함돼 있고, 공통 코드의 Tab 제한이 `innerWidth<768`로 바뀐 것을 확인했습니다.
- Mac 저장소의 디자인 디렉터리에 R4 HTML·통합 CSS·공통 코드를 반영했고, 세 파일 모두 로컬 결과물과 SHA-256이 일치함을 확인했습니다.
- R4 파일 로드·새로고침과 R4 JavaScript의 실제 키보드 동작은 검증하지 않았습니다. R3 조작 결과와 1280px 임시 CSS 미리보기가 그 검증을 대신하지 않습니다.

### Selected color token checks

Historical R2 calculations from selected declared token pairs:

| Pair | Ratio |
|---|---:|
| Light body #172033 on white | 16.27:1 |
| Light secondary #59677A on white | 5.76:1 |
| White on primary blue #175CD3 | 5.99:1 |
| Dark body #E7EDF6 on #161D2A | 14.35:1 |
| Dark secondary #A8B4C6 on #161D2A | 8.05:1 |
| Dark accent #9EC5EF on #161D2A | 9.40:1 |

This is a selected-token calculation, not a full accessibility compliance claim. R3의 상태 배지를 포함한 실제 구성요소 전체의 대비 검토는 남아 있습니다.

### 브라우저 검증 이력과 남은 범위

Earlier, the cloud browser rejected local file navigation because the file protocol is not allowed. The official local HTTP application preview then returned `net::ERR_BLOCKED_BY_CLIENT`. Those attempts obtained no rendered app screenshot or interaction result, and the preview process was stopped. No alternate browser surface was used to evade that block.

이후 사용자가 직접 연 Mac Chrome 창을 정상 OS 화면 캡처로 확인해 R2·R3 Reader와 질문 패널 초기 상태를 검토했습니다. Aside로 기존 R3 탭에 연결한 뒤에는 질문 입력·전송·페이지 전환과 패널 재열기 후 초안·대상 유지를 실제로 확인했습니다. 좁은 화면의 가림을 발견해 R4를 통합했으며, 임시 CSS의 1280px 미리보기까지 확인했습니다. 파일 열기·새로고침은 수행하지 않았습니다.

Consequently the following remain open:

- R4 새 파일 로드, 좁은 폭에서 패널 가림 수정, 열림·닫힘 배치와 탐색 메뉴 검토.
- 보조 도구 간 탭 전환, 펼친 문서 관리 메뉴, 예제 후속 상태와 나머지 제품 화면의 검토.
- 실제 CSS 뷰포트 크기를 함께 기록한 반응형 배치와 넘침 검토.
- Mobile keyboard/IME visibility and touch operation.
- Keyboard focus order, native-dialog behavior and screen-reader output.
- Actual end-to-end navigation across the assembled artifact.
- Usability with representative users.

### Scope limits

This is a high-coverage local sample prototype, not complete service behavior. There is one representative active finding and draft. Replacement content and progress are preset examples. State lasts only until reload. Real uploads, AI, authentication, backup files, source verification, arbitrary imported HTML and desktop/mobile shells remain future implementation and validation work.

다음 검토는 R4 파일 로드와 좁은 화면의 Ask, 키보드, Library·Maintenance의 실제 화면과 동선입니다. 현재 묶음 이름은 `haeryong-design-r4.zip`이며 R2·R3 ZIP은 당시 기준으로 보존합니다. 일부 R3 동선과 1280px 임시 CSS 미리보기만으로 전체 디자인을 승인하지 않습니다.

