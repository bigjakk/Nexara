import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, waitFor } from "@testing-library/react";
// useGuestPowerSync → useVM needs a QueryClientProvider.
import { renderWithProviders } from "@/test/test-utils";
import { Terminal } from "./Terminal";
import { useConsoleStore } from "@/stores/console-store";
import { shellTab } from "../console.fixtures";
import {
  MockWebSocket,
  installMockWebSocket,
  resetConsoleStore,
} from "../console.mocks";

// Mock the console-token minter. The Terminal always mints a scoped JWT before
// opening the WS (security review fix #1). The real minter caches within the
// token's TTL; its own behaviour is covered in api/console-queries.test.ts.
const { mintSpy } = vi.hoisted(() => ({
  mintSpy: vi.fn(() => Promise.resolve("scoped-test-token")),
}));

// Spread the real module so wsAuthProtocols keeps its real shape. Stubbing it
// here instead would leave the protocol assertion below testing this file's
// own copy of the answer.
vi.mock("../api/console-queries", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("../api/console-queries")>();
  return { ...actual, createConsoleTokenMinter: () => mintSpy };
});

// Mock xterm.js with class implementations
vi.mock("@xterm/xterm", () => {
  class MockTerminal {
    loadAddon = vi.fn();
    open = vi.fn();
    write = vi.fn();
    writeln = vi.fn();
    onData = vi.fn().mockReturnValue({ dispose: vi.fn() });
    onResize = vi.fn().mockReturnValue({ dispose: vi.fn() });
    dispose = vi.fn();
    cols = 80;
    rows = 24;
  }
  return { Terminal: MockTerminal };
});

const { fitSpy } = vi.hoisted(() => ({ fitSpy: vi.fn() }));

vi.mock("@xterm/addon-fit", () => {
  class MockFitAddon {
    fit = fitSpy;
    dispose = vi.fn();
  }
  return { FitAddon: MockFitAddon };
});

vi.mock("@xterm/addon-web-links", () => {
  class MockWebLinksAddon {
    dispose = vi.fn();
  }
  return { WebLinksAddon: MockWebLinksAddon };
});

// Mock ResizeObserver, keeping the callback so a test can fire it.
let lastResizeCallback: (() => void) | null = null;
class MockResizeObserver {
  constructor(cb: () => void) {
    lastResizeCallback = cb;
  }
  observe = vi.fn();
  unobserve = vi.fn();
  disconnect = vi.fn();
}
Object.assign(globalThis, { ResizeObserver: MockResizeObserver });

beforeEach(() => {
  installMockWebSocket();
  lastResizeCallback = null;
  fitSpy.mockClear();
  mintSpy.mockClear();
  vi.spyOn(Storage.prototype, "getItem").mockReturnValue("test-token");
  resetConsoleStore();
});

afterEach(() => {
  vi.useRealTimers();
});

const tab = shellTab();

describe("Terminal", () => {
  it("renders a terminal container", () => {
    const { container } = renderWithProviders(
      <Terminal tab={tab} visible={true} />,
    );
    expect(container.querySelector("div")).toBeTruthy();
  });

  it("creates a WebSocket connection after minting a scoped token", async () => {
    renderWithProviders(<Terminal tab={tab} visible={true} />);
    // Mint resolves on the microtask queue; wait for the WS to be created.
    await waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });
    const ws = MockWebSocket.instances[0];
    expect(ws).toBeDefined();
    // Token rides in Sec-WebSocket-Protocol, NOT in the URL (remediation 2.7).
    expect(ws?.url).not.toContain("token=");
    expect(ws?.url).toContain("cluster_id=cluster-1");
    expect(ws?.url).toContain("type=node_shell");
    expect(ws?.protocols).toEqual([
      "nexara.token",
      "nexara.token.scoped-test-token",
    ]);
  });

  it("does not dial a background tab until it is first shown", async () => {
    // Restored tabs all mount at once on login. Only the active one may
    // connect — otherwise every persisted session mints an audited token and
    // grabs a Proxmox console slot nobody is looking at.
    const { rerender } = renderWithProviders(
      <Terminal tab={tab} visible={false} />,
    );
    await Promise.resolve();
    expect(mintSpy).not.toHaveBeenCalled();
    expect(MockWebSocket.instances).toHaveLength(0);

    rerender(<Terminal tab={tab} visible={true} />);
    await waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });
    expect(mintSpy).toHaveBeenCalledTimes(1);
  });

  it("connects a background tab that was explicitly reconnected", async () => {
    // reconnectTab bumps reconnectKey from the tab bar without switching
    // tabs; without this the tab would spin on "connecting" forever.
    renderWithProviders(
      <Terminal tab={{ ...tab, reconnectKey: 1 }} visible={false} />,
    );
    await waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });
  });

  it("keeps a live session open when switched away from", async () => {
    const { rerender } = renderWithProviders(
      <Terminal tab={tab} visible={true} />,
    );
    await waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });
    const ws = MockWebSocket.instances[0];

    rerender(<Terminal tab={tab} visible={false} />);
    await Promise.resolve();

    expect(ws?.close).not.toHaveBeenCalled();
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it("does not let a superseded connection schedule its own reconnect", async () => {
    // The teardown flag used to live in a ref for the whole component. The
    // cleanup set it true and the very next effect run set it false again in
    // the same commit, so when the previous run's socket finally closed a
    // beat later its onclose read the flag as false, mistook its own
    // teardown for a dropped connection, and scheduled a retry. That retry bumped
    // reconnectKey, which re-ran the effect, which produced another ghost —
    // one click of Reconnect spun forever, opening a fresh Proxmox console
    // every few seconds.
    useConsoleStore.setState({
      tabs: [{ ...tab, status: "connected" }],
      activeTabId: tab.id,
      windowMode: "floating",
    });

    const { rerender } = renderWithProviders(
      <Terminal tab={tab} visible={true} />,
    );
    await waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });
    const stale = MockWebSocket.instances[0];

    // Manual reconnect: the effect re-runs, closing the first socket and
    // opening a second.
    rerender(<Terminal tab={{ ...tab, reconnectKey: 1 }} visible={true} />);
    await waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(2);
    });
    expect(stale?.close).toHaveBeenCalled();

    // Now the first socket's close event lands, after its replacement is up.
    vi.useFakeTimers();
    act(() => {
      stale?.onclose?.();
    });

    const afterGhost = useConsoleStore.getState().tabs[0];
    expect(afterGhost?.status).not.toBe("reconnecting");

    // Nothing was scheduled, so nothing bumps reconnectKey and re-runs the
    // effect. With the bug this reached 1 and the cycle restarted.
    //
    // Load-bearing fixture detail: tab is a node_shell with no
    // resourceId/kind/vmid, so resolveAndReconnect skips both awaits and
    // bumps reconnectKey synchronously inside the timer callback. Give
    // tab a resourceId and this assertion goes quiet for the wrong
    // reason.
    act(() => {
      vi.advanceTimersByTime(30_000);
    });
    expect(useConsoleStore.getState().tabs[0]?.reconnectKey).toBe(0);
  });

  it("still auto-reconnects when a live connection actually drops", async () => {
    // The guard above must not swallow real drops: the socket belonging to
    // the current effect run is entitled to schedule a retry.
    useConsoleStore.setState({
      tabs: [{ ...tab, status: "connected" }],
      activeTabId: tab.id,
      windowMode: "floating",
    });

    renderWithProviders(<Terminal tab={tab} visible={true} />);
    await waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });

    act(() => {
      MockWebSocket.instances[0]?.onclose?.();
    });

    expect(useConsoleStore.getState().tabs[0]?.status).toBe("reconnecting");
  });

  it("leaves no live status behind when the console is torn down", async () => {
    // Nothing survives the unmount, so the tab must not still read
    // "connected" — it goes back to the pre-dial state a reload leaves it in.
    useConsoleStore.setState({
      tabs: [{ ...tab, status: "connected" }],
      activeTabId: tab.id,
      windowMode: "floating",
    });

    const { unmount } = renderWithProviders(
      <Terminal tab={tab} visible={true} />,
    );
    await waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });

    unmount();

    expect(useConsoleStore.getState().tabs[0]?.status).toBe("idle");
  });

  it("does not reflow the guest's pty to the minimized thumbnail", async () => {
    // fit() is not a local-only operation: it fires term.onResize, which
    // sends a resize frame and SIGWINCHes whatever is running on the guest.
    // The PiP is 320x200 — roughly 38x11 — so fitting to it would redraw a
    // live vim/htop into a fraction of its columns and leave it mangled on
    // restore. This only became reachable once minimize stopped destroying
    // the terminal and dialling a fresh shell.
    useConsoleStore.setState({
      tabs: [{ ...tab, status: "connected" }],
      activeTabId: tab.id,
      windowMode: "floating",
    });
    renderWithProviders(<Terminal tab={tab} visible={true} />);
    await waitFor(() => {
      expect(lastResizeCallback).not.toBeNull();
    });

    act(() => {
      useConsoleStore.getState().setWindowMode("minimized");
    });
    fitSpy.mockClear();

    act(() => {
      lastResizeCallback?.();
    });
    // The callback defers to requestAnimationFrame; give it a frame.
    await new Promise((r) =>
      requestAnimationFrame(() => {
        r(null);
      }),
    );
    expect(fitSpy).not.toHaveBeenCalled();
  });

  it("re-fits once the window is restored", async () => {
    // The converse of the test above: a real size change still has to reach
    // the terminal, or the console comes back from the PiP mis-sized.
    useConsoleStore.setState({
      tabs: [{ ...tab, status: "connected" }],
      activeTabId: tab.id,
      windowMode: "minimized",
    });
    renderWithProviders(<Terminal tab={tab} visible={true} />);
    await waitFor(() => {
      expect(lastResizeCallback).not.toBeNull();
    });
    fitSpy.mockClear();

    act(() => {
      useConsoleStore.getState().setWindowMode("floating");
    });
    act(() => {
      lastResizeCallback?.();
    });
    await new Promise((r) =>
      requestAnimationFrame(() => {
        r(null);
      }),
    );
    expect(fitSpy).toHaveBeenCalled();
  });

  it("hides terminal when not visible", () => {
    const { container } = renderWithProviders(
      <Terminal tab={tab} visible={false} />,
    );
    const div = container.firstChild as HTMLElement;
    expect(div.style.display).toBe("none");
  });

  it("shows terminal when visible", () => {
    const { container } = renderWithProviders(
      <Terminal tab={tab} visible={true} />,
    );
    const div = container.firstChild as HTMLElement;
    expect(div.style.display).toBe("block");
  });
});
