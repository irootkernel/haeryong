async page => {
  const input = INPUT;
  const assert = (ok, message) => { if (!ok) throw Error(message); };
  if (!page.privateIngress) {
    page.privateIngress = { opens: {}, events: [], ignoreKeys: new Set() };
    const state = page.privateIngress;
    state.track = target => target.on('response', async response => {
      const request = response.request();
      if (request.method() === 'POST' && new URL(response.url()).pathname === '/__haeryong/handoff') {
        state.events.push({ status: response.status(), origin: request.headers().origin || null, host: new URL(response.url()).host });
      }
      if (request.method() !== 'POST' || !new URL(response.url()).pathname.endsWith('/render-sessions') || response.status() !== 201) return;
      if (state.ignoreKeys.has(request.headers()['idempotency-key'])) return;
      const value = (await response.json()).data;
      state.opens[value.version_id] = { value, key: request.headers()['idempotency-key'] };
    });
    state.track(page);
  }
  const state = page.privateIngress;
  const route = fixture => input.base + '/documents/' + fixture.document + '/versions/' + fixture.version + '?page=first.html';
  const read = async (target, fixture, expectedOrigin, navigate = true) => {
    await target.goto(route(fixture));
    const viewer = target.frameLocator('iframe[title="결과 뷰어"]');
    await viewer.locator('#heading').waitFor({ timeout: 25000 });
    assert(await viewer.locator('#heading').innerText() === fixture.label + ' first', 'wrong version payload');
    await viewer.locator('#increment').click();
    assert(await viewer.locator('#increment').innerText() === '1', 'ordinary script interaction failed');
    const frame = target.frames().find(f => f !== target.mainFrame() && f.url().includes('first.html'));
    assert(frame, 'renderer frame not found');
    await frame.waitForFunction(() => window.moduleValue === 'module-running' && window.workerValue === 'worker-running' && window.rootAsset);
    const boundary = await frame.evaluate(() => {
      let dom = false, storage = false;
      try { parent.document.body; dom = true; } catch {}
      try { parent.localStorage; storage = true; } catch {}
      parent.postMessage({ type: 'management.delete', payload: { delete: true } }, '*');
      return { origin: location.origin, hostDOMDenied: !dom, hostStorageDenied: !storage,
        nativeAbsent: !(window.wails || window.runtime || window.haeryongNative), rootAsset: window.rootAsset, color: getComputedStyle(document.querySelector('#heading')).color };
    });
    assert(boundary.hostDOMDenied && boundary.hostStorageDenied && boundary.nativeAbsent, 'generated-frame authority separation failed');
    assert(boundary.origin !== new URL(input.base).origin, 'renderer shares management origin');
    if (expectedOrigin) assert(boundary.origin === new URL(expectedOrigin).origin, 'unexpected renderer delivery origin');
    assert(boundary.color === 'rgb(1, 2, 3)', 'ordinary stylesheet delivery failed');
    assert(boundary.rootAsset === 'asset-' + fixture.label.split(' ').pop(), 'root-relative asset changed version');
    if (navigate) {
      const created = target.waitForEvent('popup');
      await viewer.locator('#newtab').click();
      const popup = await created;
      try {
        await popup.frameLocator('iframe[title="결과 뷰어"]').locator('#second').waitFor();
        const opened = new URL(popup.url());
        assert(opened.origin === new URL(input.base).origin && opened.pathname === new URL(route(fixture)).pathname &&
          opened.searchParams.get('page') === 'nested/second.html' && opened.searchParams.get('page_query') === 'popup=1' &&
          opened.searchParams.get('page_fragment') === 'part', 'authored new-tab context or public Reader origin lost');
      } finally { await popup.close(); }
      await viewer.locator('#next').click();
      await target.frameLocator('iframe[title="결과 뷰어"]').locator('#second').waitFor();
      await target.waitForFunction(() => new URL(location.href).searchParams.get('page') === 'nested/second.html');
      const url = new URL(target.url());
      assert(url.searchParams.get('page_query') === 'scenario=2' && url.searchParams.get('page_fragment') === 'part', 'multi-page query/fragment lost');
    }
    return boundary;
  };
  if (input.phase === 'local' || input.phase === 'local-recovery') {
    await page.goto(input.base + '/health/live');
    if (input.phase === 'local') {
      await page.evaluate(token => localStorage.setItem('haeryong.owner-session.v1', token), input.token);
    } else {
      assert(await page.evaluate(() => localStorage.getItem('haeryong.owner-session.v1')) === input.token, 'local remembered credential changed');
    }
    const boundary = await read(page, input.fixture[0], input.expectedOrigins && input.expectedOrigins[0]);
    return { boundary, rememberedLocalOwner: true, multiPageNavigation: true, ordinaryModulesAndWorker: true, authoredNewTab: true };
  }
  if (input.phase === 'live') {
    state.token = await page.evaluate(() => localStorage.getItem('haeryong.owner-session.v1'));
    assert(state.token, 'normal pairing UI did not retain the remote credential');
    await page.waitForFunction(path => location.pathname === path, new URL(route(input.fixture[0])).pathname);
    assert(new URL(page.url()).pathname === new URL(route(input.fixture[0])).pathname, 'login did not return to the exact Reader');
    const first = await read(page, input.fixture[0], input.expectedOrigins[0]);
    state.child = await page.context().newPage();
    state.track(state.child);
    const second = await read(state.child, input.fixture[1], input.expectedOrigins[1]);
    assert(first.origin !== second.origin, 'two versions share an origin');
    const frame = page.frames().find(f => f !== page.mainFrame());
    const crossURL = new URL('/root-asset.txt?cross-port=1', input.expectedOrigins[1]).href;
    // Chromium hides a CORS-denied response from the page's response event.
    const blocked = page.waitForEvent('requestfailed', { predicate: request => request.url() === crossURL });
    const fetchDenied = await frame.evaluate(async target => {
      try { await fetch(target, { credentials: 'include' }); return false; } catch { return true; }
    }, crossURL);
    const browserDenied = await blocked;
    // Separately observe the server status with this browser context's real cookies.
    const crossResponse = await page.context().request.get(crossURL, { headers: { Origin: first.origin } });
    assert(fetchDenied && browserDenied.failure() && crossResponse.status() === 403, 'cross-port request used another renderer cookie');
    const cookies = await page.context().cookies(input.expectedOrigins);
    const selected = input.fixture.slice(0, 2).map(fixture => {
      const opened = state.opens[fixture.version];
      assert(opened, 'trusted UI Open observation missing');
      const name = 'hyr_view_' + fixture.version + '_' + opened.value.session_id;
      const cookie = cookies.find(c => c.name === name);
      assert(cookie && cookie.secure && cookie.httpOnly && cookie.sameSite === 'Strict' && cookie.path === '/', 'narrow cookie policy changed');
      return { name, secure: cookie.secure, httpOnly: cookie.httpOnly, sameSite: cookie.sameSite, path: cookie.path };
    });
    await page.getByRole('button', { name: '링크 복사', exact: true }).click();
    const copy = await page.getByLabel('복사할 링크', { exact: true }).inputValue();
    const copied = new URL(copy);
    assert(copied.origin === input.base && copied.pathname === new URL(route(input.fixture[0])).pathname && copied.searchParams.get('page') === 'nested/second.html', 'Copy link changed pinned target or external origin');
    await page.reload();
    await page.frameLocator('iframe[title="결과 뷰어"]').locator('#second').waitFor();
    assert(await page.evaluate(() => localStorage.getItem('haeryong.owner-session.v1')) === state.token, 'remembered reload lost credential');
    const opened = state.opens[input.fixture[0].version].value;
    const renewed = await page.evaluate(async ({ version, session }) => {
      const response = await fetch('/api/v1/versions/' + version + '/render-sessions/' + session + '/renew', {
        method: 'POST', headers: { Authorization: 'Bearer ' + localStorage.getItem('haeryong.owner-session.v1'), 'Content-Type': 'application/json' }, body: '{}' });
      return { status: response.status, data: (await response.json()).data };
    }, { version: input.fixture[0].version, session: opened.session_id });
    assert(renewed.status === 200 && renewed.data.session_id === opened.session_id, 'private lease renew failed');
    const cookieOnly = await page.evaluate(async () => (await fetch('/api/v1/session', { credentials: 'include' })).status);
    assert(cookieOnly === 401, 'renderer cookies authenticated the management endpoint');
    const unmapped = await page.evaluate(async version => {
      const headers = { Authorization: 'Bearer ' + localStorage.getItem('haeryong.owner-session.v1') };
      const bootstrap = await fetch('/api/v1/versions/' + version + '/render-bootstrap', { headers });
      const open = await fetch('/api/v1/versions/' + version + '/render-sessions', { method: 'POST',
        headers: { ...headers, 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID() }, body: '{}' });
      return { bootstrapStatus: bootstrap.status, bootstrap: (await bootstrap.json()).data,
        openStatus: open.status, error: (await open.json()).error };
    }, input.fixture[2].version);
    assert(unmapped.bootstrapStatus === 200 && !unmapped.bootstrap.renderer_available && unmapped.bootstrap.renderer_unavailable_reason === 'renderer_remote_ingress' && unmapped.openStatus === 503 && unmapped.error.details.reason === 'renderer_remote_ingress', 'unmapped version falsely advertised Reader delivery');
    state.saved = input.fixture.slice(0, 2).map(f => ({ version: f.version, ...state.opens[f.version] }));
    if (input.screenshot) await page.screenshot({ path: input.screenshot, fullPage: true });
    return { first, second, cookies: selected, crossPortBrowserBlocked: true, crossPortHTTPStatus: crossResponse.status(),
      copyLink: copy, authoredNewTabs: true, rememberedReload: true, renewStatus: renewed.status,
      cookieOnlyStatus: cookieOnly, unmappedStatus: unmapped.openStatus, unmappedReason: unmapped.error.details.reason };
  }
  if (input.phase === 'browser-boundaries') {
    const csp = await page.evaluate(async () => {
      const violations = [];
      const handler = event => violations.push({ directive: event.effectiveDirective, blocked: event.blockedURI });
      document.addEventListener('securitypolicyviolation', handler);
      const frame = document.createElement('iframe'); frame.name = 'qa-csp-negative'; frame.src = 'https://unconfigured.invalid/reader'; frame.hidden = true;
      const form = document.createElement('form'); form.method = 'POST'; form.action = 'https://unconfigured.invalid/handoff'; form.target = frame.name;
      document.body.append(frame, form); form.submit();
      try {
        const deadline = Date.now() + 10000;
        while (!(violations.some(v => v.directive === 'frame-src') && violations.some(v => v.directive === 'form-action'))) {
          if (Date.now() > deadline) throw Error('unconfigured frame/form CSP refusal was not observed');
          await new Promise(resolve => setTimeout(resolve, 20));
        }
        return violations;
      } finally { document.removeEventListener('securitypolicyviolation', handler); frame.remove(); form.remove(); }
    });
    const redirect = await page.context().newPage();
    let rawTopLevel;
    try {
      const raw = new URL('/nested/second.html?top=1#part', input.expectedOrigins[0]).href;
      const initial = redirect.waitForResponse(r => r.request().isNavigationRequest() && r.url() === raw.split('#')[0]);
      await redirect.goto(raw, { waitUntil: 'commit' });
      const initialResponse = await initial;
      assert(!initialResponse.headers()['haeryong-derivative'], 'top-level renderer returned transformed artifact bytes');
      await redirect.frameLocator('iframe[title="결과 뷰어"]').locator('#second').waitFor();
      const url = new URL(redirect.url());
      assert(url.origin === new URL(input.base).origin && url.pathname === new URL(route(input.fixture[0])).pathname &&
        url.searchParams.get('page') === 'nested/second.html' && url.searchParams.get('page_query') === 'top=1' && url.searchParams.get('page_fragment') === 'part', 'raw top-level redirect lost trusted context');
      rawTopLevel = { status: initialResponse.status(), exactContext: true };
    } finally { await redirect.close(); }
    return { unconfiguredCSP: csp, rawTopLevel, observedUIHandoffs: state.events };
  }
  if (input.phase === 'restart') {
    const observed = [];
    for (const saved of state.saved) {
      const replay = await page.evaluate(async ({ version, key }) => {
        const response = await fetch('/api/v1/versions/' + version + '/render-sessions', { method: 'POST',
          headers: { Authorization: 'Bearer ' + localStorage.getItem('haeryong.owner-session.v1'),
            'Content-Type': 'application/json', 'Idempotency-Key': key }, body: '{}' });
        return { status: response.status, data: (await response.json()).data };
      }, saved);
      assert(replay.status === 201 && JSON.stringify(replay.data) === JSON.stringify(saved.value), 'completed Open receipt changed after restart');
    }
    // A receipt replay does not construct a cold renderer. Reopen both real UI
    // selections before testing the old handoff against active listeners.
    const first = await read(page, input.fixture[0], input.expectedOrigins[0], false);
    const second = await read(state.child, input.fixture[1], input.expectedOrigins[1], false);
    for (const saved of state.saved) {
      const rejected = page.waitForResponse(r => r.request().method() === 'POST' && r.url().startsWith(saved.value.handoff.url));
      await page.evaluate(({ url, secret }) => {
        const frame = document.createElement('iframe'); frame.name = 'qa-dead-handoff'; frame.id = 'qa-dead-handoff'; frame.hidden = true;
        const form = document.createElement('form'); form.method = 'POST'; form.action = url + '?page=first.html'; form.target = frame.name; form.id = 'qa-dead-form';
        const input = document.createElement('input'); input.name = 'secret'; input.value = secret; form.append(input);
        document.body.append(frame, form); form.submit();
      }, saved.value.handoff);
      const oldStatus = (await rejected).status();
      await page.evaluate(() => { document.querySelector('#qa-dead-form').remove(); document.querySelector('#qa-dead-handoff').remove(); });
      assert(oldStatus === 401, 'old handoff status after restart: ' + oldStatus);
      observed.push({ version: saved.version, originalSession: saved.value.session_id, deadHandoffStatus: oldStatus });
    }
    for (const saved of state.saved) assert(state.opens[saved.version].value.session_id !== saved.value.session_id, 'Reader did not open a fresh session');
    return { replayedReceiptsUnchanged: true, observed, first, second, rememberedCredential: true };
  }
  if (input.phase === 'revoked') {
    const opened = state.opens[input.fixture[0].version].value;
    const denied = await page.evaluate(async ({ token, version, session }) => {
      const headers = { Authorization: 'Bearer ' + token, 'Content-Type': 'application/json' };
      const paths = [
        { path: '/api/v1/versions/' + version + '/metadata?kind=normalized' },
        { path: '/api/v1/versions/' + version + '/render-sessions', method: 'POST', key: true },
        { path: '/api/v1/versions/' + version + '/render-sessions/' + session + '/renew', method: 'POST' },
        { path: '/api/v1/versions/' + version + '/metadata?kind=normalized', forged: true }];
      const values = [];
      for (const item of paths) {
        const fields = { ...headers };
        if (item.key) fields['Idempotency-Key'] = crypto.randomUUID();
        if (item.forged) fields['Tailscale-User-Login'] = 'owner@example.com';
        const response = await fetch(item.path, { method: item.method || 'GET', headers: fields,
          body: item.method ? '{}' : undefined });
        values.push({ status: response.status, code: (await response.json()).error.code });
      }
      return values;
    }, { token: state.token, version: input.fixture[0].version, session: opened.session_id });
    assert(denied.every(r => r.status === 401), 'revoked bearer retained protected access');
    const narrow = [];
    for (const origin of input.expectedOrigins) {
      const target = await page.context().newPage();
      try { narrow.push((await target.goto(origin + '/root-asset.txt?revoked=1')).status()); }
      finally { await target.close(); }
    }
    assert(narrow.every(status => status === 401), 'revoked delivery statuses: ' + JSON.stringify(narrow));
    await page.reload();
    await page.getByRole('heading', { name: 'Haeryong 연결', exact: true }).waitFor();
    assert(await page.locator('iframe[title="결과 뷰어"]').count() === 0, 'revoked Reader frame retained');
    assert(await page.evaluate(() => localStorage.getItem('haeryong.owner-session.v1')) === null, 'revoked credential retained as current');
    await state.child.close();
    return { bearer: denied, narrow, recoveryUI: true, currentCredentialCleared: true };
  }
  throw Error('unknown private-ingress driver phase');
}
