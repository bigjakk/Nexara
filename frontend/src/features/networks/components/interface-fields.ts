import type {
  NetworkInterface,
  NetworkInterfaceOptions,
} from "../types/network";

/**
 * Which Proxmox settings apply to which interface type, and the enums they
 * accept. Mirrors PVE's own NetworkEdit dialog — see
 * https://pve.proxmox.com/pve-docs/api-viewer/#/nodes/{node}/network for the
 * parameter list behind it.
 */

/** The types Proxmox's Create menu offers. The API's enum is wider, but the
 *  rest describe interfaces that already exist (a physical NIC, an OVS port
 *  implied by its bridge) rather than ones an operator creates by hand. */
export const CREATABLE_INTERFACE_TYPES = [
  { value: "bridge", label: "Linux Bridge", namePrefix: "vmbr" },
  { value: "bond", label: "Linux Bond", namePrefix: "bond" },
  { value: "vlan", label: "Linux VLAN", namePrefix: "" },
  { value: "OVSBridge", label: "OVS Bridge", namePrefix: "vmbr" },
  { value: "OVSBond", label: "OVS Bond", namePrefix: "bond" },
  { value: "OVSIntPort", label: "OVS IntPort", namePrefix: "" },
] as const;

const TYPE_LABELS: Record<string, string> = {
  ...Object.fromEntries(
    CREATABLE_INTERFACE_TYPES.map((t) => [t.value, t.label]),
  ),
  eth: "Network Device",
  alias: "Alias",
  OVSPort: "OVS Port",
  vnet: "VNet",
  fabric: "Fabric",
  unknown: "Unknown",
};

export function interfaceTypeLabel(type: string): string {
  return TYPE_LABELS[type] ?? type;
}

/** Kernel bonding modes, as offered for a Linux bond. */
export const LINUX_BOND_MODES = [
  "balance-rr",
  "active-backup",
  "balance-xor",
  "broadcast",
  "802.3ad",
  "balance-tlb",
  "balance-alb",
] as const;

/** Open vSwitch has its own, disjoint set. */
export const OVS_BOND_MODES = [
  "active-backup",
  "balance-slb",
  "lacp-balance-slb",
  "lacp-balance-tcp",
] as const;

export const BOND_HASH_POLICIES = ["layer2", "layer2+3", "layer3+4"] as const;

/** The modes valid for a type, and the one Proxmox preselects on create. The
 *  two sets are disjoint, so switching type must reset the field. */
export function bondModesFor(type: string): readonly string[] {
  return type === "OVSBond" ? OVS_BOND_MODES : LINUX_BOND_MODES;
}

export function defaultBondMode(type: string): string {
  return type === "OVSBond" ? "active-backup" : "balance-rr";
}

/** Hash policy only means anything for the two modes that hash. */
export function supportsHashPolicy(bondMode: string | undefined): boolean {
  return bondMode === "balance-xor" || bondMode === "802.3ad";
}

/** A primary slave only means anything when one slave is active at a time. */
export function supportsBondPrimary(bondMode: string | undefined): boolean {
  return bondMode === "active-backup";
}

export interface FieldSet {
  /** IPv4/IPv6 address, gateway and comment fields. */
  ip: boolean;
  autostart: boolean;
  bridgePorts: boolean;
  vlanAware: boolean;
  ovsBridge: boolean;
  ovsPorts: boolean;
  ovsBonds: boolean;
  ovsOptions: boolean;
  ovsTag: boolean;
  slaves: boolean;
  bondMode: boolean;
  vlanRawDevice: boolean;
  vlanId: boolean;
}

/** The fields Proxmox shows for a given interface type. */
export function fieldsForType(type: string): FieldSet {
  const isOVSAttached =
    type === "OVSBond" || type === "OVSIntPort" || type === "OVSPort";
  return {
    // An OVS bond is addressed through its bridge, never directly.
    ip: type !== "OVSBond",
    autostart: !isOVSAttached,
    bridgePorts: type === "bridge",
    vlanAware: type === "bridge",
    ovsBridge: isOVSAttached,
    ovsPorts: type === "OVSBridge",
    ovsBonds: type === "OVSBond",
    ovsOptions: isOVSAttached || type === "OVSBridge",
    ovsTag: isOVSAttached,
    slaves: type === "bond",
    bondMode: type === "bond" || type === "OVSBond",
    vlanRawDevice: type === "vlan",
    vlanId: type === "vlan",
  };
}

/**
 * Every option the form renders a control for, paired with the fieldsForType
 * flag that governs it. Used to decide what to clear when editing.
 *
 * Only settings with a control belong here. `address`, `netmask`, `address6`
 * and `netmask6` deliberately do NOT: the form edits the CIDR forms instead, so
 * they would be absent from every submission and land in `delete` on every
 * single edit — Proxmox derives them back from cidr, but only because it
 * expands cidr before applying `delete`.
 */
const OPTION_FIELD_GUARDS: {
  key: keyof NetworkInterfaceOptions;
  guard: keyof FieldSet | null;
}[] = [
  { key: "cidr", guard: "ip" },
  { key: "gateway", guard: "ip" },
  { key: "cidr6", guard: "ip" },
  { key: "gateway6", guard: "ip" },
  { key: "comments", guard: null },
  { key: "mtu", guard: null },
  { key: "bridge_ports", guard: "bridgePorts" },
  { key: "bridge_vids", guard: "vlanAware" },
  { key: "bridge_vlan_aware", guard: "vlanAware" },
  { key: "slaves", guard: "slaves" },
  { key: "bond_mode", guard: "bondMode" },
  { key: "bond_xmit_hash_policy", guard: "bondMode" },
  { key: "bond-primary", guard: "bondMode" },
  { key: "ovs_bridge", guard: "ovsBridge" },
  { key: "ovs_ports", guard: "ovsPorts" },
  { key: "ovs_bonds", guard: "ovsBonds" },
  { key: "ovs_options", guard: "ovsOptions" },
  { key: "ovs_tag", guard: "ovsTag" },
  { key: "vlan-id", guard: "vlanId" },
  { key: "vlan-raw-device", guard: "vlanRawDevice" },
];

function isBlank(value: unknown): boolean {
  return value === undefined || value === "" || value === 0;
}

/**
 * Names the settings that need Proxmox's `delete` parameter: ones the existing
 * interface carries a value for and the submitted form does not. An empty form
 * field is not enough — Proxmox ignores an empty value, so a cleared gateway
 * silently stays put unless it is named here.
 *
 * Fields that don't apply to the type are skipped: switching a bond's mode
 * shouldn't try to clear bridge settings it never had a form control for.
 */
export function clearedSettings(
  existing: NetworkInterface,
  submitted: NetworkInterfaceOptions,
  type: string,
): string[] {
  const fields = fieldsForType(type);
  const cleared: string[] = [];
  for (const { key, guard } of OPTION_FIELD_GUARDS) {
    if (guard !== null && !fields[guard]) continue;
    if (!isBlank(existing[key]) && isBlank(submitted[key])) {
      cleared.push(key);
    }
  }
  return cleared;
}

/** The member interfaces of a bridge or bond, whichever key holds them. */
export function interfacePorts(iface: NetworkInterface): string {
  const ports = [
    iface.bridge_ports,
    iface.ovs_ports,
    iface.slaves,
    iface.ovs_bonds,
  ].find((v) => v !== undefined && v !== "");
  return ports ?? "--";
}

/** IPv4 and IPv6 addresses on one line, as the Proxmox grid shows them. */
export function interfaceAddresses(iface: NetworkInterface): string {
  const parts = [
    iface.cidr ?? iface.address,
    iface.cidr6 ?? iface.address6,
  ].filter((v): v is string => v !== undefined && v !== "");
  return parts.length > 0 ? parts.join(", ") : "--";
}

export function interfaceGateways(iface: NetworkInterface): string {
  const parts = [iface.gateway, iface.gateway6].filter(
    (v): v is string => v !== undefined && v !== "",
  );
  return parts.length > 0 ? parts.join(", ") : "--";
}
