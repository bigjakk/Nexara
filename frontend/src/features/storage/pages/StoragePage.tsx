import { useCallback, useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { HardDrive } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Segmented } from "@/components/ui/segmented";
import { Skeleton } from "@/components/ui/skeleton";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { NoClustersEmptyState } from "@/features/dashboard/components/NoClustersEmptyState";
import { formatBytes, formatPercent } from "@/lib/format";
import { AddStorageDialog } from "../components/AddStorageDialog";
import {
  ClusterStoresPanel,
  type ClusterReport,
} from "../components/ClusterStoresPanel";
import { storageSummary, storageTotals } from "../lib/store-rows";

export function StoragePage() {
  const { t: td } = useTranslation("dashboard");
  const [searchParams] = useSearchParams();
  const clustersQuery = useClusters();
  const clusters = clustersQuery.data ?? [];
  const clusterParam = searchParams.get("cluster") ?? "";
  const [selectedClusterId, setSelectedClusterId] =
    useState<string>(clusterParam);

  // Sync with URL query param when it changes (e.g. navigating from search)
  useEffect(() => {
    if (clusterParam) {
      setSelectedClusterId(clusterParam);
    }
  }, [clusterParam]);

  // No cluster picked lists every cluster.
  const selectedId = clusters.some((c) => c.id === selectedClusterId)
    ? selectedClusterId
    : "";
  const shown =
    selectedId === "" ? clusters : clusters.filter((c) => c.id === selectedId);
  const [only] = shown;

  // Each cluster's panel reads its own stores, with the hook every other
  // reader of them uses, and tells the page what it has: the header and the
  // strip sum the panels in view.
  const [reports, setReports] = useState<Record<string, ClusterReport>>({});
  const report = useCallback((clusterId: string, next: ClusterReport) => {
    setReports((prev) => ({ ...prev, [clusterId]: next }));
  }, []);

  const loading = shown.some((c) => reports[c.id]?.isLoading ?? true);
  // A cluster that never answered has no stores to count, and is not one of
  // the clusters the stores are "on".
  const unread = shown.filter((c) => reports[c.id]?.unread === true).length;
  const totals = storageTotals(shown.flatMap((c) => reports[c.id]?.rows ?? []));
  const bytes = (n: number) => (loading ? "—" : formatBytes(n));

  return (
    <div className="space-y-5">
      <PageHeader
        title="Storage"
        summary={
          // Not while a cluster is still being read: the count would be short.
          (loading
            ? undefined
            : storageSummary(totals, shown.length - unread, unread)) ??
          "The stores on your clusters, what they hold and how full they are."
        }
        actions={
          <>
            {clusters.length > 1 && (
              <Segmented
                aria-label="Cluster"
                size="md"
                value={selectedId}
                onValueChange={setSelectedClusterId}
                options={[
                  { value: "", label: "All clusters" },
                  ...clusters.map((c) => ({ value: c.id, label: c.name })),
                ]}
              />
            )}
            {/* The page's one primary button, while it is plain which
                cluster it adds to. With several in view each panel has its
                own. */}
            {only !== undefined && shown.length === 1 && (
              <AddStorageDialog clusterId={only.id} />
            )}
          </>
        }
      />

      {clustersQuery.isLoading && <Skeleton className="h-28 w-full" />}

      {clustersQuery.isError && (
        <p className="text-sm text-destructive">Failed to load clusters.</p>
      )}

      {clustersQuery.isSuccess && clusters.length === 0 && (
        <NoClustersEmptyState
          icon={HardDrive}
          description={td("addClusterToGetStarted")}
        />
      )}

      {shown.length > 0 && (
        <>
          <Panel flush bodyClassName="py-4">
            <StatStrip>
              <Stat
                label="Capacity"
                value={bytes(totals.total)}
                sub={
                  loading
                    ? undefined
                    : `${String(totals.stores)} ${totals.stores === 1 ? "store" : "stores"}`
                }
              />
              <Stat
                label="Used"
                value={bytes(totals.used)}
                sub={
                  loading || totals.percent === null
                    ? undefined
                    : `${formatPercent(totals.percent)} of capacity`
                }
                percent={loading ? null : totals.percent}
              />
              <Stat
                label="Free"
                value={bytes(totals.free)}
                sub={
                  !loading && totals.total > 0
                    ? `${formatPercent((totals.free / totals.total) * 100)} of capacity`
                    : undefined
                }
              />
              <Stat
                label="Near full"
                value={loading ? "—" : String(totals.nearFull)}
                tone={
                  loading
                    ? "quiet"
                    : totals.full > 0
                      ? "bad"
                      : totals.nearFull > 0
                        ? "warn"
                        : "quiet"
                }
                sub={
                  !loading && totals.full > 0
                    ? `${String(totals.full)} at 90% or more`
                    : "75% used or more"
                }
              />
            </StatStrip>
          </Panel>

          {/* With several clusters in view each panel says its own totals
              and carries its own Add storage, which the header does for one. */}
          {shown.map((cluster) => (
            <ClusterStoresPanel
              key={cluster.id}
              clusterId={cluster.id}
              title={cluster.name}
              showTotals={shown.length > 1}
              actions={
                shown.length > 1 ? (
                  <AddStorageDialog
                    clusterId={cluster.id}
                    clusterName={cluster.name}
                    triggerVariant="outline"
                  />
                ) : undefined
              }
              onReport={report}
            />
          ))}
        </>
      )}
    </div>
  );
}
