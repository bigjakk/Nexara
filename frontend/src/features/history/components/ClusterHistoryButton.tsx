import { unaddressableHint } from "@/lib/api-path";
import type { ClusterHistory } from "../lib/cluster-histories";
import { HistoryButton } from "./HistoryDrawer";

/**
 * A list row's History button for an object of the cluster page, named by
 * `title`. Without a name a browser can send as a path segment there is
 * nothing to ask for, and the object's other buttons give way to the same
 * explanation (unaddressableHint): no button either.
 */
export function ClusterHistoryButton({
  history,
  title,
}: {
  history: ClusterHistory;
  title: string;
}) {
  if (unaddressableHint(title) !== null) return null;
  return (
    <HistoryButton kind={history.kind} target={history.target} title={title} />
  );
}
