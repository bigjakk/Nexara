import { Fragment, useMemo, useState } from "react";
import {
  useParams,
  Link,
  useNavigate,
  useSearchParams,
} from "react-router-dom";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  History,
  Layers,
  MoreHorizontal,
  Pencil,
  Terminal,
  Trash2,
} from "lucide-react";
import { DetailField } from "@/components/DetailField";
import { DetailHeader, Fact, StatusFact } from "@/components/page/DetailHeader";
import { DefGrid, Panel } from "@/components/page/Panel";
import { Stat, StatStrip, type StatTone } from "@/components/page/StatStrip";
import { StatusIcon } from "@/components/StatusIcon";
import { UsageBar } from "@/components/UsageBar";
import { Button } from "@/components/ui/button";
import { openConsole } from "@/features/console/lib/open-console";
import type { ConsoleClickKeys } from "@/features/console/lib/console-session";
import { usePermissions } from "@/hooks/usePermissions";
import { useCephStatus } from "@/features/ceph/api/ceph-queries";
import { reportsNoCeph } from "../lib/ceph-absent";
import {
  useCluster,
  useClusterNodes,
  useClusterStorage,
} from "../api/cluster-queries";
import {
  formatBytes,
  formatBytesPerSecond,
  formatPercent,
  formatUptime,
} from "@/lib/format";
import { useClusterMetrics } from "@/hooks/useMetrics";
import { nodePVEVersion } from "@/lib/pve-version";
import { cn } from "@/lib/utils";
import { usageLevel, USAGE_TEXT_CLASS } from "@/lib/usage-level";
import { visibleClusterIssues } from "@/lib/health-issues";
import { sparklineSeries, seriesTail } from "@/lib/metric-series";
import { useHealthDismissStore } from "@/stores/health-dismiss-store";
import { useHealthMuteStore } from "@/stores/health-mute-store";
import { GuestCells } from "@/features/dashboard/components/GuestCell";
import { useSeedMetrics } from "@/features/dashboard/api/historical-queries";
import {
  fleetGuestFromRow,
  type FleetGuest,
} from "@/features/dashboard/lib/fleet";
import { ResourceTable } from "@/features/inventory/components/ResourceTable";
import { InventoryUnavailableNote } from "@/features/inventory/components/InventoryUnavailableNote";
import { useInventoryData } from "@/features/inventory/api/inventory-queries";
import { storageTotals, storeRows } from "@/features/storage/lib/store-rows";
import { AddStorageDialog } from "@/features/storage/components/AddStorageDialog";
import { ClusterStoresPanel } from "@/features/storage/components/ClusterStoresPanel";
import { ClusterBackupTab } from "@/features/backup/components/ClusterBackupTab";
import type {
  ClusterResponse,
  HealthIssue,
  NodeResponse,
  StorageResponse,
} from "@/types/api";
import type { AggregatedMetrics, VmLiveMetric } from "@/types/ws";
import { ClusterCephTab } from "../components/ClusterCephTab";
import {
  ClusterCertificateBanner,
  TLS_FINGERPRINT_ISSUE,
} from "../components/ClusterCertificateBanner";
import { ClusterConfigureTab } from "../components/ClusterConfigureTab";
import { AcceptClusterName } from "../components/AcceptClusterName";
import { ClusterHistoryTab } from "../components/ClusterHistoryTab";
import {
  DEFAULT_CONFIGURE_SECTION,
  isConfigureSection,
  type ConfigureSection,
} from "../lib/configure-sections";
import { EditClusterDialog } from "../components/EditClusterDialog";
import { DeleteClusterDialog } from "../components/DeleteClusterDialog";
import { nodesNote } from "../lib/cluster-summary";
import { DateTime } from "@/components/DateTime";

const STATUS_LABEL: Record<ClusterResponse["status"], string> = {
  online: "Online",
  degraded: "Degraded",
  offline: "Offline",
  inactive: "Inactive",
  unknown: "Unknown",
};

export function ClusterDetailPage() {
  const { clusterId } = useParams<{ clusterId: string }>();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const rawTabParam = searchParams.get("tab") ?? "";
  // "nodes" was the default tab's value before it became "overview" — keep old
  // ?tab=nodes deep links landing on the same content.
  const tabParam = rawTabParam === "nodes" ? "" : rawTabParam;
  // The settings sit under Configure and keep the ?tab= values they had as
  // tabs of their own, so ?tab=firewall selects Configure with Firewall in it.
  // Configure itself goes back to the setting last shown.
  const section = isConfigureSection(tabParam) ? tabParam : null;
  const [lastSection, setLastSection] = useState<ConfigureSection>(
    DEFAULT_CONFIGURE_SECTION,
  );
  if (section !== null && section !== lastSection) setLastSection(section);
  const topTab =
    section !== null || tabParam === "configure"
      ? "configure"
      : tabParam || "overview";
  // Controlled + URL-synced tabs: ?tab= deep links (SearchBar shortcuts,
  // GlobalHealthIndicator, DRSConfigCard) select a tab even when the page is
  // already mounted, and clicking a tab rewrites the param (replace, so tab
  // hops don't pollute history). A bare `value` without onValueChange froze
  // the tab bar for anyone arriving via a ?tab= link.
  const setTab = (v: string) => {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (v === "overview") next.delete("tab");
        else next.set("tab", v);
        return next;
      },
      { replace: true },
    );
  };
  const clusterQuery = useCluster(clusterId ?? "");
  const nodesQuery = useClusterNodes(clusterId ?? "");
  // The same read the dashboard and the Storage page make, for the strip's
  // storage figure.
  const storageQuery = useClusterStorage(clusterId ?? "");
  const cluster = clusterQuery.data;

  // All guests in the cluster, as inventory rows so the VMs tab gets the
  // full ResourceTable (context menu, bulk actions) like the folder view.
  const {
    rows: inventoryRows,
    failedClusterIds,
    isLoading: inventoryLoading,
  } = useInventoryData();
  const clusterVmRows = useMemo(
    () =>
      inventoryRows.filter(
        (row) => row.clusterId === clusterId && row.type !== "node",
      ),
    [inventoryRows, clusterId],
  );
  const nodes = nodesQuery.data ?? [];
  // Until the guest list has answered, or when it could not be read, an empty
  // list is not "no guests": the Nodes table shows a dash instead.
  const guestsKnown =
    !inventoryLoading && !failedClusterIds.includes(clusterId ?? "");
  // Each node's guests as cells for the Nodes table, in VMID order.
  const guestsByNode = useMemo(() => {
    const map = new Map<string, FleetGuest[]>();
    for (const row of clusterVmRows) {
      const guest = fleetGuestFromRow(row);
      if (!guest) continue;
      const list = map.get(row.nodeName) ?? [];
      list.push(guest);
      map.set(row.nodeName, list);
    }
    for (const list of map.values()) list.sort((a, b) => a.vmid - b.vmid);
    return map;
  }, [clusterVmRows]);
  // A template is not a guest anyone runs: not counted, as on the dashboard.
  const guests = useMemo(() => {
    const real = clusterVmRows.filter((row) => !row.template);
    return {
      total: real.length,
      running: real.filter((row) => row.status === "running").length,
    };
  }, [clusterVmRows]);

  const { canConsole, hasPermission, canView } = usePermissions();
  // The Ceph tab cannot set Ceph up, so it goes when the cluster says it has
  // none. Loading, a failed read and a refusal keep it. The tab's own read,
  // shared through the cache; not sent at all to whoever may not view Ceph.
  const cephStatus = useCephStatus(canView("ceph") ? (clusterId ?? "") : "");
  const noCeph =
    cephStatus.data === undefined && reportsNoCeph(cephStatus.error);
  const shownTab = topTab === "ceph" && noCeph ? "overview" : topTab;
  // History is read from the audit log, and its rows open Proxmox task logs:
  // without both grants the tab would be a table of refusals.
  const canHistory =
    hasPermission("view", "audit") && hasPermission("view", "task");
  // The header offers only what the server would allow: its Update and
  // Delete routes ask for these.
  const canEditCluster = hasPermission("manage", "cluster");
  const canManageStorage = hasPermission("manage", "storage");
  const canManageBackup = hasPermission("manage", "backup");
  const canDeleteCluster = hasPermission("delete", "cluster");

  const clusterMetrics = useClusterMetrics(clusterId ?? "");

  const dismissed = useHealthDismissStore((s) => s.dismissed);
  const mutedTypes = useHealthMuteStore((s) => s.mutedTypes);
  // The changed certificate has its own banner above the tabs.
  const issues = useMemo(
    () =>
      visibleClusterIssues(
        clusterId ?? "",
        cluster?.issues ?? [],
        new Set(dismissed),
        new Set(mutedTypes),
      ).filter((issue) => issue.type !== TLS_FINGERPRINT_ISSUE),
    [clusterId, cluster?.issues, dismissed, mutedTypes],
  );

  const [editOpen, setEditOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const isLoading = clusterQuery.isLoading || nodesQuery.isLoading;
  const error = clusterQuery.error ?? nodesQuery.error;
  const nodesOnline = nodes.filter((n) => n.status === "online").length;

  function openNodeShell(nodeName: string, keys: ConsoleClickKeys) {
    openConsole(
      { clusterID: clusterId ?? "", node: nodeName, type: "node_shell" },
      keys,
    );
  }

  return (
    <div className="space-y-5">
      {error != null ? (
        <Panel>
          <p className="text-sm text-destructive">
            Failed to load cluster data.
          </p>
          <Button variant="link" asChild className="mt-1 h-auto px-0">
            <Link to="/clusters">Back to clusters</Link>
          </Button>
        </Panel>
      ) : (
        /* Everything below is one cluster's, so it is keyed on the cluster.

           The route clusters/:clusterId is one page across every cluster, and
           the ?tab= that the search bar's shortcuts leave in the URL keeps the
           same tab selected through a move. A tab that survived the move would
           carry over what it holds: the node it was on, a dialog left open, a
           note typed into a form, a save still in flight. A late failure of
           the one would then render on the other's card, a delete confirmed in
           the one's dialog would be sent to the other, and a note typed for
           the one would be saved to the other. Radix already unmounts a tab's
           content when another tab is selected, so none of that outlives a
           change of tab; this gives a change of cluster the same lifetime. The
           selected tab is URL state, not tab state, and is left alone.

           In the app the shell already does this for every page: AppShell keys
           its error boundary on the pathname, so a change of cluster rebuilds
           the whole page and this key never changes under a mounted page. It is
           here so that the page does not depend on that, which a shell that
           stopped remounting would expose in every tab at once, and so that it
           is right in the bare router its tests run in.

           In a bare router, what the key leaves alone: the queries and
           live-metrics subscription above, which follow clusterId. A tab's own
           queries are cached by key, so a rebuilt tab reads again only what
           has gone stale. */
        <Fragment key={clusterId ?? ""}>
          {isLoading || cluster == null ? (
            <div className="flex items-center gap-3.5">
              <Skeleton className="h-11 w-11 rounded-[10px]" />
              <div className="space-y-2">
                <Skeleton className="h-7 w-48" />
                <Skeleton className="h-4 w-72" />
              </div>
            </div>
          ) : (
            <DetailHeader
              crumbs={[
                { label: "Inventory", to: "/inventory" },
                { label: cluster.name },
              ]}
              icon={<Layers />}
              title={cluster.name}
              facts={
                <>
                  <StatusFact
                    status={cluster.status}
                    label={STATUS_LABEL[cluster.status]}
                    detail={
                      nodes.length > 0
                        ? `${String(nodesOnline)} of ${String(nodes.length)} nodes${cluster.status === "online" ? "" : " online"}`
                        : undefined
                    }
                  />
                  {cluster.pve_version !== "" && (
                    <Fact label="Version">PVE {cluster.pve_version}</Fact>
                  )}
                  {guestsKnown && (
                    <Fact label="Guests">{String(guests.total)}</Fact>
                  )}
                  <Fact label="API">
                    <span className="font-mono text-[12.5px] [overflow-wrap:anywhere]">
                      {cluster.api_url}
                    </span>
                  </Fact>
                </>
              }
              actions={
                <>
                  {/* Named for what it edits: tabs below have rows with an
                      Edit button of their own. */}
                  {canEditCluster && (
                    <Button
                      variant="outline"
                      size="sm"
                      className="gap-1.5"
                      aria-label="Edit cluster"
                      onClick={() => {
                        setEditOpen(true);
                      }}
                    >
                      <Pencil className="h-4 w-4" />
                      Edit
                    </Button>
                  )}
                  {(canHistory || canDeleteCluster) && (
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button
                          variant="outline"
                          size="sm"
                          className="px-2"
                          aria-label="More actions"
                        >
                          <MoreHorizontal />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        {canHistory && (
                          <DropdownMenuItem
                            className="gap-2"
                            onClick={() => {
                              setTab("history");
                            }}
                          >
                            <History className="h-4 w-4" />
                            History
                          </DropdownMenuItem>
                        )}
                        {canHistory && canDeleteCluster && (
                          <DropdownMenuSeparator />
                        )}
                        {canDeleteCluster && (
                          <DropdownMenuItem
                            className="gap-2 text-destructive focus:text-destructive"
                            onClick={() => {
                              setDeleteOpen(true);
                            }}
                          >
                            <Trash2 className="h-4 w-4" />
                            Delete cluster
                          </DropdownMenuItem>
                        )}
                      </DropdownMenuContent>
                    </DropdownMenu>
                  )}
                </>
              }
            />
          )}

          {cluster != null && <ClusterCertificateBanner cluster={cluster} />}

          {/* The strip holds what is looked at; what is set up once is under
              Configure. */}
          <Tabs
            value={shownTab}
            onValueChange={(v) => {
              setTab(v === "configure" ? lastSection : v);
            }}
          >
            <TabsList>
              <TabsTrigger value="overview">Overview</TabsTrigger>
              <TabsTrigger value="vms">
                VMs ({clusterVmRows.length})
              </TabsTrigger>
              <TabsTrigger value="storage">Storage</TabsTrigger>
              <TabsTrigger value="backup">Backup</TabsTrigger>
              {!noCeph && <TabsTrigger value="ceph">Ceph</TabsTrigger>}
              <TabsTrigger value="configure">Configure</TabsTrigger>
              {canHistory && <TabsTrigger value="history">History</TabsTrigger>}
            </TabsList>

            <TabsContent value="overview" className="mt-5 space-y-4">
              {issues.length > 0 && (
                <AttentionPanel clusterId={clusterId ?? ""} issues={issues} />
              )}
              <PerformancePanel
                clusterId={clusterId ?? ""}
                metrics={clusterMetrics}
                nodes={nodes}
                storage={storageQuery.data}
                guests={guestsKnown ? guests : undefined}
              />
              <Panel flush title="Nodes" aside={nodesNote(nodes)}>
                {isLoading ? (
                  <div className="space-y-2 px-[18px]">
                    <Skeleton className="h-10 w-full" />
                    <Skeleton className="h-10 w-full" />
                    <Skeleton className="h-10 w-full" />
                  </div>
                ) : nodes.length === 0 ? (
                  <p className="px-[18px] text-sm text-muted-foreground">
                    No nodes found for this cluster.
                  </p>
                ) : (
                  <div className="@container">
                    <div
                      aria-hidden="true"
                      className={cn(
                        "hidden gap-x-6 border-b px-[18px] pb-2 text-xs font-medium text-muted-foreground @5xl:grid",
                        NODE_COLUMNS,
                      )}
                    >
                      <span>Node</span>
                      <span>CPU</span>
                      <span>Memory</span>
                      <span>Guests</span>
                      <span>Root disk</span>
                      <span>Version</span>
                      <span>Uptime</span>
                      <span />
                    </div>
                    <ul className="divide-y">
                      {nodes.map((node) => (
                        <NodeRow
                          key={node.id}
                          node={node}
                          href={`/clusters/${clusterId ?? ""}/nodes/${node.id}`}
                          live={clusterMetrics?.nodeMetrics.get(node.id)}
                          guests={
                            guestsKnown
                              ? (guestsByNode.get(node.name) ?? [])
                              : undefined
                          }
                          onShell={
                            node.status === "online" && canConsole("node")
                              ? (keys) => {
                                  openNodeShell(node.name, keys);
                                }
                              : undefined
                          }
                        />
                      ))}
                    </ul>
                  </div>
                )}
              </Panel>
              {cluster != null && <ConnectionPanel cluster={cluster} />}
            </TabsContent>

            <TabsContent value="ceph">
              <ClusterCephTab clusterId={clusterId ?? ""} />
            </TabsContent>

            {canHistory && (
              <TabsContent value="history">
                <ClusterHistoryTab clusterId={clusterId ?? ""} />
              </TabsContent>
            )}

            <TabsContent value="vms">
              {clusterId !== undefined &&
                failedClusterIds.includes(clusterId) && (
                  <InventoryUnavailableNote />
                )}
              <ResourceTable data={clusterVmRows} />
            </TabsContent>

            {/* Where Proxmox has Datacenter > Storage: the stores Nexara has
                read for this cluster, and Add storage. */}
            <TabsContent value="storage">
              <ClusterStoresPanel
                clusterId={clusterId ?? ""}
                title="Storage"
                showTotals
                actions={
                  canManageStorage ? (
                    <AddStorageDialog clusterId={clusterId ?? ""} />
                  ) : undefined
                }
              />
            </TabsContent>

            {/* Where Proxmox has Datacenter > Backup: the cluster's backup
                schedules, with or without a PBS server. */}
            <TabsContent value="backup">
              <ClusterBackupTab
                clusterId={clusterId ?? ""}
                canManage={canManageBackup}
              />
            </TabsContent>

            <TabsContent value="configure">
              <ClusterConfigureTab
                clusterId={clusterId ?? ""}
                pveVersion={cluster?.pve_version ?? ""}
                section={section ?? lastSection}
                onSectionChange={setTab}
              />
            </TabsContent>
          </Tabs>

          {/* Mounted only while open: the edit dialog seeds its form from its
              props once, and the delete dialog keeps what was typed. */}
          {editOpen && cluster != null && (
            <EditClusterDialog
              cluster={cluster}
              open={editOpen}
              onOpenChange={setEditOpen}
            />
          )}
          {deleteOpen && cluster != null && (
            <DeleteClusterDialog
              cluster={cluster}
              open={deleteOpen}
              onOpenChange={setDeleteOpen}
              onDeleted={() => {
                void navigate("/clusters");
              }}
            />
          )}
        </Fragment>
      )}
    </div>
  );
}

/** The colour a utilisation figure takes: plain until it is a problem. */
function usageTone(percent: number | null | undefined): StatTone {
  const level = usageLevel(percent ?? null);
  return level === "err" ? "bad" : level === "warn" ? "warn" : "default";
}

/** Two rates on a line each: together they are too long for a strip cell. */
function ratePair(a: number, b: number, aLabel: string, bLabel: string) {
  return (
    <>
      <span className="block truncate">
        {formatBytesPerSecond(a)} {aLabel}
      </span>
      <span className="block truncate">
        {formatBytesPerSecond(b)} {bLabel}
      </span>
    </>
  );
}

/**
 * What the cluster is doing now: each measure's latest reading, over the
 * shape of its last hour where the page holds one.
 */
function PerformancePanel({
  clusterId,
  metrics,
  nodes,
  storage,
  guests,
}: {
  clusterId: string;
  metrics: AggregatedMetrics | undefined;
  nodes: readonly NodeResponse[];
  storage: readonly StorageResponse[] | undefined;
  /** Undefined until the guest list has answered. */
  guests: { total: number; running: number } | undefined;
}) {
  const seed = useSeedMetrics(clusterId);
  const liveHistory = metrics?.history;
  const history = useMemo(
    () => sparklineSeries(seed ?? [], liveHistory ?? []),
    [seed, liveHistory],
  );
  const store = useMemo(
    () =>
      storage === undefined
        ? undefined
        : storageTotals(storeRows(storage, new Map())),
    [storage],
  );
  const cores = nodes.reduce((sum, n) => sum + n.cpu_count, 0);

  return (
    <Panel flush title="Performance" aside="Live, with the last hour">
      <StatStrip>
        <Stat
          label="CPU"
          value={metrics ? formatPercent(metrics.cpuPercent) : "—"}
          tone={usageTone(metrics?.cpuPercent)}
          sub={
            nodes.length > 0
              ? `${String(cores)} cores on ${String(nodes.length)} ${nodes.length === 1 ? "node" : "nodes"}`
              : undefined
          }
          trend={seriesTail(history, (p) => p.cpuPercent)}
          trendMax={100}
        />
        <Stat
          label="Memory"
          value={metrics ? formatPercent(metrics.memPercent) : "—"}
          tone={usageTone(metrics?.memPercent)}
          sub={
            metrics && metrics.memTotal > 0
              ? `${formatBytes(metrics.memUsed)} of ${formatBytes(metrics.memTotal)}`
              : undefined
          }
          trend={seriesTail(history, (p) => p.memPercent)}
          trendMax={100}
        />
        <Stat
          label="Storage"
          value={store?.percent == null ? "—" : formatPercent(store.percent)}
          tone={usageTone(store?.percent)}
          sub={
            store !== undefined && store.total > 0
              ? `${formatBytes(store.used)} of ${formatBytes(store.total)}`
              : undefined
          }
          percent={store?.percent ?? null}
        />
        <Stat
          label="Guests running"
          value={guests === undefined ? "—" : String(guests.running)}
          tone={guests?.total === 0 ? "quiet" : "default"}
          sub={guests === undefined ? undefined : `of ${String(guests.total)}`}
        />
        <Stat
          label="Disk I/O"
          value={
            metrics
              ? formatBytesPerSecond(metrics.diskReadBps + metrics.diskWriteBps)
              : "—"
          }
          sub={
            metrics
              ? ratePair(
                  metrics.diskReadBps,
                  metrics.diskWriteBps,
                  "read",
                  "write",
                )
              : undefined
          }
          trend={seriesTail(history, (p) =>
            p.diskReadBps === null || p.diskWriteBps === null
              ? null
              : p.diskReadBps + p.diskWriteBps,
          )}
        />
        <Stat
          label="Network"
          value={
            metrics
              ? formatBytesPerSecond(metrics.netInBps + metrics.netOutBps)
              : "—"
          }
          sub={
            metrics
              ? ratePair(metrics.netInBps, metrics.netOutBps, "in", "out")
              : undefined
          }
          trend={seriesTail(history, (p) =>
            p.netInBps === null || p.netOutBps === null
              ? null
              : p.netInBps + p.netOutBps,
          )}
        />
      </StatStrip>
    </Panel>
  );
}

/** One column template for the header line and every node, once there is room. */
const NODE_COLUMNS =
  "@5xl:grid-cols-[minmax(0,1.3fr)_minmax(0,1.2fr)_minmax(0,1.2fr)_minmax(0,2.2fr)_5.5rem_4.5rem_5rem_4.5rem]";

/**
 * One node: its state and name, its load, a cell per guest, and what it
 * runs. A table's columns where the panel is wide, a block per node where it
 * is not.
 */
function NodeRow({
  node,
  href,
  live,
  guests,
  onShell,
}: {
  node: NodeResponse;
  href: string;
  live: VmLiveMetric | undefined;
  /** Undefined until the guest list has answered: not the same as none. */
  guests: FleetGuest[] | undefined;
  /** Absent when the node is down or the caller may not open a shell. */
  onShell: ((keys: ConsoleClickKeys) => void) | undefined;
}) {
  return (
    <li
      className={cn(
        "flex flex-wrap items-center gap-x-6 gap-y-2.5 px-[18px] py-3 transition-colors hover:bg-foreground/[0.035] @5xl:grid",
        NODE_COLUMNS,
      )}
    >
      {/* Narrow, the name and the Shell button share the first line and
          everything else wraps under them. */}
      <div className="order-1 flex min-w-0 flex-[1_1_calc(100%-7rem)] flex-wrap items-center gap-x-2 gap-y-1 @5xl:order-none">
        <span className="flex min-w-0 items-center gap-2">
          <StatusIcon status={node.status} />
          <Link to={href} className="truncate font-medium hover:underline">
            {node.name}
          </Link>
        </span>
        {node.status !== "online" && (
          <Badge variant="destructive">{node.status}</Badge>
        )}
        {node.ha_state === "maintenance" && (
          <Badge variant="warning">Maintenance</Badge>
        )}
      </div>

      <div className="order-3 grid basis-full grid-cols-2 gap-x-6 @2xl:flex-[1_1_18rem] @5xl:contents">
        <NodeMeter
          label="CPU"
          percent={live?.cpuPercent ?? null}
          detail={`${String(node.cpu_count)} cores`}
        />
        <NodeMeter
          label="Memory"
          percent={live?.memPercent ?? null}
          detail={formatBytes(node.mem_total)}
        />
      </div>

      <div className="order-4 min-w-0 basis-full @2xl:flex-[1_1_14rem] @5xl:order-none">
        {guests !== undefined ? (
          <GuestCells guests={guests} />
        ) : (
          <span className="text-xs text-muted-foreground">—</span>
        )}
      </div>

      <div className="order-5 flex basis-full flex-wrap gap-x-5 gap-y-0.5 text-[12.5px] text-muted-foreground @5xl:contents @5xl:text-sm">
        <span className="whitespace-nowrap">
          <span className="text-muted-foreground/75 @5xl:sr-only">
            Root disk{" "}
          </span>
          {formatBytes(node.disk_total)}
        </span>
        <span
          className="whitespace-nowrap @5xl:text-foreground"
          title={node.pve_version}
        >
          <span className="text-muted-foreground/75 @5xl:sr-only">
            Version{" "}
          </span>
          {nodePVEVersion(node.pve_version)}
        </span>
        <span className="whitespace-nowrap @5xl:text-foreground">
          <span className="text-muted-foreground/75 @5xl:sr-only">Up </span>
          {formatUptime(node.uptime)}
        </span>
      </div>

      <div className="order-2 flex justify-end @5xl:order-none">
        {onShell !== undefined && (
          <Button
            variant="ghost"
            size="sm"
            className="h-7 gap-1 px-2 text-xs"
            title="Open shell"
            onClick={onShell}
          >
            <Terminal className="h-3.5 w-3.5" />
            Shell
          </Button>
        )}
      </div>
    </li>
  );
}

/**
 * A node's load: the bar, the reading, and what it is a share of. Named above
 * the bar where the list is too narrow for the header line. No reading yet
 * draws an empty track and a dash, never 0%.
 */
function NodeMeter({
  label,
  percent,
  detail,
}: {
  label: string;
  percent: number | null;
  detail: string;
}) {
  const clamped = percent === null ? null : Math.min(100, Math.max(0, percent));
  const level = usageLevel(clamped);
  return (
    <div className="min-w-0">
      <div className="text-xs text-muted-foreground @5xl:sr-only">{label}</div>
      <div className="flex items-center gap-2">
        <UsageBar percent={clamped} className="max-w-24 flex-1" />
        <span
          className={cn(
            "w-9 shrink-0 text-xs tabular-nums",
            level === "ok" ? "text-muted-foreground" : USAGE_TEXT_CLASS[level],
          )}
        >
          {clamped === null ? "—" : `${String(Math.round(clamped))}%`}
        </span>
      </div>
      <div className="mt-1 truncate text-xs text-muted-foreground/75">
        <span>{detail}</span>
      </div>
    </div>
  );
}

/** The cluster's open health issues. Not drawn at all when there are none. */
function AttentionPanel({
  clusterId,
  issues,
}: {
  clusterId: string;
  issues: readonly HealthIssue[];
}) {
  return (
    <Panel
      title="Needs attention"
      aside={`${String(issues.length)} open ${issues.length === 1 ? "issue" : "issues"}`}
    >
      <ul className="divide-y text-sm">
        {issues.map((issue) => (
          <li
            key={`${issue.type}:${issue.target}:${issue.detail}`}
            className="flex items-start gap-2.5 py-2.5 first:pt-0 last:pb-0"
          >
            <span
              aria-hidden="true"
              className={cn(
                "mt-[7px] h-2 w-2 shrink-0 rounded-full",
                issue.severity === "err" ? "bg-red-500" : "bg-amber-500",
              )}
            />
            <div className="min-w-0">
              <p>
                <span className="font-medium">{issue.summary}</span>
                {issue.target !== "" && (
                  <span className="ml-2 text-muted-foreground">
                    {issue.target}
                  </span>
                )}
              </p>
              {issue.detail !== "" && (
                <p className="mt-0.5 text-[12.5px] text-muted-foreground [overflow-wrap:anywhere]">
                  {issue.detail}
                </p>
              )}
              <AcceptClusterName clusterId={clusterId} issue={issue} />
            </div>
          </li>
        ))}
      </ul>
    </Panel>
  );
}

/** How Nexara reaches the cluster. */
function ConnectionPanel({ cluster }: { cluster: ClusterResponse }) {
  return (
    <Panel title="Connection">
      <DefGrid className="grid-cols-[repeat(auto-fit,minmax(min(13rem,100%),1fr))]">
        <DetailField label="API address" variant="mono">
          <span className="text-[12.5px] [overflow-wrap:anywhere]">
            {cluster.api_url}
          </span>
        </DetailField>
        <DetailField label="API token" variant="mono">
          <span className="text-[12.5px] [overflow-wrap:anywhere]">
            {cluster.token_id}
          </span>
        </DetailField>
        <DetailField label="Credential">
          {cluster.credential_source === "bootstrap"
            ? "Created by Nexara"
            : "Entered by hand"}
        </DetailField>
        <DetailField label="Certificate">
          {cluster.tls_fingerprint !== ""
            ? "Pinned by fingerprint"
            : "Checked against trusted CAs"}
        </DetailField>
        <DetailField label="Sync interval">
          Every {String(cluster.sync_interval_seconds)} s
        </DetailField>
        <DetailField label="Added">
          <DateTime value={cluster.created_at} />
        </DetailField>
      </DefGrid>
    </Panel>
  );
}
