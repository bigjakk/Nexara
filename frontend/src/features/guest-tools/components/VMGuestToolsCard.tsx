import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import { Panel, PropertyList, PropertyRow } from "@/components/page/Panel";
import { SettingRow, SettingsSection } from "@/components/page/Settings";
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
  guestToolsInFlight,
  guestToolsPolicy,
  guestToolsStagedMismatch,
  guestToolsStageAction,
  guestToolsState,
} from "../lib/guest-tools-state";
import { DateTime } from "@/components/DateTime";

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
      <Panel title="Guest tools">
        <p className="text-sm text-muted-foreground">
          {isLoading
            ? "Checking..."
            : "No guest tools information for this guest yet."}
        </p>
      </Panel>
    );
  }

  const mayExecute = canExecute("guest_tools");
  const mayManage = canManage("guest_tools");
  const running = guest.status === "running";
  const inFlight = guestToolsInFlight(guest);
  const { label, variant, errorTone } = guestToolsState(guest);

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

  const stageLabel = {
    stage: "Stage update",
    reinstall: "Reinstall",
    install: "Install",
  }[guestToolsStageAction(guest)];

  // Non-null only while what is staged is not what the target names — the
  // window between an operator changing the target and the reconcile loop
  // withdrawing the staging it superseded.
  const stagedMismatch = guestToolsStagedMismatch(guest);

  const excludeId = `gt-exclude-${String(vmid)}`;
  const pinId = `gt-pin-${String(vmid)}`;

  return (
    // What is installed and what to do about it on the left, this guest's
    // standing policy on the right; the two fold into one column.
    <div className="flex flex-wrap items-start gap-4">
      <Panel
        className="flex-[1_1_380px]"
        title="Guest tools"
        aside="virtio-win drivers and the QEMU guest agent"
        actions={<Badge variant={variant}>{label}</Badge>}
      >
        <PropertyList>
          <PropertyRow label="Installed">
            <span className="font-mono text-[12.5px]">
              {guest.installed_version || "--"}
            </span>
          </PropertyRow>
          <PropertyRow label="Target">
            <span className="font-mono text-[12.5px]">
              {guest.target_version || "--"}
            </span>
            {guest.policy_target_version ? (
              <span className="ml-1 text-[12.5px] text-muted-foreground">
                (pinned)
              </span>
            ) : null}
          </PropertyRow>
          <PropertyRow label="Guest agent">
            <span className="font-mono text-[12.5px]">
              {guest.agent_version || "--"}
            </span>
            {guest.agent_running ? "" : " (not running)"}
          </PropertyRow>
          <PropertyRow label="Last checked">
            <DateTime value={guest.detected_at} fallback="Never" />
          </PropertyRow>
        </PropertyList>

        {stagedMismatch ? (
          <p
            role="status"
            className="mt-3 text-[12.5px] text-amber-700 dark:text-amber-400"
          >
            {stagedMismatch}
          </p>
        ) : null}

        {guest.last_error ? (
          <p className={`mt-3 text-[12.5px] ${errorTone}`}>
            {guest.last_error}
          </p>
        ) : null}

        <div className="mt-4 flex flex-wrap items-center gap-2 border-t pt-4">
          {inFlight ? (
            <Button
              size="sm"
              variant="outline"
              className="gap-1.5"
              disabled={busy || !mayExecute}
              onClick={() => {
                void run(
                  () => cancel.mutateAsync(vmid),
                  "Staged update cancelled.",
                );
              }}
            >
              <X className="h-3.5 w-3.5" />
              Cancel staged update
            </Button>
          ) : (
            <>
              <Button
                size="sm"
                className="gap-1.5"
                disabled={busy || !mayExecute || guest.excluded || !running}
                title={`${stageLabel} at the guest's next boot`}
                onClick={() => {
                  void run(
                    () => stage.mutateAsync({ vmid, body: { run_now: false } }),
                    "Staged for the next boot.",
                  );
                }}
              >
                <Download className="h-3.5 w-3.5" />
                {stageLabel} at next boot
              </Button>
              <Button
                size="sm"
                variant="outline"
                className="gap-1.5"
                disabled={busy || !mayExecute || guest.excluded || !running}
                title="Install now, without waiting for a reboot. Replacing the network driver briefly drops the guest's network."
                onClick={() => {
                  void run(
                    () => stage.mutateAsync({ vmid, body: { run_now: true } }),
                    "Update started in the guest.",
                  );
                }}
              >
                <Play className="h-3.5 w-3.5" />
                Update now
              </Button>
            </>
          )}
          <Button
            size="sm"
            variant="ghost"
            className="gap-1.5"
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
            <RefreshCw className="h-3.5 w-3.5" />
            Re-check
          </Button>
        </div>

        {note ? (
          <p className="mt-3 text-[12.5px] text-muted-foreground">{note}</p>
        ) : null}
      </Panel>

      <SettingsSection
        className="flex-[1_1_380px]"
        title="Policy for this guest"
        description="The cluster's guest tools policy decides when updates are staged. These two settings change that for this guest alone."
      >
        <SettingRow
          label="Exclude from automatic updates"
          description="The guest stays visible in the fleet view; nothing is staged for it automatically."
          htmlFor={excludeId}
          className="sm:grid-cols-[minmax(0,1fr)_auto]"
        >
          <Checkbox
            id={excludeId}
            checked={guest.excluded}
            disabled={busy || !mayManage}
            onCheckedChange={(v) => {
              void run(
                () =>
                  setPolicy.mutateAsync({
                    vmid,
                    policy: guestToolsPolicy(guest, { excluded: v === true }),
                  }),
                v === true ? "Guest excluded." : "Guest included.",
              );
            }}
          />
        </SettingRow>

        <SettingRow
          label="Pinned version"
          description="A pin here outranks the cluster policy for this guest only."
          htmlFor={pinId}
          className="sm:grid-cols-1"
        >
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
                    policy: guestToolsPolicy(guest, {
                      target_version: v === FOLLOW_CLUSTER ? "" : v,
                    }),
                  }),
                "Pinned version saved.",
              );
            }}
          >
            <SelectTrigger id={pinId}>
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
        </SettingRow>
      </SettingsSection>
    </div>
  );
}
