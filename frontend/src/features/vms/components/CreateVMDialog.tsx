import React, { useEffect, useMemo, useState, useRef } from "react";
import { useNavigate } from "react-router-dom";
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
import {
  useClusterNodes,
  useClusterStorage,
  useNodeBridges,
  useClusterVMs,
  useMachineTypes,
} from "@/features/clusters/api/cluster-queries";
import { useStorageContent } from "@/features/storage/api/storage-queries";
import { useClusterVMIDs, useCreateVM, useResourcePools } from "../api/vm-queries";
import { apiClient } from "@/lib/api-client";
import type { NodeResponse, VMResponse } from "@/types/api";
import { TaskProgressBanner } from "./TaskProgressBanner";
import {
  osTypes,
  cpuTypes,
  scsiControllers,
  netModels,
  diskFormats,
  cacheModes,
  diskBusTypes,
  osDefaults,
  isWindowsOS,
} from "../lib/vm-config-constants";

interface CreateVMDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
}

interface DiskEntry {
  id: number;
  bus: "scsi" | "ide" | "sata" | "virtio";
  size: string;
  storage: string;
  format: string;
  cache: string;
  discard: boolean;
  ssd: boolean;
  iothread: boolean;
}

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

type Step =
  | "general"
  | "os"
  | "system"
  | "disks"
  | "cpu"
  | "memory"
  | "network"
  | "cloudinit"
  | "confirm";

const steps: Step[] = [
  "general",
  "os",
  "system",
  "disks",
  "cpu",
  "memory",
  "network",
  "cloudinit",
  "confirm",
];

const stepLabels: Record<Step, string> = {
  general: "General",
  os: "OS",
  system: "System",
  disks: "Disks",
  cpu: "CPU",
  memory: "Memory",
  network: "Network",
  cloudinit: "Cloud-Init",
  confirm: "Confirm",
};


export function CreateVMDialog({
  open,
  onOpenChange,
  clusterId,
}: CreateVMDialogProps) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data: nodes } = useClusterNodes(clusterId);
  const { data: storageList } = useClusterStorage(clusterId);
  const { data: usedVMIDs } = useClusterVMIDs(clusterId);
  const { data: clusterVMs } = useClusterVMs(clusterId);
  const { data: resourcePools } = useResourcePools(clusterId);
  const createMutation = useCreateVM();
  const pollTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // Storage pools filtered for disk images
  const imageStorageOptions = useMemo(
    () =>
      storageList
        ? [
            ...new Set(
              storageList
                .filter(
                  (s) =>
                    s.active && s.enabled && s.content.includes("images"),
                )
                .map((s) => s.storage),
            ),
          ].sort()
        : [],
    [storageList],
  );

  // Storage pools filtered for ISO images — track id+name for API calls
  const isoStoragePools = useMemo(
    () => {
      if (!storageList) return [];
      const seen = new Set<string>();
      return storageList
        .filter((s) => {
          if (!s.active || !s.enabled || !s.content.includes("iso")) return false;
          if (seen.has(s.storage)) return false;
          seen.add(s.storage);
          return true;
        })
        .sort((a, b) => a.storage.localeCompare(b.storage));
    },
    [storageList],
  );

  // Best available node: sort by available resources (total - allocated)
  const bestNode = useMemo(() => {
    if (!nodes || nodes.length === 0) return "";
    if (!clusterVMs) return "";

    // Calculate allocated CPU/mem per node
    const allocated = new Map<string, { cpu: number; mem: number }>();
    for (const vm of clusterVMs) {
      // Find which node this VM belongs to
      const nodeEntry = nodes.find((n: NodeResponse) => n.id === vm.node_id);
      if (!nodeEntry) continue;
      const existing = allocated.get(nodeEntry.name) ?? { cpu: 0, mem: 0 };
      existing.cpu += vm.cpu_count;
      existing.mem += vm.mem_total;
      allocated.set(nodeEntry.name, existing);
    }

    // Score nodes by remaining headroom (weighted: 50% CPU, 50% memory)
    let best = nodes[0];
    let bestScore = -1;
    for (const n of nodes) {
      if (n.status !== "online") continue;
      const alloc = allocated.get(n.name) ?? { cpu: 0, mem: 0 };
      const cpuFree = n.cpu_count > 0 ? (n.cpu_count - alloc.cpu) / n.cpu_count : 0;
      const memFree = n.mem_total > 0 ? (n.mem_total - alloc.mem) / n.mem_total : 0;
      const score = cpuFree * 0.5 + memFree * 0.5;
      if (score > bestScore) {
        bestScore = score;
        best = n;
      }
    }
    return best?.name ?? "";
  }, [nodes, clusterVMs]);

  const nextAvailableId = useMemo(() => {
    if (!usedVMIDs || usedVMIDs.size === 0) return 100;
    let candidate = 100;
    while (usedVMIDs.has(candidate)) candidate++;
    return candidate;
  }, [usedVMIDs]);

  // --- State ---
  const [step, setStep] = useState<Step>("general");
  const [upid, setUpid] = useState<string | null>(null);

  // General
  const [vmid, setVmid] = useState("");
  const [name, setName] = useState("");
  const [node, setNode] = useState("");
  const [pool, setPool] = useState("");
  const [tags, setTags] = useState("");
  const [description, setDescription] = useState("");

  // OS
  const [ostype, setOstype] = useState("l26");
  const [isoStorageId, setIsoStorageId] = useState(""); // UUID for API call
  const [isoImage, setIsoImage] = useState("");

  // System
  const [bios, setBios] = useState("seabios");
  const [machine, setMachine] = useState("pc");
  const [scsihw, setScsihw] = useState("virtio-scsi-pci");
  const [agentEnabled, setAgentEnabled] = useState(true);
  const [agentFstrim, setAgentFstrim] = useState(true);
  const [efiDisk, setEfiDisk] = useState(false);
  const [efiStorage, setEfiStorage] = useState("");
  const [tpmEnabled, setTpmEnabled] = useState(false);
  const [tpmStorage, setTpmStorage] = useState("");

  // Disks — array of disk entries
  const [disks, setDisks] = useState<DiskEntry[]>([
    { id: 1, bus: "scsi", size: "32", storage: "", format: "qcow2", cache: "none", discard: true, ssd: true, iothread: true },
  ]);
  const [nextDiskId, setNextDiskId] = useState(2);

  // VirtIO drivers ISO (Windows only)
  const [virtioDrivers, setVirtioDrivers] = useState(false);
  const [virtioIsoStorageId, setVirtioIsoStorageId] = useState("");
  const [virtioIsoImage, setVirtioIsoImage] = useState("");

  // CPU
  const [cores, setCores] = useState("2");
  const [sockets, setSockets] = useState("1");
  const [cpuType, setCpuType] = useState("x86-64-v2-AES");
  const [numa, setNuma] = useState(false);

  // Memory
  const [memory, setMemory] = useState("2048");
  const [balloonEnabled, setBalloonEnabled] = useState(true);
  const [balloonMin, setBalloonMin] = useState("");

  // Network
  const [bridge, setBridge] = useState("vmbr0");
  const [netModel, setNetModel] = useState("virtio");
  const [vlan, setVlan] = useState("");
  const [firewall, setFirewall] = useState(true);
  const [rateLimit, setRateLimit] = useState("");
  const [macAddress, setMacAddress] = useState("");
  const [mtu, setMtu] = useState("");
  const [multiqueue, setMultiqueue] = useState("");

  // Cloud-Init
  const [ciuser, setCiuser] = useState("");
  const [cipassword, setCipassword] = useState("");
  const [sshkeys, setSshkeys] = useState("");
  const [ipconfig0, setIpconfig0] = useState("");
  const [nameserver, setNameserver] = useState("");
  const [searchdomain, setSearchdomain] = useState("");

  // Confirm
  const [startAfter, setStartAfter] = useState(false);
  const [onboot, setOnboot] = useState(false);

  // Fetch ISO content when isoStorage selected (using UUID)
  const { data: isoContent } = useStorageContent(clusterId, isoStorageId);
  const isoList = useMemo(
    () =>
      isoContent
        ? isoContent.filter((item) => item.content === "iso")
        : [],
    [isoContent],
  );

  // Fetch VirtIO drivers ISO content
  const { data: virtioIsoContent } = useStorageContent(clusterId, virtioIsoStorageId);
  const virtioIsoList = useMemo(
    () =>
      virtioIsoContent
        ? virtioIsoContent.filter((item) => item.content === "iso")
        : [],
    [virtioIsoContent],
  );

  // Fetch network bridges for the selected node
  const { data: bridges } = useNodeBridges(clusterId, node);

  // Fetch machine types for the selected node
  const { data: rawMachineTypes } = useMachineTypes(clusterId, node);
  const machineOptions = useMemo(() => {
    if (!rawMachineTypes || rawMachineTypes.length === 0) {
      return [
        { value: "pc", label: "i440fx (Default)" },
        { value: "q35", label: "q35" },
      ];
    }
    // Group by base type (q35 vs i440fx), prefer +pve variants, sort by version desc
    const pveTypes = rawMachineTypes
      .filter((mt) => mt.id.includes("+pve"))
      .sort((a, b) => b.id.localeCompare(a.id));
    const result: { value: string; label: string }[] = [];
    // Add the latest +pve q35 and i440fx as recommended options
    const q35pve = pveTypes.find((mt) => mt.id.includes("q35"));
    const i440pve = pveTypes.find((mt) => mt.id.includes("i440fx"));
    if (q35pve) result.push({ value: q35pve.id, label: `${q35pve.id} (Recommended)` });
    if (i440pve) result.push({ value: i440pve.id, label: i440pve.id });
    // Add all +pve variants
    for (const mt of pveTypes) {
      if (mt.id !== q35pve?.id && mt.id !== i440pve?.id) {
        result.push({ value: mt.id, label: mt.id });
      }
    }
    // Add generic fallbacks at the end
    result.push({ value: "q35", label: "q35 (generic)" });
    result.push({ value: "pc", label: "i440fx (generic)" });
    return result;
  }, [rawMachineTypes]);

  // Initialize defaults on open — use best available node
  useEffect(() => {
    if (open && vmid === "" && usedVMIDs) {
      setVmid(String(nextAvailableId));
    }
    if (open && node === "" && bestNode) {
      setNode(bestNode);
    }
  }, [open, usedVMIDs, nextAvailableId, vmid, bestNode, node]);

  // When bridges load, default to first active bridge
  useEffect(() => {
    if (bridges && bridges.length > 0 && bridge === "vmbr0") {
      const activeBridge = bridges.find((b) => b.active);
      if (activeBridge) {
        setBridge(activeBridge.iface);
      }
    }
  }, [bridges, bridge]);

  // When machine types load, default to the recommended +pve q35 variant
  useEffect(() => {
    if (machineOptions.length > 0 && (machine === "pc" || machine === "q35")) {
      const recommended = machineOptions[0];
      if (recommended && recommended.value !== "pc" && recommended.value !== "q35") {
        setMachine(recommended.value);
      }
    }
  }, [machineOptions, machine]);

  // Apply OS-type defaults when ostype changes
  function applyOSDefaults(newOstype: string) {
    const defaults = osDefaults[newOstype] ?? osDefaults["other"];
    if (!defaults) return;

    // Disk bus — update first disk only
    setDisks((prev) => {
      const first = prev[0];
      if (!first) return prev;
      return [{ ...first, bus: defaults.diskBus }, ...prev.slice(1)];
    });

    // Network model
    setNetModel(defaults.netModel);

    // SCSI controller
    if (defaults.scsihw) {
      setScsihw(defaults.scsihw);
    } else {
      setScsihw("virtio-scsi-pci");
    }

    // BIOS
    setBios(defaults.bios);

    // Machine type — pick +pve variant matching the base
    const base = defaults.machineBase;
    const pveMatch = machineOptions.find(
      (mt) => mt.value.includes(base) && mt.value.includes("+pve"),
    );
    setMachine(pveMatch?.value ?? base);

    // EFI disk
    setEfiDisk(defaults.efiDisk);

    // TPM
    setTpmEnabled(defaults.tpm);

    // VirtIO drivers — reset when switching away from Windows
    if (!isWindowsOS(newOstype)) {
      setVirtioDrivers(false);
      setVirtioIsoStorageId("");
      setVirtioIsoImage("");
    }
  }

  // Cleanup poll timer on unmount
  useEffect(() => {
    return () => {
      if (pollTimerRef.current) clearInterval(pollTimerRef.current);
    };
  }, []);

  const isDuplicate = usedVMIDs ? usedVMIDs.has(Number(vmid)) : false;
  const stepIdx = steps.indexOf(step);
  const isWindows = isWindowsOS(ostype);

  // Disk management helpers
  function addDisk() {
    setDisks((prev) => [
      ...prev,
      {
        id: nextDiskId,
        bus: prev[0]?.bus ?? "scsi",
        size: "32",
        storage: prev[0]?.storage ?? "",
        format: "qcow2",
        cache: "none",
        discard: true,
        ssd: true,
        iothread: true,
      },
    ]);
    setNextDiskId((n) => n + 1);
  }

  function removeDisk(id: number) {
    setDisks((prev) => prev.filter((d) => d.id !== id));
  }

  function updateDisk(id: number, changes: Partial<DiskEntry>) {
    setDisks((prev) =>
      prev.map((d) => (d.id === id ? { ...d, ...changes } : d)),
    );
  }

  // Build disk string for Proxmox
  function buildDiskString(disk: DiskEntry): string {
    if (!disk.storage || !disk.size) return "";
    const parts = [`${disk.storage}:${disk.size}`];
    if (disk.format !== "qcow2") parts.push(`format=${disk.format}`);
    if (disk.cache !== "none") parts.push(`cache=${disk.cache}`);
    if (disk.discard) parts.push("discard=on");
    if (disk.ssd) parts.push("ssd=1");
    if (disk.iothread) parts.push("iothread=1");
    return parts.join(",");
  }

  // Assign device names (e.g. scsi0, scsi1, ide0) — skipping IDE slots used by ISOs
  function assignDiskDeviceNames(): { name: string; disk: DiskEntry }[] {
    const counters: Record<string, number> = { scsi: 0, ide: 0, sata: 0, virtio: 0 };
    const result: { name: string; disk: DiskEntry }[] = [];
    for (const disk of disks) {
      let idx = counters[disk.bus] ?? 0;
      // Skip IDE slots reserved for ISOs
      if (disk.bus === "ide") {
        while (idx === 2 || (idx === 3 && virtioDrivers)) {
          idx++;
        }
      }
      result.push({ name: `${disk.bus}${idx}`, disk });
      counters[disk.bus] = idx + 1;
    }
    return result;
  }

  function buildNet0(): string {
    const parts: string[] = [];
    if (macAddress) {
      parts.push(`${netModel}=${macAddress}`);
    } else {
      parts.push(netModel);
    }
    parts.push(`bridge=${bridge}`);
    if (firewall) parts.push("firewall=1");
    if (vlan) parts.push(`tag=${vlan}`);
    if (rateLimit) parts.push(`rate=${rateLimit}`);
    if (mtu) parts.push(`mtu=${mtu}`);
    if (multiqueue) parts.push(`queues=${multiqueue}`);
    return parts.join(",");
  }

  function buildAgent(): string {
    if (!agentEnabled) return "";
    const parts = ["enabled=1"];
    if (agentFstrim) parts.push("fstrim_cloned_disks=1");
    return parts.join(",");
  }

  function buildEfiDisk0(): string {
    if (!efiDisk || !efiStorage) return "";
    return `${efiStorage}:1,format=qcow2,efitype=4m,pre-enrolled-keys=1`;
  }

  function buildTpmState0(): string {
    if (!tpmEnabled || !tpmStorage) return "";
    return `${tpmStorage}:1,version=v2.0`;
  }

  function handleSubmit() {
    // Assign device names and build disk strings
    const diskAssignments = assignDiskDeviceNames();
    const extra: Record<string, string> = {};

    // Put first disk in the primary scsi0 field if it's scsi, otherwise in extra
    let primaryDiskField: string | undefined;
    for (const { name: devName, disk } of diskAssignments) {
      const diskStr = buildDiskString(disk);
      if (!diskStr) continue;
      if (devName === "scsi0") {
        primaryDiskField = diskStr;
      } else {
        extra[devName] = diskStr;
      }
    }

    // OS ISO on ide2
    const ide2 = isoImage ? `${isoImage},media=cdrom` : "";

    // VirtIO drivers ISO on ide3
    if (virtioDrivers && virtioIsoImage) {
      extra["ide3"] = `${virtioIsoImage},media=cdrom`;
    }

    // Build boot order from disk device names
    const bootDevices: string[] = [];
    if (diskAssignments.length > 0 && diskAssignments[0]) {
      bootDevices.push(diskAssignments[0].name);
    }
    bootDevices.push("ide2");
    bootDevices.push("net0");
    const bootOrder = `order=${bootDevices.join(";")}`;

    const body: import("../types/vm").CreateVMRequest = {
      vmid: Number(vmid),
      name,
      node,
      cores: Number(cores),
      sockets: Number(sockets),
      memory: Number(memory),
      net0: buildNet0(),
      ostype,
      boot: bootOrder,
      start: startAfter,
    };
    if (primaryDiskField) body.scsi0 = primaryDiskField;
    if (ide2) body.ide2 = ide2;
    if (Object.keys(extra).length > 0) body.extra = extra;

    // System
    if (bios !== "seabios") body.bios = bios;
    body.machine = machine;
    body.scsihw = scsihw;
    const efi = buildEfiDisk0();
    if (efi) body.efidisk0 = efi;
    const tpm = buildTpmState0();
    if (tpm) body.tpmstate0 = tpm;
    const ag = buildAgent();
    if (ag) body.agent = ag;
    // CPU
    if (cpuType !== "kvm64") body.cpu = cpuType;
    if (numa) body.numa = true;
    // Memory
    if (!balloonEnabled) {
      body.balloon = 0;
    } else if (balloonMin) {
      body.balloon = Number(balloonMin);
    }
    // Boot / Options
    if (onboot) body.onboot = true;
    // Cloud-Init
    if (ciuser) body.ciuser = ciuser;
    if (cipassword) body.cipassword = cipassword;
    if (sshkeys) body.sshkeys = sshkeys;
    if (ipconfig0) body.ipconfig0 = ipconfig0;
    if (nameserver) body.nameserver = nameserver;
    if (searchdomain) body.searchdomain = searchdomain;
    // Meta
    if (description) body.description = description;
    if (tags) body.tags = tags;
    if (pool) body.pool = pool;

    createMutation.mutate(
      { clusterId, body },
      {
        onSuccess: (data) => {
          setUpid(data.upid);
        },
      },
    );
  }

  function handleClose() {
    setStep("general");
    setUpid(null);
    setVmid("");
    setName("");
    setNode("");
    setPool("");
    setTags("");
    setDescription("");
    setOstype("l26");
    setIsoStorageId("");
    setIsoImage("");
    setBios("seabios");
    setMachine("pc");
    setScsihw("virtio-scsi-pci");
    setAgentEnabled(true);
    setAgentFstrim(true);
    setEfiDisk(false);
    setEfiStorage("");
    setTpmEnabled(false);
    setTpmStorage("");
    setDisks([{ id: 1, bus: "scsi", size: "32", storage: "", format: "qcow2", cache: "none", discard: true, ssd: true, iothread: true }]);
    setNextDiskId(2);
    setVirtioDrivers(false);
    setVirtioIsoStorageId("");
    setVirtioIsoImage("");
    setCores("2");
    setSockets("1");
    setCpuType("x86-64-v2-AES");
    setNuma(false);
    setMemory("2048");
    setBalloonEnabled(true);
    setBalloonMin("");
    setBridge("vmbr0");
    setNetModel("virtio");
    setVlan("");
    setFirewall(true);
    setRateLimit("");
    setMacAddress("");
    setMtu("");
    setMultiqueue("");
    setCiuser("");
    setCipassword("");
    setSshkeys("");
    setIpconfig0("");
    setNameserver("");
    setSearchdomain("");
    setStartAfter(false);
    setOnboot(false);
    createMutation.reset();
    onOpenChange(false);
  }

  const canProceed =
    step === "general"
      ? Number(vmid) > 0 && node !== ""
      : step === "cpu"
        ? Number(cores) > 0
        : step === "memory"
          ? Number(memory) > 0
          : true;

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent className="max-w-2xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Create Virtual Machine</DialogTitle>
          <DialogDescription>
            Step {stepIdx + 1} of {steps.length}:{" "}
            {stepLabels[step]}
          </DialogDescription>
        </DialogHeader>

        {/* Step navigation tabs */}
        <div className="flex flex-wrap gap-1 border-b pb-2">
          {steps.map((s, i) => (
            <button
              key={s}
              type="button"
              onClick={() => {
                setStep(s);
              }}
              className={`px-2 py-1 text-xs rounded-md transition-colors ${
                s === step
                  ? "bg-primary text-primary-foreground"
                  : i < stepIdx
                    ? "bg-muted text-foreground hover:bg-muted/80"
                    : "text-muted-foreground hover:bg-muted/50"
              }`}
            >
              {stepLabels[s]}
            </button>
          ))}
        </div>

        {upid ? (
          <TaskProgressBanner
            clusterId={clusterId}
            upid={upid}
            kind="vm"
            resourceId=""
            onComplete={() => {
              // After task completes, poll for the new VM record and navigate to it
              const createdVmid = Number(vmid);
              const pollForVM = () => {
                void queryClient
                  .invalidateQueries({ queryKey: ["clusters", clusterId, "vms"] })
                  .then(() =>
                    apiClient.get<VMResponse[]>(
                      `/api/v1/clusters/${clusterId}/vms`,
                    ),
                  )
                  .then((vms) => {
                    const found = vms.find((v) => v.vmid === createdVmid);
                    if (found) {
                      if (pollTimerRef.current) clearInterval(pollTimerRef.current);
                      pollTimerRef.current = null;
                      // Reset dialog state without closing
                      createMutation.reset();
                      onOpenChange(false);
                      navigate(`/inventory/vm/${clusterId}/${found.id}`);
                    }
                  })
                  .catch(() => {
                    // ignore, will retry
                  });
              };
              // First attempt immediately, then poll every 3s
              pollForVM();
              pollTimerRef.current = setInterval(pollForVM, 3000);
              // Safety: stop polling after 30s and just close
              setTimeout(() => {
                if (pollTimerRef.current) {
                  clearInterval(pollTimerRef.current);
                  pollTimerRef.current = null;
                  handleClose();
                }
              }, 30000);
            }}
            description={`Create VM ${name || vmid}`}
          />
        ) : (
          <div className="space-y-4">
            {/* === GENERAL === */}
            {step === "general" && (
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label>VMID</Label>
                  <Input
                    type="number"
                    min={1}
                    value={vmid}
                    onChange={(e) => {
                      setVmid(e.target.value);
                    }}
                  />
                  {isDuplicate && (
                    <p className="text-xs text-yellow-600 dark:text-yellow-500">
                      VMID may already be in use
                    </p>
                  )}
                </div>
                <div className="space-y-2">
                  <Label>Name</Label>
                  <Input
                    value={name}
                    onChange={(e) => {
                      setName(e.target.value);
                    }}
                    placeholder="my-vm"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Target Node</Label>
                  <select
                    value={node}
                    onChange={(e) => {
                      setNode(e.target.value);
                      setBridge("vmbr0"); // reset bridge when node changes
                    }}
                    className={selectClass}
                  >
                    <option value="">Select node</option>
                    {nodes?.map((n) => (
                      <option key={n.id} value={n.name}>
                        {n.name}
                        {n.name === bestNode ? " (best available)" : ""}
                        {n.status !== "online" ? " [offline]" : ""}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="space-y-2">
                  <Label>Resource Pool</Label>
                  <select
                    value={pool}
                    onChange={(e) => {
                      setPool(e.target.value);
                    }}
                    className={selectClass}
                  >
                    <option value="">None</option>
                    {resourcePools?.map((p) => (
                      <option key={p.poolid} value={p.poolid}>
                        {p.poolid}{p.comment ? ` — ${p.comment}` : ""}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="space-y-2 sm:col-span-2">
                  <Label>Tags</Label>
                  <Input
                    value={tags}
                    onChange={(e) => {
                      setTags(e.target.value);
                    }}
                    placeholder="tag1;tag2;tag3"
                  />
                </div>
                <div className="space-y-2 sm:col-span-2">
                  <Label>Description</Label>
                  <textarea
                    value={description}
                    onChange={(e) => {
                      setDescription(e.target.value);
                    }}
                    className="flex min-h-[60px] w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                    placeholder="Optional description"
                    rows={2}
                  />
                </div>
              </div>
            )}

            {/* === OS === */}
            {step === "os" && (
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2 sm:col-span-2">
                  <Label>OS Type</Label>
                  <select
                    value={ostype}
                    onChange={(e) => {
                      const newOstype = e.target.value;
                      setOstype(newOstype);
                      applyOSDefaults(newOstype);
                    }}
                    className={selectClass}
                  >
                    {osTypes.map((o) => (
                      <option key={o.value} value={o.value}>
                        {o.label}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="space-y-2">
                  <Label>ISO Storage</Label>
                  <select
                    value={isoStorageId}
                    onChange={(e) => {
                      const selectedId = e.target.value;
                      setIsoStorageId(selectedId);
                      setIsoImage("");
                    }}
                    className={selectClass}
                  >
                    <option value="">Do not use any media</option>
                    {isoStoragePools.map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.storage}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="space-y-2">
                  <Label>ISO Image</Label>
                  <select
                    value={isoImage}
                    onChange={(e) => {
                      setIsoImage(e.target.value);
                    }}
                    className={selectClass}
                    disabled={!isoStorageId}
                  >
                    <option value="">
                      {isoStorageId
                        ? isoList.length === 0
                          ? "No ISOs found"
                          : "Select ISO"
                        : "Select storage first"}
                    </option>
                    {isoList.map((iso) => (
                      <option key={iso.volid} value={iso.volid}>
                        {iso.volid.split("/").pop()}
                      </option>
                    ))}
                  </select>
                </div>
                {/* VirtIO Drivers ISO — shown for Windows OS types */}
                {isWindows && (
                  <>
                    <div className="sm:col-span-2 flex items-center gap-2 pt-2">
                      <Checkbox
                        id="virtio-drivers"
                        checked={virtioDrivers}
                        onCheckedChange={(c) => {
                          const checked = Boolean(c);
                          setVirtioDrivers(checked);
                          if (checked) {
                            // VirtIO drivers: recommend scsi bus + virtio NIC
                            setDisks((prev) => {
                              const first = prev[0];
                              if (!first) return prev;
                              return [{ ...first, bus: "scsi" as const }, ...prev.slice(1)];
                            });
                            setNetModel("virtio");
                          } else {
                            // Restore OS defaults for bus and NIC
                            const defaults = osDefaults[ostype] ?? osDefaults["other"];
                            if (defaults) {
                              setDisks((prev) => {
                                const first = prev[0];
                                if (!first) return prev;
                                return [{ ...first, bus: defaults.diskBus }, ...prev.slice(1)];
                              });
                              setNetModel(defaults.netModel);
                            }
                            setVirtioIsoStorageId("");
                            setVirtioIsoImage("");
                          }
                        }}
                      />
                      <Label htmlFor="virtio-drivers" className="text-sm font-normal">
                        Add additional drive for VirtIO drivers
                      </Label>
                    </div>
                    {virtioDrivers && (
                      <>
                        <div className="space-y-2">
                          <Label>VirtIO ISO Storage</Label>
                          <select
                            value={virtioIsoStorageId}
                            onChange={(e) => {
                              setVirtioIsoStorageId(e.target.value);
                              setVirtioIsoImage("");
                            }}
                            className={selectClass}
                          >
                            <option value="">Select storage</option>
                            {isoStoragePools.map((p) => (
                              <option key={p.id} value={p.id}>
                                {p.storage}
                              </option>
                            ))}
                          </select>
                        </div>
                        <div className="space-y-2">
                          <Label>VirtIO Drivers ISO</Label>
                          <select
                            value={virtioIsoImage}
                            onChange={(e) => {
                              setVirtioIsoImage(e.target.value);
                            }}
                            className={selectClass}
                            disabled={!virtioIsoStorageId}
                          >
                            <option value="">
                              {virtioIsoStorageId
                                ? virtioIsoList.length === 0
                                  ? "No ISOs found"
                                  : "Select VirtIO ISO"
                                : "Select storage first"}
                            </option>
                            {virtioIsoList.map((iso) => (
                              <option key={iso.volid} value={iso.volid}>
                                {iso.volid.split("/").pop()}
                              </option>
                            ))}
                          </select>
                        </div>
                        <p className="text-xs text-muted-foreground sm:col-span-2">
                          Enabling VirtIO drivers sets disk bus to SCSI and network to VirtIO for best Windows performance.
                        </p>
                      </>
                    )}
                  </>
                )}
              </div>
            )}

            {/* === SYSTEM === */}
            {step === "system" && (
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label>BIOS</Label>
                  <select
                    value={bios}
                    onChange={(e) => {
                      setBios(e.target.value);
                      if (e.target.value === "ovmf") {
                        setEfiDisk(true);
                        // Pick the best q35+pve variant, fall back to generic q35
                        const q35pve = machineOptions.find((mt) => mt.value.includes("q35") && mt.value.includes("+pve"));
                        setMachine(q35pve?.value ?? "q35");
                      } else {
                        setEfiDisk(false);
                      }
                    }}
                    className={selectClass}
                  >
                    <option value="seabios">SeaBIOS (Default)</option>
                    <option value="ovmf">OVMF (UEFI)</option>
                  </select>
                </div>
                <div className="space-y-2">
                  <Label>Machine Type</Label>
                  <select
                    value={machine}
                    onChange={(e) => {
                      setMachine(e.target.value);
                    }}
                    className={selectClass}
                  >
                    {machineOptions.map((mt) => (
                      <option key={mt.value} value={mt.value}>
                        {mt.label}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="space-y-2">
                  <Label>SCSI Controller</Label>
                  <select
                    value={scsihw}
                    onChange={(e) => {
                      setScsihw(e.target.value);
                    }}
                    className={selectClass}
                  >
                    {scsiControllers.map((sc) => (
                      <option key={sc.value} value={sc.value}>
                        {sc.label}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="space-y-2">
                  <Label>QEMU Guest Agent</Label>
                  <div className="flex flex-col gap-2 pt-1">
                    <div className="flex items-center gap-2">
                      <Checkbox
                        id="agent-enabled"
                        checked={agentEnabled}
                        onCheckedChange={(c) => {
                          setAgentEnabled(Boolean(c));
                        }}
                      />
                      <Label htmlFor="agent-enabled" className="text-sm font-normal">
                        Enable
                      </Label>
                    </div>
                    {agentEnabled && (
                      <div className="flex items-center gap-2">
                        <Checkbox
                          id="agent-fstrim"
                          checked={agentFstrim}
                          onCheckedChange={(c) => {
                            setAgentFstrim(Boolean(c));
                          }}
                        />
                        <Label htmlFor="agent-fstrim" className="text-sm font-normal">
                          TRIM on clone
                        </Label>
                      </div>
                    )}
                  </div>
                </div>
                {efiDisk && (
                  <div className="space-y-2 sm:col-span-2">
                    <Label>EFI Disk Storage</Label>
                    <select
                      value={efiStorage}
                      onChange={(e) => {
                        setEfiStorage(e.target.value);
                      }}
                      className={selectClass}
                    >
                      <option value="">Select storage</option>
                      {imageStorageOptions.map((s) => (
                        <option key={s} value={s}>
                          {s}
                        </option>
                      ))}
                    </select>
                  </div>
                )}
                <div className="space-y-2 sm:col-span-2">
                  <div className="flex items-center gap-2">
                    <Checkbox
                      id="tpm-enabled"
                      checked={tpmEnabled}
                      onCheckedChange={(c) => {
                        setTpmEnabled(Boolean(c));
                      }}
                    />
                    <Label htmlFor="tpm-enabled" className="text-sm">
                      Add TPM v2.0
                    </Label>
                  </div>
                  {tpmEnabled && (
                    <select
                      value={tpmStorage}
                      onChange={(e) => {
                        setTpmStorage(e.target.value);
                      }}
                      className={selectClass + " mt-2"}
                    >
                      <option value="">Select storage for TPM</option>
                      {imageStorageOptions.map((s) => (
                        <option key={s} value={s}>
                          {s}
                        </option>
                      ))}
                    </select>
                  )}
                </div>
              </div>
            )}

            {/* === DISKS === */}
            {step === "disks" && (
              <div className="space-y-4">
                {disks.map((disk, idx) => (
                  <div key={disk.id} className="rounded-lg border p-3 space-y-3">
                    <div className="flex items-center justify-between">
                      <span className="text-sm font-medium">
                        Disk {idx + 1}
                      </span>
                      {disks.length > 1 && (
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          className="h-7 text-xs text-destructive hover:text-destructive"
                          onClick={() => { removeDisk(disk.id); }}
                        >
                          Remove
                        </Button>
                      )}
                    </div>
                    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                      <div className="space-y-1">
                        <Label className="text-xs">Bus Type</Label>
                        <select
                          value={disk.bus}
                          onChange={(e) => { updateDisk(disk.id, { bus: e.target.value as DiskEntry["bus"] }); }}
                          className={selectClass}
                        >
                          {diskBusTypes.map((bt) => (
                            <option key={bt.value} value={bt.value}>{bt.label}</option>
                          ))}
                        </select>
                      </div>
                      <div className="space-y-1">
                        <Label className="text-xs">Storage</Label>
                        <select
                          value={disk.storage}
                          onChange={(e) => { updateDisk(disk.id, { storage: e.target.value }); }}
                          className={selectClass}
                        >
                          <option value="">Default</option>
                          {imageStorageOptions.map((s) => (
                            <option key={s} value={s}>{s}</option>
                          ))}
                        </select>
                      </div>
                      <div className="space-y-1">
                        <Label className="text-xs">Size (GB)</Label>
                        <Input
                          type="number"
                          min={1}
                          value={disk.size}
                          onChange={(e) => { updateDisk(disk.id, { size: e.target.value }); }}
                        />
                      </div>
                      <div className="space-y-1">
                        <Label className="text-xs">Format</Label>
                        <select
                          value={disk.format}
                          onChange={(e) => { updateDisk(disk.id, { format: e.target.value }); }}
                          className={selectClass}
                        >
                          {diskFormats.map((f) => (
                            <option key={f.value} value={f.value}>{f.label}</option>
                          ))}
                        </select>
                      </div>
                      <div className="space-y-1">
                        <Label className="text-xs">Cache</Label>
                        <select
                          value={disk.cache}
                          onChange={(e) => { updateDisk(disk.id, { cache: e.target.value }); }}
                          className={selectClass}
                        >
                          {cacheModes.map((cm) => (
                            <option key={cm.value} value={cm.value}>{cm.label}</option>
                          ))}
                        </select>
                      </div>
                      <div className="flex flex-wrap gap-x-4 gap-y-1 items-center pt-4">
                        <div className="flex items-center gap-1.5">
                          <Checkbox
                            id={`discard-${disk.id}`}
                            checked={disk.discard}
                            onCheckedChange={(c) => { updateDisk(disk.id, { discard: Boolean(c) }); }}
                          />
                          <Label htmlFor={`discard-${disk.id}`} className="text-xs font-normal">TRIM</Label>
                        </div>
                        <div className="flex items-center gap-1.5">
                          <Checkbox
                            id={`ssd-${disk.id}`}
                            checked={disk.ssd}
                            onCheckedChange={(c) => { updateDisk(disk.id, { ssd: Boolean(c) }); }}
                          />
                          <Label htmlFor={`ssd-${disk.id}`} className="text-xs font-normal">SSD</Label>
                        </div>
                        <div className="flex items-center gap-1.5">
                          <Checkbox
                            id={`io-${disk.id}`}
                            checked={disk.iothread}
                            onCheckedChange={(c) => { updateDisk(disk.id, { iothread: Boolean(c) }); }}
                          />
                          <Label htmlFor={`io-${disk.id}`} className="text-xs font-normal">IO Thread</Label>
                        </div>
                      </div>
                    </div>
                  </div>
                ))}
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={addDisk}
                >
                  + Add Disk
                </Button>
              </div>
            )}

            {/* === CPU === */}
            {step === "cpu" && (
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label>Sockets</Label>
                  <Input
                    type="number"
                    min={1}
                    value={sockets}
                    onChange={(e) => {
                      setSockets(e.target.value);
                    }}
                  />
                </div>
                <div className="space-y-2">
                  <Label>Cores</Label>
                  <Input
                    type="number"
                    min={1}
                    value={cores}
                    onChange={(e) => {
                      setCores(e.target.value);
                    }}
                  />
                </div>
                <div className="space-y-2 sm:col-span-2">
                  <Label>CPU Type</Label>
                  <select
                    value={cpuType}
                    onChange={(e) => {
                      setCpuType(e.target.value);
                    }}
                    className={selectClass}
                  >
                    {cpuTypes.map((ct) => (
                      <option key={ct} value={ct}>
                        {ct}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="flex items-center gap-2 sm:col-span-2">
                  <Checkbox
                    id="numa"
                    checked={numa}
                    onCheckedChange={(c) => {
                      setNuma(Boolean(c));
                    }}
                  />
                  <Label htmlFor="numa" className="text-sm font-normal">
                    Enable NUMA
                  </Label>
                </div>
                <p className="text-xs text-muted-foreground sm:col-span-2">
                  Total vCPUs: {Number(cores) * Number(sockets)}
                </p>
              </div>
            )}

            {/* === MEMORY === */}
            {step === "memory" && (
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label>Memory (MiB)</Label>
                  <Input
                    type="number"
                    min={64}
                    step={128}
                    value={memory}
                    onChange={(e) => {
                      setMemory(e.target.value);
                    }}
                  />
                  <p className="text-xs text-muted-foreground">
                    {(Number(memory) / 1024).toFixed(1)} GiB
                  </p>
                </div>
                <div className="space-y-2">
                  <div className="flex items-center gap-2">
                    <Checkbox
                      id="balloon-enabled"
                      checked={balloonEnabled}
                      onCheckedChange={(c) => {
                        setBalloonEnabled(Boolean(c));
                      }}
                    />
                    <Label htmlFor="balloon-enabled" className="text-sm">
                      Ballooning
                    </Label>
                  </div>
                  {balloonEnabled && (
                    <div className="space-y-1 pt-1">
                      <Label className="text-xs text-muted-foreground">
                        Minimum Memory (MiB)
                      </Label>
                      <Input
                        type="number"
                        min={0}
                        value={balloonMin}
                        onChange={(e) => {
                          setBalloonMin(e.target.value);
                        }}
                        placeholder="Same as max"
                      />
                    </div>
                  )}
                </div>
              </div>
            )}

            {/* === NETWORK === */}
            {step === "network" && (
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label>Model</Label>
                  <select
                    value={netModel}
                    onChange={(e) => {
                      setNetModel(e.target.value);
                    }}
                    className={selectClass}
                  >
                    {netModels.map((nm) => (
                      <option key={nm.value} value={nm.value}>
                        {nm.label}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="space-y-2">
                  <Label>Bridge</Label>
                  <select
                    value={bridge}
                    onChange={(e) => {
                      setBridge(e.target.value);
                    }}
                    className={selectClass}
                  >
                    {bridges && bridges.length > 0 ? (
                      bridges.map((b) => (
                        <option key={b.iface} value={b.iface}>
                          {b.iface}{b.cidr ? ` (${b.cidr})` : ""}{!b.active ? " [inactive]" : ""}
                        </option>
                      ))
                    ) : (
                      <option value="vmbr0">vmbr0</option>
                    )}
                  </select>
                </div>
                <div className="space-y-2">
                  <Label>VLAN Tag</Label>
                  <Input
                    type="number"
                    min={1}
                    max={4094}
                    value={vlan}
                    onChange={(e) => {
                      setVlan(e.target.value);
                    }}
                    placeholder="None"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Rate Limit (MB/s)</Label>
                  <Input
                    type="number"
                    min={0}
                    value={rateLimit}
                    onChange={(e) => {
                      setRateLimit(e.target.value);
                    }}
                    placeholder="Unlimited"
                  />
                </div>
                <div className="space-y-2">
                  <Label>MAC Address</Label>
                  <Input
                    value={macAddress}
                    onChange={(e) => {
                      setMacAddress(e.target.value);
                    }}
                    placeholder="Auto-generated"
                  />
                </div>
                <div className="space-y-2">
                  <Label>MTU</Label>
                  <Input
                    type="number"
                    min={68}
                    max={65520}
                    value={mtu}
                    onChange={(e) => {
                      setMtu(e.target.value);
                    }}
                    placeholder="Default"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Multiqueue</Label>
                  <Input
                    type="number"
                    min={0}
                    max={64}
                    value={multiqueue}
                    onChange={(e) => {
                      setMultiqueue(e.target.value);
                    }}
                    placeholder="Disabled"
                  />
                </div>
                <div className="flex items-center gap-2 pt-5">
                  <Checkbox
                    id="firewall"
                    checked={firewall}
                    onCheckedChange={(c) => {
                      setFirewall(Boolean(c));
                    }}
                  />
                  <Label htmlFor="firewall" className="text-sm font-normal">
                    Firewall
                  </Label>
                </div>
              </div>
            )}

            {/* === CLOUD-INIT === */}
            {step === "cloudinit" && (
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label>User</Label>
                  <Input
                    value={ciuser}
                    onChange={(e) => {
                      setCiuser(e.target.value);
                    }}
                    placeholder="root"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Password</Label>
                  <Input
                    type="password"
                    value={cipassword}
                    onChange={(e) => {
                      setCipassword(e.target.value);
                    }}
                    placeholder="Optional"
                  />
                </div>
                <div className="space-y-2 sm:col-span-2">
                  <Label>SSH Public Keys</Label>
                  <textarea
                    value={sshkeys}
                    onChange={(e) => {
                      setSshkeys(e.target.value);
                    }}
                    className="flex min-h-[80px] w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-sm font-mono focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                    placeholder="ssh-rsa AAAA..."
                    rows={3}
                  />
                </div>
                <div className="space-y-2 sm:col-span-2">
                  <Label>IP Config (ipconfig0)</Label>
                  <Input
                    value={ipconfig0}
                    onChange={(e) => {
                      setIpconfig0(e.target.value);
                    }}
                    placeholder="ip=dhcp or ip=10.0.0.2/24,gw=10.0.0.1"
                  />
                </div>
                <div className="space-y-2">
                  <Label>DNS Server</Label>
                  <Input
                    value={nameserver}
                    onChange={(e) => {
                      setNameserver(e.target.value);
                    }}
                    placeholder="8.8.8.8"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Search Domain</Label>
                  <Input
                    value={searchdomain}
                    onChange={(e) => {
                      setSearchdomain(e.target.value);
                    }}
                    placeholder="example.com"
                  />
                </div>
              </div>
            )}

            {/* === CONFIRM === */}
            {step === "confirm" && (
              <div className="space-y-4">
                <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 rounded-lg border p-3 text-sm">
                  <span className="text-muted-foreground">VMID</span>
                  <span>{vmid}</span>
                  <span className="text-muted-foreground">Name</span>
                  <span>{name || "--"}</span>
                  <span className="text-muted-foreground">Node</span>
                  <span>{node}</span>
                  {pool && (
                    <>
                      <span className="text-muted-foreground">Pool</span>
                      <span>{pool}</span>
                    </>
                  )}
                  {tags && (
                    <>
                      <span className="text-muted-foreground">Tags</span>
                      <span>{tags}</span>
                    </>
                  )}
                  <span className="text-muted-foreground">OS Type</span>
                  <span>
                    {osTypes.find((o) => o.value === ostype)?.label ?? ostype}
                  </span>
                  {isoImage && (
                    <>
                      <span className="text-muted-foreground">ISO</span>
                      <span className="truncate">{isoImage.split("/").pop()}</span>
                    </>
                  )}
                  <span className="text-muted-foreground">BIOS</span>
                  <span>
                    {bios === "ovmf" ? "OVMF (UEFI)" : "SeaBIOS"}
                    {" / "}{machineOptions.find((mt) => mt.value === machine)?.label ?? machine}
                  </span>
                  <span className="text-muted-foreground">SCSI HW</span>
                  <span>
                    {scsiControllers.find((sc) => sc.value === scsihw)?.label ??
                      scsihw}
                  </span>
                  <span className="text-muted-foreground">CPU</span>
                  <span>
                    {cores} cores x {sockets} socket(s) ({cpuType})
                    {numa ? " +NUMA" : ""}
                  </span>
                  <span className="text-muted-foreground">Memory</span>
                  <span>
                    {memory} MiB
                    {balloonEnabled
                      ? balloonMin
                        ? ` (balloon min ${balloonMin} MiB)`
                        : " (balloon on)"
                      : " (balloon off)"}
                  </span>
                  {disks.map((disk, idx) => {
                    const devNames = assignDiskDeviceNames();
                    const devName = devNames[idx]?.name ?? `disk${idx}`;
                    return (
                      <React.Fragment key={disk.id}>
                        <span className="text-muted-foreground">
                          {devName.toUpperCase()}
                        </span>
                        <span>
                          {disk.size} GB{disk.storage ? ` on ${disk.storage}` : ""}
                          {" "}({disk.format})
                          {disk.discard ? " +discard" : ""}
                          {disk.ssd ? " +ssd" : ""}
                          {disk.iothread ? " +iothread" : ""}
                        </span>
                      </React.Fragment>
                    );
                  })}
                  {virtioDrivers && virtioIsoImage && (
                    <>
                      <span className="text-muted-foreground">VirtIO ISO</span>
                      <span className="truncate">{virtioIsoImage.split("/").pop()}</span>
                    </>
                  )}
                  <span className="text-muted-foreground">Network</span>
                  <span>
                    {netModels.find((nm) => nm.value === netModel)?.label ??
                      netModel}
                    , bridge={bridge}
                    {vlan ? `, vlan=${vlan}` : ""}
                    {firewall ? ", fw" : ""}
                    {rateLimit ? `, rate=${rateLimit}` : ""}
                  </span>
                  {agentEnabled && (
                    <>
                      <span className="text-muted-foreground">Agent</span>
                      <span>
                        Enabled{agentFstrim ? " +fstrim" : ""}
                      </span>
                    </>
                  )}
                  {efiDisk && efiStorage && (
                    <>
                      <span className="text-muted-foreground">EFI Disk</span>
                      <span>{efiStorage}</span>
                    </>
                  )}
                  {tpmEnabled && tpmStorage && (
                    <>
                      <span className="text-muted-foreground">TPM</span>
                      <span>v2.0 on {tpmStorage}</span>
                    </>
                  )}
                  {(ciuser || sshkeys || ipconfig0) && (
                    <>
                      <span className="text-muted-foreground">Cloud-Init</span>
                      <span>
                        {ciuser ? `user=${ciuser}` : ""}
                        {sshkeys ? " +ssh-keys" : ""}
                        {ipconfig0 ? ` ip=${ipconfig0}` : ""}
                      </span>
                    </>
                  )}
                </div>

                <div className="flex flex-col gap-2">
                  <div className="flex items-center gap-2">
                    <Checkbox
                      id="start-after"
                      checked={startAfter}
                      onCheckedChange={(c) => {
                        setStartAfter(Boolean(c));
                      }}
                    />
                    <Label htmlFor="start-after" className="text-sm font-normal">
                      Start after creation
                    </Label>
                  </div>
                  <div className="flex items-center gap-2">
                    <Checkbox
                      id="onboot"
                      checked={onboot}
                      onCheckedChange={(c) => {
                        setOnboot(Boolean(c));
                      }}
                    />
                    <Label htmlFor="onboot" className="text-sm font-normal">
                      Start on boot
                    </Label>
                  </div>
                </div>
              </div>
            )}

            {createMutation.isError && (
              <p className="text-sm text-destructive">
                {createMutation.error.message}
              </p>
            )}

            <DialogFooter>
              <Button type="button" variant="outline" onClick={handleClose}>
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
              {step !== "confirm" ? (
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
              ) : (
                <Button
                  type="button"
                  disabled={createMutation.isPending}
                  onClick={handleSubmit}
                >
                  {createMutation.isPending ? "Creating..." : "Create VM"}
                </Button>
              )}
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
