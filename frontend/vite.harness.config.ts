import { defineConfig } from "vite";

// Scratch verification build for the labelled Reader/Versions preselection
// harness page ONLY. The output directory is never inside the repository:
// pass an explicit agent-owned scratch path (unique verified task directory
// under /Volumes/RootKernel/tmp) through HAEYONG_HARNESS_OUT_DIR or the
// vite --outDir CLI override. This build is never committed under
// internal/webassets and never served as the production shell.
const outDir = process.env.HAEYONG_HARNESS_OUT_DIR;
if (!outDir) {
  throw new Error("HAEYONG_HARNESS_OUT_DIR (agent scratch path outside the repository) is required for the harness build");
}
export default defineConfig({
  base: '/harness/',
  build: {
    outDir,
    emptyOutDir: true,
    assetsInlineLimit: 0,
    sourcemap: false,
    rollupOptions: {
      input: [new URL("./harness.html", import.meta.url).pathname],
    },
  },
});
