import { useState } from "react";
import { useTranslation } from "react-i18next";
import { DatabaseBackup, ServerOff } from "lucide-react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { Segmented } from "@/components/ui/segmented";
import { EmptyState } from "@/components/EmptyState";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel } from "@/components/page/Panel";
import { ApiClientError } from "@/lib/api-client";
import { usePermissions } from "@/hooks/usePermissions";
import {
  usePBSServers,
  usePBSDatastoreStatus,
  usePBSSnapshots,
  usePBSSyncJobs,
  usePBSVerifyJobs,
  usePBSTasks,
  useBackupJobs,
  useBackupCoverage,
  useVeeamServers,
} from "../api/backup-queries";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useCanAddPBSServer } from "../hooks/useCanAddPBSServer";
import { DatastoreOverview } from "../components/DatastoreOverview";
import { SnapshotTable } from "../components/SnapshotTable";
import { SyncJobTable } from "../components/SyncJobTable";
import { VerifyJobTable } from "../components/VerifyJobTable";
import { PBSTaskTable } from "../components/PBSTaskTable";
import { PBSHistoryTabContent } from "../components/PBSHistoryTab";
import { HistoryTabTrigger } from "@/features/history/components/HistoryTab";
import { backupHistories } from "@/features/history/lib/backup-histories";
import { AddPBSServerDialog } from "../components/AddPBSServerDialog";
import { EditPBSServerDialog } from "../components/EditPBSServerDialog";
import { DeletePBSServerDialog } from "../components/DeletePBSServerDialog";
import { BackupJobTable } from "../components/BackupJobTable";
import { BackupJobDialog } from "../components/BackupJobDialog";
import { BackupCoverageReport } from "../components/BackupCoverageReport";
import { ServerActions } from "../components/ServerActions";
import { VeeamServersPanel } from "../veeam/VeeamServersPanel";
import { AddVeeamServerDialog } from "../veeam/AddVeeamServerDialog";
import { Badge } from "@/components/ui/badge";
import {
  ProviderHeader,
  ProviderTabLabel,
  ProviderTrademarkNote,
} from "../components/ProviderIdentity";
import {
  coverageStats,
  coverageSummaryLine,
  pbsSummaryLine,
  veeamSummaryLine,
} from "../lib/backup-summary";

type ProviderTab = "pbs" | "veeam" | "coverage";

const PBS_PURPOSE =
  "Datastores, snapshots and jobs on your Proxmox Backup Servers";
const VEEAM_PURPOSE =
  "Proxmox backup jobs on your Veeam Backup & Replication servers";
const COVERAGE_PURPOSE =
  "Which guests are backed up, by which provider, and which are not";

/**
 * The header's sentence while the Veeam tab is open. A component of its own
 * so the servers are read only then, as the tab's panel reads them.
 */
function VeeamSummary() {
  const { data } = useVeeamServers();
  return <>{(data && veeamSummaryLine(data)) ?? VEEAM_PURPOSE}</>;
}

/** The header's sentence while the Coverage tab is open. */
function CoverageSummary() {
  const { data } = useBackupCoverage();
  return (
    <>{data ? coverageSummaryLine(coverageStats(data)) : COVERAGE_PURPOSE}</>
  );
}

/**
 * The header's Add button on the Veeam tab. Offered once the registry has
 * been read, as it always was: a caller who cannot list the servers cannot
 * add one either.
 */
function AddVeeamServerAction() {
  const { isSuccess } = useVeeamServers();
  return isSuccess ? <AddVeeamServerDialog /> : null;
}

export function BackupDashboardPage() {
  const { t } = useTranslation("backup");
  const [providerTab, setProviderTab] = useState<ProviderTab>("pbs");
  const serversQuery = usePBSServers();
  const servers = serversQuery.data ?? [];
  const [selectedServerId, setSelectedServerId] = useState<string>("");
  const [editOpen, setEditOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  // Reconciled against the list, not merely defaulted — the same fix
  // VeeamServersPanel carries. A bare `selectedServerId || servers[0]` keeps
  // pointing at a server that has just been deleted, because the stale id is
  // still non-empty: every query then targets a 404, no switcher button is
  // highlighted, and the whole server line (including the Delete item that
  // caused it) disappears, with no way back but a reload.
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
  const { hasPermission, hasGlobalPermission } = usePermissions();
  const canAddPBS = useCanAddPBSServer();
  // A PBS server's routes take their grant on the cluster it is attached to,
  // and install-wide when it is standalone (requirePBSPerm, PBSHandler).
  const onServer = activeServer?.cluster_id
    ? hasPermission
    : hasGlobalPermission;
  const canEditServer = onServer("manage", "pbs");
  const canDeleteServer = onServer("delete", "pbs");
  const canManageBackups = onServer("manage", "backup");
  const canDeleteBackups = onServer("delete", "backup");

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
   * The state of the selected PBS server, on its line.
   *
   * Deliberately narrower than "the status query failed". `isNotFound` is a
   * 404 — the server really is unreachable. Any other error is most often a
   * token missing Datastore.Audit, where the server is reachable and
   * authenticated and only the permission is wrong; telling that operator to
   * check connectivity sends them the wrong way. The unreachable state further
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
    return <Badge variant="healthy">Connected</Badge>;
  }

  const pbsSummary =
    pbsSummaryLine({
      serverCount: servers.length,
      activeName: activeServer?.name ?? "",
      datastores: dsStatusQuery.data,
      snapshotCount: snapshotsQuery.data?.length,
    }) ?? PBS_PURPOSE;

  return (
    <div className="space-y-5">
      {/* The sentence and the Add button follow the provider tab: each
          provider is a system of its own, and the page has one primary
          action at a time. */}
      <PageHeader
        title="Backup"
        summary={
          providerTab === "pbs" ? (
            pbsSummary
          ) : providerTab === "veeam" ? (
            <VeeamSummary />
          ) : (
            <CoverageSummary />
          )
        }
        actions={
          providerTab === "pbs" ? (
            <AddPBSServerDialog />
          ) : providerTab === "veeam" ? (
            <AddVeeamServerAction />
          ) : undefined
        }
      />

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
      <Tabs
        value={providerTab}
        onValueChange={(value) => {
          setProviderTab(value as ProviderTab);
        }}
      >
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

        <TabsContent value="pbs" className="mt-5 space-y-4">
          {activeServer && (
            <>
              <ProviderHeader
                provider="pbs"
                product="Proxmox Backup Server"
                name={activeServer.name}
                address={activeServer.api_url}
                status={pbsStatusBadge()}
                switcher={
                  servers.length > 1 ? (
                    <Segmented
                      aria-label="Backup server"
                      value={activeServerId}
                      onValueChange={setSelectedServerId}
                      options={servers.map((server) => ({
                        value: server.id,
                        label: server.name,
                      }))}
                    />
                  ) : undefined
                }
                actions={
                  <ServerActions
                    name={activeServer.name}
                    canEdit={canEditServer}
                    canDelete={canDeleteServer}
                    onEdit={() => {
                      setEditOpen(true);
                    }}
                    // A viewer is shown the server's settings, read-only.
                    onView={() => {
                      setEditOpen(true);
                    }}
                    onDelete={() => {
                      setDeleteOpen(true);
                    }}
                  />
                }
              />
              <EditPBSServerDialog
                server={activeServer}
                open={editOpen}
                onOpenChange={setEditOpen}
                readOnly={!canEditServer}
              />
              <DeletePBSServerDialog
                server={activeServer}
                open={deleteOpen}
                onOpenChange={setDeleteOpen}
              />
            </>
          )}

          {(serversQuery.isLoading || dsStatusQuery.isLoading) && (
            <div className="space-y-4">
              {serversQuery.isLoading && <Skeleton className="h-7 w-72" />}
              <Skeleton className="h-9 w-full max-w-xl" />
              <Skeleton className="h-64 w-full rounded-[10px]" />
            </div>
          )}

          {!serversQuery.isLoading && servers.length === 0 && (
            <EmptyState
              icon={DatabaseBackup}
              title="No PBS servers yet"
              description={
                canAddPBS
                  ? "Register a Proxmox Backup Server to see its datastores, snapshots and jobs here, and to restore from it."
                  : t("noPBSServersAddedYet")
              }
            />
          )}

          {isNotFound && (
            <EmptyState
              icon={ServerOff}
              title="PBS server unreachable"
              description={
                // Its credentials are changed with Edit (manage:pbs).
                canEditServer
                  ? "Could not connect to the PBS server. Check credentials and connectivity."
                  : "Could not connect to the PBS server."
              }
            />
          )}

          {dsStatusQuery.isError && !isNotFound && (
            <Panel>
              <p className="text-sm text-destructive">
                Could not load PBS data. The server answered, so this is more
                often a permission than a connectivity problem: check that the
                API token holds Datastore.Audit.
              </p>
            </Panel>
          )}

          {activeServerId &&
            !dsStatusQuery.isLoading &&
            !dsStatusQuery.isError && (
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
                  <HistoryTabTrigger
                    history={backupHistories.pbsServer(activeServerId)}
                  />
                </TabsList>

                <TabsContent value="overview">
                  <DatastoreOverview
                    datastores={datastores}
                    pbsId={activeServerId}
                    canManage={canManageBackups}
                  />
                </TabsContent>

                <TabsContent value="snapshots">
                  <SnapshotTable
                    snapshots={snapshots}
                    pbsId={activeServerId}
                    canManage={canManageBackups}
                    canDelete={canDeleteBackups}
                    canRestore={hasPermission("manage", "backup")}
                  />
                </TabsContent>

                <TabsContent value="schedules">
                  <Panel
                    flush
                    title="Backup schedules"
                    aside={
                      clusters.length === 1 ? clusters[0]?.name : undefined
                    }
                    actions={
                      <>
                        {clusters.length > 1 && (
                          <Segmented
                            aria-label="Cluster"
                            value={activeClusterId}
                            onValueChange={setSelectedClusterId}
                            options={clusters.map((cluster) => ({
                              value: cluster.id,
                              label: cluster.name,
                            }))}
                          />
                        )}
                        {hasPermission("manage", "backup") && (
                          <BackupJobDialog clusterId={activeClusterId} />
                        )}
                      </>
                    }
                  >
                    <BackupJobTable
                      jobs={backupJobs}
                      clusterId={activeClusterId}
                    />
                  </Panel>
                </TabsContent>

                <TabsContent value="replication">
                  <SyncJobTable
                    jobs={syncJobs}
                    pbsId={activeServerId}
                    canRun={canManageBackups}
                  />
                </TabsContent>

                <TabsContent value="verification">
                  <VerifyJobTable
                    jobs={verifyJobs}
                    pbsId={activeServerId}
                    canRun={canManageBackups}
                  />
                </TabsContent>

                <TabsContent value="tasks">
                  <PBSTaskTable tasks={tasks} pbsId={activeServerId} />
                </TabsContent>

                <PBSHistoryTabContent pbsId={activeServerId} />
              </Tabs>
            )}
        </TabsContent>

        <TabsContent value="veeam" className="mt-5 space-y-4">
          <VeeamServersPanel />
        </TabsContent>

        {/* Multi-provider by design: the report asks each cluster's
            /backup-coverage, which answers across PBS and Veeam together and
            models eligibility, so it needs neither provider to be selected. */}
        <TabsContent value="coverage" className="mt-5 space-y-4">
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
