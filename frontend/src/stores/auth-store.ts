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
  resumeSessionPatiently,
  setAuthFailureCallback,
  setAuthRefreshCallback,
  signOutRequest,
  storeTokens,
} from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { dismissToasts, resetSessionState } from "@/stores/session-reset";

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
 * Whether the refresh cookie in the jar may be someone other than `mine`'s.
 *
 * nexara_user is the one record of whose cookie it is. Every tab of the browser
 * shares it; a sign-in writes it, and so does every refresh that rotates the
 * cookie, and a session that ends removes its own and leaves another user's
 * (clearTokens, lib/api-client.ts). So it names the user the cookie was last
 * issued to, and when that is not this tab's user,
 * another tab has signed someone else in and replaced the cookie: this tab's own
 * session was orphaned then, and the cookie in the jar is not its to use. No
 * stored user names no one, and is not taken for someone else's. One that
 * cannot be told from this tab's user — no id, which a corrupt value comes to —
 * is: the safe answer to a question whose wrong one revokes a stranger's
 * session.
 */
function cookieIsAnotherUsers(mine: Pick<User, "id"> | null): boolean {
  const owner = getStoredUser();
  return owner !== null && (mine === null || owner.id !== mine.id);
}

/**
 * The last step of Sign out and Sign out everywhere, once the server has been
 * asked: the session they were for ends here.
 *
 * `epoch` is that session's, taken before the request went (currentSessionEpoch),
 * and `held` the user the store held then. When the epoch has moved the session
 * is not the current one any more, and ending the current one — clearTokens()
 * ends whichever there is — would end somebody else's. If the store has moved on
 * too (it no longer holds `held`), either
 *
 *  - the session ended while the request was out (a refresh the server refused,
 *    another sign-out that finished first), and what ended it did what this
 *    would; or
 *  - a new one began (a sign-in, say), and the store followed it. Its stored
 *    user and its per-session state are its own, and none of this sign-out's to
 *    end.
 *
 * Nothing is done to it, then, but to drop the sign-out's own flag:
 * isLoggingOut was raised for the request, makes the refresh callback drop what
 * it is given while it is up, and must not stay up.
 *
 * When the store has NOT moved on — it holds the very user it held — the sign-out
 * is the user's to finish, whatever the epoch says. That is a session that began
 * without the store following it: a refresh answered for another user on the
 * cookie the tabs share, which the callback drops while the flag is up. The tab
 * would go on showing a user it holds no token for, and acting as the next one
 * while it did. The user is compared by identity, not by id: the same user
 * signing in again is a new session, and the store's user is a new object.
 */
function finishSignOut(
  set: SetAuth,
  get: () => Pick<AuthState, "user">,
  epoch: number,
  held: User | null,
): void {
  if (currentSessionEpoch() !== epoch && get().user !== held) {
    set({ isLoggingOut: false });
    return;
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
}

/**
 * Applies the identity an auth response names. A response that names someone
 * other than the user held is a session changing hands without ending — a
 * refresh after another tab signed someone else in on the shared cookie, an SSO
 * callback over a live session, a resume whose cookie is no longer the stored
 * user's — and the new user would otherwise be served the previous one's cached
 * reads, console tabs and dialogs. So their state goes first, as if they had
 * signed out. The same user again (a refresh, a permission change) keeps theirs.
 *
 * Whoever begins here also starts with nothing on the toast screen. The reset
 * dismissed what was on it when the last session ended, but sonner hands every
 * Toaster that mounts each toast that exists and was never dismissed, and the
 * login pages have no Toaster of their own to show a toast raised since or to
 * clear it: the one AppShell mounts once `set` below has rendered it would show
 * it to the person who just signed in.
 *
 * And with the flag that holds a refresh back mid-logout down. One raised for
 * the identity before — by a logout, or the revoke of its current session, that
 * has not settled — means nothing for this one, and would hold back the
 * rehydration of every refresh they get until a reload. The same user again
 * keeps theirs: a flag up then is a sign-out they have begun.
 */
function adoptIdentity(
  set: SetAuth,
  res: AuthResponse,
  held: Pick<User, "id"> | null,
  extra: Partial<AuthState> = {},
): void {
  const begins = held === null || held.id !== res.user.id;
  if (held !== null && held.id !== res.user.id) forgetSession();
  if (begins) dismissToasts();
  set({
    user: res.user,
    permissions: res.permissions,
    isAuthenticated: true,
    signedOutByUser: false,
    ...(begins ? { isLoggingOut: false } : {}),
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
      // Called once per refusal, by the refresh the server refused, however many
      // requests were waiting on it (lib/api-client.ts). A request made while
      // nobody is signed in refreshes nothing, so it reports nothing, and no
      // report can clear a query its observer rebuilds, over and over
      // (stores/session-reset.ts).
      //
      // It can still arrive with no session to end: a request made before
      // initialize() below has settled, on a page that came up with a stored
      // user, tries the refresh cookie too, and the server may refuse it while
      // the resume is still out. Ending a session that is not there would wipe
      // the cache and every store for nobody, so only a store that is signed in
      // has one to end.
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

    // The page goes to them instead. What the stores rehydrated for the stored
    // user is theirs only if the stored user is who signed in: otherwise it is
    // forgotten first, here, because nothing else will — the login page does
    // not wait for isInitialized, and adoptIdentity saw nobody held (this has
    // not set the user yet), so it reset nothing, and a different user would
    // come out of the spinner with the stored user's console tabs and dismissed
    // issues. Nothing of THEIRS is lost by it: ProtectedRoute holds the
    // authenticated tree back until isInitialized, so nothing of theirs has been
    // written to a store yet.
    const releaseToTheNewSession = () => {
      // Nobody signed in is not the stored user either, whatever the ids say: a
      // stored user without one (a corrupt value) must not make the two equal.
      const who = get().user;
      if (who === null || who.id !== storedUser.id) forgetSession();
      set({ isLoading: false, isInitialized: true });
    };

    try {
      // Empty body — the HttpOnly refresh cookie carries the token. Sent the way
      // every refresh is (resumeSession, lib/api-client.ts): under the lock the
      // tabs of this browser take turns on, where there is one, and asked once
      // more if the server says another tab won the race for the cookie. And
      // asked AGAIN, a few times and while the spinner stays up (isInitialized
      // is false until this ends), when it could not look: a refresh that lost
      // that race twice, a 429, a 5xx, no network, an answer that is no session
      // (resumeSessionPatiently, which has the schedule and its reasons). Only a
      // cookie the server REFUSED ends the session at once, below; so does one
      // that is still failing after the last attempt.
      //
      // Several tabs restored at browser start, on plain HTTP where there is no
      // lock, resume on one single-use cookie at one instant, and the losers
      // meet exactly those failures. Ending the session on the first of them
      // took nexara_user, which every tab shares, out of localStorage, and the
      // next reload of every other tab came up at the login page.
      const res = await resumeSessionPatiently(epoch);
      if (currentSessionEpoch() !== epoch) {
        releaseToTheNewSession();
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
      // A session that began or ended while this was out — someone signed in
      // while it waited, which is also how the patience stops (a
      // StaleSessionError) — is theirs, and nothing here applies.
      if (currentSessionEpoch() !== epoch) {
        releaseToTheNewSession();
        return;
      }
      // What reaches here is the server's refusal, or the last attempt's
      // failure: the session is over, or cannot be told to be anything else.
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
    // The session this sign-out is for, which is all it may end (finishSignOut).
    const epoch = currentSessionEpoch();
    const held = get().user;
    // Whose cookie is in the jar decides whether there is anything to send. If
    // another tab has signed someone else in, nexara_user names them and the
    // cookie is theirs: this tab's own session was orphaned when theirs replaced
    // it, and is signed out here, and only here: nexara_user, which is theirs,
    // is left as it is (clearTokens), so that a reload of their tab still
    // resumes. A request would carry THEIR cookie, and /auth/logout revokes with
    // the cookie. A token this tab holds that is still valid lets the server see
    // that its owner is not the session's; an expired one it cannot check
    // (authOptional names the caller only for a token that validates), and the
    // cookie would be the one credential: Sign out here would end the other
    // user's session.
    if (!cookieIsAnotherUsers(held)) {
      // Empty body — the HttpOnly cookie carries the refresh token. Hit
      // /auth/logout so the server can revoke the session and clear the cookie.
      //
      // With the token held and no more: never refreshed first, nor waited for
      // under the cross-tab lock. The cookie is what this endpoint revokes with
      // (it is authOptional); the token only lets the server check that the
      // session is the caller's, which it skips for one that has expired. A
      // request that had to resolve a token first was never sent when the
      // refresh was failing — the user signed out here, and the session and its
      // cookie stayed alive on the server until the refresh token's lifetime ran
      // out.
      try {
        await signOutRequest();
      } catch {
        // Proceed with local cleanup even if server logout fails
      }
    }
    finishSignOut(set, get, epoch, held);
  },

  logoutAll: async () => {
    set({ isLoggingOut: true });
    const epoch = currentSessionEpoch();
    const held = get().user;
    try {
      await apiClient.post(apiPath`/api/v1/auth/logout-all`);
    } catch {
      // Proceed with local cleanup even if server call fails
    }
    finishSignOut(set, get, epoch, held);
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
