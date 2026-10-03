import { toast } from "sonner";
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
 * THE TOASTS are dismissed too, and first. sonner keeps its toasts in module
 * state, outside any Toaster, and every Toaster that mounts is handed each one
 * that was never dismissed (Observer.subscribe replays getActiveToasts(), sonner
 * 2.0.8; a toast on screen when its Toaster unmounted, and one raised while none
 * was mounted, were both shown by the next Toaster in jsdom with the real
 * library). The Toaster is AppShell's, and ProtectedRoute remounts AppShell with
 * everything under it on every path that ends or hands over a session, so what
 * the previous user had on screen — a failure naming a node, in the server's
 * words — would be shown again to the next one once they are in. toast.dismiss()
 * marks every active toast dismissed in that state, which is what the replay
 * filters on, and tells a Toaster that is still mounted (a hand-over remounts it
 * a render later) to take them off. It only reaches the toasts that exist when it
 * runs. A toast raised AFTER this, while no Toaster is mounted — the login pages
 * have none — is replayed all the same, so auth-store's adoptIdentity dismisses
 * once more when the next identity begins, before it renders AppShell and the
 * Toaster in it. What nothing here can catch is a toast raised after that, by
 * work that belongs to the session that ended: it is shown at once, to whoever
 * is in, and only the work itself can refuse to raise it (lib/query-client.ts
 * and the hook-level sites that name sessionScope). The dismissal has its own
 * try/catch, so that a failure in sonner cannot skip the cache and the stores,
 * which matter more; and it goes first, so that nothing else can skip it.
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
  dismissToasts();
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

/**
 * Takes every toast that exists off the screen and out of what sonner would show
 * again (see THE TOASTS above). Called when a session ends, for what is on the
 * screen, and when the next identity begins (auth-store adoptIdentity), for what
 * was raised in between. Reported rather than thrown, as auth-store reports the
 * rest of the reset: it is sonner's state that failed, and the session still
 * ends — or begins.
 */
export function dismissToasts(): void {
  try {
    toast.dismiss();
  } catch (err) {
    console.error("Could not dismiss the toasts a session left behind", err);
  }
}
