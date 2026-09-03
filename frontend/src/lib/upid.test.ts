import { describe, expect, it } from "vitest";
import { upidVmid } from "./upid";

describe("upidVmid", () => {
  it("extracts the VMID from guest task UPIDs", () => {
    expect(
      upidVmid("UPID:pve1:0004F9DE:1A2B3C4D:65F1A2B3:qmstart:105:root@pam:"),
    ).toBe(105);
    expect(
      upidVmid("UPID:node-02:000A11FF:0B2C3D4E:66001122:vzdump:104:veeam@pve:"),
    ).toBe(104);
  });

  it("returns null for non-guest tasks with empty or non-numeric ids", () => {
    expect(
      upidVmid(
        "UPID:pve1:0004F9DE:1A2B3C4D:65F1A2B3:srvreload:networking:root@pam:",
      ),
    ).toBeNull();
    expect(
      upidVmid("UPID:pve1:0004F9DE:1A2B3C4D:65F1A2B3:aptupdate::root@pam:"),
    ).toBeNull();
  });

  it("returns null for malformed strings", () => {
    expect(upidVmid("")).toBeNull();
    expect(upidVmid("not-a-upid")).toBeNull();
    expect(upidVmid("UPID:pve1:only:a:few")).toBeNull();
  });
});
