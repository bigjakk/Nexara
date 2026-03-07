import { useState, useMemo } from "react";
import {
  Play,
  Square,
  PowerOff,
  RotateCcw,
  Trash2,
  ArrowLeftRight,
  X,
  Loader2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { Table, Row } from "@tanstack/react-table";
import {
  useVMAction,
  useDestroyVM,
  useMigrateVM,
  useMigrateContainer,
} from "@/features/vms/api/vm-queries";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import type { InventoryRow } from "../types/inventory";
import type { ResourceKind } from "@/features/vms/types/vm";

interface BulkActionToolbarProps {
  table: Table<InventoryRow>;
}

export function BulkActionToolbar({ table }: BulkActionToolbarProps) {
  const selectedRows = table.getFilteredSelectedRowModel().rows;
  const selectedCount = selectedRows.length;
  const actionMutation = useVMAction();
  const destroyMutation = useDestroyVM();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [migrateOpen, setMigrateOpen] = useState(false);
  const [progress, setProgress] = useState<{
    done: number;
    total: number;
  } | null>(null);

  if (selectedCount === 0) return null;

  const vmCtRows = selectedRows.filter(
    (r) => r.original.type === "vm" || r.original.type === "ct",
  );

  async function executeBulkAction(
    action: "start" | "stop" | "shutdown" | "reboot",
  ) {
    const rows = vmCtRows;
    setProgress({ done: 0, total: rows.length });
    let done = 0;
    const promises = rows.map((row) =>
      actionMutation
        .mutateAsync({
          clusterId: row.original.clusterId,
          resourceId: row.original.id,
          kind: row.original.type as ResourceKind,
          action,
        })
        .then(() => {
          done++;
          setProgress({ done, total: rows.length });
        })
        .catch(() => {
          done++;
          setProgress({ done, total: rows.length });
        }),
    );
    await Promise.all(promises);
    setTimeout(() => {
      setProgress(null);
      table.toggleAllRowsSelected(false);
    }, 1500);
  }

  async function executeBulkDelete() {
    const rows = vmCtRows;
    setConfirmDelete(false);
    setProgress({ done: 0, total: rows.length });
    let done = 0;
    const promises = rows.map((row) =>
      destroyMutation
        .mutateAsync({
          clusterId: row.original.clusterId,
          resourceId: row.original.id,
          kind: row.original.type as ResourceKind,
        })
        .then(() => {
          done++;
          setProgress({ done, total: rows.length });
        })
        .catch(() => {
          done++;
          setProgress({ done, total: rows.length });
        }),
    );
    await Promise.all(promises);
    setTimeout(() => {
      setProgress(null);
      table.toggleAllRowsSelected(false);
    }, 1500);
  }

  const isBusy = progress !== null;

  return (
    <>
      <div className="flex items-center gap-2 rounded-lg border bg-muted/50 px-4 py-2">
        <span className="text-sm font-medium">{selectedCount} selected</span>
        {progress ? (
          <div className="flex items-center gap-2 text-sm">
            <Loader2 className="h-3 w-3 animate-spin" />
            {String(progress.done)}/{String(progress.total)} done
          </div>
        ) : (
          <div className="flex gap-1">
            <Button
              variant="outline"
              size="sm"
              className="gap-1"
              disabled={isBusy || vmCtRows.length === 0}
              onClick={() => {
                void executeBulkAction("start");
              }}
            >
              <Play className="h-3 w-3" />
              Start
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="gap-1"
              disabled={isBusy || vmCtRows.length === 0}
              onClick={() => {
                void executeBulkAction("stop");
              }}
            >
              <Square className="h-3 w-3" />
              Stop
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="gap-1"
              disabled={isBusy || vmCtRows.length === 0}
              onClick={() => {
                void executeBulkAction("shutdown");
              }}
            >
              <PowerOff className="h-3 w-3" />
              Shutdown
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="gap-1"
              disabled={isBusy || vmCtRows.length === 0}
              onClick={() => {
                void executeBulkAction("reboot");
              }}
            >
              <RotateCcw className="h-3 w-3" />
              Reboot
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="gap-1"
              disabled={isBusy || vmCtRows.length === 0}
              onClick={() => {
                setMigrateOpen(true);
              }}
            >
              <ArrowLeftRight className="h-3 w-3" />
              Migrate
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="gap-1 text-destructive"
              disabled={isBusy || vmCtRows.length === 0}
              onClick={() => {
                setConfirmDelete(true);
              }}
            >
              <Trash2 className="h-3 w-3" />
              Delete
            </Button>
          </div>
        )}
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            table.toggleAllRowsSelected(false);
          }}
          className="ml-auto gap-1"
          disabled={isBusy}
        >
          <X className="h-3 w-3" />
          Clear
        </Button>
      </div>

      <Dialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Confirm Bulk Delete</DialogTitle>
            <DialogDescription>
              This will destroy {String(vmCtRows.length)} resource
              {vmCtRows.length !== 1 ? "s" : ""}. This action cannot be undone.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                setConfirmDelete(false);
              }}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                void executeBulkDelete();
              }}
            >
              Delete {String(vmCtRows.length)} resource
              {vmCtRows.length !== 1 ? "s" : ""}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <BulkMigrateDialog
        open={migrateOpen}
        onOpenChange={setMigrateOpen}
        rows={vmCtRows}
        onComplete={() => {
          table.toggleAllRowsSelected(false);
        }}
      />
    </>
  );
}

// --- Bulk Migrate Dialog ---

interface BulkMigrateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  rows: Row<InventoryRow>[];
  onComplete: () => void;
}

function BulkMigrateDialog({
  open,
  onOpenChange,
  rows,
  onComplete,
}: BulkMigrateDialogProps) {
  const [targetNode, setTargetNode] = useState("");
  const [online, setOnline] = useState(true);
  const [progress, setProgress] = useState<{
    done: number;
    total: number;
    errors: string[];
  } | null>(null);

  const migrateVM = useMigrateVM();
  const migrateCT = useMigrateContainer();

  // All selected VMs must be from the same cluster for intra-cluster migration.
  // Group by cluster to determine if we can proceed.
  const clusterIds = useMemo(
    () => [...new Set(rows.map((r) => r.original.clusterId))],
    [rows],
  );
  const singleCluster = clusterIds.length === 1;
  const clusterId = clusterIds[0] ?? "";

  const nodesQuery = useClusterNodes(singleCluster ? clusterId : "");
  const nodes = nodesQuery.data ?? [];
  const onlineNodes = nodes.filter((n) => n.status === "online");

  const handleClose = (v: boolean) => {
    if (progress) return; // Don't close while migrating
    onOpenChange(v);
    if (!v) {
      setTargetNode("");
      setProgress(null);
    }
  };

  const handleMigrate = async () => {
    if (!targetNode || rows.length === 0) return;

    const total = rows.length;
    setProgress({ done: 0, total, errors: [] });
    let done = 0;
    const errors: string[] = [];

    const promises = rows.map((row) => {
      const isVM = row.original.type === "vm";
      const promise = isVM
        ? migrateVM.mutateAsync({
            clusterId: row.original.clusterId,
            vmId: row.original.id,
            body: { target: targetNode, online },
          })
        : migrateCT.mutateAsync({
            clusterId: row.original.clusterId,
            containerId: row.original.id,
            body: { target: targetNode, online },
          });

      return promise
        .then(() => {
          done++;
          setProgress({ done, total, errors: [...errors] });
        })
        .catch((err: Error) => {
          done++;
          errors.push(
            `${row.original.name} (${String(row.original.vmid)}): ${err.message}`,
          );
          setProgress({ done, total, errors: [...errors] });
        });
    });

    await Promise.all(promises);

    if (errors.length === 0) {
      setTimeout(() => {
        setProgress(null);
        onOpenChange(false);
        setTargetNode("");
        onComplete();
      }, 1500);
    }
  };

  const isDone = progress !== null && progress.done === progress.total;

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Bulk Migrate</DialogTitle>
          <DialogDescription>
            Migrate {String(rows.length)} resource
            {rows.length !== 1 ? "s" : ""} to a target node.
          </DialogDescription>
        </DialogHeader>

        {!singleCluster ? (
          <p className="text-sm text-destructive">
            All selected resources must be from the same cluster for bulk
            migration. You have resources from {String(clusterIds.length)}{" "}
            clusters selected.
          </p>
        ) : progress ? (
          <div className="space-y-3 py-2">
            <div className="flex items-center gap-2 text-sm">
              {isDone ? (
                <span className="font-medium">
                  Completed {String(progress.done)}/{String(progress.total)}
                </span>
              ) : (
                <>
                  <Loader2 className="h-4 w-4 animate-spin" />
                  <span>
                    Migrating {String(progress.done)}/{String(progress.total)}
                    ...
                  </span>
                </>
              )}
            </div>
            <div className="h-2 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full bg-primary transition-all"
                style={{
                  width: `${String(Math.round((progress.done / progress.total) * 100))}%`,
                }}
              />
            </div>
            {progress.errors.length > 0 && (
              <div className="max-h-32 space-y-1 overflow-y-auto rounded border border-destructive/30 bg-destructive/5 p-2">
                {progress.errors.map((e, i) => (
                  <p key={i} className="text-xs text-destructive">
                    {e}
                  </p>
                ))}
              </div>
            )}
          </div>
        ) : (
          <div className="space-y-4 py-2">
            <div className="space-y-2">
              <Label>Target Node</Label>
              <select
                className="w-full rounded-md border bg-background px-3 py-2 text-sm"
                value={targetNode}
                onChange={(e) => {
                  setTargetNode(e.target.value);
                }}
              >
                <option value="">Select node...</option>
                {onlineNodes.map((n) => (
                  <option key={n.id} value={n.name}>
                    {n.name}
                  </option>
                ))}
              </select>
            </div>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={online}
                onChange={(e) => {
                  setOnline(e.target.checked);
                }}
                className="rounded"
              />
              Online migration (live, no downtime)
            </label>
            <div className="rounded border bg-muted/30 p-3">
              <p className="mb-1 text-xs font-medium text-muted-foreground">
                Resources to migrate:
              </p>
              <div className="max-h-32 space-y-0.5 overflow-y-auto text-sm">
                {rows.map((r) => (
                  <div key={r.original.key} className="flex gap-2">
                    <span className="font-mono text-xs text-muted-foreground">
                      {r.original.type.toUpperCase()} {String(r.original.vmid)}
                    </span>
                    <span>{r.original.name}</span>
                    <span className="text-xs text-muted-foreground">
                      ({r.original.nodeName})
                    </span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        )}

        <DialogFooter>
          {isDone && progress && progress.errors.length > 0 ? (
            <Button
              variant="outline"
              onClick={() => {
                setProgress(null);
                onOpenChange(false);
                setTargetNode("");
                onComplete();
              }}
            >
              Close
            </Button>
          ) : !progress ? (
            <>
              <Button
                variant="outline"
                onClick={() => {
                  handleClose(false);
                }}
              >
                Cancel
              </Button>
              <Button
                onClick={() => {
                  void handleMigrate();
                }}
                disabled={!targetNode || !singleCluster}
              >
                Migrate {String(rows.length)} resource
                {rows.length !== 1 ? "s" : ""}
              </Button>
            </>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
