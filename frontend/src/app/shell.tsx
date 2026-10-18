// Trusted application shell: navigation, service indicator, route screens and
// the connection-state banner. Screens without an implementing owner render a
// truthful not-implemented placeholder; capability absence is never faked.
import { useEffect, useRef, useState } from "react";
import { NOTICE, ShellController } from "../client/controller.ts";
import type { ShellState } from "../client/controller.ts";
import { ConnectScreen } from "./connect.tsx";
import { HomeScreen } from "./screens/home.tsx";
import { LibraryScreen } from "./screens/library.tsx";
import { StorageScreen } from "./screens/storage.tsx";
import { ReaderScreen } from "./screens/reader.tsx";
import { navigate, safeReturn, useShellRoute } from "./router.tsx";
import type { ScreenId } from "./router.tsx";
import "./theme.css";

const NAV_PRIMARY: { screen: ScreenId; path: string; label: string }[] = [
  { screen: "home", path: "/", label: "홈" },
  { screen: "library", path: "/library", label: "서재" },
  { screen: "projects", path: "/projects", label: "프로젝트" },
  { screen: "maintenance", path: "/maintenance", label: "정비" },
];
const NAV_SECONDARY: { screen: ScreenId; path: string; label: string }[] = [
  { screen: "trash", path: "/trash", label: "휴지통" },
  { screen: "settings", path: "/settings", label: "설정" },
];

const SCREEN_TITLES: Record<ScreenId, string> = {
  home: "홈",
  connect: "연결",
  library: "서재",
  projects: "프로젝트",
  project: "프로젝트",
  sources: "원본",
  versions: "버전",
  reader: "읽기",
  maintenance: "정비",
  job: "정비 작업",
  trash: "휴지통",
  settings: "설정",
};

function NavLink({ screen, path, label, current }: { screen: ScreenId; path: string; label: string; current: boolean }) {
  return (
    <a
      className="nav-link"
      href={path}
      aria-current={current ? "page" : undefined}
      data-screen={screen}
      onClick={(event) => {
        event.preventDefault();
        navigate(path);
      }}
    >
      {label}
    </a>
  );
}

function Banner({ state, controller }: { state: ShellState; controller: ShellController }) {
  if (state.phase === "degraded") {
    return (
      <div className="banner" data-tone="warning" role="alert">
        <p>{NOTICE.disconnected}</p>
        <button type="button" onClick={() => void controller.verify()}>
          다시 연결
        </button>
      </div>
    );
  }
  if (state.phase === "incompatible") {
    return (
      <div className="banner" data-tone="danger" role="alert">
        <p>{NOTICE.incompatible}</p>
      </div>
    );
  }
  return null;
}

function PlaceholderScreen({ screen }: { screen: ScreenId }) {
  return (
    <section className="panel">
      <h1>{SCREEN_TITLES[screen]}</h1>
      <p>이 화면은 아직 구현되지 않았습니다.</p>
      <p className="placeholder-note">
        아래에서 다른 화면으로 이동하거나, 연결 화면에서 연결 상태를 확인할 수 있습니다.
      </p>
      <div className="form-row">
        <button type="button" className="primary" onClick={() => navigate("/")}>
          홈으로
        </button>
        <button type="button" onClick={() => navigate("/connect")}>
          연결 화면
        </button>
      </div>
    </section>
  );
}

/** Reader route binding: decodes the once-encoded page/query/fragment. */
function ReaderRoute({ controller, path }: { controller: ShellController; path: string }) {
  const parts = path.split("/");
  const documentId = parts[2] ?? "";
  const versionId = parts[4] ?? "";
  const params = new URLSearchParams(location.search);
  return (
    <ReaderScreen
      controller={controller}
      documentId={documentId}
      versionId={versionId}
      page={params.has("page") ? params.get("page") : null}
      pageQuery={params.get("page_query") ?? ""}
      pageFragment={params.get("page_fragment") ?? ""}
    />
  );
}

export function App({ controller }: { controller: ShellController }) {
  const [state, setState] = useState<ShellState>(controller.state);
  const { route } = useShellRoute();
  const appliedReturn = useRef(false);

  useEffect(() => controller.subscribe(setState), [controller]);
  useEffect(() => {
    // A /connect?return= entry validates the return route against the registry
    // exactly once when the session becomes verified.
    if (state.phase === "verified" && !appliedReturn.current) {
      appliedReturn.current = true;
      const target = new URLSearchParams(location.search).get("return");
      if (target !== null) {
        const destination = safeReturn(target);
        if (destination !== location.pathname + location.search + location.hash) {
          history.replaceState({}, "", destination);
          window.dispatchEvent(new PopStateEvent("popstate"));
        }
      }
    }
  }, [state.phase]);
  useEffect(() => {
    controller.start();
    return () => controller.stop();
  }, [controller]);
  useEffect(() => {
    const onVisibility = () => {
      if (!document.hidden) void controller.verify();
    };
    document.addEventListener("visibilitychange", onVisibility);
    return () => document.removeEventListener("visibilitychange", onVisibility);
  }, [controller]);

  if (state.phase === "unauthenticated" || state.phase === "checking") {
    return (
      <main className="shell-content">
        <ConnectScreen controller={controller} state={state} />
      </main>
    );
  }

  const stateLabel =
    state.phase === "verified" ? "연결됨" : state.phase === "degraded" ? "연결 끊김" : "호환 불가";
  const screen = route?.screen ?? "home";
  return (
    <div className="shell">
      <nav className="shell-sidebar" aria-label="주요 탐색">
        {NAV_PRIMARY.map((item) => (
          <NavLink key={item.path} {...item} current={screen === item.screen} />
        ))}
        <p className="nav-group-label">보조</p>
        {NAV_SECONDARY.map((item) => (
          <NavLink key={item.path} {...item} current={screen === item.screen} />
        ))}
      </nav>
      <div className="shell-main">
        <header className="shell-header">
          <p className="shell-brand">Haeryong</p>
          <p className="service-indicator">
            <span className="muted-code">{location.origin}</span>
            <span className="service-state" data-state={state.phase}>
              {stateLabel}
            </span>
            {state.capabilities && (
              <span>
                서재 <span className="muted-code">{state.capabilities.library_id.slice(0, 8)}</span>
              </span>
            )}
          </p>
        </header>
        <Banner state={state} controller={controller} />
        <main className="shell-content" key={`${state.capabilities?.library_id ?? ""}:${state.session?.principal_id ?? ""}:${state.capabilities?.runtime_generation ?? ""}`}>
          {state.phase === "incompatible" ? (
            <section className="panel"><h1>연결 확인 필요</h1><p>{NOTICE.incompatible}</p></section>
          ) : route === null ? (
            <section className="panel">
              <h1>열 수 없는 주소</h1>
              <p className="placeholder-note">이 주소의 화면은 사용할 수 없습니다.</p>
              <div className="form-row">
                <button type="button" className="primary" onClick={() => navigate("/")}>
                  홈으로
                </button>
              </div>
            </section>
          ) : screen === "connect" ? (
            <ConnectScreen controller={controller} state={state} />
          ) : screen === "home" ? (
            <HomeScreen controller={controller} />
          ) : screen === "library" ? (
            <LibraryScreen controller={controller} />
          ) : screen === "reader" && route.path ? (
            <ReaderRoute controller={controller} path={route.path} />
          ) : screen === "settings" && route.section === "storage" ? (
            <StorageScreen controller={controller} />
          ) : (
            <PlaceholderScreen screen={screen} />
          )}
        </main>
      </div>
    </div>
  );
}
