import { describe, it, expect, beforeEach, vi, afterEach } from "vitest";
import * as consoleQueries from "@/features/console/api/console-queries";
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

  constructor(public url: string, public protocols?: string | string[]) {
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

// flushPromises drains any pending microtasks. Needed because connect()
// awaits the mintWSHubToken promise before opening the WebSocket — tests
// must let the resolved mock-mint promise settle before asserting on the
// MockWebSocket instances array.
async function flushPromises(): Promise<void> {
  // Two ticks: one to resolve the mint, one to chain the .finally cleanup.
  await Promise.resolve();
  await Promise.resolve();
}

describe("websocket-store", () => {
  let mintSpy: import("vitest").MockInstance<typeof consoleQueries.mintWSHubToken>;

  beforeEach(() => {
    vi.useFakeTimers();
    MockWebSocket.instances = [];
    vi.stubGlobal("WebSocket", MockWebSocket);
    mintSpy = vi.spyOn(consoleQueries, "mintWSHubToken").mockResolvedValue({
      token: "test-jwt-token",
      expires_in: 60,
    });

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
      pendingConnect: null,
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    mintSpy.mockRestore();
  });

  it("starts in disconnected state", () => {
    expect(useWebSocketStore.getState().status).toBe("disconnected");
  });

  it("transitions to connecting then connected", async () => {
    useWebSocketStore.getState().connect();
    expect(useWebSocketStore.getState().status).toBe("connecting");

    await flushPromises();
    const ws = getLastInstance();
    ws.simulateOpen();
    expect(useWebSocketStore.getState().status).toBe("connected");
  });

  it("opens with subprotocol auth and never embeds token in URL", async () => {
    useWebSocketStore.getState().connect();
    await flushPromises();

    const ws = getLastInstance();
    expect(ws.url).toContain("/ws");
    expect(ws.url).not.toContain("token=");
    expect(ws.protocols).toEqual([
      "nexara.token",
      "nexara.token.test-jwt-token",
    ]);
  });

  it("transitions to disconnected on disconnect()", async () => {
    useWebSocketStore.getState().connect();
    await flushPromises();
    const ws = getLastInstance();
    ws.simulateOpen();

    useWebSocketStore.getState().disconnect();
    expect(useWebSocketStore.getState().status).toBe("disconnected");
  });

  it("does not connect when token mint fails", async () => {
    mintSpy.mockRejectedValueOnce(new Error("mint failed"));
    useWebSocketStore.getState().connect();
    await flushPromises();
    expect(MockWebSocket.instances).toHaveLength(0);
    // Failure path schedules a reconnect via the backoff timer.
    expect(useWebSocketStore.getState().status).toBe("reconnecting");
  });

  it("subscribe adds listener and sends subscribe message when connected", async () => {
    useWebSocketStore.getState().connect();
    await flushPromises();
    const ws = getLastInstance();
    ws.simulateOpen();

    const listener = vi.fn();
    useWebSocketStore.getState().subscribe("cluster:abc:metrics", listener);

    expect(useWebSocketStore.getState().listeners.has("cluster:abc:metrics")).toBe(true);
    expect(ws.sentMessages.some((m) => m.includes('"subscribe"'))).toBe(true);
  });

  it("unsubscribe removes listener and sends unsubscribe when last listener removed", async () => {
    useWebSocketStore.getState().connect();
    await flushPromises();
    const ws = getLastInstance();
    ws.simulateOpen();

    const listener = vi.fn();
    useWebSocketStore.getState().subscribe("cluster:abc:metrics", listener);
    useWebSocketStore.getState().unsubscribe("cluster:abc:metrics", listener);

    expect(useWebSocketStore.getState().listeners.has("cluster:abc:metrics")).toBe(false);
    expect(ws.sentMessages.some((m) => m.includes('"unsubscribe"'))).toBe(true);
  });

  it("dispatches data messages to registered listeners", async () => {
    useWebSocketStore.getState().connect();
    await flushPromises();
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

  it("does not dispatch to unrelated channel listeners", async () => {
    useWebSocketStore.getState().connect();
    await flushPromises();
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

  it("re-subscribes all channels on welcome message", async () => {
    useWebSocketStore.getState().connect();
    await flushPromises();
    const ws = getLastInstance();
    ws.simulateOpen();

    const listener = vi.fn();
    useWebSocketStore.getState().subscribe("cluster:abc:metrics", listener);
    ws.sentMessages = []; // Clear previous messages

    // Simulate welcome (reconnect scenario)
    ws.simulateMessage({ type: "welcome", message: "connected" });

    expect(ws.sentMessages.some((m) => m.includes("cluster:abc:metrics"))).toBe(true);
  });

  it("attempts reconnect with backoff on close", async () => {
    useWebSocketStore.getState().connect();
    await flushPromises();
    const ws = getLastInstance();
    ws.simulateOpen();

    ws.simulateClose();
    expect(useWebSocketStore.getState().status).toBe("reconnecting");
    expect(useWebSocketStore.getState().reconnectAttempts).toBe(1);

    // After backoff timer fires, it should attempt reconnect (mint a new
    // hub token + open another WS).
    await vi.advanceTimersByTimeAsync(1000);
    await flushPromises();
    expect(MockWebSocket.instances).toHaveLength(2);
  });

  it("does not reconnect after explicit disconnect", async () => {
    useWebSocketStore.getState().connect();
    await flushPromises();
    const ws = getLastInstance();
    ws.simulateOpen();

    useWebSocketStore.getState().disconnect();
    expect(useWebSocketStore.getState().status).toBe("disconnected");

    await vi.advanceTimersByTimeAsync(5000);
    // Should not have created a new WebSocket
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it("guards against double-connect while a mint is in flight", async () => {
    useWebSocketStore.getState().connect();
    // Second connect() before the mint resolves must be a no-op.
    useWebSocketStore.getState().connect();
    await flushPromises();
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(mintSpy).toHaveBeenCalledTimes(1);
  });

  it("connect→disconnect→connect during a slow mint opens only one socket", async () => {
    // The StrictMode double-mount race: attempt 1's mint is still in
    // flight when disconnect()+connect() install attempt 2. When attempt
    // 1's mint finally resolves it must notice it was superseded and bail
    // instead of opening a duplicate socket whose callbacks fight the
    // live connection's state.
    let resolveFirst: (v: { token: string; expires_in: number }) => void =
      () => undefined;
    mintSpy.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveFirst = resolve;
        }),
    );

    useWebSocketStore.getState().connect(); // attempt 1 — mint hangs
    useWebSocketStore.getState().disconnect();
    useWebSocketStore.getState().connect(); // attempt 2 — fast mock mint
    await flushPromises();

    expect(MockWebSocket.instances).toHaveLength(1);
    const live = getLastInstance();
    live.simulateOpen();
    expect(useWebSocketStore.getState().status).toBe("connected");

    // Attempt 1's stale mint resolves — no second socket, live state intact.
    resolveFirst({ token: "stale-token", expires_in: 60 });
    await flushPromises();
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(useWebSocketStore.getState().status).toBe("connected");
    expect(useWebSocketStore.getState().socket).not.toBeNull();
  });

  it("a socket the store no longer owns cannot run teardown on close", async () => {
    useWebSocketStore.getState().connect();
    await flushPromises();
    const first = getLastInstance();
    first.simulateOpen();
    expect(useWebSocketStore.getState().status).toBe("connected");

    // Server reaps the connection: legit teardown + scheduled reconnect.
    first.simulateClose();
    expect(useWebSocketStore.getState().status).toBe("reconnecting");

    await vi.advanceTimersByTimeAsync(1000);
    await flushPromises();
    const second = MockWebSocket.instances[1];
    if (!second) throw new Error("expected reconnect socket");
    second.simulateOpen();
    expect(useWebSocketStore.getState().status).toBe("connected");

    // A late close event from the dead first socket must not null the new
    // connection or schedule another reconnect.
    first.simulateClose();
    expect(useWebSocketStore.getState().status).toBe("connected");
    expect(useWebSocketStore.getState().socket).not.toBeNull();
  });
});
