import { describe, it, expect } from "vitest";
import {
  parseKVString,
  buildKVString,
  parseNet0,
  buildNet0,
  parseAgent,
  buildAgent,
  parseVGA,
  buildVGA,
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

describe("parseNet0 / buildNet0", () => {
  it("parses full net0 string with MAC", () => {
    const raw =
      "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0,firewall=1,tag=100,rate=10,mtu=1500,queues=4";
    const parsed = parseNet0(raw);
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
    const parsed = parseNet0("e1000,bridge=vmbr1");
    expect(parsed.model).toBe("e1000");
    expect(parsed.mac).toBe("");
    expect(parsed.bridge).toBe("vmbr1");
  });

  it("handles empty string", () => {
    const parsed = parseNet0("");
    expect(parsed.model).toBe("virtio");
    expect(parsed.mac).toBe("");
  });

  it("round-trips with MAC", () => {
    const original = "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0,firewall=1";
    const parsed = parseNet0(original);
    const rebuilt = buildNet0(parsed);
    expect(rebuilt).toBe(original);
  });

  it("round-trips without MAC", () => {
    const original = "e1000,bridge=vmbr0";
    const parsed = parseNet0(original);
    const rebuilt = buildNet0(parsed);
    expect(rebuilt).toBe(original);
  });

  it("builds with only model", () => {
    expect(
      buildNet0({
        model: "virtio",
        mac: "",
        bridge: "",
        firewall: false,
        vlanTag: "",
        rateLimit: "",
        mtu: "",
        multiqueue: "",
      }),
    ).toBe("virtio");
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

  it("parses empty string", () => {
    expect(parseVGA("")).toEqual({ type: "std", memory: "" });
  });

  it("builds simple type", () => {
    expect(buildVGA({ type: "virtio", memory: "" })).toBe("virtio");
  });

  it("builds type with memory", () => {
    expect(buildVGA({ type: "qxl", memory: "64" })).toBe("qxl,memory=64");
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
