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
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { useMigrateContainer } from "../api/vm-queries";
import { TaskProgressBanner } from "./TaskProgressBanner";

interface MigrateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
  containerId: string;
  containerName: string;
}

export function MigrateDialog({
  open,
  onOpenChange,
  clusterId,
  containerId,
  containerName,
}: MigrateDialogProps) {
  const { data: nodes } = useClusterNodes(clusterId);
  const migrateMutation = useMigrateContainer();
  const [upid, setUpid] = useState<string | null>(null);

  const [target, setTarget] = useState("");
  const [online, setOnline] = useState(false);

  function handleSubmit(e: React.SyntheticEvent) {
    e.preventDefault();
    migrateMutation.mutate(
      {
        clusterId,
        containerId,
        body: { target, online },
      },
      {
        onSuccess: (data) => {
          setUpid(data.upid);
        },
      },
    );
  }

  function handleClose() {
    setUpid(null);
    setTarget("");
    setOnline(false);
    migrateMutation.reset();
    onOpenChange(false);
  }

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Migrate Container</DialogTitle>
          <DialogDescription>
            Migrate <strong>{containerName}</strong> to another node.
          </DialogDescription>
        </DialogHeader>

        {upid ? (
          <TaskProgressBanner
            clusterId={clusterId}
            upid={upid}
            kind="ct"
            resourceId={containerId}
            onComplete={() => { handleClose(); }}
            description={`Migrate ${containerName}`}
          />
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="migrate-target">Target Node</Label>
              <select
                id="migrate-target"
                value={target}
                onChange={(e) => { setTarget(e.target.value); }}
                className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                required
              >
                <option value="">Select a node</option>
                {nodes?.map((n) => (
                  <option key={n.id} value={n.name}>
                    {n.name}
                  </option>
                ))}
              </select>
            </div>

            <div className="flex items-center gap-2">
              <Checkbox
                id="migrate-online"
                checked={online}
                onCheckedChange={(checked) => { setOnline(Boolean(checked)); }}
              />
              <Label htmlFor="migrate-online" className="text-sm">
                Online migration (no downtime)
              </Label>
            </div>

            {migrateMutation.isError && (
              <p className="text-sm text-destructive">
                {migrateMutation.error.message}
              </p>
            )}

            <DialogFooter>
              <Button type="button" variant="outline" onClick={handleClose}>
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={target.length === 0 || migrateMutation.isPending}
              >
                {migrateMutation.isPending ? "Migrating..." : "Migrate"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
