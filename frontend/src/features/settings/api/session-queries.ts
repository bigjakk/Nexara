import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient, sessionScope } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { useAuthStore } from "@/stores/auth-store";
import type { UserSession } from "@/types/api";

/**
 * Query key for one user's session list.
 *
 * Keyed by user id, which matters more here than for most queries. Signing out
 * clears the QueryClient (stores/session-reset.ts), and a QUERY's own response
 * cannot land after that: clear() cancels the fetch, and the retryer ignores a
 * later settle. So this is the second line, against a writer that is not a
 * query — a mutation's setQueryData still in flight when the session ended runs
 * its callbacks anyway. The client defaults to a 5 minute staleTime, so what
 * such a write left under a shared key would let the next person to sign in on
 * this browser read the previous user's rows straight from cache with no
 * refetch. Those rows are device names, IP addresses and activity times, so
 * that is a cross-user disclosure rather than merely stale UI.
 */
const sessionsKey = (userID: string) => ["auth", "sessions", userID] as const;

/** The caller's own active sessions — what is signed in to this account. */
export function useSessions() {
  const userID = useAuthStore((s) => s.user?.id ?? "");
  return useQuery({
    queryKey: sessionsKey(userID),
    queryFn: () => apiClient.list<UserSession>(apiPath`/api/v1/auth/sessions`),
    // No user means nothing to ask about, and the endpoint would 401 anyway.
    enabled: userID !== "",
  });
}

/** What a revoke takes when it is made: see useRevokeSession. */
interface RevokeMade {
  /** True once the session the revoke was made in has ended or been replaced. */
  ended: () => boolean;
  /** Who made it: the user the tab showed then, or null when it showed nobody. */
  by: string | null;
}

/**
 * What a revoke does when it settles, in whichever way, after the session it was
 * made in has ended (see useRevokeSession for why). Only a revoke of the current
 * session raised the flag, so only that one has anything to do — and it lowers no
 * flag, since one that is up now may not be the one it raised.
 *
 * With the flag down there is nothing left of the sign-out it began: the session
 * ended through clearAuth, which lowered it, or somebody else was adopted since,
 * which lowered it too (adoptIdentity). With the flag up and the store holding the
 * revoke's author, signed in, the sign-out is finished here: either the session
 * ended without the store being told — nobody else was adopted in this tab — or
 * the same user signed in again and has begun a sign-out of their own, which is
 * what finishing it does. With the flag up and somebody else shown, it is theirs:
 * a sign-out they have begun, which the revoke leaves to them.
 */
function settledAfterTheSessionEnded(
  session: UserSession,
  by: string | null,
): void {
  if (!session.is_current) return;
  const state = useAuthStore.getState();
  if (!state.isLoggingOut) return;
  if (state.isAuthenticated && state.user !== null && state.user.id === by) {
    state.clearAuth({ byUser: true });
  }
}

/**
 * Revoke one session.
 *
 * Takes the whole session rather than just its id so the callbacks below can
 * live HERE, at hook level, instead of being passed per-call from the row that
 * triggered them. That is not a style choice: one useMutation shared across
 * many rows drops the first call's per-call callbacks when a second mutate()
 * runs — mutate() reassigns the observer's options and detaches it from the
 * in-flight mutation. Revoking two sessions quickly would then skip the
 * clearAuth below for the first one, leaving the SPA believing it is still
 * signed in on a session the server has already killed. Hook-level callbacks
 * live on the mutation itself and always fire.
 *
 * Always fire, and so also after the session they were made in has ended — a
 * sign-out, an expiry, the same or another user signing in. A revoke of the
 * current session that settles then must not sign out whoever is signed in by
 * now: the session it revoked is gone, and clearAuth would end theirs. So
 * `onMutate` takes the session the revoke is made in (sessionScope) and who made
 * it, and hands both to the callbacks as their context. Once the session has
 * ended they drop no cache, invalidate nothing and lower no flag, and do one
 * thing (settledAfterTheSessionEnded): the tab can still be showing the revoke's
 * author, flag up, although the session ended. A refresh answered for ANOTHER
 * user does that. Another tab signed them in on the shared cookie, api-client
 * moves its epoch and installs their token, and auth-store's refresh callback
 * drops the answer because the flag is up (it holds back a refresh that resolves
 * mid-logout). The store then shows the author over someone else's token, and
 * nothing else will end that or lower the flag. So the revoke finishes the
 * sign-out the user asked for, in this tab: clearAuth, as the success path does,
 * and for a failure too, since the token this tab holds is no longer the one its
 * screen is showing. The same user signed in again, with the flag up, has begun
 * a sign-out of their own, and gets that.
 *
 * Anyone else is left alone, the flag included. Somebody adopted since had the
 * flag lowered as they began (adoptIdentity), and one that is up now is a
 * sign-out they began themselves: lowering it would turn their forced sign-out,
 * if their session is dead, into one that carries their last page to whoever
 * signs in next. So it never signs out anyone who was adopted since, which is why
 * the sign-out above needs the store to still hold the author.
 */
export function useRevokeSession() {
  const qc = useQueryClient();
  const clearAuth = useAuthStore((s) => s.clearAuth);
  const userID = useAuthStore((s) => s.user?.id ?? "");

  return useMutation({
    mutationFn: (session: UserSession) =>
      apiClient.delete<{ message: string }>(
        apiPath`/api/v1/auth/sessions/${session.id}`,
      ),
    onMutate: (session): RevokeMade => {
      // Taken first, before the flag is raised: the session this revoke is made
      // in, and who made it, which is what the callbacks below are judged
      // against.
      const made = {
        ended: sessionScope(),
        by: useAuthStore.getState().user?.id ?? null,
      };
      // The same guard logout()/logoutAll() raise before their network call.
      // A /auth/refresh already in flight can resolve after this revoke lands
      // and rehydrate isAuthenticated through setAuthRefreshCallback, undoing
      // the sign-out; the flag makes that callback drop the late response.
      if (session.is_current) {
        useAuthStore.setState({ isLoggingOut: true });
      }
      return made;
    },
    onError: (_err, session, made) => {
      // A revoke whose session cannot be told is left alone entirely: onMutate
      // did not run, so it raised nothing.
      if (made === undefined) return;
      if (made.ended()) {
        settledAfterTheSessionEnded(session, made.by);
        return;
      }
      // Nothing was signed out, so leaving the flag raised would suppress
      // permission rehydration for the rest of the session.
      if (session.is_current) {
        useAuthStore.setState({ isLoggingOut: false });
      }
    },
    onSuccess: (_data, session, made: RevokeMade | undefined) => {
      // TanStack types the context as always there for onSuccess, and it is once
      // onMutate has run; the parameter says what a revoke that skipped it would
      // give, so that one is left alone rather than made to throw. Its flag was
      // never raised, so there is none to lower.
      if (made === undefined) return;
      if (made.ended()) {
        settledAfterTheSessionEnded(session, made.by);
        return;
      }
      // Revoking the session you are holding: the server has already cleared
      // the refresh cookie, so there is nothing left to refresh with. Drop
      // local auth state rather than let the next silent refresh fail and
      // bounce the user out with an error they did not ask for.
      if (session.is_current) {
        // Drop the list outright rather than invalidate it: this browser is
        // signed out, so a refetch would only 401, and leaving the rows in
        // cache keeps someone else's devices one login away from being read.
        qc.removeQueries({ queryKey: sessionsKey(userID) });
        // The user ended this session themselves: the login page it leads to
        // does not carry the page they were on (signedOutByUser). Explicit
        // intent, and redundant with isLoggingOut, which onMutate raised and
        // clearAuth also reads.
        clearAuth({ byUser: true });
        return;
      }
      void qc.invalidateQueries({ queryKey: sessionsKey(userID) });
    },
  });
}
