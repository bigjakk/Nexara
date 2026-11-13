import { useVMContextMenuStore } from "@/stores/vm-context-menu-store";
import { useTaskLogStore } from "@/stores/task-log-store";
import { useVMAction } from "../api/vm-queries";
import { CreateSnapshotDialog } from "./CreateSnapshotDialog";
import { CloneDialog } from "./CloneDialog";
import { CloneToTemplateDialog } from "./CloneToTemplateDialog";
import { DeployTemplateDialog } from "./DeployTemplateDialog";
import { MigrateJobDialog } from "./MigrateJobDialog";
import { DestroyDialog } from "./DestroyDialog";
import { ConvertToTemplateDialog } from "./ConvertToTemplateDialog";
import { MoveToFolderDialog } from "./MoveToFolderDialog";
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

/**
 * What each confirmed power action costs, in a sentence. An action that is not
 * listed gets the general warning.
 */
const POWER_CONSEQUENCE: Partial<Record<string, string>> = {
  shutdown:
    "The guest is asked to shut down, and stays off until it is started again.",
  stop: "The guest is powered off at once, without shutting down. Anything it has not saved is lost.",
  reset:
    "The guest is reset at once, without shutting down. Anything it has not saved is lost.",
};

export function VMContextDialogs() {
  const { target, openDialog, confirmAction, confirmActionLabel, closeDialog } =
    useVMContextMenuStore();
  const setPanelOpen = useTaskLogStore((s) => s.setPanelOpen);
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);
  const actionMutation = useVMAction();

  if (!target) return null;

  return (
    <>
      <CreateSnapshotDialog
        open={openDialog === "snapshot"}
        onOpenChange={(open) => {
          if (!open) closeDialog();
        }}
        clusterId={target.clusterId}
        resourceId={target.resourceId}
        kind={target.kind}
        resourceName={target.name}
      />

      <CloneDialog
        open={openDialog === "clone"}
        onOpenChange={(open) => {
          if (!open) closeDialog();
        }}
        clusterId={target.clusterId}
        resourceId={target.resourceId}
        kind={target.kind}
        sourceName={target.name}
      />

      <MigrateJobDialog
        open={openDialog === "migrate"}
        onOpenChange={(open) => {
          if (!open) closeDialog();
        }}
        clusterId={target.clusterId}
        resourceId={target.resourceId}
        vmid={target.vmid}
        vmName={target.name}
        kind={target.kind}
        currentNode={target.currentNode}
        status={target.status}
      />

      <DestroyDialog
        open={openDialog === "destroy"}
        onOpenChange={(open) => {
          if (!open) closeDialog();
        }}
        clusterId={target.clusterId}
        resourceId={target.resourceId}
        kind={target.kind}
        resourceName={target.name}
      />

      <CloneToTemplateDialog
        open={openDialog === "clone-to-template"}
        onOpenChange={(open) => {
          if (!open) closeDialog();
        }}
        clusterId={target.clusterId}
        resourceId={target.resourceId}
        kind={target.kind}
        sourceName={target.name}
      />

      <DeployTemplateDialog
        open={openDialog === "deploy"}
        onOpenChange={(open) => {
          if (!open) closeDialog();
        }}
        clusterId={target.clusterId}
        vmId={target.resourceId}
        kind={target.kind}
        templateName={target.name}
      />

      <ConvertToTemplateDialog
        open={openDialog === "convert-to-template"}
        onOpenChange={(open) => {
          if (!open) closeDialog();
        }}
        clusterId={target.clusterId}
        resourceId={target.resourceId}
        kind={target.kind}
        resourceName={target.name}
      />

      <MoveToFolderDialog
        open={openDialog === "move-to-folder"}
        onOpenChange={(open) => {
          if (!open) closeDialog();
        }}
        clusterId={target.clusterId}
        vmId={target.resourceId}
        vmName={target.name}
      />

      <Dialog
        open={openDialog === "confirm-action"}
        onOpenChange={(open) => {
          if (!open) closeDialog();
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {confirmActionLabel} {target.name}?
            </DialogTitle>
            <DialogDescription>
              {(confirmAction !== null
                ? POWER_CONSEQUENCE[confirmAction]
                : undefined) ?? "This may cause data loss in the guest."}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="gap-2 sm:gap-0">
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
                          description: `${confirmActionLabel ?? ""} ${target.name}`,
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
