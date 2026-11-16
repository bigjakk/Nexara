import { useId, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Panel } from "@/components/page/Panel";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import { usePermissions } from "@/hooks/usePermissions";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { describeError } from "@/lib/api-error";
import { formatExactDateTime } from "@/lib/format";
import { useCreateUpsMaintenanceWindow, useDeleteUpsMaintenanceWindow, useUpsMaintenanceWindows } from "../api/ups-queries";
import type { UpsDevice } from "../types/ups";
import { useTranslation } from "react-i18next";

/** A datetime-local input's value for a moment, in local time. */
function localInput(ms: number): string {
  return new Date(ms - new Date(ms).getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
}

function windowState(w: { starts_at: string; ends_at: string }, now: number): "ended" | "open" | "upcoming" {
  if (Date.parse(w.ends_at) <= now) return "ended";
  return Date.parse(w.starts_at) <= now ? "open" : "upcoming";
}

/**
 * A UPS's maintenance windows: while one is open the UPS drops out of the
 * global UPS alert rules, and its own rules hold — for a battery swap, say.
 * Shown with global view:maintenance_window, changed with manage.
 */
export function UpsMaintenance({ device }: { device: UpsDevice }) {
  const { t } = useTranslation("power");
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
    <Panel
      title={t("upsMaintenance.maintenanceWindows")}
      actions={
        canManage ? (
          <Button
            size="sm"
            variant="outline"
            onClick={() => {
              setAdding(true);
            }}
          >
            {t("upsMaintenance.addWindow")}
          </Button>
        ) : undefined
      }
    >
      <p className="-mt-2 mb-3 max-w-[65ch] text-sm text-muted-foreground">
        {t("upsMaintenance.whileOneIsOpenName", { name: device.name })}
      </p>
      {list.length === 0 ? (
        // Its loading, failure and paused states too: a paused read has no data
        // and no error, and is not "no windows".
        <QueryStateNotice
          query={windows}
          subject={t("upsMaintenance.thisUPSSMaintenanceWindows")}
          empty={t("upsMaintenance.noMaintenanceWindows")}
          skeletonClassName="h-8 w-full"
        />
      ) : (
        <ul className="divide-y text-sm">
          {list.map((w) => {
            const state = windowState(w, now);
            return (
              <li key={w.id} className="flex flex-wrap items-center justify-between gap-2 py-2 first:pt-0 last:pb-0">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                    {formatExactDateTime(w.starts_at)} – {formatExactDateTime(w.ends_at)}
                    {state === "open" ? (
                      <Badge variant="secondary">{t("upsMaintenance.openNow")}</Badge>
                    ) : (
                      <span className="text-[12.5px] text-muted-foreground">
                        {state === "ended" ? t("upsMaintenance.ended") : t("upsMaintenance.upcoming")}
                      </span>
                    )}
                  </div>
                  {w.description !== "" && <div className="break-words text-muted-foreground">{w.description}</div>}
                </div>
                {canManage && (
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={t("upsMaintenance.removeTheWindowFromStartsAt", { startsAt: formatExactDateTime(w.starts_at) })}
                    disabled={remove.isPending}
                    onClick={() => {
                      remove.mutate(w.id);
                    }}
                  >
                    {t("upsMaintenance.remove")}
                  </Button>
                )}
              </li>
            );
          })}
        </ul>
      )}
      {adding && (
        <AddWindowDialog
          device={device}
          onClose={() => {
            setAdding(false);
          }}
        />
      )}
    </Panel>
  );
}

function AddWindowDialog({ device, onClose }: { device: UpsDevice; onClose: () => void }) {
  const { t } = useTranslation("power");
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
      { action: t("upsMaintenance.addingAMaintenanceWindowOn", { name: device.name }), onSuccess: onClose, onError: () => undefined },
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
          <DialogTitle>{t("upsMaintenance.maintenanceWindowOnName", { name: device.name })}</DialogTitle>
          <DialogDescription>
            {t("upsMaintenance.whileItIsOpenName", { name: device.name })}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor={`${id}-start`}>{t("upsMaintenance.starts")}</Label>
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
            <Label htmlFor={`${id}-end`}>{t("upsMaintenance.ends")}</Label>
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
            <Label htmlFor={`${id}-why`}>{t("upsMaintenance.whyOptional")}</Label>
            <Input
              id={`${id}-why`}
              maxLength={1024}
              value={description}
              onChange={(e) => {
                setDescription(e.target.value);
              }}
            />
          </div>
          {!valid && <p className="text-sm text-destructive">{t("upsMaintenance.theWindowMustEndAfter")}</p>}
          {create.isError && <p className="text-sm text-destructive">{describeError(create.error)}</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t("upsMaintenance.cancel")}
          </Button>
          <Button disabled={!valid || create.isPending} onClick={save}>
            {create.isPending ? t("upsMaintenance.adding") : t("upsMaintenance.addWindow")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
