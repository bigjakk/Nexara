import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { fileURLToPath, URL } from "node:url";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  build: {
    // Browser floor, NOT a bare "es2022"+: esbuild only transpiles syntax
    // down to the target, so anything newer (class static blocks, logical
    // assignment) makes older mobile browsers throw SyntaxError before the
    // app boots — a silent white screen. Self-hosted users check dashboards
    // from old phone browsers, so keep this as low as possible. The hard
    // floor is top-level await (used by noVNC's WebCodecs probe), which
    // cannot be transpiled away: Chrome/Edge/Firefox 89, Safari 15.
    target: ["chrome89", "edge89", "firefox89", "safari15"],
    rollupOptions: {
      output: {
        // Vite 7 / rolldown requires manualChunks as a function (the object
        // form is rollup-only). Keyed off the module path to preserve the
        // same deliberate vendor splits.
        manualChunks: (id: string) => {
          if (!id.includes("node_modules")) return undefined;
          if (/node_modules\/(react|react-dom|react-router|react-router-dom)\//.test(id))
            return "vendor-react";
          if (id.includes("node_modules/@tanstack/")) return "vendor-tanstack";
          if (id.includes("node_modules/@radix-ui/")) return "vendor-ui";
          if (id.includes("node_modules/recharts/")) return "vendor-charts";
          if (id.includes("node_modules/@xyflow/")) return "vendor-flow";
          if (id.includes("node_modules/@xterm/")) return "vendor-terminal";
          if (/node_modules\/(i18next|react-i18next)\//.test(id))
            return "vendor-i18n";
          return undefined;
        },
      },
    },
  },
  server: {
    port: 3000,
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: true,
      },
      "/ws": {
        target: "http://localhost:8080",
        ws: true,
      },
    },
  },
});
