/// <reference types="vite/client" />
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { MutationObserver } from "@tanstack/react-query";
import { queryClient } from "@/lib/query-client";
import { deferred } from "@/test/fake-server";
import {
  emptyPerSessionStores,
  PER_SESSION_STORES,
} from "@/test/per-session-stores";
import { useBrandingStore } from "./branding-store";
import { useChangelogStore } from "./changelog-store";
import { useConsoleStore } from "./console-store";
import { useHealthMuteStore } from "./health-mute-store";
import { useMetricStore } from "./metric-store";
import { usePBSKeyStore } from "./pbs-key-store";
import { usePreferencesStore } from "./preferences-store";
import { resetSessionState } from "./session-reset";
import { useSidebarStore } from "./sidebar-store";
import { useTaskLogStore } from "./task-log-store";
import { useThemeStore } from "./theme-store";

// Synthetic key file, as in PendingPBSKey.test.tsx.
const PBS_KEY =
  '{"kdf":null,"data":"CANARY-session-reset-key","fingerprint":"aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99"}';

/**
 * The stores that hold what is the browser's rather than the session's: how it
 * looks and a setting that stands until the user changes it (see the table in
 * stores/session-reset.ts). `set` gives each a value no default has.
 */
const BROWSER_STORES: Record<string, { set: () => void; read: () => unknown }> =
  {
    "theme-store.ts": {
      set: () => {
        useThemeStore.getState().setMode("dark");
      },
      read: () => useThemeStore.getState().mode,
    },
    "sidebar-store.ts": {
      set: () => {
        useSidebarStore.getState().setWidth(300);
        useSidebarStore.getState().setPerspective("storage");
        useSidebarStore.getState().toggleNode("cluster:cluster01");
      },
      read: () => {
        const s = useSidebarStore.getState();
        return [s.width, s.perspective, [...s.expandedNodes]];
      },
    },
    "preferences-store.ts": {
      set: () => {
        usePreferencesStore
          .getState()
          .setPreferences({ byteUnit: "decimal", accentColor: "teal" });
      },
      read: () => usePreferencesStore.getState().preferences,
    },
    "health-mute-store.ts": {
      set: () => {
        useHealthMuteStore.getState().mute("task_failed");
      },
      read: () => useHealthMuteStore.getState().mutedTypes,
    },
    "changelog-store.ts": {
      set: () => {
        useChangelogStore.getState().setLastSeenVersion("9.9.9");
      },
      read: () => useChangelogStore.getState().lastSeenVersion,
    },
    "branding-store.ts": {
      set: () => {
        useBrandingStore.getState().setAppTitle("Example Title");
      },
      read: () => useBrandingStore.getState().appTitle,
    },
  };

/** Stores that are neither, and why. */
const NOT_RESET_HERE: Record<string, string> = {
  "auth-store.ts": "the session itself; its own paths call the reset",
  "session-reset.ts": "the reset",
  "pbs-key-store.ts":
    "owner-scoped: shown once, to the user whose save generated it",
  "websocket-store.ts": "AppShell disconnects it when it unmounts",
};

/** Every production source file, as text (the repo's source guards read src this way). */
const SOURCES: Record<string, string> = import.meta.glob(
  ["/src/**/*.{ts,tsx}", "!/src/**/*.test.{ts,tsx}", "!/src/test/**"],
  { eager: true, query: "?raw", import: "default" },
);

function leaveNothingBehind() {
  localStorage.clear();
  queryClient.clear();
  emptyPerSessionStores();
  usePBSKeyStore.setState({ pending: [] });
  useThemeStore.getState().setMode("system");
  useBrandingStore.setState({ appTitle: "Nexara" });
  useChangelogStore.setState({ lastSeenVersion: null });
  useHealthMuteStore.setState({ mutedTypes: [] });
  usePreferencesStore.setState({
    preferences: {
      byteUnit: "binary",
      dateFormat: "relative",
      refreshInterval: 30,
      accentColor: "default",
      language: "en",
    },
  });
  useSidebarStore.setState({
    width: 240,
    perspective: "vms",
    expandedNodes: new Set<string>(),
  });
  document.title = "";
  // localStorage again: the setters above wrote their defaults back to it.
  localStorage.clear();
}

beforeEach(leaveNothingBehind);
afterEach(leaveNothingBehind);

describe("resetSessionState: the query and mutation caches", () => {
  it("empties both, mutations in flight included", async () => {
    queryClient.setQueryData(["clusters"], [{ id: "cluster01" }]);
    const gate = deferred<string>();
    const mutation = new MutationObserver(queryClient, {
      mutationFn: () => gate.promise,
    });
    const settled = mutation.mutate(undefined);
    // The same setup holds data before the reset — the emptiness below is the
    // reset's doing, not an empty fixture's.
    expect(queryClient.getQueryCache().getAll()).toHaveLength(1);
    expect(queryClient.getMutationCache().getAll()).toHaveLength(1);

    resetSessionState();

    expect(queryClient.getQueryData(["clusters"])).toBeUndefined();
    expect(queryClient.getQueryCache().getAll()).toHaveLength(0);
    expect(queryClient.getMutationCache().getAll()).toHaveLength(0);

    gate.resolve("done");
    await settled;
  });
});

describe("resetSessionState: the stores that belong to the session", () => {
  it.each(Object.entries(PER_SESSION_STORES))("empties %s", (_file, probe) => {
    probe.dirty();
    expect(probe.holdsData()).toBe(true);

    resetSessionState();

    expect(probe.holdsData()).toBe(false);
  });

  it("takes the persisted console tabs with it, and keeps where the window sits", () => {
    const consoleStore = useConsoleStore.getState();
    consoleStore.setWindowPosition({ x: 11, y: 22 });
    consoleStore.setWindowSize({ width: 640, height: 480 });
    PER_SESSION_STORES["console-store.ts"]?.dirty();
    const persistedBefore = JSON.parse(
      localStorage.getItem("nexara-console-tabs") ?? "{}",
    ) as { state?: { tabs?: unknown[] } };
    // The tab really is in the persisted copy that a reload would restore.
    expect(persistedBefore.state?.tabs).toHaveLength(1);

    resetSessionState();

    const persisted = JSON.parse(
      localStorage.getItem("nexara-console-tabs") ?? "{}",
    ) as {
      state?: {
        tabs?: unknown[];
        activeTabId?: string | null;
        windowMode?: string;
        windowPosition?: unknown;
        windowSize?: unknown;
      };
    };
    expect(persisted.state?.tabs).toEqual([]);
    expect(persisted.state?.activeTabId).toBeNull();
    expect(persisted.state?.windowMode).toBe("hidden");
    expect(persisted.state?.windowPosition).toEqual({ x: 11, y: 22 });
    expect(persisted.state?.windowSize).toEqual({ width: 640, height: 480 });
  });

  it("takes the persisted dismissed issues with it", () => {
    PER_SESSION_STORES["health-dismiss-store.ts"]?.dirty();
    expect(
      JSON.parse(localStorage.getItem("nexara-health-dismissed") ?? "[]"),
    ).toHaveLength(1);

    resetSessionState();

    expect(
      JSON.parse(localStorage.getItem("nexara-health-dismissed") ?? "[]"),
    ).toEqual([]);
  });

  it("takes dismissals another tab persisted too, which this tab's own list never held", () => {
    // Memory empty, storage not: restoreAll would have left them, and they carry
    // a cluster id, a target and the issue's detail text.
    localStorage.setItem(
      "nexara-health-dismissed",
      JSON.stringify([
        "cluster01|node_offline|pve-01|critical|pve-01 is offline",
      ]),
    );
    expect(PER_SESSION_STORES["health-dismiss-store.ts"]?.holdsData()).toBe(
      false,
    );

    resetSessionState();

    expect(
      JSON.parse(localStorage.getItem("nexara-health-dismissed") ?? "[]"),
    ).toEqual([]);
  });

  it("keeps the task panel's size and the metrics' refresh interval, which are the browser's", () => {
    useTaskLogStore.getState().setPanelOpen(false);
    useTaskLogStore.getState().setPanelHeight(321);
    useMetricStore.getState().setRefreshInterval(5000);
    PER_SESSION_STORES["task-log-store.ts"]?.dirty();
    PER_SESSION_STORES["metric-store.ts"]?.dirty();

    resetSessionState();

    expect(useTaskLogStore.getState().panelOpen).toBe(false);
    expect(useTaskLogStore.getState().panelHeight).toBe(321);
    expect(useMetricStore.getState().refreshInterval).toBe(5000);
  });
});

describe("resetSessionState: what belongs to the browser, or to a key's owner", () => {
  it.each(Object.entries(BROWSER_STORES))(
    "leaves %s as it was",
    (_file, { set, read }) => {
      const initial = JSON.stringify(read());
      set();
      const chosen = JSON.stringify(read());
      // The value is one the store does not start with, so "unchanged" below
      // cannot mean "back to the default".
      expect(chosen).not.toBe(initial);

      resetSessionState();

      expect(JSON.stringify(read())).toBe(chosen);
    },
  );

  it("does not touch a PBS key still waiting to be saved — it is owner-scoped, not session-scoped", () => {
    // Nobody is signed in, so deliver keeps the key for its owner, as it does
    // on the login page once a session has expired.
    usePBSKeyStore.getState().deliver({
      owner: "user-owner",
      cluster: "cluster01",
      clusterName: "cluster01",
      storage: "store01",
      keyText: PBS_KEY,
    });
    expect(usePBSKeyStore.getState().pending).toHaveLength(1);

    resetSessionState();

    const pending = usePBSKeyStore.getState().pending;
    expect(pending).toHaveLength(1);
    expect(pending[0]?.keyText).toBe(PBS_KEY);
    expect(pending[0]?.owner).toBe("user-owner");
  });
});

describe("every store has a verdict", () => {
  it("lists every file in src/stores, and nothing that is not there", () => {
    const files = Object.keys(import.meta.glob("./*.{ts,tsx}"))
      .map((f) => f.slice("./".length))
      .filter((f) => !/\.test\.tsx?$/.test(f))
      .sort();
    const classified = [
      ...Object.keys(PER_SESSION_STORES),
      ...Object.keys(BROWSER_STORES),
      ...Object.keys(NOT_RESET_HERE),
    ].sort();

    // A new store fails here until someone decides which it is. Per-session
    // (names, ids or content derived from the signed-in user's session): add a
    // probe to src/test/per-session-stores.ts and reset it in
    // stores/session-reset.ts. A setting of the browser's: add it to
    // BROWSER_STORES above. Neither: say why in NOT_RESET_HERE.
    expect(
      files,
      "src/stores holds a file that stores/session-reset.test.ts does not classify (or classifies one that is gone)",
    ).toEqual(classified);
  });

  it("finds no zustand store anywhere but src/stores, where the table above can see it", () => {
    // Any file that imports zustand (or one of its middleware) holds a store,
    // wherever it lives: one under features/, or a .tsx, would otherwise escape
    // the check above, which reads only src/stores.
    const importsZustand = /\b(?:from|import)\s+["']zustand(?:\/[\w-]+)?["']/;
    const holders = Object.entries(SOURCES)
      .filter(([, source]) => importsZustand.test(source))
      .map(([path]) => path);

    // Control: the pattern and the glob do see the stores that are there,
    // including one that imports only the middleware path.
    expect(holders).toContain("/src/stores/console-store.ts");
    expect(holders).toContain("/src/stores/auth-store.ts");

    expect(
      holders.filter((path) => !path.startsWith("/src/stores/")),
      "a zustand store outside src/stores: move it there and classify it in stores/session-reset.test.ts (per-session, or the browser's)",
    ).toEqual([]);
  });
});
