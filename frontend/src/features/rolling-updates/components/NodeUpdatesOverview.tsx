import { useState } from "react";
import { Panel } from "@/components/page/Panel";
import { cn } from "@/lib/utils";
import { PackagePreviewTable } from "./PackagePreviewTable";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { useNodePackagePreview } from "../api/rolling-update-queries";
import { useSettledQueryError } from "@/hooks/useSettledQueryError";
import { securityPackageCount } from "../lib/packages";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import { describeError } from "@/lib/api-error";
import { Loader2, ChevronDown } from "lucide-react";

/** A dot and a few words: what the last update check said about a node. */
function UpdateState({
  dot,
  className,
  children,
}: {
  /** The dot's colour classes. */
  dot: string;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <span className={cn("inline-flex items-center gap-[7px]", className)}>
      <span
        aria-hidden="true"
        className={cn("h-2 w-2 shrink-0 rounded-full", dot)}
      />
      {children}
    </span>
  );
}

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
    <li>
      <button
        type="button"
        aria-expanded={expanded}
        className="flex w-full flex-wrap items-center gap-x-4 gap-y-1 px-[18px] py-2.5 text-left text-sm outline-hidden transition-colors hover:bg-foreground/[0.035] focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
        onClick={() => {
          setExpanded(!expanded);
        }}
      >
        <span className="flex min-w-[8.5rem] items-center gap-2 font-medium">
          <ChevronDown
            className={cn(
              "h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform",
              !expanded && "-rotate-90",
            )}
          />
          {nodeName}
        </span>

        {isLoading && settledError === null ? (
          <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
        ) : settledError !== null ? (
          <UpdateState
            dot="bg-red-500"
            className="text-red-700 dark:text-red-400"
          >
            Update check failed
          </UpdateState>
        ) : unread ? (
          <UpdateState
            dot="ring-[1.5px] ring-inset ring-muted-foreground/60"
            className="text-muted-foreground"
          >
            Not checked
          </UpdateState>
        ) : count === 0 ? (
          <UpdateState dot="bg-emerald-500" className="text-muted-foreground">
            Up to date
          </UpdateState>
        ) : (
          <span className="flex flex-wrap items-center gap-x-3">
            <UpdateState dot={securityCount > 0 ? "bg-amber-500" : "bg-data"}>
              {String(count)} update{count !== 1 ? "s" : ""}
            </UpdateState>
            {securityCount > 0 && (
              <span className="text-amber-700 dark:text-amber-400">
                {String(securityCount)} security
              </span>
            )}
          </span>
        )}
      </button>

      {expanded && (
        <div className="bg-foreground/[0.03] py-3">
          {settledError !== null && (
            <div className="px-[18px] text-sm text-muted-foreground">
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
            <p className="px-[18px] text-sm text-muted-foreground">
              {isLoading
                ? "Checking for updates..."
                : unread
                  ? "Nexara has not checked this node for updates yet."
                  : "No packages are pending on this node."}
            </p>
          )}
        </div>
      )}
    </li>
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
    <Panel
      flush
      title="Available updates"
      aside="pending apt packages on each node"
    >
      {nodes !== undefined && nodes.length > 0 ? (
        <ul className="divide-y border-t">
          {nodes.map((node) => (
            <NodePackageRow
              key={node.id}
              clusterId={clusterId}
              nodeName={node.name}
            />
          ))}
        </ul>
      ) : (
        <div className="px-[18px] pb-1">
          <QueryStateNotice
            query={nodesQuery}
            subject="this cluster's nodes"
            empty="No nodes found."
            skeletonClassName="h-20 w-full"
          />
        </div>
      )}
    </Panel>
  );
}
