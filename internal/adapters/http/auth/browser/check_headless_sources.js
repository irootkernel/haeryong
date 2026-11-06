async page => {
  const input = INPUT;
  let stage = "start";
  try {
  const assert = (ok, message) => {
    if (!ok) throw Error(message);
  };

  const waitVerified = async target => {
    await target.waitForFunction(
      () => document.querySelector('.service-state')?.getAttribute('data-state') === 'verified',
      null,
      { timeout: 25000 }
    );
  };

  const inspectFrameOnFirstPage = async (target, item, expectedOrigin) => {
    const route =
      input.base +
      '/documents/' +
      item.document +
      '/versions/' +
      item.version +
      '?page=first.html' +
      (item.hasMetadata ? '#region=overview' : '');
  stage = "reader navigation";
    await target.goto(route);
    await waitVerified(target);

  stage = "reader frame";
    const viewer = target.frameLocator('iframe[title="결과 뷰어"]');
    await viewer.locator('#heading').waitFor({ timeout: 25000 });
    assert(
      (await viewer.locator('#heading').innerText()) === item.label + ' first',
      'Unexpected heading text for ' + item.label
    );
    await viewer.locator('#increment').click();
    assert(
      (await viewer.locator('#increment').innerText()) === '1',
      'Ordinary script interaction failed for ' + item.label
    );

    const frame = target.frames().find(f => f !== target.mainFrame() && f.url().includes('first.html'));
    assert(frame, 'Isolated renderer frame not found for ' + item.label);
    const expectedAsset = 'asset-' + item.label + '-' + item.runToken;
  stage = "reader module/worker/asset";
    await frame.waitForFunction(
      expected =>
        window.moduleValue === 'module-running' &&
        window.workerValue === 'worker-running' &&
        window.rootAsset === expected,
      expectedAsset,
      { timeout: 15000 }
    );

    const boundary = await frame.evaluate(() => {
      let dom = false;
      let storage = false;
      try {
        void parent.document.body;
        dom = true;
      } catch {}
      try {
        void parent.localStorage;
        storage = true;
      } catch {}
      return {
        origin: location.origin,
        hostDOMDenied: !dom,
        hostStorageDenied: !storage,
        nativeAbsent: Boolean(!(window.wails || window.runtime || window.haeryongNative)),
        rootAsset: window.rootAsset,
        color: getComputedStyle(document.querySelector('#heading')).color,
      };
    });

    assert(
      boundary.hostDOMDenied && boundary.hostStorageDenied && boundary.nativeAbsent,
      'Generated-frame authority separation failed for ' + item.label
    );
    assert(boundary.origin !== new URL(input.base).origin, 'Renderer shares management origin');
    if (expectedOrigin) {
      assert(
        boundary.origin === new URL(expectedOrigin).origin,
        'Unexpected renderer origin: ' + boundary.origin + ' vs ' + expectedOrigin
      );
    }
    assert(boundary.color === 'rgb(1, 2, 3)', 'Stylesheet delivery failed for ' + item.label);
    assert(boundary.rootAsset === expectedAsset, 'Root asset mismatch for ' + item.label);
    return { viewer, boundary };
  };

  const navigateSecondPage = async (target, viewer, item) => {
    await viewer.locator('#next').click();
    await target.frameLocator('iframe[title="결과 뷰어"]').locator('#second').waitFor({ timeout: 20000 });
    await target.waitForFunction(() => new URL(location.href).searchParams.get('page') === 'nested/second.html');
    const url = new URL(target.url());
    assert(
      url.searchParams.get('page_query') === 'scenario=2' && url.searchParams.get('page_fragment') === 'part',
      'Multi-page query/fragment lost for ' + item.label
    );
    assert(
      (await target.frameLocator('iframe[title="결과 뷰어"]').locator('#second').innerText()) ===
        item.label + ' second',
      'Second page heading mismatch for ' + item.label
    );
  };

  // 1. Sourced version (v_sourced): T-007-05-01 multi-page reading + T-007-05-03 posthoc verification & Ask panel.
  const sourcedItem = input.fixture[0];
  const { viewer: sourcedViewer, boundary: sourcedBoundary } = await inspectFrameOnFirstPage(
    page,
    sourcedItem,
    input.expectedOrigins[0]
  );

  stage = "sourced header identities";
  const sourcedHeader = page.locator('section.version-header');
  await sourcedHeader.waitFor({ timeout: 15000 });
  await page.waitForFunction(
    ({ gitId, snapId }) => {
      const text = document.querySelector('section.version-header')?.innerText || '';
      return (
        text.includes('증거 기록됨(정확성 보증 아님)') &&
        text.includes(gitId) &&
        text.includes('보존 reference_only') &&
        text.includes(snapId) &&
        text.includes('보존 선택 스냅샷')
      );
    },
    { gitId: input.gitSourceId, snapId: input.snapSourceId },
    { timeout: 15000 }
  );

  const contextToggle = page.locator('#reader-context-toggle');
  if ((await contextToggle.getAttribute('aria-expanded')) !== 'true') {
    await contextToggle.click();
  }
  const evidenceTab = page.locator('#reader-evidence-tab');
  if ((await evidenceTab.getAttribute('aria-selected')) !== 'true') {
    await evidenceTab.click();
  }

  const regionScope = page.locator('.source-panel .evidence-region').first();
  await regionScope.waitFor({ timeout: 15000 });

  // 1a. Verify unretained Git citation (ev-git-unretained) in browser UI -> unavailable / missing_source.
  stage = "git evidence detail";
  const gitRow = regionScope.locator('li.evidence-row').filter({ hasText: 'ev-git-unretained' });
  await gitRow.locator('button.link').first().click();
  await page.waitForFunction(
    () => {
      const rows = Array.from(document.querySelectorAll('.source-panel .evidence-region li.evidence-row'));
      const row = rows.find(el => el.innerText.includes('ev-git-unretained'));
      return (
        row &&
        row.innerText.includes('작성자 선언(불변): reference_only(작성자 선언)') &&
        row.innerText.includes('서비스 가용성(별도): 마지막 확인 시점에 원본 없음(확인 시각은 별도 기록) · 사유 missing_source')
      );
    },
    null,
    { timeout: 15000 }
  );
  const gitCompareBox = gitRow.locator('input[type="checkbox"]');
  if (!(await gitCompareBox.isChecked())) {
    await gitCompareBox.check();
  }
  stage = "git verification presentation";
  await gitRow.getByRole('button', { name: '출처 근거 확인', exact: true }).click();
  await page.waitForFunction(
    () => {
      const rows = Array.from(document.querySelectorAll('.source-panel .evidence-region li.evidence-row'));
      const row = rows.find(el => el.innerText.includes('ev-git-unretained'));
      return (
        row &&
        row.innerText.includes('확인 결과: 사용 불가') &&
        row.innerText.includes('사유 missing_source') &&
        row.innerText.includes('작성자 선언(불변): reference_only(작성자 선언)')
      );
    },
    null,
    { timeout: 15000 }
  );

  // 1b. Verify retained selected_snapshot citation (ev-snap-retained) in browser UI -> matched / retained_snapshot.
  stage = "snapshot evidence detail";
  const snapRow = regionScope.locator('li.evidence-row').filter({ hasText: 'ev-snap-retained' });
  await snapRow.locator('button.link').first().click();
  await page.waitForFunction(
    expectedSha => {
      const rows = Array.from(document.querySelectorAll('.source-panel .evidence-region li.evidence-row'));
      const row = rows.find(el => el.innerText.includes('ev-snap-retained'));
      return (
        row &&
        row.innerText.includes('작성자 선언(불변): verified(작성자 선언)') &&
        row.innerText.includes('서비스 가용성(별도): 사용 가능 · 바이트 retained_snapshot') &&
        row.innerText.includes(expectedSha)
      );
    },
    input.retainedSha256,
    { timeout: 15000 }
  );
  const excerptText = await snapRow.locator('pre.evidence-excerpt').innerText();
  assert(
    excerptText.trim() === input.retainedText.trim(),
    'Retained snapshot inline excerpt mismatch in Evidence panel'
  );
  const snapCompareBox = snapRow.locator('input[type="checkbox"]');
  if (!(await snapCompareBox.isChecked())) {
    await snapCompareBox.check();
  }
  stage = "snapshot verification presentation";
  await snapRow.getByRole('button', { name: '출처 근거 확인', exact: true }).click();
  await page.waitForFunction(
    () => {
      const rows = Array.from(document.querySelectorAll('.source-panel .evidence-region li.evidence-row'));
      const row = rows.find(el => el.innerText.includes('ev-snap-retained'));
      return (
        row &&
        row.innerText.includes('확인 결과: 일치(기대 비교 — 사후 검증)') &&
        row.innerText.includes('작성자 선언(불변): verified(작성자 선언)')
      );
    },
    null,
    { timeout: 15000 }
  );
  stage = "snapshot verification history";
  await snapRow.getByRole('button', { name: '검증 이력 보기/숨기기', exact: true }).click();
  await page.waitForFunction(
    () => {
      const rows = Array.from(document.querySelectorAll('.source-panel .evidence-region li.evidence-row'));
      const row = rows.find(el => el.innerText.includes('ev-snap-retained'));
      return row && row.innerText.includes('사후 검증(posthoc)은 원본 제공 입력을 관측했다고 주장하지 않습니다.');
    },
    null,
    { timeout: 15000 }
  );

  // 1c. Inspect Ask tab on v_sourced: verify paused current-source missing question & boundary disclosures.
  stage = "saved question list";
  await page.locator('#reader-ask-tab').click();
  const askPanel = page.locator('section.ask-panel');
  await askPanel.waitFor({ timeout: 15000 });
  await page.waitForFunction(
    () => {
      const panel = document.querySelector('section.ask-panel');
      return panel && panel.innerText.includes('paused') && panel.innerText.includes('저장된 질문과 답변 읽기');
    },
    null,
    { timeout: 15000 }
  );
  await askPanel.getByRole('button', { name: '저장된 질문과 답변 읽기', exact: true }).first().click();
  stage = "saved question detail";
  const savedQuestion = page.locator('article[aria-label="저장된 질문 상세"]');
  await savedQuestion.waitFor({ timeout: 15000 });
  const savedQuestionText = await savedQuestion.innerText();
  assert(
    savedQuestionText.includes('원본 기준 current · target 확인된 원본 target 없음'),
    'Saved question did not report null target_set_id'
  );
  assert(
    savedQuestionText.includes(
      'unable_to_verify · The exact requested project-source selection is unavailable; no partial source request was dispatched.'
    ),
    'Saved question did not display unable_to_verify limitation'
  );
  assert(
    (await savedQuestion.locator('section[aria-label*="답변"]').count()) === 0,
    'Paused missing-source question unexpectedly rendered answer sections'
  );

  // Exact missing-selection proof.
  const expectedSelection = {
    source_intent: 'moving_target',
    version_id: input.fixture[0].version,
    source_mode: 'current',
    provider_id: 'codex',
    selections: [{ source_id: input.gitSourceId, paths: [{ path: 'notes/ask-missing.md' }] }],
  };
  const selectionDetails = savedQuestion.locator('details').filter({
    hasText: '실제로 캡처한 원본 target과 선택',
  });
  await selectionDetails.locator('summary').click();
  const displayedSelection = JSON.parse(await selectionDetails.locator('pre').innerText());
  assert(
    JSON.stringify(displayedSelection.requested_selection) === JSON.stringify(expectedSelection) &&
      Array.isArray(displayedSelection.targets) && displayedSelection.targets.length === 0,
    'Exact requested missing source/path absent from saved Question details'
  );
  const publicQuestion = await page.evaluate(async id => {
    const response = await fetch('/api/v1/questions/' + encodeURIComponent(id), {
      headers: { Authorization: 'Bearer ' + localStorage.getItem('haeryong.owner-session.v1') },
    });
    if (response.status !== 200) throw Error('Saved Question read failed');
    return (await response.json()).data;
  }, input.questionId);
  assert(
    JSON.stringify(publicQuestion.requested_selection) === JSON.stringify(expectedSelection),
    'Saved Question public read lost exact requested selection'
  );
  // End exact missing-selection proof.

  await page.waitForFunction(
    () => {
      const btn = document.querySelector('#ask-new');
      return btn && !btn.disabled;
    },
    null,
    { timeout: 15000 }
  );
  stage = "source/provider draft boundary";
  await page.locator('#ask-new').click();
  const askEditorText = await page.locator('div.ask-editor').innerText();
  assert(
    askEditorText.includes('provider 기능 지원과 계정·사용량 준비는 별개입니다.') &&
      askEditorText.includes('원본을 찾지 못하면 한계로 남깁니다. 다른 원본이나 설명만으로 코드 확인 성공을 대신하지 않습니다.') &&
      askEditorText.includes('과거 관측은 이번 읽기 성공을 보장하지 않습니다.') &&
      askEditorText.includes('보관 reference_only · 마지막 가용성 unavailable') &&
      askEditorText.includes('보관 selected_snapshot'),
    'Ask editor source/provider boundary disclosures incomplete'
  );

  // 1d. Navigate to second page of v_sourced.
  await navigateSecondPage(page, sourcedViewer, sourcedItem);

  // 2. Plain version without source metadata (v_plain): T-007-05-01 multi-page reading & absent metadata state.
  stage = "plain directory";
  const plainItem = input.fixture[1];
  const { viewer: plainViewer, boundary: plainBoundary } = await inspectFrameOnFirstPage(
    page,
    plainItem,
    input.expectedOrigins[1]
  );
  assert(
    sourcedBoundary.origin !== plainBoundary.origin,
    'Sourced and plain versions must use distinct isolated renderer origins'
  );

  await page.waitForFunction(
    () => {
      const header = document.querySelector('section.version-header')?.innerText || '';
      return (
        header.includes('출처 기준 알 수 없음/검증되지 않음') &&
        header.includes('공급된 소스/출처 메타데이터 없음 — 알 수 없음/검증되지 않음.')
      );
    },
    null,
    { timeout: 15000 }
  );

  const plainContextToggle = page.locator('#reader-context-toggle');
  if ((await plainContextToggle.getAttribute('aria-expanded')) !== 'true') {
    await plainContextToggle.click();
  }
  const plainEvidenceTab = page.locator('#reader-evidence-tab');
  if ((await plainEvidenceTab.getAttribute('aria-selected')) !== 'true') {
    await plainEvidenceTab.click();
  }
  await page.waitForFunction(
    () => {
      const panel = document.querySelector('section.source-panel')?.innerText || '';
      return panel.includes(
        '공급된 소스/근거 메타데이터 없음 — 출처 기준 알 수 없음/검증되지 않음. 페이지 읽기와 평범한 JavaScript는 그대로 사용할 수 있습니다.'
      );
    },
    null,
    { timeout: 15000 }
  );

  await navigateSecondPage(page, plainViewer, plainItem);

  // 3. Project Sources screen (/projects/{projectId}/sources) & Source Settings (/settings/projects?project={projectId}).
  stage = "project Sources boundary";
  await page.goto(input.base + '/projects/' + input.projectId + '/sources');
  await waitVerified(page);
  const sourcesPanel = page.locator('section[aria-label="프로젝트 출처"]');
  await sourcesPanel.waitFor({ timeout: 15000 });
  await page.waitForFunction(
    ({ gitId, snapId }) => {
      const text = document.querySelector('section[aria-label="프로젝트 출처"]')?.innerText || '';
      return (
        text.includes('기록된 관측만 표시합니다. 이 화면을 열어도 소스를 새로 읽지 않습니다.') &&
        text.includes(gitId) &&
        text.includes('보관 reference_only · 공개 대상 codex') &&
        text.includes('가용성 unavailable') &&
        text.includes(snapId) &&
        text.includes('보관 selected_snapshot · 공개 대상 codex')
      );
    },
    { gitId: input.gitSourceId, snapId: input.snapSourceId },
    { timeout: 15000 }
  );

  stage = "source settings boundary";
  await page.goto(input.base + '/settings/projects?project=' + input.projectId);
  await waitVerified(page);
  const settingsPanel = page.locator('section[aria-label="출처 설정"]');
  await settingsPanel.waitFor({ timeout: 15000 });
  await settingsPanel.locator('form.source-settings').waitFor({ timeout: 15000 });
  assert(
    (await settingsPanel.getByLabel('기본 소스 모드', { exact: true }).inputValue()) === 'pinned',
    'Expected project default source mode pinned'
  );
  assert(
    (await settingsPanel.getByLabel('기본 Provider ID', { exact: true }).inputValue()) === 'codex',
    'Expected project default provider ID codex'
  );
  // Exact approved-root proof.
  const approvedRootViews = [];
  for (const fields of await settingsPanel.locator('fieldset').all()) {
    const sourceId = await fields.getByLabel('출처 ID', { exact: true }).inputValue();
    const rootPath = await fields.getByLabel('승인할 원본 루트', { exact: true }).inputValue();
    const providerIds = await fields.getByLabel('공개할 Provider ID(쉼표 구분)', { exact: true }).inputValue();
    assert(rootPath === input.approvedRoots[sourceId], 'Approved source root changed');
    assert(providerIds === 'codex', 'Approved source provider disclosure changed');
    approvedRootViews.push({ source_id: sourceId, root_path: rootPath, provider_ids: ['codex'] });
  }
  assert(approvedRootViews.length === 2, 'Expected both approved source mappings');
  // End exact approved-root proof.

  const settingsText = await settingsPanel.innerText();
  assert(
    settingsText.includes(
      'Haeryong 원본 reader는 선택한 루트의 파일을 읽고 원본을 수정하지 않습니다. 제공자 연결과 계정·사용량 준비 상태는 AI 제공자 설정에서 확인하세요. 외부 에이전트는 소유자의 기존 도구·권한을 사용하며, 이 설정은 OS 읽기 격리 경계가 아닙니다.'
    ),
    'Source settings boundary notice missing'
  );

  // 4. Provider Screen (/settings/ai): T-007-05-02 unavailable provider executable & host-mode boundaries.
  stage = "provider boundary";
  await page.goto(input.base + '/settings/ai');
  await waitVerified(page);
  const providerScreen = page.locator('[data-provider-screen]');
  await providerScreen.waitFor({ timeout: 15000 });
  await page.locator('[data-provider-status]').waitFor({ timeout: 15000 });

  const providerStateText = await page.locator('[data-provider-state]').innerText();
  const providerReasonText = await page.locator('[data-provider-reason]').innerText();
  const providerFullText = await providerScreen.innerText();
  assert(
    providerStateText === '제공자 연결을 확인할 수 없습니다',
    'Unexpected provider state label: ' + providerStateText
  );
  assert(
    providerReasonText === '로컬 제공자 연결을 사용할 수 없습니다',
    'Unexpected provider reason label: ' + providerReasonText
  );
  assert(
    providerFullText.includes('호스트의 Codex 구독 연결 상태입니다. 서비스 기능 지원과 계정 준비는 별개입니다.') &&
      providerFullText.includes('생성 지원됨 · 질문 지원됨. 연결 확인은 요청의 완료나 원본 접근을 보장하지 않습니다.') &&
      providerFullText.includes('로그인·계정·결제 설정은 호스트의 Codex에서 관리합니다. 연결이 없어도 기존 문서 읽기와 Review는 계속 사용할 수 있습니다.'),
    'Provider screen boundary notices missing'
  );

  // 5. Authenticated browser API checks for /api/v1/providers and /api/v1/source-reads/{retainedReadId}.
  stage = "private raw source/provider/job API";
  const apiChecks = await page.evaluate(
    async ({ readId, jobId }) => {
      const token = localStorage.getItem('haeryong.owner-session.v1');
      const headers = { Authorization: 'Bearer ' + token };
      const provResp = await fetch('/api/v1/providers', { headers });
      const provBody = await provResp.json();
      const readResp = await fetch('/api/v1/source-reads/' + encodeURIComponent(readId), { headers });
      const readText = await readResp.text();
      const attemptsResp = await fetch('/api/v1/jobs/' + encodeURIComponent(jobId), { headers });
      const attempts = await attemptsResp.json();
      return {
        providerStatusHttp: provResp.status,
        providerData: provBody.data,
        retainedReadHttp: readResp.status,
        retainedReadETag: readResp.headers.get('etag'),
        attemptsHttp: attemptsResp.status,
        attempts: attempts.data,
        retainedReadText: readText,
      };
    },
    { readId: input.retainedReadId, jobId: input.questionJobId }
  );
  assert(
    apiChecks.providerStatusHttp === 200 &&
      apiChecks.providerData.provider_id === 'codex' &&
      apiChecks.providerData.state === 'provider_error' &&
      apiChecks.providerData.reason === 'transport_unavailable' &&
      apiChecks.providerData.authentication_mode === 'unknown',
    'Browser /api/v1/providers response mismatch'
  );
  assert(
    apiChecks.retainedReadHttp === 200 &&
      apiChecks.retainedReadETag === '"' + input.retainedSha256 + '"' &&
      apiChecks.attemptsHttp === 200 && apiChecks.attempts.provider_attempts.count === 0 &&
      apiChecks.retainedReadText === input.retainedText,
    'Browser /api/v1/source-reads/{id} response mismatch'
  );

  if (input.screenshot) {
    await page.screenshot({ path: input.screenshot, fullPage: true });
  }

  return {
    complete: true,
    modeLabel: input.modeLabel,
    sourcedRendererOrigin: sourcedBoundary.origin,
    plainRendererOrigin: plainBoundary.origin,
    multiPageSourcedVerified: true,
    multiPagePlainWithoutMetadataVerified: true,
    gitUnretainedPosthocOutcome: 'unavailable:missing_source',
    snapshotRetainedPosthocOutcome: 'matched:retained_snapshot',
    authoredCaptureStatusImmutable: true,
    currentModeMissingQuestionPaused: true,
    requestedSelection: publicQuestion.requested_selection,
    displayedSelection,
    approvedRootViews,
    sourcesScreenRecordedOnlyNoticeVerified: true,
    sourceSettingsBoundaryNoticeVerified: true,
    providerScreenState: apiChecks.providerData.state,
    providerScreenReason: apiChecks.providerData.reason,
    missingQuestionAttemptCount: apiChecks.attempts.provider_attempts.count,
    retainedReadExactBytesAndETag: true,
  };
  } catch (error) {
    let diagnostic;
    try {
      diagnostic = await page.evaluate(() => ({
        url: location.href,
        header: document.querySelector('section.version-header')?.innerText ?? null,
        evidence: document.querySelector('section.source-panel')?.innerText?.slice(0, 9000) ?? null,
        ask: document.querySelector('section.ask-panel')?.innerText?.slice(0, 5000) ?? null,
        service: document.querySelector('.service-state')?.getAttribute('data-state') ?? null,
      }));
      if (input.screenshot) await page.screenshot({path: input.screenshot, fullPage: true});
    } catch (snapshotError) { diagnostic = {snapshotError: String(snapshotError)}; }
    return {complete: false, stage, error: String(error), diagnostic};
  }
}
