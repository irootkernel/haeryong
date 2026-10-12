/* R5 auxiliary panel polish. The original Reader owns all state and actions. */
(function (H) {
  'use strict';
  if (!H || H.contextR5Installed) return;
  H.contextR5Installed = true;

  function paintFiles(body) {
    const version = H.version();
    if (!version || !version.pages) return;
    for (const button of body.querySelectorAll('[data-action^="page:"]')) {
      const page = version.pages[button.dataset.action.slice('page:'.length)];
      const row = button.closest('.list-row');
      if (!page || !row) continue;
      const title = row.querySelector('.row-title');
      const path = row.querySelector('.row-meta');
      if (title) title.textContent = page.title || page.path;
      if (path) path.textContent = page.path;
      // The title reset removes its old marker; also clear any previous sibling marker.
      row.querySelectorAll('.file-current').forEach(marker => marker.remove());
      if (title && button.getAttribute('aria-current') === 'page') {
        const marker = document.createElement('span');
        marker.className = 'file-current';
        marker.textContent = '현재 페이지';
        title.appendChild(document.createTextNode(' '));
        title.appendChild(marker);
      }
    }
  }

  function paintEvidence(body) {
    const documentItem = H.doc();
    const version = H.version();
    const page = version && version.pages[H.state.pageId];
    if (!documentItem || !page) return;
    const prefix = [documentItem.title, H.state.versionId, page.path].filter(Boolean).join(' · ') + ' · ';
    const status = H.$('readerEvidenceStatus');
    if (status && status.textContent.startsWith(prefix)) {
      status.textContent = status.textContent.slice(prefix.length);
    }
    const revision = body.querySelector('p.small.muted');
    const versionPrefix = H.state.versionId + ' · ';
    if (revision && revision.textContent.startsWith(versionPrefix)) {
      revision.textContent = revision.textContent.slice(versionPrefix.length);
    }
  }

  const originalAfterReaderRender = H.afterReaderRender;
  H.afterReaderRender = function () {
    const result = originalAfterReaderRender.apply(this, arguments);
    if (H.state.route !== 'reader') return result;
    const context = H.$('context');
    const body = context && context.querySelector('.context-body');
    if (!body) return result;
    if (H.state.context === 'files') paintFiles(body);
    if (H.state.context === 'evidence') paintEvidence(body);
    return result;
  };
})(window.HR);
