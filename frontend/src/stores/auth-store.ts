import { create } from "zustand";
import type {
  AuthResponse,
  LoginRequest,
  LogoutRequest,
  RegisterRequest,
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
}

interface AuthActions {
  login: (req: LoginRequest) => Promise<void>;
  register: (req: RegisterRequest) => Promise<void>;
  logout: () => Promise<void>;
  logoutAll: () => Promise<void>;
  initialize: () => Promise<void>;
  clearAuth: () => void;
  setAuthFromResponse: (res: AuthResponse) => void;
}

export const useAuthStore = create<AuthState & AuthActions>()((set, get) => ({
  user: null,
  permissions: [],
  isAuthenticated: false,
  isLoading: false,
  isInitialized: false,

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
      const res = await apiClient.postPublic<AuthResponse>(
        "/api/v1/auth/login",
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
    });
  },

  clearAuth: () => {
    clearTokens();
    set({
      user: null,
      permissions: [],
      isAuthenticated: false,
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
