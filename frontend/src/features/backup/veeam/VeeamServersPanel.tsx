import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ServerCog } from "lucide-react";
import { Skeleton } from "@/components/ui/skeleton";
import { Segmented } from "@/components/ui/segmented";
import { EmptyState } from "@/components/EmptyState";
import { Panel } from "@/components/page/Panel";
import { ApiClientError } from "@/lib/api-client";
import { usePermissions } from "@/hooks/usePermissions";
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
import { EditVeeamServerDialog } from "./EditVeeamServerDialog";
import { DeleteVeeamServerDialog } from "./DeleteVeeamServerDialog";
import { VeeamRequirementsNote } from "./VeeamRequirementsNote";
import { VeeamServerTable } from "./VeeamServerTable";
import { VeeamJobTable } from "./VeeamJobTable";
import { VeeamSessionTable } from "./VeeamSessionTable";
import { VeeamRepositoryPanels } from "./VeeamRepositoryPanels";
import { VeeamPlatformMapping } from "./VeeamPlatformMapping";
import { VeeamOrphanTable } from "./VeeamOrphanTable";
import { VeeamServerStatusBadge } from "./VeeamServerStatusBadge";
import { veeamServerStatus } from "./veeam-server-status";
import { ProviderHeader } from "../components/ProviderIdentity";
import { ServerActions } from "../components/ServerActions";

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
    return <Skeleton className="h-32 rounded-[10px]" />;
  }
  if (query.isError) {
    const status =
      query.error instanceof ApiClientError ? query.error.status : 0;
    return (
      <Panel>
        <p className="py-6 text-center text-sm text-destructive">
          {status === 403
            ? `You do not have permission to view ${resource}. This spans every cluster the Veeam server protects, so it requires global Veeam access.`
            : `Could not load this from Nexara. ${
                query.error instanceof Error ? query.error.message : ""
              }`}
        </p>
      </Panel>
    );
  }
  return <>{children}</>;
}

/**
 * The Veeam tab: the server registry, plus the inventory the collector
 * gathers from whichever server is selected.
 */
export function VeeamServersPanel() {
  const { t } = useTranslation("backup");
  const serversQuery = useVeeamServers();
  const servers = serversQuery.data ?? [];
  // The server registry's writes are install-wide checks: adding and editing
  // take manage:veeam, removing delete:veeam.
  const { hasGlobalPermission } = usePermissions();
  const canManage = hasGlobalPermission("manage", "veeam");
  const canDelete = hasGlobalPermission("delete", "veeam");

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
      <div className="space-y-4">
        <Skeleton className="h-7 w-72" />
        <Skeleton className="h-9 w-full max-w-xl" />
        <Skeleton className="h-40 w-full rounded-[10px]" />
      </div>
    );
  }

  if (serversQuery.isError) {
    return (
      <Panel>
        <p className="text-sm text-destructive">
          Failed to load Veeam servers.{" "}
          {serversQuery.error instanceof Error
            ? serversQuery.error.message
            : ""}
        </p>
      </Panel>
    );
  }

  if (servers.length === 0) {
    return (
      <div className="space-y-4">
        <EmptyState
          icon={ServerCog}
          title="No Veeam servers yet"
          description={
            canManage
              ? "Connect a Veeam Backup & Replication server to see the state of its Proxmox backup jobs alongside your clusters."
              : t("noVeeamServersAddedYet")
          }
        />
        <VeeamRequirementsNote />
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {/* Says which server's state the tables below belong to: the tab strip
          above only names the product, and it scrolls out of view on a narrow
          viewport. */}
      {activeServer != null && (
        <ProviderHeader
          provider="veeam"
          product="Veeam Backup & Replication"
          name={activeServer.name}
          address={activeServer.base_url}
          facts={[
            activeServer.product_version !== ""
              ? `v${activeServer.product_version}`
              : "",
            activeServer.license_edition,
          ]}
          status={<VeeamServerStatusBadge server={activeServer} />}
          switcher={
            servers.length > 1 ? (
              <Segmented
                aria-label="Veeam server"
                value={activeServerId}
                onValueChange={setSelectedServerId}
                options={servers.map((server) => ({
                  value: server.id,
                  label:
                    veeamServerStatus(server) === "Error" ? (
                      // A server that is failing says so on its segment: the
                      // line above only speaks for the one selected.
                      <>
                        <span
                          aria-hidden="true"
                          className="h-1.5 w-1.5 rounded-full bg-red-500"
                        />
                        {server.name}
                      </>
                    ) : (
                      server.name
                    ),
                  title:
                    veeamServerStatus(server) === "Error"
                      ? `${server.name}: the last sync failed`
                      : undefined,
                }))}
              />
            ) : undefined
          }
          actions={
            <ServerActions
              name={activeServer.name}
              canEdit={canManage}
              canDelete={canDelete}
              onEdit={() => {
                setEditTarget(activeServer);
              }}
              onDelete={() => {
                setDeleteTarget(activeServer);
              }}
            />
          }
        />
      )}

      {/* The reason a server is in Error, where it is seen without opening
          the Servers tab. */}
      {activeServer != null &&
        activeServer.enabled &&
        activeServer.last_sync_error !== "" && (
          <p className="text-sm text-destructive">
            Last sync failed: {activeServer.last_sync_error}
          </p>
        )}

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
                <span className="ml-1.5 rounded-full bg-amber-500/20 px-1.5 text-xs text-amber-700 dark:text-amber-400">
                  {unmappedCount}
                </span>
              )}
            </TabsTrigger>
            <TabsTrigger value="orphans">
              Orphaned ({orphans.length})
            </TabsTrigger>
            <TabsTrigger value="servers">
              Servers ({servers.length})
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
              <VeeamRepositoryPanels
                serverId={activeServerId}
                repositories={repositories}
              />
            </VeeamTabBody>
          </TabsContent>

          {/* Every registered server, whichever is selected above: where a
              connection is tested and its details are read. */}
          <TabsContent value="servers" className="space-y-4">
            <VeeamServerTable
              servers={servers}
              onEdit={setEditTarget}
              onDelete={setDeleteTarget}
            />
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
