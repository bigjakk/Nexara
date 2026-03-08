import { useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { PackagePreviewTable } from "./PackagePreviewTable";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { useNodePackagePreview } from "../api/rolling-update-queries";
import type { AptPackage } from "@/types/api";
import {
  Loader2,
  Package,
  ChevronDown,
  ChevronRight,
  CheckCircle,
} from "lucide-react";

function NodePackageRow({ clusterId, nodeName }: { clusterId: string; nodeName: string }) {
  const [expanded, setExpanded] = useState(false);
  const { data: packages, isLoading } = useNodePackagePreview(clusterId, nodeName);

  const count = packages?.length ?? 0;
  const securityCount = packages?.filter(
    (p: AptPackage) => p.Priority === "important" || p.Origin === "Debian-Security",
  ).length ?? 0;

  return (
    <div className="rounded-md border">
      <button
        type="button"
        className="flex w-full items-center gap-3 p-3 text-left hover:bg-accent/50"
        onClick={() => {
          setExpanded(!expanded);
        }}
      >
        {expanded ? (
          <ChevronDown className="h-4 w-4 shrink-0 text-muted-foreground" />
        ) : (
          <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground" />
        )}

        <span className="min-w-[120px] font-medium">{nodeName}</span>

        {isLoading ? (
          <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
        ) : count === 0 ? (
          <Badge variant="outline" className="gap-1">
            <CheckCircle className="h-3 w-3 text-green-500" />
            Up to date
          </Badge>
        ) : (
          <div className="flex items-center gap-2">
            <Badge variant="secondary" className="gap-1">
              <Package className="h-3 w-3" />
              {String(count)} update{count !== 1 ? "s" : ""}
            </Badge>
            {securityCount > 0 && (
              <Badge variant="destructive">
                {String(securityCount)} security
              </Badge>
            )}
          </div>
        )}
      </button>

      {expanded && packages && packages.length > 0 && (
        <div className="border-t px-3 pb-3 pt-2">
          <PackagePreviewTable packages={packages} />
        </div>
      )}
    </div>
  );
}

interface NodeUpdatesOverviewProps {
  clusterId: string;
}

export function NodeUpdatesOverview({ clusterId }: NodeUpdatesOverviewProps) {
  const { data: nodes, isLoading } = useClusterNodes(clusterId);

  if (isLoading) {
    return (
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-base">Available Updates</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex h-20 items-center justify-center">
            <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
          </div>
        </CardContent>
      </Card>
    );
  }

  if (!nodes || nodes.length === 0) {
    return null;
  }

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="text-base">Available Updates</CardTitle>
      </CardHeader>
      <CardContent className="space-y-2">
        {nodes.map((node) => (
          <NodePackageRow
            key={node.id}
            clusterId={clusterId}
            nodeName={node.name}
          />
        ))}
      </CardContent>
    </Card>
  );
}
