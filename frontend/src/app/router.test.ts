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
