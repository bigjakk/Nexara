import { useCallback } from "react";
import { useAuthStore } from "@/stores/auth-store";

export function usePermissions() {
  const permissions = useAuthStore((s) => s.permissions);
  const globalPermissions = useAuthStore((s) => s.globalPermissions);
  const user = useAuthStore((s) => s.user);

  const hasPermission = useCallback(
    (action: string, resource: string): boolean => {
      // Legacy admin fallback
      if (user?.role === "admin") return true;
      return permissions.includes(`${action}:${resource}`);
    },
    [permissions, user?.role],
  );

  // A grant on the whole install, which a global gate on the server accepts;
  // a cluster-scoped one is in `permissions` but not here.
  const hasGlobalPermission = useCallback(
    (action: string, resource: string): boolean => {
      if (user?.role === "admin") return true;
      return globalPermissions.includes(`${action}:${resource}`);
    },
    [globalPermissions, user?.role],
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

  // Console access is its own permission family ("console:node" etc.) —
  // deliberately not implied by view:* so read-only roles can't open shells.
  const canConsole = useCallback(
    (resource: string) => hasPermission("console", resource),
    [hasPermission],
  );

  const isAdmin =
    user?.role === "admin" ||
    (hasPermission("manage", "user") && hasPermission("manage", "role"));

  return {
    permissions,
    hasPermission,
    hasGlobalPermission,
    canView,
    canManage,
    canExecute,
    canDelete,
    canConsole,
    isAdmin,
  };
}
