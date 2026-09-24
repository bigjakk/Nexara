import { MutationCache, QueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ApiClientError } from "@/lib/api-client";
import { PathSegmentError } from "@/lib/api-path";

/**
 * 4xx statuses that do mean "ask again", so they keep the one retry.
 * Everything else in the 4xx range is the server saying the request itself is
 * wrong, and asking a second time gets the same answer. The general limiter in
 * internal/api/middleware.go skips only /api/v1/auth and /ws, so an ordinary
 * GET really can be throttled.
 *
 * The retry is a courtesy rather than a cure: Fiber's limiter is a fixed
 * window whose Retry-After can be tens of seconds out, while the first retry
 * waits ~1s, so the second attempt usually lands in the same window and fails
 * alike. Listing them here costs one request and keeps the pre-existing
 * behaviour; making it actually work would need a retryDelay that reads
 * Retry-After.
 */
const RETRYABLE_CLIENT_ERRORS = new Set([408, 429]);

/**
 * One retry, except for client errors. A 403 from an RBAC check or a 404 for a
 * resource that does not exist is deterministic: retrying only delays the error
 * the UI needs to show, and — because TanStack resets a query with no data to
 * `pending` on every refetch — stretches the loading skeleton that replaces the
 * error card on each poll.
 *
 * It also quiets a 401 storm at session expiry: api-client clears the tokens
 * before it throws, so a retry could never rescue the session, but it did send
 * another /auth/refresh plus another unauthenticated request for every query
 * in flight.
 *
 * A path apiPath refused (lib/api-path.ts) is the same kind of answer, given
 * before any request: the object's name cannot be sent as a path segment, and
 * it will not become sendable on a second try.
 */
export function retryUnlessClientError(
  failureCount: number,
  error: Error,
): boolean {
  if (error instanceof PathSegmentError) {
    return false;
  }
  if (
    error instanceof ApiClientError &&
    error.status >= 400 &&
    error.status < 500 &&
    !RETRYABLE_CLIENT_ERRORS.has(error.status)
  ) {
    return false;
  }
  return failureCount < 1;
}

// The app-wide QueryClient. Lives outside main.tsx so non-React modules
// (e.g. the WebSocket store's reconnect catch-up) can trigger invalidation
// without a hook context.
export const queryClient = new QueryClient({
  // Project rule: errors must ALWAYS surface to the user. Most action
  // mutations (VM start/stop from the tree and tables, bulk actions, alert
  // ack, ...) historically had no onError at all, so an HTTP failure — which
  // never produces a UPID and therefore bypasses the task panel — was
  // completely silent. This cache-level handler is the safety net: any
  // mutation without its own onError gets a toast.
  mutationCache: new MutationCache({
    onError: (error, _variables, _context, mutation) => {
      if (mutation.options.onError) return; // handled inline by the caller
      const message =
        error instanceof Error && error.message.length > 0
          ? error.message
          : "Request failed";
      toast.error(message);
    },
  }),
  defaultOptions: {
    queries: {
      staleTime: 5 * 60_000, // 5 minutes — inventory data doesn't change rapidly
      gcTime: 10 * 60_000, // 10 minutes — keep cache longer to avoid refetches
      retry: retryUnlessClientError,
      refetchOnWindowFocus: false,
    },
  },
});
