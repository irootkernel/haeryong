/* R5 design adapter. Load after the four original modules, before HR.start().
 * This changes only the in-memory concept, never real settings or connections.
 * Grok authored the accompanying scoped CSS/fragment edits; ChatGPT integrates state.
 */
(function () {
  "use strict";
  const H = window.HR;
  if (!H || !H.settingsAction) throw new Error("The original settings module is required.");
  const previous = {
    render: H.afterSettingsRender, action: H.settingsAction,
    input: H.settingsInput, change: H.settingsChange, core: H.coreAction
  };
  const sectionKeys = {
    projects: ["projectSourcePodway", "projectSourceHaeryong"],
    ai: ["defaultSource"],
    maintenance: ["scheduleMode", "scheduleInterval", "scheduleTime", "scheduleTimezone", "maintenanceScope", "maintenanceDepth", "maintenanceAction"],
    retention: ["retentionDays", "autoPurge"]
  };
  const labels = {
    general: "읽기 화면", connection: "연결 상태", sessions: "연결한 장치",
    projects: "프로젝트 원본", ai: "질문 기준", maintenance: "정비",
    retention: "휴지통 보존", storage: "가져오기", backup: "백업", about: "정보"
  };
  const clone = value => JSON.parse(JSON.stringify(value));
  const $ = id => H.$(id);
  const state = () => H.state;
  function context() {
    const s = state();
    return s.settingsR5 || (s.settingsR5 = { section: null, drafts: {}, feedback: {} });
  }
  function changesFor(section, source) {
    const changed = {};
    for (const key of sectionKeys[section] || []) {
      if (JSON.stringify(source[key]) !== JSON.stringify(state().settings[key])) changed[key] = source[key];
    }
    return changed;
  }
  function rememberDraft(section) {
    if (!sectionKeys[section]) return;
    const changed = changesFor(section, state().settingsDraft || state().settings);
    if (Object.keys(changed).length) context().drafts[section] = clone(changed);
    else delete context().drafts[section];
  }
  function activateSection(section) {
    const s = state(), c = context();
    if (c.section !== section) {
      if (c.section) rememberDraft(c.section);
      c.section = section;
    }
    const changed = section ? changesFor(section, Object.assign({}, s.settings, c.drafts[section] || {})) : {};
    s.settingsDraft = Object.keys(changed).length ? Object.assign({}, s.settings, changed) : null;
    s.settingsDirty = !!s.settingsDraft;
    s.settingsFeedback = section ? c.feedback[section] || "" : "";
  }
  function setFeedback(section, message) {
    if (!section) return;
    context().feedback[section] = message || "";
    if (state().route === "settings" && state().setting === section) {
      state().settingsFeedback = message || "";
      renderFeedback(section);
    }
  }
  function renderFeedback(section) {
    const host = $("settingsBody");
    if (!host || state().route !== "settings") return;
    let node = $("settingsFeedback");
    if (!node) {
      node = document.createElement("p");
      node.id = "settingsFeedback";
      host.appendChild(node);
    }
    const message = context().feedback[section] || "";
    node.className = "hr-settings-feedback";
    node.setAttribute("role", "status");
    node.setAttribute("aria-live", "polite");
    node.textContent = message;
    node.hidden = !message;
  }
  function hide(node, hidden) {
    if (!node) return;
    node.hidden = hidden;
    if ("disabled" in node) node.disabled = hidden;
  }
  function focusId(id) {
    const node = id && $(id);
    if (node && node.focus) node.focus();
  }
  function pendingValues(section) {
    return Object.assign({}, state().settings, context().drafts[section] || {},
      state().route === "settings" && state().setting === section ? changesFor(section, state().settingsDraft || state().settings) : {});
  }
  function commitSection(section, values) {
    const s = state();
    for (const key of sectionKeys[section] || []) s.settings[key] = values[key];
    delete context().drafts[section];
    s.settingsDraft = null;
    s.settingsDirty = false;
    s.settingsPending = null;
    s.settingsPendingSection = null;
    setFeedback(section, labels[section] + "의 변경을 적용했어요.");
    H.render();
    H.toast(labels[section] + "의 변경을 시안에 적용했어요.");
  }
  function cancelSection(section) {
    delete context().drafts[section];
    state().settingsDraft = null;
    state().settingsDirty = false;
    state().settingsPending = null;
    state().settingsPendingSection = null;
    setFeedback(section, "이 항목의 적용 전 변경을 취소했어요.");
    H.render();
  }
  function decorateActions(host, section) {
    if (!sectionKeys[section]) return;
    const apply = host.querySelector('[data-action="' + (section === "retention" ? "dialog:retention" : "settings:apply") + '"]');
    if (!apply) return;
    let footer = host.querySelector(".hr-settings-actions");
    if (!footer) {
      footer = document.createElement("div");
      footer.className = "hr-settings-actions";
      apply.parentNode.insertBefore(footer, apply);
      footer.appendChild(apply);
    }
    let cancel = host.querySelector('[data-action="settings:cancel"]');
    if (!cancel) {
      cancel = document.createElement("button");
      cancel.type = "button";
      cancel.className = "button";
      cancel.dataset.action = "settings:cancel";
      footer.appendChild(cancel);
    }
    cancel.textContent = "변경 취소";
    cancel.disabled = !state().settingsDirty;
    if (section !== "retention") {
      apply.textContent = "변경 적용";
      apply.disabled = !state().settingsDirty;
    }
  }
  function scheduleFields() {
    const mode = (state().settingsDraft || state().settings).scheduleMode;
    for (const id of ["intervalValue", "runTime", "timezone"]) {
      const control = $(id);
      if (!control) continue;
      const inactive = id === "intervalValue" ? mode !== "interval" : mode === "manual";
      control.disabled = inactive;
      hide(control.closest(".field"), inactive);
    }
    const interval = $("intervalValue");
    const grid = interval && interval.closest(".grid");
    if (grid) hide(grid, mode === "manual");
  }
  function decorateSettings() {
    const s = state(), host = $("settingsBody");
    if (s.route !== "settings" || !host) return;
    host.dataset.setting = s.setting;
    const eyebrow = host.querySelector(".page-header .eyebrow");
    if (eyebrow) eyebrow.hidden = true;
    for (const button of document.querySelectorAll('[data-action^="setting:"]')) {
      const key = button.dataset.action.slice(8);
      if (labels[key]) button.textContent = labels[key];
    }
    if (s.setting === "general") {
      const size = $("fontSize"), line = $("lineHeight");
      if (size) for (const o of size.options) o.textContent = o.value === "18" ? "크게 · 18px" : "기본 · 16px";
      if (line) for (const o of line.options) o.textContent = o.value === "1.9" ? "넉넉하게 · 1.9" : "보통 · 1.7";
    }
    if (s.setting === "connection") {
      const chip = $("connectionStatus"), form = host.querySelector("form");
      if (chip && form && chip.parentElement) {
        const row = chip.parentElement;
        row.classList.add("hr-connection-status");
        form.insertBefore(row, form.firstChild);
      }
      const reconnect = host.querySelector('[data-action="go:connect"]');
      if (reconnect) { reconnect.textContent = "다시 연결"; reconnect.classList.remove("primary"); }
      const addressHelp = host.querySelector(".field .small");
      if (addressHelp) addressHelp.textContent = "현재 연결 대상의 주소예요.";
      const repeated = form && form.querySelector("p.small");
      if (repeated && repeated.textContent.indexOf("연결 상태를 보여 주는 시안") >= 0) repeated.hidden = true;
    }
    if (s.setting === "sessions") {
      for (const row of host.querySelectorAll(".list-row")) {
        const action = row.querySelector('[data-action="session:revoke"]');
        const device = action && s.devices.find(d => d.id === action.dataset.sessionId);
        if (!device || !device.current) continue;
        const title = row.querySelector(".row-title");
        if (title && !title.querySelector(".chip")) {
          const badge = document.createElement("span");
          badge.className = "chip";
          badge.textContent = "현재 장치";
          title.appendChild(document.createTextNode(" "));
          title.appendChild(badge);
        }
      }
    }
    if (s.setting === "storage") {
      const repeated = host.querySelector(".section > .empty");
      if (repeated) repeated.hidden = true;
    }
    if (s.setting === "maintenance") scheduleFields();
    decorateActions(host, s.setting);
    renderFeedback(s.setting);
  }
  function decorateConnect() {
    const s = state();
    if (s.route !== "connect") return;
    const screen = $("screen"), p = s.pair || {};
    if (!screen) return;
    const phase = p.status || "idle";
    const phaseNode = $("connectPhase");
    if (phaseNode) phaseNode.dataset.phase = phase;
    const status = $("pairStatus");
    const messages = {
      idle: "연결 코드를 요청해 시작하세요.",
      waiting: "Mac에서 같은 코드를 확인하고 승인해 주세요.",
      approved: p.remember ? "연결 승인됨 · 이 장치를 기억해요." : "연결 승인됨 · 이번 연결에서만 사용해요.",
      connected: "연결되어 있어요. 읽던 문서로 돌아갈 수 있어요.",
      rejected: "연결 요청이 거절됐어요. Mac에서 확인한 뒤 새 코드를 요청하세요.",
      expired: "비교 코드가 만료됐어요. 새 코드를 요청해 주세요.",
      disconnected: "이 장치의 연결을 해제했어요. 다시 연결하려면 새 코드를 요청하세요."
    };
    if (status) {
      status.textContent = messages[phase] || messages.idle;
      status.className = "notice" + (["rejected", "expired", "disconnected"].includes(phase) ? " warn" : "");
      status.setAttribute("role", "status");
      status.setAttribute("aria-live", "polite");
      const card = screen.querySelector("article.card");
      if (card) card.insertBefore(status, card.firstChild);
    }
    const code = $("pairCode");
    if (code) {
      code.textContent = p.code || "";
      hide(code.parentElement, !["waiting", "rejected", "expired"].includes(phase));
    }
    const request = screen.querySelector('[data-action="pair:request"]');
    const retry = screen.querySelector('[data-action="pair:retry"]');
    const reader = screen.querySelector('[data-action="go:reader"]');
    hide(request, !["idle", "disconnected", "waiting"].includes(phase));
    if (request) { request.textContent = phase === "waiting" ? "Mac의 승인 기다리는 중" : "연결 코드 요청"; request.disabled = phase === "waiting" || request.hidden; }
    hide(retry, !["rejected", "expired"].includes(phase));
    if (retry) retry.textContent = "새 코드 요청";
    hide(reader, !["approved", "connected"].includes(phase));
    if (reader) reader.textContent = "읽던 문서로 돌아가기";
    for (const action of ["pair:approve", "pair:reject", "pair:expire"]) {
      const button = screen.querySelector('[data-action="' + action + '"]');
      if (button) button.disabled = phase !== "waiting";
    }
    const remember = $("rememberDevice");
    if (remember) remember.disabled = ["waiting", "approved", "connected"].includes(phase);
    const label = screen.querySelector("article .row-title");
    if (label) label.textContent = "연결할 컴퓨터";
    const intro = screen.querySelector(".page-header .muted");
    if (intro) intro.textContent = "컴퓨터에서 비교 코드를 확인하고, 읽던 문서로 돌아오세요.";
    const card = screen.querySelector("article.card");
    if (card) for (const node of card.querySelectorAll("p")) {
      if (node.textContent.indexOf("로컬 확인 화면의 여섯 자리") >= 0) node.textContent = "Mac의 연결 요청 화면과 코드가 같은지 확인하세요. 코드는 Mac에서 승인합니다.";
    }
    for (const node of screen.querySelectorAll(".stack > p.small")) {
      if (node.textContent === "연결 상태 미리보기") node.hidden = true;
    }
  }
  H.afterSettingsRender = function () {
    const s = state(), next = s.route === "settings" ? s.setting : null;
    if (context().section !== next) activateSection(next);
    s.settingsFeedback = next ? context().feedback[next] || "" : "";
    previous.render();
    decorateSettings();
    decorateConnect();
  };
  function changed(previousHandler, id, value, target) {
    const section = state().route === "settings" ? state().setting : null;
    const handled = previousHandler(id, value, target);
    if (!handled) return handled;
    if (section) {
      rememberDraft(section);
      if (["themeChoice", "fontSize", "lineHeight"].includes(id)) setFeedback(section, "읽기 모양을 바로 반영했어요.");
      else setFeedback(section, state().settingsDirty ? "이 항목에 아직 적용하지 않은 변경이 있어요." : "현재 설정과 같아요.");
      decorateSettings();
      if (id === "themeChoice") focusId(id);
    }
    if (state().route === "connect") decorateConnect();
    return handled;
  }
  H.settingsInput = (id, value, target) => changed(previous.input, id, value, target);
  H.settingsChange = (id, value, target) => changed(previous.change, id, value, target);
  H.settingsAction = function (action, button) {
    const s = state(), section = s.setting;
    if (action.startsWith("setting:") && labels[action.slice(8)]) {
      rememberDraft(context().section);
      s.setting = action.slice(8);
      activateSection(s.setting);
      H.render();
      return true;
    }
    if (action === "settings:cancel") { cancelSection(section); return true; }
    if (action === "settings:apply" && sectionKeys[section] && section !== "retention") {
      rememberDraft(section);
      commitSection(section, pendingValues(section));
      return true;
    }
    if (action === "dialog:retention" || (action === "settings:apply" && section === "retention")) {
      rememberDraft("retention");
      s.settingsDraft = pendingValues("retention");
      s.settingsPending = null;
      s.settingsPendingSection = null;
      const result = previous.action("dialog:retention", button);
      if (s.settingsPending) s.settingsPendingSection = "retention";
      else setFeedback("retention", s.settingsFeedback || "보존 일수를 확인해 주세요.");
      const dialog = $("appDialog");
      if (dialog && s.settingsPending) dialog.dataset.settingsDialog = "retention";
      return result;
    }
    if (action === "settings:confirmPolicy" && s.settingsPendingSection === "retention" && s.settingsPending) {
      const values = clone(s.settingsPending);
      H.closeDialog();
      commitSection("retention", values);
      return true;
    }
    if (action === "pair:approve") {
      if (!s.pair || s.pair.status !== "waiting") { H.toast("먼저 새 연결 코드를 요청해 주세요."); return true; }
      s.pair.status = "approved";
      s.connectionStatus = "connected";
      if (["disconnected", "offline", "session", "session-expired", "revoked"].includes(s.scenario)) s.scenario = "normal";
      const id = "paired-preview-device", prior = s.devices.find(d => d.id === id);
      const item = { id, name: "미리보기 장치", current: prior ? !!prior.current : !s.devices.some(d => d.current), temporary: !s.pair.remember };
      if (prior) Object.assign(prior, item); else s.devices.push(item);
      H.render();
      H.toast("승인된 연결 상태를 시연했어요. 실제 장치 승인은 변경하지 않았어요.");
      return true;
    }
    if (["pair:reject", "pair:expire"].includes(action) && (!s.pair || s.pair.status !== "waiting")) {
      H.toast("코드를 요청한 뒤 결과를 시연해 주세요.");
      return true;
    }
    const currentRevoked = action === "session:confirmRevoke" && s.devices.some(d => d.id === s.pendingSession && d.current);
    const result = previous.action(action, button);
    if (!result) return result;
    if (currentRevoked) { s.pair.status = "disconnected"; s.pair.code = ""; H.render(); }
    if (action === "settings:closeDialog") s.settingsPendingSection = null;
    if (action === "connection:retry") setFeedback("connection", "");
    if (action.startsWith("backup:") || action === "dialog:restoreBackup") setFeedback("backup", "");
    decorateSettings();
    decorateConnect();
    return result;
  };
  H.coreAction = function (action, button) {
    const s = state();
    if (action === "go:reader" && s.route === "connect") {
      if (!s.pair || !["approved", "connected"].includes(s.pair.status)) { H.toast("연결 승인 결과를 먼저 확인해 주세요."); return true; }
      const target = s.pair.returnTarget;
      s.pair.returnTarget = null;
      s.pair.status = "connected";
      if (target && target.docId && H.doc(target.docId) && !["trashed", "purged"].includes(H.doc(target.docId).lifecycle)) H.openDoc(target.docId, target.versionId, target.pageId);
      else H.go("reader");
      return true;
    }
    if (action === "dialog:close" && s.settingsPendingSection) { s.settingsPending = null; s.settingsPendingSection = null; }
    return previous.core(action, button);
  };
  const policyDialog = $("appDialog");
  if (policyDialog && policyDialog.addEventListener) {
    const clearPolicyConfirmation = function () {
      const s = state();
      if (s.settingsPendingSection === "retention") {
        s.settingsPending = null;
        s.settingsPendingSection = null;
      }
      if (policyDialog.dataset.settingsDialog === "retention") delete policyDialog.dataset.settingsDialog;
    };
    policyDialog.addEventListener("cancel", function (event) {
      // Let every native cancel listener run before checking whether dismissal was prevented.
      // The original core listener continues to own focus restoration.
      Promise.resolve().then(function () {
        if (!event.defaultPrevented) clearPolicyConfirmation();
      });
    });
    policyDialog.addEventListener("close", clearPolicyConfirmation);
  }
})();
