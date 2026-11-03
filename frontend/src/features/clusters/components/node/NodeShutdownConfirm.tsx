import type { ReactNode } from "react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { useShutdownNode } from "../../api/cluster-queries";

/**
 * A node's shutdown behind its confirm, shared by the page's power actions and
 * the Power card's "shut it down now" during an outage, so both ask the same
 * question: a shutdown always confirms.
 */
export function NodeShutdownConfirm({
  clusterId,
  nodeName,
  trigger,
  onConfirm,
  onError,
}: {
  clusterId: string;
  nodeName: string;
  trigger: ReactNode;
  onConfirm?: () => void;
  onError?: (err: unknown) => void;
}) {
  const shutdown = useShutdownNode(clusterId, nodeName);
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>{trigger}</AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Shutdown {nodeName}?</AlertDialogTitle>
          <AlertDialogDescription>
            This will shut down the node. It will go offline and all running
            guests will be stopped. You will need physical or out-of-band
            access to power it back on.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            onClick={() => {
              onConfirm?.();
              shutdown.mutate(undefined, onError === undefined ? undefined : { onError });
            }}
            disabled={shutdown.isPending}
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
          >
            Shutdown
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
