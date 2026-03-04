import { useState } from "react";
import { Play, Square, Trash2, X, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { Table } from "@tanstack/react-table";
import { useVMAction, useDestroyVM } from "@/features/vms/api/vm-queries";
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
  const [progress, setProgress] = useState<{ done: number; total: number } | null>(null);

  if (selectedCount === 0) return null;

  const vmCtRows = selectedRows.filter(
    (r) => r.original.type === "vm" || r.original.type === "ct",
  );

  async function executeBulkAction(action: "start" | "stop") {
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
        <span className="text-sm font-medium">
          {selectedCount} selected
        </span>
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
              onClick={() => { void executeBulkAction("start"); }}
            >
              <Play className="h-3 w-3" />
              Start
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="gap-1"
              disabled={isBusy || vmCtRows.length === 0}
              onClick={() => { void executeBulkAction("stop"); }}
            >
              <Square className="h-3 w-3" />
              Stop
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="gap-1 text-destructive"
              disabled={isBusy || vmCtRows.length === 0}
              onClick={() => { setConfirmDelete(true); }}
            >
              <Trash2 className="h-3 w-3" />
              Delete
            </Button>
          </div>
        )}
        <Button
          variant="ghost"
          size="sm"
          onClick={() => { table.toggleAllRowsSelected(false); }}
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
              onClick={() => { setConfirmDelete(false); }}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={() => { void executeBulkDelete(); }}
            >
              Delete {String(vmCtRows.length)} resource
              {vmCtRows.length !== 1 ? "s" : ""}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
