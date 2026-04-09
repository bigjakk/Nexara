/**
 * Mirrors `frontend/src/hooks/usePermissions.ts`. Reads RBAC permissions
 * from the auth store and provides convenience predicates for gating UI.
 *
 * Permissions are in `action:resource` format (e.g. "execute:vm",
 * "view:cluster"). The list is populated on login and refreshed on every
 * token refresh — see `persistAuthResponse` in `features/api/api-client.ts`.
 *
 * IMPORTANT: this is purely a UX gate. The backend re-checks every action,
 * so client-side bypassing only ever produces a 403 from the server.
 */

import { useCallback } from "react";

import { useAuthStore } from "@/stores/auth-store";

export function usePermissions() {
  const permissions = useAuthStore((s) => s.permissions);
  const user = useAuthStore((s) => s.user);

  const hasPermission = useCallback(
    (action: string, resource: string): boolean => {
      // Legacy admin fallback — matches the backend's `requirePerm` which
      // also falls back to `requireAdmin` when the RBAC engine is unavailable.
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
