import { describe, expect, it } from "vitest";
import type { NodePCIDevice } from "@/features/vms/api/vm-queries";
import {
  findCheckedPCIMappingAt,
  findReusablePCIMapping,
  parsePCIMappingEntry,
  PCI_PATH_PATTERN,
  pciDeviceLabel,
  pciDevicesAt,
  pciEntryForDevice,
  pciHostRisks,
  pciSlot,
  pciUnreadPaths,
  pciEntryMatches,
  pciMappingEntryDescription,
  pciPathsOverlap,
  samePCIMappingEntry,
  type PCIMappingEntry,
} from "./pci-mapping";

function dev(partial: Partial<NodePCIDevice>): NodePCIDevice {
  return {
    id: "",
    class: "0x030000",
    device_name: "",
    vendor_name: "",
    device: "",
    vendor: "0x1234",
    iommugroup: -1,
    ...partial,
  };
}

// As lspci lists them: "0x" ids, function 1 before function 0.
const devices: NodePCIDevice[] = [
  dev({ id: "0000:01:00.1", device: "0x0011", iommugroup: 14, subsystem_vendor: "0xabcd", subsystem_device: "0x0001" }),
  dev({ id: "0000:01:00.0", device: "0x5678", iommugroup: 14, subsystem_vendor: "0xABCD", subsystem_device: "0xEF01" }),
  dev({ id: "0000:02:00.0", device: "0x0002" }),
  dev({ id: "0000:03:00.0", device: "0x0003", iommugroup: 0, subsystem_vendor: "0xabcd" }),
  dev({ id: "0000:04:00.0", device: "0x0004", iommugroup: 7, mdev: true }),
  dev({ id: "0000:05:00.0", vendor: "0x12345", device: "0x0005" }),
];

describe("parsePCIMappingEntry", () => {
  it("reads every key, in any order", () => {
    expect(
      parsePCIMappingEntry(
        "subsystem-id=abcd:ef01,path=0000:01:00.0,node=pve-01,iommugroup=14,id=1234:5678,description=left",
      ),
    ).toEqual({
      node: "pve-01",
      path: "0000:01:00.0",
      id: "1234:5678",
      subsystemId: "abcd:ef01",
      iommugroup: "14",
    });
  });

  it("leaves what the entry lacks empty", () => {
    expect(parsePCIMappingEntry("id=1234:0002,node=pve-01,path=0000:02:00.0")).toEqual({
      node: "pve-01",
      path: "0000:02:00.0",
      id: "1234:0002",
      subsystemId: "",
      iommugroup: "",
    });
  });
});

describe("pciSlot", () => {
  it("drops the function", () => {
    expect(pciSlot("0000:01:00.1")).toBe("0000:01:00");
    expect(pciSlot("0000:01:00")).toBe("0000:01:00");
  });
});

describe("PCI_PATH_PATTERN", () => {
  it.each(["0000:01:00.0", "0000:01:00", "10000:0a:1f.7"])("takes %s", (p) => {
    expect(PCI_PATH_PATTERN.test(p)).toBe(true);
  });
  it.each(["01:00.0", "0000:0A:00.0", "0000:01:00.0;0000:02:00.0", "0000:01:00.0,node=x", ""])(
    "refuses %s",
    (p) => {
      expect(PCI_PATH_PATTERN.test(p)).toBe(false);
    },
  );
});

describe("pciEntryForDevice", () => {
  it("copies the device's ids without 0x, lowercased, with its group", () => {
    expect(pciEntryForDevice(devices, "pve-01", "0000:01:00.0")).toEqual({
      entry: {
        node: "pve-01",
        path: "0000:01:00.0",
        id: "1234:5678",
        subsystemId: "abcd:ef01",
        iommugroup: "14",
      },
      mdev: false,
    });
  });

  it("builds the whole device from function 0", () => {
    expect(pciEntryForDevice(devices, "pve-01", "0000:01:00")?.entry).toEqual({
      node: "pve-01",
      path: "0000:01:00",
      id: "1234:5678",
      subsystemId: "abcd:ef01",
      iommugroup: "14",
    });
  });

  it("leaves out a group the device is not in, and a subsystem it does not report", () => {
    expect(pciEntryForDevice(devices, "pve-01", "0000:02:00.0")?.entry).toEqual({
      node: "pve-01",
      path: "0000:02:00.0",
      id: "1234:0002",
      subsystemId: "",
      iommugroup: "",
    });
  });

  it("keeps group 0, and takes half a subsystem id as none", () => {
    const got = pciEntryForDevice(devices, "pve-01", "0000:03:00.0")?.entry;
    expect(got?.iommugroup).toBe("0");
    expect(got?.subsystemId).toBe("");
  });

  it("says when the device needs the mdev flag", () => {
    expect(pciEntryForDevice(devices, "pve-01", "0000:04:00.0")?.mdev).toBe(true);
  });

  it("has nothing for a device the node does not list, or one with an odd id", () => {
    expect(pciEntryForDevice(devices, "pve-01", "0000:09:00.0")).toBeUndefined();
    expect(pciEntryForDevice(devices, "pve-01", "0000:09:00")).toBeUndefined();
    expect(pciEntryForDevice(devices, "pve-01", "0000:05:00.0")).toBeUndefined();
  });
});

describe("findReusablePCIMapping", () => {
  const nic: PCIMappingEntry = {
    node: "pve-01",
    path: "0000:02:00.0",
    id: "1234:0002",
    subsystemId: "",
    iommugroup: "",
  };
  const gpu: PCIMappingEntry = {
    node: "pve-01",
    path: "0000:01:00.0",
    id: "1234:5678",
    subsystemId: "abcd:ef01",
    iommugroup: "14",
  };
  const m = (id: string, map: string[], mdev = false) => ({ id, map, mdev });

  it("finds the mapping whose only entry here is exactly the device", () => {
    const mappings = [
      m("other", ["id=9999:0001,node=pve-01,path=0000:09:00.0"]),
      m("gpu01", ["id=1234:5678,iommugroup=14,node=pve-01,path=0000:01:00.0,subsystem-id=abcd:ef01"]),
    ];
    expect(findReusablePCIMapping(mappings, gpu, false)?.id).toBe("gpu01");
  });

  it("ignores the mapping's entries for other nodes", () => {
    const mappings = [
      m("nic01", ["id=1234:0002,node=pve-02,path=0000:05:00.0", "id=1234:0002,node=pve-01,path=0000:02:00.0"]),
    ];
    expect(findReusablePCIMapping(mappings, nic, false)?.id).toBe("nic01");
  });

  it.each([
    ["another path", "id=1234:0002,node=pve-01,path=0000:02:00"],
    ["another id", "id=1234:0003,node=pve-01,path=0000:02:00.0"],
    ["a subsystem id", "id=1234:0002,node=pve-01,path=0000:02:00.0,subsystem-id=abcd:0002"],
    ["an IOMMU group", "id=1234:0002,iommugroup=0,node=pve-01,path=0000:02:00.0"],
  ])("does not take an entry with %s", (_why, entry) => {
    expect(findReusablePCIMapping([m("nic01", [entry])], nic, false)).toBeUndefined();
  });

  // Proxmox compares the stored id with `ne` against sysfs's lowercase hex,
  // so a mapping made elsewhere in uppercase refuses to start the VM.
  it("does not take an entry whose id is stored in uppercase", () => {
    const entry = "id=1234:ABCD,node=pve-01,path=0000:02:00.0";
    expect(
      findReusablePCIMapping([m("nic01", [entry])], { ...nic, id: "1234:abcd" }, false),
    ).toBeUndefined();
  });

  it("does not take a mapping with a second entry on the node", () => {
    const mappings = [
      m("nic01", ["id=1234:0002,node=pve-01,path=0000:02:00.0", "id=1234:0003,node=pve-01,path=0000:03:00.0"]),
    ];
    expect(findReusablePCIMapping(mappings, nic, false)).toBeUndefined();
  });

  it("takes only a mapping whose mdev flag matches the device", () => {
    const mappings = [m("nic01", ["id=1234:0002,node=pve-01,path=0000:02:00.0"], true)];
    expect(findReusablePCIMapping(mappings, nic, false)).toBeUndefined();
    expect(findReusablePCIMapping(mappings, nic, true)?.id).toBe("nic01");
  });
});

describe("pciDeviceLabel", () => {
  it("names the device, else its vendor, else its address", () => {
    expect(pciDeviceLabel(dev({ id: "0000:01:00.0", device_name: "Example GPU", vendor_name: "Example Corp" }))).toBe("Example GPU");
    expect(pciDeviceLabel(dev({ id: "0000:01:00.0", vendor_name: "Example Corp" }))).toBe("Example Corp");
    expect(pciDeviceLabel(dev({ id: "0000:01:00.0" }))).toBe("0000:01:00.0");
  });

  it("cleans what the device reports", () => {
    expect(pciDeviceLabel(dev({ id: "0000:01:00.0", device_name: "Example‮ GPU\n" }))).toBe("Example GPU");
  });
});

describe("pciDevicesAt", () => {
  it("is the one function, or every function of the slot", () => {
    expect(pciDevicesAt(devices, "0000:01:00.1").map((d) => d.id)).toEqual(["0000:01:00.1"]);
    expect(pciDevicesAt(devices, "0000:01:00").map((d) => d.id)).toEqual([
      "0000:01:00.1",
      "0000:01:00.0",
    ]);
    expect(pciDevicesAt(devices, "0000:09:00.0")).toEqual([]);
  });
});

describe("pciHostRisks", () => {
  const node: NodePCIDevice[] = [
    dev({ id: "0000:10:00.0", class: "0x010802", iommugroup: 30 }),
    dev({ id: "0000:11:00.0", class: "0x020000", iommugroup: 31 }),
    dev({ id: "0000:12:00.0", class: "0x0c0330", iommugroup: 32 }),
    dev({ id: "0000:13:00.0", class: "0x030000", iommugroup: 30 }),
    dev({ id: "0000:14:00.0", class: "0x030000", iommugroup: 30 }),
    dev({ id: "0000:15:00.0", class: "0x030000", iommugroup: -1 }),
    dev({ id: "0000:16:00.0", class: "0x040300", iommugroup: -1 }),
  ];
  const at = (id: string) => node.filter((d) => d.id === id);

  it("names the controllers a node most often needs", () => {
    expect(pciHostRisks(at("0000:10:00.0"), node)[0]).toBe(
      "0000:10:00.0 is a storage controller: if the node's own disks are on it, the node loses them.",
    );
    expect(pciHostRisks(at("0000:11:00.0"), node)).toEqual([
      "0000:11:00.0 is a network controller: if the node's management or cluster network runs over it, the node loses that network.",
    ]);
    expect(pciHostRisks(at("0000:12:00.0"), node)).toEqual([
      "0000:12:00.0 is a USB controller: the node loses every USB device on it.",
    ]);
  });

  it("names the rest of the IOMMU group, which goes with it", () => {
    expect(pciHostRisks(at("0000:13:00.0"), node)).toEqual([
      "Proxmox takes every device in the IOMMU group away from the node when the VM starts, so 0000:10:00.0, 0000:14:00.0 go too.",
    ]);
    // What is passed is not a peer of itself.
    expect(
      pciHostRisks([...at("0000:13:00.0"), ...at("0000:14:00.0")], node).at(-1),
    ).toBe(
      "Proxmox takes every device in the IOMMU group away from the node when the VM starts, so 0000:10:00.0 goes too.",
    );
  });

  it("warns nothing for a device in no group, whatever else is in none", () => {
    // -1 is no group, not one group every such device shares.
    expect(pciHostRisks(at("0000:15:00.0"), node)).toEqual([]);
  });
});

describe("findCheckedPCIMappingAt", () => {
  const m = (id: string, map: string[], checks: { severity: string; message: string }[] = []) => ({
    id,
    map,
    checks,
  });

  it("takes the mapping whose only entry here is at the path and Proxmox checked clean", () => {
    const mappings = [
      m("broken", ["id=1234:0007,node=pve-01,path=0000:07:00.0"], [
        { severity: "error", message: "Invalid configuration: 'id' does not match" },
      ]),
      m("two", ["id=1234:0008,node=pve-01,path=0000:07:00.0", "id=1234:0009,node=pve-01,path=0000:08:00.0"]),
      m("elsewhere", ["id=1234:0008,node=pve-02,path=0000:07:00.0"]),
      m("clean", ["id=1234:0008,node=pve-01,path=0000:07:00.0"]),
    ];
    expect(findCheckedPCIMappingAt(mappings, "pve-01", "0000:07:00.0")?.id).toBe("clean");
  });

  it("takes nothing at another path, or on another node", () => {
    const mappings = [m("clean", ["id=1234:0008,node=pve-01,path=0000:07:00.0"])];
    expect(findCheckedPCIMappingAt(mappings, "pve-01", "0000:07:00")).toBeUndefined();
    expect(findCheckedPCIMappingAt(mappings, "pve-02", "0000:07:00.0")).toBeUndefined();
  });
});

describe("pciDevicesAt with a list", () => {
  it("takes every path of a ;-joined list", () => {
    expect(
      pciDevicesAt(devices, "0000:02:00.0;0000:03:00.0").map((d) => d.id),
    ).toEqual(["0000:02:00.0", "0000:03:00.0"]);
  });
});

describe("pciHostRisks with a device named twice", () => {
  it("weighs it once", () => {
    const nvme = dev({ id: "0000:10:00.0", class: "0x010802", iommugroup: 30 });
    expect(pciHostRisks([nvme, nvme], [nvme])).toEqual([
      "0000:10:00.0 is a storage controller: if the node's own disks are on it, the node loses them.",
    ]);
  });
});

describe("pciUnreadPaths", () => {
  it("names each path whose device the list does not hold, once", () => {
    // 0000:0c:00.0 is named only inside the list, beside a device the list
    // holds.
    expect(
      pciUnreadPaths(
        ["0000:01:00.0", "0000:0c:00.0;0000:02:00.0", "0000:09:00.0", "0000:09:00.0", "0000:0a:00"],
        devices,
      ),
    ).toEqual(["0000:0c:00.0", "0000:09:00.0", "0000:0a:00"]);
  });

  // A chipset slot: the list shows its audio function but hides function 0,
  // the ISA bridge that passing the whole slot would take too.
  it("names a whole device whose function 0 the list does not hold", () => {
    const chipset = [dev({ id: "0000:1f:00.3", class: "0x040300", device: "0x001f" })];
    expect(pciUnreadPaths(["0000:1f:00"], chipset)).toEqual(["0000:1f:00"]);
    expect(pciUnreadPaths(["0000:1f:00.3"], chipset)).toEqual([]);
  });

  it("names nothing the list holds, whole devices included", () => {
    expect(pciUnreadPaths(["0000:01:00", "0000:02:00.0"], devices)).toEqual([]);
    expect(pciUnreadPaths([], devices)).toEqual([]);
  });
});

describe("samePCIMappingEntry", () => {
  const entry =
    "description=left slot,id=abcd:5678,iommugroup=7,node=pve-01,path=0000:01:00.0,subsystem-id=abcd:ef01";

  it("matches an entry the server rewrote: key order, id case, group spelling", () => {
    expect(
      samePCIMappingEntry(
        entry,
        "node=pve-01,path=0000:01:00.0,id=ABCD:5678,subsystem-id=ABCD:EF01,iommugroup=+07,description=left slot",
      ),
    ).toBe(true);
  });

  it("tells apart any change of what the entry says", () => {
    for (const other of [
      entry.replace("node=pve-01", "node=pve-02"),
      entry.replace("path=0000:01:00.0", "path=0000:01:00.1"),
      entry.replace("id=abcd:5678", "id=abcd:5679"),
      entry.replace("subsystem-id=abcd:ef01", "subsystem-id=abcd:ef02"),
      entry.replace("iommugroup=7", "iommugroup=8"),
      entry.replace(",iommugroup=7", ""),
      entry.replace("description=left slot,", ""),
    ]) {
      expect(samePCIMappingEntry(entry, other)).toBe(false);
    }
  });
});

describe("pciEntryMatches", () => {
  const expected: PCIMappingEntry = {
    node: "pve-01",
    path: "0000:01:00.0",
    id: "1234:5678",
    subsystemId: "abcd:ef01",
    iommugroup: "7",
  };

  it("is true when the entry holds exactly what the device reports", () => {
    expect(
      pciEntryMatches(
        "id=1234:5678,iommugroup=7,node=pve-01,path=0000:01:00.0,subsystem-id=abcd:ef01",
        expected,
      ),
    ).toBe(true);
  });

  it("is false for the same device spelled as Proxmox refuses it: an uppercase id, a group written another way", () => {
    const hex: PCIMappingEntry = { ...expected, id: "abcd:5678" };
    const exact =
      "id=abcd:5678,iommugroup=7,node=pve-01,path=0000:01:00.0,subsystem-id=abcd:ef01";
    expect(pciEntryMatches(exact, hex)).toBe(true);
    for (const entry of [
      exact.replace("id=abcd:5678", "id=ABCD:5678"),
      exact.replace("subsystem-id=abcd:ef01", "subsystem-id=ABCD:EF01"),
      exact.replace("iommugroup=7", "iommugroup=07"),
      exact.replace("iommugroup=7", "iommugroup=+7"),
    ]) {
      expect(pciEntryMatches(entry, hex)).toBe(false);
    }
  });

  it("is false for a stale group, a missing subsystem id or another path", () => {
    expect(
      pciEntryMatches(
        "id=1234:5678,iommugroup=9,node=pve-01,path=0000:01:00.0,subsystem-id=abcd:ef01",
        expected,
      ),
    ).toBe(false);
    expect(
      pciEntryMatches(
        "id=1234:5678,iommugroup=7,node=pve-01,path=0000:01:00.0",
        expected,
      ),
    ).toBe(false);
    expect(
      pciEntryMatches(
        "id=1234:5678,iommugroup=7,node=pve-01,path=0000:01:00,subsystem-id=abcd:ef01",
        expected,
      ),
    ).toBe(false);
  });
});

describe("pciPathsOverlap", () => {
  it("is the same address, or a whole device and one of its functions", () => {
    expect(pciPathsOverlap("0000:01:00.0", "0000:01:00.0")).toBe(true);
    expect(pciPathsOverlap("0000:01:00", "0000:01:00.1")).toBe(true);
    expect(pciPathsOverlap("0000:01:00.1", "0000:01:00")).toBe(true);
    expect(pciPathsOverlap("0000:02:00.0;0000:01:00.0", "0000:01:00")).toBe(
      true,
    );
  });

  it("is not two functions of one device, or two devices", () => {
    expect(pciPathsOverlap("0000:01:00.0", "0000:01:00.1")).toBe(false);
    expect(pciPathsOverlap("0000:01:00.0", "0000:02:00.0")).toBe(false);
    expect(pciPathsOverlap("0000:02:00.0;0000:03:00.0", "0000:01:00")).toBe(
      false,
    );
  });
});

describe("pciMappingEntryDescription", () => {
  it("reads the entry's own description, = and all", () => {
    expect(
      pciMappingEntryDescription(
        "description=slot=2,id=1234:5678,node=pve-01,path=0000:01:00.0",
      ),
    ).toBe("slot=2");
    expect(
      pciMappingEntryDescription("id=1234:5678,node=pve-01,path=0000:01:00.0"),
    ).toBe("");
  });
});
