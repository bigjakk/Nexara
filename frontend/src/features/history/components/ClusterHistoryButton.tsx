import { usePermissions } from "@/hooks/usePermissions";
import { unaddressableHint } from "@/lib/api-path";
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
}: {
  history: ClusterHistory;
  title: string;
}) {
  const { hasPermission } = usePermissions();
  if (
    unaddressableHint(title) !== null ||
    !hasPermission("view", history.permission)
  ) {
    return null;
  }
  return (
    <HistoryButton kind={history.kind} target={history.target} title={title} />
  );
}
