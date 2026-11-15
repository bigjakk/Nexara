import { describe, it, expect } from "vitest";
import { ApiClientError } from "@/lib/api-client";
import { reportsNoCeph } from "./ceph-absent";

const refused = (status: number, message: string) =>
  new ApiClientError(status, { error: "proxmox_error", message });

describe("reportsNoCeph", () => {
  it.each([
    [
      "no ceph-mon binary",
      refused(502, "binary not installed: /usr/bin/ceph-mon"),
      true,
    ],
    [
      "not initialised",
      refused(
        502,
        "pveceph configuration not initialized - missing '/etc/pve/ceph.conf'",
      ),
      true,
    ],
    ["unreachable", refused(502, "Failed to connect to Proxmox"), false],
    ["refused", refused(403, "Insufficient permissions"), false],
    [
      "not an API answer",
      new Error("binary not installed: /usr/bin/ceph-mon"),
      false,
    ],
    ["no error", null, false],
  ])("%s", (_name, error, want) => {
    expect(reportsNoCeph(error)).toBe(want);
  });
});
