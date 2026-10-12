/* R5 management presentation bindings. Load after the original modules, before HR.start(). */
(function (H) {
  'use strict';
  if (!H || H.managementR5Installed) return;
  H.managementR5Installed = true;

  const projectName = id => ({ podway: 'Podway', haeryong: 'Haeryong' }[id] || id || '프로젝트');
  const setText = (node, value) => {
    if (node && node.textContent !== value) node.textContent = value;
  };
  const setHidden = (node, hidden) => {
    if (node && node.hidden !== hidden) node.hidden = hidden;
  };
  const buttonFor = (root, action) => root && root.querySelector('[data-action="' + action + '"]');

  function primaryAction(root, actions, preferred) {
    if (!root) return;
    for (const action of actions) {
      for (const button of root.querySelectorAll('[data-action="' + action + '"]')) {
        button.classList.toggle('primary', action === preferred && !button.disabled && !button.hidden);
      }
    }
  }

  function paintReports() {
    if (H.state.route !== 'maintenance') return;
    const screen = H.$('screen');
    const report = H.$('reportBody');
    const scan = H.state.scan || {};
    if (!screen || !report) return;
    const heading = report.querySelector('.page-header h1');
    if (heading) heading.setAttribute('aria-level', '2');

    const tabs = report.parentElement && report.parentElement.querySelector('.tabs');
    if (tabs) {
      tabs.setAttribute('role', 'tablist');
      tabs.setAttribute('aria-label', '정비 보고서');
      for (const tab of tabs.querySelectorAll('[data-action^="report:"]')) {
        const key = tab.dataset.action.slice(7);
        const selected = key === H.state.report;
        tab.id = 'reportTab-' + key;
        tab.setAttribute('role', 'tab');
        tab.setAttribute('aria-selected', String(selected));
        tab.setAttribute('aria-controls', 'reportBody');
        tab.tabIndex = selected ? 0 : -1;
      }
      report.setAttribute('role', 'tabpanel');
      report.setAttribute('aria-labelledby', 'reportTab-' + H.state.report);
      report.tabIndex = 0;
    }

    const introduction = screen.querySelector(':scope > .page-header .muted');
    const scope = { both: 'Podway · Haeryong', harbor: 'Podway', yard: 'Haeryong' }[scan.scope] || '선택한 프로젝트';
    setText(introduction, scan.status === 'idle'
      ? '프로젝트의 설명을 점검합니다. 아래는 미리 준비한 샘플 보고서입니다.'
      : scope + '의 선택한 페이지를 점검하는 샘플 검사입니다.');

    if (H.state.report === 'coverage') {
      const units = scan.units || [];
      setText(report.querySelector('.page-header .muted'),
        '선택한 전체 ' + units.length + '개 페이지를 기준으로 구조 확인과 의미 평가를 구분합니다.');
      const rows = report.querySelectorAll('tbody tr');
      units.forEach((unit, index) => {
        const cell = rows[index] && rows[index].querySelector('td');
        if (!cell) return;
        const documentItem = H.doc(unit.docId);
        const version = documentItem && documentItem.versions[unit.versionId];
        const page = version && version.pages[unit.pageId];
        const pageTitle = page && page.title || unit.path || unit.title;
        const documentContext = pageTitle === unit.title ? unit.versionId : unit.title + ' · ' + unit.versionId;
        const markup = '<span class="row-title">' + H.escape(pageTitle) + '</span>' +
          '<span class="small muted">' + H.escape(unit.path) + '</span>' +
          '<span class="small muted">' + H.escape(documentContext) + '</span>';
        if (cell.innerHTML !== markup) cell.innerHTML = markup;
      });
    }
    if (H.state.report === 'freshness') {
      setText(report.querySelector('.page-header .muted'),
        '변경된 원본과 원본 정보가 없는 설명을 함께 확인합니다.');
      const rows = report.querySelectorAll('.list-row');
      if (rows[0]) setText(rows[0].querySelector('.chip'), '원본 변경됨');
      if (rows[1]) setText(rows[1].querySelector('.chip'), '원본 정보 없음');
    }
  }

  function paintLifecycle() {
    for (const id of ['archiveRows', 'trashRows']) {
      const host = H.$(id);
      if (!host) continue;
      for (const row of host.querySelectorAll('.list-row')) {
        const actionButton = row.querySelector('[data-action^="lifecycle:restore:"], [data-action^="lifecycle:unarchive:"]');
        if (!actionButton) continue;
        const documentItem = H.doc(actionButton.dataset.action.split(':').pop());
        if (!documentItem) continue;
        const destination = id === 'archiveRows' || documentItem.priorLifecycle !== 'archived' ? '서재' : '보관함';
        const restoreText = destination === '보관함' ? '보관함으로 복원' : '서재로 복원';
        setText(actionButton, restoreText);
        setText(row.querySelector('.row-meta'), documentItem.path + ' · ' + (id === 'archiveRows' ? '보관 중' : restoreText));
      }
    }
  }

  function paintFinding(body) {
    const finding = H.state.finding || {};
    const approve = buttonFor(body, 'finding:approve');
    const stale = !!finding.stale || H.state.scenario === 'stale' && !finding.reassessed;
    const create = buttonFor(body, 'draft:create');
    if (create) create.disabled = stale;
    const preferred = stale || finding.decision === 'rejected' ? 'finding:reassess'
      : approve && !approve.disabled ? 'finding:approve'
      : finding.decision === 'approved' ? 'draft:create' : 'finding:reassess';
    primaryAction(body, ['finding:approve', 'finding:reject', 'finding:reassess', 'finding:undo', 'draft:create'], preferred);
  }

  function paintDraft(body) {
    const draft = H.state.draft || {};
    const status = H.$('draftState');
    if (status && draft.title) {
      for (const node of Array.from(status.childNodes)) {
        if (node.nodeType !== 3) continue;
        const duplicate = ' · ' + draft.title + ' · ';
        if (node.textContent.includes(duplicate)) node.textContent = node.textContent.replace(duplicate, ' · ');
      }
    }
    const content = H.$('draftContent');
    const first = content && content.firstElementChild;
    if (first && /^H[1-6]$/.test(first.tagName) && first.textContent.trim() === draft.title) setHidden(first, true);
    const publish = buttonFor(body, 'draft:publish');
    const preferred = publish && !publish.disabled ? 'draft:publish'
      : buttonFor(body, 'draft:rebase') ? 'draft:rebase'
      : draft.status === 'published' ? 'draft:open' : null;
    primaryAction(body, ['draft:replace', 'draft:reject', 'draft:publish', 'draft:rebase', 'draft:open'], preferred);
  }

  function paintScan(dialog, body) {
    const scan = H.state.scan || {};
    const semantic = scan.depth === 'semantic';
    const running = scan.status === 'running';
    const budget = H.$('scanBudget');
    const field = H.$('scanBudgetField') || budget && budget.closest('.field');
    const note = body.querySelector('p.small');
    dialog.dataset.scanDepth = semantic ? 'semantic' : 'structural';
    setHidden(field, !semantic);
    setHidden(budget, !semantic);
    if (budget) budget.disabled = !semantic || running;
    for (const id of ['scanScope', 'scanDepth']) {
      const select = H.$(id);
      if (select) select.disabled = running;
    }
    if (note) {
      note.id = 'scanBudgetHelp';
      setText(note, semantic
        ? 'AI 호출 예산은 이번 의미 검토의 호출 시도 횟수입니다. 문서나 페이지 수의 제한이 아닙니다.'
        : '구조 검사는 페이지와 연결 목록을 확인하며 AI를 호출하지 않습니다.');
      if (budget) budget.setAttribute('aria-describedby', note.id);
      const repeated = field && field.querySelector('span.small');
      setHidden(repeated, true);
    }
    primaryAction(body, ['scan:start'], 'scan:start');
  }

  function paintImport(dialog, body) {
    const upload = H.state.upload || {};
    const captured = upload.snapshot || upload;
    const versionImport = captured.kind === 'version' || captured.kind === 'newversion';
    const wrapper = H.$('importBaseWrap');
    const base = H.$('importBase');
    dialog.dataset.importKind = versionImport ? 'newversion' : 'newdoc';
    dialog.dataset.uploadState = upload.status || 'idle';
    setHidden(wrapper, !versionImport);
    setHidden(base, !versionImport);
    if (base) base.disabled = !versionImport || !!upload.snapshot || ['completed', 'abandoned'].includes(upload.status);
    if (wrapper) {
      setText(wrapper.querySelector('label'), '기준 버전');
      setText(wrapper.querySelector('p.small'), '가져오는 동안 이 기준 버전을 유지합니다.');
    }
    const preferred = {
      idle: 'upload:start', failed: 'upload:start', running: 'upload:pause',
      paused: 'upload:resume', completed: 'upload:open', conflict: 'upload:rebase'
    }[upload.status || 'idle'];
    primaryAction(body, ['upload:start', 'upload:pause', 'upload:resume', 'upload:open', 'upload:rebase'], preferred);
  }

  function paintCreate(body) {
    const settings = H.state.settings || {};
    const key = H.state.projectId === 'podway' ? 'projectSourcePodway' : 'projectSourceHaeryong';
    const projectMode = settings[key];
    const sourceMode = projectMode && projectMode !== 'inherit' ? projectMode : settings.defaultSource || 'snapshot';
    const sourceLabel = {
      snapshot: '저장된 설명 기준', current: '연결된 원본 함께 사용',
      'mapped-only': '매핑된 프로젝트만', 'active-doc': '열린 문서 기준', 'ask-each-time': '요청할 때 범위 선택'
    }[sourceMode] || '저장된 설명 기준';
    setText(H.$('createScopeSummary'), projectName(H.state.projectId) + ' · ' + sourceLabel +
      (settings.providerChoice === 'sample' ? ' · 샘플 제공자' : ''));
    const create = H.state.create || {};
    primaryAction(body, ['create:start', 'create:open'], create.status === 'completed' ? 'create:open' : 'create:start');
  }

  function paintDialog() {
    const dialog = H.$('appDialog');
    const body = H.$('dialogBody');
    if (!dialog || !body) return;
    if (!dialog.open) {
      delete dialog.dataset.scanDepth;
      delete dialog.dataset.importKind;
      delete dialog.dataset.uploadState;
      return;
    }
    if (!body.querySelector('#scanScope')) delete dialog.dataset.scanDepth;
    if (!body.querySelector('#importKind')) {
      delete dialog.dataset.importKind;
      delete dialog.dataset.uploadState;
    }
    if (body.querySelector('#findingDecision')) paintFinding(body);
    if (body.querySelector('#draftState')) paintDraft(body);
    if (body.querySelector('#scanScope')) paintScan(dialog, body);
    if (body.querySelector('#importKind')) paintImport(dialog, body);
    if (body.querySelector('#createTitle')) paintCreate(body);
  }

  function paintHomeImport() {
    if (H.state.route !== 'home') return;
    const screen = H.$('screen');
    if (!screen) return;
    const upload = H.state.upload || {};
    const retained = !['idle', 'complete', 'completed', 'abandoned'].includes(upload.status || 'idle');
    const notices = Array.from(screen.children).filter(node =>
      node.id === 'homeImportR5' || node.classList.contains('notice') && node.querySelector('[data-action="dialog:import"]'));
    if (!retained) {
      notices.forEach(node => node.remove());
      return;
    }
    let notice = notices.shift();
    notices.forEach(node => node.remove());
    if (!notice) {
      notice = document.createElement('div');
      notice.className = 'notice';
      notice.innerHTML = '<strong></strong><p class="row-meta"></p><button class="button" type="button" data-action="dialog:import">진행 상황 보기</button>';
      screen.prepend(notice);
    }
    notice.id = 'homeImportR5';
    const labels = { running: '가져오는 중', paused: '일시정지', conflict: '기준 버전 확인 필요', failed: '다시 시도 필요' };
    const captured = upload.snapshot || upload;
    setText(notice.querySelector('strong'), upload.status === 'paused' ? '이어서 가져올 문서가 있습니다' : '진행 중인 가져오기');
    setText(notice.querySelector('p'), (captured.title || upload.title || '가져오기') + ' · ' +
      (labels[upload.status] || upload.status) + ' · ' + (upload.progress || 0) + '%');
  }

  function paint() {
    paintReports();
    paintLifecycle();
    paintDialog();
    paintHomeImport();
  }

  // The original management module calls syncUploadSummary at the end of each job paint.
  // Queue one microtask so its remaining disabled-state writes finish first. No polling is used.
  let queued = false;
  function queuePaint() {
    if (queued) return;
    queued = true;
    Promise.resolve().then(function () {
      queued = false;
      paint();
    });
  }
  const originalSummary = H.syncUploadSummary;
  if (originalSummary) H.syncUploadSummary = function () {
    const result = originalSummary.apply(this, arguments);
    queuePaint();
    return result;
  };

  const originalAfter = H.afterManagementRender;
  H.afterManagementRender = function () {
    const result = originalAfter && originalAfter.apply(this, arguments);
    paint();
    return result;
  };
  const originalAction = H.managementAction;
  H.managementAction = function (action) {
    const focused = document.activeElement;
    const restoreTabFocus = action && action.startsWith('report:') && focused && focused.dataset.action === action;
    const result = originalAction.apply(this, arguments);
    if (result) {
      paint();
      if (restoreTabFocus) {
        const tab = H.$('reportTab-' + action.slice(7));
        if (tab) tab.focus({ preventScroll: true });
      }
    }
    return result;
  };
  for (const hook of ['managementInput', 'managementChange']) {
    const original = H[hook];
    H[hook] = function () {
      const result = original.apply(this, arguments);
      if (result) paintDialog();
      return result;
    };
  }
  const originalLifecycle = H.renderLifecycle;
  H.renderLifecycle = function () {
    const result = originalLifecycle.apply(this, arguments);
    paintLifecycle();
    return result;
  };
  const originalHome = H.syncHome;
  H.syncHome = function () {
    const result = originalHome.apply(this, arguments);
    paintHomeImport();
    return result;
  };

  const dialog = H.$('appDialog');
  if (dialog) dialog.addEventListener('close', function () {
    paintDialog();
    paintHomeImport();
  });
  document.addEventListener('keydown', function (event) {
    if (H.state.route !== 'maintenance' || !['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
    const target = event.target && event.target.closest && event.target.closest('[role="tab"][data-action^="report:"]');
    if (!target) return;
    const tabs = Array.from(target.parentElement.querySelectorAll('[role="tab"]'));
    const index = tabs.indexOf(target);
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1
      : (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
    event.preventDefault();
    tabs[next].focus();
    tabs[next].click();
  });
})(window.HR);

