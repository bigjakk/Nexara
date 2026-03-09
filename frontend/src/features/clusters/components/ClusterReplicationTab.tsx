import { useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Play, Plus, Trash2 } from "lucide-react";
import { useAuth } from "@/hooks/useAuth";
import { useReplicationJobs, useCreateReplicationJob, useDeleteReplicationJob, useTriggerReplication } from "@/features/replication/api/replication-queries";

interface ClusterReplicationTabProps {
  clusterId: string;
}

export function ClusterReplicationTab({ clusterId }: ClusterReplicationTabProps) {
  const { canManage } = useAuth();
  const jobsQuery = useReplicationJobs(clusterId);
  const createJob = useCreateReplicationJob(clusterId);
  const deleteJob = useDeleteReplicationJob(clusterId);
  const triggerJob = useTriggerReplication(clusterId);

  const [open, setOpen] = useState(false);
  const [jobId, setJobId] = useState("");
  const [target, setTarget] = useState("");
  const [schedule, setSchedule] = useState("*/15");

  const handleCreate = (e: React.SyntheticEvent) => {
    e.preventDefault();
    createJob.mutate({ id: jobId, type: "local", target, schedule }, {
      onSuccess: () => { setOpen(false); setJobId(""); setTarget(""); },
    });
  };

  const formatTime = (ts?: number) => {
    if (!ts) return "—";
    return new Date(ts * 1000).toLocaleString();
  };

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>Replication Jobs</CardTitle>
        {canManage("replication") && (
          <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>
              <Button size="sm"><Plus className="mr-2 h-4 w-4" />Create Job</Button>
            </DialogTrigger>
            <DialogContent className="max-w-sm">
              <DialogHeader><DialogTitle>Create Replication Job</DialogTitle></DialogHeader>
              <form onSubmit={handleCreate} className="space-y-4">
                <div><Label>Job ID (e.g. 100-0)</Label><Input value={jobId} onChange={(e) => { setJobId(e.target.value); }} required /></div>
                <div><Label>Target Node</Label><Input value={target} onChange={(e) => { setTarget(e.target.value); }} required /></div>
                <div><Label>Schedule</Label><Input value={schedule} onChange={(e) => { setSchedule(e.target.value); }} /></div>
                <Button type="submit" disabled={createJob.isPending}>{createJob.isPending ? "Creating..." : "Create"}</Button>
              </form>
            </DialogContent>
          </Dialog>
        )}
      </CardHeader>
      <CardContent>
        {jobsQuery.isLoading ? <Skeleton className="h-20 w-full" /> :
         !jobsQuery.data || jobsQuery.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">No replication jobs configured.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>ID</TableHead>
                <TableHead>Guest</TableHead>
                <TableHead>Target</TableHead>
                <TableHead>Schedule</TableHead>
                <TableHead>Last Sync</TableHead>
                <TableHead>Status</TableHead>
                {canManage("replication") && <TableHead className="text-right">Actions</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {jobsQuery.data.map((job) => (
                <TableRow key={job.id}>
                  <TableCell className="font-medium">{job.id}</TableCell>
                  <TableCell>{job.guest}</TableCell>
                  <TableCell>{job.target}</TableCell>
                  <TableCell className="text-xs">{job.schedule}</TableCell>
                  <TableCell className="text-xs">{formatTime(job.last_sync)}</TableCell>
                  <TableCell>
                    {job.error ? (
                      <Badge variant="destructive" title={job.error}>Error ({job.fail_count})</Badge>
                    ) : (
                      <Badge variant="default">OK</Badge>
                    )}
                  </TableCell>
                  {canManage("replication") && (
                    <TableCell className="text-right space-x-1">
                      {job.source && (
                        <Button variant="ghost" size="sm" onClick={() => { triggerJob.mutate({ id: job.id, node: job.source ?? "" }); }} title="Trigger sync now">
                          <Play className="h-4 w-4" />
                        </Button>
                      )}
                      <Button variant="ghost" size="sm" onClick={() => { deleteJob.mutate(job.id); }}>
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
    </Card>
  );
}
