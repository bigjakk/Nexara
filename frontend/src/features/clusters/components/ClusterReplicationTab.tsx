import { useState, useMemo } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Pencil, Play, Plus, Trash2 } from "lucide-react";
import { useAuth } from "@/hooks/useAuth";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import {
  useReplicationJobs,
  useCreateReplicationJob,
  useUpdateReplicationJob,
  useDeleteReplicationJob,
  useTriggerReplication,
} from "@/features/replication/api/replication-queries";
import type { ReplicationJob } from "@/features/replication/api/replication-queries";
import {
  useClusterVMs,
  useClusterNodes,
} from "@/features/clusters/api/cluster-queries";

interface ClusterReplicationTabProps {
  clusterId: string;
}

export function ClusterReplicationTab({
  clusterId,
}: ClusterReplicationTabProps) {
  const { canManage } = useAuth();
  const jobsQuery = useReplicationJobs(clusterId);
  const deleteJob = useDeleteReplicationJob(clusterId);
  const triggerJob = useTriggerReplication(clusterId);

  const [createOpen, setCreateOpen] = useState(false);
  const [editJob, setEditJob] = useState<ReplicationJob | null>(null);
  const [pendingDelete, setPendingDelete] = useState<ReplicationJob | null>(
    null,
  );

  const formatTime = (ts?: number) => {
    if (!ts) return "—";
    return new Date(ts * 1000).toLocaleString();
  };

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>Replication Jobs</CardTitle>
        {canManage("replication") && (
          <Dialog open={createOpen} onOpenChange={setCreateOpen}>
            <DialogTrigger asChild>
              <Button size="sm">
                <Plus className="mr-2 h-4 w-4" />
                Create Job
              </Button>
            </DialogTrigger>
            <DialogContent className="max-w-md">
              <DialogHeader>
                <DialogTitle>Create Replication Job</DialogTitle>
              </DialogHeader>
              <CreateJobForm
                clusterId={clusterId}
                onSuccess={() => {
                  setCreateOpen(false);
                }}
              />
            </DialogContent>
          </Dialog>
        )}
      </CardHeader>
      <CardContent>
        {jobsQuery.isLoading ? (
          <Skeleton className="h-20 w-full" />
        ) : !jobsQuery.data || jobsQuery.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No replication jobs configured.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>ID</TableHead>
                <TableHead>Guest</TableHead>
                <TableHead>Target</TableHead>
                <TableHead>Schedule</TableHead>
                <TableHead>Comment</TableHead>
                <TableHead>Last Sync</TableHead>
                <TableHead>Status</TableHead>
                {canManage("replication") && (
                  <TableHead className="text-right">Actions</TableHead>
                )}
              </TableRow>
            </TableHeader>
            <TableBody>
              {jobsQuery.data.map((job) => (
                <TableRow key={job.id}>
                  <TableCell className="font-medium">{job.id}</TableCell>
                  <TableCell>{job.guest}</TableCell>
                  <TableCell>{job.target}</TableCell>
                  <TableCell className="text-xs">
                    {job.schedule ?? "*/15"}
                  </TableCell>
                  <TableCell
                    className="max-w-[150px] truncate text-xs text-muted-foreground"
                    title={job.comment ?? ""}
                  >
                    {job.comment ?? "—"}
                  </TableCell>
                  <TableCell className="text-xs">
                    {formatTime(job.last_sync)}
                  </TableCell>
                  <TableCell>
                    {job.error ? (
                      <Badge variant="destructive" title={job.error}>
                        Error ({job.fail_count})
                      </Badge>
                    ) : job.disable ? (
                      <Badge variant="secondary">Disabled</Badge>
                    ) : (
                      <Badge variant="default">OK</Badge>
                    )}
                  </TableCell>
                  {canManage("replication") && (
                    <TableCell className="text-right space-x-1">
                      <Button
                        variant="ghost"
                        size="sm"
                        aria-label={`Edit replication job ${job.id}`}
                        title="Edit job"
                        onClick={() => {
                          setEditJob(job);
                        }}
                      >
                        <Pencil className="h-4 w-4" />
                      </Button>
                      {job.source && (
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => {
                            triggerJob.mutate({
                              id: job.id,
                              node: job.source ?? "",
                            });
                          }}
                          aria-label={`Trigger sync for replication job ${job.id}`}
                          title="Trigger sync now"
                        >
                          <Play className="h-4 w-4" />
                        </Button>
                      )}
                      <Button
                        aria-label={`Delete replication job ${job.id}`}
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          setPendingDelete(job);
                        }}
                      >
                        <Trash2 className="h-4 w-4 text-destructive" />
                      </Button>
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>

      {/*
        What the delete does, from pve-manager PVE/API2/ReplicationConfig.pm
        "delete": Nexara sends neither keep nor force
        (proxmox.Client.DeleteReplicationJob), so a local job gets
        remove_job=full. PVE/ReplicationState.pm job_status gives a job
        marked for removal next_sync 1, the earliest, even when disabled, so
        the replication runner's next pass takes it up: pve-guest-common
        PVE/Replication.pm replicate() removes the job's replicated volumes
        on the target over SSH (remote_prepare_local_job with an empty volume
        list) and its replication snapshots on the source, and only then drops
        the job from the config.
      */}
      <ConfirmDeleteDialog
        target={pendingDelete}
        onClose={() => {
          setPendingDelete(null);
        }}
        onConfirm={(job) => {
          deleteJob.mutate(job.id);
        }}
        title={(job) => `Delete replication job ${job.id}?`}
        description={(job) =>
          `Proxmox marks the job for removal as soon as you confirm, and removes it in the background shortly after: it deletes the copy of guest ${String(job.guest)}'s disks that this job replicated to ${job.target}, and the job's replication snapshots on the source. The guest and its own disks are not touched. The replicated copy on ${job.target} cannot be recovered.`
        }
      />

      {editJob != null && (
        <EditJobDialog
          clusterId={clusterId}
          job={editJob}
          onClose={() => {
            setEditJob(null);
          }}
        />
      )}
    </Card>
  );
}

// --- Create Job Form ---

function CreateJobForm({
  clusterId,
  onSuccess,
}: {
  clusterId: string;
  onSuccess: () => void;
}) {
  const vmsQuery = useClusterVMs(clusterId);
  const nodesQuery = useClusterNodes(clusterId);
  const jobsQuery = useReplicationJobs(clusterId);
  const createJob = useCreateReplicationJob(clusterId);

  const [selectedGuest, setSelectedGuest] = useState("");
  const [target, setTarget] = useState("");
  const [schedule, setSchedule] = useState("*/15");
  const [rate, setRate] = useState("");
  const [comment, setComment] = useState("");

  // Auto-generate job number for selected guest
  const nextJobNum = useMemo(() => {
    if (!selectedGuest) return 0;
    const existing = (jobsQuery.data ?? []).filter(
      (j) => String(j.guest) === selectedGuest,
    );
    return existing.length;
  }, [jobsQuery.data, selectedGuest]);

  const handleCreate = (e: React.SyntheticEvent) => {
    e.preventDefault();
    if (!selectedGuest || !target) return;
    const jobId = `${selectedGuest}-${String(nextJobNum)}`;
    createJob.mutate(
      {
        id: jobId,
        type: "local",
        target,
        schedule: schedule || "*/15",
        ...(rate ? { rate } : {}),
        ...(comment ? { comment } : {}),
      },
      { onSuccess },
    );
  };

  return (
    <form onSubmit={handleCreate} className="space-y-4">
      <div className="space-y-2">
        <Label>VM / CT</Label>
        <Select value={selectedGuest} onValueChange={setSelectedGuest}>
          <SelectTrigger>
            <SelectValue placeholder="Select a VM or CT..." />
          </SelectTrigger>
          <SelectContent>
            {(vmsQuery.data ?? []).map((vm) => (
              <SelectItem key={vm.vmid} value={String(vm.vmid)}>
                {vm.vmid} — {vm.name} ({vm.type})
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="space-y-2">
        <Label>Target Node</Label>
        <Select value={target} onValueChange={setTarget}>
          <SelectTrigger>
            <SelectValue placeholder="Select target node..." />
          </SelectTrigger>
          <SelectContent>
            {(nodesQuery.data ?? []).map((node) => (
              <SelectItem key={node.name} value={node.name}>
                {node.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="space-y-2">
        <Label>Schedule (cron)</Label>
        <Input
          value={schedule}
          onChange={(e) => {
            setSchedule(e.target.value);
          }}
          placeholder="*/15"
        />
      </div>

      <div className="space-y-2">
        <Label>Rate Limit (MB/s, optional)</Label>
        <Input
          value={rate}
          onChange={(e) => {
            setRate(e.target.value);
          }}
          placeholder="e.g. 10"
        />
      </div>

      <div className="space-y-2">
        <Label>Comment</Label>
        <Input
          value={comment}
          onChange={(e) => {
            setComment(e.target.value);
          }}
          placeholder="Optional description"
        />
      </div>

      {selectedGuest && (
        <p className="text-xs text-muted-foreground">
          Job ID: {selectedGuest}-{String(nextJobNum)}
        </p>
      )}

      {createJob.isError && (
        <p className="text-sm text-destructive">{createJob.error.message}</p>
      )}

      {createJob.isSuccess && (
        <p className="text-sm text-emerald-600">
          Replication job created successfully.
        </p>
      )}

      <Button
        type="submit"
        disabled={!selectedGuest || !target || createJob.isPending}
      >
        {createJob.isPending ? "Creating..." : "Create"}
      </Button>
    </form>
  );
}

// --- Edit Job Dialog ---

function EditJobDialog({
  clusterId,
  job,
  onClose,
}: {
  clusterId: string;
  job: ReplicationJob;
  onClose: () => void;
}) {
  const updateJob = useUpdateReplicationJob(clusterId);

  const [schedule, setSchedule] = useState(job.schedule ?? "*/15");
  const [rate, setRate] = useState(job.rate ?? "");
  const [comment, setComment] = useState(job.comment ?? "");
  const [disable, setDisable] = useState(job.disable ? "1" : "0");

  const handleSave = (e: React.SyntheticEvent) => {
    e.preventDefault();
    updateJob.mutate(
      {
        id: job.id,
        schedule,
        ...(rate ? { rate } : {}),
        comment,
        disable: Number(disable),
      },
      { onSuccess: onClose },
    );
  };

  return (
    <Dialog
      open
      onOpenChange={(v) => {
        if (!v) onClose();
      }}
    >
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Edit Replication Job: {job.id}</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSave} className="space-y-4">
          <div className="grid grid-cols-2 gap-4 text-sm">
            <div>
              <span className="text-muted-foreground">Guest:</span>{" "}
              <span className="font-medium">{job.guest}</span>
            </div>
            <div>
              <span className="text-muted-foreground">Target:</span>{" "}
              <span className="font-medium">{job.target}</span>
            </div>
          </div>

          <div className="space-y-2">
            <Label>Schedule (cron)</Label>
            <Input
              value={schedule}
              onChange={(e) => {
                setSchedule(e.target.value);
              }}
              placeholder="*/15"
            />
          </div>

          <div className="space-y-2">
            <Label>Rate Limit (MB/s, optional)</Label>
            <Input
              value={rate}
              onChange={(e) => {
                setRate(e.target.value);
              }}
              placeholder="e.g. 10"
            />
          </div>

          <div className="space-y-2">
            <Label>Comment</Label>
            <Input
              value={comment}
              onChange={(e) => {
                setComment(e.target.value);
              }}
              placeholder="Optional description"
            />
          </div>

          <div className="space-y-2">
            <Label>Enabled</Label>
            <Select value={disable} onValueChange={setDisable}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="0">Enabled</SelectItem>
                <SelectItem value="1">Disabled</SelectItem>
              </SelectContent>
            </Select>
          </div>

          {updateJob.isError && (
            <p className="text-sm text-destructive">
              {updateJob.error.message}
            </p>
          )}

          <div className="flex gap-2">
            <Button type="submit" disabled={updateJob.isPending}>
              {updateJob.isPending ? "Saving..." : "Save"}
            </Button>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
