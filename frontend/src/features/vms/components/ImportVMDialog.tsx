import { useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
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
import { Checkbox } from "@/components/ui/checkbox";
import { AlertTriangle } from "lucide-react";
import {
  useClusterNodes,
  useClusterStorage,
  useNodeBridges,
} from "@/features/clusters/api/cluster-queries";
import {
  useDownloadURL,
  useUploadFile,
} from "@/features/storage/api/storage-queries";
import { useResourcePools } from "@/features/pools/api/pool-queries";
import { usePermissions } from "@/hooks/usePermissions";
import { deriveFilenameFromURL } from "@/lib/derive-filename";
import { formatBytes } from "@/lib/format";
import {
  biosOptions,
  cpuTypes,
  machineTypes,
  netModels,
  osTypes,
  scsiControllers,
} from "@/features/vms/lib/vm-config-constants";
import {
  useEnableImportContent,
  useImportMetadata,
  useImportSourceContent,
  useImportSources,
  useQueryURLMetadata,
  useStartImport,
} from "../api/import-queries";
import { EsxiSourceForm } from "./EsxiSourceForm";
import { TaskProgressBanner } from "./TaskProgressBanner";
import type {
  ImportMetadataResponse,
  ImportSource,
  StartImportRequest,
  StorageResponse,
} from "@/types/api";

interface ImportVMDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
}

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring";

type Step = "source" | "inspect" | "target" | "customize" | "review";
type SourceMode = "storage" | "url" | "upload";
type Acquisition = "staged" | "url" | "upload";
const steps: Step[] = ["source", "inspect", "target", "customize", "review"];
const stepLabels: Record<Step, string> = {
  source: "Source",
  inspect: "Inspect",
  target: "Target",
  customize: "Customize",
  review: "Review",
};

// PVE backends that store disk images as files — the only ones usable as OVA extraction /
// import-working storage. Block backends (rbd, lvm, lvmthin, zfspool) can't.
const fileBasedTypes = new Set(["dir", "nfs", "cifs", "btrfs"]);

const MAX_VMID = 999999999;

// NUL separator: it can never appear in storage/node names, so keys can't collide.
function sourceKeyOf(s: ImportSource): string {
  return `${s.storage}\u0000${s.node}`;
}

function volidBasename(volid: string): string {
  return volid.split("/").pop() ?? volid;
}

export function ImportVMDialog({
  open,
  onOpenChange,
  clusterId,
}: ImportVMDialogProps) {
  const { data: nodes } = useClusterNodes(clusterId);
  const { data: storageList } = useClusterStorage(clusterId);
  const { data: importSources } = useImportSources(clusterId);
  const { data: resourcePools } = useResourcePools(clusterId);
  const queryClient = useQueryClient();
  const { canManage } = usePermissions();

  const [step, setStep] = useState<Step>("source");

  // Source selection
  const [sourceMode, setSourceMode] = useState<SourceMode>("storage");
  const [sourceKey, setSourceKey] = useState("");
  const [selectedVolume, setSelectedVolume] = useState("");
  const [acquisition, setAcquisition] = useState<Acquisition>("staged");

  // URL download
  const [downloadUrl, setDownloadUrl] = useState("");
  const [downloadFilename, setDownloadFilename] = useState("");
  const [downloadSize, setDownloadSize] = useState<number | null>(null);
  const [downloadUpid, setDownloadUpid] = useState<string | null>(null);

  // Browser upload
  const [uploadFile, setUploadFile] = useState<File | null>(null);
  const [uploadProgress, setUploadProgress] = useState<number | null>(null);
  const [uploadUpid, setUploadUpid] = useState<string | null>(null);

  // Source management
  const [showEsxiForm, setShowEsxiForm] = useState(false);
  const [pendingSelectStorage, setPendingSelectStorage] = useState<
    string | null
  >(null);
  const [pendingSelectFilename, setPendingSelectFilename] = useState<
    string | null
  >(null);

  // Target selection
  const [targetNode, setTargetNode] = useState("");
  const [targetStorage, setTargetStorage] = useState("");
  const [workingStorage, setWorkingStorage] = useState("");
  const [bridge, setBridge] = useState("");
  const [vmid, setVmid] = useState("");
  const [name, setName] = useState("");
  const [diskFormat, setDiskFormat] = useState("");
  const [startAfter, setStartAfter] = useState(false);
  const [liveImport, setLiveImport] = useState(false);

  // Customize step — guest-config overrides, pre-filled from the source where known.
  const [cores, setCores] = useState("");
  const [sockets, setSockets] = useState("");
  const [memory, setMemory] = useState("");
  const [cpuType, setCpuType] = useState("");
  const [osType, setOsType] = useState("");
  const [bios, setBios] = useState("");
  const [machine, setMachine] = useState("");
  const [scsihw, setScsihw] = useState("");
  const [pool, setPool] = useState("");
  const [tags, setTags] = useState("");
  const [description, setDescription] = useState("");
  const [onboot, setOnboot] = useState(false);
  const [agent, setAgent] = useState(false);
  const [numa, setNuma] = useState(false);
  const [netModel, setNetModel] = useState("");
  const [vlan, setVlan] = useState("");
  const [firewall, setFirewall] = useState(false);
  const [macAddress, setMacAddress] = useState("");
  const [rateLimit, setRateLimit] = useState("");
  const [mtu, setMtu] = useState("");
  const [multiqueue, setMultiqueue] = useState("");

  const [importUpid, setImportUpid] = useState<string | null>(null);

  const metadataMutation = useImportMetadata();
  const downloadMutation = useDownloadURL();
  const uploadMutation = useUploadFile();
  const urlMetaMutation = useQueryURLMetadata();
  const enableImportMutation = useEnableImportContent();
  const startMutation = useStartImport();

  const selectedSource = useMemo(
    () =>
      (importSources ?? []).find((s) => sourceKeyOf(s) === sourceKey) ?? null,
    [importSources, sourceKey],
  );
  const sourceStorageName = selectedSource?.storage ?? "";
  const sourceNode = selectedSource?.node ?? "";
  const sourceShared = selectedSource?.shared ?? false;
  const sourceStorageType = selectedSource?.type ?? "";
  const sourcePoolId = selectedSource?.pool_id ?? "";
  const sourceIsEsxi = sourceStorageType === "esxi";

  // URL/upload can only target a real file storage — you can't download/upload into ESXi.
  const sourceOptions = useMemo(() => {
    const all = importSources ?? [];
    return sourceMode === "storage"
      ? all
      : all.filter((s) => s.type !== "esxi");
  }, [importSources, sourceMode]);

  const { data: sourceContent } = useImportSourceContent(
    clusterId,
    sourceStorageName || null,
    sourceNode || null,
  );

  const targetNodeOptions = useMemo(() => {
    const online = (nodes ?? []).filter((n) => n.status === "online");
    // Non-shared source storage: the volume only exists on its owning node.
    if (sourceNode && !sourceShared) {
      return online.filter((n) => n.name === sourceNode);
    }
    return online;
  }, [nodes, sourceNode, sourceShared]);

  const dedupByName = (pools: StorageResponse[]): string[] =>
    [...new Set(pools.map((s) => s.storage))].sort();

  const targetNodeId = useMemo(
    () => (nodes ?? []).find((n) => n.name === targetNode)?.id ?? "",
    [nodes, targetNode],
  );

  const imageStorageOptions = useMemo(
    () =>
      dedupByName(
        (storageList ?? []).filter(
          (s) =>
            s.active &&
            s.enabled &&
            s.content.includes("images") &&
            (s.shared || s.node_id === targetNodeId),
        ),
      ),
    [storageList, targetNodeId],
  );

  const workingStorageOptions = useMemo(
    () =>
      dedupByName(
        (storageList ?? []).filter(
          (s) =>
            s.active &&
            s.enabled &&
            s.content.includes("images") &&
            fileBasedTypes.has(s.type) &&
            (s.shared || s.node_id === targetNodeId),
        ),
      ),
    [storageList, targetNodeId],
  );

  // Storages that could host import content but don't yet — offered for one-click enablement
  // when the cluster has no import source at all.
  const enableableStorages = useMemo(
    () =>
      dedupByName(
        (storageList ?? []).filter(
          (s) =>
            s.enabled &&
            fileBasedTypes.has(s.type) &&
            !s.content.includes("import"),
        ),
      ),
    [storageList],
  );

  const targetStorageType = useMemo(() => {
    const pool = (storageList ?? []).find(
      (s) =>
        s.storage === targetStorage && (s.shared || s.node_id === targetNodeId),
    );
    return pool?.type ?? "";
  }, [storageList, targetStorage, targetNodeId]);

  // OVA extraction needs a file-based storage. If the disks land on a block storage, a
  // file-based working storage is effectively required — warn and preselect one.
  const isOvaSource = selectedVolume.toLowerCase().endsWith(".ova");
  const needsWorkingStorage =
    isOvaSource &&
    targetStorage !== "" &&
    !fileBasedTypes.has(targetStorageType);

  const { data: bridges } = useNodeBridges(clusterId, targetNode);

  const effectiveDownloadFilename =
    downloadFilename.trim() || deriveFilenameFromURL(downloadUrl.trim());
  const downloadUrlValid = /^https?:\/\//i.test(downloadUrl.trim());

  const vmidNum = vmid ? Number(vmid) : 0;
  const vmidValid = vmid === "" || (vmidNum >= 100 && vmidNum <= MAX_VMID);

  // Auto-select a source after registering an ESXi host / enabling import content.
  useEffect(() => {
    if (!pendingSelectStorage || !importSources) return;
    const match = importSources.find((s) => s.storage === pendingSelectStorage);
    if (match) {
      setSourceKey(sourceKeyOf(match));
      setPendingSelectStorage(null);
      setShowEsxiForm(false);
    }
  }, [pendingSelectStorage, importSources]);

  // Auto-select the just-downloaded / just-uploaded file once the content list refreshes.
  useEffect(() => {
    if (!pendingSelectFilename) return;
    const match = (sourceContent?.items ?? []).find(
      (it) => volidBasename(it.volid) === pendingSelectFilename,
    );
    if (match) {
      setSelectedVolume(match.volid);
      setPendingSelectFilename(null);
    }
  }, [pendingSelectFilename, sourceContent]);

  // Preselect a working storage when the target placement requires one — but only ONCE per
  // target storage, so the user can still clear it afterwards without the effect re-forcing it.
  const workingAutoSetFor = useRef<string>("");
  useEffect(() => {
    if (
      needsWorkingStorage &&
      !workingStorage &&
      workingStorageOptions.length > 0 &&
      workingAutoSetFor.current !== targetStorage
    ) {
      workingAutoSetFor.current = targetStorage;
      setWorkingStorage(workingStorageOptions[0] ?? "");
    }
  }, [
    needsWorkingStorage,
    workingStorage,
    workingStorageOptions,
    targetStorage,
  ]);

  function resetSourceDownstream() {
    setSelectedVolume("");
    setAcquisition("staged");
    // The detected filename/size and any download task describe the *previous* source's
    // storage, so clear them (keep the typed URL — the user may reuse it against the new
    // target). Otherwise stale metadata and a completed banner bleed across source switches.
    setDownloadFilename("");
    setDownloadSize(null);
    setDownloadUpid(null);
    setUploadUpid(null);
    setUploadFile(null);
    setUploadProgress(null);
    setPendingSelectFilename(null);
    setTargetNode("");
    setTargetStorage("");
    setWorkingStorage("");
    setBridge("");
    setName("");
    resetCustomize();
  }

  function resetAndClose() {
    setStep("source");
    setSourceMode("storage");
    setSourceKey("");
    setSelectedVolume("");
    setAcquisition("staged");
    setDownloadUrl("");
    setDownloadFilename("");
    setDownloadSize(null);
    setDownloadUpid(null);
    setUploadFile(null);
    setUploadProgress(null);
    setUploadUpid(null);
    setShowEsxiForm(false);
    setPendingSelectStorage(null);
    setPendingSelectFilename(null);
    setTargetNode("");
    setTargetStorage("");
    setWorkingStorage("");
    setBridge("");
    setVmid("");
    setName("");
    setDiskFormat("");
    setStartAfter(false);
    setLiveImport(false);
    resetCustomize();
    setImportUpid(null);
    metadataMutation.reset();
    downloadMutation.reset();
    uploadMutation.reset();
    urlMetaMutation.reset();
    startMutation.reset();
    onOpenChange(false);
  }

  function inferSourceFormat(volid: string): string {
    if (sourceIsEsxi) return "esxi";
    const lower = volid.toLowerCase();
    if (lower.endsWith(".ova")) return "ova";
    if (lower.endsWith(".ovf")) return "ovf";
    if (lower.endsWith(".vmdk")) return "vmdk";
    if (lower.endsWith(".qcow2")) return "qcow2";
    if (lower.endsWith(".vhdx")) return "vhdx";
    if (lower.endsWith(".vhd")) return "vhd";
    if (lower.endsWith(".raw")) return "raw";
    if (lower.endsWith(".img")) return "raw";
    return "";
  }

  function invalidateContent() {
    void queryClient.invalidateQueries({
      queryKey: ["clusters", clusterId, "import-content"],
    });
  }

  function goToInspect() {
    metadataMutation.mutate(
      {
        clusterId,
        node: sourceNode,
        storage: sourceStorageName,
        volume: selectedVolume,
      },
      {
        onSuccess: (meta: ImportMetadataResponse) => {
          if (!name) setName(meta.name);
          if (!targetNode) setTargetNode(sourceNode);
          // Pre-fill the Customize step from the source guest (only fields left untouched),
          // so the wizard shows the detected hardware and the user can adjust from there.
          const ca = meta.create_args;
          // cores/sockets come from the normalized topology (meta.cores/meta.sockets), not
          // the raw create-args — an ESXi guest reports vCPUs as sockets with no cores, which
          // the backend folds into cores × 1 socket.
          if (!cores && meta.cores > 0) setCores(String(meta.cores));
          if (!sockets && meta.sockets > 0) setSockets(String(meta.sockets));
          if (!memory && meta.memory > 0) setMemory(String(meta.memory));
          if (!osType && meta.ostype) setOsType(meta.ostype);
          if (!bios && ca["bios"]) setBios(ca["bios"]);
          if (!machine && ca["machine"]) setMachine(ca["machine"]);
          if (!scsihw && ca["scsihw"]) setScsihw(ca["scsihw"]);
          if (!cpuType && ca["cpu"]) setCpuType(ca["cpu"]);
          setStep("inspect");
        },
      },
    );
  }

  function resetCustomize() {
    setCores("");
    setSockets("");
    setMemory("");
    setCpuType("");
    setOsType("");
    setBios("");
    setMachine("");
    setScsihw("");
    setPool("");
    setTags("");
    setDescription("");
    setOnboot(false);
    setAgent(false);
    setNuma(false);
    setNetModel("");
    setVlan("");
    setFirewall(false);
    setMacAddress("");
    setRateLimit("");
    setMtu("");
    setMultiqueue("");
  }

  function checkUrl() {
    if (!downloadUrlValid) return;
    urlMetaMutation.mutate(
      { clusterId, node: sourceNode, url: downloadUrl.trim() },
      {
        onSuccess: (meta) => {
          if (meta.filename && !downloadFilename)
            setDownloadFilename(meta.filename);
          setDownloadSize(meta.size ?? null);
        },
      },
    );
  }

  function triggerDownload() {
    if (!downloadUrlValid || !effectiveDownloadFilename || !sourcePoolId)
      return;
    setDownloadUpid(null);
    downloadMutation.mutate(
      {
        clusterId,
        storageId: sourcePoolId,
        data: {
          url: downloadUrl.trim(),
          content: "import",
          filename: effectiveDownloadFilename,
        },
      },
      {
        onSuccess: (res) => {
          setDownloadUpid(res.upid || null);
        },
      },
    );
  }

  function triggerUpload() {
    if (!uploadFile || !sourcePoolId) return;
    setUploadUpid(null);
    setUploadProgress(0);
    uploadMutation.mutate(
      {
        clusterId,
        storageId: sourcePoolId,
        content: "import",
        file: uploadFile,
        onProgress: (p) => {
          setUploadProgress(p);
        },
      },
      {
        onSuccess: (res) => {
          setUploadProgress(null);
          if (res.upid) {
            setUploadUpid(res.upid);
          } else {
            // No server task (already present) — refresh and select immediately.
            onAcquireComplete(uploadFile.name, "upload");
          }
        },
        onError: () => {
          setUploadProgress(null);
        },
      },
    );
  }

  function onAcquireComplete(filename: string, how: Acquisition) {
    invalidateContent();
    setAcquisition(how);
    setPendingSelectFilename(filename);
  }

  function submitImport() {
    const body: StartImportRequest = {
      node: sourceNode,
      storage: sourceStorageName,
      volume: selectedVolume,
      source_format: inferSourceFormat(selectedVolume),
      source_acquisition: sourceIsEsxi ? "esxi" : acquisition,
      target_node: targetNode,
      target_storage: targetStorage,
      start_after: startAfter,
      live_import: liveImport,
    };
    if (workingStorage) body.working_storage = workingStorage;
    if (bridge) body.bridge = bridge;
    if (vmid) body.vmid = Number(vmid);
    if (name) body.name = name;
    if (diskFormat) body.disk_format = diskFormat;

    // Customize-step overrides (omit blanks so the source-derived value is kept).
    if (cores) body.cores = Number(cores);
    if (sockets) body.sockets = Number(sockets);
    if (memory) body.memory = Number(memory);
    if (cpuType) body.cpu_type = cpuType;
    if (osType) body.os_type = osType;
    if (bios) body.bios = bios;
    if (machine) body.machine = machine;
    if (scsihw) body.scsihw = scsihw;
    if (pool) body.pool = pool;
    if (tags) body.tags = tags;
    if (description) body.description = description;
    if (onboot) body.onboot = true;
    if (agent) body.agent = true;
    if (numa) body.numa = true;
    // Network options only matter when a NIC is being attached.
    if (bridge) {
      if (netModel) body.net_model = netModel;
      if (vlan) body.vlan_tag = Number(vlan);
      if (firewall) body.firewall = true;
      if (macAddress) body.mac_address = macAddress;
      if (rateLimit) body.rate_limit = rateLimit;
      if (mtu) body.mtu = Number(mtu);
      if (multiqueue) body.multiqueue = Number(multiqueue);
    }

    startMutation.mutate(
      { clusterId, body },
      {
        onSuccess: (job) => {
          setImportUpid(job.upid ?? null);
        },
      },
    );
  }

  const stepIdx = steps.indexOf(step);
  const canProceed =
    step === "source"
      ? selectedVolume !== "" && sourceNode !== ""
      : step === "target"
        ? targetNode !== "" && targetStorage !== ""
        : true;

  const meta = metadataMutation.data;
  const diskEntries = meta ? Object.entries(meta.disks) : [];

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) resetAndClose();
      }}
    >
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Import VM</DialogTitle>
          <DialogDescription>
            Import a virtual machine from an OVA/OVF appliance, a disk image, or
            an ESXi/vCenter host.
          </DialogDescription>
        </DialogHeader>

        {/* Step indicator */}
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          {steps.map((s, i) => (
            <span
              key={s}
              className={i === stepIdx ? "font-medium text-foreground" : ""}
            >
              {i + 1}. {stepLabels[s]}
              {i < steps.length - 1 ? " ›" : ""}
            </span>
          ))}
        </div>

        {importUpid ? (
          <TaskProgressBanner
            clusterId={clusterId}
            upid={importUpid}
            description={`Import ${name || "VM"}`}
          />
        ) : (
          /* 13rem budgets the dialog chrome (header, step indicator, footer,
             p-6 and the gaps) against DialogContent's own 85vh cap, so this
             stays the only scroller. Measured at 194px with a single-line
             header; below sm: the footer buttons stack and the budget is no
             longer enough, so a phone can still show both scrollbars. */
          <div className="max-h-[calc(85vh-13rem)] space-y-4 overflow-y-auto pr-1">
            {step === "source" && (
              <div className="space-y-4">
                <div className="flex flex-wrap gap-2">
                  <Button
                    type="button"
                    size="sm"
                    variant={sourceMode === "storage" ? "default" : "outline"}
                    onClick={() => {
                      setSourceMode("storage");
                    }}
                  >
                    From import storage
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant={sourceMode === "url" ? "default" : "outline"}
                    onClick={() => {
                      setSourceMode("url");
                    }}
                  >
                    Download OVA from URL
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant={sourceMode === "upload" ? "default" : "outline"}
                    onClick={() => {
                      setSourceMode("upload");
                    }}
                  >
                    Upload OVA
                  </Button>
                </div>

                {sourceMode === "url" && (
                  <div className="space-y-2">
                    <Label>OVA URL</Label>
                    <div className="flex gap-2">
                      <Input
                        value={downloadUrl}
                        onChange={(e) => {
                          setDownloadUrl(e.target.value);
                          setDownloadSize(null);
                        }}
                        placeholder="https://example.com/appliance.ova"
                        autoComplete="off"
                        spellCheck={false}
                      />
                      <Button
                        type="button"
                        size="sm"
                        variant="outline"
                        disabled={
                          !downloadUrlValid || urlMetaMutation.isPending
                        }
                        onClick={checkUrl}
                      >
                        {urlMetaMutation.isPending ? "Checking…" : "Check URL"}
                      </Button>
                    </div>
                    <Label>Filename (optional — derived from the URL)</Label>
                    <Input
                      value={downloadFilename}
                      onChange={(e) => {
                        setDownloadFilename(e.target.value);
                      }}
                      placeholder={effectiveDownloadFilename || "appliance.ova"}
                      autoComplete="off"
                      spellCheck={false}
                    />
                    {downloadSize != null && downloadSize > 0 && (
                      <p className="text-xs text-muted-foreground">
                        Detected size: {formatBytes(downloadSize)}
                      </p>
                    )}
                  </div>
                )}

                <div className="space-y-2">
                  <Label>
                    {sourceMode === "storage"
                      ? "Import source"
                      : "Download / upload to storage"}
                  </Label>
                  <select
                    className={selectClass}
                    value={sourceKey}
                    onChange={(e) => {
                      setSourceKey(e.target.value);
                      resetSourceDownstream();
                    }}
                  >
                    <option value="">Select a source…</option>
                    {sourceOptions.map((s) => (
                      <option key={sourceKeyOf(s)} value={sourceKeyOf(s)}>
                        {s.storage}
                        {s.type === "esxi"
                          ? " (ESXi)"
                          : s.shared
                            ? " (shared)"
                            : ` — ${s.node}`}
                      </option>
                    ))}
                  </select>
                  {sourceOptions.length === 0 && (
                    <div className="space-y-2 text-xs text-muted-foreground">
                      <p>
                        No import-capable storage found. Add the
                        &quot;import&quot; content type to a directory/NFS
                        storage, register an ESXi source, or upload an OVA.
                      </p>
                      {canManage("storage") &&
                        enableableStorages.length > 0 && (
                          <div className="flex flex-wrap items-center gap-2">
                            <span>Enable import content on:</span>
                            {enableableStorages.map((s) => (
                              <Button
                                key={s}
                                type="button"
                                size="sm"
                                variant="outline"
                                disabled={enableImportMutation.isPending}
                                onClick={() => {
                                  enableImportMutation.mutate(
                                    { clusterId, storage: s },
                                    {
                                      onSuccess: () => {
                                        setPendingSelectStorage(s);
                                      },
                                    },
                                  );
                                }}
                              >
                                {s}
                              </Button>
                            ))}
                          </div>
                        )}
                      {enableImportMutation.isError && (
                        <p className="text-destructive">
                          {enableImportMutation.error instanceof Error
                            ? enableImportMutation.error.message
                            : "Failed to enable import content"}
                        </p>
                      )}
                    </div>
                  )}
                </div>

                {/* ESXi source registration */}
                {showEsxiForm ? (
                  <EsxiSourceForm
                    clusterId={clusterId}
                    onRegistered={(storage) => {
                      setPendingSelectStorage(storage);
                    }}
                    onCancel={() => {
                      setShowEsxiForm(false);
                    }}
                  />
                ) : (
                  canManage("vm_import") && (
                    <button
                      type="button"
                      className="text-xs text-muted-foreground hover:text-foreground"
                      onClick={() => {
                        setShowEsxiForm(true);
                      }}
                    >
                      + Add ESXi / vCenter source
                    </button>
                  )
                )}

                {/* URL download panel */}
                {sourceMode === "url" && sourcePoolId && (
                  <div className="space-y-2 rounded-md border border-border p-3">
                    <Button
                      type="button"
                      size="sm"
                      disabled={
                        !downloadUrlValid ||
                        !effectiveDownloadFilename ||
                        downloadMutation.isPending
                      }
                      onClick={triggerDownload}
                    >
                      {downloadMutation.isPending
                        ? "Starting…"
                        : "Download to storage"}
                    </Button>
                    {downloadUpid && (
                      <TaskProgressBanner
                        clusterId={clusterId}
                        upid={downloadUpid}
                        description="Downloading OVA"
                        onComplete={(ok) => {
                          if (ok)
                            onAcquireComplete(effectiveDownloadFilename, "url");
                        }}
                      />
                    )}
                    {downloadMutation.isError && (
                      <p className="text-xs text-destructive">
                        {downloadMutation.error instanceof Error
                          ? downloadMutation.error.message
                          : "Download failed"}
                      </p>
                    )}
                  </div>
                )}
                {sourceMode === "url" && sourceKey && !sourcePoolId && (
                  <p className="text-xs text-muted-foreground">
                    This source isn&apos;t in the inventory yet, so a URL
                    download can&apos;t target it.
                  </p>
                )}

                {/* Upload panel */}
                {sourceMode === "upload" && sourcePoolId && (
                  <div className="space-y-2 rounded-md border border-border p-3">
                    <Input
                      type="file"
                      accept=".ova"
                      onChange={(e) => {
                        setUploadFile(e.target.files?.[0] ?? null);
                      }}
                    />
                    <Button
                      type="button"
                      size="sm"
                      disabled={
                        !uploadFile ||
                        uploadMutation.isPending ||
                        uploadProgress != null
                      }
                      onClick={triggerUpload}
                    >
                      {uploadProgress != null
                        ? `Uploading… ${String(uploadProgress)}%`
                        : uploadMutation.isPending
                          ? "Uploading…"
                          : "Upload to storage"}
                    </Button>
                    {uploadUpid && (
                      <TaskProgressBanner
                        clusterId={clusterId}
                        upid={uploadUpid}
                        description="Processing uploaded OVA"
                        onComplete={(ok) => {
                          if (ok && uploadFile)
                            onAcquireComplete(uploadFile.name, "upload");
                        }}
                      />
                    )}
                    {uploadMutation.isError && (
                      <p className="text-xs text-destructive">
                        {uploadMutation.error instanceof Error
                          ? uploadMutation.error.message
                          : "Upload failed"}
                      </p>
                    )}
                  </div>
                )}
                {sourceMode === "upload" && sourceKey && !sourcePoolId && (
                  <p className="text-xs text-muted-foreground">
                    This source isn&apos;t in the inventory yet, so an upload
                    can&apos;t target it.
                  </p>
                )}

                {sourceKey && (
                  <div className="space-y-2">
                    <Label>Source guest / image</Label>
                    <select
                      className={selectClass}
                      value={selectedVolume}
                      onChange={(e) => {
                        setSelectedVolume(e.target.value);
                        setAcquisition("staged");
                      }}
                    >
                      <option value="">Select a source…</option>
                      {(sourceContent?.items ?? []).map((item) => (
                        <option key={item.volid} value={item.volid}>
                          {volidBasename(item.volid)}
                          {item.size > 0 ? ` (${formatBytes(item.size)})` : ""}
                        </option>
                      ))}
                    </select>
                  </div>
                )}

                {metadataMutation.isError && (
                  <p className="text-sm text-destructive">
                    Failed to read import metadata:{" "}
                    {metadataMutation.error.message}
                  </p>
                )}
              </div>
            )}

            {step === "inspect" && meta && (
              <div className="space-y-4">
                <div className="grid grid-cols-2 gap-3 text-sm">
                  <div>
                    <span className="text-muted-foreground">Name:</span>{" "}
                    {meta.name || "—"}
                  </div>
                  <div>
                    <span className="text-muted-foreground">OS type:</span>{" "}
                    {meta.ostype || "—"}
                  </div>
                  <div>
                    <span className="text-muted-foreground">Cores:</span>{" "}
                    {meta.cores || "—"}
                  </div>
                  <div>
                    <span className="text-muted-foreground">Memory:</span>{" "}
                    {meta.memory ? `${String(meta.memory)} MiB` : "—"}
                  </div>
                  <div>
                    <span className="text-muted-foreground">Source:</span>{" "}
                    {meta.source || "—"}
                  </div>
                </div>
                <div className="space-y-1">
                  <Label>Disks ({diskEntries.length})</Label>
                  <div className="rounded-md border border-border text-sm">
                    {diskEntries.length === 0 && (
                      <div className="px-3 py-2 text-muted-foreground">
                        No disks detected.
                      </div>
                    )}
                    {diskEntries.map(([slot, disk]) => (
                      <div
                        key={slot}
                        className="flex justify-between border-b border-border px-3 py-1.5 last:border-b-0"
                      >
                        <span className="font-mono">{slot}</span>
                        <span className="truncate text-muted-foreground">
                          {disk.volid}
                        </span>
                      </div>
                    ))}
                  </div>
                </div>
                {meta.warnings.length > 0 && (
                  <div className="space-y-1 rounded-md border border-amber-500/40 bg-amber-500/5 p-3">
                    <div className="flex items-center gap-1.5 text-sm font-medium text-amber-600">
                      <AlertTriangle className="h-4 w-4" /> Review after import
                    </div>
                    <ul className="list-inside list-disc text-xs text-muted-foreground">
                      {meta.warnings.map((w, i) => (
                        <li key={`${w.type}-${String(i)}`}>
                          {w.type.replace(/-/g, " ")}
                          {w.value ? `: ${w.value}` : ""}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
              </div>
            )}

            {step === "target" && (
              <div className="space-y-3">
                <div className="space-y-2">
                  <Label>Target node</Label>
                  <select
                    className={selectClass}
                    value={targetNode}
                    onChange={(e) => {
                      setTargetNode(e.target.value);
                      setTargetStorage("");
                      setWorkingStorage("");
                      setBridge("");
                    }}
                    disabled={sourceNode !== "" && !sourceShared}
                  >
                    <option value="">Select a node…</option>
                    {targetNodeOptions.map((n) => (
                      <option key={n.id} value={n.name}>
                        {n.name}
                      </option>
                    ))}
                  </select>
                  {sourceNode !== "" && !sourceShared && (
                    <p className="text-xs text-muted-foreground">
                      The source storage is not shared, so the import must run
                      on node {sourceNode}.
                    </p>
                  )}
                </div>
                <div className="space-y-2">
                  <Label>Target storage (disks)</Label>
                  <select
                    className={selectClass}
                    value={targetStorage}
                    onChange={(e) => {
                      setTargetStorage(e.target.value);
                    }}
                  >
                    <option value="">Select a storage…</option>
                    {imageStorageOptions.map((s) => (
                      <option key={s} value={s}>
                        {s}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-2">
                    <Label>
                      Working storage{" "}
                      {needsWorkingStorage ? "(required)" : "(optional)"}
                    </Label>
                    <select
                      className={selectClass}
                      value={workingStorage}
                      onChange={(e) => {
                        setWorkingStorage(e.target.value);
                      }}
                    >
                      <option value="">Default (target storage)</option>
                      {workingStorageOptions.map((s) => (
                        <option key={s} value={s}>
                          {s}
                        </option>
                      ))}
                    </select>
                  </div>
                  <div className="space-y-2">
                    <Label>Network bridge (optional)</Label>
                    <select
                      className={selectClass}
                      value={bridge}
                      onChange={(e) => {
                        setBridge(e.target.value);
                      }}
                    >
                      <option value="">No NIC</option>
                      {(bridges ?? []).map((b) => (
                        <option key={b.iface} value={b.iface}>
                          {b.iface}
                        </option>
                      ))}
                    </select>
                  </div>
                </div>
                {needsWorkingStorage && (
                  <div className="flex items-start gap-1.5 rounded-md border border-amber-500/40 bg-amber-500/5 p-2 text-xs text-amber-600">
                    <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                    <span>
                      The target storage is block-based, but extracting an OVA
                      needs a file-based working storage. A working storage has
                      been preselected; clear it only if the target already
                      stores images as files.
                    </span>
                  </div>
                )}
                <div className="space-y-2">
                  <Label>VMID (blank = auto)</Label>
                  <Input
                    value={vmid}
                    onChange={(e) => {
                      setVmid(e.target.value.replace(/[^0-9]/g, ""));
                    }}
                    placeholder="auto"
                  />
                  {!vmidValid && (
                    <p className="text-xs text-destructive">
                      VMID must be between 100 and {MAX_VMID}.
                    </p>
                  )}
                </div>
                <div className="space-y-2">
                  <Label>Disk format (optional)</Label>
                  <select
                    className={selectClass}
                    value={diskFormat}
                    onChange={(e) => {
                      setDiskFormat(e.target.value);
                    }}
                  >
                    <option value="">Storage default</option>
                    <option value="qcow2">qcow2</option>
                    <option value="raw">raw</option>
                    <option value="vmdk">vmdk</option>
                  </select>
                </div>
              </div>
            )}

            {step === "customize" && (
              <div className="space-y-4">
                <p className="text-xs text-muted-foreground">
                  Adjust the imported guest&apos;s settings. Fields are detected
                  from the source where possible; leave one as-is to keep the
                  detected value. Adding disks or extra NICs isn&apos;t
                  supported here — do that after import.
                </p>

                <div className="space-y-2">
                  <Label className="text-xs uppercase text-muted-foreground">
                    OS &amp; System
                  </Label>
                  <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-1">
                      <Label>OS type</Label>
                      <select
                        className={selectClass}
                        value={osType}
                        onChange={(e) => {
                          setOsType(e.target.value);
                        }}
                      >
                        <option value="">Detected</option>
                        {osTypes.map((o) => (
                          <option key={o.value} value={o.value}>
                            {o.label}
                          </option>
                        ))}
                      </select>
                    </div>
                    <div className="space-y-1">
                      <Label>SCSI controller</Label>
                      <select
                        className={selectClass}
                        value={scsihw}
                        onChange={(e) => {
                          setScsihw(e.target.value);
                        }}
                      >
                        <option value="">Detected / default</option>
                        {scsiControllers.map((s) => (
                          <option key={s.value} value={s.value}>
                            {s.label}
                          </option>
                        ))}
                      </select>
                    </div>
                    <div className="space-y-1">
                      <Label>BIOS</Label>
                      <select
                        className={selectClass}
                        value={bios}
                        onChange={(e) => {
                          setBios(e.target.value);
                        }}
                      >
                        <option value="">Detected</option>
                        {biosOptions.map((b) => (
                          <option key={b.value} value={b.value}>
                            {b.label}
                          </option>
                        ))}
                      </select>
                    </div>
                    <div className="space-y-1">
                      <Label>Machine</Label>
                      <select
                        className={selectClass}
                        value={machine}
                        onChange={(e) => {
                          setMachine(e.target.value);
                        }}
                      >
                        <option value="">Detected</option>
                        {machineTypes.map((m) => (
                          <option key={m.value} value={m.value}>
                            {m.label}
                          </option>
                        ))}
                      </select>
                    </div>
                  </div>
                  <label className="flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={agent}
                      onCheckedChange={(v) => {
                        setAgent(v === true);
                      }}
                    />
                    <span>Enable QEMU guest agent</span>
                  </label>
                </div>

                <div className="space-y-2">
                  <Label className="text-xs uppercase text-muted-foreground">
                    CPU &amp; Memory
                  </Label>
                  <div className="grid grid-cols-3 gap-3">
                    <div className="space-y-1">
                      <Label>Cores</Label>
                      <Input
                        value={cores}
                        onChange={(e) => {
                          setCores(e.target.value.replace(/[^0-9]/g, ""));
                        }}
                        placeholder="detected"
                      />
                    </div>
                    <div className="space-y-1">
                      <Label>Sockets</Label>
                      <Input
                        value={sockets}
                        onChange={(e) => {
                          setSockets(e.target.value.replace(/[^0-9]/g, ""));
                        }}
                        placeholder="1"
                      />
                    </div>
                    <div className="space-y-1">
                      <Label>Memory (MiB)</Label>
                      <Input
                        value={memory}
                        onChange={(e) => {
                          setMemory(e.target.value.replace(/[^0-9]/g, ""));
                        }}
                        placeholder="detected"
                      />
                    </div>
                  </div>
                  <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-1">
                      <Label>CPU type</Label>
                      <select
                        className={selectClass}
                        value={cpuType}
                        onChange={(e) => {
                          setCpuType(e.target.value);
                        }}
                      >
                        <option value="">Default (x86-64-v2-AES)</option>
                        {cpuTypes.map((cpu) => (
                          <option key={cpu} value={cpu}>
                            {cpu}
                          </option>
                        ))}
                      </select>
                    </div>
                    <label className="mt-6 flex items-center gap-2 text-sm">
                      <Checkbox
                        checked={numa}
                        onCheckedChange={(v) => {
                          setNuma(v === true);
                        }}
                      />
                      <span>Enable NUMA</span>
                    </label>
                  </div>
                </div>

                {bridge ? (
                  <div className="space-y-2">
                    <Label className="text-xs uppercase text-muted-foreground">
                      Network (on {bridge})
                    </Label>
                    <div className="grid grid-cols-2 gap-3">
                      <div className="space-y-1">
                        <Label>Model</Label>
                        <select
                          className={selectClass}
                          value={netModel}
                          onChange={(e) => {
                            setNetModel(e.target.value);
                          }}
                        >
                          <option value="">From source</option>
                          {netModels.map((n) => (
                            <option key={n.value} value={n.value}>
                              {n.label}
                            </option>
                          ))}
                        </select>
                      </div>
                      <div className="space-y-1">
                        <Label>VLAN tag</Label>
                        <Input
                          value={vlan}
                          onChange={(e) => {
                            setVlan(e.target.value.replace(/[^0-9]/g, ""));
                          }}
                          placeholder="none"
                        />
                      </div>
                      <div className="space-y-1">
                        <Label>MAC address</Label>
                        <Input
                          value={macAddress}
                          onChange={(e) => {
                            setMacAddress(e.target.value);
                          }}
                          placeholder="from source / auto"
                        />
                      </div>
                      <div className="space-y-1">
                        <Label>Rate limit (MB/s)</Label>
                        <Input
                          value={rateLimit}
                          onChange={(e) => {
                            setRateLimit(
                              e.target.value.replace(/[^0-9.]/g, ""),
                            );
                          }}
                          placeholder="unlimited"
                        />
                      </div>
                      <div className="space-y-1">
                        <Label>MTU</Label>
                        <Input
                          value={mtu}
                          onChange={(e) => {
                            setMtu(e.target.value.replace(/[^0-9]/g, ""));
                          }}
                          placeholder="default"
                        />
                      </div>
                      <div className="space-y-1">
                        <Label>Multiqueue</Label>
                        <Input
                          value={multiqueue}
                          onChange={(e) => {
                            setMultiqueue(
                              e.target.value.replace(/[^0-9]/g, ""),
                            );
                          }}
                          placeholder="disabled"
                        />
                      </div>
                    </div>
                    <label className="flex items-center gap-2 text-sm">
                      <Checkbox
                        checked={firewall}
                        onCheckedChange={(v) => {
                          setFirewall(v === true);
                        }}
                      />
                      <span>Enable firewall on the NIC</span>
                    </label>
                  </div>
                ) : (
                  <p className="text-xs text-muted-foreground">
                    No network bridge was selected on the Target step, so no NIC
                    will be attached. Go back to add one if the guest needs
                    networking.
                  </p>
                )}

                <div className="space-y-2">
                  <Label className="text-xs uppercase text-muted-foreground">
                    Identity &amp; options
                  </Label>
                  <div className="space-y-1">
                    <Label>VM name</Label>
                    <Input
                      value={name}
                      onChange={(e) => {
                        setName(e.target.value);
                      }}
                      placeholder="Guest name"
                    />
                  </div>
                  <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-1">
                      <Label>Resource pool</Label>
                      <select
                        className={selectClass}
                        value={pool}
                        onChange={(e) => {
                          setPool(e.target.value);
                        }}
                      >
                        <option value="">None</option>
                        {(resourcePools ?? []).map((rp) => (
                          <option key={rp.poolid} value={rp.poolid}>
                            {rp.poolid}
                          </option>
                        ))}
                      </select>
                    </div>
                    <div className="space-y-1">
                      <Label>Tags</Label>
                      <Input
                        value={tags}
                        onChange={(e) => {
                          setTags(e.target.value);
                        }}
                        placeholder="tag1;tag2"
                      />
                    </div>
                  </div>
                  <div className="space-y-1">
                    <Label>Description</Label>
                    <Input
                      value={description}
                      onChange={(e) => {
                        setDescription(e.target.value);
                      }}
                      placeholder="Optional"
                    />
                  </div>
                  <label className="flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={onboot}
                      onCheckedChange={(v) => {
                        setOnboot(v === true);
                      }}
                    />
                    <span>Start at boot (onboot)</span>
                  </label>
                </div>
              </div>
            )}

            {step === "review" && (
              <div className="space-y-3 text-sm">
                <div className="grid grid-cols-2 gap-2">
                  <div>
                    <span className="text-muted-foreground">Name:</span>{" "}
                    {name || meta?.name}
                  </div>
                  <div>
                    <span className="text-muted-foreground">VMID:</span>{" "}
                    {vmid || "auto"}
                  </div>
                  <div>
                    <span className="text-muted-foreground">Target node:</span>{" "}
                    {targetNode}
                  </div>
                  <div>
                    <span className="text-muted-foreground">
                      Target storage:
                    </span>{" "}
                    {targetStorage}
                  </div>
                  <div>
                    <span className="text-muted-foreground">
                      Working storage:
                    </span>{" "}
                    {workingStorage || "target default"}
                  </div>
                  <div>
                    <span className="text-muted-foreground">Disks:</span>{" "}
                    {diskEntries.length}
                  </div>
                  <div>
                    <span className="text-muted-foreground">CPU:</span>{" "}
                    {cores || meta?.cores || "?"} core(s)
                    {sockets ? ` × ${sockets} socket(s)` : ""}
                  </div>
                  <div>
                    <span className="text-muted-foreground">Memory:</span>{" "}
                    {memory || meta?.memory
                      ? `${memory || String(meta?.memory)} MiB`
                      : "?"}
                  </div>
                  <div>
                    <span className="text-muted-foreground">Bridge:</span>{" "}
                    {bridge
                      ? `${bridge}${vlan ? ` (VLAN ${vlan})` : ""}`
                      : "none"}
                  </div>
                  <div>
                    <span className="text-muted-foreground">Pool:</span>{" "}
                    {pool || "none"}
                  </div>
                </div>
                <label className="flex items-center gap-2">
                  <Checkbox
                    checked={startAfter}
                    disabled={liveImport}
                    onCheckedChange={(v) => {
                      setStartAfter(v === true);
                    }}
                  />
                  <span>Start the VM after the import completes</span>
                </label>
                <label className="flex items-start gap-2">
                  <Checkbox
                    checked={liveImport}
                    onCheckedChange={(v) => {
                      const on = v === true;
                      setLiveImport(on);
                      if (on) setStartAfter(false);
                    }}
                  />
                  <span>
                    Live import (boot while disks stream in)
                    <span className="block text-xs text-amber-600">
                      If the import fails, all data written since it started is
                      lost. Power off the source first; test on a throwaway VM
                      before relying on this.
                    </span>
                  </span>
                </label>
                {startMutation.isError && (
                  <p className="text-destructive">
                    Import failed: {startMutation.error.message}
                  </p>
                )}
              </div>
            )}
          </div>
        )}

        {!importUpid && (
          <DialogFooter>
            <Button type="button" variant="outline" onClick={resetAndClose}>
              Cancel
            </Button>
            {stepIdx > 0 && (
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  const prev = steps[stepIdx - 1];
                  if (prev) setStep(prev);
                }}
              >
                Back
              </Button>
            )}
            {step === "review" ? (
              <Button
                type="button"
                disabled={startMutation.isPending || !vmidValid}
                onClick={submitImport}
              >
                {startMutation.isPending ? "Importing…" : "Start import"}
              </Button>
            ) : step === "source" ? (
              <Button
                type="button"
                disabled={!canProceed || metadataMutation.isPending}
                onClick={goToInspect}
              >
                {metadataMutation.isPending ? "Reading…" : "Next"}
              </Button>
            ) : (
              <Button
                type="button"
                disabled={!canProceed || (step === "target" && !vmidValid)}
                onClick={() => {
                  const next = steps[stepIdx + 1];
                  if (next) setStep(next);
                }}
              >
                Next
              </Button>
            )}
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  );
}
