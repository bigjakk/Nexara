import { useId, useState } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import { usePermissions } from "@/hooks/usePermissions";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { describeError } from "@/lib/api-error";
import { formatDateTime } from "@/lib/format";
import { useCreateUpsMaintenanceWindow, useDeleteUpsMaintenanceWindow, useUpsMaintenanceWindows } from "../api/ups-queries";
import type { UpsDevice } from "../types/ups";

/** A datetime-local input's value for a moment, in local time. */
function localInput(ms: number): string {
  return new Date(ms - new Date(ms).getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
}

function windowState(w: { starts_at: string; ends_at: string }, now: number): string {
  if (Date.parse(w.ends_at) <= now) return "Ended";
  return Date.parse(w.starts_at) <= now ? "Open now" : "Upcoming";
}

/**
 * A UPS's maintenance windows: while one is open the UPS drops out of the
 * global UPS alert rules, and its own rules hold — for a battery swap, say.
 * Shown with global view:maintenance_window, changed with manage.
 */
export function UpsMaintenance({ device }: { device: UpsDevice }) {
  const { hasGlobalPermission } = usePermissions();
  const canView = hasGlobalPermission("view", "maintenance_window");
  const canManage = hasGlobalPermission("manage", "maintenance_window");
  const windows = useUpsMaintenanceWindows(device.id, canView);
  const remove = useDeleteUpsMaintenanceWindow(device.id);
  const [adding, setAdding] = useState(false);
  if (!canView) return null;
  const now = Date.now();
  const list = windows.data ?? [];
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between gap-2 space-y-0">
        <CardTitle className="text-base">Maintenance windows</CardTitle>
        {canManage && (
          <Button
            size="sm"
            variant="outline"
            onClick={() => {
              setAdding(true);
            }}
          >
            Add window
          </Button>
        )}
      </CardHeader>
      <CardContent className="space-y-2 text-sm">
        <p className="text-muted-foreground">
          While one is open, {device.name} drops out of the global UPS alert rules, and its own rules hold.
        </p>
        {list.length === 0 ? (
          // Its loading, failure and paused states too: a paused read has no data
          // and no error, and is not "no windows".
          <QueryStateNotice
            query={windows}
            subject="this UPS's maintenance windows"
            empty="No maintenance windows."
            skeletonClassName="h-8 w-full"
          />
        ) : (
          <ul className="divide-y">
            {list.map((w) => (
              <li key={w.id} className="flex flex-wrap items-center justify-between gap-2 py-2">
                <div className="min-w-0">
                  <div>
                    {formatDateTime(w.starts_at)} – {formatDateTime(w.ends_at)}{" "}
                    <span className="text-muted-foreground">· {windowState(w, now)}</span>
                  </div>
                  {w.description !== "" && <div className="break-words text-muted-foreground">{w.description}</div>}
                </div>
                {canManage && (
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={`Remove the window from ${formatDateTime(w.starts_at)}`}
                    disabled={remove.isPending}
                    onClick={() => {
                      remove.mutate(w.id);
                    }}
                  >
                    Remove
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
      </CardContent>
      {adding && (
        <AddWindowDialog
          device={device}
          onClose={() => {
            setAdding(false);
          }}
        />
      )}
    </Card>
  );
}

function AddWindowDialog({ device, onClose }: { device: UpsDevice; onClose: () => void }) {
  const id = useId();
  const [start, setStart] = useState(() => localInput(Date.now()));
  const [end, setEnd] = useState(() => localInput(Date.now() + 60 * 60 * 1000));
  const [description, setDescription] = useState("");
  const create = useCreateUpsMaintenanceWindow(device.id);
  const settle = useSaveOutcome(true);
  // A datetime-local value names no offset, so it parses as local time.
  const startMs = Date.parse(start);
  const endMs = Date.parse(end);
  const valid = Number.isFinite(startMs) && Number.isFinite(endMs) && endMs > startMs;

  const save = () => {
    settle(
      create.mutateAsync({
        starts_at: new Date(startMs).toISOString(),
        ends_at: new Date(endMs).toISOString(),
        ...(description === "" ? {} : { description }),
      }),
      { action: `Adding a maintenance window on ${device.name}`, onSuccess: onClose, onError: () => undefined },
    );
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Maintenance window on {device.name}</DialogTitle>
          <DialogDescription>
            While it is open, {device.name} drops out of the global UPS alert rules, and its own rules hold.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor={`${id}-start`}>Starts</Label>
            <Input
              id={`${id}-start`}
              type="datetime-local"
              value={start}
              onChange={(e) => {
                setStart(e.target.value);
              }}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor={`${id}-end`}>Ends</Label>
            <Input
              id={`${id}-end`}
              type="datetime-local"
              value={end}
              onChange={(e) => {
                setEnd(e.target.value);
              }}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor={`${id}-why`}>Why (optional)</Label>
            <Input
              id={`${id}-why`}
              maxLength={1024}
              value={description}
              onChange={(e) => {
                setDescription(e.target.value);
              }}
            />
          </div>
          {!valid && <p className="text-sm text-destructive">The window must end after it starts.</p>}
          {create.isError && <p className="text-sm text-destructive">{describeError(create.error)}</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={!valid || create.isPending} onClick={save}>
            {create.isPending ? "Adding…" : "Add window"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
