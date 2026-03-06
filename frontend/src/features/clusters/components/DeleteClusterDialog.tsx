import { useState } from "react";
import { useDeleteCluster } from "@/features/dashboard/api/dashboard-queries";
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
import type { ClusterResponse } from "@/types/api";

interface DeleteClusterDialogProps {
  cluster: ClusterResponse;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function DeleteClusterDialog({ cluster, open, onOpenChange }: DeleteClusterDialogProps) {
  const [confirmName, setConfirmName] = useState("");
  const deleteMutation = useDeleteCluster();

  function handleDelete() {
    deleteMutation.mutate(cluster.id, {
      onSuccess: () => {
        onOpenChange(false);
      },
    });
  }

  return (
    <Dialog open={open} onOpenChange={(v) => { onOpenChange(v); if (!v) setConfirmName(""); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete Cluster</DialogTitle>
          <DialogDescription>
            This will permanently remove <strong>{cluster.name}</strong> and all associated data (nodes, VMs, metrics).
            Type the cluster name to confirm.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <Input
            placeholder={cluster.name}
            value={confirmName}
            onChange={(e) => { setConfirmName(e.target.value); }}
          />
          {deleteMutation.isError && (
            <p className="text-sm text-destructive">
              {deleteMutation.error instanceof Error ? deleteMutation.error.message : "Delete failed"}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => { onOpenChange(false); }}>Cancel</Button>
          <Button
            variant="destructive"
            disabled={confirmName !== cluster.name || deleteMutation.isPending}
            onClick={handleDelete}
          >
            {deleteMutation.isPending ? "Deleting..." : "Delete Cluster"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
