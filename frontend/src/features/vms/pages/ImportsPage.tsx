import { useEffect, useState } from "react";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { Label } from "@/components/ui/label";
import { ImportHistoryTable } from "../components/ImportHistoryTable";
import { ImportSourcesManager } from "../components/ImportSourcesManager";

const selectClass =
  "flex h-9 w-64 rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring";

export function ImportsPage() {
  const { data: clusters } = useClusters();
  const [clusterId, setClusterId] = useState("");

  useEffect(() => {
    if (!clusterId && clusters && clusters.length > 0 && clusters[0]) {
      setClusterId(clusters[0].id);
    }
  }, [clusters, clusterId]);

  return (
    <div className="space-y-4 p-6">
      <div>
        <h1 className="text-xl font-semibold">VM Imports</h1>
        <p className="text-sm text-muted-foreground">
          History of guests imported from OVA/OVF appliances, disk images, and ESXi/vCenter hosts.
        </p>
      </div>

      {clusters && clusters.length > 1 && (
        <div className="space-y-1">
          <Label>Cluster</Label>
          <select
            className={selectClass}
            value={clusterId}
            onChange={(e) => { setClusterId(e.target.value); }}
          >
            {clusters.map((c) => (
              <option key={c.id} value={c.id}>{c.name}</option>
            ))}
          </select>
        </div>
      )}

      {clusterId && <ImportSourcesManager clusterId={clusterId} />}

      {clusterId && (
        <div className="space-y-2">
          <h2 className="text-sm font-semibold">History</h2>
          <ImportHistoryTable clusterId={clusterId} />
        </div>
      )}
    </div>
  );
}
