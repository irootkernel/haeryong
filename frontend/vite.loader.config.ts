import { defineConfig } from "vite";

// One self-contained unprivileged derivative. Served by the renderer adapter,
// never a management script or an import from the trusted host's origin.
export default defineConfig({
  build: {
    outDir: "../internal/webassets/dist/renderer",
    emptyOutDir: true,
    sourcemap: false,
    lib: { entry: "src/viewer/loader.ts", name: "HaeryongPageIntegration", formats: ["iife"], fileName: () => "loader.js" },
  },
});
