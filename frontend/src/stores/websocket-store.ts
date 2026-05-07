import { create } from "zustand";
import {
  mintWSHubToken,
  wsAuthProtocols,
} from "@/features/console/api/console-queries";
import type {
  WsConnectionState,
  WsIncomingMessage,
  WsOutgoingMessage,
} from "@/types/ws";

type ChannelListener = (payload: unknown) => void;

interface WebSocketState {
  status: WsConnectionState;
  socket: WebSocket | null;
  listeners: Map<string, Set<ChannelListener>>;
  reconnectAttempts: number;
  reconnectTimer: ReturnType<typeof setTimeout> | null;
  pingTimer: ReturnType<typeof setInterval> | null;
  // Tracks the in-flight token mint so reconnect attempts that arrive
  // before the previous mint settles don't open a second WebSocket.
  pendingConnect: Promise<void> | null;
}

interface WebSocketActions {
  connect: () => void;
  disconnect: () => void;
  subscribe: (channel: string, listener: ChannelListener) => void;
  unsubscribe: (channel: string, listener: ChannelListener) => void;
  send: (msg: WsOutgoingMessage) => void;
}

const MAX_RECONNECT_DELAY_MS = 30_000;
const PING_INTERVAL_MS = 25_000;

function getReconnectDelay(attempt: number): number {
  return Math.min(1000 * Math.pow(2, attempt), MAX_RECONNECT_DELAY_MS);
}

function buildHubWsUrl(): string {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const host = window.location.host;
  // No `?token=` — the JWT rides in `Sec-WebSocket-Protocol` instead.
  return `${protocol}//${host}/ws`;
}

export const useWebSocketStore = create<WebSocketState & WebSocketActions>()(
  (set, get) => ({
    status: "disconnected",
    socket: null,
    listeners: new Map(),
    reconnectAttempts: 0,
    reconnectTimer: null,
    pingTimer: null,
    pendingConnect: null,

    connect: () => {
      const state = get();
      if (state.socket || state.pendingConnect) return;

      set({ status: "connecting" });

      // Mint a hub token, then open the WS with it as a subprotocol.
      // Anything between mint and open is racy with disconnect()/reconnect,
      // so we guard with `pendingConnect` (cleared only when THIS specific
      // promise settles, never when a later connect() has installed a
      // newer one) and re-check status before AND after creating the socket.
      const pending: Promise<void> = (async () => {
        let token: string;
        try {
          const minted = await mintWSHubToken();
          token = minted.token;
        } catch {
          // Mint failed (network, 401, etc.). Drop to reconnecting so the
          // backoff kicks in; if auth is broken the apiClient's refresh
          // path will surface the failure first.
          const s = get();
          if (s.status !== "disconnected") {
            const attempts = s.reconnectAttempts;
            const delay = getReconnectDelay(attempts);
            set({ status: "reconnecting", reconnectAttempts: attempts + 1 });
            const timer = setTimeout(() => {
              set({ reconnectTimer: null });
              get().connect();
            }, delay);
            set({ reconnectTimer: timer });
          }
          return;
        }

        // The user may have called disconnect() while the mint was in
        // flight. Don't open a socket if they did.
        if (get().status === "disconnected") return;

        const ws = new WebSocket(buildHubWsUrl(), wsAuthProtocols(token));

        ws.onopen = () => {
          // disconnect() may have fired between `new WebSocket()` and the
          // open event. If so, close immediately and skip the bookkeeping.
          if (get().status === "disconnected") {
            ws.close();
            return;
          }
          set({ status: "connected", reconnectAttempts: 0 });

          // Start application-level ping
          const pingTimer = setInterval(() => {
            const cur = get();
            if (cur.socket?.readyState === WebSocket.OPEN) {
              cur.send({ type: "ping" });
            }
          }, PING_INTERVAL_MS);
          set({ pingTimer });
        };

        ws.onmessage = (event: MessageEvent) => {
          let msg: WsIncomingMessage;
          try {
            msg = JSON.parse(String(event.data)) as WsIncomingMessage;
          } catch {
            return;
          }

          if (msg.type === "welcome") {
            // Re-subscribe all channels on (re)connect
            const { listeners } = get();
            const channels = Array.from(listeners.keys());
            if (channels.length > 0) {
              get().send({ type: "subscribe", channels });
            }
            return;
          }

          if (msg.type === "data" && msg.channel) {
            const { listeners } = get();
            const channelListeners = listeners.get(msg.channel);
            if (channelListeners) {
              for (const listener of channelListeners) {
                listener(msg.payload);
              }
            }
          }
        };

        ws.onclose = () => {
          const cur = get();
          if (cur.pingTimer) {
            clearInterval(cur.pingTimer);
          }
          set({ socket: null, pingTimer: null });

          // Only reconnect if we weren't intentionally disconnecting
          if (cur.status !== "disconnected") {
            const attempts = cur.reconnectAttempts;
            const delay = getReconnectDelay(attempts);
            set({ status: "reconnecting", reconnectAttempts: attempts + 1 });
            const timer = setTimeout(() => {
              set({ reconnectTimer: null });
              get().connect();
            }, delay);
            set({ reconnectTimer: timer });
          }
        };

        ws.onerror = () => {
          // onclose will fire after onerror, so reconnect is handled there
        };

        // disconnect() between `new WebSocket()` and here would have only
        // seen socket: null; the freshly-created `ws` would orphan with no
        // close path. Re-check and clean up if status flipped underneath.
        if (get().status === "disconnected") {
          ws.close();
          return;
        }
        set({ socket: ws });
      })().finally(() => {
        // Only clear if THIS specific pending is still the in-flight one.
        // disconnect() + connect() may have installed a newer pending while
        // the previous mint was still resolving; clobbering it would let a
        // third concurrent connect() slip through the guard.
        if (get().pendingConnect === pending) {
          set({ pendingConnect: null });
        }
      });

      set({ pendingConnect: pending });
    },

    disconnect: () => {
      const state = get();
      // Set status first so onclose doesn't trigger reconnect
      set({ status: "disconnected" });

      if (state.pingTimer) {
        clearInterval(state.pingTimer);
      }
      if (state.reconnectTimer) {
        clearTimeout(state.reconnectTimer);
      }
      if (state.socket) {
        state.socket.close();
      }
      set({
        socket: null,
        pingTimer: null,
        reconnectTimer: null,
        reconnectAttempts: 0,
        // Clear the in-flight mint marker so a subsequent connect() isn't
        // blocked. The mint promise itself can still settle in the
        // background — its inner code re-checks `status === "disconnected"`
        // before opening the socket, so it'll bail out harmlessly.
        pendingConnect: null,
      });
    },

    subscribe: (channel: string, listener: ChannelListener) => {
      const { listeners, socket } = get();
      const existing = listeners.get(channel);
      const isNewChannel = !existing || existing.size === 0;

      const updated = new Map(listeners);
      const channelSet = new Set(existing);
      channelSet.add(listener);
      updated.set(channel, channelSet);
      set({ listeners: updated });

      // Send subscribe message if this is a new channel
      if (isNewChannel && socket?.readyState === WebSocket.OPEN) {
        get().send({ type: "subscribe", channels: [channel] });
      }
    },

    unsubscribe: (channel: string, listener: ChannelListener) => {
      const { listeners, socket } = get();
      const existing = listeners.get(channel);
      if (!existing) return;

      const channelSet = new Set(existing);
      channelSet.delete(listener);

      const updated = new Map(listeners);
      if (channelSet.size === 0) {
        updated.delete(channel);
        // Unsubscribe from server when no listeners remain
        if (socket?.readyState === WebSocket.OPEN) {
          get().send({ type: "unsubscribe", channels: [channel] });
        }
      } else {
        updated.set(channel, channelSet);
      }
      set({ listeners: updated });
    },

    send: (msg: WsOutgoingMessage) => {
      const { socket } = get();
      if (socket?.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify(msg));
      }
    },
  }),
);
