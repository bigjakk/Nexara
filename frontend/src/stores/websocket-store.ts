import { create } from "zustand";
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

function buildWsUrl(): string {
  const token = localStorage.getItem("access_token");
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const host = window.location.host;
  return `${protocol}//${host}/ws?token=${encodeURIComponent(token ?? "")}`;
}

export const useWebSocketStore = create<WebSocketState & WebSocketActions>()(
  (set, get) => ({
    status: "disconnected",
    socket: null,
    listeners: new Map(),
    reconnectAttempts: 0,
    reconnectTimer: null,
    pingTimer: null,

    connect: () => {
      const state = get();
      if (state.socket) return;

      const token = localStorage.getItem("access_token");
      if (!token) return;

      set({ status: "connecting" });

      const ws = new WebSocket(buildWsUrl());

      ws.onopen = () => {
        set({ status: "connected", reconnectAttempts: 0 });

        // Start application-level ping
        const pingTimer = setInterval(() => {
          const s = get();
          if (s.socket?.readyState === WebSocket.OPEN) {
            s.send({ type: "ping" });
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
        const s = get();
        if (s.pingTimer) {
          clearInterval(s.pingTimer);
        }
        set({ socket: null, pingTimer: null });

        // Only reconnect if we weren't intentionally disconnecting
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
      };

      ws.onerror = () => {
        // onclose will fire after onerror, so reconnect is handled there
      };

      set({ socket: ws });
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
