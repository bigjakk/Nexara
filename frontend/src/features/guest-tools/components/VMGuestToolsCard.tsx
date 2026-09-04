import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { RefreshCw, Download, Play, X } from "lucide-react";
import { usePermissions } from "@/hooks/usePermissions";
import { useVirtioWinReleases } from "../api/virtio-win-queries";
import {
  useGuestToolsGuest,
  useDetectGuestTools,
  useStageGuestToolsUpdate,
  useCancelGuestToolsUpdate,
  useSetGuestToolsPolicy,
} from "../api/guest-tools-queries";
import {
  guestToolsStateLabel,
  guestToolsStagedMismatch,
  guestToolsStateVariant,
} from "../lib/guest-tools-state";

/** Sentinel for "follow the cluster's target" — Select cannot hold "". */
const FOLLOW_CLUSTER = "__cluster__";

interface VMGuestToolsCardProps {
  clusterId: string;
  /** Proxmox VMID, the stable identity per-guest state is keyed on. */
  vmid: number;
}

/**
 * Guest tools status and controls for a single VM: the content of the VM's
 * Guest Tools tab.
 *
 * The Overview tab carries only a one-line summary on the QEMU Guest Agent
 * panel; everything actionable lives here so Overview stays readable.
 *
 * Reads from the cluster fleet endpoint and picks out this guest, so there is
 * no second API surface to permission, document and keep in step — the list is
 * one row per Windows guest, and the target/up-to-date/needs-update judgements
 * are already computed server-side.
 *
 * Windows-only: the tab and this card are both gated on that by VMDetailPage,
 * which is the one place the guest's OS is classified. Nothing here re-checks.
 */
export function VMGuestToolsCard({ clusterId, vmid }: VMGuestToolsCardProps) {
  const { guest, isLoading } = useGuestToolsGuest(clusterId, vmid);
  const { data: releases } = useVirtioWinReleases();
  const detect = useDetectGuestTools(clusterId);
  const stage = useStageGuestToolsUpdate(clusterId);
  const cancel = useCancelGuestToolsUpdate(clusterId);
  const setPolicy = useSetGuestToolsPolicy(clusterId);
  const { canExecute, canManage } = usePermissions();
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);

  if (isLoading || !guest) {
    return (
      <div className="rounded-lg border p-4">
        <p className="text-sm text-muted-foreground">
          {isLoading
            ? "Checking..."
            : "No guest tools information for this guest yet."}
        </p>
      </div>
    );
  }

  const mayExecute = canExecute("guest_tools");
  const mayManage = canManage("guest_tools");
  const running = guest.status === "running";
  const inFlight =
    guest.stage === "staged" ||
    guest.stage === "running" ||
    guest.stage === "staging";

  const run = async (fn: () => Promise<unknown>, ok: string) => {
    setBusy(true);
    setNote("");
    try {
      await fn();
      setNote(ok);
    } catch (err) {
      setNote(err instanceof Error ? err.message : "Action failed.");
    } finally {
      setBusy(false);
    }
  };

  // Staging is deliberately not gated on the guest being behind: reinstalling
  // to repair a broken driver install, and installing on a guest that has none,
  // are both real needs. The backend has never required it either.
  const stageLabel = guest.needs_update
    ? "Stage update"
    : guest.installed_version
      ? "Reinstall"
      : "Install";

  // Non-null only while what is staged is not what the target names — the
  // window between an operator changing the target and the reconcile loop
  // withdrawing the staging it superseded.
  const stagedMismatch = guestToolsStagedMismatch(guest);

  return (
    <div className="rounded-lg border p-4">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-sm font-semibold">virtio-win guest tools</h3>
        <Badge variant={guestToolsStateVariant(guest)}>
          {guestToolsStateLabel(guest)}
        </Badge>
      </div>

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <div>
          <p className="text-xs text-muted-foreground">Installed</p>
          <p className="text-sm">{guest.installed_version || "--"}</p>
        </div>
        <div>
          <p className="text-xs text-muted-foreground">Target</p>
          <p className="text-sm">
            {guest.target_version || "--"}
            {guest.policy_target_version ? (
              <span className="ml-1 text-xs text-muted-foreground">
                (pinned)
              </span>
            ) : null}
          </p>
        </div>
        <div>
          <p className="text-xs text-muted-foreground">Guest agent</p>
          <p className="text-sm">
            {guest.agent_version || "--"}
            {guest.agent_running ? "" : " (not running)"}
          </p>
        </div>
        <div>
          <p className="text-xs text-muted-foreground">Last checked</p>
          <p className="text-sm">
            {guest.detected_at
              ? new Date(guest.detected_at).toLocaleString()
              : "Never"}
          </p>
        </div>
      </div>

      {stagedMismatch ? (
        <p
          role="status"
          className="mt-3 text-xs text-amber-600 dark:text-amber-400"
        >
          {stagedMismatch}
        </p>
      ) : null}

      {guest.last_error ? (
        <p
          className={
            guest.reboot_required
              ? "mt-3 text-xs text-muted-foreground"
              : "mt-3 text-xs text-destructive"
          }
        >
          {guest.last_error}
        </p>
      ) : null}

      <div className="mt-4 flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          variant="outline"
          disabled={busy || !running}
          title={
            running
              ? "Re-read the installed version from the guest"
              : "The guest must be running"
          }
          onClick={() => {
            void run(() => detect.mutateAsync(vmid), "Version re-read.");
          }}
        >
          <RefreshCw className="mr-2 h-3.5 w-3.5" />
          Re-check
        </Button>

        {inFlight ? (
          <Button
            size="sm"
            variant="outline"
            disabled={busy || !mayExecute}
            onClick={() => {
              void run(
                () => cancel.mutateAsync(vmid),
                "Staged update cancelled.",
              );
            }}
          >
            <X className="mr-2 h-3.5 w-3.5" />
            Cancel staged update
          </Button>
        ) : (
          <>
            <Button
              size="sm"
              disabled={busy || !mayExecute || guest.excluded || !running}
              title={`${stageLabel} at the guest's next boot`}
              onClick={() => {
                void run(
                  () => stage.mutateAsync({ vmid, body: { run_now: false } }),
                  "Staged for the next boot.",
                );
              }}
            >
              <Download className="mr-2 h-3.5 w-3.5" />
              {stageLabel} at next boot
            </Button>
            <Button
              size="sm"
              variant="outline"
              disabled={busy || !mayExecute || guest.excluded || !running}
              title="Install now, without waiting for a reboot. Replacing the network driver briefly drops the guest's network."
              onClick={() => {
                void run(
                  () => stage.mutateAsync({ vmid, body: { run_now: true } }),
                  "Update started in the guest.",
                );
              }}
            >
              <Play className="mr-2 h-3.5 w-3.5" />
              Update now
            </Button>
          </>
        )}
      </div>

      <div className="mt-4 space-y-3 border-t pt-3">
        <div className="flex items-start gap-2">
          <Checkbox
            id={`gt-exclude-${String(vmid)}`}
            checked={guest.excluded}
            disabled={busy || !mayManage}
            onCheckedChange={(v) => {
              void run(
                () =>
                  setPolicy.mutateAsync({
                    vmid,
                    policy: {
                      excluded: v === true,
                      target_version: guest.policy_target_version,
                      note: guest.note,
                    },
                  }),
                v === true ? "Guest excluded." : "Guest included.",
              );
            }}
          />
          <div className="space-y-1">
            <Label htmlFor={`gt-exclude-${String(vmid)}`}>
              Exclude from automatic updates
            </Label>
            <p className="text-xs text-muted-foreground">
              The guest stays visible in the fleet view; nothing is staged for
              it automatically.
            </p>
          </div>
        </div>

        <div className="space-y-1">
          <Label>Pinned version</Label>
          <Select
            value={
              guest.policy_target_version === ""
                ? FOLLOW_CLUSTER
                : guest.policy_target_version
            }
            disabled={busy || !mayManage}
            onValueChange={(v) => {
              void run(
                () =>
                  setPolicy.mutateAsync({
                    vmid,
                    policy: {
                      excluded: guest.excluded,
                      target_version: v === FOLLOW_CLUSTER ? "" : v,
                      note: guest.note,
                    },
                  }),
                "Pinned version saved.",
              );
            }}
          >
            <SelectTrigger className="max-w-sm">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={FOLLOW_CLUSTER}>
                Follow the cluster target
              </SelectItem>
              {(releases ?? []).map((r) => (
                <SelectItem key={r.version} value={r.version}>
                  {r.version}
                  {r.is_stable ? " (stable)" : ""}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <p className="text-xs text-muted-foreground">
            A pin here outranks the cluster policy for this guest only.
          </p>
        </div>
      </div>

      {note ? (
        <p className="mt-3 text-xs text-muted-foreground">{note}</p>
      ) : null}
    </div>
  );
}
