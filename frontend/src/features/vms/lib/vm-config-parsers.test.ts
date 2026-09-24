import { describe, it, expect } from "vitest";
import {
  parseKVString,
  buildKVString,
  parseNet,
  buildNet,
  parseAgent,
  buildAgent,
  parseVGA,
  buildVGA,
  parseAudio,
  buildAudio,
  parseBootOrder,
  buildBootOrder,
  parseDisk,
  parseCPU,
  buildCPU,
  parseWatchdog,
  buildWatchdog,
  parseSMBIOS,
  buildSMBIOS,
} from "./vm-config-parsers";

describe("parseKVString / buildKVString", () => {
  it("parses empty string", () => {
    expect(parseKVString("").size).toBe(0);
  });

  it("parses key=value pairs", () => {
    const m = parseKVString("bridge=vmbr0,firewall=1");
    expect(m.get("bridge")).toBe("vmbr0");
    expect(m.get("firewall")).toBe("1");
  });

  it("handles bare keys", () => {
    const m = parseKVString("virtio,bridge=vmbr0");
    expect(m.get("virtio")).toBe("");
    expect(m.get("bridge")).toBe("vmbr0");
  });

  it("round-trips correctly", () => {
    const m = new Map<string, string>([
      ["bridge", "vmbr0"],
      ["firewall", "1"],
    ]);
    expect(buildKVString(m)).toBe("bridge=vmbr0,firewall=1");
  });
});

describe("parseNet / buildNet", () => {
  it("parses full net0 string with MAC", () => {
    const raw =
      "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0,firewall=1,tag=100,rate=10,mtu=1500,queues=4";
    const parsed = parseNet(raw);
    expect(parsed.model).toBe("virtio");
    expect(parsed.mac).toBe("AA:BB:CC:DD:EE:FF");
    expect(parsed.bridge).toBe("vmbr0");
    expect(parsed.firewall).toBe(true);
    expect(parsed.vlanTag).toBe("100");
    expect(parsed.rateLimit).toBe("10");
    expect(parsed.mtu).toBe("1500");
    expect(parsed.multiqueue).toBe("4");
  });

  it("parses net0 without MAC", () => {
    const parsed = parseNet("e1000,bridge=vmbr1");
    expect(parsed.model).toBe("e1000");
    expect(parsed.mac).toBe("");
    expect(parsed.bridge).toBe("vmbr1");
  });

  it("handles empty string", () => {
    const parsed = parseNet("");
    expect(parsed.model).toBe("virtio");
    expect(parsed.mac).toBe("");
  });

  it("round-trips with MAC", () => {
    const original = "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0,firewall=1";
    const parsed = parseNet(original);
    const rebuilt = buildNet(parsed);
    expect(rebuilt).toBe(original);
  });

  it("round-trips without MAC", () => {
    const original = "e1000,bridge=vmbr0";
    const parsed = parseNet(original);
    const rebuilt = buildNet(parsed);
    expect(rebuilt).toBe(original);
  });

  it("builds with only model", () => {
    expect(
      buildNet({
        model: "virtio",
        mac: "",
        bridge: "",
        firewall: false,
        vlanTag: "",
        rateLimit: "",
        mtu: "",
        multiqueue: "",
        linkDown: false,
      }),
    ).toBe("virtio");
  });

  it("builds a new NIC's options in the order it always has", () => {
    expect(
      buildNet({
        model: "e1000",
        mac: "",
        bridge: "vmbr0",
        firewall: true,
        vlanTag: "10",
        rateLimit: "5",
        mtu: "1400",
        multiqueue: "2",
        linkDown: true,
      }),
    ).toBe(
      "e1000,bridge=vmbr0,firewall=1,tag=10,rate=5,mtu=1400,queues=2,link_down=1",
    );
  });

  // Out of print_net's sorted order on purpose, and carrying options the
  // editor has no field for — trunks, and a key newer than this code — so a
  // builder that re-derives the string from its fields, or re-sorts it,
  // cannot reproduce it.
  const STORED =
    "virtio=02:00:00:00:00:01,bridge=vmbr0,queues=4,trunks=10;20;30,mtu=1500,rate=12.5,link_down=1,tag=5,future-opt=abc";

  it("reads every field of a NIC that carries options it has no field for", () => {
    expect(parseNet(STORED)).toEqual({
      model: "virtio",
      mac: "02:00:00:00:00:01",
      bridge: "vmbr0",
      firewall: false,
      vlanTag: "5",
      rateLimit: "12.5",
      mtu: "1500",
      multiqueue: "4",
      linkDown: true,
    });
  });

  it("reads model= and macaddr= spelled out, not macaddr as the model", () => {
    const parsed = parseNet(
      "model=e1000,macaddr=02:00:00:00:00:05,bridge=vmbr0",
    );
    expect(parsed.model).toBe("e1000");
    expect(parsed.mac).toBe("02:00:00:00:00:05");
    expect(parsed.bridge).toBe("vmbr0");
  });

  it.each([
    ["1", true],
    ["on", true],
    ["YES", true],
    ["True", true],
    ["0", false],
    ["off", false],
    ["no", false],
    ["FALSE", false],
  ])(
    "reads a firewall or link_down of %s as %s, as pve-common's parse_boolean does",
    (spelled, want) => {
      expect(parseNet(`virtio,firewall=${spelled}`).firewall).toBe(want);
      expect(parseNet(`virtio,link_down=${spelled}`).linkDown).toBe(want);
    },
  );

  it.each([
    ["out of print_net's order, with trunks and an unknown key", STORED],
    [
      "in print_net's own order",
      "virtio=02:00:00:00:00:02,bridge=vmbr0,firewall=1,link_down=1,mtu=1500,queues=4,rate=12.5,tag=5,trunks=10;20;30",
    ],
    [
      "with booleans spelled on and yes",
      "e1000=02:00:00:00:00:03,bridge=vmbr1,firewall=on,link_down=yes",
    ],
    [
      "with an explicit firewall=0",
      "virtio=02:00:00:00:00:04,bridge=vmbr0,firewall=0",
    ],
    [
      "with model= and macaddr= spelled out",
      "model=e1000,macaddr=02:00:00:00:00:05,bridge=vmbr0",
    ],
    ["with host-tunnel", "virtio=02:00:00:00:00:06,bridge=vmbr0,host-tunnel=1"],
    ["with no bridge", "virtio=02:00:00:00:00:07"],
    [
      "with no model, which Proxmox itself refuses",
      "bridge=vmbr0,macaddr=02:00:00:00:00:08",
    ],
  ])("rebuilds an untouched NIC %s to exactly what is stored", (_name, raw) => {
    expect(buildNet(parseNet(raw), raw)).toBe(raw);
  });

  it("rewrites only the edited option, where it stands", () => {
    expect(buildNet({ ...parseNet(STORED), bridge: "vmbr1" }, STORED)).toBe(
      "virtio=02:00:00:00:00:01,bridge=vmbr1,queues=4,trunks=10;20;30,mtu=1500,rate=12.5,link_down=1,tag=5,future-opt=abc",
    );
  });

  it("removes cleared options and nothing else", () => {
    expect(
      buildNet({ ...parseNet(STORED), vlanTag: "", linkDown: false }, STORED),
    ).toBe(
      "virtio=02:00:00:00:00:01,bridge=vmbr0,queues=4,trunks=10;20;30,mtu=1500,rate=12.5,future-opt=abc",
    );
  });

  it("appends options the NIC did not have", () => {
    const raw = "virtio=02:00:00:00:00:02,bridge=vmbr0,trunks=10";
    expect(
      buildNet({ ...parseNet(raw), firewall: true, mtu: "9000" }, raw),
    ).toBe(
      "virtio=02:00:00:00:00:02,bridge=vmbr0,trunks=10,firewall=1,mtu=9000",
    );
  });

  it("turns an explicit firewall=0 on where it stands", () => {
    const raw = "virtio=02:00:00:00:00:04,firewall=0,bridge=vmbr0";
    expect(buildNet({ ...parseNet(raw), firewall: true }, raw)).toBe(
      "virtio=02:00:00:00:00:04,firewall=1,bridge=vmbr0",
    );
  });

  it.each([
    [
      "the model=MAC shorthand",
      "virtio=02:00:00:00:00:01,bridge=vmbr0",
      "e1000=02:00:00:00:00:01,bridge=vmbr0",
    ],
    [
      "model= and macaddr=",
      "model=virtio,macaddr=02:00:00:00:00:05,bridge=vmbr0",
      "model=e1000,macaddr=02:00:00:00:00:05,bridge=vmbr0",
    ],
    ["a bare model", "bridge=vmbr0,virtio", "bridge=vmbr0,e1000"],
    // A blank segment is not the model: taking it for one would put the new
    // model there and drop the shorthand, and the MAC with it.
    [
      "the shorthand after a blank segment",
      ",virtio=02:00:00:00:00:01,bridge=vmbr0",
      ",e1000=02:00:00:00:00:01,bridge=vmbr0",
    ],
  ])(
    "changes the model written as %s, keeping the MAC and the form",
    (_name, raw, want) => {
      expect(buildNet({ ...parseNet(raw), model: "e1000" }, raw)).toBe(want);
    },
  );

  // A second MAC, as model=MAC beside macaddr=, is a duplicate key to
  // parse_property_string: the model goes in bare.
  it("gives a NIC stored with no model the one picked, leaving its macaddr= alone", () => {
    const raw = "bridge=vmbr0,macaddr=02:00:00:00:00:08";
    expect(buildNet({ ...parseNet(raw), model: "e1000" }, raw)).toBe(
      "e1000,bridge=vmbr0,macaddr=02:00:00:00:00:08",
    );
  });

  // Hand-edited configs can carry them, and Proxmox accepts them.
  it("skips blank segments in a NIC, as parse_property_string does", () => {
    expect(parseNet(",virtio=02:00:00:00:00:01,bridge=vmbr0,")).toEqual({
      model: "virtio",
      mac: "02:00:00:00:00:01",
      bridge: "vmbr0",
      firewall: false,
      vlanTag: "",
      rateLimit: "",
      mtu: "",
      multiqueue: "",
      linkDown: false,
    });
  });

  // The Hardware panel has no control that changes a MAC; these pin the path
  // one would take.
  it.each([
    [
      "rewrites the MAC inside the model=MAC shorthand",
      "virtio=02:00:00:00:00:01,bridge=vmbr0",
      "02:00:00:00:00:09",
      "virtio=02:00:00:00:00:09,bridge=vmbr0",
    ],
    [
      "leaves the model bare when the shorthand's MAC is cleared",
      "virtio=02:00:00:00:00:01,bridge=vmbr0",
      "",
      "virtio,bridge=vmbr0",
    ],
    [
      "rewrites macaddr= where it stands",
      "model=virtio,macaddr=02:00:00:00:00:05,bridge=vmbr0",
      "02:00:00:00:00:09",
      "model=virtio,macaddr=02:00:00:00:00:09,bridge=vmbr0",
    ],
    [
      "removes macaddr= when the MAC is cleared",
      "model=virtio,macaddr=02:00:00:00:00:05,bridge=vmbr0",
      "",
      "model=virtio,bridge=vmbr0",
    ],
    [
      "adds macaddr= beside a bare model",
      "virtio,bridge=vmbr0",
      "02:00:00:00:00:09",
      "virtio,bridge=vmbr0,macaddr=02:00:00:00:00:09",
    ],
  ])("%s", (_name, raw, mac, want) => {
    expect(buildNet({ ...parseNet(raw), mac }, raw)).toBe(want);
  });
});

describe("parseAgent / buildAgent", () => {
  it("parses '1'", () => {
    const a = parseAgent("1");
    expect(a.enabled).toBe(true);
    expect(a.fstrimClonedDisks).toBe(false);
  });

  it("parses '0'", () => {
    const a = parseAgent("0");
    expect(a.enabled).toBe(false);
  });

  it("parses kv format", () => {
    const a = parseAgent("enabled=1,fstrim_cloned_disks=1");
    expect(a.enabled).toBe(true);
    expect(a.fstrimClonedDisks).toBe(true);
  });

  it("parses empty string", () => {
    const a = parseAgent("");
    expect(a.enabled).toBe(false);
  });

  it("builds enabled with fstrim", () => {
    expect(buildAgent({ enabled: true, fstrimClonedDisks: true })).toBe(
      "enabled=1,fstrim_cloned_disks=1",
    );
  });

  it("builds enabled without fstrim", () => {
    expect(buildAgent({ enabled: true, fstrimClonedDisks: false })).toBe(
      "enabled=1",
    );
  });

  it("builds disabled", () => {
    expect(buildAgent({ enabled: false, fstrimClonedDisks: false })).toBe("0");
  });
});

describe("parseVGA / buildVGA", () => {
  it("parses simple type", () => {
    expect(parseVGA("std")).toEqual({ type: "std", memory: "" });
  });

  it("parses type with memory", () => {
    expect(parseVGA("qxl,memory=64")).toEqual({ type: "qxl", memory: "64" });
  });

  it("reads no vga line as no type — Proxmox's default, which is not always std", () => {
    expect(parseVGA("")).toEqual({ type: "", memory: "" });
  });

  it("reads a value with no type as no type", () => {
    expect(parseVGA("memory=32")).toEqual({ type: "", memory: "32" });
  });

  it("reads the type= spelling", () => {
    expect(parseVGA("type=qxl,memory=64")).toEqual({
      type: "qxl",
      memory: "64",
    });
  });

  it("skips blank segments in vga, as parse_property_string does", () => {
    expect(parseVGA(",qxl,memory=64,")).toEqual({ type: "qxl", memory: "64" });
  });

  it("builds simple type", () => {
    expect(buildVGA({ type: "virtio", memory: "" })).toBe("virtio");
  });

  it("builds type with memory", () => {
    expect(buildVGA({ type: "qxl", memory: "64" })).toBe("qxl,memory=64");
  });

  it.each([
    "",
    "std",
    "qxl,memory=64",
    "std,clipboard=vnc",
    "memory=32",
    "type=virtio",
    "qxl2",
  ])("rebuilds an untouched vga %j to exactly what is stored", (raw) => {
    expect(buildVGA(parseVGA(raw), raw)).toBe(raw);
  });

  it.each([
    [
      "keeps clipboard when the type changes",
      "std,clipboard=vnc",
      "qxl",
      "",
      "qxl,clipboard=vnc",
    ],
    ["sets a type where there was no vga line", "", "qxl", "", "qxl"],
    ["puts a new type first", "memory=32", "std", "32", "std,memory=32"],
    ["removes the line when set back to the default", "qxl", "", "", ""],
    [
      "keeps the rest when set back to the default",
      "qxl,memory=64",
      "",
      "64",
      "memory=64",
    ],
    [
      "keeps the type= spelling",
      "type=std,memory=16",
      "qxl",
      "16",
      "type=qxl,memory=16",
    ],
    [
      "changes memory where it stands",
      "qxl,memory=64,clipboard=vnc",
      "qxl",
      "128",
      "qxl,memory=128,clipboard=vnc",
    ],
    // Not "qxl,memory=": Proxmox refuses a key with no value ("missing key in
    // comma-separated list property").
    ["removes memory when it is cleared", "qxl,memory=64", "qxl", "", "qxl"],
    // A blank segment is not the type, as it is not a NIC's model.
    [
      "changes the type after a blank segment",
      ",std,memory=16",
      "qxl",
      "16",
      ",qxl,memory=16",
    ],
  ])("%s", (_name, raw, type, memory, want) => {
    expect(buildVGA({ type, memory }, raw)).toBe(want);
  });
});

describe("parseAudio / buildAudio", () => {
  it.each([
    "device=ich9-intel-hda,driver=spice",
    "device=ich9-intel-hda,driver=none",
    "device=AC97",
    "driver=spice,device=intel-hda",
  ])("rebuilds an untouched audio0 %j to exactly what is stored", (raw) => {
    expect(buildAudio(parseAudio(raw), raw)).toBe(raw);
  });

  it("keeps the driver when the device changes", () => {
    const raw = "device=ich9-intel-hda,driver=none";
    expect(buildAudio({ ...parseAudio(raw), device: "AC97" }, raw)).toBe(
      "device=AC97,driver=none",
    );
  });

  it("removes audio0 when the device is cleared", () => {
    const raw = "device=AC97,driver=none";
    expect(buildAudio({ ...parseAudio(raw), device: "" }, raw)).toBe("");
  });

  it("builds a new device with its driver spelled out, as it always has", () => {
    expect(buildAudio({ device: "AC97", driver: "spice" })).toBe(
      "device=AC97,driver=spice",
    );
  });

  // The Hardware panel has no driver control; these pin the path one would
  // take.
  it.each([
    [
      "rewrites the driver where it stands",
      "driver=spice,device=AC97",
      "none",
      "driver=none,device=AC97",
    ],
    [
      "adds a driver the value did not spell out",
      "device=AC97",
      "none",
      "device=AC97,driver=none",
    ],
    [
      "removes the driver when it is cleared",
      "driver=none,device=AC97",
      "",
      "device=AC97",
    ],
  ])("%s", (_name, raw, driver, want) => {
    expect(buildAudio({ ...parseAudio(raw), driver }, raw)).toBe(want);
  });
});

describe("parseBootOrder / buildBootOrder", () => {
  it("parses boot order", () => {
    expect(parseBootOrder("order=scsi0;ide2;net0")).toEqual([
      "scsi0",
      "ide2",
      "net0",
    ]);
  });

  it("handles empty string", () => {
    expect(parseBootOrder("")).toEqual([]);
  });

  it("handles raw format without order= prefix", () => {
    expect(parseBootOrder("scsi0;net0")).toEqual(["scsi0", "net0"]);
  });

  it("builds boot order", () => {
    expect(buildBootOrder(["scsi0", "ide2"])).toBe("order=scsi0;ide2");
  });

  it("builds empty", () => {
    expect(buildBootOrder([])).toBe("");
  });
});

describe("parseDisk", () => {
  it("parses full disk string", () => {
    const d = parseDisk(
      "local-lvm:vm-100-disk-0,size=32G,format=qcow2,cache=none,discard=on,ssd=1,iothread=1",
    );
    expect(d.storage).toBe("local-lvm");
    expect(d.volume).toBe("local-lvm:vm-100-disk-0");
    expect(d.size).toBe("32G");
    expect(d.format).toBe("qcow2");
    expect(d.cache).toBe("none");
    expect(d.discard).toBe(true);
    expect(d.ssd).toBe(true);
    expect(d.iothread).toBe(true);
  });

  it("derives the format from the volume extension when format= is absent", () => {
    // How Proxmox actually writes file-based disks — the extension is the only
    // record of the image format.
    expect(parseDisk("nas:121/vm-121-disk-0.qcow2,size=81G").format).toBe(
      "qcow2",
    );
    expect(parseDisk("local:100/vm-100-disk-0.raw,size=32G").format).toBe(
      "raw",
    );
    expect(parseDisk("nfs:100/vm-100-disk-1.vmdk,size=10G").format).toBe(
      "vmdk",
    );
  });

  it("reports no format for block-backed volumes", () => {
    // LVM/ZFS/RBD volumes have no extension and are raw by definition.
    expect(parseDisk("local-lvm:vm-100-disk-0,size=32G").format).toBe("");
    expect(parseDisk("test:vm-121-disk-0,size=81G").format).toBe("");
  });

  it("lets an explicit format= win over the extension", () => {
    expect(
      parseDisk("local:100/vm-100-disk-0.raw,format=qcow2,size=32G").format,
    ).toBe("qcow2");
  });

  it("parses minimal disk string", () => {
    const d = parseDisk("local-lvm:vm-100-disk-0,size=10G");
    expect(d.storage).toBe("local-lvm");
    expect(d.size).toBe("10G");
    expect(d.discard).toBe(false);
  });

  it("handles empty string", () => {
    const d = parseDisk("");
    expect(d.storage).toBe("");
  });

  it("parses cdrom entry", () => {
    const d = parseDisk("none,media=cdrom");
    expect(d.volume).toBe("none");
    expect(d.storage).toBe("");
  });
});

describe("parseCPU / buildCPU", () => {
  it("parses a bare model", () => {
    const c = parseCPU("host");
    expect(c.model).toBe("host");
    expect(c.flags).toEqual({});
    expect(c.extra.size).toBe(0);
  });

  it("parses the cputype= form to the same shape as the bare form", () => {
    expect(parseCPU("cputype=host").model).toBe("host");
  });

  it("parses +/- flags", () => {
    const c = parseCPU("host,flags=+nested-virt;-pcid;+aes");
    expect(c.flags).toEqual({ "nested-virt": "on", pcid: "off", aes: "on" });
  });

  it("keeps options it has no UI for", () => {
    const c = parseCPU("host,flags=+aes,hidden=1,phys-bits=host");
    expect(c.extra.get("hidden")).toBe("1");
    expect(c.extra.get("phys-bits")).toBe("host");
  });

  it("treats an empty field as no model", () => {
    expect(parseCPU("").model).toBe("");
    expect(buildCPU(parseCPU(""))).toBe("");
  });

  it("round-trips a model with flags and extras without dropping either", () => {
    const raw = "host,flags=+aes;+nested-virt,hidden=1";
    expect(buildCPU(parseCPU(raw))).toBe(raw);
  });

  it("emits flags in a stable order so an untouched config is not dirty", () => {
    const a = buildCPU(parseCPU("host,flags=+nested-virt;+aes"));
    const b = buildCPU(parseCPU("host,flags=+aes;+nested-virt"));
    expect(a).toBe(b);
  });

  it("changing the model preserves the flags (the drop bug)", () => {
    const c = parseCPU("x86-64-v2-AES,flags=+nested-virt");
    c.model = "host";
    expect(buildCPU(c)).toBe("host,flags=+nested-virt");
  });

  it("a VM with no cpu line normalises the same on both sides of a diff", () => {
    // Regression: the panel defaulted the model when loading but not when
    // building the comparison baseline, so every VM without a `cpu:` line
    // looked dirty at mount and any save rewrote its CPU model — Proxmox's
    // real default for an absent cpu is kvm64, not this placeholder.
    const DEFAULT = "x86-64-v2-AES";
    const normalize = (raw: string) => {
      const p = parseCPU(raw);
      return buildCPU({ ...p, model: p.model || DEFAULT });
    };
    const loaded = parseCPU("");
    const asDisplayed = buildCPU({
      model: loaded.model || DEFAULT,
      flags: loaded.flags,
      extra: loaded.extra,
    });
    expect(normalize("")).toBe(asDisplayed);
    expect(normalize(DEFAULT)).toBe(asDisplayed);
  });

  it("drops the flags= segment entirely when no flag is set", () => {
    const c = parseCPU("host,flags=+aes");
    c.flags = {};
    expect(buildCPU(c)).toBe("host");
  });
});

describe("parseWatchdog / buildWatchdog", () => {
  it("treats an empty field as no watchdog device", () => {
    expect(parseWatchdog("")).toEqual({ model: "", action: "" });
    expect(buildWatchdog({ model: "", action: "" })).toBe("");
  });

  it("parses the bare default_key model form", () => {
    expect(parseWatchdog("i6300esb")).toEqual({
      model: "i6300esb",
      action: "",
    });
  });

  it("parses model and action", () => {
    expect(parseWatchdog("model=i6300esb,action=reset")).toEqual({
      model: "i6300esb",
      action: "reset",
    });
  });

  it("treats a model-less watchdog as PVE's default device, not as none", () => {
    // `watchdog: action=reset` is legal — PVE's model is optional and
    // defaults to i6300esb. Reporting model "" would claim the VM has no
    // watchdog and leave the action stranded behind a disabled control.
    expect(parseWatchdog("action=reset")).toEqual({
      model: "i6300esb",
      action: "reset",
    });
  });

  it("normalises a model-less watchdog to the same string on both sides", () => {
    // The panel compares buildWatchdog(state) against
    // buildWatchdog(parseWatchdog(original)), so normalising must not make an
    // untouched VM look dirty.
    const raw = "action=reset";
    const normalized = buildWatchdog(parseWatchdog(raw));
    expect(normalized).toBe("model=i6300esb,action=reset");
    expect(buildWatchdog(parseWatchdog(normalized))).toBe(normalized);
  });

  it("normalises the bare form to model= on build", () => {
    expect(buildWatchdog(parseWatchdog("i6300esb"))).toBe("model=i6300esb");
  });

  it("drops the action when none is set", () => {
    expect(buildWatchdog({ model: "ib700", action: "" })).toBe("model=ib700");
  });

  it("removes the device when the model is cleared, even with an action", () => {
    expect(buildWatchdog({ model: "", action: "reset" })).toBe("");
  });
});

describe("parseSMBIOS / buildSMBIOS", () => {
  it("treats an empty field as empty values", () => {
    const p = parseSMBIOS("");
    expect(p.uuid).toBe("");
    expect(p.values.manufacturer).toBe("");
    expect(buildSMBIOS(p)).toBe("");
  });

  it("decodes text fields only when base64=1 is set", () => {
    // "RGVsbA==" is "Dell"
    const encoded = parseSMBIOS("manufacturer=RGVsbA==,base64=1");
    expect(encoded.values.manufacturer).toBe("Dell");
  });

  it("leaves plain values alone when base64 is absent", () => {
    // Without the flag this is literal text that merely looks like base64 —
    // decoding it would turn a real manufacturer name into mojibake.
    const plain = parseSMBIOS("manufacturer=Dell");
    expect(plain.values.manufacturer).toBe("Dell");
  });

  it("keeps the uuid unencoded", () => {
    const u = "00000000-0000-4000-8000-000000000001";
    const p = parseSMBIOS(`uuid=${u},base64=1`);
    expect(p.uuid).toBe(u);
    expect(buildSMBIOS(p)).toBe(`uuid=${u}`);
  });

  it("always encodes text on write and flags it", () => {
    const p = parseSMBIOS("");
    p.values.manufacturer = "Dell";
    expect(buildSMBIOS(p)).toBe("manufacturer=RGVsbA==,base64=1");
  });

  it("encodes values Proxmox's plain grammar could not carry", () => {
    const p = parseSMBIOS("");
    p.values.product = "PowerEdge R740, Gen 2";
    const built = buildSMBIOS(p);
    expect(built).toContain("base64=1");
    // The comma must survive inside the encoded value, not split the string.
    expect(parseSMBIOS(built).values.product).toBe("PowerEdge R740, Gen 2");
  });

  it("round-trips non-ASCII text", () => {
    const p = parseSMBIOS("");
    p.values.family = "Bäckerei";
    expect(parseSMBIOS(buildSMBIOS(p)).values.family).toBe("Bäckerei");
  });

  it("omits base64=1 when only a uuid is set", () => {
    const p = parseSMBIOS("");
    p.uuid = "00000000-0000-4000-8000-000000000001";
    expect(buildSMBIOS(p)).not.toContain("base64");
  });
});
