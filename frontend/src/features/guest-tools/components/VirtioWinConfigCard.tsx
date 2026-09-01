import { useState, useEffect } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Disc3, AlertTriangle, Download, RefreshCw } from "lucide-react";
import { formatBytes } from "@/lib/format";
import { useClusterStorage } from "@/features/storage/api/storage-queries";
import {
  useVirtioWinConfig,
  useUpdateVirtioWinConfig,
  useVirtioWinReleases,
  useDownloadVirtioWin,
  useCheckVirtioWinNow,
} from "../api/virtio-win-queries";
import { isAlreadyPresent, isAlreadyRunning } from "../types/virtio-win";
import type { VirtioWinConfigRequest } from "../types/virtio-win";
import {
  buildSchedule,
  localTimezone,
  parseSchedule,
  type ParsedSchedule,
} from "../lib/virtio-win-schedule";
import { VirtioWinScheduleFields } from "./VirtioWinScheduleFields";
import { usePermissions } from "@/hooks/usePermissions";

/** Sentinel for the "follow upstream stable" choice — Select cannot hold "". */
const FOLLOW_STABLE = "__stable__";

interface VirtioWinConfigCardProps {
  clusterId: string;
}

export function VirtioWinConfigCard({ clusterId }: VirtioWinConfigCardProps) {
  const { data: config, isLoading } = useVirtioWinConfig(clusterId);
  const { data: releases } = useVirtioWinReleases();
  const { data: storages } = useClusterStorage(clusterId);
  const updateConfig = useUpdateVirtioWinConfig(clusterId);
  const download = useDownloadVirtioWin(clusterId);
  const checkNow = useCheckVirtioWinNow(clusterId);
  const { canManage } = usePermissions();
  const readOnly = !canManage("storage");

  const [enabled, setEnabled] = useState(false);
  const [storage, setStorage] = useState("");
  // Not editable here: which node performs the download is resolved server-side
  // to one that actually carries the storage, which is the right answer in every
  // case this form can express. It is still round-tripped so a node pinned
  // through the API survives a save from this card.
  const [node, setNode] = useState("");
  const [targetVersion, setTargetVersion] = useState("");
  const [pruneEnabled, setPruneEnabled] = useState(false);
  const [schedule, setSchedule] = useState<ParsedSchedule>(() =>
    parseSchedule(""),
  );
  const [timezone, setTimezone] = useState("");
  const [saveStatus, setSaveStatus] = useState<"idle" | "saved" | "error">(
    "idle",
  );
  const [downloadNote, setDownloadNote] = useState("");

  // Seeded from the fields the form OWNS, not from the config object.
  //
  // Depending on `config` would re-seed on any refetch, and this card now has
  // two buttons that change the config without touching a single form field:
  // Check now writes last_check_at/next_check_at, and saving the download
  // source below changes source_url. Either would reset a half-made schedule
  // edit out from under the operator — and re-saving afterwards would quietly
  // write back the OLD schedule. A genuine external change to a field the form
  // shows still re-seeds, because that field is in the dependency list.
  const {
    enabled: cfgEnabled,
    storage: cfgStorage,
    node: cfgNode,
    target_version: cfgTargetVersion,
    prune_enabled: cfgPruneEnabled,
    check_schedule: cfgCheckSchedule,
    check_timezone: cfgCheckTimezone,
  } = config ?? {};
  useEffect(() => {
    if (cfgEnabled === undefined) return; // no config loaded yet
    setEnabled(cfgEnabled);
    setStorage(cfgStorage ?? "");
    setNode(cfgNode ?? "");
    setTargetVersion(cfgTargetVersion ?? "");
    setPruneEnabled(cfgPruneEnabled ?? false);
    setSchedule(parseSchedule(cfgCheckSchedule ?? ""));
    // Prefill the viewer's own zone only when nothing is configured at all.
    // Once a schedule exists, an empty zone is a deliberate "server time"
    // that must survive being opened in another browser.
    setTimezone(
      cfgCheckTimezone === "" && cfgCheckSchedule === ""
        ? localTimezone()
        : (cfgCheckTimezone ?? ""),
    );
  }, [
    cfgEnabled,
    cfgStorage,
    cfgNode,
    cfgTargetVersion,
    cfgPruneEnabled,
    cfgCheckSchedule,
    cfgCheckTimezone,
  ]);

  if (isLoading) {
    return <Skeleton className="h-96 w-full" />;
  }

  // Only pools that actually accept ISOs can be a target. Offering the rest
  // would produce a save that succeeds and a download that always fails.
  //
  // Deduped by name: storage_pools holds one row per (cluster, node, storage),
  // so a non-shared pool like `local` arrives once per node and would otherwise
  // render as three identical options on a three-node cluster.
  const isoStorages = Array.from(
    new Map(
      (storages ?? [])
        .filter((s) => s.content.split(",").includes("iso"))
        .map((s) => [s.storage, s]),
    ).values(),
  );
  const effective =
    config?.effective_version ??
    releases?.find((r) => r.is_stable)?.version ??
    "";
  const effectiveRelease = releases?.find((r) => r.version === effective);

  const handleSave = () => {
    setSaveStatus("idle");
    const cron = buildSchedule(schedule);
    const request: VirtioWinConfigRequest = {
      enabled,
      storage,
      node,
      target_version: targetVersion,
      prune_enabled: pruneEnabled,
      check_schedule: cron,
      // Meaningless without a schedule, and sending it anyway would record a
      // zone against a cluster that is on the six-hourly interval.
      check_timezone: cron === "" ? "" : timezone,
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

  const handleDownloadNow = () => {
    setDownloadNote("");
    download.mutate(
      targetVersion ? { version: targetVersion } : {},
      {
        onSuccess: (result) => {
          if (isAlreadyPresent(result)) {
            setDownloadNote(
              `virtio-win ${result.version} is already on ${storage}.`,
            );
          } else if (isAlreadyRunning(result)) {
            setDownloadNote(
              `A download of virtio-win ${result.version} is already in progress.`,
            );
          } else {
            setDownloadNote(
              `Download of virtio-win ${result.version} started on ${result.node}.`,
            );
          }
        },
        onError: (err: unknown) => {
          setDownloadNote(
            err instanceof Error ? err.message : "Download failed.",
          );
        },
      },
    );
  };

  const handleCheckNow = () => {
    setDownloadNote("");
    checkNow.mutate(undefined, {
      onSuccess: (result) => {
        if (result.download) {
          setDownloadNote(
            `Checked. virtio-win ${result.download.version} was missing — download started on ${result.download.node}.`,
          );
        } else if (result.config.last_error !== "") {
          setDownloadNote(`Check failed: ${result.config.last_error}`);
        } else {
          setDownloadNote("Checked. Storage is already up to date.");
        }
      },
      onError: (err: unknown) => {
        setDownloadNote(err instanceof Error ? err.message : "Check failed.");
      },
    });
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Disc3 className="h-5 w-5" />
          virtio-win ISO
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-6">
        <p className="text-sm text-muted-foreground">
          Keeps a virtio-win ISO on this cluster&apos;s storage so Windows guests
          have drivers and the QEMU guest agent available. The Proxmox node
          fetches it directly &mdash; roughly 840&nbsp;MB per release.
        </p>

        {config?.last_error ? (
          <div className="flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 p-3 dark:border-amber-700 dark:bg-amber-950">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
            <div className="space-y-1 text-xs text-amber-700 dark:text-amber-300">
              <p className="font-medium">Last check failed</p>
              <p className="break-words">{config.last_error}</p>
            </div>
          </div>
        ) : null}

        <div className="flex items-start gap-2">
          <Checkbox
            id="virtio-win-enabled"
            checked={enabled}
            disabled={readOnly}
            onCheckedChange={(v) => {
              setEnabled(v === true);
            }}
          />
          <div className="space-y-1">
            <Label htmlFor="virtio-win-enabled">Download automatically</Label>
            <p className="text-xs text-muted-foreground">
              Checks on the schedule below and fetches the target version when
              it is missing.
            </p>
          </div>
        </div>

        <VirtioWinScheduleFields
          schedule={schedule}
          timezone={timezone}
          disabled={readOnly}
          onScheduleChange={setSchedule}
          onTimezoneChange={setTimezone}
        />

        <div className="space-y-2">
          <Label htmlFor="virtio-win-storage">Target storage</Label>
          <Select
            value={storage}
            disabled={readOnly}
            onValueChange={(v) => {
              setStorage(v);
            }}
          >
            <SelectTrigger id="virtio-win-storage">
              <SelectValue placeholder="Select an ISO-capable storage" />
            </SelectTrigger>
            <SelectContent>
              {isoStorages.map((s) => (
                <SelectItem key={s.id} value={s.storage}>
                  {s.storage} ({s.type}
                  {s.shared ? ", shared" : ""})
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {isoStorages.length === 0 && (
            <p className="text-xs text-muted-foreground">
              No storage on this cluster accepts ISO content.
            </p>
          )}
        </div>

        <div className="space-y-2">
          <Label htmlFor="virtio-win-version">Version</Label>
          <Select
            value={targetVersion === "" ? FOLLOW_STABLE : targetVersion}
            disabled={readOnly}
            onValueChange={(v) => {
              setTargetVersion(v === FOLLOW_STABLE ? "" : v);
            }}
          >
            <SelectTrigger id="virtio-win-version">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={FOLLOW_STABLE}>
                Follow upstream stable
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
            {targetVersion
              ? "Pinned. New upstream releases will not be downloaded until you change this."
              : "Follows whatever upstream marks stable."}
            {effective ? ` Currently resolves to ${effective}` : ""}
            {effectiveRelease && effectiveRelease.iso_size > 0
              ? ` (${formatBytes(effectiveRelease.iso_size)})`
              : ""}
            .
          </p>
        </div>

        <div className="flex items-start gap-2">
          <Checkbox
            id="virtio-win-prune"
            checked={pruneEnabled}
            disabled={readOnly}
            onCheckedChange={(v) => {
              setPruneEnabled(v === true);
            }}
          />
          <div className="space-y-1">
            <Label htmlFor="virtio-win-prune">Remove superseded ISOs</Label>
            <p className="text-xs text-muted-foreground">
              After a successful download, deletes virtio-win ISOs that are
              neither pinned by a cluster nor the newest. Only files named
              <code className="mx-1 rounded bg-muted px-1">
                virtio-win-&lt;version&gt;.iso
              </code>
              are ever touched.
            </p>
          </div>
        </div>

        <div className="space-y-1 border-t pt-4 text-xs text-muted-foreground">
          <p>
            <span className="font-medium text-foreground">Last checked</span>{" "}
            {config?.last_check_at
              ? new Date(config.last_check_at).toLocaleString()
              : "never"}
          </p>
          <p>
            <span className="font-medium text-foreground">Next check</span>{" "}
            {!enabled
              ? "not scheduled — automatic downloads are off"
              : config?.next_check_at
                ? new Date(config.next_check_at).toLocaleString()
                : "within a minute"}
          </p>
          {config?.source_url ? (
            <p className="break-all">
              <span className="font-medium text-foreground">Source</span>{" "}
              {config.source_url}
            </p>
          ) : null}
        </div>

        {downloadNote ? (
          <p className="text-xs text-muted-foreground">{downloadNote}</p>
        ) : null}

        <div className="flex items-center gap-3">
          <Button
            onClick={handleSave}
            disabled={readOnly || updateConfig.isPending}
          >
            {updateConfig.isPending ? "Saving..." : "Save"}
          </Button>
          <Button
            variant="outline"
            onClick={handleCheckNow}
            // Off means there is no cycle to trigger; the API refuses it too,
            // and Download now is the button for fetching without opting in.
            disabled={
              readOnly || checkNow.isPending || storage === "" || !enabled
            }
          >
            <RefreshCw
              className={`mr-2 h-4 w-4 ${checkNow.isPending ? "animate-spin" : ""}`}
            />
            {checkNow.isPending ? "Checking..." : "Check now"}
          </Button>
          <Button
            variant="outline"
            onClick={handleDownloadNow}
            disabled={readOnly || download.isPending || storage === ""}
          >
            <Download className="mr-2 h-4 w-4" />
            {download.isPending ? "Starting..." : "Download now"}
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
