import { Badge } from "@/components/ui/badge";
import type { ClusterResponse } from "@/types/api";

const statusConfig: Record<
  ClusterResponse["status"],
  { label: string; variant: "default" | "secondary" | "destructive" | "outline" }
> = {
  online: { label: "Online", variant: "default" },
  degraded: { label: "Degraded", variant: "outline" },
  offline: { label: "Offline", variant: "destructive" },
  inactive: { label: "Inactive", variant: "secondary" },
  unknown: { label: "Unknown", variant: "secondary" },
};

export function ClusterStatusBadge({
  status,
}: {
  status: ClusterResponse["status"];
}) {
  const cfg = statusConfig[status];
  return <Badge variant={cfg.variant}>{cfg.label}</Badge>;
}
