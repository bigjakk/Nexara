/**
 * Auth store — tracks the current auth state machine.
 *
 * States:
 *   - loading      — initial load, checking SecureStore
 *   - unconfigured — no server URL set yet, need onboarding
 *   - logged_out   — server URL set but no valid session
 *   - totp_pending — credentials accepted, awaiting TOTP code
 *   - locked       — session exists but biometric unlock required
 *   - authed       — fully authenticated
 */

import { create } from "zustand";

import {
  apiLogin,
  apiLogout,
  apiVerifyTOTP,
  configureApiClient,
  persistAuthResponse,
} from "@/features/api/api-client";
import { isTOTPRequired, type AuthUser } from "@/features/api/types";
import { secureStorage } from "@/lib/secure-storage";

export type AuthStatus =
  | "loading"
  | "unconfigured"
  | "logged_out"
  | "totp_pending"
  | "locked"
  | "authed";

interface AuthState {
  status: AuthStatus;
  user: AuthUser | null;
  serverUrl: string | null;
  /**
   * RBAC permissions in `action:resource` format (e.g. "execute:vm",
   * "view:cluster"). Refreshed on every login / TOTP verify / token
   * refresh, restored from SecureStore on bootstrap, cleared on logout.
   * Used by `usePermissions()` to gate UI affordances client-side. The
   * backend re-checks every action — this is purely UX.
   */
  permissions: string[];
  totpPendingToken: string | null;
  error: string | null;

  // State transitions
  bootstrap: () => Promise<void>;
  setServerUrl: (url: string) => Promise<void>;
  login: (email: string, password: string) => Promise<void>;
  verifyTotp: (code: string) => Promise<void>;
  clearTotpPending: () => void;
  unlock: () => void;
  lock: () => void;
  logout: () => Promise<void>;
  changeServer: () => Promise<void>;
  setError: (error: string | null) => void;
}

export const useAuthStore = create<AuthState>((set, get) => ({
  status: "loading",
  user: null,
  serverUrl: null,
  permissions: [],
  totpPendingToken: null,
  error: null,

  /**
   * Runs once on app start. Decides the initial screen.
   */
  bootstrap: async () => {
    try {
      const serverUrl = await secureStorage.getServerUrl();
      if (!serverUrl) {
        set({ status: "unconfigured" });
        return;
      }

      await configureApiClient(serverUrl);
      // Seed the MRU list on every launch. Handles migration for users
      // who set a server URL before the recents feature existed, and
      // keeps the current URL at the head of the list as expected.
      await secureStorage.addRecentServerUrl(serverUrl);

      const [refreshToken, user, permissions] = await Promise.all([
        secureStorage.getRefreshToken(),
        secureStorage.getUser(),
        secureStorage.getPermissions(),
      ]);

      if (!refreshToken || !user) {
        set({ status: "logged_out", serverUrl, permissions: [] });
        return;
      }

      const biometricEnrolled = await secureStorage.isBiometricEnrolled();
      set({
        status: biometricEnrolled ? "locked" : "authed",
        serverUrl,
        user: {
          id: user.id,
          email: user.email,
          display_name: user.displayName,
          role: user.role,
        },
        permissions,
      });
    } catch (err) {
      const message = err instanceof Error ? err.message : "Bootstrap failed";
      set({ status: "logged_out", error: message });
    }
  },

  setServerUrl: async (url: string) => {
    const cleaned = url.trim().replace(/\/$/, "");
    if (!/^https?:\/\//.test(cleaned)) {
      throw new Error("Server URL must start with http:// or https://");
    }
    // Note: http:// is accepted here but the UI layer (server-url.tsx,
    // login.tsx) shows a strong warning dialog before calling this action
    // so the user can see the TLS risk and explicitly acknowledge it. We
    // don't hard-block plain HTTP because plenty of self-hosted dev/lab
    // setups legitimately run without a reverse-proxied public cert.
    await configureApiClient(cleaned);
    // Record this URL in the MRU list so it shows up in the server-URL
    // picker next time. Runs after configureApiClient succeeds — we don't
    // want to clutter the list with URLs we couldn't even construct a
    // client for. (Unreachable-but-well-formed URLs still get added; the
    // user can delete them from the picker.)
    await secureStorage.addRecentServerUrl(cleaned);
    set({ serverUrl: cleaned, status: "logged_out", error: null });
  },

  login: async (email: string, password: string) => {
    try {
      const result = await apiLogin(email, password);

      if (isTOTPRequired(result)) {
        set({
          status: "totp_pending",
          totpPendingToken: result.totp_pending_token,
          error: null,
        });
        return;
      }

      await persistAuthResponse(result);
      set({
        status: "authed",
        user: result.user,
        permissions: result.permissions,
        totpPendingToken: null,
        error: null,
      });
    } catch (err) {
      const message = loginErrorMessage(err);
      set({ error: message });
      throw err;
    }
  },

  verifyTotp: async (code: string) => {
    const token = get().totpPendingToken;
    if (!token) {
      throw new Error("No TOTP pending");
    }
    try {
      const result = await apiVerifyTOTP(token, code);
      await persistAuthResponse(result);
      set({
        status: "authed",
        user: result.user,
        permissions: result.permissions,
        totpPendingToken: null,
        error: null,
      });
    } catch (err) {
      const message = loginErrorMessage(err);
      set({ error: message });
      throw err;
    }
  },

  clearTotpPending: () => {
    set({ status: "logged_out", totpPendingToken: null, error: null });
  },

  unlock: () => {
    if (get().status === "locked") {
      set({ status: "authed" });
    }
  },

  lock: () => {
    if (get().status === "authed") {
      set({ status: "locked" });
    }
  },

  logout: async () => {
    try {
      await apiLogout();
    } finally {
      set({
        status: "logged_out",
        user: null,
        permissions: [],
        totpPendingToken: null,
        error: null,
      });
    }
  },

  /**
   * Sign out and send the user back to the server-URL prompt so they can
   * point the app at a different Nexara instance.
   *
   * Intentionally does NOT delete `nx.server_url` from SecureStore — the
   * server-URL screen reads that value on mount and prefills the input so
   * the user can edit rather than retype. Once they tap Continue the
   * normal setServerUrl() flow overwrites it with the new value.
   *
   * Edge case: if the user force-quits the app while sitting on the
   * server-URL prompt, next launch will read the old URL back from
   * SecureStore and bootstrap straight to the login screen. They can
   * re-tap "Change server" in Settings to try again. Accepted trade-off
   * in exchange for the "prefill with current URL" convenience.
   */
  changeServer: async () => {
    try {
      await apiLogout();
    } finally {
      set({
        status: "unconfigured",
        user: null,
        serverUrl: null,
        permissions: [],
        totpPendingToken: null,
        error: null,
      });
    }
  },

  setError: (error: string | null) => set({ error }),
}));

// Cap server-supplied error messages so a misbehaving (or compromised)
// backend can't surface a multi-megabyte payload into our UI text node
// or the diagnostics log buffer (security review L6, defensive).
const MAX_LOGIN_ERROR_LENGTH = 200;

function clampMessage(msg: string): string {
  if (msg.length <= MAX_LOGIN_ERROR_LENGTH) return msg;
  return msg.slice(0, MAX_LOGIN_ERROR_LENGTH) + "…";
}

function loginErrorMessage(err: unknown): string {
  if (typeof err === "object" && err !== null && "response" in err) {
    const resp = (err as { response?: { data?: { message?: string } } })
      .response;
    if (resp?.data?.message) return clampMessage(resp.data.message);
  }
  if (err instanceof Error) return clampMessage(err.message);
  return "Login failed";
}
