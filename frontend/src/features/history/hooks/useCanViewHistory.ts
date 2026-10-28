import { usePermissions } from "@/hooks/usePermissions";

/**
 * Whether the caller can read a History: it is the audit log, and its rows
 * open Proxmox task logs, so without both grants it would be a table of
 * refusals. HistoryButton and a guest's History tab hold the same rule. A
 * History of the cluster page takes the view permission of its tab or object
 * as well, named by `resource`.
 */
export function useCanViewHistory(resource?: string): boolean {
  const { hasPermission } = usePermissions();
  return (
    hasPermission("view", "audit") &&
    hasPermission("view", "task") &&
    (resource === undefined || hasPermission("view", resource))
  );
}
