import { useState, useEffect, useMemo } from "react";
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

import { RotateCcw, AlertTriangle, Loader2 } from "lucide-react";
import { useRestoreBackup } from "../api/backup-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { describeError } from "@/lib/api-error";
import { TaskProgressBanner } from "@/features/vms/components/TaskProgressBanner";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { usePermissions } from "@/hooks/usePermissions";
import {
  useClusterNodes,
  useClusterStorage,
  useClusterVMs,
} from "@/features/clusters/api/cluster-queries";
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
  const [force, setForce] = useState(false);
  // Unique gives the guest new MAC addresses and a new identity: right for a
  // copy under a new VMID, wrong for the original guest coming back. So it
  // follows the VMID until the user sets it.
  const [uniqueChoice, setUniqueChoice] = useState<boolean | null>(null);
  const unique = uniqueChoice ?? vmid.trim() !== snapshot.backup_id;
  const [startAfter, setStartAfter] = useState(false);
  // The server holds a start after restore to what the guest's power route
  // needs (execute on its kind), so the tick is offered only to a holder.
  const { canExecute } = usePermissions();
  const canStart = canExecute(
    snapshot.backup_type === "ct" ? "container" : "vm",
  );
  const [error, setError] = useState<string | null>(null);
  const [restoreUpid, setRestoreUpid] = useState<string | null>(null);

  const clustersQuery = useClusters();
  const clusters = useMemo(
    () => clustersQuery.data ?? [],
    [clustersQuery.data],
  );
  const nodesQuery = useClusterNodes(targetClusterId);
  const nodes = useMemo(() => nodesQuery.data ?? [], [nodesQuery.data]);
  const storageQuery = useClusterStorage(targetClusterId);
  const allStorage = useMemo(
    () => storageQuery.data ?? [],
    [storageQuery.data],
  );
  const vmsQuery = useClusterVMs(targetClusterId);
  const clusterVMs = useMemo(() => vmsQuery.data ?? [], [vmsQuery.data]);

  // Filter to storage that accepts disk images, deduplicate shared storage
  const targetStorage = useMemo(() => {
    const seen = new Set<string>();
    return allStorage.filter((s) => {
      if (!s.active || !s.enabled) return false;
      if (!s.content.includes("images") && !s.content.includes("rootdir"))
        return false;
      if (seen.has(s.storage)) return false;
      seen.add(s.storage);
      return true;
    });
  }, [allStorage]);

  // Find PBS storage entries for this cluster (used to build archive string)
  const pbsStorage = allStorage.filter((s) => s.type === "pbs");

  // Find which node an existing VM lives on (for force restore)
  const existingVM = useMemo(() => {
    const parsed = parseInt(vmid, 10);
    if (isNaN(parsed)) return undefined;
    return clusterVMs.find((v) => v.vmid === parsed);
  }, [clusterVMs, vmid]);

  const existingVMNode = useMemo(() => {
    if (!existingVM) return undefined;
    return nodes.find((n) => n.id === existingVM.node_id);
  }, [existingVM, nodes]);

  // Auto-select first cluster
  useEffect(() => {
    if (clusters.length > 0 && !targetClusterId) {
      setTargetClusterId(clusters[0]?.id ?? "");
    }
  }, [clusters, targetClusterId]);

  // Auto-select first online node when cluster changes
  useEffect(() => {
    if (nodes.length > 0 && !targetNode) {
      const online = nodes.find((n) => n.status === "online");
      setTargetNode(online?.name ?? nodes[0]?.name ?? "");
    }
  }, [nodes, targetNode]);

  // When force is checked and VM exists on a specific node, auto-select that node
  useEffect(() => {
    if (force && existingVMNode) {
      setTargetNode(existingVMNode.name);
    }
  }, [force, existingVMNode]);

  const restoreMutation = useRestoreBackup();
  // The dialog shows a failure itself (the hook opts out of the app's toast),
  // and one that lands after it was closed is toasted instead.
  const settle = useSaveOutcome(open);

  const handleRestore = () => {
    if (!targetClusterId || !targetNode || !vmid) return;

    const parsedVmid = parseInt(vmid, 10);
    if (isNaN(parsedVmid) || parsedVmid <= 0) return;

    setError(null);
    settle(
      restoreMutation.mutateAsync({
        clusterId: targetClusterId,
        body: {
          pbs_server_id: pbsId,
          backup_type: snapshot.backup_type,
          backup_id: snapshot.backup_id,
          backup_time: snapshot.backup_time,
          datastore: snapshot.datastore,
          target_node: targetNode,
          vmid: parsedVmid,
          ...(storage ? { storage } : {}),
          force,
          unique,
          start_after_restore: startAfter && canStart,
        },
      }),
      {
        action: `Restoring ${snapshot.backup_type}/${snapshot.backup_id} to ${targetNode}`,
        onSuccess: (data) => {
          setError(null);
          setRestoreUpid(data.upid);
        },
        onError: (err) => {
          setError(describeError(err) || "Restore failed");
        },
      },
    );
  };

  const handleOpenChange = (v: boolean) => {
    setOpen(v);
    if (!v) {
      setError(null);
      setTargetNode("");
      setRestoreUpid(null);
    }
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="sm" title="Restore">
          <RotateCcw className="h-4 w-4" />
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Restore backup</DialogTitle>
          <DialogDescription>
            Restore {snapshot.backup_type.toUpperCase()}/{snapshot.backup_id}{" "}
            from {snapshot.datastore} to a PVE node.
          </DialogDescription>
        </DialogHeader>

        {restoreUpid ? (
          <>
            <div className="py-2">
              <TaskProgressBanner
                clusterId={targetClusterId}
                upid={restoreUpid}
                description={`Restoring ${snapshot.backup_type}/${snapshot.backup_id}`}
              />
            </div>
            <DialogFooter>
              <Button
                variant="outline"
                onClick={() => {
                  handleOpenChange(false);
                }}
              >
                Close
              </Button>
            </DialogFooter>
          </>
        ) : (
          <>
            {pbsStorage.length === 0 && (
              <div className="flex items-start gap-2 rounded-md border border-amber-500/50 bg-amber-500/10 p-3 text-sm">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400" />
                <span>
                  No PBS storage found on this PVE cluster. The PBS server must
                  be added as a storage in PVE (Datacenter &gt; Storage &gt; Add
                  &gt; Proxmox Backup Server) before you can restore.
                </span>
              </div>
            )}

            <div className="space-y-4 py-2">
              <div className="space-y-2">
                <Label htmlFor="restore-cluster">Target cluster</Label>
                <select
                  id="restore-cluster"
                  className="h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
                  value={targetClusterId}
                  onChange={(e) => {
                    setTargetClusterId(e.target.value);
                    setTargetNode("");
                    setStorage("");
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
                <Label htmlFor="restore-node">Target node</Label>
                <select
                  id="restore-node"
                  className="h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
                  value={targetNode}
                  onChange={(e) => {
                    setTargetNode(e.target.value);
                  }}
                  disabled={nodes.length === 0 || (force && !!existingVMNode)}
                >
                  <option value="">Select node...</option>
                  {nodes.map((n) => (
                    <option key={n.id} value={n.name}>
                      {n.name} {n.status === "online" ? "" : `(${n.status})`}
                    </option>
                  ))}
                </select>
                {force && existingVMNode && (
                  <p className="text-xs text-muted-foreground">
                    Auto-selected {existingVMNode.name}: VM {vmid} exists on
                    this node.
                  </p>
                )}
              </div>

              <div className="space-y-2">
                <Label htmlFor="restore-vmid">VMID</Label>
                <Input
                  id="restore-vmid"
                  value={vmid}
                  onChange={(e) => {
                    setVmid(e.target.value);
                  }}
                  placeholder="e.g., 100"
                />
                <p className="text-xs text-muted-foreground">
                  Defaults to the original VMID. Change to restore as a new VM.
                </p>
              </div>

              <div className="space-y-2">
                <Label htmlFor="restore-storage">
                  Target storage (optional)
                </Label>
                <select
                  id="restore-storage"
                  className="h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
                  value={storage}
                  onChange={(e) => {
                    setStorage(e.target.value);
                  }}
                >
                  <option value="">Default (original storage)</option>
                  {targetStorage.map((s) => (
                    <option key={s.storage} value={s.storage}>
                      {s.storage} ({s.type})
                    </option>
                  ))}
                </select>
              </div>

              <div className="flex flex-wrap items-center gap-4">
                <label className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={unique}
                    onChange={(e) => {
                      setUniqueChoice(e.target.checked);
                    }}
                    className="rounded"
                  />
                  Unique (new MAC addresses)
                </label>
                <label className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={force}
                    onChange={(e) => {
                      setForce(e.target.checked);
                    }}
                    className="rounded"
                  />
                  Overwrite existing VM
                </label>
                <label className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={startAfter && canStart}
                    disabled={!canStart}
                    onChange={(e) => {
                      setStartAfter(e.target.checked);
                    }}
                    className="rounded"
                  />
                  Start after restore
                </label>
              </div>

              {force && (
                <div className="flex items-start gap-2 rounded-md border border-destructive/50 bg-destructive/10 p-3 text-sm text-destructive">
                  <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
                  <span>
                    This will stop VM {vmid} and overwrite it with the backup.
                    This cannot be undone.
                  </span>
                </div>
              )}

              {error && <p className="text-sm text-destructive">{error}</p>}
            </div>

            <DialogFooter>
              <Button
                variant="outline"
                onClick={() => {
                  handleOpenChange(false);
                }}
              >
                Cancel
              </Button>
              <Button
                onClick={handleRestore}
                disabled={
                  restoreMutation.isPending ||
                  !targetClusterId ||
                  !targetNode ||
                  !vmid ||
                  pbsStorage.length === 0
                }
              >
                {restoreMutation.isPending ? (
                  <>
                    <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                    Restoring...
                  </>
                ) : (
                  "Restore"
                )}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
