import { describe, it, expect } from "vitest";
import { parseCTNet, buildCTNet, emptyCTNet } from "./ct-net";

describe("CT NIC round-trip", () => {
  // The panel diffs buildCTNet(parseCTNet(raw)) against raw to decide whether
  // there are unsaved changes. Anything but a byte-identical rebuild leaves it
  // permanently dirty and strips the missing keys on save.
  it.each([
    // What Proxmox actually writes for a container NIC.
    "name=eth0,bridge=vmbr0,hwaddr=BC:24:11:00:00:01,ip=dhcp,type=veth",
    // Unmodelled keys beyond type.
    "name=eth0,bridge=vmbr0,ip=dhcp,type=veth,link_down=1",
    "name=eth0,bridge=vmbr0,type=veth,trunks=10;20;30",
    // firewall=0 spelled out rather than omitted.
    "name=eth0,bridge=vmbr0,ip=dhcp,firewall=0,type=veth",
    // Every modelled key present.
    "name=eth0,bridge=vmbr0,hwaddr=AA:BB:CC:DD:EE:FF,ip=10.0.0.5/24,gw=10.0.0.1,ip6=auto,gw6=fe80::1,firewall=1,rate=10,mtu=1400,tag=42,type=veth",
    // Unmodelled key first, to prove position is preserved too.
    "type=veth,name=eth0,bridge=vmbr0,ip=dhcp",
  ])("rebuilds %j unchanged", (raw) => {
    expect(buildCTNet(parseCTNet(raw))).toBe(raw);
  });

  it("keeps unmodelled keys when a modelled field is edited", () => {
    const nic = parseCTNet(
      "name=eth0,bridge=vmbr0,ip=dhcp,type=veth,link_down=1",
    );
    const edited = { ...nic, bridge: "vmbr1" };
    expect(buildCTNet(edited)).toBe(
      "name=eth0,bridge=vmbr1,ip=dhcp,type=veth,link_down=1",
    );
  });

  it("drops a modelled field the user cleared, keeping the rest in place", () => {
    const nic = parseCTNet("name=eth0,bridge=vmbr0,ip=dhcp,type=veth");
    expect(buildCTNet({ ...nic, ip: "" })).toBe(
      "name=eth0,bridge=vmbr0,type=veth",
    );
  });

  it("appends a modelled field that was not in the original string", () => {
    const nic = parseCTNet("name=eth0,bridge=vmbr0,type=veth");
    expect(buildCTNet({ ...nic, tag: "42" })).toBe(
      "name=eth0,bridge=vmbr0,type=veth,tag=42",
    );
  });

  it("toggles firewall without disturbing the other keys", () => {
    const nic = parseCTNet("name=eth0,bridge=vmbr0,firewall=0,type=veth");
    expect(nic.firewall).toBe(false);
    expect(buildCTNet({ ...nic, firewall: true })).toBe(
      "name=eth0,bridge=vmbr0,firewall=1,type=veth",
    );
  });

  it("exposes unmodelled keys separately from the modelled ones", () => {
    const nic = parseCTNet("name=eth0,bridge=vmbr0,ip=dhcp,type=veth");
    expect(nic.bridge).toBe("vmbr0");
    expect([...nic.extra]).toEqual([["type", "veth"]]);
  });

  it("builds a new NIC in canonical order", () => {
    // No original string to preserve, so the modelled order applies.
    const nic = emptyCTNet({
      name: "eth1",
      bridge: "vmbr0",
      ip: "dhcp",
      firewall: true,
    });
    expect(buildCTNet(nic)).toBe("name=eth1,bridge=vmbr0,ip=dhcp,firewall=1");
  });

  it("round-trips an empty string", () => {
    expect(buildCTNet(parseCTNet(""))).toBe("");
  });
});
