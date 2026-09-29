import { QueryClient } from "@tanstack/react-query";
import {
  createMutationCache,
  queryClient as appQueryClient,
} from "@/lib/query-client";

/**
 * A QueryClient that meets a failed mutation as the app's does.
 *
 * Its mutation cache is the app's own (createMutationCache, lib/query-client.ts,
 * not a copy of it), so a failed mutation raises the global error toast here
 * exactly when it would in production: when its hook has no onError of its own.
 * renderWithProviders' client (test-utils.tsx) has no such cache, so nothing
 * toasts on it, and a "no toast" assertion there passes with the opt-out
 * removed.
 *
 * Its queries run on the app's own defaults — cache times, no refetch on window
 * focus — with only retry turned off. A test that asserts a toast has to
 * observe it, by mocking sonner (`toast.error`), and pair every "no toast" with
 * a case that does toast on this same client, or its silence proves nothing.
 */
export function createAppQueryClient(): QueryClient {
  return new QueryClient({
    mutationCache: createMutationCache(),
    defaultOptions: {
      queries: { ...appQueryClient.getDefaultOptions().queries, retry: false },
    },
  });
}
