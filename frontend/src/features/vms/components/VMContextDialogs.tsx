import { useVMContextMenuStore } from "@/stores/vm-context-menu-store";
import { useTaskLogStore } from "@/stores/task-log-store";
import { useVMAction } from "../api/vm-queries";
import { CloneDialog } from "./CloneDialog";
import { MigrateJobDialog } from "./MigrateJobDialog";
import { DestroyDialog } from "./DestroyDialog";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import type { VMAction } from "../types/vm";

export function VMContextDialogs() {
  const { target, openDialog, confirmAction, confirmActionLabel, closeDialog } =
    useVMContextMenuStore();
  const setPanelOpen = useTaskLogStore((s) => s.setPanelOpen);
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);
  const actionMutation = useVMAction();

  if (!target) return null;

  return (
    <>
      <CloneDialog
        open={openDialog === "clone"}
        onOpenChange={(open) => { if (!open) closeDialog(); }}
        clusterId={target.clusterId}
        resourceId={target.resourceId}
        kind={target.kind}
        sourceName={target.name}
      />

      <MigrateJobDialog
        open={openDialog === "migrate"}
        onOpenChange={(open) => { if (!open) closeDialog(); }}
        clusterId={target.clusterId}
        vmid={target.vmid}
        vmName={target.name}
        kind={target.kind}
        currentNode={target.currentNode}
        status={target.status}
      />

      <DestroyDialog
        open={openDialog === "destroy"}
        onOpenChange={(open) => { if (!open) closeDialog(); }}
        clusterId={target.clusterId}
        resourceId={target.resourceId}
        kind={target.kind}
        resourceName={target.name}
      />

      <Dialog
        open={openDialog === "confirm-action"}
        onOpenChange={(open) => { if (!open) closeDialog(); }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Confirm {confirmActionLabel}</DialogTitle>
            <DialogDescription>
              Are you sure you want to {confirmActionLabel?.toLowerCase()}{" "}
              <strong>{target.name}</strong>? This action may cause data loss.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={closeDialog}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={actionMutation.isPending}
              onClick={() => {
                if (confirmAction) {
                  actionMutation.mutate(
                    {
                      clusterId: target.clusterId,
                      resourceId: target.resourceId,
                      kind: target.kind,
                      action: confirmAction as VMAction,
                    },
                    {
                      onSuccess: (data) => {
                        setFocusedTask({
                          clusterId: target.clusterId,
                          upid: data.upid,
                          description: `${confirmActionLabel} ${target.name}`,
                        });
                        setPanelOpen(true);
                        closeDialog();
                      },
                    },
                  );
                }
              }}
            >
              {confirmActionLabel}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
