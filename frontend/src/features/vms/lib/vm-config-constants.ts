/** Shared VM configuration option arrays used by CreateVMDialog and HardwarePanel. */

import i18n from "@/lib/i18n";

export const osTypes = [
  {
    value: "l26",
    get label() {
      return i18n.t("vms:vmConfigConstants.linux26");
    },
  },
  {
    value: "l24",
    get label() {
      return i18n.t("vms:vmConfigConstants.linux24");
    },
  },
  {
    value: "win11",
    get label() {
      return i18n.t("vms:vmConfigConstants.windows112022");
    },
  },
  {
    value: "win10",
    get label() {
      return i18n.t("vms:vmConfigConstants.windows1020162019");
    },
  },
  {
    value: "win8",
    get label() {
      return i18n.t("vms:vmConfigConstants.windows82012");
    },
  },
  {
    value: "win7",
    get label() {
      return i18n.t("vms:vmConfigConstants.windows72008r2");
    },
  },
  {
    value: "wvista",
    get label() {
      return i18n.t("vms:vmConfigConstants.windowsVista2008");
    },
  },
  {
    value: "w2k3",
    get label() {
      return i18n.t("vms:vmConfigConstants.windows2003");
    },
  },
  {
    value: "w2k8",
    get label() {
      return i18n.t("vms:vmConfigConstants.windows2008");
    },
  },
  {
    value: "wxp",
    get label() {
      return i18n.t("vms:vmConfigConstants.windowsXP2003");
    },
  },
  {
    value: "w2k",
    get label() {
      return i18n.t("vms:vmConfigConstants.windows2000");
    },
  },
  {
    value: "solaris",
    get label() {
      return i18n.t("vms:vmConfigConstants.solaris");
    },
  },
  {
    value: "other",
    get label() {
      return i18n.t("vms:vmConfigConstants.other");
    },
  },
] as const;

export const cpuTypes = [
  // Generic / virtual
  "x86-64-v2-AES",
  "x86-64-v2",
  "x86-64-v3",
  "x86-64-v4",
  "host",
  "max",
  "kvm64",
  "kvm32",
  "qemu64",
  "qemu32",
  // Intel — newest first
  "GraniteRapids",
  "SapphireRapids-v2",
  "SapphireRapids",
  "Cooperlake-v2",
  "Cooperlake",
  "Icelake-Server-v6",
  "Icelake-Server-v5",
  "Icelake-Server-v4",
  "Icelake-Server-v3",
  "Icelake-Server-noTSX",
  "Icelake-Server",
  "Icelake-Client-noTSX",
  "Icelake-Client",
  "Cascadelake-Server-v5",
  "Cascadelake-Server-v4",
  "Cascadelake-Server-v2",
  "Cascadelake-Server-noTSX",
  "Cascadelake-Server",
  "Skylake-Server-v5",
  "Skylake-Server-v4",
  "Skylake-Server-noTSX-IBRS",
  "Skylake-Server-IBRS",
  "Skylake-Server",
  "Skylake-Client-v4",
  "Skylake-Client-noTSX-IBRS",
  "Skylake-Client-IBRS",
  "Skylake-Client",
  "Broadwell-IBRS",
  "Broadwell-noTSX-IBRS",
  "Broadwell-noTSX",
  "Broadwell",
  "Haswell-IBRS",
  "Haswell-noTSX-IBRS",
  "Haswell-noTSX",
  "Haswell",
  "IvyBridge-IBRS",
  "IvyBridge",
  "SandyBridge-IBRS",
  "SandyBridge",
  "Westmere-IBRS",
  "Westmere",
  "Nehalem-IBRS",
  "Nehalem",
  "Penryn",
  "Conroe",
  "core2duo",
  "coreduo",
  "KnightsMill",
  // AMD
  "EPYC-Genoa",
  "EPYC-Milan-v2",
  "EPYC-Milan",
  "EPYC-Rome-v4",
  "EPYC-Rome-v3",
  "EPYC-Rome-v2",
  "EPYC-Rome",
  "EPYC-v4",
  "EPYC-v3",
  "EPYC-IBPB",
  "EPYC",
  "Opteron_G5",
  "Opteron_G4",
  "Opteron_G3",
  "Opteron_G2",
  "Opteron_G1",
  "phenom",
  "athlon",
  // Legacy
  "pentium3",
  "pentium2",
  "pentium",
  "486",
] as const;

/**
 * Fallback list of VM-specific CPU flags, used only when a cluster's Proxmox
 * is too old to serve /nodes/{node}/capabilities/qemu/cpu-flags (it answers
 * 501 and the handler returns an empty list). Mirrors the curated set in
 * PVE's QemuServer/CPUFlags.pm; the live endpoint is authoritative and also
 * reports per-node support, which this list cannot.
 */
export const cpuFlags: { name: string; description: string }[] = [
  {
    name: "nested-virt",
    get description() {
      return i18n.t("vms:vmConfigConstants.controlsNestedVirtualizationSvmOn");
    },
  },
  {
    name: "aes",
    get description() {
      return i18n.t("vms:vmConfigConstants.activateAESInstructionSetFor");
    },
  },
  {
    name: "amd-no-ssb",
    get description() {
      return i18n.t("vms:vmConfigConstants.notifiesGuestOSThatHost");
    },
  },
  {
    name: "amd-ssbd",
    get description() {
      return i18n.t(
        "vms:vmConfigConstants.improvesSpectreMitigationPerformanceWith",
      );
    },
  },
  {
    name: "hv-evmcs",
    get description() {
      return i18n.t(
        "vms:vmConfigConstants.improvePerformanceForNestedVirtualization",
      );
    },
  },
  {
    name: "hv-tlbflush",
    get description() {
      return i18n.t(
        "vms:vmConfigConstants.improvePerformanceInOvercommittedWindows",
      );
    },
  },
  {
    name: "ibpb",
    get description() {
      return i18n.t(
        "vms:vmConfigConstants.allowsImprovedSpectreMitigationWith",
      );
    },
  },
  {
    name: "md-clear",
    get description() {
      return i18n.t("vms:vmConfigConstants.requiredToLetTheGuest");
    },
  },
  {
    name: "pcid",
    get description() {
      return i18n.t("vms:vmConfigConstants.meltdownFixCostReductionOn");
    },
  },
  {
    name: "pdpe1gb",
    get description() {
      return i18n.t("vms:vmConfigConstants.allowGuestOSToUse");
    },
  },
  {
    name: "spec-ctrl",
    get description() {
      return i18n.t(
        "vms:vmConfigConstants.allowsImprovedSpectreMitigationWith2",
      );
    },
  },
  {
    name: "ssbd",
    get description() {
      return i18n.t(
        "vms:vmConfigConstants.protectionForSpeculativeStoreBypass",
      );
    },
  },
  {
    name: "virt-ssbd",
    get description() {
      return i18n.t("vms:vmConfigConstants.basisForSpeculativeStoreBypass");
    },
  },
];

/** watchdog model — PVE's $watchdog_fmt, default i6300esb. */
export const watchdogModels = [
  {
    value: "i6300esb",
    get label() {
      return i18n.t("vms:vmConfigConstants.intel6300ESBDefault");
    },
  },
  { value: "ib700", label: "iBASE 700" },
];

/** watchdog action taken when the guest stops polling the device. */
export const watchdogActions = [
  {
    value: "",
    get label() {
      return i18n.t("vms:vmConfigConstants.defaultNone");
    },
  },
  {
    value: "reset",
    get label() {
      return i18n.t("vms:vmConfigConstants.reset");
    },
  },
  {
    value: "shutdown",
    get label() {
      return i18n.t("vms:vmConfigConstants.shutdown");
    },
  },
  {
    value: "poweroff",
    get label() {
      return i18n.t("vms:vmConfigConstants.powerOff");
    },
  },
  {
    value: "pause",
    get label() {
      return i18n.t("vms:vmConfigConstants.pause");
    },
  },
  {
    value: "debug",
    get label() {
      return i18n.t("vms:vmConfigConstants.debug");
    },
  },
  {
    value: "none",
    get label() {
      return i18n.t("vms:vmConfigConstants.none");
    },
  },
];

/** VNC server keyboard layout. Proxmox's enum, in its own order. */
export const keyboardLayouts = [
  "de",
  "de-ch",
  "da",
  "en-gb",
  "en-us",
  "es",
  "fi",
  "fr",
  "fr-be",
  "fr-ca",
  "fr-ch",
  "hu",
  "is",
  "it",
  "ja",
  "lt",
  "mk",
  "nl",
  "no",
  "pl",
  "pt",
  "pt-br",
  "sv",
  "sl",
  "tr",
];

export const scsiControllers = [
  {
    value: "virtio-scsi-pci",
    get label() {
      return i18n.t("vms:vmConfigConstants.virtIOSCSI");
    },
  },
  {
    value: "virtio-scsi-single",
    get label() {
      return i18n.t("vms:vmConfigConstants.virtIOSCSISingle");
    },
  },
  { value: "lsi", label: "LSI 53C895A" },
  { value: "lsi53c810", label: "LSI 53C810" },
  {
    value: "megasas",
    get label() {
      return i18n.t("vms:vmConfigConstants.megaRAIDSAS");
    },
  },
  {
    value: "pvscsi",
    get label() {
      return i18n.t("vms:vmConfigConstants.vMwarePVSCSI");
    },
  },
] as const;

export const netModels = [
  {
    value: "virtio",
    get label() {
      return i18n.t("vms:vmConfigConstants.virtIOParavirt");
    },
  },
  {
    value: "e1000",
    get label() {
      return i18n.t("vms:vmConfigConstants.intelE1000");
    },
  },
  {
    value: "e1000e",
    get label() {
      return i18n.t("vms:vmConfigConstants.intelE1000E");
    },
  },
  {
    value: "vmxnet3",
    get label() {
      return i18n.t("vms:vmConfigConstants.vMwareVmxnet3");
    },
  },
  {
    value: "rtl8139",
    get label() {
      return i18n.t("vms:vmConfigConstants.realtekRTL8139");
    },
  },
] as const;

export const diskFormats = [
  {
    value: "qcow2",
    get label() {
      return i18n.t("vms:vmConfigConstants.qemuImageQcow2");
    },
  },
  {
    value: "raw",
    get label() {
      return i18n.t("vms:vmConfigConstants.rawDiskImageRaw");
    },
  },
  {
    value: "vmdk",
    get label() {
      return i18n.t("vms:vmConfigConstants.vMwareImageVmdk");
    },
  },
] as const;

export const cacheModes = [
  {
    value: "none",
    get label() {
      return i18n.t("vms:vmConfigConstants.noCacheNone");
    },
  },
  {
    value: "directsync",
    get label() {
      return i18n.t("vms:vmConfigConstants.directSync");
    },
  },
  {
    value: "writethrough",
    get label() {
      return i18n.t("vms:vmConfigConstants.writeThrough");
    },
  },
  {
    value: "writeback",
    get label() {
      return i18n.t("vms:vmConfigConstants.writeBackUnsafe");
    },
  },
  {
    value: "unsafe",
    get label() {
      return i18n.t("vms:vmConfigConstants.unsafe");
    },
  },
] as const;

export const vgaTypes = [
  {
    value: "std",
    get label() {
      return i18n.t("vms:vmConfigConstants.standardVGA");
    },
  },
  { value: "qxl", label: "SPICE (QXL)" },
  {
    value: "virtio",
    get label() {
      return i18n.t("vms:vmConfigConstants.virtIOGPU");
    },
  },
  {
    value: "virtio-gl",
    get label() {
      return i18n.t("vms:vmConfigConstants.virtIOGPUGL");
    },
  },
  {
    value: "vmware",
    get label() {
      return i18n.t("vms:vmConfigConstants.vMwareCompatible");
    },
  },
  {
    value: "cirrus",
    get label() {
      return i18n.t("vms:vmConfigConstants.cirrusLogic");
    },
  },
  {
    value: "serial0",
    get label() {
      return i18n.t("vms:vmConfigConstants.serialTerminal");
    },
  },
  {
    value: "none",
    get label() {
      return i18n.t("vms:vmConfigConstants.none");
    },
  },
] as const;

export const biosOptions = [
  {
    value: "seabios",
    get label() {
      return i18n.t("vms:vmConfigConstants.seaBIOSLegacy");
    },
  },
  { value: "ovmf", label: "OVMF (UEFI)" },
] as const;

export const machineTypes = [
  {
    value: "pc",
    get label() {
      return i18n.t("vms:vmConfigConstants.i440fxDefault");
    },
  },
  { value: "q35", label: "Q35" },
] as const;

export const audioDevices = [
  {
    value: "",
    get label() {
      return i18n.t("vms:vmConfigConstants.none");
    },
  },
  {
    value: "ich9-intel-hda",
    get label() {
      return i18n.t("vms:vmConfigConstants.ich9IntelHDA");
    },
  },
  {
    value: "intel-hda",
    get label() {
      return i18n.t("vms:vmConfigConstants.intelHDA");
    },
  },
  {
    value: "AC97",
    get label() {
      return i18n.t("vms:vmConfigConstants.ac97");
    },
  },
] as const;

export const diskBusTypes = [
  {
    value: "scsi",
    get label() {
      return i18n.t("vms:vmConfigConstants.scsi");
    },
  },
  {
    value: "ide",
    get label() {
      return i18n.t("vms:vmConfigConstants.ide");
    },
  },
  {
    value: "sata",
    get label() {
      return i18n.t("vms:vmConfigConstants.sata");
    },
  },
  {
    value: "virtio",
    get label() {
      return i18n.t("vms:vmConfigConstants.virtIOBlock");
    },
  },
] as const;

/**
 * OS-type-dependent defaults matching Proxmox VE wizard behavior.
 * Source: pve-manager OSDefaults.js, OSTypeEdit.js, SystemEdit.js
 */
export interface OSDefaults {
  diskBus: "scsi" | "ide" | "sata" | "virtio";
  netModel: string;
  scsihw: string;
  bios: string;
  machineBase: "q35" | "pc";
  tpm: boolean;
  efiDisk: boolean;
}

function windowsDefaults(overrides?: Partial<OSDefaults>): OSDefaults {
  return {
    diskBus: "ide",
    netModel: "e1000",
    scsihw: "virtio-scsi-single",
    bios: "seabios",
    machineBase: "pc",
    tpm: false,
    efiDisk: false,
    ...overrides,
  };
}

export const osDefaults: Record<string, OSDefaults> = {
  // Linux
  l26: {
    diskBus: "scsi",
    netModel: "virtio",
    scsihw: "virtio-scsi-single",
    bios: "seabios",
    machineBase: "pc",
    tpm: false,
    efiDisk: false,
  },
  l24: {
    diskBus: "ide",
    netModel: "e1000",
    scsihw: "virtio-scsi-single",
    bios: "seabios",
    machineBase: "pc",
    tpm: false,
    efiDisk: false,
  },
  // Windows 11/2022/2025 — q35 + UEFI + TPM
  win11: windowsDefaults({
    machineBase: "q35",
    bios: "ovmf",
    tpm: true,
    efiDisk: true,
  }),
  // Windows 10 and older modern Windows
  win10: windowsDefaults(),
  win8: windowsDefaults(),
  win7: windowsDefaults(),
  w2k8: windowsDefaults(),
  wvista: windowsDefaults(),
  w2k3: windowsDefaults(),
  // Legacy Windows — rtl8139, no SCSI controller
  wxp: windowsDefaults({ netModel: "rtl8139", scsihw: "" }),
  w2k: windowsDefaults({ netModel: "rtl8139", scsihw: "" }),
  // Other
  solaris: {
    diskBus: "ide",
    netModel: "e1000",
    scsihw: "virtio-scsi-single",
    bios: "seabios",
    machineBase: "pc",
    tpm: false,
    efiDisk: false,
  },
  other: {
    diskBus: "ide",
    netModel: "e1000",
    scsihw: "virtio-scsi-single",
    bios: "seabios",
    machineBase: "pc",
    tpm: false,
    efiDisk: false,
  },
};

/** Returns true if the given ostype is any Windows variant. */
export function isWindowsOS(ostype: string): boolean {
  return (
    ostype.startsWith("win") ||
    ostype.startsWith("w2k") ||
    ostype === "wxp" ||
    ostype === "wvista"
  );
}

/** Fields that require a VM restart to take effect when changed on a running VM. */
export const RESTART_REQUIRED_FIELDS = new Set([
  "cores",
  "sockets",
  "cpu",
  "numa",
  "memory",
  "balloon",
  "bios",
  "machine",
  "scsihw",
  "net0",
  "vga",
  "audio0",
  "kvm",
  "acpi",
]);

export const rngSources = [
  {
    value: "/dev/urandom",
    get label() {
      return i18n.t("vms:vmConfigConstants.devUrandomRecommended");
    },
  },
  { value: "/dev/random", label: "/dev/random" },
  {
    value: "/dev/hwrng",
    get label() {
      return i18n.t("vms:vmConfigConstants.devHwrngHardware");
    },
  },
] as const;

export const serialOptions = [
  {
    value: "socket",
    get label() {
      return i18n.t("vms:vmConfigConstants.socketQmTerminal");
    },
  },
  { value: "/dev/ttyS0", label: "/dev/ttyS0" },
  { value: "/dev/ttyS1", label: "/dev/ttyS1" },
  { value: "/dev/ttyS2", label: "/dev/ttyS2" },
  { value: "/dev/ttyS3", label: "/dev/ttyS3" },
] as const;

export const virtiofsCacheModes = [
  {
    value: "auto",
    get label() {
      return i18n.t("vms:vmConfigConstants.auto");
    },
  },
  {
    value: "always",
    get label() {
      return i18n.t("vms:vmConfigConstants.always");
    },
  },
  {
    value: "never",
    get label() {
      return i18n.t("vms:vmConfigConstants.never");
    },
  },
] as const;

export const efiTypes = [
  {
    value: "4m",
    get label() {
      return i18n.t("vms:vmConfigConstants.n4mSecureBootCapable");
    },
  },
  {
    value: "2m",
    get label() {
      return i18n.t("vms:vmConfigConstants.n2MLegacy");
    },
  },
] as const;

export const tpmVersions = [
  { value: "v2.0", label: "TPM 2.0" },
  { value: "v1.2", label: "TPM 1.2" },
] as const;

/** Pattern-based check for config keys that require a VM restart. */
export function isRestartRequired(key: string): boolean {
  if (RESTART_REQUIRED_FIELDS.has(key)) return true;
  if (/^net\d+$/.test(key)) return true;
  if (/^usb\d+$/.test(key)) return true;
  if (/^hostpci\d+$/.test(key)) return true;
  if (/^serial\d+$/.test(key)) return true;
  return false;
}
