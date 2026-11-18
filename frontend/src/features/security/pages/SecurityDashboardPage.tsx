import { useState, useCallback, useEffect } from "react";
import { useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { ShieldAlert, Loader2, RefreshCw } from "lucide-react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import { Segmented } from "@/components/ui/segmented";
import { Skeleton } from "@/components/ui/skeleton";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel } from "@/components/page/Panel";
import { SettingsSection } from "@/components/page/Settings";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { NoClustersEmptyState } from "@/features/dashboard/components/NoClustersEmptyState";
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
  useRollingUpdateJob,
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
  const { hasPermission } = useAuth();
  const canManageRolling = hasPermission("manage", "rolling_update");
  const canManageSSH = hasPermission("manage", "ssh_credentials");
  // Starting a scan, its schedule, its notifications and deleting one take
  // manage:cve_scan on the cluster; reading this page takes only view.
  const canManageScans = hasPermission("manage", "cve_scan");

  const [searchParams, setSearchParams] = useSearchParams();
  const { data: clusters, isLoading: clustersLoading } = useClusters();
  const [selectedScanId, setSelectedScanId] = useState<string | null>(null);
  const [kevDialogScanId, setKEVDialogScanId] = useState<string | null>(null);

  // The tab, the cluster and the job live in the URL, so a reload or a pasted
  // link lands where it was: a job belongs to one cluster, and asking another
  // for it answers 404.
  const selectedJobId = searchParams.get("job");
  const activeTab = searchParams.get("tab") ?? "vulnerabilities";
  const urlClusterId = searchParams.get("cluster") ?? "";
  const urlClusterKnown = clusters?.some((c) => c.id === urlClusterId) ?? false;

  const setSelectedJob = useCallback(
    (job: Pick<RollingUpdateJob, "id" | "cluster_id"> | null) => {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        if (job) {
          next.set("job", job.id);
          next.set("cluster", job.cluster_id);
          next.set("tab", "rolling-updates");
        } else {
          next.delete("job");
        }
        return next;
      });
    },
    [setSearchParams],
  );

  const setSelectedCluster = useCallback(
    (clusterId: string) => {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        next.set("cluster", clusterId);
        next.delete("job");
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

  const activeClusterId = urlClusterKnown
    ? urlClusterId
    : (clusters?.[0]?.id ?? "");

  // A job link without a known cluster (one saved before the cluster was kept
  // in the URL) is tried on the default cluster. Found, the job's cluster is
  // written into the URL, which ends this mode; not found (a failed read with
  // no data, not a failed poll), the link opens that cluster's jobs list.
  const legacyJob = useRollingUpdateJob(
    selectedJobId && !urlClusterKnown ? activeClusterId : "",
    selectedJobId ?? "",
  );
  const inLegacyMode = selectedJobId !== null && !urlClusterKnown;
  const legacyJobMissing =
    inLegacyMode && legacyJob.isError && legacyJob.data === undefined;
  const legacyJobCluster = inLegacyMode
    ? legacyJob.data?.cluster_id
    : undefined;
  useEffect(() => {
    if (!legacyJobMissing && legacyJobCluster === undefined) return;
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (legacyJobCluster !== undefined)
          next.set("cluster", legacyJobCluster);
        else next.delete("job");
        return next;
      },
      { replace: true },
    );
  }, [legacyJobMissing, legacyJobCluster, setSearchParams]);

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
        <NoClustersEmptyState
          icon={ShieldAlert}
          description={t("noClustersConfigured")}
        />
      </div>
    );
  }

  // Rolling update progress detail view: a page about one job, with its own
  // header and a trail back to the list.
  if (selectedJobId && !legacyJobMissing) {
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
                  setSelectedCluster(clusterId);
                  setSelectedScanId(null);
                }}
                options={clusters.map((cluster) => ({
                  value: cluster.id,
                  label: cluster.name,
                }))}
              />
            )}
            {/* The one primary button is the open tab's own action. */}
            {activeTab === "rolling-updates"
              ? canManageRolling &&
                activeClusterId && (
                  <CreateRollingUpdateWizard
                    clusterId={activeClusterId}
                    clusterName={
                      clusters.find((c) => c.id === activeClusterId)?.name
                    }
                    onStarted={setSelectedJob}
                  />
                )
              : canManageScans && (
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
                canScan={canManageScans}
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
                  canManage={canManageScans}
                  onSelectScan={setSelectedScanId}
                />
              )}
            </Panel>
          )}

          <SettingsSection
            title="Scanning"
            description="When this cluster is scanned, and who is told what a scan finds."
          >
            <ScanScheduleSettings
              clusterId={activeClusterId}
              canManage={canManageScans}
            />
            <CVENotifySettings
              clusterId={activeClusterId}
              canManage={canManageScans}
            />
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
                  // A started job is watched from its own view, where the
                  // node steps, Pause and Cancel are.
                  startJob.mutate(
                    { clusterId: activeClusterId, jobId },
                    {
                      onSuccess: (_job, vars) => {
                        setSelectedJob({
                          id: vars.jobId,
                          cluster_id: vars.clusterId,
                        });
                      },
                    },
                  );
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
