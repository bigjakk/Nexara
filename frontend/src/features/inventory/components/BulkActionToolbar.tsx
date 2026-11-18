import { useState } from "react";
import {
  Play,
  Square,
  PowerOff,
  RotateCcw,
  Trash2,
  ArrowLeftRight,
  MoreHorizontal,
  X,
  Loader2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
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
import {
  useGuestActionAllowed,
  type GuestAction,
} from "@/features/vms/lib/vm-action-defs";
import type { InventoryRow } from "../types/inventory";
import type { ResourceKind } from "@/features/vms/types/vm";
import { BulkMigrateDialog } from "./BulkMigrateDialog";

interface BulkActionToolbarProps {
  table: Table<InventoryRow>;
}

export function BulkActionToolbar({ table }: BulkActionToolbarProps) {
  const selectedRows = table.getFilteredSelectedRowModel().rows;
  const selectedCount = selectedRows.length;
  const actionMutation = useVMAction();
  const destroyMutation = useDestroyVM();
  const allowed = useGuestActionAllowed();
  const [confirmDelete, setConfirmDelete] = useState(false);
  // `verb` asks the question and labels its button ("Shut down 3 resources");
  // `label` is the noun the title uses.
  const [confirmBulk, setConfirmBulk] = useState<{
    action: "stop" | "shutdown";
    label: string;
    verb: string;
  } | null>(null);
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
      table.resetRowSelection(true);
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
      table.resetRowSelection(true);
    }, 1500);
  }

  const isBusy = progress !== null;

  const noGuests = isBusy || vmCtRows.length === 0;
  // An action is offered only when the role allows it for every kind of guest
  // selected. A role held on one cluster can still be refused for another's guests.
  const kinds = [
    ...new Set(vmCtRows.map((r) => r.original.type as ResourceKind)),
  ];
  const can = (action: GuestAction) =>
    kinds.length > 0 && kinds.every((k) => allowed(action, k));

  return (
    <>
      {/* Sits in the table panel's title row, in place of the view controls.
          The everyday actions are buttons; the forced stop and the delete are
          one step further away, under More. */}
      <div
        role="group"
        aria-label="Bulk actions"
        className="flex flex-wrap items-center gap-1.5"
      >
        <span className="mr-1 text-sm font-medium tabular-nums">
          {selectedCount} selected
        </span>
        {progress ? (
          <span className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="h-3.5 w-3.5 animate-spin text-info" />
            {String(progress.done)}/{String(progress.total)} done
          </span>
        ) : (
          <>
            {can("start") && (
              <Button
                variant="outline"
                size="sm"
                className="gap-1.5"
                disabled={noGuests}
                onClick={() => {
                  void executeBulkAction("start");
                }}
              >
                <Play />
                Start
              </Button>
            )}
            {can("shutdown") && (
              <Button
                variant="outline"
                size="sm"
                className="gap-1.5"
                disabled={noGuests}
                onClick={() => {
                  setConfirmBulk({
                    action: "shutdown",
                    label: "Shutdown",
                    verb: "Shut down",
                  });
                }}
              >
                <PowerOff />
                Shutdown
              </Button>
            )}
            {can("reboot") && (
              <Button
                variant="outline"
                size="sm"
                className="gap-1.5"
                disabled={noGuests}
                onClick={() => {
                  void executeBulkAction("reboot");
                }}
              >
                <RotateCcw />
                Reboot
              </Button>
            )}
            {can("migrate") && (
              <Button
                variant="outline"
                size="sm"
                className="gap-1.5"
                disabled={noGuests}
                onClick={() => {
                  setMigrateOpen(true);
                }}
              >
                <ArrowLeftRight />
                Migrate
              </Button>
            )}
            {(can("stop") || can("destroy")) && (
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button
                    variant="outline"
                    size="sm"
                    className="px-2"
                    aria-label="More bulk actions"
                    disabled={noGuests}
                  >
                    <MoreHorizontal />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  {can("stop") && (
                    <DropdownMenuItem
                      className="gap-2"
                      onClick={() => {
                        setConfirmBulk({
                          action: "stop",
                          label: "Stop",
                          verb: "Stop",
                        });
                      }}
                    >
                      <Square className="h-4 w-4" />
                      Stop
                    </DropdownMenuItem>
                  )}
                  {can("stop") && can("destroy") && <DropdownMenuSeparator />}
                  {can("destroy") && (
                    <DropdownMenuItem
                      className="gap-2 text-destructive focus:text-destructive"
                      onClick={() => {
                        setConfirmDelete(true);
                      }}
                    >
                      <Trash2 className="h-4 w-4" />
                      Delete
                    </DropdownMenuItem>
                  )}
                </DropdownMenuContent>
              </DropdownMenu>
            )}
          </>
        )}
        {/* Empties the selection itself, here and after each bulk action:
            unticking the rows on show would leave one that a filter has
            hidden still selected, to come back ticked later. */}
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            table.resetRowSelection(true);
          }}
          className="gap-1 px-2 text-muted-foreground"
          disabled={isBusy}
        >
          <X />
          Clear
        </Button>
      </div>

      <Dialog
        open={confirmBulk !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmBulk(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              Confirm bulk {confirmBulk?.label.toLowerCase()}
            </DialogTitle>
            <DialogDescription>
              This will {confirmBulk?.verb.toLowerCase()}{" "}
              {String(vmCtRows.length)} resource
              {vmCtRows.length !== 1 ? "s" : ""}. Running guests may lose
              unsaved data.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                setConfirmBulk(null);
              }}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                const pending = confirmBulk;
                setConfirmBulk(null);
                if (pending) void executeBulkAction(pending.action);
              }}
            >
              {confirmBulk?.verb} {String(vmCtRows.length)} resource
              {vmCtRows.length !== 1 ? "s" : ""}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Confirm bulk delete</DialogTitle>
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
          table.resetRowSelection(true);
        }}
      />
    </>
  );
}
