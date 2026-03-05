import { useState } from "react";
import { Shield } from "lucide-react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { ApiClientError } from "@/lib/api-client";
import {
  usePBSServers,
  usePBSDatastoreStatus,
  usePBSSnapshots,
  usePBSSyncJobs,
  usePBSVerifyJobs,
} from "../api/backup-queries";
import { DatastoreCards } from "../components/DatastoreCards";
import { SnapshotTable } from "../components/SnapshotTable";
import { SyncJobTable } from "../components/SyncJobTable";
import { VerifyJobTable } from "../components/VerifyJobTable";
import { GCDialog } from "../components/GCDialog";
import { DatastoreChart } from "../components/DatastoreChart";
import { AddPBSServerDialog } from "../components/AddPBSServerDialog";

export function BackupDashboardPage() {
  const serversQuery = usePBSServers();
  const servers = serversQuery.data ?? [];
  const [selectedServerId, setSelectedServerId] = useState<string>("");

  const activeServerId =
    selectedServerId || (servers.length > 0 ? servers[0]?.id ?? "" : "");

  const activeServer = servers.find((s) => s.id === activeServerId);

  const dsStatusQuery = usePBSDatastoreStatus(activeServerId);
  const snapshotsQuery = usePBSSnapshots(activeServerId);
  const syncJobsQuery = usePBSSyncJobs(activeServerId);
  const verifyJobsQuery = usePBSVerifyJobs(activeServerId);

  const datastores = dsStatusQuery.data ?? [];
  const snapshots = snapshotsQuery.data ?? [];
  const syncJobs = syncJobsQuery.data ?? [];
  const verifyJobs = verifyJobsQuery.data ?? [];

  const isNotFound =
    dsStatusQuery.isError &&
    dsStatusQuery.error instanceof ApiClientError &&
    dsStatusQuery.error.status === 404;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Shield className="h-6 w-6 text-primary" />
          <h1 className="text-2xl font-semibold">Backup</h1>
        </div>
        <AddPBSServerDialog />
      </div>

      {serversQuery.isLoading && (
        <div className="grid gap-4 md:grid-cols-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-32" />
          ))}
        </div>
      )}

      {!serversQuery.isLoading && servers.length === 0 && (
        <div className="rounded-md border bg-muted/50 px-6 py-12 text-center">
          <Shield className="mx-auto mb-3 h-10 w-10 text-muted-foreground" />
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
              onClick={() => {
                setSelectedServerId(server.id);
              }}
              className={`rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
                activeServerId === server.id
                  ? "bg-primary text-primary-foreground"
                  : "bg-muted text-muted-foreground hover:bg-accent"
              }`}
            >
              {server.name}
            </button>
          ))}
        </div>
      )}

      {activeServer && servers.length === 1 && (
        <p className="text-sm text-muted-foreground">
          Server: <span className="font-medium">{activeServer.name}</span>
        </p>
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
          Failed to load PBS data. Check server connectivity.
        </p>
      )}

      {activeServerId && !dsStatusQuery.isLoading && !dsStatusQuery.isError && (
        <>
          <DatastoreCards datastores={datastores} />

          <Tabs defaultValue="overview">
            <TabsList>
              <TabsTrigger value="overview">Overview</TabsTrigger>
              <TabsTrigger value="snapshots">
                Snapshots ({snapshots.length})
              </TabsTrigger>
              <TabsTrigger value="replication">
                Replication ({syncJobs.length})
              </TabsTrigger>
              <TabsTrigger value="verification">
                Verification ({verifyJobs.length})
              </TabsTrigger>
            </TabsList>

            <TabsContent value="overview" className="space-y-6">
              <DatastoreChart pbsId={activeServerId} />
              {datastores.length > 0 && (
                <div className="flex flex-wrap gap-2">
                  {datastores.map((ds) => (
                    <GCDialog
                      key={ds.store}
                      pbsId={activeServerId}
                      store={ds.store}
                    />
                  ))}
                </div>
              )}
            </TabsContent>

            <TabsContent value="snapshots" className="space-y-4">
              <SnapshotTable snapshots={snapshots} />
            </TabsContent>

            <TabsContent value="replication" className="space-y-4">
              <SyncJobTable jobs={syncJobs} pbsId={activeServerId} />
            </TabsContent>

            <TabsContent value="verification" className="space-y-4">
              <VerifyJobTable jobs={verifyJobs} pbsId={activeServerId} />
            </TabsContent>
          </Tabs>
        </>
      )}
    </div>
  );
}
