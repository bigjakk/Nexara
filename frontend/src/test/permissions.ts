import { useAuthStore } from "@/stores/auth-store";

/** What every guest action takes (guestActionPermission in features/vms/lib/vm-action-defs.tsx). */
export const GUEST_ACTION_PERMISSIONS = [
  "execute:vm",
  "execute:container",
  "manage:vm",
  "manage:container",
  "delete:vm",
  "delete:container",
  "manage:migration",
];

/** Signs in a user who holds exactly these permissions, install-wide. */
export function grantPermissions(...permissions: string[]): void {
  useAuthStore.setState({
    user: null,
    permissions,
    globalPermissions: permissions,
  });
}
