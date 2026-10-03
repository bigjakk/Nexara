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
 * The net keeps to the session a mutation was submitted in, here as in the app:
 * a failure that settles after that session ended or changed hands (api-client's
 * clearTokens(), or storeTokens() for another user) is not toasted, and neither
 * is a StaleSessionError. So a test that expects a toast must not let the
 * session end between the submit and the failure. A real api-client with nobody
 * signed in does just that: its first request tries a refresh, which fails, and
 * the failure clears the tokens — a session ending under a mutation that was
 * submitted before it — so such a test signs in first
 * (test/late-toast-sessions.ts), or it passes only when an earlier test of the
 * file has already latched the module signed out. A test that asserts the
 * silence after a session ended pairs it with the same failure in a session
 * that goes on.
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
