import { describe, it, expect, beforeEach, vi, afterEach } from "vitest";
import { useWebSocketStore } from "./websocket-store";

// Mock WebSocket
class MockWebSocket {
  static readonly CONNECTING = 0 as const;
  static readonly OPEN = 1 as const;
  static readonly CLOSING = 2 as const;
  static readonly CLOSED = 3 as const;

  static instances: MockWebSocket[] = [];
  readyState: number = MockWebSocket.CONNECTING;
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  sentMessages: string[] = [];

  constructor(public url: string) {
    MockWebSocket.instances.push(this);
  }

  send(data: string) {
    this.sentMessages.push(data);
  }

  close() {
    this.readyState = MockWebSocket.CLOSED;
    this.onclose?.();
  }

  simulateOpen() {
    this.readyState = MockWebSocket.OPEN;
    this.onopen?.();
  }

  simulateMessage(data: unknown) {
    this.onmessage?.({ data: JSON.stringify(data) });
  }

  simulateClose() {
    this.readyState = MockWebSocket.CLOSED;
    this.onclose?.();
  }
}

function getLastInstance(): MockWebSocket {
  const ws = MockWebSocket.instances[0];
  if (!ws) throw new Error("No MockWebSocket instance found");
  return ws;
}

describe("websocket-store", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    MockWebSocket.instances = [];
    vi.stubGlobal("WebSocket", MockWebSocket);
    localStorage.setItem("access_token", "test-jwt-token");

    // Reset store state
    const store = useWebSocketStore.getState();
    store.disconnect();
    useWebSocketStore.setState({
      status: "disconnected",
      socket: null,
      listeners: new Map(),
      reconnectAttempts: 0,
      reconnectTimer: null,
      pingTimer: null,
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  it("starts in disconnected state", () => {
    expect(useWebSocketStore.getState().status).toBe("disconnected");
  });

  it("transitions to connecting then connected", () => {
    useWebSocketStore.getState().connect();
    expect(useWebSocketStore.getState().status).toBe("connecting");

    const ws = getLastInstance();
    ws.simulateOpen();
    expect(useWebSocketStore.getState().status).toBe("connected");
  });

  it("transitions to disconnected on disconnect()", () => {
    useWebSocketStore.getState().connect();
    const ws = getLastInstance();
    ws.simulateOpen();

    useWebSocketStore.getState().disconnect();
    expect(useWebSocketStore.getState().status).toBe("disconnected");
  });

  it("does not connect without a token", () => {
    localStorage.removeItem("access_token");
    useWebSocketStore.getState().connect();
    expect(MockWebSocket.instances).toHaveLength(0);
  });

  it("subscribe adds listener and sends subscribe message when connected", () => {
    useWebSocketStore.getState().connect();
    const ws = getLastInstance();
    ws.simulateOpen();

    const listener = vi.fn();
    useWebSocketStore.getState().subscribe("cluster:abc:metrics", listener);

    expect(useWebSocketStore.getState().listeners.has("cluster:abc:metrics")).toBe(true);
    expect(ws.sentMessages.some((m) => m.includes('"subscribe"'))).toBe(true);
  });

  it("unsubscribe removes listener and sends unsubscribe when last listener removed", () => {
    useWebSocketStore.getState().connect();
    const ws = getLastInstance();
    ws.simulateOpen();

    const listener = vi.fn();
    useWebSocketStore.getState().subscribe("cluster:abc:metrics", listener);
    useWebSocketStore.getState().unsubscribe("cluster:abc:metrics", listener);

    expect(useWebSocketStore.getState().listeners.has("cluster:abc:metrics")).toBe(false);
    expect(ws.sentMessages.some((m) => m.includes('"unsubscribe"'))).toBe(true);
  });

  it("dispatches data messages to registered listeners", () => {
    useWebSocketStore.getState().connect();
    const ws = getLastInstance();
    ws.simulateOpen();

    const listener = vi.fn();
    useWebSocketStore.getState().subscribe("cluster:abc:metrics", listener);

    ws.simulateMessage({
      type: "data",
      channel: "cluster:abc:metrics",
      payload: { test: true },
    });

    expect(listener).toHaveBeenCalledWith({ test: true });
  });

  it("does not dispatch to unrelated channel listeners", () => {
    useWebSocketStore.getState().connect();
    const ws = getLastInstance();
    ws.simulateOpen();

    const listener = vi.fn();
    useWebSocketStore.getState().subscribe("cluster:abc:metrics", listener);

    ws.simulateMessage({
      type: "data",
      channel: "cluster:xyz:metrics",
      payload: { other: true },
    });

    expect(listener).not.toHaveBeenCalled();
  });

  it("re-subscribes all channels on welcome message", () => {
    useWebSocketStore.getState().connect();
    const ws = getLastInstance();
    ws.simulateOpen();

    const listener = vi.fn();
    useWebSocketStore.getState().subscribe("cluster:abc:metrics", listener);
    ws.sentMessages = []; // Clear previous messages

    // Simulate welcome (reconnect scenario)
    ws.simulateMessage({ type: "welcome", message: "connected" });

    expect(ws.sentMessages.some((m) => m.includes("cluster:abc:metrics"))).toBe(true);
  });

  it("attempts reconnect with backoff on close", () => {
    useWebSocketStore.getState().connect();
    const ws = getLastInstance();
    ws.simulateOpen();

    ws.simulateClose();
    expect(useWebSocketStore.getState().status).toBe("reconnecting");
    expect(useWebSocketStore.getState().reconnectAttempts).toBe(1);

    // After backoff timer fires, it should attempt reconnect
    vi.advanceTimersByTime(1000);
    expect(MockWebSocket.instances).toHaveLength(2);
  });

  it("does not reconnect after explicit disconnect", () => {
    useWebSocketStore.getState().connect();
    const ws = getLastInstance();
    ws.simulateOpen();

    useWebSocketStore.getState().disconnect();
    expect(useWebSocketStore.getState().status).toBe("disconnected");

    vi.advanceTimersByTime(5000);
    // Should not have created a new WebSocket
    expect(MockWebSocket.instances).toHaveLength(1);
  });
});
