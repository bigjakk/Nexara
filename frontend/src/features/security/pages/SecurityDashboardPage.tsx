import { useState, useCallback } from "react";
import { useSearchParams } from "react-router-dom";
import { ShieldAlert, Loader2, RefreshCw } from "lucide-react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useAuth } from "@/hooks/useAuth";
import {
  useCVEScans,
  useSecurityPosture,
  useTriggerScan,
} from "../api/cve-queries";
import { SecurityPostureCard } from "../components/SecurityPostureCard";
import { ScanScheduleCard } from "../components/ScanScheduleCard";
import { ScanHistoryTable } from "../components/ScanHistoryTable";
import { VulnerabilityTable } from "../components/VulnerabilityTable";
import {
  useRollingUpdateJobs,
  useStartRollingUpdateJob,
  useCancelRollingUpdateJob,
} from "@/features/rolling-updates/api/rolling-update-queries";
import { RollingUpdateJobsTable } from "@/features/rolling-updates/components/RollingUpdateJobsTable";
import { RollingUpdateProgress } from "@/features/rolling-updates/components/RollingUpdateProgress";
import { CreateRollingUpdateWizard } from "@/features/rolling-updates/components/CreateRollingUpdateWizard";
import { NodeUpdatesOverview } from "@/features/rolling-updates/components/NodeUpdatesOverview";
import { SSHCredentialsForm } from "@/features/rolling-updates/components/SSHCredentialsForm";
import type { RollingUpdateJob } from "@/types/api";

export function SecurityDashboardPage() {
  const { hasPermission } = useAuth();
  const canManageRolling = hasPermission("manage", "rolling_update");
  const canManageSSH = hasPermission("manage", "ssh_credentials");

  const [searchParams, setSearchParams] = useSearchParams();
  const { data: clusters, isLoading: clustersLoading } = useClusters();
  const [selectedClusterId, setSelectedClusterId] = useState<string>("");
  const [selectedScanId, setSelectedScanId] = useState<string | null>(null);

  // Persist selected job ID in URL search params so page refresh preserves the view.
  const selectedJobId = searchParams.get("job");
  const activeTab = searchParams.get("tab") ?? "vulnerabilities";

  const setSelectedJob = useCallback(
    (job: RollingUpdateJob | null) => {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        if (job) {
          next.set("job", job.id);
          next.set("tab", "rolling-updates");
        } else {
          next.delete("job");
        }
        return next;
      });
    },
    [setSearchParams],
  );

  const setActiveTab = useCallback(
    (tab: string) => {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        next.set("tab", tab);
        next.delete("job"); // Clear job detail when switching tabs
        return next;
      });
    },
    [setSearchParams],
  );

  const activeClusterId =
    selectedClusterId ||
    (clusters && clusters.length > 0 ? (clusters[0]?.id ?? "") : "");

  // CVE scanning hooks
  const { data: posture, isLoading: postureLoading } =
    useSecurityPosture(activeClusterId);
  const { data: scans, isLoading: scansLoading } =
    useCVEScans(activeClusterId);
  const triggerScan = useTriggerScan();

  // Rolling update hooks
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
          <ShieldAlert className="mx-auto mb-2 h-12 w-12" />
          <p>No clusters configured. Add a cluster first.</p>
        </div>
      </div>
    );
  }

  // Rolling update progress detail view
  if (selectedJobId) {
    return (
      <div className="space-y-6 p-6">
        <div className="flex items-center gap-3">
          <ShieldAlert className="h-6 w-6" />
          <h1 className="text-2xl font-bold">Security</h1>
        </div>
        <RollingUpdateProgress
          clusterId={activeClusterId}
          jobId={selectedJobId}
          canManage={canManageRolling}
          onBack={() => {
            setSearchParams((prev) => {
              const next = new URLSearchParams(prev);
              next.delete("job");
              return next;
            });
          }}
        />
      </div>
    );
  }

  return (
    <div className="space-y-6 p-6">
      {/* Header */}
      <div className="flex items-center gap-3">
        <ShieldAlert className="h-6 w-6" />
        <h1 className="text-2xl font-bold">Security</h1>
      </div>

      {/* Cluster selector */}
      {clusters.length > 1 && (
        <div className="flex gap-2">
          {clusters.map((cluster) => (
            <button
              key={cluster.id}
              onClick={() => {
                setSelectedClusterId(cluster.id);
                setSelectedScanId(null);
                setSelectedJob(null);
              }}
              className={`rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
                activeClusterId === cluster.id
                  ? "bg-primary text-primary-foreground"
                  : "bg-muted text-muted-foreground hover:bg-accent"
              }`}
            >
              {cluster.name}
            </button>
          ))}
        </div>
      )}

      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <TabsList>
          <TabsTrigger value="vulnerabilities">Vulnerability Scanning</TabsTrigger>
          <TabsTrigger value="rolling-updates">Rolling Updates</TabsTrigger>
        </TabsList>

        {/* Vulnerability Scanning Tab */}
        <TabsContent value="vulnerabilities" className="mt-4 space-y-6">
          <div className="flex items-center justify-end">
            <button
              onClick={() => {
                triggerScan.mutate(activeClusterId);
              }}
              disabled={triggerScan.isPending}
              className="flex items-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
            >
              {triggerScan.isPending ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <RefreshCw className="h-4 w-4" />
              )}
              Scan Now
            </button>
          </div>

          {postureLoading ? (
            <div className="h-32 animate-pulse rounded-lg border bg-card" />
          ) : posture ? (
            <SecurityPostureCard posture={posture} />
          ) : null}

          <ScanScheduleCard clusterId={activeClusterId} />

          {selectedScanId ? (
            <VulnerabilityTable
              clusterId={activeClusterId}
              scanId={selectedScanId}
              onBack={() => {
                setSelectedScanId(null);
              }}
            />
          ) : (
            <div className="space-y-3">
              <h2 className="text-lg font-semibold">Scan History</h2>
              {scansLoading ? (
                <div className="h-48 animate-pulse rounded-lg border bg-card" />
              ) : (
                <ScanHistoryTable
                  scans={scans ?? []}
                  clusterId={activeClusterId}
                  onSelectScan={setSelectedScanId}
                />
              )}
            </div>
          )}
        </TabsContent>

        {/* Rolling Updates Tab */}
        <TabsContent value="rolling-updates" className="mt-4 space-y-6">
          <div className="flex items-center justify-end">
            {canManageRolling && activeClusterId && (
              <CreateRollingUpdateWizard clusterId={activeClusterId} />
            )}
          </div>

          <NodeUpdatesOverview clusterId={activeClusterId} />

          {canManageSSH && activeClusterId && (
            <div className="space-y-2">
              <h3 className="text-sm font-semibold">SSH Credentials</h3>
              <p className="text-xs text-muted-foreground">
                Configure SSH access for automated <code>apt dist-upgrade</code> on nodes.
              </p>
              <SSHCredentialsForm clusterId={activeClusterId} />
            </div>
          )}

          {jobsLoading ? (
            <div className="flex h-32 items-center justify-center">
              <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
            </div>
          ) : (
            <RollingUpdateJobsTable
              jobs={jobs ?? []}
              canManage={canManageRolling}
              onSelect={setSelectedJob}
              onStart={(jobId) => {
                startJob.mutate({ clusterId: activeClusterId, jobId });
              }}
              onCancel={(jobId) => {
                cancelJob.mutate({ clusterId: activeClusterId, jobId });
              }}
            />
          )}
        </TabsContent>
      </Tabs>
    </div>
  );
}
