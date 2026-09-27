import type { NodeUSBDevice } from "@/features/vms/api/vm-queries";

/*
 * Cluster USB resource mappings: reading their node entries, picking the
 * device an entry passes through, and writing one. A mapping is the only way
 * an API token — which is all Nexara connects with — can pass a host USB
 * device to a guest: qemu-server lets nobody but root@pam set a raw usbN
 * host=… (check_usb_perm, src/PVE/API2/Qemu.pm).
 */

/**
 * The parts of a property string, split as pve-common's parse_property_string
 * (src/PVE/JSONSchema.pm) does: on ",", each part at its first "=". Key and
 * value are trimmed for reading, and a part with no "=" has no key.
 */
function entrySegments(raw: string): { key: string | null; value: string }[] {
  if (raw === "") return [];
  return raw.split(",").map((part) => {
    const idx = part.indexOf("=");
    if (idx === -1) return { key: null, value: part.trim() };
    return {
      key: part.slice(0, idx).trim(),
      value: part.slice(idx + 1).trim(),
    };
  });
}

/**
 * One node entry of a USB mapping's `map`, in pve-guest-common's $map_fmt
 * (src/PVE/Mapping/USB.pm): `id` is the vendor:product id, and `path`, when
 * present, is the port the mapping passes through instead.
 */
export interface USBMappingEntry {
  node: string;
  id: string;
  path: string;
}

export function parseUSBMappingEntry(raw: string): USBMappingEntry {
  const entry: USBMappingEntry = { node: "", id: "", path: "" };
  for (const s of entrySegments(raw)) {
    if (s.key === "node") entry.node = s.value;
    else if (s.key === "id") entry.id = s.value;
    else if (s.key === "path") entry.path = s.value;
  }
  return entry;
}

/**
 * The entry a mapping has for `node`. A VM can use only one: qemu-server
 * refuses the start with "More than one USB mapping per host not supported"
 * (parse_usb_device, src/PVE/QemuServer/USB.pm), though the create API would
 * store more.
 */
export function usbMappingEntryFor(
  map: readonly string[],
  node: string,
): USBMappingEntry | undefined {
  return map.map(parseUSBMappingEntry).find((e) => e.node === node);
}

/**
 * The mapping whose entry for `node` passes exactly this device: the same id
 * and the same port, or no port for a pick by id. Adding a device again then
 * reuses its mapping rather than minting a second one for the same hardware.
 *
 * The stored id must match the lowercase one EXACTLY. Proxmox's start-time
 * check compares it with `ne` against the node's lowercase sysfs hex
 * (assert_valid, pve-guest-common src/PVE/Mapping/USB.pm), so a mapping made
 * elsewhere as "ABCD:EF01" refuses to start every VM that uses it. And the
 * mapping must have exactly ONE entry for the node: qemu-server refuses the
 * start of any VM using one with more ("More than one USB mapping per host
 * not supported", parse_usb_device), whatever the listing's check says.
 *
 * A mapping Proxmox reports an error for is still reused. With the id exact,
 * the error can only say the device is not there now — and a new mapping for
 * the same device would say the same, so skipping it would only mint
 * duplicates. The dialog shows the check next to the reuse instead.
 */
export function findReusableUSBMapping<M extends { map: readonly string[] }>(
  mappings: readonly M[],
  node: string,
  deviceId: string,
  path: string,
): M | undefined {
  const id = deviceId.toLowerCase();
  return mappings.find((m) => {
    const entries = m.map
      .map(parseUSBMappingEntry)
      .filter((e) => e.node === node);
    const [e] = entries;
    return (
      entries.length === 1 && e !== undefined && e.id === id && e.path === path
    );
  });
}

/**
 * Text a device reported about itself, made safe to show and to store. A USB
 * device supplies its own product and manufacturer strings, so they can carry
 * anything: control characters (a carriage return makes Proxmox refuse a
 * mapping description outright, "property contains a line feed") and bidi
 * controls that reorder what is printed around them. Each becomes a space, and
 * runs of space collapse to one. Descriptions read back from Proxmox get the
 * same treatment before they are shown, since anyone who can edit a mapping
 * there can set one.
 */
export function cleanDeviceText(text: string): string {
  const cleaned = Array.from(text, (ch) => {
    const cp = ch.codePointAt(0) ?? 0;
    const control = cp < 0x20 || (cp >= 0x7f && cp <= 0x9f);
    const bidi =
      (cp >= 0x202a && cp <= 0x202e) ||
      (cp >= 0x2066 && cp <= 0x2069) ||
      cp === 0x200e ||
      cp === 0x200f ||
      cp === 0x061c;
    return control || bidi ? " " : ch;
  }).join("");
  return cleaned.replace(/\s+/g, " ").trim();
}

/**
 * A text's length as Proxmox's maxLength counts it: in characters (code
 * points), not UTF-16 units — the way the server counts it too
 * (utf8.RuneCountInString), so a description of accented letters is allowed
 * its full length.
 */
export function characterCount(text: string): number {
  return Array.from(text).length;
}

/**
 * pve-configid as Nexara's route declares it (apischema catalogue): a letter,
 * then letters, digits, "_" or "-", 2 to 128 characters in all.
 */
export const MAPPING_ID_PATTERN = /^[A-Za-z][A-Za-z0-9_-]{1,127}$/;

/**
 * A mapping name made from a device's label, e.g. "Example Serial Adapter" →
 * "example-serial-adapter": lowercase, runs of anything else collapsed to
 * one "-", starting with a letter, at most 32 characters — cut at the last
 * word that fits rather than mid-word, unless that would keep under half —
 * before a -2, -3, … suffix that steps past the names already `taken`
 * (compared case-insensitively).
 */
export function suggestMappingName(
  label: string,
  taken: ReadonlySet<string>,
): string {
  let base = label
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
  if (base !== "" && !/^[a-z]/.test(base)) base = `usb-${base}`;
  if (base.length > 32) {
    // The 33rd character decides: a "-" there means the first 32 end on a
    // word; otherwise back up to the last "-" within them.
    const boundary = base.slice(0, 33).lastIndexOf("-");
    base = boundary >= 16 ? base.slice(0, boundary) : base.slice(0, 32);
  }
  base = base.replace(/-+$/, "");
  if (base.length < 2) base = "usb-device";
  const lowerTaken = new Set([...taken].map((t) => t.toLowerCase()));
  let name = base;
  for (let n = 2; lowerTaken.has(name); n++) name = `${base}-${String(n)}`;
  return name;
}

// ---------------------------------------------------------------------------
// Picking the device an entry passes through
// ---------------------------------------------------------------------------

export const USB_DEVICE_ID_PATTERN = /^[0-9A-Fa-f]{4}:[0-9A-Fa-f]{4}$/;

/** Why a typed device id is refused, in the words every picker uses. */
export const USB_DEVICE_ID_HINT =
  "Enter vendor:product as four hex digits each, without 0x (e.g. 1234:5678).";

export const usbDeviceId = (d: NodeUSBDevice) => `${d.vendid}:${d.prodid}`;
export const usbPortPath = (d: NodeUSBDevice) =>
  `${String(d.busnum)}-${d.usbpath}`;

/**
 * The devices a passthrough picker offers, filtered as Proxmox's USBSelector
 * (pve-manager www/manager6/form/USBSelector.js) filters them: no root hub
 * (it has no usbpath), nothing without a product id, and no hub (class 9).
 */
export function passthroughCandidates(
  devices: NodeUSBDevice[] | undefined,
): NodeUSBDevice[] {
  return (devices ?? []).filter(
    (d) => d.usbpath !== "" && d.prodid !== "" && d.class !== 9,
  );
}

/** The device's own name, cleaned — it is the device's to say — or its id. */
export function deviceLabel(d: NodeUSBDevice | undefined, id: string): string {
  return (
    cleanDeviceText(d?.product ?? "") ||
    cleanDeviceText(d?.manufacturer ?? "") ||
    `USB ${id}`
  );
}

/** A host pick: by device id (any port), or the device on one port. */
export type USBPickMode = "device" | "port";

export interface PickedUSBDevice {
  /** vendor:product, lowercase. */
  deviceId: string;
  /** The port, or "" for a pick by id. */
  path: string;
  label: string;
}

/**
 * What a host pick passes through: a device id and, for a port, its path. A
 * typed id is lowercased to match the node's sysfs, which is what Proxmox
 * compares a mapping's id against when the VM starts. A port needs a device
 * listed on it: Proxmox's own mapping editor refuses an empty port the same
 * way, since the mapping needs the id of the device on it
 * (window/USBMapEdit.js).
 */
export function pickedUSBDevice(
  mode: USBPickMode,
  deviceId: string,
  port: string,
  candidates: readonly NodeUSBDevice[],
): PickedUSBDevice | null {
  if (mode === "device") {
    if (!USB_DEVICE_ID_PATTERN.test(deviceId)) return null;
    const id = deviceId.toLowerCase();
    const d = candidates.find((c) => usbDeviceId(c) === id);
    return { deviceId: id, path: "", label: deviceLabel(d, id) };
  }
  const d = candidates.find((c) => usbPortPath(c) === port);
  if (!d) return null;
  return {
    deviceId: usbDeviceId(d),
    path: port,
    label: deviceLabel(d, usbDeviceId(d)),
  };
}

// ---------------------------------------------------------------------------
// Writing entries
// ---------------------------------------------------------------------------

/**
 * An entry's own description, which parseUSBMappingEntry does not read.
 * Proxmox's mapping editor never shows it, but an entry can carry one, and an
 * update replaces every entry, so a rewritten entry has to keep it.
 */
export function usbMappingEntryDescription(raw: string): string {
  let description = "";
  for (const s of entrySegments(raw)) {
    if (s.key === "description") description = s.value;
  }
  return description;
}

/**
 * One node entry as a property string: node, id, path, description, an empty
 * one left out. The order here is only what the request carries — the server
 * re-checks and re-writes every entry in an order of its own
 * (proxmox.CanonicalUSBMap).
 */
export function buildUSBMappingEntry(entry: {
  node: string;
  id: string;
  path: string;
  description?: string;
}): string {
  let raw = `node=${entry.node},id=${entry.id.toLowerCase()}`;
  if (entry.path) raw += `,path=${entry.path}`;
  if (entry.description) raw += `,description=${entry.description}`;
  return raw;
}

/**
 * Whether two stored entries say the same thing, whatever order their keys
 * are in and whatever case the id is in — the server rewrites both on every
 * save, so a raw string need not survive another operator's edit of the
 * same mapping.
 */
export function sameUSBMappingEntry(a: string, b: string): boolean {
  const x = parseUSBMappingEntry(a);
  const y = parseUSBMappingEntry(b);
  return (
    x.node === y.node &&
    x.id.toLowerCase() === y.id.toLowerCase() &&
    x.path === y.path &&
    usbMappingEntryDescription(a) === usbMappingEntryDescription(b)
  );
}

/**
 * How many entries a mapping has for each node. More than one for a node means
 * no VM using the mapping starts there: qemu-server refuses with "More than
 * one USB mapping per host not supported" (parse_usb_device,
 * src/PVE/QemuServer/USB.pm), though Proxmox's API stores them.
 */
export function usbMappingEntriesPerNode(
  map: readonly string[],
): Map<string, number> {
  const counts = new Map<string, number>();
  for (const raw of map) {
    const { node } = parseUSBMappingEntry(raw);
    if (node !== "") counts.set(node, (counts.get(node) ?? 0) + 1);
  }
  return counts;
}
