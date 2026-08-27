import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Play, Square, PauseCircle, PlayCircle } from "lucide-react";
import { usePermissions } from "@/hooks/usePermissions";
import {
  useStartVeeamJob,
  useStopVeeamJob,
  useSetVeeamJobEnabled,
} from "../api/backup-queries";
import type { VeeamJob } from "../types/backup";

interface VeeamJobActionsProps {
  serverId: string;
  job: VeeamJob;
}

/**
 * EJobStatus is Running | Inactive | Disabled | Enabled | Stopping | Stopped |
 * Starting. Three of those are in-flight, and lumping them together as "not
 * Running" would offer Run on a job that is already starting.
 */
const DISABLED_STATUS = "Disabled";
const STOPPABLE_STATUSES = new Set(["Running", "Starting"]);
const STOPPING_STATUS = "Stopping";

/**
 * Run / Stop / Enable / Disable for one Veeam job.
 *
 * Two of the four are disruptive and both confirm, per the project's
 * confirm-disruptive-actions rule:
 *
 *  - Stop abandons a run in progress. The guests it had not reached keep
 *    whatever recovery point they already had, and Veeam will record the run
 *    as "Failed" either way.
 *  - Disable is the quieter one and the reason this rule exists: nothing is
 *    deleted, no alarm is raised anywhere, and protection simply stops
 *    accruing while every existing restore point sits there looking healthy.
 *
 * Run and Enable are additive and go straight through.
 */
export function VeeamJobActions({ serverId, job }: VeeamJobActionsProps) {
  const { canExecute } = usePermissions();
  const [confirming, setConfirming] = useState<"stop" | "disable" | null>(null);
  // The 204 outcome: Veeam accepted the start but the job had no objects to
  // process, so no run exists. Surfaced explicitly — an operator who clicked
  // Run would otherwise watch for a session that is never going to appear and
  // conclude the click was dropped.
  const [notice, setNotice] = useState("");

  const startJob = useStartVeeamJob(serverId);
  const stopJob = useStopVeeamJob(serverId);
  const setEnabled = useSetVeeamJobEnabled(serverId);

  // The permission is execute:veeam, resolved per cluster on the server. This
  // hides the buttons for a caller who holds it nowhere; a caller who holds it
  // on a different cluster still sees them and gets a 403, which is the same
  // shape every other cluster-scoped action in Nexara has.
  if (!canExecute("veeam")) return null;

  // A live run beats the stored status, which is only as fresh as the last
  // inventory pass. A job started from Nexara has a session immediately and a
  // status that still reads "Stopped" for minutes, so keying on status alone
  // hid Stop for exactly as long as the operator was most likely to want it.
  const running = job.running_session_id !== "";
  const stoppable = running || STOPPABLE_STATUSES.has(job.status);
  const stopping =
    job.status === STOPPING_STATUS ||
    job.running_session_state === STOPPING_STATUS;
  const disabled = job.status === DISABLED_STATUS;
  const busy = startJob.isPending || stopJob.isPending || setEnabled.isPending;

  const error = startJob.error ?? stopJob.error ?? setEnabled.error;

  function runNow() {
    setNotice("");
    startJob.mutate(job.veeam_id, {
      onSuccess: (result) => {
        // The server sends a message for both of the outcomes that are not a
        // plain "a run started": the job had nothing to process, and Veeam
        // accepted the request but reported no run to track. Keyed on the
        // message rather than on `started`, or the second one — where started
        // IS true — would be dropped silently and the operator would watch a
        // Runs tab that shows nothing.
        if (result.message != null && result.message !== "") {
          setNotice(result.message);
        } else if (!result.started) {
          setNotice(
            "Veeam accepted the request but the job had nothing to back up, so no run was started.",
          );
        }
      },
    });
  }

  function confirmStop() {
    setNotice("");
    setConfirming(null);
    stopJob.mutate(job.veeam_id);
  }

  function confirmDisable() {
    setNotice("");
    setConfirming(null);
    setEnabled.mutate({ jobVeeamId: job.veeam_id, enabled: false });
  }

  return (
    // Stops a click on any control from also toggling the row it sits in.
    <div
      className="flex items-center justify-end gap-1"
      onClick={(e) => {
        e.stopPropagation();
      }}
    >
      {stoppable || stopping ? (
        <Button
          variant="ghost"
          size="sm"
          // A job already stopping has nothing left to ask for: the stop is
          // async and takes ~30s to land, so a second request would only spend
          // another domain logon to be told the job is no longer running.
          disabled={busy || stopping}
          onClick={() => {
            setConfirming("stop");
          }}
          title={
            stopping ? "This job is already stopping" : "Stop the running job"
          }
        >
          <Square className="h-4 w-4" />
          <span className="sr-only">Stop job</span>
        </Button>
      ) : (
        <Button
          variant="ghost"
          size="sm"
          disabled={busy || disabled}
          onClick={runNow}
          title={
            disabled
              ? "This job is disabled. Enable it before running it."
              : "Run this job now"
          }
        >
          <Play className="h-4 w-4" />
          <span className="sr-only">Run job now</span>
        </Button>
      )}

      {disabled ? (
        <Button
          variant="ghost"
          size="sm"
          disabled={busy}
          onClick={() => {
            setNotice("");
            setEnabled.mutate({ jobVeeamId: job.veeam_id, enabled: true });
          }}
          title="Put this job back on its schedule"
        >
          <PlayCircle className="h-4 w-4" />
          <span className="sr-only">Enable job</span>
        </Button>
      ) : (
        <Button
          variant="ghost"
          size="sm"
          disabled={busy}
          onClick={() => {
            setConfirming("disable");
          }}
          title="Take this job off its schedule"
        >
          <PauseCircle className="h-4 w-4" />
          <span className="sr-only">Disable job</span>
        </Button>
      )}

      {notice !== "" && (
        <span className="max-w-xs text-xs text-muted-foreground">{notice}</span>
      )}
      {error != null && (
        <span className="max-w-xs text-xs text-destructive">
          {error instanceof Error ? error.message : "The action failed."}
        </span>
      )}

      <AlertDialog
        open={confirming !== null}
        onOpenChange={(open) => {
          if (!open) setConfirming(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {confirming === "stop"
                ? `Stop "${job.name}"?`
                : `Disable "${job.name}"?`}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {confirming === "stop"
                ? "The run is abandoned. Guests it has not reached yet keep whatever recovery point they already had, and Veeam records the run as failed — it does not distinguish a stop from a genuine failure. Nexara records that you asked for this one, so it will not raise a failed-job alert for it."
                : "The job stops running on its schedule. Nothing is deleted and existing restore points stay where they are, so protection quietly stops accruing with no warning from Veeam. Recovery points will age past their RPO from here."}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={confirming === "stop" ? confirmStop : confirmDisable}
            >
              {confirming === "stop" ? "Stop job" : "Disable job"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
