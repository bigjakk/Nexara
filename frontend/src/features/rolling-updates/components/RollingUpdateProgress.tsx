import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { NodeStepBadge } from "./NodeStepBadge";
import { PackagePreviewTable } from "./PackagePreviewTable";
import {
  useRollingUpdateJob,
  useRollingUpdateNodes,
  useConfirmNodeUpgrade,
  useSkipNode,
  usePauseRollingUpdateJob,
  useResumeRollingUpdateJob,
  useCancelRollingUpdateJob,
} from "../api/rolling-update-queries";
import type { RollingUpdateNode, AptPackage, GuestSnapshot, HAConflict } from "@/types/api";
import {
  Loader2,
  ArrowLeft,
  Pause,
  Play,
  XCircle,
  CheckCircle,
  SkipForward,
  ChevronDown,
  ChevronRight,
  AlertTriangle,
} from "lucide-react";
import { useState } from "react";

interface RollingUpdateProgressProps {
  clusterId: string;
  jobId: string;
  canManage: boolean;
  onBack: () => void;
}

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

function StepIndicator({ node }: { node: RollingUpdateNode }) {
  const currentIdx = STEP_ORDER.indexOf(
    node.step as (typeof STEP_ORDER)[number],
  );

  return (
    <div className="flex items-center gap-1">
      {STEP_ORDER.map((step, i) => {
        let color = "bg-muted";
        if (node.step === "failed") {
          color = i <= currentIdx ? "bg-destructive" : "bg-muted";
        } else if (node.step === "skipped") {
          color = "bg-muted";
        } else if (i < currentIdx) {
          color = "bg-green-500";
        } else if (i === currentIdx) {
          color = "bg-primary";
        }

        return (
          <div
            key={step}
            className={`h-2 w-6 rounded-full ${color}`}
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

function NodeRow({
  node,
  clusterId,
  jobId,
  canManage,
}: {
  node: RollingUpdateNode;
  clusterId: string;
  jobId: string;
  canManage: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  const confirmUpgrade = useConfirmNodeUpgrade();
  const skipNode = useSkipNode();

  const packages = parseJsonField<AptPackage>(node.packages_json);
  const guests = parseJsonField<GuestSnapshot>(node.guests_json);

  return (
    <div className="rounded-md border">
      <div
        className="flex cursor-pointer items-center gap-3 p-3"
        onClick={() => { setExpanded(!expanded); }}
      >
        {expanded ? (
          <ChevronDown className="h-4 w-4 shrink-0 text-muted-foreground" />
        ) : (
          <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground" />
        )}

        <span className="min-w-[120px] font-medium">{node.node_name}</span>
        <NodeStepBadge step={node.step} />
        <StepIndicator node={node} />

        <div className="ml-auto flex items-center gap-2">
          {canManage && node.step === "awaiting_upgrade" && (
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
              Confirm Upgrade
            </Button>
          )}
          {canManage && node.step === "pending" && (
            <Button
              variant="ghost"
              size="sm"
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
        <div className="space-y-3 border-t px-3 pb-3 pt-2">
          {node.failure_reason && (
            <p className="text-sm text-destructive">{node.failure_reason}</p>
          )}
          {node.skip_reason && (
            <p className="text-sm text-muted-foreground">Skipped: {node.skip_reason}</p>
          )}

          {guests.length > 0 && (
            <div>
              <p className="mb-1 text-xs font-medium text-muted-foreground">
                Guests ({guests.length})
                {guests.some((g) => g.passthrough) && (
                  <span className="ml-2 text-yellow-500">
                    {guests.filter((g) => g.passthrough).length} with passthrough
                  </span>
                )}
              </p>
              <div className="flex flex-wrap gap-1">
                {guests.map((g) => (
                  <Badge
                    key={g.vmid}
                    variant={g.passthrough ? "secondary" : "outline"}
                    className={`text-xs ${g.passthrough ? "border-yellow-500/50" : ""}`}
                  >
                    {g.type === "qemu" ? "VM" : "CT"} {g.vmid}
                    {g.name ? ` (${g.name})` : ""}
                    {g.passthrough ? " [passthrough]" : ""}
                  </Badge>
                ))}
              </div>
            </div>
          )}

          {packages.length > 0 && (
            <div>
              <p className="mb-1 text-xs font-medium text-muted-foreground">
                Packages ({packages.length})
              </p>
              <PackagePreviewTable packages={packages} />
            </div>
          )}

          {node.upgrade_output && (
            <div>
              <p className="mb-1 text-xs font-medium text-muted-foreground">
                Upgrade Output
              </p>
              <pre className="max-h-48 overflow-auto rounded-md bg-muted p-2 text-xs">
                {node.upgrade_output}
              </pre>
            </div>
          )}

          <div className="grid grid-cols-2 gap-2 text-xs text-muted-foreground sm:grid-cols-4">
            {node.drain_started_at && (
              <div>
                <span className="font-medium">Drain started: </span>
                {new Date(node.drain_started_at).toLocaleTimeString()}
              </div>
            )}
            {node.drain_completed_at && (
              <div>
                <span className="font-medium">Drain done: </span>
                {new Date(node.drain_completed_at).toLocaleTimeString()}
              </div>
            )}
            {node.upgrade_started_at && (
              <div>
                <span className="font-medium">Upgrade started: </span>
                {new Date(node.upgrade_started_at).toLocaleTimeString()}
              </div>
            )}
            {node.upgrade_completed_at && (
              <div>
                <span className="font-medium">Upgrade done: </span>
                {new Date(node.upgrade_completed_at).toLocaleTimeString()}
              </div>
            )}
            {node.upgrade_confirmed_at && !node.upgrade_started_at && (
              <div>
                <span className="font-medium">Upgrade confirmed: </span>
                {new Date(node.upgrade_confirmed_at).toLocaleTimeString()}
              </div>
            )}
            {node.reboot_started_at && (
              <div>
                <span className="font-medium">Reboot started: </span>
                {new Date(node.reboot_started_at).toLocaleTimeString()}
              </div>
            )}
            {node.reboot_completed_at && (
              <div>
                <span className="font-medium">Reboot done: </span>
                {new Date(node.reboot_completed_at).toLocaleTimeString()}
              </div>
            )}
            {node.health_check_at && (
              <div>
                <span className="font-medium">Health check: </span>
                {new Date(node.health_check_at).toLocaleTimeString()}
              </div>
            )}
            {node.restore_completed_at && (
              <div>
                <span className="font-medium">Restore done: </span>
                {new Date(node.restore_completed_at).toLocaleTimeString()}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function HAWarningsCard({
  warnings,
  policy,
}: {
  warnings: HAConflict[];
  policy: string;
}) {
  const [expanded, setExpanded] = useState(false);
  const errorCount = warnings.filter((w) => w.severity === "error").length;
  const warnCount = warnings.length - errorCount;

  return (
    <Card>
      <div
        className="flex cursor-pointer items-center gap-2 px-4 py-3"
        onClick={() => { setExpanded(!expanded); }}
      >
        <AlertTriangle className="h-4 w-4 shrink-0 text-yellow-500" />
        <span className="text-sm font-medium">
          HA Warnings ({warnings.length})
        </span>
        <Badge variant="outline" className="text-xs">
          Policy: {policy}
        </Badge>
        {errorCount > 0 && (
          <Badge variant="destructive" className="text-xs">
            {errorCount} error{errorCount > 1 ? "s" : ""}
          </Badge>
        )}
        {warnCount > 0 && (
          <Badge variant="outline" className="border-yellow-500/50 text-xs text-yellow-500">
            {warnCount} warning{warnCount > 1 ? "s" : ""}
          </Badge>
        )}
        <div className="ml-auto">
          {expanded ? (
            <ChevronDown className="h-4 w-4 text-muted-foreground" />
          ) : (
            <ChevronRight className="h-4 w-4 text-muted-foreground" />
          )}
        </div>
      </div>
      {expanded && (
        <CardContent className="space-y-2 border-t pt-3">
          {warnings.map((w, i) => {
            const isError = w.severity === "error";
            return (
              <div
                key={`${String(w.vmid)}-${w.node}-${String(i)}`}
                className={`flex items-start gap-2 rounded-md border p-3 text-sm ${
                  isError
                    ? "border-destructive/50 bg-destructive/10"
                    : "border-yellow-500/50 bg-yellow-500/10"
                }`}
              >
                {isError ? (
                  <XCircle className="mt-0.5 h-4 w-4 shrink-0 text-destructive" />
                ) : (
                  <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-yellow-500" />
                )}
                <div>
                  <p>{w.message}</p>
                  <p className="mt-1 text-xs text-muted-foreground">
                    Node: {w.node} | Source: {w.source}
                    {w.rule_name ? ` (${w.rule_name})` : ""}
                  </p>
                </div>
              </div>
            );
          })}
        </CardContent>
      )}
    </Card>
  );
}

export function RollingUpdateProgress({
  clusterId,
  jobId,
  canManage,
  onBack,
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

  if (jobLoading || nodesLoading) {
    return (
      <div className="flex h-32 items-center justify-center">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!job || !nodes) {
    return <p className="text-muted-foreground">Job not found</p>;
  }

  const completedCount = nodes.filter((n) => n.step === "completed").length;
  const failedCount = nodes.filter((n) => n.step === "failed").length;
  const skippedCount = nodes.filter((n) => n.step === "skipped").length;

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <Button variant="ghost" size="sm" onClick={onBack}>
          <ArrowLeft className="mr-1 h-4 w-4" />
          Back
        </Button>

        <Badge
          variant={
            job.status === "failed"
              ? "destructive"
              : job.status === "running"
                ? "default"
                : "outline"
          }
        >
          {job.status}
        </Badge>

        <span className="text-sm text-muted-foreground">
          {completedCount}/{nodes.length} completed
          {failedCount > 0 && `, ${String(failedCount)} failed`}
          {skippedCount > 0 && `, ${String(skippedCount)} skipped`}
        </span>

        <div className="ml-auto flex items-center gap-2">
          {canManage && job.status === "running" && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => { pauseJob.mutate({ clusterId, jobId }); }}
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
              onClick={() => { resumeJob.mutate({ clusterId, jobId }); }}
              disabled={resumeJob.isPending}
            >
              <Play className="mr-1 h-3 w-3" />
              Resume
            </Button>
          )}
          {canManage &&
            (job.status === "running" || job.status === "paused") && (
              <Button
                variant="destructive"
                size="sm"
                onClick={() => { cancelJob.mutate({ clusterId, jobId }); }}
                disabled={cancelJob.isPending}
              >
                <XCircle className="mr-1 h-3 w-3" />
                Cancel
              </Button>
            )}
        </div>
      </div>

      {job.failure_reason && (
        <Card>
          <CardContent className="py-3">
            <p className="text-sm text-destructive">{job.failure_reason}</p>
          </CardContent>
        </Card>
      )}

      {job.ha_warnings.length > 0 && (
        <HAWarningsCard warnings={job.ha_warnings} policy={job.ha_policy} />
      )}

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-base">Node Pipeline</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2">
          {nodes.map((node) => (
            <NodeRow
              key={node.id}
              node={node}
              clusterId={clusterId}
              jobId={jobId}
              canManage={canManage}
            />
          ))}
        </CardContent>
      </Card>
    </div>
  );
}
