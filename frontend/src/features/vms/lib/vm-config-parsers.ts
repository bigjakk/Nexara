/**
 * Pure utility functions for parsing and building Proxmox VM config strings.
 * No React dependencies — safe for import in any context.
 */

// ---------------------------------------------------------------------------
// Generic key=value helpers
// ---------------------------------------------------------------------------

const MAC_RE = /^[0-9A-Fa-f]{2}(:[0-9A-Fa-f]{2}){5}$/;

export function parseKVString(raw: string): Map<string, string> {
  const map = new Map<string, string>();
  if (!raw) return map;
  for (const segment of raw.split(",")) {
    const idx = segment.indexOf("=");
    if (idx === -1) {
      // bare value — store as key with empty value
      map.set(segment.trim(), "");
    } else {
      map.set(segment.slice(0, idx).trim(), segment.slice(idx + 1).trim());
    }
  }
  return map;
}

export function buildKVString(map: Map<string, string>): string {
  const parts: string[] = [];
  for (const [k, v] of map) {
    if (v === "") {
      parts.push(k);
    } else {
      parts.push(`${k}=${v}`);
    }
  }
  return parts.join(",");
}

// ---------------------------------------------------------------------------
// net0
// ---------------------------------------------------------------------------

export interface ParsedNet {
  model: string;
  mac: string;
  bridge: string;
  firewall: boolean;
  vlanTag: string;
  rateLimit: string;
  mtu: string;
  multiqueue: string;
}

/**
 * Parse a Proxmox net0 string.
 * Formats:
 *   "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0,firewall=1,tag=100"
 *   "virtio,bridge=vmbr0"
 */
export function parseNet0(raw: string): ParsedNet {
  const result: ParsedNet = {
    model: "virtio",
    mac: "",
    bridge: "",
    firewall: false,
    vlanTag: "",
    rateLimit: "",
    mtu: "",
    multiqueue: "",
  };
  if (!raw) return result;

  const segments = raw.split(",");
  for (const seg of segments) {
    const eqIdx = seg.indexOf("=");
    if (eqIdx === -1) {
      // bare model name like "virtio"
      result.model = seg.trim();
      continue;
    }
    const key = seg.slice(0, eqIdx).trim();
    const val = seg.slice(eqIdx + 1).trim();

    // First segment may be "virtio=AA:BB:CC:DD:EE:FF"
    if (MAC_RE.test(val) && !["bridge", "firewall", "tag", "rate", "mtu", "queues"].includes(key)) {
      result.model = key;
      result.mac = val;
    } else {
      switch (key) {
        case "bridge":
          result.bridge = val;
          break;
        case "firewall":
          result.firewall = val === "1";
          break;
        case "tag":
          result.vlanTag = val;
          break;
        case "rate":
          result.rateLimit = val;
          break;
        case "mtu":
          result.mtu = val;
          break;
        case "queues":
          result.multiqueue = val;
          break;
        default:
          // model=MAC is already handled above; unknown keys ignored
          break;
      }
    }
  }
  return result;
}

export function buildNet0(parsed: ParsedNet): string {
  const parts: string[] = [];
  if (parsed.mac) {
    parts.push(`${parsed.model}=${parsed.mac}`);
  } else {
    parts.push(parsed.model);
  }
  if (parsed.bridge) parts.push(`bridge=${parsed.bridge}`);
  if (parsed.firewall) parts.push("firewall=1");
  if (parsed.vlanTag) parts.push(`tag=${parsed.vlanTag}`);
  if (parsed.rateLimit) parts.push(`rate=${parsed.rateLimit}`);
  if (parsed.mtu) parts.push(`mtu=${parsed.mtu}`);
  if (parsed.multiqueue) parts.push(`queues=${parsed.multiqueue}`);
  return parts.join(",");
}

// ---------------------------------------------------------------------------
// agent
// ---------------------------------------------------------------------------

export interface ParsedAgent {
  enabled: boolean;
  fstrimClonedDisks: boolean;
}

/**
 * Parse Proxmox agent field. Handles:
 *   "1", "0", "enabled=1,fstrim_cloned_disks=1", or empty/missing.
 */
export function parseAgent(raw: string): ParsedAgent {
  if (!raw) return { enabled: false, fstrimClonedDisks: false };

  // Simple "0" or "1"
  if (raw === "1") return { enabled: true, fstrimClonedDisks: false };
  if (raw === "0") return { enabled: false, fstrimClonedDisks: false };

  const kv = parseKVString(raw);
  return {
    enabled: kv.get("enabled") === "1",
    fstrimClonedDisks: kv.get("fstrim_cloned_disks") === "1",
  };
}

export function buildAgent(parsed: ParsedAgent): string {
  if (!parsed.enabled) return "0";
  const parts = ["enabled=1"];
  if (parsed.fstrimClonedDisks) parts.push("fstrim_cloned_disks=1");
  return parts.join(",");
}

// ---------------------------------------------------------------------------
// vga
// ---------------------------------------------------------------------------

export interface ParsedVGA {
  type: string;
  memory: string;
}

/**
 * Parse vga field: "std", "qxl,memory=64", "virtio-gl", etc.
 */
export function parseVGA(raw: string): ParsedVGA {
  if (!raw) return { type: "std", memory: "" };

  const idx = raw.indexOf(",");
  if (idx === -1) return { type: raw.trim(), memory: "" };

  const type = raw.slice(0, idx).trim();
  const rest = raw.slice(idx + 1);
  const kv = parseKVString(rest);
  return { type, memory: kv.get("memory") ?? "" };
}

export function buildVGA(parsed: ParsedVGA): string {
  if (parsed.memory) return `${parsed.type},memory=${parsed.memory}`;
  return parsed.type;
}

// ---------------------------------------------------------------------------
// boot order
// ---------------------------------------------------------------------------

/**
 * Parse boot field: "order=scsi0;ide2;net0"
 * Returns device list like ["scsi0", "ide2", "net0"].
 */
export function parseBootOrder(raw: string): string[] {
  if (!raw) return [];
  // strip "order=" prefix if present
  const val = raw.startsWith("order=") ? raw.slice(6) : raw;
  return val.split(";").filter(Boolean);
}

export function buildBootOrder(devices: string[]): string {
  if (devices.length === 0) return "";
  return `order=${devices.join(";")}`;
}

// ---------------------------------------------------------------------------
// audio0
// ---------------------------------------------------------------------------

export interface ParsedAudio {
  device: string;
  driver: string;
}

/**
 * Parse audio0 field: "device=ich9-intel-hda,driver=spice"
 */
export function parseAudio(raw: string): ParsedAudio {
  if (!raw) return { device: "", driver: "spice" };
  const kv = parseKVString(raw);
  return {
    device: kv.get("device") ?? "",
    driver: kv.get("driver") ?? "spice",
  };
}

export function buildAudio(parsed: ParsedAudio): string {
  if (!parsed.device) return "";
  return `device=${parsed.device},driver=${parsed.driver || "spice"}`;
}

// ---------------------------------------------------------------------------
// startup order
// ---------------------------------------------------------------------------

export interface ParsedStartup {
  order: string;
  up: string;
  down: string;
}

/**
 * Parse startup field: "order=1,up=30,down=60"
 */
export function parseStartup(raw: string): ParsedStartup {
  if (!raw) return { order: "", up: "", down: "" };
  const kv = parseKVString(raw);
  return {
    order: kv.get("order") ?? "",
    up: kv.get("up") ?? "",
    down: kv.get("down") ?? "",
  };
}

export function buildStartup(parsed: ParsedStartup): string {
  const parts: string[] = [];
  if (parsed.order) parts.push(`order=${parsed.order}`);
  if (parsed.up) parts.push(`up=${parsed.up}`);
  if (parsed.down) parts.push(`down=${parsed.down}`);
  return parts.join(",");
}

// ---------------------------------------------------------------------------
// disk
// ---------------------------------------------------------------------------

export interface ParsedDisk {
  storage: string;
  volume: string;
  size: string;
  format: string;
  cache: string;
  discard: boolean;
  ssd: boolean;
  iothread: boolean;
}

/**
 * Parse a disk config string like:
 *   "local-lvm:vm-100-disk-0,size=32G,format=qcow2,cache=none,discard=on,ssd=1,iothread=1"
 *   "local-lvm:32"
 *   "none,media=cdrom" (empty CD drive)
 */
export function parseDisk(raw: string): ParsedDisk {
  const result: ParsedDisk = {
    storage: "",
    volume: "",
    size: "",
    format: "",
    cache: "",
    discard: false,
    ssd: false,
    iothread: false,
  };
  if (!raw) return result;

  const segments = raw.split(",");
  const first = segments[0] ?? "";

  // First segment is "storage:volume" or "storage:size" or "none"
  const colonIdx = first.indexOf(":");
  if (colonIdx !== -1) {
    result.storage = first.slice(0, colonIdx);
    result.volume = first;
  } else {
    result.volume = first;
  }

  for (let i = 1; i < segments.length; i++) {
    const seg = segments[i] ?? "";
    const eqIdx = seg.indexOf("=");
    if (eqIdx === -1) continue;
    const key = seg.slice(0, eqIdx).trim();
    const val = seg.slice(eqIdx + 1).trim();

    switch (key) {
      case "size":
        result.size = val;
        break;
      case "format":
        result.format = val;
        break;
      case "cache":
        result.cache = val;
        break;
      case "discard":
        result.discard = val === "on";
        break;
      case "ssd":
        result.ssd = val === "1";
        break;
      case "iothread":
        result.iothread = val === "1";
        break;
    }
  }
  return result;
}

// ---------------------------------------------------------------------------
// USB passthrough
// ---------------------------------------------------------------------------

export interface ParsedUSB {
  host: string;
  usb3: boolean;
  spice: boolean;
}

export function parseUSB(raw: string): ParsedUSB {
  if (!raw) return { host: "", usb3: false, spice: false };
  if (raw === "spice") return { host: "", usb3: false, spice: true };
  const kv = parseKVString(raw);
  return {
    host: kv.get("host") ?? "",
    usb3: kv.get("usb3") === "1",
    spice: false,
  };
}

export function buildUSB(parsed: ParsedUSB): string {
  if (parsed.spice) return "spice";
  const parts: string[] = [];
  if (parsed.host) parts.push(`host=${parsed.host}`);
  if (parsed.usb3) parts.push("usb3=1");
  return parts.join(",");
}

// ---------------------------------------------------------------------------
// PCI passthrough
// ---------------------------------------------------------------------------

export interface ParsedPCI {
  host: string;
  pcie: boolean;
  rombar: boolean;
  xvga: boolean;
  mdev: string;
}

export function parsePCI(raw: string): ParsedPCI {
  if (!raw) return { host: "", pcie: false, rombar: true, xvga: false, mdev: "" };
  const segments = raw.split(",");
  const result: ParsedPCI = { host: "", pcie: false, rombar: true, xvga: false, mdev: "" };
  for (const seg of segments) {
    const eqIdx = seg.indexOf("=");
    if (eqIdx === -1) {
      // bare PCI address like "02:00"
      result.host = seg.trim();
      continue;
    }
    const key = seg.slice(0, eqIdx).trim();
    const val = seg.slice(eqIdx + 1).trim();
    switch (key) {
      case "host": result.host = val; break;
      case "pcie": result.pcie = val === "1"; break;
      case "rombar": result.rombar = val !== "0"; break;
      case "x-vga": result.xvga = val === "1"; break;
      case "mdev": result.mdev = val; break;
    }
  }
  return result;
}

export function buildPCI(parsed: ParsedPCI): string {
  const parts: string[] = [];
  if (parsed.host) parts.push(parsed.host);
  if (parsed.pcie) parts.push("pcie=1");
  if (!parsed.rombar) parts.push("rombar=0");
  if (parsed.xvga) parts.push("x-vga=1");
  if (parsed.mdev) parts.push(`mdev=${parsed.mdev}`);
  return parts.join(",");
}

// ---------------------------------------------------------------------------
// Serial port
// ---------------------------------------------------------------------------

export function parseSerial(raw: string): string {
  return raw.trim() || "socket";
}

export function buildSerial(value: string): string {
  return value.trim() || "socket";
}

// ---------------------------------------------------------------------------
// VirtIO RNG
// ---------------------------------------------------------------------------

export interface ParsedRNG {
  source: string;
  maxBytes: string;
  period: string;
}

export function parseRNG(raw: string): ParsedRNG {
  if (!raw) return { source: "/dev/urandom", maxBytes: "", period: "" };
  const kv = parseKVString(raw);
  return {
    source: kv.get("source") ?? "/dev/urandom",
    maxBytes: kv.get("max_bytes") ?? "",
    period: kv.get("period") ?? "",
  };
}

export function buildRNG(parsed: ParsedRNG): string {
  const parts: string[] = [];
  parts.push(`source=${parsed.source || "/dev/urandom"}`);
  if (parsed.maxBytes) parts.push(`max_bytes=${parsed.maxBytes}`);
  if (parsed.period) parts.push(`period=${parsed.period}`);
  return parts.join(",");
}

// ---------------------------------------------------------------------------
// VirtioFS share
// ---------------------------------------------------------------------------

export interface ParsedVirtioFS {
  dirid: string;
  cache: string;
  directIo: boolean;
}

export function parseVirtioFS(raw: string): ParsedVirtioFS {
  if (!raw) return { dirid: "", cache: "auto", directIo: false };
  const kv = parseKVString(raw);
  return {
    dirid: kv.get("dirid") ?? "",
    cache: kv.get("cache") ?? "auto",
    directIo: kv.get("direct-io") === "1",
  };
}

export function buildVirtioFS(parsed: ParsedVirtioFS): string {
  const parts: string[] = [];
  if (parsed.dirid) parts.push(`dirid=${parsed.dirid}`);
  if (parsed.cache) parts.push(`cache=${parsed.cache}`);
  if (parsed.directIo) parts.push("direct-io=1");
  return parts.join(",");
}

// ---------------------------------------------------------------------------
// EFI disk
// ---------------------------------------------------------------------------

export interface ParsedEFIDisk {
  volume: string;
  storage: string;
  efitype: string;
  preEnrolledKeys: boolean;
}

export function parseEFIDisk(raw: string): ParsedEFIDisk {
  if (!raw) return { volume: "", storage: "", efitype: "4m", preEnrolledKeys: false };
  const segments = raw.split(",");
  const first = segments[0] ?? "";
  const colonIdx = first.indexOf(":");
  const result: ParsedEFIDisk = {
    volume: first,
    storage: colonIdx !== -1 ? first.slice(0, colonIdx) : "",
    efitype: "4m",
    preEnrolledKeys: false,
  };
  for (let i = 1; i < segments.length; i++) {
    const seg = segments[i] ?? "";
    const eqIdx = seg.indexOf("=");
    if (eqIdx === -1) continue;
    const key = seg.slice(0, eqIdx).trim();
    const val = seg.slice(eqIdx + 1).trim();
    switch (key) {
      case "efitype": result.efitype = val; break;
      case "pre-enrolled-keys": result.preEnrolledKeys = val === "1"; break;
    }
  }
  return result;
}

export function buildEFIDisk(parsed: ParsedEFIDisk): string {
  const parts: string[] = [parsed.volume || parsed.storage + ":1"];
  if (parsed.efitype) parts.push(`efitype=${parsed.efitype}`);
  if (parsed.preEnrolledKeys) parts.push("pre-enrolled-keys=1");
  return parts.join(",");
}

// ---------------------------------------------------------------------------
// TPM state
// ---------------------------------------------------------------------------

export interface ParsedTPMState {
  volume: string;
  storage: string;
  version: string;
}

export function parseTPMState(raw: string): ParsedTPMState {
  if (!raw) return { volume: "", storage: "", version: "v2.0" };
  const segments = raw.split(",");
  const first = segments[0] ?? "";
  const colonIdx = first.indexOf(":");
  const result: ParsedTPMState = {
    volume: first,
    storage: colonIdx !== -1 ? first.slice(0, colonIdx) : "",
    version: "v2.0",
  };
  for (let i = 1; i < segments.length; i++) {
    const seg = segments[i] ?? "";
    const eqIdx = seg.indexOf("=");
    if (eqIdx === -1) continue;
    const key = seg.slice(0, eqIdx).trim();
    const val = seg.slice(eqIdx + 1).trim();
    if (key === "version") result.version = val;
  }
  return result;
}

export function buildTPMState(parsed: ParsedTPMState): string {
  const parts: string[] = [parsed.volume || parsed.storage + ":1"];
  if (parsed.version) parts.push(`version=${parsed.version}`);
  return parts.join(",");
}

// ---------------------------------------------------------------------------
// net0 → generic parseNet / buildNet (used for multi-NIC)
// ---------------------------------------------------------------------------

export function parseNet(raw: string): ParsedNet & { linkDown: boolean } {
  const result = parseNet0(raw);
  return {
    ...result,
    linkDown: raw.includes("link_down=1"),
  };
}

export function buildNet(parsed: ParsedNet & { linkDown: boolean }): string {
  let s = buildNet0(parsed);
  if (parsed.linkDown) s += ",link_down=1";
  return s;
}
