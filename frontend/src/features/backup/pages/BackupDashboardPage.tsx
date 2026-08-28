import { useState } from "react";
import { Shield, Pencil, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { ApiClientError } from "@/lib/api-client";
import {
  usePBSServers,
  usePBSDatastoreStatus,
  usePBSSnapshots,
  usePBSSyncJobs,
  usePBSVerifyJobs,
  usePBSTasks,
  useBackupJobs,
} from "../api/backup-queries";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { DatastoreCards } from "../components/DatastoreCards";
import { SnapshotTable } from "../components/SnapshotTable";
import { SyncJobTable } from "../components/SyncJobTable";
import { VerifyJobTable } from "../components/VerifyJobTable";
import { PBSTaskTable } from "../components/PBSTaskTable";
import { GCDialog } from "../components/GCDialog";
import { PruneDialog } from "../components/PruneDialog";
import { DatastoreChart } from "../components/DatastoreChart";
import { AddPBSServerDialog } from "../components/AddPBSServerDialog";
import { EditPBSServerDialog } from "../components/EditPBSServerDialog";
import { DeletePBSServerDialog } from "../components/DeletePBSServerDialog";
import { BackupJobTable } from "../components/BackupJobTable";
import { BackupJobDialog } from "../components/BackupJobDialog";
import { DatastoreIOChart } from "../components/DatastoreIOChart";
import { DatastoreConfigCard } from "../components/DatastoreConfigCard";
import { CapacityForecastChart } from "../components/CapacityForecastChart";
import { BackupCoverageReport } from "../components/BackupCoverageReport";
import { VeeamServersPanel } from "../veeam/VeeamServersPanel";
import { Badge } from "@/components/ui/badge";
import { PROVIDER_FILL_CLASSES } from "../components/provider-accents";
import {
  ProviderBadge,
  ProviderHeader,
  ProviderTabLabel,
  ProviderTrademarkNote,
} from "../components/ProviderIdentity";

export function BackupDashboardPage() {
  const serversQuery = usePBSServers();
  const servers = serversQuery.data ?? [];
  const [selectedServerId, setSelectedServerId] = useState<string>("");
  const [editOpen, setEditOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  // Reconciled against the list, not merely defaulted — the same fix
  // VeeamServersPanel carries. A bare `selectedServerId || servers[0]` keeps
  // pointing at a server that has just been deleted, because the stale id is
  // still non-empty: every query then targets a 404, no switcher button is
  // highlighted, and the whole provider header (including the Delete button
  // that caused it) disappears, with no way back but a reload.
  const selectionIsLive = servers.some((srv) => srv.id === selectedServerId);
  const activeServerId = selectionIsLive
    ? selectedServerId
    : (servers[0]?.id ?? "");

  const activeServer = servers.find((srv) => srv.id === activeServerId);

  const dsStatusQuery = usePBSDatastoreStatus(activeServerId);
  const snapshotsQuery = usePBSSnapshots(activeServerId);
  const syncJobsQuery = usePBSSyncJobs(activeServerId);
  const verifyJobsQuery = usePBSVerifyJobs(activeServerId);
  const tasksQuery = usePBSTasks(activeServerId);

  // Cluster data for backup jobs
  const clustersQuery = useClusters();
  const clusters = clustersQuery.data ?? [];
  const [selectedClusterId, setSelectedClusterId] = useState<string>("");
  // Reconciled for the same reason the server selection above is.
  const clusterSelectionIsLive = clusters.some(
    (c) => c.id === selectedClusterId,
  );
  const activeClusterId = clusterSelectionIsLive
    ? selectedClusterId
    : (clusters[0]?.id ?? "");
  const backupJobsQuery = useBackupJobs(activeClusterId);

  const datastores = dsStatusQuery.data ?? [];
  const snapshots = snapshotsQuery.data ?? [];
  const syncJobs = syncJobsQuery.data ?? [];
  const verifyJobs = verifyJobsQuery.data ?? [];
  const tasks = tasksQuery.data ?? [];
  const backupJobs = backupJobsQuery.data ?? [];

  const isNotFound =
    dsStatusQuery.isError &&
    dsStatusQuery.error instanceof ApiClientError &&
    dsStatusQuery.error.status === 404;

  /**
   * The header badge for the selected PBS server.
   *
   * Deliberately narrower than "the status query failed". `isNotFound` is a
   * 404 — the server really is unreachable. Any other error is most often a
   * token missing Datastore.Audit, where the server is reachable and
   * authenticated and only the permission is wrong; telling that operator to
   * check connectivity sends them the wrong way. The unreachable card further
   * down the tab draws the same distinction.
   */
  function pbsStatusBadge() {
    if (dsStatusQuery.isLoading) {
      // Reserve the space rather than popping a badge in on second paint.
      return <Skeleton className="h-5 w-20" />;
    }
    if (isNotFound) {
      return <Badge variant="destructive">Unreachable</Badge>;
    }
    if (dsStatusQuery.isError) {
      return <Badge variant="secondary">Status unavailable</Badge>;
    }
    return (
      <Badge variant="default" className="bg-emerald-600">
        Connected
      </Badge>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-3">
        <Shield className="h-6 w-6 text-primary" />
        <h1 className="text-2xl font-semibold">Backup</h1>
      </div>

      {/* Provider tabs. PBS and Veeam are independent backup providers with
          nothing in common at the connection level, and either can be
          configured without the other — so Veeam must be reachable even when
          no PBS server exists.

          Coverage sits BESIDE them rather than inside either. It is the one
          view that spans both providers — "which guests are protected, by
          what, and which genuinely aren't" — so filing it under one provider
          made it a claim about that provider alone. It was previously a PBS
          sub-tab nested inside the `activeServerId && !isError` guard, which
          meant a Veeam-only install, or one whose PBS server was unreachable,
          could not reach the unified report at all. */}
      <Tabs defaultValue="pbs">
        <TabsList>
          <TabsTrigger value="pbs">
            <ProviderTabLabel provider="pbs">
              Proxmox Backup Server
            </ProviderTabLabel>
          </TabsTrigger>
          <TabsTrigger value="veeam">
            <ProviderTabLabel provider="veeam">Veeam</ProviderTabLabel>
          </TabsTrigger>
          {/* Left unmarked on purpose. Coverage answers across both providers,
              so wearing either one's accent would make it a claim about that
              provider alone. */}
          <TabsTrigger value="coverage">Coverage</TabsTrigger>
        </TabsList>

        <TabsContent value="pbs" className="space-y-6">
          {activeServer ? (
            <ProviderHeader
              provider="pbs"
              title="Proxmox Backup Server"
              subtitle={[activeServer.name, activeServer.api_url]
                .filter((part) => part !== "")
                .join(" · ")}
              status={pbsStatusBadge()}
              actions={
                <div className="flex items-center gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      setEditOpen(true);
                    }}
                  >
                    <Pencil className="mr-1.5 h-3.5 w-3.5" />
                    Edit
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      setDeleteOpen(true);
                    }}
                  >
                    <Trash2 className="mr-1.5 h-3.5 w-3.5" />
                    Delete
                  </Button>
                  <AddPBSServerDialog />
                </div>
              }
            />
          ) : (
            <div className="flex items-center justify-end gap-2">
              <AddPBSServerDialog />
            </div>
          )}

          {activeServer && (
            <>
              <EditPBSServerDialog
                server={activeServer}
                open={editOpen}
                onOpenChange={setEditOpen}
              />
              <DeletePBSServerDialog
                server={activeServer}
                open={deleteOpen}
                onOpenChange={setDeleteOpen}
              />
            </>
          )}

          {serversQuery.isLoading && (
            <div className="grid gap-4 md:grid-cols-3">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-32" />
              ))}
            </div>
          )}

          {!serversQuery.isLoading && servers.length === 0 && (
            <div className="rounded-md border bg-muted/50 px-6 py-12 text-center">
              <div className="mb-3 flex justify-center">
                <ProviderBadge provider="pbs" size="lg" />
              </div>
              <h2 className="text-lg font-medium">No PBS Servers</h2>
              <p className="mt-1 mb-4 text-sm text-muted-foreground">
                Register a Proxmox Backup Server to manage backups.
              </p>
              <AddPBSServerDialog />
            </div>
          )}

          {servers.length > 1 && (
            <div className="flex gap-2">
              {servers.map((server) => (
                <button
                  key={server.id}
                  aria-pressed={activeServerId === server.id}
                  onClick={() => {
                    setSelectedServerId(server.id);
                  }}
                  className={`rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
                    activeServerId === server.id
                      ? PROVIDER_FILL_CLASSES.pbs
                      : "bg-muted text-muted-foreground hover:bg-accent"
                  }`}
                >
                  {server.name}
                </button>
              ))}
            </div>
          )}

          {dsStatusQuery.isLoading && (
            <div className="grid gap-4 md:grid-cols-3">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-32" />
              ))}
            </div>
          )}

          {isNotFound && (
            <div className="rounded-md border bg-muted/50 px-6 py-12 text-center">
              {/* Muted shield, not the provider tile its sibling empty state
                  wears: the accent marks identity, and this is a failure. */}
              <Shield className="mx-auto mb-3 h-10 w-10 text-muted-foreground" />
              <h2 className="text-lg font-medium">PBS Server Unreachable</h2>
              <p className="mt-1 text-sm text-muted-foreground">
                Could not connect to the PBS server. Check credentials and
                connectivity.
              </p>
            </div>
          )}

          {dsStatusQuery.isError && !isNotFound && (
            <p className="text-sm text-destructive">
              Could not load PBS data. The server answered, so this is more
              often a permission than a connectivity problem — check that the
              API token holds Datastore.Audit.
            </p>
          )}

          {activeServerId &&
            !dsStatusQuery.isLoading &&
            !dsStatusQuery.isError && (
              <>
                <DatastoreCards datastores={datastores} />

                <Tabs defaultValue="overview">
                  <TabsList>
                    <TabsTrigger value="overview">Overview</TabsTrigger>
                    <TabsTrigger value="snapshots">
                      Snapshots ({snapshots.length})
                    </TabsTrigger>
                    <TabsTrigger value="schedules">
                      Schedules ({backupJobs.length})
                    </TabsTrigger>
                    <TabsTrigger value="replication">
                      Replication ({syncJobs.length})
                    </TabsTrigger>
                    <TabsTrigger value="verification">
                      Verification ({verifyJobs.length})
                    </TabsTrigger>
                    <TabsTrigger value="tasks">
                      Tasks ({tasks.length})
                    </TabsTrigger>
                  </TabsList>

                  <TabsContent value="overview" className="space-y-6">
                    <DatastoreChart pbsId={activeServerId} />
                    {datastores.map((ds) => (
                      <DatastoreIOChart
                        key={ds.store}
                        pbsId={activeServerId}
                        store={ds.store}
                      />
                    ))}
                    {datastores.map((ds) => (
                      <CapacityForecastChart
                        key={`forecast-${ds.store}`}
                        pbsId={activeServerId}
                        store={ds.store}
                      />
                    ))}
                    {datastores.map((ds) => (
                      <DatastoreConfigCard
                        key={`config-${ds.store}`}
                        pbsId={activeServerId}
                        store={ds.store}
                      />
                    ))}
                    {datastores.length > 0 && (
                      <div className="flex flex-wrap gap-2">
                        {datastores.map((ds) => (
                          <div key={ds.store} className="flex gap-1">
                            <GCDialog pbsId={activeServerId} store={ds.store} />
                            <PruneDialog
                              pbsId={activeServerId}
                              store={ds.store}
                            />
                          </div>
                        ))}
                      </div>
                    )}
                  </TabsContent>

                  <TabsContent value="snapshots" className="space-y-4">
                    <SnapshotTable
                      snapshots={snapshots}
                      pbsId={activeServerId}
                    />
                  </TabsContent>

                  <TabsContent value="schedules" className="space-y-4">
                    {clusters.length > 1 && (
                      <div className="flex gap-2">
                        {clusters.map((cluster) => (
                          <button
                            key={cluster.id}
                            aria-pressed={activeClusterId === cluster.id}
                            onClick={() => {
                              setSelectedClusterId(cluster.id);
                            }}
                            className={`rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
                              activeClusterId === cluster.id
                                ? "bg-primary text-primary-foreground"
                                : "bg-muted text-muted-foreground hover:bg-accent"
                            }`}
                          >
                            {cluster.name}
                          </button>
                        ))}
                      </div>
                    )}
                    <div className="flex justify-end">
                      <BackupJobDialog clusterId={activeClusterId} />
                    </div>
                    <BackupJobTable
                      jobs={backupJobs}
                      clusterId={activeClusterId}
                    />
                  </TabsContent>

                  <TabsContent value="replication" className="space-y-4">
                    <SyncJobTable jobs={syncJobs} pbsId={activeServerId} />
                  </TabsContent>

                  <TabsContent value="verification" className="space-y-4">
                    <VerifyJobTable jobs={verifyJobs} pbsId={activeServerId} />
                  </TabsContent>

                  <TabsContent value="tasks" className="space-y-4">
                    <PBSTaskTable tasks={tasks} pbsId={activeServerId} />
                  </TabsContent>
                </Tabs>
              </>
            )}
        </TabsContent>

        <TabsContent value="veeam" className="space-y-4">
          <VeeamServersPanel />
        </TabsContent>

        {/* Multi-provider by design: the report asks each cluster's
            /backup-coverage, which answers across PBS and Veeam together and
            models eligibility, so it needs neither provider to be selected. */}
        <TabsContent value="coverage" className="space-y-4">
          <BackupCoverageReport />
        </TabsContent>
      </Tabs>

      {/* Below the tab set, not inside a tab: the Coverage report names Veeam
          in its own right, and a note nested in a provider panel would miss
          that panel's own loading and error returns. */}
      <ProviderTrademarkNote providers={["pbs", "veeam"]} />
    </div>
  );
}
