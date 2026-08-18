import { describe, it, expect } from "vitest";
import { parseBwlimit, resolveDiskFormat } from "./disk-move";

describe("parseBwlimit", () => {
  it.each([
    ["", 0, false],
    ["0", 0, false],
    ["51200", 51200, false],
  ])("accepts %j", (text, value, invalid) => {
    expect(parseBwlimit(text)).toEqual({ value, invalid });
  });

  it.each(["-5", "1.5", "abc"])("rejects %j", (text) => {
    expect(parseBwlimit(text).invalid).toBe(true);
  });
});

describe("resolveDiskFormat", () => {
  it("sends nothing for block-backed targets", () => {
    // LVM/ZFS/RBD only hold raw; a format there is an error.
    expect(resolveDiskFormat("qcow2", "qcow2", "lvmthin")).toBe("");
    expect(resolveDiskFormat(null, "qcow2", "rbd")).toBe("");
  });

  it("sends nothing when no target is chosen yet", () => {
    expect(resolveDiskFormat(null, "qcow2", undefined)).toBe("");
  });

  it("preserves the source format when the field is untouched", () => {
    // Sending nothing would let PVE allocate in the storage default.
    expect(resolveDiskFormat(null, "qcow2", "nfs")).toBe("qcow2");
    expect(resolveDiskFormat(null, "raw", "dir")).toBe("raw");
  });

  it("honours an explicit choice, including the storage default", () => {
    expect(resolveDiskFormat("vmdk", "qcow2", "nfs")).toBe("vmdk");
    expect(resolveDiskFormat("", "qcow2", "nfs")).toBe("");
  });

  it("falls back to the storage default for an unknown source format", () => {
    expect(resolveDiskFormat(null, "subvol", "dir")).toBe("");
    expect(resolveDiskFormat(null, undefined, "dir")).toBe("");
  });
});
