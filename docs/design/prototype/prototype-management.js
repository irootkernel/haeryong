(function () {
  "use strict";

  const H = window.HR;
  if (!H) throw new Error("HR must be initialized before management interactions.");
  const timers = new Map();
  let sequence = 0;
  const labels = {
    pending: "제안 대기", approved: "승인됨", rejected: "거절됨",
    ready: "게시 전 초안", preparing: "초안 준비 중", published: "게시됨",
    idle: "시작 전", running: "진행 중", paused: "일시정지",
    completed: "완료", abandoned: "명시적으로 중단됨",
    conflict: "기준 버전 확인 필요", failed: "진행할 수 없음"
  };

  function state() {
    const s = H.state;
    s.finding = s.finding || { decision: "pending", reassessed: false };
    s.finding.revision = s.finding.revision || 1;
    s.finding.history = s.finding.history || [];
    s.finding.target = s.finding.target || { docId: "podway", versionId: "v3", pageId: "flow" };
    s.draft = s.draft || { status: "ready", base: "v3", title: "재시도 결과를 확인하는 기준" };
    s.draft.id = s.draft.id || "draft-seed";
    s.draft.revision = s.draft.revision || 1;
    s.draft.history = s.draft.history || [];
    if (s.draft.docId === undefined) s.draft.docId = "podway";
    s.draft.project = s.draft.project || "podway";
    s.draft.body = s.draft.body || sampleBody(s.draft.title, "실패 이후의 재시도 조건과 확인할 근거를 설명하는 예시 초안입니다.");
    s.scan = s.scan || { status: "idle", progress: 0 };
    s.scan.history = s.scan.history || [];
    s.scan.scope = s.scan.scope || "both";
    s.scan.depth = s.scan.depth || "structural";
    s.scan.budget = s.scan.budget || "20";
    s.scan.units = s.scan.units || sampleUnits("both", true);
    s.upload = s.upload || freshUpload();
    s.upload.history = s.upload.history || [];
    s.create = s.create || {
      status: "idle", progress: 0, title: "연결 복구 안내",
      request: "연결이 끊겼다가 돌아왔을 때의 흐름을 설명해 주세요.", draftId: null
    };
    return s;
  }

  function sampleBody(title, request) {
    return "<h2>" + H.escape(title || "새 설명") + "</h2>" +
      "<p>" + H.escape(request || "선택한 자료를 설명하는 로컬 예시입니다.") + "</p>" +
      "<h3>확인할 내용</h3><p>현재 상태를 확인하고, 필요한 근거를 읽은 뒤 다음 행동을 선택합니다.</p>" +
      "<p>디자인 시안의 표본 내용입니다. 실제 모델 호출이나 원본 변경은 없습니다.</p>";
  }

  function sampleUnits(scope, semantic) {
    let assessed = 0;
    const project = scope === "yard" ? "haeryong" : scope === "harbor" ? "podway" : null;
    return H.docs.filter(function (doc) {
      return doc.lifecycle === "active" && (!project || doc.project === project);
    }).flatMap(function (doc) {
      const version = doc.versions[latestId(doc)];
      return Object.keys(version.pages).map(function (id) {
        const unavailable = doc.source === "unknown";
        const checked = semantic && !unavailable && assessed < 4;
        if (checked) assessed += 1;
        return { docId: doc.id, versionId: version.id, pageId: id, title: doc.title,
          path: version.pages[id].path, project: doc.project,
          semantic: unavailable ? "unavailable" : checked ? "assessed" : "unchecked" };
      });
    });
  }

  function coverage(scan) {
    const units = scan.units || [];
    return {
      structural: units.length, eligible: units.length,
      assessed: units.filter(function (u) { return u.semantic === "assessed"; }).length,
      unavailable: units.filter(function (u) { return u.semantic === "unavailable"; }).length,
      unchecked: units.filter(function (u) { return u.semantic === "unchecked"; }).length
    };
  }

  function normalized(fragment) {
    const substitutions = {
      "해룡 작업 지식": "Haeryong", "근해 항로": "Podway",
      "야간 입항 체크리스트": "재시도 중 작업 소유권이 바뀌면",
      "크레인 점검 주기": "문서 버전과 개인 검토 기록",
      "부두 전력 배분": "연결 복구와 질문 초안", "용접 자격 명단": "팀에서 받은 HTML 설명",
      "도장 작업 환기": "질문 맥락과 저장", "부품 입고 절차": "초기 연결 프로토콜",
      "비상 정지 동선": "개인 검토 기록", "조류 관측 메모": "원본을 모르는 HTML",
      "선체 블록 배치": "버전 간 변경 비교", "선체 두께 측정": "사용할 수 없는 원본",
      "월간 부하 시험": "검토 상태 판정", "입항 절차": "재시도와 실행",
      "설비 안전": "문서와 개인 검토", "자격과 입고": "질문과 원본",
      "전력과 부두 전력": "연결과 연결 복구", "부두 전력": "연결 복구", "전력": "연결",
      "접안 전 전력 확인": "재시도 결과의 소유권 확인",
      "같은 작업장 언급뿐이라": "같은 용어만 나타나", "박서준": "소유자", "김하린": "소유자"
    };
    let result = fragment || "";
    Object.keys(substitutions).sort(function (a, b) { return b.length - a.length; }).forEach(function (key) {
      result = result.split(key).join(substitutions[key]);
    });
    return result;
  }

  function freshUpload(previous) {
    const d = H.doc();
    return {
      id: null, status: "idle", progress: 0, kind: "new",
      title: "연결 복구 안내", base: latestId(d) || "v3",
      docId: d ? d.id : "podway", sample: "folder", resultId: null,
      resultVersionId: null, snapshot: null, error: "", history: previous || []
    };
  }

  function latestId(doc) {
    if (!doc || !doc.versions) return null;
    if (doc.latestVersionId && doc.versions[doc.latestVersionId]) return doc.latestVersionId;
    return Object.keys(doc.versions).sort(function (a, b) {
      return a.localeCompare(b, undefined, { numeric: true });
    }).pop() || null;
  }

  function alive(doc) {
    return !!doc && doc.lifecycle !== "purged" && doc.lifecycle !== "trashed";
  }

  function stop(id) {
    if (timers.has(id)) {
      clearInterval(timers.get(id));
      timers.delete(id);
    }
  }

  function run(job, tick) {
    stop(job.id);
    timers.set(job.id, setInterval(function () {
      if (job.status !== "running" && job.status !== "preparing") {
        stop(job.id);
        return;
      }
      tick();
      paint();
    }, 320));
  }

  function blocked(needsProvider) {
    const scenario = H.state.scenario || "normal";
    if (/^(disconnected|offline|revoked|expired|session-expired|session)$/.test(scenario)) {
      H.toast("연결을 다시 확인해 주세요. 입력한 내용은 이 시안에 남아 있습니다.");
      return true;
    }
    if (needsProvider && /provider|quota|ai-unavailable/.test(scenario)) {
      H.toast("AI를 사용할 수 없는 예시입니다. 저장된 설명은 계속 읽을 수 있습니다.");
      return true;
    }
    return false;
  }

  function text(id, value) {
    const el = H.$(id);
    if (el) el.textContent = value == null ? "" : String(value);
  }

  function summary(id, message) {
    const container = H.$(id);
    if (!container) return;
    let el = container.querySelector("[data-management-status]");
    if (!el) {
      el = container.querySelector("p");
      if (!el) {
        el = document.createElement("p");
        container.prepend(el);
      }
      el.dataset.managementStatus = "true";
    }
    el.textContent = message;
  }

  function value(id, next) {
    const el = H.$(id);
    if (el && document.activeElement !== el && next != null) el.value = String(next);
  }

  function status(id, message, actions) {
    const el = H.$(id);
    if (!el) return;
    const signature = message + JSON.stringify(actions || []);
    if (el.dataset.managementSignature === signature) return;
    el.dataset.managementSignature = signature;
    el.textContent = message;
    (actions || []).forEach(function (item) {
      const button = document.createElement("button");
      button.type = "button";
      button.dataset.action = item.action;
      button.textContent = item.label;
      button.className = "button";
      el.appendChild(document.createTextNode(" "));
      el.appendChild(button);
    });
  }

  function disable(action, disabled) {
    document.querySelectorAll('[data-action="' + action + '"]').forEach(function (el) {
      el.disabled = !!disabled;
    });
  }

  function progress(id, amount) {
    const el = H.$(id);
    if (!el) return;
    if (el.tagName === "PROGRESS") {
      el.max = 100;
      el.value = amount;
    } else {
      const fill = el.querySelector("[data-progress-fill], .progress-fill, i");
      if (fill) fill.style.width = amount + "%";
      else if (el.tagName === "INPUT") el.value = amount;
      else el.textContent = amount + "%";
    }
    el.setAttribute("aria-valuenow", String(amount));
    el.setAttribute("aria-valuemin", "0");
    el.setAttribute("aria-valuemax", "100");
  }

  function redraw() {
    H.render();
    paint();
  }

  function dialog(name) {
    const names = {
      finding: ["발견 내용", "finding"], draft: ["초안 검토", "draftPreview"],
      scan: ["정비 시작", "scan"], import: ["결과 가져오기", "import"],
      create: ["새 설명 만들기", "create"]
    };
    const entry = names[name];
    if (!entry) return;
    H.state.managementDialog = name;
    H.openDialog(entry[0], normalized(H.fragments[entry[1]]) || "<p>이 시안의 화면을 준비 중입니다.</p>");
    paint();
  }

  function outcomeVersion(doc, body, title) {
    const result = H.newVersion(doc, body, title);
    const id = typeof result === "string" ? result : result && result.id;
    const versionId = id || latestId(doc);
    if (!versionId || !doc.versions[versionId]) throw new Error("The sample result did not create an openable version.");
    doc.versions[versionId].review = "unreviewed";
    doc.last = { version: versionId, page: "index" };
    if (!doc.versions[doc.preferred]) doc.preferred = versionId;
    return versionId;
  }

  function newDocument(title, project) {
    let id;
    do { id = "sample-" + (++sequence); } while (H.docs.some(function (d) { return d.id === id; }));
    const doc = {
      id: id, project: project || "podway", title: title || "새 설명",
      path: "결과/" + id + "/index.html", freshness: "unknown", source: "unknown",
      lifecycle: "active", priorLifecycle: "active", preferred: "v1",
      last: { version: null, page: "index" }, versions: {}
    };
    H.docs.push(doc);
    return doc;
  }

  function readUploadForm() {
    const u = state().upload;
    if (u.snapshot) return;
    ["importKind", "importTitle", "importBase", "importSample"].forEach(function (id) {
      const el = H.$(id);
      if (el) managementChange(id, el.value, el);
    });
  }

  function openImport(existingVersion) {
    const s = state();
    const unfinished = ["running", "paused", "conflict", "failed"].includes(s.upload.status);
    if (!unfinished && ["completed", "abandoned"].includes(s.upload.status)) {
      s.upload = freshUpload(s.upload.history);
    }
    if (!unfinished && existingVersion) {
      const doc = H.doc();
      s.upload.kind = "version";
      s.upload.docId = doc.id;
      s.upload.base = s.versionId;
      s.upload.title = doc.title;
    }
    if (unfinished && existingVersion) H.toast("진행 중인 가져오기의 대상과 기준을 그대로 표시합니다.");
    dialog("import");
  }

  function startUpload() {
    const s = state();
    const u = s.upload;
    if (["running", "completed", "abandoned", "conflict"].includes(u.status)) return;
    if (blocked(false)) return;
    if (u.status === "paused") { resumeUpload(); return; }
    readUploadForm();
    const title = String(u.title || "").trim();
    if (!title) { u.error = "결과의 제목을 입력해 주세요."; paint(); return; }
    const target = H.doc(u.docId);
    if (u.kind === "version" && (!alive(target) || !target.versions[u.base])) {
      u.status = "conflict";
      u.error = "선택한 문서 또는 기준 버전을 사용할 수 없습니다. 원래 대상을 확인해 주세요.";
      paint();
      return;
    }
    if (!u.snapshot) {
      u.snapshot = {
        kind: u.kind, title: title, docId: u.kind === "version" ? u.docId : null,
        base: u.kind === "version" ? u.base : null, sample: u.sample,
        project: (target && target.project) || s.projectId || "podway"
      };
      u.id = "upload-" + (++sequence);
      u.progress = 0;
    }
    u.status = "running";
    u.error = "";
    beginUploadTimer(u);
    paint();
  }

  function beginUploadTimer(u) {
    run(u, function () {
      u.progress = Math.min(100, u.progress + 10);
      if (u.progress === 100) completeUpload(u);
    });
  }

  function completeUpload(u) {
    stop(u.id);
    if (u.resultId || u.status !== "running") return;
    const selected = u.snapshot;
    let doc = selected.kind === "version" ? H.doc(selected.docId) : null;
    if (selected.kind === "version" && (!alive(doc) || latestId(doc) !== selected.base)) {
      u.status = "conflict";
      u.error = "가져오는 동안 문서의 기준이 달라졌습니다. 선택했던 " + selected.base +
        "을 그대로 유지했습니다. 현재 기준을 확인한 뒤 다시 진행해 주세요.";
      return;
    }
    if (!doc) doc = newDocument(selected.title, selected.project);
    const body = sampleBody(selected.title, "선택한 " +
      ({ folder: "폴더", zip: "ZIP", html: "HTML" }[selected.sample] || selected.sample) +
      "의 내용을 표현하는 예시 결과입니다. 실제 파일을 읽거나 업로드하지 않았습니다.");
    const versionId = outcomeVersion(doc, body, selected.title);
    u.resultId = doc.id;
    u.resultVersionId = versionId;
    u.status = "completed";
    u.error = "";
    u.history.push({ id: u.id, status: "completed", title: selected.title, docId: doc.id, versionId: versionId });
    H.toast("가져오기 예시가 완료되었습니다. 결과 열기에서 확인할 수 있습니다.");
  }

  function resumeUpload() {
    const u = state().upload;
    if (u.status !== "paused" || !u.snapshot || blocked(false)) return;
    u.status = "running";
    u.error = "";
    beginUploadTimer(u);
    paint();
  }

  function rebaseUpload() {
    const u = state().upload;
    const doc = H.doc(u.snapshot ? u.snapshot.docId : u.docId);
    if (u.status !== "conflict" || !alive(doc) || blocked(false)) return;
    const base = latestId(doc);
    if (!base) return;
    u.base = base;
    if (u.snapshot) u.snapshot.base = base;
    u.error = "";
    u.status = u.snapshot ? "paused" : "idle";
    u.progress = Math.min(u.progress || 0, 90);
    H.toast("명시적으로 선택한 현재 버전 " + base + "을 새 기준으로 기록했습니다.");
    paint();
  }

  function isStaleFinding() {
    const s = state();
    return !!s.finding.stale || ((s.scenario === "stale") && !s.finding.reassessed);
  }

  function decideFinding(decision) {
    const f = state().finding;
    if (blocked(false)) return;
    if (decision === "approved" && isStaleFinding()) {
      f.message = "근거의 기준이 달라졌습니다. 먼저 재평가해 주세요.";
      paint();
      return;
    }
    f.decision = decision;
    f.overlayActive = decision === "approved";
    f.revision += 1;
    f.history.push({ action: decision, revision: f.revision });
    f.message = decision === "approved" ?
      "제안 메타데이터만 승인했습니다. 원본과 개인 검토 상태는 그대로입니다." :
      "이 제안을 거절했습니다. 다른 문서나 개인 검토 상태는 바뀌지 않습니다.";
    paint();
  }

  function prepareDraft(fromFinding) {
    const s = state();
    if (blocked(true)) return;
    if (s.draft.status === "preparing" || s.create.status === "running") {
      dialog("draft");
      return;
    }
    if (fromFinding && isStaleFinding()) {
      s.finding.message = "오래된 발견은 먼저 재평가한 뒤 초안을 만들어 주세요.";
      paint();
      return;
    }
    const target = s.finding.target;
    const doc = fromFinding ? H.doc(target.docId) : null;
    if (fromFinding && !alive(doc)) {
      H.toast("초안을 만들 원래 문서를 사용할 수 없습니다.");
      return;
    }
    const title = fromFinding ? "재시도 결과를 확인하는 기준" : String(s.create.title || "").trim();
    const request = fromFinding ? "관찰한 변경과 인용 근거를 반영하는 예시 초안입니다." : String(s.create.request || "").trim();
    if (!title || !request) {
      s.create.error = "제목과 설명할 내용을 입력해 주세요.";
      paint();
      return;
    }
    s.draft = {
      id: "draft-" + (++sequence), status: "preparing", progress: 0,
      base: doc ? latestId(doc) : null, docId: doc ? doc.id : null,
      project: doc ? doc.project : (s.projectId || "podway"),
      title: title, request: request, body: sampleBody(title, request),
      revision: 1, history: [], resultId: null, resultVersionId: null,
      message: "", origin: fromFinding ? "finding" : "create"
    };
    const draft = s.draft;
    if (!fromFinding) {
      s.create.status = "running";
      s.create.progress = 0;
      s.create.draftId = draft.id;
      s.create.error = "";
    } else dialog("draft");
    run(draft, function () {
      draft.progress = Math.min(100, draft.progress + 20);
      if (draft.origin === "create" && s.create.draftId === draft.id) s.create.progress = draft.progress;
      if (draft.progress === 100) {
        stop(draft.id);
        draft.status = "ready";
        draft.message = "표본 초안이 준비되었습니다. 검토와 게시는 별도 행동입니다.";
        if (draft.origin === "create" && s.create.draftId === draft.id) s.create.status = "completed";
        H.toast("예시 초안이 준비되었습니다.");
      }
    });
    paint();
  }

  function draftStale(draft) {
    if (!draft.docId) return false;
    const doc = H.doc(draft.docId);
    return !alive(doc) || latestId(doc) !== draft.base ||
      (/^(stale|stale-draft|stale-base|version-conflict)$/.test(H.state.scenario || "") && !draft.rebased);
  }

  function publishDraft() {
    const draft = state().draft;
    if (draft.status !== "ready" || draft.resultId || blocked(false)) return;
    if (draftStale(draft)) {
      draft.message = "초안의 기준 " + draft.base + "이 현재 버전과 다릅니다. 초안은 게시하지 않았습니다.";
      draft.conflict = true;
      paint();
      return;
    }
    const doc = draft.docId ? H.doc(draft.docId) : newDocument(draft.title, draft.project);
    const versionId = outcomeVersion(doc, draft.body, draft.title);
    draft.status = "published";
    draft.resultId = doc.id;
    draft.resultVersionId = versionId;
    draft.history.push({ action: "publish", revision: draft.revision, versionId: versionId });
    draft.message = "예시 버전 " + versionId + "을 게시했습니다. 개인 검토는 미검토 상태입니다.";
    paint();
    H.toast("게시한 표본 결과를 열 수 있습니다.");
  }

  function replaceDraft() {
    const draft = state().draft;
    if (draft.status !== "ready" || blocked(false)) return;
    draft.revision += 1;
    draft.history.push({ action: "replace", revision: draft.revision, base: draft.base });
    draft.body = sampleBody(draft.title, (draft.request || "확인할 근거와 설명 순서") +
      " 표본 교체 파일 " + draft.revision + "을 적용했습니다. 선택한 기준 버전은 그대로입니다.");
    draft.message = "교체 표본을 적용했습니다. 초안 리비전 " + draft.revision +
      "을 다시 검토해 주세요. 아직 게시되지 않았습니다.";
    paint();
  }

  function beginScan() {
    const s = state();
    const scan = s.scan;
    if (scan.status === "running") return;
    ["scanScope", "scanDepth", "scanBudget"].forEach(function (id) {
      const el = H.$(id);
      if (el) managementChange(id, el.value, el);
    });
    if (blocked(scan.depth === "semantic")) return;
    const budget = Number(scan.budget);
    if (!Number.isFinite(budget) || budget < 1) {
      scan.error = "작업 예산에는 1 이상의 횟수를 입력해 주세요.";
      paint();
      return;
    }
    scan.id = "scan-" + (++sequence);
    scan.status = "running";
    scan.progress = 0;
    scan.error = "";
    scan.captured = { scope: scan.scope, depth: scan.depth, budget: scan.budget, project: s.projectId };
    scan.units = sampleUnits(scan.scope, scan.depth === "semantic");
    run(scan, function () {
      scan.progress = Math.min(100, scan.progress + 10);
      if (scan.progress === 100) {
        stop(scan.id);
        scan.status = "completed";
        scan.coverage = coverage(scan);
        const counts = scan.coverage;
        scan.history.unshift({
          id: scan.id, scope: { both: "프로젝트 2곳", yard: "Haeryong", harbor: "Podway" }[scan.captured.scope] || scan.captured.scope,
          status: scan.depth === "semantic" ? "일부 의미 검토 완료" : "구조 확인 완료",
          structural: counts.structural + "/" + counts.eligible, semantic: counts.assessed + "/" + counts.eligible,
          date: "2026-10-12"
        });
        H.toast("예시 정비가 끝났습니다. 미평가 자료를 포함한 범위를 확인해 주세요.");
      }
    });
    H.closeDialog();
    H.go("maintenance");
    paint();
  }

  function addPreviewContent(draft) {
    const marker = H.$("draftState");
    if (!marker) return;
    const body = H.$("dialogBody");
    let title = H.$("draftTitle") || (body && body.querySelector(".page-header h1"));
    let content = H.$("draftContent");
    if (!content) {
      content = body && body.querySelector("article.section");
      if (!content) {
        content = document.createElement("section");
        content.className = "card section";
        marker.insertAdjacentElement("afterend", content);
      }
      content.id = "draftContent";
    }
    if (title) title.textContent = draft.title;
    const key = draft.id + ":" + draft.revision;
    if (content.dataset.draftRevisionKey !== key) {
      content.innerHTML = draft.body;
      content.dataset.draftRevisionKey = key;
    }
    const original = body && body.querySelector("article.card:not(#draftContent)");
    if (original) {
      const heading = original.querySelector("h2");
      const desc = original.querySelector("p");
      if (heading) heading.textContent = draft.base ? "기준 " + draft.base : "새 문서로 게시";
      if (desc) desc.textContent = draft.docId ?
        ((H.doc(draft.docId) || {}).title || "원래 문서") + "의 표본 버전을 기준으로 준비한 내용입니다." :
        "아직 서재에 게시되지 않은 새 설명입니다.";
    }
    const publish = body && body.querySelector('[data-action="draft:publish"]');
    if (publish) publish.textContent = "검토한 초안 게시";
    const footer = body && body.querySelector("p.small.muted");
    if (footer) footer.textContent = "교체와 거절은 초안 기록에 남습니다. 별도로 게시해야 서재의 새 버전이 됩니다. " +
      (draft.history.length ? "이 초안의 기록: " + draft.history.map(function (item) {
        return ({ replace: "교체", reject: "거절", publish: "게시", rebase: "기준 변경" }[item.action] || item.action) +
          " r" + item.revision;
      }).join(" · ") : "아직 초안을 변경하지 않았습니다.");
  }

  function paintReports(s) {
    const report = H.$("reportBody");
    const counts = coverage(s.scan);
    const countText = "구조 " + counts.structural + "/" + counts.eligible +
      " · 의미 " + counts.assessed + "/" + counts.eligible +
      " · 미평가 " + counts.unchecked + " · 원본 없음 " + counts.unavailable;
    if (!report) return;
    if (report.dataset.managementRendered !== s.report) {
      report.innerHTML = normalized(H.fragments[s.report]);
      report.dataset.managementRendered = s.report;
    }
    const summaryHeader = report.parentElement && report.parentElement.querySelector("header .small");
    if (summaryHeader) summaryHeader.textContent = "선택한 표본 범위 · " + countText;
    const mainHeader = H.$("screen") && H.$("screen").querySelector(".page-header .muted");
    if (mainHeader) mainHeader.textContent = "Podway와 Haeryong의 설명을 점검하는 로컬 시안입니다. " +
      (s.scan.status === "idle" ? "아래에는 미리 준비한 표본 보고서를 표시합니다." : "이번 예시 실행의 범위를 표시합니다.");
    if (s.report === "coverage") {
      const intro = report.querySelector(".page-header .muted");
      if (intro) intro.textContent = countText + ". 원본을 확인하지 못한 항목도 전체 범위에 남깁니다.";
      const cards = report.querySelectorAll(".grid .card");
      if (cards[0]) {
        cards[0].querySelector(".row-title").textContent = counts.structural + "/" + counts.eligible;
        cards[0].querySelector(".muted").textContent = "선택한 페이지와 경로 확인";
      }
      if (cards[1]) {
        cards[1].querySelector(".row-title").textContent = counts.assessed + "/" + counts.eligible;
        cards[1].querySelector(".muted").textContent = "전체 선택 범위 중 평가한 페이지";
      }
      const table = report.querySelector("tbody");
      if (table) table.innerHTML = s.scan.units.map(function (unit) {
        return "<tr><td>" + H.escape(unit.title) + "<br><span class='small muted'>" + H.escape(unit.path) +
          "</span></td><td>" + H.escape(unit.project === "podway" ? "Podway" : "Haeryong") +
          "</td><td>확인됨</td><td>" + ({ assessed: "평가됨", unchecked: "미평가", unavailable: "원본 없음" }[unit.semantic]) + "</td></tr>";
      }).join("");
      const notice = report.querySelector(".notice");
      if (notice) notice.textContent = "평가하지 않은 " + counts.unchecked + "개와 원본을 확인하지 못한 " +
        counts.unavailable + "개도 전체 " + counts.eligible + "개에 포함됩니다. 구조 확인만으로 의미가 최신이라고 판단하지 않습니다.";
    }
    if (s.report === "freshness") {
      const rows = report.querySelectorAll(".list-row");
      const primary = H.doc(s.finding.target.docId);
      if (rows[0] && primary) {
        rows[0].querySelector(".row-title").textContent = primary.title;
        rows[0].querySelector(".row-meta").textContent = "기준 " + s.finding.target.versionId + " · 결과 반영 조건을 확인한 표본 발견";
        rows[0].querySelector(".small").textContent = "Podway · " + (labels[s.finding.decision] || s.finding.decision) +
          (s.finding.reassessed ? " · 현재 기준 재평가" : "");
      }
      if (rows[1]) {
        const unknown = H.doc("unknown");
        rows[1].querySelector(".row-title").textContent = unknown ? unknown.title : "원본을 모르는 설명";
        rows[1].querySelector(".row-meta").textContent = "원본 정보 없음 · 최신 여부를 추정하지 않음";
        rows[1].querySelector(".small").textContent = "저장된 HTML은 계속 읽을 수 있습니다.";
        const button = rows[1].querySelector("button");
        if (button) { button.dataset.action = "open:unknown"; button.textContent = "설명 읽기"; }
      }
    }
    if (s.report === "relationships") {
      const suggestions = Array.from(report.querySelectorAll(".list-row")).filter(function (row) {
        return !!row.querySelector('[data-action="finding:approve"]');
      });
      if (suggestions[0]) {
        suggestions[0].querySelector(".row-title").textContent = "재시도 설명 → 문서 버전과 개인 검토 기록";
        suggestions[0].querySelector(".row-meta").textContent = "현재 소유권과 버전 고정이라는 근거를 함께 참고 · " +
          (labels[s.finding.decision] || s.finding.decision);
      }
      if (suggestions[1]) {
        suggestions[1].querySelector(".row-title").textContent = "원본을 모르는 HTML → 추가 근거 필요";
        suggestions[1].querySelector(".row-meta").textContent = "근거가 부족한 예시입니다. 확인 전에는 연결을 제안하지 않습니다.";
        const controls = suggestions[1].querySelector(".row-end");
        if (controls) controls.textContent = "근거 부족";
      }
    }
    if (s.report === "drafts") {
      const list = report.querySelector(".list");
      if (list) list.innerHTML = "<article class='list-row'><div><p class='row-title'>" +
        H.escape(s.draft.title) + "</p><p class='row-meta'>" +
        H.escape((labels[s.draft.status] || s.draft.status) + " · 리비전 " + s.draft.revision +
          (s.draft.base ? " · 기준 " + s.draft.base : " · 새 문서")) +
        "</p></div><div class='row-end'><button class='button primary' type='button' data-action='dialog:draft'>초안과 이력 열기</button></div></article>";
    }
    if (s.report === "activity") {
      const items = s.finding.history.map(function (item) {
        return "발견 " + ({ approved: "승인", rejected: "거절", undo: "승인 취소", reassess: "재평가" }[item.action] || item.action) +
          " · 리비전 " + item.revision;
      }).concat(s.draft.history.map(function (item) {
        return "초안 " + ({ replace: "교체", reject: "거절", publish: "게시", rebase: "기준 재선택" }[item.action] || item.action) +
          " · 리비전 " + item.revision + (item.versionId ? " · " + item.versionId : "");
      })).concat(s.scan.history.map(function (item) { return item.status + " · " + item.scope + " · 의미 " + item.semantic; }));
      const heading = report.querySelector("h1");
      const description = report.querySelector(".page-header .muted");
      if (heading) heading.textContent = "이 시안의 활동 " + items.length + "건";
      if (description) description.textContent = "이번 탭에서 실행한 표본 동작입니다. 실제 서버의 기록이 아닙니다.";
      const list = report.querySelector(".list");
      if (list) list.innerHTML = items.length ? items.reverse().map(function (item) {
        return "<article class='list-row'><div><p class='row-title'>" + H.escape(item) +
          "</p><p class='row-meta'>로컬 시안 · 본문과 개인 검토 상태는 별도로 관리합니다.</p></div></article>";
      }).join("") : "<p class='empty'>아직 이 시안에서 실행한 정비 동작이 없습니다.</p>";
    }
    const screen = H.$("screen");
    if (screen && !H.$("scanHistory")) {
      Array.from(screen.querySelectorAll("section.section")).forEach(function (section) {
        const heading = section.querySelector("h2");
        const list = section.querySelector(".list");
        if (heading && heading.textContent.trim() === "검사 기록" && list) list.id = "scanHistory";
      });
    }
  }

  function paint() {
    const s = state();
    const u = s.upload;
    const scan = s.scan;
    const draft = s.draft;
    const f = s.finding;
    paintReports(s);
    const counts = coverage(scan);
    text("scanStatus", (labels[scan.status] || scan.status) +
      (scan.status === "running" ? " · " + scan.progress + "% · " +
        (scan.progress < 30 ? "자료 목록 확인" : scan.progress < 60 ? "구조 확인" : "일부 의미 검토") : "") +
      (scan.status === "completed" ? " · 구조 " + counts.structural + "/" + counts.eligible +
        ", 의미 " + counts.assessed + "/" + counts.eligible + " · 미평가 " + counts.unchecked + ", 원본 없음 " + counts.unavailable : "") +
      (scan.error ? " · " + scan.error : ""));
    value("scanScope", scan.scope);
    value("scanDepth", scan.depth);
    value("scanBudget", scan.budget);
    const budgetField = H.$("scanBudget");
    if (budgetField) {
      budgetField.removeAttribute("max");
      const body = H.$("dialogBody");
      const note = body && body.querySelector("p.small");
      if (note) note.textContent = "예산은 이번 의미 검토의 AI 호출 시도 횟수입니다. 문서의 크기나 선택한 자료의 개수 제한이 아닙니다. 구조 확인은 AI 없이 사용할 수 있습니다.";
    }
    disable("scan:start", scan.status === "running");
    const history = H.$("scanHistory") || H.$("activityHistory");
    if (history) history.innerHTML = scan.history.map(function (row) {
      return "<p>" + H.escape(row.date + " · " + row.scope + " · " + row.status +
        " · 구조 " + row.structural + ", 의미 " + row.semantic) + "</p>";
    }).join("") || "<p>아직 실행한 예시 정비가 없습니다.</p>";

    const fMessage = (isStaleFinding() ? "재평가 필요" : labels[f.decision] || f.decision) +
      " · 리비전 " + f.revision + (f.message ? " · " + f.message : "");
    summary("findingDecision", fMessage);
    text("findingStatus", fMessage);
    const findingRoot = H.$("findingDecision") && H.$("dialogBody");
    if (findingRoot) {
      const doc = H.doc(f.target.docId);
      const heading = findingRoot.querySelector(".page-header h1");
      if (heading) heading.textContent = doc ? doc.title : "원래 대상 문서";
      const description = findingRoot.querySelector(".page-header .muted");
      if (description) description.textContent = "Podway · 선택한 설명과 근거에 대한 표본 발견";
      const evidence = findingRoot.querySelector("article.card p");
      if (evidence) evidence.textContent = "작성 당시 설명은 결과가 도착하면 저장하는 흐름을 사용합니다. 비교한 표본은 결과를 저장하기 직전에 현재 소유권을 확인합니다.";
      const target = findingRoot.querySelector(".code");
      if (target) target.textContent = "기준 " + f.target.versionId + " · 실행기/재시도/흐름.html · 디자인 검토용 근거";
      const overlay = findingRoot.querySelector("article.section");
      if (overlay) {
        const paragraphs = overlay.querySelectorAll("p");
        if (paragraphs[0]) paragraphs[0].textContent = "주제: 재시도와 결과 반영. 관찰한 근거를 바탕으로 다시 확인할 부분을 표시합니다.";
        if (paragraphs[1]) paragraphs[1].textContent = "문서 버전과 개인 검토 기록을 함께 참고하도록 제안합니다. 승인해도 원본 HTML을 게시하지 않습니다.";
      }
      const limit = findingRoot.querySelector("p.notice");
      if (limit) limit.textContent = "한계: 전체 " + counts.eligible + "개 표본 페이지 중 의미 " + counts.assessed +
        "개를 평가했습니다. 미평가와 원본 없음은 그대로 남깁니다. 승인은 원본 게시나 개인 검토 완료가 아닙니다.";
    }
    disable("finding:approve", isStaleFinding() || f.decision === "approved");
    disable("finding:undo", !f.overlayActive);

    let draftMessage = (labels[draft.status] || draft.status) + " · " + draft.title +
      " · 리비전 " + draft.revision + (draft.base ? " · 기준 " + draft.base : " · 새 문서");
    if (draft.status === "preparing") draftMessage += " · " + (draft.progress || 0) + "%";
    if (draft.status === "ready" && draftStale(draft)) draftMessage += " · 기준이 달라져 게시할 수 없습니다.";
    if (draft.message) draftMessage += " · " + draft.message;
    const draftActions = [];
    if (draft.status === "published") draftActions.push({ action: "draft:open", label: "게시한 결과 열기" });
    if (draft.status === "ready" && (draft.conflict || draftStale(draft))) draftActions.push({ action: "draft:rebase", label: "현재 버전을 기준으로 다시 검토" });
    status("draftState", draftMessage, draftActions);
    text("draftSummary", draftMessage);
    addPreviewContent(draft);
    disable("draft:replace", draft.status !== "ready");
    disable("draft:publish", draft.status !== "ready" || draftStale(draft));
    disable("draft:reject", !["ready", "preparing"].includes(draft.status));

    value("importKind", u.kind === "version" ? "newversion" : "newdoc");
    value("importTitle", u.title);
    value("importSample", u.sample);
    const baseSelect = H.$("importBase");
    if (baseSelect && baseSelect.tagName === "SELECT") {
      const doc = H.doc(u.docId);
      Object.keys((doc && doc.versions) || {}).forEach(function (id) {
        if (!Array.from(baseSelect.options).some(function (option) { return option.value === id; })) {
          const option = document.createElement("option");
          option.value = id;
          option.textContent = id;
          baseSelect.appendChild(option);
        }
      });
    }
    value("importBase", u.base);
    ["importKind", "importTitle", "importBase", "importSample"].forEach(function (id) {
      const el = H.$(id);
      if (el) el.disabled = !!u.snapshot || u.status === "completed" || u.status === "abandoned";
    });
    progress("importProgress", u.progress || 0);
    const captured = u.snapshot;
    const targetDoc = H.doc(captured ? captured.docId : u.docId);
    const targetLabel = (captured ? captured.kind : u.kind) === "version" ?
      "문서 " + (targetDoc ? targetDoc.title : "사용할 수 없음") + " · 기준 " + (captured ? captured.base : u.base) :
      "새 문서";
    const uploadMessage = (labels[u.status] || u.status) + " · " + targetLabel +
      " · " + (u.progress || 0) + "%" + (u.error ? " · " + u.error : "") +
      (u.status === "completed" ? " · 결과가 서재에 반영되었습니다." : "");
    status("importStatus", uploadMessage, u.status === "conflict" && alive(targetDoc) ?
      [{ action: "upload:rebase", label: "현재 기준을 확인하고 선택" }] : []);
    text("uploadSummaryText", (u.title || "가져오기") + " · " + uploadMessage +
      " · 이 값은 실제 전송량이 아닌 디자인 시안의 진행 예시입니다.");
    if (H.syncUploadSummary) H.syncUploadSummary();
    disable("upload:start", !["idle", "failed"].includes(u.status));
    disable("upload:pause", u.status !== "running");
    disable("upload:resume", u.status !== "paused");
    disable("upload:open", u.status !== "completed");
    disable("upload:abandon", ["idle", "completed", "abandoned"].includes(u.status));

    value("createTitle", s.create.title);
    value("createRequest", s.create.request);
    text("createStatus", (labels[s.create.status] || s.create.status) +
      (s.create.status === "running" ? " · " + s.create.progress + "%" : "") +
      (s.create.status === "completed" ? " · 초안 열기에서 검토한 뒤 별도로 게시할 수 있습니다." : "") +
      (s.create.error ? " · " + s.create.error : ""));
    disable("create:start", s.create.status === "running");
    disable("create:open", !s.create.draftId || s.create.draftId !== draft.id);
    document.querySelectorAll('[data-action^="report:"]').forEach(function (button) {
      button.setAttribute("aria-selected", button.dataset.action === "report:" + s.report ? "true" : "false");
    });
  }

  function managementAction(action) {
    const s = state();
    if (action.indexOf("report:") === 0) {
      const report = action.slice(7);
      if (!["coverage", "freshness", "relationships", "topics", "drafts", "activity"].includes(report)) return false;
      s.report = report;
      redraw();
      return true;
    }
    switch (action) {
      case "dialog:scan": dialog("scan"); break;
      case "dialog:finding": dialog("finding"); break;
      case "dialog:draft": dialog("draft"); break;
      case "dialog:import": openImport(false); break;
      case "dialog:importVersion": openImport(true); break;
      case "dialog:create": dialog("create"); break;
      case "finding:approve": decideFinding("approved"); break;
      case "finding:reject": decideFinding("rejected"); break;
      case "finding:undo":
        if (s.finding.overlayActive && !blocked(false)) {
          s.finding.overlayActive = false;
          s.finding.decision = "pending";
          s.finding.history.push({ action: "undo", revision: ++s.finding.revision });
          s.finding.message = "적용한 메타데이터만 되돌렸습니다. 원본과 개인 검토는 바뀌지 않았습니다.";
          paint();
        }
        break;
      case "finding:reassess":
        if (!blocked(true)) {
          const doc = H.doc(s.finding.target.docId);
          if (!alive(doc)) { H.toast("원래 대상 문서를 사용할 수 없습니다."); break; }
          s.finding.stale = false;
          s.finding.reassessed = true;
          s.finding.decision = "pending";
          s.finding.overlayActive = false;
          s.finding.target.versionId = latestId(doc);
          s.finding.history.push({ action: "reassess", revision: ++s.finding.revision });
          s.finding.message = "현재 기준에 대한 표본 재평가를 준비했습니다. 승인과 게시는 별도입니다.";
          paint();
        }
        break;
      case "draft:create": prepareDraft(true); break;
      case "draft:replace": replaceDraft(); break;
      case "draft:reject":
        if (["ready", "preparing"].includes(s.draft.status) && !blocked(false)) {
          stop(s.draft.id);
          s.draft.status = "rejected";
          s.draft.history.push({ action: "reject", revision: s.draft.revision });
          s.draft.message = "초안을 거절했습니다. 게시한 원본은 바뀌지 않았습니다.";
          if (s.create.draftId === s.draft.id) s.create.status = "rejected";
          paint();
        }
        break;
      case "draft:publish": publishDraft(); break;
      case "draft:rebase":
        if (s.draft.status === "ready" && (s.draft.conflict || draftStale(s.draft)) && !blocked(false)) {
          const doc = H.doc(s.draft.docId);
          if (!alive(doc)) { H.toast("원래 대상 문서를 사용할 수 없습니다."); break; }
          s.draft.base = latestId(doc);
          s.draft.rebased = true;
          s.draft.conflict = false;
          s.draft.history.push({ action: "rebase", revision: ++s.draft.revision, base: s.draft.base });
          s.draft.message = "현재 기준을 명시적으로 선택했습니다. 이 초안을 다시 읽은 뒤 게시해 주세요.";
          paint();
        }
        break;
      case "draft:open":
        if (s.draft.resultId) { H.closeDialog(); H.openDoc(s.draft.resultId, s.draft.resultVersionId, "index"); }
        break;
      case "scan:start": beginScan(); break;
      case "scan:open": H.closeDialog(); s.report = "coverage"; H.go("maintenance"); break;
      case "upload:start": startUpload(); break;
      case "upload:pause":
        if (s.upload.status === "running") { stop(s.upload.id); s.upload.status = "paused"; paint(); }
        break;
      case "upload:resume": resumeUpload(); break;
      case "upload:rebase": rebaseUpload(); break;
      case "upload:open":
        if (s.upload.status === "completed" && s.upload.resultId) {
          H.closeDialog();
          H.openDoc(s.upload.resultId, s.upload.resultVersionId, "index");
        }
        break;
      case "upload:abandon":
        if (!["idle", "completed", "abandoned"].includes(s.upload.status)) {
          stop(s.upload.id);
          s.upload.status = "abandoned";
          s.upload.error = "이 표본 업로드를 명시적으로 중단했습니다. 게시된 문서를 삭제한 것은 아닙니다.";
          s.upload.history.push({ id: s.upload.id, status: "abandoned", title: s.upload.title });
          paint();
        }
        break;
      case "create:start": prepareDraft(false); break;
      case "create:open":
        if (s.create.draftId && s.create.draftId === s.draft.id) dialog("draft");
        else H.toast("이 생성 요청의 초안이 아직 준비되지 않았습니다.");
        break;
      default: return false;
    }
    return true;
  }

  function managementChange(id, next) {
    const s = state();
    const uploadFields = { importKind: "kind", importTitle: "title", importBase: "base", importSample: "sample" };
    if (uploadFields[id]) {
      if (!s.upload.snapshot && !["completed", "abandoned"].includes(s.upload.status)) {
        s.upload[uploadFields[id]] = id === "importKind" ?
          (next === "newversion" || next === "version" ? "version" : "new") : String(next);
        s.upload.error = "";
      }
      return true;
    }
    const scanFields = { scanScope: "scope", scanDepth: "depth", scanBudget: "budget" };
    if (scanFields[id]) {
      if (s.scan.status !== "running") s.scan[scanFields[id]] = String(next);
      return true;
    }
    if (id === "createTitle" || id === "createRequest") {
      s.create[id === "createTitle" ? "title" : "request"] = String(next);
      s.create.error = "";
      return true;
    }
    return false;
  }

  H.managementAction = managementAction;
  H.managementInput = managementChange;
  H.managementChange = managementChange;
  H.afterManagementRender = paint;
  H.managementStopJobs = function () { Array.from(timers.keys()).forEach(stop); };
}());
