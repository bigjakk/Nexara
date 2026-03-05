import { useEffect, useMemo, useState } from "react";
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
import { Checkbox } from "@/components/ui/checkbox";
import { useClusterNodes, useClusterStorage } from "@/features/clusters/api/cluster-queries";
import { useCloneVM, useClusterVMIDs } from "../api/vm-queries";
import { TaskProgressBanner } from "./TaskProgressBanner";
import type { ResourceKind } from "../types/vm";

interface CloneDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
  resourceId: string;
  kind: ResourceKind;
  sourceName: string;
}

export function CloneDialog({
  open,
  onOpenChange,
  clusterId,
  resourceId,
  kind,
  sourceName,
}: CloneDialogProps) {
  const { data: nodes } = useClusterNodes(clusterId);
  const { data: storageList } = useClusterStorage(clusterId);
  const { data: usedVMIDs } = useClusterVMIDs(clusterId);
  const cloneMutation = useCloneVM();

  // Deduplicate storage names (same pool may appear on multiple nodes)
  const storageOptions = storageList
    ? [...new Set(storageList
        .filter((s) => s.active && s.enabled && s.content.includes("images"))
        .map((s) => s.storage))]
        .sort()
    : [];

  // Find the first available VMID starting from 100
  const nextAvailableId = useMemo(() => {
    if (!usedVMIDs || usedVMIDs.size === 0) return 100;
    let candidate = 100;
    while (usedVMIDs.has(candidate)) candidate++;
    return candidate;
  }, [usedVMIDs]);

  const [upid, setUpid] = useState<string | null>(null);
  const [newId, setNewId] = useState("");
  const [name, setName] = useState("");
  const [target, setTarget] = useState("");
  const [full, setFull] = useState(true);
  const [storage, setStorage] = useState("");

  // Auto-populate VMID when dialog opens and data is available
  useEffect(() => {
    if (open && newId === "" && usedVMIDs) {
      setNewId(String(nextAvailableId));
    }
  }, [open, usedVMIDs, nextAvailableId, newId]);

  const numericId = Number(newId);
  const isDuplicate = usedVMIDs ? usedVMIDs.has(numericId) : false;
  const isValid = newId.length > 0 && numericId > 0;

  function handleSubmit(e: React.SyntheticEvent) {
    e.preventDefault();
    cloneMutation.mutate(
      {
        clusterId,
        resourceId,
        kind,
        body: {
          new_id: Number(newId),
          name,
          target,
          full,
          storage: full ? storage : "",
        },
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
    setNewId("");
    setName("");
    setTarget("");
    setFull(true);
    setStorage("");
    cloneMutation.reset();
    onOpenChange(false);
  }

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Clone {kind === "ct" ? "Container" : "VM"}</DialogTitle>
          <DialogDescription>
            Clone <strong>{sourceName}</strong> to a new{" "}
            {kind === "ct" ? "container" : "virtual machine"}.
          </DialogDescription>
        </DialogHeader>

        {upid ? (
          <div className="space-y-4">
            <TaskProgressBanner
              clusterId={clusterId}
              upid={upid}
              kind={kind}
              resourceId={resourceId}
              onComplete={() => { handleClose(); }}
              description={`Clone ${sourceName}`}
            />
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor="clone-new-id">New VMID</Label>
                <Input
                  id="clone-new-id"
                  type="number"
                  min={1}
                  value={newId}
                  onChange={(e) => { setNewId(e.target.value); }}
                  placeholder="e.g. 200"
                  required
                />
                {isDuplicate && (
                  <p className="text-xs text-yellow-600 dark:text-yellow-500">
                    VMID {numericId} may already be in use
                  </p>
                )}
              </div>
              <div className="space-y-2">
                <Label htmlFor="clone-name">Name</Label>
                <Input
                  id="clone-name"
                  value={name}
                  onChange={(e) => { setName(e.target.value); }}
                  placeholder="Optional"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="clone-target">Target Node</Label>
                <select
                  id="clone-target"
                  value={target}
                  onChange={(e) => { setTarget(e.target.value); }}
                  className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                >
                  <option value="">Same node</option>
                  {nodes?.map((n) => (
                    <option key={n.id} value={n.name}>
                      {n.name}
                    </option>
                  ))}
                </select>
              </div>
              {full && (
                <div className="space-y-2">
                  <Label htmlFor="clone-storage">Storage</Label>
                  <select
                    id="clone-storage"
                    value={storage}
                    onChange={(e) => { setStorage(e.target.value); }}
                    className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  >
                    <option value="">Same as source</option>
                    {storageOptions.map((s) => (
                      <option key={s} value={s}>
                        {s}
                      </option>
                    ))}
                  </select>
                </div>
              )}
            </div>

            <div className="flex items-center gap-2">
              <Checkbox
                id="clone-full"
                checked={full}
                onCheckedChange={(checked) => { setFull(Boolean(checked)); }}
              />
              <Label htmlFor="clone-full" className="text-sm">
                Full clone (independent copy)
              </Label>
            </div>

            {cloneMutation.isError && (
              <p className="text-sm text-destructive">
                {cloneMutation.error.message}
              </p>
            )}

            <DialogFooter>
              <Button type="button" variant="outline" onClick={handleClose}>
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={!isValid || cloneMutation.isPending}
              >
                {cloneMutation.isPending ? "Cloning..." : "Clone"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
