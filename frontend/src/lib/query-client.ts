import {
  MutationCache,
  QueryClient,
  type Mutation,
} from "@tanstack/react-query";
import { toast } from "sonner";
import {
  ApiClientError,
  sessionScope,
  StaleSessionError,
} from "@/lib/api-client";
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

/**
 * The app's mutation cache, with its error safety net.
 *
 * Project rule: errors must ALWAYS surface to the user. Most action
 * mutations (VM start/stop from the tree and tables, bulk actions, alert
 * ack, ...) historically had no onError at all, so an HTTP failure — which
 * never produces a UPID and therefore bypasses the task panel — was
 * completely silent. This cache-level handler is the safety net: any
 * mutation without its own onError gets a toast.
 *
 * Not one that failed for a session that is over. TanStack runs a mutation's
 * callbacks whenever its answer lands, whatever has happened to the page and
 * the cache since: clearing the cache at Sign out does not stop a mutation in
 * flight (stores/session-reset.ts). And sonner keeps its toasts in module state,
 * not in the Toaster, and shows any that was never dismissed to each Toaster that
 * mounts. AppShell, and the Toaster in it, sits under ProtectedRoute's outlet,
 * which is keyed by the user: a sign-out unmounts it and the next sign-in mounts
 * another, and a change of user remounts it at once. So after a change of user a
 * late failure goes straight into the new Toaster, and after a sign-out it waits
 * in sonner and is replayed into the next one (the login pages have none). The
 * reset dismisses what is on sonner's screen when a session ends or changes
 * hands (adoptIdentity reaches it through forgetSession), and adoptIdentity
 * dismisses again as a new identity begins, which between them cover every toast
 * raised before that. One that settles after it is shown at once, to whoever is
 * signed in, with the previous user's words in it: a node's name in the server's
 * message. Each mutation is therefore tied, when it is submitted, to the session
 * then current (sessionScope), and its failure is reported only while that
 * session still is. That includes the failure that ends one: a request that
 * meets its session's expiry fails after the sign-out it caused, and its
 * "Session expired" has nobody left to be shown to.
 *
 * A StaleSessionError is never reported, in whichever session it arrives: it
 * says that what failed belonged to a session that has since ended, which tells
 * the one now current nothing it can act on.
 *
 * A factory, not an inline literal, so a test can give its QueryClient this
 * very handler (src/test/app-query-client.ts) instead of a copy of it.
 */
export function createMutationCache(): MutationCache {
  // The session each mutation was submitted in, as sessionScope() answers for
  // it. TanStack runs onMutate synchronously inside mutate(), before the request
  // goes out and so before its answer or a change of session can come. Only a
  // mutation restored from a dehydrated state skips it, and the app restores
  // none (it persists no client), so every mutation that can reach onError below
  // has an entry. The map is keyed by the mutation, so an entry goes with it.
  const submittedIn = new WeakMap<Mutation<unknown, unknown>, () => boolean>();
  return new MutationCache({
    onMutate: (_variables, mutation) => {
      submittedIn.set(mutation, sessionScope());
    },
    onError: (error, _variables, _context, mutation) => {
      if (mutation.options.onError) return; // handled inline by the caller
      if (error instanceof StaleSessionError) return;
      // A mutation with no entry is treated as one of an ended session and not
      // reported: nothing says whose it was, and what this net must never do is
      // hand one person's failure to whoever is looking now. It cannot happen
      // today (see above). Should a client that restores mutations ever be added,
      // a restored mutation's failure going unreported is a gap someone will
      // notice, where reporting it could show one user another's words with
      // nothing to give it away.
      const ended = submittedIn.get(mutation);
      if (ended === undefined || ended()) return;
      const message =
        error instanceof Error && error.message.length > 0
          ? error.message
          : "Request failed";
      toast.error(message);
    },
  });
}

// The app-wide QueryClient. Lives outside main.tsx so non-React modules
// (e.g. the WebSocket store's reconnect catch-up) can trigger invalidation
// without a hook context.
export const queryClient = new QueryClient({
  mutationCache: createMutationCache(),
  defaultOptions: {
    queries: {
      staleTime: 5 * 60_000, // 5 minutes — inventory data doesn't change rapidly
      gcTime: 10 * 60_000, // 10 minutes — keep cache longer to avoid refetches
      retry: retryUnlessClientError,
      refetchOnWindowFocus: false,
    },
  },
});
