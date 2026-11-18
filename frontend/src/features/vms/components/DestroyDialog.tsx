import { useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { CopyableName } from "@/components/CopyableName";
import { ConfirmNameField } from "@/components/TypeToConfirmDialog";
import { useDestroyVM } from "../api/vm-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { TaskProgressBanner } from "./TaskProgressBanner";
import type { ResourceKind } from "../types/vm";
import { useTranslation } from "react-i18next";

interface DestroyDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
  resourceId: string;
  kind: ResourceKind;
  resourceName: string;
}

export function DestroyDialog({
  open,
  onOpenChange,
  clusterId,
  resourceId,
  kind,
  resourceName,
}: DestroyDialogProps) {
  const { t } = useTranslation("vms");
  const navigate = useNavigate();
  const destroyMutation = useDestroyVM(true);
  const settle = useSaveOutcome(open ? resourceId : null);
  const [upid, setUpid] = useState<string | null>(null);
  const [confirmText, setConfirmText] = useState("");

  const isConfirmed = confirmText === resourceName;

  function handleSubmit(e: React.SyntheticEvent) {
    e.preventDefault();
    settle(destroyMutation.mutateAsync({ clusterId, resourceId, kind }), {
      action: t("destroyDialogActions.action", {
        resourceName,
      }),
      onSuccess: (data) => {
        setUpid(data.upid);
      },
      onError: () => undefined, // shown below from the mutation's state
    });
  }

  function handleClose() {
    setUpid(null);
    setConfirmText("");
    destroyMutation.reset();
    onOpenChange(false);
  }

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            Destroy {kind === "ct" ? "container" : "VM"}
          </DialogTitle>
          <DialogDescription>
            This permanently destroys <CopyableName name={resourceName} /> and
            all its data: its disks, its snapshots and its configuration. It
            cannot be undone.
          </DialogDescription>
        </DialogHeader>

        {upid ? (
          <TaskProgressBanner
            clusterId={clusterId}
            upid={upid}
            onComplete={(success) => {
              // A failed destroy leaves the guest, and the banner says why:
              // stay, rather than leave for the inventory as if it had gone.
              if (!success) return;
              handleClose();
              void navigate("/inventory");
            }}
            description={`Destroy ${resourceName}`}
          />
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <ConfirmNameField
              id="destroy-confirm"
              name={resourceName}
              assist
              value={confirmText}
              onChange={setConfirmText}
            />

            {destroyMutation.isError && (
              <p className="text-sm text-destructive">
                {destroyMutation.error.message}
              </p>
            )}

            <DialogFooter className="gap-2 sm:gap-0">
              <Button type="button" variant="outline" onClick={handleClose}>
                Cancel
              </Button>
              {/* The one filled red control in the dialog. */}
              <Button
                type="submit"
                variant="destructive"
                disabled={!isConfirmed || destroyMutation.isPending}
              >
                {destroyMutation.isPending ? "Destroying..." : "Destroy"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
