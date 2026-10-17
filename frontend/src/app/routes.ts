// Canonical browser route registry (docs/contracts/schema-registry.md). The
// trusted shell serves exactly these content-free routes; unknown paths render
// a client-side unknown-route view. HistoryAPI navigation preserves
// Back/Forward including page/page_query/page_fragment/#region values.

export type ScreenId =
  | "home"
  | "connect"
  | "library"
  | "projects"
  | "project"
  | "sources"
  | "versions"
  | "reader"
  | "maintenance"
  | "job"
  | "trash"
  | "settings";

export interface ShellRoute {
  path: string;
  screen: ScreenId;
  section?: string;
}

const UUID = "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}";

const PROJECT_DETAIL = new RegExp(`^/projects/(${UUID})(/sources)?$`);
const DOCUMENT_VERSIONS = new RegExp(`^/documents/(${UUID})/versions(/(${UUID}))?$`);
const MAINTENANCE_JOB = new RegExp(`^/maintenance/jobs/(${UUID})$`);
const SETTINGS_SECTION = /^\/settings\/(general|connection|sessions|projects|ai|maintenance|storage|retention|backup|about)$/;

export function parseRoute(pathname: string): ShellRoute | null {
  switch (pathname) {
    case "/":
      return { path: pathname, screen: "home" };
    case "/connect":
      return { path: pathname, screen: "connect" };
    case "/library":
      return { path: pathname, screen: "library" };
    case "/projects":
      return { path: pathname, screen: "projects" };
    case "/maintenance":
      return { path: pathname, screen: "maintenance" };
    case "/trash":
      return { path: pathname, screen: "trash" };
    case "/settings":
      return { path: pathname, screen: "settings" };
  }
  if (PROJECT_DETAIL.test(pathname)) {
    return { path: pathname, screen: pathname.endsWith("/sources") ? "sources" : "project" };
  }
  if (DOCUMENT_VERSIONS.test(pathname)) {
    return { path: pathname, screen: pathname.split("/").length === 4 ? "versions" : "reader" };
  }
  if (MAINTENANCE_JOB.test(pathname)) {
    return { path: pathname, screen: "job" };
  }
  const section = SETTINGS_SECTION.exec(pathname);
  if (section) {
    return { path: pathname, screen: "settings", section: section[1] };
  }
  return null;
}

/**
 * Containment validation for a once-decoded page_path (package contract):
 * / separators, no leading / or drive prefix, no empty, "." or ".." segment
 * and no NUL. Literal backslash, newline and tab are representable filename
 * characters and stay valid.
 */
function validDecodedPage(page: string): boolean {
  if (page.includes("\u0000") || page.startsWith("/")) return false;
  // Matches the production query path contract: any initial ASCII letter
  // followed by ":" is a drive-like prefix, separator or not.
  if (/^[a-zA-Z]:/.test(page)) return false;
  if (page === "") return false;
  for (const segment of page.split("/")) {
    if (segment === "" || segment === "." || segment === "..") return false;
  }
  return true;
}

/** Validate a login-return route against the registry; anything else falls back to home. */
export function safeReturn(value: string | null): string {
  // Raw-level shell guards stay: external/protocol-relative targets, literal
  // raw backslash and raw control characters never reach URL parsing.
  if (!value || !value.startsWith("/") || value.startsWith("//") || /[\\\u0000-\u001f\u007f]/.test(value)) {
    return "/";
  }
  try {
    const url = new URL(value, location.origin);
    if (url.origin !== location.origin || url.username || url.password) return "/";
    const path = url.pathname;
    const known =
      ["/", "/connect", "/library", "/projects", "/maintenance", "/trash", "/settings"].includes(path) ||
      new RegExp(`^/projects/${UUID}(/sources)?$`).test(path) ||
      new RegExp(`^/documents/${UUID}/versions(/${UUID})?$`).test(path) ||
      new RegExp(`^/maintenance/jobs/${UUID}$`).test(path) ||
      /^\/settings\/(general|connection|sessions|projects|ai|maintenance|storage|retention|backup|about)$/.test(path);
    const queryKeys = new Set([
      "q",
      "project_id",
      "topic_id",
      "lifecycle",
      "source_basis",
      "page",
      "page_query",
      "page_fragment",
      "context_ref",
    ]);
    for (const [key, entry] of url.searchParams) {
      if (!queryKeys.has(key)) return "/";
      if (key === "page") {
        // Decoded exactly once by URLSearchParams; no trim, replace or second
        // decode. Containment rules per package contract; the blanket control
        // filter does not apply to valid filename characters.
        if (!validDecodedPage(entry)) return "/";
        continue;
      }
      if (/[\u0000-\u001f\u007f]/.test(entry)) return "/";
    }
    return known ? path + url.search + url.hash : "/";
  } catch {
    return "/";
  }
}

