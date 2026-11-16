import { useState, useMemo } from "react";
import { useTranslation } from "react-i18next";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import {
  usePBSSnapshotsByBackupID,
  useTriggerBackup,
  useDeleteSnapshot,
} from "@/features/backup/api/backup-queries";
import { useClusterStorage } from "@/features/clusters/api/cluster-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { describeError } from "@/lib/api-error";
import { RestoreDialog } from "@/features/backup/components/RestoreDialog";
import { TaskProgressBanner } from "./TaskProgressBanner";
import type { PBSSnapshot } from "@/features/backup/types/backup";
import { Play, Trash2, Loader2 } from "lucide-react";
import { formatBytes, formatExactDateTime } from "@/lib/format";

// The exact time: the Latest stat already gives its age (timeAgo).
function formatUnixTime(ts: number): string {
  return formatExactDateTime(ts * 1000);
}

function timeAgo(ts: number): string {
  const diff = Math.floor(Date.now() / 1000 - ts);
  if (diff < 3600) return `${String(Math.floor(diff / 60))}m ago`;
  if (diff < 86400) return `${String(Math.floor(diff / 3600))}h ago`;
  return `${String(Math.floor(diff / 86400))}d ago`;
}

interface BackupPanelProps {
  vmid: number;
  clusterId: string;
  nodeName: string;
  kind: "vm" | "ct";
}

export function BackupPanel({
  vmid,
  clusterId,
  nodeName,
  kind,
}: BackupPanelProps) {
  const { t } = useTranslation("backup");
  const backupId = String(vmid);
  const {
    data: snapshots,
    isLoading,
    error,
  } = usePBSSnapshotsByBackupID(clusterId, backupId);
  const [backupDialogOpen, setBackupDialogOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<PBSSnapshot | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const deleteMutation = useDeleteSnapshot(true);
  // The dialog shows a failed delete itself, and one that lands after it was
  // closed is toasted instead. Told apart by the snapshot it is for.
  const settleDelete = useSaveOutcome(
    deleteTarget
      ? `${deleteTarget.pbs_server_id}/${deleteTarget.datastore}/${deleteTarget.backup_type}/${deleteTarget.backup_id}/${String(deleteTarget.backup_time)}`
      : null,
  );

  const handleDelete = () => {
    if (!deleteTarget) return;
    setDeleteError(null);
    settleDelete(
      deleteMutation.mutateAsync({
        pbsId: deleteTarget.pbs_server_id,
        store: deleteTarget.datastore,
        body: {
          backup_type: deleteTarget.backup_type,
          backup_id: deleteTarget.backup_id,
          backup_time: deleteTarget.backup_time,
        },
      }),
      {
        action: t("guestBackupDeleteAction", {
          backup: `${deleteTarget.backup_type}/${deleteTarget.backup_id}`,
          time: formatUnixTime(deleteTarget.backup_time),
        }),
        onSuccess: () => {
          setDeleteTarget(null);
        },
        onError: (err) => {
          setDeleteError(describeError(err) || "Delete failed");
        },
      },
    );
  };

  if (isLoading) {
    return <Skeleton className="h-40 w-full" />;
  }

  const hasSnapshots = !error && snapshots && snapshots.length > 0;
  const sorted = hasSnapshots
    ? [...snapshots].sort((a, b) => b.backup_time - a.backup_time)
    : [];
  const latest: PBSSnapshot | undefined = sorted[0];
  const unverified = sorted.filter((snap) => !snap.verified).length;

  return (
    <div className="space-y-4">
      <Panel
        flush
        title="PBS backups"
        aside={
          hasSnapshots
            ? `${String(sorted.length)} on Proxmox Backup Server${unverified > 0 ? `, ${String(unverified)} not verified` : ", all verified"}`
            : undefined
        }
        actions={
          // As before the rebuild: a list that failed to load offers nothing.
          error ? undefined : (
            <Button
              size="sm"
              className="gap-1.5"
              onClick={() => {
                setBackupDialogOpen(true);
              }}
            >
              <Play className="h-4 w-4" />
              Back up now
            </Button>
          )
        }
      >
        {error ? (
          <p className="px-[18px] py-6 text-sm text-destructive">
            Failed to load backups: {error.message}
          </p>
        ) : !hasSnapshots ? (
          <div className="px-[18px] py-6 text-sm text-muted-foreground">
            <p>No PBS backups found for VMID {backupId}.</p>
            <p className="mt-1 text-[12.5px]">
              Backups appear here when a Proxmox Backup Server is configured and
              backups have been synced.
            </p>
          </div>
        ) : (
          <>
            {latest && (
              <StatStrip className="pb-4">
                <Stat label="Backups" value={String(sorted.length)} />
                <Stat
                  label="Latest"
                  value={timeAgo(latest.backup_time)}
                  sub={formatUnixTime(latest.backup_time)}
                />
                <Stat
                  label="Latest size"
                  value={formatBytes(latest.size)}
                  sub={`on ${latest.datastore}`}
                />
              </StatStrip>
            )}
            <p className="px-[18px] pb-3 text-[12.5px] text-muted-foreground">
              {t("guestSnapshotsSharedDatastore")}
            </p>
            <div className="border-t">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Time</TableHead>
                    <TableHead className="hidden sm:table-cell">Type</TableHead>
                    <TableHead>Datastore</TableHead>
                    <TableHead className="text-right">Size</TableHead>
                    <TableHead>Verified</TableHead>
                    <TableHead>Protected</TableHead>
                    <TableHead className="w-24">
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {sorted.map((snap) => (
                    <TableRow
                      key={`${snap.backup_type}-${snap.backup_id}-${String(snap.backup_time)}`}
                    >
                      <TableCell className="whitespace-nowrap">
                        <span className="tabular-nums">
                          {formatUnixTime(snap.backup_time)}
                        </span>
                        <span className="ml-2 text-[12.5px] text-muted-foreground">
                          {timeAgo(snap.backup_time)}
                        </span>
                      </TableCell>
                      <TableCell className="hidden text-muted-foreground sm:table-cell">
                        {snap.backup_type.toUpperCase()}
                      </TableCell>
                      <TableCell>{snap.datastore}</TableCell>
                      <TableCell className="text-right font-mono text-[12.5px]">
                        {formatBytes(snap.size)}
                      </TableCell>
                      <TableCell>
                        {snap.verified ? (
                          <Badge variant="healthy">Verified</Badge>
                        ) : (
                          <span className="text-muted-foreground">No</span>
                        )}
                      </TableCell>
                      <TableCell>
                        {snap.protected ? (
                          <Badge variant="outline">Protected</Badge>
                        ) : (
                          <span className="text-muted-foreground">No</span>
                        )}
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center justify-end gap-1">
                          <RestoreDialog
                            snapshot={snap}
                            pbsId={snap.pbs_server_id}
                          />
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-7 w-7 p-0 text-muted-foreground hover:text-destructive"
                            title="Delete backup"
                            aria-label="Delete backup"
                            onClick={() => {
                              setDeleteTarget(snap);
                            }}
                            disabled={snap.protected}
                          >
                            <Trash2 className="h-4 w-4" />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </>
        )}
      </Panel>

      {/* Backup Now Dialog */}
      <BackupNowDialog
        open={backupDialogOpen}
        onOpenChange={setBackupDialogOpen}
        clusterId={clusterId}
        nodeName={nodeName}
        vmid={vmid}
        kind={kind}
      />

      {/* Delete Confirmation Dialog */}
      {deleteTarget && (
        <Dialog
          open={!!deleteTarget}
          onOpenChange={(open) => {
            if (!open) {
              setDeleteTarget(null);
              setDeleteError(null);
            }
          }}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Delete backup</DialogTitle>
              <DialogDescription>
                Are you sure you want to delete the backup from{" "}
                <strong>{formatUnixTime(deleteTarget.backup_time)}</strong> on{" "}
                <strong>{deleteTarget.datastore}</strong>? This cannot be
                undone.
              </DialogDescription>
            </DialogHeader>
            {deleteError && (
              <p className="text-sm text-destructive">{deleteError}</p>
            )}
            <DialogFooter>
              <Button
                variant="outline"
                onClick={() => {
                  setDeleteTarget(null);
                  setDeleteError(null);
                }}
              >
                Cancel
              </Button>
              <Button
                variant="destructive"
                onClick={handleDelete}
                disabled={deleteMutation.isPending}
              >
                {deleteMutation.isPending ? "Deleting..." : "Delete"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </div>
  );
}

// --- Backup Now Dialog ---

interface BackupNowDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
  nodeName: string;
  vmid: number;
  kind: "vm" | "ct";
}

function BackupNowDialog({
  open,
  onOpenChange,
  clusterId,
  nodeName,
  vmid,
  kind,
}: BackupNowDialogProps) {
  const { t } = useTranslation("backup");
  const [storage, setStorage] = useState("");
  const [mode, setMode] = useState("snapshot");
  const [compress, setCompress] = useState("zstd");
  const [error, setError] = useState<string | null>(null);
  const [backupUpid, setBackupUpid] = useState<string | null>(null);

  const storageQuery = useClusterStorage(clusterId);
  const allStorage = useMemo(
    () => storageQuery.data ?? [],
    [storageQuery.data],
  );

  // Deduplicate shared storage, only show backup-capable storage
  // Sort: PBS first (recommended), then others
  const backupStorage = useMemo(() => {
    const seen = new Set<string>();
    const filtered = allStorage.filter((s) => {
      if (!s.active || !s.enabled) return false;
      if (!s.content.includes("backup")) return false;
      if (seen.has(s.storage)) return false;
      seen.add(s.storage);
      return true;
    });
    // PBS storage first
    return filtered.sort((a, b) => {
      if (a.type === "pbs" && b.type !== "pbs") return -1;
      if (a.type !== "pbs" && b.type === "pbs") return 1;
      return a.storage.localeCompare(b.storage);
    });
  }, [allStorage]);

  // Auto-select first PBS storage, or first backup storage
  const defaultStorage = useMemo(() => {
    const pbs = backupStorage.find((s) => s.type === "pbs");
    if (pbs) return pbs.storage;
    return backupStorage[0]?.storage ?? "";
  }, [backupStorage]);

  // Set storage to default when dialog opens
  const effectiveStorage = storage || defaultStorage;

  const triggerBackup = useTriggerBackup();
  // The dialog shows a failure itself (the hook opts out of the app's toast),
  // and one that lands after it was closed is toasted instead.
  const settle = useSaveOutcome(open);

  const handleBackup = () => {
    if (!effectiveStorage || !nodeName) return;
    setError(null);
    settle(
      triggerBackup.mutateAsync({
        clusterId,
        body: {
          vmid: String(vmid),
          node: nodeName,
          storage: effectiveStorage,
          mode,
          compress,
        },
      }),
      {
        action: t("guestBackupNowAction", {
          vmid: String(vmid),
          storage: effectiveStorage,
        }),
        onSuccess: (data) => {
          setError(null);
          setBackupUpid(data.upid);
        },
        onError: (err) => {
          setError(describeError(err) || "Backup failed");
        },
      },
    );
  };

  const handleClose = (v: boolean) => {
    onOpenChange(v);
    if (!v) {
      setError(null);
      setBackupUpid(null);
      setStorage("");
    }
  };

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Back up now</DialogTitle>
          <DialogDescription>
            Start a vzdump backup of {kind.toUpperCase()} {String(vmid)} on{" "}
            {nodeName}.
          </DialogDescription>
        </DialogHeader>

        {backupUpid ? (
          <>
            <div className="py-2">
              <TaskProgressBanner
                clusterId={clusterId}
                upid={backupUpid}
                description={`Backing up ${kind}/${String(vmid)}`}
              />
            </div>
            <DialogFooter>
              <Button
                variant="outline"
                onClick={() => {
                  handleClose(false);
                }}
              >
                Close
              </Button>
            </DialogFooter>
          </>
        ) : (
          <>
            <div className="space-y-4 py-2">
              <div className="space-y-2">
                <Label>Storage</Label>
                <select
                  className="w-full rounded-md border bg-background px-3 py-2 text-sm"
                  value={effectiveStorage}
                  onChange={(e) => {
                    setStorage(e.target.value);
                  }}
                >
                  {backupStorage.length === 0 && (
                    <option value="">No backup storage available</option>
                  )}
                  {backupStorage.map((s) => (
                    <option key={s.storage} value={s.storage}>
                      {s.storage} ({s.type})
                      {s.type === "pbs" ? " - Recommended" : ""}
                    </option>
                  ))}
                </select>
                {backupStorage.some((s) => s.type === "pbs") &&
                  effectiveStorage &&
                  (() => {
                    const selected = backupStorage.find(
                      (s) => s.storage === effectiveStorage,
                    );
                    if (selected?.type === "pbs") {
                      return (
                        <p className="text-xs text-muted-foreground">
                          PBS storage provides deduplication and incremental
                          backups.
                        </p>
                      );
                    }
                    return (
                      <p className="text-xs text-muted-foreground">
                        A PBS storage is available. Consider using it for
                        deduplication and incremental backups.
                      </p>
                    );
                  })()}
              </div>

              <div className="space-y-2">
                <Label>Mode</Label>
                <select
                  className="w-full rounded-md border bg-background px-3 py-2 text-sm"
                  value={mode}
                  onChange={(e) => {
                    setMode(e.target.value);
                  }}
                >
                  <option value="snapshot">Snapshot</option>
                  <option value="suspend">Suspend</option>
                  <option value="stop">Stop</option>
                </select>
              </div>

              <div className="space-y-2">
                <Label>Compression</Label>
                <select
                  className="w-full rounded-md border bg-background px-3 py-2 text-sm"
                  value={compress}
                  onChange={(e) => {
                    setCompress(e.target.value);
                  }}
                >
                  <option value="zstd">ZSTD (recommended)</option>
                  <option value="lzo">LZO</option>
                  <option value="gzip">GZIP</option>
                  <option value="0">None</option>
                </select>
              </div>

              {error && <p className="text-sm text-destructive">{error}</p>}
            </div>

            <DialogFooter>
              <Button
                variant="outline"
                onClick={() => {
                  handleClose(false);
                }}
              >
                Cancel
              </Button>
              <Button
                onClick={handleBackup}
                disabled={
                  triggerBackup.isPending || !effectiveStorage || !nodeName
                }
              >
                {triggerBackup.isPending ? (
                  <>
                    <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                    Starting...
                  </>
                ) : (
                  "Start backup"
                )}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
