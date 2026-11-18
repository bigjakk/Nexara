import { useState } from "react";
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
import { useConvertToTemplate } from "../api/vm-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { TaskProgressBanner } from "./TaskProgressBanner";
import type { ResourceKind } from "../types/vm";
import { useTranslation } from "react-i18next";

interface ConvertToTemplateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
  resourceId: string;
  kind: ResourceKind;
  resourceName: string;
}

export function ConvertToTemplateDialog({
  open,
  onOpenChange,
  clusterId,
  resourceId,
  kind,
  resourceName,
}: ConvertToTemplateDialogProps) {
  const { t } = useTranslation("vms");
  const convertMutation = useConvertToTemplate();
  const settle = useSaveOutcome(open ? resourceId : null);
  const [upid, setUpid] = useState<string | null>(null);
  const [confirmText, setConfirmText] = useState("");

  const isConfirmed = confirmText === resourceName;
  const typeLabel = kind === "ct" ? "container" : "VM";

  function handleSubmit(e: React.SyntheticEvent) {
    e.preventDefault();
    settle(convertMutation.mutateAsync({ clusterId, resourceId, kind }), {
      action: t("convertToTemplateDialogActions.action", { resourceName }),
      onSuccess: (data) => {
        setUpid(data.upid);
      },
      onError: () => undefined, // shown below from the mutation's state
    });
  }

  function handleClose() {
    setUpid(null);
    setConfirmText("");
    convertMutation.reset();
    onOpenChange(false);
  }

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Convert to template</DialogTitle>
          <DialogDescription>
            This turns <CopyableName name={resourceName} /> into a read-only
            template. It can no longer be started or changed, only cloned into
            new {typeLabel}s, and it cannot be turned back: to get a runnable{" "}
            {typeLabel} again you clone the template.
          </DialogDescription>
        </DialogHeader>

        {upid ? (
          <TaskProgressBanner
            clusterId={clusterId}
            upid={upid}
            onComplete={(success) => {
              // A failed task stays on the banner, which gives Proxmox's reason.
              if (success) handleClose();
            }}
            description={`Convert ${resourceName} to template`}
          />
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <ConfirmNameField
              id="confirm-name"
              name={resourceName}
              assist
              value={confirmText}
              onChange={setConfirmText}
            />

            {convertMutation.isError && (
              <p className="text-sm text-destructive">
                {convertMutation.error.message}
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
                disabled={!isConfirmed || convertMutation.isPending}
              >
                {convertMutation.isPending
                  ? "Converting..."
                  : "Convert to template"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
