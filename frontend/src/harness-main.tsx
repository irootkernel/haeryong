// Scratch verification entrypoint for the labelled Reader/Versions
// exact-document preselection harness. This page is NEVER built into the
// committed production bundle (see vite.harness.config.ts); it exists so the
// browser proof can drive the SAME production ImportControl with an exact
// document context before the real Reader/Versions screens ship.
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { PreselectionHarness } from "./app/screens/preselect-harness.tsx";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <PreselectionHarness />
  </StrictMode>,
);
