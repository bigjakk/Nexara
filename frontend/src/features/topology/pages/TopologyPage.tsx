import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Network } from "lucide-react";
import { EmptyState } from "@/components/EmptyState";
import { AddClusterDialog } from "@/features/dashboard/components/AddClusterDialog";
import { useDashboardMetrics } from "@/hooks/useMetrics";
import { useRecentActivity } from "@/features/audit/api/audit-queries";
import { parseDetails } from "@/components/layout/task-status";
import { useTopologyData } from "../api/topology-queries";
import { TopologyCanvas } from "../components/TopologyCanvas";
import type { MigrationFlight } from "../components/TopologyCanvas";
import { TopologyControls } from "../components/TopologyControls";
import { TopologyLegend } from "../components/TopologyLegend";
import type { TopologyFilters } from "../lib/topology-transform";

export function TopologyPage() {
  const { t } = useTranslation("topology");
  const { t: td } = useTranslation("dashboard");
  const { input, isLoading, error } = useTopologyData();

  const [filters, setFilters] = useState<TopologyFilters>({
    showVMs: false,
    showStorage: true,
    selectedClusterId: null,
  });

  const [direction, setDirection] = useState<"TB" | "LR">("TB");

  const clusterIds = useMemo(
    () => input.clusters.map((c) => c.id),
    [input.clusters],
  );
  const liveMetrics = useDashboardMetrics(clusterIds);

  // Running migrate tasks (from the shared activity cache, WS-invalidated)
  // become in-flight edges between source and target host.
  const activityQuery = useRecentActivity();
  const migrations = useMemo<MigrationFlight[]>(() => {
    const out: MigrationFlight[] = [];
    for (const entry of activityQuery.data ?? []) {
      if (entry.task_status !== "running") continue;
      if (!entry.action.toLowerCase().includes("migrate")) continue;
      if (!entry.cluster_id) continue;
      const details = parseDetails(entry.details);
      // Key names vary by producer: the migration orchestrator and DRS write
      // source_node/target_node, the direct migrate handler writes node/target.
      const source =
        (typeof details["source_node"] === "string"
          ? details["source_node"]
          : null) ?? details.node;
      const target =
        (typeof details["target_node"] === "string"
          ? details["target_node"]
          : null) ??
        (typeof details["target"] === "string" ? details["target"] : null);
      if (!source || !target) continue;
      out.push({
        id: entry.id,
        clusterId: entry.cluster_id,
        vmid: entry.resource_vmid,
        name: entry.resource_name,
        sourceName: source,
        targetName: target,
      });
    }
    return out;
  }, [activityQuery.data]);

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center">
        <div className="text-muted-foreground">{t("loadingTopology")}</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex h-full items-center justify-center">
        <div className="text-destructive">
          {t("failedLoadTopology", { error: error.message })}
        </div>
      </div>
    );
  }

  if (input.clusters.length === 0) {
    return (
      <div className="flex h-full items-center justify-center p-6">
        <EmptyState
          icon={Network}
          title={td("noClustersRegistered")}
          description={t("noClustersTopology")}
          action={<AddClusterDialog />}
        />
      </div>
    );
  }

  return (
    <div className="flex h-[calc(100vh-5rem)] flex-col gap-3 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold tracking-tight">{t("topology")}</h1>
        <TopologyLegend />
      </div>

      <TopologyControls
        clusters={input.clusters}
        filters={filters}
        onFiltersChange={setFilters}
        direction={direction}
        onDirectionChange={setDirection}
      />

      <div className="min-h-0 flex-1">
        <TopologyCanvas
          input={input}
          filters={filters}
          direction={direction}
          liveMetrics={liveMetrics}
          migrations={migrations}
        />
      </div>
    </div>
  );
}
