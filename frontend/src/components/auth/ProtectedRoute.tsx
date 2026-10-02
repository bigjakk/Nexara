import { Navigate, Outlet, useLocation } from "react-router-dom";
import { Loader2 } from "lucide-react";
import { useAuth } from "@/hooks/useAuth";
import { useAuthStore } from "@/stores/auth-store";

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
    // on. After a sign-out the user asked for it would hand that page to
    // whoever signs in next, so that one leads to the bare login page. The flag
    // is in memory, so it is clear for a visitor who arrives without a session —
    // a bookmark, an address typed into the bar, is a page load — and that one
    // still gets theirs.
    return (
      <Navigate
        to={
          signedOutByUser
            ? "/login"
            : `/login?returnTo=${encodeURIComponent(location.pathname)}`
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
  // answer, and the hub socket AppShell opened with their token.
  return <Outlet key={user?.id} />;
}
