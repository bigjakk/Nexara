import { useState } from "react";
import { Link } from "react-router-dom";
import { useQueries } from "@tanstack/react-query";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { AddClusterDialog } from "@/features/dashboard/components/AddClusterDialog";
import { EditClusterDialog } from "@/features/clusters/components/EditClusterDialog";
import { DeleteClusterDialog } from "@/features/clusters/components/DeleteClusterDialog";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import type { NodeResponse, VMResponse, ClusterResponse } from "@/types/api";
import { ClusterStatusBadge } from "@/components/ClusterStatusBadge";
import { EmptyState } from "@/components/EmptyState";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel } from "@/components/page/Panel";
import { StatusIcon } from "@/components/StatusIcon";
import { UsageBar } from "@/components/UsageBar";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Skeleton } from "@/components/ui/skeleton";
import { Layers, Pencil, Trash2 } from "lucide-react";
import { useDashboardMetrics } from "@/hooks/useMetrics";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";
import { usageLevel, USAGE_TEXT_CLASS } from "@/lib/usage-level";
import { clustersSummary } from "../lib/cluster-summary";

/** What a cluster's state dot says to a screen reader. */
const STATUS_LABEL: Record<ClusterResponse["status"], string> = {
  online: "Online",
  degraded: "Degraded",
  offline: "Offline",
  inactive: "Inactive",
  unknown: "Unknown",
};

/** A cluster's guests that someone runs: templates are not counted. */
function guestCounts(vms: readonly VMResponse[] | undefined) {
  if (vms === undefined) return undefined;
  const guests = vms.filter((v) => !v.template);
  return {
    total: guests.length,
    running: guests.filter((v) => v.status === "running").length,
  };
}

/** A live load figure: the bar and its reading, a dash until one arrives. */
function LoadCell({
  percent,
  detail,
}: {
  percent: number | undefined;
  detail?: string | undefined;
}) {
  const clamped =
    percent === undefined ? null : Math.min(100, Math.max(0, percent));
  const level = usageLevel(clamped);
  return (
    <div className="min-w-[7rem]">
      <div className="flex items-center gap-2">
        <UsageBar percent={clamped} className="w-16 shrink-0 lg:w-20" />
        <span
          className={cn(
            "text-xs tabular-nums",
            level === "ok" ? "text-muted-foreground" : USAGE_TEXT_CLASS[level],
          )}
        >
          {clamped === null ? "—" : `${String(Math.round(clamped))}%`}
        </span>
      </div>
      {detail !== undefined && (
        <div className="mt-1 whitespace-nowrap text-xs text-muted-foreground/75">
          {detail}
        </div>
      )}
    </div>
  );
}

export function ClustersListPage() {
  const { data: clusters, isLoading, error } = useClusters();
  const [editCluster, setEditCluster] = useState<ClusterResponse | null>(null);
  const [deleteCluster, setDeleteCluster] = useState<ClusterResponse | null>(
    null,
  );

  const nodeQueries = useQueries({
    queries: (clusters ?? []).map((cluster) => ({
      queryKey: ["clusters", cluster.id, "nodes"],
      queryFn: () =>
        apiClient.list<NodeResponse>(
          apiPath`/api/v1/clusters/${cluster.id}/nodes`,
        ),
      enabled: clusters !== undefined && clusters.length > 0,
    })),
  });

  const vmQueries = useQueries({
    queries: (clusters ?? []).map((cluster) => ({
      queryKey: ["clusters", cluster.id, "vms"],
      queryFn: () =>
        apiClient.list<VMResponse>(apiPath`/api/v1/clusters/${cluster.id}/vms`),
      enabled: clusters !== undefined && clusters.length > 0,
    })),
  });

  // Live load for the CPU and memory bars: one metrics subscription per
  // cluster, held while this page is open.
  const metrics = useDashboardMetrics((clusters ?? []).map((c) => c.id));

  const summary = clustersSummary(
    (clusters ?? []).map((cluster, i) => ({
      status: cluster.status,
      nodes: nodeQueries[i]?.data?.length,
      guests: guestCounts(vmQueries[i]?.data)?.total,
    })),
  );

  return (
    <div className="space-y-5">
      <PageHeader
        title="Clusters"
        summary={summary ?? "The Proxmox VE clusters Nexara manages."}
        actions={<AddClusterDialog />}
      />

      {error != null && (
        <Panel>
          <p className="text-sm text-destructive">Failed to load clusters.</p>
        </Panel>
      )}

      {isLoading && (
        <Panel>
          <div className="space-y-2">
            {Array.from({ length: 3 }, (_, i) => (
              <Skeleton key={i} className="h-12 w-full" />
            ))}
          </div>
        </Panel>
      )}

      {!isLoading && !error && clusters?.length === 0 && (
        <div data-testid="empty-state">
          <EmptyState
            icon={Layers}
            title="No clusters"
            description="No clusters configured. Add one to get started."
          />
        </div>
      )}

      {!isLoading && clusters !== undefined && clusters.length > 0 && (
        <Panel flush>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Cluster</TableHead>
                <TableHead className="hidden md:table-cell">Version</TableHead>
                <TableHead className="hidden sm:table-cell">Nodes</TableHead>
                <TableHead className="hidden sm:table-cell">Guests</TableHead>
                <TableHead className="hidden md:table-cell">CPU</TableHead>
                <TableHead className="hidden md:table-cell">Memory</TableHead>
                <TableHead className="w-20">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {clusters.map((cluster, i) => {
                const nodes = nodeQueries[i]?.data;
                const guests = guestCounts(vmQueries[i]?.data);
                const live = metrics.get(cluster.id);
                const nodesOnline =
                  nodes === undefined
                    ? undefined
                    : `${String(nodes.filter((n) => n.status === "online").length)} of ${String(nodes.length)}`;

                return (
                  <TableRow key={cluster.id}>
                    <TableCell>
                      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                        <StatusIcon
                          status={cluster.status}
                          label={STATUS_LABEL[cluster.status]}
                        />
                        <Link
                          to={`/clusters/${cluster.id}`}
                          className="font-medium hover:underline"
                          data-testid={`cluster-link-${cluster.id}`}
                        >
                          {cluster.name}
                        </Link>
                        {/* Online is the dot alone; any other state says so. */}
                        {cluster.status !== "online" && (
                          <ClusterStatusBadge status={cluster.status} />
                        )}
                      </div>
                      <p className="mt-0.5 font-mono text-xs text-muted-foreground [overflow-wrap:anywhere]">
                        {cluster.api_url}
                      </p>
                      {/* A phone has no room for the columns: the counts
                          sit under the name there. */}
                      <p className="mt-1 text-xs text-muted-foreground sm:hidden">
                        {[
                          nodesOnline === undefined
                            ? undefined
                            : `${nodesOnline} nodes online`,
                          guests === undefined
                            ? undefined
                            : `${String(guests.running)} of ${String(guests.total)} guests running`,
                        ]
                          .filter((part) => part !== undefined)
                          .join(", ")}
                      </p>
                    </TableCell>
                    <TableCell className="hidden whitespace-nowrap md:table-cell">
                      {cluster.pve_version !== ""
                        ? `PVE ${cluster.pve_version}`
                        : "—"}
                    </TableCell>
                    <TableCell className="hidden whitespace-nowrap sm:table-cell">
                      {nodesOnline === undefined
                        ? "—"
                        : `${nodesOnline} online`}
                    </TableCell>
                    <TableCell className="hidden sm:table-cell">
                      {guests === undefined ? (
                        "—"
                      ) : (
                        <>
                          <div>{guests.total}</div>
                          <div className="whitespace-nowrap text-xs text-muted-foreground/75">
                            {guests.running} running
                          </div>
                        </>
                      )}
                    </TableCell>
                    <TableCell className="hidden md:table-cell">
                      <LoadCell percent={live?.cpuPercent} />
                    </TableCell>
                    <TableCell className="hidden md:table-cell">
                      <LoadCell
                        percent={live?.memPercent}
                        detail={
                          live !== undefined && live.memTotal > 0
                            ? `${formatBytes(live.memUsed)} of ${formatBytes(live.memTotal)}`
                            : undefined
                        }
                      />
                    </TableCell>
                    <TableCell>
                      <div className="flex justify-end gap-0.5">
                        <Button
                          aria-label={`Edit ${cluster.name}`}
                          variant="ghost"
                          size="icon"
                          className="h-8 w-8 text-muted-foreground hover:text-foreground"
                          onClick={() => {
                            setEditCluster(cluster);
                          }}
                        >
                          <Pencil className="h-4 w-4" />
                        </Button>
                        <Button
                          aria-label={`Delete ${cluster.name}`}
                          variant="ghost"
                          size="icon"
                          className="h-8 w-8 text-muted-foreground hover:text-destructive"
                          onClick={() => {
                            setDeleteCluster(cluster);
                          }}
                        >
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </Panel>
      )}

      {/* Keyed on the cluster, so that the dialog of another is a new one. This
          one seeds its form from its props once and reads the error of its save
          from state it keeps, and it is not held while its request is out: Tab
          walks out of it to the Edit buttons behind the modal, which a pointer
          cannot reach, and Enter on another cluster's would otherwise open that
          cluster over the first one's typed form and failed save. */}
      {editCluster && (
        <EditClusterDialog
          key={editCluster.id}
          cluster={editCluster}
          open={true}
          onOpenChange={(v) => {
            if (!v) setEditCluster(null);
          }}
        />
      )}
      {/* Keyed on the cluster, so that the dialog of another is a new one. This
          one keeps what was typed and ticked in state of its own, among it "also
          delete the Proxmox user and API token Nexara created", and it is not
          held while its request is out: Tab walks out of it to the Delete
          buttons behind the modal, which a pointer cannot reach, and Enter on
          another cluster's would otherwise open that cluster's confirmation with
          the first one's tick already on, and confirming it would delete the
          second cluster's Proxmox user and token. */}
      {deleteCluster && (
        <DeleteClusterDialog
          key={deleteCluster.id}
          cluster={deleteCluster}
          open={true}
          onOpenChange={(v) => {
            if (!v) setDeleteCluster(null);
          }}
        />
      )}
    </div>
  );
}
