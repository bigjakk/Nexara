import { Fragment } from "react";
import { Navigate, Outlet, useLocation } from "react-router-dom";
import { Loader2 } from "lucide-react";
import { useAuth } from "@/hooks/useAuth";
import { useSignOutFromOtherTabs } from "@/hooks/useSignOutFromOtherTabs";
import { useAuthStore } from "@/stores/auth-store";

/**
 * Every signed-in tab follows a sign-out, or a change of user, made in another
 * tab, at once — not when its own access token next needs a refresh.
 */
function FollowOtherTabs() {
  useSignOutFromOtherTabs();
  return null;
}

export function ProtectedRoute() {
  const { user, isAuthenticated, isInitialized } = useAuth();
  const signedOutByUser = useAuthStore((s) => s.signedOutByUser);
  const location = useLocation();

  if (!isInitialized) {
    return (
      <div className="flex h-screen items-center justify-center">
        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!isAuthenticated) {
    // returnTo brings someone whose session EXPIRED back to the page they were
    // on — its query too, which is all a console tab's page has to say which
    // console it is. After a sign-out the user asked for it would hand that
    // page to whoever signs in next, so that one leads to the bare login page.
    // The flag is in memory, so it is clear for a visitor who arrives without a
    // session — a bookmark, an address typed into the bar, is a page load — and
    // that one still gets theirs.
    return (
      <Navigate
        to={
          signedOutByUser
            ? "/login"
            : `/login?returnTo=${encodeURIComponent(location.pathname + location.search)}`
        }
        replace
      />
    );
  }

  // Keyed by who is signed in, so that a session that changes hands — a refresh
  // answered for someone else (see auth-store adoptIdentity) — remounts
  // everything below as a sign-out does. What auth-store resets is the cache
  // and the stores; component state, open dialogs and typed forms would stay,
  // and so would a call made as the previous user that is still waiting for its
  // answer, and the hub socket AppShell opened with their token. The follower
  // of the other tabs goes with them, and looks again at what it missed.
  return (
    <Fragment key={user?.id}>
      <FollowOtherTabs />
      <Outlet />
    </Fragment>
  );
}
