import { useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  ArrowRightLeft,
  Container,
  Database,
  LibraryBig,
  MoreHorizontal,
  Trash2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Segmented } from "@/components/ui/segmented";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/EmptyState";
import { DetailHeader, Fact, StatusFact } from "@/components/page/DetailHeader";
import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import {
  HistoryTabContent,
  HistoryTabTrigger,
} from "@/features/history/components/HistoryTab";
import { backupHistories } from "@/features/history/lib/backup-histories";
import {
  useCluster,
  useClusterNodes,
} from "@/features/clusters/api/cluster-queries";
import { usePermissions } from "@/hooks/usePermissions";
import { formatBytes, formatPercent } from "@/lib/format";
import { usageLevel } from "@/lib/usage-level";
import { useClusterStorage, useStorageContent } from "../api/storage-queries";
import { StorageContentTable } from "../components/StorageContentTable";
import { StorageGuestTable } from "../components/StorageGuestTable";
import { UploadDialog } from "../components/UploadDialog";
import { URLDownloadDialog } from "../components/URLDownloadDialog";
import { OCIPullDialog } from "../components/OCIPullDialog";
import { ApplianceBrowserDialog } from "../components/ApplianceBrowserDialog";
import { BulkMoveDialog } from "../components/BulkMoveDialog";
import { EditStorageDialog } from "../components/EditStorageDialog";
import { DeleteStorageDialog } from "../components/DeleteStorageDialog";
import { isPVEAtLeast } from "../lib/pve-version";
import {
  contentBreakdown,
  contentKindNoun,
  contentKindPlural,
  contentList,
  sortContentKinds,
  storageTypeLabel,
  storePercent,
  storeState,
  STATE_ICON,
  STATE_LABEL,
} from "../lib/store-rows";
import type { StorageContentItem } from "../types/storage";

/** The content filter's "every kind" choice; no Proxmox content kind is named this. */
const ALL_KINDS = "all";

export function StorageDetailPage() {
  const { clusterId = "", storageId = "" } = useParams<{
    clusterId: string;
    storageId: string;
  }>();
  const navigate = useNavigate();
  const [kind, setKind] = useState(ALL_KINDS);
  // These four open from a More menu, so the page holds their open state.
  const [evacuateOpen, setEvacuateOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [appliancesOpen, setAppliancesOpen] = useState(false);
  const [ociOpen, setOciOpen] = useState(false);

  const storageQuery = useClusterStorage(clusterId);
  const nodesQuery = useClusterNodes(clusterId);
  const contentQuery = useStorageContent(clusterId, storageId);
  const clusterQuery = useCluster(clusterId);
  // What the store's routes take: manage:storage to edit it or add content,
  // delete:storage to remove it; Evacuate moves VM disks, manage:vm.
  const { hasPermission } = usePermissions();
  const canManageStorage = hasPermission("manage", "storage");
  const canDeleteStorage = hasPermission("delete", "storage");
  const canMoveVMDisks = hasPermission("manage", "vm");

  const pools = useMemo(() => storageQuery.data ?? [], [storageQuery.data]);
  const pool = useMemo(
    () => pools.find((p) => p.id === storageId),
    [pools, storageId],
  );
  const items: StorageContentItem[] = contentQuery.data ?? [];

  const hostNodeName = useMemo(() => {
    if (!pool || pool.shared) return null;
    return nodesQuery.data?.find((n) => n.id === pool.node_id)?.name ?? null;
  }, [nodesQuery.data, pool]);

  const contentTypes = useMemo(
    () =>
      (pool?.content ?? "")
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean),
    [pool],
  );
  const hasImages = contentTypes.includes("images");
  const hasRootdir = contentTypes.includes("rootdir");
  const hasGuestVolumes = hasImages || hasRootdir;
  const hasVztmpl = contentTypes.includes("vztmpl");
  const hasIso = contentTypes.includes("iso");
  const hasImport = contentTypes.includes("import");
  // OCI requires a file-based storage with vztmpl + PVE 9.1+.
  const pveSupportsOCI = isPVEAtLeast(
    clusterQuery.data?.pve_version ?? "",
    "9.1",
  );
  const filterableTypes = sortContentKinds(contentTypes).filter(
    (t) =>
      t === "iso" ||
      t === "vztmpl" ||
      t === "images" ||
      t === "backup" ||
      t === "rootdir" ||
      t === "snippets",
  );

  const guestItems = items.filter(
    (item) => item.content === "images" || item.content === "rootdir",
  );

  const evacuateTargets = useMemo(() => {
    if (!pool) return [];
    const seen = new Set<string>();
    return pools
      .filter(
        (p) =>
          p.content.includes("images") &&
          p.storage !== pool.storage &&
          p.active &&
          p.enabled,
      )
      .filter((p) => {
        if (seen.has(p.storage)) return false;
        seen.add(p.storage);
        return true;
      })
      .map((p) => p.storage);
  }, [pools, pool]);

  const migrateTargets = useMemo(() => {
    if (!pool) return [];
    const seen = new Set<string>();
    return pools
      .filter(
        (p) =>
          p.storage !== pool.storage &&
          p.active &&
          p.enabled &&
          (p.content.includes("images") || p.content.includes("rootdir")),
      )
      .filter((p) => {
        if (seen.has(p.storage)) return false;
        seen.add(p.storage);
        return true;
      })
      .map((p) => p.storage);
  }, [pools, pool]);

  function filterByType(type: string): StorageContentItem[] {
    return items.filter((item) => item.content === type);
  }

  if (storageQuery.isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-4 w-40" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }

  if (!pool) {
    return (
      <EmptyState
        icon={Database}
        title="Store not found"
        description="It may have been removed, or you may not have access."
        action={
          <Button variant="outline" size="sm" asChild>
            <Link to="/storage">Back to storage</Link>
          </Button>
        }
      />
    );
  }

  const storageHistory = backupHistories.storage(
    clusterId,
    pool.storage,
    hostNodeName ?? undefined,
  );
  // A storage that is not shared is a different one on each node, so its
  // History is its host node's, and is not read before that node is known: the
  // first read would be the same-named storage of every node. A node that
  // cannot be told (the node list failed, does not hold the pool's node, or
  // names it with nothing) reads nothing either, rather than falling back to
  // every node's: a name is tested for being there, not for being null, since
  // an empty one would drop `node` from the read.
  const historyPlaceholder =
    pool.shared || hostNodeName ? undefined : nodesQuery.isPending ? (
      <Skeleton className="h-40 w-full" />
    ) : (
      <p className="text-sm text-muted-foreground">
        Which node this storage is on is not known, so its History cannot be
        shown.
      </p>
    );

  const state = storeState(pool);
  const percent = storePercent(pool);
  const kinds = contentList(pool.content);
  const guestCount = new Set(
    guestItems
      .filter((item) => item.vmid != null)
      .map((item) => `${item.content}:${String(item.vmid)}`),
  ).size;
  const shownKind = filterableTypes.some((t) => t === kind) ? kind : ALL_KINDS;
  const shownItems = shownKind === ALL_KINDS ? items : filterByType(shownKind);
  const canEvacuate = canMoveVMDisks && hasImages && evacuateTargets.length > 0;
  const canPullOCI = hasVztmpl && pveSupportsOCI;
  const contentLoaded = !contentQuery.isLoading && !contentQuery.isError;

  return (
    <div className="space-y-5">
      <DetailHeader
        crumbs={[
          { label: "Storage", to: "/storage" },
          {
            label: clusterQuery.data?.name ?? "Cluster",
            to: `/storage?cluster=${clusterId}`,
          },
          { label: pool.storage },
        ]}
        icon={<Database />}
        title={pool.storage}
        facts={
          <>
            <StatusFact
              status={STATE_ICON[state]}
              label={STATE_LABEL[state]}
              nameDot
            />
            <Fact label="Type">{storageTypeLabel(pool.type)}</Fact>
            {pool.shared ? (
              <Fact label="Scope">Shared</Fact>
            ) : hostNodeName ? (
              <Fact label="Node">
                <Link
                  to={`/clusters/${clusterId}/nodes/${pool.node_id}`}
                  className="hover:text-foreground hover:underline"
                >
                  {hostNodeName}
                </Link>
              </Fact>
            ) : (
              <Fact label="Scope">Local</Fact>
            )}
            <Fact label="Holds">{kinds !== "" ? kinds : "Nothing"}</Fact>
          </>
        }
        actions={
          <>
            {/* Read-only without manage:storage: its configuration is
                view:storage's to read. */}
            <EditStorageDialog
              clusterId={clusterId}
              storageId={pool.id}
              storageName={pool.storage}
              storageType={pool.type}
              readOnly={!canManageStorage}
            />
            {/* No menu when neither of its items is offered. */}
            {(canEvacuate || canDeleteStorage) && (
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
                  {canEvacuate && (
                    <DropdownMenuItem
                      className="gap-2"
                      onClick={() => {
                        setEvacuateOpen(true);
                      }}
                    >
                      <ArrowRightLeft className="h-4 w-4" />
                      Evacuate
                    </DropdownMenuItem>
                  )}
                  {canEvacuate && canDeleteStorage && <DropdownMenuSeparator />}
                  {canDeleteStorage && (
                    <DropdownMenuItem
                      className="gap-2 text-destructive focus:text-destructive"
                      onClick={() => {
                        setDeleteOpen(true);
                      }}
                    >
                      <Trash2 className="h-4 w-4" />
                      Delete
                    </DropdownMenuItem>
                  )}
                </DropdownMenuContent>
              </DropdownMenu>
            )}
          </>
        }
      />

      <Panel flush bodyClassName="py-4">
        <StatStrip>
          <Stat
            label="Used"
            value={percent === null ? "—" : formatBytes(pool.used)}
            sub={
              percent === null
                ? "No size reported"
                : `${formatPercent(percent)} of capacity`
            }
            tone={
              usageLevel(percent) === "err"
                ? "bad"
                : usageLevel(percent) === "warn"
                  ? "warn"
                  : "default"
            }
            percent={percent}
          />
          <Stat
            label="Free"
            value={percent === null ? "—" : formatBytes(pool.avail)}
            sub={
              percent === null
                ? undefined
                : `${formatPercent((pool.avail / pool.total) * 100)} of capacity`
            }
          />
          <Stat
            label="Total"
            value={percent === null ? "—" : formatBytes(pool.total)}
            sub={
              pool.shared
                ? "Shared across nodes"
                : hostNodeName
                  ? `On ${hostNodeName}`
                  : undefined
            }
          />
          <Stat
            label="Items"
            value={contentLoaded ? String(items.length) : "—"}
            tone={contentLoaded && items.length === 0 ? "quiet" : "default"}
            sub={
              contentLoaded && items.length > 0
                ? contentBreakdown(items.map((item) => item.content))
                : undefined
            }
          />
        </StatStrip>
      </Panel>

      <Tabs defaultValue="content">
        <TabsList>
          <TabsTrigger value="content">Content</TabsTrigger>
          {hasGuestVolumes && <TabsTrigger value="guests">Guests</TabsTrigger>}
          <HistoryTabTrigger history={storageHistory} />
        </TabsList>

        <TabsContent value="content" className="mt-5">
          <Panel
            flush
            title="Content"
            aside={
              contentLoaded && shownKind !== ALL_KINDS
                ? `${String(shownItems.length)} of ${String(items.length)}`
                : undefined
            }
            actions={
              <>
                {filterableTypes.length > 1 && (
                  <Segmented
                    aria-label="Content kind"
                    value={shownKind}
                    onValueChange={setKind}
                    options={[
                      { value: ALL_KINDS, label: "All" },
                      ...filterableTypes.map((t) => ({
                        value: t,
                        ariaLabel: `${contentKindPlural(t)}, ${String(filterByType(t).length)}`,
                        label: (
                          <>
                            {contentKindPlural(t)}
                            <span className="font-normal opacity-70">
                              {filterByType(t).length}
                            </span>
                          </>
                        ),
                      })),
                    ]}
                  />
                )}
                {canManageStorage && (
                  <UploadDialog
                    clusterId={clusterId}
                    storageId={pool.id}
                    supportedContent={pool.content}
                  />
                )}
                {canManageStorage && (hasIso || hasVztmpl || hasImport) && (
                  <URLDownloadDialog
                    clusterId={clusterId}
                    storageId={pool.id}
                    supportedContent={pool.content}
                  />
                )}
                {canManageStorage && hasVztmpl && (
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button
                        variant="outline"
                        size="sm"
                        className="px-2"
                        aria-label="More ways to add content"
                      >
                        <MoreHorizontal />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                      <DropdownMenuItem
                        className="gap-2"
                        onClick={() => {
                          setAppliancesOpen(true);
                        }}
                      >
                        <LibraryBig className="h-4 w-4" />
                        Browse appliances
                      </DropdownMenuItem>
                      {canPullOCI && (
                        <DropdownMenuItem
                          className="gap-2"
                          onClick={() => {
                            setOciOpen(true);
                          }}
                        >
                          <Container className="h-4 w-4" />
                          Pull OCI image
                        </DropdownMenuItem>
                      )}
                    </DropdownMenuContent>
                  </DropdownMenu>
                )}
              </>
            }
          >
            {contentQuery.isLoading && (
              <div className="space-y-2 px-[18px] pb-1">
                <Skeleton className="h-8 w-full" />
                <Skeleton className="h-8 w-full" />
                <Skeleton className="h-8 w-full" />
              </div>
            )}
            {contentQuery.isError && (
              <p className="px-[18px] pb-1 text-sm text-destructive">
                Failed to load storage content.
              </p>
            )}
            {contentLoaded && (
              <StorageContentTable
                items={shownItems}
                clusterId={clusterId}
                storageId={pool.id}
                emptyText={
                  shownKind === ALL_KINDS
                    ? undefined
                    : `No ${contentKindNoun(shownKind)} on this store.`
                }
              />
            )}
          </Panel>
        </TabsContent>

        {hasGuestVolumes && (
          <TabsContent value="guests" className="mt-5">
            <Panel
              flush
              title="Guests"
              aside={
                contentQuery.isLoading
                  ? "Loading..."
                  : `${String(guestCount)} with ${guestItems.length === 1 ? "a volume" : "volumes"} on this store`
              }
            >
              {contentQuery.isLoading ? (
                <div className="space-y-2 px-[18px] pb-1">
                  <Skeleton className="h-8 w-full" />
                  <Skeleton className="h-8 w-full" />
                </div>
              ) : (
                <StorageGuestTable
                  items={guestItems}
                  clusterId={clusterId}
                  migrateTargets={migrateTargets}
                />
              )}
            </Panel>
          </TabsContent>
        )}

        <HistoryTabContent
          history={storageHistory}
          placeholder={historyPlaceholder}
        />
      </Tabs>

      {canEvacuate && (
        <BulkMoveDialog
          clusterId={clusterId}
          storageId={pool.id}
          storageName={pool.storage}
          targetOptions={evacuateTargets}
          open={evacuateOpen}
          onOpenChange={setEvacuateOpen}
        />
      )}
      {canDeleteStorage && (
        <DeleteStorageDialog
          clusterId={clusterId}
          storageId={pool.id}
          storageName={pool.storage}
          open={deleteOpen}
          onOpenChange={setDeleteOpen}
          onDeleted={() => {
            void navigate(`/storage?cluster=${clusterId}`);
          }}
        />
      )}
      {hasVztmpl && (
        <ApplianceBrowserDialog
          clusterId={clusterId}
          storageId={pool.id}
          storageName={pool.storage}
          open={appliancesOpen}
          onOpenChange={setAppliancesOpen}
        />
      )}
      {canPullOCI && (
        <OCIPullDialog
          clusterId={clusterId}
          storageId={pool.id}
          open={ociOpen}
          onOpenChange={setOciOpen}
        />
      )}
    </div>
  );
}
