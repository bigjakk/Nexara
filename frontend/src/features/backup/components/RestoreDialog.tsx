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
import { TaskProgressBanner } from "@/features/vms/components/TaskProgressBanner";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
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
  const [unique, setUnique] = useState(true);
  const [startAfter, setStartAfter] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [restoreUpid, setRestoreUpid] = useState<string | null>(null);

  const clustersQuery = useClusters();
  const clusters = clustersQuery.data ?? [];
  const nodesQuery = useClusterNodes(targetClusterId);
  const nodes = nodesQuery.data ?? [];
  const storageQuery = useClusterStorage(targetClusterId);
  const allStorage = storageQuery.data ?? [];
  const vmsQuery = useClusterVMs(targetClusterId);
  const clusterVMs = vmsQuery.data ?? [];

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

  const handleRestore = () => {
    if (!targetClusterId || !targetNode || !vmid) return;

    const parsedVmid = parseInt(vmid, 10);
    if (isNaN(parsedVmid) || parsedVmid <= 0) return;

    setError(null);
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
          ...(storage ? { storage } : {}),
          force,
          unique,
          start_after_restore: startAfter,
        },
      },
      {
        onSuccess: (data) => {
          setError(null);
          setRestoreUpid(data.upid);
        },
        onError: (err: Error) => {
          setError(err.message || "Restore failed");
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
          <DialogTitle>Restore Backup</DialogTitle>
          <DialogDescription>
            Restore {snapshot.backup_type.toUpperCase()}/{snapshot.backup_id} from{" "}
            {snapshot.datastore} to a PVE node.
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
              <div className="flex items-start gap-2 rounded-md border border-yellow-500/50 bg-yellow-500/10 p-3 text-sm">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-yellow-600" />
                <span>
                  No PBS storage found on this PVE cluster. The PBS server must
                  be added as a storage in PVE (Datacenter &gt; Storage &gt; Add
                  &gt; Proxmox Backup Server) before you can restore.
                </span>
              </div>
            )}

            <div className="space-y-4 py-2">
              <div className="space-y-2">
                <Label>Target Cluster</Label>
                <select
                  className="w-full rounded-md border bg-background px-3 py-2 text-sm"
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
                <Label>Target Node</Label>
                <select
                  className="w-full rounded-md border bg-background px-3 py-2 text-sm"
                  value={targetNode}
                  onChange={(e) => {
                    setTargetNode(e.target.value);
                  }}
                  disabled={nodes.length === 0 || (force && !!existingVMNode)}
                >
                  <option value="">Select node...</option>
                  {nodes.map((n) => (
                    <option key={n.id} value={n.name}>
                      {n.name}{" "}
                      {n.status === "online" ? "" : `(${n.status})`}
                    </option>
                  ))}
                </select>
                {force && existingVMNode && (
                  <p className="text-xs text-muted-foreground">
                    Auto-selected {existingVMNode.name} — VM {vmid} exists on
                    this node.
                  </p>
                )}
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
                <p className="text-xs text-muted-foreground">
                  Defaults to the original VMID. Change to restore as a new VM.
                </p>
              </div>

              <div className="space-y-2">
                <Label>Target Storage (optional)</Label>
                <select
                  className="w-full rounded-md border bg-background px-3 py-2 text-sm"
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
                      setUnique(e.target.checked);
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
                    checked={startAfter}
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

              {error && (
                <p className="text-sm text-destructive">{error}</p>
              )}
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
