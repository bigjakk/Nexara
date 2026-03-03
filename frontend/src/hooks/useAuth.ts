import { useAuthStore } from "@/stores/auth-store";

export function useAuth() {
  const user = useAuthStore((s) => s.user);
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);
  const isLoading = useAuthStore((s) => s.isLoading);
  const isInitialized = useAuthStore((s) => s.isInitialized);
  const login = useAuthStore((s) => s.login);
  const register = useAuthStore((s) => s.register);
  const logout = useAuthStore((s) => s.logout);
  const logoutAll = useAuthStore((s) => s.logoutAll);

  return {
    user,
    isAuthenticated,
    isLoading,
    isInitialized,
    // UX hint only — all admin actions enforced server-side via requireAdmin() middleware
    isAdmin: user?.role === "admin",
    login,
    register,
    logout,
    logoutAll,
  };
}
