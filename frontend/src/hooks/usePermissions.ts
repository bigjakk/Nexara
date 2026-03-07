import { useCallback } from "react";
import { useAuthStore } from "@/stores/auth-store";

export function usePermissions() {
  const permissions = useAuthStore((s) => s.permissions);
  const user = useAuthStore((s) => s.user);

  const hasPermission = useCallback(
    (action: string, resource: string): boolean => {
      // Legacy admin fallback
      if (user?.role === "admin") return true;
      return permissions.includes(`${action}:${resource}`);
    },
    [permissions, user?.role],
  );

  const canView = useCallback(
    (resource: string) => hasPermission("view", resource),
    [hasPermission],
  );

  const canManage = useCallback(
    (resource: string) => hasPermission("manage", resource),
    [hasPermission],
  );

  const canExecute = useCallback(
    (resource: string) => hasPermission("execute", resource),
    [hasPermission],
  );

  const canDelete = useCallback(
    (resource: string) => hasPermission("delete", resource),
    [hasPermission],
  );

  const isAdmin =
    user?.role === "admin" ||
    (hasPermission("manage", "user") && hasPermission("manage", "role"));

  return {
    permissions,
    hasPermission,
    canView,
    canManage,
    canExecute,
    canDelete,
    isAdmin,
  };
}
