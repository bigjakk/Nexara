import { useAuthStore } from "@/stores/auth-store";
import { usePermissions } from "@/hooks/usePermissions";

export function useAuth() {
  const user = useAuthStore((s) => s.user);
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);
  const isLoading = useAuthStore((s) => s.isLoading);
  const isInitialized = useAuthStore((s) => s.isInitialized);
  const login = useAuthStore((s) => s.login);
  const register = useAuthStore((s) => s.register);
  const logout = useAuthStore((s) => s.logout);
  const logoutAll = useAuthStore((s) => s.logoutAll);
  const { isAdmin, hasPermission, canView, canManage, canExecute, canDelete } =
    usePermissions();

  return {
    user,
    isAuthenticated,
    isLoading,
    isInitialized,
    // RBAC-aware admin check — UX hint, server enforces all permissions
    isAdmin,
    hasPermission,
    canView,
    canManage,
    canExecute,
    canDelete,
    login,
    register,
    logout,
    logoutAll,
  };
}
