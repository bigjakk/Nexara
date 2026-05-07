import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { Terminal } from "./Terminal";
import type { ConsoleTab } from "../types/console";

// Mock the console-token mint endpoint. The Terminal now mints a scoped JWT
// before opening the WS (security review fix #1) — desktop callers never
// pass accessToken; mobile passes its pre-minted token directly.
vi.mock("../api/console-queries", () => ({
  mintConsoleToken: vi.fn(() =>
    Promise.resolve({ token: "scoped-test-token", expires_in: 60 }),
  ),
  wsAuthProtocols: (token: string) => [
    "nexara.token",
    "nexara.token." + token,
  ],
}));

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

vi.mock("@xterm/addon-fit", () => {
  class MockFitAddon {
    fit = vi.fn();
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

// Mock ResizeObserver
class MockResizeObserver {
  observe = vi.fn();
  unobserve = vi.fn();
  disconnect = vi.fn();
}
Object.assign(globalThis, { ResizeObserver: MockResizeObserver });

// Mock WebSocket
class MockWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: MockWebSocket[] = [];
  readyState = MockWebSocket.CONNECTING;
  binaryType = "blob";
  onopen: (() => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;

  url: string;
  protocols: string | string[] | undefined;
  constructor(url: string, protocols?: string | string[]) {
    this.url = url;
    this.protocols = protocols;
    MockWebSocket.instances.push(this);
  }
  send = vi.fn();
  close = vi.fn();
}

Object.assign(globalThis, { WebSocket: MockWebSocket });

beforeEach(() => {
  MockWebSocket.instances = [];
  vi.spyOn(Storage.prototype, "getItem").mockReturnValue("test-token");
});

const testTab: ConsoleTab = {
  id: "test-tab-1",
  clusterID: "cluster-1",
  node: "node1",
  type: "node_shell",
  label: "node1 shell",
  status: "connecting",
  reconnectKey: 0,
};

describe("Terminal", () => {
  it("renders a terminal container", () => {
    const { container } = render(<Terminal tab={testTab} visible={true} />);
    expect(container.querySelector("div")).toBeTruthy();
  });

  it("creates a WebSocket connection after minting a scoped token", async () => {
    render(<Terminal tab={testTab} visible={true} />);
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

  it("uses the override accessToken instead of minting when provided (mobile path)", async () => {
    render(
      <Terminal
        tab={testTab}
        visible={true}
        accessToken="mobile-prebaked-token"
      />,
    );
    await waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });
    const ws = MockWebSocket.instances[0];
    expect(ws?.url).not.toContain("token=");
    expect(ws?.protocols).toEqual([
      "nexara.token",
      "nexara.token.mobile-prebaked-token",
    ]);
  });

  it("hides terminal when not visible", () => {
    const { container } = render(<Terminal tab={testTab} visible={false} />);
    const div = container.firstChild as HTMLElement;
    expect(div.style.display).toBe("none");
  });

  it("shows terminal when visible", () => {
    const { container } = render(<Terminal tab={testTab} visible={true} />);
    const div = container.firstChild as HTMLElement;
    expect(div.style.display).toBe("block");
  });
});
