import { useState } from "react";
import { RefreshCw, Loader2 } from "lucide-react";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useAuth } from "@/hooks/useAuth";
import {
  useRollingUpdateJobs,
  useStartRollingUpdateJob,
  useCancelRollingUpdateJob,
} from "../api/rolling-update-queries";
import { RollingUpdateJobsTable } from "../components/RollingUpdateJobsTable";
import { RollingUpdateProgress } from "../components/RollingUpdateProgress";
import { CreateRollingUpdateWizard } from "../components/CreateRollingUpdateWizard";
import type { RollingUpdateJob } from "@/types/api";

export function RollingUpdatesPage() {
  const { hasPermission } = useAuth();
  const canManage = hasPermission("manage", "rolling_update");

  const { data: clusters, isLoading: clustersLoading } = useClusters();
  const [selectedClusterId, setSelectedClusterId] = useState<string>("");
  const [selectedJob, setSelectedJob] = useState<RollingUpdateJob | null>(null);

  const activeClusterId =
    selectedClusterId ||
    (clusters && clusters.length > 0 ? (clusters[0]?.id ?? "") : "");

  const { data: jobs, isLoading: jobsLoading } =
    useRollingUpdateJobs(activeClusterId);
  const startJob = useStartRollingUpdateJob();
  const cancelJob = useCancelRollingUpdateJob();

  if (clustersLoading) {
    return (
      <div className="flex h-full items-center justify-center">
        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!clusters || clusters.length === 0) {
    return (
      <div className="flex h-full items-center justify-center">
        <div className="text-center text-muted-foreground">
          <RefreshCw className="mx-auto mb-2 h-12 w-12" />
          <p>No clusters configured. Add a cluster first.</p>
        </div>
      </div>
    );
  }

  // Show progress view when a job is selected
  if (selectedJob) {
    return (
      <div className="space-y-6 p-6">
        <div className="flex items-center gap-3">
          <RefreshCw className="h-6 w-6" />
          <h1 className="text-2xl font-bold">Rolling Update</h1>
        </div>
        <RollingUpdateProgress
          clusterId={activeClusterId}
          jobId={selectedJob.id}
          canManage={canManage}
          onBack={() => { setSelectedJob(null); }}
        />
      </div>
    );
  }

  return (
    <div className="space-y-6 p-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <RefreshCw className="h-6 w-6" />
          <h1 className="text-2xl font-bold">Rolling Updates</h1>
        </div>
        {canManage && activeClusterId && (
          <CreateRollingUpdateWizard clusterId={activeClusterId} />
        )}
      </div>

      {/* Cluster selector */}
      {clusters.length > 1 && (
        <div className="flex items-center gap-2">
          <label className="text-sm font-medium">Cluster:</label>
          <select
            className="rounded-md border bg-background px-3 py-1.5 text-sm"
            value={activeClusterId}
            onChange={(e) => {
              setSelectedClusterId(e.target.value);
              setSelectedJob(null);
            }}
          >
            {clusters.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </div>
      )}

      {/* Jobs table */}
      {jobsLoading ? (
        <div className="flex h-32 items-center justify-center">
          <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
        </div>
      ) : (
        <RollingUpdateJobsTable
          jobs={jobs ?? []}
          canManage={canManage}
          onSelect={setSelectedJob}
          onStart={(jobId) =>
            { startJob.mutate({ clusterId: activeClusterId, jobId }); }
          }
          onCancel={(jobId) =>
            { cancelJob.mutate({ clusterId: activeClusterId, jobId }); }
          }
        />
      )}
    </div>
  );
}
