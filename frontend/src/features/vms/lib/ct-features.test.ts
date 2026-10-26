import { describe, it, expect } from "vitest";
import { parseFeatures, buildFeatures } from "./ct-features";

describe("parseFeatures / buildFeatures", () => {
  // Proxmox stores the value as sent: any order, booleans in any spelling
  // parse_boolean takes, and keys the form has no field for.
  const STORED = "keyctl=on,nesting=1,force_rw_sys=1,mount=nfs;cifs";

  it("reads every spelling of a flag, and the mount list", () => {
    expect(parseFeatures(STORED)).toEqual({
      nesting: true,
      fuse: false,
      keyctl: true,
      mknod: false,
      mount: "nfs;cifs",
    });
    expect(parseFeatures("fuse=yes,mknod=0")).toMatchObject({
      fuse: true,
      mknod: false,
    });
  });

  // Proxmox's permission check compares mount as written, spaces and all:
  // trimmed, it reads as a change only root@pam may make.
  it.each(["", STORED, "mount= nfs;cifs ,nesting=1"])(
    "rebuilds an untouched value %j to exactly what is stored",
    (raw) => {
      expect(buildFeatures(parseFeatures(raw), raw)).toBe(raw);
    },
  );

  it.each([
    [
      "adds a flag after the rest, keeping force_rw_sys",
      STORED,
      { fuse: true },
      "keyctl=on,nesting=1,force_rw_sys=1,mount=nfs;cifs,fuse=1",
    ],
    [
      "drops a flag turned off, and nothing else",
      STORED,
      { keyctl: false },
      "nesting=1,force_rw_sys=1,mount=nfs;cifs",
    ],
    [
      "turns on a flag spelled 0 where it stands",
      "mknod=0,nesting=1",
      { mknod: true },
      "mknod=1,nesting=1",
    ],
    [
      "clears the mount list",
      STORED,
      { mount: "" },
      "keyctl=on,nesting=1,force_rw_sys=1",
    ],
    [
      "writes a new value in the form's order",
      "",
      { nesting: true, keyctl: true, mount: "nfs" },
      "nesting=1,keyctl=1,mount=nfs",
    ],
  ])("%s", (_name, raw, edit, want) => {
    expect(buildFeatures({ ...parseFeatures(raw), ...edit }, raw)).toBe(want);
  });
});
