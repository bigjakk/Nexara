/** Shared VM configuration option arrays used by CreateVMDialog and HardwarePanel. */

export const osTypes = [
  { value: "l26", label: "Linux 2.6+" },
  { value: "l24", label: "Linux 2.4" },
  { value: "win11", label: "Windows 11/2022" },
  { value: "win10", label: "Windows 10/2016/2019" },
  { value: "win8", label: "Windows 8/2012" },
  { value: "win7", label: "Windows 7/2008r2" },
  { value: "wvista", label: "Windows Vista/2008" },
  { value: "w2k3", label: "Windows 2003" },
  { value: "w2k8", label: "Windows 2008" },
  { value: "wxp", label: "Windows XP/2003" },
  { value: "w2k", label: "Windows 2000" },
  { value: "solaris", label: "Solaris" },
  { value: "other", label: "Other" },
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

export const scsiControllers = [
  { value: "virtio-scsi-pci", label: "VirtIO SCSI" },
  { value: "virtio-scsi-single", label: "VirtIO SCSI Single" },
  { value: "lsi", label: "LSI 53C895A" },
  { value: "lsi53c810", label: "LSI 53C810" },
  { value: "megasas", label: "MegaRAID SAS" },
  { value: "pvscsi", label: "VMware PVSCSI" },
] as const;

export const netModels = [
  { value: "virtio", label: "VirtIO (paravirt.)" },
  { value: "e1000", label: "Intel E1000" },
  { value: "e1000e", label: "Intel E1000E" },
  { value: "vmxnet3", label: "VMware vmxnet3" },
  { value: "rtl8139", label: "Realtek RTL8139" },
] as const;

export const diskFormats = [
  { value: "qcow2", label: "QEMU image (qcow2)" },
  { value: "raw", label: "Raw disk image (raw)" },
  { value: "vmdk", label: "VMware image (vmdk)" },
] as const;

export const cacheModes = [
  { value: "none", label: "No cache (none)" },
  { value: "directsync", label: "Direct sync" },
  { value: "writethrough", label: "Write through" },
  { value: "writeback", label: "Write back (unsafe)" },
  { value: "unsafe", label: "Unsafe" },
] as const;

export const vgaTypes = [
  { value: "std", label: "Standard VGA" },
  { value: "qxl", label: "SPICE (QXL)" },
  { value: "virtio", label: "VirtIO-GPU" },
  { value: "virtio-gl", label: "VirtIO-GPU (GL)" },
  { value: "vmware", label: "VMware compatible" },
  { value: "cirrus", label: "Cirrus Logic" },
  { value: "serial0", label: "Serial terminal" },
  { value: "none", label: "None" },
] as const;

export const biosOptions = [
  { value: "seabios", label: "SeaBIOS (Legacy)" },
  { value: "ovmf", label: "OVMF (UEFI)" },
] as const;

export const machineTypes = [
  { value: "pc", label: "i440fx (Default)" },
  { value: "q35", label: "Q35" },
] as const;

export const audioDevices = [
  { value: "", label: "None" },
  { value: "ich9-intel-hda", label: "ich9 Intel HDA" },
  { value: "intel-hda", label: "Intel HDA" },
  { value: "AC97", label: "AC97" },
] as const;

export const diskBusTypes = [
  { value: "scsi", label: "SCSI" },
  { value: "ide", label: "IDE" },
  { value: "sata", label: "SATA" },
  { value: "virtio", label: "VirtIO Block" },
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
  l26: { diskBus: "scsi", netModel: "virtio", scsihw: "virtio-scsi-single", bios: "seabios", machineBase: "pc", tpm: false, efiDisk: false },
  l24: { diskBus: "ide", netModel: "e1000", scsihw: "virtio-scsi-single", bios: "seabios", machineBase: "pc", tpm: false, efiDisk: false },
  // Windows 11/2022/2025 — q35 + UEFI + TPM
  win11: windowsDefaults({ machineBase: "q35", bios: "ovmf", tpm: true, efiDisk: true }),
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
  solaris: { diskBus: "ide", netModel: "e1000", scsihw: "virtio-scsi-single", bios: "seabios", machineBase: "pc", tpm: false, efiDisk: false },
  other: { diskBus: "ide", netModel: "e1000", scsihw: "virtio-scsi-single", bios: "seabios", machineBase: "pc", tpm: false, efiDisk: false },
};

/** Returns true if the given ostype is any Windows variant. */
export function isWindowsOS(ostype: string): boolean {
  return ostype.startsWith("win") || ostype.startsWith("w2k") || ostype === "wxp" || ostype === "wvista";
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
