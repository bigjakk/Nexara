import { useEffect, useMemo, useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Plus } from "lucide-react";
import {
  useClusterNodes,
  useClusterStorage,
} from "@/features/clusters/api/cluster-queries";
import { useResourcePools } from "@/features/pools/api/pool-queries";
import { formatBytes } from "@/lib/format";
import { useCreateBackupJob, useUpdateBackupJob } from "../api/backup-queries";
import type { BackupJob, BackupJobParams } from "../types/backup";
import { GuestMultiSelect } from "./GuestMultiSelect";
import { ScheduleBuilder } from "./ScheduleBuilder";

// PVE's four guest-selection modes. "exclude" is an all-guests job with an
// exclusion list, which is why it ships all=1 alongside the list.
type GuestSelection = "all" | "include" | "exclude" | "pool";

const ALL_NODES = "__all__";
const DEFAULT_SCHEDULE = "02:00";

interface BackupJobDialogProps {
  clusterId: string;
  job?: BackupJob | null;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}

export function BackupJobDialog({
  clusterId,
  job,
  open: controlledOpen,
  onOpenChange: controlledOnOpenChange,
}: BackupJobDialogProps) {
  const isEdit = !!job;
  const [internalOpen, setInternalOpen] = useState(false);
  const open = controlledOpen ?? internalOpen;
  const onOpenChange = controlledOnOpenChange ?? setInternalOpen;

  const [schedule, setSchedule] = useState(DEFAULT_SCHEDULE);
  const [storage, setStorage] = useState("");
  const [node, setNode] = useState("");
  const [selection, setSelection] = useState<GuestSelection>("all");
  const [vmid, setVmid] = useState("");
  const [exclude, setExclude] = useState("");
  const [pool, setPool] = useState("");
  const [mode, setMode] = useState("snapshot");
  const [compress, setCompress] = useState("zstd");
  const [enabled, setEnabled] = useState(true);
  const [comment, setComment] = useState("");

  const createMutation = useCreateBackupJob();
  const updateMutation = useUpdateBackupJob();
  const storageQuery = useClusterStorage(clusterId);
  const nodesQuery = useClusterNodes(clusterId);
  const poolsQuery = useResourcePools(clusterId);

  // Storage is reported per node, so shared targets repeat; dedupe by name and
  // keep only what can actually hold backups.
  const backupStorages = useMemo(() => {
    const seen = new Set<string>();
    return (storageQuery.data ?? [])
      .filter((s) => s.content.split(",").includes("backup") && s.enabled)
      .filter((s) => {
        if (seen.has(s.storage)) return false;
        seen.add(s.storage);
        return true;
      })
      .sort((a, b) => a.storage.localeCompare(b.storage));
  }, [storageQuery.data]);

  const { reset: resetCreate } = createMutation;
  const { reset: resetUpdate } = updateMutation;

  useEffect(() => {
    if (!open) return;
    resetCreate();
    resetUpdate();
    if (job) {
      setSchedule(job.schedule ?? "");
      setStorage(job.storage ?? "");
      setNode(job.node ?? "");
      setMode(job.mode ?? "snapshot");
      setCompress(job.compress ?? "zstd");
      setEnabled(job.enabled !== 0);
      setComment(job.comment ?? "");
      setVmid(job.vmid ?? "");
      setExclude(job.exclude ?? "");
      setPool(job.pool ?? "");
      // Same precedence vzdump itself applies to a config carrying more than
      // one selection (all, then pool, then the vmid list), so a hand-edited
      // job opens as the thing PVE will actually back up.
      if (job.exclude) {
        setSelection("exclude");
      } else if (job.all) {
        setSelection("all");
      } else if (job.pool) {
        setSelection("pool");
      } else if (job.vmid) {
        setSelection("include");
      } else {
        setSelection("all");
      }
      return;
    }
    setSchedule(DEFAULT_SCHEDULE);
    setStorage("");
    setNode("");
    setSelection("all");
    setVmid("");
    setExclude("");
    setPool("");
    setMode("snapshot");
    setCompress("zstd");
    setEnabled(true);
    setComment("");
  }, [job, open, resetCreate, resetUpdate]);

  // With a single backup target there is no choice to make — preselect it.
  useEffect(() => {
    const only = backupStorages.length === 1 ? backupStorages[0] : undefined;
    if (!open || job || storage !== "" || !only) return;
    setStorage(only.storage);
  }, [open, job, storage, backupStorages]);

  // An existing job may point at storage that is gone or no longer flagged for
  // backups; keep it listed so editing the schedule doesn't silently retarget.
  const storageOptions = useMemo(() => {
    const names = backupStorages.map((s) => ({
      name: s.storage,
      detail: `${s.type}${s.avail > 0 ? ` · ${formatBytes(s.avail)} free` : ""}`,
    }));
    if (storage !== "" && !names.some((s) => s.name === storage)) {
      names.unshift({ name: storage, detail: "not listed on this cluster" });
    }
    return names;
  }, [backupStorages, storage]);

  const selectionValue =
    selection === "include" ? vmid : selection === "exclude" ? exclude : pool;
  const canSubmit =
    schedule.trim() !== "" &&
    storage !== "" &&
    (selection === "all" || selectionValue.trim() !== "");

  const handleSubmit = () => {
    const guests: BackupJobParams =
      selection === "include"
        ? { vmid }
        : selection === "exclude"
          ? { all: 1, exclude }
          : selection === "pool"
            ? { pool }
            : { all: 1 };

    const body: BackupJobParams = {
      enabled: enabled ? 1 : 0,
      schedule,
      storage,
      node,
      mode,
      compress,
      comment,
      ...guests,
    };

    if (job) {
      updateMutation.mutate(
        { clusterId, jobId: job.id, body },
        {
          onSuccess: () => {
            onOpenChange(false);
          },
        },
      );
    } else {
      createMutation.mutate(
        { clusterId, body },
        {
          onSuccess: () => {
            onOpenChange(false);
          },
        },
      );
    }
  };

  const isPending = createMutation.isPending || updateMutation.isPending;
  const error = createMutation.error ?? updateMutation.error;

  const trigger = !isEdit ? (
    <DialogTrigger asChild>
      <Button variant="outline" size="sm">
        <Plus className="mr-1.5 h-3.5 w-3.5" />
        Add Schedule
      </Button>
    </DialogTrigger>
  ) : null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {trigger}
      <DialogContent className="max-h-[85vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>
            {isEdit ? "Edit Backup Job" : "Create Backup Job"}
          </DialogTitle>
          <DialogDescription>
            {isEdit
              ? "Modify the vzdump backup job schedule."
              : "Create a new vzdump backup job schedule."}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <div className="space-y-2">
            <Label>Schedule</Label>
            <ScheduleBuilder value={schedule} onChange={setSchedule} />
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="backup-storage">Storage</Label>
              <Select
                value={storage}
                onValueChange={setStorage}
                disabled={storageQuery.isLoading}
              >
                <SelectTrigger id="backup-storage">
                  <SelectValue
                    placeholder={
                      storageQuery.isLoading
                        ? "Loading..."
                        : "Select backup storage"
                    }
                  />
                </SelectTrigger>
                <SelectContent>
                  {storageOptions.map((s) => (
                    <SelectItem key={s.name} value={s.name}>
                      {s.name}
                      <span className="ml-2 text-xs text-muted-foreground">
                        {s.detail}
                      </span>
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {storageQuery.isSuccess && backupStorages.length === 0 && (
                <p className="text-xs text-muted-foreground">
                  No storage on this cluster accepts backups. Enable the
                  &quot;VZDump backup file&quot; content type on a storage
                  first.
                </p>
              )}
            </div>

            <div className="space-y-2">
              <Label htmlFor="backup-node">Node</Label>
              <Select
                value={node === "" ? ALL_NODES : node}
                onValueChange={(v) => {
                  setNode(v === ALL_NODES ? "" : v);
                }}
              >
                <SelectTrigger id="backup-node">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={ALL_NODES}>All nodes</SelectItem>
                  {(nodesQuery.data ?? []).map((n) => (
                    <SelectItem key={n.id} value={n.name}>
                      {n.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="backup-selection">Guests</Label>
              <Select
                value={selection}
                onValueChange={(v) => {
                  setSelection(v as GuestSelection);
                }}
              >
                <SelectTrigger id="backup-selection">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">All guests</SelectItem>
                  <SelectItem value="include">Selected guests</SelectItem>
                  <SelectItem value="exclude">All except selected</SelectItem>
                  <SelectItem value="pool">Resource pool</SelectItem>
                </SelectContent>
              </Select>
            </div>

            {selection !== "all" && (
              <div className="space-y-2">
                <Label htmlFor="backup-guests">
                  {selection === "pool"
                    ? "Pool"
                    : selection === "exclude"
                      ? "Excluded guests"
                      : "Included guests"}
                </Label>
                {selection === "pool" ? (
                  <Select value={pool} onValueChange={setPool}>
                    <SelectTrigger id="backup-guests">
                      <SelectValue placeholder="Select a pool" />
                    </SelectTrigger>
                    <SelectContent>
                      {(poolsQuery.data ?? []).map((p) => (
                        <SelectItem key={p.poolid} value={p.poolid}>
                          {p.poolid}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                ) : (
                  <GuestMultiSelect
                    id="backup-guests"
                    clusterId={clusterId}
                    value={selection === "exclude" ? exclude : vmid}
                    onChange={selection === "exclude" ? setExclude : setVmid}
                    placeholder={
                      selection === "exclude"
                        ? "Select guests to skip..."
                        : "Select guests..."
                    }
                  />
                )}
              </div>
            )}
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="backup-mode">Mode</Label>
              <Select value={mode} onValueChange={setMode}>
                <SelectTrigger id="backup-mode">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="snapshot">Snapshot</SelectItem>
                  <SelectItem value="suspend">Suspend</SelectItem>
                  <SelectItem value="stop">Stop</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="backup-compress">Compression</Label>
              <Select value={compress} onValueChange={setCompress}>
                <SelectTrigger id="backup-compress">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="zstd">zstd</SelectItem>
                  <SelectItem value="lzo">lzo</SelectItem>
                  <SelectItem value="gzip">gzip</SelectItem>
                  <SelectItem value="0">None</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="space-y-2">
            <Label htmlFor="backup-comment">Comment</Label>
            <Input
              id="backup-comment"
              value={comment}
              onChange={(e) => {
                setComment(e.target.value);
              }}
              placeholder="Optional description"
            />
          </div>

          <div className="flex items-center gap-2">
            <Checkbox
              id="backup-enabled"
              checked={enabled}
              onCheckedChange={(checked) => {
                setEnabled(checked === true);
              }}
            />
            <Label htmlFor="backup-enabled">Enabled</Label>
          </div>

          {error && (
            <p className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {error.message}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => {
              onOpenChange(false);
            }}
          >
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={isPending || !canSubmit}>
            {isPending ? "Saving..." : isEdit ? "Update" : "Create"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
