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
import { Disc3, AlertTriangle, Download } from "lucide-react";
import { formatBytes } from "@/lib/format";
import { useClusterStorage } from "@/features/storage/api/storage-queries";
import {
  useVirtioWinConfig,
  useUpdateVirtioWinConfig,
  useVirtioWinReleases,
  useDownloadVirtioWin,
} from "../api/virtio-win-queries";
import { isAlreadyPresent, isAlreadyRunning } from "../types/virtio-win";
import type { VirtioWinConfigRequest } from "../types/virtio-win";
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
  const [saveStatus, setSaveStatus] = useState<"idle" | "saved" | "error">(
    "idle",
  );
  const [downloadNote, setDownloadNote] = useState("");

  useEffect(() => {
    if (config) {
      setEnabled(config.enabled);
      setStorage(config.storage);
      setNode(config.node);
      setTargetVersion(config.target_version);
      setPruneEnabled(config.prune_enabled);
    }
  }, [config]);

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
    const request: VirtioWinConfigRequest = {
      enabled,
      storage,
      node,
      target_version: targetVersion,
      prune_enabled: pruneEnabled,
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
              Checks upstream every 6 hours and fetches the target version when
              it is missing.
            </p>
          </div>
        </div>

        <div className="space-y-2">
          <Label>Target storage</Label>
          <Select
            value={storage}
            disabled={readOnly}
            onValueChange={(v) => {
              setStorage(v);
            }}
          >
            <SelectTrigger>
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
          <Label>Version</Label>
          <Select
            value={targetVersion === "" ? FOLLOW_STABLE : targetVersion}
            disabled={readOnly}
            onValueChange={(v) => {
              setTargetVersion(v === FOLLOW_STABLE ? "" : v);
            }}
          >
            <SelectTrigger>
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

        {config?.last_check_at ? (
          <p className="text-xs text-muted-foreground">
            Last checked {new Date(config.last_check_at).toLocaleString()}.
          </p>
        ) : null}

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
