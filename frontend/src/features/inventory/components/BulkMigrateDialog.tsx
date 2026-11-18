import { useState, useMemo, useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ViewMigrationsLink } from "@/features/migrations/components/ViewMigrationsLink";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { UsageBar } from "@/components/UsageBar";
import { WarningCallout } from "@/components/WarningCallout";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { sessionScope, StaleSessionError } from "@/lib/api-client";
import { DiskMoveOptions } from "@/features/storage/components/DiskMoveOptions";
import {
  parseBwlimit,
  resolveDiskFormat,
} from "@/features/storage/lib/disk-move";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import {
  useClusterNodes,
  useClusterStorage,
  useClusterVMs,
  useNodeBridges,
} from "@/features/clusters/api/cluster-queries";
import { useClusterMetrics } from "@/hooks/useMetrics";
import {
  useCreateMigration,
  useRunPreFlightCheck,
  useExecuteMigration,
  useMigrationJob,
} from "@/features/migrations/api/migration-queries";
import type {
  CreateMigrationRequest,
  MigrationType,
  MigrationMode,
  VMType,
} from "@/features/migrations/types/migration";
import { MigrationStatusText } from "@/features/migrations/components/MigrationStatusText";
import { movesStoppedHAGuest } from "@/features/migrations/lib/ha-migrate";
import type { InventoryRow } from "../types/inventory";
import type { Row } from "@tanstack/react-table";
import { ArrowLeftRight, CheckCircle2, XCircle, Loader2 } from "lucide-react";

type WizardStep = "config" | "preflight" | "progress";

interface BulkMigrateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  rows: Row<InventoryRow>[];
  onComplete: () => void;
}

interface JobEntry {
  vmName: string;
  vmid: number;
  vmType: string;
  sourceNode: string;
  jobId: string;
  preflightPassed: boolean | null;
  error: string | null;
}

export function BulkMigrateDialog({
  open,
  onOpenChange,
  rows,
  onComplete,
}: BulkMigrateDialogProps) {
  const { t } = useTranslation("vms");
  const [step, setStep] = useState<WizardStep>("config");

  // Form state
  const [migrationType, setMigrationType] =
    useState<MigrationType>("intra-cluster");
  const [migrationMode, setMigrationMode] = useState<MigrationMode>("live");
  const [targetClusterId, setTargetClusterId] = useState("");
  const [targetNode, setTargetNode] = useState("");
  const [targetStorage, setTargetStorage] = useState("");
  const [online, setOnline] = useState(true);
  const [bwlimit, setBwlimit] = useState("");
  // null = untouched (use each disk's current format where the target allows).
  const [diskFormat, setDiskFormat] = useState<string | null>(null);
  // null = follow the per-mode default in `deleteSource` below.
  const [deleteSourceOverride, setDeleteSourceOverride] = useState<
    boolean | null
  >(null);
  const [storageMap, setStorageMap] = useState<Record<string, string>>({});
  const [networkMap, setNetworkMap] = useState<Record<string, string>>({});

  // Job tracking
  const [jobs, setJobs] = useState<JobEntry[]>([]);
  const [isCreating, setIsCreating] = useState(false);
  const [isExecuting, setIsExecuting] = useState(false);
  const [executedJobIds, setExecutedJobIds] = useState<string[]>([]);

  const queryClient = useQueryClient();
  const createMutation = useCreateMigration();
  const checkMutation = useRunPreFlightCheck();
  const executeMutation = useExecuteMigration();

  // Cluster validation
  const clusterIds = useMemo(
    () => [...new Set(rows.map((r) => r.original.clusterId))],
    [rows],
  );
  const singleCluster = clusterIds.length === 1;
  const clusterId = clusterIds[0] ?? "";

  // Data hooks
  const { data: clusters } = useClusters();
  const effectiveTargetClusterId =
    migrationType === "cross-cluster" ? targetClusterId : clusterId;
  const { data: targetNodes } = useClusterNodes(effectiveTargetClusterId);
  const { data: sourceStorage } = useClusterStorage(clusterId);
  const needsStorageList =
    migrationType === "cross-cluster" ||
    migrationMode === "storage" ||
    migrationMode === "both";

  // Moving storage implies freeing the source, so default it on there. Across
  // clusters "delete source" destroys the source guests, so it stays opt-in.
  // With it off Proxmox keeps each source volume as an unused disk.
  const movesStorage =
    migrationType === "intra-cluster" &&
    (migrationMode === "storage" || migrationMode === "both");
  const deleteSource = deleteSourceOverride ?? movesStorage;
  // See MigrateJobDialog: only meaningful where something is left behind.
  const showDeleteSource = migrationType === "cross-cluster" || movesStorage;
  const { data: targetStorageList } = useClusterStorage(
    migrationType === "cross-cluster"
      ? targetClusterId
      : needsStorageList
        ? clusterId
        : "",
  );

  // Use first selected VM's node for bridge lookup
  const firstNode = rows[0]?.original.nodeName ?? "";
  const { data: sourceBridges } = useNodeBridges(
    migrationType === "cross-cluster" ? clusterId : "",
    migrationType === "cross-cluster" ? firstNode : "",
  );
  const { data: targetBridges } = useNodeBridges(
    migrationType === "cross-cluster" ? targetClusterId : "",
    migrationType === "cross-cluster" ? targetNode : "",
  );

  // Auto-node-selection
  const clusterMetrics = useClusterMetrics(effectiveTargetClusterId);
  const { data: clusterVMs } = useClusterVMs(effectiveTargetClusterId);

  const vmCountByNodeId = useMemo(() => {
    const map = new Map<string, number>();
    if (clusterVMs) {
      for (const vm of clusterVMs) {
        if (vm.status === "running") {
          map.set(vm.node_id, (map.get(vm.node_id) ?? 0) + 1);
        }
      }
    }
    return map;
  }, [clusterVMs]);

  const availableTargetNodes = targetNodes;

  useEffect(() => {
    if (!availableTargetNodes || availableTargetNodes.length === 0) return;
    if (targetNode.length > 0) return;

    const nodeMetrics = clusterMetrics?.nodeMetrics;
    let bestNode = "";
    let bestScore = Infinity;

    for (const node of availableTargetNodes) {
      if (node.status !== "online") continue;
      const live = nodeMetrics?.get(node.id);
      const cpu = live?.cpuPercent ?? 0;
      const mem = live?.memPercent ?? 0;
      const vms = vmCountByNodeId.get(node.id) ?? 0;
      const score = vms * 100 + cpu + mem;
      if (score < bestScore) {
        bestScore = score;
        bestNode = node.name;
      }
    }

    if (bestNode === "") {
      const firstOnline = availableTargetNodes.find(
        (n) => n.status === "online",
      );
      if (firstOnline) bestNode = firstOnline.name;
    }

    if (bestNode !== "") setTargetNode(bestNode);
  }, [
    availableTargetNodes,
    clusterMetrics?.nodeMetrics,
    targetNode.length,
    vmCountByNodeId,
  ]);

  // Deduplicate storage
  const uniqueSourceStorage = sourceStorage
    ? Array.from(new Map(sourceStorage.map((s) => [s.storage, s])).values())
    : [];
  const uniqueTargetStorage = targetStorageList
    ? Array.from(new Map(targetStorageList.map((s) => [s.storage, s])).values())
    : [];

  // Filter target storage by content type for selected VM types
  const hasVMs = rows.some((r) => r.original.type === "vm");
  const hasCTs = rows.some((r) => r.original.type === "ct");
  const filteredTargetStorage = uniqueTargetStorage.filter((s) => {
    if (!s.active || !s.enabled) return false;
    if (hasVMs && s.content.includes("images")) return true;
    if (hasCTs && s.content.includes("rootdir")) return true;
    return false;
  });

  const { value: bwlimitKib, invalid: bwlimitInvalid } = parseBwlimit(bwlimit);

  // A bulk move sends every selected guest's disks to one storage, so the
  // format follows that single target. Containers in the selection can't take
  // a format, so a mixed selection hides the field rather than sending one
  // the backend would reject.
  // Containers have no format choice, so a selection containing any CT hides
  // the field rather than sending a format the backend rejects.
  const showFormat = movesStorage && !hasCTs;
  const formatTargetType = showFormat
    ? filteredTargetStorage.find((s) => s.storage === targetStorage)?.type
    : undefined;

  // Form validation
  const isFormValid = (() => {
    if (bwlimitInvalid) return false;
    if (!singleCluster) return false;
    if (migrationType === "cross-cluster") {
      return targetNode.length > 0 && targetClusterId.length > 0;
    }
    if (migrationMode === "storage") return targetStorage.length > 0;
    if (migrationMode === "both")
      return targetNode.length > 0 && targetStorage.length > 0;
    return targetNode.length > 0;
  })();

  function resetForm() {
    setStep("config");
    setMigrationType("intra-cluster");
    setMigrationMode("live");
    setTargetClusterId("");
    setTargetNode("");
    setTargetStorage("");
    setOnline(true);
    setBwlimit("");
    setDiskFormat(null);
    setDeleteSourceOverride(null);
    setStorageMap({});
    setNetworkMap({});
    setJobs([]);
    setIsCreating(false);
    setIsExecuting(false);
    setExecutedJobIds([]);
  }

  function handleClose() {
    if (isCreating || isExecuting) return;
    resetForm();
    onOpenChange(false);
  }

  async function handleCreateAndCheck() {
    if (rows.length === 0) return;
    setIsCreating(true);

    // The session this was started in. The check is a write to the job the create
    // just made — it sets it to "checking", records the report and moves it on, to
    // pending or to failed — and needs manage:migration, not ownership of the job:
    // sent after the session changed hands it would run as whoever is signed in
    // next, on the previous user's job.
    const ended = sessionScope();

    const effectiveMode =
      migrationType === "intra-cluster" ? migrationMode : "live";

    const results = await Promise.allSettled(
      rows.map(async (row) => {
        const r = row.original;
        const vmType: VMType = r.type === "ct" ? "lxc" : "qemu";

        const req: CreateMigrationRequest = {
          source_cluster_id: r.clusterId,
          target_cluster_id:
            migrationType === "intra-cluster" ? r.clusterId : targetClusterId,
          source_node: r.nodeName,
          target_node: effectiveMode === "storage" ? r.nodeName : targetNode,
          vmid: r.vmid ?? 0,
          vm_type: vmType,
          migration_type: migrationType,
          migration_mode: effectiveMode,
          storage_map: migrationType === "cross-cluster" ? storageMap : {},
          network_map: migrationType === "cross-cluster" ? networkMap : {},
          online,
          bwlimit_kib: bwlimitKib,
          // Forced off where the control is hidden — see MigrateJobDialog.
          delete_source: showDeleteSource && deleteSource,
          disk_format:
            vmType === "qemu"
              ? resolveDiskFormat(diskFormat, undefined, formatTargetType)
              : "",
          target_vmid: 0,
          target_storage:
            effectiveMode === "storage" || effectiveMode === "both"
              ? targetStorage
              : "",
        };

        const created = await createMutation.mutateAsync(req);
        if (ended()) throw new StaleSessionError();
        const report = await checkMutation.mutateAsync(created.id);
        return {
          name: r.name,
          vmid: r.vmid,
          type: r.type,
          node: r.nodeName,
          jobId: created.id,
          passed: report.passed,
        };
      }),
    );

    const newJobs: JobEntry[] = [];
    for (let i = 0; i < results.length; i++) {
      const result = results[i];
      const row = rows[i];
      if (!result || !row) continue;
      if (result.status === "fulfilled") {
        newJobs.push({
          vmName: result.value.name,
          vmid: result.value.vmid ?? 0,
          vmType: result.value.type,
          sourceNode: result.value.node,
          jobId: result.value.jobId,
          preflightPassed: result.value.passed,
          error: null,
        });
      } else {
        newJobs.push({
          vmName: row.original.name,
          vmid: row.original.vmid ?? 0,
          vmType: row.original.type,
          sourceNode: row.original.nodeName,
          jobId: "",
          preflightPassed: false,
          error:
            result.reason instanceof Error
              ? result.reason.message
              : String(result.reason),
        });
      }
    }

    setJobs(newJobs);
    setIsCreating(false);
    setStep("preflight");
  }

  async function handleExecuteAll() {
    const passingJobs = jobs.filter((j) => j.preflightPassed && j.jobId);
    if (passingJobs.length === 0) return;

    setIsExecuting(true);
    const executedIds: string[] = [];

    const results = await Promise.allSettled(
      passingJobs.map((job) => executeMutation.mutateAsync(job.jobId)),
    );

    for (let i = 0; i < results.length; i++) {
      const result = results[i];
      const job = passingJobs[i];
      if (!result || !job) continue;
      if (result.status === "fulfilled") {
        executedIds.push(job.jobId);
      } else {
        setJobs((prev) =>
          prev.map((j) =>
            j.jobId === job.jobId
              ? {
                  ...j,
                  error:
                    result.reason instanceof Error
                      ? result.reason.message
                      : String(result.reason),
                  preflightPassed: false,
                }
              : j,
          ),
        );
      }
    }

    setExecutedJobIds(executedIds);
    setIsExecuting(false);
    setStep("progress");
    void queryClient.invalidateQueries({ queryKey: ["recent-activity"] });
  }

  const passingCount = jobs.filter((j) => j.preflightPassed).length;
  const failingCount = jobs.filter((j) => j.preflightPassed === false).length;

  // The selected guests a node move hands, stopped, to the HA manager: warned,
  // and the execute button says it goes ahead anyway when one of them passed.
  const haStoppedRows =
    migrationType === "intra-cluster" && migrationMode !== "storage"
      ? rows.filter((r) =>
          movesStoppedHAGuest(r.original.status, r.original.haState),
        )
      : [];
  const haStoppedVmids = new Set(haStoppedRows.map((r) => r.original.vmid));
  const executesHAStopped = jobs.some(
    (j) => j.preflightPassed && haStoppedVmids.has(j.vmid),
  );

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) handleClose();
      }}
    >
      <DialogContent className="max-h-[85vh] max-w-xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <ArrowLeftRight className="h-5 w-5" />
            {step === "config" && "Bulk migrate"}
            {step === "preflight" && "Pre-flight checks"}
            {step === "progress" && "Migration progress"}
          </DialogTitle>
          <DialogDescription>
            Migrate {String(rows.length)} resource
            {rows.length !== 1 ? "s" : ""}
          </DialogDescription>
        </DialogHeader>

        {singleCluster && step !== "progress" && haStoppedRows.length > 0 && (
          <WarningCallout
            title={t("haStoppedMigrateBulkTitle", {
              count: haStoppedRows.length,
            })}
          >
            <p>
              {t("haStoppedMigrateBulkBody", {
                names: haStoppedRows.map((r) => r.original.name).join(", "),
              })}
            </p>
          </WarningCallout>
        )}

        {!singleCluster ? (
          <p className="text-sm text-destructive">
            All selected resources must be from the same cluster. You have
            resources from {String(clusterIds.length)} clusters selected.
          </p>
        ) : step === "config" ? (
          <ConfigStep
            rows={rows}
            migrationType={migrationType}
            setMigrationType={(v) => {
              setMigrationType(v);
              setTargetNode("");
              setTargetClusterId("");
              setStorageMap({});
              setNetworkMap({});
              // See MigrateJobDialog: cross-cluster delete-source destroys the
              // source guest, so it must not be inherited across a type change.
              setDeleteSourceOverride(null);
            }}
            migrationMode={migrationMode}
            setMigrationMode={(v) => {
              setMigrationMode(v);
              setTargetStorage("");
              if (v === "storage") setTargetNode("");
            }}
            targetClusterId={targetClusterId}
            setTargetClusterId={(v) => {
              setTargetClusterId(v);
              setTargetNode("");
              setStorageMap({});
              setNetworkMap({});
            }}
            targetNode={targetNode}
            setTargetNode={setTargetNode}
            targetStorage={targetStorage}
            setTargetStorage={setTargetStorage}
            online={online}
            setOnline={setOnline}
            bwlimit={bwlimit}
            setBwlimit={setBwlimit}
            showFormat={showFormat}
            formatTargetType={formatTargetType}
            diskFormat={diskFormat}
            setDiskFormat={setDiskFormat}
            deleteSource={deleteSource}
            setDeleteSource={setDeleteSourceOverride}
            showDeleteSource={showDeleteSource}
            movesStorage={movesStorage}
            storageMap={storageMap}
            setStorageMap={setStorageMap}
            networkMap={networkMap}
            setNetworkMap={setNetworkMap}
            clusters={clusters}
            clusterId={clusterId}
            availableTargetNodes={availableTargetNodes}
            clusterMetrics={clusterMetrics}
            filteredTargetStorage={filteredTargetStorage}
            uniqueSourceStorage={uniqueSourceStorage}
            uniqueTargetStorage={uniqueTargetStorage}
            sourceBridges={sourceBridges}
            targetBridges={targetBridges}
            isFormValid={isFormValid}
            isCreating={isCreating}
            onCancel={handleClose}
            onSubmit={() => {
              void handleCreateAndCheck();
            }}
          />
        ) : step === "preflight" ? (
          <div className="space-y-4">
            <div className="max-h-60 space-y-2 overflow-y-auto">
              {jobs.map((j, i) => (
                <div
                  key={j.jobId || String(i)}
                  className="flex items-center gap-2 rounded-md border p-3"
                >
                  {j.error ? (
                    <XCircle className="h-4 w-4 shrink-0 text-red-500" />
                  ) : j.preflightPassed ? (
                    <CheckCircle2 className="h-4 w-4 shrink-0 text-emerald-500" />
                  ) : (
                    <XCircle className="h-4 w-4 shrink-0 text-red-500" />
                  )}
                  <div className="min-w-0 flex-1">
                    <p className="text-sm font-medium">
                      {j.vmName} ({j.vmType.toUpperCase()} {String(j.vmid)})
                    </p>
                    {j.error && (
                      <p className="truncate text-xs text-destructive">
                        {j.error}
                      </p>
                    )}
                    {!j.error && !j.preflightPassed && (
                      <p className="text-xs text-destructive">
                        Pre-flight checks failed
                      </p>
                    )}
                  </div>
                </div>
              ))}
            </div>

            <div className="flex items-center justify-between text-sm text-muted-foreground">
              <span>
                {String(passingCount)} passed, {String(failingCount)} failed
              </span>
            </div>

            <div className="flex gap-2">
              {passingCount > 0 && (
                <Button
                  onClick={() => {
                    void handleExecuteAll();
                  }}
                  disabled={isExecuting}
                  className="flex-1"
                >
                  {isExecuting ? (
                    <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                  ) : null}
                  {executesHAStopped ? (
                    t("executeMigrationsAnyway", { count: passingCount })
                  ) : (
                    <>
                      Execute {String(passingCount)} migration
                      {passingCount !== 1 ? "s" : ""}
                    </>
                  )}
                </Button>
              )}
              <Button variant="outline" onClick={handleClose}>
                Close
              </Button>
            </div>
          </div>
        ) : (
          <div className="space-y-4">
            <div className="max-h-72 space-y-2 overflow-y-auto">
              {jobs
                .filter((j) => executedJobIds.includes(j.jobId))
                .map((j) => (
                  <BulkJobProgress
                    key={j.jobId}
                    jobId={j.jobId}
                    vmName={j.vmName}
                    vmid={j.vmid}
                    vmType={j.vmType}
                  />
                ))}
              {jobs
                .filter((j) => j.error || !j.preflightPassed)
                .map((j, i) => (
                  <div
                    key={`skip-${String(i)}`}
                    className="flex items-center gap-2 rounded-md border border-destructive/30 p-3"
                  >
                    <XCircle className="h-4 w-4 shrink-0 text-red-500" />
                    <span className="text-sm">
                      {j.vmName} ({j.vmType.toUpperCase()} {String(j.vmid)})
                    </span>
                    <span className="ml-auto text-[12.5px] font-medium text-red-700 dark:text-red-400">
                      Skipped
                    </span>
                  </div>
                ))}
            </div>

            <div className="flex gap-2">
              <ViewMigrationsLink
                onNavigate={() => {
                  handleClose();
                  onComplete();
                }}
              />
              <Button
                variant="outline"
                onClick={() => {
                  handleClose();
                  onComplete();
                }}
              >
                Close
              </Button>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// Config step — extracted to reduce nesting
// ---------------------------------------------------------------------------

interface ConfigStepProps {
  rows: Row<InventoryRow>[];
  migrationType: MigrationType;
  setMigrationType: (v: MigrationType) => void;
  migrationMode: MigrationMode;
  setMigrationMode: (v: MigrationMode) => void;
  targetClusterId: string;
  setTargetClusterId: (v: string) => void;
  targetNode: string;
  setTargetNode: (v: string) => void;
  targetStorage: string;
  setTargetStorage: (v: string) => void;
  online: boolean;
  setOnline: (v: boolean) => void;
  bwlimit: string;
  setBwlimit: (v: string) => void;
  showFormat: boolean;
  formatTargetType?: string | undefined;
  diskFormat: string | null;
  setDiskFormat: (v: string) => void;
  deleteSource: boolean;
  setDeleteSource: (v: boolean) => void;
  showDeleteSource: boolean;
  movesStorage: boolean;
  storageMap: Record<string, string>;
  setStorageMap: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  networkMap: Record<string, string>;
  setNetworkMap: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  clusters: Array<{ id: string; name: string }> | undefined;
  clusterId: string;
  availableTargetNodes:
    | Array<{ id: string; name: string; status: string }>
    | undefined;
  clusterMetrics: ReturnType<typeof useClusterMetrics>;
  filteredTargetStorage: Array<{
    storage: string;
    type: string;
    active: boolean;
    enabled: boolean;
    content: string;
  }>;
  uniqueSourceStorage: Array<{
    storage: string;
    type: string;
  }>;
  uniqueTargetStorage: Array<{
    storage: string;
    type: string;
  }>;
  sourceBridges: Array<{ iface: string }> | undefined;
  targetBridges: Array<{ iface: string }> | undefined;
  isFormValid: boolean;
  isCreating: boolean;
  onSubmit: () => void;
  onCancel: () => void;
}

function ConfigStep({
  rows,
  migrationType,
  setMigrationType,
  migrationMode,
  setMigrationMode,
  targetClusterId,
  setTargetClusterId,
  targetNode,
  setTargetNode,
  targetStorage,
  setTargetStorage,
  online,
  setOnline,
  bwlimit,
  setBwlimit,
  showFormat,
  formatTargetType,
  diskFormat,
  setDiskFormat,
  deleteSource,
  setDeleteSource,
  showDeleteSource,
  movesStorage,
  storageMap,
  setStorageMap,
  networkMap,
  setNetworkMap,
  clusters,
  clusterId,
  availableTargetNodes,
  clusterMetrics,
  filteredTargetStorage,
  uniqueSourceStorage,
  uniqueTargetStorage,
  sourceBridges,
  targetBridges,
  isFormValid,
  isCreating,
  onSubmit,
  onCancel,
}: ConfigStepProps) {
  return (
    <div className="space-y-4">
      {/* Migration Type */}
      <div className="space-y-2">
        <Label>Migration type</Label>
        <Select
          value={migrationType}
          onValueChange={(v) => {
            setMigrationType(v as MigrationType);
          }}
        >
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="intra-cluster">Within this cluster</SelectItem>
            <SelectItem value="cross-cluster">To another cluster</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {/* What to move (intra-cluster only) */}
      {migrationType === "intra-cluster" && (
        <div className="space-y-2">
          <Label>What to move</Label>
          <Select
            value={migrationMode}
            onValueChange={(v) => {
              setMigrationMode(v as MigrationMode);
            }}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="live">The guests, to another node</SelectItem>
              <SelectItem value="storage">
                Their disks, to another storage
              </SelectItem>
              <SelectItem value="both">The guests and their disks</SelectItem>
            </SelectContent>
          </Select>
          <p className="text-[11px] text-muted-foreground">
            {migrationMode === "live" &&
              "Moves the guests to a different node."}
            {migrationMode === "storage" &&
              "Moves all of the guests' disks to a different storage on the same node."}
            {migrationMode === "both" &&
              "Moves the guests to another node and all of their disks to a different storage."}
          </p>
        </div>
      )}

      {/* Target Cluster (cross-cluster) */}
      {migrationType === "cross-cluster" && (
        <div className="space-y-2">
          <Label>Target cluster</Label>
          <Select
            value={targetClusterId}
            onValueChange={(v) => {
              setTargetClusterId(v);
            }}
          >
            <SelectTrigger>
              <SelectValue placeholder="Select target cluster" />
            </SelectTrigger>
            <SelectContent>
              {clusters
                ?.filter((c) => c.id !== clusterId)
                .map((c) => (
                  <SelectItem key={c.id} value={c.id}>
                    {c.name}
                  </SelectItem>
                ))}
            </SelectContent>
          </Select>
        </div>
      )}

      {/* Target Node */}
      {!(migrationType === "intra-cluster" && migrationMode === "storage") && (
        <div className="space-y-2">
          <Label>Target node</Label>
          <Select value={targetNode} onValueChange={setTargetNode}>
            <SelectTrigger>
              <SelectValue placeholder="Select target node" />
            </SelectTrigger>
            <SelectContent>
              {availableTargetNodes?.map((n) => {
                const live = clusterMetrics?.nodeMetrics.get(n.id);
                const cpuLabel = live
                  ? `${String(Math.round(live.cpuPercent))}%`
                  : null;
                const memLabel = live
                  ? `${String(Math.round(live.memPercent))}%`
                  : null;
                return (
                  <SelectItem key={n.id} value={n.name}>
                    <span className="flex items-center gap-2">
                      {n.name}
                      {n.status !== "online" && (
                        <span className="text-muted-foreground">
                          ({n.status})
                        </span>
                      )}
                      {cpuLabel && memLabel && (
                        <span className="text-[10px] text-muted-foreground">
                          CPU {cpuLabel} · Mem {memLabel}
                        </span>
                      )}
                    </span>
                  </SelectItem>
                );
              })}
            </SelectContent>
          </Select>
          <p className="text-[11px] text-muted-foreground">
            The least loaded node is picked for you.
          </p>
        </div>
      )}

      {/* Target Storage (intra-cluster storage/both) */}
      {migrationType === "intra-cluster" &&
        (migrationMode === "storage" || migrationMode === "both") && (
          <div className="space-y-2">
            <Label>Target storage</Label>
            <Select value={targetStorage} onValueChange={setTargetStorage}>
              <SelectTrigger>
                <SelectValue placeholder="Select target storage" />
              </SelectTrigger>
              <SelectContent>
                {filteredTargetStorage.map((s) => (
                  <SelectItem key={s.storage} value={s.storage}>
                    {s.storage} ({s.type})
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="text-[11px] text-muted-foreground">
              All of the guests' disks are moved to this storage.
            </p>
          </div>
        )}

      {/* Storage Mapping (cross-cluster) */}
      {migrationType === "cross-cluster" &&
        targetClusterId.length > 0 &&
        uniqueSourceStorage.length > 0 && (
          <div className="space-y-2">
            <Label>Storage mapping</Label>
            <div className="space-y-2 rounded-md border p-3">
              {uniqueSourceStorage.map((src) => (
                <div
                  key={src.storage}
                  className="grid grid-cols-[1fr_auto_1fr] items-center gap-2"
                >
                  <span className="truncate text-sm">{src.storage}</span>
                  <ArrowLeftRight className="h-3 w-3 text-muted-foreground" />
                  <Select
                    value={storageMap[src.storage] ?? ""}
                    onValueChange={(v) => {
                      setStorageMap((prev) => ({
                        ...prev,
                        [src.storage]: v,
                      }));
                    }}
                  >
                    <SelectTrigger className="h-8 text-xs">
                      <SelectValue placeholder="Same name" />
                    </SelectTrigger>
                    <SelectContent>
                      {uniqueTargetStorage.map((tgt) => (
                        <SelectItem key={tgt.storage} value={tgt.storage}>
                          {tgt.storage} ({tgt.type})
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              ))}
            </div>
          </div>
        )}

      {/* Network Mapping (cross-cluster) */}
      {migrationType === "cross-cluster" &&
        targetClusterId.length > 0 &&
        targetNode.length > 0 &&
        sourceBridges &&
        sourceBridges.length > 0 && (
          <div className="space-y-2">
            <Label>Network mapping</Label>
            <div className="space-y-2 rounded-md border p-3">
              {sourceBridges.map((src) => (
                <div
                  key={src.iface}
                  className="grid grid-cols-[1fr_auto_1fr] items-center gap-2"
                >
                  <span className="truncate text-sm">{src.iface}</span>
                  <ArrowLeftRight className="h-3 w-3 text-muted-foreground" />
                  <Select
                    value={networkMap[src.iface] ?? ""}
                    onValueChange={(v) => {
                      setNetworkMap((prev) => ({
                        ...prev,
                        [src.iface]: v,
                      }));
                    }}
                  >
                    <SelectTrigger className="h-8 text-xs">
                      <SelectValue placeholder="Same name" />
                    </SelectTrigger>
                    <SelectContent>
                      {targetBridges?.map((tgt) => (
                        <SelectItem key={tgt.iface} value={tgt.iface}>
                          {tgt.iface}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              ))}
            </div>
          </div>
        )}

      {/* Options */}
      <div className="space-y-3">
        {!(
          migrationType === "intra-cluster" && migrationMode === "storage"
        ) && (
          <div className="flex items-center justify-between">
            <Label>Live migration</Label>
            <Switch checked={online} onCheckedChange={setOnline} />
          </div>
        )}
        <DiskMoveOptions
          idPrefix="bulk-migrate"
          hideFormat={!showFormat}
          targetStorageType={formatTargetType}
          format={diskFormat}
          onFormatChange={setDiskFormat}
          bwlimit={bwlimit}
          onBwlimitChange={setBwlimit}
          deleteSource={deleteSource}
          onDeleteSourceChange={setDeleteSource}
          deleteSourceLabel="Delete source after migration"
          hideDeleteSource={!showDeleteSource}
          {...(movesStorage
            ? {
                keptHint:
                  "Source volumes are kept as unused disks on each guest.",
              }
            : {})}
        />
      </div>

      {/* Resource list */}
      <div className="rounded border bg-muted/30 p-3">
        <p className="mb-1 text-xs font-medium text-muted-foreground">
          Resources to migrate:
        </p>
        <div className="max-h-32 space-y-0.5 overflow-y-auto text-sm">
          {rows.map((r) => (
            <div key={r.original.key} className="flex gap-2">
              <span className="font-mono text-xs text-muted-foreground">
                {r.original.type.toUpperCase()} {String(r.original.vmid)}
              </span>
              <span>{r.original.name}</span>
              <span className="text-xs text-muted-foreground">
                ({r.original.nodeName})
              </span>
            </div>
          ))}
        </div>
      </div>

      <DialogFooter className="gap-2 sm:gap-0">
        <Button
          type="button"
          variant="outline"
          onClick={onCancel}
          disabled={isCreating}
        >
          Cancel
        </Button>
        <Button onClick={onSubmit} disabled={!isFormValid || isCreating}>
          {isCreating ? (
            <>
              <Loader2 className="mr-2 h-4 w-4 animate-spin" />
              Creating jobs and running checks...
            </>
          ) : (
            "Run pre-flight checks"
          )}
        </Button>
      </DialogFooter>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Per-job progress row (polls its own migration job)
// ---------------------------------------------------------------------------

function BulkJobProgress({
  jobId,
  vmName,
  vmid,
  vmType,
}: {
  jobId: string;
  vmName: string;
  vmid: number;
  vmType: string;
}) {
  const { data: job } = useMigrationJob(jobId);

  const status = job?.status ?? "pending";
  const progress = job?.progress ?? 0;

  return (
    <div className="space-y-1 rounded-md border p-3">
      <div className="flex items-center justify-between">
        <span className="text-sm font-medium">
          {vmName} ({vmType.toUpperCase()} {String(vmid)})
        </span>
        <span className="text-[12.5px]">
          <MigrationStatusText status={status} />
        </span>
      </div>
      {/* A sliver even at 0%, so a job that just started is not an empty
          track. */}
      {(status === "pending" || status === "migrating") && (
        <UsageBar percent={Math.max(progress * 100, 5)} progress />
      )}
      {status === "failed" && job?.error_message && (
        <p className="text-xs text-destructive">{job.error_message}</p>
      )}
    </div>
  );
}
