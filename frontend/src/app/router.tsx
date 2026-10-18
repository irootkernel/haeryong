// React history subscription over the shared canonical route registry.
import { useEffect, useState } from "react";
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
    const update = () =>
      setState({
        url: location.pathname + location.search + location.hash,
        route: parseRoute(location.pathname),
      });
    window.addEventListener("popstate", update);
    window.addEventListener("hashchange", update);
    // Every initial history entry gets one unique Reader context: two
    // independent windows must never share an initial open key. Only an
    // absent context is stamped; the URL, query, fragment, history.length
    // and any other state members are preserved, and the notification is a
    // one-shot popstate because the next pass sees the stamped state.
    if (typeof history.state?.haeryongReaderContext !== "string") {
      history.replaceState({ ...history.state, haeryongReaderContext: crypto.randomUUID() }, "", location.href);
      window.dispatchEvent(new PopStateEvent("popstate"));
    }
    return () => { window.removeEventListener("popstate", update); window.removeEventListener("hashchange", update); };
  }, []);
  return state;
}
