import { useMutation } from "@tanstack/react-query";

import { apiPost } from "@/features/api/api-client";
import type {
  ConsoleTokenRequest,
  ConsoleTokenResponse,
} from "@/features/api/types";

/**
 * Mint a short-lived (5 minute) scope-locked JWT for opening a single
 * console WebSocket. The token returned here can ONLY be used at the
 * matching `/ws/console` or `/ws/vnc` endpoint with exactly the
 * cluster_id/node/vmid/type the request specified.
 *
 * Implemented as a mutation rather than a query because each call mints
 * a fresh token and we don't want it cached or auto-refetched.
 */
export function useMintConsoleToken() {
  return useMutation({
    mutationFn: (req: ConsoleTokenRequest) =>
      apiPost<ConsoleTokenResponse, ConsoleTokenRequest>(
        "/auth/console-token",
        req,
      ),
  });
}
