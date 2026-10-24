// Focused safeReturn regressions: the login-return contract keeps raw shell
// guards while validating the once-decoded page_path per the package rules
// (no leading "/", drive prefix, empty/"."/".." segment or NUL; literal
// backslash/newline/tab are representable filename characters).
import test from "node:test";
import assert from "node:assert/strict";

(globalThis as { location?: { origin: string } }).location = { origin: "http://127.0.0.1:9270" };
const { safeReturn } = await import("./routes.ts");

const DOC = "/documents/11111111-1111-4111-8111-111111111111/versions/22222222-2222-4222-8222-222222222222";

test("positive: literal backslash, newline and tab filenames survive one decode", () => {
  const cases = [
    `${DOC}?page=nested%5Cguide.html`,
    `${DOC}?page=notes%0Aline.html`,
    `${DOC}?page=tab%09sep.html`,
  ];
  for (const value of cases) {
    assert.equal(safeReturn(value), value, value);
  }
});

test("positive: Unicode, encoded '?' and '#' and a literal '%2e' name decode once and survive", () => {
  const cases = [
    `${DOC}?page=%ED%95%9C%EA%B8%80%20%EB%AC%B8%EC%84%9C.md`,
    `${DOC}?page=lit%3Fq%23h.html`,
    `${DOC}?page=file%252ename.html`,
    `${DOC}?page=%252e%252e%2Fescape.txt`,
  ];
  for (const value of cases) {
    assert.equal(safeReturn(value), value, value);
  }
});

test("positive: page_query/page_fragment and #region context are preserved exactly", () => {
  const value = `${DOC}?page=nested%2Fguide.html&page_query=q%3D1&page_fragment=part#region=example`;
  assert.equal(safeReturn(value), value);
});

test("negative: external, protocol-relative and credentialed returns are rejected", () => {
  for (const value of [
    "https://unrelated.example/",
    "//evil.example/path",
    "http://user:pass@127.0.0.1:9270/connect",
  ]) {
    assert.equal(safeReturn(value), "/", value);
  }
});

test("negative: raw backslash and raw shell control characters are rejected", () => {
  for (const value of [
    `${DOC}?page=a\\b.html`,
    `${DOC}?page=a\nb.html`,
    `${DOC}?page=a\tb.html`,
    `${DOC}?page=a\u0001b.html`,
  ]) {
    assert.equal(safeReturn(value), "/", JSON.stringify(value));
  }
});

test("negative: traversal, dot and empty segments, leading slash, NUL and drive prefix", () => {
  for (const page of [
    "..%2F..%2Fetc%2Fpasswd",
    "%2E%2E/escape.html",
    "..",
    ".",
    "nested/%2E/guide.html",
    "nested%2F%2Fguide.html",
    "%2Fabsolute.html",
    "nul%00.html",
    "C%3A%5Cwindows%5Cwin.ini",
    "C%3A/system32/config",
    "C%3Anotes.html",
  ]) {
    assert.equal(safeReturn(`${DOC}?page=${page}`), "/", page);
  }
});

test("negative: unknown routes and unknown query keys fall back to home", () => {
  for (const value of [
    "/not-a-route",
    "/settings/not-a-section",
    `${DOC}?evil=1`,
    `${DOC}?page=ok.html&csrf=1`,
  ]) {
    assert.equal(safeReturn(value), "/", value);
  }
});

test("negative: control characters remain rejected outside the page parameter", () => {
  assert.equal(safeReturn(`${DOC}?page=ok.html&page_query=a%0Ab`), "/");
  assert.equal(safeReturn(`${DOC}?page=ok.html&page_fragment=%09x`), "/");
});

test("an empty page parameter is rejected", () => {
  assert.equal(safeReturn(`${DOC}?page=`), "/");
});

test("T5 host region and comparison keys are closed while raw artifact values stay data", async () => {
  const { pinnedReaderURL } = await import("./routes.ts");
  const raw = { page: "자료/😀 %2F +?#.html", query: "q=%2F+ ?# 한글", fragment: "%2F +#" };
  const route = pinnedReaderURL("11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", raw.page, raw.query, raw.fragment, "영역 %2F +");
  assert.equal(safeReturn(route), route);
  const url = new URL(route, location.origin);
  assert.equal(url.searchParams.get("page"), raw.page); assert.equal(url.searchParams.get("page_query"), raw.query); assert.equal(url.searchParams.get("page_fragment"), raw.fragment);
  for (const hash of ["#unknown=x", "#region=a&region=b", "#region=", "#region=%ZZ"]) assert.equal(safeReturn(DOC + hash), "/");
  const pair = "from_version=22222222-2222-4222-8222-222222222222&to_version=33333333-3333-4333-8333-333333333333";
  assert.equal(safeReturn(DOC + "?" + pair), "/");
  const versions = DOC.slice(0, DOC.lastIndexOf("/")) + "?" + pair;
  assert.equal(safeReturn(versions), versions);
});
test("T5 automatic login return works twice; checking/offline cannot consume it", async () => {
  const { connectionDestination } = await import("./routes.ts");
  const source = DOC + "?page=nested%2F%ED%95%9C.html&page_query=q%3D%252F%2B#రు";
  const valid = source.split("#")[0] + "#region=%ED%95%9C";
  for (let cycle = 0; cycle < 2; cycle++) {
    const connect = connectionDestination("unauthenticated", valid)!;
    assert.equal(connectionDestination("checking", connect), null); assert.equal(connectionDestination("degraded", connect), null);
    assert.equal(connectionDestination("verified", connect), valid);
  }
  assert.equal(connectionDestination("verified", "/connect?return=https%3A%2F%2Fevil.invalid"), "/");
});
test("R1 encoded control data in a nonempty Unicode region survives parse, return and Copy", async () => {
  const { pinnedReaderURL, regionFromHash } = await import("./routes.ts");
  const { parseRegionIntent } = await import("./screens/reader-navigation.ts");
  const { copiedLink } = await import("./screens/deep-links.ts");
  const region = "section\tone";
  const route = pinnedReaderURL("11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "a.html", "", "", region);
  const hash = "#region=section%09one";
  assert.ok(route.endsWith(hash));
  assert.equal(regionFromHash(hash), region);
  assert.equal(parseRegionIntent(hash)?.regionId, region);
  assert.equal(safeReturn(route), route);
  const configuration = { configuration: { projects: {}, network: { remote_enabled: false } }, config_revision: 1, etag: "e" };
  assert.equal(copiedLink(configuration, "http://127.0.0.1:1111", route).url, "http://127.0.0.1:1111" + route);
  assert.equal(regionFromHash("#region=section\tone"), null);
  assert.equal(safeReturn(DOC + "#region=section\tone"), "/");
});
test("T5 copied base is approved configuration or explicitly local-only, never a credentialed base", async () => {
  const { copiedLink } = await import("./screens/deep-links.ts");
  const configuration = { configuration: { projects: {}, network: { remote_enabled: true, external_base_url: "https://private.invalid" } }, config_revision: 1, etag: "e" };
  assert.deepEqual(copiedLink(configuration, "http://127.0.0.1:1111", DOC), { url: "https://private.invalid" + DOC, localOnly: false });
  configuration.configuration.network.remote_enabled = false;
  assert.deepEqual(copiedLink(configuration, "http://127.0.0.1:1111", DOC), { url: "http://127.0.0.1:1111" + DOC, localOnly: true });
  assert.throws(() => copiedLink(configuration, "http://secret:token@127.0.0.1:1111", DOC));
});

test("T5 automatic return retains the existing exact SourceSettings project query only on its registered screen", () => {
  const query = "project=11111111-1111-4111-8111-111111111111";
  assert.equal(safeReturn("/settings/projects?" + query), "/settings/projects?" + query);
  assert.equal(safeReturn("/library?" + query), "/");
  assert.equal(safeReturn("/settings/projects?project=unknown"), "/");
});
