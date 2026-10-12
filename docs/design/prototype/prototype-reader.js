(function (HR) {
  'use strict';
  if (!HR) throw new Error('The Reader module needs window.HR.');

  const contextNames = new Set(['ask', 'evidence', 'notes', 'links', 'files']);
  let contextOpener = null;
  let questionSequence = 0;
  const state = () => HR.state;
  const esc = value => HR.escape(String(value == null ? '' : value));
  const byId = id => HR.$(id);
  const currentTarget = () => {
    const target = Object.assign({}, HR.target());
    const steps = state().readerSimulation || {};
    const key = HR.targetKey(target);
    if (Object.prototype.hasOwnProperty.call(steps, key)) target.scenarioStep = steps[key];
    return target;
  };
  const targetKey = target => HR.targetKey(target || currentTarget());
  const savedText = value => typeof value === 'string' ? value : (value && value.text) || '';
  const connected = () => !/disconnect|offline|session/.test(state().scenario || '');
  const providerAvailable = () => !/^provider$|provider[-_ ]?unavailable/.test(state().scenario || '');
  const sourceUnavailable = target => /^source$|source[-_ ]?unavailable/.test(state().scenario || '') || (HR.doc(target.docId) || {}).source === 'unknown';
  const actions = action => Array.from(document.querySelectorAll('[data-action]')).filter(node => node.dataset.action === action);

  function setText(id, text) {
    const node = byId(id);
    if (node) node.textContent = text;
  }

  function setValue(id, value) {
    const node = byId(id);
    if (node && document.activeElement !== node && node.value !== String(value)) node.value = value;
  }

  function targetLabel(target) {
    if (!target) return '';
    const doc = HR.doc(target.docId);
    const version = doc && doc.versions && doc.versions[target.versionId];
    const page = version && version.pages && version.pages[target.pageId];
    return [doc ? doc.title : target.title, target.versionId, page ? page.path : target.path].filter(Boolean).join(' · ');
  }

  function pageFor(target) {
    const doc = HR.doc(target.docId);
    const version = doc && doc.versions && doc.versions[target.versionId];
    return version && version.pages ? version.pages[target.pageId] : null;
  }

  function excerptFor(target) {
    const page = pageFor(target);
    if (!page || !page.html) return '선택한 페이지 전체를 질문의 문맥으로 사용합니다.';
    const holder = document.createElement('div');
    holder.innerHTML = page.html;
    const paragraph = holder.querySelector('p');
    return paragraph ? paragraph.textContent.trim() : (page.title || '선택한 페이지 전체');
  }

  function selectedArticleText() {
    const article = byId('article');
    const selection = window.getSelection && window.getSelection();
    if (!article || !selection || selection.isCollapsed || !selection.anchorNode || !article.contains(selection.anchorNode)) return '';
    return selection.toString().trim();
  }

  function noteDraft(target) {
    const captured = target || currentTarget();
    const key = targetKey(captured);
    state().noteDrafts = state().noteDrafts || {};
    state().savedNotes = state().savedNotes || {};
    let draft = state().noteDrafts[key];
    if (!draft || typeof draft === 'string') {
      const baseline = savedText(state().savedNotes[key]);
      const text = typeof draft === 'string' ? draft : baseline;
      draft = { target: Object.assign({}, captured), text, baseline, dirty: text !== baseline, conflictResolved: false };
      state().noteDrafts[key] = draft;
    }
    return draft;
  }

  function questionDraft(capture) {
    const draft = state().questionDraft || (state().questionDraft = { text: '', captured: null, quote: '', mode: 'snapshot', answers: [] });
    draft.answers = draft.answers || [];
    if (capture && !draft.captured) {
      draft.captured = currentTarget();
      draft.quote = draft.quote || selectedArticleText() || excerptFor(draft.captured);
      const settings = state().settings || {};
      const project = (HR.doc(draft.captured.docId) || {}).project;
      const override = settings[project === 'podway' ? 'projectSourcePodway' : 'projectSourceHaeryong'];
      const preference = override && override !== 'inherit' ? override : settings.defaultSource;
      draft.mode = preference === 'current' ? 'current' : 'snapshot';
    }
    return draft;
  }

  function liveMessage(message, kind) {
    const id = kind === 'note' ? 'noteStatus' : 'askStatus';
    let node = byId(id);
    if (!node && kind !== 'note') {
      const input = byId('questionInput');
      if (input) {
        node = document.createElement('p');
        node.id = id;
        node.className = 'reader-status muted';
        node.setAttribute('role', 'status');
        input.insertAdjacentElement('afterend', node);
      }
    }
    if (node) { node.setAttribute('role', 'status'); node.textContent = message; }
  }

  function paintNote() {
    if (!byId('noteInput')) return;
    const draft = noteDraft();
    setValue('noteInput', draft.text);
    setText('noteTarget', targetLabel(draft.target));
    const message = !connected()
      ? '연결이 끊겼습니다. 작성한 초안은 남아 있습니다. 다시 연결한 뒤 저장해 주세요.'
      : draft.dirty ? '저장하지 않은 변경 · 이 페이지의 초안이 유지됩니다.'
        : state().savedNotes[targetKey(draft.target)] ? '이 시안에 저장됨 · 이 버전과 페이지에 연결되어 있습니다.'
          : '아직 저장한 메모가 없습니다.';
    liveMessage(message, 'note');
    actions('note:save').forEach(button => { button.disabled = !connected() || !draft.dirty; });
    actions('note:save').forEach(button => { button.textContent = '메모 저장'; });
    actions('note:cancel').forEach(button => { button.disabled = !draft.dirty; });
  }

  function paintAnswers() {
    const list = byId('answerList');
    if (!list) return;
    const draft = questionDraft(false);
    const key = draft.captured ? targetKey(draft.captured) : targetKey();
    const records = draft.answers.filter(answer => answer.targetKey === key);
    if (!records.length) {
      list.innerHTML = '<p class="muted reader-empty">이 문맥에서 질문하면 답변과 근거가 여기에 남습니다.</p>';
      return;
    }
    list.innerHTML = records.slice().reverse().map(answer => {
      const pending = answer.status === 'pending';
      return '<article class="card stack reader-answer">' +
        '<p class="eyebrow">' + esc(targetLabel(answer.captured)) + '</p>' +
        '<h3>질문</h3><p class="reader-preserve">' + esc(answer.question) + '</p>' +
        '<h3>' + (pending ? '답변 준비 중' : '답변 예시') + '</h3>' +
        '<p class="reader-preserve" role="status">' + esc(pending ? '선택한 문맥을 확인하고 있습니다. 시안 안에서 진행됩니다.' : answer.text) + '</p>' +
        '<p class="muted">' + esc(answer.mode === 'current' ? '질문 기준: 현재 소스' : '질문 기준: 문서 작성 당시 소스') + '</p>' +
        (answer.limitation ? '<p class="notice">' + esc(answer.limitation) + '</p>' : '') +
        (!pending ? '<button type="button" class="button secondary" data-action="dialog:citation" data-answer="' + esc(answer.id) + '">선택한 문맥과 근거 보기</button>' : '') + '</article>';
    }).join('');
  }

  function paintQuestion() {
    if (!byId('questionInput')) return;
    const draft = questionDraft(true);
    const different = targetKey(draft.captured) !== targetKey();
    setText('askTarget', targetLabel(draft.captured) + (draft.captured.scenarioStep == null ? '' : ' · 시나리오 단계 ' + draft.captured.scenarioStep));
    setValue('questionInput', draft.text || '');
    const mode = byId('askMode');
    if (mode && !mode.querySelector('option[value="snapshot"]')) {
      mode.innerHTML = '<option value="snapshot">문서 작성 당시 소스</option><option value="current">현재 소스</option>';
      if (mode.parentElement && mode.parentElement.firstChild && mode.parentElement.firstChild.nodeType === 3) mode.parentElement.firstChild.textContent = '질문 소스';
    }
    setValue('askMode', draft.mode || 'snapshot');
    const quoteNode = byId('askQuote') || byId('selectedQuote') || document.querySelector('#askTarget + blockquote');
    if (quoteNode) quoteNode.textContent = draft.quote;
    if (different) liveMessage('다른 페이지로 이동해도 작성 중인 질문의 대상은 유지됩니다. 바꾸려면 현재 페이지로 문맥을 변경해 주세요.');
    else if (!connected()) liveMessage('전송되지 않음 · 연결을 확인한 뒤 직접 다시 보내 주세요. 질문 초안은 유지됩니다.');
    else if (!providerAvailable()) liveMessage('AI를 사용할 수 없습니다. 질문 초안은 유지되며 읽기와 메모는 계속 사용할 수 있습니다.');
    else if (draft.notice) liveMessage(draft.notice);
    else liveMessage('질문과 선택한 문맥은 패널을 닫아도 유지됩니다.');
    actions('ask:send').forEach(button => { button.disabled = !connected() || !providerAvailable(); });
    actions('ask:retarget').forEach(button => { button.textContent = '현재 페이지로 변경'; });
    paintAnswers();
  }

  function paintReview() {
    const version = HR.version();
    if (!version) return;
    const reviewed = version.review === 'reviewed';
    const labels = { unreviewed: '미검토', reviewing: '검토 중', reviewed: '검토 완료' };
    setText('reviewState', '내 검토 · ' + (labels[version.review] || labels.unreviewed));
    const reviewBadge = byId('reviewState');
    if (reviewBadge) reviewBadge.dataset.state = version.review || 'unreviewed';
    actions('review').forEach(button => {
      button.textContent = reviewed ? '이 버전 다시 검토' : '이 버전 전체 검토 완료';
      button.setAttribute('aria-pressed', String(reviewed));
      button.disabled = !connected();
    });
    const marked = (state().revisit || []).includes(state().docId);
    actions('revisit').forEach(button => {
      button.setAttribute('aria-pressed', String(marked));
      button.textContent = marked ? '다시 볼 문서에 추가됨' : '다시 볼 문서';
      button.disabled = !connected();
    });
  }

  function paintLinkDecision() {
    const choice = (state().linkDecisions || {})[targetKey()];
    if (!choice) return;
    const buttons = actions('link:approve').concat(actions('link:reject'));
    buttons.forEach(button => { button.disabled = true; });
    let status = byId('linkStatus');
    if (!status && buttons[0]) {
      status = document.createElement('p');
      status.id = 'linkStatus';
      status.setAttribute('role', 'status');
      buttons[0].parentElement.appendChild(status);
    }
    if (status) status.textContent = choice === 'approved' ? '관련 문서 제안을 수락했습니다. 원본 내용과 내 검토는 그대로입니다.' : '이 제안을 거절했습니다. 원본 링크는 바뀌지 않습니다.';
  }

  function paintEvidence() {
    if (state().context !== 'evidence') return;
    const context = byId('context');
    const body = context && context.querySelector('.context-body');
    if (!body) return;
    const target = currentTarget();
    const unavailable = sourceUnavailable(target);
    let summary = byId('readerEvidenceStatus');
    if (!summary) {
      summary = document.createElement('p');
      summary.id = 'readerEvidenceStatus';
      summary.className = 'notice small';
      body.prepend(summary);
    }
    summary.textContent = targetLabel(target) + (unavailable
      ? ' · 원본 정보 없음 또는 사용 불가. 저장된 설명은 그대로 읽을 수 있습니다.'
      : ' · 선택한 페이지의 근거 예시. 내 검토 상태와는 별개입니다.');
    const firstRowPath = body.querySelector('.list-row .row-meta');
    if (firstRowPath) firstRowPath.textContent = target.path;
    const revision = body.querySelector('p.small.muted');
    if (revision) revision.textContent = unavailable ? '확인 가능한 원본 리비전이 없습니다.' : target.versionId + ' · 작성된 근거와 이후 확인 기록을 나누어 봅니다.';
    body.querySelectorAll('.section').forEach(section => { section.hidden = unavailable; });
  }

  function paintSimulation() {
    const node = byId('simulation');
    if (!node) return;
    const index = ((state().readerSimulation || {})[targetKey()] || 0) % 4;
    const messages = [
      '준비 · 현재 소유권은 실행 세대 1입니다. 아직 결과를 만들지 않았습니다.',
      '1 / 3 · 실행 세대 1이 결과를 만들었습니다. 결과는 아직 저장되지 않았습니다.',
      '2 / 3 · 새 실행 세대 2가 소유권을 이어받았습니다. 이전 결과는 여전히 실행 세대 1의 소유권을 갖고 있습니다.',
      '3 / 3 · 이전 결과가 도착했지만 현재 소유권과 달라 거절됐습니다. 저장된 결과는 바뀌지 않았습니다.'
    ];
    let status = byId('simulationResult') || byId('readerSimulationStatus') || (node.matches('output, [role="status"]') ? node : node.querySelector('[data-simulation-status]'));
    if (!status) {
      status = document.createElement('p');
      status.id = 'readerSimulationStatus';
      status.className = 'notice small';
      status.dataset.simulationStatus = 'true';
      status.setAttribute('role', 'status');
      if (node.matches('button,input')) {
        const anchor = node.parentElement && node.parentElement.tagName === 'P' ? node.parentElement : node;
        anchor.insertAdjacentElement('afterend', status);
      } else node.appendChild(status);
    }
    status.textContent = messages[index];
    const labels = ['결과 만들기', '새 실행으로 소유권 넘기기', '이전 결과 도착시키기', '처음부터 다시'];
    actions('simulate').forEach(button => { button.textContent = labels[index]; });
  }

  function redrawContext() {
    HR.renderContext();
  }

  function openContext(name, button) {
    if (!contextNames.has(name)) return false;
    if (!state().context && button) contextOpener = { id: button.id, action: button.dataset.action };
    if (name === 'ask') questionDraft(true);
    state().context = name;
    redrawContext();
    const focusId = name === 'ask' ? 'questionInput' : name === 'notes' ? 'noteInput' : null;
    if (focusId) {
      const input = byId(focusId);
      if (input) input.focus({ preventScroll: true });
    }
    return true;
  }

  function closeContext() {
    state().context = null;
    redrawContext();
    const opener = contextOpener && ((contextOpener.id && byId(contextOpener.id)) || actions(contextOpener.action)[0]);
    if (opener && opener.isConnected) opener.focus({ preventScroll: true });
    contextOpener = null;
    return true;
  }

  function openPage(pageId, versionId) {
    const doc = HR.doc();
    const version = doc && doc.versions[versionId || state().versionId];
    if (!version || !version.pages[pageId]) { HR.toast('이 버전에는 해당 페이지가 없습니다.'); return true; }
    const current = state().pageId;
    const article = byId('article');
    if (article && article.parentElement) {
      state().readerPositions = state().readerPositions || {};
      state().readerPositions[targetKey()] = article.parentElement.scrollTop;
    }
    HR.openDoc(doc.id, version.id || versionId || state().versionId, pageId);
    if (current !== pageId && state().questionDraft.captured && state().questionDraft.text) HR.toast('작성 중인 질문은 원래 페이지에 연결되어 있습니다.');
    return true;
  }

  function showConflict(draft) {
    draft.conflict = { serverText: '다른 기기의 메모 예시: 결과를 반영하기 직전에 generation을 다시 확인할 것.', localText: draft.text };
    state().readerConflictKey = targetKey(draft.target);
    HR.openDialog('다른 기기에서 메모가 바뀌었습니다',
      '<p>내 초안은 그대로 보관되어 있습니다. 현재 메모를 불러오거나, 두 내용을 확인하여 직접 병합해 주세요.</p>' +
      '<h3>현재 메모</h3><blockquote>' + esc(draft.conflict.serverText) + '</blockquote>' +
      '<label for="readerMergeText">저장할 병합 내용</label><textarea id="readerMergeText" rows="6">' + esc(draft.text) + '</textarea>',
      '<button type="button" class="button secondary" data-action="note:reload">현재 메모 불러오기</button>' +
      '<button type="button" class="button primary" data-action="note:merge">병합 내용 저장</button>');
  }

  function saveNote() {
    const draft = noteDraft();
    if (!connected()) { liveMessage('저장되지 않음 · 다시 연결한 뒤 저장해 주세요. 초안은 유지됩니다.', 'note'); return true; }
    if (/conflict/.test(state().scenario || '') && !draft.conflictResolved) { showConflict(draft); return true; }
    const key = targetKey(draft.target);
    state().savedNotes[key] = { text: draft.text, target: Object.assign({}, draft.target), savedAt: '시안에 저장됨' };
    draft.baseline = draft.text;
    draft.dirty = false;
    paintNote();
    HR.toast('이 버전과 페이지에 메모를 저장했습니다.');
    return true;
  }

  function sendQuestion() {
    const draft = questionDraft(true);
    const question = (draft.text || '').trim();
    if (!connected() || !providerAvailable()) { paintQuestion(); return true; }
    if (!question) {
      liveMessage('질문을 입력해 주세요.');
      const input = byId('questionInput');
      if (input) { input.setAttribute('aria-invalid', 'true'); input.focus(); }
      return true;
    }
    const captured = Object.assign({}, draft.captured);
    const answer = {
      id: 'reader-question-' + (++questionSequence), targetKey: targetKey(captured), captured,
      question, quote: draft.quote, mode: draft.mode, status: 'pending', text: '', limitation: ''
    };
    draft.answers.push(answer);
    draft.text = '';
    draft.needsNewCapture = true;
    draft.notice = '질문을 시안에 기록했습니다. 답변 예시를 준비합니다.';
    const input = byId('questionInput');
    if (input) { input.value = ''; input.removeAttribute('aria-invalid'); }
    paintQuestion();
    const unavailable = sourceUnavailable(captured);
    setTimeout(() => {
      answer.status = unavailable ? 'partial' : 'completed';
      answer.text = unavailable
        ? '저장된 설명은 계속 읽을 수 있습니다. 다만 요청한 소스가 준비되지 않아 현재 코드에서도 같은 조건이 성립하는지는 확인할 수 없습니다.'
        : '이 예제에서는 결과를 반영하는 시점에 실행의 generation과 현재 소유자를 다시 확인합니다. 이전 실행이 만든 결과라면 반영하지 않고, 현재 실행의 결과만 받아들입니다. 선택한 설명과 함께 이 경계를 확인해 보세요.';
      answer.limitation = unavailable ? '부분 답변 예시 · 소스 확인 불가. 문서 내용만으로 현재 코드의 동작을 검증했다고 표시하지 않습니다.' : '시안용 답변 예시입니다. 실제 AI나 소스 저장소에 요청하지 않았습니다.';
      if (draft.captured && targetKey(draft.captured) === answer.targetKey && !draft.text) draft.notice = '답변이 이 질문의 버전과 페이지에 연결되어 남았습니다.';
      if (state().route === 'reader' && state().context === 'ask') { paintQuestion(); }
    }, 450);
    return true;
  }

  function showCitation(button) {
    const draft = questionDraft(false);
    const answer = button && button.dataset.answer ? draft.answers.find(item => item.id === button.dataset.answer) : null;
    const target = answer ? answer.captured : currentTarget();
    const quote = answer ? answer.quote : selectedArticleText() || excerptFor(target);
    HR.openDialog('질문의 문맥과 근거',
      '<p class="muted">' + esc(targetLabel(target)) + '</p><h3>질문할 때 선택한 설명</h3><blockquote class="reader-preserve">' + esc(quote) + '</blockquote>' +
      '<p>이 문맥은 질문을 보낼 때 고정되었습니다. 다른 페이지로 이동해도 바뀌지 않습니다.</p>' +
      '<p class="notice">시안에서는 저장된 설명의 위치를 보여줍니다. 소스 코드의 의미까지 검증한 결과로 표시하지 않습니다.</p>',
      '<button type="button" class="button primary" data-action="citation:open" data-doc="' + esc(target.docId) + '" data-version="' + esc(target.versionId) + '" data-page="' + esc(target.pageId) + '">이 설명 위치 열기</button>');
    return true;
  }

  function showMore() {
    const target = currentTarget();
    HR.openDialog('문서 메뉴', '<p>' + esc(targetLabel(target)) + '</p>' +
      '<div class="reader-menu stack"><button type="button" class="button secondary" data-action="dialog:importVersion">새 버전 가져오기</button>' +
      '<button type="button" class="button secondary" data-action="dialog:reference">페이지 위치 복사</button>' +
      '<button type="button" class="button secondary" data-action="dialog:attachment">원본 파일 안내</button>' +
      '<button type="button" class="button secondary" data-action="lifecycle:archive:' + esc(target.docId) + '">문서 보관하기</button>' +
      '<button type="button" class="button danger" data-action="lifecycle:trash:' + esc(target.docId) + '">문서 휴지통으로 이동</button></div>');
    return true;
  }

  function showReference() {
    const target = currentTarget();
    const reference = '#doc=' + encodeURIComponent(target.docId) + '&version=' + encodeURIComponent(target.versionId) + '&page=' + encodeURIComponent(target.pageId);
    HR.openDialog('페이지 위치 복사',
      '<p>' + esc(targetLabel(target)) + '</p><label class="field" for="readerReference">이 시안의 문서 위치</label>' +
      '<input id="readerReference" type="text" readonly value="' + esc(reference) + '">' +
      '<p class="small muted">필드의 내용을 직접 복사할 수 있습니다. 문서·버전·페이지를 식별하는 시안용 표기이며, 외부 기기에서 열리는 공유 링크가 준비되었다는 뜻은 아닙니다.</p>');
    const field = byId('readerReference');
    if (field) { field.focus(); field.select(); }
    return true;
  }

  function showAttachment(button) {
    const name = button && (button.dataset.path || button.dataset.file) || 'attachments/retry-trace.txt';
    HR.openDialog('첨부 파일', '<p><strong>' + esc(name) + '</strong></p><p>미리 볼 수 없는 형식도 원래 경로와 함께 보관됩니다.</p>' +
      '<p class="notice">이 시안에는 다운로드할 실제 원본 파일이 포함되어 있지 않습니다. 실제 제품에서는 선택한 원본 파일을 내려받는 위치입니다.</p>');
    return true;
  }

  function showReattach(button) {
    const doc = HR.doc();
    const destinationVersion = state().route === 'versions' && doc.versions[state().compareNew] ? state().compareNew : state().versionId;
    const version = doc.versions[destinationVersion];
    const oldVersion = state().route === 'versions' && doc.versions[state().compareOld] ? state().compareOld : doc.versions.v2 ? 'v2' : state().versionId;
    const origin = { docId: doc.id, versionId: oldVersion, pageId: 'flow', title: doc.title, path: '이전 버전의 재시도 설명' };
    const originKey = button && button.dataset.noteKey || targetKey(origin);
    const section = button && button.closest('.section');
    const copy = section && Array.from(section.querySelectorAll('p')).map(node => node.textContent).find(text => text.includes('메모 본문:'));
    const sampleText = copy ? copy.split('메모 본문:').slice(1).join('메모 본문:').trim() : '이전 버전에서 확인한 재시도 조건을 현재 설명과 다시 비교할 것.';
    state().readerReattach = { origin, originKey, versionId: destinationVersion, pageId: state().pageId, text: savedText(state().savedNotes[originKey]) || sampleText };
    HR.openDialog('메모를 현재 버전에 다시 연결',
      '<p>이전 버전의 위치 기록을 남기고, 직접 고른 페이지에 메모를 연결합니다. 비슷한 제목만으로 자동 이동하지 않습니다.</p>' +
      '<blockquote>' + esc(state().readerReattach.text) + '</blockquote>' +
      '<label for="reattachPage">연결할 페이지</label><select id="reattachPage">' +
      Object.entries(version.pages).map(([id, page]) => '<option value="' + esc(id) + '"' + (id === state().pageId ? ' selected' : '') + '>' + esc(page.path) + '</option>').join('') + '</select>',
      '<button type="button" class="button primary" data-action="note:reattach">선택한 페이지에 연결</button>');
    return true;
  }

  HR.renderReaderContext = function () {
    const context = state().context;
    if (!contextNames.has(context)) return '';
    const wrapper = document.createElement('div');
    wrapper.innerHTML = HR.fragments[context] || '';
    Array.from(wrapper.children).filter(node => node.classList.contains('context-head')).forEach(node => node.remove());
    const body = Array.from(wrapper.children).find(node => node.classList.contains('context-body'));
    if (body) body.classList.remove('context-body');
    return wrapper.innerHTML;
  };

  HR.afterReaderRender = function () {
    if (state().route !== 'reader') return;
    const doc = HR.doc();
    const version = HR.version();
    const page = version && version.pages[state().pageId];
    if (!doc || !version || !page) return;
    const title = byId('readerTitle') || document.querySelector('#screen h1');
    if (title) title.textContent = doc.title;
    const projectLabel = document.querySelector('#screen .page-header .eyebrow');
    if (projectLabel) projectLabel.textContent = doc.project === 'podway' ? 'Podway' : doc.project === 'haeryong' ? 'Haeryong' : doc.project;
    const date = document.querySelector('#screen .page-header time');
    if (date && version.date) {
      date.textContent = '버전 생성 ' + version.date.slice(0, 10);
      date.dateTime = version.date.slice(0, 10);
      date.title = '선택한 버전의 생성 시각: ' + version.date;
    }
    const freshness = { current: '원본과 일치', changed: '원본 변경됨', unknown: '원본 정보 없음' };
    setText('freshnessState', sourceUnavailable(currentTarget()) ? '원본 정보 없음 또는 사용 불가' : state().scenario === 'partial' ? '일부 범위 확인' : freshness[doc.freshness] || '원본 확인 전');
    const freshnessBadge = byId('freshnessState');
    if (freshnessBadge) freshnessBadge.dataset.state = sourceUnavailable(currentTarget()) ? 'unknown' : state().scenario === 'partial' ? 'partial' : doc.freshness || 'unknown';
    actions('context:notes').forEach(button => { button.textContent = '메모'; });
    const moreMenu = document.querySelector('#screen .more-menu');
    if (moreMenu && !moreMenu.querySelector('[data-action="dialog:reference"]')) {
      const copy = document.createElement('button');
      copy.type = 'button';
      copy.className = 'button ghost';
      copy.dataset.action = 'dialog:reference';
      copy.textContent = '페이지 위치 복사';
      moreMenu.appendChild(copy);
    }
    setText('readerPath', page.path);
    setText('pagePath', page.path);
    const versionSelect = byId('versionSelect');
    if (versionSelect) {
      versionSelect.innerHTML = Object.keys(doc.versions).slice().reverse().map(id => '<option value="' + esc(id) + '">' + esc(id) + (id === Object.keys(doc.versions).slice(-1)[0] ? ' · 최신 버전' : '') + '</option>').join('');
      versionSelect.value = state().versionId;
      versionSelect.title = versionSelect.selectedOptions[0]?.textContent || state().versionId;
    }
    const pageSelect = byId('pageSelect');
    if (pageSelect) {
      pageSelect.innerHTML = Object.entries(version.pages).map(([id, item]) => '<option value="' + esc(id) + '">' + esc(item.title || item.path) + '</option>').join('');
      pageSelect.value = state().pageId;
      pageSelect.title = page.title || page.path;
    }
    const article = byId('article');
    if (article && article.dataset.targetKey !== targetKey()) {
      if (page.html) article.innerHTML = page.html;
      article.dataset.targetKey = targetKey();
      const position = (state().readerPositions || {})[targetKey()];
      if (position != null && article.parentElement) article.parentElement.scrollTop = position;
    }
    document.querySelectorAll('[data-action^="page:"]').forEach(button => {
      const id = button.dataset.action.slice(5);
      if (version.pages[id]) {
        button.setAttribute('aria-current', id === state().pageId ? 'page' : 'false');
        if (state().context === 'files') {
          button.setAttribute('aria-label', version.pages[id].path);
          const row = button.closest('.list-row');
          const path = row && row.querySelector('.row-title');
          const metadata = row && row.querySelector('.row-meta');
          if (path) path.textContent = version.pages[id].path;
          if (metadata) metadata.textContent = version.pages[id].title;
        }
      }
    });
    paintReview();
    paintNote();
    paintQuestion();
    paintEvidence();
    paintLinkDecision();
    paintSimulation();
  };

  HR.readerInput = function (id, value) {
    if (id === 'noteInput') {
      const draft = noteDraft();
      draft.text = value;
      draft.dirty = value !== draft.baseline;
      paintNote();
      return true;
    }
    if (id === 'questionInput') {
      const draft = questionDraft(true);
      draft.text = value;
      if (draft.needsNewCapture && value) {
        const explicitMode = draft.nextMode;
        draft.captured = null;
        draft.quote = '';
        draft.needsNewCapture = false;
        questionDraft(true);
        if (explicitMode) draft.mode = explicitMode;
        delete draft.nextMode;
        paintQuestion();
      }
      draft.notice = '질문 초안이 유지됩니다.';
      const input = byId('questionInput');
      if (input) input.removeAttribute('aria-invalid');
      return true;
    }
    if (id === 'readerMergeText') {
      const draft = state().noteDrafts[state().readerConflictKey];
      if (draft) draft.mergeText = value;
      return true;
    }
    return false;
  };

  HR.readerChange = function (id, value) {
    if (id === 'pageSelect') return openPage(value);
    if (id === 'versionSelect') {
      const doc = HR.doc();
      const version = doc && doc.versions[value];
      if (!version) return true;
      return openPage(version.pages[state().pageId] ? state().pageId : Object.keys(version.pages)[0], value);
    }
    if (id === 'askMode') {
      const draft = questionDraft(true);
      draft.mode = value === 'current' ? 'current' : 'snapshot';
      if (draft.needsNewCapture) draft.nextMode = draft.mode;
      return true;
    }
    if (id === 'reattachPage') {
      if (state().readerReattach) state().readerReattach.pageId = value;
      return true;
    }
    return false;
  };

  HR.readerAction = function (action, button) {
    if (action === 'context:close') return closeContext();
    if (action.startsWith('context:')) return openContext(action.slice(8), button);
    if (action.startsWith('page:')) return openPage(action.slice(5));
    if (action === 'review') {
      if (!connected()) { HR.toast('다시 연결한 뒤 검토 상태를 저장해 주세요.'); return true; }
      const version = HR.version();
      version.review = version.review === 'reviewed' ? 'reviewing' : 'reviewed';
      paintReview();
      HR.toast(version.review === 'reviewed' ? '이 버전 전체를 검토 완료로 표시했습니다. 소스 확인 상태와는 별개입니다.' : '이 버전을 다시 검토 중으로 표시했습니다.');
      return true;
    }
    if (action === 'revisit') {
      if (!connected()) { HR.toast('다시 연결한 뒤 저장해 주세요.'); return true; }
      state().revisit = state().revisit || [];
      const index = state().revisit.indexOf(state().docId);
      if (index === -1) state().revisit.push(state().docId); else state().revisit.splice(index, 1);
      paintReview();
      HR.toast(index === -1 ? '다시 볼 문서에 추가했습니다.' : '다시 볼 문서에서 해제했습니다.');
      return true;
    }
    if (action === 'simulate') {
      state().readerSimulation = state().readerSimulation || {};
      state().readerSimulation[targetKey()] = ((state().readerSimulation[targetKey()] || 0) + 1) % 4;
      paintSimulation();
      return true;
    }
    if (action === 'ask:retarget') {
      const draft = questionDraft(true);
      draft.captured = currentTarget();
      draft.quote = selectedArticleText() || excerptFor(draft.captured);
      draft.needsNewCapture = false;
      draft.notice = '질문 문맥을 현재 페이지로 바꿨습니다. 입력한 질문은 유지됩니다.';
      paintQuestion();
      return true;
    }
    if (action === 'ask:send') return sendQuestion();
    if (action === 'note:save') return saveNote();
    if (action === 'note:cancel') {
      const draft = noteDraft();
      draft.text = draft.baseline;
      draft.dirty = false;
      const input = byId('noteInput');
      if (input) input.value = draft.text;
      paintNote();
      HR.toast('저장된 메모로 되돌렸습니다.');
      return true;
    }
    if (action === 'note:reload' || action === 'note:merge') {
      const draft = state().noteDrafts[state().readerConflictKey];
      if (!draft || !draft.conflict) return true;
      const merged = byId('readerMergeText');
      draft.text = action === 'note:reload' ? draft.conflict.serverText : merged ? merged.value : (draft.mergeText || draft.text);
      draft.baseline = draft.text;
      draft.dirty = false;
      draft.conflictResolved = true;
      draft.conflict = null;
      state().savedNotes[state().readerConflictKey] = { text: draft.text, target: Object.assign({}, draft.target), savedAt: '시안에 저장됨' };
      HR.closeDialog();
      paintNote();
      HR.toast(action === 'note:reload' ? '현재 메모를 불러왔습니다.' : '직접 확인한 병합 내용을 저장했습니다.');
      return true;
    }
    if (action === 'note:reattach') {
      const pending = state().readerReattach;
      if (!pending) return true;
      const target = Object.assign(currentTarget(), { versionId: pending.versionId, pageId: pending.pageId });
      const page = pageFor(target);
      if (!page) { HR.toast('연결할 페이지를 다시 선택해 주세요.'); return true; }
      target.path = page.path;
      const existing = savedText(state().savedNotes[targetKey(target)]);
      const existingDraft = state().noteDrafts[targetKey(target)];
      if ((existing && existing !== pending.text) || (existingDraft && existingDraft.dirty)) {
        HR.toast('이 페이지에는 이미 메모나 초안이 있습니다. 다른 페이지를 선택해 주세요. 기존 내용은 바꾸지 않았습니다.');
        return true;
      }
      state().savedNotes[pending.originKey] = state().savedNotes[pending.originKey] || { text: pending.text, target: pending.origin };
      state().savedNotes[targetKey(target)] = { text: pending.text, target, originalTarget: pending.origin, savedAt: '시안에 다시 연결됨' };
      state().noteDrafts[targetKey(target)] = { text: pending.text, baseline: pending.text, target, dirty: false };
      state().noteReattachments = state().noteReattachments || [];
      state().noteReattachments.push({ from: pending.origin, to: target });
      state().readerReattach = null;
      HR.closeDialog();
      HR.toast('이전 버전의 기록을 남기고 선택한 페이지에 메모를 연결했습니다.');
      return true;
    }
    if (action === 'link:approve' || action === 'link:reject') {
      if (!connected()) { HR.toast('연결을 확인한 뒤 제안을 처리해 주세요.'); return true; }
      state().linkDecisions = state().linkDecisions || {};
      state().linkDecisions[targetKey()] = action === 'link:approve' ? 'approved' : 'rejected';
      paintLinkDecision();
      return true;
    }
    if (action === 'dialog:more') return showMore();
    if (action === 'dialog:reference' || action === 'link:copy') return showReference();
    if (action === 'dialog:citation') return showCitation(button);
    if (action === 'dialog:attachment') return showAttachment(button);
    if (action === 'dialog:reattach') return showReattach(button);
    if (action === 'citation:open') {
      const doc = HR.doc(button.dataset.doc);
      if (!doc || doc.lifecycle === 'purged' || doc.lifecycle === 'trashed') { HR.toast('참조한 문서를 현재 읽을 수 없습니다.'); return true; }
      HR.closeDialog();
      HR.openDoc(doc.id, button.dataset.version, button.dataset.page);
      return true;
    }
    return false;
  };
})(window.HR);
