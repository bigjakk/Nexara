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
        manualChunks: {
          "vendor-react": ["react", "react-dom", "react-router-dom"],
          "vendor-tanstack": [
            "@tanstack/react-query",
            "@tanstack/react-table",
          ],
          "vendor-ui": [
            "@radix-ui/react-avatar",
            "@radix-ui/react-checkbox",
            "@radix-ui/react-context-menu",
            "@radix-ui/react-dialog",
            "@radix-ui/react-dropdown-menu",
            "@radix-ui/react-label",
            "@radix-ui/react-popover",
            "@radix-ui/react-select",
            "@radix-ui/react-separator",
            "@radix-ui/react-slider",
            "@radix-ui/react-slot",
            "@radix-ui/react-switch",
            "@radix-ui/react-tabs",
            "@radix-ui/react-tooltip",
          ],
          "vendor-charts": ["recharts"],
          "vendor-flow": ["@xyflow/react"],
          "vendor-terminal": [
            "@xterm/xterm",
            "@xterm/addon-fit",
            "@xterm/addon-web-links",
          ],
          "vendor-i18n": ["i18next", "react-i18next"],
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
