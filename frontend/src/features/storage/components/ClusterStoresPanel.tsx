import { Fragment, useEffect, useMemo, useState, type ReactNode } from "react";
import { Link, useNavigate } from "react-router-dom";
import { ChevronRight } from "lucide-react";
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
  storeGroups,
  storeRows,
  storeState,
  type StoreGroup,
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

/** ", inactive" for a phone's line under the name; nothing when all is well. */
function stateNote(state: StoreState): string {
  return state === "active" ? "" : `, ${STATE_LABEL[state].toLowerCase()}`;
}

/** One cluster's stores, each storage definition once, each row opening its store's page. */
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
  const groups = useMemo(() => storeGroups(rows), [rows]);
  const totals = storageTotals(rows);
  // The storage ids whose node rows are open.
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set());
  const toggle = (storage: string) => {
    setOpen((prev) => {
      const next = new Set(prev);
      if (!next.delete(storage)) next.add(storage);
      return next;
    });
  };

  useEffect(() => {
    onReport?.(clusterId, { rows, isLoading, unread });
  }, [clusterId, rows, isLoading, unread, onReport]);

  const storePath = (row: StoreRow) => `/storage/${clusterId}/${row.pool.id}`;

  /** A store that is one row: shared, or local to a single node. */
  const singleRow = ({ lead: row, state }: StoreGroup) => {
    const path = storePath(row);
    const where = row.pool.shared
      ? "shared"
      : row.nodeName !== ""
        ? `on ${row.nodeName}`
        : "local";
    return (
      <TableRow
        key={row.pool.id}
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
            {row.pool.storage}
          </Link>
          <div className="text-[12.5px] text-muted-foreground">
            {storageTypeLabel(row.pool.type)}
            <span className="sm:hidden">
              , {where}
              {stateNote(state)}
            </span>
          </div>
        </TableCell>
        <TableCell className="hidden text-muted-foreground sm:table-cell">
          {row.pool.shared ? (
            "Shared"
          ) : row.nodeName === "" ? (
            "Local"
          ) : (
            <Link
              to={`/clusters/${clusterId}/nodes/${row.pool.node_id}`}
              className="hover:text-foreground hover:underline"
              onClick={(e) => {
                e.stopPropagation();
              }}
            >
              {row.nodeName}
            </Link>
          )}
        </TableCell>
        <TableCell className="hidden text-muted-foreground lg:table-cell">
          {contentList(row.pool.content) || "Nothing"}
        </TableCell>
        <TableCell>
          <StorageCapacityBar used={row.pool.used} total={row.pool.total} />
        </TableCell>
        <TableCell>
          <StoreStateCell state={state} />
        </TableCell>
      </TableRow>
    );
  };

  /**
   * A local store on several nodes: one row, standing for its fullest node,
   * that opens to a row per node.
   */
  const nodeRows = (group: StoreGroup) => {
    const { lead, state } = group;
    const storage = lead.pool.storage;
    const expanded = open.has(storage);
    const nodes = `${String(group.rows.length)} nodes`;
    return (
      <Fragment key={storage}>
        <TableRow
          className="cursor-pointer"
          onClick={() => {
            toggle(storage);
          }}
        >
          <TableCell>
            <button
              type="button"
              aria-expanded={expanded}
              className="-ml-1 inline-flex items-center gap-1 rounded-sm text-left font-medium [overflow-wrap:anywhere] hover:underline"
              onClick={(e) => {
                e.stopPropagation();
                toggle(storage);
              }}
            >
              <ChevronRight
                aria-hidden="true"
                className={cn(
                  "h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform",
                  expanded && "rotate-90",
                )}
              />
              {storage}
            </button>
            <div className="pl-[18px] text-[12.5px] text-muted-foreground">
              {storageTypeLabel(lead.pool.type)}
              <span className="sm:hidden">
                , on {nodes}
                {stateNote(state)}
              </span>
            </div>
          </TableCell>
          <TableCell className="hidden text-muted-foreground sm:table-cell">
            {nodes}
          </TableCell>
          <TableCell className="hidden text-muted-foreground lg:table-cell">
            {contentList(lead.pool.content) || "Nothing"}
          </TableCell>
          <TableCell>
            <StorageCapacityBar used={lead.pool.used} total={lead.pool.total} />
            {group.fullest !== null && group.fullest.nodeName !== "" && (
              <div className="mt-1 text-[12px] text-muted-foreground">
                Fullest on {group.fullest.nodeName}
              </div>
            )}
          </TableCell>
          <TableCell>
            <StoreStateCell state={state} />
          </TableCell>
        </TableRow>
        {expanded &&
          group.rows.map((row) => {
            const path = storePath(row);
            return (
              <TableRow
                key={row.pool.id}
                className="cursor-pointer"
                onClick={() => {
                  void navigate(path);
                }}
              >
                <TableCell className="pl-[42px]">
                  <Link
                    to={path}
                    aria-label={`${storage} on ${row.nodeName || "an unknown node"}`}
                    className="[overflow-wrap:anywhere] hover:underline"
                    onClick={(e) => {
                      e.stopPropagation();
                    }}
                  >
                    {row.nodeName || "Unknown node"}
                  </Link>
                  {/* A phone shows the State column's dot alone. */}
                  {storeState(row.pool) !== "active" && (
                    <div className="text-[12.5px] text-muted-foreground sm:hidden">
                      {STATE_LABEL[storeState(row.pool)]}
                    </div>
                  )}
                </TableCell>
                <TableCell className="hidden sm:table-cell" />
                <TableCell className="hidden lg:table-cell" />
                <TableCell>
                  <StorageCapacityBar
                    used={row.pool.used}
                    total={row.pool.total}
                  />
                </TableCell>
                <TableCell>
                  <StoreStateCell state={storeState(row.pool)} />
                </TableCell>
              </TableRow>
            );
          })}
      </Fragment>
    );
  };

  return (
    <Panel
      flush
      title={title}
      aside={
        isLoading
          ? "Loading..."
          : showTotals && totals.stores > 0
            ? [
                `${String(totals.stores)} ${totals.stores === 1 ? "store" : "stores"}`,
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
                {groups.map((group) =>
                  group.rows.length > 1 ? nodeRows(group) : singleRow(group),
                )}
              </TableBody>
            </Table>
          )}
        </>
      )}
    </Panel>
  );
}
