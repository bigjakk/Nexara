import { AlertTriangle } from "lucide-react";

/** Shown above a ResourceTable when the owning cluster's inventory queries
 * are failing with no cached data — the table below it is empty until the
 * cluster is reachable again. (With cached data the stale rows keep
 * rendering and no note is shown.) */
export function InventoryUnavailableNote() {
  return (
    <p className="mb-4 flex items-center gap-2 text-sm text-amber-600 dark:text-amber-400">
      <AlertTriangle className="h-4 w-4 shrink-0" />
      Cluster inventory is currently unavailable — guests can&apos;t be listed
      right now.
    </p>
  );
}
