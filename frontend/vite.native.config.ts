import { defineConfig } from "vite";

// Separate native-shell entrypoint build. Never embedded in the Go daemon and
// not committed; the reused pinned beta28 harness ReaderMode serves this
// bundle at its fixed static origin. TASK-003-05 binds the production port.
export default defineConfig({
  build: {
    outDir: "dist-native",
    emptyOutDir: true,
    assetsInlineLimit: 0,
    sourcemap: false,
    rollupOptions: { input: "native.html" },
  },
});
