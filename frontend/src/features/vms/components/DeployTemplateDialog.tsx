import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useCloneVM, useClusterVMIDs } from "../api/vm-queries";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { useClusterStorage } from "@/features/storage/api/storage-queries";
import { TaskProgressBanner } from "./TaskProgressBanner";
import type { ResourceKind } from "../types/vm";

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

interface DeployTemplateDialogProps {
  clusterId: string;
  vmId: string;
  kind: ResourceKind;
  templateName: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function DeployTemplateDialog({
  clusterId,
  vmId,
  kind,
  templateName,
  open,
  onOpenChange,
}: DeployTemplateDialogProps) {
  const cloneMutation = useCloneVM();
  const { data: existingIds } = useClusterVMIDs(clusterId);
  const { data: nodes } = useClusterNodes(clusterId);
  const { data: storages } = useClusterStorage(clusterId);

  // Filter to active storages that support disk images or rootdir
  const diskStorages = storages?.filter((s) => {
    if (!s.active || !s.enabled) return false;
    const content = s.content.split(",").map((c) => c.trim());
    return content.includes("images") || content.includes("rootdir");
  });

  const [newId, setNewId] = useState("");
  const [newName, setNewName] = useState("");
  const [fullClone, setFullClone] = useState(true);
  const [storage, setStorage] = useState("");
  const [targetNode, setTargetNode] = useState("");
  const [upid, setUpid] = useState<string | null>(null);

  function getNextId(): number {
    if (!existingIds) return 100;
    let id = 100;
    while (existingIds.has(id)) id++;
    return id;
  }

  function handleOpen(isOpen: boolean) {
    if (isOpen) {
      const next = getNextId();
      setNewId(String(next));
      setNewName(`${templateName}-clone`);
      setFullClone(true);
      setStorage("");
      setTargetNode("");
      setUpid(null);
    }
    onOpenChange(isOpen);
  }

  function handleDeploy(e: React.SyntheticEvent) {
    e.preventDefault();
    const parsedId = parseInt(newId, 10);
    if (isNaN(parsedId) || parsedId < 100) return;

    cloneMutation.mutate(
      {
        clusterId,
        resourceId: vmId,
        kind,
        body: {
          new_id: parsedId,
          name: newName,
          target: targetNode,
          full: fullClone,
          storage,
        },
      },
      {
        onSuccess: (data) => {
          setUpid(data.upid);
        },
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={handleOpen}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Deploy Template</DialogTitle>
          <DialogDescription>
            Clone &ldquo;{templateName}&rdquo; to create a new {kind === "ct" ? "container" : "VM"}.
          </DialogDescription>
        </DialogHeader>

        {upid && (
          <TaskProgressBanner
            clusterId={clusterId}
            upid={upid}
            kind={kind}
            resourceId={vmId}
            onComplete={() => {
              setUpid(null);
              onOpenChange(false);
            }}
            description="Deploying template"
          />
        )}

        {!upid && (
          <form onSubmit={handleDeploy} className="space-y-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor="deploy-vmid">New VMID</Label>
                <Input
                  id="deploy-vmid"
                  type="number"
                  min={100}
                  value={newId}
                  onChange={(e) => { setNewId(e.target.value); }}
                  required
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="deploy-name">Name</Label>
                <Input
                  id="deploy-name"
                  value={newName}
                  onChange={(e) => { setNewName(e.target.value); }}
                  required
                />
              </div>
            </div>

            {nodes && nodes.length > 0 && (
              <div className="space-y-2">
                <Label htmlFor="deploy-node">Target Node</Label>
                <select
                  id="deploy-node"
                  value={targetNode}
                  onChange={(e) => { setTargetNode(e.target.value); }}
                  className={selectClass}
                >
                  <option value="">Same node</option>
                  {nodes.map((n) => (
                    <option key={n.id} value={n.name}>{n.name}</option>
                  ))}
                </select>
              </div>
            )}

            <div className="space-y-2">
              <Label htmlFor="deploy-storage">Target Storage</Label>
              <select
                id="deploy-storage"
                value={storage}
                onChange={(e) => { setStorage(e.target.value); }}
                className={selectClass}
              >
                <option value="">Default (same as template)</option>
                {diskStorages?.map((s) => (
                  <option key={s.id} value={s.storage}>{s.storage} ({s.type})</option>
                ))}
              </select>
            </div>

            <div className="flex items-center gap-2">
              <Checkbox
                id="deploy-full"
                checked={fullClone}
                onCheckedChange={(checked) => { setFullClone(Boolean(checked)); }}
              />
              <Label htmlFor="deploy-full" className="text-sm">
                Full Clone (independent copy)
              </Label>
            </div>

            {cloneMutation.isError && (
              <p className="text-sm text-destructive">
                {cloneMutation.error.message}
              </p>
            )}

            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => { onOpenChange(false); }}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={cloneMutation.isPending}>
                {cloneMutation.isPending ? "Deploying..." : "Deploy"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
