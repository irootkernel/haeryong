"""Exercise real Wails window close and process crash against an owned fixture."""

import argparse
import json
import os
from pathlib import Path
import subprocess
import shutil
import sys
import tempfile
import time
import urllib.request
import urllib.error
from urllib.parse import urlparse
import uuid

parser = argparse.ArgumentParser()
parser.add_argument("--endpoint", required=True)
parser.add_argument("--binary", type=Path, required=True)
parser.add_argument("--output", type=Path, required=True)
parser.add_argument("--browser-cli", help="Existing playwright-cli executable; adds real browser checks")
args = parser.parse_args()
assert urlparse(args.endpoint).hostname == "127.0.0.1"
args.output.mkdir(mode=0o700, parents=True, exist_ok=True)


def get(path):
    with urllib.request.urlopen(args.endpoint + path, timeout=5) as response:
        return json.load(response)


identity = get("/health")
assert identity["sqlite"] == "3.53.4"
assert identity["settings"] == {"journal_mode": "wal", "synchronous": 2, "foreign_keys": 1, "busy_timeout": 5000}
for name, body in [("wrong-instance", b'{"instance":"not-this-fixture"}'), ("empty-body", b"")]:
    client = name + "-" + uuid.uuid4().hex
    request = urllib.request.Request(args.endpoint + "/attach/" + client, data=body, method="POST")
    try:
        urllib.request.urlopen(request, timeout=5)
        raise AssertionError("invalid attachment accepted")
    except urllib.error.HTTPError as error:
        assert error.code == 400
    assert client not in get("/observations")
for url in ["https://127.0.0.1:1", "http://127.0.0.1", "http://user@127.0.0.1:1", "http://10.0.0.1:1", ""]:
    rejected = subprocess.run([str(args.binary.resolve()), "--url", url], capture_output=True, text=True, timeout=5)
    assert rejected.returncode == 1 and "fixture URL required" in rejected.stderr
outcomes = []
for case, close in [("nativeclose", True), ("nativecrash", False)]:
    client = case + "-" + uuid.uuid4().hex
    assert client not in get("/observations")
    argv = [str(args.binary.resolve()), "--url", args.endpoint + "/?client=" + client]
    if close:
        argv += ["--close-after", "20s"]
    with (args.output / (client + ".log")).open("w") as log:
        process = subprocess.Popen(argv, stdout=log, stderr=log)
        started = time.monotonic()
        try:
            deadline = time.monotonic() + 15
            while client not in (observations := get("/observations")):
                assert process.poll() is None, f"{client} exited before attachment"
                if time.monotonic() >= deadline:
                    raise AssertionError(f"{client} did not complete frontend/health/attach handshake; health={get('/health')}; observed clients={list(observations)}")
                time.sleep(0.2)
            assert process.poll() is None, "native process exited before fresh attachment was verified"
            attachment = observations[client]
            assert attachment["instance"] == identity["instance"]
            assert "AppleWebKit/" in attachment["user_agent"]
            if close:
                code = process.wait(timeout=25)
                assert code == 0, code
                assert time.monotonic() - started >= 18, "native exited before the requested close interval"
            else:
                process.kill()
                code = process.wait(timeout=5)
                assert code == -9, code
            assert get("/health") == identity
            outcomes.append({"client": client, "attachment": attachment,
                             "client_exit": code, "service_same_instance": True})
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=5)
browser = "not_run_by_this_command"
browser_cleanup = "not_run"
browser_directory = None
session = None


def write_result():
    status = "failed" if browser_cleanup == "failed" else (
        "pass" if browser == "not_run_by_this_command" or browser.startswith("pass:") else "incomplete")
    result = {"status": status, "fixture_only": True, "service": identity,
              "outcomes": outcomes, "browser": browser, "browser_cleanup": browser_cleanup,
              "browser_directory": str(browser_directory) if browser_directory else None,
              "browser_session": session,
              "close_mechanism": "Wails window.Close, not human UI interaction"}
    (args.output / "lifecycle-result.json").write_text(json.dumps(result, indent=2))
    print(json.dumps(result))


if args.browser_cli:
    before_browser = get("/observations")
    temporary_root = Path(tempfile.gettempdir())
    if os.path.ismount("/Volumes/RootKernel") and os.access("/Volumes/RootKernel", os.W_OK):
        temporary_root = Path("/Volumes/RootKernel/tmp")
        temporary_root.mkdir(parents=True, exist_ok=True)
    browser_directory = Path(tempfile.mkdtemp(prefix="hy-", dir=temporary_root))
    session = "h" + uuid.uuid4().hex[:8]
    prefix = [args.browser_cli, "-s=" + session]
    cli_env = os.environ | {
        "TMPDIR": str(browser_directory),
        "PLAYWRIGHT_DAEMON_SESSION_DIR": str(browser_directory / "sessions"),
        "PLAYWRIGHT_DAEMON_SOCKETS_DIR": str(browser_directory / "sockets"),
    }

    def cli(argv, timeout=60):
        checked = subprocess.run(prefix + argv, cwd=args.output, env=cli_env,
                                 capture_output=True, text=True, timeout=timeout)
        print(checked.stdout, end="")
        print(checked.stderr, end="")
        checked.check_returncode()
        assert "### Error" not in checked.stdout, checked.stdout
        return checked.stdout

    script = """async (page) => {
      const identity = IDENTITY;
      const expectedKeys = Object.keys(BEFORE_BROWSER).sort();
      const issues = [];
      let noClientPhase = true;
      let unexpectedAttachments = 0;
      page.on('request', request => { if (noClientPhase && request.url().includes('/attach/')) unexpectedAttachments++; });
      page.on('pageerror', error => issues.push(String(error)));
      page.on('console', entry => { if (['error','warning'].includes(entry.type())) issues.push(entry.text()); });
      await page.goto(BASE + '/');
      const observations = () => page.evaluate(() => fetch('/observations').then(r => r.json()));
      const assertNoAttachment = async () => {
        if (unexpectedAttachments || JSON.stringify(Object.keys(await observations()).sort()) !== JSON.stringify(expectedKeys)) throw new Error('unrequested attachment');
      };
      await page.getByRole('button', {name:'Check fixture service'}).waitFor();
      await assertNoAttachment();
      await page.getByRole('button', {name:'Check fixture service'}).press('Enter');
      await page.waitForFunction(expected => document.querySelector('output').textContent === expected,
        identity.fixture + ': ' + identity.instance);
      await assertNoAttachment();
      if (issues.length) throw new Error('normal browser errors: ' + issues.join('; '));
      await page.screenshot({path: SCREENSHOT});
      await page.route('**/health', () => {});
      const healthStarted = await page.evaluate(() => performance.now());
      await page.getByRole('button', {name:'Check fixture service'}).click();
      await page.waitForFunction(() => document.querySelector('output').textContent === 'Checking fixture service');
      await page.waitForFunction(() => document.querySelector('output').textContent === 'Health request timed out; try again', null, {timeout:8000});
      const healthElapsed = (await page.evaluate(() => performance.now())) - healthStarted;
      if (healthElapsed < 4000 || healthElapsed > 7000) throw new Error('health deadline outside four-to-seven-second band');
      if (await page.getByRole('button').isDisabled()) throw new Error('health timeout disabled retry');
      await page.unroute('**/health');
      await page.getByRole('button').click();
      await page.waitForFunction(expected => document.querySelector('output').textContent === expected,
        identity.fixture + ': ' + identity.instance);
      await assertNoAttachment();
      noClientPhase = false;
      const client = CLIENT;
      let attachmentStarted;
      await page.route('**/attach/**', async () => { attachmentStarted = await page.evaluate(() => performance.now()); });
      await page.goto(BASE + '/?client=' + client);
      await page.waitForFunction(() => document.querySelector('output[aria-label="Attachment status"]').textContent === 'Attachment request timed out; use Retry attachment', null, {timeout:10000});
      const attachmentElapsed = (await page.evaluate(() => performance.now())) - attachmentStarted;
      if (!Number.isFinite(attachmentElapsed) || attachmentElapsed < 4000 || attachmentElapsed > 7000) throw new Error('attachment deadline outside four-to-seven-second band');
      if (await page.getByRole('button', {name:'Retry attachment'}).isDisabled()) throw new Error('attach timeout disabled retry');
      if ((await observations())[client]) throw new Error('stalled attach was recorded');
      await page.unroute('**/attach/**');
      await page.getByRole('button', {name:'Check fixture service'}).click();
      await page.waitForFunction(expected => document.querySelector('output').textContent === expected,
        identity.fixture + ': ' + identity.instance);
      if ((await observations())[client]) throw new Error('health check retried attachment');
      if (await page.locator('output[aria-label="Attachment status"]').textContent() !== 'Attachment request timed out; use Retry attachment') throw new Error('health check hid failed attachment');
      const rejectedMessage = 'Attach HTTP 400: fixture instance mismatch; use Retry attachment';
      const beforeRejected = Object.keys(await observations()).sort();
      await page.route('**/attach/**', route => route.continue({postData:JSON.stringify({instance:'not-this-fixture'})}));
      await page.getByRole('button', {name:'Retry attachment'}).click();
      await page.waitForFunction(expected => document.querySelector('output[aria-label="Attachment status"]').textContent === expected, rejectedMessage);
      if (JSON.stringify(Object.keys(await observations()).sort()) !== JSON.stringify(beforeRejected)) throw new Error('rejected attachment was recorded');
      if (await page.getByRole('button', {name:'Retry attachment'}).isDisabled()) throw new Error('HTTP rejection disabled retry');
      await page.route('**/health', route => route.fulfill({status:503,body:'unavailable'}));
      await page.getByRole('button', {name:'Check fixture service'}).click();
      await page.waitForFunction(() => document.querySelector('output[aria-label="Health status"]').textContent === 'Health HTTP 503');
      if (await page.locator('output[aria-label="Attachment status"]').textContent() !== rejectedMessage) throw new Error('failed health check hid attachment failure');
      if (JSON.stringify(Object.keys(await observations()).sort()) !== JSON.stringify(beforeRejected)) throw new Error('failed health check retried attachment');
      await page.unroute('**/health');
      await page.unroute('**/attach/**');
      await page.getByRole('button', {name:'Retry attachment'}).click();
      await page.waitForFunction(expected => document.querySelector('output[aria-label="Attachment status"]').textContent === expected, 'Attached to ' + identity.instance);
      if ((await observations())[client]?.instance !== identity.instance) throw new Error('attachment retry not recorded');
      await page.screenshot({path: ATTACHMENT_SCREENSHOT});
      const healthClient = client + '-health';
      await page.route('**/health', () => {});
      await page.goto(BASE + '/?client=' + healthClient);
      const healthAttachmentMessage = 'Health request timed out; try again; use Retry attachment';
      await page.waitForFunction(expected => document.querySelector('output[aria-label="Attachment status"]').textContent === expected, healthAttachmentMessage, {timeout:10000});
      if (await page.getByRole('button', {name:'Retry attachment'}).isDisabled()) throw new Error('attachment health failure disabled retry');
      if ((await observations())[healthClient]) throw new Error('failed attachment health read was recorded');
      await page.unroute('**/health');
      await page.getByRole('button', {name:'Check fixture service'}).click();
      await page.waitForFunction(expected => document.querySelector('output[aria-label="Health status"]').textContent === expected, identity.fixture + ': ' + identity.instance);
      if (await page.locator('output[aria-label="Attachment status"]').textContent() !== healthAttachmentMessage || (await observations())[healthClient]) throw new Error('health check cleared or retried failed attachment');
      await page.getByRole('button', {name:'Retry attachment'}).click();
      await page.waitForFunction(expected => document.querySelector('output[aria-label="Attachment status"]').textContent === expected, 'Attached to ' + identity.instance);
      if ((await observations())[healthClient]?.instance !== identity.instance) throw new Error('attachment health recovery not recorded');
      return {passed:true, keyboard_check:true, no_unrequested_attachment:true,
        health_timeout_and_retry:true, attach_timeout_and_retry:true, independent_status:true,
        attachment_http_rejection:true, failed_health_preserves_attachment:true, attachment_health_retry:true,
        health_timeout_ms:healthElapsed, attach_timeout_ms:attachmentElapsed, normal_console_issues:0};
    }"""
    for key, value in {
        "ATTACHMENT_SCREENSHOT": str((args.output / "browser-attachment-retry.png").resolve()),
        "IDENTITY": identity, "SCREENSHOT": str((args.output / "browser-check.png").resolve()),
        "BASE": args.endpoint, "CLIENT": "browser-timeout-" + uuid.uuid4().hex,
        "BEFORE_BROWSER": before_browser,
    }.items():
        script = script.replace(key, json.dumps(value))
    browser = "incomplete"
    try:
        subprocess.run([sys.executable, "-B", str(Path(__file__).with_name("check_checker.py")),
                        "--browser-cli", args.browser_cli], check=True)
        cli(["open", "about:blank"])
        observed = cli(["run-code", script])
        assert '"passed":true' in "".join(observed.split()), "browser did not return the pass record"
        browser = "pass: keyboard, no-client, health/attach timeout and retry, HTTP rejection, independent failures"
    finally:
        try:
            cli(["close"])
            shutil.rmtree(browser_directory)
            browser_cleanup = "pass"
        except Exception:
            browser_cleanup = "failed"
            raise
        finally:
            write_result()
else:
    write_result()
