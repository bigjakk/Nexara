import { describe, expect, it } from "vitest";
import {
  diskFormatFor,
  diskFormatToSend,
  diskFormatsForStorageType,
} from "./vm-config-constants";

// Offering qcow2 on an RBD store showed a disk "(qcow2)" that came out raw.
describe("diskFormatsForStorageType", () => {
  it.each([
    ["rbd", ["raw"]],
    ["lvmthin", ["raw"]],
    ["nfs", ["qcow2", "raw", "vmdk"]],
    [undefined, ["qcow2", "raw", "vmdk"]],
  ])("a %s store takes %j", (type, want) => {
    expect(diskFormatsForStorageType(type)).toEqual(want);
  });
});

// A file store makes a disk raw when no format is sent; thin LVM refuses
// qcow2, so a raw-only store is left to its own default.
describe("diskFormatFor and diskFormatToSend", () => {
  it.each([
    ["nfs", "qcow2", "qcow2", "qcow2"],
    ["nfs", "vmdk", "vmdk", "vmdk"],
    ["lvmthin", "qcow2", "raw", undefined],
    [undefined, "qcow2", "qcow2", undefined],
  ])(
    "a %s store shows a %s choice as %s and sends %s",
    (type, chosen, shown, sent) => {
      expect(diskFormatFor(type, chosen)).toBe(shown);
      expect(diskFormatToSend(type, chosen)).toBe(sent);
    },
  );
});
