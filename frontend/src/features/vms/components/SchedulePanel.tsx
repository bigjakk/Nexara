import { useCallback, useState } from "react";
import { Trash2, Plus, AlertCircle, Loader2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Panel } from "@/components/page/Panel";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
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
import {
  scheduledSnapshotNamePattern,
  snapshotPrefixError,
  SNAPSHOT_NAME_PREFIX_MAX_LENGTH,
  SNAPSHOT_PREFIX_RULES,
} from "../lib/snapshot-name";
import { scheduleRunState } from "../lib/schedule-status";
import type { ResourceKind } from "../types/vm";
import { DateTime } from "@/components/DateTime";

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
  // The rows this panel shows: this guest's, out of the cluster's list. The
  // same test decides what the list polls for (useScheduledTasks), so another
  // guest's run never keeps this tab re-reading.
  const shown = useCallback(
    (s: ScheduledTask) =>
      s.resource_id === String(vmid) &&
      s.resource_type === (kind === "ct" ? "ct" : "vm"),
    [vmid, kind],
  );
  const { data: schedules, isLoading } = useScheduledTasks(clusterId, shown);
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
    action === "snapshot" ? snapshotPrefixError(snapName, kind) : null;

  const mySchedules = schedules?.filter(shown);

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

  const guestWord = kind === "ct" ? "container" : "VM";
  const count = mySchedules?.length ?? 0;
  const paused = mySchedules?.filter((s) => !s.enabled).length ?? 0;

  return (
    <div className="space-y-4">
      <Panel
        flush
        title="Schedules"
        aside={
          isLoading
            ? "Loading..."
            : count === 0
              ? undefined
              : `${String(count)} for this ${guestWord}${paused > 0 ? `, ${String(paused)} disabled` : ""}`
        }
        actions={
          <Button
            size="sm"
            className="gap-1.5"
            onClick={() => {
              setDialogOpen(true);
            }}
          >
            <Plus className="h-4 w-4" />
            Add schedule
          </Button>
        }
      >
        {isLoading ? (
          <p className="px-[18px] py-6 text-sm text-muted-foreground">
            Reading the cluster&apos;s schedules.
          </p>
        ) : !mySchedules?.length ? (
          <p className="px-[18px] py-6 text-sm text-muted-foreground">
            No scheduled tasks for this {guestWord}. A schedule takes a snapshot
            or reboots it on a cron expression.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Action</TableHead>
                <TableHead>Schedule</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Next run</TableHead>
                <TableHead>Last run</TableHead>
                <TableHead className="w-12">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {mySchedules.map((s) => (
                <TableRow key={s.id}>
                  <TableCell className="font-medium capitalize">
                    {s.action}
                  </TableCell>
                  <TableCell className="font-mono text-[12.5px]">
                    {s.schedule}
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-col gap-1">
                      <div className="flex items-center gap-2">
                        <RunState status={s.last_status} />
                        {/* The scheduler disables a task whose cron can never
                            fire. Without this the row just stops, with a failed
                            icon and no next run, and nothing saying why. */}
                        {!s.enabled && (
                          <Badge variant="secondary">Disabled</Badge>
                        )}
                      </div>
                      {s.last_error ? (
                        <span
                          className="max-w-[22rem] truncate text-[12.5px] text-destructive"
                          title={s.last_error}
                        >
                          {s.last_error}
                        </span>
                      ) : null}
                    </div>
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-[12.5px] tabular-nums text-muted-foreground">
                    <DateTime value={s.next_run_at} fallback="--" />
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-[12.5px] tabular-nums text-muted-foreground">
                    <DateTime value={s.last_run_at} fallback="--" />
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      aria-label={`Delete ${s.action} schedule ${s.schedule}`}
                      variant="ghost"
                      size="sm"
                      className="h-7 w-7 p-0 text-muted-foreground hover:text-destructive"
                      onClick={() => {
                        setPendingDelete(s);
                      }}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Panel>

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
            <DialogTitle>Add a schedule</DialogTitle>
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
              <Label>Cron expression</Label>
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
                  Snapshot name prefix (optional)
                </Label>
                <Input
                  id="schedule-snap-name"
                  value={snapName}
                  onChange={(e) => {
                    setSnapName(e.target.value);
                  }}
                  placeholder="Optional"
                  maxLength={SNAPSHOT_NAME_PREFIX_MAX_LENGTH}
                />
                {/* A prefix, not a name: the scheduler adds each run's date and
                    time in UTC to it, and uses "auto" when it is empty
                    (internal/scheduler, scheduledSnapshotName). It used to send
                    this verbatim, and a guest holds each snapshot name once, so
                    every run after the first failed. The line below spells out
                    the name a run takes. The placeholder stays free of any
                    YYYYMMDD pattern: typed in, it would be kept literally as
                    part of the prefix, not filled in. */}
                <p
                  className={
                    snapNameError
                      ? "mt-1 text-xs text-destructive"
                      : "mt-1 text-xs text-muted-foreground"
                  }
                >
                  {snapNameError ??
                    `Each run takes a new snapshot named ${scheduledSnapshotNamePattern(snapName)}, from the run's date and time in UTC; earlier ones are kept.${snapName.length === 0 ? "" : ` ${SNAPSHOT_PREFIX_RULES}`}`}
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

// Success and Failed describe how the last run's Proxmox task ENDED. The
// scheduler records a run it has handed to Proxmox as "dispatched" and settles
// it once the task finishes; until then — and while the scheduler is still
// starting the run, "running" — the row shows Running. It used to show Failed
// for "running", and Success as soon as Proxmox accepted the call, however the
// task then went.
function RunState({ status }: { status: string | null }) {
  switch (scheduleRunState(status)) {
    case "pending":
      return <span className="text-muted-foreground">Pending</span>;
    case "running":
      return (
        <span className="flex items-center gap-1.5">
          <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" />
          Running
        </span>
      );
    case "success":
      // Healthy is quiet: a dot and plain text.
      return (
        <span className="flex items-center gap-[7px]">
          <span
            aria-hidden="true"
            className="h-[7px] w-[7px] rounded-full bg-emerald-500"
          />
          Success
        </span>
      );
    case "failed":
      return (
        <span className="flex items-center gap-1.5 text-red-700 dark:text-red-400">
          <AlertCircle className="h-3.5 w-3.5" />
          Failed
        </span>
      );
  }
}
