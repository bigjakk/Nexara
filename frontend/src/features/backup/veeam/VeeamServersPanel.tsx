import { useState } from "react";
import { DatabaseBackup } from "lucide-react";
import { Skeleton } from "@/components/ui/skeleton";
import { ApiClientError } from "@/lib/api-client";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  useVeeamServers,
  useVeeamJobs,
  useVeeamSessions,
  useVeeamRepositories,
} from "../api/backup-queries";
import type { VeeamServer } from "../types/backup";
import { AddVeeamServerDialog } from "./AddVeeamServerDialog";
import { EditVeeamServerDialog } from "./EditVeeamServerDialog";
import { DeleteVeeamServerDialog } from "./DeleteVeeamServerDialog";
import { VeeamRequirementsNote } from "./VeeamRequirementsNote";
import { VeeamServerTable } from "./VeeamServerTable";
import { VeeamJobTable } from "./VeeamJobTable";
import { VeeamSessionTable } from "./VeeamSessionTable";
import { VeeamRepositoryCards } from "./VeeamRepositoryCards";
import { VeeamRepositoryChart } from "./VeeamRepositoryChart";

/**
 * Renders a tab's body, distinguishing "still loading", "we could not ask" and
 * "there is genuinely nothing".
 *
 * Collapsing an error into an empty list is the failure mode this exists to
 * prevent: repositories require GLOBAL view:veeam (one repository holds every
 * cluster's backups), so a cluster-scoped viewer gets a 403 — and telling them
 * "no repositories reported by this server" would be a confident lie about
 * their backup infrastructure.
 */
function VeeamTabBody({
  query,
  children,
}: {
  query: { isLoading: boolean; isError: boolean; error: unknown };
  children: React.ReactNode;
}) {
  if (query.isLoading) {
    return <Skeleton className="h-32" />;
  }
  if (query.isError) {
    const status =
      query.error instanceof ApiClientError ? query.error.status : 0;
    return (
      <p className="py-8 text-center text-sm text-destructive">
        {status === 403
          ? "You do not have permission to view this. Repositories span every cluster a Veeam server protects, so they require global Veeam access."
          : `Could not load this from Nexara. ${
              query.error instanceof Error ? query.error.message : ""
            }`}
      </p>
    );
  }
  return <>{children}</>;
}

/**
 * The Veeam tab: the server registry, plus the inventory the collector
 * gathers from whichever server is selected.
 */
export function VeeamServersPanel() {
  const serversQuery = useVeeamServers();
  const servers = serversQuery.data ?? [];

  const [selectedServerId, setSelectedServerId] = useState<string>("");
  const [editTarget, setEditTarget] = useState<VeeamServer | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<VeeamServer | null>(null);

  // Reconciled against the list, not merely defaulted: a bare
  // `selectedServerId || servers[0]` keeps pointing at a server that has been
  // deleted, because the stale id is still non-empty. Every query then targets
  // a 404 and no switcher button is highlighted, with no way back but a
  // reload.
  const selectionIsLive = servers.some((s) => s.id === selectedServerId);
  const activeServerId = selectionIsLive
    ? selectedServerId
    : (servers[0]?.id ?? "");

  const jobsQuery = useVeeamJobs(activeServerId);
  const sessionsQuery = useVeeamSessions(activeServerId);
  const reposQuery = useVeeamRepositories(activeServerId);

  const jobs = jobsQuery.data ?? [];
  const sessions = sessionsQuery.data ?? [];
  const repositories = reposQuery.data ?? [];

  if (serversQuery.isLoading) {
    return (
      <div className="space-y-3">
        <Skeleton className="h-10" />
        <Skeleton className="h-24" />
      </div>
    );
  }

  if (serversQuery.isError) {
    return (
      <p className="text-sm text-destructive">
        Failed to load Veeam servers.{" "}
        {serversQuery.error instanceof Error ? serversQuery.error.message : ""}
      </p>
    );
  }

  if (servers.length === 0) {
    return (
      <div className="space-y-4">
        <div className="rounded-md border bg-muted/50 px-6 py-12 text-center">
          <DatabaseBackup className="mx-auto mb-3 h-10 w-10 text-muted-foreground" />
          <h2 className="text-lg font-medium">No Veeam Servers</h2>
          <p className="mx-auto mt-1 mb-4 max-w-lg text-sm text-muted-foreground">
            Connect a Veeam Backup &amp; Replication server to see the state of
            its Proxmox backup jobs alongside your clusters.
          </p>
          <AddVeeamServerDialog />
        </div>
        <VeeamRequirementsNote />
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-2">
        {servers.length > 1 ? (
          <div className="flex flex-wrap gap-2">
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
        ) : (
          <span />
        )}
        <AddVeeamServerDialog />
      </div>

      <VeeamServerTable
        servers={servers}
        onEdit={setEditTarget}
        onDelete={setDeleteTarget}
      />

      {activeServerId !== "" && (
        <Tabs defaultValue="jobs">
          <TabsList>
            <TabsTrigger value="jobs">Jobs ({jobs.length})</TabsTrigger>
            <TabsTrigger value="sessions">Runs ({sessions.length})</TabsTrigger>
            <TabsTrigger value="repositories">
              Repositories ({repositories.length})
            </TabsTrigger>
          </TabsList>

          <TabsContent value="jobs" className="space-y-4">
            <VeeamTabBody query={jobsQuery}>
              <VeeamJobTable jobs={jobs} scopeKey={activeServerId} />
            </VeeamTabBody>
          </TabsContent>

          <TabsContent value="sessions" className="space-y-4">
            <VeeamTabBody query={sessionsQuery}>
              <VeeamSessionTable
                sessions={sessions}
                scopeKey={activeServerId}
              />
            </VeeamTabBody>
          </TabsContent>

          <TabsContent value="repositories" className="space-y-4">
            <VeeamTabBody query={reposQuery}>
              <VeeamRepositoryCards repositories={repositories} />
              {repositories.map((repo) => (
                <VeeamRepositoryChart
                  key={repo.id}
                  serverId={activeServerId}
                  repository={repo}
                />
              ))}
            </VeeamTabBody>
          </TabsContent>
        </Tabs>
      )}

      {editTarget != null && (
        <EditVeeamServerDialog
          server={editTarget}
          open
          onOpenChange={(open) => {
            if (!open) setEditTarget(null);
          }}
        />
      )}
      {deleteTarget != null && (
        <DeleteVeeamServerDialog
          server={deleteTarget}
          open
          onOpenChange={(open) => {
            if (!open) setDeleteTarget(null);
          }}
        />
      )}
    </div>
  );
}
