import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { useDeleteSnapshot } from "../api/backup-queries";
import type { PBSSnapshot } from "../types/backup";

interface DeleteSnapshotDialogProps {
  snapshot: PBSSnapshot;
  pbsId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function DeleteSnapshotDialog({
  snapshot,
  pbsId,
  open,
  onOpenChange,
}: DeleteSnapshotDialogProps) {
  const deleteMutation = useDeleteSnapshot();

  const handleDelete = () => {
    deleteMutation.mutate(
      {
        pbsId,
        store: snapshot.datastore,
        body: {
          backup_type: snapshot.backup_type,
          backup_id: snapshot.backup_id,
          backup_time: snapshot.backup_time,
        },
      },
      {
        onSuccess: () => {
          onOpenChange(false);
        },
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete Snapshot</DialogTitle>
          <DialogDescription>
            Are you sure you want to delete{" "}
            <strong>
              {snapshot.backup_type}/{snapshot.backup_id}
            </strong>{" "}
            from {snapshot.datastore}? This action cannot be undone.
          </DialogDescription>
        </DialogHeader>
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
            onClick={handleDelete}
            disabled={deleteMutation.isPending}
          >
            {deleteMutation.isPending ? "Deleting..." : "Delete"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
