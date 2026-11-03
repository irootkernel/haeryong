async page => {
  const input = INPUT, cases = [], mutations = [], providerReads = [];
  let stage = 'initial verified shell';
  try {
  let generation = true, questions = true;
  const api = async path => page.evaluate(async path => {
    const response = await fetch('/api/v1/' + path, {
      headers: {Authorization: 'Bearer ' + localStorage.getItem('haeryong.owner-session.v1')},
    });
    if (!response.ok) throw Error('fixture read refused: ' + path + ' ' + response.status);
    return (await response.json()).data;
  }, path);
  await page.goto(input.origin + '/library');
  await page.waitForFunction(() => document.querySelector('[data-state="verified"]'));
  const capabilities = await api('capabilities'), session = await api('session');
  let runtime = capabilities.runtime_generation, sessionId = session.session_id;
  const project = capabilities.unfiled_project_id;
  const draft = {
    draft_id: '11111111-1111-4111-8111-111111111111', project_id: project,
    document_id: input.document, base_version_id: input.version,
    job_id: '22222222-2222-4222-8222-222222222222', state: 'ready', intent: 'draft',
    revision: 1, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    payload_sha256: 'a'.repeat(64), metadata_sha256: null, published_version_id: null,
    failure_reason: null, etag: '"controlled-ready-draft-1"',
  };
  await page.route(input.origin + '/api/v1/**', async route => {
    const request = route.request(), path = request.url().slice(input.origin.length).split('?')[0];
    if (request.method() !== 'GET' && request.method() !== 'HEAD' &&
        (/\/generations$|\/questions$|\/drafts\/[^/]+$|\/jobs\/[^/]+\/(cancel|resume)$/.test(path))) {
      mutations.push({method: request.method(), path});
      await route.abort('blockedbyclient'); return;
    }
    if (path === '/api/v1/providers') {
      providerReads.push('controlled unavailable response; no native provider probe');
      await route.fulfill({status: 503, json: {error: {code: 'UNAVAILABLE', message: 'Controlled status refusal', retryable: true, details: {}}}});
      return;
    }
    if (path === '/api/v1/capabilities' && request.method() === 'GET') {
      const response = await route.fetch(), body = await response.json();
      body.data.features.generation = generation;
      body.data.features.questions = questions;
      body.data.runtime_generation = runtime;
      await route.fulfill({response, json: body}); return;
    }
    if (path === '/api/v1/session' && request.method() === 'GET' && sessionId !== session.session_id) {
      const response = await route.fetch(), body = await response.json();
      body.data.session_id = sessionId;
      await route.fulfill({response, json: body}); return;
    }
    if (path === '/api/v1/drafts' && request.method() === 'GET') {
      await route.fulfill({json: {data: {items: [draft], next_cursor: null}}}); return;
    }
    if (path === '/api/v1/drafts/' + draft.draft_id && request.method() === 'GET') {
      await route.fulfill({json: {data: draft}}); return;
    }
    await route.continue();
  });
  const confirmed = async (restore = async () => {}) => {
    const sessionRead = page.waitForResponse(r => r.url() === input.origin + '/api/v1/session' && r.status() === 200);
    const capabilityRead = page.waitForResponse(r => r.url() === input.origin + '/api/v1/capabilities' && r.status() === 200);
    await restore();
    await page.evaluate(() => window.dispatchEvent(new Event('focus')));
    await Promise.all([sessionRead, capabilityRead]);
    await page.waitForFunction(() => document.querySelector('[data-state="verified"]'));
  };
  const enabled = async locator => {
    await locator.waitFor({state: 'visible'});
    await page.waitForFunction(element => !element.disabled, await locator.elementHandle());
  };
  const journalSnapshot = () => page.evaluate(() => Object.fromEntries(
    Object.keys(localStorage).filter(key => /^haeryong\.(generation|questions|draft-action)\.v1:/.test(key))
      .sort().map(key => [key, localStorage.getItem(key)])));
  const cycle = async (name, controls, draftValue) => {
    stage = name + ' connected';
    const before = await draftValue(), journals = await journalSnapshot();
    const connected = await Promise.all(controls.map(async control => !await control.isDisabled()));
    await page.context().setOffline(true);
    try {
      stage = name + ' disconnected';
      await page.evaluate(() => window.dispatchEvent(new Event('focus')));
      await page.waitForFunction(() => document.querySelector('[data-state="degraded"]'));
      if (name === 'Ask Submit') await page.getByRole('button', {name: '다시 열기', exact: true}).waitFor();
      const mounted = await Promise.all(controls.map(async control => await control.count() === 1));
      const readerState = name === 'Ask Submit' ? await page.evaluate(() => ({
        submit_exists: !!document.getElementById('ask-submit'),
        text_exists: !!document.getElementById('ask-text'),
        text_value: document.getElementById('ask-text')?.value ?? null,
        context_open: document.getElementById('reader-context-panel')?.open ?? null,
        tab_selected: document.getElementById('reader-ask-tab')?.getAttribute('aria-selected') ?? null,
        frame_count: document.querySelectorAll('iframe').length,
        headings: [...document.querySelectorAll('h1,h2')].map(element => element.textContent),
      })) : undefined;
      const disabled = await Promise.all(controls.map(async control => await control.count() === 1 && await control.isDisabled()));
      const retained = before === await draftValue();
      stage = name + ' reconnect';
      await confirmed(() => page.context().setOffline(false));
      const reenabled = await Promise.all(controls.map(async control => await control.count() === 1 && !await control.isDisabled()));
      const after = await journalSnapshot();
      cases.push({name, connected_enabled: connected, disconnected_mounted: mounted, disconnected_disabled: disabled,
        reader_state: readerState,
        reconnected_enabled: reenabled, draft_retained: retained && before === await draftValue(),
        journal_bytes_unchanged: JSON.stringify(journals) === JSON.stringify(after),
        confirmed_session_and_capabilities: true,
        passed: connected.every(Boolean) && disabled.every(Boolean) && reenabled.every(Boolean) &&
          retained && before === await draftValue() && JSON.stringify(journals) === JSON.stringify(after) &&
          (readerState === undefined || readerState.frame_count === 0 && await page.locator('iframe').count() === 0)});
    } finally { await page.context().setOffline(false); }
  };
  stage = 'Generation editor';
  await page.getByRole('button', {name: '설명 생성', exact: true}).click();
  await page.getByRole('combobox', {name: 'AI 제공 방식', exact: true}).selectOption('codex');
  const prompt = page.getByRole('textbox', {name: '요청', exact: true});
  await prompt.fill('Controlled unsent Generation draft 🌊');
  const submit = page.getByRole('button', {name: '생성 요청', exact: true});
  await enabled(submit);
  await cycle('Generation Submit', [submit], () => prompt.inputValue());
  stage = 'Generation capability withdrawal';
  generation = false; await confirmed();
  const unsupportedGeneration = await submit.isDisabled();
  generation = true; await confirmed(); await enabled(submit);
  const promptRetained = await prompt.inputValue() === 'Controlled unsent Generation draft 🌊';
  cases.push({name: 'Generation capability withdrawal', passed: unsupportedGeneration && promptRetained,
    controlled_capability: true, request_draft_unchanged: promptRetained});
  stage = 'Ready Draft fixture';
  await page.getByRole('button', {name: '초안 관리', exact: true}).click();
  await page.locator('.draft-choice').click();
  const selected = page.getByRole('region', {name: '선택한 초안', exact: true});
  await selected.getByLabel('결과 ZIP', {exact: true}).setInputFiles(input.zip);
  const publish = selected.getByRole('button', {name: '저장된 기준으로 발행', exact: true});
  const reject = selected.getByRole('button', {name: '초안 거부', exact: true});
  const replace = selected.getByRole('button', {name: '선택한 전체 결과로 교체', exact: true});
  await enabled(publish); await enabled(replace);
  const draftSelection = async () => JSON.stringify({
    file: await selected.getByLabel('결과 ZIP', {exact: true}).inputValue(),
    pinned_draft: (await selected.innerText()).includes(draft.draft_id),
    pinned_version: (await selected.innerText()).includes(input.version),
  });
  await cycle('Ready Draft publish/reject/replace', [publish, reject, replace], draftSelection);
  const pendingKey = `haeryong.draft-action.v1:${capabilities.library_id}:${session.principal_id}:${draft.draft_id}`;
  await page.evaluate(({key, draft, runtime}) => localStorage.setItem(key, JSON.stringify({
    key: '33333333-3333-4333-8333-333333333333', draftId: draft.draft_id,
    action: 'publish', etag: draft.etag, dispatchReady: false, runtimeGeneration: runtime,
  })), {key: pendingKey, draft, runtime: capabilities.runtime_generation});
  await selected.getByRole('button', {name: '초안 상태 확인', exact: true}).click();
  const recover = selected.getByRole('button', {name: '원래 초안 작업 복구', exact: true});
  await enabled(recover);
  const pendingRemainsDisabled = await publish.isDisabled() && await reject.isDisabled() && await replace.isDisabled();
  await cycle('Pending Draft original recovery', [recover], draftSelection);
  cases.push({name: 'Pending Draft excludes new actions', passed: pendingRemainsDisabled && await publish.isDisabled() && await reject.isDisabled() && await replace.isDisabled()});
  stage = 'Ask editor';
  await page.goto(input.route);
  const context = page.getByRole('button', {name: '근거 패널', exact: true});
  if (await context.getAttribute('aria-expanded') !== 'true') await context.click();
  await page.getByRole('tab', {name: 'Ask', exact: true}).click();
  const prepare = page.getByRole('button', {name: '현재 위치로 새 질문 준비', exact: true});
  await enabled(prepare); await prepare.click();
  await page.getByRole('combobox', {name: '질문 provider', exact: true}).selectOption('codex');
  const question = page.getByRole('textbox', {name: '질문 본문', exact: true});
  await question.fill('Controlled unsent pinned question 🌊');
  const ask = page.getByRole('button', {name: '질문 제출', exact: true});
  await enabled(ask);
  const questionDraft = async () => await question.count() === 1 ? JSON.stringify({
    text: await question.inputValue(),
    target: await page.getByLabel('캡처한 질문 대상', {exact: true}).innerText(),
    provider: await page.getByRole('combobox', {name: '질문 provider', exact: true}).inputValue(),
    mode: await page.getByRole('combobox', {name: '질문 원본 기준', exact: true}).inputValue(),
  }) : null;
  await cycle('Ask Submit', [ask], questionDraft);
  stage = 'Ask capability withdrawal';
  questions = false; await confirmed();
  await page.getByText('Ask: 소유자 질문 기능은 아직 제공되지 않습니다.', {exact: true}).waitFor();
  const unsupportedQuestion = await ask.count() === 0;
  questions = true; await confirmed(); await enabled(ask);
  cases.push({name: 'Ask capability withdrawal', passed: unsupportedQuestion && await question.inputValue() === 'Controlled unsent pinned question 🌊',
    controlled_capability: true, draft_retained: await question.inputValue() === 'Controlled unsent pinned question 🌊'});
  stage = 'Reader runtime replacement';
  runtime = '44444444-4444-4444-8444-444444444444'; await confirmed();
  await page.waitForFunction(() => !document.getElementById('ask-text'));
  cases.push({name: 'Changed runtime retires the unsent editor', passed: await question.count() === 0,
    controlled_capability_runtime: true});
  runtime = capabilities.runtime_generation; await confirmed();
  const reopenAsk = async () => {
    const toggle = page.getByRole('button', {name: '근거 패널', exact: true});
    if (await toggle.getAttribute('aria-expanded') !== 'true') await toggle.click();
    await page.getByRole('tab', {name: 'Ask', exact: true}).click();
    await enabled(prepare); await prepare.click();
    await question.fill('Controlled old-session draft');
  };
  await reopenAsk();
  stage = 'Reader session replacement';
  sessionId = '55555555-5555-4555-8555-555555555555'; await confirmed();
  await page.waitForFunction(() => !document.getElementById('ask-text'));
  cases.push({name: 'Changed owner session retires the unsent editor', passed: await question.count() === 0,
    controlled_session_identity: true});
  sessionId = session.session_id; await confirmed();
  stage = 'Sources current truth';
  const sourcePage = input.origin + '/settings/projects?project=' + encodeURIComponent(project);
  await page.goto(sourcePage);
  await page.getByRole('heading', {name: '프로젝트 설정 · 출처', exact: true}).waitFor();
  const sourceText = await page.locator('.source-settings-panel').innerText();
  const statusLink = page.getByRole('button', {name: 'AI 제공자 설정', exact: true});
  const hasLink = await statusLink.count() === 1;
  if (input.screenshot) await page.screenshot({path: input.screenshot, fullPage: true});
  let navigated = false;
  if (hasLink) {
    await statusLink.focus(); await page.keyboard.press('Enter');
    await page.getByRole('heading', {name: 'AI 제공자', exact: true}).waitFor();
    navigated = page.url() === input.origin + '/settings/ai';
  }
  cases.push({name: 'Sources current truth and explicit AI navigation',
    passed: !sourceText.includes('실제 제공자는 아직 연결되지 않았습니다') && hasLink && navigated &&
      sourceText.includes('원본을 수정하지 않습니다') && sourceText.includes('OS 읽기 격리 경계가 아닙니다'),
    obsolete_claim_absent: !sourceText.includes('실제 제공자는 아직 연결되지 않았습니다'),
    explicit_status_link: hasLink, keyboard_navigation: navigated,
    provider_status_requests: providerReads.length, provider_status_fixture: '503 interception; no native probe'});
  return {all_passed: cases.every(item => item.passed) && mutations.length === 0,
    cases, consequential_requests: mutations, provider_submissions: 0,
    classification: 'Actual compiled production React/shell/controller, normal disposable daemon/auth/import; read-only ready-Draft DTO and local pending-intent fixtures, controlled capability response/offline network; no Draft backend or provider qualification'};
  } catch (error) {
    return {all_passed: false, cases, consequential_requests: mutations, provider_submissions: 0,
      failed_stage: stage, error: String(error).slice(0, 1200),
      shell: await page.evaluate(() => ({phase: document.querySelector('[data-state]')?.getAttribute('data-state'),
        headings: [...document.querySelectorAll('h1,h2')].map(element => element.textContent)}))};
  }
}
