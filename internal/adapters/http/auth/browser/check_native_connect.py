"""TASK-003-05: shared Connect/transfer UI checks over a labelled synthetic NativePort.
This does not establish Go secret custody or actual Wails/native operation evidence.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import threading
import urllib.parse
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--browser-cli", type=Path, required=True)
parser.add_argument("--receipt", type=Path, required=True)
parser.add_argument("--screenshot", type=Path)
args = parser.parse_args()
REPO = Path(__file__).resolve().parents[5]
receipt = args.receipt.resolve()
assert receipt.parent.is_dir(), "receipt parent must exist"
volume = Path("/Volumes/RootKernel")
base = volume / "tmp" if os.path.ismount(volume) and os.access(volume, os.W_OK) else Path(tempfile.gettempdir())
os.umask(0o077)
work = Path(tempfile.mkdtemp(prefix="h5n-", dir=base))
env = os.environ | {"TMPDIR": str(work), "PYTHONDONTWRITEBYTECODE": "1"}
cli = str(args.browser_cli.resolve(strict=True))
session = work.name
proof = {"scope": __doc__, "runtime": str(work), "playwright_session": session, "complete": False, "cases": []}
server = None
server_thread = None
opened = False


def fingerprints():
    paths = sorted(p for p in (REPO / "frontend").rglob("*")
                   if p.is_file() and not any(part in {"node_modules", "dist", "dist-native", ".playwright"} for part in p.relative_to(REPO / "frontend").parts))
    paths.append(Path(__file__).resolve())
    rows = {str(p.relative_to(REPO)): hashlib.sha256(p.read_bytes()).hexdigest() for p in paths}
    return hashlib.sha256(json.dumps(rows, sort_keys=True).encode()).hexdigest()


def call(argv, result=False):
    process = subprocess.run([cli, "--session=" + session, *argv], cwd=work, env=env,
                             capture_output=True, text=True, timeout=180)
    if process.returncode or "### Error" in process.stdout:
        diagnostic = process.stdout.split("### Error", 1)[1].split("###", 1)[0] if "### Error" in process.stdout else process.stderr or process.stdout
        proof["browser_error"] = diagnostic[:1800]
        raise AssertionError("browser operation failed; see bounded fixture receipt")
    if result:
        match = re.search(r"### Result\s*\n", process.stdout)
        if not match:
            proof["browser_error"] = "missing result section: " + process.stdout[-500:]
            raise AssertionError("browser operation returned no result")
        return json.JSONDecoder().raw_decode(process.stdout[match.end():].lstrip())[0]


def js(code):
    return call(["run-code", code], True)


def require(name, condition, detail=None):
    if not condition:
        raise AssertionError(name + ": " + json.dumps(detail, ensure_ascii=False, default=str)[:600])
    proof["cases"].append({"name": name, "ok": True})


before = fingerprints()
try:
    browser_inputs = {}
    for name in ("b", "c"):
        directory = work / ("browser-input-" + name)
        directory.mkdir()
        (directory / "page.html").write_text("<!doctype html><title>" + name + "</title>")
        browser_inputs[name] = str(directory)
    harness_dir = work / "harness-dist"
    build = subprocess.run([str(REPO / "frontend/node_modules/.bin/vite"), "build", "--config", "vite.harness.config.ts"],
                           cwd=REPO / "frontend", env=env | {"HAEYONG_HARNESS_OUT_DIR": str(harness_dir)},
                           capture_output=True, text=True, timeout=420)
    if build.returncode:
        proof["build_error"] = (build.stderr or build.stdout)[-1800:]
        raise AssertionError("harness build failed")

    class Handler(SimpleHTTPRequestHandler):
        def __init__(self, *argv, **kwargs):
            super().__init__(*argv, directory=str(harness_dir), **kwargs)

        def log_message(self, *argv):
            pass

        def translate_path(self, path):
            path = urllib.parse.urlsplit(path).path
            if path.startswith("/harness/"):
                path = path[len("/harness"):]
            return super().translate_path(path)

        def end_headers(self):
            self.send_header("Cache-Control", "no-store")
            super().end_headers()

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    server_thread = threading.Thread(target=server.serve_forever, daemon=True)
    server_thread.start()
    origin = "http://127.0.0.1:" + str(server.server_address[1])
    url = origin + "/harness/harness.html?mode=nativepairing"
    proof["server_origin"] = origin
    opened = True
    call(["open", url])
    identity = js("""async page => {
      page.__nativeConnectErrors = [];
      page.on('pageerror', error => page.__nativeConnectErrors.push(error.message));
      page.on('console', message => { if (message.type() === 'error') page.__nativeConnectErrors.push(message.text()); });
      await page.setViewportSize({width:1440,height:900});
      await page.getByRole('heading',{name:'네이티브 페어링 harness (검증 전용)'}).waitFor();
      await page.getByRole('button',{name:'연결 코드 요청'}).waitFor();
      return {url:page.url(),title:await page.title(),overlay:await page.locator('vite-error-overlay').count()};
    }""")
    require("page_identity", identity["url"] == url and identity["title"] == "Haeryong 진입 지점 harness (검증 전용)", identity)
    require("no_framework_overlay", identity["overlay"] == 0, identity)
    require("native_remember_hidden", js("""async page => !(await page.getByRole('checkbox',{name:'이 장치 기억'}).count())"""))
    require("persistent_policy_visible", js("""async page => await page.getByText('보호된 로컬 저장소에 연결 자격을 저장합니다',{exact:false}).count()>0"""))
    require("stopped_daemon_visible", js("""async page => (await page.getByTestId('daemon-state').textContent()).includes('실행 중이 아닙니다')"""))
    started = js("""async page => {
      await page.getByRole('button',{name:'데몬 시작',exact:true}).click();
      await page.getByTestId('daemon-state').filter({hasText:'로컬 서비스가 확인되었습니다.'}).waitFor();
      return await page.getByTestId('connect-endpoint').textContent();
    }""")
    require("explicit_start_displays_service_not_ui_origin", started == "http://127.0.0.1:9270", started)

    # Hold a REAL in-flight synthetic poll across keyboard Stop, then release
    # its approved reply; local ownership must prevent a late adoption.
    stopped = js("""async page => {
      await page.evaluate(()=>{window.__nativePairingHarness.script.holdPoll=true;});
      await page.getByRole('button',{name:'연결 코드 요청'}).focus();
      await page.keyboard.press('Enter');
      await page.getByText('승인을 기다리는 중입니다',{exact:false}).waitFor();
      await page.waitForFunction(()=>window.__nativePairingHarness.script.pendingPoll);
      const code=await page.locator('.code-display').textContent();
      await page.keyboard.press('Escape');
      await page.getByText('대기를 중단했습니다',{exact:false}).waitFor();
      await page.evaluate(()=>{
        const port=window.__nativePairingHarness;
        port.script.poll='approved';port.script.holdPoll=false;port.releaseAllPolls();
      });
      await page.waitForFunction(()=>!window.__nativePairingHarness.script.pendingPoll);
      await page.waitForTimeout(2300);
      return await page.evaluate(()=>{const s=window.__nativePairingHarness.script;return {polls:s.pollCalls,abandons:s.abandonCalls,adopts:s.adoptCalls};}).then(s=>({...s,comparison:code}));
    }""")
    require("keyboard_comparison_code", stopped["comparison"] == "246810", stopped)
    require("escape_abandons_once", stopped["abandons"] == 1, stopped)
    require("late_approved_poll_never_adopts", stopped["adopts"] == 0 and stopped["polls"] == 1, stopped)

    failed = js("""async page => {
      await page.evaluate(()=>{const s=window.__nativePairingHarness.script;s.poll='pending';s.adopt='store-failed';});
      await page.getByRole('button',{name:'연결 코드 요청'}).focus();
      await page.keyboard.press('Enter');
      await page.getByText('승인을 기다리는 중입니다',{exact:false}).waitFor();
      await page.evaluate(()=>{window.__nativePairingHarness.script.poll='approved';});
      await page.getByText('연결 자격을 저장하지 못했습니다',{exact:false}).waitFor({timeout:15000});
      return await page.getByTestId('native-pairing-harness').getAttribute('data-phase');
    }""")
    require("typed_store_failure_keeps_unauthenticated", failed == "unauthenticated", failed)

    unknown = js("""async page => {
      await page.evaluate(()=>{const s=window.__nativePairingHarness.script;s.poll='pending';s.abandonResults=['network-failed'];});
      await page.getByRole('button',{name:'연결 코드 요청'}).click();
      await page.getByText('승인을 기다리는 중입니다',{exact:false}).waitFor();
      await page.keyboard.press('Escape');
      await page.getByText('대기 중단 여부를 확인하지 못했습니다',{exact:false}).waitFor();
      return !(await page.getByText('대기를 중단했습니다',{exact:false}).count());
    }""")
    require("unknown_stop_never_claims_stopped", unknown)

    raced = js("""async page => {
      await page.evaluate(()=>{const s=window.__nativePairingHarness.script;s.poll='pending';s.adopt='ok';s.abandonResults=['already-adopted'];});
      await page.getByRole('button',{name:'연결 코드 요청'}).click();
      await page.getByText('승인을 기다리는 중입니다',{exact:false}).waitFor();
      await page.keyboard.press('Escape');
      await page.getByText('연결이 이미 완료되었습니다',{exact:false}).waitFor();
      await page.getByRole('heading',{name:'연결된 소유자'}).waitFor();
      return await page.getByTestId('native-pairing-harness').getAttribute('data-phase');
    }""")
    require("already_adopted_stop_attaches_truthfully", raced == "verified", raced)

    happy = js("""async page => {
      await page.getByRole('button',{name:'로그아웃',exact:true}).click();
      await page.getByRole('button',{name:'연결 코드 요청'}).waitFor();
      await page.evaluate(()=>{const s=window.__nativePairingHarness.script;s.poll='pending';s.adopt='ok';s.abandonResults=[];});
      const before=await page.evaluate(()=>window.__nativePairingHarness.script.adoptCalls);
      await page.getByRole('button',{name:'연결 코드 요청'}).click();
      await page.getByText('승인을 기다리는 중입니다',{exact:false}).waitFor();
      await page.evaluate(()=>{window.__nativePairingHarness.script.poll='approved';});
      await page.getByRole('heading',{name:'연결된 소유자'}).waitFor({timeout:15000});
      const after=await page.evaluate(()=>window.__nativePairingHarness.script.adoptCalls);
      return {adoptions:after-before,phase:await page.getByTestId('native-pairing-harness').getAttribute('data-phase')};
    }""")
    require("relogin_tokenless_approved_adopts_once", happy["adoptions"] == 1 and happy["phase"] == "verified", happy)
    proof["browser"] = js("async page => await page.evaluate(()=>navigator.userAgent)")
    errors = js("async page => page.__nativeConnectErrors")
    require("no_page_or_console_errors", errors == [], errors)
    if args.screenshot:
        js("async page => {await page.screenshot({path:" + json.dumps(str(args.screenshot.resolve())) + "});return true;}")

    # ---- TASK-003-05 M2 C2: actual production App foreground/pin regressions ----
    # The real App mounts its own visibility handler (verify) while Connect's
    # own listener resumes polling, so one foreground event exercises BOTH
    # production paths at once over the labelled synthetic port. open() drives
    # a fresh page, so each driven page registers its own error capture.
    app_url = origin + "/harness/harness.html?mode=nativeapp"
    call(["open", app_url])

    def attach_error_capture():
        js("""async page => {
          if (!page.__nativeConnectErrors) {
            page.__nativeConnectErrors = [];
            page.on('pageerror', error => page.__nativeConnectErrors.push(error.message));
            page.on('console', message => { if (message.type() === 'error') page.__nativeConnectErrors.push(message.text()); });
          }
          return true;
        }""")

    attach_error_capture()
    app_identity = js("""async page => {
      await page.getByRole('heading',{name:'Haeryong 연결'}).waitFor();
      await page.getByTestId('daemon-state').waitFor();
      return await page.evaluate(() => ({harness: !!window.__nativePairingHarness, attachAttempts: window.__nativePairingHarness.script.attachAttempts}));
    }""")
    require("app_mode_mounts_real_shell_unauthenticated", app_identity["harness"] and app_identity["attachAttempts"] >= 1, app_identity)

    # Real tab visibility is preferred; headless Chrome may not emit real
    # visibilitychange events, in which case the bounded proof dispatches a
    # clearly LABELLED visibilitychange DOM event through the REAL production
    # App handler. Physical/native focus is never claimed.
    real_visibility = js("""async page => {
      const other = await page.context().newPage();
      await other.bringToFront();
      await page.waitForTimeout(250);
      const hidden = await page.evaluate(() => document.hidden);
      await other.close();
      await page.bringToFront();
      await page.waitForTimeout(250);
      return hidden;
    }""")
    proof["real_tab_visibility"] = bool(real_visibility)
    if real_visibility:
        proof["visibility_route"] = "real_tab_switch"
    else:
        proof["visibility_route"] = "labelled_dispatched_visibility_event_through_real_app_handler"

    def foreground_recheck():
        if real_visibility:
            js("""async page => {
              const other = await page.context().newPage();
              await other.bringToFront();
              await page.waitForTimeout(250);
              await other.close();
              await page.bringToFront();
              await page.waitForTimeout(250);
              return true;
            }""")
        else:
            js("""async page => {
              await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')));
              await page.waitForTimeout(400);
              return true;
            }""")

    # F1a: an ordinary no-credential foreground recheck must not destroy the
    # still-owned held pairing poll (the pre-C2 mapping invalidated the
    # transport era and ended the attempt as issuanceLost). Connect's own
    # visibility poll may legitimately hold a SECOND poll of the same attempt;
    # exactly one adoption must still own the outcome.
    f1a_before = js("""async page => {
      await page.evaluate(() => { const s=window.__nativePairingHarness.script; s.holdPoll=true; s.poll='pending'; });
      const before = await page.evaluate(() => ({attach: window.__nativePairingHarness.script.attachAttempts, pair: window.__nativePairingHarness.script.pairCalls}));
      await page.getByRole('button',{name:'연결 코드 요청'}).click();
      await page.getByText('승인을 기다리는 중입니다',{exact:false}).waitFor();
      await page.waitForFunction(() => window.__nativePairingHarness.script.pendingPoll);
      return before;
    }""")
    foreground_recheck()
    f1a_held = js("""async page => {
      await page.waitForTimeout(600);
      return await page.evaluate(async () => {
        const s=window.__nativePairingHarness.script;
        const text=document.body.textContent||'';
        return {waiting: text.includes('승인을 기다리는 중입니다')?1:0, issuanceLost: text.includes('연결이 확인되지 않았습니다')?1:0, held: s.pendingPoll, polls: s.pollCalls, attaches: s.attachAttempts};
      });
    }""")
    require("no_credential_foreground_recheck_ran", f1a_held["attaches"] > f1a_before["attach"], {"before": f1a_before, "after": f1a_held})
    require("original_held_poll_survives_foreground_recheck", f1a_held["waiting"] == 1 and f1a_held["held"] and f1a_held["issuanceLost"] == 0, f1a_held)
    f1a_done = js("""async page => {
      await page.evaluate(() => { const p=window.__nativePairingHarness; p.script.poll='approved'; p.script.holdPoll=false; p.releaseAllPolls(); });
      await page.locator('.service-state[data-state="verified"]').waitFor({timeout:15000});
      return await page.evaluate(() => { const s=window.__nativePairingHarness.script; return {pairs:s.pairCalls, adopts:s.adoptCalls, polls:s.pollCalls}; });
    }""")
    require("overlapping_approved_replies_keep_single_adoption", f1a_done["pairs"] == 1 and f1a_done["adopts"] == 1, f1a_done)

    # F1b: another device revokes the live Go credential A. The FIRST loss
    # still cleans fully (dispose + unauthenticated); Go retains the dead A,
    # so foreground rechecks re-observe SESSION_REVOKED while the owner pairs
    # B — those repeats must be idempotent and keep B's held poll alive.
    f1b_revoke = js("""async page => {
      await page.evaluate(() => {
        const s=window.__nativePairingHarness.script;
        // Another device revokes the live credential; Go retains the dead
        // token/file, so attach also answers SESSION_REVOKED from now on.
        s.sessionStatus='revoked'; s.attachStatus='revoked';
      });
      return await page.evaluate(() => window.__nativePairingHarness.script.disposeCalls);
    }""")
    foreground_recheck()
    f1b_first = js("""async page => {
      await page.getByRole('button',{name:'연결 코드 요청'}).waitFor({timeout:15000});
      await page.evaluate(() => { window.__nativePairingHarness.script.sessionStatus=''; });
      return await page.evaluate(() => window.__nativePairingHarness.script.disposeCalls);
    }""")
    require("first_revocation_still_cleans_fully", f1b_first == f1b_revoke + 1, {"before": f1b_revoke, "after": f1b_first})
    f1b_before = js("""async page => {
      await page.evaluate(() => { const s=window.__nativePairingHarness.script; s.holdPoll=true; s.poll='pending'; });
      const before = await page.evaluate(() => ({attach: window.__nativePairingHarness.script.attachAttempts, pair: window.__nativePairingHarness.script.pairCalls, adopts: window.__nativePairingHarness.script.adoptCalls, disposes: window.__nativePairingHarness.script.disposeCalls}));
      await page.getByRole('button',{name:'연결 코드 요청'}).click();
      await page.getByText('승인을 기다리는 중입니다',{exact:false}).waitFor();
      await page.waitForFunction(() => window.__nativePairingHarness.script.pendingPoll);
      return before;
    }""")
    foreground_recheck()
    f1b_held = js("""async page => {
      await page.waitForTimeout(600);
      return await page.evaluate(() => {
        const s=window.__nativePairingHarness.script;
        const text=document.body.textContent||'';
        return {waiting: text.includes('승인을 기다리는 중입니다')?1:0, issuanceLost: text.includes('연결이 확인되지 않았습니다')?1:0, held: s.pendingPoll, attaches: s.attachAttempts, disposes: s.disposeCalls};
      });
    }""")
    require("repeated_revoked_recheck_ran_without_redisposal", f1b_held["attaches"] > f1b_before["attach"] and f1b_held["disposes"] == f1b_before["disposes"], {"before": f1b_before, "after": f1b_held})
    require("new_pair_poll_survives_repeated_revoked_recheck", f1b_held["waiting"] == 1 and f1b_held["held"] and f1b_held["issuanceLost"] == 0, f1b_held)
    f1b_done = js("""async page => {
      await page.evaluate(() => { const p=window.__nativePairingHarness; p.script.poll='approved'; p.script.holdPoll=false; p.releaseAllPolls(); });
      await page.locator('.service-state[data-state="verified"]').waitFor({timeout:15000});
      return await page.evaluate(() => ({pairs: window.__nativePairingHarness.script.pairCalls, adopts: window.__nativePairingHarness.script.adoptCalls}));
    }""")
    require("new_pair_adopts_once_after_repeated_revoked_rechecks", f1b_done["pairs"] - f1b_before["pair"] == 1 and f1b_done["adopts"] - f1b_before["adopts"] == 1, {"before": f1b_before, "after": f1b_done})

    # Console/page health on the driven App page before the pin-error pages
    # open their own fresh documents (each pin page carries its own asserts).
    app_errors = js("async page => page.__nativeConnectErrors")
    require("no_page_or_console_errors_app_foreground", app_errors == [], app_errors)

    # ---- TASK-003-05 M3: shared-panel native acquisition journeys over the
    # labelled in-memory synthetic FS (actual Wails pickers belong to M4). ----
    library_ready = js("""async page => {
      await page.evaluate(() => { history.pushState({}, "", "/library"); window.dispatchEvent(new PopStateEvent("popstate")); });
      await page.getByTestId('library-document').waitFor({timeout:15000});
      return true;
    }""")
    require("library_mounts_with_synthetic_data", library_ready)

    # ZIP download: native consent copy, no-conflict publication with the
    # honest durability warning, and the consented-replace conflict path.
    download_warned = js("""async page => {
      await page.evaluate(() => { window.__nativePairingHarness.script.destDurability = 'unconfirmed'; });
      await page.getByRole('button',{name:'원본 디렉터리 내려받기'}).click();
      await page.getByRole('button',{name:'내려받기 시작'}).click();
      const consent = await page.getByTestId('download-consent').textContent();
      await page.getByRole('checkbox').check();
      await page.getByRole('button',{name:'동의하고 계속'}).click();
      await page.getByRole('button',{name:'저장 위치 선택 (시스템 저장 대화상자)'}).click();
      await page.getByTestId('download-done').waitFor({timeout:15000});
      const done = await page.getByTestId('download-done').textContent();
      const script = await page.evaluate(() => {
        const s = window.__nativePairingHarness.script;
        return { intents: s.destIntents, writes: s.destWriteCalls, closes: s.destCloseCalls };
      });
      return {
        consent,
        nativeConsent: (consent||'').includes('기존 파일은 절대 덮어쓰지 않습니다'),
        browserOnlyConsentAbsent: !(consent||'').includes('기존 존재 여부를 감지하거나'),
        done,
        warned: (done||'').includes('디스크 동기화 확인에 실패'),
        intents: script.intents,
        writes: script.writes,
        closes: script.closes,
      };
    }""")
    require("native_zip_download_publishes_with_durability_warning",
            download_warned["nativeConsent"] and download_warned["browserOnlyConsentAbsent"]
            and download_warned["warned"] and download_warned["writes"] >= 2 and download_warned["closes"] == 1
            and len(download_warned["intents"]) == 1 and "expected" not in (download_warned["intents"][0] or {}),
            download_warned)

    conflict_replaced = js("""async page => {
      await page.evaluate(() => {
        const s = window.__nativePairingHarness.script;
        s.destExists = true; s.destDurability = 'confirmed';
      });
      await page.getByRole('button',{name:'새 내려받기 시작 (동의 초기화)'}).click();
      await page.getByRole('button',{name:'내려받기 시작'}).click();
      await page.getByRole('checkbox').check();
      await page.getByRole('button',{name:'동의하고 계속'}).click();
      await page.getByRole('button',{name:'저장 위치 선택 (시스템 저장 대화상자)'}).click();
      await page.getByTestId('download-conflict').waitFor({timeout:15000});
      await page.getByRole('button',{name:'동의하고 교체'}).click();
      await page.getByTestId('download-done').waitFor({timeout:15000});
      const done = await page.getByTestId('download-done').textContent();
      const script = await page.evaluate(() => {
        const s = window.__nativePairingHarness.script;
        return { lastIntent: s.destIntents[s.destIntents.length-1], closes: s.destCloseCalls };
      });
      return { done, lastIntent: script.lastIntent, closes: script.closes };
    }""")
    require("existing_target_conflict_requires_explicit_replace_consent",
            bool(conflict_replaced["lastIntent"]) and conflict_replaced["lastIntent"].get("replace") is True
            and conflict_replaced["closes"] == 2 and "디스크 동기화" not in (conflict_replaced["done"] or ""),
            conflict_replaced)

    # Native import acquisition: the shared control plans the owner-selected
    # tree (pages collected, full-tree representation disclosed) without any
    # second engine; no dispatch happens in this labelled journey.
    import_planned = js("""async page => {
      await page.evaluate(() => {
        const s = window.__nativePairingHarness.script;
        s.fsTree = [
          { path: '.hidden', kind: 'file', size: 2 },
          { path: 'empty', kind: 'directory' },
          { path: 'link', kind: 'symlink', link_target: '../x' },
          { path: 'nested/page.html', kind: 'file', size: 5 },
        ];
        s.fsFiles = { '.hidden': [1, 2], 'nested/page.html': [9, 8, 7, 6, 5] };
      });
      await page.getByRole('button',{name:'가져오기',exact:true}).click();
      await page.getByRole('button',{name:'시스템 선택기로 선택'}).click();
      await page.getByText('네이티브 선택은 빈 디렉터리와 심볼릭 링크를 포함해 선택한 트리를 그대로 반영합니다.',{exact:false}).waitFor({timeout:15000});
      const start = page.getByRole('button',{name:'가져오기 시작'});
      return { enabled: !(await start.isDisabled()) };
    }""")
    require("native_import_selection_plans_shared_input", import_planned["enabled"], import_planned)

    # ---- TASK-003-05 M3 C2: failure-path honesty and late-choice ownership ----
    # Post-probe conflict at close time: the target appeared between the
    # probe and publication; the original stays untouched and the panel
    # surfaces the EXPLICIT conflict recovery (consented replace), not a
    # generic incomplete.
    close_conflict = js("""async page => {
      const before = await page.evaluate(() => window.__nativePairingHarness.script.destCloseCalls);
      await page.evaluate(() => {
        const s = window.__nativePairingHarness.script;
        s.destExists = false; s.destDurability = 'confirmed'; s.destCloseConflictsOnce = true;
      });
      await page.getByRole('button',{name:'새 내려받기 시작 (동의 초기화)'}).click();
      await page.getByRole('button',{name:'내려받기 시작'}).click();
      await page.getByRole('checkbox').check();
      await page.getByRole('button',{name:'동의하고 계속'}).click();
      await page.getByRole('button',{name:'저장 위치 선택 (시스템 저장 대화상자)'}).click();
      await page.getByTestId('download-conflict').waitFor({timeout:15000});
      await page.getByRole('button',{name:'동의하고 교체'}).click();
      await page.getByTestId('download-done').waitFor({timeout:15000});
      const after = await page.evaluate(() => window.__nativePairingHarness.script.destCloseCalls);
      const script = await page.evaluate(() => {
        const s = window.__nativePairingHarness.script;
        return { lastIntent: s.destIntents[s.destIntents.length-1] };
      });
      return { done: await page.getByTestId('download-done').textContent(), closesDelta: after - before, lastIntent: script.lastIntent };
    }""")
    require("post_probe_close_conflict_surfaces_explicit_replace_recovery",
            close_conflict["closesDelta"] == 2 and close_conflict["lastIntent"] and close_conflict["lastIntent"].get("replace") is True,
            close_conflict)

    # A failed abort on the failed-write path preserves the typed exact temp
    # path beside the primary incomplete outcome.
    cleanup_path = js("""async page => {
      await page.evaluate(() => {
        const s = window.__nativePairingHarness.script;
        s.destAbortFailsTemp = '/Downloads/.haeryong-x01-.partial';
        s.packageFailWrite = true;
      });
      await page.getByRole('button',{name:'새 내려받기 시작 (동의 초기화)'}).click();
      await page.getByRole('button',{name:'내려받기 시작'}).click();
      await page.getByRole('checkbox').check();
      await page.getByRole('button',{name:'동의하고 계속'}).click();
      await page.getByRole('button',{name:'저장 위치 선택 (시스템 저장 대화상자)'}).click();
      await page.getByTestId('download-incomplete').waitFor({timeout:15000});
      const detail = await page.getByTestId('download-incomplete').textContent();
      await page.evaluate(() => {
        const s = window.__nativePairingHarness.script;
        s.destAbortFailsTemp = null;
        s.packageFailWrite = false;
      });
      return { detail, showsPath: (detail||'').includes('/Downloads/.haeryong-x01-.partial') };
    }""")
    require("failed_abort_cleanup_keeps_exact_temp_path_visible", cleanup_path["showsPath"], cleanup_path)

    # An unobserved close (non-typed failure) classifies as the typed
    # uncertain outcome with the truthful retry notice.
    uncertain = js("""async page => {
      await page.evaluate(() => { window.__nativePairingHarness.script.destCloseUncertain = true; });
      await page.getByRole('button',{name:'새 내려받기 시작 (동의 초기화)'}).click();
      await page.getByRole('button',{name:'내려받기 시작'}).click();
      await page.getByRole('checkbox').check();
      await page.getByRole('button',{name:'동의하고 계속'}).click();
      await page.getByRole('button',{name:'저장 위치 선택 (시스템 저장 대화상자)'}).click();
      await page.getByTestId('download-incomplete').waitFor({timeout:15000});
      const detail = await page.getByTestId('download-incomplete').textContent();
      await page.evaluate(() => { window.__nativePairingHarness.script.destCloseUncertain = false; });
      return { detail, uncertain: (detail||'').includes('완료 여부를 확인할 수 없습니다') };
    }""")
    require("unobserved_close_classifies_typed_uncertain", uncertain["uncertain"], uncertain)

    # Late destination picker + route leave: the held choice resolves after
    # the panel unmounted; its abort fails with a typed path and the runtime
    # cleanup alert (App-level) shows the EXACT path, with no download.
    late_dest = js("""async page => {
      const counts = await page.evaluate(() => {
        const s = window.__nativePairingHarness.script;
        return { opens: s.destIntents.length, aborts: s.destAbortCalls };
      });
      await page.evaluate(() => { window.__nativePairingHarness.script.holdDestPicker = true; });
      const acquire = page.getByRole('button',{name:'저장 위치 선택 (시스템 저장 대화상자)'}).click().then(() => true).catch(() => false);
      await page.waitForTimeout(400);
      await page.evaluate(() => {
        window.__nativePairingHarness.script.destAbortFailsTemp = '/Downloads/.haeryong-late-.partial';
        history.pushState({}, "", "/");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });
      await page.waitForTimeout(300);
      await page.evaluate(() => {
        const p = window.__nativePairingHarness;
        p.script.holdDestPicker = false;
        p.releaseDestPickers();
      });
      await acquire;
      await page.getByTestId('cleanup-alert').waitFor({timeout:15000});
      const alertText = await page.getByTestId('cleanup-alert').textContent();
      const after = await page.evaluate(() => {
        const s = window.__nativePairingHarness.script;
        return { opens: s.destIntents.length, aborts: s.destAbortCalls };
      });
      await page.evaluate(() => { window.__nativePairingHarness.script.destAbortFailsTemp = null; });
      return {
        alertText,
        showsPath: (alertText||'').includes('/Downloads/.haeryong-late-.partial'),
        newOpens: after.opens - counts.opens,
        newAborts: after.aborts - counts.aborts,
      };
    }""")
    require("late_destination_pick_after_leave_routes_cleanup_path_alert",
            late_dest["showsPath"] and late_dest["newOpens"] == 0 and late_dest["newAborts"] >= 1,
            late_dest)

    # Late source picker + leave: the held import selection releases its
    # handle after the control is gone; no panel state resurrection, no
    # download-side activity, no cleanup alert (the release succeeds).
    late_source = js("""async page => {
      await page.evaluate(() => {
        history.pushState({}, "", "/library");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });
      await page.getByRole('button',{name:'가져오기',exact:true}).waitFor({timeout:15000});
      const before = await page.evaluate(() => ({
        releases: window.__nativePairingHarness.script.sourceReleaseCalls,
        alerts: document.querySelectorAll('[data-testid="cleanup-alert"]').length,
      }));
      await page.evaluate(() => { window.__nativePairingHarness.script.holdSourcePicker = true; });
      await page.getByRole('button',{name:'가져오기',exact:true}).click();
      const pick = page.getByRole('button',{name:'시스템 선택기로 선택'}).click().then(() => true).catch(() => false);
      await page.waitForTimeout(400);
      await page.evaluate(() => {
        history.pushState({}, "", "/");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });
      await page.waitForTimeout(300);
      await page.evaluate(() => {
        const p = window.__nativePairingHarness;
        p.script.holdSourcePicker = false;
        p.releaseSourcePickers();
      });
      await pick;
      await page.waitForTimeout(400);
      const after = await page.evaluate(() => ({
        releases: window.__nativePairingHarness.script.sourceReleaseCalls,
        alerts: document.querySelectorAll('[data-testid="cleanup-alert"]').length,
      }));
      return { released: after.releases - before.releases, alertsAdded: after.alerts - before.alerts };
    }""")
    require("late_source_pick_after_leave_releases_handle_quietly",
            late_source["released"] >= 1 and late_source["alertsAdded"] == 0,
            late_source)

    # Exact await-release barrier (Import): H0 owned → native pick H1 valid
    # → H1 installed, H0's RELEASE is held → browser B picked during the
    # await (epoch advance + B's own release of H1) → H0 release resolves →
    # the stale flow must record NO UI, release nothing extra, and preserve
    # B's browser note. Distinct handles prove exact ownership.
    import_release_barrier = js("""async page => {
      await page.evaluate(() => {
        history.pushState({}, "", "/library");
        window.dispatchEvent(new PopStateEvent("popstate"));
        const s = window.__nativePairingHarness.script;
        s.holdSourcePicker = false; s.holdDestPicker = false; s.holdReleaseHandles = [];
        s.sourceReleaseTrace = []; s.sourceChooseTrace = [];
      });
      await page.getByRole('button',{name:'가져오기',exact:true}).waitFor({timeout:15000});
      // First native pick completes normally → H0 owned.
      await page.getByRole('button',{name:'가져오기',exact:true}).click();
      await page.getByRole('button',{name:'시스템 선택기로 선택'}).click();
      await page.getByText('네이티브 선택은 빈 디렉터리와 심볼릭 링크를 포함해 선택한 트리를 그대로 반영합니다.',{exact:false}).waitFor({timeout:15000});
      const firstChoose = await page.evaluate(() => window.__nativePairingHarness.script.sourceChooseTrace);
      const h0 = firstChoose[firstChoose.length - 1];
      // Second native pick: H1 valid; H0's release is HELD inside the
      // releaseNativeSource await (exact post-install barrier).
      await page.evaluate((handle) => { window.__nativePairingHarness.script.holdReleaseHandles = [handle]; }, h0);
      const secondPick = page.getByRole('button',{name:'시스템 선택기로 선택'}).click().then(() => true).catch(() => false);
      // DETERMINISTIC barrier: wait until H1 is chosen AND the H0 release
      // is actually captured in the hold queue (post-install, pre-resume).
      await page.waitForFunction((h0Handle) => {
        const p = window.__nativePairingHarness;
        return p.script.sourceChooseTrace.length === 2 && p.script.sourceChooseTrace[1] !== h0Handle
          && p.releaseHolds.some((held) => held.handle === h0Handle);
      }, h0, { timeout: 15000 });
      const secondChoose = await page.evaluate(() => window.__nativePairingHarness.script.sourceChooseTrace);
      const h1 = secondChoose[secondChoose.length - 1];
      // While the H0 release is held, a BROWSER folder input supersedes the
      // intent: epoch advances and the browser path releases H1 itself.
      await page.locator('input[webkitdirectory]').first().setInputFiles(BROWSER_INPUT);
      await page.waitForTimeout(300);
      const browserNoteVisible = await page.getByText('폴더 선택(FileList)은 빈 디렉터리와 심볼릭 링크를 표현하지 못합니다.',{exact:false}).count();
      // Release H0: the stale native flow resumes after the await.
      await page.evaluate((handle) => { window.__nativePairingHarness.releaseHeldRelease(handle); }, h0);
      await secondPick;
      await page.waitForTimeout(400);
      const state = await page.evaluate(() => {
        const s = window.__nativePairingHarness.script;
        return { releaseTrace: [...s.sourceReleaseTrace], chooseTrace: [...s.sourceChooseTrace] };
      });
      const nativeNoteStillVisible = await page.getByText('네이티브 선택은 빈 디렉터리와 심볼릭 링크를 포함해 선택한 트리를 그대로 반영합니다.',{exact:false}).count();
      return {
        h0, h1,
        releaseTrace: state.releaseTrace,
        chooseCount: state.chooseTrace.length,
        browserNoteWasVisible: browserNoteVisible > 0,
        nativeNoteStillVisible: nativeNoteStillVisible > 0,
      };
    }""".replace("BROWSER_INPUT", json.dumps(browser_inputs["b"])))
    require("import_await_release_barrier_stale_flow_preserves_browser_selection",
            import_release_barrier["h0"] != import_release_barrier["h1"]
            and import_release_barrier["browserNoteWasVisible"]
            and not import_release_barrier["nativeNoteStillVisible"]
            and import_release_barrier["h0"] in import_release_barrier["releaseTrace"]
            and import_release_barrier["h1"] in import_release_barrier["releaseTrace"]
            and import_release_barrier["chooseCount"] == 2,
            import_release_barrier)

    # Native metadata held → browser B → late native A must NOT overwrite B
    # (the inverse-order selection race); the browser folder note stays.
    import_inverse = js("""async page => {
      await page.evaluate(() => {
        window.__nativePairingHarness.script.holdSourcePicker = true;
      });
      const nativePick = page.getByRole('button',{name:'시스템 선택기로 선택'}).click().then(() => true).catch(() => false);
      await page.waitForTimeout(300);
      await page.locator('input[webkitdirectory]').first().setInputFiles(BROWSER_INPUT);
      await page.waitForTimeout(200);
      const browserNote = await page.getByText('폴더 선택(FileList)은 빈 디렉터리와 심볼릭 링크를 표현하지 못합니다.',{exact:false}).count();
      await page.evaluate(() => {
        const p = window.__nativePairingHarness;
        p.script.holdSourcePicker = false;
        p.releaseSourcePickers();
      });
      await nativePick;
      await page.waitForTimeout(400);
      const nativeNoteAfter = await page.getByText('네이티브 선택은 빈 디렉터리와 심볼릭 링크를 포함해 선택한 트리를 그대로 반영합니다.',{exact:false}).count();
      const browserNoteAfter = await page.getByText('폴더 선택(FileList)은 빈 디렉터리와 심볼릭 링크를 표현하지 못합니다.',{exact:false}).count();
      return { browserNoteBefore: browserNote > 0, nativeNoteAfter: nativeNoteAfter > 0, browserNoteAfter: browserNoteAfter > 0 };
    }""".replace("BROWSER_INPUT", json.dumps(browser_inputs["c"])))
    require("late_native_metadata_does_not_overwrite_browser_selection",
            import_inverse["browserNoteBefore"] and not import_inverse["nativeNoteAfter"] and import_inverse["browserNoteAfter"],
            import_inverse)

    # Post-release UNMOUNT variant: H0 owned → H1 valid+installed → H0
    # release held → UNMOUNT during the await → H0 release resolves → the
    # stale flow must not record any UI and both handles must be released.
    import_unmount_barrier = js("""async page => {
      await page.evaluate(() => {
        const s = window.__nativePairingHarness.script;
        s.holdSourcePicker = false; s.holdDestPicker = false; s.holdReleaseHandles = [];
        s.sourceReleaseTrace = []; s.sourceChooseTrace = [];
      });
      await page.getByRole('button',{name:'가져오기',exact:true}).click();
      await page.getByRole('button',{name:'시스템 선택기로 선택'}).click();
      await page.getByText('네이티브 선택은 빈 디렉터리와 심볼릭 링크를 포함해 선택한 트리를 그대로 반영합니다.',{exact:false}).waitFor({timeout:15000});
      const firstChoose = await page.evaluate(() => window.__nativePairingHarness.script.sourceChooseTrace);
      const h0 = firstChoose[firstChoose.length - 1];
      await page.evaluate((handle) => { window.__nativePairingHarness.script.holdReleaseHandles = [handle]; }, h0);
      const secondPick = page.getByRole('button',{name:'시스템 선택기로 선택'}).click().then(() => true).catch(() => false);
      await page.waitForFunction((h0Handle) => {
        const p = window.__nativePairingHarness;
        return p.releaseHolds.some((held) => held.handle === h0Handle);
      }, h0, { timeout: 15000 });
      // Leave through the real shell navigation during the release await.
      await page.getByRole('link',{name:'홈',exact:true}).click();
      await page.getByRole('heading',{name:'결과 디렉터리 가져오기',exact:true}).waitFor({state:'detached'});
      await page.waitForTimeout(200);
      await page.evaluate((handle) => { window.__nativePairingHarness.releaseHeldRelease(handle); }, h0);
      await secondPick;
      await page.waitForTimeout(400);
      const state = await page.evaluate(() => ({
        releaseTrace: [...window.__nativePairingHarness.script.sourceReleaseTrace],
        chooseTrace: [...window.__nativePairingHarness.script.sourceChooseTrace],
      }));
      const secondChoose = await page.evaluate(() => window.__nativePairingHarness.script.sourceChooseTrace);
      const h1 = secondChoose[secondChoose.length - 1];
      return { h0, h1, releaseTrace: state.releaseTrace, chooseCount: state.chooseTrace.length };
    }""")
    require("import_post_release_unmount_releases_both_handles_no_ui",
            import_unmount_barrier["h0"] != import_unmount_barrier["h1"]
            and import_unmount_barrier["h0"] in import_unmount_barrier["releaseTrace"]
            and import_unmount_barrier["h1"] in import_unmount_barrier["releaseTrace"]
            and import_unmount_barrier["chooseCount"] == 2,
            import_unmount_barrier)

    # Empty browser input invalidation: a native pick in flight → browser
    # folder input CLEARED (empty FileList) → the empty input advances the
    # epoch and releases the handle; the late native result must not record
    # UI or overwrite the cleared state.
    import_empty_input = js("""async page => {
      await page.getByRole('link',{name:'서재',exact:true}).click();
      await page.evaluate(() => {
        const s = window.__nativePairingHarness.script;
        s.holdSourcePicker = true; s.sourceReleaseTrace = []; s.sourceChooseTrace = [];
      });
      await page.getByRole('button',{name:'가져오기',exact:true}).click();
      const pick = page.getByRole('button',{name:'시스템 선택기로 선택'}).click().then(() => true).catch(() => false);
      await page.waitForTimeout(300);
      // Empty the folder input: the browser change event fires with 0 files.
      await page.evaluate(() => {
        const input = document.querySelector('input[webkitdirectory]');
        if (!input) throw new Error('folder input not found');
        const dt = new DataTransfer();
        input.files = dt.files;
        input.dispatchEvent(new Event('change', { bubbles: true }));
      });
      await page.waitForTimeout(200);
      const cleared = await page.getByText('폴더 선택(FileList)은 빈 디렉터리와 심볼릭 링크를 표현하지 못합니다.',{exact:false}).count();
      await page.evaluate(() => {
        const p = window.__nativePairingHarness;
        p.script.holdSourcePicker = false;
        p.releaseSourcePickers();
      });
      await pick;
      await page.waitForTimeout(400);
      const nativeNoteAfter = await page.getByText('네이티브 선택은 빈 디렉터리와 심볼릭 링크를 포함해 선택한 트리를 그대로 반영합니다.',{exact:false}).count();
      const state = await page.evaluate(() => ({
        releaseTrace: [...window.__nativePairingHarness.script.sourceReleaseTrace],
      }));
      return { clearedNoteWasVisible: cleared > 0, nativeNoteAfter: nativeNoteAfter > 0, releaseTrace: state.releaseTrace };
    }""")
    require("empty_browser_input_invalidates_native_intent",
            import_empty_input["clearedNoteWasVisible"] and not import_empty_input["nativeNoteAfter"]
            and len(import_empty_input["releaseTrace"]) >= 1,
            import_empty_input)

    # A labelled source-picker fault after browser reselection exercises the
    # stale ERROR branch too, rather than checking successful late replies only.
    late_source_error = js("""async page => {
      const tree = await page.evaluate(() => {
        const s = window.__nativePairingHarness.script;
        s.holdSourcePicker = true;
        return s.fsTree;
      });
      try {
        await page.getByRole('button',{name:'시스템 선택기로 선택'}).click();
        await page.waitForFunction(() => window.__nativePairingHarness.sourcePickerQueue.length === 1);
        await page.locator('input[webkitdirectory]').first().setInputFiles(BROWSER_INPUT);
        await page.evaluate(() => {
          const p = window.__nativePairingHarness;
          p.script.fsTree = null; // The labelled fixture fails while creating its reply.
          p.script.holdSourcePicker = false;
          p.releaseSourcePickers();
        });
        await page.waitForFunction(() => {
          const button = [...document.querySelectorAll('button')].find(b => b.textContent.includes('시스템 선택기로 선택'));
          return button && !button.disabled;
        });
        return await page.getByText('폴더 선택(FileList)은 빈 디렉터리와 심볼릭 링크를 표현하지 못합니다.',{exact:false}).count() === 1;
      } finally {
        await page.evaluate(original => { window.__nativePairingHarness.script.fsTree = original; }, tree);
      }
    }""".replace("BROWSER_INPUT", json.dumps(browser_inputs["b"])))
    require("late_source_error_preserves_new_browser_selection", late_source_error)

    # ---- TASK-003-05 M3 C3: selection-replacement races (F4 completion) ----
    # Duplicate destination chooser while one flight is active: the second
    # click is rejected SYNCHRONOUSLY (no second chooser call, no second
    # handle/effect, no cancel reset). The single flight completes normally.
    dest_singleflight = js("""async page => {
      await page.getByRole('link',{name:'홈',exact:true}).click();
      await page.getByRole('link',{name:'서재',exact:true}).click();
      await page.evaluate(() => {
        const s = window.__nativePairingHarness.script;
        s.destExists = false; s.destDurability = 'confirmed'; s.destCloseConflictsOnce = false;
        s.destCloseUncertain = false; s.destAbortFailsTemp = null; s.packageFailWrite = false;
        s.holdDestPicker = true;
      });
      await page.getByRole('button',{name:'원본 디렉터리 내려받기'}).click();
      await page.getByRole('button',{name:'내려받기 시작'}).click();
      await page.getByRole('checkbox').check();
      await page.getByRole('button',{name:'동의하고 계속'}).click();
      const before = await page.evaluate(() => ({
        chooses: window.__nativePairingHarness.script.destChooseCalls,
        opens: window.__nativePairingHarness.script.destIntents.length,
        closes: window.__nativePairingHarness.script.destCloseCalls,
      }));
      // TWO actual DOM clicks in the same JS turn: the first enters the
      // chooser (held), the second is rejected by the synchronous flight.
      await page.evaluate(() => {
        const btn = document.querySelectorAll('button');
        const chooser = [...btn].find(b => b.textContent.includes('저장 위치 선택'));
        if (!chooser) throw new Error('chooser button not found');
        chooser.click();
        chooser.click();
      });
      // The held queue length proves exactly one chooser REQUEST entered
      // (the synthetic counter increments only after the held await).
      await page.waitForFunction(() => window.__nativePairingHarness.destPickerQueue.length >= 1);
      const heldQueue = await page.evaluate(() => window.__nativePairingHarness.destPickerQueue.length);
      await page.evaluate(() => { window.__nativePairingHarness.releaseDestPickers(); });
      await page.getByTestId('download-done').waitFor({timeout:15000});
      const after = await page.evaluate(() => ({
        chooses: window.__nativePairingHarness.script.destChooseCalls,
        opens: window.__nativePairingHarness.script.destIntents.length,
        closes: window.__nativePairingHarness.script.destCloseCalls,
      }));
      return {
        heldQueue: heldQueue,
        chooseDelta: after.chooses - before.chooses,
        openDelta: after.opens - before.opens,
        closeDelta: after.closes - before.closes,
      };
    }""")
    require("duplicate_destination_chooser_rejected_single_flight",
            dest_singleflight["heldQueue"] == 1 and dest_singleflight["chooseDelta"] == 1
            and dest_singleflight["openDelta"] == 1 and dest_singleflight["closeDelta"] == 1,
            dest_singleflight)

    # Hold an actual NativePort abort reply while duplicate conflict actions
    # arrive. The next handle must remain owned through its own unmount cleanup.
    discard_flight = js("""async page => {
      await page.getByRole('link',{name:'홈',exact:true}).click();
      await page.getByRole('link',{name:'서재',exact:true}).click();
      await page.evaluate(() => {
        const p = window.__nativePairingHarness;
        p.script.destExists = true;
        p.script.holdDestPicker = false;
        const original = p.call;
        const state = { selected: [], aborted: [], opens: 0, heldOnce: false, release: null };
        p.__discardFlight = state;
        p.__discardOriginal = original;
        p.call = async (operation, payload) => {
          if (operation === 'destOpen') state.opens++;
          if (operation === 'destAbort') {
            state.aborted.push(payload.handle);
            const reply = await original.call(p, operation, payload);
            if (!state.heldOnce) {
              state.heldOnce = true;
              await new Promise(resolve => { state.release = resolve; });
            }
            return reply;
          }
          const reply = await original.call(p, operation, payload);
          if (operation === 'chooseDownloadDestination') {
            const handle = 'discard-choice-' + state.selected.length;
            state.selected.push(handle);
            return { ...reply, handle };
          }
          return reply;
        };
      });
      try {
        await page.getByRole('button',{name:'원본 디렉터리 내려받기'}).click();
        await page.getByRole('button',{name:'내려받기 시작'}).click();
        await page.getByRole('checkbox').check();
        await page.getByRole('button',{name:'동의하고 계속'}).click();
        await page.getByRole('button',{name:'저장 위치 선택 (시스템 저장 대화상자)'}).click();
        await page.getByTestId('download-conflict').waitFor();
        await page.evaluate(() => {
          const buttons = [...document.querySelectorAll('button')];
          const discard = buttons.find(b => b.textContent.trim() === '다른 이름 사용');
          const replace = buttons.find(b => b.textContent.trim() === '동의하고 교체');
          discard.click(); discard.click(); replace.click();
        });
        await page.waitForFunction(() => typeof window.__nativePairingHarness.__discardFlight.release === 'function');
        const pending = await page.evaluate(() => {
          const state = window.__nativePairingHarness.__discardFlight;
          return { aborts: state.aborted.length, opens: state.opens, choices: state.selected.length };
        });
        await page.evaluate(() => window.__nativePairingHarness.__discardFlight.release());
        await page.getByRole('button',{name:'저장 위치 선택 (시스템 저장 대화상자)'}).click();
        await page.getByTestId('download-conflict').waitFor();
        await page.getByRole('link',{name:'홈',exact:true}).click();
        await page.waitForFunction(() => {
          const state = window.__nativePairingHarness.__discardFlight;
          return state.selected.length === 2 && state.aborted.includes(state.selected[1]);
        });
        return await page.evaluate(pending => {
          const state = window.__nativePairingHarness.__discardFlight;
          return { pending, selected: state.selected, aborted: state.aborted, opens: state.opens };
        }, pending);
      } finally {
        await page.evaluate(() => {
          const p = window.__nativePairingHarness;
          p.__discardFlight.release?.();
          p.call = p.__discardOriginal;
          delete p.__discardOriginal;
          delete p.__discardFlight;
        });
      }
    }""")
    require("conflict_discard_owns_flight_and_preserves_next_handle",
            discard_flight["pending"] == {"aborts": 1, "opens": 0, "choices": 1}
            and len(discard_flight["selected"]) == 2
            and discard_flight["aborted"] == discard_flight["selected"]
            and discard_flight["opens"] == 0,
            discard_flight)

    # TASK-003-05 M4: asynchronous projects arrival after the import panel
    # mounts. The Library loads its project list once per mount/epoch; when
    # the panel opens while that drain is still in flight the control must
    # keep the genuine start disabled until the list arrives, fill the
    # default target exactly once (never over an explicit choice), enable
    # the start, and dispatch createUpload against the EXACT arrived
    # target. The synthetic port intentionally answers createUpload with
    # the labelled 501 FEATURE_NOT_READY (unimplemented default), so the
    # capture proves the dispatch target without inventing any transfer
    # success.
    call(["open", app_url])
    attach_error_capture()
    async_projects = js("""async page => {
      await page.evaluate(() => {
        const harness = window.__nativePairingHarness;
        const original = harness.call.bind(harness);
        harness.__projectsOriginal = original;
        harness.__projectsFlight = { held: [], captured: [] };
        harness.call = async function(operation, payload) {
          const flight = window.__nativePairingHarness.__projectsFlight;
          if (operation === 'listProjects') {
            await new Promise(resolve => flight.held.push(resolve));
          }
          if (operation === 'createUpload') {
            flight.captured.push(payload && payload.request ? payload.request : payload);
          }
          return original(operation, payload);
        };
      });
      try {
        // Reach the verified shell through the real pairing flow; the
        // wrapper passes everything except the held listProjects through.
        await page.getByRole('button',{name:'연결 코드 요청'}).click();
        await page.evaluate(() => { window.__nativePairingHarness.script.poll = 'approved'; });
        await page.locator('.service-state[data-state="verified"]').waitFor({timeout:15000});
        // The Library mounts and its project drain parks on the held
        // listProjects; the import panel opens with an empty list.
        await page.evaluate(() => { history.pushState({}, "", "/library"); window.dispatchEvent(new PopStateEvent("popstate")); });
        await page.getByRole('button',{name:'가져오기',exact:true}).waitFor({timeout:15000});
        await page.getByRole('button',{name:'가져오기',exact:true}).click();
        await page.getByRole('button',{name:'시스템 선택기로 선택'}).click();
        await page.getByText('네이티브 선택은 빈 디렉터리와 심볼릭 링크를 포함해 선택한 트리를 그대로 반영합니다.',{exact:false}).waitFor({timeout:15000});
        const start = page.getByRole('button',{name:'가져오기 시작'});
        const disabledWhileEmpty = await start.isDisabled();
        // The list arrives late: the default target fills exactly once and
        // the genuine start enables within an explicit readiness window.
        await page.evaluate(() => { for (const release of window.__nativePairingHarness.__projectsFlight.held) release(); });
        await page.waitForFunction(() => {
          const button = [...document.querySelectorAll('button')].find(x => x.textContent.trim() === '가져오기 시작');
          return button ? !button.disabled : false;
        }, {timeout: 15000});
        await start.click();
        // The engine dispatches createUpload against the arrived target;
        // the labelled synthetic FEATURE_NOT_READY stops the flow honestly.
        await page.waitForFunction(() => window.__nativePairingHarness.__projectsFlight.captured.length >= 1, {timeout: 15000});
        await page.waitForTimeout(600);
        const stage = await page.evaluate(() => document.body.textContent || '');
        const flight = await page.evaluate(() => window.__nativePairingHarness.__projectsFlight);
        return { disabledWhileEmpty, captured: flight.captured, stage };
      } finally {
        await page.evaluate(() => {
          const p = window.__nativePairingHarness;
          if (p.__projectsFlight) { for (const release of p.__projectsFlight.held) release(); }
          p.call = p.__projectsOriginal;
          delete p.__projectsOriginal;
          delete p.__projectsFlight;
        });
      }
    }""")
    require("late_projects_keep_genuine_start_disabled_until_arrival", async_projects["disabledWhileEmpty"], async_projects)
    require("late_projects_arrival_fills_exact_dispatch_target",
            len(async_projects["captured"]) == 1
            and async_projects["captured"][0].get("target", {}).get("project_id") == "proj-1"
            and async_projects["captured"][0].get("target", {}).get("document_id", "missing") is None,
            async_projects)
    require("late_projects_dispatch_stops_labelled_without_invented_success",
            "FEATURE_NOT_READY" in (async_projects["stage"] or "") and "게시" not in (async_projects["stage"] or ""),
            async_projects)
    late_errors = js("async page => page.__nativeConnectErrors")
    require("no_page_or_console_errors_late_projects", late_errors == [], late_errors)

    # F2: permanent Go binding pins surface an accurate incompatible state
    # with the existing typed notices — never a transient recovering notice,
    # never effective authority, never a forced new pairing.
    # Actual App startup after a typed stopped-daemon attach: the native
    # start offer remains reachable without a previously confirmed session.
    call(["open", app_url + "&attachFailure=daemon-stopped"])
    attach_error_capture()
    stopped_boot = js("""async page => {
      await page.waitForFunction(() => window.__nativePairingState().phase === 'degraded');
      await page.getByTestId('daemon-state').filter({hasText:'로컬 데몬이 실행 중이 아닙니다'}).waitFor();
      const before = await page.evaluate(() => window.__nativePairingHarness.script.pairCalls);
      await page.getByRole('button',{name:'데몬 시작',exact:true}).click();
      await page.getByTestId('daemon-state').filter({hasText:'로컬 서비스가 확인되었습니다'}).waitFor();
      await page.waitForFunction(() => window.__nativePairingState().phase === 'unauthenticated');
      await page.getByRole('button',{name:'연결 코드 요청',exact:true}).click();
      await page.locator('.code-display').waitFor();
      return await page.evaluate(before => ({ before, pairs: window.__nativePairingHarness.script.pairCalls,
        running: window.__nativePairingHarness.script.daemonRunning }), before);
    }""")
    require("native_stopped_first_boot_keeps_explicit_start_and_pairing_reachable",
            stopped_boot["before"] == 0 and stopped_boot["pairs"] == 1 and stopped_boot["running"], stopped_boot)

    pin_cases = [
        ("api-incompatible", "지원하지 않는 서비스 계약입니다"),
        ("library-mismatch", "연결하려는 서재가 이 프로필과 일치하지 않아"),
        ("credential-mismatched", "저장된 연결 자격이 현재 서비스 결속과 일치하지 않습니다"),
    ]
    for pin_code, pin_text in pin_cases:
        call(["open", app_url + "&attachFailure=" + pin_code])
        attach_error_capture()
        pin = js("""async page => {
          await page.locator('.service-state[data-state="incompatible"]').waitFor({timeout:15000});
          return await page.evaluate((pinText) => {
            const text=document.body.textContent||'';
            return {typed: text.includes(pinText), transient: text.includes('일시적으로 응답하지 않습니다'), pairs: window.__nativePairingHarness.script.pairCalls};
          }, PIN_TEXT);
        }""".replace("PIN_TEXT", json.dumps(pin_text)))
        require("pin_error_" + pin_code + "_typed_incompatible_no_transient", pin["typed"] and not pin["transient"] and pin["pairs"] == 0, pin)
    pin_errors = js("async page => page.__nativeConnectErrors")
    require("no_pin_page_or_console_errors", pin_errors == [], pin_errors)

    require("source_unchanged_during_check", fingerprints() == before)
    proof["input_sha256"] = before
    proof["complete"] = True
finally:
    if opened:
        try:
            call(["close"])
            proof["browser_closed"] = True
        except Exception as error:
            proof["browser_closed"] = False
            proof["cleanup_error"] = str(error)
    if server is not None:
        server.shutdown()
        server.server_close()
        server_thread.join(timeout=5)
        proof["server_closed"] = not server_thread.is_alive()
    else:
        proof["server_closed"] = True
    receipt.write_text(json.dumps(proof, ensure_ascii=False, indent=2) + "\n")
    if proof.get("browser_closed", not opened) and proof["server_closed"] and proof["complete"]:
        shutil.rmtree(work)

assert proof["complete"] and proof["browser_closed"] and proof["server_closed"]
print(json.dumps({"complete": True, "cases": len(proof["cases"]), "browser_closed": True, "server_closed": True}))
