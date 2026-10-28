import { usePermissions } from "@/hooks/usePermissions";
import { unaddressableHint } from "@/lib/api-path";
import type { HistoryDrawerRequest } from "@/stores/history-drawer-store";
import type { ClusterHistory } from "../lib/cluster-histories";
import { HistoryButton } from "./HistoryDrawer";

/**
 * A History button of the cluster page — a list row's, for an object, or a
 * tab's, beside its title — named by `title`. Shown to a caller who holds the
 * view permission of the object or tab as well (HistoryButton asks for the
 * audit log and the task log). Without a name a browser can send as a path
 * segment there is nothing to ask for, and the object's other buttons give way
 * to the same explanation (unaddressableHint): no button either.
 */
export function ClusterHistoryButton({
  history,
  title,
  rowsAbove,
}: {
  history: ClusterHistory;
  title: string;
  /** Live rows above the first page, as the drawer's (a Veeam job's runs). */
  rowsAbove?: HistoryDrawerRequest["rowsAbove"];
}) {
  const { hasPermission } = usePermissions();
  const permissions =
    typeof history.permission === "string"
      ? [history.permission]
      : history.permission;
  if (
    unaddressableHint(title) !== null ||
    !permissions.every((resource) => hasPermission("view", resource))
  ) {
    return null;
  }
  return (
    <HistoryButton
      kind={history.kind}
      target={history.target}
      title={title}
      rowsAbove={rowsAbove}
    />
  );
}
