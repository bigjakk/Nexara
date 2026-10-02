import { queryClient } from "@/lib/query-client";
import { useConsoleStore } from "@/stores/console-store";
import { useCreateResourceStore } from "@/stores/create-resource-store";
import { useHealthDismissStore } from "@/stores/health-dismiss-store";
import { useMetricStore } from "@/stores/metric-store";
import { useTaskLogStore } from "@/stores/task-log-store";
import { useVMContextMenuStore } from "@/stores/vm-context-menu-store";

/**
 * Forgets what the SPA read, or was showing, while the session that just ended
 * was signed in. auth-store calls it (through forgetSession) right after
 * clearTokens(), on every path that ends a session locally: logout, logoutAll,
 * clearAuth (which the forced-logout callback and the profile and session
 * pages route into), and a boot with no live session to resume. It calls it
 * too when a session changes hands without ending: an auth response that names
 * a different user than the one held (adoptIdentity).
 *
 * Signing out is a state change, not a reload — AppShell does not reload the
 * page — so every module singleton below outlives it into the next sign-in in
 * the same tab, and wiping the token alone left all of it for whoever signed
 * in next.
 *
 * THE QUERY CACHE is the one that matters most. It holds server reads made
 * under the previous user's permissions. The app's staleTime is five minutes,
 * so the next user is served them with no request at all, and a refetch that
 * fails — a 403, because they may not read it — keeps the old data on screen,
 * by design. So the whole cache goes, not selected keys.
 *
 * cancelQueries() first, then clear(). clear() alone already stops a fetch in
 * flight and drops its late answer (QueryCache.remove -> query.destroy ->
 * cancel({ silent: true })); what cancelQueries() adds is revert: the query
 * goes back to its pre-fetch state, idle, before it is removed. Without it an
 * observer still attached when this runs is left reporting a fetch that never
 * finishes. cancelQueries() alone is not enough either — it keeps the cache.
 *
 * clear() also empties the MutationCache, which does NOT cancel a mutation in
 * flight: its hook-level onSuccess and onSettled still run when it settles,
 * because the mutation holds its own options. A callback that writes the
 * answer into the cache would put the previous user's data straight back. The
 * app has three such writers (setQueryData). Two, in VMActions
 * (handleTaskComplete) and useSetResourceConfig, patch an entry that is already
 * there and so create nothing in an empty cache. The third,
 * useUpsertSSHCredentials, seeds one, and pins the session it was sent in for
 * that reason.
 *
 * SYNCHRONOUS, ON PURPOSE. The caller sets isAuthenticated to false in the same
 * turn, and React renders after both, so ProtectedRoute unmounts the
 * authenticated tree — every query observer in the app — in the render that
 * follows. A gap of a macrotask or more between the two would let a render in
 * between rebuild an observer's removed query with no token: an observer whose
 * query is gone builds a new one, and fetches it, the next time its options
 * are set, and the observers are told of the cancellations from a
 * setTimeout(0). A gap of microtasks is harmless: nothing renders in it.
 *
 * WHICH STORES. A store goes here when it holds data derived from the session
 * (names, ids, the content of what was open) rather than a choice about how
 * this browser looks:
 *
 *   reset                  why
 *   console-store          tabs name the guests and nodes the user opened; they
 *                          are persisted and the active one dials on mount.
 *                          Window position and size are the browser's: kept.
 *   task-log-store         focusedTask names a cluster and a guest. Panel open
 *                          and height are the browser's: kept.
 *   metric-store           live metrics and history by cluster. The refresh
 *                          interval is the browser's: kept.
 *   health-dismiss-store   persisted signatures embed the cluster id, the
 *                          target and the issue's detail text, and hide those
 *                          issues from whoever signs in next.
 *   vm-context-menu-store  the target (a guest's name and node) and the dialog
 *                          open on it, which AppShell renders from the store.
 *   create-resource-store  the open create dialog and the cluster it targets,
 *                          rendered the same way.
 *
 *   kept (the browser's)   why
 *   theme-store, sidebar-store (layout; its expanded keys are opaque ids),
 *   branding-store (the installation's, the same for everyone),
 *   changelog-store (which release notes this browser showed),
 *   preferences-store (display settings applied before sign-in — the login
 *   page's language and accent — and holding no data of anyone's),
 *   health-mute-store (issue-type names, a standing setting "until the user
 *   restores it"), and the column layouts and presets in localStorage.
 *
 *   NOT reset, and never to be: pbs-key-store. It is owner-scoped on purpose:
 *   a PBS encryption key Proxmox generated is shown once, to the user whose
 *   save generated it, and Nexara keeps no other copy, so clearing it here
 *   would destroy a key its owner has not saved yet. It drops a key itself,
 *   when a different user signs in. It must not be imported from here, either:
 *   it imports auth-store, which imports this. websocket-store is not reset
 *   here: AppShell disconnects it when it unmounts.
 *
 * A new store has to be classified in stores/session-reset.test.ts, which
 * fails until it is: one that belongs to the session gets a probe in
 * src/test/per-session-stores.ts and a line below.
 *
 * Throws only if localStorage does. console-store persists through zustand's
 * persist middleware, which writes after it has updated the store and does not
 * catch a full quota; the other stores catch their own. So console-store goes
 * last — everything else is already reset when it throws, and its own state is
 * too — and auth-store reports the failure (console.error) and carries on: a
 * sign-out, or the next user's sign-in, completes regardless.
 */
export function resetSessionState(): void {
  void queryClient.cancelQueries();
  queryClient.clear();

  useTaskLogStore.getState().resetSession();
  useMetricStore.getState().clearAll();
  // Not restoreAll, which returns early when this tab's list is empty and so
  // leaves what another tab persisted.
  useHealthDismissStore.getState().resetSession();
  useVMContextMenuStore.getState().resetSession();
  useCreateResourceStore.getState().resetSession();
  useConsoleStore.getState().resetSession();
}
