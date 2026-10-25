import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { useAuthStore } from "@/stores/auth-store";
import { useConsoleStore } from "@/stores/console-store";
import { usePreferencesStore } from "@/stores/preferences-store";
import type { ConsoleOpenMode } from "@/stores/preferences-store";
import { resetConsoleStore } from "../console.mocks";
import { ADMIN } from "@/test/fake-server";
import { consoleIdentity, takeLaunch } from "./console-launch";
import { consoleSessionHref } from "./console-session";
import { openConsole, type ConsoleRequest } from "./open-console";

vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

const target = {
  clusterID: "cluster01",
  node: "pve-01",
  vmid: 101,
  type: "vm_vnc",
  resourceId: "resource-01",
  kind: "vm",
} as const;
const request: ConsoleRequest = { ...target, guestName: "linux01" };

const noKeys = { ctrlKey: false, metaKey: false };

function prefer(mode: ConsoleOpenMode) {
  usePreferencesStore.setState((s) => ({
    preferences: { ...s.preferences, consoleOpenMode: mode },
  }));
}

beforeEach(() => {
  resetConsoleStore();
  useAuthStore.setState({ user: ADMIN, isAuthenticated: true });
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.mocked(toast.error).mockClear();
  prefer("floating");
});

// Which mode a click asks for is wantsNewTab's (console-session.test.ts); this
// is that the answer reaches the right place.
describe("openConsole", () => {
  it("opens the floating window, labelled from the guest's name", () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);

    openConsole(request, noKeys);

    const { tabs, windowMode } = useConsoleStore.getState();
    expect(open).not.toHaveBeenCalled();
    expect(tabs.map((t) => t.label)).toEqual(["VNC: linux01"]);
    expect(windowMode).toBe("floating");
  });

  it("opens a browser tab of its own, with noopener and a launch for this user", () => {
    prefer("tab");
    const open = vi.spyOn(window, "open").mockReturnValue(null);

    openConsole(request, noKeys);

    // noopener: the tab gets its own event loop (and no handle back here).
    const href = consoleSessionHref({ ...target, label: "" });
    expect(open).toHaveBeenCalledWith(
      expect.stringMatching(/#launch=[0-9a-f]{32}$/),
      "_blank",
      "noopener",
    );
    const [url] = open.mock.calls[0] ?? [];
    const [path, launch] = String(url).split("#launch=");
    expect(path).toBe(href);
    expect(
      takeLaunch(
        `#launch=${launch ?? ""}`,
        ADMIN.id,
        consoleIdentity({ ...target, label: "" }),
      ),
    ).toBe(true);
    expect(useConsoleStore.getState().tabs).toEqual([]);
  });

  it("opens no tab for a console whose node is not known yet", () => {
    // Every launcher's: the node comes from a list that may still be loading.
    prefer("tab");
    const open = vi.spyOn(window, "open").mockReturnValue(null);

    openConsole({ ...request, node: "" }, noKeys);

    expect(open).not.toHaveBeenCalled();
    expect(toast.error).toHaveBeenCalledOnce();
  });
});
