import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import { fileURLToPath, URL } from "node:url";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    globals: true,
    // Vitest's 5s default assumes a dev box. These dialog tests drive real
    // Radix Selects through jsdom, and a full-suite run puts the slowest of
    // them (AlertRuleForm, BackupJobDialog, AddVeeamServerDialog,
    // EditClusterDialog) at 1.7-2.3s even on an unloaded 12-core box — barely
    // 2x, which loaded CI hardware spends easily. That is what timed out on
    // Gitea run 524. The cost is inherent: userEvent's delay and
    // pointerEventsCheck tunables buy ~5%, the rest is jsdom layout and React
    // re-render. 15s (vitest's own browser-mode default) restores the margin
    // without hiding hangs — waitFor keeps its separate 1s budget, so a stuck
    // assertion still fails in about a second.
    testTimeout: 15_000,
  },
});
