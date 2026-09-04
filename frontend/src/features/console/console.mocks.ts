import { vi } from "vitest";
import { useConsoleStore } from "@/stores/console-store";

/**
 * Mock installers for the console tests. Split from `console.fixtures.ts`
 * because these reach into vitest and the store, and the tab fixtures must not.
 *
 * What is NOT here, and cannot be: the `vi.mock` factories and their
 * `vi.hoisted` spies. Those are hoisted above the module body, so an imported
 * binding is not yet initialised when they run; each test file declares its own.
 */

/**
 * A stand-in WebSocket that records every socket the code under test opens.
 *
 * `readyState` starts CONNECTING, which is what a fresh socket really reports.
 * Neither console suite depends on the initial value — the only production
 * reads sit in callbacks no test fires — so it is set to the honest one rather
 * than to whatever a given test found convenient.
 */
export class MockWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: MockWebSocket[] = [];

  readyState: number = MockWebSocket.CONNECTING;
  binaryType = "blob";
  onopen: (() => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  // Optional parameters so a test can fire these bare — `ws.onclose?.()` — as
  // well as with an event, which is how the close-race tests drive them.
  onclose: ((e?: Partial<CloseEvent>) => void) | null = null;
  onerror: ((e?: Partial<Event>) => void) | null = null;

  constructor(
    public url: string,
    public protocols?: string | string[],
  ) {
    MockWebSocket.instances.push(this);
  }

  send = vi.fn();
  close = vi.fn();

  /** Deliver the backend's "proxy is connected" control frame. */
  deliverConnected() {
    this.onmessage?.({
      data: JSON.stringify({ type: "connected" }),
    } as MessageEvent);
  }
}

/** Install the socket stand-in and forget any sockets a prior test opened. */
export function installMockWebSocket() {
  Object.assign(globalThis, { WebSocket: MockWebSocket });
  MockWebSocket.instances = [];
}

/**
 * No tabs, nothing active, console closed. Deliberately leaves windowPosition
 * and windowSize alone — nothing here reads them, and resetting geometry a test
 * did not set would make this look like a full store reset, which it is not.
 */
export function resetConsoleStore() {
  useConsoleStore.setState({
    tabs: [],
    activeTabId: null,
    windowMode: "hidden",
  });
}
