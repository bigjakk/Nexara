import { usePermissions } from "@/hooks/usePermissions";

/**
 * Whether the caller can read a History: it is the audit log, and its rows
 * open Proxmox task logs, so without both grants it would be a table of
 * refusals. HistoryButton and a guest's History tab hold the same rule. A
 * History of the cluster page takes the view permission of its tab or object
 * as well, named by `resource` — every one of them, when the object's rows
 * belong to several (a backup server's are the server's and the backups').
 */
export function useCanViewHistory(
  resource?: string | readonly string[],
): boolean {
  const { hasPermission } = usePermissions();
  const resources =
    resource === undefined
      ? []
      : typeof resource === "string"
        ? [resource]
        : resource;
  return (
    hasPermission("view", "audit") &&
    hasPermission("view", "task") &&
    resources.every((r) => hasPermission("view", r))
  );
}
