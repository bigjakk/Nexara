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
import { PlayCircle } from "lucide-react";
import { useTriggerBackup } from "../api/backup-queries";

interface TriggerBackupDialogProps {
  clusterId: string;
}

export function TriggerBackupDialog({ clusterId }: TriggerBackupDialogProps) {
  const [open, setOpen] = useState(false);
  const [node, setNode] = useState("");
  const [vmid, setVmid] = useState("");
  const [storage, setStorage] = useState("");
  const [mode, setMode] = useState("snapshot");
  const [compress, setCompress] = useState("zstd");

  const backupMutation = useTriggerBackup();

  const handleBackup = () => {
    if (!node || !vmid) return;

    backupMutation.mutate(
      {
        clusterId,
        body: { vmid, node, storage, mode, compress },
      },
      {
        onSuccess: () => {
          setOpen(false);
          setNode("");
          setVmid("");
          setStorage("");
        },
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">
          <PlayCircle className="mr-1.5 h-3.5 w-3.5" />
          Backup Now
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Trigger Backup</DialogTitle>
          <DialogDescription>
            Start a vzdump backup on the selected cluster.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-4">
          <div className="space-y-2">
            <Label>Node</Label>
            <Input
              value={node}
              onChange={(e) => {
                setNode(e.target.value);
              }}
              placeholder="e.g., pve1"
            />
          </div>
          <div className="space-y-2">
            <Label>VMID(s)</Label>
            <Input
              value={vmid}
              onChange={(e) => {
                setVmid(e.target.value);
              }}
              placeholder="e.g., 100 or 100,101,102"
            />
          </div>
          <div className="space-y-2">
            <Label>Storage (optional)</Label>
            <Input
              value={storage}
              onChange={(e) => {
                setStorage(e.target.value);
              }}
              placeholder="e.g., local or pbs-store"
            />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>Mode</Label>
              <select
                className="w-full rounded-md border bg-background px-3 py-2 text-sm"
                value={mode}
                onChange={(e) => {
                  setMode(e.target.value);
                }}
              >
                <option value="snapshot">Snapshot</option>
                <option value="suspend">Suspend</option>
                <option value="stop">Stop</option>
              </select>
            </div>
            <div className="space-y-2">
              <Label>Compression</Label>
              <select
                className="w-full rounded-md border bg-background px-3 py-2 text-sm"
                value={compress}
                onChange={(e) => {
                  setCompress(e.target.value);
                }}
              >
                <option value="zstd">zstd</option>
                <option value="lzo">lzo</option>
                <option value="gzip">gzip</option>
                <option value="0">None</option>
              </select>
            </div>
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
            onClick={handleBackup}
            disabled={backupMutation.isPending || !node || !vmid}
          >
            {backupMutation.isPending ? "Starting..." : "Start Backup"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
