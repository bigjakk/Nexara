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
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { useTranslation } from "react-i18next";

/**
 * A node's shutdown behind its confirm, shared by the page's power actions and
 * the Power card's "shut it down now" during an outage, so both ask the same
 * question: a shutdown always confirms.
 *
 * Opened by its `trigger`, or, from a menu item that is gone by the time the
 * dialog shows, by `open` and `onOpenChange`.
 */
export function NodeShutdownConfirm({
  clusterId,
  nodeName,
  trigger,
  open,
  onOpenChange,
  onConfirm,
  onError,
}: {
  clusterId: string;
  nodeName: string;
  trigger?: ReactNode;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  onConfirm?: () => void;
  onError?: (err: unknown) => void;
}) {
  const { t } = useTranslation("clusters");
  // With an onError the caller shows a failure, and one that lands after it
  // has gone is toasted; without one the app's error toast reports it.
  const shutdown = useShutdownNode(clusterId, nodeName, onError !== undefined);
  const settle = useSaveOutcome(`${clusterId}/${nodeName}`);
  return (
    <AlertDialog
      {...(open !== undefined ? { open } : {})}
      {...(onOpenChange !== undefined ? { onOpenChange } : {})}
    >
      {trigger !== undefined && (
        <AlertDialogTrigger asChild>{trigger}</AlertDialogTrigger>
      )}
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Shut down {nodeName}?</AlertDialogTitle>
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
              if (onError === undefined) {
                shutdown.mutate();
              } else {
                settle(shutdown.mutateAsync(), {
                  action: t("nodeShutdownConfirmActions.shuttingDownNodeName", { nodeName }),
                  onError,
                });
              }
            }}
            disabled={shutdown.isPending}
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
          >
            Shut down
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
