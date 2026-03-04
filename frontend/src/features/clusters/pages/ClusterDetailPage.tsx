import { useParams, Link } from "react-router-dom";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ArrowLeft } from "lucide-react";
import { useCluster, useClusterNodes } from "../api/cluster-queries";
import { formatBytes, formatUptime } from "@/lib/format";

export function ClusterDetailPage() {
  const { clusterId } = useParams<{ clusterId: string }>();
  const clusterQuery = useCluster(clusterId ?? "");
  const nodesQuery = useClusterNodes(clusterId ?? "");

  const cluster = clusterQuery.data;
  const nodes = nodesQuery.data ?? [];

  const isLoading = clusterQuery.isLoading || nodesQuery.isLoading;
  const error = clusterQuery.error ?? nodesQuery.error;

  return (
    <div className="space-y-6">
      <Link
        to="/"
        className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" />
        Back to Dashboard
      </Link>

      {error != null ? (
        <div className="rounded-lg border border-destructive bg-destructive/10 p-4 text-destructive">
          Failed to load cluster data.
        </div>
      ) : (
        <>
          <Card>
            <CardHeader className="flex flex-row items-center justify-between space-y-0">
              {isLoading ? (
                <Skeleton className="h-7 w-48" />
              ) : (
                <>
                  <CardTitle className="text-xl">
                    {cluster?.name ?? ""}
                  </CardTitle>
                  <Badge
                    variant={
                      cluster?.is_active === true ? "default" : "secondary"
                    }
                  >
                    {cluster?.is_active === true ? "Active" : "Inactive"}
                  </Badge>
                </>
              )}
            </CardHeader>
            {cluster != null && (
              <CardContent>
                <p className="text-sm text-muted-foreground">
                  {cluster.api_url}
                </p>
              </CardContent>
            )}
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Nodes</CardTitle>
            </CardHeader>
            <CardContent>
              {isLoading ? (
                <div className="space-y-2">
                  <Skeleton className="h-10 w-full" />
                  <Skeleton className="h-10 w-full" />
                  <Skeleton className="h-10 w-full" />
                </div>
              ) : nodes.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  No nodes found for this cluster.
                </p>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Name</TableHead>
                      <TableHead>Status</TableHead>
                      <TableHead>CPUs</TableHead>
                      <TableHead>Memory</TableHead>
                      <TableHead>Disk</TableHead>
                      <TableHead>PVE Version</TableHead>
                      <TableHead>Uptime</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {nodes.map((node) => (
                      <TableRow key={node.id}>
                        <TableCell className="font-medium">
                          {node.name}
                        </TableCell>
                        <TableCell>
                          <Badge
                            variant={
                              node.status === "online"
                                ? "default"
                                : "destructive"
                            }
                          >
                            {node.status}
                          </Badge>
                        </TableCell>
                        <TableCell>{node.cpu_count}</TableCell>
                        <TableCell>{formatBytes(node.mem_total)}</TableCell>
                        <TableCell>{formatBytes(node.disk_total)}</TableCell>
                        <TableCell>{node.pve_version}</TableCell>
                        <TableCell>{formatUptime(node.uptime)}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>
        </>
      )}
    </div>
  );
}
