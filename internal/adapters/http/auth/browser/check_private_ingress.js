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
  // Use real browser metadata for the renderer's unchanged same-origin gate.
  const deliveryStatus = async (context, url) => {
    const probe = await context.newPage();
    try { return (await probe.goto(url)).status(); }
    finally { await probe.close(); }
  };
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
    assert(boundary.rootAsset === 'asset-' + (fixture.assetIndex || fixture.label.split(' ').pop()), 'root-relative asset changed version');
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
  if (input.phase === 'task02-cohort') {
    const v0 = input.fixture[0];
    const v2 = input.fixture[2];
    const renderOrigin1 = input.expectedOrigins[0];
    const isHttps = new URL(input.base).protocol === 'https:';
    const createdContexts = [];
    const newCtx = async () => {
      const ctx = await page.context().browser().newContext();
      createdContexts.push(ctx);
      return ctx;
    };
    const requestPairingViaUI = async (targetPage, label, remember) => {
      await targetPage.getByLabel('장치 이름', { exact: true }).fill(label);
      if (typeof remember === 'boolean') {
        await targetPage.getByLabel('이 장치 기억', { exact: true }).setChecked(remember);
      }
      for (let attempt = 0; attempt < 35; attempt++) {
        const respPromise = targetPage.waitForResponse(
          r => r.url().endsWith('/api/v1/auth/pairings') && r.request().method() === 'POST',
        );
        await targetPage.getByRole('button', { name: '연결 코드 요청', exact: true }).click();
        const resp = await respPromise;
        if (resp.status() === 201) {
          return (await resp.json()).data;
        }
        assert(resp.status() === 429, 'unexpected pairing POST status: ' + resp.status());
        await targetPage.waitForTimeout(2000);
      }
      throw Error('pairing rate limit did not clear within 70s');
    };
    const approvePairingWithToken = async (targetPage, p, approverToken) => {
      const status = await targetPage.evaluate(async ({ p, token }) => {
        const r = await fetch('/api/v1/auth/pairings/' + p.pairing_id + '/approve', {
          method: 'POST',
          headers: {
            Authorization: 'Bearer ' + token,
            'Content-Type': 'application/json',
            'If-Match': p.etag,
            'Idempotency-Key': crypto.randomUUID(),
          },
          body: JSON.stringify({ code: p.code, origin: p.origin }),
        });
        return r.status;
      }, { p, token: approverToken });
      assert(status === 200, 'approve pairing failed with status ' + status);
    };
    const waitVerified = async targetPage => {
      await targetPage.waitForFunction(
        () => document.querySelector('[data-state]')?.getAttribute('data-state') === 'verified',
        null,
        { timeout: 20000 },
      );
    };
    try {
      // 1. Unpaired private client denials, Connect transport/storage notes, and Stop waiting.
      const unpairedCtx = await newCtx();
      const unpairedPage = await unpairedCtx.newPage();
      await unpairedPage.goto(input.base + '/connect?return=' + encodeURIComponent('https://unrelated.example/phish'));
      await unpairedPage.getByRole('heading', { name: 'Haeryong 연결', exact: true }).waitFor();
      const secNote = await unpairedPage.getByTestId('connect-security-note').innerText();
      const storageNote = await unpairedPage.getByTestId('connect-storage-note').innerText();
      assert(
        isHttps ? secNote.includes('비공개 HTTPS 신뢰 연결') : secNote.includes('루프백'),
        'unexpected Connect security note: ' + secNote,
      );
      assert(storageNote.includes('이 장치 기억'), 'unexpected Connect storage note: ' + storageNote);

      const unpairedStatuses = await unpairedPage.evaluate(async ({ ver }) => {
        const checks = [
          { path: '/api/v1/projects' },
          { path: '/api/v1/versions/' + ver + '/metadata?kind=normalized' },
          { path: '/api/v1/versions/' + ver + '/files?path=first.html' },
          { path: '/api/v1/versions/' + ver + '/package' },
          { path: '/api/v1/private/assets' },
          { path: '/api/v1/events' },
          { path: '/api/v1/operations/00000000-0000-4000-8000-000000000000' },
          { path: '/api/v1/session', headers: { 'Tailscale-User-Login': 'owner@example.com', 'X-Forwarded-User': 'admin' } },
        ];
        const out = [];
        for (const c of checks) {
          const r = await fetch(c.path, { headers: c.headers || {}, credentials: 'omit' });
          out.push(r.status);
        }
        return out;
      }, { ver: v0.version });
      assert(unpairedStatuses.every(s => s === 401), 'unpaired endpoint did not return 401: ' + JSON.stringify(unpairedStatuses));

      const step = async (name, fn) => {
        try {
          return await fn();
        } catch (err) {
          throw Error(`[task02-cohort:${name}] ${err && err.message ? err.message : String(err)}`);
        }
      };

      const stopPairing = await step('1-unpaired-and-stop-waiting', async () => {
        const sp = await requestPairingViaUI(unpairedPage, 'Task02 stop-waiting peer', true);
        await unpairedPage.waitForFunction(() => document.querySelector('[aria-label="비교 코드"]') !== null, null, { timeout: 9000 });
        const selfApproveStatus = await unpairedPage.evaluate(async p => {
          const r = await fetch('/api/v1/auth/pairings/' + p.pairing_id + '/approve', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'If-Match': p.etag, 'Idempotency-Key': crypto.randomUUID() },
            body: JSON.stringify({ code: p.code, origin: p.origin }),
          });
          return r.status;
        }, sp);
        assert(selfApproveStatus === 401, 'self-approval without owner bearer did not return 401: ' + selfApproveStatus);
        const secretLeaked = await unpairedPage.evaluate(
          secret => document.documentElement.outerHTML.includes(secret) || location.href.includes(secret),
          sp.polling_secret,
        );
        assert(!secretLeaked, 'polling secret leaked in DOM or URL');
        await unpairedPage.getByRole('button', { name: '대기 중단', exact: true }).click();
        await unpairedPage.waitForFunction(
          () => document.querySelector('[aria-label="페어링 진행"]')?.textContent?.includes('대기를 중단했습니다'),
          null,
          { timeout: 9000 },
        );
        return sp;
      });

      // 2. Trusted owner exact-return pairing, remembered reload/copied-exact-link new-tab, pending/approved-unredeemed list,
      // wrong code refusal, approved-unredeemed rejection (TE-N01), and 409/412 decision race readback.
      const ownerCtx = await newCtx();
      const ownerPage = await ownerCtx.newPage();
      state.track(ownerPage);
      const exactReturnTarget = '/documents/' + v0.document + '/versions/' + v0.version + '?page=nested%2Fsecond.html&page_query=scenario%3D2&page_fragment=part#region=example';
      const ownerPageToken = await step('2a-owner-exact-return-reload-and-copied-deep-link-newtab', async () => {
        await ownerPage.goto(input.base + '/connect?return=' + encodeURIComponent(exactReturnTarget));
        const ownerPairing = await requestPairingViaUI(ownerPage, 'Task02 trusted owner', true);
        await approvePairingWithToken(ownerPage, ownerPairing, input.token);
        await waitVerified(ownerPage);
        await ownerPage.frameLocator('iframe[title="결과 뷰어"]').locator('#second').waitFor({ timeout: 25000 });
        assert(
          await ownerPage.evaluate(() => location.pathname + location.search + location.hash) === exactReturnTarget,
          'exact-version/page/query/fragment/region login return was lost',
        );
        const tok = await ownerPage.evaluate(() => localStorage.getItem('haeryong.owner-session.v1'));
        assert(Boolean(tok), 'remembered owner token missing from localStorage');
        assert(
          !(await ownerPage.evaluate(t => document.documentElement.outerHTML.includes(t) || location.href.includes(t), tok)),
          'owner bearer leaked into DOM or URL',
        );
        await ownerPage.reload();
        await waitVerified(ownerPage);
        await ownerPage.frameLocator('iframe[title="결과 뷰어"]').locator('#second').waitFor({ timeout: 25000 });

        // Copy exact-version link via Reader UI and reopen that exact deep link in a new tab.
        await ownerPage.locator('#reader-copy-link').click();
        const copiedInput = ownerPage.getByLabel('복사할 링크', { exact: true });
        await copiedInput.waitFor();
        const copiedExactUrl = await copiedInput.inputValue();
        assert(copiedExactUrl === input.base + exactReturnTarget, 'unexpected copied exact-version URL: ' + copiedExactUrl);
        assert(!copiedExactUrl.includes(tok), 'copied link leaked owner bearer token');
        const secondTab = await ownerCtx.newPage();
        state.track(secondTab);
        await secondTab.goto(copiedExactUrl);
        await waitVerified(secondTab);
        await secondTab.frameLocator('iframe[title="결과 뷰어"]').locator('#second').waitFor({ timeout: 25000 });
        assert(
          await secondTab.evaluate(() => location.pathname + location.search + location.hash) === exactReturnTarget,
          'new tab did not preserve copied exact-version/page/query/fragment/region link',
        );
        await secondTab.close();
        return tok;
      });

      await step('2b-unsafe-external-return-fallback', async () => {
        await ownerPage.goto(input.base + '/connect?return=' + encodeURIComponent('https://unrelated.example/phish'));
        await waitVerified(ownerPage);
        assert(await ownerPage.evaluate(() => location.pathname) === '/', 'unsafe external return was not rejected');
      });

      const pendingList = ownerPage.getByTestId('pending-pairings-list');
      await step('2c-pending-list-wrong-code-approved-unredeemed-reject-and-missing-row-readback', async () => {
        await ownerPage.goto(input.base + '/connect');
        await waitVerified(ownerPage);
        await ownerPage.getByRole('button', { name: '장치 목록 새로고침', exact: true }).click();
        const stopRow = pendingList.locator('li', { hasText: 'Task02 stop-waiting peer' });
        await stopRow.waitFor();
        assert(
          (await stopRow.innerText()).includes('승인 대기'),
          'stop-waiting request was cancelled on server or missing from pending list',
        );
        await stopRow.getByRole('button', { name: '요청 선택', exact: true }).click();
        assert(await ownerPage.getByLabel('요청 ID', { exact: true }).inputValue() === stopPairing.pairing_id, 'selecting row did not populate request ID');
        await ownerPage.getByLabel('비교 코드', { exact: true }).fill('00000000');
        await ownerPage.getByRole('button', { name: '장치 승인', exact: true }).click();
        await ownerPage.waitForFunction(
          () => document.querySelector('[aria-label="소유자 도구 결과"]')?.textContent?.includes('INVALID_REQUEST'),
          null,
          { timeout: 12000 },
        );
        await ownerPage.getByLabel('비교 코드', { exact: true }).fill(stopPairing.code);
        await ownerPage.getByRole('button', { name: '장치 승인', exact: true }).click();
        await ownerPage.waitForFunction(
          () => document.querySelector('[aria-label="소유자 도구 결과"]')?.textContent?.includes('요청이 처리되었습니다'),
          null,
          { timeout: 12000 },
        );
        await ownerPage.getByRole('button', { name: '장치 목록 새로고침', exact: true }).click();
        await ownerPage.waitForFunction(
          () => document.querySelector('[data-testid="pending-pairings-list"]')?.textContent?.includes('승인됨·미수령'),
          null,
          { timeout: 12000 },
        );
        await pendingList.locator('li', { hasText: 'Task02 stop-waiting peer' }).getByRole('button', { name: '요청 선택', exact: true }).click();
        await ownerPage.getByLabel('비교 코드', { exact: true }).fill(stopPairing.code);
        await ownerPage.getByRole('button', { name: '장치 거절', exact: true }).click();
        await ownerPage.waitForFunction(
          () => document.querySelector('[aria-label="소유자 도구 결과"]')?.textContent?.includes('요청이 처리되었습니다'),
          null,
          { timeout: 12000 },
        );
        const rejectedPoll = await ownerPage.evaluate(async p => {
          const r = await fetch('/api/v1/auth/pairings/' + p.pairing_id + '/poll', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ polling_secret: p.polling_secret }),
          });
          return (await r.json()).data;
        }, stopPairing);
        assert(rejectedPoll.state === 'rejected', 'expected rejected state on poll, got ' + JSON.stringify(rejectedPoll));
        // Successful reject clears the form inputs; re-fill the original request ID and code
        // before attempting another decision on the now-rejected request to verify missing-row readback.
        await ownerPage.getByLabel('요청 ID', { exact: true }).fill(stopPairing.pairing_id);
        await ownerPage.getByLabel('비교 코드', { exact: true }).fill(stopPairing.code);
        await ownerPage.getByRole('button', { name: '장치 승인', exact: true }).click();
        await ownerPage.waitForFunction(
          () => document.querySelector('[aria-label="소유자 도구 결과"]')?.textContent?.includes('더 이상 활성 대기 목록에 없습니다'),
          null,
          { timeout: 12000 },
        );
      });

      await step('2d-stale-etag-412-decision-post-race', async () => {
        const racePairing = await requestPairingViaUI(unpairedPage, 'Task02 race peer', false);
        await unpairedPage.waitForFunction(() => document.querySelector('[aria-label="비교 코드"]') !== null, null, { timeout: 9000 });
        await unpairedPage.getByRole('button', { name: '대기 중단', exact: true }).click();
        await unpairedPage.waitForFunction(
          () => document.querySelector('[aria-label="페어링 진행"]')?.textContent?.includes('대기를 중단했습니다'),
          null,
          { timeout: 9000 },
        );
        await ownerPage.getByRole('button', { name: '장치 목록 새로고침', exact: true }).click();
        const raceRow = pendingList.locator('li', { hasText: 'Task02 race peer' });
        await raceRow.waitFor();
        await raceRow.getByRole('button', { name: '요청 선택', exact: true }).click();
        await ownerPage.getByLabel('비교 코드', { exact: true }).fill(racePairing.code);
        let interceptedRaceOnce = false;
        const raceRoutePattern = '**/api/v1/auth/pairings';
        await ownerPage.route(raceRoutePattern, async route => {
          if (!interceptedRaceOnce && route.request().method() === 'GET') {
            interceptedRaceOnce = true;
            const resp = await route.fetch();
            await approvePairingWithToken(ownerPage, racePairing, input.token);
            await route.fulfill({ response: resp });
            return;
          }
          await route.continue();
        });
        const staleDecisionRespPromise = ownerPage.waitForResponse(
          r => r.url().endsWith('/api/v1/auth/pairings/' + racePairing.pairing_id + '/reject') && r.request().method() === 'POST',
        );
        await ownerPage.getByRole('button', { name: '장치 거절', exact: true }).click();
        const staleDecisionResp = await staleDecisionRespPromise;
        await ownerPage.unroute(raceRoutePattern);
        assert(staleDecisionResp.status() === 412, 'expected 412 on stale-ETag decision POST race, got ' + staleDecisionResp.status());
        await ownerPage.waitForFunction(
          () => document.querySelector('[aria-label="소유자 도구 결과"]')?.textContent?.includes('서버 상태가 변경되었습니다(현재 상태: 승인됨(미수령), 리비전 2)'),
          null,
          { timeout: 12000 },
        );
        // Reject the approved-unredeemed racePairing with its refreshed ETag so the queue stays clean.
        await ownerPage.getByRole('button', { name: '장치 거절', exact: true }).click();
        await ownerPage.waitForFunction(
          () => document.querySelector('[aria-label="소유자 도구 결과"]')?.textContent?.includes('요청이 처리되었습니다'),
          null,
          { timeout: 12000 },
        );
      });

      // 3. Memory-only selection, single-use duplicate redemption refusal, and narrow delivery cookie lifecycle across reload (200) and UI peer revocation (401) (TE-P02).
      await step('3-memory-only-reload-and-ui-peer-revoke', async () => {
        const memCtx = await newCtx();
        const memPage = await memCtx.newPage();
        state.track(memPage);
        await memPage.goto(input.base + '/connect?return=' + encodeURIComponent('/documents/' + v0.document + '/versions/' + v0.version + '?page=first.html'));
        await memPage.getByLabel('이 장치 기억', { exact: true }).setChecked(false);
        const memStorageNote = await memPage.getByTestId('connect-storage-note').innerText();
        assert(memStorageNote.includes('현재 탭 메모리에만 자격이 유지되어') && memStorageNote.includes('서버 세션은 자동 해지되지 않으며'), 'memory-only storage note missing expected guidance: ' + memStorageNote);
        const memPairing = await requestPairingViaUI(memPage, 'Task02 memory-only reader', false);
        await approvePairingWithToken(ownerPage, memPairing, ownerPageToken);
        await waitVerified(memPage);
        await memPage.frameLocator('iframe[title="결과 뷰어"]').locator('#heading').waitFor({ timeout: 25000 });
        assert(await memPage.evaluate(() => localStorage.getItem('haeryong.owner-session.v1')) === null, 'memory-only session persisted token to localStorage');
        const memOpen = state.opens[v0.version];
        assert(Boolean(memOpen), 'memory-only render session open missing');

        // Single-use duplicate redemption must fail with 409 INVALID_TRANSITION.
        const dupRedeem = await memPage.evaluate(async p => {
          const r = await fetch('/api/v1/auth/pairings/' + p.pairing_id + '/poll', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ polling_secret: p.polling_secret }),
          });
          return { status: r.status, code: (await r.json()).error?.code };
        }, memPairing);
        assert(dupRedeem.status === 409, 'duplicate redemption did not return 409: ' + JSON.stringify(dupRedeem));

        // Reload memory-only page: drops bearer, returns to Connect with safe return, unmounts Reader,
        // denies new management/bootstrap/open without bearer, while already-issued narrow cookie stays valid until session/delivery revocation (TE-P02).
        await memPage.reload();
        await memPage.getByRole('heading', { name: 'Haeryong 연결', exact: true }).waitFor();
        assert(await memPage.locator('iframe[title="결과 뷰어"]').count() === 0, 'memory-only reload retained Reader iframe');
        assert(new URL(memPage.url()).searchParams.get('return')?.includes(v0.version), 'memory-only reload lost safe return target');
        const memAfterReloadMgmt = await memPage.evaluate(async ver => {
          const s1 = (await fetch('/api/v1/session')).status;
          const s2 = (await fetch('/api/v1/versions/' + ver + '/render-bootstrap')).status;
          const s3 = (await fetch('/api/v1/versions/' + ver + '/render-sessions', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID() },
            body: '{}',
          })).status;
          return [s1, s2, s3];
        }, v0.version);
        assert(memAfterReloadMgmt.every(s => s === 401), 'memory-only reload retained bearer authority: ' + JSON.stringify(memAfterReloadMgmt));
        const cookieStatusAfterReload = await deliveryStatus(memCtx, new URL('/root-asset.txt?mem_after_reload=1', renderOrigin1).href);
        assert(cookieStatusAfterReload === 200, 'expected narrow delivery cookie to remain valid while server session/lease active, got ' + cookieStatusAfterReload);

        // Revoke 'Task02 memory-only reader' through ownerPage's real /settings/sessions UI button and verify narrow delivery cookie returns 401.
        const memSessionId = await ownerPage.evaluate(async tok => {
          const r = await fetch('/api/v1/sessions', { headers: { Authorization: 'Bearer ' + tok } });
          const items = (await r.json()).data.items;
          return items.find(s => s.device_label === 'Task02 memory-only reader' && s.state === 'active')?.session_id;
        }, ownerPageToken);
        assert(Boolean(memSessionId), 'active memory-only session not found in /api/v1/sessions');

        await ownerPage.getByTestId('open-sessions-settings').click();
        await ownerPage.getByRole('heading', { name: '세션 및 보안 정책', exact: true }).waitFor();
        await ownerPage.getByTestId('revoke-device-' + memSessionId).click();
        await ownerPage.waitForFunction(
          sid =>
            document.querySelector('[data-testid="sessions-device-message"]')?.textContent?.includes('Task02 memory-only reader: 해지가 확인되었습니다.') &&
            document.querySelector('[data-testid="session-state-' + sid + '"]')?.textContent === 'revoked',
          memSessionId,
          { timeout: 12000 },
        );
        const cookieStatusAfterRevoke = await deliveryStatus(memCtx, new URL('/root-asset.txt?mem_after_revoke=1', renderOrigin1).href);
        assert(cookieStatusAfterRevoke === 401, 'expected narrow delivery cookie 401 after owner session UI revoke, got ' + cookieStatusAfterRevoke);
      });

      // 4. Same-owner impact actions (source scope & document Trash/Restore) and generated-frame denial (TE-P03).
      const ownerOpen = await step('4-same-owner-impact-and-frame-denial', async () => {
        const impactResult = await ownerPage.evaluate(async ({ tok, sourceRoot, docId }) => {
          const headers = { Authorization: 'Bearer ' + tok, 'Content-Type': 'application/json' };
          const checkedData = async (response, expectedStatus, label) => {
            const body = await response.json();
            if (response.status !== expectedStatus || !body.data) {
              throw new Error(label + ': HTTP ' + response.status + ', code=' + (body.error?.code ?? 'missing-data'));
            }
            return body.data;
          };
          const proj = await checkedData(await fetch('/api/v1/projects', {
            method: 'POST',
            headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() },
            body: JSON.stringify({ name: 'Task02 impact project' }),
          }), 201, 'create impact project');
          const cfg0 = await checkedData(await fetch('/api/v1/configuration', { headers }), 200, 'read impact configuration');
          const srcPlan = await checkedData(await fetch('/api/v1/configuration/plans', {
            method: 'POST',
            headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() },
            body: JSON.stringify({
              scope: { kind: 'project', project_id: proj.project_id },
              expected_config_revision: cfg0.config_revision,
              changes: { sources: [{ source_id: crypto.randomUUID(), kind: 'files', root_path: sourceRoot, repo_id: null, retention_mode: 'reference_only', provider_ids: [] }] },
            }),
          }), 200, 'plan impact source');
          const unconfirmedSrc = (await fetch('/api/v1/configuration/plans/' + srcPlan.plan_id + '/commit', {
            method: 'POST',
            headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() },
            body: JSON.stringify({ expected_config_revision: srcPlan.base_config_revision, confirmed: false }),
          })).status;
          const confirmedSrc = (await fetch('/api/v1/configuration/plans/' + srcPlan.plan_id + '/commit', {
            method: 'POST',
            headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() },
            body: JSON.stringify({ expected_config_revision: srcPlan.base_config_revision, confirmed: true }),
          })).status;

          const doc0 = await checkedData(await fetch('/api/v1/documents/' + docId, { headers }), 200, 'read impact document');
          const trashPlan = await checkedData(await fetch('/api/v1/documents/' + docId + '/lifecycle-plans', {
            method: 'POST',
            headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() },
            body: JSON.stringify({ operation: 'trash', expected_revision: doc0.revision }),
          }), 201, 'plan trash');
          const unconfirmedTrash = (await fetch('/api/v1/lifecycle-plans/' + trashPlan.plan_id + '/commit', {
            method: 'POST',
            headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() },
            body: JSON.stringify({ plan_id: trashPlan.plan_id, expected_revision: doc0.revision, confirmed: false }),
          })).status;
          const confirmedTrash = await checkedData(await fetch('/api/v1/lifecycle-plans/' + trashPlan.plan_id + '/commit', {
            method: 'POST',
            headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() },
            body: JSON.stringify({ plan_id: trashPlan.plan_id, expected_revision: doc0.revision, confirmed: true }),
          }), 200, 'commit trash');
          const restorePlan = await checkedData(await fetch('/api/v1/documents/' + docId + '/lifecycle-plans', {
            method: 'POST',
            headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() },
            body: JSON.stringify({ operation: 'restore', expected_revision: confirmedTrash.revision }),
          }), 201, 'plan restore');
          const confirmedRestore = await checkedData(await fetch('/api/v1/lifecycle-plans/' + restorePlan.plan_id + '/commit', {
            method: 'POST',
            headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() },
            body: JSON.stringify({ plan_id: restorePlan.plan_id, expected_revision: confirmedTrash.revision, confirmed: true }),
          }), 200, 'commit restore');
          return { unconfirmedSrc, confirmedSrc, unconfirmedTrash, trashedLifecycle: confirmedTrash.lifecycle, restoredLifecycle: confirmedRestore.lifecycle };
        }, { tok: ownerPageToken, sourceRoot: input.disposableSourceRoot, docId: v2.document });
        assert(
          impactResult.unconfirmedSrc === 400 && impactResult.confirmedSrc === 200 &&
          impactResult.unconfirmedTrash === 400 && impactResult.trashedLifecycle === 'trash' && impactResult.restoredLifecycle === 'active',
          'same-owner impact actions failed: ' + JSON.stringify(impactResult),
        );

        // Open Reader on ownerPage for v0, verify generated-frame management denial, and prepare for passive-idle expiry test (TE-P01, TE-A01).
        await read(ownerPage, v0, renderOrigin1, false);
        const op = state.opens[v0.version];
        assert(Boolean(op), 'ownerPage render session open missing');
        const rFrame = ownerPage.frames().find(f => f !== ownerPage.mainFrame() && f.url().includes('first.html'));
        const frameMgmt = await rFrame.evaluate(async ({ base, docId }) => {
          parent.postMessage({ type: 'management.configure', payload: { session_idle_seconds: 99999 } }, '*');
          const statuses = [];
          for (const path of ['/api/v1/configuration/plans', '/api/v1/documents/' + docId + '/lifecycle-plans', '/api/v1/auth/pairings/arbitrary/approve']) {
            try {
              const r = await fetch(base + path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' });
              statuses.push(r.status);
            } catch {
              statuses.push(0);
            }
          }
          return statuses;
        }, { base: input.base, docId: v2.document });
        assert(frameMgmt.every(s => s === 0 || s === 401 || s === 403), 'generated frame management call succeeded: ' + JSON.stringify(frameMgmt));
        return op;
      });

      // 5. Pair policyCtx ('Task02 policy editor') and exercise /settings/sessions UI, validation, fresh-inventory preview guard (G3),
      // stale-draft preservation, and configuring session_idle_seconds=6 to expire ownerPage + open /settings/sessions tab (G2) while passive liveness/events/renew run (TE-P01, TE-A01, TE-P05).
      const policyCtx = await newCtx();
      const policyPage = await policyCtx.newPage();
      const applyBtn = policyPage.getByTestId('policy-commit-button');
      const policyCreatedAtMs = await step('5a-policy-editor-pair-fresh-inventory-guard-and-stale-draft', async () => {
        await policyPage.goto(input.base + '/connect');
        const policyPairing = await requestPairingViaUI(policyPage, 'Task02 policy editor', true);
        const createdMs = Date.now();
        await approvePairingWithToken(ownerPage, policyPairing, ownerPageToken);
        await waitVerified(policyPage);
        await policyPage.getByTestId('open-sessions-settings').click();
        await policyPage.getByRole('heading', { name: '세션 및 보안 정책', exact: true }).waitFor();
        await policyPage.getByTestId('current-session-summary').waitFor();
        await policyPage.getByTestId('policy-current-summary').waitFor();
        await policyPage.getByTestId('sessions-device-list').locator('li', { hasText: '(이 장치)' }).waitFor();
        const policySectionText = await policyPage.getByTestId('security-policy-section').innerText();
        assert(
          policySectionText.includes('실제 소유자 활동만 최근 활동 시각을 갱신합니다') &&
          policySectionText.includes('유휴 만료 시계를 연장하지 않습니다'),
          'passive activity explanation missing',
        );

        await policyPage.getByTestId('policy-remember-default').setChecked(false);
        await policyPage.getByTestId('policy-idle-seconds').fill('6');
        await policyPage.getByTestId('policy-absolute-seconds').fill('');

        // G3: Failed current listSessions read during preview must surface error, preserve draft, and refuse confirmable preview from stale cached devices.
        let failSessionsListOnce = true;
        const sessionsRoutePattern = '**/api/v1/sessions';
        await policyPage.route(sessionsRoutePattern, async route => {
          if (failSessionsListOnce && route.request().method() === 'GET') {
            failSessionsListOnce = false;
            await route.fulfill({
              status: 503,
              contentType: 'application/json',
              body: JSON.stringify({ error: { code: 'SERVICE_UNAVAILABLE', message: 'simulated inventory read failure', retryable: true } }),
            });
            return;
          }
          await route.continue();
        });
        await policyPage.getByTestId('policy-preview-button').click();
        await policyPage.getByTestId('policy-error-notice').waitFor();
        await policyPage.unroute(sessionsRoutePattern);
        assert(await policyPage.getByTestId('policy-preview-panel').count() === 0, 'preview panel rendered despite failed device inventory read');
        assert(await policyPage.getByTestId('policy-pending-panel').count() === 0, 'undispatched plan entered pending state on inventory read failure');
        assert(await policyPage.getByTestId('policy-idle-seconds').inputValue() === '6', 'failed inventory read lost user draft input');

        // Retry preview with working listSessions.
        await policyPage.getByTestId('policy-preview-button').click();
        await policyPage.getByTestId('policy-preview-panel').waitFor();
        await policyPage.getByTestId('policy-normalized-summary').waitFor();
        await policyPage.getByTestId('policy-expiry-preview').waitFor();
        assert((await policyPage.getByTestId('policy-expiry-preview').innerText()).includes('미리보기 시점 기준 추정치이며'), 'expiry estimate note missing');
        assert(await applyBtn.isDisabled(), 'apply button enabled before confirmation checkbox');

        // Advance config_revision out-of-band via input.token so policyPage's commit encounters 412 STALE_PLAN.
        await policyPage.evaluate(async tok => {
          const headers = { Authorization: 'Bearer ' + tok, 'Content-Type': 'application/json' };
          const cur = (await (await fetch('/api/v1/configuration', { headers })).json()).data;
          const plan = (await (await fetch('/api/v1/configuration/plans', {
            method: 'POST',
            headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() },
            body: JSON.stringify({ scope: { kind: 'library' }, expected_config_revision: cur.config_revision, changes: { security: cur.configuration.security } }),
          })).json()).data;
          await fetch('/api/v1/configuration/plans/' + plan.plan_id + '/commit', {
            method: 'POST',
            headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() },
            body: JSON.stringify({ expected_config_revision: plan.base_config_revision, confirmed: true }),
          });
        }, input.token);
        await policyPage.getByTestId('policy-confirm-checkbox').check();
        await applyBtn.click();
        await policyPage.waitForFunction(
          () => document.querySelector('[data-testid="policy-error-notice"]')?.textContent?.includes('구성 리비전이 변경되었거나 계획이 만료되었습니다. 입력한 정책 변경안은 유지되므로'),
          null,
          { timeout: 12000 },
        );
        assert(
          await policyPage.getByTestId('policy-idle-seconds').inputValue() === '6' &&
          await policyPage.getByTestId('policy-remember-default').isChecked() === false,
          'stale plan reread reset user-proposed policy draft inputs',
        );
        return createdMs;
      });

      await step('5b-idle-expiry-with-open-sessions-tab-and-passive-renew', async () => {
        // G2: Keep a second tab in ownerCtx open on /settings/sessions with no owner action
        // while causing and observing a background /api/v1/events SSE invalidation/close + reconnect after installing the request observer,
        // proving zero automatic touching reads and real 6s idle expiry.
        const idleSessionsTab = await ownerCtx.newPage();
        let releaseFirstEventsStream;
        const firstEventsStreamGate = new Promise(resolve => {
          releaseFirstEventsStream = resolve;
        });
        let firstEventsStreamIntercepted = false;
        let idleSessionsEventsReconnects = 0;
        const idleEventsPattern = '**/api/v1/events';
        await idleSessionsTab.route(idleEventsPattern, async route => {
          if (!firstEventsStreamIntercepted) {
            firstEventsStreamIntercepted = true;
            await firstEventsStreamGate;
            await route.fulfill({
              status: 200,
              contentType: 'text/event-stream',
              body: 'event: configuration.changed\ndata: {}\n\n',
            });
            return;
          }
          idleSessionsEventsReconnects++;
          await route.continue();
        });
        await idleSessionsTab.goto(input.base + '/settings/sessions');
        await idleSessionsTab.getByRole('heading', { name: '세션 및 보안 정책', exact: true }).waitFor();
        await idleSessionsTab.getByTestId('policy-current-summary').waitFor();
        await idleSessionsTab.getByTestId('sessions-device-list').locator('li', { hasText: '(이 장치)' }).waitFor();
        let idleSessionsTouchingReads = 0;
        const onIdleTabReq = req => {
          const u = req.url();
          if (u.endsWith('/api/v1/sessions') || u.endsWith('/api/v1/configuration') || u.endsWith('/api/v1/auth/pairings')) {
            idleSessionsTouchingReads++;
          }
        };
        idleSessionsTab.on('request', onIdleTabReq);
        releaseFirstEventsStream();

        // Before applying session_idle_seconds=6, start tracking an open /api/v1/events stream and passive renew on ownerPage.
        await ownerPage.evaluate(async ({ tok, ver, sid }) => {
          window.__idleEventStreamEnded = false;
          (async () => {
            try {
              const resp = await fetch('/api/v1/events', { headers: { Authorization: 'Bearer ' + tok } });
              const reader = resp.body.getReader();
              while (true) {
                const { done } = await reader.read();
                if (done) break;
              }
            } catch {}
            window.__idleEventStreamEnded = true;
          })();
          const renewResp = await fetch('/api/v1/versions/' + ver + '/render-sessions/' + sid + '/renew', {
            method: 'POST',
            headers: { Authorization: 'Bearer ' + tok, 'Content-Type': 'application/json' },
            body: '{}',
          });
          window.__passiveRenewStatus = renewResp.status;
        }, { tok: ownerPageToken, ver: v0.version, sid: ownerOpen.value.session_id });
        assert(await ownerPage.evaluate(() => window.__passiveRenewStatus) === 200, 'passive renew before idle expiry failed');

        // Re-preview and commit session_idle_seconds=6 on policyPage.
        await policyPage.getByTestId('policy-remember-default').setChecked(true);
        await policyPage.getByTestId('policy-preview-button').click();
        await policyPage.getByTestId('policy-preview-panel').waitFor();
        await policyPage.getByTestId('policy-confirm-checkbox').check();
        await applyBtn.click();
        await policyPage.waitForFunction(
          () => document.querySelector('[data-testid="policy-saved-notice"]')?.textContent?.includes('보안 정책 변경이 저장되었습니다.'),
          null,
          { timeout: 12000 },
        );

        // Keep policyPage and input.token active with genuine owner reads every 2s while ownerPage (Reader) and idleSessionsTab (/settings/sessions) sit with ONLY passive shell liveness/events running for 7.5s.
        for (let i = 0; i < 4; i++) {
          await policyPage.waitForTimeout(2000);
          await policyPage.evaluate(async tok => {
            await fetch('/api/v1/projects', { headers: { Authorization: 'Bearer ' + tok } });
            const myTok = localStorage.getItem('haeryong.owner-session.v1');
            await fetch('/api/v1/projects', { headers: { Authorization: 'Bearer ' + myTok } });
          }, input.token);
        }

        idleSessionsTab.off('request', onIdleTabReq);
        await idleSessionsTab.unroute(idleEventsPattern);
        assert(idleSessionsEventsReconnects >= 1, 'expected background SSE /api/v1/events reconnect on idle /settings/sessions tab after observer install, got ' + idleSessionsEventsReconnects);
        assert(idleSessionsTouchingReads === 0, 'open /settings/sessions tab issued automatic touching reads without owner action: ' + idleSessionsTouchingReads);

        // Verify ownerPage and idleSessionsTab expired on schedule (passive checks did not reset last_seen_at):
        // SSE stream ended, stored token cleared, Reader unmounted, and all protected endpoints + narrow cookie return 401.
        await ownerPage.waitForFunction(() => window.__idleEventStreamEnded === true, null, { timeout: 10000 });
        await ownerPage.waitForFunction(() => localStorage.getItem('haeryong.owner-session.v1') === null, null, { timeout: 10000 });
        await idleSessionsTab.getByRole('heading', { name: 'Haeryong 연결', exact: true }).waitFor();
        await idleSessionsTab.close();
        assert(await ownerPage.locator('iframe[title="결과 뷰어"]').count() === 0, 'idle-expired Reader iframe remained mounted');

        const expiredChecks = await policyPage.evaluate(async ({ expiredTok, ver, sid, opKey }) => {
          const headers = { Authorization: 'Bearer ' + expiredTok, 'Content-Type': 'application/json' };
          const reqs = [
            fetch('/api/v1/projects', { headers }),
            fetch('/api/v1/projects', { method: 'POST', headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() }, body: JSON.stringify({ name: 'Revive attempt' }) }),
            fetch('/api/v1/versions/' + ver + '/files?path=first.html', { headers }),
            fetch('/api/v1/versions/' + ver + '/package', { headers }),
            fetch('/api/v1/operations/' + opKey, { headers }),
            fetch('/api/v1/versions/' + ver + '/render-sessions', { method: 'POST', headers: { ...headers, 'Idempotency-Key': opKey }, body: '{}' }),
            fetch('/api/v1/versions/' + ver + '/render-sessions/' + sid + '/renew', { method: 'POST', headers, body: '{}' }),
          ];
          const resps = await Promise.all(reqs);
          return Promise.all(resps.map(async r => ({ status: r.status, code: (await r.json()).error?.code })));
        }, { expiredTok: ownerPageToken, ver: v0.version, sid: ownerOpen.value.session_id, opKey: ownerOpen.key });
        assert(
          expiredChecks.every(c => c.status === 401 && c.code === 'SESSION_EXPIRED'),
          'idle-expired token retained access or was revived: ' + JSON.stringify(expiredChecks),
        );
        const cookieAfterIdleExpiry = await deliveryStatus(ownerCtx, new URL('/root-asset.txt?after_idle_expiry=1', renderOrigin1).href);
        assert(cookieAfterIdleExpiry === 401, 'narrow delivery cookie remained valid after owner session idle expiry: ' + cookieAfterIdleExpiry);
      });

      // 6. Durable policy commit returning 401 SESSION_EXPIRED retains UNKNOWN recovery intent and reconciles via original receipt under a replacement session (TE-P05 v3).
      await step('6-self-expiring-commit-401-unknown-and-receipt-recovery', async () => {
        // Wait until policyPage's session is >61s old (touching every 3s so the 6s idle limit does not expire it)
        // so the 60s CreatePairing rate-limit window is completely clear before issuing freshApproverPairing and replacementPairing.
        while (Date.now() - policyCreatedAtMs < 61500) {
          await policyPage.waitForTimeout(3000);
          await policyPage.evaluate(async () => {
            const myTok = localStorage.getItem('haeryong.owner-session.v1');
            await fetch('/api/v1/projects', { headers: { Authorization: 'Bearer ' + myTok } });
          });
        }
        // Create a fresh remembered session ('Task02 fresh approver') whose CreatedAt is ~0s ago (< 45s).
        const freshApproverPairing = await requestPairingViaUI(ownerPage, 'Task02 fresh approver', true);
        const policyToken = await policyPage.evaluate(() => localStorage.getItem('haeryong.owner-session.v1'));
        await approvePairingWithToken(policyPage, freshApproverPairing, policyToken);
        await waitVerified(ownerPage);

        // On policyPage (age >= 61.5s), preview and commit session_idle_seconds=null, session_absolute_seconds=45.
        await policyPage.getByTestId('policy-idle-seconds').fill('');
        await policyPage.getByTestId('policy-absolute-seconds').fill('45');
        await policyPage.getByTestId('policy-preview-button').click();
        await policyPage.getByTestId('policy-preview-panel').waitFor();
        assert((await policyPage.getByTestId('policy-normalized-summary').innerText()).includes('45초'), 'normalized policy summary missing 45s limit');
        assert((await policyPage.getByTestId('policy-expiry-preview').innerText()).includes('이 장치 — 적용 즉시 현재 세션 만료'), 'expiry preview missing current-device self-expiry warning');
        let commitPostCount = 0;
        const onCommitReq = r => {
          if (r.method() === 'POST' && r.url().includes('/configuration/plans/') && r.url().endsWith('/commit')) {
            commitPostCount++;
          }
        };
        policyPage.on('request', onCommitReq);
        await policyPage.getByTestId('policy-confirm-checkbox').check();
        const commit401RespPromise = policyPage.waitForResponse(
          r => r.request().method() === 'POST' && r.url().includes('/configuration/plans/') && r.url().endsWith('/commit'),
        );
        await applyBtn.click();
        const commit401Resp = await commit401RespPromise;
        assert(commit401Resp.status() === 401, 'expected 401 SESSION_EXPIRED on self-expiring policy commit, got ' + commit401Resp.status());
        await policyPage.getByRole('heading', { name: 'Haeryong 연결', exact: true }).waitFor();
        assert(
          await policyPage.evaluate(() => document.body.textContent.includes('보안 정책 변경 적용 여부를 확인하지 못한 채 이 세션이 만료되었습니다.')),
          'UNKNOWN policy-commit notice missing after 401 SESSION_EXPIRED',
        );
        assert(await policyPage.evaluate(() => localStorage.getItem('haeryong.owner-session.v1')) === null, 'expired token retained after 401 policy commit');

        // Pair replacement session on policyPage using ownerPage ('Task02 fresh approver', age < 45s).
        const replacementPairing = await requestPairingViaUI(policyPage, 'Task02 replacement owner', true);
        await ownerPage.goto(input.base + '/connect');
        await waitVerified(ownerPage);
        await ownerPage.getByLabel('요청 ID', { exact: true }).fill(replacementPairing.pairing_id);
        await ownerPage.getByLabel('비교 코드', { exact: true }).fill(replacementPairing.code);
        await ownerPage.getByRole('button', { name: '장치 승인', exact: true }).click();
        await waitVerified(policyPage);

        // Use SPA navigation in the original policyPage controller (never policyPage.goto) so in-memory recovery intent is preserved.
        if (await policyPage.evaluate(() => location.pathname !== '/settings/sessions')) {
          await policyPage.getByTestId('open-sessions-settings').click();
        }
        await policyPage.getByRole('heading', { name: '세션 및 보안 정책', exact: true }).waitFor();
        await policyPage.getByTestId('policy-pending-panel').waitFor();
        const opReads = [];
        const onOpReq = r => {
          if (r.method() === 'GET' && r.url().includes('/api/v1/operations/')) {
            opReads.push(r.url().split('/').pop());
          }
        };
        policyPage.on('request', onOpReq);
        await policyPage.getByTestId('policy-recover-button').click();
        await policyPage.waitForFunction(
          () => document.querySelector('[data-testid="policy-saved-notice"]')?.textContent?.includes('이전 세션이 만료되기 전에 보안 정책 변경이 적용된 것을 확인했습니다.'),
          null,
          { timeout: 12000 },
        );
        policyPage.off('request', onOpReq);
        policyPage.off('request', onCommitReq);
        assert(commitPostCount === 1 && opReads.length === 1, 'expected 1 commit POST and 1 operation receipt read, got ' + commitPostCount + ' / ' + opReads.length);
        assert(await policyPage.getByTestId('policy-absolute-seconds').inputValue() === '45', 'reconciled config did not reflect 45s absolute expiry');

        // Restore unlimited expiry policy so baseline local owner session remains valid.
        await policyPage.getByTestId('policy-absolute-seconds').fill('');
        await policyPage.getByTestId('policy-preview-button').click();
        await policyPage.getByTestId('policy-preview-panel').waitFor();
        await policyPage.getByTestId('policy-confirm-checkbox').check();
        await applyBtn.click();
        await policyPage.waitForFunction(
          () => document.querySelector('[data-testid="policy-saved-notice"]')?.textContent?.includes('보안 정책 변경이 저장되었습니다.'),
          null,
          { timeout: 12000 },
        );
      });

      await step('6b-live-mounted-reader-peer-revoke-via-sessions-ui', async () => {
        await read(ownerPage, v0, renderOrigin1, false);
        const freshOpen = state.opens[v0.version];
        assert(Boolean(freshOpen), 'freshApprover render session open missing');
        const freshApproverToken = await ownerPage.evaluate(() => localStorage.getItem('haeryong.owner-session.v1'));
        assert(Boolean(freshApproverToken), 'freshApprover remembered token missing');

        await ownerPage.evaluate(async tok => {
          window.__peerRevokeLiveStreamOpened = false;
          window.__peerRevokeLiveStreamEnded = false;
          (async () => {
            try {
              const resp = await fetch('/api/v1/events', { headers: { Authorization: 'Bearer ' + tok } });
              if (resp.status === 200 && resp.body) {
                window.__peerRevokeLiveStreamOpened = true;
                const reader = resp.body.getReader();
                while (true) {
                  const { done } = await reader.read();
                  if (done) break;
                }
              }
            } catch {}
            window.__peerRevokeLiveStreamEnded = true;
          })();
        }, freshApproverToken);
        await ownerPage.waitForFunction(() => window.__peerRevokeLiveStreamOpened === true, null, { timeout: 10000 });

        const freshSessionId = await policyPage.evaluate(async () => {
          const tok = localStorage.getItem('haeryong.owner-session.v1');
          const r = await fetch('/api/v1/sessions', { headers: { Authorization: 'Bearer ' + tok } });
          const items = (await r.json()).data.items;
          return items.find(s => s.device_label === 'Task02 fresh approver' && s.state === 'active')?.session_id;
        });
        assert(Boolean(freshSessionId), 'active Task02 fresh approver session not found');

        await policyPage.getByTestId('sessions-refresh-button').click();
        await policyPage.getByTestId('revoke-device-' + freshSessionId).waitFor();
        await policyPage.getByTestId('revoke-device-' + freshSessionId).click();
        await policyPage.waitForFunction(
          sid =>
            document.querySelector('[data-testid="sessions-device-message"]')?.textContent?.includes('Task02 fresh approver: 해지가 확인되었습니다.') &&
            document.querySelector('[data-testid="session-state-' + sid + '"]')?.textContent === 'revoked',
          freshSessionId,
          { timeout: 12000 },
        );

        await ownerPage.waitForFunction(() => window.__peerRevokeLiveStreamEnded === true, null, { timeout: 10000 });
        await ownerPage.waitForFunction(() => localStorage.getItem('haeryong.owner-session.v1') === null, null, { timeout: 10000 });
        assert(await ownerPage.locator('iframe[title="결과 뷰어"]').count() === 0, 'revoked peer Reader iframe remained mounted');

        const liveRevokedChecks = await policyPage.evaluate(async ({ revokedTok, ver, sid, opKey }) => {
          const headers = { Authorization: 'Bearer ' + revokedTok, 'Content-Type': 'application/json' };
          const reqs = [
            fetch('/api/v1/projects', { headers }),
            fetch('/api/v1/projects', { method: 'POST', headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() }, body: JSON.stringify({ name: 'Revoked live peer attempt' }) }),
            fetch('/api/v1/versions/' + ver + '/files?path=first.html', { headers }),
            fetch('/api/v1/versions/' + ver + '/package', { headers }),
            fetch('/api/v1/operations/' + opKey, { headers }),
            fetch('/api/v1/versions/' + ver + '/render-sessions', { method: 'POST', headers: { ...headers, 'Idempotency-Key': opKey }, body: '{}' }),
            fetch('/api/v1/versions/' + ver + '/render-sessions/' + sid + '/renew', { method: 'POST', headers, body: '{}' }),
          ];
          const resps = await Promise.all(reqs);
          return Promise.all(resps.map(async r => ({ status: r.status, code: (await r.json()).error?.code })));
        }, { revokedTok: freshApproverToken, ver: v0.version, sid: freshOpen.value.session_id, opKey: freshOpen.key });
        assert(
          liveRevokedChecks.every(c => c.status === 401 && c.code === 'SESSION_REVOKED'),
          'revoked live peer token retained access: ' + JSON.stringify(liveRevokedChecks),
        );
        const cookieAfterLivePeerRevoke = await deliveryStatus(ownerCtx, new URL('/root-asset.txt?after_live_peer_revoke=1', renderOrigin1).href);
        assert(cookieAfterLivePeerRevoke === 401, 'narrow delivery cookie remained valid after live peer UI revoke: ' + cookieAfterLivePeerRevoke);
      });

      return {
        mode: input.modeLabel,
        unpairedDenied: true,
        stopWaitingLocalOnly: true,
        exactReturnAndRememberedReload: true,
        copiedExactVersionLinkNewTab: true,
        unsafeReturnRejected: true,
        pendingAndApprovedUnredeemedRejected: true,
        decisionRaceAuthoritativeReadback: true,
        singleUseDuplicateRedemptionDenied: true,
        memoryOnlyReloadAndCookieLifecycle: true,
        peerRevokeViaSessionsUIEndsEventsAndDelivery: true,
        sameOwnerImpactAndFrameDenial: true,
        failedInventoryReadBlocksConfirmablePreview: true,
        stalePlanPreservesUserDraft: true,
        openSessionsScreenDoesNotTouchIdleClock: true,
        passiveChecksDoNotExtendIdleExpiry: true,
        idleExpiryTerminatesEventsAndDelivery: true,
        commit401RetainedUnknownAndReconciledByReceipt: true,
      };
    } finally {
      for (const ctx of createdContexts) {
        try { await ctx.close(); } catch {}
      }
    }
  }
  if (input.phase === 'task03-cohort') {
    const v = input.fixture[0], target = page;
    const observed = {mode:input.modeLabel, controlledAsk:'browser-peer-ui-only', physicalDevice:'not_run', virtualKeyboard:'short-viewport-only'};
    const verify = async () => target.waitForFunction(()=>document.querySelector('.service-state')?.getAttribute('data-state')==='verified');
    const api = async (method,path,body) => target.evaluate(async a=>{
      const r=await fetch('/api/v1'+a.path,{method:a.method,headers:{Authorization:'Bearer '+localStorage.getItem('haeryong.owner-session.v1'),...(a.body===undefined?{}:{'Content-Type':'application/json'})},body:a.body===undefined?undefined:JSON.stringify(a.body)});
      return {status:r.status,data:(await r.json()).data};
    },{method,path,body});
    const context = async tab => {
      if(await target.locator('#reader-context-toggle').getAttribute('aria-expanded')!=='true')await target.locator('#reader-context-toggle').click();
      await target.getByRole('tab',{name:tab,exact:true}).click();
    };
    const closeContext = async () => {if(await target.locator('#reader-context-panel').evaluate(e=>e.matches(':modal')))await target.keyboard.press('Escape');};
    const requests=[];target.on('request',r=>requests.push(r.url()));
    const stage = async (name,fn) => {if(input.selectedStages&&!input.selectedStages.includes(name)&&!(['import-faults','download-faults'].includes(name)&&input.selectedStages.includes('transfer-faults')))return;try{const result=await fn();(observed.completedStages??=[]).push(name);return result;}catch(e){(observed.failedStages??=[]).push({stage:name,error:e.message,alerts:await target.locator('[role=alert]').allTextContents()});return;}};
    try {
    await target.setViewportSize({width:390,height:844});
    await target.goto(route(v));await verify();
    await target.frameLocator('iframe').locator('#heading').waitFor();
    await stage('responsive',async()=>{
      const frame=target.frames().find(f=>f!==target.mainFrame()&&f.url().includes('first.html'));
      await frame.evaluate(()=>window.task03Continuity='same-frame');
      observed.viewports=[];
      for(const [width,height,scale] of [[390,844,100],[390,844,200],[390,320,100],[844,390,100],[820,1180,100],[1440,900,100]]){
        await target.setViewportSize({width,height});await target.evaluate(s=>document.documentElement.style.fontSize=s+'%',scale);
        await target.locator('#reader-context-toggle').scrollIntoViewIfNeeded();
        const metrics=await target.evaluate(()=>{
          const rect=id=>{const r=document.getElementById(id).getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height};};
          return {width:innerWidth,height:innerHeight,overflow:document.documentElement.scrollWidth>innerWidth+1,context:rect('reader-context-toggle'),files:rect('reader-files-toggle'),bar:document.querySelector('.reader-actions').getBoundingClientRect().height};
        });
        assert(!metrics.overflow,'horizontal overflow '+JSON.stringify(metrics));
        assert(metrics.context.height>=44&&metrics.context.width>=44,'Context target under 44px');
        if(width<768){assert(metrics.files.height>=44&&metrics.files.width>=44,'Files target under 44px');assert(metrics.bar<height/2,'sticky controls consume reading viewport');}
        if(width<1280){
          await context('Ask');await target.locator('#ask-new').click();await target.locator('#ask-text').waitFor();
          await target.locator('#ask-submit').scrollIntoViewIfNeeded();
          assert(await target.locator('#reader-context-panel').evaluate(e=>e.matches(':modal')),'Context must be modal');
          const submit=await target.locator('#ask-submit').boundingBox();assert(submit.y>=0&&submit.y+submit.height<=height+1,'Submit obscured');
          await target.locator('#ask-text').focus();await target.keyboard.press('Tab');
          assert(await target.evaluate(()=>document.getElementById('reader-context-panel').contains(document.activeElement)),'focus escaped modal');
          await target.keyboard.press('Escape');
          await target.waitForFunction(()=>document.activeElement?.id==='reader-context-toggle');
          metrics.modalAndFocusReturn=true;
        }
        observed.viewports.push({...metrics,scale});
      }
      assert(await frame.evaluate(()=>window.task03Continuity)==='same-frame','resize remounted viewer');
      await target.evaluate(()=>document.documentElement.style.fontSize='');await target.setViewportSize({width:390,height:844});
      observed.contrast=await target.evaluate(()=>{
        const rgb=s=>s.match(/[\d.]+/g).slice(0,3).map(Number),lum=c=>rgb(c).map(x=>{x/=255;return x<=.04045?x/12.92:((x+.055)/1.055)**2.4;}).reduce((a,x,i)=>a+x*[.2126,.7152,.0722][i],0);
        const css=getComputedStyle(document.body),a=lum(css.color),b=lum(css.backgroundColor);return (Math.max(a,b)+.05)/(Math.min(a,b)+.05);
      });assert(observed.contrast>=4.5,'text contrast under 4.5');
      await target.locator('#reader-context-toggle').focus();await target.keyboard.press('Tab');
      observed.focus=await target.evaluate(()=>{const s=getComputedStyle(document.activeElement);return {width:s.outlineWidth,style:s.outlineStyle};});
      assert(observed.focus.style!=='none'&&parseFloat(observed.focus.width)>=2,'keyboard focus not visible');
    });
    await stage('library-search',async()=>{
      let mode='loading', release;const gate=new Promise(r=>release=r);
      const handler=async r=>{if(mode==='loading')await gate;if(mode==='error')return r.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE',retryable:true,details:{}}}});const response=await r.fetch();const body=await response.json();if(mode==='empty')body.data={...body.data,items:[],next_cursor:null};await r.fulfill({response,json:body});};
      await target.route('**/api/v1/documents?*',handler);await target.goto(input.base+'/library');
      await target.getByTestId('library-count').filter({hasText:'불러오는 중'}).waitFor();mode='error';release();await target.getByRole('alert').waitFor();
      mode='empty';await target.reload();await target.getByTestId('library-empty').waitFor();
      mode='real';await target.reload();await target.getByTestId('library-document').first().waitFor();await target.unroute('**/api/v1/documents?*',handler);
      const field=target.getByRole('search').getByLabel('검색어',{exact:true});const box=await field.boundingBox();assert(box.height>=44,'search field under 44px');
      await field.fill('nevermatchunique');const zeroResponse=target.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/search'&&new URL(r.url()).searchParams.get('q')==='nevermatchunique');await target.getByRole('search').getByRole('button',{name:'검색',exact:true}).click();
      const zero=(await (await zeroResponse).json()).data;assert(zero&&zero.total_matches==='0','unexpected zero-search response '+JSON.stringify(zero));
      if(zero.index.state!=='complete'){
        // Keep actual incomplete-index facts; the separate complete/empty presentation is an explicit HTTP peer.
        observed.noResultOriginalIndexState=zero.index.state;
        const completeEmpty=async r=>r.fulfill({json:{data:{...zero,index:{...zero.index,state:'complete'}}}});
        await target.route('**/api/v1/search?*',completeEmpty);await target.getByRole('button',{name:'검색 새로고침',exact:true}).click();await target.getByTestId('search-status').filter({hasText:'일치하는 결과가 없습니다'}).waitFor();await target.unroute('**/api/v1/search?*',completeEmpty);observed.noResultCompletePresentation='controlled HTTP peer';
      }else await target.getByTestId('search-status').filter({hasText:'일치하는 결과가 없습니다'}).waitFor();
      await field.fill('alpha-search-token');await target.getByRole('search').getByRole('button',{name:'검색',exact:true}).click();await target.getByTestId('search-hit').locator('a[href*="'+v.version+'"]').first().click();
      await target.frameLocator('iframe').locator('#heading').waitFor();observed.library={loadingErrorRetryEmptyPopulated:true,noResultsAndSearchNavigation:true};
    });
    await stage('reader-note-review-links',async()=>{
      await read(target,v,input.expectedOrigins[0],true);await target.goto(route(v));await target.frameLocator('iframe').locator('#heading').waitFor();
      assert(!requests.some(u=>/\/unrequested-sentinel\.(html|bin)(?:\?|$)/.test(u)),'unrequested payload fetched');
      await target.locator('#reader-files-toggle').click();assert(await target.locator('#reader-files-panel').evaluate(e=>e.matches(':modal')),'Files not modal');await target.keyboard.press('Escape');
      const frame=target.frames().find(f=>f!==target.mainFrame()&&f.url().includes('first.html'));
      const payload={selected_text:'Retry selected quote',detail:'Task03 complete context '.repeat(300)};
      await frame.evaluate(async p=>{parent.postMessage({type:'management.delete',payload:{forged:true}},'*');await window.__haeryong.select('retry',p);},payload);
      await target.getByText('선택한 영역: retry',{exact:true}).waitFor();
      await context('Evidence');await target.locator('.evidence-row').first().getByRole('button').first().click();
      const choices=target.locator('.evidence-detail label.choice:visible');await choices.first().waitFor();for(const choice of await choices.all())assert((await choice.boundingBox()).height>=44,'Evidence choice under 44px');
      await context('Notes');await target.getByLabel('새 노트·재연결 범위').selectOption('region');await target.locator('#note-new').click();await target.locator('#note-body').fill('Task03 page exact note');await target.locator('#note-save').click();await target.waitForFunction(()=>!document.querySelector('#note-body'));
      const notes=(await api('GET','/versions/'+v.version+'/annotations')).data.items;const note=notes.find(n=>n.body==='Task03 page exact note');assert(note?.anchor.page_path==='first.html'&&note.anchor.region_id==='retry'&&note.context_ref,'note exact scope/context missing');
      await target.locator('.note-entry').filter({hasText:'Task03 page exact note'}).getByRole('button',{name:'저장된 context 읽기',exact:true}).click();
      await target.locator('.note-context-text').filter({hasText:'Task03 complete context'}).waitFor();assert((await target.locator('.note-context-text').innerText()).includes(payload.detail),'complete context truncated');await target.getByRole('button',{name:'context 닫기',exact:true}).click();
      observed.note={page:note.anchor.page_path,region:note.anchor.region_id,contextBytes:new TextEncoder().encode(JSON.stringify(payload)).length};
      await context('Links');await target.getByRole('button',{name:'Version 2 Reference',exact:true}).click();await target.frameLocator('iframe').locator('#second').waitFor();
      await target.getByRole('button',{name:'참조한 페이지로 돌아가기',exact:true}).click();await target.frameLocator('iframe').locator('#heading').waitFor();
      await closeContext();await target.locator('#review-start').click();await target.locator('#review-complete').click();await target.locator('#review-state').filter({hasText:'검토 완료'}).waitFor();
      const review=(await api('GET','/versions/'+v.version+'/review')).data;assert(review.value?.state==='reviewed','whole-version Review not stored');
      await target.locator('#lifecycle-more-'+v.document).click();await target.getByRole('button',{name:'보관',exact:true}).click();await target.locator('#lifecycle-confirm-'+v.document).waitFor();await target.keyboard.press('Escape');
      await target.locator('#reader-versions').click();await target.locator('#versions-from').waitFor();await target.locator('#versions-return').click();await target.frameLocator('iframe').locator('#heading').waitFor();
      observed.reader={onDemand:true,multiPage:true,ordinaryJavaScript:true,exactReferenceReturn:true,wholeVersionReview:true,lifecyclePlanCancel:true,versionsReturn:true};
    });
    await stage('outline',async()=>{
      await target.goto(route(v));await target.frameLocator('iframe').locator('#heading').waitFor();
      await context('Evidence');const choice=target.locator('.region-outline').getByRole('button',{name:'Retry Section',exact:true});
      await choice.focus();assert(await choice.evaluate(e=>e===document.activeElement),'outline keyboard focus missing');
      await target.keyboard.press('Enter');await target.waitForFunction(()=>!document.getElementById('reader-context-panel').matches(':modal'));
      const frame=target.frames().find(f=>f!==target.mainFrame()&&f.url().includes('first.html'));
      await frame.waitForFunction(()=>document.activeElement?.id==='retry');
      await context('Evidence');
      assert(await choice.getAttribute('aria-current')==='true','outline lost selected region');
      await target.locator('#reader-evidence-content').getByRole('heading',{name:/first\.html · 영역 retry/}).waitFor();
      await target.locator('.evidence-row').first().getByRole('button').first().click();await target.locator('.evidence-detail label.choice:visible').first().waitFor();
      observed.outline={keyboardEnter:true,page:'first.html',region:'retry',frameFocusAcknowledged:true,selectedOutlineAndEvidence:true,viewport:[390,844]};
      await closeContext();
    });
    await stage('controlled-ask',async()=>{
      const qText='Controlled Task03 question?',answerText='Controlled browser peer answer. This is UI evidence only.';
      const selectedContext={selected_text:'Ask exact selected context',detail:'Complete Ask display context '.repeat(300)},contextText=JSON.stringify(selectedContext);
      await target.goto(route(v));await target.frameLocator('iframe').locator('#heading').waitFor();const askFrame=target.frames().find(f=>f!==target.mainFrame()&&f.url().includes('first.html'));await askFrame.evaluate(async value=>window.__haeryong.select('retry',value),selectedContext);await target.getByText('선택한 영역: retry',{exact:true}).waitFor();
      const digests=await target.evaluate(async texts=>Object.fromEntries(await Promise.all(texts.map(async text=>{const raw=new TextEncoder().encode(text);return [text,{bytes:raw.length,sha:Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',raw))).map(x=>x.toString(16).padStart(2,'0')).join('')}];}))),[qText,answerText,input.noteText,contextText]);
      const hash=t=>digests[t].sha,bytes=t=>digests[t].bytes;
      const qid='00000000-0000-4000-8000-000000000302',jid='00000000-0000-4000-8000-000000000301',aid='00000000-0000-4000-8000-000000000303';
      const ref=t=>({object_id:hash(t),bytes_sha256:hash(t),byte_size:String(bytes(t)),media_type:'text/plain; charset=utf-8'});
      let captured,operation,submitted=false;
      const accepted=()=>({operation_id:operation,question_id:qid,job_id:jid,job_url:'/api/v1/jobs/'+jid});
      const answer={answer_id:aid,question_id:qid,attempt_id:'00000000-0000-4000-8000-000000000304',state:'final',created_at:'2026-11-06T00:00:00Z',body_ref:ref(answerText),body_url:'/api/v1/answers/'+aid+'/body',citations:[{basis:'posthoc_verified',read_id:null,verification_id:'00000000-0000-4000-8000-000000000305',input_origin:null,target_set_id:'00000000-0000-4000-8000-000000000306',source:{kind:'snapshot',snapshot_sha256:input.noteSha256},location:{path:'note.md'},bytes_sha256:input.noteSha256,availability:'available',bytes_origin:'retained_snapshot',reason:null,observed_at:null,verified_at:'2026-11-06T00:00:00Z'}],explanation_references:[{version_id:v.version,page_path:'first.html',region_id:'retry',bytes_sha256:input.pageSha256}],limitations:[{code:'controlled_browser_peer_ui_only',message:'UI peer; no provider execution.'}]};
      const question=()=>({question_id:qid,project_id:v.project,document_id:v.document,version_id:v.version,job_id:jid,job_state:'completed',job_error:null,latest_answer_id:aid,operation_id:operation,created_at:answer.created_at,page_path:captured?.page_path??'first.html',region_id:captured?.region_id??null,page_query:'',page_fragment:'',body_ref:captured?.body_ref??ref(qText),text_url:'/api/v1/questions/'+qid+'/text',context_ref:captured?.context_ref??null,display_context:captured?.context_ref?{context_ref:captured.context_ref,bytes_sha256:hash(contextText),byte_size:String(bytes(contextText)),media_type:'application/json',render_instance_id:null}:null,explanation_reference:{version_id:v.version,page_path:'first.html',region_id:'retry',bytes_sha256:input.pageSha256},source_mode:'pinned',target_set_id:null,targets:[],requested_selection:{selections:[]},limitations:[],answers:{items:[answer],next_cursor:null}});
      const handler=async r=>{const req=r.request(),u=new URL(req.url()),p=u.pathname;let data;
        if(req.method()==='POST'&&p==='/api/v1/versions/'+v.version+'/questions'){captured=req.postDataJSON();operation=req.headers()['idempotency-key'];assert(captured.page_path==='first.html'&&captured.region_id==='retry'&&captured.context_ref&&captured.body_ref?.bytes_sha256===hash(qText),'captured Ask binding/body differs');const exactContext=await target.evaluate(ref=>Object.keys(localStorage).filter(k=>k.startsWith('haeryong.context-transfer.v1:')).map(k=>JSON.parse(localStorage.getItem(k))).find(x=>x.receipt?.context_ref===ref)?.receipt,captured.context_ref);assert(exactContext?.bytes_sha256===hash(contextText),'actual prepared context bytes differ');submitted=true;return r.fulfill({status:202,json:{data:accepted()}});}
        if(p==='/api/v1/operations/'+operation)data={operation_id:operation,state:'completed',original_status:202,result_metadata:accepted()};
        else if(p==='/api/v1/jobs/'+jid)data={job_id:jid,state:'completed',kind:'question',cancel_requested:false,error:null};
        else if(p==='/api/v1/versions/'+v.version+'/questions')data={items:submitted?[question()]:[],next_cursor:null};
        else if(p==='/api/v1/questions/'+qid)data=question();
        else if(p==='/api/v1/questions/'+qid+'/text'||p==='/api/v1/answers/'+aid+'/body'||p==='/api/v1/questions/'+qid+'/context'||p==='/api/v1/answers/'+aid+'/citations/0/source'){const text=p.endsWith('/text')?qText:p.endsWith('/context')?contextText:p.endsWith('/source')?input.noteText:answerText;return r.fulfill({status:200,headers:{'Content-Type':'text/plain; charset=utf-8','Content-Length':String(bytes(text)),ETag:'"sha256-'+hash(text)+'"'},body:text});}
        else return r.continue();await r.fulfill({status:200,json:{data}});
      };
      await target.route('**/api/v1/**',handler);
      try{await context('Ask');await target.locator('.ask-panel > label.field select').first().selectOption('region');await target.locator('#ask-new').click();await target.locator('#ask-text').fill(qText);await target.locator('#ask-submit').click();await target.getByRole('button',{name:'이 질문의 저장된 답변 읽기',exact:true}).waitFor();await target.getByRole('button',{name:'이 질문의 저장된 답변 읽기',exact:true}).click();await target.getByRole('button',{name:'답변 전체 읽기',exact:true}).click();await target.getByText(answerText,{exact:true}).waitFor();await target.getByRole('button',{name:'질문에 저장된 문맥 전체 읽기',exact:true}).click();assert(await target.getByLabel('저장된 질문 문맥 전체',{exact:true}).textContent()===contextText,'complete saved context differs');await target.getByRole('button',{name:'인용한 보관 원본 전체 읽기',exact:true}).click();assert(await target.getByLabel('인용한 정확한 보관 원본',{exact:true}).textContent()===input.noteText,'complete citation bytes differ');assert(submitted&&captured.body_ref&&operation,'real preparation or request identity missing');observed.ask={controlledPeer:true,requestIdentity:true,realBodyPreparation:true,completeSavedAnswer:true,actualCapturedRegionContextMatched:true,completeSavedContextAndCitationPeerStreams:true,providerExecution:false};}finally{await target.unroute('**/api/v1/**',handler);}
      await closeContext();
    });
    await stage('provider-maintenance',async()=>{
      let calls=0,degraded=false;const provider=async r=>{calls++;await r.fulfill({json:{data:{state:'ready',ordinary_usage_allowed:true,authentication_mode:'chatgpt',read_only:true,model:'controlled-browser-peer',effort:'high',reason:null}}});};
      const session=async r=>degraded?r.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE',retryable:true,details:{}}}}):r.continue();
      await target.route('**/api/v1/providers',provider);await target.route('**/api/v1/session',session);
      await target.goto(input.base+'/settings/ai');await target.locator('[data-provider-state]').filter({hasText:'연결 확인됨'}).waitFor();const before=calls;
      degraded=true;await target.evaluate(()=>document.dispatchEvent(new Event('visibilitychange')));await target.locator('.service-state[data-state="degraded"]').waitFor();assert(await target.locator('[data-provider-status]').count()===0,'stale ready state remained');
      await target.getByRole('link',{name:'정비',exact:true}).click();await target.locator('[data-maintenance-scan-disabled]').waitFor();assert((await target.locator('.placeholder-note').innerText()).includes('연결이 확인되지'),'Maintenance outage copy missing');
      await target.goto(input.base+'/settings/ai');await target.locator('.service-state[data-state="degraded"]').waitFor();assert(calls===before,'provider called while disconnected');
      degraded=false;await target.getByRole('button',{name:'다시 연결',exact:true}).click();await verify();assert(calls===before,'automatic reconnect provider probe');await target.getByRole('button',{name:'제공자 상태 새로 고침',exact:true}).click();await target.locator('[data-provider-status]').waitFor();assert(calls===before+1,'explicit refresh missing');
      await target.unroute('**/api/v1/providers',provider);await target.unroute('**/api/v1/session',session);
      const actualReply=target.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/providers');await target.getByRole('button',{name:'제공자 상태 새로 고침',exact:true}).click();const unavailable=(await (await actualReply).json()).data;
      await target.locator('[data-provider-state]').waitFor();assert(unavailable.state!=='ready'&&await target.locator('.service-state[data-state=verified]').count()===1,'provider refusal conflated with daemon outage');
      const disabledFeatures=async r=>{const response=await r.fetch(),body=await response.json();body.data.features={...body.data.features,generation:false,questions:false};await r.fulfill({response,json:body});};
      await target.route('**/api/v1/capabilities',disabledFeatures);await target.reload();await target.locator('[data-provider-feature-absence]').waitFor();await target.unroute('**/api/v1/capabilities',disabledFeatures);
      observed.provider={peerReady:true,outageClearsReady:true,entryDisconnectedNoProbe:true,explicitReconnectRefresh:true,actualUnavailableState:unavailable.state,actualUnavailableReason:unavailable.reason,featureAbsencePresentation:'controlled capability peer',nativeClientCalls:0};
    });
    await stage('transfer',async()=>{
      const importOne=async(label,path)=>{await target.goto(input.base+'/library');await target.getByRole('button',{name:'가져오기',exact:true}).click();const reset=target.getByRole('button',{name:'새 가져오기 시작',exact:true});if(await reset.count())await reset.click();await target.getByLabel(label,{exact:true}).setInputFiles(path);await target.getByRole('button',{name:'가져오기 시작',exact:true}).click();await target.getByTestId('import-stage').filter({hasText:'발행 완료'}).waitFor({timeout:60000});};
      await importOne('폴더 선택',input.sourceDirPath);await importOne('ZIP 선택',input.archivePath);await importOne('단일 HTML',input.singleHtmlPath);
      await target.goto(input.base+'/library');const row=target.locator('[data-document="'+v.document+'"]');await row.getByRole('button',{name:'원본 디렉터리 내려받기',exact:true}).click();await target.getByRole('button',{name:'내려받기 시작',exact:true}).click();await target.getByLabel('위 교체 규칙을 이해하고 동의합니다.').check();await target.getByRole('button',{name:'동의하고 계속',exact:true}).click();
      const zone=target.getByTestId('download-drop-zone');await zone.scrollIntoViewIfNeeded();const b=await zone.boundingBox(),cdp=await target.context().newCDPSession(target);
      try{for(const type of ['dragEnter','dragOver','drop'])await cdp.send('Input.dispatchDragEvent',{type,x:b.x+20,y:b.y+20,data:{items:[],files:[input.destination],dragOperationsMask:1}});}finally{await cdp.detach();}
      await target.getByTestId('download-permission').waitFor();await target.getByRole('button',{name:'이 폴더에 저장',exact:true}).click();await target.getByTestId('download-done').waitFor({timeout:60000});
      observed.browserArchive='haeryong-'+v.version+'.zip';observed.transfer={directoryFileList:true,zipSelector:true,singleHtml:true,realHostDirectoryTarget:true,preGrantedHandleNotPickerUx:true};
      if(input.screenshot)await target.screenshot({path:input.screenshot,fullPage:true});
    });
    await stage('faults',async()=>{
      // Observe the real loader's port only in this disposable browser, before its next load.
      await target.addInitScript(()=>{const original=MessagePort.prototype.postMessage;MessagePort.prototype.postMessage=function(message,...rest){if(message?.protocol_version==='1.0'){window.task03Port=this;window.task03Last=message;}return original.call(this,message,...rest);};});
      await target.goto(route(v));await target.frameLocator('iframe').locator('#heading').waitFor();
      let frame=target.frames().find(f=>f!==target.mainFrame()&&f.url().includes('first.html'));
      await frame.waitForFunction(()=>window.task03Port&&window.task03Last);
      const effects=[];const listener=r=>{if(['POST','PUT','DELETE'].includes(r.method())&&/\/api\/v1\/(uploads|contexts|versions\/[^/]+\/(annotations|questions|review)|documents\/[^/]+\/lifecycle)/.test(new URL(r.url()).pathname))effects.push({method:r.method(),path:new URL(r.url()).pathname});};target.on('request',listener);
      await frame.evaluate(()=>{const base=window.task03Last,port=window.task03Port;const send=(type,payload,extra={})=>port.postMessage({...base,sequence:++base.sequence,type,payload,...extra});send('management.delete',{all:true});send('region.selected',{region_id:'forged'},{protocol_version:'9.9'});send('region.selected',{region_id:'forged'},{page_path:'foreign.html'});send('region.selected',new Uint8Array([1]));parent.postMessage({type:'management.delete',payload:{all:true}},'*');});
      await target.waitForTimeout(250);assert(effects.length===0,'malformed message dispatched an operation');assert(await target.getByText('선택한 영역: forged',{exact:true}).count()===0,'forged region accepted');
      // A valid begin/abort on the actual bound port retains its staging journal.
      await frame.evaluate(async()=>{const port=window.task03Port,base=window.task03Last,id=crypto.randomUUID();const wait=closed=>new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('context abort reply missing')),10000);const receive=e=>{if(e.data?.payload?.transfer_id===id&&(!closed||e.data.payload.closed)){clearTimeout(timer);port.removeEventListener('message',receive);resolve(e.data);}};port.addEventListener('message',receive);});const ack=wait(false);port.postMessage({...base,sequence:++base.sequence,type:'context.transfer.begin',payload:{transfer_id:id,media_type:'application/json'}});await ack;const done=wait(true);port.postMessage({...base,sequence:++base.sequence,type:'context.transfer.abort',payload:{transfer_id:id}});await done;window.task03AbortedTransfer=id;});
      const aborted=await frame.evaluate(()=>window.task03AbortedTransfer);
      assert(await target.evaluate(id=>Object.keys(localStorage).filter(k=>k.startsWith('haeryong.context-transfer.v1:')).map(k=>JSON.parse(localStorage.getItem(k))).some(r=>r.transferId===id&&r.aborted&&r.uploadId),aborted),'abort journal not retained');
      target.off('request',listener);
      // Reload restores the loader's own sequence; lose one real committed chunk acknowledgement.
      await target.goto(route(v));await target.frameLocator('iframe').locator('#heading').waitFor();frame=target.frames().find(f=>f!==target.mainFrame()&&f.url().includes('first.html'));
      let lost=false,creates=0;const pattern='**/api/v1/uploads**';
      const interrupted=async r=>{const req=r.request(),path=new URL(req.url()).pathname;if(req.method()==='POST'&&path==='/api/v1/uploads')creates++;if(req.method()==='PUT'&&!lost){lost=true;await r.fetch();await r.abort();return;}await r.continue();};await target.route(pattern,interrupted);
      try{await frame.evaluate(async()=>window.__haeryong.select('retry',{selected_text:'retry after interrupted acknowledgement',detail:'exact context '.repeat(500)}));await target.getByText('선택한 영역: retry',{exact:true}).waitFor();assert(lost&&creates===1,'interrupted context forked its upload');}finally{await target.unroute(pattern,interrupted);}
      // Keep the old port across SPA disposal, then prove its late message cannot select a new frame.
      await target.evaluate(()=>addEventListener('message',e=>{if(e.data?.type==='task03.retain'&&e.ports[0])window.task03RetiredPort=e.ports[0];}));
      const binding=await frame.evaluate(()=>{const b={...window.task03Last};parent.postMessage({type:'task03.retain'},'*',[window.task03Port]);return b;});await target.waitForFunction(()=>window.task03RetiredPort);
      await target.getByRole('link',{name:'서재',exact:true}).click();await target.locator('[data-document="'+v.document+'"]').getByTestId('library-read').click();await target.frameLocator('iframe').locator('#heading').waitFor();
      await target.evaluate(b=>{window.task03RetiredPort.postMessage({...b,sequence:b.sequence+100,type:'region.selected',payload:{region_id:'stale'}});window.task03RetiredPort.close();},binding);await target.waitForTimeout(250);assert(await target.getByText('선택한 영역: stale',{exact:true}).count()===0,'disposed port changed current region');
      // Navigate while a real context PUT is held. Disposal must retire the pending echo, retaining incomplete staging.
      let release,held=false;const gate=new Promise(r=>release=r);const pending=async r=>{if(r.request().method()==='PUT'&&!held){held=true;await gate;await r.abort().catch(()=>{});return;}await r.continue();};await target.route(pattern,pending);
      frame=target.frames().find(f=>f!==target.mainFrame()&&f.url().includes('first.html'));
      try{await frame.evaluate(()=>{window.__haeryong.select('retry',{detail:'navigation pending '.repeat(500)}).catch(()=>{});});const deadline=Date.now()+10000;while(!held){assert(Date.now()<deadline,'context PUT gate not reached');await target.waitForTimeout(50);}await target.frameLocator('iframe').locator('#next').click();await target.frameLocator('iframe').locator('#second').waitFor();release();await target.waitForTimeout(250);assert(await target.getByText('선택한 영역: retry',{exact:true}).count()===0,'late old-page context became current');}finally{release();await target.unroute(pattern,pending);}
      await target.goto(route(v));await target.frameLocator('iframe').locator('#heading').waitFor();
      if(input.screenshot){await target.screenshot({path:input.screenshot.replace('.png','-reader-mobile.png'),fullPage:true});await target.setViewportSize({width:820,height:1180});await context('Notes');await target.screenshot({path:input.screenshot.replace('.png','-reader-tablet.png'),fullPage:true});await closeContext();await target.setViewportSize({width:390,height:844});}
      observed.contextFaults={finiteMalformedNonDispatch:true,actualBoundPortAbortRetained:true,committedChunkAckLossRecoveredSameUpload:true,oldFramePortRetired:true,navigationRetiresPendingEcho:true};
    });
    await stage('library-cursor',async()=>{
      let baseline;const handler=async r=>{const url=new URL(r.request().url());if(!baseline){const response=await r.fetch();baseline=await response.json();}const second=url.searchParams.get('cursor')==='task03-cursor';const data={...baseline.data,items:second?baseline.data.items.slice(1):baseline.data.items.slice(0,1),next_cursor:second?null:'task03-cursor'};await r.fulfill({json:{data}});};
      await target.route('**/api/v1/documents?*',handler);try{await target.goto(input.base+'/library');await target.getByTestId('library-document').waitFor();assert((await target.getByTestId('library-count').innerText()).includes('목록이 더 있습니다'),'partial list presented as complete');await target.getByRole('button',{name:'더 불러오기',exact:true}).click();await target.waitForFunction(()=>document.querySelectorAll('[data-testid=library-document]').length>1);observed.libraryCursor='controlled HTTP peer using actual document rows';}finally{await target.unroute('**/api/v1/documents?*',handler);}
    });
    await stage('import-faults',async()=>{
      await target.goto(input.base+'/library');await target.getByRole('button',{name:'가져오기',exact:true}).click();
      const form=target.getByTestId('import-control');for(const [label,path] of [['폴더 선택',input.sourceDirPath],['ZIP 선택',input.archivePath],['단일 HTML',input.singleHtmlPath]]){await form.getByLabel(label,{exact:true}).setInputFiles(path);await form.getByRole('button',{name:'가져오기 시작',exact:true}).waitFor();if(label==='폴더 선택')assert((await form.getByTestId('input-limits').innerText()).includes('빈 디렉터리'),'FileList limitation missing');await form.getByLabel(label,{exact:true}).setInputFiles(label==='폴더 선택'?input.emptyDirectoryPath:[]);assert(await form.getByRole('button',{name:'가져오기 시작',exact:true}).isDisabled(),'cancelled selector retained dispatch');}
      let puts=0,held=false,release,cancelled=false,creates=0;const gate=new Promise(r=>release=r),pattern='**/api/v1/uploads**';const handler=async r=>{const req=r.request();if(req.method()==='POST'&&new URL(req.url()).pathname==='/api/v1/uploads')creates++;if(req.method()==='PUT'&&++puts===2){held=true;await gate;/* Let the held write finish; cancellation is checked after its real acknowledgement. */}await r.continue();};await target.route(pattern,handler);
      try{await form.getByLabel('폴더 선택',{exact:true}).setInputFiles(input.sourceDirPath);const created=target.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/uploads'&&r.request().method()==='POST');await form.getByRole('button',{name:'가져오기 시작',exact:true}).click();const upload=(await (await created).json()).data.upload_id;const deadline=Date.now()+10000;while(!held){assert(Date.now()<deadline,'import second PUT gate missing');await target.waitForTimeout(50);}await target.getByRole('button',{name:'전송 취소 (스테이징 유지)',exact:true}).click();cancelled=true;release();await target.getByTestId('import-stage').filter({hasText:'전송 취소됨'}).waitFor();
        await target.getByLabel('입력 다시 선택',{exact:true}).setInputFiles(input.sourceDirPath);const accepted=target.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/imports'&&r.request().method()==='POST');await target.getByRole('button',{name:'다시 선택 후 이어서 전송 (범위 재검증)',exact:true}).click();const result=(await (await accepted).json()).data;await target.getByTestId('import-stage').filter({hasText:'발행 완료'}).waitFor({timeout:60000});const job=(await api('GET','/jobs/'+result.job_id)).data;assert(job.state==='completed'&&creates===1,'resume did not retain original upload');observed.resumeImportedVersion=job.result.version_id;observed.originalUploadRetained=upload;
      }finally{release();await target.unroute(pattern,handler);}
      observed.importFaults={selectorClearNoDispatch:true,FileListEmptyDirectoryLimit:true,originalUploadCancelResume:true};
    });
    await stage('download-faults',async()=>{
      const saveToHost=async()=>{await target.getByRole('button',{name:'내려받기 시작',exact:true}).click();await target.getByLabel('위 교체 규칙을 이해하고 동의합니다.').check();await target.getByRole('button',{name:'동의하고 계속',exact:true}).click();const zone=target.getByTestId('download-drop-zone');await zone.scrollIntoViewIfNeeded();const b=await zone.boundingBox(),cdp=await target.context().newCDPSession(target);try{for(const type of ['dragEnter','dragOver','drop'])await cdp.send('Input.dispatchDragEvent',{type,x:b.x+20,y:b.y+20,data:{items:[],files:[input.destination],dragOperationsMask:1}});}finally{await cdp.detach();}await target.getByTestId('download-permission').waitFor();await target.getByRole('button',{name:'이 폴더에 저장',exact:true}).click();};
      await target.goto(route(v));await target.frameLocator('iframe').locator('#heading').waitFor();await target.locator('#reader-files-toggle').click();await target.locator('#reader-files-panel').getByRole('button',{name:'note.md',exact:true}).click();await saveToHost();await target.getByTestId('download-done').waitFor();observed.browserFile='note.md';
      await target.goto(input.base+'/library');await target.locator('[data-document="'+v.document+'"]').getByRole('button',{name:'원본 디렉터리 내려받기',exact:true}).click();
      await target.evaluate(()=>{const original=window.fetch.bind(window);window.task03OriginalFetch=original;window.fetch=async(...args)=>{const response=await original(...args);if(!String(args[0]).endsWith('/package'))return response;const reader=response.body.getReader();const gate=new Promise(r=>window.task03ReleaseDownload=r);return new Response(new ReadableStream({async pull(controller){const part=await reader.read();if(part.done){await gate;controller.close();}else controller.enqueue(part.value);},cancel(){return reader.cancel();}}),{status:response.status,headers:response.headers});};});
      try{await saveToHost();await target.getByTestId('download-progress').filter({hasNotText:'수신 0 B'}).waitFor();await target.getByRole('button',{name:'취소',exact:true}).click();await target.evaluate(()=>window.task03ReleaseDownload());await target.getByTestId('download-incomplete').waitFor();assert(await target.getByTestId('download-done').count()===0,'cancelled download marked complete');}finally{await target.evaluate(()=>{window.task03ReleaseDownload?.();window.fetch=window.task03OriginalFetch;});}
      await target.addInitScript(()=>{delete window.showSaveFilePicker;delete DataTransferItem.prototype.getAsFileSystemHandle;});await target.goto(input.base+'/library');await target.locator('[data-document="'+v.document+'"]').getByRole('button',{name:'원본 디렉터리 내려받기',exact:true}).click();await target.getByRole('button',{name:'내려받기 시작',exact:true}).click();await target.getByTestId('download-unavailable').filter({hasText:'haeryong files download'}).waitFor();
      observed.downloadFaults={fileStreamRealHost:true,realStreamDownloadCancelIncomplete:true,unavailableTransportCliGuidance:true};
    });
    await stage('captures',async()=>{
      await target.goto(route(v));await target.frameLocator('iframe').locator('#heading').waitFor();await target.setViewportSize({width:390,height:844});await target.locator('iframe').scrollIntoViewIfNeeded();await target.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
      if(input.screenshot)await target.screenshot({path:input.screenshot.replace('.png','-reading-phone.png')});
      await target.setViewportSize({width:820,height:1180});await context('Notes');await target.locator('#note-new').click();await target.locator('#note-body').fill('Tablet editor layout observation');await target.locator('#note-body').focus();if(input.screenshot)await target.screenshot({path:input.screenshot.replace('.png','-tablet-editor.png')});
      observed.captures={phoneReadingPosition:true,tabletModalEditor:true,physicalDevice:false};
    });
    return {...observed,complete:!observed.failedStages?.length,error:observed.failedStages?.map(x=>x.stage+': '+x.error).join('; ')};
    } catch(error) {return {...observed,complete:false,error:error.message,diagnostic:{alerts:await target.locator('[role=alert]').allTextContents(),searchStatus:await target.getByTestId('search-status').allTextContents()}};}
  }
  throw Error('unknown private-ingress driver phase');
}
