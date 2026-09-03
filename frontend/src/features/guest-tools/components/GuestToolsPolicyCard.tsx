import { useState, useEffect } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Wrench, AlertTriangle } from "lucide-react";
import { usePermissions } from "@/hooks/usePermissions";
import { useVirtioWinReleases } from "../api/virtio-win-queries";
import {
  useGuestToolsConfig,
  useUpdateGuestToolsConfig,
} from "../api/guest-tools-queries";
import type {
  GuestToolsConfigRequest,
  GuestToolsMode,
} from "../types/guest-tools";

/** Sentinel for "follow the cluster's virtio-win target" — Select can't hold "". */
const FOLLOW_ISO_TARGET = "__iso_target__";

interface GuestToolsPolicyCardProps {
  clusterId: string;
}

export function GuestToolsPolicyCard({ clusterId }: GuestToolsPolicyCardProps) {
  const { data: config, isLoading } = useGuestToolsConfig(clusterId);
  const { data: releases } = useVirtioWinReleases();
  const updateConfig = useUpdateGuestToolsConfig(clusterId);
  const { canManage } = usePermissions();
  const readOnly = !canManage("guest_tools");

  const [mode, setMode] = useState<GuestToolsMode>("disabled");
  const [targetVersion, setTargetVersion] = useState("");
  const [snapshotBefore, setSnapshotBefore] = useState(false);
  const [maxConcurrent, setMaxConcurrent] = useState(5);
  const [saveStatus, setSaveStatus] = useState<"idle" | "saved" | "error">(
    "idle",
  );

  useEffect(() => {
    if (config) {
      setMode(config.mode);
      setTargetVersion(config.target_version);
      setSnapshotBefore(config.snapshot_before);
      setMaxConcurrent(config.max_concurrent);
    }
  }, [config]);

  if (isLoading) {
    return <Skeleton className="h-96 w-full" />;
  }

  const handleSave = () => {
    setSaveStatus("idle");
    const request: GuestToolsConfigRequest = {
      mode,
      target_version: targetVersion,
      max_concurrent: maxConcurrent,
      snapshot_before: snapshotBefore,
    };
    updateConfig.mutate(request, {
      onSuccess: () => {
        setSaveStatus("saved");
        setTimeout(() => {
          setSaveStatus("idle");
        }, 3000);
      },
      onError: () => {
        setSaveStatus("error");
      },
    });
  };

  const noStorage = mode !== "disabled" && !config?.iso_storage;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Wrench className="h-5 w-5" />
          Guest tools updates
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-6">
        <p className="text-sm text-muted-foreground">
          Tracks the virtio drivers and QEMU guest agent installed in Windows
          guests. Updates are staged and installed at the guest&apos;s next
          reboot &mdash; Nexara never reboots a guest itself.
        </p>

        {noStorage && (
          <div className="flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 p-3 dark:border-amber-700 dark:bg-amber-950">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
            <div className="space-y-1 text-xs text-amber-700 dark:text-amber-300">
              <p className="font-medium">
                No virtio-win ISO storage configured
              </p>
              <p>
                Set a target storage under <strong>virtio-win ISO</strong>{" "}
                above. Detection still works, but nothing can be staged without
                the ISO.
              </p>
            </div>
          </div>
        )}

        <div className="space-y-2">
          <Label>Mode</Label>
          <Select
            value={mode}
            disabled={readOnly}
            onValueChange={(v) => {
              setMode(v as GuestToolsMode);
            }}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="disabled">Disabled</SelectItem>
              <SelectItem value="report">
                Report only &mdash; detect versions, change nothing
              </SelectItem>
              <SelectItem value="staged">
                Staged &mdash; install at each guest&apos;s next boot
              </SelectItem>
            </SelectContent>
          </Select>
          <p className="text-xs text-muted-foreground">
            {mode === "report"
              ? "Reads the installed version from each running Windows guest. Nothing is written to any guest."
              : mode === "staged"
                ? "Guests that are behind get the installer staged to run at their next reboot."
                : "Nothing is detected or staged."}
          </p>
        </div>

        <div className="space-y-2">
          <Label>Target version</Label>
          <Select
            value={targetVersion === "" ? FOLLOW_ISO_TARGET : targetVersion}
            disabled={readOnly}
            onValueChange={(v) => {
              setTargetVersion(v === FOLLOW_ISO_TARGET ? "" : v);
            }}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={FOLLOW_ISO_TARGET}>
                Follow the virtio-win ISO target
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
            {config?.effective_version
              ? `Guests without their own pin resolve to ${config.effective_version}.`
              : "No target could be resolved yet."}
          </p>
        </div>

        <div className="flex items-start gap-2">
          <Checkbox
            id="guest-tools-snapshot"
            checked={snapshotBefore}
            disabled={readOnly}
            onCheckedChange={(v) => {
              setSnapshotBefore(v === true);
            }}
          />
          <div className="space-y-1">
            <Label htmlFor="guest-tools-snapshot">
              Snapshot before an immediate update
            </Label>
            <p className="text-xs text-muted-foreground">
              The full bundle replaces storage and network drivers, and a bad
              one can leave a guest unbootable. Applies to{" "}
              <strong>Update now</strong> only: a snapshot taken when an update
              is staged would be stale by the time the guest actually reboots.
            </p>
          </div>
        </div>

        <div className="space-y-2">
          <Label htmlFor="guest-tools-concurrency">
            Maximum guests updating at once
          </Label>
          <Input
            id="guest-tools-concurrency"
            type="number"
            min={1}
            max={100}
            className="w-32"
            value={maxConcurrent}
            disabled={readOnly}
            onChange={(e) => {
              setMaxConcurrent(Number(e.target.value));
            }}
          />
          <p className="text-xs text-muted-foreground">
            Swapping boot-disk drivers across a whole fleet at once turns one
            bad release into an outage.
          </p>
        </div>

        <div className="flex items-center gap-3">
          <Button
            onClick={handleSave}
            disabled={readOnly || updateConfig.isPending}
          >
            {updateConfig.isPending ? "Saving..." : "Save"}
          </Button>
          {saveStatus === "saved" && (
            <span className="text-sm text-muted-foreground">Saved</span>
          )}
          {saveStatus === "error" && (
            <span className="text-sm text-destructive">
              Save failed. Check your permissions and try again.
            </span>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
