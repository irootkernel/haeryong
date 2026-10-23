// React history subscription over the shared canonical route registry.
import { useEffect, useState } from "react";
import { readerContext } from "./reader-context.ts";
import { parseRoute } from "./routes.ts";
import type { ShellRoute } from "./routes.ts";
export { parseRoute, safeReturn } from "./routes.ts";
export type { ScreenId, ShellRoute } from "./routes.ts";

export function navigate(to: string): void {
  history.pushState({ haeryongReaderContext: crypto.randomUUID() }, "", to);
  window.dispatchEvent(new PopStateEvent("popstate"));
}

export interface RouteState {
  url: string;
  route: ShellRoute | null;
}

export function useShellRoute(): RouteState {
  const [state, setState] = useState<RouteState>(() => ({
    url: location.pathname + location.search + location.hash,
    route: parseRoute(location.pathname),
  }));
  useEffect(() => {
    let documentURL = location.pathname + location.search;
    let context = typeof history.state?.haeryongReaderContext === "string" ? history.state.haeryongReaderContext : crypto.randomUUID();
    const update = () => {
      const nextDocument = location.pathname + location.search;
      // Native hash navigation creates a history entry without state. Retain
      // the live page context for that hash-only selection. A new route (also
      // an explicit connect return) must establish its own context before render.
      context = readerContext(documentURL, nextDocument, context, history.state?.haeryongReaderContext, () => crypto.randomUUID());
      if (typeof history.state?.haeryongReaderContext !== "string") {
        history.replaceState({ ...history.state, haeryongReaderContext: context }, "", location.href);
      }
      documentURL = nextDocument;
      setState({
        url: location.pathname + location.search + location.hash,
        route: parseRoute(location.pathname),
      });
    };
    window.addEventListener("popstate", update);
    window.addEventListener("hashchange", update);
    // Every initial history entry gets one unique Reader context: two
    // independent windows must never share an initial open key. Only an
    // absent context is stamped; the URL, query, fragment, history.length
    // and any other state members are preserved, and the notification is a
    // one-shot popstate because the next pass sees the stamped state.
    if (typeof history.state?.haeryongReaderContext !== "string") {
      history.replaceState({ ...history.state, haeryongReaderContext: context }, "", location.href);
      window.dispatchEvent(new PopStateEvent("popstate"));
    }
    return () => { window.removeEventListener("popstate", update); window.removeEventListener("hashchange", update); };
  }, []);
  return state;
}
