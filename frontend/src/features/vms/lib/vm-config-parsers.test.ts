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
    const raw = "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0,firewall=1,tag=100,rate=10,mtu=1500,queues=4";
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
    expect(buildNet0({ model: "virtio", mac: "", bridge: "", firewall: false, vlanTag: "", rateLimit: "", mtu: "", multiqueue: "" })).toBe("virtio");
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
    expect(buildAgent({ enabled: true, fstrimClonedDisks: true })).toBe("enabled=1,fstrim_cloned_disks=1");
  });

  it("builds enabled without fstrim", () => {
    expect(buildAgent({ enabled: true, fstrimClonedDisks: false })).toBe("enabled=1");
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
    expect(parseBootOrder("order=scsi0;ide2;net0")).toEqual(["scsi0", "ide2", "net0"]);
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
    const d = parseDisk("local-lvm:vm-100-disk-0,size=32G,format=qcow2,cache=none,discard=on,ssd=1,iothread=1");
    expect(d.storage).toBe("local-lvm");
    expect(d.volume).toBe("local-lvm:vm-100-disk-0");
    expect(d.size).toBe("32G");
    expect(d.format).toBe("qcow2");
    expect(d.cache).toBe("none");
    expect(d.discard).toBe(true);
    expect(d.ssd).toBe(true);
    expect(d.iothread).toBe(true);
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
