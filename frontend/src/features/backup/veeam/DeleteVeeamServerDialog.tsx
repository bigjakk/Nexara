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
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
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
  const settle = useSaveOutcome(open);

  function handleDelete() {
    settle(deleteMutation.mutateAsync(server.id), {
      action: `Deleting Veeam server ${server.name}`,
      onSuccess: () => {
        onOpenChange(false);
        setConfirmName("");
      },
      onError: () => undefined, // shown below from the mutation's state
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
          <DialogTitle>Delete Veeam server</DialogTitle>
          <DialogDescription>
            This removes <CopyableName name={server.name} /> from Nexara along
            with its stored credential. Backups and jobs on the Veeam server
            itself are not affected. Type the server name to confirm.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <Input
            placeholder={server.name}
            aria-label="Server name, to confirm"
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
            {deleteMutation.isPending ? "Deleting..." : "Delete server"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
