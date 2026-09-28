import { describe, it, expect } from "vitest";
import type { NodeUSBDevice } from "@/features/vms/api/vm-queries";
import {
  parseUSBMappingEntry,
  usbMappingEntryFor,
  findReusableUSBMapping,
  suggestMappingName,
  cleanDeviceText,
  MAPPING_ID_PATTERN,
  buildUSBMappingEntry,
  characterCount,
  pickedUSBDevice,
  usbMappingEntriesPerNode,
  usbMappingEntryDescription,
} from "./usb-mapping";

describe("USB mapping entries", () => {
  const mappings = [
    {
      id: "byid",
      map: ["id=abcd:ef01,node=pve-01", "id=abcd:ef01,node=pve-02"],
    },
    { id: "byport", map: ["id=abcd:ef01,node=pve-01,path=1-2"] },
    { id: "elsewhere", map: ["id=1234:5678,node=pve-02"] },
    // Stored uppercase: Proxmox's start-time check compares it with `ne`
    // against lowercase sysfs hex, so every VM using it fails to start.
    { id: "uppercase", map: ["id=5678:ABCD,node=pve-01"] },
    // Two entries for one node: qemu-server refuses the start outright.
    {
      id: "twice",
      map: ["id=9999:0001,node=pve-01", "id=9999:0001,node=pve-01,path=1-4"],
    },
  ];

  // As vm-config-parsers' segment() always has, before this moved here: the
  // parse is for showing an entry, and Proxmox's API never stores one with
  // spaces around a key (parse_property_string refuses " node" as a key).
  it("reads keys and values with surrounding spaces trimmed", () => {
    expect(parseUSBMappingEntry(" node = pve-01 , id=1234:5678 ")).toEqual({
      node: "pve-01",
      id: "1234:5678",
      path: "",
    });
  });

  it("reads an entry whatever order its keys are in", () => {
    expect(parseUSBMappingEntry("path=1-2.3,node=pve-01,id=1234:5678")).toEqual(
      { node: "pve-01", id: "1234:5678", path: "1-2.3" },
    );
    expect(usbMappingEntryFor(mappings[0]?.map ?? [], "pve-02")).toEqual({
      node: "pve-02",
      id: "abcd:ef01",
      path: "",
    });
    expect(
      usbMappingEntryFor(mappings[2]?.map ?? [], "pve-01"),
    ).toBeUndefined();
  });

  it("reuses only a mapping that passes exactly the same thing on the node", () => {
    expect(
      findReusableUSBMapping(mappings, "pve-01", "abcd:ef01", "")?.id,
    ).toBe("byid");
    // The PICKED id may come in any case (a typed one); it is lowercased.
    expect(
      findReusableUSBMapping(mappings, "pve-01", "ABCD:EF01", "")?.id,
    ).toBe("byid");
    expect(
      findReusableUSBMapping(mappings, "pve-01", "abcd:ef01", "1-2")?.id,
    ).toBe("byport");
    // A by-id mapping is not a port mapping, nor the reverse.
    expect(
      findReusableUSBMapping(mappings, "pve-01", "abcd:ef01", "1-3"),
    ).toBeUndefined();
    // An entry for another node does not count.
    expect(
      findReusableUSBMapping(mappings, "pve-01", "1234:5678", ""),
    ).toBeUndefined();
  });

  it("never reuses a mapping Proxmox could not start a VM with", () => {
    // The STORED id must already be lowercase.
    expect(
      findReusableUSBMapping(mappings, "pve-01", "5678:abcd", ""),
    ).toBeUndefined();
    // Nor one with two entries for the node, even if one matches exactly.
    expect(
      findReusableUSBMapping(mappings, "pve-01", "9999:0001", ""),
    ).toBeUndefined();
    expect(
      findReusableUSBMapping(mappings, "pve-02", "abcd:ef01", "")?.id,
    ).toBe("byid");
  });

  // With the id exact, an error can only mean the device is not there right
  // now, and a new mapping would report the same: reuse it, so adding an
  // unplugged device twice does not mint usb-…-2.
  it("still reuses a mapping Proxmox reports the device missing for", () => {
    const missing = [
      {
        id: "unplugged",
        map: ["id=abcd:0001,node=pve-01"],
        errors: [
          {
            severity: "error",
            message: "Invalid configuration: usb device 'abcd:0001' not found",
          },
        ],
      },
    ];
    expect(findReusableUSBMapping(missing, "pve-01", "abcd:0001", "")?.id).toBe(
      "unplugged",
    );
  });
});

describe("cleanDeviceText", () => {
  it("keeps an ordinary device name as it is", () => {
    expect(cleanDeviceText("Example Serial Adapter")).toBe(
      "Example Serial Adapter",
    );
  });

  it("drops what a device could put in its own name", () => {
    // A carriage return would make Proxmox refuse the create outright.
    expect(cleanDeviceText("Example\rAdapter\n")).toBe("Example Adapter");
    expect(cleanDeviceText("Tab\tand\u0085NEL")).toBe("Tab and NEL");
    // Direction overrides and isolates would disguise the text.
    expect(cleanDeviceText("abc\u202Edcb\u2066x\u2069")).toBe("abc dcb x");
    expect(cleanDeviceText("  \u200E  ")).toBe("");
    // The Arabic letter mark is a bidi control too.
    expect(cleanDeviceText("ab\u061Ccd")).toBe("ab cd");
  });
});

describe("suggestMappingName", () => {
  it("makes a pve-configid out of a device's name", () => {
    expect(suggestMappingName("Example Serial Adapter", new Set())).toBe(
      "example-serial-adapter",
    );
    expect(suggestMappingName("  USB 3.0 -- Hub!! ", new Set())).toBe(
      "usb-3-0-hub",
    );
    // A digit cannot start a pve-configid.
    expect(suggestMappingName("2.4GHz Receiver", new Set())).toBe(
      "usb-2-4ghz-receiver",
    );
    expect(suggestMappingName("", new Set())).toBe("usb-device");
    expect(suggestMappingName("!!!", new Set())).toBe("usb-device");
    expect(suggestMappingName("x", new Set())).toBe("usb-device");
  });

  it("starts a name with the kind it is given", () => {
    expect(suggestMappingName("2080 Ti", new Set(), "pci")).toBe("pci-2080-ti");
    expect(suggestMappingName("", new Set(), "pci")).toBe("pci-device");
    expect(suggestMappingName("Example GPU", new Set(["example-gpu"]), "pci")).toBe(
      "example-gpu-2",
    );
  });

  it("caps the length without ending on a dash", () => {
    const name = suggestMappingName(
      "Example Very Long Dual Serial Bridge Controller",
      new Set(),
    );
    expect(name.length).toBeLessThanOrEqual(32);
    expect(name).not.toMatch(/-$/);
    expect(name).toMatch(MAPPING_ID_PATTERN);
    // Cut at the last word that fits, not mid-word.
    expect(
      suggestMappingName(
        "Example Dual Serial Bridge Controller Adapter",
        new Set(),
      ),
    ).toBe("example-dual-serial-bridge");
    // A boundary that would keep under half the name is not worth it.
    expect(
      suggestMappingName("Ab Cdefghijklmnopqrstuvwxyzabcdefghij", new Set()),
    ).toBe("ab-cdefghijklmnopqrstuvwxyzabcde");
    // One long word has no boundary to back up to; it is cut at 32.
    expect(
      suggestMappingName(
        "Examplesingleverylongwordwithoutanyspaces",
        new Set(),
      ),
    ).toBe("examplesingleverylongwordwithout");
    // The "usb-" a leading digit needs is inside the 32, not on top of it.
    const prefixed = suggestMappingName(
      "2.4GHz Wireless Receiver For Example Keyboards",
      new Set(),
    );
    expect(prefixed).toBe("usb-2-4ghz-wireless-receiver-for");
    expect(prefixed.length).toBeLessThanOrEqual(32);
  });

  it("steps past names already taken, ignoring case", () => {
    expect(
      suggestMappingName(
        "Example Radio",
        new Set(["example-radio", "Example-Radio-2"]),
      ),
    ).toBe("example-radio-3");
  });
});

describe("writing entries", () => {
  it("writes node, id, path, description, leaving out what is empty", () => {
    expect(
      buildUSBMappingEntry({ node: "pve-01", id: "1234:5678", path: "" }),
    ).toBe("node=pve-01,id=1234:5678");
    expect(
      buildUSBMappingEntry({
        node: "pve-01",
        id: "ABCD:EF01",
        path: "1-2.3",
        description: "left port",
      }),
    ).toBe("node=pve-01,id=abcd:ef01,path=1-2.3,description=left port");
  });

  it("reads an entry's own description, which may hold an equals sign", () => {
    expect(
      usbMappingEntryDescription("node=pve-01,id=1234:5678,description=a=b"),
    ).toBe("a=b");
    expect(usbMappingEntryDescription("node=pve-01,id=1234:5678")).toBe("");
  });

  it("counts entries per node, skipping an entry that names none", () => {
    const counts = usbMappingEntriesPerNode([
      "node=pve-01,id=1234:5678",
      "id=1234:5678,node=pve-01,path=1-2",
      "node=pve-02,id=1234:5678",
      "id=1234:5678",
    ]);
    expect([...counts.entries()]).toEqual([
      ["pve-01", 2],
      ["pve-02", 1],
    ]);
  });

  it("counts characters as Proxmox's maxLength does, not UTF-16 units", () => {
    expect(characterCount("abc")).toBe(3);
    expect(characterCount("\u{1F600}\u{1F600}")).toBe(2);
    expect(characterCount("é".repeat(4096))).toBe(4096);
  });
});

describe("pickedUSBDevice", () => {
  const devices: NodeUSBDevice[] = [
    {
      busnum: 1,
      devnum: 3,
      port: "0",
      vendid: "1234",
      prodid: "5678",
      product: "Example Serial Adapter",
      manufacturer: "",
      speed: "12",
      class: 0,
      usbpath: "2",
      level: 1,
    },
  ];

  it("picks by id, lowercased, on any port", () => {
    expect(pickedUSBDevice("device", "1234:5678", "", devices)).toEqual({
      deviceId: "1234:5678",
      path: "",
      label: "Example Serial Adapter",
    });
    // A typed id for a device the node does not list is still a pick.
    expect(pickedUSBDevice("device", "ABCD:EF01", "", devices)).toEqual({
      deviceId: "abcd:ef01",
      path: "",
      label: "USB abcd:ef01",
    });
    expect(pickedUSBDevice("device", "12345678", "", devices)).toBeNull();
  });

  it("picks a port only with a device listed on it", () => {
    expect(pickedUSBDevice("port", "", "1-2", devices)).toEqual({
      deviceId: "1234:5678",
      path: "1-2",
      label: "Example Serial Adapter",
    });
    expect(pickedUSBDevice("port", "", "1-9", devices)).toBeNull();
  });
});
