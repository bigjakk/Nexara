import { useEffect, useMemo, useState, useRef } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  CheckField,
  Field,
  FieldGrid,
  FormSection,
  NativeSelect,
  TextArea,
} from "@/components/page/Field";
import { PropertyList, PropertyRow } from "@/components/page/Panel";
import { Wizard, type WizardStep } from "@/components/page/Wizard";
import { cn } from "@/lib/utils";
import {
  useClusterNodes,
  useClusterStorage,
  useNodeBridges,
  useClusterVMs,
  useMachineTypes,
  useCPUModels,
} from "@/features/clusters/api/cluster-queries";
import { useStorageContent } from "@/features/storage/api/storage-queries";
import {
  useClusterVMIDs,
  useCreateVM,
  useResourcePools,
} from "../api/vm-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { usePermissions } from "@/hooks/usePermissions";
import { apiClient, sessionScope } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import type { NodeResponse, VMResponse } from "@/types/api";
import { TaskProgressBanner } from "./TaskProgressBanner";
import {
  osTypes,
  cpuTypes as fallbackCpuTypes,
  scsiControllers,
  netModels,
  diskFormats,
  diskFormatFor,
  diskFormatToSend,
  diskFormatsForStorageType,
  busTakes,
  cacheModes,
  diskBusTypes,
  osDefaults,
  isWindowsOS,
} from "../lib/vm-config-constants";
import { filterStorageByContent } from "../lib/storage-filter";

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

const steps: readonly WizardStep<Step>[] = [
  {
    id: "general",
    label: "General",
    hint: "What the VM is called and where it runs.",
  },
  {
    id: "os",
    label: "OS",
    hint: "The guest operating system and the media it installs from.",
  },
  {
    id: "system",
    label: "System",
    hint: "Firmware, machine type and the controller the disks attach to.",
  },
  { id: "disks", label: "Disks", hint: "The drives the VM starts with." },
  { id: "cpu", label: "CPU", hint: "The virtual processors the guest sees." },
  {
    id: "memory",
    label: "Memory",
    hint: "How much RAM the guest gets, and whether it can hand some back.",
  },
  { id: "network", label: "Network", hint: "The VM's first network card." },
  {
    id: "cloudinit",
    label: "Cloud-init",
    hint: "Optional. First-boot settings for images that support cloud-init.",
  },
  {
    id: "confirm",
    label: "Review",
    hint: "Check the settings, then create the VM.",
  },
];

export function CreateVMDialog({
  open,
  onOpenChange,
  clusterId,
}: CreateVMDialogProps) {
  const { t } = useTranslation("vms");
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data: nodes } = useClusterNodes(clusterId);
  const { data: storageList, isPending: storagePending } =
    useClusterStorage(clusterId);
  const { data: usedVMIDs } = useClusterVMIDs(clusterId);
  const { data: clusterVMs } = useClusterVMs(clusterId);
  const { data: resourcePools } = useResourcePools(clusterId);
  const createMutation = useCreateVM();
  const settle = useSaveOutcome(open);
  const pollTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const safetyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Which look for a created VM is current. A closed dialog, or the next
  // create, ends the last one: its poll and timeout must not act on what the
  // dialog holds now (they wiped a second VM's wizard and went to the first).
  const searchRef = useRef(0);

  const [node, setNode] = useState("");
  // The stores that hold disk images on the chosen node: one only another
  // node has would be refused at create (see filterStorageByContent).
  const imageStorageOptions = useMemo(
    () =>
      filterStorageByContent(storageList, "images", nodes, node).map(
        (s) => s.storage,
      ),
    [storageList, nodes, node],
  );

  // Each store's type, for the disk formats it takes.
  const storageTypes = useMemo(
    () => new Map((storageList ?? []).map((s) => [s.storage, s.type])),
    [storageList],
  );

  // A disk, EFI disk or TPM state left without a store was not sent at all, so
  // the VM was made without it ("Default" meant nothing to Proxmox). One left
  // unset goes to the first store that holds images, as Proxmox's wizard does.
  // A pick the node does not have falls back too, so a select always shows
  // the store that will be sent.
  const firstImageStore = imageStorageOptions[0] ?? "";
  const storeOf = (picked: string) =>
    imageStorageOptions.includes(picked) ? picked : firstImageStore;
  const noImageStore = !storagePending && imageStorageOptions.length === 0;
  const diskStoreType = (disk: DiskEntry) =>
    storageTypes.get(storeOf(disk.storage));

  /** The format a disk gets: its choice if its store takes it, else raw. */
  function effectiveFormat(disk: DiskEntry): string {
    return diskFormatFor(diskStoreType(disk), disk.format);
  }

  // ISO stores on the chosen node, by row id (content is read through it): an
  // image listed from another node's own store is not there at create.
  const isoStoragePools = useMemo(
    () => filterStorageByContent(storageList, "iso", nodes, node),
    [storageList, nodes, node],
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
      const cpuFree =
        n.cpu_count > 0 ? (n.cpu_count - alloc.cpu) / n.cpu_count : 0;
      const memFree =
        n.mem_total > 0 ? (n.mem_total - alloc.mem) / n.mem_total : 0;
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
    {
      id: 1,
      bus: "scsi",
      size: "32",
      storage: "",
      format: "qcow2",
      cache: "none",
      discard: true,
      ssd: true,
      iothread: true,
    },
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
  // The server holds a create's start to what the power route needs (execute
  // on the kind), so the tick is offered only to a holder, as RestoreDialog's.
  const { canExecute } = usePermissions();
  const canStart = canExecute("vm");
  const [onboot, setOnboot] = useState(false);

  // A pick the chosen node does not have counts as none, and its image with it.
  // Content is read through the picked row.
  const isoStore = isoStoragePools.some((p) => p.id === isoStorageId)
    ? isoStorageId
    : "";
  const isoPicked = isoStore ? isoImage : "";
  const virtioStore = isoStoragePools.some((p) => p.id === virtioIsoStorageId)
    ? virtioIsoStorageId
    : "";
  const virtioPicked = virtioStore ? virtioIsoImage : "";
  const { data: isoContent } = useStorageContent(clusterId, isoStore);
  const isoList = useMemo(
    () =>
      isoContent ? isoContent.filter((item) => item.content === "iso") : [],
    [isoContent],
  );

  // Fetch VirtIO drivers ISO content
  const { data: virtioIsoContent } = useStorageContent(clusterId, virtioStore);
  const virtioIsoList = useMemo(
    () =>
      virtioIsoContent
        ? virtioIsoContent.filter((item) => item.content === "iso")
        : [],
    [virtioIsoContent],
  );

  // Fetch network bridges for the selected node
  const { data: bridges } = useNodeBridges(clusterId, node);

  // Fetch CPU models for the selected node
  const { data: rawCpuModels } = useCPUModels(clusterId, node);
  const cpuTypeOptions = useMemo(() => {
    if (!rawCpuModels || rawCpuModels.length === 0) {
      return fallbackCpuTypes.map((t) => t);
    }
    // Sort: custom first, then by name
    return [...rawCpuModels]
      .sort((a, b) => {
        if (a.custom !== b.custom) return a.custom ? -1 : 1;
        return a.name.localeCompare(b.name);
      })
      .map((m) => m.name);
  }, [rawCpuModels]);

  // Fetch machine types for the selected node
  const { data: rawMachineTypes } = useMachineTypes(clusterId, node);
  const machineOptions = useMemo(() => {
    if (!rawMachineTypes || rawMachineTypes.length === 0) {
      return [
        { value: "pc", label: "i440fx (default)" },
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
    if (q35pve)
      result.push({ value: q35pve.id, label: `${q35pve.id} (recommended)` });
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
      if (
        recommended &&
        recommended.value !== "pc" &&
        recommended.value !== "q35"
      ) {
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

  /** Ends the current look for a created VM. */
  function stopSearch() {
    searchRef.current += 1;
    if (pollTimerRef.current) clearInterval(pollTimerRef.current);
    pollTimerRef.current = null;
    if (safetyTimerRef.current) clearTimeout(safetyTimerRef.current);
    safetyTimerRef.current = null;
  }

  // Cleanup poll timer on unmount
  useEffect(() => {
    return () => {
      if (pollTimerRef.current) clearInterval(pollTimerRef.current);
      if (safetyTimerRef.current) clearTimeout(safetyTimerRef.current);
    };
  }, []);

  const isDuplicate = usedVMIDs ? usedVMIDs.has(Number(vmid)) : false;
  const stepIdx = steps.findIndex((s) => s.id === step);
  const isWindows = isWindowsOS(ostype);

  // Disk management helpers
  function addDisk() {
    setDisks((prev) => [
      ...prev,
      {
        id: nextDiskId,
        // With none left, the bus the OS defaults (or the VirtIO ISO) give a
        // first disk: a Windows guest without VirtIO drivers sees no SCSI disk.
        bus:
          prev[0]?.bus ??
          (virtioDrivers
            ? "scsi"
            : ((osDefaults[ostype] ?? osDefaults["other"])?.diskBus ?? "scsi")),
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
    const store = storeOf(disk.storage);
    if (!store || !disk.size) return "";
    const parts = [`${store}:${disk.size}`];
    const format = diskFormatToSend(diskStoreType(disk), disk.format);
    if (format) parts.push(`format=${format}`);
    if (disk.cache !== "none") parts.push(`cache=${disk.cache}`);
    if (disk.discard) parts.push("discard=on");
    if (disk.ssd && busTakes(disk.bus, "ssd")) parts.push("ssd=1");
    if (disk.iothread && busTakes(disk.bus, "iothread")) {
      parts.push("iothread=1");
    }
    return parts.join(",");
  }

  // Assign device names (e.g. scsi0, scsi1, ide0) — skipping IDE slots used by ISOs
  function assignDiskDeviceNames(): { name: string; disk: DiskEntry }[] {
    const counters: Record<string, number> = {
      scsi: 0,
      ide: 0,
      sata: 0,
      virtio: 0,
    };
    const result: { name: string; disk: DiskEntry }[] = [];
    for (const disk of disks) {
      let idx = counters[disk.bus] ?? 0;
      // Skip IDE slots reserved for ISOs
      if (disk.bus === "ide") {
        while (idx === 2 || (idx === 3 && virtioDrivers)) {
          idx++;
        }
      }
      result.push({ name: `${disk.bus}${String(idx)}`, disk });
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
    const store = storeOf(efiStorage);
    if (!efiDisk || !store) return "";
    const format = diskFormatToSend(storageTypes.get(store), "qcow2");
    const formatPart = format ? `,format=${format}` : "";
    return `${store}:1${formatPart},efitype=4m,pre-enrolled-keys=1`;
  }

  function buildTpmState0(): string {
    const store = storeOf(tpmStorage);
    if (!tpmEnabled || !store) return "";
    return `${store}:1,version=v2.0`;
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
    const ide2 = isoPicked ? `${isoPicked},media=cdrom` : "";

    // VirtIO drivers ISO on ide3
    if (virtioDrivers && virtioPicked) {
      extra["ide3"] = `${virtioPicked},media=cdrom`;
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
      start: startAfter && canStart,
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
    body.hotplug = "network,disk,usb";
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

    settle(createMutation.mutateAsync({ clusterId, body }), {
      action: `Creating VM ${name || vmid}`,
      onSuccess: (data) => {
        setUpid(data.upid);
      },
      onError: () => undefined, // shown below from the mutation's state
    });
  }

  function handleClose() {
    stopSearch();
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
    setDisks([
      {
        id: 1,
        bus: "scsi",
        size: "32",
        storage: "",
        format: "qcow2",
        cache: "none",
        discard: true,
        ssd: true,
        iothread: true,
      },
    ]);
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

  /**
   * Whether a step's fields are complete. Next, the step list and Create hold
   * at the first one that is not, so no disk the dialog shows is left out.
   */
  function stepComplete(id: Step): boolean {
    switch (id) {
      case "general":
        return Number(vmid) > 0 && node !== "";
      case "system":
        return (
          (!efiDisk || storeOf(efiStorage) !== "") &&
          (!tpmEnabled || storeOf(tpmStorage) !== "")
        );
      case "disks":
        return disks.every(
          (d) => storeOf(d.storage) !== "" && Number(d.size) > 0,
        );
      case "cpu":
        return Number(cores) > 0;
      case "memory":
        return Number(memory) > 0;
      default:
        return true;
    }
  }
  const canProceed = stepComplete(step);
  const firstIncomplete = steps.findIndex((s) => !stepComplete(s.id));
  const canGoTo = (id: Step) =>
    firstIncomplete < 0 ||
    steps.findIndex((s) => s.id === id) <= firstIncomplete;

  const diskDevices = assignDiskDeviceNames();
  const osLabel = osTypes.find((o) => o.value === ostype)?.label ?? ostype;
  const machineLabel =
    machineOptions.find((mt) => mt.value === machine)?.label ?? machine;
  /** The Review step's way back to the step a group of settings came from. */
  const editStep = (target: Step) => (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      className="h-6 px-2 text-primary hover:text-primary"
      aria-label={`Edit ${steps.find((s) => s.id === target)?.label ?? target}`}
      onClick={() => {
        setStep(target);
      }}
    >
      Edit
    </Button>
  );

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent
        className={cn(
          "flex max-h-[85vh] flex-col gap-0 overflow-hidden p-0",
          upid ? "max-w-lg" : "h-[640px] max-w-3xl",
        )}
      >
        <DialogHeader className="border-b px-5 py-4 pr-12 sm:px-6">
          <DialogTitle>Create virtual machine</DialogTitle>
          {/* What is being built so far, so the header reads as its identity. */}
          <DialogDescription>
            VM {vmid || "—"}
            {name ? `, ${name}` : ""}
            {node ? `, on ${node}` : ""}
          </DialogDescription>
        </DialogHeader>

        {upid ? (
          <div className="px-5 py-5 sm:px-6">
            <TaskProgressBanner
              clusterId={clusterId}
              upid={upid}
              onComplete={() => {
                // After task completes, poll for the new VM record and navigate to it
                stopSearch();
                const search = searchRef.current;
                const createdVmid = Number(vmid);
                // The session that created it. A look at the list that is already
                // out when it ends is answered all the same, and what it finds is
                // that session's VM: navigating then goes through whoever is signed
                // in next, and a protected route carries the page as returnTo.
                const ended = sessionScope();
                const pollForVM = () => {
                  void queryClient
                    .invalidateQueries({
                      queryKey: ["clusters", clusterId, "vms"],
                    })
                    .then(() =>
                      apiClient.list<VMResponse>(
                        apiPath`/api/v1/clusters/${clusterId}/vms`,
                      ),
                    )
                    .then((vms) => {
                      if (ended() || search !== searchRef.current) return;
                      const found = vms.find((v) => v.vmid === createdVmid);
                      if (found) {
                        // Reset as well as close: the dialog is mounted once,
                        // in AppShell, and kept the task, so the next Create
                        // VM opened on it and went to this VM again.
                        handleClose();
                        void navigate(`/inventory/vm/${clusterId}/${found.id}`);
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
                safetyTimerRef.current = setTimeout(() => {
                  if (search === searchRef.current) handleClose();
                }, 30000);
              }}
              description={`Create VM ${name || vmid}`}
            />
          </div>
        ) : (
          <Wizard
            steps={steps}
            current={step}
            onStepChange={setStep}
            canGoTo={canGoTo}
            notice={
              createMutation.isError ? (
                <p className="text-sm text-destructive">
                  {createMutation.error.message}
                </p>
              ) : (
                noImageStore &&
                (step === "system" ||
                  step === "disks" ||
                  step === "confirm") && (
                  <p className="text-sm text-muted-foreground">
                    No storage for disk images is available on{" "}
                    {node || "this cluster"}, so the VM can only be made without
                    a disk, an EFI disk or a TPM state.
                  </p>
                )
              )
            }
            footer={
              <>
                <Button
                  type="button"
                  variant="ghost"
                  className="mr-auto"
                  onClick={handleClose}
                >
                  Cancel
                </Button>
                {stepIdx > 0 && (
                  <Button
                    type="button"
                    variant="outline"
                    onClick={() => {
                      const prev = steps[stepIdx - 1];
                      if (prev) setStep(prev.id);
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
                      if (next) setStep(next.id);
                    }}
                  >
                    Next
                  </Button>
                ) : (
                  <Button
                    type="button"
                    disabled={createMutation.isPending || firstIncomplete >= 0}
                    onClick={handleSubmit}
                  >
                    {createMutation.isPending ? "Creating..." : "Create VM"}
                  </Button>
                )}
              </>
            }
          >
            {/* === GENERAL === */}
            {step === "general" && (
              <FieldGrid>
                <Field
                  label="VMID"
                  htmlFor="vm-vmid"
                  warning={isDuplicate && "VMID may already be in use"}
                >
                  <Input
                    id="vm-vmid"
                    type="number"
                    min={1}
                    value={vmid}
                    onChange={(e) => {
                      setVmid(e.target.value);
                    }}
                  />
                </Field>
                <Field label="Name" htmlFor="vm-name">
                  <Input
                    id="vm-name"
                    value={name}
                    onChange={(e) => {
                      setName(e.target.value);
                    }}
                    placeholder="my-vm"
                  />
                </Field>
                <Field label="Node" htmlFor="vm-node">
                  <NativeSelect
                    id="vm-node"
                    value={node}
                    onChange={(e) => {
                      setNode(e.target.value);
                      setBridge("vmbr0"); // reset bridge when node changes
                    }}
                  >
                    <option value="">Select node</option>
                    {nodes?.map((n) => (
                      <option key={n.id} value={n.name}>
                        {n.name}
                        {n.name === bestNode ? " (best available)" : ""}
                        {n.status !== "online" ? " [offline]" : ""}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
                <Field label="Resource pool" htmlFor="vm-pool">
                  <NativeSelect
                    id="vm-pool"
                    value={pool}
                    onChange={(e) => {
                      setPool(e.target.value);
                    }}
                  >
                    <option value="">None</option>
                    {resourcePools?.map((p) => (
                      <option key={p.poolid} value={p.poolid}>
                        {p.poolid}
                        {p.comment ? ` — ${p.comment}` : ""}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
                <Field
                  label="Tags"
                  htmlFor="vm-tags"
                  hint="Separated by semicolons."
                  className="sm:col-span-2"
                >
                  <Input
                    id="vm-tags"
                    value={tags}
                    onChange={(e) => {
                      setTags(e.target.value);
                    }}
                    placeholder="tag1;tag2;tag3"
                  />
                </Field>
                <Field
                  label="Description"
                  htmlFor="vm-description"
                  className="sm:col-span-2"
                >
                  <TextArea
                    id="vm-description"
                    value={description}
                    onChange={(e) => {
                      setDescription(e.target.value);
                    }}
                    placeholder="Optional description"
                    rows={2}
                  />
                </Field>
              </FieldGrid>
            )}

            {/* === OS === */}
            {step === "os" && (
              <FieldGrid>
                <Field
                  label="OS type"
                  htmlFor="vm-ostype"
                  hint="Sets the defaults for the disk bus, network card and firmware on the steps that follow."
                  className="sm:col-span-2"
                >
                  <NativeSelect
                    id="vm-ostype"
                    value={ostype}
                    onChange={(e) => {
                      const newOstype = e.target.value;
                      setOstype(newOstype);
                      applyOSDefaults(newOstype);
                    }}
                  >
                    {osTypes.map((o) => (
                      <option key={o.value} value={o.value}>
                        {o.label}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
                <Field label="ISO storage" htmlFor="vm-iso-storage">
                  <NativeSelect
                    id="vm-iso-storage"
                    value={isoStore}
                    onChange={(e) => {
                      const selectedId = e.target.value;
                      setIsoStorageId(selectedId);
                      setIsoImage("");
                    }}
                  >
                    <option value="">Do not use any media</option>
                    {isoStoragePools.map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.storage}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
                <Field label="ISO image" htmlFor="vm-iso-image">
                  <NativeSelect
                    id="vm-iso-image"
                    value={isoPicked}
                    onChange={(e) => {
                      setIsoImage(e.target.value);
                    }}
                    disabled={!isoStore}
                  >
                    <option value="">
                      {isoStore
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
                  </NativeSelect>
                </Field>
                {/* VirtIO Drivers ISO — shown for Windows OS types */}
                {isWindows && (
                  <FormSection title="VirtIO drivers" className="sm:col-span-2">
                    <CheckField
                      id="virtio-drivers"
                      label="Add a second drive with the VirtIO drivers ISO"
                      hint="Also sets the disk bus to SCSI and the network card to VirtIO, which perform best on Windows."
                      checked={virtioDrivers}
                      onCheckedChange={(checked) => {
                        setVirtioDrivers(checked);
                        if (checked) {
                          // VirtIO drivers: recommend scsi bus + virtio NIC
                          setDisks((prev) => {
                            const first = prev[0];
                            if (!first) return prev;
                            return [
                              { ...first, bus: "scsi" as const },
                              ...prev.slice(1),
                            ];
                          });
                          setNetModel("virtio");
                        } else {
                          // Restore OS defaults for bus and NIC
                          const defaults =
                            osDefaults[ostype] ?? osDefaults["other"];
                          if (defaults) {
                            setDisks((prev) => {
                              const first = prev[0];
                              if (!first) return prev;
                              return [
                                { ...first, bus: defaults.diskBus },
                                ...prev.slice(1),
                              ];
                            });
                            setNetModel(defaults.netModel);
                          }
                          setVirtioIsoStorageId("");
                          setVirtioIsoImage("");
                        }
                      }}
                    />
                    {virtioDrivers && (
                      <FieldGrid>
                        <Field
                          label="VirtIO ISO storage"
                          htmlFor="vm-virtio-storage"
                        >
                          <NativeSelect
                            id="vm-virtio-storage"
                            value={virtioStore}
                            onChange={(e) => {
                              setVirtioIsoStorageId(e.target.value);
                              setVirtioIsoImage("");
                            }}
                          >
                            <option value="">Select storage</option>
                            {isoStoragePools.map((p) => (
                              <option key={p.id} value={p.id}>
                                {p.storage}
                              </option>
                            ))}
                          </NativeSelect>
                        </Field>
                        <Field
                          label="VirtIO drivers ISO"
                          htmlFor="vm-virtio-image"
                        >
                          <NativeSelect
                            id="vm-virtio-image"
                            value={virtioPicked}
                            onChange={(e) => {
                              setVirtioIsoImage(e.target.value);
                            }}
                            disabled={!virtioStore}
                          >
                            <option value="">
                              {virtioStore
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
                          </NativeSelect>
                        </Field>
                      </FieldGrid>
                    )}
                  </FormSection>
                )}
              </FieldGrid>
            )}

            {/* === SYSTEM === */}
            {step === "system" && (
              <div className="space-y-4">
                <FieldGrid>
                  <Field label="BIOS" htmlFor="vm-bios">
                    <NativeSelect
                      id="vm-bios"
                      value={bios}
                      onChange={(e) => {
                        setBios(e.target.value);
                        if (e.target.value === "ovmf") {
                          setEfiDisk(true);
                          // Pick the best q35+pve variant, fall back to generic q35
                          const q35pve = machineOptions.find(
                            (mt) =>
                              mt.value.includes("q35") &&
                              mt.value.includes("+pve"),
                          );
                          setMachine(q35pve?.value ?? "q35");
                        } else {
                          setEfiDisk(false);
                        }
                      }}
                    >
                      <option value="seabios">SeaBIOS (default)</option>
                      <option value="ovmf">OVMF (UEFI)</option>
                    </NativeSelect>
                  </Field>
                  <Field label="Machine type" htmlFor="vm-machine">
                    <NativeSelect
                      id="vm-machine"
                      value={machine}
                      onChange={(e) => {
                        setMachine(e.target.value);
                      }}
                    >
                      {machineOptions.map((mt) => (
                        <option key={mt.value} value={mt.value}>
                          {mt.label}
                        </option>
                      ))}
                    </NativeSelect>
                  </Field>
                  <Field label="SCSI controller" htmlFor="vm-scsihw">
                    <NativeSelect
                      id="vm-scsihw"
                      value={scsihw}
                      onChange={(e) => {
                        setScsihw(e.target.value);
                      }}
                    >
                      {scsiControllers.map((sc) => (
                        <option key={sc.value} value={sc.value}>
                          {sc.label}
                        </option>
                      ))}
                    </NativeSelect>
                  </Field>
                  {efiDisk && (
                    <Field
                      label="EFI disk storage"
                      htmlFor="vm-efi-storage"
                      hint="UEFI keeps its settings on a small disk of its own."
                    >
                      <NativeSelect
                        id="vm-efi-storage"
                        value={storeOf(efiStorage)}
                        onChange={(e) => {
                          setEfiStorage(e.target.value);
                        }}
                      >
                        <option value="" disabled>
                          Select storage
                        </option>
                        {imageStorageOptions.map((s) => (
                          <option key={s} value={s}>
                            {s}
                          </option>
                        ))}
                      </NativeSelect>
                    </Field>
                  )}
                </FieldGrid>
                <FormSection title="QEMU guest agent">
                  <div className="space-y-3">
                    <CheckField
                      id="agent-enabled"
                      label="Enable the guest agent"
                      hint="The agent has to be installed inside the guest as well."
                      checked={agentEnabled}
                      onCheckedChange={setAgentEnabled}
                    />
                    {agentEnabled && (
                      <CheckField
                        id="agent-fstrim"
                        label="TRIM on clone"
                        hint="Runs fstrim in the guest after a disk is moved or the VM is migrated."
                        checked={agentFstrim}
                        onCheckedChange={setAgentFstrim}
                      />
                    )}
                  </div>
                </FormSection>
                <FormSection title="TPM">
                  <CheckField
                    id="tpm-enabled"
                    label="Add TPM v2.0"
                    checked={tpmEnabled}
                    onCheckedChange={setTpmEnabled}
                  />
                  {tpmEnabled && (
                    <Field label="TPM storage" htmlFor="vm-tpm-storage">
                      <NativeSelect
                        id="vm-tpm-storage"
                        value={storeOf(tpmStorage)}
                        onChange={(e) => {
                          setTpmStorage(e.target.value);
                        }}
                      >
                        <option value="" disabled>
                          Select storage for TPM
                        </option>
                        {imageStorageOptions.map((s) => (
                          <option key={s} value={s}>
                            {s}
                          </option>
                        ))}
                      </NativeSelect>
                    </Field>
                  )}
                </FormSection>
              </div>
            )}

            {/* === DISKS === */}
            {step === "disks" && (
              <div className="space-y-4">
                {disks.map((disk, idx) => (
                  <FormSection
                    key={disk.id}
                    title={`Disk ${String(idx + 1)}`}
                    aside={
                      <span className="font-mono">
                        {diskDevices[idx]?.name}
                      </span>
                    }
                    action={
                      // The last one too: a VM without a disk is a choice
                      // (PXE, or a disk attached later).
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        className="h-6 px-2 text-muted-foreground"
                        aria-label={`Remove disk ${String(idx + 1)}`}
                        onClick={() => {
                          removeDisk(disk.id);
                        }}
                      >
                        Remove
                      </Button>
                    }
                  >
                    <FieldGrid>
                      <Field
                        label="Storage"
                        htmlFor={`vm-disk-storage-${String(disk.id)}`}
                      >
                        <NativeSelect
                          id={`vm-disk-storage-${String(disk.id)}`}
                          value={storeOf(disk.storage)}
                          onChange={(e) => {
                            updateDisk(disk.id, { storage: e.target.value });
                          }}
                        >
                          <option value="" disabled>
                            Select storage
                          </option>
                          {imageStorageOptions.map((s) => (
                            <option key={s} value={s}>
                              {s}
                            </option>
                          ))}
                        </NativeSelect>
                      </Field>
                      <Field
                        label="Size (GiB)"
                        htmlFor={`vm-disk-size-${String(disk.id)}`}
                      >
                        <Input
                          id={`vm-disk-size-${String(disk.id)}`}
                          type="number"
                          min={1}
                          value={disk.size}
                          onChange={(e) => {
                            updateDisk(disk.id, { size: e.target.value });
                          }}
                        />
                      </Field>
                      <Field
                        label="Bus type"
                        htmlFor={`vm-disk-bus-${String(disk.id)}`}
                      >
                        <NativeSelect
                          id={`vm-disk-bus-${String(disk.id)}`}
                          value={disk.bus}
                          onChange={(e) => {
                            updateDisk(disk.id, {
                              bus: e.target.value as DiskEntry["bus"],
                            });
                          }}
                        >
                          {diskBusTypes.map((bt) => (
                            <option key={bt.value} value={bt.value}>
                              {bt.label}
                            </option>
                          ))}
                        </NativeSelect>
                      </Field>
                      <Field
                        label="Format"
                        htmlFor={`vm-disk-format-${String(disk.id)}`}
                      >
                        <NativeSelect
                          id={`vm-disk-format-${String(disk.id)}`}
                          value={effectiveFormat(disk)}
                          disabled={
                            diskFormatsForStorageType(diskStoreType(disk))
                              .length === 1
                          }
                          onChange={(e) => {
                            updateDisk(disk.id, { format: e.target.value });
                          }}
                        >
                          {diskFormats
                            .filter((f) =>
                              diskFormatsForStorageType(
                                diskStoreType(disk),
                              ).includes(f.value),
                            )
                            .map((f) => (
                              <option key={f.value} value={f.value}>
                                {f.label}
                              </option>
                            ))}
                        </NativeSelect>
                      </Field>
                      <Field
                        label="Cache"
                        htmlFor={`vm-disk-cache-${String(disk.id)}`}
                      >
                        <NativeSelect
                          id={`vm-disk-cache-${String(disk.id)}`}
                          value={disk.cache}
                          onChange={(e) => {
                            updateDisk(disk.id, { cache: e.target.value });
                          }}
                        >
                          {cacheModes.map((cm) => (
                            <option key={cm.value} value={cm.value}>
                              {cm.label}
                            </option>
                          ))}
                        </NativeSelect>
                      </Field>
                      <Field label="Options">
                        <div className="flex min-h-9 flex-wrap items-center gap-x-5 gap-y-2">
                          <CheckField
                            id={`discard-${String(disk.id)}`}
                            label="TRIM"
                            checked={disk.discard}
                            onCheckedChange={(checked) => {
                              updateDisk(disk.id, { discard: checked });
                            }}
                          />
                          <CheckField
                            id={`ssd-${String(disk.id)}`}
                            label="SSD"
                            checked={disk.ssd && busTakes(disk.bus, "ssd")}
                            disabled={!busTakes(disk.bus, "ssd")}
                            onCheckedChange={(checked) => {
                              updateDisk(disk.id, { ssd: checked });
                            }}
                          />
                          <CheckField
                            id={`io-${String(disk.id)}`}
                            label="IO thread"
                            checked={
                              disk.iothread && busTakes(disk.bus, "iothread")
                            }
                            disabled={!busTakes(disk.bus, "iothread")}
                            onCheckedChange={(checked) => {
                              updateDisk(disk.id, { iothread: checked });
                            }}
                          />
                        </div>
                      </Field>
                    </FieldGrid>
                  </FormSection>
                ))}
                <div className="border-t pt-4">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="gap-1.5"
                    onClick={addDisk}
                  >
                    <Plus />
                    Add disk
                  </Button>
                </div>
              </div>
            )}

            {/* === CPU === */}
            {step === "cpu" && (
              <FieldGrid>
                <Field label="Sockets" htmlFor="vm-sockets">
                  <Input
                    id="vm-sockets"
                    type="number"
                    min={1}
                    value={sockets}
                    onChange={(e) => {
                      setSockets(e.target.value);
                    }}
                  />
                </Field>
                <Field
                  label="Cores"
                  htmlFor="vm-cores"
                  hint={`${String(Number(cores) * Number(sockets))} vCPUs in total.`}
                >
                  <Input
                    id="vm-cores"
                    type="number"
                    min={1}
                    value={cores}
                    onChange={(e) => {
                      setCores(e.target.value);
                    }}
                  />
                </Field>
                <Field
                  label="CPU type"
                  htmlFor="vm-cputype"
                  className="sm:col-span-2"
                >
                  <NativeSelect
                    id="vm-cputype"
                    value={cpuType}
                    onChange={(e) => {
                      setCpuType(e.target.value);
                    }}
                  >
                    {cpuTypeOptions.map((ct) => (
                      <option key={ct} value={ct}>
                        {ct}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
                <CheckField
                  id="numa"
                  label="Enable NUMA"
                  checked={numa}
                  onCheckedChange={setNuma}
                  className="sm:col-span-2"
                />
              </FieldGrid>
            )}

            {/* === MEMORY === */}
            {step === "memory" && (
              <FieldGrid>
                <Field
                  label="Memory (MiB)"
                  htmlFor="vm-memory"
                  hint={`${(Number(memory) / 1024).toFixed(1)} GiB`}
                >
                  <Input
                    id="vm-memory"
                    type="number"
                    min={64}
                    step={128}
                    value={memory}
                    onChange={(e) => {
                      setMemory(e.target.value);
                    }}
                  />
                </Field>
                {balloonEnabled && (
                  <Field
                    label="Minimum memory (MiB)"
                    htmlFor="vm-balloon-min"
                    hint="What ballooning may shrink the guest down to."
                  >
                    <Input
                      id="vm-balloon-min"
                      type="number"
                      min={0}
                      value={balloonMin}
                      onChange={(e) => {
                        setBalloonMin(e.target.value);
                      }}
                      placeholder="Same as max"
                    />
                  </Field>
                )}
                <CheckField
                  id="balloon-enabled"
                  label="Ballooning"
                  hint="Lets the host take back memory the guest is not using."
                  checked={balloonEnabled}
                  onCheckedChange={setBalloonEnabled}
                  className="sm:col-span-2"
                />
              </FieldGrid>
            )}

            {/* === NETWORK === */}
            {step === "network" && (
              <FieldGrid>
                <Field label="Model" htmlFor="vm-net-model">
                  <NativeSelect
                    id="vm-net-model"
                    value={netModel}
                    onChange={(e) => {
                      setNetModel(e.target.value);
                    }}
                  >
                    {netModels.map((nm) => (
                      <option key={nm.value} value={nm.value}>
                        {nm.label}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
                <Field label="Bridge" htmlFor="vm-bridge">
                  <NativeSelect
                    id="vm-bridge"
                    value={bridge}
                    onChange={(e) => {
                      setBridge(e.target.value);
                    }}
                  >
                    {bridges && bridges.length > 0 ? (
                      bridges.map((b) => (
                        <option key={b.iface} value={b.iface}>
                          {b.iface}
                          {b.cidr ? ` (${b.cidr})` : ""}
                          {!b.active ? " [inactive]" : ""}
                        </option>
                      ))
                    ) : (
                      <option value="vmbr0">vmbr0</option>
                    )}
                  </NativeSelect>
                </Field>
                <Field label="VLAN tag" htmlFor="vm-vlan">
                  <Input
                    id="vm-vlan"
                    type="number"
                    min={1}
                    max={4094}
                    value={vlan}
                    onChange={(e) => {
                      setVlan(e.target.value);
                    }}
                    placeholder="None"
                  />
                </Field>
                <Field label="Rate limit (MB/s)" htmlFor="vm-rate">
                  <Input
                    id="vm-rate"
                    type="number"
                    min={0}
                    value={rateLimit}
                    onChange={(e) => {
                      setRateLimit(e.target.value);
                    }}
                    placeholder="Unlimited"
                  />
                </Field>
                <Field label="MAC address" htmlFor="vm-mac">
                  <Input
                    id="vm-mac"
                    value={macAddress}
                    onChange={(e) => {
                      setMacAddress(e.target.value);
                    }}
                    placeholder="Auto-generated"
                  />
                </Field>
                <Field label="MTU" htmlFor="vm-mtu">
                  <Input
                    id="vm-mtu"
                    type="number"
                    min={68}
                    max={65520}
                    value={mtu}
                    onChange={(e) => {
                      setMtu(e.target.value);
                    }}
                    placeholder="Default"
                  />
                </Field>
                <Field label="Multiqueue" htmlFor="vm-multiqueue">
                  <Input
                    id="vm-multiqueue"
                    type="number"
                    min={0}
                    max={64}
                    value={multiqueue}
                    onChange={(e) => {
                      setMultiqueue(e.target.value);
                    }}
                    placeholder="Disabled"
                  />
                </Field>
                <CheckField
                  id="firewall"
                  label="Firewall"
                  hint="Applies the guest's firewall rules to this card."
                  checked={firewall}
                  onCheckedChange={setFirewall}
                  className="sm:col-span-2"
                />
              </FieldGrid>
            )}

            {/* === CLOUD-INIT === */}
            {step === "cloudinit" && (
              <FieldGrid>
                <Field label="User" htmlFor="vm-ciuser">
                  <Input
                    id="vm-ciuser"
                    value={ciuser}
                    onChange={(e) => {
                      setCiuser(e.target.value);
                    }}
                    placeholder="root"
                  />
                </Field>
                <Field label="Password" htmlFor="vm-cipassword">
                  <Input
                    id="vm-cipassword"
                    type="password"
                    value={cipassword}
                    onChange={(e) => {
                      setCipassword(e.target.value);
                    }}
                    placeholder="Optional"
                  />
                </Field>
                <Field
                  label="SSH public keys"
                  htmlFor="vm-sshkeys"
                  hint="One key per line."
                  className="sm:col-span-2"
                >
                  <TextArea
                    id="vm-sshkeys"
                    value={sshkeys}
                    onChange={(e) => {
                      setSshkeys(e.target.value);
                    }}
                    className="min-h-[80px] font-mono"
                    placeholder="ssh-rsa AAAA..."
                    rows={3}
                  />
                </Field>
                <Field
                  label="IP config (ipconfig0)"
                  htmlFor="vm-ipconfig0"
                  className="sm:col-span-2"
                >
                  <Input
                    id="vm-ipconfig0"
                    value={ipconfig0}
                    onChange={(e) => {
                      setIpconfig0(e.target.value);
                    }}
                    placeholder="ip=dhcp or ip=192.0.2.10/24,gw=192.0.2.1"
                  />
                </Field>
                <Field label="DNS server" htmlFor="vm-nameserver">
                  <Input
                    id="vm-nameserver"
                    value={nameserver}
                    onChange={(e) => {
                      setNameserver(e.target.value);
                    }}
                    placeholder="192.0.2.53"
                  />
                </Field>
                <Field label="Search domain" htmlFor="vm-searchdomain">
                  <Input
                    id="vm-searchdomain"
                    value={searchdomain}
                    onChange={(e) => {
                      setSearchdomain(e.target.value);
                    }}
                    placeholder="example.com"
                  />
                </Field>
              </FieldGrid>
            )}

            {/* === REVIEW === */}
            {step === "confirm" && (
              <div className="space-y-4">
                <div className="flex flex-wrap gap-x-6 gap-y-2">
                  <CheckField
                    id="start-after"
                    label="Start after creation"
                    checked={startAfter && canStart}
                    disabled={!canStart}
                    hint={
                      canStart
                        ? undefined
                        : t("startNeedsExecute", { permission: "execute:vm" })
                    }
                    onCheckedChange={setStartAfter}
                  />
                  <CheckField
                    id="onboot"
                    label="Start on boot"
                    checked={onboot}
                    onCheckedChange={setOnboot}
                  />
                </div>
                <FormSection title="General" action={editStep("general")}>
                  <PropertyList>
                    <PropertyRow label="VMID">{vmid}</PropertyRow>
                    <PropertyRow label="Name">{name || "—"}</PropertyRow>
                    <PropertyRow label="Node">{node}</PropertyRow>
                    {pool && <PropertyRow label="Pool">{pool}</PropertyRow>}
                    {tags && <PropertyRow label="Tags">{tags}</PropertyRow>}
                  </PropertyList>
                </FormSection>
                <FormSection title="Operating system" action={editStep("os")}>
                  <PropertyList>
                    <PropertyRow label="OS type">{osLabel}</PropertyRow>
                    {isoPicked && (
                      <PropertyRow label="ISO">
                        {isoPicked.split("/").pop()}
                      </PropertyRow>
                    )}
                    {virtioDrivers && virtioPicked && (
                      <PropertyRow label="VirtIO ISO">
                        {virtioPicked.split("/").pop()}
                      </PropertyRow>
                    )}
                  </PropertyList>
                </FormSection>
                <FormSection title="System" action={editStep("system")}>
                  <PropertyList>
                    <PropertyRow label="BIOS">
                      {bios === "ovmf" ? "OVMF (UEFI)" : "SeaBIOS"}
                    </PropertyRow>
                    <PropertyRow label="Machine type">
                      {machineLabel}
                    </PropertyRow>
                    <PropertyRow label="SCSI controller">
                      {scsiControllers.find((sc) => sc.value === scsihw)
                        ?.label ?? scsihw}
                    </PropertyRow>
                    {agentEnabled && (
                      <PropertyRow label="Guest agent">
                        Enabled{agentFstrim ? ", TRIM on clone" : ""}
                      </PropertyRow>
                    )}
                    {efiDisk && (
                      <PropertyRow label="EFI disk">
                        {storeOf(efiStorage)}
                      </PropertyRow>
                    )}
                    {tpmEnabled && (
                      <PropertyRow label="TPM">
                        v2.0 on {storeOf(tpmStorage)}
                      </PropertyRow>
                    )}
                  </PropertyList>
                </FormSection>
                <FormSection title="Disks" action={editStep("disks")}>
                  <PropertyList>
                    {disks.length === 0 && (
                      <PropertyRow label="Disks">None</PropertyRow>
                    )}
                    {disks.map((disk, idx) => (
                      <PropertyRow
                        key={disk.id}
                        label={
                          <span className="font-mono text-[12.5px]">
                            {diskDevices[idx]?.name ?? `disk${String(idx)}`}
                          </span>
                        }
                      >
                        {disk.size} GiB
                        {` on ${storeOf(disk.storage)}`} (
                        {effectiveFormat(disk)}){disk.discard ? ", TRIM" : ""}
                        {disk.ssd && busTakes(disk.bus, "ssd") ? ", SSD" : ""}
                        {disk.iothread && busTakes(disk.bus, "iothread")
                          ? ", IO thread"
                          : ""}
                      </PropertyRow>
                    ))}
                  </PropertyList>
                </FormSection>
                <FormSection title="CPU and memory" action={editStep("cpu")}>
                  <PropertyList>
                    <PropertyRow label="CPU">
                      {cores} {Number(cores) === 1 ? "core" : "cores"} ×{" "}
                      {sockets} {Number(sockets) === 1 ? "socket" : "sockets"},{" "}
                      {cpuType}
                      {numa ? ", NUMA" : ""}
                    </PropertyRow>
                    <PropertyRow label="Memory">
                      {memory} MiB
                      {balloonEnabled
                        ? balloonMin
                          ? `, ballooning down to ${balloonMin} MiB`
                          : ", ballooning on"
                        : ", ballooning off"}
                    </PropertyRow>
                  </PropertyList>
                </FormSection>
                <FormSection title="Network" action={editStep("network")}>
                  <PropertyList>
                    <PropertyRow label="Card">
                      {netModels.find((nm) => nm.value === netModel)?.label ??
                        netModel}{" "}
                      on {bridge}
                      {vlan ? `, VLAN ${vlan}` : ""}
                      {firewall ? ", firewall" : ""}
                      {rateLimit ? `, ${rateLimit} MB/s` : ""}
                    </PropertyRow>
                    {(ciuser || sshkeys || ipconfig0) && (
                      <PropertyRow label="Cloud-init">
                        {[
                          ciuser ? `user ${ciuser}` : "",
                          sshkeys ? "SSH keys" : "",
                          ipconfig0,
                        ]
                          .filter((part) => part !== "")
                          .join(", ")}
                      </PropertyRow>
                    )}
                  </PropertyList>
                </FormSection>
              </div>
            )}
          </Wizard>
        )}
      </DialogContent>
    </Dialog>
  );
}
