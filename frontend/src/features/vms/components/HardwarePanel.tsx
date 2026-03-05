import { useEffect, useState, useMemo } from "react";
import { Loader2, Save, AlertTriangle, ChevronDown, ChevronRight, Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useVMConfig, useSetVMConfig, useResizeDisk } from "../api/vm-queries";
import { useClusterStorage, useStorageContent } from "@/features/storage/api/storage-queries";
import {
  cpuTypes,
  scsiControllers,
  netModels,
  vgaTypes,
  biosOptions,
  machineTypes,
  cacheModes,
  diskFormats,
  osTypes,
  audioDevices,
} from "../lib/vm-config-constants";
import {
  parseNet0,
  buildNet0,
  parseAgent,
  buildAgent,
  parseVGA,
  buildVGA,
  parseDisk,
  parseAudio,
  buildAudio,
  parseStartup,
  buildStartup,
  parseBootOrder,
  buildBootOrder,
} from "../lib/vm-config-parsers";
import type { VMConfig } from "../types/vm";

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

const DISK_KEY_RE = /^(scsi|ide|sata|virtio|efidisk|tpmstate|unused)\d+$/;

const diskBusTypes = [
  { value: "scsi", label: "SCSI", max: 30 },
  { value: "virtio", label: "VirtIO Block", max: 15 },
  { value: "sata", label: "SATA", max: 5 },
  { value: "ide", label: "IDE", max: 3 },
] as const;

interface HardwarePanelProps {
  clusterId: string;
  vmId: string;
  vmStatus: string;
}

function str(val: unknown): string {
  if (val == null) return "";
  return String(val);
}

function num(val: unknown): number {
  if (val == null) return 0;
  const n = Number(val);
  return Number.isNaN(n) ? 0 : n;
}

function bool01(val: unknown): boolean {
  return Number(val) === 1;
}

interface SectionProps {
  title: string;
  children: React.ReactNode;
  defaultOpen?: boolean;
}

function Section({ title, children, defaultOpen = true }: SectionProps) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <Card className="overflow-hidden">
      <CardHeader
        className="cursor-pointer select-none px-3 py-2"
        onClick={() => { setOpen(!open); }}
      >
        <CardTitle className="flex items-center gap-2 text-xs font-medium">
          {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
          {title}
        </CardTitle>
      </CardHeader>
      {open && <CardContent className="px-3 pb-3 pt-0">{children}</CardContent>}
    </Card>
  );
}

/** Per-disk editable options tracked in local state. */
interface DiskEdit {
  cache: string;
  discard: boolean;
  ssd: boolean;
  iothread: boolean;
  newSize: string;
}

export function HardwarePanel({ clusterId, vmId, vmStatus }: HardwarePanelProps) {
  const { data: config, isLoading } = useVMConfig(clusterId, vmId);
  const setConfigMutation = useSetVMConfig();
  const resizeMutation = useResizeDisk();

  // --- Original config snapshot for diff comparison ---
  const [original, setOriginal] = useState<VMConfig | null>(null);

  // --- CPU ---
  const [cores, setCores] = useState("1");
  const [sockets, setSockets] = useState("1");
  const [cpuType, setCpuType] = useState("x86-64-v2-AES");
  const [numa, setNuma] = useState(false);
  const [cpulimit, setCpulimit] = useState("");
  const [cpuunits, setCpuunits] = useState("");
  const [affinity, setAffinity] = useState("");

  // --- Memory ---
  const [memory, setMemory] = useState("2048");
  const [balloon, setBalloon] = useState("");
  const [shares, setShares] = useState("");

  // --- System ---
  const [bios, setBios] = useState("seabios");
  const [machine, setMachine] = useState("pc");
  const [scsihw, setScsihw] = useState("virtio-scsi-pci");
  const [agentEnabled, setAgentEnabled] = useState(false);
  const [agentFstrim, setAgentFstrim] = useState(false);
  const [hasTpm, setHasTpm] = useState(false);
  const [kvmEnabled, setKvmEnabled] = useState(true);
  const [acpi, setAcpi] = useState(true);

  // --- Boot / Options ---
  const [onboot, setOnboot] = useState(false);
  const [tablet, setTablet] = useState(true);
  const [hotplug, setHotplug] = useState("");
  const [bootOrder, setBootOrder] = useState("");
  const [ostype, setOstype] = useState("l26");
  const [protection, setProtection] = useState(false);
  const [localtime, setLocaltime] = useState(false);
  const [startupOrder, setStartupOrder] = useState("");
  const [startupUp, setStartupUp] = useState("");
  const [startupDown, setStartupDown] = useState("");

  // --- Network (net0 only) ---
  const [netModel, setNetModel] = useState("virtio");
  const [netMac, setNetMac] = useState("");
  const [netBridge, setNetBridge] = useState("");
  const [netFirewall, setNetFirewall] = useState(false);
  const [netVlan, setNetVlan] = useState("");
  const [netRate, setNetRate] = useState("");
  const [netMtu, setNetMtu] = useState("");
  const [netLinkDown, setNetLinkDown] = useState(false);

  // --- Display ---
  const [vgaType, setVgaType] = useState("std");
  const [vgaMemory, setVgaMemory] = useState("");
  const [audioDevice, setAudioDevice] = useState("");

  // --- CD/DVD (ide2) ---
  const [cdromValue, setCdromValue] = useState("none");
  const [cdromStorageId, setCdromStorageId] = useState("");

  // --- Meta ---
  const [description, setDescription] = useState("");
  const [tags, setTags] = useState("");

  // --- Disk edits (keyed by device name e.g. "scsi0") ---
  const [diskEdits, setDiskEdits] = useState<Record<string, DiskEdit>>({});
  // --- Disks pending removal ---
  const [disksToRemove, setDisksToRemove] = useState<Set<string>>(new Set());
  // --- Add-disk form ---
  const [showAddDisk, setShowAddDisk] = useState(false);
  const [addDiskBus, setAddDiskBus] = useState("scsi");
  const [addDiskStorage, setAddDiskStorage] = useState("");
  const [addDiskSize, setAddDiskSize] = useState("32");
  const [addDiskFormat, setAddDiskFormat] = useState("qcow2");
  const [addDiskCache, setAddDiskCache] = useState("");
  const [addDiskDiscard, setAddDiskDiscard] = useState(false);
  const [addDiskSsd, setAddDiskSsd] = useState(false);
  const [addDiskIothread, setAddDiskIothread] = useState(false);
  // --- Pending new disks to add on save ---
  const [pendingNewDisks, setPendingNewDisks] = useState<Array<{
    key: string;
    storage: string;
    size: string;
    format: string;
    cache: string;
    discard: boolean;
    ssd: boolean;
    iothread: boolean;
  }>>([]);

  const { data: storages } = useClusterStorage(clusterId);

  // Filter storages that support disk images, deduplicated by storage name
  const diskStorages = useMemo(() => {
    if (!storages) return [];
    const seen = new Set<string>();
    return storages.filter((s) => {
      if (!s.content.includes("images") || !s.enabled || !s.active) return false;
      if (seen.has(s.storage)) return false;
      seen.add(s.storage);
      return true;
    });
  }, [storages]);

  // Filter storages that support ISO content, deduplicated by storage name
  const isoStorages = useMemo(() => {
    if (!storages) return [];
    const seen = new Set<string>();
    return storages.filter((s) => {
      if (!s.content.includes("iso") || !s.enabled || !s.active) return false;
      if (seen.has(s.storage)) return false;
      seen.add(s.storage);
      return true;
    });
  }, [storages]);

  // Fetch ISO content from the selected storage
  const { data: isoContent } = useStorageContent(clusterId, cdromStorageId);
  const isoFiles = useMemo(() => {
    if (!isoContent) return [];
    return isoContent.filter((item) => item.content === "iso");
  }, [isoContent]);

  // Auto-select first ISO storage when storages load (for browsing ISOs)
  useEffect(() => {
    if (isoStorages.length > 0 && !cdromStorageId) {
      setCdromStorageId(isoStorages[0]?.id ?? "");
    }
  }, [isoStorages, cdromStorageId]);

  function updateDiskEdit(key: string, partial: Partial<DiskEdit>) {
    setDiskEdits((prev) => ({
      ...prev,
      [key]: { ...(prev[key] ?? { cache: "", discard: false, ssd: false, iothread: false, newSize: "" }), ...partial },
    }));
  }

  function findNextDiskIndex(bus: string): number {
    const busInfo = diskBusTypes.find((b) => b.value === bus);
    const maxIdx = busInfo?.max ?? 15;
    const usedIndices = new Set<number>();
    // From existing config
    if (config) {
      for (const key of Object.keys(config)) {
        if (key.startsWith(bus)) {
          const idx = parseInt(key.slice(bus.length), 10);
          if (!Number.isNaN(idx)) usedIndices.add(idx);
        }
      }
    }
    // From pending new disks
    for (const d of pendingNewDisks) {
      if (d.key.startsWith(bus)) {
        const idx = parseInt(d.key.slice(bus.length), 10);
        if (!Number.isNaN(idx)) usedIndices.add(idx);
      }
    }
    for (let i = 0; i <= maxIdx; i++) {
      if (!usedIndices.has(i) && !disksToRemove.has(`${bus}${String(i)}`)) return i;
    }
    return maxIdx;
  }

  function handleAddDisk() {
    const idx = findNextDiskIndex(addDiskBus);
    const key = `${addDiskBus}${String(idx)}`;
    setPendingNewDisks((prev) => [
      ...prev,
      {
        key,
        storage: addDiskStorage,
        size: addDiskSize,
        format: addDiskFormat,
        cache: addDiskCache,
        discard: addDiskDiscard,
        ssd: addDiskSsd,
        iothread: addDiskIothread,
      },
    ]);
    setShowAddDisk(false);
    setAddDiskSize("32");
    setAddDiskFormat("qcow2");
    setAddDiskCache("");
    setAddDiskDiscard(false);
    setAddDiskSsd(false);
    setAddDiskIothread(false);
  }

  function handleRemoveDisk(key: string) {
    setDisksToRemove((prev) => new Set(prev).add(key));
  }

  function handleUndoRemoveDisk(key: string) {
    setDisksToRemove((prev) => {
      const next = new Set(prev);
      next.delete(key);
      return next;
    });
  }

  function handleRemovePendingDisk(key: string) {
    setPendingNewDisks((prev) => prev.filter((d) => d.key !== key));
  }

  // Populate fields from config
  useEffect(() => {
    if (!config) return;
    setOriginal(config);

    // CPU
    setCores(str(config["cores"] ?? 1));
    setSockets(str(config["sockets"] ?? 1));
    setCpuType(str(config["cpu"] ?? "x86-64-v2-AES"));
    setNuma(bool01(config["numa"]));
    setCpulimit(config["cpulimit"] != null ? str(config["cpulimit"]) : "");
    setCpuunits(config["cpuunits"] != null ? str(config["cpuunits"]) : "");
    setAffinity(str(config["affinity"] ?? ""));

    // Memory
    setMemory(str(config["memory"] ?? 2048));
    setBalloon(config["balloon"] != null ? str(config["balloon"]) : "");
    setShares(config["shares"] != null ? str(config["shares"]) : "");

    // System
    setBios(str(config["bios"] ?? "seabios"));
    setMachine(str(config["machine"] ?? "pc"));
    setScsihw(str(config["scsihw"] ?? "virtio-scsi-pci"));
    const agent = parseAgent(str(config["agent"] ?? ""));
    setAgentEnabled(agent.enabled);
    setAgentFstrim(agent.fstrimClonedDisks);
    setHasTpm(config["tpmstate0"] != null && str(config["tpmstate0"]) !== "");
    setKvmEnabled(config["kvm"] != null ? bool01(config["kvm"]) : true);
    setAcpi(config["acpi"] != null ? bool01(config["acpi"]) : true);

    // Initialize disk edits from config
    const edits: Record<string, DiskEdit> = {};
    for (const [key, val] of Object.entries(config)) {
      if (DISK_KEY_RE.test(key) && !key.startsWith("efidisk") && !key.startsWith("tpmstate") && !key.startsWith("unused")) {
        const d = parseDisk(str(val));
        edits[key] = {
          cache: d.cache,
          discard: d.discard,
          ssd: d.ssd,
          iothread: d.iothread,
          newSize: "",
        };
      }
    }
    setDiskEdits(edits);

    // Boot / Options
    setOnboot(bool01(config["onboot"]));
    setTablet(config["tablet"] != null ? bool01(config["tablet"]) : true);
    setHotplug(str(config["hotplug"] ?? ""));
    setOstype(str(config["ostype"] ?? "l26"));
    setProtection(bool01(config["protection"]));
    setLocaltime(bool01(config["localtime"]));
    const bootDevices = parseBootOrder(str(config["boot"] ?? ""));
    setBootOrder(bootDevices.join(";"));
    const startup = parseStartup(str(config["startup"] ?? ""));
    setStartupOrder(startup.order);
    setStartupUp(startup.up);
    setStartupDown(startup.down);

    // Network
    const net = parseNet0(str(config["net0"] ?? ""));
    setNetModel(net.model);
    setNetMac(net.mac);
    setNetBridge(net.bridge);
    setNetFirewall(net.firewall);
    setNetVlan(net.vlanTag);
    setNetRate(net.rateLimit);
    setNetMtu(net.mtu);
    // link_down is a sub-param of net0 — check raw string
    const net0Raw = str(config["net0"] ?? "");
    setNetLinkDown(net0Raw.includes("link_down=1"));

    // Display
    const vga = parseVGA(str(config["vga"] ?? ""));
    setVgaType(vga.type);
    setVgaMemory(vga.memory);
    const audio = parseAudio(str(config["audio0"] ?? ""));
    setAudioDevice(audio.device);

    // CD/DVD (ide2)
    const ide2Val = str(config["ide2"] ?? "");
    if (ide2Val && ide2Val !== "none,media=cdrom") {
      // Extract the volid — e.g. "local:iso/ubuntu.iso,media=cdrom" -> "local:iso/ubuntu.iso"
      const parts = ide2Val.split(",");
      setCdromValue(parts[0] ?? "none");
    } else {
      setCdromValue("none");
    }

    // Meta
    setDescription(str(config["description"] ?? ""));
    setTags(str(config["tags"] ?? ""));
  }, [config]);

  // Discover disk keys from config (exclude ide2 cdrom)
  const diskEntries = useMemo(() => {
    if (!config) return [];
    return Object.entries(config)
      .filter(([key, val]) => {
        if (!DISK_KEY_RE.test(key)) return false;
        // Exclude CD/DVD drives (ide2 with media=cdrom or "none")
        if (key === "ide2") {
          const v = str(val);
          if (v.includes("media=cdrom") || v === "none,media=cdrom" || v === "none") return false;
        }
        return true;
      })
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([key, val]) => ({ key, parsed: parseDisk(str(val)) }));
  }, [config]);

  // Discover additional NICs (net1, net2, ...)
  const extraNics = useMemo(() => {
    if (!config) return [];
    return Object.entries(config)
      .filter(([key]) => /^net\d+$/.test(key) && key !== "net0")
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([key, val]) => ({ key, raw: str(val) }));
  }, [config]);

  const isRunning = vmStatus.toLowerCase() === "running";

  function buildCurrentNet0(): string {
    let net0 = buildNet0({
      model: netModel,
      mac: netMac,
      bridge: netBridge,
      firewall: netFirewall,
      vlanTag: netVlan,
      rateLimit: netRate,
      mtu: netMtu,
      multiqueue: "",
    });
    if (netLinkDown) net0 += ",link_down=1";
    return net0;
  }

  function handleSave() {
    if (!original) return;
    const fields: Record<string, string> = {};
    const deleteFields: string[] = [];

    // CPU
    if (cores !== str(original["cores"] ?? 1)) fields["cores"] = cores;
    if (sockets !== str(original["sockets"] ?? 1)) fields["sockets"] = sockets;
    if (cpuType !== str(original["cpu"] ?? "x86-64-v2-AES")) fields["cpu"] = cpuType;
    const origNuma = bool01(original["numa"]);
    if (numa !== origNuma) fields["numa"] = numa ? "1" : "0";
    const origCpulimit = original["cpulimit"] != null ? str(original["cpulimit"]) : "";
    if (cpulimit !== origCpulimit) fields["cpulimit"] = cpulimit || "0";
    const origCpuunits = original["cpuunits"] != null ? str(original["cpuunits"]) : "";
    if (cpuunits !== origCpuunits) {
      if (cpuunits) { fields["cpuunits"] = cpuunits; } else { deleteFields.push("cpuunits"); }
    }
    if (affinity !== str(original["affinity"] ?? "")) {
      if (affinity) { fields["affinity"] = affinity; } else { deleteFields.push("affinity"); }
    }

    // Memory
    if (memory !== str(original["memory"] ?? 2048)) fields["memory"] = memory;
    const origBalloon = original["balloon"] != null ? str(original["balloon"]) : "";
    if (balloon !== origBalloon) fields["balloon"] = balloon || "0";
    const origShares = original["shares"] != null ? str(original["shares"]) : "";
    if (shares !== origShares) {
      if (shares) { fields["shares"] = shares; } else { deleteFields.push("shares"); }
    }

    // System
    if (bios !== str(original["bios"] ?? "seabios")) fields["bios"] = bios;
    if (machine !== str(original["machine"] ?? "pc")) fields["machine"] = machine;
    if (scsihw !== str(original["scsihw"] ?? "virtio-scsi-pci")) fields["scsihw"] = scsihw;
    const newAgent = buildAgent({ enabled: agentEnabled, fstrimClonedDisks: agentFstrim });
    const origAgent = buildAgent(parseAgent(str(original["agent"] ?? "")));
    if (newAgent !== origAgent) fields["agent"] = newAgent;
    const origHasTpm = original["tpmstate0"] != null && str(original["tpmstate0"]) !== "";
    if (hasTpm !== origHasTpm) {
      if (hasTpm) {
        const firstDisk = Object.entries(original).find(([k]) => /^scsi\d+$/.test(k));
        const tpmStorage = firstDisk ? parseDisk(str(firstDisk[1])).storage : "local-lvm";
        fields["tpmstate0"] = `${tpmStorage}:1,version=v2.0`;
      } else {
        deleteFields.push("tpmstate0");
      }
    }
    const origKvm = original["kvm"] != null ? bool01(original["kvm"]) : true;
    if (kvmEnabled !== origKvm) fields["kvm"] = kvmEnabled ? "1" : "0";
    const origAcpi = original["acpi"] != null ? bool01(original["acpi"]) : true;
    if (acpi !== origAcpi) fields["acpi"] = acpi ? "1" : "0";

    // Disk config changes (cache, discard, ssd, iothread) via SetVMConfig
    for (const [key, edit] of Object.entries(diskEdits)) {
      const origRaw = str(original[key] ?? "");
      const origDisk = parseDisk(origRaw);
      if (
        edit.cache !== origDisk.cache ||
        edit.discard !== origDisk.discard ||
        edit.ssd !== origDisk.ssd ||
        edit.iothread !== origDisk.iothread
      ) {
        const segments = origRaw.split(",");
        const base = segments[0] ?? "";
        const kv = new Map<string, string>();
        for (let i = 1; i < segments.length; i++) {
          const seg = segments[i] ?? "";
          const eqIdx = seg.indexOf("=");
          if (eqIdx !== -1) {
            kv.set(seg.slice(0, eqIdx).trim(), seg.slice(eqIdx + 1).trim());
          }
        }
        if (edit.cache) { kv.set("cache", edit.cache); } else { kv.delete("cache"); }
        if (edit.discard) { kv.set("discard", "on"); } else { kv.delete("discard"); }
        if (edit.ssd) { kv.set("ssd", "1"); } else { kv.delete("ssd"); }
        if (edit.iothread) { kv.set("iothread", "1"); } else { kv.delete("iothread"); }
        const optParts: string[] = [];
        for (const [k, v] of kv) {
          optParts.push(`${k}=${v}`);
        }
        fields[key] = optParts.length > 0 ? `${base},${optParts.join(",")}` : base;
      }
    }

    // Boot / Options
    const origOnboot = bool01(original["onboot"]);
    if (onboot !== origOnboot) fields["onboot"] = onboot ? "1" : "0";
    const origTablet = original["tablet"] != null ? bool01(original["tablet"]) : true;
    if (tablet !== origTablet) fields["tablet"] = tablet ? "1" : "0";
    if (hotplug !== str(original["hotplug"] ?? "")) fields["hotplug"] = hotplug;
    if (ostype !== str(original["ostype"] ?? "l26")) fields["ostype"] = ostype;
    const origProtection = bool01(original["protection"]);
    if (protection !== origProtection) fields["protection"] = protection ? "1" : "0";
    const origLocaltime = bool01(original["localtime"]);
    if (localtime !== origLocaltime) fields["localtime"] = localtime ? "1" : "0";
    // Boot order
    const origBootDevices = parseBootOrder(str(original["boot"] ?? ""));
    const newBootStr = bootOrder.trim();
    if (newBootStr !== origBootDevices.join(";")) {
      const devices = newBootStr.split(";").filter(Boolean);
      fields["boot"] = buildBootOrder(devices);
    }
    // Startup order
    const origStartup = parseStartup(str(original["startup"] ?? ""));
    const newStartup = buildStartup({ order: startupOrder, up: startupUp, down: startupDown });
    const origStartupStr = buildStartup(origStartup);
    if (newStartup !== origStartupStr) {
      if (newStartup) { fields["startup"] = newStartup; } else { deleteFields.push("startup"); }
    }

    // Network
    const newNet0 = buildCurrentNet0();
    const origNet0 = str(original["net0"] ?? "");
    if (newNet0 !== origNet0) fields["net0"] = newNet0;

    // Display
    const newVga = buildVGA({ type: vgaType, memory: vgaMemory });
    const origVga = str(original["vga"] ?? "");
    if (newVga !== origVga) fields["vga"] = newVga;
    const newAudio = buildAudio({ device: audioDevice, driver: "spice" });
    const origAudio = str(original["audio0"] ?? "");
    if (newAudio !== origAudio) {
      if (newAudio) { fields["audio0"] = newAudio; } else { deleteFields.push("audio0"); }
    }

    // CD/DVD (ide2)
    const origIde2 = str(original["ide2"] ?? "");
    const origCdrom = origIde2 && origIde2 !== "none,media=cdrom"
      ? (origIde2.split(",")[0] ?? "none")
      : "none";
    if (cdromValue !== origCdrom) {
      if (cdromValue === "none") {
        fields["ide2"] = "none,media=cdrom";
      } else {
        fields["ide2"] = `${cdromValue},media=cdrom`;
      }
    }

    // Meta
    if (description !== str(original["description"] ?? "")) fields["description"] = description;
    if (tags !== str(original["tags"] ?? "")) fields["tags"] = tags;

    // Disk removals
    for (const diskKey of disksToRemove) {
      deleteFields.push(diskKey);
    }

    // New disks
    for (const newDisk of pendingNewDisks) {
      let val = `${newDisk.storage}:${newDisk.size}`;
      if (newDisk.format) val += `,format=${newDisk.format}`;
      if (newDisk.cache) val += `,cache=${newDisk.cache}`;
      if (newDisk.discard) val += ",discard=on";
      if (newDisk.ssd) val += ",ssd=1";
      if (newDisk.iothread) val += ",iothread=1";
      fields[newDisk.key] = val;
    }

    // Combine delete fields
    if (deleteFields.length > 0) {
      const existing = fields["delete"] ?? "";
      const all = existing ? `${existing},${deleteFields.join(",")}` : deleteFields.join(",");
      fields["delete"] = all;
    }

    // Handle disk resizes via the separate resize API
    for (const [key, edit] of Object.entries(diskEdits)) {
      if (edit.newSize) {
        resizeMutation.mutate({ clusterId, vmId, disk: key, size: edit.newSize });
      }
    }

    if (Object.keys(fields).length === 0) return;

    setConfigMutation.mutate({ clusterId, vmId, fields }, {
      onSuccess: () => {
        // Clear pending state after successful save
        setDisksToRemove(new Set());
        setPendingNewDisks([]);
      },
    });
  }

  const hasChanges = useMemo(() => {
    if (!original) return false;
    if (cores !== str(original["cores"] ?? 1)) return true;
    if (sockets !== str(original["sockets"] ?? 1)) return true;
    if (cpuType !== str(original["cpu"] ?? "x86-64-v2-AES")) return true;
    if (numa !== bool01(original["numa"])) return true;
    if (cpulimit !== (original["cpulimit"] != null ? str(original["cpulimit"]) : "")) return true;
    if (cpuunits !== (original["cpuunits"] != null ? str(original["cpuunits"]) : "")) return true;
    if (affinity !== str(original["affinity"] ?? "")) return true;
    if (memory !== str(original["memory"] ?? 2048)) return true;
    if (balloon !== (original["balloon"] != null ? str(original["balloon"]) : "")) return true;
    if (shares !== (original["shares"] != null ? str(original["shares"]) : "")) return true;
    if (bios !== str(original["bios"] ?? "seabios")) return true;
    if (machine !== str(original["machine"] ?? "pc")) return true;
    if (scsihw !== str(original["scsihw"] ?? "virtio-scsi-pci")) return true;
    if (buildAgent({ enabled: agentEnabled, fstrimClonedDisks: agentFstrim }) !== buildAgent(parseAgent(str(original["agent"] ?? "")))) return true;
    if (hasTpm !== (original["tpmstate0"] != null && str(original["tpmstate0"]) !== "")) return true;
    if (kvmEnabled !== (original["kvm"] != null ? bool01(original["kvm"]) : true)) return true;
    if (acpi !== (original["acpi"] != null ? bool01(original["acpi"]) : true)) return true;
    if (onboot !== bool01(original["onboot"])) return true;
    if (tablet !== (original["tablet"] != null ? bool01(original["tablet"]) : true)) return true;
    if (hotplug !== str(original["hotplug"] ?? "")) return true;
    if (ostype !== str(original["ostype"] ?? "l26")) return true;
    if (protection !== bool01(original["protection"])) return true;
    if (localtime !== bool01(original["localtime"])) return true;
    const origBootDevices = parseBootOrder(str(original["boot"] ?? ""));
    if (bootOrder.trim() !== origBootDevices.join(";")) return true;
    const origStartup = parseStartup(str(original["startup"] ?? ""));
    if (buildStartup({ order: startupOrder, up: startupUp, down: startupDown }) !== buildStartup(origStartup)) return true;
    if (buildCurrentNet0() !== str(original["net0"] ?? "")) return true;
    if (buildVGA({ type: vgaType, memory: vgaMemory }) !== str(original["vga"] ?? "")) return true;
    if (buildAudio({ device: audioDevice, driver: "spice" }) !== str(original["audio0"] ?? "")) return true;
    // CD/DVD
    const origIde2 = str(original["ide2"] ?? "");
    const origCdromVal = origIde2 && origIde2 !== "none,media=cdrom"
      ? (origIde2.split(",")[0] ?? "none")
      : "none";
    if (cdromValue !== origCdromVal) return true;
    if (description !== str(original["description"] ?? "")) return true;
    if (tags !== str(original["tags"] ?? "")) return true;
    for (const [key, edit] of Object.entries(diskEdits)) {
      if (edit.newSize) return true;
      const origDisk = parseDisk(str(original[key] ?? ""));
      if (edit.cache !== origDisk.cache || edit.discard !== origDisk.discard || edit.ssd !== origDisk.ssd || edit.iothread !== origDisk.iothread) return true;
    }
    if (disksToRemove.size > 0) return true;
    if (pendingNewDisks.length > 0) return true;
    return false;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [original, cores, sockets, cpuType, numa, cpulimit, cpuunits, affinity, memory, balloon, shares, bios, machine, scsihw, agentEnabled, agentFstrim, hasTpm, kvmEnabled, acpi, onboot, tablet, hotplug, ostype, protection, localtime, bootOrder, startupOrder, startupUp, startupDown, netModel, netMac, netBridge, netFirewall, netVlan, netRate, netMtu, netLinkDown, vgaType, vgaMemory, audioDevice, cdromValue, description, tags, diskEdits, disksToRemove, pendingNewDisks]);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-8">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!config) {
    return (
      <div className="rounded-lg border p-6 text-center">
        <p className="text-sm text-muted-foreground">
          Unable to load VM configuration.
        </p>
      </div>
    );
  }

  const compactSelect = selectClass + " h-8 text-xs";

  return (
    <div className="space-y-3">
      {isRunning && (
        <div className="flex items-center gap-2 rounded-md border border-amber-300 bg-amber-50 px-3 py-1.5 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200">
          <AlertTriangle className="h-3.5 w-3.5 flex-shrink-0" />
          <span>Running VM — hardware changes will be pending until restart.</span>
        </div>
      )}

      {/* 2-column grid for compact cards */}
      <div className="grid gap-3 lg:grid-cols-2">

        {/* CPU */}
        <Section title="CPU">
          <div className="grid grid-cols-2 gap-2">
            <div className="space-y-1">
              <Label className="text-xs">Cores</Label>
              <Input type="number" min={1} value={cores} onChange={(e) => { setCores(e.target.value); }} className="h-8 text-xs" />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Sockets</Label>
              <Input type="number" min={1} value={sockets} onChange={(e) => { setSockets(e.target.value); }} className="h-8 text-xs" />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Type</Label>
              <select className={compactSelect} value={cpuType} onChange={(e) => { setCpuType(e.target.value); }}>
                {cpuTypes.map((t) => (<option key={t} value={t}>{t}</option>))}
              </select>
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Limit</Label>
              <Input type="number" min={0} max={128} step={0.1} value={cpulimit} onChange={(e) => { setCpulimit(e.target.value); }} placeholder="0 (none)" className="h-8 text-xs" />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Units</Label>
              <Input type="number" min={1} max={262144} value={cpuunits} onChange={(e) => { setCpuunits(e.target.value); }} placeholder="100" className="h-8 text-xs" />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Affinity</Label>
              <Input value={affinity} onChange={(e) => { setAffinity(e.target.value); }} placeholder="e.g. 0,5,8-11" className="h-8 text-xs" />
            </div>
          </div>
          <div className="mt-2 flex items-center gap-4">
            <div className="flex items-center gap-1.5">
              <Checkbox id="hw-numa" checked={numa} onCheckedChange={(v) => { setNuma(v === true); }} />
              <Label htmlFor="hw-numa" className="cursor-pointer text-xs">NUMA</Label>
            </div>
            <span className="text-[10px] text-muted-foreground">vCPUs: {num(cores) * num(sockets)}</span>
          </div>
        </Section>

        {/* Memory */}
        <Section title="Memory">
          <div className="grid grid-cols-2 gap-2">
            <div className="space-y-1">
              <Label className="text-xs">Memory (MiB)</Label>
              <Input type="number" min={64} step={64} value={memory} onChange={(e) => { setMemory(e.target.value); }} className="h-8 text-xs" />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Balloon Min (MiB)</Label>
              <Input type="number" min={0} step={64} value={balloon} onChange={(e) => { setBalloon(e.target.value); }} placeholder="0=disabled" className="h-8 text-xs" />
            </div>
            <div className="col-span-2 space-y-1">
              <Label className="text-xs">Ballooning Shares</Label>
              <Input type="number" min={0} max={50000} value={shares} onChange={(e) => { setShares(e.target.value); }} placeholder="1000 (default)" className="h-8 text-xs" />
            </div>
          </div>
        </Section>

        {/* System */}
        <Section title="System">
          <div className="grid grid-cols-2 gap-2">
            <div className="space-y-1">
              <Label className="text-xs">BIOS</Label>
              <select className={compactSelect} value={bios} onChange={(e) => { setBios(e.target.value); }}>
                {biosOptions.map((o) => (<option key={o.value} value={o.value}>{o.label}</option>))}
              </select>
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Machine</Label>
              <select className={compactSelect} value={machine} onChange={(e) => { setMachine(e.target.value); }}>
                {machineTypes.map((o) => (<option key={o.value} value={o.value}>{o.label}</option>))}
              </select>
            </div>
            <div className="col-span-2 space-y-1">
              <Label className="text-xs">SCSI Controller</Label>
              <select className={compactSelect} value={scsihw} onChange={(e) => { setScsihw(e.target.value); }}>
                {scsiControllers.map((o) => (<option key={o.value} value={o.value}>{o.label}</option>))}
              </select>
            </div>
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1.5">
            <div className="flex items-center gap-1.5">
              <Checkbox id="hw-agent" checked={agentEnabled} onCheckedChange={(v) => { setAgentEnabled(v === true); }} />
              <Label htmlFor="hw-agent" className="cursor-pointer text-xs">Agent</Label>
            </div>
            {agentEnabled && (
              <div className="flex items-center gap-1.5">
                <Checkbox id="hw-agent-fstrim" checked={agentFstrim} onCheckedChange={(v) => { setAgentFstrim(v === true); }} />
                <Label htmlFor="hw-agent-fstrim" className="cursor-pointer text-xs">FSTRIM</Label>
              </div>
            )}
            <div className="flex items-center gap-1.5">
              <Checkbox id="hw-tpm" checked={hasTpm} onCheckedChange={(v) => { setHasTpm(v === true); }} />
              <Label htmlFor="hw-tpm" className="cursor-pointer text-xs">TPM</Label>
            </div>
            <div className="flex items-center gap-1.5">
              <Checkbox id="hw-kvm" checked={kvmEnabled} onCheckedChange={(v) => { setKvmEnabled(v === true); }} />
              <Label htmlFor="hw-kvm" className="cursor-pointer text-xs">KVM</Label>
            </div>
            <div className="flex items-center gap-1.5">
              <Checkbox id="hw-acpi" checked={acpi} onCheckedChange={(v) => { setAcpi(v === true); }} />
              <Label htmlFor="hw-acpi" className="cursor-pointer text-xs">ACPI</Label>
            </div>
          </div>
        </Section>

        {/* Boot / Options */}
        <Section title="Boot / Options">
          <div className="grid grid-cols-2 gap-2">
            <div className="space-y-1">
              <Label className="text-xs">OS Type</Label>
              <select className={compactSelect} value={ostype} onChange={(e) => { setOstype(e.target.value); }}>
                {osTypes.map((o) => (<option key={o.value} value={o.value}>{o.label}</option>))}
              </select>
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Hotplug</Label>
              <div className="flex flex-wrap gap-x-3 gap-y-1 pt-1">
                {(["network", "disk", "usb", "memory", "cpu"] as const).map((opt) => {
                  const parts = hotplug ? hotplug.split(",").filter(Boolean) : [];
                  const checked = parts.includes(opt);
                  return (
                    <div key={opt} className="flex items-center gap-1">
                      <Checkbox
                        id={`hp-${opt}`}
                        checked={checked}
                        onCheckedChange={(v) => {
                          const next = v === true
                            ? [...parts, opt]
                            : parts.filter((p) => p !== opt);
                          setHotplug(next.join(","));
                        }}
                      />
                      <Label htmlFor={`hp-${opt}`} className="cursor-pointer text-xs capitalize">{opt}</Label>
                    </div>
                  );
                })}
              </div>
            </div>
            <div className="col-span-2 space-y-1">
              <Label className="text-xs">Boot Order</Label>
              <Input value={bootOrder} onChange={(e) => { setBootOrder(e.target.value); }} placeholder="scsi0;ide2;net0" className="h-8 text-xs" />
            </div>
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1.5">
            <div className="flex items-center gap-1.5">
              <Checkbox id="hw-onboot" checked={onboot} onCheckedChange={(v) => { setOnboot(v === true); }} />
              <Label htmlFor="hw-onboot" className="cursor-pointer text-xs">Start at boot</Label>
            </div>
            <div className="flex items-center gap-1.5">
              <Checkbox id="hw-tablet" checked={tablet} onCheckedChange={(v) => { setTablet(v === true); }} />
              <Label htmlFor="hw-tablet" className="cursor-pointer text-xs">Tablet</Label>
            </div>
            <div className="flex items-center gap-1.5">
              <Checkbox id="hw-protection" checked={protection} onCheckedChange={(v) => { setProtection(v === true); }} />
              <Label htmlFor="hw-protection" className="cursor-pointer text-xs">Protection</Label>
            </div>
            <div className="flex items-center gap-1.5">
              <Checkbox id="hw-localtime" checked={localtime} onCheckedChange={(v) => { setLocaltime(v === true); }} />
              <Label htmlFor="hw-localtime" className="cursor-pointer text-xs">Local Time</Label>
            </div>
          </div>
          <div className="mt-2 grid grid-cols-3 gap-2">
            <div className="space-y-1">
              <Label className="text-xs">Startup Order</Label>
              <Input type="number" min={0} value={startupOrder} onChange={(e) => { setStartupOrder(e.target.value); }} placeholder="#" className="h-8 text-xs" />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Up Delay (s)</Label>
              <Input type="number" min={0} value={startupUp} onChange={(e) => { setStartupUp(e.target.value); }} placeholder="0" className="h-8 text-xs" />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Down (s)</Label>
              <Input type="number" min={0} value={startupDown} onChange={(e) => { setStartupDown(e.target.value); }} placeholder="0" className="h-8 text-xs" />
            </div>
          </div>
        </Section>

        {/* Network */}
        <Section title="Network (net0)">
          {config["net0"] != null ? (
            <>
              <div className="grid grid-cols-2 gap-2">
                <div className="space-y-1">
                  <Label className="text-xs">Model</Label>
                  <select className={compactSelect} value={netModel} onChange={(e) => { setNetModel(e.target.value); }}>
                    {netModels.map((m) => (<option key={m.value} value={m.value}>{m.label}</option>))}
                  </select>
                </div>
                <div className="space-y-1">
                  <Label className="text-xs">Bridge</Label>
                  <Input value={netBridge} onChange={(e) => { setNetBridge(e.target.value); }} placeholder="vmbr0" className="h-8 text-xs" />
                </div>
                <div className="col-span-2 space-y-1">
                  <Label className="text-xs">MAC Address</Label>
                  <Input value={netMac} onChange={(e) => { setNetMac(e.target.value); }} placeholder="Auto-generated" className="h-8 text-xs" />
                </div>
                <div className="space-y-1">
                  <Label className="text-xs">VLAN</Label>
                  <Input type="number" min={1} max={4094} value={netVlan} onChange={(e) => { setNetVlan(e.target.value); }} placeholder="None" className="h-8 text-xs" />
                </div>
                <div className="space-y-1">
                  <Label className="text-xs">Rate (MB/s)</Label>
                  <Input type="number" min={0} value={netRate} onChange={(e) => { setNetRate(e.target.value); }} placeholder="Unlimited" className="h-8 text-xs" />
                </div>
                <div className="space-y-1">
                  <Label className="text-xs">MTU</Label>
                  <Input type="number" min={0} value={netMtu} onChange={(e) => { setNetMtu(e.target.value); }} placeholder="Default" className="h-8 text-xs" />
                </div>
              </div>
              <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1.5">
                <div className="flex items-center gap-1.5">
                  <Checkbox id="hw-net-firewall" checked={netFirewall} onCheckedChange={(v) => { setNetFirewall(v === true); }} />
                  <Label htmlFor="hw-net-firewall" className="cursor-pointer text-xs">Firewall</Label>
                </div>
                <div className="flex items-center gap-1.5">
                  <Checkbox id="hw-net-linkdown" checked={netLinkDown} onCheckedChange={(v) => { setNetLinkDown(v === true); }} />
                  <Label htmlFor="hw-net-linkdown" className="cursor-pointer text-xs">Disconnect</Label>
                </div>
              </div>
            </>
          ) : (
            <p className="text-xs text-muted-foreground">No net0 configured.</p>
          )}
          {extraNics.length > 0 && (
            <div className="mt-2">
              <p className="mb-0.5 text-[10px] font-medium text-muted-foreground">Additional NICs</p>
              {extraNics.map(({ key, raw }) => (
                <p key={key} className="truncate font-mono text-[10px]">{key}: {raw}</p>
              ))}
            </div>
          )}
        </Section>

        {/* Display / Audio */}
        <Section title="Display / Audio">
          <div className="grid grid-cols-2 gap-2">
            <div className="space-y-1">
              <Label className="text-xs">VGA</Label>
              <select className={compactSelect} value={vgaType} onChange={(e) => { setVgaType(e.target.value); }}>
                {vgaTypes.map((v) => (<option key={v.value} value={v.value}>{v.label}</option>))}
              </select>
            </div>
            {vgaType === "qxl" ? (
              <div className="space-y-1">
                <Label className="text-xs">QXL Memory (MB)</Label>
                <Input type="number" min={0} value={vgaMemory} onChange={(e) => { setVgaMemory(e.target.value); }} placeholder="Default" className="h-8 text-xs" />
              </div>
            ) : (
              <div className="space-y-1">
                <Label className="text-xs">Audio</Label>
                <select className={compactSelect} value={audioDevice} onChange={(e) => { setAudioDevice(e.target.value); }}>
                  {audioDevices.map((a) => (<option key={a.value} value={a.value}>{a.label}</option>))}
                </select>
              </div>
            )}
            {vgaType === "qxl" && (
              <div className="col-span-2 space-y-1">
                <Label className="text-xs">Audio</Label>
                <select className={compactSelect} value={audioDevice} onChange={(e) => { setAudioDevice(e.target.value); }}>
                  {audioDevices.map((a) => (<option key={a.value} value={a.value}>{a.label}</option>))}
                </select>
              </div>
            )}
          </div>
        </Section>

        {/* CD/DVD Drive (ide2) */}
        <Section title="CD/DVD Drive">
          <div className="space-y-2">
            <div className="space-y-1">
              <Label className="text-xs">ISO Image</Label>
              <select
                className={compactSelect}
                value={cdromValue}
                onChange={(e) => { setCdromValue(e.target.value); }}
              >
                <option value="none">No media (empty drive)</option>
                {isoFiles.map((iso) => (
                  <option key={iso.volid} value={iso.volid}>
                    {iso.volid}
                  </option>
                ))}
              </select>
            </div>
            {isoStorages.length > 1 && (
              <div className="space-y-1">
                <Label className="text-xs">Browse storage</Label>
                <select
                  className={compactSelect}
                  value={cdromStorageId}
                  onChange={(e) => { setCdromStorageId(e.target.value); }}
                >
                  {isoStorages.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.storage}
                    </option>
                  ))}
                </select>
              </div>
            )}
            {cdromValue !== "none" && (
              <p className="text-[10px] text-muted-foreground">
                Current: {cdromValue}
              </p>
            )}
          </div>
        </Section>

        {/* Description / Tags */}
        <Section title="Description / Tags" defaultOpen={false}>
          <div className="space-y-2">
            <div className="space-y-1">
              <Label className="text-xs">Description</Label>
              <textarea
                value={description}
                onChange={(e) => { setDescription(e.target.value); }}
                rows={2}
                className="flex w-full rounded-md border border-input bg-transparent px-2 py-1.5 text-xs shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Tags</Label>
              <Input value={tags} onChange={(e) => { setTags(e.target.value); }} placeholder="Semicolon-separated" className="h-8 text-xs" />
            </div>
          </div>
        </Section>

      </div>{/* end 2-col grid */}

      {/* Disks — full width */}
      <Section title="Disks">
        {diskEntries.length > 0 && (
          <div className="space-y-2">
            {diskEntries.map(({ key, parsed }) => {
              const isRemoved = disksToRemove.has(key);
              if (key.startsWith("efidisk") || key.startsWith("tpmstate")) {
                return (
                  <div key={key} className="flex items-center gap-2 rounded border px-2 py-1 text-xs text-muted-foreground">
                    <span className="font-mono font-medium">{key}</span>
                    <span>{parsed.storage}:{parsed.volume.split(":")[1] ?? ""}</span>
                    <span className="text-[10px]">({key.startsWith("efidisk") ? "EFI" : "TPM"})</span>
                  </div>
                );
              }
              if (key.startsWith("unused")) {
                if (isRemoved) {
                  return (
                    <div key={key} className="flex items-center gap-2 rounded border border-red-300 bg-red-50 px-2 py-1 dark:border-red-800 dark:bg-red-950">
                      <span className="font-mono text-xs font-medium text-red-700 line-through dark:text-red-400">{key}</span>
                      <span className="text-[10px] text-red-600 dark:text-red-400">marked for removal</span>
                      <Button variant="ghost" size="sm" className="ml-auto h-6 px-2 text-[10px]" onClick={() => { handleUndoRemoveDisk(key); }}>Undo</Button>
                    </div>
                  );
                }
                return (
                  <div key={key} className="flex items-center gap-2 rounded border border-amber-300 bg-amber-50 px-2 py-1 dark:border-amber-800 dark:bg-amber-950">
                    <span className="font-mono text-xs font-medium text-amber-700 dark:text-amber-400">{key}</span>
                    <span className="truncate text-[10px] text-amber-600 dark:text-amber-400">{parsed.volume || str(config?.[key] ?? "")}</span>
                    <span className="rounded bg-amber-200 px-1 py-0.5 text-[9px] font-medium text-amber-800 dark:bg-amber-900 dark:text-amber-300">Unused</span>
                    <Button variant="ghost" size="sm" className="ml-auto h-6 gap-1 px-2 text-[10px] text-destructive hover:text-destructive" onClick={() => { handleRemoveDisk(key); }}>
                      <Trash2 className="h-3 w-3" /> Remove
                    </Button>
                  </div>
                );
              }
              if (isRemoved) {
                return (
                  <div key={key} className="flex items-center gap-2 rounded border border-red-300 bg-red-50 px-2 py-1 dark:border-red-800 dark:bg-red-950">
                    <span className="font-mono text-xs font-medium text-red-700 line-through dark:text-red-400">{key}</span>
                    <span className="text-[10px] text-red-600 dark:text-red-400">{parsed.storage} &middot; {parsed.size} — marked for removal</span>
                    <Button variant="ghost" size="sm" className="ml-auto h-6 px-2 text-[10px]" onClick={() => { handleUndoRemoveDisk(key); }}>Undo</Button>
                  </div>
                );
              }
              const edit = diskEdits[key] ?? { cache: parsed.cache, discard: parsed.discard, ssd: parsed.ssd, iothread: parsed.iothread, newSize: "" };
              return (
                <div key={key} className="rounded border p-2">
                  <div className="mb-1.5 flex items-center gap-2">
                    <span className="font-mono text-xs font-medium">{key}</span>
                    <span className="text-[10px] text-muted-foreground">{parsed.storage} &middot; {parsed.size || "--"} &middot; {parsed.format || "raw"}</span>
                    <Button variant="ghost" size="sm" className="ml-auto h-6 gap-1 px-2 text-[10px] text-destructive hover:text-destructive" onClick={() => { handleRemoveDisk(key); }}>
                      <Trash2 className="h-3 w-3" /> Remove
                    </Button>
                  </div>
                  <div className="grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-6">
                    <div className="space-y-0.5">
                      <Label className="text-[10px]">Resize To</Label>
                      <Input type="text" value={edit.newSize} onChange={(e) => { updateDiskEdit(key, { newSize: e.target.value }); }} placeholder={parsed.size || "e.g. 64G"} className="h-7 text-xs" />
                    </div>
                    <div className="space-y-0.5">
                      <Label className="text-[10px]">Cache</Label>
                      <select className={compactSelect + " !h-7"} value={edit.cache} onChange={(e) => { updateDiskEdit(key, { cache: e.target.value }); }}>
                        <option value="">Default</option>
                        {cacheModes.map((c) => (<option key={c.value} value={c.value}>{c.label}</option>))}
                      </select>
                    </div>
                    <div className="flex items-end gap-1.5 pb-0.5">
                      <Checkbox id={`hw-disk-${key}-discard`} checked={edit.discard} onCheckedChange={(v) => { updateDiskEdit(key, { discard: v === true }); }} />
                      <Label htmlFor={`hw-disk-${key}-discard`} className="cursor-pointer text-[10px]">Discard</Label>
                    </div>
                    <div className="flex items-end gap-1.5 pb-0.5">
                      <Checkbox id={`hw-disk-${key}-ssd`} checked={edit.ssd} onCheckedChange={(v) => { updateDiskEdit(key, { ssd: v === true }); }} />
                      <Label htmlFor={`hw-disk-${key}-ssd`} className="cursor-pointer text-[10px]">SSD</Label>
                    </div>
                    <div className="flex items-end gap-1.5 pb-0.5">
                      <Checkbox id={`hw-disk-${key}-iothread`} checked={edit.iothread} onCheckedChange={(v) => { updateDiskEdit(key, { iothread: v === true }); }} />
                      <Label htmlFor={`hw-disk-${key}-iothread`} className="cursor-pointer text-[10px]">IO Thread</Label>
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        )}
        {diskEntries.length === 0 && pendingNewDisks.length === 0 && (
          <p className="text-xs text-muted-foreground">No disks configured.</p>
        )}
        {pendingNewDisks.length > 0 && (
          <div className={diskEntries.length > 0 ? "mt-2 space-y-1.5" : "space-y-1.5"}>
            <p className="text-[10px] font-medium text-muted-foreground">New disks (created on save)</p>
            {pendingNewDisks.map((d) => (
              <div key={d.key} className="flex items-center gap-2 rounded border border-green-300 bg-green-50 px-2 py-1 dark:border-green-800 dark:bg-green-950">
                <span className="font-mono text-xs font-medium text-green-700 dark:text-green-400">{d.key}</span>
                <span className="text-[10px] text-green-600 dark:text-green-400">
                  {d.storage}:{d.size}G &middot; {d.format}
                  {d.cache ? ` &middot; ${d.cache}` : ""}
                  {d.discard ? " &middot; discard" : ""}
                  {d.ssd ? " &middot; ssd" : ""}
                  {d.iothread ? " &middot; iothread" : ""}
                </span>
                <Button variant="ghost" size="sm" className="ml-auto h-6 gap-1 px-2 text-[10px] text-destructive hover:text-destructive" onClick={() => { handleRemovePendingDisk(d.key); }}>
                  <Trash2 className="h-3 w-3" /> Cancel
                </Button>
              </div>
            ))}
          </div>
        )}
        {showAddDisk ? (
          <div className="mt-2 rounded border border-dashed p-2">
            <p className="mb-2 text-xs font-medium">Add New Disk</p>
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-6">
              <div className="space-y-0.5">
                <Label className="text-[10px]">Bus</Label>
                <select className={compactSelect + " !h-7"} value={addDiskBus} onChange={(e) => { setAddDiskBus(e.target.value); }}>
                  {diskBusTypes.map((b) => (<option key={b.value} value={b.value}>{b.label}</option>))}
                </select>
              </div>
              <div className="space-y-0.5">
                <Label className="text-[10px]">Storage</Label>
                <select className={compactSelect + " !h-7"} value={addDiskStorage} onChange={(e) => { setAddDiskStorage(e.target.value); }}>
                  <option value="">Select...</option>
                  {diskStorages.map((s) => (<option key={s.id} value={s.storage}>{s.storage} ({s.type})</option>))}
                </select>
              </div>
              <div className="space-y-0.5">
                <Label className="text-[10px]">Size (GB)</Label>
                <Input type="number" min={1} className="h-7 text-xs" value={addDiskSize} onChange={(e) => { setAddDiskSize(e.target.value); }} />
              </div>
              <div className="space-y-0.5">
                <Label className="text-[10px]">Format</Label>
                <select className={compactSelect + " !h-7"} value={addDiskFormat} onChange={(e) => { setAddDiskFormat(e.target.value); }}>
                  {diskFormats.map((f) => (<option key={f.value} value={f.value}>{f.label}</option>))}
                </select>
              </div>
              <div className="space-y-0.5">
                <Label className="text-[10px]">Cache</Label>
                <select className={compactSelect + " !h-7"} value={addDiskCache} onChange={(e) => { setAddDiskCache(e.target.value); }}>
                  <option value="">Default</option>
                  {cacheModes.map((c) => (<option key={c.value} value={c.value}>{c.label}</option>))}
                </select>
              </div>
            </div>
            <div className="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1">
              <div className="flex items-center gap-1.5">
                <Checkbox id="hw-add-disk-discard" checked={addDiskDiscard} onCheckedChange={(v) => { setAddDiskDiscard(v === true); }} />
                <Label htmlFor="hw-add-disk-discard" className="cursor-pointer text-[10px]">Discard</Label>
              </div>
              <div className="flex items-center gap-1.5">
                <Checkbox id="hw-add-disk-ssd" checked={addDiskSsd} onCheckedChange={(v) => { setAddDiskSsd(v === true); }} />
                <Label htmlFor="hw-add-disk-ssd" className="cursor-pointer text-[10px]">SSD</Label>
              </div>
              <div className="flex items-center gap-1.5">
                <Checkbox id="hw-add-disk-iothread" checked={addDiskIothread} onCheckedChange={(v) => { setAddDiskIothread(v === true); }} />
                <Label htmlFor="hw-add-disk-iothread" className="cursor-pointer text-[10px]">IO Thread</Label>
              </div>
              <div className="ml-auto flex gap-1.5">
                <Button size="sm" className="h-6 gap-1 px-2 text-[10px]" onClick={handleAddDisk} disabled={!addDiskStorage || !addDiskSize}>
                  <Plus className="h-3 w-3" /> Add
                </Button>
                <Button variant="ghost" size="sm" className="h-6 px-2 text-[10px]" onClick={() => { setShowAddDisk(false); }}>Cancel</Button>
              </div>
            </div>
          </div>
        ) : (
          <div className="mt-2">
            <Button variant="outline" size="sm" className="h-7 gap-1 text-xs" onClick={() => { setShowAddDisk(true); }}>
              <Plus className="h-3 w-3" /> Add Disk
            </Button>
          </div>
        )}
        {resizeMutation.isError && (
          <p className="mt-1 text-xs text-destructive">Resize failed: {resizeMutation.error.message}</p>
        )}
        {resizeMutation.isSuccess && (
          <p className="mt-1 text-xs text-green-600 dark:text-green-500">Disk resized successfully.</p>
        )}
      </Section>

      {/* Feedback + Save */}
      {setConfigMutation.isError && (
        <p className="text-xs text-destructive">{setConfigMutation.error.message}</p>
      )}
      {setConfigMutation.isSuccess && (
        <p className="text-xs text-green-600 dark:text-green-500">
          Saved.{isRunning ? " Some changes need a restart." : ""}
        </p>
      )}

      <Button size="sm" className="gap-1.5" onClick={handleSave} disabled={!hasChanges || setConfigMutation.isPending}>
        <Save className="h-3.5 w-3.5" />
        {setConfigMutation.isPending ? "Saving..." : "Save Changes"}
      </Button>
    </div>
  );
}
