import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Database } from "lucide-react";
import {
  useCephStatus,
  useCephOSDs,
  useCephPools,
  useCephMonitors,
  useCephFS,
  useCephCrushRules,
} from "@/features/ceph/api/ceph-queries";
import { CephStatusCards } from "@/features/ceph/components/CephStatusCards";
import { OSDGrid } from "@/features/ceph/components/OSDGrid";
import { OSDTable } from "@/features/ceph/components/OSDTable";
import { PoolTable } from "@/features/ceph/components/PoolTable";
import { PoolCreateDialog } from "@/features/ceph/components/PoolCreateDialog";
import { MonitorList } from "@/features/ceph/components/MonitorList";
import { CrushTree } from "@/features/ceph/components/CrushTree";
import { CephMetricsChart } from "@/features/ceph/components/CephMetricsChart";
import { ApiClientError } from "@/lib/api-client";
import { formatTimestamp } from "@/lib/format";

interface ClusterCephTabProps {
  clusterId: string;
}

interface CephNoticeProps {
  title: string;
  children: ReactNode;
  /** Omitted when a retry cannot get further than the current attempt did. */
  onRetry?: (() => void) | undefined;
}

/**
 * The centred notice shown for every state that has no Ceph dashboard to
 * render. Shared so those states look alike and, more to the point, so the
 * tab always has something to fall back to instead of rendering nothing.
 */
function CephNotice({ title, children, onRetry }: CephNoticeProps) {
  return (
    <div className="rounded-md border bg-muted/50 px-6 py-12 text-center">
      <Database className="mx-auto mb-3 h-10 w-10 text-muted-foreground" />
      <h2 className="text-lg font-medium">{title}</h2>
      <div className="mx-auto mt-1 max-w-prose text-sm text-muted-foreground">
        {children}
      </div>
      {/* No busy state: a retry is only offered where fetchStatus is already
          "idle", and refetching an undefined-data query from idle resets it to
          `pending` (fetchState in query.js), so the click lands on the
          isLoading skeleton before this button could ever show one. */}
      {onRetry !== undefined && (
        <Button variant="outline" size="sm" className="mt-4" onClick={onRetry}>
          Retry
        </Button>
      )}
    </div>
  );
}

/** The server's own message, or a bare status when the body carried none —
 *  a proxy-generated 502 has no JSON body and, over HTTP/2, no statusText. */
function describeError(error: unknown): string {
  if (!(error instanceof ApiClientError)) return "";
  return error.message !== "" ? error.message : `HTTP ${String(error.status)}`;
}

export function ClusterCephTab({ clusterId }: ClusterCephTabProps) {
  const statusQuery = useCephStatus(clusterId);
  const osdsQuery = useCephOSDs(clusterId);
  const poolsQuery = useCephPools(clusterId);
  const monitorsQuery = useCephMonitors(clusterId);
  const fsQuery = useCephFS(clusterId);
  const crushRulesQuery = useCephCrushRules(clusterId);

  const status = statusQuery.data;
  const osds = osdsQuery.data ?? [];
  const pools = poolsQuery.data ?? [];
  const monitors = monitorsQuery.data ?? [];
  const filesystems = fsQuery.data ?? [];
  const crushRules = crushRulesQuery.data ?? [];

  const isCephNotFound =
    statusQuery.isError &&
    statusQuery.error instanceof ApiClientError &&
    (statusQuery.error.status === 404 ||
      statusQuery.error.status === 500 ||
      statusQuery.error.status === 502);

  // Whatever the API said, verbatim. A cluster with no Ceph and a cluster we
  // cannot reach both arrive here as a 502 — handlers.mapProxmoxError gives a
  // bare Proxmox APIError and a failed connection the same status — so the
  // message is the only thing that tells the operator which one they have.
  const serverMessage = describeError(statusQuery.error);

  const retryStatus = () => void statusQuery.refetch();

  if (statusQuery.isLoading) {
    return (
      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
        {Array.from({ length: 4 }).map((_, i) => (
          <Skeleton key={i} className="h-32" />
        ))}
      </div>
    );
  }

  // Both error branches are deliberately gated on there being no data to show.
  // TanStack keeps the last good `data` through a failed refetch, so without
  // the guard one dropped 60s poll would replace a live Ceph dashboard with
  // "Ceph Not Available" — a flat contradiction of what the operator was
  // looking at a second earlier. With data in hand the banner below says the
  // refresh failed and the dashboard stays up.
  if (statusQuery.isError && !status) {
    if (isCephNotFound) {
      return (
        <CephNotice title="Ceph Not Available" onRetry={retryStatus}>
          <p>
            Nexara could not read Ceph status from this cluster. Ceph may not be
            installed or configured, or the cluster may be unreachable.
          </p>
          {serverMessage !== "" && (
            <p className="mt-2 font-mono text-xs break-words">
              {serverMessage}
            </p>
          )}
        </CephNotice>
      );
    }

    // Everything else — a 403 from the view:ceph check included, so the copy
    // must not presume the cause. The server's message names it.
    return (
      <CephNotice title="Failed to Load Ceph Status" onRetry={retryStatus}>
        <p>Nexara could not read Ceph status from this cluster.</p>
        {serverMessage !== "" && (
          <p className="mt-2 font-mono text-xs break-words">{serverMessage}</p>
        )}
      </CephNotice>
    );
  }

  // Terminal fallback. The guards above cover loading, no-Ceph and errored,
  // but those are not every state a query can hold. When TanStack Query pauses
  // a retry it reports isLoading false (isLoading is isPending && isFetching,
  // and a paused fetch is not fetching), isError false and data undefined all
  // at once; a fetch cancelled on unmount reverts to the same shape. Returning
  // null there left the whole tab panel empty with nothing on screen to
  // explain why, so this branch has to say something for any state at all.
  if (!status) {
    return (
      <CephNotice
        title="Ceph Status Unavailable"
        // A paused retryer cannot be restarted from here: Query.fetch() sees a
        // non-idle fetchStatus and early-returns into continueRetry(), which
        // only flips a flag — nothing dispatches and no request goes out. Only
        // queryCache.onFocus()/onOnline() call the retryer's real continue().
        // So offer the button solely from "idle", where refetch() does work.
        onRetry={statusQuery.isPaused ? undefined : retryStatus}
      >
        <p>
          {statusQuery.isPaused
            ? // Both gates in the retryer's canContinue() have to reopen —
              // onlineManager.isOnline() and focusManager.isFocused(), the
              // latter being visibilityState !== "hidden" — and neither one
              // re-renders this tree when it shuts, so name both rather than
              // deducing a single cause that may already be out of date.
              "The last attempt failed and the retry is paused. It resumes on its own once this tab is in the foreground and the browser is online."
            : "Nexara has not read Ceph status from this cluster yet."}
        </p>
      </CephNotice>
    );
  }

  return (
    <div className="space-y-6">
      {statusQuery.isError && (
        <p
          role="status"
          className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {/* One interpolation, so no JSX whitespace collapses into the join.
              The timestamp matters: a stale HEALTH_OK is the reading most
              likely to be believed and most likely to be wrong. */}
          {`Ceph status as of ${formatTimestamp(statusQuery.dataUpdatedAt)} — the most recent refresh failed${serverMessage !== "" ? `: ${serverMessage}` : "."}`}
        </p>
      )}

      <CephStatusCards status={status} />

      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="osds">
            OSDs ({status.osdmap.num_osds})
          </TabsTrigger>
          <TabsTrigger value="pools">Pools ({pools.length})</TabsTrigger>
          <TabsTrigger value="monitors">
            Monitors ({status.monmap.num_mons})
          </TabsTrigger>
          {filesystems.length > 0 && (
            <TabsTrigger value="fs">CephFS ({filesystems.length})</TabsTrigger>
          )}
        </TabsList>

        <TabsContent value="overview" className="space-y-6">
          <CephMetricsChart clusterId={clusterId} status={status} />
          {osds.length > 0 && <CrushTree osds={osds} crushRules={crushRules} />}
        </TabsContent>

        <TabsContent value="osds" className="space-y-4">
          <OSDGrid osds={osds} />
          <OSDTable osds={osds} clusterId={clusterId} />
        </TabsContent>

        <TabsContent value="pools" className="space-y-4">
          <div className="flex justify-end">
            <PoolCreateDialog
              clusterId={clusterId}
              osdCount={status.osdmap.num_osds}
            />
          </div>
          <PoolTable pools={pools} clusterId={clusterId} />
        </TabsContent>

        <TabsContent value="monitors" className="space-y-4">
          <MonitorList monitors={monitors} />
        </TabsContent>

        {filesystems.length > 0 && (
          <TabsContent value="fs" className="space-y-4">
            <div className="rounded-md border">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b bg-muted/30">
                    <th className="px-4 py-2 text-left font-medium">Name</th>
                    <th className="px-4 py-2 text-left font-medium">
                      Metadata Pool
                    </th>
                    <th className="px-4 py-2 text-left font-medium">
                      Data Pool
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {filesystems.map((fs) => (
                    <tr key={fs.name} className="border-b">
                      <td className="px-4 py-2 font-medium">{fs.name}</td>
                      <td className="px-4 py-2 text-muted-foreground">
                        {fs.metadata_pool}
                      </td>
                      <td className="px-4 py-2 text-muted-foreground">
                        {fs.data_pool}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </TabsContent>
        )}
      </Tabs>
    </div>
  );
}
