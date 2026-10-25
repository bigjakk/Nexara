import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useLocation } from "react-router-dom";
import type { ReactNode } from "react";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { ApiClientError, apiClient, USER_KEY } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { useConsoleStore, useConsoleStoreApi } from "@/stores/console-store";
import { ADMIN, VIEWER } from "@/test/fake-server";
import { renderWithProviders } from "@/test/test-utils";
import { resetConsoleStore } from "../console.mocks";
import { leaveLaunch } from "../lib/console-launch";
import type { ConsoleTab } from "../types/console";
import { ConsoleSessionPage } from "./ConsoleSessionPage";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);
// The event hub's socket is AppShell's to test; here it would dial.
vi.mock("@/hooks/useLiveUpdates", () => ({ useLiveUpdates: vi.fn() }));

// The viewers are stubbed down to what the page hands them: the tab, whether
// to dial, and the console store they would run on, kept for a test to reach.
const viewer: { store: ReturnType<typeof useConsoleStoreApi> | null } = {
  store: null,
};
function ViewerStub({ tab, visible }: { tab: ConsoleTab; visible: boolean }) {
  viewer.store = useConsoleStoreApi();
  return (
    <div
      data-testid="viewer"
      data-node={tab.node}
      data-label={tab.label}
      data-resource={tab.resourceId ?? ""}
      data-visible={String(visible)}
    />
  );
}
vi.mock("../components/VNCViewer", () => ({
  VNCViewer: ({
    tab,
    visible,
    windowControls,
  }: {
    tab: ConsoleTab;
    visible: boolean;
    windowControls?: boolean;
  }) => (
    <>
      <ViewerStub tab={tab} visible={visible} />
      <span data-testid="window-controls">{String(windowControls)}</span>
    </>
  ),
}));
vi.mock("../components/Terminal", () => ({
  Terminal: ({ tab, visible }: { tab: ConsoleTab; visible: boolean }) => (
    <ViewerStub tab={tab} visible={visible} />
  ),
}));

const VNC_LINK =
  "/console/session?cluster=cluster01&type=vm_vnc&node=pve-01&vmid=101&resource=resource-01";
const OWNER_KEY = "nexara-console-owner";

/** The guest the VM query answers with, as the server would send it. */
let guest: Record<string, unknown> | null;

function LocationProbe() {
  const location = useLocation();
  return (
    <p data-testid="location">
      {location.search}
      {location.hash}
    </p>
  );
}

/** The link as a Nexara launcher opens it: with a launch left for `user`. */
function launched(link: string, user: string = ADMIN.id): string {
  const launch = leaveLaunch(
    {
      clusterID: "cluster01",
      type: "vm_vnc",
      node: "pve-01",
      vmid: 101,
      label: "",
    },
    user,
  );
  return `${link}#launch=${launch ?? ""}`;
}

/** The nodes as the page's own query has them: rendered in its batch. */
function NodesProbe() {
  const { data } = useClusterNodes("cluster01");
  return <p data-testid="nodes">{data?.map((n) => n.name).join(",")}</p>;
}

function renderAt(entry: string, extra?: ReactNode) {
  return renderWithProviders(
    <>
      <ConsoleSessionPage />
      <LocationProbe />
      {extra}
    </>,
    { router: { initialEntries: [entry] } },
  );
}

function setVisibility(value: string) {
  Object.defineProperty(document, "visibilityState", {
    value,
    configurable: true,
  });
}

beforeEach(() => {
  resetConsoleStore();
  sessionStorage.clear();
  viewer.store = null;
  localStorage.setItem(USER_KEY, JSON.stringify(ADMIN));
  useAuthStore.setState({
    user: ADMIN,
    isAuthenticated: true,
    isLoggingOut: false,
    permissions: [],
  });
  guest = {
    id: "resource-01",
    cluster_id: "cluster01",
    vmid: 101,
    name: "linux01",
    status: "running",
  };
  vi.mocked(apiClient.get).mockImplementation((path: string) =>
    !path.includes("/vms/")
      ? Promise.resolve({})
      : guest === null
        ? Promise.reject(
            new ApiClientError(404, { error: "not_found", message: "gone" }),
          )
        : Promise.resolve(guest),
  );
  vi.mocked(apiClient.list).mockImplementation((path: string) =>
    Promise.resolve(
      path.endsWith("/nodes")
        ? [
            { id: "node-01", name: "pve-01" },
            { id: "node-02", name: "pve-02" },
          ]
        : [{ id: "cluster01", name: "cluster01" }],
    ),
  );
});

afterEach(() => {
  Reflect.deleteProperty(document, "visibilityState");
  localStorage.clear();
  document.title = "";
});

describe("ConsoleSessionPage", () => {
  it("runs the console a launcher opened on a store of its own", async () => {
    const { unmount } = renderAt(launched(VNC_LINK));

    const shown = await screen.findByTestId("viewer");
    expect(shown).toHaveAttribute("data-node", "pve-01");
    expect(shown).toHaveAttribute("data-resource", "resource-01");
    // The floating window's maximize has no window to size here.
    expect(screen.getByTestId("window-controls").textContent).toBe("false");
    // Named from the guest the server returned, not from the link.
    expect(shown).toHaveAttribute("data-label", "VNC: linux01");
    await waitFor(() => {
      expect(document.title).toBe("VNC: linux01 · Nexara");
    });
    expect(viewer.store).not.toBe(useConsoleStore);
    expect(useConsoleStore.getState().tabs).toEqual([]);
    // The launch is spent, and off the address a copy would carry.
    await waitFor(() => {
      expect(screen.getByTestId("location").textContent).not.toContain(
        "launch",
      );
    });

    // The guest's name is not left in the title for the login page.
    unmount();
    expect(document.title).toBe("Nexara");
  });

  it("keeps a console that is up whatever later answers say of its link", async () => {
    // The hub's catch-up after a restart refetches everything on screen, and a
    // guest can move to a node newer than the list read when it passed.
    const { queryClient } = renderAt(launched(VNC_LINK), <NodesProbe />);
    const shown = await screen.findByTestId("viewer");
    const nodes = { queryKey: ["clusters", "cluster01", "nodes"] };

    vi.mocked(apiClient.list).mockResolvedValue([
      { id: "node-02", name: "pve-02" },
    ]);
    await act(async () => {
      await queryClient.refetchQueries(nodes);
    });
    // Observers hear of it a macrotask later: wait until the page has too.
    await waitFor(() => {
      expect(screen.getByTestId("nodes").textContent).toBe("pve-02");
    });

    expect(screen.getByTestId("viewer")).toBe(shown);
  });

  it("writes a migrated guest's new node into its link, for a reload to dial", async () => {
    renderAt(launched(VNC_LINK));
    await screen.findByTestId("viewer");

    act(() => {
      viewer.store?.getState().updateTabNode("cluster01", 101, "pve-02");
    });

    await waitFor(() => {
      expect(screen.getByTestId("location").textContent).toContain(
        "node=pve-02",
      );
    });
  });

  it.each([
    [
      "an incomplete link",
      "/console/session?type=vm_vnc&node=pve-01&vmid=101",
      ADMIN,
      101,
      "This console link is incomplete",
    ],
    [
      "a console the user's role may not open",
      VNC_LINK,
      VIEWER,
      101,
      "You cannot open this console",
    ],
    // The header would show whatever the link said.
    [
      "a node its cluster does not have",
      VNC_LINK.replace("node=pve-01", "node=pve-09"),
      ADMIN,
      101,
      "This console link names no node of its cluster",
    ],
    // Its label, Power and Media would follow the named guest while the
    // screen showed the dialled one.
    [
      "a guest that is not the VMID it dials",
      VNC_LINK,
      ADMIN,
      102,
      "This console link does not match its guest",
    ],
  ])(
    "says so, and dials nothing, for %s",
    async (_, link, user, guestVmid, says) => {
      useAuthStore.setState({ user, permissions: [] });
      guest = { ...guest, vmid: guestVmid };

      renderAt(launched(link));

      expect(await screen.findByText(says)).toBeInTheDocument();
      expect(screen.queryByTestId("viewer")).toBeNull();
    },
  );

  it("says it could not check a link whose guest the server could not read", async () => {
    // Only a row that is gone or hidden (404, 403) is dropped.
    vi.mocked(apiClient.get).mockImplementation((path: string) =>
      path.includes("/vms/")
        ? Promise.reject(
            new ApiClientError(500, { error: "internal", message: "down" }),
          )
        : Promise.resolve({}),
    );

    renderAt(launched(VNC_LINK));

    expect(
      await screen.findByText("This console link could not be checked"),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("viewer")).toBeNull();
  });

  it("dials again after a reload, for the user this tab ran it for", async () => {
    sessionStorage.setItem(
      OWNER_KEY,
      JSON.stringify({ user: ADMIN.id, console: "cluster01|vm_vnc|101" }),
    );

    renderAt(VNC_LINK);

    expect(await screen.findByTestId("viewer")).toBeInTheDocument();
  });

  it("asks again after a reload when the guest row it ran is gone", async () => {
    // The VMID may name another guest by now.
    guest = null;
    sessionStorage.setItem(
      OWNER_KEY,
      JSON.stringify({ user: ADMIN.id, console: "cluster01|vm_vnc|101" }),
    );

    renderAt(VNC_LINK);

    expect(
      await screen.findByRole("button", { name: "Connect" }),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("viewer")).toBeNull();
  });

  it("closes the console as soon as this tab follows a sign-out", async () => {
    // Not when the revoke it sends answers, which a held request can delay.
    renderAt(launched(VNC_LINK));
    await screen.findByTestId("viewer");

    act(() => {
      useAuthStore.setState({ isLoggingOut: true });
    });

    expect(screen.getByText("Signed out in another tab")).toBeInTheDocument();
    expect(screen.queryByTestId("viewer")).toBeNull();
  });

  it("dials by VMID, without the guest's menus, when the row it names is gone", async () => {
    guest = null;

    renderAt(launched(VNC_LINK));

    const shown = await screen.findByTestId("viewer");
    expect(shown).toHaveAttribute("data-resource", "");
    expect(shown).toHaveAttribute("data-label", "VNC: 101");
  });

  it.each([
    // Pasted, bookmarked, from another site, printed by a guest in a
    // terminal, or brought back by the login page, which drops the hash.
    ["a link no launcher left", () => VNC_LINK],
    ["a launch left for someone else", () => launched(VNC_LINK, VIEWER.id)],
    [
      "a console this tab last ran for someone else",
      () => {
        sessionStorage.setItem(
          OWNER_KEY,
          JSON.stringify({ user: VIEWER.id, console: "cluster01|vm_vnc|101" }),
        );
        return launched(VNC_LINK);
      },
    ],
  ])("asks before it dials %s", async (_, link) => {
    renderAt(link());

    await userEvent.click(
      await screen.findByRole("button", { name: "Connect" }),
    );

    expect(await screen.findByTestId("viewer")).toBeInTheDocument();
    expect(JSON.parse(sessionStorage.getItem(OWNER_KEY) ?? "null")).toEqual({
      user: ADMIN.id,
      console: "cluster01|vm_vnc|101",
    });
  });

  it("waits to dial until the tab is first shown", async () => {
    // Opened behind the current tab, or restored with the browser.
    setVisibility("hidden");
    renderAt(launched(VNC_LINK));
    const shown = await screen.findByTestId("viewer");
    expect(shown).toHaveAttribute("data-visible", "false");

    act(() => {
      setVisibility("visible");
      document.dispatchEvent(new Event("visibilitychange"));
    });

    expect(shown).toHaveAttribute("data-visible", "true");
  });

  it("ends its session when another tab signs someone else in", async () => {
    const clearAuth = vi.fn();
    useAuthStore.setState({ clearAuth });
    renderAt(launched(VNC_LINK));
    await screen.findByTestId("viewer");

    act(() => {
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: USER_KEY,
          newValue: JSON.stringify(VIEWER),
          storageArea: localStorage,
        }),
      );
    });

    expect(clearAuth).toHaveBeenCalledWith({ byUser: true });
  });
});

// The console's components and hooks, as text — but for the floating
// console's own, which run on its store by definition.
const FLOATING_ONLY = ["FloatingConsole.tsx", "ConsoleTabBar.tsx"];
const CONSOLE_SOURCES: Record<string, string> = import.meta.glob(
  [
    "/src/features/console/{components,hooks}/*.{ts,tsx}",
    "!/src/features/console/**/*.test.{ts,tsx}",
  ],
  { eager: true, query: "?raw", import: "default" },
);

describe("the console viewers", () => {
  // A console tab runs them on its own store, through ConsoleStoreContext. One
  // that reached for useConsoleStore would read and write the floating
  // console's instead — status, reconnects, parking — from inside that tab.
  it("reach the console store only through its context", () => {
    const sources = Object.entries(CONSOLE_SOURCES).filter(
      ([path]) => !FLOATING_ONLY.some((name) => path.endsWith(`/${name}`)),
    );
    expect(sources.map(([path]) => path)).toEqual(
      expect.arrayContaining([
        "/src/features/console/components/VNCViewer.tsx",
        "/src/features/console/components/Terminal.tsx",
        "/src/features/console/hooks/useIdleTabOnUnmount.ts",
      ]),
    );
    for (const [path, source] of sources) {
      expect(source, path).not.toMatch(/\buseConsoleStore\b/);
    }
  });
});
