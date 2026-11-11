(async () => {
  const C = /*__CONFIG__*/null;
  const rows = [];
  let stage = "bootstrap", pageError = false;
  const native = typeof window.haeryongNativeCall === "function";
  let frameObservation = null, announcedFrame = null, pageAnnouncement = null, readyChannel = null;
  const frameIdentity = value => value && typeof value === "object" ? Object.fromEntries(
    ["document_id", "version_id", "page_path", "render_instance_id"].filter(key => typeof value[key] === "string").map(key => [key, value[key]])) : null;
  const observedPost = MessagePort.prototype.postMessage;
  MessagePort.prototype.postMessage = function (value, ...rest) {
    if (value?.type === "reference.navigation") readyChannel = frameIdentity(value);
    return observedPost.call(this, value, ...rest);
  };
  addEventListener("message", event => {
    const frame = document.querySelector("section.reader-content iframe.reader-frame");
    if (event.source === frame?.contentWindow && event.data?.type === "viewer.loaded") {
      announcedFrame = event.source; pageAnnouncement = frameIdentity(event.data); readyChannel = null;
    }
  });
  const frameReady = target => {
    const viewer = document.querySelector("section.reader-content"), frame = viewer?.querySelector("iframe.reader-frame");
    const route = new URL(target.route, location.origin), parts = route.pathname.split("/");
    const expected = { document_id: target.document_id ?? parts[2], version_id: target.version_id ?? parts[4], page_path: route.searchParams.get("page") };
    const rect = frame?.getBoundingClientRect();
    let visible = !!rect && rect.width > 0 && rect.height > 0;
    for (let node = frame; node; node = node.parentElement) {
      const style = getComputedStyle(node);
      if (style.display === "none" || style.visibility !== "visible" || Number(style.opacity) === 0) visible = false;
    }
    const current = !!frame && frame.isConnected && announcedFrame === frame.contentWindow;
    const matching = current && expected.page_path !== null && pageAnnouncement && readyChannel &&
      Object.entries(expected).every(([key, value]) => pageAnnouncement[key] === value && readyChannel[key] === value) &&
      typeof pageAnnouncement.render_instance_id === "string" && pageAnnouncement.render_instance_id.length > 0 &&
      typeof readyChannel.render_instance_id === "string" && readyChannel.render_instance_id.length > 0 &&
      pageAnnouncement.render_instance_id === readyChannel.render_instance_id;
    frameObservation = { iframe_connected: !!frame?.isConnected, iframe_DOM_visible: visible,
      requested_document_id: expected.document_id, requested_version_id: expected.version_id,
      requested_page: expected.page_path, selected_page: current ? readyChannel?.page_path ?? null : null,
      viewer_loaded: current ? pageAnnouncement : null, reference_navigation: readyChannel, exact_binding_matches: !!matching };
    // Keep the original frame/status/alert gate and strengthen it with the
    // adopted Task03 current-frame/channel identity check; no fallback.
    return !!frame && !viewer.querySelector('[role="status"],[role="alert"]') && visible && !!matching;
  };
  // Retain only the public route category and401 status, never headers/body.
  const authorityHTTP = [], observedFetch = window.fetch;
  window.fetch = async function(input, init) {
    const response = await observedFetch.call(this, input, init);
    const path = new URL(input instanceof Request ? input.url : String(input), location.href).pathname;
    if ((path === "/api/v1/session" || path === "/api/v1/capabilities") && response.status === 401) authorityHTTP.push({ path, status: 401 });
    return response;
  };
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

  const receiptRoot = operationId => {
    // Current receipt call sites explicitly recover on /maintenance. Inline
    // finding/effect views and another screen are not this recovery view.
    const views = [...document.querySelectorAll("section.maintenance-screen > [data-maintenance-recovery]")];
    check(views.length <= 1, "task05-unique-maintenance-recovery-view:" + views.length);
    const matches = views.length ? [...views[0].querySelectorAll(":scope > div")]
      .filter(node => [...node.querySelectorAll(":scope > p")].some(p => p.textContent === "작업 " + operationId)) : [];
    check(matches.length <= 1, "task05-unique-operation-receipt:" + matches.length);
    return matches[0] ?? null;
  };
  const retainedRequest = item => {
    const r = item.request;
    check(r.operation === "overlayUndo" || r.operation === "topicAssignmentSet", "task05-closed-stale-intent-kind");
    return { kind: r.operation === "overlayUndo" ? "undo" : "topic", id: r.target, etag: r.if_match,
      ...(r.operation === "topicAssignmentSet" ? { body: r.body } : {}) };
  };
  async function task05Actions() {
    const plan = C.task05_actions;
    check(plan.schema === "haeryong.task05-finite-actions/v1" && plan.preparation_only === false, "task05-exact-opt-in");
    check(plan.controlled_fixture === true && C.settings_read === false && C.settings_restriction === false, "task05-controlled-not-genuine-mutation");
    for (const item of plan.cases) {
      const ui = item.ui;
      check(ui && ui.route && ui.scope_selector && ui.button_label, "task05-adopted-hooks-required");
      await navigate(ui.route);
      stage = "task05-action:" + item.id;
      await wait(() => document.querySelector(ui.scope_selector));
      if (ui.reload_before) {
        check(ui.reload_before === true && typeof ui.reload_scope_selector === "string" &&
          ui.reload_label === "현재 상태 불러오기 · 입력 유지", "task05-explicit-current-reload-hook");
        await activate(ui.reload_label, document.querySelector(ui.reload_scope_selector));
        check(Array.isArray(ui.reload_expected_text) && ui.reload_expected_text.length > 0, "task05-current-reload-visible-revision-oracle");
        for (const text of ui.reload_expected_text) await wait(() => document.querySelector(ui.scope_selector)?.textContent.includes(text));
      }
      if (ui.acknowledge_before) {
        await activate("결과 확인 · 새 결정 준비", document.querySelector(ui.scope_selector));
      }
      await control("task05-arm", { case_id: item.id });
      await activate(ui.button_label, document.querySelector(ui.scope_selector));
      if (ui.dialog_label) {
        check(ui.dialog_selector, "task05-exact-dialog-hook");
        await wait(() => [...document.querySelectorAll(ui.dialog_selector)].some(node => node.getAttribute("aria-label") === ui.dialog_label));
        const dialog = [...document.querySelectorAll(ui.dialog_selector)].find(node => node.getAttribute("aria-label") === ui.dialog_label);
        for (const field of ui.fields ?? []) {
          const label = [...dialog.querySelectorAll("label")].find(node => [...node.childNodes].filter(child => child.nodeType === Node.TEXT_NODE).map(child => child.textContent).join("").trim() === field.label);
          const input = label?.control ?? label?.querySelector("input,select");
          check(input && !input.disabled, "task05-real-dialog-control:" + field.label);
          if (field.kind === "checkbox") {
            check(input instanceof HTMLInputElement && input.type === "checkbox" && typeof field.value === "boolean", "task05-real-checkbox");
            if (input.checked !== field.value) input.click();
            check(input.checked === field.value, "task05-confirmed-checkbox");
          } else {
            check(field.kind === "input" || field.kind === "select", "task05-closed-field-kind");
            const type = field.kind === "select" ? HTMLSelectElement : HTMLInputElement;
            check(input instanceof type, "task05-real-input-kind");
            Object.getOwnPropertyDescriptor(type.prototype, "value").set.call(input, field.value);
            input.dispatchEvent(new Event(field.kind === "select" ? "change" : "input", { bubbles: true }));
            check(input.value === field.value, "task05-confirmed-exact-scope-field");
          }
        }
        for (const text of ui.dialog_expected_text ?? []) check(dialog.textContent.includes(text), "task05-dialog-exact-captured-scope");
        check(ui.confirm_label, "task05-explicit-confirmation-hook");
        await activate(ui.confirm_label, dialog);
      }
      await wait(async () => (await control("task05-state")).responses >= 1);
      const observation = await control("task05-state");
      if (item.drop_ack_once) {
        check(item.required_deliveries === 1 && item.max_deliveries === 1 && item.require_receipt_get === true, "task05-get-only-recovery-slot");
        if (item.id === "B1-all-linked-undo-lostack") {
          await wait(async () => {
            const state = await control("task05-state");
            stage = "task05-B1-automatic-receipt-read-failed-before-recovery";
            return state.case_id === item.id && state.operation_id === observation.operation_id &&
              state.receipt_gate?.blocked === 1 && state.receipt_gate.released === false;
          });
          // Observe the original action's settled UNKNOWN before disposing it.
          // Its enabled recovery button is emitted after #run's finally clears busy.
          await wait(() => {
            const scopes = [...document.querySelectorAll(ui.scope_selector)];
            check(scopes.length <= 1, "task05-B1-unique-original-action:" + scopes.length);
            const scope = scopes[0], status = scope?.querySelector(':scope > [role="status"]');
            const recover = status?.querySelector('button[data-maintenance-action="recover"]');
            const retained = scope?.querySelector(":scope > [data-maintenance-intent] pre");
            stage = "task05-B1-original-action-settled-unknown";
            if (!scope?.querySelector(':scope > [role="alert"]') || !status?.textContent.includes(ui.pending_text) ||
                !recover || recover.disabled || !retained) return false;
            const intent = JSON.parse(retained.textContent);
            check(intent.operation_id === observation.operation_id && same(intent.request, retainedRequest(item)),
              "task05-B1-original-unknown-intent-preserved");
            return true;
          });
        }
        if (ui.recovery_route) await navigate(ui.recovery_route);
        check(ui.pending_text && ui.recover_label, "task05-unknown-and-same-receipt-hooks");
        await wait(() => {
          const receipt = receiptRoot(observation.operation_id);
          stage = "task05-same-operation-pending-visible";
          return receipt?.textContent.includes(ui.pending_text);
        });
        if (item.id === "B1-all-linked-undo-lostack") {
          await control("task05-receipt-recover", { case_id: item.id, operation_id: observation.operation_id });
        }
        await activate(ui.recover_label, receiptRoot(observation.operation_id));
      }
      await wait(async () => (await control("task05-state")).responses === item.required_deliveries);
      check(Array.isArray(ui.expected_text) && ui.expected_text.length > 0, "task05-actual-visible-result-oracle-required");
      for (const text of ui.expected_text) await wait(() => {
        const scope = ui.outcome_scope === "operation_receipt"
          ? receiptRoot(observation.operation_id) : document.querySelector(ui.scope_selector);
        stage = ui.outcome_scope === "operation_receipt"
          ? "task05-same-operation-completed-receipt-visible" : "task05-visible-action-result";
        return scope?.textContent.includes(text);
      });
      if (item.interleave) {
        check(item.expected_status === 412 && ui.verify_retained_request === true, "task05-real-stale-error-oracle");
        const scope = document.querySelector(ui.scope_selector);
        const retained = JSON.parse(scope.querySelector("[data-maintenance-intent] pre").textContent);
        check(retained.operation_id === observation.operation_id && same(retained.request, retainedRequest(item)), "task05-original-stale-intent-preserved");
        check(scope.querySelector('[role="alert"]')?.textContent.includes("412 PRECONDITION_FAILED") &&
          !scope.querySelector("[data-maintenance-outcome]"), "task05-error-not-success");
      }
      for (const label of ui.disabled_labels ?? []) {
        const scope = document.querySelector(ui.scope_selector);
        check(scope, "task05-visible-current-disabled-scope");
        const node = button(label, scope);
        check(!node || node.disabled, "task05-unavailable-control-not-enabled:" + label);
      }
      await control("task05-verify", { case_id: item.id });
      log("task05.actual-action-and-public-oracle:" + item.id);
    }
    const readers = C.task05_readers;
    check(Array.isArray(readers) && readers.length > 0, "task05-real-render-required");
    for (let i = 0; i < readers.length; i++) {
      await navigate(readers[i].route);
      stage = "task05-real-current-frame";
      await wait(() => frameReady(readers[i]));
      rows.push({ name: "task05.actual-current-frame", status: "pass", detail: { ...frameObservation } });
      // Native leaves only the last authorized page to the existing windowClose
      // owner. Browser closes EVERY real tuple before its separate no-open denial.
      if (!native || i < readers.length - 1) {
        await navigate("/maintenance");
        await wait(() => !document.querySelector("iframe.reader-frame"));
        await control("task05-render-closed");
      }
    }
    if (!native && C.task05_authority) {
      check(!document.querySelector("iframe.reader-frame"), "task05-no-open-before-authority-loss");
      const authority = C.task05_authority;
      await control("hold", { job_id: authority.job_id });
      await navigate(authority.route);
      await wait(async () => (await control("state")).held);
      await control("denied", { enabled: true });
      await wait(() => !document.querySelector('.service-state[data-state="verified"]'));
      await control("release");
      await wait(async () => (await control("state")).released);
      for (const selector of authority.cleared_selectors) check(!document.querySelector(selector), "task05-late-protected-response-cleared");
      check(authorityHTTP.length > 0 && !document.querySelector("iframe.reader-frame"), "task05-actual-401-no-dangling-render");
      log("task05.authority-era-late-response-with-zero-open");
      await control("denied", { enabled: false });
    }
  }
  window.addEventListener("error", () => { pageError = true; });
  window.addEventListener("unhandledrejection", () => { pageError = true; });
  try {
    await wait(() => document.querySelector('.service-state[data-state="verified"]'));
    if (native) check(window.__haeryongNativeTransport === "HAEYONG_NATIVE_TRANSPORT_BOUND", "actual-native-transport");
    await task05Actions();
  } catch {
    rows.push({ name: "task05.failure." + stage, status: "fail", detail: frameObservation });
  }
  rows.push({ name: "page.error", status: pageError ? "fail" : "pass" }, { name: "page.done", status: "pass" });
  if (native) await window.haeryongNativeCall(701101, [{ Rows: rows }]);
  return rows;
})()
