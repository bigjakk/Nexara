import { useNavigate } from "react-router-dom";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ClusterStatusBadge } from "@/components/ClusterStatusBadge";
import { Server, Monitor, Box, HardDrive } from "lucide-react";
import { formatBytes } from "@/lib/format";
import type { ClusterSummary } from "../api/dashboard-queries";

interface ClusterCardProps {
  summary: ClusterSummary;
}

export function ClusterCard({ summary }: ClusterCardProps) {
  const navigate = useNavigate();
  const { cluster, nodeCount, vmCount, containerCount, storageTotalBytes } =
    summary;

  return (
    <Card
      className="cursor-pointer transition-colors hover:bg-accent/50"
      onClick={() => {
        void navigate(`/clusters/${cluster.id}`);
      }}
      data-testid="cluster-card"
    >
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle className="text-lg">{cluster.name}</CardTitle>
        <ClusterStatusBadge status={cluster.status} />
      </CardHeader>
      <CardContent>
        <div className="grid grid-cols-2 gap-3 text-sm">
          <div className="flex items-center gap-2">
            <Server className="h-4 w-4 text-muted-foreground" />
            <span>
              {nodeCount} {nodeCount === 1 ? "node" : "nodes"}
            </span>
          </div>
          <div className="flex items-center gap-2">
            <Monitor className="h-4 w-4 text-muted-foreground" />
            <span>
              {vmCount} {vmCount === 1 ? "VM" : "VMs"}
            </span>
          </div>
          <div className="flex items-center gap-2">
            <Box className="h-4 w-4 text-muted-foreground" />
            <span>
              {containerCount} {containerCount === 1 ? "CT" : "CTs"}
            </span>
          </div>
          <div className="flex items-center gap-2">
            <HardDrive className="h-4 w-4 text-muted-foreground" />
            <span>{formatBytes(storageTotalBytes)}</span>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
