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
import { Square } from "lucide-react";
import { usePermissions } from "@/hooks/usePermissions";
import { useStopVeeamSession } from "../api/backup-queries";
import type { VeeamSession } from "../types/backup";

interface VeeamSessionActionsProps {
  serverId: string;
  session: VeeamSession;
}

/**
 * The two ESessionState values a stop cannot help with.
 *
 * Defined by exclusion on purpose. ESessionState has twelve values and only
 * "Stopped" is terminal — the rest include WaitingRepository, WaitingSlot,
 * Idle and ActionRequired, which are precisely the states an operator most
 * wants to kill a run out of. An allow-list of the "obviously running" ones
 * would have hidden the button on every stuck run.
 *
 * "Stopping" is excluded because the stop is async and takes ~30s to land, so
 * a second request would only spend another domain logon to be told the
 * session is no longer running.
 */
const UNSTOPPABLE_STATES = new Set(["Stopped", "Stopping"]);

/** Stop one running Veeam session. */
export function VeeamSessionActions({
  serverId,
  session,
}: VeeamSessionActionsProps) {
  const { canExecute } = usePermissions();
  const [confirming, setConfirming] = useState(false);
  const stopSession = useStopVeeamSession(serverId);

  if (!canExecute("veeam")) return null;
  // A finished run has nothing to stop, and rendering a disabled button on
  // every historical row would be noise on a table that is mostly history. An
  // empty state is treated as finished: it is what a row with no state at all
  // reads as, and offering a stop on one would be a guess.
  if (session.state === "" || UNSTOPPABLE_STATES.has(session.state))
    return null;

  return (
    <div
      className="flex items-center justify-end gap-2"
      onClick={(e) => {
        e.stopPropagation();
      }}
    >
      {stopSession.error != null && (
        <span className="max-w-xs text-xs text-destructive">
          {stopSession.error instanceof Error
            ? stopSession.error.message
            : "Could not stop the run."}
        </span>
      )}
      <Button
        variant="ghost"
        size="sm"
        disabled={stopSession.isPending}
        onClick={() => {
          setConfirming(true);
        }}
        title="Stop this run"
      >
        <Square className="h-4 w-4" />
        <span className="sr-only">Stop run</span>
      </Button>

      <AlertDialog open={confirming} onOpenChange={setConfirming}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Stop &quot;{session.name}&quot;?
            </AlertDialogTitle>
            <AlertDialogDescription>
              The run is abandoned. Guests it has not reached yet keep whatever
              recovery point they already had, and Veeam records the run as
              failed — it does not distinguish a stop from a genuine failure.
              Nexara records that you asked for this one, so it will not raise a
              failed-job alert for it.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                setConfirming(false);
                stopSession.mutate(session.veeam_id);
              }}
            >
              Stop run
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
