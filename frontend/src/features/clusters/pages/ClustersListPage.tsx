import { Link } from "react-router-dom";
import { useQueries } from "@tanstack/react-query";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { AddClusterDialog } from "@/features/dashboard/components/AddClusterDialog";
import { apiClient } from "@/lib/api-client";
import type { NodeResponse, VMResponse } from "@/types/api";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Skeleton } from "@/components/ui/skeleton";

export function ClustersListPage() {
  const { data: clusters, isLoading, error } = useClusters();

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
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}
