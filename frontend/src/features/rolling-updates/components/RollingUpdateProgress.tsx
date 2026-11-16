import { useState } from "react";
import {
  Loader2,
  Pause,
  Play,
  CheckCircle,
  SkipForward,
  ChevronDown,
  RefreshCw,
} from "lucide-react";
import { DetailField } from "@/components/DetailField";
import {
  Breadcrumbs,
  DetailHeader,
  Fact,
  StatusFact,
} from "@/components/page/DetailHeader";
import { DefGrid, Panel } from "@/components/page/Panel";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { NodeStepBadge } from "./NodeStepBadge";
import { PackagePreviewTable } from "./PackagePreviewTable";
import {
  RollingUpdateJobConfirm,
  type PendingJobAction,
} from "./RollingUpdateJobConfirm";
import {
  useRollingUpdateJob,
  useRollingUpdateNodes,
  useConfirmNodeUpgrade,
  useSkipNode,
  usePauseRollingUpdateJob,
  useResumeRollingUpdateJob,
  useCancelRollingUpdateJob,
} from "../api/rolling-update-queries";
import type {
  RollingUpdateJob,
  RollingUpdateNode,
  AptPackage,
  GuestSnapshot,
  HAConflict,
} from "@/types/api";
import { formatTime } from "@/lib/format";
import { DateTime } from "@/components/DateTime";

interface RollingUpdateProgressProps {
  clusterId: string;
  /** The cluster's name, for the facts line. */
  clusterName?: string | undefined;
  jobId: string;
  canManage: boolean;
}

/** Where the job list lives: the trail back from a job. */
const JOBS_PATH = "/security?tab=rolling-updates";

const STEP_ORDER = [
  "pending",
  "draining",
  "awaiting_upgrade",
  "upgrading",
  "rebooting",
  "health_check",
  "restoring",
  "completed",
] as const;

/** The pipeline as a row of ticks: done plain, the current one blue. */
function StepIndicator({ node }: { node: RollingUpdateNode }) {
  const currentIdx = STEP_ORDER.indexOf(
    node.step as (typeof STEP_ORDER)[number],
  );
  const finished = node.step === "completed";

  return (
    <div className="flex items-center gap-1" aria-hidden="true">
      {STEP_ORDER.map((step, i) => {
        let color = "bg-foreground/10";
        if (node.step === "failed") {
          color = i <= currentIdx ? "bg-red-500" : "bg-foreground/10";
        } else if (node.step === "skipped") {
          color = "bg-foreground/10";
        } else if (i < currentIdx || finished) {
          color = "bg-foreground/35";
        } else if (i === currentIdx) {
          color = "bg-info";
        }

        return (
          <div
            key={step}
            className={`h-1.5 w-5 rounded-full ${color}`}
            title={step.replace(/_/g, " ")}
          />
        );
      })}
    </div>
  );
}

function parseJsonField<T>(raw: unknown): T[] {
  if (Array.isArray(raw)) return raw as T[];
  if (typeof raw === "string") {
    try {
      return JSON.parse(raw) as T[];
    } catch {
      return [];
    }
  }
  return [];
}

/** The times a node passed each stage, in the order it passes them. */
function nodeTimes(node: RollingUpdateNode): Array<[string, string]> {
  const times: Array<[string, string | undefined]> = [
    ["Drain started", node.drain_started_at],
    ["Drain done", node.drain_completed_at],
    ["Upgrade started", node.upgrade_started_at],
    ["Upgrade done", node.upgrade_completed_at],
    [
      "Upgrade confirmed",
      node.upgrade_started_at ? undefined : node.upgrade_confirmed_at,
    ],
    ["Reboot started", node.reboot_started_at],
    ["Reboot done", node.reboot_completed_at],
    ["Health check", node.health_check_at],
    ["Restore done", node.restore_completed_at],
  ];
  return times.flatMap(([label, at]) =>
    at ? [[label, formatTime(at)] as [string, string]] : [],
  );
}

function NodeRow({
  node,
  clusterId,
  jobId,
  jobRunning,
  canManage,
}: {
  node: RollingUpdateNode;
  clusterId: string;
  jobId: string;
  jobRunning: boolean;
  canManage: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  const confirmUpgrade = useConfirmNodeUpgrade();
  const skipNode = useSkipNode();

  const packages = parseJsonField<AptPackage>(node.packages_json);
  const guests = parseJsonField<GuestSnapshot>(node.guests_json);
  const times = nodeTimes(node);

  return (
    <li>
      <div
        className="flex cursor-pointer flex-wrap items-center gap-x-4 gap-y-2 px-[18px] py-2.5 transition-colors hover:bg-foreground/[0.035]"
        onClick={() => {
          setExpanded(!expanded);
        }}
      >
        {/* The click is the row's; the button is there for the keyboard. */}
        <button
          type="button"
          aria-expanded={expanded}
          className="flex min-w-[8.5rem] items-center gap-2 rounded-sm text-left font-medium outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
        >
          <ChevronDown
            className={cn(
              "h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform",
              !expanded && "-rotate-90",
            )}
          />
          {node.node_name}
        </button>
        {/* An in-place upgrade never reboots a node that still has guests on
            it, so a kernel update finishes with the reboot still owed. The
            badge says that instead of "Completed", or the row reads as done. */}
        <NodeStepBadge
          step={node.step}
          rebootRequired={node.reboot_required === true}
          className="min-w-[9.5rem]"
        />
        <StepIndicator node={node} />

        <div className="ml-auto flex items-center gap-2">
          {/* A failed, cancelled or paused job leaves its awaiting node as it
              was; the API refuses to confirm it, so the button is not offered. */}
          {canManage && jobRunning && node.step === "awaiting_upgrade" && (
            <Button
              size="sm"
              onClick={(e) => {
                e.stopPropagation();
                confirmUpgrade.mutate({ clusterId, jobId, nodeId: node.id });
              }}
              disabled={confirmUpgrade.isPending}
            >
              {confirmUpgrade.isPending ? (
                <Loader2 className="mr-1 h-3 w-3 animate-spin" />
              ) : (
                <CheckCircle className="mr-1 h-3 w-3" />
              )}
              Confirm upgrade
            </Button>
          )}
          {canManage && node.step === "pending" && (
            <Button
              variant="ghost"
              size="sm"
              className="text-muted-foreground"
              onClick={(e) => {
                e.stopPropagation();
                skipNode.mutate({ clusterId, jobId, nodeId: node.id });
              }}
              disabled={skipNode.isPending}
            >
              <SkipForward className="mr-1 h-3 w-3" />
              Skip
            </Button>
          )}
        </div>
      </div>

      {expanded && (
        <div className="space-y-4 bg-foreground/[0.03] px-[18px] py-3.5 text-sm">
          {node.failure_reason && (
            <p className="text-red-700 [overflow-wrap:anywhere] dark:text-red-400">
              {node.failure_reason}
            </p>
          )}
          {node.skip_reason && (
            <p className="text-muted-foreground">Skipped: {node.skip_reason}</p>
          )}

          {guests.length > 0 && (
            <div>
              <p className="mb-1.5 text-[12.5px] font-medium text-muted-foreground">
                Guests ({guests.length})
                {guests.some((g) => g.passthrough) && (
                  <span className="ml-2 text-amber-700 dark:text-amber-400">
                    {guests.filter((g) => g.passthrough).length} with
                    passthrough
                  </span>
                )}
              </p>
              <div className="flex flex-wrap gap-1.5">
                {guests.map((g) => (
                  <span
                    key={g.vmid}
                    className={cn(
                      "rounded-[5px] px-1.5 py-0.5 text-[12.5px]",
                      g.passthrough
                        ? "bg-amber-500/15 text-amber-700 dark:text-amber-400"
                        : "bg-foreground/[0.07]",
                    )}
                  >
                    {g.type === "qemu" ? "VM" : "CT"} {g.vmid}
                    {g.name ? ` (${g.name})` : ""}
                    {g.passthrough ? " [passthrough]" : ""}
                  </span>
                ))}
              </div>
            </div>
          )}

          {packages.length > 0 && (
            <div>
              <p className="mb-1 text-[12.5px] font-medium text-muted-foreground">
                Packages ({packages.length})
              </p>
              <div className="-mx-[18px]">
                <PackagePreviewTable packages={packages} />
              </div>
            </div>
          )}

          {node.upgrade_output && (
            <div>
              <p className="mb-1 text-[12.5px] font-medium text-muted-foreground">
                Upgrade output
              </p>
              <pre className="max-h-48 overflow-auto rounded-md bg-foreground/[0.06] p-2.5 font-mono text-xs">
                {node.upgrade_output}
              </pre>
            </div>
          )}

          {times.length > 0 && (
            <DefGrid className="gap-y-2.5">
              {times.map(([label, at]) => (
                <DetailField key={label} label={label}>
                  {at}
                </DetailField>
              ))}
            </DefGrid>
          )}

          {!node.failure_reason &&
            !node.skip_reason &&
            guests.length === 0 &&
            packages.length === 0 &&
            !node.upgrade_output &&
            times.length === 0 && (
              <p className="text-muted-foreground">
                Nothing has been recorded for this node yet.
              </p>
            )}
        </div>
      )}
    </li>
  );
}

function HAWarningsPanel({
  warnings,
  policy,
}: {
  warnings: HAConflict[];
  policy: string;
}) {
  const [expanded, setExpanded] = useState(false);
  const errorCount = warnings.filter((w) => w.severity === "error").length;
  const warnCount = warnings.length - errorCount;
  const counts = [
    ...(errorCount > 0
      ? [`${String(errorCount)} error${errorCount > 1 ? "s" : ""}`]
      : []),
    ...(warnCount > 0
      ? [`${String(warnCount)} warning${warnCount > 1 ? "s" : ""}`]
      : []),
  ].join(", ");

  return (
    <Panel
      flush={expanded}
      title={`HA warnings (${String(warnings.length)})`}
      aside={
        <span
          className={
            errorCount > 0
              ? "text-red-700 dark:text-red-400"
              : "text-amber-700 dark:text-amber-400"
          }
        >
          {counts}
          <span className="text-muted-foreground">, policy: {policy}</span>
        </span>
      }
      actions={
        <Button
          variant="ghost"
          size="sm"
          aria-expanded={expanded}
          className="text-muted-foreground"
          onClick={() => {
            setExpanded(!expanded);
          }}
        >
          {expanded ? "Hide" : "Show"}
        </Button>
      }
      bodyClassName={expanded ? "" : "pb-1"}
    >
      {expanded && (
        <ul className="divide-y border-t">
          {warnings.map((w, i) => {
            const isError = w.severity === "error";
            return (
              <li
                key={`${String(w.vmid)}-${w.node}-${String(i)}`}
                className="flex items-baseline gap-2.5 px-[18px] py-2.5 text-sm"
              >
                <span
                  aria-hidden="true"
                  className={cn(
                    "h-2 w-2 shrink-0 -translate-y-px rounded-full",
                    isError ? "bg-red-500" : "bg-amber-500",
                  )}
                />
                <div className="min-w-0">
                  <p className="[overflow-wrap:anywhere]">{w.message}</p>
                  <p className="mt-0.5 text-[12.5px] text-muted-foreground">
                    {w.node ? `Node: ${w.node} | ` : ""}Source: {w.source}
                    {w.rule_name ? ` (${w.rule_name})` : ""}
                  </p>
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </Panel>
  );
}

/** The dot StatusFact draws for a job's state, and the word beside it. */
const JOB_STATE: Record<
  RollingUpdateJob["status"],
  { dot: string; label: string }
> = {
  pending: { dot: "unknown", label: "Pending" },
  running: { dot: "running", label: "Running" },
  paused: { dot: "paused", label: "Paused" },
  completed: { dot: "unknown", label: "Completed" },
  failed: { dot: "offline", label: "Failed" },
  cancelled: { dot: "unknown", label: "Cancelled" },
};

export function RollingUpdateProgress({
  clusterId,
  clusterName,
  jobId,
  canManage,
}: RollingUpdateProgressProps) {
  const { data: job, isLoading: jobLoading } = useRollingUpdateJob(
    clusterId,
    jobId,
  );
  const { data: nodes, isLoading: nodesLoading } = useRollingUpdateNodes(
    clusterId,
    jobId,
  );
  const pauseJob = usePauseRollingUpdateJob();
  const resumeJob = useResumeRollingUpdateJob();
  const cancelJob = useCancelRollingUpdateJob();
  const [pendingCancel, setPendingCancel] = useState<PendingJobAction | null>(
    null,
  );

  const crumbs = [
    { label: "Security", to: "/security" },
    { label: "Rolling updates", to: JOBS_PATH },
    { label: "Job" },
  ];

  if (jobLoading || nodesLoading) {
    return (
      <div className="space-y-5">
        <Breadcrumbs crumbs={crumbs} />
        <div className="flex h-32 items-center justify-center">
          <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
        </div>
      </div>
    );
  }

  if (!job || !nodes) {
    return (
      <div className="space-y-3">
        <Breadcrumbs crumbs={crumbs} />
        <p className="text-muted-foreground">Job not found</p>
      </div>
    );
  }

  const completedCount = nodes.filter((n) => n.step === "completed").length;
  const failedCount = nodes.filter((n) => n.step === "failed").length;
  const skippedCount = nodes.filter((n) => n.step === "skipped").length;
  const progress = `${String(completedCount)}/${String(nodes.length)} completed${
    failedCount > 0 ? `, ${String(failedCount)} failed` : ""
  }${skippedCount > 0 ? `, ${String(skippedCount)} skipped` : ""}`;
  const state = JOB_STATE[job.status];

  return (
    <div className="space-y-5">
      <DetailHeader
        crumbs={crumbs}
        icon={<RefreshCw />}
        title="Rolling update"
        facts={
          <>
            <StatusFact status={state.dot} label={state.label} nameDot />
            <Fact label="Nodes">{progress}</Fact>
            {clusterName !== undefined && (
              <Fact label="Cluster">{clusterName}</Fact>
            )}
            <Fact label="Guests">
              {job.drain_guests ? "drained first" : "stay running"}
            </Fact>
            <Fact label="At a time">{String(job.parallelism)}</Fact>
            <Fact label="Reboot">{job.reboot_after_update ? "yes" : "no"}</Fact>
            <Fact label="Created">
              <DateTime value={job.created_at} />
            </Fact>
          </>
        }
        actions={
          <>
            {canManage && job.status === "running" && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  pauseJob.mutate({ clusterId, jobId });
                }}
                disabled={pauseJob.isPending}
              >
                <Pause className="mr-1 h-3 w-3" />
                Pause
              </Button>
            )}
            {canManage && job.status === "paused" && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  resumeJob.mutate({ clusterId, jobId });
                }}
                disabled={resumeJob.isPending}
              >
                <Play className="mr-1 h-3 w-3" />
                Resume
              </Button>
            )}
            {canManage &&
              (job.status === "running" || job.status === "paused") && (
                <Button
                  variant="outline"
                  size="sm"
                  className="text-destructive hover:text-destructive"
                  onClick={() => {
                    setPendingCancel({ kind: "cancel", job });
                  }}
                  disabled={cancelJob.isPending}
                >
                  Cancel
                </Button>
              )}
            <RollingUpdateJobConfirm
              pending={pendingCancel}
              onClose={() => {
                setPendingCancel(null);
              }}
              onConfirm={() => {
                cancelJob.mutate({ clusterId, jobId });
              }}
            />
          </>
        }
      />

      <div className="space-y-4">
        {job.failure_reason && (
          <Panel title="Why it failed">
            <p className="text-sm text-red-700 [overflow-wrap:anywhere] dark:text-red-400">
              {job.failure_reason}
            </p>
          </Panel>
        )}

        {job.ha_warnings && job.ha_warnings.length > 0 && (
          <HAWarningsPanel warnings={job.ha_warnings} policy={job.ha_policy} />
        )}

        <Panel flush title="Nodes" aside="in the order they are updated">
          <ul className="divide-y border-t">
            {nodes.map((node) => (
              <NodeRow
                key={node.id}
                node={node}
                clusterId={clusterId}
                jobId={jobId}
                jobRunning={job.status === "running"}
                canManage={canManage}
              />
            ))}
          </ul>
        </Panel>
      </div>
    </div>
  );
}
