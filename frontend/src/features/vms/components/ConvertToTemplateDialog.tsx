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
import { TaskProgressBanner } from "./TaskProgressBanner";
import type { ResourceKind } from "../types/vm";

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
  const convertMutation = useConvertToTemplate();
  const [upid, setUpid] = useState<string | null>(null);
  const [confirmText, setConfirmText] = useState("");

  const isConfirmed = confirmText === resourceName;
  const typeLabel = kind === "ct" ? "container" : "VM";

  function handleSubmit(e: React.SyntheticEvent) {
    e.preventDefault();
    convertMutation.mutate(
      { clusterId, resourceId, kind },
      {
        onSuccess: (data) => {
          setUpid(data.upid);
        },
      },
    );
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
            onComplete={() => {
              handleClose();
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
