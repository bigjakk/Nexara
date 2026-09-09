import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import type { UserSession } from "@/types/api";

/**
 * Query key for one user's session list.
 *
 * Keyed by user id, which matters more here than for most queries. Nothing
 * clears the QueryClient on logout — logout is a state change, not a reload —
 * and the client defaults to a 5 minute staleTime, so a shared key would let
 * the next person to sign in on this browser read the previous user's rows
 * straight from cache with no refetch. Those rows are device names, IP
 * addresses and activity times, so that is a cross-user disclosure rather than
 * merely stale UI.
 */
const sessionsKey = (userID: string) => ["auth", "sessions", userID] as const;

/** The caller's own active sessions — what is signed in to this account. */
export function useSessions() {
  const userID = useAuthStore((s) => s.user?.id ?? "");
  return useQuery({
    queryKey: sessionsKey(userID),
    queryFn: () => apiClient.list<UserSession>("/api/v1/auth/sessions"),
    // No user means nothing to ask about, and the endpoint would 401 anyway.
    enabled: userID !== "",
  });
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
 */
export function useRevokeSession() {
  const qc = useQueryClient();
  const clearAuth = useAuthStore((s) => s.clearAuth);
  const userID = useAuthStore((s) => s.user?.id ?? "");

  return useMutation({
    mutationFn: (session: UserSession) =>
      apiClient.delete<{ message: string }>(
        `/api/v1/auth/sessions/${session.id}`,
      ),
    onMutate: (session) => {
      // The same guard logout()/logoutAll() raise before their network call.
      // A /auth/refresh already in flight can resolve after this revoke lands
      // and rehydrate isAuthenticated through setAuthRefreshCallback, undoing
      // the sign-out; the flag makes that callback drop the late response.
      if (session.is_current) {
        useAuthStore.setState({ isLoggingOut: true });
      }
    },
    onError: (_err, session) => {
      // Nothing was signed out, so leaving the flag raised would suppress
      // permission rehydration for the rest of the session.
      if (session.is_current) {
        useAuthStore.setState({ isLoggingOut: false });
      }
    },
    onSuccess: (_data, session) => {
      // Revoking the session you are holding: the server has already cleared
      // the refresh cookie, so there is nothing left to refresh with. Drop
      // local auth state rather than let the next silent refresh fail and
      // bounce the user out with an error they did not ask for.
      if (session.is_current) {
        // Drop the list outright rather than invalidate it: this browser is
        // signed out, so a refetch would only 401, and leaving the rows in
        // cache keeps someone else's devices one login away from being read.
        qc.removeQueries({ queryKey: sessionsKey(userID) });
        clearAuth();
        return;
      }
      void qc.invalidateQueries({ queryKey: sessionsKey(userID) });
    },
  });
}
