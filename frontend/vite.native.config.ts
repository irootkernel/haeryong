import { defineConfig } from "vite";

// Separate native-shell entrypoint build. Never embedded in the Go daemon and
// not committed; TASK-003-05 binds the real desktop port here.
export default defineConfig({
  build: {
    outDir: "dist-native",
    emptyOutDir: true,
    assetsInlineLimit: 0,
    sourcemap: false,
    rollupOptions: { input: "src/native-main.tsx" },
  },
});
