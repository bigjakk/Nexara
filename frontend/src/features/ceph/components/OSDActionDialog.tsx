import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { AlertTriangle, ShieldAlert } from "lucide-react";
import { cn } from "@/lib/utils";
import { useCephOSDAction, useCephOSDPreflight } from "../api/ceph-queries";
import type { CephOSD, CephOSDAction, CephOSDPreflight } from "../types/ceph";

interface OSDActionDialogProps {
  clusterId: string;
  osd: CephOSD | null;
  action: CephOSDAction | null;
  onClose: () => void;
}

const actionCopy: Record<CephOSDAction, { title: string; verb: string; description: string }> = {
  in: {
    title: "Mark %s in",
    verb: "Mark In",
    description:
      "Ceph will start backfilling placement groups onto this OSD. Expect recovery traffic until the cluster rebalances.",
  },
  out: {
    title: "Mark %s out",
    verb: "Mark Out",
    description:
      "The daemon keeps running, but Ceph remaps its placement groups onto the remaining OSDs. Use this to drain an OSD before maintenance.",
  },
  start: {
    title: "Start %s",
    verb: "Start",
    description: "Starts the OSD daemon. Ceph marks it up once it finishes booting and peering.",
  },
  stop: {
    title: "Stop %s",
    verb: "Stop",
    description:
      "Stops the OSD daemon. Ceph marks it down immediately, and marks it out once mon_osd_down_out_interval elapses (10 minutes by default), which then triggers a rebalance.",
  },
  restart: {
    title: "Restart %s",
    verb: "Restart",
    description: "Restarts the OSD daemon. It will be down for the duration of the restart.",
  },
};

/**
 * Renders nothing until an action is picked, so the pre-flight request fires
 * when the dialog opens rather than sitting stale in the cache.
 */
export function OSDActionDialog({
  clusterId,
  osd,
  action,
  onClose,
}: OSDActionDialogProps) {
  if (osd === null || action === null) return null;

  return (
    <OSDActionDialogContent
      key={`${String(osd.id)}:${action}`}
      clusterId={clusterId}
      osd={osd}
      action={action}
      onClose={onClose}
    />
  );
}

interface OSDActionDialogContentProps {
  clusterId: string;
  osd: CephOSD;
  action: CephOSDAction;
  onClose: () => void;
}

function OSDActionDialogContent({
  clusterId,
  osd,
  action,
  onClose,
}: OSDActionDialogContentProps) {
  const osdAction = useCephOSDAction();
  const preflight = useCephOSDPreflight(clusterId, osd.id, action);

  const copy = actionCopy[action];
  const osdName = osd.name || `osd.${String(osd.id)}`;
  const assessment = preflight.data;
  const critical = assessment?.severity === "critical";

  function handleConfirm() {
    osdAction.mutate(
      { clusterId, osdId: osd.id, action },
      { onSuccess: onClose },
    );
  }

  function handleOpenChange(next: boolean) {
    if (!next && !osdAction.isPending) {
      osdAction.reset();
      onClose();
    }
  }

  return (
    <Dialog open onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{copy.title.replace("%s", osdName)}</DialogTitle>
          <DialogDescription>{copy.description}</DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          {preflight.isLoading && <Skeleton className="h-28 w-full" />}

          {preflight.isError && (
            <div className="rounded-lg border border-amber-500/50 bg-amber-500/10 p-4 text-sm">
              <div className="flex items-center gap-2 font-medium text-amber-600 dark:text-amber-500">
                <AlertTriangle className="h-4 w-4" />
                Safety check unavailable
              </div>
              <p className="mt-1 text-muted-foreground">
                Could not read the cluster&apos;s current redundancy state, so the
                impact of this action is unknown. {preflight.error.message}
              </p>
            </div>
          )}

          {assessment && <PreflightSummary assessment={assessment} />}

          {osdAction.isError && (
            <p className="text-sm text-destructive">{osdAction.error.message}</p>
          )}
        </div>

        <DialogFooter>
          <Button
            variant="outline"
            onClick={onClose}
            disabled={osdAction.isPending}
          >
            Cancel
          </Button>
          <Button
            variant={assessment?.disruptive ?? true ? "destructive" : "default"}
            onClick={handleConfirm}
            disabled={osdAction.isPending}
          >
            {osdAction.isPending
              ? "Working..."
              : critical
                ? `${copy.verb} anyway`
                : copy.verb}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PreflightSummary({ assessment }: { assessment: CephOSDPreflight }) {
  const critical = assessment.severity === "critical";
  const warning = assessment.severity === "warning";

  return (
    <div className="space-y-3">
      <div className="grid grid-cols-2 gap-3 rounded-lg border p-3 text-sm">
        <Projection
          label="Hosts serving data"
          before={assessment.hosts_serving}
          after={assessment.hosts_serving_after}
        />
        <Projection
          label="OSDs up and in"
          before={assessment.osds_serving}
          after={assessment.osds_serving_after}
          total={assessment.osds_total}
        />
        {assessment.pools.length > 0 && (
          <div className="col-span-2 border-t pt-2">
            <div className="text-xs text-muted-foreground">Pool redundancy</div>
            <div className="mt-1 space-y-0.5">
              {assessment.pools.map((pool) => (
                <div key={pool.pool_name} className="flex justify-between font-mono text-xs">
                  <span>{pool.pool_name}</span>
                  <span className="text-muted-foreground">
                    size {pool.size} / min_size {pool.min_size}
                  </span>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>

      {assessment.warnings.length > 0 && (
        <div
          className={cn(
            "rounded-lg border p-4 text-sm",
            critical
              ? "border-destructive/50 bg-destructive/10"
              : "border-amber-500/50 bg-amber-500/10",
          )}
        >
          <div
            className={cn(
              "flex items-center gap-2 font-medium",
              critical
                ? "text-destructive"
                : "text-amber-600 dark:text-amber-500",
            )}
          >
            {critical ? (
              <ShieldAlert className="h-4 w-4" />
            ) : (
              <AlertTriangle className="h-4 w-4" />
            )}
            {critical ? "This can stall guest I/O" : "Redundancy will be reduced"}
          </div>
          <ul className="mt-2 list-disc space-y-1 pl-5 text-muted-foreground">
            {assessment.warnings.map((text) => (
              <li key={text}>{text}</li>
            ))}
          </ul>
          {(critical || warning) && (
            <p className="mt-2 text-xs text-muted-foreground">
              Assumes Ceph&apos;s default host-level failure domain. Clusters using
              an OSD-level or rack-level domain may differ.
            </p>
          )}
        </div>
      )}
    </div>
  );
}

function Projection({
  label,
  before,
  after,
  total,
}: {
  label: string;
  before: number;
  after: number;
  total?: number;
}) {
  const drops = after < before;

  return (
    <div>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-0.5 font-mono">
        <span>{before}</span>
        <span className="mx-1 text-muted-foreground">&rarr;</span>
        <span className={cn(drops && "font-semibold text-amber-600 dark:text-amber-500")}>
          {after}
        </span>
        {total !== undefined && (
          <span className="ml-1 text-xs text-muted-foreground">of {total}</span>
        )}
      </div>
    </div>
  );
}
