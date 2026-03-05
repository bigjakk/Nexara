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
} from "../api/vm-queries";
import type { ResourceKind } from "../types/vm";

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

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
        <Button size="sm" className="gap-2" onClick={() => { setDialogOpen(true); }}>
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
                    <StatusIcon status={s.last_status} />
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
                      variant="ghost"
                      size="sm"
                      onClick={() => { handleDelete(s.id); }}
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
                onChange={(e) => { setAction(e.target.value); }}
              >
                <option value="snapshot">Snapshot</option>
                <option value="reboot">Reboot</option>
              </select>
            </div>
            <div>
              <Label>Cron Expression</Label>
              <Input
                value={cronExpr}
                onChange={(e) => { setCronExpr(e.target.value); }}
                placeholder="0 2 * * *"
              />
              <p className="mt-1 text-xs text-muted-foreground">
                Format: minute hour day month weekday (e.g. &quot;0 2 * * *&quot; = daily at 2 AM)
              </p>
            </div>
            {action === "snapshot" && (
              <div>
                <Label>Snapshot Name Template (optional)</Label>
                <Input
                  value={snapName}
                  onChange={(e) => { setSnapName(e.target.value); }}
                  placeholder="auto-YYYYMMDD-HHMMSS"
                />
              </div>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => { setDialogOpen(false); }}>
              Cancel
            </Button>
            <Button
              onClick={handleCreate}
              disabled={createSchedule.isPending || !cronExpr}
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
      <span className="flex items-center gap-1 text-xs text-green-600">
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
