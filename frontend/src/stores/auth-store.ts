import { create } from "zustand";
import type {
  AuthResponse,
  LoginRequest,
  LogoutRequest,
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

  initialize: async () => {
    const storedUser = getStoredUser();
    if (!storedUser) {
      set({ isInitialized: true });
      return;
    }

    set({ isLoading: true });

    try {
      // Try refreshing to validate the session
      const res = await apiClient.postPublic<AuthResponse>(
        "/api/v1/auth/refresh",
        {
          refresh_token: localStorage.getItem("refresh_token"),
        },
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

    // Register callback for forced logout on auth failure
    setAuthFailureCallback(() => {
      get().clearAuth();
    });
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
    const refreshToken = localStorage.getItem("refresh_token");
    if (refreshToken) {
      try {
        const body: LogoutRequest = { refresh_token: refreshToken };
        await apiClient.post("/api/v1/auth/logout", body);
      } catch {
        // Proceed with local cleanup even if server logout fails
      }
    }
    clearTokens();
    set({
      user: null,
      permissions: [],
      isAuthenticated: false,
      totpPending: false,
      totpPendingToken: null,
    });
  },

  logoutAll: async () => {
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
