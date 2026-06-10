import { QueryClient } from "@tanstack/react-query";

// The app-wide QueryClient. Lives outside main.tsx so non-React modules
// (e.g. the WebSocket store's reconnect catch-up) can trigger invalidation
// without a hook context.
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5 * 60_000, // 5 minutes — inventory data doesn't change rapidly
      gcTime: 10 * 60_000, // 10 minutes — keep cache longer to avoid refetches
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
});
