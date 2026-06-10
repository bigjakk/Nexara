import { MutationCache, QueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

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
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
});
