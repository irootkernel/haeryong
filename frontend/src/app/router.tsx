// React history subscription over the shared canonical route registry.
import { useEffect, useState } from "react";
import { parseRoute } from "./routes.ts";
import type { ShellRoute } from "./routes.ts";
export { parseRoute, safeReturn } from "./routes.ts";
export type { ScreenId, ShellRoute } from "./routes.ts";

export function navigate(to: string): void {
  history.pushState({}, "", to);
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
    return () => window.removeEventListener("popstate", update);
  }, []);
  return state;
}
