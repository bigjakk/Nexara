import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { HardDrive } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Segmented } from "@/components/ui/segmented";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { EmptyState } from "@/components/EmptyState";
import { StatusIcon } from "@/components/StatusIcon";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { AddClusterDialog } from "@/features/dashboard/components/AddClusterDialog";
import { formatBytes, formatPercent } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { ClusterResponse } from "@/types/api";
import { useClusterStorage } from "../api/storage-queries";
import { AddStorageDialog } from "../components/AddStorageDialog";
import { StorageCapacityBar } from "../components/StorageCapacityBar";
import {
  contentList,
  STATE_ICON,
  STATE_LABEL,
  storageSummary,
  storageTotals,
  storageTypeLabel,
  storeRows,
  storeState,
  type StoreRow,
  type StoreState,
} from "../lib/store-rows";

/** What one cluster's panel has read, as the page's header sums it. */
interface ClusterReport {
  rows: StoreRow[];
  isLoading: boolean;
  /** The cluster's stores could not be read at all: there is nothing cached. */
  unread: boolean;
}

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
        <EmptyState
          icon={HardDrive}
          title={td("noClustersRegistered")}
          description={td("addClusterToGetStarted")}
          action={<AddClusterDialog />}
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

          {shown.map((cluster) => (
            <ClusterStores
              key={cluster.id}
              cluster={cluster}
              several={shown.length > 1}
              onReport={report}
            />
          ))}
        </>
      )}
    </div>
  );
}

function StoreStateCell({ state }: { state: StoreState }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-[7px] whitespace-nowrap text-muted-foreground",
        state === "inactive" && "font-medium text-red-700 dark:text-red-400",
      )}
    >
      {/* The word beside it is what a screen reader gets; a phone has room
          for the dot alone, and says a problem under the store's name. */}
      <span aria-hidden="true" className="flex">
        <StatusIcon status={STATE_ICON[state]} />
      </span>
      <span className="sr-only sm:not-sr-only">{STATE_LABEL[state]}</span>
    </span>
  );
}

function ClusterStores({
  cluster,
  several,
  onReport,
}: {
  cluster: ClusterResponse;
  /**
   * More than one cluster is in view: the panel then says its own totals and
   * carries its own Add storage, which the page's header does for one.
   */
  several: boolean;
  onReport: (clusterId: string, report: ClusterReport) => void;
}) {
  const navigate = useNavigate();
  const storageQuery = useClusterStorage(cluster.id);
  const nodesQuery = useClusterNodes(cluster.id);
  const { isLoading, isError } = storageQuery;
  // A refresh that fails keeps the stores last read, and they stay listed
  // and counted. Only a cluster that never answered has nothing to show.
  const unread = isError && storageQuery.data === undefined;
  const stale = isError && storageQuery.data !== undefined;
  const rows = useMemo(
    () =>
      storeRows(
        storageQuery.data ?? [],
        new Map((nodesQuery.data ?? []).map((node) => [node.id, node.name])),
      ),
    [storageQuery.data, nodesQuery.data],
  );
  const totals = storageTotals(rows);

  useEffect(() => {
    onReport(cluster.id, { rows, isLoading, unread });
  }, [cluster.id, rows, isLoading, unread, onReport]);

  return (
    <Panel
      flush
      title={cluster.name}
      aside={
        isLoading
          ? "Loading..."
          : several && rows.length > 0
            ? [
                `${String(rows.length)} ${rows.length === 1 ? "store" : "stores"}`,
                ...(totals.total > 0
                  ? [
                      `${formatBytes(totals.used)} of ${formatBytes(totals.total)} used`,
                    ]
                  : []),
              ].join(", ")
            : undefined
      }
      actions={
        several ? (
          <AddStorageDialog
            clusterId={cluster.id}
            clusterName={cluster.name}
            triggerVariant="outline"
          />
        ) : undefined
      }
    >
      {isLoading ? (
        <div className="space-y-2 px-[18px] pb-1">
          {Array.from({ length: 3 }, (_, i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </div>
      ) : unread ? (
        <p className="px-[18px] pb-1 text-sm text-destructive">
          Failed to load this cluster&apos;s storage.
        </p>
      ) : (
        <>
          {stale && (
            <p className="px-[18px] pb-2 text-[12.5px] text-muted-foreground">
              Could not refresh this cluster&apos;s storage. Showing what was
              last read.
            </p>
          )}
          {rows.length === 0 ? (
            <p className="px-[18px] pb-1 text-sm text-muted-foreground">
              This cluster has no storage configured. Add storage to see it
              here.
            </p>
          ) : (
            // Fixed widths, so the columns line up from one cluster's panel to
            // the next.
            <Table className="table-fixed">
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="w-[38%] sm:w-[22%]">Store</TableHead>
                  <TableHead className="hidden w-[11%] sm:table-cell">
                    Where
                  </TableHead>
                  <TableHead className="hidden lg:table-cell">Holds</TableHead>
                  <TableHead className="lg:w-[27%]">Used</TableHead>
                  <TableHead className="w-10 sm:w-[6.5rem]">
                    <span className="sr-only sm:not-sr-only">State</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map(({ pool, nodeName }) => {
                  const path = `/storage/${cluster.id}/${pool.id}`;
                  const state = storeState(pool);
                  const where = pool.shared
                    ? "shared"
                    : nodeName !== ""
                      ? nodeName
                      : "local";
                  return (
                    <TableRow
                      key={pool.id}
                      className="cursor-pointer"
                      onClick={() => {
                        void navigate(path);
                      }}
                    >
                      <TableCell>
                        <Link
                          to={path}
                          className="font-medium [overflow-wrap:anywhere] hover:underline"
                          onClick={(e) => {
                            e.stopPropagation();
                          }}
                        >
                          {pool.storage}
                        </Link>
                        <div className="text-[12.5px] text-muted-foreground">
                          {storageTypeLabel(pool.type)}
                          <span className="sm:hidden">
                            , {where === nodeName ? `on ${where}` : where}
                            {state !== "active" &&
                              `, ${STATE_LABEL[state].toLowerCase()}`}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell className="hidden text-muted-foreground sm:table-cell">
                        {pool.shared ? (
                          "Shared"
                        ) : nodeName === "" ? (
                          "Local"
                        ) : (
                          <Link
                            to={`/clusters/${cluster.id}/nodes/${pool.node_id}`}
                            className="hover:text-foreground hover:underline"
                            onClick={(e) => {
                              e.stopPropagation();
                            }}
                          >
                            {nodeName}
                          </Link>
                        )}
                      </TableCell>
                      <TableCell className="hidden text-muted-foreground lg:table-cell">
                        {contentList(pool.content) || "Nothing"}
                      </TableCell>
                      <TableCell>
                        <StorageCapacityBar
                          used={pool.used}
                          total={pool.total}
                        />
                      </TableCell>
                      <TableCell>
                        <StoreStateCell state={state} />
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </>
      )}
    </Panel>
  );
}
