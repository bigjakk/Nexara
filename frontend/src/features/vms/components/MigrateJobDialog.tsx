import { useState, useEffect, useRef, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
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
import type { TaskLogLine } from "../api/vm-queries";
import type {
  CreateMigrationRequest,
  MigrationType,
  MigrationMode,
  VMType,
  CheckSeverity,
} from "@/features/migrations/types/migration";
import type { ResourceKind } from "../types/vm";
import { useTaskLogStore } from "@/stores/task-log-store";
import {
  ArrowLeftRight,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  Loader2,
} from "lucide-react";

type WizardStep = "config" | "preflight" | "progress";

const severityIcon: Record<CheckSeverity, React.ReactNode> = {
  pass: <CheckCircle2 className="h-4 w-4 text-green-500" />,
  warn: <AlertTriangle className="h-4 w-4 text-yellow-500" />,
  fail: <XCircle className="h-4 w-4 text-red-500" />,
};

const statusColors: Record<string, string> = {
  pending: "bg-gray-100 text-gray-700",
  checking: "bg-blue-100 text-blue-700",
  migrating: "bg-yellow-100 text-yellow-700",
  completed: "bg-green-100 text-green-700",
  failed: "bg-red-100 text-red-700",
  cancelled: "bg-gray-100 text-gray-500",
};

interface MigrateJobDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
  vmid: number;
  vmName: string;
  kind: ResourceKind;
  currentNode: string;
  status: string;
}

export function MigrateJobDialog({
  open,
  onOpenChange,
  clusterId,
  vmid,
  vmName,
  kind,
  currentNode,
  status,
}: MigrateJobDialogProps) {
  const isRunning = status.toLowerCase() === "running";
  const [step, setStep] = useState<WizardStep>("config");
  const [jobId, setJobId] = useState("");

  // Form state
  const [migrationType, setMigrationType] =
    useState<MigrationType>("intra-cluster");
  const [migrationMode, setMigrationMode] = useState<MigrationMode>("live");
  const [targetClusterId, setTargetClusterId] = useState("");
  const [targetNode, setTargetNode] = useState("");
  const [targetStorage, setTargetStorage] = useState("");
  const [online, setOnline] = useState(isRunning);
  const [bwlimit, setBwlimit] = useState([0]);
  const [deleteSource, setDeleteSource] = useState(false);
  const [targetVmid, setTargetVmid] = useState("");
  const [storageMap, setStorageMap] = useState<Record<string, string>>({});
  const [networkMap, setNetworkMap] = useState<Record<string, string>>({});

  // Data hooks
  const { data: clusters } = useClusters();
  const effectiveTargetClusterId =
    migrationType === "cross-cluster" ? targetClusterId : clusterId;
  const { data: targetNodes } = useClusterNodes(effectiveTargetClusterId);
  const { data: sourceStorage } = useClusterStorage(clusterId);
  const needsStorageList =
    migrationType === "cross-cluster" ||
    (migrationMode === "storage" || migrationMode === "both");
  const { data: targetStorageList } = useClusterStorage(
    migrationType === "cross-cluster"
      ? targetClusterId
      : needsStorageList
        ? clusterId
        : "",
  );
  const { data: sourceBridges } = useNodeBridges(
    migrationType === "cross-cluster" ? clusterId : "",
    migrationType === "cross-cluster" ? currentNode : "",
  );
  const { data: targetBridges } = useNodeBridges(
    migrationType === "cross-cluster" ? targetClusterId : "",
    migrationType === "cross-cluster" ? targetNode : "",
  );

  const queryClient = useQueryClient();
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);

  // Mutation hooks
  const createMutation = useCreateMigration();
  const checkMutation = useRunPreFlightCheck();
  const executeMutation = useExecuteMigration();
  const { data: job } = useMigrationJob(jobId);

  const vmType: VMType = kind === "ct" ? "lxc" : "qemu";

  // Invalidate VM/node caches when migration completes or fails so the
  // VM detail page reflects the new node and doesn't show "not found".
  const prevJobStatus = useRef<string | undefined>(undefined);
  useEffect(() => {
    const status = job?.status;
    if (
      prevJobStatus.current !== status &&
      (status === "completed" || status === "failed")
    ) {
      void queryClient.invalidateQueries({ queryKey: ["clusters", clusterId] });
      void queryClient.invalidateQueries({ queryKey: ["recent-activity"] });
    }
    prevJobStatus.current = status;
  }, [job?.status, clusterId, queryClient]);

  function resetForm() {
    setStep("config");
    setJobId("");
    setMigrationType("intra-cluster");
    setMigrationMode("live");
    setTargetClusterId("");
    setTargetNode("");
    setTargetStorage("");
    setOnline(isRunning);
    setBwlimit([0]);
    setDeleteSource(false);
    setTargetVmid("");
    setStorageMap({});
    setNetworkMap({});
  }

  function handleClose() {
    // If migration is still running, open the global progress dialog
    // so the user can re-check from the task bar.
    if (step === "progress" && job && job.upid && (job.status === "pending" || job.status === "migrating")) {
      setFocusedTask({
        clusterId,
        upid: job.upid,
        description: `Migrate ${vmName} (VMID ${String(vmid)})`,
      });
    }
    resetForm();
    createMutation.reset();
    checkMutation.reset();
    executeMutation.reset();
    onOpenChange(false);
  }

  function handleCreate() {
    const effectiveMode =
      migrationType === "intra-cluster" ? migrationMode : "live";
    const req: CreateMigrationRequest = {
      source_cluster_id: clusterId,
      target_cluster_id:
        migrationType === "intra-cluster" ? clusterId : targetClusterId,
      source_node: currentNode,
      target_node:
        effectiveMode === "storage" ? currentNode : targetNode,
      vmid,
      vm_type: vmType,
      migration_type: migrationType,
      migration_mode: effectiveMode,
      storage_map: migrationType === "cross-cluster" ? storageMap : {},
      network_map: migrationType === "cross-cluster" ? networkMap : {},
      online,
      bwlimit_kib: bwlimit[0] ?? 0,
      delete_source: deleteSource,
      target_vmid: targetVmid ? parseInt(targetVmid, 10) : 0,
      target_storage:
        effectiveMode === "storage" || effectiveMode === "both"
          ? targetStorage
          : "",
    };

    void createMutation.mutateAsync(req).then((created) => {
      setJobId(created.id);
      void checkMutation.mutateAsync(created.id).then(() => {
        setStep("preflight");
      });
    });
  }

  function handleExecute() {
    void executeMutation.mutateAsync(jobId).then(() => {
      setStep("progress");
      // Trigger task panel to pick up the new task_history entry from the backend.
      void queryClient.invalidateQueries({ queryKey: ["recent-activity"] });
    });
  }

  function updateStorageMapping(sourcePool: string, targetPool: string) {
    setStorageMap((prev) => ({ ...prev, [sourcePool]: targetPool }));
  }

  function updateNetworkMapping(sourceBridge: string, targetBridge: string) {
    setNetworkMap((prev) => ({ ...prev, [sourceBridge]: targetBridge }));
  }

  // Filter out current node for intra-cluster migration
  const availableTargetNodes =
    migrationType === "intra-cluster"
      ? targetNodes?.filter((n) => n.name !== currentNode)
      : targetNodes;

  // Auto-select the best node (lowest combined CPU + memory + VM count).
  const clusterMetrics = useClusterMetrics(effectiveTargetClusterId);
  const { data: clusterVMs } = useClusterVMs(effectiveTargetClusterId);

  // Build a VM count per node_id (DB UUID) for scoring
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

  useEffect(() => {
    if (!availableTargetNodes || availableTargetNodes.length === 0) return;
    // Only auto-select when no node is chosen yet
    if (targetNode.length > 0) return;

    const nodeMetrics = clusterMetrics?.nodeMetrics;

    // Score each online node — lower is better (less loaded).
    // VM count is used as the primary signal (nodes with fewer VMs are preferred),
    // with live CPU+mem as a tiebreaker.
    let bestNode = "";
    let bestScore = Infinity;

    for (const node of availableTargetNodes) {
      if (node.status !== "online") continue;

      const live = nodeMetrics?.get(node.id);
      // If no live metrics, assume idle (0%) — don't penalize nodes missing metrics
      const cpu = live?.cpuPercent ?? 0;
      const mem = live?.memPercent ?? 0;
      const vms = vmCountByNodeId.get(node.id) ?? 0;
      // VM count * 100 dominates, CPU+mem (0-200 range) breaks ties
      const score = vms * 100 + cpu + mem;

      if (score < bestScore) {
        bestScore = score;
        bestNode = node.name;
      }
    }

    // Fallback: just pick the first online node if no metrics
    if (bestNode === "") {
      const firstOnline = availableTargetNodes.find((n) => n.status === "online");
      if (firstOnline) bestNode = firstOnline.name;
    }

    if (bestNode !== "") {
      setTargetNode(bestNode);
    }
  }, [availableTargetNodes, clusterMetrics?.nodeMetrics, targetNode.length, vmCountByNodeId]);

  // Deduplicate storage pools by name
  const uniqueSourceStorage = sourceStorage
    ? Array.from(
        new Map(sourceStorage.map((s) => [s.storage, s])).values(),
      )
    : [];
  const uniqueTargetStorage = targetStorageList
    ? Array.from(
        new Map(targetStorageList.map((s) => [s.storage, s])).values(),
      )
    : [];

  const isFormValid = (() => {
    if (migrationType === "cross-cluster") {
      return targetNode.length > 0 && targetClusterId.length > 0;
    }
    // Intra-cluster
    if (migrationMode === "storage") {
      return targetStorage.length > 0;
    }
    if (migrationMode === "both") {
      return targetNode.length > 0 && targetStorage.length > 0;
    }
    // Live mode
    return targetNode.length > 0;
  })();

  const bwlimitValue = bwlimit[0] ?? 0;

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v) handleClose(); }}>
      <DialogContent className="max-w-xl overflow-hidden">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <ArrowLeftRight className="h-5 w-5" />
            {step === "config" && "Migrate"}
            {step === "preflight" && "Pre-Flight Checks"}
            {step === "progress" && "Migration Progress"}
          </DialogTitle>
          <DialogDescription>
            Migrate <strong>{vmName}</strong> (VMID {String(vmid)}) from{" "}
            <strong>{currentNode}</strong>
          </DialogDescription>
        </DialogHeader>

        {step === "config" && (
          <div className="space-y-4">
            {/* Migration Type */}
            <div className="space-y-2">
              <Label>Migration Type</Label>
              <Select
                value={migrationType}
                onValueChange={(v) => {
                  setMigrationType(v as MigrationType);
                  setTargetNode("");
                  setTargetClusterId("");
                  setStorageMap({});
                  setNetworkMap({});
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
                    setTargetStorage("");
                    if (v === "storage") {
                      setTargetNode("");
                    }
                  }}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="live">
                      Live (move VM to another node)
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
                    "Moves the VM to a different node (memory migration)"}
                  {migrationMode === "storage" &&
                    "Moves all VM disks to a different storage on the same node"}
                  {migrationMode === "both" &&
                    "Moves VM to another node and all disks to a different storage"}
                </p>
              </div>
            )}

            {/* Target Cluster (cross-cluster only) */}
            {migrationType === "cross-cluster" && (
              <div className="space-y-2">
                <Label>Target Cluster</Label>
                <Select
                  value={targetClusterId}
                  onValueChange={(v) => {
                    setTargetClusterId(v);
                    setTargetNode("");
                    setStorageMap({});
                    setNetworkMap({});
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

            {/* Target Node (hidden for storage-only mode) */}
            {!(migrationType === "intra-cluster" && migrationMode === "storage") && (
            <div className="space-y-2">
              <Label>Target Node</Label>
              <Select value={targetNode} onValueChange={setTargetNode}>
                <SelectTrigger>
                  <SelectValue placeholder="Select target node" />
                </SelectTrigger>
                <SelectContent>
                  {availableTargetNodes?.map((n) => {
                    const live = clusterMetrics?.nodeMetrics.get(n.id);
                    const cpuLabel = live ? `${String(Math.round(live.cpuPercent))}%` : null;
                    const memLabel = live ? `${String(Math.round(live.memPercent))}%` : null;
                    return (
                      <SelectItem key={n.id} value={n.name}>
                        <span className="flex items-center gap-2">
                          {n.name}
                          {n.status !== "online" && (
                            <span className="text-muted-foreground">({n.status})</span>
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

            {/* Target Storage (storage/both mode for intra-cluster) */}
            {migrationType === "intra-cluster" &&
              (migrationMode === "storage" || migrationMode === "both") && (
                <div className="space-y-2">
                  <Label>Target Storage</Label>
                  <Select value={targetStorage} onValueChange={setTargetStorage}>
                    <SelectTrigger>
                      <SelectValue placeholder="Select target storage" />
                    </SelectTrigger>
                    <SelectContent>
                      {uniqueTargetStorage
                        .filter((s) => {
                          if (!s.active || !s.enabled) return false;
                          const contentType = vmType === "lxc" ? "rootdir" : "images";
                          return s.content.includes(contentType);
                        })
                        .map((s) => (
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

            {/* Storage Mapping (cross-cluster only) */}
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
                            updateStorageMapping(src.storage, v);
                          }}
                        >
                          <SelectTrigger className="h-8 text-xs">
                            <SelectValue placeholder="Same name" />
                          </SelectTrigger>
                          <SelectContent>
                            {uniqueTargetStorage.map((tgt) => (
                              <SelectItem
                                key={tgt.storage}
                                value={tgt.storage}
                              >
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

            {/* Network Mapping (cross-cluster only) */}
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
                            updateNetworkMapping(src.iface, v);
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

            {/* Target VMID (cross-cluster only) */}
            {migrationType === "cross-cluster" && (
              <div className="space-y-2">
                <Label>Target VMID (optional)</Label>
                <Input
                  type="number"
                  value={targetVmid}
                  onChange={(e) => { setTargetVmid(e.target.value); }}
                  placeholder="Auto"
                />
              </div>
            )}

            {/* Options */}
            <div className="space-y-3">
              {!(migrationType === "intra-cluster" && migrationMode === "storage") && (
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

            {(createMutation.isError || checkMutation.isError) && (
              <p className="text-sm text-destructive">
                {createMutation.error?.message ?? checkMutation.error?.message}
              </p>
            )}

            <Button
              onClick={handleCreate}
              disabled={!isFormValid || createMutation.isPending}
              className="w-full"
            >
              {createMutation.isPending || checkMutation.isPending ? (
                <>
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                  Running Pre-Flight Checks...
                </>
              ) : (
                "Create & Run Pre-Flight Checks"
              )}
            </Button>
          </div>
        )}

        {step === "preflight" && job && (
          <div className="space-y-4">
            {job.check_results ? (
              <>
                <div className="space-y-2">
                  {job.check_results.checks.map((check, i) => (
                    <div
                      key={i}
                      className="flex items-start gap-2 rounded-md border p-3"
                    >
                      {severityIcon[check.severity]}
                      <div>
                        <p className="text-sm font-medium">{check.name}</p>
                        <p className="text-xs text-muted-foreground">
                          {check.message}
                        </p>
                      </div>
                    </div>
                  ))}
                </div>
                <div className="flex gap-2">
                  {job.check_results.passed ? (
                    <Button
                      onClick={handleExecute}
                      disabled={executeMutation.isPending}
                      className="flex-1"
                    >
                      {executeMutation.isPending ? (
                        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                      ) : null}
                      Execute Migration
                    </Button>
                  ) : (
                    <p className="text-sm text-red-500">
                      Pre-flight checks failed. Fix issues and try again.
                    </p>
                  )}
                  <Button variant="outline" onClick={handleClose}>
                    Close
                  </Button>
                </div>
              </>
            ) : (
              <div className="flex items-center gap-2">
                <Loader2 className="h-4 w-4 animate-spin" />
                <p className="text-sm">Running checks...</p>
              </div>
            )}
          </div>
        )}

        {step === "progress" && job && (
          <MigrationProgress
            job={job}
            clusterId={clusterId}
            onClose={handleClose}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

/** Progress step with live task log showing speed/throughput. */
function MigrationProgress({
  job,
  clusterId,
  onClose,
}: {
  job: { status: string; progress: number; upid: string; error_message: string; migration_mode: string; target_storage: string };
  clusterId: string;
  onClose: () => void;
}) {
  const isActive = job.status === "pending" || job.status === "migrating";
  const hasUpid = job.upid.length > 0;
  const { data: logLines } = useQuery({
    queryKey: ["migration-log", clusterId, job.upid],
    queryFn: () =>
      apiClient.get<TaskLogLine[]>(
        `/api/v1/clusters/${clusterId}/tasks/${encodeURIComponent(job.upid)}/log`,
      ),
    enabled: hasUpid,
    refetchInterval: isActive ? 3000 : false,
  });

  // Extract speed/throughput from log lines (Proxmox includes lines like
  // "transferred 1.23 GiB in 45s (28.0 MiB/s)" or similar).
  const speedLines = logLines
    ?.map((l) => l.t)
    .filter((t) => /\b(MiB\/s|GiB\/s|KiB\/s|MB\/s|GB\/s|transferred)\b/i.test(t));
  const speedLine = speedLines && speedLines.length > 0
    ? speedLines[speedLines.length - 1]
    : undefined;

  const isStorageMode = job.migration_mode === "storage" || job.migration_mode === "both";

  function progressLabel(): string {
    if (job.status === "pending") return "Starting migration...";
    if (job.migration_mode === "storage") {
      return `Moving disks to ${job.target_storage}... (${String(Math.round(job.progress * 100))}%)`;
    }
    if (job.migration_mode === "both") {
      if (job.progress < 0.5) {
        return "Phase 1: Live migration in progress...";
      }
      return `Phase 2: Moving disks to ${job.target_storage}... (${String(Math.round(job.progress * 100))}%)`;
    }
    return "Migration in progress...";
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <span className="text-sm font-medium">Status</span>
        <Badge
          variant="outline"
          className={statusColors[job.status] ?? ""}
        >
          {job.status}
        </Badge>
      </div>

      {isActive && (
        <div className="space-y-2">
          <div className="h-2 overflow-hidden rounded-full bg-muted">
            <div
              className="h-full bg-primary transition-all"
              style={{
                width: `${String(Math.max(job.progress * 100, 5))}%`,
              }}
            />
          </div>
          <p className="text-center text-xs text-muted-foreground">
            {progressLabel()}
          </p>
          {speedLine && (
            <p className="text-center text-xs font-medium text-blue-600 dark:text-blue-400 break-all">
              {speedLine}
            </p>
          )}
        </div>
      )}

      {job.status === "completed" && (
        <p className="text-sm text-green-600">
          {isStorageMode
            ? "Storage migration completed successfully."
            : "Migration completed successfully."}
        </p>
      )}
      {job.status === "failed" && (
        <p className="text-sm text-red-500">
          Migration failed: {job.error_message}
        </p>
      )}

      {/* Task Log (only for real Proxmox UPIDs) */}
      {logLines && logLines.length > 0 && (
        <div className="space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Task Log</span>
          <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-all rounded bg-muted/50 p-2 font-mono text-[11px] leading-relaxed">
            {logLines.map((line) => line.t).join("\n")}
          </pre>
        </div>
      )}

      <Button variant="outline" onClick={onClose}>
        Close
      </Button>
    </div>
  );
}
