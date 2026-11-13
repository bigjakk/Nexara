import { useEffect, useMemo, type ReactNode } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { StatusIcon } from "@/components/StatusIcon";
import { Panel } from "@/components/page/Panel";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";
import { useClusterStorage } from "../api/storage-queries";
import { StorageCapacityBar } from "./StorageCapacityBar";
import {
  contentList,
  STATE_ICON,
  STATE_LABEL,
  storageTotals,
  storageTypeLabel,
  storeRows,
  storeState,
  type StoreRow,
  type StoreState,
} from "../lib/store-rows";

/** What one cluster's panel has read, as the Storage page's header sums it. */
export interface ClusterReport {
  rows: StoreRow[];
  isLoading: boolean;
  /** The cluster's stores could not be read at all: there is nothing cached. */
  unread: boolean;
}

interface ClusterStoresPanelProps {
  clusterId: string;
  /** The cluster's name on the Storage page; on the cluster's own page, "Storage". */
  title: ReactNode;
  /** Say the panel's own store count and usage beside its title. */
  showTotals: boolean;
  /** Controls at the right of the title row, such as Add storage. */
  actions?: ReactNode;
  /** For a page that sums several panels: what this one has read. */
  onReport?: (clusterId: string, report: ClusterReport) => void;
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

/** One cluster's stores, each row opening the store's page. */
export function ClusterStoresPanel({
  clusterId,
  title,
  showTotals,
  actions,
  onReport,
}: ClusterStoresPanelProps) {
  const navigate = useNavigate();
  const storageQuery = useClusterStorage(clusterId);
  const nodesQuery = useClusterNodes(clusterId);
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
    onReport?.(clusterId, { rows, isLoading, unread });
  }, [clusterId, rows, isLoading, unread, onReport]);

  return (
    <Panel
      flush
      title={title}
      aside={
        isLoading
          ? "Loading..."
          : showTotals && rows.length > 0
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
      actions={actions}
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
                  const path = `/storage/${clusterId}/${pool.id}`;
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
                            to={`/clusters/${clusterId}/nodes/${pool.node_id}`}
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
