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
