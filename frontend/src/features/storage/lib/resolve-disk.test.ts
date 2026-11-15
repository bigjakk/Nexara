import { describe, expect, it } from "vitest";
import { ctVolumeKey, vmVolumeKey } from "./resolve-disk";

// move_disk takes every bus's disks, efidisk0, tpmstate0 and unusedN, and
// refuses only the TPM state of a running VM; move_volume takes rootfs and
// mpN of a stopped container. The volume is matched whole, so disk-1 is not
// found in disk-10.
const VM = {
  scsi0: "store01:vm-100-disk-10,size=32G",
  efidisk0: "store01:vm-100-disk-1,efitype=4m,size=4M",
  tpmstate0: "store01:vm-100-disk-2,size=4M,version=v2.0",
  unused0: "store01:vm-100-disk-3",
};
const CT = {
  rootfs: "store01:subvol-200-disk-0,size=8G",
  mp0: "volume=store01:subvol-200-disk-1,mp=/srv,size=4G",
  unused0: "store01:subvol-200-disk-2",
};

describe("vmVolumeKey", () => {
  it.each([
    ["store01:vm-100-disk-10", false, "scsi0"],
    ["store01:vm-100-disk-1", true, "efidisk0"],
    ["store01:vm-100-disk-2", false, "tpmstate0"],
    ["store01:vm-100-disk-3", true, "unused0"],
    ["store01:vm-100-disk-2", true, null],
    ["store01:vm-100-disk-9", false, null],
  ])("%s, running %s → %s", (volid, running, key) => {
    const got = vmVolumeKey(VM, volid, running);
    expect(got.key).toBe(key);
    if (key === null) expect(got.reason).toBeTruthy();
  });
});

describe("ctVolumeKey", () => {
  it.each([
    ["store01:subvol-200-disk-0", false, "rootfs"],
    ["store01:subvol-200-disk-1", false, "mp0"],
    ["store01:subvol-200-disk-1", true, null],
    ["store01:subvol-200-disk-2", false, null],
  ])("%s, running %s → %s", (volid, running, key) => {
    const got = ctVolumeKey(CT, volid, running);
    expect(got.key).toBe(key);
    if (key === null) expect(got.reason).toBeTruthy();
  });
});
