import { defineConfig } from "vite";

// Production trusted-shell build. Output is committed under
// internal/webassets/dist and served by the Go daemon; no dev server is used.
// check_build.py overrides --outDir for read-only verification builds.
export default defineConfig({
  build: {
    outDir: "../internal/webassets/dist",
    emptyOutDir: true,
    assetsInlineLimit: 0,
    sourcemap: false,
  },
});
