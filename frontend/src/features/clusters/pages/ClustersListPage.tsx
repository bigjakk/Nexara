import { useState } from "react";
import { Link } from "react-router-dom";
import { useQueries } from "@tanstack/react-query";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { AddClusterDialog } from "@/features/dashboard/components/AddClusterDialog";
import { EditClusterDialog } from "@/features/clusters/components/EditClusterDialog";
import { DeleteClusterDialog } from "@/features/clusters/components/DeleteClusterDialog";
import { apiClient } from "@/lib/api-client";
import type { NodeResponse, VMResponse, ClusterResponse } from "@/types/api";
import { Badge } from "@/components/ui/badge";
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
import { Pencil, Trash2 } from "lucide-react";

export function ClustersListPage() {
  const { data: clusters, isLoading, error } = useClusters();
  const [editCluster, setEditCluster] = useState<ClusterResponse | null>(null);
  const [deleteCluster, setDeleteCluster] = useState<ClusterResponse | null>(null);

  const nodeQueries = useQueries({
    queries: (clusters ?? []).map((cluster) => ({
      queryKey: ["clusters", cluster.id, "nodes"],
      queryFn: () =>
        apiClient.get<NodeResponse[]>(`/api/v1/clusters/${cluster.id}/nodes`),
      enabled: clusters !== undefined && clusters.length > 0,
    })),
  });

  const vmQueries = useQueries({
    queries: (clusters ?? []).map((cluster) => ({
      queryKey: ["clusters", cluster.id, "vms"],
      queryFn: () =>
        apiClient.get<VMResponse[]>(`/api/v1/clusters/${cluster.id}/vms`),
      enabled: clusters !== undefined && clusters.length > 0,
    })),
  });

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Clusters</h1>
        <AddClusterDialog />
      </div>

      {error != null && (
        <div className="rounded-lg border border-destructive bg-destructive/10 p-4 text-destructive">
          Failed to load clusters.
        </div>
      )}

      {isLoading && (
        <div className="space-y-2">
          {Array.from({ length: 3 }, (_, i) => (
            <Skeleton key={i} className="h-12 w-full" />
          ))}
        </div>
      )}

      {!isLoading && !error && clusters?.length === 0 && (
        <div
          className="rounded-lg border border-dashed p-8 text-center text-muted-foreground"
          data-testid="empty-state"
        >
          No clusters configured. Add one to get started.
        </div>
      )}

      {!isLoading && clusters !== undefined && clusters.length > 0 && (
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Nodes</TableHead>
                <TableHead className="text-right">VMs</TableHead>
                <TableHead>API URL</TableHead>
                <TableHead className="w-24" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {clusters.map((cluster, i) => {
                const nodes = nodeQueries[i]?.data;
                const vms = vmQueries[i]?.data;
                const vmCount = vms?.filter((v) => v.type === "qemu").length;

                return (
                  <TableRow key={cluster.id}>
                    <TableCell>
                      <Link
                        to={`/clusters/${cluster.id}`}
                        className="font-medium text-primary hover:underline"
                        data-testid={`cluster-link-${cluster.id}`}
                      >
                        {cluster.name}
                      </Link>
                    </TableCell>
                    <TableCell>
                      <Badge variant={cluster.is_active ? "default" : "secondary"}>
                        {cluster.is_active ? "Active" : "Inactive"}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-right">
                      {nodes !== undefined ? nodes.length : "—"}
                    </TableCell>
                    <TableCell className="text-right">
                      {vmCount !== undefined ? vmCount : "—"}
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {cluster.api_url}
                    </TableCell>
                    <TableCell>
                      <div className="flex gap-1">
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7"
                          onClick={() => { setEditCluster(cluster); }}
                        >
                          <Pencil className="h-3.5 w-3.5" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7"
                          onClick={() => { setDeleteCluster(cluster); }}
                        >
                          <Trash2 className="h-3.5 w-3.5 text-destructive" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}

      {editCluster && (
        <EditClusterDialog
          cluster={editCluster}
          open={true}
          onOpenChange={(v) => { if (!v) setEditCluster(null); }}
        />
      )}
      {deleteCluster && (
        <DeleteClusterDialog
          cluster={deleteCluster}
          open={true}
          onOpenChange={(v) => { if (!v) setDeleteCluster(null); }}
        />
      )}
    </div>
  );
}
