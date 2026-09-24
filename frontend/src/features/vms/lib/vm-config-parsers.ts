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
// Property strings, edited in place
// ---------------------------------------------------------------------------

/**
 * One comma-separated segment of a Proxmox property string. `key` is null
 * for a segment with no "=", which holds the value of the format's
 * default_key (a NIC's model, a VGA type). `raw` is the segment as stored.
 */
interface Segment {
  key: string | null;
  value: string;
  raw: string;
}

/**
 * Split one segment as pve-common's parse_property_string
 * (src/PVE/JSONSchema.pm) does: at its first "=", or not at all when it has
 * none. Key and value are trimmed for reading; `raw` is left as it was.
 */
function segment(raw: string): Segment {
  const idx = raw.indexOf("=");
  if (idx === -1) return { key: null, value: raw.trim(), raw };
  return {
    key: raw.slice(0, idx).trim(),
    value: raw.slice(idx + 1).trim(),
    raw,
  };
}

function splitSegments(raw: string): Segment[] {
  return raw === "" ? [] : raw.split(",").map(segment);
}

function joinSegments(segs: Segment[]): string {
  return segs.map((s) => s.raw).join(",");
}

/**
 * Write `text` in place of the segments `owns` matches: where the first one
 * stands, dropping any later match. When nothing matches it is appended, or
 * prepended for a default_key. `text` null removes the matches. Every other
 * segment keeps its bytes and its position.
 */
function setSegment(
  segs: Segment[],
  owns: (s: Segment) => boolean,
  text: string | null,
  whenAbsent: "append" | "prepend" = "append",
): Segment[] {
  const out: Segment[] = [];
  let found = false;
  for (const s of segs) {
    if (!owns(s)) {
      out.push(s);
      continue;
    }
    if (!found && text !== null) out.push(segment(text));
    found = true;
  }
  if (found || text === null) return out;
  return whenAbsent === "prepend"
    ? [segment(text), ...out]
    : [...out, segment(text)];
}

/**
 * pve-common's parse_boolean (src/PVE/JSONSchema.pm), which
 * parse_property_string applies to every boolean option: 1, on, yes and
 * true, in any case, are true; 0, off, no and false are false. Anything else
 * fails Proxmox's check_type there; it reads as off here.
 */
function pveBoolean(value: string): boolean {
  return /^(1|on|yes|true)$/i.test(value);
}

// ---------------------------------------------------------------------------
// netN
// ---------------------------------------------------------------------------

/*
 * qemu-server's $net_fmt (src/PVE/QemuServer/Network.pm), transcribed:
 *
 *   model        the default_key, so a bare segment is the model. One of
 *                $nic_model_list: e1000, e1000-82540em, e1000-82544gc,
 *                e1000-82545em, e1000e, i82551, i82557b, i82559er, ne2k_isa,
 *                ne2k_pci, pcnet, rtl8139, virtio, vmxnet3.
 *   <model>=MAC  one alias per $nic_model_list entry (keyAlias model, alias
 *                macaddr): model and MAC in one segment. The form print_net
 *                writes.
 *   macaddr      the MAC, when it is not written through that shorthand.
 *   bridge       pve-bridge-id. Absent means user-mode (NAT) networking.
 *   queues       integer 0-64, "Number of packet queues to be used on the
 *                device": virtio-net multiqueue. print_netdev_full and
 *                print_netdevice_full (src/PVE/QemuServer.pm) use it only
 *                when the model is virtio, and ignore it on any other.
 *   rate         number >= 0: MB/s, fractional allowed.
 *   tag          integer 1-4094.
 *   trunks       "vlanid[;vlanid...]", each an id or an id-id range.
 *   mtu          integer 1-65520, VirtIO only; 1 means the bridge's MTU.
 *   firewall, link_down, host-tunnel
 *                booleans (see pveBoolean).
 *
 * The API re-prints every netN it is sent ($update_vm_api in
 * src/PVE/API2/Qemu.pm runs parse_net, then print_net), so a NIC written
 * through it reads back in print_property_string's order: the <model>=MAC
 * shorthand first, then the other options sorted by key. A NIC edited by
 * hand keeps whatever order it was written in.
 */

/**
 * $net_fmt's own keys. A key=value segment whose key is none of these and
 * whose value is a MAC is the <model>=MAC shorthand: matched on that shape
 * rather than against $nic_model_list, so a model Proxmox adds later still
 * reads as the model.
 */
const NET_FMT_KEYS = new Set([
  "model",
  "macaddr",
  "bridge",
  "queues",
  "rate",
  "tag",
  "trunks",
  "firewall",
  "link_down",
  "mtu",
  "host-tunnel",
]);

export interface ParsedNet {
  model: string;
  mac: string;
  bridge: string;
  firewall: boolean;
  vlanTag: string;
  rateLimit: string;
  mtu: string;
  /** queues=. The Hardware panel has no control for it. */
  multiqueue: string;
  linkDown: boolean;
}

/**
 * The fields buildNet writes, each with the $net_fmt key that stores it, in
 * the order a new NIC's options are written.
 */
const NET_FIELDS = [
  ["bridge", "bridge"],
  ["firewall", "firewall"],
  ["tag", "vlanTag"],
  ["rate", "rateLimit"],
  ["mtu", "mtu"],
  ["queues", "multiqueue"],
  ["link_down", "linkDown"],
] as const;

function isNetModelShorthand(seg: Segment): boolean {
  return (
    seg.key !== null && !NET_FMT_KEYS.has(seg.key) && MAC_RE.test(seg.value)
  );
}

/** The segment naming the model: bare, model=, or <model>=MAC. */
function isNetModelSegment(seg: Segment): boolean {
  return (
    (seg.key === null && seg.value !== "") ||
    seg.key === "model" ||
    isNetModelShorthand(seg)
  );
}

/**
 * Parse a netN value, e.g.
 *   "virtio=02:00:00:00:00:01,bridge=vmbr0,firewall=1,tag=100"
 *   "virtio,bridge=vmbr0"
 *   "model=e1000,macaddr=02:00:00:00:00:01,bridge=vmbr0"
 * Options with no field here (trunks, host-tunnel, a key newer than the
 * transcription above) are not read; buildNet keeps them.
 */
export function parseNet(raw: string): ParsedNet {
  const result: ParsedNet = {
    model: "virtio",
    mac: "",
    bridge: "",
    firewall: false,
    vlanTag: "",
    rateLimit: "",
    mtu: "",
    multiqueue: "",
    linkDown: false,
  };
  for (const seg of splitSegments(raw)) {
    if (seg.key === null) {
      // A blank segment is skipped, as parse_property_string skips it.
      if (seg.value !== "") result.model = seg.value;
      continue;
    }
    if (isNetModelShorthand(seg)) {
      result.model = seg.key;
      result.mac = seg.value;
      continue;
    }
    switch (seg.key) {
      case "model":
        result.model = seg.value;
        break;
      case "macaddr":
        result.mac = seg.value;
        break;
      case "bridge":
        result.bridge = seg.value;
        break;
      case "firewall":
        result.firewall = pveBoolean(seg.value);
        break;
      case "tag":
        result.vlanTag = seg.value;
        break;
      case "rate":
        result.rateLimit = seg.value;
        break;
      case "mtu":
        result.mtu = seg.value;
        break;
      case "queues":
        result.multiqueue = seg.value;
        break;
      case "link_down":
        result.linkDown = pveBoolean(seg.value);
        break;
    }
  }
  return result;
}

/**
 * Build a netN value on `base`, the value as stored. Only an option whose
 * field differs from what `base` holds is rewritten, where it stands; every
 * other segment keeps its bytes and its position, including the options
 * this editor has no field for. So an untouched NIC rebuilds to exactly
 * `base`, and comparing the two is the dirty check. With no `base` it builds
 * a new NIC.
 */
export function buildNet(parsed: ParsedNet, base = ""): string {
  const was = parseNet(base);
  let segs = splitSegments(base);

  const modelSeg = segs.find(isNetModelSegment);
  // Where the MAC is written: inside the model's segment when that is the
  // model=MAC shorthand, or when there is neither a model segment nor a
  // macaddr= (a new NIC, written the way print_net writes one). Beside a bare
  // or model= model, it goes in macaddr=.
  const shorthand = modelSeg
    ? isNetModelShorthand(modelSeg)
    : !segs.some((s) => s.key === "macaddr");
  const macChanged = parsed.mac !== was.mac;
  // A stored value with no model is one Proxmox refuses: $net_fmt's model is
  // not optional, and pve-common's check_prop (src/PVE/JSONSchema.pm)
  // rejects a missing key that is not. It gains a model segment only when
  // the model, or a MAC with no macaddr= to hold it, is changed here; adding
  // one unasked would make an untouched NIC differ from what is stored.
  if (
    segs.length === 0 ||
    parsed.model !== was.model ||
    (shorthand && macChanged)
  ) {
    let text = parsed.model;
    if (shorthand && parsed.mac) text = `${parsed.model}=${parsed.mac}`;
    else if (modelSeg?.key === "model") text = `model=${parsed.model}`;
    segs = setSegment(segs, isNetModelSegment, text, "prepend");
  }
  if (!shorthand && macChanged) {
    segs = setSegment(
      segs,
      (s) => s.key === "macaddr",
      parsed.mac ? `macaddr=${parsed.mac}` : null,
    );
  }
  for (const [key, field] of NET_FIELDS) {
    const value = parsed[field];
    if (value === was[field]) continue;
    let text: string | null = null;
    if (value === true) text = `${key}=1`;
    else if (typeof value === "string" && value !== "")
      text = `${key}=${value}`;
    segs = setSegment(segs, (s) => s.key === key, text);
  }
  return joinSegments(segs);
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
  /**
   * "" when the config names no type, which leaves the choice to Proxmox
   * when the VM starts (get_vga_properties, src/PVE/QemuServer.pm).
   */
  type: string;
  memory: string;
}

/** The segment naming the type: bare ($vga_fmt's default_key) or type=. */
function isVgaTypeSegment(seg: Segment): boolean {
  return (seg.key === null && seg.value !== "") || seg.key === "type";
}

/**
 * Parse vga against qemu-server's $vga_fmt (src/PVE/QemuServer.pm), e.g.
 * "std", "qxl,memory=64", "std,clipboard=vnc". `type` is an optional
 * default_key, so "memory=32" alone is a valid value with no type. `memory`
 * is in MiB. `clipboard` has no field here, and buildVGA keeps it.
 */
export function parseVGA(raw: string): ParsedVGA {
  const result: ParsedVGA = { type: "", memory: "" };
  for (const seg of splitSegments(raw)) {
    if (isVgaTypeSegment(seg)) result.type = seg.value;
    else if (seg.key === "memory") result.memory = seg.value;
  }
  return result;
}

/**
 * Build vga on `base` the way buildNet builds a NIC: only a changed option
 * is rewritten, so an untouched value, no vga line included, rebuilds to
 * exactly `base`. "" means no vga line at all.
 */
export function buildVGA(parsed: ParsedVGA, base = ""): string {
  const was = parseVGA(base);
  let segs = splitSegments(base);
  if (parsed.type !== was.type) {
    let text: string | null = null;
    if (parsed.type) {
      text =
        segs.find(isVgaTypeSegment)?.key === "type"
          ? `type=${parsed.type}`
          : parsed.type;
    }
    segs = setSegment(segs, isVgaTypeSegment, text, "prepend");
  }
  if (parsed.memory !== was.memory) {
    segs = setSegment(
      segs,
      (s) => s.key === "memory",
      parsed.memory ? `memory=${parsed.memory}` : null,
    );
  }
  return joinSegments(segs);
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
 * Parse audio0 field: "device=ich9-intel-hda,driver=spice". An absent driver
 * is spice, $audio_fmt's default (src/PVE/QemuServer.pm).
 */
export function parseAudio(raw: string): ParsedAudio {
  if (!raw) return { device: "", driver: "spice" };
  const kv = parseKVString(raw);
  return {
    device: kv.get("device") ?? "",
    driver: kv.get("driver") ?? "spice",
  };
}

/**
 * Build audio0 on `base` the way buildNet builds a NIC: only a changed
 * option is rewritten, so an untouched value rebuilds to exactly `base`. No
 * device means no audio0 at all ($audio_fmt requires one): "". A device
 * added where there was none is written with its driver spelled out.
 */
export function buildAudio(parsed: ParsedAudio, base = ""): string {
  if (!parsed.device) return "";
  if (!base)
    return `device=${parsed.device},driver=${parsed.driver || "spice"}`;
  const was = parseAudio(base);
  let segs = splitSegments(base);
  if (parsed.device !== was.device) {
    segs = setSegment(
      segs,
      (s) => s.key === "device",
      `device=${parsed.device}`,
    );
  }
  if (parsed.driver !== was.driver) {
    segs = setSegment(
      segs,
      (s) => s.key === "driver",
      parsed.driver ? `driver=${parsed.driver}` : null,
    );
  }
  return joinSegments(segs);
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
/**
 * Derive an image format from a volume id's file extension. Proxmox writes
 * file-based volumes as vm-100-disk-0.qcow2 / .raw / .vmdk and typically omits
 * a format= key, so the extension is the only record of what the image is.
 * Returns "" for extension-less volumes (LVM, ZFS, RBD), which are raw by
 * definition and take no format on a move.
 */
function formatFromVolume(volume: string): string {
  const match = /\.(qcow2|raw|vmdk)$/.exec(volume);
  return match?.[1] ?? "";
}

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

  // File-based storages encode the format in the volume's file extension and
  // usually omit format=, e.g. "nas:121/vm-121-disk-0.qcow2,size=81G".
  // An explicit format= below still wins. Block-backed volumes have no
  // extension and are always raw, which callers treat as "storage decides".
  result.format = formatFromVolume(result.volume);

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
  if (!raw)
    return { host: "", pcie: false, rombar: true, xvga: false, mdev: "" };
  const segments = raw.split(",");
  const result: ParsedPCI = {
    host: "",
    pcie: false,
    rombar: true,
    xvga: false,
    mdev: "",
  };
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
      case "host":
        result.host = val;
        break;
      case "pcie":
        result.pcie = val === "1";
        break;
      case "rombar":
        result.rombar = val !== "0";
        break;
      case "x-vga":
        result.xvga = val === "1";
        break;
      case "mdev":
        result.mdev = val;
        break;
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
  if (!raw)
    return { volume: "", storage: "", efitype: "4m", preEnrolledKeys: false };
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
      case "efitype":
        result.efitype = val;
        break;
      case "pre-enrolled-keys":
        result.preEnrolledKeys = val === "1";
        break;
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
// cpu
// ---------------------------------------------------------------------------

/** Flag state in a Proxmox `flags=` list: forced on, forced off, or unset. */
export type CPUFlagState = "default" | "on" | "off";

export interface ParsedCPU {
  /** Bare CPU model, e.g. "host" or "x86-64-v2-AES". */
  model: string;
  /** Only flags explicitly present as +flag / -flag. */
  flags: Record<string, Exclude<CPUFlagState, "default">>;
  /**
   * Every other key=value segment (hidden, hv-vendor-id, phys-bits,
   * guest-phys-bits, level, reported-model), preserved verbatim so a
   * round-trip through the editor cannot drop options it has no UI for.
   */
  extra: Map<string, string>;
}

/**
 * Parse the `cpu` field. Proxmox accepts the model either bare or as
 * `cputype=`:
 *   "host"
 *   "cputype=host,flags=+nested-virt;-pcid"
 *   "x86-64-v2-AES,flags=+aes,hidden=1"
 *
 * The model is normalised to its bare form; `buildCPU` emits it the same way.
 */
export function parseCPU(raw: string): ParsedCPU {
  const parsed: ParsedCPU = { model: "", flags: {}, extra: new Map() };
  if (!raw.trim()) return parsed;

  for (const segment of raw.split(",")) {
    const seg = segment.trim();
    if (!seg) continue;
    const idx = seg.indexOf("=");
    if (idx === -1) {
      // Bare segment — the model in its short form.
      parsed.model = seg;
      continue;
    }
    const key = seg.slice(0, idx).trim();
    const value = seg.slice(idx + 1).trim();
    if (key === "cputype") {
      parsed.model = value;
    } else if (key === "flags") {
      for (const flag of value.split(";")) {
        const f = flag.trim();
        if (f.startsWith("+")) parsed.flags[f.slice(1)] = "on";
        else if (f.startsWith("-")) parsed.flags[f.slice(1)] = "off";
      }
    } else {
      parsed.extra.set(key, value);
    }
  }
  return parsed;
}

export function buildCPU(parsed: ParsedCPU): string {
  if (!parsed.model) return "";
  const parts = [parsed.model];
  // Sorted so an unchanged selection always rebuilds to the same string and
  // does not register as a pending change.
  const flagNames = Object.keys(parsed.flags).sort();
  const flags = flagNames.map(
    (n) => (parsed.flags[n] === "on" ? "+" : "-") + n,
  );
  if (flags.length > 0) parts.push(`flags=${flags.join(";")}`);
  for (const [k, v] of parsed.extra) parts.push(`${k}=${v}`);
  return parts.join(",");
}

// ---------------------------------------------------------------------------
// watchdog
// ---------------------------------------------------------------------------

export interface ParsedWatchdog {
  /** "" when no watchdog device is configured. */
  model: string;
  action: string;
}

/** PVE's watchdog_fmt marks `model` optional with this default. */
const DEFAULT_WATCHDOG_MODEL = "i6300esb";

/**
 * Parse watchdog: "i6300esb", "model=i6300esb,action=reset", or — since PVE
 * makes the model optional — "action=reset" alone, which still means a real
 * i6300ESB device. Reporting no model for that would tell the user the VM has
 * no watchdog when it has one, and hide the action behind a disabled control.
 */
export function parseWatchdog(raw: string): ParsedWatchdog {
  if (!raw.trim()) return { model: "", action: "" };
  let model = "";
  let action = "";
  for (const segment of raw.split(",")) {
    const seg = segment.trim();
    if (!seg) continue;
    const idx = seg.indexOf("=");
    if (idx === -1) {
      model = seg;
      continue;
    }
    const key = seg.slice(0, idx).trim();
    const value = seg.slice(idx + 1).trim();
    if (key === "model") model = value;
    else if (key === "action") action = value;
  }
  // A non-empty watchdog field always describes a device, so fill in the model
  // PVE would have used rather than reporting "none".
  return { model: model || DEFAULT_WATCHDOG_MODEL, action };
}

export function buildWatchdog(parsed: ParsedWatchdog): string {
  if (!parsed.model) return "";
  const parts = [`model=${parsed.model}`];
  if (parsed.action) parts.push(`action=${parsed.action}`);
  return parts.join(",");
}

// ---------------------------------------------------------------------------
// smbios1
// ---------------------------------------------------------------------------

/** The base64-encoded string fields of smbios1. `uuid` is never encoded. */
export const SMBIOS_TEXT_FIELDS = [
  "manufacturer",
  "product",
  "version",
  "serial",
  "sku",
  "family",
] as const;

export type SMBIOSTextField = (typeof SMBIOS_TEXT_FIELDS)[number];

export interface ParsedSMBIOS {
  uuid: string;
  /** Decoded plain text, keyed by field name. */
  values: Record<SMBIOSTextField, string>;
}

function decodeBase64(value: string): string {
  try {
    // Proxmox encodes UTF-8 bytes; atob yields latin1, so re-decode.
    const binary = atob(value);
    const bytes = Uint8Array.from(binary, (c) => c.charCodeAt(0));
    return new TextDecoder().decode(bytes);
  } catch {
    // Not decodable — show the raw value rather than losing it.
    return value;
  }
}

function encodeBase64(value: string): string {
  const bytes = new TextEncoder().encode(value);
  let binary = "";
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary);
}

function emptySMBIOSValues(): Record<SMBIOSTextField, string> {
  return {
    manufacturer: "",
    product: "",
    version: "",
    serial: "",
    sku: "",
    family: "",
  };
}

/**
 * Parse smbios1. Values are base64 only when the string carries `base64=1` —
 * an older config may hold plain text that happens to look like base64, and
 * decoding that would turn "Dell" into mojibake.
 */
export function parseSMBIOS(raw: string): ParsedSMBIOS {
  const result: ParsedSMBIOS = { uuid: "", values: emptySMBIOSValues() };
  if (!raw.trim()) return result;

  const kv = parseKVString(raw);
  const isBase64 = kv.get("base64") === "1";
  result.uuid = kv.get("uuid") ?? "";
  for (const field of SMBIOS_TEXT_FIELDS) {
    const v = kv.get(field);
    if (v == null || v === "") continue;
    result.values[field] = isBase64 ? decodeBase64(v) : v;
  }
  return result;
}

/**
 * Build smbios1, always base64-encoding the text fields and flagging it.
 * Proxmox's own schema constrains those fields to the base64 alphabet, so a
 * plain value containing a space or comma is rejected outright; encoding
 * unconditionally is what its web UI does too.
 */
export function buildSMBIOS(parsed: ParsedSMBIOS): string {
  const parts: string[] = [];
  if (parsed.uuid) parts.push(`uuid=${parsed.uuid}`);
  let hasText = false;
  for (const field of SMBIOS_TEXT_FIELDS) {
    const v = parsed.values[field];
    if (!v) continue;
    parts.push(`${field}=${encodeBase64(v)}`);
    hasText = true;
  }
  if (parts.length === 0) return "";
  if (hasText) parts.push("base64=1");
  return parts.join(",");
}
