import { useEffect } from "react";
import { useEventInvalidation } from "@/hooks/useEventInvalidation";
import { useWebSocketStore } from "@/stores/websocket-store";

/**
 * The event hub's socket, open while the calling page is mounted, and the cache
 * invalidation its events drive for `clusterIds` (keep the array's identity
 * stable: a new one resubscribes). AppShell runs it for the whole app; a
 * console in a browser tab of its own runs it for its cluster, so a guest's
 * power-on reaches its parked console within seconds rather than a poll.
 */
export function useLiveUpdates(clusterIds: string[]): void {
  const connect = useWebSocketStore((s) => s.connect);
  const disconnect = useWebSocketStore((s) => s.disconnect);

  useEffect(() => {
    connect();
    return () => {
      disconnect();
    };
  }, [connect, disconnect]);

  useEventInvalidation(clusterIds);
}
