import { usePermissions } from "@/hooks/usePermissions";

/**
 * Whether the caller can read a History: it is the audit log, and its rows
 * open Proxmox task logs, so without both grants it would be a table of
 * refusals. HistoryButton and a guest's History tab hold the same rule.
 */
export function useCanViewHistory(): boolean {
  const { hasPermission } = usePermissions();
  return hasPermission("view", "audit") && hasPermission("view", "task");
}
