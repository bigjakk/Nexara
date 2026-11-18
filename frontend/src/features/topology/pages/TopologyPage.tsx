import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Network } from "lucide-react";
import { PageHeader } from "@/components/page/PageHeader";
import { NoClustersEmptyState } from "@/features/dashboard/components/NoClustersEmptyState";
import { useDashboardMetrics } from "@/hooks/useMetrics";
import { useRecentActivity } from "@/features/audit/api/audit-queries";
import { parseDetails } from "@/components/layout/task-status";
import { usePermissions } from "@/hooks/usePermissions";
import { useUpsDevices } from "@/features/power/api/ups-queries";
import type { UpsDevice } from "@/features/power/types/ups";
import { useTopologyData } from "../api/topology-queries";
import { TopologyCanvas } from "../components/TopologyCanvas";
import type { MigrationFlight } from "../components/TopologyCanvas";
import { TopologyControls } from "../components/TopologyControls";
import { TopologyLegend } from "../components/TopologyLegend";
import { topologyCounts } from "../lib/topology-summary";
import type { TopologyFilters } from "../lib/topology-transform";

const NO_UPS: UpsDevice[] = [];

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

  // The power overlay: offered to global view:ups holders, and fetched only
  // while it is on.
  const { hasGlobalPermission } = usePermissions();
  const canViewUps = hasGlobalPermission("view", "ups");
  const [showPower, setShowPower] = useState(false);
  const ups = useUpsDevices(canViewUps && showPower);
  const power = canViewUps && showPower ? (ups.data ?? NO_UPS) : null;

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

  const counts = useMemo(
    () => topologyCounts(input, filters.selectedClusterId),
    [input, filters.selectedClusterId],
  );

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
        {t("loadingTopology")}
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-destructive">
        {t("failedLoadTopology", { error: error.message })}
      </div>
    );
  }

  if (input.clusters.length === 0) {
    return (
      <div className="space-y-5">
        <PageHeader title={t("topology")} summary={t("topologyPurpose")} />
        <NoClustersEmptyState
          icon={Network}
          description={t("noClustersTopology")}
        />
      </div>
    );
  }

  return (
    // The page fills the shell's content box instead of flowing: main is a
    // sized flex item, so h-full resolves against it and the map stays inside
    // the window whatever the Activity panel's height. It adds no padding of
    // its own (the shell pads); the canvas takes that padding back on three
    // sides so the map runs to the edges.
    <div className="flex h-full min-h-[24rem] flex-col gap-4">
      <PageHeader
        title={t("topology")}
        summary={
          <>
            {[
              td("summaryClusterCount", { count: counts.clusters }),
              td("fleetNodeCount", { count: counts.nodes }),
              td("fleetGuestCount", { count: counts.guests }),
              t("summaryStorageCount", { count: counts.storage }),
              ...(migrations.length > 0
                ? [t("summaryMigrations", { count: migrations.length })]
                : []),
            ].join(", ")}
            {counts.nodesOffline > 0 && (
              <span className="ml-3 font-semibold text-red-700 dark:text-red-400">
                {td("summaryNodesOffline", { count: counts.nodesOffline })}
              </span>
            )}
          </>
        }
      />

      <div className="relative -mx-4 -mb-4 min-h-0 flex-1 border-t md:-mx-6 md:-mb-6">
        <TopologyCanvas
          input={input}
          filters={filters}
          direction={direction}
          liveMetrics={liveMetrics}
          migrations={migrations}
          power={power}
        />
        {/* Floating over the canvas: what it draws at the top, how to read
            it at the bottom. Each leaves the far corner to the zoom buttons
            and the minimap. */}
        <TopologyControls
          className="absolute left-3 top-3 max-w-[calc(100%-4.5rem)]"
          clusters={input.clusters}
          filters={filters}
          onFiltersChange={setFilters}
          direction={direction}
          onDirectionChange={setDirection}
          showPower={showPower}
          onShowPowerChange={canViewUps ? setShowPower : undefined}
        />
        <TopologyLegend
          className="absolute bottom-3 left-3 max-w-[calc(100%-1.5rem)] md:max-w-[calc(100%-15.5rem)]"
          power={power !== null}
          migrating={migrations.length > 0}
        />
      </div>
    </div>
  );
}
