import { useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RotateCcw } from "lucide-react";
import { useRestoreBackup } from "../api/backup-queries";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import type { PBSSnapshot } from "../types/backup";

interface RestoreDialogProps {
  snapshot: PBSSnapshot;
  pbsId: string;
}

export function RestoreDialog({ snapshot, pbsId }: RestoreDialogProps) {
  const [open, setOpen] = useState(false);
  const [targetClusterId, setTargetClusterId] = useState("");
  const [targetNode, setTargetNode] = useState("");
  const [vmid, setVmid] = useState(snapshot.backup_id);
  const [storage, setStorage] = useState("");

  const clustersQuery = useClusters();
  const clusters = clustersQuery.data ?? [];
  const restoreMutation = useRestoreBackup();

  const handleRestore = () => {
    if (!targetClusterId || !targetNode || !vmid) return;

    const parsedVmid = parseInt(vmid, 10);
    if (isNaN(parsedVmid) || parsedVmid <= 0) return;

    restoreMutation.mutate(
      {
        clusterId: targetClusterId,
        body: {
          pbs_server_id: pbsId,
          backup_type: snapshot.backup_type,
          backup_id: snapshot.backup_id,
          backup_time: snapshot.backup_time,
          datastore: snapshot.datastore,
          target_node: targetNode,
          vmid: parsedVmid,
          storage: storage,
        },
      },
      {
        onSuccess: () => {
          setOpen(false);
        },
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="sm">
          <RotateCcw className="h-4 w-4" />
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Restore Backup</DialogTitle>
          <DialogDescription>
            Restore {snapshot.backup_type.toUpperCase()} {snapshot.backup_id}{" "}
            from {snapshot.datastore}.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-4">
          <div className="space-y-2">
            <Label>Target Cluster</Label>
            <select
              className="w-full rounded-md border bg-background px-3 py-2 text-sm"
              value={targetClusterId}
              onChange={(e) => {
                setTargetClusterId(e.target.value);
              }}
            >
              <option value="">Select cluster...</option>
              {clusters.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-2">
            <Label>Target Node</Label>
            <Input
              value={targetNode}
              onChange={(e) => {
                setTargetNode(e.target.value);
              }}
              placeholder="e.g., pve1"
            />
          </div>
          <div className="space-y-2">
            <Label>VMID</Label>
            <Input
              value={vmid}
              onChange={(e) => {
                setVmid(e.target.value);
              }}
              placeholder="e.g., 100"
            />
          </div>
          <div className="space-y-2">
            <Label>Storage (optional)</Label>
            <Input
              value={storage}
              onChange={(e) => {
                setStorage(e.target.value);
              }}
              placeholder="e.g., local-lvm"
            />
          </div>
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => {
              setOpen(false);
            }}
          >
            Cancel
          </Button>
          <Button
            onClick={handleRestore}
            disabled={
              restoreMutation.isPending || !targetClusterId || !targetNode || !vmid
            }
          >
            {restoreMutation.isPending ? "Restoring..." : "Restore"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
