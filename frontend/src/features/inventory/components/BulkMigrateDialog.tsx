import { useState, useMemo, useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Badge } from "@/components/ui/badge";
import { Slider } from "@/components/ui/slider";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
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
import type { InventoryRow } from "../types/inventory";
import type { Row } from "@tanstack/react-table";
import {
  ArrowLeftRight,
  CheckCircle2,
  XCircle,
  Loader2,
} from "lucide-react";

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
  const [step, setStep] = useState<WizardStep>("config");

  // Form state
  const [migrationType, setMigrationType] =
    useState<MigrationType>("intra-cluster");
  const [migrationMode, setMigrationMode] = useState<MigrationMode>("live");
  const [targetClusterId, setTargetClusterId] = useState("");
  const [targetNode, setTargetNode] = useState("");
  const [targetStorage, setTargetStorage] = useState("");
  const [online, setOnline] = useState(true);
  const [bwlimit, setBwlimit] = useState([0]);
  const [deleteSource, setDeleteSource] = useState(false);
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
    ? Array.from(
        new Map(targetStorageList.map((s) => [s.storage, s])).values(),
      )
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

  // Form validation
  const isFormValid = (() => {
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
    setBwlimit([0]);
    setDeleteSource(false);
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
          target_node:
            effectiveMode === "storage" ? r.nodeName : targetNode,
          vmid: r.vmid ?? 0,
          vm_type: vmType,
          migration_type: migrationType,
          migration_mode: effectiveMode,
          storage_map: migrationType === "cross-cluster" ? storageMap : {},
          network_map: migrationType === "cross-cluster" ? networkMap : {},
          online,
          bwlimit_kib: bwlimit[0] ?? 0,
          delete_source: deleteSource,
          target_vmid: 0,
          target_storage:
            effectiveMode === "storage" || effectiveMode === "both"
              ? targetStorage
              : "",
        };

        const created = await createMutation.mutateAsync(req);
        const report = await checkMutation.mutateAsync(created.id);
        return { name: r.name, vmid: r.vmid, type: r.type, node: r.nodeName, jobId: created.id, passed: report.passed };
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

  const bwlimitValue = bwlimit[0] ?? 0;
  const passingCount = jobs.filter((j) => j.preflightPassed).length;
  const failingCount = jobs.filter((j) => j.preflightPassed === false).length;

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
            {step === "config" && "Bulk Migrate"}
            {step === "preflight" && "Pre-Flight Checks"}
            {step === "progress" && "Migration Progress"}
          </DialogTitle>
          <DialogDescription>
            Migrate {String(rows.length)} resource
            {rows.length !== 1 ? "s" : ""}
          </DialogDescription>
        </DialogHeader>

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
            bwlimitValue={bwlimitValue}
            deleteSource={deleteSource}
            setDeleteSource={setDeleteSource}
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
                    <CheckCircle2 className="h-4 w-4 shrink-0 text-green-500" />
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
                  Execute {String(passingCount)} Migration
                  {passingCount !== 1 ? "s" : ""}
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
                    <Badge
                      variant="outline"
                      className="ml-auto bg-red-100 text-red-700"
                    >
                      skipped
                    </Badge>
                  </div>
                ))}
            </div>

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
  bwlimit: number[];
  setBwlimit: (v: number[]) => void;
  bwlimitValue: number;
  deleteSource: boolean;
  setDeleteSource: (v: boolean) => void;
  storageMap: Record<string, string>;
  setStorageMap: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  networkMap: Record<string, string>;
  setNetworkMap: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  clusters:
    | Array<{ id: string; name: string }>
    | undefined;
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
  bwlimitValue,
  deleteSource,
  setDeleteSource,
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
}: ConfigStepProps) {
  return (
    <div className="space-y-4">
      {/* Migration Type */}
      <div className="space-y-2">
        <Label>Migration Type</Label>
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
            <SelectItem value="intra-cluster">
              Intra-Cluster (same cluster)
            </SelectItem>
            <SelectItem value="cross-cluster">
              Cross-Cluster (different clusters)
            </SelectItem>
          </SelectContent>
        </Select>
      </div>

      {/* Migration Mode (intra-cluster only) */}
      {migrationType === "intra-cluster" && (
        <div className="space-y-2">
          <Label>Migration Mode</Label>
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
              <SelectItem value="live">
                Live (move to another node)
              </SelectItem>
              <SelectItem value="storage">
                Storage (move disks to another storage)
              </SelectItem>
              <SelectItem value="both">
                Both (move VM + move disks)
              </SelectItem>
            </SelectContent>
          </Select>
          <p className="text-[11px] text-muted-foreground">
            {migrationMode === "live" &&
              "Moves VMs to a different node (memory migration)"}
            {migrationMode === "storage" &&
              "Moves all VM disks to a different storage on the same node"}
            {migrationMode === "both" &&
              "Moves VMs to another node and all disks to a different storage"}
          </p>
        </div>
      )}

      {/* Target Cluster (cross-cluster) */}
      {migrationType === "cross-cluster" && (
        <div className="space-y-2">
          <Label>Target Cluster</Label>
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
      {!(
        migrationType === "intra-cluster" && migrationMode === "storage"
      ) && (
        <div className="space-y-2">
          <Label>Target Node</Label>
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
            Auto-selects the least loaded node
          </p>
        </div>
      )}

      {/* Target Storage (intra-cluster storage/both) */}
      {migrationType === "intra-cluster" &&
        (migrationMode === "storage" || migrationMode === "both") && (
          <div className="space-y-2">
            <Label>Target Storage</Label>
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
              All VM disks will be moved to this storage
            </p>
          </div>
        )}

      {/* Storage Mapping (cross-cluster) */}
      {migrationType === "cross-cluster" &&
        targetClusterId.length > 0 &&
        uniqueSourceStorage.length > 0 && (
          <div className="space-y-2">
            <Label>Storage Mapping</Label>
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
            <Label>Network Mapping</Label>
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
            <Label>Live Migration</Label>
            <Switch checked={online} onCheckedChange={setOnline} />
          </div>
        )}
        {migrationType === "cross-cluster" && (
          <div className="flex items-center justify-between">
            <Label>Delete Source After Migration</Label>
            <Switch
              checked={deleteSource}
              onCheckedChange={setDeleteSource}
            />
          </div>
        )}
        <div className="space-y-2">
          <Label>
            Bandwidth Limit:{" "}
            {bwlimitValue === 0
              ? "Unlimited"
              : `${String(bwlimitValue)} KiB/s`}
          </Label>
          <Slider
            value={bwlimit}
            onValueChange={setBwlimit}
            min={0}
            max={1048576}
            step={1024}
          />
        </div>
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

      <Button
        onClick={onSubmit}
        disabled={!isFormValid || isCreating}
        className="w-full"
      >
        {isCreating ? (
          <>
            <Loader2 className="mr-2 h-4 w-4 animate-spin" />
            Creating Jobs & Running Checks...
          </>
        ) : (
          "Create & Run Pre-Flight Checks"
        )}
      </Button>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Per-job progress row (polls its own migration job)
// ---------------------------------------------------------------------------

const statusColors: Record<string, string> = {
  pending: "bg-gray-100 text-gray-700",
  checking: "bg-blue-100 text-blue-700",
  migrating: "bg-yellow-100 text-yellow-700",
  completed: "bg-green-100 text-green-700",
  failed: "bg-red-100 text-red-700",
  cancelled: "bg-gray-100 text-gray-500",
};

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
        <Badge
          variant="outline"
          className={statusColors[status] ?? ""}
        >
          {status}
        </Badge>
      </div>
      {(status === "pending" || status === "migrating") && (
        <div className="h-1.5 overflow-hidden rounded-full bg-muted">
          <div
            className="h-full bg-primary transition-all"
            style={{
              width: `${String(Math.max(progress * 100, 5))}%`,
            }}
          />
        </div>
      )}
      {status === "failed" && job?.error_message && (
        <p className="text-xs text-destructive">{job.error_message}</p>
      )}
    </div>
  );
}
