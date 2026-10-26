import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
// useGuestPowerSync → useVM needs a QueryClientProvider.
import { renderWithProviders } from "@/test/test-utils";
import { VNCViewer } from "./VNCViewer";
import { useConsoleStore } from "@/stores/console-store";
import type { ConsoleTab } from "../types/console";
import { vncTab } from "../console.fixtures";
import {
  MockWebSocket,
  installMockWebSocket,
  resetConsoleStore,
} from "../console.mocks";

// The VNC console always mints a scoped JWT before opening the WS (security
// review fix #1). The real minter's caching is covered in
// api/console-queries.test.ts.
const { mintSpy } = vi.hoisted(() => ({
  mintSpy: vi.fn(() => Promise.resolve("scoped-test-token")),
}));

// Spread the real module so buildVncWsUrl/wsAuthProtocols keep their real
// shape (the URL must stay token-free) and VNCToolbar's ISO hooks still exist.
vi.mock("../api/console-queries", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("../api/console-queries")>();
  return { ...actual, createConsoleTokenMinter: () => mintSpy };
});

// What the console does when its session has ended is ask the app to check its
// own (lib/session-ended.ts); these tests only need to see that it asks.
const { sessionEndedSpy } = vi.hoisted(() => ({ sessionEndedSpy: vi.fn() }));
vi.mock("../lib/session-ended", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/session-ended")>()),
  consoleSessionEnded: sessionEndedSpy,
}));

// The toolbar is chrome with its own server state; irrelevant here.
vi.mock("./VNCToolbar", () => ({ VNCToolbar: () => null }));

// Minimal noVNC stand-in. Records its listeners so a test can fire the
// "connect"/"disconnect" events the real RFB emits asynchronously. The class
// lives inside the factory because vi.mock is hoisted above the module body;
// only the instance list is shared out, via vi.hoisted.
interface RFBProbe {
  disconnect: ReturnType<typeof vi.fn>;
  emit: (type: string) => void;
}

const { rfbInstances } = vi.hoisted(() => ({ rfbInstances: [] as RFBProbe[] }));

vi.mock("@novnc/novnc", () => {
  class MockRFB implements RFBProbe {
    scaleViewport = false;
    resizeSession = true;
    focusOnClick = false;
    disconnect = vi.fn();
    private listeners: Record<string, (() => void)[]> = {};

    constructor(
      public target: HTMLElement,
      public socket: unknown,
      public options: Record<string, unknown>,
    ) {
      rfbInstances.push(this);
      // As noVNC does: it takes over the socket's onclose, and reports a close
      // as its disconnect event, at once and without the close code.
      (socket as { onclose: unknown }).onclose = () => {
        this.emit("disconnect");
      };
    }
    addEventListener(type: string, fn: () => void) {
      (this.listeners[type] ??= []).push(fn);
    }
    emit(type: string) {
      for (const fn of this.listeners[type] ?? []) fn();
    }
  }
  return { default: MockRFB };
});

const tab = vncTab();

function seedStore(status: ConsoleTab["status"] = "connected") {
  useConsoleStore.setState({
    tabs: [{ ...tab, status }],
    activeTabId: tab.id,
    windowMode: "floating",
  });
}

function tabStatus() {
  return useConsoleStore.getState().tabs[0]?.status;
}

beforeEach(() => {
  installMockWebSocket();
  rfbInstances.length = 0;
  mintSpy.mockClear();
  resetConsoleStore();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("VNCViewer", () => {
  it("mints a scoped token and opens the VNC socket", async () => {
    seedStore("connecting");
    renderWithProviders(<VNCViewer tab={tab} visible={true} />);

    await waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });
    expect(mintSpy).toHaveBeenCalledTimes(1);
    // Token rides in Sec-WebSocket-Protocol, never the URL (remediation 2.7).
    expect(MockWebSocket.instances[0]?.url).not.toContain("token=");
    expect(MockWebSocket.instances[0]?.protocols).toEqual([
      "nexara.token",
      "nexara.token.scoped-test-token",
    ]);
  });

  it("does not let a superseded connection disconnect the tab", async () => {
    // This is the reported bug. noVNC's disconnect event lands a beat after
    // the socket is torn down — measured at ~700ms against a real Proxmox
    // host, comfortably after the replacement connection is already up. The
    // teardown flag used to live in a component-lifetime ref that the next
    // effect run reset to false, so the outgoing generation reported itself
    // as a dropped connection: it stamped "disconnected" over a live socket
    // and scheduled a retry that bumped reconnectKey, re-ran the effect, and
    // looped forever.
    seedStore("connected");
    const { rerender } = renderWithProviders(
      <VNCViewer tab={tab} visible={true} />,
    );
    await waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });
    act(() => {
      MockWebSocket.instances[0]?.deliverConnected();
    });
    await waitFor(() => {
      expect(rfbInstances).toHaveLength(1);
    });
    const staleRfb = rfbInstances[0];
    expect(tabStatus()).toBe("connected");

    // Manual reconnect — the effect re-runs and dials again.
    rerender(<VNCViewer tab={{ ...tab, reconnectKey: 1 }} visible={true} />);
    await waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(2);
    });
    act(() => {
      MockWebSocket.instances[1]?.deliverConnected();
    });
    await waitFor(() => {
      expect(rfbInstances).toHaveLength(2);
    });
    act(() => {
      rfbInstances[1]?.emit("connect");
    });
    expect(tabStatus()).toBe("connected");

    // Only now does the first RFB's disconnect arrive.
    vi.useFakeTimers();
    act(() => {
      staleRfb?.emit("disconnect");
    });

    // It must neither mark the live tab dead...
    expect(tabStatus()).toBe("connected");
    // ...nor schedule a retry that would bump reconnectKey and restart the
    // whole cycle.
    act(() => {
      vi.advanceTimersByTime(30_000);
    });
    expect(useConsoleStore.getState().tabs[0]?.reconnectKey).toBe(0);
    expect(MockWebSocket.instances).toHaveLength(2);
  });

  it("still auto-reconnects when the live connection actually drops", async () => {
    // The guard above must not swallow real drops.
    seedStore("connected");
    renderWithProviders(<VNCViewer tab={tab} visible={true} />);
    await waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });
    act(() => {
      MockWebSocket.instances[0]?.deliverConnected();
    });
    await waitFor(() => {
      expect(rfbInstances).toHaveLength(1);
    });
    act(() => {
      rfbInstances[0]?.emit("connect");
    });

    act(() => {
      rfbInstances[0]?.emit("disconnect");
    });
    expect(tabStatus()).toBe("reconnecting");
  });

  // The server says so at open (an error with the code, then the Close) or
  // while open (the Close, which RFB's disconnect follows without its code).
  it.each([
    {
      name: "refuses it at open",
      connectedFirst: false,
      end: () => {
        MockWebSocket.instances[0]?.onmessage?.({
          data: JSON.stringify({ type: "error", code: "session_ended" }),
        } as MessageEvent);
        // Final at the error, before the Close that follows it.
        expect(tabStatus()).toBe("disconnected");
        MockWebSocket.instances[0]?.deliverClose(3000);
      },
    },
    {
      name: "closes it while open",
      connectedFirst: true,
      end: () => {
        MockWebSocket.instances[0]?.deliverClose(3000);
      },
    },
  ])(
    "stops for good when the server $name because its session has ended",
    async ({ connectedFirst, end }) => {
      sessionEndedSpy.mockClear();
      seedStore("connected");
      const { rerender } = renderWithProviders(
        <VNCViewer tab={tab} visible={true} />,
      );
      await waitFor(() => {
        expect(MockWebSocket.instances).toHaveLength(1);
      });
      if (connectedFirst) {
        act(() => {
          MockWebSocket.instances[0]?.deliverConnected();
        });
        await waitFor(() => {
          expect(rfbInstances).toHaveLength(1);
        });
      }

      vi.useFakeTimers();
      act(end);
      act(() => {
        vi.advanceTimersByTime(30_000);
      });

      expect(tabStatus()).toBe("disconnected");
      expect(useConsoleStore.getState().tabs[0]?.reconnectKey).toBe(0);
      expect(sessionEndedSpy).toHaveBeenCalledTimes(1);
      rerender(
        <VNCViewer tab={{ ...tab, status: "disconnected" }} visible={true} />,
      );
      expect(screen.getByText("Session ended")).toBeInTheDocument();
    },
  );

  it("ignores frames queued on a superseded socket", async () => {
    // close() is asynchronous, so a frame already in flight still dispatches.
    // Acting on it would build a second RFB into the same container, clobber
    // rfbRef, and leak the live Proxmox console session.
    seedStore("connecting");
    const { rerender } = renderWithProviders(
      <VNCViewer tab={tab} visible={true} />,
    );
    await waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });
    const staleWs = MockWebSocket.instances[0];

    rerender(<VNCViewer tab={{ ...tab, reconnectKey: 1 }} visible={true} />);
    await waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(2);
    });

    act(() => {
      staleWs?.deliverConnected();
    });
    expect(rfbInstances).toHaveLength(0);

    // The live socket is still fully able to bring a session up.
    act(() => {
      MockWebSocket.instances[1]?.deliverConnected();
    });
    await waitFor(() => {
      expect(rfbInstances).toHaveLength(1);
    });
  });

  it("leaves no live status behind when the console is torn down", async () => {
    // Nothing survives the unmount, so the tab must not still read
    // "connected" — it goes back to the pre-dial state a reload leaves it in.
    seedStore("connected");
    const { unmount } = renderWithProviders(
      <VNCViewer tab={tab} visible={true} />,
    );
    await waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });

    unmount();

    expect(tabStatus()).toBe("idle");
  });

  it("keeps a settled status when torn down", async () => {
    // "guest-stopped" is the parked powered-off console; it is still true
    // with no socket, so the reset must not clobber it back to idle (which
    // would make the next mount dial a guest known to be off).
    seedStore("guest-stopped");
    const { unmount } = renderWithProviders(
      <VNCViewer tab={{ ...tab, status: "guest-stopped" }} visible={true} />,
    );
    await Promise.resolve();
    unmount();

    expect(tabStatus()).toBe("guest-stopped");
  });
});
