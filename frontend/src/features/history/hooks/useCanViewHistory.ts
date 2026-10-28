import { usePermissions } from "@/hooks/usePermissions";

/**
 * Whether the caller can read a History: it is the audit log, and its rows
 * open Proxmox task logs, so without both grants it would be a table of
 * refusals. HistoryButton and a guest's History tab hold the same rule. A
 * History of the cluster page takes the view permission of its tab or object
 * as well, named by `resource` — every one of them, when the object's rows
 * belong to several (a backup server's are the server's and the backups').
 *
 * `global` is for a History of an object that belongs to no one cluster (a
 * backup server, a firewall template): the server's route accepts only grants
 * held on the whole install, so a cluster-scoped holder is not offered it.
 * View:task only decides whether a task's exit text is shown, as anywhere.
 */
export function useCanViewHistory(
  resource?: string | readonly string[],
  global = false,
): boolean {
  const { hasPermission, hasGlobalPermission } = usePermissions();
  const resources =
    resource === undefined
      ? []
      : typeof resource === "string"
        ? [resource]
        : resource;
  const held = global ? hasGlobalPermission : hasPermission;
  return (
    held("view", "audit") &&
    hasPermission("view", "task") &&
    resources.every((r) => held("view", r))
  );
}
