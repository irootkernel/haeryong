// Scratch verification entrypoint for the labelled Reader/Versions
// exact-document preselection harness. This page is NEVER built into the
// committed production bundle (see vite.harness.config.ts); it exists so the
// browser proof can drive the SAME production ImportControl with an exact
// document context before the real Reader/Versions screens ship.
// The labelled first-commit identity harness (TASK-003-04 R9, ?mode=committest)
// and the labelled REAL caller-identity harness (TASK-003-04 R10, ?mode=real
// with &doc=&ver=) are selected ONLY by URL parameters; every other URL keeps
// the default preselection mode unchanged.
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { PreselectionHarness } from "./app/screens/preselect-harness.tsx";
import { FirstCommitHarness, RealCallerHarness } from "./app/screens/first-commit-harness.tsx";
import { NativeAppHarness, NativePairingHarness } from "./app/screens/native-pairing-harness.tsx";

const params = new URLSearchParams(location.search);
const mode = params.get("mode");
createRoot(document.getElementById("root")!).render(
  mode === "committest" ? <FirstCommitHarness /> : (
    mode === "real" ? <RealCallerHarness documentId={params.get("doc") ?? ""} versionId={params.get("ver") ?? ""} /> : (
      mode === "nativepairing" ? <NativePairingHarness /> : (
        mode === "nativeapp" ? <NativeAppHarness /> : (
          <StrictMode>
            <PreselectionHarness />
          </StrictMode>
        )
      )
    )
  ),
);
