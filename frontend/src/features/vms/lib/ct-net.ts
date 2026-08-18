import { parseKVString, buildKVString } from "./vm-config-parsers";

/**
 * An LXC network interface as the Resources form edits it.
 *
 * `extra` and `order` exist so the form can round-trip a NIC it does not fully
 * understand. Proxmox writes keys this form has no field for — `type=veth` on
 * every container, plus `link_down`, `trunks` and friends — and rebuilding
 * from the modelled fields alone would silently drop them on save and leave
 * the panel permanently dirty against its own config.
 */
export interface CTNetEdit {
  name: string;
  bridge: string;
  hwaddr: string;
  ip: string;
  gw: string;
  ip6: string;
  gw6: string;
  firewall: boolean;
  rate: string;
  mtu: string;
  tag: string;
  /** Keys Proxmox reported that this form does not model, kept verbatim. */
  extra: Map<string, string>;
  /** Original key order, so an untouched NIC rebuilds byte-identically. */
  order: string[];
}

/** Keys the form owns; everything else in a NIC string lands in `extra`. */
const MODELLED_KEYS = new Set([
  "name",
  "bridge",
  "hwaddr",
  "ip",
  "gw",
  "ip6",
  "gw6",
  "firewall",
  "rate",
  "mtu",
  "tag",
]);

/** A blank NIC, for the "Add Network Interface" path. */
export function emptyCTNet(overrides: Partial<CTNetEdit> = {}): CTNetEdit {
  return {
    name: "",
    bridge: "",
    hwaddr: "",
    ip: "",
    gw: "",
    ip6: "",
    gw6: "",
    firewall: false,
    rate: "",
    mtu: "",
    tag: "",
    extra: new Map(),
    order: [],
    ...overrides,
  };
}

export function parseCTNet(raw: string): CTNetEdit {
  const result = emptyCTNet();
  if (!raw) return result;

  const kv = parseKVString(raw);
  result.name = kv.get("name") ?? "";
  result.bridge = kv.get("bridge") ?? "";
  result.hwaddr = kv.get("hwaddr") ?? "";
  result.ip = kv.get("ip") ?? "";
  result.gw = kv.get("gw") ?? "";
  result.ip6 = kv.get("ip6") ?? "";
  result.gw6 = kv.get("gw6") ?? "";
  result.firewall = kv.get("firewall") === "1";
  result.rate = kv.get("rate") ?? "";
  result.mtu = kv.get("mtu") ?? "";
  result.tag = kv.get("tag") ?? "";

  result.order = [...kv.keys()];
  for (const [key, value] of kv) {
    if (!MODELLED_KEYS.has(key)) result.extra.set(key, value);
  }
  return result;
}

export function buildCTNet(n: CTNetEdit): string {
  const wasPresent = new Set(n.order);

  // Current values for the modelled keys. A key left empty is omitted, which
  // is how the form expresses "cleared" — except a boolean that Proxmox spelled
  // out as firewall=0, which is re-emitted so an untouched NIC stays identical.
  const modelled = new Map<string, string>();
  if (n.name) modelled.set("name", n.name);
  if (n.bridge) modelled.set("bridge", n.bridge);
  if (n.hwaddr) modelled.set("hwaddr", n.hwaddr);
  if (n.ip) modelled.set("ip", n.ip);
  if (n.gw) modelled.set("gw", n.gw);
  if (n.ip6) modelled.set("ip6", n.ip6);
  if (n.gw6) modelled.set("gw6", n.gw6);
  if (n.firewall) modelled.set("firewall", "1");
  else if (wasPresent.has("firewall")) modelled.set("firewall", "0");
  if (n.rate) modelled.set("rate", n.rate);
  if (n.mtu) modelled.set("mtu", n.mtu);
  if (n.tag) modelled.set("tag", n.tag);

  const out = new Map<string, string>();
  // Original keys first, in their original positions.
  for (const key of n.order) {
    const current = modelled.get(key);
    if (current !== undefined) out.set(key, current);
    else if (n.extra.has(key)) out.set(key, n.extra.get(key) ?? "");
    // A modelled key the user cleared is intentionally dropped.
  }
  // Then anything newly set, and any extras that arrived without an order.
  for (const [key, value] of modelled) if (!out.has(key)) out.set(key, value);
  for (const [key, value] of n.extra) if (!out.has(key)) out.set(key, value);

  return buildKVString(out);
}
