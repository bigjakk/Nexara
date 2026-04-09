/**
 * WebSocket store for the mobile app. Mirrors the web frontend's
 * `frontend/src/stores/websocket-store.ts` so the same channel + message
 * conventions apply on both clients.
 *
 * Differences from web:
 *  - Reads server URL from secure storage instead of `window.location`
 *  - Reads access token from secure storage instead of `localStorage`
 *  - Uses React Native's global WebSocket implementation
 *  - Token can change at runtime (refresh) — we reconnect with the new
 *    token via `reconnectWithFreshToken()` exposed for the API client
 */

import { create } from "zustand";

import { ensureFreshAccessToken } from "@/features/api/api-client";
import { secureStorage } from "@/lib/secure-storage";
import type {
  WsConnectionState,
  WsIncomingMessage,
  WsOutgoingMessage,
} from "@/features/api/ws-types";

type ChannelListener = (payload: unknown) => void;

interface WebSocketState {
  status: WsConnectionState;
  socket: WebSocket | null;
  listeners: Map<string, Set<ChannelListener>>;
  reconnectAttempts: number;
  reconnectTimer: ReturnType<typeof setTimeout> | null;
  pingTimer: ReturnType<typeof setInterval> | null;
}

interface WebSocketActions {
  connect: () => Promise<void>;
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

async function buildWsUrl(token: string): Promise<string | null> {
  const serverUrl = await secureStorage.getServerUrl();
  if (!serverUrl) return null;

  // Convert http(s)://host[:port] -> ws(s)://host[:port]/ws?token=...
  const wsBase = serverUrl.replace(/^http/, "ws");
  return `${wsBase}/ws?token=${encodeURIComponent(token)}`;
}

export const useWsStore = create<WebSocketState & WebSocketActions>(
  (set, get) => ({
    status: "disconnected",
    socket: null,
    listeners: new Map(),
    reconnectAttempts: 0,
    reconnectTimer: null,
    pingTimer: null,

    connect: async () => {
      const state = get();
      if (state.socket) return;

      // Proactively refresh the access token before every connect attempt.
      // Without this the reconnect loop would re-read a stale token from
      // SecureStore forever once the JWT expired (default 15 min), which
      // silently broke the event stream for anyone who kept the app open
      // more than a few hours. On refresh failure the helper emits
      // auth-lost, which bounces the user to login via the listener in
      // app/_layout.tsx — that listener also calls wsDisconnect, so we
      // just bail out here and let the teardown happen.
      let token: string | null;
      try {
        token = await ensureFreshAccessToken();
      } catch {
        return;
      }
      if (!token) return;

      const url = await buildWsUrl(token);
      if (!url) return;

      set({ status: "connecting" });

      const ws = new WebSocket(url);

      ws.onopen = () => {
        set({ status: "connected", reconnectAttempts: 0 });

        const pingTimer = setInterval(() => {
          const s = get();
          if (s.socket?.readyState === WebSocket.OPEN) {
            s.send({ type: "ping" });
          }
        }, PING_INTERVAL_MS);
        set({ pingTimer });
      };

      ws.onmessage = (event: WebSocketMessageEvent) => {
        let msg: WsIncomingMessage;
        try {
          msg = JSON.parse(String(event.data)) as WsIncomingMessage;
        } catch {
          return;
        }

        if (msg.type === "welcome") {
          // Re-subscribe everything on (re)connect.
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
        const s = get();
        if (s.pingTimer) clearInterval(s.pingTimer);
        set({ socket: null, pingTimer: null });

        if (s.status !== "disconnected") {
          const attempts = s.reconnectAttempts;
          const delay = getReconnectDelay(attempts);
          set({
            status: "reconnecting",
            reconnectAttempts: attempts + 1,
          });
          const timer = setTimeout(() => {
            set({ reconnectTimer: null });
            void get().connect();
          }, delay);
          set({ reconnectTimer: timer });
        }
      };

      ws.onerror = () => {
        // onclose fires after onerror; reconnect handled there.
      };

      set({ socket: ws });
    },

    disconnect: () => {
      const state = get();
      set({ status: "disconnected" });

      if (state.pingTimer) clearInterval(state.pingTimer);
      if (state.reconnectTimer) clearTimeout(state.reconnectTimer);
      if (state.socket) state.socket.close();

      set({
        socket: null,
        pingTimer: null,
        reconnectTimer: null,
        reconnectAttempts: 0,
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
