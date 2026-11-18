import { useCallback, useEffect, useRef, useState, useMemo } from "react";
import { useTranslation, Trans } from "react-i18next";
import {
  AlertTriangle,
  ChevronDown,
  Plus,
  Trash2,
  ArrowUp,
  ArrowDown,
  HardDrive,
  Network,
  Disc,
  Usb,
  Cpu,
  Terminal,
  Dice1,
  FolderOpen,
  Key,
  Shield,
  Monitor,
  type LucideIcon,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { formatBytes } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { Panel } from "@/components/page/Panel";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import {
  useVMConfig,
  useSetVMConfig,
  useResizeDisk,
  useDetachDisk,
  useNodeUSBDevices,
  useNodePCIDevices,
  type NodePCIDevice,
  type NodeUSBDevice,
} from "../api/vm-queries";
import {
  useClusterStorage,
  useStorageContent,
} from "@/features/storage/api/storage-queries";
import { useNodeBridges } from "@/features/clusters/api/cluster-queries";
import { MoveDiskDialog } from "@/features/storage/components/DiskActions";
import { AddDeviceMenu } from "./hardware/AddDeviceMenu";
import {
  Check,
  Checks,
  DeviceList,
  DeviceRow,
  DisclosureRow,
  Field,
  FieldGrid,
  Group,
  GroupList,
  IconAction,
  PendingBadge,
  SaveBar,
} from "./hardware/HardwareRows";
import {
  clearSaveBarClass,
  fieldInputClass,
  fieldSelectClass,
} from "./hardware/field-classes";
import {
  cpuTypes as fallbackCpuTypes,
  cpuFlags as fallbackCpuFlags,
  watchdogModels,
  watchdogActions,
  keyboardLayouts,
  scsiControllers,
  netModels,
  vgaTypes,
  biosOptions,
  machineTypes as fallbackMachineTypes,
  cacheModes,
  diskFormatFor,
  diskFormatToSend,
  diskFormats,
  diskFormatsForStorageType,
  osTypes,
  audioDevices,
} from "../lib/vm-config-constants";
import {
  useCPUModels,
  useCPUFlags,
  useMachineTypes,
} from "@/features/clusters/api/cluster-queries";
import {
  parseNet,
  buildNet,
  parseAgent,
  buildAgent,
  parseVGA,
  buildVGA,
  parseDisk,
  parseDriveMedia,
  buildCDROM,
  isCloudInitVolume,
  parseAudio,
  buildAudio,
  parseStartup,
  buildStartup,
  bootDevices,
  buildBootOrder,
  parseMachine,
  buildMachine,
  dropsIntelViommu,
  parseUSB,
  isRawUSBPassthrough,
  parsePCI,
  isRootOnlyPCI,
  parseRNG,
  parseVirtioFS,
  parseEFIDisk,
  parseTPMState,
  parseCPU,
  buildCPU,
  parseWatchdog,
  buildWatchdog,
  parseSMBIOS,
  buildSMBIOS,
  SMBIOS_TEXT_FIELDS,
} from "../lib/vm-config-parsers";
import { cleanDeviceText } from "@/features/mappings/lib/usb-mapping";
import { pciDeviceLabel } from "@/features/mappings/lib/pci-mapping";
import type {
  CPUFlagState,
  ParsedNet,
  SMBIOSTextField,
} from "../lib/vm-config-parsers";
import type { VMConfig } from "../types/vm";
import { describeError } from "@/lib/api-error";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import i18n from "@/lib/i18n";

const DISK_KEY_RE = /^(scsi|ide|sata|virtio|efidisk|tpmstate|unused)\d+$/;

/**
 * The drives a boot order may name: verify_bootdev (src/PVE/QemuServer.pm)
 * refuses an EFI disk or a TPM state there. It takes usbN and hostpciN too,
 * which the boot list does not offer.
 */
const BOOT_DISK_KEY_RE = /^(scsi|ide|sata|virtio)\d+$/;

/**
 * Shown when a VM config carries no `cpu:` line. Proxmox's real default there
 * is kvm64 (get_default_cpu_type), so this is only a UI placeholder — never
 * write it back for a VM that never had it. normalizeCPU applies it to both
 * sides of every comparison so an untouched VM is not dirty and no save
 * silently upgrades the guest-visible CPU model.
 */
const DEFAULT_CPU_TYPE = "x86-64-v2-AES";

/**
 * The memory a config without a memory line runs with: $memory_fmt's
 * `current` default of 512 MiB, which parse_memory returns for an undefined
 * value (src/PVE/QemuServer/Memory.pm).
 */
const DEFAULT_MEMORY = 512;

/**
 * The machine a config naming no machine type runs as: qemu-server's default
 * for x86_64 (default_machine_for_arch, src/PVE/QemuServer/Machine.pm).
 */
const DEFAULT_MACHINE = "pc";

/**
 * The machine type a stored machine value runs: the one it names, or the
 * default.
 */
function machineTypeOf(stored: string): string {
  return parseMachine(stored).type || DEFAULT_MACHINE;
}

/**
 * What a config setting no SCSI controller or OS type runs as: the defaults
 * of qemu-server's scsihw and ostype options ($confdesc,
 * src/PVE/QemuServer.pm).
 */
const DEFAULT_SCSIHW = "lsi";
const DEFAULT_OSTYPE = "other";

/**
 * The machine value Save sends for the type picked: the stored value itself
 * while the type picked is the one it runs, the default for a value naming
 * none included, or else that value with only its type replaced.
 */
function machineFor(type: string, stored: string): string {
  return type === machineTypeOf(stored) ? stored : buildMachine(type, stored);
}

/** All smbios1 text fields blank — the shape parseSMBIOS/buildSMBIOS expect. */
function emptySmbios(): Record<SMBIOSTextField, string> {
  return {
    manufacturer: "",
    product: "",
    version: "",
    serial: "",
    sku: "",
    family: "",
  };
}

/** Canonical form of a raw `cpu` value, with the placeholder model applied. */
function normalizeCPU(raw: string): string {
  const parsed = parseCPU(raw);
  return buildCPU({ ...parsed, model: parsed.model || DEFAULT_CPU_TYPE });
}

const diskBusTypes = [
  {
    value: "scsi",
    get label() {
      return i18n.t("vms:hardwarePanel.scsi");
    },
    max: 30,
  },
  {
    value: "virtio",
    get label() {
      return i18n.t("vms:hardwarePanel.virtIOBlock");
    },
    max: 15,
  },
  {
    value: "sata",
    get label() {
      return i18n.t("vms:hardwarePanel.sata");
    },
    max: 5,
  },
  {
    value: "ide",
    get label() {
      return i18n.t("vms:hardwarePanel.ide");
    },
    max: 3,
  },
] as const;

interface HardwarePanelProps {
  clusterId: string;
  vmId: string;
  vmStatus: string;
  nodeName: string;
}

/**
 * A raw usbN host as the node knows it: "Product (1234:5678)" for a
 * vendor:product id, "Product (1-2)" for a port, or the host alone when the
 * node lists nothing there. qemu-server takes the id with or without 0x
 * ($USB_ID_RE, src/PVE/QemuServer/USB.pm); the node lists it without.
 */
function usbHostLabel(
  host: string,
  devices: NodeUSBDevice[] | undefined,
): string {
  const id = host.toLowerCase().replace(/0x/g, "");
  const match = devices?.find(
    (d) =>
      `${d.vendid}:${d.prodid}` === id ||
      `${String(d.busnum)}-${d.usbpath}` === host,
  );
  // The device names itself, so its text is cleaned before it is shown.
  const name =
    cleanDeviceText(match?.product ?? "") ||
    cleanDeviceText(match?.manufacturer ?? "");
  return name ? `${name} (${host})` : host;
}

/**
 * A raw hostpciN host as the node knows it: "Device (0000:01:00.0)", or the
 * host alone when the node lists nothing there. qemu-server takes the domain
 * as optional ("01:00.0" is 0000:01:00.0) and the function too — the whole
 * device, named here after function 0.
 */
function pciHostLabel(
  host: string,
  devices: NodePCIDevice[] | undefined,
): string {
  const full = host.split(":").length === 2 ? `0000:${host}` : host;
  const record = /\.[0-9a-f]$/i.test(full) ? full : `${full}.0`;
  const match = devices?.find((d) => d.id === record.toLowerCase());
  return match ? `${pciDeviceLabel(match)} (${host})` : host;
}

function str(val: unknown): string {
  if (val == null) return "";
  if (typeof val === "string") return val;
  if (typeof val === "number" || typeof val === "boolean") return String(val);
  return JSON.stringify(val);
}

function num(val: unknown): number {
  if (val == null) return 0;
  const n = Number(val);
  return Number.isNaN(n) ? 0 : n;
}

function bool01(val: unknown): boolean {
  return Number(val) === 1;
}

/**
 * One NIC's state: every field parseNet reads, queues= included though it
 * has no control here. Save rebuilds each NIC on its stored value (buildNet):
 * an untouched NIC is not sent, and an edited one keeps every option this
 * panel does not edit exactly as stored.
 */
type NICEdit = ParsedNet;

/**
 * A <select> whose configured value may not be in the option list — an
 * inactive storage, a keyboard layout Proxmox added later. Without this, a
 * controlled select whose value matches no option shows its FIRST option
 * (react-dom's updateOptions selects the first one not disabled), often the
 * "" one ("Automatic"/"None"), while state still holds the real value; one
 * click then silently reassigns it. The option is built from `value`: pass
 * the STORED value and it stays in the list after another pick, so it can be
 * picked back.
 */
function CurrentValueOption({
  value,
  known,
}: {
  value: string;
  known: string[];
}) {
  const { t } = useTranslation("vms");
  if (!value || known.includes(value)) return null;
  return (
    <option value={value}>{t("hardwarePanel.valueCurrent", { value })}</option>
  );
}

/** The hotplug option's values, as the tab names them. */
const HOTPLUG_LABELS = {
  get network() {
    return i18n.t("vms:hardwarePanel.hotplugNetwork");
  },
  get disk() {
    return i18n.t("vms:hardwarePanel.hotplugDisk");
  },
  get usb() {
    return i18n.t("vms:hardwarePanel.hotplugUsb");
  },
  get memory() {
    return i18n.t("vms:hardwarePanel.hotplugMemory");
  },
  get cpu() {
    return i18n.t("vms:hardwarePanel.hotplugCpu");
  },
} as const;

const SMBIOS_LABELS: Record<SMBIOSTextField, string> = {
  get manufacturer() {
    return i18n.t("vms:hardwarePanel.smbiosManufacturer");
  },
  get product() {
    return i18n.t("vms:hardwarePanel.smbiosProduct");
  },
  get version() {
    return i18n.t("vms:hardwarePanel.smbiosVersion");
  },
  get serial() {
    return i18n.t("vms:hardwarePanel.smbiosSerial");
  },
  get sku() {
    return i18n.t("vms:hardwarePanel.smbiosSku");
  },
  get family() {
    return i18n.t("vms:hardwarePanel.smbiosFamily");
  },
};

/** Returns a human-readable label for a bootable device key. */
function bootDeviceLabel(
  key: string,
  config: VMConfig,
): { label: string; type: "disk" | "cdrom" | "net" | "other" } {
  if (/^net\d+$/.test(key)) {
    const raw = str(config[key]);
    const bridge = raw.match(/bridge=([^,]+)/)?.[1] ?? "";
    return {
      label: bridge
        ? i18n.t("vms:hardwarePanel.bootNetworkBridge", { key, bridge })
        : i18n.t("vms:hardwarePanel.bootNetwork", { key }),
      type: "net",
    };
  }
  const val = str(config[key]);
  const drive = parseDriveMedia(val);
  if (drive.cdrom) {
    const iso = drive.file;
    return {
      label:
        iso && iso !== "none"
          ? i18n.t("vms:hardwarePanel.bootCdromIso", { key, iso })
          : i18n.t("vms:hardwarePanel.bootCdromEmpty", { key }),
      type: "cdrom",
    };
  }
  if (DISK_KEY_RE.test(key)) {
    const storage = val.split(":")[0] ?? "";
    const size = val.match(/size=([^,]+)/)?.[1] ?? "";
    return {
      label: storage
        ? size
          ? i18n.t("vms:hardwarePanel.bootDiskStorageSize", {
              key,
              storage,
              size,
            })
          : i18n.t("vms:hardwarePanel.bootDiskStorage", { key, storage })
        : size
          ? i18n.t("vms:hardwarePanel.bootDiskSize", { key, size })
          : i18n.t("vms:hardwarePanel.bootDisk", { key }),
      type: "disk",
    };
  }
  return { label: key, type: "other" };
}

/** Per-disk editable options tracked in local state. */
interface DiskEdit {
  cache: string;
  discard: boolean;
  ssd: boolean;
  iothread: boolean;
  newSize: string;
}

export function HardwarePanel({
  clusterId,
  vmId,
  vmStatus,
  nodeName,
}: HardwarePanelProps) {
  const { t } = useTranslation("vms");
  const { data: config, isLoading } = useVMConfig(clusterId, vmId);
  const setConfigMutation = useSetVMConfig();
  const resizeMutation = useResizeDisk();
  // The panel shows a failed save or resize in its save bar, and a delete's in
  // its dialog; one that lands after either has gone is toasted instead. One
  // Save can resize several disks over one mutation, so their failures are
  // kept here rather than read from its state, which holds only the last.
  const settle = useSaveOutcome();
  const [resizeErrors, setResizeErrors] = useState<string[]>([]);

  // Removing an unusedN key DESTROYS the volume it holds, if this VM owns it:
  // Proxmox frees it on the spot, where removing a live drive only parks its
  // volume as unusedN. So it is not staged for Save with the reversible edits.
  // It is confirmed, then sent at once through the detach route, whose audit
  // row names the volume the key held.
  const detachDisk = useDetachDisk();
  const [unusedToDelete, setUnusedToDelete] = useState<{
    key: string;
    volume: string;
  } | null>(null);
  const settleDelete = useSaveOutcome(unusedToDelete?.key ?? null);

  // Removing a cloud-init drive frees its volume, where removing an ISO drive
  // only empties a slot (vmconfig_register_unused_drive,
  // src/PVE/QemuServer.pm). So it is staged like the other removals, but only
  // once this confirmation says so.
  const [cloudInitToRemove, setCloudInitToRemove] = useState<{
    key: string;
    volume: string;
  } | null>(null);
  // Where focus goes once that confirmation stages the removal: the Remove
  // button that opened it is gone, replaced by the row's Undo.
  const cdromList = useRef<HTMLDivElement>(null);

  // Which device rows have their editor open, and whether the rarely used
  // options are showing. Display only: closing a row keeps what was typed.
  const [openRows, setOpenRows] = useState<Set<string>>(new Set());
  const [advancedOpen, setAdvancedOpen] = useState(false);

  // USB/PCI device listing from node
  const { data: usbDevices } = useNodeUSBDevices(clusterId, nodeName);
  const { data: pciDevices } = useNodePCIDevices(clusterId, nodeName);
  const { data: bridges } = useNodeBridges(clusterId, nodeName);
  const bridgeNames = useMemo(
    () => bridges?.map((b) => b.iface) ?? [],
    [bridges],
  );

  // Dynamic CPU models from Proxmox
  const { data: rawCpuModels } = useCPUModels(clusterId, nodeName);
  const cpuTypes = useMemo(() => {
    if (!rawCpuModels || rawCpuModels.length === 0)
      return fallbackCpuTypes.map((t) => t);
    return [...rawCpuModels]
      .sort((a, b) => {
        if (a.custom !== b.custom) return a.custom ? -1 : 1;
        return a.name.localeCompare(b.name);
      })
      .map((m) => m.name);
  }, [rawCpuModels]);

  // Dynamic CPU flags from Proxmox. Falls back to the built-in list when the
  // cluster predates the cpu-flags endpoint (handler returns an empty list).
  const { data: rawCpuFlags } = useCPUFlags(clusterId, nodeName);
  const cpuFlagList = useMemo(() => {
    if (!rawCpuFlags || rawCpuFlags.length === 0)
      // Fallback list: nothing is known about per-node support, which is not
      // the same as knowing no node supports the flag — hence null.
      return fallbackCpuFlags.map((f) => ({
        name: f.name,
        description: f.description,
        supportedOn: null as string[] | null,
      }));
    // Deliberately not re-sorted. Proxmox hoists nested-virt to the front and
    // pushes flags no node supports to the back (query_available_cpu_flags);
    // sorting by name here would throw both signals away.
    return rawCpuFlags.map((f) => ({
      name: f.name,
      description: f.description ?? "",
      supportedOn: f.supported_on,
    }));
  }, [rawCpuFlags]);

  // Dynamic machine types from Proxmox
  const { data: rawMachineTypes } = useMachineTypes(clusterId, nodeName);
  const machineTypes = useMemo(() => {
    if (!rawMachineTypes || rawMachineTypes.length === 0) {
      return [...fallbackMachineTypes];
    }
    const pveTypes = rawMachineTypes
      .filter((mt) => mt.id.includes("+pve"))
      .sort((a, b) => b.id.localeCompare(a.id));
    const result: { value: string; label: string }[] = [];
    const q35pve = pveTypes.find((mt) => mt.id.includes("q35"));
    const i440pve = pveTypes.find((mt) => mt.id.includes("i440fx"));
    if (q35pve)
      result.push({
        value: q35pve.id,
        label: t("hardwarePanel.idRecommended", { id: q35pve.id }),
      });
    if (i440pve) result.push({ value: i440pve.id, label: i440pve.id });
    for (const mt of pveTypes) {
      if (mt.id !== q35pve?.id && mt.id !== i440pve?.id) {
        result.push({ value: mt.id, label: mt.id });
      }
    }
    result.push({ value: "q35", label: t("hardwarePanel.q35Generic") });
    result.push({ value: "pc", label: t("hardwarePanel.i440fxGeneric") });
    return result;
  }, [rawMachineTypes, t]);

  // --- Original config snapshot for diff comparison ---
  const [original, setOriginal] = useState<VMConfig | null>(null);

  // --- Generic device state for new device types ---
  const [pendingDeviceAdds, setPendingDeviceAdds] = useState<
    Record<string, string>
  >({});
  const [deviceRemovals, setDeviceRemovals] = useState<Set<string>>(new Set());

  // --- Multi-NIC state ---
  const [nicEdits, setNicEdits] = useState<Record<string, NICEdit>>({});

  // --- CPU ---
  const [cores, setCores] = useState("1");
  const [sockets, setSockets] = useState("1");
  const [cpuType, setCpuType] = useState(DEFAULT_CPU_TYPE);
  // Only explicitly-set flags live here; anything absent is Proxmox's default.
  const [cpuFlagStates, setCpuFlagStates] = useState<
    Record<string, Exclude<CPUFlagState, "default">>
  >({});
  // cpu= options with no UI (hidden, phys-bits, hv-vendor-id, …), carried
  // through untouched so editing the model or a flag cannot drop them.
  const [cpuExtra, setCpuExtra] = useState<Map<string, string>>(new Map());

  const setCpuFlagCount = Object.keys(cpuFlagStates).length;

  // A flag the config sets but Proxmox no longer lists still needs a control,
  // or saving any other CPU change would silently strip it. Derived from the
  // loaded config rather than current state, so setting such a flag back to
  // Default does not make its own row disappear mid-edit.
  const cpuFlagRows = useMemo(() => {
    const known = new Set(cpuFlagList.map((f) => f.name));
    const extras = Object.keys(parseCPU(str(original?.["cpu"] ?? "")).flags)
      .filter((n) => !known.has(n))
      .sort()
      .map((name) => ({
        name,
        description: "",
        supportedOn: null as string[] | null,
      }));
    return [...cpuFlagList, ...extras];
  }, [cpuFlagList, original]);
  const [numa, setNuma] = useState(false);
  const [cpulimit, setCpulimit] = useState("");
  const [vcpus, setVcpus] = useState("");
  const [cpuunits, setCpuunits] = useState("");

  // --- Memory ---
  const [memory, setMemory] = useState(String(DEFAULT_MEMORY));
  // memory is a $memory_fmt property string whose `current` is required, so
  // qemu-server refuses "" (and the whole Save with it); deleted, it reads as
  // the format's default of 512 MiB (Memory.pm parse_memory). Neither is what
  // a cleared field means, so a Save waits until it has a value.
  const memoryMissing = memory === "";
  const [balloon, setBalloon] = useState("");
  const [shares, setShares] = useState("");
  const [allowKsm, setAllowKsm] = useState(true);

  // --- System ---
  const [bios, setBios] = useState("seabios");
  const [machine, setMachine] = useState(DEFAULT_MACHINE);
  const [scsihw, setScsihw] = useState(DEFAULT_SCSIHW);
  const [agentEnabled, setAgentEnabled] = useState(false);
  const [agentFstrim, setAgentFstrim] = useState(false);
  const [kvmEnabled, setKvmEnabled] = useState(true);
  const [acpi, setAcpi] = useState(true);

  // --- Boot / Options ---
  const [onboot, setOnboot] = useState(false);
  const [tablet, setTablet] = useState(true);
  const [hotplug, setHotplug] = useState("");
  const [bootOrder, setBootOrder] = useState<
    Array<{ device: string; enabled: boolean }>
  >([]);
  const [ostype, setOstype] = useState(DEFAULT_OSTYPE);
  const [protection, setProtection] = useState(false);
  const [localtime, setLocaltime] = useState(false);
  const [startupOrder, setStartupOrder] = useState("");
  const [startupUp, setStartupUp] = useState("");
  const [startupDown, setStartupDown] = useState("");
  const [freeze, setFreeze] = useState(false);
  const [reboot, setReboot] = useState(true);
  const [tdf, setTdf] = useState(false);
  const [startdate, setStartdate] = useState("");
  const [keyboard, setKeyboard] = useState("");
  const [vmstatestorage, setVmstatestorage] = useState("");
  const [migrateDowntime, setMigrateDowntime] = useState("");
  const [migrateSpeed, setMigrateSpeed] = useState("");
  const [watchdogModel, setWatchdogModel] = useState("");
  const [watchdogAction, setWatchdogAction] = useState("");
  const [smbiosUuid, setSmbiosUuid] = useState("");
  const [smbios, setSmbios] =
    useState<Record<SMBIOSTextField, string>>(emptySmbios);

  // --- Network is handled by nicEdits state ---

  // --- Display ---
  // "" is no type set: no vga line, or one with only other options
  // (memory=32, clipboard=vnc). Proxmox picks the type; shown as "Default".
  const [vgaType, setVgaType] = useState("");
  const [vgaMemory, setVgaMemory] = useState("");
  const [audioDevice, setAudioDevice] = useState("");

  // The values as STORED behind every select's "(current)" option. Taken
  // from the loaded config, not the edit state, as cpuFlagRows is: picking
  // another value then leaves the stored one in the list, so it can be
  // picked back. Each is read the way the populate effect reads it.
  const storedNics = useMemo(() => {
    const nics: Record<string, ParsedNet> = {};
    for (const [key, val] of Object.entries(original ?? {})) {
      if (/^net\d+$/.test(key)) nics[key] = parseNet(str(val));
    }
    return nics;
  }, [original]);
  const storedVgaType = useMemo(
    () => parseVGA(str(original?.["vga"] ?? "")).type,
    [original],
  );
  const storedSelects = useMemo(() => {
    const wd = parseWatchdog(str(original?.["watchdog"] ?? ""));
    return {
      ostype: str(original?.["ostype"] ?? DEFAULT_OSTYPE),
      cpuType: parseCPU(str(original?.["cpu"] ?? "")).model || DEFAULT_CPU_TYPE,
      bios: str(original?.["bios"] ?? "seabios"),
      audio: parseAudio(str(original?.["audio0"] ?? "")).device,
      machine: machineTypeOf(str(original?.["machine"] ?? "")),
      scsihw: str(original?.["scsihw"] ?? DEFAULT_SCSIHW),
      keyboard: str(original?.["keyboard"] ?? ""),
      vmstatestorage: str(original?.["vmstatestorage"] ?? ""),
      watchdogModel: wd.model,
      watchdogAction: wd.action,
    };
  }, [original]);

  // --- CD/DVD (all devices with media=cdrom) ---
  const [cdromEdits, setCdromEdits] = useState<Record<string, string>>({});
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
  const [pendingNewDisks, setPendingNewDisks] = useState<
    Array<{
      key: string;
      storage: string;
      size: string;
      format: string;
      cache: string;
      discard: boolean;
      ssd: boolean;
      iothread: boolean;
    }>
  >([]);

  const { data: storages } = useClusterStorage(clusterId);

  // Filter storages that support disk images, deduplicated by storage name
  const diskStorages = useMemo(() => {
    if (!storages) return [];
    const seen = new Set<string>();
    return storages.filter((s) => {
      if (!s.content.includes("images") || !s.enabled || !s.active)
        return false;
      if (seen.has(s.storage)) return false;
      seen.add(s.storage);
      return true;
    });
  }, [storages]);
  // The new disk's store type decides the formats it offers and sends: qcow2
  // sent to thin LVM, RBD or ZFS was refused ("unsupported format 'qcow2'").
  const addDiskType = diskStorages.find(
    (s) => s.storage === addDiskStorage,
  )?.type;
  const addDiskFormats = diskFormatsForStorageType(addDiskType);

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

  // Auto-select ISO storage: prefer the one matching a mounted ISO, else first available
  useEffect(() => {
    if (isoStorages.length === 0) return;
    if (cdromStorageId) return;
    // If an ISO is mounted (e.g. "local:iso/ubuntu.iso"), match by storage name prefix
    const mountedIso = Object.values(cdromEdits).find(
      (v) => v !== "none" && v.includes(":"),
    );
    if (mountedIso) {
      const storageName = mountedIso.split(":")[0];
      const match = isoStorages.find((s) => s.storage === storageName);
      if (match) {
        setCdromStorageId(match.id);
        return;
      }
    }
    setCdromStorageId(isoStorages[0]?.id ?? "");
  }, [isoStorages, cdromStorageId, cdromEdits]);

  function updateDiskEdit(key: string, partial: Partial<DiskEdit>) {
    setDiskEdits((prev) => ({
      ...prev,
      [key]: {
        ...(prev[key] ?? {
          cache: "",
          discard: false,
          ssd: false,
          iothread: false,
          newSize: "",
        }),
        ...partial,
      },
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
      if (!usedIndices.has(i) && !disksToRemove.has(`${bus}${String(i)}`))
        return i;
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
        format: diskFormatToSend(addDiskType, addDiskFormat) ?? "",
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

  function openUnusedDelete(key: string, volume: string) {
    // A failure left over from the last attempt belongs to that one. Cleared
    // here, on the way in, so it holds however the dialog was last closed.
    detachDisk.reset();
    setUnusedToDelete({ key, volume });
  }

  function closeUnusedDelete() {
    setUnusedToDelete(null);
  }

  // The staged changes: disks and devices marked for removal, and new ones
  // waiting for Save. The populate effect below re-reads every field when the
  // config changes, and clears these with them: left in place across a disk
  // move, which changes the config when it finishes, or a change made
  // elsewhere, a later Save would apply them without the edits that went with
  // them. A Save and an unused-disk delete clear them too, as soon as they
  // succeed, through this one list, so the two cannot drift apart.
  const clearStagedChanges = useCallback(() => {
    setDisksToRemove(new Set());
    setPendingNewDisks([]);
    setPendingDeviceAdds({});
    setDeviceRemovals(new Set());
  }, []);

  // Populate fields from config, and drop what was staged on the last one
  useEffect(() => {
    if (!config) return;
    setOriginal(config);
    clearStagedChanges();

    // CPU
    setCores(str(config["cores"] ?? 1));
    setSockets(str(config["sockets"] ?? 1));
    const parsedCPU = parseCPU(str(config["cpu"] ?? ""));
    setCpuType(parsedCPU.model || DEFAULT_CPU_TYPE);
    setCpuFlagStates(parsedCPU.flags);
    setCpuExtra(parsedCPU.extra);
    setNuma(bool01(config["numa"]));
    setCpulimit(config["cpulimit"] != null ? str(config["cpulimit"]) : "");
    setVcpus(config["vcpus"] != null ? str(config["vcpus"]) : "");
    setCpuunits(config["cpuunits"] != null ? str(config["cpuunits"]) : "");

    // Memory
    setMemory(str(config["memory"] ?? DEFAULT_MEMORY));
    setBalloon(config["balloon"] != null ? str(config["balloon"]) : "");
    setShares(config["shares"] != null ? str(config["shares"]) : "");
    setAllowKsm(
      config["allow-ksm"] != null ? bool01(config["allow-ksm"]) : true,
    );

    // System
    setBios(str(config["bios"] ?? "seabios"));
    // The type alone: viommu= and the rest are kept from the stored value.
    setMachine(machineTypeOf(str(config["machine"] ?? "")));
    setScsihw(str(config["scsihw"] ?? DEFAULT_SCSIHW));
    const agent = parseAgent(str(config["agent"] ?? ""));
    setAgentEnabled(agent.enabled);
    setAgentFstrim(agent.fstrimClonedDisks);
    setKvmEnabled(config["kvm"] != null ? bool01(config["kvm"]) : true);
    setAcpi(config["acpi"] != null ? bool01(config["acpi"]) : true);

    // Initialize disk edits from config
    const edits: Record<string, DiskEdit> = {};
    for (const [key, val] of Object.entries(config)) {
      if (
        DISK_KEY_RE.test(key) &&
        !key.startsWith("efidisk") &&
        !key.startsWith("tpmstate") &&
        !key.startsWith("unused")
      ) {
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
    setOstype(str(config["ostype"] ?? DEFAULT_OSTYPE));
    setProtection(bool01(config["protection"]));
    setLocaltime(bool01(config["localtime"]));
    // Only devices the VM has: Proxmox refuses an order naming one it does
    // not (see bootToSend), and boots by the rest.
    // A legacy boot value (boot: cdn), or none, is read as Proxmox boots by
    // it, so it shows what boots; it is rewritten only when the order is.
    const booted = bootDevices(config).devices.filter((d) => config[d] != null);
    // Build full boot order: enabled devices first (in order), then all other bootable devices (disabled)
    const enabledSet = new Set(booted);
    const allBootable: string[] = [];
    for (const key of Object.keys(config)) {
      if (BOOT_DISK_KEY_RE.test(key)) allBootable.push(key);
      else if (/^net\d+$/.test(key)) allBootable.push(key);
    }
    allBootable.sort((a, b) => a.localeCompare(b));
    const ordered: Array<{ device: string; enabled: boolean }> = [];
    // First: enabled devices in their configured order
    for (const d of booted) {
      ordered.push({ device: d, enabled: true });
    }
    // Then: remaining bootable devices not in boot order (disabled)
    for (const d of allBootable) {
      if (!enabledSet.has(d)) {
        ordered.push({ device: d, enabled: false });
      }
    }
    setBootOrder(ordered);
    const startup = parseStartup(str(config["startup"] ?? ""));
    setStartupOrder(startup.order);
    setStartupUp(startup.up);
    setStartupDown(startup.down);
    setFreeze(bool01(config["freeze"]));
    setReboot(config["reboot"] != null ? bool01(config["reboot"]) : true);
    setTdf(bool01(config["tdf"]));
    setStartdate(str(config["startdate"] ?? ""));
    setKeyboard(str(config["keyboard"] ?? ""));
    setVmstatestorage(str(config["vmstatestorage"] ?? ""));
    setMigrateDowntime(
      config["migrate_downtime"] != null ? str(config["migrate_downtime"]) : "",
    );
    setMigrateSpeed(
      config["migrate_speed"] != null ? str(config["migrate_speed"]) : "",
    );
    const wd = parseWatchdog(str(config["watchdog"] ?? ""));
    setWatchdogModel(wd.model);
    setWatchdogAction(wd.action);
    const sm = parseSMBIOS(str(config["smbios1"] ?? ""));
    setSmbiosUuid(sm.uuid);
    setSmbios({ ...sm.values });

    // Network (multi-NIC)
    const nics: Record<string, NICEdit> = {};
    for (const [cfgKey, cfgVal] of Object.entries(config)) {
      if (/^net\d+$/.test(cfgKey)) nics[cfgKey] = parseNet(str(cfgVal));
    }
    setNicEdits(nics);

    // Display
    const vga = parseVGA(str(config["vga"] ?? ""));
    setVgaType(vga.type);
    setVgaMemory(vga.memory);
    const audio = parseAudio(str(config["audio0"] ?? ""));
    setAudioDevice(audio.device);

    // CD/DVD — scan all config keys for media=cdrom (ide*, sata*, etc.),
    // each by the volume it holds ("none" for no media).
    const newCdromEdits: Record<string, string> = {};
    for (const [cfgKey, cfgVal] of Object.entries(config)) {
      if (!DISK_KEY_RE.test(cfgKey)) continue;
      const drive = parseDriveMedia(str(cfgVal));
      if (drive.cdrom) newCdromEdits[cfgKey] = drive.file;
    }
    setCdromEdits(newCdromEdits);

    // Meta
    setDescription(str(config["description"] ?? ""));
    setTags(str(config["tags"] ?? ""));
  }, [config, clearStagedChanges]);

  // Discover disk keys from config (exclude any CD-ROM device)
  const diskEntries = useMemo(() => {
    if (!config) return [];
    return Object.entries(config)
      .filter(([key, val]) => {
        if (!DISK_KEY_RE.test(key)) return false;
        // Exclude CD/DVD drives on any bus
        return !parseDriveMedia(str(val)).cdrom;
      })
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([key, val]) => ({ key, parsed: parseDisk(str(val)) }));
  }, [config]);

  // Discover all NIC keys from config
  const nicKeys = useMemo(() => {
    if (!config) return [];
    return Object.keys(config)
      .filter((key) => /^net\d+$/.test(key))
      .sort((a, b) => a.localeCompare(b));
  }, [config]);

  // Discover USB, PCI, Serial, RNG, VirtioFS devices from config
  const usbKeys = useMemo(
    () =>
      !config
        ? []
        : Object.keys(config)
            .filter((k) => /^usb\d+$/.test(k))
            .sort(),
    [config],
  );
  const pciKeys = useMemo(
    () =>
      !config
        ? []
        : Object.keys(config)
            .filter((k) => /^hostpci\d+$/.test(k))
            .sort(),
    [config],
  );
  const serialKeys = useMemo(
    () =>
      !config
        ? []
        : Object.keys(config)
            .filter((k) => /^serial\d+$/.test(k))
            .sort(),
    [config],
  );
  const virtiofsKeys = useMemo(
    () =>
      !config
        ? []
        : Object.keys(config)
            .filter((k) => /^virtiofs\d+$/.test(k))
            .sort(),
    [config],
  );

  const isRunning = vmStatus.toLowerCase() === "running";

  function updateNicEdit(key: string, partial: Partial<NICEdit>) {
    setNicEdits((prev) => ({
      ...prev,
      [key]: { ...(prev[key] ?? parseNet("")), ...partial },
    }));
  }

  function handleAddDevice(key: string, value: string) {
    setPendingDeviceAdds((prev) => ({ ...prev, [key]: value }));
  }

  function handleAddCDROM(key: string, isoVolid: string) {
    setCdromEdits((prev) => ({ ...prev, [key]: isoVolid }));
  }

  // A stored drive stays listed, marked for removal until Save as a disk
  // is; one added since is dropped.
  function handleRemoveCDROM(key: string) {
    if (original?.[key] != null) {
      setDeviceRemovals((prev) => new Set(prev).add(key));
      return;
    }
    setCdromEdits((prev) => {
      const next = { ...prev };
      // eslint-disable-next-line @typescript-eslint/no-dynamic-delete
      delete next[key];
      return next;
    });
  }

  function handleRemoveDevice(key: string) {
    setDeviceRemovals((prev) => new Set(prev).add(key));
  }

  function handleUndoRemoveDevice(key: string) {
    setDeviceRemovals((prev) => {
      const next = new Set(prev);
      next.delete(key);
      return next;
    });
  }

  function handleCancelPendingDevice(key: string) {
    setPendingDeviceAdds((prev) => {
      return Object.fromEntries(
        Object.entries(prev).filter(([k]) => k !== key),
      );
    });
  }

  function handleSave() {
    if (!original || memoryMissing) return;
    setResizeErrors([]);
    const fields: Record<string, string> = {};
    const deleteFields: string[] = [];

    // CPU. Cores and sockets are optional integers (default 1) that
    // qemu-server's schema refuses empty, and the whole write with it, so one
    // cleared is deleted and reads back as 1.
    if (cores !== str(original["cores"] ?? 1)) {
      if (cores) fields["cores"] = cores;
      else deleteFields.push("cores");
    }
    if (sockets !== str(original["sockets"] ?? 1)) {
      if (sockets) fields["sockets"] = sockets;
      else deleteFields.push("sockets");
    }
    const newCPU = buildCPU({
      model: cpuType,
      flags: cpuFlagStates,
      extra: cpuExtra,
    });
    if (newCPU !== normalizeCPU(str(original["cpu"] ?? "")))
      fields["cpu"] = newCPU;
    const origNuma = bool01(original["numa"]);
    if (numa !== origNuma) fields["numa"] = numa ? "1" : "0";
    const origCpulimit =
      original["cpulimit"] != null ? str(original["cpulimit"]) : "";
    if (cpulimit !== origCpulimit) fields["cpulimit"] = cpulimit || "0";
    const origCpuunits =
      original["cpuunits"] != null ? str(original["cpuunits"]) : "";
    if (cpuunits !== origCpuunits) {
      if (cpuunits) {
        fields["cpuunits"] = cpuunits;
      } else {
        deleteFields.push("cpuunits");
      }
    }

    // Memory
    if (memory !== str(original["memory"] ?? DEFAULT_MEMORY))
      fields["memory"] = memory;
    const origBalloon =
      original["balloon"] != null ? str(original["balloon"]) : "";
    if (balloon !== origBalloon) fields["balloon"] = balloon || "0";
    const origShares =
      original["shares"] != null ? str(original["shares"]) : "";
    if (shares !== origShares) {
      if (shares) {
        fields["shares"] = shares;
      } else {
        deleteFields.push("shares");
      }
    }

    // System. The machine and the agent are among the optionalScalars.
    if (bios !== str(original["bios"] ?? "seabios")) fields["bios"] = bios;
    if (scsihw !== str(original["scsihw"] ?? DEFAULT_SCSIHW))
      fields["scsihw"] = scsihw;
    const origKvm = original["kvm"] != null ? bool01(original["kvm"]) : true;
    if (kvmEnabled !== origKvm) fields["kvm"] = kvmEnabled ? "1" : "0";
    const origAcpi = original["acpi"] != null ? bool01(original["acpi"]) : true;
    if (acpi !== origAcpi) fields["acpi"] = acpi ? "1" : "0";

    // Disk config changes (cache, discard, ssd, iothread) via SetVMConfig.
    // A disk marked for removal is only deleted, never also set: Proxmox
    // refuses a key that is both ("you can't use '-scsi0' and -delete scsi0'
    // at the same time", $update_vm_api in src/PVE/API2/Qemu.pm), and the
    // whole Save fails with it.
    for (const [key, edit] of Object.entries(diskEdits)) {
      if (disksToRemove.has(key)) continue;
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
        // Only what was edited is rewritten: a flag left alone keeps its
        // spelling (ssd=on is as true to Proxmox as ssd=1).
        if (edit.cache !== origDisk.cache) {
          if (edit.cache) kv.set("cache", edit.cache);
          else kv.delete("cache");
        }
        if (edit.discard !== origDisk.discard) {
          if (edit.discard) kv.set("discard", "on");
          else kv.delete("discard");
        }
        if (edit.ssd !== origDisk.ssd) {
          if (edit.ssd) kv.set("ssd", "1");
          else kv.delete("ssd");
        }
        if (edit.iothread !== origDisk.iothread) {
          if (edit.iothread) kv.set("iothread", "1");
          else kv.delete("iothread");
        }
        const optParts: string[] = [];
        for (const [k, v] of kv) {
          optParts.push(`${k}=${v}`);
        }
        fields[key] =
          optParts.length > 0 ? `${base},${optParts.join(",")}` : base;
      }
    }

    // Boot / Options
    const origOnboot = bool01(original["onboot"]);
    if (onboot !== origOnboot) fields["onboot"] = onboot ? "1" : "0";
    const origTablet =
      original["tablet"] != null ? bool01(original["tablet"]) : true;
    if (tablet !== origTablet) fields["tablet"] = tablet ? "1" : "0";
    if (hotplug !== str(original["hotplug"] ?? "")) fields["hotplug"] = hotplug;
    if (ostype !== str(original["ostype"] ?? DEFAULT_OSTYPE))
      fields["ostype"] = ostype;
    const origProtection = bool01(original["protection"]);
    if (protection !== origProtection)
      fields["protection"] = protection ? "1" : "0";
    const origLocaltime = bool01(original["localtime"]);
    if (localtime !== origLocaltime)
      fields["localtime"] = localtime ? "1" : "0";
    for (const f of optionalScalars) {
      if (f.value === f.was) continue;
      if (f.value) fields[f.key] = f.value;
      else deleteFields.push(f.key);
    }
    for (const b of optionalBooleans) {
      if (b.value !== b.was) fields[b.key] = b.value ? "1" : "0";
    }
    // Boot order: see bootToSend.
    if (bootToSend !== null) fields["boot"] = bootToSend;
    // Startup order, rebuilt on the stored value: a bare order stays.
    const origStartupStr = str(original["startup"] ?? "");
    const newStartup = buildStartup(
      { order: startupOrder, up: startupUp, down: startupDown },
      origStartupStr,
    );
    if (newStartup !== origStartupStr) {
      if (newStartup) {
        fields["startup"] = newStartup;
      } else {
        deleteFields.push("startup");
      }
    }

    // Network (multi-NIC). Each NIC is rebuilt on its stored value, so an
    // untouched one comes back identical and is not sent, and an edited one
    // changes only the options edited here. A NIC marked for removal is only
    // deleted, for the reason a removed disk is.
    for (const [nicKey, nic] of Object.entries(nicEdits)) {
      if (deviceRemovals.has(nicKey)) continue;
      const origVal = str(original[nicKey] ?? "");
      const newVal = buildNet(nic, origVal);
      if (newVal !== origVal) fields[nicKey] = newVal;
    }

    // Generic device additions
    for (const [key, val] of Object.entries(pendingDeviceAdds)) {
      fields[key] = val;
      // If adding an EFI disk, auto-set BIOS to OVMF
      if (key === "efidisk0" && bios !== "ovmf") {
        fields["bios"] = "ovmf";
      }
    }

    // Generic device removals
    for (const key of deviceRemovals) {
      deleteFields.push(key);
    }

    // Display. Rebuilt on the stored values like the NICs. With no vga line
    // nothing is written until a type is picked. Default drops the type, and
    // the whole line when nothing else is left on it.
    const origVga = str(original["vga"] ?? "");
    const newVga = buildVGA({ type: vgaType, memory: vgaMemory }, origVga);
    if (newVga !== origVga) {
      if (newVga) {
        fields["vga"] = newVga;
      } else {
        deleteFields.push("vga");
      }
    }
    // The driver has no control here; it is carried from the stored value.
    const origAudio = str(original["audio0"] ?? "");
    const newAudio = buildAudio(
      { ...parseAudio(origAudio), device: audioDevice },
      origAudio,
    );
    if (newAudio !== origAudio) {
      if (newAudio) {
        fields["audio0"] = newAudio;
      } else {
        deleteFields.push("audio0");
      }
    }

    // CD/DVD drives (all media=cdrom devices), each rebuilt on its stored
    // value like the NICs: a new ISO keeps the drive's other options.
    for (const [key, isoVal] of Object.entries(cdromEdits)) {
      if (deviceRemovals.has(key)) continue;
      const origRaw = str(original[key] ?? "");
      const newVal = buildCDROM(isoVal, origRaw);
      if (newVal !== origRaw) fields[key] = newVal;
    }

    // Meta
    if (description !== str(original["description"] ?? ""))
      fields["description"] = description;
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
      const all = existing
        ? `${existing},${deleteFields.join(",")}`
        : deleteFields.join(",");
      fields["delete"] = all;
    }

    // Handle disk resizes via the separate resize API. Not for a disk this
    // same Save removes: the resize is its own request, raced by the delete.
    // While the disk is still in the config it grows a volume the operator
    // is detaching, which Proxmox cannot shrink back ("shrinking disks is not
    // supported"); once the delete has taken it out, it fails ("disk 'scsi0'
    // does not exist", resize_vm in src/PVE/API2/Qemu.pm) beside a Save that
    // succeeded.
    for (const [key, edit] of Object.entries(diskEdits)) {
      if (disksToRemove.has(key)) continue;
      if (edit.newSize) {
        settle(
          resizeMutation.mutateAsync({
            clusterId,
            vmId,
            disk: key,
            size: edit.newSize,
          }),
          {
            action: t("hardwarePanel.resizingDiskKey", { key }),
            onError: (err) => {
              const msg =
                describeError(err) || t("hardwarePanel.theRequestFailed");
              setResizeErrors((prev) => [...prev, `${key}: ${msg}`]);
            },
          },
        );
      }
    }

    if (Object.keys(fields).length === 0) return;

    settle(setConfigMutation.mutateAsync({ clusterId, vmId, fields }), {
      action: t("hardwarePanel.savingTheHardwareSettings"),
      // Clear pending state after successful save
      onSuccess: clearStagedChanges,
      onError: () => undefined, // shown in the save bar from the mutation's state
    });
  }

  /**
   * Optional scalar options: one description of each, consumed by both the
   * pending-change diff in handleSave and the dirty check in hasChanges.
   * Those two are otherwise hand-maintained twins, and a field added to one
   * but not the other either never saves or never marks the panel dirty.
   * An empty value means "clear it", which Proxmox expresses as a delete.
   */
  const optionalScalars = useMemo(() => {
    if (!original) return [];
    const o = (k: string) => (original[k] != null ? str(original[k]) : "");
    return [
      { key: "vcpus", value: vcpus, was: o("vcpus") },
      { key: "startdate", value: startdate, was: o("startdate") },
      { key: "keyboard", value: keyboard, was: o("keyboard") },
      {
        key: "vmstatestorage",
        value: vmstatestorage,
        was: o("vmstatestorage"),
      },
      {
        key: "migrate_downtime",
        value: migrateDowntime,
        was: o("migrate_downtime"),
      },
      { key: "migrate_speed", value: migrateSpeed, was: o("migrate_speed") },
      {
        key: "watchdog",
        value: buildWatchdog({ model: watchdogModel, action: watchdogAction }),
        was: buildWatchdog(parseWatchdog(o("watchdog"))),
      },
      {
        key: "smbios1",
        value: buildSMBIOS({ uuid: smbiosUuid, values: smbios }),
        was: buildSMBIOS(parseSMBIOS(o("smbios1"))),
      },
      // These two are rebuilt on what is stored, keeping every option the
      // panel has no control for, and are never empty once changed.
      {
        key: "machine",
        value: machineFor(machine, o("machine")),
        was: o("machine"),
      },
      {
        key: "agent",
        value: buildAgent(
          {
            enabled: agentEnabled,
            // FSTRIM is an edit only while the agent is on: its control is
            // shown only then, and Proxmox ignores it otherwise.
            fstrimClonedDisks: agentEnabled
              ? agentFstrim
              : parseAgent(o("agent")).fstrimClonedDisks,
          },
          o("agent"),
        ),
        was: o("agent"),
      },
    ];
  }, [
    original,
    vcpus,
    startdate,
    keyboard,
    vmstatestorage,
    migrateDowntime,
    migrateSpeed,
    watchdogModel,
    watchdogAction,
    smbiosUuid,
    smbios,
    machine,
    agentEnabled,
    agentFstrim,
  ]);

  /**
   * The SMBIOS UUID as loaded. Changing or clearing it changes the guest's
   * SMBIOS UUID on next boot, which can deactivate Windows and — because
   * Veeam's Proxmox objectId IS the smbios1 uuid (see internal/veeam
   * inventory) — silently drops the guest out of Nexara's own backup
   * correlation. Warn before that lands rather than hiding it.
   */
  const originalSmbiosUuid = useMemo(
    () => parseSMBIOS(str(original?.["smbios1"] ?? "")).uuid,
    [original],
  );
  const smbiosUuidChanged =
    originalSmbiosUuid !== "" && smbiosUuid !== originalSmbiosUuid;

  /** Booleans Proxmox defaults to a specific value when the key is absent. */
  const optionalBooleans = useMemo(() => {
    if (!original) return [];
    const ob = (k: string, dflt: boolean) =>
      original[k] != null ? bool01(original[k]) : dflt;
    return [
      { key: "freeze", value: freeze, was: ob("freeze", false) },
      { key: "reboot", value: reboot, was: ob("reboot", true) },
      { key: "tdf", value: tdf, was: ob("tdf", false) },
      { key: "allow-ksm", value: allowKsm, was: ob("allow-ksm", true) },
    ];
  }, [original, freeze, reboot, tdf, allowKsm]);

  /**
   * The `boot` value Save sends, or null when it sends none: the ticked
   * devices less every device this same Save removes, or the VM does not
   * have. Proxmox refuses a boot order that names a device the config lacks
   * or the request also deletes ("invalid bootorder: device ... does not
   * exist'", in $update_vm_api, src/PVE/API2/Qemu.pm), and it refuses it
   * late. That function writes each delete to the VM's pending changes first,
   * then the other options one at a time in hash order, and dies on reaching
   * boot before applying any of it. The removal, and whichever options came
   * before boot, stay pending and apply at the next start (vm_start_nolock
   * calls vmconfig_apply_pending, src/PVE/QemuServer.pm) or with the next
   * write that succeeds, after the request has reported an error.
   *
   * It is compared with the stored order less the same devices, because
   * that is what Proxmox makes of the stored order when no boot is sent: its
   * delete loop drops each deleted device from it ("remove from bootorder if
   * necessary"). So removing a ticked device sends no boot of its own. A
   * device the VM lacks it keeps, and boots by the rest (device_bootorder):
   * left out of both, an order naming one is not sent just to drop it.
   */
  // The legacy value the stored boot order comes from, or null (bootDevices).
  const legacyBoot = useMemo(
    () => (original ? bootDevices(original).legacy : null),
    [original],
  );

  const bootToSend = useMemo(() => {
    if (!original) return null;
    const kept = (device: string) =>
      original[device] != null &&
      !disksToRemove.has(device) &&
      !deviceRemovals.has(device);
    const stored = bootDevices(original).devices.filter(kept);
    const enabled = bootOrder
      .filter((b) => b.enabled && kept(b.device))
      .map((b) => b.device);
    return enabled.join(";") === stored.join(";")
      ? null
      : buildBootOrder(enabled);
  }, [original, bootOrder, disksToRemove, deviceRemovals]);

  const hasChanges = useMemo(() => {
    if (!original) return false;
    if (cores !== str(original["cores"] ?? 1)) return true;
    if (sockets !== str(original["sockets"] ?? 1)) return true;
    if (
      buildCPU({ model: cpuType, flags: cpuFlagStates, extra: cpuExtra }) !==
      normalizeCPU(str(original["cpu"] ?? ""))
    )
      return true;
    if (numa !== bool01(original["numa"])) return true;
    if (
      cpulimit !==
      (original["cpulimit"] != null ? str(original["cpulimit"]) : "")
    )
      return true;
    if (
      cpuunits !==
      (original["cpuunits"] != null ? str(original["cpuunits"]) : "")
    )
      return true;
    if (memory !== str(original["memory"] ?? DEFAULT_MEMORY)) return true;
    if (
      balloon !== (original["balloon"] != null ? str(original["balloon"]) : "")
    )
      return true;
    if (shares !== (original["shares"] != null ? str(original["shares"]) : ""))
      return true;
    if (bios !== str(original["bios"] ?? "seabios")) return true;
    if (scsihw !== str(original["scsihw"] ?? DEFAULT_SCSIHW)) return true;
    if (
      kvmEnabled !== (original["kvm"] != null ? bool01(original["kvm"]) : true)
    )
      return true;
    if (acpi !== (original["acpi"] != null ? bool01(original["acpi"]) : true))
      return true;
    if (onboot !== bool01(original["onboot"])) return true;
    if (
      tablet !==
      (original["tablet"] != null ? bool01(original["tablet"]) : true)
    )
      return true;
    if (hotplug !== str(original["hotplug"] ?? "")) return true;
    if (ostype !== str(original["ostype"] ?? DEFAULT_OSTYPE)) return true;
    if (protection !== bool01(original["protection"])) return true;
    if (localtime !== bool01(original["localtime"])) return true;
    if (optionalScalars.some((f) => f.value !== f.was)) return true;
    if (optionalBooleans.some((b) => b.value !== b.was)) return true;
    if (bootToSend !== null) return true;
    const origStartup = str(original["startup"] ?? "");
    if (
      buildStartup(
        { order: startupOrder, up: startupUp, down: startupDown },
        origStartup,
      ) !== origStartup
    )
      return true;
    // Multi-NIC changes, compared as handleSave compares them
    for (const [nicKey, nic] of Object.entries(nicEdits)) {
      const origNic = str(original[nicKey] ?? "");
      if (buildNet(nic, origNic) !== origNic) return true;
    }
    // Generic device changes
    if (Object.keys(pendingDeviceAdds).length > 0) return true;
    if (deviceRemovals.size > 0) return true;
    const origVga = str(original["vga"] ?? "");
    if (buildVGA({ type: vgaType, memory: vgaMemory }, origVga) !== origVga)
      return true;
    const origAudio = str(original["audio0"] ?? "");
    if (
      buildAudio(
        { ...parseAudio(origAudio), device: audioDevice },
        origAudio,
      ) !== origAudio
    )
      return true;
    // CD/DVD drives, compared as handleSave compares them
    for (const [key, isoVal] of Object.entries(cdromEdits)) {
      if (deviceRemovals.has(key)) continue;
      const origRaw = str(original[key] ?? "");
      if (buildCDROM(isoVal, origRaw) !== origRaw) return true;
    }
    if (description !== str(original["description"] ?? "")) return true;
    if (tags !== str(original["tags"] ?? "")) return true;
    for (const [key, edit] of Object.entries(diskEdits)) {
      if (edit.newSize) return true;
      const origDisk = parseDisk(str(original[key] ?? ""));
      if (
        edit.cache !== origDisk.cache ||
        edit.discard !== origDisk.discard ||
        edit.ssd !== origDisk.ssd ||
        edit.iothread !== origDisk.iothread
      )
        return true;
    }
    if (disksToRemove.size > 0) return true;
    if (pendingNewDisks.length > 0) return true;
    return false;
  }, [
    original,
    cores,
    sockets,
    cpuType,
    cpuFlagStates,
    cpuExtra,
    numa,
    cpulimit,
    cpuunits,
    memory,
    balloon,
    shares,
    bios,
    scsihw,
    kvmEnabled,
    acpi,
    onboot,
    tablet,
    hotplug,
    ostype,
    protection,
    localtime,
    optionalScalars,
    optionalBooleans,
    bootToSend,
    startupOrder,
    startupUp,
    startupDown,
    nicEdits,
    vgaType,
    vgaMemory,
    audioDevice,
    cdromEdits,
    description,
    tags,
    diskEdits,
    disksToRemove,
    pendingNewDisks,
    pendingDeviceAdds,
    deviceRemovals,
  ]);

  if (isLoading) {
    return <Skeleton className="h-96 w-full" />;
  }

  if (!config) {
    return (
      <Panel>
        <p className="text-sm text-muted-foreground">
          {t("hardwarePanel.unableToLoadVMConfiguration")}
        </p>
      </Panel>
    );
  }

  function toggleRow(key: string) {
    setOpenRows((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  // How the panels below split what the config holds. Display only: Save
  // reads none of these.
  const totalVcpus = num(cores) * num(sockets);
  const hardDisks = diskEntries.filter(
    ({ key }) => !/^(efidisk|tpmstate|unused)/.test(key),
  );
  const firmwareDisks = diskEntries.filter(({ key }) =>
    /^(efidisk|tpmstate)/.test(key),
  );
  const unusedDisks = diskEntries.filter(({ key }) => key.startsWith("unused"));
  const cdromEntries = Object.entries(cdromEdits).sort(([a], [b]) =>
    a.localeCompare(b),
  );
  const pendingOf = (re: RegExp) =>
    Object.entries(pendingDeviceAdds).filter(([k]) => re.test(k));
  // Staged adds with no list of their own kind: an EFI disk, a TPM state, a
  // cloud-init drive. All are drives, so they wait among the disks.
  const pendingDrives = Object.entries(pendingDeviceAdds).filter(
    ([k]) => !/^(net|usb|hostpci|serial|virtiofs)\d+$/.test(k) && k !== "rng0",
  );
  const hasOtherDevices =
    usbKeys.length > 0 ||
    pciKeys.length > 0 ||
    serialKeys.length > 0 ||
    virtiofsKeys.length > 0 ||
    config["rng0"] != null ||
    pendingDeviceAdds["rng0"] != null ||
    pendingOf(/^(usb|hostpci|serial|virtiofs)\d+$/).length > 0;
  const otherDeviceCount =
    usbKeys.length +
    pciKeys.length +
    serialKeys.length +
    virtiofsKeys.length +
    (config["rng0"] != null ? 1 : 0);
  const bootEnabled = bootOrder.filter((b) => b.enabled).length;
  /** "1 vCPU", "2 hard disks": a count with its noun, from hardwarePanel.count. */
  const plural = (n: number, noun: string) =>
    t(`hardwarePanel.count.${noun}`, { count: n });

  const undoButton = (onClick: () => void) => (
    <Button variant="ghost" size="sm" className="h-7 px-2" onClick={onClick}>
      {t("hardwarePanel.undo")}
    </Button>
  );
  const cancelButton = (onClick: () => void) => (
    <Button variant="ghost" size="sm" className="h-7 px-2" onClick={onClick}>
      {t("hardwarePanel.cancel")}
    </Button>
  );
  const removedMarker = (
    <PendingBadge>{t("hardwarePanel.markedForRemoval")}</PendingBadge>
  );
  /** A staged add of one kind, as rows after the devices of that kind. */
  const pendingRows = (re: RegExp, icon: LucideIcon, title: string) =>
    pendingOf(re).map(([key, val]) => (
      <DeviceRow
        key={key}
        name={key}
        icon={icon}
        title={title}
        marker={<PendingBadge>{t("hardwarePanel.addedOnSave")}</PendingBadge>}
        summary={<span className="break-all font-mono">{val}</span>}
        actions={cancelButton(() => {
          handleCancelPendingDevice(key);
        })}
      />
    ));

  return (
    <div className={cn("space-y-4", clearSaveBarClass)}>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <p className="min-w-0 flex-[1_1_18rem] text-sm text-muted-foreground">
          {t(
            isRunning
              ? "hardwarePanel.summaryRunning"
              : "hardwarePanel.summary",
            {
              vcpus: plural(totalVcpus, "vcpu"),
              memory: formatBytes(num(memory) * 1024 * 1024),
              disks: plural(hardDisks.length, "hardDisk"),
              nics: plural(nicKeys.length, "networkDevice"),
            },
          )}
        </p>
        {/* Add device: the tab's one primary action. */}
        <AddDeviceMenu
          // Staged adds count as taken — devices, and CD/DVD drives, which are
          // staged separately — so two added before a Save land in two slots
          // rather than the second replacing the first. The machine type is the
          // staged one too: it is saved in the same write as a device added
          // now, and PCIe is offered by it.
          config={{ ...config, ...pendingDeviceAdds, ...cdromEdits, machine }}
          clusterId={clusterId}
          nodeName={nodeName}
          diskStorages={diskStorages.map((s) => ({
            storage: s.storage,
            type: s.type,
            id: s.id,
          }))}
          usbDevices={usbDevices}
          pciDevices={pciDevices}
          bridges={bridgeNames}
          isoFiles={isoFiles}
          onAddDevice={handleAddDevice}
          onAddCDROM={handleAddCDROM}
          onAddDisk={() => {
            setShowAddDisk(true);
          }}
        />
      </div>

      {/* Two columns that fold into one: what the VM is made of on the left,
          how it is shown and started on the right. */}
      <div className="flex flex-wrap items-start gap-4">
        <div className="flex min-w-0 flex-[3_1_520px] flex-col gap-4">
          <Panel
            flush
            title={t("hardwarePanel.processorAndMemory")}
            aside={t("hardwarePanel.vcpusAndMemory", {
              vcpus: plural(totalVcpus, "vcpu"),
              memory: formatBytes(num(memory) * 1024 * 1024),
            })}
          >
            <GroupList>
              <Group
                title={t("hardwarePanel.processor")}
                summary={t("hardwarePanel.socketsOfCores", {
                  sockets: plural(num(sockets), "socket"),
                  cores: plural(num(cores), "core"),
                })}
              >
                <FieldGrid>
                  <Field label={t("hardwarePanel.sockets")}>
                    <Input
                      type="number"
                      min={1}
                      value={sockets}
                      onChange={(e) => {
                        setSockets(e.target.value);
                      }}
                      className={fieldInputClass}
                    />
                  </Field>
                  <Field label={t("hardwarePanel.cores")}>
                    <Input
                      type="number"
                      min={1}
                      value={cores}
                      onChange={(e) => {
                        setCores(e.target.value);
                      }}
                      className={fieldInputClass}
                    />
                  </Field>
                  <Field label={t("hardwarePanel.cpuType")}>
                    <select
                      className={fieldSelectClass}
                      value={cpuType}
                      onChange={(e) => {
                        setCpuType(e.target.value);
                      }}
                    >
                      <CurrentValueOption
                        value={storedSelects.cpuType}
                        known={cpuTypes}
                      />
                      {cpuTypes.map((t) => (
                        <option key={t} value={t}>
                          {t}
                        </option>
                      ))}
                    </select>
                  </Field>
                  <Field label={t("hardwarePanel.cpuLimit")}>
                    <Input
                      type="number"
                      min={0}
                      max={128}
                      step={0.1}
                      value={cpulimit}
                      onChange={(e) => {
                        setCpulimit(e.target.value);
                      }}
                      placeholder={t("hardwarePanel.zeroNone")}
                      className={fieldInputClass}
                    />
                  </Field>
                  <Field label={t("hardwarePanel.cpuUnits")}>
                    <Input
                      type="number"
                      min={1}
                      max={262144}
                      value={cpuunits}
                      onChange={(e) => {
                        setCpuunits(e.target.value);
                      }}
                      placeholder="100"
                      className={fieldInputClass}
                    />
                  </Field>
                  <Field label={t("hardwarePanel.hotplugVCPUs")}>
                    <Input
                      type="number"
                      min={1}
                      max={Math.max(1, totalVcpus)}
                      value={vcpus}
                      onChange={(e) => {
                        setVcpus(e.target.value);
                      }}
                      placeholder={t("hardwarePanel.allTotalVcpus", {
                        totalVcpus: String(totalVcpus),
                      })}
                      className={fieldInputClass}
                    />
                  </Field>
                </FieldGrid>
                <Checks>
                  <Check
                    id="hw-numa"
                    label={t("hardwarePanel.numa")}
                    checked={numa}
                    onCheckedChange={(v) => {
                      setNuma(v === true);
                    }}
                  />
                </Checks>

                <details className="group">
                  <summary className="flex cursor-pointer list-none items-center gap-1.5 text-[13px] font-medium text-muted-foreground hover:text-foreground [&::-webkit-details-marker]:hidden">
                    <ChevronDown
                      aria-hidden="true"
                      className="h-3.5 w-3.5 -rotate-90 transition-transform group-open:rotate-0"
                    />
                    {t("hardwarePanel.cpuFlags")}
                    {setCpuFlagCount > 0 && (
                      <span className="font-normal">
                        {t("hardwarePanel.setCpuFlagCountSet", {
                          setCpuFlagCount,
                        })}
                      </span>
                    )}
                  </summary>
                  <div className="mt-2 divide-y">
                    {cpuFlagRows.map((flag) => {
                      const state = cpuFlagStates[flag.name] ?? "default";
                      // Three outcomes, not two: null is "Proxmox did not say"
                      // (old cluster or the fallback list), an empty list is
                      // "checked, and no node supports it", and a list without this
                      // node is "supported in the cluster, but not here".
                      const support:
                        "unknown" | "nowhere" | "elsewhere" | "here" =
                        flag.supportedOn === null
                          ? "unknown"
                          : flag.supportedOn.length === 0
                            ? "nowhere"
                            : flag.supportedOn.includes(nodeName)
                              ? "here"
                              : "elsewhere";
                      return (
                        <div key={flag.name} className="space-y-0.5 py-2">
                          {/* Name and control share one row; the description spans
                              the full width beneath, so a long one does not squeeze
                              the name into a one-word-per-line column. */}
                          <div className="flex items-center justify-between gap-2">
                            <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                              <code className="text-[12.5px]">{flag.name}</code>
                              {support === "elsewhere" && (
                                <span
                                  className="text-xs text-amber-700 dark:text-amber-400"
                                  title={t(
                                    "hardwarePanel.notSupportedOnNodeNameAvailable",
                                    {
                                      nodeName,
                                      supportedOn: (
                                        flag.supportedOn ?? []
                                      ).join(", "),
                                    },
                                  )}
                                >
                                  {t("hardwarePanel.unsupportedHere")}
                                </span>
                              )}
                              {support === "nowhere" && (
                                <span
                                  className="text-xs text-red-700 dark:text-red-400"
                                  title={t(
                                    "hardwarePanel.noNodeInThisCluster",
                                    { name: flag.name },
                                  )}
                                >
                                  {t("hardwarePanel.unsupportedOnAnyNode")}
                                </span>
                              )}
                            </div>
                            <select
                              // cn(), not concatenation: the shared class carries
                              // w-full, and only tailwind-merge lets w-24 win.
                              className={cn(fieldSelectClass, "w-24 shrink-0")}
                              value={state}
                              aria-label={t("hardwarePanel.nameCPUFlag", {
                                name: flag.name,
                              })}
                              onChange={(e) => {
                                const v = e.target.value as CPUFlagState;
                                setCpuFlagStates((prev) => {
                                  // Rebuilt by filter rather than delete: "default"
                                  // means absent from flags=, not a stored value.
                                  const next = Object.fromEntries(
                                    Object.entries(prev).filter(
                                      ([name]) => name !== flag.name,
                                    ),
                                  );
                                  if (v !== "default") next[flag.name] = v;
                                  return next;
                                });
                              }}
                            >
                              <option value="default">
                                {t("hardwarePanel.default")}
                              </option>
                              <option value="on">
                                {t("hardwarePanel.on")}
                              </option>
                              <option value="off">
                                {t("hardwarePanel.off")}
                              </option>
                            </select>
                          </div>
                          {flag.description && (
                            <p className="text-xs leading-snug text-muted-foreground">
                              {flag.description}
                            </p>
                          )}
                        </div>
                      );
                    })}
                  </div>
                </details>
              </Group>

              <Group
                title={t("hardwarePanel.memory")}
                summary={formatBytes(num(memory) * 1024 * 1024)}
              >
                <FieldGrid>
                  <Field
                    label={t("hardwarePanel.memoryMiB")}
                    note={
                      memoryMissing && (
                        <p className="mt-1 text-[12.5px] text-destructive">
                          {t("hardwarePanel.proxmoxCannotTakeAnEmpty")}
                        </p>
                      )
                    }
                  >
                    <Input
                      type="number"
                      min={64}
                      step={64}
                      value={memory}
                      onChange={(e) => {
                        setMemory(e.target.value);
                      }}
                      className={fieldInputClass}
                    />
                  </Field>
                  <Field label={t("hardwarePanel.balloonMinimumMiB")}>
                    <Input
                      type="number"
                      min={0}
                      step={64}
                      value={balloon}
                      onChange={(e) => {
                        setBalloon(e.target.value);
                      }}
                      placeholder={t("hardwarePanel.n0Disabled")}
                      className={fieldInputClass}
                    />
                  </Field>
                  <Field label={t("hardwarePanel.ballooningShares")}>
                    <Input
                      type="number"
                      min={0}
                      max={50000}
                      value={shares}
                      onChange={(e) => {
                        setShares(e.target.value);
                      }}
                      placeholder={t("hardwarePanel.cpuUnitsDefault")}
                      className={fieldInputClass}
                    />
                  </Field>
                </FieldGrid>
                <Checks>
                  <Check
                    id="hw-allow-ksm"
                    label={t("hardwarePanel.allowKSMPageMerging")}
                    checked={allowKsm}
                    onCheckedChange={(v) => {
                      setAllowKsm(v === true);
                    }}
                  />
                </Checks>
              </Group>
            </GroupList>
          </Panel>

          <Panel
            flush
            title={t("hardwarePanel.disks")}
            aside={t("hardwarePanel.disksAndDrives", {
              disks: plural(hardDisks.length, "hardDisk"),
              drives: plural(cdromEntries.length, "cdDvdDrive"),
            })}
            actions={
              showAddDisk ? undefined : (
                <Button
                  variant="outline"
                  size="sm"
                  className="gap-1.5"
                  onClick={() => {
                    setShowAddDisk(true);
                  }}
                >
                  <Plus className="h-3.5 w-3.5" /> {t("hardwarePanel.addDisk")}
                </Button>
              )
            }
          >
            {diskEntries.length === 0 && pendingNewDisks.length === 0 && (
              <p className="border-t px-[18px] py-3 text-sm text-muted-foreground">
                {t("hardwarePanel.noDisksConfigured")}
              </p>
            )}
            <DeviceList>
              {hardDisks.map(({ key, parsed }) => {
                if (disksToRemove.has(key)) {
                  return (
                    <DeviceRow
                      key={key}
                      name={key}
                      icon={HardDrive}
                      title={t("hardwarePanel.hardDisk")}
                      struck
                      marker={removedMarker}
                      summary={
                        <>
                          <span>{parsed.size || "--"}</span>
                          {parsed.storage && (
                            <span>
                              {t("hardwarePanel.onStorage", {
                                storage: parsed.storage,
                              })}
                            </span>
                          )}
                        </>
                      }
                      actions={undoButton(() => {
                        handleUndoRemoveDisk(key);
                      })}
                    />
                  );
                }
                const edit = diskEdits[key] ?? {
                  cache: parsed.cache,
                  discard: parsed.discard,
                  ssd: parsed.ssd,
                  iothread: parsed.iothread,
                  newSize: "",
                };
                const edited =
                  edit.newSize !== "" ||
                  edit.cache !== parsed.cache ||
                  edit.discard !== parsed.discard ||
                  edit.ssd !== parsed.ssd ||
                  edit.iothread !== parsed.iothread;
                return (
                  <DeviceRow
                    key={key}
                    name={key}
                    icon={HardDrive}
                    title={t("hardwarePanel.hardDisk")}
                    marker={
                      edited ? (
                        <PendingBadge>{t("hardwarePanel.edited")}</PendingBadge>
                      ) : null
                    }
                    summary={
                      <>
                        <span>{parsed.size || "--"}</span>
                        <span>{parsed.format || "raw"}</span>
                        {parsed.storage && (
                          <span>
                            {t("hardwarePanel.onStorage", {
                              storage: parsed.storage,
                            })}
                          </span>
                        )}
                        {edit.newSize && (
                          <span>
                            {t("hardwarePanel.resizeToNewSize", {
                              newSize: edit.newSize,
                            })}
                          </span>
                        )}
                        {edit.cache && (
                          <span>
                            {t("hardwarePanel.cacheCache", {
                              cache: edit.cache,
                            })}
                          </span>
                        )}
                        {edit.discard && (
                          <span>{t("hardwarePanel.discard")}</span>
                        )}
                        {edit.ssd && (
                          <span>{t("hardwarePanel.ssdEmulation")}</span>
                        )}
                        {edit.iothread && (
                          <span>{t("hardwarePanel.ioThread")}</span>
                        )}
                      </>
                    }
                    open={openRows.has(key)}
                    onToggle={() => {
                      toggleRow(key);
                    }}
                    actions={
                      <>
                        <MoveDiskDialog
                          clusterId={clusterId}
                          vmId={vmId}
                          diskName={key}
                          storageOptions={diskStorages.map((s) => ({
                            storage: s.storage,
                            type: s.type,
                          }))}
                          currentStorage={parsed.storage}
                          currentFormat={parsed.format}
                        />
                        <IconAction
                          label={t("hardwarePanel.removeKey", { key })}
                          icon={Trash2}
                          destructive
                          onClick={() => {
                            handleRemoveDisk(key);
                          }}
                        />
                      </>
                    }
                    editor={
                      <>
                        <FieldGrid>
                          <Field label={t("hardwarePanel.resizeTo")}>
                            <Input
                              type="text"
                              value={edit.newSize}
                              onChange={(e) => {
                                updateDiskEdit(key, {
                                  newSize: e.target.value,
                                });
                              }}
                              placeholder={parsed.size || "e.g. 64G"}
                              className={fieldInputClass}
                            />
                          </Field>
                          <Field label={t("hardwarePanel.cache")}>
                            <select
                              className={fieldSelectClass}
                              value={edit.cache}
                              onChange={(e) => {
                                updateDiskEdit(key, { cache: e.target.value });
                              }}
                            >
                              <CurrentValueOption
                                value={parsed.cache}
                                known={cacheModes.map((c) => c.value)}
                              />
                              <option value="">
                                {t("hardwarePanel.default")}
                              </option>
                              {cacheModes.map((c) => (
                                <option key={c.value} value={c.value}>
                                  {c.label}
                                </option>
                              ))}
                            </select>
                          </Field>
                        </FieldGrid>
                        <Checks>
                          <Check
                            id={`hw-disk-${key}-discard`}
                            label={t("hardwarePanel.discard2")}
                            checked={edit.discard}
                            onCheckedChange={(v) => {
                              updateDiskEdit(key, { discard: v === true });
                            }}
                          />
                          <Check
                            id={`hw-disk-${key}-ssd`}
                            label={t("hardwarePanel.ssdEmulation")}
                            checked={edit.ssd}
                            onCheckedChange={(v) => {
                              updateDiskEdit(key, { ssd: v === true });
                            }}
                          />
                          <Check
                            id={`hw-disk-${key}-iothread`}
                            label={t("hardwarePanel.ioThread")}
                            checked={edit.iothread}
                            onCheckedChange={(v) => {
                              updateDiskEdit(key, { iothread: v === true });
                            }}
                          />
                        </Checks>
                      </>
                    }
                  />
                );
              })}
              {pendingNewDisks.map((d) => (
                <DeviceRow
                  key={d.key}
                  name={d.key}
                  icon={HardDrive}
                  title={t("hardwarePanel.hardDisk")}
                  marker={
                    <PendingBadge>
                      {t("hardwarePanel.createdOnSave")}
                    </PendingBadge>
                  }
                  summary={
                    <>
                      <span>{d.size}G</span>
                      {d.format && <span>{d.format}</span>}
                      <span>
                        {t("hardwarePanel.onStorage", { storage: d.storage })}
                      </span>
                      {d.cache && (
                        <span>
                          {t("hardwarePanel.cacheCache", { cache: d.cache })}
                        </span>
                      )}
                      {d.discard && <span>{t("hardwarePanel.discard")}</span>}
                      {d.ssd && <span>{t("hardwarePanel.ssdEmulation")}</span>}
                      {d.iothread && <span>{t("hardwarePanel.ioThread")}</span>}
                    </>
                  }
                  actions={cancelButton(() => {
                    handleRemovePendingDisk(d.key);
                  })}
                />
              ))}
            </DeviceList>

            {showAddDisk && (
              <div className="border-t px-[18px] py-4">
                <p className="mb-3 text-sm font-medium">
                  {t("hardwarePanel.newHardDisk")}
                </p>
                <FieldGrid>
                  <Field label={t("hardwarePanel.bus")}>
                    <select
                      className={fieldSelectClass}
                      value={addDiskBus}
                      onChange={(e) => {
                        setAddDiskBus(e.target.value);
                      }}
                    >
                      {diskBusTypes.map((b) => (
                        <option key={b.value} value={b.value}>
                          {b.label}
                        </option>
                      ))}
                    </select>
                  </Field>
                  <Field label={t("hardwarePanel.storage")}>
                    <select
                      className={fieldSelectClass}
                      value={addDiskStorage}
                      onChange={(e) => {
                        setAddDiskStorage(e.target.value);
                      }}
                    >
                      <option value="">{t("hardwarePanel.select")}</option>
                      {diskStorages.map((s) => (
                        <option key={s.id} value={s.storage}>
                          {s.storage} ({s.type})
                        </option>
                      ))}
                    </select>
                  </Field>
                  <Field label={t("hardwarePanel.sizeGiB")}>
                    <Input
                      type="number"
                      min={1}
                      className={fieldInputClass}
                      value={addDiskSize}
                      onChange={(e) => {
                        setAddDiskSize(e.target.value);
                      }}
                    />
                  </Field>
                  <Field label={t("hardwarePanel.format")}>
                    <select
                      className={fieldSelectClass}
                      value={diskFormatFor(addDiskType, addDiskFormat)}
                      disabled={addDiskFormats.length === 1}
                      onChange={(e) => {
                        setAddDiskFormat(e.target.value);
                      }}
                    >
                      {diskFormats
                        .filter((f) => addDiskFormats.includes(f.value))
                        .map((f) => (
                          <option key={f.value} value={f.value}>
                            {f.label}
                          </option>
                        ))}
                    </select>
                  </Field>
                  <Field label={t("hardwarePanel.cache")}>
                    <select
                      className={fieldSelectClass}
                      value={addDiskCache}
                      onChange={(e) => {
                        setAddDiskCache(e.target.value);
                      }}
                    >
                      <option value="">{t("hardwarePanel.default")}</option>
                      {cacheModes.map((c) => (
                        <option key={c.value} value={c.value}>
                          {c.label}
                        </option>
                      ))}
                    </select>
                  </Field>
                </FieldGrid>
                <div className="mt-3 flex flex-wrap items-center gap-x-5 gap-y-2">
                  <Check
                    id="hw-add-disk-discard"
                    label={t("hardwarePanel.discard2")}
                    checked={addDiskDiscard}
                    onCheckedChange={(v) => {
                      setAddDiskDiscard(v === true);
                    }}
                  />
                  <Check
                    id="hw-add-disk-ssd"
                    label={t("hardwarePanel.ssdEmulation")}
                    checked={addDiskSsd}
                    onCheckedChange={(v) => {
                      setAddDiskSsd(v === true);
                    }}
                  />
                  <Check
                    id="hw-add-disk-iothread"
                    label={t("hardwarePanel.ioThread")}
                    checked={addDiskIothread}
                    onCheckedChange={(v) => {
                      setAddDiskIothread(v === true);
                    }}
                  />
                  <div className="ml-auto flex gap-2">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        setShowAddDisk(false);
                      }}
                    >
                      {t("hardwarePanel.cancel")}
                    </Button>
                    <Button
                      variant="secondary"
                      size="sm"
                      className="gap-1.5"
                      onClick={handleAddDisk}
                      disabled={!addDiskStorage || !addDiskSize}
                    >
                      <Plus className="h-3.5 w-3.5" /> {t("hardwarePanel.add")}
                    </Button>
                  </div>
                </div>
              </div>
            )}

            <div
              ref={cdromList}
              role="region"
              aria-label={t("hardwarePanel.cdDVDDrives")}
              tabIndex={-1}
              className="outline-none"
            >
              <DeviceList>
                {cdromEntries.map(([key, isoVal]) => {
                  // A cloud-init drive holds the volume Proxmox generates, not
                  // an ISO: picking one would free it, as removing it does.
                  const cloudInit = isCloudInitVolume(isoVal);
                  const title = cloudInit
                    ? t("hardwarePanel.cloudInitDrive")
                    : t("hardwarePanel.cdDvdDrive");
                  const icon = cloudInit ? Monitor : Disc;
                  if (deviceRemovals.has(key)) {
                    return (
                      <DeviceRow
                        key={key}
                        name={key}
                        icon={icon}
                        title={title}
                        struck
                        marker={removedMarker}
                        actions={undoButton(() => {
                          handleUndoRemoveDevice(key);
                        })}
                      />
                    );
                  }
                  const stored = original?.[key];
                  const edited =
                    stored != null &&
                    buildCDROM(isoVal, str(stored)) !== str(stored);
                  return (
                    <DeviceRow
                      key={key}
                      name={key}
                      icon={icon}
                      title={title}
                      marker={
                        stored == null ? (
                          <PendingBadge>
                            {t("hardwarePanel.addedOnSave")}
                          </PendingBadge>
                        ) : edited ? (
                          <PendingBadge>
                            {t("hardwarePanel.edited")}
                          </PendingBadge>
                        ) : null
                      }
                      summary={
                        cloudInit ? (
                          <span className="break-all font-mono">{isoVal}</span>
                        ) : undefined
                      }
                      actions={
                        <>
                          {!cloudInit && (
                            <select
                              className={cn(
                                fieldSelectClass,
                                "w-64 max-w-full",
                              )}
                              aria-label={t("hardwarePanel.mediaInKey", {
                                key,
                              })}
                              value={isoVal}
                              onChange={(e) => {
                                setCdromEdits((prev) => ({
                                  ...prev,
                                  [key]: e.target.value,
                                }));
                              }}
                            >
                              <option value="none">
                                {t("hardwarePanel.noMediaEmptyDrive")}
                              </option>
                              {isoVal !== "none" &&
                                !isoFiles.some(
                                  (iso) => iso.volid === isoVal,
                                ) && (
                                  <option value={isoVal}>
                                    {t("hardwarePanel.isoValCurrent", {
                                      isoVal,
                                    })}
                                  </option>
                                )}
                              {isoFiles.map((iso) => (
                                <option key={iso.volid} value={iso.volid}>
                                  {iso.volid}
                                </option>
                              ))}
                            </select>
                          )}
                          <IconAction
                            label={t("hardwarePanel.removeKey", { key })}
                            icon={Trash2}
                            destructive
                            onClick={() => {
                              if (cloudInit) {
                                setCloudInitToRemove({ key, volume: isoVal });
                              } else {
                                handleRemoveCDROM(key);
                              }
                            }}
                          />
                        </>
                      }
                    />
                  );
                })}
              </DeviceList>
              {isoStorages.length > 1 && cdromEntries.length > 0 && (
                <label className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t px-[18px] py-2.5 pl-[44px] text-[12.5px] text-muted-foreground">
                  {t("hardwarePanel.listISOImagesFrom")}
                  <select
                    className={cn(fieldSelectClass, "w-48")}
                    value={cdromStorageId}
                    onChange={(e) => {
                      setCdromStorageId(e.target.value);
                    }}
                  >
                    {isoStorages.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.storage}
                      </option>
                    ))}
                  </select>
                </label>
              )}
            </div>

            <DeviceList>
              {firmwareDisks.map(({ key }) => {
                // Staged like any other device removal, so marked like one.
                if (deviceRemovals.has(key)) {
                  return (
                    <DeviceRow
                      key={key}
                      name={key}
                      icon={key.startsWith("efidisk") ? Key : Shield}
                      title={
                        key.startsWith("efidisk")
                          ? t("hardwarePanel.efiDisk")
                          : t("hardwarePanel.tpmState")
                      }
                      struck
                      marker={removedMarker}
                      actions={undoButton(() => {
                        handleUndoRemoveDevice(key);
                      })}
                    />
                  );
                }
                if (key.startsWith("efidisk")) {
                  const efi = parseEFIDisk(str(config[key]));
                  return (
                    <DeviceRow
                      key={key}
                      name={key}
                      icon={Key}
                      title={t("hardwarePanel.efiDisk")}
                      summary={
                        <>
                          <span>
                            {t("hardwarePanel.onStorage", {
                              storage: efi.storage,
                            })}
                          </span>
                          <span>{efi.efitype}</span>
                          {efi.preEnrolledKeys && (
                            <span>
                              {t("hardwarePanel.secureBootKeysEnrolled")}
                            </span>
                          )}
                        </>
                      }
                      actions={
                        <IconAction
                          label={t("hardwarePanel.removeKey", { key })}
                          icon={Trash2}
                          destructive
                          onClick={() => {
                            handleRemoveDevice(key);
                          }}
                        />
                      }
                    />
                  );
                }
                const tpm = parseTPMState(str(config[key]));
                return (
                  <DeviceRow
                    key={key}
                    name={key}
                    icon={Shield}
                    title={t("hardwarePanel.tpmState")}
                    summary={
                      <>
                        <span>
                          {t("hardwarePanel.onStorage", {
                            storage: tpm.storage,
                          })}
                        </span>
                        <span>{tpm.version}</span>
                      </>
                    }
                    actions={
                      <IconAction
                        label={t("hardwarePanel.removeKey", { key })}
                        icon={Trash2}
                        destructive
                        onClick={() => {
                          handleRemoveDevice(key);
                        }}
                      />
                    }
                  />
                );
              })}
              {unusedDisks.map(({ key, parsed }) => {
                // Never staged, so never "marked for removal": the Remove
                // button asks first and then deletes (see unusedToDelete).
                const volume = parsed.volume || str(config[key] ?? "");
                return (
                  <DeviceRow
                    key={key}
                    name={key}
                    icon={HardDrive}
                    title={t("hardwarePanel.detachedDisk")}
                    marker={
                      <Badge variant="warning">
                        {t("hardwarePanel.unused")}
                      </Badge>
                    }
                    summary={
                      <>
                        <span className="break-all font-mono">{volume}</span>
                        <span>{t("hardwarePanel.stillTakesSpaceOnIts")}</span>
                      </>
                    }
                    actions={
                      <IconAction
                        label={t("hardwarePanel.removeKey", { key })}
                        icon={Trash2}
                        destructive
                        onClick={() => {
                          openUnusedDelete(key, volume);
                        }}
                      />
                    }
                  />
                );
              })}
              {pendingDrives.map(([key, val]) => (
                <DeviceRow
                  key={key}
                  name={key}
                  icon={
                    key.startsWith("efidisk")
                      ? Key
                      : key.startsWith("tpmstate")
                        ? Shield
                        : isCloudInitVolume(val)
                          ? Monitor
                          : HardDrive
                  }
                  title={
                    key.startsWith("efidisk")
                      ? t("hardwarePanel.efiDisk")
                      : key.startsWith("tpmstate")
                        ? t("hardwarePanel.tpmState")
                        : isCloudInitVolume(val)
                          ? t("hardwarePanel.cloudInitDrive")
                          : t("hardwarePanel.drive")
                  }
                  marker={
                    <PendingBadge>
                      {t("hardwarePanel.addedOnSave")}
                    </PendingBadge>
                  }
                  summary={<span className="break-all font-mono">{val}</span>}
                  actions={cancelButton(() => {
                    handleCancelPendingDevice(key);
                  })}
                />
              ))}
            </DeviceList>
          </Panel>

          <Panel
            flush
            title={t("hardwarePanel.network")}
            aside={plural(nicKeys.length, "device")}
          >
            {nicKeys.length === 0 && (
              <p className="border-t px-[18px] py-3 text-sm text-muted-foreground">
                {t("hardwarePanel.noNetworkDevicesConfigured")}
              </p>
            )}
            <DeviceList>
              {nicKeys.map((nicKey) => {
                const nic = nicEdits[nicKey];
                if (!nic) return null;
                if (deviceRemovals.has(nicKey)) {
                  return (
                    <DeviceRow
                      key={nicKey}
                      name={nicKey}
                      icon={Network}
                      title={t("hardwarePanel.networkDevice")}
                      struck
                      marker={removedMarker}
                      actions={undoButton(() => {
                        handleUndoRemoveDevice(nicKey);
                      })}
                    />
                  );
                }
                const stored = str(original?.[nicKey] ?? "");
                const edited = buildNet(nic, stored) !== stored;
                return (
                  <DeviceRow
                    key={nicKey}
                    name={nicKey}
                    icon={Network}
                    title={t("hardwarePanel.networkDevice")}
                    marker={
                      edited ? (
                        <PendingBadge>{t("hardwarePanel.edited")}</PendingBadge>
                      ) : null
                    }
                    summary={
                      <>
                        <span>
                          {netModels.find((m) => m.value === nic.model)
                            ?.label ?? nic.model}
                        </span>
                        <span>
                          {nic.bridge
                            ? t("hardwarePanel.onBridge", {
                                bridge: nic.bridge,
                              })
                            : t("hardwarePanel.noBridgeNAT")}
                        </span>
                        {nic.vlanTag && (
                          <span>
                            {t("hardwarePanel.vlanVlanTag", {
                              vlanTag: nic.vlanTag,
                            })}
                          </span>
                        )}
                        {nic.mac && (
                          <span className="font-mono">{nic.mac}</span>
                        )}
                        {nic.firewall && (
                          <span>{t("hardwarePanel.firewallOn")}</span>
                        )}
                        {nic.linkDown && (
                          <span>{t("hardwarePanel.disconnected")}</span>
                        )}
                      </>
                    }
                    open={openRows.has(nicKey)}
                    onToggle={() => {
                      toggleRow(nicKey);
                    }}
                    actions={
                      <IconAction
                        label={t("hardwarePanel.removeNicKey", { nicKey })}
                        icon={Trash2}
                        destructive
                        onClick={() => {
                          handleRemoveDevice(nicKey);
                        }}
                      />
                    }
                    editor={
                      <>
                        <FieldGrid>
                          <Field label={t("hardwarePanel.model")}>
                            <select
                              className={fieldSelectClass}
                              value={nic.model}
                              onChange={(e) => {
                                updateNicEdit(nicKey, {
                                  model: e.target.value,
                                });
                              }}
                            >
                              <CurrentValueOption
                                value={storedNics[nicKey]?.model ?? ""}
                                known={netModels.map((m) => m.value)}
                              />
                              {netModels.map((m) => (
                                <option key={m.value} value={m.value}>
                                  {m.label}
                                </option>
                              ))}
                            </select>
                          </Field>
                          <Field label={t("hardwarePanel.bridge")}>
                            {bridgeNames.length > 0 ? (
                              <select
                                className={fieldSelectClass}
                                value={nic.bridge}
                                onChange={(e) => {
                                  updateNicEdit(nicKey, {
                                    bridge: e.target.value,
                                  });
                                }}
                              >
                                {/* No bridge is qemu's user-mode network,
                                    NATed, with its own DHCP and DNS ($net_fmt's
                                    bridge, src/PVE/QemuServer/Network.pm). */}
                                <option value="">
                                  {t("hardwarePanel.noBridgeNAT2")}
                                </option>
                                <CurrentValueOption
                                  value={storedNics[nicKey]?.bridge ?? ""}
                                  known={bridgeNames}
                                />
                                {/* One typed before the list arrived, neither
                                    listed nor stored, which the select would
                                    otherwise show as its first option. */}
                                {nic.bridge &&
                                  nic.bridge !== storedNics[nicKey]?.bridge &&
                                  !bridgeNames.includes(nic.bridge) && (
                                    <option value={nic.bridge}>
                                      {nic.bridge}
                                    </option>
                                  )}
                                {bridgeNames.map((b) => (
                                  <option key={b} value={b}>
                                    {b}
                                  </option>
                                ))}
                              </select>
                            ) : (
                              <Input
                                value={nic.bridge}
                                onChange={(e) => {
                                  updateNicEdit(nicKey, {
                                    bridge: e.target.value,
                                  });
                                }}
                                placeholder="vmbr0"
                                className={fieldInputClass}
                              />
                            )}
                          </Field>
                          <Field label={t("hardwarePanel.vlanTag")}>
                            <Input
                              type="number"
                              min={1}
                              max={4094}
                              value={nic.vlanTag}
                              onChange={(e) => {
                                updateNicEdit(nicKey, {
                                  vlanTag: e.target.value,
                                });
                              }}
                              placeholder={t("hardwarePanel.none")}
                              className={fieldInputClass}
                            />
                          </Field>
                          <Field label={t("hardwarePanel.rateLimitMBS")}>
                            <Input
                              type="number"
                              min={0}
                              value={nic.rateLimit}
                              onChange={(e) => {
                                updateNicEdit(nicKey, {
                                  rateLimit: e.target.value,
                                });
                              }}
                              placeholder={t("hardwarePanel.unlimited")}
                              className={fieldInputClass}
                            />
                          </Field>
                          <Field label={t("hardwarePanel.mtu")}>
                            <Input
                              type="number"
                              min={0}
                              value={nic.mtu}
                              onChange={(e) => {
                                updateNicEdit(nicKey, { mtu: e.target.value });
                              }}
                              placeholder={t("hardwarePanel.default")}
                              className={fieldInputClass}
                            />
                          </Field>
                        </FieldGrid>
                        <Checks>
                          <Check
                            id={`hw-${nicKey}-fw`}
                            label={t("hardwarePanel.firewall")}
                            checked={nic.firewall}
                            onCheckedChange={(v) => {
                              updateNicEdit(nicKey, { firewall: v === true });
                            }}
                          />
                          <Check
                            id={`hw-${nicKey}-linkdown`}
                            label={t("hardwarePanel.disconnect")}
                            checked={nic.linkDown}
                            onCheckedChange={(v) => {
                              updateNicEdit(nicKey, { linkDown: v === true });
                            }}
                          />
                        </Checks>
                      </>
                    }
                  />
                );
              })}
              {pendingRows(
                /^net\d+$/,
                Network,
                t("hardwarePanel.networkDevice"),
              )}
            </DeviceList>
          </Panel>

          {hasOtherDevices && (
            <Panel
              flush
              title={t("hardwarePanel.otherDevices")}
              aside={plural(otherDeviceCount, "device")}
            >
              <DeviceList>
                {usbKeys.map((key) => {
                  const parsed = parseUSB(str(config[key]));
                  if (deviceRemovals.has(key)) {
                    return (
                      <DeviceRow
                        key={key}
                        name={key}
                        icon={Usb}
                        title={t("hardwarePanel.usbDevice")}
                        struck
                        marker={removedMarker}
                        actions={undoButton(() => {
                          handleUndoRemoveDevice(key);
                        })}
                      />
                    );
                  }
                  // qemu-server's check_usb_perm refuses to change or delete a
                  // raw host passthrough for anyone but root@pam, and a Nexara
                  // token never is — so a Remove would fail, and take the rest of
                  // the Save down with it. The row says why in its place, as text:
                  // a disabled button's title never shows (Button takes no pointer
                  // events when disabled).
                  const rootOnly = isRawUSBPassthrough(parsed);
                  return (
                    <DeviceRow
                      key={key}
                      name={key}
                      icon={Usb}
                      title={t("hardwarePanel.usbDevice")}
                      summary={
                        <>
                          {parsed.spice ? (
                            <span>{t("hardwarePanel.spiceRedirection")}</span>
                          ) : parsed.mapping ? (
                            <>
                              <span>{t("hardwarePanel.mapped")}</span>
                              <span className="font-mono">
                                {parsed.mapping}
                              </span>
                            </>
                          ) : (
                            <span>{usbHostLabel(parsed.host, usbDevices)}</span>
                          )}
                          {parsed.usb3 && (
                            <span>{t("hardwarePanel.usb3")}</span>
                          )}
                          {rootOnly && (
                            <span>
                              {t("hardwarePanel.passedThroughDirectlyOnlyRoot")}
                            </span>
                          )}
                        </>
                      }
                      actions={
                        rootOnly ? undefined : (
                          <IconAction
                            label={t("hardwarePanel.removeKey", { key })}
                            icon={Trash2}
                            destructive
                            onClick={() => {
                              handleRemoveDevice(key);
                            }}
                          />
                        )
                      }
                    />
                  );
                })}
                {pendingRows(/^usb\d+$/, Usb, t("hardwarePanel.usbDevice"))}

                {pciKeys.map((key) => {
                  const parsed = parsePCI(str(config[key]));
                  if (deviceRemovals.has(key)) {
                    return (
                      <DeviceRow
                        key={key}
                        name={key}
                        icon={Cpu}
                        title={t("hardwarePanel.pciDevice")}
                        struck
                        marker={removedMarker}
                        actions={undoButton(() => {
                          handleUndoRemoveDevice(key);
                        })}
                      />
                    );
                  }
                  // check_hostpci_perm refuses to change or delete a host
                  // device, or a romfile, for anyone but root@pam — the USB rows'
                  // reason, and said the same way.
                  const rootOnly = isRootOnlyPCI(parsed);
                  return (
                    <DeviceRow
                      key={key}
                      name={key}
                      icon={Cpu}
                      title={t("hardwarePanel.pciDevice")}
                      summary={
                        <>
                          {parsed.mapping ? (
                            <>
                              <span>{t("hardwarePanel.mapped")}</span>
                              <span className="font-mono">
                                {parsed.mapping}
                              </span>
                            </>
                          ) : (
                            <span>{pciHostLabel(parsed.host, pciDevices)}</span>
                          )}
                          {parsed.pcie && (
                            <span>{t("hardwarePanel.pCIe")}</span>
                          )}
                          {parsed.xvga && <span>x-vga</span>}
                          {rootOnly && (
                            <span>
                              {parsed.host
                                ? t(
                                    "hardwarePanel.passedThroughDirectlyOnlyRoot",
                                  )
                                : t("hardwarePanel.usesAROMFileOnly")}
                            </span>
                          )}
                        </>
                      }
                      actions={
                        rootOnly ? undefined : (
                          <IconAction
                            label={t("hardwarePanel.removeKey", { key })}
                            icon={Trash2}
                            destructive
                            onClick={() => {
                              handleRemoveDevice(key);
                            }}
                          />
                        )
                      }
                    />
                  );
                })}
                {pendingRows(/^hostpci\d+$/, Cpu, t("hardwarePanel.pciDevice"))}

                {serialKeys.map((key) => {
                  const val = str(config[key]);
                  // $update_vm_api (src/PVE/API2/Qemu.pm) lets nobody but
                  // root@pam delete a serialN that is not a socket: the USB rows'
                  // reason, said the same way.
                  const rootOnly = val !== "socket";
                  if (deviceRemovals.has(key)) {
                    return (
                      <DeviceRow
                        key={key}
                        name={key}
                        icon={Terminal}
                        title={t("hardwarePanel.serialPort")}
                        struck
                        marker={removedMarker}
                        actions={undoButton(() => {
                          handleUndoRemoveDevice(key);
                        })}
                      />
                    );
                  }
                  return (
                    <DeviceRow
                      key={key}
                      name={key}
                      icon={Terminal}
                      title={t("hardwarePanel.serialPort")}
                      summary={
                        <>
                          <span className="font-mono">{val}</span>
                          {rootOnly && (
                            <span>
                              {t("hardwarePanel.aHostSerialPortOnly")}
                            </span>
                          )}
                        </>
                      }
                      actions={
                        rootOnly ? undefined : (
                          <IconAction
                            label={t("hardwarePanel.removeKey", { key })}
                            icon={Trash2}
                            destructive
                            onClick={() => {
                              handleRemoveDevice(key);
                            }}
                          />
                        )
                      }
                    />
                  );
                })}
                {pendingRows(
                  /^serial\d+$/,
                  Terminal,
                  t("hardwarePanel.serialPort"),
                )}

                {config["rng0"] != null && !deviceRemovals.has("rng0") ? (
                  (() => {
                    const rng = parseRNG(str(config["rng0"]));
                    return (
                      <DeviceRow
                        name="rng0"
                        icon={Dice1}
                        title={t("hardwarePanel.virtIORNG")}
                        summary={
                          <>
                            <span className="font-mono">{rng.source}</span>
                            {rng.maxBytes && (
                              <span>
                                {t("hardwarePanel.atMostMaxBytesBytes", {
                                  maxBytes: rng.maxBytes,
                                })}
                              </span>
                            )}
                            {rng.period && (
                              <span>
                                {t("hardwarePanel.everyPeriodMs", {
                                  period: rng.period,
                                })}
                              </span>
                            )}
                          </>
                        }
                        actions={
                          <IconAction
                            label={t("hardwarePanel.removeRng0")}
                            icon={Trash2}
                            destructive
                            onClick={() => {
                              handleRemoveDevice("rng0");
                            }}
                          />
                        }
                      />
                    );
                  })()
                ) : deviceRemovals.has("rng0") ? (
                  <DeviceRow
                    name="rng0"
                    icon={Dice1}
                    title={t("hardwarePanel.virtIORNG")}
                    struck
                    marker={removedMarker}
                    actions={undoButton(() => {
                      handleUndoRemoveDevice("rng0");
                    })}
                  />
                ) : pendingDeviceAdds["rng0"] ? (
                  <DeviceRow
                    name="rng0"
                    icon={Dice1}
                    title={t("hardwarePanel.virtIORNG")}
                    marker={
                      <PendingBadge>
                        {t("hardwarePanel.addedOnSave")}
                      </PendingBadge>
                    }
                    summary={
                      <span className="break-all font-mono">
                        {pendingDeviceAdds["rng0"]}
                      </span>
                    }
                    actions={cancelButton(() => {
                      handleCancelPendingDevice("rng0");
                    })}
                  />
                ) : null}

                {virtiofsKeys.map((key) => {
                  const parsed = parseVirtioFS(str(config[key]));
                  if (deviceRemovals.has(key)) {
                    return (
                      <DeviceRow
                        key={key}
                        name={key}
                        icon={FolderOpen}
                        title={t("hardwarePanel.virtioFSShare")}
                        struck
                        marker={removedMarker}
                        actions={undoButton(() => {
                          handleUndoRemoveDevice(key);
                        })}
                      />
                    );
                  }
                  return (
                    <DeviceRow
                      key={key}
                      name={key}
                      icon={FolderOpen}
                      title={t("hardwarePanel.virtioFSShare")}
                      summary={
                        <>
                          <span className="font-mono">{parsed.dirid}</span>
                          <span>
                            {t("hardwarePanel.cacheCache", {
                              cache: parsed.cache,
                            })}
                          </span>
                          {parsed.directIo && (
                            <span>{t("hardwarePanel.directIO")}</span>
                          )}
                        </>
                      }
                      actions={
                        <IconAction
                          label={t("hardwarePanel.removeKey", { key })}
                          icon={Trash2}
                          destructive
                          onClick={() => {
                            handleRemoveDevice(key);
                          }}
                        />
                      }
                    />
                  );
                })}
                {pendingRows(
                  /^virtiofs\d+$/,
                  FolderOpen,
                  t("hardwarePanel.virtiofsShare"),
                )}
              </DeviceList>
            </Panel>
          )}

          <Panel flush title={t("hardwarePanel.options")}>
            <GroupList>
              <Group title={t("hardwarePanel.guest")}>
                <FieldGrid>
                  <Field
                    label={t("hardwarePanel.osType")}
                    className="col-span-2"
                  >
                    <select
                      className={fieldSelectClass}
                      value={ostype}
                      onChange={(e) => {
                        setOstype(e.target.value);
                      }}
                    >
                      <CurrentValueOption
                        value={storedSelects.ostype}
                        known={osTypes.map((o) => o.value)}
                      />
                      {osTypes.map((o) => (
                        <option key={o.value} value={o.value}>
                          {o.label}
                        </option>
                      ))}
                    </select>
                  </Field>
                </FieldGrid>
                <Checks>
                  <Check
                    id="hw-agent"
                    label={t("hardwarePanel.agent")}
                    checked={agentEnabled}
                    onCheckedChange={(v) => {
                      setAgentEnabled(v === true);
                    }}
                  />
                  {agentEnabled && (
                    <Check
                      id="hw-agent-fstrim"
                      label={t("hardwarePanel.fstrim")}
                      checked={agentFstrim}
                      onCheckedChange={(v) => {
                        setAgentFstrim(v === true);
                      }}
                    />
                  )}
                  <Check
                    id="hw-tablet"
                    label={t("hardwarePanel.tablet")}
                    checked={tablet}
                    onCheckedChange={(v) => {
                      setTablet(v === true);
                    }}
                  />
                  <Check
                    id="hw-localtime"
                    label={t("hardwarePanel.localTime")}
                    checked={localtime}
                    onCheckedChange={(v) => {
                      setLocaltime(v === true);
                    }}
                  />
                </Checks>
              </Group>

              <Group title={t("hardwarePanel.virtualization")}>
                <Checks>
                  <Check
                    id="hw-kvm"
                    label={t("hardwarePanel.kvm")}
                    checked={kvmEnabled}
                    onCheckedChange={(v) => {
                      setKvmEnabled(v === true);
                    }}
                  />
                  <Check
                    id="hw-acpi"
                    label={t("hardwarePanel.acpi")}
                    checked={acpi}
                    onCheckedChange={(v) => {
                      setAcpi(v === true);
                    }}
                  />
                </Checks>
                <div className="space-y-1.5">
                  <p className="text-[12.5px] text-muted-foreground">
                    {t("hardwarePanel.hotplug")}
                  </p>
                  <Checks>
                    {(["network", "disk", "usb", "memory", "cpu"] as const).map(
                      (opt) => {
                        const parts = hotplug
                          ? hotplug.split(",").filter(Boolean)
                          : [];
                        const checked = parts.includes(opt);
                        return (
                          <Check
                            key={opt}
                            id={`hp-${opt}`}
                            label={HOTPLUG_LABELS[opt]}
                            checked={checked}
                            onCheckedChange={(v) => {
                              const next =
                                v === true
                                  ? [...parts, opt]
                                  : parts.filter((p) => p !== opt);
                              setHotplug(next.join(","));
                            }}
                          />
                        );
                      },
                    )}
                  </Checks>
                </div>
              </Group>

              <Group
                title={t("hardwarePanel.startup")}
                summary={
                  onboot
                    ? t("hardwarePanel.startsWithTheNode")
                    : t("hardwarePanel.startedByHand")
                }
              >
                <Checks>
                  <Check
                    id="hw-onboot"
                    label={t("hardwarePanel.startAtBoot")}
                    checked={onboot}
                    onCheckedChange={(v) => {
                      setOnboot(v === true);
                    }}
                  />
                  <Check
                    id="hw-protection"
                    label={t("hardwarePanel.protection")}
                    checked={protection}
                    onCheckedChange={(v) => {
                      setProtection(v === true);
                    }}
                  />
                </Checks>
                <FieldGrid>
                  <Field label={t("hardwarePanel.startupOrder")}>
                    <Input
                      type="number"
                      min={0}
                      value={startupOrder}
                      onChange={(e) => {
                        setStartupOrder(e.target.value);
                      }}
                      placeholder="#"
                      className={fieldInputClass}
                    />
                  </Field>
                  <Field label={t("hardwarePanel.upDelayS")}>
                    <Input
                      type="number"
                      min={0}
                      value={startupUp}
                      onChange={(e) => {
                        setStartupUp(e.target.value);
                      }}
                      placeholder="0"
                      className={fieldInputClass}
                    />
                  </Field>
                  <Field label={t("hardwarePanel.downDelayS")}>
                    <Input
                      type="number"
                      min={0}
                      value={startupDown}
                      onChange={(e) => {
                        setStartupDown(e.target.value);
                      }}
                      placeholder="0"
                      className={fieldInputClass}
                    />
                  </Field>
                </FieldGrid>
              </Group>
            </GroupList>

            <DisclosureRow
              open={advancedOpen}
              onToggle={() => {
                setAdvancedOpen((open) => !open);
              }}
            >
              {t("hardwarePanel.advancedOptions")}
            </DisclosureRow>
            {advancedOpen && (
              <GroupList>
                <Group title={t("hardwarePanel.clockAndKeyboard")}>
                  <FieldGrid>
                    <Field label={t("hardwarePanel.rtcStartDate")}>
                      <Input
                        value={startdate}
                        onChange={(e) => {
                          setStartdate(e.target.value);
                        }}
                        placeholder="now"
                        className={fieldInputClass}
                      />
                    </Field>
                    <Field label={t("hardwarePanel.keyboardLayout")}>
                      <select
                        className={fieldSelectClass}
                        value={keyboard}
                        onChange={(e) => {
                          setKeyboard(e.target.value);
                        }}
                      >
                        <option value="">
                          {t("hardwarePanel.defaultGuestHandled")}
                        </option>
                        <CurrentValueOption
                          value={storedSelects.keyboard}
                          known={keyboardLayouts}
                        />
                        {keyboardLayouts.map((k) => (
                          <option key={k} value={k}>
                            {k}
                          </option>
                        ))}
                      </select>
                    </Field>
                  </FieldGrid>
                  <Checks>
                    <Check
                      id="hw-tdf"
                      label={t("hardwarePanel.timeDriftFix")}
                      checked={tdf}
                      onCheckedChange={(v) => {
                        setTdf(v === true);
                      }}
                    />
                  </Checks>
                </Group>

                <Group title={t("hardwarePanel.migrationAndSavedState")}>
                  <FieldGrid>
                    <Field
                      label={t("hardwarePanel.vmStateStorage")}
                      className="col-span-2"
                    >
                      <select
                        className={fieldSelectClass}
                        value={vmstatestorage}
                        onChange={(e) => {
                          setVmstatestorage(e.target.value);
                        }}
                      >
                        <option value="">{t("hardwarePanel.automatic")}</option>
                        <CurrentValueOption
                          value={storedSelects.vmstatestorage}
                          known={diskStorages.map((st) => st.storage)}
                        />
                        {diskStorages.map((st) => (
                          <option key={st.id} value={st.storage}>
                            {st.storage} ({st.type})
                          </option>
                        ))}
                      </select>
                    </Field>
                    <Field label={t("hardwarePanel.migrateDowntimeS")}>
                      <Input
                        type="number"
                        min={0}
                        step={0.1}
                        value={migrateDowntime}
                        onChange={(e) => {
                          setMigrateDowntime(e.target.value);
                        }}
                        placeholder={t("hardwarePanel.migrateDowntimeDefault")}
                        className={fieldInputClass}
                      />
                    </Field>
                    <Field label={t("hardwarePanel.migrateSpeedMBS")}>
                      <Input
                        type="number"
                        min={0}
                        value={migrateSpeed}
                        onChange={(e) => {
                          setMigrateSpeed(e.target.value);
                        }}
                        placeholder={t("hardwarePanel.zeroUnlimited")}
                        className={fieldInputClass}
                      />
                    </Field>
                  </FieldGrid>
                </Group>

                <Group title={t("hardwarePanel.powerAndWatchdog")}>
                  <Checks>
                    <Check
                      id="hw-reboot"
                      label={t("hardwarePanel.allowReboot")}
                      checked={reboot}
                      onCheckedChange={(v) => {
                        setReboot(v === true);
                      }}
                    />
                    <Check
                      id="hw-freeze"
                      label={t("hardwarePanel.freezeCPUAtStartup")}
                      checked={freeze}
                      onCheckedChange={(v) => {
                        setFreeze(v === true);
                      }}
                    />
                  </Checks>
                  <FieldGrid>
                    <Field label={t("hardwarePanel.watchdogDevice")}>
                      <select
                        className={fieldSelectClass}
                        value={watchdogModel}
                        aria-label={t("hardwarePanel.watchdogDevice")}
                        onChange={(e) => {
                          setWatchdogModel(e.target.value);
                        }}
                      >
                        <option value="">{t("hardwarePanel.none")}</option>
                        <CurrentValueOption
                          value={storedSelects.watchdogModel}
                          known={watchdogModels.map((m) => m.value)}
                        />
                        {watchdogModels.map((m) => (
                          <option key={m.value} value={m.value}>
                            {m.label}
                          </option>
                        ))}
                      </select>
                    </Field>
                    <Field label={t("hardwarePanel.watchdogAction")}>
                      <select
                        className={fieldSelectClass}
                        value={watchdogAction}
                        aria-label={t("hardwarePanel.watchdogAction")}
                        disabled={!watchdogModel}
                        onChange={(e) => {
                          setWatchdogAction(e.target.value);
                        }}
                      >
                        <CurrentValueOption
                          value={storedSelects.watchdogAction}
                          known={watchdogActions.map((a) => a.value)}
                        />
                        {watchdogActions.map((a) => (
                          <option key={a.value} value={a.value}>
                            {a.label}
                          </option>
                        ))}
                      </select>
                    </Field>
                  </FieldGrid>
                </Group>

                <Group
                  title={t("hardwarePanel.smbios")}
                  summary={t("hardwarePanel.type1WhatTheGuest")}
                >
                  <Field
                    label={t("hardwarePanel.uuid")}
                    note={
                      smbiosUuidChanged && (
                        <p className="mt-1.5 flex items-start gap-1.5 text-[12.5px] leading-snug text-amber-700 dark:text-amber-400">
                          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                          <span>
                            {smbiosUuid
                              ? t("hardwarePanel.changingTheSMBIOSUUID")
                              : t("hardwarePanel.clearingThisRemovesTheSMBIOS")}
                          </span>
                        </p>
                      )
                    }
                  >
                    <Input
                      value={smbiosUuid}
                      onChange={(e) => {
                        setSmbiosUuid(e.target.value);
                      }}
                      placeholder={
                        originalSmbiosUuid
                          ? originalSmbiosUuid
                          : t("hardwarePanel.autoGeneratedOnFirstBoot")
                      }
                      className={cn(fieldInputClass, "font-mono")}
                    />
                  </Field>
                  <FieldGrid>
                    {SMBIOS_TEXT_FIELDS.map((field) => (
                      <Field key={field} label={SMBIOS_LABELS[field]}>
                        <Input
                          value={smbios[field]}
                          onChange={(e) => {
                            setSmbios((prev) => ({
                              ...prev,
                              [field]: e.target.value,
                            }));
                          }}
                          className={fieldInputClass}
                        />
                      </Field>
                    ))}
                  </FieldGrid>
                </Group>
              </GroupList>
            )}
          </Panel>
        </div>

        <div className="flex min-w-0 flex-[2_1_340px] flex-col gap-4">
          <Panel flush title={t("hardwarePanel.displayAndBoot")}>
            <GroupList>
              <Group title={t("hardwarePanel.display")}>
                <FieldGrid>
                  <Field label={t("hardwarePanel.graphicsCard")}>
                    <select
                      className={fieldSelectClass}
                      value={vgaType}
                      onChange={(e) => {
                        setVgaType(e.target.value);
                      }}
                    >
                      {/* No type set: no vga line, or one with only other options
                          (memory=32, clipboard=vnc). qemu-server picks the type
                          when the VM starts (get_vga_properties,
                          src/PVE/QemuServer.pm): virtio on aarch64; cirrus for
                          ostype w2k, wxp and w2k3 and, on a machine version below
                          2.9, for every guest but Windows Vista/2008 and newer; std
                          otherwise. Not always std, so it is not shown as std:
                          Proxmox's own GUI calls it "Default" too (kvm_vga_drivers,
                          pve-manager www/manager6/Utils.js). */}
                      <option value="">{t("hardwarePanel.default")}</option>
                      <CurrentValueOption
                        value={storedVgaType}
                        known={vgaTypes.map((v) => v.value)}
                      />
                      {vgaTypes.map((v) => (
                        <option key={v.value} value={v.value}>
                          {v.label}
                        </option>
                      ))}
                    </select>
                  </Field>
                  {vgaType === "qxl" && (
                    <Field label={t("hardwarePanel.qxlMemoryMiB")}>
                      <Input
                        type="number"
                        min={0}
                        value={vgaMemory}
                        onChange={(e) => {
                          setVgaMemory(e.target.value);
                        }}
                        placeholder={t("hardwarePanel.default")}
                        className={fieldInputClass}
                      />
                    </Field>
                  )}
                  <Field label={t("hardwarePanel.audio")}>
                    <select
                      className={fieldSelectClass}
                      value={audioDevice}
                      onChange={(e) => {
                        setAudioDevice(e.target.value);
                      }}
                    >
                      <CurrentValueOption
                        value={storedSelects.audio}
                        known={audioDevices.map((a) => a.value)}
                      />
                      {audioDevices.map((a) => (
                        <option key={a.value} value={a.value}>
                          {a.label}
                        </option>
                      ))}
                    </select>
                  </Field>
                </FieldGrid>
              </Group>

              <Group title={t("hardwarePanel.firmwareAndChipset")}>
                <FieldGrid>
                  <Field label={t("hardwarePanel.bios")}>
                    <select
                      className={fieldSelectClass}
                      value={bios}
                      onChange={(e) => {
                        setBios(e.target.value);
                      }}
                    >
                      <CurrentValueOption
                        value={storedSelects.bios}
                        known={biosOptions.map((o) => o.value)}
                      />
                      {biosOptions.map((o) => (
                        <option key={o.value} value={o.value}>
                          {o.label}
                        </option>
                      ))}
                    </select>
                  </Field>
                  <Field
                    label={t("hardwarePanel.machine")}
                    note={
                      /* Saved, buildMachine takes it off: Proxmox refuses an
                         Intel vIOMMU beside a type without q35 in it. */
                      dropsIntelViommu(
                        machine,
                        str(original?.["machine"] ?? ""),
                      ) && (
                        <p className="mt-1.5 text-[12.5px] leading-snug text-amber-700 dark:text-amber-400">
                          {t("hardwarePanel.thisVMSIntelVIOMMU")}
                        </p>
                      )
                    }
                  >
                    <select
                      className={fieldSelectClass}
                      value={machine}
                      onChange={(e) => {
                        setMachine(e.target.value);
                      }}
                    >
                      <CurrentValueOption
                        value={storedSelects.machine}
                        known={machineTypes.map((o) => o.value)}
                      />
                      {machineTypes.map((o) => (
                        <option key={o.value} value={o.value}>
                          {o.label}
                        </option>
                      ))}
                    </select>
                  </Field>
                  <Field
                    label={t("hardwarePanel.scsiController")}
                    className="col-span-full"
                  >
                    <select
                      className={fieldSelectClass}
                      value={scsihw}
                      onChange={(e) => {
                        setScsihw(e.target.value);
                      }}
                    >
                      <CurrentValueOption
                        value={storedSelects.scsihw}
                        known={scsiControllers.map((o) => o.value)}
                      />
                      {scsiControllers.map((o) => (
                        <option key={o.value} value={o.value}>
                          {o.label}
                        </option>
                      ))}
                    </select>
                  </Field>
                </FieldGrid>
              </Group>

              <Group
                title={t("hardwarePanel.bootOrder")}
                summary={
                  bootOrder.length === 0
                    ? undefined
                    : t("hardwarePanel.bootTried", {
                        enabled: String(bootEnabled),
                        devices: plural(bootOrder.length, "device"),
                      })
                }
              >
                {bootOrder.length === 0 && (
                  <p className="text-sm text-muted-foreground">
                    {t("hardwarePanel.noBootableDevices")}
                  </p>
                )}
                {legacyBoot !== null && bootOrder.length > 0 && (
                  <p className="pb-1 text-[12.5px] text-muted-foreground">
                    {original?.["boot"] == null
                      ? t("bootLegacyDefault")
                      : t("bootLegacy", { value: legacyBoot })}
                  </p>
                )}
                <ol className="-mx-1.5 space-y-0.5">
                  {bootOrder.map((entry, idx) => {
                    const info = bootDeviceLabel(entry.device, config);
                    const DeviceIcon =
                      info.type === "net"
                        ? Network
                        : info.type === "cdrom"
                          ? Disc
                          : HardDrive;
                    return (
                      <li
                        key={entry.device}
                        className={cn(
                          "flex items-center gap-2 rounded-md px-1.5 py-1 text-sm",
                          !entry.enabled && "text-muted-foreground",
                        )}
                      >
                        <Checkbox
                          id={`boot-${entry.device}`}
                          checked={entry.enabled}
                          onCheckedChange={(v) => {
                            setBootOrder((prev) =>
                              prev.map((b, i) =>
                                i === idx ? { ...b, enabled: v === true } : b,
                              ),
                            );
                          }}
                        />
                        <DeviceIcon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                        <span className="min-w-0 flex-1 truncate">
                          {info.label}
                        </span>
                        <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
                          {String(idx + 1)}
                        </span>
                        <IconAction
                          label={t("hardwarePanel.moveLabelEarlierInThe", {
                            label: info.label,
                          })}
                          icon={ArrowUp}
                          className="h-6 w-6"
                          disabled={idx === 0}
                          onClick={() => {
                            setBootOrder((prev) => {
                              const next = [...prev];
                              const a = next[idx];
                              const b = next[idx - 1];
                              if (a && b) {
                                next[idx] = b;
                                next[idx - 1] = a;
                              }
                              return next;
                            });
                          }}
                        />
                        <IconAction
                          label={t("hardwarePanel.moveLabelLaterInThe", {
                            label: info.label,
                          })}
                          icon={ArrowDown}
                          className="h-6 w-6"
                          disabled={idx === bootOrder.length - 1}
                          onClick={() => {
                            setBootOrder((prev) => {
                              const next = [...prev];
                              const a = next[idx];
                              const b = next[idx + 1];
                              if (a && b) {
                                next[idx] = b;
                                next[idx + 1] = a;
                              }
                              return next;
                            });
                          }}
                        />
                      </li>
                    );
                  })}
                </ol>
              </Group>
            </GroupList>
          </Panel>

          <Panel title={t("hardwarePanel.descriptionAndTags")}>
            <div className="space-y-3">
              <Field label={t("hardwarePanel.description")}>
                <textarea
                  value={description}
                  onChange={(e) => {
                    setDescription(e.target.value);
                  }}
                  rows={3}
                  className="flex w-full rounded-md border border-input bg-transparent px-2.5 py-1.5 text-sm placeholder:text-muted-foreground focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring"
                />
              </Field>
              <Field label={t("hardwarePanel.tags")}>
                <Input
                  value={tags}
                  onChange={(e) => {
                    setTags(e.target.value);
                  }}
                  placeholder={t("hardwarePanel.semicolonSeparated")}
                  className={fieldInputClass}
                />
              </Field>
            </div>
          </Panel>
        </div>
      </div>

      {/* Held open while the delete is in flight: Cancel cannot un-send it,
          and closing would hide whether it worked. That also keeps one
          delete in flight at a time, so a second mutate() cannot detach the
          first one's onSuccess. */}
      <AlertDialog
        open={unusedToDelete !== null}
        onOpenChange={(open) => {
          if (!open && !detachDisk.isPending) closeUnusedDelete();
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("hardwarePanel.deleteUnusedDiskKey", {
                key: unusedToDelete?.key,
              })}
            </AlertDialogTitle>
            <AlertDialogDescription>
              <Trans
                t={t}
                i18nKey="hardwarePanel.thisWillPermanentlyDeleteSpan"
                components={{
                  volume: (
                    <span className="font-mono font-semibold">
                      {unusedToDelete?.volume}
                    </span>
                  ),
                }}
              />
            </AlertDialogDescription>
          </AlertDialogHeader>
          {/* Always shown, not gated on hasChanges: the sentence costs
              nothing when there is nothing to lose, and a warning that is
              always there is simpler than one that depends on a check. */}
          <p className="text-sm text-amber-700 dark:text-amber-400">
            {t("hardwarePanel.deletingReloadsThisVMS")}
          </p>
          {detachDisk.isError && (
            <p className="text-sm text-destructive">
              {detachDisk.error.message}
            </p>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={detachDisk.isPending}>
              {t("hardwarePanel.cancel")}
            </AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              disabled={detachDisk.isPending}
              onClick={(e) => {
                // Stays open until the outcome is known: success closes it,
                // a failure stays on screen.
                e.preventDefault();
                if (!unusedToDelete) return;
                settleDelete(
                  detachDisk.mutateAsync({
                    clusterId,
                    vmId,
                    disk: unusedToDelete.key,
                  }),
                  {
                    action: t("hardwarePanel.deletingUnusedDiskKey", {
                      key: unusedToDelete.key,
                    }),
                    onSuccess: () => {
                      // The delete changed the config, so the refetch
                      // re-populates every field and the unsaved edits are
                      // gone, as the dialog said. The staged changes go at
                      // once, as the populate effect drops them when that
                      // refetch lands, so none is sent in between.
                      clearStagedChanges();
                      closeUnusedDelete();
                    },
                    onError: () => undefined, // shown below from the mutation's state
                  },
                );
              }}
            >
              {detachDisk.isPending
                ? t("hardwarePanel.deleting")
                : t("hardwarePanel.deleteDisk")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        open={cloudInitToRemove !== null}
        onOpenChange={(open) => {
          if (!open) setCloudInitToRemove(null);
        }}
      >
        <AlertDialogContent fallbackFocus={() => cdromList.current}>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("hardwarePanel.removeCloudInitDriveKey", {
                key: cloudInitToRemove?.key,
              })}
            </AlertDialogTitle>
            <AlertDialogDescription>
              <Trans
                t={t}
                i18nKey="hardwarePanel.whenTheRemovalAppliesOn"
                components={{
                  volume: (
                    <span className="font-mono font-semibold">
                      {cloudInitToRemove?.volume}
                    </span>
                  ),
                }}
              />
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("hardwarePanel.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              onClick={() => {
                if (cloudInitToRemove) handleRemoveCDROM(cloudInitToRemove.key);
              }}
            >
              {t("hardwarePanel.removeDrive")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <SaveBar
        dirty={hasChanges}
        saving={setConfigMutation.isPending}
        disabled={!hasChanges || memoryMissing || setConfigMutation.isPending}
        onSave={handleSave}
        hideIdle={
          setConfigMutation.isError ||
          setConfigMutation.isSuccess ||
          resizeErrors.length > 0 ||
          resizeMutation.isSuccess
        }
      >
        {setConfigMutation.isError && (
          <span className="text-destructive">
            {setConfigMutation.error.message}
          </span>
        )}
        {setConfigMutation.isSuccess && (
          <span className="text-muted-foreground">
            {isRunning
              ? t("hardwarePanel.savedSomeChangesNeedARestart")
              : t("hardwarePanel.saved")}
          </span>
        )}
        {resizeErrors.map((msg, i) => (
          <span key={i} className="text-destructive">
            {t("hardwarePanel.resizeFailedMsg", { msg })}
          </span>
        ))}
        {resizeMutation.isSuccess && (
          <span className="text-muted-foreground">
            {t("hardwarePanel.diskResizedSuccessfully")}
          </span>
        )}
        {/* Said here too: the Memory field may be a screen away from Save. */}
        {memoryMissing && (
          <span className="text-destructive">
            {t("hardwarePanel.memoryIsEmptySoSave")}
          </span>
        )}
      </SaveBar>
    </div>
  );
}
