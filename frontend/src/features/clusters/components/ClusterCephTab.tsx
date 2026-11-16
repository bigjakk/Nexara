import { useState, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Panel } from "@/components/page/Panel";
import { Segmented } from "@/components/ui/segmented";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent } from "@/components/ui/tabs";
import { CircleAlert, Database } from "lucide-react";
import {
  useCephStatus,
  useCephOSDs,
  useCephPools,
  useCephMonitors,
  useCephFS,
  useCephCrushRules,
} from "@/features/ceph/api/ceph-queries";
import { CephStatusPanel } from "@/features/ceph/components/CephStatusPanel";
import { OSDTable } from "@/features/ceph/components/OSDTable";
import { PoolTable } from "@/features/ceph/components/PoolTable";
import { PoolCreateDialog } from "@/features/ceph/components/PoolCreateDialog";
import { MonitorList } from "@/features/ceph/components/MonitorList";
import { CrushTree } from "@/features/ceph/components/CrushTree";
import { CephMetricsChart } from "@/features/ceph/components/CephMetricsChart";
import { ApiClientError } from "@/lib/api-client";
import { describeError } from "@/lib/api-error";
import { retryState, useSettledQueryError } from "@/hooks/useSettledQueryError";
import { formatExactDateTime } from "@/lib/format";
import { HistoryTabContent } from "@/features/history/components/HistoryTab";
import { useCanViewHistory } from "@/features/history/hooks/useCanViewHistory";
import { clusterHistories } from "@/features/history/lib/cluster-histories";

interface ClusterCephTabProps {
  clusterId: string;
}

interface CephNoticeProps {
  title: string;
  children: ReactNode;
  /** Omitted when a retry cannot get further than the current attempt did. */
  onRetry?: (() => void) | undefined;
  /** True while a fetch is already in flight, which makes a retry a no-op. */
  busy?: boolean | undefined;
}

/**
 * The centred notice shown for every state that has no Ceph dashboard to
 * render. Shared so those states look alike and, more to the point, so the
 * tab always has something to fall back to instead of rendering nothing.
 */
function CephNotice({
  title,
  children,
  onRetry,
  busy = false,
}: CephNoticeProps) {
  return (
    <div className="panel flex flex-col items-center rounded-[10px] px-6 py-14 text-center">
      <div className="flex h-11 w-11 items-center justify-center rounded-[10px] bg-foreground/[0.06]">
        <Database className="h-5 w-5 text-muted-foreground" />
      </div>
      <h2 className="mt-4 text-base font-semibold [font-stretch:108%]">
        {title}
      </h2>
      <div className="mt-1.5 max-w-prose text-sm text-muted-foreground">
        {children}
      </div>
      {/* Query.fetch() early-returns into continueRetry() whenever fetchStatus
          is not "idle" — no dispatch, no request — so a retry offered mid-fetch
          would do literally nothing when clicked. This notice now stays on
          screen through the 60s poll, so that window is reachable; the label
          doubles as the in-flight feedback the skeletons used to give. */}
      {onRetry !== undefined && (
        <Button
          variant="outline"
          size="sm"
          className="mt-6"
          disabled={busy}
          onClick={onRetry}
        >
          {busy ? "Checking..." : "Retry"}
        </Button>
      )}
    </div>
  );
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

  // Without this the notice below would be replaced by skeletons on every 60s
  // poll, for the whole duration of a request that on an unreachable cluster
  // runs to a full Proxmox timeout. See the hook for why.
  const settledError = useSettledQueryError(statusQuery, clusterId);

  const isCephNotFound =
    settledError instanceof ApiClientError &&
    (settledError.status === 404 ||
      settledError.status === 500 ||
      settledError.status === 502);

  // Whatever the API said, verbatim. A cluster with no Ceph and a cluster we
  // cannot reach both arrive here as a 502 — handlers.mapProxmoxError gives a
  // bare Proxmox APIError and a failed connection the same status — so the
  // message is the only thing that tells the operator which one they have.
  const serverMessage = describeError(settledError);

  const retryStatus = () => void statusQuery.refetch();
  const retry = retryState(statusQuery);

  // Which view of the tab is on show. The panels are Radix tab panels, so only
  // the one on show is mounted and reads its data.
  const [view, setView] = useState("overview");
  const history = clusterHistories.ceph(clusterId);
  const canHistory = useCanViewHistory(
    history.permission,
    history.target.clusterId === null,
  );

  // Skeletons are for the first load only: once there is an outcome to show,
  // a background refetch must not take it off the screen.
  if (statusQuery.isLoading && settledError === null) {
    return (
      <div role="status" aria-label="Loading Ceph status" className="space-y-4">
        <Skeleton className="h-[104px] rounded-[10px]" />
        <Skeleton className="h-72 rounded-[10px]" />
      </div>
    );
  }

  // Both error branches are deliberately gated on there being no data to show.
  // TanStack keeps the last good `data` through a failed refetch, so without
  // the guard one dropped 60s poll would replace a live Ceph dashboard with
  // "Ceph is not available" — a flat contradiction of what the operator was
  // looking at a second earlier. With data in hand the banner below says the
  // refresh failed and the dashboard stays up.
  if (settledError !== null && !status) {
    if (isCephNotFound) {
      return (
        <CephNotice
          title="Ceph is not available"
          onRetry={retryStatus}
          busy={retry.busy}
        >
          <p>
            Nexara could not read Ceph status from this cluster. Ceph may not be
            installed or configured, or the cluster may be unreachable.
          </p>
          {serverMessage !== "" && (
            <p className="mt-2 font-mono text-[12.5px] break-words">
              {serverMessage}
            </p>
          )}
        </CephNotice>
      );
    }

    // Everything else — a 403 from the view:ceph check included, so the copy
    // must not presume the cause. The server's message names it.
    return (
      <CephNotice
        title="Could not load Ceph status"
        onRetry={retryStatus}
        busy={retry.busy}
      >
        <p>Nexara could not read Ceph status from this cluster.</p>
        {serverMessage !== "" && (
          <p className="mt-2 font-mono text-[12.5px] break-words">
            {serverMessage}
          </p>
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
        title="Ceph status is unavailable"
        onRetry={retry.offer ? retryStatus : undefined}
        busy={retry.busy}
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

  // A view that is no longer on offer (the last CephFS gone, History no longer
  // granted) falls back to the overview instead of an empty tab.
  const shown =
    (view === "fs" && filesystems.length === 0) ||
    (view === "history" && !canHistory)
      ? "overview"
      : view;

  return (
    <div className="space-y-4">
      {settledError !== null && (
        <p
          role="status"
          className="flex items-start gap-2.5 rounded-[8px] bg-destructive/[0.08] px-3.5 py-2.5 text-sm"
        >
          <CircleAlert
            aria-hidden="true"
            className="mt-0.5 h-4 w-4 shrink-0 text-destructive"
          />
          {/* One interpolation, so no JSX whitespace collapses into the join.
              The timestamp matters: a stale HEALTH_OK is the reading most
              likely to be believed and most likely to be wrong. */}
          <span className="min-w-0 [overflow-wrap:anywhere]">
            {`Ceph status as of ${formatExactDateTime(statusQuery.dataUpdatedAt, "—", "clock")} — the most recent refresh failed${serverMessage !== "" ? `: ${serverMessage}` : "."}`}
          </span>
        </p>
      )}

      <CephStatusPanel status={status} />

      <Tabs value={shown} onValueChange={setView}>
        <Segmented
          aria-label="Ceph view"
          size="md"
          value={shown}
          onValueChange={setView}
          options={[
            { value: "overview", label: "Overview" },
            {
              value: "osds",
              label: `OSDs (${String(status.osdmap.num_osds)})`,
            },
            { value: "pools", label: `Pools (${String(pools.length)})` },
            {
              value: "monitors",
              label: `Monitors (${String(status.monmap.num_mons)})`,
            },
            ...(filesystems.length > 0
              ? [
                  {
                    value: "fs",
                    label: `CephFS (${String(filesystems.length)})`,
                  },
                ]
              : []),
            ...(canHistory ? [{ value: "history", label: "History" }] : []),
          ]}
        />

        <TabsContent
          value="overview"
          aria-label="Overview"
          aria-labelledby={undefined}
          className="space-y-4"
        >
          <CephMetricsChart clusterId={clusterId} status={status} />
          {osds.length > 0 && <CrushTree osds={osds} crushRules={crushRules} />}
        </TabsContent>

        <TabsContent value="osds" aria-label="OSDs" aria-labelledby={undefined}>
          <OSDTable osds={osds} clusterId={clusterId} />
        </TabsContent>

        <TabsContent
          value="pools"
          aria-label="Pools"
          aria-labelledby={undefined}
        >
          <PoolTable
            pools={pools}
            clusterId={clusterId}
            actions={
              <PoolCreateDialog
                clusterId={clusterId}
                osdCount={status.osdmap.num_osds}
              />
            }
          />
        </TabsContent>

        <TabsContent
          value="monitors"
          aria-label="Monitors"
          aria-labelledby={undefined}
        >
          <MonitorList monitors={monitors} clusterId={clusterId} />
        </TabsContent>

        {filesystems.length > 0 && (
          <TabsContent
            value="fs"
            aria-label="CephFS"
            aria-labelledby={undefined}
          >
            <Panel
              flush
              title="CephFS"
              aside={
                filesystems.length === 1
                  ? "1 file system"
                  : `${String(filesystems.length)} file systems`
              }
            >
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Name</TableHead>
                    <TableHead>Metadata pool</TableHead>
                    <TableHead>Data pool</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filesystems.map((fs) => (
                    <TableRow key={fs.name}>
                      <TableCell className="font-medium">{fs.name}</TableCell>
                      <TableCell className="text-muted-foreground">
                        {fs.metadata_pool}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {fs.data_pool}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </Panel>
          </TabsContent>
        )}

        <HistoryTabContent history={history} label="History" />
      </Tabs>
    </div>
  );
}
