import { useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { PackagePreviewTable } from "./PackagePreviewTable";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { useNodePackagePreview } from "../api/rolling-update-queries";
import { useSettledQueryError } from "@/hooks/useSettledQueryError";
import { securityPackageCount } from "../lib/packages";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import { describeError } from "@/lib/api-error";
import {
  Loader2,
  Package,
  ChevronDown,
  ChevronRight,
  CheckCircle,
  AlertTriangle,
  HelpCircle,
} from "lucide-react";

function NodePackageRow({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const [expanded, setExpanded] = useState(false);
  const query = useNodePackagePreview(clusterId, nodeName);
  const { data: packages, isLoading } = query;
  // This badge is the only thing an operator reads before deciding a node is
  // patched, so it must never answer for a read that did not happen. Without
  // the failure and unread branches below, a token missing Sys.Audit on the
  // node — or a retry paused behind a backgrounded tab — falls through
  // `count === 0` into a green "Up to date" for a node nobody has checked.
  const settledError = useSettledQueryError(query);
  const serverMessage = describeError(settledError);
  const unread = settledError === null && packages === undefined;

  const count = packages?.length ?? 0;
  const securityCount = securityPackageCount(packages);

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

        {isLoading && settledError === null ? (
          <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
        ) : settledError !== null ? (
          <Badge
            variant="outline"
            className="gap-1 border-destructive/40 text-destructive"
          >
            <AlertTriangle className="h-3 w-3" />
            Update check failed
          </Badge>
        ) : unread ? (
          <Badge variant="outline" className="gap-1 text-muted-foreground">
            <HelpCircle className="h-3 w-3" />
            Not checked
          </Badge>
        ) : count === 0 ? (
          <Badge variant="outline" className="gap-1">
            <CheckCircle className="h-3 w-3 text-emerald-500" />
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

      {expanded && (
        <div className="border-t px-3 pb-3 pt-2">
          {settledError !== null && (
            <div className="text-sm text-muted-foreground">
              <p>
                {/* A previous check can have succeeded before this one failed,
                    and TanStack keeps its rows. Saying the patch level is
                    unknown directly above that list would contradict it. */}
                {packages === undefined
                  ? "Nexara could not read the pending updates for this node. Its patch level is unknown — the node may be unreachable, or the cluster token may lack Sys.Audit on it."
                  : "The most recent update check for this node failed. The list below is from the last check that succeeded, so it may be out of date."}
              </p>
              {serverMessage !== "" && (
                <p className="mt-1 font-mono text-xs break-words text-destructive">
                  {serverMessage}
                </p>
              )}
            </div>
          )}

          {packages !== undefined && packages.length > 0 && (
            <div className={settledError !== null ? "mt-3" : undefined}>
              <PackagePreviewTable packages={packages} />
            </div>
          )}

          {/* Expanding a row must never open an empty box: the states with
              nothing to list still owe the operator a sentence. */}
          {settledError === null && (packages === undefined || count === 0) && (
            <p className="text-sm text-muted-foreground">
              {isLoading
                ? "Checking for updates..."
                : unread
                  ? "Nexara has not checked this node for updates yet."
                  : "No packages are pending on this node."}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

interface NodeUpdatesOverviewProps {
  clusterId: string;
}

export function NodeUpdatesOverview({ clusterId }: NodeUpdatesOverviewProps) {
  const nodesQuery = useClusterNodes(clusterId);
  const nodes = nodesQuery.data;

  // A cluster that really has no nodes has nothing to report here, so the card
  // stays hidden. Every other outcome, a failed read included, keeps the card
  // and goes through the notice below — vanishing was how this card used to
  // report that it could not list the nodes.
  if (nodesQuery.isSuccess && nodes !== undefined && nodes.length === 0) {
    return null;
  }

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="text-base">Available Updates</CardTitle>
      </CardHeader>
      <CardContent className="space-y-2">
        {nodes !== undefined && nodes.length > 0 ? (
          nodes.map((node) => (
            <NodePackageRow
              key={node.id}
              clusterId={clusterId}
              nodeName={node.name}
            />
          ))
        ) : (
          <QueryStateNotice
            query={nodesQuery}
            subject="this cluster's nodes"
            empty="No nodes found."
            skeletonClassName="h-20 w-full"
          />
        )}
      </CardContent>
    </Card>
  );
}
