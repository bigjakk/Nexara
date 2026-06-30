import { useMemo, useState } from "react";
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
import { useDownloadURL } from "@/features/storage/api/storage-queries";
import {
  useImportMetadata,
  useImportSourceContent,
  useStartImport,
} from "../api/import-queries";
import { TaskProgressBanner } from "./TaskProgressBanner";
import type {
  ImportMetadataResponse,
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

type Step = "source" | "inspect" | "target" | "review";
const steps: Step[] = ["source", "inspect", "target", "review"];
const stepLabels: Record<Step, string> = {
  source: "Source",
  inspect: "Inspect",
  target: "Target",
  review: "Review",
};

const fileBasedTypes = new Set(["dir", "nfs", "cifs"]);

function formatBytes(bytes: number): string {
  if (!bytes || bytes <= 0) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i] ?? ""}`;
}

export function ImportVMDialog({ open, onOpenChange, clusterId }: ImportVMDialogProps) {
  const { data: nodes } = useClusterNodes(clusterId);
  const { data: storageList } = useClusterStorage(clusterId);

  const [step, setStep] = useState<Step>("source");

  // Source selection
  const [sourceMode, setSourceMode] = useState<"storage" | "url">("storage");
  const [sourceStorageId, setSourceStorageId] = useState("");
  const [selectedVolume, setSelectedVolume] = useState("");
  const [downloadUrl, setDownloadUrl] = useState("");
  const [downloadFilename, setDownloadFilename] = useState("");
  const [downloadUpid, setDownloadUpid] = useState<string | null>(null);

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

  const [importUpid, setImportUpid] = useState<string | null>(null);

  const { data: sourceContent } = useImportSourceContent(
    clusterId,
    sourceStorageId || null,
  );
  const metadataMutation = useImportMetadata();
  const downloadMutation = useDownloadURL();
  const startMutation = useStartImport();

  const nodeName = useMemo(() => {
    const map = new Map<string, string>();
    (nodes ?? []).forEach((n) => map.set(n.id, n.name));
    return (id: string) => map.get(id) ?? id;
  }, [nodes]);

  const importStorages = useMemo(
    () =>
      (storageList ?? []).filter(
        (s) => s.enabled && s.content.includes("import"),
      ),
    [storageList],
  );

  const sourceNode = sourceContent?.node ?? "";
  const sourceShared = sourceContent?.shared ?? false;
  const sourceStorageName = sourceContent?.storage ?? "";
  const sourceStorageType = useMemo(
    () => importStorages.find((s) => s.id === sourceStorageId)?.type ?? "",
    [importStorages, sourceStorageId],
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

  const { data: bridges } = useNodeBridges(clusterId, targetNode);

  function resetAndClose() {
    setStep("source");
    setSourceMode("storage");
    setSourceStorageId("");
    setSelectedVolume("");
    setDownloadUrl("");
    setDownloadFilename("");
    setDownloadUpid(null);
    setTargetNode("");
    setTargetStorage("");
    setWorkingStorage("");
    setBridge("");
    setVmid("");
    setName("");
    setDiskFormat("");
    setStartAfter(false);
    setLiveImport(false);
    setImportUpid(null);
    metadataMutation.reset();
    startMutation.reset();
    onOpenChange(false);
  }

  function inferSourceFormat(volid: string): string {
    if (sourceStorageType === "esxi") return "esxi";
    const lower = volid.toLowerCase();
    if (lower.endsWith(".ova")) return "ova";
    if (lower.endsWith(".ovf")) return "ovf";
    if (lower.endsWith(".vmdk")) return "vmdk";
    if (lower.endsWith(".qcow2")) return "qcow2";
    if (lower.endsWith(".vhdx")) return "vhdx";
    return "";
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
          setStep("inspect");
        },
      },
    );
  }

  function triggerDownload() {
    if (!downloadUrl || !downloadFilename || !sourceStorageId) return;
    downloadMutation.mutate(
      {
        clusterId,
        storageId: sourceStorageId,
        data: { url: downloadUrl, content: "import", filename: downloadFilename },
      },
      {
        onSuccess: (res) => {
          setDownloadUpid(res.upid || null);
        },
      },
    );
  }

  function submitImport() {
    const body: StartImportRequest = {
      node: sourceNode,
      storage: sourceStorageName,
      volume: selectedVolume,
      source_format: inferSourceFormat(selectedVolume),
      source_acquisition: sourceStorageType === "esxi" ? "esxi" : "staged",
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
    <Dialog open={open} onOpenChange={(o) => { if (!o) resetAndClose(); }}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Import VM</DialogTitle>
          <DialogDescription>
            Import a virtual machine from an OVA/OVF appliance, a disk image, or an ESXi/vCenter host.
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
          <div className="max-h-[60vh] space-y-4 overflow-y-auto pr-1">
            {step === "source" && (
              <div className="space-y-4">
                <div className="flex gap-2">
                  <Button
                    type="button"
                    size="sm"
                    variant={sourceMode === "storage" ? "default" : "outline"}
                    onClick={() => { setSourceMode("storage"); }}
                  >
                    From import storage
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant={sourceMode === "url" ? "default" : "outline"}
                    onClick={() => { setSourceMode("url"); }}
                  >
                    Download OVA from URL
                  </Button>
                </div>

                {sourceMode === "url" && (
                  <div className="space-y-2">
                    <Label>OVA URL</Label>
                    <Input
                      value={downloadUrl}
                      onChange={(e) => { setDownloadUrl(e.target.value); }}
                      placeholder="https://example.com/appliance.ova"
                    />
                    <Label>Filename</Label>
                    <Input
                      value={downloadFilename}
                      onChange={(e) => { setDownloadFilename(e.target.value); }}
                      placeholder="appliance.ova"
                    />
                  </div>
                )}

                <div className="space-y-2">
                  <Label>{sourceMode === "url" ? "Download to storage" : "Import storage"}</Label>
                  <select
                    className={selectClass}
                    value={sourceStorageId}
                    onChange={(e) => {
                      setSourceStorageId(e.target.value);
                      setSelectedVolume("");
                      // The node/shared flag comes from the chosen source, so any
                      // prior target placement is no longer valid.
                      setTargetNode("");
                      setTargetStorage("");
                      setWorkingStorage("");
                      setBridge("");
                      setName("");
                    }}
                  >
                    <option value="">Select a storage…</option>
                    {importStorages.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.storage} — {nodeName(s.node_id)}
                        {s.type === "esxi" ? " (ESXi)" : ""}
                      </option>
                    ))}
                  </select>
                  {importStorages.length === 0 && (
                    <p className="text-xs text-muted-foreground">
                      No import-capable storage found. Add a directory/NFS storage with the
                      &quot;import&quot; content type, or register an ESXi source in Proxmox.
                    </p>
                  )}
                </div>

                {sourceMode === "url" && sourceStorageId && (
                  <div className="space-y-2 rounded-md border border-border p-3">
                    <Button
                      type="button"
                      size="sm"
                      disabled={!downloadUrl || !downloadFilename || downloadMutation.isPending}
                      onClick={triggerDownload}
                    >
                      {downloadMutation.isPending ? "Starting…" : "Download to storage"}
                    </Button>
                    {downloadUpid && (
                      <TaskProgressBanner
                        clusterId={clusterId}
                        upid={downloadUpid}
                        description="Downloading OVA"
                      />
                    )}
                    <p className="text-xs text-muted-foreground">
                      After the download completes, select the file below.
                    </p>
                  </div>
                )}

                {sourceStorageId && (
                  <div className="space-y-2">
                    <Label>Source guest / image</Label>
                    <select
                      className={selectClass}
                      value={selectedVolume}
                      onChange={(e) => { setSelectedVolume(e.target.value); }}
                    >
                      <option value="">Select a source…</option>
                      {(sourceContent?.items ?? []).map((item) => (
                        <option key={item.volid} value={item.volid}>
                          {item.volid.split("/").pop() ?? item.volid}
                          {item.size > 0 ? ` (${formatBytes(item.size)})` : ""}
                        </option>
                      ))}
                    </select>
                  </div>
                )}

                {metadataMutation.isError && (
                  <p className="text-sm text-destructive">
                    Failed to read import metadata: {metadataMutation.error.message}
                  </p>
                )}
              </div>
            )}

            {step === "inspect" && meta && (
              <div className="space-y-4">
                <div className="grid grid-cols-2 gap-3 text-sm">
                  <div><span className="text-muted-foreground">Name:</span> {meta.name || "—"}</div>
                  <div><span className="text-muted-foreground">OS type:</span> {meta.ostype || "—"}</div>
                  <div><span className="text-muted-foreground">Cores:</span> {meta.cores || "—"}</div>
                  <div><span className="text-muted-foreground">Memory:</span> {meta.memory ? `${String(meta.memory)} MiB` : "—"}</div>
                  <div><span className="text-muted-foreground">Source:</span> {meta.source || "—"}</div>
                </div>
                <div className="space-y-1">
                  <Label>Disks ({diskEntries.length})</Label>
                  <div className="rounded-md border border-border text-sm">
                    {diskEntries.length === 0 && (
                      <div className="px-3 py-2 text-muted-foreground">No disks detected.</div>
                    )}
                    {diskEntries.map(([slot, disk]) => (
                      <div key={slot} className="flex justify-between border-b border-border px-3 py-1.5 last:border-b-0">
                        <span className="font-mono">{slot}</span>
                        <span className="truncate text-muted-foreground">{disk.volid}</span>
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
                      <option key={n.id} value={n.name}>{n.name}</option>
                    ))}
                  </select>
                  {sourceNode !== "" && !sourceShared && (
                    <p className="text-xs text-muted-foreground">
                      The source storage is not shared, so the import must run on node {sourceNode}.
                    </p>
                  )}
                </div>
                <div className="space-y-2">
                  <Label>Target storage (disks)</Label>
                  <select
                    className={selectClass}
                    value={targetStorage}
                    onChange={(e) => { setTargetStorage(e.target.value); }}
                  >
                    <option value="">Select a storage…</option>
                    {imageStorageOptions.map((s) => (
                      <option key={s} value={s}>{s}</option>
                    ))}
                  </select>
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-2">
                    <Label>Working storage (optional)</Label>
                    <select
                      className={selectClass}
                      value={workingStorage}
                      onChange={(e) => { setWorkingStorage(e.target.value); }}
                    >
                      <option value="">Default (target storage)</option>
                      {workingStorageOptions.map((s) => (
                        <option key={s} value={s}>{s}</option>
                      ))}
                    </select>
                  </div>
                  <div className="space-y-2">
                    <Label>Network bridge (optional)</Label>
                    <select
                      className={selectClass}
                      value={bridge}
                      onChange={(e) => { setBridge(e.target.value); }}
                    >
                      <option value="">No NIC</option>
                      {(bridges ?? []).map((b) => (
                        <option key={b.iface} value={b.iface}>{b.iface}</option>
                      ))}
                    </select>
                  </div>
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-2">
                    <Label>VMID (blank = auto)</Label>
                    <Input
                      value={vmid}
                      onChange={(e) => { setVmid(e.target.value.replace(/[^0-9]/g, "")); }}
                      placeholder="auto"
                    />
                  </div>
                  <div className="space-y-2">
                    <Label>Name</Label>
                    <Input value={name} onChange={(e) => { setName(e.target.value); }} />
                  </div>
                </div>
                <div className="space-y-2">
                  <Label>Disk format (optional)</Label>
                  <select
                    className={selectClass}
                    value={diskFormat}
                    onChange={(e) => { setDiskFormat(e.target.value); }}
                  >
                    <option value="">Storage default</option>
                    <option value="qcow2">qcow2</option>
                    <option value="raw">raw</option>
                    <option value="vmdk">vmdk</option>
                  </select>
                </div>
              </div>
            )}

            {step === "review" && (
              <div className="space-y-3 text-sm">
                <div className="grid grid-cols-2 gap-2">
                  <div><span className="text-muted-foreground">Name:</span> {name || meta?.name}</div>
                  <div><span className="text-muted-foreground">VMID:</span> {vmid || "auto"}</div>
                  <div><span className="text-muted-foreground">Target node:</span> {targetNode}</div>
                  <div><span className="text-muted-foreground">Target storage:</span> {targetStorage}</div>
                  <div><span className="text-muted-foreground">Disks:</span> {diskEntries.length}</div>
                  <div><span className="text-muted-foreground">Bridge:</span> {bridge || "none"}</div>
                </div>
                <label className="flex items-center gap-2">
                  <Checkbox checked={startAfter} onCheckedChange={(v) => { setStartAfter(v === true); }} />
                  <span>Start the VM after the import completes</span>
                </label>
                <label className="flex items-start gap-2">
                  <Checkbox checked={liveImport} onCheckedChange={(v) => { setLiveImport(v === true); }} />
                  <span>
                    Live import (boot while disks stream in)
                    <span className="block text-xs text-amber-600">
                      If the import fails, all data written since it started is lost. Power off the
                      source first; test on a throwaway VM before relying on this.
                    </span>
                  </span>
                </label>
                {startMutation.isError && (
                  <p className="text-destructive">Import failed: {startMutation.error.message}</p>
                )}
              </div>
            )}
          </div>
        )}

        {!importUpid && (
          <DialogFooter>
            <Button type="button" variant="outline" onClick={resetAndClose}>
              {step === "review" ? "Cancel" : "Cancel"}
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
              <Button type="button" disabled={startMutation.isPending} onClick={submitImport}>
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
                disabled={!canProceed}
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
