/* R5 product design bindings. Load after the four original modules, before HR.start(). */
(function (H) {
  'use strict';
  if (!H || H.productR5Installed) return;
  H.productR5Installed = true;

  const projectName = id => ({ podway: 'Podway', haeryong: 'Haeryong' }[id] || id || '프로젝트');
  const directRowCount = node => node ? Array.from(node.children).filter(child => child.classList.contains('list-row')).length : 0;

  function paintLibrary() {
    if (H.state.route !== 'library') return;
    const rows = H.$('libraryRows');
    if (!rows) return;
    const count = directRowCount(rows);
    const counter = H.$('libraryCount');
    if (counter) {
      counter.textContent = '문서 ' + count + '건';
      counter.hidden = false;
    }
    rows.hidden = count === 0;
    const empty = H.$('libraryEmpty');
    if (empty) empty.hidden = count > 0;
  }

  function paintProjects(screen) {
    const current = H.state.projectId;
    for (const button of screen.querySelectorAll('[data-action^="project:"]')) {
      const selected = button.dataset.action.slice('project:'.length) === current;
      button.setAttribute('aria-pressed', String(selected));
      const card = button.closest('.card');
      const marker = card && card.querySelector('.project-selected');
      if (marker) marker.hidden = !selected;
    }
    const heading = H.$('projectScopeTitle');
    if (heading) heading.textContent = projectName(current) + ' 문서 · ' + directRowCount(H.$('projectRows')) + '개';
  }

  function paintSources(screen) {
    const path = H.$('sourcePath');
    if (!path) return;
    const sourceName = screen.querySelector('.source-row.is-available .source-name');
    if (sourceName) sourceName.textContent = path.textContent;
    const retained = screen.querySelector('.sources .disclosure p');
    const revision = screen.querySelector('.source-identity .source-id code:not(#sourcePath)');
    const date = screen.querySelector('.source-identity time');
    if (retained && revision && date) {
      retained.textContent = '경로 ' + path.textContent + '. 리비전 ' + revision.textContent + '. 날짜 ' + date.textContent + '.';
    }
  }

  function paintVersions() {
    if (H.state.route !== 'versions') return;
    const screen = H.$('screen');
    const documentItem = H.doc();
    const oldId = H.state.compareOld;
    const newId = H.state.compareNew;
    const previous = documentItem && documentItem.versions[oldId];
    const next = documentItem && documentItem.versions[newId];
    if (!screen || !previous || !next) return;

    const open = screen.querySelector('[data-action="version:open"]');
    if (open) open.textContent = '비교 대상 ' + newId + ' 열기';

    const body = H.$('diffBody');
    if (body) {
      let heading = H.$('versionSummaryTitle');
      if (!heading) {
        heading = document.createElement('h2');
        heading.id = 'versionSummaryTitle';
        body.parentNode.insertBefore(heading, body);
      }
      heading.textContent = '본문 비교 요약';
      let description = H.$('versionSummaryDescription');
      if (!description) {
        description = document.createElement('p');
        description.id = 'versionSummaryDescription';
        description.className = 'small muted';
        body.parentNode.insertBefore(description, body);
      }
      description.textContent = '각 버전의 첫 페이지 앞부분을 발췌했습니다. 전체 문장과 코드·표는 해당 버전을 열어 확인하세요.';
      body.setAttribute('aria-labelledby', heading.id);
      body.setAttribute('aria-describedby', description.id);

      const pageCount = new Set([...Object.keys(previous.pages), ...Object.keys(next.pages)]).size;
      const notice = body.nextElementSibling;
      const scopeCopy = notice && notice.classList.contains('notice') && notice.querySelector('p');
      if (scopeCopy) {
        scopeCopy.textContent = '비교 범위: ' + oldId + ' → ' + newId + ', ' + pageCount + '개 페이지. 경로 목록에는 변경되지 않은 페이지도 포함됩니다. 원본 저장소의 변경 여부는 이 비교에 포함되지 않습니다.';
      }
    }

    const table = screen.querySelector('.table-wrap');
    const tableSection = table && table.closest('.section');
    const tableHeading = tableSection && tableSection.querySelector('h2');
    if (tableHeading) tableHeading.textContent = '페이지별 변경 내역';

    for (const button of screen.querySelectorAll('[data-action="dialog:reattach"]')) {
      const section = button.closest('.section');
      if (!section) continue;
      // This authored sample is fixed to Podway v2 -> v3, not an inferred anchor analysis.
      section.hidden = documentItem.id !== 'podway' || oldId !== 'v2' || newId !== 'v3';
      const heading = section.querySelector('h2');
      const meta = section.querySelector('.row-meta');
      const explanation = Array.from(section.querySelectorAll('p')).find(node => node.textContent.includes('메모 본문:'));
      if (heading) heading.textContent = '메모 위치 다시 연결 · 예시';
      if (meta) meta.textContent = '고정 예시 · Podway v2의 메모를 v3에 연결';
      if (explanation) {
        explanation.textContent = '이 문구와 연결 끊김 상태는 v2에서 v3으로 메모를 다시 연결하는 흐름을 보여 주는 고정 예시입니다. 선택한 본문에서 자동으로 판정한 결과가 아닙니다. 메모 본문: 인수 시점의 체크포인트가 읽기 위치와 같은지 따로 볼 것.';
      }
      button.textContent = '예시 메모를 v3에 다시 연결';
    }
  }

  const originalLibrary = H.renderLibrary;
  H.renderLibrary = function () {
    const result = originalLibrary.apply(this, arguments);
    paintLibrary();
    return result;
  };

  const originalVersions = H.syncVersions;
  H.syncVersions = function () {
    const result = originalVersions.apply(this, arguments);
    paintVersions();
    return result;
  };

  const originalRender = H.render;
  H.render = function () {
    const result = originalRender.apply(this, arguments);
    const screen = H.$('screen');
    if (screen && H.state.route === 'projects') paintProjects(screen);
    if (screen && H.state.route === 'sources') paintSources(screen);
    return result;
  };
})(window.HR);
