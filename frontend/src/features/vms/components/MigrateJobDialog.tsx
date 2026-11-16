import { useState, useEffect, useRef, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ViewMigrationsLink } from "@/features/migrations/components/ViewMigrationsLink";
import { toast } from "sonner";
import { apiClient, sessionScope } from "@/lib/api-client";
import { describeError } from "@/lib/api-error";
import { apiPath } from "@/lib/api-path";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Badge, type BadgeVariant } from "@/components/ui/badge";
import { Field, FormSection } from "@/components/page/Field";
import { WarningCallout } from "@/components/WarningCallout";
import { cn } from "@/lib/utils";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { TaskLogSection } from "@/components/TaskLogSection";
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
import { movesStoppedHAGuest } from "@/features/migrations/lib/ha-migrate";
import { useVMConfig, useContainerConfig } from "../api/vm-queries";
import { parseDisk } from "../lib/vm-config-parsers";
import type { TaskLogLine } from "@/features/tasks/api/tasks-queries";
import type {
  CreateMigrationRequest,
  MigrationType,
  MigrationMode,
  VMType,
  CheckSeverity,
} from "@/features/migrations/types/migration";
import type { ResourceKind } from "../types/vm";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import {
  ArrowRight,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  Loader2,
} from "lucide-react";

type WizardStep = "config" | "preflight" | "progress";

const severityIcon: Record<CheckSeverity, React.ReactNode> = {
  pass: <CheckCircle2 className="h-4 w-4 text-emerald-500" />,
  warn: <AlertTriangle className="h-4 w-4 text-amber-500" />,
  fail: <XCircle className="h-4 w-4 text-red-500" />,
};

/** How each job state reads: work under way in blue, done is quiet. */
const statusBadge: Record<string, BadgeVariant> = {
  pending: "secondary",
  checking: "info",
  migrating: "info",
  completed: "healthy",
  failed: "destructive",
  cancelled: "outline",
};

interface MigrateJobDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
  resourceId: string;
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
  resourceId,
  vmid,
  vmName,
  kind,
  currentNode,
  status,
}: MigrateJobDialogProps) {
  const { t } = useTranslation("vms");
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
  const [bwlimit, setBwlimit] = useState("");
  // null = untouched (use the disks' current format where the target allows).
  const [diskFormat, setDiskFormat] = useState<string | null>(null);
  // null = follow the per-mode default in `deleteSource` below.
  const [deleteSourceOverride, setDeleteSourceOverride] = useState<
    boolean | null
  >(null);
  const [targetVmid, setTargetVmid] = useState("");
  const [storageMap, setStorageMap] = useState<Record<string, string>>({});
  const [networkMap, setNetworkMap] = useState<Record<string, string>>({});
  // Per-disk storage targets (disk key -> target storage) for intra-cluster
  // storage migration when disks span multiple storages. "" means keep in place.
  const [diskTargets, setDiskTargets] = useState<Record<string, string>>({});

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
  const { data: sourceBridges } = useNodeBridges(
    migrationType === "cross-cluster" ? clusterId : "",
    migrationType === "cross-cluster" ? currentNode : "",
  );
  const { data: targetBridges } = useNodeBridges(
    migrationType === "cross-cluster" ? targetClusterId : "",
    migrationType === "cross-cluster" ? targetNode : "",
  );

  // For storage/both modes, fetch the guest config so we can exclude the
  // storage(s) its disks already sit on. A storage migration moves EVERY disk
  // to the chosen target, and Proxmox rejects moving a disk onto the storage it
  // already lives on, so any currently-used storage is an invalid target —
  // including each one when a guest's disks span multiple storages.
  const needsDiskPlacement =
    migrationType === "intra-cluster" &&
    (migrationMode === "storage" || migrationMode === "both");

  // Moving storage implies freeing the source, so default it on there. Across
  // clusters "delete source" destroys the whole source guest, so it stays
  // opt-in. With it off Proxmox keeps each source volume as an unused disk.
  const deleteSource = deleteSourceOverride ?? needsDiskPlacement;
  // Only meaningful where something is actually left behind: a cross-cluster
  // migration (where it destroys the source guest) or an intra-cluster move
  // that relocates disks. A same-cluster node migration leaves no copy, and
  // executeIntraCluster never reads the flag.
  const showDeleteSource =
    migrationType === "cross-cluster" || needsDiskPlacement;
  const { data: vmConfig } = useVMConfig(
    needsDiskPlacement && kind !== "ct" ? clusterId : "",
    needsDiskPlacement && kind !== "ct" ? resourceId : "",
  );
  const { data: ctConfig } = useContainerConfig(
    needsDiskPlacement && kind === "ct" ? clusterId : "",
    needsDiskPlacement && kind === "ct" ? resourceId : "",
  );
  const guestConfig = kind === "ct" ? ctConfig : vmConfig;

  // The guest's movable disks with their current storage + size. Mirrors the
  // backend's discoverDisks set: QEMU scsi/virtio/sata/ide (skipping cdrom and
  // cloudinit drives); LXC rootfs/mp*. efidisk is intentionally excluded — the
  // backend never moves it.
  const guestDisks = useMemo(() => {
    const out: {
      key: string;
      storage: string;
      size: string;
      format: string;
    }[] = [];
    if (!guestConfig) return out;
    const qemuDiskPrefixes = ["scsi", "virtio", "sata", "ide"];
    for (const [key, val] of Object.entries(guestConfig)) {
      if (typeof val !== "string") continue;
      if (kind === "ct") {
        if (key !== "rootfs" && !key.startsWith("mp")) continue;
      } else {
        if (!qemuDiskPrefixes.some((p) => key.startsWith(p))) continue;
        if (val.includes("media=cdrom") || val.includes("cloudinit")) continue;
      }
      const { storage, size, format } = parseDisk(val);
      if (storage) out.push({ key, storage, size, format: format || "" });
    }
    out.sort((a, b) => a.key.localeCompare(b.key));
    return out;
  }, [guestConfig, kind]);

  const currentDiskStorages = useMemo(
    () => new Set(guestDisks.map((d) => d.storage)),
    [guestDisks],
  );

  // Distinct formats across the guest's disks. One format means the move can
  // preselect it; a mix has no single answer, so the storage default is used.
  const currentDiskFormats = useMemo(
    () => new Set(guestDisks.map((d) => d.format).filter((f) => f !== "")),
    [guestDisks],
  );

  // Per-disk targeting kicks in only when the guest's disks span more than one
  // storage — otherwise the single Target Storage dropdown covers it.
  const perDiskStorage = needsDiskPlacement && currentDiskStorages.size > 1;

  const queryClient = useQueryClient();
  // Mutation hooks
  const createMutation = useCreateMigration(true);
  const checkMutation = useRunPreFlightCheck(true);
  // The check of a job whose create landed after the dialog closed: its own
  // instance, so its state never shows in the dialog when it is reopened.
  const lateCheck = useRunPreFlightCheck(true);
  const settle = useSaveOutcome(open);
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
    setBwlimit("");
    setDiskFormat(null);
    setDeleteSourceOverride(null);
    setTargetVmid("");
    setStorageMap({});
    setNetworkMap({});
    setDiskTargets({});
  }

  function handleClose() {
    resetForm();
    createMutation.reset();
    checkMutation.reset();
    executeMutation.reset();
    onOpenChange(false);
  }

  function handleCreate() {
    const effectiveMode =
      migrationType === "intra-cluster" ? migrationMode : "live";
    // Per-disk targets (disk key -> storage), dropping disks left in place.
    const perDiskMap = Object.fromEntries(
      Object.entries(diskTargets).filter(([, v]) => v !== ""),
    );
    const storageMapPayload =
      migrationType === "cross-cluster"
        ? storageMap
        : perDiskStorage
          ? perDiskMap
          : {};
    const req: CreateMigrationRequest = {
      source_cluster_id: clusterId,
      target_cluster_id:
        migrationType === "intra-cluster" ? clusterId : targetClusterId,
      source_node: currentNode,
      target_node: effectiveMode === "storage" ? currentNode : targetNode,
      vmid,
      vm_type: vmType,
      migration_type: migrationType,
      migration_mode: effectiveMode,
      storage_map: storageMapPayload,
      network_map: migrationType === "cross-cluster" ? networkMap : {},
      online,
      bwlimit_kib: bwlimitKib,
      // Forced off where the control is hidden, so a value left over from a
      // mode the user switched away from cannot ride along unseen.
      delete_source: showDeleteSource && deleteSource,
      disk_format: resolveDiskFormat(
        diskFormat,
        sourceDiskFormat,
        formatTargetType,
      ),
      target_vmid: targetVmid ? parseInt(targetVmid, 10) : 0,
      target_storage:
        (effectiveMode === "storage" || effectiveMode === "both") &&
        !perDiskStorage
          ? targetStorage
          : "",
    };

    // The check is a write to the job the create just made — it sets it to
    // "checking", records the report and moves it on, to pending or to failed —
    // and needs manage:migration, not ownership of the job: sent after the
    // session changed hands it would run as whoever is signed in next, on the
    // previous user's job. useSaveOutcome drops a create that lands after that.
    // Closed before the create answered, the job is still checked, and a
    // failed check is toasted: nothing on screen shows it.
    const ended = sessionScope();
    const checkAction = t("migrationCheckAction", { name: vmName });
    settle(createMutation.mutateAsync(req), {
      action: t("migrationCreateAction", { name: vmName }),
      onSuccess: (created) => {
        setJobId(created.id);
        settle(checkMutation.mutateAsync(created.id), {
          action: checkAction,
          onSuccess: () => {
            setStep("preflight");
          },
          onError: () => undefined, // shown below from the mutation's state
        });
      },
      onLateSuccess: (created) => {
        lateCheck.mutateAsync(created.id).then(undefined, (err: unknown) => {
          if (ended()) return;
          toast.error(
            `${checkAction} failed: ${describeError(err) || "the request failed."}`,
          );
        });
      },
      onError: () => undefined, // shown below from the mutation's state
    });
  }

  function handleExecute() {
    executeMutation.mutateAsync(jobId).then(
      () => {
        setStep("progress");
        // Trigger task panel to pick up the new task_history entry from the backend.
        void queryClient.invalidateQueries({ queryKey: ["recent-activity"] });
      },
      () => undefined, // reported by the app's error toast
    );
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

  // A node move within the cluster of a stopped HA guest: warned, and the
  // execute button says it goes ahead anyway. clusterVMs is this cluster's here.
  const haStopped =
    migrationType === "intra-cluster" &&
    migrationMode !== "storage" &&
    movesStoppedHAGuest(
      status,
      clusterVMs?.find((vm) => vm.id === resourceId)?.ha_state ?? "",
    );
  const haStoppedWarning = haStopped && (
    <WarningCallout title={t("haStoppedMigrateTitle")}>
      <p>{t("haStoppedMigrateBody", { node: currentNode })}</p>
    </WarningCallout>
  );

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
      const firstOnline = availableTargetNodes.find(
        (n) => n.status === "online",
      );
      if (firstOnline) bestNode = firstOnline.name;
    }

    if (bestNode !== "") {
      setTargetNode(bestNode);
    }
  }, [
    availableTargetNodes,
    clusterMetrics?.nodeMetrics,
    targetNode.length,
    vmCountByNodeId,
  ]);

  // Deduplicate storage pools by name
  const uniqueSourceStorage = sourceStorage
    ? Array.from(new Map(sourceStorage.map((s) => [s.storage, s])).values())
    : [];
  const uniqueTargetStorage = targetStorageList
    ? Array.from(new Map(targetStorageList.map((s) => [s.storage, s])).values())
    : [];

  // Content-compatible, active+enabled storages. Used per-disk (each disk
  // excludes only its OWN current storage).
  const compatibleStorage = uniqueTargetStorage.filter((s) => {
    if (!s.active || !s.enabled) return false;
    const contentType = vmType === "lxc" ? "rootdir" : "images";
    return s.content.includes(contentType);
  });

  // Single-target list: also excludes every storage already holding a disk (a
  // same-storage move fails). Used when all disks share one storage.
  const eligibleTargetStorage = compatibleStorage.filter(
    (s) => !currentDiskStorages.has(s.storage),
  );

  const hasPerDiskSelection = Object.values(diskTargets).some((v) => v !== "");
  const { value: bwlimitKib, invalid: bwlimitInvalid } = parseBwlimit(bwlimit);

  const isFormValid = (() => {
    // A malformed bandwidth limit blocks every mode.
    if (bwlimitInvalid) return false;
    if (migrationType === "cross-cluster") {
      return targetNode.length > 0 && targetClusterId.length > 0;
    }
    // Intra-cluster
    if (migrationMode === "storage") {
      return perDiskStorage ? hasPerDiskSelection : targetStorage.length > 0;
    }
    if (migrationMode === "both") {
      return (
        targetNode.length > 0 &&
        (perDiskStorage ? hasPerDiskSelection : targetStorage.length > 0)
      );
    }
    // Live mode
    return targetNode.length > 0;
  })();

  // A format only makes sense when every disk lands on one known storage, so
  // per-disk targeting (mixed destinations) rules it out — as do containers,
  // whose volumes have no format and whose jobs the API rejects if one is set.
  const showFormat = needsDiskPlacement && !perDiskStorage && kind !== "ct";
  const formatTargetType = showFormat
    ? uniqueTargetStorage.find((s) => s.storage === targetStorage)?.type
    : undefined;
  // Guests whose disks are all in the same format can preselect it; mixed or
  // unknown formats fall back to the storage default.
  const sourceDiskFormat =
    currentDiskFormats.size === 1 ? [...currentDiskFormats][0] : undefined;

  const storageOnly =
    migrationType === "intra-cluster" && migrationMode === "storage";

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) handleClose();
      }}
    >
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>
            {step === "config" &&
              `Migrate ${kind === "ct" ? "container" : "VM"}`}
            {step === "preflight" && "Pre-flight checks"}
            {step === "progress" && "Migration progress"}
          </DialogTitle>
          <DialogDescription>
            <strong>{vmName}</strong> (VMID {String(vmid)}), currently on{" "}
            <strong>{currentNode}</strong>.
          </DialogDescription>
        </DialogHeader>

        {step === "config" && (
          <div className="space-y-4">
            <Field label="Migration type" htmlFor="migrate-type">
              <Select
                value={migrationType}
                onValueChange={(v) => {
                  setMigrationType(v as MigrationType);
                  setTargetNode("");
                  setTargetClusterId("");
                  setStorageMap({});
                  setNetworkMap({});
                  // Back to the per-mode default. Cross-cluster delete-source
                  // destroys the source guest, so it has to be chosen for THIS
                  // migration — not inherited from a storage move the user was
                  // configuring a moment ago.
                  setDeleteSourceOverride(null);
                }}
              >
                <SelectTrigger id="migrate-type">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="intra-cluster">
                    Within this cluster
                  </SelectItem>
                  <SelectItem value="cross-cluster">
                    To another cluster
                  </SelectItem>
                </SelectContent>
              </Select>
            </Field>

            {/* What to move (intra-cluster only) */}
            {migrationType === "intra-cluster" && (
              <Field
                label="What to move"
                htmlFor="migrate-mode"
                hint={
                  migrationMode === "live"
                    ? "Moves the guest to a different node."
                    : migrationMode === "storage"
                      ? "Moves all of the guest's disks to a different storage on the same node."
                      : "Moves the guest to another node and all of its disks to a different storage."
                }
              >
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
                  <SelectTrigger
                    id="migrate-mode"
                    aria-describedby="migrate-mode-note"
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="live">
                      The guest, to another node
                    </SelectItem>
                    <SelectItem value="storage">
                      Its disks, to another storage
                    </SelectItem>
                    <SelectItem value="both">
                      The guest and its disks
                    </SelectItem>
                  </SelectContent>
                </Select>
              </Field>
            )}

            {/* Target cluster (cross-cluster only) */}
            {migrationType === "cross-cluster" && (
              <Field label="Target cluster" htmlFor="migrate-cluster">
                <Select
                  value={targetClusterId}
                  onValueChange={(v) => {
                    setTargetClusterId(v);
                    setTargetNode("");
                    setStorageMap({});
                    setNetworkMap({});
                  }}
                >
                  <SelectTrigger id="migrate-cluster">
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
              </Field>
            )}

            {/* Target node (hidden for storage-only mode) */}
            {!storageOnly && (
              <Field
                label="Target node"
                htmlFor="migrate-node"
                hint="The least loaded node is picked for you."
              >
                <Select value={targetNode} onValueChange={setTargetNode}>
                  <SelectTrigger
                    id="migrate-node"
                    aria-describedby="migrate-node-note"
                  >
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
                          <span className="flex items-baseline gap-2">
                            {n.name}
                            {n.status !== "online" && (
                              <span className="text-muted-foreground">
                                ({n.status})
                              </span>
                            )}
                            {cpuLabel && memLabel && (
                              <span className="text-xs text-muted-foreground">
                                CPU {cpuLabel} · Mem {memLabel}
                              </span>
                            )}
                          </span>
                        </SelectItem>
                      );
                    })}
                  </SelectContent>
                </Select>
              </Field>
            )}

            {/* Target storage (storage/both mode for intra-cluster) */}
            {migrationType === "intra-cluster" &&
              (migrationMode === "storage" || migrationMode === "both") &&
              (perDiskStorage ? (
                <Field
                  label="Target storage for each disk"
                  hint={
                    hasPerDiskSelection
                      ? "This guest's disks are on more than one storage. Pick a target for each, or leave a disk where it is."
                      : "This guest's disks are on more than one storage. Choose a new storage for at least one of them."
                  }
                >
                  <div className="divide-y border-y">
                    {guestDisks.map((d) => {
                      const diskOptions = compatibleStorage.filter(
                        (s) => s.storage !== d.storage,
                      );
                      return (
                        <div
                          key={d.key}
                          className="grid grid-cols-[minmax(0,1fr)_minmax(0,11rem)] items-center gap-3 py-2"
                        >
                          <div className="min-w-0 truncate text-sm">
                            <span className="font-mono text-[12.5px]">
                              {d.key}
                            </span>
                            <span className="ml-2 text-[12.5px] text-muted-foreground">
                              {d.storage}
                              {d.size ? ` · ${d.size}` : ""}
                            </span>
                          </div>
                          <Select
                            value={diskTargets[d.key] || "__keep__"}
                            onValueChange={(v) => {
                              setDiskTargets((prev) => ({
                                ...prev,
                                [d.key]: v === "__keep__" ? "" : v,
                              }));
                            }}
                          >
                            <SelectTrigger
                              className="h-8"
                              aria-label={`Target storage for ${d.key}`}
                            >
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              <SelectItem value="__keep__">
                                Keep on {d.storage}
                              </SelectItem>
                              {diskOptions.map((s) => (
                                <SelectItem key={s.storage} value={s.storage}>
                                  {s.storage} ({s.type})
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </div>
                      );
                    })}
                  </div>
                </Field>
              ) : (
                <Field
                  label="Target storage"
                  htmlFor="migrate-storage"
                  hint={
                    currentDiskStorages.size > 0
                      ? `All disks move to this storage. The guest's current storage (${Array.from(currentDiskStorages).join(", ")}) is left out: Proxmox cannot move a disk onto the storage it is already on.`
                      : "All of the guest's disks are moved to this storage."
                  }
                >
                  <Select
                    value={targetStorage}
                    onValueChange={setTargetStorage}
                  >
                    <SelectTrigger
                      id="migrate-storage"
                      aria-describedby="migrate-storage-note"
                    >
                      <SelectValue placeholder="Select target storage" />
                    </SelectTrigger>
                    <SelectContent>
                      {eligibleTargetStorage.map((s) => (
                        <SelectItem key={s.storage} value={s.storage}>
                          {s.storage} ({s.type})
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  {guestConfig &&
                    targetStorageList !== undefined &&
                    eligibleTargetStorage.length === 0 && (
                      <p className="text-[12.5px] leading-snug text-destructive">
                        No eligible target storage. Every compatible storage
                        either already holds one of this guest&apos;s disks or
                        is unavailable.
                      </p>
                    )}
                </Field>
              ))}

            {/* Storage mapping (cross-cluster only) */}
            {migrationType === "cross-cluster" &&
              targetClusterId.length > 0 &&
              uniqueSourceStorage.length > 0 && (
                <Field
                  label="Storage mapping"
                  hint="A storage left unmapped keeps its name on the target cluster."
                >
                  <div className="divide-y border-y">
                    {uniqueSourceStorage.map((src) => (
                      <MappingRow key={src.storage} source={src.storage}>
                        <Select
                          value={storageMap[src.storage] ?? ""}
                          onValueChange={(v) => {
                            updateStorageMapping(src.storage, v);
                          }}
                        >
                          <SelectTrigger
                            className="h-8"
                            aria-label={`Target storage for ${src.storage}`}
                          >
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
                      </MappingRow>
                    ))}
                  </div>
                </Field>
              )}

            {/* Network mapping (cross-cluster only) */}
            {migrationType === "cross-cluster" &&
              targetClusterId.length > 0 &&
              targetNode.length > 0 &&
              sourceBridges &&
              sourceBridges.length > 0 && (
                <Field
                  label="Network mapping"
                  hint="A bridge left unmapped keeps its name on the target node."
                >
                  <div className="divide-y border-y">
                    {sourceBridges.map((src) => (
                      <MappingRow key={src.iface} source={src.iface}>
                        <Select
                          value={networkMap[src.iface] ?? ""}
                          onValueChange={(v) => {
                            updateNetworkMapping(src.iface, v);
                          }}
                        >
                          <SelectTrigger
                            className="h-8"
                            aria-label={`Target bridge for ${src.iface}`}
                          >
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
                      </MappingRow>
                    ))}
                  </div>
                </Field>
              )}

            {/* Target VMID (cross-cluster only) */}
            {migrationType === "cross-cluster" && (
              <Field
                label="Target VMID"
                htmlFor="migrate-vmid"
                hint="Optional."
              >
                <Input
                  id="migrate-vmid"
                  type="number"
                  value={targetVmid}
                  onChange={(e) => {
                    setTargetVmid(e.target.value);
                  }}
                  placeholder="Auto"
                  className="max-w-[10rem]"
                />
              </Field>
            )}

            <FormSection title="Options">
              <div className="space-y-3.5">
                {!storageOnly && (
                  <div className="flex items-center justify-between gap-4">
                    <Label htmlFor="migrate-online">Live migration</Label>
                    <Switch
                      id="migrate-online"
                      checked={online}
                      onCheckedChange={setOnline}
                    />
                  </div>
                )}
                <DiskMoveOptions
                  idPrefix="migrate"
                  hideFormat={!showFormat}
                  targetStorageType={formatTargetType}
                  sourceFormat={sourceDiskFormat}
                  format={diskFormat}
                  onFormatChange={setDiskFormat}
                  bwlimit={bwlimit}
                  onBwlimitChange={setBwlimit}
                  deleteSource={deleteSource}
                  onDeleteSourceChange={setDeleteSourceOverride}
                  deleteSourceLabel="Delete source after migration"
                  hideDeleteSource={!showDeleteSource}
                  {...(needsDiskPlacement
                    ? {
                        keptHint:
                          "Source volumes are kept as unused disks on the guest.",
                      }
                    : {})}
                />
              </div>
            </FormSection>

            {haStoppedWarning}

            {(createMutation.isError || checkMutation.isError) && (
              <p className="text-sm text-destructive">
                {createMutation.error?.message ?? checkMutation.error?.message}
              </p>
            )}

            <DialogFooter className="gap-2 sm:gap-0">
              <Button type="button" variant="outline" onClick={handleClose}>
                Cancel
              </Button>
              <Button
                onClick={handleCreate}
                disabled={!isFormValid || createMutation.isPending}
              >
                {createMutation.isPending || checkMutation.isPending ? (
                  <>
                    <Loader2 className="animate-spin" />
                    Running pre-flight checks...
                  </>
                ) : (
                  "Run pre-flight checks"
                )}
              </Button>
            </DialogFooter>
          </div>
        )}

        {step === "preflight" && job && (
          <div className="space-y-4">
            {job.check_results ? (
              <>
                {job.check_results.checks.length > 0 && (
                  <ul className="divide-y border-y">
                    {job.check_results.checks.map((check, i) => (
                      <li key={i} className="flex items-start gap-2.5 py-2.5">
                        <span className="mt-0.5 shrink-0">
                          {severityIcon[check.severity]}
                        </span>
                        <div className="min-w-0">
                          <p className="text-sm font-medium">{check.name}</p>
                          <p className="text-[12.5px] leading-snug text-muted-foreground">
                            {check.message}
                          </p>
                        </div>
                      </li>
                    ))}
                  </ul>
                )}
                <p
                  className={cn(
                    "text-sm",
                    job.check_results.passed
                      ? "text-muted-foreground"
                      : "text-destructive",
                  )}
                >
                  {job.check_results.passed
                    ? "The checks passed. Nothing has been moved yet."
                    : "Pre-flight checks failed. Fix the issues and try again."}
                </p>
                {job.check_results.passed && haStoppedWarning}
                <DialogFooter className="gap-2 sm:gap-0">
                  <Button variant="outline" onClick={handleClose}>
                    Close
                  </Button>
                  {job.check_results.passed && (
                    <Button
                      onClick={handleExecute}
                      disabled={executeMutation.isPending}
                    >
                      {executeMutation.isPending ? (
                        <Loader2 className="animate-spin" />
                      ) : null}
                      {haStopped
                        ? t("executeMigrationAnyway")
                        : "Execute migration"}
                    </Button>
                  )}
                </DialogFooter>
              </>
            ) : (
              <div className="flex items-center gap-2 text-sm text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin" />
                Running checks...
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

/** One line of a mapping: what it is called at the source, and where it lands. */
function MappingRow({
  source,
  children,
}: {
  source: string;
  children: React.ReactNode;
}) {
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-center gap-2.5 py-2">
      <span className="truncate text-sm">{source}</span>
      <ArrowRight
        aria-hidden="true"
        className="h-3.5 w-3.5 text-muted-foreground"
      />
      {children}
    </div>
  );
}

/** Progress step with live task log showing speed/throughput. */
function MigrationProgress({
  job,
  clusterId,
  onClose,
}: {
  job: {
    status: string;
    progress: number;
    upid: string;
    error_message: string;
    migration_mode: string;
    target_storage: string;
  };
  clusterId: string;
  onClose: () => void;
}) {
  const isActive = job.status === "pending" || job.status === "migrating";
  const hasUpid = job.upid.length > 0;
  const { data: logLines, isLoading: logLoading } = useQuery({
    queryKey: ["migration-log", clusterId, job.upid],
    queryFn: () =>
      apiClient.list<TaskLogLine>(
        apiPath`/api/v1/clusters/${clusterId}/tasks/${job.upid}/log`,
      ),
    enabled: hasUpid,
    refetchInterval: isActive ? 3000 : false,
  });

  // Extract speed/throughput from log lines (Proxmox includes lines like
  // "transferred 1.23 GiB in 45s (28.0 MiB/s)" or similar).
  const speedLines = logLines
    ?.map((l) => l.t)
    .filter((t) =>
      /\b(MiB\/s|GiB\/s|KiB\/s|MB\/s|GB\/s|transferred)\b/i.test(t),
    );
  const speedLine =
    speedLines && speedLines.length > 0
      ? speedLines[speedLines.length - 1]
      : undefined;

  const isStorageMode =
    job.migration_mode === "storage" || job.migration_mode === "both";

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
      <div className="flex items-center justify-between gap-4">
        <span className="text-sm text-muted-foreground">Status</span>
        <Badge variant={statusBadge[job.status] ?? "outline"}>
          {job.status.charAt(0).toUpperCase() + job.status.slice(1)}
        </Badge>
      </div>

      {isActive && (
        <div className="space-y-2">
          <div className="h-2 overflow-hidden rounded-full bg-muted">
            <div
              className="h-full rounded-full bg-info transition-all"
              style={{
                width: `${String(Math.max(job.progress * 100, 5))}%`,
              }}
            />
          </div>
          <p className="text-[12.5px] text-muted-foreground">
            {progressLabel()}
          </p>
          {speedLine && (
            <p className="break-all font-mono text-xs text-muted-foreground">
              {speedLine}
            </p>
          )}
        </div>
      )}

      {job.status === "completed" && (
        <p className="text-sm">
          {isStorageMode
            ? "Storage migration completed successfully."
            : "Migration completed successfully."}
        </p>
      )}
      {job.status === "failed" && (
        <p className="text-sm text-destructive">
          Migration failed: {job.error_message}
        </p>
      )}

      {/* Task Log (only for real Proxmox UPIDs) */}
      {hasUpid && <TaskLogSection lines={logLines} isLoading={logLoading} />}

      <DialogFooter className="gap-2 sm:gap-0">
        <ViewMigrationsLink onNavigate={onClose} />
        <Button variant="outline" onClick={onClose}>
          Close
        </Button>
      </DialogFooter>
    </div>
  );
}
