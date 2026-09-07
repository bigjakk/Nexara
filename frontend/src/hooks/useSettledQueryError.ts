import { useRef } from "react";

/**
 * The parts of a TanStack query result the shared failure UI reads. Declared
 * structurally rather than as `UseQueryResult<T>` so a query of any row type
 * can be handed over without a cast or a generic.
 */
export interface QueryStateLike {
  isLoading: boolean;
  isError: boolean;
  isSuccess: boolean;
  isPaused: boolean;
  error: Error | null;
  /** Not cleared by a refetch, unlike `error` — see below. */
  errorUpdatedAt: number;
  fetchStatus: "fetching" | "paused" | "idle";
  /** Typed as returning `unknown` so a promise-returning `refetch` can be
   *  passed straight in without tripping no-misused-promises. */
  refetch: () => unknown;
}

/**
 * The query's last failure, remembered across the refetch that hides it.
 *
 * TanStack clears `error` and resets `status` to "pending" every time it
 * refetches a query that has no data (`fetchState` in query.js), so a caller
 * reading `isError` alone forgets it ever failed the moment the next fetch
 * starts — the failure notice the operator is reading flips back to a loading
 * skeleton, and on an unreachable node it stays there for a full Proxmox
 * timeout. Returning the remembered error keeps the notice up instead.
 *
 * `errorUpdatedAt` is the key because it is what survives that reset: a
 * refetch of the *same* failed query leaves it alone, while a different query
 * (the component was handed a new cluster or node without remounting) arrives
 * with its own value — 0 when it has never failed. So a remembered failure is
 * dropped rather than shown against a read it did not come from, with no
 * argument for the caller to pass and keep in sync. The write is idempotent
 * for a given render input, so StrictMode's double render is harmless.
 *
 * `identity` is belt and braces for a component that outlives the thing it is
 * reading: pass whatever names that thing (a cluster id, a node name) and a
 * remembered failure is additionally confined to it. Two unrelated queries
 * would have to have failed in the same millisecond for the timestamp alone
 * to be fooled, but the check is free and the consequence — a stale failure
 * shown against a healthy object — is the kind that gets believed.
 */
export function useSettledQueryError(
  query: QueryStateLike,
  identity?: string,
): Error | null {
  const remembered = useRef<{
    error: Error;
    errorUpdatedAt: number;
    identity: string | undefined;
  } | null>(null);

  if (query.isError && query.error !== null) {
    remembered.current = {
      error: query.error,
      errorUpdatedAt: query.errorUpdatedAt,
      identity,
    };
  } else if (query.isSuccess) {
    remembered.current = null;
  }

  if (query.isError) return query.error;

  // Spelled out rather than folded into an optional chain: a fake query result
  // in a test can leave errorUpdatedAt undefined, and `undefined === undefined`
  // would then match a null ref and dereference it.
  const last = remembered.current;
  if (last === null) return null;
  if (last.errorUpdatedAt !== query.errorUpdatedAt) return null;
  if (last.identity !== identity) return null;
  return last.error;
}

/**
 * Whether to offer a Retry for a query, and whether that button must be
 * disabled — shared so the several places that draw one cannot drift apart.
 *
 * A paused retryer cannot be restarted from a button: Query.fetch() sees a
 * non-idle fetchStatus and early-returns into continueRetry(), which only
 * flips a flag — nothing dispatches and no request goes out. Only
 * queryCache.onFocus()/onOnline() call the retryer's real continue(). So the
 * button is offered solely from a state a refetch can actually start from,
 * and "busy" covers the in-flight window where a click would be a no-op.
 */
export function retryState(query: QueryStateLike): {
  offer: boolean;
  busy: boolean;
} {
  return { offer: !query.isPaused, busy: query.fetchStatus !== "idle" };
}
