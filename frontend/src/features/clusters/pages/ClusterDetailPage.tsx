import { useParams, Link } from "react-router-dom";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ArrowLeft, Terminal } from "lucide-react";
import { useCluster, useClusterNodes } from "../api/cluster-queries";
import { useConsoleStore } from "@/stores/console-store";
import { formatBytes, formatUptime } from "@/lib/format";
import { ClusterCephTab } from "../components/ClusterCephTab";
import { ClusterNetworksTab } from "../components/ClusterNetworksTab";
import { ClusterFirewallTab } from "../components/ClusterFirewallTab";
import { ClusterDRSTab } from "../components/ClusterDRSTab";

export function ClusterDetailPage() {
  const { clusterId } = useParams<{ clusterId: string }>();
  const clusterQuery = useCluster(clusterId ?? "");
  const nodesQuery = useClusterNodes(clusterId ?? "");
  const addTab = useConsoleStore((s) => s.addTab);
  const showConsole = useConsoleStore((s) => s.showConsole);

  const cluster = clusterQuery.data;
  const nodes = nodesQuery.data ?? [];

  const isLoading = clusterQuery.isLoading || nodesQuery.isLoading;
  const error = clusterQuery.error ?? nodesQuery.error;

  function openNodeShell(nodeName: string) {
    addTab({
      clusterID: clusterId ?? "",
      node: nodeName,
      type: "node_shell",
      label: `Shell: ${nodeName}`,
    });
    showConsole();
  }

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

          <Tabs defaultValue="nodes">
            <TabsList>
              <TabsTrigger value="nodes">Nodes</TabsTrigger>
              <TabsTrigger value="ceph">Ceph</TabsTrigger>
              <TabsTrigger value="networks">Networks</TabsTrigger>
              <TabsTrigger value="firewall">Firewall</TabsTrigger>
              <TabsTrigger value="drs">DRS</TabsTrigger>
            </TabsList>

            <TabsContent value="nodes">
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
                          <TableHead className="text-right">Actions</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {nodes.map((node) => (
                          <TableRow key={node.id}>
                            <TableCell className="font-medium">
                              <Link
                                to={`/clusters/${clusterId ?? ""}/nodes/${node.id}`}
                                className="text-primary hover:underline"
                              >
                                {node.name}
                              </Link>
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
                            <TableCell className="text-right">
                              <Button
                                size="sm"
                                variant="ghost"
                                className="gap-1"
                                disabled={node.status !== "online"}
                                onClick={() => { openNodeShell(node.name); }}
                                title="Open Shell"
                              >
                                <Terminal className="h-4 w-4" />
                              </Button>
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  )}
                </CardContent>
              </Card>
            </TabsContent>

            <TabsContent value="ceph">
              <ClusterCephTab clusterId={clusterId ?? ""} />
            </TabsContent>

            <TabsContent value="networks">
              <ClusterNetworksTab clusterId={clusterId ?? ""} />
            </TabsContent>

            <TabsContent value="firewall">
              <ClusterFirewallTab clusterId={clusterId ?? ""} />
            </TabsContent>

            <TabsContent value="drs">
              <ClusterDRSTab clusterId={clusterId ?? ""} />
            </TabsContent>
          </Tabs>
        </>
      )}
    </div>
  );
}
