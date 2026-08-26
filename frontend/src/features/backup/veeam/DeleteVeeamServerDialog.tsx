import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { CopyableName } from "@/components/CopyableName";
import { useDeleteVeeamServer } from "../api/backup-queries";
import type { VeeamServer } from "../types/backup";

interface DeleteVeeamServerDialogProps {
  server: VeeamServer;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

/**
 * Type-the-name confirmation, matching the PBS equivalent.
 *
 * Removing the server discards its stored credential, so re-adding means
 * re-entering the administrator password. Nothing on the Veeam side is
 * touched — the backups and jobs are unaffected.
 */
export function DeleteVeeamServerDialog({
  server,
  open,
  onOpenChange,
}: DeleteVeeamServerDialogProps) {
  const [confirmName, setConfirmName] = useState("");
  const deleteMutation = useDeleteVeeamServer();

  function handleDelete() {
    deleteMutation.mutate(server.id, {
      onSuccess: () => {
        onOpenChange(false);
        setConfirmName("");
      },
    });
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        onOpenChange(v);
        if (!v) setConfirmName("");
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete Veeam Server</DialogTitle>
          <DialogDescription>
            This removes <CopyableName name={server.name} /> from Nexara along
            with its stored credential. Backups and jobs on the Veeam server
            itself are not affected. Type the server name to confirm.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <Input
            placeholder={server.name}
            value={confirmName}
            onChange={(e) => {
              setConfirmName(e.target.value);
            }}
          />
          {deleteMutation.error != null && (
            <p className="text-sm text-destructive">
              {deleteMutation.error instanceof Error
                ? deleteMutation.error.message
                : "Failed to delete the Veeam server"}
            </p>
          )}
        </div>

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => {
              onOpenChange(false);
            }}
          >
            Cancel
          </Button>
          <Button
            variant="destructive"
            disabled={confirmName !== server.name || deleteMutation.isPending}
            onClick={handleDelete}
          >
            {deleteMutation.isPending ? "Deleting..." : "Delete Server"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
