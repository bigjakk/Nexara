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
  getStoredUser,
  setAuthFailureCallback,
  setAuthRefreshCallback,
  storeTokens,
} from "@/lib/api-client";

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
}

interface AuthActions {
  login: (req: LoginRequest) => Promise<void>;
  register: (req: RegisterRequest) => Promise<void>;
  logout: () => Promise<void>;
  logoutAll: () => Promise<void>;
  initialize: () => Promise<void>;
  clearAuth: () => void;
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

export const useAuthStore = create<AuthState & AuthActions>()((set, get) => ({
  user: null,
  permissions: [],
  isAuthenticated: false,
  isLoading: false,
  isInitialized: false,
  totpPending: false,
  totpPendingToken: null,
  isLoggingOut: false,

  initialize: async () => {
    // Register callback for forced logout on auth failure first so any
    // refresh failure inside this method also routes through it.
    setAuthFailureCallback(() => {
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
      set({ isInitialized: true });
      return;
    }

    set({ isLoading: true });

    try {
      // Empty body — the HttpOnly refresh cookie carries the token.
      const res = await apiClient.postPublic<AuthResponse>(
        "/api/v1/auth/refresh",
        {},
      );
      storeTokens(res);
      set({
        user: res.user,
        permissions: res.permissions,
        isAuthenticated: true,
        isLoading: false,
        isInitialized: true,
      });
    } catch {
      clearTokens();
      set({
        user: null,
        permissions: [],
        isAuthenticated: false,
        isLoading: false,
        isInitialized: true,
      });
    }
  },

  login: async (req: LoginRequest) => {
    set({ isLoading: true });
    try {
      const res = await apiClient.postPublic<
        AuthResponse | TOTPRequiredResponse
      >("/api/v1/auth/login", req);

      if (isTotpRequired(res)) {
        set({
          isLoading: false,
          totpPending: true,
          totpPendingToken: res.totp_pending_token,
        });
        return;
      }

      storeTokens(res);
      set({
        user: res.user,
        permissions: res.permissions,
        isAuthenticated: true,
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
        "/api/v1/auth/totp/verify-login",
        body,
      );
      storeTokens(res);
      set({
        user: res.user,
        permissions: res.permissions,
        isAuthenticated: true,
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
        "/api/v1/auth/totp/verify-login",
        body,
      );
      storeTokens(res);
      set({
        user: res.user,
        permissions: res.permissions,
        isAuthenticated: true,
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
        "/api/v1/auth/register",
        req,
      );
      storeTokens(res);
      set({
        user: res.user,
        permissions: res.permissions,
        isAuthenticated: true,
        isLoading: false,
      });
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
      await apiClient.post("/api/v1/auth/logout", {});
    } catch {
      // Proceed with local cleanup even if server logout fails
    }
    clearTokens();
    set({
      user: null,
      permissions: [],
      isAuthenticated: false,
      totpPending: false,
      totpPendingToken: null,
      isLoggingOut: false,
    });
  },

  logoutAll: async () => {
    set({ isLoggingOut: true });
    try {
      await apiClient.post("/api/v1/auth/logout-all");
    } catch {
      // Proceed with local cleanup even if server call fails
    }
    clearTokens();
    set({
      user: null,
      permissions: [],
      isAuthenticated: false,
      totpPending: false,
      totpPendingToken: null,
      isLoggingOut: false,
    });
  },

  clearAuth: () => {
    clearTokens();
    set({
      user: null,
      permissions: [],
      isAuthenticated: false,
      totpPending: false,
      totpPendingToken: null,
      isLoggingOut: false,
    });
  },

  setAuthFromResponse: (res: AuthResponse) => {
    set({
      user: res.user,
      permissions: res.permissions,
      isAuthenticated: true,
      isLoading: false,
    });
  },
}));
