/* R5: exception banners lead to the place where the user can act. */
(function (H) {
  'use strict';
  const destinations = {
    source: ['원본 범위 확인', 'source'],
    provider: ['질문 설정 확인', 'provider'],
    conflict: ['메모 확인', 'notes'],
    partial: ['확인 범위 보기', 'coverage'],
    stale: ['초안 검토', 'drafts']
  };
  const renderBanner = H.renderBanner;
  H.renderBanner = function () {
    const result = renderBanner.apply(this, arguments);
    const destination = destinations[H.state.scenario];
    const action = H.$('banner')?.querySelector('button');
    if (destination && action) {
      action.textContent = destination[0];
      action.dataset.action = 'recovery:' + destination[1];
    }
    return result;
  };

  const coreAction = H.coreAction;
  H.coreAction = function (action) {
    if (!action.startsWith('recovery:')) return coreAction.apply(this, arguments);
    const target = action.slice('recovery:'.length);
    if (target === 'source') {
      if (H.doc()?.project) H.state.projectId = H.doc().project;
      H.go('sources');
    } else if (target === 'provider') {
      H.state.setting = 'ai';
      H.go('settings');
    } else if (target === 'notes') {
      // Open a fresh panel so Reader records this recovery entry as its opener.
      H.state.context = null;
      H.go('reader');
      if (H.state.route === 'reader') {
        const opener = H.$('banner')?.querySelector('[data-action="recovery:notes"]');
        H.readerAction('context:notes', opener);
      }
    } else if (target === 'coverage' || target === 'drafts') {
      H.state.report = target;
      H.go('maintenance');
    } else {
      return coreAction.apply(this, arguments);
    }
    return true;
  };
})(window.HR);
