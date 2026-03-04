import { describe, it, expect, vi, beforeEach } from "vitest";
import { render } from "@testing-library/react";
import { Terminal } from "./Terminal";
import type { ConsoleTab } from "../types/console";

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

  constructor(_url: string) {
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
};

describe("Terminal", () => {
  it("renders a terminal container", () => {
    const { container } = render(<Terminal tab={testTab} visible={true} />);
    expect(container.querySelector("div")).toBeTruthy();
  });

  it("creates a WebSocket connection with correct URL", () => {
    render(<Terminal tab={testTab} visible={true} />);
    expect(MockWebSocket.instances).toHaveLength(1);
    const ws = MockWebSocket.instances[0];
    expect(ws).toBeDefined();
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
