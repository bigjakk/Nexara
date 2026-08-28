import { describe, expect, it } from "vitest";
import {
  bondModesFor,
  clearedSettings,
  defaultBondMode,
  fieldsForType,
  interfaceAddresses,
  interfaceGateways,
  interfacePorts,
  supportsBondPrimary,
  supportsHashPolicy,
} from "./interface-fields";
import type { NetworkInterface } from "../types/network";

function iface(overrides: Partial<NetworkInterface> = {}): NetworkInterface {
  return { iface: "vmbr0", type: "bridge", active: 1, autostart: 1, ...overrides };
}

describe("fieldsForType", () => {
  it("gives a Linux bridge its ports and VLAN-aware toggle, not bond controls", () => {
    const f = fieldsForType("bridge");
    expect(f.bridgePorts).toBe(true);
    expect(f.vlanAware).toBe(true);
    expect(f.ip).toBe(true);
    expect(f.slaves).toBe(false);
    expect(f.bondMode).toBe(false);
  });

  it("gives a Linux bond slaves and a mode", () => {
    const f = fieldsForType("bond");
    expect(f.slaves).toBe(true);
    expect(f.bondMode).toBe(true);
    expect(f.bridgePorts).toBe(false);
  });

  it("addresses an OVS bond through its bridge, never directly", () => {
    const f = fieldsForType("OVSBond");
    expect(f.ip).toBe(false);
    expect(f.ovsBridge).toBe(true);
    expect(f.ovsBonds).toBe(true);
    expect(f.slaves).toBe(false);
  });

  it("hides autostart on the OVS types Proxmox brings up via their bridge", () => {
    expect(fieldsForType("OVSPort").autostart).toBe(false);
    expect(fieldsForType("OVSIntPort").autostart).toBe(false);
    expect(fieldsForType("OVSBond").autostart).toBe(false);
    expect(fieldsForType("bridge").autostart).toBe(true);
  });

  it("gives a Linux VLAN a raw device and a tag", () => {
    const f = fieldsForType("vlan");
    expect(f.vlanRawDevice).toBe(true);
    expect(f.vlanId).toBe(true);
  });
});

describe("bond mode dependent fields", () => {
  it("enables hash policy only for the modes that hash", () => {
    expect(supportsHashPolicy("balance-xor")).toBe(true);
    expect(supportsHashPolicy("802.3ad")).toBe(true);
    expect(supportsHashPolicy("active-backup")).toBe(false);
    expect(supportsHashPolicy(undefined)).toBe(false);
  });

  it("enables bond-primary only for active-backup", () => {
    expect(supportsBondPrimary("active-backup")).toBe(true);
    expect(supportsBondPrimary("802.3ad")).toBe(false);
  });
});

describe("clearedSettings", () => {
  it("names a field the operator emptied", () => {
    const existing = iface({ cidr: "10.0.0.2/24", gateway: "10.0.0.1" });
    // Gateway removed, CIDR kept.
    expect(clearedSettings(existing, { cidr: "10.0.0.2/24" }, "bridge")).toEqual([
      "gateway",
    ]);
  });

  it("returns nothing when every value is still present", () => {
    const existing = iface({ cidr: "10.0.0.2/24", gateway: "10.0.0.1" });
    expect(
      clearedSettings(existing, { cidr: "10.0.0.2/24", gateway: "10.0.0.1" }, "bridge"),
    ).toEqual([]);
  });

  it("treats a zeroed number as cleared", () => {
    const existing = iface({ mtu: 9000 });
    expect(clearedSettings(existing, {}, "bridge")).toEqual(["mtu"]);
  });

  it("ignores settings the type has no control for", () => {
    // A bond carries no bridge_ports control, so editing it must not try to
    // clear a value the form never showed.
    const existing = iface({ type: "bond", bridge_ports: "eno1", slaves: "eno1 eno2" });
    expect(clearedSettings(existing, { slaves: "eno1 eno2" }, "bond")).toEqual([]);
  });

  it("never clears address/netmask, which the form has no control for", () => {
    // Proxmox returns address+netmask alongside the derived cidr. The form
    // edits cidr only, so listing them would put them in `delete` on every
    // single edit and rely on Proxmox re-deriving them.
    const existing = iface({
      cidr: "192.168.90.236/24",
      address: "192.168.90.236",
      netmask: "255.255.255.0",
      address6: "fd00::2",
      netmask6: "64",
    });
    // Only the comment changed; the CIDR is resubmitted unchanged.
    expect(
      clearedSettings(existing, { cidr: "192.168.90.236/24", comments: "x" }, "bridge"),
    ).toEqual([]);
  });

  it("clears VLAN-aware through delete rather than a false value", () => {
    const existing = iface({ bridge_vlan_aware: 1, cidr: "10.0.0.2/24" });
    // Unchecking omits the key entirely; Proxmox needs it named to unset it.
    expect(
      clearedSettings(existing, { cidr: "10.0.0.2/24" }, "bridge"),
    ).toEqual(["bridge_vlan_aware"]);
  });

  it("clears a bond's hash policy when the mode no longer supports one", () => {
    const existing = iface({
      type: "bond",
      bond_mode: "802.3ad",
      bond_xmit_hash_policy: "layer3+4",
    });
    // Switching to active-backup drops the hash policy from the submitted form.
    expect(
      clearedSettings(existing, { bond_mode: "active-backup" }, "bond"),
    ).toEqual(["bond_xmit_hash_policy"]);
  });
});

describe("bond mode defaults", () => {
  it("offers each bond type only its own, disjoint enum", () => {
    expect(bondModesFor("bond")).toContain("802.3ad");
    expect(bondModesFor("bond")).not.toContain("lacp-balance-tcp");
    expect(bondModesFor("OVSBond")).toContain("lacp-balance-tcp");
    expect(bondModesFor("OVSBond")).not.toContain("802.3ad");
  });

  it("preselects what Proxmox preselects", () => {
    expect(defaultBondMode("bond")).toBe("balance-rr");
    expect(defaultBondMode("OVSBond")).toBe("active-backup");
  });

  it("keeps each default inside its own enum", () => {
    for (const type of ["bond", "OVSBond"]) {
      expect(bondModesFor(type)).toContain(defaultBondMode(type));
    }
  });
});

describe("table cell helpers", () => {
  it("shows a bridge's ports, a bond's slaves, and an OVS bridge's ports", () => {
    expect(interfacePorts(iface({ bridge_ports: "bond0" }))).toBe("bond0");
    expect(interfacePorts(iface({ type: "bond", slaves: "eno1 eno2" }))).toBe(
      "eno1 eno2",
    );
    expect(
      interfacePorts(iface({ type: "OVSBridge", ovs_ports: "eno1" })),
    ).toBe("eno1");
    expect(interfacePorts(iface())).toBe("--");
  });

  it("joins both address families on one line", () => {
    expect(
      interfaceAddresses(iface({ cidr: "10.0.0.2/24", cidr6: "fd00::2/64" })),
    ).toBe("10.0.0.2/24, fd00::2/64");
    expect(interfaceAddresses(iface({ address: "10.0.0.2" }))).toBe("10.0.0.2");
    expect(interfaceAddresses(iface())).toBe("--");
  });

  it("joins both gateways on one line", () => {
    expect(
      interfaceGateways(iface({ gateway: "10.0.0.1", gateway6: "fd00::1" })),
    ).toBe("10.0.0.1, fd00::1");
    expect(interfaceGateways(iface())).toBe("--");
  });
});
