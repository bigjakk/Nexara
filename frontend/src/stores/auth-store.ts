import { create } from "zustand";
import type {
  AuthResponse,
  LoginRequest,
  RegisterRequest,
  TOTPRequiredResponse,
  TOTPVerifyLoginRequest,
  User,
} from "@/types/api";
import {
  apiClient,
  clearTokens,
  currentSessionEpoch,
  getStoredUser,
  setAuthFailureCallback,
  setAuthRefreshCallback,
  storeTokens,
} from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { resetSessionState } from "@/stores/session-reset";

interface AuthState {
  user: User | null;
  permissions: string[];
  isAuthenticated: boolean;
  isLoading: boolean;
  isInitialized: boolean;
  totpPending: boolean;
  totpPendingToken: string | null;
  // True between the moment logout starts and the local clearAuth that
  // follows it. Suppresses the refresh-rehydration callback so a
  // /auth/refresh that resolves mid-logout cannot re-flip
  // isAuthenticated back to true.
  isLoggingOut: boolean;
  // True from a sign-out the user asked for (Sign out, Sign out everywhere,
  // revoking the session they hold) until the next sign-in. ProtectedRoute then
  // leaves the page they were on out of the login URL: returnTo is there to
  // bring someone whose session EXPIRED back to where they were, and after a
  // sign-out it would hand that page to whoever signs in next.
  signedOutByUser: boolean;
}

interface AuthActions {
  login: (req: LoginRequest) => Promise<void>;
  register: (req: RegisterRequest) => Promise<void>;
  logout: () => Promise<void>;
  logoutAll: () => Promise<void>;
  initialize: () => Promise<void>;
  /** `byUser`: the user asked for this, rather than the session failing. */
  clearAuth: (options?: { byUser?: boolean }) => void;
  setAuthFromResponse: (res: AuthResponse) => void;
  verifyTotp: (code: string) => Promise<void>;
  verifyTotpRecovery: (recoveryCode: string) => Promise<void>;
  clearTotpPending: () => void;
}

function isTotpRequired(
  res: AuthResponse | TOTPRequiredResponse,
): res is TOTPRequiredResponse {
  return "totp_pending_token" in res;
}

type SetAuth = (partial: Partial<AuthState>) => void;

/**
 * Forgets the state of a session that is over (stores/session-reset.ts). A
 * failure to is reported rather than thrown: it comes only from localStorage (a
 * full quota, in console-store's persist), and must not stop a sign-out — or
 * the next user's sign-in — from completing, or leave boot on the spinner that
 * waits for isInitialized.
 */
function forgetSession(): void {
  try {
    resetSessionState();
  } catch (err) {
    console.error("Could not forget the state of the session that ended", err);
  }
}

/** The session is over: forgets what it left, then applies `signedOut`. */
function endSession(signedOut: () => void): void {
  forgetSession();
  signedOut();
}

/**
 * Applies the identity an auth response names. A response that names someone
 * other than the user held is a session changing hands without ending — a
 * refresh after another tab signed someone else in on the shared cookie, an SSO
 * callback over a live session, a resume whose cookie is no longer the stored
 * user's — and the new user would otherwise be served the previous one's cached
 * reads, console tabs and dialogs. So their state goes first, as if they had
 * signed out. The same user again (a refresh, a permission change) keeps theirs.
 */
function adoptIdentity(
  set: SetAuth,
  res: AuthResponse,
  held: Pick<User, "id"> | null,
  extra: Partial<AuthState> = {},
): void {
  if (held !== null && held.id !== res.user.id) forgetSession();
  set({
    user: res.user,
    permissions: res.permissions,
    isAuthenticated: true,
    signedOutByUser: false,
    ...extra,
  });
}

export const useAuthStore = create<AuthState & AuthActions>()((set, get) => ({
  user: null,
  permissions: [],
  isAuthenticated: false,
  isLoading: false,
  isInitialized: false,
  totpPending: false,
  totpPendingToken: null,
  isLoggingOut: false,
  signedOutByUser: false,

  initialize: async () => {
    // Register callback for forced logout on auth failure first so any
    // refresh failure inside this method also routes through it.
    setAuthFailureCallback(() => {
      // Expiry is reported by every request that meets it: a burst of reads
      // answered 401 together each call this, and so does a read that fails once
      // nobody is signed in — including a query an observer rebuilt after the
      // reset. Only the first has a session to end. The rest would wipe the
      // cache and every store again, and a rebuilt query's report would clear
      // that very query, which its observer rebuilds, forever
      // (stores/session-reset.ts).
      if (!get().isAuthenticated) return;
      get().clearAuth();
    });

    // Re-hydrate user + permissions on every successful background
    // refresh so a permission rotation reaches the SPA within one access-
    // token lifetime (Finding A11 — no logout required). Suppressed
    // during logout so a refresh that resolves mid-logout cannot re-flip
    // isAuthenticated back to true.
    setAuthRefreshCallback((res) => {
      if (get().isLoggingOut) return;
      get().setAuthFromResponse(res);
    });

    // Cached user metadata only seeds optimistic UI; the cookie-backed
    // /auth/refresh below is the actual authentication gate.
    const storedUser = getStoredUser();
    if (!storedUser) {
      // No session to resume, so none of what is persisted for a session is
      // anyone's: sessions that ended before this build left their console
      // tabs and dismissed issues behind. And a page that starts signed out is
      // latched like every other signed-out state (see signedOut in api-client):
      // the refresh cookie may still be good, and only a sign-in may begin a
      // session here — not a request that finds nobody signed in.
      clearTokens();
      endSession(() => {
        set({ isInitialized: true });
      });
      return;
    }

    set({ isLoading: true });

    // The session this resume is for. If one begins or ends while the refresh
    // is out — someone signs in from the login page while it is slow — its
    // answer is not applied over theirs (see sessionEpoch in api-client).
    const epoch = currentSessionEpoch();

    try {
      // Empty body — the HttpOnly refresh cookie carries the token.
      const res = await apiClient.postPublic<AuthResponse>(
        apiPath`/api/v1/auth/refresh`,
        {},
      );
      if (currentSessionEpoch() !== epoch) {
        set({ isLoading: false, isInitialized: true });
        return;
      }
      storeTokens(res);
      // The cookie may no longer be the stored user's (another tab signed
      // someone else in): what was persisted for the stored user goes.
      adoptIdentity(set, res, storedUser, {
        isLoading: false,
        isInitialized: true,
      });
    } catch {
      if (currentSessionEpoch() !== epoch) {
        set({ isLoading: false, isInitialized: true });
        return;
      }
      clearTokens();
      // The cache is empty this early, but the stores that persist (console
      // tabs, dismissed issues) were just rehydrated from the dead session.
      endSession(() => {
        set({
          user: null,
          permissions: [],
          isAuthenticated: false,
          isLoading: false,
          isInitialized: true,
        });
      });
    }
  },

  login: async (req: LoginRequest) => {
    set({ isLoading: true });
    try {
      const res = await apiClient.postPublic<
        AuthResponse | TOTPRequiredResponse
      >(apiPath`/api/v1/auth/login`, req);

      if (isTotpRequired(res)) {
        set({
          isLoading: false,
          totpPending: true,
          totpPendingToken: res.totp_pending_token,
        });
        return;
      }

      storeTokens(res);
      adoptIdentity(set, res, get().user, {
        isLoading: false,
        totpPending: false,
        totpPendingToken: null,
      });
    } catch (err) {
      set({ isLoading: false });
      throw err;
    }
  },

  verifyTotp: async (code: string) => {
    const { totpPendingToken } = get();
    if (!totpPendingToken) throw new Error("No pending TOTP challenge");

    set({ isLoading: true });
    try {
      const body: TOTPVerifyLoginRequest = {
        totp_pending_token: totpPendingToken,
        code,
      };
      const res = await apiClient.postPublic<AuthResponse>(
        apiPath`/api/v1/auth/totp/verify-login`,
        body,
      );
      storeTokens(res);
      adoptIdentity(set, res, get().user, {
        isLoading: false,
        totpPending: false,
        totpPendingToken: null,
      });
    } catch (err) {
      set({ isLoading: false });
      throw err;
    }
  },

  verifyTotpRecovery: async (recoveryCode: string) => {
    const { totpPendingToken } = get();
    if (!totpPendingToken) throw new Error("No pending TOTP challenge");

    set({ isLoading: true });
    try {
      const body: TOTPVerifyLoginRequest = {
        totp_pending_token: totpPendingToken,
        recovery_code: recoveryCode,
      };
      const res = await apiClient.postPublic<AuthResponse>(
        apiPath`/api/v1/auth/totp/verify-login`,
        body,
      );
      storeTokens(res);
      adoptIdentity(set, res, get().user, {
        isLoading: false,
        totpPending: false,
        totpPendingToken: null,
      });
    } catch (err) {
      set({ isLoading: false });
      throw err;
    }
  },

  clearTotpPending: () => {
    set({ totpPending: false, totpPendingToken: null });
  },

  register: async (req: RegisterRequest) => {
    set({ isLoading: true });
    try {
      const res = await apiClient.postPublic<AuthResponse>(
        apiPath`/api/v1/auth/register`,
        req,
      );
      storeTokens(res);
      adoptIdentity(set, res, get().user, { isLoading: false });
    } catch (err) {
      set({ isLoading: false });
      throw err;
    }
  },

  logout: async () => {
    set({ isLoggingOut: true });
    // Empty body — the HttpOnly cookie carries the refresh token. Always hit
    // /auth/logout so the server can revoke the session and clear the cookie.
    try {
      await apiClient.post(apiPath`/api/v1/auth/logout`, {});
    } catch {
      // Proceed with local cleanup even if server logout fails
    }
    clearTokens();
    endSession(() => {
      set({
        user: null,
        permissions: [],
        isAuthenticated: false,
        totpPending: false,
        totpPendingToken: null,
        isLoggingOut: false,
        signedOutByUser: true,
      });
    });
  },

  logoutAll: async () => {
    set({ isLoggingOut: true });
    try {
      await apiClient.post(apiPath`/api/v1/auth/logout-all`);
    } catch {
      // Proceed with local cleanup even if server call fails
    }
    clearTokens();
    endSession(() => {
      set({
        user: null,
        permissions: [],
        isAuthenticated: false,
        totpPending: false,
        totpPendingToken: null,
        isLoggingOut: false,
        signedOutByUser: true,
      });
    });
  },

  clearAuth: (options) => {
    clearTokens();
    endSession(() => {
      set({
        user: null,
        permissions: [],
        isAuthenticated: false,
        totpPending: false,
        totpPendingToken: null,
        isLoggingOut: false,
        // A sign-out the user asked for that finds the session already gone —
        // its own request refused, and the refresh behind it — gets here first,
        // through the forced-logout path, before logout() has reached its last
        // step. It is still theirs: the page has not yet been left for the login
        // URL, and which one it is depends on this.
        signedOutByUser: options?.byUser === true || get().isLoggingOut,
      });
    });
  },

  setAuthFromResponse: (res: AuthResponse) => {
    adoptIdentity(set, res, get().user, { isLoading: false });
  },
}));
