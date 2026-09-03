import { useState } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { ApiClientError } from "@/lib/api-client";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  useVeeamServers,
  useVeeamJobs,
  useVeeamSessions,
  useVeeamRepositories,
  useVeeamPlatforms,
  useVeeamOrphanedObjects,
  useVeeamInfrastructure,
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
import { VeeamPlatformMapping } from "./VeeamPlatformMapping";
import { VeeamOrphanTable } from "./VeeamOrphanTable";
import { VeeamServerStatusBadge } from "./VeeamServerStatusBadge";
import { PROVIDER_FILL_CLASSES } from "../components/provider-accents";
import { ProviderBadge, ProviderHeader } from "../components/ProviderIdentity";

/**
 * Renders a tab's body, distinguishing "still loading", "we could not ask" and
 * "there is genuinely nothing".
 *
 * Collapsing an error into an empty list is the failure mode this exists to
 * prevent. Several of these tabs require GLOBAL view:veeam because what they
 * show spans every cluster a server protects, so a cluster-scoped viewer gets
 * a 403 — and telling them "no repositories reported by this server" would be
 * a confident lie about their backup infrastructure.
 *
 * `resource` names what the tab was actually asking for. Without it every tab
 * inherited the copy written for the first one and explained a 403 on the
 * platform list in terms of repositories.
 */
function VeeamTabBody({
  query,
  resource,
  children,
}: {
  query: { isLoading: boolean; isError: boolean; error: unknown };
  resource: string;
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
          ? `You do not have permission to view ${resource}. This spans every cluster the Veeam server protects, so it requires global Veeam access.`
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

  const activeServer = servers.find((s) => s.id === activeServerId);

  const jobsQuery = useVeeamJobs(activeServerId);
  const sessionsQuery = useVeeamSessions(activeServerId);
  const reposQuery = useVeeamRepositories(activeServerId);
  const platformsQuery = useVeeamPlatforms(activeServerId);
  const orphansQuery = useVeeamOrphanedObjects(activeServerId);
  const infrastructureQuery = useVeeamInfrastructure(activeServerId);

  const jobs = jobsQuery.data ?? [];
  const sessions = sessionsQuery.data ?? [];
  const repositories = reposQuery.data ?? [];
  const platforms = platformsQuery.data ?? [];
  const orphans = orphansQuery.data ?? [];
  const infrastructure = infrastructureQuery.data ?? [];
  // Surfaced on the tab itself: an unmapped connection is not a cosmetic gap.
  // Its guests are absent from backup coverage entirely and invisible to
  // anyone without global Veeam access, and nothing else on this page says so.
  const unmappedCount = platforms.filter((p) => p.cluster_id === null).length;

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
          <div className="mb-3 flex justify-center">
            <ProviderBadge provider="veeam" size="lg" />
          </div>
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
      {/* Says which product's state the tables below belong to. The tab label
          used to be the only thing that did, and it scrolls out of view on a
          narrow viewport. */}
      <ProviderHeader
        provider="veeam"
        title="Veeam Backup & Replication"
        subtitle={
          activeServer != null
            ? [
                activeServer.base_url,
                activeServer.product_version !== ""
                  ? `v${activeServer.product_version}`
                  : null,
                activeServer.license_edition !== ""
                  ? activeServer.license_edition
                  : null,
              ]
                .filter((part): part is string => part != null && part !== "")
                .join(" · ")
            : ""
        }
        status={
          activeServer != null && (
            <VeeamServerStatusBadge server={activeServer} />
          )
        }
        actions={<AddVeeamServerDialog />}
      />

      {servers.length > 1 && (
        <div className="flex flex-wrap gap-2">
          {servers.map((server) => (
            <button
              key={server.id}
              aria-pressed={activeServerId === server.id}
              onClick={() => {
                setSelectedServerId(server.id);
              }}
              className={`rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
                activeServerId === server.id
                  ? PROVIDER_FILL_CLASSES.veeam
                  : "bg-muted text-muted-foreground hover:bg-accent"
              }`}
            >
              {server.name}
            </button>
          ))}
        </div>
      )}

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
            <TabsTrigger value="clusters">
              Clusters
              {unmappedCount > 0 && (
                <span className="ml-1.5 rounded-full bg-amber-500/20 px-1.5 text-xs text-amber-600 dark:text-amber-400">
                  {unmappedCount}
                </span>
              )}
            </TabsTrigger>
            <TabsTrigger value="orphans">
              Orphaned ({orphans.length})
            </TabsTrigger>
          </TabsList>

          <TabsContent value="jobs" className="space-y-4">
            <VeeamTabBody query={jobsQuery} resource="backup jobs">
              <VeeamJobTable jobs={jobs} serverId={activeServerId} />
            </VeeamTabBody>
          </TabsContent>

          <TabsContent value="sessions" className="space-y-4">
            <VeeamTabBody query={sessionsQuery} resource="job runs">
              <VeeamSessionTable
                sessions={sessions}
                serverId={activeServerId}
              />
            </VeeamTabBody>
          </TabsContent>

          <TabsContent value="clusters" className="space-y-4">
            <VeeamTabBody query={platformsQuery} resource="cluster mappings">
              {/*
                Keyed on the server, so switching servers remounts and drops
                the previous one's in-flight/error state. The platforms query
                is cached for minutes, so a failure from server A would
                otherwise stay rendered under server B's table.
              */}
              <VeeamPlatformMapping
                key={activeServerId}
                serverId={activeServerId}
                platforms={platforms}
                infrastructure={infrastructure}
              />
            </VeeamTabBody>
          </TabsContent>

          <TabsContent value="orphans" className="space-y-4">
            <VeeamTabBody query={orphansQuery} resource="orphaned backups">
              <VeeamOrphanTable serverId={activeServerId} objects={orphans} />
            </VeeamTabBody>
          </TabsContent>

          <TabsContent value="repositories" className="space-y-4">
            <VeeamTabBody query={reposQuery} resource="repositories">
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
