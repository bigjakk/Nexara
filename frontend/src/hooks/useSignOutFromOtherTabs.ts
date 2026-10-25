import { useEffect } from "react";
import { confirmSession, getStoredUser, USER_KEY } from "@/lib/api-client";
import {
  SIGNED_OUT_KEY,
  signOutNoteApplies,
  useAuthStore,
  type SignedOutNote,
} from "@/stores/auth-store";

/**
 * How long a sign-out note may be acted on at the server. A tab frozen in the
 * background hears its storage events when it wakes, perhaps after the same
 * user signed in again on the cookie the tabs share: a revoke sent for an older
 * note would end that newer session, so this tab then ends only its own.
 */
const NOTE_FRESH_MS = 10_000;

function parsed<T>(record: string | null): Partial<T> | null {
  if (record === null) return null;
  try {
    return JSON.parse(record) as Partial<T> | null;
  } catch {
    return null;
  }
}

/**
 * Follows a change of the browser's session made in another tab, as soon as
 * it is made. A tab only learns of one on its own when a refresh is refused:
 * access tokens are checked without a session lookup and refreshed only for a
 * request, so a console tab kept its shell open after Sign out in the main tab
 * until its token ran out.
 *
 * - A sign-out there (SIGNED_OUT_KEY, written just before the stored user
 *   goes): this tab's session ends too, as the user's own sign-out, so the
 *   login page hands this page on to nobody — and where the server never got
 *   the sign-out, this tab sends it, as the user asked, and says nothing more.
 *   A note from before this tab's session began was about an earlier one.
 * - The stored user now names someone else: they signed in on the cookie the
 *   tabs share, and this tab's session is orphaned. It ends here the same way.
 * - It is gone with no sign-out to say why: a refusal there, a resume that
 *   only could not reach the server (clearTokens removes it then too), or the
 *   current session revoked on the Sessions page. This tab cannot tell which,
 *   so it asks the server (confirmSession), and a refusal ends the session as
 *   every refusal does.
 */
export function useSignOutFromOtherTabs(): void {
  useEffect(() => {
    let asking: ReturnType<typeof setTimeout> | undefined;

    /** This tab's user, while it holds a session that is not ending. */
    function mine(): string | null {
      const { user, isAuthenticated, isLoggingOut } = useAuthStore.getState();
      return isAuthenticated && !isLoggingOut && user !== null ? user.id : null;
    }

    /** Follows a sign-out note of this tab's user. */
    function followSignOut(note: Partial<SignedOutNote> | null) {
      const me = mine();
      const { clearAuth, logout, sessionBeganAt } = useAuthStore.getState();
      // After a sign-in on this tab's login page the console comes back in
      // the same page, where the last note written since it loaded is still
      // stored: one from before the session began is about an earlier one.
      if (me === null || !signOutNoteApplies(note, me, sessionBeganAt)) return;
      if (note.revoked === true) {
        // Over at the server, and the sign-out removes the stored user itself:
        // one that names this user here is on its way out, or a newer session's.
        clearAuth({ byUser: true, keepStoredUser: true });
        return;
      }
      const stored = getStoredUser()?.id;
      if (
        Date.now() - note.at > NOTE_FRESH_MS ||
        (stored !== undefined && stored !== me)
      ) {
        // Perhaps alive at the server still: a stored user that names this user
        // may be one a refresh of it wrote back, and goes with this session.
        clearAuth({ byUser: true });
        return;
      }
      // The server never got it: sent from here, as the user asked there, and
      // as a plain sign-out even when it was Sign out everywhere — every
      // console tab sends at once, and that one is limited to a few a minute.
      void logout({ announce: false });
    }

    function followUser(id: unknown) {
      const me = mine();
      if (me === null || id === me) return;
      if (id !== undefined) {
        useAuthStore.getState().clearAuth({ byUser: true });
        return;
      }
      // After a moment: every console tab of the browser hears this at once,
      // and one tab's answer writes the record back for all of them.
      clearTimeout(asking);
      asking = setTimeout(() => {
        if (mine() !== me) return;
        const now = getStoredUser()?.id;
        // Back by now, or someone else's: followed like any other change.
        if (now !== undefined) followUser(now);
        else void confirmSession();
      }, Math.random() * 2_000);
    }

    // Made after this tab's session began but before it listened: a sign-out,
    // or a change of the stored user.
    followSignOut(parsed<SignedOutNote>(localStorage.getItem(SIGNED_OUT_KEY)));
    followUser(getStoredUser()?.id);

    function onStorage(event: StorageEvent) {
      if (event.storageArea !== localStorage) return;
      if (event.key === SIGNED_OUT_KEY) {
        followSignOut(parsed<SignedOutNote>(event.newValue));
        return;
      }
      // A null key is a clear() of the whole storage.
      if (event.key !== null && event.key !== USER_KEY) return;
      // What the other tab wrote, not what storage holds now: this tab's own
      // refresh may have written its user back since.
      followUser(
        event.key === null
          ? undefined
          : parsed<{ id: unknown }>(event.newValue)?.id,
      );
    }
    window.addEventListener("storage", onStorage);
    return () => {
      window.removeEventListener("storage", onStorage);
      clearTimeout(asking);
    };
  }, []);
}
