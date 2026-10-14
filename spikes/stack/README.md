# Bounded stack spike

TASK-001-02 owns this executable experiment. It uses disposable synthetic data,
a loopback-only HTTP service, shared compiled React assets and a separate Wails
Go module. It implements no production authentication, source reader, renderer
isolation, library schema or daemon installation.

The headless module imports SQLite and has no Wails dependency. `desktop/` owns
all Wails imports. `frontend/` is built once and served to both clients; neither
client starts the service. Fixture health reports the actual SQLite engine and
process instance. Client attachment requires JavaScript to read that instance
from the running service before reporting it.

## Repeat the experiment

Prerequisites: the pinned Go/Node combination, macOS ARM64 with Xcode for the
native build, Docker with an existing Linux ARM64 image, Python 3, and a browser.
See [the measured environment and results](../../docs/testing/stack-spike.md).
Choose an empty, owner-created scratch directory with mode `0700` outside this checkout. Agents follow
the repository's mounted-volume temporary-file rule.

From the repository root, build and run the engine checks with:

```sh
python3 -B spikes/stack/check.py --output "$SPIKE_OUTPUT" --linux-image "$EXISTING_LINUX_ARM64_IMAGE_SHA256"
```

The command installs the exact frontend lockfile locally with lifecycle scripts
disabled, builds both modules, runs SQLite tests on macOS and Linux, and starts
the Linux HTTP fixture without a GUI or external network. Docker uses
`--pull=never`, a read-only filesystem/bind mounts and disposable `/tmp`; the
command removes only its uniquely named container. Go binaries and the result
record stay in the chosen output directory. The ignored `node_modules` and
`frontend/dist` build output stay under `spikes/stack/frontend`. Host test databases are temporary
children of the chosen output directory, passed as `TMPDIR`; Linux test/fixture databases live on the container tmpfs and disappear
with it. The result records the checked host GOOS/GOARCH. The native dependency still requires macOS
GUI frameworks; this restriction does not apply to the headless module.

Start the macOS fixture in a separate terminal:

```sh
"$SPIKE_OUTPUT/fixture" --assets spikes/stack/frontend/dist --endpoint "$SPIKE_OUTPUT/endpoint" --database "$SPIKE_OUTPUT/fixture.db"
```

Use the URL written to `endpoint` for the following commands:

```sh
python3 -B spikes/stack/check_lifecycle.py --endpoint "$FIXTURE_URL" --binary "$SPIKE_OUTPUT/native" --output "$SPIKE_OUTPUT"
```

Add `--browser-cli /absolute/path/to/playwright-cli` to include the real browser
keyboard action, exact health-instance assertion, no-client attachment check,
and controlled health/attachment stalls with measured four-to-seven-second
timeout bands around the five-second setting. A client page has separate health
and attachment status; **Retry attachment** actually repeats attachment, while
**Check fixture service** does not hide a failed attachment or retry it.
Attachment failures name **Retry attachment**, including a failed health read
before the POST; HTTP 400 identifies the fixture instance mismatch. Browser
checks cover that real rejection and a failing health read while it remains visible.
This uses an existing CLI/browser installation and closes only its own session.
The tested Playwright CLI is `@playwright/cli` 0.1.1 (Playwright
1.59.0-alpha-1771104257000). Each browser run creates its own private temporary
directory, following the mounted-volume rule. Process-only `TMPDIR`,
`PLAYWRIGHT_DAEMON_SESSION_DIR` and `PLAYWRIGHT_DAEMON_SOCKETS_DIR` overrides
isolate the registry, profiles and sockets; changing `TMPDIR` alone is insufficient.
Successful close removes only this directory. A close failure retains it at
`browser_directory` in the result for recovery with the same overrides and
`browser_session` name. Preserve other CLI sessions. The optional browser check also
opens two owned sessions with the same name in separate namespaces and requires
that closing one leaves the other usable.
Browser execution or close failures retain `lifecycle-result.json` with an
explicit incomplete/failed status and still make the command fail.
The build command runs `check_checker.py` for deterministic browser/cleanup and
readiness-failure probes; those synthetic faults are separate from real browser
and native execution.
Both commands read back the fixture connection's SQLite settings; the engine
tests require version 3.53.4. Connection settings are not inferred by opening a
different SQLite connection. They do not prove power-loss durability.

The fixture requires private database/endpoint parents, rejects symlink targets,
and creates the endpoint exclusively. Remove only your previous endpoint after
stopping its owned process before reusing that exact path. The manual service's
`fixture.db` remains inside the private output directory.

Open `$FIXTURE_URL/?client=browser` in a browser, press **Check fixture service**,
and compare its visible instance with `lifecycle-result.json` and
`$FIXTURE_URL/observations`. The native checker loads the compiled frontend,
calls the actual Wails window's `Close` method, then launches another client and
terminates only that child with SIGKILL. Both cases use fresh random client IDs on every invocation and must leave the same service
available. Invalid native URLs and malformed/mismatched attachments are rejected; the close case must survive until near its requested close interval. Stop only the fixture process started for this run afterward.

The service has no bearer authentication and must remain on loopback with only
its disposable assets/database. Pairing/private ingress belongs to TASK-001-03;
imported HTML and native bridge separation belong to TASK-001-04. This fixture
must not be used as a private-data service or exposed through a proxy unchanged.
