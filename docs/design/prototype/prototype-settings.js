/* Haeryong design preview: local Settings and Connect interactions. */
(function () {
  "use strict";
  const H = window.HR;
  if (!H) throw new Error("Haeryong shared state is required.");

  const ids = {
    endpoint: "endpoint", defaultSource: "defaultSource", scheduleMode: "scheduleMode",
    retentionDays: "retentionDays", autoPurge: "autoPurge", fontSize: "fontSize",
    lineHeight: "lineHeight", providerChoice: "providerChoice", modelChoice: "modelChoice",
    rememberSource: "rememberSource", rememberProvider: "rememberProvider",
    startPage: "startPage", reduceMotion: "reduceMotion", projectScope: "projectScope",
    maintenanceScope: "maintenanceScope", backupSchedule: "backupSchedule",
    disclosureWork: "projectSourcePodway", disclosureRef: "projectSourceHaeryong",
    intervalValue: "scheduleInterval", runTime: "scheduleTime", timezone: "scheduleTimezone",
    scope: "maintenanceScope", depth: "maintenanceDepth", actionPolicy: "maintenanceAction"
  };
  const defaults = {
    fontSize: "16", lineHeight: "1.7", retentionDays: "30", autoPurge: false,
    endpoint: "http://127.0.0.1:47321", defaultSource: "snapshot", scheduleMode: "manual",
    providerChoice: "sample", modelChoice: "sample", rememberSource: true,
    rememberProvider: true, startPage: "continue", reduceMotion: false,
    projectScope: "selected", maintenanceScope: "all", backupSchedule: "manual",
    projectSourcePodway: "inherit", projectSourceHaeryong: "inherit",
    scheduleInterval: "7일", scheduleTime: "04:30", scheduleTimezone: "Asia/Seoul",
    maintenanceDepth: "surface", maintenanceAction: "report"
  };
  const $ = id => H.$(id);
  const esc = text => H.escape(String(text == null ? "" : text));
  const copy = value => JSON.parse(JSON.stringify(value));

  function ensure() {
    const s = H.state;
    s.settings = Object.assign({}, defaults, s.settings || {});
    s.pair = Object.assign({ status: "idle", remember: true, code: "482 916", sequence: 0 }, s.pair || {});
    s.backup = Object.assign({ completed: "2026-10-11 18:20", attempt: "성공", trial: "미실행" }, s.backup || {});
    s.devices = s.devices || [];
    return s;
  }

  function working() {
    const s = ensure();
    return s.settingsDraft || s.settings;
  }

  function setDraft(key, value) {
    const s = ensure();
    if (!s.settingsDraft) s.settingsDraft = copy(s.settings);
    s.settingsDraft[key] = value;
    s.settingsDirty = JSON.stringify(s.settingsDraft) !== JSON.stringify(s.settings);
    feedback(s.settingsDirty ? "아직 적용하지 않은 변경사항이 있어요." : "모든 변경사항을 적용했어요.");
  }

  function text(id, value) {
    const node = $(id);
    if (node) node.textContent = String(value);
  }

  function feedback(message) {
    H.state.settingsFeedback = message;
    let node = $("settingsFeedback");
    const host = $("settingsBody");
    if (!node && host && H.state.route === "settings") {
      node = document.createElement("p");
      node.id = "settingsFeedback";
      node.className = "muted settings-feedback";
      node.setAttribute("role", "status");
      node.setAttribute("aria-live", "polite");
      host.appendChild(node);
    }
    if (node) node.textContent = message;
  }

  function setControl(id, value) {
    const node = $(id);
    if (!node) return;
    if (node.type === "checkbox") node.checked = !!value;
    else if (document.activeElement !== node) node.value = String(value);
  }

  function applyAppearance() {
    const s = ensure();
    const root = document.documentElement;
    root.style.setProperty("--reader-font-size", s.settings.fontSize + "px");
    root.style.setProperty("--reader-line-height", s.settings.lineHeight);
    root.dataset.theme = s.theme || "light";
    root.dataset.reducedMotion = s.settings.reduceMotion ? "true" : "false";
  }

  function policySummary() {
    const s = ensure();
    const p = working();
    const duration = p.retentionDays === "forever"
      ? "계속 보관" : p.retentionDays + "일 보관";
    const pending = s.settingsDirty && (p.retentionDays !== s.settings.retentionDays || p.autoPurge !== s.settings.autoPurge);
    text("policySummary", duration + " · 자동 영구 삭제 " + (p.autoPurge ? "켜짐" : "꺼짐") + (pending ? " · 적용 전" : ""));
  }

  function retentionDialog() {
    const s = ensure();
    const proposed = copy(working());
    s.settingsPending = proposed;
    const count = (H.docs || []).filter(doc => doc.lifecycle === "trashed").length;
    const duration = value => value === "forever" ? "계속 보관" : esc(value) + "일";
    H.openDialog("보존 정책의 영향 확인",
      '<p>보관 기간과 자동 영구 삭제는 각각 적용돼요.</p>' +
      '<dl class="settings-impact"><dt>보관 기간</dt><dd>' + duration(s.settings.retentionDays) + " → " + duration(proposed.retentionDays) + '</dd>' +
      '<dt>자동 영구 삭제</dt><dd>' + (s.settings.autoPurge ? "켜짐" : "꺼짐") + " → " + (proposed.autoPurge ? "켜짐" : "꺼짐") + '</dd></dl>' +
      '<p>현재 휴지통의 시안 문서 ' + count + '개에 적용할 정책이에요. 설정을 적용하는 지금 삭제되는 문서는 <strong>0개</strong>예요.</p>' +
      '<p class="muted">' + (proposed.autoPurge ? "자동 삭제가 켜져 있으면 보존 기간이 지난 휴지통 항목이 대상이 돼요. " : "보존 기간이 지나도 자동 삭제는 실행하지 않아요. ") +
      '이 시안에서는 정책 상태만 바뀌며 실제 파일과 백업은 변경하지 않아요.</p>',
      '<button type="button" class="button" data-action="settings:closeDialog" autofocus>취소</button>' +
      '<button type="button" class="button primary" data-action="settings:confirmPolicy">정책 적용</button>');
  }

  function commitSettings(proposed) {
    const s = ensure();
    s.settings = copy(proposed);
    s.settingsDraft = null;
    s.settingsPending = null;
    s.settingsDirty = false;
    applyAppearance();
    feedback("설정을 시안에 적용했어요.");
    H.render();
    H.toast("설정을 시안에 적용했어요.");
  }

  function validateSettings(value) {
    if (value.retentionDays !== "forever" &&
      (!/^\d+$/.test(String(value.retentionDays)) || !Number.isSafeInteger(Number(value.retentionDays)) || Number(value.retentionDays) < 1)) {
      feedback("보존 일수는 1일 이상의 정수로 입력해 주세요.");
      const node = $("retentionDays");
      if (node) { node.setAttribute("aria-invalid", "true"); node.focus(); }
      return false;
    }
    if (value.endpoint && !/^https?:\/\/[^\s]+$/i.test(value.endpoint)) {
      feedback("연결 주소를 http:// 또는 https://로 시작하는 주소로 입력해 주세요.");
      const node = $("endpoint");
      if (node) { node.setAttribute("aria-invalid", "true"); node.focus(); }
      return false;
    }
    return true;
  }

  function connectionRetry() {
    const s = ensure();
    if (!validateSettings(working())) return;
    s.connectionAttempt = (s.connectionAttempt || 0) + 1;
    const attempt = s.connectionAttempt;
    s.connectionStatus = "checking";
    feedback("연결 상태를 확인하는 시안이에요…");
    text("connectionStatus", "연결 확인 중 · 시안");
    if ($("connectionStatus")) $("connectionStatus").className = "chip";
    setTimeout(function () {
      if (H.state.connectionAttempt !== attempt) return;
      H.state.connectionStatus = "connected";
      if (["disconnected", "offline"].includes(H.state.scenario)) H.state.scenario = "normal";
      feedback("시안의 연결됨 상태로 전환했어요.");
      if (H.state.route === "settings") H.render();
      H.toast("연결됨 · 시안 상태");
    }, 650);
  }

  function deviceFrom(button, action) {
    const data = button && button.dataset || {};
    const requested = action.split(":").slice(2).join(":") || data.sessionId || data.session || data.deviceId || data.device || data.id;
    return requested ? H.state.devices.find(device => device.id === requested) : H.state.devices.find(device => !device.current);
  }

  function revokeDialog(button, action) {
    ensure();
    const device = deviceFrom(button, action);
    if (!device) { H.toast("해제할 다른 장치가 없어요."); return; }
    H.state.pendingSession = device.id;
    H.openDialog("장치 연결 해제",
      '<p><strong>' + esc(device.name) + '</strong>의 시안 연결을 해제할까요?</p>' +
      '<p>' + (device.current ? "이 장치가 연결 화면으로 돌아가요. " : "이 장치는 다시 연결할 때 승인이 필요해요. ") +
      '문서와 메모 초안은 유지돼요.</p><p class="muted">실제 장치의 접속 권한은 변경되지 않아요.</p>',
      '<button type="button" class="button" data-action="settings:closeDialog" autofocus>취소</button>' +
      '<button type="button" class="button" data-action="session:confirmRevoke">시안 연결 해제</button>');
  }

  function renderSessions() {
    const host = $("sessionList") || $("sessionsList") || (H.state.route === "settings" && H.state.setting === "sessions" && document.querySelector("#settingsBody .list"));
    if (!host) return;
    host.id = "sessionList";
    host.innerHTML = H.state.devices.map(device =>
      '<li class="list-row"><div><p class="row-title">' + esc(device.name) + '</p><p class="row-meta">' +
      (device.current ? "현재 장치 · " : "") + (device.temporary ? "이번 연결에서만 사용" : "기억한 장치") +
      ' · 시안 세션</p></div><div class="row-end"><button type="button" class="button" data-action="session:revoke" data-session-id="' + esc(device.id) + '">연결 해제</button></div></li>'
    ).join("") || '<li class="empty">연결된 시안 장치가 없어요.</li>';
  }

  function backupDetails() {
    const b = ensure().backup;
    const last = b.completed || "성공한 백업 없음";
    const host = H.state.route === "settings" && H.state.setting === "backup" && document.querySelector("#settingsBody .list");
    if (host) {
      host.innerHTML = '<li class="list-row"><div><p class="row-title">마지막 성공</p><p class="row-meta" id="backupCompleted">' + esc(last) + '</p></div><span class="chip">시안 백업</span></li>' +
        '<li class="list-row"><div><p class="row-title">최근 시도</p><p class="row-meta" id="backupAttempt">' + esc(b.attempt) + '</p></div></li>' +
        '<li class="list-row"><div><p class="row-title">복원 점검</p><p class="row-meta" id="backupTrial">' + esc(b.trial) + '</p></div></li>';
    }
    text("backupCompleted", last);
    text("backupAttempt", b.attempt);
    text("backupTrial", b.trial);
    text("backupStatus", "마지막 성공: " + last + " · 최근 시도: " + b.attempt + " · 복원 점검: " + b.trial);
    if (H.state.route === "settings" && H.state.setting === "backup") {
      feedback("마지막 성공 " + last + " · 최근 시도 " + b.attempt + " · 복원 점검 " + b.trial);
    }
  }

  function createBackup() {
    const s = ensure();
    if (s.backup.running) { H.toast("시안 백업을 준비하고 있어요."); return; }
    const job = (s.backup.job || 0) + 1;
    s.backup.job = job;
    s.backup.running = true;
    s.backup.attempt = "진행 중 · 시안";
    const snapshot = { documents: (H.docs || []).filter(doc => doc.lifecycle !== "purged").length, notes: Object.keys(s.savedNotes || {}).length };
    backupDetails();
    setTimeout(function () {
      if (H.state.backup.job !== job) return;
      const b = H.state.backup;
      b.running = false;
      b.completed = "2026-10-12 10:30 · 시안";
      b.attempt = "성공 · 시안";
      b.snapshot = snapshot;
      backupDetails();
      H.toast("시안 백업 완료: 문서 " + snapshot.documents + "개, 메모 " + snapshot.notes + "개");
    }, 800);
  }

  function restoreDialog() {
    const b = ensure().backup;
    if (b.running) { H.toast("시안 백업이 끝난 뒤 복원을 점검해 주세요."); return; }
    H.openDialog("백업 복원 점검",
      '<p><strong>' + esc(b.completed || "샘플 백업") + '</strong>의 복원 가능 상태를 살펴봐요.</p>' +
      '<p>현재 작업은 유지하고, 예시 백업의 문서·버전·메모 항목을 점검한 결과만 표시해요.</p>' +
      '<p class="muted">실제 백업 파일을 읽거나 기존 데이터를 덮어쓰지 않는 시안이에요.</p>',
      '<button type="button" class="button" data-action="settings:closeDialog" autofocus>취소</button>' +
      '<button type="button" class="button primary" data-action="backup:trial">시안 복원 점검</button>');
  }

  function trialBackup() {
    const b = ensure().backup;
    H.closeDialog();
    b.trialJob = (b.trialJob || 0) + 1;
    const job = b.trialJob;
    b.trial = "점검 중 · 시안";
    backupDetails();
    setTimeout(function () {
      if (H.state.backup.trialJob !== job) return;
      H.state.backup.trial = "완료 · 문서·버전·메모 예시 확인";
      backupDetails();
      H.toast("시안 복원 점검을 완료했어요. 현재 문서는 그대로예요.");
    }, 650);
  }

  function capturePairTarget() {
    const s = ensure();
    if (!s.pair.returnTarget) s.pair.returnTarget = copy(H.target());
  }

  function pairStatus() {
    const p = ensure().pair;
    const status = {
      idle: "연결 요청을 시작해 주세요.", waiting: "Mac에서 비교 코드를 확인하고 승인을 기다려 주세요.",
      approved: p.remember ? "연결 승인됨 · 이 장치를 기억해요." : "연결 승인됨 · 이번 연결에서만 사용해요.",
      connected: p.remember ? "연결됨 · 기억한 장치" : "연결됨 · 임시 연결",
      rejected: "연결이 거절됐어요. Mac에서 확인한 뒤 다시 요청해 주세요.",
      expired: "비교 코드가 만료됐어요. 새 코드로 다시 요청해 주세요."
    };
    text("pairCode", p.code || "482 916");
    text("pairStatus", status[p.status] || status.idle);
    setControl("rememberDevice", p.remember);
    const node = $("pairStatus");
    if (node) {
      node.setAttribute("role", "status");
      node.setAttribute("aria-live", "polite");
      node.className = "notice" + (["rejected", "expired"].includes(p.status) ? " warn" : "");
    }
  }

  function pairRequest() {
    const s = ensure();
    capturePairTarget();
    s.pair.sequence += 1;
    s.pair.code = String(482 + (s.pair.sequence % 100)) + " " + String(916 - (s.pair.sequence % 100));
    s.pair.status = "waiting";
    pairStatus();
    H.toast("새 비교 코드로 연결을 요청한 시안이에요.");
  }

  function pairApprove() {
    const s = ensure();
    capturePairTarget();
    if (["expired", "rejected"].includes(s.pair.status)) { H.toast("새 비교 코드로 먼저 다시 요청해 주세요."); return; }
    s.pair.status = "approved";
    s.connectionStatus = "connected";
    if (["disconnected", "offline", "session", "session-expired", "revoked"].includes(s.scenario)) s.scenario = "normal";
    const id = "paired-preview-device";
    const prior = s.devices.find(device => device.id === id);
    const item = { id: id, name: "미리보기 장치", current: prior ? !!prior.current : !s.devices.some(device => device.current), temporary: !s.pair.remember };
    if (prior) Object.assign(prior, item);
    else s.devices.push(item);
    const target = copy(s.pair.returnTarget);
    s.pair.returnTarget = null;
    pairStatus();
    H.toast(s.pair.remember ? "시안 연결을 승인하고 장치를 기억했어요." : "이번 연결만 승인했어요. 시안에서만 유지돼요.");
    if (target && target.docId && H.doc(target.docId) && !["trashed", "purged"].includes(H.doc(target.docId).lifecycle)) {
      H.openDoc(target.docId, target.versionId, target.pageId);
    } else H.go("reader");
  }

  function syncUploadSummary() {
    const host = $("uploadSummary");
    if (!host) return;
    const u = ensure().upload || {};
    const status = u.status || "idle";
    const labels = { idle: "준비 전", running: "가져오는 중", uploading: "가져오는 중", paused: "일시 정지", interrupted: "연결 중단", ready: "준비 완료", conflict: "기준 버전 확인 필요", failed: "가져오기 중단", complete: "완료", completed: "완료", abandoned: "가져오기 취소됨" };
    const captured = u.snapshot || u;
    const target = captured.docId && H.doc(captured.docId);
    const versionImport = captured.kind === "version" || captured.kind === "newversion";
    const targetLabel = versionImport ? (target ? target.title : "선택 문서") + " · 새 버전 · 기준 " + captured.base : "새 문서";
    const button = (action, label, primary) => '<button type="button" class="button' + (primary ? ' primary' : '') + '" data-action="' + action + '">' + label + '</button>';
    let actions = "";
    if (status === "idle" || status === "abandoned") {
      host.innerHTML = '<p class="row-title">진행 중인 가져오기가 없어요</p><p id="uploadSummaryText" class="row-meta">선택한 문서와 새 버전의 진행 상황이 여기에 표시돼요.</p><div class="row">' + button("dialog:import", "가져오기", true) + '</div>';
      return;
    }
    if (status === "running" || status === "uploading") actions += button("upload:pause", "일시 정지");
    if (status === "paused" || status === "interrupted") actions += button("upload:resume", "이어서 가져오기", true);
    if (status === "conflict") actions += button("upload:rebase", "기준 버전 확인", true);
    if (status === "failed") actions += button("upload:start", "다시 시도", true);
    if (status === "completed" || status === "complete") actions += button("upload:open", "가져온 결과 열기", true);
    else actions += button("upload:abandon", "가져오기 취소");
    host.innerHTML = '<p class="row-title">' + esc(captured.title || u.title || "가져오기") + '</p>' +
      '<p id="uploadSummaryText" class="row-meta">' + esc(labels[status] || status) + ' · ' + esc(targetLabel) + ' · ' + esc(u.progress || 0) + '%</p>' +
      (u.error ? '<p class="notice">' + esc(u.error) + '</p>' : '') +
      '<progress max="100" value="' + Math.max(0, Math.min(100, Number(u.progress) || 0)) + '" aria-label="가져오기 시안 진행률"></progress>' +
      '<div class="row">' + actions + '</div><p class="small muted">이 진행 상황은 시안의 샘플 상태예요.</p>';
  }

  H.syncUploadSummary = syncUploadSummary;

  function syncSchedule() {
    const p = working();
    const interval = $("intervalValue");
    const clock = $("runTime");
    const timezone = $("timezone");
    if (interval) interval.disabled = p.scheduleMode !== "interval";
    if (clock) clock.disabled = p.scheduleMode === "manual";
    if (timezone) timezone.disabled = p.scheduleMode === "manual";
    let summary = $("scheduleSummary");
    const host = H.state.route === "settings" && H.state.setting === "maintenance" && document.querySelector("#settingsBody form");
    if (!summary && host) {
      summary = document.createElement("p");
      summary.id = "scheduleSummary";
      summary.className = "notice";
      summary.setAttribute("role", "status");
      host.appendChild(summary);
    }
    if (summary) summary.textContent = p.scheduleMode === "manual" ? "수동 점검 · 자동 실행 일정이 없어요." :
      (p.scheduleMode === "interval" ? p.scheduleInterval + " 간격" : "매일") + " · " + p.scheduleTime + " · " + p.scheduleTimezone + " · 일정 선호를 보여 주는 시안이에요.";
  }

  function normalizeSettingsCopy() {
    const s = ensure();
    const host = $("settingsBody");
    const sourceOptions = '<option value="snapshot">저장된 설명 기준</option><option value="current">연결된 원본 함께 사용</option>';
    if (host && s.route === "settings") {
      const maintenanceTab = document.querySelector('[data-action="setting:maintenance"]');
      if (maintenanceTab) maintenanceTab.textContent = "정비";
      const set = (selector, value) => { const node = host.querySelector(selector); if (node) node.textContent = value; };
      const each = (selector, values) => host.querySelectorAll(selector).forEach((node, index) => { if (values[index] !== undefined) node.textContent = values[index]; });
      if (s.setting === "general") {
        set(".page-header .muted", "문서를 편하게 읽을 수 있는 테마와 본문 크기를 골라요.");
        set(".field .small", "선택한 테마를 앱과 예시 문서에 함께 적용해요.");
        set(".notice", "표시 선호는 이 시안 탭을 열어 둔 동안 유지돼요. 문서 내용은 바뀌지 않아요.");
      } else if (s.setting === "connection") {
        set(".page-header h2", "연결 상태");
        set(".page-header .muted", "해룡이 실행 중인 컴퓨터와의 연결 상태를 살펴봐요.");
        set('label[for="endpoint"]', "저장된 연결 주소");
        set(".field .small", "연결 주소를 보여 주는 예시예요.");
        each("form > p.small", ["연결 상태를 보여 주는 시안이에요.", "다시 연결하면 작성하던 문서와 초안으로 돌아갈 수 있어요."]);
      } else if (s.setting === "sessions") {
        set(".page-header h2", "연결한 장치");
        set(".page-header .muted", "기억한 장치와 이번 연결에서만 사용하는 장치를 구분해요.");
        set(".notice", "연결 해제는 이 시안의 장치 목록에 반영돼요. 문서와 메모 초안은 유지됩니다.");
      } else if (s.setting === "projects") {
        set(".page-header h2", "프로젝트별 원본 사용");
        set(".page-header .muted", "프로젝트마다 연결된 경로와 다음 질문에 사용할 기본 원본 범위를 확인해요.");
        const cards = host.querySelectorAll("form .card");
        [["Podway", "Podway/ · 예시 연결 경로"], ["Haeryong", "Haeryong/ · 예시 연결 경로"]].forEach((project, index) => {
          const card = cards[index];
          if (!card) return;
          const title = card.querySelector(".row-title");
          const path = card.querySelector(".row-meta");
          const label = card.querySelector("label");
          const select = card.querySelector("select");
          if (title) title.textContent = project[0];
          if (path) path.textContent = project[1];
          if (label) label.textContent = "새 질문의 기본 범위";
          if (select) select.innerHTML = '<option value="inherit">AI 기본 설정 따름</option>' + sourceOptions;
        });
        set("form > .small", "다음에 시작하는 질문에 적용해요. 작성 중인 질문의 대상과 기준은 유지됩니다.");
        set('[data-action="settings:apply"]', "프로젝트 선호 적용");
      } else if (s.setting === "ai") {
        set(".page-header h2", "질문의 기본 범위");
        set(".page-header .muted", "질문을 시작할 때 사용할 기준을 정해요. 작성 중인 질문의 기준은 자동으로 바뀌지 않아요.");
        set('label[for="defaultSource"]', "새 질문의 기본 범위");
        const select = $("defaultSource");
        if (select) select.innerHTML = sourceOptions;
        set(".notice", s.scenario === "provider" ? "지금은 제공자 사용 불가 상태를 보여 줘요. 질문 초안을 유지하고 읽기와 메모를 계속할 수 있어요." : "질문 화면에서 기본 범위를 바로 확인할 수 있어요. 매번 설정을 다시 확인할 필요는 없어요.");
        set("form > .small", "이 시안의 답변은 예시이며 실제 AI 요청을 보내지 않아요.");
        const providerLine = host.querySelector("form > p");
        if (providerLine && providerLine.firstChild && providerLine.firstChild.nodeType === 3) providerLine.firstChild.textContent = "질문 제공자 ";
        set('[data-action="settings:apply"]', "질문 선호 적용");
      } else if (s.setting === "maintenance") {
        set(".page-header .eyebrow", "정비");
        set(".page-header h2", "점검 선호");
        set(".page-header .muted", "무엇을 얼마나 자주 살펴볼지 정해요. 점검 결과와 문서 게시 여부는 별도로 검토합니다.");
        const scope = $("scope");
        if (scope) scope.innerHTML = '<option value="all">전체 프로젝트</option><option value="podway">Podway</option><option value="haeryong">Haeryong</option>';
        const mode = $("scheduleMode");
        if (mode) mode.innerHTML = '<option value="manual">필요할 때 직접</option><option value="interval">일정 간격</option><option value="calendar">매일 정해진 시각</option>';
        set("form > .small", "이 화면은 일정 선호를 보여 주는 시안이에요. 실제 예약 작업은 만들지 않아요.");
      } else if (s.setting === "retention") {
        set(".page-header h2", "휴지통 보존 기간");
        set(".page-header .muted", "휴지통에 보낸 문서를 얼마나 남길지 정해요. 보관함과 서재의 이전 버전에는 적용하지 않아요.");
        set('label[for="retentionDays"]', "휴지통에 보관할 일수");
        const label = host.querySelector("label.row");
        if (label) Array.from(label.childNodes).filter(node => node.nodeType === 3).forEach(node => { node.textContent = " 보존 기간이 지난 휴지통 문서를 자동으로 영구 삭제"; });
        set('[data-action="dialog:retention"]', "영향 확인 후 적용");
      } else if (s.setting === "storage") {
        set(".page-header h2", "이어서 가져오기");
        set(".page-header .muted", "중단한 가져오기도 같은 문서와 기준 버전에서 이어갈 수 있어요.");
        set(".empty", "가져온 결과는 서재에서 다시 열 수 있어요.");
      } else if (s.setting === "backup") {
        set(".page-header h2", "백업과 복원 점검");
        set(".page-header .muted", "마지막 성공과 최근 시도를 따로 확인하고, 복원 가능 상태를 살펴봐요.");
        set('[data-action="backup:create"]', "새 백업 만들기");
        set('[data-action="dialog:restoreBackup"]', "복원 점검");
        set("section > .small", "복원 점검은 현재 문서를 유지한 채 예시 결과만 보여 줘요.");
      } else if (s.setting === "about") {
        set(".page-header .muted", "화면 구성과 사용 흐름을 검토하기 위한 제품 시안이에요.");
        set(".row-title", "Haeryong 디자인 시안");
        set(".row-meta", "기준 문서 v1.6.2 · 샘플 기준일 2026-10-12");
        const paragraphs = host.querySelectorAll("article > p");
        if (paragraphs[2]) paragraphs[2].textContent = "데스크톱과 좁은 화면을 위한 반응형 구성을 포함해요. 실제 브라우저와 기기의 호환성 검증은 아직 완료하지 않았어요.";
        set(".notice", "샘플 데이터로 동작하며 새로고침하면 초기화돼요. 실제 파일, 계정, 서버와 연결하지 않습니다.");
      }
    }
    if (s.route === "connect") {
      const screen = $("screen");
      if (!screen) return;
      const title = screen.querySelector(".page-header h2");
      const intro = screen.querySelector(".page-header .muted");
      const address = screen.querySelector("article .row-meta");
      if (title) title.textContent = "내 컴퓨터에 연결";
      if (intro) intro.textContent = "비교 코드를 확인하고, 이어 읽던 문서로 돌아가요.";
      if (address) address.textContent = s.settings.endpoint + " · 예시";
    }
  }

  H.settingsAction = function (action, button) {
    const s = ensure();
    if (action.startsWith("setting:")) {
      const section = action.slice(8);
      if (!["general", "connection", "sessions", "projects", "ai", "maintenance", "retention", "storage", "backup", "about"].includes(section)) return false;
      s.setting = section;
      H.render();
      return true;
    }
    switch (action) {
      case "settings:apply": {
        const proposed = copy(working());
        if (!validateSettings(proposed)) return true;
        if (proposed.retentionDays !== s.settings.retentionDays || proposed.autoPurge !== s.settings.autoPurge) retentionDialog();
        else commitSettings(proposed);
        return true;
      }
      case "dialog:retention": if (validateSettings(working())) retentionDialog(); return true;
      case "settings:confirmPolicy":
        if (s.settingsPending) { const next = copy(s.settingsPending); H.closeDialog(); commitSettings(next); }
        return true;
      case "settings:closeDialog": s.settingsPending = null; s.pendingSession = null; H.closeDialog(); return true;
      case "settings:cancel": s.settingsDraft = null; s.settingsPending = null; s.settingsDirty = false; feedback("적용 전 변경사항을 취소했어요."); H.render(); return true;
      case "connection:retry": connectionRetry(); return true;
      case "session:confirmRevoke": {
        const device = s.devices.find(item => item.id === s.pendingSession);
        if (!device) { H.closeDialog(); return true; }
        s.devices = s.devices.filter(item => item.id !== device.id);
        s.pendingSession = null;
        H.closeDialog();
        if (device.current) { s.pair.status = "expired"; s.connectionStatus = "disconnected"; s.pair.returnTarget = copy(H.target()); H.go("connect"); }
        else H.render();
        H.toast(device.name + "의 시안 연결을 해제했어요.");
        return true;
      }
      case "backup:create": createBackup(); return true;
      case "dialog:restoreBackup": restoreDialog(); return true;
      case "backup:trial": trialBackup(); return true;
      case "backup:simulateFailure": s.backup.attempt = "실패 · 시안"; s.backup.running = false; s.backup.job = (s.backup.job || 0) + 1; backupDetails(); H.toast("실패한 최근 시도와 마지막 성공을 구분해서 표시해요."); return true;
      case "pair:request": case "pair:retry": pairRequest(); return true;
      case "pair:approve": pairApprove(); return true;
      case "pair:reject": capturePairTarget(); s.pair.status = "rejected"; pairStatus(); return true;
      case "pair:expire": capturePairTarget(); s.pair.status = "expired"; pairStatus(); return true;
      default:
        if (action === "session:revoke" || action.startsWith("session:revoke:")) { revokeDialog(button, action); return true; }
        return false;
    }
  };

  function changeValue(id, value, target) {
    const s = ensure();
    if (id === "rememberDevice") {
      s.pair.remember = !!(target ? target.checked : value === true || value === "true");
      pairStatus();
      return true;
    }
    if (id === "themeChoice") {
      s.settings.themeChoice = value;
      if (s.settingsDraft) s.settingsDraft.themeChoice = value;
      s.theme = value === "dark" ? "dark" : value === "system" && window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
      applyAppearance();
      H.render();
      return true;
    }
    const key = ids[id];
    if (!key) return false;
    const next = target && target.type === "checkbox" ? target.checked : value;
    if (key === "fontSize" || key === "lineHeight") {
      const number = Number(next);
      if (!Number.isFinite(number) || number < (key === "fontSize" ? 14 : 1.3) || number > (key === "fontSize" ? 24 : 2.4)) return true;
      s.settings[key] = String(next);
      if (s.settingsDraft) s.settingsDraft[key] = String(next);
      applyAppearance();
      text(id + "Value", String(next) + (key === "fontSize" ? "px" : ""));
      feedback("읽기 모양을 시안에 바로 적용했어요.");
      return true;
    }
    setDraft(key, next);
    if (target) target.removeAttribute("aria-invalid");
    if (key === "retentionDays" || key === "autoPurge") policySummary();
    if (["scheduleMode", "scheduleInterval", "scheduleTime", "scheduleTimezone"].includes(key)) syncSchedule();
    return true;
  }

  H.settingsInput = function (id, value, target) {
    if (id === "themeChoice") return false;
    return changeValue(id, value, target);
  };

  H.settingsChange = function (id, value, target) { return changeValue(id, value, target); };

  H.afterSettingsRender = function () {
    const s = ensure();
    applyAppearance();
    normalizeSettingsCopy();
    const config = working();
    const sourceSelect = $("defaultSource");
    if (sourceSelect && sourceSelect.tagName === "SELECT" && !Array.from(sourceSelect.options).some(option => option.value === "snapshot")) {
      sourceSelect.innerHTML = '<option value="snapshot">저장된 설명 기준</option><option value="current">연결된 원본 함께 사용</option>';
    }
    Object.keys(ids).forEach(id => setControl(id, config[ids[id]]));
    setControl("themeChoice", s.theme || "light");
    text("fontSizeValue", s.settings.fontSize + "px");
    text("lineHeightValue", s.settings.lineHeight);
    policySummary();
    renderSessions();
    syncUploadSummary();
    syncSchedule();
    if (s.route === "settings" && s.setting === "connection" && !$("connectionStatus")) {
      const node = document.querySelector("#settingsBody .chip");
      if (node) node.id = "connectionStatus";
    }
    if (s.route === "settings" && s.setting === "ai" && !$("providerStatus")) {
      const node = document.querySelector("#settingsBody .chip");
      if (node) node.id = "providerStatus";
    }
    text("connectionStatus", s.connectionStatus === "checking" ? "연결 확인 중 · 시안" :
      ["disconnected", "offline"].includes(s.scenario) || s.connectionStatus === "disconnected" ? "연결 끊김 · 시안" : "연결됨 · 시안");
    if ($("connectionStatus")) $("connectionStatus").className = "chip" + (["disconnected", "offline"].includes(s.scenario) || s.connectionStatus === "disconnected" ? " warn" : s.connectionStatus === "checking" ? "" : " good");
    text("providerStatus", ["provider", "provider-unavailable", "no-provider"].includes(s.scenario) ? "사용 가능한 제공자가 없어요. 읽기와 메모는 계속 사용할 수 있어요." : "예시 제공자 · 실제 AI 요청은 보내지 않아요.");
    if ($("providerStatus")) $("providerStatus").className = "chip" + (["provider", "provider-unavailable", "no-provider"].includes(s.scenario) ? " warn" : "");
    if (s.route === "connect") {
      if (!s.pair.wasOnConnect) s.pair.returnTarget = copy(H.target());
      s.pair.wasOnConnect = true;
      capturePairTarget();
      pairStatus();
    } else s.pair.wasOnConnect = false;
    if (s.route === "settings" && s.setting === "backup") backupDetails();
    else if (s.route === "settings" && s.settingsFeedback) feedback(s.settingsFeedback);
    document.querySelectorAll('[data-action^="setting:"]').forEach(button => {
      const selected = button.dataset.action === "setting:" + s.setting;
      button.classList.toggle("active", selected);
      if (selected) button.setAttribute("aria-current", "page");
      else button.removeAttribute("aria-current");
    });
  };
})();
