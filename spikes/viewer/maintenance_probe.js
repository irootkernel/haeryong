(async () => {
  const C = /*__CONFIG__*/null;
  const rows = [];
  let stage = "bootstrap", pageError = false;
  const native = typeof window.haeryongNativeCall === "function";
  const stable = value => value !== null && typeof value === "object" ? Array.isArray(value) ? value.map(stable) : Object.fromEntries(Object.keys(value).sort().map(key => [key, stable(value[key])])) : value;
  const same = (a, b) => JSON.stringify(stable(a)) === JSON.stringify(stable(b));
  const check = (condition, name) => { stage = name; if (!condition) throw new Error(name); };
  const log = name => rows.push({ name, status: "pass" });
  const settle = () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  const wait = async predicate => { const end = Date.now() + 45000; while (Date.now() < end) { if (await predicate()) return; await new Promise(resolve => setTimeout(resolve, 100)); } throw new Error(stage); };
  const button = (name, root = document) => [...root.querySelectorAll("button")].find(node => node.textContent.trim() === name);
  const activate = async (name, root = document) => { const node = button(name, root); check(node && !node.disabled, `enabled:${name}`); node.focus(); node.click(); await settle(); };
  const navigate = async route => { history.pushState({ haeryongReaderContext: crypto.randomUUID() }, "", route); dispatchEvent(new PopStateEvent("popstate")); await settle(); };
  const control = async (action, value = {}) => { const response = await fetch(C.control_origin + "/__maintenance_probe", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ action, ...value }) }); check(response.ok, "local-probe-control"); return response.json(); };
  const panel = () => document.querySelector("section.maintenance-screen");
  const articles = () => [...(panel()?.querySelectorAll("article.maintenance-finding") ?? [])];
  const row = id => articles().find(node => node.getAttribute("aria-label") === "발견 항목 " + id);
  const field = (node, name) => [...node.querySelectorAll("dl.maintenance-facts > div")].find(entry => entry.querySelector("dt")?.textContent === name)?.querySelector("dd > span.semantic-detail-value");
  function exactField(node, name, expected) {
    const span = field(node, name);
    check(span && span.childElementCount === 0, "escaped-field:" + name);
    const kind = expected === undefined ? "missing" : expected === null ? "null" : typeof expected;
    check(span.dataset.valueKind === kind, "field-kind:" + name);
    if (expected !== null && typeof expected === "object") check(same(JSON.parse(span.textContent), expected), "complete-json:" + name);
    else check(span.textContent === (expected === undefined ? "기록 없음" : expected === null ? "없음(null)" : String(expected)), "complete-text:" + name);
  }
  async function jobRows(job) {
    await navigate("/maintenance/jobs/" + encodeURIComponent(job.job_id));
    stage = "job-mount:" + job.job_id;
    await wait(() => panel()?.textContent.includes(job.job_id) && panel()?.querySelector('[aria-label="실제 스캔 진행 및 캡처 정책"]') && !panel()?.textContent.includes("작업을 확인하는 중…"));
    if (job.expected_semantic) {
      const report = panel().querySelector('[aria-label="실제 스캔 진행 및 캡처 정책"]');
      const semantic = JSON.parse(field(report, "semantic").textContent);
      check(Object.entries(job.expected_semantic).every(([key, value]) => same(semantic[key], value)), "original-partial-job-semantic-counts");
    }
    const end = Date.now() + 120000;
    while (!job.findings.every(expected => row(expected.finding_id))) {
      check(Date.now() < end, "selected-findings-enumeration-deadline");
      const retry = button("다시 불러오기", panel());
      if (retry && !retry.disabled) {
        const before = articles().map(node => node.textContent);
        await activate("다시 불러오기", panel());
        await wait(() => !panel()?.textContent.includes("불러오는 중…"));
        check(before.every(value => articles().some(node => node.textContent === value)), "cursor-fault-retains-earlier-full-rows");
        log("maintenance.explicit-cursor-retry-retains-rows");
      } else {
        const more = button("더 불러오기", panel());
        if (more && !more.disabled) { more.click(); await settle(); }
        else await new Promise(resolve => setTimeout(resolve, 100));
      }
    }
    for (const expected of job.findings) {
      const node = row(expected.finding_id);
      check(node, "exact-finding-row");
      for (const detail of node.querySelectorAll("details")) detail.open = true;
      for (const name of ["project_id", "document_id", "version_id", "page_path", "region_id", "target_set_id", "captured_config_revision", "permissions_revision", "reason", "applied_ref", "evidence", "proposal", "evidence_fingerprint", "etag"]) exactField(node, name, expected[name]);
      check(node.querySelector("strong")?.textContent === (expected.origin === "ai" ? "AI가 추가한 관리 정보" : "결정적 구조 검사"), "permanent-origin-label");
      const prose = [...node.querySelectorAll("p.maintenance-prose")];
      check(prose[0]?.textContent === expected.rationale && prose[1]?.textContent === (expected.uncertainty === "" ? "빈 기록(없음)" : expected.uncertainty), "complete-rationale-uncertainty");
      check(!node.querySelector("script,iframe,object,embed"), "finding-detail-is-text");
      const proposal = expected.proposal;
      if (expected.kind === "freshness_assessment") {
        check(node.textContent.includes("실제 평가 범위:") || node.textContent.includes("보관된 평가 형식/범위를 해석할 수 없습니다"), "scope-not-inferred-from-outer-null");
        if (proposal && typeof proposal === "object" && proposal.scope) {
          exactField(node, "scope", proposal.scope); exactField(node, "state", proposal.state); exactField(node, "assessed_at", proposal.assessed_at);
          if (expected.status === "proposed") check(node.textContent.includes("최신성 평가 후보 · 적용된 최신성 아님"), "candidate-not-published");
        }
        const canonical = C.canonical_by_finding?.[expected.finding_id];
        if (canonical) {
          await activate("동일 대상/범위의 적용된 평가와 이력 보기", node);
          stage = "actual-target-bound-published-history";
          await wait(() => node.querySelector('[aria-label="적용된 최신성 및 저장 이력"]')?.textContent.includes(canonical.applicable.target_set_id));
          const history = node.querySelector('[aria-label="적용된 최신성 및 저장 이력"]');
          const a = canonical.applicable;
          check(history.textContent.includes(a.captured_at ?? "없음") && history.textContent.includes(a.assessed_at ?? "없음") && history.textContent.includes("재평가 필요 " + (a.reassessment_required ? "예" : "아니오")), "separate-real-times-and-reassessment");
          const scope = a.scope;
          const scopeText = scope.kind === "version" ? "버전 전체" : scope.kind === "page" ? "페이지 " + scope.page_path : "영역 " + scope.region_id + "(" + scope.page_path + ")";
          const badges = { aligned_within_scope: "범위 내 정렬", outdated_against_target: "대비 구식", reassessment_needed: "재평가 필요", unable_to_verify: "확인 불가", not_assessed: "미평가" };
          check(history.textContent.includes("소스 의도 " + (a.source_intent ?? "없음")) && history.textContent.includes("범위 " + scopeText), "exact-published-source-intent-and-scope");
          check(history.textContent.includes("기록된 배지: " + (badges[a.status] ?? a.status)), "exact-recorded-freshness-badge");
          check(history.textContent.includes("커버리지 " + a.coverage.assessed + "/" + a.coverage.eligible + "(완전 " + (a.coverage.complete ? "참" : "거짓") + ", 단위 " + a.coverage.unit_kind + ")"), "exact-recorded-coverage-denominator");
          if (a.coverage.units_url) check(!history.textContent.includes("커버리지 단위 집합이 없습니다(대상 집합 없음)"), "existing-fixed-coverage-not-falsely-absent");
          for (const expectedRecord of canonical.history) {
            stage = "actual-retained-assessment-id";
            await wait(() => history.textContent.includes(expectedRecord.assessment_id));
            const detail = [...history.querySelectorAll("details")].find(n => n.querySelector("summary")?.textContent.includes(expectedRecord.assessment_id));
            check(detail, "exact-published-history-row"); detail.open = true;
            const value = detail.querySelector("span.semantic-detail-value");
            check(value && same(JSON.parse(value.textContent), expectedRecord), "exact-complete-history-dto");
          }
          log("maintenance.new-consumer-published-scope-history");
        }
      }
      if (["new_document_proposal", "new_version_proposal"].includes(expected.kind)) check(node.textContent.includes("미게시 콘텐츠 제안") && !node.querySelector("a[href*='draft']"), "content-not-published");
      if (expected.kind === "topic_assignment") check(node.textContent.includes("작성된 본문의 주제·내용 커버리지로 간주하지 않습니다"), "topic-not-authored-body");
      if (expected.kind === "related_explanation") check(node.textContent.includes("주제적 유사성은 사용 관계나 의존성을 뜻하지 않습니다"), "analogy-not-used-by");
      check(![...node.querySelectorAll("button")].some(b => /^(승인|거절|고정|실행 취소|재평가|초안 생성|게시)$/.test(b.textContent.trim()) && !b.disabled), "no-future-action-execution");
    }
    log("maintenance.exact-typed-finding-details:" + job.job_id);
    const navigation = C.navigation_from_finding;
    if (navigation && job.findings.some(item => item.finding_id === navigation.finding_id)) {
      await activate("정확한 관계 대상 열기", row(navigation.finding_id));
      const actual = new URL(location.href), expected = new URL(navigation.target.route, location.origin);
      check(actual.pathname === expected.pathname && actual.search === expected.search && actual.hash === expected.hash, "actual-finding-target-navigation");
      await reader(navigation.target, "/maintenance/jobs/" + job.job_id, true);
      log("maintenance.actual-exact-target-and-safe-history-return");
    }
  }
  async function reader(target, returnRoute = null, alreadyNavigated = false) {
    if (!alreadyNavigated) await navigate(target.route);
    stage = "real-reader-frame";
    await wait(() => { const viewer = document.querySelector("section.reader-content"); return viewer?.querySelector("iframe.reader-frame") && !viewer.querySelector('[role="status"],[role="alert"]'); });
    await activate("근거 패널");
    document.querySelector("#reader-links-tab")?.click();
    await wait(() => document.querySelector("[data-management-links]"));
    check(document.querySelector('[aria-label="Authored references"]') && document.querySelector('[aria-label="Backlinks"]'), "authored-and-backlinks-separate");
    const links = document.querySelector("[data-management-links]");
    if (target.effects) for (const effect of target.effects) {
      stage = "reader-exact-overlay:" + effect.overlay_id;
      // Relation and topic cards both expose their complete stored DTO in
      // details. Topic cards intentionally have no relation-only data attribute.
      const findEffect = () => [...links.querySelectorAll("li")].find(node => {
        const detail = node.querySelector("details > pre");
        if (!detail) return false;
        try {
          const value = JSON.parse(detail.textContent);
          return value.overlay_id === effect.overlay_id && value.kind === effect.kind;
        } catch { return false; }
      });
      await wait(() => findEffect());
      const effectNode = findEffect();
      check(effectNode.textContent.includes("AI 추가") && effectNode.textContent.includes(effect.decision_actor), "origin-separate-from-decision-actor");
      const details = effectNode.querySelector("details"); check(details, "actual-overlay-detail"); details.open = true;
      check(same(JSON.parse(details.querySelector("pre").textContent), effect), "complete-original-effect-dto");
    }
    log("reader.actual-frame-and-management-separation");
    if (returnRoute) { history.back(); await wait(() => location.pathname === returnRoute); }
    else await navigate("/maintenance");
    await wait(() => !document.querySelector("iframe.reader-frame"));
  }
  window.addEventListener("error", () => { pageError = true; });
  window.addEventListener("unhandledrejection", () => { pageError = true; });
  try {
    await wait(() => document.querySelector('.service-state[data-state="verified"]'));
    if (native) check(window.__haeryongNativeTransport === "HAEYONG_NATIVE_TRANSPORT_BOUND", "actual-native-transport");
    for (const job of C.jobs) await jobRows(job);
    if (C.late_jobs) {
      await control("hold", { job_id: C.late_jobs.old });
      await navigate("/maintenance/jobs/" + C.late_jobs.old);
      stage = "genuine-old-job-response-held";
      await wait(async () => (await control("state")).held);
      const newer = C.jobs.find(job => job.job_id === C.late_jobs.new);
      await jobRows(newer);
      const before = articles().map(node => node.textContent);
      await control("release"); await wait(async () => (await control("state")).released); await settle();
      check(same(before, articles().map(node => node.textContent)), "late-old-job-cannot-regress-new-panel");
      log("maintenance.actual-old-response-late-guard");
    }
    for (const target of C.readers) await reader(target);
    if (C.settings_read) {
      await navigate("/settings/maintenance"); stage = "settings-categories";
      await wait(() => document.querySelector("[data-action-policy] select"));
      const settings = document.querySelector("[data-action-policy]");
      for (const name of ["backlink_index_repair", "related_explanation", "topic_assignment", "freshness_assessment", "content_generation"]) {
        const select = settings.querySelector('select[aria-label="' + name + '"]');
        check(select, "actual-settings-category:" + name);
        check(same([...select.options].map(o => o.value), name === "content_generation" ? ["disabled", "propose", "draft"] : ["disabled", "propose", "auto"]), "exact-setting-enums");
      }
      check(!settings.textContent.includes("auto-publish"), "no-auto-publish-setting");
      log("settings.actual-category-read-only");
      if (C.settings_expected_revision !== undefined) check(settings.textContent.includes("리비전 " + C.settings_expected_revision), "settings.post-restart-same-revision");
      if (C.settings_restriction) {
        const select = settings.querySelector('select[aria-label="related_explanation"]');
        if (select.value !== "auto") {
          rows.push({ name: "settings.restriction.current-not-auto", status: "not_run", reason: "Actual current mode is not auto; no widening or replacement fixture." });
        } else {
          const set = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value").set;
          set.call(select, "propose"); select.dispatchEvent(new Event("change", { bubbles: true })); await settle();
          await control("settings-conflict-once"); await activate("영향 미리보기", settings);
          await wait(() => settings.querySelector('[role="alert"]')?.textContent.includes("로컬 입력은 유지됩니다"));
          check(select.value === "propose", "settings-conflict-preserves-local-restriction");
          await activate("현재 구성 다시 읽기 (입력 유지)", settings);
          await wait(() => !button("현재 구성 다시 읽기 (입력 유지)", settings)?.disabled);
          check(select.value === "propose", "settings-reload-preserves-input");
          await activate("영향 미리보기", settings);
          await wait(() => settings.querySelector('[aria-label="정책 영향 미리보기"]'));
          await activate("미리보기 취소", settings);
          check(!settings.querySelector('[aria-label="정책 영향 미리보기"]') && select.value === "propose", "real-policy-preview-cancel-no-commit");
          await activate("영향 미리보기", settings);
          await wait(() => settings.querySelector('[aria-label="정책 영향 미리보기"]'));
          const confirmation = settings.querySelector('[aria-label="정책 영향 미리보기"] input[type="checkbox"]');
          check(confirmation && !confirmation.checked, "explicit-policy-confirmation-required");
          confirmation.click(); await settle(); await activate("확인한 정책 적용", settings);
          await wait(async () => (await control("state")).settings_committed);
          await wait(() => !button("현재 구성 다시 읽기 (입력 유지)", settings)?.disabled && select.value === "propose");
          log("settings.real-revisioned-restriction-commit");
        }
      }
    }
    if (C.disconnect) {
      await jobRows(C.jobs[0]); const before = articles().map(node => node.textContent);
      await control("disconnected", { enabled: true }); await activate("목록/작업 새로고침");
      await wait(() => panel()?.querySelector('[role="alert"]'));
      check(same(before, articles().map(node => node.textContent)), "disconnect-retains-confirmed-rows");
      await control("disconnected", { enabled: false }); await activate("목록/작업 새로고침");
      await wait(() => !panel()?.querySelector('[role="alert"]') && !panel()?.textContent.includes("불러오는 중…"));
      log("maintenance.controlled-disconnect-reconnect");
    }
    if (C.authority_loss) {
      await jobRows(C.jobs[0]); await control("denied", { enabled: true });
      await activate("목록/작업 새로고침"); stage = "authority-clears-protected-details";
      await wait(() => articles().length === 0 && !document.querySelector("[data-management-links]"));
      log("maintenance.controlled-401-clears-details");
      await control("denied", { enabled: false });
    }
    if (native) {
      check(C.native_window_reader, "native-window-reader-required");
      await navigate(C.native_window_reader.route); stage = "native-window-owned-final-frame";
      await wait(() => { const viewer = document.querySelector("section.reader-content"); return viewer?.querySelector("iframe.reader-frame") && !viewer.querySelector('[role="status"],[role="alert"]'); });
      // Leave one real session for the existing native windowClose owner.
    } else { await navigate("/maintenance"); await wait(() => !document.querySelector("iframe.reader-frame")); }
  } catch {
    rows.push({ name: "maintenance.failure." + stage, status: "fail" });
  }
  rows.push({ name: "page.error", status: pageError ? "fail" : "pass" }, { name: "page.done", status: "pass" });
  if (native) await window.haeryongNativeCall(701101, [{ Rows: rows }]);
  return rows;
})()
