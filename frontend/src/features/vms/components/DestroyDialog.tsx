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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useDestroyVM } from "../api/vm-queries";
import { TaskProgressBanner } from "./TaskProgressBanner";
import type { ResourceKind } from "../types/vm";

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
  const navigate = useNavigate();
  const destroyMutation = useDestroyVM();
  const [upid, setUpid] = useState<string | null>(null);
  const [confirmText, setConfirmText] = useState("");

  const isConfirmed = confirmText === resourceName;

  function handleSubmit(e: React.SyntheticEvent) {
    e.preventDefault();
    destroyMutation.mutate(
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
    destroyMutation.reset();
    onOpenChange(false);
  }

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            Destroy {kind === "ct" ? "Container" : "VM"}
          </DialogTitle>
          <DialogDescription>
            This will permanently destroy{" "}
            <strong>{resourceName}</strong> and all its data. This action cannot
            be undone.
          </DialogDescription>
        </DialogHeader>

        {upid ? (
          <TaskProgressBanner
            clusterId={clusterId}
            upid={upid}
            kind={kind}
            resourceId={resourceId}
            onComplete={() => {
              handleClose();
              void navigate("/inventory");
            }}
          />
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="destroy-confirm">
                Type <strong>{resourceName}</strong> to confirm
              </Label>
              <Input
                id="destroy-confirm"
                value={confirmText}
                onChange={(e) => { setConfirmText(e.target.value); }}
                placeholder={resourceName}
                autoComplete="off"
              />
            </div>

            {destroyMutation.isError && (
              <p className="text-sm text-destructive">
                {destroyMutation.error.message}
              </p>
            )}

            <DialogFooter>
              <Button type="button" variant="outline" onClick={handleClose}>
                Cancel
              </Button>
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
