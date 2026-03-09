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
import { AlertTriangle } from "lucide-react";
import { useClusterNodes, useClusterStorage } from "@/features/clusters/api/cluster-queries";
import { useCloneToTemplate, useClusterVMIDs } from "../api/vm-queries";
import { TaskProgressBanner } from "./TaskProgressBanner";
import type { ResourceKind } from "../types/vm";

interface CloneToTemplateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
  resourceId: string;
  kind: ResourceKind;
  sourceName: string;
}

export function CloneToTemplateDialog({
  open,
  onOpenChange,
  clusterId,
  resourceId,
  kind,
  sourceName,
}: CloneToTemplateDialogProps) {
  const { data: nodes } = useClusterNodes(clusterId);
  const { data: storageList } = useClusterStorage(clusterId);
  const { data: usedVMIDs } = useClusterVMIDs(clusterId);
  const cloneToTemplateMutation = useCloneToTemplate();

  const storageOptions = storageList
    ? [...new Set(storageList
        .filter((s) => s.active && s.enabled && s.content.includes("images"))
        .map((s) => s.storage))]
        .sort()
    : [];

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

  useEffect(() => {
    if (open && newId === "" && usedVMIDs) {
      setNewId(String(nextAvailableId));
    }
    if (open && name === "") {
      setName(`${sourceName}-template`);
    }
  }, [open, usedVMIDs, nextAvailableId, newId, name, sourceName]);

  const numericId = Number(newId);
  const isDuplicate = usedVMIDs ? usedVMIDs.has(numericId) : false;
  const isValid = newId.length > 0 && numericId > 0;

  function handleSubmit(e: React.SyntheticEvent) {
    e.preventDefault();
    cloneToTemplateMutation.mutate(
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
    cloneToTemplateMutation.reset();
    onOpenChange(false);
  }

  const typeLabel = kind === "ct" ? "container" : "VM";

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Clone to Template</DialogTitle>
          <DialogDescription>
            Clone <strong>{sourceName}</strong> and convert the clone into a template.
          </DialogDescription>
        </DialogHeader>

        {upid ? (
          <div className="space-y-4">
            <TaskProgressBanner
              clusterId={clusterId}
              upid={upid}
              onComplete={() => { handleClose(); }}
              description={`Clone ${sourceName} to template`}
            />
            <p className="text-xs text-muted-foreground">
              After cloning completes, the clone will be automatically converted to a template.
            </p>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="rounded-md border border-blue-500/50 bg-blue-500/10 p-3">
              <div className="flex items-start gap-2">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-blue-600 dark:text-blue-400" />
                <p className="text-sm text-blue-700 dark:text-blue-300">
                  This will create a clone of your {typeLabel} and convert it to a read-only template.
                  The original {typeLabel} will not be modified.
                </p>
              </div>
            </div>

            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor="ctt-new-id">New VMID</Label>
                <Input
                  id="ctt-new-id"
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
                <Label htmlFor="ctt-name">Template Name</Label>
                <Input
                  id="ctt-name"
                  value={name}
                  onChange={(e) => { setName(e.target.value); }}
                  placeholder="Optional"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="ctt-target">Target Node</Label>
                <select
                  id="ctt-target"
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
                  <Label htmlFor="ctt-storage">Storage</Label>
                  <select
                    id="ctt-storage"
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
                id="ctt-full"
                checked={full}
                onCheckedChange={(checked) => { setFull(Boolean(checked)); }}
              />
              <Label htmlFor="ctt-full" className="text-sm">
                Full clone (independent copy)
              </Label>
            </div>

            {cloneToTemplateMutation.isError && (
              <p className="text-sm text-destructive">
                {cloneToTemplateMutation.error.message}
              </p>
            )}

            <DialogFooter>
              <Button type="button" variant="outline" onClick={handleClose}>
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={!isValid || cloneToTemplateMutation.isPending}
              >
                {cloneToTemplateMutation.isPending ? "Cloning..." : "Clone to Template"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
