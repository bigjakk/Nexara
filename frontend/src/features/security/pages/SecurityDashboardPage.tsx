import { useState, useCallback } from "react";
import { useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { ShieldAlert, Loader2, RefreshCw } from "lucide-react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import { Segmented } from "@/components/ui/segmented";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/EmptyState";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel } from "@/components/page/Panel";
import { SettingsSection } from "@/components/page/Settings";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { AddClusterDialog } from "@/features/dashboard/components/AddClusterDialog";
import { useAuth } from "@/hooks/useAuth";
import {
  useCVEScans,
  useSecurityPosture,
  useTriggerScan,
} from "../api/cve-queries";
import { postureSummary } from "../lib/posture";
import { PosturePanel } from "../components/PosturePanel";
import { SSVCHistogram } from "../components/SSVCHistogram";
import { CVENotifySettings } from "../components/CVENotifySettings";
import { KEVDetailDialog } from "../components/KEVDetailDialog";
import { ScanScheduleSettings } from "../components/ScanScheduleSettings";
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
  const { t } = useTranslation("security");
  const { t: td } = useTranslation("dashboard");
  const { hasPermission } = useAuth();
  const canManageRolling = hasPermission("manage", "rolling_update");
  const canManageSSH = hasPermission("manage", "ssh_credentials");

  const [searchParams, setSearchParams] = useSearchParams();
  const { data: clusters, isLoading: clustersLoading } = useClusters();
  const [selectedClusterId, setSelectedClusterId] = useState<string>("");
  const [selectedScanId, setSelectedScanId] = useState<string | null>(null);
  const [kevDialogScanId, setKEVDialogScanId] = useState<string | null>(null);

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
  const postureQuery = useSecurityPosture(activeClusterId);
  const { data: posture, isLoading: postureLoading } = postureQuery;
  const { data: scans, isLoading: scansLoading } = useCVEScans(activeClusterId);
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
        <EmptyState
          icon={ShieldAlert}
          title={td("noClustersRegistered")}
          description={t("noClustersConfigured")}
          action={<AddClusterDialog />}
        />
      </div>
    );
  }

  // Rolling update progress detail view: a page about one job, with its own
  // header and a trail back to the list.
  if (selectedJobId) {
    return (
      <RollingUpdateProgress
        clusterId={activeClusterId}
        clusterName={clusters.find((c) => c.id === activeClusterId)?.name}
        jobId={selectedJobId}
        canManage={canManageRolling}
      />
    );
  }

  return (
    <div className="space-y-5">
      <PageHeader
        title={t("security")}
        summary={postureSummary(postureLoading ? undefined : posture, t)}
        actions={
          <>
            {clusters.length > 1 && (
              <Segmented
                aria-label={t("cluster")}
                size="md"
                value={activeClusterId}
                onValueChange={(clusterId) => {
                  setSelectedClusterId(clusterId);
                  setSelectedScanId(null);
                  setSelectedJob(null);
                }}
                options={clusters.map((cluster) => ({
                  value: cluster.id,
                  label: cluster.name,
                }))}
              />
            )}
            {/* The one primary button is the open tab's own action. */}
            {activeTab === "rolling-updates" ? (
              canManageRolling &&
              activeClusterId && (
                <CreateRollingUpdateWizard clusterId={activeClusterId} />
              )
            ) : (
              <Button
                onClick={() => {
                  triggerScan.mutate(activeClusterId);
                }}
                disabled={triggerScan.isPending}
                className="gap-2"
              >
                {triggerScan.isPending ? (
                  <Loader2 className="h-4 w-4 animate-spin" />
                ) : (
                  <RefreshCw className="h-4 w-4" />
                )}
                {t("scanNow")}
              </Button>
            )}
          </>
        }
      />

      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <TabsList>
          <TabsTrigger value="vulnerabilities">
            {t("vulnerabilityScanning")}
          </TabsTrigger>
          <TabsTrigger value="rolling-updates">
            {t("rollingUpdates")}
          </TabsTrigger>
        </TabsList>

        {/* Vulnerability Scanning Tab: what the last scan found, the scans
            themselves, then how scanning is set up. */}
        <TabsContent value="vulnerabilities" className="mt-5 space-y-4">
          {/* Without a posture, the kit's notice says why: still loading, or
              a read that failed, which must not look like an empty page. */}
          {posture ? (
            <>
              <PosturePanel
                posture={posture}
                onShowKEV={(scanId) => {
                  setKEVDialogScanId(scanId);
                }}
              />
              <SSVCHistogram
                actCount={posture.act_count}
                attendCount={posture.attend_count}
                trackStarCount={posture.track_star_count}
                trackCount={posture.track_count}
              />
            </>
          ) : (
            <QueryStateNotice
              query={postureQuery}
              subject={t("postureSubject")}
              empty={null}
              skeletonClassName="h-32 w-full rounded-[10px]"
            />
          )}

          {selectedScanId ? (
            <VulnerabilityTable
              clusterId={activeClusterId}
              scanId={selectedScanId}
              onBack={() => {
                setSelectedScanId(null);
              }}
            />
          ) : (
            <Panel
              flush
              title="Scan history"
              aside={
                scans && scans.length > 0
                  ? `${String(scans.length)} ${scans.length === 1 ? "scan" : "scans"}`
                  : undefined
              }
            >
              {scansLoading ? (
                <div className="space-y-2 px-[18px] pb-1">
                  <Skeleton className="h-10 w-full" />
                  <Skeleton className="h-10 w-full" />
                  <Skeleton className="h-10 w-full" />
                </div>
              ) : (
                <ScanHistoryTable
                  scans={scans ?? []}
                  clusterId={activeClusterId}
                  onSelectScan={setSelectedScanId}
                />
              )}
            </Panel>
          )}

          <SettingsSection
            title="Scanning"
            description="When this cluster is scanned, and who is told what a scan finds."
          >
            <ScanScheduleSettings clusterId={activeClusterId} />
            <CVENotifySettings clusterId={activeClusterId} />
          </SettingsSection>
        </TabsContent>

        {/* Rolling Updates Tab */}
        <TabsContent value="rolling-updates" className="mt-5 space-y-4">
          {/* Gated like every sibling on this tab: with no clusters
              activeClusterId is "", which disables the query behind the card
              and leaves it reporting a read that was never going to happen. */}
          {activeClusterId && (
            <NodeUpdatesOverview clusterId={activeClusterId} />
          )}

          <Panel
            flush
            title="Rolling update jobs"
            aside={
              jobs && jobs.length > 0
                ? `${String(jobs.length)} ${jobs.length === 1 ? "job" : "jobs"}`
                : undefined
            }
          >
            {jobsLoading ? (
              <div className="flex h-24 items-center justify-center">
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
          </Panel>

          {canManageSSH && activeClusterId && (
            <SettingsSection
              title="SSH access"
              description={
                <>
                  Nexara runs <code>apt dist-upgrade</code> on each node over
                  SSH during an automated rolling update. It also uses this
                  access, once a day, to read each node&apos;s memory layout
                  with <code>dmidecode</code> (set{" "}
                  <code>NODE_MEMORY_READ_INTERVAL=0</code> to turn that off).
                </>
              }
            >
              <SSHCredentialsForm clusterId={activeClusterId} />
            </SettingsSection>
          )}
        </TabsContent>
      </Tabs>

      <KEVDetailDialog
        clusterId={activeClusterId}
        scanId={kevDialogScanId}
        open={kevDialogScanId !== null}
        onOpenChange={(open) => {
          if (!open) setKEVDialogScanId(null);
        }}
      />
    </div>
  );
}
