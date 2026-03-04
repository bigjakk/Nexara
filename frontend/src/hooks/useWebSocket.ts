import { useWebSocketStore } from "@/stores/websocket-store";
import type { WsConnectionState } from "@/types/ws";

type ChannelListener = (payload: unknown) => void;

interface UseWebSocketReturn {
  status: WsConnectionState;
  subscribe: (channel: string, listener: ChannelListener) => void;
  unsubscribe: (channel: string, listener: ChannelListener) => void;
}

export function useWebSocket(): UseWebSocketReturn {
  const status = useWebSocketStore((s) => s.status);
  const subscribe = useWebSocketStore((s) => s.subscribe);
  const unsubscribe = useWebSocketStore((s) => s.unsubscribe);

  return { status, subscribe, unsubscribe };
}
