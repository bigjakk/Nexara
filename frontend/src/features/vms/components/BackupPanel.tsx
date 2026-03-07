import { useState, useMemo } from "react";
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
import {
  usePBSSnapshotsByBackupID,
  useTriggerBackup,
  useDeleteSnapshot,
} from "@/features/backup/api/backup-queries";
import { useClusterStorage } from "@/features/clusters/api/cluster-queries";
import { RestoreDialog } from "@/features/backup/components/RestoreDialog";
import { TaskProgressBanner } from "./TaskProgressBanner";
import type { PBSSnapshot } from "@/features/backup/types/backup";
import { Archive, Play, Trash2, Loader2, Star } from "lucide-react";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i] ?? ""}`;
}

function formatUnixTime(ts: number): string {
  return new Date(ts * 1000).toLocaleString();
}

function timeAgo(ts: number): string {
  const diff = Math.floor(Date.now() / 1000 - ts);
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  return `${Math.floor(diff / 86400)}d ago`;
}

interface BackupPanelProps {
  vmid: number;
  clusterId: string;
  nodeName: string;
  kind: "vm" | "ct";
}

export function BackupPanel({ vmid, clusterId, nodeName, kind }: BackupPanelProps) {
  const backupId = String(vmid);
  const { data: snapshots, isLoading, error } = usePBSSnapshotsByBackupID(backupId);
  const [backupDialogOpen, setBackupDialogOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<PBSSnapshot | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const deleteMutation = useDeleteSnapshot();

  const handleDelete = () => {
    if (!deleteTarget) return;
    setDeleteError(null);
    deleteMutation.mutate(
      {
        pbsId: deleteTarget.pbs_server_id,
        store: deleteTarget.datastore,
        body: {
          backup_type: deleteTarget.backup_type,
          backup_id: deleteTarget.backup_id,
          backup_time: deleteTarget.backup_time,
        },
      },
      {
        onSuccess: () => {
          setDeleteTarget(null);
        },
        onError: (err: Error) => {
          setDeleteError(err.message || "Delete failed");
        },
      },
    );
  };

  if (isLoading) {
    return (
      <div className="space-y-3">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-32 w-full" />
      </div>
    );
  }

  if (error) {
    return (
      <p className="py-8 text-center text-sm text-destructive">
        Failed to load backups: {error.message}
      </p>
    );
  }

  const hasSnapshots = snapshots && snapshots.length > 0;
  const sorted = hasSnapshots
    ? [...snapshots].sort((a, b) => b.backup_time - a.backup_time)
    : [];
  const latest = sorted[0] as PBSSnapshot | undefined;

  return (
    <div className="space-y-4">
      {/* Header with Backup Now button */}
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold">PBS Backups</h3>
        <Button
          size="sm"
          className="gap-1.5"
          onClick={() => { setBackupDialogOpen(true); }}
        >
          <Play className="h-4 w-4" />
          Backup Now
        </Button>
      </div>

      {!hasSnapshots ? (
        <div className="flex flex-col items-center gap-2 py-12 text-muted-foreground">
          <Archive className="h-10 w-10" />
          <p className="text-sm">No PBS backups found for VMID {backupId}.</p>
          <p className="text-xs">
            Backups appear here when a Proxmox Backup Server is configured and
            backups have been synced.
          </p>
        </div>
      ) : (
        <>
          {/* Summary */}
          <div className="flex items-center gap-4 rounded-lg border p-4">
            <div>
              <p className="text-xs text-muted-foreground">Total Backups</p>
              <p className="text-lg font-semibold">{String(snapshots.length)}</p>
            </div>
            {latest && (
              <>
                <div>
                  <p className="text-xs text-muted-foreground">Latest Backup</p>
                  <p className="text-sm font-medium">
                    {timeAgo(latest.backup_time)}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {formatUnixTime(latest.backup_time)}
                  </p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Latest Size</p>
                  <p className="text-sm font-medium">
                    {formatBytes(latest.size)}
                  </p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Datastore</p>
                  <p className="text-sm font-medium">{latest.datastore}</p>
                </div>
              </>
            )}
          </div>

          {/* Table */}
          <div className="rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Time</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>Datastore</TableHead>
                  <TableHead className="text-right">Size</TableHead>
                  <TableHead>Verified</TableHead>
                  <TableHead>Protected</TableHead>
                  <TableHead className="w-24" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {sorted.map((snap) => (
                  <TableRow
                    key={`${snap.backup_type}-${snap.backup_id}-${String(snap.backup_time)}`}
                  >
                    <TableCell>
                      <div>
                        <p className="text-sm">
                          {formatUnixTime(snap.backup_time)}
                        </p>
                        <p className="text-xs text-muted-foreground">
                          {timeAgo(snap.backup_time)}
                        </p>
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant={
                          snap.backup_type === "vm" ? "default" : "secondary"
                        }
                      >
                        {snap.backup_type.toUpperCase()}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-sm">{snap.datastore}</TableCell>
                    <TableCell className="text-right font-mono text-sm">
                      {formatBytes(snap.size)}
                    </TableCell>
                    <TableCell>
                      {snap.verified ? (
                        <Badge variant="default" className="bg-green-600">
                          Yes
                        </Badge>
                      ) : (
                        <Badge variant="secondary">No</Badge>
                      )}
                    </TableCell>
                    <TableCell>
                      {snap.protected ? (
                        <Badge variant="default" className="bg-blue-600">
                          Yes
                        </Badge>
                      ) : (
                        <span className="text-muted-foreground">No</span>
                      )}
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center gap-1">
                        <RestoreDialog
                          snapshot={snap}
                          pbsId={snap.pbs_server_id}
                        />
                        <Button
                          variant="ghost"
                          size="sm"
                          title="Delete backup"
                          onClick={() => { setDeleteTarget(snap); }}
                          disabled={snap.protected}
                        >
                          <Trash2 className="h-4 w-4 text-destructive" />
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
              <DialogTitle>Delete Backup</DialogTitle>
              <DialogDescription>
                Are you sure you want to delete the backup from{" "}
                <strong>{formatUnixTime(deleteTarget.backup_time)}</strong> on{" "}
                <strong>{deleteTarget.datastore}</strong>? This cannot be undone.
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
  const [storage, setStorage] = useState("");
  const [mode, setMode] = useState("snapshot");
  const [compress, setCompress] = useState("zstd");
  const [error, setError] = useState<string | null>(null);
  const [backupUpid, setBackupUpid] = useState<string | null>(null);

  const storageQuery = useClusterStorage(clusterId);
  const allStorage = storageQuery.data ?? [];

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

  const handleBackup = () => {
    if (!effectiveStorage || !nodeName) return;
    setError(null);
    triggerBackup.mutate(
      {
        clusterId,
        body: {
          vmid: String(vmid),
          node: nodeName,
          storage: effectiveStorage,
          mode,
          compress,
        },
      },
      {
        onSuccess: (data) => {
          setError(null);
          setBackupUpid(data.upid);
        },
        onError: (err: Error) => {
          setError(err.message || "Backup failed");
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
          <DialogTitle>Backup Now</DialogTitle>
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
              <Button variant="outline" onClick={() => { handleClose(false); }}>
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
                  onChange={(e) => { setStorage(e.target.value); }}
                >
                  {backupStorage.length === 0 && (
                    <option value="">No backup storage available</option>
                  )}
                  {backupStorage.map((s) => (
                    <option key={s.storage} value={s.storage}>
                      {s.storage} ({s.type}){s.type === "pbs" ? " - Recommended" : ""}
                    </option>
                  ))}
                </select>
                {backupStorage.some((s) => s.type === "pbs") && effectiveStorage && (
                  (() => {
                    const selected = backupStorage.find((s) => s.storage === effectiveStorage);
                    if (selected?.type === "pbs") {
                      return (
                        <p className="flex items-center gap-1 text-xs text-green-600">
                          <Star className="h-3 w-3" />
                          PBS storage provides deduplication and incremental backups.
                        </p>
                      );
                    }
                    return (
                      <p className="text-xs text-muted-foreground">
                        A PBS storage is available. Consider using it for deduplication and incremental backups.
                      </p>
                    );
                  })()
                )}
              </div>

              <div className="space-y-2">
                <Label>Mode</Label>
                <select
                  className="w-full rounded-md border bg-background px-3 py-2 text-sm"
                  value={mode}
                  onChange={(e) => { setMode(e.target.value); }}
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
                  onChange={(e) => { setCompress(e.target.value); }}
                >
                  <option value="zstd">ZSTD (recommended)</option>
                  <option value="lzo">LZO</option>
                  <option value="gzip">GZIP</option>
                  <option value="0">None</option>
                </select>
              </div>

              {error && (
                <p className="text-sm text-destructive">{error}</p>
              )}
            </div>

            <DialogFooter>
              <Button
                variant="outline"
                onClick={() => { handleClose(false); }}
              >
                Cancel
              </Button>
              <Button
                onClick={handleBackup}
                disabled={
                  triggerBackup.isPending ||
                  !effectiveStorage ||
                  !nodeName
                }
              >
                {triggerBackup.isPending ? (
                  <>
                    <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                    Starting...
                  </>
                ) : (
                  "Start Backup"
                )}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
