import { ProviderScreen } from "./screens/provider.tsx";
import { BackupScreen } from "./screens/backup.tsx";
import { SessionsScreen } from "./screens/sessions.tsx";
import {TrashScreen} from "./screens/trash.tsx";
import { connectionDestination } from "./routes.ts";
import { VersionsScreen } from "./screens/versions.tsx";
import { parseRegionIntent } from "./screens/reader-navigation.ts";
// Trusted application shell: navigation, service indicator, route screens and
// the connection-state banner. Screens without an implementing owner render a
// truthful not-implemented placeholder; capability absence is never faked.
import { useEffect, useState } from "react";
import { NOTICE, ShellController } from "../client/controller.ts";
import type { ShellState } from "../client/controller.ts";
import { ConnectScreen } from "./connect.tsx";
import { HomeScreen } from "./screens/home.tsx";
import { LibraryScreen } from "./screens/library.tsx";
import { ProjectsScreen, SourcesScreen, SourceSettings } from "./screens/sources.tsx";
import { StorageScreen } from "./screens/storage.tsx";
import { ReaderScreen } from "./screens/reader.tsx";
import { navigate, useShellRoute } from "./router.tsx";
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
  // Cleanup alerts render independently of the auth banner branches: a
  // surviving destination temp path must stay visible even while degraded
  // or incompatible, instead of being masked by the early returns.
  const cleanup = state.cleanupAlerts.length > 0 && (
    <div className="banner" data-tone="warning" role="alert" data-testid="cleanup-alerts">
      {state.cleanupAlerts.map((tempPath) => (
        <p key={tempPath} data-testid="cleanup-alert">
          {tempPath === ""
            ? "이전 내려받기 선택의 정리 여부를 확인할 수 없습니다. 임시 파일이 남아 있을 수 있으니 확인 후 직접 삭제할 수 있습니다."
            : `이전 내려받기 흐름이 화면을 벗어난 뒤 임시 파일 정리에 실패했습니다. 남아 있을 수 있는 경로: ${tempPath}`}
          {" "}창/연결을 닫으면 정리가 다시 시도됩니다.
          <button type="button" onClick={() => controller.dismissRuntimeCleanupAlert(tempPath)}>
            이 알림 닫기
          </button>
        </p>
      ))}
    </div>
  );
  let authBanner: React.ReactNode = null;
  if (state.phase === "degraded") {
    authBanner = (
      <div className="banner" data-tone="warning" role="alert">
        <p>{NOTICE.disconnected}</p>
        <button type="button" onClick={() => void controller.verify()}>
          다시 연결
        </button>
      </div>
    );
  } else if (state.phase === "incompatible") {
    authBanner = (
      <div className="banner" data-tone="danger" role="alert">
        <p>{state.notice}</p>
      </div>
    );
  }
  if (cleanup && authBanner) {
    return (
      <div>
        {cleanup}
        {authBanner}
      </div>
    );
  }
  return cleanup || authBanner;
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
        {screen === "settings" && (
          <>
            <a
              className="action-link"
              href="/settings/sessions"
              onClick={(event) => {
                if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
                event.preventDefault();
                navigate("/settings/sessions");
              }}
            >
              세션 및 보안 정책
            </a>
            <a
              className="action-link"
              href="/settings/ai"
              onClick={(event) => {
                if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
                event.preventDefault();
                navigate("/settings/ai");
              }}
            >
              AI 제공자
            </a>
          </>
        )}
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
      regionIntent={parseRegionIntent(location.hash)}
    />
  );
}

export function App({ controller }: { controller: ShellController }) {
  const [state, setState] = useState<ShellState>(controller.state);
  const { route, url } = useShellRoute();

  useEffect(() => controller.subscribe(setState), [controller]);
  useEffect(() => {
    const destination = connectionDestination(state.phase, url);
    if (destination !== null) {
      history.replaceState({ ...history.state, haeryongReaderContext: crypto.randomUUID() }, "", destination);
      window.dispatchEvent(new PopStateEvent("popstate"));
    }
  }, [state.phase, url]);
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

  // A native first boot can have an initialized but stopped daemon. Its
  // signed discovery fails before any session is attached; keep the
  // explicit start/retry controls reachable instead of showing an empty
  // last-confirmed library. Previously attached sessions keep their shell.
  const nativeBootUnavailable = controller.nativeSessionAvailable && state.phase === "degraded"
    && state.session === null && state.capabilities === null;
  if (state.phase === "unauthenticated" || state.phase === "checking" || nativeBootUnavailable) {
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
            <section className="panel"><h1>연결 확인 필요</h1><p>{state.notice}</p></section>
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
          ) : screen === "trash" ? (<TrashScreen controller={controller}/>) : screen === "versions" ? (
            <VersionsScreen key={route.path + ":" + history.state?.haeryongReaderContext} controller={controller} documentId={route.path.split("/")[2]!} />
          ) : screen === "reader" && route.path ? (
            <ReaderRoute controller={controller} path={route.path} />
          ) : screen === "projects" || screen === "project" || (screen === "settings" && route.section === "projects" && !new URLSearchParams(location.search).has("project")) ? (
            <ProjectsScreen controller={controller} />
          ) : screen === "sources" ? (
            <SourcesScreen key={route.path.split("/")[2]} controller={controller} projectId={route.path.split("/")[2]!} />
          ) : screen === "settings" && route.section === "projects" ? (
            <SourceSettings key={new URLSearchParams(location.search).get("project")} controller={controller} projectId={new URLSearchParams(location.search).get("project")!} />
          ) : screen === "settings" && route.section === "ai" ? (<ProviderScreen controller={controller} />
          ) : screen === "settings" && route.section === "sessions" ? (
            <SessionsScreen controller={controller} />
          ) : screen === "settings" && route.section === "backup" ? (<BackupScreen controller={controller} />
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
