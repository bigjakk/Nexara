import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { useDeleteCephPool } from "../api/ceph-queries";

interface PoolDeleteDialogProps {
  clusterId: string;
  poolName: string;
  open: boolean;
  onClose: () => void;
}

export function PoolDeleteDialog({
  clusterId,
  poolName,
  open,
  onClose,
}: PoolDeleteDialogProps) {
  const deletePool = useDeleteCephPool();

  function handleDelete() {
    deletePool.mutate(
      { clusterId, poolName },
      { onSettled: onClose },
    );
  }

  return (
    <Dialog open={open} onOpenChange={(v: boolean) => { if (!v) onClose(); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete Pool &quot;{poolName}&quot;?</DialogTitle>
          <DialogDescription>
            This will permanently delete the Ceph pool and all data stored in
            it. This action cannot be undone.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            onClick={handleDelete}
            disabled={deletePool.isPending}
          >
            {deletePool.isPending ? "Deleting..." : "Delete Pool"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
