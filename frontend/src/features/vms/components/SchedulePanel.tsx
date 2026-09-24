import { useState } from "react";
import { Trash2, Plus, Clock, AlertCircle, CheckCircle2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import {
  useScheduledTasks,
  useCreateSchedule,
  useDeleteSchedule,
  type ScheduledTask,
} from "../api/vm-queries";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import { snapshotNameError, SNAPSHOT_NAME_RULES } from "../lib/snapshot-name";
import type { ResourceKind } from "../types/vm";

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring";

interface SchedulePanelProps {
  clusterId: string;
  kind: ResourceKind;
  vmid: number;
  node: string;
}

export function SchedulePanel({
  clusterId,
  kind,
  vmid,
  node,
}: SchedulePanelProps) {
  const { data: schedules, isLoading } = useScheduledTasks(clusterId);
  const createSchedule = useCreateSchedule();
  const deleteSchedule = useDeleteSchedule();

  const [dialogOpen, setDialogOpen] = useState(false);
  const [action, setAction] = useState("snapshot");
  const [cronExpr, setCronExpr] = useState("0 2 * * *");
  const [snapName, setSnapName] = useState("");
  const [pendingDelete, setPendingDelete] = useState<ScheduledTask | null>(
    null,
  );

  // Scoped to the snapshot action because handleCreate only sends snap_name
  // for "snapshot". A bad name left behind by switching the action away would
  // otherwise disable Create from a field that is no longer on screen.
  const snapNameError =
    action === "snapshot" ? snapshotNameError(snapName, kind) : null;

  // Filter schedules to this resource.
  const mySchedules = schedules?.filter(
    (s) =>
      s.resource_id === String(vmid) &&
      s.resource_type === (kind === "ct" ? "ct" : "vm"),
  );

  function handleCreate() {
    const params: Record<string, unknown> = {};
    if (action === "snapshot" && snapName) {
      params["snap_name"] = snapName;
    }

    createSchedule.mutate(
      {
        clusterId,
        body: {
          resource_type: kind === "ct" ? "ct" : "vm",
          resource_id: String(vmid),
          node,
          action,
          schedule: cronExpr,
          params,
          enabled: true,
        },
      },
      {
        onSuccess: () => {
          setDialogOpen(false);
          setSnapName("");
        },
      },
    );
  }

  function handleDelete(scheduleId: string) {
    deleteSchedule.mutate({ clusterId, scheduleId });
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-lg font-semibold">Scheduled Tasks</h3>
        <Button
          size="sm"
          className="gap-2"
          onClick={() => {
            setDialogOpen(true);
          }}
        >
          <Plus className="h-4 w-4" />
          Add Schedule
        </Button>
      </div>

      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : !mySchedules?.length ? (
        <p className="text-sm text-muted-foreground">
          No scheduled tasks for this resource.
        </p>
      ) : (
        <div className="rounded-md border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-4 py-2 text-left font-medium">Action</th>
                <th className="px-4 py-2 text-left font-medium">Schedule</th>
                <th className="px-4 py-2 text-left font-medium">Status</th>
                <th className="px-4 py-2 text-left font-medium">Next Run</th>
                <th className="px-4 py-2 text-left font-medium">Last Run</th>
                <th className="px-4 py-2 text-right font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {mySchedules.map((s) => (
                <tr key={s.id} className="border-b">
                  <td className="px-4 py-2 font-medium capitalize">
                    {s.action}
                  </td>
                  <td className="px-4 py-2 font-mono text-xs">{s.schedule}</td>
                  <td className="px-4 py-2">
                    <div className="flex flex-col gap-1">
                      <div className="flex items-center gap-2">
                        <StatusIcon status={s.last_status} />
                        {/* The scheduler disables a task whose cron can never
                            fire. Without this the row just stops, with a failed
                            icon and no next run, and nothing saying why. */}
                        {!s.enabled && (
                          <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                            Disabled
                          </span>
                        )}
                      </div>
                      {s.last_error ? (
                        <span
                          className="max-w-[22rem] truncate text-xs text-destructive"
                          title={s.last_error}
                        >
                          {s.last_error}
                        </span>
                      ) : null}
                    </div>
                  </td>
                  <td className="px-4 py-2 text-xs text-muted-foreground">
                    {s.next_run_at
                      ? new Date(s.next_run_at).toLocaleString()
                      : "--"}
                  </td>
                  <td className="px-4 py-2 text-xs text-muted-foreground">
                    {s.last_run_at
                      ? new Date(s.last_run_at).toLocaleString()
                      : "--"}
                  </td>
                  <td className="px-4 py-2 text-right">
                    <Button
                      aria-label={`Delete ${s.action} schedule ${s.schedule}`}
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        setPendingDelete(s);
                      }}
                    >
                      <Trash2 className="h-4 w-4 text-destructive" />
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <ConfirmDeleteDialog
        target={pendingDelete}
        onClose={() => {
          setPendingDelete(null);
        }}
        onConfirm={(s) => {
          handleDelete(s.id);
        }}
        title={(s) => `Delete the ${s.action} schedule ${s.schedule}?`}
        // The DELETE removes only the scheduled_tasks row
        // (handlers.ScheduleHandler.Delete); nothing prunes what earlier runs
        // made, and a run the scheduler already dispatched is not cancelled.
        description={(s) =>
          `No further ${s.action} runs are started for this ${kind === "ct" ? "container" : "VM"}. A run already under way is not stopped${s.action === "snapshot" ? ", and snapshots earlier runs took are kept" : ""}. This cannot be undone; add the schedule again to resume it.`
        }
      />

      {/* Create Dialog */}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create Scheduled Task</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            <div>
              <Label>Action</Label>
              <select
                className={selectClass}
                value={action}
                onChange={(e) => {
                  setAction(e.target.value);
                }}
              >
                <option value="snapshot">Snapshot</option>
                <option value="reboot">Reboot</option>
              </select>
            </div>
            <div>
              <Label>Cron Expression</Label>
              <Input
                value={cronExpr}
                onChange={(e) => {
                  setCronExpr(e.target.value);
                }}
                placeholder="0 2 * * *"
              />
              <p className="mt-1 text-xs text-muted-foreground">
                Format: minute hour day month weekday (e.g. &quot;0 2 * *
                *&quot; = daily at 2 AM)
              </p>
            </div>
            {action === "snapshot" && (
              <div>
                <Label htmlFor="schedule-snap-name">
                  Snapshot Name (optional)
                </Label>
                <Input
                  id="schedule-snap-name"
                  value={snapName}
                  onChange={(e) => {
                    setSnapName(e.target.value);
                  }}
                  placeholder="Optional"
                  maxLength={40}
                />
                {/* Not a template: the scheduler stores this string and passes
                    it to Proxmox verbatim, minting "auto-<timestamp>" only
                    when it is empty (internal/scheduler, executeSnapshot). The
                    old "auto-YYYYMMDD-HHMMSS" placeholder implied a
                    substitution that does not exist — and, being a legal name,
                    would have been taken literally had anyone typed it. */}
                <p
                  className={
                    snapNameError
                      ? "mt-1 text-xs text-destructive"
                      : "mt-1 text-xs text-muted-foreground"
                  }
                >
                  {snapNameError ??
                    (snapName.length === 0
                      ? "Leave empty to auto-generate a timestamped name for each run."
                      : `Used as-is on every run — no date is substituted, so a recurring job collides with its own previous snapshot. Leave empty to auto-name each run. ${SNAPSHOT_NAME_RULES}`)}
                </p>
              </div>
            )}
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                setDialogOpen(false);
              }}
            >
              Cancel
            </Button>
            <Button
              onClick={handleCreate}
              disabled={
                createSchedule.isPending || !cronExpr || snapNameError !== null
              }
            >
              {createSchedule.isPending ? "Creating..." : "Create"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function StatusIcon({ status }: { status: string | null }) {
  if (!status) {
    return (
      <span className="flex items-center gap-1 text-xs text-muted-foreground">
        <Clock className="h-3 w-3" />
        Pending
      </span>
    );
  }
  if (status === "success") {
    return (
      <span className="flex items-center gap-1 text-xs text-emerald-600">
        <CheckCircle2 className="h-3 w-3" />
        Success
      </span>
    );
  }
  return (
    <span className="flex items-center gap-1 text-xs text-destructive">
      <AlertCircle className="h-3 w-3" />
      Failed
    </span>
  );
}
