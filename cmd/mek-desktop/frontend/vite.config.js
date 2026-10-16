import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";

export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    // The Wails runtime is served by the app itself, not bundled.
    rollupOptions: { external: ["/wails/runtime.js"] },
  },
  test: { environment: "node", include: ["src/**/*.test.js"] },
});
