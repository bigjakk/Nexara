/**
 * Pure logic behind the node Options and Notes cards: reading and rewriting
 * the two property-string settings (wakeonlan, location), what the card shows
 * for each setting, which fields a node's version has, which settings an edit
 * changed, and the checks each field gets before it is sent.
 *
 * Every rule transcribed from Proxmox cites its source (pve-manager
 * PVE/NodeConfig.pm, pve-common src/PVE/JSONSchema.pm and ParseUtils.pm), and
 * none is stricter than the original, bar the two its comments name (a number
 * has to travel as a JSON number, a location's name inside a property string):
 * a check here refuses what Proxmox refuses too, because whatever it accepts
 * and Nexara refuses is a setting the operator can then change only in
 * Proxmox's own UI.
 */

import {
  joinSegments,
  setSegment,
  splitSegments,
  type Segment,
} from "@/lib/property-string";
import { isNodePVEAtLeast, PVE_NODE_FEATURES } from "@/lib/pve-version";
import type {
  NodeConfigRead,
  NodeNotes,
  NodeOptionKey,
  NodeOptionReadKey,
  NodeOptions,
  NodeOptionsChanges,
} from "../api/node-options-queries";

// ---------------------------------------------------------------------------
// Reading a property string
// ---------------------------------------------------------------------------

interface PropertyStringRead {
  /**
   * Each known key's value, trimmed. A bare segment is the default key's
   * value and is filed under that key.
   */
  values: Map<string, string>;
  /** The segments with a key this code does not know, trimmed, in order. */
  unknownSegments: string[];
}

/**
 * Reads `raw` with the structure of pve-common's parse_property_string
 * (src/PVE/JSONSchema.pm), or returns null for a value that function refuses:
 *
 *   - a segment of only whitespace is skipped;
 *   - a segment is "key=value" split at its first "=", where the key must be
 *     non-empty and the value, AS WRITTEN, too ("missing key in
 *     comma-separated list property" otherwise). Its /^([^=]+)=(.+)\z/ never
 *     trims, so "name= " has a value — one space — and is no empty one;
 *   - a segment with no "=" is the default key's value, and a format with no
 *     default key refuses it ("value without key, but schema does not define
 *     a default key");
 *   - a key given twice, the default key by name and bare included, is
 *     refused ("duplicate key in comma-separated list property");
 *   - a newline is refused ("properties must not contain newlines").
 *
 * The one way it is more lenient: segment() trims a key and a value for reading,
 * which parse_property_string does not, so padding that Proxmox would count in
 * the value (and refuse it as a MAC or an interface) is not counted here. That is
 * unreachable from a GET: parse_config drops a value that fails its format when
 * a node reads its file, so a value that reaches Nexara at all is one that node
 * accepted.
 *
 * A key outside `keys` is an "invalid key" to the Proxmox this was transcribed
 * from, and is kept all the same (unknownSegments): a newer node may have added
 * it.
 */
function readPropertyString(
  raw: string,
  keys: readonly string[],
  defaultKey: string | null,
): PropertyStringRead | null {
  const values = new Map<string, string>();
  const seen = new Set<string>();
  const unknownSegments: string[] = [];
  for (const seg of splitSegments(raw)) {
    if (seg.raw.includes("\n")) return null;
    if (seg.key === null) {
      if (seg.value === "") continue;
      if (defaultKey === null || seen.has(defaultKey)) return null;
      seen.add(defaultKey);
      values.set(defaultKey, seg.value);
      continue;
    }
    // The value as written, not as trimmed: see above.
    const written = seg.raw.slice(seg.raw.indexOf("=") + 1);
    if (seg.key === "" || written === "" || seen.has(seg.key)) return null;
    seen.add(seg.key);
    if (keys.includes(seg.key)) values.set(seg.key, seg.value);
    else unknownSegments.push(seg.raw.trim());
  }
  return { values, unknownSegments };
}

// ---------------------------------------------------------------------------
// wakeonlan
// ---------------------------------------------------------------------------

/*
 * pve-manager's $wakeonlan_desc (PVE/NodeConfig.pm), transcribed:
 *
 *   mac                MAC address, format mac-addr, the default_key and not
 *                      optional. Proxmox's own UI edits the whole value as one
 *                      MAC field (proxmox-widget-toolkit NodeOptionsView), so
 *                      it writes the bare MAC: both "02:00:00:00:00:01" and
 *                      "mac=02:00:00:00:00:01,bind-interface=vmbr0" occur.
 *   bind-interface     optional, format pve-iface. Default: the interface
 *                      carrying the default route. Since pve-manager 8.1.9.
 *   broadcast-address  optional, format ipv4. Default: 255.255.255.255. Since
 *                      pve-manager 8.1.9.
 *
 * The MAC is the one other nodes wake THIS node with. The interface and the
 * broadcast address are the ones THIS node uses when it sends a wake packet to
 * another (the wakeonlan method in PVE/API2/Nodes.pm reads them from the
 * sending node's own config).
 */
const WAKE_ON_LAN_KEYS = ["mac", "bind-interface", "broadcast-address"];

export interface WakeOnLanFields {
  mac: string;
  bindInterface: string;
  broadcastAddress: string;
}

export type ParsedWakeOnLan =
  | {
      ok: true;
      fields: WakeOnLanFields;
      /** Segments with a key this code does not know, kept by buildWakeOnLan. */
      unknownSegments: string[];
    }
  | { ok: false };

const NO_WAKE_ON_LAN: WakeOnLanFields = {
  mac: "",
  bindInterface: "",
  broadcastAddress: "",
};

/**
 * Parses a wakeonlan value. "" parses as no setting at all, every field empty.
 * `ok: false` is a value Proxmox refuses — a second bare segment, a duplicate
 * key, "mac=" or "=x", or other segments with no MAC, which is required.
 * Proxmox's own read drops such a value, so it is defensive here: the card
 * shows it as it is and the dialog can only remove it.
 */
export function parseWakeOnLan(raw: string): ParsedWakeOnLan {
  const read = readPropertyString(raw, WAKE_ON_LAN_KEYS, "mac");
  if (read === null) return { ok: false };
  const mac = read.values.get("mac") ?? "";
  const hasSegments = read.values.size > 0 || read.unknownSegments.length > 0;
  if (mac === "" && hasSegments) return { ok: false };
  return {
    ok: true,
    fields: {
      mac,
      bindInterface: read.values.get("bind-interface") ?? "",
      broadcastAddress: read.values.get("broadcast-address") ?? "",
    },
    unknownSegments: read.unknownSegments,
  };
}

function isWakeOnLanMac(seg: Segment): boolean {
  return (seg.key === null && seg.value !== "") || seg.key === "mac";
}

/**
 * Builds a wakeonlan value on `base`, the value as stored, the way buildNet
 * (features/vms/lib/vm-config-parsers.ts) builds a NIC: only a field that
 * differs from what `base` holds is rewritten, where it stands, so every other
 * segment keeps its bytes and its position — the keys this code has no field
 * for included — and an untouched value rebuilds to exactly `base`. A bare MAC
 * stays bare, and a "mac=" one stays keyed. A MAC with none to replace is
 * written bare and first, as Proxmox's own UI writes it.
 *
 * No MAC means no setting: "" (the MAC is required, so the interface and
 * broadcast address cannot outlive it). A `base` that does not parse is built
 * over as if there were none.
 */
export function buildWakeOnLan(fields: WakeOnLanFields, base = ""): string {
  if (fields.mac === "") return "";
  const parsed = parseWakeOnLan(base);
  const was = parsed.ok ? parsed.fields : NO_WAKE_ON_LAN;
  let segs = parsed.ok ? splitSegments(base) : [];
  if (fields.mac !== was.mac) {
    const keyed = segs.find(isWakeOnLanMac)?.key === "mac";
    const text = keyed ? `mac=${fields.mac}` : fields.mac;
    segs = setSegment(segs, isWakeOnLanMac, text, "prepend");
  }
  if (fields.bindInterface !== was.bindInterface) {
    segs = setSegment(
      segs,
      (s) => s.key === "bind-interface",
      fields.bindInterface ? `bind-interface=${fields.bindInterface}` : null,
    );
  }
  if (fields.broadcastAddress !== was.broadcastAddress) {
    segs = setSegment(
      segs,
      (s) => s.key === "broadcast-address",
      fields.broadcastAddress
        ? `broadcast-address=${fields.broadcastAddress}`
        : null,
    );
  }
  return joinSegments(segs);
}

// ---------------------------------------------------------------------------
// location
// ---------------------------------------------------------------------------

/*
 * pve-common's format pve-node-location (src/PVE/JSONSchema.pm), which
 * pve-manager's $confdesc->{location} (PVE/NodeConfig.pm) uses, transcribed:
 *
 *   latitude   number, -90 to 90, required.
 *   longitude  number, -180 to 180, required.
 *   name       string, at most 128 characters, optional.
 *
 * It has NO default key, so a bare segment is refused. Since pve-manager
 * 9.1.13. Unset, the node takes the datacenter's location.
 */
const LOCATION_KEYS = ["latitude", "longitude", "name"];

export interface LocationFields {
  latitude: string;
  longitude: string;
  name: string;
}

export type ParsedLocation =
  | {
      ok: true;
      fields: LocationFields;
      /** Segments with a key this code does not know, kept by buildLocation. */
      unknownSegments: string[];
    }
  | { ok: false };

const NO_LOCATION: LocationFields = { latitude: "", longitude: "", name: "" };

/**
 * Parses a location value. "" parses as no setting, every field empty.
 * `ok: false` is a value Proxmox refuses: a bare segment, a duplicate key, an
 * empty key or value, or other segments without both coordinates.
 */
export function parseLocation(raw: string): ParsedLocation {
  const read = readPropertyString(raw, LOCATION_KEYS, null);
  if (read === null) return { ok: false };
  const latitude = read.values.get("latitude") ?? "";
  const longitude = read.values.get("longitude") ?? "";
  const hasSegments = read.values.size > 0 || read.unknownSegments.length > 0;
  if ((latitude === "" || longitude === "") && hasSegments) {
    return { ok: false };
  }
  return {
    ok: true,
    fields: { latitude, longitude, name: read.values.get("name") ?? "" },
    unknownSegments: read.unknownSegments,
  };
}

const LOCATION_FIELD_KEYS = [
  ["latitude", "latitude"],
  ["longitude", "longitude"],
  ["name", "name"],
] as const;

/**
 * Builds a location value on `base` as buildWakeOnLan builds a wakeonlan one:
 * only a changed field is rewritten, where it stands, and a new one is written
 * "latitude=…,longitude=…[,name=…]". All three empty means no setting: "".
 * Callers check the fields first (a name alone is not a value).
 */
export function buildLocation(fields: LocationFields, base = ""): string {
  if (fields.latitude === "" && fields.longitude === "" && fields.name === "") {
    return "";
  }
  const parsed = parseLocation(base);
  const was = parsed.ok ? parsed.fields : NO_LOCATION;
  let segs = parsed.ok ? splitSegments(base) : [];
  for (const [key, field] of LOCATION_FIELD_KEYS) {
    const value = fields[field];
    if (value === was[field]) continue;
    segs = setSegment(
      segs,
      (s) => s.key === key,
      value === "" ? null : `${key}=${value}`,
    );
  }
  return joinSegments(segs);
}

// ---------------------------------------------------------------------------
// What the card shows
// ---------------------------------------------------------------------------

/** startall-onboot-delay: seconds before "start all" on boot; default 0. */
export function describeStartDelay(seconds: number | undefined): string {
  if (seconds === undefined) return "Default (no delay)";
  return seconds === 1 ? "1 second" : `${String(seconds)} seconds`;
}

/** ballooning-target: percent of memory; default 80. */
export function describeBallooningTarget(percent: number | undefined): string {
  return percent === undefined ? "Default (80%)" : `${String(percent)}%`;
}

/**
 * The wakeonlan value in words: "02:00:00:00:00:01", "… via vmbr0",
 * "…, broadcast 192.0.2.255". A value Nexara cannot read is shown as stored.
 */
export function describeWakeOnLan(raw: string | undefined): string {
  if (raw === undefined) return "Not configured";
  const parsed = parseWakeOnLan(raw);
  if (!parsed.ok) return raw;
  const { mac, bindInterface, broadcastAddress } = parsed.fields;
  if (mac === "") return "Not configured";
  let text = mac;
  if (bindInterface !== "") text += ` via ${bindInterface}`;
  if (broadcastAddress !== "") text += `, broadcast ${broadcastAddress}`;
  return text;
}

/**
 * The location value in words: "Site A (12.5, -45.25)" or "12.5, -45.25". Unset
 * it is the datacenter's, which the node falls back to. A value Nexara cannot
 * read is shown as stored.
 */
export function describeLocation(raw: string | undefined): string {
  if (raw === undefined) return "From datacenter";
  const parsed = parseLocation(raw);
  if (!parsed.ok) return raw;
  const { latitude, longitude, name } = parsed.fields;
  if (latitude === "" || longitude === "") return "From datacenter";
  const coordinates = `${latitude}, ${longitude}`;
  return name === "" ? coordinates : `${name} (${coordinates})`;
}

/**
 * The notes as shown, or null when there are none. The node's file keeps each
 * line with its "\n", so a trailing one is not text; whitespace alone is not
 * notes either.
 */
export function visibleNotes(description: string | undefined): string | null {
  const text = description?.trimEnd() ?? "";
  return text === "" ? null : text;
}

// ---------------------------------------------------------------------------
// Which fields a node has
// ---------------------------------------------------------------------------

export interface NodeOptionSupport {
  ballooningTarget: boolean;
  /** The Wake-on-LAN interface and broadcast address. */
  wolBindBroadcast: boolean;
  location: boolean;
}

/**
 * Which of the version-gated fields to offer for a node. An older node answers
 * a key it does not know with a 400, so a field its version lacks is hidden and
 * never sent (PVE_NODE_FEATURES says from which release each one is there). A
 * field the read ALREADY holds is offered whatever the version says: the node
 * has it, and an unknown version ("") would otherwise hide a setting the
 * operator can see on the card and has to be able to change or clear.
 */
export function nodeOptionSupport(
  pveVersion: string,
  read: NodeOptions,
): NodeOptionSupport {
  const wol = parseWakeOnLan(read.wakeonlan ?? "");
  const wolHoldsBindOrBroadcast =
    wol.ok &&
    (wol.fields.bindInterface !== "" || wol.fields.broadcastAddress !== "");
  return {
    ballooningTarget:
      isNodePVEAtLeast(pveVersion, PVE_NODE_FEATURES.BALLOONING_TARGET) ||
      read["ballooning-target"] !== undefined,
    wolBindBroadcast:
      isNodePVEAtLeast(pveVersion, PVE_NODE_FEATURES.WOL_BIND_BROADCAST) ||
      wolHoldsBindOrBroadcast,
    location:
      isNodePVEAtLeast(pveVersion, PVE_NODE_FEATURES.LOCATION) ||
      (read.location ?? "") !== "",
  };
}

// ---------------------------------------------------------------------------
// What changed under an open dialog
// ---------------------------------------------------------------------------

/** What each setting is called on the card and in a note about it. */
export const NODE_OPTION_LABELS: Record<NodeOptionKey, string> = {
  "startall-onboot-delay": "Start on boot delay",
  "ballooning-target": "RAM ballooning target",
  wakeonlan: "Wake-on-LAN",
  location: "Location",
  description: "Notes",
};

/**
 * The settings of the Options dialog whose stored value is not the same in two
 * reads of the node, in the order the card lists them: what the dialog opened
 * with against what a re-read after a 409 found. Only a setting that dialog
 * SHOWS is compared (`shown`, its own support): one it hides is not something
 * the operator can have overwritten by saving here. A number is compared as a
 * number and a string by its exact bytes, "" and absent being alike unset.
 */
export function optionsChangedBetween(
  was: NodeOptions,
  now: NodeOptions,
  shown: Pick<NodeOptionSupport, "ballooningTarget" | "location">,
): NodeOptionReadKey[] {
  const changed: NodeOptionReadKey[] = [];
  if (was["startall-onboot-delay"] !== now["startall-onboot-delay"]) {
    changed.push("startall-onboot-delay");
  }
  if (
    shown.ballooningTarget &&
    was["ballooning-target"] !== now["ballooning-target"]
  ) {
    changed.push("ballooning-target");
  }
  if (storedText(was.wakeonlan) !== storedText(now.wakeonlan)) {
    changed.push("wakeonlan");
  }
  if (shown.location && storedText(was.location) !== storedText(now.location)) {
    changed.push("location");
  }
  return changed;
}

/**
 * Whether the notes of two reads differ, as a save would see it: after one
 * trailing "\n" is taken off each, and with whitespace alone counting as none.
 */
export function notesChangedBetween(was: NodeNotes, now: NodeNotes): boolean {
  return (
    storedDescription(was.description) !== storedDescription(now.description)
  );
}

// ---------------------------------------------------------------------------
// What an edit changed
// ---------------------------------------------------------------------------

/**
 * What a dialog holds for each key it edits: the value, or null for none. A
 * key the dialog does not edit — a field it hid, or a setting it shows
 * read-only — is left OUT, which is what keeps it out of the request.
 */
export interface NodeOptionDrafts {
  "startall-onboot-delay"?: number | null;
  "ballooning-target"?: number | null;
  wakeonlan?: string | null;
  location?: string | null;
  description?: string | null;
}

/**
 * The description's text as the editor holds it: the node's file keeps each
 * line with its "\n" and read-back gives every line one, so one trailing "\n"
 * is not part of what anyone wrote.
 */
export function descriptionForEditing(description: string | undefined): string {
  if (description === undefined) return "";
  return description.endsWith("\n") ? description.slice(0, -1) : description;
}

type Outcome<T> =
  { kind: "same" } | { kind: "set"; value: T } | { kind: "clear" };

/**
 * One key: what the dialog opened with (null for unset) against what it holds
 * now. Unset against unset is no change; a value turned unset is a clear; a
 * value that is not what was there is a set. A draft of `undefined` is a key
 * the dialog does not edit.
 */
function settle<T extends number | string>(
  was: T | null,
  draft: T | null | undefined,
): Outcome<T> {
  if (draft === undefined) return { kind: "same" };
  if (draft === null)
    return was === null ? { kind: "same" } : { kind: "clear" };
  return draft === was ? { kind: "same" } : { kind: "set", value: draft };
}

/** A string setting as stored: absent and "" are both unset. */
function storedText(value: string | undefined): string | null {
  return value === undefined || value === "" ? null : value;
}

/** The inverse for a draft: "" is unset, and is never sent as a value. */
function draftText(
  value: string | null | undefined,
): string | null | undefined {
  return value === "" ? null : value;
}

/**
 * The description as stored, ready to compare: one trailing "\n" off, and none
 * at all when there is only whitespace.
 */
function storedDescription(description: string | undefined): string | null {
  if (description === undefined || description.trim() === "") return null;
  return descriptionForEditing(description);
}

/** The description as drafted: whitespace alone is no notes, as it is stored. */
function draftDescription(
  value: string | null | undefined,
): string | null | undefined {
  return value !== undefined && value !== null && value.trim() === ""
    ? null
    : value;
}

/**
 * The settings an edit changed, as the body of the PUT less its digest. Only
 * a key that changed is in it, so a save writes back nothing the operator did
 * not touch — what the form shows can be out of date, and an untouched field
 * sent back would overwrite what changed since. A cleared setting is named in
 * `delete`, because Proxmox clears a key only that way, and a key is never in
 * both: "" is never sent, and a set and a clear cannot meet in one request
 * (Proxmox assigns the values first and deletes afterwards, so the value would
 * be lost without a word).
 *
 * Numbers are compared as numbers and strings by their exact bytes. The
 * description is compared after one trailing "\n" is taken off what was read,
 * and a whitespace-only one counts as none.
 */
export function changedNodeOptions(
  opened: NodeConfigRead,
  drafts: NodeOptionDrafts,
): NodeOptionsChanges {
  const changes: NodeOptionsChanges = {};
  const clear: NodeOptionKey[] = [];

  const delay = settle(
    opened["startall-onboot-delay"] ?? null,
    drafts["startall-onboot-delay"],
  );
  if (delay.kind === "set") changes["startall-onboot-delay"] = delay.value;
  else if (delay.kind === "clear") clear.push("startall-onboot-delay");

  const target = settle(
    opened["ballooning-target"] ?? null,
    drafts["ballooning-target"],
  );
  if (target.kind === "set") changes["ballooning-target"] = target.value;
  else if (target.kind === "clear") clear.push("ballooning-target");

  const wakeonlan = settle(
    storedText(opened.wakeonlan),
    draftText(drafts.wakeonlan),
  );
  if (wakeonlan.kind === "set") changes.wakeonlan = wakeonlan.value;
  else if (wakeonlan.kind === "clear") clear.push("wakeonlan");

  const location = settle(
    storedText(opened.location),
    draftText(drafts.location),
  );
  if (location.kind === "set") changes.location = location.value;
  else if (location.kind === "clear") clear.push("location");

  const description = settle(
    storedDescription(opened.description),
    draftDescription(drafts.description),
  );
  if (description.kind === "set") changes.description = description.value;
  else if (description.kind === "clear") clear.push("description");

  if (clear.length > 0) changes.delete = clear;
  return changes;
}

// ---------------------------------------------------------------------------
// Checks before a send
// ---------------------------------------------------------------------------

/**
 * A field's check: its trimmed value, or the message to show under it. An
 * empty field passes (it means unset) except where its dialog needs a value.
 */
export type Checked<T> = { ok: true; value: T } | { ok: false; error: string };

/**
 * An integer setting, then its range. The pattern is is_integer's
 * (src/PVE/JSONSchema.pm: m/^[+-]?\d+\z/) with ASCII digits only: the value
 * goes out as a JSON number, so a digit the browser cannot read as one is no
 * use however Proxmox would count it, and that is the one place this is
 * narrower than Proxmox. The bounds are $confdesc's (PVE/NodeConfig.pm):
 * startall-onboot-delay 0-300, ballooning-target 0-100, both inclusive.
 */
export function checkInteger(
  text: string,
  min: number,
  max: number,
): Checked<number | null> {
  const trimmed = text.trim();
  if (trimmed === "") return { ok: true, value: null };
  const value = Number(trimmed);
  if (!/^[+-]?[0-9]+$/.test(trimmed) || value < min || value > max) {
    return {
      ok: false,
      error: `Enter a whole number from ${String(min)} to ${String(max)}.`,
    };
  }
  // -0 is a number, and prints as "0"; keep one zero.
  return { ok: true, value: value === 0 ? 0 : value };
}

/**
 * pve_verify_mac_addr (src/PVE/JSONSchema.pm): m/^[a-f0-9][02468ace](?::[a-f0-9]{2}){5}\z/i.
 * Colons only, and unicast only: the I/G bit of the first byte is not set,
 * which is the second digit being even.
 */
const MAC_ADDRESS = /^[a-f0-9][02468ace](?::[a-f0-9]{2}){5}$/i;

export function checkMacAddress(text: string): Checked<string> {
  const value = text.trim();
  if (value !== "" && !MAC_ADDRESS.test(value)) {
    return {
      ok: false,
      error:
        "Enter a unicast MAC address in the form XX:XX:XX:XX:XX:XX, with an even first byte.",
    };
  }
  return { ok: true, value };
}

/**
 * pve_verify_iface (src/PVE/JSONSchema.pm): m/^[a-z][a-z0-9_]{1,20}([:\.]\d+)?\z/i.
 * Perl's \d matches any Unicode digit, so \p{Nd} with the u flag keeps this
 * from refusing a name Proxmox accepts.
 */
const INTERFACE_NAME = /^[a-z][a-z0-9_]{1,20}(?:[:.]\p{Nd}+)?$/iu;

export function checkInterfaceName(text: string): Checked<string> {
  const value = text.trim();
  if (value !== "" && !INTERFACE_NAME.test(value)) {
    return {
      ok: false,
      error: "Enter a network interface name, such as vmbr0 or eth0.10.",
    };
  }
  return { ok: true, value };
}

/**
 * $IPV4OCTET and $IPV4RE (pve-common src/PVE/ParseUtils.pm), as pve_verify_ipv4
 * (src/PVE/JSONSchema.pm) uses them: m/^(?:$IPV4RE)\z/. ASCII digits.
 */
const IPV4_OCTET = "(?:25[0-5]|(?:2[0-4]|1[0-9]|[1-9])?[0-9])";
const IPV4_ADDRESS = new RegExp(`^(?:(?:${IPV4_OCTET}\\.){3}${IPV4_OCTET})$`);

export function checkIPv4Address(text: string): Checked<string> {
  const value = text.trim();
  if (value !== "" && !IPV4_ADDRESS.test(value)) {
    return { ok: false, error: "Enter an IPv4 address, such as 192.0.2.255." };
  }
  return { ok: true, value };
}

/**
 * is_number (src/PVE/JSONSchema.pm):
 * /^[+-]?(\d+\.\d+|\d+\.|\.\d+|\d+)([eE][+-]?\d+)?\z/, with \d as \p{Nd} for
 * the same reason as the interface name.
 */
const PVE_NUMBER =
  /^[+-]?(?:\p{Nd}+\.\p{Nd}+|\p{Nd}+\.|\.\p{Nd}+|\p{Nd}+)(?:[eE][+-]?\p{Nd}+)?$/u;

/**
 * A coordinate of pve-node-location (src/PVE/JSONSchema.pm): a number between
 * -`limit` and `limit`, inclusive (latitude 90, longitude 180). The range is
 * applied only to a number JavaScript can read; a digit it cannot is left for
 * Proxmox to judge, as the only one who can.
 */
export function checkCoordinate(
  text: string,
  name: "latitude" | "longitude",
  limit: number,
): Checked<string> {
  const value = text.trim();
  if (value === "") return { ok: true, value };
  const parsed = Number(value);
  if (
    !PVE_NUMBER.test(value) ||
    (!Number.isNaN(parsed) && Math.abs(parsed) > limit)
  ) {
    return {
      ok: false,
      error: `Enter the ${name} as a number from -${String(limit)} to ${String(limit)}.`,
    };
  }
  return { ok: true, value };
}

/**
 * A text's length in characters (code points), which is how Proxmox counts
 * maxLength (length() on the decoded string) and how the server counts it:
 * not the UTF-16 units of String.length, which count an emoji as two.
 * (features/mappings/lib/usb-mapping.ts counts the same way.)
 */
export function characterCount(text: string): number {
  return Array.from(text).length;
}

/** pve-node-location's name: maxLength 128 (src/PVE/JSONSchema.pm). */
export const LOCATION_NAME_MAX_LENGTH = 128;

/**
 * The location's name: at most 128 characters, and no ",". The comma is not a
 * Proxmox rule on the name but the format's own: a property string ends a
 * value at it, so what followed would be read as another segment.
 */
export function checkLocationName(text: string): Checked<string> {
  const value = text.trim();
  if (value.includes(",")) {
    return {
      ok: false,
      error: "The name cannot contain a comma.",
    };
  }
  if (characterCount(value) > LOCATION_NAME_MAX_LENGTH) {
    return {
      ok: false,
      error: `The name can be at most ${String(LOCATION_NAME_MAX_LENGTH)} characters.`,
    };
  }
  return { ok: true, value };
}

/** $confdesc->{description}: maxLength 64 * 1024 (PVE/NodeConfig.pm). */
export const NOTES_MAX_LENGTH = 64 * 1024;

/**
 * The notes: at most 65536 characters, the schema's own limit. That is the most
 * ANY node takes, not what every node takes: the server answers a request over
 * Proxmox's size cap with a 413 of its own, and the cap is 64 KiB before PVE 8.4
 * and 512 KiB since, counted after encoding. On an older node a long note, with
 * multi-byte characters especially, is refused sooner, and the message says so
 * rather than promise the schema's figure.
 */
export function checkNotes(text: string): Checked<string> {
  if (characterCount(text) > NOTES_MAX_LENGTH) {
    return {
      ok: false,
      error: `Notes can be at most ${String(NOTES_MAX_LENGTH)} characters; on Proxmox VE before 8.4 a long note can be refused sooner.`,
    };
  }
  return { ok: true, value: text };
}
